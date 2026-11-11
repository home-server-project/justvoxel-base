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
function fixture({ local = true, bedrock = false, search = '', clipboard, nordvpn = true } = {}) {
  const guide = new Element();
  const domain = new Element();
  const print = new Element();
  const invite = new Element();
  const invitation = new Element();
  invitation.open = false;
  invitation.showModal = () => { invitation.open = true; };
  invitation.close = () => { invitation.open = false; };
  const invitationText = new Element();
  const copy = new Element();
  const close = new Element();
  const copyStatus = new Element();
  const summary = new Element();
  const java = new Element();
  const domainAddresses = Array.from({ length: bedrock ? 2 : 1 }, () => new Element());
  const ids = ['custom', 'local', 'tailscale', 'netbird', 'playit', 'nordvpn'];
  const switches = ids.map(id => new Element({ connectMethod: id }));
  switches[1].disabled = !local;
  switches[1].checked = local;
  switches[2].disabled = true;
  switches[5].disabled = !nordvpn;
  const cards = ids.map(id => new Element({ connectCard: id, connectName: id === 'nordvpn' ? 'NordVPN Meshnet' : id }));
  const endpoints = (address, javaJoin, port = '19139') => [
    new Element({ connectEndpoint: 'Java', connectJoin: javaJoin }),
    ...(bedrock ? [new Element({ connectEndpoint: 'Bedrock', connectAddress: address, connectPort: port })] : []),
  ];
  const methodEndpoints = [
    endpoints('', ''),
    endpoints('192.168.0.59', '192.168.0.59:25571'),
    endpoints('100.80.0.8', '100.80.0.8:25571'),
    endpoints('100.90.0.9', '100.90.0.9:25571'),
    [new Element({ connectEndpoint: 'Java', connectJoin: 'example.gl.joinmc.link:12345' })],
    endpoints('100.96.0.10', '100.96.0.10:25571'),
  ];
  methodEndpoints[0].forEach(endpoint => { endpoint.querySelector = () => domainAddresses[1]; });
  cards.forEach((card, i) => { card.querySelectorAll = () => methodEndpoints[i]; });
  const page = new Element({ connectServerName: 'Family Survival', javaPort: '25571', ...(bedrock ? { bedrockPort: '19139' } : {}) });
  page.querySelector = selector => ({
    '[data-connect-guide]': guide,
    '#connect-domain': domain,
    '[data-connect-print]': print,
    '[data-connect-summary]': summary,
    '[data-connect-invite]': invite,
    '[data-connect-invitation]': invitation,
    '[data-connect-invitation-text]': invitationText,
    '[data-connect-copy]': copy,
    '[data-connect-invitation-close]': close,
    '[data-connect-copy-status]': copyStatus,
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
    navigator: { clipboard },
    URLSearchParams,
    document: {
      querySelector: () => page,
      body: { classList: { toggle: (name, on) => on ? classes.add(name) : classes.delete(name) } },
    },
    window: { location: { search }, print: () => { prints++; }, addEventListener: (type, fn) => { events[type] = fn; } },
  });
  return { invite, invitation, invitationText, copy, close, copyStatus, methodEndpoints, guide, domain, print, summary, java, domainAddresses, switches, cards, classes, events, prints: () => prints };
}

