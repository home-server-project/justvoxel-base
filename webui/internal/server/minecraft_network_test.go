package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type networkTestAPI struct {
	role string
	*serverInventoryTestAPI
	network       api.MinecraftNetworkStatus
	writes        int
	reads         int
	whitelistArgs []string
	connection    api.MinecraftConnection
	connectionErr error
}

func (f *networkTestAPI) MinecraftNetwork(context.Context, string) (api.MinecraftNetworkStatus, error) {
	f.reads++
	return f.network, nil
}
func (f *networkTestAPI) ConfigureMinecraftNetwork(_ context.Context, _ string, c api.MinecraftNetworkConfig) error {
	f.network.Config = c
	f.writes++
	return nil
}
func (f *networkTestAPI) RecoverMinecraftNetwork(context.Context, string) error {
	f.writes++
	return nil
}
func (f *networkTestAPI) MinecraftNetworkWhitelist(_ context.Context, _ string, platform, action, name string) error {
	f.whitelistArgs = []string{platform, action, name}
	f.writes++
	return nil
}
func (f *networkTestAPI) MinecraftInstanceConnection(context.Context, string, string) (api.MinecraftConnection, error) {
	return f.connection, f.connectionErr
}
func newNetworkTestApp(t *testing.T) (*App, *networkTestAPI) {
	t.Helper()
	_, base := newServerInventoryApp(t)
	f := &networkTestAPI{serverInventoryTestAPI: base, network: api.MinecraftNetworkStatus{Config: api.MinecraftNetworkConfig{JavaPort: 25565, BedrockPort: 19132, WelcomeMessage: "Welcome"}, WhitelistEnforced: true, Servers: []api.MinecraftNetworkServer{{ID: "jv-family1", Software: "paper", Eligible: true, State: "stopped"}, {ID: "jv-kids001", Software: "vanilla", State: "stopped"}}}}
	app, err := New(f, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	return app, f
}
func TestMinecraftNetworkWorkspaceAndCSRF(t *testing.T) {
	app, f := newNetworkTestApp(t)
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/workspace/minecraft-network", ""))
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	for _, want := range []string{"Network Whitelist", "family Survival", "PicoLobby", "Standalone", `name="members"`, `name="welcome_message"`, `value="configure"`, `value="add"`} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatal("missing network UI", want)
		}
	}
	rr = httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodPost, "http://example/workspace/minecraft-network", "csrf=wrong&action=configure"))
	if rr.Code != 403 || f.writes != 0 {
		t.Fatal("CSRF bypass")
	}
}
func TestMinecraftNetworkConnectionPresentation(t *testing.T) {
	app, f := newNetworkTestApp(t)
	standalone := api.InstanceRuntimeSettings{JavaPort: 25569, BedrockPort: 19136, BedrockEnabled: true}
	f.connection = api.MinecraftConnection{ID: "jv-family1", NetworkMember: true, JavaPort: 25565, BedrockPort: 19132, BedrockEnabled: true}
	got, err := app.minecraftConnectionSettings(context.Background(), "session", "jv-family1", standalone)
	if err != nil || got.JavaPort != 25565 || got.BedrockPort != 19132 {
		t.Fatal(got, err)
	}
	if standalone.JavaPort != 25569 {
		t.Fatal("stored standalone port changed")
	}
	f.connectionErr = errors.New("network recovery")
	if _, err = app.minecraftConnectionSettings(context.Background(), "session", "jv-family1", standalone); err == nil {
		t.Fatal("connection silently fell back to backend")
	}
}

func (f *networkTestAPI) Session(ctx context.Context, session string) (api.SessionInfo, error) {
	identity, err := f.serverInventoryTestAPI.Session(ctx, session)
	if f.role != "" {
		identity.Role = f.role
	}
	return identity, err
}
func (f *networkTestAPI) Status(ctx context.Context, session string) (api.Status, error) {
	status, err := f.serverInventoryTestAPI.Status(ctx, session)
	status.System.IPv4 = "192.168.1.50"
	return status, err
}
func TestMinecraftNetworkRoutesRequireAdministrator(t *testing.T) {
	for _, role := range []string{"viewer", "operator"} {
		app, f := newNetworkTestApp(t)
		f.role = role
		for _, path := range []string{"/minecraft/network", "/workspace/minecraft-network"} {
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				rr := httptest.NewRecorder()
				app.Handler().ServeHTTP(rr, authenticatedAdminRequest(method, "http://example"+path, "csrf=csrf-token&action=recover"))
				if rr.Code != 403 || f.writes != 0 {
					t.Fatal("non-administrator reached Minecraft Network", role, path, method, rr.Code)
				}
			}
		}
	}
}
func TestMinecraftNetworkHowToConnectAndInvitationUseProxy(t *testing.T) {
	app, f := newNetworkTestApp(t)
	f.connection = api.MinecraftConnection{ID: "jv-family1", NetworkMember: true, JavaPort: 25565, BedrockPort: 19132, BedrockEnabled: true}
	for _, path := range []string{"/minecraft/how-to-connect?server=jv-family1", "/api/minecraft/servers/invitation?server=jv-family1"} {
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example"+path, ""))
		if rr.Code != 200 {
			t.Fatal(path, rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), "192.168.1.50") || !strings.Contains(rr.Body.String(), "19132") {
			t.Fatal("network endpoint absent", path, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "25567") || strings.Contains(rr.Body.String(), "19134") {
			t.Fatal("invitation exposes private backend endpoint", path)
		}
	}
}
