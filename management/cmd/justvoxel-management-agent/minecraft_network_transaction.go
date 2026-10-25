package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Every changed backend must be stopped through its normal player-aware control
// before publication. No world data, software choice, or boot setting is changed.
func networkApply(ctx context.Context, b *instanceRuntimeBackend, c minecraftNetworkConfig) error {
	return networkApplyWithMaintenanceRelease(ctx, b, c, func() error { return nil })
}

// releaseMaintenance retains the Agent operation mutex and reset exclusions.
// It must be idempotent and retain any release error for final cleanup.
func networkApplyWithMaintenanceRelease(ctx context.Context, b *instanceRuntimeBackend, c minecraftNetworkConfig, releaseMaintenance func() error) (result error) {
	if err := networkPending(b); err != nil {
		return err
	}
	before, err := networkRead(b)
	if err != nil {
		return err
	}
	committed := false
	var stopped []string
	defer func() {
		if result == nil || committed || networkPending(b) != nil {
			return
		}
		// Any rollback has completed and reservations have closed before this
		// deferred restoration. Never start runtime against unsafe journal state.
		if before.Config.Enabled {
			prepareCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			prepareErr := networkPreparePrevious(prepareCtx, b)
			cancel()
			if prepareErr != nil {
				result = errors.Join(result, prepareErr)
				return
			}
		}
		if err := releaseMaintenance(); err != nil {
			result = errors.Join(result, instanceRuntimeFailure(networkMaintenanceReleaseMessage))
			return
		}
		if len(stopped) > 0 {
			restoreCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			if networkRestartStopped(restoreCtx, b, stopped) != nil {
				result = errors.Join(result, instanceRuntimeFailure("One or more previously running servers could not be restored. Check server status and use their normal Start controls."))
			}
		}
		if before.Config.Enabled {
			restartCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			restartErr := networkStartPrevious(restartCtx, b)
			result = errors.Join(result, restartErr)
		}
	}()
	records, err := b.records()
	if err != nil {
		return err
	}
	after := before
	after.Config = c
	after.Imported = append([]string{}, before.Imported...)
	if after.ForwardingSecret == "" {
		var secret [32]byte
		if _, err = rand.Read(secret[:]); err != nil {
			return err
		}
		after.ForwardingSecret = hex.EncodeToString(secret[:])
	}
	if len(after.FloodgateKey) == 0 {
		after.FloodgateKey = make([]byte, 16)
		if _, err = rand.Read(after.FloodgateKey); err != nil {
			return err
		}
	}
	selected := map[string]bool{}
	nextRecords := append([]instanceRuntimeRecord{}, records...)
	affected := map[string]bool{}
	var running []string
	for i, record := range records {
		id := record.Runtime.ID
		member := c.Enabled && networkContains(c.Members, id)
		if networkContains(c.Members, id) {
			if !networkRecordEligible(record.Runtime) || record.EnvironmentSHA256 == "" {
				return instanceRuntimeFailure("Only activated Paper and Purpur servers can join Minecraft Network.")
			}
			selected[id] = true
		}
		if record.NetworkMember || member {
			affected[id] = true
			if err = b.verifyLive(record); err != nil {
				return err
			}
			observed, e := b.observeRaw(ctx, id)
			if e != nil || (observed.State != "running" && observed.State != "stopped" && observed.State != "failed") {
				return instanceRuntimeFailure("Minecraft Network cannot safely change a selected server in its current state. Check server status and retry Apply.")
			}
			if observed.State == "running" {
				running = append(running, id)
			}
			if err = b.storage(ctx, "preflight", record); err != nil {
				return err
			}
		}
		nextRecords[i].NetworkMember = member
		if member {
			after, err = networkEnrollWhitelist(after, id, func() ([]minecraftNetworkPlayer, error) { return networkImportWhitelist(b, record) })
			if err != nil {
				return err
			}
		}
	}
	if len(selected) != len(c.Members) {
		return instanceRuntimeFailure("A selected Minecraft server is unavailable.")
	}
	// Saved standalone values remain reserved against other instances, but a
	// network member may hand its current public port to the network front gate.
	if c.Enabled {
		for _, record := range records {
			if networkContains(c.Members, record.Runtime.ID) {
				continue
			}
			for _, p := range instanceReservedPorts(record.Runtime) {
				if p == c.JavaPort || p == c.BedrockPort {
					return instanceRuntimeFailure("A Minecraft Network port is reserved by a standalone server.")
				}
			}
		}
	}
	snapshot, _, _, err := b.snapshot(ctx)
	if err != nil {
		return err
	}
	snapshot.NetworkTransition = true
	rendered, err := b.render(snapshot, nextRecords)
	if err != nil {
		return err
	}
	dir, err := b.privateDirectory(b.networkDirectory(), true)
	if err != nil {
		return err
	}
	dir.Close()
	// Each independent normal lifecycle retains its own player check and bounded
	// countdown. Indexed results keep bookkeeping deterministic without shared writes.
	sort.Strings(running)
	stopErrors := make([]error, len(running))
	var wave sync.WaitGroup
	for i, id := range running {
		wave.Add(1)
		go func(i int, id string) {
			defer wave.Done()
			stopErrors[i] = b.actionWithWarnings(ctx, id, "stop", true)
		}(i, id)
	}
	wave.Wait()
	// Even a failed stop request may have completed shutdown. Only confirmed
	// stopped servers belong to this operation's best-effort restoration set. A
	// successful normal Stop already confirmed shutdown itself.
	verifyCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	for i, id := range running {
		state, e := b.observeRaw(verifyCtx, id)
		if stopErrors[i] == nil || (e == nil && state.State == "stopped") {
			stopped = append(stopped, id)
		}
	}
	cancel()
	if err = errors.Join(stopErrors...); err != nil {
		return err
	}
	// Stop the front gate before changing any member or plugin configuration.
	// systemd's checked stop is bounded by the request context; no force kill.
	if before.Config.Enabled {
		if err = networkStopInfrastructure(ctx, b); err != nil {
			return instanceRuntimeFailure("Minecraft Network could not be stopped safely.")
		}
	}
	// Claim free public ports while preparing. No caller can borrow our locks.
	var held []io.Closer
	defer func() {
		for _, h := range held {
			h.Close()
		}
	}()
	public := []int{}
	if c.Enabled {
		public = []int{c.JavaPort, c.BedrockPort}
	} else {
		for _, r := range nextRecords {
			if affected[r.Runtime.ID] {
				public = append(public, instanceReservedPorts(r.Runtime)...)
			}
		}
	}
	seen := map[int]bool{}
	for _, p := range public {
		if seen[p] {
			continue
		}
		seen[p] = true
		listeners, e := instanceHoldPort(p)
		if e != nil {
			return e
		}
		for _, l := range listeners {
			held = append(held, l)
		}
		udp, e := instanceHoldUDP(p)
		if e != nil {
			return e
		}
		held = append(held, udp)
	}
	// Preparation is confined to new infrastructure. Downloads are verified before
	// any backend publication; it does not touch existing Minecraft data labels.
	if c.Enabled {
		if _, err = b.command(ctx, minecraftNetworkHelper, "prepare"); err != nil {
			return instanceRuntimeFailure("Verified Minecraft Network components are unavailable. Existing server definitions were preserved.")
		}
	}
	if encoded, e := json.Marshal(after); e != nil || len(encoded) > 256*1024 {
		return instanceRuntimeFailure("Minecraft Network whitelist exceeds the private state size limit.")
	}
	journal := minecraftNetworkJournal{Schema: 1, Before: before, After: after, Files: []minecraftNetworkFile{}}
	for _, f := range rendered {
		if !affected[f.ID] {
			continue
		}
		for i := range nextRecords {
			if nextRecords[i].Runtime.ID == f.ID {
				nextRecords[i].EnvironmentSHA256 = instanceHash(f.Environment)
				nextRecords[i].QuadletSHA256 = instanceHash(f.Quadlet)
			}
		}
		_, env, q := b.paths(f.ID)
		for _, item := range []struct {
			path string
			data []byte
		}{{env, f.Environment}, {q, f.Quadlet}} {
			if err = networkJournalFile(b, &journal, item.path, item.data); err != nil {
				return err
			}
		}
	}
	changes, nextFirewall, err := networkPlanFirewall(ctx, b, before, after, records, nextRecords)
	if err != nil {
		return err
	}
	journal.Changes = changes
	after.Firewall = nextFirewall["network"]
	journal.After = after
	for i := range nextRecords {
		if !affected[nextRecords[i].Runtime.ID] {
			continue
		}
		nextRecords[i].Firewall = nextFirewall[nextRecords[i].Runtime.ID]
		data, e := json.Marshal(nextRecords[i])
		if e != nil {
			return e
		}
		if err = networkJournalFile(b, &journal, filepath.Join(b.root, nextRecords[i].Runtime.ID, "config.json"), data); err != nil {
			return err
		}
	}
	for name, data := range networkQuadlets(after) {
		if !before.Config.Enabled {
			if _, e := os.Lstat(filepath.Join(b.quadlets, name)); !errors.Is(e, os.ErrNotExist) {
				return instanceRuntimeFailure("An unowned Minecraft Network definition already exists.")
			}
		}
		if err = networkJournalFile(b, &journal, filepath.Join(b.quadlets, name), data); err != nil {
			return err
		}
	}
	// On disable remove only the infrastructure definitions committed by this
	// feature. They are captured in the same durable recovery journal.
	if !after.Config.Enabled && before.Config.Enabled {
		for name := range networkQuadlets(before) {
			if err = networkJournalFile(b, &journal, filepath.Join(b.quadlets, name), nil); err != nil {
				return err
			}
		}
	}
	// Legacy definitions are removed by migration, never generated normally.
	legacyPath := filepath.Join(b.quadlets, "justvoxel-picolobby.container")
	if !before.Config.Enabled {
		if _, e := os.Lstat(legacyPath); !errors.Is(e, os.ErrNotExist) {
			return instanceRuntimeFailure("An unowned Minecraft Network definition already exists.")
		}
	}
	if err = networkJournalFile(b, &journal, legacyPath, nil); err != nil {
		return err
	}
	// Stage the complete unit set outside generator search paths before journal.
	stage, err := os.MkdirTemp(b.networkDirectory(), "stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	for _, f := range journal.Files {
		if strings.HasSuffix(f.Path, ".container") || strings.HasSuffix(f.Path, ".network") {
			if f.After != nil {
				if err = b.writePrivate(filepath.Join(stage, filepath.Base(f.Path)), f.After, false); err != nil {
					return err
				}
			}
		}
	}
	if after.Config.Enabled {
		if _, err = b.command(ctx, "env", "QUADLET_UNIT_DIRS="+stage, "/usr/lib/systemd/system-generators/podman-system-generator", "--dryrun"); err != nil {
			return instanceRuntimeFailure("Minecraft Network definitions could not be validated.")
		}
	}
	if encoded, e := json.Marshal(journal); e != nil || len(encoded) > 1024*1024 {
		return instanceRuntimeFailure("Minecraft Network publication exceeds the private recovery evidence limit.")
	}
	for _, record := range records {
		if !affected[record.Runtime.ID] {
			continue
		}
		state, e := b.observeRaw(ctx, record.Runtime.ID)
		if e != nil || (state.State != "stopped" && state.State != "failed") {
			return instanceRuntimeFailure("Minecraft Network could not confirm that every selected server stopped safely.")
		}
	}
	if err = b.writeJSON(filepath.Join(b.networkDirectory(), "pending.json"), journal, false); err != nil {
		return errInstanceRecovery
	}
	fail := func() error {
		recoveryCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		if networkRollbackDurable(recoveryCtx, b, journal) != nil {
			return instanceRuntimeFailure("Minecraft Network needs recovery. Use Retry recovery in Minecraft Network.")
		}
		return instanceRuntimeFailure("Minecraft Network changes failed and were rolled back. Server data was preserved.")
	}
	// Journal presence blocks boot/manual startup until all files and hashes agree.
	if err = networkWriteState(b, after); err != nil {
		return fail()
	}
	if _, err = b.command(ctx, minecraftNetworkHelper, "publish"); err != nil {
		return fail()
	}
	for _, f := range journal.Files {
		if err = networkPublishFile(b, f.Path, f.After); err != nil {
			return fail()
		}
	}
	for _, record := range nextRecords {
		if affected[record.Runtime.ID] {
			if _, err = b.command(ctx, minecraftNetworkHelper, "backend-publish", record.Runtime.ID); err != nil {
				return fail()
			}
		}
	}
	for _, change := range changes {
		if err = b.changeFirewall(ctx, change); err != nil {
			return fail()
		}
	}
	if _, err = b.command(ctx, "systemctl", "daemon-reload"); err != nil {
		return fail()
	}
	for _, r := range nextRecords {
		if affected[r.Runtime.ID] {
			if err = b.verifyHashes(r); err != nil {
				return fail()
			}
		}
	}
	if err = b.removePrivate(filepath.Join(b.networkDirectory(), "legacy-rollback.json")); err != nil {
		return fail()
	}
	if err = b.clearPending(filepath.Join(b.networkDirectory(), "pending.json")); err != nil {
		return errInstanceRecovery
	}
	committed = true
	// Infrastructure is optional and only started following an enabled commit.
	for _, h := range held {
		h.Close()
	}
	held = nil
	if err = releaseMaintenance(); err != nil {
		return instanceRuntimeFailure(networkMaintenanceReleaseMessage)
	}
	if c.Enabled {
		if _, err = b.command(ctx, "systemctl", "--no-block", "start", "--", "justvoxel-velocity.service"); err != nil {
			result = instanceRuntimeFailure(networkNotReadyMessage)
		} else if !networkWaitReady(ctx, b, c.JavaPort) {
			result = instanceRuntimeFailure(networkNotReadyMessage)
		}
	}
	if networkRestartStopped(ctx, b, stopped) != nil {
		result = errors.Join(result, instanceRuntimeFailure("Minecraft Network configuration was saved, but one or more previously running servers could not be started. Use their normal Start controls after checking server status."))
	}
	return result
}

