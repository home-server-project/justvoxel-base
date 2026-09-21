package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	setupDiagnosticSchemaVersion = "v1"
	setupDiagnosticRequestLimit  = 32 * 1024
)

var (
	setupDiagnosticIPv4Pattern = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	setupDiagnosticKeyPattern  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)
	runBootcStatusJSON         = func(ctx context.Context) ([]byte, error) {
		return exec.CommandContext(ctx, "bootc", "status", "--json").CombinedOutput()
	}
)

type setupDiagnosticSessionRequest struct {
	Interface string `json:"interface"`
}

type setupDiagnosticSessionResponse struct {
	OK            bool   `json:"ok"`
	SchemaVersion string `json:"schema_version"`
	SessionID     string `json:"session_id"`
}

type setupDiagnosticEventRequest struct {
	Event  string            `json:"event"`
	Values map[string]string `json:"values"`
}

type setupDiagnosticImageIdentity struct {
	Reference string
	Digest    string
}

func registerAdminSetupDiagnosticRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("POST /v1/admin/setup/diagnostics/session", s.adminSetupDiagnosticSession)
	mux.HandleFunc("POST /v1/admin/setup/diagnostics/{id}/event", s.adminSetupDiagnosticEvent)
	mux.HandleFunc("GET /v1/admin/setup/diagnostics/{id}/log", s.adminSetupDiagnosticLog)
}

func (s *server) adminSetupDiagnosticSession(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	if s.operations == nil {
		writeError(w, http.StatusServiceUnavailable, "setup diagnostic logging is unavailable")
		return
	}
	var request setupDiagnosticSessionRequest
	if !decodeSetupDiagnosticJSON(w, r, &request) {
		return
	}
	request.Interface = strings.TrimSpace(request.Interface)
	if request.Interface != "webui" && request.Interface != "mjust" {
		writeError(w, http.StatusBadRequest, "invalid setup diagnostic interface")
		return
	}
	id, err := s.operations.beginSetupDiagnostic(request.Interface)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "setup diagnostic session could not be created")
		return
	}
	writeJSON(w, http.StatusCreated, setupDiagnosticSessionResponse{OK: true, SchemaVersion: setupDiagnosticSchemaVersion, SessionID: id})
}

func (s *server) adminSetupDiagnosticEvent(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	if s.operations == nil {
		writeError(w, http.StatusServiceUnavailable, "setup diagnostic logging is unavailable")
		return
	}
	id := r.PathValue("id")
	if !validOperationID(id) {
		writeError(w, http.StatusBadRequest, "invalid setup diagnostic id")
		return
	}
	var request setupDiagnosticEventRequest
	if !decodeSetupDiagnosticJSON(w, r, &request) {
		return
	}
	request.Event = strings.TrimSpace(request.Event)
	if request.Event == "" || len(request.Event) > 120 || strings.ContainsAny(request.Event, "\r\n") {
		writeError(w, http.StatusBadRequest, "invalid setup diagnostic event")
		return
	}
	if len(request.Values) > 64 {
		writeError(w, http.StatusBadRequest, "too many setup diagnostic values")
		return
	}
	for key, value := range request.Values {
		if !setupDiagnosticKeyPattern.MatchString(key) || len(value) > 2048 || strings.ContainsAny(value, "\r\n") {
			writeError(w, http.StatusBadRequest, "invalid setup diagnostic value")
			return
		}
	}
	if err := s.operations.appendSetupDiagnostic(id, "USER", request.Event, request.Values); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, "setup diagnostic session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "setup diagnostic event could not be recorded")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) adminSetupDiagnosticLog(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	if s.operations == nil {
		writeError(w, http.StatusServiceUnavailable, "setup diagnostic logging is unavailable")
		return
	}
	id := r.PathValue("id")
	if !validOperationID(id) {
		writeError(w, http.StatusBadRequest, "invalid setup diagnostic id")
		return
	}
	data, err := s.operations.readSetupDiagnostic(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, "setup diagnostic log not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "setup diagnostic log could not be read")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="justvoxel-setup-%s.log"`, id))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func decodeSetupDiagnosticJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, setupDiagnosticRequestLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid JSON request")
		return false
	}
	return true
}

func (s *operationStore) beginSetupDiagnostic(interfaceName string) (string, error) {
	if s == nil || s.setupLogsDir == "" {
		return "", errors.New("setup diagnostic store is unavailable")
	}
	id, err := newOperationID()
	if err != nil {
		return "", err
	}
	s.diagnosticMu.Lock()
	defer s.diagnosticMu.Unlock()
	if err := s.createSetupDiagnosticLocked(id, interfaceName); err != nil {
		return "", err
	}
	return id, nil
}

