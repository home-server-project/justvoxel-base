package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

// Internal Phase 2 inputs. No HTTP route, identity creation, world creation,
// service reload/start, or active runtime publication is attached to this code.
// Callers must provide the COMPLETE configured set, including stopped instances,
// and a fresh trusted snapshot of appliance storage and physical RAM.
// Runtime secrets never enter the Phase 1 identity/name store.
type minecraftInstanceRuntimeConfig struct {
	ID               string `json:"id"`
	JavaPort         int    `json:"java_port"`
	BedrockEnabled   bool   `json:"bedrock_enabled"`
	BedrockPort      int    `json:"bedrock_port"`
	JavaMemory       string `json:"java_memory"`
	ContainerMemory  string `json:"container_memory"`
	Software         string `json:"software"`
	LoaderVersion    string `json:"loader_version,omitempty"`
	VersionPolicy    string `json:"version_policy,omitempty"`
	Version          string `json:"version"` // resolved pin or LATEST, as in the legacy environment
	PaperChannel     string `json:"paper_channel"`
	ImageTag         string `json:"image_tag"`
	ImageDigest      string `json:"image_digest,omitempty"`
	StartOnBoot      bool   `json:"start_on_boot"`
	EULAAccepted     bool   `json:"eula_accepted"`
	UID              uint32 `json:"uid"`
	GID              uint32 `json:"gid"`
	Timezone         string `json:"timezone"`
	GameMode         string `json:"game_mode"`
	Difficulty       string `json:"difficulty"`
	MaxPlayers       int    `json:"max_players"`
	Whitelist        bool   `json:"whitelist"`
	EnforceWhitelist bool   `json:"enforce_whitelist"`
	MOTD             string `json:"motd"`
	RCONPassword     string `json:"rcon_password"`
}

type minecraftInstanceRuntimeContext struct {
	// Defaults for new instances; existing instances use their pinned placement.
	Placements      map[string]instanceRuntimeRecord
	BackupPlacement *instanceRuntimeBackupStorage
	BackupPath      string
	StorageRoot     string
	MountPoint      string
	ExpectedUUID    string
	ExpectedSource  string
	TotalMemoryMiB  int64 // physical RAM, never free memory, swap, or zram
	TemplateRoot    string
}

type minecraftInstanceRuntimeFiles struct {
	ID              string
	UnitName        string
	ContainerName   string
	QuadletPath     string
	EnvironmentPath string
	DataPath        string
	Quadlet         []byte
	Environment     []byte // private; must never be returned through a preview API/log
}

var instanceRuntimePathPattern = regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`)
var instanceRuntimeMemoryPattern = regexp.MustCompile(`^([1-9][0-9]{0,8})([MmGg])$`)
var instanceRuntimeVersionPattern = regexp.MustCompile(`^(LATEST|[0-9]+\.[0-9]+(\.[0-9]+)?([-+][A-Za-z0-9.-]+)?)$`)
var instanceRuntimeSecretPattern = regexp.MustCompile(`^[a-fA-F0-9]{32,128}$`)
var instanceRuntimeTimezonePattern = regexp.MustCompile(`^[A-Za-z0-9_+/-]{1,128}$`)
var instanceRuntimeUUIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,128}$`)

func (ctx minecraftInstanceRuntimeContext) forInstance(id string) minecraftInstanceRuntimeContext {
	if r, ok := ctx.Placements[id]; ok {
		ctx.StorageRoot, ctx.MountPoint, ctx.ExpectedUUID, ctx.ExpectedSource = r.Storage.Root, r.Storage.MountPoint, r.Storage.UUID, r.Storage.Source
		if r.Backups != nil {
			ctx.BackupPath = r.Backups.Root
		}
	}
	return ctx
}

func validateInstanceStorageContext(ctx minecraftInstanceRuntimeContext) error {
	for _, path := range []string{ctx.StorageRoot, ctx.BackupPath} {
		if err := validateInstanceRuntimePath(path); err != nil {
			return err
		}
	}
	// Avoid known OS/configuration trees even if supplied as the storage parent.
	for _, unsafe := range []string{"/etc", "/usr", "/boot", "/proc", "/sys", "/dev", "/run"} {
		if instanceRuntimeInside(ctx.StorageRoot, unsafe) {
			return errors.New("unsafe storage root")
		}
	}
	if ctx.MountPoint == "" {
		if ctx.ExpectedUUID != "" || ctx.ExpectedSource != "" {
			return errors.New("mount evidence requires a mount point")
		}
	} else {
		if err := validateInstanceRuntimePath(ctx.MountPoint); err != nil {
			return err
		}
		if !instanceRuntimeInside(ctx.StorageRoot, ctx.MountPoint) {
			return errors.New("instance storage is outside the configured data mount")
		}
		if ctx.ExpectedUUID == "" && ctx.ExpectedSource == "" {
			return errors.New("configured mount requires identity evidence")
		}
	}
	if ctx.ExpectedUUID != "" && !instanceRuntimeUUIDPattern.MatchString(ctx.ExpectedUUID) {
		return errors.New("invalid mount UUID")
	}
	if ctx.ExpectedSource != "" && (!instanceRuntimePathPattern.MatchString(ctx.ExpectedSource) || filepath.Clean(ctx.ExpectedSource) != ctx.ExpectedSource) {
		return errors.New("invalid mount source")
	}
	return nil
}

