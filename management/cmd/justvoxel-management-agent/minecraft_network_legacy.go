package main

// Compatibility for pre-migration journals only. There is no public engine
// option and normal generation never calls the historical generator below.
import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func networkRollbackIsLegacy(b *instanceRuntimeBackend, j minecraftNetworkJournal) bool {
	path := filepath.Join(b.quadlets, "justvoxel-picolobby.container")
	for _, f := range j.Files {
		if f.Path == path {
			return j.Before.Config.Enabled && f.Existed
		}
	}
	// Old whitelist journals did not record infrastructure files.
	_, err := b.readPrivate(path)
	return j.Before.Config.Enabled && err == nil
}

func networkWriteRollbackState(b *instanceRuntimeBackend, j minecraftNetworkJournal) error {
	if err := networkWriteState(b, j.Before); err != nil {
		return err
	}
	if !networkRollbackIsLegacy(b, j) {
		path := filepath.Join(b.networkDirectory(), "legacy-rollback.json")
		if _, err := b.readPrivate(path); errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return b.removePrivate(path)
	}
	identities, err := b.identities().listInstances("")
	if err != nil {
		return err
	}
	names := map[string]string{}
	for _, identity := range identities {
		names[identity.ID] = identity.DisplayName
	}
	generated := networkLegacyInfrastructure(j.Before, names)
	if err := b.writeJSON(filepath.Join(b.networkDirectory(), "generated.json"), generated, true); err != nil {
		return err
	}
	data, err := b.readPrivate(filepath.Join(b.networkDirectory(), "generated.json"))
	if err != nil {
		return err
	}
	return b.writeJSON(filepath.Join(b.networkDirectory(), "legacy-rollback.json"), map[string]any{"schema": 1, "generated_sha256": instanceHash(data)}, true)
}

// Only the obsolete startup guard is translated; all other historical bytes
// remain journal evidence. This makes an interrupted rollback retryable too.
func networkLegacyGuard(data []byte) []byte {
	return []byte(strings.ReplaceAll(string(data), minecraftNetworkHelper+" start-picolobby", minecraftNetworkHelper+" check"))
}

// Called while maintenance is still held, including failures before journaling.
func networkPreparePrevious(ctx context.Context, b *instanceRuntimeBackend) error {
	path := filepath.Join(b.quadlets, "justvoxel-picolobby.container")
	data, err := b.readPrivate(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := b.readPrivate(filepath.Join(b.networkDirectory(), "legacy-rollback.json")); errors.Is(err, os.ErrNotExist) {
		state, readErr := networkRead(b)
		if readErr != nil {
			return readErr
		}
		if err = networkWriteRollbackState(b, minecraftNetworkJournal{Before: state}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	normalized := networkLegacyGuard(data)
	if string(normalized) != string(data) {
		if err = b.writePrivate(path, normalized, true); err != nil {
			return err
		}
		_, err = b.command(ctx, "systemctl", "daemon-reload")
	}
	return err
}

func networkStartPrevious(ctx context.Context, b *instanceRuntimeBackend) error {
	units := []string{"justvoxel-velocity.service"}
	if _, err := b.readPrivate(filepath.Join(b.networkDirectory(), "legacy-rollback.json")); err == nil {
		if _, err := b.readPrivate(filepath.Join(b.quadlets, "justvoxel-picolobby.container")); err != nil {
			return err
		}
		units = append([]string{"justvoxel-picolobby.service"}, units...)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, err := b.command(ctx, "systemctl", append([]string{"--no-block", "start", "--"}, units...)...)
	return err
}

func networkLegacyInfrastructure(s minecraftNetworkState, names map[string]string) map[string][]byte {
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
	files := networkInfrastructure(s, names)
	delete(files, "velocity/plugins/justvoxel-lobby/lobby.conf")
	files["velocity/velocity.toml"] = []byte(velocity.String())
	files["picolobby/server.toml"] = []byte(lobby.String())
	return files
}
