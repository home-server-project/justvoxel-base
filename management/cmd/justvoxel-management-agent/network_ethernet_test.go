package main

import (
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

type fakeNetworkConfigurationClient struct {
	fakeNetworkClient
	staged, persisted          int
	order                      []string
	destroyError, persistError error
}

func (f *fakeNetworkConfigurationClient) StageEthernet(_ context.Context, s networking.EthernetSettings) (*networking.EthernetCandidate, error) {
	f.staged++
	return &networking.EthernetCandidate{Settings: s}, nil
}
func (f *fakeNetworkConfigurationClient) PersistEthernet(context.Context, *networking.EthernetCandidate) error {
	f.order = append(f.order, "persist")
	f.persisted++
	return f.persistError
}
func (f *fakeNetworkConfigurationClient) DestroyCheckpoint(ctx context.Context, checkpoint networking.Checkpoint) error {
	f.order = append(f.order, "destroy")
	if f.destroyError != nil {
		return f.destroyError
	}
	return f.fakeNetworkClient.DestroyCheckpoint(ctx, checkpoint)
}
func (f *fakeNetworkConfigurationClient) CheckConnectivity(context.Context) (string, error) {
	return "full", nil
}
func (f *fakeNetworkConfigurationClient) ReconnectInterface(context.Context, string, string) error {
	return nil
}

func networkAdminRequest(s *server, path, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	registerNetworkRoutes(mux, s)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, authorizedRequest(http.MethodPost, path, body))
	return rr
}

func ethernetAgentFixture(t *testing.T) (*server, *fakeNetworkConfigurationClient, *time.Time) {
	t.Helper()
	oldOpen, oldNow := openNetworkClient, networkNow
	fake := &fakeNetworkConfigurationClient{}
	now := time.Now().UTC()
	openNetworkClient = func(context.Context) (networkClient, error) { return fake, nil }
	networkNow = func() time.Time { return now }
	t.Cleanup(func() { openNetworkClient = oldOpen; networkNow = oldNow })
	return adminServerForTest(), fake, &now
}

func ethernetAgentBody(id string) string {
	body, _ := json.Marshal(map[string]any{"checkpoint_id": id, "profile_uuid": "12345678-1234-1234-1234-123456789abc", "method": "manual", "address": "192.0.2.20", "prefix": 24, "gateway": "192.0.2.1", "dns": []string{"192.0.2.53"}, "mtu": 1500, "autoconnect": true})
	return string(body)
}

func TestEthernetAgentRequiresAdministratorAndCoveredCheckpoint(t *testing.T) {
	s, fake, _ := ethernetAgentFixture(t)
	for _, role := range []principalRole{roleOperator, roleViewer} {
		if rr := networkAdminRequest(roleServerForTest(role), "/v1/admin/network/ethernet/enp1s0", ethernetAgentBody("missing")); rr.Code != http.StatusForbidden {
			t.Fatalf("role %s status %d", role, rr.Code)
		}
	}
	if rr := networkAdminRequest(s, "/v1/admin/network/ethernet/enp1s0", ethernetAgentBody("missing")); rr.Code != http.StatusNotFound {
		t.Fatalf("missing checkpoint status %d", rr.Code)
	}
	transaction, err := s.beginNetworkCheckpoint(context.Background(), []string{"enp2s0"}, 90)
	if err != nil {
		t.Fatal(err)
	}
	if rr := networkAdminRequest(s, "/v1/admin/network/ethernet/enp1s0", ethernetAgentBody(transaction.ID)); rr.Code != http.StatusConflict {
		t.Fatalf("wrong interface checkpoint status %d", rr.Code)
	}
	if fake.staged != 0 || fake.persisted != 0 {
		t.Fatal("unprotected or unauthorized change reached backend")
	}
}

func TestEthernetAgentInvalidInputRejectedBeforeStaging(t *testing.T) {
	s, fake, _ := ethernetAgentFixture(t)
	body := strings.Replace(ethernetAgentBody("missing"), "192.0.2.20", "invalid", 1)
	if rr := networkAdminRequest(s, "/v1/admin/network/ethernet/enp1s0", body); rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid input status %d", rr.Code)
	}
	if fake.staged != 0 {
		t.Fatal("invalid input staged")
	}
}

