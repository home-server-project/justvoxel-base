const assert = require('node:assert/strict');
const { readFileSync, readdirSync } = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');
const root = path.join(__dirname, '../webui/internal/server');
const read = name => readFileSync(path.join(root, name), 'utf8');

test('Add Server reuses the maintained wizard, software controls and six-step presentation', () => {
  const wizard = read('templates/setup_wizard.html');
  const review = read('templates/setup_review.html');
  for (const control of ['server-software-card', 'data-memory-settings', 'setup-backup-topline', 'data-setup-connections']) assert.ok(wizard.includes(control));
  assert.ok(wizard.includes('{{$wizard.BasePath}}/recommended'));
  assert.ok(wizard.includes('{{template "setup-instance-name" .}}'));
  assert.ok(wizard.includes('name="display_name"'));
  assert.ok(wizard.includes('name="motd"'));
  for (const [index, name] of ['Server', 'Cross-play', 'Memory', 'Storage', 'Backups', 'Review'].entries()) assert.ok(review.includes(`<span class="setup-number">${index + 1}</span><span>${name}</span>`));
  const designs = readdirSync(path.join(root, 'templates')).filter(name => name.endsWith('.html') && read('templates/' + name).includes('data-setup-connections'));
  assert.deepEqual(designs, ['setup_wizard.html']);
});

test('Add Server is a keyboard accessible native action outside the ordered cards', () => {
  const markup = read('templates/minecraft_servers.html');
  assert.ok(markup.includes('<a href="{{if .Configured}}/minecraft/servers/add{{else}}/setup{{end}}"{{if .Configured}} target="_blank" rel="noopener"{{end}}>Add Server</a>'));
  assert.ok(!markup.includes('minecraft-add-server-card'));
  assert.ok(markup.indexOf('>Add Server</a>') < markup.indexOf('minecraft-server-entries'));
  assert.ok(read('static/minecraft-servers.css').includes(':focus-visible'));
});

test('How to Connect remains a single implementation with no Add Server connection alternative', () => {
  for (const [folder, expected] of [
    ['templates', ['how_to_connect.html']],
    ['static', ['how-to-connect.css', 'how-to-connect.js']],
  ]) {
    const names = readdirSync(path.join(root, folder)).filter(name => /how[-_]to[-_]connect/.test(name)).sort();
    assert.deepEqual(names, expected);
  }
  assert.ok(read('how_to_connect.go').includes('how_to_connect.html'));
  assert.ok(!read('templates/minecraft_servers.html').includes('connect-method'));
  assert.ok(!read('templates/setup_wizard.html').includes('connect-method'));
});

test('Add Server Review retains the compact stretched grid without height sizing', () => {
  const css = read('static/setup.css');
  const rules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)]
    .filter(([, selector]) => selector.includes('.setup-review-summary-add-server'));
  assert.ok(css.includes('grid-template-areas:"server cross-play" "server startup" "minecraft storage";align-items:stretch'));
  assert.ok(!css.includes('"minecraft startup"'));
  assert.ok(!css.includes('"storage storage"'));
  for (const [, , declarations] of rules) {
    assert.doesNotMatch(declarations, /(?:^|;)(?:min-|max-)?height\s*:|grid-auto-rows\s*:|grid-template-rows\s*:/);
  }
  assert.ok(css.includes('@media(max-width:700px){\n  .setup-review-summary.setup-review-summary-add-server{grid-template-areas:"server" "cross-play" "startup" "minecraft" "storage"}'));
  assert.ok(css.includes('.setup-body.setup-add-server-review{height:auto;min-height:100dvh;overflow:visible}'));
});

test('only Add Server exits have tab closing hooks and Review keeps the real POST and CSRF', () => {
  const wizard = read('templates/setup_wizard.html');
  assert.ok(wizard.includes('<script src="/static/setup-operation.js" defer></script>'));
  assert.ok(wizard.includes('{{if .AddServer}}<div class="setup-terminal-note"><a class="button-link secondary setup-dashboard-exit" href="/?workspace=minecraft-servers" data-add-server-cancel>Cancel</a></div>{{else}}<div class="setup-terminal-note"><a class="button-link secondary setup-dashboard-exit" href="/">Back to dashboard</a>'));
  const review = read('templates/setup_review.html');
  assert.ok(review.includes('<form method="post" action="{{$wizard.BasePath}}/review/cancel"{{if .AddServer}} data-add-server-review-cancel{{end}}><input type="hidden" name="csrf" value="{{.CSRF}}">'));
  assert.ok(review.includes('{{if .AddServer}}<p class="notice error" role="alert" data-add-server-cancel-error hidden></p>{{end}}'));
});

