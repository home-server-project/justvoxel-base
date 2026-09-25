package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/home-server-project/justvoxel/management/internal/networking"
)

func TestNetworkWiFiSavedConnectUsesArmedCheckpoint(t *testing.T) {
	fake := &fakeNetworkClient{}
	previousOpen := openNetworkClient
	previousNow := networkNow
	openNetworkClient = func(context.Context) (networkClient, error) { return fake, nil }
	networkNow = func() time.Time { return time.Date(2026, 9, 25, 23, 30, 0, 0, time.UTC) }
	t.Cleanup(func() {
		openNetworkClient = previousOpen
		networkNow = previousNow
	})

	s := &server{networkTransactions: make(map[string]networkCheckpointTransaction)}
	transaction, err := s.beginNetworkCheckpoint(context.Background(), []string{"wlp2s0"}, 90)
	if err != nil {
		t.Fatalf("beginNetworkCheckpoint() error = %v", err)
	}

	mux := http.NewServeMux()
	registerNetworkRoutes(mux, s)
	body := []byte(`{"checkpoint_id":"` + transaction.ID + `","profile_uuid":"profile-1"}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/network/wifi/wlp2s0/connect", bytes.NewReader(body))
	request = request.WithContext(context.WithValue(request.Context(), peerUIDKey{}, uint32(0)))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("connect status = %d, body %s", response.Code, response.Body.String())
	}
	if fake.activated != "wlp2s0:profile-1" {
		t.Fatalf("saved profile activation = %q", fake.activated)
	}
	if _, ok := s.activeNetworkCheckpoint(transaction.ID); !ok {
		t.Fatal("successful Wi-Fi mutation removed pending checkpoint before confirmation")
	}
}

func TestNetworkWiFiConnectRejectsCheckpointForOtherInterface(t *testing.T) {
	fake := &fakeNetworkClient{}
	previousOpen := openNetworkClient
	previousNow := networkNow
	openNetworkClient = func(context.Context) (networkClient, error) { return fake, nil }
	networkNow = func() time.Time { return time.Date(2026, 9, 25, 23, 30, 0, 0, time.UTC) }
	t.Cleanup(func() {
		openNetworkClient = previousOpen
		networkNow = previousNow
	})

	s := &server{networkTransactions: make(map[string]networkCheckpointTransaction)}
	transaction, err := s.beginNetworkCheckpoint(context.Background(), []string{"wlp3s0"}, 90)
	if err != nil {
		t.Fatalf("beginNetworkCheckpoint() error = %v", err)
	}
	mux := http.NewServeMux()
	registerNetworkRoutes(mux, s)

	body, _ := json.Marshal(map[string]string{
		"checkpoint_id": transaction.ID,
		"profile_uuid":  "profile-1",
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/network/wifi/wlp2s0/connect", bytes.NewReader(body))
	request = request.WithContext(context.WithValue(request.Context(), peerUIDKey{}, uint32(0)))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("coverage status = %d, body %s", response.Code, response.Body.String())
	}
	if fake.activated != "" {
		t.Fatalf("profile activated despite checkpoint mismatch: %q", fake.activated)
	}
}

func TestNetworkWiFiRadioOffRequiresCheckpointForAllWiFiDevices(t *testing.T) {
	fake := &fakeNetworkClient{
		snapshot: networking.Snapshot{
			Devices: []networking.Device{
				{Interface: "wlp2s0", Kind: networking.DeviceKindWiFi},
				{Interface: "wlp3s0", Kind: networking.DeviceKindWiFi},
				{Interface: "enp1s0", Kind: networking.DeviceKindEthernet},
			},
		},
	}
	previousOpen := openNetworkClient
	previousNow := networkNow
	openNetworkClient = func(context.Context) (networkClient, error) { return fake, nil }
	networkNow = func() time.Time { return time.Date(2026, 9, 25, 23, 30, 0, 0, time.UTC) }
	t.Cleanup(func() {
		openNetworkClient = previousOpen
		networkNow = previousNow
	})

	s := &server{networkTransactions: make(map[string]networkCheckpointTransaction)}
	transaction, err := s.beginNetworkCheckpoint(context.Background(), []string{"wlp2s0", "wlp3s0"}, 90)
	if err != nil {
		t.Fatalf("beginNetworkCheckpoint() error = %v", err)
	}
	mux := http.NewServeMux()
	registerNetworkRoutes(mux, s)

	body, _ := json.Marshal(map[string]any{
		"enabled":       false,
		"checkpoint_id": transaction.ID,
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/network/wifi/radio", bytes.NewReader(body))
	request = request.WithContext(context.WithValue(request.Context(), peerUIDKey{}, uint32(0)))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("radio status = %d, body %s", response.Code, response.Body.String())
	}
	if fake.wirelessEnabled == nil || *fake.wirelessEnabled {
		t.Fatalf("wireless enabled = %v, want false", fake.wirelessEnabled)
	}
}

func TestNetworkWiFiForgetRejectsActiveProfile(t *testing.T) {
	fake := &fakeNetworkClient{forgetError: networking.ErrWiFiProfileActive}
	previousOpen := openNetworkClient
	openNetworkClient = func(context.Context) (networkClient, error) { return fake, nil }
	t.Cleanup(func() {
		openNetworkClient = previousOpen
	})

	s := &server{networkTransactions: make(map[string]networkCheckpointTransaction)}
	mux := http.NewServeMux()
	registerNetworkRoutes(mux, s)
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/network/wifi/profiles/profile-active/forget", nil)
	request = request.WithContext(context.WithValue(request.Context(), peerUIDKey{}, uint32(0)))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("forget status = %d, body %s", response.Code, response.Body.String())
	}
}
