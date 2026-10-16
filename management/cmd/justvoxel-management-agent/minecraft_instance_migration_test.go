package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMigrationPortableDataCannotReplaceDestinationIdentityOrRuntimeOwnership(t *testing.T) {
	f := newInstanceActivationFixture(t)
	b := f.backend
	previous, err := b.readRecord(f.configs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	dir, _, _ := b.paths(previous.Runtime.ID)
	config, err := b.readPrivate(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(previous.Storage.DataPath), 0700); err != nil {
		t.Fatal(err)
	}
	parent, err := b.privateDirectory(filepath.Dir(previous.Storage.DataPath), true)
	if err != nil {
		t.Fatal(err)
	}
	parent.Close()
	tx := filepath.Join(filepath.Dir(previous.Storage.DataPath), ".justvoxel-import-test")
	d, err := b.privateDirectory(tx, true)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	evidence := map[string]any{"schema": 1, "instance_id": previous.Runtime.ID, "data_path": previous.Storage.DataPath, "transaction": tx, "config_identity": instanceHash(config), "data_identity": "1:2", "staged_identity": "verified", "source_identity": "sourceInstanceID=jv-d4e5f6", "was_running": true}
	if err = b.writeJSON(filepath.Join(tx, "evidence.json"), evidence, false); err != nil {
		t.Fatal(err)
	}
	portable := map[string]any{"instance_id": previous.Runtime.ID, "game_mode": "creative", "difficulty": "hard", "whitelist": false, "enforce_whitelist": false, "max_players": 25, "motd": "Imported server"}
	if err = b.writeJSON(filepath.Join(tx, "portable.json"), portable, false); err != nil {
		t.Fatal(err)
	}
	next, err := b.migrationPortableSettings(previous, tx)
	if err != nil {
		t.Fatal(err)
	}
	expected := previous
	expected.Runtime.GameMode = "creative"
	expected.Runtime.Difficulty = "hard"
	expected.Runtime.Whitelist = false
	expected.Runtime.EnforceWhitelist = false
	expected.Runtime.MaxPlayers = 25
	expected.Runtime.MOTD = "Imported server"
	if !reflect.DeepEqual(next, expected) {
		t.Fatal("portable source changed destination identity, ports, limits, storage, boot policy or runtime ownership")
	}
	portable["instance_id"] = "jv-d4e5f6"
	if err = b.writeJSON(filepath.Join(tx, "portable.json"), portable, true); err != nil {
		t.Fatal(err)
	}
	if _, err = b.migrationPortableSettings(previous, tx); err == nil {
		t.Fatal("archive source identity replaced selected destination")
	}
	delete(evidence, "instance_id")
	if err = b.writeJSON(filepath.Join(tx, "evidence.json"), evidence, true); err != nil {
		t.Fatal(err)
	}
	if _, err = b.migrationPortableSettings(previous, tx); err == nil {
		t.Fatal("missing durable identity was guessed")
	}
}
