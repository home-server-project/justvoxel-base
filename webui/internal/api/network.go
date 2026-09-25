package api

import (
	"context"
	"net/http"
	"net/url"
)

type NetworkIPAddress struct {
	Address string `json:"address"`
	Prefix  uint32 `json:"prefix"`
}

type NetworkIPConfig struct {
	Addresses []NetworkIPAddress `json:"addresses"`
	Gateway   string             `json:"gateway,omitempty"`
	DNS       []string           `json:"dns"`
}

type NetworkProfile struct {
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

type NetworkWireless struct {
	SSID        string `json:"ssid,omitempty"`
	Signal      uint8  `json:"signal"`
	BitrateKbps uint32 `json:"bitrate_kbps"`
}

type NetworkDevice struct {
	Interface             string              `json:"interface"`
	IPInterface           string              `json:"ip_interface,omitempty"`
	Kind                  string              `json:"kind"`
	State                 string              `json:"state"`
	Managed               bool                `json:"managed"`
	HardwareAddress       string              `json:"hardware_address,omitempty"`
	MTU                   uint32              `json:"mtu"`
	Carrier               *bool               `json:"carrier,omitempty"`
	SpeedMbps             uint32              `json:"speed_mbps,omitempty"`
	IPv4                  NetworkIPConfig     `json:"ipv4"`
	IPv6                  NetworkIPConfig     `json:"ipv6"`
	ActiveConnection      *NetworkProfile     `json:"active_connection,omitempty"`
	AvailableProfileUUIDs []string            `json:"available_profile_uuids"`
	Wireless              *NetworkWireless    `json:"wireless,omitempty"`
}

type NetworkStatus struct {
	Version           string           `json:"version"`
	State             string           `json:"state"`
	Connectivity      string           `json:"connectivity"`
	NetworkingEnabled bool             `json:"networking_enabled"`
	WirelessEnabled   bool             `json:"wireless_enabled"`
	Devices           []NetworkDevice  `json:"devices"`
	Profiles          []NetworkProfile `json:"profiles"`
}

type WiFiNetwork struct {
	SSID           string `json:"ssid"`
	BSSID          string `json:"bssid"`
	Strength       uint8  `json:"strength"`
	FrequencyMHz   uint32 `json:"frequency_mhz"`
	MaxBitrateKbps uint32 `json:"max_bitrate_kbps"`
	Security       string `json:"security"`
	KeyManagement  string `json:"key_management"`
	Hidden         bool   `json:"hidden"`
	Known          bool   `json:"known"`
	Active         bool   `json:"active"`
}

type WiFiNetworksResponse struct {
	Interface string        `json:"interface"`
	Networks  []WiFiNetwork `json:"networks"`
}

func (c *Client) NetworkStatus(ctx context.Context, session string) (NetworkStatus, error) {
	var out NetworkStatus
	err := c.do(ctx, http.MethodGet, "/v1/network", session, nil, &out)
	return out, err
}

func (c *Client) WiFiNetworks(ctx context.Context, session, interfaceName string) (WiFiNetworksResponse, error) {
	var out WiFiNetworksResponse
	path := "/v1/network/wifi/" + url.PathEscape(interfaceName) + "/networks"
	err := c.do(ctx, http.MethodGet, path, session, nil, &out)
	return out, err
}

func (c *Client) RequestWiFiScan(ctx context.Context, session, interfaceName string) error {
	path := "/v1/network/wifi/" + url.PathEscape(interfaceName) + "/scan"
	return c.do(ctx, http.MethodPost, path, session, nil, nil)
}
