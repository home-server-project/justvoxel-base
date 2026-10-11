const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const root = path.resolve(__dirname, '..');
const read = (file) => fs.readFileSync(path.join(root, file), 'utf8');
const app = read('webui/internal/server/static/app.js');
const setup = read('webui/internal/server/static/setup-operation.js');
const template = read('webui/internal/server/templates/setup_progress.html');
const consoleCode = app.slice(app.indexOf('window.JustVoxelLiveConsole = (() => {'), app.indexOf('\ndocument.addEventListener("keydown", (event) => {\n  if (event.key !== "Escape") return;'));
const A = '12345678-1234-4123-8123-123456789abc';
const B = '12345678-1234-4123-8123-123456789abd';
const flush = async () => { for (let i = 0; i < 12; i++) await Promise.resolve(); };
const deferred = () => { let resolve, reject; const promise = new Promise((a, b) => { resolve = a; reject = b; }); return { promise, resolve, reject }; };
const response = (text, status = 200, error = '') => ({
  ok: status === 200, status, redirected: false,
  headers: { get: () => 'text/plain; charset=utf-8' },
  text: async () => text, json: async () => ({ error }),
});

class Element {
  constructor(tag) {
    this.tag = tag; this.children = []; this.dataset = {}; this.attributes = {}; this.listeners = {};
    this.className = ''; this.textContent = ''; this.hidden = false; this.open = false;
    this.scrollHeight = 200; this.clientHeight = 100; this.scrollTop = 100;
    this.classList = { add() {}, toggle() {} };
  }
  append(...nodes) { this.children.push(...nodes); }
  appendChild(node) { this.append(node); return node; }
  replaceChildren(...nodes) { this.children = nodes; }
  setAttribute(key, value) { this.attributes[key] = value; }
  removeAttribute(key) { delete this.attributes[key]; }
  addEventListener(name, fn) { (this.listeners[name] ||= []).push(fn); }
  fire(name) { for (const fn of this.listeners[name] || []) fn({}); }
  all() { return [this, ...this.children.flatMap((node) => node.all())]; }
  querySelectorAll() { return []; }
}

function harness() {
  const body = new Element('body');
  const timers = new Map(), events = new Map(), reads = [], pending = [], navigations = [], workspaces = [];
  let timerID = 0;
  const window = {
    setTimeout(fn, delay) { timers.set(++timerID, { fn, delay }); return timerID; },
    clearTimeout(id) { timers.delete(id); },
    addEventListener(name, fn) { events.set(name, fn); },
    location: { href: 'https://justvoxel/setup/progress/' + A, origin: 'https://justvoxel', assign(url) { navigations.push(url); } },
  };
  const context = vm.createContext({
    window, URL, AbortController,
    document: { body, createElement: (tag) => new Element(tag) },
    fetch(url, options) { const next = deferred(); reads.push({ url, options }); pending.push(next); return next.promise; },
    setupWorkspaceWindow(element, options) {
      const workspace = {
        element, options, fronts: 0,
        open() { element.open = true; },
        close() { element.open = false; options.onClose(); },
        bringToFront() { this.fronts++; },
      };
      workspaces.push(workspace); return workspace;
    },
  });
  vm.runInContext(consoleCode, context);
  return {
    context, body, window, timers, events, reads, pending, navigations, workspaces,
    api: window.JustVoxelLiveConsole,
    open(kind = 'setup', id = A, state = 'running', extra = {}) { window.JustVoxelLiveConsole.open({ kind, operationID: id, state, ...extra }); },
    get viewer() { return body.all().find((node) => node.tag === 'pre'); },
    get status() { return body.all().find((node) => node.attributes.role === 'status'); },
    async answer(text, status = 200, error = '') { pending.shift().resolve(response(text, status, error)); await flush(); },
    async tick() { const [id, timer] = timers.entries().next().value; timers.delete(id); timer.fn(); await flush(); },
  };
}