func (s *operationStore) beginSetupWithDiagnostic(planFingerprint, sessionID string) (operationJournal, bool, error) {
	operation, created, err := s.beginSetup(planFingerprint)
	if err != nil {
		return operationJournal{}, false, err
	}
	if validOperationID(sessionID) {
		if err := s.bindSetupDiagnostic(sessionID, operation.OperationID, planFingerprint); err == nil {
			_ = s.appendSetupDiagnostic(operation.OperationID, "APPLY", "reviewed setup accepted for execution", map[string]string{
				"operation_id":     operation.OperationID,
				"plan_fingerprint": planFingerprint,
			})
			return operation, created, nil
		}
	}
	_ = s.ensureSetupOperationDiagnostic(operation.OperationID)
	_ = s.appendSetupDiagnostic(operation.OperationID, "APPLY", "reviewed setup accepted for execution", map[string]string{
		"operation_id":     operation.OperationID,
		"plan_fingerprint": planFingerprint,
	})
	return operation, created, nil
}

func (s *operationStore) ensureSetupOperationDiagnostic(operationID string) error {
	if s == nil || !validOperationID(operationID) {
		return errors.New("invalid setup operation id")
	}
	s.diagnosticMu.Lock()
	defer s.diagnosticMu.Unlock()
	path := s.setupDiagnosticPath(operationID)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.createSetupDiagnosticLocked(operationID, "operation")
}

func (s *operationStore) createSetupDiagnosticLocked(id, interfaceName string) error {
	path := s.setupDiagnosticPath(id)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	image := collectSetupDiagnosticImageIdentity()
	hostname := s.diagnosticHostname
	lines := []string{
		"JustVoxel Setup Diagnostic Log",
		"schema=" + setupDiagnosticSchemaVersion,
		"session_id=" + id,
		"started_at=" + now,
		"interface=" + sanitizeSetupDiagnosticText(interfaceName, hostname),
		"privacy=secrets redacted; IPv4/IPv6 addresses redacted; hostnames redacted",
		"image_reference=" + sanitizeSetupDiagnosticImageReference(image.Reference),
		"image_digest=" + sanitizeSetupDiagnosticImageDigest(image.Digest),
		"",
	}
	if _, err := io.WriteString(file, strings.Join(lines, "\n")); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func (s *operationStore) bindSetupDiagnostic(sessionID, operationID, planFingerprint string) error {
	if sessionID == "" {
		return nil
	}
	if !validOperationID(sessionID) || !validOperationID(operationID) || !operationFingerprintPattern.MatchString(planFingerprint) {
		return errors.New("invalid setup diagnostic binding")
	}
	s.diagnosticMu.Lock()
	defer s.diagnosticMu.Unlock()
	source := s.setupDiagnosticPath(sessionID)
	target := s.setupDiagnosticPath(operationID)
	if source == target {
		return nil
	}
	if _, err := os.Stat(target); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(source, target); err != nil {
		return err
	}
	return s.appendSetupDiagnosticLocked(operationID, "OPERATION", "setup session bound to persistent operation", map[string]string{
		"operation_id":     operationID,
		"plan_fingerprint": planFingerprint,
	})
}

func (s *operationStore) appendSetupDiagnostic(id, category, message string, values map[string]string) error {
	if s == nil || !validOperationID(id) {
		return errors.New("invalid setup diagnostic id")
	}
	s.diagnosticMu.Lock()
	defer s.diagnosticMu.Unlock()
	return s.appendSetupDiagnosticLocked(id, category, message, values)
}

func (s *operationStore) appendSetupDiagnosticLocked(id, category, message string, values map[string]string) error {
	path := s.setupDiagnosticPath(id)
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("setup diagnostic log is not a regular file")
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	timestamp := s.now().UTC().Format(time.RFC3339Nano)
	category = strings.ToUpper(strings.TrimSpace(category))
	if category == "" {
		category = "INFO"
	}
	message = sanitizeSetupDiagnosticText(message, s.diagnosticHostname)
	var line strings.Builder
	fmt.Fprintf(&line, "[%s] %s %s", timestamp, category, message)
	if len(values) > 0 {
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := sanitizeSetupDiagnosticValue(key, values[key], s.diagnosticHostname)
			fmt.Fprintf(&line, " %s=%q", key, value)
		}
	}
	line.WriteByte('\n')
	_, err = io.WriteString(file, line.String())
	return err
}

func (s *operationStore) readSetupDiagnostic(id string) ([]byte, error) {
	if s == nil || !validOperationID(id) {
		return nil, errors.New("invalid setup diagnostic id")
	}
	s.diagnosticMu.Lock()
	defer s.diagnosticMu.Unlock()
	path := s.setupDiagnosticPath(id)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("setup diagnostic log is not a regular file")
	}
	if info.Size() > 4*1024*1024 {
		return nil, errors.New("setup diagnostic log is too large")
	}
	return os.ReadFile(path)
}

