package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Execute the production fixed helper in an isolated command fixture. All runtime
// and network boundaries are stubs; instance-context and the planner are real.
type containerFixture struct {
	t                         *testing.T
	root, helper, remote, old string
}

func newContainerFixture(t *testing.T) *containerFixture {
	t.Helper()
	f := &containerFixture{t: t, root: t.TempDir(), remote: "sha256:" + strings.Repeat("2", 64), old: "sha256:" + strings.Repeat("1", 64)}
	f.helper = filepath.Join(f.root, "minecraft-container-update")
	for _, name := range []string{"instances/jv-a1b2c3", "instances/jv-sibling", "bin"} {
		if err := os.MkdirAll(filepath.Join(f.root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	f.write("active", "active")
	f.write("running", "old-id")
	f.write("players", "There are 0 of a max of 20 players online:")
	f.write("remote", f.remote)
	f.write("calls", "")
	record := `{"schema_version":1,"runtime":{"id":"jv-a1b2c3","software":"paper","image_tag":"stable","image_digest":"` + f.old + `","version":"26.2","version_policy":"recommended","uid":1000,"gid":1000,"bedrock_enabled":true},"storage":{"root":"/fixture","data_path":"/fixture/minecraft-instances/jv-a1b2c3/data"}}`
	f.write("instances/jv-a1b2c3/config.json", record)
	f.write("instances/jv-sibling/config.json", "sibling must remain identical")
	common := `sleep(){ :; }
require_root(){ :; }
validate_server_capabilities(){ :; }
JV_MINECRAFT_IMAGE_REPO=docker.io/itzg/minecraft-server
JV_MAINTENANCE_LOCK="$FIXTURE_ROOT/lock"
JV_SETUP_IN_PROGRESS="$FIXTURE_ROOT/setup"
systemctl(){
 printf 'systemctl %s\n' "$*" >> "$FIXTURE_ROOT/calls"
 case "$1" in
 show) cat "$FIXTURE_ROOT/active";;
 is-active) [[ $(cat "$FIXTURE_ROOT/active") == active ]];;
 --no-block|stop) printf inactive > "$FIXTURE_ROOT/active";;
 start)
  printf active > "$FIXTURE_ROOT/active"
  if [[ $(jq -r .runtime.image_digest "$FIXTURE_ROOT/instances/jv-a1b2c3/config.json") == "$FIXTURE_REMOTE" ]];then printf new-id > "$FIXTURE_ROOT/running";else printf old-id > "$FIXTURE_ROOT/running";fi;;
 *) return 93;;esac
}
podman(){
 printf 'podman %s\n' "$*" >> "$FIXTURE_ROOT/calls"
 case "$1" in
 pull) printf 'Copying blob fixture\nCopying config fixture\nWriting manifest fixture\n'; touch "$FIXTURE_ROOT/downloaded"; if [[ -f $FIXTURE_ROOT/move-remote ]]; then cat "$FIXTURE_ROOT/move-remote" > "$FIXTURE_ROOT/remote"; fi;;
 ps) [[ $(cat "$FIXTURE_ROOT/active") != active ]] || printf minecraft-jv-a1b2c3;;
 image)
  if [[ $3 == *"$FIXTURE_REMOTE" ]];then
   [[ -f $FIXTURE_ROOT/downloaded ]] || return 1
   if [[ $5 == *Digest* ]];then printf '%s' "$FIXTURE_REMOTE";else printf new-id;fi
  else
   [[ ! -f $FIXTURE_ROOT/missing ]] || return 1
   if [[ $5 == *Digest* ]];then printf '%s' "$FIXTURE_OLD";else printf old-id;fi
  fi;;
 inspect)
  if [[ $4 == *State.Running* ]];then [[ $(cat "$FIXTURE_ROOT/active") == active ]] && printf true || printf false
  else cat "$FIXTURE_ROOT/running";fi;;
 exec) cat "$FIXTURE_ROOT/players";;
 kill) return 0;;
 *) return 94;;esac
}
`
	f.write("common.sh", common)
	f.write("update-policy.sh", `jv_remote_platform_digest(){ cat "$FIXTURE_ROOT/remote"; }`)
	for _, name := range []string{"minecraft-container-update", "minecraft-container-plan.sh", "instance-context.sh", "migration-instance.sh", "interrupt-safety.sh", "operation-trace.sh"} {
		data, err := os.ReadFile(filepath.Join("../../../mjust/libexec", name))
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ReplaceAll(string(data), "/usr/libexec/justvoxel/mjust/", f.root+"/")
		text = strings.ReplaceAll(text, "/usr/libexec/justvoxel/management-agent", f.root+"/management-agent")
		text = strings.ReplaceAll(text, "/etc/justvoxel/instances", f.root+"/instances")
		f.write(name, text)
	}
	f.write("validate-instance-runtime-commit", "#!/bin/bash\n[[ $1 == jv-a1b2c3 ]]\n")
	f.write("verify-minecraft-stack", "#!/bin/bash\nprintf 'verify %s\\n' \"$*\" >> \"$FIXTURE_ROOT/calls\"\n")
	f.write("management-agent", `#!/bin/bash
printf 'agent %s\n' "$*" >> "$FIXTURE_ROOT/calls"
[[ $2 == jv-a1b2c3 ]] || exit 95
case "$1" in
 instance-container-publish)
  jq --arg digest "$3" '.runtime.image_digest=$digest' "$FIXTURE_ROOT/instances/$2/config.json" > "$FIXTURE_ROOT/next"
  mv "$FIXTURE_ROOT/next" "$FIXTURE_ROOT/instances/$2/config.json";;
 instance-version-backup) [[ $(cat "$FIXTURE_ROOT/active") == inactive ]];;
 *) exit 96;;esac
`)
	return f
}
func (f *containerFixture) write(name, text string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.root, name), []byte(text), 0700); err != nil {
		f.t.Fatal(err)
	}
}
func (f *containerFixture) run(args ...string) ([]byte, error) {
	cmd := exec.Command("bash", append([]string{f.helper, "--instance", "jv-a1b2c3"}, args...)...)
	cmd.Env = append(os.Environ(), "FIXTURE_ROOT="+f.root, "FIXTURE_REMOTE="+f.remote, "FIXTURE_OLD="+f.old)
	return cmd.CombinedOutput()
}
func (f *containerFixture) status() minecraftContainerStatus {
	f.t.Helper()
	data, err := f.run("status")
	if err != nil {
		f.t.Fatalf("status fixture: %v %s", err, data)
	}
	var out minecraftContainerStatus
	if err = json.Unmarshal(data, &out); err != nil {
		f.t.Fatalf("status JSON: %s", data)
	}
	return out
}
func (f *containerFixture) text(name string) string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.root, name))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(data)
}
func (f *containerFixture) apply(plan minecraftContainerStatus, restart, backup, players bool) ([]byte, error) {
	return f.run("update", plan.PlanFingerprint, boolText(restart), boolText(backup), boolText(players))
}

