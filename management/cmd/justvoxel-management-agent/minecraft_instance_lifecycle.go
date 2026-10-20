package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var instanceSHA256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var instanceFirewallZonePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var instancePlayerListPattern = regexp.MustCompile(`^There are ([0-9]+) of a max of ([0-9]+) players online:?[ \t]*([^\r\n]*)$`)

type instanceRuntimeStatus struct {
	ID          string `json:"id"`
	State       string `json:"state"`
	StartOnBoot bool   `json:"start_on_boot"`
	Message     string `json:"message,omitempty"`
}

func (b *instanceRuntimeBackend) observeRaw(ctx context.Context, id string) (instanceRuntimeStatus, error) {
	result := instanceRuntimeStatus{ID: id, State: "unavailable"}
	if !instanceIDPattern.MatchString(id) {
		return result, errInstanceID
	}
	output, err := b.command(ctx, "systemctl", "show", "--property=LoadState,ActiveState,SubState", "--", "minecraft-"+id+".service")
	if err != nil {
		return result, instanceRuntimeFailure("Systemd instance state is unavailable.")
	}
	props := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			props[key] = value
		}
	}
	switch props["LoadState"] {
	case "not-found":
		props["ActiveState"] = "inactive"
	case "loaded":
	default:
		return result, instanceRuntimeFailure("Generated instance unit is unavailable or invalid.")
	}
	switch props["ActiveState"] {
	case "activating":
		result.State = "starting"
		return result, nil
	case "deactivating":
		result.State = "stopping"
		return result, nil
	case "failed":
		// Confirm the container is not still running before permitting recovery.
	case "active", "inactive":
	default:
		return result, instanceRuntimeFailure("Instance service state is unavailable.")
	}
	output, err = b.command(ctx, "podman", "ps", "--all", "--filter", "name=^minecraft-"+id+"$", "--format", "{{.Names}}")
	if err != nil {
		return result, instanceRuntimeFailure("Instance container state is unavailable.")
	}
	containerState := "absent"
	containerExit := 0
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 1 || fields[0] != "minecraft-"+id {
			return result, instanceRuntimeFailure("Instance container evidence is invalid.")
		}
		if containerState != "absent" {
			return result, instanceRuntimeFailure("Duplicate instance container evidence.")
		}
		stateOutput, err := b.command(ctx, "podman", "inspect", "--type", "container", "--format", "{{.State.Status}} {{.State.ExitCode}}", "minecraft-"+id)
		if err != nil {
			return result, instanceRuntimeFailure("Instance container inspection is unavailable.")
		}
		stateFields := strings.Fields(string(stateOutput))
		if len(stateFields) != 2 {
			return result, instanceRuntimeFailure("Instance container inspection is invalid.")
		}
		containerExit, err = strconv.Atoi(stateFields[1])
		if err != nil || containerExit < 0 || containerExit > 255 {
			return result, instanceRuntimeFailure("Instance container exit status is invalid.")
		}
		containerState = stateFields[0]
	}
	if props["LoadState"] == "not-found" {
		if containerState != "absent" {
			return result, instanceRuntimeFailure("Container exists without its configured service.")
		}
		result.State = "configured"
		return result, nil
	}
	if props["ActiveState"] == "failed" {
		if containerState == "running" || containerState == "paused" || containerState == "stopping" {
			return result, instanceRuntimeFailure("Failed service still has a live container.")
		}
		result.State = "failed"
		return result, nil
	}
	if props["ActiveState"] == "active" {
		if containerState != "running" {
			result.State = "failed"
		} else {
			result.State = "running"
		}
	} else {
		switch containerState {
		case "absent", "exited", "stopped", "created":
			result.State = "stopped"
			if containerState == "exited" && containerExit != 0 {
				result.State = "failed"
			}
		default:
			return result, instanceRuntimeFailure("Container state disagrees with the stopped service.")
		}
	}
	return result, nil
}
func (b *instanceRuntimeBackend) observe(ctx context.Context, id string) (instanceRuntimeStatus, error) {
	status := instanceRuntimeStatus{ID: id, State: "unavailable"}
	record, err := b.readRecord(id)
	if err != nil {
		return status, err
	}
	status.StartOnBoot = record.Runtime.StartOnBoot
	pending, err := b.pending(id)
	if err != nil || pending {
		status.State = "recovery_required"
		status.Message = errInstanceRecovery.Error()
		return status, nil
	}
	if record.EnvironmentSHA256 == "" {
		raw, err := b.observeRaw(ctx, id)
		if err != nil {
			return status, err
		}
		if raw.State != "configured" {
			status.State = "invalid_configuration"
			status.Message = "Uncommitted runtime evidence exists."
			return status, nil
		}
		status.State = "configured"
		return status, nil
	}
	if err = b.verifyLive(record); err != nil {
		status.State = "invalid_configuration"
		status.Message = "Committed runtime files are unavailable or inconsistent."
		return status, nil
	}
	raw, err := b.observeRaw(ctx, id)
	raw.StartOnBoot = status.StartOnBoot
	if err == nil && raw.State == "configured" {
		raw.State = "unavailable"
		err = instanceRuntimeFailure("Committed instance has no generated systemd unit.")
	}
	if err != nil {
		raw.Message = err.Error()
		return raw, err
	}
	if err = b.storage(ctx, "validate", record); err != nil {
		raw.State = "unavailable"
		raw.Message = "Configured instance storage is unavailable."
		return raw, err
	}
	return raw, nil
}

