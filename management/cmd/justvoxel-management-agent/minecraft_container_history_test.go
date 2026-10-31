package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
			return []byte("sha256:" + strings.Repeat("a", 64) + "|" + old.Digest + "|1024"), nil
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
	if len(calls) != 3 || calls[2] != "podman untag "+retainedImageReference("jv-a1b2c3", old.Digest)+" "+retainedImageReference("jv-a1b2c3", old.Digest) {
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
			return []byte("sha256:" + strings.Repeat("a", 64) + "|" + old.Digest + "|1024"), nil
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

// Podman can report different digests for two references to the same local ID.
func TestContainerHistoryLocalTagIdentity(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, invalid := range []string{"", "different-id", "size", "malformed", "missing", "source-digest"} {
			t.Run(fmt.Sprintf("existing=%t/%s", existing, invalid), func(t *testing.T) {
				f := newInstanceActivationFixture(t)
				f.activateBoth()
				b := f.backend
				id := f.configs[0].ID
				old, selected := historyImage(0), historyImage(1)
				record, err := b.readRecord(id)
				if err != nil {
					t.Fatal(err)
				}
				record.Runtime.ImageDigest = selected.Digest
				dir, _, _ := b.paths(id)
				if err = b.writeJSON(filepath.Join(dir, "config.json"), record, true); err != nil {
					t.Fatal(err)
				}
				ref := retainedImageReference(id, old.Digest)
				localID := strings.Repeat("a", 64)
				tagged := existing
				bad := invalid
				calls := []string{}
				b.command = func(_ context.Context, cmd string, args ...string) ([]byte, error) {
					calls = append(calls, cmd+" "+strings.Join(args, " "))
					switch strings.Join(args[:2], " ") {
					case "image exists":
						if args[2] != ref {
							t.Fatal("unexpected reference", args)
						}
						if !tagged {
							return nil, historyImageMissing{}
						}
						return nil, nil
					case "image inspect":
						if args[2] == "docker.io/itzg/minecraft-server@"+old.Digest {
							digest := old.Digest
							if bad == "source-digest" {
								digest = selected.Digest
							}
							return []byte(localID + "|" + digest + "|1024"), nil
						}
						if args[2] != ref {
							t.Fatal("unexpected reference", args)
						}
						switch bad {
						case "different-id":
							return []byte("sha256:" + strings.Repeat("b", 64) + "|" + selected.Digest + "|1024"), nil
						case "size":
							return []byte(localID + "|" + selected.Digest + "|2048"), nil
						case "malformed":
							return []byte("invalid|digest|1024"), nil
						case "missing":
							return nil, errors.New("missing retained reference")
						}
						return []byte(localID + "|" + selected.Digest + "|1024"), nil
					}
					if args[0] == "tag" {
						tagged = true
						return nil, nil
					}
					if args[0] == "untag" && len(args) == 3 && args[1] == ref && args[2] == ref {
						tagged = false
						return nil, nil
					}
					t.Fatal("unexpected command", calls[len(calls)-1])
					return nil, nil
				}
				err = b.containerHistoryCommand(context.Background(), []string{"record", id, old.Digest, selected.Digest})
				if invalid != "" {
					if err == nil {
						t.Fatal("invalid image accepted")
					}
					if _, err = os.Stat(b.containerHistoryPath(id)); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("failed record created history", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(b.containerHistoryPath(id))
				if err != nil {
					t.Fatal(err)
				}
				var persisted containerImageHistory
				if err = json.Unmarshal(data, &persisted); err != nil || len(persisted.PreviousImages) != 1 || persisted.PreviousImages[0].Digest != old.Digest {
					t.Fatalf("registry identity lost: %s %v", data, err)
				}
				// Capture status JSON, which must preserve the registry digest too.
				output, err := os.CreateTemp(t.TempDir(), "status")
				if err != nil {
					t.Fatal(err)
				}
				defer output.Close()
				stdout := os.Stdout
				os.Stdout = output
				err = b.containerHistoryCommand(context.Background(), []string{"status", id})
				os.Stdout = stdout
				if err != nil {
					t.Fatal(err)
				}
				statusData, err := os.ReadFile(output.Name())
				if err != nil || !strings.Contains(string(statusData), old.Digest) {
					t.Fatal("status lost old digest", string(statusData), err)
				}
				// Later status and cleanup must reject changed size or invalid/missing tags.
				restoreHistory := func() {
					t.Helper()
					if err := os.WriteFile(b.containerHistoryPath(id), data, 0600); err != nil {
						t.Fatal(err)
					}
					tagged = true
				}
				for _, corruption := range []string{"size", "malformed", "missing"} {
					restoreHistory()
					bad = corruption
					if err = b.containerHistoryCommand(context.Background(), []string{"status", id}); err == nil {
						t.Fatal("status accepted", bad)
					}
					restoreHistory()
					bad = corruption
					if err = b.saveContainerHistory(context.Background(), id, containerImageHistory{PendingRemoval: persisted.PreviousImages}); err == nil {
						t.Fatal("cleanup accepted", bad)
					}
				}
				restoreHistory()
				bad = ""
				if err = b.containerHistoryCommand(context.Background(), []string{"retention", id, "0"}); err != nil {
					t.Fatal(err)
				}
				if tagged || calls[len(calls)-1] != "podman untag "+ref+" "+ref {
					t.Fatal("cleanup did not untag exact private reference", calls)
				}
			})
		}
	}
}

type historyImageMissing struct{}

func (historyImageMissing) Error() string { return "image missing" }
func (historyImageMissing) ExitCode() int { return 1 }

// Model Podman's name ownership, including its documented all-names behavior
// when untag is passed only IMAGE. A permissive untag stub hid this regression.
func TestContainerHistoryUpdateRestoreUpdatePreservesNames(t *testing.T) {
	for _, keep := range []int{1, 2} {
		t.Run(fmt.Sprint(keep), func(t *testing.T) {
			f := newInstanceActivationFixture(t)
			f.activateBoth()
			b := f.backend
			id := f.configs[0].ID
			a, next, earlier := historyImage(1), historyImage(2), historyImage(3)
			repo := "docker.io/itzg/minecraft-server@"
			names := map[string]string{}
			identities := map[string]retainedContainerImage{}
			for _, image := range []retainedContainerImage{a, next, earlier} {
				names[repo+image.Digest] = image.Digest
				identities[image.Digest] = image
			}
			sibling := retainedImageReference("jv-sibling", a.Digest)
			names[sibling] = a.Digest
			names["localhost/unrelated:stable"] = a.Digest
			serving := a.Digest
			b.command = func(_ context.Context, cmd string, args ...string) ([]byte, error) {
				if cmd != "podman" {
					t.Fatal(cmd)
				}
				switch args[0] {
				case "image":
					digest, exists := names[args[2]]
					if !exists {
						return nil, historyImageMissing{}
					}
					if args[1] == "exists" {
						return nil, nil
					}
					if args[1] != "inspect" {
						t.Fatal(args)
					}
					image := identities[digest]
					return []byte(strings.TrimPrefix(digest, "sha256:") + "|" + digest + "|" + fmt.Sprint(image.Size)), nil
				case "tag":
					digest, exists := names[args[1]]
					if !exists {
						return nil, historyImageMissing{}
					}
					names[args[2]] = digest
					return nil, nil
				case "untag":
					digest, exists := names[args[1]]
					if !exists {
						return nil, historyImageMissing{}
					}
					if len(args) == 2 {
						for name, identity := range names {
							if identity == digest {
								delete(names, name)
							}
						}
					} else {
						for _, name := range args[2:] {
							if names[name] != digest {
								t.Fatal("wrong image owner", args)
							}
							delete(names, name)
						}
					}
					return nil, nil
				}
				t.Fatal("unexpected command", args)
				return nil, nil
			}
			publish := func(digest string) {
				t.Helper()
				if names[repo+digest] != digest {
					t.Fatal("selected registry name lost", digest)
				}
				record, err := b.readRecord(id)
				if err != nil {
					t.Fatal(err)
				}
				record.Runtime.ImageDigest = digest
				dir, _, _ := b.paths(id)
				if err = b.writeJSON(filepath.Join(dir, "config.json"), record, true); err != nil {
					t.Fatal(err)
				}
				serving = digest // selected server has started and verified this identity
			}
			recordHistory := func(old, selected string, want []retainedContainerImage) {
				t.Helper()
				if err := b.containerHistoryCommand(context.Background(), []string{"record", id, old, selected}); err != nil {
					t.Fatalf("record after verified runtime %s: %v", serving, err)
				}
				h, err := b.containerHistory(id)
				if err != nil || h.KeepPreviousImages != keep || len(h.PreviousImages) != len(want) || len(h.PendingRemoval) != 0 {
					t.Fatalf("rotation: %+v %v", h, err)
				}
				for i, image := range want {
					if h.PreviousImages[i].Digest != image.Digest || names[retainedImageReference(id, image.Digest)] != image.Digest {
						t.Fatal("retained identity/order lost", h)
					}
				}
			}
			publish(a.Digest)
			if err := b.containerHistoryCommand(context.Background(), []string{"retention", id, fmt.Sprint(keep)}); err != nil {
				t.Fatal(err)
			}
			recordHistory(earlier.Digest, a.Digest, []retainedContainerImage{earlier})
			expected := func(first retainedContainerImage) []retainedContainerImage {
				images := []retainedContainerImage{first}
				if keep == 2 {
					images = append(images, earlier)
				}
				return images
			}
			publish(next.Digest)
			recordHistory(a.Digest, next.Digest, expected(a))
			publish(a.Digest) // restore retained A, retaining B as the undo image
			recordHistory(next.Digest, a.Digest, expected(next))
			publish(next.Digest)
			recordHistory(a.Digest, next.Digest, expected(a))
			// Without explicit-name cleanup, restoring A removes its registry
			// name and the final record above fails inspecting the serving A.
			if names[repo+serving] != serving || names[sibling] != a.Digest || names["localhost/unrelated:stable"] != a.Digest {
				t.Fatal("serving/sibling/unrelated name removed", names)
			}
			// A later retention reduction removes only owned private names.
			if err := b.containerHistoryCommand(context.Background(), []string{"retention", id, "0"}); err != nil {
				t.Fatal(err)
			}
			h, err := b.containerHistory(id)
			if err != nil || h.KeepPreviousImages != 0 || len(h.PreviousImages) != 0 || len(h.PendingRemoval) != 0 {
				t.Fatal(h, err)
			}
			if names[repo+a.Digest] != a.Digest || names[repo+serving] != serving || names[sibling] != a.Digest {
				t.Fatal("retention removed shared names")
			}
		})
	}
}
