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
  addEventListener(name, callback) { this.listeners[name] = callback; }
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
  const start = js.indexOf('  const renderCards = () => {');
  const end = js.indexOf('  const renderSettings = ', start);
  assert.ok(start > 0 && end > start);
  const cards = new Node('div');
  const connections = [
    {id: 'first', name: 'AI agent', purpose: 'AI Agent', level: 1, enabled: true, auth_mode: 'token'},
    {id: 'second', name: 'Automation', purpose: 'API Client', level: 3, enabled: false, auth_mode: 'oauth'},
    {id: 'third', name: '<script>untrusted label</script>', level: 2, enabled: true, auth_mode: 'token'},
  ];
  const selected = [];
  const context = vm.createContext({snapshot: {connections}, cards, busy: false, selectedID: '', renderSettings: c => selected.push(c.id), providerIcon: () => new Node('img'), purposeLabel: value => value || 'Other', levelLabel: value => `Level ${value}`, scopeLabel: () => 'All Minecraft servers', activityLabel: value => value || 'No activity yet', text: (tag, value) => new Node(tag, value)});
  vm.runInContext(js.slice(start, end) + '\nrenderCards();', context);
  assert.equal(cards.children.length, 3);
  for (const [index, card] of cards.children.entries()) {
    assert.equal(card.dataset.gatewayConnection, connections[index].id);
    assert.equal(card.children[0].children[0].children[1].children[0].textContent, connections[index].name);
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

test('compact top lane orders Add Connection before Connection help above the default card overview', () => {
  const lane = workspace.match(/<nav class="minecraft-workspace-tabs"[^>]*>([\s\S]*?)<\/nav>/)[1];
  assert.ok(lane.indexOf('data-gateway-add') < lane.indexOf('data-gateway-help-open'));
  assert.ok(workspace.indexOf('</nav>') < workspace.indexOf('data-gateway-overview'));
  assert.match(overview, /minecraft-server-inventory/);
  assert.match(overview, /minecraft-server-entries" data-gateway-cards/);
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

test('help explains all access levels and connection choices in plain language', () => {
  for (const phrase of ['Choose your app', 'OpenAI — ChatGPT &amp; Codex', 'Claude', 'Grok', 'Gemini', 'GitHub Copilot', 'Other AI / MCP app', 'Automation / REST API', 'Level 1 — Read Only', 'Cannot change anything.', 'Level 2 — Basic Management', 'Stop is not included.', 'Level 3 — Full Management', 'Level 4 — Full System Control', 'Only enable this for a connection you fully trust.', 'JustVoxel connection token', 'Never expose port 8099 directly to the public Internet.']) assert.ok(help.includes(phrase), phrase);
});

test('provider cards replace the purpose dropdown with one OpenAI choice for ChatGPT and Codex', () => {
  const purposes = [...form.matchAll(/type="radio" name="purpose" value="([^"]+)"/g)].map(match => match[1]);
  assert.deepEqual(purposes, ['OpenAI', 'Claude', 'Grok', 'Gemini', 'GitHub Copilot', 'Other AI / MCP app', 'Automation / REST API']);
  assert.equal((form.match(/<strong>OpenAI<\/strong>/g) || []).length, 1);
  assert.match(form, /<strong>OpenAI<\/strong><small>ChatGPT &amp; Codex<\/small>/);
  assert.doesNotMatch(form, /<select name="purpose"|name="auth_mode"|Connection method|OAuth — Advanced|What do you want to call it/);
  for (const phrase of ['>Name</span>', 'What will use this connection?', 'How much access should it have?', 'Which Minecraft servers?', 'All servers', 'Choose servers']) assert.ok(form.includes(phrase), phrase);
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
  assert.match(css, /repeat\(3, minmax\(0, 1fr\)\)/);
  assert.match(css, /@media \(max-width: 520px\)/);
});

test('normal creation uses the existing purpose field and always uses a token', () => {
  assert.match(js, /renderSettings\(\{name: '', purpose: 'OpenAI', auth_mode: 'token', level: 1, enabled: true, all_instances: true, instance_ids: \[\]\}\)/);
  assert.match(js, /purpose: field\('purpose'\)\.value\.trim\(\)/);
  assert.match(js, /auth_mode: c\.id \? c\.auth_mode : 'token'/);
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
  assert.match(js, /text\('p', purposeLabel\(connection\.purpose\)\)/);
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

test('normal help is provider first and technical integration is collapsed at the bottom', () => {
  const normal = help.slice(0, help.indexOf('<details class="panel"><summary>Developer integration'));
  assert.doesNotMatch(normal, /OAuth|JWKS|issuer|audience|subject|client ID/);
  assert.match(help, /<details class="panel"><summary>Developer integration<\/summary>/);
  assert.doesNotMatch(help, /<details[^>]* open/);
  assert.ok(help.indexOf('Remote access and security') < help.indexOf('Developer integration'));
  for (const phrase of ['supported OpenAI MCP connection', 'Claude custom connector', 'supported Grok custom MCP connector', 'user’s account/application', 'configuring a custom MCP server', 'compatible Streamable HTTP MCP client', 'REST address and JustVoxel connection token', 'account, plan or subscription, and network access']) assert.ok(normal.includes(phrase), phrase);
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
    details, structuredClone, busy: false, instances: [], showSurface() {}, showOverview() {},
    document: {createElement: tag => new Node(tag), querySelector: () => ({content: {cloneNode() { return new Node('form'); }}})},
    text: (tag, value) => new Node(tag, value), activityLabel: value => value || 'No activity yet',
    mutate: payload => requests.push(payload), requestLevel4: payload => confirmations.push(payload),
  });
  const helpers = js.slice(js.indexOf('  const providers ='), js.indexOf('  const activityLabel'));
  const rendererStart = js.indexOf('  const renderSettings =');
  const rendererEnd = js.indexOf("  find('[data-gateway-add]').addEventListener", rendererStart);
  const renderer = js.slice(rendererStart, rendererEnd);
  vm.runInContext(helpers + renderer + '\nglobalThis.render = renderSettings;', context);
  context.render(connection);
  return {node, field, requests, confirmations, submit() { settings.listeners.submit({preventDefault() {}}); }};
}

test('new provider selections submit a token connection and keep OAuth fields out of the form', () => {
  for (const purpose of ['OpenAI', 'Claude', 'Grok', 'Gemini', 'GitHub Copilot', 'Other AI / MCP app', 'Automation / REST API']) {
    // Even an unexpected caller-supplied mode cannot enable OAuth on creation.
    const h = settingsHarness({name: 'My AI Assistant', purpose: 'OpenAI', auth_mode: 'oauth', level: 1, enabled: true, all_instances: true, instance_ids: []});
    assert.equal(h.node('[data-gateway-oauth]').removed, true);
    assert.equal(h.node('[data-gateway-advanced]').hidden, true);
    h.field('purpose').value = purpose;
    h.submit();
    assert.equal(h.requests.length, 1);
    assert.equal(h.requests[0].action, 'create');
    assert.equal(h.requests[0].connection.auth_mode, 'token');
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
