package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAdminValidationClientPreservesPassedResult(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != adminValidationPath {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer session-token" {
			t.Fatalf("missing bearer session: %q", r.Header.Get("Authorization"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("{\"ok\":true,\"output\":\"OK: runtime files are healthy\\n\\nJustVoxel validation passed.\\n\",\"exit_code\":0}")),
			Header:     make(http.Header),
		}, nil
	})}}

	result, err := client.AdminValidation(context.Background(), "session-token")
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.ExitCode != 0 || !strings.Contains(result.Output, "JustVoxel validation passed.") {
		t.Fatalf("unexpected validation result: %#v", result)
	}
}

func TestAdminValidationClientPreservesDetectedProblems(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("{\"ok\":false,\"output\":\"FAIL: minecraft.service is not active\\n\\nJustVoxel validation failed with 1 problem(s).\\n\",\"exit_code\":1}")),
			Header:     make(http.Header),
		}, nil
	})}}

	result, err := client.AdminValidation(context.Background(), "session-token")
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || result.ExitCode != 1 || !strings.Contains(result.Output, "FAIL: minecraft.service is not active") {
		t.Fatalf("detected problems were not preserved: %#v", result)
	}
}

func TestAdminValidationClientKeepsUnavailableSeparateFromProblems(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Body:       io.NopCloser(strings.NewReader(`{"error":"JustVoxel validation is unavailable"}`)),
			Header:     make(http.Header),
		}, nil
	})}}

	result, err := client.AdminValidation(context.Background(), "session-token")
	if err == nil {
		t.Fatal("expected validation service failure")
	}
	if result != (AdminValidationResponse{}) {
		t.Fatalf("service failure masqueraded as validation result: %#v", result)
	}
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) || responseErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("error = %#v, want HTTP 503 ResponseError", err)
	}
}

func TestAdminValidationClientRejectsInconsistentResult(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"output":"unexpected","exit_code":1}`)),
			Header:     make(http.Header),
		}, nil
	})}}

	if _, err := client.AdminValidation(context.Background(), "session-token"); err == nil || !strings.Contains(err.Error(), "inconsistent validation result") {
		t.Fatalf("error = %v, want inconsistent-result rejection", err)
	}
}

func TestAdminValidationClientUsesBoundedTimeout(t *testing.T) {
	oldTimeout := adminValidationClientTimeout
	adminValidationClientTimeout = 10 * time.Millisecond
	defer func() { adminValidationClientTimeout = oldTimeout }()

	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-time.After(250 * time.Millisecond):
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"ok":true,"output":"ok","exit_code":0}`)),
				Header:     make(http.Header),
			}, nil
		}
	})}}

	started := time.Now()
	_, err := client.AdminValidation(context.Background(), "session-token")
	if err == nil || !strings.Contains(err.Error(), "management API unavailable") {
		t.Fatalf("error = %v, want bounded transport timeout", err)
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("validation timeout took too long: %s", elapsed)
	}
}