test('local defaults to selected; print-ready guide includes exactly the selected methods', () => {
  const f = fixture();
  assert.equal(f.domain.disabled, true);
  assert.equal(f.guide.hidden, false);
  assert.equal(f.print.disabled, false);
  assert.equal(f.invite.disabled, false);
  assert.equal(f.summary.textContent, 'local');
  assert.deepEqual(f.cards.map(card => card.hidden), [true, false, true, true, true, true]);
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
  assert.equal(f.invite.disabled, true);
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
  assert.equal(f.invite.disabled, true);
  f.invite.listeners.click();
  assert.equal(f.invitation.open, false);
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
    assert.equal(f.invite.disabled, true, value);
    f.invite.listeners.click();
    assert.equal(f.invitation.open, false);
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
  for (const control of ['.connect-controls', '.connect-page button', '.connect-page input', '.connect-page select', '.connect-invitation', '.connect-invite-controls']) assert.ok(print.includes(control));
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
  assert.match(template, /class="connect-title-row title-row"><h1 id="connect-title">How to connect<\/h1><div class="connect-server-lane">/);
  assert.ok(!template.includes('>Server<'));
  assert.match(template, /<label for="connect-domain">Custom domain<\/label>\s*<input id="connect-domain"[^>]+disabled>\s*<input id="connect-custom" type="checkbox" role="switch"/);
  assert.ok(template.includes('Hostname only — no https:// or port.'));
  assert.ok(template.includes('Choose how your friends will join, then save the connection guide to share.'));
  const lane = template.slice(template.indexOf('<div class="connect-actions connect-domain">'), template.indexOf('<div class="connect-invite-controls">'));
  assert.ok(template.indexOf('<div class="connect-methods">') < template.indexOf('<div class="connect-actions connect-domain">'));
  assert.match(lane, /<label for="connect-domain">Custom domain<\/label>\s*<input id="connect-domain"[^>]+>\s*<input id="connect-custom"[^>]+>/);
  assert.match(lane, /<button type="button" data-connect-print disabled>Save to PDF<\/button>/);
  // The helper occupies a separate row; the button ends the four primary controls.
  assert.match(lane, /<input id="connect-custom"[^>]+>\s*{{end}}{{end}}\s*<button[^>]+>Save to PDF<\/button>\s*<small class="muted" id="connect-domain-help">Hostname only — no https:\/\/ or port\.<\/small>\s*<\/div>\s*$/);
  assert.equal((lane.match(/<p[ >]/g) || []).length, 0);
  const method = template.slice(template.indexOf('{{define "connect-method-control"}}'));
  assert.match(method, /<div class="connect-method">\s*<label[^>]+>[\s\S]*{{.Name}}<\/label>\s*<input[^>]+role="switch"[^>]+>\s*<\/div>/);
  assert.equal((method.match(/alt="" aria-hidden="true"/g) || []).length, 5);
  assert.ok(!lane.includes('<img'));
  assert.ok(!/<small|<span/.test(method));
  const grid = template.slice(template.indexOf('<div class="connect-methods">'), template.indexOf('<div class="connect-actions connect-domain">'));
  const order = Array.from(grid.matchAll(/eq \.ID "(local|playit|tailscale|netbird|nordvpn)"/g), match => match[1]);
  assert.deepEqual(order, ['local', 'playit', 'tailscale', 'netbird', 'nordvpn']);
  assert.match(template, /class="connect-print-logo" src="\/static\/justvoxel-login-logo.png"/);
  const login = readFileSync(path.join(__dirname, '../webui/internal/server/templates/login.html'), 'utf8');
  assert.ok(login.includes('src="/static/justvoxel-login-logo.png"'));
  assert.match(template, /connect-guide-top-row"><img[^>]+><p>{{.ConnectionGuide.Server}}<\/p>/);
  assert.ok(!template.slice(0, template.indexOf('<article class="connect-guide"')).includes('<img'));
  assert.equal((template.match(/<img /g) || []).length, 6);
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
  assert.match(screen, /\.connect-methods\{display:grid;gap:10px\}/);
  assert.match(screen, /\.connect-method-row-primary\{grid-template-columns:repeat\(2,minmax\(0,1fr\)\)\}/);
  assert.match(screen, /\.connect-method-row-private\{grid-template-columns:repeat\(3,minmax\(0,1fr\)\)\}/);
  assert.match(screen, /@media\(max-width:640px\)/);
  assert.match(screen, /\.connect-method-row-primary,\.connect-method-row-private,\.connect-cards\{grid-template-columns:1fr\}/);
  assert.match(screen, /\.connect-actions button\{grid-column:4;grid-row:1;white-space:nowrap\}/);
  // Colors are supplied by app.css classes/tokens, and switches have no local painting.
  assert.ok(!/#[a-f0-9]{3,8}\b/i.test(screen));
  assert.ok(!/appearance:|::?before|::?after|connect-switch/.test(css));
  assert.match(app, /input\[type=checkbox\]:not\(\.destructive-confirm-toggle\):not\(\.storage-confirm-toggle\)\{appearance:none;/);
  assert.ok(!source.includes('Your guide is ready.'));
  assert.ok(!source.includes('Enter a valid custom hostname to preview or save the guide.'));
});

test('invitation uses the current selected endpoints and friendly name only', () => {
  const f = fixture({ bedrock: true });
  assert.equal(f.invitation.open, false);
  f.invite.listeners.click();
  assert.equal(f.invitation.open, true);
  assert.equal(f.invitationText.value, 'Join my Minecraft Family Survival on JustVoxel\n\nlocal\nJava: 192.168.0.59:25571\nBedrock: 192.168.0.59:19139');
  assert.ok(!f.invitationText.value.includes('Join Family Survival on Minecraft'));
  for (const absent of ['jv-', 'password', 'token', '/host/', 'https://', 'PS5', 'backup', 'tailscale', 'netbird', 'playit']) {
    assert.ok(!f.invitationText.value.includes(absent), absent);
  }
  f.close.listeners.click();
  assert.equal(f.invitation.open, false);
  f.switches[2].disabled = false;
  f.switches[2].checked = true;
  f.switches[4].checked = true;
  f.invite.listeners.click();
  assert.equal(f.invitationText.value, 'Join my Minecraft Family Survival on JustVoxel\n\nlocal\nJava: 192.168.0.59:25571\nBedrock: 192.168.0.59:19139\n\ntailscale\nJava: 100.80.0.8:25571\nBedrock: 100.80.0.8:19139\n\nplayit\nJava: example.gl.joinmc.link:12345');
  f.close.listeners.click();
  f.switches[1].checked = false;
  f.switches[2].disabled = true;
  f.invite.listeners.click();
  assert.equal(f.invitationText.value, 'Join my Minecraft Family Survival on JustVoxel\n\nplayit\nJava: example.gl.joinmc.link:12345');
  f.close.listeners.click();
  f.switches[4].checked = false;
  f.invite.listeners.click();
  assert.equal(f.invite.disabled, true);
  assert.equal(f.invitation.open, false);
  assert.equal(f.invitationText.value, '');
});

test('custom invitation reuses validated hostname and configured Java and Bedrock ports', () => {
  const f = fixture({ bedrock: true });
  f.switches[0].checked = true;
  f.switches[1].checked = false;
  for (const hostname of [' minecraft.Example.com ', 'renamed.example']) {
    f.domain.value = hostname;
    f.domain.listeners.input();
    f.invite.listeners.click();
    assert.equal(f.invitationText.value, `Join my Minecraft Family Survival on JustVoxel\n\ncustom\nJava: ${hostname.trim()}:25571\nBedrock: ${hostname.trim()}:19139`);
    f.close.listeners.click();
  }
});

test('Bedrock uses the corresponding public port and brackets IPv6 without inventing a port', () => {
  const f = fixture({ bedrock: true });
  f.switches[1].checked = false;
  f.switches[4].checked = true;
  f.methodEndpoints[4].push(new Element({ connectEndpoint: 'Bedrock', connectAddress: 'bedrock.playit.gg', connectPort: '50123' }));
  f.invite.listeners.click();
  assert.ok(f.invitationText.value.includes('Bedrock: bedrock.playit.gg:50123'));
  assert.ok(!f.invitationText.value.includes('19139'));
  f.close.listeners.click();
  f.methodEndpoints[4][1].dataset.connectAddress = '2001:db8::8';
  f.invite.listeners.click();
  assert.ok(f.invitationText.value.includes('Bedrock: [2001:db8::8]:50123'));
  f.close.listeners.click();
  f.methodEndpoints[4][1].dataset.connectPort = '';
  f.invite.listeners.click();
  assert.ok(!f.invitationText.value.includes('Bedrock:'));
  f.close.listeners.click();
  f.methodEndpoints[4].shift();
  f.switches[4].listeners.change();
  assert.equal(f.invite.disabled, true);
  f.invite.listeners.click();
  assert.equal(f.invitation.open, false);
});

test('direct invite opens only for invite=1 with usable current data', () => {
  assert.equal(fixture({ search: '?server=jv-family1&invite=1' }).invitation.open, true);
  assert.equal(fixture({ local: false, search: '?server=jv-family1&invite=1' }).invitation.open, false);
  for (const search of ['', '?server=jv-family1', '?invite=0', '?invite=true']) {
    assert.equal(fixture({ search }).invitation.open, false, search);
  }
});

test('clipboard success confirms copying; failure or missing API preserves selectable text', async () => {
  let copied;
  const success = fixture({ clipboard: { writeText: async text => { copied = text; } } });
  success.invite.listeners.click();
  await success.copy.listeners.click();
  assert.equal(copied, success.invitationText.value);
  assert.equal(success.copyStatus.textContent, 'Copied.');
  for (const clipboard of [undefined, { writeText: async () => { throw new Error('denied'); } }]) {
    const f = fixture({ clipboard });
    f.invite.listeners.click();
    const text = f.invitationText.value;
    await f.copy.listeners.click();
    assert.equal(f.copyStatus.textContent, 'Copy unavailable. Select the invitation text and copy it manually.');
    assert.equal(f.invitationText.value, text);
    assert.equal(f.invitation.open, true);
    f.close.listeners.click();
    f.invite.listeners.click();
    assert.equal(f.copyStatus.textContent, '');
  }
});

test('invitation is a native dialog with selectable plain text outside the single printable guide', () => {
  const template = readFileSync(path.join(__dirname, '../webui/internal/server/templates/how_to_connect.html'), 'utf8');
  assert.match(template, /<dialog class="connect-invitation"[^>]+aria-labelledby="connect-invitation-title">/);
  assert.match(template, /<textarea[^>]+data-connect-invitation-text readonly[^>]*><\/textarea>/);
  assert.match(template, /data-connect-copy>Copy invitation<\/button>/);
  assert.match(template, /data-connect-invitation-close>Close<\/button>/);
  assert.ok(template.indexOf('</dialog>') < template.indexOf('<article class="connect-guide"'));
  assert.ok(!source.slice(source.indexOf('  const guide =')).includes('fetch('));
  for (const forbidden of ['execCommand', 'XMLHttpRequest', 'navigator.share', 'sendBeacon', 'WebSocket', 'localStorage', 'sessionStorage', 'innerHTML']) {
    assert.ok(!source.includes(forbidden), forbidden);
  }
});

function selectorFixture({ search = '', popover = true, inventory = { servers: [] }, failure = false, ok = true } = {}) {
  const selector = new Element({ selectedServer: 'jv-family1' });
  const summary = new Element();
  summary.textContent = 'Family Survival';
  summary.children = [];
  summary.prepend = icon => summary.children.unshift(icon);
  summary.focus = () => { summary.focused = true; };
  summary.getBoundingClientRect = () => ({ width: 160, left: 900, bottom: 100 });
  const menu = new Element();
  menu.style = {};
  menu.matches = () => menu.popoverOpen === true;
  if (popover) {
    menu.showPopover = () => { menu.popoverOpen = true; };
    menu.hidePopover = () => { menu.popoverOpen = false; };
  }
  const options = ['jv-family1', 'jv-kids001', 'jv-noicon1'].map(serverId => {
    const option = new Element({ serverId });
    option.textContent = serverId === 'jv-family1' ? 'Family Survival' : 'Kids';
    option.children = [];
    option.prepend = icon => option.children.unshift(icon);
    return option;
  });
  menu.querySelectorAll = () => options;
  selector.querySelector = query => query === 'summary' ? summary : menu;
  const page = { querySelector: query => query === '.server-selector' ? selector : null };
  const location = { search };
  const requests = [];
  const events = {};
  vm.runInNewContext(source, {
    document: {
      querySelector: () => page,
      createElement: tag => {
        assert.equal(tag, 'img');
        const icon = new Element();
        icon.remove = () => {
          for (const element of [summary, ...options]) element.children = element.children.filter(child => child !== icon);
        };
        return icon;
      },
    },
    window: { location, innerWidth: 1000, innerHeight: 600, addEventListener: (type, fn) => { events[type] = fn; } },
    URLSearchParams,
    fetch: async (url, init) => {
      requests.push({ url, init });
      if (failure) throw new Error('inventory unavailable');
      return { ok, json: async () => inventory };
    },
  });
  return { selector, summary, menu, options, location, requests, events };
}

test('standalone server selector navigates by immutable ID and preserves only invite=1', () => {
  for (const [search, expected] of [
    ['', '/minecraft/how-to-connect?server=jv-kids001'],
    ['?server=jv-family1&invite=1&domain=unsafe&other=discard', '/minecraft/how-to-connect?server=jv-kids001&invite=1'],
    ['?invite=0&other=discard', '/minecraft/how-to-connect?server=jv-kids001'],
  ]) {
    const f = selectorFixture({ search });
    f.selector.open = true;
    f.selector.listeners.toggle();
    let navigated = false;
    Object.defineProperty(f.location, 'href', { set(value) {
      navigated = true;
      assert.equal(f.selector.open, false);
      assert.equal(f.menu.popoverOpen, false);
      assert.equal(value, expected);
    } });
    f.options[1].listeners.click();
    assert.equal(navigated, true);
  }
  for (const id of ['', 'Purpur', 'jv-bad', 'jv-ABCDEF', '../server', 'jv-kids001&invite=1']) {
    const f = selectorFixture();
    f.options[1].dataset.serverId = id;
    f.options[1].listeners.click();
    assert.equal(f.location.href, undefined, id);
  }
});

test('selector menu follows summary geometry, supports fallback and closes on Escape and print', () => {
  for (const popover of [true, false]) {
    const f = selectorFixture({ popover });
    f.selector.open = true;
    f.selector.listeners.toggle();
    assert.equal(popover ? f.menu.popoverOpen : !f.menu.hidden, true);
    assert.equal(f.menu.style.width, '160px');
    assert.equal(f.menu.style.left, '836px');
    assert.equal(f.menu.style.top, '103px');
    assert.equal(f.menu.style.maxHeight, '240px');
    let prevented = false;
    f.selector.listeners.keydown({ key: 'Escape', preventDefault() { prevented = true; } });
    assert.equal(prevented, true);
    assert.equal(f.selector.open, false);
    assert.equal(f.summary.focused, true);
    assert.equal(popover ? f.menu.popoverOpen : !f.menu.hidden, false);
    f.selector.open = true;
    f.selector.listeners.toggle();
    f.events.beforeprint();
    assert.equal(f.selector.open, false);
    assert.equal(popover ? f.menu.popoverOpen : !f.menu.hidden, false);
    if (popover) {
      f.selector.open = true;
      f.menu.popoverOpen = false;
      f.menu.listeners.toggle();
      assert.equal(f.selector.open, false);
    }
  }
});

test('authoritative inventory enriches only matching icons without changing template names or choices', async () => {
  const f = selectorFixture({ inventory: { servers: [
    { id: 'jv-kids001', name: 'Untrusted replacement', software: 'purpur', software_icon: '/static/kids.svg' },
    { id: 'jv-family1', name: '<img onerror=alert(1)>', software: 'paper', software_icon: '/static/family.svg' },
    { id: 'jv-absent1', name: 'Family Survival', software_icon: '/static/absent.svg' },
    { id: 'jv-noicon1', name: 'Kids', software: 'purpur' },
  ] } });
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(f.requests.length, 1);
  assert.equal(f.requests[0].url, '/api/minecraft/workspace/selection?selection=none');
  assert.equal(f.requests[0].init.credentials, 'same-origin');
  assert.equal(f.requests[0].init.cache, 'no-store');
  assert.equal(f.summary.textContent, 'Family Survival');
  assert.equal(f.options[1].textContent, 'Kids');
  assert.equal(f.options.length, 3);
  for (const [element, src] of [[f.summary, '/static/family.svg'], [f.options[0], '/static/family.svg'], [f.options[1], '/static/kids.svg']]) {
    assert.equal(element.children.length, 1);
    const icon = element.children[0];
    assert.equal(icon.className, 'server-selector-icon');
    assert.equal(icon.alt, '');
    assert.equal(icon.src, src);
    icon.listeners.error();
    assert.equal(element.children.length, 0);
    assert.ok(element.textContent);
  }
  assert.equal(f.options[2].children.length, 0);
});

test('inventory failure or malformed inventory leaves text-only navigation usable', async () => {
  for (const config of [{ failure: true }, { ok: false }, { inventory: {} }]) {
    const f = selectorFixture(config);
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(f.summary.textContent, 'Family Survival');
    assert.equal(f.summary.children.length, 0);
    assert.equal(f.options[1].disabled, false);
    f.options[1].listeners.click();
    assert.equal(f.location.href, '/minecraft/how-to-connect?server=jv-kids001');
  }
});

test('invitation keeps Close secondary before primary Copy; selector reuses workspace presentation only', () => {
  const template = readFileSync(path.join(__dirname, '../webui/internal/server/templates/how_to_connect.html'), 'utf8');
  const css = readFileSync(path.join(__dirname, '../webui/internal/server/static/how-to-connect.css'), 'utf8');
  assert.match(template, /class="secondary" data-connect-invitation-close>Close<\/button><button type="button" data-connect-copy>Copy invitation/);
  const lane = template.split('<div class="connect-server-lane">')[1].split('</details>')[0];
  assert.match(lane, /<span id="connect-server-label">Server:<\/span>\s*<details class="server-selector"/);
  assert.match(lane, /data-selected-server="{{.SelectedMinecraftID}}"/);
  assert.match(lane, /<summary[^>]+aria-labelledby="connect-server-label connect-server-name"/);
  assert.match(lane, /{{else}}Choose server{{end}}/);
  assert.match(lane, /class="server-selector-menu" popover="auto"/);
  assert.match(lane, /{{range .ConnectionServers}}<button[^>]+class="server-selector-option" data-server-id="{{.ID}}"/);
  assert.match(lane, /<span>{{.Name}}<\/span>/);
  assert.ok(template.includes('(not .ConnectionServers)'));
  assert.ok(template.includes('Minecraft is not configured yet'));
  assert.doesNotMatch(template, /<select|data-workspace-window|data-workspace-refresh|data-workspace-close|Select server|src="\/static\/app.js"/);
  assert.match(css, /\.connect-server-lane\{display:flex;align-items:center;gap:[^;]+;min-width:0;max-width:100%/);
  assert.doesNotMatch(css, /data-server-selector-slot|\.server-selector(?:[>{. :]|-)/);
  for (const forbidden of ['localStorage', 'sessionStorage', 'justVoxelSelectServer', 'workspaceServerSelections', 'data-server-workspace', 'data-workspace', 'innerHTML']) {
    assert.ok(!source.includes(forbidden), forbidden);
  }
  assert.equal((source.match(/fetch\(/g) || []).length, 1);
});

test('NordVPN participates in selected preview, PDF and invitation through generic method handling', () => {
  for (const bedrock of [false, true]) {
    const f = fixture({ bedrock });
    assert.equal(f.cards[5].hidden, true);
    assert.ok(!f.summary.textContent.includes('NordVPN'));
    f.invite.listeners.click();
    assert.ok(!f.invitationText.value.includes('NordVPN'));
    f.close.listeners.click();
    f.switches[1].checked = false;
    f.switches[5].checked = true;
    f.switches[5].listeners.change();
    assert.equal(f.guide.hidden, false);
    assert.deepEqual(f.cards.map(card => card.hidden), [true, true, true, true, true, false]);
    assert.equal(f.summary.textContent, 'NordVPN Meshnet');
    f.events.beforeprint();
    assert.equal(f.classes.has('connect-print-ready'), true);
    f.print.listeners.click();
    assert.equal(f.prints(), 1);
    f.invite.listeners.click();
    assert.equal(f.invitationText.value, 'Join my Minecraft Family Survival on JustVoxel\n\nNordVPN Meshnet\nJava: 100.96.0.10:25571' + (bedrock ? '\nBedrock: 100.96.0.10:19139' : ''));
    f.close.listeners.click();
    f.switches[5].checked = false;
    f.switches[1].checked = true;
    f.switches[5].listeners.change();
    assert.equal(f.cards[5].hidden, true);
    assert.equal(f.summary.textContent, 'local');
    f.invite.listeners.click();
    assert.ok(!f.invitationText.value.includes('NordVPN'));
  }
});

test('unavailable NordVPN cannot contribute to preview, PDF or invitation even if checked', () => {
  const f = fixture({ local: false, nordvpn: false });
  assert.equal(f.switches[5].disabled, true);
  f.switches[5].checked = true;
  f.switches[5].listeners.change();
  assert.equal(f.cards[5].hidden, true);
  assert.equal(f.guide.hidden, true);
  assert.equal(f.summary.textContent, '');
  assert.equal(f.print.disabled, true);
  f.events.beforeprint();
  assert.equal(f.classes.has('connect-print-ready'), false);
  f.print.listeners.click();
  assert.equal(f.prints(), 0);
  f.invite.listeners.click();
  assert.equal(f.invitation.open, false);
  assert.equal(f.invitationText.value, '');
});

test('private method row and PS5 warning include all three private networks', () => {
  const template = readFileSync(path.join(__dirname, '../webui/internal/server/templates/how_to_connect.html'), 'utf8');
  const privateRow = template.split('<div class="connect-method-row connect-method-row-private">')[1].split('</div>')[0];
  const ids = Array.from(privateRow.matchAll(/eq \.ID "([^"]+)"/g), match => match[1]);
  assert.deepEqual(ids, ['tailscale', 'netbird', 'nordvpn']);
  assert.ok(template.includes('Tailscale, NetBird and NordVPN Meshnet still require access to the relevant private network on the connecting device. These PS5 methods do not grant that access.'));
  const css = readFileSync(path.join(__dirname, '../webui/internal/server/static/how-to-connect.css'), 'utf8');
  const mobile = css.slice(css.indexOf('@media(max-width:640px)'), css.indexOf('.connect-invite-controls'));
  assert.match(mobile, /\.connect-method-row-primary,\.connect-method-row-private,\.connect-cards\{grid-template-columns:1fr\}/);
});
