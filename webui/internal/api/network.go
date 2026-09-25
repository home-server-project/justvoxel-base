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
	Interface             string           `json:"interface"`
	IPInterface           string           `json:"ip_interface,omitempty"`
	Kind                  string           `json:"kind"`
	State                 string           `json:"state"`
	Managed               bool             `json:"managed"`
	HardwareAddress       string           `json:"hardware_address,omitempty"`
	MTU                   uint32           `json:"mtu"`
	Carrier               *bool            `json:"carrier,omitempty"`
	SpeedMbps             uint32           `json:"speed_mbps,omitempty"`
	IPv4                  NetworkIPConfig  `json:"ipv4"`
	IPv6                  NetworkIPConfig  `json:"ipv6"`
	ActiveConnection      *NetworkProfile  `json:"active_connection,omitempty"`
	AvailableProfileUUIDs []string         `json:"available_profile_uuids"`
	Wireless              *NetworkWireless `json:"wireless,omitempty"`
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
	ProfileUUID    string `json:"profile_uuid,omitempty"`
	Hidden         bool   `json:"hidden"`
	Known          bool   `json:"known"`
	Active         bool   `json:"active"`
}

type WiFiNetworksResponse struct {
	Interface string        `json:"interface"`
	Networks  []WiFiNetwork `json:"networks"`
}

type NetworkCheckpoint struct {
	ID                     string   `json:"id"`
	Status                 string   `json:"status"`
	Interfaces             []string `json:"interfaces"`
	RollbackTimeoutSeconds uint32   `json:"rollback_timeout_seconds"`
	CreatedAt              string   `json:"created_at"`
	ExpiresAt              string   `json:"expires_at"`
}

type NetworkCheckpointRollbackDevice struct {
	Interface string `json:"interface"`
	Result    string `json:"result"`
}

type NetworkCheckpointRollback struct {
	OK      bool                              `json:"ok"`
	ID      string                            `json:"id"`
	Results []NetworkCheckpointRollbackDevice `json:"results"`
}

type NetworkWiFiMutation struct {
	OK          bool               `json:"ok"`
	Action      string             `json:"action"`
	Interface   string             `json:"interface,omitempty"`
	ProfileUUID string             `json:"profile_uuid,omitempty"`
	Checkpoint *NetworkCheckpoint `json:"checkpoint,omitempty"`
}

type NetworkWiFiConnectRequest struct {
	CheckpointID  string
	ProfileUUID   string
	SSID           string
	BSSID          string
	KeyManagement string
	Password      string
	Hidden        bool
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

func (c *Client) CreateNetworkCheckpoint(ctx context.Context, session string, interfaces []string, timeout uint32) (NetworkCheckpoint, error) {
	var out NetworkCheckpoint
	body := map[string]any{
		"interfaces":               interfaces,
		"rollback_timeout_seconds": timeout,
	}
	err := c.do(ctx, http.MethodPost, "/v1/admin/network/checkpoints", session, body, &out)
	return out, err
}

func (c *Client) NetworkCheckpoint(ctx context.Context, session, id string) (NetworkCheckpoint, error) {
	var out NetworkCheckpoint
	path := "/v1/admin/network/checkpoints/" + url.PathEscape(id)
	err := c.do(ctx, http.MethodGet, path, session, nil, &out)
	return out, err
}

func (c *Client) ConfirmNetworkCheckpoint(ctx context.Context, session, id string) error {
	path := "/v1/admin/network/checkpoints/" + url.PathEscape(id) + "/confirm"
	return c.do(ctx, http.MethodPost, path, session, nil, nil)
}

func (c *Client) RollbackNetworkCheckpoint(ctx context.Context, session, id string) (NetworkCheckpointRollback, error) {
	var out NetworkCheckpointRollback
	path := "/v1/admin/network/checkpoints/" + url.PathEscape(id) + "/rollback"
	err := c.do(ctx, http.MethodPost, path, session, nil, &out)
	return out, err
}

func (c *Client) SetWiFiRadio(ctx context.Context, session string, enabled bool, checkpointID string) (NetworkWiFiMutation, error) {
	var out NetworkWiFiMutation
	body := map[string]any{
		"enabled": enabled,
	}
	if checkpointID != "" {
		body["checkpoint_id"] = checkpointID
	}
	err := c.do(ctx, http.MethodPost, "/v1/admin/network/wifi/radio", session, body, &out)
	return out, err
}

func (c *Client) ConnectWiFi(ctx context.Context, session, interfaceName string, request NetworkWiFiConnectRequest) (NetworkWiFiMutation, error) {
	var out NetworkWiFiMutation
	body := map[string]any{
		"checkpoint_id": request.CheckpointID,
	}
	if request.ProfileUUID != "" {
		body["profile_uuid"] = request.ProfileUUID
	} else {
		body["ssid"] = request.SSID
		body["bssid"] = request.BSSID
		body["key_management"] = request.KeyManagement
		body["password"] = request.Password
		body["hidden"] = request.Hidden
	}
	path := "/v1/admin/network/wifi/" + url.PathEscape(interfaceName) + "/connect"
	err := c.do(ctx, http.MethodPost, path, session, body, &out)
	return out, err
}

func (c *Client) DisconnectWiFi(ctx context.Context, session, interfaceName, checkpointID string) (NetworkWiFiMutation, error) {
	var out NetworkWiFiMutation
	path := "/v1/admin/network/wifi/" + url.PathEscape(interfaceName) + "/disconnect"
	err := c.do(ctx, http.MethodPost, path, session, map[string]string{"checkpoint_id": checkpointID}, &out)
	return out, err
}

func (c *Client) ForgetWiFiProfile(ctx context.Context, session, profileUUID string) (NetworkWiFiMutation, error) {
	var out NetworkWiFiMutation
	path := "/v1/admin/network/wifi/profiles/" + url.PathEscape(profileUUID) + "/forget"
	err := c.do(ctx, http.MethodPost, path, session, nil, &out)
	return out, err
}
