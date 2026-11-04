package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestValidBackupDeleteID(t *testing.T) {
	const prefix = "jv-bc3cc1979a86/"
	const suffix = "-backup-paper-2026-11-02-2105.tar.gz"
	// Keep the friendly name within 64 characters while exercising the byte limit.
	name := strings.Repeat("界", 64)
	byteLimitArchive := name + "-backup-" + strings.Repeat("a", 245-len(name)-len("-backup--2026-11-02-2105.tar.gz")) + "-2026-11-02-2105.tar.gz"
	cases := []struct {
		name string
		id   string
		want bool
	}{
		{"real Forge", "jv-d16a0bacf9d0/Forge-backup-forge-2026-11-02-2105.tar.gz", true},
		{"real Spigot", prefix + "Spigot-backup-spigot-2026-11-02-1100.tar.gz", true},
		{"real Vanilla", prefix + "Vanilla-backup-vanilla-2026-11-02-1030.tar.gz", true},
		{"real Paper", prefix + "Paper1-backup-paper-2026-11-02-0931.tar.gz", true},
		{"old development instance", prefix + "instance-backup-paper-2026-10-29-0831.tar.gz", false},
		{"legacy root seconds", "minecraft-2026-11-02-210501.tar.gz", false},
		{"legacy root software", "minecraft-paper-2026-11-02-2105-2.tar.gz", false},
		{"legacy instance seconds", prefix + "minecraft-2026-11-02-210501.tar.gz", false},
		{"legacy instance software", prefix + "minecraft-purpur-2026-11-02-2105.tar.gz", false},
		{"Unicode letters numbers marks and punctuation", prefix + "世界 Cafe\u0301 Ⅲ._-'(2)" + suffix, true},
		{"one character name", prefix + "A" + suffix, true},
		{"64 character name", prefix + strings.Repeat("A", 64) + suffix, true},
		{"245 byte basename", prefix + byteLimitArchive, true},
		{"collision one", prefix + "Paper-backup-paper-2026-11-02-2105-1.tar.gz", true},
		{"collision 999", prefix + "Paper-backup-paper-2026-11-02-2105-999.tar.gz", true},
		{"minimum instance ID", "jv-a1b2c3/A" + suffix, true},
		{"71 character software", prefix + "A-backup-" + strings.Repeat("a", 71) + "-2026-11-02-2105.tar.gz", true},
		{"traversal", "../minecraft-2026-11-02-210501.tar.gz", false},
		{"instance traversal", prefix + "../Paper" + suffix, false},
		{"nested directory", prefix + "nested/Paper" + suffix, false},
		{"short instance ID", "jv-id/Paper" + suffix, false},
		{"long instance ID", "jv-1234567890123/Paper" + suffix, false},
		{"uppercase instance ID", "jv-ABC123/Paper" + suffix, false},
		{"empty basename", prefix, false},
		{"modern root", "Paper" + suffix, false},
		{"unsafe character", prefix + "Paper!" + suffix, false},
		{"backslash", prefix + "Paper\\name" + suffix, false},
		{"leading space", prefix + " Paper" + suffix, false},
		{"trailing space", prefix + "Paper " + suffix, false},
		{"leading Unicode whitespace", prefix + "\u00a0Paper" + suffix, false},
		{"dot name", prefix + "." + suffix, false},
		{"dot dot name", prefix + ".." + suffix, false},
		{"empty friendly name", prefix + suffix, false},
		{"65 character name", prefix + strings.Repeat("A", 65) + suffix, false},
		{"uppercase software", prefix + "Paper-backup-Paper-2026-11-02-2105.tar.gz", false},
		{"numeric software start", prefix + "Paper-backup-1paper-2026-11-02-2105.tar.gz", false},
		{"unsafe software", prefix + "Paper-backup-paper_test-2026-11-02-2105.tar.gz", false},
		{"empty software", prefix + "Paper-backup--2026-11-02-2105.tar.gz", false},
		{"72 character software", prefix + "A-backup-" + strings.Repeat("a", 72) + "-2026-11-02-2105.tar.gz", false},
		{"collision zero", prefix + "Paper-backup-paper-2026-11-02-2105-0.tar.gz", false},
		{"collision four digits", prefix + "Paper-backup-paper-2026-11-02-2105-1000.tar.gz", false},
		{"collision leading zero", prefix + "Paper-backup-paper-2026-11-02-2105-01.tar.gz", false},
		{"246 byte basename", prefix + strings.Replace(byteLimitArchive, "-backup-", "-backup-a", 1), false},
		{"unrelated archive", prefix + "unrelated.tar.gz", false},
		{"invalid UTF-8", prefix + "\xff" + suffix, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validBackupDeleteID(tc.id); got != tc.want {
				t.Fatalf("validBackupDeleteID(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}

func TestAdminBackupDeletePlanValidatesBeforeTransport(t *testing.T) {
	const id = "jv-d16a0bacf9d0/Forge-backup-forge-2026-11-02-2105.tar.gz"
	calls := 0
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != adminBackupDeletePlanPath {
			t.Fatal(r.Method, r.URL.Path)
		}
		var request AdminBackupDeleteRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if len(request.BackupIDs) != 1 || request.BackupIDs[0] != id {
			t.Fatalf("unexpected backup selection: %v", request.BackupIDs)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
	})}}
	if _, err := client.AdminBackupDeletePlan(context.Background(), "token", AdminBackupDeleteRequest{BackupIDs: []string{id}}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("valid modern ID made %d transport calls, want 1", calls)
	}
	if _, err := client.AdminBackupDeletePlan(context.Background(), "token", AdminBackupDeleteRequest{BackupIDs: []string{"../" + id}}); err == nil || err.Error() != "invalid backup id" {
		t.Fatalf("invalid ID returned %v, want invalid backup id", err)
	}
	if calls != 1 {
		t.Fatalf("invalid ID reached transport: %d calls", calls)
	}
}
