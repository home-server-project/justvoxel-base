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

func TestCheckpointLifecycleUsesNetworkManagerDBus(t *testing.T) {
	var createdDevices []dbus.ObjectPath
	var createdTimeout uint32
	var createdFlags uint32
	var destroyed dbus.ObjectPath
	var rolledBack dbus.ObjectPath

	client := &Client{call: func(_ context.Context, path dbus.ObjectPath, method string, args ...any) *dbus.Call {
		switch method {
		case managerInterface + ".GetDeviceByIpIface":
			interfaceName, _ := args[0].(string)
			return dbusCall(dbus.ObjectPath("/org/freedesktop/NetworkManager/Devices/" + interfaceName))
		case managerInterface + ".CheckpointCreate":
			createdDevices, _ = args[0].([]dbus.ObjectPath)
			createdTimeout, _ = args[1].(uint32)
			createdFlags, _ = args[2].(uint32)
			return dbusCall(dbus.ObjectPath("/org/freedesktop/NetworkManager/Checkpoint/1"))
		case managerInterface + ".CheckpointDestroy":
			destroyed, _ = args[0].(dbus.ObjectPath)
			return dbusCall()
		case managerInterface + ".CheckpointRollback":
			rolledBack, _ = args[0].(dbus.ObjectPath)
			return dbusCall(map[string]uint32{
				"/org/freedesktop/NetworkManager/Devices/enp1s0": 0,
			})
		default:
			return &dbus.Call{Err: os.ErrInvalid}
		}
	}}

	checkpoint, err := client.CreateCheckpoint(context.Background(), []string{"enp1s0"}, 90)
	if err != nil {
		t.Fatalf("CreateCheckpoint() error = %v", err)
	}
	if checkpoint.Handle != "/org/freedesktop/NetworkManager/Checkpoint/1" || checkpoint.RollbackTimeoutSeconds != 90 {
		t.Fatalf("unexpected checkpoint: %+v", checkpoint)
	}
	if len(createdDevices) != 1 || string(createdDevices[0]) != "/org/freedesktop/NetworkManager/Devices/enp1s0" {
		t.Fatalf("unexpected checkpoint devices: %+v", createdDevices)
	}
	if createdTimeout != 90 || createdFlags != 0 {
		t.Fatalf("CheckpointCreate args timeout=%d flags=%d", createdTimeout, createdFlags)
	}

	results, err := client.RollbackCheckpoint(context.Background(), checkpoint)
	if err != nil {
		t.Fatalf("RollbackCheckpoint() error = %v", err)
	}
	if results["enp1s0"] != RollbackResultOK {
		t.Fatalf("unexpected rollback results: %+v", results)
	}
	if rolledBack != dbus.ObjectPath(checkpoint.Handle) {
		t.Fatalf("rollback used %q, want %q", rolledBack, checkpoint.Handle)
	}

	if err := client.DestroyCheckpoint(context.Background(), checkpoint); err != nil {
		t.Fatalf("DestroyCheckpoint() error = %v", err)
	}
	if destroyed != dbus.ObjectPath(checkpoint.Handle) {
		t.Fatalf("destroy used %q, want %q", destroyed, checkpoint.Handle)
	}
}

func TestCreateCheckpointRequiresExplicitInterfacesAndTimeout(t *testing.T) {
	client := &Client{call: func(_ context.Context, _ dbus.ObjectPath, _ string, _ ...any) *dbus.Call {
		return &dbus.Call{Err: os.ErrInvalid}
	}}
	if _, err := client.CreateCheckpoint(context.Background(), nil, 90); err == nil {
		t.Fatal("CreateCheckpoint() accepted all-device checkpoint")
	}
	if _, err := client.CreateCheckpoint(context.Background(), []string{"enp1s0"}, 0); err == nil {
		t.Fatal("CreateCheckpoint() accepted infinite rollback timeout")
	}
}

func TestRollbackResultName(t *testing.T) {
	tests := map[uint32]RollbackResult{
		0:   RollbackResultOK,
		1:   RollbackResultNoDevice,
		2:   RollbackResultDeviceUnmanaged,
		3:   RollbackResultFailed,
		999: RollbackResultUnknown,
	}
	for value, want := range tests {
		if got := rollbackResultName(value); got != want {
			t.Fatalf("rollbackResultName(%d) = %q, want %q", value, got, want)
		}
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
