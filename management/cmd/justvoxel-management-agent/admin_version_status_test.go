package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVersionStatusIsAdministratorOnlyAndValidatesInputs(t *testing.T) {
	whitelistSurfaceFixture(t)
	old := runAdminVersionStatus
	defer func() { runAdminVersionStatus = old }()
	called := false
	runAdminVersionStatus = func(_ context.Context, policy, version, serverType, imageTag string) ([]byte, error) {
		called = true
		return []byte(`{"server_software":"Paper","installed":"26.2","available":"26.3","recommended":"26.2","selected_candidate":"26.2","policy":"recommended","update_available":true,"paper_supported":true}`), nil
	}
	viewer := surfaceTestServer(t, roleViewer)
	rr := httptest.NewRecorder()
	viewer.adminVersionStatus(rr, surfaceRequest(http.MethodGet, "/v1/minecraft/instances/jv-a1b2c3/version/status", ""))
	if rr.Code != http.StatusForbidden || called {
		t.Fatalf("viewer: %d, helper=%v", rr.Code, called)
	}
	admin := surfaceTestServer(t, roleAdministrator)
	rr = httptest.NewRecorder()
	admin.adminVersionStatus(rr, surfaceRequest(http.MethodGet, "/v1/minecraft/instances/jv-a1b2c3/version/status?policy=pinned&version=26.3%3Bbad", ""))
	if rr.Code != http.StatusBadRequest || called {
		t.Fatalf("unsafe version: %d, helper=%v", rr.Code, called)
	}
	rr = httptest.NewRecorder()
	admin.adminVersionStatus(rr, surfaceRequest(http.MethodGet, "/v1/minecraft/instances/jv-a1b2c3/version/status?policy=recommended", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d %s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{`"available":"26.3"`, `"recommended":"26.2"`, `"selected_candidate":"26.2"`, `"policy":"recommended"`, `"update_available":true`} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("status missing %s: %s", want, rr.Body.String())
		}
	}
}

func TestSetupVersionPreviewUsesAdministratorAndExplicitBedrockChoice(t *testing.T) {
	whitelistSurfaceFixture(t)
	old := runSetupVersionPreview
	defer func() { runSetupVersionPreview = old }()
	called := false
	runSetupVersionPreview = func(_ context.Context, policy, version, bedrock, serverType string) ([]byte, error) {
		called = true
		if policy != "pinned" || version != "1.21.8" || bedrock != "yes" {
			t.Fatalf("unexpected preview request: %q %q %q", policy, version, bedrock)
		}
		return []byte(`{"selected_candidate":"1.21.8","candidate_channel":"ALPHA","paper_supported":true}`), nil
	}
	viewer := surfaceTestServer(t, roleViewer)
	rr := httptest.NewRecorder()
	viewer.adminSetupVersionPreview(rr, surfaceRequest(http.MethodGet, "/v1/admin/setup/version-preview?policy=pinned&version=1.21.8&bedrock=yes", ""))
	if rr.Code != http.StatusForbidden || called {
		t.Fatalf("viewer: %d, helper=%v", rr.Code, called)
	}
	admin := surfaceTestServer(t, roleAdministrator)
	rr = httptest.NewRecorder()
	admin.adminSetupVersionPreview(rr, surfaceRequest(http.MethodGet, "/v1/admin/setup/version-preview?policy=pinned&version=1.21.8&bedrock=yes", ""))
	if rr.Code != http.StatusOK || !called || !strings.Contains(rr.Body.String(), `"candidate_channel":"ALPHA"`) {
		t.Fatalf("preview: %d %s", rr.Code, rr.Body.String())
	}
}

func TestVersionCommandOutputSeparatesDiagnostics(t *testing.T) {
	for _, exitCode := range []string{"0", "1"} {
		t.Run("exit-"+exitCode, func(t *testing.T) {
			output, err := versionCommandOutput(context.Background(), "/bin/sh", "-c",
				`printf '%s\n' 'publisher diagnostic' >&2; printf '%s\n' '{"selected_candidate":"26.3","crossplay_compatible":false,"reason":"Verified artifacts unavailable"}'; exit "$1"`, "version-helper", exitCode)
			if exitCode != "0" {
				if err == nil {
					t.Fatal("non-zero subprocess exit must remain an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var status adminVersionStatus
			if err := json.Unmarshal(output, &status); err != nil {
				t.Fatalf("stdout JSON was polluted: %v", err)
			}
			if status.SelectedCandidate != "26.3" || status.Reason != "Verified artifacts unavailable" || strings.Contains(string(output), "diagnostic") {
				t.Fatalf("unexpected stdout: %q", output)
			}
		})
	}
}

func TestVersionHandlersRejectFailedHelperWithoutLeakingOutput(t *testing.T) {
	whitelistSurfaceFixture(t)
	oldStatus, oldPreview := runAdminVersionStatus, runSetupVersionPreview
	defer func() { runAdminVersionStatus, runSetupVersionPreview = oldStatus, oldPreview }()
	failedCommand := func() ([]byte, error) {
		return versionCommandOutput(context.Background(), "/bin/sh", "-c",
			`printf '%s' 'private diagnostic' >&2; printf '%s' '{"selected_candidate":"26.3"}'; exit 1`)
	}
	runAdminVersionStatus = func(context.Context, string, string, string, string) ([]byte, error) { return failedCommand() }
	runSetupVersionPreview = func(context.Context, string, string, string, string) ([]byte, error) { return failedCommand() }
	admin := surfaceTestServer(t, roleAdministrator)
	for _, setup := range []bool{false, true} {
		rr := httptest.NewRecorder()
		if setup {
			admin.adminSetupVersionPreview(rr, surfaceRequest(http.MethodGet, "/v1/admin/setup/version-preview?policy=recommended&bedrock=yes", ""))
		} else {
			admin.adminVersionStatus(rr, surfaceRequest(http.MethodGet, "/v1/minecraft/instances/jv-a1b2c3/version/status", ""))
		}
		if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), "Version information is unavailable") || strings.Contains(rr.Body.String(), "26.3") || strings.Contains(rr.Body.String(), "diagnostic") {
			t.Fatalf("failed helper response: %d %s", rr.Code, rr.Body.String())
		}
	}
}
