package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func failedUnitFixture(names ...string) []byte {
	rows := make([]map[string]string, 0)
	for _, name := range names {
		rows = append(rows, map[string]string{"unit": name, "description": "Example unit", "active": "failed", "sub": "failed"})
	}
	data, _ := json.Marshal(rows)
	return data
}

func failedUnitTestRequest(s *server, path string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	registerAdminFailedUnitRoutes(mux, s)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, surfaceRequest(http.MethodGet, path, ""))
	return rr
}

func TestFailedUnitNames(t *testing.T) {
	for _, unit := range []string{"minecraft.service", "backup.timer", "data.mount", `srv-my\x20disk.mount`, `worker@my\x2dinstance.service`, "-.mount", "a.socket", "a.path", "a.target", "a.automount", "a.swap", "a.device", "a.scope", "a.slice", "worker@.service", "worker@user@host.service"} {
		if !validFailedUnitName(unit) {
			t.Errorf("rejected %q", unit)
		}
	}
	for _, unit := range []string{"", "../etc/passwd", "x/y.mount", "*.service", "[ab].service", "a.service;reboot", "a.service\n", "a service", "@b.service", `a\q.mount`, `a\xZZ.mount`, "a.unknown", strings.Repeat("x", 256) + ".service"} {
		if validFailedUnitName(unit) {
			t.Errorf("accepted %q", unit)
		}
	}
}

