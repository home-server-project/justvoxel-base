package main

import (
	"errors"
	"net/http"
	"sync"
)

type adminOperationResponse struct {
	Operation *operationJournal `json:"operation"`
}

var (
	managementOperationStoresMu sync.Mutex
	managementOperationStores   = make(map[*server]*operationStore)
	openManagementOperationStore = func() (*operationStore, error) {
		return openOperationStore(operationStateDir)
	}
)

func registerAdminOperationRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/admin/operations/{id}", s.adminOperationStatus)
	mux.HandleFunc("GET /v1/admin/setup/current-operation", s.adminCurrentSetupOperation)
}

func managementOperationStore(s *server) (*operationStore, error) {
	managementOperationStoresMu.Lock()
	defer managementOperationStoresMu.Unlock()
	if store := managementOperationStores[s]; store != nil {
		return store, nil
	}
	store, err := openManagementOperationStore()
	if err != nil {
		return nil, err
	}
	managementOperationStores[s] = store
	return store, nil
}

func (s *server) adminOperationStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	store, err := managementOperationStore(s)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "operation status is unavailable")
		return
	}
	id := r.PathValue("id")
	if !validOperationID(id) {
		writeError(w, http.StatusBadRequest, "invalid operation id")
		return
	}
	operation, err := store.get(id)
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
	store, err := managementOperationStore(s)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "operation status is unavailable")
		return
	}
	operation, err := store.currentSetup()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "current setup operation could not be read")
		return
	}
	writeJSON(w, http.StatusOK, adminOperationResponse{Operation: operation})
}