func (s *operationStore) setupDiagnosticPath(id string) string {
	return filepath.Join(s.setupLogsDir, id+".log")
}

func (s *operationStore) appendSetupJournalDiagnosticBestEffort(journal operationJournal) {
	if s == nil || journal.OperationType != operationTypeSetup || !validOperationID(journal.OperationID) {
		return
	}
	_ = s.appendSetupDiagnostic(journal.OperationID, "STAGE", "setup operation progress", map[string]string{
		"state":           string(journal.State),
		"stage":           journal.Stage,
		"status":          journal.Status,
		"rollback_state":  journal.Rollback.State,
		"rollback_result": journal.Rollback.Result,
	})
}

func (s *operationStore) appendSetupWorkerErrorBestEffort(operationID string, err error) {
	if s == nil || err == nil || !validOperationID(operationID) {
		return
	}
	_ = s.appendSetupDiagnostic(operationID, "ERROR", "setup worker finished with an error", map[string]string{"error": err.Error()})
}

func setupDiagnosticPlanRequestValues(request adminSetupPlanRequest) map[string]string {
	return map[string]string{
		"motd":                 request.Server.MOTD,
		"max_players":          fmt.Sprintf("%d", request.Server.MaxPlayers),
		"bedrock_enabled":      fmt.Sprintf("%t", request.Server.BedrockEnabled),
		"timezone":             request.Server.Timezone,
		"java_memory":          request.Minecraft.JavaMemory,
		"container_memory":     request.Minecraft.ContainerMemory,
		"java_port":            fmt.Sprintf("%d", request.Minecraft.JavaPort),
		"bedrock_port":         fmt.Sprintf("%d", request.Minecraft.BedrockPort),
		"minecraft_image":      request.Minecraft.ImageTag,
		"version_policy":       request.Minecraft.VersionPolicy,
		"minecraft_version":    request.Minecraft.Version,
		"storage_type":         request.Storage.Type,
		"storage_path":         request.Storage.Path,
		"storage_device":       request.Storage.Device,
		"storage_mount_point":  request.Storage.MountPoint,
		"backup_automatic":     fmt.Sprintf("%t", request.Backups.Automatic),
		"backup_daily_time":    request.Backups.DailyTime,
		"backup_keep":          fmt.Sprintf("%d", request.Backups.Keep),
		"backup_type":          request.Backups.Type,
		"backup_path":          request.Backups.Path,
		"backup_device":        request.Backups.Device,
		"backup_mount_point":   request.Backups.MountPoint,
		"backup_source":        request.Backups.Source,
		"backup_username":      request.Backups.Username,
		"backup_domain":        request.Backups.Domain,
	}
}

func setupDiagnosticNormalizedPlanValues(plan *adminSetupNormalizedPlan) map[string]string {
	if plan == nil {
		return nil
	}
	return map[string]string{
		"motd":                 plan.Server.MOTD,
		"max_players":          fmt.Sprintf("%d", plan.Server.MaxPlayers),
		"bedrock_enabled":      fmt.Sprintf("%t", plan.Server.BedrockEnabled),
		"timezone":             plan.Server.Timezone,
		"java_memory":          plan.Minecraft.JavaMemory,
		"container_memory":     plan.Minecraft.ContainerMemory,
		"java_port":            fmt.Sprintf("%d", plan.Minecraft.JavaPort),
		"bedrock_port":         fmt.Sprintf("%d", plan.Minecraft.BedrockPort),
		"minecraft_image":      plan.Minecraft.ImageTag,
		"version_policy":       plan.Minecraft.VersionPolicy,
		"minecraft_version":    plan.Minecraft.Version,
		"storage_type":         plan.Storage.Type,
		"storage_path":         plan.Storage.Path,
		"storage_device":       plan.Storage.Device,
		"storage_mount_point":  plan.Storage.MountPoint,
		"backup_automatic":     fmt.Sprintf("%t", plan.Backups.Automatic),
		"backup_keep":          fmt.Sprintf("%d", plan.Backups.Keep),
		"backup_type":          plan.Backups.Type,
		"backup_path":          plan.Backups.Path,
		"backup_device":        plan.Backups.Device,
		"backup_mount_point":   plan.Backups.MountPoint,
		"backup_source":        plan.Backups.Source,
		"backup_username":      plan.Backups.Username,
		"backup_domain":        plan.Backups.Domain,
	}
}

