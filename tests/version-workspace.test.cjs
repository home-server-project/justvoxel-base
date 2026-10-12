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
  querySelector(selector) {
    if (selector === '[data-version-custom]') return this.custom;
    return this.all().find(node => selector === '[data-version-review]' && 'versionReview' in node.dataset) || null;
  }
  replaceChildren(...children) { this.children = []; this.append(...children); }
  set innerHTML(value) {
    assert.match(value, /select name="channel"/);
    this.elements = { channel: new Element('select'), custom_tag: new Element('input') };
    this.custom = new Element('label');
    this.custom.append(this.elements.custom_tag);
    this.append(this.elements.channel, this.custom);
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
    Boolean, String, Number, URLSearchParams, csrf: 'csrf', state: {}, root, ticket: 1, sequence: 1,
    pending: { operation }, updateOperation: '',
    minecraft: { server_type: current, image_tag: tag },
    status: { server_software: current[0].toUpperCase() + current.slice(1), image_state: 'up_to_date' },
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
      if (url === '/api/dashboard-status') return { players: { online } };
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
    root, calls, cards, choice, find, form, state: context.state,
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
    warning: root.all().find(node => node.className === 'notice warning'),
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
  plans[0].resolve(); await old; assert.deepEqual(page.reviews(), [review]);
  page.click('paper'); const failed = page.review(); page.click('vanilla');
  plans[2].reject(new Error('Old plan failed')); await failed;
  assertSoftwareSelection(page,'vanilla','paper'); assert.equal(page.reviews().length,0);
  assert.equal(page.state.textContent,'');
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
