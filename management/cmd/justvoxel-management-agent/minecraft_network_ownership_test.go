package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Preserve fixture roles by immutable ID; records() has filesystem order.
func networkOwnershipRecords(t *testing.T, f *instanceActivationFixture) []instanceRuntimeRecord {
	t.Helper()
	records := make([]instanceRuntimeRecord, 0, len(f.configs))
	for _, config := range f.configs {
		record, err := f.backend.readRecord(config.ID)
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}

func TestNetworkMemoryCapacityEquation(t *testing.T) {
	for _, workload := range []int64{512, 2048, 4096, 6144} {
		total := workload + 512 + 1024
		if !instanceMemoryCapacity(total, workload, minecraftNetworkProxyMemoryMiB) || instanceMemoryCapacity(total-1, workload, minecraftNetworkProxyMemoryMiB) {
			t.Fatalf("Network capacity must mean workload + 512 + 1024 <= total: %d", workload)
		}
	}
}

func TestNetworkOverlayPreservesStandaloneBootPreference(t *testing.T) {
	for _, saved := range []bool{false, true} {
		f := newInstanceActivationFixtureFor(t, "paper", "1.21.4")
		records := networkOwnershipRecords(t, f)
		records[0].Runtime.StartOnBoot = saved
		standalone, err := f.backend.render(f.snapshot, records)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(standalone[0].Quadlet), "[Install]") != saved || strings.Contains(string(standalone[0].Quadlet), "WantedBy=multi-user.target") != saved {
			t.Fatal("standalone rendering does not reflect saved boot preference")
		}
		records[0].NetworkMember = true
		enrolled, err := f.backend.render(f.snapshot, records)
		if err != nil {
			t.Fatal(err)
		}
		q := string(enrolled[0].Quadlet)
		if strings.Count(q, "[Install]") != 1 || strings.Count(q, "WantedBy=multi-user.target") != 1 || records[0].Runtime.StartOnBoot != saved {
			t.Fatal("effective boot ownership changed saved preference or duplicated installation", q)
		}
		records[0].NetworkMember = false
		removed, err := f.backend.render(f.snapshot, records)
		if err != nil || !bytes.Equal(removed[0].Quadlet, standalone[0].Quadlet) || records[0].Runtime.StartOnBoot != saved {
			t.Fatal("standalone boot preference was not restored", err)
		}
	}
}

func TestNetworkOwnsSelectedLifecycle(t *testing.T) {
	for _, later := range []bool{false, true} {
		t.Run(map[bool]string{false: "enable", true: "add to enabled"}[later], func(t *testing.T) {
			f, c := networkTransactionFixture(t, true)
			first, second := f.configs[0].ID, f.configs[1].ID
			if later {
				if err := networkApply(context.Background(), f.backend, c); err != nil {
					t.Fatal(err)
				}
			}
			f.states[first], f.containers[first] = "active", "running"
			f.states[second], f.containers[second] = "inactive", "exited"
			before, err := f.backend.readRecord(second)
			if err != nil {
				t.Fatal(err)
			}
			h := networkLifecycleControls(f)
			c.Members = []string{first, second}
			if err := networkApply(context.Background(), f.backend, c); err != nil {
				t.Fatal(err)
			}
			want := append([]string{}, c.Members...)
			sort.Strings(want)
			if !reflect.DeepEqual(h.stops, []string{first}) || !reflect.DeepEqual(h.starts, want) {
				t.Fatal("running member was not maintained or stopped selection was not started", h.stops, h.starts)
			}
			for _, id := range c.Members {
				record, err := f.backend.readRecord(id)
				if err != nil || !record.NetworkMember || f.states[id] != "active" {
					t.Fatal(id, err)
				}
			}
			after, err := f.backend.readRecord(second)
			if err != nil || after.Runtime.StartOnBoot != before.Runtime.StartOnBoot {
				t.Fatal("saved boot preference changed", err)
			}
		})
	}
}

