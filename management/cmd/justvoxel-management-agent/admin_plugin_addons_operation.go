package main

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Evidence precedes backup/stop and atomic publication. The inode of our
// exclusive temporary file proves which final file recovery may remove.
type addonTransaction struct {
	InstanceID        string     `json:"instance_id"`
	SchemaVersion     string     `json:"schema_version"`
	OperationID       string     `json:"operation_id"`
	Fingerprint       string     `json:"plan_fingerprint"`
	DataPath          string     `json:"data_path"`
	Version           string     `json:"minecraft_version"`
	Entry             addonEntry `json:"entry"`
	RestartRequired   bool       `json:"restart_required"`
	OriginallyRunning bool       `json:"originally_running"`
	Manifest          []byte     `json:"original_manifest"`
	ParentDevice      uint64     `json:"parent_device"`
	ParentInode       uint64     `json:"parent_inode"`
	FileDevice        uint64     `json:"file_device"`
	FileInode         uint64     `json:"file_inode"`
	Temp              string     `json:"temp_filename"`
}

func commitAddonManifest(data string, previous []byte, m addonManifest) (bool, error) {
	if err := validateAddonManifest(m); err != nil {
		return false, err
	}
	_, raw, err := loadAddonManifest(data)
	if err != nil || !bytes.Equal(raw, previous) {
		return false, errors.New("Add-ons ownership changed")
	}
	root, err := openAddonState(data, true)
	if err != nil {
		return false, err
	}
	defer root.Close()
	if _, _, err = readAddonManifestAt(root, addonManifest{"v1", []addonEntry{}}); err != nil {
		return false, err
	}
	id, err := newOperationID()
	if err != nil {
		return false, err
	}
	temp := ".addons-" + id + ".tmp"
	fd, err := unix.Openat(int(root.Fd()), temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return false, err
	}
	f := os.NewFile(uintptr(fd), temp)
	defer unix.Unlinkat(int(root.Fd()), temp, 0)
	raw, err = json.Marshal(m)
	if err == nil && len(raw) > pluginManifestLimit {
		err = errors.New("Add-ons manifest exceeds limit")
	}
	if err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return false, err
	}
	current, err := openAddonState(data, false)
	if err != nil {
		return false, err
	}
	before, e := root.Stat()
	after, e2 := current.Stat()
	current.Close()
	if e != nil || e2 != nil || !os.SameFile(before, after) {
		return false, errors.New("Ownership directory changed")
	}
	_, raw, err = loadAddonManifest(data)
	if err != nil || !bytes.Equal(raw, previous) {
		return false, errors.New("Add-ons ownership changed")
	}
	if err = unix.Renameat(int(root.Fd()), temp, int(root.Fd()), addonManifestName); err != nil {
		return false, err
	}
	return true, root.Sync()
}
func addonPinnedParent(tx addonTransaction) (*os.File, error) {
	root, err := openAddonDirectory(tx.DataPath, filepath.Dir(tx.Entry.Target), false)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if unix.Fstat(int(root.Fd()), &st) != nil || uint64(st.Dev) != tx.ParentDevice || st.Ino != tx.ParentInode {
		root.Close()
		return nil, errors.New("Add-ons parent identity changed")
	}
	return root, nil
}
func addonTransactionFile(root *os.File, name string, tx addonTransaction) (bool, error) {
	var st unix.Stat_t
	err := unix.Fstatat(int(root.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || uint64(st.Dev) != tx.FileDevice || st.Ino != tx.FileInode {
		return true, errors.New("Transaction file identity changed")
	}
	fd, err := unix.Openat(int(root.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return true, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var opened unix.Stat_t
	if unix.Fstat(fd, &opened) != nil || opened.Dev != st.Dev || opened.Ino != st.Ino {
		return true, errors.New("File identity changed")
	}
	h := sha512.New()
	n, err := io.Copy(h, io.LimitReader(f, pluginMaxJar+1))
	if err != nil || n > pluginMaxJar || hex.EncodeToString(h.Sum(nil)) != tx.Entry.SHA512 {
		return true, errors.New("Transaction file hash changed")
	}
	return true, nil
}
func restoreAddonRuntime(ctx context.Context, tx addonTransaction) error {
	ctx = withPluginInstance(ctx, tx.InstanceID)
	if !tx.RestartRequired {
		return nil
	}
	active, err := pluginServiceActive(ctx, pluginInstanceID(ctx))
	if err != nil {
		return err
	}
	if tx.OriginallyRunning {
		if !active {
			if err = pluginService(ctx, pluginInstanceID(ctx), "start"); err != nil {
				return err
			}
		}
		return verifyPluginRuntime(ctx, pluginInstanceID(ctx), tx.Version)
	}
	if active {
		if err = pluginService(ctx, pluginInstanceID(ctx), "stop"); err != nil {
			return err
		}
	}
	active, err = pluginServiceActive(ctx, pluginInstanceID(ctx))
	if err != nil || active {
		return errors.New("Original stopped state unverified")
	}
	return nil
}
func rollbackAddon(ctx context.Context, tx addonTransaction) error {
	ctx = withPluginInstance(ctx, tx.InstanceID)
	_, raw, err := loadAddonManifest(tx.DataPath)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, tx.Manifest) {
		return errors.New("Ownership commit requires target verification; rollback will not guess")
	}
	if tx.RestartRequired {
		if err = pluginService(ctx, pluginInstanceID(ctx), "stop"); err != nil {
			return err
		}
		active, e := pluginServiceActive(ctx, pluginInstanceID(ctx))
		if e != nil || active {
			return errors.New("Stopped runtime unverified")
		}
	}
	root, err := addonPinnedParent(tx)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, name := range []string{tx.Entry.Filename, tx.Temp} {
		exists, e := addonTransactionFile(root, name, tx)
		if e != nil {
			return e
		}
		if exists {
			if err = unix.Unlinkat(int(root.Fd()), name, 0); err != nil {
				return err
			}
		}
	}
	if err = root.Sync(); err != nil {
		return err
	}
	return restoreAddonRuntime(ctx, tx)
}
func (s *server) revalidateAddon(ctx context.Context, p addonPlan, file *os.File) error {
	ctx = withPluginInstance(ctx, p.Server.InstanceID)
	r, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost/", nil)
	sc, data, ok := s.pluginContext(&pluginContextSink{header: make(http.Header)}, r)
	if !ok {
		return errors.New("Server context unavailable")
	}
	current, err := resolveAddonPlan(sc, data, addonUpload{Kind: p.Kind, Filename: p.Filename, File: file})
	if err != nil || current.Fingerprint != p.Fingerprint {
		return errors.New("Add-ons plan changed; review again")
	}
	return nil
}
func (s *server) checkAddonEnvironment(ctx context.Context, p addonPlan) error {
	ctx = withPluginInstance(ctx, p.Server.InstanceID)
	r, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost/", nil)
	current, data, ok := s.pluginContext(&pluginContextSink{header: make(http.Header)}, r)
	if !ok || data != p.DataPath || current != p.Server {
		return errors.New("Server context changed during Add-ons installation")
	}
	parents, err := addonDirectoryEvidence(data, p.Destination)
	if err != nil {
		return err
	}
	expected := strings.TrimSuffix(p.Environment, "/missing")
	if p.Environment == expected {
		if parents != expected {
			return errors.New("Existing Add-ons parent identity changed")
		}
	} else if parents != expected && !strings.HasPrefix(parents, expected+"/") {
		return errors.New("Existing Add-ons parent identity changed")
	}
	return nil
}
func executeAddonInstall(s *server, id string, p addonPlan, file *os.File, confirm bool, actor session) {
	ctx, cancel := context.WithTimeout(withPluginInstance(context.Background(), p.Server.InstanceID), 20*time.Minute)
	defer cancel()
	if err := s.operations.requireMaintenanceInstance(id, p.Server.InstanceID); err != nil {
		return
	}
	success := false
	defer func() {
		if s.store != nil {
			_ = s.store.recordAuditEvent(actor, operationTypePluginAddonInstall, p.Kind, success, "Managed local Add-ons installation")
		}
	}()
	failUntouched := func(err error) {
		_, _ = s.operations.transition(id, operationRolledBack, "complete", "Add-ons installation cancelled before content or runtime changes: "+err.Error())
	}
	lock, err := acquirePluginRuntimeLock()
	if err != nil {
		failUntouched(err)
		return
	}
	defer lock.Close()
	if err = s.revalidateAddon(ctx, p, file); err != nil {
		failUntouched(err)
		return
	}
	m, raw, err := loadAddonManifest(p.DataPath)
	if err != nil {
		failUntouched(err)
		return
	}
	if p.RestartRequired {
		online, _, e := pluginInstallPlayers(ctx, pluginInstanceID(ctx))
		if e != nil || online > 0 && !confirm {
			failUntouched(errors.New("Player status needs review and confirmation"))
			return
		}
	}
	originallyRunning := false
	if p.RestartRequired {
		originallyRunning, err = pluginServiceActive(ctx, pluginInstanceID(ctx))
		if err != nil {
			failUntouched(err)
			return
		}
	}
	root, err := openAddonDirectory(p.DataPath, p.Destination, true)
	if err != nil {
		failUntouched(err)
		return
	}
	defer root.Close()
	var parent unix.Stat_t
	if unix.Fstat(int(root.Fd()), &parent) != nil {
		failUntouched(errors.New("Destination identity unavailable"))
		return
	}
	temp := ".addon-" + id + ".tmp"
	fd, err := unix.Openat(int(root.Fd()), temp, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		failUntouched(err)
		return
	}
	staged := os.NewFile(uintptr(fd), temp)
	_, err = file.Seek(0, io.SeekStart)
	if err != nil {
		staged.Close()
		unix.Unlinkat(int(root.Fd()), temp, 0)
		failUntouched(err)
		return
	}
	h := sha512.New()
	n, err := io.Copy(io.MultiWriter(staged, h), io.LimitReader(file, p.Size+1))
	if err == nil && (n != p.Size || hex.EncodeToString(h.Sum(nil)) != p.SHA512) {
		err = errors.New("Upload changed")
	}
	if err == nil {
		err = unix.Fchown(fd, int(parent.Uid), int(parent.Gid))
	}
	if err == nil {
		err = unix.Fchmod(fd, 0644)
	}
	if err == nil {
		err = staged.Sync()
	}
	var fst unix.Stat_t
	if err == nil {
		err = unix.Fstat(fd, &fst)
	}
	err = errors.Join(err, staged.Close())
	if err != nil {
		unix.Unlinkat(int(root.Fd()), temp, 0)
		failUntouched(err)
		return
	}
	tx := addonTransaction{p.Server.InstanceID, "v1", id, p.Fingerprint, p.DataPath, p.Server.MinecraftVersion, p.addonEntry, p.RestartRequired, originallyRunning, raw, uint64(parent.Dev), parent.Ino, uint64(fst.Dev), fst.Ino, temp}
	evidenceBase := filepath.Join(s.operations.baseDir, "plugin-addons")
	if err = ensurePrivateDirectory(evidenceBase); err == nil {
		err = os.Mkdir(filepath.Join(evidenceBase, id), 0700)
	}
	var evidence *os.File
	if err == nil {
		evidence, err = openPluginEvidenceDirectory(filepath.Join(evidenceBase, id))
	}
	if err == nil {
		err = root.Sync()
		if err == nil {
			err = publishPluginEvidence(evidence, "transaction.json", tx)
		}
		evidence.Close()
		if err == nil {
			base, e := openPluginEvidenceDirectory(evidenceBase)
			err = e
			if e == nil {
				err = base.Sync()
				base.Close()
			}
		}
		if err == nil {
			base, e := openPluginEvidenceDirectory(s.operations.baseDir)
			err = e
			if e == nil {
				err = base.Sync()
				base.Close()
			}
		}
	}
	if err != nil {
		unix.Unlinkat(int(root.Fd()), temp, 0)
		failUntouched(err)
		return
	}
	// From this point every failure has durable deterministic recovery evidence.
	fail := func(cause error) {
		if _, e := s.operations.transition(id, operationFailed, "rollback", "Add-ons installation failed: "+cause.Error()); e != nil {
			s.operations.transition(id, operationNeedsAttention, "rollback", "Recovery evidence preserved; failure journal could not be saved.")
			return
		}
		if _, e := s.operations.transition(id, operationRollingBack, "rollback", "Removing only the exact transaction-owned file."); e != nil {
			s.operations.transition(id, operationNeedsAttention, "rollback", "Recovery evidence preserved; rollback journal could not be saved.")
			return
		}
		recoveryCtx, c := context.WithTimeout(withPluginInstance(context.Background(), p.Server.InstanceID), 20*time.Minute)
		defer c()
		if e := rollbackAddon(recoveryCtx, tx); e != nil {
			s.operations.transition(id, operationNeedsAttention, "rollback", "Add-ons recovery needs attention. Retry recovery uses preserved evidence: "+e.Error())
			return
		}
		s.operations.transition(id, operationRolledBack, "complete", "Add-ons installation rolled back; original Minecraft state verified.")
	}
	if _, err = s.operations.transition(id, operationValidating, "validating", "Validating managed Add-ons installation."); err != nil {
		fail(err)
		return
	}
	if _, err = s.operations.transition(id, operationRunning, "installing", "Preparing the exact managed Add-ons file."); err != nil {
		fail(err)
		return
	}
	if p.RestartRequired {
		if err = s.operations.pluginProgress(id, "backup", "Creating verified cold Minecraft backup before executable content installation."); err != nil {
			fail(err)
			return
		}
		if err = instanceMaintenanceColdBackup(ctx, p.Server.InstanceID); err != nil {
			fail(errors.New("Verified cold backup failed"))
			return
		}
		if active, e := pluginServiceActive(ctx, pluginInstanceID(ctx)); e != nil || active {
			fail(errors.New("Minecraft did not stop after backup"))
			return
		}
	}
	if err = s.checkAddonEnvironment(ctx, p); err != nil {
		fail(err)
		return
	}
	// Parent paths and host metadata are checked again after backup. Destination
	// creation by this transaction changes only the expected parent evidence.
	inventory, err := readAddonHostInventory(p.DataPath)
	if err != nil {
		fail(err)
		return
	}
	d, _ := addonDefinitionFor(p.Kind)
	host, dest, err := resolveAddonHost(p.DataPath, d, inventory)
	if err != nil || !host.Available || dest != p.Destination || host.Identity != p.Host.Identity {
		fail(errors.New("Host or destination changed"))
		return
	}
	pinned, err := addonPinnedParent(tx)
	if err != nil {
		fail(err)
		return
	}
	pinned.Close()
	if err = addonCollision(p.DataPath, p.addonEntry, m); err != nil {
		fail(err)
		return
	}
	if err = s.operations.pluginProgress(id, "installing", "Atomically placing the exact managed file without overwrite."); err != nil {
		fail(err)
		return
	}
	exists, stagedErr := addonTransactionFile(root, temp, tx)
	if stagedErr != nil || !exists {
		fail(errors.New("Staged content identity or hash changed before placement"))
		return
	}
	if err = unix.Renameat2(int(root.Fd()), temp, int(root.Fd()), p.Filename, unix.RENAME_NOREPLACE); err != nil {
		fail(err)
		return
	}
	if err = root.Sync(); err != nil {
		fail(err)
		return
	}
	if p.RestartRequired {
		if err = s.operations.pluginProgress(id, "restarting", "Starting Minecraft for health verification."); err != nil {
			fail(err)
			return
		}
		if err = pluginService(ctx, pluginInstanceID(ctx), "start"); err != nil {
			fail(err)
			return
		}
	}
	if _, err = s.operations.transition(id, operationVerifying, "verifying", "Verifying exact file ownership and Minecraft health where required."); err != nil {
		fail(err)
		return
	}
	if p.RestartRequired {
		if err = verifyPluginRuntime(ctx, pluginInstanceID(ctx), p.Server.MinecraftVersion); err != nil {
			fail(err)
			return
		}
		if err = restoreAddonRuntime(ctx, tx); err != nil {
			fail(err)
			return
		}
	}
	if err = s.checkAddonEnvironment(ctx, p); err != nil {
		fail(err)
		return
	}
	pinned, err = addonPinnedParent(tx)
	if err != nil {
		fail(err)
		return
	}
	exists, err = addonTransactionFile(pinned, p.Filename, tx)
	pinned.Close()
	if err != nil || !exists {
		fail(errors.New("Final content verification failed"))
		return
	}
	m.Artifacts = append(m.Artifacts, p.addonEntry)
	committed, err := commitAddonManifest(p.DataPath, raw, m)
	if err != nil {
		if committed {
			s.operations.transition(id, operationNeedsAttention, "ownership", "Ownership commit durability needs verification. Retry recovery is available.")
			return
		}
		fail(err)
		return
	}
	message := "Exact managed file installed. No Minecraft restart was required."
	if p.RestartRequired {
		message = "Exact managed file installed; Minecraft returned healthy. Host runtime loading has not been independently verified."
		if !originallyRunning {
			message += " Minecraft returned to its original stopped state."
		}
	}
	if _, err = s.operations.transition(id, operationSucceeded, "complete", message); err == nil {
		success = true
	} else {
		s.operations.transition(id, operationNeedsAttention, "ownership", "Exact file committed; completion journal could not be saved. Retry recovery is available.")
	}
}
func readAddonTransaction(s *server, id string) (addonTransaction, error) {
	var tx addonTransaction
	if !operationIDPattern.MatchString(id) {
		return tx, errors.New("Invalid operation ID")
	}
	root, err := openPluginEvidenceDirectory(filepath.Join(s.operations.baseDir, "plugin-addons", id))
	if err != nil {
		return tx, err
	}
	defer root.Close()
	raw, err := readAddonFile(root, "transaction.json", pluginManifestLimit+16384)
	if err != nil {
		return tx, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&tx) != nil || dec.Decode(new(any)) != io.EOF || tx.SchemaVersion != "v1" || validatePluginInstance(tx.InstanceID, tx.DataPath) != nil || tx.OperationID != id || !filepath.IsAbs(tx.DataPath) || tx.Temp != ".addon-"+id+".tmp" || tx.FileInode == 0 || tx.ParentInode == 0 || tx.RestartRequired != (tx.Entry.Kind != "worldedit_schematic") {
		return tx, errors.New("Invalid Add-ons recovery evidence")
	}
	if err = validateAddonManifest(addonManifest{"v1", []addonEntry{tx.Entry}}); err != nil {
		return tx, err
	}
	if len(tx.Manifest) > 0 {
		var old addonManifest
		if json.Unmarshal(tx.Manifest, &old) != nil || validateAddonManifest(old) != nil {
			return tx, errors.New("Invalid original ownership evidence")
		}
	}
	return tx, nil
}
func (s *server) adminAddonRecover(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	var req struct {
		OperationID string `json:"operation_id"`
	}
	if !decodePluginInstallJSON(w, r, &req) {
		return
	}
	if s.operations == nil {
		writeError(w, 503, "Maintenance unavailable")
		return
	}
	journal, err := s.operations.get(req.OperationID)
	if err != nil || journal.OperationType != operationTypePluginAddonInstall || journal.State != operationNeedsAttention || journal.InstanceID != pluginInstanceID(r.Context()) {
		writeError(w, 409, "Add-ons operation is not awaiting recovery")
		return
	}
	lock, err := acquirePluginRuntimeLock()
	if err != nil {
		writeError(w, 409, "Minecraft maintenance is busy")
		return
	}
	defer lock.Close()
	tx, err := readAddonTransaction(s, req.OperationID)
	if err != nil && journal.Stage == "interrupted_queued" && (errors.Is(err, unix.ENOENT) || errors.Is(err, os.ErrNotExist)) {
		resolved, e := s.operations.transition(req.OperationID, operationResolved, "resolved", "Queued Add-ons operation interrupted before runtime or final content changes.")
		if e != nil {
			writeError(w, 500, "Recovery result unavailable")
			return
		}
		writeJSON(w, 200, adminOperationResponse{Operation: &resolved})
		return
	}
	if err != nil || tx.Fingerprint != journal.PlanFingerprint || tx.InstanceID != journal.InstanceID {
		writeError(w, 409, "Exact Add-ons recovery evidence unavailable; maintenance remains reserved")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Minute)
	defer cancel()
	m, raw, err := loadAddonManifest(tx.DataPath)
	if err == nil && !bytes.Equal(raw, tx.Manifest) {
		expected := addonManifest{"v1", []addonEntry{}}
		if len(tx.Manifest) > 0 {
			json.Unmarshal(tx.Manifest, &expected)
		}
		expected.Artifacts = append(expected.Artifacts, tx.Entry)
		a, _ := json.Marshal(m)
		b, _ := json.Marshal(expected)
		if !bytes.Equal(a, b) {
			err = errors.New("Ownership differs from exact transaction commit")
		} else {
			root, e := addonPinnedParent(tx)
			err = e
			if e == nil {
				exists, e := addonTransactionFile(root, tx.Entry.Filename, tx)
				root.Close()
				err = e
				if !exists && err == nil {
					err = errors.New("Managed target missing")
				}
			}
			if err == nil {
				err = restoreAddonRuntime(ctx, tx)
			}
		}
	} else if err == nil {
		err = rollbackAddon(ctx, tx)
	}
	if err != nil {
		writeError(w, 409, "Recovery cannot yet be verified; evidence is preserved: "+err.Error())
		return
	}
	resolved, err := s.operations.transition(req.OperationID, operationResolved, "resolved", "Exact Add-ons ownership and original Minecraft state recovered and verified.")
	if err != nil {
		writeError(w, 500, "Recovery result could not be persisted")
		return
	}
	writeJSON(w, 200, adminOperationResponse{Operation: &resolved})
}