func TestFailedUnitListAuthorizationAndStructure(t *testing.T) {
	old := runFailedUnitCommand
	defer func() { runFailedUnitCommand = old }()
	calls := 0
	data := failedUnitFixture()
	runFailedUnitCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 6*time.Second {
			t.Fatal("missing short timeout")
		}
		if name != "systemctl" || !reflect.DeepEqual(args, []string{"list-units", "--failed", "--all", "--no-pager", "--output=json"}) {
			t.Fatalf("unexpected command %s %#v", name, args)
		}
		return data, nil
	}
	for _, role := range []principalRole{roleViewer, roleOperator} {
		s := surfaceTestServer(t, role)
		if rr := failedUnitTestRequest(s, "/v1/admin/systemd/failed"); rr.Code != http.StatusForbidden {
			t.Fatalf("role %s: %d", role, rr.Code)
		}
		if rr := failedUnitTestRequest(s, "/v1/admin/systemd/failed/minecraft.service/journal"); rr.Code != http.StatusForbidden {
			t.Fatalf("journal role %s: %d", role, rr.Code)
		}
	}
	if calls != 0 {
		t.Fatal("non-administrator reached commands")
	}
	s := surfaceTestServer(t, roleAdministrator)
	for _, names := range [][]string{nil, {"minecraft.service", "backup.timer", `srv-my\x20disk.mount`}} {
		data = failedUnitFixture(names...)
		rr := failedUnitTestRequest(s, "/v1/admin/systemd/failed")
		if rr.Code != http.StatusOK {
			t.Fatalf("list: %d %s", rr.Code, rr.Body.String())
		}
		var payload struct {
			Units []failedUnit `json:"failed_units"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Units == nil || len(payload.Units) != len(names) {
			t.Fatalf("list %#v", payload.Units)
		}
		for i, unit := range payload.Units {
			if unit.Unit != names[i] || unit.ActiveState != "failed" || unit.Description != "Example unit" {
				t.Fatalf("unit %#v", unit)
			}
		}
	}
	names := make([]string, 130)
	for i := range names {
		names[i] = fmt.Sprintf("example-%d.service", i)
	}
	data = failedUnitFixture(names...)
	units, err := enumerateFailedUnits(context.Background())
	if err != nil || len(units) != 100 {
		t.Fatalf("bound: %d, %v", len(units), err)
	}
}

func TestFailedUnitJournalFreshMembershipAndBounds(t *testing.T) {
	old := runFailedUnitCommand
	defer func() { runFailedUnitCommand = old }()
	unit := `srv-my\x20disk.mount`
	data := failedUnitFixture(unit, "minecraft.service")
	var calls []string
	expectedLimit := "60"
	runFailedUnitCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name)
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("no timeout")
		}
		switch name {
		case "systemctl":
			return data, nil
		case "journalctl":
			want := []string{"-b", "-u", unit, "--no-pager", "-n", expectedLimit, "-o", "short-iso"}
			if !reflect.DeepEqual(args, want) {
				t.Fatalf("journal arguments %#v, want %#v", args, want)
			}
			return []byte("current boot journal\n"), nil
		default:
			t.Fatalf("unexpected command %s", name)
			return nil, nil
		}
	}
	s := surfaceTestServer(t, roleAdministrator)
	path := "/v1/admin/systemd/failed/" + url.PathEscape(unit) + "/journal"
	for _, query := range []string{"", "?limit=bad", "?limit=0", "?limit=-1", "?limit=60", "?limit=1000"} {
		expectedLimit = "60"
		if query == "?limit=1000" {
			expectedLimit = "200"
		}
		calls = nil
		rr := failedUnitTestRequest(s, path+query)
		if rr.Code != http.StatusOK || rr.Body.String() != "current boot journal\n" || rr.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("journal %s: %d %s", query, rr.Code, rr.Body.String())
		}
		if !reflect.DeepEqual(calls, []string{"systemctl", "journalctl"}) {
			t.Fatalf("fresh enumeration missing: %#v", calls)
		}
	}
	// The earlier list succeeds, but the next request must re-enumerate.
	if rr := failedUnitTestRequest(s, "/v1/admin/systemd/failed"); rr.Code != 200 {
		t.Fatal(rr.Code)
	}
	data = failedUnitFixture("minecraft.service")
	for _, missing := range []string{unit, "not-failed.service", "Minecraft.service"} {
		calls = nil
		rr := failedUnitTestRequest(s, "/v1/admin/systemd/failed/"+url.PathEscape(missing)+"/journal")
		if rr.Code != http.StatusNotFound || !reflect.DeepEqual(calls, []string{"systemctl"}) {
			t.Fatalf("nonmember %q: %d %#v", missing, rr.Code, calls)
		}
	}
	for _, invalid := range []string{"a.service;reboot", "*.service", "a/b.mount", `a\bad.mount`, "a.service\n"} {
		calls = nil
		rr := failedUnitTestRequest(s, "/v1/admin/systemd/failed/"+url.PathEscape(invalid)+"/journal")
		if rr.Code != http.StatusBadRequest || len(calls) != 0 {
			t.Fatalf("unsafe %q: %d %#v", invalid, rr.Code, calls)
		}
	}
}

func TestFailedUnitUnavailableAndOversize(t *testing.T) {
	old := runFailedUnitCommand
	defer func() { runFailedUnitCommand = old }()
	s := surfaceTestServer(t, roleAdministrator)
	for _, output := range [][]byte{[]byte("not json"), []byte("null"), []byte(strings.Repeat("x", failedUnitOutputLimit+1))} {
		runFailedUnitCommand = func(context.Context, string, ...string) ([]byte, error) { return output, nil }
		rr := failedUnitTestRequest(s, "/v1/admin/systemd/failed")
		if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), "unavailable") {
			t.Fatalf("invalid list: %d %s", rr.Code, rr.Body.String())
		}
	}
	runFailedUnitCommand = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("secret privileged command details")
	}
	rr := failedUnitTestRequest(s, "/v1/admin/systemd/failed")
	if rr.Code != http.StatusServiceUnavailable || strings.Contains(rr.Body.String(), "secret") {
		t.Fatal(rr.Body.String())
	}
	for _, oversized := range []bool{false, true} {
		runFailedUnitCommand = func(_ context.Context, name string, _ ...string) ([]byte, error) {
			if name == "systemctl" {
				return failedUnitFixture("minecraft.service"), nil
			}
			if oversized {
				return []byte(strings.Repeat("x", failedUnitOutputLimit+1)), nil
			}
			return nil, errors.New("secret journal failure")
		}
		rr := failedUnitTestRequest(s, "/v1/admin/systemd/failed/minecraft.service/journal")
		if rr.Code != http.StatusServiceUnavailable || strings.Contains(rr.Body.String(), "secret") {
			t.Fatalf("journal failure: %d %s", rr.Code, rr.Body.String())
		}
	}
	var buffer failedUnitOutput
	chunk := []byte(strings.Repeat("x", 8192))
	for i := 0; i < 100; i++ {
		n, err := buffer.Write(chunk)
		if err != nil || n != len(chunk) {
			t.Fatal("writer did not drain")
		}
	}
	if !buffer.overflow || buffer.Len() != failedUnitOutputLimit {
		t.Fatal("unbounded output buffer")
	}
	source, err := os.ReadFile("failed_units.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "exec.CommandContext(ctx, name, args...)") || strings.Contains(string(source), `"sh"`) || strings.Contains(string(source), `"bash"`) {
		t.Fatal("command execution must use argument arrays without a shell")
	}
}
