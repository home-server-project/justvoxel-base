package main

import (
    "bufio"
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "io"
    "log"
    "os/exec"
    "time"
)

const adminMigrationImportTransactionHelper="/usr/libexec/justvoxel/mjust/admin-migration-import-transaction-json"
var migrationImportWorkerTimeout=12*time.Hour

type migrationImportTransactionRequest struct {
    OperationID string `json:"operation_id"`
    PlanFingerprint string `json:"plan_fingerprint"`
    Request adminMigrationImportRequest `json:"request"`
    Normalized adminMigrationImportNormalized `json:"normalized"`
    Context adminMigrationImportContext `json:"context"`
    Requirements adminMigrationImportRequirements `json:"requirements"`
    PlayersConfirmed bool `json:"players_confirmed"`
    EULAAccepted bool `json:"eula_accepted"`
    VanillaConfirmed bool `json:"vanilla_confirmed"`
    PluginsConfirmed bool `json:"plugins_confirmed"`
    OnlineModeConfirmed bool `json:"online_mode_confirmed"`
    BackupSMBPassword string `json:"backup_smb_password,omitempty"`
}
type migrationImportTransactionEvent struct{Event string `json:"event"`;State string `json:"state,omitempty"`;Stage string `json:"stage,omitempty"`;Status string `json:"status"`;Outcome string `json:"outcome,omitempty"`}

