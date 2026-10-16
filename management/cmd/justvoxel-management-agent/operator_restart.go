package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	operatorRestartLimit    = 2
	operatorRestartCooldown = 15 * time.Minute
)

var (
	errOperatorUnavailable = errors.New("operator account is unavailable")
	operatorRestartMu      sync.Mutex
)

type operatorRestartAvailability struct {
	Allowed    bool
	Used       int
	Limit      int
	Reason     string
	RetryAfter time.Duration
}

type operatorRestartGuard struct {
	store  *webUIStore
	tx     *sql.Tx
	userID int64
}

func (s *webUIStore) beginOperatorRestart(userID int64) (*operatorRestartGuard, operatorRestartAvailability, error) {
	availability := operatorRestartAvailability{Limit: operatorRestartLimit}
	if s == nil || s.db == nil || userID <= 0 {
		return nil, availability, errOperatorUnavailable
	}

	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, availability, fmt.Errorf("begin operator restart check: %w", err)
	}
	rollback := func(err error) (*operatorRestartGuard, operatorRestartAvailability, error) {
		_ = tx.Rollback()
		return nil, availability, err
	}

	var role webUserRole
	var enabled int
	var lastPersonal sql.NullString
	if err := tx.QueryRow(`SELECT u.role, u.enabled, o.restart_used, o.last_restart_at
		FROM web_users AS u
		JOIN operator_usage AS o ON o.user_id = u.id
		WHERE u.id = ?`, userID).Scan(&role, &enabled, &availability.Used, &lastPersonal); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rollback(errOperatorUnavailable)
		}
		return rollback(fmt.Errorf("read operator restart allowance: %w", err))
	}
	if role != webUserRoleOperator || enabled != 1 {
		return rollback(errOperatorUnavailable)
	}

	if availability.Used >= operatorRestartLimit {
		availability.Reason = "restart_limit_reached"
		_ = tx.Rollback()
		return nil, availability, nil
	}

	var lastGlobal sql.NullString
	if err := tx.QueryRow(`SELECT last_restart_at FROM operator_global_state WHERE id = 1`).Scan(&lastGlobal); err != nil {
		return rollback(fmt.Errorf("read global operator restart state: %w", err))
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
		candidate := parsed.Add(operatorRestartCooldown)
		if candidate.After(nextAllowed) {
			nextAllowed = candidate
		}
	}
	if !nextAllowed.IsZero() && now.Before(nextAllowed) {
		availability.Reason = "restart_cooldown"
		availability.RetryAfter = nextAllowed.Sub(now)
		_ = tx.Rollback()
		return nil, availability, nil
	}

	availability.Allowed = true
	return &operatorRestartGuard{store: s, tx: tx, userID: userID}, availability, nil
}

func (g *operatorRestartGuard) cancel() {
	if g == nil || g.tx == nil {
		return
	}
	_ = g.tx.Rollback()
	g.tx = nil
}

// Target is derived internally by the authenticated lifecycle handler.
func (g *operatorRestartGuard) acceptTarget(actor session, target string) (int, error) {
	if g == nil || g.tx == nil || g.store == nil {
		return 0, errors.New("operator restart guard is not active")
	}
	now := g.store.now().UTC()
	nowText := formatWebUIStoreTime(now)
	if _, err := g.tx.Exec(`UPDATE operator_usage
		SET restart_used = restart_used + 1, last_restart_at = ?, updated_at = ?
		WHERE user_id = ?`, nowText, nowText, g.userID); err != nil {
		g.cancel()
		return 0, fmt.Errorf("consume operator restart allowance: %w", err)
	}
	var used int
	if err := g.tx.QueryRow(`SELECT restart_used FROM operator_usage WHERE user_id = ?`, g.userID).Scan(&used); err != nil {
		g.cancel()
		return 0, fmt.Errorf("read consumed operator restart allowance: %w", err)
	}
	if _, err := g.tx.Exec(`UPDATE operator_global_state
		SET last_restart_at = ?, updated_at = ? WHERE id = 1`, nowText, nowText); err != nil {
		g.cancel()
		return 0, fmt.Errorf("update global operator restart state: %w", err)
	}
	if err := insertAuditEventTx(g.tx, now, actor, "minecraft_restart", target, true, fmt.Sprintf("operator restart allowance %d/%d", used, operatorRestartLimit)); err != nil {
		g.cancel()
		return 0, err
	}
	if used == operatorRestartLimit {
		title := "Operator restart limit reached"
		message := fmt.Sprintf("%s has used %d of %d Minecraft restart actions. Administrator reset is required before another Operator restart.", actor.Username, used, operatorRestartLimit)
		if _, err := g.tx.Exec(`INSERT INTO notifications(created_at, kind, user_id, title, message)
			VALUES(?, 'operator_restart_limit', ?, ?, ?)`, nowText, g.userID, title, message); err != nil {
			g.cancel()
			return 0, fmt.Errorf("create operator restart-limit notification: %w", err)
		}
		if err := insertAuditEventTx(g.tx, now, actor, "operator_restart_limit_reached", actor.Username, true, fmt.Sprintf("restart allowance %d/%d", used, operatorRestartLimit)); err != nil {
			g.cancel()
			return 0, err
		}
	}
	if err := g.tx.Commit(); err != nil {
		g.tx = nil
		return 0, fmt.Errorf("commit operator restart accounting: %w", err)
	}
	g.tx = nil
	return used, nil
}

