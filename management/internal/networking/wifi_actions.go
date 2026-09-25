package networking

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"

	"github.com/godbus/dbus/v5"
)

var ErrWiFiProfileActive = errors.New("Wi-Fi profile is currently active")

// WiFiConnectRequest is a new persistent Wi-Fi connection. Password exists only
// for a new personal network and is cleared by ConnectWiFi before return.
type WiFiConnectRequest struct {
	Interface     string
	SSID          string
	BSSID         string
	KeyManagement string
	Password      Secret
	Hidden        bool
}

// WiFiConnectResult returns only stable appliance identifiers.
type WiFiConnectResult struct {
	ProfileUUID string
}

// SetWirelessEnabled changes NetworkManager's global Wi-Fi radio state.
func (c *Client) SetWirelessEnabled(ctx context.Context, enabled bool) error {
	if err := c.ready(); err != nil {
		return err
	}
	if err := c.call(
		ctx,
		managerPath,
		propertiesInterface+".Set",
		managerInterface,
		"WirelessEnabled",
		dbus.MakeVariant(enabled),
	).Err; err != nil {
		return fmt.Errorf("set Wi-Fi radio state: %w", err)
	}
	return nil
}

// ActivateWiFiProfile activates an existing saved Wi-Fi profile on a selected
// device using stable profile UUID and interface identifiers.
func (c *Client) ActivateWiFiProfile(ctx context.Context, interfaceName, profileUUID string) error {
	device, err := c.wifiDevicePath(ctx, interfaceName)
	if err != nil {
		return err
	}
	profile, _, err := c.wifiProfilePathByUUID(ctx, profileUUID)
	if err != nil {
		return err
	}

	var active dbus.ObjectPath
	if err := c.call(
		ctx,
		managerPath,
		managerInterface+".ActivateConnection",
		profile,
		device,
		dbus.ObjectPath("/"),
	).Store(&active); err != nil {
		return fmt.Errorf("activate saved Wi-Fi profile: %w", err)
	}
	if !validObjectPath(active) {
		return errors.New("NetworkManager returned an invalid active Wi-Fi connection")
	}
	return nil
}

// DisconnectWiFi disconnects a selected Wi-Fi device.
func (c *Client) DisconnectWiFi(ctx context.Context, interfaceName string) error {
	device, err := c.wifiDevicePath(ctx, interfaceName)
	if err != nil {
		return err
	}
	if err := c.call(ctx, device, deviceInterface+".Disconnect").Err; err != nil {
		return fmt.Errorf("disconnect Wi-Fi: %w", err)
	}
	return nil
}

// ForgetWiFiProfile deletes an inactive saved Wi-Fi profile. Active profiles
// must be disconnected and confirmed first so a "forget" action never defeats
// the checkpoint rollback guarantee.
func (c *Client) ForgetWiFiProfile(ctx context.Context, profileUUID string) error {
	profilePath, profile, err := c.wifiProfilePathByUUID(ctx, profileUUID)
	if err != nil {
		return err
	}
	snapshot, err := c.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("verify Wi-Fi profile state: %w", err)
	}
	for _, device := range snapshot.Devices {
		if device.ActiveConnection != nil && device.ActiveConnection.UUID == profile.UUID {
			return ErrWiFiProfileActive
		}
	}
	if err := c.call(ctx, profilePath, connectionInterface+".Delete").Err; err != nil {
		return fmt.Errorf("forget Wi-Fi profile: %w", err)
	}
	return nil
}

