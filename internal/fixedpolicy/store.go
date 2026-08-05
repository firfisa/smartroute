// Package fixedpolicy owns user-authored exact-target routing locks. It is a
// management-plane store only: the Phase 0 runtime deliberately does not read
// this database when selecting a route.
package fixedpolicy

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/firfisa/smartroute/internal/learning"
	"github.com/firfisa/smartroute/internal/model"
	_ "modernc.org/sqlite"
)

const (
	CurrentSchemaVersion = 1
	SourceManual         = "manual"
)

var (
	ErrCorrupt  = errors.New("fixed-policy database is corrupt or unreadable")
	ErrNotFound = errors.New("active fixed policy was not found")
)

type Config struct {
	Path        string
	BusyTimeout time.Duration
}

type Rule struct {
	RuleID           string          `json:"rule_id"`
	NetworkProfileID string          `json:"network_profile_id"`
	Hostname         string          `json:"hostname"`
	Port             uint16          `json:"port"`
	Transport        model.Transport `json:"transport"`
	Path             model.Path      `json:"path"`
	Source           string          `json:"source"`
	CreatedAt        time.Time       `json:"created_at"`
	ExpiresAt        *time.Time      `json:"expires_at,omitempty"`
	RevokedAt        *time.Time      `json:"revoked_at,omitempty"`
	Expired          bool            `json:"expired"`
}

type LockRequest struct {
	Target    model.Target
	Path      model.Path
	CreatedAt time.Time
	ExpiresAt *time.Time
}

type LockResult struct {
	Rule                  Rule `json:"rule"`
	SupersededActiveRules int  `json:"superseded_active_rules"`
}

type ListResult struct {
	DatabaseExists bool   `json:"database_exists"`
	Rules          []Rule `json:"rules"`
}

type Store struct {
	db   *sql.DB
	path string
}

func Open(ctx context.Context, config Config) (*Store, error) {
	absolute, timeout, err := validateConfig(config)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, fmt.Errorf("create fixed-policy directory: %w", err)
	}
	db, err := openDatabase(absolute, timeout, false)
	if err != nil {
		return nil, err
	}
	store := &Store{db: db, path: absolute}
	if err := store.initialize(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := os.Chmod(absolute, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("secure fixed-policy database permissions: %w", err)
	}
	return store, nil
}

