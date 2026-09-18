package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

const setupApplyTestOperationID = "12345678-1234-4123-8123-123456789abc"

func TestAdminSetupApplyClientContract(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != adminSetupApplyPath {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer session-token" {
			t.Fatalf("missing bearer session: %q", r.Header.Get("Authorization"))
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		for _, want := range []string{
			`"plan_fingerprint":"` + setupPlanTestFingerprint + `"`,
			`"motd":"Family Minecraft"`,
			`"version_policy":"recommended"`,
			`"source":"//nas/minecraft-backups"`,
			`"username":"backup-user"`,
			`"eula_accepted":true`,
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("apply request missing %s: %s", want, text)
			}
		}
		if strings.Contains(strings.ToLower(text), "password") {
			t.Fatalf("A5.2 apply request must not contain password data: %s", text)
		}
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Body: io.NopCloser(strings.NewReader(`{
				"ok":true,
				"created":true,
				"operation":{
					"schema_version":"v1",
					"operation_id":"` + setupApplyTestOperationID + `",
					"operation_type":"setup",
					"plan_fingerprint":"` + setupPlanTestFingerprint + `",
					"state":"queued",
					"stage":"queued",
					"status":"Setup operation queued.",
					"started_at":"2026-09-17T20:00:00Z",
					"updated_at":"2026-09-17T20:00:00Z",
					"rollback":{"state":"not_started"}
				}
			}`)),
			Header: make(http.Header),
		}, nil
	})}}

	out, err := client.AdminSetupApply(context.Background(), "session-token", AdminSetupApplyRequest{
		PlanFingerprint: setupPlanTestFingerprint,
		Request:         setupPlanTestRequest(),
		EULAAccepted:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.OK || !out.Created || out.Operation == nil || out.Operation.OperationID != setupApplyTestOperationID {
		t.Fatalf("unexpected apply response: %#v", out)
	}
}

func TestAdminSetupApplyClientCarriesSMBPasswordOnlyWhenProvided(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), `"smb_password":"super-secret"`) {
			t.Fatalf("SMB execution secret missing from apply payload: %s", body)
		}
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"created":true,"operation":{"schema_version":"v1","operation_id":"` + setupApplyTestOperationID + `","operation_type":"setup","plan_fingerprint":"` + setupPlanTestFingerprint + `","state":"queued","stage":"queued","status":"Setup operation queued.","started_at":"2026-09-17T20:00:00Z","updated_at":"2026-09-17T20:00:00Z","rollback":{"state":"not_started"}}}`)),
			Header:     make(http.Header),
		}, nil
	})}}
	_, err := client.AdminSetupApply(context.Background(), "session-token", AdminSetupApplyRequest{
		PlanFingerprint: setupPlanTestFingerprint,
		Request:         setupPlanTestRequest(),
		EULAAccepted:    true,
		SMBPassword:     "super-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAdminSetupApplyPreservesStalePlanConflict(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusConflict,
			Body:       io.NopCloser(strings.NewReader(`{"ok":false,"code":"stale_plan","error":"the reviewed setup has changed; return to Review before configuring","created":false}`)),
			Header:     make(http.Header),
		}, nil
	})}}

	out, err := client.AdminSetupApply(context.Background(), "session-token", AdminSetupApplyRequest{
		PlanFingerprint: setupPlanTestFingerprint,
		Request:         setupPlanTestRequest(),
		EULAAccepted:    true,
	})
	if err == nil {
		t.Fatal("expected stale-plan error")
	}
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) || responseErr.StatusCode != http.StatusConflict {
		t.Fatalf("error = %#v, want HTTP 409 ResponseError", err)
	}
	if out.Code != "stale_plan" || out.Error == "" || out.Created {
		t.Fatalf("structured conflict not preserved: %#v", out)
	}
}

func TestAdminSetupApplyRejectsInvalidFingerprintBeforeRequest(t *testing.T) {
	called := false
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("should not run")
	})}}
	_, err := client.AdminSetupApply(context.Background(), "session-token", AdminSetupApplyRequest{
		PlanFingerprint: "not-a-fingerprint",
		Request:         setupPlanTestRequest(),
		EULAAccepted:    true,
	})
	if err == nil || called {
		t.Fatalf("invalid fingerprint err=%v transport_called=%t", err, called)
	}
}

func TestAdminSetupApplyResponseIsStrictAndBounded(t *testing.T) {
	for _, body := range []string{
		`{"ok":false,"code":"stale_plan","error":"stale","created":false,"unexpected":true}`,
		`{"ok":false,"code":"stale_plan","error":"stale","created":false} {}`,
		`{"ok":true,"created":true,"operation":{"schema_version":"v1","operation_id":"bad","operation_type":"setup","plan_fingerprint":"` + setupPlanTestFingerprint + `","state":"queued","stage":"queued","status":"queued","started_at":"x","updated_at":"x","rollback":{"state":"not_started"}}}`,
		`{"ok":false,"code":"error","error":"` + strings.Repeat("x", 40*1024) + `","created":false}`,
	} {
		client := &Client{http: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})}}
		if _, err := client.AdminSetupApply(context.Background(), "session-token", AdminSetupApplyRequest{
			PlanFingerprint: setupPlanTestFingerprint,
			Request:         setupPlanTestRequest(),
			EULAAccepted:    true,
		}); err == nil {
			t.Fatalf("invalid response unexpectedly accepted: %s", body)
		}
	}
}