type instancePlayersConfirmation struct {
	Players instancePlayers
	Names   []string
}

func (e *instancePlayersConfirmation) Error() string {
	return "Players are online; explicit interruption confirmation is required."
}

func (b *instanceRuntimeBackend) action(ctx context.Context, id, action string) error {
	return b.actionWithWarnings(ctx, id, action, false)
}
func (b *instanceRuntimeBackend) actionWithWarnings(ctx context.Context, id, action string, warnings bool) error {
	if !instanceIDPattern.MatchString(id) {
		return errInstanceID
	}
	if action != "start" && action != "stop" && action != "restart" {
		return instanceRuntimeFailure("Invalid instance action.")
	}
	record, err := b.readRecord(id)
	if err != nil {
		return err
	}
	if err = b.verifyLive(record); err != nil {
		return err
	}
	if err = b.storage(ctx, "validate", record); err != nil {
		return err
	}
	state, err := b.observeRaw(ctx, id)
	if err != nil {
		return err
	}
	var players instancePlayers
	if (action == "stop" || action == "restart") && state.State == "running" {
		rawPlayers, err := b.rcon(ctx, id, "list")
		if err != nil {
			return err
		}
		players, err = parseInstancePlayers(id, rawPlayers)
		if err != nil {
			return err
		}
		if players.Online > 0 && !warnings {
			names := instancePlayerListPattern.FindStringSubmatch(strings.TrimSpace(string(rawPlayers)))[3]
			return &instancePlayersConfirmation{Players: players, Names: strings.Split(names, ", ")}
		}
	}
	// Reservation validation is needed before startup, not to refuse a safe
	// graceful stop that can reduce an existing resource overcommit.
	if action != "stop" {
		snapshot, _, _, err := b.snapshot(ctx)
		if err != nil {
			return err
		}
		records, err := b.records()
		if err != nil {
			return err
		}
		for _, r := range records {
			pending, err := b.pending(r.Runtime.ID)
			if err != nil || pending {
				return errInstanceRecovery
			}
		}
		if _, err = b.render(snapshot, records); err != nil {
			return err
		}
		if err = b.admitMemory(ctx, snapshot.TotalMemoryMiB, records, record.Runtime, false); err != nil {
			return err
		}
	}
	unit := "minecraft-" + id + ".service"
	if action == "stop" || action == "restart" {
		switch state.State {
		case "running":
			if players.Online > 0 {
				if err = b.stopWithWarnings(ctx, id); err != nil {
					return err
				}
			} else {
				stopCtx, cancel := context.WithTimeout(ctx, 150*time.Second)
				_, err = b.command(stopCtx, "systemctl", "stop", "--", unit)
				cancel()
				if err != nil {
					return instanceRuntimeFailure("Instance graceful stop failed or exceeded the safety timeout; normal graceful shutdown continues.")
				}
			}
			stopped, err := b.observeRaw(ctx, id)
			if err != nil || stopped.State != "stopped" {
				return instanceRuntimeFailure("Instance graceful shutdown could not be confirmed.")
			}
		case "failed":
			// observeRaw verified that no live container remains behind the failed unit.
			if _, err = b.command(ctx, "systemctl", "stop", "--", unit); err != nil {
				return instanceRuntimeFailure("Failed instance unit could not be stopped.")
			}
			stopped, err := b.observeRaw(ctx, id)
			if err != nil || stopped.State != "stopped" {
				return instanceRuntimeFailure("Instance graceful shutdown could not be confirmed.")
			}
		case "stopped":
		default:
			return instanceRuntimeFailure("Instance player presence cannot be safely determined in its current state.")
		}
		if action == "stop" {
			return nil
		}
	} else if state.State == "running" {
		return nil
	} else if state.State != "stopped" && state.State != "failed" {
		return instanceRuntimeFailure("Instance cannot be started in its current state.")
	}
	if record.Runtime.BedrockEnabled {
		heldUDP, e := instanceHoldUDP(record.Runtime.BedrockPort)
		if e != nil {
			return e
		}
		heldUDP.Close()
	}
	// A stopped instance's reservation does not prove the host port is still free.
	held, err := instanceHoldPort(record.Runtime.JavaPort)
	if err != nil {
		return err
	}
	for _, l := range held {
		l.Close()
	}
	if _, err = b.command(ctx, "systemctl", "--no-block", "start", "--", unit); err != nil {
		return instanceRuntimeFailure("Instance start request failed.")
	}
	return nil
}
func instanceExitOne(err error) bool {
	var exit interface{ ExitCode() int }
	return errors.As(err, &exit) && exit.ExitCode() == 1
}
func (b *instanceRuntimeBackend) queryFirewall(ctx context.Context, r instanceFirewallRule) (bool, error) {
	args := []string{"--zone=" + r.Zone}
	if r.Permanent {
		args = append(args, "--permanent")
	}
	args = append(args, "--query-port="+strconv.Itoa(r.Port)+"/"+r.protocol())
	output, err := b.command(ctx, "firewall-cmd", args...)
	value := strings.TrimSpace(string(output))
	if err == nil && value == "yes" {
		return true, nil
	}
	if instanceExitOne(err) && value == "no" {
		return false, nil
	}
	return false, instanceRuntimeFailure("Instance firewall state is unavailable.")
}
func (b *instanceRuntimeBackend) planFirewall(ctx context.Context, old, next instanceRuntimeRecord) ([]instanceFirewallChange, []instanceFirewallRule, error) {
	changes := []instanceFirewallChange{}
	owned := []instanceFirewallRule{}
	output, err := b.command(ctx, "firewall-cmd", "--get-default-zone")
	zone := strings.TrimSpace(string(output))
	if err != nil || !instanceFirewallZonePattern.MatchString(zone) {
		return nil, nil, instanceRuntimeFailure("Instance firewall zone is unavailable.")
	}
	desired := []instanceFirewallRule{}
	for _, permanent := range []bool{true, false} {
		desired = append(desired, instanceFirewallRule{Zone: zone, Port: next.Runtime.JavaPort, Permanent: permanent})
		if next.Runtime.BedrockEnabled {
			desired = append(desired, instanceFirewallRule{Zone: zone, Port: next.Runtime.BedrockPort, Permanent: permanent, Protocol: "udp"})
		}
	}
	for _, rule := range old.Firewall {
		if instanceRuleForRuntime(rule, next.Runtime) {
			owned = append(owned, rule)
			continue
		}
		present, err := b.queryFirewall(ctx, rule)
		if err != nil {
			return nil, nil, err
		}
		if present {
			changes = append(changes, instanceFirewallChange{Rule: rule, Add: false})
		}
	}
	for _, rule := range desired {
		present, err := b.queryFirewall(ctx, rule)
		if err != nil {
			return nil, nil, err
		}
		if !present {
			changes = append(changes, instanceFirewallChange{Rule: rule, Add: true})
			if !instanceRuleContains(owned, rule) {
				owned = append(owned, rule)
			}
		}
	}
	return changes, owned, nil
}
func instanceRuleContains(rules []instanceFirewallRule, rule instanceFirewallRule) bool {
	for _, r := range rules {
		if r.Zone == rule.Zone && r.Port == rule.Port && r.Permanent == rule.Permanent && r.protocol() == rule.protocol() {
			return true
		}
	}
	return false
}
func (b *instanceRuntimeBackend) changeFirewall(ctx context.Context, c instanceFirewallChange) error {
	present, err := b.queryFirewall(ctx, c.Rule)
	if err != nil {
		return err
	}
	if present == c.Add {
		return nil
	}
	args := []string{"--zone=" + c.Rule.Zone}
	if c.Rule.Permanent {
		args = append(args, "--permanent")
	}
	action := "--remove-port="
	if c.Add {
		action = "--add-port="
	}
	args = append(args, action+strconv.Itoa(c.Rule.Port)+"/"+c.Rule.protocol())
	if _, err = b.command(ctx, "firewall-cmd", args...); err != nil {
		return instanceRuntimeFailure("Instance firewall update failed.")
	}
	present, err = b.queryFirewall(ctx, c.Rule)
	if err != nil || present != c.Add {
		return instanceRuntimeFailure("Instance firewall update could not be verified.")
	}
	return nil
}

