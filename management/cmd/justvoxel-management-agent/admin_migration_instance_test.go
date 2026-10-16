package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func migrationSurfaceTestServer(t *testing.T, role principalRole) *server {
	t.Helper()
	f := newInstanceActivationFixture(t)
	old := instanceRuntimes
	instanceRuntimes = f.backend
	t.Cleanup(func() { instanceRuntimes = old })
	return surfaceTestServer(t, role)
}
func migrationSurfaceRequest(method, path, body string) *http.Request {
	r := surfaceRequest(method, path, body)
	r.SetPathValue("id", "jv-a1b2c3")
	return r
}
func TestMigrationSelectedOperationIsolation(t *testing.T) {
	s := migrationSurfaceTestServer(t, roleAdministrator)
	s.operations = openTestOperationStore(t)
	op, _, err := s.operations.beginMigrationImport("jv-a1b2c3", testMigrationImportFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if op.InstanceID != "jv-a1b2c3" {
		t.Fatal("missing durable destination identity")
	}
	mux := http.NewServeMux()
	registerMigrationInstanceOperationRoutes(mux, s)
	for _, id := range []string{"jv-a1b2c3", "jv-d4e5f6"} {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, surfaceRequest("GET", "/v1/minecraft/instances/"+id+"/migration/current-operation", ""))
		if rr.Code != 200 {
			t.Fatal(rr.Code, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), op.OperationID) != (id == op.InstanceID) {
			t.Fatal("operation crossed instance boundary", rr.Body.String())
		}
		rr = httptest.NewRecorder()
		mux.ServeHTTP(rr, surfaceRequest("GET", "/v1/minecraft/instances/"+id+"/migration/operations/"+op.OperationID, ""))
		if id != op.InstanceID && rr.Code != 404 {
			t.Fatal("sibling reconnected", rr.Code)
		}
	}
	if _, e := s.operations.transition(op.OperationID, operationNeedsAttention, "test_attention", "Retained migration requires recovery."); e != nil {
		t.Fatal(e)
	}
	request := surfaceRequest("POST", "/migration/import/resolve", `{"operation_id":"`+op.OperationID+`","keep_current_state":true}`)
	request.SetPathValue("id", "jv-d4e5f6")
	refused := httptest.NewRecorder()
	s.adminMigrationImportResolve(refused, request)
	if refused.Code != 404 {
		t.Fatal("sibling resolved selected migration", refused.Code)
	}
	if _, _, err = s.operations.beginMigrationRecovery("jv-d4e5f6", testMigrationRecoveryFingerprint); err == nil {
		t.Fatal("sibling took recovery ownership")
	}
}
func TestMigrationRejectsMissingMalformedAndStaleSelectionBeforeHelper(t *testing.T) {
	s := migrationSurfaceTestServer(t, roleAdministrator)
	old := runAdminMigrationImportPlanHelper
	t.Cleanup(func() { runAdminMigrationImportPlanHelper = old })
	runAdminMigrationImportPlanHelper = func(context.Context, string, []byte) ([]byte, error) {
		t.Fatal("helper ran for invalid destination")
		return nil, nil
	}
	for _, id := range []string{"", "family", "jv-../bad", "jv-stale1"} {
		req := surfaceRequest("GET", "/migration/import", "")
		req.SetPathValue("id", id)
		rr := httptest.NewRecorder()
		s.adminMigrationImportDiscover(rr, req)
		if rr.Code < 400 {
			t.Fatal("invalid destination accepted", id)
		}
	}
}
func TestMigrationImportCannotPlanFreshDestination(t *testing.T) {
	helper := step5A2ImportHelper(false)
	helper.Normalized.Destination.Mode = "fresh"
	if validateSuccessfulAdminMigrationImportPlan(step5A2ImportRequest(), &helper) == nil {
		t.Fatal("fresh destination accepted")
	}
}
func TestMigrationPlanHelperReceivesSelectedContext(t *testing.T) {
	s := migrationSurfaceTestServer(t, roleAdministrator)
	old := runAdminMigrationImportPlanHelper
	t.Cleanup(func() { runAdminMigrationImportPlanHelper = old })
	runAdminMigrationImportPlanHelper = func(ctx context.Context, action string, _ []byte) ([]byte, error) {
		if migrationInstanceID(ctx) != "jv-a1b2c3" {
			t.Fatal("missing selected context")
		}
		return json.Marshal(adminMigrationImportDiscoveryResponse{OK: true, SchemaVersion: "v1", Configured: true, SourceKinds: []string{"local"}})
	}
	rr := httptest.NewRecorder()
	s.adminMigrationImportDiscover(rr, migrationSurfaceRequest("GET", "/migration/import", ""))
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
}

func TestMigrationRestartDoesNotInventMissingInstanceIdentity(t *testing.T) {
	first := openTestOperationStore(t)
	journal, _, err := first.beginMigrationImport("jv-a1b2c3", testMigrationImportFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	journal.InstanceID = "" // Evidence written by a pre-instance migration implementation.
	if err = first.persist(journal); err != nil {
		t.Fatal(err)
	}
	if err = first.close(); err != nil {
		t.Fatal(err)
	}
	second, err := openOperationStore(first.baseDir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.close()
	current, err := second.currentMigration()
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.InstanceID != "" || current.State != operationNeedsAttention {
		t.Fatal("legacy identity was guessed or silently discarded", current)
	}
	if _, _, err = second.beginMigrationRecovery("jv-a1b2c3", testMigrationRecoveryFingerprint); err == nil {
		t.Fatal("unbound operation recovered against a guessed instance")
	}
}
