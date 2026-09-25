package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/home-server-project/justvoxel/management/internal/networking"
)

func TestNetworkCheckpointCreateConfirmAndOverlap(t *testing.T) {
	fake := &fakeNetworkClient{}
	previousOpen := openNetworkClient
	previousNow := networkNow
	openNetworkClient = func(context.Context) (networkClient, error) { return fake, nil }
	networkNow = func() time.Time { return time.Date(2026, 9, 25, 22, 0, 0, 0, time.UTC) }
	t.Cleanup(func() {
		openNetworkClient = previousOpen
		networkNow = previousNow
	})

	s := &server{networkTransactions: make(map[string]networkCheckpointTransaction)}
	mux := http.NewServeMux()
	registerNetworkRoutes(mux, s)

	createBody := []byte(`{"interfaces":["enp1s0"],"rollback_timeout_seconds":90}`)
	createRequest := httptest.NewRequest(http.MethodPost, "/v1/admin/network/checkpoints", bytes.NewReader(createBody))
	createRequest = createRequest.WithContext(context.WithValue(createRequest.Context(), peerUIDKey{}, uint32(0)))
	createResponse := httptest.NewRecorder()
	mux.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body %s", createResponse.Code, createResponse.Body.String())
	}

	var created networkCheckpointView
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ID == "" || created.Status != "pending" {
		t.Fatalf("unexpected checkpoint view: %+v", created)
	}
	if created.RollbackTimeoutSeconds != 90 || created.ExpiresAt != "2026-09-25T22:01:30Z" {
		t.Fatalf("unexpected checkpoint timing: %+v", created)
	}

	overlapRequest := httptest.NewRequest(http.MethodPost, "/v1/admin/network/checkpoints", bytes.NewReader(createBody))
	overlapRequest = overlapRequest.WithContext(context.WithValue(overlapRequest.Context(), peerUIDKey{}, uint32(0)))
	overlapResponse := httptest.NewRecorder()
	mux.ServeHTTP(overlapResponse, overlapRequest)
	if overlapResponse.Code != http.StatusConflict {
		t.Fatalf("overlap status = %d, body %s", overlapResponse.Code, overlapResponse.Body.String())
	}

	confirmRequest := httptest.NewRequest(http.MethodPost, "/v1/admin/network/checkpoints/"+created.ID+"/confirm", nil)
	confirmRequest = confirmRequest.WithContext(context.WithValue(confirmRequest.Context(), peerUIDKey{}, uint32(0)))
	confirmResponse := httptest.NewRecorder()
	mux.ServeHTTP(confirmResponse, confirmRequest)
	if confirmResponse.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, body %s", confirmResponse.Code, confirmResponse.Body.String())
	}
	if fake.destroyed.Handle == "" {
		t.Fatal("checkpoint was not destroyed on confirm")
	}

	statusRequest := httptest.NewRequest(http.MethodGet, "/v1/admin/network/checkpoints/"+created.ID, nil)
	statusRequest = statusRequest.WithContext(context.WithValue(statusRequest.Context(), peerUIDKey{}, uint32(0)))
	statusResponse := httptest.NewRecorder()
	mux.ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusNotFound {
		t.Fatalf("confirmed checkpoint still active: %d", statusResponse.Code)
	}
}