func TestContainerHelperImageMatrixAndNoReleaseMetadata(t *testing.T) {
	for _, item := range []struct {
		name, want string
		setup      func(*containerFixture)
	}{
		{"new remote", "update_available", func(f *containerFixture) {}},
		{"missing local", "image_missing", func(f *containerFixture) { f.write("missing", "") }},
		{"remote failure", "unavailable", func(f *containerFixture) { f.write("remote", "invalid") }},
		{"current", "up_to_date", func(f *containerFixture) { f.write("remote", f.old) }},
	} {
		t.Run(item.name, func(t *testing.T) {
			f := newContainerFixture(t)
			item.setup(f)
			plan := f.status()
			if plan.State != item.want || plan.InstanceID != "jv-a1b2c3" || plan.Image != "docker.io/itzg/minecraft-server:stable" {
				t.Fatalf("%+v", plan)
			}
			if strings.Contains(f.text("calls"), "release") {
				t.Fatal("release metadata dependency")
			}
		})
	}
}
func TestContainerHelperPullOnlyPreservesRuntimeAndSibling(t *testing.T) {
	for _, active := range []bool{true, false} {
		t.Run(boolText(active), func(t *testing.T) {
			f := newContainerFixture(t)
			if !active {
				f.write("active", "inactive")
			}
			before := f.text("instances/jv-a1b2c3/config.json")
			sibling := f.text("instances/jv-sibling/config.json")
			plan := f.status()
			f.write("calls", "")
			if data, err := f.apply(plan, false, false, false); err != nil {
				t.Fatalf("pull: %v %s", err, data)
			}
			calls := f.text("calls")
			for _, forbidden := range []string{"systemctl start", "systemctl stop", "systemctl --no-block", "instance-version-backup", "verify "} {
				if strings.Contains(calls, forbidden) {
					t.Fatalf("pull-only used %s", forbidden)
				}
			}
			if !strings.Contains(calls, "podman pull docker.io/itzg/minecraft-server@"+f.remote) {
				t.Fatal("exact image not pulled")
			}
			var previous, next map[string]any
			_ = json.Unmarshal([]byte(before), &previous)
			_ = json.Unmarshal([]byte(f.text("instances/jv-a1b2c3/config.json")), &next)
			previous["runtime"].(map[string]any)["image_digest"] = f.remote
			a, _ := json.Marshal(previous)
			b, _ := json.Marshal(next)
			if string(a) != string(b) {
				t.Fatal("non-image settings changed")
			}
			if f.text("instances/jv-sibling/config.json") != sibling {
				t.Fatal("sibling changed")
			}
			status := f.status()
			want := "up_to_date"
			if active {
				want = "restart_needed"
			}
			if status.State != want {
				t.Fatalf("after pull: %+v", status)
			}
		})
	}
}
func TestContainerHelperRestartChecksOptionalBackupAndNoRedundantPull(t *testing.T) {
	for _, backup := range []bool{true, false} {
		t.Run(boolText(backup), func(t *testing.T) {
			f := newContainerFixture(t)
			plan := f.status()
			if data, err := f.apply(plan, true, backup, false); err != nil {
				t.Fatalf("restart: %v %s", err, data)
			}
			calls := f.text("calls")
			if strings.Contains(calls, "instance-version-backup") != backup {
				t.Fatal("backup choice lost")
			}
			for _, want := range []string{"systemctl --no-block stop minecraft-jv-a1b2c3.service", "systemctl start minecraft-jv-a1b2c3.service", "verify --instance jv-a1b2c3 26.2"} {
				if !strings.Contains(calls, want) {
					t.Fatalf("missing %s", want)
				}
			}
			if f.status().State != "up_to_date" {
				t.Fatal("restart not verified")
			}
		})
	}
	f := newContainerFixture(t)
	plan := f.status()
	if data, err := f.apply(plan, false, false, false); err != nil {
		t.Fatalf("%v %s", err, data)
	}
	plan = f.status()
	f.write("calls", "")
	if data, err := f.apply(plan, true, false, false); err != nil {
		t.Fatalf("%v %s", err, data)
	}
	if strings.Contains(f.text("calls"), "podman pull") {
		t.Fatal("restart pulled the same image")
	}
}
func TestContainerHelperFailsClosedBeforeDisruption(t *testing.T) {
	for _, reason := range []string{"stale", "unknown players", "online players", "stopped"} {
		t.Run(reason, func(t *testing.T) {
			f := newContainerFixture(t)
			if reason == "stopped" {
				f.write("active", "inactive")
			}
			plan := f.status()
			switch reason {
			case "stale":
				f.write("remote", "sha256:"+strings.Repeat("3", 64))
			case "unknown players":
				f.write("players", "unavailable")
			case "online players":
				f.write("players", "There are 1 of a max of 20 players online: Alex")
			}
			f.write("calls", "")
			if _, err := f.apply(plan, true, false, false); err == nil {
				t.Fatal("unsafe restart accepted")
			}
			if reason == "stale" && strings.Contains(f.text("calls"), "podman pull") {
				t.Fatal("stale plan pulled before rejection")
			}
			for _, forbidden := range []string{"systemctl start", "systemctl stop", "systemctl --no-block", "instance-container-publish", "instance-version-backup"} {
				if strings.Contains(f.text("calls"), forbidden) {
					t.Fatalf("unsafe mutation %s", forbidden)
				}
			}
		})
	}
}

