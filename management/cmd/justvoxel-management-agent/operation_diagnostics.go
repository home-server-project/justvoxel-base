package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const operationDiagnosticLimit = 4 * 1024 * 1024

var operationDiagnosticSensitivePattern = regexp.MustCompile(`(?i)password|secret|token|cookie|authorization|credential|api[_-]?key`)

type diagnosticLogEntry struct {
	Category      string `json:"category"`
	LogID         string `json:"log_id"`
	OperationType string `json:"operation_type,omitempty"`
	OperationID   string `json:"operation_id,omitempty"`
	State         string `json:"state,omitempty"`
	Timestamp     string `json:"timestamp"`
}

func registerAdminDiagnosticLogRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("POST /v1/admin/logs/support", s.adminSupportReport)
	mux.HandleFunc("POST /v1/admin/logs/network", s.adminNetworkReport)
	mux.HandleFunc("GET /v1/admin/logs", s.adminDiagnosticLogList)
	mux.HandleFunc("GET /v1/admin/logs/{category}/{id}", s.adminDiagnosticLogRead)
}

func (s *server) adminDiagnosticLogList(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	if s.operations == nil {
		writeError(w, http.StatusServiceUnavailable, "diagnostic logs are unavailable")
		return
	}
	entries, err := s.operations.listDiagnosticLogs()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "diagnostic logs could not be listed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": entries})
}

func (s *server) adminDiagnosticLogRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	if s.operations == nil {
		writeError(w, http.StatusServiceUnavailable, "diagnostic logs are unavailable")
		return
	}
	category, id := r.PathValue("category"), r.PathValue("id")
	if !validDiagnosticCategory(category) || !validOperationID(id) {
		writeError(w, http.StatusBadRequest, "invalid diagnostic log identifier")
		return
	}
	data, err := s.operations.readDiagnosticLog(category, id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, errOperationNotFound) {
			writeError(w, http.StatusNotFound, "diagnostic log not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "diagnostic log could not be read")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="justvoxel-%s-%s.log"`, category, id))
	}
	_, _ = w.Write(data)
}

func (s *operationStore) appendOperationJournalDiagnosticBestEffort(journal operationJournal) {
	if s == nil || journal.OperationType == operationTypeSetup || !validOperationID(journal.OperationID) {
		return
	}
	_ = s.appendOperationDiagnostic(journal.OperationID, journal.OperationType, "STAGE", "operation progress", map[string]string{
		"state": string(journal.State), "stage": journal.Stage, "status": journal.Status,
		"rollback_state": journal.Rollback.State, "rollback_result": journal.Rollback.Result,
	})
}

func (s *operationStore) appendOperationDiagnostic(id, operationType, category, message string, values map[string]string) error {
	if s == nil || s.logsDir == "" || !validOperationID(id) || !validDiagnosticOperationType(operationType) {
		return errors.New("invalid operation diagnostic identity")
	}
	s.diagnosticMu.Lock()
	defer s.diagnosticMu.Unlock()
	if err := validatePrivateDiagnosticDirectory(s.logsDir); err != nil {
		return err
	}
	dir := filepath.Join(s.logsDir, diagnosticCategoryForOperationType(operationType)+"-logs")
	if err := ensurePrivateDirectory(dir); err != nil {
		return err
	}
	path := filepath.Join(dir, id+".log")
	file, err := openPrivateDiagnosticFile(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() >= operationDiagnosticLimit {
		return nil
	}
	size := info.Size()
	if size == 0 {
		written, err := fmt.Fprintf(file, "JustVoxel %s Diagnostic Log\noperation_id=%s\nstarted_at=%s\nprivacy=secrets and host identity redacted\n\n", operationType, id, s.now().UTC().Format("2006-01-02T15:04:05.999999999Z07:00"))
		if err != nil {
			return err
		}
		size += int64(written)
	}
	var line strings.Builder
	fmt.Fprintf(&line, "[%s] %s %s", s.now().UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), sanitizeSetupDiagnosticText(category, s.diagnosticHostname), sanitizeSetupDiagnosticText(message, s.diagnosticHostname))
	privateKey := ""
	if diagnosticCategoryForOperationType(operationType) == "plugins" {
		privateKey, _ = readCurseForgeKey()
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !setupDiagnosticKeyPattern.MatchString(key) {
			continue
		}
		value := values[key]
		if privateKey != "" {
			value = strings.ReplaceAll(value, privateKey, "<REDACTED>")
		}
		if operationDiagnosticSensitivePattern.MatchString(value) {
			value = "<sensitive diagnostic detail redacted>"
		}
		if len(value) > 2048 {
			value = value[:2048] + "…"
		}
		value = sanitizeSetupDiagnosticValue(key, value, s.diagnosticHostname)
		// Migration bundle extensions are filenames, not hostnames. Preserve the
		// known suffix while retaining redaction of the stem and destination.
		if operationType == operationTypeMigrationExport && (key == "file" || key == "destination") {
			original := values[key]
			filename := filepath.Base(original)
			if len(original) <= 2048 && !operationDiagnosticSensitivePattern.MatchString(original) && migrationExportFilenamePattern.MatchString(filename) {
				value = sanitizeSetupDiagnosticValue(key, strings.TrimSuffix(original, ".tar.gz"), s.diagnosticHostname) + ".tar.gz"
			}
		}
		fmt.Fprintf(&line, " %s=%q", key, value)
	}
	line.WriteByte('\n')
	remaining := operationDiagnosticLimit - size
	if remaining < 0 {
		return nil
	}
	data := []byte(line.String())
	if int64(len(data)) > remaining {
		data = []byte("[diagnostic log size limit reached]\n")
		if int64(len(data)) > remaining {
			return nil
		}
	}
	_, err = file.Write(data)
	return err
}