// ConnectWiFi creates a persistent Open, OWE, WPA/WPA2 Personal, or WPA3
// Personal profile and activates it. Enterprise and WEP credential creation are
// deliberately unsupported.
func (c *Client) ConnectWiFi(ctx context.Context, request WiFiConnectRequest) (WiFiConnectResult, error) {
	defer request.Password.Clear()
	if err := validateWiFiConnectRequest(request); err != nil {
		return WiFiConnectResult{}, err
	}

	device, err := c.wifiDevicePath(ctx, request.Interface)
	if err != nil {
		return WiFiConnectResult{}, err
	}

	specific := dbus.ObjectPath("/")
	if !request.Hidden && strings.TrimSpace(request.BSSID) != "" {
		specific, err = c.wifiAccessPointPath(ctx, device, request.SSID, request.BSSID, request.KeyManagement)
		if err != nil {
			return WiFiConnectResult{}, err
		}
	}

	uuid, err := newConnectionUUID()
	if err != nil {
		return WiFiConnectResult{}, fmt.Errorf("generate Wi-Fi connection UUID: %w", err)
	}
	settings := newWiFiSettings(request, uuid)
	options := map[string]dbus.Variant{"persist": dbus.MakeVariant("disk")}

	var profilePath dbus.ObjectPath
	var activePath dbus.ObjectPath
	var result map[string]dbus.Variant
	callErr := c.call(
		ctx,
		managerPath,
		managerInterface+".AddAndActivateConnection2",
		settings,
		device,
		specific,
		options,
	).Store(&profilePath, &activePath, &result)
	scrubWiFiSettingsSecret(settings)
	if callErr != nil {
		return WiFiConnectResult{}, redactSecretError(fmt.Errorf("connect to Wi-Fi: %w", callErr), request.Password)
	}
	if !validObjectPath(profilePath) || !validObjectPath(activePath) {
		return WiFiConnectResult{}, errors.New("NetworkManager returned an invalid Wi-Fi activation")
	}
	return WiFiConnectResult{ProfileUUID: uuid}, nil
}

func (c *Client) wifiDevicePath(ctx context.Context, interfaceName string) (dbus.ObjectPath, error) {
	path, err := c.devicePath(ctx, interfaceName)
	if err != nil {
		return "", err
	}
	props, err := c.getAll(ctx, path, deviceInterface)
	if err != nil {
		return "", fmt.Errorf("read Wi-Fi device: %w", err)
	}
	if deviceKindFromNM(uint32Value(props, "DeviceType")) != DeviceKindWiFi {
		return "", fmt.Errorf("interface %q is not a Wi-Fi device", interfaceName)
	}
	return path, nil
}

func (c *Client) wifiProfilePathByUUID(ctx context.Context, profileUUID string) (dbus.ObjectPath, ConnectionProfile, error) {
	profileUUID = strings.TrimSpace(profileUUID)
	if profileUUID == "" {
		return "", ConnectionProfile{}, errors.New("Wi-Fi profile UUID is required")
	}
	var paths []dbus.ObjectPath
	if err := c.call(ctx, settingsPath, settingsInterface+".ListConnections").Store(&paths); err != nil {
		return "", ConnectionProfile{}, fmt.Errorf("list NetworkManager connection profiles: %w", err)
	}
	for _, path := range paths {
		settings, err := c.connectionSettings(ctx, path)
		if err != nil {
			return "", ConnectionProfile{}, err
		}
		connection := settings["connection"]
		if stringValue(connection, "uuid") != profileUUID {
			continue
		}
		if stringValue(connection, "type") != "802-11-wireless" {
			return "", ConnectionProfile{}, errors.New("profile is not a Wi-Fi connection")
		}
		wireless := settings["802-11-wireless"]
		security := settings["802-11-wireless-security"]
		return path, ConnectionProfile{
			ID:            stringValue(connection, "id"),
			UUID:          profileUUID,
			Type:          "802-11-wireless",
			InterfaceName: stringValue(connection, "interface-name"),
			SSID:          ssidValue(wireless, "ssid"),
			KeyManagement: stringValue(security, "key-mgmt"),
		}, nil
	}
	return "", ConnectionProfile{}, fmt.Errorf("Wi-Fi profile %q was not found", profileUUID)
}

