package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"

	modernsqlite "modernc.org/sqlite"
)

const (
	BackupDatabaseName   = "learning.db"
	BackupKeyName        = "learning.db.key"
	BackupManifestName   = "manifest.json"
	BackupIncompleteName = "INCOMPLETE"
	BackupFormatVersion  = 1
)

type StoreStatus struct {
	SchemaVersion    int        `json:"schema_version"`
	SessionCount     int64      `json:"session_count"`
	EvidenceCount    int64      `json:"evidence_count"`
	DirectEvidence   int64      `json:"direct_evidence"`
	ProxyEvidence    int64      `json:"proxy_evidence"`
	OldestObservedAt *time.Time `json:"oldest_observed_at,omitempty"`
	NewestObservedAt *time.Time `json:"newest_observed_at,omitempty"`
}

type BackupManifest struct {
	FormatVersion  int         `json:"format_version"`
	CreatedAt      time.Time   `json:"created_at"`
	DatabaseFile   string      `json:"database_file"`
	KeyFile        string      `json:"key_file"`
	DatabaseSHA256 string      `json:"database_sha256"`
	KeySHA256      string      `json:"key_sha256"`
	Status         StoreStatus `json:"status"`
}

type RestoreResult struct {
	DatabasePath string      `json:"database_path"`
	KeyPath      string      `json:"key_path"`
	Status       StoreStatus `json:"status"`
}

func (s *Store) Status(ctx context.Context) (StoreStatus, error) {
	version, err := s.SchemaVersion(ctx)
	if err != nil {
		return StoreStatus{}, err
	}
	var status StoreStatus
	var oldestMS, newestMS sql.NullInt64
	err = s.db.QueryRowContext(ctx, `
SELECT
    (SELECT COUNT(*) FROM sessions),
    COUNT(*),
    COALESCE(SUM(CASE WHEN direction = 'direct' THEN 1 ELSE 0 END), 0),
    COALESCE(SUM(CASE WHEN direction = 'proxy' THEN 1 ELSE 0 END), 0),
    MIN(observed_at_ms),
    MAX(observed_at_ms)
FROM strong_evidence`).Scan(
		&status.SessionCount, &status.EvidenceCount,
		&status.DirectEvidence, &status.ProxyEvidence,
		&oldestMS, &newestMS,
	)
	if err != nil {
		return StoreStatus{}, fmt.Errorf("inspect durable learning store: %w", err)
	}
	status.SchemaVersion = version
	if oldestMS.Valid {
		value := time.UnixMilli(oldestMS.Int64).UTC()
		status.OldestObservedAt = &value
	}
	if newestMS.Valid {
		value := time.UnixMilli(newestMS.Int64).UTC()
		status.NewestObservedAt = &value
	}
	return status, nil
}

// Backup creates a new, self-contained, privacy-sensitive snapshot directory.
// The online backup API captures a consistent SQLite image without requiring
// callers to copy WAL/SHM files. The destination must not already exist.
func (s *Store) Backup(ctx context.Context, destination string) (BackupManifest, error) {
	if err := validateBackupDestination(destination); err != nil {
		return BackupManifest{}, err
	}
	if err := ctx.Err(); err != nil {
		return BackupManifest{}, err
	}
	absolute, err := filepath.Abs(filepath.Clean(destination))
	if err != nil {
		return BackupManifest{}, fmt.Errorf("resolve backup destination: %w", err)
	}
	parentInfo, err := os.Stat(filepath.Dir(absolute))
	if err != nil {
		return BackupManifest{}, fmt.Errorf("inspect backup parent: %w", err)
	}
	if !parentInfo.IsDir() {
		return BackupManifest{}, errors.New("backup parent must be a directory")
	}
	if err := os.Mkdir(absolute, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return BackupManifest{}, errors.New("backup destination must not already exist")
		}
		return BackupManifest{}, fmt.Errorf("create backup destination: %w", err)
	}
	if err := os.WriteFile(filepath.Join(absolute, BackupIncompleteName), []byte("SmartRoute backup is incomplete and must not be restored.\n"), 0o600); err != nil {
		return BackupManifest{}, fmt.Errorf("create incomplete backup marker: %w", err)
	}

	databasePath := filepath.Join(absolute, BackupDatabaseName)
	if err := s.onlineBackup(ctx, databasePath); err != nil {
		return BackupManifest{}, err
	}
	if err := os.Chmod(databasePath, 0o600); err != nil {
		return BackupManifest{}, fmt.Errorf("secure backup database permissions: %w", err)
	}
	keyPath := filepath.Join(absolute, BackupKeyName)
	if err := writeExclusive(keyPath, s.secret, 0o600); err != nil {
		return BackupManifest{}, fmt.Errorf("write backup key: %w", err)
	}

	verified, err := Open(ctx, Config{Path: databasePath})
	if err != nil {
		return BackupManifest{}, fmt.Errorf("verify backup database: %w", err)
	}
	status, statusErr := verified.Status(ctx)
	checkpointErr := verified.Checkpoint(ctx)
	closeErr := verified.Close()
	if err := errors.Join(statusErr, checkpointErr, closeErr); err != nil {
		return BackupManifest{}, fmt.Errorf("verify backup contents: %w", err)
	}
	databaseHash, err := hashFile(databasePath)
	if err != nil {
		return BackupManifest{}, err
	}
	keyHash, err := hashFile(keyPath)
	if err != nil {
		return BackupManifest{}, err
	}
	manifest := BackupManifest{
		FormatVersion: BackupFormatVersion, CreatedAt: time.Now().UTC(),
		DatabaseFile: BackupDatabaseName, KeyFile: BackupKeyName,
		DatabaseSHA256: databaseHash, KeySHA256: keyHash, Status: status,
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return BackupManifest{}, fmt.Errorf("encode backup manifest: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := writeExclusive(filepath.Join(absolute, BackupManifestName), encoded, 0o600); err != nil {
		return BackupManifest{}, fmt.Errorf("write backup manifest: %w", err)
	}
	if err := os.Remove(filepath.Join(absolute, BackupIncompleteName)); err != nil {
		return BackupManifest{}, fmt.Errorf("finalize backup: %w", err)
	}
	return manifest, nil
}

