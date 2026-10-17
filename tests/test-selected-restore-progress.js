// Browser polling fixtures: no browser, services, or privileged runtime required.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const vm = require("node:vm");
const source = fs.readFileSync("webui/internal/server/static/restore-operation.js", "utf8");
async function fixture(progress, url = "/workspace/backups/restore/status?server=jv-family1") {
  const nodes = new Map();
  const node = (selector) => {
    if (!nodes.has(selector)) nodes.set(selector, { hidden: true, textContent: "initial" });
    return nodes.get(selector);
  };
  let calls = 0;
  const timers = [];
  const panel = {
    dataset: { selectedServerId: "jv-family1", statusUrl: url },
    isConnected: true, querySelector: node,
  };
  const root = { querySelector: () => panel };
  const window = { location: { href: "http://example/", origin: "http://example" }, setTimeout: (fn) => timers.push(fn) };
  vm.runInNewContext(source, { document: root, window, URL,
    fetch: async (target, options) => {
      calls++;
      assert.equal(target.searchParams.get("server"), "jv-family1");
      assert.equal(options.credentials, "same-origin");
      return { ok: true, status: 200, json: async () => progress };
    },
  });
  await new Promise(setImmediate);
  window.JustVoxelRestoreOperation.init(root);
  assert.ok(calls <= 1, "duplicate initialization starts duplicate polling");
  return { nodes, node: (name) => node(`[data-restore-${name}]`), timers, calls };
}
(async () => {
  const active = { id: "jv-family1", state_label: "Validating", stage_label: "Preparing Restore data", status: "Working", terminal: false };
  let f = await fixture(active);
  assert.equal(f.node("state").textContent, "Validating");
  assert.equal(f.timers.length, 1);
  for (const [flag, control] of [["complete", "success"], ["rolled_back", "rollback"], ["recovery_required", "recovery"]]) {
    f = await fixture({ ...active, [flag]: true, attention: flag === "recovery_required", terminal: true });
    assert.equal(f.node(control).hidden, false);
    assert.equal(f.timers.length, 0);
  }
  f = await fixture({ ...active, id: "jv-kids001", recovery_required: true });
  assert.equal(f.node("status").textContent, "initial", "foreign status rendered");
  assert.equal(f.node("recovery").hidden, true);
  assert.equal(f.timers.length, 1, "failed read must reconnect");
  for (const url of ["/workspace/backups/restore/status?server=jv-kids001", "/workspace/backups/restore/status", "http://foreign/workspace/backups/restore/status?server=jv-family1"]) {
    f = await fixture(active, url);
    assert.equal(f.calls, 0, "invalid identity URL polled");
  }
  console.log("PASS: selected Restore polling, identity, terminal notices, recovery, and single initialization");
})().catch((err) => { console.error(err); process.exitCode = 1; });