func TestEthernetAgentPersistsOnlyAfterSuccessfulConfirmation(t *testing.T) {
	for _, outcome := range []string{"confirm", "destroy-fails", "persist-fails", "rollback", "timeout"} {
		t.Run(outcome, func(t *testing.T) {
			s, fake, now := ethernetAgentFixture(t)
			transaction, err := s.beginNetworkCheckpoint(context.Background(), []string{"enp1s0"}, 90)
			if err != nil {
				t.Fatal(err)
			}
			if rr := networkAdminRequest(s, "/v1/admin/network/ethernet/enp1s0", ethernetAgentBody(transaction.ID)); rr.Code != http.StatusOK {
				t.Fatalf("stage %d: %s", rr.Code, rr.Body.String())
			}
			if fake.staged != 1 || fake.persisted != 0 {
				t.Fatal("candidate persisted before Keep settings")
			}
			if _, err := s.claimNetworkMutationCheckpoint(transaction.ID, []string{"enp1s0"}); err == nil {
				t.Fatal("another mutation accepted over staged Ethernet settings")
			}
			if outcome == "timeout" {
				*now = now.Add(91 * time.Second)
				if _, ok := s.activeNetworkCheckpoint(transaction.ID); ok {
					t.Fatal("expired candidate retained")
				}
				if fake.persisted != 0 {
					t.Fatal("timeout persisted candidate")
				}
				return
			}
			terminal := "confirm"
			wantStatus, wantWrites := http.StatusOK, 1
			if outcome == "rollback" {
				terminal, wantWrites = "rollback", 0
			}
			if outcome == "destroy-fails" {
				fake.destroyError = errors.New("checkpoint expired")
				wantStatus, wantWrites = http.StatusServiceUnavailable, 0
			}
			if outcome == "persist-fails" {
				fake.persistError = errors.New("disk unavailable")
				wantStatus = http.StatusServiceUnavailable
			}
			rr := networkAdminRequest(s, "/v1/admin/network/checkpoints/"+transaction.ID+"/"+terminal, "")
			if rr.Code != wantStatus || fake.persisted != wantWrites {
				t.Fatalf("terminal status %d, writes %d, body %s", rr.Code, fake.persisted, rr.Body.String())
			}
			if wantWrites == 1 && strings.Join(fake.order, ",") != "destroy,persist" {
				t.Fatalf("unsafe ordering: %v", fake.order)
			}
			if outcome == "persist-fails" && !strings.Contains(rr.Body.String(), "saving the persistent profile failed") {
				t.Fatal("persistence failure hidden")
			}
		})
	}
}

func TestEthernetRejectsStagingNearCheckpointExpiry(t *testing.T) {
	s, fake, now := ethernetAgentFixture(t)
	transaction, err := s.beginNetworkCheckpoint(context.Background(), []string{"enp1s0"}, 90)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(75 * time.Second)
	if rr := networkAdminRequest(s, "/v1/admin/network/ethernet/enp1s0", ethernetAgentBody(transaction.ID)); rr.Code != http.StatusConflict {
		t.Fatalf("near-expiry stage status %d", rr.Code)
	}
	if fake.staged != 0 || fake.persisted != 0 {
		t.Fatal("near-expiry stage reached backend")
	}
}

func TestPendingEthernetRecoveryExposesOnlyOpaqueIDsToAdministrators(t *testing.T) {
	s, _, _ := ethernetAgentFixture(t)
	transaction, err := s.beginNetworkCheckpoint(context.Background(), []string{"enp1s0"}, 90)
	if err != nil {
		t.Fatal(err)
	}
	if rr := networkAdminRequest(s, "/v1/admin/network/ethernet/enp1s0", ethernetAgentBody(transaction.ID)); rr.Code != http.StatusOK {
		t.Fatal(rr.Body.String())
	}
	for _, role := range []principalRole{roleAdministrator, roleOperator, roleViewer} {
		sess := s.sessions["token"]
		sess.Role = role
		s.sessions["token"] = sess
		mux := http.NewServeMux()
		registerNetworkRoutes(mux, s)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, authorizedRequest(http.MethodGet, "/v1/network", ""))
		if rr.Code != http.StatusOK {
			t.Fatalf("role %s status %d", role, rr.Code)
		}
		visible := strings.Contains(rr.Body.String(), transaction.ID)
		if visible != (role == roleAdministrator) {
			t.Fatalf("role %s checkpoint visibility %t", role, visible)
		}
		if strings.Contains(rr.Body.String(), "/checkpoint/") || strings.Contains(rr.Body.String(), "/device/") {
			t.Fatal("raw D-Bus handles exposed")
		}
	}
}

func TestEthernetAuditsStagingPersistenceAndTimeout(t *testing.T) {
	s, _, now := ethernetAgentFixture(t)
	store, _ := openTestWebUIStore(t)
	s.store = store
	for _, outcome := range []string{"confirm", "timeout"} {
		transaction, err := s.beginNetworkCheckpoint(context.Background(), []string{"enp1s0"}, 90)
		if err != nil {
			t.Fatal(err)
		}
		if rr := networkAdminRequest(s, "/v1/admin/network/ethernet/enp1s0", ethernetAgentBody(transaction.ID)); rr.Code != http.StatusOK {
			t.Fatal(rr.Body.String())
		}
		if outcome == "confirm" {
			if rr := networkAdminRequest(s, "/v1/admin/network/checkpoints/"+transaction.ID+"/confirm", ""); rr.Code != http.StatusOK {
				t.Fatal(rr.Body.String())
			}
		} else {
			*now = now.Add(91 * time.Second)
			s.activeNetworkCheckpoint(transaction.ID)
		}
	}
	if rr := networkAdminRequest(s, "/v1/admin/network/ethernet/enp1s0", `{"method":"invalid"}`); rr.Code != http.StatusBadRequest {
		t.Fatal("invalid input accepted")
	}
	events, err := store.listAuditEvents(20)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, event := range events {
		found[event.Action] = true
	}
	for _, action := range []string{"network_ethernet_stage", "network_ethernet_persist", "network_ethernet_timeout"} {
		if !found[action] {
			t.Fatalf("missing audit %s", action)
		}
	}
	if events[0].Success {
		t.Fatal("invalid configuration attempt audited as successful")
	}
}
