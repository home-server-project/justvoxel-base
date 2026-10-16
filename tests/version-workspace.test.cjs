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
  insertBefore(child, reference) {
    const index = this.children.indexOf(reference);
    assert.ok(index >= 0);
    child.parent = this;
    this.children.splice(index, 0, child);
  }
  setAttribute(name, value) { this[name] = value; }
  addEventListener(name, listener) { this.listeners[name] = listener; }
  querySelector(selector) {
    if (selector === '[data-version-custom]') return this.custom;
    if (selector === '.version-channel-row') return this.channelRow;
    if (selector === '[data-version-operation]') return this.all().find(node => 'versionOperation' in node.dataset) || null;
    return this.all().find(node => selector === '[data-version-review]' && 'versionReview' in node.dataset) || null;
  }
  replaceChildren(...children) { this.children = []; this.append(...children); }
  set innerHTML(value) {
    assert.match(value, /select name="channel"/);
    this.html = value;
    this.elements = { channel: new Element('select'), custom_tag: new Element('input') };
    this.custom = new Element('label');
    this.custom.textContent = value.match(/<label data-version-custom>([^<]*)/)?.[1];
    this.custom.append(this.elements.custom_tag);
    assert.doesNotMatch(value, /data-version-custom hidden/);
    this.channelRow = Object.assign(new Element('div'), { className: 'version-channel-row' });
    const channelLabel = new Element('label');
    channelLabel.textContent = value.match(/<label>([^<]*)/)?.[1];
    channelLabel.append(this.elements.channel);
    this.channelRow.append(channelLabel, this.custom);
    this.append(this.channelRow);
  }
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
    location: {origin: 'https://example.test', search: '?server=jv-family1'},
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

test('same-version policy changes use one reviewed instance operation', async () => {
  for (const change of [policyChange, { ...policyChange, before: 'pinned', after: 'recommended' }]) {
    const result = review(statusFor('26.2'), { changes: [change, { field: 'version', before: 'LATEST', after: '26.2' }], online: 2 });
    assert.ok(result.apply);
    assert.equal(result.consent, undefined);
    assert.equal(result.root.all().filter((element) => element.textContent?.startsWith('Minecraft:')).length, 0);
    await result.apply.listeners.click();
    assert.deepEqual(result.calls.filter((call) => call.url).map((call) => call.url), ['/api/version/workspace/update']);
    assert.equal(result.calls.at(-1).watch, 'operation-1');
  }
});

