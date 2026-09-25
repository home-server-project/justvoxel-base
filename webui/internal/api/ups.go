package api

import (
	"context"
	"net/http"
)

type UPSSource struct {
	Mode string `json:"mode"`
	Host string `json:"host,omitempty"`
	Port int    `json:"port,omitempty"`
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
	Available           bool        `json:"available"`
	LocalSetupAvailable bool        `json:"local_setup_available"`
	Source              UPSSource   `json:"source"`
	Monitoring          bool        `json:"monitoring"`
	Devices             []UPSDevice `json:"devices"`
	Message             string      `json:"message,omitempty"`
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
