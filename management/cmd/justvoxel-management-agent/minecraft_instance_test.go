package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func testMinecraftInstances(t *testing.T) *minecraftInstanceStore {
	t.Helper()
	return &minecraftInstanceStore{directory: filepath.Join(t.TempDir(), "instances"), random: rand.Reader, uid: os.Getuid(), gid: os.Getgid()}
}

func TestMinecraftInstanceGenerationAndPersistence(t *testing.T) {
	store := testMinecraftInstances(t)
	missing, err := store.read()
	if err != nil || missing.Status != "unavailable" {
		t.Fatalf("read=%+v err=%v", missing, err)
	}
	if _, err := os.Stat(store.directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read created state")
	}
	first, err := store.register("", false)
	if err != nil {
		t.Fatal(err)
	}
	if !instanceIDPattern.MatchString(first.ID) || len(first.ID) != 15 {
		t.Fatalf("invalid automatic ID: %q", first.ID)
	}
	// A new store represents a restarted Agent, with generation unavailable.
	restarted := *store
	restarted.random = bytes.NewReader(nil)
	second, err := restarted.register("", false)
	if err != nil || first != second {
		t.Fatalf("identity changed: %+v err=%v", second, err)
	}
	for path, mode := range map[string]os.FileMode{store.directory: 0700, filepath.Join(store.directory, "minecraft.json"): 0600} {
		info, err := os.Stat(path)
		if err != nil || !store.private(info, mode) {
			t.Fatalf("invalid permissions for %s: %v", path, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(store.directory, "minecraft.json"))
	if err != nil || string(data) != `{"id":"`+first.ID+`"}`+"\n" {
		t.Fatalf("record contains more than identity: %s err=%v", data, err)
	}
}

func TestMinecraftInstanceConcurrentRegistration(t *testing.T) {
	store := testMinecraftInstances(t)
	var workers sync.WaitGroup
	results := make(chan minecraftInstanceIdentity, 24)
	failures := make(chan error, 24)
	for i := 0; i < 24; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			independent := *store
			id, err := independent.register("", false)
			if err != nil {
				failures <- err
			} else {
				results <- id
			}
		}()
	}
	workers.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	first := ""
	for result := range results {
		if first == "" {
			first = result.ID
		}
		if result.ID != first {
			t.Fatal("concurrent calls returned different IDs")
		}
	}
	entries, err := os.ReadDir(store.directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != "minecraft.json" {
		t.Fatalf("records=%v err=%v", entries, err)
	}
}

func TestMinecraftInstanceSurvivesProcessRestart(t *testing.T) {
	if directory := os.Getenv("JV_TEST_INSTANCE_DIRECTORY"); directory != "" {
		store := &minecraftInstanceStore{directory: directory, random: bytes.NewReader(nil), uid: os.Getuid(), gid: os.Getgid()}
		id, err := store.register("", false)
		if err != nil || id.ID != os.Getenv("JV_TEST_INSTANCE_ID") {
			t.Fatalf("restart identity=%+v err=%v", id, err)
		}
		return
	}
	store := testMinecraftInstances(t)
	id, err := store.register("", false)
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestMinecraftInstanceSurvivesProcessRestart$")
	child.Env = append(os.Environ(), "JV_TEST_INSTANCE_DIRECTORY="+store.directory, "JV_TEST_INSTANCE_ID="+id.ID)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("restart: %v %s", err, output)
	}
}

