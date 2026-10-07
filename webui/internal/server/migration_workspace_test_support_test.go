package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

const serverMigrationOperationID = "23456789-1234-4abc-8def-123456789abc"
const serverMigrationFingerprint = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

type fakeServerMigrationAPI struct {
	fakeAPI
	role            string
	sessionErr      error
	exportDiscovery api.AdminMigrationExportDiscoveryResponse
	importDiscovery api.AdminMigrationImportDiscoveryResponse
	recovery        api.AdminMigrationRecoveryDiscoveryResponse
	current         *api.PersistentOperation
	operation       *api.PersistentOperation
	discoveryCalls  int
	mutationCalls   []string
}

func (f *fakeServerMigrationAPI) Session(_ context.Context, session string) (api.SessionInfo, error) {
	if f.sessionErr != nil {
		return api.SessionInfo{}, f.sessionErr
	}
	if session != "session-token" {
		return api.SessionInfo{}, api.ErrUnauthorized
	}
	role := f.role
	if role == "" {
		role = "administrator"
	}
	return api.SessionInfo{Username: "voxel", Role: role, AuthSource: "system"}, nil
}

func (f *fakeServerMigrationAPI) AdminMigrationExportDiscovery(_ context.Context, session string) (api.AdminMigrationExportDiscoveryResponse, error) {
	if session != "session-token" {
		return api.AdminMigrationExportDiscoveryResponse{}, api.ErrUnauthorized
	}
	f.discoveryCalls++
	return f.exportDiscovery, nil
}

func (f *fakeServerMigrationAPI) AdminMigrationImportDiscovery(_ context.Context, session string) (api.AdminMigrationImportDiscoveryResponse, error) {
	if session != "session-token" {
		return api.AdminMigrationImportDiscoveryResponse{}, api.ErrUnauthorized
	}
	f.discoveryCalls++
	return f.importDiscovery, nil
}

func (f *fakeServerMigrationAPI) AdminMigrationRecoveryDiscovery(_ context.Context, session string) (api.AdminMigrationRecoveryDiscoveryResponse, error) {
	if session != "session-token" {
		return api.AdminMigrationRecoveryDiscoveryResponse{}, api.ErrUnauthorized
	}
	f.discoveryCalls++
	return f.recovery, nil
}

func (f *fakeServerMigrationAPI) AdminCurrentMigrationOperation(_ context.Context, session string) (api.PersistentOperationResponse, error) {
	if session != "session-token" {
		return api.PersistentOperationResponse{}, api.ErrUnauthorized
	}
	if f.current == nil {
		return api.PersistentOperationResponse{}, nil
	}
	copy := *f.current
	return api.PersistentOperationResponse{Operation: &copy}, nil
}

func (f *fakeServerMigrationAPI) AdminOperation(_ context.Context, session, id string) (api.PersistentOperationResponse, error) {
	if session != "session-token" {
		return api.PersistentOperationResponse{}, api.ErrUnauthorized
	}
	if f.operation == nil || f.operation.OperationID != id {
		return api.PersistentOperationResponse{}, &api.ResponseError{StatusCode: http.StatusNotFound, Message: "operation not found"}
	}
	copy := *f.operation
	return api.PersistentOperationResponse{Operation: &copy}, nil
}

type fakeServerMigrationExportAPI struct {
	fakeServerMigrationAPI
	plan       api.AdminMigrationExportPlanResponse
	planErr    error
	planCalls  int
	planReq    api.AdminMigrationExportTargetRequest
	apply      api.AdminMigrationApplyResponse
	applyErr   error
	applyCalls int
	applyReq   api.AdminMigrationExportApplyRequest
}

func (f *fakeServerMigrationExportAPI) AdminMigrationExportPlan(_ context.Context, session string, request api.AdminMigrationExportTargetRequest) (api.AdminMigrationExportPlanResponse, error) {
	if session != "session-token" {
		return api.AdminMigrationExportPlanResponse{}, api.ErrUnauthorized
	}
	f.planCalls++
	f.mutationCalls = append(f.mutationCalls, "export plan")
	f.planReq = request
	return f.plan, f.planErr
}

