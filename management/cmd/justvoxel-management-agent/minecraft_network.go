package main

// Minecraft Network is an opt-in overlay. Runtime.JavaPort, BedrockPort and all
// per-instance whitelist settings always describe the standalone configuration.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const minecraftNetworkRoot = "/etc/justvoxel/minecraft-network"
const minecraftNetworkHelper = "/usr/libexec/justvoxel/mjust/minecraft-network-runtime"
const minecraftNetworkName = "justvoxel-minecraft-network"

type minecraftNetworkConfig struct {
	Enabled        bool     `json:"enabled"`
	JavaPort       int      `json:"java_port"`
	BedrockPort    int      `json:"bedrock_port"`
	Members        []string `json:"members"`
	WelcomeMessage string   `json:"welcome_message"`
}
type minecraftNetworkPlayer struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

// This object and the publication journal are private root-owned JSON. Public
// responses use a separate allowlist of fields, never this structure.
type minecraftNetworkState struct {
	Schema           int                      `json:"schema"`
	Config           minecraftNetworkConfig   `json:"config"`
	Imported         []string                 `json:"imported"`
	Whitelist        []minecraftNetworkPlayer `json:"whitelist"`
	ForwardingSecret string                   `json:"forwarding_secret"`
	FloodgateKey     []byte                   `json:"floodgate_key"`
	Firewall         []instanceFirewallRule   `json:"firewall"`
}
type minecraftNetworkFile struct {
	Path    string `json:"path"`
	Before  []byte `json:"before"`
	After   []byte `json:"after"`
	Existed bool   `json:"existed"`
}
type minecraftNetworkJournal struct {
	Schema  int                      `json:"schema"`
	Before  minecraftNetworkState    `json:"before"`
	After   minecraftNetworkState    `json:"after"`
	Files   []minecraftNetworkFile   `json:"files"`
	Changes []instanceFirewallChange `json:"changes"`
}

var networkUUIDPattern = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)
var networkJavaName = regexp.MustCompile(`^[A-Za-z0-9_]{1,16}$`)
var networkBedrockName = regexp.MustCompile(`^[A-Za-z0-9_ ][A-Za-z0-9_ ]{0,15}$`)

// Probe only the configured local Java entrance; errors never reach public status.
var networkTCPReady = func(ctx context.Context, javaPort int) bool {
	if javaPort < 1024 || javaPort > 65535 {
		return false
	}
	dialer := net.Dialer{Timeout: 250 * time.Millisecond}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(javaPort)))
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func networkRuntimeReady(ctx context.Context, b *instanceRuntimeBackend, javaPort int) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for _, unit := range []string{"justvoxel-picolobby.service", "justvoxel-velocity.service"} {
		if _, err := b.command(ctx, "systemctl", "is-active", "--quiet", unit); err != nil {
			return false
		}
	}
	return ctx.Err() == nil && networkTCPReady(ctx, javaPort)
}

