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

const (
	defaultNetworkCheckpointTimeout = 90
	minNetworkCheckpointTimeout     = 30
	maxNetworkCheckpointTimeout     = 300
)

var (
	networkNow                   = time.Now
	errNetworkCheckpointOverlap  = errors.New("one or more interfaces are already protected by another network transaction")
	errNetworkCheckpointInactive = errors.New("network checkpoint is not active")
	errNetworkCheckpointBusy     = errors.New("network checkpoint operation is already in progress")
)

type networkCheckpointTransaction struct {
	ID                  string
	Checkpoint          networking.Checkpoint
	Interfaces          []string
	CreatedProfileUUIDs []string
	CreatedAt           time.Time
	ExpiresAt           time.Time
	Busy                bool
}

type networkCheckpointView struct {
	ID                     string   `json:"id"`
	Status                 string   `json:"status"`
	Interfaces             []string `json:"interfaces"`
	RollbackTimeoutSeconds uint32   `json:"rollback_timeout_seconds"`
	CreatedAt              string   `json:"created_at"`
	ExpiresAt              string   `json:"expires_at"`
}

type networkCheckpointRollbackDeviceView struct {
	Interface string `json:"interface"`
	Result    string `json:"result"`
}

type networkCheckpointRollbackView struct {
	OK      bool                                  `json:"ok"`
	ID      string                                `json:"id"`
	Results []networkCheckpointRollbackDeviceView `json:"results"`
}

func registerNetworkCheckpointRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("POST /v1/admin/network/checkpoints", s.networkCheckpointCreate)
	mux.HandleFunc("GET /v1/admin/network/checkpoints/{id}", s.networkCheckpointStatus)
	mux.HandleFunc("POST /v1/admin/network/checkpoints/{id}/confirm", s.networkCheckpointConfirm)
	mux.HandleFunc("POST /v1/admin/network/checkpoints/{id}/rollback", s.networkCheckpointRollback)
}

