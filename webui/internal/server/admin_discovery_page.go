package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type adminDiscoveryAPI interface {
	Session(ctx context.Context, session string) (api.SessionInfo, error)
	AdminConfiguration(ctx context.Context, session string) (api.AdminConfigurationDiscovery, error)
	AdminStorage(ctx context.Context, session string) (api.AdminStorageDiscovery, error)
	AdminSetupDefaults(ctx context.Context, session string) (api.AdminSetupDefaults, error)
}

type storageBrowserMigrationDiscoveryAPI interface {
	AdminDataMigrationDiscovery(ctx context.Context, session string) (api.AdminDataMigrationDiscoveryResponse, error)
}

type serverSettingsPageData struct {
	Title                    string
	Version                  string
	ManagementAPI            string
	CSRF                     string
	Identity                 api.SessionInfo
	Configuration            api.AdminConfigurationDiscovery
	Defaults                 api.AdminSetupDefaults
	Form                     api.AdminConfigurationChangeRequest
	Plan                     *api.AdminConfigurationChangeResponse
	Error                    string
	Message                  string
	SystemMemory             string
	SystemReserveMinimum     string
	SystemReserveRecommended string
}

type storageDeviceView struct {
	Name                 string
	Path                 string
	Parent               string
	Type                 string
	Size                 string
	Filesystem           string
	Label                string
	UUID                 string
	Mountpoints          string
	Model                string
	Transport            string
	ReadOnly             bool
	System               bool
	FilesystemUsed       string
	FilesystemFree       string
	FilesystemUsageKnown bool
}

type storageSettingsPageData struct {
	Title         string
	Version       string
	ManagementAPI string
	CSRF          string
	Identity      api.SessionInfo
	Configuration api.AdminConfigurationDiscovery
	SystemDisks   []string
	Devices       []storageDeviceView
}

type storageBrowserPartitionView struct {
	storageDeviceView
	FilesystemDisplay   string
	Role                string
	Mounted             bool
	Formatted           bool
	Swap                bool
	Interactive         bool
	MinecraftCandidate  bool
	MinecraftMountPoint string
}

type storageBrowserFreeSpaceView struct {
	Device    string
	Start     string
	End       string
	Size      string
	SizeBytes uint64
}

type storageBrowserDiskView struct {
	Name               string
	Path               string
	Size               string
	Model              string
	Transport          string
	System             bool
	MinecraftWholeDisk bool
	Partitions         []storageBrowserPartitionView
	FreeSpaces         []storageBrowserFreeSpaceView
}

type storageBrowserPageData struct {
	Title                  string
	Version                string
	ManagementAPI          string
	CSRF                   string
	Identity               api.SessionInfo
	Configuration          api.AdminConfigurationDiscovery
	Disks                  []storageBrowserDiskView
	InternalDisks          []storageBrowserDiskView
	ExternalDisks          []storageBrowserDiskView
	SelectedDisk           string
	MinecraftMigrationNote string
}

func (a *App) registerAdminDiscoveryPages(mux *http.ServeMux) {
	mux.HandleFunc("GET /settings/server", a.serverSettingsPage)
	mux.HandleFunc("POST /settings/server/plan", a.serverSettingsPlan)
	mux.HandleFunc("POST /settings/server/apply", a.serverSettingsApply)
	mux.HandleFunc("GET /settings/storage", a.storageSettingsPage)
	mux.HandleFunc("GET /settings/new-storage", a.storageBrowserPage)
	mux.HandleFunc("GET /workspace/storage", a.storageBrowserWindow)
	a.registerAdminBackupStoragePages(mux)
	a.registerAdminStorageProvisionPages(mux)
	a.registerStorageBrowserActionRoutes(mux)
	a.registerSetupWizardRoutes(mux)
	a.registerSetupWizardReviewRoutes(mux)
}