// VerifyBackup validates a completed manifest and performs SQLite integrity
// checks against a private temporary copy, leaving the backup unchanged.
func VerifyBackup(ctx context.Context, sourceDirectory string) (BackupManifest, error) {
	absolute, manifest, err := validateBackupFiles(ctx, sourceDirectory)
	if err != nil {
		return BackupManifest{}, err
	}
	temporary, err := os.MkdirTemp("", "smartroute-backup-verify-")
	if err != nil {
		return BackupManifest{}, fmt.Errorf("create private backup verification directory: %w", err)
	}
	_ = os.Chmod(temporary, 0o700)
	defer os.RemoveAll(temporary)
	for _, name := range []string{BackupDatabaseName, BackupKeyName} {
		if err := copyFileExclusive(ctx, filepath.Join(absolute, name), filepath.Join(temporary, name), 0o600); err != nil {
			return BackupManifest{}, fmt.Errorf("copy backup for verification: %w", err)
		}
	}
	if err := verifyManagedChecksums(temporary, manifest); err != nil {
		return BackupManifest{}, err
	}
	verified, err := Open(ctx, Config{Path: filepath.Join(temporary, BackupDatabaseName)})
	if err != nil {
		return BackupManifest{}, fmt.Errorf("open backup verification copy: %w", err)
	}
	status, statusErr := verified.Status(ctx)
	checkpointErr := verified.Checkpoint(ctx)
	closeErr := verified.Close()
	if err := errors.Join(statusErr, checkpointErr, closeErr); err != nil {
		return BackupManifest{}, fmt.Errorf("verify backup SQLite contents: %w", err)
	}
	if !reflect.DeepEqual(status, manifest.Status) {
		return BackupManifest{}, errors.New("backup manifest status does not match SQLite contents")
	}
	return manifest, nil
}

