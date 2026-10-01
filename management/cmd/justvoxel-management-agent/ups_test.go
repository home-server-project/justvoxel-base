package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

func TestNormalizeUPSSource(t *testing.T) {
	cases := []struct {
		name    string
		mode    string
		host    string
		port    int
		want    upsSourceConfig
		wantErr bool
	}{
		{name: "local", mode: "local", want: upsSourceConfig{Mode: "local"}},
		{name: "remote default port", mode: "remote", host: "nut.example.test", want: upsSourceConfig{Mode: "remote", Host: "nut.example.test", Port: 3493}},
		{name: "remote ipv4", mode: "remote", host: "192.0.2.51", port: 3493, want: upsSourceConfig{Mode: "remote", Host: "192.0.2.51", Port: 3493}},
		{name: "reject url", mode: "remote", host: "http://example.test", wantErr: true},
		{name: "reject bad port", mode: "remote", host: "example.test", port: 70000, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeUPSSource(tc.mode, tc.host, tc.port)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v want %#v", got, tc.want)
			}
		})
	}
}

func TestParseUPSVariablesDropsSensitiveFields(t *testing.T) {
	values := parseUPSVariables("ups.status: OL\nups.mfr: CyberPower\nups.model: Test UPS\nups.serial: SECRET\nbattery.charge: 97\n")
	if values["ups.status"] != "OL" || values["ups.mfr"] != "CyberPower" || values["battery.charge"] != "97" {
		t.Fatalf("expected allowlisted values, got %#v", values)
	}
	if _, ok := values["ups.serial"]; ok {
		t.Fatal("serial number must never enter the normalized UPS response")
	}
}

func TestNormalizeUPSState(t *testing.T) {
	for raw, want := range map[string]string{
		"OL":        "online",
		"OB":        "on_battery",
		"OB LB":     "low_battery",
		"OL BYPASS": "bypass",
		"":          "unknown",
	} {
		if got := normalizeUPSState(raw); got != want {
			t.Fatalf("%q: got %q want %q", raw, got, want)
		}
	}
}

func TestListUPSNamesUsesRemoteTarget(t *testing.T) {
	original := runUPSC
	defer func() { runUPSC = original }()
	var got []string
	runUPSC = func(_ context.Context, args ...string) ([]byte, error) {
		got = append([]string(nil), args...)
		return []byte("ups-one\nups.two\nbad name\n"), nil
	}
	names, err := listUPSNames(context.Background(), upsSourceConfig{Mode: "remote", Host: "192.0.2.51", Port: 3493})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"-l", "192.0.2.51:3493"}) {
		t.Fatalf("unexpected upsc args: %#v", got)
	}
	if !reflect.DeepEqual(names, []string{"ups-one", "ups.two"}) {
		t.Fatalf("unexpected names: %#v", names)
	}
}

func TestUPSSourceForgetRequiresAdministratorAndHWS(t *testing.T) {
	oldRead, oldLook, oldRemove := upsReadFile, upsLookPath, upsRemoveFile
	defer func() { upsReadFile, upsLookPath, upsRemoveFile = oldRead, oldLook, oldRemove }()
	variant := "justvoxel-hws"
	upsReadFile = func(path string) ([]byte, error) {
		if path == upsVariantPath {
			return []byte(variant), nil
		}
		return nil, os.ErrNotExist
	}
	upsLookPath = func(string) (string, error) { return "/usr/bin/upsc", nil }
	removed := 0
	upsRemoveFile = func(string) error {
		removed++
		return nil
	}
	request := func(s *server) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		mux := http.NewServeMux()
		registerUPSRoutes(mux, s)
		mux.ServeHTTP(rr, authorizedRequest(http.MethodPost, "http://unix/v1/admin/ups/source/forget", ""))
		return rr
	}
	if rr := request(roleServerForTest(roleOperator)); rr.Code != http.StatusForbidden {
		t.Fatalf("operator forget = %d, want 403", rr.Code)
	}
	if removed != 0 {
		t.Fatal("operator reached source removal")
	}
	for _, value := range []string{"justvoxel-vm", "justvoxel-base"} {
		variant = value
		if rr := request(adminServerForTest()); rr.Code != http.StatusConflict {
			t.Fatalf("%s forget = %d, want 409", value, rr.Code)
		}
	}
	if removed != 0 {
		t.Fatal("non-HWS variant reached source removal")
	}
}

