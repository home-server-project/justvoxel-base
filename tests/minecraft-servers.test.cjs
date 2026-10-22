const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { test } = require('node:test');
const vm = require('node:vm');
const root = `${__dirname}/../webui/internal/server/`;
const source = readFileSync(`${root}static/minecraft-servers.js`, 'utf8');
const key = 'justvoxel:minecraft-server-order:v1';
const ids = ['jv-family1', 'jv-kids001', 'jv-new0001'];

// Small DOM fixture for the page script's native buttons, visibility and moves.
class Element {
  constructor(selector, dataset = {}) {
    this.selector = selector;
    this.dataset = dataset;
    this.children = [];
    this.listeners = {};
    this.hidden = false;
    this.disabled = false;
    this.inert = false;
  }
  appendChild(child) {
    if (child.parent) child.parent.children.splice(child.parent.children.indexOf(child), 1);
    child.parent = this;
    this.children.push(child);
    return child;
  }
  insertBefore(child, reference) {
    child.parent.children.splice(child.parent.children.indexOf(child), 1);
    this.children.splice(this.children.indexOf(reference), 0, child);
    child.parent = this;
  }
  querySelectorAll(selector) {
    return this.children.flatMap(child => [
      ...(child.selector === selector ? [child] : []), ...child.querySelectorAll(selector),
    ]);
  }
  querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
  addEventListener(type, handler, capture = false) {
    (this.listeners[type] ||= []).push({ handler, capture });
  }
  fire(type) {
    const event = {
      prevented: false, stopped: false,
      preventDefault() { this.prevented = true; },
      stopImmediatePropagation() { this.stopped = true; },
    };
    const ancestors = [];
    for (let node = this; node; node = node.parent) ancestors.unshift(node);
    for (const node of ancestors) {
      for (const { handler, capture } of node.listeners[type] || []) {
        if (capture && !event.stopped) handler(event);
      }
    }
    if (!event.stopped && !this.disabled) {
      for (const { handler, capture } of this.listeners[type] || []) {
        if (!capture && !event.stopped) handler(event);
      }
    }
    return event;
  }
  focus() { Element.focused = this; }
}

function page({ saved = null, readFails = false, accessFails = false, writeFails = false, inventory = ids } = {}) {
  const document = new Element('document');
  const list = document.appendChild(new Element('.minecraft-server-entries'));
  const arrange = document.appendChild(new Element('[data-server-arrange]'));
  const done = document.appendChild(new Element('[data-server-arrange-done]'));
  arrange.hidden = done.hidden = true;
  const cards = inventory.map(id => {
    const card = list.appendChild(new Element('[data-minecraft-server-card]', { serverId: id }));
    // Deliberately identical display names: identity must come from data-server-id.
    card.name = 'Same friendly name';
    const normal = card.appendChild(new Element('[data-server-normal-actions]'));
    const details = normal.appendChild(new Element('details'));
    details.open = true;
    const link = normal.appendChild(new Element('a'));
    link.href = `/?workspace=minecraft&server=${id}`;
    const form = normal.appendChild(new Element('form'));
    form.action = `/minecraft/servers/view/${id}/stop`;
    form.method = 'post';
    form.csrf = 'csrf-token';
    const moves = card.appendChild(new Element('[data-server-arrange-actions]'));
    moves.hidden = true;
    moves.appendChild(new Element('[data-server-move-earlier]'));
    moves.appendChild(new Element('[data-server-move-later]'));
    return card;
  });
  const writes = [];
  const localStorage = {
    getItem(requested) {
      assert.equal(requested, key);
      if (readFails) throw new Error('read denied');
      return saved;
    },
    setItem(requested, value) {
      assert.equal(requested, key);
      if (writeFails) throw new Error('write denied');
      writes.push(value);
    },
  };
  const context = { document, window: { get localStorage() {
    if (accessFails) throw new Error('storage access denied');
    return localStorage;
  } } };
  vm.runInNewContext(source, context);
  return {
    initialize: () => context.justVoxelMinecraftServers(document),
    list, arrange, done, cards, writes,
    order: () => list.querySelectorAll('[data-minecraft-server-card]').map(card => card.dataset.serverId),
  };
}
const earlier = card => card.querySelector('[data-server-move-earlier]');
const later = card => card.querySelector('[data-server-move-later]');

