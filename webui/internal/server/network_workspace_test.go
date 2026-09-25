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
	scanned string
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
		Networks: []api.WiFiNetwork{{SSID: "Home WiFi", Strength: 84, Security: "wpa-personal"}},
	}, nil
}

func (f *fakeNetworkAPI) RequestWiFiScan(_ context.Context, session, interfaceName string) error {
	if session != "session-token" {
		return api.ErrUnauthorized
	}
	f.scanned = interfaceName
	return nil
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
