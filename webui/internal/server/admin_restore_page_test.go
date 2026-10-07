package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeRestoreAPI struct {
	fakeAPI
	role      string
	operation *api.PersistentOperation
}

func (f *fakeRestoreAPI) Session(_ context.Context, session string) (api.SessionInfo, error) {
	if session != "session-token" {
		return api.SessionInfo{}, api.ErrUnauthorized
	}
	return api.SessionInfo{Username: "voxel", Role: f.role, AuthSource: "system"}, nil
}

func (f *fakeRestoreAPI) AdminOperation(_ context.Context, session, id string) (api.PersistentOperationResponse, error) {
	if session != "session-token" {
		return api.PersistentOperationResponse{}, api.ErrUnauthorized
	}
	if f.operation != nil && f.operation.OperationID != id {
		return api.PersistentOperationResponse{}, &api.ResponseError{StatusCode: http.StatusNotFound, Message: "operation not found"}
	}
	return api.PersistentOperationResponse{Operation: f.operation}, nil
}

func TestRestoreProgressAPIStillServesBackupsWorkspace(t *testing.T) {
	const operationID = "87654321-4321-4abc-8def-123456789abc"
	for _, tc := range []struct {
		name          string
		role          string
		operationType string
		missing       bool
		requestID     string
		wantStatus    int
	}{
		{name: "administrator", role: "administrator", operationType: "restore", requestID: operationID, wantStatus: http.StatusOK},
		{name: "operator", role: "operator", operationType: "restore", requestID: operationID, wantStatus: http.StatusForbidden},
		{name: "wrong operation type", role: "administrator", operationType: "migration", requestID: operationID, wantStatus: http.StatusNotFound},
		{name: "missing operation", role: "administrator", missing: true, requestID: operationID, wantStatus: http.StatusNotFound},
		{name: "unknown operation ID", role: "administrator", operationType: "restore", requestID: "unknown-operation", wantStatus: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeRestoreAPI{role: tc.role}
			if !tc.missing {
				client.operation = &api.PersistentOperation{
					SchemaVersion: "v1", OperationID: operationID, OperationType: tc.operationType,
					State: "running", Stage: "staging",
				}
			}
			app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
			if err != nil {
				t.Fatal(err)
			}
			response := httptestResponse(app, authenticatedAdminRequest(http.MethodGet, "http://example/api/restore/progress/"+tc.requestID, ""))
			if response.Code != tc.wantStatus {
				t.Fatalf("Restore progress API returned %d, want %d: %s", response.Code, tc.wantStatus, response.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				return
			}
			if !strings.Contains(response.Header().Get("Content-Type"), "application/json") {
				t.Fatalf("Content-Type = %q, want application/json", response.Header().Get("Content-Type"))
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
			}
			var result api.PersistentOperationResponse
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Operation == nil || result.Operation.OperationID != operationID || result.Operation.State != "running" || result.Operation.Stage != "staging" {
				t.Fatalf("unexpected Restore progress response: %#v", result.Operation)
			}
		})
	}
}

func TestStandaloneRestoreUIRetired(t *testing.T) {
	app, err := New(&fakeAPI{}, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/settings/restore", http.StatusNotFound},
		{http.MethodGet, "/settings/restore/review", http.StatusNotFound},
		{http.MethodPost, "/settings/restore/apply", http.StatusMethodNotAllowed},
		{http.MethodGet, "/settings/restore/progress/operation-id", http.StatusNotFound},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			response := httptestResponse(app, authenticatedAdminRequest(tc.method, "http://example"+tc.path, ""))
			if response.Code != tc.status {
				t.Fatalf("retired Restore route returned %d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
		})
	}
	for _, path := range []string{
		"templates/restore.html",
		"templates/restore_review.html",
		"templates/restore_progress.html",
	} {
		if _, err := assets.ReadFile(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("retired template %s: expected absence, got %v", path, err)
		}
	}
	content, err := assets.ReadFile("templates/backups_workspace.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{
		"/workspace/backups/restore/plan",
		"/workspace/backups/restore/apply",
		"/api/restore/progress/",
	} {
		if !strings.Contains(string(content), route) {
			t.Errorf("Backups Workspace replacement is missing %q", route)
		}
	}
}
