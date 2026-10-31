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
	common := `sleep(){
 if [[ -f $FIXTURE_ROOT/stopping ]]; then
  printf inactive > "$FIXTURE_ROOT/active"
  printf '' > "$FIXTURE_ROOT/running"
  rm "$FIXTURE_ROOT/stopping"
 fi
}
require_root(){ :; }
validate_server_capabilities(){ :; }
JV_MINECRAFT_IMAGE_REPO=docker.io/itzg/minecraft-server
JV_MAINTENANCE_LOCK="$FIXTURE_ROOT/lock"
JV_SETUP_IN_PROGRESS="$FIXTURE_ROOT/setup"
flock(){
 command flock "$@" || return $?
 printf 'flock %s\n' "$*" >> "$FIXTURE_ROOT/calls"
}
systemctl(){
 printf 'systemctl %s\n' "$*" >> "$FIXTURE_ROOT/calls"
 case "$1" in
 show) cat "$FIXTURE_ROOT/active";;
 is-active) [[ $(cat "$FIXTURE_ROOT/active") == active ]];;
 --no-block)
  [[ $# == 3 && $2 == stop && $3 == minecraft-jv-a1b2c3.service ]] || return 93
  [[ ! -f $FIXTURE_ROOT/stop-fail ]] || return 18
  printf deactivating > "$FIXTURE_ROOT/active"
  touch "$FIXTURE_ROOT/stopping";;
 stop)
  [[ $# == 2 && $2 == minecraft-jv-a1b2c3.service ]] || return 93
  printf inactive > "$FIXTURE_ROOT/active"
  printf '' > "$FIXTURE_ROOT/running";;
 start)
  [[ $# == 2 && $2 == minecraft-jv-a1b2c3.service ]] || return 93
  [[ $(cat "$FIXTURE_ROOT/active") != active ]] || return 0
  # Model ExecStartPre opening the same lock independently of updater fd 9.
  if ! command flock -n "$JV_MAINTENANCE_LOCK" true; then
   printf 'crossplay lock blocked\n' >> "$FIXTURE_ROOT/calls"
   return 98
  fi
  printf 'crossplay lock acquired\n' >> "$FIXTURE_ROOT/calls"
  if [[ -f $FIXTURE_ROOT/start-fail ]]; then rm "$FIXTURE_ROOT/start-fail"; return 19; fi
  printf active > "$FIXTURE_ROOT/active"
  if [[ $(jq -r .runtime.image_digest "$FIXTURE_ROOT/instances/jv-a1b2c3/config.json") == "$FIXTURE_REMOTE" ]];then if [[ -f $FIXTURE_ROOT/wrong-new-id ]]; then printf wrong-id > "$FIXTURE_ROOT/running"; else printf new-id > "$FIXTURE_ROOT/running"; fi;else printf old-id > "$FIXTURE_ROOT/running";fi;;
 *) return 93;;esac
}
podman(){
 printf 'podman %s\n' "$*" >> "$FIXTURE_ROOT/calls"
 case "$1" in
 pull) [[ ! -f $FIXTURE_ROOT/pull-fail ]] || return 17; printf 'Copying blob fixture\nCopying config fixture\nWriting manifest fixture\n'; touch "$FIXTURE_ROOT/downloaded"; if [[ -f $FIXTURE_ROOT/move-remote ]]; then cat "$FIXTURE_ROOT/move-remote" > "$FIXTURE_ROOT/remote"; fi;;
 ps) [[ $(cat "$FIXTURE_ROOT/active") != active ]] || printf minecraft-jv-a1b2c3;;
 image)
  [[ $# == 5 && $2 == inspect && $4 == --format ]] || return 94
  if [[ $3 == *"$FIXTURE_REMOTE" || $3 == new-id ]];then
   [[ -f $FIXTURE_ROOT/downloaded ]] || return 1
   if [[ $5 == *Digest* ]];then printf '%s' "$FIXTURE_REMOTE";else printf new-id;fi
  else
   [[ $3 == old-id || $3 == *"$FIXTURE_OLD" || $3 == docker.io/itzg/minecraft-server:stable ]] || return 94
   [[ ! -f $FIXTURE_ROOT/missing ]] || return 1
   if [[ $5 == *Digest* ]];then printf '%s' "$FIXTURE_OLD";else printf old-id;fi
  fi;;
 inspect)
  if [[ $2 == --format ]]; then format=$3; container=$4; else container=$2; [[ $3 == --format ]] || return 94; format=$4; fi
  [[ $container == minecraft-jv-a1b2c3 ]] || return 94
  case "$format" in
   '{{.State.Running}}') [[ -n $(cat "$FIXTURE_ROOT/running") ]] && printf true || printf false;;
   '{{.Image}}') [[ -n $(cat "$FIXTURE_ROOT/running") ]] || return 1; cat "$FIXTURE_ROOT/running";;
   *) return 94;;esac;;
 exec) [[ $2 == minecraft-jv-a1b2c3 && $3 == rcon-cli && -n $(cat "$FIXTURE_ROOT/running") ]] || return 1; cat "$FIXTURE_ROOT/players";;
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
	f.write("verify-minecraft-stack", "#!/bin/bash\nif [[ -f $FIXTURE_ROOT/verify-new-fail && $(cat $FIXTURE_ROOT/running) == new-id ]]; then exit 22; fi\nif [[ -f $FIXTURE_ROOT/fail-old && $(jq -r .runtime.image_digest $FIXTURE_ROOT/instances/jv-a1b2c3/config.json) == $FIXTURE_OLD ]]; then exit 1; fi\nprintf 'verify %s\\n' \"$*\" >> \"$FIXTURE_ROOT/calls\"\n")
	f.write("management-agent", `#!/bin/bash
