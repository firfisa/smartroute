package fixedpolicy

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/model"
	_ "modernc.org/sqlite"
)

func TestListReadOnlyMissingDoesNotCreateDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policies.db")
	result, err := ListReadOnly(context.Background(), Config{Path: path}, false, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if result.DatabaseExists || len(result.Rules) != 0 {
		t.Fatalf("result=%+v", result)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only list created database: %v", err)
	}
}

func TestLockSupersedeListExpireAndRevoke(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policies.db")
	store, err := Open(context.Background(), Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("database permissions=%o", info.Mode().Perm())
	}
	now := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	target := model.Target{NetworkProfileID: "office", Hostname: "API.Example.COM.", Port: 443, Transport: model.TransportTCP}
	first, err := store.Lock(context.Background(), LockRequest{Target: target, Path: model.PathDirect, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if first.SupersededActiveRules != 0 || first.Rule.Hostname != "api.example.com" || first.Rule.Source != SourceManual || first.Rule.ExpiresAt != nil {
		t.Fatalf("first=%+v", first)
	}
	expires := now.Add(time.Hour)
	second, err := store.Lock(context.Background(), LockRequest{Target: target, Path: model.PathProxy, CreatedAt: now.Add(time.Minute), ExpiresAt: &expires})
	if err != nil {
		t.Fatal(err)
	}
	if second.SupersededActiveRules != 1 || second.Rule.Path != model.PathProxy {
		t.Fatalf("second=%+v", second)
	}
	active, err := store.List(context.Background(), false, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].RuleID != second.Rule.RuleID || active[0].Expired || active[0].RevokedAt != nil {
		t.Fatalf("active=%+v", active)
	}
	if expired, err := store.List(context.Background(), false, expires); err != nil || len(expired) != 0 {
		t.Fatalf("expired active=%+v err=%v", expired, err)
	}
	all, err := store.List(context.Background(), true, expires)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].RevokedAt == nil || !all[1].Expired {
		t.Fatalf("all=%+v", all)
	}
	third, err := store.Lock(context.Background(), LockRequest{Target: target, Path: model.PathDirect, CreatedAt: expires.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	revokedAt := expires.Add(2 * time.Minute)
	revoked, err := store.Revoke(context.Background(), third.Rule.RuleID, revokedAt)
	if err != nil {
		t.Fatal(err)
	}
	if revoked.RevokedAt == nil || !revoked.RevokedAt.Equal(revokedAt) {
		t.Fatalf("revoked=%+v", revoked)
	}
	if _, err := store.Revoke(context.Background(), third.Rule.RuleID, revokedAt.Add(time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second revoke error=%v", err)
	}
}

func TestLockValidation(t *testing.T) {
	now := time.Now().UTC()
	valid := LockRequest{
		Target: model.Target{NetworkProfileID: "profile", Hostname: "example.com", Port: 443, Transport: model.TransportTCP},
		Path:   model.PathDirect, CreatedAt: now,
	}
	tests := []LockRequest{
		{},
		{Target: valid.Target, Path: model.PathOriginal, CreatedAt: now},
		{Target: model.Target{NetworkProfileID: "profile", Hostname: "example.com", Port: 443, Transport: model.TransportUDP}, Path: model.PathDirect, CreatedAt: now},
		{Target: model.Target{NetworkProfileID: "", Hostname: "example.com", Port: 443, Transport: model.TransportTCP}, Path: model.PathDirect, CreatedAt: now},
	}
	expired := now
	withBadExpiry := valid
	withBadExpiry.ExpiresAt = &expired
	tests = append(tests, withBadExpiry)
	for _, request := range tests {
		if _, err := validateLockRequest(request); err == nil {
			t.Fatalf("accepted invalid request=%+v", request)
		}
	}
}

func TestOpenReadOnlyRejectsCorruptAndFutureSchema(t *testing.T) {
	directory := t.TempDir()
	corrupt := filepath.Join(directory, "corrupt.db")
	if err := os.WriteFile(corrupt, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenReadOnly(context.Background(), Config{Path: corrupt}); err == nil {
		t.Fatal("corrupt database accepted")
	}

	future := filepath.Join(directory, "future.db")
	store, err := Open(context.Background(), Config{Path: future})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", future)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE schema_metadata SET value = 999 WHERE key = 'schema_version'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenReadOnly(context.Background(), Config{Path: future}); err == nil {
		t.Fatal("future schema accepted")
	}
}
