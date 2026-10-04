const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');
const vm = require('node:vm');

const source = readFileSync(path.join(__dirname, '../webui/internal/server/static/version-workspace.js'), 'utf8');
// Exercise the production review and Apply handler with DOM/API boundaries replaced.
const reviewSource = source.slice(source.indexOf('  const requiresRuntimeUpdate ='), source.indexOf('  const wireForm ='));
class Element {
  constructor(tag) {
    this.tag = tag;
    this.children = [];
    this.dataset = {};
    this.listeners = {};
    this.checked = false;
  }
  append(...children) { this.children.push(...children); }
  appendChild(child) { this.append(child); }
  setAttribute(name, value) { this[name] = value; }
  addEventListener(name, listener) { this.listeners[name] = listener; }
  querySelector() { return null; }
  remove() { this.removed = true; }
  all() { return [this, ...this.children.flatMap((child) => child.all())]; }
}
const policyChange = { field: 'version_policy', before: 'recommended', after: 'pinned' };
const statusFor = (candidate, extra = {}) => ({
  installed: '26.2', selected_candidate: candidate, paper_supported: true,
  crossplay_enabled: false, crossplay_compatible: false,
  update_available: true, stack_state: 'updates_available', plan_fingerprint: 'refreshed', ...extra,
});
function review(candidate, { changes = [policyChange], online = 0, refreshed = candidate } = {}) {
  const calls = [];
  const context = vm.createContext({
    Boolean, Number, URLSearchParams, csrf: 'csrf', state: {}, updateOperation: '',
    document: { createElement: (tag) => new Element(tag) },
    card: (title) => Object.assign(new Element('section'), { textContent: title }),
    line: (label, value) => Object.assign(new Element('div'), { textContent: `${label}: ${value}` }),
    stackLabel: (value) => value || 'Unavailable',
    policyLabel: (mode) => ({ recommended: 'Recommended', pinned: 'Specific version', latest: 'Latest' })[mode],
    statusURL: () => '/api/version/workspace/status',
    request: async (url, options) => {
      calls.push({ url, body: options?.body });
      if (url.endsWith('/settings/apply')) return { applied: true };
      if (url.endsWith('/status')) return refreshed;
      if (url.endsWith('/update')) return { operation: { operation_id: 'operation-1' } };
      throw new Error(`Unexpected request: ${url}`);
    },
    render: async (message) => { calls.push({ message }); },
    watchUpdate: (id) => { calls.push({ watch: id }); },
  });
  vm.runInContext(reviewSource + '\nglobalThis.showReview = showReview;', context);
  const root = new Element('div');
  const form = { elements: { policy: {} } };
  context.showReview(root, form, { changes, restart_required: true }, new URLSearchParams({ version_policy: 'pinned' }), candidate, { online });
  const apply = root.all().find((element) => element.tag === 'button' && element.textContent === 'Apply changes');
  const consent = root.all().find((element) => element.tag === 'input');
  return { root, apply, consent, calls };
}

test('same-version mode changes save without starting an update, even with unrelated stack updates available', async () => {
  for (const change of [policyChange, { ...policyChange, before: 'pinned', after: 'recommended' }]) {
    const result = review(statusFor('26.2'), { changes: [change, { field: 'version', before: 'LATEST', after: '26.2' }], online: 2 });
    assert.ok(result.apply);
    assert.equal(result.consent, undefined);
    assert.equal(result.root.all().filter((element) => element.textContent?.startsWith('Minecraft:')).length, 0);
    await result.apply.listeners.click();
    assert.deepEqual(result.calls.filter((call) => call.url).map((call) => call.url), ['/api/minecraft/workspace/settings/apply', '/api/version/workspace/status']);
    assert.equal(result.calls.at(-1).message, 'Changes saved.');
  }
});

test('one Apply saves, refreshes, starts the existing update with its new fingerprint, and watches progress', async () => {
  const result = review(statusFor('26.3', { plan_fingerprint: 'preview' }), { refreshed: statusFor('26.3') });
  assert.equal(result.root.all().filter((element) => element.tag === 'button' && element.textContent.startsWith('Apply')).length, 1);
  await result.apply.listeners.click();
  assert.deepEqual(result.calls.filter((call) => call.url).map((call) => call.url), ['/api/minecraft/workspace/settings/apply', '/api/version/workspace/status', '/api/version/workspace/update']);
  const update = result.calls.find((call) => call.url?.endsWith('/update'));
  assert.equal(update.body.get('plan_fingerprint'), 'refreshed');
  assert.equal(update.body.get('confirm_players'), 'no');
  assert.equal(result.calls.at(-1).watch, 'operation-1');
});

test('online players require the existing switch before Apply and pass explicit confirmation', async () => {
  const result = review(statusFor('26.3'), { online: 2 });
  assert.equal(result.apply.disabled, true);
  assert.equal(result.consent.role, 'switch');
  assert.ok(result.root.all().some((element) => element.className?.includes('system-ups-shutdown-switch')));
  assert.ok(result.root.all().some((element) => element.textContent === 'Allow the update to interrupt online players.'));
  await result.apply.listeners.click();
  assert.equal(result.calls.length, 0);
  result.consent.checked = true;
  result.consent.listeners.change();
  assert.equal(result.apply.disabled, false);
  await result.apply.listeners.click();
  assert.equal(result.calls.find((call) => call.url?.endsWith('/update')).body.get('confirm_players'), 'yes');
});

test('cross-play incompatibility blocks Apply and explains how to proceed', () => {
  const result = review(statusFor('26.3', { crossplay_enabled: true }));
  assert.equal(result.apply, undefined);
  assert.ok(result.root.all().some((element) => element.textContent?.includes('Disable Cross-play first')));
});

test('changed candidate after saving stops before the updater without automatic retries', async () => {
  const result = review(statusFor('26.3'), { refreshed: statusFor('26.4') });
  await result.apply.listeners.click();
  assert.equal(result.calls.filter((call) => call.url?.endsWith('/settings/apply')).length, 1);
  assert.equal(result.calls.filter((call) => call.url?.endsWith('/update')).length, 0);
  assert.match(result.calls.at(-1).message, /Changes saved.*Review changes again/);
});

test('persistent operation polling continues while running and renders its final result', async () => {
  let nextPoll;
  let result = { operation: { state: 'running', status: 'Creating cold backup.' } };
  const messages = [];
  const context = vm.createContext({
    URLSearchParams, updateOperation: '', state: {},
    request: async (url) => {
      assert.equal(url, '/api/version/workspace/update-operation?id=operation-1');
      return result;
    },
    render: async (message) => { messages.push(message); },
    setTimeout: (callback, delay) => { assert.equal(delay, 2000); nextPoll = callback; },
  });
  const pollingSource = source.slice(source.indexOf('  const watchUpdate ='), source.indexOf('  // Let the native select'));
  vm.runInContext(pollingSource + '\nglobalThis.watchUpdate = watchUpdate;', context);
  await context.watchUpdate('operation-1');
  assert.equal(context.state.textContent, 'Creating cold backup.');
  assert.equal(context.updateOperation, 'operation-1');
  result = { operation: { state: 'succeeded', status: 'Server update completed.' } };
  await nextPoll();
  assert.equal(context.updateOperation, '');
  assert.deepEqual(messages, ['Server update completed.']);
});
