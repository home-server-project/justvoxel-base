// Focused dependency-free presentation and uncertain-submission regressions.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const root = path.join(__dirname, "..");
const appSource = fs.readFileSync(path.join(root, "webui/internal/server/static/app.js"), "utf8");
const helper = appSource.slice(appSource.indexOf("function formatDiagnosticLogTimestamp("), appSource.indexOf("if (topbarClock) {"));
const entry = { timestamp: "2026-10-17T22:19:07Z" };
for (const time of ["12", "24"]) {
  for (const date of ["mdy", "dmy"]) {
    const context = vm.createContext({ topbarClock: { dataset: { systemTimezone: "America/Toronto" } }, clockPreferences: { time, date }, entry });
    vm.runInContext(helper, context);
    const formatted = vm.runInContext("formatDiagnosticLogTimestamp(entry.timestamp)", context);
    assert(formatted.startsWith(date === "mdy" ? "Oct 17 2026" : "17 Oct 2026"), formatted);
    assert.match(formatted, time === "12" ? /06:19\s*PM/ : /18:19/);
    assert.equal(entry.timestamp, "2026-10-17T22:19:07Z");
    assert.equal(vm.runInContext('formatDiagnosticLogTimestamp("invalid")', context), "invalid");
    assert.equal(vm.runInContext('formatDiagnosticLogTimestamp("")', context), "");
    assert.equal(vm.runInContext('formatDiagnosticLogTimestamp("2026-10-17T22:19:07.123456789Z")', context), formatted);
    context.topbarClock.dataset.systemTimezone = "invalid/timezone";
    assert.equal(vm.runInContext("formatDiagnosticLogTimestamp(entry.timestamp)", context), entry.timestamp);
  }
}
assert(appSource.includes('formatDiagnosticLogTimestamp(entry.timestamp)].filter(Boolean)'));
assert(!appSource.includes('entry.timestamp ='));

const setupSource = fs.readFileSync(path.join(root, "webui/internal/server/static/setup-operation.js"), "utf8");
async function uncertainSubmission() {
  const events = {};
  const form = { dataset: { eulaAccepted: "true" }, action: "/setup/review/apply", addEventListener: (name, fn) => { events[name] = fn; }, append: (...children) => { form.children = children; } };
  const configure = { addEventListener() {}, disabled: false };
  const document = {
    addEventListener: (_, fn) => fn(),
    querySelector: selector => selector === "[data-setup-apply]" ? form : selector === "[data-setup-configure]" ? configure : null,
    createElement: () => ({ setAttribute() {} }),
  };
  let submissions = 0;
  const fetch = async () => { submissions++; throw new TypeError("Failed to fetch"); };
  vm.runInNewContext(setupSource, { document, window: {}, URLSearchParams, FormData: class { forEach(fn) { fn("csrf-token", "csrf"); } }, fetch, resumeReviewedIdentitySubmission: submit => submit() });
  await events.submit({ preventDefault() {} });
  assert.equal(submissions, 1);
  assert.match(form.children[0].textContent, /Setup status could not be confirmed/);
  assert(!form.children[0].textContent.includes("Could not start"));
  assert.equal(form.children[1].textContent, "Reload Review");
  assert.equal(form.children[1].href, "/setup/review");
}
uncertainSubmission().catch(error => { console.error(error); process.exitCode = 1; });
