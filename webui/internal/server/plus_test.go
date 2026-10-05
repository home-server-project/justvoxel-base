package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type plusTestAPI struct {
	fakeAPI
	playerCalls int
	role        string
}

func (f *plusTestAPI) Players(context.Context, string) (api.Players, error) {
	f.playerCalls++
	panic("Plus must not query game players")
}
func (f *plusTestAPI) Session(context.Context, string) (api.SessionInfo, error) {
	return api.SessionInfo{Username: "admin", Role: f.role}, nil
}
func (f *plusTestAPI) PlusHostLogs(context.Context, string, string) (api.PlusHostLogs, error) {
	return api.PlusHostLogs{OK: true, Output: "host journal"}, nil
}

func TestPlusDesktopAndStatusDoNotUseGameLayer(t *testing.T) {
	client := &plusTestAPI{role: "administrator"}
	app, err := New(client, Config{Plus: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/api/dashboard-status"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, rr.Code, rr.Body.String())
		}
		if path == "/" {
			for _, absent := range []string{"Minecraft Settings", "data-minecraft-open", "data-version-open", "data-backups-open", "data-migration-open", "Back up Minecraft before reboot", "data-quick-look-players"} {
				if strings.Contains(rr.Body.String(), absent) {
					t.Fatalf("Plus desktop exposed %s", absent)
				}
			}
			for _, present := range []string{"System Monitor", "Network", "System Update", "Storage", "data-plus=\"true\"", "Set up Pterodactyl and Drydock"} {
				if !strings.Contains(rr.Body.String(), present) {
					t.Fatalf("Plus desktop lost %s", present)
				}
			}
		}
	}
	if client.playerCalls != 0 {
		t.Fatalf("player queries: %d", client.playerCalls)
	}
}

func TestPlusRejectsLegacyGameRoutes(t *testing.T) {
	app, err := New(&plusTestAPI{role: "administrator"}, Config{Plus: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/minecraft/start", "/api/minecraft/workspace/settings", "/workspace/backups", "/workspace/migration", "/setup/review/apply", "/api/system/workspace/reset/minecraft/apply", "/api/new-storage/minecraft-data/apply", "/api/new-storage/whole-disk/apply", "/settings/server", "/settings/server-migration/import/apply", "/api/version/workspace/update", "/operations/whitelist"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, httptest.NewRequest(method, path, nil))
			if rr.Code != http.StatusNotFound {
				t.Fatalf("%s %s: %d", method, path, rr.Code)
			}
		}
	}
}

func TestPlusSetupAndHostLogsKeepAdministratorBoundary(t *testing.T) {
	for _, role := range []string{"administrator", "operator", "viewer"} {
		app, err := New(&plusTestAPI{role: role}, Config{Plus: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/setup", "/api/system/workspace/logs"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, req)
			expected := http.StatusForbidden
			if role == "administrator" {
				expected = http.StatusOK
			}
			if rr.Code != expected {
				t.Fatalf("%s %s: %d %s", role, path, rr.Code, rr.Body.String())
			}
		}
	}
}
