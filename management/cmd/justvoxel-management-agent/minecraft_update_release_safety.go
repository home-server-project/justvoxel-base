package main

import (
	"context"
	"fmt"
	"strings"
)

// Automatic journal closure only proves the already-running committed runtime.
// It must never recover, publish, start or otherwise repair runtime state.
func verifyMinecraftFailedUpdateSafeToRelease(ctx context.Context, id string) error {
	if !instanceIDPattern.MatchString(id) {
		return errInstanceID
	}
	b := instanceRuntimes
	record, err := b.readRecord(id)
	if err != nil {
		return fmt.Errorf("read committed runtime: %w", err)
	}
	if err := b.verifyLive(record); err != nil {
		return fmt.Errorf("verify committed runtime: %w", err)
	}
	status, err := b.observe(ctx, id)
	if err != nil {
		return fmt.Errorf("observe running runtime: %w", err)
	}
	if status.State != "running" {
		return fmt.Errorf("committed runtime is not running: %s", status.State)
	}
	if !operationFingerprintPattern.MatchString(record.Runtime.ImageDigest) {
		return fmt.Errorf("committed image digest is unavailable or invalid")
	}
	image, err := b.command(ctx, "podman", "inspect", "--type", "container", "--format", "{{.Image}}", "minecraft-"+id)
	if err != nil {
		return fmt.Errorf("inspect running container image: %w", err)
	}
	imageID := strings.TrimSpace(string(image))
	if !operationFingerprintPattern.MatchString(imageID) && !instanceSHA256Pattern.MatchString(imageID) {
		return fmt.Errorf("running container image ID is unavailable or invalid")
	}
	digest, err := b.command(ctx, "podman", "image", "inspect", imageID, "--format", "{{.Digest}}")
	if err != nil {
		return fmt.Errorf("inspect running image digest: %w", err)
	}
	if strings.TrimSpace(string(digest)) != record.Runtime.ImageDigest {
		return fmt.Errorf("running image digest does not match committed runtime")
	}
	// The journal already owns maintenance exclusion. This is the existing full
	// verifier, including readiness, RCON, software/version and plugin checks.
	if _, err := b.command(ctx, "env", "JUSTVOXEL_MAINTENANCE_LOCK_HELD=1", "/usr/libexec/justvoxel/mjust/verify-minecraft-stack", "--instance", id, record.Runtime.Version); err != nil {
		return fmt.Errorf("verify running Minecraft stack: %w", err)
	}
	return nil
}
