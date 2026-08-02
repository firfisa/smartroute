// Package observe provides privacy-safe, bounded local runtime observation files.
package observe

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/firfisa/smartroute/internal/model"
)

const schemaVersion = 1

const (
	SourceEngine     = "engine"
	SourceGuard      = "guard"
	SourceSupervisor = "supervisor"
)

var managedSources = []string{SourceEngine, SourceGuard, SourceSupervisor}

type Options struct {
	Directory                string
	Source                   string
	MaxFileBytes             int64
	MaxFiles                 int
	Retention                time.Duration
	IncludeCleartextHostname bool
	Clock                    func() time.Time
}

// Event contains only the bounded routing metadata approved for persistence.
// Target is transformed before encoding and is never marshaled directly.
type Event struct {
	EventType        string
	Target           *model.Target
	SelectedPath     model.Path
	SelectedLane     string
	ReasonCode       string
	PolicyReason     string
	Observation      *model.Observation
	OtherObservation *model.Observation
	FailureClass     string
	DirectFailure    string
	ProxyFailure     string
	AdaptiveFailure  string
	OriginalFailure  string
	Committed        *bool
	LearningReason   string
	PolicyState      model.PolicyState
	Service          string
	State            string
	Attempt          int
	BackoffMS        int64
}

type Recorder struct {
	mu       sync.Mutex
	opts     Options
	salt     []byte
	file     *os.File
	size     int64
	openedAt time.Time
	sequence uint64
}

type storedTarget struct {
	NetworkProfileHash string          `json:"network_profile_hash"`
	HostnameHash       string          `json:"hostname_hash"`
	Hostname           string          `json:"hostname,omitempty"`
	Port               uint16          `json:"port"`
	Transport          model.Transport `json:"transport"`
}

type storedEvent struct {
	SchemaVersion    int                `json:"schema_version"`
	RecordedAt       time.Time          `json:"recorded_at"`
	Source           string             `json:"source"`
	EventType        string             `json:"event_type"`
	Target           *storedTarget      `json:"target,omitempty"`
	SelectedPath     model.Path         `json:"selected_path,omitempty"`
	SelectedLane     string             `json:"selected_lane,omitempty"`
	ReasonCode       string             `json:"reason_code,omitempty"`
	PolicyReason     string             `json:"policy_reason,omitempty"`
	Observation      *model.Observation `json:"observation,omitempty"`
	OtherObservation *model.Observation `json:"other_observation,omitempty"`
	FailureClass     string             `json:"failure_class,omitempty"`
	DirectFailure    string             `json:"direct_failure,omitempty"`
	ProxyFailure     string             `json:"proxy_failure,omitempty"`
	AdaptiveFailure  string             `json:"adaptive_failure,omitempty"`
	OriginalFailure  string             `json:"original_failure,omitempty"`
	Committed        *bool              `json:"committed,omitempty"`
	LearningReason   string             `json:"learning_reason,omitempty"`
	PolicyState      model.PolicyState  `json:"policy_state,omitempty"`
	Service          string             `json:"service,omitempty"`
	State            string             `json:"state,omitempty"`
	Attempt          int                `json:"attempt,omitempty"`
	BackoffMS        int64              `json:"backoff_ms,omitempty"`
}

func New(opts Options) (*Recorder, error) {
	if err := validateOptions(opts); err != nil {
		return nil, err
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if err := os.MkdirAll(opts.Directory, 0o700); err != nil {
		return nil, fmt.Errorf("create observation directory: %w", err)
	}
	_ = os.Chmod(opts.Directory, 0o700)
	salt, err := loadOrCreateSalt(opts.Directory)
	if err != nil {
		return nil, err
	}
	sourceDir := filepath.Join(opts.Directory, opts.Source)
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		return nil, fmt.Errorf("create observation source directory: %w", err)
	}
	return &Recorder{opts: opts, salt: salt}, nil
}

func validateOptions(opts Options) error {
	if opts.Directory == "" || filepath.Clean(opts.Directory) == "." || filepath.Clean(opts.Directory) == string(filepath.Separator) {
		return errors.New("safe observation directory is required")
	}
	if !isManagedSource(opts.Source) {
		return errors.New("observation source must be engine, guard, or supervisor")
	}
	if opts.MaxFileBytes < 1024 || opts.MaxFiles < 1 || opts.Retention <= 0 {
		return errors.New("positive bounded observation limits are required")
	}
	return nil
}

func isManagedSource(source string) bool {
	for _, candidate := range managedSources {
		if source == candidate {
			return true
		}
	}
	return false
}

