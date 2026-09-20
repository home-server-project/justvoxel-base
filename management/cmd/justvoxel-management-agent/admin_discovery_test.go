package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalRootCanReadConfigurationWithoutBearerSession(t *testing.T) {
	s := surfaceTestServer(t, roleViewer)
	old := runAdminDiscoveryHelper
	defer func() { runAdminDiscoveryHelper = old }()

	runAdminDiscoveryHelper = func(_ context.Context, action string) ([]byte, error) {
		if action != "configuration" {
			t.Fatalf("action = %q, want configuration", action)
		}
		return []byte(`{"configured":true,"minecraft":{"java_memory":"4G","container_memory":"6G","java_port":25565,"bedrock_enabled":false,"bedrock_port":19132,"timezone":"UTC","max_players":10,"motd":"JustVoxel","image_tag":"stable","version_mode":"pinned","version":"1.21.8"},"backup":{"keep":7,"schedule":"*-*-* 04:30:00","timer_enabled":true}}`), nil
	}

	req := requestWithPeerUID(http.MethodGet, "http://unix/v1/admin/configuration", 0)
	rr := httptest.NewRecorder()
	s.adminConfiguration(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("local root configuration read returned %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"configured":true`) || !strings.Contains(rr.Body.String(), `"max_players":10`) {
		t.Fatalf("unexpected local root configuration response: %s", rr.Body.String())
	}
}

func TestAdminDiscoveryRequiresAdministrator(t *testing.T) {
	old := runAdminDiscoveryHelper
	defer func() { runAdminDiscoveryHelper = old }()
	called := false
	runAdminDiscoveryHelper = func(_ context.Context, _ string) ([]byte, error) {
		called = true
		return []byte(`{"configured":false,"minecraft":{},"backup":{}}`), nil
	}

	for _, role := range []principalRole{roleOperator, roleViewer} {
		s := surfaceTestServer(t, role)
		rr := httptest.NewRecorder()
		s.adminConfiguration(rr, surfaceRequest(http.MethodGet, "/v1/admin/configuration", ""))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("role %s status = %d, want 403", role, rr.Code)
		}
	}
	if called {
		t.Fatal("discovery helper ran for a non-Administrator")
	}
}

func TestAdminConfigurationSupportsConfiguredAndUnconfiguredStates(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminDiscoveryHelper
	defer func() { runAdminDiscoveryHelper = old }()

	outputs := []string{
		`{"configured":false,"minecraft":{},"backup":{}}`,
		`{"configured":true,"minecraft":{"data_path":"/var/lib/justvoxel/minecraft","java_memory":"4G","container_memory":"6G","java_port":25565,"bedrock_enabled":true,"bedrock_port":19132,"timezone":"America/Toronto","max_players":10,"motd":"JustVoxel","image_tag":"stable","version_mode":"pinned","version":"1.21.8","game_mode":"survival","difficulty":"normal","whitelist_enabled":true,"enforce_whitelist":true},"backup":{"type":"system","path":"/var/lib/justvoxel/backups","keep":7,"schedule":"*-*-* 04:30:00","timer_enabled":true}}`,
	}
	index := 0
	runAdminDiscoveryHelper = func(_ context.Context, action string) ([]byte, error) {
		if action != "configuration" {
			t.Fatalf("action = %q, want configuration", action)
		}
		out := []byte(outputs[index])
		index++
		return out, nil
	}

	for _, wantConfigured := range []bool{false, true} {
		rr := httptest.NewRecorder()
		s.adminConfiguration(rr, surfaceRequest(http.MethodGet, "/v1/admin/configuration", ""))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
		}
		want := `"configured":false`
		if wantConfigured {
			want = `"configured":true`
		}
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("response missing %s: %s", want, rr.Body.String())
		}
	}
}

func TestAdminStorageDropsUnknownSensitiveFields(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminDiscoveryHelper
	defer func() { runAdminDiscoveryHelper = old }()
	runAdminDiscoveryHelper = func(_ context.Context, action string) ([]byte, error) {
		if action != "storage" {
			t.Fatalf("action = %q, want storage", action)
		}
		return []byte(`{"system_disks":["/dev/vda"],"credentials":"do-not-leak","devices":[{"name":"vdb1","path":"/dev/vdb1","parent":"vdb","type":"part","size_bytes":1073741824,"filesystem":"xfs","label":"BACKUP","uuid":"abc","mountpoints":["/var/mnt/backup"],"model":"Virtual Disk","transport":"virtio","read_only":false,"system":false,"password":"secret"}]}`), nil
	}

	rr := httptest.NewRecorder()
	s.adminStorage(rr, surfaceRequest(http.MethodGet, "/v1/admin/storage", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, forbidden := range []string{"do-not-leak", "secret", "credentials", "password"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("sanitized storage response leaked %q: %s", forbidden, body)
		}
	}
	for _, want := range []string{"/dev/vda", "/dev/vdb1", "/var/mnt/backup", "Virtual Disk"} {
		if !strings.Contains(body, want) {
			t.Fatalf("storage response missing %q: %s", want, body)
		}
	}
}

func TestAdminDiscoveryErrorsAreSanitized(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	old := runAdminDiscoveryHelper
	defer func() { runAdminDiscoveryHelper = old }()

	runAdminDiscoveryHelper = func(_ context.Context, _ string) ([]byte, error) {
		return []byte("password=top-secret"), errors.New("exit status 1")
	}
	rr := httptest.NewRecorder()
	s.adminSetupDefaults(rr, surfaceRequest(http.MethodGet, "/v1/admin/setup-defaults", ""))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("helper failure status = %d: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "top-secret") {
		t.Fatalf("helper error leaked raw output: %s", rr.Body.String())
	}

	runAdminDiscoveryHelper = func(_ context.Context, _ string) ([]byte, error) {
		return []byte("not-json"), nil
	}
	rr = httptest.NewRecorder()
	s.adminConfiguration(rr, surfaceRequest(http.MethodGet, "/v1/admin/configuration", ""))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("invalid JSON status = %d: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "not-json") {
		t.Fatalf("invalid helper payload leaked: %s", rr.Body.String())
	}
}
