package main

import (
	"context"
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

func TestGlobalMinecraftRuntimeRoutesRetired(t *testing.T) {
	mux := http.NewServeMux()
	registerMinecraftRoutes(mux, adminServerForTest())
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/v1/players"},
		{http.MethodPost, "/v1/minecraft/start"},
		{http.MethodPost, "/v1/minecraft/stop"},
	} {
		t.Run(route.path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, authorizedRequest(route.method, route.path, `{}`))
			if rr.Code != http.StatusNotFound {
				t.Fatalf("retired route returned %d: %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestInstanceMinecraftRuntimeRoutesRemainRegistered(t *testing.T) {
	mux := http.NewServeMux()
	registerMinecraftRoutes(mux, adminServerForTest())
	for _, route := range []struct{ method, action string }{
		{http.MethodGet, "players"},
		{http.MethodGet, "status"},
		{http.MethodPost, "start"},
		{http.MethodPost, "stop"},
		{http.MethodPost, "restart"},
	} {
		t.Run(route.action, func(t *testing.T) {
			_, pattern := mux.Handler(authorizedRequest(route.method, "/v1/minecraft/instances/jv-a1b2c3/"+route.action, `{}`))
			want := route.method + " /v1/minecraft/instances/{id}/" + route.action
			if pattern != want {
				t.Fatalf("instance route pattern=%q, want %q", pattern, want)
			}
		})
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

func TestGlobalMinecraftRestartRouteRetired(t *testing.T) {
	mux := http.NewServeMux()
	registerMinecraftRoutes(mux, adminServerForTest())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/restart", `{}`))
	if rr.Code != http.StatusNotFound {
		t.Fatal(rr.Code, rr.Body.String())
	}
}
