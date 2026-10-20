package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMinecraftMemoryRunningAndBootBudgets(t *testing.T) {
	for _, tc := range []struct {
		name, state, other, target string
		boot, otherBoot, fits      bool
	}{
		{"VM stopped 6G plus 6G", "inactive", "6G", "6G", false, false, true},
		{"VM running 6G plus 6G", "active", "6G", "6G", false, false, false},
		{"VM starting 6G plus 6G", "activating", "6G", "6G", false, false, false},
		{"safe running 4G plus 2G", "active", "4G", "2G", false, false, true},
		{"safe boot 4G plus 2G", "inactive", "4G", "2G", true, true, true},
		{"unsafe boot 4G plus 4G", "inactive", "4G", "4G", true, true, false},
		{"unsafe boot stopped 6G plus 6G", "inactive", "6G", "6G", true, true, false},
		{"stopped boot disabled ignored", "inactive", "6G", "6G", true, false, true},
		{"unknown sibling state", "unknown", "2G", "2G", false, false, false},
		{"stopping sibling", "deactivating", "2G", "2G", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newInstanceActivationFixture(t)
			a, b := f.configs[0], f.configs[1]
			a.ContainerMemory, a.StartOnBoot = tc.other, tc.otherBoot
			b.ContainerMemory = tc.target
			f.states[a.ID] = tc.state
			if tc.state == "active" {
				f.containers[a.ID] = "running"
			}
			// A running restart candidate must be counted exactly once.
			f.states[b.ID], f.containers[b.ID] = "active", "running"
			records := []instanceRuntimeRecord{{Runtime: a}, {Runtime: b}}
			err := f.backend.admitMemory(context.Background(), 7867, records, b, tc.boot)
			if (err == nil) != tc.fits {
				t.Fatalf("fits=%v error=%v", tc.fits, err)
			}
		})
	}
}

func TestManyInstalledMemoryRendersAndAdmitsOne(t *testing.T) {
	store, snapshot, configs := instanceRuntimeFixture(t)
	snapshot.TotalMemoryMiB = 7867
	for i := range configs {
		configs[i].JavaMemory, configs[i].ContainerMemory = "4G", "6G"
		configs[i].StartOnBoot = i == 0
	}
	for i, id := range []string{"aaaaaa", "bbbbbb"} {
		registerLogicalForTest(t, store, id, "paper", id)
		c := configs[0]
		c.ID = "jv-" + id
		c.JavaPort = 25568 + i
		c.StartOnBoot = false
		configs = append(configs, c)
	}
	files, err := store.renderInstanceRuntimes(snapshot, configs)
	if err != nil || len(files) != 4 {
		t.Fatal("installed memory must not restrict rendering", err)
	}
	b := &instanceRuntimeBackend{command: func(_ context.Context, command string, args ...string) ([]byte, error) {
		if command == "systemctl" {
			return []byte("LoadState=not-found\nActiveState=inactive\n"), nil
		}
		return nil, nil
	}}
	records := []instanceRuntimeRecord{}
	for _, c := range configs {
		records = append(records, instanceRuntimeRecord{Runtime: c})
	}
	if err := b.admitMemory(context.Background(), 7867, records, configs[0], false); err != nil {
		t.Fatal(err)
	}
	if err := b.admitMemory(context.Background(), 7867, records, configs[0], true); err != nil {
		t.Fatal(err)
	}
}

