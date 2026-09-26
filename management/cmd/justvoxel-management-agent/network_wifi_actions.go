package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/home-server-project/justvoxel/management/internal/networking"
)

var errNetworkCheckpointCoverage = errors.New("network checkpoint does not cover the required interface")

type networkWiFiMutationView struct {
	OK          bool                   `json:"ok"`
	Action      string                 `json:"action"`
	Interface   string                 `json:"interface,omitempty"`
	ProfileUUID string                 `json:"profile_uuid,omitempty"`
	Checkpoint  *networkCheckpointView `json:"checkpoint,omitempty"`
}

func registerNetworkWiFiActionRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("POST /v1/admin/network/wifi/radio", s.networkWiFiRadio)
	mux.HandleFunc("POST /v1/admin/network/wifi/{interface}/connect", s.networkWiFiConnect)
	mux.HandleFunc("POST /v1/admin/network/wifi/{interface}/disconnect", s.networkWiFiDisconnect)
	mux.HandleFunc("POST /v1/admin/network/wifi/profiles/{uuid}/forget", s.networkWiFiForget)
}

func (s *server) networkWiFiRadio(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	var request struct {
		Enabled      bool   `json:"enabled"`
		CheckpointID string `json:"checkpoint_id,omitempty"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	client, err := openNetworkClient(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Wi-Fi control is unavailable")
		return
	}
	defer client.Close()

	var transaction networkCheckpointTransaction
	claimed := false
	if !request.Enabled {
		snapshot, err := client.Snapshot(ctx)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "Wi-Fi state is unavailable")
			return
		}
		interfaces := make([]string, 0)
		for _, device := range snapshot.Devices {
			if device.Kind == networking.DeviceKindWiFi {
				interfaces = append(interfaces, device.Interface)
			}
		}
		sort.Strings(interfaces)
		if len(interfaces) > 0 {
			transaction, err = s.claimNetworkMutationCheckpoint(request.CheckpointID, interfaces)
			if err != nil {
				writeNetworkMutationCheckpointError(w, err)
				return
			}
			claimed = true
			defer s.releaseNetworkCheckpoint(transaction.ID)
		}
	}

	if err := client.SetWirelessEnabled(ctx, request.Enabled); err != nil {
		if claimed {
			s.rollbackFailedNetworkMutation(ctx, client, transaction)
		}
		if s.store != nil {
			_ = s.store.recordAuditEvent(actor, "network_wifi_radio", fmt.Sprintf("enabled=%t", request.Enabled), false, "NetworkManager rejected change")
		}
		writeError(w, http.StatusServiceUnavailable, "Wi-Fi radio state could not be changed")
		return
	}
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "network_wifi_radio", fmt.Sprintf("enabled=%t", request.Enabled), true, "")
	}

	view := networkWiFiMutationView{OK: true, Action: "radio"}
	if claimed {
		checkpoint := networkCheckpointToView(transaction)
		view.Checkpoint = &checkpoint
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *server) networkWiFiConnect(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	interfaceName := strings.TrimSpace(r.PathValue("interface"))
	var request struct {
		CheckpointID  string `json:"checkpoint_id"`
		ProfileUUID   string `json:"profile_uuid,omitempty"`
		SSID          string `json:"ssid,omitempty"`
		BSSID         string `json:"bssid,omitempty"`
		KeyManagement string `json:"key_management,omitempty"`
		Password      string `json:"password,omitempty"`
		Hidden        bool   `json:"hidden,omitempty"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}

	transaction, err := s.claimNetworkMutationCheckpoint(request.CheckpointID, []string{interfaceName})
	if err != nil {
		writeNetworkMutationCheckpointError(w, err)
		return
	}
	defer s.releaseNetworkCheckpoint(transaction.ID)

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	client, err := openNetworkClient(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Wi-Fi control is unavailable")
		return
	}
	defer client.Close()

	action := "connect-saved"
	profileUUID := strings.TrimSpace(request.ProfileUUID)
	if profileUUID != "" {
		if err := client.ActivateWiFiProfile(ctx, interfaceName, profileUUID); err != nil {
			s.rollbackFailedNetworkMutation(ctx, client, transaction)
			if s.store != nil {
				_ = s.store.recordAuditEvent(actor, "network_wifi_connect", interfaceName, false, "saved profile activation failed")
			}
			writeError(w, http.StatusServiceUnavailable, "Saved Wi-Fi network could not be connected")
			return
		}
	} else {
		action = "connect-new"
		secret := networking.NewSecret(request.Password)
		request.Password = ""
		result, err := client.ConnectWiFi(ctx, networking.WiFiConnectRequest{
			Interface:     interfaceName,
			SSID:          request.SSID,
			BSSID:         request.BSSID,
			KeyManagement: request.KeyManagement,
			Password:      secret,
			Hidden:        request.Hidden,
		})
		secret.Clear()
		if result.ProfileUUID != "" {
			s.addNetworkCreatedProfile(transaction.ID, result.ProfileUUID)
		}
		if err != nil {
			s.rollbackFailedNetworkMutation(ctx, client, transaction)
			if s.store != nil {
				_ = s.store.recordAuditEvent(actor, "network_wifi_connect", interfaceName, false, "new profile activation failed")
			}
			status := http.StatusBadRequest
			if strings.Contains(err.Error(), "no longer available") || strings.Contains(err.Error(), "security changed") {
				status = http.StatusConflict
			}
			writeError(w, status, err.Error())
			return
		}
		profileUUID = result.ProfileUUID
	}

	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "network_wifi_connect", interfaceName, true, action)
	}
	checkpoint := networkCheckpointToView(transaction)
	writeJSON(w, http.StatusOK, networkWiFiMutationView{
		OK:          true,
		Action:      action,
		Interface:   interfaceName,
		ProfileUUID: profileUUID,
		Checkpoint:  &checkpoint,
	})
}

