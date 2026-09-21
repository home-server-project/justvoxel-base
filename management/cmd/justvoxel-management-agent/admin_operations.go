package main

import (
	"errors"
	"net/http"
)

type adminOperationResponse struct {
	Operation *operationJournal `json:"operation"`
}

func registerAdminOperationRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/admin/operations/{id}", s.adminOperationStatus)
	mux.HandleFunc("GET /v1/admin/setup/current-operation", s.adminCurrentSetupOperation)
	mux.HandleFunc("GET /v1/admin/restore/current-operation", s.adminCurrentRestoreOperation)
	mux.HandleFunc("GET /v1/admin/data-migration/current-operation", s.adminCurrentDataMigrationOperation)
}

func (s *server) adminOperationStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	if s.operations == nil {
		writeError(w, http.StatusServiceUnavailable, "operation status is unavailable")
		return
	}
	id := r.PathValue("id")
	if !validOperationID(id) {
		writeError(w, http.StatusBadRequest, "invalid operation id")
		return
	}
	operation, err := s.operations.get(id)
	if err != nil {
		if errors.Is(err, errOperationNotFound) {
			writeError(w, http.StatusNotFound, "operation not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "operation status could not be read")
		return
	}
	writeJSON(w, http.StatusOK, adminOperationResponse{Operation: &operation})
}

func (s *server) adminCurrentSetupOperation(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	if s.operations == nil {
		writeError(w, http.StatusServiceUnavailable, "operation status is unavailable")
		return
	}
	operation, err := s.operations.currentSetup()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "current setup operation could not be read")
		return
	}
	writeJSON(w, http.StatusOK, adminOperationResponse{Operation: operation})
}

func (s *server) adminCurrentRestoreOperation(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	if s.operations == nil {
		writeError(w, http.StatusServiceUnavailable, "operation status is unavailable")
		return
	}
	operation, err := s.operations.currentRestore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "current restore operation could not be read")
		return
	}
	writeJSON(w, http.StatusOK, adminOperationResponse{Operation: operation})
}

func (s *server) adminCurrentDataMigrationOperation(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	if s.operations == nil {
		writeError(w, http.StatusServiceUnavailable, "operation status is unavailable")
		return
	}
	operation, err := s.operations.currentDataMigration()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "current data migration operation could not be read")
		return
	}
	writeJSON(w, http.StatusOK, adminOperationResponse{Operation: operation})
}
