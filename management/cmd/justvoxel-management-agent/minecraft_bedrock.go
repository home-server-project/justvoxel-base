package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"time"
)

var bedrockVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(\.[0-9]+)?$`)

func nativeBedrockValid(c minecraftInstanceRuntimeConfig) bool {
	return c.Software == "bedrock" && !c.Whitelist && !c.EnforceWhitelist && !c.BedrockEnabled && c.BedrockPort == 0 && c.JavaPort == 0 && c.JavaMemory == "" && c.LoaderVersion == "" && c.ImageTag == "stable" && c.VersionPolicy == "pinned" && bedrockVersionPattern.MatchString(c.Version) && c.GameMode != "spectator" && c.BedrockTCPPort > 0 && c.BedrockTCPPort <= 65535 && c.BedrockUDPStart > 0 && c.BedrockUDPEnd <= 65535 && c.MaxPlayers >= 1 && c.MaxPlayers <= 1000 && c.BedrockUDPEnd-c.BedrockUDPStart+1 >= c.MaxPlayers && c.BedrockUDPEnd-c.BedrockUDPStart+1 <= 1000 && !(c.BedrockTCPPort >= c.BedrockUDPStart && c.BedrockTCPPort <= c.BedrockUDPEnd)
}
func instanceSignalingPort(c minecraftInstanceRuntimeConfig) int {
	if c.Software == "bedrock" {
		return c.BedrockTCPPort
	}
	return c.JavaPort
}
func nativeBedrockRules(c minecraftInstanceRuntimeConfig, zone string, permanent bool) []instanceFirewallRule {
	rules := []instanceFirewallRule{{Zone: zone, Port: c.BedrockTCPPort, Protocol: "tcp", Permanent: permanent}}
	for port := c.BedrockUDPStart; port <= c.BedrockUDPEnd; port++ {
		rules = append(rules, instanceFirewallRule{Zone: zone, Port: port, Protocol: "udp", Permanent: permanent})
	}
	return rules
}
func closeBedrockPorts(held []io.Closer) {
	for _, c := range held {
		c.Close()
	}
}
func holdNativeBedrock(c minecraftInstanceRuntimeConfig) ([]io.Closer, error) {
	held := []io.Closer{}
	for _, rule := range nativeBedrockRules(c, "", false) {
		ports, err := instanceHoldFirewallPort(rule)
		if err != nil {
			closeBedrockPorts(held)
			return nil, err
		}
		held = append(held, ports...)
	}
	return held, nil
}

// Includes stopped reservations, both Java/Geyser protocols and complete native
// ranges. A native allocation never reuses another Minecraft listener's number.
func allocateNativeBedrock(records []instanceRuntimeRecord, players, tcp, start, end int) (int, int, int, error) {
	if players < 1 || players > 1000 {
		return 0, 0, 0, errors.New("Bedrock maximum players must be between 1 and 1000")
	}
	used := map[int]bool{}
	for _, r := range records {
		c := r.Runtime
		if c.Software == "bedrock" {
			used[c.BedrockTCPPort] = true
			for p := c.BedrockUDPStart; p <= c.BedrockUDPEnd; p++ {
				used[p] = true
			}
		} else {
			used[c.JavaPort] = true
			if c.BedrockEnabled {
				used[c.BedrockPort] = true
			}
		}
	}
	freeTCP := func(p int) bool {
		if used[p] {
			return false
		}
		held, err := instanceHoldPort(p)
		if err != nil {
			return false
		}
		for _, l := range held {
			l.Close()
		}
		return true
	}
	if tcp == 0 {
		for tcp = 19132; tcp <= 65535 && !freeTCP(tcp); tcp++ {
		}
	} else if !freeTCP(tcp) {
		return 0, 0, 0, errors.New("Bedrock TCP port conflicts with an existing listener")
	}
	if tcp < 1 || tcp > 65535 {
		return 0, 0, 0, errors.New("No Bedrock TCP port is available")
	}
	used[tcp] = true
	freeRange := func(a, b int) bool {
		if a < 1 || b > 65535 || b-a+1 < players || b-a+1 > 1000 {
			return false
		}
		held := []io.Closer{}
		defer func() { closeBedrockPorts(held) }()
		for p := a; p <= b; p++ {
			if used[p] {
				return false
			}
			conn, err := instanceHoldUDP(p)
			if err != nil {
				return false
			}
			held = append(held, conn)
		}
		return true
	}
	if start == 0 && end == 0 {
		for start = 19140; start+players-1 <= 65535; start++ {
			end = start + players - 1
			if freeRange(start, end) {
				return tcp, start, end, nil
			}
		}
	} else if freeRange(start, end) {
		return tcp, start, end, nil
	}
	return 0, 0, 0, errors.New("No safe contiguous Bedrock UDP gameplay range is available")
}
func (b *instanceRuntimeBackend) planNativeBedrock(ctx context.Context, m *adminSetupPlanMinecraft, players int) error {
	records, err := b.records()
	if err != nil {
		return err
	}
	legacy, err := b.identities().read()
	if err != nil {
		return err
	}
	if legacy.ID != "" {
		raw, e := runAdminDiscoveryHelper(ctx, "configuration")
		if e != nil {
			return instanceRuntimeFailure("Existing Minecraft listeners could not be verified")
		}
		var configuration adminConfigurationDiscovery
		if json.Unmarshal(raw, &configuration) != nil || !configuration.Configured || configuration.Minecraft.JavaPort < 1 {
			return instanceRuntimeFailure("Existing Minecraft listeners could not be verified")
		}
		m := configuration.Minecraft
		records = append(records, instanceRuntimeRecord{Runtime: minecraftInstanceRuntimeConfig{JavaPort: m.JavaPort, BedrockEnabled: m.BedrockEnabled, BedrockPort: m.BedrockPort}})
	}
	tcp, start, end, err := allocateNativeBedrock(records, players, m.BedrockTCPPort, m.BedrockUDPStart, m.BedrockUDPEnd)
	if err != nil {
		return err
	}
	m.BedrockTCPPort, m.BedrockUDPStart, m.BedrockUDPEnd = tcp, start, end
	return nil
}
func bedrockEnvironment(c minecraftInstanceRuntimeConfig) []byte {
	return []byte(fmt.Sprintf("EULA=TRUE\nVERSION=%s\nTRANSPORT=nethernet\nSERVER_PORT=%d\nSERVER_UDP_PORTS=%d-%d\nSERVER_NAME=\"%s\"\nGAMEMODE=%s\nMAX_PLAYERS=%d\nENABLE_LAN_VISIBILITY=FALSE\nALLOW_LIST=FALSE\nUID=%d\nGID=%d\nSTOP_SERVER_ANNOUNCE_DELAY=60\n", c.Version, c.BedrockTCPPort, c.BedrockUDPStart, c.BedrockUDPEnd, instanceRuntimeEnvEscape(c.MOTD), c.GameMode, c.MaxPlayers, c.UID, c.GID))
}

var bedrockPlayersPattern = regexp.MustCompile(`There are ([0-9]+)/([0-9]+) players online`)

func (b *instanceRuntimeBackend) bedrockPlayers(ctx context.Context, id string) (instancePlayers, error) {
	p := instancePlayers{ID: id, State: "unknown"}
	since := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := b.command(ctx, "podman", "exec", "minecraft-"+id, "send-command", "list"); err != nil {
		return p, instanceRuntimeFailure("Bedrock player query failed")
	}
	for attempt := 0; attempt < 5; attempt++ {
		raw, err := b.command(ctx, "podman", "logs", "--since", since, "--tail", "80", "minecraft-"+id)
		if err != nil || len(raw) > 65536 {
			return p, instanceRuntimeFailure("Bedrock player status is unavailable")
		}
		matches := bedrockPlayersPattern.FindAllStringSubmatch(string(raw), -1)
		if len(matches) > 0 {
			m := matches[len(matches)-1]
			online, e1 := strconv.Atoi(m[1])
			maximum, e2 := strconv.Atoi(m[2])
			if e1 == nil && e2 == nil && online >= 0 && online <= maximum && maximum <= 1000 {
				p.State = "online"
				p.Online = online
				p.Maximum = maximum
				return p, nil
			}
		}
		if err = b.waitMaintenance(ctx, 200*time.Millisecond); err != nil {
			return p, err
		}
	}
	return p, instanceRuntimeFailure("Bedrock player presence could not be verified; the server was not interrupted")
}

func (b *instanceRuntimeBackend) validateNewNativeBedrock(ctx context.Context, c minecraftInstanceRuntimeConfig) error {
	m := adminSetupPlanMinecraft{BedrockTCPPort: c.BedrockTCPPort, BedrockUDPStart: c.BedrockUDPStart, BedrockUDPEnd: c.BedrockUDPEnd}
	return b.planNativeBedrock(ctx, &m, c.MaxPlayers)
}

func (b *instanceRuntimeBackend) waitNativeBedrockReady(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	for {
		if _, err := b.bedrockPlayers(ctx, id); err == nil {
			return nil
		}
		state, err := b.observeRaw(ctx, id)
		if err != nil || state.State != "running" && state.State != "starting" {
			return instanceRuntimeFailure("Native Bedrock startup could not be verified")
		}
		if b.waitMaintenance(ctx, 2*time.Second) != nil {
			return instanceRuntimeFailure("Native Bedrock did not become ready before the safety timeout")
		}
	}
}
