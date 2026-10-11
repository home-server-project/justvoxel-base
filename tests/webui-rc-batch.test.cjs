const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { test } = require('node:test');
const vm = require('node:vm');
const path = require('node:path');
const repo = path.join(__dirname, '..');
const read = name => readFileSync(path.join(repo, name), 'utf8');
const app = read('webui/internal/server/static/app.js');
const css = read('webui/internal/server/static/app.css');

class Element {
  constructor(tag) { this.tag = tag; this.children = []; this.dataset = {}; this.events = {}; this.attrs = {}; this.style = { setProperty() {} }; }
  append(...children) { this.children.push(...children); }
  appendChild(child) { this.append(child); }
  replaceChildren(...children) { this.children = children; }
  setAttribute(name, value) { this.attrs[name] = value; }
  addEventListener(name, handler) { this.events[name] = handler; }
  click() { return this.events.click?.(); }
  all() { return [this, ...this.children.flatMap(child => child.all())]; }
  querySelectorAll(tag) { return this.all().filter(node => node.tag === tag); }
}

test('Control Center offsets only desktop padding/border and preserves compact panel and green frame', () => {
  assert.ok(css.includes('@media(min-width:601px){.app-header .control-center{margin-right:calc(-.5rem - 1px)}}'));
  assert.match(css, /\.app-header\.topbar\{[^}]*padding:2px \.5rem;[^}]*border:1px solid/);
  const declarations = block => Object.fromEntries(block.split(';').filter(Boolean).map(value => value.trim().split(':')));
  const topbar = declarations([...css.matchAll(/\.app-header\.topbar\{([^}]*)\}/g)].findLast(match => match[1].includes('height:40px'))[1]);
  const desktop = css.match(/@media\(min-width:601px\)\{\s*\.app-header \.control-center\{([^}]*)\}\s*\.app-header \.control-center-trigger\{([^}]*)\}\s*\}/);
  assert.ok(desktop, 'frame geometry must be restricted to desktop widths above 600px');
  const wrapper = declarations(desktop[1]);
  const trigger = declarations(desktop[2]);
  const compactTrigger = declarations(css.match(/\.app-header \.control-center-trigger\{([^}]*height:32px;[^}]*)\}/)[1]);
  assert.ok(css.includes('*{box-sizing:border-box}'));
  assert.match(css, /\.topbar\{[^}]*align-items:center;/);
  assert.equal(trigger.position, 'relative');
  assert.equal(wrapper.height, compactTrigger.height, 'preserve the panel containing block height');
  assert.equal(topbar.height, '40px', 'preserve the topbar outer height');
  assert.equal(trigger['min-height'], trigger.height);
  // Grid centers the wrapper inside the border-box content area. The relative
  // trigger offset must reach both outer border lanes without moving the panel.
  const height = parseFloat(topbar.height);
  const inset = parseFloat(topbar.border) + parseFloat(topbar.padding);
  const wrapperTop = inset + (height - 2 * inset - parseFloat(wrapper.height)) / 2;
  const triggerTop = wrapperTop + parseFloat(trigger.top);
  assert.equal(triggerTop, 0, 'green top border shares the topbar top border lane');
  assert.equal(triggerTop + parseFloat(trigger.height), height, 'green bottom border shares the topbar bottom border lane');
  assert.match(css, /\.control-center-panel\{position:absolute;[^}]*right:0;/);
  assert.ok(css.includes('.control-center-panel{position:fixed;top:50px;left:.45rem;right:.45rem;bottom:.45rem;width:auto;'));
  assert.ok(css.includes('.app-header.topbar{margin:4px;gap:.2rem;padding:2px .3rem}'));
  assert.ok(css.includes('.app-header .control-center-trigger:hover,.app-header .control-center-trigger:focus-visible,.app-header .control-center[open]>.control-center-trigger{border-color:#4f8b69}'));
  assert.ok(css.includes('.server-software-card.is-selected,'));
  assert.ok(!css.includes('.version-software-choice.is-current'));
  assert.ok(!css.includes('.server-software-card.is-current'));
});

