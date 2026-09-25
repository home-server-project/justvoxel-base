package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/home-server-project/justvoxel/management/internal/networking"
)

type fakeNetworkClient struct {
	snapshot networking.Snapshot
	networks []networking.WiFiNetwork
	scanned  string
}

func (f *fakeNetworkClient) Close() error { return nil }
func (f *fakeNetworkClient) Snapshot(context.Context) (networking.Snapshot, error) {
	return f.snapshot, nil
}
func (f *fakeNetworkClient) WiFiNetworks(_ context.Context, iface string) ([]networking.WiFiNetwork, error) {
	return f.networks, nil
}
func (f *fakeNetworkClient) RequestWiFiScan(_ context.Context, iface string) error {
	f.scanned = iface
	return nil
}

func TestNetworkSnapshotViewUsesStableNames(t *testing.T) {
	view := networkSnapshotView(networking.Snapshot{
		Version:           "1.58.0",
		State:             70,
		Connectivity:      4,
		NetworkingEnabled: true,
		WirelessEnabled:   true,
		Devices: []networking.Device{{
			Interface: "enp1s0",
			Kind:      networking.DeviceKindEthernet,
			State:     100,
			Managed:   true,
			IPv4: networking.IPConfig{
				Addresses: []networking.IPAddress{{Address: "192.168.0.59", Prefix: 24}},
				Gateway:   "192.168.0.1",
				DNS:       []string{"192.168.0.1"},
			},
		}},
	})
	if view.State != "connected-global" || view.Connectivity != "full" {
		t.Fatalf("unexpected manager state: %+v", view)
	}
	if len(view.Devices) != 1 || view.Devices[0].Interface != "enp1s0" || view.Devices[0].State != "activated" {
		t.Fatalf("unexpected device view: %+v", view.Devices)
	}
}

func TestNetworkRoutesAllowReadAccessAndScan(t *testing.T) {
	fake := &fakeNetworkClient{
		snapshot: networking.Snapshot{
			Version:           "1.58.0",
			State:             70,
			Connectivity:      4,
			NetworkingEnabled: true,
		},
		networks: []networking.WiFiNetwork{{SSID: "Home WiFi", Strength: 82, Security: networking.WiFiSecurityPersonal}},
	}
	previous := openNetworkClient
	openNetworkClient = func(context.Context) (networkClient, error) { return fake, nil }
	t.Cleanup(func() { openNetworkClient = previous })

	s := &server{}
	mux := http.NewServeMux()
	registerNetworkRoutes(mux, s)

	statusRequest := httptest.NewRequest(http.MethodGet, "/v1/network", nil)
	statusRequest = statusRequest.WithContext(context.WithValue(statusRequest.Context(), peerUIDKey{}, uint32(0)))
	statusResponse := httptest.NewRecorder()
	mux.ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("network status = %d, body %s", statusResponse.Code, statusResponse.Body.String())
	}

	wifiRequest := httptest.NewRequest(http.MethodGet, "/v1/network/wifi/wlp2s0/networks", nil)
	wifiRequest = wifiRequest.WithContext(context.WithValue(wifiRequest.Context(), peerUIDKey{}, uint32(0)))
	wifiResponse := httptest.NewRecorder()
	mux.ServeHTTP(wifiResponse, wifiRequest)
	if wifiResponse.Code != http.StatusOK {
		t.Fatalf("Wi-Fi networks = %d, body %s", wifiResponse.Code, wifiResponse.Body.String())
	}

	scanRequest := httptest.NewRequest(http.MethodPost, "/v1/network/wifi/wlp2s0/scan", nil)
	scanRequest = scanRequest.WithContext(context.WithValue(scanRequest.Context(), peerUIDKey{}, uint32(0)))
	scanResponse := httptest.NewRecorder()
	mux.ServeHTTP(scanResponse, scanRequest)
	if scanResponse.Code != http.StatusAccepted {
		t.Fatalf("Wi-Fi scan = %d, body %s", scanResponse.Code, scanResponse.Body.String())
	}
	if fake.scanned != "wlp2s0" {
		t.Fatalf("scan interface = %q, want wlp2s0", fake.scanned)
	}
}
