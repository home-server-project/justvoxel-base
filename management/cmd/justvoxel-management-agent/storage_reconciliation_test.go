package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"testing"
	"time"
)

func TestConfiguredStorageReconciliationStartupIsBoundedAndNonfatal(t *testing.T) {
	original := runConfiguredStorageReconciliation
	defer func() { runConfiguredStorageReconciliation = original }()
	originalWriter := log.Writer()
	defer log.SetOutput(originalWriter)
	for _, problem := range []error{nil, errors.New("unsafe historical identity"), context.DeadlineExceeded} {
		var logged bytes.Buffer
		log.SetOutput(&logged)
		called := false
		runConfiguredStorageReconciliation = func(ctx context.Context) error {
			called = true
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > configuredStorageReconcileTimeout {
				t.Fatal("startup reconciliation did not receive a bounded context")
			}
			return problem
		}
		reconcileConfiguredStorageAtStartup()
		if !called {
			t.Fatal("startup reconciliation was not called")
		}
		if problem != nil && (!strings.Contains(logged.String(), problem.Error()) || !strings.Contains(logged.String(), "startup continues")) {
			t.Fatalf("nonfatal reconciliation failure was not logged: %s", logged.String())
		}
		if problem == nil && logged.Len() != 0 {
			t.Fatalf("successful reconciliation logged a failure: %s", logged.String())
		}
	}
}

func TestConfiguredStorageReconciliationStartupContract(t *testing.T) {
	mainSource, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mainSource), "func serve(socket string) error {\n\treconcileConfiguredStorageAtStartup()\n") {
		t.Fatal("serve must run nonfatal reconciliation before opening the API")
	}
	source, err := os.ReadFile("storage_reconciliation.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`exec.CommandContext(ctx, "/usr/bin/nsenter", "--mount=/proc/1/ns/mnt", "--", configuredStorageReconcileHelper)`,
		`"/usr/libexec/justvoxel/mjust/storage-reconcile-configured"`,
		`Setpgid: true`, `syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)`,
	} {
		if !strings.Contains(string(source), required) {
			t.Fatalf("fixed host-namespace startup boundary missing %q", required)
		}
	}
}