func (a *App) serverSettingsPage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.adminDiscoveryRequest(w, r)
	if !ok {
		return
	}
	configuration, err := client.AdminConfiguration(r.Context(), session)
	if err != nil {
		a.handleAdminDiscoveryError(w, r, err)
		return
	}
	defaults, err := client.AdminSetupDefaults(r.Context(), session)
	if err != nil {
		a.handleAdminDiscoveryError(w, r, err)
		return
	}
	message := ""
	if r.URL.Query().Get("result") == "saved" {
		switch {
		case r.URL.Query().Get("memory_restart") == "1":
			message = "Memory settings saved and Minecraft restarted with the new limits."
		case r.URL.Query().Get("next_start") == "1":
			message = "Memory settings saved. Minecraft is stopped, so the new limits will be used on its next start."
		case r.URL.Query().Get("restart") == "1":
			message = "Settings saved. Minecraft was not restarted; restart it when it is safe to apply the server changes."
		default:
			message = "Settings saved and applied."
		}
	}
	data := a.buildServerSettingsPageData(identity, configuration, defaults, configurationRequestFromDiscovery(configuration), nil, "", message)
	data.CSRF = csrfFromRequest(r)
	a.renderAdminDiscovery(w, "server_settings.html", data)
}

func (a *App) storageSettingsPage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.adminDiscoveryRequest(w, r)
	if !ok {
		return
	}
	configuration, err := client.AdminConfiguration(r.Context(), session)
	if err != nil {
		a.handleAdminDiscoveryError(w, r, err)
		return
	}
	storage, err := client.AdminStorage(r.Context(), session)
	if err != nil {
		a.handleAdminDiscoveryError(w, r, err)
		return
	}
	devices := make([]storageDeviceView, 0, len(storage.Devices))
	for _, device := range storage.Devices {
		devices = append(devices, storageDeviceView{
			Name: device.Name, Path: device.Path, Parent: device.Parent, Type: device.Type, Size: humanBytes(device.SizeBytes),
			Filesystem: device.Filesystem, Label: device.Label, UUID: device.UUID,
			Mountpoints: strings.Join(device.Mountpoints, ", "), Model: device.Model, Transport: device.Transport,
			ReadOnly: device.ReadOnly, System: device.System,
		})
	}
	a.renderAdminDiscovery(w, "storage_settings.html", storageSettingsPageData{
		Title: "Storage overview", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI,
		CSRF: csrfFromRequest(r), Identity: identity, Configuration: configuration,
		SystemDisks: storage.SystemDisks, Devices: devices,
	})
}

func (a *App) storageBrowserPage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.adminDiscoveryRequest(w, r)
	if !ok {
		return
	}
	data, err := a.buildStorageBrowserPageData(r.Context(), session, client, identity, csrfFromRequest(r))
	if err != nil {
		a.handleAdminDiscoveryError(w, r, err)
		return
	}
	a.renderAdminDiscovery(w, "storage_browser.html", data)
}