func (s *server) networkWiFiDisconnect(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	interfaceName := strings.TrimSpace(r.PathValue("interface"))
	var request struct {
		CheckpointID string `json:"checkpoint_id"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}

	transaction, err := s.claimNetworkMutationCheckpoint(request.CheckpointID, []string{interfaceName})
	if err != nil {
		writeNetworkMutationCheckpointError(w, err)
		return
	}
	defer s.releaseNetworkCheckpoint(transaction.ID)

	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	client, err := openNetworkClient(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Wi-Fi control is unavailable")
		return
	}
	defer client.Close()

	if err := client.DisconnectWiFi(ctx, interfaceName); err != nil {
		s.rollbackFailedNetworkMutation(ctx, client, transaction)
		if s.store != nil {
			_ = s.store.recordAuditEvent(actor, "network_wifi_disconnect", interfaceName, false, "disconnect failed")
		}
		writeError(w, http.StatusServiceUnavailable, "Wi-Fi could not be disconnected")
		return
	}
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "network_wifi_disconnect", interfaceName, true, "")
	}
	checkpoint := networkCheckpointToView(transaction)
	writeJSON(w, http.StatusOK, networkWiFiMutationView{
		OK:         true,
		Action:     "disconnect",
		Interface:  interfaceName,
		Checkpoint: &checkpoint,
	})
}

func (s *server) networkWiFiForget(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	profileUUID := strings.TrimSpace(r.PathValue("uuid"))
	if profileUUID == "" {
		writeError(w, http.StatusBadRequest, "Wi-Fi profile UUID is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	client, err := openNetworkClient(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Wi-Fi control is unavailable")
		return
	}
	defer client.Close()

	if err := client.ForgetWiFiProfile(ctx, profileUUID); err != nil {
		if s.store != nil {
			_ = s.store.recordAuditEvent(actor, "network_wifi_forget", profileUUID, false, "forget failed")
		}
		if errors.Is(err, networking.ErrWiFiProfileActive) {
			writeError(w, http.StatusConflict, "Disconnect and confirm this Wi-Fi network before forgetting it")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "network_wifi_forget", profileUUID, true, "")
	}
	writeJSON(w, http.StatusOK, networkWiFiMutationView{OK: true, Action: "forget", ProfileUUID: profileUUID})
}

func (s *server) claimNetworkMutationCheckpoint(id string, requiredInterfaces []string) (networkCheckpointTransaction, error) {
	transaction, err := s.claimNetworkCheckpoint(strings.TrimSpace(id))
	if err != nil {
		return networkCheckpointTransaction{}, err
	}
	covered := make(map[string]struct{}, len(transaction.Interfaces))
	for _, interfaceName := range transaction.Interfaces {
		covered[interfaceName] = struct{}{}
	}
	for _, interfaceName := range requiredInterfaces {
		if _, ok := covered[strings.TrimSpace(interfaceName)]; !ok {
			s.releaseNetworkCheckpoint(transaction.ID)
			return networkCheckpointTransaction{}, errNetworkCheckpointCoverage
		}
	}
	return transaction, nil
}

func writeNetworkMutationCheckpointError(w http.ResponseWriter, err error) {
	if errors.Is(err, errNetworkCheckpointCoverage) {
		writeError(w, http.StatusConflict, errNetworkCheckpointCoverage.Error())
		return
	}
	writeNetworkCheckpointClaimError(w, err)
}

func (s *server) rollbackFailedNetworkMutation(
	ctx context.Context,
	client networkClient,
	transaction networkCheckpointTransaction,
) {
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 8*time.Second)
	defer cancel()
	if _, err := client.RollbackCheckpoint(rollbackCtx, transaction.Checkpoint); err == nil {
		s.removeNetworkCheckpoint(transaction.ID)
	}
}
