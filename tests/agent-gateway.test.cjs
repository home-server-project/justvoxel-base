'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const vm = require('node:vm');
const base = `${__dirname}/../`;
const js = readFileSync(`${base}webui/internal/server/static/agent-gateway.js`, 'utf8');
const header = readFileSync(`${base}webui/internal/server/templates/header.html`, 'utf8');
const backend = readFileSync(`${base}management/cmd/justvoxel-management-agent/agent_gateway.go`, 'utf8');
const transport = readFileSync(`${base}webui/internal/server/agent_gateway.go`, 'utf8');

class Node {
  constructor(tag, value = '') { this.tagName = tag; this.textContent = value; this.children = []; this.dataset = {}; this.listeners = {}; }
  append(...children) {
    for (const child of children) {
      if (child.parentNode) child.parentNode.children = child.parentNode.children.filter(item => item !== child);
      child.parentNode = this;
      this.children.push(child);
    }
  }
  prepend(...children) { this.children.unshift(...children); children.forEach(child => { child.parentNode = this; }); }
  querySelectorAll(selector) { return this.children.flatMap(child => [...(selector === 'input' && child.tagName === 'input' ? [child] : []), ...child.querySelectorAll(selector)]); }
  closest() { return this.parentNode; }
  focus() { this.focused = true; }
  select() { this.selected = true; this.selectionStart = 0; this.selectionEnd = this.value.length; }
  setSelectionRange(start, end) { this.selectionStart = start; this.selectionEnd = end; }
  remove() { this.removed = true; if (this.parentNode) this.parentNode.children = this.parentNode.children.filter(child => child !== this); }
  replaceChildren(...children) { this.children = children; }
  after(...children) { this.afterChildren = children; }
  addEventListener(name, callback) { this.listeners[name] = callback; }
  setAttribute(name, value) { this[name] = value; }
}

test('workspace reuses movable workspace infrastructure and primary-only Control Center tile', () => {
  assert.match(header, /data-primary-admin-only data-agent-gateway-open/);
  assert.match(header, /class="workspace-window system-workspace-dialog" data-workspace-window="agent-gateway"/);
  assert.match(js, /setupWorkspaceWindow\(dialog, \{onOpen: load\}\)/);
  assert.match(js, /if \(!primary\(\)\) return;/);
  assert.match(transport, /a\.fileBrowserIdentity\(w,\s*r\)/);
  assert.match(backend, /s\.requirePrimaryAdministrator\(w,\s*r\)/);
  assert.doesNotMatch(js, /\.style\.|<style|insertRule|cssText/);
});

test('multiple independent cards select their own immutable connection settings', () => {
  const start = js.indexOf('  const renderCards = ');
  const end = js.indexOf('  const renderSettings = ', start);
  assert.ok(start > 0 && end > start);
  const cards = new Node('div');
  const connections = [
    {id: 'first', name: 'AI agent', purpose: 'AI Agent', level: 1, enabled: true, auth_mode: 'token'},
    {id: 'second', name: 'Automation', purpose: 'API Client', level: 3, enabled: false, auth_mode: 'oauth'},
    {id: 'third', name: '<script>untrusted label</script>', level: 2, enabled: true, auth_mode: 'token'},
  ];
  const selected = [];
  const context = vm.createContext({snapshot: {enabled: true, connections}, cards, busy: false, selectedID: '', renderSettings: c => selected.push(c.id), providerIcon: () => new Node('img'), purposeLabel: value => value || 'Other', levelLabel: value => `Level ${value}`, scopeLabel: () => 'All Minecraft servers', activityLabel: value => value || 'No activity yet', text: (tag, value) => new Node(tag, value)});
  vm.runInContext(js.slice(start, end) + '\nrenderCards();', context);
  assert.equal(cards.children.length, 3);
  for (const [index, card] of cards.children.entries()) {
    assert.equal(card.dataset.gatewayConnection, connections[index].id);
    assert.equal(card.children[0].children[0].textContent, connections[index].name);
    card.children.at(-1).children[0].listeners.click();
    assert.equal(context.selectedID, connections[index].id);
  }
  assert.deepEqual(selected, ['first', 'second', 'third']);
});

test('token is presented once, discarded from response state, and cleared on close', () => {
  const start = js.indexOf('  const showTokenOnce = result => {');
  const end = js.indexOf('  const closeLevel4', start);
  const tokenNode = new Node('pre');
  let shown = 0;
  const context = vm.createContext({tokenNode, tokenDialog: {showModal() { shown++; }}, clearToken() { tokenNode.textContent = ''; }});
  vm.runInContext(js.slice(start, end) + '\nglobalThis.show = showTokenOnce;', context);
  const result = {token: 'placeholder-one-time-value'};
  context.show(result);
  assert.equal(shown, 1);
  assert.equal(tokenNode.textContent, 'placeholder-one-time-value');
  assert.equal(result.token, '');
  context.show(result);
  assert.equal(shown, 1);
  assert.match(js, /addEventListener\('close', clearToken\)/);
  assert.match(js, /addEventListener\('pagehide', clearToken\)/);
  assert.doesNotMatch(js, /localStorage|sessionStorage|console\./);
  assert.match(header, /shown only once/);
  assert.match(header, /<button type="button" class="secondary" data-gateway-token-done>Done<\/button>/);
  assert.match(js, /tokenDialog\.querySelector\('\[data-gateway-token-done\]'\)\.addEventListener\('click', \(\) => tokenDialog\.close\(\)\)/);
  assert.doesNotMatch(header + js, /data-gateway-token-close/);
  assert.match(js, /navigator\.clipboard\.writeText\(tokenNode\.textContent\)/);
});

test('every lower-to-Level-4 change uses the established switch and slider confirmation', () => {
  assert.match(js, /updated\.level === 4 && c\.level !== 4/);
  assert.match(js, /requestLevel4\(payload\)/);
  assert.match(js, /Number\(levelSlider\.value\) !== 100 \|\| !levelToggle\.checked \|\| !primary\(\)/);
  assert.match(js, /levelSlider\.value = '0'; levelToggle\.checked = false/);
  assert.match(js, /acknowledged: true, slider: 100/);
  assert.match(header, /data-gateway-level4-dialog/);
  assert.match(header, /data-destructive-submit disabled>Enable Level 4/);
  assert.match(header, /data-destructive-toggle aria-label="Acknowledge Level 4 system control"/);
  for (const text of ['highly privileged control', 'system services', 'storage', 'network/firewall', 'availability']) assert.ok(header.includes(text));
  assert.match(backend, /request\.Acknowledged && request\.Slider\s*==\s*100/);
});