func TestContainerHelperExplicitPlayerConfirmation(t *testing.T) {
	f := newContainerFixture(t)
	f.write("players", "There are 1 of a max of 20 players online: Alex")
	plan := f.status()
	if data, err := f.apply(plan, true, false, true); err != nil {
		t.Fatalf("confirmed restart: %v %s", err, data)
	}
	if !strings.Contains(f.text("calls"), "systemctl --no-block stop minecraft-jv-a1b2c3.service") {
		t.Fatal("confirmed graceful stop missing")
	}
}

func TestContainerTagMovementDuringPullDoesNotPublishOrDisrupt(t *testing.T) {
	f := newContainerFixture(t)
	plan := f.status()
	f.write("move-remote", "sha256:"+strings.Repeat("3", 64))
	f.write("calls", "")
	if _, err := f.apply(plan, false, false, false); err == nil {
		t.Fatal("tag changed during pull but apply succeeded")
	}
	for _, forbidden := range []string{"instance-container-publish", "systemctl start", "systemctl stop", "systemctl --no-block", "instance-version-backup"} {
		if strings.Contains(f.text("calls"), forbidden) {
			t.Fatalf("moving tag invoked %s", forbidden)
		}
	}
}

func TestContainerStoppedMissingImageCanBeAcknowledgedWithoutStarting(t *testing.T) {
	f := newContainerFixture(t)
	f.write("active", "inactive")
	f.write("missing", "")
	f.write("calls", "")
	if data, err := f.run("verify"); err != nil {
		t.Fatalf("safe missing-image retry stranded: %v %s", err, data)
	}
	for _, forbidden := range []string{"systemctl start", "systemctl stop", "instance-container-publish", "podman pull"} {
		if strings.Contains(f.text("calls"), forbidden) {
			t.Fatalf("acknowledgement invoked %s", forbidden)
		}
	}
}
