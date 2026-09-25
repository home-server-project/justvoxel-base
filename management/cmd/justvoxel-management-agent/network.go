package main

import (
	"context"
	"net/http"
	"time"

	"github.com/home-server-project/justvoxel/management/internal/networking"
)

type networkClient interface {
	Close() error
	Snapshot(context.Context) (networking.Snapshot, error)
	WiFiNetworks(context.Context, string) ([]networking.WiFiNetwork, error)
	RequestWiFiScan(context.Context, string) error
	CreateCheckpoint(context.Context, []string, uint32) (networking.Checkpoint, error)
	DestroyCheckpoint(context.Context, networking.Checkpoint) error
	RollbackCheckpoint(context.Context, networking.Checkpoint) (map[string]networking.RollbackResult, error)
}

var openNetworkClient = func(ctx context.Context) (networkClient, error) {
	return networking.NewSystem(ctx)
}

type networkIPConfigView struct {
	Addresses []networking.IPAddress `json:"addresses"`
	Gateway   string                 `json:"gateway,omitempty"`
	DNS       []string               `json:"dns"`
}

type networkProfileView struct {
	ID                  string `json:"id"`
	UUID                string `json:"uuid"`
	Type                string `json:"type"`
	InterfaceName       string `json:"interface_name,omitempty"`
	Autoconnect         bool   `json:"autoconnect"`
	AutoconnectPriority int32  `json:"autoconnect_priority"`
	SSID                string `json:"ssid,omitempty"`
	Hidden              bool   `json:"hidden,omitempty"`
	KeyManagement       string `json:"key_management,omitempty"`
	IPv4Method          string `json:"ipv4_method,omitempty"`
	IPv6Method          string `json:"ipv6_method,omitempty"`
}

type networkWirelessView struct {
	SSID        string `json:"ssid,omitempty"`
	Signal      uint8  `json:"signal"`
	BitrateKbps uint32 `json:"bitrate_kbps"`
}

type networkDeviceView struct {
	Interface             string               `json:"interface"`
	IPInterface           string               `json:"ip_interface,omitempty"`
	Kind                  string               `json:"kind"`
	State                 string               `json:"state"`
	Managed               bool                 `json:"managed"`
	HardwareAddress       string               `json:"hardware_address,omitempty"`
	MTU                   uint32               `json:"mtu"`
	Carrier               *bool                `json:"carrier,omitempty"`
	SpeedMbps             uint32               `json:"speed_mbps,omitempty"`
	IPv4                  networkIPConfigView  `json:"ipv4"`
	IPv6                  networkIPConfigView  `json:"ipv6"`
	ActiveConnection      *networkProfileView  `json:"active_connection,omitempty"`
	AvailableProfileUUIDs []string             `json:"available_profile_uuids"`
	Wireless              *networkWirelessView `json:"wireless,omitempty"`
}

type networkStatusView struct {
	Version           string               `json:"version"`
	State             string               `json:"state"`
	Connectivity      string               `json:"connectivity"`
	NetworkingEnabled bool                 `json:"networking_enabled"`
	WirelessEnabled   bool                 `json:"wireless_enabled"`
	Devices           []networkDeviceView  `json:"devices"`
	Profiles          []networkProfileView `json:"profiles"`
}

type networkWiFiNetworkView struct {
	SSID           string `json:"ssid"`
	BSSID          string `json:"bssid,omitempty"`
	Strength       uint8  `json:"strength"`
	FrequencyMHz   uint32 `json:"frequency_mhz,omitempty"`
	MaxBitrateKbps uint32 `json:"max_bitrate_kbps,omitempty"`
	Security       string `json:"security"`
	KeyManagement  string `json:"key_management,omitempty"`
	Hidden         bool   `json:"hidden"`
	Known          bool   `json:"known"`
	Active         bool   `json:"active"`
}

type networkWiFiView struct {
	Interface string                   `json:"interface"`
	Networks  []networkWiFiNetworkView `json:"networks"`
}

func registerNetworkRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/network", s.networkStatus)
	mux.HandleFunc("GET /v1/network/wifi/{interface}/networks", s.networkWiFiNetworks)
	mux.HandleFunc("POST /v1/network/wifi/{interface}/scan", s.networkWiFiScan)
	registerNetworkCheckpointRoutes(mux, s)
}

func (s *server) networkStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireReadAccess(w, r); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	client, err := openNetworkClient(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "network status is unavailable")
		return
	}
	defer client.Close()

	snapshot, err := client.Snapshot(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "network status is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, networkSnapshotView(snapshot))
}