func OpenReadOnly(ctx context.Context, config Config) (*Store, error) {
	absolute, timeout, err := validateConfig(config)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absolute)
	if errors.Is(err, os.ErrNotExist) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, fmt.Errorf("inspect fixed-policy database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("fixed-policy database path must be a regular file")
	}
	db, err := openDatabase(absolute, timeout, true)
	if err != nil {
		return nil, err
	}
	store := &Store{db: db, path: absolute}
	if err := store.validateCurrent(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Lock(ctx context.Context, request LockRequest) (LockResult, error) {
	normalized, err := validateLockRequest(request)
	if err != nil {
		return LockResult{}, err
	}
	ruleID, err := newRuleID()
	if err != nil {
		return LockResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LockResult{}, fmt.Errorf("begin fixed-policy lock transaction: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE fixed_policies SET revoked_at_ms = ?
WHERE network_profile_id = ? AND hostname = ? AND port = ? AND transport = ? AND revoked_at_ms IS NULL`,
		normalized.CreatedAt.UnixMilli(), normalized.Target.NetworkProfileID, normalized.Target.Hostname,
		normalized.Target.Port, string(normalized.Target.Transport))
	if err != nil {
		return LockResult{}, fmt.Errorf("supersede active fixed policy: %w", err)
	}
	superseded, err := result.RowsAffected()
	if err != nil {
		return LockResult{}, fmt.Errorf("count superseded fixed policies: %w", err)
	}
	var expires any
	if normalized.ExpiresAt != nil {
		expires = normalized.ExpiresAt.UnixMilli()
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO fixed_policies(
  rule_id, network_profile_id, hostname, port, transport, path, source,
  created_at_ms, expires_at_ms, revoked_at_ms
) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		ruleID, normalized.Target.NetworkProfileID, normalized.Target.Hostname, normalized.Target.Port,
		string(normalized.Target.Transport), string(normalized.Path), SourceManual,
		normalized.CreatedAt.UnixMilli(), expires)
	if err != nil {
		return LockResult{}, fmt.Errorf("insert fixed policy: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LockResult{}, fmt.Errorf("commit fixed-policy lock: %w", err)
	}
	rule := Rule{
		RuleID: ruleID, NetworkProfileID: normalized.Target.NetworkProfileID, Hostname: normalized.Target.Hostname,
		Port: normalized.Target.Port, Transport: normalized.Target.Transport, Path: normalized.Path,
		Source: SourceManual, CreatedAt: normalized.CreatedAt, ExpiresAt: normalized.ExpiresAt,
	}
	return LockResult{Rule: rule, SupersededActiveRules: int(superseded)}, nil
}

func (s *Store) Revoke(ctx context.Context, ruleID string, revokedAt time.Time) (Rule, error) {
	if err := validateRuleID(ruleID); err != nil {
		return Rule{}, err
	}
	if revokedAt.IsZero() {
		return Rule{}, errors.New("fixed-policy revocation time is required")
	}
	revokedAt = revokedAt.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Rule{}, fmt.Errorf("begin fixed-policy revoke transaction: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx,
		`UPDATE fixed_policies SET revoked_at_ms = ? WHERE rule_id = ? AND revoked_at_ms IS NULL`,
		revokedAt.UnixMilli(), ruleID)
	if err != nil {
		return Rule{}, fmt.Errorf("revoke fixed policy: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return Rule{}, fmt.Errorf("count revoked fixed policies: %w", err)
	}
	if changed != 1 {
		return Rule{}, ErrNotFound
	}
	rule, err := ruleByID(ctx, tx, ruleID, revokedAt)
	if err != nil {
		return Rule{}, err
	}
	if err := tx.Commit(); err != nil {
		return Rule{}, fmt.Errorf("commit fixed-policy revoke: %w", err)
	}
	return rule, nil
}

func (s *Store) List(ctx context.Context, includeInactive bool, now time.Time) ([]Rule, error) {
	if now.IsZero() {
		return nil, errors.New("fixed-policy list time is required")
	}
	query := `
SELECT rule_id, network_profile_id, hostname, port, transport, path, source,
       created_at_ms, expires_at_ms, revoked_at_ms
FROM fixed_policies`
	if !includeInactive {
		query += ` WHERE revoked_at_ms IS NULL AND (expires_at_ms IS NULL OR expires_at_ms > ?)`
	}
	query += ` ORDER BY created_at_ms, rule_id`
	var rows *sql.Rows
	var err error
	if includeInactive {
		rows, err = s.db.QueryContext(ctx, query)
	} else {
		rows, err = s.db.QueryContext(ctx, query, now.UTC().UnixMilli())
	}
	if err != nil {
		return nil, fmt.Errorf("list fixed policies: %w", err)
	}
	defer rows.Close()
	var rules []Rule
	for rows.Next() {
		rule, err := scanRule(rows, now.UTC())
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate fixed policies: %w", err)
	}
	return rules, nil
}

func ListReadOnly(ctx context.Context, config Config, includeInactive bool, now time.Time) (ListResult, error) {
	store, err := OpenReadOnly(ctx, config)
	if errors.Is(err, os.ErrNotExist) {
		return ListResult{DatabaseExists: false, Rules: []Rule{}}, nil
	}
	if err != nil {
		return ListResult{}, err
	}
	defer store.Close()
	rules, err := store.List(ctx, includeInactive, now)
	if err != nil {
		return ListResult{}, err
	}
	if rules == nil {
		rules = []Rule{}
	}
	return ListResult{DatabaseExists: true, Rules: rules}, nil
}

func validateLockRequest(request LockRequest) (LockRequest, error) {
	if request.CreatedAt.IsZero() {
		return LockRequest{}, errors.New("fixed-policy creation time is required")
	}
	if request.Path != model.PathDirect && request.Path != model.PathProxy {
		return LockRequest{}, errors.New("fixed-policy path must be direct or proxy")
	}
	if request.Target.Transport != model.TransportTCP {
		return LockRequest{}, errors.New("fixed-policy transport must be tcp in Phase 0")
	}
	if _, err := learning.CanonicalTargetKey(request.Target); err != nil {
		return LockRequest{}, fmt.Errorf("fixed-policy target: %w", err)
	}
	normalized := request
	normalized.CreatedAt = request.CreatedAt.UTC()
	normalized.Target.Hostname = strings.ToLower(strings.TrimSuffix(request.Target.Hostname, "."))
	if ip := net.ParseIP(normalized.Target.Hostname); ip != nil {
		normalized.Target.Hostname = ip.String()
	}
	if request.ExpiresAt != nil {
		value := request.ExpiresAt.UTC()
		if !value.After(normalized.CreatedAt) {
			return LockRequest{}, errors.New("fixed-policy expiration must be after creation")
		}
		normalized.ExpiresAt = &value
	}
	return normalized, nil
}

func newRuleID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate fixed-policy rule ID: %w", err)
	}
	return "policy-" + hex.EncodeToString(value), nil
}

func validateRuleID(value string) error {
	if !strings.HasPrefix(value, "policy-") || len(value) != len("policy-")+32 {
		return errors.New("fixed-policy rule ID is invalid")
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "policy-"))
	if err != nil {
		return errors.New("fixed-policy rule ID is invalid")
	}
	return nil
}

func validateConfig(config Config) (string, time.Duration, error) {
	if config.Path == "" {
		return "", 0, errors.New("fixed-policy database path is required")
	}
	clean := filepath.Clean(config.Path)
	if clean == "." || clean == string(filepath.Separator) {
		return "", 0, errors.New("fixed-policy database path must name a file")
	}
	absolute, err := filepath.Abs(clean)
	if err != nil {
		return "", 0, fmt.Errorf("resolve fixed-policy database path: %w", err)
	}
	timeout := config.BusyTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return absolute, timeout, nil
}

func openDatabase(path string, timeout time.Duration, readOnly bool) (*sql.DB, error) {
	values := url.Values{}
	values.Add("_pragma", "foreign_keys(1)")
	values.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", timeout.Milliseconds()))
	if readOnly {
		values.Add("mode", "ro")
		values.Add("_pragma", "query_only(1)")
	} else {
		values.Add("_pragma", "journal_mode(WAL)")
		values.Add("_txlock", "immediate")
	}
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: values.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open fixed-policy database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

func (s *Store) initialize(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("open fixed-policy database: %w", err)
	}
	if err := s.quickCheck(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_metadata (
  key TEXT PRIMARY KEY,
  value INTEGER NOT NULL
);
INSERT OR IGNORE INTO schema_metadata(key, value) VALUES('schema_version', 1);
CREATE TABLE IF NOT EXISTS fixed_policies (
  rule_id TEXT PRIMARY KEY,
  network_profile_id TEXT NOT NULL,
  hostname TEXT NOT NULL,
  port INTEGER NOT NULL CHECK(port BETWEEN 1 AND 65535),
  transport TEXT NOT NULL CHECK(transport = 'tcp'),
  path TEXT NOT NULL CHECK(path IN ('direct', 'proxy')),
  source TEXT NOT NULL CHECK(source = 'manual'),
  created_at_ms INTEGER NOT NULL,
  expires_at_ms INTEGER,
  revoked_at_ms INTEGER,
  CHECK(expires_at_ms IS NULL OR expires_at_ms > created_at_ms),
  CHECK(revoked_at_ms IS NULL OR revoked_at_ms >= created_at_ms)
);
CREATE UNIQUE INDEX IF NOT EXISTS one_active_fixed_policy_per_target
ON fixed_policies(network_profile_id, hostname, port, transport)
WHERE revoked_at_ms IS NULL;
`)
	if err != nil {
		return fmt.Errorf("initialize fixed-policy schema: %w", err)
	}
	return s.validateCurrent(ctx)
}

func (s *Store) validateCurrent(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if err := s.quickCheck(ctx); err != nil {
		return err
	}
	var version int
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM schema_metadata WHERE key = 'schema_version'`).Scan(&version); err != nil {
		return fmt.Errorf("%w: read fixed-policy schema version: %v", ErrCorrupt, err)
	}
	if version != CurrentSchemaVersion {
		return fmt.Errorf("fixed-policy schema %d does not match supported version %d", version, CurrentSchemaVersion)
	}
	return nil
}

func (s *Store) quickCheck(ctx context.Context) error {
	var result string
	if err := s.db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&result); err != nil {
		return fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if result != "ok" {
		return fmt.Errorf("%w: quick_check=%s", ErrCorrupt, result)
	}
	return nil
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func ruleByID(ctx context.Context, queryer queryRower, ruleID string, now time.Time) (Rule, error) {
	row := queryer.QueryRowContext(ctx, `
SELECT rule_id, network_profile_id, hostname, port, transport, path, source,
       created_at_ms, expires_at_ms, revoked_at_ms
FROM fixed_policies WHERE rule_id = ?`, ruleID)
	return scanRule(row, now)
}

type scanner interface {
	Scan(...any) error
}

func scanRule(row scanner, now time.Time) (Rule, error) {
	var rule Rule
	var port int
	var transport, path string
	var createdMS int64
	var expiresMS, revokedMS sql.NullInt64
	if err := row.Scan(
		&rule.RuleID, &rule.NetworkProfileID, &rule.Hostname, &port, &transport, &path, &rule.Source,
		&createdMS, &expiresMS, &revokedMS,
	); err != nil {
		return Rule{}, fmt.Errorf("scan fixed policy: %w", err)
	}
	rule.Port = uint16(port)
	rule.Transport = model.Transport(transport)
	rule.Path = model.Path(path)
	rule.CreatedAt = time.UnixMilli(createdMS).UTC()
	if expiresMS.Valid {
		value := time.UnixMilli(expiresMS.Int64).UTC()
		rule.ExpiresAt = &value
		rule.Expired = !now.Before(value)
	}
	if revokedMS.Valid {
		value := time.UnixMilli(revokedMS.Int64).UTC()
		rule.RevokedAt = &value
	}
	if err := validateStoredRule(rule); err != nil {
		return Rule{}, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return rule, nil
}

func validateStoredRule(rule Rule) error {
	if err := validateRuleID(rule.RuleID); err != nil {
		return err
	}
	request := LockRequest{
		Target: model.Target{NetworkProfileID: rule.NetworkProfileID, Hostname: rule.Hostname, Port: rule.Port, Transport: rule.Transport},
		Path:   rule.Path, CreatedAt: rule.CreatedAt, ExpiresAt: rule.ExpiresAt,
	}
	if _, err := validateLockRequest(request); err != nil {
		return err
	}
	if rule.Source != SourceManual {
		return errors.New("fixed-policy source is invalid")
	}
	if rule.RevokedAt != nil && rule.RevokedAt.Before(rule.CreatedAt) {
		return errors.New("fixed-policy revocation precedes creation")
	}
	return nil
}