func (r *Recorder) Record(event Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := os.Stat(filepath.Join(r.opts.Directory, ".paused")); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check observation pause state: %w", err)
	}

	now := r.opts.Clock().UTC()
	stored := storedEvent{
		SchemaVersion: schemaVersion, RecordedAt: now, Source: r.opts.Source,
		EventType: event.EventType, SelectedPath: event.SelectedPath,
		SelectedLane: event.SelectedLane, ReasonCode: event.ReasonCode,
		PolicyReason: event.PolicyReason, Observation: event.Observation, OtherObservation: event.OtherObservation,
		FailureClass: event.FailureClass, DirectFailure: event.DirectFailure,
		ProxyFailure: event.ProxyFailure, AdaptiveFailure: event.AdaptiveFailure,
		OriginalFailure: event.OriginalFailure, Committed: event.Committed,
		LearningReason: event.LearningReason, PolicyState: event.PolicyState,
		Service: event.Service, State: event.State, Attempt: event.Attempt, BackoffMS: event.BackoffMS,
	}
	if event.Target != nil {
		stored.Target = r.transformTarget(*event.Target)
	}
	line, err := json.Marshal(stored)
	if err != nil {
		return fmt.Errorf("encode observation: %w", err)
	}
	line = append(line, '\n')
	if int64(len(line)) > r.opts.MaxFileBytes {
		return errors.New("single observation exceeds max file size")
	}
	if r.file == nil || now.Sub(r.openedAt) >= r.opts.Retention || (r.size > 0 && r.size+int64(len(line)) > r.opts.MaxFileBytes) {
		if err := r.rotate(now); err != nil {
			return err
		}
	}
	if _, err := r.file.Write(line); err != nil {
		return fmt.Errorf("write observation: %w", err)
	}
	r.size += int64(len(line))
	return nil
}

func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}

func (r *Recorder) transformTarget(target model.Target) *storedTarget {
	host := strings.ToLower(strings.TrimSuffix(target.Hostname, "."))
	result := &storedTarget{
		NetworkProfileHash: r.hash("profile\x00" + target.NetworkProfileID),
		HostnameHash:       r.hash("hostname\x00" + host),
		Port:               target.Port, Transport: target.Transport,
	}
	if r.opts.IncludeCleartextHostname {
		result.Hostname = target.Hostname
	}
	return result
}

func (r *Recorder) hash(value string) string {
	mac := hmac.New(sha256.New, r.salt)
	_, _ = io.WriteString(mac, value)
	return hex.EncodeToString(mac.Sum(nil))
}

func (r *Recorder) rotate(now time.Time) error {
	if r.file != nil {
		if err := r.file.Close(); err != nil {
			return fmt.Errorf("close observation file: %w", err)
		}
		r.file = nil
	}
	sourceDir := filepath.Join(r.opts.Directory, r.opts.Source)
	for {
		r.sequence++
		name := fmt.Sprintf("%s-%06d.jsonl", now.Format("20060102T150405.000000000Z"), r.sequence)
		file, err := os.OpenFile(filepath.Join(sourceDir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("create observation file: %w", err)
		}
		r.file = file
		r.size = 0
		r.openedAt = now
		break
	}
	return r.prune(now)
}

func (r *Recorder) prune(now time.Time) error {
	sourceDir := filepath.Join(r.opts.Directory, r.opts.Source)
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return fmt.Errorf("list observation files: %w", err)
	}
	type fileInfo struct {
		path string
		mod  time.Time
	}
	var files []fileInfo
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect observation file: %w", err)
		}
		files = append(files, fileInfo{filepath.Join(sourceDir, entry.Name()), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	cutoff := now.Add(-r.opts.Retention)
	for len(files) > 0 {
		removeIndex := -1
		for index, candidate := range files {
			if r.file != nil && candidate.path == r.file.Name() {
				continue
			}
			if candidate.mod.Before(cutoff) || len(files) > r.opts.MaxFiles {
				removeIndex = index
			}
			if removeIndex >= 0 || len(files) <= r.opts.MaxFiles {
				break
			}
		}
		if removeIndex < 0 {
			break
		}
		if err := os.Remove(files[removeIndex].path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("prune observation file: %w", err)
		}
		files = append(files[:removeIndex], files[removeIndex+1:]...)
	}
	return nil
}

func loadOrCreateSalt(directory string) ([]byte, error) {
	path := filepath.Join(directory, ".salt")
	for attempt := 0; attempt < 2; attempt++ {
		data, err := os.ReadFile(path)
		if err == nil {
			if len(data) != 32 {
				return nil, errors.New("observation salt has invalid length")
			}
			return data, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read observation salt: %w", err)
		}
		data = make([]byte, 32)
		if _, err := rand.Read(data); err != nil {
			return nil, fmt.Errorf("generate observation salt: %w", err)
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("create observation salt: %w", err)
		}
		if _, err := file.Write(data); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("write observation salt: %w", err)
		}
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("close observation salt: %w", err)
		}
		return data, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read concurrently created observation salt: %w", err)
	}
	if len(data) != 32 {
		return nil, errors.New("observation salt has invalid length")
	}
	return data, nil
}