// Exercise the scoped browser handlers without loading unrelated dashboard code.
const vm = require('node:vm');
const setupSource = read('static/setup-operation.js');
function loadSetupOperation(context) {
  let onReady;
  context.document.addEventListener = (name, handler) => {
    assert.equal(name, 'DOMContentLoaded');
    onReady = handler;
  };
  vm.runInNewContext(setupSource, context);
  assert.equal(typeof onReady, 'function');
  onReady();
}
function wizardTab({ review = false, closes = false, closeThrows = false, response, requestError } = {}) {
  const events = {};
  const timers = [];
  const navigations = [];
  const requests = [];
  const error = { hidden: true, textContent: '' };
  const control = {
    href: 'https://example.test/?workspace=minecraft-servers',
    action: 'https://example.test/minecraft/servers/add/review/cancel',
    addEventListener: (name, handler) => { events[name] = handler; },
  };
  let closeCalls = 0;
  const window = {
    closed: false,
    location: { origin: 'https://example.test', assign: value => navigations.push(value) },
    close() {
      closeCalls++;
      if (closeThrows) throw new Error('Closing denied');
      window.closed = closes;
    },
  };
  class FormData {
    constructor(form) { this.form = form; }
  }
  loadSetupOperation({
    document: { querySelector: selector => ({
      '[data-add-server-cancel]': review ? null : control,
      '[data-add-server-review-cancel]': review ? control : null,
      '[data-add-server-cancel-error]': error,
    })[selector] },
    window, URL, FormData,
    setTimeout: fn => timers.push(fn),
    fetch: async (action, options) => {
      requests.push({ action, options });
      assert.equal(closeCalls, 0, 'must await cancellation before closing');
      if (requestError) throw requestError;
      return response || { ok: true, redirected: true, url: 'https://example.test/?workspace=minecraft-servers' };
    },
  });
  return { events, timers, navigations, requests, error, control, closeCalls: () => closeCalls };
}
function ordinaryEvent(overrides = {}) {
  return { button: 0, prevented: false, preventDefault() { this.prevented = true; }, ...overrides };
}

test('intermediate Cancel closes only an ordinary click, with direct fallback if closing is denied', () => {
  for (const closes of [true, false]) {
    const tab = wizardTab({ closes });
    const event = ordinaryEvent();
    tab.events.click(event);
    assert.equal(event.prevented, true);
    assert.equal(tab.closeCalls(), 1);
    tab.timers.forEach(fn => fn());
    assert.deepEqual(tab.navigations, closes ? [] : [tab.control.href]);
    assert.equal(tab.requests.length, 0);
  }
  const rejected = wizardTab({ closeThrows: true });
  rejected.events.click(ordinaryEvent());
  rejected.timers.forEach(fn => fn());
  assert.deepEqual(rejected.navigations, [rejected.control.href]);
  for (const overrides of [{ ctrlKey: true }, { metaKey: true }, { shiftKey: true }, { altKey: true }, { button: 1 }, { defaultPrevented: true }]) {
    const tab = wizardTab();
    const event = ordinaryEvent(overrides);
    tab.events.click(event);
    assert.equal(event.prevented, false);
    assert.equal(tab.closeCalls(), 0);
  }
});

test('Review Return POSTs actual FormData before closing, with direct fallback', async () => {
  for (const closes of [true, false]) {
    const tab = wizardTab({ review: true, closes });
    const event = ordinaryEvent();
    await tab.events.submit(event);
    assert.equal(event.prevented, true);
    assert.equal(tab.requests.length, 1);
    const { action, options } = tab.requests[0];
    assert.equal(action, tab.control.action);
    assert.equal(options.method, 'POST');
    assert.equal(options.credentials, 'same-origin');
    assert.equal(options.redirect, 'follow');
    assert.equal(options.body.form, tab.control);
    assert.equal(tab.closeCalls(), 1);
    assert.equal(tab.error.hidden, true);
    tab.timers.forEach(fn => fn());
    assert.deepEqual(tab.navigations, closes ? [] : ['/?workspace=minecraft-servers']);
  }
});

test('Review cancellation failure stays visible and retryable without closing or navigating', async () => {
  for (const failure of [
    { requestError: new TypeError('Network failure') },
    { response: { ok: false, redirected: false, url: 'https://example.test/minecraft/servers/add/review/cancel' } },
    { response: { ok: true, redirected: true, url: 'https://example.test/login' } },
    { response: { ok: true, redirected: true, url: 'https://example.test/minecraft/servers/add/review' } },
  ]) {
    const tab = wizardTab({ review: true, ...failure });
    await tab.events.submit(ordinaryEvent());
    assert.equal(tab.closeCalls(), 0);
    assert.equal(tab.timers.length, 0);
    assert.deepEqual(tab.navigations, []);
    assert.equal(tab.error.hidden, false);
    assert.match(tab.error.textContent, /Could not cancel Add Server/);
    await tab.events.submit(ordinaryEvent());
    assert.equal(tab.requests.length, 2, 'failed cancellation must remain retryable');
  }
});

test('first-run pages install no Add Server exit handlers', () => {
  loadSetupOperation({ document: { querySelector: () => null } });
});
