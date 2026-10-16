package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

type migrationInstanceContextKey struct{}

func migrationInstanceID(ctx context.Context) string {
	id, _ := ctx.Value(migrationInstanceContextKey{}).(string)
	return id
}

// Resolve the authoritative destination before any helper, journal or runtime access.
func (s *server) migrationInstanceRequest(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return false
	}
	id, ok := instanceRequestID(w, r)
	if !ok {
		return false
	}
	if _, err := instanceRuntimes.readRecord(id); err != nil {
		instanceRuntimeError(w, err, id)
		return false
	}
	*r = *r.WithContext(context.WithValue(r.Context(), migrationInstanceContextKey{}, id))
	return true
}
func registerMigrationInstanceOperationRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/minecraft/instances/{id}/migration/current-operation", s.migrationInstanceCurrent)
	mux.HandleFunc("GET /v1/minecraft/instances/{id}/migration/operations/{operation}", s.migrationInstanceOperation)
	mux.HandleFunc("GET /v1/minecraft/instances/{id}/migration/operations/{operation}/trace", s.migrationInstanceOperation)
}
func (s *server) migrationInstanceCurrent(w http.ResponseWriter, r *http.Request) {
	if !s.migrationInstanceRequest(w, r) {
		return
	}
	if s.operations == nil {
		writeError(w, 503, "operation status is unavailable")
		return
	}
	op, err := s.operations.currentMigration()
	if err != nil {
		writeError(w, 500, "operation status could not be read")
		return
	}
	if op != nil && op.InstanceID != r.PathValue("id") {
		op = nil
	}
	writeJSON(w, 200, adminOperationResponse{Operation: op})
}
func (s *server) migrationInstanceOperation(w http.ResponseWriter, r *http.Request) {
	if !s.migrationInstanceRequest(w, r) {
		return
	}
	if s.operations == nil {
		writeError(w, 503, "operation status is unavailable")
		return
	}
	id := r.PathValue("operation")
	if !validOperationID(id) {
		writeError(w, 400, "invalid operation id")
		return
	}
	op, err := s.operations.get(id)
	if errors.Is(err, errOperationNotFound) || err == nil && (op.InstanceID != r.PathValue("id") || !isServerMigrationOperation(op.OperationType)) {
		writeError(w, 404, "migration operation not found")
		return
	}
	if err != nil {
		writeError(w, 500, "operation status could not be read")
		return
	}
	if strings.HasSuffix(r.URL.Path, "/trace") {
		r.SetPathValue("id", id)
		s.adminOperationTrace(w, r)
		return
	}
	writeJSON(w, 200, adminOperationResponse{Operation: &op})
}
func isServerMigrationOperation(kind string) bool {
	return kind == operationTypeMigrationImport || kind == operationTypeMigrationExport || kind == operationTypeMigrationRecovery
}