func TestUPSSourceForgetReturnsLocalWhenFileExistsOrIsMissing(t *testing.T) {
	oldRead, oldLook, oldRemove, oldRun := upsReadFile, upsLookPath, upsRemoveFile, runUPSC
	oldSource, oldShutdown, oldSharing, oldDir := upsSourcePath, upsShutdownPath, upsSharingPath, upsConfigDir
	oldSystemctl, oldGroup, oldActive := upsSystemctl, upsSetNUTGroup, upsServiceActive
	defer func() {
		upsReadFile, upsLookPath, upsRemoveFile, runUPSC = oldRead, oldLook, oldRemove, oldRun
		upsSourcePath, upsShutdownPath, upsSharingPath, upsConfigDir = oldSource, oldShutdown, oldSharing, oldDir
		upsSystemctl, upsSetNUTGroup, upsServiceActive = oldSystemctl, oldGroup, oldActive
	}()
	dir := t.TempDir()
	upsSourcePath = dir + "/source.json"
	upsShutdownPath = dir + "/shutdown.json"
	upsSharingPath = dir + "/sharing.json"
	upsConfigDir = dir + "/nut"
	if err := os.WriteFile(upsSourcePath, []byte(`{"mode":"remote","host":"nut.example.test","port":3493}`), 0o600); err != nil {
		t.Fatal(err)
	}
	upsReadFile = func(path string) ([]byte, error) {
		if path == upsVariantPath {
			return []byte("justvoxel-hws"), nil
		}
		return os.ReadFile(path)
	}
	upsLookPath = func(string) (string, error) { return "/usr/bin/upsc", nil }
	upsRemoveFile = os.Remove
	upsSystemctl = func(context.Context, ...string) error { return nil }
	upsSetNUTGroup = func(string) error { return nil }
	upsServiceActive = func(context.Context, string) bool { return false }
	runUPSC = func(context.Context, ...string) ([]byte, error) { return nil, errors.New("no local UPS") }
	mux := http.NewServeMux()
	registerUPSRoutes(mux, adminServerForTest())
	for i := 0; i < 2; i++ {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, authorizedRequest(http.MethodPost, "http://unix/v1/admin/ups/source/forget", ""))
		if rr.Code != http.StatusOK {
			t.Fatalf("forget attempt %d = %d: %s", i, rr.Code, rr.Body.String())
		}
		var status upsStatusView
		if err := json.Unmarshal(rr.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if !status.Available || status.Source.Mode != "local" || status.Source.Host != "" {
			t.Fatalf("forget attempt %d returned %#v", i, status)
		}
	}
}

func TestUPSCapabilityRequiresHWSAndClient(t *testing.T) {
	originalRead := upsReadFile
	originalLook := upsLookPath
	originalStat := upsStat
	defer func() {
		upsReadFile = originalRead
		upsLookPath = originalLook
		upsStat = originalStat
	}()

	upsReadFile = func(path string) ([]byte, error) {
		if path == upsVariantPath {
			return []byte("justvoxel-hws\n"), nil
		}
		return nil, os.ErrNotExist
	}
	upsLookPath = func(name string) (string, error) {
		switch name {
		case "upsc", "nut-scanner":
			return "/usr/bin/" + name, nil
		default:
			return "", os.ErrNotExist
		}
	}
	upsStat = func(name string) (os.FileInfo, error) {
		if name == upsLibUSBPath {
			return nil, nil
		}
		return nil, os.ErrNotExist
	}

	available, setup, variant := upsCapability()
	if !available || !setup || variant != "justvoxel-hws" {
		t.Fatalf("unexpected capability: available=%v setup=%v variant=%q", available, setup, variant)
	}

	upsReadFile = func(path string) ([]byte, error) {
		if path == upsVariantPath {
			return []byte("justvoxel-vm\n"), nil
		}
		return nil, os.ErrNotExist
	}
	available, setup, _ = upsCapability()
	if available || setup {
		t.Fatal("VM variant must not expose UPS capability")
	}
}
