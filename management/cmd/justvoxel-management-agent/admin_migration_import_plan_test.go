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