func decodeMinecraftInstanceRuntime(r io.Reader) ([]minecraftInstanceRuntimeConfig, error) {
	data, err := io.ReadAll(io.LimitReader(r, 1024*1024+1))
	if err != nil || len(data) > 1024*1024 {
		return nil, errors.New("invalid instance runtime configuration")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var configs []minecraftInstanceRuntimeConfig
	if err := decoder.Decode(&configs); err != nil {
		return nil, errors.New("invalid instance runtime configuration")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || len(configs) == 0 {
		return nil, errors.New("invalid instance runtime configuration")
	}
	return configs, nil
}

func instanceRuntimeMemoryMiB(value string) (int64, error) {
	parts := instanceRuntimeMemoryPattern.FindStringSubmatch(value)
	if parts == nil {
		return 0, errors.New("memory must be a positive M or G quantity")
	}
	n, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, err
	}
	if strings.EqualFold(parts[2], "G") {
		n *= 1024
	}
	// Bound arithmetic and reject limits that cannot be practical appliance inputs.
	if n < 512 || n > 1048576 {
		return 0, errors.New("memory must be between 512 MiB and 1 TiB")
	}
	return n, nil
}

func instanceRuntimeInside(path, parent string) bool {
	return path == parent || strings.HasPrefix(path, parent+"/")
}

// Check every existing component, including dangling symlinks; missing tails
// are allowed for previews. This function never creates a data directory.
func validateInstanceRuntimePath(path string) error {
	if !instanceRuntimePathPattern.MatchString(path) || filepath.Clean(path) != path || path == "/" {
		return errors.New("unsafe instance runtime path")
	}
	for current := path; current != "/"; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("runtime path components must be directories without symlinks")
		}
	}
	return nil
}

