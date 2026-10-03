package main

import (
	"context"
	"net/http"
	"time"

	"github.com/home-server-project/justvoxel/management/internal/networking"
)

// Keep extensions optional so existing Wi-Fi-only clients retain their contract.
type networkConfigurationClient interface {
	StageEthernet(context.Context, networking.EthernetSettings) (*networking.EthernetCandidate, error)
	PersistEthernet(context.Context, *networking.EthernetCandidate) error
	CheckConnectivity(context.Context) (string, error)
	ReconnectInterface(context.Context, string, string) error
}

func (s *server) networkEthernetConfigure(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	auditTarget, auditDetail := "ethernet", "configuration attempt rejected; persistent profile unchanged"
	auditSuccess := false
	defer func() {
		if s.store != nil {
			_ = s.store.recordAuditEvent(actor, "network_ethernet_stage", auditTarget, auditSuccess, auditDetail)
		}
	}()
	var request struct {
		CheckpointID string `json:"checkpoint_id"`
		networking.EthernetSettings
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Interface = r.PathValue("interface")
	if err := networking.ValidateEthernetSettings(request.EthernetSettings); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	auditTarget = request.Interface
	transaction, err := s.claimNetworkMutationCheckpoint(request.CheckpointID, []string{request.Interface})
	if err != nil {
		writeNetworkMutationCheckpointError(w, err)
		return
	}
	defer s.releaseNetworkCheckpoint(transaction.ID)
	if !transaction.ExpiresAt.After(networkNow().Add(20 * time.Second)) {
		writeError(w, http.StatusConflict, "checkpoint is too close to expiry; revert and create a fresh checkpoint")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	client, err := openNetworkClient(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Ethernet control is unavailable")
		return
	}
	defer client.Close()
	configuration, ok := client.(networkConfigurationClient)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "live Ethernet staging is unavailable")
		return
	}
	candidate, err := configuration.StageEthernet(ctx, request.EthernetSettings)
	if err != nil {
		rolledBack := s.rollbackFailedNetworkMutation(ctx, client, transaction)
		if s.store != nil {
			_ = s.store.recordAuditEvent(actor, "network_ethernet_rollback", request.Interface, rolledBack, "rollback requested; NetworkManager timeout remains authoritative")
		}
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.networkMu.Lock()
	current, exists := s.networkTransactions[transaction.ID]
	if exists {
		current.EthernetCandidate = candidate
		current.EthernetActor = actor
		s.networkTransactions[transaction.ID] = current
	}
	s.networkMu.Unlock()
	if !exists {
		writeError(w, http.StatusConflict, "checkpoint expired; persistent profile was not changed")
		return
	}
	auditSuccess, auditDetail = true, "live settings staged; persistent profile unchanged"
	checkpoint := networkCheckpointToView(current)
	writeJSON(w, http.StatusOK, networkWiFiMutationView{OK: true, Action: "ethernet-stage", Interface: request.Interface, Checkpoint: &checkpoint})
}

func (s *server) networkConnectivityCheck(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	client, err := openNetworkClient(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "connectivity check is unavailable")
		return
	}
	defer client.Close()
	configuration, ok := client.(networkConfigurationClient)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "connectivity check is unavailable")
		return
	}
	state, err := configuration.CheckConnectivity(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "NetworkManager connectivity check failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"connectivity": state})
}

func (s *server) networkReconnect(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	var request struct {
		CheckpointID string `json:"checkpoint_id"`
		ProfileUUID  string `json:"profile_uuid"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	interfaceName := r.PathValue("interface")
	if err := networking.ValidateInterface(interfaceName); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	transaction, err := s.claimNetworkMutationCheckpoint(request.CheckpointID, []string{interfaceName})
	if err != nil {
		writeNetworkMutationCheckpointError(w, err)
		return
	}
	defer s.releaseNetworkCheckpoint(transaction.ID)
	if !transaction.ExpiresAt.After(networkNow().Add(20 * time.Second)) {
		writeError(w, http.StatusConflict, "checkpoint is too close to expiry; revert and create a fresh checkpoint")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	client, err := openNetworkClient(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "network repair is unavailable")
		return
	}
	defer client.Close()
	configuration, ok := client.(networkConfigurationClient)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "network repair is unavailable")
		return
	}
	err = configuration.ReconnectInterface(ctx, interfaceName, request.ProfileUUID)
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "network_reconnect", interfaceName, err == nil, "reconnect active profile")
	}
	if err != nil {
		s.rollbackFailedNetworkMutation(ctx, client, transaction)
		writeError(w, http.StatusConflict, "active interface could not be reconnected")
		return
	}
	checkpoint := networkCheckpointToView(transaction)
	writeJSON(w, http.StatusOK, networkWiFiMutationView{OK: true, Action: "reconnect", Interface: interfaceName, Checkpoint: &checkpoint})
}