func networkDefaultState() minecraftNetworkState {
	return minecraftNetworkState{Schema: 1, Config: minecraftNetworkConfig{JavaPort: 25565, BedrockPort: 19132, Members: []string{}, WelcomeMessage: "Welcome to JustVoxel!"}, Imported: []string{}, Whitelist: []minecraftNetworkPlayer{}, Firewall: []instanceFirewallRule{}}
}
func networkContains(ids []string, id string) bool {
	for _, v := range ids {
		if id == v {
			return true
		}
	}
	return false
}
func networkEligible(software string) bool { return software == "paper" || software == "purpur" }
func networkRecordEligible(c minecraftInstanceRuntimeConfig) bool {
	if !networkEligible(c.Software) {
		return false
	}
	if c.Version == "LATEST" {
		return true
	}
	parts := regexp.MustCompile(`^([0-9]+)[.]([0-9]+)`).FindStringSubmatch(c.Version)
	if parts == nil {
		return false
	}
	major, _ := strconv.Atoi(parts[1])
	minor, _ := strconv.Atoi(parts[2])
	return major > 1 || (major == 1 && minor >= 13)
}
func networkValidateConfig(c minecraftNetworkConfig) error {
	if c.JavaPort < 1024 || c.JavaPort > 65535 || c.BedrockPort < 1024 || c.BedrockPort > 65535 || c.JavaPort == c.BedrockPort || len(c.Members) > 27 || len(c.WelcomeMessage) > 512 || strings.IndexFunc(c.WelcomeMessage, unicode.IsControl) >= 0 {
		return instanceRuntimeFailure("Invalid Minecraft Network ports, members or welcome message.")
	}
	seen := map[string]bool{}
	for _, id := range c.Members {
		if !instanceIDPattern.MatchString(id) || seen[id] {
			return errInstanceID
		}
		seen[id] = true
	}
	return nil
}
func networkRead(b *instanceRuntimeBackend) (minecraftNetworkState, error) {
	state := networkDefaultState()
	data, err := b.readPrivate(filepath.Join(b.networkDirectory(), "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if instanceDecode(data, &state) != nil || state.Schema != 1 || networkValidateConfig(state.Config) != nil {
		return state, instanceRuntimeFailure("Minecraft Network configuration is unavailable.")
	}
	if state.Config.Enabled && (!instanceRuntimeSecretPattern.MatchString(state.ForwardingSecret) || len(state.FloodgateKey) != 16) {
		return state, errInstanceRecovery
	}
	if _, err = networkMergePlayers(state.Whitelist, nil); err != nil {
		return state, err
	}
	return state, nil
}
func networkPending(b *instanceRuntimeBackend) error {
	_, err := b.readPrivate(filepath.Join(b.networkDirectory(), "pending.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return instanceRuntimeFailure("Minecraft Network recovery is required. Use Retry recovery in Minecraft Network.")
}
func networkMergePlayers(existing, incoming []minecraftNetworkPlayer) ([]minecraftNetworkPlayer, error) {
	byID := map[string]minecraftNetworkPlayer{}
	for _, p := range append(append([]minecraftNetworkPlayer{}, existing...), incoming...) {
		if !networkUUIDPattern.MatchString(p.UUID) || len(p.Name) == 0 || len(p.Name) > 32 || strings.ContainsAny(p.Name, "\x00\r\n") {
			return nil, instanceRuntimeFailure("A whitelist contains an invalid player identity; no network changes were made.")
		}
		p.UUID = strings.ToLower(p.UUID)
		if _, ok := byID[p.UUID]; !ok {
			byID[p.UUID] = p
		}
	}
	out := make([]minecraftNetworkPlayer, 0, len(byID))
	for _, p := range byID {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UUID < out[j].UUID })
	return out, nil
}
func networkImportWhitelist(b *instanceRuntimeBackend, r instanceRuntimeRecord) ([]minecraftNetworkPlayer, error) {
	f, err := instanceOpen(filepath.Join(r.Storage.DataPath, "whitelist.json"), os.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		return []minecraftNetworkPlayer{}, nil
	}
	if err != nil {
		return nil, instanceRuntimeFailure("A member whitelist could not be read.")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errInstanceRecovery
	}
	data, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return nil, errInstanceRecovery
	}
	var entries []minecraftNetworkPlayer
	if instanceDecode(data, &entries) != nil {
		return nil, instanceRuntimeFailure("A member whitelist could not be imported.")
	}
	return networkMergePlayers(nil, entries)
}

// The only modification to an enrolled instance's generated definition. Its
// existing private data mount, boot setting and stored standalone ports survive.
func networkOverlay(f minecraftInstanceRuntimeFiles, r instanceRuntimeRecord) (minecraftInstanceRuntimeFiles, error) {
	if !r.NetworkMember {
		return f, nil
	}
	if !networkRecordEligible(r.Runtime) {
		return f, instanceRuntimeFailure("This server software remains standalone.")
	}
	f.Quadlet = regexp.MustCompile(`(?m)^PublishPort=.*\n`).ReplaceAll(f.Quadlet, nil)
	f.Quadlet = bytes.Replace(f.Quadlet, []byte("[Container]\n"), []byte("[Container]\nNetwork="+minecraftNetworkName+".network\nNetworkAlias="+r.Runtime.ID+"\n"), 1)
	f.Environment = bytes.Replace(f.Environment, []byte("ONLINE_MODE=TRUE\n"), []byte("ONLINE_MODE=FALSE\n"), 1)
	f.Environment = append(f.Environment, []byte("PATCH_DEFINITIONS=/data/.justvoxel-network-patches.json\n")...)
	return f, nil
}
func networkInfrastructure(s minecraftNetworkState, names map[string]string) map[string][]byte {
	if !s.Config.Enabled {
		return map[string][]byte{}
	}
	var velocity strings.Builder
	velocity.WriteString("config-version = \"2.9\"\nbind = \"0.0.0.0:25565\"\nonline-mode = true\nforce-key-authentication = false\nplayer-info-forwarding-mode = \"modern\"\nforwarding-secret-file = \"forwarding.secret\"\nforced-hosts = {}\n[servers]\njv-lobby = \"justvoxel-picolobby:25565\"\n")
	ids := append([]string{}, s.Config.Members...)
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Fprintf(&velocity, "%s = %q\n", id, id+":25565")
	}
	velocity.WriteString("try = [\"jv-lobby\"]\n[advanced]\nbungee-plugin-message-channel = true\n")
	var lobby strings.Builder
	fmt.Fprintf(&lobby, "bind = \"0.0.0.0:25565\"\nwelcome_message = %q\ndefault_game_mode = \"adventure\"\n[forwarding]\nmethod = \"MODERN\"\nsecret = %q\n[lobby]\nenabled = true\n[lobby.selector]\nslot = 4\nitem = \"minecraft:compass\"\ndisplay_name = \"Server Selector\"\n", s.Config.WelcomeMessage, s.ForwardingSecret)
	lobby.WriteString("[server_list]\nmax_players = 0\n")
	for i, id := range ids {
		fmt.Fprintf(&lobby, "[[lobby.servers]]\nid = %q\ndisplay_name = %q\nserver = %q\n[[lobby.npcs]]\nid = %q\ndestination = %q\nname = %q\nx = %d.0\ny = 320.0\nz = 4.0\nyaw = 180.0\npitch = 0.0\n", id, names[id], id, id+"-npc", id, names[id], i*3)
	}
	players, _ := json.Marshal(s.Whitelist)
	return map[string][]byte{
		"velocity/velocity.toml": []byte(velocity.String()), "velocity/forwarding.secret": []byte(s.ForwardingSecret),
		"velocity/plugins/global-whitelist/config.properties": []byte("white-list=true\nenforce-whitelist=true\n"),
		"velocity/plugins/global-whitelist/whitelist.json":    players,
		"velocity/plugins/floodgate/key.pem":                  s.FloodgateKey,
		"velocity/plugins/floodgate/config.yml": []byte(`key-file-name: key.pem
username-prefix: "."
replace-spaces: true

disconnect:
  invalid-key: "Please connect through the official Geyser"
  invalid-arguments-length: "Expected {} arguments, got {}. Is Geyser up-to-date?"

player-link:
  enabled: false
  require-link: false
  enable-own-linking: false
  allowed: false
  link-code-timeout: 300
  type: sqlite
  enable-global-linking: false

metrics:
  enabled: false
  uuid: "00000000-0000-0000-0000-000000000000"

send-floodgate-data: true
config-version: 3
`),
		"velocity/plugins/Geyser-Velocity/config.yml": []byte("bedrock:\n  address: 0.0.0.0\n  port: 19132\n  clone-remote-port: false\n  transport: nethernet\nremote:\n  address: auto\n  auth-type: floodgate\n"),
		"picolobby/server.toml":                       []byte(lobby.String()),
	}
}
func registerMinecraftNetworkRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /v1/minecraft/network", s.minecraftNetworkStatus)
	mux.HandleFunc("PUT /v1/minecraft/network", s.minecraftNetworkConfigure)
	mux.HandleFunc("POST /v1/minecraft/network/recover", s.minecraftNetworkRecover)
	mux.HandleFunc("GET /v1/minecraft/network/whitelist", s.minecraftNetworkStatus)
	mux.HandleFunc("POST /v1/minecraft/network/whitelist", s.minecraftNetworkWhitelist)
	mux.HandleFunc("GET /v1/minecraft/instances/{id}/connection", s.minecraftNetworkConnection)
}
func (s *server) minecraftNetworkStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	n, err := networkRead(instanceRuntimes)
	if err != nil {
		writeError(w, 409, "Minecraft Network configuration is unavailable.")
		return
	}
	records, err := instanceRuntimes.records()
	if err != nil {
		instanceRuntimeError(w, err, "")
		return
	}
	members := []map[string]any{}
	for _, record := range records {
		state, e := instanceRuntimes.observe(r.Context(), record.Runtime.ID)
		status := "unavailable"
		if e == nil {
			status = state.State
		}
		members = append(members, map[string]any{"id": record.Runtime.ID, "software": record.Runtime.Software, "eligible": networkRecordEligible(record.Runtime), "state": status})
	}
	players := []map[string]string{}
	for _, p := range n.Whitelist {
		platform := "java"
		name := p.Name
		if strings.HasPrefix(p.UUID, "00000000-0000-0000-") || strings.HasPrefix(name, ".") {
			platform = "bedrock"
			name = strings.TrimPrefix(name, ".")
		}
		players = append(players, map[string]string{"name": name, "platform": platform})
	}
	recoveryRequired := networkPending(instanceRuntimes) != nil
	writeJSON(w, 200, map[string]any{"config": n.Config, "servers": members, "whitelist": players, "whitelist_enforced": true, "recovery_required": recoveryRequired, "runtime_ready": n.Config.Enabled && !recoveryRequired && networkRuntimeReady(r.Context(), instanceRuntimes, n.Config.JavaPort)})
}
func (s *server) minecraftNetworkConnection(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireReadAccess(w, r); !ok {
		return
	}
	id, ok := instanceRequestID(w, r)
	if !ok {
		return
	}
	record, err := instanceRuntimes.readRecord(id)
	if err != nil {
		instanceRuntimeError(w, err, id)
		return
	}
	n, err := networkRead(instanceRuntimes)
	if err != nil || networkPending(instanceRuntimes) != nil {
		writeError(w, 409, "Minecraft connection information is unavailable during network recovery.")
		return
	}
	java, bedrock, enabled := record.Runtime.JavaPort, record.Runtime.BedrockPort, record.Runtime.BedrockEnabled
	member := n.Config.Enabled && networkContains(n.Config.Members, id)
	if member {
		java, bedrock, enabled = n.Config.JavaPort, n.Config.BedrockPort, true
	}
	writeJSON(w, 200, map[string]any{"id": id, "network_member": member, "java_port": java, "bedrock_port": bedrock, "bedrock_enabled": enabled})
}
func (s *server) minecraftNetworkConfigure(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	var c minecraftNetworkConfig
	if !decodeJSON(w, r, &c) {
		return
	}
	if networkValidateConfig(c) != nil {
		writeError(w, 400, "Invalid Minecraft Network configuration.")
		return
	}
	releaseMaintenance, unlock, err := s.lockPhasedInstanceRuntime()
	if err != nil {
		instanceRuntimeError(w, err, "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 240*time.Second)
	defer cancel()
	err = errors.Join(networkApplyWithMaintenanceRelease(ctx, instanceRuntimes, c, releaseMaintenance), unlock())
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "minecraft_network_configure", "minecraft_network", err == nil, "")
	}
	if err != nil {
		instanceRuntimeError(w, err, "")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (s *server) minecraftNetworkRecover(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdministrator(w, r); !ok {
		return
	}
	var req struct{}
	if !decodeJSON(w, r, &req) {
		return
	}
	unlock, err := s.lockInstanceRuntime(true)
	if err != nil {
		instanceRuntimeError(w, err, "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	if err = errors.Join(networkRecover(ctx, instanceRuntimes), unlock()); err != nil {
		instanceRuntimeError(w, err, "")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// Whitelist resolution is explicit and platform-aware. UUIDs never come from a
// client. Removal uses the saved identity and needs no external lookup.
func networkResolvePlayer(ctx context.Context, platform, name string) (minecraftNetworkPlayer, error) {
	var p minecraftNetworkPlayer
	address := "https://api.mojang.com/users/profiles/minecraft/" + name
	if platform == "bedrock" {
		address = "https://api.geysermc.org/v2/xbox/xuid/" + strings.ReplaceAll(name, " ", "%20")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return p, err
	}
	req.Header.Set("User-Agent", "JustVoxel/MinecraftNetwork")
	resp, err := pluginDownloadClient.Do(req)
	if err != nil {
		return p, instanceRuntimeFailure("Player identity lookup is unavailable.")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return p, instanceRuntimeFailure("Player identity could not be verified.")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8193))
	if err != nil || len(data) > 8192 {
		return p, errInstanceRecovery
	}
	if platform == "java" {
		var v struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if json.Unmarshal(data, &v) != nil || len(v.ID) != 32 || !networkJavaName.MatchString(v.Name) {
			return p, errInstanceRecovery
		}
		p.Name = v.Name
		p.UUID = v.ID[:8] + "-" + v.ID[8:12] + "-" + v.ID[12:16] + "-" + v.ID[16:20] + "-" + v.ID[20:]
	} else {
		var v struct {
			XUID string `json:"xuid"`
		}
		if json.Unmarshal(data, &v) != nil {
			return p, errInstanceRecovery
		}
		x, e := strconv.ParseUint(v.XUID, 10, 64)
		if e != nil || x == 0 {
			return p, errInstanceRecovery
		}
		h := fmt.Sprintf("%016x", x)
		p.UUID = "00000000-0000-0000-" + h[:4] + "-" + h[4:]
		p.Name = "." + strings.ReplaceAll(name, " ", "_")
	}
	if _, err = networkMergePlayers(nil, []minecraftNetworkPlayer{p}); err != nil {
		return p, err
	}
	return p, nil
}
func (s *server) minecraftNetworkWhitelist(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireAdministrator(w, r)
	if !ok {
		return
	}
	var req struct {
		Action   string `json:"action"`
		Platform string `json:"platform"`
		Name     string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if (req.Action != "add" && req.Action != "remove") || (req.Platform != "java" && req.Platform != "bedrock") || (req.Platform == "java" && !networkJavaName.MatchString(req.Name)) || (req.Platform == "bedrock" && !networkBedrockName.MatchString(req.Name)) {
		writeError(w, 400, "Choose Java Edition or Bedrock and enter a valid player name.")
		return
	}
	unlock, err := s.lockInstanceRuntime()
	if err != nil {
		instanceRuntimeError(w, err, "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	err = func() error {
		n, err := networkRead(instanceRuntimes)
		if err != nil {
			return err
		}
		if err = networkPending(instanceRuntimes); err != nil {
			return err
		}
		wanted := req.Name
		if req.Platform == "bedrock" {
			wanted = "." + strings.ReplaceAll(wanted, " ", "_")
		}
		entries := []minecraftNetworkPlayer{}
		found := false
		for _, p := range n.Whitelist {
			if strings.EqualFold(p.Name, wanted) {
				found = true
				if req.Action == "remove" {
					continue
				}
			}
			entries = append(entries, p)
		}
		if req.Action == "add" && !found {
			p, e := networkResolvePlayer(ctx, req.Platform, req.Name)
			if e != nil {
				return e
			}
			entries, err = networkMergePlayers(entries, []minecraftNetworkPlayer{p})
			if err != nil {
				return err
			}
		}
		next := n
		next.Whitelist = entries
		return networkPublishWhitelist(ctx, instanceRuntimes, n, next)
	}()
	err = errors.Join(err, unlock())
	if s.store != nil {
		_ = s.store.recordAuditEvent(actor, "minecraft_network_whitelist_"+req.Action, "minecraft_network", err == nil, req.Platform)
	}
	if err != nil {
		instanceRuntimeError(w, err, "")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func networkCheckReservations(b *instanceRuntimeBackend, records []instanceRuntimeRecord) error {
	n, err := networkRead(b)
	if err != nil {
		return err
	}
	if !n.Config.Enabled {
		return nil
	}
	for _, r := range records {
		if r.NetworkMember {
			if !networkRecordEligible(r.Runtime) || !networkContains(n.Config.Members, r.Runtime.ID) {
				return errInstanceRecovery
			}
			continue
		}
		for _, p := range instanceReservedPorts(r.Runtime) {
			if p == n.Config.JavaPort || p == n.Config.BedrockPort {
				return instanceRuntimeFailure("This port is reserved by Minecraft Network.")
			}
		}
	}
	return nil
}

func (b *instanceRuntimeBackend) networkDirectory() string {
	if b.networkRoot != "" {
		return b.networkRoot
	}
	return minecraftNetworkRoot
}

// Enrollment is the only automatic import boundary. Re-enrollment never
// resurrects a player the administrator removed from the network whitelist.
func networkEnrollWhitelist(state minecraftNetworkState, id string, load func() ([]minecraftNetworkPlayer, error)) (minecraftNetworkState, error) {
	if !instanceIDPattern.MatchString(id) {
		return state, errInstanceID
	}
	if networkContains(state.Imported, id) {
		return state, nil
	}
	incoming, err := load()
	if err != nil {
		return state, err
	}
	players, err := networkMergePlayers(state.Whitelist, incoming)
	if err != nil {
		return state, err
	}
	next := state
	next.Whitelist = players
	next.Imported = append(append([]string{}, state.Imported...), id)
	return next, nil
}
