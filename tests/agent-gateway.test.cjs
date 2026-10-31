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
  append(...children) { this.children.push(...children); }
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
  for (const section of ['Getting started', 'Providers / connection types', 'Access levels', 'Minecraft scope', 'Connection / login', 'Remote access']) assert.ok(help.includes(`<h3>${section}</h3>`), section);
  assert.match(help, /<ol class="gateway-help-steps">/);
  assert.equal((help.match(/<li>/g) || []).length, 5);
  const table = help.match(/<table[\s\S]*?<\/table>/)[0];
  for (const label of ['Level', 'Name', 'Can do', 'Read Only', 'Basic Management', 'Full Management', 'Full System']) assert.ok(table.includes(`>${label}<`), label);
  assert.equal((table.match(/<tr>/g) || []).length, 5);
  assert.match(help, /No level can manage AI\/API connection permissions, user accounts, or Factory Reset/);
  assert.match(help, /Remote\/cloud AI clients need a reachable HTTPS MCP address/);
  assert.match(help, /Never expose port 8099 directly to the public Internet/);
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
  for (const section of ['Status', 'Actions', 'Advanced details', 'Connection ID', 'Safe credential fingerprint']) assert.ok((form + js).includes(section));
});

test('normal help keeps raw endpoints and integration details collapsed at the bottom', () => {
  const normal = help.slice(0, help.indexOf('<details class="panel"><summary>Developer integration'));
  assert.doesNotMatch(normal, /OAuth|JWKS|issuer|audience|subject|client ID|data-gateway-mcp|data-gateway-rest/);
  assert.match(help, /<details class="panel"><summary>Developer integration<\/summary>/);
  assert.doesNotMatch(help, /<details[^>]* open/);
  assert.ok(help.indexOf('Remote access') < help.indexOf('Developer integration'));
  for (const phrase of ['supported OpenAI MCP connection', 'Claude custom connector', 'supported Grok custom MCP connector', 'account/application', 'custom MCP server', 'compatible MCP client', 'REST address and JustVoxel connection token', 'account, plan or subscription, and network access']) assert.ok(normal.includes(phrase), phrase);
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
function settingsHarness(connection) {
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
    if (!fields.has(name)) { const input = new Node('input'); input.value = ''; fields.set(name, input); }
    return fields.get(name);
  };
  const settings = new Node('form');
  settings.elements = {namedItem: field};
  settings.querySelector = node;
  settings.querySelectorAll = () => [];
  settings.reportValidity = () => true;
  const details = new Node('section');
  details.querySelector = () => settings;
  const requests = [];
  const confirmations = [];
  const context = vm.createContext({
    details, structuredClone, busy: false, instances: [],
    location: {origin: 'https://voxel.example', protocol: 'https:', hostname: 'voxel.example'}, navigator: {clipboard: {async writeText() {}}},
    content: node('[data-gateway-content]'), find: node,
    document: {createElement: tag => new Node(tag), querySelector: () => ({content: {cloneNode() { return new Node('form'); }}})},
    text: (tag, value) => new Node(tag, value), activityLabel: value => value || 'No activity yet',
    mutate: payload => requests.push(payload), requestLevel4: payload => confirmations.push(payload),
  });
  const helpers = js.slice(js.indexOf('  const providers ='), js.indexOf('  const activityLabel'));
  const rendererStart = js.indexOf('  const renderSettings =');
  const rendererEnd = js.indexOf("  find('[data-gateway-add]').addEventListener", rendererStart);
  const renderer = js.slice(rendererStart, rendererEnd);
  const surfaces = js.slice(js.indexOf("  let selectedID ="), js.indexOf('  const levelLabel ='));
  const navigation = js.slice(rendererEnd, js.indexOf("  for (const button of dialog.querySelectorAll('[data-gateway-copy-address]'))", rendererEnd));
  details.hidden = true;
  node('[data-gateway-overview]').hidden = false;
  node('[data-gateway-help]').hidden = true;
  vm.runInContext(surfaces + helpers + renderer + navigation + '\nglobalThis.render = renderSettings;', context);
  if (connection) context.render(connection);
  return {node, field, details, requests, confirmations, submit() { settings.listeners.submit({preventDefault() {}}); }};
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
  assert.match(css, /\.gateway-name-access-row \{[^}]*grid-template-columns: minmax\(0, 3fr\) minmax\(0, 2fr\)/);
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
  const onboarding = h.node('[data-gateway-settings-title]').afterChildren[0];
  const descendants = node => [node, ...node.children.flatMap(descendants)];
  const nodes = descendants(onboarding);
  for (const value of ['Ready to connect', 'MCP address', 'https://voxel.example/mcp/jvc_example', 'Copy address', 'Sign in with JustVoxel', 'When OpenAI connects, JustVoxel will open the secure sign-in and approval page.']) assert.ok(nodes.some(node => node.textContent === value), value);
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

test('normal actions match authentication type and maintenance stays under Advanced', () => {
  const actions = form.slice(form.indexOf('<section data-gateway-actions'), form.indexOf('<p data-gateway-live-note'));
  assert.deepEqual([...actions.matchAll(/<button[^>]*>([^<]+)<\/button>/g)].map(match => match[1]), ['Rotate token', 'Revoke token', 'Delete']);
  assert.doesNotMatch(actions, /allowance|usage limits|Reset restart/);
  const advanced = form.match(/<details data-gateway-advanced[\s\S]*?<\/details>/)[0];
  assert.match(advanced, /data-gateway-allowance>Reset usage limits/);
  assert.match(advanced, /Reset the restart and backup usage limits for this connection/);
  for (const auth_mode of ['token', 'oauth_local']) {
    const h = settingsHarness({id: 'one', name: 'App', purpose: 'OpenAI', auth_mode, level: 1, enabled: true, all_instances: true});
    for (const action of ['rotate', 'revoke']) assert.equal(h.node(`[data-gateway-${action}]`).hidden, auth_mode !== 'token');
    h.node('[data-gateway-allowance]').listeners.click();
    assert.equal(h.requests[0].action, 'reset-allowance');
  }
});
