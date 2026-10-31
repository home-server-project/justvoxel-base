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
  querySelectorAll(selector) { return this.children.flatMap(child => [...((selector === 'input' || selector === 'input[name="instance_ids"]' && child.name === 'instance_ids') && child.tagName === 'input' ? [child] : []), ...child.querySelectorAll(selector)]); }
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
    const action = card.children[1].children[1].children[0];
    assert.equal(action.textContent, '⋯');
    let prevented = false;
    action.listeners.click({preventDefault() { prevented = true; }});
    assert.equal(prevented, true);
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
  for (const section of ['Access levels', 'Minecraft scope', 'Authentication', 'Network access']) assert.ok(help.includes(`<h3>${section}</h3>`), section);
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
  assert.deepEqual(purposes, ['OpenAI', 'Claude', 'Grok', 'Gemini', 'GitHub Copilot', 'OpenClaw', 'Other AI / MCP app', 'Automation / REST API']);
  assert.equal((form.match(/<strong>OpenAI<\/strong>/g) || []).length, 1);
  assert.equal(purposes.filter(purpose => purpose === 'OpenClaw').length, 1);
  assert.match(form, /<strong>OpenClaw<\/strong><small>Control UI &amp; CLI<\/small>/);
  assert.match(form, /<strong>GitHub Copilot<\/strong><small>CLI &amp; VS Code<\/small>/);
  assert.match(form, /<strong>OpenAI<\/strong><small>ChatGPT &amp; Codex<\/small>/);
  assert.match(form, /<strong>Claude<\/strong><small>Claude &amp; Claude Code<\/small>/);
  assert.doesNotMatch(form, /<select name="purpose"|name="auth_mode"|Connection method|OAuth — Advanced|What do you want to call it/);
  for (const phrase of ['>Name</span>', 'What will use this connection?', 'Access level', 'Which Minecraft servers?', 'All servers']) assert.ok(form.includes(phrase), phrase);
  assert.match(form, /class="gateway-provider-grid"/);
  for (const asset of ['openai', 'claude', 'grok', 'googlegemini', 'githubcopilot', 'openclaw']) {
    assert.ok(form.includes(`/static/ai-providers/${asset}.svg`));
    const svg = readFileSync(`${base}webui/internal/server/static/ai-providers/${asset}.svg`, 'utf8');
    assert.match(svg, /<svg/);
    assert.doesNotMatch(svg, /<script|(?:href|src)="https?:/);
  }
  assert.doesNotMatch(form, /<img[^>]+src="https?:/);
  const css = readFileSync(`${base}webui/internal/server/static/agent-gateway.css`, 'utf8');
  assert.match(css, /gateway-provider-card:has\(input:checked\)/);
  assert.match(css, /gateway-provider-card:has\(input:focus-visible\)/);
  assert.match(css, /gateway-provider-grid \{ display: grid;/);
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
  for (const purpose of ['Claude', 'Grok', 'Gemini', 'GitHub Copilot', 'OpenClaw', 'Other AI / MCP app', 'Automation / REST API']) assert.equal(context.label(purpose), purpose);
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
  for (const section of ['STATUS', 'Advanced details', 'Connection ID', 'Safe credential fingerprint']) assert.ok((form + js).includes(section));
});

test('normal help keeps raw endpoints and integration details collapsed at the bottom', () => {
  const normal = help.slice(0, help.indexOf('<details class="panel"><summary>Developer integration'));
  assert.doesNotMatch(normal, /JWKS|issuer|audience|subject|client ID|data-gateway-mcp|data-gateway-rest/);
  assert.match(help, /<details class="panel"><summary>Developer integration<\/summary>/);
  assert.doesNotMatch(help, /<details[^>]* open/);
  assert.ok(help.indexOf('Authentication') < help.indexOf('Developer integration'));
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
  const created = [];
  const create = (tag, value = '') => { const element = new Node(tag, value); created.push(element); return element; };
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
  settings.querySelectorAll = selector => {
    if (selector === 'input[name="purpose"]') return [providerRadio];
    if (selector === 'input[name="instance_ids"]:checked') return node('[data-gateway-instance-options]').querySelectorAll('input').filter(input => input.checked);
    return [];
  };
  settings.reportValidity = () => true;
  const details = new Node('section');
  details.querySelector = () => settings;
  const requests = [];
  const confirmations = [];
  const context = vm.createContext({
    setTimeout: () => 1, clearTimeout() {}, details, structuredClone, busy: false, instances: inventory, snapshot: {enabled: gatewayEnabled},
    URL, location: {origin: 'https://voxel.example', protocol: 'https:', hostname: 'voxel.example'}, navigator: {clipboard: {async writeText() {}}},
    content: node('[data-gateway-content]'), find: node,
    document: {createElement: create, querySelector: () => ({content: {cloneNode() { return new Node('form'); }}})},
    text: create, activityLabel: value => value || 'No activity yet',
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
  return {node, field, details, settings, created, requests, confirmations, scope(value) { const toggle = node('[data-gateway-all-servers]'); toggle.checked = value === 'all'; toggle.listeners.change(); }, preview(purpose) { field('purpose').value = purpose; providerRadio.listeners.change?.(); }, submit() { settings.listeners.submit({preventDefault() {}}); }};
}

test('branded providers submit local OAuth while generic MCP and automation retain tokens', () => {
  for (const purpose of ['OpenAI', 'Claude', 'Grok', 'Gemini', 'GitHub Copilot', 'OpenClaw', 'Other AI / MCP app', 'Automation / REST API']) {
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
    assert.doesNotMatch(form + js, /data-gateway-settings-title|— settings/);
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
  const ordered = ['gateway-name-access-row', 'data-gateway-purpose-label', 'data-gateway-providers', 'Which Minecraft servers?', 'gateway-save'];
  for (let i = 1; i < ordered.length; i++) assert.ok(form.indexOf(ordered[i - 1]) < form.indexOf(ordered[i]));
  assert.match(form, /gateway-save"><button[^>]+data-gateway-delete hidden>Delete<\/button><button type="submit" data-gateway-save>Create/);
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
  const onboarding = h.node('[data-gateway-status]').afterChildren[0];
  const descendants = node => [node, ...node.children.flatMap(descendants)];
  const nodes = descendants(onboarding);
  for (const value of ['MCP address', 'Copy', 'Complete JustVoxel sign-in/approval when prompted.']) assert.ok(nodes.some(node => node.textContent === value), value);
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
    const [heading, identity, metrics, activity] = card.children;
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
    assert.equal(card.children.length, 4);
    const actions = identity.children[1];
    assert.equal(actions.className, 'storage-action-menu minecraft-server-more');
    assert.equal(actions.children.length, 1);
    assert.equal(actions.children[0].tagName, 'summary');
    assert.equal(actions.children[0].textContent, '⋯');
    assert.equal(actions.children[0]['aria-label'], 'Settings for <Assistant>');
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
  const icon = css.match(/^\[data-agent-gateway-dialog\] \.gateway-provider-icon \{([^}]+)\}/m)[1];
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

test('sections are unframed and provider cards use compact responsive tracks', () => {
  assert.doesNotMatch(form, /<fieldset|<legend/);
  assert.match(form, /role="group" aria-labelledby="gateway-provider-label"/);
  assert.match(form, /role="group" aria-labelledby="gateway-scope-label"/);
  const css = readFileSync(`${base}webui/internal/server/static/agent-gateway.css`, 'utf8');
  assert.ok(css.includes('gateway-provider-grid { display: grid;'));
  assert.match(css, /gateway-provider-card \{[^}]*height: 64px;[^}]*cursor: pointer/);
  assert.match(css, /grid-template-columns: repeat\(auto-fit, minmax\(min\(100%, 170px\), 1fr\)\)/);
  assert.doesNotMatch(css, /224px|88px/);
  for (const card of form.matchAll(/<label class="gateway-provider-card">([\s\S]*?)<\/label>/g)) assert.match(card[1], /<input type="radio" name="purpose"/);
  assert.match(css, /@container gateway \(max-width: 520px\)/);
  assert.match(css, /\[data-gateway-details\][\s\S]*?max-width: 64rem/);
});

test('normal actions match authentication type and reset usage limits is absent', () => {
  const actions = form.slice(form.indexOf('<div class="action-row gateway-credential-actions"'), form.indexOf('<p data-gateway-live-note'));
  assert.deepEqual([...actions.matchAll(/<button[^>]*>([^<]+)<\/button>/g)].map(match => match[1]), ['Rotate token', 'Revoke token']);
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
  const context = vm.createContext({setTimeout: () => 1, clearTimeout() {}, URL, location: {origin}, find,
    navigator: {clipboard: {async writeText(value) { copied.push(value); }}},
    document: {createElement: tag => new Node(tag)}, text: (tag, value) => new Node(tag, value)});
  const providers = js.slice(js.indexOf('  const providers ='), js.indexOf('  const activityLabel'));
  const guide = js.slice(js.indexOf('  // Presentation only:'), js.indexOf('  const renderSettings ='));
  vm.runInContext(providers + guide + '\nglobalThis.render = renderProviderGuide; globalThis.setup = setupHelpGuide; globalThis.local = localAddress; globalThis.secure = secureAddress;', context);
  return {host, copied, find, context, render(purpose, options) { context.render(host, purpose, origin + (purpose === 'Automation / REST API' ? '/api/v1' : '/mcp/jvc_current'), options); return guideText(host); }};
}

test('provider preview changes immediately and only Apply persists the selected purpose', () => {
  const existing = {id: 'jvc_current', name: 'Home', purpose: 'OpenAI', auth_mode: 'oauth_local', level: 1, enabled: true, all_instances: true};
  const h = settingsHarness(existing);
  const guide = h.node('[data-gateway-status]').afterChildren[0];
  for (const purpose of ['Claude', 'Grok', 'Gemini', 'GitHub Copilot', 'OpenClaw', 'OpenAI']) {
    h.preview(purpose);
    assert.ok(!descendants(guide).some(node => node.className === 'gateway-connection-identity' || node.tagName === 'img'));
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

test('OpenAI private ChatGPT prerequisite switches to a copyable local Codex command', async () => {
  const h = guideHarness();
  let content = h.render('OpenAI', {settings: true});
  for (const phrase of ['ChatGPT needs a reachable connection', 'Secure MCP Tunnel', 'public HTTPS', 'Do not paste this 192.168.1.20 address into ChatGPT', 'LOCAL reference only']) assert.ok(content.includes(phrase), phrase);
  assert.ok(!descendants(h.host).some(node => node.className === 'gateway-address'));
  assert.ok(descendants(h.host).some(node => node.href === 'https://developers.openai.com/api/docs/guides/secure-mcp-tunnels'));
  descendants(h.host).find(node => node.tagName === 'button' && node.textContent === 'Codex').listeners.click();
  content = guideText(h.host);
  for (const phrase of ['codex mcp add justvoxel --url http://192.168.1.20:8099/mcp/jvc_current', 'codex mcp list', 'computer running Codex can reach JustVoxel']) assert.ok(content.includes(phrase), phrase);
  assert.doesNotMatch(content, /Connection prerequisite|Secure MCP Tunnel|public HTTPS|Do not paste/);
  await descendants(h.host).find(node => node.textContent === 'Copy command').listeners.click();
  assert.equal(h.copied[0], 'codex mcp add justvoxel --url http://192.168.1.20:8099/mcp/jvc_current');
});

test('public HTTPS candidates show provider steps without claiming proven reachability', () => {
  const required = new Map([
    ['OpenAI', ['ChatGPT → Plugins', '+ → Add custom MCP server', 'Under Connection', 'choose Tunnel', 'risk warning', 'Create as a plugin', 'sign-in/approval', 'Plugins → MCPs → Add', 'Streamable HTTP', 'STDIO is for launching a local MCP process', 'reachable HTTPS MCP address in URL', 'Leave Bearer token env var and Headers empty']],
    ['Claude', ['Pro/Max', 'Customize → Connectors', '+ Add → Add custom connector', 'Sign in now', 'Sign in when needed', 'Register automatically', 'Dynamic Client Registration']],
    ['Grok', ['grok.com/connectors', 'New Connector → Custom', 'reachable JustVoxel MCP URL']],
    ['Gemini', ['Settings → Connected Apps', 'Personal Intelligence → Connected Apps', 'Add a custom app', 'Select Next', 'Dynamic Client Registration', '18+', 'United States', 'Personal Google Account', 'Keep Activity on', 'English', 'automatic registration failed']],
  ]);
  for (const settings of [true, false]) {
    const h = guideHarness('https://example.com');
    for (const [provider, phrases] of required) {
      const content = h.render(provider, {settings});
      for (const phrase of phrases) assert.ok(content.includes(phrase), `${provider}: ${phrase}`);
      assert.ok(content.includes('must be reachable by'));
      assert.ok(content.includes('URL alone does not prove reachability'));
      assert.doesNotMatch(content, /verified reachable|Connection prerequisite/);
      const address = descendants(h.host).find(node => node.tagName === 'input');
      assert.equal(address.value, 'https://example.com/mcp/jvc_current');
      assert.ok(descendants(h.host).some(node => node.tagName === 'button' && node.textContent === 'Copy'));
    }
  }
});

test('official provider links use the requested guides and isolated new tabs', () => {
  const h = guideHarness('https://example.com');
  for (const [provider, mode, url] of [
    ['OpenAI', 'ChatGPT', 'https://help.openai.com/en/articles/12584461-connecting-custom-mcp-servers-in-chatgpt'],
    ['OpenAI', 'Codex', 'https://developers.openai.com/learn/docs-mcp'],
    ['Claude', 'Claude', 'https://support.claude.com/en/articles/11175166-get-started-with-custom-connectors-using-remote-mcp'],
    ['Claude', 'Claude Code', 'https://code.claude.com/docs/en/mcp'],
    ['Grok', undefined, 'https://docs.x.ai/grok/connectors'],
    ['Gemini', undefined, 'https://support.google.com/gemini/answer/17209137'],
    ['GitHub Copilot', 'CLI', 'https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-mcp-servers'],
    ['GitHub Copilot', 'VS Code', 'https://code.visualstudio.com/docs/agent-customization/mcp-servers'],
    ['OpenClaw', 'Control UI', 'https://docs.openclaw.ai/tools/mcp'],
    ['OpenClaw', 'CLI', 'https://docs.openclaw.ai/cli/mcp/transports'],
  ]) {
    h.render(provider, {mode});
    const links = descendants(h.host).filter(node => node.tagName === 'a');
    assert.ok(links.some(node => node.href === url), url);
    for (const link of links) {
      assert.equal(link.target, '_blank');
      assert.equal(link.rel, 'noopener noreferrer');
    }
  }
});

test('Help selects one guide without any connection mutation or saved state', () => {
  const h = guideHarness(); h.context.setup();
  const buttons = h.find('[data-gateway-help-providers]').children;
  const guide = h.find('[data-gateway-help-guide]');
  assert.equal(buttons.length, 8);
  assert.deepEqual(buttons.map(button => button.children[1].textContent), ['OpenAI', 'Claude', 'Grok', 'Gemini', 'GitHub Copilot', 'OpenClaw', 'Other AI / MCP app', 'Automation / REST API']);
  for (const button of buttons) {
    button.listeners.click();
    assert.equal(buttons.filter(item => item['aria-pressed'] === 'true').length, 1);
    assert.equal(guide.children.filter(item => item.className === 'gateway-connection-identity').length, 1);
    assert.equal(guide.children[0].children[1].children[0].textContent, button.children[1].textContent);
  }
  const setup = js.slice(js.indexOf('  const setupHelpGuide ='), js.indexOf('  const renderSettings ='));
  assert.doesNotMatch(setup, /mutate|send\(|snapshot|selectedID|fetch\(/);
});

test('private host detection is independent of HTTP and HTTPS security', () => {
  const {context} = guideHarness();
  for (const url of ['https://10.1.2.3/mcp', 'https://172.16.0.1/mcp', 'https://172.31.255.2/mcp', 'https://192.168.0.1/mcp', 'https://localhost/mcp', 'https://127.0.0.2/mcp', 'https://[::1]/mcp', 'http://app.localhost/mcp', 'https://[fc00::1]/mcp', 'https://[fdff::1]/mcp', 'https://[fe80::1]/mcp', 'https://[febf::1]/mcp']) assert.equal(context.local(url), true, url);
  for (const url of ['http://voxel.example/mcp', 'https://voxel.example/mcp', 'https://172.15.0.1/mcp', 'https://172.32.0.1/mcp', 'https://[fec0::1]/mcp', 'https://[2001:db8::1]/mcp']) assert.equal(context.local(url), false, url);
});

test('existing settings use equal Name/Access tracks with an unlabeled switch and metric expiration', () => {
  const h = settingsHarness({id: 'one', name: 'Home', purpose: 'OpenAI', auth_mode: 'oauth_local', enabled: true, all_instances: true});
  assert.match(form, /gateway-enabled-control"><label class="gateway-switch">/);
  assert.equal(h.node('.gateway-name-access-row').afterChildren, undefined);
  assert.equal(h.node('[data-gateway-status-details]').children[0].children[1], h.field('expires_at'));
  assert.ok(form.indexOf('data-gateway-status hidden') < form.indexOf('data-gateway-advanced hidden'));
  assert.ok(form.indexOf('data-gateway-actions hidden') < form.indexOf('data-gateway-advanced hidden'));
  assert.doesNotMatch(js, /form\.append\(form\.querySelector\('\[data-gateway-advanced\]'\)\)/);
  assert.doesNotMatch(form.match(/<details data-gateway-advanced[^>]*>/)[0], /\bopen\b/);
  const css = readFileSync(`${base}webui/internal/server/static/agent-gateway.css`, 'utf8');
  assert.match(css, /\[data-gateway-settings\] \.gateway-name-access-row \{ grid-template-columns: minmax\(0, 1fr\) minmax\(0, 1fr\) auto/);
  assert.match(css, /gateway-status-metrics \.gateway-expiration \{[^}]*width: 100%/);
  assert.match(css, /@container gateway \(max-width: 420px\)/);
});

test('enabled switch has no visible label and waits for Apply', () => {
  const connection = {id: 'one', name: 'Home', purpose: 'OpenAI', auth_mode: 'oauth_local', level: 1, enabled: true, all_instances: true};
  const h = settingsHarness(connection);
  assert.match(form, /name="enabled" aria-label="Connection enabled"/);
  assert.doesNotMatch(form, /<span>Enabled<\/span>|<span>Disabled<\/span>/);
  assert.doesNotMatch(js.slice(js.indexOf('  const renderSettings =')), /syncEnabled|gateway-switch span/);
  h.field('enabled').checked = false;
  assert.equal(connection.enabled, true);
  assert.equal(h.requests.length, 0);
  h.submit();
  assert.equal(h.requests[0].action, 'apply');
  assert.equal(h.requests[0].connection.enabled, false);
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
  assert.match(css, /\[data-gateway-settings\] \.gateway-server-lane \{ display: flex; flex-wrap: wrap/);
  assert.match(form, />All servers<\/span><\/label>/);
  assert.doesNotMatch(form, /Choose servers/);
  assert.equal((form.match(/data-gateway-all-servers/g) || []).length, 1);
});

test('settings metrics use supported status, human auth and scope values and local dates', () => {
  const stamp = '2026-10-31T15:55:40.259788033Z';
  for (const [auth_mode, auth] of [['oauth_local', 'OAuth'], ['oauth', 'OAuth'], ['token', 'Token']]) {
    for (const [enabled, gateway, state] of [[true, true, 'Ready'], [true, false, 'Gateway off'], [false, true, 'Disabled'], [false, false, 'Disabled']]) {
      const h = settingsHarness({id: 'one', name: 'Home', purpose: 'OpenAI', auth_mode, enabled, all_instances: false, instance_ids: ['paper'], created_at: stamp, updated_at: stamp}, [], gateway);
      const metrics = h.node('[data-gateway-status-details]').children;
      assert.deepEqual(metrics.map(metric => metric.children[0].textContent), ['EXPIRATION', 'STATUS', 'LAST ACTIVITY', 'AUTH', 'SCOPE']);
      assert.deepEqual(metrics.slice(1).map(metric => metric.children[1].textContent), [state, 'No activity yet', auth, '1 servers']);
      const safe = h.node('[data-gateway-safe-details]').children;
      const expected = new Date(stamp).toLocaleString(undefined, {year: 'numeric', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit'});
      assert.equal(safe[5].textContent, expected);
      assert.equal(safe[7].textContent, expected);
      assert.ok(!guideText(h.node('[data-gateway-safe-details]')).includes(stamp));
    }
  }
  const h = settingsHarness({id: 'one', name: 'Home', purpose: 'OpenAI', enabled: true, all_instances: true});
  assert.equal(h.node('[data-gateway-status-details]').children[4].children[1].textContent, 'All servers');
  assert.doesNotMatch(js, /Credential status|Sign in with JustVoxel/);
});

function copyHarness(clipboard, legacy) {
  const parent = new Node('div');
  const field = new Node('input'); field.value = 'http://voxel.local/mcp/jvc_current'; parent.append(field);
  const status = new Node('p'); status.className = 'gateway-visually-hidden';
  const button = new Node('button', 'Copy');
  let reset;
  const attempts = [];
  const context = vm.createContext({setTimeout(callback) { reset = callback; return 1; }, clearTimeout() {}, navigator: {clipboard}, document: {
    createElement: tag => new Node(tag),
    execCommand(command) { attempts.push(command); if (legacy instanceof Error) throw legacy; return legacy; },
  }});
  vm.runInContext(js.slice(js.indexOf('  const copyText ='), js.indexOf('  const settingsDate =')) + '\nglobalThis.copy = copyText;', context);
  return {parent, field, status, button, attempts, reset: () => reset(), copy: () => context.copy(field.value, field, status, button)};
}

test('copy uses clipboard first and guarded legacy copying on LAN HTTP or permission rejection', async () => {
  const copied = [];
  const primary = copyHarness({async writeText(value) { copied.push(value); }}, false);
  await primary.copy();
  assert.deepEqual(copied, [primary.field.value]);
  assert.equal(primary.status.textContent, 'Copied');
  assert.equal(primary.button.textContent, 'Copied');
  primary.reset();
  assert.equal(primary.button.textContent, 'Copy');
  assert.equal(primary.attempts.length, 0);
  for (const clipboard of [undefined, {async writeText() { throw new Error('Denied'); }}]) {
    const fallback = copyHarness(clipboard, true); await fallback.copy();
    assert.deepEqual(fallback.attempts, ['copy']);
    assert.equal(fallback.status.textContent, 'Copied');
    assert.equal(fallback.button.textContent, 'Copied');
    assert.equal(fallback.status.className, 'gateway-visually-hidden');
    assert.equal(fallback.parent.children.length, 1);
  }
});

test('failed copying selects the entire address with button and invisible keyboard guidance', async () => {
  for (const legacy of [false, new Error('Unavailable')]) {
    const h = copyHarness(undefined, legacy); await h.copy();
    assert.equal(h.status.textContent, 'Address selected — press Ctrl+C');
    assert.equal(h.status.className, 'gateway-visually-hidden');
    assert.equal(h.button.textContent, 'Selected');
    assert.equal(h.button.title, 'Address selected — press Ctrl+C');
    h.reset();
    assert.equal(h.button.textContent, 'Copy');
    assert.equal(h.field.focused, true);
    assert.equal(h.field.selected, true);
    assert.equal(h.field.selectionStart, 0);
    assert.equal(h.field.selectionEnd, h.field.value.length);
    assert.equal(h.parent.children[0], h.field);
    assert.equal(h.parent.children.length, 1);
  }
  assert.match(js, /copy\.addEventListener\('click', \(\) => copyText\(value, field, status, copy\)\)/);
  assert.match(js, /await copyText\(field\.value, field, status, button\)/);
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
  assert.equal(copy.textContent, 'Selected');
  assert.equal(copy.title, 'Address selected — press Ctrl+C');
  assert.ok(descendants(h.host).some(node => node.className === 'gateway-visually-hidden' && node.textContent === copy.title));
});

test('date presentation falls back only for invalid dates and leaves expiration conversion intact', () => {
  const context = vm.createContext({});
  vm.runInContext(js.slice(js.indexOf('  const settingsDate ='), js.indexOf('  const localAddress =')) + '\nglobalThis.format = settingsDate;', context);
  assert.equal(context.format('invalid timestamp'), 'invalid timestamp');
  assert.equal(context.format(undefined), 'Unavailable');
  assert.match(js, /date\.getTime\(\) - date\.getTimezoneOffset\(\) \* 60000/);
  assert.match(js, /updated\.expires_at = new Date\(field\('expires_at'\)\.value\)\.toISOString\(\)/);
});


test('Add has no provider onboarding, addresses, commands or network callouts beyond its compact summary', () => {
  for (const purpose of ['OpenAI', 'Claude', 'Grok', 'Gemini', 'GitHub Copilot', 'OpenClaw', 'Other AI / MCP app', 'Automation / REST API']) {
    const h = settingsHarness({name: '', purpose, level: 1, enabled: true, all_instances: true});
    h.preview(purpose);
    assert.equal(h.settings.className, 'gateway-add-settings');
    assert.equal(h.node('[data-gateway-status]').afterChildren, undefined);
    assert.doesNotMatch(form + js, /data-gateway-settings-title|— settings/);
    assert.ok(!h.created.some(node => ['ol', 'aside', 'code'].includes(node.tagName) || node.className === 'gateway-guide'));
    const content = h.created.map(node => node.value || node.textContent).join('\n');
    assert.doesNotMatch(content, /MCP address|How to connect|Secure MCP Tunnel|Official setup guide|https?:\/\//);
    assert.equal(h.node('[data-gateway-save]').textContent, 'Create');
    assert.equal(h.requests.length, 0);
  }
});

test('Add has one neutral summary immediately below provider cards, with no setup controls', () => {
  assert.equal((form.match(/data-gateway-compatibility-summary/g) || []).length, 1);
  assert.match(form, /value="Automation \/ REST API"[^\n]+\n\s*<\/div>\n\s*<p class="muted gateway-compatibility-summary" data-gateway-compatibility-summary aria-live="polite" hidden><\/p>/);
  const summary = form.match(/<p[^>]*data-gateway-compatibility-summary[^>]*><\/p>/)[0];
  assert.doesNotMatch(summary, /warning|notice|icon|<svg|<img/);
  assert.doesNotMatch(form, /data-gateway-copy|MCP address|https?:\/\/|codex mcp|claude mcp|openclaw mcp|copilot mcp/);
  const css = readFileSync(`${base}webui/internal/server/static/agent-gateway.css`, 'utf8');
  const rule = css.match(/\.gateway-compatibility-summary \{([^}]+)\}/)[1];
  assert.match(rule, /margin: \.4rem 0 0; font-size: \.8rem;/);
  assert.doesNotMatch(rule, /background|border|yellow|warning|height|padding/);
});

test('Add immediately previews all eight exact summaries without persisting purpose or auth', () => {
  const summaries = new Map([
    ['OpenAI', 'ChatGPT: Secure MCP Tunnel or reachable public HTTPS · Codex: local/LAN supported'],
    ['Claude', 'Claude: remotely reachable MCP required · Claude Code: local/LAN supported'],
    ['Grok', 'Public Internet MCP endpoint required'],
    ['Gemini', 'Public HTTPS Streamable HTTP endpoint required'],
    ['GitHub Copilot', 'CLI & VS Code: local/LAN supported'],
    ['OpenClaw', 'Control UI & CLI: local/LAN supported'],
    ['Other AI / MCP app', 'Depends on whether the MCP client runs locally or in the cloud'],
    ['Automation / REST API', 'Client must be able to reach the REST API endpoint'],
  ]);
  const draft = {name: 'Home', purpose: 'OpenAI', auth_mode: 'oauth_local', level: 1, enabled: true, all_instances: true};
  const original = structuredClone(draft);
  const h = settingsHarness(draft);
  const summary = h.node('[data-gateway-compatibility-summary]');
  assert.equal(summary.hidden, false);
  assert.equal(summary.textContent, summaries.get('OpenAI'));
  for (const [purpose, expected] of summaries) {
    h.preview(purpose);
    assert.equal(summary.textContent, expected);
    assert.doesNotMatch(summary.textContent, /https?:\/\/|192\.168\.|Ready|Unsupported|\n/);
    assert.equal(h.requests.length, 0);
    assert.deepEqual(draft, original);
  }
  const edit = settingsHarness({...draft, id: 'jvc_current'});
  assert.equal(edit.node('[data-gateway-compatibility-summary]').hidden, true);
  assert.doesNotMatch(js, /localStorage|sessionStorage/);
});

test('Add shares the desktop configuration row and enabled draft with Settings', () => {
  const draft = {name: 'Home', purpose: 'OpenAI', level: 1, enabled: true, all_instances: true};
  const h = settingsHarness(draft);
  assert.match(form, /gateway-name-access-row[\s\S]*?gateway-enabled-control"><label class="gateway-switch">/);
  assert.equal((form.match(/name="enabled"/g) || []).length, 1);
  for (const checked of [false, true, false]) {
    h.field('enabled').checked = checked;
    assert.doesNotMatch(form, /<span>Enabled<\/span>|<span>Disabled<\/span>/);
    assert.equal(h.requests.length, 0);
    assert.equal(draft.enabled, true);
  }
  h.submit();
  assert.equal(h.requests[0].action, 'create');
  assert.equal(h.requests[0].connection.enabled, false);
  const css = readFileSync(`${base}webui/internal/server/static/agent-gateway.css`, 'utf8');
  assert.match(css, /\[data-gateway-settings\] \.gateway-name-access-row \{ grid-template-columns: minmax\(0, 1fr\) minmax\(0, 1fr\) auto/);
});

test('Add and Settings share one wrapping scope lane and retain disabled draft selections', () => {
  const inventory = ['Paper1', 'Purpur', 'Spigot', 'Vanilla'].map((name, index) => ({id: String(index), display_name: name, software_icon: `/static/software/${name}.svg`}));
  for (const id of [undefined, 'existing']) {
    const h = settingsHarness({id, name: 'Home', purpose: 'OpenAI', level: 1, enabled: true, all_instances: false, instance_ids: ['0', '2']}, inventory);
    const lane = h.node('[data-gateway-instance-options]');
    assert.equal(lane.children.length, 4);
    for (const [index, item] of lane.children.entries()) {
      assert.equal(item.className, 'gateway-server-item');
      assert.deepEqual(item.children.map(child => child.tagName), ['input', 'img', 'span']);
      assert.equal(item.children[0].role, 'switch');
      assert.equal(item.children[1].className, 'minecraft-server-software-icon');
      assert.equal(item.children[1].src, inventory[index].software_icon);
      assert.equal(item.children[2].textContent, inventory[index].display_name);
    }
    for (const scope of ['all', 'selected', 'all', 'selected']) {
      h.scope(scope);
      assert.equal(h.node('[data-gateway-scope]').hidden, false);
      assert.ok(lane.querySelectorAll('input').every(input => input.disabled === (scope === 'all')));
      assert.deepEqual(lane.querySelectorAll('input').map(input => input.checked), [true, false, true, false]);
      assert.equal(h.requests.length, 0);
    }
    h.submit();
    assert.deepEqual(Array.from(h.requests[0].connection.instance_ids), ['0', '2']);
  }
  const css = readFileSync(`${base}webui/internal/server/static/agent-gateway.css`, 'utf8');
  assert.match(css, /\[data-gateway-settings\] \.gateway-server-lane \{ display: flex; flex-wrap: wrap/);
  assert.match(css, /gateway-server-item \{[^}]*flex: 0 0 auto/);
  assert.match(css, /gateway-server-item \{[^}]*border-inline-start: 1px solid/);
});

test('Settings puts configuration before metrics, guide, actions and final Advanced', () => {
  const h = settingsHarness({id: 'existing', name: 'Home', purpose: 'OpenAI', enabled: true, all_instances: true});
  const order = ['gateway-name-access-row', 'data-gateway-purpose-label', 'gateway-scope-label', 'data-gateway-status hidden', 'data-gateway-actions hidden', 'gateway-save', 'data-gateway-advanced hidden'];
  for (let i = 1; i < order.length; i++) assert.ok(form.indexOf(order[i - 1]) < form.indexOf(order[i]), order[i]);
  assert.equal(h.node('.gateway-name-access-row').afterChildren, undefined);
  assert.equal(h.node('[data-gateway-status]').afterChildren[0].className, 'gateway-guide gateway-settings-guide');
  assert.equal(h.node('.gateway-primary').afterChildren, undefined);
  assert.doesNotMatch(form + js, /data-gateway-settings-title|— settings/);
});

test('address classification covers the requested protocol and host combinations', () => {
  const {context} = guideHarness();
  for (const [address, privateHost, secure] of [
    ['http://192.168.0.28:8099', true, false],
    ['https://192.168.0.28', true, true],
    ['http://example.com', false, false],
    ['https://example.com', false, true],
  ]) {
    assert.equal(context.local(address), privateHost, address);
    assert.equal(context.secure(address), secure, address);
  }
});

test('cloud modes gate private and non-HTTPS endpoints before any copy or paste workflow', () => {
  for (const origin of ['http://192.168.1.20:8099', 'https://192.168.1.20', 'http://localhost', 'https://[fd00::1]', 'http://example.com']) {
    const h = guideHarness(origin);
    for (const options of [{settings: true}, {help: true}]) {
      for (const [purpose, title, body] of [
        ['OpenAI', 'ChatGPT needs a reachable connection', 'Secure MCP Tunnel'],
        ['Claude', 'Claude needs a remotely reachable MCP endpoint', 'Anthropic infrastructure'],
        ['Grok', 'Public endpoint required', 'public'],
        ['Gemini', 'Public HTTPS endpoint required', 'publicly reachable HTTPS Streamable HTTP'],
      ]) {
        const content = h.render(purpose, options);
        assert.ok(content.includes('Connection prerequisite'), purpose);
        assert.ok(content.includes(title), purpose);
        assert.ok(content.includes(body), purpose);
        assert.ok(!descendants(h.host).some(node => node.className === 'gateway-address'), purpose);
        assert.ok(!descendants(h.host).some(node => node.tagName === 'input'), purpose);
        assert.ok(!descendants(h.host).some(node => node.tagName === 'button' && /Copy/.test(node.textContent)), purpose);
        assert.ok(!descendants(h.host).some(node => node.tagName === 'ol'), purpose);
        if (purpose === 'Grok') assert.ok(descendants(h.host).some(node => node.href === 'https://docs.x.ai/grok/connectors/custom-mcp-tunneling'));
        if (purpose === 'Claude') assert.doesNotMatch(content, /built-in.*tunnel/);
      }
    }
  }
  const h = guideHarness();
  assert.match(h.render('Claude'), /current JustVoxel address is private and cannot be reached directly/);
  assert.match(h.render('Grok'), /xAI requires a custom MCP server to be reachable over the public internet/);
  assert.match(h.render('Gemini'), /current JustVoxel endpoint does not meet that requirement/);
});

test('Settings uses an address row only when the selected client mode can use it', () => {
  const h = guideHarness();
  for (const [purpose, mode] of [['OpenAI', 'Codex'], ['Claude', 'Claude Code'], ['GitHub Copilot', 'CLI'], ['GitHub Copilot', 'VS Code'], ['OpenClaw', 'Control UI'], ['OpenClaw', 'CLI'], ['Other AI / MCP app'], ['Automation / REST API']]) {
    h.render(purpose, {settings: true, mode});
    const nodes = descendants(h.host);
    assert.ok(!nodes.some(node => node.className === 'gateway-connection-identity' || node.tagName === 'img'));
    const address = h.host.children[0];
    assert.equal(address.className, 'gateway-address');
    assert.deepEqual(address.children.map(node => node.tagName), ['span', 'input', 'button']);
    assert.equal(address.children[0].textContent, purpose === 'Automation / REST API' ? 'REST API address' : 'MCP address');
    assert.equal(address.children[1].readOnly, true);
    assert.equal(address.children[1].value, 'http://192.168.1.20:8099' + (purpose === 'Automation / REST API' ? '/api/v1' : '/mcp/jvc_current'));
    assert.equal(address.children[2].textContent, 'Copy');
    assert.equal(address.children[2]['data-gateway-copy-label'], 'Selected');
    assert.equal(h.host.children[1].className, 'gateway-visually-hidden');
    assert.equal(h.host.children[1].role, 'status');
    assert.doesNotMatch(guideText(h.host), /Connection prerequisite|public.endpoint required|cannot be reached directly/);
  }
});

test('Settings and Help use the same mode reachability rules', () => {
  for (const origin of ['http://192.168.1.20', 'https://192.168.1.20', 'http://example.com', 'https://example.com']) {
    const h = guideHarness(origin);
    for (const [purpose, modes] of [['OpenAI', ['ChatGPT', 'Codex']], ['Claude', ['Claude', 'Claude Code']], ['GitHub Copilot', ['CLI', 'VS Code']], ['OpenClaw', ['Control UI', 'CLI']], ['Grok', [undefined]], ['Gemini', [undefined]]]) {
      for (const mode of modes) {
        const settings = h.render(purpose, {settings: true, mode});
        const help = h.render(purpose, {help: true, mode});
        assert.equal(settings.includes('Connection prerequisite'), help.includes('Connection prerequisite'), purpose + ': ' + mode);
        assert.equal(h.host.children[0].className, 'gateway-connection-identity');
      }
    }
  }
  const h = guideHarness();
  const claude = h.render('Claude', {help: true});
  assert.ok(claude.includes('Owner or Primary Owner'));
  assert.ok(claude.includes('Organization settings → Connectors'));
  const chatgpt = h.render('OpenAI', {help: true});
  for (const phrase of ['Platform tunnel configuration', 'running tunnel-client', 'outbound connection to OpenAI', 'server itself can remain private']) assert.ok(chatgpt.includes(phrase), phrase);
});

test('generic MCP and REST reachability remain client-specific without AI cloud restrictions', () => {
  const h = guideHarness();
  for (const options of [{settings: true}, {help: true}]) {
    const generic = h.render('Other AI / MCP app', options);
    for (const phrase of ['depends on where your MCP client runs', 'local client can use this LAN address', 'cloud-hosted client usually needs', 'Streamable HTTP / HTTP', 'if that client can reach it', 'OAuth flow when supported', 'Compatibility depends on the client']) assert.ok(generic.includes(phrase), phrase);
    assert.ok(descendants(h.host).some(node => node.value === 'http://192.168.1.20:8099/mcp/jvc_current'));
    const rest = h.render('Automation / REST API', options);
    for (const phrase of ['REST client/automation must be able to reach it', 'one-time JustVoxel API token', 'Store the token securely']) assert.ok(rest.includes(phrase), phrase);
    assert.doesNotMatch(rest, /OAuth|MCP address|Connection prerequisite|cloud|public HTTPS/);
  }
});

test('compact desktop lanes wrap by available width with Advanced as the final collapsed element', () => {
  const css = readFileSync(`${base}webui/internal/server/static/agent-gateway.css`, 'utf8');
  assert.doesNotMatch(form + js, /data-gateway-settings-title|— settings/);
  assert.doesNotMatch(form, /Choose servers/);
  assert.match(form, /class="gateway-server-lane" data-gateway-scope data-gateway-instance-options>\s*<label class="gateway-server-item gateway-all-servers"><input[^>]+role="switch"[^>]+data-gateway-all-servers/);
  assert.equal((form.match(/data-gateway-all-servers/g) || []).length, 1);
  assert.match(form, /name="server_scope" value="all"/);
  assert.match(css, /gateway-server-lane \{ display: flex; flex-wrap: wrap; align-items: center/);
  assert.match(css, /gateway-status-metrics \{ display: grid; grid-template-columns: repeat\(5, minmax\(0, 1fr\)\);[^}]*border-block: 1px solid/);
  assert.match(css, /gateway-guide-toolbar \{ display: flex; flex-wrap: wrap; align-items: center/);
  assert.match(css, /gateway-guide-toolbar > a \{ margin-left: auto/);
  assert.match(css, /gateway-settings-guide \.gateway-address \{ padding: 0; background: transparent/);
  assert.match(css, /gateway-settings-guide \.gateway-help-steps \{ display: grid; grid-template-columns: repeat\(2, minmax\(0, 1fr\)\)/);
  assert.match(css, /@container gateway \(max-width: 600px\)[\s\S]*gateway-help-steps \{ grid-template-columns: minmax\(0, 1fr\)/);
  assert.match(css, /gateway-actions-lane \{ display: flex; flex-wrap: wrap; align-items: center/);
  assert.match(css, /gateway-save \{ flex-wrap: nowrap; margin: 0 0 0 auto/);
  const actions = form.slice(form.indexOf('gateway-actions-lane'), form.indexOf('<details data-gateway-advanced'));
  assert.ok(actions.indexOf('data-gateway-live-note') < actions.indexOf('data-gateway-delete'));
  assert.match(actions, /class="action-row gateway-save"><button[^>]+data-gateway-delete[^>]*>Delete<\/button><button[^>]+data-gateway-save>Create<\/button>/);
  assert.ok(actions.indexOf('data-gateway-live-note') < actions.indexOf('data-gateway-save'));
  assert.match(form, /<details data-gateway-advanced hidden>[\s\S]*<\/details>\s*<\/form>/);
  assert.doesNotMatch(form.match(/<details data-gateway-advanced[^>]*>/)[0], /\bopen\b/);
});

test('All servers adapter submits all scope and preserves exactly the chosen individual IDs', () => {
  const inventory = ['paper', 'purpur', 'spigot', 'vanilla'].map(id => ({id, display_name: id, software_icon: `/static/${id}.svg`}));
  for (const id of [undefined, 'existing']) {
    const h = settingsHarness({id, name: 'Home', purpose: 'OpenAI', auth_mode: 'oauth_local', level: 1, enabled: true, all_instances: false, instance_ids: ['paper']}, inventory);
    const inputs = h.node('[data-gateway-instance-options]').querySelectorAll('input');
    h.scope('all');
    assert.equal(h.node('[data-gateway-all-servers]').disabled, undefined);
    assert.ok(inputs.every(input => input.disabled));
    assert.deepEqual(inputs.map(input => input.checked), [true, false, false, false]);
    h.submit();
    assert.equal(h.requests[0].connection.all_instances, true);
    assert.deepEqual(Array.from(h.requests[0].connection.instance_ids), []);
    h.scope('selected');
    assert.ok(inputs.every(input => !input.disabled));
    h.submit();
    assert.deepEqual(Array.from(h.requests[1].connection.instance_ids), ['paper']);
    inputs[1].checked = true;
    h.submit();
    assert.deepEqual(Array.from(h.requests[2].connection.instance_ids), ['paper', 'purpur']);
    h.scope('all'); h.scope('selected');
    assert.deepEqual(inputs.map(input => input.checked), [true, true, false, false]);
  }
});


test('Settings and provider command buttons reuse shared JustVoxel sizes and treatments', () => {
  const css = readFileSync(`${base}webui/internal/server/static/agent-gateway.css`, 'utf8');
  for (const [purpose, mode] of [['OpenAI', 'ChatGPT'], ['OpenAI', 'Codex'], ['GitHub Copilot', 'CLI'], ['GitHub Copilot', 'VS Code'], ['OpenClaw', 'Control UI'], ['OpenClaw', 'CLI']]) {
    const h = guideHarness(); h.render(purpose, {settings: true, mode});
    for (const button of descendants(h.host).filter(node => node.tagName === 'button')) {
      assert.equal(button.className, 'secondary', button.textContent);
    }
  }
  assert.match(form, /class="danger" data-gateway-delete/);
  assert.match(form, /<button type="submit" data-gateway-save>Create<\/button>/);
  // The shared primary treatment is the default button; secondary and danger change color only.
  for (const rule of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    if (/gateway-(?:settings-guide|actions-lane|save|guide-modes|guide-toolbar|address)/.test(rule[1]) &&
        /button|:is\([^)]*button/.test(rule[1])) {
      assert.doesNotMatch(rule[2], /(?:padding(?:-[\w-]+)?|(?:min-|max-)?height|font(?:-size)?)\s*:/);
    }
  }
  assert.doesNotMatch(css, /gateway-settings-guide\s*\{[^}]*font(?:-size)?\s*:/);
});

test('OAuth and token connections retain adjacent Delete and Apply with note before the group', () => {
  const group = form.match(/<div class="action-row gateway-save">([^]*?)<\/div>/)[1];
  assert.deepEqual([...group.matchAll(/<button[^>]*data-gateway-(\w+)[^>]*>/g)].map(match => match[1]), ['delete', 'save']);
  assert.ok(form.indexOf('data-gateway-live-note') < form.indexOf('action-row gateway-save'));
  for (const auth_mode of ['oauth_local', 'token']) {
    const h = settingsHarness({id: 'one', purpose: 'OpenAI', auth_mode, name: 'Home', level: 1, enabled: true, all_instances: true});
    assert.equal(h.node('[data-gateway-delete]').hidden, false);
    assert.equal(h.node('[data-gateway-save]').textContent, 'Apply');
    assert.equal(h.node('[data-gateway-actions]').hidden, auth_mode !== 'token');
    for (const action of ['rotate', 'revoke']) assert.equal(h.node(`[data-gateway-${action}]`).hidden, auth_mode !== 'token');
  }
  const add = settingsHarness({purpose: 'OpenAI', enabled: true, all_instances: true});
  assert.equal(add.node('[data-gateway-delete]').hidden, true);
  assert.equal(add.node('[data-gateway-save]').textContent, 'Create');
});

test('expiration is the single editable centered metric and keeps local draft and ISO submission', () => {
  const css = readFileSync(`${base}webui/internal/server/static/agent-gateway.css`, 'utf8');
  assert.equal((form.match(/name="expires_at"/g) || []).length, 1);
  assert.match(form, /class="gateway-expiration" type="datetime-local" name="expires_at" aria-label="Expiration"/);
  assert.doesNotMatch(form, /<label[^>]*>Expiration/);
  assert.doesNotMatch(form.slice(form.indexOf('<details data-gateway-advanced')), /expires_at/);
  assert.match(css, /gateway-status-metrics > div \{[^}]*text-align: center/);
  for (const columns of [5, 3, 2]) assert.ok(css.includes(`grid-template-columns: repeat(${columns}, minmax(0, 1fr))`));
  assert.match(css, /@container gateway \(max-width: 420px\) \{\s*[^{}]+gateway-status-metrics \{ grid-template-columns: minmax\(0, 1fr\)/);
  const expires_at = '2026-11-01T16:30:00.000Z';
  for (const id of ['one']) {
    const connection = {id, name: 'Home', purpose: 'OpenAI', auth_mode: 'oauth_local', enabled: true, all_instances: true, expires_at};
    const h = settingsHarness(connection);
    const date = new Date(expires_at);
    assert.equal(h.field('expires_at').value, new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 16));
    assert.equal(h.node('[data-gateway-status-details]').children[0].children[1], h.field('expires_at'));
    h.field('expires_at').value = '2026-11-02T09:45';
    assert.equal(connection.expires_at, expires_at);
    assert.equal(h.requests.length, 0);
    h.submit();
    assert.equal(h.requests[0].connection.expires_at, new Date('2026-11-02T09:45').toISOString());
    h.field('expires_at').value = ''; h.submit();
    assert.equal(Object.hasOwn(h.requests[1].connection, 'expires_at'), false);
  }
});

test('client modes default correctly and never mutate or persist connection changes', () => {
  for (const [purpose, modes] of [['OpenAI', ['ChatGPT', 'Codex']], ['Claude', ['Claude', 'Claude Code']], ['GitHub Copilot', ['CLI', 'VS Code']], ['OpenClaw', ['Control UI', 'CLI']]]) {
    const connection = {id: 'jvc_current', name: 'Home', purpose, auth_mode: 'oauth_local', level: 1, enabled: true, all_instances: true};
    const before = structuredClone(connection);
    const h = settingsHarness(connection);
    const host = h.node('[data-gateway-status]').afterChildren[0];
    const tabs = () => descendants(host).find(node => node.className === 'gateway-guide-modes');
    assert.deepEqual(tabs().children.map(node => node.textContent), modes);
    assert.equal(tabs().children[0]['aria-pressed'], 'true');
    for (const mode of [...modes].reverse().concat(modes)) {
      tabs().children.find(node => node.textContent === mode).listeners.click();
      assert.equal(tabs().children.filter(node => node['aria-pressed'] === 'true').length, 1);
      assert.equal(tabs().children.find(node => node.textContent === mode)['aria-pressed'], 'true');
      assert.equal(h.requests.length, 0);
      assert.deepEqual(connection, before);
    }
    h.submit();
    assert.equal(h.requests[0].connection.purpose, purpose);
    assert.equal(h.requests[0].connection.id, 'jvc_current');
    assert.equal(h.requests[0].connection.auth_mode, 'oauth_local');
    assert.equal(Object.hasOwn(h.requests[0].connection, 'mode'), false);
  }
  assert.doesNotMatch(js, /localStorage|sessionStorage/);
});

test('OpenClaw local modes offer current Control UI and CLI setup and OAuth commands', async () => {
  const h = guideHarness();
  for (const options of [{settings: true}, {help: true}]) {
    const ui = h.render('OpenClaw', {...options, mode: 'Control UI'});
    for (const phrase of ['Control UI → Settings → MCP', 'Configured servers', 'Add server', 'Name: JustVoxel', 'Transport: Streamable HTTP', 'OAuth', 'Sign in', 'openclaw mcp doctor justvoxel --probe']) assert.ok(ui.includes(phrase), phrase);
    const cli = h.render('OpenClaw', {...options, mode: 'CLI'});
    for (const phrase of ['openclaw mcp add justvoxel', '--url', '--transport streamable-http', 'openclaw mcp set justvoxel', '"auth":"oauth"', 'openclaw mcp login justvoxel', 'openclaw mcp doctor justvoxel --probe', 'openclaw mcp status --verbose']) assert.ok(cli.includes(phrase), phrase);
    assert.doesNotMatch(cli, /Connection prerequisite/);
    if (options.settings) assert.doesNotMatch(cli, /public|Internet/);
    if (options.settings) {
      const command = descendants(h.host).find(node => node.value?.startsWith('openclaw mcp add'));
      assert.equal(command.value, 'openclaw mcp add justvoxel --url http://192.168.1.20:8099/mcp/jvc_current --transport streamable-http');
      await command.parentNode.children[1].listeners.click();
      assert.equal(h.copied[0], command.value);
    }
  }
});

test('OpenClaw Gateway may reach private endpoints without a public internet prerequisite', () => {
  for (const origin of ['http://192.168.1.20', 'https://192.168.1.20', 'http://10.0.0.2', 'http://localhost']) {
    const h = guideHarness(origin);
    for (const mode of ['Control UI', 'CLI']) {
      for (const options of [{settings: true}, {help: true}]) {
        const content = h.render('OpenClaw', {...options, mode});
        assert.ok(content.includes('This local address can work when the OpenClaw Gateway can reach JustVoxel.'));
        assert.doesNotMatch(content, /cannot reach|Connection prerequisite/);
        if (options.settings) assert.doesNotMatch(content, /public|Internet/);
        if (options.help && mode === 'Control UI') assert.ok(content.includes('+ → Plugins → Add MCP server…'));
      }
    }
  }
});

test('Copilot CLI and VS Code use private HTTP URLs and distinguish command transport', () => {
  const h = guideHarness();
  const cli = h.render('GitHub Copilot', {settings: true});
  for (const phrase of ['copilot mcp add --transport http justvoxel http://192.168.1.20:8099/mcp/jvc_current', '/mcp add', 'Ctrl+S', '/mcp auth justvoxel', 'OAuth re-authentication', 'copilot mcp list', 'STDIO is for launching a local MCP process']) assert.ok(cli.includes(phrase), phrase);
  const code = h.render('GitHub Copilot', {settings: true, mode: 'VS Code'});
  const steps = descendants(h.host).filter(node => node.tagName === 'li').map(node => node.textContent);
  assert.deepEqual(steps, ['Open Command Palette.', 'Run MCP: Add Server.', 'Choose HTTP.', 'Paste the JustVoxel MCP endpoint.', 'Enter server ID: justvoxel.', 'Choose Global.', 'Choose Copilot Global.', 'Complete OAuth/browser authentication if requested.']);
  assert.ok(code.includes('HTTP is correct because JustVoxel provides a URL'));
  assert.ok(code.includes('Command/stdio is for launching a local MCP process'));
  for (const content of [cli, code]) {
    assert.doesNotMatch(content, /Connection prerequisite|public.endpoint|cannot reach/);
    assert.ok(content.includes('Repository-level Copilot cloud agent and code review do not currently support remote OAuth MCP servers.'));
  }
});

test('Claude Code stays local-capable with HTTP OAuth and never inherits cloud prerequisites', () => {
  const h = guideHarness();
  for (const options of [{settings: true}, {help: true}]) {
    const content = h.render('Claude', {...options, mode: 'Claude Code'});
    for (const phrase of ['claude mcp add --transport http justvoxel', 'claude mcp login justvoxel', 'claude mcp get justvoxel', 'claude mcp list', 'machine where the CLI is running', 'HTTP servers support OAuth', 'No static bearer token', '/mcp']) assert.ok(content.includes(phrase), phrase);
    assert.doesNotMatch(content, /Anthropic infrastructure|Connection prerequisite|remotely reachable|public.endpoint/);
    if (options.settings) {
      assert.ok(descendants(h.host).some(node => node.value === 'http://192.168.1.20:8099/mcp/jvc_current'));
      assert.ok(descendants(h.host).some(node => node.value === 'claude mcp add --transport http justvoxel http://192.168.1.20:8099/mcp/jvc_current'));
    }
  }
});

test('OpenClaw preserves an existing external OAuth connection on Apply', () => {
  const oauth = {issuer: 'https://issuer.example', audience: 'https://voxel.example/mcp', jwks_uri: 'https://issuer.example/keys', scope: 'justvoxel:connect', subject: 'user', client_id: 'openclaw'};
  const h = settingsHarness({id: 'legacy-openclaw', name: 'OpenClaw', purpose: 'OpenClaw', auth_mode: 'oauth', oauth, level: 1, enabled: true, all_instances: true});
  h.submit();
  assert.equal(h.requests[0].action, 'apply');
  assert.equal(h.requests[0].connection.auth_mode, 'oauth');
  assert.deepEqual({...h.requests[0].connection.oauth}, oauth);
});


test('overview ellipsis shares the Minecraft summary style without a bottom action row', () => {
  const renderer = js.slice(js.indexOf('  const renderCards ='), js.indexOf('  // Presentation only:'));
  const server = readFileSync(`${base}webui/internal/server/templates/minecraft_servers.html`, 'utf8');
  const style = readFileSync(`${base}webui/internal/server/static/storage-browser.css`, 'utf8');
  const css = readFileSync(`${base}webui/internal/server/static/agent-gateway.css`, 'utf8');
  assert.match(server, /class="storage-action-menu minecraft-server-more"/);
  assert.match(renderer, /actions.className = 'storage-action-menu minecraft-server-more'/);
  assert.match(renderer, /text\('summary', '⋯'\)/);
  assert.match(style, /\.storage-action-menu>summary\{/);
  assert.match(renderer, /identity.append\(provider, actions\)/);
  assert.doesNotMatch(renderer + css, /gateway-card-actions/);
  assert.doesNotMatch(renderer, /text\('button', 'Settings'\)/);
  assert.match(css, /minecraft-server-software-row > p \{ align-items: center;/);
  assert.match(css, /minecraft-server-software-row \.gateway-provider-icon \{ width: 1.1rem; height: 1.1rem;/);
});

test('create and edit use one configuration template, provider list and scope helper', () => {
  assert.equal((header.match(/<template data-gateway-settings-template>/g) || []).length, 1);
  for (const name of ['name', 'level', 'enabled']) assert.equal((form.match(new RegExp(`name="${name}"`, 'g')) || []).length, 1);
  assert.equal((form.match(/data-gateway-providers/g) || []).length, 1);
  assert.equal((js.match(/renderMinecraftScope\(form, c\)/g) || []).length, 1);
  for (const id of [undefined, 'existing']) {
    const h = settingsHarness({id, name: 'Home', purpose: 'OpenAI', level: 1, enabled: true, all_instances: true});
    assert.equal(h.settings.className, id ? 'gateway-edit-settings' : 'gateway-add-settings');
    assert.equal(h.node('[data-gateway-status]').hidden, !id);
    assert.equal(h.node('[data-gateway-advanced]').hidden, !id);
    assert.equal(h.node('[data-gateway-delete]').hidden, !id);
    assert.equal(Boolean(h.node('[data-gateway-status]').afterChildren), Boolean(id));
    assert.equal(h.node('[data-gateway-status-details]').children.length, id ? 5 : 0);
    assert.equal(h.node('[data-gateway-save]').textContent, id ? 'Apply' : 'Create');
    h.submit();
    assert.equal(h.requests[0].action, id ? 'apply' : 'create');
  }
});

test('default Help is a reference with provider manuals and developer integration collapsed', () => {
  const referenceStart = help.indexOf('<details class="panel" data-gateway-provider-reference>');
  assert.ok(referenceStart > 0);
  const normal = help.slice(0, referenceStart);
  assert.doesNotMatch(normal, /Getting connected|Choose your app|data-gateway-help-providers|data-gateway-help-guide|How to connect/);
  for (const section of ['Access levels', 'Minecraft scope', 'Authentication', 'Network access']) assert.ok(normal.includes(`<h3>${section}</h3>`));
  for (const phrase of ['Selected servers', 'OAuth connections', 'Token connections', 'shown once', 'Local clients', 'Cloud clients']) assert.ok(normal.includes(phrase));
  const local = normal.match(/<dt>Local clients<\/dt><dd>(.*?)<\/dd>/)[1];
  for (const name of ['Codex', 'Claude Code', 'GitHub Copilot CLI', 'VS Code', 'OpenClaw', 'other local MCP clients']) assert.ok(local.includes(name), name);
  assert.match(local, /machine running the client can reach JustVoxel/);
  const cloud = normal.match(/<dt>Cloud clients<\/dt><dd>(.*?)<\/dd>/)[1];
  for (const name of ['ChatGPT', 'Claude remote connectors', 'Grok', 'Gemini']) assert.ok(cloud.includes(name), name);
  assert.match(cloud, /run outside your LAN/);
  const reference = help.slice(referenceStart, help.indexOf('<details class="panel"><summary>Developer integration'));
  assert.match(reference, /<summary>Provider reference<\/summary>/);
  assert.match(reference, /data-gateway-help-providers/);
  assert.match(reference, /data-gateway-help-guide/);
  assert.doesNotMatch(help, /<details[^>]*\bopen\b/);
  const h = guideHarness(); h.context.setup();
  const buttons = h.find('[data-gateway-help-providers]').children;
  for (const button of buttons) {
    button.listeners.click();
    assert.ok(h.find('[data-gateway-help-guide]').children.length > 0);
  }
  for (const [provider, phrases] of [['OpenAI', ['ChatGPT', 'Codex']], ['Claude', ['Claude', 'Claude Code']], ['GitHub Copilot', ['CLI', 'VS Code']], ['OpenClaw', ['Control UI', 'CLI']]]) {
    const content = h.render(provider, {help: true});
    for (const phrase of phrases) assert.ok(content.includes(phrase), provider + ': ' + phrase);
  }
});

test('ChatGPT Help documents both connection architectures for private and public endpoints', () => {
  for (const origin of ['http://192.168.1.20:8099', 'https://voxel.example']) {
    const h = guideHarness(origin);
    const content = h.render('OpenAI', {help: true, mode: 'ChatGPT'});
    for (const phrase of [
      'Option 1 — Secure MCP Tunnel', 'Option 2 — Public HTTPS endpoint',
      'ChatGPT → OpenAI Secure MCP Tunnel → tunnel-client on a machine that can reach JustVoxel → private JustVoxel MCP endpoint',
      'outbound connection to OpenAI', 'can remain private on the LAN',
      'Inbound exposure of port 8099 is not required merely for the tunnel',
      'OpenAI Platform/workspace availability and permissions',
      'secure public HTTPS endpoint reachable by ChatGPT', 'custom MCP/plugin with that HTTPS endpoint',
      'JustVoxel OAuth remains the authentication flow',
    ]) assert.ok(content.includes(phrase), phrase);
    const urls = descendants(h.host).filter(node => node.tagName === 'a').map(node => node.href);
    assert.ok(urls.includes('https://developers.openai.com/api/docs/guides/secure-mcp-tunnels'));
    assert.ok(urls.includes('https://help.openai.com/en/articles/12584461-connecting-custom-mcp-servers-in-chatgpt'));
    const codex = h.render('OpenAI', {help: true, mode: 'Codex'});
    assert.ok(codex.includes('codex mcp add justvoxel --url <MCP_URL>'));
    assert.ok(codex.includes('codex mcp list'));
    assert.ok(codex.includes('JustVoxel LAN MCP URL directly when the machine running Codex can reach JustVoxel'));
    assert.doesNotMatch(codex, /Secure MCP Tunnel|secure-mcp-tunnels/);
  }
});

test('Help distinguishes cloud prerequisites from local Claude, Copilot and OpenClaw connections', () => {
  for (const origin of ['http://192.168.1.20:8099', 'https://voxel.example']) {
    const h = guideHarness(origin);
    const claude = h.render('Claude', {help: true});
    for (const phrase of ['requests originate from Anthropic infrastructure', 'private JustVoxel LAN URL is not directly reachable', 'endpoint reachable from Anthropic infrastructure']) assert.ok(claude.includes(phrase), phrase);
    assert.doesNotMatch(claude, /tunnel/i);
    const code = h.render('Claude', {help: true, mode: 'Claude Code'});
    assert.ok(code.includes('machine where the CLI is running'));
    assert.ok(code.includes('Claude Code is a local client'));
    assert.ok(code.includes('JustVoxel LAN HTTP MCP endpoint when its machine can reach JustVoxel'));
    assert.doesNotMatch(code, /Anthropic infrastructure|Connection prerequisite/);
    const grok = h.render('Grok', {help: true});
    for (const phrase of ['public Internet reachability', 'Private LAN URLs do not work directly', 'xAI documents tunneling for local MCP servers']) assert.ok(grok.includes(phrase), phrase);
    assert.ok(descendants(h.host).some(node => node.href === 'https://docs.x.ai/grok/connectors/custom-mcp-tunneling'));
    if (origin.startsWith('http:')) assert.doesNotMatch(grok, /192\.168\.1\.20/);
    const gemini = h.render('Gemini', {help: true});
    for (const phrase of ['publicly reachable HTTPS Streamable HTTP MCP endpoint', 'Private LAN HTTP does not qualify', 'does not provide an automatic Google tunnel', 'Dynamic Client Registration']) assert.ok(gemini.includes(phrase), phrase);
    assert.ok(descendants(h.host).some(node => node.href === 'https://developers.google.com/connected-apps/gpc/prerequisites'));
    for (const mode of ['CLI', 'VS Code']) {
      const copilot = h.render('GitHub Copilot', {help: true, mode});
      assert.ok(copilot.includes('Their machine must be able to reach JustVoxel, so a private LAN URL may work directly'));
      assert.doesNotMatch(copilot, /Connection prerequisite/);
    }
    for (const mode of ['Control UI', 'CLI']) {
      const claw = h.render('OpenClaw', {help: true, mode});
      for (const phrase of ['OpenClaw Gateway owns the MCP connection', 'Gateway can reach JustVoxel on the LAN', 'use the private endpoint directly', 'Public Internet exposure is not required']) assert.ok(claw.includes(phrase), phrase);
      assert.doesNotMatch(claw, /Connection prerequisite/);
    }
  }
});


test('copy feedback has no visible status row and reserves intrinsic label width with standard sizing', async () => {
  const h = guideHarness(); h.render('OpenAI', {settings: true, mode: 'Codex'});
  const count = h.host.children.length;
  const row = h.host.children[0];
  const button = row.children[2];
  await button.listeners.click();
  assert.equal(button.textContent, 'Copied');
  assert.equal(button.className, 'secondary');
  assert.equal(h.host.children.length, count);
  assert.equal(row.children.length, 3);
  for (const node of descendants(h.host).filter(node => node.role === 'status')) assert.equal(node.className, 'gateway-visually-hidden');
  const css = readFileSync(`${base}webui/internal/server/static/agent-gateway.css`, 'utf8');
  assert.match(css, /\[data-gateway-copy-label\] \{ display: inline-grid; white-space: nowrap;/);
  assert.match(css, /content: attr\(data-gateway-copy-label\); visibility: hidden; height: 0/);
  assert.match(header, /class="gateway-visually-hidden" data-gateway-address-status role="status"/);
  const copyRule = css.match(/\[data-gateway-copy-label\] \{([^}]+)\}/)[1];
  assert.doesNotMatch(copyRule, /padding|font|height/);
});
