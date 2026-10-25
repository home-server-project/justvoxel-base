package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type rconWorkspaceAPI struct {
	*phase7API
	calls    []string
	requests []api.InstanceConsoleRequest
	wrongID  bool
}

func (f *rconWorkspaceAPI) MinecraftInstanceConsole(_ context.Context, _, id string, request api.InstanceConsoleRequest) (api.InstanceConsoleResponse, error) {
	f.calls = append(f.calls, id)
	f.requests = append(f.requests, request)
	if f.wrongID {
		id = "jv-kids001"
	}
	return api.InstanceConsoleResponse{OK: true, ID: id, Output: "<script>plain text</script>\nAlex", Commands: []api.InstanceConsoleCommand{{Name: "plugin:warp", Usage: "plugin:warp <place>"}}}, nil
}
func rconWorkspaceApp(t *testing.T) (*App, *rconWorkspaceAPI) {
	t.Helper()
	_, base := phase7App(t)
	f := &rconWorkspaceAPI{phase7API: base}
	app, err := New(f, Config{Version: "test", ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	return app, f
}
func TestSelectedRconRolesCSRFAndIdentity(t *testing.T) {
	for _, role := range []string{"administrator", "operator", "viewer"} {
		for _, discovery := range []bool{false, true} {
			app, f := rconWorkspaceApp(t)
			f.role = role
			body := "csrf=csrf-token&server_id=jv-family1&command=whitelist+list"
			if discovery {
				body = "csrf=csrf-token&server_id=jv-family1&discover=yes"
			}
			rr := phase7Request(app, "POST", "/api/minecraft/workspace/console?server=jv-family1", body)
			if role != "administrator" {
				if rr.Code != 403 || len(f.calls) != 0 {
					t.Fatal("role gained console", role, rr.Code)
				}
				continue
			}
			if rr.Code != 200 || len(f.calls) != 1 || f.calls[0] != "jv-family1" || f.requests[0].Discover != discovery {
				t.Fatal("selected request lost", rr.Code, rr.Body.String(), f.calls)
			}
			if !discovery && f.requests[0].Command != "whitelist list" {
				t.Fatal("command changed")
			}
			if !strings.Contains(rr.Body.String(), `\u003cscript\u003eplain text`) {
				t.Fatal("output lost text escaping", rr.Body.String())
			}
		}
	}
	app, f := rconWorkspaceApp(t)
	for _, tc := range []struct{ path, body string }{
		{"?server=jv-family1", "csrf=wrong&server_id=jv-family1&command=list"},
		{"?server=jv-family1", "csrf=csrf-token&server_id=jv-kids001&command=list"},
		{"?server=jv-kids001", "csrf=csrf-token&server_id=jv-family1&discover=yes"},
		{"", "csrf=csrf-token&server_id=jv-family1&command=list"},
		{"?server=jv-family1&server=jv-kids001", "csrf=csrf-token&server_id=jv-family1&command=list"},
		{"?server=jv-absent1", "csrf=csrf-token&server_id=jv-absent1&command=list"},
	} {
		rr := phase7Request(app, "POST", "/api/minecraft/workspace/console"+tc.path, tc.body)
		if rr.Code < 400 || len(f.calls) != 0 {
			t.Fatal("invalid/stale selection executed", tc, rr.Code, f.calls)
		}
	}
	f.wrongID = true
	rr := phase7Request(app, "POST", "/api/minecraft/workspace/console?server=jv-family1", "csrf=csrf-token&server_id=jv-family1&command=list")
	if rr.Code != 502 {
		t.Fatal("sibling response accepted", rr.Code)
	}
	rr = httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, authenticatedAdminRequest(http.MethodGet, "http://example/api/minecraft/workspace/console?server=jv-family1", ""))
	if rr.Code < 400 {
		t.Fatal("GET executed console")
	}
}
