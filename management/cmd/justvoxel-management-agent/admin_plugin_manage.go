package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

type pluginManageAcknowledgeRequest struct {
	OperationID string `json:"operation_id"`
}
type pluginManageRequest struct {
	Source    string `json:"source,omitempty"`
	Action    string `json:"action"`
	ProjectID string `json:"project_id"`
}
type pluginManageApplyRequest struct {
	Source          string `json:"source,omitempty"`
	Action          string `json:"action"`
	ProjectID       string `json:"project_id"`
	PlanFingerprint string `json:"plan_fingerprint"`
	ConfirmPlayers  bool   `json:"confirm_players"`
}
type pluginLocationEvidence struct {
	Entry pluginManifestEntry `json:"entry"`
	State string              `json:"state"`
}
type pluginManagePlan struct {
	Source               string                   `json:"source,omitempty"`
	Action               string                   `json:"action"`
	ProjectID            string                   `json:"project_id"`
	Name                 string                   `json:"project_name"`
	State                string                   `json:"state"`
	Entry                pluginManifestEntry      `json:"-"`
	Server               pluginServerContext      `json:"server"`
	DataPath             string                   `json:"-"`
	DataDevice           uint64                   `json:"-"`
	DataInode            uint64                   `json:"-"`
	Manifest             []byte                   `json:"-"`
	Parents              []pluginLocationEvidence `json:"-"`
	Online               int                      `json:"online"`
	Players              []string                 `json:"players"`
	ConfirmationRequired bool                     `json:"confirmation_required"`
	RestartRequired      bool                     `json:"restart_required"`
	Fingerprint          string                   `json:"plan_fingerprint"`
}
type pluginManageTransaction struct {
	InstanceID        string              `json:"instance_id"`
	SchemaVersion     string              `json:"schema_version"`
	OperationID       string              `json:"operation_id"`
	Action            string              `json:"action"`
	PlanFingerprint   string              `json:"plan_fingerprint"`
	DataPath          string              `json:"data_path"`
	DataDevice        uint64              `json:"data_device"`
	DataInode         uint64              `json:"data_inode"`
	MinecraftVersion  string              `json:"minecraft_version"`
	OriginallyRunning bool                `json:"originally_running"`
	Entry             pluginManifestEntry `json:"entry"`
	OriginalState     string              `json:"original_state"`
	Manifest          []byte              `json:"manifest"`
}