// RestoreBackup restores a verified backup to a brand-new database path. It
// never overwrites a database, key, or earlier incomplete restore marker.
func RestoreBackup(ctx context.Context, sourceDirectory, destinationDatabase string) (RestoreResult, error) {
	manifest, err := VerifyBackup(ctx, sourceDirectory)
	if err != nil {
		return RestoreResult{}, err
	}
	if err := validateDatabaseDestination(destinationDatabase); err != nil {
		return RestoreResult{}, err
	}
	absoluteSource, err := filepath.Abs(filepath.Clean(sourceDirectory))
	if err != nil {
		return RestoreResult{}, fmt.Errorf("resolve backup source: %w", err)
	}
	absoluteDatabase, err := filepath.Abs(filepath.Clean(destinationDatabase))
	if err != nil {
		return RestoreResult{}, fmt.Errorf("resolve restore destination: %w", err)
	}
	parentInfo, err := os.Stat(filepath.Dir(absoluteDatabase))
	if err != nil {
		return RestoreResult{}, fmt.Errorf("inspect restore parent: %w", err)
	}
	if !parentInfo.IsDir() {
		return RestoreResult{}, errors.New("restore parent must be a directory")
	}
	keyPath := absoluteDatabase + ".key"
	markerPath := absoluteDatabase + ".INCOMPLETE"
	for _, candidate := range []string{absoluteDatabase, keyPath, markerPath} {
		if _, err := os.Lstat(candidate); err == nil {
			return RestoreResult{}, fmt.Errorf("restore refuses existing path %s", candidate)
		} else if !errors.Is(err, os.ErrNotExist) {
			return RestoreResult{}, fmt.Errorf("inspect restore destination: %w", err)
		}
	}
	if err := writeExclusive(markerPath, []byte("SmartRoute restore is incomplete and must not be used.\n"), 0o600); err != nil {
		return RestoreResult{}, fmt.Errorf("create incomplete restore marker: %w", err)
	}
	if err := copyFileExclusive(ctx, filepath.Join(absoluteSource, manifest.DatabaseFile), absoluteDatabase, 0o600); err != nil {
		return RestoreResult{}, fmt.Errorf("restore learning database: %w", err)
	}
	if err := copyFileExclusive(ctx, filepath.Join(absoluteSource, manifest.KeyFile), keyPath, 0o600); err != nil {
		return RestoreResult{}, fmt.Errorf("restore learning database key: %w", err)
	}
	restoredDatabaseHash, err := hashFile(absoluteDatabase)
	if err != nil {
		return RestoreResult{}, err
	}
	restoredKeyHash, err := hashFile(keyPath)
	if err != nil {
		return RestoreResult{}, err
	}
	if restoredDatabaseHash != manifest.DatabaseSHA256 || restoredKeyHash != manifest.KeySHA256 {
		return RestoreResult{}, errors.New("restored files changed after backup verification")
	}
	restored, err := Open(ctx, Config{Path: absoluteDatabase})
	if err != nil {
		return RestoreResult{}, fmt.Errorf("open restored learning database: %w", err)
	}
	status, statusErr := restored.Status(ctx)
	checkpointErr := restored.Checkpoint(ctx)
	closeErr := restored.Close()
	if err := errors.Join(statusErr, checkpointErr, closeErr); err != nil {
		return RestoreResult{}, fmt.Errorf("verify restored learning database: %w", err)
	}
	if !reflect.DeepEqual(status, manifest.Status) {
		return RestoreResult{}, errors.New("restored database status does not match backup manifest")
	}
	if err := os.Remove(markerPath); err != nil {
		return RestoreResult{}, fmt.Errorf("finalize learning database restore: %w", err)
	}
	return RestoreResult{DatabasePath: absoluteDatabase, KeyPath: keyPath, Status: status}, nil
}

type onlineBackuper interface {
	NewBackup(string) (*modernsqlite.Backup, error)
}

func (s *Store) onlineBackup(ctx context.Context, destination string) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("reserve backup connection: %w", err)
	}
	defer conn.Close()
	err = conn.Raw(func(driverConn any) error {
		backuper, ok := driverConn.(onlineBackuper)
		if !ok {
			return errors.New("SQLite driver does not support online backup")
		}
		backup, err := backuper.NewBackup(destination)
		if err != nil {
			return fmt.Errorf("initialize online backup: %w", err)
		}
		finished := false
		defer func() {
			if !finished {
				_ = backup.Finish()
			}
		}()
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			more, err := backup.Step(128)
			if err != nil {
				return fmt.Errorf("copy online backup pages: %w", err)
			}
			if !more {
				break
			}
		}
		if err := backup.Finish(); err != nil {
			return fmt.Errorf("finish online backup: %w", err)
		}
		finished = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("create online backup: %w", err)
	}
	return nil
}

