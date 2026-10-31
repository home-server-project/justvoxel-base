package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	operatorBackupLimit    = 2
	operatorBackupCooldown = 15 * time.Minute
)

var operatorBackupMu sync.Mutex

type operatorBackupAvailability struct {
	Allowed    bool
	Used       int
	Limit      int
	Reason     string
	RetryAfter time.Duration
}

type operatorBackupGuard struct {
	store        *webUIStore
	tx           *sql.Tx
	userID       int64
	connectionID string
}

func (s *webUIStore) beginOperatorBackup(userID int64) (*operatorBackupGuard, operatorBackupAvailability, error) {
	return s.beginOperatorBackupPrincipal(userID, "")
}

func (s *webUIStore) beginOperatorBackupPrincipal(userID int64, connectionID string) (*operatorBackupGuard, operatorBackupAvailability, error) {
	availability := operatorBackupAvailability{Limit: operatorBackupLimit}
	if s == nil || s.db == nil || (userID <= 0 && connectionID == "") {
		return nil, availability, errOperatorUnavailable
	}

	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, availability, fmt.Errorf("begin operator backup check: %w", err)
	}
	rollback := func(err error) (*operatorBackupGuard, operatorBackupAvailability, error) {
		_ = tx.Rollback()
		return nil, availability, err
	}

	var role webUserRole
	var enabled int
	var lastPersonal sql.NullString
	var readErr error
	if connectionID != "" {
		var raw string
		readErr = tx.QueryRow(`SELECT c.record_json, o.backup_used, o.last_backup_at FROM gateway_connections c JOIN gateway_usage o ON o.connection_id=c.id WHERE c.id=?`, connectionID).Scan(&raw, &availability.Used, &lastPersonal)
		var c gatewayConnection
		if readErr == nil {
			readErr = json.Unmarshal([]byte(raw), &c)
		}
		if readErr == nil && c.Enabled && c.Level == 2 {
			role = webUserRoleOperator
			enabled = 1
		}
	} else {
		readErr = tx.QueryRow(`SELECT u.role, u.enabled, o.backup_used, o.last_backup_at
		FROM web_users AS u
		JOIN operator_usage AS o ON o.user_id = u.id
		WHERE u.id = ?`, userID).Scan(&role, &enabled, &availability.Used, &lastPersonal)
	}
	if err := readErr; err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rollback(errOperatorUnavailable)
		}
		return rollback(fmt.Errorf("read operator backup allowance: %w", err))
	}
	if role != webUserRoleOperator || enabled != 1 {
		return rollback(errOperatorUnavailable)
	}
	if availability.Used >= operatorBackupLimit {
		availability.Reason = "backup_limit_reached"
		_ = tx.Rollback()
		return nil, availability, nil
	}

	var lastGlobal sql.NullString
	if err := tx.QueryRow(`SELECT last_backup_at FROM operator_global_state WHERE id = 1`).Scan(&lastGlobal); err != nil {
		return rollback(fmt.Errorf("read global operator backup state: %w", err))
	}

	now := s.now().UTC()
	nextAllowed := time.Time{}
	for _, raw := range []sql.NullString{lastPersonal, lastGlobal} {
		if !raw.Valid || raw.String == "" {
			continue
		}
		parsed, err := parseWebUIStoreTime(raw.String)
		if err != nil {
			return rollback(err)
		}
		candidate := parsed.Add(operatorBackupCooldown)
		if candidate.After(nextAllowed) {
			nextAllowed = candidate
		}
	}
	if !nextAllowed.IsZero() && now.Before(nextAllowed) {
		availability.Reason = "backup_cooldown"
		availability.RetryAfter = nextAllowed.Sub(now)
		_ = tx.Rollback()
		return nil, availability, nil
	}

	availability.Allowed = true
	return &operatorBackupGuard{store: s, tx: tx, userID: userID, connectionID: connectionID}, availability, nil
}

func (g *operatorBackupGuard) cancel() {
	if g == nil || g.tx == nil {
		return
	}
	_ = g.tx.Rollback()
	g.tx = nil
}

