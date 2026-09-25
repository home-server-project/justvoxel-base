package networking

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/godbus/dbus/v5"
)

const (
	networkManagerService = "org.freedesktop.NetworkManager"
	managerInterface      = "org.freedesktop.NetworkManager"
	deviceInterface       = "org.freedesktop.NetworkManager.Device"
	wiredInterface        = "org.freedesktop.NetworkManager.Device.Wired"
	wirelessInterface     = "org.freedesktop.NetworkManager.Device.Wireless"
	accessPointInterface  = "org.freedesktop.NetworkManager.AccessPoint"
	activeInterface       = "org.freedesktop.NetworkManager.Connection.Active"
	ip4Interface          = "org.freedesktop.NetworkManager.IP4Config"
	ip6Interface          = "org.freedesktop.NetworkManager.IP6Config"
	settingsInterface     = "org.freedesktop.NetworkManager.Settings"
	connectionInterface   = "org.freedesktop.NetworkManager.Settings.Connection"
	propertiesInterface   = "org.freedesktop.DBus.Properties"
)

var (
	managerPath  = dbus.ObjectPath("/org/freedesktop/NetworkManager")
	settingsPath = dbus.ObjectPath("/org/freedesktop/NetworkManager/Settings")
)

const (
	apFlagPrivacy uint32 = 0x00000001

	apSecKeyMgmtPSK        uint32 = 0x00000100
	apSecKeyMgmt8021X      uint32 = 0x00000200
	apSecKeyMgmtSAE        uint32 = 0x00000400
	apSecKeyMgmtOWE        uint32 = 0x00000800
	apSecKeyMgmtOWETransit uint32 = 0x00001000
	apSecKeyMgmtSuiteB192  uint32 = 0x00002000
)

type callFunc func(context.Context, dbus.ObjectPath, string, ...any) *dbus.Call

// Client talks directly to NetworkManager on the system D-Bus. It is owned by
// JustVoxel and has no dependency on nm-hsp or any other user-facing frontend.
type Client struct {
	conn *dbus.Conn
	call callFunc
}

// NewSystem creates a direct NetworkManager D-Bus client.
func NewSystem(ctx context.Context) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("connect to system D-Bus: %w", err)
	}
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("connect to system D-Bus: %w", err)
	}
	client := &Client{conn: conn}
	client.call = func(ctx context.Context, path dbus.ObjectPath, method string, args ...any) *dbus.Call {
		return conn.Object(networkManagerService, path).CallWithContext(ctx, method, 0, args...)
	}
	return client, nil
}

// Close releases the D-Bus connection.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// Snapshot returns the current non-secret NetworkManager state.
func (c *Client) Snapshot(ctx context.Context) (Snapshot, error) {
	if err := c.ready(); err != nil {
		return Snapshot{}, err
	}
	managerProps, err := c.getAll(ctx, managerPath, managerInterface)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read NetworkManager properties: %w", err)
	}
	profiles, profilesByPath, err := c.readProfiles(ctx)
	if err != nil {
		return Snapshot{}, err
	}

	var devicePaths []dbus.ObjectPath
	if err := c.call(ctx, managerPath, managerInterface+".GetDevices").Store(&devicePaths); err != nil {
		return Snapshot{}, fmt.Errorf("list NetworkManager devices: %w", err)
	}

	snapshot := Snapshot{
		Version:           stringValue(managerProps, "Version"),
		State:             uint32Value(managerProps, "State"),
		Connectivity:      uint32Value(managerProps, "Connectivity"),
		NetworkingEnabled: boolValue(managerProps, "NetworkingEnabled"),
		WirelessEnabled:   boolValue(managerProps, "WirelessEnabled"),
		Profiles:          profiles,
		Devices:           make([]Device, 0, len(devicePaths)),
	}
	for _, path := range devicePaths {
		device, err := c.readDevice(ctx, path, profilesByPath)
		if err != nil {
			return Snapshot{}, fmt.Errorf("read device %s: %w", path, err)
		}
		snapshot.Devices = append(snapshot.Devices, device)
	}
	sort.Slice(snapshot.Devices, func(i, j int) bool {
		return snapshot.Devices[i].Interface < snapshot.Devices[j].Interface
	})
	return snapshot, nil
}

