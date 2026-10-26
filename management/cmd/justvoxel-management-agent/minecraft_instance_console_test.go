package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestInstanceConsoleCommandsStayArgumentsAndSelected(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	id, sibling := f.configs[0].ID, f.configs[1].ID
	f.states[id], f.containers[id] = "active", "running"
	previous, oldLock := instanceRuntimes, pluginRuntimeLockPath
	instanceRuntimes, pluginRuntimeLockPath = f.backend, filepath.Join(t.TempDir(), "maintenance.lock")
	t.Cleanup(func() { instanceRuntimes, pluginRuntimeLockPath = previous, oldLock })
	s := adminServerForTest()
	s.operations = openTestOperationStore(t)
	mux := http.NewServeMux()
	registerInstanceRuntimeRoutes(mux, s)
	base := f.backend.command
	var commands []string
	fail := false
	f.backend.command = func(ctx context.Context, executable string, args ...string) ([]byte, error) {
		if executable == "podman" && len(args) > 0 && args[0] == "exec" {
			if len(args) != 4 || !reflect.DeepEqual(args[:3], []string{"exec", "minecraft-" + id, "rcon-cli"}) {
				t.Fatal("console escaped selected RCON argument path", executable, args)
			}
			commands = append(commands, args[3])
			if fail {
				return []byte("RCON_PASSWORD=" + f.configs[0].RCONPassword), errors.New("private stderr environment " + f.configs[0].RCONPassword)
			}
			return []byte("\x1b[33mPlayers:\x1b[0m\n§aAlex§r\n" + f.configs[0].RCONPassword + "\nRCON_PASSWORD=" + f.configs[0].RCONPassword), nil
		}
		return base(ctx, executable, args...)
	}
	for _, command := range []string{"list", "whitelist list", "say $(touch /tmp/never); `id` && echo $RCON_PASSWORD", " /version "} {
		payload, _ := json.Marshal(map[string]string{"command": command})
		rr := httptest.NewRecorder()
		start := len(f.calls)
		mux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/instances/"+id+"/console", string(payload)))
		if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"id":"`+id+`"`) || !strings.Contains(rr.Body.String(), `Players:\nAlex`) {
			t.Fatal(rr.Code, rr.Body.String())
		}
		want, _ := normalizeInstanceConsoleCommand(command)
		if commands[len(commands)-1] != want {
			t.Fatal("command altered", commands)
		}
		for _, call := range f.calls[start:] {
			if strings.Contains(call, sibling) {
				t.Fatal("sibling touched", call)
			}
		}
		if strings.Contains(rr.Body.String(), f.configs[0].RCONPassword) {
			t.Fatal("credential leaked")
		}
	}
	fail = true
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/instances/"+id+"/console", `{"command":"list"}`))
	if rr.Code < 400 {
		t.Fatal("failed command succeeded")
	}
	for _, secret := range []string{f.configs[0].RCONPassword, "private stderr", "RCON_PASSWORD="} {
		if strings.Contains(rr.Body.String(), secret) {
			t.Fatal("private failure leaked")
		}
	}
	f.states[id], f.containers[id] = "inactive", "exited"
	before := len(commands)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/instances/"+id+"/console", `{"command":"list"}`))
	if rr.Code < 400 || len(commands) != before {
		t.Fatal("stopped server executed command")
	}
}

func TestInstanceConsoleDiscoveryRoleAndLock(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	previous, oldLock := instanceRuntimes, pluginRuntimeLockPath
	instanceRuntimes, pluginRuntimeLockPath = f.backend, filepath.Join(t.TempDir(), "maintenance.lock")
	t.Cleanup(func() { instanceRuntimes, pluginRuntimeLockPath = previous, oldLock })
	for _, role := range []principalRole{roleViewer, roleOperator, roleAdministrator} {
		s := roleServerForTest(role)
		s.operations = openTestOperationStore(t)
		mux := http.NewServeMux()
		registerInstanceRuntimeRoutes(mux, s)
		lock, err := acquirePluginRuntimeLock()
		if err != nil {
			t.Fatal(err)
		}
		before := len(f.calls)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/instances/"+f.configs[0].ID+"/console", `{"discover":true}`))
		lock.Close()
		want := http.StatusForbidden
		if role == roleAdministrator {
			want = http.StatusConflict
		}
		if rr.Code != want || len(f.calls) != before {
			t.Fatal("discovery bypassed role/lock", role, rr.Code)
		}
	}
}

func TestInstanceHelpParsingPartialAndUnusual(t *testing.T) {
	raw := "---- Help: Index (1/3) ----\n§a/list: Players online\n/plugin:warp <place> - Warp home\nweather <clear|rain>\nUnusual prose from a plugin\n<html>unsafe</html>\n???\n"
	entries, pages := parseInstanceHelp(raw)
	if pages != 3 || len(entries) != 3 || entries[0].Name != "list" || entries[0].Description != "Players online" || entries[1].Name != "plugin:warp" || entries[1].Usage != "plugin:warp <place>" {
		t.Fatal(entries, pages)
	}
	entries, _ = parseInstanceHelp("help is unavailable\n<title>not a command</title>\n\x00\n")
	if len(entries) != 0 {
		t.Fatal("invented commands", entries)
	}
}

func TestInstanceHelpDiscoveryBounds(t *testing.T) {
	if instanceHelpMaxPages != 64 || instanceHelpMaxBytes != 65536 || instanceHelpMaxEntries != 512 {
		t.Fatal("unexpected discovery bounds")
	}
	for _, scenario := range []string{"complete", "pages", "bytes", "entries", "repeat", "failure", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			f := newInstanceActivationFixture(t)
			calls := 0
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			f.backend.command = func(probe context.Context, exe string, args ...string) ([]byte, error) {
				calls++
				if exe != "podman" || len(args) != 4 || args[1] != "minecraft-"+f.configs[0].ID || args[2] != "rcon-cli" {
					t.Fatal("wrong help path", exe, args)
				}
				want := "help"
				if calls > 1 {
					want = fmt.Sprintf("help %d", calls)
				}
				if args[3] != want {
					t.Fatal("wrong help page", args)
				}
				if deadline, ok := probe.Deadline(); !ok || time.Until(deadline) > 5*time.Second {
					t.Fatal("unbounded page execution")
				}
				if scenario == "failure" && calls == 2 {
					return nil, errors.New("private help failure")
				}
				if scenario == "cancel" {
					cancel()
				}
				line := fmt.Sprintf("Help page %d of 9999\n/cmd%d: Available", calls, calls)
				if scenario == "complete" {
					line = fmt.Sprintf("\x1b[33m--------- Help: Index (%d/12) ---------\x1b[0m\n\x1b[33m/help%d: \x1b[37mShows the help menu\x1b[0m", calls, calls)
				}
				if scenario == "repeat" {
					line = "Help page 1 of 9999\n/list: Players"
				}
				if scenario == "bytes" {
					line += "\n" + strings.Repeat("?", 8192-len(line)-1)
				}
				if scenario == "entries" {
					line = fmt.Sprintf("Help page %d of 6\n", calls)
					for entry := 0; entry < 100; entry++ {
						line += fmt.Sprintf("/cmd%d: Available\n", (calls-1)*100+entry)
					}
				}
				return []byte(line), nil
			}
			entries, partial, err := f.backend.discoverCommands(ctx, f.configs[0].ID, f.configs[0].RCONPassword)
			if err != nil || partial != (scenario != "complete") || len(entries) == 0 || calls > instanceHelpMaxPages {
				t.Fatal("unsafe discovery", entries, partial, err, calls)
			}
			switch scenario {
			case "complete":
				if calls != 12 || len(entries) != 12 || entries[11].Name != "help12" || entries[0].Description != "Shows the help menu" {
					t.Fatal(calls, entries)
				}
			case "pages":
				if calls != 64 || len(entries) != 64 {
					t.Fatal(calls)
				}
			case "bytes":
				if calls != 9 || len(entries) != 8 {
					t.Fatal(calls, len(entries))
				}
			case "entries":
				if calls != 6 || len(entries) != 512 {
					t.Fatal(calls)
				}
			case "repeat", "failure":
				if calls != 2 || len(entries) != 1 {
					t.Fatal(calls)
				}
			case "cancel":
				if calls != 1 || len(entries) != 1 {
					t.Fatal(calls)
				}
			}
		})
	}
}

func TestInstanceRCONFormattingPrecedesPrivateDiagnosticSanitization(t *testing.T) {
	secret := "private-rcon-value"
	raw := "\x1b[33m--------- Help: Index (1/2) ---------\x1b[0m\n" +
		"\x1b[33m/help: \x1b[37mShows the help menu\x1b[0m\n" +
		"§6/plugin:warp <place>§r - §fWarp home§r\n" +
		"\x1b[38;2;255;128;0m/list: Players online\x1b[0m\n" +
		"\x1b[33m" + secret[:8] + "§a" + secret[8:] + "\x1b[0m\n" +
		"pass\x1b[0mword: hidden\nto§aken: hidden\nENVIRONMENT: hidden\nKEY=value"
	text := instanceRCONText([]byte(raw), secret)
	for _, fragment := range []string{"\x1b", "[33m", "[37m", "[0m", "§", secret, "hidden", "KEY=value"} {
		if strings.Contains(text, fragment) {
			t.Fatalf("RCON text retained formatting/private fragment %q", fragment)
		}
	}
	if !strings.Contains(text, "[redacted]") || strings.Count(text, "[private diagnostic line omitted]") != 4 {
		t.Fatal("diagnostic sanitizer was bypassed", text)
	}
	entries, pages := parseInstanceHelp(text)
	if pages != 2 || len(entries) != 3 || entries[0].Name != "help" || entries[0].Description != "Shows the help menu" || entries[1].Usage != "plugin:warp <place>" {
		t.Fatal("colorized Paper/Purpur help was not parsed", entries, pages)
	}
}

func TestInstanceConsoleManualHelpAndDiscoveryNormalizeSelectedOutput(t *testing.T) {
	f := newInstanceActivationFixture(t)
	f.activateBoth()
	id := f.configs[0].ID
	f.states[id], f.containers[id] = "active", "running"
	previous, oldLock := instanceRuntimes, pluginRuntimeLockPath
	instanceRuntimes, pluginRuntimeLockPath = f.backend, filepath.Join(t.TempDir(), "maintenance.lock")
	t.Cleanup(func() { instanceRuntimes, pluginRuntimeLockPath = previous, oldLock })
	s := adminServerForTest()
	s.operations = openTestOperationStore(t)
	mux := http.NewServeMux()
	registerInstanceRuntimeRoutes(mux, s)
	base := f.backend.command
	var commands []string
	f.backend.command = func(ctx context.Context, executable string, args ...string) ([]byte, error) {
		if executable == "podman" && len(args) > 0 && args[0] == "exec" {
			if len(args) != 4 || !reflect.DeepEqual(args[:3], []string{"exec", "minecraft-" + id, "rcon-cli"}) {
				t.Fatal("help escaped selected immutable instance", args)
			}
			commands = append(commands, args[3])
			return []byte("\x1b[33m--------- Help: Index (1/1) ---------\x1b[0m\n\x1b[33m/help: \x1b[37mShows the help menu\x1b[0m\n§a/list: Players online§r\n" + f.configs[0].RCONPassword), nil
		}
		return base(ctx, executable, args...)
	}
	for _, body := range []string{`{"command":"help"}`, `{"command":"help list"}`, `{"command":"help plugin:warp"}`, `{"discover":true}`} {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/instances/"+id+"/console", body))
		var result instanceConsoleResponse
		if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &result) != nil || result.ID != id {
			t.Fatal(rr.Code, rr.Body.String())
		}
		if strings.Contains(body, "discover") {
			if result.Partial || len(result.Commands) != 2 || result.Commands[0].Name != "help" {
				t.Fatal("help discovery lost colorized entries", result)
			}
		} else if !strings.Contains(result.Output, "/help: Shows the help menu\n/list: Players online\n[redacted]") {
			t.Fatal("manual help retained broken formatting or private data", result.Output)
		}
	}
	if !reflect.DeepEqual(commands, []string{"help", "help list", "help plugin:warp", "help"}) {
		t.Fatal("manual/discovered help changed command arguments", commands)
	}
	before := len(commands)
	for _, body := range []string{`{"command":"stop"}`, `{"command":"/minecraft:stop"}`} {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/instances/"+id+"/console", body))
		if rr.Code != http.StatusBadRequest || len(commands) != before {
			t.Fatal("stop protection changed", rr.Code, commands)
		}
	}
	f.states[id], f.containers[id] = "inactive", "exited"
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, authorizedRequest("POST", "/v1/minecraft/instances/"+id+"/console", `{"discover":true}`))
	if rr.Code < 400 || len(commands) != before {
		t.Fatal("stopped server discovered commands", rr.Code, commands)
	}
}
