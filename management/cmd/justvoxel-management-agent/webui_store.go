package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	_ "github.com/mattn/go-sqlite3"
)

const webUIDatabasePath = stateDir + "/webui.db"

type webUserRole string

const (
	webUserRoleOperator webUserRole = "operator"
	webUserRoleViewer   webUserRole = "viewer"
)

var (
	errWebUserNotFound      = errors.New("WebUI user not found")
	errWebUserReserved      = errors.New("voxel is reserved for the primary system administrator")
	errWebUserInvalidRole   = errors.New("WebUI user role must be operator or viewer")
	errWebUserInvalidName   = errors.New("WebUI username is invalid")
	errWebUserPasswordEmpty = errors.New("WebUI password is required")
)

type webUser struct {
	ID                int64
	Username          string
	Role              webUserRole
	Enabled           bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
	PasswordChangedAt time.Time
}

type webUIStore struct {
	db  *sql.DB
	now func() time.Time
}

type webUIStoreMigration struct {
	version    int
	name       string
	statements []string
}

var webUIStoreMigrations = []webUIStoreMigration{
	{
		version: 1,
		name:    "web_identity_foundation",
		statements: []string{
			`CREATE TABLE web_users (
				id INTEGER PRIMARY KEY,
				username TEXT NOT NULL COLLATE NOCASE UNIQUE,
				role TEXT NOT NULL CHECK (role IN ('operator', 'viewer')),
				enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
				salt TEXT NOT NULL,
				password_hash TEXT NOT NULL,
				memory_kib INTEGER NOT NULL CHECK (memory_kib > 0),
				iterations INTEGER NOT NULL CHECK (iterations > 0),
				parallelism INTEGER NOT NULL CHECK (parallelism > 0),
				key_length INTEGER NOT NULL CHECK (key_length > 0),
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				password_changed_at TEXT NOT NULL,
				CHECK (length(trim(username)) BETWEEN 1 AND 64),
				CHECK (lower(username) <> 'voxel')
			)`,
			`CREATE INDEX web_users_role_enabled_idx ON web_users(role, enabled)`,
			`CREATE TABLE operator_usage (
				user_id INTEGER PRIMARY KEY REFERENCES web_users(id) ON DELETE CASCADE,
				restart_used INTEGER NOT NULL DEFAULT 0 CHECK (restart_used >= 0),
				backup_used INTEGER NOT NULL DEFAULT 0 CHECK (backup_used >= 0),
				last_restart_at TEXT,
				last_backup_at TEXT,
				updated_at TEXT NOT NULL
			)`,
			`CREATE TABLE operator_global_state (
				id INTEGER PRIMARY KEY CHECK (id = 1),
				last_restart_at TEXT,
				last_backup_at TEXT,
				updated_at TEXT NOT NULL
			)`,
			`INSERT INTO operator_global_state(id, updated_at) VALUES(1, '1970-01-01T00:00:00Z')`,
			`CREATE TABLE audit_events (
				id INTEGER PRIMARY KEY,
				occurred_at TEXT NOT NULL,
				actor_username TEXT NOT NULL,
				actor_role TEXT NOT NULL CHECK (actor_role IN ('administrator', 'operator', 'viewer')),
				action TEXT NOT NULL,
				target TEXT,
				success INTEGER NOT NULL CHECK (success IN (0, 1)),
				context TEXT NOT NULL DEFAULT ''
			)`,
			`CREATE INDEX audit_events_occurred_at_idx ON audit_events(occurred_at DESC, id DESC)`,
			`CREATE TABLE notifications (
				id INTEGER PRIMARY KEY,
				created_at TEXT NOT NULL,
				kind TEXT NOT NULL,
				user_id INTEGER REFERENCES web_users(id) ON DELETE CASCADE,
				title TEXT NOT NULL,
				message TEXT NOT NULL,
				resolved_at TEXT,
				resolved_by TEXT
			)`,
			`CREATE INDEX notifications_open_idx ON notifications(resolved_at, created_at DESC, id DESC)`,
		},
	},
	{
		version: 2,
		name:    "system_monitor_profile",
		statements: []string{
			`CREATE TABLE system_monitor_profile (
				id INTEGER PRIMARY KEY CHECK (id = 1),
				profile_json TEXT NOT NULL,
				updated_at TEXT NOT NULL
			)`,
			`INSERT INTO system_monitor_profile(id, profile_json, updated_at) VALUES(
				1,
				'{"system":true,"cpu":true,"memory":true,"load":true,"filesystem":true,"diskio":true,"network":true,"processes":true,"containers":true,"sensors":true,"alerts":true,"process_count":10}',
				'1970-01-01T00:00:00Z'
			)`,
		},
	},
}

