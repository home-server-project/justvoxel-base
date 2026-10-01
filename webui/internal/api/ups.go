package api

import (
	"context"
	"net/http"
)

type UPSSource struct {
	Mode       string `json:"mode"`
	Host       string `json:"host,omitempty"`
	Port       int    `json:"port,omitempty"`
	UPSName    string `json:"ups_name,omitempty"`
	Driver     string `json:"driver,omitempty"`
	DevicePort string `json:"device_port,omitempty"`
}

type UPSShutdownRequest struct {
	Enabled         bool   `json:"enabled"`
	DelaySeconds    int    `json:"delay_seconds"`
	MonitorUsername string `json:"monitor_username"`
	MonitorPassword string `json:"monitor_password"`
}

type UPSSharingRequest struct {
	Enabled        bool   `json:"enabled"`
	ListenAddress  string `json:"listen_address"`
	ListenPort     int    `json:"listen_port"`
	ClientUsername string `json:"client_username"`
	ClientPassword string `json:"client_password"`
}

type UPSDevice struct {
	Name                  string   `json:"name"`
	DisplayName           string   `json:"display_name"`
	State                 string   `json:"state"`
	Manufacturer          string   `json:"manufacturer,omitempty"`
	Model                 string   `json:"model,omitempty"`
	BatteryCharge         *float64 `json:"battery_charge,omitempty"`
	BatteryRuntimeSeconds *int64   `json:"battery_runtime_seconds,omitempty"`
	LoadPercent           *float64 `json:"load_percent,omitempty"`
	InputVoltage          *float64 `json:"input_voltage,omitempty"`
	OutputVoltage         *float64 `json:"output_voltage,omitempty"`
	InputFrequency        *float64 `json:"input_frequency,omitempty"`
	OutputFrequency       *float64 `json:"output_frequency,omitempty"`
	BatteryVoltage        *float64 `json:"battery_voltage,omitempty"`
	Temperature           *float64 `json:"temperature,omitempty"`
}

type UPSStatus struct {
	Available                    bool        `json:"available"`
	LocalSetupAvailable          bool        `json:"local_setup_available"`
	Source                       UPSSource   `json:"source"`
	Monitoring                   bool        `json:"monitoring"`
	Devices                      []UPSDevice `json:"devices"`
	Message                      string      `json:"message,omitempty"`
	NUTMode                      string      `json:"nut_mode"`
	ProtectionEnabled            bool        `json:"protection_enabled"`
	ShutdownDelaySeconds         int         `json:"shutdown_delay_seconds"`
	MonitorCredentialsConfigured bool        `json:"monitor_credentials_configured"`
	SharingEnabled               bool        `json:"sharing_enabled"`
	SharingListenAddress         string      `json:"sharing_listen_address,omitempty"`
	SharingListenPort            int         `json:"sharing_listen_port,omitempty"`
	SharingCredentialsConfigured bool        `json:"sharing_credentials_configured"`
	MonitorServiceActive         bool        `json:"monitor_service_active"`
	ServerServiceActive          bool        `json:"server_service_active"`
	DriverServiceActive          bool        `json:"driver_service_active"`
}

func (c *Client) UPSStatus(ctx context.Context, session string) (UPSStatus, error) {
	var out UPSStatus
	err := c.do(ctx, http.MethodGet, "/v1/ups", session, nil, &out)
	return out, err
}

func (c *Client) AdminSetUPSSource(ctx context.Context, session string, source UPSSource) (UPSStatus, error) {
	var out UPSStatus
	err := c.do(ctx, http.MethodPost, "/v1/admin/ups/source", session, source, &out)
	return out, err
}

func (c *Client) AdminForgetUPSSource(ctx context.Context, session string) (UPSStatus, error) {
	var out UPSStatus
	err := c.do(ctx, http.MethodPost, "/v1/admin/ups/source/forget", session, nil, &out)
	return out, err
}

func (c *Client) AdminSetUPSShutdown(ctx context.Context, session string, request UPSShutdownRequest) (UPSStatus, error) {
	var out UPSStatus
	err := c.do(ctx, http.MethodPost, "/v1/admin/ups/shutdown", session, request, &out)
	return out, err
}

func (c *Client) AdminSetUPSSharing(ctx context.Context, session string, request UPSSharingRequest) (UPSStatus, error) {
	var out UPSStatus
	err := c.do(ctx, http.MethodPost, "/v1/admin/ups/sharing", session, request, &out)
	return out, err
}
