package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/firfisa/smartroute/internal/model"
)

func populateLifecycleStore(t *testing.T, store *Store) model.Target {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, session := range []string{"session-a", "session-b"} {
		if err := store.StartSession(ctx, session, now); err != nil {
			t.Fatal(err)
		}
	}
	target := storeTarget("private-profile", "Secret.Example", 443)
	for index, direction := range []model.Path{model.PathDirect, model.PathProxy, model.PathProxy} {
		other := model.PathProxy
		if direction == model.PathProxy {
			other = model.PathDirect
		}
		session := "session-a"
		if index == 2 {
			session = "session-b"
		}
		written, err := store.AppendStrongEvidence(
			ctx, target, session, storeWinner(direction),
			storeFailure(other, model.StageOutbound, "timeout"), now.Add(time.Duration(index)*time.Second),
		)
		if err != nil || !written {
			t.Fatalf("append %d written=%v error=%v", index, written, err)
		}
	}
	return target
}

func TestStoreStatusReportsOnlyAggregateEvidence(t *testing.T) {
	store, _ := openTestStore(t)
	populateLifecycleStore(t, store)
	status, err := store.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.SchemaVersion != CurrentSchemaVersion || status.SessionCount != 2 || status.EvidenceCount != 3 ||
		status.DirectEvidence != 1 || status.ProxyEvidence != 2 || status.OldestObservedAt == nil || status.NewestObservedAt == nil {
		t.Fatalf("status = %+v", status)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("Secret.Example")) || bytes.Contains(encoded, []byte("private-profile")) {
		t.Fatalf("status contains target identity: %s", encoded)
	}
}

