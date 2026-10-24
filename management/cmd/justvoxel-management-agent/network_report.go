package main

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Extra privacy for vendor/network journals, including URLs and bare key/value
// material. Existing Support redaction still applies to every report section.
var networkSensitivePattern = regexp.MustCompile(`(?i)nordvpn://|https?://|exchange[ _-]?token|private[ _-]?key|public[ _-]?key|setup[ _-]?key|preshared|bearer|\b[a-z0-9_+/=-]{32,}\b`)

func sanitizeNetworkOutput(data []byte) []byte {
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if networkSensitivePattern.MatchString(line) {
			lines[i] = "<network detail redacted>"
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

func (s *server) adminNetworkReport(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" || r.ContentLength != 0 {
		writeError(w, http.StatusBadRequest, "network report takes no input")
		return
	}
	if s.operations == nil {
		writeError(w, http.StatusServiceUnavailable, "network report unavailable")
		return
	}
	entry, err := s.operations.generateNetworkReport(r.Context())
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "network_report_generate", "network", err == nil, "private local diagnostic report")
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "network report could not be generated")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "log": entry})
}

func (s *operationStore) generateNetworkReport(ctx context.Context) (diagnosticLogEntry, error) {
	id, err := newOperationID()
	if err != nil {
		return diagnosticLogEntry{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 55*time.Second)
	defer cancel()
	report := supportReport{hostname: s.diagnosticHostname}
	meta, err := readReleaseMetadata()
	if err != nil {
		meta = releaseMetadata{Version: "unavailable", SourceCommit: "unavailable", ManagementAPI: "v1"}
	}
	report.section("NETWORK REPORT METADATA", []byte(fmt.Sprintf("generated_at=%s\nwebui_version=%s\nsource_commit=%s\nmanagement_api=%s\n", s.now().UTC().Format(time.RFC3339Nano), meta.Version, meta.SourceCommit, meta.ManagementAPI)))
	command := func(title, name string, args ...string) {
		sectionCtx, sectionCancel := context.WithTimeout(ctx, 3*time.Second)
		defer sectionCancel()
		data, commandErr := runSupportCommand(sectionCtx, name, args...)
		if commandErr != nil {
			data = []byte("<section unavailable>")
		}
		report.section(title, sanitizeNetworkOutput(data))
	}
	for _, unit := range []string{"NetworkManager.service", "tailscaled.service", "netbird.service", "playit.service", "nordvpnd.service", "nordvpnd.socket"} {
		command(unit+" STATE", "systemctl", "show", unit, "--no-pager", "--property=LoadState,UnitFileState,ActiveState,SubState,Result")
		if unit != "nordvpnd.socket" {
			command(unit+" CURRENT-BOOT JOURNAL", "journalctl", "-b", "-u", unit, "--no-pager", "-n", "100", "-o", "cat")
		}
	}
	command("DEVICES", "nmcli", "--terse", "--fields", "DEVICE,TYPE,STATE", "device", "status")
	command("INTERFACES AND ADDRESSES", "ip", "-brief", "address", "show")
	command("IPV4 ROUTES", "ip", "-4", "route", "show")
	command("IPV6 ROUTES", "ip", "-6", "route", "show")
	command("DNS / RESOLVED", "resolvectl", "status", "--no-pager")
	command("FAILED UNITS", "systemctl", "--failed", "--no-pager", "--no-legend", "--plain")
	for _, provider := range remoteProviders {
		// Deliberately omit identities, dashboards, tunnel destinations and raw output.
		providerCtx, providerCancel := context.WithTimeout(ctx, 4*time.Second)
		status, statusErr := remoteProviderStatus(providerCtx, provider)
		providerCancel()
		data := []byte("<provider unavailable>")
		if statusErr == nil {
			data = []byte(fmt.Sprintf("installed=%t enabled=%t active=%t configured=%t connected=%t account=%s meshnet=%s", status.Installed, status.Enabled, status.Active, status.Configured, status.Connected, status.Account, status.Meshnet))
		}
		report.section(provider.name+" SUMMARY", data)
	}
	return s.storeDiagnosticReport("network", id, []byte(report.String()))
}
