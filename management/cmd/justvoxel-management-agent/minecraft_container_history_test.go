package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func historyImage(n int) retainedContainerImage {
	return retainedContainerImage{Digest: "sha256:" + strings.Repeat("0123456789abcdef"[n:n+1], 64), Image: "docker.io/itzg/minecraft-server:stable", RetainedAt: time.Date(2026, 10, 27, 0, n, 0, 0, time.UTC), Size: 1024}
}
func TestContainerHistoryDefaultAndExactNewestRetention(t *testing.T) {
	f := newInstanceActivationFixture(t)
	h, err := f.backend.containerHistory("jv-a1b2c3")
	if err != nil || h.KeepPreviousImages != 1 || len(h.PreviousImages) != 0 {
		t.Fatalf("missing setting: %+v %v", h, err)
	}
	for _, keep := range []int{0, 1, 2, 7} {
		h = containerImageHistory{KeepPreviousImages: keep}
		for n := 0; n < 10; n++ {
			old := historyImage(n)
			h = rotateContainerHistory(h, &old, "sha256:"+strings.Repeat("f", 64))
		}
		want := keep
		if want > 10 {
			want = 10
		}
		if len(h.PreviousImages) != want {
			t.Fatalf("retention %d: %+v", keep, h.PreviousImages)
		}
		for i, image := range h.PreviousImages {
			if image.Digest != historyImage(9-i).Digest {
				t.Fatalf("retention order: %+v", h.PreviousImages)
			}
		}
	}
	// The current selected digest is never a previous image, including a restore.
	current, old := historyImage(1), historyImage(0)
	h = containerImageHistory{KeepPreviousImages: 1, PreviousImages: []retainedContainerImage{old}}
	h = rotateContainerHistory(h, &current, old.Digest)
	if len(h.PreviousImages) != 1 || h.PreviousImages[0].Digest != current.Digest {
		t.Fatal("restore did not retain an undo image")
	}
}
func TestContainerHistoryPerInstanceDurabilityAndReferenceOnlyCleanup(t *testing.T) {
	f := newInstanceActivationFixture(t)
	b := f.backend
	d, err := b.privateDirectory(b.staging, true)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	old, newer := historyImage(0), historyImage(1)
	calls := []string{}
	b.command = func(_ context.Context, cmd string, args ...string) ([]byte, error) {
		calls = append(calls, cmd+" "+strings.Join(args, " "))
		if cmd != "podman" {
			t.Fatal(cmd)
		}
		if args[0] == "image" && args[1] == "inspect" {
			return []byte(old.Digest + " 1024"), nil
		}
		return nil, nil
	}
	sibling := containerImageHistory{KeepPreviousImages: 2, PreviousImages: []retainedContainerImage{old}}
	if err = b.saveContainerHistory(context.Background(), "jv-sibling", sibling); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(b.containerHistoryPath("jv-sibling"))
	if err != nil {
		t.Fatal(err)
	}
	h := containerImageHistory{KeepPreviousImages: 1, PreviousImages: []retainedContainerImage{old}}
	h = rotateContainerHistory(h, &newer, "sha256:"+strings.Repeat("e", 64))
	if err = b.saveContainerHistory(context.Background(), "jv-a1b2c3", h); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(b.containerHistoryPath("jv-sibling"))
	if err != nil || string(before) != string(after) {
		t.Fatal("sibling history changed")
	}
	selected, err := b.containerHistory("jv-a1b2c3")
	if err != nil || !reflect.DeepEqual(selected.PreviousImages, []retainedContainerImage{newer}) {
		t.Fatalf("selected history: %+v %v", selected, err)
	}
	if len(calls) != 3 || calls[2] != "podman untag "+retainedImageReference("jv-a1b2c3", old.Digest) {
		t.Fatalf("cleanup: %v", calls)
	}
	for _, call := range calls {
		for _, forbidden := range []string{"prune", "--force", " rmi ", " rm ", "justvoxel-retained/jv-sibling"} {
			if strings.Contains(call, forbidden) {
				t.Fatal(call)
			}
		}
	}
	// No selected/running image identity is ever passed to an image deletion.
	if strings.Contains(strings.Join(calls, "\n"), "podman untag "+old.Digest) {
		t.Fatal("underlying image deleted")
	}
}
func TestContainerHistoryRejectsMalformedAndRetriesInterruptedCleanup(t *testing.T) {
	f := newInstanceActivationFixture(t)
	b := f.backend
	d, err := b.privateDirectory(b.staging, true)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	d, err = b.privateDirectory(filepath.Dir(b.containerHistoryPath("jv-a1b2c3")), true)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	if err = b.writePrivate(b.containerHistoryPath("jv-a1b2c3"), []byte(`{"keep_previous_images":-1,"previous_images":[]}`), false); err != nil {
		t.Fatal(err)
	}
	if _, err = b.containerHistory("jv-a1b2c3"); err == nil {
		t.Fatal("negative stored retention accepted")
	}
	old := historyImage(0)
	fail := true
	b.command = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "image" && args[1] == "inspect" {
			return []byte(old.Digest + " 1024"), nil
		}
		if args[0] == "untag" && fail {
			return nil, errors.New("busy")
		}
		return nil, nil
	}
	h := containerImageHistory{KeepPreviousImages: 0, PreviousImages: []retainedContainerImage{}, PendingRemoval: []retainedContainerImage{old}}
	if err = b.saveContainerHistory(context.Background(), "jv-a1b2c3", h); err == nil {
		t.Fatal("cleanup failure hidden")
	}
	persisted, err := b.containerHistory("jv-a1b2c3")
	if err != nil || len(persisted.PendingRemoval) != 1 {
		t.Fatal("cleanup evidence lost")
	}
	fail = false
	if err = b.saveContainerHistory(context.Background(), "jv-a1b2c3", persisted); err != nil {
		t.Fatal(err)
	}
	persisted, err = b.containerHistory("jv-a1b2c3")
	if err != nil || len(persisted.PendingRemoval) != 0 {
		t.Fatal("cleanup retry failed")
	}
}