var runAdminMigrationImportTransactionHelper=func(ctx context.Context,request migrationImportTransactionRequest,handle func(migrationImportTransactionEvent)error)error{
    payload,err:=json.Marshal(request);if err!=nil{return err}
    cmd:=exec.CommandContext(ctx,adminMigrationImportTransactionHelper);cmd.Stdin=bytes.NewReader(payload)
    stdout,err:=cmd.StdoutPipe();if err!=nil{return err};cmd.Stderr=io.Discard
    if err:=cmd.Start();err!=nil{return err}
    scanner:=bufio.NewScanner(stdout);scanner.Buffer(make([]byte,1024),64*1024)
    for scanner.Scan(){
        var event migrationImportTransactionEvent
        d:=json.NewDecoder(bytes.NewReader(scanner.Bytes()));d.DisallowUnknownFields()
        if err:=d.Decode(&event);err!=nil{_ = cmd.Process.Kill();_ = cmd.Wait();return errors.New("server migration Import helper returned invalid progress data")}
        var trailing any;if err:=d.Decode(&trailing);err!=io.EOF{_ = cmd.Process.Kill();_ = cmd.Wait();return errors.New("server migration Import helper returned invalid progress data")}
        if err:=handle(event);err!=nil{_ = cmd.Process.Kill();_ = cmd.Wait();return err}
    }
    if err:=scanner.Err();err!=nil{_ = cmd.Process.Kill();_ = cmd.Wait();return err};return cmd.Wait()
}
var startMigrationImportWorker=func(s *server,operationID string,plan migrationImportExecutionPlan){go func(){ctx,cancel:=context.WithTimeout(context.Background(),migrationImportWorkerTimeout);defer cancel();if err:=executeMigrationImportTransaction(ctx,s.operations,operationID,plan);err!=nil{log.Printf("Server migration Import operation %s finished with error: %v",operationID,err)}}()}
func executeMigrationImportTransaction(parent context.Context,store *operationStore,operationID string,plan migrationImportExecutionPlan)error{
    if store==nil{return errors.New("operation store is unavailable")}
    operation,err:=store.get(operationID);if err!=nil{return err}
    if operation.OperationType!=operationTypeMigrationImport||operation.PlanFingerprint==""{return errors.New("invalid server migration Import operation identity")}
    if !validAdminMigrationImportRequest(plan.Request)||plan.Context.SourceIdentity==""||plan.Context.DestinationIdentity==""{return markMigrationImportNeedsAttention(store,operationID,"invalid_execution_plan","Server migration Import execution data is incomplete; administrator attention is required.")}
    if _,err:=store.transition(operationID,operationValidating,"import_preflight","Preparing the reviewed server migration Import for one bounded staging attempt before changing Minecraft.");err!=nil{return err}
    request:=migrationImportTransactionRequest{OperationID:operationID,PlanFingerprint:operation.PlanFingerprint,Request:plan.Request,Normalized:plan.Normalized,Context:plan.Context,Requirements:plan.Requirements,PlayersConfirmed:plan.PlayersConfirmed,EULAAccepted:plan.EULAAccepted,VanillaConfirmed:plan.VanillaConfirmed,PluginsConfirmed:plan.PluginsConfirmed,OnlineModeConfirmed:plan.OnlineModeConfirmed,BackupSMBPassword:plan.BackupSMBPassword}
    finalSeen:=false
    err=runAdminMigrationImportTransactionHelper(parent,request,func(event migrationImportTransactionEvent)error{
        switch event.Event{
        case "progress":return applyMigrationImportProgress(store,operationID,event)
        case "result":if finalSeen{return errors.New("server migration Import helper returned multiple final results")};finalSeen=true;return applyMigrationImportResult(store,operationID,event)
        default:return errors.New("server migration Import helper returned an unsupported event")
        }
    })
    if err!=nil{_ = markMigrationImportNeedsAttention(store,operationID,"import_backend_interrupted","Server migration Import backend stopped unexpectedly; preserved Import or runtime state requires administrator attention.");return err}
    if !finalSeen{err:=errors.New("server migration Import helper ended without a final result");_ = markMigrationImportNeedsAttention(store,operationID,"import_backend_incomplete","Server migration Import backend ended without a final safety result; administrator attention is required.");return err}
    return nil
}
func applyMigrationImportProgress(store *operationStore,operationID string,event migrationImportTransactionEvent)error{
    if event.Stage==""||event.Status==""{return errors.New("server migration Import progress event is incomplete")}
    var desired operationState
    switch event.State{case string(operationValidating):desired=operationValidating;case string(operationRunning):desired=operationRunning;case string(operationVerifying):desired=operationVerifying;default:return errors.New("server migration Import progress event has an invalid state")}
    current,err:=store.get(operationID);if err!=nil{return err};if current.State==desired{_,err=store.updateProgress(operationID,desired,event.Stage,event.Status);return err};_,err=store.transition(operationID,desired,event.Stage,event.Status);return err
}
func applyMigrationImportResult(store *operationStore,operationID string,event migrationImportTransactionEvent)error{
    if event.Status==""{return errors.New("server migration Import result event is incomplete")}
    current,err:=store.get(operationID);if err!=nil{return err}
    switch event.Outcome{
    case "succeeded":
        if current.State!=operationVerifying{return errors.New("server migration Import success arrived before verification")}
        _,err=store.transition(operationID,operationSucceeded,"completed",event.Status);return err
    case "rolled_back":
        if current.State!=operationFailed&&current.State!=operationRollingBack{if _,err:=store.transition(operationID,operationFailed,"import_failed","Server migration Import did not complete; safe rollback state is being recorded.");err!=nil{return err}}
        current,err=store.get(operationID);if err!=nil{return err};if current.State==operationFailed{if _,err:=store.transition(operationID,operationRollingBack,"rollback","Finalizing server migration Import rollback state.");err!=nil{return err}}
        _,err=store.transition(operationID,operationRolledBack,"import_rolled_back",event.Status);return err
    case "needs_attention":return markMigrationImportNeedsAttention(store,operationID,"import_needs_attention",event.Status)
    default:return errors.New("server migration Import result event has an invalid outcome")
    }
}
func markMigrationImportNeedsAttention(store *operationStore,operationID,stage,status string)error{
    current,err:=store.get(operationID);if err!=nil{return err};if current.State==operationNeedsAttention{_,err=store.updateProgress(operationID,operationNeedsAttention,stage,status);return err};if current.State==operationSucceeded||current.State==operationRolledBack{return errors.New("completed server migration Import cannot require attention")};_,err=store.transition(operationID,operationNeedsAttention,stage,status);return err
}
