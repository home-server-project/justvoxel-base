package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const adminValidationHelper = "/usr/libexec/justvoxel/mjust/validate-backend"

var runAdminValidationHelper = func(ctx context.Context) ([]byte, error) {
	cmd := exec.CommandContext(ctx, adminValidationHelper)
	var output pluginBoundedOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	return output.Bytes(), err
}

type validationExitCoder interface {
	ExitCode() int
}

type adminValidationResponse struct {
	OK       bool   `json:"ok"`
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
}

func registerAdminValidationRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/admin/validation", s.adminValidation)
}

func (s *server) adminValidation(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	output, err := runAdminValidationHelper(ctx)
	var exitErr validationExitCoder
	if ctx.Err() != nil || (err != nil && !(errors.As(err, &exitErr) && exitErr.ExitCode() == 1)) {
		writeError(w, http.StatusServiceUnavailable, "JustVoxel validation is unavailable")
		return
	}

	var report strings.Builder
	report.WriteString("Host / appliance\n")
	report.Write(output)
	report.WriteString("\nMinecraft instances\n")
	ok := err == nil
	// Bound the API report independently of inventory size. Checks still run for
	// every safe registered identity even when presentation reaches its limit.
	truncated := false
	line := func(text string) {
		if report.Len()+len(text) < 80<<10 {
			report.WriteString(text + "\n")
		} else {
			truncated = true
			ok = false
		}
	}
	check := func(passed bool, label string) {
		if passed {
			line("PASS: " + label)
		} else {
			ok = false
			line("FAIL: " + label)
		}
	}

	b := instanceRuntimes
	network, networkErr := networkRead(b)
	if networkErr != nil || networkPending(b) != nil {
		check(false, "Minecraft Network infrastructure")
	} else if network.Config.Enabled {
		check(networkRuntimeReady(ctx, b, network.Config.JavaPort), "Minecraft Network infrastructure")
	}
	instances, registryErr := b.identities().listInstances("")
	legacyID, legacyErr := b.identities().readRecord("minecraft.json")
	check(registryErr == nil && (legacyErr == nil || errors.Is(legacyErr, os.ErrNotExist)), "Immutable instance registry")
	// records also checks orphan/foreign Quadlets and runtime file coverage.
	// A failed inventory must not prevent checking intact registered siblings.
	records, inventoryErr := b.records()
	check(inventoryErr == nil, "Committed runtime inventory and coverage")
	registered := make(map[string]bool)
	if registryErr == nil && (legacyErr == nil || errors.Is(legacyErr, os.ErrNotExist)) {
		for _, instance := range instances {
			if instance.ID != legacyID {
				registered[instance.ID] = true
			}
		}
		if legacyErr == nil {
			line("WARN: Retained legacy compatibility identity excluded from normal validation")
		}
		for _, record := range records {
			check(registered[record.Runtime.ID], "Runtime has a modern registered identity ("+record.Runtime.ID+")")
		}
		if len(registered) == 0 {
			line("PASS: No modern Minecraft instances registered")
		}
		for _, instance := range instances {
			if !registered[instance.ID] {
				continue
			}
			line("\n" + instance.DisplayName + " (" + instance.ID + ")")
			if err := b.validateApplianceInstance(ctx, instance.ID, check); err != nil {
				// Never expose command output, private records, or raw errors.
				writeError(w, http.StatusServiceUnavailable, "JustVoxel validation is unavailable")
				return
			}
		}
	}
	if ctx.Err() != nil {
		writeError(w, http.StatusServiceUnavailable, "JustVoxel validation is unavailable")
		return
	}
	if truncated {
		report.WriteString("FAIL: Validation report exceeded its display limit\n")
	}
	exitCode := 0
	if !ok {
		exitCode = 1
		report.WriteString("\nJustVoxel validation failed.\n")
	} else {
		report.WriteString("\nJustVoxel validation passed.\n")
	}
	writeJSON(w, http.StatusOK, adminValidationResponse{OK: ok, Output: report.String(), ExitCode: exitCode})
}