test('Export Done clears operation navigation, selects Export, and reopening loads fresh Export', async () => {
  const loads = [];
  const root = { addEventListener(name, handler) { this[name] = handler; }, contains: () => true };
  const location = { href: 'http://example/?workspace=migration&operation=old-operation', origin: 'http://example', pathname: '/', search: '?workspace=migration&operation=old-operation' };
  let windowOptions;
  const context = vm.createContext({
    URL, URLSearchParams, Object, root, currentTab: 'export', currentURL: '/workspace/migration/progress/old-operation',
    migrationDialog: {}, migrationSourceSMBPassword: '', migrationExportSMBPassword: '', migrationSubmitController: null,
    tabURLs: { export: '/workspace/migration/export', import: '/workspace/migration/import', recovery: '/workspace/migration/recovery' },
    window: { location, history: { state: {}, replaceState(_, __, next) { const url = new URL(next, location.href); location.href = url.href; location.search = url.search; } } },
    setupWorkspaceWindow: (_, options) => { windowOptions = options; return { close: options.onClose }; },
    syncMigrationTabs: () => { loads.push('select-export'); },
    loadMigration: async url => { loads.push(url); }, loadMigrationEntry: async () => { loads.push('normal-entry'); }, abortMigrationRequests() {},
  });
  const update = app.slice(app.indexOf('  const updateMigrationLocation ='), app.indexOf('  const setMigrationBusy ='));
  const start = app.indexOf('    root.addEventListener("click", async (event) => {', app.indexOf('const renderMigrationMarkup ='));
  const click = app.slice(start, app.indexOf('    initializeMigrationRoot(root);', start));
  const windowStart = app.indexOf('  const workspaceWindow = setupWorkspaceWindow(migrationDialog, {');
  const windowSource = app.slice(windowStart, app.indexOf('  migrationOpen.addEventListener', windowStart));
  vm.runInContext(update + windowSource + click, context);
  const link = { getAttribute: () => '/?workspace=migration&tab=export' };
  let prevented = false;
  await root.click({ target: { closest: () => link }, preventDefault() { prevented = true; } });
  assert.ok(prevented);
  assert.equal(location.search, '?workspace=migration&tab=export');
  assert.equal(context.currentTab, 'export');
  assert.equal(context.currentURL, '/workspace/migration/export');
  assert.deepEqual(loads, ['select-export', '/workspace/migration/export']);
  windowOptions.onClose();
  await windowOptions.onOpen();
  assert.equal(loads.at(-1), 'normal-entry');
  assert.ok(!loads.some(value => value.includes('/progress/')));
});

const logSource = app.slice(app.indexOf('  const systemLogDestination ='), app.indexOf('  const renderUsers =', app.indexOf('  const systemLogDestination =')));
async function openLogs(search, logs, live = {}) {
  const listCalls = [];
  const calls = [];
  const content = new Element('div');
  const context = vm.createContext({
    URLSearchParams, Number, JSON, Array, Math, String, encodeURIComponent, loadSequence: 1, content,
    window: { location: { search } }, document: { createElement: tag => new Element(tag) },
    localStorage: { getItem: () => null }, ResizeObserver: class { observe() {} disconnect() {} },
    systemSplitter: () => new Element('div'), systemFetchJSON: async url => { listCalls.push(url); return { logs, ...live }; }, systemHandleAuth: () => false,
    fetch: async url => { calls.push(url); return { ok: !live.journalError, text: async () => live.journalText || 'Export evidence' }; },
  });
  vm.runInContext(logSource + '\nglobalThis.renderLogs = renderLogs;', context);
  await context.renderLogs(1);
  await new Promise(resolve => setImmediate(resolve));
  return { content, calls, listCalls, render: async () => { await context.renderLogs(1); } };
}
const migrationLog = { category: 'migration', log_id: 'operation-1', operation_type: 'migration_export' };

test('Migration Logs deep link selects and reads only an entry returned by the authoritative list', async () => {
  const page = await openLogs('?workspace=system&tab=logs&log_group=migration&log_id=operation-1', [migrationLog]);
  assert.deepEqual(page.calls, ['/api/system/workspace/logs/migration/operation-1']);
  assert.equal(page.content.all().find(node => node.textContent === 'Migration').attrs['aria-pressed'], 'true');
  assert.equal(page.content.all().find(node => node.className === 'system-log-entry secondary').attrs['aria-pressed'], 'true');
  assert.ok(page.content.all().some(node => node.textContent === 'Export evidence'));
  await page.render();
  assert.equal(page.content.all().find(node => node.textContent === 'Setup').attrs['aria-pressed'], 'true');
  assert.equal(page.calls.length, 1);
});

test('unknown or unsafe deep-linked file never builds a read path; normal Logs still defaults to Setup', async () => {
  for (const id of ['missing', '../etc/passwd', '']) {
    const page = await openLogs('?workspace=system&tab=logs&log_group=migration&log_id=' + encodeURIComponent(id), [migrationLog]);
    assert.deepEqual(page.calls, []);
    assert.equal(page.content.all().find(node => node.textContent === 'Migration').attrs['aria-pressed'], 'true');
  }
  for (const search of ['', '?workspace=system&tab=logs', '?workspace=system&tab=logs&log_group=unknown&log_id=operation-1']) {
    const page = await openLogs(search, [migrationLog]);
    assert.deepEqual(page.calls, []);
    assert.equal(page.content.all().find(node => node.textContent === 'Setup').attrs['aria-pressed'], 'true');
  }
});