func TestManualMemoryStartRestartAndStop(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.snapshot.TotalMemoryMiB = 7867
	for _, c := range f.configs {
		record, err := f.backend.readRecord(c.ID)
		if err != nil {
			t.Fatal(err)
		}
		record.Runtime.JavaMemory, record.Runtime.ContainerMemory, record.Runtime.StartOnBoot = "4G", "6G", false
		dir, _, _ := f.backend.paths(c.ID)
		if err := f.backend.writeJSON(filepath.Join(dir, "config.json"), record, true); err != nil {
			t.Fatal(err)
		}
	}
	f.activateBoth()
	a, b := f.configs[0].ID, f.configs[1].ID
	if err := f.backend.action(context.Background(), a, "start"); err != nil {
		t.Fatal(err)
	}
	if err := f.backend.action(context.Background(), a, "restart"); err != nil {
		t.Fatal("restart counted target twice", err)
	}
	if err := f.backend.action(context.Background(), b, "start"); err == nil || !strings.Contains(err.Error(), "Not enough Minecraft memory capacity") {
		t.Fatal("unsafe simultaneous start", err)
	}
	if err := f.backend.action(context.Background(), a, "stop"); err != nil {
		t.Fatal(err)
	}
	if err := f.backend.action(context.Background(), b, "start"); err != nil {
		t.Fatal("stopped server consumed budget", err)
	}
}

func TestBootMemoryUpdatesPreserveSelectedAndSiblings(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.snapshot.TotalMemoryMiB = 16384
	f.activateBoth()
	ctx := context.Background()
	for _, c := range f.configs {
		old, err := f.backend.readRecord(c.ID)
		if err != nil {
			t.Fatal(err)
		}
		next := old
		next.Runtime.JavaMemory, next.Runtime.ContainerMemory, next.Runtime.StartOnBoot = "3G", "4G", true
		records, err := f.backend.records()
		if err != nil {
			t.Fatal(err)
		}
		if err := f.backend.apply(ctx, old, instanceReplaceRecord(records, next), f.snapshot); err != nil {
			t.Fatal(err)
		}
	}
	f.snapshot.TotalMemoryMiB = 7867
	id := f.configs[1].ID
	old, err := f.backend.readRecord(id)
	if err != nil {
		t.Fatal(err)
	}
	records, err := f.backend.records()
	if err != nil {
		t.Fatal(err)
	}
	next := old
	next.Runtime.StartOnBoot = false
	if err := f.backend.apply(ctx, old, instanceReplaceRecord(records, next), f.snapshot); err != nil {
		t.Fatal("disabling unsafe boot set refused", err)
	}
	old, err = f.backend.readRecord(id)
	if err != nil {
		t.Fatal(err)
	}
	records, err = f.backend.records()
	if err != nil {
		t.Fatal(err)
	}
	next = old
	next.Runtime.StartOnBoot = true
	if err := f.backend.apply(ctx, old, instanceReplaceRecord(records, next), f.snapshot); err == nil {
		t.Fatal("unsafe boot enable accepted")
	}
	current, err := f.backend.readRecord(id)
	if err != nil || current.Runtime.StartOnBoot {
		t.Fatal("rejected boot enable altered selected server", err)
	}
	// 4G + 2G fits; increasing the boot-enabled 2G to 4G must fail.
	next = old
	next.Runtime.JavaMemory, next.Runtime.ContainerMemory, next.Runtime.StartOnBoot = "1G", "2G", true
	if err := f.backend.apply(ctx, old, instanceReplaceRecord(records, next), f.snapshot); err != nil {
		t.Fatal(err)
	}
	old, err = f.backend.readRecord(id)
	if err != nil {
		t.Fatal(err)
	}
	records, err = f.backend.records()
	if err != nil {
		t.Fatal(err)
	}
	next = old
	next.Runtime.ContainerMemory = "4G"
	if err := f.backend.apply(ctx, old, instanceReplaceRecord(records, next), f.snapshot); err == nil {
		t.Fatal("unsafe boot memory increase accepted")
	}
}

func TestStartAfterSetupFingerprint(t *testing.T) {
	var response adminSetupPlanResponse
	if err := json.Unmarshal([]byte(validAdminSetupPlanResponse), &response); err != nil {
		t.Fatal(err)
	}
	response.Normalized.AddServer = true
	before, err := adminSetupPlanFingerprint(response.SchemaVersion, response.Normalized, response.Requirements)
	if err != nil {
		t.Fatal(err)
	}
	response.Normalized.StartAfterSetup = true
	after, err := adminSetupPlanFingerprint(response.SchemaVersion, response.Normalized, response.Requirements)
	if err != nil || before == after {
		t.Fatal("start intent not bound to review", err)
	}
}