func instanceRuntimeText(value string, max int) bool {
	if len(value) > max || !utf8.ValidString(value) || strings.Contains(value, "@@") {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}

func instanceRuntimeEnvEscape(value string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
}

func instanceRuntimeTemplate(source []byte, values map[string]string) ([]byte, error) {
	// Single-pass replacement: user text is never reinterpreted as a template.
	pairs := make([]string, 0, len(values)*2)
	for key, value := range values {
		pairs = append(pairs, "@@"+key+"@@", value)
	}
	rendered := strings.NewReplacer(pairs...).Replace(string(source))
	if strings.Contains(rendered, "@@") {
		return nil, errors.New("unresolved runtime template token")
	}
	return []byte(rendered), nil
}

func instanceRuntimeBool(value bool) string {
	if value {
		return "TRUE"
	}
	return "FALSE"
}

// Pure with respect to appliance state: registry is read under its existing
// lock; names are deliberately not loaded. Even malformed presentation metadata
// cannot become a runtime identifier. Legacy identity cannot use a new Quadlet.
func (s *minecraftInstanceStore) renderInstanceRuntimes(ctx minecraftInstanceRuntimeContext, configs []minecraftInstanceRuntimeConfig) ([]minecraftInstanceRuntimeFiles, error) {
	return s.renderInstanceRuntimesCandidate(ctx, configs, "")
}

// A single internally generated candidate may be validated before registration.
// This never publishes files or accepts an unregistered ID from an API.
func (s *minecraftInstanceStore) renderInstanceRuntimesCandidate(ctx minecraftInstanceRuntimeContext, configs []minecraftInstanceRuntimeConfig, candidate string) ([]minecraftInstanceRuntimeFiles, error) {
	dir, err := s.openDirectory(false)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	if err := syscall.Flock(int(dir.Fd()), syscall.LOCK_SH); err != nil {
		return nil, err
	}
	defer syscall.Flock(int(dir.Fd()), syscall.LOCK_UN)
	// Validate the active identity collection without loading presentation names.
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	registered := map[string]string{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id, err := s.readRecord(entry.Name())
		if err != nil {
			return nil, err
		}
		if registered[id] != "" {
			return nil, errInstanceDuplicate
		}
		if strings.HasPrefix(entry.Name(), "jv-") && entry.Name() != id+".json" {
			return nil, errors.New("instance record filename does not match ID")
		}
		registered[id] = entry.Name()
	}
	if len(configs) == 0 || len(configs) > 1024 {
		return nil, errors.New("invalid runtime configuration count")
	}
	if len(ctx.Placements) == 0 {
		if err := validateInstanceStorageContext(ctx); err != nil {
			return nil, err
		}
	} else {
		for _, c := range configs {
			placement, ok := ctx.Placements[c.ID]
			if !ok || validateInstanceRecordPlacement(placement) != nil {
				return nil, errors.New("invalid instance placement")
			}
			if err := validateInstanceStorageContext(ctx.forInstance(c.ID)); err != nil {
				return nil, err
			}
		}
	}
	tcp, udp := map[int]bool{}, map[int]bool{}
	reserve := func(port int, ports map[int]bool) error {
		if port < 1 || port > 65535 {
			return errors.New("port must be between 1 and 65535")
		}
		if ports[port] {
			return errors.New("conflicting configured port assignment")
		}
		ports[port] = true
		return nil
	}
	if ctx.TotalMemoryMiB < 2048 || ctx.TotalMemoryMiB > 16777216 {
		return nil, errors.New("invalid physical memory reservation")
	}
	seen := map[string]bool{}
	ordered := append([]minecraftInstanceRuntimeConfig(nil), configs...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	for _, c := range ordered {
		ctx := ctx.forInstance(c.ID)
		if !instanceIDPattern.MatchString(c.ID) {
			return nil, errInstanceID
		}
		if seen[c.ID] {
			return nil, errors.New("duplicate instance ID")
		}
		seen[c.ID] = true
		if c.ID == candidate {
			if registered[c.ID] != "" {
				return nil, errors.New("candidate identity is already registered or unavailable")
			}
		} else if registered[c.ID] != c.ID+".json" {
			return nil, errors.New("runtime instance must already be registered")
		}
		dataPath := filepath.Join(ctx.StorageRoot, "minecraft-instances", c.ID, "data")
		if err := validateInstanceRuntimePath(dataPath); err != nil {
			return nil, err
		}
		for _, reserved := range []string{ctx.BackupPath, s.directory} {
			if instanceRuntimeInside(dataPath, reserved) || instanceRuntimeInside(reserved, dataPath) {
				return nil, errors.New("instance data overlaps protected state")
			}
		}
		if err := reserve(c.JavaPort, tcp); err != nil {
			return nil, err
		}
		if c.BedrockEnabled && !javaServerCrossplay(c.Software) {
			return nil, errors.New("unsupported cross-play software")
		}
		if c.BedrockEnabled {
			if err := reserve(c.BedrockPort, tcp); err != nil {
				return nil, err
			}
			if err := reserve(c.BedrockPort, udp); err != nil {
				return nil, err
			}
		} else if c.BedrockPort != 0 {
			return nil, errors.New("disabled Bedrock must not reserve a port")
		}
		heap, err := instanceRuntimeMemoryMiB(c.JavaMemory)
		if err != nil {
			return nil, err
		}
		limit, err := instanceRuntimeMemoryMiB(c.ContainerMemory)
		if err != nil {
			return nil, err
		}
		if limit-heap < 512 {
			return nil, errors.New("container memory must leave at least 512 MiB above Java memory")
		}
		if limit > ctx.TotalMemoryMiB-1024 {
			return nil, errors.New("container memory must leave at least 1 GiB for the appliance")
		}
		if (c.ImageDigest != "" && !operationFingerprintPattern.MatchString(c.ImageDigest)) || (c.VersionPolicy != "" && c.VersionPolicy != "pinned" && c.VersionPolicy != "recommended" && c.VersionPolicy != "latest") || !validSetupServerType(c.Software) || !instanceRuntimeVersionPattern.MatchString(c.Version) || !imageTagInputPattern.MatchString(c.ImageTag) {
			return nil, errors.New("invalid Minecraft software, version, or image tag")
		}
		if err := validateJavaServerSelection(c.Software, c.VersionPolicy, c.Version, c.LoaderVersion, c.ImageTag, c.BedrockEnabled); err != nil {
			return nil, err
		}
		if c.PaperChannel != "default" && c.PaperChannel != "experimental" {
			return nil, errors.New("invalid Paper channel")
		}
		if c.Software == "paper" && c.Version == "LATEST" && c.PaperChannel != "experimental" {
			return nil, errors.New("latest Paper requires the experimental channel")
		}
		if !c.EULAAccepted || c.UID == 0 || c.GID == 0 || c.UID == ^uint32(0) || c.GID == ^uint32(0) {
			return nil, errors.New("explicit EULA acceptance and non-root Minecraft IDs are required")
		}
		if c.GameMode != "survival" && c.GameMode != "creative" && c.GameMode != "adventure" && c.GameMode != "spectator" {
			return nil, errors.New("invalid game mode")
		}
		if c.Difficulty != "peaceful" && c.Difficulty != "easy" && c.Difficulty != "normal" && c.Difficulty != "hard" {
			return nil, errors.New("invalid difficulty")
		}
		if c.MaxPlayers < 1 || c.MaxPlayers > 1000 || !instanceRuntimeTimezonePattern.MatchString(c.Timezone) || !instanceRuntimeText(c.MOTD, 1024) || !instanceRuntimeSecretPattern.MatchString(c.RCONPassword) {
			return nil, errors.New("invalid startup configuration")
		}
		if _, err := time.LoadLocation(c.Timezone); err != nil {
			return nil, errors.New("invalid startup timezone")
		}
	}

	envTemplate, err := os.ReadFile(filepath.Join(ctx.TemplateRoot, "config", "minecraft.env.in"))
	if err != nil {
		return nil, err
	}
	quadletTemplate, err := os.ReadFile(filepath.Join(ctx.TemplateRoot, "quadlets", "minecraft-instance.container.in"))
	if err != nil {
		return nil, err
	}
	files := make([]minecraftInstanceRuntimeFiles, 0, len(ordered))
	for _, c := range ordered {
		ctx := ctx.forInstance(c.ID)
		name := "minecraft-" + c.ID
		f := minecraftInstanceRuntimeFiles{ID: c.ID, UnitName: name + ".service", ContainerName: name, QuadletPath: "/etc/containers/systemd/" + name + ".container", EnvironmentPath: "/etc/justvoxel/instances/" + c.ID + "/minecraft.env", DataPath: filepath.Join(ctx.StorageRoot, "minecraft-instances", c.ID, "data")}
		capabilities, _ := javaServerSoftware(c.Software)
		values := map[string]string{
			"EULA": "TRUE", "MINECRAFT_SERVER_TYPE": capabilities.Type, "MINECRAFT_VERSION": c.Version, "PAPER_CHANNEL": c.PaperChannel,
			"MINECRAFT_UID": strconv.FormatUint(uint64(c.UID), 10), "MINECRAFT_GID": strconv.FormatUint(uint64(c.GID), 10), "TIMEZONE": instanceRuntimeEnvEscape(c.Timezone),
			"JAVA_MEMORY": strings.ToUpper(c.JavaMemory), "GAME_MODE": c.GameMode, "DIFFICULTY": c.Difficulty, "MAX_PLAYERS": strconv.Itoa(c.MaxPlayers),
			"WHITELIST_ENABLED": instanceRuntimeBool(c.Whitelist), "ENFORCE_WHITELIST": instanceRuntimeBool(c.EnforceWhitelist),
			"RCON_PASSWORD": instanceRuntimeEnvEscape(c.RCONPassword), "MOTD": instanceRuntimeEnvEscape(c.MOTD), "PLUGINS_LINE": "# Instance cross-play is not supported in Phase 2.",
		}
		source := string(envTemplate)
		if c.Software != "paper" {
			source = regexp.MustCompile(`(?m)^PAPER_CHANNEL=.*\n`).ReplaceAllString(source, "")
		}
		if !javaServerManagedViaVersion(c.Software) {
			source = regexp.MustCompile(`(?m)^MODRINTH_PROJECTS=.*\n`).ReplaceAllString(source, "")
		}
		f.Environment, err = instanceRuntimeTemplate([]byte(source), values)
		if err != nil {
			return nil, err
		}
		if c.Software == "spigot" {
			f.Environment = append(f.Environment, []byte("BUILD_FROM_SOURCE=TRUE\n")...)
		} else if capabilities.ModLoader {
			f.Environment = append(f.Environment, []byte(capabilities.LoaderEnvironment+"="+c.LoaderVersion+"\n")...)
		}
		install := "# Start on Boot disabled; service remains manually startable."
		if c.StartOnBoot {
			install = "[Install]\nWantedBy=multi-user.target"
		}
		bedrockPublish := ""
		if c.BedrockEnabled {
			bedrockPublish = "PublishPort=" + strconv.Itoa(c.BedrockPort) + ":19132/tcp\nPublishPort=" + strconv.Itoa(c.BedrockPort) + ":19132/udp"
		}
		f.Quadlet, err = instanceRuntimeTemplate(quadletTemplate, map[string]string{
			"BEDROCK_PUBLISH": bedrockPublish,
			"INSTANCE_ID":     c.ID, "DATA_PATH": f.DataPath, "ENV_PATH": f.EnvironmentPath, "STORAGE_ROOT": ctx.StorageRoot, "JAVA_PORT": strconv.Itoa(c.JavaPort),
			"MINECRAFT_IMAGE": instanceRuntimeImage(c), "CONTAINER_MEMORY": strings.ToUpper(c.ContainerMemory),
			"DATA_MOUNT_POINT": ctx.MountPoint, "DATA_EXPECTED_UUID": ctx.ExpectedUUID, "DATA_EXPECTED_SOURCE": ctx.ExpectedSource, "INSTALL_SECTION": install,
		})
		if err != nil {
			return nil, err
		}
		if c.Software != "vanilla" && !capabilities.ManagedCrossplay {
			f.Quadlet = bytes.Replace(f.Quadlet, []byte("/instance-crossplay-start "), []byte("/validate-instance-start-memory "), 1)
		}
		files = append(files, f)
	}
	return files, nil
}

// Publish only an inactive, new private bundle, atomically as one directory.
// No replacement or copying into systemd search paths. Activation/rollback of
// live multi-file configuration belongs to the separate Phase 3 operation.
// Caller owns a private staging parent (root:root in production). Re-render
// under the activation lock with fresh reservations before any future apply.
func (s *minecraftInstanceStore) stageInstanceRuntimes(ctx minecraftInstanceRuntimeContext, configs []minecraftInstanceRuntimeConfig, parent, name string) error {
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`).MatchString(name) {
		return errors.New("invalid runtime bundle name")
	}
	if err := validateInstanceRuntimePath(parent); err != nil {
		return err
	}
	if strings.Contains(parent+"/", "/containers/systemd/") {
		return errors.New("staging must be outside Quadlet search paths")
	}
	for _, active := range []string{"/etc", "/usr", "/run", "/boot", "/proc", "/sys", "/dev", ctx.StorageRoot, ctx.BackupPath, s.directory} {
		if instanceRuntimeInside(parent, active) {
			return errors.New("staging must be outside active configuration and Minecraft storage")
		}
	}
	files, err := s.renderInstanceRuntimes(ctx, configs)
	if err != nil {
		return err
	}
	dir, err := os.OpenFile(parent, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer dir.Close()
	info, err := dir.Stat()
	if err != nil || !s.private(info, 0700) {
		return errors.New("runtime staging parent must be private and owned by root")
	}
	if err := syscall.Flock(int(dir.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(dir.Fd()), syscall.LOCK_UN)
	target := filepath.Join(parent, name)
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return errors.New("runtime bundle already exists or is inaccessible")
	}
	temp, err := os.MkdirTemp(parent, ".runtime-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	if err := os.Chown(temp, s.uid, s.gid); err != nil {
		return err
	}
	write := func(path string, data []byte) error {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		defer file.Close()
		if err := file.Chown(s.uid, s.gid); err != nil {
			return err
		}
		if _, err := file.Write(data); err != nil {
			return err
		}
		if err := file.Sync(); err != nil {
			return err
		}
		return file.Close()
	}
	for _, f := range files {
		if err := write(filepath.Join(temp, f.ID+".env"), f.Environment); err != nil {
			return err
		}
		if err := write(filepath.Join(temp, f.ContainerName+".container"), f.Quadlet); err != nil {
			return err
		}
	}
	staging, err := os.Open(temp)
	if err != nil {
		return err
	}
	err = staging.Sync()
	staging.Close()
	if err != nil {
		return err
	}
	// NOREPLACE also protects against non-cooperating writers; never clobber
	// an existing preview, symlink, or working bundle.
	if err := unix.Renameat2(unix.AT_FDCWD, temp, unix.AT_FDCWD, target, unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("complete runtime bundle published but durability is uncertain: %w", err)
	}
	return nil
}

func instanceRuntimeImage(c minecraftInstanceRuntimeConfig) string {
	if c.ImageDigest != "" {
		return "docker.io/itzg/minecraft-server@" + c.ImageDigest
	}
	return "docker.io/itzg/minecraft-server:" + c.ImageTag
}