func (a *App) storageBrowserWindow(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.adminDiscoveryRequest(w, r)
	if !ok {
		return
	}
	data, err := a.buildStorageBrowserPageData(r.Context(), session, client, identity, csrfFromRequest(r))
	if err != nil {
		a.handleAdminDiscoveryError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := a.templates.ExecuteTemplate(w, "storage_browser_content", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (a *App) buildStorageBrowserPageData(ctx context.Context, session string, client adminDiscoveryAPI, identity api.SessionInfo, csrf string) (storageBrowserPageData, error) {
	configuration, err := client.AdminConfiguration(ctx, session)
	if err != nil {
		return storageBrowserPageData{}, err
	}
	storage, err := client.AdminStorage(ctx, session)
	if err != nil {
		return storageBrowserPageData{}, err
	}

	migrationCandidates := make(map[string]api.AdminDataMigrationCandidate)
	migrationWholeDisks := make(map[string]bool)
	migrationNote := ""
	if migrationClient, ok := any(client).(storageBrowserMigrationDiscoveryAPI); ok {
		discovery, err := migrationClient.AdminDataMigrationDiscovery(ctx, session)
		if err != nil {
			migrationNote = "Minecraft storage assignment is temporarily unavailable. Other storage actions are still available."
		} else {
			for _, candidate := range discovery.Partitions {
				migrationCandidates[candidate.Path] = candidate
			}
			for _, candidate := range discovery.WholeDisks {
				migrationWholeDisks[candidate.Path] = true
			}
		}
	} else {
		migrationNote = "Minecraft storage assignment is unavailable in this WebUI build."
	}

	systemDisks := make(map[string]struct{}, len(storage.SystemDisks))
	for _, path := range storage.SystemDisks {
		systemDisks[path] = struct{}{}
	}
	systemDiskNames := make(map[string]bool)
	for _, device := range storage.Devices {
		if device.Type == "part" && storageBrowserLooksSystem(device) {
			systemDiskNames[device.Parent] = true
		}
	}

	disks := make([]storageBrowserDiskView, 0)
	diskIndex := make(map[string]int)
	diskPathIndex := make(map[string]int)
	for _, device := range storage.Devices {
		if device.Type != "disk" || strings.HasPrefix(strings.ToLower(device.Name), "zram") {
			continue
		}
		_, listedSystemDisk := systemDisks[device.Path]
		diskIndex[device.Name] = len(disks)
		diskPathIndex[device.Path] = len(disks)
		disks = append(disks, storageBrowserDiskView{
			Name: device.Name, Path: device.Path, Size: humanBytes(device.SizeBytes),
			Model: device.Model, Transport: device.Transport,
			System:             device.System || listedSystemDisk || systemDiskNames[device.Name] || storageBrowserLooksSystem(device),
			MinecraftWholeDisk: migrationWholeDisks[device.Path],
		})
	}
	for _, device := range storage.Devices {
		if device.Type != "part" {
			continue
		}
		index, exists := diskIndex[device.Parent]
		if !exists {
			continue
		}
		systemPartition := device.System || storageBrowserLooksSystem(device)
		roleDevice := device
		roleDevice.System = systemPartition
		role := storageBrowserRole(roleDevice, configuration)
		migrationCandidate, canMigrate := migrationCandidates[device.Path]
		if strings.Contains(role, "Minecraft") || strings.Contains(role, "Backups") {
			canMigrate = false
		}
		view := storageBrowserPartitionView{
			storageDeviceView: storageDeviceView{
				Name: device.Name, Path: device.Path, Parent: device.Parent, Type: device.Type,
				Size: humanBytes(device.SizeBytes), Filesystem: device.Filesystem, Label: device.Label,
				UUID: device.UUID, Mountpoints: strings.Join(device.Mountpoints, ", "), Model: device.Model,
				Transport: device.Transport, ReadOnly: device.ReadOnly, System: systemPartition,
				FilesystemUsed: humanBytes(device.FilesystemUsedBytes), FilesystemFree: humanBytes(device.FilesystemFreeBytes),
				FilesystemUsageKnown: device.FilesystemUsageKnown,
			},
			FilesystemDisplay: storageBrowserFilesystemDisplay(device.Filesystem),
			Role:              role, Mounted: len(device.Mountpoints) > 0, Formatted: device.Filesystem != "",
			Swap:                device.Filesystem == "swap",
			Interactive:         device.Filesystem != "swap",
			MinecraftCandidate:  canMigrate,
			MinecraftMountPoint: migrationCandidate.Mountpoint,
		}
		disks[index].Partitions = append(disks[index].Partitions, view)
	}
	for _, free := range storage.FreeSpaces {
		index, exists := diskPathIndex[free.Device]
		if !exists {
			continue
		}
		disks[index].FreeSpaces = append(disks[index].FreeSpaces, storageBrowserFreeSpaceView{
			Device: free.Device, Start: free.Start, End: free.End,
			Size: humanBytes(free.SizeBytes), SizeBytes: free.SizeBytes,
		})
	}

	internalDisks := make([]storageBrowserDiskView, 0, len(disks))
	externalDisks := make([]storageBrowserDiskView, 0)
	for _, disk := range disks {
		if strings.EqualFold(strings.TrimSpace(disk.Transport), "usb") {
			externalDisks = append(externalDisks, disk)
			continue
		}
		internalDisks = append(internalDisks, disk)
	}
	selectedDisk := ""
	if len(internalDisks) > 0 {
		selectedDisk = internalDisks[0].Name
	} else if len(externalDisks) > 0 {
		selectedDisk = externalDisks[0].Name
	}

	return storageBrowserPageData{
		Title: "Storage", Version: a.config.Version, ManagementAPI: a.config.ManagementAPI,
		CSRF: csrf, Identity: identity, Configuration: configuration, Disks: disks,
		InternalDisks: internalDisks, ExternalDisks: externalDisks, SelectedDisk: selectedDisk,
		MinecraftMigrationNote: migrationNote,
	}, nil
}

func storageBrowserLooksSystem(device api.AdminStorageDevice) bool {
	for _, mountpoint := range device.Mountpoints {
		switch {
		case mountpoint == "/",
			mountpoint == "/boot",
			strings.HasPrefix(mountpoint, "/boot/"),
			mountpoint == "/var",
			mountpoint == "/var/tmp",
			mountpoint == "/var/lib/containers",
			strings.HasPrefix(mountpoint, "/var/lib/containers/"),
			mountpoint == "/etc",
			strings.HasPrefix(mountpoint, "/etc/"),
			mountpoint == "/sysroot",
			strings.HasPrefix(mountpoint, "/sysroot/"):
			return true
		}
	}
	return false
}

func storageBrowserFilesystemDisplay(filesystem string) string {
	switch strings.ToLower(strings.TrimSpace(filesystem)) {
	case "xfs":
		return "XFS"
	case "ext4":
		return "ext4"
	case "btrfs":
		return "Btrfs"
	case "ntfs", "ntfs-3g":
		return "NTFS"
	case "vfat", "fat", "fat32":
		return "FAT / FAT32"
	case "exfat":
		return "exFAT"
	case "swap":
		return "Swap"
	case "":
		return "Not formatted"
	default:
		return filesystem
	}
}

func storageBrowserRole(device api.AdminStorageDevice, configuration api.AdminConfigurationDiscovery) string {
	if device.Filesystem == "swap" {
		return "Swap"
	}
	if device.System {
		return "System partition"
	}
	roles := make([]string, 0, 2)
	if configuration.Configured {
		if storageDeviceMatchesConfiguration(device, configuration.Minecraft.DataExpectedUUID, configuration.Minecraft.DataMountPoint, configuration.Minecraft.DataPath) {
			roles = append(roles, "Minecraft")
		}
		if storageDeviceMatchesConfiguration(device, configuration.Backup.ExpectedUUID, configuration.Backup.MountPoint, configuration.Backup.Path) {
			roles = append(roles, "Backups")
		}
	}
	if len(roles) > 0 {
		return strings.Join(roles, " + ")
	}
	if device.Filesystem == "" {
		return "Not formatted"
	}
	if len(device.Mountpoints) > 0 {
		return "Mounted"
	}
	return "Available"
}

func storageDeviceMatchesConfiguration(device api.AdminStorageDevice, expectedUUID, expectedMountPoint, configuredPath string) bool {
	if expectedUUID != "" && device.UUID != "" && device.UUID == expectedUUID {
		return true
	}
	for _, mountpoint := range device.Mountpoints {
		if expectedMountPoint != "" && mountpoint == expectedMountPoint {
			return true
		}
		if mountpoint != "" && configuredPath != "" && (configuredPath == mountpoint || strings.HasPrefix(configuredPath, strings.TrimRight(mountpoint, "/")+"/")) {
			return true
		}
	}
	return false
}

func (a *App) adminDiscoveryRequest(w http.ResponseWriter, r *http.Request) (string, adminDiscoveryAPI, api.SessionInfo, bool) {
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return "", nil, api.SessionInfo{}, false
	}
	client, ok := a.api.(adminDiscoveryAPI)
	if !ok {
		http.Error(w, "appliance discovery is unavailable", http.StatusServiceUnavailable)
		return "", nil, api.SessionInfo{}, false
	}
	identity, err := client.Session(r.Context(), session)
	if err != nil {
		a.handleAdminDiscoveryError(w, r, err)
		return "", nil, api.SessionInfo{}, false
	}
	if identity.Role != "administrator" {
		http.Error(w, "Administrator access required", http.StatusForbidden)
		return "", nil, api.SessionInfo{}, false
	}
	return session, client, identity, true
}

func (a *App) handleAdminDiscoveryError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, api.ErrUnauthorized) {
		a.clearSessionCookies(w)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if errors.Is(err, api.ErrPasswordChangeRequired) {
		http.Redirect(w, r, "/password", http.StatusSeeOther)
		return
	}
	http.Error(w, "appliance discovery is unavailable", http.StatusBadGateway)
}

func (a *App) renderAdminDiscovery(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := a.templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func humanBytes(value uint64) string {
	const unit = uint64(1024)
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	div, exp := unit, 0
	for n := value / unit; n >= unit && exp < 5; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(value)/float64(div), "KMGTPE"[exp])
}