func openWebUIStore(path string) (*webUIStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("WebUI database path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve WebUI database path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, fmt.Errorf("create WebUI database directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(absolute), 0o700); err != nil {
		return nil, fmt.Errorf("secure WebUI database directory: %w", err)
	}

	u := &url.URL{Scheme: "file", Path: absolute}
	query := u.Query()
	query.Set("_foreign_keys", "on")
	query.Set("_busy_timeout", "5000")
	query.Set("_journal_mode", "WAL")
	query.Set("_synchronous", "FULL")
	u.RawQuery = query.Encode()

	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return nil, fmt.Errorf("open WebUI database: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)

	store := &webUIStore{db: db, now: time.Now}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect WebUI database: %w", err)
	}
	if err := verifyWebUIStorePragmas(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := applyWebUIStoreMigrations(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := os.Chmod(absolute, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("secure WebUI database: %w", err)
	}
	return store, nil
}

func verifyWebUIStorePragmas(db *sql.DB) error {
	var journalMode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		return fmt.Errorf("read SQLite journal mode: %w", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		return fmt.Errorf("SQLite journal mode is %q, want WAL", journalMode)
	}
	var foreignKeys int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		return fmt.Errorf("read SQLite foreign key setting: %w", err)
	}
	if foreignKeys != 1 {
		return errors.New("SQLite foreign key enforcement is disabled")
	}
	return nil
}

func applyWebUIStoreMigrations(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create WebUI schema migration table: %w", err)
	}

	rows, err := db.Query(`SELECT version, name FROM schema_migrations ORDER BY version`)
	if err != nil {
		return fmt.Errorf("read WebUI schema migration history: %w", err)
	}
	type appliedMigration struct {
		version int
		name    string
	}
	var applied []appliedMigration
	for rows.Next() {
		var item appliedMigration
		if err := rows.Scan(&item.version, &item.name); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan WebUI schema migration history: %w", err)
		}
		applied = append(applied, item)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close WebUI schema migration history: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read WebUI schema migration history: %w", err)
	}

	if len(applied) > len(webUIStoreMigrations) {
		return fmt.Errorf("WebUI database schema version %d is newer than this management agent supports", applied[len(applied)-1].version)
	}
	for i, item := range applied {
		expected := webUIStoreMigrations[i]
		if item.version != expected.version || item.name != expected.name {
			return fmt.Errorf("WebUI database migration history mismatch at version %d", item.version)
		}
	}

	for i := len(applied); i < len(webUIStoreMigrations); i++ {
		migration := webUIStoreMigrations[i]
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin WebUI schema migration %d: %w", migration.version, err)
		}
		for _, statement := range migration.statements {
			if _, err := tx.Exec(statement); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("apply WebUI schema migration %d (%s): %w", migration.version, migration.name, err)
			}
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations(version, name, applied_at) VALUES(?, ?, ?)`,
			migration.version,
			migration.name,
			formatWebUIStoreTime(time.Now()),
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record WebUI schema migration %d: %w", migration.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit WebUI schema migration %d: %w", migration.version, err)
		}
	}
	return nil
}

func (s *webUIStore) close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *webUIStore) resetFactoryState() error {
	if s == nil || s.db == nil {
		return errors.New("WebUI store is unavailable")
	}
	profile, err := json.Marshal(defaultSystemMonitorProfile())
	if err != nil {
		return fmt.Errorf("encode default system monitor profile: %w", err)
	}
	now := formatWebUIStoreTime(s.now())
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin WebUI factory reset: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, statement := range []string{
		`DELETE FROM notifications`,
		`DELETE FROM audit_events`,
		`DELETE FROM operator_usage`,
		`DELETE FROM web_users`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("reset WebUI factory state: %w", err)
		}
	}
	result, err := tx.Exec(
		`UPDATE operator_global_state SET last_restart_at = NULL, last_backup_at = NULL, updated_at = ? WHERE id = 1`,
		now,
	)
	if err != nil {
		return fmt.Errorf("reset operator global state: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		if err != nil {
			return fmt.Errorf("read operator global reset result: %w", err)
		}
		return errors.New("operator global state row is missing")
	}
	result, err = tx.Exec(
		`UPDATE system_monitor_profile SET profile_json = ?, updated_at = ? WHERE id = 1`,
		string(profile),
		now,
	)
	if err != nil {
		return fmt.Errorf("reset system monitor profile: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		if err != nil {
			return fmt.Errorf("read system monitor reset result: %w", err)
		}
		return errors.New("system monitor profile row is missing")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit WebUI factory reset: %w", err)
	}
	return nil
}

func (s *webUIStore) createWebUser(username string, role webUserRole, password string) (webUser, error) {
	username, err := normalizeWebUsername(username)
	if err != nil {
		return webUser{}, err
	}
	if !validWebUserRole(role) {
		return webUser{}, errWebUserInvalidRole
	}
	if password == "" {
		return webUser{}, errWebUserPasswordEmpty
	}
	credential, err := localAccountForPassword(username, string(role), password)
	if err != nil {
		return webUser{}, fmt.Errorf("hash WebUI user password: %w", err)
	}
	now := s.now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return webUser{}, fmt.Errorf("begin WebUI user creation: %w", err)
	}
	result, err := tx.Exec(`INSERT INTO web_users(
		username, role, enabled, salt, password_hash, memory_kib, iterations,
		parallelism, key_length, created_at, updated_at, password_changed_at
	) VALUES(?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		username,
		role,
		credential.Salt,
		credential.Hash,
		credential.MemoryKiB,
		credential.Iterations,
		credential.Parallelism,
		credential.KeyLength,
		formatWebUIStoreTime(now),
		formatWebUIStoreTime(now),
		formatWebUIStoreTime(now),
	)
	if err != nil {
		_ = tx.Rollback()
		return webUser{}, fmt.Errorf("create WebUI user: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		_ = tx.Rollback()
		return webUser{}, fmt.Errorf("read new WebUI user id: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO operator_usage(user_id, updated_at) VALUES(?, ?)`, id, formatWebUIStoreTime(now)); err != nil {
		_ = tx.Rollback()
		return webUser{}, fmt.Errorf("initialize WebUI user usage: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return webUser{}, fmt.Errorf("commit WebUI user creation: %w", err)
	}
	return s.webUserByID(id)
}

func (s *webUIStore) listWebUsers() ([]webUser, error) {
	rows, err := s.db.Query(`SELECT id, username, role, enabled, created_at, updated_at, password_changed_at
		FROM web_users ORDER BY username COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("list WebUI users: %w", err)
	}
	defer rows.Close()
	var users []webUser
	for rows.Next() {
		user, err := scanWebUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list WebUI users: %w", err)
	}
	return users, nil
}

func (s *webUIStore) webUserByID(id int64) (webUser, error) {
	return scanWebUser(s.db.QueryRow(`SELECT id, username, role, enabled, created_at, updated_at, password_changed_at
		FROM web_users WHERE id = ?`, id))
}

func (s *webUIStore) webUserByUsername(username string) (webUser, error) {
	username = strings.TrimSpace(username)
	return scanWebUser(s.db.QueryRow(`SELECT id, username, role, enabled, created_at, updated_at, password_changed_at
		FROM web_users WHERE username = ? COLLATE NOCASE`, username))
}

func (s *webUIStore) setWebUserRole(id int64, role webUserRole) error {
	if !validWebUserRole(role) {
		return errWebUserInvalidRole
	}
	return s.execUserUpdate(`UPDATE web_users SET role = ?, updated_at = ? WHERE id = ?`, role, formatWebUIStoreTime(s.now()), id)
}

func (s *webUIStore) setWebUserEnabled(id int64, enabled bool) error {
	value := 0
	if enabled {
		value = 1
	}
	return s.execUserUpdate(`UPDATE web_users SET enabled = ?, updated_at = ? WHERE id = ?`, value, formatWebUIStoreTime(s.now()), id)
}

func (s *webUIStore) setWebUserPassword(id int64, password string) error {
	if password == "" {
		return errWebUserPasswordEmpty
	}
	user, err := s.webUserByID(id)
	if err != nil {
		return err
	}
	credential, err := localAccountForPassword(user.Username, string(user.Role), password)
	if err != nil {
		return fmt.Errorf("hash WebUI user password: %w", err)
	}
	now := formatWebUIStoreTime(s.now())
	return s.execUserUpdate(`UPDATE web_users SET
		salt = ?, password_hash = ?, memory_kib = ?, iterations = ?, parallelism = ?, key_length = ?,
		password_changed_at = ?, updated_at = ? WHERE id = ?`,
		credential.Salt,
		credential.Hash,
		credential.MemoryKiB,
		credential.Iterations,
		credential.Parallelism,
		credential.KeyLength,
		now,
		now,
		id,
	)
}

func (s *webUIStore) deleteWebUser(id int64) error {
	return s.execUserUpdate(`DELETE FROM web_users WHERE id = ?`, id)
}

func (s *webUIStore) checkWebUserPassword(username, password string) (webUser, bool, error) {
	username = strings.TrimSpace(username)
	var user webUser
	var enabled int
	var createdAt, updatedAt, passwordChangedAt string
	var salt, hash string
	var memoryKiB, iterations, parallelism, keyLength int64
	err := s.db.QueryRow(`SELECT
		id, username, role, enabled, created_at, updated_at, password_changed_at,
		salt, password_hash, memory_kib, iterations, parallelism, key_length
		FROM web_users WHERE username = ? COLLATE NOCASE`, username).Scan(
		&user.ID,
		&user.Username,
		&user.Role,
		&enabled,
		&createdAt,
		&updatedAt,
		&passwordChangedAt,
		&salt,
		&hash,
		&memoryKiB,
		&iterations,
		&parallelism,
		&keyLength,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return webUser{}, false, nil
	}
	if err != nil {
		return webUser{}, false, fmt.Errorf("read WebUI user credential: %w", err)
	}
	user.Enabled = enabled == 1
	if user.CreatedAt, err = parseWebUIStoreTime(createdAt); err != nil {
		return webUser{}, false, err
	}
	if user.UpdatedAt, err = parseWebUIStoreTime(updatedAt); err != nil {
		return webUser{}, false, err
	}
	if user.PasswordChangedAt, err = parseWebUIStoreTime(passwordChangedAt); err != nil {
		return webUser{}, false, err
	}
	if memoryKiB <= 0 || iterations <= 0 || parallelism <= 0 || parallelism > 255 || keyLength <= 0 {
		return webUser{}, false, errors.New("WebUI user credential parameters are invalid")
	}
	account := localAccount{
		Username:    user.Username,
		Role:        string(user.Role),
		Salt:        salt,
		Hash:        hash,
		MemoryKiB:   uint32(memoryKiB),
		Iterations:  uint32(iterations),
		Parallelism: uint8(parallelism),
		KeyLength:   uint32(keyLength),
	}
	return user, user.Enabled && verifyLocalAccount(account, password), nil
}

func (s *webUIStore) execUserUpdate(query string, args ...any) error {
	result, err := s.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("update WebUI user: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read WebUI user update result: %w", err)
	}
	if count == 0 {
		return errWebUserNotFound
	}
	return nil
}

type webUserScanner interface {
	Scan(dest ...any) error
}

func scanWebUser(scanner webUserScanner) (webUser, error) {
	var user webUser
	var enabled int
	var createdAt, updatedAt, passwordChangedAt string
	if err := scanner.Scan(
		&user.ID,
		&user.Username,
		&user.Role,
		&enabled,
		&createdAt,
		&updatedAt,
		&passwordChangedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return webUser{}, errWebUserNotFound
		}
		return webUser{}, fmt.Errorf("scan WebUI user: %w", err)
	}
	user.Enabled = enabled == 1
	var err error
	if user.CreatedAt, err = parseWebUIStoreTime(createdAt); err != nil {
		return webUser{}, err
	}
	if user.UpdatedAt, err = parseWebUIStoreTime(updatedAt); err != nil {
		return webUser{}, err
	}
	if user.PasswordChangedAt, err = parseWebUIStoreTime(passwordChangedAt); err != nil {
		return webUser{}, err
	}
	return user, nil
}

func normalizeWebUsername(username string) (string, error) {
	username = strings.TrimSpace(username)
	if username == "" || len(username) > 64 {
		return "", errWebUserInvalidName
	}
	if strings.EqualFold(username, systemAdminUsername) {
		return "", errWebUserReserved
	}
	for _, char := range username {
		if unicode.IsControl(char) {
			return "", errWebUserInvalidName
		}
	}
	return username, nil
}

func validWebUserRole(role webUserRole) bool {
	return role == webUserRoleOperator || role == webUserRoleViewer
}

func formatWebUIStoreTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseWebUIStoreTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse WebUI database timestamp: %w", err)
	}
	return parsed, nil
}