// WiFiNetworks returns NetworkManager's currently known Wi-Fi networks for an
// interface name. Raw D-Bus object paths never need to cross the Management API.
func (c *Client) WiFiNetworks(ctx context.Context, interfaceName string) ([]WiFiNetwork, error) {
	path, err := c.devicePath(ctx, interfaceName)
	if err != nil {
		return nil, err
	}
	deviceProps, err := c.getAll(ctx, path, deviceInterface)
	if err != nil {
		return nil, fmt.Errorf("read Wi-Fi device: %w", err)
	}
	if deviceKindFromNM(uint32Value(deviceProps, "DeviceType")) != DeviceKindWiFi {
		return nil, fmt.Errorf("interface %q is not a Wi-Fi device", interfaceName)
	}

	wirelessProps, err := c.getAll(ctx, path, wirelessInterface)
	if err != nil {
		return nil, fmt.Errorf("read Wi-Fi device: %w", err)
	}
	activeAP := objectPathValue(wirelessProps, "ActiveAccessPoint")

	var accessPoints []dbus.ObjectPath
	if err := c.call(ctx, path, wirelessInterface+".GetAllAccessPoints").Store(&accessPoints); err != nil {
		return nil, fmt.Errorf("list Wi-Fi access points: %w", err)
	}
	profiles, _, err := c.readProfiles(ctx)
	if err != nil {
		return nil, err
	}
	known := make(map[string]string)
	for _, profile := range profiles {
		if profile.Type == "802-11-wireless" && profile.SSID != "" {
			key := profile.SSID + "\x00" + profile.KeyManagement
			if _, exists := known[key]; !exists {
				known[key] = profile.UUID
			}
		}
	}

	byNetwork := make(map[string]WiFiNetwork)
	for _, accessPoint := range accessPoints {
		props, err := c.getAll(ctx, accessPoint, accessPointInterface)
		if err != nil {
			return nil, fmt.Errorf("read Wi-Fi access point %s: %w", accessPoint, err)
		}
		ssid := ssidValue(props, "Ssid")
		security, keyManagement := classifyWiFiSecurity(
			uint32Value(props, "Flags"),
			uint32Value(props, "WpaFlags"),
			uint32Value(props, "RsnFlags"),
		)
		network := WiFiNetwork{
			SSID:           ssid,
			BSSID:          stringValue(props, "HwAddress"),
			Strength:       byteValue(props, "Strength"),
			FrequencyMHz:   uint32Value(props, "Frequency"),
			MaxBitrateKbps: uint32Value(props, "MaxBitrate"),
			Security:       security,
			KeyManagement: keyManagement,
			ProfileUUID:   known[ssid+"\x00"+keyManagement],
			Hidden:        ssid == "",
			Known:         known[ssid+"\x00"+keyManagement] != "",
			Active:        validObjectPath(activeAP) && accessPoint == activeAP,
		}
		key := ssid + "\x00" + string(security)
		if ssid == "" {
			key = string(accessPoint)
		}
		current, exists := byNetwork[key]
		if !exists || network.Active || (!current.Active && network.Strength > current.Strength) {
			byNetwork[key] = network
		}
	}

	networks := make([]WiFiNetwork, 0, len(byNetwork))
	for _, network := range byNetwork {
		networks = append(networks, network)
	}
	sort.SliceStable(networks, func(i, j int) bool {
		if networks[i].Active != networks[j].Active {
			return networks[i].Active
		}
		if networks[i].Known != networks[j].Known {
			return networks[i].Known
		}
		if networks[i].Strength != networks[j].Strength {
			return networks[i].Strength > networks[j].Strength
		}
		return networks[i].SSID < networks[j].SSID
	})
	return networks, nil
}

