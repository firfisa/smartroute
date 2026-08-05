package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"sync"
	"time"

	"github.com/firfisa/smartroute/internal/learning"
	"github.com/firfisa/smartroute/internal/model"
)

// DurablePolicyIndex is a bounded process-local snapshot. Lookup performs no
// database I/O and never stores a cleartext target identity.
type DurablePolicyIndex struct {
	mu       sync.RWMutex
	innerPad [sha256.BlockSize]byte
	outerPad [sha256.BlockSize]byte
	policies map[[durablePolicyKeyBytes]byte]DurablePolicy
	capacity int
}

type DurablePolicyIndexStats struct {
	Entries  int `json:"entries"`
	Capacity int `json:"capacity"`
}

func NewDurablePolicyIndex(secret []byte, policies []DurablePolicy, capacity int) (*DurablePolicyIndex, error) {
	if len(secret) != 32 {
		return nil, ErrCorrupt
	}
	if capacity < 1 || len(policies) > capacity {
		return nil, ErrCorrupt
	}
	index := &DurablePolicyIndex{policies: make(map[[durablePolicyKeyBytes]byte]DurablePolicy, len(policies)), capacity: capacity}
	for offset := range index.innerPad {
		var keyByte byte
		if offset < len(secret) {
			keyByte = secret[offset]
		}
		index.innerPad[offset] = keyByte ^ 0x36
		index.outerPad[offset] = keyByte ^ 0x5c
	}
	for _, policy := range policies {
		if policy.Path != model.PathDirect && policy.Path != model.PathProxy {
			return nil, ErrCorrupt
		}
		index.policies[policy.TargetKey] = policy
	}
	return index, nil
}

func (s *Store) NewDurablePolicyIndex(ctx context.Context, capacity int) (*DurablePolicyIndex, error) {
	policies, err := s.LoadDurablePolicies(ctx, capacity)
	if err != nil {
		return nil, err
	}
	return NewDurablePolicyIndex(s.secret, policies, capacity)
}

func (i *DurablePolicyIndex) PreferredPath(target model.Target) model.Path {
	key, err := i.key(target)
	if err != nil {
		return ""
	}
	i.mu.RLock()
	policy, ok := i.policies[key]
	i.mu.RUnlock()
	if !ok {
		return ""
	}
	return policy.Path
}

// Remember updates the hot-path snapshot immediately. Persisting the same
// choice happens asynchronously and cannot delay this connection.
func (i *DurablePolicyIndex) Remember(target model.Target, path model.Path, now time.Time) (DurablePolicyChange, error) {
	if path != model.PathDirect && path != model.PathProxy {
		return DurablePolicyChange{}, nil
	}
	key, err := i.key(target)
	if err != nil {
		return DurablePolicyChange{}, err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if existing, exists := i.policies[key]; exists && existing.Path == path {
		return DurablePolicyChange{Policy: existing}, nil
	}
	change := DurablePolicyChange{Applied: true, Policy: DurablePolicy{TargetKey: key, Path: path, UpdatedAt: now.UTC()}}
	if _, exists := i.policies[key]; !exists && len(i.policies) >= i.capacity {
		var oldestKey [durablePolicyKeyBytes]byte
		var oldest DurablePolicy
		first := true
		for candidateKey, candidate := range i.policies {
			if first || candidate.UpdatedAt.Before(oldest.UpdatedAt) ||
				(candidate.UpdatedAt.Equal(oldest.UpdatedAt) && bytes.Compare(candidateKey[:], oldestKey[:]) < 0) {
				oldestKey, oldest, first = candidateKey, candidate, false
			}
		}
		delete(i.policies, oldestKey)
		change.Evicted = true
		change.EvictedTargetKey = oldestKey
	}
	i.policies[key] = change.Policy
	return change, nil
}

func (i *DurablePolicyIndex) Stats() DurablePolicyIndexStats {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return DurablePolicyIndexStats{Entries: len(i.policies), Capacity: i.capacity}
}

func (i *DurablePolicyIndex) key(target model.Target) ([durablePolicyKeyBytes]byte, error) {
	canonical, err := learning.CanonicalTargetKey(target)
	if err != nil {
		return [durablePolicyKeyBytes]byte{}, err
	}
	innerInput := make([]byte, sha256.BlockSize+len(canonical))
	copy(innerInput, i.innerPad[:])
	copy(innerInput[sha256.BlockSize:], canonical)
	inner := sha256.Sum256(innerInput)
	var outerInput [sha256.BlockSize + sha256.Size]byte
	copy(outerInput[:sha256.BlockSize], i.outerPad[:])
	copy(outerInput[sha256.BlockSize:], inner[:])
	return sha256.Sum256(outerInput[:]), nil
}