// Walk every component without following symbolic links; retain the resulting
// descriptor for all directory-relative JAR operations.
func openPluginDataDirectory(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return nil, errors.New("Unsafe Minecraft data path")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), "minecraft-data"), nil
}
func openPrivatePluginChild(parent *os.File, name string, create bool) (*os.File, error) {
	if create {
		if err := unix.Mkdirat(int(parent.Fd()), name, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
			return nil, err
		}
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != uint32(os.Geteuid()) || (name == ".justvoxel" && st.Mode&0022 != 0 || name != ".justvoxel" && st.Mode&0777 != 0700) {
		unix.Close(fd)
		return nil, errors.New("Unsafe private plugin directory")
	}
	f := os.NewFile(uintptr(fd), name)
	if create {
		if err := parent.Sync(); err != nil {
			f.Close()
			return nil, err
		}
	}
	return f, nil
}
func openDisabledPluginDirectory(dataPath string, create bool) (*os.File, error) {
	data, err := openPluginDataDirectory(dataPath)
	if err != nil {
		return nil, err
	}
	defer data.Close()
	owned, err := openPrivatePluginChild(data, ".justvoxel", create)
	if err != nil {
		return nil, err
	}
	defer owned.Close()
	return openPrivatePluginChild(owned, "disabled-plugins", create)
}
func pluginFilePresent(root *os.File, name string) (bool, error) {
	if root == nil {
		return false, nil
	}
	var st unix.Stat_t
	err := unix.Fstatat(int(root.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	return err == nil, err
}
func verifyManagedPluginAt(root *os.File, e pluginManifestEntry) error {
	if root == nil || e.Main == "" || managedPluginDescriptor(e.Filename, e.PluginName, e.Main) != "" {
		return errors.New("Plugin ownership identity is incomplete")
	}
	f, err := openPluginAt(root, e.Filename)
	if err != nil {
		return errors.New("Managed plugin JAR is unavailable or unsafe")
	}
	defer f.Close()
	identity, err := inspectStagedPlugin(f, e.Filename)
	if err != nil || identity.Name != e.PluginName || identity.Main != e.Main {
		return errors.New("Managed plugin descriptor differs from ownership manifest")
	}
	var original unix.Stat_t
	if unix.Fstat(int(f.Fd()), &original) != nil {
		return errors.New("Managed plugin identity cannot be verified")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	hash := sha512.New()
	n, err := io.Copy(hash, io.LimitReader(f, pluginMaxJar+1))
	if err != nil || n != original.Size || hex.EncodeToString(hash.Sum(nil)) != e.SHA512 {
		return errors.New("Managed plugin hash differs from ownership manifest")
	}
	var current, opened unix.Stat_t
	if unix.Fstatat(int(root.Fd()), e.Filename, &current, unix.AT_SYMLINK_NOFOLLOW) != nil || unix.Fstat(int(f.Fd()), &opened) != nil || current.Dev != original.Dev || current.Ino != original.Ino || current.Mode&unix.S_IFMT != unix.S_IFREG || current.Size != original.Size || current.Mtim != original.Mtim || current.Ctim != original.Ctim || opened.Mtim != original.Mtim || opened.Ctim != original.Ctim {
		return errors.New("Managed plugin changed while verifying")
	}
	return nil
}
func resolveManagedPluginLocation(path string, e pluginManifestEntry) (string, error) {
	active, err := openPluginDirectory(path)
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return "", errors.New("Active plugin directory is unsafe")
	}
	if active != nil {
		defer active.Close()
	}
	disabled, err := openDisabledPluginDirectory(path, false)
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return "", errors.New("Disabled plugin directory is unsafe")
	}
	if disabled != nil {
		defer disabled.Close()
	}
	a, err := pluginFilePresent(active, e.Filename)
	if err != nil {
		return "", err
	}
	d, err := pluginFilePresent(disabled, e.Filename)
	if err != nil {
		return "", err
	}
	if a == d {
		return "", errors.New("Managed JAR is missing or present in both active and disabled locations")
	}
	if a {
		return "enabled", verifyManagedPluginAt(active, e)
	}
	return "disabled", verifyManagedPluginAt(disabled, e)
}
func fingerprintPluginManagePlan(p pluginManagePlan) string {
	raw, _ := json.Marshal(struct {
		Action, Project, State, Path string
		Entry                        pluginManifestEntry
		Server                       pluginServerContext
		Device, Inode                uint64
		Manifest                     []byte
		Parents                      []pluginLocationEvidence
		Online                       int
		Players                      []string
	}{p.Action, p.ProjectID, p.State, p.DataPath, p.Entry, p.Server, p.DataDevice, p.DataInode, p.Manifest, p.Parents, p.Online, p.Players})
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func resolvePluginManagePlan(ctx context.Context, server pluginServerContext, path, action, project string, sources ...string) (pluginManagePlan, error) {
	ctx = withPluginInstance(ctx, server.InstanceID)
	source := pluginRequestSource(sources)
	p := pluginManagePlan{Source: source, Action: action, ProjectID: project, Server: server, DataPath: path, Players: []string{}, Parents: []pluginLocationEvidence{}}
	if !slices.Contains([]string{"enable", "disable", "remove"}, action) || !validPluginIdentity(source, project) {
		return p, errors.New("Invalid plugin action or project identity")
	}
	if !server.Supported || server.MinecraftVersion == "" {
		return p, errors.New("Current Paper/Purpur server context is unavailable")
	}
	data, err := openPluginDataDirectory(path)
	if err != nil {
		return p, err
	}
	defer data.Close()
	var st unix.Stat_t
	if unix.Fstat(int(data.Fd()), &st) != nil {
		return p, errors.New("Data path identity unavailable")
	}
	p.DataDevice, p.DataInode = uint64(st.Dev), st.Ino
	manifest, raw, err := readPluginManifest(path)
	if err != nil {
		return p, errors.New("Plugin ownership manifest is unsafe")
	}
	p.Manifest = raw
	found := false
	for _, e := range manifest.Artifacts {
		if pluginIdentity(e.Source, e.ProjectID) == pluginIdentity(source, project) {
			p.Entry = e
			found = true
		}
	}
	if !found {
		return p, errors.New("Only exact Plugins-managed entries support this action")
	}
	p.State, err = resolveManagedPluginLocation(path, p.Entry)
	if err != nil {
		return p, err
	}
	p.Name = p.Entry.PluginName
	if action == "enable" && p.State != "disabled" || action == "disable" && p.State != "enabled" {
		return p, errors.New("Plugin state does not allow the requested action")
	}
	for _, parent := range p.Entry.RequiredBy {
		// Orphan relationships may remain after removal of the parent.
		for _, e := range manifest.Artifacts {
			if pluginIdentity(e.Source, e.ProjectID) == pluginParentIdentity(parent) {
				state, eerr := resolveManagedPluginLocation(path, e)
				if eerr != nil {
					return p, errors.New("Required managed parent state cannot be safely verified")
				}
				p.Parents = append(p.Parents, pluginLocationEvidence{e, state})
				if action != "enable" && state == "enabled" {
					return p, fmt.Errorf("%s requires this plugin; disable that managed plugin first", e.PluginName)
				}
			}
		}
	}
	if action == "enable" {
		root, err := openPluginDirectory(path)
		if err != nil {
			return p, err
		}
		err = checkPluginInstallCollisions(root, pluginInstallPlan{Artifacts: []pluginInstallArtifact{{Filename: p.Entry.Filename, PluginName: p.Entry.PluginName, Main: p.Entry.Main}}})
		root.Close()
		if err != nil {
			return p, err
		}
	}
	p.Online, p.Players, err = pluginInstallPlayers(ctx, pluginInstanceID(ctx))
	if err != nil {
		return p, err
	}
	p.ConfirmationRequired = p.Online > 0
	p.RestartRequired = true
	p.Fingerprint = fingerprintPluginManagePlan(p)
	return p, nil
}
func (s *server) resolvePluginManage(ctx context.Context, action, project string, sources ...string) (pluginManagePlan, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/", nil)
	server, path, ok := s.pluginContext(&pluginContextSink{header: make(http.Header)}, req)
	if !ok {
		return pluginManagePlan{}, errors.New("Authoritative server context unavailable")
	}
	return resolvePluginManagePlan(ctx, server, path, action, project, pluginRequestSource(sources))
}
func (s *server) adminPluginManagePlan(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	var req pluginManageRequest
	if !decodePluginInstallJSON(w, r, &req) {
		return
	}
	plan, err := s.resolvePluginManage(r.Context(), req.Action, req.ProjectID, req.Source)
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, plan)
}
func (s *server) adminPluginManage(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	var req pluginManageApplyRequest
	if !decodePluginInstallJSON(w, r, &req) {
		return
	}
	accepted := false
	defer func() {
		if !accepted && s.store != nil && slices.Contains([]string{"enable", "disable", "remove"}, req.Action) && validPluginIdentity(req.Source, req.ProjectID) {
			_ = s.store.recordAuditEvent(actor, "plugin_"+req.Action, req.ProjectID, false, "Managed plugin lifecycle request rejected")
		}
	}()
	if s.operations == nil {
		writeError(w, 503, "Plugin operation tracking is unavailable")
		return
	}
	plan, err := s.resolvePluginManage(r.Context(), req.Action, req.ProjectID, req.Source)
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	if !operationFingerprintPattern.MatchString(req.PlanFingerprint) || plan.Fingerprint != req.PlanFingerprint {
		writeError(w, 409, "Plugin plan changed; review again")
		return
	}
	if plan.Online > 0 && !req.ConfirmPlayers {
		writeError(w, 409, "Online players require interruption confirmation")
		return
	}
	journal, created, err := s.operations.beginMinecraftMaintenance(plan.Fingerprint, "plugin_"+plan.Action, plan.Server.InstanceID)
	if err != nil {
		writeError(w, 409, "Another Minecraft maintenance operation is active")
		return
	}
	accepted = true
	writeJSON(w, 202, map[string]any{"created": created, "operation": journal})
	if created {
		startPluginManageWorker(s, journal.OperationID, plan, req.ConfirmPlayers, actor)
	}
}
func (s *server) adminPluginOperation(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	if s.operations == nil {
		writeError(w, 503, "Plugin operation tracking unavailable")
		return
	}
	journal, err := s.operations.currentPluginOperation()
	if err != nil {
		writeError(w, 500, "Plugin operation unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"operation": journal})
}

var startPluginManageWorker = func(s *server, id string, p pluginManagePlan, confirm bool, actor session) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		_ = executePluginManage(ctx, s, id, p, confirm, actor)
	}()
}

func createPluginManageEvidence(store *operationStore, id string, tx pluginManageTransaction) (*os.File, error) {
	root := filepath.Join(store.baseDir, "plugin-manage")
	if err := ensurePrivateDirectory(root); err != nil {
		return nil, err
	}
	parent, err := openPluginEvidenceDirectory(root)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	dir, err := openPrivatePluginChild(parent, id, true)
	if err != nil {
		return nil, err
	}
	if err = publishPluginEvidence(dir, "transaction.json", tx); err != nil {
		dir.Close()
		return nil, err
	}
	base, err := openPluginEvidenceDirectory(store.baseDir)
	if err != nil {
		dir.Close()
		return nil, err
	}
	defer base.Close()
	if err = base.Sync(); err != nil {
		dir.Close()
		return nil, err
	}
	return dir, nil
}
func openPluginOperationStaging(path, id string, create bool) (*os.File, error) {
	if !validOperationID(id) {
		return nil, errors.New("Invalid staging operation identity")
	}
	data, err := openPluginDataDirectory(path)
	if err != nil {
		return nil, err
	}
	defer data.Close()
	owned, err := openPrivatePluginChild(data, ".justvoxel", create)
	if err != nil {
		return nil, err
	}
	defer owned.Close()
	parent, err := openPrivatePluginChild(owned, "plugin-transactions", create)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	return openPrivatePluginChild(parent, id, create)
}
func checkedPluginMove(from, to *os.File, e pluginManifestEntry) (bool, error) {
	if err := verifyManagedPluginAt(from, e); err != nil {
		return false, err
	}
	present, err := pluginFilePresent(to, e.Filename)
	if err != nil || present {
		return false, errors.New("Plugin move target exists or is unsafe")
	}
	if err := unix.Renameat2(int(from.Fd()), e.Filename, int(to.Fd()), e.Filename, unix.RENAME_NOREPLACE); err != nil {
		return false, err
	}
	return true, errors.Join(from.Sync(), to.Sync())
}
func checkManageDataIdentity(tx pluginManageTransaction) error {
	data, err := openPluginDataDirectory(tx.DataPath)
	if err != nil {
		return err
	}
	defer data.Close()
	var st unix.Stat_t
	if unix.Fstat(int(data.Fd()), &st) != nil || uint64(st.Dev) != tx.DataDevice || st.Ino != tx.DataInode {
		return errors.New("Minecraft data path identity changed")
	}
	return nil
}
func executePluginManage(ctx context.Context, s *server, id string, p pluginManagePlan, confirm bool, actor session) (result error) {
	ctx = withPluginInstance(ctx, p.Server.InstanceID)
	if err := s.operations.requireMaintenanceInstance(id, p.Server.InstanceID); err != nil {
		return err
	}
	success := false
	defer func() {
		if s.store != nil {
			_ = s.store.recordAuditEvent(actor, "plugin_"+p.Action, p.ProjectID, success, "Managed plugin lifecycle")
		}
	}()
	untouched := func(cause error) error {
		_ = s.operations.appendOperationDiagnostic(id, "plugin_"+p.Action, "failure", "Plugin action cancelled before runtime or JAR changes", map[string]string{"reason": cause.Error()})
		journal, err := s.operations.get(id)
		if err != nil {
			return errors.Join(cause, err)
		}
		if journal.State != operationQueued {
			if _, err = s.operations.transition(id, operationFailed, "rollback", "Plugin action cancelled before runtime or JAR changes."); err != nil {
				return errors.Join(cause, err)
			}
			if _, err = s.operations.transition(id, operationRollingBack, "rollback", "No runtime or JAR changes require rollback."); err != nil {
				return errors.Join(cause, err)
			}
		}
		_, err = s.operations.transition(id, operationRolledBack, "complete", "Plugin action cancelled safely before changes.")
		return errors.Join(cause, err)
	}
	lock, err := acquirePluginMaintenanceLock()
	if err != nil {
		return untouched(err)
	}
	defer lock.Close()
	current, err := s.resolvePluginManage(ctx, p.Action, p.ProjectID, p.Source)
	if err != nil || current.Fingerprint != p.Fingerprint {
		return untouched(errors.New("Plugin plan changed; review again"))
	}
	online, _, err := pluginInstallPlayers(ctx, pluginInstanceID(ctx))
	if err != nil || online > 0 && !confirm {
		return untouched(errors.New("Final player check requires review and confirmation"))
	}
	running, err := pluginServiceActive(ctx, pluginInstanceID(ctx))
	if err != nil {
		return untouched(err)
	}
	tx := pluginManageTransaction{p.Server.InstanceID, "v1", id, p.Action, p.Fingerprint, p.DataPath, p.DataDevice, p.DataInode, p.Server.MinecraftVersion, running, p.Entry, p.State, p.Manifest}
	evidence, err := createPluginManageEvidence(s.operations, id, tx)
	if err != nil {
		return untouched(errors.New("Private plugin transaction evidence could not be saved"))
	}
	defer evidence.Close()
	if _, err = s.operations.transition(id, operationValidating, "validating", "Preparing the reviewed plugin action."); err != nil {
		return err
	}
	online, _, err = pluginInstallPlayers(ctx, pluginInstanceID(ctx))
	if err != nil || online > 0 && !confirm {
		return untouched(errors.New("Final player status changed or requires interruption confirmation"))
	}
	rollback := func(cause error) error { return rollbackPluginManage(s, id, tx, cause, lock) }
	if _, err = s.operations.transition(id, operationRunning, "backup", "Creating a verified cold Minecraft backup."); err != nil {
		return untouched(err)
	}
	if err = instanceMaintenanceColdBackup(ctx, p.Server.InstanceID); err != nil {
		return rollback(errors.New("Cold backup failed"))
	}
	if active, err := pluginServiceActive(ctx, pluginInstanceID(ctx)); err != nil || active {
		return rollback(errors.New("Minecraft stopped state could not be proven"))
	}
	if err = checkManageDataIdentity(tx); err != nil {
		return rollback(err)
	}
	state, err := resolveManagedPluginLocation(p.DataPath, p.Entry)
	if err != nil || state != p.State {
		return rollback(errors.New("Managed plugin state changed"))
	}
	active, err := openPluginDirectory(p.DataPath)
	if err != nil {
		return rollback(err)
	}
	defer active.Close()
	disabled, err := openDisabledPluginDirectory(p.DataPath, true)
	if err != nil {
		return rollback(err)
	}
	defer disabled.Close()
	source, target := active, disabled
	if p.State == "disabled" {
		source = disabled
	}
	if p.Action == "enable" {
		target = active
		err = checkPluginInstallCollisions(active, pluginInstallPlan{Artifacts: []pluginInstallArtifact{{Filename: p.Entry.Filename, PluginName: p.Entry.PluginName, Main: p.Entry.Main}}})
		if err != nil {
			return rollback(err)
		}
	}
	if p.Action == "remove" {
		target, err = openPluginOperationStaging(p.DataPath, id, true)
		if err != nil {
			return rollback(err)
		}
		defer target.Close()
	}
	if err = s.operations.pluginProgress(id, "changing", "Moving the exact verified managed JAR; configuration and data remain untouched."); err != nil {
		return rollback(err)
	}
	var staging *os.File
	if p.Action == "remove" {
		staging = target
	}
	if err = checkPinnedManagedPluginDirectories(tx, active, disabled, staging); err != nil {
		return rollback(err)
	}
	if _, err = checkedPluginMove(source, target, p.Entry); err != nil {
		return rollback(errors.New("Atomic plugin move could not be proven"))
	}
	if err = s.operations.pluginProgress(id, "restarting", "Starting Minecraft for runtime verification."); err != nil {
		return rollback(err)
	}
	previousStartup, err := snapshotPluginLifecycleStartup(p.DataPath)
	if err != nil {
		return rollback(err)
	}
	if err = lock.start(ctx, pluginInstanceID(ctx)); err != nil {
		return rollback(errors.New("Minecraft could not start"))
	}
	if _, err = s.operations.transition(id, operationVerifying, "verifying", "Verifying Minecraft and managed plugin state."); err != nil {
		return rollback(err)
	}
	if err = verifyPluginRuntime(ctx, pluginInstanceID(ctx), p.Server.MinecraftVersion); err != nil {
		recordPluginStartupEvidence(s.operations, id, p.DataPath, []string{p.Entry.PluginName})
		return rollback(errors.New("Minecraft stack verification failed"))
	}
	if err = verifyPluginLifecycleStartupFresh(p.DataPath, previousStartup); err != nil {
		recordPluginStartupEvidence(s.operations, id, p.DataPath, []string{p.Entry.PluginName})
		return rollback(err)
	}
	if p.Action == "enable" {
		err = verifyPluginStartup(p.DataPath, []string{p.Entry.PluginName})
	} else {
		err = verifyPluginAbsent(p.DataPath, p.Entry.PluginName)
	}
	if err != nil {
		recordPluginStartupEvidence(s.operations, id, p.DataPath, []string{p.Entry.PluginName})
		return rollback(err)
	}
	if !running {
		if err = pluginService(ctx, pluginInstanceID(ctx), "stop"); err != nil {
			return rollback(err)
		}
		if active, err := pluginServiceActive(ctx, pluginInstanceID(ctx)); err != nil || active {
			return rollback(errors.New("Original stopped state unavailable"))
		}
	}
	if err = checkManageDataIdentity(tx); err != nil {
		return rollback(err)
	}
	if err = checkPinnedManagedPluginDirectories(tx, active, disabled, staging); err != nil {
		return rollback(err)
	}
	if err = verifyManagedPluginAt(target, p.Entry); err != nil {
		return rollback(err)
	}
	manifest, raw, err := readPluginManifest(p.DataPath)
	if err != nil || !bytes.Equal(raw, p.Manifest) {
		return rollback(errors.New("Plugin ownership manifest changed"))
	}
	if p.Action == "remove" {
		manifest.Artifacts = slices.DeleteFunc(manifest.Artifacts, func(e pluginManifestEntry) bool { return reflect.DeepEqual(e, p.Entry) })
		if _, err = commitPluginManifest(p.DataPath, p.Manifest, manifest); err != nil {
			return rollback(errors.New("Plugin ownership commit failed"))
		}
	}
	if err = checkManageDataIdentity(tx); err != nil {
		return rollback(err)
	}
	if _, err = s.operations.transition(id, operationSucceeded, "complete", "Plugin "+p.Action+" completed. Minecraft runtime and exact JAR state verified; configuration and data preserved."); err != nil {
		if current, e := s.operations.get(id); e == nil && current.State == operationSucceeded {
			success = true
			return err
		}
		return rollback(err)
	}
	success = true
	// Retain the rollback copy through the durable success journal.
	if p.Action == "remove" {
		if err = verifyManagedPluginAt(target, p.Entry); err == nil {
			err = unix.Unlinkat(int(target.Fd()), p.Entry.Filename, 0)
		}
		if err == nil {
			err = target.Sync()
		}
		if err != nil {
			_ = s.operations.appendOperationDiagnostic(id, "plugin_remove", "cleanup", "Verified final state kept; private staging cleanup could not be completed.", nil)
		}
	}
	return nil
}
func restorePluginManageFiles(tx pluginManageTransaction) error {
	if err := checkManageDataIdentity(tx); err != nil {
		return err
	}
	manifest, raw, err := readPluginManifest(tx.DataPath)
	if err != nil {
		return err
	}
	var original pluginManifest
	restoreManifest := !bytes.Equal(raw, tx.Manifest)
	if restoreManifest {
		if json.Unmarshal(tx.Manifest, &original) != nil {
			return errors.New("Original manifest evidence is invalid")
		}
		expected := original
		expected.Artifacts = slices.Clone(original.Artifacts)
		expected.Artifacts = slices.DeleteFunc(expected.Artifacts, func(e pluginManifestEntry) bool { return reflect.DeepEqual(e, tx.Entry) })
		if tx.Action != "remove" || !reflect.DeepEqual(manifest, expected) {
			return errors.New("Manifest changed outside this transaction")
		}
	}

	active, err := openPluginDirectory(tx.DataPath)
	if err != nil {
		return err
	}
	defer active.Close()
	disabled, err := openDisabledPluginDirectory(tx.DataPath, true)
	if err != nil {
		return err
	}
	defer disabled.Close()
	staging, err := openPluginOperationStaging(tx.DataPath, tx.OperationID, false)
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return err
	}
	if staging != nil {
		defer staging.Close()
	}
	roots := []*os.File{active, disabled, staging}
	sourceIndex := -1
	for i, root := range roots {
		exists, err := pluginFilePresent(root, tx.Entry.Filename)
		if err != nil {
			return err
		}
		if exists {
			if sourceIndex >= 0 {
				return errors.New("Ambiguous plugin copies; recovery blocked")
			}
			if err = verifyManagedPluginAt(root, tx.Entry); err != nil {
				return err
			}
			sourceIndex = i
		}
	}
	if sourceIndex < 0 {
		return errors.New("Rollback JAR is missing")
	}
	originalIndex := 0
	if tx.OriginalState == "disabled" {
		originalIndex = 1
	}
	if originalIndex != sourceIndex {
		if _, err = checkedPluginMove(roots[sourceIndex], roots[originalIndex], tx.Entry); err != nil {
			return err
		}
	}
	if restoreManifest {
		if _, err = commitPluginManifestContent(tx.DataPath, raw, original, tx.Manifest); err != nil {
			return err
		}
	}
	state, err := resolveManagedPluginLocation(tx.DataPath, tx.Entry)
	if err != nil || state != tx.OriginalState {
		return errors.New("Original JAR location could not be proven")
	}
	return nil
}
func restorePluginManageRuntime(ctx context.Context, tx pluginManageTransaction, lock *pluginMaintenanceLock) error {
	ctx = withPluginInstance(ctx, tx.InstanceID)
	previous, err := snapshotPluginLifecycleStartup(tx.DataPath)
	if err != nil {
		return err
	}
	if err := lock.start(ctx, pluginInstanceID(ctx)); err != nil {
		return err
	}
	if err := verifyPluginRuntime(ctx, pluginInstanceID(ctx), tx.MinecraftVersion); err != nil {
		return err
	}
	if err = verifyPluginLifecycleStartupFresh(tx.DataPath, previous); err != nil {
		return err
	}
	if tx.OriginalState == "enabled" {
		err = verifyPluginStartup(tx.DataPath, []string{tx.Entry.PluginName})
	} else {
		err = verifyPluginAbsent(tx.DataPath, tx.Entry.PluginName)
	}
	if err != nil {
		return err
	}
	if !tx.OriginallyRunning {
		if err = pluginService(ctx, pluginInstanceID(ctx), "stop"); err != nil {
			return err
		}
		if active, err := pluginServiceActive(ctx, pluginInstanceID(ctx)); err != nil || active {
			return errors.New("Stopped runtime could not be proven")
		}
	}
	return nil
}
func rollbackPluginManage(s *server, id string, tx pluginManageTransaction, cause error, lock *pluginMaintenanceLock) error {
	_ = s.operations.appendOperationDiagnostic(id, "plugin_"+tx.Action, "failure", "Managed plugin action failed", map[string]string{"reason": cause.Error()})
	if !lock.protected() {
		_, err := s.operations.transition(id, operationNeedsAttention, "rollback", "Minecraft maintenance ownership unavailable. Durable evidence is preserved.")
		return errors.Join(cause, err, errors.New("Minecraft maintenance ownership unavailable"))
	}
	_, _ = s.operations.transition(id, operationFailed, "rollback", "Plugin action failed; restoring the exact original state.")
	_, _ = s.operations.transition(id, operationRollingBack, "rollback", "Restoring verified JAR location, ownership and Minecraft state.")
	ctx, cancel := context.WithTimeout(withPluginInstance(context.Background(), tx.InstanceID), 20*time.Minute)
	defer cancel()
	err := pluginService(ctx, pluginInstanceID(ctx), "stop")
	if err == nil {
		if active, e := pluginServiceActive(ctx, pluginInstanceID(ctx)); e != nil || active {
			err = errors.New("Failed runtime could not be stopped")
		}
	}
	if err == nil {
		err = restorePluginManageFiles(tx)
	}
	if err == nil {
		err = restorePluginManageRuntime(ctx, tx, lock)
	}
	if err != nil {
		_, _ = s.operations.transition(id, operationNeedsAttention, "rollback", "Plugin recovery could not be proven. Durable evidence is preserved. See System -> Logs -> Plugins.")
		return errors.Join(cause, err)
	}
	_, err = s.operations.transition(id, operationRolledBack, "complete", "Plugin action did not complete. Exact original JAR, ownership and Minecraft runtime state restored. See System -> Logs -> Plugins.")
	return errors.Join(cause, err)
}
func readPluginManageTransaction(store *operationStore, id string) (pluginManageTransaction, error) {
	var tx pluginManageTransaction
	parent, err := openPluginEvidenceDirectory(filepath.Join(store.baseDir, "plugin-manage"))
	if err != nil {
		return tx, err
	}
	defer parent.Close()
	dir, err := openPrivatePluginChild(parent, id, false)
	if err != nil {
		return tx, err
	}
	defer dir.Close()
	fd, err := unix.Openat(int(dir.Fd()), "transaction.json", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return tx, err
	}
	f := os.NewFile(uintptr(fd), "transaction.json")
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Geteuid()) || st.Mode&0777 != 0600 || st.Size > 2*pluginManifestLimit {
		return tx, errors.New("Unsafe transaction evidence")
	}
	d := json.NewDecoder(io.LimitReader(f, 2*pluginManifestLimit+1))
	d.DisallowUnknownFields()
	if d.Decode(&tx) != nil || d.Decode(new(any)) != io.EOF || tx.SchemaVersion != "v1" || validatePluginInstance(tx.InstanceID, tx.DataPath) != nil || tx.OperationID != id || !operationFingerprintPattern.MatchString(tx.PlanFingerprint) || !slices.Contains([]string{"enable", "disable", "remove"}, tx.Action) || !slices.Contains([]string{"enabled", "disabled"}, tx.OriginalState) {
		return tx, errors.New("Invalid transaction evidence")
	}
	if tx.Action == "enable" && tx.OriginalState != "disabled" || tx.Action == "disable" && tx.OriginalState != "enabled" || !versionInputPattern.MatchString(tx.MinecraftVersion) || !validPluginIdentity(tx.Entry.Source, tx.Entry.ProjectID) || !validPluginIdentity(tx.Entry.Source, tx.Entry.VersionID) {
		return tx, errors.New("Invalid lifecycle transaction relationship")
	}
	var manifest pluginManifest
	if json.Unmarshal(tx.Manifest, &manifest) != nil || validatePluginManifest(manifest) != nil || !slices.ContainsFunc(manifest.Artifacts, func(e pluginManifestEntry) bool { return reflect.DeepEqual(e, tx.Entry) }) || !safePluginFilename(tx.Entry.Filename) || !pluginHashPattern.MatchString(tx.Entry.SHA512) || !pluginNamePattern.MatchString(tx.Entry.PluginName) || !pluginMainPattern.MatchString(tx.Entry.Main) || managedPluginDescriptor(tx.Entry.Filename, tx.Entry.PluginName, tx.Entry.Main) != "" {
		return tx, errors.New("Invalid original ownership evidence")
	}
	return tx, nil
}
func (s *server) adminPluginManageAcknowledge(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	var req pluginManageAcknowledgeRequest
	if !decodePluginInstallJSON(w, r, &req) {
		return
	}
	if s.operations == nil || !validOperationID(req.OperationID) {
		writeError(w, 400, "Invalid plugin operation identity")
		return
	}
	journal, err := s.operations.currentPluginOperation()
	if err != nil || journal == nil || journal.OperationID != req.OperationID || journal.OperationType == operationTypePluginInstall || journal.State != operationNeedsAttention {
		writeError(w, 409, "Plugin management operation is not awaiting recovery")
		return
	}
	lock, err := acquirePluginMaintenanceLock()
	if err != nil {
		writeError(w, 409, "Minecraft maintenance is busy")
		return
	}
	defer lock.Close()
	tx, err := readPluginManageTransaction(s.operations, req.OperationID)
	if err != nil && journal.Stage == "interrupted_queued" && (errors.Is(err, unix.ENOENT) || errors.Is(err, os.ErrNotExist)) {
		// Queued lifecycle operations cannot have changed runtime or files: the
		// durable transaction must precede their first non-queued transition.
		active, stateErr := pluginServiceActive(r.Context(), pluginInstanceID(r.Context()))
		if stateErr == nil && active {
			server, _, ok := s.pluginContext(&pluginContextSink{header: make(http.Header)}, r)
			if !ok || server.MinecraftVersion == "" {
				stateErr = errors.New("Server context unavailable")
			} else {
				stateErr = verifyPluginRuntime(r.Context(), pluginInstanceID(r.Context()), server.MinecraftVersion)
			}
		}
		if stateErr != nil {
			writeError(w, 409, "Unchanged runtime cannot yet be verified")
			return
		}
		resolved, e := s.operations.transition(req.OperationID, operationResolved, "resolved", "Queued plugin action interrupted before runtime or file changes; unchanged runtime verified.")
		if e != nil {
			writeError(w, 500, "Recovery result could not be persisted")
			return
		}
		writeJSON(w, 200, map[string]any{"operation": resolved})
		return
	}
	if err != nil || journal.OperationType != "plugin_"+tx.Action || journal.PlanFingerprint != tx.PlanFingerprint || journal.InstanceID != tx.InstanceID {
		writeError(w, 409, "Exact recovery evidence is unavailable; maintenance remains reserved")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Minute)
	defer cancel()
	if pluginManageTargetState(tx) == nil {
		if err = verifyPluginManageTargetRuntime(ctx, tx, lock); err == nil {
			resolved, e := s.operations.transition(req.OperationID, operationResolved, "resolved", "Complete target plugin ownership, JAR location and runtime state verified.")
			if e != nil {
				writeError(w, 500, "Recovery result could not be persisted")
				return
			}
			if tx.Action == "remove" {
				if stage, e := openPluginOperationStaging(tx.DataPath, tx.OperationID, false); e == nil {
					if verifyManagedPluginAt(stage, tx.Entry) == nil {
						_ = unix.Unlinkat(int(stage.Fd()), tx.Entry.Filename, 0)
						_ = stage.Sync()
					}
					stage.Close()
				}
			}
			if s.store != nil {
				_ = s.store.recordAuditEvent(actor, "plugin_"+tx.Action, tx.Entry.ProjectID, true, "Managed plugin target state recovered")
			}
			writeJSON(w, 200, map[string]any{"operation": resolved})
			return
		}
	}
	if !lock.protected() {
		writeError(w, 409, "Minecraft maintenance could not be reacquired; recovery evidence is preserved")
		return
	}
	// Bounded deterministic rollback is safe only with one exact rollback copy
	// and either the original or transaction-specific committed manifest.
	if err = pluginService(ctx, pluginInstanceID(ctx), "stop"); err == nil {
		if active, e := pluginServiceActive(ctx, pluginInstanceID(ctx)); e != nil || active {
			err = errors.New("Stopped runtime unavailable")
		}
	}
	if err == nil {
		err = restorePluginManageFiles(tx)
	}
	if err == nil {
		err = restorePluginManageRuntime(ctx, tx, lock)
	}
	if err != nil {
		writeError(w, 409, "Plugin recovery cannot yet be proven. See System -> Logs -> Plugins. Evidence and maintenance exclusion remain preserved.")
		return
	}
	resolved, err := s.operations.transition(req.OperationID, operationResolved, "resolved", "Exact original plugin ownership, JAR location and runtime state verified.")
	if err != nil {
		writeError(w, 500, "Recovery result could not be persisted")
		return
	}
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "plugin_"+tx.Action, tx.Entry.ProjectID, false, "Managed plugin original state recovered")
	}
	writeJSON(w, 200, map[string]any{"operation": resolved})
}