func validDiagnosticOperationType(value string) bool {
	switch value {
	case pluginReviewDiagnosticType, operationTypePluginAddonInstall, operationTypePluginInstall, operationTypePluginEnable, operationTypePluginDisable, operationTypePluginRemove, operationTypeRestore, operationTypeDataMigration, operationTypeMinecraftReset, operationTypeFactoryReset, operationTypeMigrationExport, operationTypeMigrationImport, operationTypeMigrationRecovery:
		return true
	}
	return false
}

func validDiagnosticCategory(value string) bool {
	switch value {
	case "setup", "migration", "restore", "plugins", "reset", "network", "update", "support", "operation":
		return true
	}
	return false
}

func diagnosticCategoryForOperationType(value string) string {
	switch value {
	case operationTypeSetup:
		return "setup"
	case operationTypeDataMigration, operationTypeMigrationExport, operationTypeMigrationImport, operationTypeMigrationRecovery:
		return "migration"
	case operationTypeRestore:
		return "restore"
	case pluginReviewDiagnosticType, operationTypePluginAddonInstall, operationTypePluginInstall, operationTypePluginEnable, operationTypePluginDisable, operationTypePluginRemove:
		return "plugins"
	case operationTypeMinecraftReset, operationTypeFactoryReset:
		return "reset"
	default:
		return ""
	}
}

func validatePrivateDiagnosticDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	ownership, owned := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 || !owned || ownership.Uid != uint32(os.Geteuid()) {
		return errors.New("unsafe diagnostic log directory")
	}
	return nil
}

func openPrivateDiagnosticFile(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_APPEND|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	ownership, owned := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || !owned || ownership.Uid != uint32(os.Geteuid()) {
		file.Close()
		return nil, errors.New("unsafe diagnostic log file")
	}
	return file, nil
}

func (s *operationStore) readDiagnosticLog(category, id string) ([]byte, error) {
	if !validOperationID(id) {
		return nil, errors.New("invalid diagnostic log identifier")
	}
	if category == "setup" {
		return s.readSetupDiagnostic(id)
	}
	if !validDiagnosticCategory(category) || category == "setup" {
		return nil, errors.New("invalid diagnostic log category")
	}
	if category == "operation" {
		journal, err := s.get(id)
		if err != nil {
			return nil, err
		}
		if !validDiagnosticOperationType(journal.OperationType) {
			return nil, os.ErrNotExist
		}
		category = diagnosticCategoryForOperationType(journal.OperationType)
	} else if journal, err := s.get(id); category != "support" && category != "network" && err == nil && diagnosticCategoryForOperationType(journal.OperationType) != category {
		return nil, os.ErrNotExist
	}
	s.diagnosticMu.Lock()
	defer s.diagnosticMu.Unlock()
	if err := validatePrivateDiagnosticDirectory(s.logsDir); err != nil {
		return nil, err
	}
	dir := s.diagnosticCategoryDirectory(category)
	if err := validatePrivateDiagnosticDirectory(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, id+".log")
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	ownership, owned := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() > operationDiagnosticLimit || !owned || ownership.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("unsafe diagnostic log file")
	}
	return io.ReadAll(io.LimitReader(file, operationDiagnosticLimit+1))
}

