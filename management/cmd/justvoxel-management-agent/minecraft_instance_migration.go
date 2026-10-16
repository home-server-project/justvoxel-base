package main

import (
	"path/filepath"
	"strings"
)

// The portable data contract may replace Minecraft settings; the selected
// destination retains identity, ports, limits, boot policy and backup ownership.
// This is consumed only by the root version publisher after cold data activation.
func (b *instanceRuntimeBackend) migrationPortableSettings(previous instanceRuntimeRecord, transaction string) (instanceRuntimeRecord, error) {
	next := previous
	if filepath.Dir(transaction) != filepath.Dir(previous.Storage.DataPath) || !strings.HasPrefix(filepath.Base(transaction), ".justvoxel-import-") {
		return next, errInstanceID
	}
	data, err := b.readPrivate(filepath.Join(transaction, "evidence.json"))
	if err != nil {
		return next, err
	}
	var evidence struct {
		Schema         int    `json:"schema"`
		InstanceID     string `json:"instance_id"`
		DataPath       string `json:"data_path"`
		Transaction    string `json:"transaction"`
		ConfigIdentity string `json:"config_identity"`
		DataIdentity   string `json:"data_identity"`
		StagedIdentity string `json:"staged_identity"`
		SourceIdentity string `json:"source_identity"`
		WasRunning     bool   `json:"was_running"`
	}
	if instanceDecode(data, &evidence) != nil || evidence.Schema != 1 || evidence.InstanceID != previous.Runtime.ID || evidence.DataPath != previous.Storage.DataPath || evidence.Transaction != transaction {
		return next, errInstanceRecovery
	}
	dir, _, _ := b.paths(previous.Runtime.ID)
	config, err := b.readPrivate(filepath.Join(dir, "config.json"))
	if err != nil || instanceHash(config) != evidence.ConfigIdentity {
		return next, errInstanceRecovery
	}
	data, err = b.readPrivate(filepath.Join(transaction, "portable.json"))
	if err != nil {
		return next, err
	}
	var portable struct {
		InstanceID       string `json:"instance_id"`
		GameMode         string `json:"game_mode"`
		Difficulty       string `json:"difficulty"`
		Whitelist        bool   `json:"whitelist"`
		EnforceWhitelist bool   `json:"enforce_whitelist"`
		MaxPlayers       int    `json:"max_players"`
		MOTD             string `json:"motd"`
	}
	if instanceDecode(data, &portable) != nil || portable.InstanceID != previous.Runtime.ID {
		return next, errInstanceRecovery
	}
	next.Runtime.GameMode = portable.GameMode
	next.Runtime.Difficulty = portable.Difficulty
	next.Runtime.Whitelist = portable.Whitelist
	next.Runtime.EnforceWhitelist = portable.EnforceWhitelist
	next.Runtime.MaxPlayers = portable.MaxPlayers
	next.Runtime.MOTD = portable.MOTD
	return next, nil
}
