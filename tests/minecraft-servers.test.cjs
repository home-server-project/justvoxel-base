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
function cardHarness({responses = [], bootChecked = false, runningChecked = false, connected = true} = {}) {
  const card = new Element('card', {serverId: ids[1], serverName: 'Kids Creative'});
  card.isConnected = connected;
  const running = card.appendChild(new Element('[data-server-running]')); running.checked = runningChecked;
  const boot = card.appendChild(new Element('[data-server-boot]')); boot.checked = bootChecked;
  const restart = card.appendChild(new Element('[data-server-restart]'));
  const remove = card.appendChild(new Element('[data-server-delete]'));
  const invite = card.appendChild(new Element('[data-server-invite]'));
  const queryAll = card.querySelectorAll.bind(card);
  card.querySelectorAll = selector => selector === 'input, button' ? [running, boot, restart, remove, invite] : queryAll(selector);
  const feedback = new Element('feedback');
  const calls = [], deletes = [], dialogs = [];
  let refreshes = 0;
  const root = {
    querySelector: selector => selector === '[data-server-csrf]' ? {dataset: {serverCsrf: 'csrf-token'}} : feedback,
    justVoxelRefreshServers: async () => { refreshes++; },
  };
  const create = tag => {
    const node = new Element(tag);
    node.setAttribute = () => {};
    node.append = (...nodes) => nodes.forEach(child => node.appendChild(child));
    node.showModal = () => { node.open = true; dialogs.push(node); };
    node.close = () => { node.open = false; };
    return node;
  };
  const context = vm.createContext({URLSearchParams,
    document: {createElement: create, body: create('body')},
    justVoxelOpenDeleteServer: options => deletes.push(options),
    fetch: async (url, options) => {
      calls.push({url, body: options.body});
      const result = responses.shift() ?? {ok: true};
      if (result instanceof Error) throw result;
      return {ok: !result.error, redirected: false, json: async () => result};
    },
  });
  const start = source.indexOf('  function initializeCard(');
  const end = source.indexOf("  window.addEventListener?.('resize'");
  vm.runInContext(source.slice(start, end) + '\nglobalThis.initialize = initializeCard;', context);
  let arranging = false;
  context.initialize(card, root, () => arranging);
  const fire = (control, type) => control.listeners[type][0].handler();
  return {card, running, boot, restart, remove, invite, feedback, calls, deletes, dialogs,
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
  const f = cardHarness({responses: [{name:'Kids Creative', invitation:'Join Kids Creative on Minecraft\n\nLocal connection IPv4\nJava: 192.0.2.10:25568'}]});
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