func TestStoreBackupCreatesVerifiedSelfContainedSnapshot(t *testing.T) {
	store, _ := openTestStore(t)
	target := populateLifecycleStore(t, store)
	destination := filepath.Join(t.TempDir(), "backup")
	manifest, err := store.Backup(context.Background(), destination)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.FormatVersion != BackupFormatVersion || manifest.Status.EvidenceCount != 3 ||
		len(manifest.DatabaseSHA256) != 64 || len(manifest.KeySHA256) != 64 {
		t.Fatalf("manifest = %+v", manifest)
	}
	for _, name := range []string{BackupDatabaseName, BackupKeyName, BackupManifestName} {
		assertFileMode(t, filepath.Join(destination, name), 0o600)
	}
	if _, err := os.Stat(filepath.Join(destination, BackupIncompleteName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed backup marker error = %v", err)
	}

	snapshot, err := Open(context.Background(), Config{Path: filepath.Join(destination, BackupDatabaseName)})
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	summary, err := snapshot.Summarize(context.Background(), target, time.Time{})
	if err != nil || summary.DirectWins != 1 || summary.ProxyWins != 2 || summary.ProxySessions != 2 {
		t.Fatalf("snapshot summary=%+v error=%v", summary, err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(destination, BackupManifestName))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(manifestBytes, []byte("Secret.Example")) || bytes.Contains(manifestBytes, []byte("private-profile")) {
		t.Fatalf("manifest contains target identity: %s", manifestBytes)
	}
}

func TestVerifyAndRestoreBackupWithoutMutatingSource(t *testing.T) {
	store, _ := openTestStore(t)
	target := populateLifecycleStore(t, store)
	backupDirectory := filepath.Join(t.TempDir(), "backup")
	created, err := store.Backup(context.Background(), backupDirectory)
	if err != nil {
		t.Fatal(err)
	}
	beforeDatabaseHash, err := hashFile(filepath.Join(backupDirectory, BackupDatabaseName))
	if err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyBackup(context.Background(), backupDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(created, verified) {
		t.Fatalf("created=%+v verified=%+v", created, verified)
	}
	afterDatabaseHash, err := hashFile(filepath.Join(backupDirectory, BackupDatabaseName))
	if err != nil {
		t.Fatal(err)
	}
	if beforeDatabaseHash != afterDatabaseHash {
		t.Fatal("backup verification modified source database")
	}

	restorePath := filepath.Join(t.TempDir(), "restored.db")
	result, err := RestoreBackup(context.Background(), backupDirectory, restorePath)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status.EvidenceCount != 3 || result.DatabasePath != restorePath || result.KeyPath != restorePath+".key" {
		t.Fatalf("restore result = %+v", result)
	}
	for _, path := range []string{restorePath, restorePath + ".key"} {
		assertFileMode(t, path, 0o600)
	}
	if _, err := os.Stat(restorePath + ".INCOMPLETE"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed restore marker error = %v", err)
	}
	restored, err := Open(context.Background(), Config{Path: restorePath})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	summary, err := restored.Summarize(context.Background(), target, time.Time{})
	if err != nil || summary.DirectWins != 1 || summary.ProxyWins != 2 {
		t.Fatalf("restored summary=%+v error=%v", summary, err)
	}
}

func TestVerifyBackupRejectsIncompleteAndTamperedArtifacts(t *testing.T) {
	store, _ := openTestStore(t)
	populateLifecycleStore(t, store)
	backupDirectory := filepath.Join(t.TempDir(), "backup")
	if _, err := store.Backup(context.Background(), backupDirectory); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(backupDirectory, BackupIncompleteName)
	if err := os.WriteFile(marker, []byte("incomplete"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBackup(context.Background(), backupDirectory); err == nil || !bytes.Contains([]byte(err.Error()), []byte("INCOMPLETE")) {
		t.Fatalf("incomplete VerifyBackup() error = %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(backupDirectory, BackupManifestName)
	originalManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifestFields map[string]any
	if err := json.Unmarshal(originalManifest, &manifestFields); err != nil {
		t.Fatal(err)
	}
	manifestFields["unknown"] = true
	unknownManifest, err := json.Marshal(manifestFields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, unknownManifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBackup(context.Background(), backupDirectory); err == nil || !bytes.Contains([]byte(err.Error()), []byte("unknown field")) {
		t.Fatalf("unknown-field VerifyBackup() error = %v", err)
	}
	if err := os.WriteFile(manifestPath, originalManifest, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(backupDirectory, BackupDatabaseName), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("tampered")); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBackup(context.Background(), backupDirectory); err == nil || !bytes.Contains([]byte(err.Error()), []byte("checksum mismatch")) {
		t.Fatalf("tampered VerifyBackup() error = %v", err)
	}
}

func TestRestoreBackupRefusesExistingDestination(t *testing.T) {
	store, _ := openTestStore(t)
	backupDirectory := filepath.Join(t.TempDir(), "backup")
	if _, err := store.Backup(context.Background(), backupDirectory); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "existing.db")
	if err := os.WriteFile(destination, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreBackup(context.Background(), backupDirectory, destination); err == nil || !bytes.Contains([]byte(err.Error()), []byte("refuses existing path")) {
		t.Fatalf("RestoreBackup() error = %v", err)
	}
	if data, err := os.ReadFile(destination); err != nil || string(data) != "keep" {
		t.Fatalf("existing destination changed: data=%q error=%v", data, err)
	}
}

func TestStoreBackupRefusesExistingDestination(t *testing.T) {
	store, _ := openTestStore(t)
	destination := t.TempDir()
	marker := filepath.Join(destination, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Backup(context.Background(), destination); err == nil || !bytes.Contains([]byte(err.Error()), []byte("must not already exist")) {
		t.Fatalf("Backup() error = %v", err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "keep" {
		t.Fatalf("existing destination changed: data=%q error=%v", data, err)
	}
}

func TestStoreBackupLeavesIncompleteMarkerOnFailure(t *testing.T) {
	store, _ := openTestStore(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "failed-backup")
	if _, err := store.Backup(context.Background(), destination); err == nil {
		t.Fatal("Backup() error = nil")
	}
	if data, err := os.ReadFile(filepath.Join(destination, BackupIncompleteName)); err != nil || !bytes.Contains(data, []byte("must not be restored")) {
		t.Fatalf("incomplete marker data=%q error=%v", data, err)
	}
}

func TestStoreBackupRejectsUnsafeOrCanceledDestination(t *testing.T) {
	store, _ := openTestStore(t)
	for _, destination := range []string{"", ".", string(filepath.Separator)} {
		if _, err := store.Backup(context.Background(), destination); err == nil {
			t.Fatalf("Backup(%q) error = nil", destination)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	destination := filepath.Join(t.TempDir(), "canceled")
	if _, err := store.Backup(ctx, destination); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Backup() error = %v", err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled backup created destination: %v", err)
	}
}
