package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const liveMemoryPath = "http://example/api/minecraft/servers/live-memory"

func TestMinecraftLiveMemoryAuthenticatedReadOnlyRoles(t *testing.T) {
	for _, role := range []string{"administrator", "operator", "viewer", "unknown"} {
		t.Run(role, func(t *testing.T) {
			app, f := newServerInventoryApp(t)
			f.role = role
			reads := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads++
				if r.Method != http.MethodGet || r.URL.Path != "/api/4/containers" {
					t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Cookie") != "" {
					t.Error("session leaked upstream")
				}
				_, _ = w.Write([]byte(`[]`))
			}))
			defer upstream.Close()
			app.minecraftContainersURL = upstream.URL + "/api/4/containers"
			response := httptestResponse(app, authenticatedAdminRequest("GET", liveMemoryPath, ""))
			want := http.StatusOK
			if role == "unknown" {
				want = http.StatusForbidden
			}
			if response.Code != want {
				t.Fatalf("status %d, want %d", response.Code, want)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Error("missing no-store")
			}
			if role == "unknown" && reads != 0 {
				t.Error("unauthorized upstream read")
			}
			if role != "unknown" && reads != 1 {
				t.Error("expected one upstream read")
			}
			if len(f.actions) != 0 || len(f.reads) != 0 || f.action != "" {
				t.Error("live RAM touched Minecraft configuration or lifecycle")
			}
			post := httptestResponse(app, authenticatedAdminRequest("POST", liveMemoryPath, ""))
			if post.Code != http.StatusMethodNotAllowed {
				t.Errorf("POST status %d", post.Code)
			}
		})
	}
}

func TestMinecraftLiveMemoryRequiresSession(t *testing.T) {
	for _, kind := range []string{"missing", "expired", "password-change"} {
		t.Run(kind, func(t *testing.T) {
			app, _ := newServerInventoryApp(t)
			// Authentication must fail before trying to reach this invalid source.
			app.minecraftContainersURL = ":invalid"
			request := httptest.NewRequest("GET", liveMemoryPath, nil)
			want := http.StatusUnauthorized
			if kind == "expired" {
				request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "expired"})
			}
			if kind == "password-change" {
				request = authenticatedAdminRequest("GET", liveMemoryPath, "")
				request.AddCookie(&http.Cookie{Name: mustChangeCookie, Value: "1"})
				want = http.StatusForbidden
			}
			response := httptestResponse(app, request)
			if response.Code != want || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status %d, headers %v", response.Code, response.Header())
			}
		})
	}
}

func TestMinecraftLiveMemorySanitizesContainers(t *testing.T) {
	app, _ := newServerInventoryApp(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
		 {"name":"minecraft-jv-family1","memory_usage":1000,"memory_inactive_file":250,"memory_limit":2048,"id":"private-id","image":"private-image","command":"private-command","path":"/private/path","env":"private-token","cpu_percent":95},
		 {"name":"minecraft-jv-kids001","memory_usage":100,"memory_inactive_file":200,"memory_limit":2048},
		 {"name":"minecraft-jv-abcdef","memory_usage":400,"memory_limit":1024},
		 {"name":"minecraft-jv-123456789012","memory_usage":500,"memory_inactive_file":"bad","memory_limit":null},
		 {"name":"minecraft-jv-null001","memory_usage":600,"memory_inactive_file":null,"memory_limit":-1},
		 {"name":"minecraft-jv-neg0001","memory_usage":700,"memory_inactive_file":-1,"memory_limit":"bad"},
		 {"name":"minecraft-jv-zero001","memory_usage":0,"memory_limit":0},
		 {"name":"minecraft-jv-bad0001","memory_usage":-10,"memory_limit":2048},
		 {"name":"minecraft-jv-bad0002","memory_usage":null,"memory_limit":2048},
		 {"name":"minecraft-jv-bad0003","memory_usage":"NaN","memory_limit":2048},
		 {"name":"minecraft-jv-bad0004","memory_usage":"Infinity","memory_limit":2048},
		 {"name":"minecraft-jv-bad0005","memory_usage":1e999,"memory_limit":2048},
		 {"name":"minecraft-jv-bad0006","memory_limit":2048},
		 {"name":"other-container","memory_usage":99,"memory_limit":100},
		 {"name":"minecraft","memory_usage":99},
		 {"name":"minecraft-jv-short","memory_usage":99},
		 {"name":"minecraft-jv-UPPER01","memory_usage":99},
		 {"name":"minecraft-jv-1234567890123","memory_usage":99},
		 {"name":"prefix-minecraft-jv-family1","memory_usage":99},
		 {"name":"minecraft-jv-family1-suffix","memory_usage":99},
		 {"name":"/minecraft-jv-family1","memory_usage":99},
		 {"name":42,"memory_usage":99}
		]`))
	}))
	defer upstream.Close()
	app.minecraftContainersURL = upstream.URL
	response := httptestResponse(app, authenticatedAdminRequest("GET", liveMemoryPath, ""))
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	var got []map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"id": "jv-family1", "usage_bytes": float64(750), "limit_bytes": float64(2048)},
		{"id": "jv-kids001", "usage_bytes": float64(0), "limit_bytes": float64(2048)},
		{"id": "jv-abcdef", "usage_bytes": float64(400), "limit_bytes": float64(1024)},
		{"id": "jv-123456789012", "usage_bytes": float64(500)},
		{"id": "jv-null001", "usage_bytes": float64(600)},
		{"id": "jv-neg0001", "usage_bytes": float64(700)},
		{"id": "jv-zero001", "usage_bytes": float64(0)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sanitized response = %#v, want %#v", got, want)
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Type") != "application/json" {
		t.Error("incorrect response headers")
	}
	if minecraftContainersGlancesURL != "http://127.0.0.1:61208/api/4/containers" {
		t.Fatal("live RAM must use the narrow loopback containers endpoint")
	}
}

func TestMinecraftLiveMemoryGlancesFailure(t *testing.T) {
	for _, body := range []string{"upstream-error", `{broken`, `{}`, `null`, `[{"name":`, `[] trailing`} {
		t.Run(body, func(t *testing.T) {
			app, _ := newServerInventoryApp(t)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if body == "upstream-error" {
					w.WriteHeader(http.StatusServiceUnavailable)
				}
				_, _ = w.Write([]byte(body))
			}))
			defer upstream.Close()
			app.minecraftContainersURL = upstream.URL
			response := httptestResponse(app, authenticatedAdminRequest("GET", liveMemoryPath, ""))
			if response.Code != http.StatusBadGateway || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status %d, headers %v", response.Code, response.Header())
			}
			if strings.Contains(response.Body.String(), "usage_bytes") {
				t.Fatal("failure fabricated memory data")
			}
		})
	}
	app, _ := newServerInventoryApp(t)
	upstream := httptest.NewServer(http.NotFoundHandler())
	upstream.Close()
	app.minecraftContainersURL = upstream.URL
	response := httptestResponse(app, authenticatedAdminRequest("GET", liveMemoryPath, ""))
	if response.Code != http.StatusBadGateway {
		t.Fatalf("connection failure status %d", response.Code)
	}
}
