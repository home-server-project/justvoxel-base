package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

const persistentOperationTestID = "12345678-1234-4123-8123-123456789abc"
const persistentOperationTestFingerprint = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

const persistentOperationTestBody = `{
  "operation":{
    "schema_version":"v1",
    "operation_id":"12345678-1234-4123-8123-123456789abc",
    "operation_type":"setup",
    "plan_fingerprint":"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    "state":"running",
    "stage":"preparing_storage",
    "status":"Preparing storage.",
    "started_at":"2026-09-17T18:30:00Z",
    "updated_at":"2026-09-17T18:30:05Z",
    "rollback":{"state":"not_started"}
  }
}`

func TestAdminOperationClientContract(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/admin/operations/"+persistentOperationTestID {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer session-token" {
			t.Fatalf("missing bearer session: %q", r.Header.Get("Authorization"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(persistentOperationTestBody)),
			Header:     make(http.Header),
		}, nil
	})}}

	response, err := client.AdminOperation(context.Background(), "session-token", persistentOperationTestID)
	if err != nil {
		t.Fatal(err)
	}
	if response.Operation == nil {
		t.Fatal("operation missing")
	}
	if response.Operation.OperationID != persistentOperationTestID || response.Operation.PlanFingerprint != persistentOperationTestFingerprint {
		t.Fatalf("unexpected operation identity: %#v", response.Operation)
	}
	if response.Operation.State != "running" || response.Operation.Stage != "preparing_storage" || response.Operation.Rollback.State != "not_started" {
		t.Fatalf("unexpected operation status: %#v", response.Operation)
	}
}

func TestAdminCurrentSetupOperationSupportsNoCurrentOperation(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/admin/setup/current-operation" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"operation":null}`)),
			Header:     make(http.Header),
		}, nil
	})}}

	response, err := client.AdminCurrentSetupOperation(context.Background(), "session-token")
	if err != nil {
		t.Fatal(err)
	}
	if response.Operation != nil {
		t.Fatalf("operation = %#v, want nil", response.Operation)
	}
}

func TestAdminOperationRejectsMalformedIDBeforeRequest(t *testing.T) {
	called := false
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("unexpected request")
	})}}
	if _, err := client.AdminOperation(context.Background(), "session-token", "../../etc/passwd"); err == nil {
		t.Fatal("malformed operation id unexpectedly accepted")
	}
	if called {
		t.Fatal("HTTP request made for malformed operation id")
	}
}

func TestPersistentOperationResponseIsStrictAndBounded(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "unknown field", body: strings.Replace(persistentOperationTestBody, `"rollback":{"state":"not_started"}`, `"rollback":{"state":"not_started"},"password":"secret"`, 1)},
		{name: "trailing json", body: persistentOperationTestBody + ` {}`},
		{name: "invalid returned id", body: strings.Replace(persistentOperationTestBody, persistentOperationTestID, "not-an-operation-id", 1)},
		{name: "oversized", body: `{"operation":{"schema_version":"v1","operation_id":"` + persistentOperationTestID + `","operation_type":"setup","plan_fingerprint":"` + persistentOperationTestFingerprint + `","state":"running","stage":"running","status":"` + strings.Repeat("x", 17000) + `","started_at":"x","updated_at":"x","rollback":{"state":"not_started"}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &Client{http: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(tc.body)),
					Header:     make(http.Header),
				}, nil
			})}}
			if _, err := client.AdminCurrentSetupOperation(context.Background(), "session-token"); err == nil {
				t.Fatal("invalid operation response unexpectedly accepted")
			}
		})
	}
}

func TestPersistentOperationClientMapsAuthorizationErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   error
	}{
		{status: http.StatusUnauthorized, want: ErrUnauthorized},
		{status: http.StatusForbidden, want: ErrPasswordChangeRequired},
	} {
		client := &Client{http: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: tc.status,
				Body:       io.NopCloser(strings.NewReader(`{"error":"denied"}`)),
				Header:     make(http.Header),
			}, nil
		})}}
		_, err := client.AdminCurrentSetupOperation(context.Background(), "session-token")
		if !errors.Is(err, tc.want) {
			t.Fatalf("status %d error = %v, want %v", tc.status, err, tc.want)
		}
	}
}
