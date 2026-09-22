package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeStorageActionAPI struct {
	fakeDiscoveryAPI
	plan       api.AdminStorageActionResponse
	apply      api.AdminStorageActionResponse
	planned    api.AdminStorageActionRequest
	applied    api.AdminStorageActionRequest
	planCalls  int
	applyCalls int
}

func (f *fakeStorageActionAPI) AdminStorageActionPlan(_ context.Context, session string, request api.AdminStorageActionRequest) (api.AdminStorageActionResponse, error) {
	if session != "session-token" {
		return api.AdminStorageActionResponse{}, api.ErrUnauthorized
	}
	f.planCalls++
	f.planned = request
	return f.plan, nil
}

func (f *fakeStorageActionAPI) AdminStorageActionApply(_ context.Context, session string, request api.AdminStorageActionRequest) (api.AdminStorageActionResponse, error) {
	if session != "session-token" {
		return api.AdminStorageActionResponse{}, api.ErrUnauthorized
	}
	f.applyCalls++
	f.applied = request
	return f.apply, nil
}

func TestStorageBrowserActionPlanUsesSelectedPartition(t *testing.T) {
	client := &fakeStorageActionAPI{}
	client.plan = api.AdminStorageActionResponse{
		OK: true,
		Proposed: api.AdminStorageActionPlan{
			Operation: "format", Device: "/dev/vdb1", Role: "Minecraft",
			Confirmation: "FORMAT /dev/vdb1", Fingerprint: "abc123", Destructive: true,
		},
		Warnings: []string{"Minecraft data is stored on this partition."},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	body := "csrf=csrf-token&operation=format&device=%2Fdev%2Fvdb1"
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/actions/plan", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("storage action plan returned %d: %s", rr.Code, rr.Body.String())
	}
	if client.planCalls != 1 || client.applyCalls != 0 {
		t.Fatalf("plan calls=%d apply calls=%d", client.planCalls, client.applyCalls)
	}
	if client.planned.Operation != "format" || client.planned.Device != "/dev/vdb1" {
		t.Fatalf("unexpected planned request: %#v", client.planned)
	}
	for _, want := range []string{"FORMAT /dev/vdb1", "Minecraft", "abc123"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("storage action plan response missing %q: %s", want, rr.Body.String())
		}
	}
}

func TestStorageBrowserActionApplyCarriesReviewedEvidence(t *testing.T) {
	client := &fakeStorageActionAPI{}
	client.apply = api.AdminStorageActionResponse{
		OK: true, Applied: true,
		Proposed: api.AdminStorageActionPlan{Operation: "format", Device: "/dev/vdb1", Filesystem: "xfs"},
	}
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	body := "csrf=csrf-token&operation=format&device=%2Fdev%2Fvdb1&confirmation=FORMAT+%2Fdev%2Fvdb1&fingerprint=abc123"
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/actions/apply", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("storage action apply returned %d: %s", rr.Code, rr.Body.String())
	}
	if client.applyCalls != 1 || client.planCalls != 0 {
		t.Fatalf("apply calls=%d plan calls=%d", client.applyCalls, client.planCalls)
	}
	if client.applied.Confirmation != "FORMAT /dev/vdb1" || client.applied.Fingerprint != "abc123" {
		t.Fatalf("review evidence lost at apply: %#v", client.applied)
	}
}

func TestStorageBrowserActionsRejectOperatorAndBadCSRF(t *testing.T) {
	client := &fakeStorageActionAPI{}
	client.role = "operator"
	app, err := New(client, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	body := "csrf=csrf-token&operation=unmount&device=%2Fdev%2Fvdb1"
	rr := httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/actions/plan", body))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("operator storage action status=%d, want 403", rr.Code)
	}
	if client.planCalls != 0 || client.applyCalls != 0 {
		t.Fatal("privileged storage action API ran for Operator")
	}

	client.role = "administrator"
	body = "csrf=wrong&operation=unmount&device=%2Fdev%2Fvdb1"
	rr = httptestResponse(app, authenticatedAdminRequest(http.MethodPost, "http://example/api/new-storage/actions/plan", body))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("bad CSRF storage action status=%d, want 403", rr.Code)
	}
	if client.planCalls != 0 || client.applyCalls != 0 {
		t.Fatal("storage action API ran after CSRF rejection")
	}
}
