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
  get firstChild() { return this.children[0]; }
  append(...children) { children.forEach((child) => { if (child.parent) child.parent.children = child.parent.children.filter(node => node !== child); child.parent = this; this.children.push(child); }); }
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
    justVoxelSelectionGeneration: { version: 0 },
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
    justVoxelSelectionGeneration: { version: 0 },
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
    selection: 0, justVoxelSelectionGeneration: {version: 0},
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
    warning: root.all().find(node => node.className === 'notice warning' && !('versionCompatibility' in node.dataset)),
    reviews: () => root.all().filter(node => 'versionReview' in node.dataset),
    consent: () => root.all().find(node => node.tag === 'input' && node.type === 'checkbox'),
  };
}
async function softwareReview(overrides = {}, options = {}) {
  const page = softwareWorkspace({ overrides, ...options });
  page.click('purpur');
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
test('resolved operations use normal-user keep-current success wording', () => {
  for (const kind of [undefined, 'switch', 'container']) {
    const context = operationPresentation(kind ? new Map([['resolved-operation', kind]]) : new Map());
    const frame = context.operationFrame({operation_id:'resolved-operation',state:'resolved',status:''});
    assert.equal(frame.children[0].textContent, 'Current version kept');
    assert.doesNotMatch(frame.children[0].textContent, /verified/i);
  }
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
    ['Current version kept. Review changes again when ready.', 'Current version kept. Review changes again when ready.'],
  ]) {
    context.message = message;
    await vm.runInContext('(async () => {\n' + finish + '\n})()', context);
    assert.equal(context.state.textContent, expected);
    assert.equal(context.content.all().filter(node => node.textContent === operation.status).length, 1);
    assert.equal(context.content.children[0].children[0].children[1].textContent, operation.status);
    assert.ok(!context.content.all().some(node => node.textContent === 'Server update started.'));
  }
});
const css = readFileSync(path.join(__dirname, '../webui/internal/server/static/app.css'), 'utf8');
function cssRule(selector) {
  const start = css.indexOf(selector + '{');
  assert.ok(start >= 0, selector);
  return css.slice(start + selector.length + 1, css.indexOf('}', start));
}
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

});
test('operation framing and state accents are scoped to Version Workspace', () => {
  assert.match(cssRule('.version-workspace-body .version-operation-status'), /border:1px solid var\(--version-operation-accent\);border-left-width:4px/);
  for (const [state,color] of [['running','warning'],['succeeded','success'],['failed','error']]) {
    assert.equal(cssRule('.version-workspace-body .version-operation-status.is-'+state), '--version-operation-accent:var(--jv-'+color+')');
  }
  assert.ok(!source.includes('state.textContent = operation?.status'));
  assert.ok(source.includes('const progress = operationFrame(operation)'));
});

test('failed operation keeps recovery acknowledgement and reconnect polling stays intact', async () => {
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
    assert.equal(acknowledge.textContent,'Keep current version');
    await acknowledge.listeners.click();
    assert.equal(calls[0].url,'/api/version/workspace/update');
    assert.equal(calls[0].body.get('operation_id'),'persisted');
    assert.equal(calls[0].body.get('acknowledge'),'yes');
    assert.equal(calls[0].body.has('plan_fingerprint'),false);
    if(failure) {
      assert.equal(context.state.textContent,'Verification failed.');
      assert.equal(acknowledge.disabled,false);
      assert.equal(calls.length,1);
    } else {
      assert.equal(calls[1].message,'Current version kept. Review changes again when ready.');
      assert.doesNotMatch(calls[1].message,/verified/i);
    }
  }
  assert.ok(source.includes('completedOperation = operation;'));
  assert.ok(source.includes('confirmedOperation = completedOperation'));
  assert.ok(source.includes('watchUpdate(operation.operation_id)'));
});

