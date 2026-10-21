package server

import (
	"context"
	"testing"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type containerWorkspaceTestAPI struct {
	*phase7API
	id     string
	writes int
	change api.MinecraftContainerUpdateRequest
}

func (f *containerWorkspaceTestAPI) MinecraftInstanceContainerStatus(_ context.Context, _, id string) (api.MinecraftContainerStatus, error) {
	f.id = id
	return api.MinecraftContainerStatus{InstanceID: id, Image: "docker.io/itzg/minecraft-server:stable", State: "update_available", PlanFingerprint: "reviewed"}, nil
}
func (f *containerWorkspaceTestAPI) MinecraftInstanceContainerUpdate(_ context.Context, _, id string, change api.MinecraftContainerUpdateRequest) (api.PersistentOperationResponse, error) {
	f.id = id
	f.writes++
	f.change = change
	return api.PersistentOperationResponse{Operation: &api.PersistentOperation{InstanceID: id, OperationType: "minecraft_container_update"}}, nil
}
func (f *containerWorkspaceTestAPI) MinecraftInstanceContainerOperation(_ context.Context, _, id, _ string) (api.PersistentOperationResponse, error) {
	f.id = id
	return api.PersistentOperationResponse{}, nil
}
func (f *containerWorkspaceTestAPI) MinecraftInstanceContainerAcknowledge(_ context.Context, _, id, _ string) (api.PersistentOperationResponse, error) {
	f.id = id
	return api.PersistentOperationResponse{}, nil
}
func TestContainerWorkspaceSelectionAndStrictCSRFDecisions(t *testing.T) {
	_, base := phase7App(t)
	client := &containerWorkspaceTestAPI{phase7API: base}
	app, err := New(client, Config{ManagementAPI: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	w := phase7Request(app, "GET", "/api/version/container/status?server=jv-family1", "")
	if w.Code != 200 || client.id != "jv-family1" {
		t.Fatalf("selected status: %d %s", w.Code, w.Body.String())
	}
	body := "csrf=csrf-token&plan_fingerprint=reviewed&restart_now=no&backup_before_restart=no&confirm_players=no"
	for _, suffix := range []string{"&image=docker.io/evil", "&command=pull", "&restart_now=yes", "&backup_before_restart=maybe"} {
		w = phase7Request(app, "POST", "/api/version/container/update?server=jv-family1", body+suffix)
		if w.Code != 400 || client.writes != 0 {
			t.Fatalf("unbounded request: %d", w.Code)
		}
	}
	w = phase7Request(app, "POST", "/api/version/container/update?server=jv-family1", "csrf=wrong&plan_fingerprint=reviewed")
	if w.Code != 403 || client.writes != 0 {
		t.Fatal("CSRF bypass")
	}
	w = phase7Request(app, "POST", "/api/version/container/update?server=jv-family1", body)
	if w.Code != 202 || client.writes != 1 || client.id != "jv-family1" || client.change.RestartNow || client.change.BackupBeforeRestart {
		t.Fatalf("selected pull-only choices: %d %+v", w.Code, client.change)
	}
}
