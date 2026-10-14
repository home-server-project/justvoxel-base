const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { test } = require('node:test');
const path = require('node:path');
const vm = require('node:vm');
const source = readFileSync(path.join(__dirname, '../webui/internal/server/static/how-to-connect.js'), 'utf8');

class Element {
  constructor(dataset = {}) {
    this.dataset = dataset;
    this.listeners = {};
    this.disabled = false;
    this.checked = false;
    this.hidden = false;
    this.value = '';
    this.textContent = '';
  }
  addEventListener(type, fn) { this.listeners[type] = fn; }
  setAttribute(name, value) { this[name] = value; }
  setCustomValidity(value) { this.validity = value; }
  set innerHTML(_) { throw new Error('unsafe HTML insertion'); }
}
function fixture({ local = true, bedrock = false } = {}) {
  const guide = new Element();
  const domain = new Element();
  const print = new Element();
  const summary = new Element();
  const java = new Element();
  const domainAddresses = Array.from({ length: bedrock ? 2 : 1 }, () => new Element());
  const ids = ['custom', 'local', 'tailscale', 'netbird', 'playit'];
  const switches = ids.map(id => new Element({ connectMethod: id }));
  switches[1].disabled = !local;
  switches[1].checked = local;
  switches[2].disabled = true;
  const cards = ids.map(id => new Element({ connectCard: id, connectName: id }));
  const page = new Element({ javaPort: '25571', ...(bedrock ? { bedrockPort: '19139' } : {}) });
  page.querySelector = selector => ({
    '[data-connect-guide]': guide,
    '#connect-domain': domain,
    '[data-connect-print]': print,
    '[data-connect-summary]': summary,
    '[data-connect-domain-java]': java,
  })[selector];
  page.querySelectorAll = selector => ({
    '[data-connect-method]': switches,
    '[data-connect-card]': cards,
    '[data-connect-domain-address]': domainAddresses,
  })[selector];
  const classes = new Set();
  const events = {};
  let prints = 0;
  vm.runInNewContext(source, {
    document: {
      querySelector: () => page,
      body: { classList: { toggle: (name, on) => on ? classes.add(name) : classes.delete(name) } },
    },
    window: { print: () => { prints++; }, addEventListener: (type, fn) => { events[type] = fn; } },
  });
  return { guide, domain, print, summary, java, domainAddresses, switches, cards, classes, events, prints: () => prints };
}

test('local defaults to selected; print-ready guide includes exactly the selected methods', () => {
  const f = fixture();
  assert.equal(f.domain.disabled, true);
  assert.equal(f.guide.hidden, false);
  assert.equal(f.print.disabled, false);
  assert.equal(f.summary.textContent, 'local');
  assert.deepEqual(f.cards.map(card => card.hidden), [true, false, true, true, true]);
  f.switches[3].checked = true;
  f.switches[3].listeners.change();
  assert.equal(f.summary.textContent, 'local · netbird');
  f.print.listeners.click();
  assert.equal(f.prints(), 1);
  f.switches[1].checked = false;
  f.switches[3].checked = false;
  f.switches[1].listeners.change();
  assert.equal(f.guide.hidden, true);
  assert.equal(f.print.disabled, true);
  assert.equal(f.classes.has('connect-print-ready'), false);
  f.print.listeners.click();
  assert.equal(f.prints(), 1);
});

test('unavailable methods cannot contribute even if checked; no address is invented', () => {
  const f = fixture({ local: false });
  f.switches[1].checked = true;
  f.switches[2].checked = true;
  f.switches[2].listeners.change();
  assert.equal(f.guide.hidden, true);
  assert.equal(f.summary.textContent, '');
  assert.equal(f.print.disabled, true);
});

test('custom domain immediately updates safe text and authoritative Java port', () => {
  const f = fixture({ bedrock: true });
  f.switches[0].checked = true;
  f.switches[0].listeners.change();
  assert.equal(f.domain.disabled, false);
  assert.equal(f.guide.hidden, true);
  assert.equal(f.print.disabled, true);
  f.domain.value = ' minecraft.Example.com ';
  f.domain.listeners.input();
  assert.equal(f.guide.hidden, false);
  assert.equal(f.print.disabled, false);
  assert.equal(f.java.textContent, 'minecraft.Example.com:25571');
  assert.deepEqual(f.domainAddresses.map(node => node.textContent), ['minecraft.Example.com', 'minecraft.Example.com']);
  assert.equal(f.summary.textContent, 'custom · local');
  f.print.listeners.click();
  assert.equal(f.prints(), 1);
});

