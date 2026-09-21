package main

import (
    "bytes"
    "context"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "errors"
    "io"
    "net/http"
    "os/exec"
    "strings"
    "time"
)

const adminMigrationRecoveryHelper = "/usr/libexec/justvoxel/mjust/admin-migration-recovery-json"
var adminMigrationRecoveryTimeout = 20 * time.Second
var runAdminMigrationRecoveryHelper = func(ctx context.Context, action string, request []byte) ([]byte, error) {
    cmd := exec.CommandContext(ctx, adminMigrationRecoveryHelper, action)
    if len(request) > 0 { cmd.Stdin = bytes.NewReader(request) }
    return cmd.CombinedOutput()
}

type adminMigrationRecoverySummary struct {
    Transaction string `json:"transaction"`
    Phase string `json:"phase"`
    SourceType string `json:"source_type"`
    UpdatedAt string `json:"updated_at"`
    Mode string `json:"mode"`
    Finalizable bool `json:"finalizable"`
}
type adminMigrationRecoveryDiscoveryResponse struct {
    OK bool `json:"ok"`
    SchemaVersion string `json:"schema_version"`
    Configured bool `json:"configured"`
    Transactions []adminMigrationRecoverySummary `json:"transactions"`
}
type adminMigrationRecoveryPlanRequest struct { Transaction string `json:"transaction"` }
type adminMigrationRecoveryRequirements struct {
    FinalizeConfirmationRequired bool `json:"finalize_confirmation_required"`
    RuntimeValidationRequired bool `json:"runtime_validation_required"`
    UnconfiguredValidationRequired bool `json:"unconfigured_validation_required"`
}
type adminMigrationRecoveryContext struct { StateIdentity string `json:"state_identity"` }
type adminMigrationRecoveryHelperResponse struct {
    OK bool `json:"ok"`
    SchemaVersion string `json:"schema_version"`
    Code string `json:"code,omitempty"`
    Error string `json:"error,omitempty"`
    Normalized *adminMigrationRecoverySummary `json:"normalized,omitempty"`
    Warnings []adminMigrationExportWarning `json:"warnings"`
    Requirements *adminMigrationRecoveryRequirements `json:"requirements,omitempty"`
    Context *adminMigrationRecoveryContext `json:"context,omitempty"`
}
type adminMigrationRecoveryPlanResponse struct {
    OK bool `json:"ok"`
    SchemaVersion string `json:"schema_version"`
    PlanFingerprint string `json:"plan_fingerprint,omitempty"`
    Code string `json:"code,omitempty"`
    Error string `json:"error,omitempty"`
    Normalized *adminMigrationRecoverySummary `json:"normalized,omitempty"`
    Warnings []adminMigrationExportWarning `json:"warnings"`
    Requirements *adminMigrationRecoveryRequirements `json:"requirements,omitempty"`
}

func registerAdminMigrationRecoveryRoutes(mux *http.ServeMux, s *server) {
    mux.HandleFunc("GET /v1/admin/migration/recovery", s.adminMigrationRecoveryDiscover)
    mux.HandleFunc("POST /v1/admin/migration/recovery/plan", s.adminMigrationRecoveryPlan)
}

func (s *server) adminMigrationRecoveryDiscover(w http.ResponseWriter, r *http.Request) {
    if _, ok := s.requireAdministrator(w, r); !ok { return }
    ctx, cancel := context.WithTimeout(r.Context(), adminMigrationRecoveryTimeout); defer cancel()
    output, err := runAdminMigrationRecoveryHelper(ctx, "discover", nil)
    if err != nil { writeError(w, http.StatusServiceUnavailable, "migration recovery discovery is unavailable"); return }
    var out adminMigrationRecoveryDiscoveryResponse
    if err := decodeAdminMigrationRecoveryJSON(output, &out); err != nil || !out.OK || out.SchemaVersion != "v1" { writeError(w, http.StatusInternalServerError, "migration recovery discovery returned invalid data"); return }
    if out.Transactions == nil { out.Transactions = []adminMigrationRecoverySummary{} }
    writeJSON(w, http.StatusOK, out)
}