func TestNetworkCheckpointRollbackReturnsInterfaceResults(t *testing.T) {
	fake := &fakeNetworkClient{rollbackResults: map[string]networking.RollbackResult{
		"enp1s0": networking.RollbackResultOK,
		"wlp2s0": networking.RollbackResultFailed,
	}}
	previousOpen := openNetworkClient
	previousNow := networkNow
	openNetworkClient = func(context.Context) (networkClient, error) { return fake, nil }
	networkNow = func() time.Time { return time.Date(2026, 9, 25, 22, 0, 0, 0, time.UTC) }
	t.Cleanup(func() {
		openNetworkClient = previousOpen
		networkNow = previousNow
	})

	s := &server{networkTransactions: make(map[string]networkCheckpointTransaction)}
	transaction, err := s.beginNetworkCheckpoint(context.Background(), []string{"enp1s0", "wlp2s0"}, 90)
	if err != nil {
		t.Fatalf("beginNetworkCheckpoint() error = %v", err)
	}

	mux := http.NewServeMux()
	registerNetworkRoutes(mux, s)
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/network/checkpoints/"+transaction.ID+"/rollback", nil)
	request = request.WithContext(context.WithValue(request.Context(), peerUIDKey{}, uint32(0)))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("rollback status = %d, body %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"ok":false`) ||
		!strings.Contains(response.Body.String(), `"interface":"enp1s0","result":"ok"`) ||
		!strings.Contains(response.Body.String(), `"interface":"wlp2s0","result":"failed"`) {
		t.Fatalf("unexpected rollback response: %s", response.Body.String())
	}
	if fake.rolledBack.Handle == "" {
		t.Fatal("checkpoint was not rolled back")
	}
}

func TestNetworkCheckpointTimeoutValidationAndExpiry(t *testing.T) {
	if got, err := normalizeNetworkCheckpointTimeout(0); err != nil || got != 90 {
		t.Fatalf("default timeout = %d, err %v", got, err)
	}
	for _, value := range []uint32{1, 29, 301, 999} {
		if _, err := normalizeNetworkCheckpointTimeout(value); err == nil {
			t.Fatalf("timeout %d was accepted", value)
		}
	}

	fake := &fakeNetworkClient{}
	previousOpen := openNetworkClient
	previousNow := networkNow
	now := time.Date(2026, 9, 25, 22, 0, 0, 0, time.UTC)
	openNetworkClient = func(context.Context) (networkClient, error) { return fake, nil }
	networkNow = func() time.Time { return now }
	t.Cleanup(func() {
		openNetworkClient = previousOpen
		networkNow = previousNow
	})

	s := &server{networkTransactions: make(map[string]networkCheckpointTransaction)}
	transaction, err := s.beginNetworkCheckpoint(context.Background(), []string{"enp1s0"}, 30)
	if err != nil {
		t.Fatalf("beginNetworkCheckpoint() error = %v", err)
	}
	now = now.Add(31 * time.Second)
	if _, ok := s.activeNetworkCheckpoint(transaction.ID); ok {
		t.Fatal("expired checkpoint remained active")
	}
}

func TestNetworkCheckpointClaimRejectsConcurrentTerminalAction(t *testing.T) {
	fake := &fakeNetworkClient{}
	previousOpen := openNetworkClient
	previousNow := networkNow
	openNetworkClient = func(context.Context) (networkClient, error) { return fake, nil }
	networkNow = func() time.Time { return time.Date(2026, 9, 25, 22, 0, 0, 0, time.UTC) }
	t.Cleanup(func() {
		openNetworkClient = previousOpen
		networkNow = previousNow
	})

	s := &server{networkTransactions: make(map[string]networkCheckpointTransaction)}
	transaction, err := s.beginNetworkCheckpoint(context.Background(), []string{"enp1s0"}, 90)
	if err != nil {
		t.Fatalf("beginNetworkCheckpoint() error = %v", err)
	}
	if _, err := s.claimNetworkCheckpoint(transaction.ID); err != nil {
		t.Fatalf("first claim error = %v", err)
	}
	if _, err := s.claimNetworkCheckpoint(transaction.ID); !errors.Is(err, errNetworkCheckpointBusy) {
		t.Fatalf("second claim error = %v, want busy", err)
	}
	s.releaseNetworkCheckpoint(transaction.ID)
	if _, err := s.claimNetworkCheckpoint(transaction.ID); err != nil {
		t.Fatalf("claim after release error = %v", err)
	}
}

func TestNetworkCheckpointInterfaceNormalization(t *testing.T) {
	got, err := normalizeNetworkCheckpointInterfaces([]string{" wlp2s0 ", "enp1s0", "wlp2s0"})
	if err != nil {
		t.Fatalf("normalizeNetworkCheckpointInterfaces() error = %v", err)
	}
	if strings.Join(got, ",") != "enp1s0,wlp2s0" {
		t.Fatalf("normalized interfaces = %v", got)
	}
	if _, err := normalizeNetworkCheckpointInterfaces(nil); err == nil {
		t.Fatal("empty checkpoint interface list was accepted")
	}
}