test('invalid enabled custom domain blocks the guide, print button and browser-menu printing', () => {
  const f = fixture();
  f.switches[0].checked = true;
  for (const value of ['', 'https://example.com', 'example.com/path', 'user:password@example.com', '<img src=x onerror=alert(1)>', 'example.com:25565', 'a..com', '-bad.com', 'bad-.com', '192.168.1.2', 'javascript:alert(1)', 'x'.repeat(64) + '.com']) {
    f.domain.value = value;
    f.domain.listeners.input();
    assert.equal(f.print.disabled, true, value);
    assert.equal(f.guide.hidden, true, value);
    assert.equal(f.domain['aria-invalid'], 'true', value);
    assert.equal(f.java.textContent, '', value);
    f.events.beforeprint();
    assert.equal(f.classes.has('connect-print-ready'), false, value);
    f.print.listeners.click();
  }
  assert.equal(f.prints(), 0);
  f.switches[0].checked = false;
  f.switches[0].listeners.change();
  assert.equal(f.domain.disabled, true);
  assert.equal(f.print.disabled, false);
  assert.equal(f.summary.textContent, 'local');
});

test('unconfigured page without a guide needs no client configuration', () => {
  vm.runInNewContext(source, { document: { querySelector: () => ({ querySelector: () => null }) } });
});

test('print rules hide all editing and unselected cards and block invalid browser printing', () => {
  const css = readFileSync(path.join(__dirname, '../webui/internal/server/static/how-to-connect.css'), 'utf8');
  assert.match(css, /\.connect-page \[hidden\]\{display:none!important\}/);
  const print = css.slice(css.indexOf('@media print'));
  for (const control of ['.connect-controls', '.connect-page button', '.connect-page input', '.connect-page select']) assert.ok(print.includes(control));
  assert.match(print, /\.connect-page\.connect-print-ready \.connect-guide\{display:block\}/);
  assert.match(print, /\.connect-page:not\(\.connect-print-ready\) \.connect-guide\{display:none!important\}/);
  assert.match(print, /\.connect-page:not\(\.connect-print-ready\) \.connect-print-blocked\{display:block!important\}/);
});