func (s *server) adminMigrationRecoveryPlan(w http.ResponseWriter, r *http.Request) {
    if _, ok := s.requireAdministrator(w, r); !ok { return }
    var request adminMigrationRecoveryPlanRequest
    if !decodeAdminMigrationRecoveryRequest(w, r, &request) { return }
    payload, _ := json.Marshal(request)
    ctx, cancel := context.WithTimeout(r.Context(), adminMigrationRecoveryTimeout); defer cancel()
    output, err := runAdminMigrationRecoveryHelper(ctx, "plan", payload)
    if err != nil { writeError(w, http.StatusServiceUnavailable, "migration recovery planning is unavailable"); return }
    var helper adminMigrationRecoveryHelperResponse
    if err := decodeAdminMigrationRecoveryJSON(output, &helper); err != nil || helper.SchemaVersion != "v1" { writeError(w, http.StatusInternalServerError, "migration recovery planner returned invalid data"); return }
    if helper.Warnings == nil { helper.Warnings = []adminMigrationExportWarning{} }
    response := adminMigrationRecoveryPlanResponse{OK:helper.OK, SchemaVersion:helper.SchemaVersion, Code:helper.Code, Error:helper.Error, Normalized:helper.Normalized, Warnings:helper.Warnings, Requirements:helper.Requirements}
    if !helper.OK { response.Error = boundedMigrationRecoveryError(response.Error); writeJSON(w, http.StatusBadRequest, response); return }
    if helper.Normalized == nil || helper.Requirements == nil || helper.Context == nil || helper.Context.StateIdentity == "" { writeError(w, http.StatusInternalServerError, "migration recovery planner returned incomplete data"); return }
    fingerprint, err := adminMigrationRecoveryFingerprint(helper.SchemaVersion, helper.Normalized, helper.Requirements, helper.Context)
    if err != nil { writeError(w, http.StatusInternalServerError, "migration recovery plan identity could not be created"); return }
    response.PlanFingerprint = fingerprint
    writeJSON(w, http.StatusOK, response)
}

func adminMigrationRecoveryFingerprint(schema string, normalized *adminMigrationRecoverySummary, requirements *adminMigrationRecoveryRequirements, context *adminMigrationRecoveryContext) (string, error) {
    if schema == "" || normalized == nil || requirements == nil || context == nil || context.StateIdentity == "" { return "", errors.New("incomplete migration recovery plan") }
    payload, err := json.Marshal(struct { Schema string `json:"schema"`; Normalized *adminMigrationRecoverySummary `json:"normalized"`; Requirements *adminMigrationRecoveryRequirements `json:"requirements"`; StateIdentity string `json:"state_identity"` }{schema, normalized, requirements, context.StateIdentity})
    if err != nil { return "", err }
    sum := sha256.Sum256(payload)
    return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func decodeAdminMigrationRecoveryRequest(w http.ResponseWriter, r *http.Request, target *adminMigrationRecoveryPlanRequest) bool {
    decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)); decoder.DisallowUnknownFields()
    if err := decoder.Decode(target); err != nil { writeError(w, http.StatusBadRequest, "invalid JSON request"); return false }
    var trailing any
    if err := decoder.Decode(&trailing); err != io.EOF { writeError(w, http.StatusBadRequest, "invalid JSON request"); return false }
    if target.Transaction == "" || len(target.Transaction) > 4096 || !strings.HasPrefix(target.Transaction, "/") || strings.ContainsAny(target.Transaction, "\r\n") { writeError(w, http.StatusBadRequest, "invalid migration recovery transaction"); return false }
    return true
}

func decodeAdminMigrationRecoveryJSON(data []byte, target any) error {
    decoder := json.NewDecoder(bytes.NewReader(data)); decoder.DisallowUnknownFields()
    if err := decoder.Decode(target); err != nil { return err }
    var trailing any
    if err := decoder.Decode(&trailing); err != io.EOF { if err == nil { return errors.New("unexpected trailing JSON value") }; return err }
    return nil
}

func boundedMigrationRecoveryError(message string) string {
    if message == "" || len(message) > 512 || strings.ContainsAny(message, "\r\n") { return "migration recovery plan could not be validated" }
    return message
}