test('browser CSRF and machine bearer boundaries stay separate', () => {
  assert.match(js, /new URLSearchParams\(\{csrf: content\.dataset\.gatewayCsrf, request: JSON\.stringify\(payload\)\}\)/);
  assert.match(transport, /a\.validCSRF\(r\)/);
  assert.match(transport, /Header\.Get\("Authorization"\)/);
  assert.match(transport, /Stateless:\s*true/);
  assert.match(transport, /client\.GatewayExecute\(ctx,\s*token/);
});

test('workspace exposes enabled/activity instead of fabricated connected sessions', () => {
  assert.match(js, /'Last activity'/);
  assert.match(js, /'Enabled' : 'Disabled'/);
  assert.doesNotMatch(header.slice(header.indexOf('data-agent-gateway-dialog'), header.indexOf('data-workspace-window="terminal"')), />Connected</);
  for (const setting of ['name', 'purpose', 'level', 'enabled', 'expires_at', 'server_scope', 'instance_ids']) assert.ok(header.includes(`name="${setting}"`) || js.includes(`name = '${setting}'`));
  assert.match(header, /plan or subscription/);
  assert.match(header, /does not require an AI provider model API key/);
});

test('Factory Reset revokes before destructive cleanup and reports the effect', () => {
  const reset = readFileSync(`${base}management/cmd/justvoxel-management-agent/admin_factory_reset.go`, 'utf8');
  const worker = reset.slice(reset.indexOf('func executeFactoryReset'));
  assert.ok(worker.indexOf('gatewayFactoryRevoke()') < worker.indexOf('s.retireFactoryResetInstances'));
  assert.match(reset, /external_connections_action/);
  assert.match(reset, /gateway_action/);
  const app = readFileSync(`${base}webui/internal/server/static/app.js`, 'utf8');
  assert.match(app, /All AI\/API connections and credentials/);
});

const workspace = header.slice(header.indexOf('  <dialog class="workspace-window system-workspace-dialog" data-workspace-window="agent-gateway"'), header.indexOf('  <template data-gateway-settings-template>'));
const overview = workspace.slice(workspace.indexOf('<section data-gateway-overview>'), workspace.indexOf('<section class="panel" data-gateway-details'));
const help = workspace.slice(workspace.indexOf('<section data-gateway-help hidden>'));
const form = header.slice(header.indexOf('<template data-gateway-settings-template>'), header.indexOf('<dialog class="minecraft-delete-dialog" data-gateway-level4-dialog'));

test('top lane has Connections, Add, Help and a persistent right-aligned Gateway switch', () => {
  const lane = workspace.match(/<nav class="minecraft-workspace-tabs gateway-navigation"[^>]*>([\s\S]*?)<\/nav>/)[1];
  assert.deepEqual([...lane.matchAll(/<button[^>]*>([^<]+)<\/button>/g)].map(match => match[1]), ['Connections', 'Add', 'Help']);
  assert.match(lane, /data-gateway-connections aria-selected="true"/);
  assert.equal((workspace.match(/data-gateway-enabled/g) || []).length, 1);
  assert.match(lane, /gateway-global-switch/);
  assert.ok(lane.indexOf('data-gateway-help-open') < lane.indexOf('data-gateway-enabled'));
  assert.doesNotMatch(overview + help + form, /data-gateway-enabled/);
  assert.ok(workspace.indexOf('</nav>') < workspace.indexOf('data-gateway-overview'));
  assert.match(overview, /minecraft-server-inventory/);
  assert.match(overview, /minecraft-server-entries" data-gateway-cards/);
  assert.ok(js.includes("if (!snapshot.connections.length) cards.append(text('p', 'No connections yet. Select Add to get started.'))"));
  const css = readFileSync(`${base}webui/internal/server/static/agent-gateway.css`, 'utf8');
  assert.match(css, /gateway-navigation \.gateway-global-switch \{[^}]*margin-left: auto/);
  assert.doesNotMatch(overview, /<form|data-gateway-overview hidden/);
  assert.match(workspace, /data-gateway-details hidden/);
  assert.match(workspace, /data-gateway-help hidden/);
  assert.match(js, /let surface = 'overview'/);
  assert.match(js, /showSurface\('settings'\)/);
  assert.match(js, /showSurface\('help'\)/);
  assert.match(js, /showSurface\('overview'\)/);
});

test('cards contain human summaries while technical fields and endpoints stay off the overview', () => {
  const renderer = js.slice(js.indexOf('  const renderCards ='), js.indexOf('  const renderSettings ='));
  assert.match(renderer, /panel minecraft-server-card/);
  for (const helper of ['purposeLabel', 'levelLabel', 'scopeLabel', 'activityLabel']) assert.ok(renderer.includes(helper));
  assert.doesNotMatch(renderer + overview, /fingerprint|created_at|updated_at|jwks|issuer|audience|expires_at|OpenAPI|data-gateway-mcp|data-gateway-rest/);
  assert.doesNotMatch(renderer, /text\('[^']+', connection\.id\)/);
  for (const endpoint of ['mcp', 'rest', 'openapi', 'metadata']) assert.ok(help.includes(`data-gateway-${endpoint}`));
  assert.match(help, /data-gateway-copy-address="mcp"/);
  assert.match(help, /data-gateway-copy-address="rest"/);
  assert.ok(help.indexOf('Developer integration') < help.indexOf('data-gateway-openapi'));
});

test('help uses compact sections and a comparison table with collapsed developer details', () => {
  for (const section of ['Getting connected', 'Access levels', 'Minecraft scope', 'Security / authorization']) assert.ok(help.includes(`<h3>${section}</h3>`), section);
  assert.match(help, /data-gateway-help-providers/);
  assert.equal((help.match(/data-gateway-help-guide/g) || []).length, 1);
  const table = help.match(/<table[\s\S]*?<\/table>/)[0];
  for (const label of ['Level', 'Name', 'Can do', 'Read Only', 'Basic Management', 'Full Management', 'Full System']) assert.ok(table.includes(`>${label}<`), label);
  assert.equal((table.match(/<tr>/g) || []).length, 5);
  assert.match(help, /No level can manage AI\/API connection permissions, user accounts, or Factory Reset/);
  assert.doesNotMatch(workspace + js, /data-gateway-summary|gateway-summary/);
});

test('provider cards replace the purpose dropdown with one OpenAI choice for ChatGPT and Codex', () => {
  const purposes = [...form.matchAll(/type="radio" name="purpose" value="([^"]+)"/g)].map(match => match[1]);
  assert.deepEqual(purposes, ['OpenAI', 'Claude', 'Grok', 'Gemini', 'GitHub Copilot', 'Other AI / MCP app', 'Automation / REST API']);
  assert.equal((form.match(/<strong>OpenAI<\/strong>/g) || []).length, 1);
  assert.match(form, /<strong>OpenAI<\/strong><small>ChatGPT &amp; Codex<\/small>/);
  assert.doesNotMatch(form, /<select name="purpose"|name="auth_mode"|Connection method|OAuth — Advanced|What do you want to call it/);
  for (const phrase of ['>Name</span>', 'What will use this connection?', 'Access level', 'Which Minecraft servers?', 'All servers', 'Choose servers']) assert.ok(form.includes(phrase), phrase);
  assert.match(form, /class="gateway-provider-grid"/);
  for (const asset of ['openai', 'claude', 'grok', 'googlegemini', 'githubcopilot']) {
    assert.ok(form.includes(`/static/ai-providers/${asset}.svg`));
    const svg = readFileSync(`${base}webui/internal/server/static/ai-providers/${asset}.svg`, 'utf8');
    assert.match(svg, /<svg/);
    assert.doesNotMatch(svg, /<script|(?:href|src)="https?:/);
  }
  assert.doesNotMatch(form, /<img[^>]+src="https?:/);
  const css = readFileSync(`${base}webui/internal/server/static/agent-gateway.css`, 'utf8');
  assert.match(css, /gateway-provider-card:has\(input:checked\)/);
  assert.match(css, /gateway-provider-card:has\(input:focus-visible\)/);
  assert.match(css, /gateway-provider-grid \{ display: flex; flex-wrap: wrap;/);
  assert.match(css, /@media \(max-width: 520px\)/);
});

test('normal creation uses the existing purpose field and provider authentication', () => {
  assert.match(js, /renderSettings\(\{name: '', purpose: 'OpenAI', auth_mode: 'oauth_local', level: 1, enabled: true, all_instances: true, instance_ids: \[\]\}\)/);
  assert.match(js, /purpose: field\('purpose'\)\.value\.trim\(\)/);
  assert.match(js, /auth_mode: c\.id \? c\.auth_mode : providerAuthMode\(field\('purpose'\)\.value\.trim\(\)\)/);
  assert.doesNotMatch(js, /field\('auth_mode'\)/);
  assert.match(js, /querySelector\('\[data-gateway-advanced\]'\)\.hidden = !c\.id/);
});

test('provider presentation uses safe text and a fixed local generic icon for custom purposes', () => {
  const start = js.indexOf('  const providers =');
  const end = js.indexOf('  const activityLabel', start);
  const context = vm.createContext({document: {createElement: tag => new Node(tag)}});
  vm.runInContext(js.slice(start, end) + '\nglobalThis.label = purposeLabel; globalThis.icon = providerIcon;', context);
  assert.equal(context.label('OpenAI'), 'OpenAI · ChatGPT & Codex');
  assert.equal(context.icon('OpenAI').src, '/static/ai-providers/openai.svg');
  for (const purpose of ['Claude', 'Grok', 'Gemini', 'GitHub Copilot', 'Other AI / MCP app', 'Automation / REST API']) assert.equal(context.label(purpose), purpose);
  assert.equal(context.label('AI Agent'), 'AI assistant');
  assert.equal(context.label('API Client'), 'Automation or app');
  assert.equal(context.icon('AI Agent').src, '/static/justvoxel-logo.png');
  const custom = '<img src=x onerror=alert(1)>/../../custom';
  assert.equal(context.label(custom), custom);
  assert.equal(context.icon(custom).src, '/static/justvoxel-logo.png');
  assert.equal(context.icon(custom).alt, '');
  assert.match(js, /text\('span', purposeLabel\(connection\.purpose\)\)/);
  assert.match(js, /text\('strong', purposeLabel\(c\.purpose\)\)/);
  assert.doesNotMatch(js, /innerHTML|insertAdjacentHTML/);
});

test('OAuth settings remain editable only for an existing OAuth connection', () => {
  assert.match(js, /const oauthMode = Boolean\(c\.id\) && c\.auth_mode === 'oauth'/);
  assert.match(js, /if \(oauthMode\) \{[\s\S]*?querySelector\('\[data-gateway-advanced\]'\)\.append\(oauthFields\)/);
  assert.match(js, /else oauthFields\.remove\(\)/);
  assert.match(js, /if \(updated.auth_mode === 'oauth'\) updated.oauth = Object.fromEntries/);
  assert.match(form, /data-gateway-oauth hidden><h4>Developer integration/);
  for (const name of ['issuer', 'audience', 'jwks_uri', 'scope', 'subject', 'client_id']) assert.ok(form.includes(`name="${name}"`));
  for (const section of ['STATUS', 'Actions', 'Advanced details', 'Connection ID', 'Safe credential fingerprint']) assert.ok((form + js).includes(section));
});

test('normal help keeps raw endpoints and integration details collapsed at the bottom', () => {
  const normal = help.slice(0, help.indexOf('<details class="panel"><summary>Developer integration'));
  assert.doesNotMatch(normal, /OAuth|JWKS|issuer|audience|subject|client ID|data-gateway-mcp|data-gateway-rest/);
  assert.match(help, /<details class="panel"><summary>Developer integration<\/summary>/);
  assert.doesNotMatch(help, /<details[^>]* open/);
  assert.ok(help.indexOf('Security / authorization') < help.indexOf('Developer integration'));
});

test('System Control Center keeps Terminal bottom-left and connections bottom-right', () => {
  const system = header.split('<span class="control-section-label">System</span>')[1].split('</section>')[0];
  const labels = [...system.matchAll(/<strong>([^<]+)<\/strong>/g)].map(match => match[1]);
  assert.deepEqual(labels, ['System Settings', 'System Monitor', 'System Update', 'System Network', 'Storage', 'File Browser', 'Terminal', 'AI &amp; API Connections']);
});

test('AI workspace retains the shared header hierarchy, refresh and close controls', () => {
  assert.match(workspace, /<p class="eyebrow">JustVoxel<\/p><h2 id="agent-gateway-title">AI &amp; API Connections<\/h2>/);
  assert.match(workspace, /workspace-header-refresh" type="button" data-gateway-refresh>Refresh/);
  assert.match(workspace, /workspace-header-close"[^>]*data-gateway-close[^>]*aria-label=[^>]*><svg/);
});

// Exercise the settings renderer and submit listener without a browser or secrets.
function settingsHarness(connection, inventory = [], gatewayEnabled = true) {
  const nodes = new Map();
  const node = selector => {
    if (!nodes.has(selector)) {
      const element = new Node('div');
      element.value = '';
      element.remove = () => { element.removed = true; };
      nodes.set(selector, element);
    }
    return nodes.get(selector);
  };
  const fields = new Map();
  const field = name => {
    if (!fields.has(name)) { const input = new Node('input'); input.value = ''; new Node('label').append(input); fields.set(name, input); }
    return fields.get(name);
  };
  const settings = new Node('form');
  settings.elements = {namedItem: field};
  settings.querySelector = node;
  const providerRadio = new Node('input');
  const scopeRadio = new Node('input');
  settings.querySelectorAll = selector => {
    if (selector === 'input[name="purpose"]') return [providerRadio];
    if (selector === 'input[name="server_scope"]') return [scopeRadio];
    if (selector === 'input[name="instance_ids"]:checked') return node('[data-gateway-instance-options]').querySelectorAll('input').filter(input => input.checked);
    return [];
  };
  const scope = node('[aria-labelledby="gateway-scope-label"]');
  const scopeHeading = new Node('h4');
  const scopeChoices = [new Node('label'), new Node('label')];
  scope.querySelector = () => scopeHeading;
  scope.querySelectorAll = () => scopeChoices;
  settings.reportValidity = () => true;
  const details = new Node('section');
  details.querySelector = () => settings;
  const requests = [];
  const confirmations = [];
  const context = vm.createContext({
    details, structuredClone, busy: false, instances: inventory, snapshot: {enabled: gatewayEnabled},
    URL, location: {origin: 'https://voxel.example', protocol: 'https:', hostname: 'voxel.example'}, navigator: {clipboard: {async writeText() {}}},
    content: node('[data-gateway-content]'), find: node,
    document: {createElement: tag => new Node(tag), querySelector: () => ({content: {cloneNode() { return new Node('form'); }}})},
    text: (tag, value) => new Node(tag, value), activityLabel: value => value || 'No activity yet',
    mutate: payload => requests.push(payload), requestLevel4: payload => confirmations.push(payload),
  });
  const helpers = js.slice(js.indexOf('  const providers ='), js.indexOf('  const activityLabel'));
  const rendererStart = js.indexOf('  const renderSettings =');
  const rendererEnd = js.indexOf("  find('[data-gateway-add]').addEventListener", rendererStart);
  const renderer = js.slice(js.indexOf('  // Presentation only:'), rendererStart) + js.slice(rendererStart, rendererEnd);
  const surfaces = js.slice(js.indexOf("  let selectedID ="), js.indexOf('  const levelLabel ='));
  const navigation = js.slice(rendererEnd, js.indexOf("  for (const button of dialog.querySelectorAll('[data-gateway-copy-address]'))", rendererEnd));
  details.hidden = true;
  node('[data-gateway-overview]').hidden = false;
  node('[data-gateway-help]').hidden = true;
  vm.runInContext(surfaces + helpers + renderer + navigation + '\nglobalThis.render = renderSettings;', context);
  if (connection) context.render(connection);
  return {node, field, details, settings, requests, confirmations, scope(value) { field('server_scope').value = value; scopeRadio.listeners.change(); }, preview(purpose) { field('purpose').value = purpose; providerRadio.listeners.change(); }, submit() { settings.listeners.submit({preventDefault() {}}); }};
}

test('branded providers submit local OAuth while generic MCP and automation retain tokens', () => {
  for (const purpose of ['OpenAI', 'Claude', 'Grok', 'Gemini', 'GitHub Copilot', 'Other AI / MCP app', 'Automation / REST API']) {
    // Existing external OAuth cannot be selected through new connection creation.
    const h = settingsHarness({name: 'My AI Assistant', purpose: 'OpenAI', auth_mode: 'oauth', level: 1, enabled: true, all_instances: true, instance_ids: []});
    assert.equal(h.node('[data-gateway-oauth]').removed, true);
    assert.equal(h.node('[data-gateway-advanced]').hidden, true);
    h.field('purpose').value = purpose;
    h.submit();
    assert.equal(h.requests.length, 1);
    assert.equal(h.requests[0].action, 'create');
    assert.equal(h.requests[0].connection.auth_mode, ['Other AI / MCP app', 'Automation / REST API'].includes(purpose) ? 'token' : 'oauth_local');
    assert.equal(h.requests[0].connection.purpose, purpose);
    assert.equal(h.requests[0].connection.oauth, undefined);
    assert.equal(h.requests[0].connection.level, 1);
    assert.equal(h.requests[0].connection.enabled, true);
    assert.equal(h.requests[0].connection.all_instances, true);
  }
});

test('existing OAuth bindings and custom provider labels survive settings Apply', () => {
  const oauth = {issuer: 'https://issuer.example', audience: 'https://appliance.example/mcp', jwks_uri: 'https://issuer.example/keys', scope: 'justvoxel:connect', subject: 'user', client_id: 'app'};
  const existing = {id: 'legacy', name: 'Legacy app', purpose: '<custom app>', auth_mode: 'oauth', oauth, level: 2, enabled: true, all_instances: true};
  const h = settingsHarness(existing);
  assert.ok(h.node('[data-gateway-advanced]').children.includes(h.node('[data-gateway-oauth]')));
  assert.equal(h.node('[data-gateway-oauth]').hidden, false);
  assert.equal(h.node('[data-gateway-advanced]').hidden, false);
  assert.equal(h.node('[data-gateway-purpose-label]').textContent, 'Provider');
  assert.equal(h.node('[data-gateway-rotate]').hidden, true);
  assert.equal(h.node('[data-gateway-revoke]').hidden, true);
  h.field('subject').value = 'updated-user';
  h.submit();
  const updated = h.requests[0].connection;
  assert.equal(h.requests[0].action, 'apply');
  assert.equal(updated.id, existing.id);
  assert.equal(updated.purpose, existing.purpose);
  assert.equal(updated.auth_mode, 'oauth');
  assert.deepEqual({...updated.oauth}, {...oauth, subject: 'updated-user'});
  assert.equal(existing.oauth.subject, 'user');
});

test('token settings hide OAuth and retain credential actions and Level 4 confirmation', () => {
  const existing = {id: 'token-app', name: 'Assistant', purpose: 'Claude', auth_mode: 'token', level: 1, enabled: true, all_instances: true};
  const h = settingsHarness(existing);
  assert.equal(h.node('[data-gateway-oauth]').removed, true);
  assert.equal(h.node('[data-gateway-rotate]').hidden, false);
  assert.equal(h.node('[data-gateway-revoke]').hidden, false);
  h.field('level').value = '4';
  h.submit();
  assert.equal(h.requests.length, 0);
  assert.equal(h.confirmations.length, 1);
  assert.equal(h.confirmations[0].connection.level, 4);
  assert.equal(h.confirmations[0].connection.auth_mode, 'token');
});


test('first Add Connection click from overview opens the populated add surface and tabs switch both ways', () => {
  const h = settingsHarness();
  const add = h.node('[data-gateway-add]');
  const help = h.node('[data-gateway-help-open]');
  const connections = h.node('[data-gateway-connections]');
  assert.equal(h.node('[data-gateway-overview]').hidden, false);
  assert.equal(h.details.hidden, true);
  for (let visit = 0; visit < 3; visit++) {
    // Surface changes must also expose the start of the form after scrolling.
    h.node('[data-gateway-content]').scrollTop = 400;
    add.listeners.click();
    assert.equal(h.details.hidden, false);
    assert.equal(h.details.children.length, 1);
    assert.equal(h.node('[data-gateway-overview]').hidden, true);
    assert.equal(h.node('[data-gateway-help]').hidden, true);
    assert.equal(add['aria-selected'], 'true');
    assert.equal(help['aria-selected'], 'false');
    assert.equal(h.node('[data-gateway-content]').scrollTop, 0);
    assert.equal(h.field('name').value, '');
    assert.equal(h.field('level').value, 1);
    assert.equal(h.node('[data-gateway-settings-title]').hidden, true);
    assert.equal(h.node('[data-gateway-settings-title]').textContent, '');
    assert.equal(h.node('[data-gateway-save]').textContent, 'Create');
    help.listeners.click();
    assert.equal(h.details.hidden, true);
    assert.equal(h.node('[data-gateway-help]').hidden, false);
    assert.equal(h.node('[data-gateway-overview]').hidden, true);
    assert.equal(add['aria-selected'], 'false');
    assert.equal(help['aria-selected'], 'true');
    connections.listeners.click();
    assert.equal(h.node('[data-gateway-overview]').hidden, false);
    assert.equal(h.details.hidden, true);
    assert.equal(h.node('[data-gateway-help]').hidden, true);
    assert.equal(connections['aria-selected'], 'true');
    add.listeners.click();
    connections.listeners.click();
    assert.equal(h.node('[data-gateway-overview]').hidden, false);
  }
});

test('approved add form has a shared name/access row and no duplicate heading or back navigation', () => {
  assert.doesNotMatch(workspace + form, /Back to connections/);
  assert.doesNotMatch(form, /<h[1-6][^>]*>Add Connection<\/h[1-6]>/);
  const row = form.match(/<div class="gateway-name-access-row">([\s\S]*?)<\/div>/)[1];
  assert.match(row, /name="name" placeholder="My AI Assistant"/);
  assert.match(row, />Access level<\/span><select name="level"/);
  const ordered = ['gateway-name-access-row', 'data-gateway-purpose-label', 'data-gateway-providers', 'Which Minecraft servers?', 'name="enabled"', 'gateway-save'];
  for (let i = 1; i < ordered.length; i++) assert.ok(form.indexOf(ordered[i - 1]) < form.indexOf(ordered[i]));
  assert.match(form, /gateway-save"><button type="submit" data-gateway-save>Create/);
  assert.doesNotMatch(form + js, /data-gateway-back/);
  const css = readFileSync(`${base}webui/internal/server/static/agent-gateway.css`, 'utf8');
  assert.match(css, /\.gateway-name-access-row \{[^}]*grid-template-columns: minmax\(0, 1fr\) minmax\(0, 1fr\)/);
  assert.match(css, /@media \(max-width: 520px\) \{\s*\[data-agent-gateway-dialog\] \.gateway-name-access-row \{ grid-template-columns: minmax\(0, 1fr\)/);
});

test('gateway toggle updates immediately and failed mutation restores either previous state with existing feedback', async () => {
  for (const previous of [false, true]) {
    const gatewaySwitch = new Node('input');
    gatewaySwitch.checked = previous;
    const state = new Node('strong', `Gateway ${previous ? 'Enabled' : 'Disabled'}`);
    const feedback = new Node('p');
    let rejectSave;
    let request;
    const context = vm.createContext({
      gatewaySwitch, snapshot: {enabled: previous, connections: []}, busy: false,
      dialog: {querySelectorAll: () => [gatewaySwitch]},
      find: selector => selector === '[data-gateway-state]' ? state : new Node('p'),
      send: payload => { request = payload; return new Promise((resolve, reject) => { rejectSave = reject; }); },
      message: value => { feedback.textContent = value; },
      renderCards() {}, showTokenOnce() {}, load() { throw new Error('Failed saves must not reload'); },
    });
    const mutation = js.slice(js.indexOf('  const mutate ='), js.indexOf("  levelDialog.querySelector('form').addEventListener"));
    const toggle = js.slice(js.indexOf('  const showGatewayState ='), js.indexOf('  const renderCards ='));
    vm.runInContext(mutation + toggle, context);
    gatewaySwitch.checked = !previous;
    gatewaySwitch.listeners.change();
    assert.equal(gatewaySwitch.checked, !previous);
    assert.equal(state.textContent, `Gateway ${!previous ? 'Enabled' : 'Disabled'}`);
    assert.equal(gatewaySwitch.disabled, true);
    assert.equal(request.action, 'enable');
    assert.equal(request.enabled, !previous);
    rejectSave(new Error('Save failed'));
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(gatewaySwitch.checked, previous);
    assert.equal(state.textContent, `Gateway ${previous ? 'Enabled' : 'Disabled'}`);
    assert.equal(feedback.textContent, 'Save failed');
    assert.equal(Boolean(gatewaySwitch.disabled), false);
  }
});


test('local OAuth onboarding shows the connection-specific address without credential actions', () => {
  const h = settingsHarness({id: 'jvc_example', name: 'Home Server AI', purpose: 'OpenAI', auth_mode: 'oauth_local', level: 2, enabled: true, all_instances: true});
  const onboarding = h.node('.gateway-primary').afterChildren[0];
  const descendants = node => [node, ...node.children.flatMap(descendants)];
  const nodes = descendants(onboarding);
  for (const value of ['Ready to connect', 'MCP address', 'Copy', 'Complete the normal JustVoxel sign-in and approval when ChatGPT starts authentication.']) assert.ok(nodes.some(node => node.textContent === value), value);
  assert.ok(nodes.some(node => node.tagName === 'input' && node.readOnly === true && node.value === 'https://voxel.example/mcp/jvc_example'), 'connection-specific MCP address in a readonly input');
  assert.equal(h.node('[data-gateway-rotate]').hidden, true);
  assert.equal(h.node('[data-gateway-revoke]').hidden, true);
  assert.equal(h.node('[data-gateway-oauth]').removed, true);
  h.submit();
  assert.equal(h.requests[0].connection.auth_mode, 'oauth_local');
  assert.match(js, /if \(result\.connection\?\.auth_mode !== 'oauth_local'\) showTokenOnce\(result\)/);
});

test('local OAuth can never show the raw token popup', () => {
  const start = js.indexOf('  const showTokenOnce = result => {');
  const end = js.indexOf('  const closeLevel4', start);
  let shown = 0;
  const context = vm.createContext({tokenNode: new Node('pre'), tokenDialog: {showModal() { shown++; }}, clearToken() {}});
  vm.runInContext(js.slice(start, end) + '\nglobalThis.show = showTokenOnce;', context);
  context.show({connection: {auth_mode: 'oauth_local'}, token: 'must-not-be-displayed'});
  assert.equal(shown, 0);
});

test('OAuth login rechecks existing strict cookies without changing normal login or cookie policy', () => {
  const login = readFileSync(`${base}webui/internal/server/templates/login.html`, 'utf8');
  const resume = readFileSync(`${base}webui/internal/server/static/oauth-login.js`, 'utf8');
  assert.match(login, /\{\{if \.OAuthRequest\}\}<script src="\/static\/oauth-login\.js" defer><\/script>\{\{end\}\}/);
  assert.match(resume, /fetch\('\/api\/session-info'/);
  assert.match(resume, /credentials: 'same-origin'/);
  assert.match(resume, /location\.replace\('\/oauth\/authorize\?request='/);
  assert.doesNotMatch(resume, /password|localStorage|sessionStorage|console\./);
});


test('effective cards follow Gateway state without changing connection enabled flags', () => {
  const connection = {id: 'one', name: 'Assistant', enabled: true};
  const cards = new Node('div');
  const context = vm.createContext({snapshot: {enabled: false, connections: [connection]}, cards,
    providerIcon: () => new Node('img'), purposeLabel: () => 'OpenAI', levelLabel: () => 'Read Only',
    scopeLabel: () => 'All Minecraft servers', activityLabel: () => 'No activity yet',
    text: (tag, value) => new Node(tag, value)});
  const start = js.indexOf('  const renderCards =');
  const end = js.indexOf('  const renderSettings =', start);
  vm.runInContext(js.slice(start, end) + '\nglobalThis.render = renderCards;', context);
  const state = () => cards.children[0].children[0].children[1].textContent;
  const assertVisualState = active => {
    assert.equal(cards.children[0].className, `panel minecraft-server-card minecraft-server-health-${active ? 'success' : 'neutral'}`);
    assert.equal(cards.children[0].children[0].children[1].className, `minecraft-server-state ${active ? 'success' : 'neutral'}`);
  };
  context.render();
  assert.equal(state(), 'Disabled');
  assertVisualState(false);
  assert.equal(connection.enabled, true);
  context.render(true);
  assert.equal(state(), 'Enabled');
  assertVisualState(true);
  context.render(false);
  assert.equal(state(), 'Disabled');
  assertVisualState(false);
  assert.equal(connection.enabled, true);
  context.render(true);
  assert.equal(state(), 'Enabled');
  assertVisualState(true);
  assert.equal(connection.enabled, true);
  connection.enabled = false;
  context.render(true);
  assert.equal(state(), 'Disabled');
  assertVisualState(false);
  assert.match(js, /renderCards\(enabled\)/);
  const toggle = js.slice(js.indexOf('  const showGatewayState ='), start);
  assert.doesNotMatch(toggle, /connection\.enabled\s*=|connections\s*=/);
});

test('connection cards reuse server hierarchy and compact human-readable data for every auth type', () => {
  const cards = new Node('div');
  const connection = {id: 'immutable-id', name: '<Assistant>', purpose: 'OpenAI', level: 2, enabled: true, all_instances: true};
  const context = vm.createContext({
    snapshot: {enabled: true, connections: [connection]}, cards, instances: [],
    document: {createElement: tag => new Node(tag)},
    text: (tag, value) => new Node(tag, value),
  });
  const helpers = js.slice(js.indexOf('  const levelLabel ='), js.indexOf('  let pendingLevel4'));
  const renderer = js.slice(js.indexOf('  const renderCards ='), js.indexOf('  const renderSettings ='));
  vm.runInContext(helpers + renderer + '\nglobalThis.render = renderCards;', context);
  for (const [mode, label] of [['token', 'Token'], ['oauth', 'OAuth'], ['oauth_local', 'OAuth']]) {
    connection.auth_mode = mode;
    context.render();
    const card = cards.children[0];
    assert.equal(card.dataset.gatewayConnection, 'immutable-id');
    const [heading, identity, metrics, activity, actions] = card.children;
    assert.equal(heading.className, 'minecraft-server-heading');
    assert.deepEqual(heading.children.map(node => node.textContent), ['<Assistant>', 'Enabled']);
    assert.equal(identity.className, 'minecraft-server-software-row');
    assert.equal(identity.children[0].children[0].src, '/static/ai-providers/openai.svg');
    assert.equal(identity.children[0].children[1].textContent, 'OpenAI · ChatGPT & Codex');
    assert.equal(metrics.className, 'minecraft-server-metrics');
    assert.deepEqual(metrics.children.map(metric => metric.children.map(node => node.textContent)), [
      ['Access', 'Level 2'], ['Scope', 'All servers'], ['Auth', label],
    ]);
    assert.equal(activity.className, 'gateway-card-activity');
    assert.deepEqual(activity.children.map(node => node.textContent), ['Last activity', 'No activity yet']);
    assert.equal(actions.children[0].textContent, 'Settings');
    assert.equal(actions.children[0].type, 'button');
  }
  connection.all_instances = false;
  connection.instance_ids = ['jv-example'];
  connection.last_activity = '2026-10-30T12:00:00Z';
  context.instances.push({id: 'jv-example', display_name: 'Survival'});
  context.render();
  assert.equal(cards.children[0].children[2].children[1].children[1].textContent, 'Survival');
  assert.equal(cards.children[0].children[3].children[1].textContent, new Date(connection.last_activity).toLocaleString());
});

test('connection cards inherit the existing Minecraft Server responsive grid and state styles', () => {
  const css = readFileSync(`${base}webui/internal/server/static/agent-gateway.css`, 'utf8');
  const serverCSS = readFileSync(`${base}webui/internal/server/static/minecraft-servers.css`, 'utf8');
  const serverTemplate = readFileSync(`${base}webui/internal/server/templates/minecraft_servers.html`, 'utf8');
  assert.match(header, /href="\/static\/minecraft-servers\.css"/);
  assert.match(overview, /class="minecraft-server-inventory"><div class="minecraft-server-entries" data-gateway-cards/);
  assert.match(serverCSS, /\.minecraft-server-inventory \.minecraft-server-entries \{ display: grid; grid-template-columns: repeat\(auto-fill,minmax\(min\(100%,420px\),420px\)\)/);
  assert.match(serverCSS, /@container \(max-width: 600px\)[^\n]*grid-template-columns: min\(100%,420px\)/);
  assert.match(serverTemplate, /minecraft-server-health-\{\{\.CardHealth\}\}/);
  assert.match(serverCSS, /\.minecraft-server-card\.minecraft-server-health-success \{ border-color: var\(--jv-success\)/);
  assert.match(serverCSS, /\.minecraft-server-state\.success \{ color: var\(--jv-success\)/);
  assert.match(serverCSS, /\.minecraft-server-metrics span \{[^}]*text-transform: uppercase/);
  assert.match(css, /\.gateway-card-activity > span \{[^}]*text-transform: uppercase/);
  assert.doesNotMatch(css, /\[data-gateway-cards\]|\.minecraft-server-(?:inventory|entries|card|heading)/);
  assert.doesNotMatch(css, /flex:\s*0 0 320px|width:\s*320px|height:\s*300px/);
});

test('provider assets are transparent and generic providers reuse the application logo', () => {
  const css = readFileSync(`${base}webui/internal/server/static/agent-gateway.css`, 'utf8');
  const icon = css.match(/\.gateway-provider-icon \{([^}]+)\}/)[1];
  assert.match(icon, /background: transparent/);
  assert.doesNotMatch(icon, /#fff|white|border-radius/);
  for (const purpose of ['Other AI / MCP app', 'Automation / REST API']) {
    assert.ok(form.includes(`value="${purpose}"><img class="gateway-provider-icon" src="/static/justvoxel-logo.png"`));
  }
  for (const [asset, fill] of [['openai', '#FFFFFF'], ['claude', '#D97757'], ['githubcopilot', '#FFFFFF'], ['googlegemini', 'url(#gemini)']]) {
    const svg = readFileSync(`${base}webui/internal/server/static/ai-providers/${asset}.svg`, 'utf8');
    assert.ok(svg.includes(`fill="${fill}"`));
  }
  const grok = readFileSync(`${base}webui/internal/server/static/ai-providers/grok.svg`, 'utf8');
  assert.doesNotMatch(grok, /<foreignObject|fill="#050505"/);
});

test('sections are unframed and provider cards retain fixed wrapping tracks', () => {
  assert.doesNotMatch(form, /<fieldset|<legend/);
  assert.match(form, /role="group" aria-labelledby="gateway-provider-label"/);
  assert.match(form, /role="group" aria-labelledby="gateway-scope-label"/);
  const css = readFileSync(`${base}webui/internal/server/static/agent-gateway.css`, 'utf8');
  assert.ok(css.includes('gateway-provider-grid { display: flex; flex-wrap: wrap;'));
  assert.match(css, /gateway-provider-card \{[^}]*flex: 0 0 224px;[^}]*height: 88px;[^}]*cursor: pointer/);
  assert.doesNotMatch(css, /repeat\(|grid-template-columns: repeat/);
  for (const card of form.matchAll(/<label class="gateway-provider-card">([\s\S]*?)<\/label>/g)) assert.match(card[1], /<input type="radio" name="purpose"/);
  assert.match(css, /@container gateway \(max-width: 520px\)/);
  assert.match(css, /\[data-gateway-details\][\s\S]*?max-width: 64rem/);
});

test('normal actions match authentication type and reset usage limits is absent', () => {
  const actions = form.slice(form.indexOf('<section data-gateway-actions'), form.indexOf('<p data-gateway-live-note'));
  assert.deepEqual([...actions.matchAll(/<button[^>]*>([^<]+)<\/button>/g)].map(match => match[1]), ['Rotate token', 'Revoke token', 'Delete']);
  assert.doesNotMatch(actions, /allowance|usage limits|Reset restart/);
  const advanced = form.match(/<details data-gateway-advanced[\s\S]*?<\/details>/)[0];
  assert.doesNotMatch(form + js, /data-gateway-allowance|Reset usage limits|reset-allowance/);
  for (const auth_mode of ['token', 'oauth_local']) {
    const h = settingsHarness({id: 'one', name: 'App', purpose: 'OpenAI', auth_mode, level: 1, enabled: true, all_instances: true});
    for (const action of ['rotate', 'revoke']) assert.equal(h.node(`[data-gateway-${action}]`).hidden, auth_mode !== 'token');
    assert.equal(h.requests.length, 0);
    assert.equal(typeof h.node('[data-gateway-delete]').listeners.click, 'function');
  }
});

const descendants = node => [node, ...node.children.flatMap(descendants)];
const guideText = node => descendants(node).map(item => item.value || item.textContent).join('\n');
function guideHarness(origin = 'http://192.168.1.20:8099') {
  const host = new Node('section');
  const copied = [];
  const nodes = new Map();
  const find = selector => {
    if (!nodes.has(selector)) nodes.set(selector, new Node('div'));
    return nodes.get(selector);
  };
  const context = vm.createContext({URL, location: {origin}, find,
    navigator: {clipboard: {async writeText(value) { copied.push(value); }}},
    document: {createElement: tag => new Node(tag)}, text: (tag, value) => new Node(tag, value)});
  const providers = js.slice(js.indexOf('  const providers ='), js.indexOf('  const activityLabel'));
  const guide = js.slice(js.indexOf('  // Presentation only:'), js.indexOf('  const renderSettings ='));
  vm.runInContext(providers + guide + '\nglobalThis.render = renderProviderGuide; globalThis.setup = setupHelpGuide; globalThis.local = localAddress;', context);
  return {host, copied, find, context, render(purpose, options) { context.render(host, purpose, origin + (purpose === 'Automation / REST API' ? '/api/v1' : '/mcp/jvc_current'), options); return guideText(host); }};
}

test('provider preview changes immediately and only Apply persists the selected purpose', () => {
  const existing = {id: 'jvc_current', name: 'Home', purpose: 'OpenAI', auth_mode: 'oauth_local', level: 1, enabled: true, all_instances: true};
  const h = settingsHarness(existing);
  const guide = h.node('.gateway-primary').afterChildren[0];
  for (const purpose of ['Claude', 'Grok', 'Gemini', 'GitHub Copilot', 'OpenAI']) {
    h.preview(purpose);
    assert.equal(guide.children[0].children[1].children[0].textContent, purpose);
    const icon = guide.children[0].children[0];
    const expectedIcon = {'Claude': 'claude', 'Grok': 'grok', 'Gemini': 'googlegemini', 'GitHub Copilot': 'githubcopilot', 'OpenAI': 'openai'}[purpose];
    assert.equal(icon.src, `/static/ai-providers/${expectedIcon}.svg`);
    assert.equal(descendants(guide).filter(node => node.tagName === 'button' && node.textContent === 'Codex').length, purpose === 'OpenAI' ? 1 : 0);
    assert.equal(h.requests.length, 0);
    assert.equal(existing.purpose, 'OpenAI');
    assert.equal(existing.id, 'jvc_current');
    assert.ok(guideText(guide).includes('https://voxel.example/mcp/jvc_current'));
  }
  h.preview('Grok'); h.submit();
  assert.equal(h.requests[0].action, 'apply');
  assert.equal(h.requests[0].connection.purpose, 'Grok');
  assert.equal(h.requests[0].connection.id, existing.id);
  assert.equal(h.requests[0].connection.auth_mode, existing.auth_mode);
  assert.doesNotMatch(js, /text\('h4', 'Authentication'\), text\('p', 'Sign in with JustVoxel'\)/);
});

test('OpenAI switches between current ChatGPT and local Codex instructions', async () => {
  const h = guideHarness();
  let content = h.render('OpenAI');
  for (const phrase of ['ChatGPT', 'Codex', 'Plugins', 'Add custom MCP server', 'I understand and want to continue', 'install the plugin', 'reachable HTTPS', 'Secure MCP Tunnel', 'cannot connect directly']) assert.ok(content.includes(phrase), phrase);
  descendants(h.host).find(node => node.tagName === 'button' && node.textContent === 'Codex').listeners.click();
  content = guideText(h.host);
  for (const phrase of ['codex mcp add justvoxel --url http://192.168.1.20:8099/mcp/jvc_current', 'codex mcp list', 'computer running Codex can reach JustVoxel']) assert.ok(content.includes(phrase), phrase);
  assert.doesNotMatch(content, /public Internet|ChatGPT cloud cannot/);
  await descendants(h.host).find(node => node.textContent === 'Copy command').listeners.click();
  assert.equal(h.copied[0], 'codex mcp add justvoxel --url http://192.168.1.20:8099/mcp/jvc_current');
});

test('provider guides contain the verified setup, network and availability details', () => {
  const h = guideHarness();
  const required = new Map([
    ['Claude', ['Customize', 'Connectors', 'Sign in now', 'Sign in when needed', 'Register automatically', 'Dynamic Client Registration', 'reachable by Claude']],
    ['Grok', ['grok.com/connectors', 'New Connector', 'Custom', 'public Internet', 'local-only']],
    ['Gemini', ['Settings', 'Connected Apps', 'Add a custom app', 'Dynamic Client Registration', '18+', 'United States', 'Personal Google Account', 'Keep Activity', 'English', 'Gemini web', 'reachable MCP endpoint']],
    ['GitHub Copilot', ['copilot mcp add --transport http justvoxel http://192.168.1.20:8099/mcp/jvc_current', '/mcp auth justvoxel', '/mcp add', 'LAN address', 'Copilot cloud agent and Copilot code review currently do not support remote OAuth MCP servers']],
    ['Other AI / MCP app', ['Streamable HTTP', 'authentication method', 'JustVoxel token', 'Cloud clients', 'Local clients']],
    ['Automation / REST API', ['REST API address', 'http://192.168.1.20:8099/api/v1', 'JustVoxel API token', 'one-time', 'Store it securely', 'access level and Minecraft scope']],
  ]);
  for (const [provider, phrases] of required) {
    const content = h.render(provider);
    for (const phrase of phrases) assert.ok(content.includes(phrase), `${provider}: ${phrase}`);
    if (provider === 'Automation / REST API') assert.doesNotMatch(content, /OAuth|sign-in|MCP address/);
    if (provider === 'Gemini') {
      const callout = descendants(h.host).find(node => node.children[0]?.textContent === 'Current Gemini requirements');
      assert.equal(callout.className, 'notice');
    }
  }
});

test('official provider links are approved, isolated new-tab links', () => {
  const h = guideHarness();
  const approved = new Map([
    ['OpenAI', 'https://developers.openai.com/api/docs/guides/custom-mcp-server'],
    ['Claude', 'https://support.claude.com/en/articles/11175166-get-started-with-custom-connectors-using-remote-mcp'],
    ['Grok', 'https://docs.x.ai/grok/connectors'],
    ['Gemini', 'https://support.google.com/gemini/answer/17209137'],
    ['GitHub Copilot', 'https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-mcp-servers'],
  ]);
  for (const [provider, url] of [...approved, ['OpenAI', 'https://developers.openai.com/learn/docs-mcp']]) {
    h.render(provider, {mode: url.endsWith('docs-mcp') ? 'Codex' : 'ChatGPT'});
    const links = descendants(h.host).filter(node => node.tagName === 'a');
    assert.equal(links.length, 1);
    assert.equal(links[0].href, url);
    assert.equal(links[0].target, '_blank');
    assert.equal(links[0].rel, 'noopener noreferrer');
    assert.equal(links[0].textContent, 'Official setup guide ↗');
  }
});

test('Help selects one guide without any connection mutation or saved state', () => {
  const h = guideHarness(); h.context.setup();
  const buttons = h.find('[data-gateway-help-providers]').children;
  const guide = h.find('[data-gateway-help-guide]');
  assert.equal(buttons.length, 7);
  for (const button of buttons) {
    button.listeners.click();
    assert.equal(buttons.filter(item => item['aria-pressed'] === 'true').length, 1);
    assert.equal(guide.children.filter(item => item.className === 'gateway-connection-identity').length, 1);
    assert.equal(guide.children[0].children[1].children[0].textContent, button.children[1].textContent);
  }
  const setup = js.slice(js.indexOf('  const setupHelpGuide ='), js.indexOf('  const renderSettings ='));
  assert.doesNotMatch(setup, /mutate|send\(|snapshot|selectedID|fetch\(/);
});

test('local guidance detects HTTP, loopback and private IPv4 without requiring cloud access for local clients', () => {
  const {context} = guideHarness();
  for (const url of ['http://voxel.example/mcp', 'https://10.1.2.3/mcp', 'https://172.16.0.1/mcp', 'https://172.31.255.2/mcp', 'https://192.168.0.1/mcp', 'https://localhost/mcp', 'https://127.0.0.2/mcp', 'https://[::1]/mcp']) assert.equal(context.local(url), true, url);
  for (const url of ['https://voxel.example/mcp', 'https://172.15.0.1/mcp', 'https://172.32.0.1/mcp']) assert.equal(context.local(url), false, url);
});

test('existing settings use equal Name/Access tracks with compact Enabled and expiration', () => {
  const h = settingsHarness({id: 'one', name: 'Home', purpose: 'OpenAI', auth_mode: 'oauth_local', enabled: true, all_instances: true});
  const top = h.node('.gateway-name-access-row');
  assert.equal(top.children[0].className, 'gateway-enabled-control');
  assert.equal(top.children[0].children[1], h.node('.gateway-switch'));
  assert.equal(top.afterChildren[0], h.field('expires_at').closest('label'));
  assert.equal(top.afterChildren[0].className, 'gateway-expiration');
  assert.equal(h.settings.children.at(-1), h.node('[data-gateway-advanced]'));
  assert.doesNotMatch(form.match(/<details data-gateway-advanced[^>]*>/)[0], /\bopen\b/);
  const css = readFileSync(`${base}webui/internal/server/static/agent-gateway.css`, 'utf8');
  assert.match(css, /gateway-edit-settings \.gateway-name-access-row \{ grid-template-columns: minmax\(0, 1fr\) minmax\(0, 1fr\) auto/);
  assert.match(css, /gateway-expiration \{ width: fit-content; max-width: min\(100%, 24rem\)/);
  assert.match(css, /@container gateway \(max-width: 420px\)/);
});

test('Enabled immediately changes success/danger label and waits for Apply', () => {
  const connection = {id: 'one', name: 'Home', purpose: 'OpenAI', auth_mode: 'oauth_local', level: 1, enabled: true, all_instances: true};
  const h = settingsHarness(connection);
  const label = h.node('.gateway-switch span');
  assert.equal(label.textContent, 'Enabled');
  assert.equal(label.className, 'gateway-enabled-success');
  h.field('enabled').checked = false;
  h.field('enabled').listeners.change();
  assert.equal(label.textContent, 'Disabled');
  assert.equal(label.className, 'gateway-enabled-danger');
  assert.equal(connection.enabled, true);
  assert.equal(h.requests.length, 0);
  h.submit();
  assert.equal(h.requests[0].action, 'apply');
  assert.equal(h.requests[0].connection.enabled, false);
  const css = readFileSync(`${base}webui/internal/server/static/agent-gateway.css`, 'utf8');
  assert.match(css, /gateway-enabled-success \{ color: var\(--jv-success\)/);
  assert.match(css, /gateway-enabled-danger \{ color: var\(--jv-danger\)/);
});

test('scope lane keeps toggles, existing software icons and names while preserving draft selections', () => {
  const connection = {id: 'one', name: 'Home', purpose: 'OpenAI', auth_mode: 'oauth_local', level: 1, enabled: true, all_instances: false, instance_ids: ['paper']};
  const h = settingsHarness(connection, [
    {id: 'paper', display_name: 'Paper1', software_icon: '/static/paper.svg'},
    {id: 'purpur', display_name: 'Purpur', software_icon: '/static/purpur.svg'},
  ]);
  const lane = h.node('[data-gateway-instance-options]');
  assert.deepEqual(lane.children.map(item => item.children.map(child => child.tagName)), [['input', 'img', 'span'], ['input', 'img', 'span']]);
  assert.equal(lane.children[0].children[1].src, '/static/paper.svg');
  assert.equal(lane.children[0].children[2].textContent, 'Paper1');
  assert.equal(lane.children[0].children[0].role, 'switch');
  h.scope('all');
  assert.equal(h.node('[data-gateway-scope]').hidden, false);
  assert.equal(lane['aria-disabled'], 'true');
  assert.ok(lane.querySelectorAll('input').every(input => input.disabled));
  assert.equal(lane.children[0].children[0].checked, true);
  h.scope('selected');
  assert.ok(lane.querySelectorAll('input').every(input => !input.disabled));
  assert.equal(lane.children[0].children[0].checked, true);
  assert.equal(h.requests.length, 0);
  h.submit();
  assert.deepEqual(Array.from(h.requests[0].connection.instance_ids), ['paper']);
  const all = settingsHarness(connection); all.scope('all'); all.submit();
  assert.equal(all.requests[0].connection.all_instances, true);
  const css = readFileSync(`${base}webui/internal/server/static/agent-gateway.css`, 'utf8');
  assert.match(css, /gateway-edit-settings \[data-gateway-instance-options\] \{ display: flex; flex-wrap: wrap/);
  assert.match(form, />All servers<\/label>/);
  assert.match(form, />Choose servers<\/label>/);
});

test('settings metrics use supported status, human auth and scope values and local dates', () => {
  const stamp = '2026-10-31T15:55:40.259788033Z';
  for (const [auth_mode, auth] of [['oauth_local', 'OAuth'], ['oauth', 'OAuth'], ['token', 'Token']]) {
    for (const [enabled, gateway, state] of [[true, true, 'Ready'], [true, false, 'Gateway off'], [false, true, 'Disabled'], [false, false, 'Disabled']]) {
      const h = settingsHarness({id: 'one', name: 'Home', purpose: 'OpenAI', auth_mode, enabled, all_instances: false, instance_ids: ['paper'], created_at: stamp, updated_at: stamp}, [], gateway);
      const metrics = h.node('[data-gateway-status-details]').children;
      assert.deepEqual(metrics.map(metric => metric.children[0].textContent), ['STATUS', 'LAST ACTIVITY', 'AUTH', 'SCOPE']);
      assert.deepEqual(metrics.map(metric => metric.children[1].textContent), [state, 'No activity yet', auth, '1 servers']);
      const safe = h.node('[data-gateway-safe-details]').children;
      const expected = new Date(stamp).toLocaleString(undefined, {year: 'numeric', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit'});
      assert.equal(safe[5].textContent, expected);
      assert.equal(safe[7].textContent, expected);
      assert.ok(!guideText(h.node('[data-gateway-safe-details]')).includes(stamp));
    }
  }
  const h = settingsHarness({id: 'one', name: 'Home', purpose: 'OpenAI', enabled: true, all_instances: true});
  assert.equal(h.node('[data-gateway-status-details]').children[3].children[1].textContent, 'All servers');
  assert.doesNotMatch(js, /Credential status|Sign in with JustVoxel/);
});

function copyHarness(clipboard, legacy) {
  const parent = new Node('div');
  const field = new Node('input'); field.value = 'http://voxel.local/mcp/jvc_current'; parent.append(field);
  const status = new Node('p');
  const attempts = [];
  const context = vm.createContext({navigator: {clipboard}, document: {
    createElement: tag => new Node(tag),
    execCommand(command) { attempts.push(command); if (legacy instanceof Error) throw legacy; return legacy; },
  }});
  vm.runInContext(js.slice(js.indexOf('  const copyText ='), js.indexOf('  const settingsDate =')) + '\nglobalThis.copy = copyText;', context);
  return {parent, field, status, attempts, copy: () => context.copy(field.value, field, status)};
}

test('copy uses clipboard first and guarded legacy copying on LAN HTTP or permission rejection', async () => {
  const copied = [];
  const primary = copyHarness({async writeText(value) { copied.push(value); }}, false);
  await primary.copy();
  assert.deepEqual(copied, [primary.field.value]);
  assert.equal(primary.status.textContent, 'Copied');
  assert.equal(primary.attempts.length, 0);
  for (const clipboard of [undefined, {async writeText() { throw new Error('Denied'); }}]) {
    const fallback = copyHarness(clipboard, true); await fallback.copy();
    assert.deepEqual(fallback.attempts, ['copy']);
    assert.equal(fallback.status.textContent, 'Copied');
    assert.equal(fallback.parent.children.length, 1);
  }
});

test('failed copying visibly selects the entire address and offers Ctrl+C', async () => {
  for (const legacy of [false, new Error('Unavailable')]) {
    const h = copyHarness(undefined, legacy); await h.copy();
    assert.equal(h.status.textContent, 'Selected — press Ctrl+C');
    assert.equal(h.field.focused, true);
    assert.equal(h.field.selected, true);
    assert.equal(h.field.selectionStart, 0);
    assert.equal(h.field.selectionEnd, h.field.value.length);
    assert.equal(h.parent.children[0], h.field);
    assert.equal(h.parent.children.length, 1);
  }
  assert.match(js, /copy\.addEventListener\('click', \(\) => copyText\(value, field, status\)\)/);
  assert.match(js, /await copyText\(field\.value, field, status\)/);
  assert.match(header, /<input[^>]+readonly[^>]+data-gateway-mcp/);
  assert.match(header, /<input[^>]+readonly[^>]+data-gateway-rest/);
});

test('provider guide Copy command also selects the unchanged command when clipboard copying fails', async () => {
  const h = guideHarness();
  h.context.navigator = {};
  h.render('GitHub Copilot');
  const copy = descendants(h.host).find(node => node.textContent === 'Copy command');
  await copy.listeners.click();
  const command = copy.parentNode.children[0];
  assert.equal(command.value, 'copilot mcp add --transport http justvoxel http://192.168.1.20:8099/mcp/jvc_current');
  assert.equal(command.readOnly, true);
  assert.equal(command.selected, true);
  assert.equal(command.selectionEnd, command.value.length);
  assert.ok(guideText(h.host).includes('Selected — press Ctrl+C'));
});

test('date presentation falls back only for invalid dates and leaves expiration conversion intact', () => {
  const context = vm.createContext({});
  vm.runInContext(js.slice(js.indexOf('  const settingsDate ='), js.indexOf('  const localAddress =')) + '\nglobalThis.format = settingsDate;', context);
  assert.equal(context.format('invalid timestamp'), 'invalid timestamp');
  assert.equal(context.format(undefined), 'Unavailable');
  assert.match(js, /date\.getTime\(\) - date\.getTimezoneOffset\(\) \* 60000/);
  assert.match(js, /updated\.expires_at = new Date\(field\('expires_at'\)\.value\)\.toISOString\(\)/);
});
