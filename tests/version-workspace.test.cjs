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
    this.tagName = tag;
    this.children = [];
    this.dataset = {};
    this.listeners = {};
    this.checked = false;
    this.disabled = false;
  }
  append(...children) { children.forEach((child) => { child.parent = this; this.children.push(child); }); }
  appendChild(child) { this.append(child); }
  setAttribute(name, value) { this[name] = value; }
  addEventListener(name, listener) { this.listeners[name] = listener; }
  querySelector() { return null; }
  remove() { this.removed = true; if (this.parent) this.parent.children = this.parent.children.filter((child) => child !== this); }
  all() { return [this, ...this.children.flatMap((child) => child.all())]; }
}
const policyChange = { field: 'version_policy', before: 'recommended', after: 'pinned' };
const statusFor = (candidate, extra = {}) => ({
  installed: '26.2', selected_candidate: candidate, paper_supported: true,
  crossplay_enabled: false, crossplay_compatible: false,
  update_available: true, stack_state: 'updates_available', plan_fingerprint: 'refreshed', ...extra,
});
function review(candidate, { changes = [policyChange], online = 0, refreshed = candidate, container = false, planExtra = {} } = {}) {
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
  const form = { elements: container ? { channel: {} } : { policy: {} } };
  context.showReview(root, form, { changes, restart_required: true, ...planExtra }, new URLSearchParams({ version_policy: 'pinned' }), candidate, { online });
  const apply = root.all().find((element) => element.tag === 'button' && ['Apply changes', 'Confirm and apply'].includes(element.textContent));
  const consent = root.all().find((element) => element.tag === 'input');
  return { root, apply, consent, calls, state: context.state };
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

// Exercise the production software cards, selection and review together.
const softwareSource = source.slice(source.indexOf('        const software = card("");'), source.indexOf('        const panel = card("Container updates");'));
function softwareWorkspace({ current = 'paper', overrides = {}, planning } = {}) {
  const calls = [];
  const root = new Element('div');
  const context = vm.createContext({
    Boolean, String, URLSearchParams, csrf: 'csrf', state: {}, root, ticket: 1, sequence: 1,
    minecraft: { server_type: current },
    status: { server_software: current === 'purpur' ? 'Purpur' : 'Paper' },
    document: { createElement: (tag) => new Element(tag) },
    card: (title) => Object.assign(new Element('section'), { textContent: title }),
    line: (label, value) => Object.assign(new Element('div'), { textContent: `${label}: ${value}` }),
    statusURL: (policy, version, target) => `/api/version/workspace/status?server_type=${target}`,
    request: async (url, options) => {
      calls.push({ url, body: options?.body });
      if (url.startsWith('/api/version/workspace/status?')) {
        const target = new URLSearchParams(url.split('?')[1]).get('server_type');
        const plan = { ...statusFor('26.2'), server_supported: true, server_software: target === 'purpur' ? 'Purpur' : 'Paper', ...overrides };
        return planning ? planning(plan) : plan;
      }
      if (url === '/api/dashboard-status') return { players: { online: 0 } };
      if (url === '/api/version/workspace/update') return { operation: { operation_id: 'software-1' } };
      throw new Error(`Unexpected request: ${url}`);
    },
    render: async (message) => { calls.push({ message }); },
    watchUpdate: (id) => { calls.push({ watch: id }); },
  });
  vm.runInContext(softwareSource, context);
  const cards = root.all().filter((node) => node.className?.split(' ').includes('version-software-choice'));
  const choice = (target) => cards.find((node) => node.children[1].children[0].textContent.toLowerCase() === target);
  const find = (text) => root.all().find((node) => node.textContent === text);
  return {
    root, calls, cards, choice, find,
    click: (target) => choice(target).listeners.click(),
    warning: root.all().find((node) => node.textContent === 'Requires Reset Minecraft and setup again.'),
    reviews: () => root.all().filter((node) => node.textContent === 'Review server software change'),
    consent: () => root.all().find((node) => node.tag === 'input'),
    state: context.state,
  };
}
async function softwareReview(overrides = {}) {
  const workspace = softwareWorkspace({ overrides });
  await workspace.click('purpur');
  return {
    ...workspace, change: workspace.choice('purpur'),
    apply: workspace.find('Apply server software change'),
    cancel: workspace.find('Cancel review'),
    consent: workspace.consent(),
  };
}

test('software review has compact values, an OFF switch, and ordered secondary Cancel and primary Apply', async () => {
  const result = await softwareReview();
  const nodes = result.root.all();
  for (const value of ['Review server software change', 'Server software: Paper → Purpur', 'Minecraft version: 26.2', 'Players online: 0', 'Confirm server software change']) {
    assert.ok(nodes.some((node) => node.textContent === value), value);
  }
  const summary = nodes.find((node) => node.className === 'version-software-review-summary');
  assert.equal(summary.children.length, 3);
  assert.equal(result.consent.role, 'switch');
  assert.equal(result.consent.checked, false);
  assert.ok(nodes.some((node) => node.className?.includes('system-ups-shutdown-switch')));
  assert.equal(result.apply.disabled, true);
  assert.equal(result.cancel.className, 'secondary');
  assert.ok(!result.cancel.disabled);
  assert.ok(!result.apply.className?.includes('secondary'));
  const actions = nodes.find((node) => node.className === 'action-row version-software-review-actions');
  assert.deepEqual(actions.children, [result.cancel, result.apply]);
  assert.ok(!source.includes('JustVoxel will create a cold backup, safely stop Minecraft, change software and verify the stack.'));
  const before = result.calls.length;
  await result.apply.listeners.click();
  assert.equal(result.calls.length, before);
  result.consent.checked = true;
  result.consent.listeners.change();
  assert.equal(result.apply.disabled, false);
  const applying = result.apply.listeners.click();
  assert.equal(result.cancel.disabled, true);
  assert.equal(result.consent.disabled, true);
  await applying;
  const update = result.calls.find((call) => call.url === '/api/version/workspace/update');
  assert.equal(update.body.get('csrf'), 'csrf');
  assert.equal(update.body.get('server_type'), 'purpur');
  assert.equal(update.body.get('plan_fingerprint'), 'refreshed');
  assert.equal(update.body.get('confirm_players'), 'yes');
  assert.equal(result.calls.at(-1).watch, 'software-1');
});

test('software confirmation cannot enable an unsafe plan', async () => {
  for (const unsafe of [
    { stack_state: 'up_to_date' }, { server_supported: false },
    { crossplay_enabled: true, crossplay_compatible: false }, { plan_fingerprint: '' },
  ]) {
    const result = await softwareReview(unsafe);
    result.consent.checked = true;
    result.consent.listeners.change();
    assert.equal(result.apply.disabled, true);
    await result.apply.listeners.click();
    assert.ok(!result.calls.some((call) => call.url === '/api/version/workspace/update'));
    assert.ok(!result.cancel.disabled);
  }
  const compatible = await softwareReview({ crossplay_enabled: true, crossplay_compatible: true });
  compatible.consent.checked = true;
  compatible.consent.listeners.change();
  assert.equal(compatible.apply.disabled, false);
});

test('software Cancel removes the review and restores installed selection without applying', async () => {
  const result = await softwareReview();
  await result.cancel.listeners.click();
  assert.equal(result.reviews().length, 0);
  assert.equal(result.change.disabled, false);
  assert.ok(result.choice('paper').className.includes('is-selected'));
  assert.equal(result.change['aria-pressed'], 'false');
  assert.ok(!result.calls.some((call) => call.url === '/api/version/workspace/update'));
});

function assertSoftwareSelection(page, selected, installed) {
  for (const node of page.cards) {
    const type = node.children[1].children[0].textContent.toLowerCase();
    assert.equal(node.className.includes('is-selected'), type === selected);
    assert.equal(node.className.includes('is-current'), type === installed);
    if (node.tag === 'button') assert.equal(node['aria-pressed'], String(type === selected));
    assert.equal(node.children[1].children[1].textContent,
      type === installed ? 'Current' : installed !== 'vanilla' && type !== 'vanilla' ? 'Switch Available' : 'Switch Unavailable');
  }
}

test('installed Paper and Purpur stay clickable and reversibly clear and recreate one review', async () => {
  for (const installed of ['paper', 'purpur']) {
    const target = installed === 'paper' ? 'purpur' : 'paper';
    const page = softwareWorkspace({ current: installed });
    assertSoftwareSelection(page, installed, installed);
    assert.equal(page.choice(installed).tag, 'button');
    assert.equal(page.reviews().length, 0);
    await page.click(target);
    assertSoftwareSelection(page, target, installed);
    assert.equal(page.reviews().length, 1);
    const calls = page.calls.length;
    await page.click(target);
    assert.equal(page.calls.length, calls);
    assert.equal(page.reviews().length, 1);
    await page.click(installed);
    assert.equal(page.calls.length, calls);
    assertSoftwareSelection(page, installed, installed);
    assert.equal(page.reviews().length, 0);
    assert.equal(page.consent(), undefined);
    await page.click(target);
    assertSoftwareSelection(page, target, installed);
    assert.equal(page.reviews().length, 1);
    const from = installed === 'paper' ? 'Paper' : 'Purpur';
    const to = target === 'paper' ? 'Paper' : 'Purpur';
    assert.ok(page.find(`Server software: ${from} → ${to}`));
    page.find('Cancel review').listeners.click();
    assertSoftwareSelection(page, installed, installed);
    assert.equal(page.reviews().length, 0);
    assert.ok(!page.calls.some((call) => call.url === '/api/version/workspace/update'));
  }
});

test('Vanilla replaces software review with only its warning and supports leaving either way', async () => {
  for (const installed of ['paper', 'purpur']) {
    const target = installed === 'paper' ? 'purpur' : 'paper';
    const page = softwareWorkspace({ current: installed });
    await page.click(target);
    const beforeVanilla = page.calls.length;
    await page.click('vanilla');
    assert.equal(page.calls.length, beforeVanilla);
    assertSoftwareSelection(page, 'vanilla', installed);
    assert.equal(page.reviews().length, 0);
    assert.equal(page.warning.hidden, false);
    assert.equal(page.find('Apply server software change'), undefined);
    assert.equal(page.find('Confirm server software change'), undefined);
    assert.equal(page.consent(), undefined);
    await page.click(installed);
    assertSoftwareSelection(page, installed, installed);
    assert.equal(page.warning.hidden, true);
    assert.equal(page.reviews().length, 0);
    assert.equal(page.calls.length, beforeVanilla);
    await page.click('vanilla');
    await page.click(target);
    assertSoftwareSelection(page, target, installed);
    assert.equal(page.warning.hidden, true);
    assert.equal(page.reviews().length, 1);
    assert.equal(page.calls.filter((call) => call.url.includes(`server_type=${target}`)).length, 2);
    assert.ok(!page.calls.some((call) => call.url.includes('server_type=vanilla') || call.url === '/api/version/workspace/update'));
  }
});

test('Vanilla installed preserves unavailable Paper and Purpur capability', () => {
  const page = softwareWorkspace({ current: 'vanilla' });
  assertSoftwareSelection(page, 'vanilla', 'vanilla');
  for (const target of ['paper', 'purpur']) {
    assert.equal(page.choice(target).tag, 'div');
    assert.equal(page.choice(target)['aria-disabled'], 'true');
    assert.equal(page.choice(target).listeners.click, undefined);
  }
});

test('late Purpur planning cannot recreate a review after Vanilla or installed Paper selection', async () => {
  for (const next of ['vanilla', 'paper']) {
    let resolvePlan;
    const page = softwareWorkspace({ planning: (plan) => new Promise((resolve) => { resolvePlan = () => resolve(plan); }) });
    const pending = page.click('purpur');
    await page.click(next);
    resolvePlan();
    await pending;
    assertSoftwareSelection(page, next, 'paper');
    assert.equal(page.warning.hidden, next !== 'vanilla');
    assert.equal(page.reviews().length, 0);
    assert.equal(page.consent(), undefined);
    assert.equal(page.find('Apply server software change'), undefined);
    assert.ok(!page.calls.some((call) => call.url === '/api/version/workspace/update'));
  }
});

test('older planning cannot replace a fresh review of the same target or undo selection on failure', async () => {
  const pendingPlans = [];
  const page = softwareWorkspace({ planning: (plan) => new Promise((resolve, reject) => pendingPlans.push({ resolve: () => resolve(plan), reject })) });
  const old = page.click('purpur');
  await page.click('vanilla');
  const fresh = page.click('purpur');
  pendingPlans[1].resolve();
  await fresh;
  const review = page.reviews()[0];
  pendingPlans[0].resolve();
  await old;
  assert.deepEqual(page.reviews(), [review]);
  await page.click('paper');
  const failed = page.click('purpur');
  await page.click('vanilla');
  pendingPlans[2].reject(new Error('Old planning failed'));
  await failed;
  assertSoftwareSelection(page, 'vanilla', 'paper');
  assert.equal(page.reviews().length, 0);
  assert.equal(page.state.textContent, '');
});

const channelChange = { field: 'image_tag', label: 'Container channel', before: 'stable', after: 'latest' };
const imageStatus = (image_state, extra = {}) => statusFor('26.2', { image_state, ...extra });

test('current image and plugin-only updates do not create a container review Apply', () => {
  for (const status of [imageStatus('up_to_date', { stack_state: 'up_to_date', update_available: false }), imageStatus('up_to_date', { geyser_state: 'updates_available' }), imageStatus('unavailable')]) {
    const result = review(status, { container: true, changes: [] });
    assert.equal(result.apply, undefined);
    assert.ok(!result.root.all().some(node => node.textContent === 'Container image: Update available'));
  }
});

test('same-channel rebuilt image reviews and uses the existing update operation without a settings save', async () => {
  const result = review(imageStatus('updates_available'), { container: true, changes: [] });
  assert.ok(result.apply);
  assert.ok(!result.root.all().some(node => node.textContent === 'No changes were found.'));
  assert.ok(result.root.all().some(node => node.textContent === 'Container image: Update available'));
  await result.apply.listeners.click();
  assert.deepEqual(result.calls.filter(call => call.url).map(call => call.url), ['/api/version/workspace/status', '/api/version/workspace/update']);
  assert.equal(result.calls.find(call => call.url.endsWith('/update')).body.get('plan_fingerprint'), 'refreshed');
  assert.equal(result.calls.at(-1).watch, 'operation-1');
});

test('channel change saves then refreshes and applies only when the authoritative image needs maintenance', async () => {
  for (const after of ['latest', 'custom-tag']) {
    for (const image_state of ['updates_available', 'up_to_date']) {
      const result = review(imageStatus('up_to_date'), { container: true, changes: [{ ...channelChange, after }], refreshed: imageStatus(image_state) });
      assert.ok(result.root.all().some(node => node.textContent?.includes('Applying saves the channel, then re-checks')));
      await result.apply.listeners.click();
      const urls = result.calls.filter(call => call.url).map(call => call.url);
      assert.deepEqual(urls.slice(0, 2), ['/api/minecraft/workspace/settings/apply', '/api/version/workspace/status']);
      assert.equal(urls.includes('/api/version/workspace/update'), image_state === 'updates_available');
      if (image_state === 'up_to_date') assert.equal(result.calls.at(-1).message, 'Changes saved.');
    }
  }
});

test('potential container maintenance requires explicit online-player consent', async () => {
  for (const changes of [[], [channelChange]]) {
    const result = review(imageStatus(changes.length ? 'up_to_date' : 'updates_available'), { container: true, changes, online: 2, refreshed: imageStatus('updates_available'), planExtra: { confirmation_required: true } });
    assert.equal(result.apply.disabled, true);
    assert.equal(result.consent.checked, false);
    assert.equal(result.consent.role, 'switch');
    await result.apply.listeners.click();
    assert.equal(result.calls.length, 0);
    result.consent.checked = true;
    result.consent.listeners.change();
    await result.apply.listeners.click();
    assert.equal(result.calls.find(call => call.url.endsWith('/update')).body.get('confirm_players'), 'yes');
  }
});

test('saved channel with unavailable or unsafe refreshed status stops without retry', async () => {
  for (const refreshed of [imageStatus('unavailable'), imageStatus('updates_available', { plan_fingerprint: '' }), imageStatus('updates_available', { crossplay_enabled: true, crossplay_compatible: false })]) {
    const result = review(imageStatus('up_to_date'), { container: true, changes: [channelChange], refreshed });
    await result.apply.listeners.click();
    assert.equal(result.calls.filter(call => call.url?.endsWith('/settings/apply')).length, 1);
    assert.ok(!result.calls.some(call => call.url?.endsWith('/update')));
    assert.match(result.calls.at(-1).message, /^Changes saved\./);
  }
});

test('container status uses image_state and WebUI never implements a direct pull', () => {
  assert.match(source, /\[status.image_state\]/);
  assert.ok(source.includes('up_to_date: "Current", updates_available: "Update available"'));
  assert.ok(!/podman|exec.Command|child_process/.test(source));
});
