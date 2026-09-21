package main

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
)

func step5A2ImportRequest() adminMigrationImportRequest {
    return adminMigrationImportRequest{
        Source: adminMigrationImportSourceRequest{Kind:"local", Path:"/tmp/server"},
        Destination: adminMigrationImportDestinationRequest{JavaPort:25565, BedrockPort:19132, BackupKeep:7, BackupAutomatic:true},
    }
}

func step5A2ImportHelper(players bool) adminMigrationImportHelperResponse {
    online := 0
    names := []string{}
    if players {
        online = 1
        names = []string{"PlayerOne"}
    }
    return adminMigrationImportHelperResponse{
        OK:true, SchemaVersion:"v1",
        Normalized:&adminMigrationImportNormalized{
            Source:adminMigrationImportSourceNormalized{
                Path:"/tmp/server", SelectedRoot:"", SourceClass:"paper", CandidateType:"paper",
                MinecraftVersion:"1.21.8", OnlineMode:"online", GameMode:"survival", Difficulty:"normal",
                WhitelistEnabled:true, EnforceWhitelist:true, MaxPlayers:10, MOTD:"Test",
                PluginCount:0, BedrockEnabled:false, BedrockManagedPlugins:true,
                JavaPortHint:25565, BedrockPortHint:19132, ExpandedBytes:1024,
            },
            Destination:adminMigrationImportDestinationNormalized{
                Mode:"replace", DataPath:"/var/lib/justvoxel/minecraft", BackupPath:"/var/lib/justvoxel/backups",
                JavaPort:25565, BedrockPort:19132, JavaMemory:"4G", ContainerMemory:"6G", Timezone:"UTC",
                ImageTag:"stable", MinecraftUID:1000, MinecraftGID:1000, BackupKeep:7,
                BackupSchedule:"*-*-* 04:30:00", BackupAutomatic:true,
            },
        },
        Warnings:[]adminMigrationExportWarning{},
        Requirements:&adminMigrationImportRequirements{
            ImportConfirmationRequired:true, PlayersConfirmationRequired:players,
            MinecraftState:"running", Online:online, Players:names,
            SourceRevalidationOnApply:true, RuntimeValidationRequired:true, RollbackRequired:true,
        },
        Context:&adminMigrationImportContext{SourceIdentity:"source-id", ConfigIdentity:"config-id", DestinationIdentity:"destination-id"},
        Candidates:[]adminMigrationImportCandidate{},
        SourceEntries:[]adminMigrationImportSourceEntry{},
    }
}

func installStep5A2ImportPlanHelper(t *testing.T, helper adminMigrationImportHelperResponse) {
    t.Helper()
    old := runAdminMigrationImportPlanHelper
    data, err := json.Marshal(helper)
    if err != nil { t.Fatal(err) }
    runAdminMigrationImportPlanHelper = func(context.Context,string,[]byte)([]byte,error){ return data,nil }
    t.Cleanup(func(){ runAdminMigrationImportPlanHelper=old })
}

func TestMigrationImportTransportRequestValidation(t *testing.T) {
    base:=step5A2ImportRequest()
    cases:=[]struct{name string; source adminMigrationImportSourceRequest; valid bool}{
        {"local",adminMigrationImportSourceRequest{Kind:"local",Path:"/tmp/server"},true},
        {"configured-backup",adminMigrationImportSourceRequest{Kind:"backup",Path:""},true},
        {"device",adminMigrationImportSourceRequest{Kind:"device",Path:"",Device:"/dev/sdb1",Removable:true},true},
        {"nfs",adminMigrationImportSourceRequest{Kind:"nfs",Path:"",Source:"server:/export"},true},
        {"smb",adminMigrationImportSourceRequest{Kind:"smb",Path:"",Source:"//server/share",Username:"user",SMBPassword:"secret"},true},
        {"remote-absolute-path",adminMigrationImportSourceRequest{Kind:"nfs",Path:"/escape",Source:"server:/export"},false},
        {"smb-newline-secret",adminMigrationImportSourceRequest{Kind:"smb",Path:"",Source:"//server/share",Username:"user",SMBPassword:"bad\nsecret"},false},
    }
    for _,tc:=range cases{
        t.Run(tc.name,func(t *testing.T){
            request:=base
            request.Source=tc.source
            if got:=validAdminMigrationImportRequest(request);got!=tc.valid{t.Fatalf("valid=%t want %t for %#v",got,tc.valid,tc.source)}
        })
    }
}

func TestMigrationImportPlanReturnsTransportSourceEntries(t *testing.T) {
    old:=runAdminMigrationImportPlanHelper
    runAdminMigrationImportPlanHelper=func(context.Context,string,[]byte)([]byte,error){
        return []byte(`{"ok":false,"schema_version":"v1","code":"source_selection_required","error":"Choose an Import archive or server directory from this source.","warnings":[],"candidates":[],"source_entries":[{"path":"world.tar.gz","kind":"archive"},{"path":"paper-server","kind":"directory"}]}`),nil
    }
    defer func(){runAdminMigrationImportPlanHelper=old}()
    s:=surfaceTestServer(t,roleAdministrator)
    mux:=http.NewServeMux();registerAdminMigrationImportRoutes(mux,s)
    request:=step5A2ImportRequest()
    request.Source=adminMigrationImportSourceRequest{Kind:"nfs",Source:"server:/export"}
    data,_:=json.Marshal(request)
    rr:=httptest.NewRecorder();mux.ServeHTTP(rr,surfaceRequest(http.MethodPost,"/v1/admin/migration/import/plan",string(data)))
    if rr.Code!=http.StatusBadRequest||!strings.Contains(rr.Body.String(),"source_selection_required")||!strings.Contains(rr.Body.String(),"world.tar.gz"){
        t.Fatalf("status=%d body=%s",rr.Code,rr.Body.String())
    }
}

