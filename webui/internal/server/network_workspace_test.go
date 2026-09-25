package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeNetworkAPI struct {
	*fakeAPI
	role                 string
	scanned              string
	checkpointCreated    api.NetworkCheckpoint
	checkpointID         string
	checkpointConfirmed  string
	checkpointRolledBack string
}

func (f *fakeNetworkAPI) Session(_ context.Context, session string) (api.SessionInfo, error) {
	if session != "session-token" {
		return api.SessionInfo{}, api.ErrUnauthorized
	}
	role := f.role
	if role == "" {
		role = "administrator"
	}
	return api.SessionInfo{Username: "admin", Role: role, AuthSource: "system"}, nil
}

func (f *fakeNetworkAPI) NetworkStatus(_ context.Context, session string) (api.NetworkStatus, error) {
	if session != "session-token" {
		return api.NetworkStatus{}, api.ErrUnauthorized
	}
	return api.NetworkStatus{
		Version:           "1.58.0",
		State:             "connected-global",
		Connectivity:      "full",
		NetworkingEnabled: true,
		WirelessEnabled:   true,
		Devices: []api.NetworkDevice{{
			Interface: "enp1s0",
			Kind:      "ethernet",
			State:     "activated",
			Managed:   true,
			IPv4: api.NetworkIPConfig{
				Addresses: []api.NetworkIPAddress{{Address: "192.168.0.59", Prefix: 24}},
			},
		}},
	}, nil
}

func (f *fakeNetworkAPI) WiFiNetworks(_ context.Context, session, interfaceName string) (api.WiFiNetworksResponse, error) {
	if session != "session-token" {
		return api.WiFiNetworksResponse{}, api.ErrUnauthorized
	}
	return api.WiFiNetworksResponse{
		Interface: interfaceName,
		Networks:  []api.WiFiNetwork{{SSID: "Home WiFi", Strength: 84, Security: "wpa-personal"}},
	}, nil
}

func (f *fakeNetworkAPI) RequestWiFiScan(_ context.Context, session, interfaceName string) error {
	if session != "session-token" {
		return api.ErrUnauthorized
	}
	f.scanned = interfaceName
	return nil
}

func (f *fakeNetworkAPI) CreateNetworkCheckpoint(_ context.Context, session string, interfaces []string, timeout uint32) (api.NetworkCheckpoint, error) {
	if session != "session-token" {
		return api.NetworkCheckpoint{}, api.ErrUnauthorized
	}
	f.checkpointCreated = api.NetworkCheckpoint{
		ID:                     "opaque-checkpoint",
		Status:                 "pending",
		Interfaces:             append([]string(nil), interfaces...),
		RollbackTimeoutSeconds: timeout,
		CreatedAt:              "2026-09-25T22:00:00Z",
		ExpiresAt:              "2026-09-25T22:01:30Z",
	}
	return f.checkpointCreated, nil
}

func (f *fakeNetworkAPI) NetworkCheckpoint(_ context.Context, session, id string) (api.NetworkCheckpoint, error) {
	if session != "session-token" {
		return api.NetworkCheckpoint{}, api.ErrUnauthorized
	}
	f.checkpointID = id
	if f.checkpointCreated.ID != "" {
		return f.checkpointCreated, nil
	}
	return api.NetworkCheckpoint{ID: id, Status: "pending", Interfaces: []string{"enp1s0"}, RollbackTimeoutSeconds: 90}, nil
}

func (f *fakeNetworkAPI) ConfirmNetworkCheckpoint(_ context.Context, session, id string) error {
	if session != "session-token" {
		return api.ErrUnauthorized
	}
	f.checkpointConfirmed = id
	return nil
}

func (f *fakeNetworkAPI) RollbackNetworkCheckpoint(_ context.Context, session, id string) (api.NetworkCheckpointRollback, error) {
	if session != "session-token" {
		return api.NetworkCheckpointRollback{}, api.ErrUnauthorized
	}
	f.checkpointRolledBack = id
	return api.NetworkCheckpointRollback{
		OK:      true,
		ID:      id,
		Results: []api.NetworkCheckpointRollbackDevice{{Interface: "enp1s0", Result: "ok"}},
	}, nil
}