func (f *fakeServerMigrationExportAPI) AdminMigrationExportApply(_ context.Context, session string, request api.AdminMigrationExportApplyRequest) (api.AdminMigrationApplyResponse, error) {
	if session != "session-token" {
		return api.AdminMigrationApplyResponse{}, api.ErrUnauthorized
	}
	f.applyCalls++
	f.mutationCalls = append(f.mutationCalls, "export apply")
	f.applyReq = request
	return f.apply, f.applyErr
}

func exportDiscoveryFixture() api.AdminMigrationExportDiscoveryResponse {
	return api.AdminMigrationExportDiscoveryResponse{
		OK: true, SchemaVersion: "v1", SuggestedFilename: "justvoxel-migration-test.tar.gz",
		ConfiguredBackupAvailable: true,
		TargetKinds:               []string{"local", "backup", "device", "nfs", "smb"},
		Devices: []api.AdminMigrationExportDevice{
			{Path: "/dev/sdb1", Filesystem: "xfs", Model: "Backup Disk", Transport: "sata", SizeBytes: 107374182400},
			{Path: "/dev/sdc1", Filesystem: "exfat", Model: "USB Stick", Transport: "usb", SizeBytes: 34359738368, Removable: true},
		},
	}
}

func exportPlanFixture(kind string, players bool) api.AdminMigrationExportPlanResponse {
	requirements := &api.AdminMigrationExportRequirements{
		ExportConfirmationRequired: true, PlayersConfirmationRequired: players,
		MinecraftState: "running", Online: 0, Players: []string{},
		SMBPasswordRequired: kind == "smb", TargetValidationOnApply: true,
		IntegrityValidationRequired: true, RuntimeValidationRequired: true,
	}
	if players {
		requirements.Online = 1
		requirements.Players = []string{"PlayerOne"}
	}
	normalized := &api.AdminMigrationExportNormalized{
		Kind: kind, Filename: "justvoxel-migration-test.tar.gz", DataPath: "/var/lib/justvoxel/minecraft",
		DataBytes: 2147483648, TargetAvailableBytes: 10737418240, TargetFilesystem: "xfs",
		TargetDisplay: "/srv/migrations/justvoxel-migration-test.tar.gz",
	}
	switch kind {
	case "local":
		normalized.Path = "/srv/migrations"
	case "backup":
		normalized.Path = "/var/mnt/backups"
	case "device":
		normalized.Device = "/dev/sdb1"
	case "nfs":
		normalized.Source = "nas:/exports/migrations"
	case "smb":
		normalized.Source = "//nas/migrations"
		normalized.Username = "voxel"
		normalized.Domain = "HOME"
		normalized.TargetDisplay = "//nas/migrations/justvoxel-migration-test.tar.gz"
	}
	return api.AdminMigrationExportPlanResponse{
		OK: true, SchemaVersion: "v1", PlanFingerprint: serverMigrationFingerprint,
		Normalized:   normalized,
		Warnings:     []api.AdminMigrationWarning{{Code: "players_online", Message: "Online players will be interrupted while the export snapshot is prepared."}},
		Requirements: requirements,
	}
}

func exportWebRequest(method, target string, values url.Values) *http.Request {
	body := strings.NewReader("")
	if values != nil {
		body = strings.NewReader(values.Encode())
	}
	req := httptest.NewRequest(method, target, body)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "null")
	}
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "csrf-token"})
	return req
}

type fakeServerMigrationImportAPI struct {
	fakeServerMigrationAPI
	storage      api.AdminStorageDiscovery
	plan         api.AdminMigrationImportPlanResponse
	planErr      error
	planCalls    int
	planReq      api.AdminMigrationImportRequest
	apply        api.AdminMigrationApplyResponse
	applyErr     error
	applyCalls   int
	applyReq     api.AdminMigrationImportApplyRequest
	storageCalls int
}

func (f *fakeServerMigrationImportAPI) AdminStorage(_ context.Context, session string) (api.AdminStorageDiscovery, error) {
	if session != "session-token" {
		return api.AdminStorageDiscovery{}, api.ErrUnauthorized
	}
	f.storageCalls++
	return f.storage, nil
}