// Diagnostic evidence uses only the verifier's bounded, final initialization
// segment. No raw log, URL, connection string or stack frame is returned.
func readPluginStartupEvidence(path string) ([]byte, error) {
	fd, err := openPluginStartupLogs(path)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	logFD, err := unix.Openat(fd, "latest.log", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(logFD), "latest.log")
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(logFD, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Size > pluginStartupLogLimit {
		return nil, errors.New("Unsafe startup log")
	}
	raw, err := io.ReadAll(io.LimitReader(f, pluginStartupLogLimit+1))
	if err != nil || len(raw) > pluginStartupLogLimit || !utf8.Valid(raw) {
		return nil, errors.New("Invalid startup log")
	}
	for _, c := range raw {
		if c < 32 && c != '\n' && c != '\r' && c != '\t' || c == 127 {
			return nil, errors.New("Corrupt startup log")
		}
	}
	currentFD, err := openPluginStartupLogs(path)
	if err != nil {
		return nil, err
	}
	defer unix.Close(currentFD)
	var directory, currentDirectory, current, opened unix.Stat_t
	if unix.Fstat(fd, &directory) != nil || unix.Fstat(currentFD, &currentDirectory) != nil || directory.Dev != currentDirectory.Dev || directory.Ino != currentDirectory.Ino || unix.Fstatat(fd, "latest.log", &current, unix.AT_SYMLINK_NOFOLLOW) != nil || unix.Fstat(logFD, &opened) != nil {
		return nil, errors.New("Startup log identity changed")
	}
	if len(raw) > 0 {
		if !pluginStartupSnapshotStable(st, current, opened, int64(len(raw))) {
			return nil, errors.New("Startup log snapshot changed")
		}
	} else if current.Ino != st.Ino || current.Dev != st.Dev || current.Size != 0 {
		return nil, errors.New("Startup log snapshot changed")
	}
	return raw, nil
}

var pluginDiagnosticSensitivePattern = regexp.MustCompile(`(?i)password|secret|token|cookie|authorization|credential|api[ _-]*key`)

func pluginStartupDiagnosticExcerpt(raw []byte, expected []string, hostname string) []string {
	for _, c := range raw {
		if c < 32 && c != '\n' && c != '\r' && c != '\t' || c == 127 {
			return nil
		}
	}
	if len(raw) > pluginStartupLogLimit || !utf8.Valid(raw) {
		return nil
	}
	lines := []string{}
	started := false
	total := 0
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), pluginStartupLineLimit)
	for scanner.Scan() {
		line := strings.TrimSpace(pluginStartupPrefix.ReplaceAllString(scanner.Text(), ""))
		if line == "[PluginInitializerManager] Initializing plugins..." {
			lines = nil
			total = 0
			started = true
			continue
		}
		if !started {
			continue
		}
		for _, c := range line {
			if c < 32 || c == 127 {
				return nil
			}
		}
		relevant := false
		for _, name := range expected {
			if !pluginNamePattern.MatchString(name) {
				return nil
			}
			if strings.HasPrefix(line, "["+name+"] ") {
				relevant = true
			}
			for _, phrase := range []string{"Enabling ", "Disabling ", "Loading ", "Error occurred while enabling ", "Error occurred while loading ", "Could not load plugin ", "Could not enable plugin ", "Failed to load plugin ", "Failed to enable plugin "} {
				if at := strings.Index(strings.ToLower(line), strings.ToLower(phrase)); at >= 0 {
					tail := strings.TrimLeft(line[at+len(phrase):], "'\"")
					if tail == name || strings.HasPrefix(tail, name+" ") || strings.HasPrefix(tail, name+"'") || strings.HasPrefix(tail, name+"\"") || strings.HasPrefix(tail, name+":") {
						relevant = true
					}
				}
			}
		}
		if !relevant {
			continue
		}
		if pluginDiagnosticSensitivePattern.MatchString(line) {
			line = "<sensitive plugin diagnostic detail redacted>"
		} else {
			if strings.ContainsAny(line, "@\\") || strings.Contains(line, "//") || strings.Contains(strings.ToLower(line), "jdbc:") || (strings.Contains(line, " at ") && strings.Contains(line, "(") && strings.Contains(line, ")")) || strings.Contains(line, ".java:") || strings.Contains(line, "Exception:") || strings.Contains(line, "Caused by:") || strings.Contains(line, "=") {
				continue
			}
			line = sanitizeSetupDiagnosticText(line, hostname)
		}
		if len(line) > 2048 {
			line = line[:2048]
		}
		// Keep the bounded tail so the final reason for a plugin disabling
		// itself survives a long sequence of earlier logger messages.
		lines = append(lines, line)
		total += len(strconv.Quote(line)) + 256
		for len(lines) > 64 || total > (16<<10)-512 {
			total -= len(strconv.Quote(lines[0])) + 256
			lines = lines[1:]
		}
	}
	if scanner.Err() != nil {
		return nil
	}
	return lines
}
func recordPluginStartupEvidence(store *operationStore, id, path string, names []string) {
	journal, err := store.get(id)
	if err != nil || !isPluginOperationType(journal.OperationType) {
		return
	}
	raw, err := readPluginStartupEvidence(path)
	if err != nil {
		return
	}
	budget := (16 << 10) - 512
	for _, line := range pluginStartupDiagnosticExcerpt(raw, names, store.diagnosticHostname) {
		// Include timestamp, framing and quoted sanitation expansion in the evidence
		// budget, rather than counting only the original Minecraft message bytes.
		sanitized := sanitizeSetupDiagnosticValue("detail", line, store.diagnosticHostname)
		cost := len(strconv.Quote(sanitized)) + 256
		if cost > budget {
			break
		}
		budget -= cost
		_ = store.appendOperationDiagnostic(id, journal.OperationType, "startup_evidence", "Plugin startup verification evidence", map[string]string{"detail": line})
	}
}
func verifyPluginAbsent(path, name string) error {
	raw, err := readPluginStartupEvidence(path)
	if err != nil {
		return err
	}
	if !pluginStartupNameAbsent(raw, name) {
		return errors.New("Plugin absence cannot be proven from the final startup inventory")
	}
	return nil
}
func pluginStartupNameAbsent(raw []byte, name string) bool {
	if !pluginNamePattern.MatchString(name) || len(raw) == 0 || len(raw) > pluginStartupLogLimit || raw[len(raw)-1] != '\n' || !utf8.Valid(raw) {
		return false
	}
	started, valid, list := false, true, false
	remaining := 0
	groups := map[string]bool{}
	inventory := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), pluginStartupLineLimit)
	for scanner.Scan() {
		line := strings.TrimSpace(pluginStartupPrefix.ReplaceAllString(scanner.Text(), ""))
		if line == "[PluginInitializerManager] Initializing plugins..." {
			started = true
			valid = true
			list = false
			remaining = 0
			groups = map[string]bool{}
			inventory = map[string]bool{}
			continue
		}
		if !started {
			continue
		}
		for _, c := range line {
			if c < 32 || c == 127 {
				valid = false
			}
		}
		if strings.HasPrefix(line, "[PluginInitializerManager] Paper plugins") || strings.HasPrefix(line, "[PluginInitializerManager] Bukkit plugins") {
			m := pluginStartupInventory.FindStringSubmatch(line)
			if m == nil || remaining != 0 {
				valid = false
				continue
			}
			count, err := strconv.Atoi(m[2])
			if err != nil || count > 4096 || groups[m[1]] {
				valid = false
				continue
			}
			groups[m[1]] = true
			remaining = count
			list = true
			continue
		}
		if remaining > 0 {
			if !strings.HasPrefix(line, "- ") {
				valid = false
				remaining = 0
			} else {
				entries, ok := splitPluginStartupEntries(strings.TrimPrefix(line, "- "))
				if !ok {
					valid = false
				}
				for _, entry := range entries {
					m := pluginStartupEntry.FindStringSubmatch(entry)
					if m == nil || remaining <= 0 {
						valid = false
						continue
					}
					if inventory[m[1]] || m[1] == name {
						valid = false
					}
					inventory[m[1]] = true
					remaining--
				}
				continue
			}
		} else if list && strings.HasPrefix(line, "- ") {
			valid = false
		} else {
			list = false
		}
		if strings.HasPrefix(line, "["+name+"] Enabling ") {
			valid = false
		}
	}
	return scanner.Err() == nil && started && valid && remaining == 0 && len(groups) > 0 && !inventory[name]
}