// CreateCheckpoint asks NetworkManager to preserve the current state of the
// explicitly named devices. The returned D-Bus handle is internal to the
// Management Agent and must not cross the appliance API boundary.
func (c *Client) CreateCheckpoint(ctx context.Context, interfaces []string, rollbackTimeoutSeconds uint32) (Checkpoint, error) {
	if err := c.ready(); err != nil {
		return Checkpoint{}, err
	}
	if rollbackTimeoutSeconds == 0 {
		return Checkpoint{}, errors.New("checkpoint rollback timeout must be greater than zero")
	}
	if len(interfaces) == 0 {
		return Checkpoint{}, errors.New("at least one network interface is required")
	}

	seen := make(map[string]struct{}, len(interfaces))
	devices := make([]CheckpointDevice, 0, len(interfaces))
	paths := make([]dbus.ObjectPath, 0, len(interfaces))
	for _, raw := range interfaces {
		interfaceName := strings.TrimSpace(raw)
		if interfaceName == "" {
			return Checkpoint{}, errors.New("network interface is required")
		}
		if _, ok := seen[interfaceName]; ok {
			continue
		}
		seen[interfaceName] = struct{}{}

		path, err := c.devicePath(ctx, interfaceName)
		if err != nil {
			return Checkpoint{}, err
		}
		paths = append(paths, path)
		devices = append(devices, CheckpointDevice{Interface: interfaceName, Handle: string(path)})
	}
	sort.Slice(devices, func(i, j int) bool {
		return devices[i].Interface < devices[j].Interface
	})

	var checkpointPath dbus.ObjectPath
	if err := c.call(
		ctx,
		managerPath,
		managerInterface+".CheckpointCreate",
		paths,
		rollbackTimeoutSeconds,
		uint32(0),
	).Store(&checkpointPath); err != nil {
		return Checkpoint{}, fmt.Errorf("create NetworkManager checkpoint: %w", err)
	}
	if !validObjectPath(checkpointPath) {
		return Checkpoint{}, errors.New("NetworkManager returned an invalid checkpoint")
	}
	return Checkpoint{
		Handle:                 string(checkpointPath),
		Devices:                devices,
		RollbackTimeoutSeconds: rollbackTimeoutSeconds,
	}, nil
}

// DestroyCheckpoint confirms the current networking state by destroying the
// rollback checkpoint before its timeout expires.
func (c *Client) DestroyCheckpoint(ctx context.Context, checkpoint Checkpoint) error {
	if err := c.ready(); err != nil {
		return err
	}
	path := dbus.ObjectPath(strings.TrimSpace(checkpoint.Handle))
	if !validObjectPath(path) {
		return errors.New("invalid NetworkManager checkpoint")
	}
	if err := c.call(ctx, managerPath, managerInterface+".CheckpointDestroy", path).Err; err != nil {
		return fmt.Errorf("destroy NetworkManager checkpoint: %w", err)
	}
	return nil
}

// RollbackCheckpoint restores the checkpoint immediately. Results are keyed by
// appliance interface name so raw D-Bus paths never escape the backend.
func (c *Client) RollbackCheckpoint(ctx context.Context, checkpoint Checkpoint) (map[string]RollbackResult, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	path := dbus.ObjectPath(strings.TrimSpace(checkpoint.Handle))
	if !validObjectPath(path) {
		return nil, errors.New("invalid NetworkManager checkpoint")
	}

	var raw map[string]uint32
	if err := c.call(ctx, managerPath, managerInterface+".CheckpointRollback", path).Store(&raw); err != nil {
		return nil, fmt.Errorf("rollback NetworkManager checkpoint: %w", err)
	}

	interfaceByHandle := make(map[string]string, len(checkpoint.Devices))
	for _, device := range checkpoint.Devices {
		interfaceByHandle[device.Handle] = device.Interface
	}
	results := make(map[string]RollbackResult, len(checkpoint.Devices))
	for handle, result := range raw {
		if interfaceName, ok := interfaceByHandle[handle]; ok {
			results[interfaceName] = rollbackResultName(result)
		}
	}
	for _, device := range checkpoint.Devices {
		if _, ok := results[device.Interface]; !ok {
			results[device.Interface] = RollbackResultUnknown
		}
	}
	return results, nil
}

