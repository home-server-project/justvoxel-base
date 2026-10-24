package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const instanceRuntimeHelper = "/usr/libexec/justvoxel/mjust/instance-runtime-prerequisites"

// Runtime configuration is independent of identity and presentation metadata.
// Hashes commit the two live files; pending.json inhibits startup even after reboot.
type instanceRuntimeRecord struct {
	SchemaVersion     int                            `json:"schema_version"`
	Runtime           minecraftInstanceRuntimeConfig `json:"runtime"`
	Backups           *instanceRuntimeBackupStorage  `json:"backups,omitempty"`
	Storage           instanceRuntimeStorage         `json:"storage"`
	EnvironmentSHA256 string                         `json:"environment_sha256"`
	QuadletSHA256     string                         `json:"quadlet_sha256"`
	Firewall          []instanceFirewallRule         `json:"firewall"`
}
type instanceRuntimeStorage struct {
	Root       string `json:"root"`
	DataPath   string `json:"data_path"`
	MountPoint string `json:"mount_point"`
	UUID       string `json:"uuid"`
	Source     string `json:"source"`
}
type instanceFirewallRule struct {
	Zone      string `json:"zone"`
	Port      int    `json:"port"`
	Permanent bool   `json:"permanent"`
	Protocol  string `json:"protocol,omitempty"`
}
type instanceFirewallChange struct {
	Rule instanceFirewallRule `json:"rule"`
	Add  bool                 `json:"add"`
}
type instanceActivationEvidence struct {
	LiveDefinition bool                     `json:"live_definition,omitempty"`
	SchemaVersion  int                      `json:"schema_version"`
	ID             string                   `json:"id"`
	Previous       instanceRuntimeRecord    `json:"previous"`
	Target         instanceRuntimeRecord    `json:"target"`
	Environment    []byte                   `json:"environment"`
	Quadlet        []byte                   `json:"quadlet"`
	Changes        []instanceFirewallChange `json:"changes"`
}
type instanceRuntimeBackend struct {
	root, quadlets, staging string
	store                   *minecraftInstanceStore
	command                 func(context.Context, string, ...string) ([]byte, error)
	pause                   func(context.Context, time.Duration) error
	snapshot                func(context.Context) (minecraftInstanceRuntimeContext, uint32, uint32, error)
}

var instanceRuntimes = &instanceRuntimeBackend{
	root: "/etc/justvoxel/instances", quadlets: "/etc/containers/systemd", staging: "/var/lib/justvoxel-instance-runtime",
	command: instanceRuntimeCommand, snapshot: instanceRuntimeSnapshot,
}
var errInstanceRecovery = instanceRuntimeFailure("Instance runtime recovery is required. Retry the instance recovery operation.")
var errInstanceBusy = instanceRuntimeFailure("Another appliance operation is active.")

// systemd 257 prints an empty Job value when no job is queued.
// A failed query never establishes idleness.
func systemdJobIdle(output []byte, err error) bool {
	value := strings.TrimSpace(string(output))
	return err == nil && (value == "" || value == "0")
}

func instanceRuntimeCommand(ctx context.Context, command string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	var output pluginBoundedOutput
	cmd.Stdout = &output
	// Do not merge stderr with protocol output or expose it in API errors.
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	finish, err := attachOperationTrace(ctx, cmd)
	if err != nil {
		return nil, err
	}
	err = cmd.Run()
	finish(err)
	return output.Bytes(), err
}
func instanceRuntimeSnapshot(ctx context.Context) (minecraftInstanceRuntimeContext, uint32, uint32, error) {
	data, err := instanceRuntimeCommand(ctx, instanceRuntimeHelper, "snapshot")
	if err != nil {
		return minecraftInstanceRuntimeContext{}, 0, 0, instanceRuntimeFailure("Appliance storage and resource discovery is unavailable.")
	}
	return decodeInstanceRuntimeSnapshot(data)
}

func decodeInstanceRuntimeSnapshot(data []byte) (minecraftInstanceRuntimeContext, uint32, uint32, error) {
	var out struct {
		Storage    instanceRuntimeStorage        `json:"storage"`
		Backups    *instanceRuntimeBackupStorage `json:"backups,omitempty"`
		BackupPath string                        `json:"backup_path"`
		Memory     int64                         `json:"memory_mib"`
		UID        uint32                        `json:"uid"`
		GID        uint32                        `json:"gid"`
	}
	if instanceDecode(data, &out) != nil || out.UID == 0 || out.GID == 0 || out.UID == ^uint32(0) || out.GID == ^uint32(0) || out.Memory <= 0 || validateInstanceRuntimePath(out.Storage.Root) != nil || validateInstanceRuntimePath(out.BackupPath) != nil || out.Storage.DataPath != "" {
		return minecraftInstanceRuntimeContext{}, 0, 0, instanceRuntimeFailure("Appliance storage and resource discovery is unavailable.")
	}
	return minecraftInstanceRuntimeContext{
		BackupPlacement: out.Backups, BackupPath: out.BackupPath, StorageRoot: out.Storage.Root,
		MountPoint: out.Storage.MountPoint, ExpectedUUID: out.Storage.UUID, ExpectedSource: out.Storage.Source,
		TotalMemoryMiB: out.Memory, TemplateRoot: "/usr/share/justvoxel/templates",
	}, out.UID, out.GID, nil
}

func (b *instanceRuntimeBackend) identities() *minecraftInstanceStore {
	if b.store != nil {
		return b.store
	}
	return minecraftInstances
}
func instanceHash(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }

