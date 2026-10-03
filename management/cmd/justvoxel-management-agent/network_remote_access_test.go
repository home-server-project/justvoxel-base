package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func remoteFixture(t *testing.T) *[]string {
	t.Helper()
	oldRun, oldStat := remoteSystemctl, remoteStat
	calls := []string{}
	remoteSystemctl = func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "show" {
			return "LoadState=loaded\nUnitFileState=enabled\nActiveState=active", nil
		}
		return "", nil
	}
	remoteStat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	t.Cleanup(func() { remoteSystemctl = oldRun; remoteStat = oldStat })
	return &calls
}

func TestRemoteAccessExactlyThreeProvidersAndUnconfiguredPlayitRunning(t *testing.T) {
	remoteFixture(t)
	s := roleServerForTest(roleOperator)
	mux := http.NewServeMux()
	registerNetworkRoutes(mux, s)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, authorizedRequest(http.MethodGet, "/v1/network/remote-access", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if len(remoteProviders) != 3 {
		t.Fatal("unexpected provider count")
	}
	for _, id := range []string{"tailscale", "netbird", "playit"} {
		if !strings.Contains(rr.Body.String(), `"id":"`+id+`"`) {
			t.Fatalf("missing %s", id)
		}
	}
	provider, _ := remoteProviderByID("playit")
	status, err := remoteProviderStatus(context.Background(), provider)
	if err != nil || !status.Installed || !status.Enabled || !status.Active || status.Configured || status.ServiceState == "failed" {
		t.Fatalf("fresh Playit status %+v, err %v", status, err)
	}
}

func TestRemoteProviderLifecycleUsesFixedUnitsAndAudits(t *testing.T) {
	calls := remoteFixture(t)
	store, _ := openTestWebUIStore(t)
	s := adminServerForTest()
	s.store = store
	for _, provider := range remoteProviders {
		for _, action := range []string{"activate", "deactivate"} {
			*calls = nil
			rr := networkAdminRequest(s, "/v1/admin/network/remote-access/"+provider.id, `{"action":"`+action+`"}`)
			if rr.Code != http.StatusOK {
				t.Fatalf("%s %s: %s", provider.id, action, rr.Body.String())
			}
			joined := strings.Join(*calls, "\n")
			if action == "activate" && !strings.Contains(joined, "enable --now "+provider.unit) {
				t.Fatal("activate did not enable and start fixed unit")
			}
			if action == "deactivate" && (!strings.Contains(joined, "stop "+provider.unit) || !strings.Contains(joined, "disable "+provider.unit)) {
				t.Fatal("deactivate did not stop and disable fixed unit")
			}
		}
	}
	remoteSystemctl = func(context.Context, ...string) (string, error) { return "", errors.New("systemctl failed") }
	if rr := networkAdminRequest(s, "/v1/admin/network/remote-access/playit", `{"action":"activate"}`); rr.Code != http.StatusServiceUnavailable {
		t.Fatal("failed lifecycle was reported successful")
	}
	events, err := store.listAuditEvents(20)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 7 || events[0].Success || events[0].Action != "network_remote_access_activate" {
		t.Fatalf("lifecycle audit events %+v", events)
	}
	for _, event := range events {
		if !strings.HasPrefix(event.Action, "network_remote_access_") {
			t.Fatalf("unexpected action %s", event.Action)
		}
	}
}

func TestRemoteProviderRejectsArbitraryNamesAndReadOnlyMutations(t *testing.T) {
	calls := remoteFixture(t)
	for _, id := range []string{"sshd.service", "unknown", "tailscaled.service"} {
		if rr := networkAdminRequest(adminServerForTest(), "/v1/admin/network/remote-access/"+id, `{"action":"activate"}`); rr.Code != http.StatusBadRequest {
			t.Fatalf("arbitrary provider %s accepted", id)
		}
	}
	for _, role := range []principalRole{roleOperator, roleViewer} {
		if rr := networkAdminRequest(roleServerForTest(role), "/v1/admin/network/remote-access/playit", `{"action":"activate"}`); rr.Code != http.StatusForbidden {
			t.Fatalf("read-only role %s accepted", role)
		}
	}
	if len(*calls) != 0 {
		t.Fatal("rejected lifecycle reached systemctl")
	}
}

func TestPlayitConfigurationEvidenceOnlyUsesExpectedFile(t *testing.T) {
	remoteFixture(t)
	path := filepath.Join(t.TempDir(), "evidence")
	if err := os.WriteFile(path, []byte("private-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	var checked []string
	remoteStat = func(name string) (os.FileInfo, error) { checked = append(checked, name); return os.Stat(path) }
	provider, _ := remoteProviderByID("playit")
	status, err := remoteProviderStatus(context.Background(), provider)
	if err != nil || !status.Configured {
		t.Fatalf("configured status %+v, %v", status, err)
	}
	if strings.Join(checked, ",") != "/etc/playit/playit.toml" {
		t.Fatalf("unexpected configuration paths %v", checked)
	}
	payload, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "private-secret") {
		t.Fatal("secret leaked")
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if remoteConfigured(provider) {
		t.Fatal("empty configuration classified as configured")
	}
}

func TestRemoteProviderRejectsTransitionalLifecycle(t *testing.T) {
	remoteFixture(t)
	for _, state := range []string{"activating", "deactivating", "reloading", "refreshing"} {
		var mutations int
		remoteSystemctl = func(_ context.Context, args ...string) (string, error) {
			if args[0] == "show" {
				return "LoadState=loaded\nUnitFileState=enabled\nActiveState=" + state, nil
			}
			mutations++
			return "", nil
		}
		for _, action := range []string{"activate", "deactivate"} {
			rr := networkAdminRequest(adminServerForTest(), "/v1/admin/network/remote-access/playit", `{"action":"`+action+`"}`)
			if rr.Code != http.StatusConflict {
				t.Fatalf("%s %s: %d", state, action, rr.Code)
			}
		}
		if mutations != 0 {
			t.Fatal("transitional lifecycle reached systemctl")
		}
	}
}
