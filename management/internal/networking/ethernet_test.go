package networking

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/godbus/dbus/v5"
)

func validEthernetFixture() EthernetSettings {
	return EthernetSettings{Interface: "enp1s0", ProfileUUID: "12345678-1234-1234-1234-123456789abc", Method: "manual", Address: "192.0.2.20", Prefix: 24, Gateway: "192.0.2.1", DNS: []string{"192.0.2.53"}, MTU: 1500, Autoconnect: true}
}

func TestEthernetInputValidation(t *testing.T) {
	valid := validEthernetFixture()
	if err := ValidateEthernetSettings(valid); err != nil {
		t.Fatal(err)
	}
	mutations := []func(*EthernetSettings){
		func(s *EthernetSettings) { s.Interface = "eth0;reboot" },
		func(s *EthernetSettings) { s.Interface = "../../eth0" },
		func(s *EthernetSettings) { s.ProfileUUID = "/dbus/profile" },
		func(s *EthernetSettings) { s.Method = "shared" },
		func(s *EthernetSettings) { s.Address = "::1" },
		func(s *EthernetSettings) { s.Address = "999.0.0.1" },
		func(s *EthernetSettings) { s.Prefix = 33 },
		func(s *EthernetSettings) { s.Gateway = "bad" },
		func(s *EthernetSettings) { s.DNS = []string{"invalid"} },
		func(s *EthernetSettings) { s.MTU = 575 },
		func(s *EthernetSettings) { s.MTU = 9001 },
		func(s *EthernetSettings) { s.Method = "auto" },
	}
	for index, mutate := range mutations {
		candidate := valid
		mutate(&candidate)
		if err := ValidateEthernetSettings(candidate); err == nil {
			t.Fatalf("invalid case %d accepted", index)
		}
	}
	auto := valid
	auto.Method, auto.Address, auto.Gateway, auto.Prefix, auto.MTU = "auto", "", "", 0, 0
	if err := ValidateEthernetSettings(auto); err != nil {
		t.Fatal(err)
	}
}

func TestEthernetSettingsDNSData(t *testing.T) {
	base := map[string]map[string]dbus.Variant{
		"connection": {"autoconnect": dbus.MakeVariant(false)},
		"ipv4": {
			"dns":      dbus.MakeVariant([]uint32{1}),
			"dns-data": dbus.MakeVariant([]string{"198.51.100.1"}),
		},
	}
	for _, expected := range [][]string{{"192.0.2.53", "203.0.113.7"}, {}} {
		s := validEthernetFixture()
		s.DNS = expected
		if err := ValidateEthernetSettings(s); err != nil {
			t.Fatal(err)
		}
		for _, persistent := range []bool{false, true} {
			ip := ethernetSettings(base, s, persistent)["ipv4"]
			value, ok := ip["dns-data"]
			if !ok {
				t.Fatal("DNS string property missing")
			}
			dns, ok := value.Value().([]string)
			if !ok || !reflect.DeepEqual(dns, expected) {
				t.Fatalf("DNS strings = %v, want %v", value.Value(), expected)
			}
			if _, ok := ip["dns"]; ok {
				t.Fatal("legacy integer DNS property emitted")
			}
			if boolValue(ip, "ignore-auto-dns") != (len(expected) > 0) {
				t.Fatal("automatic DNS policy changed")
			}
		}
	}
}

func ethernetDBusFixture(t *testing.T, rejectReapply bool) (*Client, *map[string]map[string]dbus.Variant, *int) {
	t.Helper()
	s := validEthernetFixture()
	persistent := map[string]map[string]dbus.Variant{
		"connection":     {"uuid": dbus.MakeVariant(s.ProfileUUID), "type": dbus.MakeVariant("802-3-ethernet"), "autoconnect": dbus.MakeVariant(false)},
		"ipv4":           {"method": dbus.MakeVariant("auto")},
		"802-3-ethernet": {"mtu": dbus.MakeVariant(uint32(1500))},
	}
	applied := persistent
	version := uint64(1)
	writes := 0
	client := &Client{call: func(_ context.Context, _ dbus.ObjectPath, method string, args ...any) *dbus.Call {
		switch method {
		case managerInterface + ".GetDeviceByIpIface":
			return dbusCall(dbus.ObjectPath("/device/1"))
		case propertiesInterface + ".GetAll":
			if args[0] == deviceInterface {
				return dbusCall(map[string]dbus.Variant{"DeviceType": dbus.MakeVariant(uint32(1)), "State": dbus.MakeVariant(uint32(100)), "ActiveConnection": dbus.MakeVariant(dbus.ObjectPath("/active/1"))})
			}
			return dbusCall(map[string]dbus.Variant{"Connection": dbus.MakeVariant(dbus.ObjectPath("/profile/1"))})
		case connectionInterface + ".GetSettings":
			return dbusCall(persistent)
		case deviceInterface + ".GetAppliedConnection":
			return dbusCall(applied, version)
		case deviceInterface + ".Reapply":
			if rejectReapply {
				return &dbus.Call{Err: errors.New("unsupported change")}
			}
			if args[1] != version {
				t.Fatal("Reapply did not use the applied version")
			}
			applied = args[0].(map[string]map[string]dbus.Variant)
			version++
			return dbusCall()
		case connectionInterface + ".Update":
			writes++
			persistent = args[0].(map[string]map[string]dbus.Variant)
			return dbusCall()
		}
		return &dbus.Call{Err: errors.New("unexpected D-Bus method")}
	}}
	return client, &persistent, &writes
}

func TestEthernetStagingDoesNotWritePersistentProfile(t *testing.T) {
	client, persistent, writes := ethernetDBusFixture(t, false)
	original := *persistent
	candidate, err := client.StageEthernet(context.Background(), validEthernetFixture())
	if err != nil {
		t.Fatal(err)
	}
	if *writes != 0 || !reflect.DeepEqual(*persistent, original) {
		t.Fatal("staging replaced persistent settings")
	}
	if err := client.PersistEthernet(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	if *writes != 1 || stringValue((*persistent)["ipv4"], "method") != "manual" || !boolValue((*persistent)["connection"], "autoconnect") {
		t.Fatal("approved settings were not persisted")
	}
}

func TestEthernetUnsupportedLiveChangeNeverFallsBackToPersistence(t *testing.T) {
	client, persistent, writes := ethernetDBusFixture(t, true)
	original := *persistent
	if _, err := client.StageEthernet(context.Background(), validEthernetFixture()); err == nil {
		t.Fatal("unsupported Reapply accepted")
	}
	if *writes != 0 || !reflect.DeepEqual(*persistent, original) {
		t.Fatal("unsafe persistent-first fallback")
	}
}

func TestEthernetPersistenceRejectsConcurrentConsoleEdit(t *testing.T) {
	client, persistent, writes := ethernetDBusFixture(t, false)
	candidate, err := client.StageEthernet(context.Background(), validEthernetFixture())
	if err != nil {
		t.Fatal(err)
	}
	// Replace the map as a later GetSettings call would after a console edit.
	*persistent = ethernetSettings(*persistent, validEthernetFixture(), true)
	if err := client.PersistEthernet(context.Background(), candidate); err == nil {
		t.Fatal("concurrent persistent edit overwritten")
	}
	if *writes != 0 {
		t.Fatal("concurrent edit caused an Update")
	}
}