func TestMinecraftInstanceManualValidationAndImmutability(t *testing.T) {
	for _, suffix := range []string{"ABC123", "A1b2C3d4E5f6", "abcdefg", "12345678901"} {
		t.Run(suffix, func(t *testing.T) {
			store := testMinecraftInstances(t)
			id, err := store.register(suffix, true)
			if err != nil || id.ID != "jv-"+strings.ToLower(suffix) {
				t.Fatalf("id=%+v err=%v", id, err)
			}
			if _, err := store.register("other1", true); !errors.Is(err, errInstanceRegistered) {
				t.Fatalf("overwrite accepted: %v", err)
			}
			current, err := store.read()
			if err != nil || current != id {
				t.Fatal("manual save replaced identity")
			}
		})
	}
	for _, suffix := range []string{"", "abc12", "abcdefghijklm", "abc-12", "jv-abc123", "abc 12", "ébc123", "abc123\n", "../abc123"} {
		store := testMinecraftInstances(t)
		if _, err := store.register(suffix, true); !errors.Is(err, errInstanceSuffix) {
			t.Fatalf("accepted %q: %v", suffix, err)
		}
		if _, err := os.Stat(store.directory); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("invalid input wrote state")
		}
	}
}

func TestMinecraftInstanceCollisionAndGenerationFailure(t *testing.T) {
	store := testMinecraftInstances(t)
	if err := os.Mkdir(store.directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.directory, "registered.json"), []byte(`{"id":"jv-abc123"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.register("ABC123", true); !errors.Is(err, errInstanceDuplicate) {
		t.Fatalf("collision accepted: %v", err)
	}
	// Force an automatic collision. It must fail once, without retrying generation.
	store.random = bytes.NewReader(make([]byte, 16))
	if err := os.WriteFile(filepath.Join(store.directory, "registered.json"), []byte(`{"id":"jv-000000000000"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.register("", false); !errors.Is(err, errInstanceDuplicate) {
		t.Fatalf("automatic collision accepted: %v", err)
	}
	store.random = bytes.NewReader(nil)
	if _, err := store.register("", false); err == nil {
		t.Fatal("generation failure accepted")
	}
	if _, err := os.Stat(filepath.Join(store.directory, "minecraft.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed generation published record")
	}
	if _, err := store.register("MANUAL1", true); err != nil {
		t.Fatalf("manual fallback failed: %v", err)
	}
}

func TestMinecraftInstanceRejectsUnsafeRecords(t *testing.T) {
	for _, kind := range []string{"symlink", "permissions", "corrupt"} {
		t.Run(kind, func(t *testing.T) {
			store := testMinecraftInstances(t)
			id, err := store.register("abc123", true)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store.directory, "minecraft.json")
			switch kind {
			case "symlink":
				outside := filepath.Join(t.TempDir(), "outside.json")
				if err := os.Rename(path, outside); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			case "permissions":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(path, []byte(`{"id":"`+id.ID+`","runtime":"ignored"}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.register("", false); err == nil {
				t.Fatal("unsafe record accepted")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("unsafe record replaced")
			}
		})
	}
}

func TestMinecraftInstanceAPI(t *testing.T) {
	oldStore, oldDiscovery, oldRuntime := minecraftInstances, runAdminDiscoveryHelper, runWebHelper
	minecraftInstances = testMinecraftInstances(t)
	t.Cleanup(func() { minecraftInstances, runAdminDiscoveryHelper, runWebHelper = oldStore, oldDiscovery, oldRuntime })
	runWebHelper = func(context.Context, ...string) ([]byte, int, error) {
		t.Fatal("identity changed runtime")
		return nil, 0, nil
	}
	runAdminDiscoveryHelper = func(context.Context, string) ([]byte, error) { return []byte(`{"configured":true}`), nil }
	server := adminServerForTest()
	mux := http.NewServeMux()
	registerMinecraftRoutes(mux, server)
	read := httptest.NewRecorder()
	mux.ServeHTTP(read, authorizedRequest(http.MethodGet, "http://unix/v1/minecraft/identity", ""))
	if read.Code != 200 || !strings.Contains(read.Body.String(), `"status":"unavailable"`) {
		t.Fatalf("read: %d %s", read.Code, read.Body.String())
	}
	if _, err := os.Stat(minecraftInstances.directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read registered identity")
	}
	minecraftInstances.random = bytes.NewReader(nil)
	failed := httptest.NewRecorder()
	mux.ServeHTTP(failed, authorizedRequest(http.MethodPost, "http://unix/v1/minecraft/identity/ensure", "{}"))
	if failed.Code != 503 || !strings.Contains(failed.Body.String(), "registration failed") {
		t.Fatalf("missing fallback: %d %s", failed.Code, failed.Body.String())
	}
	saved := httptest.NewRecorder()
	mux.ServeHTTP(saved, authorizedRequest(http.MethodPost, "http://unix/v1/minecraft/identity/manual", `{"suffix":"ABC123"}`))
	if saved.Code != 200 || !strings.Contains(saved.Body.String(), "jv-abc123") {
		t.Fatalf("save: %d %s", saved.Code, saved.Body.String())
	}
	for _, request := range []struct {
		body   string
		status int
	}{
		{`{"suffix":"abc-12"}`, 400},
		{`{"suffix":"other1"}`, 409},
		{`{"suffix":"abc123","extra":"ignored"}`, 400},
	} {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, authorizedRequest(http.MethodPost, "http://unix/v1/minecraft/identity/manual", request.body))
		if rr.Code != request.status {
			t.Fatalf("manual status=%d body=%s", rr.Code, rr.Body.String())
		}
	}
	runAdminDiscoveryHelper = func(context.Context, string) ([]byte, error) {
		t.Fatal("existing identity depends on discovery")
		return nil, nil
	}
	again := httptest.NewRecorder()
	mux.ServeHTTP(again, authorizedRequest(http.MethodPost, "http://unix/v1/minecraft/identity/ensure", "{}"))
	if again.Code != 200 || again.Body.String() != saved.Body.String() {
		t.Fatal("ensure changed existing ID")
	}
}

func TestMinecraftInstanceAuthorizationAndUnconfiguredServer(t *testing.T) {
	oldStore, oldDiscovery := minecraftInstances, runAdminDiscoveryHelper
	minecraftInstances = testMinecraftInstances(t)
	t.Cleanup(func() { minecraftInstances, runAdminDiscoveryHelper = oldStore, oldDiscovery })
	runAdminDiscoveryHelper = func(context.Context, string) ([]byte, error) { return []byte(`{"configured":false}`), nil }
	unauthenticated := httptest.NewRecorder()
	adminServerForTest().minecraftInstanceRegister(unauthenticated, httptest.NewRequest(http.MethodPost, "/v1/minecraft/identity/ensure", strings.NewReader("{}")))
	if unauthenticated.Code != 401 {
		t.Fatalf("unauthenticated ensure=%d", unauthenticated.Code)
	}
	for _, role := range []principalRole{roleOperator, roleViewer} {
		s := roleServerForTest(role)
		for _, action := range []string{"ensure", "manual"} {
			rr := httptest.NewRecorder()
			s.minecraftInstanceRegister(rr, authorizedRequest(http.MethodPost, "http://unix/v1/minecraft/identity/"+action, `{"suffix":"abc123"}`))
			if rr.Code != 403 {
				t.Fatalf("%s %s: %d", role, action, rr.Code)
			}
		}
	}
	s := adminServerForTest()
	rr := httptest.NewRecorder()
	s.minecraftInstanceRead(rr, httptest.NewRequest(http.MethodGet, "/v1/minecraft/identity", nil))
	if rr.Code != 401 {
		t.Fatalf("unauthenticated read=%d", rr.Code)
	}
	rr = httptest.NewRecorder()
	s.minecraftInstanceRegister(rr, authorizedRequest(http.MethodPost, "/v1/minecraft/identity/ensure", "{}"))
	if rr.Code != 200 {
		t.Fatalf("pre-creation ensure=%d", rr.Code)
	}
	if identity, err := minecraftInstances.read(); err != nil || identity.Status != "available" {
		t.Fatal("pre-creation identity was not persisted")
	}
}

func TestMissingMinecraftIdentityDoesNotBlockRuntime(t *testing.T) {
	oldStore, oldRuntime := minecraftInstances, runWebHelper
	minecraftInstances = testMinecraftInstances(t)
	t.Cleanup(func() { minecraftInstances, runWebHelper = oldStore, oldRuntime })
	runWebHelper = func(_ context.Context, args ...string) ([]byte, int, error) {
		return []byte(`{"ok":true,"action":"start"}`), 0, nil
	}
	rr := httptest.NewRecorder()
	adminServerForTest().minecraftStart(rr, authorizedRequest(http.MethodPost, "/v1/minecraft/start", "{}"))
	if rr.Code != 200 {
		t.Fatalf("start without ID: %d %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(minecraftInstances.directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("runtime created identity")
	}
}

func useTestMinecraftInstances(t *testing.T) {
	t.Helper()
	old := minecraftInstances
	minecraftInstances = testMinecraftInstances(t)
	t.Cleanup(func() { minecraftInstances = old })
}

func TestImportDestinationIdentityWithoutArchiveIdentity(t *testing.T) {
	for _, mode := range []string{"fresh", "replace"} {
		t.Run(mode, func(t *testing.T) {
			useTestMinecraftInstances(t)
			expected := ""
			if mode == "replace" {
				identity, err := minecraftInstances.register("DEST123", true)
				if err != nil {
					t.Fatal(err)
				}
				expected = identity.ID
				minecraftInstances.random = bytes.NewReader(nil)
			}
			helper := step5A2ImportHelper(false) // Source contains no Instance ID.
			helper.Normalized.Destination.Mode = mode
			if mode == "fresh" {
				helper.Normalized.Destination.Storage = &adminSetupPlanStorage{}
				helper.Normalized.Destination.Backups = &adminSetupPlanBackups{}
				helper.Requirements.EULAAcceptanceRequired = true
			}
			installStep5A2ImportPlanHelper(t, helper)
			request := step5A2ImportRequest()
			plan, planningErr := authoritativeAdminMigrationImportPlan(context.Background(), request)
			if planningErr != nil || !plan.OK {
				t.Fatalf("plan=%+v error=%v", plan, planningErr)
			}
			body, err := json.Marshal(adminMigrationImportApplyRequest{PlanFingerprint: plan.PlanFingerprint, Request: request, ImportConfirmed: true, EULAAccepted: true})
			if err != nil {
				t.Fatal(err)
			}
			workers := 0
			oldWorker := startMigrationImportWorker
			t.Cleanup(func() { startMigrationImportWorker = oldWorker })
			startMigrationImportWorker = func(_ *server, _ string, _ migrationImportExecutionPlan) {
				workers++
				identity, err := minecraftInstances.read()
				if err != nil || identity.Status != "available" {
					t.Fatal("Import worker missing destination identity")
				}
				if mode == "replace" && identity.ID != expected {
					t.Fatal("Replace changed destination identity")
				}
			}
			s := surfaceTestServer(t, roleAdministrator)
			store := openTestOperationStore(t)
			attachTestOperationStore(t, s, store)
			if mode == "fresh" {
				minecraftInstances.random = bytes.NewReader(nil)
				rr := httptest.NewRecorder()
				s.adminMigrationImportApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/migration/import/apply", string(body)))
				if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "identity_required") || workers != 0 {
					t.Fatalf("Import proceeded without identity: %d %s", rr.Code, rr.Body.String())
				}
				if current, err := store.currentMigration(); err != nil || current != nil {
					t.Fatal("identity failure left a partial Import")
				}
				minecraftInstances.random = rand.Reader
			}
			rr := httptest.NewRecorder()
			s.adminMigrationImportApply(rr, surfaceRequest(http.MethodPost, "/v1/admin/migration/import/apply", string(body)))
			if rr.Code != http.StatusAccepted || workers != 1 {
				t.Fatalf("Import: %d %s", rr.Code, rr.Body.String())
			}
		})
	}
}
