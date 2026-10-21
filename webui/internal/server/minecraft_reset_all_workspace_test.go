package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type applianceMinecraftResetAPI struct {
	*applianceFactoryResetAPI
	selectedOperation string
}

func (f *applianceMinecraftResetAPI) AdminMinecraftResetPlan(context.Context, string) (api.AdminResetPlanResponse, error) {
	f.calls = append(f.calls, "minecraft-plan")
	return api.AdminResetPlanResponse{OK: true, Mode: "minecraft", PlanFingerprint: "reviewed-all", Participants: []api.ResetParticipant{{ID: "jv-family1", DisplayName: "Family", State: "running"}, {ID: "jv-sibling", DisplayName: "Creative", State: "stopped"}}}, nil
}
func (f *applianceMinecraftResetAPI) AdminMinecraftResetApply(_ context.Context, _ string, r api.AdminMinecraftResetApplyRequest) (api.AdminResetApplyResponse, error) {
	f.calls = append(f.calls, "minecraft-apply:"+r.PlanFingerprint)
	return api.AdminResetApplyResponse{OK: true, Operation: &api.PersistentOperation{OperationType: "minecraft_reset", State: "queued"}}, nil
}
func (f *applianceMinecraftResetAPI) AdminCurrentMinecraftResetOperation(context.Context, string) (api.PersistentOperationResponse, error) {
	f.calls = append(f.calls, "minecraft-current")
	return api.PersistentOperationResponse{Operation: &api.PersistentOperation{OperationType: "minecraft_reset", InstanceID: f.selectedOperation, State: "needs_attention"}}, nil
}
func (f *applianceMinecraftResetAPI) AdminOperation(_ context.Context, _ string, id string) (api.PersistentOperationResponse, error) {
	f.calls = append(f.calls, "minecraft-operation")
	return api.PersistentOperationResponse{Operation: &api.PersistentOperation{OperationID: id, OperationType: "minecraft_reset", InstanceID: f.selectedOperation, State: "needs_attention"}}, nil
}
func TestSystemMinecraftResetIgnoresSelection(t *testing.T) {
	for _, query := range []string{"", "?server=jv-family1", "?server=jv-sibling", "?server=invalid&server=another"} {
		t.Run(query, func(t *testing.T) {
			app, base := phase7App(t)
			f := &applianceMinecraftResetAPI{applianceFactoryResetAPI: &applianceFactoryResetAPI{phase7API: base}}
			app.api = f
			for _, request := range []struct {
				method, path, body, want string
				status                   int
			}{
				{http.MethodPost, "/api/system/workspace/reset/minecraft/plan", "csrf=csrf-token", "minecraft-plan", 200},
				{http.MethodPost, "/api/system/workspace/reset/minecraft/apply", "csrf=csrf-token&plan_fingerprint=reviewed-all&confirm_players=yes", "minecraft-apply:reviewed-all", 202},
				{http.MethodGet, "/api/system/workspace/reset/minecraft/current", "", "minecraft-current", 200},
				{http.MethodGet, "/api/system/workspace/reset/operations/12345678-1234-4123-8123-123456789abc", "", "minecraft-operation", 200},
			} {
				rr := phase7Request(app, request.method, request.path+query, request.body)
				if rr.Code != request.status || len(f.calls) == 0 || f.calls[len(f.calls)-1] != request.want {
					t.Fatal(request.path, rr.Code, rr.Body.String(), f.calls)
				}
				if request.want == "minecraft-plan" && (!strings.Contains(rr.Body.String(), "Family") || !strings.Contains(rr.Body.String(), "Creative")) {
					t.Fatal("participant list lost", rr.Body.String())
				}
			}
			// Empty InstanceID bypasses selection, while historical selected resets keep
			// their strict immutable-instance isolation.
			f.selectedOperation = "jv-family1"
			rr := phase7Request(app, http.MethodGet, "/api/system/workspace/reset/operations/12345678-1234-4123-8123-123456789abc?server=jv-sibling", "")
			if rr.Code != 409 {
				t.Fatal("selected reset isolation weakened", rr.Code, rr.Body.String())
			}
		})
	}
}

func (f *applianceMinecraftResetAPI) HistoricalMinecraftResetPlan(context.Context, string) (api.AdminResetPlanResponse, error) {
	f.calls = append(f.calls, "historical-plan")
	return api.AdminResetPlanResponse{OK: true, Mode: "minecraft", InstanceID: f.selectedOperation}, nil
}
func (f *applianceMinecraftResetAPI) HistoricalMinecraftResetApply(context.Context, string, api.AdminMinecraftResetApplyRequest) (api.AdminResetApplyResponse, error) {
	f.calls = append(f.calls, "historical-apply")
	return api.AdminResetApplyResponse{OK: true, Operation: &api.PersistentOperation{OperationType: "minecraft_reset", InstanceID: f.selectedOperation}}, nil
}
func TestSystemKeepsHistoricalResetRecoveryIndependentOfSelector(t *testing.T) {
	app, base := phase7App(t)
	f := &applianceMinecraftResetAPI{applianceFactoryResetAPI: &applianceFactoryResetAPI{phase7API: base}, selectedOperation: "jv-family1"}
	app.api = f
	for _, action := range []string{"plan", "apply"} {
		rr := phase7Request(app, http.MethodPost, "/api/system/workspace/reset/minecraft/"+action+"?server=jv-sibling", "csrf=csrf-token&plan_fingerprint=reviewed-old")
		if rr.Code != 200 && rr.Code != 202 {
			t.Fatal(rr.Code, rr.Body.String())
		}
		if f.calls[len(f.calls)-1] != "historical-"+action {
			t.Fatal(f.calls)
		}
	}
}