func (s *operationStore) listDiagnosticLogs() ([]diagnosticLogEntry, error) {
	s.mu.Lock()
	operations := make(map[string]operationJournal, len(s.operations))
	for id, journal := range s.operations {
		operations[id] = journal
	}
	s.mu.Unlock()
	s.diagnosticMu.Lock()
	defer s.diagnosticMu.Unlock()
	entries := make([]diagnosticLogEntry, 0)
	if err := validatePrivateDiagnosticDirectory(s.logsDir); err != nil {
		return nil, err
	}
	for _, category := range []string{"setup", "migration", "restore", "plugins", "reset", "network", "update", "support"} {
		dir := s.diagnosticCategoryDirectory(category)
		if err := validatePrivateDiagnosticDirectory(dir); err != nil {
			return nil, err
		}
		files, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		categoryEntries := make([]diagnosticLogEntry, 0, len(files))
		for _, file := range files {
			if file.Type() != 0 || !strings.HasSuffix(file.Name(), ".log") {
				continue
			}
			id := strings.TrimSuffix(file.Name(), ".log")
			if !validOperationID(id) {
				continue
			}
			info, err := file.Info()
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() > operationDiagnosticLimit {
				continue
			}
			owner, owned := info.Sys().(*syscall.Stat_t)
			if !owned || owner.Uid != uint32(os.Geteuid()) {
				continue
			}
			entry := diagnosticLogEntry{Category: category, LogID: id, Timestamp: info.ModTime().UTC().Format("2006-01-02T15:04:05Z07:00")}
			if journal, known := operations[id]; known && diagnosticCategoryForOperationType(journal.OperationType) == category {
				entry.OperationID = id
				entry.OperationType = journal.OperationType
				entry.State = string(journal.State)
			}
			categoryEntries = append(categoryEntries, entry)
		}
		sort.Slice(categoryEntries, func(i, j int) bool { return categoryEntries[i].Timestamp > categoryEntries[j].Timestamp })
		if len(categoryEntries) > 200 {
			categoryEntries = categoryEntries[:200]
		}
		entries = append(entries, categoryEntries...)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Timestamp > entries[j].Timestamp })
	return entries, nil
}

// Support predates the operation category directories and retains its established path.
func (s *operationStore) diagnosticCategoryDirectory(category string) string {
	if category == "support" {
		return filepath.Join(s.logsDir, "support")
	}
	return filepath.Join(s.logsDir, category+"-logs")
}

// The error text is browser-safe; command output is private diagnostic evidence.
type pluginRuntimeVerificationError struct{ output string }

func (*pluginRuntimeVerificationError) Error() string {
	return "Existing Minecraft stack verification failed"
}

func recordPluginFailure(store *operationStore, id, stage string, cause error) {
	if store == nil || cause == nil {
		return
	}
	journal, err := store.get(id)
	if err != nil {
		return
	}
	_ = store.appendOperationDiagnostic(id, journal.OperationType, stage, "Plugin operation failed", map[string]string{"reason": cause.Error()})
	var provider *pluginProviderDiagnosticError
	if errors.As(cause, &provider) {
		_ = store.appendOperationDiagnostic(id, journal.OperationType, stage, "Provider artifact verification evidence", provider.values)
	}
	var verification *pluginRuntimeVerificationError
	if !errors.As(cause, &verification) {
		return
	}
	// Preserve the existing 64 KiB command bound, split into bounded entries
	// before the existing per-value sanitizer/redactor and 4 MiB log cap.
	output := verification.output[:min(len(verification.output), 64<<10)]
	if key, err := readCurseForgeKey(); err == nil && key != "" {
		output = strings.ReplaceAll(output, key, "<REDACTED>")
	}
	var sanitized strings.Builder
	for _, line := range strings.Split(output, "\n") {
		if operationDiagnosticSensitivePattern.MatchString(line) {
			line = "<sensitive diagnostic detail redacted>"
		}
		line = sanitizeSetupDiagnosticValue("detail", line, store.diagnosticHostname)
		sanitized.WriteString(line)
		sanitized.WriteByte('\n')
	}
	output = sanitized.String()
	output = output[:min(len(output), 64<<10)]
	for output != "" {
		part := output[:min(len(output), 2048)]
		output = output[len(part):]
		_ = store.appendOperationDiagnostic(id, journal.OperationType, stage, "Stack verification output", map[string]string{"detail": part})
	}
}

func recordPluginRollbackResult(store *operationStore, id string) {
	if journal, err := store.get(id); err == nil {
		_ = store.appendOperationDiagnostic(id, journal.OperationType, "rollback_result", "Plugin rollback result", map[string]string{"state": string(journal.State), "result": journal.Status})
	}
}

// A diagnostic category identity only: never a maintenance transaction or journal.
const pluginReviewDiagnosticType = "plugin_review"

type pluginProviderDiagnosticError struct {
	reason string
	values map[string]string
}

func (e *pluginProviderDiagnosticError) Error() string { return e.reason }

