// Package store provides the local durable evidence boundary. It stores only
// keyed target fingerprints; cleartext target identities never enter SQLite.
package store

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/firfisa/smartroute/internal/learning"
	"github.com/firfisa/smartroute/internal/model"
	_ "modernc.org/sqlite"
)

const CurrentSchemaVersion = 1

var ErrCorrupt = errors.New("learning database is corrupt or unreadable")

type Config struct {
	Path        string
	BusyTimeout time.Duration
}

type Evidence struct {
	SessionID    string      `json:"session_id"`
	Direction    model.Path  `json:"direction"`
	ObservedAt   time.Time   `json:"observed_at"`
	WinnerStage  model.Stage `json:"winner_stage"`
	OtherStage   model.Stage `json:"other_stage"`
	FailureClass string      `json:"failure_class"`
}

type Summary struct {
	DirectWins     int `json:"direct_wins"`
	ProxyWins      int `json:"proxy_wins"`
	DirectSessions int `json:"direct_sessions"`
	ProxySessions  int `json:"proxy_sessions"`
}

type Store struct {
	db     *sql.DB
	secret []byte
	path   string
}

func Open(ctx context.Context, config Config) (*Store, error) {
	if config.Path == "" {
		return nil, errors.New("learning database path is required")
	}
	clean := filepath.Clean(config.Path)
	if clean == "." || clean == string(filepath.Separator) {
		return nil, errors.New("learning database path must name a file")
	}
	if config.BusyTimeout <= 0 {
		config.BusyTimeout = 5 * time.Second
	}
	absolute, err := filepath.Abs(clean)
	if err != nil {
		return nil, fmt.Errorf("resolve learning database path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, fmt.Errorf("create learning database directory: %w", err)
	}
	_, statErr := os.Stat(absolute)
	databaseExists := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect learning database: %w", statErr)
	}
	secret, err := loadOrCreateSecret(absolute+".key", databaseExists)
	if err != nil {
		return nil, err
	}

	values := url.Values{}
	values.Add("_pragma", "foreign_keys(1)")
	values.Add("_pragma", "journal_mode(WAL)")
	values.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", config.BusyTimeout.Milliseconds()))
	values.Add("_txlock", "immediate")
	dsn := (&url.URL{Scheme: "file", Path: absolute, RawQuery: values.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open learning database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &Store{db: db, secret: secret, path: absolute}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		if databaseExists {
			return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
		}
		return nil, fmt.Errorf("ping learning database: %w", err)
	}
	if err := store.quickCheck(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := os.Chmod(absolute, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("secure learning database permissions: %w", err)
	}
	return store, nil
}

// OpenReadOnly opens an existing current-schema database without creating a
// database/key, changing permissions, or running migrations.
func OpenReadOnly(ctx context.Context, config Config) (*Store, error) {
	if config.Path == "" {
		return nil, errors.New("learning database path is required")
	}
	clean := filepath.Clean(config.Path)
	if clean == "." || clean == string(filepath.Separator) {
		return nil, errors.New("learning database path must name a file")
	}
	if config.BusyTimeout <= 0 {
		config.BusyTimeout = 5 * time.Second
	}
	absolute, err := filepath.Abs(clean)
	if err != nil {
		return nil, fmt.Errorf("resolve learning database path: %w", err)
	}
	info, err := os.Stat(absolute)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("learning database does not exist")
	}
	if err != nil {
		return nil, fmt.Errorf("inspect learning database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("learning database path must be a regular file")
	}
	secret, err := loadOrCreateSecret(absolute+".key", true)
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Add("mode", "ro")
	values.Add("_pragma", "foreign_keys(1)")
	values.Add("_pragma", "query_only(1)")
	values.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", config.BusyTimeout.Milliseconds()))
	dsn := (&url.URL{Scheme: "file", Path: absolute, RawQuery: values.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open read-only learning database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &Store{db: db, secret: secret, path: absolute}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if err := store.quickCheck(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	version, err := store.SchemaVersion(ctx)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if version != CurrentSchemaVersion {
		_ = db.Close()
		return nil, fmt.Errorf("read-only learning database schema %d does not match supported version %d; no migration was attempted", version, CurrentSchemaVersion)
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) StartSession(ctx context.Context, sessionID string, startedAt time.Time) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	if startedAt.IsZero() {
		return errors.New("session start time is required")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions(session_id, started_at_ms) VALUES(?, ?)`,
		sessionID, startedAt.UTC().UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("insert learning session: %w", err)
	}
	return nil
}

// AppendStrongEvidence persists only evidence accepted by learning's shared
// gate. A weak or incomplete pair returns false without writing.
func (s *Store) AppendStrongEvidence(ctx context.Context, target model.Target, sessionID string, winner model.Observation, other *model.Observation, observedAt time.Time) (bool, error) {
	if err := validateSessionID(sessionID); err != nil {
		return false, err
	}
	if observedAt.IsZero() {
		return false, errors.New("evidence observation time is required")
	}
	direction, _, err := learning.ClassifyStrongPair(winner, other)
	if err != nil {
		return false, err
	}
	if direction == "" {
		return false, nil
	}
	if err := validateFailureClass(other.FailureClass); err != nil {
		return false, err
	}
	targetKey, err := s.targetKey(target)
	if err != nil {
		return false, err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO strong_evidence(
    target_key, session_id, direction, observed_at_ms,
    winner_stage, other_stage, failure_class
) VALUES(?, ?, ?, ?, ?, ?, ?)`,
		targetKey, sessionID, string(direction), observedAt.UTC().UnixMilli(),
		winner.StageReached.String(), other.StageReached.String(), other.FailureClass,
	)
	if err != nil {
		return false, fmt.Errorf("insert strong learning evidence: %w", err)
	}
	return true, nil
}

func (s *Store) ListEvidence(ctx context.Context, target model.Target, since time.Time) ([]Evidence, error) {
	targetKey, err := s.targetKey(target)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT session_id, direction, observed_at_ms, winner_stage, other_stage, failure_class
FROM strong_evidence
WHERE target_key = ? AND observed_at_ms >= ?
ORDER BY observed_at_ms, evidence_id`, targetKey, since.UTC().UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("query strong learning evidence: %w", err)
	}
	defer rows.Close()
	var result []Evidence
	for rows.Next() {
		var evidence Evidence
		var direction, winnerStage, otherStage string
		var observedAtMS int64
		if err := rows.Scan(&evidence.SessionID, &direction, &observedAtMS, &winnerStage, &otherStage, &evidence.FailureClass); err != nil {
			return nil, fmt.Errorf("scan strong learning evidence: %w", err)
		}
		evidence.Direction = model.Path(direction)
		evidence.ObservedAt = time.UnixMilli(observedAtMS).UTC()
		evidence.WinnerStage, err = model.ParseStage(winnerStage)
		if err != nil {
			return nil, fmt.Errorf("decode winner stage: %w", err)
		}
		evidence.OtherStage, err = model.ParseStage(otherStage)
		if err != nil {
			return nil, fmt.Errorf("decode other stage: %w", err)
		}
		result = append(result, evidence)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate strong learning evidence: %w", err)
	}
	return result, nil
}

func (s *Store) Summarize(ctx context.Context, target model.Target, since time.Time) (Summary, error) {
	targetKey, err := s.targetKey(target)
	if err != nil {
		return Summary{}, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT direction, COUNT(*), COUNT(DISTINCT session_id)
FROM strong_evidence
WHERE target_key = ? AND observed_at_ms >= ?
GROUP BY direction`, targetKey, since.UTC().UnixMilli())
	if err != nil {
		return Summary{}, fmt.Errorf("summarize strong learning evidence: %w", err)
	}
	defer rows.Close()
	var summary Summary
	for rows.Next() {
		var direction string
		var wins, sessions int
		if err := rows.Scan(&direction, &wins, &sessions); err != nil {
			return Summary{}, fmt.Errorf("scan strong evidence summary: %w", err)
		}
		switch model.Path(direction) {
		case model.PathDirect:
			summary.DirectWins, summary.DirectSessions = wins, sessions
		case model.PathProxy:
			summary.ProxyWins, summary.ProxySessions = wins, sessions
		default:
			return Summary{}, fmt.Errorf("stored evidence has invalid direction %q", direction)
		}
	}
	return summary, rows.Err()
}

// ListTargetSummaries returns one aggregate per pseudonymous exact target but
// deliberately omits both the target key and cleartext identity.
func (s *Store) ListTargetSummaries(ctx context.Context, since time.Time) ([]Summary, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT
    SUM(CASE WHEN direction = 'direct' THEN 1 ELSE 0 END),
    SUM(CASE WHEN direction = 'proxy' THEN 1 ELSE 0 END),
    COUNT(DISTINCT CASE WHEN direction = 'direct' THEN session_id END),
    COUNT(DISTINCT CASE WHEN direction = 'proxy' THEN session_id END)
FROM strong_evidence
WHERE observed_at_ms >= ?
GROUP BY target_key
ORDER BY target_key`, since.UTC().UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("list durable target summaries: %w", err)
	}
	defer rows.Close()
	var summaries []Summary
	for rows.Next() {
		var summary Summary
		if err := rows.Scan(&summary.DirectWins, &summary.ProxyWins, &summary.DirectSessions, &summary.ProxySessions); err != nil {
			return nil, fmt.Errorf("scan durable target summary: %w", err)
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate durable target summaries: %w", err)
	}
	return summaries, nil
}

func (s *Store) PruneEvidence(ctx context.Context, before time.Time) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin strong evidence pruning: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM strong_evidence WHERE observed_at_ms < ?`, before.UTC().UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("prune strong learning evidence: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count pruned learning evidence: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE NOT EXISTS (
    SELECT 1 FROM strong_evidence WHERE strong_evidence.session_id = sessions.session_id
)`); err != nil {
		return 0, fmt.Errorf("prune empty learning sessions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit strong evidence pruning: %w", err)
	}
	return count, nil
}

func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var version int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return 0, fmt.Errorf("read learning schema version: %w", err)
	}
	return version, nil
}

func (s *Store) migrate(ctx context.Context) error {
	version, err := s.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	if version > CurrentSchemaVersion {
		return fmt.Errorf("learning database schema %d is newer than supported version %d", version, CurrentSchemaVersion)
	}
	if version == CurrentSchemaVersion {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin learning migration: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
CREATE TABLE sessions (
    session_id TEXT PRIMARY KEY,
    started_at_ms INTEGER NOT NULL
);
CREATE TABLE strong_evidence (
    evidence_id INTEGER PRIMARY KEY AUTOINCREMENT,
    target_key BLOB NOT NULL CHECK(length(target_key) = 32),
    session_id TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
    direction TEXT NOT NULL CHECK(direction IN ('direct', 'proxy')),
    observed_at_ms INTEGER NOT NULL,
    winner_stage TEXT NOT NULL,
    other_stage TEXT NOT NULL,
    failure_class TEXT NOT NULL
);
CREATE INDEX strong_evidence_target_time
    ON strong_evidence(target_key, observed_at_ms, evidence_id);
PRAGMA user_version = 1;`); err != nil {
		return fmt.Errorf("apply learning schema v1: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit learning migration: %w", err)
	}
	return nil
}

func (s *Store) quickCheck(ctx context.Context) error {
	var result string
	if err := s.db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&result); err != nil {
		return fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if result != "ok" {
		return fmt.Errorf("%w: quick_check returned %q", ErrCorrupt, result)
	}
	return nil
}

func (s *Store) targetKey(target model.Target) ([]byte, error) {
	canonical, err := learning.CanonicalTargetKey(target)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write(canonical)
	return mac.Sum(nil), nil
}

func validateSessionID(value string) error {
	if value == "" || len(value) > 128 {
		return errors.New("session ID must contain 1 to 128 safe characters")
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("._-", character) {
			continue
		}
		return errors.New("session ID must contain 1 to 128 safe characters")
	}
	return nil
}

func validateFailureClass(value string) error {
	if value == "" || len(value) > 64 {
		return errors.New("failure class must contain 1 to 64 safe token characters")
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || strings.ContainsRune("._-", character) {
			continue
		}
		return errors.New("failure class must contain 1 to 64 safe token characters")
	}
	return nil
}

func loadOrCreateSecret(path string, databaseExists bool) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		if len(data) != 32 {
			return nil, errors.New("learning database key has invalid length")
		}
		return data, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read learning database key: %w", err)
	}
	if databaseExists {
		return nil, errors.New("learning database key is missing; refusing to create an incompatible replacement")
	}
	data = make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return nil, fmt.Errorf("generate learning database key: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create learning database key: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write learning database key: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close learning database key: %w", err)
	}
	return data, nil
}

// Checkpoint compacts the WAL boundary for backups and privacy tests.
func (s *Store) Checkpoint(ctx context.Context) error {
	var busy, logFrames, checkpointed int
	if err := s.db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointed); err != nil {
		return fmt.Errorf("checkpoint learning database: %w", err)
	}
	if busy != 0 {
		return fmt.Errorf("checkpoint learning database: %d busy readers", busy)
	}
	return nil
}
