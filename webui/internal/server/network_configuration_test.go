package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeNetworkConfigurationAPI struct {
	*fakeNetworkAPI
	calls            int
	ethernet         api.EthernetSettings
	provider, action string
}

func (f *fakeNetworkConfigurationAPI) RemoteAccessStatus(context.Context, string) (api.RemoteAccessStatus, error) {
	return api.RemoteAccessStatus{Providers: []api.RemoteAccessProvider{{ID: "playit", DisplayName: "Playit.gg", Installed: true, ServiceActive: true, ServiceEnabled: true}}}, nil
}
func (f *fakeNetworkConfigurationAPI) ChangeRemoteAccess(_ context.Context, _, provider, action string) error {
	f.calls++
	f.provider, f.action = provider, action
	return nil
}
func (f *fakeNetworkConfigurationAPI) ConfigureEthernet(_ context.Context, _, _ string, settings api.EthernetSettings) (api.NetworkWiFiMutation, error) {
	f.calls++
	f.ethernet = settings
	return api.NetworkWiFiMutation{OK: true}, nil
}
func (f *fakeNetworkConfigurationAPI) CheckNetworkConnectivity(context.Context, string) error {
	f.calls++
	return nil
}
func (f *fakeNetworkConfigurationAPI) ReconnectNetwork(context.Context, string, string, string, string) (api.NetworkWiFiMutation, error) {
	f.calls++
	return api.NetworkWiFiMutation{OK: true}, nil
}

func TestNetworkWorkspaceTabsFollowAuthenticatedRole(t *testing.T) {
	for _, role := range []string{"administrator", "operator", "viewer"} {
		fake := &fakeNetworkAPI{fakeAPI: &fakeAPI{}, role: role}
		app, err := New(fake, Config{Version: "1.0.0", ManagementAPI: "v1"})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/network", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, req)
		var status api.NetworkStatus
		if rr.Code != http.StatusOK {
			t.Fatalf("role %s: %d", role, rr.Code)
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		want := []string{"Overview"}
		if role == "administrator" {
			want = []string{"Overview", "Ethernet", "Wi-Fi", "Troubleshoot", "Remote Access"}
		}
		if !reflect.DeepEqual(status.WorkspaceTabs, want) {
			t.Fatalf("role %s tabs %v", role, status.WorkspaceTabs)
		}
	}
}

func TestNetworkConfigurationProxyRequiresAdministratorAndCSRF(t *testing.T) {
	paths := []string{"/api/network/ethernet/enp1s0", "/api/network/reconnect/enp1s0", "/api/network/connectivity-check", "/api/network/remote-access/playit"}
	for _, role := range []string{"administrator", "operator", "viewer"} {
		for _, csrf := range []string{"", "token"} {
			fake := &fakeNetworkConfigurationAPI{fakeNetworkAPI: &fakeNetworkAPI{fakeAPI: &fakeAPI{}, role: role}}
			app, err := New(fake, Config{Version: "1.0.0", ManagementAPI: "v1", ExternalScheme: "http"})
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range paths {
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("csrf="+csrf+"&method=auto&prefix=0&mtu=0&autoconnect=true&profile_uuid=12345678-1234-1234-1234-123456789abc&checkpoint_id=opaque&action=activate"))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
				req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "token"})
				rr := httptest.NewRecorder()
				app.Handler().ServeHTTP(rr, req)
				want := http.StatusForbidden
				if role == "administrator" && csrf == "token" {
					want = http.StatusOK
				}
				if rr.Code != want {
					t.Fatalf("%s %s csrf=%q: %d body=%s", role, path, csrf, rr.Code, rr.Body.String())
				}
			}
			if role != "administrator" || csrf == "" {
				if fake.calls != 0 {
					t.Fatal("rejected mutation reached Management API")
				}
			} else {
				if fake.ethernet.CheckpointID != "opaque" || fake.provider != "playit" || fake.action != "activate" {
					t.Fatal("proxy lost checkpoint or fixed provider action")
				}
			}
		}
	}
}

func TestNetworkWiFiControlsRemainInsideWiFiTab(t *testing.T) {
	source, err := assets.ReadFile("static/network-workspace.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(source)
	start := strings.Index(script, `} else if (selectedTab === "Wi-Fi") {`)
	end := strings.Index(script, `} else if (selectedTab === "Troubleshoot") {`)
	if start < 0 || end <= start {
		t.Fatal("Wi-Fi tab missing")
	}
	wifiTab := script[start:end]
	for _, want := range []string{"renderWiFiNetworks", "renderSavedWiFi", "renderConnectPanel"} {
		if !strings.Contains(wifiTab, want) {
			t.Fatalf("Wi-Fi tab lost %s", want)
		}
	}
	for _, want := range []string{"renderWiFiNetworks(device, currentNetworks.get(device.interface))", "renderSavedWiFi(currentSnapshot)", "renderConnectPanel()", "networkWifiRadio", "networkWifiSavedConnect", "networkWifiJoin", "networkWifiOther", "networkWifiDisconnect", "networkWifiForget", "runCheckpointedMutation", "rollback_timeout_seconds: 90"} {
		if !strings.Contains(script, want) {
			t.Fatalf("Wi-Fi behavior missing %s", want)
		}
	}
	if !strings.Contains(script, `currentSnapshot.workspace_tabs || ["Overview"]`) || !strings.Contains(script, "if (!isAdministrator()) return;") {
		t.Fatal("read-only tab/action guard missing")
	}
}
