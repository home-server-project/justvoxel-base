#!/usr/bin/env python3
"""Focused shared MOTD provider checks with isolated configuration and commands."""
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import importlib.machinery
import importlib.util
import socket
import threading

ROOT = Path(__file__).resolve().parents[1]


class ProviderMOTD(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.source = (ROOT / "runtime/justvoxel-motd").read_text()
        self.configs = {}
        for path in ["/var/lib/netbird/default.json", "/etc/netbird/config.json",
                     "/etc/netbird/config.yaml", "/etc/playit/playit.toml"]:
            replacement = self.directory / path.lstrip("/")
            replacement.parent.mkdir(parents=True, exist_ok=True)
            self.configs[path] = replacement
            self.source = self.source.replace(path, str(replacement))
        helper = (ROOT / "runtime/justvoxel-remote-access-status").read_text()
        for path, replacement in self.configs.items():
            helper = helper.replace(path, str(replacement))
        helper_path = self.directory / "remote-status"
        helper = helper.replace('PLAYIT_SOCKET = "/run/playit/playitd.sock"', f'PLAYIT_SOCKET = {str(self.directory / "missing.sock")!r}')
        helper_path.write_text(helper)
        helper_path.chmod(0o755)
        self.source = self.source.replace("/usr/libexec/justvoxel/remote-access-status", str(helper_path))
        self.script = self.directory / "motd"
        self.script.write_text(self.source)
        self.env = dict(os.environ, PATH=f"{self.directory}:{os.environ['PATH']}",
                        TS_JSON='{"BackendState":"NeedsLogin"}', TS_FAIL="0",
                        NB_JSON='{}', PLAYIT_ACTIVE="0")
        commands = {
            "nmcli": "exit 0",
            "curl": "exit 1",
            # Tailscale and NetBird are always enabled/running, including fresh tests.
            "systemctl": 'if [[ $1 == show ]]; then if [[ $* == *playit.service* && $PLAYIT_ACTIVE == 0 ]]; then echo inactive; else echo active; fi; fi\nexit 0',
            "tailscale": '[[ $* == "status --json" ]] || exit 2\n[[ $TS_FAIL == 0 ]] || exit 1\nprintf "%s" "$TS_JSON"',
            "netbird": '[[ $* == "status --json" ]] || exit 2\nprintf "%s" "$NB_JSON"',
        }
        for name, body in commands.items():
            path = self.directory / name
            path.write_text("#!/usr/bin/bash\n" + body + "\n")
            path.chmod(0o755)

    def rows(self, issue=False):
        result = subprocess.run(["bash", str(self.script)] + (["--issue"] if issue else []),
                                env=self.env, capture_output=True, text=True, check=True)
        text = re.sub(r"\\e\[[0-9;]*m", "", result.stdout)
        return dict(re.findall(r"^\s*(Tailscale|NetBird|Playit):\s+([^\n]+)", text, re.M))

    def test_fresh_services_are_not_configuration(self):
        expected = {name: "Not configured" for name in ["Tailscale", "NetBird", "Playit"]}
        self.assertEqual(self.rows(), expected)
        self.assertEqual(self.rows(issue=True), expected)
        self.assertNotIn("tailscaled.state", self.source)

    def test_tailscale_states(self):
        cases = [("NoState", {}, "Not configured"), ("NeedsLogin", {"HaveNodeKey": True}, "Not configured"),
                 ("NeedsMachineAuth", {}, "Awaiting approval"), ("Running", {}, "Connected"),
                 ("Starting", {}, "Not configured"), ("Stopped", {}, "Not configured"),
                 ("Starting", {"HaveNodeKey": True}, "Starting"),
                 ("Stopped", {"CurrentTailnet": {}}, "Stopped")]
        for state, evidence, expected in cases:
            with self.subTest(state=state, evidence=evidence):
                self.env["TS_JSON"] = json.dumps(dict(BackendState=state, **evidence))
                self.assertEqual(self.rows()["Tailscale"], expected)
                self.assertEqual(self.rows(issue=True)["Tailscale"], expected)
        for invalid in ["invalid", "null", '[]', '{"BackendState":"Running","HaveNodeKey":"true"}']:
            self.env["TS_JSON"] = invalid
            self.assertEqual(self.rows()["Tailscale"], "Unavailable")
        self.env.update(TS_JSON='{"BackendState":"Running"}', TS_FAIL="1")
        self.assertEqual(self.rows()["Tailscale"], "Unavailable")

    def test_vpn_addresses_and_stale_addresses(self):
        self.env["TS_JSON"] = json.dumps({"BackendState": "Running", "TailscaleIPs": ["fd00::1", "100.64.0.3/16"]})
        self.env["NB_JSON"] = json.dumps({"daemonStatus": "Connected", "management": {"connected": True}, "netbirdIp": "100.64.0.2/16"})
        for issue in (False, True):
            rows = self.rows(issue)
            self.assertEqual(rows["Tailscale"], "100.64.0.3")
            self.assertEqual(rows["NetBird"], "100.64.0.2")
        self.env["TS_JSON"] = json.dumps({"BackendState": "Stopped", "HaveNodeKey": True, "TailscaleIPs": ["100.64.0.3"]})
        self.env["NB_JSON"] = json.dumps({"daemonStatus": "Disconnected", "management": {"connected": False}, "netbirdIp": "100.64.0.2/16"})
        self.configs["/etc/netbird/config.json"].write_text("fixture")
        self.assertEqual(self.rows()["Tailscale"], "Stopped")
        self.assertEqual(self.rows()["NetBird"], "Not connected")

    def test_playit_configured_states(self):
        self.env["PLAYIT_ACTIVE"] = "1"
        self.assertEqual(self.rows()["Playit"], "Not configured")
        self.configs["/etc/playit/playit.toml"].write_text("fixture")
        for active, expected in [("1", "Running"), ("0", "Stopped")]:
            self.env["PLAYIT_ACTIVE"] = active
            self.assertEqual(self.rows()["Playit"], expected)
            self.assertEqual(self.rows(issue=True)["Playit"], expected)

    def test_both_entrypoints_call_shared_motd(self):
        self.assertIn("/usr/libexec/justvoxel/motd --issue", (ROOT / "runtime/justvoxel-console-issue").read_text())
        self.assertIn("exec /usr/libexec/justvoxel/motd", (ROOT / "mjust/libexec/welcome").read_text())


class LocalRemoteStatus(unittest.TestCase):
    def setUp(self):
        loader = importlib.machinery.SourceFileLoader("remote_status", str(ROOT / "runtime/justvoxel-remote-access-status"))
        spec = importlib.util.spec_from_loader(loader.name, loader)
        self.helper = importlib.util.module_from_spec(spec)
        loader.exec_module(self.helper)

    def lifecycle(self, tunnels=None, state="running", version=2, request_id=1):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        path = str(Path(directory.name) / "playit.sock")
        listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        listener.bind(path)
        listener.listen(1)
        self.addCleanup(listener.close)
        requests = []
        lifecycle = {"state": state, "data": {"tunnels": tunnels, "login_link": "SECRET", "active_tcp": 0}}
        def serve():
            with listener.accept()[0] as client:
                client.sendall(json.dumps({"message_kind": "hello", "data": {"protocol": {"ipc_version": version, "capabilities": ["lifecycle_state"]}}}).encode() + b"\n")
                if version != 2:
                    return
                with client.makefile("rb") as reader:
                    requests.append(json.loads(reader.readline()))
                client.sendall(json.dumps({"message_kind": "response", "data": {"ipc_version": 2, "request_id": request_id, "response": {"type": "state", "data": lifecycle}}}).encode() + b"\n")
        thread = threading.Thread(target=serve, daemon=True)
        thread.start()
        try:
            return self.helper.playit_lifecycle(path)
        finally:
            thread.join(1)
            self.assertFalse(thread.is_alive())
            if version == 2:
                self.assertEqual(requests, [{"ipc_version": 2, "request_id": 1, "request": {"type": "get_state"}}])

    def test_one_multiple_disabled_and_empty_tunnels(self):
        tunnel = lambda name, disabled=False: {"display_address": name, "destination": "127.0.0.1:25565", "is_disabled": disabled}
        one, two, disabled = tunnel("one.playit.gg:1234"), tunnel("two.playit.gg:2345"), tunnel("disabled.playit.gg", True)
        for tunnels, welcome, compact in [([], "No tunnels", "No tunnels"), ([disabled], "No tunnels", "No tunnels"), ([one, disabled], one["display_address"], "1 tunnel"), ([one, two, disabled], "2 tunnels", "2 tunnels")]:
            with self.subTest(tunnels=tunnels):
                result = self.lifecycle(tunnels)
                self.assertTrue(result["connected"])
                self.assertEqual(result["tunnels"], tunnels)
                self.assertEqual(self.helper.display(result), welcome)
                self.assertEqual(self.helper.display(result, compact=True), compact)
                self.assertNotIn("SECRET", json.dumps(result))

    def test_transitions_and_unavailable_or_incompatible_ipc(self):
        self.assertEqual(self.lifecycle(state="starting")["summary"], "Connecting")
        self.assertEqual(self.lifecycle(state="waiting_for_secret")["summary"], "Not configured")
        with self.assertRaises(ValueError):
            self.lifecycle([], version=99)
        with self.assertRaises(ValueError):
            self.lifecycle([], request_id=99)
        with self.assertRaises(OSError):
            self.helper.playit_lifecycle("/nonexistent/justvoxel-playit.sock")
        with patch.object(self.helper, "command", return_value="active"), patch.object(self.helper, "configured", return_value=True), patch.object(self.helper, "playit_lifecycle", side_effect=PermissionError):
            result = self.helper.provider_status("playit")
            self.assertEqual(result["summary"], "Running")
            self.assertFalse(result["connected"])
            self.assertNotIn("tunnels", result)

    def test_normal_user_ssh_uses_live_status_without_credentials_or_sudo(self):
        for provider, data, expected in [
            ("tailscale", {"BackendState": "Running", "TailscaleIPs": ["100.64.0.3/16"]}, "100.64.0.3"),
            ("netbird", {"daemonStatus": "Connected", "management": {"connected": True}, "netbirdIp": "100.64.0.4/16"}, "100.64.0.4")]:
            calls = []
            def command(*args):
                calls.append(args)
                return "active" if args[0] == "systemctl" else json.dumps(data)
            with patch.object(self.helper.os, "stat", side_effect=PermissionError), patch.object(self.helper, "command", side_effect=command):
                self.assertEqual(self.helper.display(self.helper.provider_status(provider)), expected)
            self.assertTrue(all(args[0] != "sudo" for args in calls))
            self.assertIn((provider, "status", "--json"), calls)

    def test_stopped_service_never_proves_connection_with_stale_ip(self):
        def command(*args):
            return "inactive" if args[0] == "systemctl" else '{"BackendState":"Running","TailscaleIPs":["100.64.0.3"]}'
        with patch.object(self.helper, "command", side_effect=command):
            result = self.helper.provider_status("tailscale")
            self.assertEqual(result["summary"], "Stopped")
            self.assertEqual(result["ip"], "")
            self.assertFalse(result["connected"])


if __name__ == "__main__":
    unittest.main()