func sanitizeSetupDiagnosticValue(key, value, hostname string) string {
	lower := strings.ToLower(key)
	for _, sensitive := range []string{"password", "secret", "token", "cookie", "authorization", "credential", "csrf"} {
		if strings.Contains(lower, sensitive) {
			return "<REDACTED>"
		}
	}
	if strings.Contains(lower, "source") || strings.Contains(lower, "server") || strings.Contains(lower, "host") {
		value = redactSetupDiagnosticNetworkLocation(value)
	}
	return sanitizeSetupDiagnosticText(value, hostname)
}

func sanitizeSetupDiagnosticText(value, hostname string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	if hostname != "" {
		value = replaceFold(value, hostname, "<HOSTNAME-REDACTED>")
	}
	value = setupDiagnosticIPv4Pattern.ReplaceAllStringFunc(value, func(candidate string) string {
		ip := net.ParseIP(candidate)
		if ip != nil && ip.To4() != nil {
			return "<IP-REDACTED>"
		}
		return candidate
	})
	fields := strings.Fields(value)
	for i, field := range fields {
		trimmed := strings.Trim(field, "[](),;<>\"")
		if strings.Count(trimmed, ":") >= 2 && net.ParseIP(strings.Trim(trimmed, "[]")) != nil {
			fields[i] = strings.Replace(field, trimmed, "<IP-REDACTED>", 1)
		}
	}
	return strings.Join(fields, " ")
}

func redactSetupDiagnosticNetworkLocation(value string) string {
	if strings.HasPrefix(value, "//") {
		rest := strings.TrimPrefix(value, "//")
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			return "//<HOSTNAME-REDACTED>" + rest[slash:]
		}
		return "//<HOSTNAME-REDACTED>"
	}
	if colon := strings.IndexByte(value, ':'); colon > 0 && !strings.Contains(value[:colon], "/") {
		return "<HOSTNAME-REDACTED>" + value[colon:]
	}
	return value
}

func replaceFold(value, old, replacement string) string {
	if old == "" {
		return value
	}
	lowerValue := strings.ToLower(value)
	lowerOld := strings.ToLower(old)
	var out strings.Builder
	for {
		index := strings.Index(lowerValue, lowerOld)
		if index < 0 {
			out.WriteString(value)
			break
		}
		out.WriteString(value[:index])
		out.WriteString(replacement)
		value = value[index+len(old):]
		lowerValue = lowerValue[index+len(old):]
	}
	return out.String()
}

func collectSetupDiagnosticImageIdentity() setupDiagnosticImageIdentity {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := runBootcStatusJSON(ctx)
	if err != nil {
		return setupDiagnosticImageIdentity{Reference: "<unavailable>", Digest: "<unavailable>"}
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return setupDiagnosticImageIdentity{Reference: "<unavailable>", Digest: "<unavailable>"}
	}
	booted := nestedMap(payload, "status", "booted")
	if booted == nil {
		return setupDiagnosticImageIdentity{Reference: "<unavailable>", Digest: "<unavailable>"}
	}
	reference := findImageReference(booted)
	digest := findStringRecursive(booted, map[string]bool{"imagedigest": true, "image_digest": true, "digest": true})
	if reference == "" {
		reference = "<unavailable>"
	}
	if digest == "" {
		digest = "<unavailable>"
	}
	return setupDiagnosticImageIdentity{Reference: reference, Digest: digest}
}

func nestedMap(payload map[string]any, path ...string) map[string]any {
	current := payload
	for _, key := range path {
		value, ok := current[key]
		if !ok {
			return nil
		}
		next, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		current = next
	}
	return current
}

func findImageReference(payload map[string]any) string {
	if value, ok := payload["image"].(string); ok {
		return value
	}
	if image, ok := payload["image"].(map[string]any); ok {
		if value, ok := image["image"].(string); ok {
			return value
		}
		if value, ok := image["reference"].(string); ok {
			return value
		}
	}
	return findStringRecursive(payload, map[string]bool{"imagereference": true, "image_reference": true, "reference": true})
}

func findStringRecursive(value any, keys map[string]bool) string {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
			if keys[normalized] {
				if text, ok := child.(string); ok && text != "" {
					return text
				}
			}
		}
		for _, child := range typed {
			if found := findStringRecursive(child, keys); found != "" {
				return found
			}
		}
	case []any:
		for _, child := range typed {
			if found := findStringRecursive(child, keys); found != "" {
				return found
			}
		}
	}
	return ""
}

func sanitizeSetupDiagnosticImageReference(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\r", ""), "\n", ""))
	if value == "" {
		return "<unavailable>"
	}
	return value
}

func sanitizeSetupDiagnosticImageDigest(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\r", ""), "\n", ""))
	if value == "" {
		return "<unavailable>"
	}
	return value
}