func (s *server) networkWiFiNetworks(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireReadAccess(w, r); !ok {
		return
	}
	interfaceName := r.PathValue("interface")
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	client, err := openNetworkClient(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Wi-Fi status is unavailable")
		return
	}
	defer client.Close()

	networks, err := client.WiFiNetworks(ctx, interfaceName)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Wi-Fi status is unavailable")
		return
	}
	items := make([]networkWiFiNetworkView, 0, len(networks))
	for _, network := range networks {
		items = append(items, networkWiFiNetworkView{
			SSID:           network.SSID,
			BSSID:          network.BSSID,
			Strength:       network.Strength,
			FrequencyMHz:   network.FrequencyMHz,
			MaxBitrateKbps: network.MaxBitrateKbps,
			Security:       string(network.Security),
			KeyManagement:  network.KeyManagement,
			Hidden:         network.Hidden,
			Known:          network.Known,
			Active:         network.Active,
		})
	}
	writeJSON(w, http.StatusOK, networkWiFiView{Interface: interfaceName, Networks: items})
}

func (s *server) networkWiFiScan(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireReadAccess(w, r); !ok {
		return
	}
	interfaceName := r.PathValue("interface")
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	client, err := openNetworkClient(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Wi-Fi scan is unavailable")
		return
	}
	defer client.Close()

	if err := client.RequestWiFiScan(ctx, interfaceName); err != nil {
		writeError(w, http.StatusServiceUnavailable, "Wi-Fi scan could not be started")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "interface": interfaceName})
}

func networkSnapshotView(snapshot networking.Snapshot) networkStatusView {
	view := networkStatusView{
		Version:           snapshot.Version,
		State:             networkManagerStateName(snapshot.State),
		Connectivity:      networking.ConnectivityName(snapshot.Connectivity),
		NetworkingEnabled: snapshot.NetworkingEnabled,
		WirelessEnabled:   snapshot.WirelessEnabled,
		Devices:           make([]networkDeviceView, 0, len(snapshot.Devices)),
		Profiles:          make([]networkProfileView, 0, len(snapshot.Profiles)),
	}
	for _, profile := range snapshot.Profiles {
		view.Profiles = append(view.Profiles, networkProfileToView(profile))
	}
	for _, device := range snapshot.Devices {
		item := networkDeviceView{
			Interface:             device.Interface,
			IPInterface:           device.IPInterface,
			Kind:                  string(device.Kind),
			State:                 networking.DeviceStateName(device.State),
			Managed:               device.Managed,
			HardwareAddress:       device.HardwareAddress,
			MTU:                   device.MTU,
			Carrier:               device.Carrier,
			SpeedMbps:             device.SpeedMbps,
			IPv4:                  networkIPToView(device.IPv4),
			IPv6:                  networkIPToView(device.IPv6),
			AvailableProfileUUIDs: append([]string(nil), device.AvailableProfileUUIDs...),
		}
		if device.ActiveConnection != nil {
			profile := networkProfileToView(*device.ActiveConnection)
			item.ActiveConnection = &profile
		}
		if device.Wireless != nil {
			item.Wireless = &networkWirelessView{
				SSID:        device.Wireless.SSID,
				Signal:      device.Wireless.Signal,
				BitrateKbps: device.Wireless.BitrateKbps,
			}
		}
		view.Devices = append(view.Devices, item)
	}
	return view
}

func networkProfileToView(profile networking.ConnectionProfile) networkProfileView {
	return networkProfileView{
		ID:                  profile.ID,
		UUID:                profile.UUID,
		Type:                profile.Type,
		InterfaceName:       profile.InterfaceName,
		Autoconnect:         profile.Autoconnect,
		AutoconnectPriority: profile.AutoconnectPriority,
		SSID:                profile.SSID,
		Hidden:              profile.Hidden,
		KeyManagement:       profile.KeyManagement,
		IPv4Method:          profile.IPv4Method,
		IPv6Method:          profile.IPv6Method,
	}
}

func networkIPToView(config networking.IPConfig) networkIPConfigView {
	addresses := append([]networking.IPAddress(nil), config.Addresses...)
	dns := append([]string(nil), config.DNS...)
	if addresses == nil {
		addresses = []networking.IPAddress{}
	}
	if dns == nil {
		dns = []string{}
	}
	return networkIPConfigView{Addresses: addresses, Gateway: config.Gateway, DNS: dns}
}

func networkManagerStateName(state uint32) string {
	switch state {
	case 10:
		return "asleep"
	case 20:
		return "disconnected"
	case 30:
		return "disconnecting"
	case 40:
		return "connecting"
	case 50:
		return "connected-local"
	case 60:
		return "connected-site"
	case 70:
		return "connected-global"
	default:
		return "unknown"
	}
}
