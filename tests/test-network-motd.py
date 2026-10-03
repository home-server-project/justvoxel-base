#!/usr/bin/env python3
"""Focused shared MOTD provider checks with isolated configuration and commands."""
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest

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
        self.script = self.directory / "motd"
        self.script.write_text(self.source)
        self.env = dict(os.environ, PATH=f"{self.directory}:{os.environ['PATH']}",
                        TS_JSON='{"BackendState":"NeedsLogin"}', TS_FAIL="0",
                        NB_IP="", PLAYIT_ACTIVE="0")
        commands = {
            "nmcli": "exit 0",
            "curl": "exit 1",
            # Tailscale and NetBird are always enabled/running, including fresh tests.
            "systemctl": 'if [[ $* == *playit.service* ]]; then exit "$((1-PLAYIT_ACTIVE))"; fi\nexit 0',
            "tailscale": '[[ $* == "status --json" ]] || exit 2\n[[ $TS_FAIL == 0 ]] || exit 1\nprintf "%s" "$TS_JSON"',
            "netbird": '[[ $* == "status --ipv4" ]] || exit 2\nprintf "%s" "$NB_IP"',
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
            self.assertEqual(self.rows()["Tailscale"], "Not configured")
        self.env.update(TS_JSON='{"BackendState":"Running"}', TS_FAIL="1")
        self.assertEqual(self.rows()["Tailscale"], "Not configured")

    def test_netbird_requires_config_and_valid_ipv4(self):
        self.env["NB_IP"] = "100.64.0.2"
        self.assertEqual(self.rows()["NetBird"], "Not configured")
        for path in list(self.configs)[:3]:
            self.configs[path].touch()
            for address, expected in [("100.64.0.2", "Connected"), ("100.64.0.2/16", "Connected"),
                                      ("", "Stopped"), ("999.1.1.1", "Stopped"), ("identity", "Stopped")]:
                self.env["NB_IP"] = address
                self.assertEqual(self.rows()["NetBird"], expected)
            self.configs[path].unlink()

    def test_playit_configured_states(self):
        self.env["PLAYIT_ACTIVE"] = "1"
        self.assertEqual(self.rows()["Playit"], "Not configured")
        self.configs["/etc/playit/playit.toml"].touch()
        for active, expected in [("1", "Running"), ("0", "Configured")]:
            self.env["PLAYIT_ACTIVE"] = active
            self.assertEqual(self.rows()["Playit"], expected)
            self.assertEqual(self.rows(issue=True)["Playit"], expected)

    def test_both_entrypoints_call_shared_motd(self):
        self.assertIn("/usr/libexec/justvoxel/motd --issue", (ROOT / "runtime/justvoxel-console-issue").read_text())
        self.assertIn("exec /usr/libexec/justvoxel/motd", (ROOT / "mjust/libexec/welcome").read_text())


if __name__ == "__main__":
    unittest.main()