test('one non-modal workspace, drag header, explicit opening, X and safe read-only transcript', async () => {
  const h = harness();
  assert.equal(h.workspaces.length, 0);
  h.open();
  assert.equal(h.workspaces.length, 1);
  assert.equal(h.workspaces[0].element.dataset.workspaceWindow, 'live-console');
  assert.ok(h.body.all().some((node) => 'data-workspace-drag-handle' in node.attributes));
  assert.equal(h.reads.length, 1);
  assert.equal(h.reads[0].url, `/setup/progress/${A}/log/view`);
  assert.equal(h.reads[0].options.credentials, 'same-origin');
  assert.equal(h.reads[0].options.cache, 'no-store');
  assert.equal(h.reads[0].options.headers.Accept, 'text/plain');
  await h.answer('<script>unsafe</script>\n  whitespace\n');
  assert.equal(h.viewer.textContent, '<script>unsafe</script>\n  whitespace\n');
  assert.equal(h.timers.size, 1);
  h.open();
  assert.equal(h.workspaces.length, 1);
  assert.equal(h.reads.length, 1);
  assert.equal(h.timers.size, 1);
  assert.equal(h.body.all().filter((node) => ['input', 'textarea', 'form'].includes(node.tag)).length, 0);
  h.body.all().find((node) => node.attributes['aria-label'] === 'Close Live Console').fire('click');
  assert.equal(h.workspaces[0].element.open, false);
  assert.equal(h.timers.size, 0);
  h.open();
  assert.equal(h.reads.length, 2);
  assert.equal(h.workspaces.length, 1);
});

test('only open consoles poll, refresh replaces output, and page departure stops reads', async () => {
  const h = harness(); h.open(); await h.answer('first\n');
  assert.equal([...h.timers.values()][0].delay, 2500);
  await h.tick(); await h.answer('first\nsecond\n');
  assert.equal(h.viewer.textContent, 'first\nsecond\n');
  assert.equal(h.reads.length, 2);
  h.events.get('pagehide')();
  assert.equal(h.timers.size, 0);
  assert.equal(h.workspaces[0].element.open, false);
});

test('late response after close never updates, navigates or restarts polling', async () => {
  for (const status of [200, 401, 403]) {
    const h = harness(); h.open(); const signal = h.reads[0].options.signal; h.api.close();
    assert.equal(signal.aborted, true);
    await h.answer('late transcript', status, 'Password change required.');
    assert.equal(h.viewer.textContent, ''); assert.equal(h.timers.size, 0);
    assert.deepEqual(h.navigations, []);
  }
});

test('old operation and queued close events cannot overwrite or stop a reopened console', async () => {
  const h = harness(); h.open(); h.api.close(); h.open('minecraft_reset', B);
  h.workspaces[0].options.onClose(); // delayed native close event from the prior opening
  assert.equal(h.reads.length, 2);
  assert.equal(h.reads[1].url, `/api/system/workspace/logs/reset/${encodeURIComponent(B)}`);
  h.pending[1].resolve(response('new transcript')); await flush();
  h.pending[0].resolve(response('old transcript')); await flush();
  assert.equal(h.viewer.textContent, 'new transcript'); assert.equal(h.timers.size, 1);
  assert.equal(h.workspaces.length, 1);
});

test('source switch while open ignores the old pending response', async () => {
  const h = harness(); h.open(); h.open('factory_reset', B);
  h.pending[0].resolve(response('old')); await flush();
  assert.equal(h.viewer.textContent, ''); assert.equal(h.timers.size, 0);
  h.pending[1].resolve(response('new')); await flush();
  assert.equal(h.viewer.textContent, 'new'); assert.equal(h.timers.size, 1);
});

test('transient failures preserve output, show Reconnecting and recover normally', async () => {
  const h = harness(); h.open(); await h.answer('retained');
  await h.tick(); h.pending.shift().reject(new Error('network failure')); await flush();
  assert.equal(h.viewer.textContent, 'retained'); assert.equal(h.status.textContent, 'Reconnecting…');
  h.api.updateState(A, 'needs_attention'); assert.equal(h.status.textContent, 'Reconnecting…');
  await h.tick(); await h.answer('final');
  assert.equal(h.status.textContent, 'Needs attention'); assert.equal(h.viewer.textContent, 'final');
  await h.tick(); await h.answer('raw secret API error', 503);
  assert.equal(h.viewer.textContent, 'final'); assert.equal(h.status.textContent, 'Reconnecting…');
});

test('auto-follow respects manual scroll-up and resumes near bottom', async () => {
  const h = harness(); h.open(); h.viewer.scrollTop = 100; await h.answer('one');
  assert.equal(h.viewer.scrollTop, 200);
  h.viewer.scrollTop = 0; await h.tick(); await h.answer('two');
  assert.equal(h.viewer.scrollTop, 0);
  h.viewer.scrollTop = 95; await h.tick(); await h.answer('three');
  assert.equal(h.viewer.scrollTop, 200);
});

