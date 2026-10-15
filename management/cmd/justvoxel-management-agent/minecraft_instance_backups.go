package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const instanceBackupHelper = "/usr/libexec/justvoxel/mjust/instance-backup"

var instanceBackupUnits = "/etc/systemd/system"
var instanceBackupTemplates = "/usr/share/justvoxel/templates/systemd"
var instanceBackupMu sync.Mutex
var instanceBackupWorkerMu sync.Mutex

// Inventory may contain all 365 retained records. Runtime diagnostics use a
// smaller capture; this separate bounded protocol must not silently truncate.
var instanceBackupStorageCommand = func(ctx context.Context, command string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	var output instanceBackupCapture
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	return output.Bytes(), err
}

type instanceBackupCapture struct{ bytes.Buffer }

func (b *instanceBackupCapture) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 4<<20 {
		return 0, errors.New("backup protocol output exceeded limit")
	}
	return b.Buffer.Write(p)
}

var instanceBackupTimePattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

type instanceBackupSettings struct {
	Enabled   bool   `json:"enabled"`
	Time      string `json:"time"`
	Retention int    `json:"retention"`
}
type instanceBackupStatus struct {
	LastAttempt       string `json:"last_attempt,omitempty"`
	LastSuccess       string `json:"last_success,omitempty"`
	State             string `json:"state"`
	Stage             string `json:"stage,omitempty"`
	OriginallyRunning bool   `json:"originally_running"`
	RestartRequired   bool   `json:"restart_required"`
	ArchivePublished  bool   `json:"archive_published"`
	Message           string `json:"message,omitempty"`
}
type instanceBackupApply struct {
	Previous   instanceBackupSettings `json:"previous"`
	Service    []byte                 `json:"service"`
	Timer      []byte                 `json:"timer"`
	HadService bool                   `json:"had_service"`
	HadTimer   bool                   `json:"had_timer"`
	Enabled    bool                   `json:"enabled"`
	Active     bool                   `json:"active"`
}

func instanceBackupUnit(id, suffix string) string { return "minecraft-backup-" + id + "." + suffix }
func (b *instanceRuntimeBackend) backupPath(id, file string) string {
	return filepath.Join(b.root, id, "backup-"+file+".json")
}
func validInstanceBackupSettings(v instanceBackupSettings) bool {
	return instanceBackupTimePattern.MatchString(v.Time) && v.Retention >= 1 && v.Retention <= 365
}
func (b *instanceRuntimeBackend) backupSettings(id string) (instanceBackupSettings, error) {
	v := instanceBackupSettings{Time: "04:00", Retention: 7}
	if _, err := b.readRecord(id); err != nil {
		return v, err
	}
	data, err := b.readPrivate(b.backupPath(id, "settings"))
	if errors.Is(err, os.ErrNotExist) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	if instanceDecode(data, &v) != nil || !validInstanceBackupSettings(v) {
		return v, instanceRuntimeFailure("Instance backup settings are invalid.")
	}
	return v, nil
}
func (b *instanceRuntimeBackend) backupStatus(id string) (instanceBackupStatus, error) {
	v := instanceBackupStatus{State: "idle"}
	data, err := b.readPrivate(b.backupPath(id, "status"))
	if errors.Is(err, os.ErrNotExist) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	err = instanceDecode(data, &v)
	return v, err
}
func (b *instanceRuntimeBackend) backupStorage(ctx context.Context, action, id string) ([]byte, error) {
	out, err := instanceBackupStorageCommand(ctx, "nsenter", "--mount=/proc/1/ns/mnt", "--", instanceBackupHelper, action, id)
	if err != nil {
		return nil, instanceRuntimeFailure("Instance backup storage, capacity, archive verification, or publication failed.")
	}
	return out, nil
}
func backupMinute(s string) int {
	h, _ := strconv.Atoi(s[:2])
	m, _ := strconv.Atoi(s[3:])
	return h*60 + m
}
func backupTimesSeparated(a, b string) bool {
	d := backupMinute(a) - backupMinute(b)
	if d < 0 {
		d = -d
	}
	return d >= 30 && d <= 1440-30
}

