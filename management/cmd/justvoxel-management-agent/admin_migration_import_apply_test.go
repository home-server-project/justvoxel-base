package main

import (
    "context"
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestMigrationImportApplyRequiresExplicitImportConfirmation(t *testing.T) {
    s:=surfaceTestServer(t,roleAdministrator)
    attachTestOperationStore(t,s,openTestOperationStore(t))
    mux:=http.NewServeMux();registerAdminMigrationImportRoutes(mux,s)
    body:=`{"plan_fingerprint":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","request":{"source":{"path":"/tmp/server"},"destination":{"java_port":25565,"bedrock_port":19132,"backup_keep":7,"backup_automatic":true}},"import_confirmed":false,"players_confirmed":false,"eula_accepted":false,"vanilla_confirmed":false,"plugins_confirmed":false,"online_mode_confirmed":false}`
    rr:=httptest.NewRecorder();mux.ServeHTTP(rr,surfaceRequest(http.MethodPost,"/v1/admin/migration/import/apply",body))
    if rr.Code!=http.StatusBadRequest{t.Fatalf("status=%d body=%s",rr.Code,rr.Body.String())}
}
func TestMigrationImportWorkerRejectsWrongOperationType(t *testing.T){
    store:=openTestOperationStore(t)
    op,_,err:=store.beginMigrationExport(testMigrationExportFingerprint);if err!=nil{t.Fatal(err)}
    err=executeMigrationImportTransaction(context.Background(),store,op.OperationID,migrationImportExecutionPlan{})
    if err==nil{t.Fatal("expected wrong-operation Import worker error")}
}
