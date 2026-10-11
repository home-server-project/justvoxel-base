package main

import (
	"context"
	"log"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const configuredStorageReconcileHelper = "/usr/libexec/justvoxel/mjust/storage-reconcile-configured"
const configuredStorageReconcileTimeout = 30 * time.Second

var runConfiguredStorageReconciliation = func(ctx context.Context) error {
	// The service's filesystem hardening creates a private mount namespace.
	// Inspect and retire mounts in the host namespace using only fixed paths.
	cmd := exec.CommandContext(ctx, "/usr/bin/nsenter", "--mount=/proc/1/ns/mnt", "--", configuredStorageReconcileHelper)
	cmd.Stderr = os.Stderr // Fixed helper diagnostics never contain credential contents.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	return cmd.Run()
}

func reconcileConfiguredStorageAtStartup() {
	ctx, cancel := context.WithTimeout(context.Background(), configuredStorageReconcileTimeout)
	defer cancel()
	if err := runConfiguredStorageReconciliation(ctx); err != nil {
		log.Printf("Configured storage reconciliation incomplete: %v; Management API startup continues; normal validation will report unresolved storage problems", err)
	}
}
