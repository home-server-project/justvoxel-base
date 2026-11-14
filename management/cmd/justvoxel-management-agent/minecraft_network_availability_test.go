package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNetworkCompatibilityCapabilitiesAndCache(t *testing.T) {
	for _, tc := range []struct{ software, loader, reply, want string }{
		{"paper", "", "", "supported"}, {"purpur", "", "", "supported"},
		{"pufferfish", "", "", "supported"}, {"leaf", "", "", "supported"},
		{"fabric", "0.20.1", "supported", "supported"},
		{"quilt", "0.29.2", "supported", "supported"},
		{"forge", "66.1.0", "compatibility-unavailable", "compatibility-unavailable"},
		{"neoforge", "26.3.0.102", "supported", "supported"},
		{"forge", "66.1.0", "compatibility-conflict", "compatibility-unavailable"},
		{"forge", "66.1.0", "compatibility-check-unavailable", "compatibility-check-unavailable"},
		{"forge", "66.1.0", "outage", "compatibility-check-unavailable"},
		{"forge", "LATEST", "supported", "unsupported"},
		{"spigot", "", "", "unsupported"}, {"vanilla", "", "", "unsupported"}, {"bedrock", "", "", "unsupported"},
	} {
		t.Run(tc.software+"/"+tc.reply+"/"+tc.loader, func(t *testing.T) {
			record := instanceRuntimeRecord{Runtime: minecraftInstanceRuntimeConfig{ID: "jv-abc123", Software: tc.software, Version: "26.3", VersionPolicy: "pinned", LoaderVersion: tc.loader, ImageTag: "java25"}}
			checks, keys := 0, 0
			fingerprint := strings.Repeat("a", 64)
			b := &instanceRuntimeBackend{}
			b.command = func(_ context.Context, command string, args ...string) ([]byte, error) {
				if command != minecraftNetworkHelper || len(args) != 2 || args[1] != record.Runtime.ID {
					t.Fatal("unexpected capability command", command, args)
				}
				switch args[0] {
				case "backend-availability-key":
					keys++
					return []byte(fingerprint), nil
				case "backend-availability":
					checks++
					if tc.reply == "outage" {
						return nil, errors.New("https://private.invalid/provider /private/path secret")
					}
					return json.Marshal(map[string]string{"status": tc.reply})
				default:
					t.Fatal("status attempted mutation", args)
				}
				return nil, nil
			}
			first := b.networkCompatibility(context.Background(), record)
			second := b.networkCompatibility(context.Background(), record)
			if first.Status != tc.want || first != second {
				t.Fatal(first, second)
			}
			managed := tc.loader != "" && tc.loader != "LATEST"
			if !managed {
				if checks != 0 || keys != 0 {
					t.Fatal("static capability contacted provider")
				}
				return
			}
			if checks != 1 || keys != 2 {
				t.Fatal("cache did not bind current inventory", checks, keys)
			}
			for _, bad := range []string{"https:", "private", "secret"} {
				if strings.Contains(first.Reason, bad) {
					t.Fatal("unsafe public reason", first)
				}
			}
			if tc.reply == "compatibility-unavailable" && first.Reason != "Network compatibility is not currently available for Minecraft 26.3." {
				t.Fatal(first)
			}
			b.networkCompatibilityCache.mu.Lock()
			for _, entry := range b.networkCompatibilityCache.entries {
				remaining := time.Until(entry.expires)
				if tc.want == "compatibility-check-unavailable" && remaining > 20*time.Second {
					t.Fatal("failure cached too long")
				}
				entry.expires = time.Now().Add(-time.Second)
			}
			b.networkCompatibilityCache.mu.Unlock()
			b.networkCompatibility(context.Background(), record)
			if checks != 2 {
				t.Fatal("expired result reused")
			}
			fingerprint = strings.Repeat("b", 64)
			b.networkCompatibility(context.Background(), record)
			if checks != 3 {
				t.Fatal("changed mod inventory inherited capability")
			}
			record.Runtime.LoaderVersion = "0.20.2"
			if tc.software == "forge" {
				record.Runtime.LoaderVersion = "66.1.1"
			}
			if tc.software == "neoforge" {
				record.Runtime.LoaderVersion = "26.3.0.103"
			}
			b.networkCompatibility(context.Background(), record)
			if checks != 4 {
				t.Fatal("changed loader inherited capability")
			}
			record.Runtime.ID = "jv-def456"
			b.networkCompatibility(context.Background(), record)
			if checks != 5 {
				t.Fatal("different instance inherited capability")
			}
		})
	}
}

