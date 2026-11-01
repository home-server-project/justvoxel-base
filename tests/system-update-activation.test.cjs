const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { test } = require('node:test');
const vm = require('node:vm');
const source = readFileSync(`${__dirname}/../webui/internal/server/static/app.js`, 'utf8');
const update = source.slice(source.indexOf('const initSystemUpdateWorkspace ='), source.indexOf('const workspaceWindows ='));
class Element {
  constructor() { this.events = {}; this.hidden = false; this.checked = false; this.disabled = false; this.value = 'csrf'; this.classList = { add() {}, remove() {}, contains() { return false; } }; }
  addEventListener(name, handler) { this.events[name] = handler; }
  setAttribute() {}
  removeAttribute() {}
}
function workspace(initial, recoveryError = false) {
  const nodes = new Map();
  const node = selector => { if (!nodes.has(selector)) nodes.set(selector, new Element()); return nodes.get(selector); };
  const dialog = node('[data-system-update-dialog]');
  dialog.querySelector = node;
  dialog.open = true;
  const requests = [];
  let options, state = initial;
  const context = vm.createContext({
    URLSearchParams, console, dashboard: { dataset: { configured: 'false' } }, controlCenter: null,
    MutationObserver: class { observe() {} },
    document: { querySelector: node, body: { classList: { contains() { return false; } } } },
    window: { setTimeout() { return 1; }, clearTimeout() {}, setInterval() { return 1; }, clearInterval() {} },
    readWorkspaceWindowState: () => ({ open: false }),
    setupWorkspaceWindow: (_dialog, value) => { options = value; return { open() {}, close() {} }; },
    fetch: async (url, request = {}) => {
      requests.push({ url, request });
      if (url.endsWith('/reboot')) return { ok: true, json: async () => ({ ok: true, state: 'idle' }) };
      if (url.endsWith('/force')) {
        if (recoveryError) { state = { ...state, activation_recoverable: false, recovery_status: 'unavailable' }; return { ok: false, json: async () => ({ error: 'Update recovery could not be verified. See Update logs.' }) }; }
        state = { ...state, activation_recoverable: false, recovery_status: 'finalized', recovery_message: 'Update recovery is verified. Press Reboot into update to activate it.' };
      }
      return { ok: true, json: async () => state };
    },
  });
  // Exercise the real event wiring and render code with browser stand-ins.
  vm.runInContext(update + '\ninitSystemUpdateWorkspace();', context);
  return { node, requests, async open() { options.onOpen(); for (let i = 0; i < 30; i++) await Promise.resolve(); } };
}
const staged = { ok: true, running: { digest: 'sha256:' + 'a'.repeat(64) }, staged: { digest: 'sha256:' + 'b'.repeat(64) }, reboot_required: true, checked: true, check_state: 'staged_current' };
test('normal stage and successful activation never expose Force update', async () => {
  for (const state of [staged, { ...staged, staged: null, reboot_required: false, activation_pending: false, activation_failed: false }]) {
    const page = workspace(state); await page.open();
    assert.equal(page.node('[data-system-update-force]').hidden, true);
    assert.equal(page.node('[data-system-update-quick]').checked, false);
  }
});
test('only explicit backend recoverability exposes Force update, in the approved action order', async () => {
  for (const recoverable of [false, true]) {
    const page = workspace({ ...staged, activation_failed: true, activation_recoverable: recoverable, recovery_message: 'The staged image did not activate on the previous reboot.' });
    await page.open();
    assert.equal(page.node('[data-system-update-force]').hidden, !recoverable);
    assert.match(page.node('[data-system-update-state]').textContent, /previous reboot/);
    assert.equal(page.node('[data-system-update-button]').disabled, false);
    assert.equal(page.node('[data-system-update-reboot-button]').textContent, 'Reboot into update');
  }
  const header = readFileSync(`${__dirname}/../webui/internal/server/templates/header.html`, 'utf8');
  const actions = header.slice(header.indexOf('<div class="system-update-actions">'));
  assert.ok(actions.indexOf('data-system-update-force') < actions.indexOf('data-system-update-reboot-button'));
  assert.ok(actions.indexOf('data-system-update-reboot-button') < actions.indexOf('data-system-update-button'));
  assert.match(actions, /data-system-update-force hidden/);
});
test('Force update enables Quick reboot with a 10-second warning and never submits reboot', async () => {
  const page = workspace({ ...staged, activation_failed: true, activation_recoverable: true }); await page.open();
  await page.node('[data-system-update-force]').events.click();
  assert.equal(page.node('[data-system-update-quick]').checked, true);
  assert.equal(page.node('[data-system-update-warning]').textContent, 'Player warning: 10 seconds');
  assert.equal(page.node('[data-system-update-force]').hidden, true);
  assert.match(page.node('[data-system-update-state]').textContent, /recovery is verified/);
  const posts = page.requests.filter(item => item.request.method === 'POST');
  assert.equal(posts.filter(item => item.url.endsWith('/force')).length, 1);
  assert.equal(posts.filter(item => item.url.endsWith('/reboot')).length, 0);
  assert.equal(posts.find(item => item.url.endsWith('/force')).request.body, 'csrf=csrf');
});
test('Force update failure refreshes backend authority, shows bounded error, and retains explicit reboot', async () => {
  const page = workspace({ ...staged, activation_failed: true, activation_recoverable: true }, true); await page.open();
  await page.node('[data-system-update-force]').events.click();
  assert.equal(page.node('[data-system-update-force]').hidden, true);
  assert.equal(page.node('[data-system-update-quick]').checked, true);
  assert.match(page.node('[data-system-update-error]').textContent, /See Update logs/);
  assert.equal(page.requests.filter(item => item.url.endsWith('/reboot') && item.request.method === 'POST').length, 0);
});
