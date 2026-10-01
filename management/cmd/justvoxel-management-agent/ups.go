package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	upsVariantPath = "/usr/lib/justvoxel/variant"
	upsLibUSBPath  = "/usr/lib64/libusb-1.0.so"
	defaultNUTPort = 3493
	maxUPSDevices  = 8
)

var upsSourcePath = "/etc/justvoxel/ups-source.json"

type upsSourceConfig struct {
	Mode       string `json:"mode"`
	Host       string `json:"host,omitempty"`
	Port       int    `json:"port,omitempty"`
	UPSName    string `json:"ups_name,omitempty"`
	Driver     string `json:"driver,omitempty"`
	DevicePort string `json:"device_port,omitempty"`
}

type upsDeviceView struct {
	Name                  string   `json:"name"`
	DisplayName           string   `json:"display_name"`
	State                 string   `json:"state"`
	Manufacturer          string   `json:"manufacturer,omitempty"`
	Model                 string   `json:"model,omitempty"`
	BatteryCharge         *float64 `json:"battery_charge,omitempty"`
	BatteryRuntimeSeconds *int64   `json:"battery_runtime_seconds,omitempty"`
	LoadPercent           *float64 `json:"load_percent,omitempty"`
	InputVoltage          *float64 `json:"input_voltage,omitempty"`
	OutputVoltage         *float64 `json:"output_voltage,omitempty"`
	InputFrequency        *float64 `json:"input_frequency,omitempty"`
	OutputFrequency       *float64 `json:"output_frequency,omitempty"`
	BatteryVoltage        *float64 `json:"battery_voltage,omitempty"`
	Temperature           *float64 `json:"temperature,omitempty"`
}

type upsStatusView struct {
	Available                    bool            `json:"available"`
	LocalSetupAvailable          bool            `json:"local_setup_available"`
	Source                       upsSourceConfig `json:"source"`
	Monitoring                   bool            `json:"monitoring"`
	Devices                      []upsDeviceView `json:"devices"`
	Message                      string          `json:"message,omitempty"`
	NUTMode                      string          `json:"nut_mode"`
	ProtectionEnabled            bool            `json:"protection_enabled"`
	ShutdownDelaySeconds         int             `json:"shutdown_delay_seconds"`
	MonitorCredentialsConfigured bool            `json:"monitor_credentials_configured"`
	SharingEnabled               bool            `json:"sharing_enabled"`
	SharingListenAddress         string          `json:"sharing_listen_address,omitempty"`
	SharingListenPort            int             `json:"sharing_listen_port,omitempty"`
	SharingCredentialsConfigured bool            `json:"sharing_credentials_configured"`
	MonitorServiceActive         bool            `json:"monitor_service_active"`
	ServerServiceActive          bool            `json:"server_service_active"`
	DriverServiceActive          bool            `json:"driver_service_active"`
}

var runUPSC = func(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "upsc", args...).CombinedOutput()
}

var upsLookPath = exec.LookPath
var upsReadFile = os.ReadFile
var upsStat = os.Stat
var upsRemoveFile = os.Remove

func registerUPSRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/ups", s.upsStatus)
	mux.HandleFunc("POST /v1/admin/ups/source", s.upsSourceSave)
	mux.HandleFunc("POST /v1/admin/ups/source/forget", s.upsSourceForget)
	mux.HandleFunc("POST /v1/admin/ups/shutdown", s.upsShutdownSave)
	mux.HandleFunc("POST /v1/admin/ups/sharing", s.upsSharingSave)
}

func (s *server) upsSourceForget(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	available, _, _ := upsCapability()
	if !available {
		writeError(w, http.StatusConflict, "UPS monitoring is unavailable on this JustVoxel variant")
		return
	}
	if err := forgetUPSSource(r.Context()); err != nil {
		if s.store != nil {
			_ = s.store.recordAuditEvent(actor, "ups_source_forget", "local", false, "remove failed")
		}
		writeError(w, http.StatusInternalServerError, "UPS source could not be forgotten")
		return
	}
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "ups_source_forget", "local", true, "source forgotten")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, collectUPSStatus(ctx))
}

func (s *server) upsStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireReadAccess(w, r); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, collectUPSStatus(ctx))
}

