package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func registerLogicalForTest(t *testing.T, store *minecraftInstanceStore, suffix, software, name string) minecraftRegisteredInstance {
	t.Helper()
	instance, err := store.registerLogicalInstance(suffix, true, software, name, "")
	if err != nil {
		t.Fatal(err)
	}
	return instance
}

func registrySnapshot(t *testing.T, store *minecraftInstanceStore) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.Walk(store.directory, func(path string, info os.FileInfo, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		value := fmt.Sprintf("%v:%v", info.Mode(), info.ModTime())
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += ":" + string(data)
		}
		result[path] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestInstanceRegistryLegacyCompatibilityAndReadPurity(t *testing.T) {
	store := testMinecraftInstances(t)
	if instances, err := store.listInstances("paper"); err != nil || len(instances) != 0 {
		t.Fatalf("missing list=%v err=%v", instances, err)
	}
	if _, err := os.Stat(store.directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("listing created directory")
	}
	identity, err := store.register("legacy1", true)
	if err != nil {
		t.Fatal(err)
	}
	before := registrySnapshot(t, store)
	for _, software := range []string{"paper", "purpur", "vanilla", ""} {
		instances, err := store.listInstances(software)
		if err != nil || len(instances) != 1 || instances[0].ID != identity.ID || instances[0].DisplayName != instanceDefaultName(software) {
			t.Fatalf("legacy list=%v err=%v", instances, err)
		}
	}
	if !reflect.DeepEqual(before, registrySnapshot(t, store)) {
		t.Fatal("read mutated legacy state")
	}
	// Name records cannot enter the legacy top-level collision scan, even when
	// their retained ID is no longer the current single-server identity.
	if _, err := store.renameInstance(identity.ID, "Family Survival", "paper"); err != nil {
		t.Fatal(err)
	}
	if err := store.retire(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.register("legacy2", true); err != nil {
		t.Fatalf("metadata broke legacy registration: %v", err)
	}
	if instances, err := store.listInstances("paper"); err != nil || len(instances) != 1 || instances[0].ID != "jv-legacy2" {
		t.Fatalf("retired name became active: %v %v", instances, err)
	}
}

func TestInstanceRegistryDistinctIDsNamesAndRename(t *testing.T) {
	store := testMinecraftInstances(t)
	first := registerLogicalForTest(t, store, "server1", "paper", "")
	second := registerLogicalForTest(t, store, "server2", "paper", "  ")
	third := registerLogicalForTest(t, store, "server3", "purpur", "")
	if first.ID == second.ID || first.DisplayName != "JV Paper" || second.DisplayName != "JV Paper 2" || third.DisplayName != "JV Purpur" {
		t.Fatalf("defaults: %v %v %v", first, second, third)
	}
	if _, err := store.registerLogicalInstance("SERVER1", true, "paper", "Other", ""); !errors.Is(err, errInstanceDuplicate) {
		t.Fatalf("duplicate ID: %v", err)
	}
	if _, err := store.registerLogicalInstance("server4", true, "paper", "jv paper", ""); !errors.Is(err, errInstanceNameDuplicate) {
		t.Fatalf("duplicate name: %v", err)
	}
	if _, err := store.renameInstance(second.ID, "jV pApEr", ""); !errors.Is(err, errInstanceNameDuplicate) {
		t.Fatalf("rename duplicate: %v", err)
	}
	path := filepath.Join(store.directory, first.ID+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := store.renameInstance(first.ID, "  Family Survival  ", "")
	if err != nil || renamed.ID != first.ID || renamed.DisplayName != "Family Survival" {
		t.Fatalf("rename=%v err=%v", renamed, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("rename changed identity")
	}
	if reset, err := store.renameInstance(first.ID, "", ""); err != nil || reset.DisplayName != "JV Paper" {
		t.Fatalf("empty rename=%v err=%v", reset, err)
	}
	if _, err := store.read(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store.directory, "minecraft.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("logical registration created runtime identity")
	}
	// Automatic registration uses the same ID convention and fails on collision.
	store.random = bytes.NewReader(make([]byte, 32))
	automatic, err := store.registerLogicalInstance("", false, "vanilla", "", "")
	if err != nil || automatic.ID != "jv-000000000000" || automatic.DisplayName != "JV Vanilla" {
		t.Fatalf("automatic=%v err=%v", automatic, err)
	}
	if _, err := store.registerLogicalInstance("", false, "vanilla", "", ""); !errors.Is(err, errInstanceDuplicate) {
		t.Fatalf("automatic collision=%v", err)
	}
}

func TestInstanceRegistryRejectsLegacyIDAndReservesRetainedID(t *testing.T) {
	store := testMinecraftInstances(t)
	identity, err := store.register("legacy1", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.registerLogicalInstance("legacy1", true, "paper", "", ""); !errors.Is(err, errInstanceDuplicate) {
		t.Fatalf("legacy collision=%v", err)
	}
	if _, err := store.renameInstance(identity.ID, "Family Survival", "purpur"); err != nil {
		t.Fatal(err)
	}
	if err := store.retire(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.registerLogicalInstance("legacy1", true, "paper", "", ""); !errors.Is(err, errInstanceDuplicate) {
		t.Fatalf("retained ID reused=%v", err)
	}
	instances, err := store.listInstances("paper")
	if err != nil || len(instances) != 0 {
		t.Fatalf("retained record active=%v err=%v", instances, err)
	}
}

func TestInstanceRegistryConcurrentRegistrationAndRename(t *testing.T) {
	store := testMinecraftInstances(t)
	var workers sync.WaitGroup
	results := make(chan minecraftRegisteredInstance, 12)
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			independent := *store
			instance, err := independent.registerLogicalInstance(fmt.Sprintf("srv%03d", i), true, "paper", "", "")
			if err != nil {
				failures <- err
			} else {
				results <- instance
			}
		}(i)
	}
	workers.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	ids, names := map[string]bool{}, map[string]bool{}
	for result := range results {
		if ids[result.ID] || names[result.DisplayName] {
			t.Fatal("concurrent registration reused identity or name")
		}
		ids[result.ID], names[result.DisplayName] = true, true
	}
	if len(ids) != 12 {
		t.Fatalf("registered=%d", len(ids))
	}
	// Independent stores race for one suffix: exactly one succeeds.
	outcomes := make(chan error, 12)
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			independent := *store
			_, err := independent.registerLogicalInstance("shared1", true, "paper", "", "")
			outcomes <- err
		}()
	}
	workers.Wait()
	close(outcomes)
	successes := 0
	for err := range outcomes {
		if err == nil {
			successes++
		} else if !errors.Is(err, errInstanceDuplicate) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("duplicate race successes=%d", successes)
	}
	outcomes = make(chan error, 12)
	for id := range ids {
		workers.Add(1)
		go func(id string) {
			defer workers.Done()
			independent := *store
			_, err := independent.renameInstance(id, "Family Survival", "")
			outcomes <- err
		}(id)
	}
	workers.Wait()
	close(outcomes)
	successes = 0
	for err := range outcomes {
		if err == nil {
			successes++
		} else if !errors.Is(err, errInstanceNameDuplicate) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("rename race successes=%d", successes)
	}
}

func TestInstanceRegistryPersistenceAndReadPurity(t *testing.T) {
	store := testMinecraftInstances(t)
	instance := registerLogicalForTest(t, store, "server1", "paper", "Family Survival")
	restarted := *store
	restarted.random = bytes.NewReader(nil)
	before := registrySnapshot(t, store)
	instances, err := restarted.listInstances("")
	if err != nil || len(instances) != 1 || instances[0] != instance {
		t.Fatalf("restart=%v err=%v", instances, err)
	}
	if !reflect.DeepEqual(before, registrySnapshot(t, store)) {
		t.Fatal("registry read changed state")
	}
	for path, mode := range map[string]os.FileMode{filepath.Join(store.directory, "names"): 0700, filepath.Join(store.directory, "names", instance.ID+".json"): 0600, filepath.Join(store.directory, instance.ID+".json"): 0600} {
		info, err := os.Stat(path)
		if err != nil || !store.private(info, mode) {
			t.Fatalf("permissions %s: %v", path, err)
		}
	}
}

func TestInstanceRegistryProcessRestart(t *testing.T) {
	if directory := os.Getenv("JV_TEST_REGISTRY_DIRECTORY"); directory != "" {
		store := &minecraftInstanceStore{directory: directory, random: bytes.NewReader(nil), uid: os.Getuid(), gid: os.Getgid()}
		instances, err := store.listInstances("")
		if err != nil || len(instances) != 1 || instances[0].ID != "jv-server1" || instances[0].DisplayName != "Family Survival" {
			t.Fatalf("process restart=%v err=%v", instances, err)
		}
		return
	}
	store := testMinecraftInstances(t)
	registerLogicalForTest(t, store, "server1", "paper", "Family Survival")
	child := exec.Command(os.Args[0], "-test.run=^TestInstanceRegistryProcessRestart$")
	child.Env = append(os.Environ(), "JV_TEST_REGISTRY_DIRECTORY="+store.directory)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("restart=%v %s", err, output)
	}
}

func TestInstanceRegistryUnsafeNamesDoNotWrite(t *testing.T) {
	for _, name := range []string{"../server", "a/b", `a\b`, "<script>", "Name\n", "\tName", "Name\x00", "Name\u202e", "Name\u200b", strings.Repeat("x", 65), string([]byte{0xff}), ".", ".."} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			store := testMinecraftInstances(t)
			if _, err := store.registerLogicalInstance("server1", true, "paper", name, ""); !errors.Is(err, errInstanceName) {
				t.Fatalf("accepted %q: %v", name, err)
			}
			if _, err := os.Stat(store.directory); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid name created state")
			}
			instance := registerLogicalForTest(t, store, "server1", "paper", "")
			before := registrySnapshot(t, store)
			if _, err := store.renameInstance(instance.ID, name, ""); !errors.Is(err, errInstanceName) {
				t.Fatalf("rename accepted %q: %v", name, err)
			}
			if !reflect.DeepEqual(before, registrySnapshot(t, store)) {
				t.Fatal("invalid rename wrote state")
			}
		})
	}
	if name, err := validateInstanceName("  家族のサーバー  "); err != nil || name != "家族のサーバー" {
		t.Fatalf("friendly unicode=%q err=%v", name, err)
	}
	store := testMinecraftInstances(t)
	if _, err := store.renameInstance("../server1", "Name", ""); !errors.Is(err, errInstanceID) {
		t.Fatalf("unsafe ID: %v", err)
	}
	if _, err := os.Stat(store.directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid ID accessed state")
	}
}

