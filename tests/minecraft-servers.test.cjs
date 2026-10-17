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
  const add = list.appendChild(new Element('.minecraft-add-server-card'));
  add.href = '/minecraft/servers/add';
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
    list, add, arrange, done, cards, writes,
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
    assert.equal(p.list.children[0], p.add);
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
  assert.equal(p.list.children[0], p.add);
  assert.equal(p.add.dataset.serverId, undefined);
  assert.equal(p.add.querySelector('[data-server-move-earlier]'), null);
  assert.equal(p.add.querySelector('[data-server-move-later]'), null);
  assert.equal(p.add.href, '/minecraft/servers/add');
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

test('empty and single inventories never reorder Add Server', () => {
  const empty = page({ inventory: [] });
  assert.equal(empty.arrange.hidden, true);
  assert.deepEqual(empty.list.children, [empty.add]);
  const single = page({ inventory: [ids[0]] });
  single.arrange.fire('click');
  assert.equal(earlier(single.cards[0]).disabled, true);
  assert.equal(later(single.cards[0]).disabled, true);
  assert.equal(single.list.children[0], single.add);
});

test('script is page scoped and has no backend or configuration mutation path', () => {
  const template = readFileSync(`${root}templates/minecraft_servers.html`, 'utf8');
  const sharedHeader = template.slice(0, template.indexOf('{{define "minecraft_servers.html"}}'));
  assert.ok(!sharedHeader.includes('/static/minecraft-servers.js'));
  const header = readFileSync(`${root}templates/header.html`, 'utf8');
  assert.equal((header.match(/src="\/static\/minecraft-servers.js"/g) || []).length, 1);
  assert.doesNotMatch(source, /fetch\s*\(|XMLHttpRequest|\.submit\s*\(|\.requestSubmit\s*\(|\.Servers\[0\]|\.instances\[0\]|default.server|first.server/i);
  assert.doesNotMatch(source, /textContent|innerHTML|location|\/api\/|\/var\/|password|token|csrf/);
  assert.match(source, /JSON\.stringify\(cards\(\)\.map\(card => card\.dataset\.serverId\)/);
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