printf 'agent %s\n' "$*" >> "$FIXTURE_ROOT/calls"
if [[ $1 == instance-container-history ]]; then
 [[ $3 == jv-a1b2c3 ]] || exit 95
 history="$FIXTURE_ROOT/history.json"
 [[ -f $history ]] || printf '{"keep_previous_images":1,"previous_images":[]}' > "$history"
 case "$2" in
 status) jq --arg selected "$(jq -r .runtime.image_digest "$FIXTURE_ROOT/instances/$3/config.json")" '.previous_images |= map(select(.digest != $selected))' "$history";;
 record)
  [[ ! -f $FIXTURE_ROOT/history-fail ]] || exit 24
  jq --arg old "$4" --arg selected "$5" '.previous_images=([if $old!="" and $old!=$selected then {digest:$old,image:"docker.io/itzg/minecraft-server:stable",retained_at:"2026-10-27T00:00:00Z",size:1024} else empty end]+[.previous_images[]|select(.digest!=$old and .digest!=$selected)]) | .previous_images=.previous_images[:.keep_previous_images]' "$history" > "$history.tmp"
  mv "$history.tmp" "$history";;
 retention) jq --argjson n "$4" '.keep_previous_images=$n | .previous_images=.previous_images[:$n]' "$history" > "$history.tmp"; mv "$history.tmp" "$history";;
 *) exit 96;;esac
 exit