// RequestWiFiScan asks NetworkManager to refresh its access-point list.
func (c *Client) RequestWiFiScan(ctx context.Context, interfaceName string) error {
	path, err := c.devicePath(ctx, interfaceName)
	if err != nil {
		return err
	}
	deviceProps, err := c.getAll(ctx, path, deviceInterface)
	if err != nil {
		return fmt.Errorf("read Wi-Fi device: %w", err)
	}
	if deviceKindFromNM(uint32Value(deviceProps, "DeviceType")) != DeviceKindWiFi {
		return fmt.Errorf("interface %q is not a Wi-Fi device", interfaceName)
	}
	if err := c.call(ctx, path, wirelessInterface+".RequestScan", map[string]dbus.Variant{}).Err; err != nil {
		return fmt.Errorf("request Wi-Fi scan: %w", err)
	}
	return nil
}

func (c *Client) ready() error {
	if c == nil || c.call == nil {
		return errors.New("NetworkManager client is not initialized")
	}
	return nil
}

func (c *Client) devicePath(ctx context.Context, interfaceName string) (dbus.ObjectPath, error) {
	if err := c.ready(); err != nil {
		return "", err
	}
	interfaceName = strings.TrimSpace(interfaceName)
	if interfaceName == "" {
		return "", errors.New("network interface is required")
	}
	var path dbus.ObjectPath
	if err := c.call(ctx, managerPath, managerInterface+".GetDeviceByIpIface", interfaceName).Store(&path); err != nil {
		return "", fmt.Errorf("find network interface %q: %w", interfaceName, err)
	}
	if !validObjectPath(path) {
		return "", fmt.Errorf("network interface %q was not found", interfaceName)
	}
	return path, nil
}

func (c *Client) readProfiles(ctx context.Context) ([]ConnectionProfile, map[dbus.ObjectPath]ConnectionProfile, error) {
	var paths []dbus.ObjectPath
	if err := c.call(ctx, settingsPath, settingsInterface+".ListConnections").Store(&paths); err != nil {
		return nil, nil, fmt.Errorf("list NetworkManager connection profiles: %w", err)
	}
	profiles := make([]ConnectionProfile, 0, len(paths))
	byPath := make(map[dbus.ObjectPath]ConnectionProfile, len(paths))
	for _, path := range paths {
		settings, err := c.connectionSettings(ctx, path)
		if err != nil {
			return nil, nil, err
		}
		connection := settings["connection"]
		wireless := settings["802-11-wireless"]
		security := settings["802-11-wireless-security"]
		ipv4 := settings["ipv4"]
		ipv6 := settings["ipv6"]
		profile := ConnectionProfile{
			ID:                  stringValue(connection, "id"),
			UUID:                stringValue(connection, "uuid"),
			Type:                stringValue(connection, "type"),
			InterfaceName:       stringValue(connection, "interface-name"),
			Autoconnect:         boolValueDefault(connection, "autoconnect", true),
			AutoconnectPriority: int32Value(connection, "autoconnect-priority"),
			SSID:                ssidValue(wireless, "ssid"),
			Hidden:              boolValue(wireless, "hidden"),
			KeyManagement:       stringValue(security, "key-mgmt"),
			IPv4Method:          stringValue(ipv4, "method"),
			IPv6Method:          stringValue(ipv6, "method"),
		}
		profiles = append(profiles, profile)
		byPath[path] = profile
	}
	sort.Slice(profiles, func(i, j int) bool {
		if profiles[i].ID == profiles[j].ID {
			return profiles[i].UUID < profiles[j].UUID
		}
		return profiles[i].ID < profiles[j].ID
	})
	return profiles, byPath, nil
}

func (c *Client) connectionSettings(ctx context.Context, path dbus.ObjectPath) (map[string]map[string]dbus.Variant, error) {
	var settings map[string]map[string]dbus.Variant
	if err := c.call(ctx, path, connectionInterface+".GetSettings").Store(&settings); err != nil {
		return nil, fmt.Errorf("read connection profile %s: %w", path, err)
	}
	return settings, nil
}

