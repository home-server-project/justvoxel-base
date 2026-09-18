package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAuthStatusClientContract(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/auth" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer session-token" {
			t.Fatalf("missing bearer session: %q", r.Header.Get("Authorization"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"mode":"system","username":"voxel","minimum_password_len":8}`)),
			Header:     make(http.Header),
		}, nil
	})}}

	status, err := client.AuthStatus(context.Background(), "session-token")
	if err != nil {
		t.Fatal(err)
	}
	if status.Mode != "system" || status.Username != "voxel" || status.MinimumPasswordLen != 8 {
		t.Fatalf("unexpected authentication status: %#v", status)
	}
}

func TestChangeAuthModeClientContract(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/auth/mode" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		for _, want := range []string{`"mode":"separate"`, `"system_password":"system-secret"`, `"new_web_password":"web-secret"`} {
			if !strings.Contains(text, want) {
				t.Fatalf("request body missing %s: %s", want, text)
			}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"auth_mode":"separate","reauthenticate":true}`)),
			Header:     make(http.Header),
		}, nil
	})}}

	result, err := client.ChangeAuthMode(context.Background(), "session-token", AuthModeChange{
		Mode:               "separate",
		SystemPassword:     "system-secret",
		NewWebPassword:     "web-secret",
		ConfirmWebPassword: "web-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.AuthMode != "separate" || !result.Reauthenticate {
		t.Fatalf("unexpected mode-change response: %#v", result)
	}
}