func TestNetworkUnselectedStoppedServersStayStopped(t *testing.T) {
	f, c := networkTransactionFixture(t)
	id, standalone := f.configs[0].ID, f.configs[1].ID
	f.states[standalone], f.containers[standalone] = "inactive", "exited"
	if err := networkApply(context.Background(), f.backend, c); err != nil {
		t.Fatal(err)
	}
	if f.states[standalone] != "inactive" {
		t.Fatal("unselected server started")
	}
	// Disabled Network uses saved membership only for future configuration.
	f.states[id], f.containers[id] = "inactive", "exited"
	h := networkLifecycleControls(f)
	c.Enabled = false
	if err := networkApply(context.Background(), f.backend, c); err != nil {
		t.Fatal(err)
	}
	if len(h.starts) != 0 || f.states[id] != "inactive" || f.states[standalone] != "inactive" {
		t.Fatal("disable started a stopped server", h.starts)
	}
}

func TestNetworkPublicLifecycleAndBootProtection(t *testing.T) {
	f, c := networkTransactionFixture(t)
	ctx := context.Background()
	if err := networkApply(ctx, f.backend, c); err != nil {
		t.Fatal(err)
	}
	id := c.Members[0]
	record, err := f.backend.readRecord(id)
	if err != nil {
		t.Fatal(err)
	}
	h := networkLifecycleControls(f)
	if err := f.backend.publicActionWithWarnings(ctx, id, "stop", true); err == nil || err.Error() != networkManagedStopMessage || len(h.stops) != 0 {
		t.Fatal("public Stop escaped ownership", err, h.stops)
	}
	if err := networkProtectBootPreference(record, !record.Runtime.StartOnBoot); err == nil {
		t.Fatal("member boot mutation accepted")
	}
	if err := networkProtectBootPreference(record, record.Runtime.StartOnBoot); err != nil {
		t.Fatal("preserved preference rejected", err)
	}
	if err := f.backend.actionWithWarnings(ctx, id, "stop", true); err != nil {
		t.Fatal("internal maintenance stop blocked", err)
	}
	if err := f.backend.publicActionWithWarnings(ctx, id, "start", false); err != nil {
		t.Fatal("public recovery start blocked", err)
	}
	if err := f.backend.publicActionWithWarnings(ctx, id, "restart", true); err != nil {
		t.Fatal("public maintenance restart blocked", err)
	}
	record.NetworkMember = false
	if err := networkProtectBootPreference(record, !record.Runtime.StartOnBoot); err != nil {
		t.Fatal("standalone boot mutation blocked", err)
	}
}

func TestNetworkProspectiveMemoryBudgets(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		selectedSecond, otherBoot bool
		state                     string
		total                     int64
		fits                      bool
	}{
		{"selected stopped exact fit", true, false, "inactive", 5632, true},
		{"selected stopped one MiB over", true, false, "inactive", 5631, false},
		{"selected running counted once", true, true, "active", 5632, true},
		{"running standalone exact fit", false, false, "active", 5632, true},
		{"running standalone", false, false, "active", 5631, false},
		{"activating standalone exact fit", false, false, "activating", 5632, true},
		{"activating standalone", false, false, "activating", 5631, false},
		{"stopped standalone ignored live", false, false, "inactive", 3584, true},
		{"boot standalone exact fit", false, true, "inactive", 5632, true},
		{"boot standalone counted stopped", false, true, "inactive", 5631, false},
		{"unknown standalone fails closed", false, false, "deactivating", 16384, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c := networkTransactionFixture(t, true)
			records := networkOwnershipRecords(t, f)
			for i := range records {
				records[i].Runtime.ContainerMemory = "2048M"
				records[i].Runtime.StartOnBoot = false
			}
			records[1].Runtime.StartOnBoot = tc.otherBoot
			f.states[records[1].Runtime.ID] = tc.state
			f.containers[records[1].Runtime.ID] = "exited"
			if tc.state == "active" {
				f.containers[records[1].Runtime.ID] = "running"
			}
			if tc.selectedSecond {
				c.Members = append(c.Members, records[1].Runtime.ID)
			}
			err := f.backend.admitNetworkMemory(context.Background(), tc.total, records, c)
			if (err == nil) != tc.fits {
				t.Fatal("unexpected admission", err)
			}
			c.Enabled = false
			if err := f.backend.admitNetworkMemory(context.Background(), tc.total, records, c); err != nil {
				t.Fatal("disabled Network introduced reservation", err)
			}
		})
	}
}

