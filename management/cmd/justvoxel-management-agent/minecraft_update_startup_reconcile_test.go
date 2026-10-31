package main

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func reopenedStartupUpdateFailure(t *testing.T, kind, instanceID, stage string) (*operationStore, operationJournal) {
	t.Helper()
	store := openTestOperationStore(t)
	journal, _, err := store.beginMinecraftMaintenance(testMinecraftResetFingerprint, kind, instanceID)
	if err != nil {
		t.Fatal(err)
	}
	journal, err = store.transition(journal.OperationID, operationNeedsAttention, stage, "Previous update failed; preserved for review.")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openOperationStore(store.baseDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.close() })
	current, err := reopened.currentMinecraftMaintenance()
	if err != nil || current == nil || !reflect.DeepEqual(*current, journal) {
		t.Fatalf("persisted maintenance not restored intact: %+v %v", current, err)
	}
	assertStartupUpdateLocks(t, reopened, true)
	return reopened, journal
}

func assertStartupUpdateLocks(t *testing.T, store *operationStore, held bool) {
	t.Helper()
	for _, lock := range []struct {
		file *os.File
		held bool
	}{
		{store.lockFile, store.setupLockHeld},
		{store.restoreLockFile, store.restoreLockHeld},
		{store.dataMigrationLockFile, store.dataMigrationLockHeld},
		{store.migrationLockFile, store.migrationLockHeld},
	} {
		if lock.held != held {
			t.Fatalf("shared exclusion ownership: got %v want %v", lock.held, held)
		}
		probe, err := os.OpenFile(lock.file.Name(), os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		err = syscall.Flock(int(probe.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		_ = probe.Close()
		if held && !errors.Is(err, syscall.EWOULDBLOCK) || !held && err != nil {
			t.Fatalf("shared exclusion flock: held=%v err=%v", held, err)
		}
	}
}

func startupJournalBytes(t *testing.T, store *operationStore, id string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(store.operationsDir, id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestMinecraftUpdateStartupPersistedFailure(t *testing.T) {
	for _, kind := range []string{operationTypeMinecraftContainerUpdate, operationTypeMinecraftUpdate} {
		for _, scenario := range failedUpdateSafetyScenarios {
			t.Run(kind+"/"+scenario, func(t *testing.T) {
				x := newFailedUpdateSafetyFixture(t, scenario)
				store, journal := reopenedStartupUpdateFailure(t, kind, x.id, "update_failed")
				before := startupJournalBytes(t, store, journal.OperationID)
				oldContainerRun, oldStackRun := runMinecraftContainerUpdate, runMinecraftStackUpdate
				t.Cleanup(func() { runMinecraftContainerUpdate, runMinecraftStackUpdate = oldContainerRun, oldStackRun })
				runMinecraftContainerUpdate = func(context.Context, minecraftContainerUpdateRequest) error {
					t.Fatal("startup retried container update")
					return nil
				}
				runMinecraftStackUpdate = func(context.Context, string, bool, string, string) error {
					t.Fatal("startup retried stack update")
					return nil
				}
				x.beforeCommand = func(ctx context.Context) {
					deadline, bounded := ctx.Deadline()
					if !bounded || time.Until(deadline) <= 0 || time.Until(deadline) > 90*time.Second {
						t.Fatal("startup proof needs bounded context")
					}
					assertStartupUpdateLocks(t, store, true)
					if !bytes.Equal(before, startupJournalBytes(t, store, journal.OperationID)) {
						t.Fatal("journal mutated before proof")
					}
				}
				err := reconcileMinecraftUpdateFailureAtStartup(store)
				safe := scenario == "healthy"
				if safe && err != nil || !safe && err == nil {
					t.Fatalf("reconciliation error: %v", err)
				}
				if scenario == "stack_failure" && !errors.Is(err, x.stackErr) {
					t.Fatal("startup lost proof failure", err)
				}
				assertFailedUpdateProofCount(t, x, scenario)
				finished, err := store.get(journal.OperationID)
				if err != nil {
					t.Fatal(err)
				}
				current, err := store.currentMinecraftMaintenance()
				if err != nil {
					t.Fatal(err)
				}
				assertStartupUpdateLocks(t, store, !safe)
				if !safe {
					if !reflect.DeepEqual(finished, journal) || current == nil || !reflect.DeepEqual(*current, journal) || !bytes.Equal(before, startupJournalBytes(t, store, journal.OperationID)) {
						t.Fatalf("unverified journal changed: %+v current=%+v", finished, current)
					}
				} else {
					if finished.State != operationResolved || finished.Stage != "resolved" || current != nil || store.currentMinecraftResetID != "" {
						t.Fatalf("verified runtime retained maintenance: %+v current=%+v", finished, current)
					}
					for _, text := range []string{"Previous update failed", "during startup reconciliation", "currently running committed runtime was independently verified", "No update was retried"} {
						if !strings.Contains(finished.Status, text) {
							t.Fatalf("resolution status missing %q: %s", text, finished.Status)
						}
					}
					persisted, err := readOperationJournal(filepath.Join(store.operationsDir, journal.OperationID+".json"))
					if err != nil || !reflect.DeepEqual(persisted, finished) {
						t.Fatalf("resolution not durable: %+v %v", persisted, err)
					}
				}
				_, created, err := store.beginMinecraftMaintenance(testMinecraftResetFingerprint, operationTypePluginInstall, "jv-d4e5f6")
				if safe && (err != nil || !created) || !safe && err == nil {
					t.Fatalf("competing maintenance: created=%v err=%v", created, err)
				}
			})
		}
	}
}

func TestMinecraftUpdateStartupSkipsUnapprovedJournals(t *testing.T) {
	x := newFailedUpdateSafetyFixture(t, "healthy")
	x.beforeCommand = func(context.Context) { t.Fatal("unapproved journal reached runtime verification") }
	for _, kind := range []string{operationTypeMinecraftContainerUpdate, operationTypeMinecraftUpdate, operationTypePluginInstall, operationTypeMinecraftReset} {
		for _, stage := range []string{"interrupted", "update_failed"} {
			if stage == "update_failed" && (kind == operationTypeMinecraftContainerUpdate || kind == operationTypeMinecraftUpdate) {
				continue
			}
			t.Run(kind+"/"+stage, func(t *testing.T) {
				store, journal := reopenedStartupUpdateFailure(t, kind, "jv-a1b2c3", stage)
				assertStartupReconciliationUnchanged(t, store, journal)
			})
		}
	}
	// Exercise the defensive gate directly for states/identities that loading
	// would otherwise normalize or reject before reconciliation can see them.
	for _, state := range []operationState{operationQueued, operationValidating, operationRunning, operationVerifying, operationFailed, operationNeedsAttention} {
		for _, id := range []string{"jv-a1b2c3", "", "invalid-instance"} {
			if state == operationNeedsAttention && id == "jv-a1b2c3" {
				continue
			}
			t.Run(string(state)+"/"+id, func(t *testing.T) {
				store, journal := reopenedStartupUpdateFailure(t, operationTypeMinecraftUpdate, "jv-a1b2c3", "update_failed")
				journal.State, journal.InstanceID = state, id
				store.operations[journal.OperationID] = journal
				assertStartupReconciliationUnchanged(t, store, journal)
			})
		}
	}
	t.Run("unknown operation", func(t *testing.T) {
		store, journal := reopenedStartupUpdateFailure(t, operationTypeMinecraftUpdate, "jv-a1b2c3", "update_failed")
		journal.OperationType = "unknown"
		store.operations[journal.OperationID] = journal
		assertStartupReconciliationUnchanged(t, store, journal)
	})
	t.Run("no current maintenance", func(t *testing.T) {
		if err := reconcileMinecraftUpdateFailureAtStartup(openTestOperationStore(t)); err != nil {
			t.Fatal(err)
		}
	})
}

func assertStartupReconciliationUnchanged(t *testing.T, store *operationStore, journal operationJournal) {
	t.Helper()
	before := startupJournalBytes(t, store, journal.OperationID)
	if err := reconcileMinecraftUpdateFailureAtStartup(store); err != nil {
		t.Fatal(err)
	}
	current, err := store.currentMinecraftMaintenance()
	if err != nil || current == nil || !reflect.DeepEqual(*current, journal) || !bytes.Equal(before, startupJournalBytes(t, store, journal.OperationID)) {
		t.Fatalf("unapproved journal changed: %+v %v", current, err)
	}
	assertStartupUpdateLocks(t, store, true)
}

func TestMinecraftUpdateStartupServeContract(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var serve *ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "serve" {
			serve = fn
		}
	}
	if serve == nil {
		t.Fatal("serve missing")
	}
	// Require the successful open and deferred close before reconciliation,
	// with a log-only failure branch, before even opening the API listener.
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(source[int(serve.Body.Pos())-1 : int(serve.Body.End())-1])
	open := strings.Index(body, "operations, err := openOperationStore(operationStateDir)")
	closed := strings.Index(body, "defer operations.close()")
	reconcile := strings.Index(body, "if err := reconcileMinecraftUpdateFailureAtStartup(operations); err != nil {\n\t\tlog.Printf(\"Minecraft update startup reconciliation: %v; startup continues\", err)\n\t}")
	listen := strings.Index(body, "net.Listen(")
	if open < 0 || closed <= open || reconcile <= closed || listen <= reconcile {
		t.Fatal("serve must reconcile after successful journal restoration and log errors without aborting API startup")
	}
}