func (s *server) upsSourceSave(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	available, _, _ := upsCapability()
	if !available {
		writeError(w, http.StatusConflict, "UPS monitoring is unavailable on this JustVoxel variant")
		return
	}

	var request struct {
		Mode       string `json:"mode"`
		Host       string `json:"host"`
		Port       int    `json:"port"`
		UPSName    string `json:"ups_name"`
		Driver     string `json:"driver"`
		DevicePort string `json:"device_port"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	source, err := normalizeUPSSource(request.Mode, request.Host, request.Port)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := extendUPSSource(&source, request.UPSName, request.Driver, request.DevicePort); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if source.Mode == "remote" && source.UPSName == "" {
		previous, readErr := readUPSSource()
		if readErr != nil {
			writeError(w, http.StatusConflict, "UPS source configuration is invalid")
			return
		}
		if previous.Mode == "remote" && previous.Host == source.Host && previous.Port == source.Port {
			source.UPSName = previous.UPSName
		}
	}
	if source.Mode == "local" && source.Driver == "" {
		previous, readErr := readUPSSource()
		if readErr != nil {
			writeError(w, http.StatusConflict, "UPS source configuration is invalid")
			return
		}
		if previous.Mode == "local" {
			source.UPSName, source.Driver, source.DevicePort = previous.UPSName, previous.Driver, previous.DevicePort
		}
	}

	if source.Mode == "remote" {
		ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
		names, listErr := listUPSNames(ctx, source)
		cancel()
		if listErr != nil || len(names) == 0 {
			if s.store != nil {
				_ = s.store.recordAuditEvent(actor, "ups_source_change", source.Host, false, "remote verification failed")
			}
			writeError(w, http.StatusBadRequest, "Remote NUT server did not report a UPS")
			return
		}
		if source.UPSName == "" && len(names) == 1 {
			source.UPSName = names[0]
		}
		if source.UPSName != "" {
			found := false
			for _, name := range names {
				if name == source.UPSName {
					found = true
					break
				}
			}
			if !found {
				writeError(w, http.StatusBadRequest, "Selected UPS was not reported by the remote server")
				return
			}
		}
	}

	if err := saveUPSSource(r.Context(), source); err != nil {
		if s.store != nil {
			_ = s.store.recordAuditEvent(actor, "ups_source_change", source.Mode, false, "write failed")
		}
		writeError(w, http.StatusInternalServerError, "UPS source could not be saved")
		return
	}
	if s.store != nil {
		target := source.Mode
		if source.Mode == "remote" {
			target = source.Host
		}
		_ = s.store.recordAuditEvent(actor, "ups_source_change", target, true, source.Mode)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, collectUPSStatus(ctx))
}

func upsCapability() (available, localSetupAvailable bool, variant string) {
	data, _ := upsReadFile(upsVariantPath)
	variant = strings.TrimSpace(string(data))
	if variant != "justvoxel-hws" {
		return false, false, variant
	}
	if _, err := upsLookPath("upsc"); err != nil {
		return false, false, variant
	}
	available = true
	if _, err := upsLookPath("nut-scanner"); err == nil {
		if _, statErr := upsStat(upsLibUSBPath); statErr == nil {
			localSetupAvailable = true
		}
	}
	return available, localSetupAvailable, variant
}

func collectUPSStatus(ctx context.Context) upsStatusView {
	available, localSetup, _ := upsCapability()
	view := upsStatusView{
		Available:           available,
		LocalSetupAvailable: localSetup,
		Source:              upsSourceConfig{Mode: "local"},
		Devices:             []upsDeviceView{},
	}
	if !available {
		return view
	}

	source, err := readUPSSource()
	if err != nil {
		view.Message = "UPS source configuration is invalid"
		return view
	}
	view.Source = source
	fillUPSNativeStatus(ctx, &view)
	if view.Message != "" {
		return view
	}

	names, err := listUPSNames(ctx, source)
	if err != nil {
		if source.Mode == "remote" {
			view.Message = "Remote NUT server is unavailable"
		} else {
			view.Message = "No local UPS is configured"
		}
		return view
	}
	if len(names) == 0 {
		view.Message = "No UPS devices were reported by the configured source"
		return view
	}
	if len(names) > maxUPSDevices {
		names = names[:maxUPSDevices]
	}

	for _, name := range names {
		device, err := readUPSDevice(ctx, source, name)
		if err != nil {
			continue
		}
		view.Devices = append(view.Devices, device)
	}
	view.Monitoring = len(view.Devices) > 0
	if !view.Monitoring {
		view.Message = "UPS devices were discovered but status could not be read"
	}
	return view
}

func readUPSSource() (upsSourceConfig, error) {
	data, err := upsReadFile(upsSourcePath)
	if errors.Is(err, os.ErrNotExist) {
		return upsSourceConfig{Mode: "local"}, nil
	}
	if err != nil {
		return upsSourceConfig{}, err
	}
	var stored upsSourceConfig
	if err := strictUPSJSON(data, &stored); err != nil {
		return upsSourceConfig{}, err
	}
	source, err := normalizeUPSSource(stored.Mode, stored.Host, stored.Port)
	if err != nil {
		return upsSourceConfig{}, err
	}
	if err := extendUPSSource(&source, stored.UPSName, stored.Driver, stored.DevicePort); err != nil {
		return upsSourceConfig{}, err
	}
	if stored != source {
		return upsSourceConfig{}, errors.New("UPS source configuration is not canonical")
	}
	return source, nil
}

func writeUPSSource(source upsSourceConfig) error {
	if err := os.MkdirAll(filepath.Dir(upsSourcePath), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(source)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWrite(upsSourcePath, data, 0o600)
}

func normalizeUPSSource(mode, host string, port int) (upsSourceConfig, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "", "local":
		return upsSourceConfig{Mode: "local"}, nil
	case "remote":
		host = strings.TrimSpace(host)
		if !validNUTHost(host) {
			return upsSourceConfig{}, errors.New("Remote NUT host is invalid")
		}
		if port == 0 {
			port = defaultNUTPort
		}
		if port < 1 || port > 65535 {
			return upsSourceConfig{}, errors.New("Remote NUT port is invalid")
		}
		return upsSourceConfig{Mode: "remote", Host: host, Port: port}, nil
	default:
		return upsSourceConfig{}, errors.New("UPS source mode is invalid")
	}
}

func validNUTHost(host string) bool {
	if host == "" || len(host) > 253 || strings.ContainsAny(host, "/\\@ \t\r\n") {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func nutSourceTarget(source upsSourceConfig) string {
	if source.Mode != "remote" {
		return ""
	}
	return net.JoinHostPort(source.Host, strconv.Itoa(source.Port))
}

func listUPSNames(ctx context.Context, source upsSourceConfig) ([]string, error) {
	args := []string{"-l"}
	if target := nutSourceTarget(source); target != "" {
		args = append(args, target)
	}
	output, err := runUPSC(ctx, args...)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(string(output), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || !validUPSName(name) {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

func validUPSName(name string) bool {
	if name == "" || len(name) > 80 {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func readUPSDevice(ctx context.Context, source upsSourceConfig, name string) (upsDeviceView, error) {
	target := name
	if remote := nutSourceTarget(source); remote != "" {
		target += "@" + remote
	}
	output, err := runUPSC(ctx, target)
	if err != nil {
		return upsDeviceView{}, err
	}
	values := parseUPSVariables(string(output))
	device := upsDeviceView{
		Name:         name,
		State:        normalizeUPSState(values["ups.status"]),
		Manufacturer: values["ups.mfr"],
		Model:        values["ups.model"],
	}
	device.DisplayName = strings.TrimSpace(strings.Join([]string{device.Manufacturer, device.Model}, " "))
	if device.DisplayName == "" {
		device.DisplayName = name
	}
	device.BatteryCharge = parseUPSFloat(values["battery.charge"])
	device.BatteryRuntimeSeconds = parseUPSInt(values["battery.runtime"])
	device.LoadPercent = parseUPSFloat(values["ups.load"])
	device.InputVoltage = parseUPSFloat(values["input.voltage"])
	device.OutputVoltage = parseUPSFloat(values["output.voltage"])
	device.InputFrequency = parseUPSFloat(values["input.frequency"])
	device.OutputFrequency = parseUPSFloat(values["output.frequency"])
	device.BatteryVoltage = parseUPSFloat(values["battery.voltage"])
	device.Temperature = parseUPSFloat(valueOr(values["ups.temperature"], values["battery.temperature"]))
	return device, nil
}

func parseUPSVariables(output string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "ups.status", "ups.mfr", "ups.model", "battery.charge", "battery.runtime",
			"ups.load", "input.voltage", "output.voltage", "input.frequency",
			"output.frequency", "battery.voltage", "ups.temperature", "battery.temperature":
			values[key] = value
		}
	}
	return values
}

func normalizeUPSState(raw string) string {
	tokens := strings.Fields(strings.ToUpper(raw))
	has := func(want string) bool {
		for _, token := range tokens {
			if token == want {
				return true
			}
		}
		return false
	}
	switch {
	case has("LB"):
		return "low_battery"
	case has("OB"):
		return "on_battery"
	case has("BYPASS"):
		return "bypass"
	case has("OL"):
		return "online"
	default:
		return "unknown"
	}
}

func parseUPSFloat(value string) *float64 {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return nil
	}
	return &number
}

func parseUPSInt(value string) *int64 {
	number := parseUPSFloat(value)
	if number == nil {
		return nil
	}
	rounded := int64(*number)
	return &rounded
}

func upsSourceLabel(source upsSourceConfig) string {
	if source.Mode == "remote" {
		return fmt.Sprintf("%s:%d", source.Host, source.Port)
	}
	return "local"
}
