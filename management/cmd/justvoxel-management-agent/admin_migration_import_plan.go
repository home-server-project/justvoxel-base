package main

import (
    "bytes"
    "context"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "errors"
    "io"
    "net/http"
    "os/exec"
    "strings"
    "time"
)

const adminMigrationImportPlanHelper = "/usr/libexec/justvoxel/mjust/admin-migration-import-plan-json"
var adminMigrationImportPlanTimeout = 10 * time.Minute

var runAdminMigrationImportPlanHelper = func(ctx context.Context, action string, request []byte) ([]byte, error) {
    cmd := exec.CommandContext(ctx, adminMigrationImportPlanHelper, action)
    if len(request) > 0 { cmd.Stdin = bytes.NewReader(request) }
    return cmd.Output()
}

type adminMigrationImportSourceRequest struct {
    Kind string `json:"kind,omitempty"`
    Path string `json:"path"`
    Device string `json:"device,omitempty"`
    Removable bool `json:"removable,omitempty"`
    Source string `json:"source,omitempty"`
    Username string `json:"username,omitempty"`
    Domain string `json:"domain,omitempty"`
    SMBPassword string `json:"smb_password,omitempty"`
    SelectedRoot string `json:"selected_root,omitempty"`
    SourceVersion string `json:"source_version,omitempty"`
}
type adminMigrationImportDestinationRequest struct {
    JavaPort int `json:"java_port"`
    BedrockPort int `json:"bedrock_port"`
    JavaMemory string `json:"java_memory,omitempty"`
    ContainerMemory string `json:"container_memory,omitempty"`
    Timezone string `json:"timezone,omitempty"`
    BackupKeep int `json:"backup_keep"`
    BackupDailyTime string `json:"backup_daily_time,omitempty"`
    BackupAutomatic bool `json:"backup_automatic"`
    Storage adminSetupPlanStorageRequest `json:"storage"`
    Backups adminSetupPlanBackupsRequest `json:"backups"`
}
type adminMigrationImportRequest struct {
    Source adminMigrationImportSourceRequest `json:"source"`
    Destination adminMigrationImportDestinationRequest `json:"destination"`
}
type adminMigrationImportDefaults struct {
    DataPath string `json:"data_path"`
    BackupPath string `json:"backup_path"`
    JavaMemory string `json:"java_memory"`
    ContainerMemory string `json:"container_memory"`
    Timezone string `json:"timezone"`
    JavaPort int `json:"java_port"`
    BedrockPort int `json:"bedrock_port"`
    BackupKeep int `json:"backup_keep"`
    BackupDailyTime string `json:"backup_daily_time"`
    BackupAutomatic bool `json:"backup_automatic"`
}
type adminMigrationImportDiscoveryResponse struct {
    OK bool `json:"ok"`
    SchemaVersion string `json:"schema_version"`
    Configured bool `json:"configured"`
    SourceKinds []string `json:"source_kinds"`
    Defaults adminMigrationImportDefaults `json:"defaults"`
}
type adminMigrationImportSourceEntry struct {
    Path string `json:"path"`
    Kind string `json:"kind"`
}
type adminMigrationImportCandidate struct {
    Root string `json:"root"`
    RootRelative string `json:"root_relative"`
    SourceType string `json:"sourceType"`
    Supported bool `json:"supported"`
    LevelName string `json:"levelName,omitempty"`
    MinecraftVersion string `json:"minecraftVersion,omitempty"`
    OnlineMode *bool `json:"onlineMode,omitempty"`
    GameMode string `json:"gameMode,omitempty"`
    Difficulty string `json:"difficulty,omitempty"`
    WhitelistEnabled *bool `json:"whitelistEnabled,omitempty"`
    EnforceWhitelist *bool `json:"enforceWhitelist,omitempty"`
    MaxPlayers string `json:"maxPlayers,omitempty"`
    MOTD string `json:"motd,omitempty"`
    JavaPortHint string `json:"javaPortHint,omitempty"`
    PluginJarCount int `json:"pluginJarCount,omitempty"`
    PluginJars []string `json:"pluginJars,omitempty"`
    GeyserEnabled bool `json:"geyserEnabled,omitempty"`
    GeyserAuthType string `json:"geyserAuthType,omitempty"`
    BedrockPortHint int `json:"bedrockPortHint,omitempty"`
    FloodgateEnabled bool `json:"floodgateEnabled,omitempty"`
    FloodgateKeySHA256 string `json:"floodgateKeySha256,omitempty"`
}
type adminMigrationImportSourceNormalized struct {
    Path string `json:"path"`
    SelectedRoot string `json:"selected_root"`
    SourceClass string `json:"source_class"`
    CandidateType string `json:"candidate_type"`
    MinecraftVersion string `json:"minecraft_version"`
    OnlineMode string `json:"online_mode"`
    GameMode string `json:"game_mode"`
    Difficulty string `json:"difficulty"`
    WhitelistEnabled bool `json:"whitelist_enabled"`
    EnforceWhitelist bool `json:"enforce_whitelist"`
    MaxPlayers int `json:"max_players"`
    MOTD string `json:"motd"`
    PluginCount int `json:"plugin_count"`
    BedrockEnabled bool `json:"bedrock_enabled"`
    BedrockManagedPlugins bool `json:"bedrock_managed_plugins"`
    JavaPortHint int `json:"java_port_hint"`
    BedrockPortHint int `json:"bedrock_port_hint"`
    ExpandedBytes uint64 `json:"expanded_bytes"`
}
type adminMigrationImportDestinationNormalized struct {
    Mode string `json:"mode"`
    DataPath string `json:"data_path"`
    BackupPath string `json:"backup_path"`
    JavaPort int `json:"java_port"`
    BedrockPort int `json:"bedrock_port"`
    JavaMemory string `json:"java_memory"`
    ContainerMemory string `json:"container_memory"`
    Timezone string `json:"timezone"`
    ImageTag string `json:"image_tag"`
    MinecraftUID uint32 `json:"minecraft_uid"`
    MinecraftGID uint32 `json:"minecraft_gid"`
    BackupKeep int `json:"backup_keep"`
    BackupSchedule string `json:"backup_schedule"`
    BackupAutomatic bool `json:"backup_automatic"`
    Storage *adminSetupPlanStorage `json:"storage,omitempty"`
    Backups *adminSetupPlanBackups `json:"backups,omitempty"`
}
type adminMigrationImportNormalized struct {
    Source adminMigrationImportSourceNormalized `json:"source"`
    Destination adminMigrationImportDestinationNormalized `json:"destination"`
}
type adminMigrationImportRequirements struct {
    ImportConfirmationRequired bool `json:"import_confirmation_required"`
    PlayersConfirmationRequired bool `json:"players_confirmation_required"`
    MinecraftState string `json:"minecraft_state"`
    Online int `json:"online"`
    Players []string `json:"players"`
    EULAAcceptanceRequired bool `json:"eula_acceptance_required"`
    VanillaConfirmationRequired bool `json:"vanilla_confirmation_required"`
    PluginsConfirmationRequired bool `json:"plugins_confirmation_required"`
    OnlineModeConfirmationRequired bool `json:"online_mode_confirmation_required"`
    SourceRevalidationOnApply bool `json:"source_revalidation_on_apply"`
    RuntimeValidationRequired bool `json:"runtime_validation_required"`
    RollbackRequired bool `json:"rollback_required"`
    BackupSMBPasswordRequired bool `json:"backup_smb_password_required"`
}
type adminMigrationImportContext struct {
    SourceIdentity string `json:"source_identity"`
    ConfigIdentity string `json:"config_identity"`
    DestinationIdentity string `json:"destination_identity"`
}
type adminMigrationImportHelperResponse struct {
    OK bool `json:"ok"`
    SchemaVersion string `json:"schema_version"`
    Code string `json:"code,omitempty"`
    Error string `json:"error,omitempty"`
    Normalized *adminMigrationImportNormalized `json:"normalized,omitempty"`
    Warnings []adminMigrationExportWarning `json:"warnings"`
    Requirements *adminMigrationImportRequirements `json:"requirements,omitempty"`
    Context *adminMigrationImportContext `json:"context,omitempty"`
    Candidates []adminMigrationImportCandidate `json:"candidates"`
    SourceEntries []adminMigrationImportSourceEntry `json:"source_entries"`
}
type adminMigrationImportPlanResponse struct {
    OK bool `json:"ok"`
    SchemaVersion string `json:"schema_version"`
    PlanFingerprint string `json:"plan_fingerprint,omitempty"`
    Code string `json:"code,omitempty"`
    Error string `json:"error,omitempty"`
    Normalized *adminMigrationImportNormalized `json:"normalized,omitempty"`
    Warnings []adminMigrationExportWarning `json:"warnings"`
    Requirements *adminMigrationImportRequirements `json:"requirements,omitempty"`
    Candidates []adminMigrationImportCandidate `json:"candidates"`
    SourceEntries []adminMigrationImportSourceEntry `json:"source_entries"`
    Context *adminMigrationImportContext `json:"-"`
}
type adminMigrationImportPlanningError struct { status int; message string }