test('Export polling exposes Logs on success/failure/attention and Done only on success', () => {
  const source = read('webui/internal/server/static/server-migration-operation.js');
  const start = source.indexOf('  const render = (operation) => {');
  const render = source.slice(start, source.indexOf('  const poll = async () => {', start));
  for (const state of ['running', 'succeeded', 'rolled_back', 'failed', 'needs_attention']) {
    const dashboardLink = {}, exportLogDetails = {}, backLink = {};
    const context = vm.createContext({ supportedType: () => true, terminalState: () => false, operationName: () => 'Server Export', stateLabel: () => state, stageLabel: () => '', reconnectNote: null, nameText: null, statusText: null, stateBadge: null, stageText: null, successNote: null, rollbackNote: null, attentionNote: null, recoveryLink: null, dashboardLink, exportLogDetails, backLink });
    vm.runInContext(render + '\nrender({ operation_type: "migration_export", state: ' + JSON.stringify(state) + ' });', context);
    assert.equal(dashboardLink.hidden, state !== 'succeeded');
    assert.equal(dashboardLink.textContent, 'Done');
    assert.equal(dashboardLink.href, '/?workspace=migration&tab=export');
    assert.equal(exportLogDetails.hidden, state === 'running');
  }
  const template = read('webui/internal/server/templates/migration_workspace_progress.html');
  assert.ok(template.includes('workspace=system&amp;tab=logs&amp;log_group=migration&amp;log_id={{.Operation.OperationID}}'));
  assert.ok(template.includes('Details available on System -&gt; Logs -&gt; Migration.'));
});

test('WebUI authoritative release candidate version', () => {
  assert.equal(read('webui/VERSION').trim(), '1.0.0-rc.1');
});

test('recommended memory warning boundary is 1536 MiB and hard reserve remains 1024 MiB in both WebUI forms', () => {
  const settings = read('webui/internal/server/static/settings.js');
  const nativeStart = app.indexOf('  const initNativeMemoryControls =');
  const native = app.slice(nativeStart, app.indexOf('  const buildSettingsForm =', nativeStart));
  for (const remaining of [1023, 1024, 1535, 1536, 2048]) {
    for (const kind of ['settings', 'native']) {
      const classes = new Set();
      const status = { classList: { remove: (...names) => names.forEach(name => classes.delete(name)), add: name => classes.add(name) } };
      const input = value => ({ value, addEventListener() {} });
      const game = input('1G'), maximum = input(`${4096 - remaining}M`);
      const context = vm.createContext({ Number, String, Math, Array,
        formatRemainingMemory: value => `${value} MiB`,
        document: {
          querySelector: selector => selector === '[data-memory-settings]' ? { dataset: { systemMemoryMib: '4096' }, querySelectorAll: () => [] } : null,
          getElementById: id => ({ 'java-memory': game, 'container-memory': maximum, 'memory-status': status })[id] || null,
        }, window: { location: { hash: '' } },
      });
      if (kind === 'settings') vm.runInContext(settings, context);
      else {
        const root = { dataset: {}, querySelectorAll: () => [], querySelector: selector => ({ '[name="java_memory"]': game, '[name="container_memory"]': maximum, '[data-minecraft-memory-status]': status })[selector] };
        context.root = root;
        vm.runInContext(native + '\ninitNativeMemoryControls(root, { system_memory_mib: 4096 });', context);
      }
      assert.equal(classes.has('danger'), remaining < 1024, `${kind} hard boundary ${remaining}`);
      assert.equal(classes.has('warning'), remaining >= 1024 && remaining < 1536, `${kind} recommended boundary ${remaining}`);
    }
  }
  const discovery = read('mjust/libexec/admin-discovery-json');
  const setup = read('mjust/libexec/admin-setup-plan-json');
  assert.ok(discovery.includes('system_reserve_minimum_mib:1024,system_reserve_recommended_mib:1536'));
  assert.ok(discovery.includes('remaining_mib < 1536'));
  assert.ok(setup.includes('SETUP_RECOMMENDED_SYSTEM_RESERVE_MIB=1536'));
  assert.ok(setup.includes('SETUP_MIN_SYSTEM_RESERVE_MIB=1024'));
  assert.ok(discovery.includes('recommended 1.5 GiB headroom'));
  assert.ok(setup.includes('recommended 1.5 GiB'));
  assert.ok(read('webui/internal/server/setup_wizard.go').includes('defaults.SystemReserveRecommendedMiB = 1536'));
});