// All authority comes from the immutable ID and existing runtime backend.
// Diagnostics are fixed strings: no helper output or private configuration.
func (b *instanceRuntimeBackend) validateApplianceInstance(ctx context.Context, id string, check func(bool, string)) error {
	record, err := b.readRecord(id)
	check(err == nil, "Committed instance record")
	if err != nil {
		return nil // Unsafe to use paths or configuration from an invalid record.
	}
	liveErr := b.verifyLive(record)
	check(liveErr == nil, "Committed runtime files and recovery state")
	if liveErr != nil {
		return nil // Continue with siblings, never probe uncommitted runtime.
	}
	// This existing read-only startup gate also covers restore/migration/reset
	// evidence; verifyLive owns the Go file/hash/permission checks.
	if _, err := b.command(ctx, "/usr/libexec/justvoxel/mjust/validate-instance-runtime-commit", id); err != nil {
		if !instanceExitOne(err) {
			return err
		}
		check(false, "Instance runtime commitment gate")
		return nil
	}
	check(true, "Instance runtime commitment gate")
	storageErr := b.storage(ctx, "validate", record)
	check(storageErr == nil, "Instance storage identity")
	state, stateErr := b.observe(ctx, id)
	contextBytes, contextErr := b.command(ctx, "ls", "-Zd", "--", record.Storage.DataPath)
	fields := strings.Fields(string(contextBytes))
	contextOK := false
	contextKnown := false
	if contextErr == nil && len(fields) > 0 {
		parts := strings.Split(fields[0], ":")
		contextKnown = len(parts) >= 4 && parts[0] != "" && parts[1] != "" && parts[2] != ""
		contextOK = len(parts) >= 3 && parts[2] == "container_file_t"
	}
	if !contextOK && contextKnown && storageErr == nil && stateErr == nil && state.State == "stopped" && b.instancePendingFirstRelabel(ctx, record) {
		check(true, "Instance data SELinux label will be applied on first start")
	} else {
		check(contextOK, "Instance data SELinux context is container_file_t")
	}
	var firewallOrder []string
	firewallOK := make(map[string]bool)
	for _, rule := range record.Firewall {
		key := fmt.Sprint(rule.Port) + "/" + rule.protocol()
		if _, seen := firewallOK[key]; !seen {
			firewallOrder = append(firewallOrder, key)
			firewallOK[key] = true
		}
		present, err := b.queryFirewall(ctx, rule)
		firewallOK[key] = firewallOK[key] && err == nil && present
	}
	for _, key := range firewallOrder {
		check(firewallOK[key], "Owned firewall rule "+key)
	}
	check(stateErr == nil && (state.State == "running" || state.State == "stopped"), "Instance service and container state")
	if stateErr == nil && state.State == "running" {
		_, err := b.command(ctx, "/usr/libexec/justvoxel/mjust/verify-minecraft-stack", "--instance", id, record.Runtime.Version)
		if err != nil && !instanceExitOne(err) {
			return err
		}
		check(err == nil, "Running instance Minecraft stack")
	} else if stateErr == nil && state.State == "stopped" {
		check(true, "Stopped intentionally; live stack verification skipped")
	}
	settings, settingsErr := b.backupSettings(id)
	check(settingsErr == nil, "Instance backup settings")
	enabled, active, timerErr := b.backupTimerState(ctx, id)
	check(timerErr == nil && settingsErr == nil && ((!settings.Enabled && !enabled && !active) || (settings.Enabled && enabled && active)), "Instance backup timer matches settings")
	status, statusErr := b.backupStatus(id)
	check(statusErr == nil && status.State != "failed" && status.State != "interrupted" && !status.RestartRequired, "Instance backup status")
	_, backupErr := b.backupStorage(ctx, "inventory", id)
	check(backupErr == nil, "Instance backup destination inventory")
	return nil
}

// Called only after committed runtime/storage checks and a stopped observation.
// An empty data directory and absent container leave the committed :Z mount
// pending its first start; no label is changed by this read-only check.
func (b *instanceRuntimeBackend) instancePendingFirstRelabel(ctx context.Context, record instanceRuntimeRecord) bool {
	data, err := instanceOpen(record.Storage.DataPath, unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return false
	}
	defer data.Close()
	entries, err := data.Readdirnames(1)
	if err != io.EOF || len(entries) != 0 {
		return false
	}
	_, _, path := b.paths(record.Runtime.ID)
	quadlet, err := b.readPrivate(path)
	if err != nil || instanceHash(quadlet) != record.QuadletSHA256 {
		return false
	}
	expected := "Volume=" + record.Storage.DataPath + ":/data:Z"
	found := false
	for _, line := range strings.Split(string(quadlet), "\n") {
		if line == expected {
			found = true
		}
	}
	if !found {
		return false
	}
	output, err := b.command(ctx, "podman", "ps", "--all", "--filter", "name=^minecraft-"+record.Runtime.ID+"$", "--format", "{{.Names}}")
	return err == nil && strings.TrimSpace(string(output)) == ""
}
