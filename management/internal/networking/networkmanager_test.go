package networking

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestDeviceStateName(t *testing.T) {
	if got := DeviceStateName(100); got != "activated" {
		t.Fatalf("DeviceStateName(100) = %q, want activated", got)
	}
	if got := DeviceStateName(999); got != "unknown" {
		t.Fatalf("DeviceStateName(999) = %q, want unknown", got)
	}
}

func TestConnectivityName(t *testing.T) {
	if got := ConnectivityName(4); got != "full" {
		t.Fatalf("ConnectivityName(4) = %q, want full", got)
	}
}

func TestClassifyWiFiSecurity(t *testing.T) {
	tests := []struct {
		name     string
		flags    uint32
		wpa      uint32
		rsn      uint32
		security WiFiSecurity
		keyMgmt  string
	}{
		{name: "open", security: WiFiSecurityOpen},
		{name: "owe", rsn: apSecKeyMgmtOWE, security: WiFiSecurityOWE, keyMgmt: "owe"},
		{name: "personal", rsn: apSecKeyMgmtPSK, security: WiFiSecurityPersonal, keyMgmt: "wpa-psk"},
		{name: "wpa3 personal", rsn: apSecKeyMgmtSAE, security: WiFiSecurityWPA3Personal, keyMgmt: "sae"},
		{name: "enterprise", rsn: apSecKeyMgmt8021X, security: WiFiSecurityEnterprise, keyMgmt: "wpa-eap"},
		{name: "wep", flags: apFlagPrivacy, security: WiFiSecurityWEP, keyMgmt: "none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			security, keyMgmt := classifyWiFiSecurity(tt.flags, tt.wpa, tt.rsn)
			if security != tt.security || keyMgmt != tt.keyMgmt {
				t.Fatalf("classifyWiFiSecurity() = (%q, %q), want (%q, %q)", security, keyMgmt, tt.security, tt.keyMgmt)
			}
		})
	}
}

func TestSnapshotUsesDirectNetworkManagerDBus(t *testing.T) {
	calls := make([]string, 0, 4)
	client := &Client{call: func(_ context.Context, path dbus.ObjectPath, method string, args ...any) *dbus.Call {
		calls = append(calls, string(path)+" "+method)
		switch method {
		case propertiesInterface + ".GetAll":
			if len(args) == 1 && args[0] == managerInterface {
				return dbusCall(map[string]dbus.Variant{
					"Version":           dbus.MakeVariant("1.58.0"),
					"State":             dbus.MakeVariant(uint32(70)),
					"Connectivity":      dbus.MakeVariant(uint32(4)),
					"NetworkingEnabled": dbus.MakeVariant(true),
					"WirelessEnabled":   dbus.MakeVariant(true),
				})
			}
		case settingsInterface + ".ListConnections":
			return dbusCall([]dbus.ObjectPath{})
		case managerInterface + ".GetDevices":
			return dbusCall([]dbus.ObjectPath{})
		}
		return &dbus.Call{Err: os.ErrInvalid}
	}}

	snapshot, err := client.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.Version != "1.58.0" || !snapshot.NetworkingEnabled || !snapshot.WirelessEnabled {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "org.freedesktop.DBus.Properties.GetAll") || !strings.Contains(joined, "org.freedesktop.NetworkManager.GetDevices") {
		t.Fatalf("Snapshot() did not use expected NetworkManager D-Bus methods:\n%s", joined)
	}
}

func TestManagementModuleHasNoNMHSPDependency(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	moduleFile := filepath.Join(filepath.Dir(currentFile), "..", "..", "go.mod")
	data, err := os.ReadFile(moduleFile)
	if err != nil {
		t.Fatalf("read management go.mod: %v", err)
	}
	if strings.Contains(string(data), "home-server-project/nm-hsp") {
		t.Fatal("management module must remain independent from nm-hsp")
	}
}

func dbusCall(values ...any) *dbus.Call {
	return &dbus.Call{Body: values}
}
