package main

import (
    "encoding/json"
    "errors"
    "io"
    "net/http"
    "strings"
)

const adminMigrationImportApplyRequestLimit = 20 * 1024

type adminMigrationImportApplyRequest struct {
    PlanFingerprint string `json:"plan_fingerprint"`
    Request adminMigrationImportRequest `json:"request"`
    ImportConfirmed bool `json:"import_confirmed"`
    PlayersConfirmed bool `json:"players_confirmed"`
    EULAAccepted bool `json:"eula_accepted"`
    VanillaConfirmed bool `json:"vanilla_confirmed"`
    PluginsConfirmed bool `json:"plugins_confirmed"`
    OnlineModeConfirmed bool `json:"online_mode_confirmed"`
    BackupSMBPassword string `json:"backup_smb_password,omitempty"`
}
type adminMigrationImportApplyResponse struct {
    OK bool `json:"ok"`
    Code string `json:"code,omitempty"`
    Error string `json:"error,omitempty"`
    Created bool `json:"created"`
    Operation *operationJournal `json:"operation,omitempty"`
}
type migrationImportExecutionPlan struct {
    Request adminMigrationImportRequest
    Normalized adminMigrationImportNormalized
    Context adminMigrationImportContext
    Requirements adminMigrationImportRequirements
    PlayersConfirmed bool
    EULAAccepted bool
    VanillaConfirmed bool
    PluginsConfirmed bool
    OnlineModeConfirmed bool
    BackupSMBPassword string
}

