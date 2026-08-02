package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/model"
)

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state", "learning.db")
	store, err := Open(context.Background(), Config{Path: path, BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, path
}

func storeTarget(profile, hostname string, port uint16) model.Target {
	return model.Target{NetworkProfileID: profile, Hostname: hostname, Port: port, Transport: model.TransportTCP}
}

func storeWinner(path model.Path) model.Observation {
	return model.Observation{Path: path, Success: true, StageReached: model.StageTLS, Latency: 10 * time.Millisecond}
}

func storeFailure(path model.Path, stage model.Stage, class string) *model.Observation {
	return &model.Observation{Path: path, StageReached: stage, FailureClass: class, Latency: 20 * time.Millisecond}
}

func TestOpenMigratesAndReopensSchema(t *testing.T) {
	store, path := openTestStore(t)
	version, err := store.SchemaVersion(context.Background())
	if err != nil || version != CurrentSchemaVersion {
		t.Fatalf("version=%d err=%v", version, err)
	}
	assertFileMode(t, path, 0o600)
	assertFileMode(t, path+".key", 0o600)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenReadOnlyRequiresExistingCurrentSchemaAndKey(t *testing.T) {
	directory := t.TempDir()
	missing := filepath.Join(directory, "missing.db")
	if _, err := OpenReadOnly(context.Background(), Config{Path: missing}); err == nil {
		t.Fatal("missing read-only store error = nil")
	}
	if _, err := os.Stat(missing + ".key"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only open created key: %v", err)
	}
	store, path := openTestStore(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	readOnly, err := OpenReadOnly(context.Background(), Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readOnly.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := readOnly.Close(); err != nil {
		t.Fatal(err)
	}

	writable, err := Open(context.Background(), Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writable.db.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatal(err)
	}
	if err := writable.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenReadOnly(context.Background(), Config{Path: path}); err == nil || !bytes.Contains([]byte(err.Error()), []byte("no migration was attempted")) {
		t.Fatalf("old schema read-only error = %v", err)
	}
}

func TestStrongEvidencePersistsWithoutCleartextTarget(t *testing.T) {
	store, path := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := store.StartSession(ctx, "session-1", now); err != nil {
		t.Fatal(err)
	}
	target := storeTarget("private-home-network", "Secret.Example.", 443)
	written, err := store.AppendStrongEvidence(ctx, target, "session-1", storeWinner(model.PathProxy), storeFailure(model.PathDirect, model.StageOutbound, "tls_timeout"), now)
	if err != nil || !written {
		t.Fatalf("written=%v err=%v", written, err)
	}
	evidence, err := store.ListEvidence(ctx, storeTarget("private-home-network", "secret.example", 443), now.Add(-time.Second))
	if err != nil || len(evidence) != 1 || evidence[0].Direction != model.PathProxy || evidence[0].FailureClass != "tls_timeout" {
		t.Fatalf("evidence=%+v err=%v", evidence, err)
	}
	if err := store.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		data, err := os.ReadFile(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("Secret.Example")) || bytes.Contains(data, []byte("secret.example")) || bytes.Contains(data, []byte("private-home-network")) {
			t.Fatalf("cleartext target identity found in %s", candidate)
		}
	}
}

func TestSummaryCountsDistinctSessionsAndScopesTarget(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, session := range []string{"session-a", "session-b"} {
		if err := store.StartSession(ctx, session, now); err != nil {
			t.Fatal(err)
		}
	}
	target := storeTarget("home", "example.com", 443)
	for index, session := range []string{"session-a", "session-a", "session-b"} {
		written, err := store.AppendStrongEvidence(ctx, target, session, storeWinner(model.PathProxy), storeFailure(model.PathDirect, model.StageOutbound, "failed"), now.Add(time.Duration(index)*time.Millisecond))
		if err != nil || !written {
			t.Fatalf("append %d written=%v err=%v", index, written, err)
		}
	}
	summary, err := store.Summarize(ctx, target, now.Add(-time.Second))
	if err != nil || summary.ProxyWins != 3 || summary.ProxySessions != 2 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	for _, isolated := range []model.Target{
		storeTarget("office", "example.com", 443),
		storeTarget("home", "example.com", 8443),
		storeTarget("home", "other.example", 443),
	} {
		got, err := store.Summarize(ctx, isolated, now.Add(-time.Second))
		if err != nil || got != (Summary{}) {
			t.Fatalf("isolated target=%+v summary=%+v err=%v", isolated, got, err)
		}
	}
}

func TestListTargetSummariesOmitsIdentityAndHonorsCutoff(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, session := range []string{"session-a", "session-b"} {
		if err := store.StartSession(ctx, session, now); err != nil {
			t.Fatal(err)
		}
	}
	first := storeTarget("private-home", "secret.example", 443)
	second := storeTarget("private-home", "other.example", 443)
	for _, session := range []string{"session-a", "session-b"} {
		if _, err := store.AppendStrongEvidence(ctx, first, session, storeWinner(model.PathProxy), storeFailure(model.PathDirect, model.StageOutbound, "failed"), now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.AppendStrongEvidence(ctx, second, "session-a", storeWinner(model.PathDirect), storeFailure(model.PathProxy, model.StageOutbound, "failed"), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	summaries, err := store.ListTargetSummaries(ctx, now.Add(-time.Hour))
	if err != nil || len(summaries) != 1 || summaries[0].ProxyWins != 2 || summaries[0].ProxySessions != 2 {
		t.Fatalf("summaries=%+v error=%v", summaries, err)
	}
	encoded, err := json.Marshal(summaries)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(first.Hostname)) || bytes.Contains(encoded, []byte(first.NetworkProfileID)) || bytes.Contains(encoded, []byte("target_key")) {
		t.Fatalf("summary output contains identity: %s", encoded)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.ListTargetSummaries(canceled, time.Time{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled summary error = %v", err)
	}
}

func TestWeakEvidenceAndUnknownSessionAreNotSilentlyStored(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	now := time.Now()
	if err := store.StartSession(ctx, "known", now); err != nil {
		t.Fatal(err)
	}
	target := storeTarget("home", "example.com", 443)
	written, err := store.AppendStrongEvidence(ctx, target, "known", storeWinner(model.PathProxy), nil, now)
	if err != nil || written {
		t.Fatalf("incomplete written=%v err=%v", written, err)
	}
	written, err = store.AppendStrongEvidence(ctx, target, "known", storeWinner(model.PathProxy), storeFailure(model.PathDirect, model.StageNone, "unavailable"), now)
	if err != nil || written {
		t.Fatalf("weak written=%v err=%v", written, err)
	}
	if _, err := store.AppendStrongEvidence(ctx, target, "missing", storeWinner(model.PathProxy), storeFailure(model.PathDirect, model.StageOutbound, "failed"), now); err == nil {
		t.Fatal("unknown session append error = nil")
	}
	if _, err := store.AppendStrongEvidence(ctx, target, "known", storeWinner(model.PathProxy), storeFailure(model.PathDirect, model.StageOutbound, "secret.example: password"), now); err == nil {
		t.Fatal("unsafe failure class append error = nil")
	}
}

func TestPruneEvidenceAndContextCancellation(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.StartSession(ctx, "session", now); err != nil {
		t.Fatal(err)
	}
	target := storeTarget("home", "example.com", 443)
	for _, observed := range []time.Time{now.Add(-2 * time.Hour), now} {
		_, _ = store.AppendStrongEvidence(ctx, target, "session", storeWinner(model.PathDirect), storeFailure(model.PathProxy, model.StageOutbound, "failed"), observed)
	}
	count, err := store.PruneEvidence(ctx, now.Add(-time.Hour))
	if err != nil || count != 1 {
		t.Fatalf("pruned=%d err=%v", count, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.ListEvidence(canceled, target, time.Time{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled query error = %v", err)
	}
}

func TestPruneEvidenceRemovesOnlyEmptySessions(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, session := range []string{"old-session", "current-session"} {
		if err := store.StartSession(ctx, session, now); err != nil {
			t.Fatal(err)
		}
	}
	target := storeTarget("home", "example.com", 443)
	if _, err := store.AppendStrongEvidence(ctx, target, "old-session", storeWinner(model.PathProxy), storeFailure(model.PathDirect, model.StageOutbound, "failed"), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendStrongEvidence(ctx, target, "current-session", storeWinner(model.PathProxy), storeFailure(model.PathDirect, model.StageOutbound, "failed"), now); err != nil {
		t.Fatal(err)
	}
	if count, err := store.PruneEvidence(ctx, now.Add(-time.Hour)); err != nil || count != 1 {
		t.Fatalf("pruned=%d error=%v", count, err)
	}
	status, err := store.Status(ctx)
	if err != nil || status.SessionCount != 1 || status.EvidenceCount != 1 {
		t.Fatalf("status=%+v error=%v", status, err)
	}
	if _, err := store.AppendStrongEvidence(ctx, target, "old-session", storeWinner(model.PathProxy), storeFailure(model.PathDirect, model.StageOutbound, "failed"), now); err == nil {
		t.Fatal("pruned session still accepted evidence")
	}
}

func TestConcurrentEvidenceWrites(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.StartSession(ctx, "session", now); err != nil {
		t.Fatal(err)
	}
	target := storeTarget("home", "example.com", 443)
	var wait sync.WaitGroup
	errorsChannel := make(chan error, 20)
	for index := 0; index < 20; index++ {
		wait.Add(1)
		go func(offset int) {
			defer wait.Done()
			_, err := store.AppendStrongEvidence(ctx, target, "session", storeWinner(model.PathProxy), storeFailure(model.PathDirect, model.StageOutbound, "failed"), now.Add(time.Duration(offset)*time.Millisecond))
			errorsChannel <- err
		}(index)
	}
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
	summary, err := store.Summarize(ctx, target, now.Add(-time.Second))
	if err != nil || summary.ProxyWins != 20 || summary.ProxySessions != 1 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
}

func TestCorruptDatabaseIsClassifiedAndNotOverwritten(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "learning.db")
	original := []byte("not a sqlite database")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".key", make([]byte, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(context.Background(), Config{Path: path})
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open() error = %v", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(data, original) {
		t.Fatalf("corrupt database changed: data=%q err=%v", data, readErr)
	}
}

func TestExistingDatabaseWithoutKeyIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "learning.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), Config{Path: path}); err == nil || !bytes.Contains([]byte(err.Error()), []byte("key is missing")) {
		t.Fatalf("Open() error = %v", err)
	}
}

func TestNewerSchemaIsRejectedWithoutModification(t *testing.T) {
	store, path := openTestStore(t)
	if _, err := store.db.Exec(`PRAGMA user_version = 999`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), Config{Path: path}); err == nil || !bytes.Contains([]byte(err.Error()), []byte("newer than supported")) {
		t.Fatalf("Open() error = %v", err)
	}
}

func TestCorruptedEvidenceRowIsRejected(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.StartSession(ctx, "session", now); err != nil {
		t.Fatal(err)
	}
	target := storeTarget("home", "example.com", 443)
	targetKey, err := store.targetKey(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO strong_evidence(target_key, session_id, direction, observed_at_ms, winner_stage, other_stage, failure_class)
VALUES(?, 'session', 'proxy', ?, 'invalid-stage', 'outbound', 'failed')`, targetKey, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListEvidence(ctx, target, now.Add(-time.Second)); err == nil || !bytes.Contains([]byte(err.Error()), []byte("decode winner stage")) {
		t.Fatalf("ListEvidence() error = %v", err)
	}
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode=%v want=%v", path, got, want)
	}
}