fi
[[ $2 == jv-a1b2c3 ]] || exit 95
case "$1" in
 instance-container-publish)
  printf 'publish-state %s %s\n' "$(cat "$FIXTURE_ROOT/active")" "$(cat "$FIXTURE_ROOT/running")" >> "$FIXTURE_ROOT/calls"
  if [[ -e $FIXTURE_ROOT/instances/$2/pending.json ]]; then
   printf 'recover pending\n' >> "$FIXTURE_ROOT/calls"
   jq --arg digest "$FIXTURE_OLD" '.runtime.image_digest=$digest' "$FIXTURE_ROOT/instances/$2/config.json" > "$FIXTURE_ROOT/next"
   mv "$FIXTURE_ROOT/next" "$FIXTURE_ROOT/instances/$2/config.json"
   rm "$FIXTURE_ROOT/instances/$2/pending.json"
  fi
  if [[ $3 == "$FIXTURE_REMOTE" && -f $FIXTURE_ROOT/publish-fail ]]; then return_code=20; exit "$return_code"; fi
  jq --arg digest "$3" '.runtime.image_digest=$digest' "$FIXTURE_ROOT/instances/$2/config.json" > "$FIXTURE_ROOT/next"
  mv "$FIXTURE_ROOT/next" "$FIXTURE_ROOT/instances/$2/config.json"
  if [[ $3 == "$FIXTURE_REMOTE" && -f $FIXTURE_ROOT/publish-partial ]]; then
   touch "$FIXTURE_ROOT/instances/$2/pending.json"
   exit 21
  fi;;
 instance-version-backup) [[ ! -f $FIXTURE_ROOT/backup-fail ]] || exit 23; [[ $(cat "$FIXTURE_ROOT/active") == inactive ]];;
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
func (f *containerFixture) command(args ...string) *exec.Cmd {
	cmd := exec.Command("bash", append([]string{f.helper, "--instance", "jv-a1b2c3"}, args...)...)
	cmd.Env = append(os.Environ(), "FIXTURE_ROOT="+f.root, "FIXTURE_REMOTE="+f.remote, "FIXTURE_OLD="+f.old)
	return cmd
}
func (f *containerFixture) run(args ...string) ([]byte, error) {
	cmd := f.command(args...)
	trace, err := os.OpenFile(filepath.Join(f.root, "trace"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		f.t.Fatal(err)
	}
	defer trace.Close()
	cmd.ExtraFiles = []*os.File{trace}
	cmd.Env = append(cmd.Env, "JV_OPERATION_TRACE_FD=3")
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
				f.write("running", "")
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
				f.write("running", "")
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
	f.write("running", "")
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

func TestContainerFailedPullPreservesSelectedDigestAndHistory(t *testing.T) {
	f := newContainerFixture(t)
	plan := f.status()
	before := f.text("instances/jv-a1b2c3/config.json")
	history := f.text("history.json")
	f.write("pull-fail", "")
	if _, err := f.apply(plan, true, true, true); err == nil {
		t.Fatal("failed pull succeeded")
	}
	if f.text("instances/jv-a1b2c3/config.json") != before || f.text("history.json") != history {
		t.Fatal("failed pull published image or history")
	}
	for _, forbidden := range []string{"instance-container-publish", "instance-container-history record", "systemctl --no-block", "instance-version-backup"} {
		if strings.Contains(f.text("calls"), forbidden) {
			t.Fatal(forbidden)
		}
	}
}
func TestContainerSuccessfulUpdateRetainsServingImageAndRestoresExactImage(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(boolText(running), func(t *testing.T) {
			f := newContainerFixture(t)
			if !running {
				f.write("active", "inactive")
				f.write("running", "")
			}
			if data, err := f.apply(f.status(), running, false, false); err != nil {
				t.Fatalf("update: %v %s", err, data)
			}
			status := f.status()
			if status.KeepPreviousImages != 1 || len(status.PreviousImages) != 1 || status.PreviousImages[0].Digest != f.old {
				t.Fatalf("history: %+v", status)
			}
			// Restore remains possible without consulting the moving tag/registry.
			f.write("remote", "offline")
			status = f.status()
			f.write("calls", "")
			if data, err := f.run("restore", status.RestoreFingerprint, boolText(running), boolText(running), "false", f.old); err != nil {
				t.Fatalf("restore: %v %s", err, data)
			}
			calls := f.text("calls")
			if strings.Contains(calls, "podman pull") {
				t.Fatal("restore fetched a moving image")
			}
			if !strings.Contains(calls, "instance-container-publish jv-a1b2c3 "+f.old) {
				t.Fatal("restore lost exact digest")
			}
			if running {
				for _, want := range []string{"systemctl --no-block stop", "instance-version-backup", "systemctl start", "verify --instance"} {
					if !strings.Contains(calls, want) {
						t.Fatal(want)
					}
				}
			} else if strings.Contains(calls, "systemctl start") || strings.Contains(calls, "systemctl --no-block") {
				t.Fatal("stopped restore disrupted server")
			}
			after := f.status()
			if len(after.PreviousImages) != 1 || after.PreviousImages[0].Digest != f.remote {
				t.Fatalf("undo restore lost: %+v", after)
			}
		})
	}
}
func TestContainerRestoreFailureRecoversPreviouslySelectedDigest(t *testing.T) {
	f := newContainerFixture(t)
	if data, err := f.apply(f.status(), true, false, false); err != nil {
		t.Fatalf("%v %s", err, data)
	}
	plan := f.status()
	history := f.text("history.json")
	f.write("calls", "")
	f.write("fail-old", "")
	if _, err := f.run("restore", plan.RestoreFingerprint, "true", "false", "false", f.old); err == nil {
		t.Fatal("failed verification accepted")
	}
	if !strings.Contains(f.text("instances/jv-a1b2c3/config.json"), f.remote) || f.text("running") != "new-id" {
		t.Fatal("previous selection/runtime not recovered")
	}
	if f.text("history.json") != history {
		t.Fatal("failed restore changed history")
	}
}
func TestContainerRestorePlayerConfirmationPrecedesShutdown(t *testing.T) {
	f := newContainerFixture(t)
	if data, err := f.apply(f.status(), true, false, false); err != nil {
		t.Fatalf("%v %s", err, data)
	}
	plan := f.status()
	f.write("players", "There are 1 of a max of 20 players online: Alex")
	f.write("calls", "")
	if _, err := f.run("restore", plan.RestoreFingerprint, "true", "false", "false", f.old); err == nil {
		t.Fatal("missing player confirmation accepted")
	}
	if strings.Contains(f.text("calls"), "systemctl --no-block") || strings.Contains(f.text("calls"), "instance-container-publish") {
		t.Fatal("disrupted before confirmation")
	}
	if data, err := f.run("restore", plan.RestoreFingerprint, "true", "false", "true", f.old); err != nil {
		t.Fatalf("%v %s", err, data)
	}
}

func TestContainerHelperRestartTransactionOrder(t *testing.T) {
	for _, backup := range []bool{false, true} {
		t.Run(boolText(backup), func(t *testing.T) {
			f := newContainerFixture(t)
			plan := f.status()
			f.write("calls", "")
			if data, err := f.apply(plan, true, backup, false); err != nil {
				t.Fatalf("%v %s", err, data)
			}
			calls := f.text("calls")
			order := []string{"podman pull", "podman image inspect docker.io/itzg/minecraft-server@" + f.remote + " --format {{.Digest}}", "agent instance-container-publish jv-a1b2c3 " + f.remote, "publish-state active old-id", "systemctl --no-block stop minecraft-jv-a1b2c3.service"}
			if backup {
				order = append(order, "agent instance-version-backup jv-a1b2c3")
			}
			order = append(order, "systemctl start minecraft-jv-a1b2c3.service", "podman exec minecraft-jv-a1b2c3 rcon-cli list", "podman inspect minecraft-jv-a1b2c3 --format {{.Image}}", "verify --instance jv-a1b2c3 26.2", "agent instance-container-history record")
			for _, want := range order {
				pos := strings.Index(calls, want)
				if pos < 0 {
					t.Fatalf("missing ordered call %q in %s", want, calls)
				}
				calls = calls[pos+len(want):]
			}
			trace := f.text("trace")
			for _, want := range []string{"STAGE Publishing selected image", "instance-container-publish", "STAGE Gracefully stopping selected server", "$ systemctl --no-block stop minecraft-jv-a1b2c3.service\n[exit 0]", "STAGE Starting and verifying selected server", "$ systemctl start minecraft-jv-a1b2c3.service\n[exit 0]"} {
				if !strings.Contains(trace, want) {
					t.Fatalf("missing trace %q: %s", want, trace)
				}
			}
		})
	}
}

func TestContainerHelperMaintenanceLockHandoff(t *testing.T) {
	for _, failStart := range []bool{false, true} {
		t.Run(boolText(failStart), func(t *testing.T) {
			f := newContainerFixture(t)
			plan := f.status()
			f.write("calls", "")
			if failStart {
				f.write("start-fail", "")
			}
			data, err := f.apply(plan, true, false, false)
			if failStart {
				if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 19 {
					t.Fatalf("start status not preserved: %v %s", err, data)
				}
				if f.text("running") != "old-id" || !strings.Contains(f.text("instances/jv-a1b2c3/config.json"), f.old) {
					t.Fatal("failed start did not recover old selection and server")
				}
			} else if err != nil {
				t.Fatalf("start under lock handoff: %v %s", err, data)
			}
			calls := f.text("calls")
			if strings.Contains(calls, "crossplay lock blocked") {
				t.Fatalf("ExecStartPre encountered updater lock: %s", calls)
			}
			order := []string{
				"flock -n 9\n",
				"agent instance-container-publish jv-a1b2c3 " + f.remote,
				"systemctl --no-block stop minecraft-jv-a1b2c3.service",
				"flock -u 9\n",
				"systemctl start minecraft-jv-a1b2c3.service",
				"crossplay lock acquired\n",
				"flock 9\n",
			}
			if failStart {
				order = append(order,
					"systemctl stop minecraft-jv-a1b2c3.service",
					"agent instance-container-publish jv-a1b2c3 "+f.old,
					"flock -u 9\n",
					"systemctl start minecraft-jv-a1b2c3.service",
					"crossplay lock acquired\n",
					"flock 9\n",
				)
			}
			order = append(order,
				"podman exec minecraft-jv-a1b2c3 rcon-cli list",
				"podman inspect minecraft-jv-a1b2c3 --format {{.Image}}",
				"verify --instance jv-a1b2c3 26.2",
			)
			if !failStart {
				order = append(order, "agent instance-container-history record")
			} else if strings.Contains(calls, "instance-container-history record") {
				t.Fatal("failed start recorded history")
			}
			for _, want := range order {
				pos := strings.Index(calls, want)
				if pos < 0 {
					t.Fatalf("missing ordered call %q in %s", want, calls)
				}
				calls = calls[pos+len(want):]
			}
		})
	}
}

func TestContainerHelperFailureRecoveryMatchesStoppedState(t *testing.T) {
	for _, failure := range []string{"publish-fail", "publish-partial", "stop-fail", "start-fail", "verify-new-fail", "wrong-new-id", "backup-fail"} {
		t.Run(failure, func(t *testing.T) {
			f := newContainerFixture(t)
			plan := f.status()
			history := f.text("history.json")
			f.write("calls", "")
			f.write(failure, "")
			if data, err := f.apply(plan, true, failure == "backup-fail", false); err == nil {
				t.Fatalf("failure accepted: %s", data)
			}
			calls, trace := f.text("calls"), f.text("trace")
			if f.text("active") != "active" || f.text("running") != "old-id" {
				t.Fatalf("old server not recovered: %s\n%s", calls, trace)
			}
			var record struct {
				Runtime struct {
					ImageDigest string `json:"image_digest"`
				} `json:"runtime"`
			}
			if err := json.Unmarshal([]byte(f.text("instances/jv-a1b2c3/config.json")), &record); err != nil {
				t.Fatal(err)
			}
			if record.Runtime.ImageDigest != f.old {
				t.Fatal("old selection not restored")
			}
			if f.text("history.json") != history || strings.Contains(calls, "instance-container-history record") {
				t.Fatal("failed update wrote history")
			}
			if strings.Contains(trace, "recovery is incomplete") || strings.Contains(trace, "requires attention") {
				t.Fatalf("false recovery failure: %s", trace)
			}
			stopped := failure == "start-fail" || failure == "verify-new-fail" || failure == "wrong-new-id" || failure == "backup-fail"
			if !stopped {
				for _, forbidden := range []string{"systemctl start", "systemctl stop"} {
					if strings.Contains(calls, forbidden) {
						t.Fatalf("unnecessary lifecycle mutation %s: %s", forbidden, calls)
					}
				}
				if failure != "stop-fail" && strings.Contains(calls, "systemctl --no-block") {
					t.Fatal("publication failure stopped server")
				}
			} else {
				for _, want := range []string{"agent instance-container-publish jv-a1b2c3 " + f.old, "systemctl start minecraft-jv-a1b2c3.service", "podman inspect minecraft-jv-a1b2c3 --format {{.Image}}", "verify --instance jv-a1b2c3 26.2"} {
					if !strings.Contains(calls, want) {
						t.Fatalf("recovery missing %s: %s", want, calls)
					}
				}
			}
			if failure == "publish-partial" && !strings.Contains(calls, "recover pending") {
				t.Fatal("pending publication not recovered")
			}
			code := map[string]string{"publish-fail": "20", "publish-partial": "21", "stop-fail": "18", "start-fail": "19", "verify-new-fail": "22", "backup-fail": "23"}[failure]
			if code != "" && !strings.Contains(trace, "[exit "+code+"]") {
				t.Fatalf("command failure missing from trace: %s", trace)
			}
		})
	}
}

func TestContainerHelperStopObservesContainerUntilShutdownCompletes(t *testing.T) {
	f := newContainerFixture(t)
	f.write("stop-check", `#!/bin/bash
set -Eeuo pipefail
source "$FIXTURE_ROOT/common.sh"
source "$FIXTURE_ROOT/interrupt-safety.sh"
source "$FIXTURE_ROOT/migration-instance.sh"
JV_INSTANCE_UNIT=minecraft-jv-a1b2c3.service
JV_INSTANCE_CONTAINER=minecraft-jv-a1b2c3
jv_begin_minecraft_stop
[[ $(cat "$FIXTURE_ROOT/active") == deactivating ]]
[[ $(podman inspect --format '{{.State.Running}}' "$JV_INSTANCE_CONTAINER") == true ]]
[[ $(podman inspect "$JV_INSTANCE_CONTAINER" --format '{{.State.Running}}') == true ]]
jv_minecraft_still_running
jv_wait_minecraft_stopped
[[ $(cat "$FIXTURE_ROOT/active") == inactive ]]
[[ $(podman inspect --format '{{.State.Running}}' "$JV_INSTANCE_CONTAINER") == false ]]
! jv_minecraft_still_running
`)
	f.helper = filepath.Join(f.root, "stop-check")
	if data, err := f.run(); err != nil {
		t.Fatalf("shutdown observation: %v %s", err, data)
	}
}

func TestContainerHelperUpdateRestoreUpdate(t *testing.T) {
	for _, keep := range []string{"1", "2"} {
		t.Run(keep, func(t *testing.T) {
			f := newContainerFixture(t)
			if data, err := f.run("retention", keep); err != nil {
				t.Fatalf("%v %s", err, data)
			}
			check := func(selected, running, previous string) {
				t.Helper()
				status := f.status()
				if f.text("active") != "active" || f.text("running") != running || !strings.Contains(f.text("instances/jv-a1b2c3/config.json"), selected) {
					t.Fatal("serving identity changed")
				}
				if status.KeepPreviousImages != int(keep[0]-'0') || len(status.PreviousImages) != 1 || status.PreviousImages[0].Digest != previous {
					t.Fatalf("history: %+v", status)
				}
			}
			if data, err := f.apply(f.status(), true, false, false); err != nil {
				t.Fatalf("update: %v %s", err, data)
			}
			check(f.remote, "new-id", f.old)
			plan := f.status()
			if data, err := f.run("restore", plan.RestoreFingerprint, "true", "false", "false", f.old); err != nil {
				t.Fatalf("restore: %v %s", err, data)
			}
			check(f.old, "old-id", f.remote)
			if data, err := f.apply(f.status(), true, false, false); err != nil {
				t.Fatalf("update again: %v %s", err, data)
			}
			check(f.remote, "new-id", f.old)
		})
	}
}