func (f *fakeServerMigrationImportAPI) AdminMigrationImportPlan(_ context.Context, session string, request api.AdminMigrationImportRequest) (api.AdminMigrationImportPlanResponse, error) {
	if session != "session-token" {
		return api.AdminMigrationImportPlanResponse{}, api.ErrUnauthorized
	}
	f.planCalls++
	f.mutationCalls = append(f.mutationCalls, "import plan")
	f.planReq = request
	return f.plan, f.planErr
}

func (f *fakeServerMigrationImportAPI) AdminMigrationImportApply(_ context.Context, session string, request api.AdminMigrationImportApplyRequest) (api.AdminMigrationApplyResponse, error) {
	if session != "session-token" {
		return api.AdminMigrationApplyResponse{}, api.ErrUnauthorized
	}
	f.applyCalls++
	f.mutationCalls = append(f.mutationCalls, "import apply")
	f.applyReq = request
	return f.apply, f.applyErr
}

func importDiscoveryFixture(configured bool) api.AdminMigrationImportDiscoveryResponse {
	return api.AdminMigrationImportDiscoveryResponse{
		OK: true, SchemaVersion: "v1", Configured: configured,
		SourceKinds: []string{"local", "backup", "device", "nfs", "smb"},
		Defaults: api.AdminMigrationImportDefaults{
			DataPath: "/var/lib/justvoxel/minecraft", BackupPath: "/var/lib/justvoxel/backups",
			JavaMemory: "4G", ContainerMemory: "6G", Timezone: "UTC",
			JavaPort: 25565, BedrockPort: 19132, BackupKeep: 7, BackupDailyTime: "04:30", BackupAutomatic: true,
		},
	}
}

func importMediaFixture() api.AdminMigrationExportDiscoveryResponse {
	return api.AdminMigrationExportDiscoveryResponse{
		OK: true, SchemaVersion: "v1", SuggestedFilename: "justvoxel-migration-test.tar.gz",
		Devices: []api.AdminMigrationExportDevice{
			{Path: "/dev/sdb1", Filesystem: "xfs", Model: "Archive Disk", SizeBytes: 107374182400},
			{Path: "/dev/sdc1", Filesystem: "exfat", Model: "USB Import", Transport: "usb", SizeBytes: 34359738368, Removable: true},
		},
		TargetKinds: []string{"local", "backup", "device", "nfs", "smb"},
	}
}

func importStorageFixture() api.AdminStorageDiscovery {
	return api.AdminStorageDiscovery{Devices: []api.AdminStorageDevice{
		{Name: "sdb1", Path: "/dev/sdb1", Parent: "sdb", Type: "part", SizeBytes: 107374182400, Filesystem: "xfs", UUID: "data-uuid", Model: "Data Disk"},
		{Name: "sdd1", Path: "/dev/sdd1", Parent: "sdd", Type: "part", SizeBytes: 214748364800, Filesystem: "ext4", UUID: "backup-uuid", Model: "Backup Disk"},
	}}
}

func configuredImportRequestValues(kind string) url.Values {
	values := url.Values{"csrf": {"csrf-token"}, "source_kind": {kind}}
	switch kind {
	case "local":
		values.Set("local_path", "/srv/import/server")
	case "backup":
		values.Set("source_path", "minecraft-2026-09-21.tar.gz")
	case "device":
		values.Set("source_device", "/dev/sdc1")
		values.Set("source_path", "server-export.tar.gz")
	case "nfs":
		values.Set("source_remote", "nas:/exports/migrations")
		values.Set("source_path", "server-export.tar.gz")
	case "smb":
		values.Set("source_remote", "//nas/migrations")
		values.Set("source_username", "voxel")
		values.Set("source_domain", "HOME")
		values.Set("source_smb_password", "source-secret")
		values.Set("source_path", "server-export.tar.gz")
	}
	return values
}

