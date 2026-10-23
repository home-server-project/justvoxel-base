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
  } }, setInterval: () => 1, clearInterval: () => {} };
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
  const status = card.appendChild(new Element('.minecraft-server-state'));
  status.textContent = runningChecked ? 'Running' : 'Stopped';
  status.className = 'minecraft-server-state ' + (runningChecked ? 'success' : 'error');
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
  const dropdowns = ['minecraft','version','plugins','backups','migration'].map(workspace => ({workspace,value:ids[0]}));
  const selectionEvents = [];
  const location = {href: `https://justvoxel.test/?workspace=minecraft-servers&server=${ids[0]}&operation=stale`, origin: 'https://justvoxel.test', search: `?workspace=minecraft-servers&server=${ids[0]}&operation=stale`};
  let refreshes = 0;
  const inventoryRoot = {
    querySelector: selector => selector === '[data-server-csrf]' ? {dataset: {serverCsrf: 'csrf-token'}} : feedback,
    justVoxelRefreshServers: async () => {
      refreshes++;
      status.textContent = runningChecked ? 'Running' : 'Stopped';
      status.className = 'minecraft-server-state ' + (runningChecked ? 'success' : 'error');
    },
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
      querySelectorAll: selector => dropdowns.filter(control => selector.includes(`data-server-workspace="${control.workspace}"`)),
      querySelector: selector => selector === '#dashboard[data-dashboard-status]' ? dashboard :
        launcherAvailable ? {click() { assert.equal(menu.open, false); launches.push(selector); }} : null},
    justVoxelOpenDeleteServer: options => deletes.push(options),
    fetch: async (url, options) => {
      calls.push({url, body: options.body});
      const response = responses.shift() ?? {ok: true};
      const result = await (typeof response === 'function' ? response() : response);
      if (result instanceof Error) throw result;
      return {ok: !result.error, redirected: false, json: async () => result};
    },
  });
  const app = readFileSync(`${root}static/app.js`, 'utf8');
  const sharedStart = app.indexOf('const workspaceUsername =');
  const sharedEnd = app.indexOf('globalThis.justVoxelServerSelection =', sharedStart);
  vm.runInContext('const selectionDashboard = document.querySelector("#dashboard[data-dashboard-status]"); const dashboardURL = null;\n' + app.slice(sharedStart, sharedEnd).replace('if (selectionDashboard && workspaceRestoreAllowed())', 'if (false)'), context);
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
  return {card, status, running, boot, restart, remove, invite, feedback, calls, deletes, dialogs, menu, links, guide, selections, launches, copied, context, location, dashboard, dropdowns, selectionEvents,
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
  assert.doesNotMatch(css, /minecraft-server-more>summary/);
  const menuSurface = css.match(/\.minecraft-server-more \.minecraft-server-more-items \{([^}]+)\}/)[1];
  assert.doesNotMatch(menuSurface, /background|border|color|font/);
  assert.match(css, /\.minecraft-server-more \.minecraft-server-more-items \{[^}]*box-shadow: none;/);
  assert.doesNotMatch(css, /repeat\(2,minmax\(0,1fr\)\)/);
  assert.match(css, /repeat\(auto-fill,minmax\(min\(100%,420px\),420px\)\)/);
  assert.match(css, /@container \(max-width: 600px\)[^\n]*grid-template-columns: min\(100%,420px\)/);
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
  assert.match(capacity, /Math\.min\(24, columns \* rows\)/);
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
    assert.equal(f.dashboard.dataset.selectedMinecraftId, workspace === 'minecraft' ? ids[1] : ids[0]);
    assert.equal(new URL(f.location.href).searchParams.get('workspace'), workspace);
    assert.equal(new URL(f.location.href).searchParams.has('operation'), false);
    for (const dropdown of f.dropdowns) assert.equal(dropdown.value, dropdown.workspace === workspace ? ids[1] : ids[0]);
    assert.equal(f.context.justVoxelSelectionGeneration[workspace], 1);
    assert.equal(f.selectionEvents.length, 1);
    assert.equal(f.selectionEvents[0].detail.workspace, workspace);
    f.menu.open = true;
    f.fire(link, 'click');
    assert.equal(f.launches.length, 2);
    assert.equal(f.context.justVoxelSelectionGeneration[workspace], 1, 'same selection must not fabricate a server change');
    assert.equal(f.selectionEvents.length, 1);
    assert.equal(f.selectionEvents[0].detail.workspace, workspace);
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