test('one Apply forwards the reviewed fingerprint to one instance operation and watches progress', async () => {
  const result = review(statusFor('26.3', { plan_fingerprint: 'preview' }), { refreshed: statusFor('26.3') });
  assert.equal(result.root.all().filter((element) => element.tag === 'button' && element.textContent.startsWith('Apply')).length, 1);
  await result.apply.listeners.click();
  assert.deepEqual(result.calls.filter((call) => call.url).map((call) => call.url), ['/api/version/workspace/update']);
  const update = result.calls.find((call) => call.url?.endsWith('/update'));
  assert.equal(update.body.get('plan_fingerprint'), 'preview');
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

test('review Apply never automatically replans or retries', async () => {
  const result = review(statusFor('26.3', {plan_fingerprint: 'reviewed-instance-plan'}), { refreshed: statusFor('26.4') });
  await result.apply.listeners.click();
  const calls = result.calls.filter(call => call.url);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, '/api/version/workspace/update');
  assert.equal(calls[0].body.get('plan_fingerprint'), 'reviewed-instance-plan');
});

test('persistent polling updates the frame, keeps journal messages once, and renders terminal evidence', async () => {
  let nextPoll;
  let result = { operation: { operation_id: 'operation-1', state: 'running', status: 'Creating cold backup.' } };
  const messages = [];
  const content = new Element('div');
  const context = vm.createContext({
    URLSearchParams, updateOperation: '', completedOperation: null, state: {}, content, operationKinds: new Map(),
    document: { createElement: tag => new Element(tag) },
    card: title => { const panel = new Element('section'); const heading = new Element('h2'); heading.textContent = title; panel.append(heading); return panel; },
    request: async (url) => { assert.equal(url, '/api/version/workspace/update-operation?id=operation-1'); return result; },
    render: async (message, operation) => { messages.push({message, operation}); },
    setTimeout: (callback, delay) => { assert.equal(delay, 2000); nextPoll = callback; },
  });
  const pollingSource = source.slice(source.indexOf('  const operationFrame ='), source.indexOf('  // Let the native select'));
  vm.runInContext(pollingSource + '\nglobalThis.watchUpdate = watchUpdate; globalThis.operationFrame = operationFrame;', context);
  content.append(context.operationFrame(result.operation));
  await context.watchUpdate('operation-1');
  assert.equal(context.state.textContent, '');
  assert.equal(content.all().filter(node => node.textContent === 'Creating cold backup.').length, 1);
  assert.equal(context.updateOperation, 'operation-1');
  assert.match(content.children[0].className, /is-running/);
  context.operationKinds.set('operation-1', 'switch');
  // A known switch is still running until the journal confirms success.
  assert.equal(context.operationFrame(result.operation).children[0].textContent, 'Server update in progress');
  result = { operation: { operation_id: 'operation-1', state: 'succeeded', status: 'Server update completed.' } };
  await nextPoll();
  assert.equal(context.updateOperation, '');
  assert.equal(messages[0].message, '');
  assert.equal(messages[0].operation, result.operation);
  assert.match(content.children[0].className, /is-succeeded/);
  assert.equal(content.children[0].children[0].textContent, 'Switch succeeded');
  context.operationKinds.set('operation-1', 'container');
  assert.equal(context.operationFrame(result.operation).children[0].textContent, 'Container update succeeded');
  context.operationKinds.clear();
  assert.equal(context.operationFrame(result.operation).children[0].textContent, 'Server update succeeded');
  for (const state of ['failed', 'needs_attention']) {
    const frame = context.operationFrame({state, status:'Actual persistent failure message.'});
    assert.match(frame.className, /is-failed/);
    assert.equal(frame.children[1].textContent, 'Actual persistent failure message.');
  }
});

// Extract the entire Software branch using stable enclosing branch boundaries.
const softwareStart = source.indexOf('        const software = card("");');
const softwareEnd = source.indexOf('      } else {\n        const panel = card("Minecraft Version");', softwareStart);
assert.ok(softwareStart >= 0 && softwareEnd > softwareStart, 'Software branch boundaries must exist');
const softwareSource = source.slice(softwareStart, softwareEnd);
function softwareWorkspace({ current = 'paper', tag = 'stable', online = 0, overrides = {}, planning, operation, failApply = false } = {}) {
  const calls = [];
  const previewPlans = [];
  const root = new Element('div');
  const context = vm.createContext({
    location: {origin: 'https://example.test', search: '?server=jv-family1'},
    Boolean, String, Number, URLSearchParams, csrf: 'csrf', state: {}, root, ticket: 1, sequence: 1,
    pending: { operation }, updateOperation: '', operationKinds: new Map(),
    minecraft: { id: 'jv-family1', server_type: current, image_tag: tag },
    status: { server_software: current[0].toUpperCase() + current.slice(1), image_state: 'up_to_date', ...overrides },
    document: { createElement: (tag) => new Element(tag) },
    card: (title) => Object.assign(new Element('section'), { textContent: title }),
    line: (label, value) => {
      const row = Object.assign(new Element('div'), { textContent: `${label}: ${value}` });
      row.append(Object.assign(new Element('span'), { textContent: label }), Object.assign(new Element('strong'), { textContent: value }));
      return row;
    },
    stackLabel: (value) => value,
    statusURL: (policy, version, target, imageTag) => '/api/version/workspace/status?' + new URLSearchParams({ server_type: target, image_tag: imageTag }),
    request: async (url, options) => {
      calls.push({ url, body: options?.body });
      if (url.startsWith('/api/version/workspace/status?')) {
        const params = new URLSearchParams(url.split('?')[1]);
        const target = params.get('server_type');
        const plan = { ...statusFor('26.2'), server_supported: true, runtime_update_required: true,
          server_software: target[0].toUpperCase() + target.slice(1), image_state: 'up_to_date', ...overrides };
        const response = Promise.resolve(planning ? planning(plan) : plan);
        previewPlans.push(response);
        return response;
      }
      if (url === '/api/minecraft/workspace/snapshot') return { players: { online } };
      if (url === '/api/version/workspace/update') {
        if (failApply) throw new Error('Response lost');
        return { operation: { operation_id: 'software-1' } };
      }
      throw new Error(`Unexpected request: ${url}`);
    },
    render: async (message) => { calls.push({ message }); },
    watchUpdate: (id) => { calls.push({ watch: id }); },
  });
  vm.runInContext(softwareSource, context);
  const cards = root.all().filter(node => node.className?.split(' ').includes('version-software-choice'));
  const choice = target => cards.find(node => node.children[1].children[0].textContent.toLowerCase() === target);
  const find = text => root.all().find(node => node.textContent === text);
  const form = root.all().find(node => node.tag === 'form');
  return {
    root, calls, cards, choice, find, form, state: context.state, operationKinds: context.operationKinds,
    click: target => choice(target).listeners.click(),
    review: async () => {
      const firstPlan = previewPlans.length;
      // Submit starts preview without returning its Promise. Wait for this
      // submission's plan (including deferred/rejected stale plans), then let
      // the request and Promise.all continuations finish before inspecting DOM.
      form.listeners.submit({ preventDefault() {} });
      await Promise.allSettled(previewPlans.slice(firstPlan));
      await new Promise(resolve => setImmediate(resolve));
    },
    check: () => find('Check for updates').listeners.click(),
    channel: value => { form.elements.channel.value = value; form.elements.channel.listeners.change(); },
    custom: value => { form.elements.custom_tag.value = value; form.elements.custom_tag.listeners.input(); },
    warning: root.all().find(node => node.className === 'notice warning' && !('versionCompatibility' in node.dataset)),
    reviews: () => root.all().filter(node => 'versionReview' in node.dataset),
    consent: () => root.all().find(node => node.tag === 'input' && node.type === 'checkbox'),
  };
}
async function softwareReview(overrides = {}, options = {}) {
  const page = softwareWorkspace({ overrides, ...options });
  page.click('purpur');
  page.channel('latest');
  await page.review();
  return { ...page, apply: page.find('Apply changes'), cancel: page.find('Cancel review'), consent: page.consent() };
}
function assertSoftwareSelection(page, selected, installed) {
  for (const node of page.cards) {
    const type = node.children[1].children[0].textContent.toLowerCase();
    assert.equal(node.className.includes('is-selected'), type === selected);
    assert.equal(node.className.includes('is-current'), type === installed);
    assert.equal(node['aria-pressed'], String(type === selected));
    assert.equal(node.children[1].children[1].textContent,
      type === installed ? 'Current' : installed !== 'vanilla' && type !== 'vanilla' ? 'Switch Available' : 'Switch Unavailable');
  }
}
function operationPresentation(operationKinds = new Map()) {
  const context = vm.createContext({
    operationKinds, document: { createElement: tag => new Element(tag) },
    card: title => {
      const panel = new Element('section');
      panel.append(Object.assign(new Element('h2'), { textContent: title }));
      return panel;
    },
  });
  vm.runInContext(source.slice(source.indexOf('  const operationFrame ='), source.indexOf('  const watchUpdate ='))
    + '\nglobalThis.operationFrame = operationFrame;', context);
  return context;
}
const currentComponents = {
  minecraft_state: 'up_to_date', image_state: 'up_to_date',
  geyser_state: 'disabled', floodgate_state: 'disabled', viaversion_state: 'managed_automatically',
};
for (const [name, target, overrides, heading] of [
  ['software switch', 'purpur', {}, 'Switch succeeded'],
  ['container-only update', 'paper', { image_state: 'updates_available' }, 'Container update succeeded'],
  ['Minecraft game-version-only update', 'paper', { selected_candidate: '26.3', minecraft_state: 'updates_available' }, 'Server update succeeded'],
  ['managed-plugin-only update', 'paper', { geyser_state: 'updates_available' }, 'Server update succeeded'],
  ['mixed update', 'paper', { selected_candidate: '26.3', minecraft_state: 'updates_available', image_state: 'updates_available', floodgate_state: 'updates_available' }, 'Server update succeeded'],
  ['image update with missing plugin evidence', 'paper', { image_state: 'updates_available', geyser_state: undefined }, 'Server update succeeded'],
  ['image update with unknown plugin evidence', 'paper', { image_state: 'updates_available', geyser_state: 'unavailable' }, 'Server update succeeded'],
  ['image and plugin update', 'paper', { image_state: 'updates_available', floodgate_state: 'updates_available' }, 'Server update succeeded'],
  ['image and game-version update', 'paper', { image_state: 'updates_available', selected_candidate: '26.3' }, 'Server update succeeded'],
]) {
  test(`${name} success uses the reviewed plan label`, async () => {
    const page = softwareWorkspace({ overrides: { ...currentComponents, ...overrides } });
    page.click(target);
    await page.review();
    const consent = page.consent();
    consent.checked = true; consent.listeners.change();
    await page.find('Apply changes').listeners.click();
    const operation = { operation_id: 'software-1', state: 'succeeded', status: 'Server update completed.' };
    const frame = operationPresentation(page.operationKinds).operationFrame(operation);
    assert.equal(frame.children[0].textContent, heading);
    assert.equal(frame.children[1].textContent, operation.status);
    assert.equal(page.calls.find(call => 'message' in call).message, undefined);
    assert.equal(page.calls.filter(call => call.body).length, 1);
  });
}
test('unknown operation after reconnect uses generic success without inferring its kind', () => {
  // Even a remembered kind for a different operation is not evidence for this one.
  const context = operationPresentation(new Map([['previous-operation', 'switch']]));
  Object.assign(context, {
    root: new Element('div'), pending: {}, confirmedOperation: { operation_id: 'reconnected', state: 'succeeded', status: 'Server update completed.' },
    updateOperation: '', watchUpdate: () => assert.fail('A successful operation must not restart polling'),
  });
  const start = source.indexOf('      const operation = pending.operation || confirmedOperation;');
  vm.runInContext(source.slice(start, source.indexOf('      if (currentTab === "software")', start)), context);
  assert.equal(context.root.children[0].children[0].textContent, 'Server update succeeded');
  assert.equal(context.operationKinds.has('reconnected'), false);
});
test('render shows journal progress once inside the operation frame and preserves unrelated messages', async () => {
  const operation = { operation_id: 'persisted', state: 'running', status: 'Creating cold backup.' };
  const context = operationPresentation();
  const root = new Element('div');
  root.append(context.operationFrame(operation));
  Object.assign(context, { root, operation, content: new Element('div'), state: {}, ticket: 1, sequence: 1, initializePreview: undefined, settleSelectChange: async () => {} });
  const start = source.indexOf('      await settleSelectChange();\n      if (ticket !== sequence) return;\n      content.replaceChildren(root);');
  assert.ok(start >= 0);
  const finish = source.slice(start, source.indexOf('    } catch (failure)', start));
  for (const [message, expected] of [
    ['', ''], [operation.status, ''], ['Response lost', 'Response lost'],
    ['Verification failed.', 'Verification failed.'],
    ['Current server verified and kept. Review changes again when ready.', 'Current server verified and kept. Review changes again when ready.'],
  ]) {
    context.message = message;
    await vm.runInContext('(async () => {\n' + finish + '\n})()', context);
    assert.equal(context.state.textContent, expected);
    assert.equal(context.content.all().filter(node => node.textContent === operation.status).length, 1);
    assert.equal(context.content.children[0].children[0].children[1].textContent, operation.status);
    assert.ok(!context.content.all().some(node => node.textContent === 'Server update started.'));
  }
});
test('selection stays read-only until explicit combined review and Apply', async () => {
  const page = softwareWorkspace();
  page.click('purpur'); page.channel('latest');
  assert.equal(page.calls.length, 0);
  await page.check();
  assert.equal(page.reviews().length, 0);
  assert.ok(page.calls.every(call => !call.body));
  await page.review();
  const planCalls = page.calls.filter(call => call.url.includes('/status?'));
  for (const call of planCalls) {
    const params = new URLSearchParams(call.url.split('?')[1]);
    assert.equal(params.get('server_type'), 'purpur');
    assert.equal(params.get('image_tag'), 'latest');
  }
  assert.equal(page.reviews().length, 1);
  assert.ok(!page.calls.some(call => call.body));
});
test('combined review has one OFF switch and ordered Cancel and Apply; submits both selections once', async () => {
  const page = await softwareReview({}, { online: 3 });
  for (const text of ['Server software: Paper → Purpur', 'Container channel: stable → latest', 'Minecraft version: 26.2', 'Players online: 3']) assert.ok(page.find(text), text);
  assert.equal(page.reviews()[0].children.find(node => node.className === 'version-software-review-summary').children.length, 6);
  assert.equal(page.consent.role, 'switch'); assert.equal(page.consent.checked, false);
  assert.equal(page.apply.disabled, true);
  assert.equal(page.cancel.className, 'secondary');
  assert.deepEqual(page.apply.parent.children, [page.cancel, page.apply]);
  const before = page.calls.length;
  await page.apply.listeners.click(); assert.equal(page.calls.length, before);
  page.consent.checked = true; page.consent.listeners.change();
  const applying = page.apply.listeners.click();
  assert.equal(page.cancel.disabled, true); assert.equal(page.consent.disabled, true);
  await page.apply.listeners.click(); await applying;
  const posts = page.calls.filter(call => call.body);
  assert.equal(posts.length, 1);
  assert.equal(posts[0].url, '/api/version/workspace/update');
  for (const [key, value] of Object.entries({csrf:'csrf',server_type:'purpur',image_tag:'latest',plan_fingerprint:'refreshed',confirm_players:'yes'})) assert.equal(posts[0].body.get(key), value);
  assert.equal(page.calls.at(-1).watch, 'software-1');
});
test('unsafe and unavailable combined plans cannot be confirmed or applied', async () => {
  for (const overrides of [{stack_state:'up_to_date',update_available:false},{stack_state:'unavailable'}, {server_supported:false}, {crossplay_enabled:true,crossplay_compatible:false}, {plan_fingerprint:''}]) {
    const page = await softwareReview(overrides);
    assert.equal(page.consent, undefined);
    assert.equal(page.apply.disabled, true);
    await page.apply.listeners.click();
    assert.ok(!page.calls.some(call => call.body));
    page.cancel.listeners.click(); assert.equal(page.reviews().length, 0);
  }
  const safe = await softwareReview({crossplay_enabled:true,crossplay_compatible:true});
  safe.consent.checked = true; safe.consent.listeners.change(); assert.equal(safe.apply.disabled, false);
});
test('Cancel restores installed software and channel and permits a new review', async () => {
  for (const current of ['paper','purpur']) {
    const page = softwareWorkspace({ current, tag:'custom-old' });
    const target = current === 'paper' ? 'purpur' : 'paper';
    page.click(target); page.channel('latest'); await page.review();
    page.find('Cancel review').listeners.click();
    assertSoftwareSelection(page, current, current);
    assert.equal(page.form.elements.channel.value, 'custom');
    assert.equal(page.form.elements.custom_tag.value, 'custom-old');
    assert.equal(page.reviews().length, 0); assert.equal(page.form.hidden, false);
    page.click(target); await page.review(); assert.equal(page.reviews().length, 1);
    assert.ok(!page.calls.some(call => call.body));
  }
});
test('Vanilla switching only shows the unsupported warning and can be cancelled by selecting installed software', async () => {
  for (const current of ['paper','purpur','vanilla']) {
    const page = softwareWorkspace({current});
    const target = current === 'vanilla' ? 'paper' : 'vanilla';
    if (current !== 'vanilla') { page.click('purpur'); await page.review(); }
    page.click(target);
    const before = page.calls.length;
    await page.review(); await page.check();
    assert.equal(page.calls.length, before);
    assertSoftwareSelection(page, target, current);
    assert.equal(page.warning.hidden, false);
    assert.match(page.warning.textContent, /Requires Reset Minecraft and setup again/);
    assert.equal(page.reviews().length, 0); assert.equal(page.consent(), undefined);
    page.click(current); assert.equal(page.warning.hidden, true);
    assertSoftwareSelection(page, current, current);
  }
});
test('Custom input is enabled only for Custom and malformed tags never reach preview', async () => {
  const page = softwareWorkspace();
  assert.equal(page.form.elements.custom_tag.disabled, true);
  page.channel('custom'); assert.equal(page.form.elements.custom_tag.disabled, false);
  assert.equal(page.form.elements.custom_tag.required, true);
  for (const tag of ['', '../bad', '-bad', 'a'.repeat(129), 'tag:other']) {
    page.custom(tag); await page.review(); assert.equal(page.calls.length, 0);
  }
  page.custom('release_1.2-3'); await page.review();
  assert.ok(page.calls[0].url.includes('image_tag=release_1.2-3'));
  page.channel('stable'); assert.equal(page.form.elements.custom_tag.disabled, true);
  assert.equal(page.reviews().length, 0);
});
test('verified identical-image channel review explains that runtime stays unchanged', async () => {
  const page = softwareWorkspace({overrides:{runtime_update_required:false}});
  page.channel('latest'); await page.review();
  assert.ok(page.root.all().some(node => node.textContent?.includes('current running or stopped state')));
  assert.ok(!page.root.all().some(node => node.textContent?.includes('cold backup')));
  const consent = page.consent(); consent.checked = true; consent.listeners.change();
  await page.find('Apply changes').listeners.click();
  assert.equal(page.calls.filter(call => call.body).length, 1);
});
test('late or failed previews cannot replace a fresh review or undo a changed selection', async () => {
  const plans = [];
  const page = softwareWorkspace({planning: plan => new Promise((resolve,reject) => plans.push({resolve:()=>resolve(plan),reject}))});
  page.click('purpur'); const old = page.review();
  page.click('vanilla'); page.click('purpur'); const fresh = page.review();
  plans[1].resolve(); await fresh; const review = page.reviews()[0];
  const notice = page.root.all().find(node => 'versionCompatibility' in node.dataset);
  const freshNotice = notice.textContent;
  plans[0].resolve(); await old; assert.deepEqual(page.reviews(), [review]);
  assert.equal(notice.textContent, freshNotice);
  page.click('paper'); const failed = page.review(); page.click('vanilla');
  plans[2].reject(new Error('Old plan failed')); await failed;
  assertSoftwareSelection(page,'vanilla','paper'); assert.equal(page.reviews().length,0);
  assert.equal(page.state.textContent,'');
  assert.equal(notice.hidden, true);
  assert.equal(notice.textContent, '');
});
test('lost Apply response reconnects to persisted state without retry or partial success', async () => {
  const page = await softwareReview({}, {failApply:true});
  page.consent.checked=true; page.consent.listeners.change();
  await page.apply.listeners.click(); await page.apply.listeners.click();
  assert.equal(page.calls.filter(call=>call.body).length,1);
  assert.equal(page.calls.at(-1).message,'Response lost');
  assert.ok(!page.calls.some(call=>call.url?.includes('/settings/apply')));
});
test('an existing operation disables software and channel changes', async () => {
  const page = softwareWorkspace({operation:{operation_id:'existing'}});
  assert.ok(page.cards.every(node=>node.disabled));
  assert.equal(page.form.elements.channel.disabled,true);
  page.click('purpur'); await page.review(); await page.check();
  assertSoftwareSelection(page,'paper','paper'); assert.equal(page.calls.length,0);
});
test('image status labels use image_state and the browser never pulls directly', () => {
  assert.ok(source.includes('imageLabel(status.image_state)'));
  assert.ok(source.includes('up_to_date: "Current", updates_available: "Update available"'));
  assert.ok(!/podman|exec.Command|child_process/.test(source));
});

test('software workspace fails closed when persistent operation tracking is unavailable', () => {
  assert.ok(source.includes('if (currentTab === "software") throw failure;'));
});

const css = readFileSync(path.join(__dirname, '../webui/internal/server/static/app.css'), 'utf8');
function cssRule(selector) {
  const start = css.indexOf(selector + '{');
  assert.ok(start >= 0, selector);
  return css.slice(start + selector.length + 1, css.indexOf('}', start));
}
test('channel controls share one desktop row and Custom stays present for all channel choices', () => {
  const page = softwareWorkspace();
  const row = page.form.querySelector('.version-channel-row');
  assert.deepEqual(row.children.map(node => node.tag), ['label', 'label', 'button', 'button']);
  assert.equal(row.children[0].children[0], page.form.elements.channel);
  assert.equal(row.children[1].children[0], page.form.elements.custom_tag);
  assert.deepEqual(row.children.slice(2).map(node => node.textContent), ['Check for updates', 'Review changes']);
  assert.equal(row.children[2].type, 'button');
  assert.equal(row.children[3].type, 'submit');
  for (const channel of ['stable', 'latest', 'custom', 'stable']) {
    page.channel(channel);
    assert.ok(!page.form.custom.hidden);
    assert.equal(page.form.elements.custom_tag.disabled, channel !== 'custom');
  }
  assert.doesNotMatch(source, /\[data-version-custom\]"\)\.hidden/);
  assert.match(cssRule('.version-workspace-body form.version-combined-form'), /display:block/);
  assert.match(cssRule('.version-workspace-body .version-channel-row'), /display:grid;grid-template-columns:repeat\(2,minmax\(0,1fr\)\) auto auto;align-items:end/);
  assert.ok(css.includes('@media(max-width:700px){.version-workspace-body .version-container-status{flex-wrap:wrap}.version-workspace-body .version-channel-row{grid-template-columns:repeat(2,minmax(0,1fr))}'));
  assert.ok(css.includes('@media(max-width:430px){.version-workspace-body .version-channel-row{grid-template-columns:minmax(0,1fr)}}'));
});
test('both channel labels sit directly above equally sized fields with identical typography', () => {
  const page = softwareWorkspace();
  const row = page.form.querySelector('.version-channel-row');
  assert.deepEqual(page.form.children, [row]);
  assert.deepEqual(row.children.slice(0, 2).map(label => label.textContent), ['Change update channel to:', 'Custom tag']);
  assert.match(page.form.html, /^<div class="version-channel-row"><label>Change update channel to:<select/);
  assert.doesNotMatch(page.form.html, /<p>/);
  const labels = cssRule('.version-workspace-body .version-channel-row>label');
  assert.match(labels, /display:flex;flex-direction:column/);
  assert.match(labels, /margin:0;font-size:.88rem;line-height:1.35/);
  const fields = cssRule('.version-workspace-body .version-channel-row select,.version-workspace-body .version-channel-row input');
  for (const declaration of ['width:100%', 'height:2.5rem', 'padding:.52rem .65rem', 'margin:.2rem 0 0', 'border:1px solid var(--jv-border-strong)', 'border-radius:8px', 'box-sizing:border-box', 'font:inherit']) {
    assert.ok(fields.split(';').includes(declaration), declaration);
  }
});
test('both container properties share one desktop status row with spaced labels and bold values', () => {
  for (const [tag, channel] of [['stable', 'Stable'], ['latest', 'Latest'], ['custom-release', 'Custom']]) {
    const page = softwareWorkspace({tag, overrides:{image_state:'updates_available', minecraft_state:'up_to_date'}});
    const status = page.root.all().find(node => node.className === 'version-container-status');
    assert.equal(status.parent, page.form.parent);
    assert.deepEqual(status.children.map(row => row.children.map(node => node.textContent)), [
      ['Container image:', 'Update available'], ['Current container channel:', channel],
    ]);
    for (const row of status.children) {
      assert.deepEqual(row.children.map(node => node.tag), ['span', 'strong']);
      assert.ok(row.className.includes('version-container-detail'));
    }
  }
  const context = vm.createContext({document:{createElement: tag => new Element(tag)}});
  vm.runInContext(source.slice(source.indexOf('  const line ='), source.indexOf('  const stackLabel =')) + '\nglobalThis.line = line;', context);
  const row = context.line('Container image:', 'Update available');
  assert.equal(row.className, 'version-detail-line');
  assert.deepEqual(row.children.map(node => node.tag), ['span', 'strong']);
  assert.deepEqual(row.children.map(node => node.textContent), ['Container image:', 'Update available']);
  assert.match(cssRule('.version-workspace-body .version-container-detail'), /display:flex;align-items:baseline;gap:.4rem/);
  assert.match(cssRule('.version-workspace-body .version-container-detail'), /white-space:nowrap/);
  assert.match(cssRule('.version-workspace-body .version-container-status'), /display:flex;align-items:baseline;gap:1.5rem;flex-wrap:nowrap/);
  assert.doesNotMatch(source, /Configured container channel:/);
  assert.ok(source.includes('configuredChannel.className += " version-container-detail"'));
  assert.ok(source.includes('stable: "Stable", latest: "Latest"'));
});
test('dynamic compatibility messages stay once at the bottom of updates, including after checks and reviews', async () => {
  for (const [version, stackState, compatible] of [['26.3', 'updates_available', true], ['27.1', 'waiting_for_compatibility', false]]) {
    const reason = `Minecraft ${version} is waiting for released cross-play support.\nCompatible server updates are ready to review.`;
    const page = softwareWorkspace({overrides:{reason, crossplay_enabled:true, crossplay_compatible:compatible, stack_state:stackState, available:version, recommended:'26.2'}});
    const notice = page.root.all().find(node => 'versionCompatibility' in node.dataset);
    const panel = page.form.parent;
    const assertNotice = () => {
      assert.equal(notice.textContent, reason);
      assert.equal(notice.className, page.warning.className);
      assert.equal(notice.className, 'notice warning');
      assert.equal(notice.hidden, false);
      assert.equal(panel.children.at(-1), notice);
      assert.equal(page.root.all().filter(node => node.textContent === reason).length, 1);
      assert.ok(page.root.children.indexOf(panel) > page.root.children.indexOf(page.cards[0].parent.parent));
      assert.ok(!page.state.textContent?.includes(reason));
    };
    assertNotice();
    await page.check();
    assertNotice();
    assert.equal(page.state.textContent, '');
    assert.equal(page.reviews().length, 0);
    assert.ok(page.calls.every(call => !call.body));
    await page.review();
    assertNotice();
    assert.equal(page.reviews()[0].parent, panel);
    if (!compatible) {
      assert.equal(page.consent(), undefined);
      assert.equal(page.find('Apply changes').disabled, true);
    } else assert.ok(page.root.all().some(node => node.textContent?.includes('cold backup')));
    page.click('vanilla');
    assert.equal(page.warning.hidden, false);
    assert.equal(panel.hidden, true);
    assert.match(page.warning.textContent, /Requires Reset Minecraft and setup again/);
    page.click('paper');
    assert.equal(page.warning.hidden, true);
    assert.equal(panel.hidden, false);
    assert.equal(notice.hidden, true);
    assert.equal(notice.textContent, '');
    await page.check();
    assertNotice();
  }
  const page = softwareWorkspace();
  assert.equal(page.root.all().find(node => 'versionCompatibility' in node.dataset).hidden, true);
});
test('routine update results keep neutral read-only feedback once at the bottom through Review and Cancel', async () => {
  for (const [stackState, updateAvailable, reason, expected] of [
    ['up_to_date', false, '', 'No changes were found.'],
    ['up_to_date', false, 'Your JustVoxel server is up to date. No action needed.', 'Your JustVoxel server is up to date. No action needed.'],
    ['updates_available', true, '', 'Changes are ready to review.'],
    ['updates_available', true, 'Compatible server updates are ready to review.', 'Compatible server updates are ready to review.'],
  ]) {
    const page = softwareWorkspace({overrides:{stack_state:stackState, update_available:updateAvailable, reason, crossplay_enabled:true, crossplay_compatible:true, available:'26.2', recommended:'26.2'}});
    const notice = page.root.all().find(node => 'versionCompatibility' in node.dataset);
    const panel = page.form.parent;
    if (reason) {
      assert.equal(notice.textContent, reason);
      assert.equal(notice.className, 'muted compact');
      assert.equal(notice.hidden, false);
    }
    const assertFeedback = () => {
      assert.equal(notice.textContent, expected);
      assert.equal(notice.className, 'muted compact');
      assert.equal(notice.hidden, false);
      assert.equal(panel.children.at(-1), notice);
      assert.equal(page.root.all().filter(node => node.textContent === expected).length, 1);
      assert.equal(page.state.textContent, '');
    };
    await page.check();
    assertFeedback();
    assert.equal(page.reviews().length, 0);
    assert.ok(page.calls.every(call => !call.body));
    await page.review();
    assertFeedback();
    assert.equal(page.reviews()[0].parent, panel);
    assert.ok(!page.reviews()[0].all().some(node => node.textContent === expected));
    page.find('Cancel review').listeners.click();
    assert.equal(page.reviews().length, 0);
    assert.equal(page.form.hidden, false);
    assert.equal(notice.hidden, true);
    assert.equal(notice.textContent, '');
    assert.equal(panel.children.at(-1), notice);
    await page.check();
    assertFeedback();
    assert.ok(page.calls.every(call => !call.body));
  }
});
test('structured cross-play states select warning style without matching reason text or fixed versions', async () => {
  for (const fields of [
    {crossplay_compatible:false, stack_state:'unavailable'},
    {crossplay_compatible:true, stack_state:'waiting_for_compatibility', update_available:false},
    {crossplay_compatible:true, stack_state:'updates_available', available:'future-release', recommended:'supported-release'},
  ]) {
    const reason = 'Compatibility guidance from the status contract.';
    const page = softwareWorkspace({overrides:{crossplay_enabled:true, reason, ...fields}});
    const notice = page.root.all().find(node => 'versionCompatibility' in node.dataset);
    for (const preview of [page.check, page.review]) {
      await preview();
      assert.equal(notice.className, 'notice warning');
      assert.equal(notice.textContent, reason);
      assert.equal(notice.hidden, false);
      assert.equal(page.root.all().filter(node => node.textContent === reason).length, 1);
      assert.equal(page.form.parent.children.at(-1), notice);
    }
    page.find('Cancel review').listeners.click();
    assert.equal(notice.hidden, true);
    assert.equal(notice.textContent, '');
    await page.check();
    assert.equal(notice.className, 'notice warning');
    assert.equal(notice.textContent, reason);
  }
});
test('changing the selected channel or software clears compatibility and a different result replaces it', async () => {
  const reason = 'A newer Minecraft release is waiting for cross-play support.';
  let next = {reason, crossplay_enabled:true, crossplay_compatible:true, available:'new-release', recommended:'supported-release', stack_state:'updates_available'};
  const page = softwareWorkspace({overrides:next, planning:plan => ({...plan, ...next})});
  const notice = page.root.all().find(node => 'versionCompatibility' in node.dataset);
  await page.review();
  assert.equal(notice.className, 'notice warning');
  assert.equal(page.root.all().filter(node => node.textContent === reason).length, 1);
  page.channel('latest');
  assert.equal(notice.hidden, true);
  assert.equal(notice.textContent, '');
  assert.equal(page.reviews().length, 0);
  next = {reason:'', crossplay_enabled:true, crossplay_compatible:true, available:'supported-release', recommended:'supported-release', stack_state:'up_to_date', update_available:false};
  await page.review();
  assert.equal(notice.textContent, 'No changes were found.');
  assert.equal(notice.className, 'muted compact');
  assert.equal(page.root.all().filter(node => node.textContent === 'No changes were found.').length, 1);
  assert.ok(!page.root.all().some(node => node.textContent === reason));
  assert.equal(page.form.parent.children.at(-1), notice);
  page.click('purpur');
  assert.equal(notice.hidden, true);
  assert.equal(notice.textContent, '');
  await page.check();
  assert.equal(notice.className, 'muted compact');
  page.channel('custom');
  await page.check();
  page.custom('another-tag');
  assert.equal(notice.hidden, true);
  assert.equal(notice.textContent, '');
});
test('preview failure clears old compatibility feedback and preserves the actual failure without a review', async () => {
  for (const reviewRequested of [false, true]) {
    let fail = false;
    const reason = 'Released cross-play support is not ready yet.';
    const page = softwareWorkspace({overrides:{reason, crossplay_enabled:true, crossplay_compatible:false, stack_state:'unavailable'}, planning:plan => {
      if (fail) throw new Error('Version preview failed.');
      return plan;
    }});
    const notice = page.root.all().find(node => 'versionCompatibility' in node.dataset);
    await page.review();
    assert.equal(notice.className, 'notice warning');
    assert.equal(page.root.all().filter(node => node.textContent === reason).length, 1);
    fail = true;
    if (reviewRequested) await page.review();
    else await page.check();
    assert.equal(page.state.textContent, 'Version preview failed.');
    assert.equal(notice.hidden, true);
    assert.equal(notice.textContent, '');
    assert.equal(page.reviews().length, 0);
    assert.ok(!page.root.all().some(node => node.textContent === reason));
    assert.equal(page.find('Check for updates').disabled, false);
    assert.equal(page.find('Review changes').disabled, false);
    assert.equal(page.form.parent.children.at(-1), notice);
    fail = false;
    await page.check();
    assert.equal(page.state.textContent, '');
    assert.equal(notice.textContent, reason);
    assert.equal(notice.className, 'notice warning');
  }
});
test('container image status remains independent of Minecraft status on initial render and read-only checks', async () => {
  for (const [minecraftState, imageState, label] of [['up_to_date', 'updates_available', 'Update available'], ['updates_available', 'up_to_date', 'Current']]) {
    const page = softwareWorkspace({overrides:{minecraft_state:minecraftState, image_state:imageState}});
    const image = page.root.all().find(node => node.children[0]?.textContent === 'Container image:');
    assert.equal(image.children[1].textContent, label);
    await page.check();
    assert.equal(image.children[1].textContent, label);
    assert.ok(page.calls.every(call => !call.body));
    assert.equal(page.reviews().length, 0);
  }
});
test('a verified image update remains reviewable on the unchanged Stable channel', async () => {
  const page = softwareWorkspace({overrides:{image_state:'updates_available'}});
  await page.check();
  assert.equal(page.reviews().length, 0);
  await page.review();
  const apply = page.find('Apply changes');
  assert.equal(apply.disabled, true);
  const consent = page.consent(); consent.checked = true; consent.listeners.change();
  assert.equal(apply.disabled, false);
  await apply.listeners.click();
  assert.equal(page.calls.filter(call => call.body).length, 1);
  assert.equal(page.calls.find(call => call.body).body.get('image_tag'), 'stable');
  assert.equal(page.calls.find(call => call.body).body.get('server_type'), 'paper');
});
test('Minecraft overview keeps game status separate and gives the waiting version a full desktop line', () => {
  const start = source.indexOf('        const panel = card("Minecraft Version");');
  const end = source.indexOf('        panel.appendChild(grid);', start);
  const context = vm.createContext({
    document:{createElement:tag=>new Element(tag)},
    minecraft:{version_mode:'recommended'},
    status:{installed:'26.2',available:'26.3',recommended:'26.2',crossplay_enabled:true,policy:'recommended',minecraft_state:'up_to_date',stack_state:'updates_available',image_state:'updates_available'},
    policyLabel:value=>value,releaseLabel:value=>value,stackLabel:value=>value,
  });
  const helpers = source.slice(source.indexOf('  const line ='), source.indexOf('  const stackLabel ='));
  vm.runInContext(helpers + source.slice(start,end) + '\nglobalThis.grid = grid;', context);
  const entry = label => context.grid.children.find(row => row.children[0].textContent === label);
  assert.equal(entry('Minecraft update').children[1].textContent, 'up_to_date');
  const waiting = entry('New Minecraft version');
  assert.ok(waiting.className.includes('version-new-minecraft'));
  assert.equal(waiting.children[1].textContent, '26.3 · Waiting for cross-play support');
  assert.match(cssRule('.version-workspace-body .version-status-grid>.version-new-minecraft'), /grid-column:1 \/ -1/);
  assert.match(cssRule('.version-workspace-body .version-new-minecraft strong'), /white-space:nowrap;overflow-wrap:normal/);
  assert.doesNotMatch(cssRule('.version-workspace-body .version-new-minecraft strong'), /font-size/);
  assert.ok(!context.grid.all().some(node=>['button','input','select'].includes(node.tag)));
  assert.match(source, /imageLabel\(status.image_state\)/);
});
test('operation framing and state accents are scoped to Version Workspace', () => {
  assert.match(cssRule('.version-workspace-body .version-operation-status'), /border:1px solid var\(--version-operation-accent\);border-left-width:4px/);
  for (const [state,color] of [['running','warning'],['succeeded','success'],['failed','error']]) {
    assert.equal(cssRule('.version-workspace-body .version-operation-status.is-'+state), '--version-operation-accent:var(--jv-'+color+')');
  }
  assert.ok(!source.includes('state.textContent = operation?.status'));
  assert.ok(source.includes('const progress = operationFrame(operation)'));
});

test('failed operation keeps verified acknowledgement and reconnect polling stays intact', async () => {
  const start = source.indexOf('      const operation = pending.operation || confirmedOperation;');
  const end = source.indexOf('      if (currentTab === "software")', start);
  for (const failure of [false, true]) {
    const calls = [];
    const root = new Element('div');
    const context = vm.createContext({
      root, pending:{operation:{operation_id:'persisted',state:'needs_attention',status:'Recovery evidence retained.'}},
      confirmedOperation:null,completedOperation:null,updateOperation:'',csrf:'csrf',URLSearchParams,state:{},
      document:{createElement:tag=>new Element(tag)},
      operationFrame:operation => { const panel=new Element('section'); panel.textContent=operation.status; return panel; },
      request:async (url,options)=>{ calls.push({url,body:options.body}); if(failure) throw new Error('Verification failed.'); },
      render:async message=>calls.push({message}),
      watchUpdate:id=>calls.push({watch:id}),
    });
    vm.runInContext(source.slice(start,end),context);
    const acknowledge = root.all().find(node=>node.tag==='button');
    assert.equal(acknowledge.textContent,'Verify and keep current server');
    await acknowledge.listeners.click();
    assert.equal(calls[0].url,'/api/version/workspace/update');
    assert.equal(calls[0].body.get('operation_id'),'persisted');
    assert.equal(calls[0].body.get('acknowledge'),'yes');
    assert.equal(calls[0].body.has('plan_fingerprint'),false);
    if(failure) {
      assert.equal(context.state.textContent,'Verification failed.');
      assert.equal(acknowledge.disabled,false);
      assert.equal(calls.length,1);
    } else assert.match(calls[1].message,/Current server verified and kept/);
  }
  assert.ok(source.includes('completedOperation = operation;'));
  assert.ok(source.includes('confirmedOperation = completedOperation'));
  assert.ok(source.includes('watchUpdate(operation.operation_id)'));
});
