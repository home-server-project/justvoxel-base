package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReviewedVersionPublicationChangesOnlySelectedInstance(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	records, err := f.backend.records()
	if err != nil {
		t.Fatal(err)
	}
	previous, err := f.backend.readRecord("jv-a1b2c3")
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := f.backend.readRecord("jv-d4e5f6")
	if err != nil {
		t.Fatal(err)
	}
	_, siblingEnv, siblingQuadlet := f.backend.paths(sibling.Runtime.ID)
	beforeEnv, _ := f.backend.readPrivate(siblingEnv)
	beforeQuadlet, _ := f.backend.readPrivate(siblingQuadlet)
	if err = os.WriteFile(filepath.Join(previous.Storage.DataPath, "level.dat"), []byte("existing world"), 0600); err != nil {
		t.Fatal(err)
	}
	next := previous
	next.Runtime.Version = "1.21.5"
	next.Runtime.ImageDigest = "sha256:" + strings.Repeat("a", 64)
	next.Runtime.VersionPolicy = "pinned"
	next.Runtime.Software = "purpur"
	if err = f.backend.applyVersion(context.Background(), previous, instanceReplaceRecord(records, next), f.snapshot, true); err != nil {
		t.Fatal(err)
	}
	current, err := f.backend.readRecord(previous.Runtime.ID)
	if err != nil || current.Runtime.Version != "1.21.5" || current.Runtime.Software != "purpur" {
		t.Fatal(current.Runtime.Version, err)
	}
	afterSibling, err := f.backend.readRecord(sibling.Runtime.ID)
	if err != nil || !reflect.DeepEqual(sibling, afterSibling) {
		t.Fatal("sibling record changed", err)
	}
	afterEnv, _ := f.backend.readPrivate(siblingEnv)
	afterQuadlet, _ := f.backend.readPrivate(siblingQuadlet)
	if !reflect.DeepEqual(beforeEnv, afterEnv) || !reflect.DeepEqual(beforeQuadlet, afterQuadlet) {
		t.Fatal("sibling runtime changed")
	}
	_, _, selectedQuadlet := f.backend.paths(current.Runtime.ID)
	selectedBytes, err := f.backend.readPrivate(selectedQuadlet)
	if err != nil || !strings.Contains(string(selectedBytes), "Image=docker.io/itzg/minecraft-server@"+next.Runtime.ImageDigest) {
		t.Fatal("selected image commitment missing", err)
	}
	if err = f.backend.verifyLive(current); err != nil {
		t.Fatal(err)
	}
}

func TestLiveWhitelistStateKeepsRunningServerAndSibling(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	old := f.backend.command
	whitelistCommands := []string{}
	f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command == "podman" && len(args) == 4 && args[0] == "exec" && args[1] == "minecraft-jv-a1b2c3" && args[2] == "rcon-cli" && (args[3] == "whitelist off" || args[3] == "whitelist on") {
			whitelistCommands = append(whitelistCommands, args[3])
			return []byte("Whitelist changed"), nil
		}
		return old(ctx, command, args...)
	}
	f.states["jv-a1b2c3"] = "active"
	f.containers["jv-a1b2c3"] = "running"
	previous, err := f.backend.readRecord("jv-a1b2c3")
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := f.backend.readRecord("jv-d4e5f6")
	if err != nil {
		t.Fatal(err)
	}
	records, err := f.backend.records()
	if err != nil {
		t.Fatal(err)
	}
	next := previous
	next.Runtime.Whitelist = false
	if err = f.backend.apply(context.Background(), previous, instanceReplaceRecord(records, next), f.snapshot); err != nil {
		t.Fatal(err)
	}
	current, err := f.backend.readRecord("jv-a1b2c3")
	if err != nil || current.Runtime.Whitelist || f.states["jv-a1b2c3"] != "active" || !reflect.DeepEqual(whitelistCommands, []string{"whitelist off"}) {
		t.Fatal("selected live whitelist state not preserved", err, whitelistCommands)
	}
	other, err := f.backend.readRecord("jv-d4e5f6")
	if err != nil || !reflect.DeepEqual(other, sibling) {
		t.Fatal("sibling changed", err)
	}
}

func TestVersionWorkerNeverRetagsOrRemovesSharedSiblingImages(t *testing.T) {
	source, err := os.ReadFile("../../../mjust/libexec/update-minecraft-backend")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"podman tag ", "podman image rm "} {
		if strings.Contains(string(source), forbidden) {
			t.Fatal("shared image mutation", forbidden)
		}
	}
	if !strings.Contains(string(source), `image="${JV_MINECRAFT_IMAGE_REPO}@${remote_digest}"`) {
		t.Fatal("image pulls are not bound to reviewed digest")
	}
}

func TestContainerPublicationOnlyChangesSelectedDigestWhileRunning(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	f.states["jv-a1b2c3"] = "active"
	f.containers["jv-a1b2c3"] = "running"
	previous, err := f.backend.readRecord("jv-a1b2c3")
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := f.backend.readRecord("jv-d4e5f6")
	if err != nil {
		t.Fatal(err)
	}
	records, err := f.backend.records()
	if err != nil {
		t.Fatal(err)
	}
	next := previous
	next.Runtime.ImageDigest = "sha256:" + strings.Repeat("b", 64)
	originalCommand := f.backend.command
	f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command == "podman" && len(args) == 5 && args[0] == "image" && args[1] == "inspect" && args[4] == "{{.Digest}}" {
			return []byte(next.Runtime.ImageDigest), nil
		}
		return originalCommand(ctx, command, args...)
	}
	f.calls = nil
	if err = f.backend.applyVersion(context.Background(), previous, instanceReplaceRecord(records, next), f.snapshot, true, true); err != nil {
		t.Fatal(err)
	}
	after, err := f.backend.readRecord(previous.Runtime.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after.Runtime, next.Runtime) || after.EnvironmentSHA256 != previous.EnvironmentSHA256 || f.states[previous.Runtime.ID] != "active" {
		t.Fatal("image publication changed runtime or process")
	}
	other, err := f.backend.readRecord(sibling.Runtime.ID)
	if err != nil || !reflect.DeepEqual(other, sibling) {
		t.Fatal("sibling changed", err)
	}
	for _, call := range f.calls {
		for _, forbidden := range []string{"systemctl start", "systemctl stop", "firewall-cmd", " channel "} {
			if strings.Contains(call, forbidden) {
				t.Fatalf("image publication invoked %s", call)
			}
		}
	}
	// The same special path must never authorize adjacent configuration changes.
	next.Runtime.Version = "1.21.5"
	if err = f.backend.applyVersion(context.Background(), after, instanceReplaceRecord(records, next), f.snapshot, true, true); err == nil {
		t.Fatal("image-only publication changed the Minecraft version")
	}
}