for (const runningChecked of [false, true]) {
  for (const failed of [false, true]) {
    test(`card shows ${runningChecked ? 'Stopping' : 'Starting'} until authoritative refresh, failure=${failed}`, async () => {
      let resolve;
      const f = cardHarness({runningChecked, responses: [() => new Promise(done => { resolve = done; })]});
      f.running.checked = !runningChecked;
      f.fire(f.running, 'change');
      assert.equal(f.status.textContent, runningChecked ? 'Stopping' : 'Starting');
      assert.equal(f.status.className, 'minecraft-server-state warning');
      assert.equal(f.running.checked, runningChecked);
      assert.equal(f.running.disabled, true);
      assert.equal(f.refreshes(), 0);
      resolve(failed ? {error: 'Lifecycle refused.'} : {ok: true});
      await f.settle();
      assert.equal(f.refreshes(), 1);
      assert.equal(f.status.textContent, runningChecked ? 'Running' : 'Stopped');
      if (failed) assert.equal(f.feedback.textContent, 'Lifecycle refused.');
    });
  }
}

test('card metrics keep equal tracks and center exact Bedrock port labels and values', () => {
  const template = readFileSync(`${root}templates/minecraft_servers.html`, 'utf8');
  const css = readFileSync(`${root}static/minecraft-servers.css`, 'utf8');
  assert.match(template, /<span>Bedrock port<\/span>/);
  assert.match(css, /\.minecraft-server-metrics \{[^}]*grid-template-columns: repeat\(3,minmax\(0,1fr\)\)/);
  assert.match(css, /\.minecraft-server-metrics>div \{[^}]*text-align: center/);
  assert.match(css, /repeat\(auto-fill,minmax\(min\(100%,420px\),420px\)\)/);
  assert.doesNotMatch(css, /380px/);
  for (const query of ['@media (max-width: 700px)', '@container (max-width: 600px)']) {
    assert.ok(css.includes(query + ' { .minecraft-server-inventory .minecraft-server-entries { grid-template-columns: min(100%,420px); } }'));
  }
});

test('Minecraft menu scopes a full separator before the bold success/danger action group', () => {
  const template = readFileSync(`${root}templates/minecraft_servers.html`, 'utf8');
  const css = readFileSync(`${root}static/minecraft-servers.css`, 'utf8');
  const menu = template.slice(template.indexOf('class="storage-action-menu-popover'), template.indexOf('</details>'));
  let previous = -1;
  for (const token of ['>Settings<', '>Backups<', '>Version<', '>Plugins<', '>Invite friends<', '>How to connect<', '<hr>', '{{if .CanRestart}}', '>Restart<', '>Delete server<']) {
    const index = menu.indexOf(token);
    assert.ok(index > previous, token); previous = index;
  }
  assert.match(css, /\.minecraft-server-more \.minecraft-server-more-items>hr \{[^}]*width: 100%;[^}]*height: 0;[^}]*border: 0;[^}]*border-top: 1px solid var\(--jv-border\)/);
  for (const [action, color] of [['restart', 'success'], ['delete', 'error']]) {
    assert.ok(css.includes(`.minecraft-server-more .minecraft-server-more-items>[data-server-${action}] { color: var(--jv-${color}); font-weight: 800; }`));
  }
  assert.match(css, /\.minecraft-server-more \.minecraft-server-more-items \{[^}]*box-shadow: none/);
});

test('card software icon is small, proportional and precedes the existing text', () => {
  const template = readFileSync(`${root}templates/minecraft_servers.html`, 'utf8');
  const css = readFileSync(`${root}static/minecraft-servers.css`, 'utf8');
  assert.match(template, /{{if \.SoftwareIcon}}<img class="minecraft-server-software-icon" src="{{\.SoftwareIcon}}" alt="">{{end}}<span>{{\.Software}} · {{\.Version}}<\/span>/);
  assert.match(css, /\.minecraft-server-software-icon \{[^}]*width: 1\.1rem; height: 1\.1rem; object-fit: contain; flex: none; align-self: center;/);
  assert.match(css, /\.minecraft-server-software-row>p \{[^}]*align-items: baseline/);
});

function importHarness(launcherAvailable = true) {
  const link = new Element('[data-server-import]');
  link.href = '/?workspace=migration&tab=import';
  const location = {href: 'https://justvoxel.test/?workspace=minecraft-servers&operation=stale&server=jv-kids001'};
  const replacements = [], launches = [];
  const state = {keep: true};
  const context = vm.createContext({
    URL,
    root: {querySelector: selector => selector === '[data-server-import]' ? link : null},
    document: {querySelector: selector => {
      assert.equal(selector, '[data-migration-open]');
      return launcherAvailable ? {click() { launches.push(new URL(location.href)); }} : null;
    }},
    window: {location, history: {state, replaceState(value, title, destination) {
      assert.equal(value, state); assert.equal(title, '');
      replacements.push(destination); location.href = destination.href;
    }}},
  });
  const start = source.indexOf("  const importLink = root.querySelector('[data-server-import]');");
  const end = source.indexOf('\n  };', start);
  assert.ok(start >= 0 && end > start);
  vm.runInContext(source.slice(start, end), context);
  const click = (fields = {}) => {
    const event = {button: 0, defaultPrevented: false, preventDefault() { this.defaultPrevented = true; }, ...fields};
    link.listeners.click[0].handler(event); return event;
  };
  return {link, location, replacements, launches, click};
}

