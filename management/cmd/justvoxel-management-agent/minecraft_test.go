package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func adminServerForTest() *server {
	now := time.Now()
	return &server{sessions: map[string]session{
		"token": {
			Username:   systemAdminUsername,
			Role:       roleAdministrator,
			AuthSource: authSourceSystem,
			Created:    now,
			LastSeen:   now,
		},
	}}
}

func roleServerForTest(role principalRole) *server {
	now := time.Now()
	return &server{sessions: map[string]session{
		"token": {
			Username:   "test-user",
			Role:       role,
			AuthSource: authSourceWebUI,
			WebUserID:  1,
			Created:    now,
			LastSeen:   now,
		},
	}}
}

func authorizedRequest(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Content-Type", "application/json")
	return req
}

func localRootMinecraftRequest(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req.WithContext(context.WithValue(req.Context(), peerUIDKey{}, uint32(0)))
}

func TestPlayersRequiresAuthentication(t *testing.T) {
	s := adminServerForTest()
	called := false
	oldRunner := runWebHelper
	runWebHelper = func(context.Context, ...string) ([]byte, int, error) {
		called = true
		return nil, 0, nil
	}
	defer func() { runWebHelper = oldRunner }()

	rr := httptest.NewRecorder()
	s.players(rr, httptest.NewRequest(http.MethodGet, "http://unix/v1/players", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if called {
		t.Fatal("helper executed for unauthenticated request")
	}
}

func TestPlayersUsesAllowlistedHelperAndReturnsJSON(t *testing.T) {
	s := adminServerForTest()
	oldRunner := runWebHelper
	runWebHelper = func(_ context.Context, args ...string) ([]byte, int, error) {
		if len(args) != 1 || args[0] != "players" {
			t.Fatalf("unexpected helper args: %#v", args)
		}
		return []byte(`{"configured":true,"state":"running","online":0,"max":10,"names":[]}`), 0, nil
	}
	defer func() { runWebHelper = oldRunner }()

	rr := httptest.NewRecorder()
	s.players(rr, authorizedRequest(http.MethodGet, "http://unix/v1/players", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"online":0`) || !strings.Contains(rr.Body.String(), `"names":[]`) {
		t.Fatalf("unexpected player response: %s", rr.Body.String())
	}
}

func TestLocalRootCanReadPlayersWithoutBearerSession(t *testing.T) {
	s := &server{sessions: make(map[string]session)}
	oldRunner := runWebHelper
	runWebHelper = func(_ context.Context, args ...string) ([]byte, int, error) {
		if len(args) != 1 || args[0] != "players" {
			t.Fatalf("unexpected helper args: %#v", args)
		}
		return []byte(`{"configured":true,"state":"running","online":2,"max":10,"names":["Alex","Steve"]}`), 0, nil
	}
	defer func() { runWebHelper = oldRunner }()

	req := requestWithPeerUID(http.MethodGet, "http://unix/v1/players", 0)
	rr := httptest.NewRecorder()
	s.players(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("local root Players request failed: %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"online":2`) || !strings.Contains(rr.Body.String(), `"Alex"`) {
		t.Fatalf("unexpected local root Players response: %s", rr.Body.String())
	}
}

func TestLocalRootCanControlMinecraftWithoutBearerSession(t *testing.T) {
	s := &server{sessions: make(map[string]session)}
	oldRunner := runWebHelper
	defer func() { runWebHelper = oldRunner }()

	tests := []struct {
		action string
		call   func(http.ResponseWriter, *http.Request)
	}{
		{action: "start", call: s.minecraftStart},
		{action: "stop", call: s.minecraftStop},
		{action: "restart", call: s.minecraftRestart},
	}

	for _, tc := range tests {
		t.Run(tc.action, func(t *testing.T) {
			runWebHelper = func(_ context.Context, args ...string) ([]byte, int, error) {
				if len(args) != 1 || args[0] != tc.action {
					t.Fatalf("unexpected helper args: %#v", args)
				}
				return []byte(`{"ok":true,"action":"` + tc.action + `","message":"accepted"}`), 0, nil
			}
			rr := httptest.NewRecorder()
			tc.call(rr, localRootMinecraftRequest(http.MethodPost, "http://unix/v1/minecraft/"+tc.action, `{}`))
			if rr.Code != http.StatusOK {
				t.Fatalf("local root %s failed: %d %s", tc.action, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestLocalRootRestartStillRequiresExplicitPlayerConfirmation(t *testing.T) {
	s := &server{sessions: make(map[string]session)}
	oldRunner := runWebHelper
	defer func() { runWebHelper = oldRunner }()

	callCount := 0
	runWebHelper = func(_ context.Context, args ...string) ([]byte, int, error) {
		callCount++
		switch callCount {
		case 1:
			if len(args) != 1 || args[0] != "restart" {
				t.Fatalf("unexpected initial helper args: %#v", args)
			}
			return []byte(`{"ok":false,"action":"restart","confirmation_required":true,"reason":"players_online","players":["Alex"],"online":1}`), 10, errors.New("confirmation required")
		case 2:
			if len(args) != 2 || args[0] != "restart" || args[1] != "--confirm-players" {
				t.Fatalf("unexpected confirmed helper args: %#v", args)
			}
			return []byte(`{"ok":true,"action":"restart","message":"Minecraft restart requested."}`), 0, nil
		default:
			t.Fatalf("unexpected helper call %d", callCount)
			return nil, 0, nil
		}
	}

	rr := httptest.NewRecorder()
	s.minecraftRestart(rr, localRootMinecraftRequest(http.MethodPost, "http://unix/v1/minecraft/restart", `{}`))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"confirmation_required":true`) {
		t.Fatalf("local root restart did not require player confirmation: %d %s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	s.minecraftRestart(rr, localRootMinecraftRequest(http.MethodPost, "http://unix/v1/minecraft/restart", `{"confirm_players":true}`))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"ok":true`) {
		t.Fatalf("confirmed local root restart failed: %d %s", rr.Code, rr.Body.String())
	}
}

func TestViewerCanReadPlayersButCannotMutateMinecraft(t *testing.T) {
	s := roleServerForTest(roleViewer)
	oldRunner := runWebHelper
	called := false
	runWebHelper = func(_ context.Context, args ...string) ([]byte, int, error) {
		called = true
		if len(args) == 1 && args[0] == "players" {
			return []byte(`{"configured":true,"state":"running","online":0,"max":10,"names":[]}`), 0, nil
		}
		return []byte(`{"ok":true}`), 0, nil
	}
	defer func() { runWebHelper = oldRunner }()

	rr := httptest.NewRecorder()
	s.players(rr, authorizedRequest(http.MethodGet, "http://unix/v1/players", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("viewer read failed: %d %s", rr.Code, rr.Body.String())
	}
	called = false
	for _, action := range []struct {
		path string
		call func(http.ResponseWriter, *http.Request)
	}{
		{"/v1/minecraft/start", s.minecraftStart},
		{"/v1/minecraft/stop", s.minecraftStop},
		{"/v1/minecraft/restart", s.minecraftRestart},
	} {
		rr = httptest.NewRecorder()
		action.call(rr, authorizedRequest(http.MethodPost, action.path, `{}`))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("viewer mutation %s returned %d", action.path, rr.Code)
		}
		if called {
			t.Fatalf("helper executed for forbidden viewer mutation %s", action.path)
		}
	}
}

func TestOperatorStartAllowedStopAndRestartClosedUntilQuotaLayer(t *testing.T) {
	s := roleServerForTest(roleOperator)
	oldRunner := runWebHelper
	runWebHelper = func(_ context.Context, args ...string) ([]byte, int, error) {
		if len(args) != 1 || args[0] != "start" {
			t.Fatalf("unexpected helper args: %#v", args)
		}
		return []byte(`{"ok":true,"action":"start","message":"Minecraft start requested."}`), 0, nil
	}
	defer func() { runWebHelper = oldRunner }()

	rr := httptest.NewRecorder()
	s.minecraftStart(rr, authorizedRequest(http.MethodPost, "http://unix/v1/minecraft/start", `{}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("operator start failed: %d %s", rr.Code, rr.Body.String())
	}
	for _, action := range []struct {
		path string
		call func(http.ResponseWriter, *http.Request)
	}{
		{"/v1/minecraft/stop", s.minecraftStop},
		{"/v1/minecraft/restart", s.minecraftRestart},
	} {
		rr = httptest.NewRecorder()
		action.call(rr, authorizedRequest(http.MethodPost, action.path, `{}`))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("operator action %s returned %d, want 403", action.path, rr.Code)
		}
	}
}

func TestMinecraftStartUsesAllowlistedAction(t *testing.T) {
	s := adminServerForTest()
	oldRunner := runWebHelper
	runWebHelper = func(_ context.Context, args ...string) ([]byte, int, error) {
		if len(args) != 1 || args[0] != "start" {
			t.Fatalf("unexpected helper args: %#v", args)
		}
		return []byte(`{"ok":true,"action":"start","message":"Minecraft start requested."}`), 0, nil
	}
	defer func() { runWebHelper = oldRunner }()

	rr := httptest.NewRecorder()
	s.minecraftStart(rr, authorizedRequest(http.MethodPost, "http://unix/v1/minecraft/start", `{}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestMinecraftRestartRequiresExplicitPlayerConfirmation(t *testing.T) {
	s := adminServerForTest()
	oldRunner := runWebHelper
	runWebHelper = func(_ context.Context, args ...string) ([]byte, int, error) {
		if len(args) != 1 || args[0] != "restart" {
			t.Fatalf("unexpected helper args: %#v", args)
		}
		return []byte(`{"ok":false,"action":"restart","confirmation_required":true,"reason":"players_online","players":["Alex","Steve"],"online":2}`), 10, errors.New("confirmation required")
	}
	defer func() { runWebHelper = oldRunner }()

	rr := httptest.NewRecorder()
	s.minecraftRestart(rr, authorizedRequest(http.MethodPost, "http://unix/v1/minecraft/restart", `{}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 confirmation response, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"confirmation_required":true`) {
		t.Fatalf("missing confirmation response: %s", rr.Body.String())
	}
}

func TestMinecraftConfirmedRestartUsesOnlyConfirmationFlag(t *testing.T) {
	s := adminServerForTest()
	oldRunner := runWebHelper
	runWebHelper = func(_ context.Context, args ...string) ([]byte, int, error) {
		if len(args) != 2 || args[0] != "restart" || args[1] != "--confirm-players" {
			t.Fatalf("unexpected helper args: %#v", args)
		}
		return []byte(`{"ok":true,"action":"restart","message":"Minecraft restart requested."}`), 0, nil
	}
	defer func() { runWebHelper = oldRunner }()

	rr := httptest.NewRecorder()
	s.minecraftRestart(rr, authorizedRequest(http.MethodPost, "http://unix/v1/minecraft/restart", `{"confirm_players":true}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestMinecraftPlayerStatusFailureFailsClosed(t *testing.T) {
	s := adminServerForTest()
	oldRunner := runWebHelper
	runWebHelper = func(_ context.Context, _ ...string) ([]byte, int, error) {
		return []byte(`{"ok":false,"reason":"player_status_unavailable","message":"Could not confirm player status through RCON."}`), 3, errors.New("player status unavailable")
	}
	defer func() { runWebHelper = oldRunner }()

	rr := httptest.NewRecorder()
	s.minecraftStop(rr, authorizedRequest(http.MethodPost, "http://unix/v1/minecraft/stop", `{}`))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestMinecraftRoutesDoNotExposeArbitraryActions(t *testing.T) {
	s := adminServerForTest()
	mux := http.NewServeMux()
	registerMinecraftRoutes(mux, s)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, authorizedRequest(http.MethodPost, "http://unix/v1/minecraft/shell", `{}`))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unregistered action, got %d", rr.Code)
	}
}
