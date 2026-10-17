package server

import (
	"context"
	"encoding/json"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type fakeNewBackupsAPI struct {
	fakeAPI
	role                          string
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

// All workspace backup fixtures have one actual registered immutable identity.
func (f *fakeNewBackupsAPI) MinecraftInstances(context.Context, string) ([]api.RegisteredMinecraftInstance, error) {
	return []api.RegisteredMinecraftInstance{{ID: "jv-family1", DisplayName: "Family"}}, nil
}
func (f *fakeNewBackupsAPI) MinecraftInstanceRuntimes(context.Context, string) ([]string, error) {
	return []string{"jv-family1"}, nil
}
func (f *fakeNewBackupsAPI) MinecraftInstanceConfiguration(context.Context, string, string) (api.InstanceRuntimeSettings, error) {
	return api.InstanceRuntimeSettings{Software: "paper", Version: "26.3"}, nil
}
func (f *fakeNewBackupsAPI) MinecraftInstanceStatus(context.Context, string, string) (api.MinecraftInstanceStatus, error) {
	return api.MinecraftInstanceStatus{ID: "jv-family1", State: "stopped"}, nil
}
func (f *fakeNewBackupsAPI) MinecraftInstanceBackups(_ context.Context, session, id, action string, request any) (json.RawMessage, error) {
	if id != "jv-family1" {
		return nil, &api.ResponseError{StatusCode: 404}
	}
	switch action {
	case "now":
		if session != "session-token" {
			return nil, api.ErrUnauthorized
		}
		f.backupCalls++
		if f.backupErr != nil {
			return nil, f.backupErr
		}
		return json.RawMessage(`{"ok":true}`), nil
	case "inventory":
		f.discoveryCalls++
		return json.RawMessage(`{"id":"jv-family1","backups":[]}`), nil
	case "settings":
		return json.RawMessage(`{"enabled":true,"time":"04:30","retention":7}`), nil
	case "restore/status":
		return json.RawMessage(`{"id":"jv-family1","state":"idle"}`), nil
	default:
		return json.RawMessage(`{"ok":true,"state":"idle"}`), nil
	}
}