// Accept a single ordinary daily calendar only. Unknown legacy expressions or
// drop-ins fail closed rather than advertising a separation we cannot prove.
func legacyBackupDaily(raw string) (string, error) {
	calendar := ""
	section := ""
	fragments := 0
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# /") {
			fragments++
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		if section != "[Timer]" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return "", errInstanceBusy
		}
		switch key {
		case "OnCalendar":
			if calendar != "" {
				return "", errInstanceBusy
			}
			calendar = value
		case "Persistent", "AccuracySec", "Unit":
		default:
			return "", instanceRuntimeFailure("Legacy backup schedule cannot be safely interpreted.")
		}
	}
	if fragments > 1 {
		return "", instanceRuntimeFailure("Legacy backup timer overrides require schedule review.")
	}
	re := regexp.MustCompile(`^\*-\*-\* ((?:[01][0-9]|2[0-3]):[0-5][0-9]):00$`)
	match := re.FindStringSubmatch(calendar)
	if match == nil {
		return "", instanceRuntimeFailure("Legacy backup schedule cannot be safely interpreted.")
	}
	return match[1], nil
}
func (b *instanceRuntimeBackend) backupSeparation(ctx context.Context, id string, v instanceBackupSettings) error {
	if !v.Enabled {
		return nil
	}
	records, err := b.records()
	if err != nil {
		return err
	}
	for _, r := range records {
		if r.Runtime.ID == id {
			continue
		}
		other, e := b.backupSettings(r.Runtime.ID)
		if e != nil {
			return e
		}
		if other.Enabled && !backupTimesSeparated(v.Time, other.Time) {
			return instanceRuntimeFailure("Daily backups must start at least 30 minutes apart.")
		}
	}
	enabled, e := b.command(ctx, "systemctl", "is-enabled", "minecraft-backup.timer")
	active, a := b.command(ctx, "systemctl", "is-active", "minecraft-backup.timer")
	en, ac := strings.TrimSpace(string(enabled)), strings.TrimSpace(string(active))
	if (e != nil && en != "disabled") || (a != nil && ac != "inactive") {
		return instanceRuntimeFailure("Legacy backup timer state cannot be verified.")
	}
	if en != "enabled" && en != "disabled" {
		return instanceRuntimeFailure("Legacy backup timer state cannot be verified.")
	}
	if en == "enabled" || ac == "active" {
		raw, err := b.command(ctx, "systemctl", "cat", "minecraft-backup.timer")
		if err != nil {
			return errInstanceBusy
		}
		daily, err := legacyBackupDaily(string(raw))
		if err != nil {
			return err
		}
		if !backupTimesSeparated(v.Time, daily) {
			return instanceRuntimeFailure("Daily backups must start at least 30 minutes apart, including the legacy backup.")
		}
	}
	return nil
}
func (b *instanceRuntimeBackend) backupTimerState(ctx context.Context, id string) (bool, bool, error) {
	unit := instanceBackupUnit(id, "timer")
	en, e := b.command(ctx, "systemctl", "is-enabled", unit)
	ac, a := b.command(ctx, "systemctl", "is-active", unit)
	enabled, active := strings.TrimSpace(string(en)), strings.TrimSpace(string(ac))
	if (e != nil && enabled != "disabled" && enabled != "not-found") || (a != nil && active != "inactive" && active != "unknown") {
		return false, false, errInstanceBusy
	}
	if enabled != "enabled" && enabled != "disabled" && enabled != "not-found" {
		return false, false, errInstanceBusy
	}
	if active != "active" && active != "inactive" && active != "unknown" {
		return false, false, errInstanceBusy
	}
	return enabled == "enabled", active == "active", nil
}
func (b *instanceRuntimeBackend) restoreBackupSchedule(ctx context.Context, id string, p instanceBackupApply) error {
	unit := instanceBackupUnit(id, "timer")
	if _, err := os.Lstat(filepath.Join(instanceBackupUnits, unit)); err == nil {
		if _, err = b.command(ctx, "systemctl", "disable", "--now", unit); err != nil {
			return errInstanceBusy
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, item := range []struct {
		suffix string
		data   []byte
		had    bool
	}{{"service", p.Service, p.HadService}, {"timer", p.Timer, p.HadTimer}} {
		path := filepath.Join(instanceBackupUnits, instanceBackupUnit(id, item.suffix))
		var err error
		if item.had {
			err = b.writePrivate(path, item.data, true)
		} else {
			err = b.removePrivate(path)
		}
		if err != nil {
			return err
		}
	}
	if err := b.writeJSON(b.backupPath(id, "settings"), p.Previous, true); err != nil {
		return err
	}
	if _, err := b.command(ctx, "systemctl", "daemon-reload"); err != nil {
		return errInstanceBusy
	}
	if p.Enabled {
		if _, err := b.command(ctx, "systemctl", "enable", unit); err != nil {
			return errInstanceBusy
		}
	}
	if p.Active {
		if _, err := b.command(ctx, "systemctl", "start", unit); err != nil {
			return errInstanceBusy
		}
	}
	return b.removePrivate(b.backupPath(id, "schedule-pending"))
}
func (b *instanceRuntimeBackend) configureBackups(ctx context.Context, id string, v instanceBackupSettings) error {
	if _, err := b.readRecord(id); err != nil {
		return err
	}
	if !validInstanceBackupSettings(v) {
		return instanceRuntimeFailure("Use daily HH:MM and retention between 1 and 365.")
	}
	// Retrying PUT first rolls back any interrupted apply from durable evidence.
	pending, err := b.readPrivate(b.backupPath(id, "schedule-pending"))
	if err == nil {
		var p instanceBackupApply
		if instanceDecode(pending, &p) != nil {
			return errInstanceRecovery
		}
		if err = b.restoreBackupSchedule(ctx, id, p); err != nil {
			return instanceRuntimeFailure("Backup schedule rollback requires retrying this update.")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	old, err := b.backupSettings(id)
	if err != nil {
		return err
	}
	if _, e := os.Lstat(filepath.Join(instanceBackupUnits, instanceBackupUnit(id, "service"))); e == nil {
		out, e := b.command(ctx, "systemctl", "show", "--property=ActiveState", "--value", instanceBackupUnit(id, "service"))
		state := strings.TrimSpace(string(out))
		if e != nil || (state != "inactive" && state != "failed") {
			return errInstanceBusy
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if err = b.backupSeparation(ctx, id, v); err != nil {
		return err
	}
	p := instanceBackupApply{Previous: old}
	p.Enabled, p.Active, err = b.backupTimerState(ctx, id)
	if err != nil {
		return err
	}
	for _, item := range []struct {
		suffix string
		data   *[]byte
		had    *bool
	}{{"service", &p.Service, &p.HadService}, {"timer", &p.Timer, &p.HadTimer}} {
		*item.data, err = b.readPrivate(filepath.Join(instanceBackupUnits, instanceBackupUnit(id, item.suffix)))
		if err == nil {
			*item.had = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	units := map[string][]byte{}
	for _, suffix := range []string{"service", "timer"} {
		data, e := os.ReadFile(filepath.Join(instanceBackupTemplates, "minecraft-instance-backup."+suffix+".in"))
		if e != nil {
			return e
		}
		text := strings.ReplaceAll(string(data), "@@INSTANCE_ID@@", id)
		text = strings.ReplaceAll(text, "@@TIME@@", v.Time)
		units[suffix] = []byte(text)
	}
	if err = b.writeJSON(b.backupPath(id, "schedule-pending"), p, false); err != nil {
		return err
	}
	apply := func() error {
		if _, e := b.command(ctx, "systemctl", "disable", "--now", instanceBackupUnit(id, "timer")); e != nil && p.HadTimer {
			return e
		}
		for _, suffix := range []string{"service", "timer"} {
			if e := b.writePrivate(filepath.Join(instanceBackupUnits, instanceBackupUnit(id, suffix)), units[suffix], true); e != nil {
				return e
			}
		}
		if e := b.writeJSON(b.backupPath(id, "settings"), v, true); e != nil {
			return e
		}
		if _, e := b.command(ctx, "systemctl", "daemon-reload"); e != nil {
			return e
		}
		if v.Enabled {
			if _, e := b.command(ctx, "systemctl", "enable", "--now", instanceBackupUnit(id, "timer")); e != nil {
				return e
			}
		}
		return b.removePrivate(b.backupPath(id, "schedule-pending"))
	}
	if err = apply(); err != nil {
		recoveryCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if e := b.restoreBackupSchedule(recoveryCtx, id, p); e != nil {
			return instanceRuntimeFailure("Backup schedule apply and rollback failed; retry the settings update.")
		}
		return instanceRuntimeFailure("Backup schedule update failed and was rolled back.")
	}
	return nil
}

func registerInstanceBackupRoutes(mux *http.ServeMux, s *server) {
	registerInstanceRestoreRoutes(mux, s)
	mux.HandleFunc("GET /v1/minecraft/instances/{id}/backups", s.instanceBackupRead)
	mux.HandleFunc("GET /v1/minecraft/instances/{id}/backups/status", s.instanceBackupRead)
	mux.HandleFunc("GET /v1/minecraft/instances/{id}/backups/settings", s.instanceBackupRead)
	mux.HandleFunc("PUT /v1/minecraft/instances/{id}/backups/settings", s.instanceBackupConfigure)
	mux.HandleFunc("POST /v1/minecraft/instances/{id}/backups", s.instanceBackupNow)
	mux.HandleFunc("POST /v1/minecraft/instances/{id}/backups/run", s.instanceBackupRun)
}
func (s *server) instanceBackupRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireReadAccess(w, r); !ok {
		return
	}
	id, ok := instanceRequestID(w, r)
	if !ok {
		return
	}
	v, err := instanceRuntimes.backupSettings(id)
	if err != nil {
		instanceRuntimeError(w, err, id)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/settings") {
		writeJSON(w, 200, v)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/status") {
		status, e := instanceRuntimes.backupStatus(id)
		if e != nil {
			instanceRuntimeError(w, e, id)
			return
		}
		if status.State != "running" {
			writeJSON(w, http.StatusOK, status)
			return
		}
		// A saved in-progress state is never presented as success after interruption.
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		out, e := instanceRuntimes.command(ctx, "systemctl", "show", "--property=ActiveState", "--value", instanceBackupUnit(id, "service"))
		if e != nil {
			writeError(w, 503, "Backup service state is unavailable.")
			return
		}
		active := strings.TrimSpace(string(out))
		if status.State == "running" && active != "active" && active != "activating" {
			status.State = "interrupted"
			status.Message = "Backup was interrupted. Original running intent is preserved; an administrator can retry Backup Now."
		}
		writeJSON(w, 200, status)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	out, err := instanceRuntimes.backupStorage(ctx, "inventory", id)
	if err != nil {
		instanceRuntimeError(w, err, id)
		return
	}
	var inventory []instanceBackupMetadata
	if json.Unmarshal(out, &inventory) != nil {
		writeError(w, 503, "Backup inventory is unavailable.")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "backups": inventory})
}

// Public metadata is a strict allowlist; paths and private runtime fields cannot escape.
type instanceBackupMetadata struct {
	Schema              int    `json:"schema"`
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Software            string `json:"software"`
	Version             string `json:"version"`
	Created             string `json:"created"`
	Archive             string `json:"archive"`
	Size                int64  `json:"size"`
	ConfigurationSHA256 string `json:"configuration_sha256"`
	SHA256              string `json:"sha256"`
}

func (s *server) instanceBackupConfigure(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	id, ok := instanceRequestID(w, r)
	if !ok {
		return
	}
	var v instanceBackupSettings
	if !decodeJSON(w, r, &v) {
		return
	}
	instanceBackupMu.Lock()
	defer instanceBackupMu.Unlock()
	release, err := s.lockInstanceBackup()
	if err != nil {
		instanceRuntimeError(w, err, id)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	err = instanceRuntimes.configureBackups(ctx, id, v)
	err = errors.Join(err, release())
	if err != nil {
		instanceRuntimeError(w, err, id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}
func (s *server) instanceBackupNow(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireRoles(w, r, roleAdministrator, roleOperator)
	if !ok {
		return
	}
	id, ok := instanceRequestID(w, r)
	if !ok {
		return
	}
	if _, err := instanceRuntimes.backupSettings(id); err != nil {
		instanceRuntimeError(w, err, id)
		return
	}
	var request struct{}
	if !decodeJSON(w, r, &request) {
		return
	}
	operatorBackupMu.Lock()
	defer operatorBackupMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	// Probe shared protection before queueing. The worker acquires it again; no
	// reservation is inferred from this probe or from systemd job acceptance.
	release, err := s.lockInstanceBackup()
	if err != nil {
		instanceRuntimeError(w, err, id)
		return
	}
	err = release()
	if err != nil {
		instanceRuntimeError(w, err, id)
		return
	}
	var guard *operatorBackupGuard
	if actor.Role == roleOperator {
		if s.store == nil {
			writeError(w, 403, "Operator backup allowance is unavailable.")
			return
		}
		var available operatorBackupAvailability
		guard, available, err = s.store.beginOperatorBackup(actor.WebUserID)
		if err != nil {
			writeError(w, 503, "Operator backup allowance is unavailable.")
			return
		}
		if !available.Allowed {
			code := http.StatusConflict
			if available.Reason == "backup_cooldown" {
				code = http.StatusTooManyRequests
				w.Header().Set("Retry-After", strconv.Itoa(int(available.RetryAfter.Seconds())+1))
			}
			writeJSON(w, code, map[string]any{"error": available.Reason, "backup_used": available.Used, "backup_limit": available.Limit})
			return
		}
		defer guard.cancel()
	}
	// Manual backups also work before any schedule has been configured. Install
	// disabled units through the same rollback-capable settings apply.
	instanceBackupMu.Lock()
	v, e := instanceRuntimes.backupSettings(id)
	if e == nil {
		if _, e = os.Lstat(filepath.Join(instanceBackupUnits, instanceBackupUnit(id, "service"))); errors.Is(e, os.ErrNotExist) {
			release, e = s.lockInstanceBackup()
			if e == nil {
				e = instanceRuntimes.configureBackups(ctx, id, v)
				e = errors.Join(e, release())
			}
		}
	}
	instanceBackupMu.Unlock()
	if e != nil {
		instanceRuntimeError(w, e, id)
		return
	}
	out, err := instanceRuntimes.command(ctx, "systemctl", "show", "--property=ActiveState", "--value", instanceBackupUnit(id, "service"))
	state := strings.TrimSpace(string(out))
	if err != nil || (state != "inactive" && state != "failed") {
		writeError(w, 409, "Instance backup is active or unavailable.")
		return
	}
	if _, err = instanceRuntimes.command(ctx, "systemctl", "--no-block", "start", instanceBackupUnit(id, "service")); err != nil {
		writeError(w, 503, "Instance backup request failed.")
		return
	}
	if guard != nil {
		if _, err = guard.accept(actor); err != nil {
			writeError(w, 503, "Backup requested but allowance accounting failed.")
			return
		}
	} else if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "instance_backup", id, true, "Instance backup requested")
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "id": id})
}
func (s *server) instanceBackupRun(w http.ResponseWriter, r *http.Request) {
	// Native systemd worker only. Web sessions, including administrators, cannot
	// bypass manual quota or submit a second direct long-running operation.
	if _, ok := localRootAdministrator(r); !ok {
		writeError(w, 403, "Local root worker required.")
		return
	}
	id, ok := instanceRequestID(w, r)
	if !ok {
		return
	}
	var request struct{}
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(6 * time.Hour)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		writeError(w, 503, "Backup worker connection is unavailable.")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Hour)
	defer cancel()
	if err := s.runInstanceBackup(ctx, id); err != nil {
		instanceRuntimeError(w, err, id)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "id": id})
}

// Reserve existing operation locks without waiting behind active maintenance.
// Other lifecycle operations retain their existing locking behavior.
func (s *server) lockInstanceBackup() (func() error, error) {
	if instanceRestoreActive.Load() {
		return nil, errInstanceBusy
	}
	if s.operations == nil {
		return nil, errInstanceBusy
	}
	o := s.operations
	if !o.mu.TryLock() {
		return nil, errInstanceBusy
	}
	if o.currentSetupID != "" || o.currentRestoreID != "" || o.currentDataMigrationID != "" || o.currentMigrationID != "" || o.currentMinecraftResetID != "" || o.currentFactoryResetID != "" {
		o.mu.Unlock()
		return nil, errInstanceBusy
	}
	if err := instanceRuntimes.requireNoInstanceRestore(); err != nil {
		o.mu.Unlock()
		return nil, err
	}
	if err := o.acquireMinecraftResetLocks(); err != nil {
		o.mu.Unlock()
		return nil, errInstanceBusy
	}
	lock, err := acquirePluginRuntimeLock()
	if err != nil {
		e := o.releaseMinecraftResetLocks()
		o.mu.Unlock()
		return nil, errors.Join(errInstanceBusy, e)
	}
	return func() error {
		e := lock.Close()
		e = errors.Join(e, o.releaseMinecraftResetLocks())
		o.mu.Unlock()
		return e
	}, nil
}

func (s *server) runInstanceBackup(ctx context.Context, id string) (result error) {
	if !instanceBackupWorkerMu.TryLock() {
		return errInstanceBusy
	}
	defer instanceBackupWorkerMu.Unlock()
	release, err := s.lockInstanceBackup()
	if err != nil {
		return err
	}
	held := true
	defer func() {
		if held {
			result = errors.Join(result, release())
		}
	}()
	b := instanceRuntimes
	if _, err = b.backupSettings(id); err != nil {
		return err
	}
	if _, err = b.readPrivate(b.backupPath(id, "schedule-pending")); err == nil {
		return instanceRuntimeFailure("Backup settings update requires retry before backup.")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	old, err := b.backupStatus(id)
	if err != nil {
		return err
	}
	status := instanceBackupStatus{LastAttempt: time.Now().UTC().Format(time.RFC3339), LastSuccess: old.LastSuccess, State: "running", Stage: "preflight", OriginallyRunning: old.RestartRequired, RestartRequired: old.RestartRequired}
	save := func() error { return b.writeJSON(b.backupPath(id, "status"), status, true) }
	if err = save(); err != nil {
		return err
	}
	defer func() {
		// Release before START: Phase 3B ExecStartPre must acquire maintenance.
		if held {
			result = errors.Join(result, release())
			held = false
		}
		if status.RestartRequired {
			restartCtx, cancel := context.WithTimeout(context.Background(), 16*time.Minute)
			defer cancel()
			state, e := b.observe(restartCtx, id)
			if e == nil && state.State == "running" {
				status.RestartRequired = false
			} else if e == nil && state.State == "stopped" {
				_, e = b.command(restartCtx, "systemctl", "start", "--", "minecraft-"+id+".service")
				if e == nil {
					state, e = b.observe(restartCtx, id)
					if e == nil && state.State != "running" {
						e = errInstanceBusy
					}
				}
				if e == nil {
					status.RestartRequired = false
				}
			} else {
				e = errInstanceBusy
			}
			if e != nil {
				result = errors.Join(result, instanceRuntimeFailure("Original instance restart could not be verified; running intent is retained."))
			}
		}
		// Retention cannot discard an older successful backup before required
		// restart has also succeeded. A retention problem is a reported warning on
		// an already completed backup, not a false archive/restart failure.
		if result == nil {
			status.Stage = "retention"
			retentionRelease, e := s.lockInstanceBackup()
			if e == nil {
				_, e = b.backupStorage(ctx, "retain", id)
				e = errors.Join(e, retentionRelease())
			}
			if e != nil {
				status.Message = "Backup completed; independent retention could not complete. Existing unretired files are preserved."
			}
		}
		if result != nil {
			status.State = "failed"
			status.Message = "Backup failed at " + status.Stage + ". Published archives are preserved."
			if status.RestartRequired {
				status.Message += " Restart is still required."
			}
		} else {
			status.State = "succeeded"
			status.Stage = "complete"
		}
		result = errors.Join(result, save())
	}()
	state, err := b.observe(ctx, id)
	if err != nil {
		return err
	}
	if state.State != "running" && state.State != "stopped" {
		return instanceRuntimeFailure("Instance must be safely running or stopped before backup.")
	}
	status.OriginallyRunning = state.State == "running" || old.RestartRequired
	if err = save(); err != nil {
		return err
	}
	if _, err = b.backupStorage(ctx, "preflight", id); err != nil {
		return err
	}
	if state.State == "running" {
		if _, err = b.players(ctx, id); err != nil {
			return err
		}
		// Persist restart intent before issuing even a graceful stop request.
		status.RestartRequired = true
		status.Stage = "shutdown"
		if err = save(); err != nil {
			return err
		}
		if err = b.actionWithWarnings(ctx, id, "stop", true); err != nil {
			return err
		}
	}
	status.Stage = "archive"
	if err = save(); err != nil {
		return err
	}
	if _, err = b.backupStorage(ctx, "create", id); err != nil {
		return err
	}
	status.ArchivePublished = true
	status.LastSuccess = time.Now().UTC().Format(time.RFC3339)
	status.Stage = "restart"
	if err = save(); err != nil {
		return err
	}
	return nil
}
