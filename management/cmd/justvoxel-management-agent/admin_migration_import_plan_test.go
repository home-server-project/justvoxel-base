package main

import (
    "context"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
)

func TestMigrationImportPlanRequiresAdministrator(t *testing.T) {
    old := runAdminMigrationImportPlanHelper
    runAdminMigrationImportPlanHelper = func(context.Context,string,[]byte)([]byte,error){ return []byte(`{"ok":true,"schema_version":"v1","configured":true,"source_kinds":["local"],"defaults":{"data_path":"/var/lib/justvoxel/minecraft","backup_path":"/var/lib/justvoxel/backups","java_memory":"4G","container_memory":"6G","timezone":"UTC","java_port":25565,"bedrock_port":19132,"backup_keep":7,"backup_daily_time":"04:30","backup_automatic":true}}`),nil }
    defer func(){ runAdminMigrationImportPlanHelper=old }()
    s:=surfaceTestServer(t,roleViewer)
    mux:=http.NewServeMux(); registerAdminMigrationImportRoutes(mux,s)
    rr:=httptest.NewRecorder(); mux.ServeHTTP(rr,surfaceRequest(http.MethodGet,"/v1/admin/migration/import",""))
    if rr.Code!=http.StatusForbidden { t.Fatalf("status=%d want 403",rr.Code) }
}
func TestMigrationImportRequestValidation(t *testing.T) {
    s:=surfaceTestServer(t,roleAdministrator)
    mux:=http.NewServeMux(); registerAdminMigrationImportRoutes(mux,s)
    rr:=httptest.NewRecorder()
    mux.ServeHTTP(rr,surfaceRequest(http.MethodPost,"/v1/admin/migration/import/plan",`{"source":{"path":"relative"},"destination":{"java_port":25565,"bedrock_port":19132,"backup_keep":7,"backup_automatic":true}}`))
    if rr.Code!=http.StatusBadRequest || !strings.Contains(rr.Body.String(),"invalid server migration Import request") { t.Fatalf("status=%d body=%s",rr.Code,rr.Body.String()) }
}

func TestMigrationImportCandidateAcceptsDetectorFields(t *testing.T) {
    payload := []byte(`{"ok":false,"schema_version":"v1","code":"multiple_roots","error":"Multiple Minecraft server roots were found; choose exactly one.","warnings":[],"candidates":[{"root":"/var/tmp/import/paper","root_relative":"paper","sourceType":"itzg-paper","supported":true,"levelName":"world","minecraftVersion":"1.21.8","onlineMode":true,"gameMode":"survival","difficulty":"hard","whitelistEnabled":true,"enforceWhitelist":true,"maxPlayers":"20","motd":"Paper server","javaPortHint":"25565","pluginJarCount":2,"pluginJars":["Geyser-Spigot.jar","floodgate-spigot.jar"],"geyserEnabled":true,"geyserAuthType":"floodgate","bedrockPortHint":19132,"floodgateEnabled":true,"floodgateKeySha256":"0123456789abcdef"}],"source_entries":[]}`)
    var out adminMigrationImportHelperResponse
    if err := decodeAdminMigrationImportJSON(payload, &out); err != nil {
        t.Fatalf("rich migration detector candidate rejected by strict Agent decoder: %v", err)
    }
    if len(out.Candidates) != 1 {
        t.Fatalf("candidates=%d want 1", len(out.Candidates))
    }
    candidate := out.Candidates[0]
    if candidate.SourceType != "itzg-paper" || candidate.MinecraftVersion != "1.21.8" || candidate.PluginJarCount != 2 {
        t.Fatalf("unexpected candidate: %+v", candidate)
    }
    if candidate.OnlineMode == nil || !*candidate.OnlineMode || !candidate.GeyserEnabled || !candidate.FloodgateEnabled || candidate.BedrockPortHint != 19132 {
        t.Fatalf("rich candidate fields were not preserved: %+v", candidate)
    }
}