func (c *Client) readDevice(ctx context.Context, path dbus.ObjectPath, profiles map[dbus.ObjectPath]ConnectionProfile) (Device, error) {
	props, err := c.getAll(ctx, path, deviceInterface)
	if err != nil {
		return Device{}, err
	}
	device := Device{
		Interface:       stringValue(props, "Interface"),
		IPInterface:     stringValue(props, "IpInterface"),
		DeviceType:      uint32Value(props, "DeviceType"),
		State:           uint32Value(props, "State"),
		Managed:         boolValue(props, "Managed"),
		HardwareAddress: hardwareAddress(stringValue(props, "HwAddress")),
		MTU:             uint32Value(props, "Mtu"),
	}
	device.Kind = deviceKindFromNM(device.DeviceType)

	if configPath := objectPathValue(props, "Ip4Config"); validObjectPath(configPath) {
		config, err := c.readIPConfig(ctx, configPath, ip4Interface)
		if err != nil {
			return Device{}, fmt.Errorf("read IPv4 configuration: %w", err)
		}
		device.IPv4 = config
	}
	if configPath := objectPathValue(props, "Ip6Config"); validObjectPath(configPath) {
		config, err := c.readIPConfig(ctx, configPath, ip6Interface)
		if err != nil {
			return Device{}, fmt.Errorf("read IPv6 configuration: %w", err)
		}
		device.IPv6 = config
	}
	if activePath := objectPathValue(props, "ActiveConnection"); validObjectPath(activePath) {
		active, err := c.readActiveProfile(ctx, activePath, profiles)
		if err != nil {
			return Device{}, err
		}
		device.ActiveConnection = active
	}
	for _, profilePath := range objectPathSliceValue(props, "AvailableConnections") {
		if profile, ok := profiles[profilePath]; ok && profile.UUID != "" {
			device.AvailableProfileUUIDs = append(device.AvailableProfileUUIDs, profile.UUID)
		}
	}
	sort.Strings(device.AvailableProfileUUIDs)

	switch device.Kind {
	case DeviceKindEthernet:
		if wired, err := c.getAll(ctx, path, wiredInterface); err == nil {
			carrier := boolValue(wired, "Carrier")
			device.Carrier = &carrier
			device.SpeedMbps = uint32Value(wired, "Speed")
		}
	case DeviceKindWiFi:
		if wireless, err := c.getAll(ctx, path, wirelessInterface); err == nil {
			state := &WirelessState{BitrateKbps: uint32Value(wireless, "Bitrate")}
			if apPath := objectPathValue(wireless, "ActiveAccessPoint"); validObjectPath(apPath) {
				if ap, err := c.getAll(ctx, apPath, accessPointInterface); err == nil {
					state.SSID = ssidValue(ap, "Ssid")
					state.Signal = byteValue(ap, "Strength")
				}
			}
			device.Wireless = state
		}
	}
	return device, nil
}

func (c *Client) readActiveProfile(ctx context.Context, path dbus.ObjectPath, profiles map[dbus.ObjectPath]ConnectionProfile) (*ConnectionProfile, error) {
	props, err := c.getAll(ctx, path, activeInterface)
	if err != nil {
		return nil, fmt.Errorf("read active connection: %w", err)
	}
	settingsPath := objectPathValue(props, "Connection")
	if profile, ok := profiles[settingsPath]; ok {
		copy := profile
		return &copy, nil
	}
	profile := ConnectionProfile{
		ID:   stringValue(props, "Id"),
		UUID: stringValue(props, "Uuid"),
		Type: stringValue(props, "Type"),
	}
	return &profile, nil
}

func (c *Client) readIPConfig(ctx context.Context, path dbus.ObjectPath, iface string) (IPConfig, error) {
	props, err := c.getAll(ctx, path, iface)
	if err != nil {
		return IPConfig{}, err
	}
	config := IPConfig{Gateway: stringValue(props, "Gateway")}
	if variant, ok := props["AddressData"]; ok {
		if addresses, ok := variant.Value().([]map[string]dbus.Variant); ok {
			for _, entry := range addresses {
				address := stringValue(entry, "address")
				if address != "" {
					config.Addresses = append(config.Addresses, IPAddress{Address: address, Prefix: uint32Value(entry, "prefix")})
				}
			}
		}
	}
	if variant, ok := props["NameserverData"]; ok {
		if nameservers, ok := variant.Value().([]map[string]dbus.Variant); ok {
			for _, entry := range nameservers {
				if address := stringValue(entry, "address"); address != "" {
					config.DNS = append(config.DNS, address)
				}
			}
		}
	}
	return config, nil
}