func (s *webUIStore) recordAuditEvent(actor session, action, target string, success bool, context string) error {
	if s == nil || s.db == nil {
		return errors.New("WebUI store is unavailable")
	}
	return insertAuditEvent(s.db, s.now().UTC(), actor, action, target, success, context)
}

func insertAuditEvent(executor interface {
	Exec(query string, args ...any) (sql.Result, error)
}, occurredAt time.Time, actor session, action, target string, success bool, context string) error {
	value := 0
	if success {
		value = 1
	}
	if actor.Username == "" || (actor.Role != roleAdministrator && actor.Role != roleOperator && actor.Role != roleViewer) {
		return errors.New("audit actor identity is invalid")
	}
	if _, err := executor.Exec(`INSERT INTO audit_events(
		occurred_at, actor_username, actor_role, action, target, success, context
	) VALUES(?, ?, ?, ?, ?, ?, ?)`,
		formatWebUIStoreTime(occurredAt), actor.Username, actor.Role, action, target, value, context,
	); err != nil {
		return fmt.Errorf("record audit event: %w", err)
	}
	return nil
}

func insertAuditEventTx(tx *sql.Tx, occurredAt time.Time, actor session, action, target string, success bool, context string) error {
	return insertAuditEvent(tx, occurredAt, actor, action, target, success, context)
}

func (s *webUIStore) resetOperatorRestartAllowance(userID int64, actor session) error {
	if actor.Role != roleAdministrator {
		return errors.New("administrator role is required")
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("begin restart allowance reset: %w", err)
	}
	now := s.now().UTC()
	nowText := formatWebUIStoreTime(now)
	var username string
	if err := tx.QueryRow(`SELECT username FROM web_users WHERE id = ?`, userID).Scan(&username); err != nil {
		_ = tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return errWebUserNotFound
		}
		return fmt.Errorf("read operator for restart allowance reset: %w", err)
	}
	result, err := tx.Exec(`UPDATE operator_usage SET restart_used = 0, updated_at = ? WHERE user_id = ?`, nowText, userID)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("reset operator restart allowance: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		_ = tx.Rollback()
		if err != nil {
			return fmt.Errorf("read restart allowance reset result: %w", err)
		}
		return errWebUserNotFound
	}
	if _, err := tx.Exec(`UPDATE notifications
		SET resolved_at = ?, resolved_by = ?
		WHERE kind = 'operator_restart_limit' AND user_id = ? AND resolved_at IS NULL`, nowText, actor.Username, userID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("resolve restart-limit notification: %w", err)
	}
	if err := insertAuditEventTx(tx, now, actor, "reset_restart_allowance", username, true, "operator restart allowance reset to 0/2"); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit restart allowance reset: %w", err)
	}
	return nil
}

func (s *webUIStore) operatorRestartUsage(userID int64) (int, *time.Time, error) {
	var used int
	var last sql.NullString
	if err := s.db.QueryRow(`SELECT restart_used, last_restart_at FROM operator_usage WHERE user_id = ?`, userID).Scan(&used, &last); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil, errWebUserNotFound
		}
		return 0, nil, fmt.Errorf("read operator restart usage: %w", err)
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

// Called only after the selected immutable identity has been validated.
func (s *server) instanceOperatorRestartGuard(w http.ResponseWriter, sess session, id string) (*operatorRestartGuard, bool) {
	guard, availability, err := s.store.beginOperatorRestart(sess.WebUserID)
	if err != nil {
		_ = s.store.recordAuditEvent(sess, "minecraft_restart", id, false, "operator restart eligibility check failed")
		writeError(w, http.StatusServiceUnavailable, "operator restart allowance is unavailable")
		return nil, false
	}
	if !availability.Allowed {
		contextText := availability.Reason
		_ = s.store.recordAuditEvent(sess, "minecraft_restart", id, false, contextText)
		switch availability.Reason {
		case "restart_limit_reached":
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":                        "restart allowance exhausted",
				"reason":                       availability.Reason,
				"restart_used":                 availability.Used,
				"restart_limit":                availability.Limit,
				"administrator_reset_required": true,
			})
		case "restart_cooldown":
			seconds := int(math.Ceil(availability.RetryAfter.Seconds()))
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			writeJSON(w, http.StatusTooManyRequests, map[string]any{
				"error":               "restart cooldown active",
				"reason":              availability.Reason,
				"restart_used":        availability.Used,
				"restart_limit":       availability.Limit,
				"retry_after_seconds": seconds,
			})
		default:
			writeError(w, http.StatusConflict, "operator restart is not currently available")
		}
		return nil, false
	}

	return guard, true
}