func TestInstanceRegistryRejectsUnsafeState(t *testing.T) {
	for _, kind := range []string{"corrupt-name", "unknown-field", "mismatched-id", "invalid-name", "invalid-software", "name-permissions", "name-symlink", "name-directory", "directory-permissions", "directory-symlink", "root-permissions", "root-symlink", "identity-symlink", "identity-permissions", "identity-corrupt", "duplicate-id", "duplicate-name"} {
		t.Run(kind, func(t *testing.T) {
			store := testMinecraftInstances(t)
			identity, err := store.register("legacy1", true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.renameInstance(identity.ID, "Family Survival", "paper"); err != nil {
				t.Fatal(err)
			}
			instance := registerLogicalForTest(t, store, "server1", "paper", "")
			path := filepath.Join(store.directory, "names", instance.ID+".json")
			write := func(data string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			chmod := func(path string, mode os.FileMode) {
				t.Helper()
				if err := os.Chmod(path, mode); err != nil {
					t.Fatal(err)
				}
			}
			symlink := func(path string) {
				t.Helper()
				outside := filepath.Join(t.TempDir(), "outside")
				if err := os.Rename(path, outside); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "corrupt-name":
				write(`{"id":`)
			case "unknown-field":
				write(`{"id":"jv-server1","display_name":"JV Paper","software":"paper","extra":true}`)
			case "mismatched-id":
				write(`{"id":"jv-other1","display_name":"JV Paper","software":"paper"}`)
			case "invalid-name":
				write(`{"id":"jv-server1","display_name":"../server","software":"paper"}`)
			case "invalid-software":
				write(`{"id":"jv-server1","display_name":"JV Paper","software":"unknown"}`)
			case "name-permissions":
				chmod(path, 0644)
			case "name-symlink":
				symlink(path)
			case "name-directory":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0600); err != nil {
					t.Fatal(err)
				}
			case "directory-permissions":
				chmod(filepath.Dir(path), 0755)
			case "directory-symlink":
				symlink(filepath.Dir(path))
			case "root-permissions":
				chmod(store.directory, 0755)
			case "root-symlink":
				symlink(store.directory)
			case "identity-symlink":
				symlink(filepath.Join(store.directory, instance.ID+".json"))
			case "identity-permissions":
				chmod(filepath.Join(store.directory, instance.ID+".json"), 0644)
			case "identity-corrupt":
				path = filepath.Join(store.directory, instance.ID+".json")
				write(`{"id":"invalid"}`)
			case "duplicate-id":
				path = filepath.Join(store.directory, "duplicate.json")
				write(`{"id":"jv-server1"}`)
			case "duplicate-name":
				write(`{"id":"jv-server1","display_name":"family survival","software":"paper"}`)
			}
			if _, err := store.listInstances("paper"); err == nil {
				t.Fatal("unsafe registry listed")
			}
			if _, err := store.renameInstance(identity.ID, "Other", "paper"); err == nil {
				t.Fatal("unsafe registry renamed")
			}
			if _, err := store.registerLogicalInstance("server2", true, "paper", "", ""); err == nil {
				t.Fatal("unsafe registry registered")
			}
			if kind != "root-permissions" && kind != "root-symlink" {
				current, err := store.read()
				if err != nil || current != identity {
					t.Fatalf("optional registry blocked legacy identity: %v %v", current, err)
				}
			}
		})
	}
}

func TestInstanceRegistryAPI(t *testing.T) {
	useTestMinecraftInstances(t)
	oldDiscovery := runAdminDiscoveryHelper
	t.Cleanup(func() { runAdminDiscoveryHelper = oldDiscovery })
	runAdminDiscoveryHelper = func(context.Context, string) ([]byte, error) {
		return []byte(`{"configured":true,"minecraft":{"server_type":"purpur"}}`), nil
	}
	identity, err := minecraftInstances.register("legacy1", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []principalRole{roleAdministrator, roleOperator, roleViewer} {
		mux := http.NewServeMux()
		registerMinecraftRoutes(mux, roleServerForTest(role))
		for _, path := range []string{"/v1/minecraft/instances", "/v1/minecraft/identity"} {
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, authorizedRequest(http.MethodGet, path, ""))
			if rr.Code != 200 || !strings.Contains(rr.Body.String(), identity.ID) {
				t.Fatalf("%s read=%d %s", role, rr.Code, rr.Body.String())
			}
			if path == "/v1/minecraft/instances" && !strings.Contains(rr.Body.String(), "JV Purpur") {
				t.Fatal("default did not derive from software")
			}
		}
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, authorizedRequest(http.MethodPost, "/v1/minecraft/instances/"+identity.ID+"/name", `{"display_name":"Family Survival"}`))
		want := 403
		if role == roleAdministrator {
			want = 200
		}
		if rr.Code != want {
			t.Fatalf("%s rename=%d %s", role, rr.Code, rr.Body.String())
		}
		if role == roleAdministrator {
			if _, err := minecraftInstances.renameInstance(identity.ID, "", "purpur"); err != nil {
				t.Fatal(err)
			}
		}
	}
	mux := http.NewServeMux()
	registerMinecraftRoutes(mux, adminServerForTest())
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		path := "/v1/minecraft/instances"
		if method == http.MethodPost {
			path += "/" + identity.ID + "/name"
		}
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest(method, path, strings.NewReader(`{"display_name":"Other"}`)))
		if rr.Code != 401 {
			t.Fatalf("unauthenticated=%d", rr.Code)
		}
	}
	registerLogicalForTest(t, minecraftInstances, "server1", "paper", "Family Survival")
	for _, test := range []struct {
		id, body string
		status   int
	}{
		{identity.ID, `{"display_name":"family survival"}`, 409},
		{identity.ID, `{"display_name":"<script>"}`, 400},
		{identity.ID, `{"display_name":"Other","extra":true}`, 400},
		{"invalid", `{"display_name":"Other"}`, 400},
		{"jv-absent1", `{"display_name":"Other"}`, 404},
		{identity.ID, `{}`, 200},
	} {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, authorizedRequest(http.MethodPost, "/v1/minecraft/instances/"+test.id+"/name", test.body))
		if rr.Code != test.status {
			t.Fatalf("rename %s %s=%d %s", test.id, test.body, rr.Code, rr.Body.String())
		}
	}
	// Existing endpoint payloads remain ID/status only and ensure does not rewrite.
	before := registrySnapshot(t, minecraftInstances)
	for _, endpoint := range []struct{ method, path, body string }{{"GET", "/v1/minecraft/identity", ""}, {"POST", "/v1/minecraft/identity/ensure", "{}"}} {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, authorizedRequest(endpoint.method, endpoint.path, endpoint.body))
		var result map[string]any
		if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &result) != nil || len(result) != 2 || result["id"] != identity.ID || result["status"] != "available" {
			t.Fatalf("identity contract=%d %s", rr.Code, rr.Body.String())
		}
	}
	if !reflect.DeepEqual(before, registrySnapshot(t, minecraftInstances)) {
		t.Fatal("identity endpoint rewrote state")
	}
}

