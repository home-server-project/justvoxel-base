package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func requestWithPeerUID(method, target string, uid uint32) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	return req.WithContext(context.WithValue(req.Context(), peerUIDKey{}, uid))
}

func TestRequirePeerAllowsOnlyRootAndWebUI(t *testing.T) {
	const webUID uint32 = 4242
	s := &server{webUID: webUID}
	handler := s.requirePeer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, tc := range []struct {
		name string
		uid  uint32
		want int
	}{
		{name: "root", uid: 0, want: http.StatusNoContent},
		{name: "webui", uid: webUID, want: http.StatusNoContent},
		{name: "ordinary-user", uid: 1000, want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, requestWithPeerUID(http.MethodGet, "http://unix/v1/info", tc.uid))
			if rr.Code != tc.want {
				t.Fatalf("peer uid %d returned %d, want %d: %s", tc.uid, rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

func TestLocalRootPeerIsAdministratorWithoutBearerSession(t *testing.T) {
	s := &server{sessions: make(map[string]session)}
	req := requestWithPeerUID(http.MethodGet, "http://unix/v1/admin/users", 0)
	rr := httptest.NewRecorder()

	sess, ok := s.requireAdministrator(rr, req)
	if !ok {
		t.Fatalf("local root peer was rejected: %d %s", rr.Code, rr.Body.String())
	}
	if sess.Username != systemAdminUsername || sess.Role != roleAdministrator || sess.AuthSource != authSourceLocalRoot {
		t.Fatalf("unexpected local root principal: %+v", sess)
	}
}

func TestWebUIPeerStillRequiresBearerSession(t *testing.T) {
	const webUID uint32 = 4242
	s := &server{webUID: webUID, sessions: make(map[string]session)}

	unauthenticated := requestWithPeerUID(http.MethodGet, "http://unix/v1/admin/users", webUID)
	unauthenticatedRR := httptest.NewRecorder()
	if _, ok := s.requireAdministrator(unauthenticatedRR, unauthenticated); ok {
		t.Fatal("WebUI peer without a bearer session was authorized")
	}
	if unauthenticatedRR.Code != http.StatusUnauthorized {
		t.Fatalf("WebUI peer without a bearer session returned %d, want %d", unauthenticatedRR.Code, http.StatusUnauthorized)
	}

	now := time.Now()
	s.sessions["web-session"] = session{
		Username:   systemAdminUsername,
		Role:       roleAdministrator,
		AuthSource: authSourceSystem,
		Created:    now,
		LastSeen:   now,
	}
	authenticated := requestWithPeerUID(http.MethodGet, "http://unix/v1/admin/users", webUID)
	authenticated.Header.Set("Authorization", "Bearer web-session")
	authenticatedRR := httptest.NewRecorder()
	sess, ok := s.requireAdministrator(authenticatedRR, authenticated)
	if !ok {
		t.Fatalf("WebUI bearer session was rejected: %d %s", authenticatedRR.Code, authenticatedRR.Body.String())
	}
	if sess.AuthSource != authSourceSystem || sess.Role != roleAdministrator {
		t.Fatalf("unexpected WebUI principal after bearer authorization: %+v", sess)
	}
}