test('selection generation detaches scheduled update watching without cancelling the backend operation', async () => {
  const requests = [];
  let nextPoll;
  const context = vm.createContext({
    justVoxelSelectionGeneration: {version: 0}, URLSearchParams, updateOperation:'', completedOperation:null,
    state:{}, content:{querySelector:()=>null},
    request:async url => {
      requests.push(url);
      return {operation:{operation_id:'old-operation',state:'running'}};
    },
    setTimeout:callback=>{nextPoll=callback;},
  });
  const start = source.indexOf('  const watchUpdate =');
  const end = source.indexOf('  // Let the native select', start);
  vm.runInContext(source.slice(start,end) + '\nglobalThis.watch = watchUpdate;', context);
  await context.watch('old-operation');
  assert.equal(requests.length,1);
  context.justVoxelSelectionGeneration.version++;
  context.state.textContent = 'New server';
  await nextPoll();
  assert.equal(requests.length,1, 'old watcher must not request the operation through the new selection');
  assert.equal(context.state.textContent,'New server');
  assert.ok(requests.every(url=>url.startsWith('/api/version/workspace/update-operation?')));
});

 test('Switch exposes only reviewed Paper-family changes without image controls', async () => {
  const page = await softwareReview({}, { online: 3 });
  assert.deepEqual(page.cards.map(node => node.children[1].children[0].textContent), ['Paper', 'Purpur', 'Pufferfish', 'Leaf']);
  assert.equal(page.form.all().some(node => node.tag === 'select'), false);
  assert.doesNotMatch(softwareSource, /Vanilla|vanilla|Check for updates|custom_tag|name="channel"|image_tag:|Container channel/);
  assert.equal(page.consent.checked, false); assert.equal(page.apply.disabled, true);
  assert.deepEqual(page.apply.parent.children, [page.cancel, page.apply]);
  page.consent.checked = true; page.consent.listeners.change(); await page.apply.listeners.click();
  const posts = page.calls.filter(call => call.body);
  assert.equal(posts.length, 1);
  assert.equal(posts[0].body.get('server_type'), 'purpur');
  assert.equal(posts[0].body.has('image_tag'), false);
  assert.equal(posts[0].body.get('confirm_players'), 'yes');
 });
 test('Switch unsafe plans cannot apply and Cancel restores installed software', async () => {
  for (const overrides of [{stack_state:'unavailable'}, {server_supported:false}, {crossplay_enabled:true,crossplay_compatible:false}, {plan_fingerprint:''}]) {
    const page = await softwareReview(overrides);
    assert.equal(page.apply.disabled,true); assert.equal(page.consent,undefined);
    await page.apply.listeners.click(); assert.equal(page.calls.filter(call=>call.body).length,0);
    page.cancel.listeners.click(); assertSoftwareSelection(page,'paper','paper');
    assert.equal(page.reviews().length,0);
  }
 });
 test('Switch rejects stale previews and reconnects a lost Apply response without retry', async () => {
  const plans=[];
  const page=softwareWorkspace({planning:plan=>new Promise(resolve=>plans.push(()=>resolve(plan)))});
  page.click('purpur'); const old=page.review();
  page.click('paper'); page.click('purpur'); const fresh=page.review();
  plans[1](); await fresh; const reviewed=page.reviews()[0];
  plans[0](); await old; assert.deepEqual(page.reviews(),[reviewed]);
  const failed=await softwareReview({}, {failApply:true});
  failed.consent.checked=true; failed.consent.listeners.change();
  await failed.apply.listeners.click(); await failed.apply.listeners.click();
  assert.equal(failed.calls.filter(call=>call.body).length,1);
  assert.equal(failed.calls.at(-1).message,'Response lost');
 });
 test('Version tabs are exactly Container, Minecraft Version, Switch in order', () => {
  const html=readFileSync(path.join(__dirname,'../webui/internal/server/templates/header.html'),'utf8');
  assert.deepEqual([...html.matchAll(/data-version-tab="[^"]+"[^>]*>([^<]+)<\/button>/g)].map(match=>match[1]),['Container','Minecraft Version','Switch']);
  assert.doesNotMatch(html,/>Server Software<\/button>/);
 });

class ContainerElement extends Element {
  set textContent(value) { this.ownText = String(value); this.children = []; }
  get textContent() { return (this.ownText || '') + this.children.map(child => child.textContent).join(' '); }
  set innerHTML(_) { throw new Error('Container output must use textContent'); }
}
function containerWorkspace({status = 'up_to_date', running = true, pending, statusReply, traceReply, selected = 'jv-family1', previousImages = [], retentionCount = 1, retentionReply} = {}) {
  const calls = [], timers = [], renders = [];
  const content = new ContainerElement('div');
  const context = vm.createContext({
    URLSearchParams, encodeURIComponent, Number, Boolean, String,
    justVoxelSelectionGeneration: {version: 0}, currentTab: 'container', sequence: 1,
    csrf: 'csrf', content, state: {}, operationKinds: new Map(),
    versionDialog: {querySelector: () => ({selectedOptions:[{textContent:'22222'}]})},
    document: {createElement: tag => new ContainerElement(tag)},
    card: title => {const panel=new ContainerElement('section');panel.append(Object.assign(new ContainerElement('h2'),{textContent:title}));return panel;},
    line: (label,value) => {const row=new ContainerElement('div');row.append(Object.assign(new ContainerElement('span'),{textContent:label}),Object.assign(new ContainerElement('strong'),{textContent:value||'Unavailable'}));return row;},
    operationFrame: operation => Object.assign(new ContainerElement('section'),{textContent:operation.status||operation.state}),
    window: {confirm: () => true},
    request: async (url,options) => {
      calls.push({url,body:options?.body});
      if(url==='/api/version/container/status') return statusReply ? statusReply() : {instance_id:selected,server_name:'22222',image:'docker.io/itzg/minecraft-server:stable',state:status,running,plan_fingerprint:status==='unavailable'?'':'reviewed',restore_fingerprint:'local-reviewed',keep_previous_images:retentionCount,previous_images:previousImages};
      if(url.startsWith('/api/version/container/operation')) return {operation:pending};
      if(url==='/api/minecraft/workspace/snapshot') return {players:{online:0}};
      if(url==='/api/version/container/retention') return retentionReply ? retentionReply() : {ok:true};
      if(url==='/api/version/container/acknowledge') return {operation:{state:'resolved'}};
      if(url==='/api/version/container/update') return {operation:{operation_id:'container-1',state:'running'}};
      throw new Error(url);
    },
    fetch: async url => {calls.push({trace:url});return {ok:true,text:async()=>traceReply?traceReply():'Copying blob one\nCopying config one\nWriting manifest one'};},
    setTimeout: (fn,delay) => {assert.equal(delay,2000);timers.push(fn);},
    render: async () => {renders.push('refresh');},
  });
  const start=source.indexOf('  let containerCompleted =');
  const end=source.indexOf('  const render = async',start);
  vm.runInContext(source.slice(start,end)+'\nglobalThis.mount = renderContainer;',context);
  return {context,content,calls,timers,renders,mount:()=>context.mount(context.sequence,context.justVoxelSelectionGeneration.version,''),
    toggles:()=>content.all().filter(node=>node.tag==='input'&&node.type==='checkbox'),
    update:()=>content.all().find(node=>node.tag==='button'&&node.textContent==='Update now'),
    terminal:()=>content.all().find(node=>'containerTerminal' in node.dataset)};
}
for(const [status,label,disabled] of [['up_to_date','Up to date',true],['update_available','Update available',false],['image_missing','Download required',false],['unavailable','Unable to check',true]]) {
  test('Container '+status+' uses approved rows and bounded actions',async()=>{
    const page=containerWorkspace({status});await page.mount();
    const rows=page.content.all().filter(node=>'containerRow' in node.dataset);
    assert.deepEqual(rows.map(row=>row.dataset.containerRow),['identity','controls','actions']);
    assert.match(rows[0].textContent,/Server: 22222.*Image: docker.io\/itzg\/minecraft-server:stable/);
    assert.match(rows[2].textContent,new RegExp('Status: '+label));
    assert.equal(page.update().disabled,disabled);
    assert.ok(page.toggles().every(input=>input.disabled===disabled&&!input.checked));
    assert.equal(page.timers.length,0,'idle availability must not poll');
    assert.ok(page.calls.every(call=>!call.url?.includes('/workspace/status')));
  });
}
test('Container backup and restart coupling, stopped state, and restart-only action',async()=>{
  const page=containerWorkspace({status:'update_available'});await page.mount();
  const [restart,backup]=page.toggles();
  backup.checked=true;backup.listeners.change();assert.equal(restart.checked,true);
  restart.checked=false;restart.listeners.change();assert.equal(backup.checked,false);
  const stopped=containerWorkspace({status:'image_missing',running:false});await stopped.mount();
  assert.ok(stopped.toggles().every(input=>input.disabled&&!input.checked));
  assert.equal(stopped.update().disabled,false);
  assert.match(stopped.content.textContent,/next server start/);
  const deferred=containerWorkspace({status:'restart_needed'});await deferred.mount();
  assert.equal(deferred.update().disabled,true);
  deferred.toggles()[0].checked=true;deferred.toggles()[0].listeners.change();
  assert.equal(deferred.update().disabled,false);
});
test('Container polling replaces real trace text and refreshes on completion',async()=>{
  let trace='Copying blob first';
  const op={operation_id:'container-1',state:'running'};
  const page=containerWorkspace({pending:op,traceReply:()=>trace});await page.mount();
  await new Promise(resolve=>setImmediate(resolve));
  assert.equal(page.terminal().textContent,trace);
  assert.ok(page.calls.some(call=>call.trace==='/api/operations/container-1/trace'));
  trace='Copying blob first\nCopying config final\nWriting manifest final';
  op.state='succeeded';await page.timers.shift()();
  assert.equal(page.terminal().textContent,trace);
  assert.equal(page.renders.length,1);
});
test('Container stale status and stale trace cannot replace a new selection',async()=>{
  let resolveStatus;
  const page=containerWorkspace({statusReply:()=>new Promise(resolve=>{resolveStatus=resolve;})});
  const old=page.mount();
  page.context.justVoxelSelectionGeneration.version++;page.context.sequence++;
  const replacement=new ContainerElement('div');replacement.textContent='Server B';page.content.replaceChildren(replacement);
  resolveStatus({state:'update_available',server_name:'Server A',image:'old-image'});await old;
  assert.equal(page.content.textContent,'Server B');
  let resolveTrace;
  const active=containerWorkspace({pending:{operation_id:'old-op',state:'running'},traceReply:()=>new Promise(resolve=>{resolveTrace=resolve;})});
  const oldTrace=active.mount();await new Promise(resolve=>setImmediate(resolve));
  active.context.justVoxelSelectionGeneration.version++;active.context.sequence++;
  active.content.replaceChildren(Object.assign(new ContainerElement('div'),{textContent:'New server terminal'}));
  resolveTrace('Old server trace');await oldTrace;
  assert.equal(active.content.textContent,'New server terminal');assert.equal(active.timers.length,0);
});

const retainedImage = digest => ({digest,image:'docker.io/itzg/minecraft-server:stable',retained_at:'2026-10-27T12:00:00Z',size:1048576});
const retentionInput = page => page.content.all().find(node=>node.type==='number');
const changeRetention = (page,value) => {const input=retentionInput(page);input.value=value;input.listeners.input();};
test('Container combines controls and previous images in one outer card with separate untitled output',async()=>{
 const page=containerWorkspace();await page.mount();
 const root=page.content.children[0];
 const panel=root.children[0];
 const history=panel.children.find(node=>node.className==='version-container-history backup-library-section');
 const controls=panel.children.find(node=>node.dataset.containerRow==='controls');
 assert.equal(panel.children[0].textContent,'Minecraft container');
 assert.equal(root.children.filter(node=>node.tag==='section').length,1);
 assert.ok(history);assert.equal(history.children[0].tag,'h2');
 assert.equal(history.children[0].textContent,'Previous container images');
 assert.ok(!history.className.includes('panel'));
 assert.ok(!history.all().includes(retentionInput(page)));
 assert.ok(!page.content.all().some(node=>node.tag==='button'&&node.textContent==='Save'));
 assert.deepEqual(controls.children.map(node=>node.children[0].textContent),['Keep previous images','Restart server now','Create backup before restart']);
 assert.equal(retentionInput(page).parent,controls.children[0]);
 assert.equal(page.terminal().parent,root);assert.equal(root.children.at(-1),page.terminal());
 assert.ok(!panel.all().includes(page.terminal()));
 assert.deepEqual(page.content.all().filter(node=>node.tag==='h2').map(node=>node.textContent),['Minecraft container','Previous container images']);
 assert.doesNotMatch(page.content.textContent,/Console|Installed|Recommended|Geyser|Floodgate|ViaVersion|Minecraft update/);
 assert.match(history.textContent,/No previous container images\./);
 const restore=history.all().find(node=>node.tag==='button'&&node.textContent==='Restore selected');
 assert.equal(restore.parent.className,'backup-library-summary');
 assert.equal(restore.parent.parent,history);assert.equal(history.children.at(-1),restore.parent);
 assert.match(cssRule('.version-container-retention input'),/width:3\.25rem;flex:none/);
 assert.match(cssRule('.version-container-panel .version-container-controls'),/grid-template-columns:repeat\(3,minmax\(0,1fr\)\)/);
 assert.match(cssRule('.version-container-panel .version-container-controls'),/background:var\(--jv-surface-sunken\)/);
 assert.match(css,/@media\(max-width:700px\)\{\.version-container-panel \.version-container-controls\{grid-template-columns:minmax\(0,1fr\)\}/);
 assert.match(cssRule('.version-container-history .backup-library-summary'),/justify-content:space-between/);
});
test('Container retention is limited to one or two and missing defaults to one',async()=>{
 const page=containerWorkspace({statusReply:()=>({state:'up_to_date'})});await page.mount();
 const number=retentionInput(page);
 assert.equal(number.value,'1');assert.equal(number.min,'1');assert.equal(number.max,'2');assert.equal(number.step,'1');
 assert.ok(!page.content.all().some(node=>node.tag==='select'));
 for(const valid of ['1','2']){
  const validPage=containerWorkspace({retentionCount:valid==='1'?2:1});await validPage.mount();
  changeRetention(validPage,valid);assert.equal(validPage.update().disabled,false);
  await validPage.update().listeners.click();
  const sent=validPage.calls.find(call=>call.url==='/api/version/container/retention');
  assert.equal(sent.body.get('keep_previous_images'),valid);assert.equal(sent.body.get('csrf'),'csrf');
 }
 for(const invalid of ['','0','3','12345','-1','1.5','one','Infinity','NaN']){
  const invalidPage=containerWorkspace({status:'update_available'});await invalidPage.mount();
  changeRetention(invalidPage,invalid);
  assert.equal(invalidPage.update().disabled,true);
  await invalidPage.update().listeners.click();
  assert.ok(!invalidPage.calls.some(call=>call.body));
  assert.equal(invalidPage.context.state.textContent,'Keep previous images must be 1 or 2.');
 }
});
test('Container retention dirty state enables the single apply action and resets when reverted',async()=>{
 const page=containerWorkspace();await page.mount();
 assert.equal(page.update().disabled,true);
 changeRetention(page,'2');assert.equal(page.update().disabled,false);
 changeRetention(page,'1');assert.equal(page.update().disabled,true);
 await page.update().listeners.click();assert.ok(!page.calls.some(call=>call.body));
});
test('Container retention-only apply saves and refreshes without update or restart',async()=>{
 for(const status of ['up_to_date','restart_needed','unavailable']){
  const page=containerWorkspace({status});await page.mount();
  changeRetention(page,'2');await page.update().listeners.click();
  assert.deepEqual(page.calls.filter(call=>call.body).map(call=>call.url),['/api/version/container/retention']);
  assert.ok(!page.calls.some(call=>call.url==='/api/minecraft/workspace/snapshot'));
  assert.equal(page.renders.length,1);
 }
});
test('Container update-only preserves fingerprint and restart and backup choices',async()=>{
 const page=containerWorkspace({status:'update_available'});await page.mount();
 const [restart,backup]=page.toggles();backup.checked=true;backup.listeners.change();
 assert.equal(restart.checked,true);
 await page.update().listeners.click();
 const posts=page.calls.filter(call=>call.body);assert.equal(posts.length,1);
 assert.equal(posts[0].url,'/api/version/container/update');
 assert.equal(posts[0].body.get('plan_fingerprint'),'reviewed');
 assert.equal(posts[0].body.get('restart_now'),'yes');assert.equal(posts[0].body.get('backup_before_restart'),'yes');
});
test('Container combined apply persists retention before the existing update',async()=>{
 const page=containerWorkspace({status:'update_available'});await page.mount();
 changeRetention(page,'2');await page.update().listeners.click();
 assert.deepEqual(page.calls.filter(call=>call.body).map(call=>call.url),['/api/version/container/retention','/api/version/container/update']);
 const body=page.calls.find(call=>call.url==='/api/version/container/update').body;
 assert.equal(body.get('plan_fingerprint'),'reviewed');assert.equal(body.get('restart_now'),'no');
});
test('Container failed retention save stops combined apply and shows the bounded error',async()=>{
 const page=containerWorkspace({status:'update_available',retentionReply:()=>{throw new Error('Retention save failed.');}});await page.mount();
 changeRetention(page,'2');await page.update().listeners.click();
 assert.deepEqual(page.calls.filter(call=>call.body).map(call=>call.url),['/api/version/container/retention']);
 assert.equal(page.context.state.textContent,'Retention save failed.');assert.equal(page.update().disabled,false);
 assert.equal(page.renders.length,0);
});
test('Container selection change during retention save prevents the following update',async()=>{
 let finish;
 const page=containerWorkspace({status:'update_available',retentionReply:()=>new Promise(resolve=>{finish=resolve;})});await page.mount();
 changeRetention(page,'2');const saving=page.update().listeners.click();
 assert.equal(page.update().disabled,true);
 page.context.justVoxelSelectionGeneration.version++;finish({ok:true});await saving;
 assert.ok(!page.calls.some(call=>call.url==='/api/version/container/update'));assert.equal(page.renders.length,0);
});
test('Container lists real previous images and exact selection enables restore',async()=>{
 const digest='sha256:'+'a'.repeat(64);
 for(const running of [true,false]){
  const page=containerWorkspace({running,previousImages:[retainedImage(digest)]});await page.mount();
  const restore=page.content.all().find(node=>node.tag==='button'&&node.textContent==='Restore selected');
  assert.equal(restore.disabled,true);
  const radios=page.content.all().filter(node=>node.type==='radio');assert.equal(radios.length,1);
  assert.ok(page.content.textContent.includes('sha256:aaaaaaaaaaaa…'));
  assert.ok(!page.content.textContent.includes(digest));
  radios[0].checked=true;radios[0].listeners.change();assert.equal(restore.disabled,false);
  await restore.listeners.click();
  const sent=page.calls.find(call=>call.url==='/api/version/container/update');
  assert.equal(sent.body.get('restore_digest'),digest);assert.equal(sent.body.get('plan_fingerprint'),'local-reviewed');
  assert.equal(sent.body.get('restart_now'),running?'yes':'no');
  assert.equal(page.calls.some(call=>call.url==='/api/minecraft/workspace/snapshot'),running);
 }
 const empty=containerWorkspace();await empty.mount();
 assert.equal(empty.content.all().filter(node=>node.type==='radio').length,0);
 assert.match(empty.content.textContent,/0 images/);
 assert.equal(empty.content.all().find(node=>node.textContent==='Restore selected').disabled,true);
});
test('Container keep-current action shares the Update-now lane',async()=>{
 const page=containerWorkspace({status:'update_available',pending:{operation_id:'failed-update',state:'needs_attention'}});await page.mount();
 const keep=page.content.all().find(node=>node.tag==='button'&&node.textContent==='Keep current version');
 assert.ok(keep);assert.equal(keep.parent,page.update().parent);
 assert.deepEqual(keep.parent.children.map(node=>node.textContent),['Keep current version','Update now']);
 assert.ok(!page.content.textContent.includes('Verify and keep current server'));
 await keep.listeners.click();
 assert.ok(page.calls.some(call=>call.url==='/api/version/container/acknowledge'));
});
test('Container output grows inside a responsive workspace',()=>{
 assert.match(cssRule('.version-container-workspace'),/display:flex;flex:1;flex-direction:column;min-height:0/);
 assert.match(cssRule('.version-container-workspace>.log-box'),/flex:1;min-height:140px;height:auto;max-height:none;overflow:auto/);
 assert.match(cssRule('.version-workspace-body:has(.version-container-workspace) [data-version-content]'),/flex:1;min-height:0/);
 assert.doesNotMatch(cssRule('.version-container-workspace>.log-box'),/resize:/);
});
test('Version Container rules precede the Minecraft Network-only CSS region',()=>{
 const marker='/* Compact Minecraft Network presentation; shared Players rules stay unchanged. */';
 const networkStart=css.indexOf(marker);
 assert.ok(networkStart>=0);
 for(const selector of [
  '.version-workspace-body:has(.version-container-workspace)',
  '.version-workspace-body:has(.version-container-workspace) [data-version-content]',
  '.version-container-workspace',
  '.version-container-workspace>.log-box',
  '.version-container-workspace>.panel,.version-container-workspace>[data-version-operation]',
  '.version-container-action-lane',
  '.version-container-retention',
  '.version-container-retention input',
  '.version-container-history .backup-file-grid',
  '.version-container-history .backup-file-card>div',
  '.version-container-history .backup-library-summary',
  '.version-container-workspace .version-container-history .backup-file-card',
 ]) {
  const start=css.indexOf(selector+'{');
  assert.ok(start>=0&&start<networkStart,selector);
 }
 assert.doesNotMatch(css.slice(networkStart),/\.version-/);
});
test('Old container restore selection and retention callbacks cannot submit for a new server',async()=>{
 const page=containerWorkspace({previousImages:[retainedImage('sha256:'+'a'.repeat(64))]});await page.mount();
 const radio=page.content.all().find(node=>node.type==='radio');
 radio.checked=true;radio.listeners.change();
 const restore=page.content.all().find(node=>node.tag==='button'&&node.textContent==='Restore selected');
 changeRetention(page,'2');
 const apply=page.update();
 page.context.justVoxelSelectionGeneration.version++;
 await restore.listeners.click();await apply.listeners.click();radio.listeners.change();
 assert.ok(!page.calls.some(call=>call.url==='/api/version/container/update'||call.url==='/api/version/container/retention'));
});

 test('all four Paper-compatible types can select each other and Spigot stays blocked', async () => {
  const family = ['paper', 'purpur', 'pufferfish', 'leaf'];
  for (const current of family) {
    for (const target of family.filter(value => value !== current)) {
      const page = softwareWorkspace({current});
      page.click(target); await page.review();
      const apply = page.find('Apply changes');
      const consent = page.consent();
      assert.ok(consent, `${current} -> ${target} can be reviewed`);
      consent.checked = true; consent.listeners.change();
      await apply.listeners.click();
      assert.equal(page.calls.find(call => call.body).body.get('server_type'), target);
    }
  }
  for (const current of ['spigot', 'vanilla', 'fabric', 'forge', 'neoforge', 'quilt', 'bedrock']) {
    const page = softwareWorkspace({current});
    assert.ok(page.cards.every(node => node.disabled));
  }
 });