func TestSetupStartAfterPublicationNeverRollsBack(t *testing.T) {
	for _, failStart := range []bool{false, true} {
		t.Run(map[bool]string{false: "start", true: "capacity changed"}[failStart], func(t *testing.T) {
			f, r := newProvisionalSetupFixture(t, true)
			f.snapshot.TotalMemoryMiB = 7867
			r.StartAfterSetup = true
			r.Minecraft.JavaMemory, r.Minecraft.ContainerMemory = "4G", "6G"
			e := setupActions(t, r, "validate", "apply", "verify")
			id := e.Record.Runtime.ID
			if f.states[id] != "inactive" {
				t.Fatal("unpublished Add Server started")
			}
			if failStart {
				// Model a direct systemd start after the reviewed preflight.
				f.states[f.configs[0].ID] = "active"
				f.containers[f.configs[0].ID] = "running"
			}
			e = setupActions(t, r, "commit")
			if e.Publication.Stage != "finalized" || !e.Publication.StartAttempted {
				t.Fatal(e.Publication)
			}
			if failStart && e.Publication.StartError == "" {
				t.Fatal("start failure missing")
			}
			if !failStart && f.states[id] != "active" {
				t.Fatal("published server did not start")
			}
			if _, err := f.backend.readRecord(id); err != nil {
				t.Fatal("published identity lost", err)
			}
			if _, err := os.Stat(e.Record.Storage.DataPath); err != nil {
				t.Fatal("published storage lost", err)
			}
			if !failStart {
				if err := f.backend.action(context.Background(), id, "stop"); err != nil {
					t.Fatal(err)
				}
			}
			setupActions(t, r, "rollback") // Recovery remains commit-forward.
			if f.states[id] != "inactive" {
				t.Fatal("recovery repeated an already attempted start")
			}
			assertNoSetupDelete(t, f.backend, id)
		})
	}
}

func TestAuthoritativeAddPlanMemoryChoices(t *testing.T) {
	f, _ := newProvisionalSetupFixture(t, true)
	f.snapshot.TotalMemoryMiB = 7867
	// Two installed 6G servers are valid while both are stopped.
	for _, c := range f.configs {
		old, err := f.backend.readRecord(c.ID)
		if err != nil {
			t.Fatal(err)
		}
		next := old
		next.Runtime.JavaMemory, next.Runtime.ContainerMemory, next.Runtime.StartOnBoot = "4G", "6G", false
		records, err := f.backend.records()
		if err != nil {
			t.Fatal(err)
		}
		if err := f.backend.apply(context.Background(), old, instanceReplaceRecord(records, next), f.snapshot); err != nil {
			t.Fatal(err)
		}
	}
	helper := runAdminSetupPlanHelper
	t.Cleanup(func() { runAdminSetupPlanHelper = helper })
	runAdminSetupPlanHelper = func(context.Context, []byte) ([]byte, error) { return []byte(validAdminSetupPlanResponse), nil }
	var request adminSetupPlanRequest
	if err := json.Unmarshal([]byte(validAdminSetupPlanRequest), &request); err != nil {
		t.Fatal(err)
	}
	request.AddServer = true
	out, err := authoritativeAdminSetupPlan(context.Background(), request)
	if err != nil || !out.OK {
		t.Fatal("installed aggregate blocked plan", out, err)
	}
	request.StartAfterSetup = true
	out, err = authoritativeAdminSetupPlan(context.Background(), request)
	if err != nil || !out.OK {
		t.Fatal("stopped instances consumed running capacity", out, err)
	}
	a := f.configs[0].ID
	if err := f.backend.action(context.Background(), a, "start"); err != nil {
		t.Fatal(err)
	}
	if _, err := authoritativeAdminSetupPlan(context.Background(), request); err == nil {
		t.Fatal("stale start intent admitted")
	}
	request.StartAfterSetup = false
	out, err = authoritativeAdminSetupPlan(context.Background(), request)
	if err != nil || !out.OK {
		t.Fatal("creation without starts refused", err)
	}
	// Enabling boot on one existing 6G leaves no room for a second 6G.
	old, e := f.backend.readRecord(a)
	if e != nil {
		t.Fatal(e)
	}
	next := old
	next.Runtime.StartOnBoot = true
	records, e := f.backend.records()
	if e != nil {
		t.Fatal(e)
	}
	if e := f.backend.apply(context.Background(), old, instanceReplaceRecord(records, next), f.snapshot); e != nil {
		t.Fatal(e)
	}
	request.StartOnBoot = true
	if _, err := authoritativeAdminSetupPlan(context.Background(), request); err == nil {
		t.Fatal("unsafe boot plan admitted")
	}
}