func TestNetworkWorkspaceStatusProxy(t *testing.T) {
	app, err := New(&fakeNetworkAPI{fakeAPI: &fakeAPI{}}, Config{Version: "1.0.0", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example/api/network", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "\"connectivity\":\"full\"") || !strings.Contains(rr.Body.String(), "\"interface\":\"enp1s0\"") {
		t.Fatalf("unexpected network response: %s", rr.Body.String())
	}
}

func TestNetworkWorkspaceScanRequiresCSRFAndUsesInterface(t *testing.T) {
	fake := &fakeNetworkAPI{fakeAPI: &fakeAPI{}}
	app, err := New(fake, Config{Version: "1.0.0", ManagementAPI: "v1", ExternalScheme: "http"})
	if err != nil {
		t.Fatal(err)
	}

	form := strings.NewReader("csrf=token")
	req := httptest.NewRequest(http.MethodPost, "http://example/api/network/wifi/wlp2s0/scan", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if fake.scanned != "wlp2s0" {
		t.Fatalf("scan interface = %q, want wlp2s0", fake.scanned)
	}
}

func TestNetworkWorkspaceShellIsPresentOnDashboard(t *testing.T) {
	app, err := New(&fakeNetworkAPI{fakeAPI: &fakeAPI{}}, Config{Version: "1.0.0", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"data-network-open", "data-network-workspace-dialog", "/static/network-workspace.js"} {
		if !strings.Contains(body, want) {
			t.Fatalf("dashboard is missing %q", want)
		}
	}
}

func TestNetworkCheckpointProxyLifecycle(t *testing.T) {
	fake := &fakeNetworkAPI{fakeAPI: &fakeAPI{}}
	app, err := New(fake, Config{Version: "1.0.0", ManagementAPI: "v1", ExternalScheme: "http"})
	if err != nil {
		t.Fatal(err)
	}

	createForm := strings.NewReader("csrf=token&interface=enp1s0&rollback_timeout_seconds=90")
	createRequest := httptest.NewRequest(http.MethodPost, "http://example/api/network/checkpoints", createForm)
	createRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createRequest.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	createRequest.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
	createResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body %s", createResponse.Code, createResponse.Body.String())
	}
	if fake.checkpointCreated.ID != "opaque-checkpoint" ||
		len(fake.checkpointCreated.Interfaces) != 1 ||
		fake.checkpointCreated.Interfaces[0] != "enp1s0" ||
		fake.checkpointCreated.RollbackTimeoutSeconds != 90 {
		t.Fatalf("unexpected checkpoint create proxy: %+v", fake.checkpointCreated)
	}

	statusRequest := httptest.NewRequest(http.MethodGet, "http://example/api/network/checkpoints/opaque-checkpoint", nil)
	statusRequest.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	statusResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusOK || fake.checkpointID != "opaque-checkpoint" {
		t.Fatalf("status proxy = %d id=%q body=%s", statusResponse.Code, fake.checkpointID, statusResponse.Body.String())
	}

	confirmForm := strings.NewReader("csrf=token")
	confirmRequest := httptest.NewRequest(http.MethodPost, "http://example/api/network/checkpoints/opaque-checkpoint/confirm", confirmForm)
	confirmRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	confirmRequest.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	confirmRequest.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
	confirmResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(confirmResponse, confirmRequest)
	if confirmResponse.Code != http.StatusOK || fake.checkpointConfirmed != "opaque-checkpoint" {
		t.Fatalf("confirm proxy = %d id=%q body=%s", confirmResponse.Code, fake.checkpointConfirmed, confirmResponse.Body.String())
	}

	rollbackForm := strings.NewReader("csrf=token")
	rollbackRequest := httptest.NewRequest(http.MethodPost, "http://example/api/network/checkpoints/opaque-checkpoint/rollback", rollbackForm)
	rollbackRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rollbackRequest.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	rollbackRequest.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
	rollbackResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(rollbackResponse, rollbackRequest)
	if rollbackResponse.Code != http.StatusOK || fake.checkpointRolledBack != "opaque-checkpoint" {
		t.Fatalf("rollback proxy = %d id=%q body=%s", rollbackResponse.Code, fake.checkpointRolledBack, rollbackResponse.Body.String())
	}
	if !strings.Contains(rollbackResponse.Body.String(), "\"interface\":\"enp1s0\"") {
		t.Fatalf("unexpected rollback proxy body: %s", rollbackResponse.Body.String())
	}
}

func TestNetworkCheckpointProxyRequiresAdministratorAndCSRF(t *testing.T) {
	t.Run("csrf", func(t *testing.T) {
		fake := &fakeNetworkAPI{fakeAPI: &fakeAPI{}}
		app, err := New(fake, Config{Version: "1.0.0", ManagementAPI: "v1", ExternalScheme: "http"})
		if err != nil {
			t.Fatal(err)
		}

		request := httptest.NewRequest(http.MethodPost, "http://example/api/network/checkpoints", strings.NewReader("interface=enp1s0"))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("missing CSRF status = %d", response.Code)
		}
	})

	t.Run("administrator role", func(t *testing.T) {
		fake := &fakeNetworkAPI{fakeAPI: &fakeAPI{}, role: "operator"}
		app, err := New(fake, Config{Version: "1.0.0", ManagementAPI: "v1", ExternalScheme: "http"})
		if err != nil {
			t.Fatal(err)
		}

		request := httptest.NewRequest(http.MethodGet, "http://example/api/network/checkpoints/opaque-checkpoint", nil)
		request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("operator checkpoint status = %d, body %s", response.Code, response.Body.String())
		}
	})
}