const failedUnits = [
  { unit: 'minecraft.service', description: 'Minecraft Server', active_state: 'failed', sub_state: 'failed' },
  { unit: 'backup.timer', description: 'Backup timer', active_state: 'failed', sub_state: 'failed' },
  { unit: 'srv-my\\x20disk.mount', description: 'Disk', active_state: 'failed', sub_state: 'failed' },
];
const logGroup = (page, name) => page.content.all().find(node => node.className === 'system-log-group secondary' && node.textContent === name);
const logEntries = page => page.content.all().filter(node => node.className === 'system-log-entry secondary');
const logViewer = page => page.content.all().find(node => node.tag === 'pre');

test('Failed units appends the exact category and lists each live unit separately', async () => {
  const live = { failed_units: failedUnits, journalText: 'Current boot last 60 lines' };
  const page = await openLogs('', [migrationLog], live);
  assert.deepEqual(page.content.all().filter(node => node.className === 'system-log-group secondary').map(node => node.textContent),
    ['Setup', 'Migration', 'Restore', 'Reset', 'Support', 'Failed units']);
  assert.equal(logGroup(page, 'Setup').attrs['aria-pressed'], 'true');
  await logGroup(page, 'Failed units').click();
  assert.equal(logEntries(page).length, 3);
  assert.deepEqual(logEntries(page).map(node => node.children[0].textContent), failedUnits.map(unit => unit.unit));
  assert.ok(page.content.all().some(node => node.tag === 'h3' && node.textContent === 'Units'));
  assert.match(logViewer(page).textContent, /last 60 current-boot journal lines/);
  await logEntries(page)[2].click();
  const url = '/api/system/workspace/logs/failed/' + encodeURIComponent(failedUnits[2].unit);
  assert.deepEqual(page.calls, [url]);
  assert.equal(logViewer(page).textContent, live.journalText);
  const download = page.content.all().find(node => node.textContent === 'Download log');
  assert.equal(download.href, url + '?download=1');
  assert.equal(download.hidden, false);
  await logGroup(page, 'Migration').click();
  assert.equal(logEntries(page).length, 1);
  assert.ok(page.content.all().some(node => node.tag === 'h3' && node.textContent === 'Files'));
  assert.equal(download.hidden, true);
  await logEntries(page)[0].click();
  assert.equal(page.calls.at(-1), '/api/system/workspace/logs/migration/operation-1');
});

test('Failed units refresh enumerates again and removes fixed units', async () => {
  const live = { failed_units: [...failedUnits] };
  const page = await openLogs('', [], live);
  await logGroup(page, 'Failed units').click();
  await logEntries(page)[0].click();
  live.failed_units = [failedUnits[1]];
  await page.render();
  assert.equal(logGroup(page, 'Failed units').attrs['aria-pressed'], 'true');
  assert.deepEqual(logEntries(page).map(node => node.children[0].textContent), ['backup.timer']);
  assert.equal(page.listCalls.length, 2);
  assert.ok(page.listCalls.every(url => url === '/api/system/workspace/logs'));
  live.failed_units = [];
  await page.render();
  assert.equal(logEntries(page).length, 0);
  assert.ok(page.content.all().some(node => node.textContent === 'No failed systemd units.'));
  assert.deepEqual(page.calls, ['/api/system/workspace/logs/failed/minecraft.service']);
});

test('Failed unit enumeration and journal errors show unavailable without hiding other logs or units', async () => {
  const page = await openLogs('', [migrationLog], { failed_units: null, failed_units_error: 'Failed systemd units are unavailable.' });
  await logGroup(page, 'Failed units').click();
  assert.ok(page.content.all().some(node => node.textContent === 'Failed systemd units are unavailable.'));
  assert.ok(!page.content.all().some(node => node.textContent === 'No failed systemd units.'));
  await logGroup(page, 'Migration').click();
  assert.equal(logEntries(page).length, 1);
  const journalPage = await openLogs('', [], { failed_units: failedUnits, journalError: true });
  await logGroup(journalPage, 'Failed units').click();
  await logEntries(journalPage)[0].click();
  assert.equal(logViewer(journalPage).textContent, 'Failed unit journal is unavailable.');
  assert.equal(logEntries(journalPage).length, 3);
  assert.equal(journalPage.content.all().find(node => node.textContent === 'Download log').hidden, true);
});

test('Failed-unit browser view has no privileged command execution and Agent client requests 60 lines', () => {
  assert.doesNotMatch(app, /\b(?:systemctl|journalctl|child_process)\b|exec\.Command|\b(?:execSync|spawnSync)\s*\(/);
  assert.ok(read('webui/internal/api/failed_units.go').includes('/journal?limit=60'));
  assert.ok(app.includes('refreshButton?.addEventListener("click", loadCurrentSystemTab)'));
  assert.ok(app.includes('failedUnitsSelected = false;'));
});