func TestNetworkCapacityFailureBeforeAnyPreparationOrMutation(t *testing.T) {
	for _, bootOnly := range []bool{false, true} {
		f, c := networkTransactionFixture(t, true)
		records := networkOwnershipRecords(t, f)
		var err error
		before := map[string][]byte{}
		for _, r := range records {
			dir, env, q := f.backend.paths(r.Runtime.ID)
			for _, path := range []string{filepath.Join(dir, "config.json"), env, q} {
				before[path], err = os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		if bootOnly {
			// First is selected (2 GiB), second is stopped standalone with boot enabled.
			r := records[1]
			r.Runtime.StartOnBoot = true
			dir, _, _ := f.backend.paths(r.Runtime.ID)
			if err := f.backend.writeJSON(filepath.Join(dir, "config.json"), r, true); err != nil {
				t.Fatal(err)
			}
			before[filepath.Join(dir, "config.json")], err = os.ReadFile(filepath.Join(dir, "config.json"))
			if err != nil {
				t.Fatal(err)
			}
		} else {
			c.Members = append(c.Members, records[1].Runtime.ID)
		}
		f.snapshot.TotalMemoryMiB = 5631
		original := f.backend.command
		f.backend.command = func(ctx context.Context, command string, args ...string) ([]byte, error) {
			// Existing prerequisite preflight and runtime observations are read-only;
			// Network helpers, player-aware Stop, lifecycle and firewall changes are forbidden.
			if command == minecraftNetworkHelper || command == "firewall-cmd" || (command == "nsenter" && !(len(args) > 3 && args[2] == instanceRuntimeHelper && args[3] == "preflight")) || (command == "systemctl" && args[0] != "show") || (command == "podman" && args[0] != "inspect" && args[0] != "ps") {
				t.Fatalf("capacity failure reached preparation or disruption: %s %v", command, args)
			}
			return original(ctx, command, args...)
		}
		if err := networkApply(context.Background(), f.backend, c); err == nil || !strings.Contains(err.Error(), "within available memory") {
			t.Fatal(err)
		}
		for path, data := range before {
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(data, after) {
				t.Fatal("capacity rejection mutated files", path, err)
			}
		}
		if _, err := os.Lstat(f.backend.networkDirectory()); !os.IsNotExist(err) {
			t.Fatal("capacity rejection created Network state", err)
		}
	}
}

func TestIndividualMemoryIncludesNetworkProxyAndMemberBoot(t *testing.T) {
	f, c := networkTransactionFixture(t, true)
	if err := networkApply(context.Background(), f.backend, c); err != nil {
		t.Fatal(err)
	}
	records := networkOwnershipRecords(t, f)
	for i := range records {
		records[i].Runtime.StartOnBoot = false
		records[i].Runtime.ContainerMemory = "2048M"
	}
	member, standalone := records[0].Runtime, records[1].Runtime
	f.states[member.ID], f.containers[member.ID] = "inactive", "exited"
	f.states[standalone.ID], f.containers[standalone.ID] = "inactive", "exited"
	for _, candidate := range []minecraftInstanceRuntimeConfig{member, standalone} {
		if err := f.backend.admitMemory(context.Background(), 3584, records, candidate, false); err != nil {
			t.Fatal("exact live boundary rejected", err)
		}
		if err := f.backend.admitMemory(context.Background(), 3583, records, candidate, false); err == nil {
			t.Fatal("Velocity missing from live admission")
		}
	}
	// Other active/activating units reserve full limits, while the live
	// candidate is counted exactly once even if it is already activating.
	for _, state := range []string{"active", "activating"} {
		f.states[member.ID], f.containers[member.ID] = state, "running"
		f.states[standalone.ID] = "activating"
		if err := f.backend.admitMemory(context.Background(), 5632, records, standalone, false); err != nil {
			t.Fatal("active limit plus candidate live exact fit rejected", state, err)
		}
		if err := f.backend.admitMemory(context.Background(), 5631, records, standalone, false); err == nil {
			t.Fatal("active limit omitted from live admission", state)
		}
	}
	f.states[member.ID], f.containers[member.ID] = "inactive", "exited"
	f.states[standalone.ID], f.containers[standalone.ID] = "inactive", "exited"
	if err := f.backend.admitMemory(context.Background(), 5632, records, standalone, true); err != nil {
		t.Fatal("member plus candidate boot exact fit rejected", err)
	}
	if err := f.backend.admitMemory(context.Background(), 5631, records, standalone, true); err == nil {
		t.Fatal("member boot or Velocity reservation omitted")
	}
	// A member candidate replaces its own boot reservation; a standalone
	// boot-enabled peer remains reserved independently of current activity.
	records[1].Runtime.StartOnBoot = true
	for _, total := range []int64{5632, 5631} {
		err := f.backend.admitMemory(context.Background(), total, records, member, true)
		if (err == nil) != (total == 5632) {
			t.Fatal("incorrect member candidate plus standalone boot boundary", total, err)
		}
	}
	records[1].Runtime.StartOnBoot = false
	n, err := networkRead(f.backend)
	if err != nil {
		t.Fatal(err)
	}
	n.Config.Enabled = false
	if err := networkWriteState(f.backend, n); err != nil {
		t.Fatal(err)
	}
	if err := f.backend.admitMemory(context.Background(), 3072, records, standalone, false); err != nil {
		t.Fatal("disabled Network reserved Velocity", err)
	}
	if err := f.backend.writePrivate(filepath.Join(f.backend.networkDirectory(), "state.json"), []byte("{"), true); err != nil {
		t.Fatal(err)
	}
	if err := f.backend.admitMemory(context.Background(), 16384, records, standalone, false); err == nil {
		t.Fatal("invalid Network evidence admitted start")
	}
}

func TestNetworkPublicAPIEnforcesOwnership(t *testing.T) {
	f, c := networkTransactionFixture(t)
	ctx := context.Background()
	if err := networkApply(ctx, f.backend, c); err != nil {
		t.Fatal(err)
	}
	oldBackend, oldLock := instanceRuntimes, pluginRuntimeLockPath
	instanceRuntimes = f.backend
	pluginRuntimeLockPath = filepath.Join(t.TempDir(), "maintenance.lock")
	t.Cleanup(func() { instanceRuntimes = oldBackend; pluginRuntimeLockPath = oldLock })
	s := roleServerForTest(roleAdministrator)
	s.operations = openTestOperationStore(t)
	mux := http.NewServeMux()
	registerMinecraftRoutes(mux, s)
	id := c.Members[0]
	record, err := f.backend.readRecord(id)
	if err != nil {
		t.Fatal(err)
	}
	stop := httptest.NewRecorder()
	mux.ServeHTTP(stop, authorizedRequest("POST", "/v1/minecraft/instances/"+id+"/stop", `{}`))
	if stop.Code != http.StatusConflict || !strings.Contains(stop.Body.String(), networkManagedStopMessage) || f.states[id] != "active" {
		t.Fatal("direct API Stop escaped Network ownership", stop.Code, stop.Body.String())
	}
	settings := instancePublicSettings(record.Runtime)
	settings.StartOnBoot = !settings.StartOnBoot
	body, err := json.Marshal(map[string]any{"runtime": settings})
	if err != nil {
		t.Fatal(err)
	}
	boot := httptest.NewRecorder()
	mux.ServeHTTP(boot, authorizedRequest("PUT", "/v1/minecraft/instances/"+id+"/configuration", string(body)))
	if boot.Code != http.StatusConflict || !strings.Contains(boot.Body.String(), "managed by Minecraft Network") {
		t.Fatal(boot.Code, boot.Body.String())
	}
	after, err := f.backend.readRecord(id)
	if err != nil || after.Runtime.StartOnBoot != record.Runtime.StartOnBoot {
		t.Fatal("API changed saved preference", err)
	}
	if err := f.backend.action(ctx, id, "stop"); err != nil {
		t.Fatal(err)
	}
	// A preserved preference permits unrelated normal settings edits.
	settings.StartOnBoot = record.Runtime.StartOnBoot
	settings.MOTD = "Network family"
	body, err = json.Marshal(map[string]any{"runtime": settings})
	if err != nil {
		t.Fatal(err)
	}
	edit := httptest.NewRecorder()
	mux.ServeHTTP(edit, authorizedRequest("PUT", "/v1/minecraft/instances/"+id+"/configuration", string(body)))
	if edit.Code != http.StatusOK {
		t.Fatal("unrelated settings blocked", edit.Code, edit.Body.String())
	}
	for _, action := range []string{"start", "restart"} {
		result := httptest.NewRecorder()
		mux.ServeHTTP(result, authorizedRequest("POST", "/v1/minecraft/instances/"+id+"/"+action, `{}`))
		if result.Code != http.StatusOK || f.states[id] != "active" {
			t.Fatal(action, result.Code, result.Body.String())
		}
	}
}

func TestNetworkMembershipRoundTripPreservesSavedBoot(t *testing.T) {
	for _, saved := range []bool{false, true} {
		f, c := networkTransactionFixture(t)
		id := c.Members[0]
		record, err := f.backend.readRecord(id)
		if err != nil {
			t.Fatal(err)
		}
		records, err := f.backend.records()
		if err != nil {
			t.Fatal(err)
		}
		next := record
		next.Runtime.StartOnBoot = saved
		if err := f.backend.apply(context.Background(), record, instanceReplaceRecord(records, next), f.snapshot); err != nil {
			t.Fatal(err)
		}
		standalone, err := f.backend.readRecord(id)
		if err != nil {
			t.Fatal(err)
		}
		if err := networkApply(context.Background(), f.backend, c); err != nil {
			t.Fatal(err)
		}
		member, err := f.backend.readRecord(id)
		if err != nil || !member.NetworkMember || member.Runtime.StartOnBoot != saved {
			t.Fatal("enrollment rewrote saved preference", err)
		}
		_, _, quadlet := f.backend.paths(id)
		data, err := os.ReadFile(quadlet)
		if err != nil || strings.Count(string(data), "[Install]") != 1 || !strings.Contains(string(data), "WantedBy=multi-user.target") {
			t.Fatal("enrolled boot missing", err)
		}
		c.Enabled = false
		if err := networkApply(context.Background(), f.backend, c); err != nil {
			t.Fatal(err)
		}
		removed, err := f.backend.readRecord(id)
		if err != nil || removed.NetworkMember || removed.Runtime.StartOnBoot != saved || removed.QuadletSHA256 != standalone.QuadletSHA256 {
			t.Fatal("removal did not restore saved standalone boot", err)
		}
	}
}

func TestPublicStopUsesAuthoritativeMembership(t *testing.T) {
	f, c := networkTransactionFixture(t)
	ctx := context.Background()
	if err := networkApply(ctx, f.backend, c); err != nil {
		t.Fatal(err)
	}
	n, err := networkRead(f.backend)
	if err != nil {
		t.Fatal(err)
	}
	// An ID in saved configuration is not sufficient to confer ownership.
	standalone := f.configs[1].ID
	n.Config.Members = append(n.Config.Members, standalone)
	if err := networkWriteState(f.backend, n); err != nil {
		t.Fatal(err)
	}
	if err := f.backend.publicActionWithWarnings(ctx, standalone, "stop", true); err != nil {
		t.Fatal("saved list blocked standalone stop", err)
	}
	// Conversely, enabled authoritative ownership is protected even if the list
	// is inconsistent. Stop never uses saved selection as an authorization source.
	n.Config.Members = nil
	if err := networkWriteState(f.backend, n); err != nil {
		t.Fatal(err)
	}
	if err := f.backend.publicActionWithWarnings(ctx, c.Members[0], "stop", true); err == nil || err.Error() != networkManagedStopMessage {
		t.Fatal(err)
	}
	n.Config.Enabled = false
	if err := networkWriteState(f.backend, n); err != nil {
		t.Fatal(err)
	}
	if err := f.backend.publicActionWithWarnings(ctx, c.Members[0], "stop", true); err != nil {
		t.Fatal("disabled Network retained Stop ownership", err)
	}
}
