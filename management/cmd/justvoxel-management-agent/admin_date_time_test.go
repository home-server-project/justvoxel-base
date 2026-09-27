package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSystemTimezoneValidationRejectsCommandsAndTraversal(t *testing.T) {
	for _, zone := range []string{"../../etc/passwd", "UTC;reboot", "/etc/passwd", "America/../UTC", ""} {
		if validSystemTimezone(zone) { t.Fatalf("accepted unsafe timezone %q", zone) }
	}
	if !validSystemTimezone("UTC") { t.Fatal("rejected UTC") }
}

func TestDateTimeMutationRequiresAdministrator(t *testing.T) {
	old := runTimedatectl
	defer func() { runTimedatectl = old }()
	called := false
	runTimedatectl = func(_ context.Context, _ ...string) ([]byte, error) { called = true; return nil, nil }
	for _, role := range []principalRole{roleOperator, roleViewer} {
		s := surfaceTestServer(t, role)
		rr := httptest.NewRecorder()
		s.adminDateTimeApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/date-time", `{"timezone":"UTC","automatic":true}`))
		if rr.Code != http.StatusForbidden { t.Fatalf("role %s: %d", role, rr.Code) }
	}
	if called { t.Fatal("non-administrator reached host clock") }
}

func TestDateTimeApplyUsesFixedTimedatectlArguments(t *testing.T) {
	old := runTimedatectl
	defer func() { runTimedatectl = old }()
	var calls [][]string
	runTimedatectl = func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		if args[0] == "show" { return []byte("Timezone=UTC\nNTP=yes\nNTPSynchronized=yes\n"), nil }
		return nil, nil
	}
	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	s.adminDateTimeApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/date-time", `{"timezone":"UTC","automatic":false,"date":"2026-09-27","time":"12:34"}`))
	if rr.Code != http.StatusOK { t.Fatalf("apply: %d %s", rr.Code, rr.Body.String()) }
	joined := make([]string, 0, len(calls))
	for _, args := range calls { joined = append(joined, strings.Join(args, " ")) }
	if !strings.Contains(strings.Join(joined, "\n"), "set-ntp false\nset-time 2026-09-27 12:34:00") { t.Fatalf("commands: %#v", calls) }
}

func TestDateTimeRejectsManualInputWithAutomaticTime(t *testing.T) {
	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	s.adminDateTimeApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/date-time", `{"timezone":"UTC","automatic":true,"date":"2026-09-27","time":"12:34"}`))
	if rr.Code != http.StatusBadRequest { t.Fatalf("status %d", rr.Code) }
}

func TestDateTimeRejectsInvalidManualDateBeforeHostCall(t *testing.T) {
	old := runTimedatectl
	defer func() { runTimedatectl = old }()
	called := false
	runTimedatectl = func(_ context.Context, _ ...string) ([]byte, error) { called = true; return nil, nil }
	s := surfaceTestServer(t, roleAdministrator)
	rr := httptest.NewRecorder()
	s.adminDateTimeApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/date-time", `{"timezone":"UTC","automatic":false,"date":"2026-02-30","time":"25:99"}`))
	if rr.Code != http.StatusBadRequest || called { t.Fatalf("status %d, host called %v", rr.Code, called) }
}