func TestMigrationImportApplyRejectsStaleReviewedPlan(t *testing.T) {
    helper:=step5A2ImportHelper(false)
    installStep5A2ImportPlanHelper(t,helper)
    s:=surfaceTestServer(t,roleAdministrator)
    attachTestOperationStore(t,s,openTestOperationStore(t))
    mux:=http.NewServeMux();registerAdminMigrationImportRoutes(mux,s)
    apply:=adminMigrationImportApplyRequest{PlanFingerprint:"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",Request:step5A2ImportRequest(),ImportConfirmed:true}
    data,_:=json.Marshal(apply)
    rr:=httptest.NewRecorder();mux.ServeHTTP(rr,surfaceRequest(http.MethodPost,"/v1/admin/migration/import/apply",string(data)))
    if rr.Code!=http.StatusConflict||!strings.Contains(rr.Body.String(),"stale_plan"){t.Fatalf("status=%d body=%s",rr.Code,rr.Body.String())}
}

func TestMigrationImportApplyRequiresOnlinePlayerConfirmation(t *testing.T) {
    helper:=step5A2ImportHelper(true)
    installStep5A2ImportPlanHelper(t,helper)
    fingerprint,err:=adminMigrationImportPlanFingerprint(helper.SchemaVersion,helper.Normalized,helper.Requirements,helper.Context)
    if err!=nil{t.Fatal(err)}
    s:=surfaceTestServer(t,roleAdministrator)
    attachTestOperationStore(t,s,openTestOperationStore(t))
    mux:=http.NewServeMux();registerAdminMigrationImportRoutes(mux,s)
    apply:=adminMigrationImportApplyRequest{PlanFingerprint:fingerprint,Request:step5A2ImportRequest(),ImportConfirmed:true,PlayersConfirmed:false}
    data,_:=json.Marshal(apply)
    rr:=httptest.NewRecorder();mux.ServeHTTP(rr,surfaceRequest(http.MethodPost,"/v1/admin/migration/import/apply",string(data)))
    if rr.Code!=http.StatusBadRequest||!strings.Contains(rr.Body.String(),"players_confirmation_required"){t.Fatalf("status=%d body=%s",rr.Code,rr.Body.String())}
}

func step5A2ExecutionPlan() migrationImportExecutionPlan {
    return migrationImportExecutionPlan{
        Request:step5A2ImportRequest(),
        Context:adminMigrationImportContext{SourceIdentity:"source-id",DestinationIdentity:"destination-id"},
    }
}

func TestMigrationImportWorkerPersistentOutcomes(t *testing.T) {
    tests:=[]struct{
        name string
        events []migrationImportTransactionEvent
        want operationState
        current bool
    }{
        {"success",[]migrationImportTransactionEvent{
            {Event:"progress",State:"running",Stage:"import_execute",Status:"Importing."},
            {Event:"progress",State:"verifying",Stage:"import_verify",Status:"Verifying."},
            {Event:"result",Outcome:"succeeded",Status:"Import completed."},
        },operationSucceeded,false},
        {"rolled-back",[]migrationImportTransactionEvent{
            {Event:"progress",State:"running",Stage:"import_execute",Status:"Importing."},
            {Event:"result",Outcome:"rolled_back",Status:"Previous server restored."},
        },operationRolledBack,false},
        {"needs-attention",[]migrationImportTransactionEvent{
            {Event:"progress",State:"running",Stage:"import_execute",Status:"Importing."},
            {Event:"result",Outcome:"needs_attention",Status:"Recovery evidence retained."},
        },operationNeedsAttention,true},
    }
    for _,tc:=range tests{
        t.Run(tc.name,func(t *testing.T){
            store:=openTestOperationStore(t)
            op,_,err:=store.beginMigrationImport(testMigrationImportFingerprint)
            if err!=nil{t.Fatal(err)}
            old:=runAdminMigrationImportTransactionHelper
            runAdminMigrationImportTransactionHelper=func(_ context.Context,_ migrationImportTransactionRequest,handle func(migrationImportTransactionEvent)error)error{
                for _,event:=range tc.events{if err:=handle(event);err!=nil{return err}}
                return nil
            }
            defer func(){runAdminMigrationImportTransactionHelper=old}()
            if err:=executeMigrationImportTransaction(context.Background(),store,op.OperationID,step5A2ExecutionPlan());err!=nil{t.Fatal(err)}
            final,err:=store.get(op.OperationID);if err!=nil{t.Fatal(err)}
            if final.State!=tc.want{t.Fatalf("state=%s want %s journal=%#v",final.State,tc.want,final)}
            current,err:=store.currentMigration();if err!=nil{t.Fatal(err)}
            if tc.current {
                if current==nil||current.OperationID!=op.OperationID{t.Fatalf("current=%#v want %s",current,op.OperationID)}
            } else if current!=nil {
                t.Fatalf("completed Import still owns migration family: %#v",current)
            }
        })
    }
}
