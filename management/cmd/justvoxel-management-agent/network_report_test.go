package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestNetworkReportFixedBoundedPrivateAndRedacted(t *testing.T) {
	remoteFixture(t)
	old := runSupportCommand
	defer func() { runSupportCommand = old }()
	calls := []string{}
	runSupportCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if _, bounded := ctx.Deadline(); !bounded {
			t.Fatal("unbounded section")
		}
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "resolvectl" {
			return nil, errors.New("private-error")
		}
		return []byte("safe diagnostic evidence\npassword=wifi-private\nAuthorization: Bearer api-private\nnordvpn://login?exchange_token=callback-private\nhttps://example.test/private\nPublic Key: public-private\n"), nil
	}
	store := openTestOperationStore(t)
	entry, err := store.generateNetworkReport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if entry.Category != "network" || !validDiagnosticCategory(entry.Category) {
		t.Fatal(entry)
	}
	data, err := store.readDiagnosticLog("network", entry.LogID)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"wifi-private", "api-private", "callback-private", "public-private", "example.test", "private-error"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("report leaked private detail")
		}
	}
	for _, want := range []string{"NETWORK REPORT METADATA", "NetworkManager.service STATE", "nordvpnd.socket STATE", "nordvpnd.service CURRENT-BOOT JOURNAL", "Tailscale SUMMARY", "NetBird SUMMARY", "Playit.gg SUMMARY", "NordVPN Meshnet SUMMARY", "<section unavailable>"} {
		if !strings.Contains(string(data), want) {
			t.Fatal("missing section", want)
		}
	}
	if len(data) > supportReportLimit {
		t.Fatal("oversized report")
	}
	joined := strings.Join(calls, "\n")
	for _, want := range []string{"journalctl -b -u NetworkManager.service --no-pager -n 100 -o cat", "ip -4 route show", "ip -6 route show", "resolvectl status --no-pager", "nmcli --terse --fields DEVICE,TYPE,STATE device status"} {
		if !strings.Contains(joined, want) {
			t.Fatal("fixed diagnostic missing", want)
		}
	}
	if strings.Contains(joined, "diagnostics") || strings.Contains(joined, "--show-secrets") || strings.Contains(joined, "environment") {
		t.Fatal("unsafe collector")
	}
	path := filepath.Join(store.logsDir, "network-logs", entry.LogID+".log")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		t.Fatal("report is not private")
	}
	entries, err := store.listDiagnosticLogs()
	if err != nil || len(entries) != 1 || entries[0].Category != "network" {
		t.Fatal(entries, err)
	}
	if _, err = store.readDiagnosticLog("network", "../private"); err == nil {
		t.Fatal("traversal accepted")
	}
	if _, err = store.storeDiagnosticReport("network", entry.LogID, nil); err == nil {
		t.Fatal("overwrote report")
	}
	if _, err = store.storeDiagnosticReport("network", "../../private", nil); err == nil {
		t.Fatal("path accepted")
	}
}

func TestNetworkReportAdministratorAndNoInput(t *testing.T) {
	remoteFixture(t)
	old := runSupportCommand
	defer func() { runSupportCommand = old }()
	count := 0
	runSupportCommand = func(context.Context, string, ...string) ([]byte, error) {
		count++
		return nil, errors.New("unavailable")
	}
	for _, role := range []principalRole{roleViewer, roleOperator, roleAdministrator} {
		s := surfaceTestServer(t, role)
		s.operations = openTestOperationStore(t)
		mux := http.NewServeMux()
		registerAdminDiagnosticLogRoutes(mux, s)
		for _, input := range []struct {
			path, body string
			adminWant  int
		}{
			{"/v1/admin/logs/network", "", 200},
			{"/v1/admin/logs/network?unit=other.service", "", 400},
			{"/v1/admin/logs/network?path=/etc/shadow", "", 400},
			{"/v1/admin/logs/network", `{"command":"id"}`, 400},
		} {
			before := count
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, surfaceRequest(http.MethodPost, input.path, input.body))
			want := 403
			if role == roleAdministrator {
				want = input.adminWant
			}
			if rr.Code != want {
				t.Fatal(role, rr.Code, rr.Body.String())
			}
			if want != 200 && count != before {
				t.Fatal("rejected input reached commands")
			}
		}
	}
}