func (g *operatorBackupGuard) acceptTarget(actor session, target string) (int, error) {
	if g == nil || g.tx == nil || g.store == nil {
		return 0, errors.New("operator backup guard is not active")
	}
	if !instanceIDPattern.MatchString(target) {
		g.cancel()
		return 0, errors.New("invalid Minecraft instance ID")
	}

	now := g.store.now().UTC()
	nowText := formatWebUIStoreTime(now)
	var used int
	if g.connectionID != "" {
		if _, err := g.tx.Exec(`UPDATE gateway_usage SET backup_used=backup_used+1,last_backup_at=?,updated_at=? WHERE connection_id=?`, nowText, nowText, g.connectionID); err != nil {
			g.cancel()
			return 0, err
		}
		if err := g.tx.QueryRow(`SELECT backup_used FROM gateway_usage WHERE connection_id=?`, g.connectionID).Scan(&used); err != nil {
			g.cancel()
			return 0, err
		}
	} else {
		if _, err := g.tx.Exec(`UPDATE operator_usage
		SET backup_used = backup_used + 1, last_backup_at = ?, updated_at = ?
		WHERE user_id = ?`, nowText, nowText, g.userID); err != nil {
			g.cancel()
			return 0, fmt.Errorf("consume operator backup allowance: %w", err)
		}
		if err := g.tx.QueryRow(`SELECT backup_used FROM operator_usage WHERE user_id = ?`, g.userID).Scan(&used); err != nil {
			g.cancel()
			return 0, fmt.Errorf("read consumed operator backup allowance: %w", err)
		}

	}
	if _, err := g.tx.Exec(`UPDATE operator_global_state
		SET last_backup_at = ?, updated_at = ? WHERE id = 1`, nowText, nowText); err != nil {
		g.cancel()
		return 0, fmt.Errorf("update global operator backup state: %w", err)
	}
	if err := insertAuditEventTx(g.tx, now, actor, "instance_backup", target, true, fmt.Sprintf("operator backup allowance %d/%d", used, operatorBackupLimit)); err != nil {
		g.cancel()
		return 0, err
	}
	if used == operatorBackupLimit && g.connectionID == "" {
		title := "Operator backup limit reached"
		message := fmt.Sprintf("%s has used %d of %d manual backup actions. Administrator reset is required before another Operator backup.", actor.Username, used, operatorBackupLimit)
		if _, err := g.tx.Exec(`INSERT INTO notifications(created_at, kind, user_id, title, message)
			VALUES(?, 'operator_backup_limit', ?, ?, ?)`, nowText, g.userID, title, message); err != nil {
			g.cancel()
			return 0, fmt.Errorf("create operator backup-limit notification: %w", err)
		}
		if err := insertAuditEventTx(g.tx, now, actor, "operator_backup_limit_reached", actor.Username, true, fmt.Sprintf("backup allowance %d/%d", used, operatorBackupLimit)); err != nil {
			g.cancel()
			return 0, err
		}
	}
	if err := g.tx.Commit(); err != nil {
		g.tx = nil
		return 0, fmt.Errorf("commit operator backup accounting: %w", err)
	}
	g.tx = nil
	return used, nil
}

func (s *webUIStore) resetOperatorBackupAllowance(userID int64, actor session) error {
	if actor.Role != roleAdministrator {
		return errors.New("administrator role is required")
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("begin backup allowance reset: %w", err)
	}
	now := s.now().UTC()
	nowText := formatWebUIStoreTime(now)
	var username string
	if err := tx.QueryRow(`SELECT username FROM web_users WHERE id = ?`, userID).Scan(&username); err != nil {
		_ = tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return errWebUserNotFound
		}
		return fmt.Errorf("read operator for backup allowance reset: %w", err)
	}
	result, err := tx.Exec(`UPDATE operator_usage SET backup_used = 0, updated_at = ? WHERE user_id = ?`, nowText, userID)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("reset operator backup allowance: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		_ = tx.Rollback()
		if err != nil {
			return fmt.Errorf("read backup allowance reset result: %w", err)
		}
		return errWebUserNotFound
	}
	if _, err := tx.Exec(`UPDATE notifications
		SET resolved_at = ?, resolved_by = ?
		WHERE kind = 'operator_backup_limit' AND user_id = ? AND resolved_at IS NULL`, nowText, actor.Username, userID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("resolve backup-limit notification: %w", err)
	}
	if err := insertAuditEventTx(tx, now, actor, "reset_backup_allowance", username, true, "operator backup allowance reset to 0/2"); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit backup allowance reset: %w", err)
	}
	return nil
}

func (s *webUIStore) operatorBackupUsage(userID int64) (int, *time.Time, error) {
	var used int
	var last sql.NullString
	if err := s.db.QueryRow(`SELECT backup_used, last_backup_at FROM operator_usage WHERE user_id = ?`, userID).Scan(&used, &last); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil, errWebUserNotFound
		}
		return 0, nil, fmt.Errorf("read operator backup usage: %w", err)
	}
	if !last.Valid || last.String == "" {
		return used, nil, nil
	}
	parsed, err := parseWebUIStoreTime(last.String)
	if err != nil {
		return 0, nil, err
	}
	return used, &parsed, nil
}
