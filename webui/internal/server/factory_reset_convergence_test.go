package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type applianceFactoryResetAPI struct {
	*phase7API
	calls []string
}

func (f *applianceFactoryResetAPI) AdminMinecraftResetPlan(context.Context, string) (api.AdminResetPlanResponse, error) {
	return api.AdminResetPlanResponse{}, errors.New("selected Reset must not be used")
}
func (f *applianceFactoryResetAPI) AdminMinecraftResetApply(context.Context, string, api.AdminMinecraftResetApplyRequest) (api.AdminResetApplyResponse, error) {
	return api.AdminResetApplyResponse{}, errors.New("selected Reset must not be used")
}
func (f *applianceFactoryResetAPI) AdminCurrentMinecraftResetOperation(context.Context, string) (api.PersistentOperationResponse, error) {
	return api.PersistentOperationResponse{}, errors.New("selected Reset must not be used")
}
func (f *applianceFactoryResetAPI) AdminFactoryResetPlan(context.Context, string) (api.AdminResetPlanResponse, error) {
	f.calls = append(f.calls, "plan")
	return api.AdminResetPlanResponse{OK: true, Mode: "factory", PlanFingerprint: "reviewed-appliance"}, nil
}
func (f *applianceFactoryResetAPI) AdminFactoryResetApply(_ context.Context, _ string, r api.AdminFactoryResetApplyRequest) (api.AdminResetApplyResponse, error) {
	f.calls = append(f.calls, "apply:"+r.PlanFingerprint)
	return api.AdminResetApplyResponse{OK: true, Operation: &api.PersistentOperation{OperationType: "factory_reset", State: "queued"}}, nil
}
func (f *applianceFactoryResetAPI) AdminFactoryResetResolve(_ context.Context, _ string, id string) (api.PersistentOperationResponse, error) {
	f.calls = append(f.calls, "resolve:"+id)
	return api.PersistentOperationResponse{Operation: &api.PersistentOperation{OperationType: "factory_reset", State: "resolved"}}, nil
}
func (f *applianceFactoryResetAPI) AdminCurrentFactoryResetOperation(context.Context, string) (api.PersistentOperationResponse, error) {
	f.calls = append(f.calls, "current")
	return api.PersistentOperationResponse{Operation: &api.PersistentOperation{OperationType: "factory_reset", State: "needs_attention"}}, nil
}
func (f *applianceFactoryResetAPI) AdminOperation(_ context.Context, _ string, id string) (api.PersistentOperationResponse, error) {
	f.calls = append(f.calls, "operation:"+id)
	return api.PersistentOperationResponse{Operation: &api.PersistentOperation{OperationID: id, OperationType: "factory_reset", State: "needs_attention"}}, nil
}
func TestFactoryResetWorkspaceIgnoresSelectedServer(t *testing.T) {
	for _, query := range []string{"", "?server=jv-family1", "?server=jv-sibling", "?server=invalid&server=another"} {
		t.Run(query, func(t *testing.T) {
			app, base := phase7App(t)
			f := &applianceFactoryResetAPI{phase7API: base}
			app.api = f
			for _, request := range []struct {
				method, path, body, want string
				status                   int
			}{
				{http.MethodPost, "/api/system/workspace/reset/factory/plan", "csrf=csrf-token", "plan", 200},
				{http.MethodPost, "/api/system/workspace/reset/factory/apply", "csrf=csrf-token&plan_fingerprint=reviewed-appliance&confirm_players=yes", "apply:reviewed-appliance", 202},
				{http.MethodGet, "/api/system/workspace/reset/factory/current", "", "current", 200},
				{http.MethodGet, "/api/system/workspace/reset/operations/12345678-1234-4123-8123-123456789abc", "", "operation:12345678-1234-4123-8123-123456789abc", 200},
				{http.MethodPost, "/api/system/workspace/reset/factory/resolve", "csrf=csrf-token&operation_id=appliance-operation&keep_current_state=yes", "resolve:appliance-operation", 200},
			} {
				rr := phase7Request(app, request.method, request.path+query, request.body)
				if rr.Code != request.status || len(f.calls) == 0 || f.calls[len(f.calls)-1] != request.want {
					t.Fatal(request.path, rr.Code, rr.Body.String(), f.calls)
				}
				if strings.Contains(rr.Body.String(), "instance_id") {
					t.Fatal("Factory Reset was rebound to a selected server")
				}
			}
		})
	}
}
