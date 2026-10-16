package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const setupRuntimeDiagnosticImageRepo = "docker.io/itzg/minecraft-server"

func (s *operationStore) appendSetupRuntimeHelperEvidenceBestEffort(operationID, action string, evidence map[string]string) {
	if s == nil || !validOperationID(operationID) || len(evidence) == 0 {
		return
	}
	values := make(map[string]string, len(evidence)+1)
	values["action"] = action
	for key, value := range evidence {
		values[key] = value
	}
	_ = s.appendSetupDiagnostic(operationID, "RUNTIME", "runtime helper evidence", values)
}

func (s *operationStore) appendSetupRuntimeEvidenceBestEffort(operationID, phase string, plan *adminSetupNormalizedPlan) {
	if s == nil || !validOperationID(operationID) {
		return
	}
	values := s.setupRuntimeEvidenceForOperation(operationID, plan)
	values["phase"] = phase
	_ = s.appendSetupDiagnostic(operationID, "RUNTIME", "runtime dependency evidence", values)
}

func (s *operationStore) appendSetupRuntimeVerificationBestEffort(operationID string, plan *adminSetupNormalizedPlan, elapsed time.Duration) {
	if s == nil || !validOperationID(operationID) {
		return
	}
	values := s.setupRuntimeEvidenceForOperation(operationID, plan)
	values["runtime_verify_elapsed_seconds"] = strconv.FormatInt(int64(elapsed.Round(time.Second)/time.Second), 10)
	values["rcon_verified"] = "yes"
	values["final_validation"] = "passed"
	if plan != nil && plan.Server.BedrockEnabled {
		values["bedrock_verified"] = "yes"
	} else {
		values["bedrock_verified"] = "not_enabled"
	}
	_ = s.appendSetupDiagnostic(operationID, "VERIFY", "Minecraft runtime verification completed", values)
}

func (s *operationStore) appendSetupRuntimeFailureBundleBestEffort(operationID, action string, plan *adminSetupNormalizedPlan, cause error) {
	if s == nil || !validOperationID(operationID) {
		return
	}
	values := s.setupRuntimeEvidenceForOperation(operationID, plan)
	values["action"] = action
	if cause != nil {
		values["error"] = cause.Error()
	}
	if id := values["instance_id"]; id != "" {
		if instanceIDPattern.MatchString(id) {
			values["minecraft_journal"] = setupDiagnosticRuntimeJournal("minecraft-" + id + ".service")
		} else {
			values["minecraft_journal"] = "<not_created>"
		}
	} else {
		values["minecraft_journal"] = setupDiagnosticRuntimeJournal()
	}
	values["selinux_avc"] = setupDiagnosticRecentAVC()
	if values["instance_id"] != "" {
		values["validation_snapshot"] = "Instance runtime evidence is recorded above; fixed-server appliance validation is not applicable."
	} else {
		values["validation_snapshot"] = setupDiagnosticValidationSnapshot()
	}
	values["dns_docker_io"] = setupDiagnosticDNSStatus("docker.io")
	values["dns_papermc_io"] = setupDiagnosticDNSStatus("fill.papermc.io")
	if plan != nil && plan.Server.BedrockEnabled {
		values["dns_geysermc_org"] = setupDiagnosticDNSStatus("download.geysermc.org")
	}
	_ = s.appendSetupDiagnostic(operationID, "FAILURE", "runtime failure evidence", values)
}