func pluginManageTargetState(tx pluginManageTransaction) error {
	if err := checkManageDataIdentity(tx); err != nil {
		return err
	}
	manifest, raw, err := readPluginManifest(tx.DataPath)
	if err != nil {
		return err
	}
	if tx.Action != "remove" {
		if !bytes.Equal(raw, tx.Manifest) {
			return errors.New("Ownership changed")
		}
		state, err := resolveManagedPluginLocation(tx.DataPath, tx.Entry)
		if err != nil {
			return err
		}
		wanted := "disabled"
		if tx.Action == "enable" {
			wanted = "enabled"
		}
		if state != wanted {
			return errors.New("Target state not complete")
		}
		return nil
	}
	var old pluginManifest
	if json.Unmarshal(tx.Manifest, &old) != nil {
		return errors.New("Invalid original manifest")
	}
	old.Artifacts = slices.DeleteFunc(old.Artifacts, func(e pluginManifestEntry) bool { return reflect.DeepEqual(e, tx.Entry) })
	if !reflect.DeepEqual(manifest, old) {
		return errors.New("Removal ownership commit not proven")
	}
	for _, state := range []string{"enabled", "disabled"} {
		var root *os.File
		if state == "enabled" {
			root, err = openPluginDirectory(tx.DataPath)
		} else {
			root, err = openDisabledPluginDirectory(tx.DataPath, false)
		}
		if err != nil && !errors.Is(err, unix.ENOENT) {
			return err
		}
		exists, e := pluginFilePresent(root, tx.Entry.Filename)
		if root != nil {
			root.Close()
		}
		if e != nil || exists {
			return errors.New("Removal has unexpected active or disabled JAR")
		}
	}
	staging, err := openPluginOperationStaging(tx.DataPath, tx.OperationID, false)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	defer staging.Close()
	exists, err := pluginFilePresent(staging, tx.Entry.Filename)
	if err != nil {
		return err
	}
	if exists {
		return verifyManagedPluginAt(staging, tx.Entry)
	}
	return nil
}
func verifyPluginManageTargetRuntime(ctx context.Context, tx pluginManageTransaction, lock *pluginMaintenanceLock) error {
	ctx = withPluginInstance(ctx, tx.InstanceID)
	if err := pluginService(ctx, pluginInstanceID(ctx), "stop"); err != nil {
		return err
	}
	if active, err := pluginServiceActive(ctx, pluginInstanceID(ctx)); err != nil || active {
		return errors.New("Recovery stopped state unverified")
	}
	previous, err := snapshotPluginLifecycleStartup(tx.DataPath)
	if err != nil {
		return err
	}
	if err := lock.start(ctx, pluginInstanceID(ctx)); err != nil {
		return err
	}
	if err := verifyPluginRuntime(ctx, pluginInstanceID(ctx), tx.MinecraftVersion); err != nil {
		return err
	}
	if err = verifyPluginLifecycleStartupFresh(tx.DataPath, previous); err != nil {
		return err
	}
	if tx.Action == "enable" {
		err = verifyPluginStartup(tx.DataPath, []string{tx.Entry.PluginName})
	} else {
		err = verifyPluginAbsent(tx.DataPath, tx.Entry.PluginName)
	}
	if err != nil {
		return err
	}
	if !tx.OriginallyRunning {
		if err = pluginService(ctx, pluginInstanceID(ctx), "stop"); err != nil {
			return err
		}
		if active, err := pluginServiceActive(ctx, pluginInstanceID(ctx)); err != nil || active {
			return errors.New("Original stopped state unverified")
		}
	}
	return pluginManageTargetState(tx)
}

