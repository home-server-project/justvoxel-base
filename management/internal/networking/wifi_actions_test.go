package networking

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestValidateWiFiConnectRequest(t *testing.T) {
	tests := []struct {
		name    string
		request WiFiConnectRequest
		wantErr bool
	}{
		{
			name: "open",
			request: WiFiConnectRequest{Interface: "wlp2s0", SSID: "Open", KeyManagement: ""},
		},
		{
			name: "owe",
			request: WiFiConnectRequest{Interface: "wlp2s0", SSID: "OWE", KeyManagement: "owe"},
		},
		{
			name: "wpa2",
			request: WiFiConnectRequest{Interface: "wlp2s0", SSID: "Home", KeyManagement: "wpa-psk", Password: NewSecret("password123")},
		},
		{
			name: "wpa3",
			request: WiFiConnectRequest{Interface: "wlp2s0", SSID: "Home3", KeyManagement: "sae", Password: NewSecret("password123")},
		},
		{
			name: "short psk",
			request: WiFiConnectRequest{Interface: "wlp2s0", SSID: "Home", KeyManagement: "wpa-psk", Password: NewSecret("short")},
			wantErr: true,
		},
		{
			name: "enterprise unsupported",
			request: WiFiConnectRequest{Interface: "wlp2s0", SSID: "Corp", KeyManagement: "wpa-eap"},
			wantErr: true,
		},
		{
			name: "wep unsupported",
			request: WiFiConnectRequest{Interface: "wlp2s0", SSID: "Legacy", KeyManagement: "none", Password: NewSecret("12345")},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWiFiConnectRequest(tt.request)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateWiFiConnectRequest() error = %v, wantErr %t", err, tt.wantErr)
			}
			tt.request.Password.Clear()
		})
	}
}

func TestSecretRedactsAndClearsSharedState(t *testing.T) {
	secret := NewSecret("secret-pass")
	copy := secret
	if got := secret.String(); got != redactedSecret {
		t.Fatalf("Secret.String() = %q", got)
	}
	err := redactSecretError(errors.New("failed secret-pass"), secret)
	if strings.Contains(err.Error(), "secret-pass") || !strings.Contains(err.Error(), redactedSecret) {
		t.Fatalf("redacted error = %q", err)
	}
	secret.Clear()
	if !copy.Empty() {
		t.Fatal("clearing one Secret copy did not clear shared state")
	}
}

func TestConnectWiFiUsesDirectDBusAndScrubsPSK(t *testing.T) {
	var capturedSettings map[string]map[string]dbus.Variant
	var capturedOptions map[string]dbus.Variant
	client := &Client{call: func(_ context.Context, path dbus.ObjectPath, method string, args ...any) *dbus.Call {
		switch method {
		case managerInterface + ".GetDeviceByIpIface":
			return dbusCall(dbus.ObjectPath("/org/freedesktop/NetworkManager/Devices/2"))
		case propertiesInterface + ".GetAll":
			if len(args) == 1 && args[0] == deviceInterface {
				return dbusCall(map[string]dbus.Variant{
					"DeviceType": dbus.MakeVariant(uint32(2)),
				})
			}
			if len(args) == 1 && args[0] == accessPointInterface {
				return dbusCall(map[string]dbus.Variant{
					"Ssid":       dbus.MakeVariant([]byte("Home WiFi")),
					"HwAddress":  dbus.MakeVariant("AA:BB:CC:DD:EE:FF"),
					"Flags":      dbus.MakeVariant(uint32(apFlagPrivacy)),
					"WpaFlags":   dbus.MakeVariant(uint32(0)),
					"RsnFlags":   dbus.MakeVariant(uint32(apSecKeyMgmtPSK)),
				})
			}
		case wirelessInterface + ".GetAllAccessPoints":
			return dbusCall([]dbus.ObjectPath{"/org/freedesktop/NetworkManager/AccessPoint/7"})
		case managerInterface + ".AddAndActivateConnection2":
			capturedSettings, _ = args[0].(map[string]map[string]dbus.Variant)
			capturedOptions, _ = args[3].(map[string]dbus.Variant)
			return dbusCall(
				dbus.ObjectPath("/org/freedesktop/NetworkManager/Settings/12"),
				dbus.ObjectPath("/org/freedesktop/NetworkManager/ActiveConnection/9"),
				map[string]dbus.Variant{},
			)
		}
		return &dbus.Call{Err: os.ErrInvalid}
	}}

	secret := NewSecret("password123")
	result, err := client.ConnectWiFi(context.Background(), WiFiConnectRequest{
		Interface:     "wlp2s0",
		SSID:          "Home WiFi",
		BSSID:         "AA:BB:CC:DD:EE:FF",
		KeyManagement: "wpa-psk",
		Password:      secret,
	})
	if err != nil {
		t.Fatalf("ConnectWiFi() error = %v", err)
	}
	if result.ProfileUUID == "" {
		t.Fatal("ConnectWiFi() returned empty profile UUID")
	}
	if !secret.Empty() {
		t.Fatal("ConnectWiFi() did not clear caller-visible secret state")
	}
	if capturedSettings == nil {
		t.Fatal("AddAndActivateConnection2 settings were not captured")
	}
	if _, ok := capturedSettings["802-11-wireless-security"]["psk"]; ok {
		t.Fatal("temporary settings map retained PSK after D-Bus call")
	}
	if got := stringValue(capturedSettings["802-11-wireless-security"], "key-mgmt"); got != "wpa-psk" {
		t.Fatalf("key-mgmt = %q", got)
	}
	if got := stringValue(capturedOptions, "persist"); got != "disk" {
		t.Fatalf("persist option = %q", got)
	}
}

func TestSetWirelessEnabledUsesPropertiesSet(t *testing.T) {
	var gotMethod string
	var gotEnabled bool
	client := &Client{call: func(_ context.Context, _ dbus.ObjectPath, method string, args ...any) *dbus.Call {
		gotMethod = method
		if len(args) == 3 {
			if variant, ok := args[2].(dbus.Variant); ok {
				gotEnabled, _ = variant.Value().(bool)
			}
		}
		return dbusCall()
	}}
	if err := client.SetWirelessEnabled(context.Background(), true); err != nil {
		t.Fatalf("SetWirelessEnabled() error = %v", err)
	}
	if gotMethod != propertiesInterface+".Set" || !gotEnabled {
		t.Fatalf("Properties.Set method=%q enabled=%t", gotMethod, gotEnabled)
	}
}