func collectSetupRuntimeEvidence(plan *adminSetupNormalizedPlan, instanceID ...string) map[string]string {
	container, unit, timer := "minecraft", "minecraft.service", "minecraft-backup.timer"
	env, quadlet := "/etc/justvoxel/minecraft.env", "/etc/containers/systemd/minecraft.container"
	dataPath := ""
	if len(instanceID) > 0 && instanceIDPattern.MatchString(instanceID[0]) {
		id := instanceID[0]
		container = "minecraft-" + id
		unit = container + ".service"
		timer = instanceBackupUnit(id, "timer")
		_, env, quadlet = instanceRuntimes.paths(id)
		if record, err := instanceRuntimes.readRecord(id); err == nil {
			dataPath = record.Storage.DataPath
		}
	}

	values := map[string]string{
		"quadlet_present":          setupDiagnosticPathPresence(quadlet),
		"config_present":           setupDiagnosticPathPresence("/etc/justvoxel/justvoxel.conf"),
		"minecraft_env_present":    setupDiagnosticPathPresence(env),
		"setup_marker_present":     setupDiagnosticPathPresence("/var/lib/justvoxel/management/setup-in-progress"),
		"podman_network_backend":   setupDiagnosticCommandValue(4*time.Second, "podman", "info", "--format", "{{.Host.NetworkBackend}}"),
		"podman_default_network":   setupDiagnosticCommandPresence(4*time.Second, "podman", "network", "exists", "podman"),
		"podman_network_summary":   "<unavailable>",
		"container_present":        setupDiagnosticCommandPresence(4*time.Second, "podman", "container", "exists", container),
		"container_state":          "<unavailable>",
		"container_exit_code":      "<unavailable>",
		"container_image_id":       "<unavailable>",
		"container_networks":       "<unavailable>",
		"minecraft_image_digest":   "<unavailable>",
		"minecraft_image_local_id": "<unavailable>",
		"network_online_target":    setupDiagnosticCommandValue(4*time.Second, "systemctl", "is-active", "network-online.target"),
		"network_connectivity":     setupDiagnosticCommandValue(4*time.Second, "nmcli", "-t", "-f", "CONNECTIVITY", "general"),
	}

	for key, value := range setupDiagnosticMinecraftServiceState(unit) {
		values[key] = value
	}

	if values["podman_default_network"] == "present" {
		values["podman_network_summary"] = setupDiagnosticCommandValue(4*time.Second, "podman", "network", "inspect", "podman", "--format", "{{.Name}}|{{.Driver}}|{{.NetworkInterface}}|{{.DNSEnabled}}")
	}
	if values["container_present"] == "present" {
		values["container_state"] = setupDiagnosticCommandValue(4*time.Second, "podman", "inspect", container, "--format", "{{.State.Status}}")
		values["container_exit_code"] = setupDiagnosticCommandValue(4*time.Second, "podman", "inspect", container, "--format", "{{.State.ExitCode}}")
		values["container_image_id"] = setupDiagnosticCommandValue(4*time.Second, "podman", "inspect", container, "--format", "{{.Image}}")
		values["container_networks"] = setupDiagnosticCommandValue(4*time.Second, "podman", "inspect", container, "--format", "{{range $name, $_ := .NetworkSettings.Networks}}{{$name}} {{end}}")
	}

	if plan != nil {
		values["minecraft_image_tag"] = plan.Minecraft.ImageTag
		values["java_firewall_open"] = setupDiagnosticCommandPresence(4*time.Second, "firewall-cmd", "--permanent", "--query-port="+strconv.Itoa(plan.Minecraft.JavaPort)+"/tcp")
		if plan.Server.BedrockEnabled {
			values["bedrock_firewall_open"] = setupDiagnosticCommandPresence(4*time.Second, "firewall-cmd", "--permanent", "--query-port="+strconv.Itoa(plan.Minecraft.BedrockPort)+"/udp")
		} else {
			values["bedrock_firewall_open"] = "not_enabled"
		}
		values["data_selinux_type"] = setupDiagnosticSELinuxType(plan.Storage.Path)
		if dataPath != "" {
			values["data_selinux_type"] = setupDiagnosticSELinuxType(dataPath)
		}
		imageRef := fmt.Sprintf("%s:%s", setupRuntimeDiagnosticImageRepo, plan.Minecraft.ImageTag)
		if setupDiagnosticCommandPresence(4*time.Second, "podman", "image", "exists", imageRef) == "present" {
			values["minecraft_image_digest"] = setupDiagnosticCommandValue(4*time.Second, "podman", "image", "inspect", imageRef, "--format", "{{.Digest}}")
			values["minecraft_image_local_id"] = setupDiagnosticCommandValue(4*time.Second, "podman", "image", "inspect", imageRef, "--format", "{{.Id}}")
		}
	}

	values["backup_timer_enabled"] = setupDiagnosticCommandValue(4*time.Second, "systemctl", "is-enabled", timer)
	values["backup_timer_active"] = setupDiagnosticCommandValue(4*time.Second, "systemctl", "is-active", timer)
	return values
}

func setupDiagnosticMinecraftServiceState(units ...string) map[string]string {
	unit := "minecraft.service"
	if len(units) > 0 {
		unit = units[0]
	}
	values := map[string]string{
		"service_load_state":   "<unavailable>",
		"service_active_state": "<unavailable>",
		"service_sub_state":    "<unavailable>",
		"service_result":       "<unavailable>",
		"service_exit_status":  "<unavailable>",
	}
	output, _ := setupDiagnosticRunCommand(4*time.Second, "systemctl", "show", unit, "--property=LoadState,ActiveState,SubState,Result,ExecMainStatus", "--no-pager")
	for _, line := range strings.Split(string(output), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "LoadState":
			values["service_load_state"] = value
		case "ActiveState":
			values["service_active_state"] = value
		case "SubState":
			values["service_sub_state"] = value
		case "Result":
			values["service_result"] = value
		case "ExecMainStatus":
			values["service_exit_status"] = value
		}
	}
	return values
}