func TestVMAddSecondSixGiBServerRemainsInstalledAndStopped(t *testing.T) {
	f, r := newProvisionalSetupFixture(t, false)
	f.snapshot.TotalMemoryMiB = 7867
	r.Minecraft.JavaMemory, r.Minecraft.ContainerMemory = "4G", "6G"
	first := setupActions(t, r, "validate", "apply", "verify", "commit")
	if err := f.backend.action(context.Background(), first.Record.Runtime.ID, "stop"); err != nil {
		t.Fatal(err)
	}
	sibling, err := f.backend.readRecord(first.Record.Runtime.ID)
	if err != nil {
		t.Fatal(err)
	}
	r.AddServer, r.StartAfterSetup, r.StartOnBoot = true, false, false
	r.OperationID = "22345678-1234-4123-8123-123456789abc"
	r.Server.DisplayName = "Second"
	r.Minecraft.JavaPort = instanceTestPort(t)
	r.Backups.Schedule = "*-*-* 05:00:00"
	second := setupActions(t, r, "validate", "apply", "verify", "commit")
	if f.states[second.Record.Runtime.ID] != "inactive" {
		t.Fatal("unrequested start")
	}
	records, err := f.backend.records()
	if err != nil || len(records) != 2 {
		t.Fatal("12G installed inventory unavailable", err)
	}
	current, err := f.backend.readRecord(sibling.Runtime.ID)
	if err != nil || current.Runtime.ContainerMemory != sibling.Runtime.ContainerMemory {
		t.Fatal("sibling changed", err)
	}
	if err := f.backend.action(context.Background(), second.Record.Runtime.ID, "start"); err != nil {
		t.Fatal("stopped sibling counted", err)
	}
	if err := f.backend.action(context.Background(), first.Record.Runtime.ID, "start"); err == nil {
		t.Fatal("12G simultaneous start accepted")
	}
	assertNoSetupDelete(t, f.backend, second.Record.Runtime.ID)
}

func TestSimultaneousFourAndTwoGiBManualStarts(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.snapshot.TotalMemoryMiB = 7867
	f.activateBoth()
	for i, c := range f.configs {
		old, err := f.backend.readRecord(c.ID)
		if err != nil {
			t.Fatal(err)
		}
		next := old
		next.Runtime.StartOnBoot = true
		if i == 0 {
			next.Runtime.JavaMemory, next.Runtime.ContainerMemory = "3G", "4G"
		}
		records, err := f.backend.records()
		if err != nil {
			t.Fatal(err)
		}
		if err := f.backend.apply(context.Background(), old, instanceReplaceRecord(records, next), f.snapshot); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range f.configs {
		if err := f.backend.action(context.Background(), c.ID, "start"); err != nil {
			t.Fatal("safe 4G + 2G start refused", err)
		}
	}
	for _, c := range f.configs {
		if f.states[c.ID] != "active" {
			t.Fatal("both servers did not run")
		}
	}
}