test('saved order reconciles only actual unique immutable identities and preserves unlisted cards', () => {
  for (const [stored, expected] of [
    [[ids[1], ids[0]], [ids[1], ids[0], ids[2]]],
    [[ids[1]], [ids[1], ids[0], ids[2]]],
    [[ids[1], 'jv-deleted1', ids[1], null, 42, {}, 'Family', 'jv-UPPER01', 'jv-short', 'jv-1234567890123', ids[0]], [ids[1], ids[0], ids[2]]],
    [[], ids],
    [['jv-deleted1'], ids],
  ]) {
    const p = page({ saved: JSON.stringify(stored) });
    assert.deepEqual(p.order(), expected);
    assert.equal(p.writes.length, 0);
    assert.equal(new Set(p.order()).size, ids.length);
  }
});

test('invalid JSON, nonarrays and unavailable storage retain fallback order', () => {
  for (const saved of ['{broken', '{}', 'null', '42', '"jv-kids001"']) {
    assert.deepEqual(page({ saved }).order(), ids);
  }
  assert.deepEqual(page({ readFails: true }).order(), ids);
  assert.deepEqual(page({ accessFails: true }).order(), ids);
});

test('Arrange moves real cards only, respects boundaries and persists ID arrays only', () => {
  const p = page();
  assert.equal(p.arrange.hidden, false);
  assert.equal(p.done.hidden, true);
  assert.equal(earlier(p.cards[0]).disabled, true);
  assert.equal(later(p.cards[2]).disabled, true);
  const actions = p.cards.map(card => {
    const form = card.querySelector('form');
    return [card.dataset.serverId, card.name, form.action, form.method, form.csrf, card.querySelector('a').href];
  });
  // Hidden move buttons cannot act before Arrange is entered.
  later(p.cards[0]).fire('click');
  assert.deepEqual(p.order(), ids);
  p.arrange.fire('click');
  assert.equal(p.arrange.hidden, true);
  assert.equal(p.done.hidden, false);
  assert.equal(Element.focused, p.done);
  earlier(p.cards[0]).fire('click');
  assert.deepEqual(p.order(), ids);
  later(p.cards[0]).fire('click');
  assert.deepEqual(p.order(), [ids[1], ids[0], ids[2]]);
  earlier(p.cards[0]).fire('click');
  assert.deepEqual(p.order(), ids);
  earlier(p.cards[2]).fire('click');
  assert.deepEqual(p.order(), [ids[0], ids[2], ids[1]]);
  assert.equal(later(p.cards[1]).disabled, true);
  assert.equal(p.writes.length, 3);
  for (const saved of p.writes) {
    const value = JSON.parse(saved);
    assert.equal(value.length, ids.length);
    for (const id of value) assert.match(id, /^jv-[a-z0-9]{6,12}$/);
    assert.ok(!value.includes(undefined));
  }
  assert.deepEqual(JSON.parse(p.writes.at(-1)), p.order());
  assert.deepEqual(p.cards.map(card => {
    const form = card.querySelector('form');
    return [card.dataset.serverId, card.name, form.action, form.method, form.csrf, card.querySelector('a').href];
  }), actions);
  assert.deepEqual(page({ saved: p.writes.at(-1) }).order(), p.order());
});

test('Arrange suppresses normal links and lifecycle submission; Done restores them', () => {
  const p = page();
  p.arrange.fire('click');
  for (const card of p.cards) {
    const normal = card.querySelector('[data-server-normal-actions]');
    assert.equal(normal.hidden, true);
    assert.equal(normal.inert, true);
    assert.equal(card.querySelector('details').open, false);
    assert.equal(card.querySelector('[data-server-arrange-actions]').hidden, false);
    for (const [target, type] of [[card.querySelector('a'), 'click'], [card.querySelector('form'), 'submit']]) {
      const event = target.fire(type);
      assert.equal(event.prevented, true);
      assert.equal(event.stopped, true);
    }
  }
  p.done.fire('click');
  assert.equal(Element.focused, p.arrange);
  assert.equal(p.done.hidden, true);
  assert.equal(p.arrange.hidden, false);
  for (const card of p.cards) {
    const normal = card.querySelector('[data-server-normal-actions]');
    assert.equal(normal.hidden, false);
    assert.equal(normal.inert, false);
    assert.equal(card.querySelector('[data-server-arrange-actions]').hidden, true);
    assert.equal(card.querySelector('a').fire('click').prevented, false);
    assert.equal(card.querySelector('form').fire('submit').prevented, false);
  }
});