func (s *server) networkCheckpointCreate(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	var request struct {
		Interfaces             []string `json:"interfaces"`
		RollbackTimeoutSeconds uint32   `json:"rollback_timeout_seconds"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	interfaces, err := normalizeNetworkCheckpointInterfaces(request.Interfaces)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	timeout, err := normalizeNetworkCheckpointTimeout(request.RollbackTimeoutSeconds)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	transaction, err := s.beginNetworkCheckpoint(r.Context(), interfaces, timeout)
	if err != nil {
		status := http.StatusServiceUnavailable
		message := "network checkpoint could not be created"
		if errors.Is(err, errNetworkCheckpointOverlap) {
			status = http.StatusConflict
			message = errNetworkCheckpointOverlap.Error()
		}
		if s.store != nil {
			_ = s.store.recordAuditEvent(actor, "network_checkpoint_create", strings.Join(interfaces, ","), false, err.Error())
		}
		writeError(w, status, message)
		return
	}
	if s.store != nil {
		_ = s.store.recordAuditEvent(
			actor,
			"network_checkpoint_create",
			transaction.ID,
			true,
			fmt.Sprintf("interfaces=%s timeout=%ds", strings.Join(transaction.Interfaces, ","), timeout),
		)
	}
	writeJSON(w, http.StatusCreated, networkCheckpointToView(transaction))
}

func (s *server) networkCheckpointStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	transaction, ok := s.activeNetworkCheckpoint(strings.TrimSpace(r.PathValue("id")))
	if !ok {
		writeError(w, http.StatusNotFound, "network checkpoint is not active")
		return
	}
	writeJSON(w, http.StatusOK, networkCheckpointToView(transaction))
}

func (s *server) networkCheckpointConfirm(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	transaction, err := s.claimNetworkCheckpoint(id)
	if err != nil {
		writeNetworkCheckpointClaimError(w, err)
		return
	}
	finished := false
	defer func() {
		if !finished {
			s.releaseNetworkCheckpoint(id)
		}
	}()

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	client, err := openNetworkClient(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "network checkpoint could not be confirmed")
		return
	}
	defer client.Close()

	if err := client.DestroyCheckpoint(ctx, transaction.Checkpoint); err != nil {
		if s.store != nil {
			_ = s.store.recordAuditEvent(actor, "network_checkpoint_confirm", id, false, err.Error())
		}
		writeError(w, http.StatusServiceUnavailable, "network checkpoint could not be confirmed")
		return
	}
	s.removeNetworkCheckpoint(id)
	finished = true
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "network_checkpoint_confirm", id, true, strings.Join(transaction.Interfaces, ","))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"id":     id,
		"status": "confirmed",
	})
}

func (s *server) networkCheckpointRollback(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	transaction, err := s.claimNetworkCheckpoint(id)
	if err != nil {
		writeNetworkCheckpointClaimError(w, err)
		return
	}
	finished := false
	defer func() {
		if !finished {
			s.releaseNetworkCheckpoint(id)
		}
	}()

	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	client, err := openNetworkClient(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "network checkpoint could not be rolled back")
		return
	}
	defer client.Close()

	results, err := client.RollbackCheckpoint(ctx, transaction.Checkpoint)
	if err != nil {
		if s.store != nil {
			_ = s.store.recordAuditEvent(actor, "network_checkpoint_rollback", id, false, err.Error())
		}
		writeError(w, http.StatusServiceUnavailable, "network checkpoint could not be rolled back")
		return
	}
	cleanupOK := true
	for _, profileUUID := range transaction.CreatedProfileUUIDs {
		if err := client.ForgetWiFiProfile(ctx, profileUUID); err != nil {
			cleanupOK = false
		}
	}
	s.removeNetworkCheckpoint(id)
	finished = true

	items := make([]networkCheckpointRollbackDeviceView, 0, len(transaction.Interfaces))
	success := cleanupOK
	for _, interfaceName := range transaction.Interfaces {
		result := results[interfaceName]
		if result == "" {
			result = networking.RollbackResultUnknown
		}
		if result != networking.RollbackResultOK {
			success = false
		}
		items = append(items, networkCheckpointRollbackDeviceView{
			Interface: interfaceName,
			Result:    string(result),
		})
	}
	if s.store != nil {
		_ = s.store.recordAuditEvent(
			actor,
			"network_checkpoint_rollback",
			id,
			success,
			fmt.Sprintf("interfaces=%s", strings.Join(transaction.Interfaces, ",")),
		)
	}
	writeJSON(w, http.StatusOK, networkCheckpointRollbackView{OK: success, ID: id, Results: items})
}

func (s *server) beginNetworkCheckpoint(ctx context.Context, interfaces []string, timeout uint32) (networkCheckpointTransaction, error) {
	id, err := randomToken(18)
	if err != nil {
		return networkCheckpointTransaction{}, fmt.Errorf("create network transaction id: %w", err)
	}

	s.networkMu.Lock()
	defer s.networkMu.Unlock()
	s.cleanupExpiredNetworkCheckpointsLocked(networkNow().UTC())
	for _, transaction := range s.networkTransactions {
		if networkInterfacesOverlap(transaction.Interfaces, interfaces) {
			return networkCheckpointTransaction{}, errNetworkCheckpointOverlap
		}
	}

	client, err := openNetworkClient(ctx)
	if err != nil {
		return networkCheckpointTransaction{}, fmt.Errorf("network checkpoint service unavailable: %w", err)
	}
	defer client.Close()

	checkpoint, err := client.CreateCheckpoint(ctx, interfaces, timeout)
	if err != nil {
		return networkCheckpointTransaction{}, fmt.Errorf("create network checkpoint: %w", err)
	}
	createdAt := networkNow().UTC()
	transaction := networkCheckpointTransaction{
		ID:         id,
		Checkpoint: checkpoint,
		Interfaces: append([]string(nil), interfaces...),
		CreatedAt:  createdAt,
		ExpiresAt:  createdAt.Add(time.Duration(timeout) * time.Second),
	}
	if s.networkTransactions == nil {
		s.networkTransactions = make(map[string]networkCheckpointTransaction)
	}
	s.networkTransactions[id] = transaction
	return transaction, nil
}

func (s *server) activeNetworkCheckpoint(id string) (networkCheckpointTransaction, bool) {
	if id == "" {
		return networkCheckpointTransaction{}, false
	}
	s.networkMu.Lock()
	defer s.networkMu.Unlock()
	s.cleanupExpiredNetworkCheckpointsLocked(networkNow().UTC())
	transaction, ok := s.networkTransactions[id]
	return transaction, ok
}

func (s *server) claimNetworkCheckpoint(id string) (networkCheckpointTransaction, error) {
	if id == "" {
		return networkCheckpointTransaction{}, errNetworkCheckpointInactive
	}
	s.networkMu.Lock()
	defer s.networkMu.Unlock()
	s.cleanupExpiredNetworkCheckpointsLocked(networkNow().UTC())
	transaction, ok := s.networkTransactions[id]
	if !ok {
		return networkCheckpointTransaction{}, errNetworkCheckpointInactive
	}
	if transaction.Busy {
		return networkCheckpointTransaction{}, errNetworkCheckpointBusy
	}
	transaction.Busy = true
	s.networkTransactions[id] = transaction
	return transaction, nil
}

func (s *server) releaseNetworkCheckpoint(id string) {
	s.networkMu.Lock()
	defer s.networkMu.Unlock()
	transaction, ok := s.networkTransactions[id]
	if !ok {
		return
	}
	transaction.Busy = false
	s.networkTransactions[id] = transaction
}

func (s *server) addNetworkCreatedProfile(id, profileUUID string) {
	profileUUID = strings.TrimSpace(profileUUID)
	if id == "" || profileUUID == "" {
		return
	}
	s.networkMu.Lock()
	defer s.networkMu.Unlock()
	transaction, ok := s.networkTransactions[id]
	if !ok {
		return
	}
	for _, existing := range transaction.CreatedProfileUUIDs {
		if existing == profileUUID {
			return
		}
	}
	transaction.CreatedProfileUUIDs = append(transaction.CreatedProfileUUIDs, profileUUID)
	s.networkTransactions[id] = transaction
}

func (s *server) removeNetworkCheckpoint(id string) {
	s.networkMu.Lock()
	delete(s.networkTransactions, id)
	s.networkMu.Unlock()
}

func writeNetworkCheckpointClaimError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNetworkCheckpointInactive):
		writeError(w, http.StatusNotFound, errNetworkCheckpointInactive.Error())
	case errors.Is(err, errNetworkCheckpointBusy):
		writeError(w, http.StatusConflict, errNetworkCheckpointBusy.Error())
	default:
		writeError(w, http.StatusServiceUnavailable, "network checkpoint is unavailable")
	}
}

func (s *server) cleanupExpiredNetworkCheckpointsLocked(now time.Time) {
	for id, transaction := range s.networkTransactions {
		if !transaction.ExpiresAt.After(now) {
			delete(s.networkTransactions, id)
		}
	}
}

func normalizeNetworkCheckpointInterfaces(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("at least one network interface is required")
	}
	if len(values) > 8 {
		return nil, fmt.Errorf("too many network interfaces requested")
	}
	seen := make(map[string]struct{}, len(values))
	interfaces := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			return nil, fmt.Errorf("network interface is required")
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		interfaces = append(interfaces, value)
	}
	sort.Strings(interfaces)
	return interfaces, nil
}

func normalizeNetworkCheckpointTimeout(value uint32) (uint32, error) {
	if value == 0 {
		return defaultNetworkCheckpointTimeout, nil
	}
	if value < minNetworkCheckpointTimeout || value > maxNetworkCheckpointTimeout {
		return 0, fmt.Errorf(
			"rollback timeout must be between %d and %d seconds",
			minNetworkCheckpointTimeout,
			maxNetworkCheckpointTimeout,
		)
	}
	return value, nil
}

func networkInterfacesOverlap(left, right []string) bool {
	seen := make(map[string]struct{}, len(left))
	for _, value := range left {
		seen[value] = struct{}{}
	}
	for _, value := range right {
		if _, ok := seen[value]; ok {
			return true
		}
	}
	return false
}

func networkCheckpointToView(transaction networkCheckpointTransaction) networkCheckpointView {
	return networkCheckpointView{
		ID:                     transaction.ID,
		Status:                 "pending",
		Interfaces:             append([]string(nil), transaction.Interfaces...),
		RollbackTimeoutSeconds: transaction.Checkpoint.RollbackTimeoutSeconds,
		CreatedAt:              transaction.CreatedAt.Format(time.RFC3339),
		ExpiresAt:              transaction.ExpiresAt.Format(time.RFC3339),
	}
}
