package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
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
		if operationDiagnosticSensitivePattern.MatchString(value) {
			value = "<sensitive diagnostic detail redacted>"
		}
		if len(value) > 2048 {
			value = value[:2048] + "…"
		}
		fmt.Fprintf(&line, " %s=%q", key, sanitizeSetupDiagnosticValue(key, value, s.diagnosticHostname))
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
	case operationTypeRestore, operationTypeDataMigration, operationTypeMinecraftReset, operationTypeFactoryReset, operationTypeMigrationExport, operationTypeMigrationImport, operationTypeMigrationRecovery:
		return true
	}
	return false
}

func validDiagnosticCategory(value string) bool {
	switch value { case "setup", "migration", "restore", "reset", "operation": return true }
	return false
}

func diagnosticCategoryForOperationType(value string) string {
	switch value {
	case operationTypeSetup: return "setup"
	case operationTypeDataMigration, operationTypeMigrationExport, operationTypeMigrationImport, operationTypeMigrationRecovery: return "migration"
	case operationTypeRestore: return "restore"
	case operationTypeMinecraftReset, operationTypeFactoryReset: return "reset"
	default: return ""
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
		if err != nil { return nil, err }
		if !validDiagnosticOperationType(journal.OperationType) { return nil, os.ErrNotExist }
		category = diagnosticCategoryForOperationType(journal.OperationType)
	} else if journal, err := s.get(id); err == nil && diagnosticCategoryForOperationType(journal.OperationType) != category {
		return nil, os.ErrNotExist
	}
	s.diagnosticMu.Lock()
	defer s.diagnosticMu.Unlock()
	if err := validatePrivateDiagnosticDirectory(s.logsDir); err != nil {
		return nil, err
	}
	dir := filepath.Join(s.logsDir, category+"-logs")
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
	entries := make([]diagnosticLogEntry, 0)
	if err := validatePrivateDiagnosticDirectory(s.logsDir); err != nil {
		return nil, err
	}
	for _, category := range []string{"setup", "migration", "restore", "reset"} {
		dir := filepath.Join(s.logsDir, category+"-logs")
		if err := validatePrivateDiagnosticDirectory(dir); err != nil { return nil, err }
		files, err := os.ReadDir(dir)
		if err != nil { return nil, err }
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
		if !owned || owner.Uid != uint32(os.Geteuid()) { continue }
		entry := diagnosticLogEntry{Category: category, LogID: id, Timestamp: info.ModTime().UTC().Format("2006-01-02T15:04:05Z07:00")}
		if journal, known := operations[id]; known && diagnosticCategoryForOperationType(journal.OperationType) == category {
			entry.OperationID = id
			entry.OperationType = journal.OperationType
			entry.State = string(journal.State)
		}
		categoryEntries = append(categoryEntries, entry)
		}
		sort.Slice(categoryEntries, func(i, j int) bool { return categoryEntries[i].Timestamp > categoryEntries[j].Timestamp })
		if len(categoryEntries) > 200 { categoryEntries = categoryEntries[:200] }
		entries = append(entries, categoryEntries...)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Timestamp > entries[j].Timestamp })
	return entries, nil
}
