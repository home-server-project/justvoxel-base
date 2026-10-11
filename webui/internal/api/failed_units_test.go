package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestFailedUnitClientValidationAndAuthorization(t *testing.T) {
	unit := `srv-my\x20disk.mount`
	calls := 0
	status := http.StatusOK
	body := `{"failed_units":[{"unit":"srv-my\\x20disk.mount","description":"Disk","active_state":"failed","sub_state":"failed"}]}`
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer session-token" {
			t.Fatal("missing authorized GET")
		}
		if r.URL.Path != "/v1/admin/systemd/failed" {
			if r.URL.EscapedPath() != "/v1/admin/systemd/failed/"+url.PathEscape(unit)+"/journal" || r.URL.Query().Get("limit") != "60" {
				t.Fatalf("unsafe journal request: %s", r.URL)
			}
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	units, err := client.AdminFailedUnits(context.Background(), "session-token")
	if err != nil || len(units) != 1 || units[0].Unit != unit {
		t.Fatalf("units %#v: %v", units, err)
	}
	body = "current boot journal\n"
	data, err := client.AdminFailedUnitJournal(context.Background(), "session-token", unit)
	if err != nil || string(data) != body {
		t.Fatalf("journal: %q %v", data, err)
	}
	for _, invalid := range []string{"../etc/passwd", "*.service", "a.service;reboot", "a.service\n", "a/b.mount", `a\xZZ.mount`} {
		before := calls
		if _, err := client.AdminFailedUnitJournal(context.Background(), "session-token", invalid); err == nil || calls != before {
			t.Fatalf("unsafe request %q", invalid)
		}
	}
	for _, valid := range []string{"a.service", "a.timer", "a.mount", unit, "-.mount", "a@b.service", "worker@user@host.service", "a.socket", "a.path", "a.target", "a.automount", "a.swap", "a.device", "a.scope", "a.slice"} {
		if !ValidFailedUnitName(valid) {
			t.Errorf("rejected %q", valid)
		}
	}
	for code, want := range map[int]error{401: ErrUnauthorized, 403: ErrPasswordChangeRequired} {
		status = code
		if _, err := client.AdminFailedUnits(context.Background(), "session-token"); !errors.Is(err, want) {
			t.Fatalf("list status %d: %v", status, err)
		}
		if _, err := client.AdminFailedUnitJournal(context.Background(), "session-token", unit); !errors.Is(err, want) {
			t.Fatalf("journal status %d: %v", status, err)
		}
	}
	status = http.StatusNotFound
	body = `{"error":"privileged secret details"}`
	_, err = client.AdminFailedUnitJournal(context.Background(), "session-token", unit)
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) || responseErr.StatusCode != 404 || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestFailedUnitClientRejectsInvalidOrOversizedResponses(t *testing.T) {
	body := ""
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	body = `{"failed_units":[]}`
	if units, err := client.AdminFailedUnits(context.Background(), "token"); err != nil || units == nil || len(units) != 0 {
		t.Fatalf("empty list: %#v %v", units, err)
	}
	for _, invalid := range []string{`{}`, `{"failed_units":null}`, `{"failed_units":[{"unit":"*.service","active_state":"failed"}]}`, `{"failed_units":[{"unit":"a.service","active_state":"active"}]}`, strings.Repeat("x", failedUnitOutputLimit+1)} {
		body = invalid
		if _, err := client.AdminFailedUnits(context.Background(), "token"); err == nil {
			t.Fatal("accepted invalid list")
		}
	}
	units := make([]FailedUnit, 101)
	for i := range units {
		units[i] = FailedUnit{Unit: "a.service", ActiveState: "failed"}
	}
	data, _ := json.Marshal(map[string]any{"failed_units": units})
	body = string(data)
	if _, err := client.AdminFailedUnits(context.Background(), "token"); err == nil {
		t.Fatal("accepted excessive units")
	}
	body = strings.Repeat("x", failedUnitOutputLimit+1)
	if _, err := client.AdminFailedUnitJournal(context.Background(), "token", "a.service"); err == nil {
		t.Fatal("accepted excessive journal")
	}
	body = strings.Repeat("x", failedUnitOutputLimit)
	if data, err := client.AdminFailedUnitJournal(context.Background(), "token", "a.service"); err != nil || len(data) != failedUnitOutputLimit {
		t.Fatalf("bounded journal: %d %v", len(data), err)
	}
}