func (c *Client) wifiAccessPointPath(
	ctx context.Context,
	device dbus.ObjectPath,
	ssid, bssid, keyManagement string,
) (dbus.ObjectPath, error) {
	var paths []dbus.ObjectPath
	if err := c.call(ctx, device, wirelessInterface+".GetAllAccessPoints").Store(&paths); err != nil {
		return "", fmt.Errorf("list Wi-Fi access points: %w", err)
	}
	wantBSSID := hardwareAddress(bssid)
	for _, path := range paths {
		props, err := c.getAll(ctx, path, accessPointInterface)
		if err != nil {
			return "", fmt.Errorf("read Wi-Fi access point: %w", err)
		}
		if hardwareAddress(stringValue(props, "HwAddress")) != wantBSSID || ssidValue(props, "Ssid") != ssid {
			continue
		}
		_, detectedKeyManagement := classifyWiFiSecurity(
			uint32Value(props, "Flags"),
			uint32Value(props, "WpaFlags"),
			uint32Value(props, "RsnFlags"),
		)
		if detectedKeyManagement != keyManagement {
			return "", errors.New("Wi-Fi security changed since the last scan")
		}
		return path, nil
	}
	return "", errors.New("selected Wi-Fi access point is no longer available")
}

func validateWiFiConnectRequest(request WiFiConnectRequest) error {
	if strings.TrimSpace(request.Interface) == "" {
		return errors.New("Wi-Fi interface is required")
	}
	if err := validateSSID(request.SSID); err != nil {
		return err
	}
	switch request.KeyManagement {
	case "":
		if !request.Password.Empty() {
			return errors.New("open Wi-Fi must not contain a password")
		}
	case "owe":
		if !request.Password.Empty() {
			return errors.New("OWE Wi-Fi must not contain a password")
		}
	case "wpa-psk":
		if !validWPAPSK(request.Password) {
			return errors.New("WPA/WPA2 password must be 8-63 characters or 64 hexadecimal characters")
		}
	case "sae":
		if request.Password.Empty() {
			return errors.New("WPA3 password is required")
		}
	default:
		return fmt.Errorf("unsupported Wi-Fi security method %q", request.KeyManagement)
	}
	return nil
}

func validateSSID(ssid string) error {
	length := len([]byte(ssid))
	if length == 0 {
		return errors.New("Wi-Fi network name is required")
	}
	if length > 32 {
		return errors.New("Wi-Fi network name must be 32 bytes or fewer")
	}
	return nil
}

func validWPAPSK(password Secret) bool {
	length := password.Len()
	if length >= 8 && length <= 63 {
		return true
	}
	return length == 64 && password.IsHex()
}

func newWiFiSettings(request WiFiConnectRequest, uuid string) map[string]map[string]dbus.Variant {
	settings := map[string]map[string]dbus.Variant{
		"connection": {
			"id":          dbus.MakeVariant(request.SSID),
			"uuid":        dbus.MakeVariant(uuid),
			"type":        dbus.MakeVariant("802-11-wireless"),
			"autoconnect": dbus.MakeVariant(true),
		},
		"802-11-wireless": {
			"ssid":   dbus.MakeVariant([]byte(request.SSID)),
			"mode":   dbus.MakeVariant("infrastructure"),
			"hidden": dbus.MakeVariant(request.Hidden),
		},
		"ipv4": {
			"method": dbus.MakeVariant("auto"),
		},
		"ipv6": {
			"method": dbus.MakeVariant("auto"),
		},
	}
	if request.KeyManagement != "" {
		security := map[string]dbus.Variant{"key-mgmt": dbus.MakeVariant(request.KeyManagement)}
		if request.KeyManagement == "wpa-psk" || request.KeyManagement == "sae" {
			security["psk"] = dbus.MakeVariant(request.Password.Value())
		}
		settings["802-11-wireless-security"] = security
	}
	return settings
}

func scrubWiFiSettingsSecret(settings map[string]map[string]dbus.Variant) {
	if security := settings["802-11-wireless-security"]; security != nil {
		delete(security, "psk")
	}
}

func newConnectionUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		value[0:4],
		value[4:6],
		value[6:8],
		value[8:10],
		value[10:16],
	), nil
}