func validateBackupDestination(path string) error {
	if path == "" {
		return errors.New("backup destination is required")
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == string(filepath.Separator) {
		return errors.New("backup destination must be a new dedicated directory")
	}
	return nil
}

func validateDatabaseDestination(path string) error {
	if path == "" {
		return errors.New("restore database destination is required")
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == string(filepath.Separator) {
		return errors.New("restore destination must name a new database file")
	}
	return nil
}

func validateBackupFiles(ctx context.Context, sourceDirectory string) (string, BackupManifest, error) {
	if sourceDirectory == "" {
		return "", BackupManifest{}, errors.New("backup source directory is required")
	}
	clean := filepath.Clean(sourceDirectory)
	if clean == "." || clean == string(filepath.Separator) {
		return "", BackupManifest{}, errors.New("backup source must be a dedicated directory")
	}
	if err := ctx.Err(); err != nil {
		return "", BackupManifest{}, err
	}
	absolute, err := filepath.Abs(clean)
	if err != nil {
		return "", BackupManifest{}, fmt.Errorf("resolve backup source: %w", err)
	}
	if _, err := os.Lstat(filepath.Join(absolute, BackupIncompleteName)); err == nil {
		return "", BackupManifest{}, errors.New("backup has an INCOMPLETE marker and must not be used")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", BackupManifest{}, fmt.Errorf("inspect backup incomplete marker: %w", err)
	}
	for _, name := range []string{BackupDatabaseName, BackupKeyName, BackupManifestName} {
		info, err := os.Lstat(filepath.Join(absolute, name))
		if err != nil {
			return "", BackupManifest{}, fmt.Errorf("inspect backup file %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return "", BackupManifest{}, fmt.Errorf("backup file %s must be a regular file", name)
		}
		if name == BackupManifestName && info.Size() > 64*1024 {
			return "", BackupManifest{}, errors.New("backup manifest exceeds 65536 bytes")
		}
	}
	manifestFile, err := os.Open(filepath.Join(absolute, BackupManifestName))
	if err != nil {
		return "", BackupManifest{}, fmt.Errorf("open backup manifest: %w", err)
	}
	decoder := json.NewDecoder(manifestFile)
	decoder.DisallowUnknownFields()
	var manifest BackupManifest
	decodeErr := decoder.Decode(&manifest)
	var trailing any
	trailingErr := decoder.Decode(&trailing)
	closeErr := manifestFile.Close()
	if decodeErr != nil {
		return "", BackupManifest{}, fmt.Errorf("decode backup manifest: %w", decodeErr)
	}
	if !errors.Is(trailingErr, io.EOF) {
		return "", BackupManifest{}, errors.New("backup manifest contains trailing data")
	}
	if closeErr != nil {
		return "", BackupManifest{}, fmt.Errorf("close backup manifest: %w", closeErr)
	}
	if err := validateBackupManifest(manifest); err != nil {
		return "", BackupManifest{}, err
	}
	if err := verifyManagedChecksums(absolute, manifest); err != nil {
		return "", BackupManifest{}, err
	}
	return absolute, manifest, nil
}

func validateBackupManifest(manifest BackupManifest) error {
	if manifest.FormatVersion != BackupFormatVersion || manifest.DatabaseFile != BackupDatabaseName || manifest.KeyFile != BackupKeyName {
		return errors.New("backup manifest format or managed filenames are unsupported")
	}
	if manifest.CreatedAt.IsZero() {
		return errors.New("backup manifest creation time is required")
	}
	for _, checksum := range []string{manifest.DatabaseSHA256, manifest.KeySHA256} {
		decoded, err := hex.DecodeString(checksum)
		if err != nil || len(decoded) != sha256.Size {
			return errors.New("backup manifest checksum must be SHA-256 hex")
		}
	}
	status := manifest.Status
	if status.SchemaVersion != CurrentSchemaVersion || status.SessionCount < 0 || status.EvidenceCount < 0 ||
		status.DirectEvidence < 0 || status.ProxyEvidence < 0 || status.DirectEvidence+status.ProxyEvidence != status.EvidenceCount {
		return errors.New("backup manifest aggregate status is invalid")
	}
	if status.EvidenceCount == 0 {
		if status.OldestObservedAt != nil || status.NewestObservedAt != nil {
			return errors.New("empty backup manifest must not contain evidence timestamps")
		}
	} else if status.OldestObservedAt == nil || status.NewestObservedAt == nil || status.NewestObservedAt.Before(*status.OldestObservedAt) {
		return errors.New("backup manifest evidence timestamps are invalid")
	}
	return nil
}

func verifyManagedChecksums(directory string, manifest BackupManifest) error {
	databaseHash, err := hashFile(filepath.Join(directory, manifest.DatabaseFile))
	if err != nil {
		return err
	}
	keyHash, err := hashFile(filepath.Join(directory, manifest.KeyFile))
	if err != nil {
		return err
	}
	if databaseHash != manifest.DatabaseSHA256 || keyHash != manifest.KeySHA256 {
		return errors.New("backup checksum mismatch")
	}
	return nil
}

func writeExclusive(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open backup file for checksum: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("checksum backup file: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func copyFileExclusive(ctx context.Context, source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	buffer := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			_ = output.Close()
			return err
		}
		read, readErr := input.Read(buffer)
		if read > 0 {
			if _, err := output.Write(buffer[:read]); err != nil {
				_ = output.Close()
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			_ = output.Close()
			return readErr
		}
	}
	return output.Close()
}