test('state updates preserve open console and polling; terminal setup output stays inspectable', async () => {
  const h = harness(); h.open(); await h.answer('running');
  for (const [state, label] of [['succeeded', 'Complete'], ['rolled_back', 'Rolled back'], ['needs_attention', 'Needs attention']]) {
    h.api.updateState(A, state); assert.equal(h.status.textContent, label);
    assert.equal(h.workspaces[0].element.open, true); assert.equal(h.timers.size, 1);
  }
  h.api.updateState(B, 'succeeded'); assert.equal(h.status.textContent, 'Needs attention');
});

test('safe authentication flows do not render errors', async () => {
  for (const [kind, status, error, target] of [
    ['setup', 401, '', '/login'], ['setup', 403, '', '/password'],
    ['minecraft_reset', 403, 'Password change required.', '/password'],
  ]) {
    const h = harness(); h.open(kind); await h.answer('raw error', status, error);
    assert.deepEqual(h.navigations, [target]); assert.equal(h.viewer.textContent, '');
    assert.equal(h.timers.size, 0);
  }
  const h = harness(); h.open('minecraft_reset'); await h.answer('not administrator', 403, 'Administrator required.');
  assert.deepEqual(h.navigations, []); assert.equal(h.status.textContent, 'Reconnecting…');
  const redirect = harness(); redirect.open();
  redirect.pending.shift().resolve({ ...response('login page'), redirected: true, url: 'https://justvoxel/login?expired=1' });
  await flush(); assert.deepEqual(redirect.navigations, ['/login?expired=1']);
});