func TestInstanceRegistryCorruptOptionalNamesDoNotBlockExistingIdentity(t *testing.T) {
	useTestMinecraftInstances(t)
	identity, err := minecraftInstances.register("legacy1", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := minecraftInstances.renameInstance(identity.ID, "Family Survival", "paper"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(minecraftInstances.directory, "names", identity.ID+".json"), []byte(`invalid`), 0600); err != nil {
		t.Fatal(err)
	}
	if current, err := minecraftInstances.register("", false); err != nil || current != identity {
		t.Fatalf("corrupt names blocked existing identity=%v %v", current, err)
	}
}

func TestInstanceRegistryDefaultsRespectUnnamedLegacyServer(t *testing.T) {
	store := testMinecraftInstances(t)
	legacy, err := store.register("legacy1", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.registerLogicalInstance("server1", true, "paper", "jv paper", "paper"); !errors.Is(err, errInstanceNameDuplicate) {
		t.Fatalf("explicit name displaced legacy default: %v", err)
	}
	second, err := store.registerLogicalInstance("server1", true, "paper", "", "paper")
	if err != nil || second.DisplayName != "JV Paper 2" {
		t.Fatalf("generated name=%v err=%v", second, err)
	}
	instances, err := store.listInstances("paper")
	if err != nil || len(instances) != 2 {
		t.Fatalf("list=%v err=%v", instances, err)
	}
	for _, instance := range instances {
		if instance.ID == legacy.ID && instance.DisplayName != "JV Paper" {
			t.Fatal("registration changed legacy default")
		}
	}
	if _, err := store.renameInstance(legacy.ID, "Family Survival", "paper"); err != nil {
		t.Fatal(err)
	}
	// Empty rename derives from the currently selected legacy software, even
	// after a previously stored name was generated for different software.
	renamed, err := store.renameInstance(legacy.ID, "", "purpur")
	if err != nil || renamed.DisplayName != "JV Purpur" || renamed.ID != legacy.ID {
		t.Fatalf("selected-software default=%v err=%v", renamed, err)
	}
	if current, err := store.read(); err != nil || current != legacy {
		t.Fatalf("rename replaced legacy identity=%v err=%v", current, err)
	}
}