// Openat2 rejects symlinks in every component, including intermediate directories.
func instanceOpen(path string, flags int) (*os.File, error) {
	fd, err := unix.Openat2(unix.AT_FDCWD, path, &unix.OpenHow{Flags: uint64(flags | unix.O_CLOEXEC), Resolve: unix.RESOLVE_NO_SYMLINKS})
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
func (b *instanceRuntimeBackend) privateDirectory(path string, create bool) (*os.File, error) {
	if create {
		if err := validateInstanceRuntimePath(filepath.Dir(path)); err != nil {
			return nil, err
		}
		created := false
		if err := os.Mkdir(path, 0700); err != nil {
			if !errors.Is(err, os.ErrExist) {
				return nil, err
			}
		} else {
			created = true
		}
		if created {
			if err := os.Chown(path, b.identities().uid, b.identities().gid); err != nil {
				return nil, err
			}
			parent, err := instanceOpen(filepath.Dir(path), unix.O_RDONLY|unix.O_DIRECTORY)
			if err != nil {
				return nil, err
			}
			err = parent.Sync()
			parent.Close()
			if err != nil {
				return nil, err
			}
		}
	}
	f, err := instanceOpen(path, unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !b.identities().private(info, 0700) {
		f.Close()
		return nil, instanceRuntimeFailure("Runtime directory must be private and root owned.")
	}
	return f, nil
}
func (b *instanceRuntimeBackend) readPrivate(path string) ([]byte, error) {
	f, err := instanceOpen(path, unix.O_RDONLY|unix.O_NONBLOCK)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !b.identities().private(info, 0600) {
		return nil, instanceRuntimeFailure("Invalid private runtime file.")
	}
	data, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return nil, instanceRuntimeFailure("Invalid private runtime file.")
	}
	return data, nil
}
func instanceDecode(data []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(value) != nil {
		return instanceRuntimeFailure("Invalid private runtime configuration.")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return instanceRuntimeFailure("Invalid private runtime configuration.")
	}
	return nil
}
func (b *instanceRuntimeBackend) paths(id string) (string, string, string) {
	dir := filepath.Join(b.root, id)
	return dir, filepath.Join(dir, "minecraft.env"), filepath.Join(b.quadlets, "minecraft-"+id+".container")
}
func (b *instanceRuntimeBackend) writePrivate(path string, data []byte, replace bool) error {
	parent, err := instanceOpen(filepath.Dir(path), unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return err
	}
	defer parent.Close()
	info, err := parent.Stat()
	stat, ok := infoSysStat(info)
	if err != nil || !ok || int(stat.Uid) != b.identities().uid || info.Mode().Perm()&0022 != 0 {
		return instanceRuntimeFailure("Unsafe runtime publication directory.")
	}
	if old, err := b.readPrivate(path); err == nil {
		_ = old
		if !replace {
			return instanceRuntimeFailure("Runtime file already exists.")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temp, err := os.CreateTemp(parent.Name(), ".runtime-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if err = temp.Chown(b.identities().uid, b.identities().gid); err != nil {
		return err
	}
	if err = temp.Chmod(0600); err != nil {
		return err
	}
	if _, err = temp.Write(data); err != nil {
		return err
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	flags := uint(0)
	if !replace {
		flags = unix.RENAME_NOREPLACE
	}
	if err = unix.Renameat2(unix.AT_FDCWD, temp.Name(), int(parent.Fd()), filepath.Base(path), flags); err != nil {
		return err
	}
	return parent.Sync()
}
func infoSysStat(info os.FileInfo) (*syscall.Stat_t, bool) {
	if info == nil {
		return nil, false
	}
	s, ok := info.Sys().(*syscall.Stat_t)
	return s, ok
}
func (b *instanceRuntimeBackend) writeJSON(path string, v any, replace bool) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return b.writePrivate(path, append(data, '\n'), replace)
}
func (b *instanceRuntimeBackend) removePrivate(path string) error {
	if _, err := b.readPrivate(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	dir, err := instanceOpen(filepath.Dir(path), unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err = unix.Unlinkat(int(dir.Fd()), filepath.Base(path), 0); err != nil {
		return err
	}
	return dir.Sync()
}
func (b *instanceRuntimeBackend) readRecord(id string) (instanceRuntimeRecord, error) {
	var record instanceRuntimeRecord
	if !instanceIDPattern.MatchString(id) {
		return record, errInstanceID
	}
	identityBytes, err := b.readPrivate(filepath.Join(b.identities().directory, id+".json"))
	var identity minecraftInstanceRecord
	if err != nil || instanceDecode(identityBytes, &identity) != nil || identity.ID != id {
		return record, errInstanceNotFound
	}
	return b.readRuntimeConfig(id)
}

// Internal configuration reader. Public lifecycle callers must use readRecord.
func (b *instanceRuntimeBackend) readRuntimeConfig(id string) (instanceRuntimeRecord, error) {
	var record instanceRuntimeRecord
	dir, _, _ := b.paths(id)
	f, err := b.privateDirectory(b.root, false)
	if err != nil {
		return record, err
	}
	f.Close()
	f, err = b.privateDirectory(dir, false)
	if err != nil {
		return record, err
	}
	f.Close()
	data, err := b.readPrivate(filepath.Join(dir, "config.json"))
	if err != nil {
		return record, err
	}
	if err = instanceDecode(data, &record); err != nil {
		return record, err
	}
	if record.SchemaVersion != 1 || record.Runtime.ID != id || (record.Runtime.ImageDigest != "" && !operationFingerprintPattern.MatchString(record.Runtime.ImageDigest)) || !validSetupServerType(record.Runtime.Software) || !instanceCrossplayValid(record.Runtime) || !instanceRuntimeSecretPattern.MatchString(record.Runtime.RCONPassword) || record.Storage.DataPath != filepath.Join(record.Storage.Root, "minecraft-instances", id, "data") {
		return record, instanceRuntimeFailure("Invalid ID-to-runtime mapping.")
	}
	for _, p := range []string{record.Storage.Root, record.Storage.DataPath} {
		if validateInstanceRuntimePath(p) != nil {
			return record, instanceRuntimeFailure("Invalid runtime storage.")
		}
	}
	if err := validateInstanceRecordPlacement(record); err != nil {
		return record, err
	}
	if (record.EnvironmentSHA256 == "") != (record.QuadletSHA256 == "") {
		return record, instanceRuntimeFailure("Invalid runtime commitment.")
	}
	if record.EnvironmentSHA256 != "" && (!instanceSHA256Pattern.MatchString(record.EnvironmentSHA256) || !instanceSHA256Pattern.MatchString(record.QuadletSHA256)) {
		return record, instanceRuntimeFailure("Invalid runtime commitment.")
	}
	for _, rule := range record.Firewall {
		if !instanceFirewallZonePattern.MatchString(rule.Zone) || !instanceRuleForRuntime(rule, record.Runtime) {
			return record, instanceRuntimeFailure("Invalid firewall ownership.")
		}
	}
	return record, nil
}
func (b *instanceRuntimeBackend) records() ([]instanceRuntimeRecord, error) {
	f, err := b.privateDirectory(b.root, false)
	if errors.Is(err, os.ErrNotExist) {
		return []instanceRuntimeRecord{}, b.verifyRuntimeCoverage(nil)
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	records := []instanceRuntimeRecord{}
	for _, entry := range entries {
		if !instanceIDPattern.MatchString(entry.Name()) || !entry.IsDir() {
			return nil, instanceRuntimeFailure("Invalid runtime directory entry.")
		}
		record, err := b.readRecord(entry.Name())
		if errors.Is(err, os.ErrNotExist) {
			// An interrupted registration/config write has no active reservation yet.
			// Only private unpublished files may coexist with a missing config.
			dir, env, q := b.paths(entry.Name())
			for _, p := range []string{env, q, filepath.Join(dir, "pending.json")} {
				if _, e := os.Lstat(p); !errors.Is(e, os.ErrNotExist) {
					return nil, errInstanceRecovery
				}
			}
			f, e := b.privateDirectory(dir, false)
			if e != nil {
				return nil, e
			}
			entries, e := f.ReadDir(-1)
			f.Close()
			if e != nil {
				return nil, e
			}
			for _, temp := range entries {
				if !strings.HasPrefix(temp.Name(), ".runtime-") {
					return nil, errInstanceRecovery
				}
				if _, e = b.readPrivate(filepath.Join(dir, temp.Name())); e != nil {
					return nil, e
				}
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, b.verifyRuntimeCoverage(records)
}
func (b *instanceRuntimeBackend) pending(id string) (bool, error) {
	if _, err := b.readPrivate(b.deletePath(id)); !errors.Is(err, os.ErrNotExist) {
		return true, errInstanceRecovery
	}
	dir, _, _ := b.paths(id)
	_, err := b.readPrivate(filepath.Join(dir, "pending.json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return true, err
}
func (b *instanceRuntimeBackend) verifyLive(record instanceRuntimeRecord) error {
	pending, err := b.pending(record.Runtime.ID)
	if err != nil || pending {
		return errInstanceRecovery
	}
	_, env, quadlet := b.paths(record.Runtime.ID)
	e, err := b.readPrivate(env)
	if err != nil {
		return instanceRuntimeFailure("Runtime environment is unavailable.")
	}
	q, err := b.readPrivate(quadlet)
	if err != nil {
		return instanceRuntimeFailure("Runtime Quadlet is unavailable.")
	}
	if record.EnvironmentSHA256 == "" || instanceHash(e) != record.EnvironmentSHA256 || instanceHash(q) != record.QuadletSHA256 {
		return instanceRuntimeFailure("Runtime files do not match committed configuration.")
	}
	return nil
}
func (b *instanceRuntimeBackend) render(ctx minecraftInstanceRuntimeContext, records []instanceRuntimeRecord) ([]minecraftInstanceRuntimeFiles, error) {
	return b.renderCandidate(ctx, records, "")
}
func (b *instanceRuntimeBackend) renderCandidate(ctx minecraftInstanceRuntimeContext, records []instanceRuntimeRecord, candidate string) ([]minecraftInstanceRuntimeFiles, error) {
	ctx.Placements = make(map[string]instanceRuntimeRecord, len(records))
	configs := make([]minecraftInstanceRuntimeConfig, 0, len(records))
	for _, r := range records {
		if err := validateInstanceRecordPlacement(r); err != nil {
			return nil, err
		}
		ctx.Placements[r.Runtime.ID] = r
		configs = append(configs, r.Runtime)
	}
	return b.identities().renderInstanceRuntimesCandidate(ctx, configs, candidate)
}

func (b *instanceRuntimeBackend) storage(ctx context.Context, action string, record instanceRuntimeRecord) error {
	c := record.Runtime
	s := record.Storage
	_, err := b.command(ctx, "nsenter", "--mount=/proc/1/ns/mnt", "--", instanceRuntimeHelper, action, c.ID, s.Root, s.DataPath, s.MountPoint, s.UUID, s.Source, strconv.FormatUint(uint64(c.UID), 10), strconv.FormatUint(uint64(c.GID), 10))
	if err != nil {
		return instanceRuntimeFailure("Instance storage or mount identity validation failed.")
	}
	return nil
}
func (b *instanceRuntimeBackend) validateSoftware(ctx context.Context, c minecraftInstanceRuntimeConfig) error {
	if c.Software == "bedrock" {
		if !nativeBedrockValid(c) {
			return instanceRuntimeFailure("Invalid Bedrock runtime")
		}
		return nil
	}
	args := []string{"version", c.Software, c.Version, c.PaperChannel}
	if c.Software != "paper" && c.Software != "purpur" && c.Software != "vanilla" {
		if err := validateJavaServerSelection(c.Software, c.VersionPolicy, c.Version, c.LoaderVersion, c.ImageTag, c.BedrockEnabled); err != nil {
			return err
		}
		args = append(args, c.LoaderVersion)
	}
	_, err := b.command(ctx, instanceRuntimeHelper, args...)
	if err == nil && c.BedrockEnabled && (c.Software == "fabric" || c.Software == "neoforge") {
		_, err = b.command(ctx, instanceRuntimeHelper, "crossplay", c.Software, c.Version, c.LoaderVersion)
	}
	if err != nil {
		return instanceRuntimeFailure("Selected Minecraft software/version could not be verified.")
	}
	return nil
}

// Bind both address families, holding the port until publication completes.
// Reservations remain authoritative even when a configured instance is stopped.
func instanceHoldPort(port int) ([]net.Listener, error) {
	listeners := []net.Listener{}
	for _, address := range []string{"0.0.0.0:", "[::]:"} {
		network := "tcp4"
		if strings.HasPrefix(address, "[") {
			network = "tcp6"
		}
		l, err := net.Listen(network, address+strconv.Itoa(port))
		if err != nil {
			// IPv6 may be disabled on the appliance; all other errors fail closed.
			if network == "tcp6" && (errors.Is(err, syscall.EAFNOSUPPORT) || errors.Is(err, syscall.EADDRNOTAVAIL)) {
				continue
			}
			for _, old := range listeners {
				old.Close()
			}
			return nil, instanceRuntimeFailure("Java TCP port is already occupied or unavailable.")
		}
		listeners = append(listeners, l)
	}
	return listeners, nil
}

// Reserve the same persistent-operation locks without changing journal schemas.
// Holding mu prevents another Agent operation from borrowing our held descriptors.
func (s *server) lockInstanceRuntime() (func() error, error) {
	if instanceRestoreActive.Load() {
		return nil, errInstanceBusy
	}
	if s.operations == nil {
		return nil, instanceRuntimeFailure("Appliance operation tracking is unavailable.")
	}
	o := s.operations
	o.mu.Lock()
	if o.currentSetupID != "" || o.currentRestoreID != "" || o.currentDataMigrationID != "" || o.currentMigrationID != "" || o.currentMinecraftResetID != "" || o.currentFactoryResetID != "" {
		o.mu.Unlock()
		return nil, errInstanceBusy
	}
	if err := instanceRuntimes.requireNoInstanceRestore(); err != nil {
		o.mu.Unlock()
		return nil, err
	}
	if err := o.acquireMinecraftResetLocks(); err != nil {
		o.mu.Unlock()
		return nil, errInstanceBusy
	}
	lock, err := acquirePluginRuntimeLock()
	if err != nil {
		releaseErr := o.releaseMinecraftResetLocks()
		o.mu.Unlock()
		return nil, errors.Join(errInstanceBusy, releaseErr)
	}
	return func() error {
		err := lock.Close()
		err = errors.Join(err, o.releaseMinecraftResetLocks())
		o.mu.Unlock()
		return err
	}, nil
}

func instanceStorageFor(ctx minecraftInstanceRuntimeContext, id string) instanceRuntimeStorage {
	return instanceRuntimeStorage{Root: ctx.StorageRoot, DataPath: filepath.Join(ctx.StorageRoot, "minecraft-instances", id, "data"), MountPoint: ctx.MountPoint, UUID: ctx.ExpectedUUID, Source: ctx.ExpectedSource}
}
func instanceReplaceRecord(records []instanceRuntimeRecord, next instanceRuntimeRecord) []instanceRuntimeRecord {
	out := append([]instanceRuntimeRecord(nil), records...)
	for i := range out {
		if out[i].Runtime.ID == next.Runtime.ID {
			out[i] = next
			return out
		}
	}
	return append(out, next)
}

// Apply only to the selected instance. Recovery evidence is durable before any
// live-file/firewall mutation. Data is never removed, including on rollback.
func (b *instanceRuntimeBackend) apply(ctx context.Context, previous instanceRuntimeRecord, records []instanceRuntimeRecord, snapshot minecraftInstanceRuntimeContext) (result error) {
	return b.applyVersion(ctx, previous, records, snapshot, false)
}

// Root maintenance workers publish reviewed version or image changes. The
// image-only path changes only the digest and can defer a running restart.
// Ordinary configuration callers retain their existing stopped-only rules.
func (b *instanceRuntimeBackend) applyVersion(ctx context.Context, previous instanceRuntimeRecord, records []instanceRuntimeRecord, snapshot minecraftInstanceRuntimeContext, maintenance bool, imageOnly ...bool) (result error) {
	id := previous.Runtime.ID
	for _, record := range records {
		pending, err := b.pending(record.Runtime.ID)
		if err != nil || pending {
			return errInstanceRecovery
		}
	}
	pending, err := b.pending(id)
	if err != nil || pending {
		return errInstanceRecovery
	}
	all, err := b.render(snapshot, records)
	if err != nil {
		return err
	}
	var next instanceRuntimeRecord
	var files minecraftInstanceRuntimeFiles
	for _, r := range records {
		if r.Runtime.ID == id {
			next = r
		}
	}
	for _, f := range all {
		if f.ID == id {
			files = f
		}
	}
	if next.Runtime.StartOnBoot {
		if err = b.admitMemory(ctx, snapshot.TotalMemoryMiB, records, next.Runtime, true); err != nil {
			return err
		}
	}
	if next.Runtime.ID == "" || files.ID == "" {
		return errInstanceNotFound
	}
	if err = b.storage(ctx, "preflight", next); err != nil {
		return err
	}
	if (previous.Runtime.Software == "bedrock" || next.Runtime.Software == "bedrock") && (previous.Runtime.Software != next.Runtime.Software || previous.Runtime.Version != next.Runtime.Version) {
		return instanceRuntimeFailure("Bedrock software/version conversion is not supported")
	}
	// Full configuration changes are stopped-only; boot preference is independent.
	state, err := b.observe(ctx, id)
	if err != nil {
		return err
	}
	if previous.EnvironmentSHA256 == "" {
		state, err = b.observeRaw(ctx, id)
		if err != nil {
			return err
		}
	}
	containerOnly := len(imageOnly) == 1 && imageOnly[0]
	if containerOnly {
		before, after := previous.Runtime, next.Runtime
		before.ImageDigest = after.ImageDigest
		if !maintenance || !reflect.DeepEqual(before, after) || instanceHash(files.Environment) != previous.EnvironmentSHA256 {
			return errInstanceRecovery
		}
		if next.Runtime.ImageDigest != "" {
			digest, e := b.command(ctx, "podman", "image", "inspect", instanceRuntimeImage(next.Runtime), "--format", "{{.Digest}}")
			if e != nil || strings.TrimSpace(string(digest)) != next.Runtime.ImageDigest {
				return errInstanceRecovery
			}
		}
	}
	before, after := previous.Runtime, next.Runtime
	before.StartOnBoot = after.StartOnBoot
	bootOnly := previous.EnvironmentSHA256 != "" && reflect.DeepEqual(before, after) && (state.State == "running" || previous.Runtime.StartOnBoot != next.Runtime.StartOnBoot)
	if bootOnly {
		if err = b.verifyLive(previous); err != nil {
			return err
		}
		_, oldEnvPath, oldQuadletPath := b.paths(id)
		files.Environment, err = b.readPrivate(oldEnvPath)
		if err != nil {
			return err
		}
		oldQuadlet, e := b.readPrivate(oldQuadletPath)
		if e != nil {
			return e
		}
		install := "# Start on Boot disabled; service remains manually startable."
		if after.StartOnBoot {
			install = "[Install]\nWantedBy=multi-user.target"
		}
		files.Quadlet = []byte(instanceQuadletWithoutBoot(oldQuadlet) + "\n" + install + "\n")
		for i := range all {
			if all[i].ID == id {
				all[i] = files
			}
		}
	}
	whitelistOnly := false
	if previous.EnvironmentSHA256 != "" && previous.Runtime.Whitelist != next.Runtime.Whitelist {
		before, after := previous.Runtime, next.Runtime
		before.Whitelist = after.Whitelist
		before.StartOnBoot = after.StartOnBoot
		whitelistOnly = reflect.DeepEqual(before, after)
	}
	channelOnly := false
	if maintenance && previous.EnvironmentSHA256 != "" {
		before, after := previous.Runtime, next.Runtime
		before.ImageTag = after.ImageTag
		before.ImageDigest = after.ImageDigest
		before.VersionPolicy = after.VersionPolicy
		before.Version = after.Version
		before.PaperChannel = after.PaperChannel
		channelOnly = reflect.DeepEqual(before, after)
		if channelOnly && state.State == "running" && !containerOnly {
			oldImage := instanceRuntimeImage(previous.Runtime)
			newImage := instanceRuntimeImage(next.Runtime)
			oldID, e1 := b.command(ctx, "podman", "image", "inspect", oldImage, "--format", "{{.Id}}")
			newID, e2 := b.command(ctx, "podman", "image", "inspect", newImage, "--format", "{{.Id}}")
			running, e3 := b.command(ctx, "podman", "inspect", "minecraft-"+id, "--format", "{{.Image}}")
			if e1 != nil || e2 != nil || e3 != nil || strings.TrimSpace(string(oldID)) == "" || string(oldID) != string(newID) || string(oldID) != string(running) {
				return errInstanceRecovery
			}
			if instanceHash(files.Environment) != previous.EnvironmentSHA256 {
				if _, err = b.command(ctx, "env", "JUSTVOXEL_MAINTENANCE_LOCK_HELD=1", "/usr/libexec/justvoxel/mjust/verify-minecraft-stack", "--instance", id, next.Runtime.Version); err != nil {
					return instanceRuntimeFailure("Running Minecraft does not match this definition-only change.")
				}
			}

		}
	}
	if state.State != "stopped" && state.State != "failed" && state.State != "configured" && !((bootOnly || channelOnly || whitelistOnly) && state.State == "running") {
		return instanceRuntimeFailure("Stop the selected instance before changing its runtime configuration.")
	}
	if !maintenance && previous.EnvironmentSHA256 != "" && previous.Runtime.Version != next.Runtime.Version {
		// No world-version inference: until Phase 4 only a verified empty data
		// directory can change versions. The shared maintenance lock is held.
		if next.Runtime.Version == "LATEST" {
			return instanceRuntimeFailure("Pin an explicit Minecraft version; world upgrades require independent backups in Phase 4.")
		}
		if err = b.storage(ctx, "version-empty", previous); err != nil {
			return instanceRuntimeFailure("Version changes on existing data require independent backup/migration support in Phase 4. No version was changed.")
		}
	}
	if previous.EnvironmentSHA256 != "" && previous.Runtime.LoaderVersion != next.Runtime.LoaderVersion {
		if err = b.storage(ctx, "version-empty", previous); err != nil {
			return instanceRuntimeFailure("Loader changes on existing data require separate compatibility support. No loader was changed.")
		}
	}
	if !bootOnly && !channelOnly && !whitelistOnly && next.Runtime.BedrockEnabled {
		heldUDP, e := instanceHoldCrossplayPort(next.Runtime.BedrockPort)
		if e != nil {
			return e
		}
		defer heldUDP.Close()
	}
	if !bootOnly && previous.Runtime.BedrockEnabled && (!next.Runtime.BedrockEnabled || previous.Runtime.BedrockPort != next.Runtime.BedrockPort) {
		heldUDP, e := instanceHoldCrossplayPort(previous.Runtime.BedrockPort)
		if e != nil {
			return e
		}
		defer heldUDP.Close()
	}
	if previous.EnvironmentSHA256 != "" && previous.Runtime.Software != next.Runtime.Software && !(maintenance && javaServerAllowsSwitch(previous.Runtime.Software, next.Runtime.Software)) {
		return instanceRuntimeFailure("Changing software on an existing instance is outside Phase 3A.")
	}
	var held []net.Listener
	if next.Runtime.Software == "bedrock" && state.State != "running" {
		ports, e := holdNativeBedrock(next.Runtime)
		if e != nil {
			return e
		}
		defer closeBedrockPorts(ports)
	}
	if next.Runtime.Software != "bedrock" && (state.State != "running" || next.Runtime.JavaPort != previous.Runtime.JavaPort) {
		held, err = instanceHoldPort(instanceSignalingPort(next.Runtime))
		if err != nil {
			return err
		}
		defer func() {
			for _, l := range held {
				l.Close()
			}
		}()
	}
	if next.Runtime.JavaPort != previous.Runtime.JavaPort && len(previous.Firewall) > 0 {
		old, err := instanceHoldPort(instanceSignalingPort(previous.Runtime))
		if err != nil {
			return instanceRuntimeFailure("Previous Java port is in use; firewall access cannot safely be removed.")
		}
		defer func() {
			for _, l := range old {
				l.Close()
			}
		}()
	}
	if !bootOnly && !whitelistOnly && !containerOnly {
		if err = b.validateSoftware(ctx, next.Runtime); err != nil {
			return err
		}
	}
	stage, err := b.privateDirectory(b.staging, true)
	if err != nil {
		return err
	}
	stage.Close()
	var entropy [16]byte
	if _, err = rand.Read(entropy[:]); err != nil {
		return err
	}
	bundle := "apply-" + hex.EncodeToString(entropy[:])
	configs := make([]minecraftInstanceRuntimeConfig, 0, len(records))
	for _, r := range records {
		configs = append(configs, r.Runtime)
	}
	if err = b.identities().stageInstanceRuntimes(snapshot, configs, b.staging, bundle); err != nil {
		return err
	}
	bundlePath := filepath.Join(b.staging, bundle)
	// Before journal publication, this invocation owns only this inactive bundle.
	preJournal := true
	defer func() {
		if preJournal {
			if err := os.RemoveAll(bundlePath); err != nil {
				result = errors.Join(result, instanceRuntimeFailure("Inactive staging cleanup failed."))
			}
		}
	}()
	if bootOnly {
		if err = b.writePrivate(filepath.Join(bundlePath, id+".env"), files.Environment, true); err != nil {
			return err
		}
		if err = b.writePrivate(filepath.Join(bundlePath, "minecraft-"+id+".container"), files.Quadlet, true); err != nil {
			return err
		}
	}
	evidence := instanceActivationEvidence{SchemaVersion: 1, ID: id, Previous: previous, Target: next, LiveDefinition: channelOnly && state.State == "running" && previous.EnvironmentSHA256 != instanceHash(files.Environment)}
	dir, env, quadlet := b.paths(id)
	if previous.EnvironmentSHA256 != "" {
		if err = b.verifyLive(previous); err != nil {
			return err
		}
		evidence.Environment, err = b.readPrivate(env)
		if err != nil {
			return err
		}
		evidence.Quadlet, err = b.readPrivate(quadlet)
		if err != nil {
			return err
		}
	} else {
		for _, path := range []string{env, quadlet} {
			if _, e := os.Lstat(path); !errors.Is(e, os.ErrNotExist) {
				return instanceRuntimeFailure("Unowned instance runtime already exists.")
			}
		}
	}
	// Use the Phase 2 bundle unchanged, including its startup guard.
	if err = b.validateBundle(ctx, bundlePath, all); err != nil {
		return err
	}
	next.EnvironmentSHA256 = instanceHash(files.Environment)
	next.QuadletSHA256 = instanceHash(files.Quadlet)
	var changes []instanceFirewallChange
	owned := append([]instanceFirewallRule(nil), previous.Firewall...)
	if !bootOnly && !containerOnly {
		changes, owned, err = b.planFirewall(ctx, previous, next)
		if err != nil {
			return err
		}
	}
	next.Firewall = owned
	evidence.Target = next
	evidence.Changes = changes
	pendingPath := filepath.Join(dir, "pending.json")
	// Publication may succeed even if its directory sync fails. Leave cleanup to
	// the existing confirmed success/rollback paths from this point onward.
	preJournal = false
	if err = b.writeJSON(pendingPath, evidence, false); err != nil {
		return errInstanceRecovery
	}
	fail := func() error {
		recoveryCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if b.rollback(recoveryCtx, evidence) != nil {
			return errInstanceRecovery
		}
		if err := os.RemoveAll(bundlePath); err != nil {
			return instanceRuntimeFailure("Runtime was rolled back; inactive staging cleanup failed.")
		}
		return instanceRuntimeFailure("Instance activation failed; previous runtime was restored. Instance data was preserved.")
	}
	if whitelistOnly && state.State == "running" {
		command := "whitelist off"
		if next.Runtime.Whitelist {
			command = "whitelist on"
		}
		if _, err = b.rcon(ctx, id, command); err != nil {
			return fail()
		}
	}
	if err = b.storage(ctx, "provision", next); err != nil {
		return fail()
	}
	if err = b.storage(ctx, "validate", next); err != nil {
		return fail()
	}
	for _, change := range changes {
		if err = b.changeFirewall(ctx, change); err != nil {
			return fail()
		}
	}
	if err = b.writePrivate(env, files.Environment, previous.EnvironmentSHA256 != ""); err != nil {
		return fail()
	}
	if err = b.writePrivate(quadlet, files.Quadlet, previous.QuadletSHA256 != ""); err != nil {
		return fail()
	}
	if err = b.writeJSON(filepath.Join(dir, "config.json"), next, true); err != nil {
		return fail()
	}
	// daemon-reload does not start a unit. Pending inhibits any simultaneous start.
	if _, err = b.command(ctx, "systemctl", "daemon-reload"); err != nil {
		return fail()
	}
	if err = b.verifyHashes(next); err != nil {
		return fail()
	}
	observed, err := b.observeRaw(ctx, id)
	expected := "stopped"
	if state.State == "running" {
		expected = "running"
	}
	if state.State == "failed" {
		expected = "failed"
	}
	if err != nil || observed.State != expected {
		return fail()
	}
	if err = b.clearPending(pendingPath); err != nil {
		return errInstanceRecovery
	}
	if err := os.RemoveAll(bundlePath); err != nil {
		return instanceRuntimeFailure("Runtime applied; inactive staging cleanup failed.")
	}
	return nil
}
func (b *instanceRuntimeBackend) verifyHashes(r instanceRuntimeRecord) error {
	_, env, q := b.paths(r.Runtime.ID)
	e, err := b.readPrivate(env)
	if err != nil {
		return err
	}
	data, err := b.readPrivate(q)
	if err != nil {
		return err
	}
	if instanceHash(e) != r.EnvironmentSHA256 || instanceHash(data) != r.QuadletSHA256 {
		return instanceRuntimeFailure("Runtime publication verification failed.")
	}
	return nil
}
func (b *instanceRuntimeBackend) validateBundle(ctx context.Context, path string, files []minecraftInstanceRuntimeFiles) error {
	// env accepts only an internally generated, validated path, never client text.
	output, err := b.command(ctx, "env", "QUADLET_UNIT_DIRS="+path, "/usr/lib/systemd/system-generators/podman-system-generator", "--dryrun")
	if err != nil {
		return instanceRuntimeFailure("Rendered Quadlet validation failed.")
	}
	for _, f := range files {
		if !strings.Contains(string(output), f.UnitName) {
			return instanceRuntimeFailure("Rendered Quadlet did not generate its expected service.")
		}
	}
	return nil
}
func (b *instanceRuntimeBackend) rollback(ctx context.Context, e instanceActivationEvidence) error {
	id := e.ID
	dir, env, q := b.paths(id)
	if e.Previous.Runtime.Whitelist != e.Target.Runtime.Whitelist {
		state, err := b.observeRaw(ctx, id)
		if err != nil {
			return err
		}
		if state.State == "running" {
			command := "whitelist off"
			if e.Previous.Runtime.Whitelist {
				command = "whitelist on"
			}
			if _, err = b.rcon(ctx, id, command); err != nil {
				return err
			}
		}
	}
	// Pending stays present until restoration, firewall, reload and hashes agree.
	// No stop, start, world removal, or unrelated firewall reload occurs here.
	if e.Previous.EnvironmentSHA256 != "" {
		if instanceHash(e.Environment) != e.Previous.EnvironmentSHA256 || instanceHash(e.Quadlet) != e.Previous.QuadletSHA256 {
			return errInstanceRecovery
		}
		for _, item := range []struct{ path, previous, target string }{{env, e.Previous.EnvironmentSHA256, e.Target.EnvironmentSHA256}, {q, e.Previous.QuadletSHA256, e.Target.QuadletSHA256}} {
			data, err := b.readPrivate(item.path)
			if err != nil || (instanceHash(data) != item.previous && instanceHash(data) != item.target) {
				return errInstanceRecovery
			}
		}
		if err := b.writePrivate(env, e.Environment, true); err != nil {
			return err
		}
		if err := b.writePrivate(q, e.Quadlet, true); err != nil {
			return err
		}
	} else {
		// Delete only exact files from this attempted activation, never unknown files.
		for _, item := range []struct{ path, hash string }{{env, e.Target.EnvironmentSHA256}, {q, e.Target.QuadletSHA256}} {
			data, err := b.readPrivate(item.path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil || instanceHash(data) != item.hash {
				return errInstanceRecovery
			}
			if err = b.removePrivate(item.path); err != nil {
				return err
			}
		}
	}
	for i := len(e.Changes) - 1; i >= 0; i-- {
		change := e.Changes[i]
		change.Add = !change.Add
		if err := b.changeFirewall(ctx, change); err != nil {
			return err
		}
	}
	if err := b.writeJSON(filepath.Join(dir, "config.json"), e.Previous, true); err != nil {
		return err
	}
	if _, err := b.command(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if e.Previous.EnvironmentSHA256 != "" {
		if err := b.verifyHashes(e.Previous); err != nil {
			return err
		}
	}
	observed, err := b.observeRaw(ctx, id)
	if err != nil {
		return err
	}
	if e.Previous.EnvironmentSHA256 == "" {
		if observed.State != "configured" {
			return errInstanceRecovery
		}
	} else if observed.State != "stopped" && observed.State != "running" && observed.State != "failed" {
		return errInstanceRecovery
	}
	return b.clearPending(filepath.Join(dir, "pending.json"))
}
func (b *instanceRuntimeBackend) recover(ctx context.Context, id string) error {
	if !instanceIDPattern.MatchString(id) {
		return errInstanceID
	}
	if _, err := b.readRecord(id); err != nil {
		return err
	}
	dir, _, _ := b.paths(id)
	data, err := b.readPrivate(filepath.Join(dir, "pending.json"))
	if err != nil {
		return err
	}
	var e instanceActivationEvidence
	if instanceDecode(data, &e) != nil || e.SchemaVersion != 1 || e.ID != id || e.Previous.Runtime.ID != id || e.Target.Runtime.ID != id {
		return errInstanceRecovery
	}
	// Never roll files back under a running, starting or stopping instance.
	state, err := b.observeRaw(ctx, id)
	before, after := e.Previous.Runtime, e.Target.Runtime
	before.StartOnBoot = after.StartOnBoot
	if e.Previous.EnvironmentSHA256 == e.Target.EnvironmentSHA256 {
		before.ImageTag = after.ImageTag
		before.ImageDigest = after.ImageDigest
		before.VersionPolicy = after.VersionPolicy
	}
	if e.LiveDefinition {
		before.ImageTag = after.ImageTag
		before.ImageDigest = after.ImageDigest
		before.VersionPolicy = after.VersionPolicy
		before.Version = after.Version
		before.PaperChannel = after.PaperChannel
	}
	whitelistBefore, whitelistAfter := e.Previous.Runtime, e.Target.Runtime
	whitelistBefore.Whitelist = whitelistAfter.Whitelist
	whitelistBefore.StartOnBoot = whitelistAfter.StartOnBoot
	runningWhitelist := state.State == "running" && e.Previous.EnvironmentSHA256 != "" && e.Previous.Runtime.Whitelist != e.Target.Runtime.Whitelist && reflect.DeepEqual(whitelistBefore, whitelistAfter) && len(e.Changes) == 0
	runningBootOnly := state.State == "running" && e.Previous.EnvironmentSHA256 != "" && reflect.DeepEqual(before, after) && len(e.Changes) == 0
	if err != nil || (state.State != "stopped" && state.State != "failed" && state.State != "configured" && !runningBootOnly && !runningWhitelist) {
		return errInstanceRecovery
	}
	if e.LiveDefinition && (!reflect.DeepEqual(before, after) || len(e.Changes) != 0 || e.Previous.Runtime.Software != e.Target.Runtime.Software) {
		return errInstanceRecovery
	}
	if e.Previous.SchemaVersion != 1 || e.Target.SchemaVersion != 1 || !reflect.DeepEqual(e.Previous.Storage, e.Target.Storage) || len(e.Changes) > 8 || (runningBootOnly && !e.LiveDefinition && e.Previous.EnvironmentSHA256 != e.Target.EnvironmentSHA256) {
		return errInstanceRecovery
	}
	for _, record := range []instanceRuntimeRecord{e.Previous, e.Target} {
		if record.Storage.DataPath != filepath.Join(record.Storage.Root, "minecraft-instances", id, "data") {
			return errInstanceRecovery
		}
		if record.EnvironmentSHA256 != "" && (!instanceSHA256Pattern.MatchString(record.EnvironmentSHA256) || !instanceSHA256Pattern.MatchString(record.QuadletSHA256)) {
			return errInstanceRecovery
		}
	}
	seenPorts := map[string]bool{}
	for _, c := range e.Changes {
		if !instanceFirewallZonePattern.MatchString(c.Rule.Zone) || c.Rule.Port < 1 || c.Rule.Port > 65535 || (!instanceRuleForRuntime(c.Rule, e.Previous.Runtime) && !instanceRuleForRuntime(c.Rule, e.Target.Runtime)) {
			return errInstanceRecovery
		}
		key := strconv.Itoa(c.Rule.Port) + c.Rule.protocol()
		if seenPorts[key] {
			continue
		}
		seenPorts[key] = true
		held, err := instanceHoldFirewallPort(c.Rule)
		if err != nil {
			return errInstanceRecovery
		}
		for _, l := range held {
			defer l.Close()
		}
	}
	if err = b.rollback(ctx, e); err != nil {
		return errInstanceRecovery
	}
	return nil
}

func (b *instanceRuntimeBackend) clearPending(path string) error {
	data, err := b.readPrivate(path)
	if err != nil {
		return err
	}
	if err = b.removePrivate(path); err != nil {
		if _, readErr := b.readPrivate(path); errors.Is(readErr, os.ErrNotExist) {
			return errors.Join(err, b.writePrivate(path, data, false))
		}
		return err
	}
	return nil
}

type instanceRuntimeSafeError struct{ message string }

func (e *instanceRuntimeSafeError) Error() string { return e.message }
func instanceRuntimeFailure(message string) error { return &instanceRuntimeSafeError{message: message} }

func instanceQuadletWithoutBoot(data []byte) string {
	value := strings.TrimSpace(string(data))
	for _, suffix := range []string{"[Install]\nWantedBy=multi-user.target", "# Start on Boot disabled; service remains manually startable."} {
		value = strings.TrimSuffix(value, suffix)
	}
	return strings.TrimSpace(value)
}

// The config store is authoritative. Orphan/foreign instance Quadlets may not
// silently escape reservation discovery or be adopted as a new configuration.
func (b *instanceRuntimeBackend) verifyRuntimeCoverage(records []instanceRuntimeRecord) error {
	dir, err := instanceOpen(b.quadlets, unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}
	configured := map[string]instanceRuntimeRecord{}
	for _, r := range records {
		configured[r.Runtime.ID] = r
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "minecraft-jv-") || !strings.HasSuffix(name, ".container") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "minecraft-"), ".container")
		record, ok := configured[id]
		if !instanceIDPattern.MatchString(id) || !ok {
			return errInstanceRecovery
		}
		pending, err := b.pending(id)
		if err != nil {
			return errInstanceRecovery
		}
		if pending {
			continue
		}
		if record.EnvironmentSHA256 == "" {
			return errInstanceRecovery
		}
		if err = b.verifyHashes(record); err != nil {
			return errInstanceRecovery
		}
	}
	// An activated record must also retain its exact published files.
	for _, r := range records {
		pending, err := b.pending(r.Runtime.ID)
		if err != nil {
			return errInstanceRecovery
		}
		if !pending && r.EnvironmentSHA256 != "" {
			if err = b.verifyHashes(r); err != nil {
				return errInstanceRecovery
			}
		}
	}
	return nil
}