func (s *server) adminMigrationImportApply(w http.ResponseWriter, r *http.Request) {
    actor, ok := s.requireAdministrator(w, r); if !ok { return }
    if s.operations == nil { writeAdminMigrationImportApplyFailure(w,http.StatusServiceUnavailable,"operation_status_unavailable","Server migration operation tracking is unavailable"); return }
    var request adminMigrationImportApplyRequest
    if !decodeAdminMigrationImportApplyRequest(w,r,&request) { return }
    if !operationFingerprintPattern.MatchString(request.PlanFingerprint) { writeAdminMigrationImportApplyFailure(w,http.StatusBadRequest,"invalid_plan_fingerprint","invalid reviewed server migration Import fingerprint"); return }
    if !request.ImportConfirmed { writeAdminMigrationImportApplyFailure(w,http.StatusBadRequest,"import_confirmation_required","explicit server migration Import confirmation is required"); return }

    current,err:=s.operations.currentMigration()
    if err!=nil { writeAdminMigrationImportApplyFailure(w,http.StatusInternalServerError,"operation_status_failed","current server migration operation could not be read"); return }
    if current!=nil {
        if current.OperationType==operationTypeMigrationImport && current.PlanFingerprint==request.PlanFingerprint { writeJSON(w,http.StatusOK,adminMigrationImportApplyResponse{OK:true,Created:false,Operation:current}); return }
        writeAdminMigrationImportApplyFailure(w,http.StatusConflict,"migration_busy","another server migration operation is already active"); return
    }

    plan,planningErr:=authoritativeAdminMigrationImportPlan(r.Context(),request.Request)
    if planningErr!=nil { writeAdminMigrationImportApplyFailure(w,planningErr.status,"preflight_unavailable",planningErr.message); return }
    if !plan.OK { code:=plan.Code; if code==""{code="invalid_plan"}; writeAdminMigrationImportApplyFailure(w,http.StatusBadRequest,code,plan.Error); return }
    if plan.PlanFingerprint!=request.PlanFingerprint { writeAdminMigrationImportApplyFailure(w,http.StatusConflict,"stale_plan","the reviewed server migration Import changed; review the source and destination again"); return }
    if plan.Context==nil||plan.Normalized==nil||plan.Requirements==nil { writeAdminMigrationImportApplyFailure(w,http.StatusInternalServerError,"invalid_plan","server migration Import planning returned incomplete execution data"); return }
    req:=plan.Requirements
    if req.PlayersConfirmationRequired&&!request.PlayersConfirmed { writeAdminMigrationImportApplyFailure(w,http.StatusBadRequest,"players_confirmation_required","players are online; explicit interruption confirmation is required"); return }
    if req.EULAAcceptanceRequired&&!request.EULAAccepted { writeAdminMigrationImportApplyFailure(w,http.StatusBadRequest,"eula_acceptance_required","Minecraft EULA acceptance is required for a fresh Import"); return }
    if req.VanillaConfirmationRequired&&!request.VanillaConfirmed { writeAdminMigrationImportApplyFailure(w,http.StatusBadRequest,"vanilla_confirmation_required","vanilla to Paper conversion requires explicit confirmation"); return }
    if req.PluginsConfirmationRequired&&!request.PluginsConfirmed { writeAdminMigrationImportApplyFailure(w,http.StatusBadRequest,"plugins_confirmation_required","external plugin execution requires explicit confirmation"); return }
    if req.OnlineModeConfirmationRequired&&!request.OnlineModeConfirmed { writeAdminMigrationImportApplyFailure(w,http.StatusBadRequest,"online_mode_confirmation_required","source online-mode=true requires explicit confirmation"); return }
    if req.BackupSMBPasswordRequired&&request.BackupSMBPassword=="" { writeAdminMigrationImportApplyFailure(w,http.StatusBadRequest,"backup_smb_password_required","SMB backup password is required for this fresh Import destination"); return }
    if !req.BackupSMBPasswordRequired&&request.BackupSMBPassword!="" { writeAdminMigrationImportApplyFailure(w,http.StatusBadRequest,"unexpected_backup_smb_password","SMB backup password is accepted only for a reviewed SMB backup destination"); return }

    operation,created,err:=s.operations.beginMigrationImport(request.PlanFingerprint)
    if err!=nil {
        if errors.Is(err,errFactoryResetOperationBusy) {
            writeAdminMigrationImportApplyFailure(w,http.StatusConflict,"factory_reset_needs_attention","Resolve the failed Full Factory Reset by keeping the current server, or retry the reset, before Server Import can start"); return
        }
        if errors.Is(err,errMinecraftResetOperationBusy) {
            writeAdminMigrationImportApplyFailure(w,http.StatusConflict,"minecraft_reset_busy","Reset Minecraft must finish before Server Import can start"); return
        }
        if errors.Is(err,errMigrationOperationBusy)||errors.Is(err,errMigrationLockBusy) {
            current,currentErr:=s.operations.currentMigration()
            if currentErr==nil&&current!=nil&&current.OperationType==operationTypeMigrationImport&&current.PlanFingerprint==request.PlanFingerprint { writeJSON(w,http.StatusOK,adminMigrationImportApplyResponse{OK:true,Created:false,Operation:current}); return }
            writeAdminMigrationImportApplyFailure(w,http.StatusConflict,"migration_busy","another server migration operation is already active"); return
        }
        writeAdminMigrationImportApplyFailure(w,http.StatusInternalServerError,"operation_create_failed","server migration Import operation could not be created"); return
    }
    if s.store!=nil { _=s.store.recordAuditEvent(actor,"migration_import",plan.Normalized.Source.Path,true,"Server migration Import operation accepted") }
    status:=http.StatusAccepted; if !created { status=http.StatusOK }
    writeJSON(w,status,adminMigrationImportApplyResponse{OK:true,Created:created,Operation:&operation})
    if created {
        startMigrationImportWorker(s,operation.OperationID,migrationImportExecutionPlan{
            Request:request.Request,Normalized:*plan.Normalized,Context:*plan.Context,Requirements:*plan.Requirements,
            PlayersConfirmed:request.PlayersConfirmed,EULAAccepted:request.EULAAccepted,VanillaConfirmed:request.VanillaConfirmed,
            PluginsConfirmed:request.PluginsConfirmed,OnlineModeConfirmed:request.OnlineModeConfirmed,BackupSMBPassword:request.BackupSMBPassword,
        })
    }
}
func decodeAdminMigrationImportApplyRequest(w http.ResponseWriter,r *http.Request,target *adminMigrationImportApplyRequest) bool {
    d:=json.NewDecoder(http.MaxBytesReader(w,r.Body,adminMigrationImportApplyRequestLimit)); d.DisallowUnknownFields()
    if err:=d.Decode(target);err!=nil{writeAdminMigrationImportApplyFailure(w,http.StatusBadRequest,"invalid_request","invalid JSON request");return false}
    var trailing any; if err:=d.Decode(&trailing);err!=io.EOF{writeAdminMigrationImportApplyFailure(w,http.StatusBadRequest,"invalid_request","invalid JSON request");return false}
    if !validAdminMigrationImportRequest(target.Request){writeAdminMigrationImportApplyFailure(w,http.StatusBadRequest,"invalid_request","invalid server migration Import request");return false}
    if len(target.BackupSMBPassword)>4096||strings.ContainsAny(target.BackupSMBPassword,"\r\n"){writeAdminMigrationImportApplyFailure(w,http.StatusBadRequest,"invalid_request","invalid SMB backup credential");return false}
    return true
}
func writeAdminMigrationImportApplyFailure(w http.ResponseWriter,status int,code,message string){writeJSON(w,status,adminMigrationImportApplyResponse{OK:false,Code:code,Error:message,Created:false})}