const networkMaintenanceReleaseMessage = "Minecraft Network maintenance lock could not be released. Check Network status before retrying."

const networkNotReadyMessage = "Minecraft Network configuration was saved, but the network entrance did not become ready. Check Network status and retry Apply."

func networkWaitReady(parent context.Context, b *instanceRuntimeBackend, javaPort int) bool {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	// The attempt bound also makes the existing fake pause seam sufficient for tests.
	for attempt := 0; attempt <= 30; attempt++ {
		if ctx.Err() != nil {
			return false
		}
		if networkRuntimeReady(ctx, b, javaPort) {
			return true
		}
		if attempt == 30 || b.waitMaintenance(ctx, time.Second) != nil {
			return false
		}
	}
	return false
}

// Never issue a conflicting start or bypass the durable configuration guard.
// The normal Start lifecycle verifies the currently committed runtime again.
func networkRestartStopped(ctx context.Context, b *instanceRuntimeBackend, ids []string) error {
	var failures []error
	for _, id := range ids {
		if err := networkPending(b); err != nil {
			return err
		}
		state, err := b.observeRaw(ctx, id)
		if err == nil && state.State == "stopped" {
			err = b.action(ctx, id, "start")
		} else if err == nil {
			err = instanceRuntimeFailure("Previously running server is not safely stopped.")
		}
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}
func instanceReservedPorts(c minecraftInstanceRuntimeConfig) []int {
	if c.Software == "bedrock" {
		ports := []int{c.BedrockTCPPort}
		for p := c.BedrockUDPStart; p <= c.BedrockUDPEnd; p++ {
			ports = append(ports, p)
		}
		return ports
	}
	out := []int{c.JavaPort}
	if c.BedrockEnabled {
		out = append(out, c.BedrockPort)
	}
	return out
}
func networkJournalFile(b *instanceRuntimeBackend, j *minecraftNetworkJournal, path string, after []byte) error {
	before, err := b.readPrivate(path)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !exists && after == nil {
		return nil
	}
	j.Files = append(j.Files, minecraftNetworkFile{Path: path, Before: before, After: after, Existed: exists})
	return nil
}
func networkPublishFile(b *instanceRuntimeBackend, path string, data []byte) error {
	if data == nil {
		return b.removePrivate(path)
	}
	_, err := os.Lstat(path)
	return b.writePrivate(path, data, !errors.Is(err, os.ErrNotExist))
}
func networkWriteState(b *instanceRuntimeBackend, n minecraftNetworkState) error {
	encoded, err := json.Marshal(n)
	if err != nil || len(encoded) > 256*1024 {
		return instanceRuntimeFailure("Minecraft Network whitelist exceeds the private state size limit.")
	}
	identities, err := b.identities().listInstances("")
	if err != nil {
		return err
	}
	names := map[string]string{}
	for _, id := range identities {
		names[id.ID] = id.DisplayName
	}
	if err = b.writeJSON(filepath.Join(b.networkDirectory(), "generated.json"), networkInfrastructure(n, names), true); err != nil {
		return err
	}
	_, err = os.Lstat(filepath.Join(b.networkDirectory(), "state.json"))
	return b.writeJSON(filepath.Join(b.networkDirectory(), "state.json"), n, !errors.Is(err, os.ErrNotExist))
}
func networkRollback(ctx context.Context, b *instanceRuntimeBackend, j minecraftNetworkJournal) error {
	if err := networkRollbackDurable(ctx, b, j); err != nil {
		return err
	}
	if j.Before.Config.Enabled {
		if err := networkStartPrevious(ctx, b); err != nil {
			return err
		}
	}
	return nil
}

// Configure restores durable state under maintenance, then releases maintenance
// before its deferred runtime restoration. Recovery callers keep their behavior.
func networkRollbackDurable(ctx context.Context, b *instanceRuntimeBackend, j minecraftNetworkJournal) error {
	// Refuse paths and contents not attributable to this bounded feature journal.
	if err := networkValidateJournal(b, j); err != nil {
		return err
	}
	for _, f := range j.Files {
		current, err := b.readPrivate(f.Path)
		if errors.Is(err, os.ErrNotExist) {
			if f.Existed && f.After != nil {
				return errInstanceRecovery
			}
		} else if err != nil || (instanceHash(current) != instanceHash(f.Before) && instanceHash(current) != instanceHash(f.After)) {
			// A previous recovery attempt may already have translated the one
			// removed legacy startup guard. Accept only that exact translation.
			if err != nil || f.Path != filepath.Join(b.quadlets, "justvoxel-picolobby.container") || instanceHash(current) != instanceHash(networkLegacyGuard(f.Before)) {
				return errInstanceRecovery
			}
		}
	}
	if err := networkWriteRollbackState(b, j); err != nil {
		return err
	}
	if _, err := b.command(ctx, minecraftNetworkHelper, "publish"); err != nil {
		return err
	}
	for i := len(j.Files) - 1; i >= 0; i-- {
		f := j.Files[i]
		data := f.Before
		if f.Existed && f.Path == filepath.Join(b.quadlets, "justvoxel-picolobby.container") {
			data = networkLegacyGuard(data)
		}
		if !f.Existed {
			data = nil
		}
		if err := networkPublishFile(b, f.Path, data); err != nil {
			return err
		}
	}
	for _, f := range j.Files {
		if filepath.Base(f.Path) == "config.json" {
			id := filepath.Base(filepath.Dir(f.Path))
			if _, err := b.command(ctx, minecraftNetworkHelper, "backend-publish", id); err != nil {
				return err
			}
		}
	}
	for i := len(j.Changes) - 1; i >= 0; i-- {
		c := j.Changes[i]
		c.Add = !c.Add
		if err := b.changeFirewall(ctx, c); err != nil {
			return err
		}
	}
	if j.Before.Config.Enabled {
		if err := networkPreparePrevious(ctx, b); err != nil {
			return err
		}
	}
	if _, err := b.command(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := b.clearPending(filepath.Join(b.networkDirectory(), "pending.json")); err != nil {
		return err
	}
	return nil
}
func networkValidateJournal(b *instanceRuntimeBackend, j minecraftNetworkJournal) error {
	if j.Schema != 1 || j.Before.Schema != 1 || j.After.Schema != 1 || networkValidateConfig(j.Before.Config) != nil || networkValidateConfig(j.After.Config) != nil || len(j.Files) > 90 {
		return errInstanceRecovery
	}
	seen := map[string]bool{}
	for _, f := range j.Files {
		allowed := false
		for _, name := range []string{"justvoxel-velocity.container", "justvoxel-picolobby.container", minecraftNetworkName + ".network"} {
			if f.Path == filepath.Join(b.quadlets, name) {
				allowed = true
			}
		}
		for _, id := range append(append([]string{}, j.Before.Config.Members...), j.After.Config.Members...) {
			_, env, q := b.paths(id)
			if f.Path == env || f.Path == q || f.Path == filepath.Join(b.root, id, "config.json") {
				allowed = true
			}
		}
		if !allowed || seen[f.Path] || (!f.Existed && f.Before != nil) {
			return errInstanceRecovery
		}
		seen[f.Path] = true
	}
	for _, c := range j.Changes {
		if !instanceFirewallZonePattern.MatchString(c.Rule.Zone) || c.Rule.Port < 1024 || c.Rule.Port > 65535 || (c.Rule.protocol() != "tcp" && c.Rule.protocol() != "udp") {
			return errInstanceRecovery
		}
	}
	return nil
}
func networkRecover(ctx context.Context, b *instanceRuntimeBackend) error {
	data, err := b.readPrivate(filepath.Join(b.networkDirectory(), "pending.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var j minecraftNetworkJournal
	if instanceDecode(data, &j) != nil {
		return errInstanceRecovery
	}
	// No restoration while any affected backend is running or has a queued job.
	for _, f := range j.Files {
		if filepath.Base(f.Path) == "config.json" {
			id := filepath.Base(filepath.Dir(f.Path))
			if !instanceIDPattern.MatchString(id) {
				return errInstanceRecovery
			}
			state, e := b.observeRaw(ctx, id)
			if e != nil || (state.State != "stopped" && state.State != "failed") {
				return instanceRuntimeFailure("Stop the affected servers before retrying Minecraft Network recovery.")
			}
		}
	}
	if err = networkValidateJournal(b, j); err != nil {
		return err
	}
	if err = networkStopInfrastructure(ctx, b); err != nil {
		return err
	}
	return networkRollback(ctx, b, j)
}

// Batch ownership avoids removing a firewall rule when a backend hands the same
// public port to Velocity. Existing unowned rules are never claimed or removed.
func networkPlanFirewall(ctx context.Context, b *instanceRuntimeBackend, old, next minecraftNetworkState, records, targets []instanceRuntimeRecord) ([]instanceFirewallChange, map[string][]instanceFirewallRule, error) {
	output, err := b.command(ctx, "firewall-cmd", "--get-default-zone")
	zone := strings.TrimSpace(string(output))
	if err != nil || !instanceFirewallZonePattern.MatchString(zone) {
		return nil, nil, errInstanceRecovery
	}
	key := func(r instanceFirewallRule) string {
		return fmt.Sprintf("%s/%d/%s/%t", r.Zone, r.Port, r.protocol(), r.Permanent)
	}
	owned := map[string]instanceFirewallRule{}
	for _, r := range old.Firewall {
		owned[key(r)] = r
	}
	for _, r := range records {
		if r.NetworkMember || (next.Config.Enabled && networkContains(next.Config.Members, r.Runtime.ID)) {
			for _, rule := range r.Firewall {
				owned[key(rule)] = rule
			}
		}
	}
	desired := map[string][]instanceFirewallRule{}
	add := func(id string, c minecraftInstanceRuntimeConfig) {
		for _, permanent := range []bool{true, false} {
			desired[id] = append(desired[id], instanceFirewallRule{Zone: zone, Port: c.JavaPort, Permanent: permanent, Protocol: "tcp"})
			if c.BedrockEnabled {
				for _, proto := range []string{"tcp", "udp"} {
					desired[id] = append(desired[id], instanceFirewallRule{Zone: zone, Port: c.BedrockPort, Permanent: permanent, Protocol: proto})
				}
			}
		}
	}
	if next.Config.Enabled {
		add("network", minecraftInstanceRuntimeConfig{JavaPort: next.Config.JavaPort, BedrockPort: next.Config.BedrockPort, BedrockEnabled: true})
	}
	for _, r := range targets {
		if !r.NetworkMember && ((old.Config.Enabled && networkContains(old.Config.Members, r.Runtime.ID)) || (next.Config.Enabled && networkContains(next.Config.Members, r.Runtime.ID))) {
			add(r.Runtime.ID, r.Runtime)
		}
	}
	wanted := map[string]bool{}
	result := map[string][]instanceFirewallRule{}
	changes := []instanceFirewallChange{}
	ids := []string{}
	for id := range desired {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		for _, rule := range desired[id] {
			k := key(rule)
			wanted[k] = true
			present, e := b.queryFirewall(ctx, rule)
			if e != nil {
				return nil, nil, e
			}
			_, ours := owned[k]
			if !present {
				changes = append(changes, instanceFirewallChange{Rule: rule, Add: true})
			}
			if ours || !present {
				result[id] = append(result[id], rule)
			}
		}
	}
	keys := []string{}
	for k := range owned {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !wanted[k] {
			rule := owned[k]
			present, e := b.queryFirewall(ctx, rule)
			if e != nil {
				return nil, nil, e
			}
			if present {
				changes = append(changes, instanceFirewallChange{Rule: rule, Add: false})
			}
		}
	}
	return changes, result, nil
}
func networkQuadlets(n minecraftNetworkState) map[string][]byte {
	if !n.Config.Enabled {
		return map[string][]byte{}
	}
	unit := "[Unit]\nWants=network-online.target\nAfter=network-online.target\n[Container]\nNetwork=" + minecraftNetworkName + ".network\n"
	guard := "\n[Service]\nExecStartPre=" + minecraftNetworkHelper + " check\nRestart=always\nRestartSec=10\nTimeoutStartSec=900\nTimeoutStopSec=240\n[Install]\nWantedBy=multi-user.target\n"
	return map[string][]byte{
		minecraftNetworkName + ".network": []byte("[Network]\nNetworkName=" + minecraftNetworkName + "\nDriver=bridge\n"),
		"justvoxel-velocity.container":    []byte(unit + "Image=docker.io/itzg/mc-proxy:java25\nContainerName=justvoxel-velocity\nNetworkAlias=justvoxel-velocity\nEnvironment=TYPE=VELOCITY\nEnvironment=MEMORY=512m\nEnvironment=ENABLE_RCON=false\nEnvironment=SKIP_CHOWN_DATA=true\nVolume=" + minecraftNetworkRoot + "/velocity:/server:Z\n" + fmt.Sprintf("PublishPort=%d:25565/tcp\nPublishPort=%d:19132/tcp\nPublishPort=%d:19132/udp\n", n.Config.JavaPort, n.Config.BedrockPort, n.Config.BedrockPort) + "LogDriver=journald\n" + strings.Replace(guard, " check\n", " start-velocity\n", 1)),
	}
}
func networkPublishWhitelist(ctx context.Context, b *instanceRuntimeBackend, before, after minecraftNetworkState) error {
	// Finish the one-time infrastructure migration through Apply. Whitelist
	// publication alone must never mix a legacy Quadlet with the new lobby.
	if _, err := b.readPrivate(filepath.Join(b.quadlets, "justvoxel-picolobby.container")); err == nil {
		return instanceRuntimeFailure("Apply Minecraft Network configuration to finish the lobby update before changing its whitelist.")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir, err := b.privateDirectory(b.networkDirectory(), true)
	if err != nil {
		return err
	}
	dir.Close()
	j := minecraftNetworkJournal{Schema: 1, Before: before, After: after, Files: []minecraftNetworkFile{}}
	if before.Config.Enabled {
		if _, err = b.command(ctx, "systemctl", "stop", "--", "justvoxel-velocity.service"); err != nil {
			return instanceRuntimeFailure("Network whitelist could not be enforced; the proxy could not be stopped.")
		}
	}
	if err = b.writeJSON(filepath.Join(b.networkDirectory(), "pending.json"), j, false); err != nil {
		return errInstanceRecovery
	}
	if err = networkWriteState(b, after); err == nil {
		_, err = b.command(ctx, minecraftNetworkHelper, "publish")
	}
	if err != nil {
		recoveryCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		if networkRollback(recoveryCtx, b, j) != nil {
			return errInstanceRecovery
		}
		return instanceRuntimeFailure("Network whitelist change was rolled back.")
	}
	if err = b.clearPending(filepath.Join(b.networkDirectory(), "pending.json")); err != nil {
		return err
	}
	if after.Config.Enabled {
		_, err = b.command(ctx, "systemctl", "--no-block", "start", "--", "justvoxel-velocity.service")
	}
	return err
}

// First-enable recovery must also handle a crash before daemon-reload. A unit
// that has never been generated cannot be stopped; confirm no live infrastructure
// container remains instead of treating a missing service as permanent failure.
func networkStopInfrastructure(ctx context.Context, b *instanceRuntimeBackend) error {
	for _, name := range []string{"justvoxel-velocity", "justvoxel-picolobby"} {
		output, err := b.command(ctx, "systemctl", "show", "--property=LoadState,ActiveState,SubState", "--", name+".service")
		if err != nil {
			return instanceRuntimeFailure("Minecraft Network service state is unavailable.")
		}
		loaded := false
		absent := false
		for _, line := range strings.Split(string(output), "\n") {
			if line == "LoadState=loaded" {
				loaded = true
			}
			if line == "LoadState=not-found" {
				absent = true
			}
		}
		if !loaded && !absent {
			return errInstanceRecovery
		}
		if loaded {
			if _, err = b.command(ctx, "systemctl", "stop", "--", name+".service"); err != nil {
				return err
			}
		}
		output, err = b.command(ctx, "podman", "ps", "--all", "--filter", "name=^"+name+"$", "--format", "{{.Names}}")
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(output)) == "" {
			continue
		}
		if strings.TrimSpace(string(output)) != name {
			return errInstanceRecovery
		}
		output, err = b.command(ctx, "podman", "inspect", "--type", "container", "--format", "{{.State.Status}}", name)
		if err != nil {
			return err
		}
		state := strings.TrimSpace(string(output))
		if state != "exited" && state != "created" && state != "stopped" {
			return instanceRuntimeFailure("Minecraft Network is still running; recovery has not changed server definitions.")
		}
	}
	return nil
}
