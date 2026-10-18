package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSystemdJobIdle(t *testing.T) {
	for _, tt := range []struct {
		output string
		err    error
		idle   bool
	}{
		{"", nil, true}, {" \n", nil, true}, {"0\n", nil, true}, {"42\n", nil, false}, {"job=0", nil, false}, {"", errors.New("systemctl failed"), false}, {"0", errors.New("systemctl failed"), false},
	} {
		if systemdJobIdle([]byte(tt.output), tt.err) != tt.idle {
			t.Fatalf("output=%q error=%v", tt.output, tt.err)
		}
	}
}

func TestDeleteAndStartOverSystemdJobs(t *testing.T) {
	for _, path := range []string{"delete", "start-over"} {
		for _, tt := range []struct {
			name, value string
			failure     bool
			idle        bool
		}{
			{"empty", "", false, true}, {"zero", "0\n", false, true}, {"queued", "42", false, false}, {"error", "", true, false},
		} {
			t.Run(path+"/"+tt.name, func(t *testing.T) {
				d := newDeleteFixture(t)
				b, id := d.f.backend, d.f.configs[0].ID
				original := b.command
				reached := false
				b.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
					if command == "systemctl" && len(args) > 1 && args[1] == "--property=Job" {
						reached = true
						if tt.failure {
							return nil, errors.New("unavailable")
						}
						return []byte(tt.value), nil
					}
					return original(ctx, command, args...)
				}
				var err error
				if path == "delete" {
					err = b.deleteStopped(context.Background(), id, true)
				} else {
					err = b.startOverInstanceIdle(context.Background(), id)
				}
				if !reached || (err == nil) != tt.idle {
					t.Fatalf("reached=%v error=%v idle=%v", reached, err, tt.idle)
				}
			})
		}
	}
}

func TestRuntimeCommandUsesSetupTraceWithoutPrivateStderr(t *testing.T) {
	store := openTestOperationStore(t)
	id := traceTestOperation(t, store, operationTypeSetup)
	script := filepath.Join(t.TempDir(), "runtime-helper")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' '{\"ok\":true}'\nprintf '%s' 'private-stderr-value' >&2\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	out, err := instanceRuntimeCommand(withOperationTrace(context.Background(), store, id, "runtime-secret"), script, "runtime-secret")
	if err == nil || string(out) != `{"ok":true}` {
		t.Fatal("stdout or exit protocol changed", string(out), err)
	}
	raw, err := store.readTrace(id)
	if err != nil {
		t.Fatal(err)
	}
	trace := string(raw)
	if !strings.Contains(trace, script) || !strings.Contains(trace, "[exit 7]") {
		t.Fatal("runtime command or result absent", trace)
	}
	for _, private := range []string{"private-stderr-value", "runtime-secret", `{"ok":true}`} {
		if strings.Contains(trace, private) {
			t.Fatal("private output leaked", trace)
		}
	}
}

func TestInstancePlacementRemainsPinnedAcrossDefaultAndCapacityChanges(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	records, err := f.backend.records()
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.backend.render(f.snapshot, records)
	if err != nil {
		t.Fatal(err)
	}
	changed := f.snapshot
	changed.StorageRoot = filepath.Join(f.snapshot.StorageRoot, "new-default")
	changed.BackupPath = filepath.Join(f.snapshot.StorageRoot, "other-backups")
	changed.ExpectedUUID = "new-default-uuid"
	changed.MountPoint = changed.StorageRoot
	after, err := f.backend.render(changed, records)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("existing placement moved with defaults", err)
	}
}

func TestDifferentInstanceStorageRenderingAndBackupEvidence(t *testing.T) {
	f := newInstanceActivationFixture(t)
	records, err := f.backend.records()
	if err != nil {
		t.Fatal(err)
	}
	ctx := f.snapshot
	ctx.StorageRoot = filepath.Join(ctx.StorageRoot, "other-filesystem")
	ctx.BackupPath = filepath.Join(ctx.StorageRoot, "backups")
	ctx.BackupPlacement = &instanceRuntimeBackupStorage{Type: "system", Root: ctx.BackupPath}
	records[1].Storage = instanceStorageFor(ctx, records[1].Runtime.ID)
	records[1].Backups = instanceBackupsFor(ctx, records[1].Runtime.ID)
	files, err := f.backend.render(f.snapshot, records)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		for _, r := range records {
			if r.Runtime.ID == file.ID && file.DataPath != r.Storage.DataPath {
				t.Fatal("sibling placement substituted")
			}
		}
	}
	r := records[1]
	if r.Backups.Path != filepath.Join(ctx.BackupPath, r.Runtime.ID) {
		t.Fatal("backup namespace is not instance specific")
	}
	r.Backups.Path = filepath.Join(ctx.BackupPath, records[0].Runtime.ID)
	if validateInstanceRecordPlacement(r) == nil {
		t.Fatal("sibling backup namespace accepted")
	}
}

func TestFactoryResetSystemdJobs(t *testing.T) {
	for _, tt := range []struct {
		name, value   string
		failure, idle bool
	}{
		{"empty", "", false, true}, {"zero", "0", false, true}, {"queued", "42", false, false}, {"error", "", true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFactoryResetFixture(t, 1)
			b := f.x.b
			original := b.command
			reached := false
			b.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
				if command == "systemctl" && len(args) > 1 && args[1] == "--property=Job" {
					reached = true
					if tt.failure {
						return nil, errors.New("systemctl failure")
					}
					return []byte(tt.value), nil
				}
				return original(ctx, command, args...)
			}
			_, _, err := b.factoryResetParticipants(context.Background())
			if !reached || (err == nil) != tt.idle {
				t.Fatalf("reached=%v error=%v", reached, err)
			}
		})
	}
}