test('no arbitrary URL/category, command entry or browser execution capability', () => {
  const h = harness();
  for (const config of [{ kind: 'journal', operationID: A }, { kind: 'setup', operationID: '../etc/passwd' }, { kind: 'reset', operationID: A }]) h.api.open(config);
  assert.equal(h.reads.length, 0); assert.equal(h.workspaces.length, 0);
  h.open('minecraft_reset', A, 'running', { url: '/arbitrary/path' });
  assert.equal(h.reads[0].url, `/api/system/workspace/logs/reset/${A}`);
  assert.doesNotMatch(consoleCode, /innerHTML|showModal|journalctl|systemctl|child_process|\b(?:exec|execSync|spawn|spawnSync)\b|\/bin\/(?:sh|bash)\b|\bshell\s*:|stdin/);
  assert.doesNotMatch(consoleCode, /(?:createElement|make)\(\s*["'](?:input|textarea|form)["']|contentEditable|contenteditable|onkeydown|onkeypress|addEventListener\(\s*["'](?:keydown|keypress|submit)["']/);
  assert.equal(h.body.all().filter((node) => ['input', 'textarea', 'form'].includes(node.tag)).length, 0);
  assert.deepEqual(Object.keys(h.api).sort(), ['close', 'open', 'updateState']);
});

function setupHarness(initialState) {
  const h = harness(), ids = new Map();
  for (const id of ['setup-operation-status', 'setup-operation-state', 'setup-operation-stage', 'setup-reconnect-note', 'setup-operation-success', 'setup-operation-rollback', 'setup-operation-attention', 'setup-dashboard-link', 'setup-review-link', 'setup-start-over-form', 'setup-recover-form', 'setup-start-over-disabled', 'setup-diagnostic-log-link', 'setup-diagnostic-log-view']) ids.set(id, new Element('button'));
  const panel = new Element('section'); panel.dataset = { operationId: A, initialState, initialStage: 'queued', statusUrl: `/api/setup/progress/${A}` };
  let ready;
  h.context.document.querySelector = (selector) => selector === '[data-setup-operation]' ? panel : null;
  h.context.document.getElementById = (id) => ids.get(id) || null;
  h.context.document.addEventListener = (name, fn) => { if (name === 'DOMContentLoaded') ready = fn; };
  h.window.setInterval = () => 1; h.window.clearInterval = () => {};
  vm.runInContext(setup, h.context); ready();
  return { h, ids, panel };
}

test('setup console is available from queued through terminal states, with download/dashboard terminal-only', async () => {
  for (const state of ['queued', 'validating', 'running', 'verifying', 'succeeded', 'rolled_back', 'needs_attention']) {
    const { h, ids } = setupHarness(state);
    const terminal = ['succeeded', 'rolled_back', 'needs_attention'].includes(state);
    assert.equal(ids.get('setup-diagnostic-log-view').hidden, false);
    assert.equal(ids.get('setup-diagnostic-log-link').hidden, !terminal);
    assert.equal(ids.get('setup-dashboard-link').hidden, !terminal);
    assert.equal(h.workspaces.length, 0);
    ids.get('setup-diagnostic-log-view').fire('click');
    assert.equal(h.reads.at(-1).url, `/setup/progress/${A}/log/view`);
    ids.get('setup-dashboard-link').fire('click');
    assert.equal(h.workspaces[0].element.open, false);
  }
  const { h, ids } = setupHarness('running');
  ids.get('setup-diagnostic-log-view').fire('click');
  h.pending[0].resolve({ ...response(''), json: async () => ({ operation: { state: 'succeeded', stage: 'completed' } }) });
  await flush();
  assert.equal(h.workspaces[0].element.open, true);
  assert.equal(h.status.textContent, 'Complete');
});

test('shared setup progress template retains recovery and one existing execution/log backend', () => {
  const button = template.split('\n').find((line) => line.includes('id="setup-diagnostic-log-view"'));
  assert.match(button, /<button.*type="button">Live Console<\/button>/);
  assert.doesNotMatch(button, /hidden|target=|href=/);
  assert.match(template, /app\.js[\s\S]*setup-operation\.js/);
  const backend = read('webui/internal/server/setup_wizard_apply.go');
  assert.match(backend, /renderAdminDiscovery\(w, "setup_progress.html"/);
  assert.match(backend, /GET \/setup\/progress\/\{id\}\/log\/view", a.setupWizardDiagnosticDownload/);
  assert.match(template, /action="\/setup\/recover"/);
  assert.match(setup, /needsAttention && \["storage_rollback", "runtime_rollback"\]/);
  assert.match(setup, /if \(startOverForm\) startOverForm.hidden = !rolledBack/);
  assert.match(setup, /dashboardLink\?\.addEventListener\("click", \(\) => window.JustVoxelLiveConsole.close\(\)\)/);
  assert.match(setup, /if \(!finished\) window.setTimeout\(poll, 2500\)/);
  assert.doesNotMatch(consoleCode, /readWorkspaceWindowState/);
});

const resetCode = app.slice(app.indexOf('  const renderResetOperation = (operation) => {'), app.indexOf('  const resetCurrentOperation = async () => {'));
function resetHarness() {
  const h = harness(), content = new Element('main'), order = [];
  const api = h.api;
  h.window.JustVoxelLiveConsole = { ...api, close() { order.push('console-close'); api.close(); } };
  Object.assign(h.context, {
    content, state: new Element('span'), loadSequence: 0, resetPollTimer: 0, currentTab: 'reset', activeFactoryResetOperationID: '',
    clearResetPoll() { h.window.clearTimeout(h.context.resetPollTimer); },
    systemResetOperationLabel: (op) => op.state,
    systemPanel() { const panel = new Element('section'), heading = new Element('div'); panel.append(heading); return { panel, heading }; },
    workspaceWindow: { close() { order.push('workspace-close'); } },
    Event: class { constructor(type) { this.type = type; } },
    planReset() {},
  });
  h.window.dispatchEvent = (event) => order.push(event.type);
  h.window.location.replace = (url) => order.push(url);
  h.window.location.protocol = 'https:';
  vm.runInContext(app.slice(app.indexOf('  const showFactoryResetComplete = () => {'), app.indexOf('  const systemHandleAuth = (response) => {')) + resetCode + '\nthis.renderResetOperation = renderResetOperation;', h.context);
  return { h, content, order, render: h.context.renderResetOperation };
}

test('both reset progress views open authoritative logs and rerender without duplicate polling', async () => {
  for (const kind of ['minecraft_reset', 'factory_reset']) {
    const { h, content, render } = resetHarness();
    const operation = { operation_type: kind, operation_id: A, state: 'running', stage: 'working' };
    render(operation);
    content.all().find((node) => node.textContent === 'Live Console').fire('click');
    assert.equal(h.reads[0].url, `/api/system/workspace/logs/reset/${encodeURIComponent(A)}`);
    await h.answer('reset running');
    render({ ...operation, state: 'verifying' });
    content.all().find((node) => node.textContent === 'Live Console').fire('click');
    assert.equal(h.workspaces.length, 1); assert.equal(h.reads.length, 1);
    assert.equal([...h.timers.values()].filter((timer) => timer.delay === 2500).length, 1);
    render({ ...operation, state: 'needs_attention' });
    assert.equal(h.status.textContent, 'Needs attention'); assert.equal(h.workspaces[0].element.open, true);
    assert.ok(content.all().some((node) => node.textContent === 'Live Console'));
    assert.ok(content.all().some((node) => node.textContent === (kind === 'minecraft_reset' ? 'Retry Reset Minecraft' : 'Keep current server')));
  }
});

test('successful reset transitions close console before existing completion/session behavior', () => {
  for (const kind of ['minecraft_reset', 'factory_reset']) {
    const { h, content, order, render } = resetHarness();
    render({ operation_type: kind, operation_id: A, state: 'running' });
    content.all().find((node) => node.textContent === 'Live Console').fire('click');
    render({ operation_type: kind, operation_id: A, state: 'succeeded' });
    assert.equal(h.workspaces[0].element.open, false); assert.equal(h.timers.size, 0);
    assert.deepEqual(order, kind === 'minecraft_reset' ? ['console-close', 'workspace-close', 'justvoxel:minecraft-reset-complete'] : ['console-close', '/factory-reset-complete']);
    if (kind === 'factory_reset') assert.match(h.context.document.cookie, /jv_factory_reset_complete=1; Path=\/factory-reset-complete; Max-Age=120; SameSite=Strict; Secure/);
  }
});

test('console authentication loss preserves full factory-reset session invalidation transition', async () => {
  const { h, content, order, render } = resetHarness();
  render({ operation_type: 'factory_reset', operation_id: A, state: 'running' });
  assert.equal(h.context.activeFactoryResetOperationID, A);
  content.all().find((node) => node.textContent === 'Live Console').fire('click');
  await h.answer('unauthorized', 401);
  assert.deepEqual(order, ['console-close', '/factory-reset-complete']);
  assert.match(h.context.document.cookie, /jv_factory_reset_complete=1; Path=\/factory-reset-complete; Max-Age=120; SameSite=Strict; Secure/);
  assert.deepEqual(h.navigations, []);
  assert.equal(h.workspaces[0].element.open, false);
  // Reset progress has its own timer; only console polling must stop here.
  assert.equal([...h.timers.values()].filter((timer) => timer.delay === 2500).length, 0);
  assert.equal(h.timers.get(h.context.resetPollTimer).delay, 1800);
});

test('reset console authentication loss uses login outside the active factory reset', async () => {
  for (const kind of ['minecraft_reset', 'factory_reset']) {
    const { h, content, order, render } = resetHarness();
    render({ operation_type: kind, operation_id: A, state: 'running' });
    content.all().find((node) => node.textContent === 'Live Console').fire('click');
    // The console's operation no longer matches the authoritative active reset.
    h.context.activeFactoryResetOperationID = B;
    await h.answer('unauthorized', 401);
    assert.deepEqual(order, []);
    assert.deepEqual(h.navigations, ['/login']);
    assert.equal(h.context.document.cookie, undefined);
    assert.equal(h.workspaces[0].element.open, false);
    assert.equal([...h.timers.values()].filter((timer) => timer.delay === 2500).length, 0);
  }
});

test('diagnostic routes, confirmation/recovery and System Logs/Failed units remain authoritative', () => {
  const logs = read('webui/internal/server/system_workspace_logs.go');
  assert.match(logs, /systemWorkspaceIdentity\(w, r, "administrator"\)/);
  assert.match(logs, /validWorkspaceLogID\(category, id\)/);
  assert.match(logs, /AdminDiagnosticLog\(r.Context\(\), session, category, id\)/);
  assert.match(logs, /text\/plain; charset=utf-8/);
  assert.match(logs, /case "setup", "migration", "restore", "reset", "operation"/);
  assert.match(logs, /AdminFailedUnitJournal/);
  assert.match(app, /Keep current server/);
  assert.match(app, /Review and retry Factory Reset/);
  assert.match(app, /Retry Reset Minecraft/);
  assert.match(app, /confirm_players/);
  assert.match(app, /factoryResetPollOperationID === activeFactoryResetOperationID/);
  assert.equal(read('webui/VERSION').trim(), '1.0.0-rc.1');
  assert.match(read('webui/internal/server/static/app.css'), /\.live-console-dialog\{[^}]*resize:both/);
  assert.match(read('webui/internal/server/static/app.css'), /\.live-console-transcript\{[^}]*overflow:auto[^}]*white-space:pre/);
});