func registerAdminMigrationImportRoutes(mux *http.ServeMux, s *server) {
    mux.HandleFunc("GET /v1/admin/migration/import", s.adminMigrationImportDiscover)
    mux.HandleFunc("POST /v1/admin/migration/import/plan", s.adminMigrationImportPlan)
    mux.HandleFunc("POST /v1/admin/migration/import/apply", s.adminMigrationImportApply)
    mux.HandleFunc("POST /v1/admin/migration/import/resolve", s.adminMigrationImportResolve)
}
func (s *server) adminMigrationImportDiscover(w http.ResponseWriter, r *http.Request) {
    if _, ok := s.requireAdministrator(w, r); !ok { return }
    ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second); defer cancel()
    output, err := runAdminMigrationImportPlanHelper(ctx, "discover", nil)
    if err != nil { writeError(w, http.StatusServiceUnavailable, "server migration Import discovery is unavailable"); return }
    var out adminMigrationImportDiscoveryResponse
    if err := decodeAdminMigrationImportJSON(output, &out); err != nil || !out.OK || out.SchemaVersion != "v1" || out.SourceKinds == nil { writeError(w, http.StatusInternalServerError, "server migration Import discovery returned invalid data"); return }
    writeJSON(w, http.StatusOK, out)
}
func (s *server) adminMigrationImportPlan(w http.ResponseWriter, r *http.Request) {
    if _, ok := s.requireAdministrator(w, r); !ok { return }
    var request adminMigrationImportRequest
    if !decodeAdminMigrationImportRequest(w, r, &request) { return }
    out, planningErr := authoritativeAdminMigrationImportPlan(r.Context(), request)
    if planningErr != nil { writeError(w, planningErr.status, planningErr.message); return }
    status := http.StatusOK; if !out.OK { status = http.StatusBadRequest }
    writeJSON(w, status, out)
}
func authoritativeAdminMigrationImportPlan(parent context.Context, request adminMigrationImportRequest) (adminMigrationImportPlanResponse, *adminMigrationImportPlanningError) {
    if !validAdminMigrationImportRequest(request) { return adminMigrationImportPlanResponse{}, &adminMigrationImportPlanningError{http.StatusBadRequest,"invalid server migration Import request"} }
    payload, err := json.Marshal(request); if err != nil { return adminMigrationImportPlanResponse{}, &adminMigrationImportPlanningError{http.StatusInternalServerError,"server migration Import request could not be encoded"} }
    ctx, cancel := context.WithTimeout(parent, adminMigrationImportPlanTimeout); defer cancel()
    output, err := runAdminMigrationImportPlanHelper(ctx, "plan", payload)
    if err != nil { return adminMigrationImportPlanResponse{}, &adminMigrationImportPlanningError{http.StatusServiceUnavailable,"server migration Import planning is unavailable"} }
    var helper adminMigrationImportHelperResponse
    if err := decodeAdminMigrationImportJSON(output, &helper); err != nil || helper.SchemaVersion != "v1" { return adminMigrationImportPlanResponse{}, &adminMigrationImportPlanningError{http.StatusInternalServerError,"server migration Import planner returned invalid data"} }
    if helper.Warnings == nil { helper.Warnings=[]adminMigrationExportWarning{} }; if helper.Candidates == nil { helper.Candidates=[]adminMigrationImportCandidate{} }; if helper.SourceEntries == nil { helper.SourceEntries=[]adminMigrationImportSourceEntry{} }
    public := adminMigrationImportPlanResponse{OK:helper.OK,SchemaVersion:helper.SchemaVersion,Code:helper.Code,Error:boundedMigrationImportError(helper.Error),Normalized:helper.Normalized,Warnings:helper.Warnings,Requirements:helper.Requirements,Candidates:helper.Candidates,SourceEntries:helper.SourceEntries}
    if !helper.OK { if public.Error=="" { return adminMigrationImportPlanResponse{}, &adminMigrationImportPlanningError{http.StatusInternalServerError,"server migration Import rejection contract changed"} }; return public,nil }
    if err := validateSuccessfulAdminMigrationImportPlan(request,&helper); err != nil { return adminMigrationImportPlanResponse{}, &adminMigrationImportPlanningError{http.StatusInternalServerError,"server migration Import planning contract changed"} }
    fingerprint, err := adminMigrationImportPlanFingerprint(helper.SchemaVersion,helper.Normalized,helper.Requirements,helper.Context); if err != nil { return adminMigrationImportPlanResponse{}, &adminMigrationImportPlanningError{http.StatusInternalServerError,"server migration Import plan identity could not be created"} }
    public.PlanFingerprint=fingerprint; public.Context=helper.Context
    return public,nil
}
func validateSuccessfulAdminMigrationImportPlan(request adminMigrationImportRequest, helper *adminMigrationImportHelperResponse) error {
    if helper==nil || helper.Normalized==nil || helper.Requirements==nil || helper.Context==nil { return errors.New("missing Import plan sections") }
    n, req, ctx := helper.Normalized, helper.Requirements, helper.Context
    if n.Source.Path != request.Source.Path || n.Source.SelectedRoot != request.Source.SelectedRoot && request.Source.SelectedRoot!="" { return errors.New("Import source request mismatch") }
    if n.Source.MinecraftVersion=="" || n.Source.OnlineMode!="online" || n.Source.SourceClass=="" || n.Source.CandidateType=="" || n.Source.MaxPlayers<=0 || n.Source.ExpandedBytes==0 { return errors.New("Import source plan incomplete") }
    if n.Destination.Mode!="replace" && n.Destination.Mode!="fresh" { return errors.New("invalid Import destination mode") }
    if !strings.HasPrefix(n.Destination.DataPath,"/") || !strings.HasPrefix(n.Destination.BackupPath,"/") || n.Destination.JavaPort<1 || n.Destination.BedrockPort<1 || n.Destination.MinecraftUID==0 || n.Destination.MinecraftGID==0 { return errors.New("Import destination plan incomplete") }
    if n.Destination.Mode=="fresh" && (n.Destination.Storage==nil || n.Destination.Backups==nil) { return errors.New("fresh Import storage plan incomplete") }
    if !req.ImportConfirmationRequired || !req.SourceRevalidationOnApply || !req.RuntimeValidationRequired || !req.RollbackRequired || req.Players==nil { return errors.New("Import safety requirements missing") }
    if req.PlayersConfirmationRequired && req.Online==0 { return errors.New("invalid Import player requirement") }
    if ctx.SourceIdentity=="" || ctx.DestinationIdentity=="" { return errors.New("Import fingerprint context incomplete") }
    return nil
}
func adminMigrationImportPlanFingerprint(schema string, normalized *adminMigrationImportNormalized, requirements *adminMigrationImportRequirements, ctx *adminMigrationImportContext) (string,error) {
    if schema=="" || normalized==nil || requirements==nil || ctx==nil { return "",errors.New("incomplete Import plan") }
    payload,err:=json.Marshal(struct{Schema string `json:"schema"`; Normalized *adminMigrationImportNormalized `json:"normalized"`; Requirements *adminMigrationImportRequirements `json:"requirements"`; Context *adminMigrationImportContext `json:"context"`}{schema,normalized,requirements,ctx})
    if err!=nil{return "",err}; sum:=sha256.Sum256(payload); return "sha256:"+hex.EncodeToString(sum[:]),nil
}
func validAdminMigrationImportRequest(request adminMigrationImportRequest) bool {
    source := request.Source
    if source.Kind=="" { source.Kind="local" }
    if strings.ContainsAny(source.Kind+source.Path+source.Device+source.Source+source.Username+source.Domain+source.SMBPassword+source.SelectedRoot+source.SourceVersion,"\r\n") || len(source.SMBPassword)>4096 { return false }
    switch source.Kind {
    case "local":
        if source.Path=="" || !strings.HasPrefix(source.Path,"/") || source.Device!="" || source.Source!="" || source.Username!="" || source.Domain!="" || source.SMBPassword!="" { return false }
    case "backup":
        if strings.HasPrefix(source.Path,"/") || source.Device!="" || source.Source!="" || source.Username!="" || source.Domain!="" || source.SMBPassword!="" { return false }
    case "device":
        if source.Device=="" || !strings.HasPrefix(source.Device,"/dev/") || strings.HasPrefix(source.Path,"/") || source.Source!="" || source.Username!="" || source.Domain!="" || source.SMBPassword!="" { return false }
    case "nfs":
        if source.Source=="" || !strings.Contains(source.Source,":") || strings.HasPrefix(source.Path,"/") || source.Device!="" || source.Username!="" || source.Domain!="" || source.SMBPassword!="" { return false }
    case "smb":
        if source.Source=="" || !strings.HasPrefix(source.Source,"//") || source.Username=="" || strings.HasPrefix(source.Path,"/") || source.Device!="" { return false }
    default:
        return false
    }
    if request.Destination.JavaPort<0 || request.Destination.JavaPort>65535 || request.Destination.BedrockPort<0 || request.Destination.BedrockPort>65535 || request.Destination.BackupKeep<1 { return false }
    if strings.ContainsAny(request.Destination.JavaMemory+request.Destination.ContainerMemory+request.Destination.Timezone+request.Destination.BackupDailyTime+request.Destination.Storage.Path+request.Destination.Storage.Device+request.Destination.Storage.MountPoint+request.Destination.Backups.Path+request.Destination.Backups.Device+request.Destination.Backups.MountPoint+request.Destination.Backups.Source+request.Destination.Backups.Username+request.Destination.Backups.Domain,"\r\n") { return false }
    return true
}
func decodeAdminMigrationImportRequest(w http.ResponseWriter,r *http.Request,target *adminMigrationImportRequest) bool {
    d:=json.NewDecoder(http.MaxBytesReader(w,r.Body,16*1024)); d.DisallowUnknownFields()
    if err:=d.Decode(target);err!=nil{writeError(w,http.StatusBadRequest,"invalid JSON request");return false}
    var trailing any; if err:=d.Decode(&trailing);err!=io.EOF{writeError(w,http.StatusBadRequest,"invalid JSON request");return false}
    if !validAdminMigrationImportRequest(*target){writeError(w,http.StatusBadRequest,"invalid server migration Import request");return false}; return true
}
func decodeAdminMigrationImportJSON(data []byte,target any) error {
    d:=json.NewDecoder(bytes.NewReader(data)); d.DisallowUnknownFields(); if err:=d.Decode(target);err!=nil{return err}; var trailing any; if err:=d.Decode(&trailing);err!=io.EOF{if err==nil{return errors.New("unexpected trailing JSON value")};return err}; return nil
}
func boundedMigrationImportError(v string) string { v=strings.TrimSpace(v); if v==""{return ""}; if len(v)>512||strings.ContainsAny(v,"\r\n"){return "server migration Import plan was rejected"}; return v }
