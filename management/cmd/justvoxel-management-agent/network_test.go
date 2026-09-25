package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/home-server-project/justvoxel/management/internal/networking"
)

type fakeNetworkClient struct {
	snapshot        networking.Snapshot
	networks        []networking.WiFiNetwork
	scanned         string
	checkpoint      networking.Checkpoint
	checkpointError error
	destroyed       networking.Checkpoint
	rollbackResults map[string]networking.RollbackResult
	rolledBack      networking.Checkpoint
	wirelessEnabled *bool
	activated       string
	disconnected    string
	forgotten       string
	connected       networking.WiFiConnectRequest
	connectResult   networking.WiFiConnectResult
	connectError    error
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
func (f *fakeNetworkClient) SetWirelessEnabled(_ context.Context, enabled bool) error {
	f.wirelessEnabled = &enabled
	return nil
}
func (f *fakeNetworkClient) ActivateWiFiProfile(_ context.Context, iface, profileUUID string) error {
	f.activated = iface + ":" + profileUUID
	return nil
}
func (f *fakeNetworkClient) DisconnectWiFi(_ context.Context, iface string) error {
	f.disconnected = iface
	return nil
}
func (f *fakeNetworkClient) ForgetWiFiProfile(_ context.Context, profileUUID string) error {
	f.forgotten = profileUUID
	return nil
}
func (f *fakeNetworkClient) ConnectWiFi(_ context.Context, request networking.WiFiConnectRequest) (networking.WiFiConnectResult, error) {
	f.connected = request
	if f.connectError != nil {
		return networking.WiFiConnectResult{}, f.connectError
	}
	if f.connectResult.ProfileUUID != "" {
		return f.connectResult, nil
	}
	return networking.WiFiConnectResult{ProfileUUID: "new-profile-uuid"}, nil
}
func (f *fakeNetworkClient) CreateCheckpoint(_ context.Context, interfaces []string, timeout uint32) (networking.Checkpoint, error) {
	if f.checkpointError != nil {
		return networking.Checkpoint{}, f.checkpointError
	}
	if f.checkpoint.Handle != "" {
		return f.checkpoint, nil
	}
	devices := make([]networking.CheckpointDevice, 0, len(interfaces))
	for _, interfaceName := range interfaces {
		devices = append(devices, networking.CheckpointDevice{Interface: interfaceName, Handle: "/device/" + interfaceName})
	}
	return networking.Checkpoint{
		Handle:                 "/checkpoint/1",
		Devices:                devices,
		RollbackTimeoutSeconds: timeout,
	}, nil
}
func (f *fakeNetworkClient) DestroyCheckpoint(_ context.Context, checkpoint networking.Checkpoint) error {
	f.destroyed = checkpoint
	return nil
}
func (f *fakeNetworkClient) RollbackCheckpoint(_ context.Context, checkpoint networking.Checkpoint) (map[string]networking.RollbackResult, error) {
	f.rolledBack = checkpoint
	if f.rollbackResults != nil {
		return f.rollbackResults, nil
	}
	results := make(map[string]networking.RollbackResult, len(checkpoint.Devices))
	for _, device := range checkpoint.Devices {
		results[device.Interface] = networking.RollbackResultOK
	}
	return results, nil
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
