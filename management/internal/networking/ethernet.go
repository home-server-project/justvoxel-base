package networking

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"regexp"

	"github.com/godbus/dbus/v5"
)

var interfaceIdentifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,14}$`)
var profileIdentifier = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type EthernetSettings struct {
	Interface   string   `json:"interface"`
	ProfileUUID string   `json:"profile_uuid"`
	Method      string   `json:"method"`
	Address     string   `json:"address"`
	Prefix      uint32   `json:"prefix"`
	Gateway     string   `json:"gateway"`
	DNS         []string `json:"dns"`
	MTU         uint32   `json:"mtu"`
	Autoconnect bool     `json:"autoconnect"`
}

// EthernetCandidate never crosses the API boundary. Persistent settings remain
// unchanged throughout staging and automatic checkpoint rollback.
type EthernetCandidate struct {
	Settings   EthernetSettings
	profile    dbus.ObjectPath
	device     dbus.ObjectPath
	original   map[string]map[string]dbus.Variant
	persistent map[string]map[string]dbus.Variant
	applied    map[string]map[string]dbus.Variant
	version    uint64
}

func ValidateInterface(value string) error {
	if !interfaceIdentifier.MatchString(value) {
		return errors.New("invalid network interface identifier")
	}
	return nil
}

func ValidateEthernetSettings(s EthernetSettings) error {
	if err := ValidateInterface(s.Interface); err != nil {
		return err
	}
	if !profileIdentifier.MatchString(s.ProfileUUID) {
		return errors.New("invalid Ethernet profile UUID")
	}
	if s.Method != "auto" && s.Method != "manual" {
		return errors.New("IPv4 method must be auto or manual")
	}
	validIP := func(value string) bool {
		ip, err := netip.ParseAddr(value)
		return err == nil && ip.Is4() && !ip.IsUnspecified() && !ip.IsMulticast() && !ip.IsLoopback() && value != "255.255.255.255"
	}
	if s.Prefix > 32 {
		return errors.New("IPv4 prefix must be between 0 and 32")
	}
	if s.Method == "manual" && !validIP(s.Address) {
		return errors.New("invalid manual IPv4 address")
	}
	if s.Method == "auto" && (s.Address != "" || s.Gateway != "" || s.Prefix != 0) {
		return errors.New("automatic IPv4 must not include a manual address, prefix or gateway")
	}
	if s.Gateway != "" && !validIP(s.Gateway) {
		return errors.New("invalid IPv4 gateway")
	}
	if len(s.DNS) > 8 {
		return errors.New("too many DNS servers")
	}
	for _, address := range s.DNS {
		if !validIP(address) {
			return errors.New("invalid IPv4 DNS server")
		}
	}
	if s.MTU != 0 && (s.MTU < 576 || s.MTU > 9000) {
		return errors.New("MTU must be automatic (0) or between 576 and 9000")
	}
	return nil
}

func ethernetSettings(base map[string]map[string]dbus.Variant, s EthernetSettings, persistent bool) map[string]map[string]dbus.Variant {
	out := make(map[string]map[string]dbus.Variant, len(base))
	for group, values := range base {
		out[group] = make(map[string]dbus.Variant, len(values))
		for key, value := range values {
			out[group][key] = value
		}
	}
	if out["ipv4"] == nil {
		out["ipv4"] = map[string]dbus.Variant{}
	}
	ip := out["ipv4"]
	for _, key := range []string{"addresses", "address-data", "gateway", "dns", "dns-data"} {
		delete(ip, key)
	}
	ip["method"] = dbus.MakeVariant(s.Method)
	ip["ignore-auto-dns"] = dbus.MakeVariant(len(s.DNS) > 0)
	if s.Method == "manual" {
		ip["address-data"] = dbus.MakeVariant([]map[string]dbus.Variant{{"address": dbus.MakeVariant(s.Address), "prefix": dbus.MakeVariant(s.Prefix)}})
		if s.Gateway != "" {
			ip["gateway"] = dbus.MakeVariant(s.Gateway)
		}
	}
	dns := make([]string, len(s.DNS))
	copy(dns, s.DNS)
	ip["dns-data"] = dbus.MakeVariant(dns)
	if out["802-3-ethernet"] == nil {
		out["802-3-ethernet"] = map[string]dbus.Variant{}
	}
	out["802-3-ethernet"]["mtu"] = dbus.MakeVariant(s.MTU)
	// Autoconnect is a future activation policy, not a live device setting.
	if persistent {
		out["connection"]["autoconnect"] = dbus.MakeVariant(s.Autoconnect)
	}
	return out
}

func (c *Client) StageEthernet(ctx context.Context, s EthernetSettings) (*EthernetCandidate, error) {
	if err := ValidateEthernetSettings(s); err != nil {
		return nil, err
	}
	device, err := c.devicePath(ctx, s.Interface)
	if err != nil {
		return nil, err
	}
	props, err := c.getAll(ctx, device, deviceInterface)
	if err != nil {
		return nil, err
	}
	if deviceKindFromNM(uint32Value(props, "DeviceType")) != DeviceKindEthernet || uint32Value(props, "State") != 100 {
		return nil, errors.New("Ethernet configuration requires an active Ethernet interface")
	}
	active := objectPathValue(props, "ActiveConnection")
	if !validObjectPath(active) {
		return nil, errors.New("Ethernet has no active profile")
	}
	activeProps, err := c.getAll(ctx, active, activeInterface)
	if err != nil {
		return nil, err
	}
	profile := objectPathValue(activeProps, "Connection")
	original, err := c.connectionSettings(ctx, profile)
	if err != nil {
		return nil, err
	}
	if stringValue(original["connection"], "uuid") != s.ProfileUUID || stringValue(original["connection"], "type") != "802-3-ethernet" {
		return nil, errors.New("selected profile is not the active Ethernet profile")
	}
	if original["802-1x"] != nil || stringValue(original["connection"], "controller") != "" || stringValue(original["connection"], "master") != "" {
		return nil, errors.New("advanced Ethernet profiles must be configured with nmtui")
	}
	var applied map[string]map[string]dbus.Variant
	var version uint64
	if err := c.call(ctx, device, deviceInterface+".GetAppliedConnection", uint32(0)).Store(&applied, &version); err != nil {
		return nil, errors.New("live Ethernet staging is unavailable; persistent profile was not changed")
	}
	if stringValue(applied["connection"], "uuid") != s.ProfileUUID {
		return nil, errors.New("active Ethernet profile changed")
	}
	candidate := &EthernetCandidate{Settings: s, profile: profile, device: device, original: original, persistent: ethernetSettings(original, s, true)}
	live := ethernetSettings(applied, s, false)
	if err := c.call(ctx, device, deviceInterface+".Reapply", live, version, uint32(0)).Err; err != nil {
		return nil, errors.New("NetworkManager cannot safely reapply these Ethernet settings; persistent profile was not changed")
	}
	if err := c.call(ctx, device, deviceInterface+".GetAppliedConnection", uint32(0)).Store(&candidate.applied, &candidate.version); err != nil {
		return nil, errors.New("could not verify staged Ethernet settings; revert this transaction")
	}
	if stringValue(candidate.applied["connection"], "uuid") != s.ProfileUUID || stringValue(candidate.applied["ipv4"], "method") != s.Method {
		return nil, errors.New("live Ethernet profile changed during staging; revert this transaction")
	}
	return candidate, nil
}

// PersistEthernet is called only after CheckpointDestroy succeeds. Refuse to
// overwrite concurrent console edits or a different live connection.
func (c *Client) PersistEthernet(ctx context.Context, candidate *EthernetCandidate) error {
	current, err := c.connectionSettings(ctx, candidate.profile)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, candidate.original) {
		return errors.New("persistent Ethernet profile changed during review")
	}
	var applied map[string]map[string]dbus.Variant
	var version uint64
	if err := c.call(ctx, candidate.device, deviceInterface+".GetAppliedConnection", uint32(0)).Store(&applied, &version); err != nil {
		return err
	}
	if version != candidate.version || !reflect.DeepEqual(applied, candidate.applied) {
		return errors.New("live Ethernet settings changed during review")
	}
	return c.call(ctx, candidate.profile, connectionInterface+".Update", candidate.persistent).Err
}

func (c *Client) CheckConnectivity(ctx context.Context) (string, error) {
	var state uint32
	if err := c.call(ctx, managerPath, managerInterface+".CheckConnectivity").Store(&state); err != nil {
		return "", err
	}
	return ConnectivityName(state), nil
}

func (c *Client) ReconnectInterface(ctx context.Context, interfaceName, uuid string) error {
	if err := ValidateInterface(interfaceName); err != nil {
		return err
	}
	if !profileIdentifier.MatchString(uuid) {
		return errors.New("invalid profile UUID")
	}
	device, err := c.devicePath(ctx, interfaceName)
	if err != nil {
		return err
	}
	props, err := c.getAll(ctx, device, deviceInterface)
	if err != nil {
		return err
	}
	kind := deviceKindFromNM(uint32Value(props, "DeviceType"))
	if kind != DeviceKindEthernet && kind != DeviceKindWiFi {
		return errors.New("only Ethernet and Wi-Fi repair is supported")
	}
	active := objectPathValue(props, "ActiveConnection")
	if !validObjectPath(active) {
		return errors.New("repair requires an active profile")
	}
	activeProps, err := c.getAll(ctx, active, activeInterface)
	if err != nil {
		return err
	}
	if stringValue(activeProps, "Uuid") != uuid {
		return errors.New("selected profile is no longer active")
	}
	profile := objectPathValue(activeProps, "Connection")
	var result dbus.ObjectPath
	return c.call(ctx, managerPath, managerInterface+".ActivateConnection", profile, device, dbus.ObjectPath("/")).Store(&result)
}