test('Import keeps Dashboard alive, replaces URL state and clicks the existing launcher', () => {
  const f = importHarness();
  assert.equal(f.click().defaultPrevented, true);
  assert.equal(f.replacements.length, 1); assert.equal(f.launches.length, 1);
  const destination = f.launches[0];
  assert.equal(destination.searchParams.get('workspace'), 'migration');
  assert.equal(destination.searchParams.get('tab'), 'import');
  assert.equal(destination.searchParams.has('operation'), false);
  assert.equal(destination.searchParams.get('server'), 'jv-kids001');
  assert.equal(f.link.href, '/?workspace=migration&tab=import');
  assert.doesNotMatch(source, /location\.(?:reload|assign|replace)\s*\(|location\.href\s*=/);
  const template = readFileSync(`${root}templates/minecraft_servers.html`, 'utf8');
  assert.match(template, /href="\/\?workspace=migration&amp;tab=import" data-server-import>Import<\/a>/);
  const p = page();
  p.initialize();
  assert.equal(p.arrange.listeners.click.length, 1);
});

test('Import preserves modified, middle and prevented clicks and missing-launcher fallback', () => {
  for (const fields of [{ctrlKey:true}, {metaKey:true}, {shiftKey:true}, {altKey:true}, {button:1}, {button:2}, {defaultPrevented:true}]) {
    const f = importHarness();
    const event = f.click(fields);
    assert.equal(event.defaultPrevented, !!fields.defaultPrevented);
    assert.equal(f.replacements.length, 0); assert.equal(f.launches.length, 0);
  }
  const f = importHarness(false);
  assert.equal(f.click().defaultPrevented, false);
  assert.equal(f.replacements.length, 0); assert.equal(f.launches.length, 0);
});

// Deterministic clock/network fixture; no real polling timers or browser storage.
function liveMemoryHarness(states = ['Running', 'Running', 'Stopped', 'Starting']) {
  const list = {isConnected: true};
  const cards = states.map((state, index) => {
    const card = new Element('card', {serverId: ids[index] || 'jv-start01'});
    const tile = card.appendChild(new Element('[data-server-ram]', {serverState: state}));
    tile.appendChild(new Element('[data-server-ram-value]'));
    const graph = tile.appendChild(new Element('[data-server-ram-graph]'));
    graph.replaceChildren = () => { graph.children = []; };
    return card;
  });
  const calls = [], intervals = [], cleared = [], responses = [];
  let now = 0;
  const context = vm.createContext({
    Date: {now: () => now},
    document: {createElementNS(namespace, tag) {
      assert.equal(namespace, 'http://www.w3.org/2000/svg');
      assert.equal(tag, 'polyline');
      const node = new Element(tag); node.attributes = {};
      node.setAttribute = (name, value) => { node.attributes[name] = value; };
      return node;
    }},
    fetch: async (url, options) => {
      calls.push({url, options});
      const next = responses.shift() ?? [];
      if (next instanceof Error) throw next;
      if (typeof next === 'function') return next();
      return {ok: true, redirected: false, json: async () => next};
    },
    setInterval(callback, delay) { intervals.push({callback, delay}); return intervals.length; },
    clearInterval: id => cleared.push(id),
    list, cards: () => cards,
  });
  const start = source.indexOf('  function initializeLiveMemory(');
  const end = source.indexOf('  function initializeCard(', start);
  vm.runInContext(source.slice(start, end) + '\nglobalThis.initialize = () => initializeLiveMemory(list, cards);', context);
  const settle = async () => { for (let i = 0; i < 12; i++) await Promise.resolve(); };
  return {
    list, cards, calls, intervals, cleared, responses, settle,
    start: () => context.initialize(),
    async tick() { now += 2000; await intervals[0].callback(); await settle(); },
    value: index => cards[index].querySelector('[data-server-ram-value]').textContent,
    points: index => cards[index].querySelector('[data-server-ram-graph]').children[0]?.attributes.points,
  };
}

const memoryRow = (id, usage, limit = 1024 ** 3) => ({id, usage_bytes: usage, limit_bytes: limit});

test('one immediate authenticated RAM read serves all cards, then one 2000 ms timer', async () => {
  const f = liveMemoryHarness();
  const rows = [memoryRow(ids[0], 768 * 1024 ** 2), memoryRow(ids[1], 2.34 * 1024 ** 3), memoryRow(ids[2], 123), memoryRow('jv-start01', 256 * 1024 ** 2)];
  f.responses.push(rows); f.start();
  assert.equal(f.calls.length, 1, 'first read is immediate');
  await f.settle();
  assert.equal(f.intervals.length, 1);
  assert.equal(f.intervals[0].delay, 2000);
  assert.equal(f.calls[0].url, '/api/minecraft/servers/live-memory');
  assert.equal(f.calls[0].options.credentials, 'same-origin');
  assert.equal(f.calls[0].options.cache, 'no-store');
  assert.deepEqual(f.cards.map((_, index) => f.value(index)), ['768 MB', '2.34 GB', '---', '256 MB']);
  assert.equal(f.points(0), undefined, 'one point cannot draw a trend');
  f.responses.push(rows); await f.tick();
  assert.equal(f.calls.length, 2);
  assert.equal(f.points(2), undefined, 'stopped rows never add even a zero sample');
  assert.ok(f.points(3), 'transitional state may have live RAM');
  assert.equal(f.points(0), '112,3 116,3');
  assert.equal(f.points(1), '112,0 116,0', 'geometry clamps; displayed usage is measured');
  f.cards[2].querySelector('[data-server-ram]').dataset.serverState = 'Running';
  f.responses.push(rows); await f.tick();
  assert.equal(f.points(2), undefined, 'stopped history must be empty when a new live run begins');
});

test('RAM history caps at 30 samples and scales against the container limit', async () => {
  const f = liveMemoryHarness(['Running']);
  const row = usage => [memoryRow(ids[0], usage, 100)];
  f.responses.push(row(50)); f.start(); await f.settle();
  for (let i = 0; i < 34; i++) { f.responses.push(row(25)); await f.tick(); }
  const points = f.points(0).split(' ');
  assert.equal(points.length, 30);
  assert.equal(points[0], '0,9');
  assert.equal(points.at(-1), '116,9');
  assert.equal(f.intervals.length, 1);
  assert.equal(f.calls.length, 35);
});

test('missing, invalid or failed RAM reads clear stale graphs without inventing zero', async () => {
  const f = liveMemoryHarness(['Running', 'Stopped', 'Stopping', 'Failed', 'Recovery required']);
  const rows = f.cards.map(card => memoryRow(card.dataset.serverId, 1024 ** 3));
  f.responses.push(rows); f.start(); await f.settle();
  f.responses.push(rows); await f.tick();
  assert.ok(f.points(0));
  for (const response of [[], new Error('offline'), [memoryRow(ids[0], -1)], [memoryRow(ids[0], NaN)], [memoryRow(ids[0], Infinity)], [memoryRow(ids[0], '12')]]) {
    f.responses.push(response); await f.tick();
    assert.deepEqual(f.cards.map((_, index) => f.value(index)), ['Unavailable', '---', 'Unavailable', 'Unavailable', 'Unavailable']);
    for (let i = 0; i < f.cards.length; i++) assert.equal(f.points(i), undefined);
  }
  f.responses.push(rows); await f.tick();
  assert.equal(f.points(0), undefined, 'no fabricated points survived a missing sample');
  assert.equal(f.points(1), undefined);
});

test('valid usage without a valid limit has no invented graph scale', async () => {
  const f = liveMemoryHarness(['Running']);
  f.start(); await f.settle();
  for (const limit of [undefined, null, 0, -1, NaN, Infinity, '1024']) {
    for (let i = 0; i < 2; i++) {
      f.responses.push([{id: ids[0], usage_bytes: 1024 ** 3, limit_bytes: limit}]); await f.tick();
      assert.equal(f.value(0), '1.00 GB');
      assert.equal(f.points(0), undefined);
    }
  }
});

test('disconnection stops the shared timer and outstanding reads never update removed cards', async () => {
  const f = liveMemoryHarness(['Running']);
  let resolve;
  f.responses.push(() => new Promise(done => { resolve = done; }));
  f.start(); await f.tick();
  assert.equal(f.calls.length, 1, 'pending read does not overlap');
  f.list.isConnected = false; await f.tick();
  assert.ok(f.cleared.includes(1));
  resolve({ok: true, json: async () => [memoryRow(ids[0], 100)]});
  await f.settle();
  assert.equal(f.value(0), undefined);
  assert.equal(f.points(0), undefined);
  assert.equal(f.calls.length, 1);
  const replacement = liveMemoryHarness(['Running']);
  replacement.start(); await replacement.settle();
  assert.equal(replacement.calls.length, 1);
  assert.equal(replacement.intervals.length, 1);
});