test('approved page layout uses a full-width action lane and the same guide and login logo for preview and print', () => {
  const template = readFileSync(path.join(__dirname, '../webui/internal/server/templates/how_to_connect.html'), 'utf8');
  const css = readFileSync(path.join(__dirname, '../webui/internal/server/static/how-to-connect.css'), 'utf8');
  const app = readFileSync(path.join(__dirname, '../webui/internal/server/static/app.css'), 'utf8');
  const screen = css.slice(0, css.indexOf('@media print'));
  const print = css.slice(css.indexOf('@media print'));
  for (const absent of ['connect-navigation', '/static/justvoxel-logo.png', 'Minecraft Friends Connection Guide', 'LET’S PLAY TOGETHER', 'Enter your hostname', 'Hostname / domain', 'connect-switch', '{{.Status}}', 'data-connect-message', 'Save as PDF', 'Preview the guide below']) {
    assert.ok(!template.includes(absent), absent);
  }
  assert.match(template, /class="connect-controls panel"/);
  assert.match(template, /class="connect-eyebrow eyebrow"/);
  assert.match(template, /class="connect-title-row title-row"><h1 id="connect-title">How to connect<\/h1><select class="connect-server" aria-label="Server">/);
  assert.ok(!template.includes('>Server<'));
  assert.match(template, /<label for="connect-domain">Custom domain<\/label>\s*<input id="connect-domain"[^>]+disabled>\s*<input id="connect-custom" type="checkbox" role="switch"/);
  assert.ok(template.includes('Hostname only — no https:// or port.'));
  assert.ok(template.includes('Choose how your friends will join, then save the connection guide to share.'));
  const lane = template.slice(template.indexOf('<div class="connect-actions connect-domain">'), template.indexOf('<noscript>'));
  assert.ok(template.indexOf('<div class="connect-methods">') < template.indexOf('<div class="connect-actions connect-domain">'));
  assert.match(lane, /<label for="connect-domain">Custom domain<\/label>\s*<input id="connect-domain"[^>]+>\s*<input id="connect-custom"[^>]+>/);
  assert.match(lane, /<button type="button" data-connect-print disabled>Save to PDF<\/button>/);
  // The helper occupies a separate row; the button ends the four primary controls.
  assert.match(lane, /<input id="connect-custom"[^>]+>\s*{{end}}{{end}}\s*<button[^>]+>Save to PDF<\/button>\s*<small class="muted" id="connect-domain-help">Hostname only — no https:\/\/ or port\.<\/small>\s*<\/div>\s*$/);
  assert.equal((lane.match(/<p[ >]/g) || []).length, 0);
  const method = template.slice(template.indexOf('{{define "connect-method-control"}}'));
  assert.match(method, /<div class="connect-method">\s*<label[^>]+>{{.Name}}<\/label>\s*<input[^>]+role="switch"[^>]+>\s*<\/div>/);
  assert.ok(!/<small|<span/.test(method));
  const grid = template.slice(template.indexOf('<div class="connect-methods">'), template.indexOf('<div class="connect-actions connect-domain">'));
  const order = Array.from(grid.matchAll(/eq \.ID "(local|playit|tailscale|netbird)"/g), match => match[1]);
  assert.deepEqual(order, ['local', 'playit', 'tailscale', 'netbird']);
  assert.match(template, /class="connect-print-logo" src="\/static\/justvoxel-login-logo.png"/);
  const login = readFileSync(path.join(__dirname, '../webui/internal/server/templates/login.html'), 'utf8');
  assert.ok(login.includes('src="/static/justvoxel-login-logo.png"'));
  assert.match(template, /connect-guide-top-row"><img[^>]+><p>{{.ConnectionGuide.Server}}<\/p>/);
  assert.ok(!template.slice(0, template.indexOf('<article class="connect-guide"')).includes('<img'));
  assert.equal((template.match(/<img /g) || []).length, 1);
  assert.match(template, /<h1>JustVoxel Connection Guide<\/h1>/);
  assert.match(screen, /\.connect-print-logo\{display:block;width:145px;height:auto\}/);
  assert.match(screen, /\.connect-guide-top-row\{display:flex;align-items:center;justify-content:space-between;/);
  assert.match(print, /\.connect-print-logo\{display:block;width:145px;height:auto\}/);
  assert.match(screen, /\.connect-title-row\{align-items:center\}/);
  assert.match(screen, /\.connect-actions\{display:grid;width:100%;grid-template-columns:auto minmax\(0,1fr\) auto auto;/);
  assert.ok(!screen.includes('max-width:320px'));
  assert.ok(!screen.includes('justify-content:start'));
  assert.match(screen, /\.connect-domain input\[type=text\]\{width:100%;max-width:none;/);
  assert.match(screen, /\.connect-domain small\{grid-column:2;grid-row:2;/);
  assert.match(screen, /\.connect-domain small\{grid-column:1;grid-row:3\}/);
  const guideRules = Array.from(screen.matchAll(/([^{}]+)\{([^{}]*)\}/g))
    .filter(([, selectors]) => /\.connect-guide(?:[\s,{:]|$)/.test(selectors));
  assert.ok(guideRules.length > 0);
  for (const [, selectors, declarations] of guideRules) {
    assert.ok(!/display\s*:\s*none\b/.test(declarations), selectors);
  }
  assert.match(template, /<article class="connect-guide" data-connect-guide hidden>/);
  assert.equal((template.match(/data-connect-guide/g) || []).length, 1);
  assert.match(source, /guide\.hidden = !ready;/);
  assert.match(source, /if \(update\(\)\) window\.print\(\);/);
  const frame = screen.match(/\.connect-method\{([^}]+)\}/)[1];
  assert.ok(frame.includes('border:1px solid var(--jv-border)'));
  assert.ok(frame.includes('background:var(--jv-surface-sunken)'));
  assert.ok(frame.includes('border-radius:var(--jv-radius)'));
  assert.match(screen, /\.connect-methods\{display:grid;grid-template-columns:repeat\(2,minmax\(0,1fr\)\);/);
  assert.match(screen, /@media\(max-width:640px\)/);
  assert.match(screen, /\.connect-methods,\.connect-cards\{grid-template-columns:1fr\}/);
  assert.match(screen, /\.connect-actions button\{grid-column:4;grid-row:1;white-space:nowrap\}/);
  // Colors are supplied by app.css classes/tokens, and switches have no local painting.
  assert.ok(!/#[a-f0-9]{3,8}\b/i.test(screen));
  assert.ok(!/appearance:|::?before|::?after|connect-switch/.test(css));
  assert.match(app, /input\[type=checkbox\]:not\(\.destructive-confirm-toggle\):not\(\.storage-confirm-toggle\)\{appearance:none;/);
  assert.ok(!source.includes('Your guide is ready.'));
  assert.ok(!source.includes('Enter a valid custom hostname to preview or save the guide.'));
});
