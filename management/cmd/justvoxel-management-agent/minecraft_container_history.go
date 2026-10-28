package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Image history is separate from the committed runtime definition. Older instances
// have no history file and retain one previous image by default.
type retainedContainerImage struct {
	Digest     string    `json:"digest"`
	Image      string    `json:"image"`
	RetainedAt time.Time `json:"retained_at"`
	Size       int64     `json:"size,omitempty"`
}
type containerImageHistory struct {
	KeepPreviousImages int                      `json:"keep_previous_images"`
	PreviousImages     []retainedContainerImage `json:"previous_images"`
	PendingRemoval     []retainedContainerImage `json:"pending_removal,omitempty"`
}

func (b *instanceRuntimeBackend) containerHistoryPath(id string) string {
	return filepath.Join(b.staging, "container-images", id+".json")
}
func (b *instanceRuntimeBackend) containerHistory(id string) (containerImageHistory, error) {
	h := containerImageHistory{KeepPreviousImages: 1, PreviousImages: []retainedContainerImage{}}
	if !instanceIDPattern.MatchString(id) {
		return h, errInstanceID
	}
	data, err := b.readPrivate(b.containerHistoryPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return h, nil
	}
	if err != nil {
		return h, err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err = d.Decode(&h); err != nil {
		return h, err
	}
	if d.Decode(new(any)) != io.EOF || h.KeepPreviousImages < 0 {
		return h, errors.New("invalid image history")
	}
	seen := map[string]bool{}
	for _, image := range append(append([]retainedContainerImage{}, h.PreviousImages...), h.PendingRemoval...) {
		if !operationFingerprintPattern.MatchString(image.Digest) || image.RetainedAt.IsZero() || image.Size < 0 || !strings.HasPrefix(image.Image, "docker.io/itzg/minecraft-") {
			return h, errors.New("invalid retained image")
		}
	}
	for _, image := range h.PreviousImages {
		if seen[image.Digest] {
			return h, errors.New("duplicate retained image")
		}
		seen[image.Digest] = true
	}
	return h, nil
}
func retainedImageReference(id, digest string) string {
	return "localhost/justvoxel-retained/" + id + ":" + strings.TrimPrefix(digest, "sha256:")
}
func (b *instanceRuntimeBackend) inspectRetainedImage(ctx context.Context, ref, digest string) (int64, error) {
	data, err := b.command(ctx, "podman", "image", "inspect", ref, "--format", "{{.Digest}} {{.Size}}")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 || fields[0] != digest {
		return 0, errors.New("retained image identity changed")
	}
	size, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || size < 0 {
		return 0, errors.New("invalid retained image size")
	}
	return size, nil
}
func rotateContainerHistory(h containerImageHistory, old *retainedContainerImage, selected string) containerImageHistory {
	images := []retainedContainerImage{}
	if old != nil && old.Digest != selected {
		images = append(images, *old)
	}
	for _, image := range h.PreviousImages {
		if image.Digest == selected || (old != nil && image.Digest == old.Digest) {
			continue
		}
		images = append(images, image)
	}
	if len(images) > h.KeepPreviousImages {
		images = images[:h.KeepPreviousImages]
	}
	for _, image := range h.PreviousImages {
		keep := false
		for _, kept := range images {
			if kept.Digest == image.Digest {
				keep = true
			}
		}
		if !keep {
			h.PendingRemoval = append(h.PendingRemoval, image)
		}
	}
	h.PreviousImages = images
	return h
}
func (b *instanceRuntimeBackend) saveContainerHistory(ctx context.Context, id string, h containerImageHistory) error {
	stage, err := b.privateDirectory(b.staging, true)
	if err != nil {
		return err
	}
	stage.Close()
	dir, err := b.privateDirectory(filepath.Dir(b.containerHistoryPath(id)), true)
	if err != nil {
		return err
	}
	dir.Close()
	// Commit the list before removing references. Interrupted cleanup is retried.
	if err = b.writeJSON(b.containerHistoryPath(id), h, true); err != nil {
		return err
	}
	pending := []retainedContainerImage{}
	for _, image := range h.PendingRemoval {
		keep := false
		for _, kept := range h.PreviousImages {
			if kept.Digest == image.Digest {
				keep = true
			}
		}
		if keep {
			continue
		}
		ref := retainedImageReference(id, image.Digest)
		// Never remove an underlying image ID. Untag removes ONLY this instance's
		// private reference, preserving every selected/running/sibling/unrelated image.
		if _, err = b.command(ctx, "podman", "image", "exists", ref); err != nil {
			var exit interface{ ExitCode() int }
			if errors.As(err, &exit) && exit.ExitCode() == 1 {
				continue
			}
			return err
		}
		if _, err = b.inspectRetainedImage(ctx, ref, image.Digest); err != nil {
			return err
		}
		if _, err = b.command(ctx, "podman", "untag", ref); err != nil {
			pending = append(pending, image)
		}
	}
	h.PendingRemoval = pending
	if err = b.writeJSON(b.containerHistoryPath(id), h, true); err != nil {
		return err
	}
	if len(pending) > 0 {
		return errors.New("previous image cleanup could not finish; retry retention setting")
	}
	return nil
}

// Called only by the bounded root helper holding appliance maintenance. No HTTP
// endpoint can invent an old digest or inject a reference into history.
func containerHistoryCommand(ctx context.Context, args []string) error {
	if os.Geteuid() != 0 || len(args) < 2 || !instanceIDPattern.MatchString(args[1]) {
		return errInstanceID
	}
	action, id := args[0], args[1]
	b := instanceRuntimes
	record, err := b.readRecord(id)
	if err != nil {
		return err
	}
	if err = b.verifyLive(record); err != nil {
		return err
	}
	h, err := b.containerHistory(id)
	if err != nil {
		return err
	}
	switch action {
	case "status":
		if len(args) != 2 {
			return errInstanceID
		}
		selected := record.Runtime.ImageDigest
		if selected == "" {
			repo := "docker.io/itzg/minecraft-server"
			if record.Runtime.Software == "bedrock" {
				repo = "docker.io/itzg/minecraft-bedrock-server"
			}
			if data, inspectErr := b.command(ctx, "podman", "image", "inspect", repo+":"+record.Runtime.ImageTag, "--format", "{{.Digest}}"); inspectErr == nil {
				selected = strings.TrimSpace(string(data))
				if !operationFingerprintPattern.MatchString(selected) {
					return errors.New("selected image identity unavailable")
				}
			}
		}
		visible := []retainedContainerImage{}
		for _, image := range h.PreviousImages {
			if image.Digest == selected {
				continue
			}
			if _, err = b.inspectRetainedImage(ctx, retainedImageReference(id, image.Digest), image.Digest); err != nil {
				return err
			}
			visible = append(visible, image)
		}
		h.PreviousImages = visible
		h.PendingRemoval = nil
		return json.NewEncoder(os.Stdout).Encode(h)
	case "retention":
		if len(args) != 3 {
			return errInstanceID
		}
		n, err := strconv.Atoi(args[2])
		if err != nil || n < 0 {
			return errors.New("keep previous images must be a non-negative integer")
		}
		h.KeepPreviousImages = n
		return b.saveContainerHistory(ctx, id, rotateContainerHistory(h, nil, record.Runtime.ImageDigest))
	case "record":
		if len(args) != 4 || (args[2] != "" && !operationFingerprintPattern.MatchString(args[2])) || !operationFingerprintPattern.MatchString(args[3]) || record.Runtime.ImageDigest != args[3] {
			return errInstanceID
		}
		var old *retainedContainerImage
		if args[2] != "" && args[2] != args[3] && h.KeepPreviousImages > 0 {
			repo := "docker.io/itzg/minecraft-server"
			if record.Runtime.Software == "bedrock" {
				repo = "docker.io/itzg/minecraft-bedrock-server"
			}
			source := repo + "@" + args[2]
			size, err := b.inspectRetainedImage(ctx, source, args[2])
			if err != nil {
				return err
			}
			ref := retainedImageReference(id, args[2])
			if _, err = b.command(ctx, "podman", "image", "exists", ref); err == nil {
				if _, err = b.inspectRetainedImage(ctx, ref, args[2]); err != nil {
					return err
				}
			} else {
				var exit interface{ ExitCode() int }
				if !errors.As(err, &exit) || exit.ExitCode() != 1 {
					return err
				}
				if _, err = b.command(ctx, "podman", "tag", source, ref); err != nil {
					return err
				}
			}
			if _, err = b.inspectRetainedImage(ctx, ref, args[2]); err != nil {
				return err
			}
			old = &retainedContainerImage{Digest: args[2], Image: repo + ":" + record.Runtime.ImageTag, Size: size, RetainedAt: time.Now().UTC()}
		}
		return b.saveContainerHistory(ctx, id, rotateContainerHistory(h, old, args[3]))
	}
	return fmt.Errorf("invalid image history action")
}
