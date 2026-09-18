package server

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

var setupStoragePathPattern = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)
var setupBackupTimePattern = regexp.MustCompile(`^([01]?[0-9]|2[0-3]):([0-5][0-9])$`)

type setupStorageDraft struct {
	Type       string
	Path       string
	Device     string
	MountPoint string
	Complete   bool
}

type setupBackupDraft struct {
	Automatic  bool
	DailyTime  string
	Keep       string
	Type       string
	Path       string
	Device     string
	MountPoint string
	Source     string
	Username   string
	Domain     string
	Complete   bool
}

type setupFilesystemView struct {
	Path       string
	Name       string
	Size       string
	Filesystem string
	Label      string
	UUID       string
	Mountpoint string
	Model      string
	Transport  string
	SystemDisk bool
}

func (a *App) registerSetupWizardStorageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /setup/storage", a.setupWizardSaveStorage)
	mux.HandleFunc("POST /setup/backups", a.setupWizardSaveBackups)
}

func (a *App) setupWizardSaveStorage(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.setupWizardRequest(w, r, true)
	if !ok {
		return
	}
	draft, exists := firstRunSetupDrafts.get(a, session)
	if !exists || !draft.Started {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	storage, err := client.AdminStorage(r.Context(), session)
	if err != nil {
		a.handleAdminDiscoveryError(w, r, err)
		return
	}
	draft.Inventory = storage
	draft.Storage = parseSetupStorageForm(r, draft.Defaults)

	if r.FormValue("direction") == "back" {
		draft.Storage.Complete = false
		draft.CurrentStep = 2
		firstRunSetupDrafts.save(a, session, draft)
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if err := validateSetupStorage(draft.Storage, draft.Inventory, draft.Defaults); err != nil {
		draft.Storage.Complete = false
		firstRunSetupDrafts.save(a, session, draft)
		w.WriteHeader(http.StatusBadRequest)
		a.renderSetupWizard(w, identity, draft, csrfFromRequest(r), err.Error())
		return
	}
	draft.Storage.Complete = true
	draft.CurrentStep = 4
	firstRunSetupDrafts.save(a, session, draft)
	http.Redirect(w, r, "/setup", http.StatusSeeOther)
}

func (a *App) setupWizardSaveBackups(w http.ResponseWriter, r *http.Request) {
	session, client, identity, ok := a.setupWizardRequest(w, r, true)
	if !ok {
		return
	}
	draft, exists := firstRunSetupDrafts.get(a, session)
	if !exists || !draft.Started {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	storage, err := client.AdminStorage(r.Context(), session)
	if err != nil {
		a.handleAdminDiscoveryError(w, r, err)
		return
	}
	draft.Inventory = storage
	draft.Backups = parseSetupBackupForm(r, draft.Defaults)

	if r.FormValue("direction") == "back" {
		draft.Backups.Complete = false
		draft.CurrentStep = 3
		firstRunSetupDrafts.save(a, session, draft)
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if err := validateSetupBackups(draft.Backups, draft.Storage, draft.Inventory, draft.Defaults); err != nil {
		draft.Backups.Complete = false
		firstRunSetupDrafts.save(a, session, draft)
		w.WriteHeader(http.StatusBadRequest)
		a.renderSetupWizard(w, identity, draft, csrfFromRequest(r), err.Error())
		return
	}
	draft.Backups.Complete = true
	draft.CurrentStep = 5
	firstRunSetupDrafts.save(a, session, draft)
	http.Redirect(w, r, "/setup/review", http.StatusSeeOther)
}

func initialSetupStorageDraft(defaults api.AdminSetupDefaults) setupStorageDraft {
	return setupStorageDraft{Type: "system", Path: defaults.DataPath}
}

func initialSetupBackupDraft(defaults api.AdminSetupDefaults) setupBackupDraft {
	return setupBackupDraft{
		Automatic: true,
		DailyTime: defaults.BackupDailyTime,
		Keep:      strconv.Itoa(defaults.BackupKeep),
		Type:      "system",
		Path:      defaults.BackupPath,
	}
}

func parseSetupStorageForm(r *http.Request, defaults api.AdminSetupDefaults) setupStorageDraft {
	out := setupStorageDraft{
		Type:       strings.TrimSpace(r.FormValue("storage_type")),
		Path:       strings.TrimSpace(r.FormValue("storage_path")),
		Device:     strings.TrimSpace(r.FormValue("storage_device")),
		MountPoint: strings.TrimSpace(r.FormValue("storage_mount_point")),
	}
	if out.Type == "system" {
		out.Path = defaults.DataPath
		out.Device = ""
		out.MountPoint = ""
	}
	return out
}

func parseSetupBackupForm(r *http.Request, defaults api.AdminSetupDefaults) setupBackupDraft {
	out := setupBackupDraft{
		Automatic:  r.FormValue("backup_automatic") == "on",
		DailyTime:  strings.TrimSpace(r.FormValue("backup_daily_time")),
		Keep:       strings.TrimSpace(r.FormValue("backup_keep")),
		Type:       strings.TrimSpace(r.FormValue("backup_type")),
		Path:       strings.TrimSpace(r.FormValue("backup_path")),
		Device:     strings.TrimSpace(r.FormValue("backup_device")),
		MountPoint: strings.TrimSpace(r.FormValue("backup_mount_point")),
		Source:     strings.TrimSpace(r.FormValue("backup_source")),
		Username:   strings.TrimSpace(r.FormValue("backup_username")),
		Domain:     strings.TrimSpace(r.FormValue("backup_domain")),
	}
	if out.Type == "system" {
		out.Path = defaults.BackupPath
		out.Device = ""
		out.MountPoint = ""
		out.Source = ""
		out.Username = ""
		out.Domain = ""
	}
	return out
}

func validateSetupStorage(storage setupStorageDraft, inventory api.AdminStorageDiscovery, defaults api.AdminSetupDefaults) error {
	switch storage.Type {
	case "system":
		if storage.Path != defaults.DataPath {
			return errors.New("System storage uses the standard JustVoxel Minecraft data directory.")
		}
	case "partition":
		device, ok := safeSetupDevice(inventory, storage.Device)
		if !ok {
			return errors.New("Choose one of the safe existing XFS, ext4, or Btrfs filesystems shown by JustVoxel.")
		}
		mount, err := validateSetupLocalMount(storage.MountPoint, device)
		if err != nil {
			return err
		}
		if !validSetupPath(storage.Path) || !setupPathWithin(storage.Path, mount) {
			return errors.New("Minecraft data directory must be a safe absolute path inside the selected filesystem mount point.")
		}
	default:
		return errors.New("Choose JustVoxel system storage or an existing local filesystem.")
	}
	return nil
}

func validateSetupBackups(backups setupBackupDraft, data setupStorageDraft, inventory api.AdminStorageDiscovery, defaults api.AdminSetupDefaults) error {
	if _, err := parsePositiveFormInt(backups.Keep, "Backup retention"); err != nil {
		return err
	}
	if backups.Automatic {
		if _, err := normalizeSetupBackupTime(backups.DailyTime); err != nil {
			return err
		}
	}

	switch backups.Type {
	case "system":
		if backups.Path != defaults.BackupPath {
			return errors.New("System backup storage uses the standard JustVoxel backup directory.")
		}
	case "partition":
		device, ok := safeSetupDevice(inventory, backups.Device)
		if !ok {
			return errors.New("Choose one of the safe existing XFS, ext4, or Btrfs filesystems shown by JustVoxel.")
		}
		mount, err := validateSetupLocalMount(backups.MountPoint, device)
		if err != nil {
			return err
		}
		if !validSetupPath(backups.Path) || !setupPathWithin(backups.Path, mount) {
			return errors.New("Backup directory must be a safe absolute path inside the selected filesystem mount point.")
		}
	case "nfs":
		if !validSetupNFS(backups.Source) {
			return errors.New("NFS source must look like server:/export and cannot contain spaces.")
		}
		if !validSetupMountPoint(backups.MountPoint) {
			return errors.New("Choose a safe local mount point for the NFS share.")
		}
		if !validSetupPath(backups.Path) || !setupPathWithin(backups.Path, backups.MountPoint) {
			return errors.New("Backup directory must be inside the NFS mount point.")
		}
	case "smb":
		if !validSetupSMB(backups.Source) {
			return errors.New("SMB source must look like //server/share and cannot contain spaces.")
		}
		if !validSetupMountPoint(backups.MountPoint) {
			return errors.New("Choose a safe local mount point for the SMB share.")
		}
		if !validSetupPath(backups.Path) || !setupPathWithin(backups.Path, backups.MountPoint) {
			return errors.New("Backup directory must be inside the SMB mount point.")
		}
		if backups.Username == "" {
			return errors.New("SMB username is required. The password is requested only when setup is finally applied.")
		}
		if strings.ContainsAny(backups.Username, "\r\n") || strings.ContainsAny(backups.Domain, "\r\n") {
			return errors.New("SMB username or domain is invalid.")
		}
	default:
		return errors.New("Choose a supported backup destination.")
	}

	if data.Path != "" && backups.Path != "" && setupPathsOverlap(data.Path, backups.Path) {
		return errors.New("Minecraft data and backups cannot use the same directory or contain one another.")
	}
	return nil
}

func setupFilesystemViews(storage api.AdminStorageDiscovery) []setupFilesystemView {
	out := make([]setupFilesystemView, 0)
	for _, device := range storage.Devices {
		if !safeSetupDeviceValue(device) {
			continue
		}
		mountpoint := ""
		if len(device.Mountpoints) > 0 {
			mountpoint = device.Mountpoints[0]
		}
		model := strings.TrimSpace(device.Model)
		if model == "" {
			model = "Local filesystem"
		}
		out = append(out, setupFilesystemView{
			Path: device.Path, Name: device.Name, Size: humanBytes(device.SizeBytes), Filesystem: device.Filesystem,
			Label: device.Label, UUID: device.UUID, Mountpoint: mountpoint, Model: model,
			Transport: device.Transport, SystemDisk: device.System,
		})
	}
	return out
}

func safeSetupDevice(storage api.AdminStorageDiscovery, path string) (api.AdminStorageDevice, bool) {
	for _, device := range storage.Devices {
		if device.Path == path && safeSetupDeviceValue(device) {
			return device, true
		}
	}
	return api.AdminStorageDevice{}, false
}

func safeSetupDeviceValue(device api.AdminStorageDevice) bool {
	if device.Type != "part" || device.ReadOnly {
		return false
	}
	switch device.Filesystem {
	case "xfs", "ext4", "btrfs":
	default:
		return false
	}
	for _, mountpoint := range device.Mountpoints {
		switch mountpoint {
		case "/", "/boot", "/boot/efi", "/var":
			return false
		}
	}
	return device.UUID != ""
}

func validateSetupLocalMount(requested string, device api.AdminStorageDevice) (string, error) {
	if len(device.Mountpoints) > 0 && device.Mountpoints[0] != "" {
		mounted := strings.TrimSuffix(device.Mountpoints[0], "/")
		if mounted == "" {
			mounted = "/"
		}
		if requested != mounted {
			return "", fmt.Errorf("%s is already mounted at %s. Use that mount point.", device.Path, mounted)
		}
		return mounted, nil
	}
	if !validSetupMountPoint(requested) {
		return "", errors.New("Choose a safe mount point for the selected local filesystem.")
	}
	return requested, nil
}

func validSetupPath(value string) bool {
	return value != "/" && setupStoragePathPattern.MatchString(value) && !strings.Contains(value, "//")
}

func validSetupMountPoint(value string) bool {
	if !validSetupPath(value) {
		return false
	}
	switch value {
	case "/boot", "/boot/efi", "/var":
		return false
	}
	return true
}

func setupPathWithin(path, mount string) bool {
	mount = strings.TrimSuffix(mount, "/")
	return path == mount || strings.HasPrefix(path, mount+"/")
}

func setupPathsOverlap(a, b string) bool {
	a = strings.TrimSuffix(a, "/")
	b = strings.TrimSuffix(b, "/")
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func validSetupNFS(source string) bool {
	return source != "" && strings.Contains(source, ":") && !strings.ContainsAny(source, " \t\r\n")
}

func validSetupSMB(source string) bool {
	if !strings.HasPrefix(source, "//") || strings.ContainsAny(source, " \t\r\n") {
		return false
	}
	rest := strings.TrimPrefix(source, "//")
	parts := strings.SplitN(rest, "/", 2)
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}

func normalizeSetupBackupTime(value string) (string, error) {
	match := setupBackupTimePattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) != 3 {
		return "", errors.New("Automatic backup time must use HH:MM, for example 04:30.")
	}
	hour, _ := strconv.Atoi(match[1])
	minute, _ := strconv.Atoi(match[2])
	return fmt.Sprintf("%02d:%02d", hour, minute), nil
}

func setupSameDiskWarning(draft setupDraft) string {
	if draft.Storage.Type == "" || draft.Backups.Type == "" {
		return ""
	}
	if draft.Backups.Type == "nfs" || draft.Backups.Type == "smb" {
		return ""
	}
	if draft.Storage.Type == "system" && draft.Backups.Type == "system" {
		return "Minecraft data and backups are on the same physical system disk. This helps with accidental file loss, but it does not protect against failure of that disk."
	}
	if draft.Storage.Type == "system" && draft.Backups.Type == "partition" {
		if device, ok := safeSetupDevice(draft.Inventory, draft.Backups.Device); ok && device.System {
			return "The selected backup filesystem is on the same physical system disk as Minecraft data. A separate disk or NAS provides a stronger backup failure boundary."
		}
		return ""
	}
	if draft.Storage.Type == "partition" && draft.Backups.Type == "system" {
		if device, ok := safeSetupDevice(draft.Inventory, draft.Storage.Device); ok && device.System {
			return "Minecraft data and system backups share the same physical system disk. A separate disk or NAS provides a stronger backup failure boundary."
		}
		return ""
	}
	if draft.Storage.Type == "partition" && draft.Backups.Type == "partition" {
		dataDevice, dataOK := safeSetupDevice(draft.Inventory, draft.Storage.Device)
		backupDevice, backupOK := safeSetupDevice(draft.Inventory, draft.Backups.Device)
		if dataOK && backupOK && setupPhysicalDiskKey(dataDevice) != "" && setupPhysicalDiskKey(dataDevice) == setupPhysicalDiskKey(backupDevice) {
			return "Minecraft data and backups are on partitions of the same physical disk. This is a weaker failure boundary than a separate disk or NAS."
		}
	}
	return ""
}

func setupPhysicalDiskKey(device api.AdminStorageDevice) string {
	if device.Parent != "" {
		if strings.HasPrefix(device.Parent, "/dev/") {
			return device.Parent
		}
		return "/dev/" + device.Parent
	}
	return device.Path
}