func importWebRequest(method, target string, values url.Values) *http.Request {
	body := strings.NewReader("")
	if values != nil {
		body = strings.NewReader(values.Encode())
	}
	req := httptest.NewRequest(method, target, body)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "null")
	}
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "csrf-token"})
	return req
}

type fakeServerMigrationRecoveryAPI struct {
	fakeServerMigrationAPI
	plan                 api.AdminMigrationRecoveryPlanResponse
	planErr              error
	planCalls            int
	planReq              api.AdminMigrationRecoveryPlanRequest
	apply                api.AdminMigrationApplyResponse
	applyErr             error
	applyCalls           int
	applyReq             api.AdminMigrationRecoveryApplyRequest
	resolve              api.AdminMigrationImportResolveResponse
	resolveErr           error
	resolveCalls         int
	resolveReq           api.AdminMigrationImportResolveRequest
	recoveryResolve      api.AdminMigrationRecoveryResolveResponse
	recoveryResolveErr   error
	recoveryResolveCalls int
	recoveryResolveReq   api.AdminMigrationRecoveryResolveRequest
}

func (f *fakeServerMigrationRecoveryAPI) AdminMigrationRecoveryPlan(_ context.Context, session string, request api.AdminMigrationRecoveryPlanRequest) (api.AdminMigrationRecoveryPlanResponse, error) {
	if session != "session-token" {
		return api.AdminMigrationRecoveryPlanResponse{}, api.ErrUnauthorized
	}
	f.planCalls++
	f.mutationCalls = append(f.mutationCalls, "recovery plan")
	f.planReq = request
	return f.plan, f.planErr
}

func (f *fakeServerMigrationRecoveryAPI) AdminMigrationRecoveryApply(_ context.Context, session string, request api.AdminMigrationRecoveryApplyRequest) (api.AdminMigrationApplyResponse, error) {
	if session != "session-token" {
		return api.AdminMigrationApplyResponse{}, api.ErrUnauthorized
	}
	f.applyCalls++
	f.mutationCalls = append(f.mutationCalls, "recovery apply")
	f.applyReq = request
	return f.apply, f.applyErr
}

func (f *fakeServerMigrationRecoveryAPI) AdminMigrationImportResolve(_ context.Context, session string, request api.AdminMigrationImportResolveRequest) (api.AdminMigrationImportResolveResponse, error) {
	if session != "session-token" {
		return api.AdminMigrationImportResolveResponse{}, api.ErrUnauthorized
	}
	f.resolveCalls++
	f.resolveReq = request
	return f.resolve, f.resolveErr
}

func (f *fakeServerMigrationRecoveryAPI) AdminMigrationRecoveryResolve(_ context.Context, session string, request api.AdminMigrationRecoveryResolveRequest) (api.AdminMigrationRecoveryResolveResponse, error) {
	if session != "session-token" {
		return api.AdminMigrationRecoveryResolveResponse{}, api.ErrUnauthorized
	}
	f.recoveryResolveCalls++
	f.recoveryResolveReq = request
	return f.recoveryResolve, f.recoveryResolveErr
}

func recoveryDiscoveryFixture() api.AdminMigrationRecoveryDiscoveryResponse {
	return api.AdminMigrationRecoveryDiscoveryResponse{
		OK: true, SchemaVersion: "v1", Configured: true,
		Transactions: []api.AdminMigrationRecoverySummary{
			{Transaction: "/var/lib/justvoxel/.justvoxel-import-safe", Phase: "rolled-back", SourceType: "paper", UpdatedAt: "2026-09-21T14:00:00Z", Mode: "configured", Finalizable: true},
			{Transaction: "/var/lib/justvoxel/.justvoxel-import-critical", Phase: "critical-rollback", SourceType: "vanilla", UpdatedAt: "2026-09-21T14:10:00Z", Mode: "configured-attention", Finalizable: true},
			{Transaction: "/var/lib/justvoxel/.justvoxel-import-manual", Phase: "prepared", SourceType: "paper", UpdatedAt: "2026-09-21T14:20:00Z", Mode: "manual", Finalizable: false},
		},
	}
}

func recoveryWebRequest(method, target string, values url.Values) *http.Request {
	return exportWebRequest(method, target, values)
}
