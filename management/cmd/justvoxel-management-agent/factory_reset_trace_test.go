package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Execute the real helper's safety gate and trace transport, stopping at the
// shared-cleanup boundary. No appliance paths or host services are operated.
func factoryResetGateCommand(t *testing.T, ctx context.Context, retired bool) *exec.Cmd {
	t.Helper()
	libexec, err := filepath.Abs("../../../mjust/libexec")
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join(libexec, "admin-factory-reset-json"))
	if err != nil {
		t.Fatal(err)
	}
	_, gate, ok := strings.Cut(string(source), "if [[ ${JUSTVOXEL_FACTORY_RESET_INSTANCES_RETIRED:-} != 1 ]]")
	if !ok {
		t.Fatal("helper gate not found")
	}
	gate, _, ok = strings.Cut(gate, `jv_trace_stage "RESET Cleaning appliance state"`)
	if !ok {
		t.Fatal("shared cleanup boundary not found")
	}
	gate = "if [[ ${JUSTVOXEL_FACTORY_RESET_INSTANCES_RETIRED:-} != 1 ]]" + gate
	verifier := filepath.Join(t.TempDir(), "management-agent")
	if err = os.WriteFile(verifier, []byte(`#!/bin/bash
[[ $1 == factory-reset-runtime-empty && ${JV_OPERATION_TRACE_FD:-} == 3 ]] || exit 1
printf 'VERIFIER reached\n' >&3
`), 0700); err != nil {
		t.Fatal(err)
	}
	gate = strings.ReplaceAll(gate, "/usr/libexec/justvoxel/management-agent", `"$2"`)
	script := `source "$1"
factory_reset_fail() { printf '%s\n' "$1"; exit 5; }
` + gate + `printf 'shared cleanup reached\n'`
	cmd := exec.CommandContext(ctx, "/bin/bash", "-c", script, "factory-reset-gate", filepath.Join(libexec, "operation-trace.sh"), verifier)
	// The same apply contract as runAdminFactoryResetHelper; its source contract
	// is also checked by test-factory-reset-source.sh.
	cmd.Env = []string{}
	if retired {
		cmd.Env = append(cmd.Env, "JUSTVOXEL_FACTORY_RESET_INSTANCES_RETIRED=1")
	}
	return cmd
}

func TestFactoryResetTracedHelperSafetyGate(t *testing.T) {
	for _, retired := range []bool{false, true} {
		name := "direct-without-retirement"
		if retired {
			name = "traced-after-retirement"
		}
		t.Run(name, func(t *testing.T) {
			store := openTestOperationStore(t)
			id := traceTestOperation(t, store, operationTypeFactoryReset)
			ctx := withOperationTrace(context.Background(), store, id)
			cmd := factoryResetGateCommand(t, ctx, retired)
			finish := func(error) {}
			if retired {
				var err error
				finish, err = attachOperationTrace(ctx, cmd)
				if err != nil {
					t.Fatal(err)
				}
			}
			output, runErr := cmd.Output()
			finish(runErr)
			if !retired {
				if runErr == nil || string(output) != "instance_cleanup_required\n" {
					t.Fatalf("unguarded direct invocation: %q, %v", output, runErr)
				}
				return
			}
			trace, err := store.readTrace(id)
			if runErr != nil || err != nil || string(output) != "shared cleanup reached\n" || !strings.Contains(string(trace), "VERIFIER reached") {
				t.Fatalf("traced helper: %q, %v; trace: %s, %v", output, runErr, trace, err)
			}
		})
	}
}

func TestFactoryResetRetryAfterAllParticipantsRetiredReachesSharedCleanup(t *testing.T) {
	f := newFactoryResetFixture(t, 2)
	s := factoryFixtureServer(t)
	ctx := context.Background()
	oldHelper := runAdminFactoryResetHelper
	t.Cleanup(func() { runAdminFactoryResetHelper = oldHelper })
	reached := false
	runAdminFactoryResetHelper = func(ctx context.Context, args ...string) ([]byte, error) {
		if args[0] == "plan" {
			return []byte(validFactoryResetPlanHelper), nil
		}
		if err := f.x.b.factoryResetEmpty(ctx); err != nil {
			t.Fatal("shared helper reached without empty runtime", err)
		}
		cmd := factoryResetGateCommand(t, ctx, true)
		finish, err := attachOperationTrace(ctx, cmd)
		if err != nil {
			t.Fatal(err)
		}
		output, err := cmd.Output()
		finish(err)
		if err != nil || string(output) != "shared cleanup reached\n" {
			t.Fatalf("retry could not reach shared cleanup: %q, %v", output, err)
		}
		reached = true
		// Stop deliberately before shared appliance/identity cleanup. The test
		// verifies this boundary without substituting fake successful cleanup.
		return []byte(`{"ok":false,"error":"fixture stopped at shared cleanup"}`), errors.New("fixture stop")
	}
	plan, status := s.factoryResetPlan(ctx)
	if status != 0 {
		t.Fatal(plan, status)
	}
	op, _, err := s.operations.beginFactoryReset(plan.PlanFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.retireFactoryResetInstances(ctx, op.OperationID, plan); err != nil {
		t.Fatal(err)
	}
	if _, err = s.operations.transition(op.OperationID, operationNeedsAttention, "reset_failed", "Shared cleanup failed."); err != nil {
		t.Fatal(err)
	}
	evidence, err := loadFactoryResetEvidence(s, op.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range evidence.Entries {
		if entry.Retirement.Stage != "identity_retired" {
			t.Fatal("participant not retired", entry.Retirement.Stage)
		}
	}
	before, err := os.ReadFile(factoryResetEvidencePath(s, op.OperationID))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.resolveFactoryResetParticipants(ctx, op.OperationID); err == nil {
		t.Fatal("Keep Current State accepted destructive reset")
	}
	reviewed, status := s.factoryResetPlan(ctx)
	if status != 0 || len(reviewed.Instances) != 2 {
		t.Fatal("retired participants stranded", reviewed, status)
	}
	retry, err := s.operations.retryFactoryReset(op.OperationID, reviewed.PlanFingerprint)
	if err != nil || retry.OperationID != op.OperationID {
		t.Fatal(retry, err)
	}
	if err = executeFactoryReset(ctx, s, retry.OperationID, reviewed.PlanFingerprint); err == nil || !reached {
		t.Fatal("retry did not reach controlled shared-cleanup boundary", err)
	}
	after, err := os.ReadFile(factoryResetEvidencePath(s, op.OperationID))
	if err != nil || string(after) != string(before) {
		t.Fatal("retirement evidence changed or disappeared", err)
	}
	for _, entry := range evidence.Entries {
		if f.removals[entry.Retirement.ID] != 1 {
			t.Fatal("retirement repeated", f.removals)
		}
	}
	trace, err := s.operations.readTrace(op.OperationID)
	if err != nil || !strings.Contains(string(trace), "VERIFIER reached") {
		t.Fatal("retry did not execute traced verifier", err)
	}
}