func TestNetworkRuntimePollingOmitsCompatibilityChecks(t *testing.T) {
	f := newInstanceActivationFixture(t)
	for _, c := range f.configs {
		record, err := f.backend.readRecord(c.ID)
		if err != nil {
			t.Fatal(err)
		}
		record.Runtime.Software, record.Runtime.Version, record.Runtime.LoaderVersion = "fabric", "26.3", "0.20.1"
		record.Runtime.VersionPolicy, record.Runtime.ImageTag = "pinned", "java25"
		if err := f.backend.writeJSON(filepath.Join(f.backend.root, c.ID, "config.json"), record, true); err != nil {
			t.Fatal(err)
		}
	}
	old := instanceRuntimes
	instanceRuntimes = f.backend
	t.Cleanup(func() { instanceRuntimes = old })
	original := f.backend.command
	f.backend.command = func(ctx context.Context, cmd string, args ...string) ([]byte, error) {
		if cmd == minecraftNetworkHelper {
			t.Fatal("runtime poll resolved compatibility", args)
		}
		return original(ctx, cmd, args...)
	}
	rr := httptest.NewRecorder()
	roleServerForTest(roleAdministrator).minecraftNetworkStatus(rr, authorizedRequest("GET", "/v1/minecraft/network?runtime_only=true", ""))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"state":`) || strings.Contains(rr.Body.String(), `"eligible":`) {
		t.Fatal(rr.Code, rr.Body.String())
	}
	for _, id := range []string{f.configs[0].ID, f.configs[1].ID} {
		for _, name := range []string{"network-staging", "network-before.json"} {
			if _, err := os.Lstat(filepath.Join(f.backend.root, id, name)); !os.IsNotExist(err) {
				t.Fatal("status created evidence", name, err)
			}
		}
	}
}

func TestNetworkStatusPublishesDynamicAvailability(t *testing.T) {
	for _, reply := range []string{"supported", "compatibility-unavailable", "compatibility-check-unavailable", "compatibility-conflict"} {
		t.Run(reply, func(t *testing.T) {
			f := newInstanceActivationFixture(t)
			record, err := f.backend.readRecord(f.configs[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			record.Runtime.Software, record.Runtime.Version, record.Runtime.LoaderVersion = "forge", "26.3", "66.1.0"
			record.Runtime.VersionPolicy, record.Runtime.ImageTag = "pinned", "java25"
			record.Runtime.BedrockEnabled = false
			if err := f.backend.writeJSON(filepath.Join(f.backend.root, record.Runtime.ID, "config.json"), record, true); err != nil {
				t.Fatal(err)
			}
			original := f.backend.command
			f.backend.command = func(ctx context.Context, cmd string, args ...string) ([]byte, error) {
				if cmd == minecraftNetworkHelper {
					switch args[0] {
					case "backend-availability-key":
						return []byte(strings.Repeat("a", 64)), nil
					case "backend-availability":
						return json.Marshal(map[string]string{"status": reply})
					default:
						t.Fatal("status attempted a mutation", args)
					}
				}
				return original(ctx, cmd, args...)
			}
			old := instanceRuntimes
			instanceRuntimes = f.backend
			defer func() { instanceRuntimes = old }()
			rr := httptest.NewRecorder()
			roleServerForTest(roleAdministrator).minecraftNetworkStatus(rr, authorizedRequest("GET", "/v1/minecraft/network", ""))
			var status struct {
				Servers []struct {
					ID       string `json:"id"`
					Eligible bool   `json:"eligible"`
					Status   string `json:"compatibility_status"`
					Reason   string `json:"compatibility_reason"`
				} `json:"servers"`
			}
			if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &status) != nil {
				t.Fatal(rr.Code, rr.Body.String())
			}
			found := false
			for _, server := range status.Servers {
				if server.ID == record.Runtime.ID {
					found = true
					want := networkCompatibilityPublic(reply, "26.3")
					if server.Eligible != (reply == "supported") || server.Status != want.Status || server.Reason != want.Reason {
						t.Fatal(server)
					}
				}
			}
			if !found {
				t.Fatal("managed backend omitted")
			}
		})
	}
}