// Lifecycle absence/activation must belong to a new startup, not a retained
// initialization segment from the service's previous run.
type pluginLifecycleStartupSnapshot struct {
	Device   uint64
	Inode    uint64
	Size     int64
	Modified unix.Timespec
	Changed  unix.Timespec
	Hash     [32]byte
	Markers  int
}

func snapshotPluginLifecycleStartup(path string) (*pluginLifecycleStartupSnapshot, error) {
	fd, err := openPluginStartupLogs(path)
	if errors.Is(err, unix.ENOENT) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	logFD, err := unix.Openat(fd, "latest.log", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer unix.Close(logFD)
	var st unix.Stat_t
	if unix.Fstat(logFD, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Size > pluginStartupLogLimit {
		return nil, errors.New("Unsafe lifecycle startup log")
	}
	raw, err := readPluginStartupEvidence(path)
	if err != nil {
		return nil, err
	}
	markers := countPluginStartupMarkers(raw)
	return &pluginLifecycleStartupSnapshot{Device: uint64(st.Dev), Inode: st.Ino, Size: int64(len(raw)), Modified: st.Mtim, Changed: st.Ctim, Hash: sha256.Sum256(raw), Markers: markers}, nil
}
func verifyPluginLifecycleStartupFresh(path string, previous *pluginLifecycleStartupSnapshot) error {
	current, err := snapshotPluginLifecycleStartup(path)
	if err != nil || current == nil {
		return errors.New("New Minecraft startup log is unavailable")
	}
	if previous != nil {
		if reflect.DeepEqual(current, previous) {
			return errors.New("No new Minecraft startup segment was recorded")
		}
		raw, err := readPluginStartupEvidence(path)
		if err != nil {
			return err
		}
		if current.Device == previous.Device && current.Inode == previous.Inode && int64(len(raw)) >= previous.Size && sha256.Sum256(raw[:previous.Size]) == previous.Hash && current.Markers <= previous.Markers {
			return errors.New("Only the previous Minecraft startup segment is available")
		}
	}
	if current.Markers == 0 {
		return errors.New("No final Minecraft initialization segment is available")
	}
	return nil
}
func checkPinnedManagedPluginDirectories(tx pluginManageTransaction, active, disabled, staging *os.File) error {
	if err := checkManageDataIdentity(tx); err != nil {
		return err
	}
	for _, pair := range []struct {
		kind string
		root *os.File
	}{{"enabled", active}, {"disabled", disabled}, {"staging", staging}} {
		if pair.root == nil {
			continue
		}
		var current *os.File
		var err error
		switch pair.kind {
		case "enabled":
			current, err = openPluginDirectory(tx.DataPath)
		case "disabled":
			current, err = openDisabledPluginDirectory(tx.DataPath, false)
		case "staging":
			current, err = openPluginOperationStaging(tx.DataPath, tx.OperationID, false)
		}
		if err != nil {
			return errors.New("Pinned plugin directory changed")
		}
		before, e := pair.root.Stat()
		after, e2 := current.Stat()
		current.Close()
		if e != nil || e2 != nil || !os.SameFile(before, after) {
			return errors.New("Pinned plugin directory identity changed")
		}
	}
	return nil
}

func countPluginStartupMarkers(raw []byte) int {
	count := 0
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), pluginStartupLineLimit)
	for scanner.Scan() {
		if strings.TrimSpace(pluginStartupPrefix.ReplaceAllString(scanner.Text(), "")) == "[PluginInitializerManager] Initializing plugins..." {
			count++
		}
	}
	if scanner.Err() != nil {
		return 0
	}
	return count
}