func setupDiagnosticSELinuxType(path string) string {
	if strings.TrimSpace(path) == "" {
		return "<unavailable>"
	}
	output, err := setupDiagnosticRunCommand(4*time.Second, "ls", "-Zd", "--", path)
	if err != nil {
		return "<unavailable>"
	}
	fields := strings.Fields(string(output))
	if len(fields) == 0 {
		return "<unavailable>"
	}
	parts := strings.Split(fields[0], ":")
	if len(parts) < 3 || strings.TrimSpace(parts[2]) == "" {
		return "<unavailable>"
	}
	return parts[2]
}

func setupDiagnosticRuntimeJournal(units ...string) string {
	unit := "minecraft.service"
	if len(units) > 0 {
		unit = units[0]
	}
	output, _ := setupDiagnosticRunCommand(6*time.Second, "journalctl", "-u", unit, "--no-pager", "-n", "80", "-o", "short-iso")
	return setupDiagnosticBoundedText(output, 12*1024)
}

func setupDiagnosticRecentAVC() string {
	output, _ := setupDiagnosticRunCommand(6*time.Second, "journalctl", "-k", "--no-pager", "-n", "300", "-o", "short-iso")
	lines := make([]string, 0, 8)
	for _, line := range strings.Split(string(output), "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "avc:") || strings.Contains(lower, "selinux") {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return "<none>"
	}
	return setupDiagnosticBoundedText([]byte(strings.Join(lines, "\n")), 6*1024)
}

func setupDiagnosticValidationSnapshot() string {
	output, _ := setupDiagnosticRunCommand(15*time.Second, "/usr/libexec/justvoxel/mjust/validate-backend")
	return setupDiagnosticBoundedText(output, 12*1024)
}

func setupDiagnosticDNSStatus(host string) string {
	_, err := setupDiagnosticRunCommand(4*time.Second, "getent", "ahosts", host)
	if err != nil {
		return "failed"
	}
	return "ok"
}

func setupDiagnosticPathPresence(path string) string {
	if _, err := os.Stat(path); err == nil {
		return "present"
	} else if os.IsNotExist(err) {
		return "absent"
	}
	return "<unavailable>"
}

func setupDiagnosticCommandPresence(timeout time.Duration, name string, args ...string) string {
	_, err := setupDiagnosticRunCommand(timeout, name, args...)
	if err == nil {
		return "present"
	}
	return "absent"
}

func setupDiagnosticCommandValue(timeout time.Duration, name string, args ...string) string {
	output, err := setupDiagnosticRunCommand(timeout, name, args...)
	value := setupDiagnosticFirstLine(output)
	if err != nil && (value == "" || value == "<unavailable>") {
		return "<unavailable>"
	}
	return value
}

func setupDiagnosticRunCommand(timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return runSetupDiagnosticCommand(ctx, name, args...)
}

func setupDiagnosticBoundedText(output []byte, max int) string {
	value := strings.TrimSpace(strings.ToValidUTF8(string(output), "�"))
	if value == "" {
		return "<none>"
	}
	if len(value) > max {
		value = value[:max] + "…"
	}
	return value
}

func (s *operationStore) setupRuntimeEvidenceForOperation(operationID string, plan *adminSetupNormalizedPlan) map[string]string {
	operation, err := s.get(operationID)
	if err != nil {
		// Diagnostic sessions can collect evidence before an operation exists.
		return collectSetupRuntimeEvidence(plan)
	}
	e, err := setupInstanceRead(operationID, operation.PlanFingerprint)
	if err == nil && instanceIDPattern.MatchString(e.Record.Runtime.ID) {
		values := collectSetupRuntimeEvidence(plan, e.Record.Runtime.ID)
		values["instance_id"] = e.Record.Runtime.ID
		return values
	}
	if operation.InstanceSetup || err == nil {
		return map[string]string{"instance_id": "pending", "setup_marker_present": setupDiagnosticPathPresence(setupInstanceMarker), "config_present": setupDiagnosticPathPresence(filepath.Join(setupPolicyRoot, "justvoxel.conf"))}
	}
	return collectSetupRuntimeEvidence(plan)
}