func pluginArtifactDiagnostic(a pluginInstallArtifact, stage string) map[string]string {
	algorithm := "SHA-512"
	switch pluginSource(a.Source) {
	case "CurseForge":
		algorithm = "SHA-1"
	case "Hangar":
		algorithm = "SHA-256"
	}
	values := map[string]string{"provider": pluginSource(a.Source), "project_id": a.ProjectID, "release_id": a.VersionID, "stage": stage, "expected_bytes": strconv.FormatInt(a.Size, 10), "hash_algorithm": algorithm, "hash_result": "not checked"}
	if safePluginFilename(a.Filename) {
		values["filename"] = a.Filename
	}
	// Never retain userinfo, query strings, response headers or body. Only an
	// already validated official artifact location reaches the normal sanitizer.
	if validPluginDownloadMetadata(a) {
		if u, err := url.Parse(a.URL); err == nil {
			values["artifact_location"] = u.Host + u.EscapedPath()
		}
	}
	return values
}

func (s *server) writePluginReviewFailure(w http.ResponseWriter, source, project, release, stage string, cause error) {
	values := map[string]string{"provider": pluginSource(source), "project_id": project, "release_id": release, "stage": stage, "reason": cause.Error()}
	var provider *pluginProviderDiagnosticError
	if errors.As(cause, &provider) {
		for key, value := range provider.values {
			values[key] = value
		}
	}
	id := ""
	if s.operations != nil {
		if candidate, err := newOperationID(); err == nil {
			if err := s.operations.appendOperationDiagnostic(candidate, pluginReviewDiagnosticType, stage, "Plugin provider review failed; Minecraft was not changed", values); err == nil {
				id = candidate
			}
		}
	}
	payload := map[string]any{"error": cause.Error()}
	if id != "" {
		payload["diagnostic_id"] = id
	}
	writeJSON(w, http.StatusConflict, payload)
}

// Keep command errors and output private, while preserving errors.Is semantics.
type pluginStartError struct {
	cause  error
	output string
}

func (*pluginStartError) Error() string   { return "Minecraft could not start" }
func (e *pluginStartError) Unwrap() error { return e.cause }

func recordPluginStartFailure(store *operationStore, id, path string, names []string, cause error) {
	if store == nil {
		return
	}
	journal, err := store.get(id)
	if err != nil || !isPluginOperationType(journal.OperationType) || validatePluginInstance(journal.InstanceID, "") != nil {
		return
	}
	recordPluginFailure(store, id, "start_failure", cause)
	var start *pluginStartError
	if errors.As(cause, &start) {
		recordPluginCommandEvidence(store, id, journal.OperationType, "systemctl_start", []byte(start.cause.Error()+"\n"+start.output))
	}
	// Independent bounded context so cancellation of the operation does not
	// erase evidence. Collection is best effort and cannot veto rollback.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	unit := "minecraft-" + journal.InstanceID + ".service"
	for _, command := range []struct {
		name, stage string
		args        []string
	}{
		{"systemctl", "service_state", []string{"show", unit, "--no-pager", "--property=ActiveState,SubState,Result,ExecMainCode,ExecMainStatus"}},
		{"journalctl", "service_journal", []string{"-b", "-u", unit, "--no-pager", "-n", "80", "-o", "short-iso"}},
	} {
		output, err := pluginInstallCommand(ctx, command.name, command.args...)
		if err != nil {
			_ = store.appendOperationDiagnostic(id, journal.OperationType, command.stage, "Startup evidence command unavailable", nil)
		}
		recordPluginCommandEvidence(store, id, journal.OperationType, command.stage, output)
	}
	recordPluginStartupEvidence(store, id, path, names)
}

func recordPluginCommandEvidence(store *operationStore, id, operationType, stage string, raw []byte) {
	output := string(raw[:min(len(raw), 64<<10)])
	if key, err := readCurseForgeKey(); err == nil && key != "" {
		output = strings.ReplaceAll(output, key, "<REDACTED>")
	}
	for _, line := range strings.Split(output, "\n") {
		if pluginDiagnosticSensitivePattern.MatchString(line) || strings.Contains(strings.ToLower(line), "csrf") || strings.Contains(line, "://") || strings.ContainsAny(line, "?@\\") {
			line = "<sensitive diagnostic detail redacted>"
		}
		// Sanitize before splitting, so a secret or hostname cannot escape
		// redaction by crossing the per-value length boundary.
		line = sanitizeSetupDiagnosticValue("detail", line, store.diagnosticHostname)
		for line != "" {
			part := line[:min(len(line), 2048)]
			line = line[len(part):]
			_ = store.appendOperationDiagnostic(id, operationType, stage, "Minecraft start failure evidence", map[string]string{"detail": part})
		}
	}
}