func (c *Client) getAll(ctx context.Context, path dbus.ObjectPath, iface string) (map[string]dbus.Variant, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	var props map[string]dbus.Variant
	if err := c.call(ctx, path, propertiesInterface+".GetAll", iface).Store(&props); err != nil {
		return nil, err
	}
	return props, nil
}

func classifyWiFiSecurity(flags, wpaFlags, rsnFlags uint32) (WiFiSecurity, string) {
	combined := wpaFlags | rsnFlags
	switch {
	case combined&(apSecKeyMgmt8021X|apSecKeyMgmtSuiteB192) != 0:
		return WiFiSecurityEnterprise, "wpa-eap"
	case rsnFlags&apSecKeyMgmtSAE != 0 && combined&apSecKeyMgmtPSK == 0:
		return WiFiSecurityWPA3Personal, "sae"
	case combined&apSecKeyMgmtPSK != 0:
		return WiFiSecurityPersonal, "wpa-psk"
	case rsnFlags&(apSecKeyMgmtOWE|apSecKeyMgmtOWETransit) != 0:
		return WiFiSecurityOWE, "owe"
	case flags&apFlagPrivacy != 0:
		return WiFiSecurityWEP, "none"
	case wpaFlags == 0 && rsnFlags == 0:
		return WiFiSecurityOpen, ""
	default:
		return WiFiSecurityUnknown, ""
	}
}

func stringValue(values map[string]dbus.Variant, key string) string {
	if variant, ok := values[key]; ok {
		if value, ok := variant.Value().(string); ok {
			return value
		}
	}
	return ""
}

func boolValue(values map[string]dbus.Variant, key string) bool {
	return boolValueDefault(values, key, false)
}

func boolValueDefault(values map[string]dbus.Variant, key string, fallback bool) bool {
	if variant, ok := values[key]; ok {
		if value, ok := variant.Value().(bool); ok {
			return value
		}
	}
	return fallback
}

func uint32Value(values map[string]dbus.Variant, key string) uint32 {
	if variant, ok := values[key]; ok {
		if value, ok := variant.Value().(uint32); ok {
			return value
		}
	}
	return 0
}

func int32Value(values map[string]dbus.Variant, key string) int32 {
	if variant, ok := values[key]; ok {
		if value, ok := variant.Value().(int32); ok {
			return value
		}
	}
	return 0
}

func byteValue(values map[string]dbus.Variant, key string) uint8 {
	if variant, ok := values[key]; ok {
		if value, ok := variant.Value().(byte); ok {
			return value
		}
	}
	return 0
}

func objectPathValue(values map[string]dbus.Variant, key string) dbus.ObjectPath {
	if variant, ok := values[key]; ok {
		if value, ok := variant.Value().(dbus.ObjectPath); ok {
			return value
		}
	}
	return ""
}

func objectPathSliceValue(values map[string]dbus.Variant, key string) []dbus.ObjectPath {
	if variant, ok := values[key]; ok {
		if value, ok := variant.Value().([]dbus.ObjectPath); ok {
			return value
		}
	}
	return nil
}

func ssidValue(values map[string]dbus.Variant, key string) string {
	if variant, ok := values[key]; ok {
		if value, ok := variant.Value().([]byte); ok {
			return strings.ToValidUTF8(string(value), "�")
		}
	}
	return ""
}

func validObjectPath(path dbus.ObjectPath) bool {
	return path != "" && path != "/" && path.IsValid()
}

func hardwareAddress(value string) string {
	if parsed, err := net.ParseMAC(strings.TrimSpace(value)); err == nil {
		return strings.ToUpper(parsed.String())
	}
	return strings.ToUpper(strings.TrimSpace(value))
}
