package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSQLiteWebUserLoginCreatesRoleSession(t *testing.T) {
	store, _ := openTestWebUIStore(t)
	user, err := store.createWebUser("Ilya", webUserRoleOperator, "operator-password")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{store: store, sessions: make(map[string]session)}

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewBufferString(`{"username":"Ilya","password":"operator-password"}`))
	rr := httptest.NewRecorder()
	s.providerLogin(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("operator login returned %d: %s", rr.Code, rr.Body.String())
	}
	var response struct {
		Session    string        `json:"session"`
		Username   string        `json:"username"`
		Role       principalRole `json:"role"`
		AuthSource authSource    `json:"auth_source"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Session == "" || response.Username != "Ilya" || response.Role != roleOperator || response.AuthSource != authSourceWebUI {
		t.Fatalf("unexpected login response: %#v", response)
	}
	sess, ok := s.sessions[response.Session]
	if !ok || sess.WebUserID != user.ID || sess.Role != roleOperator || sess.Username != "Ilya" {
		t.Fatalf("unexpected stored session: %#v ok=%v", sess, ok)
	}
}

func TestDisabledSQLiteWebUserCannotLogin(t *testing.T) {
	store, _ := openTestWebUIStore(t)
	user, err := store.createWebUser("Maya", webUserRoleViewer, "viewer-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.setWebUserEnabled(user.ID, false); err != nil {
		t.Fatal(err)
	}
	s := &server{store: store, sessions: make(map[string]session)}
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewBufferString(`{"username":"Maya","password":"viewer-password"}`))
	rr := httptest.NewRecorder()
	s.providerLogin(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("disabled viewer login returned %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSessionStatusReturnsServerSideIdentity(t *testing.T) {
	s := roleServerForTest(roleViewer)
	s.store, _ = openTestWebUIStore(t)
	req := authorizedRequest(http.MethodGet, "/v1/session", "")
	rr := httptest.NewRecorder()
	s.sessionStatus(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("session status returned %d: %s", rr.Code, rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte(`"role":"viewer"`)) || !bytes.Contains(rr.Body.Bytes(), []byte(`"auth_source":"webui"`)) {
		t.Fatalf("session identity missing: %s", rr.Body.String())
	}
}