// Creation leaves a complete configured reservation even if activation fails.
// Retrying apply uses this immutable ID rather than allocating another world.
func (b *instanceRuntimeBackend) create(ctx context.Context, c minecraftInstanceRuntimeConfig, name string, snapshot minecraftInstanceRuntimeContext) (string, error) {
	return b.createTracked(ctx, c, name, snapshot, nil)
}

// Setup records the generated identity before registration. Both entry points
// use exactly the same validation, publication and activation transaction.
func (b *instanceRuntimeBackend) createTracked(ctx context.Context, c minecraftInstanceRuntimeConfig, name string, snapshot minecraftInstanceRuntimeContext, track func(instanceRuntimeRecord) error) (string, error) {
	if err := b.requireNoStartOver(); err != nil {
		return "", err
	}
	if _, err := validateInstanceName(name); err != nil {
		return "", err
	}
	registry, err := b.identities().openDirectory(true)
	if err != nil {
		return "", err
	}
	registry.Close()
	records, err := b.records()
	if err != nil {
		return "", err
	}
	for _, r := range records {
		p, err := b.pending(r.Runtime.ID)
		if err != nil || p {
			return "", errInstanceRecovery
		}
	}
	if len(records) > 0 {
		if _, err = b.render(snapshot, records); err != nil {
			return "", err
		}
	}
	var entropy [6]byte
	if _, err = io.ReadFull(rand.Reader, entropy[:]); err != nil {
		return "", err
	}
	c.ID = "jv-" + hex.EncodeToString(entropy[:])
	next := instanceRuntimeRecord{SchemaVersion: 1, Runtime: c, Storage: instanceStorageFor(snapshot, c.ID), Backups: instanceBackupsFor(snapshot, c.ID), Firewall: []instanceFirewallRule{}}
	candidates := instanceReplaceRecord(records, next)
	if c.StartOnBoot {
		if err = b.admitMemory(ctx, snapshot.TotalMemoryMiB, candidates, c, true); err != nil {
			return "", err
		}
	}
	if _, err = b.renderCandidate(snapshot, candidates, c.ID); err != nil {
		return "", err
	}
	if err = b.storage(ctx, "preflight-new", next); err != nil {
		return "", err
	}
	if err = b.validateSoftware(ctx, c); err != nil {
		return "", err
	}
	held, err := instanceHoldPort(c.JavaPort)
	if err != nil {
		return "", err
	}
	for _, l := range held {
		l.Close()
	}
	if track != nil {
		if err = track(next); err != nil {
			return "", err
		}
	}
	registered, err := b.identities().registerLogicalInstance(strings.TrimPrefix(c.ID, "jv-"), true, c.Software, name, "")
	if err != nil {
		return "", err
	}
	id := registered.ID
	root, err := b.privateDirectory(b.root, true)
	if err != nil {
		return id, instanceRuntimeFailure("Instance was registered but runtime configuration could not be persisted.")
	}
	root.Close()
	dir, _, _ := b.paths(id)
	f, err := b.privateDirectory(dir, true)
	if err != nil {
		return id, err
	}
	f.Close()
	if err = b.writeJSON(filepath.Join(dir, "config.json"), next, false); err != nil {
		return id, err
	}
	if err = b.apply(ctx, next, candidates, snapshot); err != nil {
		return id, err
	}
	return id, nil
}