test('write failure retains usable DOM ordering and controls', () => {
  const p = page({ writeFails: true });
  p.arrange.fire('click');
  assert.doesNotThrow(() => later(p.cards[0]).fire('click'));
  assert.deepEqual(p.order(), [ids[1], ids[0], ids[2]]);
  p.done.fire('click');
  assert.equal(p.cards[0].querySelector('a').fire('click').prevented, false);
});

test('empty and single inventories order only real servers', () => {
  const empty = page({ inventory: [] });
  assert.equal(empty.arrange.hidden, true);
  assert.deepEqual(empty.list.children, []);
  const single = page({ inventory: [ids[0]] });
  single.arrange.fire('click');
  assert.equal(earlier(single.cards[0]).disabled, true);
  assert.equal(later(single.cards[0]).disabled, true);
  assert.equal(single.list.children[0], single.cards[0]);
});

test('script scopes every backend request to an immutable card and preserves local ordering', () => {
  const template = readFileSync(`${root}templates/minecraft_servers.html`, 'utf8');
  const header = readFileSync(`${root}templates/header.html`, 'utf8');
  assert.equal((header.match(/src="\/static\/minecraft-servers.js"/g) || []).length, 1);
  assert.ok(!template.includes('minecraft-add-server-card'));
  assert.ok(!source.includes('innerHTML'));
  assert.ok(source.includes('new URLSearchParams({server: id})'));
  assert.match(source, /JSON\.stringify\(cards\(\)\.map\(card => card\.dataset\.serverId\)/);
  for (const forbidden of ['podman', 'cgroup', 'SIGKILL', 'force stop', 'dragstart']) assert.ok(!source.includes(forbidden));
});

test('dynamic inventory initialization is idempotent', () => {
  const p = page();
  p.initialize(); p.initialize();
  assert.equal(p.arrange.listeners.click.length, 1);
  p.arrange.fire('click');
  later(p.cards[0]).fire('click');
  assert.deepEqual(p.order(), [ids[1], ids[0], ids[2]]);
  assert.equal(p.writes.length, 1);
});

// Exercise the production card coordination without starting the whole dashboard.
function cardHarness({responses = [], bootChecked = false, runningChecked = false, connected = true, helperAvailable = true, launcherAvailable = true} = {}) {
  const card = new Element('card', {serverId: ids[1], serverName: 'Kids Creative'});
  card.isConnected = connected;
  const running = card.appendChild(new Element('[data-server-running]')); running.checked = runningChecked;
  const boot = card.appendChild(new Element('[data-server-boot]')); boot.checked = bootChecked;
  const restart = card.appendChild(new Element('[data-server-restart]'));
  const remove = card.appendChild(new Element('[data-server-delete]'));
  const invite = card.appendChild(new Element('[data-server-invite]'));
  const queryAll = card.querySelectorAll.bind(card);
  card.querySelectorAll = selector => selector === 'input, button' ? [running, boot, restart, remove, invite] : queryAll(selector);
  const menu = card.appendChild(new Element('details')); menu.open = true;
  menu.appendChild(new Element('summary'));
  const choices = menu.appendChild(new Element('[popover]'));
  choices.matches = () => menu.open;
  choices.hidePopover = () => { menu.open = false; };
  const links = ['minecraft', 'backups', 'version', 'plugins'].map(workspace => {
    const link = menu.appendChild(new Element('a[data-server-workspace]', {serverWorkspace: workspace}));
    link.href = `/?workspace=${workspace}&server=${ids[1]}`;
    return link;
  });
  const guide = menu.appendChild(new Element('a'));
  guide.href = `/minecraft/how-to-connect?server=${ids[1]}`;
  const feedback = new Element('feedback');
  const calls = [], deletes = [], dialogs = [], selections = [], launches = [], copied = [];
  const dashboard = {dataset: {selectedMinecraftId: ids[0]}};
  const dropdowns = [{value: ids[0]}, {value: ids[0]}];
  const selectionEvents = [];
  const location = {href: `https://justvoxel.test/?workspace=minecraft-servers&server=${ids[0]}&operation=stale`, origin: 'https://justvoxel.test', search: `?workspace=minecraft-servers&server=${ids[0]}&operation=stale`};
  let refreshes = 0;
  const inventoryRoot = {
    querySelector: selector => selector === '[data-server-csrf]' ? {dataset: {serverCsrf: 'csrf-token'}} : feedback,
    justVoxelRefreshServers: async () => { refreshes++; },
  };
  const create = tag => {
    const node = new Element(tag);
    node.setAttribute = () => {};
    node.append = (...nodes) => nodes.forEach(child => node.appendChild(child));
    node.showModal = () => { node.open = true; dialogs.push(node); };
    node.close = () => { node.open = false; node.fire('close'); };
    node.pressEscape = () => { if (!node.fire('cancel').prevented) node.close(); };
    node.remove = () => { node.removed = true; };
    node.getBoundingClientRect = () => ({left: 100, right: 500, top: 100, bottom: 500});
    return node;
  };
  const context = vm.createContext({URLSearchParams,
    location,
    URL,
    CustomEvent: class { constructor(type, options) { this.type = type; this.detail = options.detail; } },
    window: {location, history: {state: {}, replaceState(_, __, target) { location.href = target.href; location.search = target.search; }}, dispatchEvent: event => selectionEvents.push(event)},
    navigator: {clipboard: {writeText: async text => { copied.push(text); }}},
    document: {createElement: create, body: create('body'),
      querySelectorAll: () => dropdowns,
      querySelector: selector => selector === '#dashboard[data-dashboard-status]' ? dashboard :
        launcherAvailable ? {click() { assert.equal(menu.open, false); launches.push(selector); }} : null},
    justVoxelOpenDeleteServer: options => deletes.push(options),
    fetch: async (url, options) => {
      calls.push({url, body: options.body});
      const result = responses.shift() ?? {ok: true};
      if (result instanceof Error) throw result;
      return {ok: !result.error, redirected: false, json: async () => result};
    },
  });
  const app = readFileSync(`${root}static/app.js`, 'utf8');
  const sharedStart = app.indexOf('globalThis.justVoxelSelectServer =');
  const sharedEnd = app.indexOf('globalThis.justVoxelServerSelection =', sharedStart);
  vm.runInContext('globalThis.justVoxelSelectionGeneration = 0;\n' + app.slice(sharedStart, sharedEnd), context);
  const selectServer = context.justVoxelSelectServer;
  context.justVoxelSelectServer = helperAvailable ? (...args) => {
    assert.equal(menu.open, false);
    selections.push(args);
    return selectServer(...args);
  } : undefined;
  const start = source.indexOf('  function initializeCard(');
  const end = source.indexOf("  window.addEventListener?.('resize'");
  vm.runInContext(source.slice(start, end) + '\nglobalThis.initialize = initializeCard;', context);
  let arranging = false;
  context.initialize(card, inventoryRoot, () => arranging);
  const fire = (control, type, fields = {}) => {
    const event = {button: 0, target: control, defaultPrevented: false,
      preventDefault() { this.defaultPrevented = true; }, ...fields};
    for (const {handler} of control.listeners[type] || []) handler(event);
    return event;
  };
  return {card, running, boot, restart, remove, invite, feedback, calls, deletes, dialogs, menu, links, guide, selections, launches, copied, context, location, dashboard, dropdowns, selectionEvents,
    fire, arrange: () => { arranging = true; }, refreshes: () => refreshes,
    // operation() runs asynchronously from the native change/click handler.
    settle: async () => { for (let i = 0; i < 25; i++) await Promise.resolve(); },
  };
}

test('card running switch reverts until authoritative refresh, posts only start/stop and prevents duplicates', async () => {
  for (const current of [false, true]) {
    const f = cardHarness({runningChecked: current});
    f.running.checked = !current; f.fire(f.running, 'change');
    assert.equal(f.running.checked, current);
    assert.equal(f.running.disabled, true);
    f.fire(f.restart, 'click');
    await f.settle();
    assert.equal(f.calls.length, 1);
    assert.equal(f.calls[0].url, '/api/minecraft/servers/lifecycle?server=jv-kids001');
    assert.equal(f.calls[0].body.get('action'), current ? 'stop' : 'start');
    assert.equal(f.calls[0].body.get('csrf'), 'csrf-token');
    assert.equal(f.refreshes(), 1);
  }
});

test('lifecycle failure refreshes state and shows refusal without claiming success', async () => {
  const f = cardHarness({responses: [{error: 'Players are online.'}]});
  f.running.checked = true; f.fire(f.running, 'change'); await f.settle();
  assert.equal(f.running.checked, false);
  assert.equal(f.refreshes(), 1);
  assert.equal(f.feedback.textContent, 'Players are online.');
});

test('boot uses exact selected ID and reviewed plan without extra confirmations', async () => {
  const f = cardHarness({responses: [{review: 'real-fingerprint'}, {applied: true}]});
  f.boot.checked = true; f.fire(f.boot, 'change'); await f.settle();
  assert.equal(f.boot.checked, false);
  assert.deepEqual(f.calls.map(call => call.url), ['plan','apply'].map(action => `/api/minecraft/workspace/settings/${action}?server=jv-kids001`));
  assert.equal(f.calls[1].body.get('review'), 'real-fingerprint');
  assert.equal(f.calls[1].body.get('tab'), 'overview');
  assert.equal(f.calls[1].body.get('start_on_boot'), 'true');
  assert.deepEqual([...f.calls[1].body.keys()].sort(), ['csrf','review','start_on_boot','tab']);
  assert.equal(f.refreshes(), 1);
});

for (const flag of ['confirmation_required','memory_restart_required','restart_required','players_confirmation_required']) {
  test(`boot directs ${flag} into existing Settings without applying`, async () => {
    const f = cardHarness({responses: [{review: 'review', [flag]: true}]});
    f.boot.checked = true; f.fire(f.boot, 'change'); await f.settle();
    assert.equal(f.calls.length, 1); assert.equal(f.boot.checked, false);
    assert.equal(f.feedback.children[0].href, '/?workspace=minecraft&server=jv-kids001');
    assert.equal(f.refreshes(), 1);
  });
}

test('missing review and unapplied settings preserve boot truth and expose error', async () => {
  for (const responses of [[{}], [{review:'review'}, {applied:false, error:'Settings changed.'}]]) {
    const f = cardHarness({responses});
    f.boot.checked = true; f.fire(f.boot, 'change'); await f.settle();
    assert.equal(f.boot.checked, false); assert.ok(f.feedback.textContent);
    assert.equal(f.refreshes(), 1);
  }
});

test('card delete delegates to shared two-stage helper and fixes immutable identity', async () => {
  const f = cardHarness(); f.fire(f.remove, 'click');
  assert.equal(f.calls.length, 0);
  const options = f.deletes[0];
  assert.equal(options.name, 'Kids Creative'); assert.equal(options.isCurrent(), true);
  await options.request('/api/minecraft/workspace/delete', {method:'POST', body: new URLSearchParams({confirm_name: options.name})});
  assert.equal(f.calls[0].url, '/api/minecraft/workspace/delete?server=jv-kids001');
  f.card.isConnected = false; assert.equal(options.isCurrent(), false);
  const shared = readFileSync(`${root}static/app.js`, 'utf8');
  assert.equal((shared.match(/dialog.className = "minecraft-delete-dialog"/g) || []).length, 1);
  assert.ok(shared.includes('Number(slider.value) < 100 || !toggle.checked'));
});

test('card invitation opens only a popup with Close before primary Copy', async () => {
  const f = cardHarness({responses: [{name:'Kids Creative', invitation:'Join my Minecraft Kids Creative on JustVoxel\n\nLocal connection IPv4\nJava: 192.0.2.10:25568'}]});
  f.fire(f.invite, 'click'); await f.settle();
  assert.equal(f.calls[0].url, '/api/minecraft/servers/invitation?server=jv-kids001');
  assert.equal(f.refreshes(), 0);
  assert.equal(f.dialogs.length, 1);
  const actions = f.dialogs[0].children.at(-1);
  assert.deepEqual(actions.children.map(node => node.textContent), ['Close','Copy invitation']);
  assert.equal(actions.children[0].className, 'secondary');
  assert.equal(actions.children[1].className, undefined);
});

test('unavailable invitation offers selected guide without inventing an address', async () => {
  const f = cardHarness({responses: [{error:'No default invitation is available.'}]});
  f.fire(f.invite, 'click'); await f.settle();
  assert.equal(f.dialogs.length, 0);
  assert.equal(f.feedback.children[0].href, '/minecraft/how-to-connect?server=jv-kids001');
});

test('arranging prevents card actions', async () => {
  const f = cardHarness(); f.arrange();
  f.fire(f.invite, 'click'); f.fire(f.restart, 'click'); f.fire(f.remove, 'click');
  await f.settle(); assert.equal(f.calls.length, 0); assert.equal(f.deletes.length, 0);
});

test('Inventory reuses the actual Storage menu and bounded responsive card grid', () => {
  const template = readFileSync(`${root}templates/minecraft_servers.html`, 'utf8');
  const css = readFileSync(`${root}static/minecraft-servers.css`, 'utf8');
  const header = readFileSync(`${root}templates/header.html`, 'utf8');
  for (const name of ['storage-action-menu', 'storage-action-menu-popover', 'storage-action-danger']) assert.ok(template.includes(name));
  assert.match(template, />⋯<\/summary>/);
  assert.doesNotMatch(template, />\.\.\.<\/summary>|<span>Server<\/span>|Enabled ·/);
  assert.match(template, /<span>Server running<\/span>/);
  assert.match(template, /{{if \.BedrockEnabled}}{{\.BedrockPort}}{{else}}Disabled{{end}}{{else}}Unavailable/);
  for (const page of [template, header]) assert.ok(page.includes('href="/static/storage-browser.css"'));
  assert.doesNotMatch(css, /minecraft-server-more>summary|minecraft-server-more-items[^\n]*(?:background|border|color|font)/);
  assert.match(css, /\.minecraft-server-more \.minecraft-server-more-items \{[^}]*box-shadow: none;/);
  assert.doesNotMatch(css, /repeat\(2,minmax\(0,1fr\)\)/);
  assert.match(css, /repeat\(auto-fill,minmax\(min\(100%,380px\),420px\)\)/);
  assert.match(css, /@container \(max-width: 600px\)[^\n]*grid-template-columns: 1fr/);
  const shared = readFileSync(`${root}static/storage-browser.css`, 'utf8');
  assert.match(shared, /storage-action-menu\[open\]>summary/);
  assert.match(shared, /\.storage-action-menu-popover\{[^}]*box-shadow:0 14px 36px #0008/);
  assert.match(shared, /\.storage-action-menu-popover,\.storage-action-dialog\{[^}]*box-shadow:0 14px 36px #0003/);
  const typography = shared.match(/\.storage-action-menu-popover :is\(button,a\)\{([^}]+)\}/)[1];
  for (const declaration of ['font-family:inherit', 'font-size:.82rem', 'font-weight:650', 'line-height:normal', 'text-align:left', 'align-items:center', 'padding:.58rem .65rem']) assert.ok(typography.includes(declaration));
  assert.match(shared, /\.storage-action-menu-popover \.storage-action-danger\{color:#ffb29a\}/);
});

test('Inventory action lane is between workspace header and body, without tab roles', () => {
  const template = readFileSync(`${root}templates/minecraft_servers.html`, 'utf8');
  const header = readFileSync(`${root}templates/header.html`, 'utf8');
  const workspace = header.slice(header.indexOf('data-workspace-window="minecraft-servers"'), header.indexOf('data-workspace-window="minecraft"'));
  const heading = workspace.indexOf('class="minecraft-workspace-header"');
  const slot = workspace.indexOf('<div data-minecraft-servers-actions></div>');
  const body = workspace.indexOf('class="minecraft-workspace-body minecraft-servers-body"');
  assert.ok(heading >= 0 && heading < slot && slot < body);
  const actions = template.match(/<nav[^>]*aria-label="Minecraft Servers actions"[\s\S]*?<\/nav>/)[0];
  assert.match(actions, /class="minecraft-workspace-tabs minecraft-server-arrange-toolbar"/);
  assert.doesNotMatch(actions, /role="tab|aria-selected/);
  assert.ok(actions.indexOf('>Add Server<') < actions.indexOf('>Rearrange<'));
  assert.ok(actions.indexOf('>Rearrange<') < actions.indexOf('>Import<'));
  assert.match(actions, /href="\/\?workspace=migration&amp;tab=import"/);
  // The fragment retains the lane for standalone pages; workspace mounting moves it.
  assert.ok(template.indexOf('<section class="minecraft-server-inventory"') < template.indexOf(actions));
});

test('Workspace Refresh moves the current actions into the slot once and replaces old actions', async () => {
  const app = readFileSync(`${root}static/app.js`, 'utf8');
  const start = app.indexOf('  const content = minecraftServersDialog.querySelector');
  const end = app.indexOf('  const workspaceWindow =', start);
  class Node {
    constructor() { this.children = []; }
    replaceChildren(...nodes) {
      this.children.forEach(node => { node.parent = null; });
      this.children = [];
      for (const node of nodes) {
        if (node.parent) node.parent.children.splice(node.parent.children.indexOf(node), 1);
        this.children.push(node); node.parent = this;
      }
    }
    querySelector(selector) { return this.children.find(node => node.selector === selector) || null; }
  }
  const content = new Node(), slot = new Node(), state = {};
  const fragments = [], initializations = [];
  const context = vm.createContext({
    URLSearchParams, window: {location: {search: ''}},
    minecraftServersDialog: {querySelector: selector => ({'[data-minecraft-servers-content]': content, '[data-minecraft-servers-state]': state, '[data-minecraft-servers-actions]': slot})[selector]},
    document: {createRange: () => ({selectNodeContents() {}, createContextualFragment() {
      const fragment = new Node();
      fragment.selector = '.minecraft-server-arrange-toolbar';
      fragments.push(fragment); return fragment;
    }})},
    fetch: async () => ({ok: true, redirected: false, text: async () => 'inventory'}),
    justVoxelMinecraftServers: root => { initializations.push(root.querySelector('.minecraft-server-arrange-toolbar')); },
  });
  vm.runInContext(app.slice(start, end) + '\nglobalThis.loadInventory = load;', context);
  await context.loadInventory();
  assert.equal(slot.children.length, 1);
  assert.equal(slot.children[0], fragments[0]);
  assert.equal(content.querySelector('.minecraft-server-arrange-toolbar'), null);
  await context.loadInventory();
  assert.equal(slot.children.length, 1);
  assert.equal(slot.children[0], fragments[1]);
  assert.equal(fragments[0].parent, null);
  assert.deepEqual(initializations, fragments);
  assert.equal(state.textContent, '');
});

test('Plugins capacity uses actual geometry up to 24 and retains resize pagination reset', () => {
  const app = readFileSync(`${root}static/app.js`, 'utf8');
  const start = app.indexOf('  const discoverCapacity =');
  const end = app.indexOf('  const updateCapacity =', start);
  for (const [columns, rows, expected] of [[1, 2, 2], [2, 3, 6], [4, 4, 16], [5, 4, 20], [5, 6, 24]]) {
    const context = vm.createContext({
      results: {clientHeight: rows * 144, getBoundingClientRect: () => ({width: columns * 300, height: rows * 144})},
      pageLimit: 8, getComputedStyle: () => ({gridTemplateColumns: Array(columns).fill('300px').join(' '), rowGap: '0px', getPropertyValue: () => '144px'}),
    });
    vm.runInContext(app.slice(start, end) + '\nglobalThis.capacity = discoverCapacity;', context);
    assert.equal(context.capacity(), expected);
  }
  const capacity = app.slice(start, app.indexOf('  capacityObserver.observe(results);', end));
  assert.match(capacity, /columns \* rows/);
  assert.match(capacity, /new ResizeObserver/);
  assert.match(capacity, /if \(newLimit === pageLimit\) return;/);
  assert.match(capacity, /pageLimit = newLimit;\s*resetCursors\(\);\s*if \(reload\) load\(\)/);
  const css = readFileSync(`${root}static/app.css`, 'utf8');
  for (const selector of ['.plugins-search', '.plugins-results', '.plugins-pagination']) {
    const start = css.indexOf(selector + '{');
    assert.doesNotMatch(css.slice(start, css.indexOf('}', start)), /1080px/);
  }
  for (const columns of [4, 5]) assert.ok(css.includes(`.plugins-results{grid-template-columns:repeat(${columns},minmax(0,1fr))}`));
});

for (const workspace of ['minecraft', 'backups', 'version', 'plugins']) {
  test(`${workspace} card menu selects in place through the shared helper and existing launcher`, () => {
    const f = cardHarness();
    const link = f.links.find(link => link.dataset.serverWorkspace === workspace);
    const event = f.fire(link, 'click');
    assert.equal(event.defaultPrevented, true);
    assert.equal(f.menu.open, false);
    assert.deepEqual(f.selections, [[ids[1], workspace]]);
    assert.deepEqual(f.launches, [`[data-${workspace}-open]`]);
    assert.equal(f.dashboard.dataset.selectedMinecraftId, ids[1]);
    assert.equal(new URL(f.location.href).searchParams.get('workspace'), workspace);
    assert.equal(new URL(f.location.href).searchParams.has('operation'), false);
    for (const dropdown of f.dropdowns) assert.equal(dropdown.value, ids[1]);
    assert.equal(f.context.justVoxelSelectionGeneration, 1);
    assert.equal(f.selectionEvents.length, 1);
    f.menu.open = true;
    f.fire(link, 'click');
    assert.equal(f.launches.length, 2);
    assert.equal(f.context.justVoxelSelectionGeneration, 1, 'same selection must not fabricate a server change');
    assert.equal(f.selectionEvents.length, 1);
    assert.equal(link.href, `/?workspace=${workspace}&server=${ids[1]}`);
    for (const fields of [{ctrlKey:true}, {metaKey:true}, {shiftKey:true}, {altKey:true}, {button:1}, {button:2}]) {
      f.menu.open = true;
      assert.equal(f.fire(link, 'click', fields).defaultPrevented, false);
      assert.equal(f.launches.length, 2);
      assert.equal(f.selections.length, 2);
    }
  });
}

test('workspace links retain browser fallback when helper or launcher is unavailable', () => {
  for (const options of [{helperAvailable:false}, {launcherAvailable:false}]) {
    const f = cardHarness(options);
    for (const link of f.links) assert.equal(f.fire(link, 'click').defaultPrevented, false);
    assert.equal(f.launches.length, 0);
    assert.equal(f.selections.length, 0);
  }
  assert.doesNotMatch(source, /location\.(?:reload|assign)|location\.href\s*=/);
});

test('menu markup retains immutable hrefs and opens only the guide in a new tab', () => {
  const template = readFileSync(`${root}templates/minecraft_servers.html`, 'utf8');
  for (const workspace of ['minecraft', 'backups', 'version', 'plugins']) {
    assert.ok(template.includes(`href="/?workspace=${workspace}&amp;server={{.ContextID}}" data-server-workspace="${workspace}"`));
  }
  const guide = template.match(/<a[^>]*href="\/minecraft\/how-to-connect\?server={{\.ContextID}}"[^>]*>/)[0];
  assert.match(guide, /target="_blank" rel="noopener"/);
  assert.doesNotMatch(guide, /data-server-workspace/);
  const f = cardHarness();
  assert.equal(f.fire(f.guide, 'click').defaultPrevented, false);
  assert.equal(f.selections.length, 0);
  // The popover delegates normal guide activation solely to closing the menu.
  f.fire(f.menu.querySelector('[popover]'), 'click', {target: {closest: () => f.guide}});
  assert.equal(f.menu.open, false);
  assert.equal(f.launches.length, 0);
});

test('invitation closes outside its rectangle, keeps internal clicks and copy open, and cleans up', async () => {
  const invitation = 'Join my Minecraft Kids Creative on JustVoxel\n\nLocal connection IPv4\nJava: 192.0.2.10:25568';
  const f = cardHarness({responses: [{name:'Kids Creative', invitation}]});
  f.fire(f.invite, 'click'); await f.settle();
  assert.equal(f.menu.open, false);
  assert.equal(f.selections.length, 0);
  const dialog = f.dialogs[0];
  const text = dialog.querySelector('textarea');
  const [close, copy] = dialog.children.at(-1).children;
  for (const target of [dialog, text, copy, dialog.children[0]]) {
    f.fire(dialog, 'click', {target, clientX:101, clientY:101});
    assert.equal(dialog.open, true, 'inside padding and contents stay open');
  }
  f.fire(copy, 'click'); await f.settle();
  assert.deepEqual(f.copied, [invitation]);
  assert.equal(dialog.open, true);
  f.fire(close, 'click');
  assert.equal(dialog.open, false);
  assert.equal(dialog.removed, true);
  for (const point of [{clientX:99,clientY:200}, {clientX:501,clientY:200}, {clientX:200,clientY:99}, {clientX:200,clientY:501}]) {
    dialog.open = true; dialog.removed = false;
    f.fire(dialog, 'click', point);
    assert.equal(dialog.open, false);
    assert.equal(dialog.removed, true);
  }
  // Native modal Escape cancellation is left enabled and reaches the same cleanup.
  assert.equal(dialog.listeners.cancel, undefined);
  assert.match(source, /dialog\.showModal\(\)/);
  dialog.open = true; dialog.removed = false; dialog.pressEscape();
  assert.equal(dialog.open, false);
  assert.equal(dialog.removed, true);
  assert.doesNotMatch(source, /document\.addEventListener\(['"]click/);
});
