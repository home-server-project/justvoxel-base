package server

import (
	"context"
	"net/http"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

const restorePageFingerprint = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func restorePlan(players bool) api.AdminRestorePlanResponse {
	requirements := &api.AdminRestorePlanRequirements{
		DestructiveConfirmationRequired: true, PlayersConfirmationRequired: players,
		MinecraftState: "running", Online: 0, Players: []string{},
		ArchiveIntegrityValidationOnApply: true, ArchiveSafetyValidationOnApply: true, StagingSpaceValidationOnApply: true,
	}
	if players {
		requirements.Online = 1
		requirements.Players = []string{"PlayerOne"}
	}
	return api.AdminRestorePlanResponse{
		OK: true, SchemaVersion: "v1", PlanFingerprint: restorePageFingerprint,
		Normalized: &api.AdminRestorePlanNormalized{
			BackupID: "minecraft-2026-09-20-043000.tar.gz", Mode: "world", CreatedAt: "2026-09-20T08:30:00Z",
			SizeBytes: 12345, MetadataStatus: "valid",
			Metadata:        &api.AdminRestoreBackupMetadata{Minecraft: api.AdminRestoreMetadataMinecraft{VersionMode: "pinned", ConfiguredVersion: "26.2"}},
			Current:         api.AdminRestorePlanCurrent{VersionMode: "pinned", Version: "26.3"},
			VersionRelation: "backup_older",
			Validation:      api.AdminRestorePlanValidation{ArchiveIntegrity: "pending_apply", ArchiveSafety: "pending_apply", StagingSpace: "pending_apply"},
		},
		Warnings:     []api.AdminRestorePlanWarning{{Code: "backup_older", Message: "This backup is older than the configured Minecraft version."}},
		Requirements: requirements,
	}
}

type fakeNewBackupsAPI struct {
	fakeAPI
	role                          string
	backups                       api.AdminRestoreBackupsResponse
	backupResult                  api.ManualBackupResponse
	backupErr                     error
	backupCalls                   int
	discoveryCalls                int
	configuration                 api.AdminConfigurationDiscovery
	configurationPlan             api.AdminConfigurationChangeResponse
	configurationApply            api.AdminConfigurationChangeResponse
	configurationPlanErr          error
	configurationApplyErr         error
	plannedConfiguration          api.AdminConfigurationChangeRequest
	appliedConfiguration          api.AdminConfigurationChangeRequest
	configurationPlanCalls        int
	configurationApplyCalls       int
	configurationPlanCallsAtApply int
	storage                       api.AdminStorageDiscovery
	destinationStatus             api.AdminBackupStorageResponse
	destinationPlan               api.AdminBackupStorageResponse
	destinationApply              api.AdminBackupStorageResponse
	plannedDestination            api.AdminBackupStorageRequest
	appliedDestination            api.AdminBackupStorageRequest
	destinationStatusCalls        int
	destinationPlanCalls          int
	destinationApplyCalls         int
	restorePlanResult             api.AdminRestorePlanResponse
	restorePlanErr                error
	restoreApplyResult            api.AdminRestoreApplyResponse
	restoreApplyErr               error
	restorePlanRequest            api.AdminRestorePlanRequest
	restoreApplyRequest           api.AdminRestoreApplyRequest
	restorePlanCalls              int
	restoreApplyCalls             int
	currentRestoreOperation       *api.PersistentOperation
	restoreOperation              *api.PersistentOperation
	restoreOperationCalls         int
}

func defaultNewBackupsConfiguration() api.AdminConfigurationDiscovery {
	return api.AdminConfigurationDiscovery{
		Configured: true,
		Minecraft: api.AdminMinecraftConfiguration{
			JavaMemory: "4G", ContainerMemory: "6G", JavaPort: 25565,
			BedrockEnabled: false, BedrockPort: 19132, Timezone: "UTC",
			MaxPlayers: 10, MOTD: "JustVoxel", ImageTag: "stable",
			VersionMode: "pinned", Version: "26.3",
		},
		Backup: api.AdminBackupConfiguration{
			Keep: 7, Schedule: "*-*-* 04:30:00", TimerEnabled: true,
		},
	}
}

func (f *fakeNewBackupsAPI) Session(_ context.Context, session string) (api.SessionInfo, error) {
	if session != "session-token" {
		return api.SessionInfo{}, api.ErrUnauthorized
	}
	role := f.role
	if role == "" {
		role = "administrator"
	}
	return api.SessionInfo{Username: "voxel", Role: role, AuthSource: "system"}, nil
}

func (f *fakeNewBackupsAPI) ManualBackup(_ context.Context, session string) (api.ManualBackupResponse, error) {
	if session != "session-token" {
		return api.ManualBackupResponse{}, api.ErrUnauthorized
	}
	f.backupCalls++
	if f.backupResult.Message == "" && f.backupErr == nil {
		return api.ManualBackupResponse{OK: true, Message: "Manual backup requested."}, nil
	}
	return f.backupResult, f.backupErr
}

func (f *fakeNewBackupsAPI) AdminRestoreBackups(_ context.Context, session string) (api.AdminRestoreBackupsResponse, error) {
	if session != "session-token" {
		return api.AdminRestoreBackupsResponse{}, api.ErrUnauthorized
	}
	f.discoveryCalls++
	return f.backups, nil
}

func (f *fakeNewBackupsAPI) AdminConfiguration(_ context.Context, session string) (api.AdminConfigurationDiscovery, error) {
	if session != "session-token" {
		return api.AdminConfigurationDiscovery{}, api.ErrUnauthorized
	}
	if f.configuration.Configured {
		return f.configuration, nil
	}
	return defaultNewBackupsConfiguration(), nil
}

func (f *fakeNewBackupsAPI) AdminConfigurationPlan(_ context.Context, session string, request api.AdminConfigurationChangeRequest) (api.AdminConfigurationChangeResponse, error) {
	if session != "session-token" {
		return api.AdminConfigurationChangeResponse{}, api.ErrUnauthorized
	}
	f.configurationPlanCalls++
	f.plannedConfiguration = request
	if f.configurationPlan.OK || f.configurationPlanErr != nil {
		return f.configurationPlan, f.configurationPlanErr
	}
	return api.AdminConfigurationChangeResponse{
		OK:       true,
		Changes:  []api.AdminConfigurationChange{},
		Proposed: defaultNewBackupsConfiguration(),
	}, nil
}

func (f *fakeNewBackupsAPI) AdminConfigurationApply(_ context.Context, session string, request api.AdminConfigurationChangeRequest) (api.AdminConfigurationChangeResponse, error) {
	if session != "session-token" {
		return api.AdminConfigurationChangeResponse{}, api.ErrUnauthorized
	}
	f.configurationPlanCallsAtApply = f.configurationPlanCalls
	f.configurationApplyCalls++
	f.appliedConfiguration = request
	if f.configurationApply.OK || f.configurationApplyErr != nil {
		return f.configurationApply, f.configurationApplyErr
	}
	return api.AdminConfigurationChangeResponse{OK: true, Applied: true}, nil
}

func (f *fakeNewBackupsAPI) AdminStorage(_ context.Context, session string) (api.AdminStorageDiscovery, error) {
	if session != "session-token" {
		return api.AdminStorageDiscovery{}, api.ErrUnauthorized
	}
	if len(f.storage.Devices) > 0 || len(f.storage.SystemDisks) > 0 {
		return f.storage, nil
	}
	return api.AdminStorageDiscovery{
		SystemDisks: []string{"/dev/vda"},
		Devices: []api.AdminStorageDevice{
			{Name: "vda1", Path: "/dev/vda1", Parent: "vda", Type: "part", SizeBytes: 20 * 1024 * 1024 * 1024, Filesystem: "xfs", Mountpoints: []string{"/"}, System: true},
			{Name: "vdb1", Path: "/dev/vdb1", Parent: "vdb", Type: "part", SizeBytes: 100 * 1024 * 1024 * 1024, Filesystem: "xfs", Label: "BACKUP", UUID: "backup-uuid", Mountpoints: []string{"/var/mnt/backup"}, Model: "Virtual Disk"},
			{Name: "vdc1", Path: "/dev/vdc1", Parent: "vdc", Type: "part", SizeBytes: 50 * 1024 * 1024 * 1024, Filesystem: "", Model: "Blank Disk"},
		},
	}, nil
}

func (f *fakeNewBackupsAPI) AdminBackupStorageStatus(_ context.Context, session string) (api.AdminBackupStorageResponse, error) {
	if session != "session-token" {
		return api.AdminBackupStorageResponse{}, api.ErrUnauthorized
	}
	f.destinationStatusCalls++
	if f.destinationStatus.OK {
		return f.destinationStatus, nil
	}
	return api.AdminBackupStorageResponse{OK: true, Current: api.AdminBackupStorageTarget{
		Status: "ready", StatusDetail: "Backup destination is ready.", Type: "system", Path: "/var/lib/justvoxel/backups",
		AvailableBytes: 40 * 1024 * 1024 * 1024, FilesystemBytes: 80 * 1024 * 1024 * 1024, SamePhysicalDisk: true,
	}}, nil
}

func (f *fakeNewBackupsAPI) AdminBackupStoragePlan(_ context.Context, session string, request api.AdminBackupStorageRequest) (api.AdminBackupStorageResponse, error) {
	if session != "session-token" {
		return api.AdminBackupStorageResponse{}, api.ErrUnauthorized
	}
	f.destinationPlanCalls++
	f.plannedDestination = request
	if f.destinationPlan.OK {
		return f.destinationPlan, nil
	}
	return api.AdminBackupStorageResponse{OK: true, Changed: true, Proposed: api.AdminBackupStorageTarget{
		Type: request.Type, Path: request.Path, MountPoint: request.MountPoint, ExpectedSource: request.Source,
	}}, nil
}

func (f *fakeNewBackupsAPI) AdminBackupStorageApply(_ context.Context, session string, request api.AdminBackupStorageRequest) (api.AdminBackupStorageResponse, error) {
	if session != "session-token" {
		return api.AdminBackupStorageResponse{}, api.ErrUnauthorized
	}
	f.destinationApplyCalls++
	f.appliedDestination = request
	if f.destinationApply.OK {
		return f.destinationApply, nil
	}
	return api.AdminBackupStorageResponse{OK: true, Changed: true, Applied: true, Proposed: api.AdminBackupStorageTarget{Type: request.Type, Path: request.Path}}, nil
}

func (f *fakeNewBackupsAPI) AdminRestorePlan(_ context.Context, session string, request api.AdminRestorePlanRequest) (api.AdminRestorePlanResponse, error) {
	if session != "session-token" {
		return api.AdminRestorePlanResponse{}, api.ErrUnauthorized
	}
	f.restorePlanCalls++
	f.restorePlanRequest = request
	if f.restorePlanResult.SchemaVersion != "" || f.restorePlanErr != nil {
		return f.restorePlanResult, f.restorePlanErr
	}
	return restorePlan(false), nil
}

func (f *fakeNewBackupsAPI) AdminRestoreApply(_ context.Context, session string, request api.AdminRestoreApplyRequest) (api.AdminRestoreApplyResponse, error) {
	if session != "session-token" {
		return api.AdminRestoreApplyResponse{}, api.ErrUnauthorized
	}
	f.restoreApplyCalls++
	f.restoreApplyRequest = request
	return f.restoreApplyResult, f.restoreApplyErr
}

func (f *fakeNewBackupsAPI) AdminCurrentRestoreOperation(_ context.Context, session string) (api.PersistentOperationResponse, error) {
	if session != "session-token" {
		return api.PersistentOperationResponse{}, api.ErrUnauthorized
	}
	if f.currentRestoreOperation == nil {
		return api.PersistentOperationResponse{}, nil
	}
	copy := *f.currentRestoreOperation
	return api.PersistentOperationResponse{Operation: &copy}, nil
}

func (f *fakeNewBackupsAPI) AdminOperation(_ context.Context, session, id string) (api.PersistentOperationResponse, error) {
	if session != "session-token" {
		return api.PersistentOperationResponse{}, api.ErrUnauthorized
	}
	f.restoreOperationCalls++
	if f.restoreOperation == nil || f.restoreOperation.OperationID != id {
		return api.PersistentOperationResponse{}, &api.ResponseError{StatusCode: http.StatusNotFound, Message: "operation not found"}
	}
	copy := *f.restoreOperation
	return api.PersistentOperationResponse{Operation: &copy}, nil
}
