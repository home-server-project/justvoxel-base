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

test('Control Center matches the 32px topbar and preserves panel anchoring and green frame', () => {
  assert.ok(css.includes('@media(min-width:601px){.app-header .control-center{position:absolute;top:-1px;right:-1px;height:32px;margin:0}}'));
  assert.match(css, /\.app-header\.topbar\{[^}]*padding:0 \.5rem;[^}]*border:1px solid/);
  const declarations = block => Object.fromEntries(block.split(';').filter(Boolean).map(value => value.trim().split(':')));
  const topbar = declarations([...css.matchAll(/\.app-header\.topbar\{([^}]*)\}/g)].findLast(match => match[1].startsWith('height:'))[1]);
  const desktop = [...css.matchAll(/@media\(min-width:601px\)\{\s*\.app-header \.control-center\{([^}]*)\}\s*\}/g)]
    .find(match => declarations(match[1]).height === '32px');
  assert.ok(desktop, 'panel anchor height must be preserved at desktop widths above 600px');
  const wrapper = declarations(desktop[1]);
  const trigger = declarations(css.match(/@media\(min-width:601px\)\{\s*\.app-header \.control-center-trigger\{([^}]*)\}/)[1]);
  assert.equal(topbar.border, '1px solid #303942', 'preserve the topbar border color');
  assert.equal(trigger['border-color'], '#303942', 'normal desktop trigger border matches the topbar');
  assert.ok(css.includes('*{box-sizing:border-box}'));
  assert.match(css, /\.topbar\{display:grid;[^}]*align-items:center;/);
  assert.equal(wrapper.height, '32px', 'preserve the panel containing block height');
  // Verify source declarations; VM visual review establishes rendered alignment.
  assert.equal(wrapper.position, 'absolute');
  assert.equal(wrapper.top, '-1px');
  assert.equal(wrapper.right, '-1px');
  assert.equal(wrapper.margin, '0');
  for (const margin of ['margin-top', 'margin-bottom', 'margin-right']) assert.equal(wrapper[margin], undefined);
  assert.ok(!css.includes('margin-right:calc(-.5rem - 1px)'));
  assert.ok(!css.includes('border-radius:0 12px 12px 0'));
  assert.equal(topbar.height, '32px', 'preserve the visible topbar height');
  assert.equal(topbar['min-height'], '32px');
  assert.equal(topbar['border-radius'], '12px');
  assert.equal(trigger['border-radius'], '10px 12px 12px 10px', 'desktop trigger retains rounded left corners and matches the topbar right radius');
  assert.equal(trigger.background, '#151a1f', 'preserve the normal desktop trigger background');
  assert.equal(topbar.background, '#151a1f');
  assert.match(css, /\.control-center-trigger\{[^}]*border-radius:10px;/, 'compact trigger retains its rounded treatment');
  assert.equal(topbar.padding, '0 .5rem');
  assert.equal(topbar.margin, '4px 8px');
  assert.ok(css.includes('.quick-look{position:fixed;top:40px;'));
  assert.equal(trigger.height, '32px');
  assert.equal(trigger['min-height'], '32px');
  for (const rule of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    if (!rule[1].includes('.app-header .control-center-trigger')) continue;
    assert.doesNotMatch(rule[2], /(?:^|;)\s*top\s*:\s*-4px\b/, 'trigger must stay inside its anchor');
    assert.doesNotMatch(rule[2], /(?:^|;)\s*(?:min-|max-)?height\s*:\s*40px\b/, 'no desktop 40px trigger override');
  }
  assert.match(css, /\.control-center\{position:relative;justify-self:end\}/);
  assert.match(css, /\.control-center-panel\{position:absolute;[^}]*right:0;/);
  assert.ok(!css.includes('.control-center-panel{right:calc(-.5rem - 1px)}'));
  for (const panel of css.matchAll(/\.control-center-panel\{([^}]*)\}/g)) {
    assert.doesNotMatch(panel[1], /right:\s*(?:-|calc\()/, 'panel must not compensate the right edge again');
  }
  const panel = declarations(css.match(/\.control-center-panel\{(position:absolute;[^}]*)\}/)[1]);
  assert.equal(panel.position, 'absolute');
  assert.equal(panel.right, '0');
  assert.equal(panel.top, 'calc(100% + var(--jv-panel-gap))');
  assert.ok(css.includes('.app-header .control-center-trigger{min-height:32px;height:32px;padding:.3rem .6rem;border-color:#303942}'));
  assert.match(css, /\.control-center-trigger\{[^}]*border:1px solid #35414b;/);
  for (const rule of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    if (!/\.(?:control-center(?:-trigger|-panel)?|topbar)(?:[^a-zA-Z0-9_-]|$)/.test(rule[1])) continue;
    assert.doesNotMatch(rule[2], /(?:^|;)\s*(?:overflow(?:-x|-y)?\s*:\s*(?:hidden|clip)|clip(?:-path)?\s*:|(?:transform|translate)\s*:)|translateX\(/, 'no clipping or transforms for alignment');
  }
  for (const mobile of css.matchAll(/@media\(max-width:600px\)\{([^{}]*(?:\{[^{}]*\}[^{}]*)*)\}/g)) {
    for (const rule of mobile[1].matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
      if (rule[1].trim() !== '.app-header .control-center' && rule[1].trim() !== '.control-center') continue;
      assert.doesNotMatch(rule[2], /position:absolute|top:-1px|right:-1px/, 'mobile must not use desktop attachment');
    }
  }
  assert.ok(css.includes('.control-center-panel{position:fixed;top:50px;left:.45rem;right:.45rem;bottom:.45rem;width:auto;'));
  assert.ok(css.includes('@media(max-width:600px){\n  .app-header.topbar{margin:4px;gap:.2rem;padding:0 .3rem}'));
  assert.ok(css.includes('.app-header .control-center-trigger{width:32px;padding:0}'));
  assert.ok(css.includes('.control-center-panel{top:44px}'));
  assert.ok(css.includes(':root{--jv-panel-gap:4px}'));
  assert.ok(css.includes('top:calc(100% + var(--jv-panel-gap))'));
  assert.ok(!css.includes('--jv-panel-gap:.45rem'));
  assert.ok(!css.includes('--jv-panel-gap:6px'));
  const generic = declarations(css.match(/^\.topbar\{([^}]*)\}/m)[1]);
  for (const obsolete of ['height', 'min-height', 'padding', 'top']) assert.equal(generic[obsolete], undefined);
  assert.ok(!css.includes('.app-header.topbar{height:48px;'));
  assert.equal(topbar.position, 'sticky');
  assert.equal(topbar['z-index'], '1000');
  assert.equal(topbar.top, '4px');
  assert.ok(css.includes('.app-header .control-center-trigger:hover,.app-header .control-center-trigger:focus-visible,.app-header .control-center[open]>.control-center-trigger{border-color:#4f8b69}'));
  assert.ok(css.includes('.app-header .control-center-trigger:hover,.app-header .control-center-trigger:focus-visible,.app-header .control-center[open]>.control-center-trigger{background:#1d2a23;color:#c8f2d7}'));
  assert.ok(css.includes('.server-software-card.is-selected,'));
  assert.ok(!css.includes('.version-software-choice.is-current'));
  assert.ok(!css.includes('.server-software-card.is-current'));
});

test('Control Center heading escapes panel padding and shares the 36px workspace lane', () => {
  assert.ok(css.includes('*{box-sizing:border-box}'));
  assert.ok(css.includes('.control-center-heading{display:flex;align-items:center;justify-content:space-between;height:36px;min-height:36px;margin:-.8rem -.8rem .8rem;padding:0 var(--jv-workspace-edge);border-bottom:1px solid var(--jv-border);border-radius:13px 13px 0 0}'));
  const panelPadding = [...css.matchAll(/\.control-center-panel\{([^}]*)\}/g)]
    .flatMap(match => match[1].split(';').filter(value => value.startsWith('padding:')));
  assert.deepEqual(panelPadding, ['padding:.8rem'], 'all panel sizes retain the padding matched by the heading escape');
  assert.match(css, /\.workspace-window :is\(\.system-update-dialog-header,[^}]+\)\{[^}]*height:36px;min-height:36px;[^}]*padding:0 var\(--jv-workspace-edge\);/);
});

test('desktop workspace clearance uses the pixel gap and measured anchor bottom', () => {
  const start = app.indexOf('const workspaceTopInset =');
  const end = app.indexOf('const clampWorkspaceWindow =', start);
  const gap = css.match(/:root\{--jv-panel-gap:([^}]+)\}/)[1];
  let bottom = 36;
  let anchorAvailable = true;
  let configuredGap = gap;
  let fontSize = '16px';
  const context = vm.createContext({
    document: { documentElement: {}, querySelector: selector => {
      assert.equal(selector, ".app-header.topbar");
      return anchorAvailable ? { getBoundingClientRect: () => ({ bottom }) } : null;
    } },
    getComputedStyle: () => ({ fontSize, getPropertyValue: name => { assert.equal(name, '--jv-panel-gap'); return configuredGap; } }),
  });
  vm.runInContext(app.slice(start, end) + '\nthis.topInset = workspaceTopInset;', context);
  assert.equal(context.topInset(), 40);
  fontSize = '20px';
  assert.equal(context.topInset(), 40, 'pixel clearance is independent of root font size');
  bottom = 58;
  assert.equal(context.topInset(), 62, 'desktop positioning continues to follow its anchor');
  bottom = 36;
  configuredGap = '';
  assert.equal(context.topInset(), 40, 'missing CSS gap falls back to 4px');
  configuredGap = 'invalid';
  assert.equal(context.topInset(), 40, 'invalid CSS gap keeps a finite inset');
  anchorAvailable = false;
  assert.equal(context.topInset(), 8, 'missing anchor retains the minimum safe inset');
});

test('compact workspace families share the 44px top and retain bottom clearance', () => {
  assert.match(css, /@media\(max-width:700px\)\{\s*:root\{--jv-workspace-top-inset:44px;/);
  assert.ok(css.includes('--jv-workspace-top-inset:76px;'), 'preserve the separate desktop fallback');
  const networkCSS = read('webui/internal/server/static/network-workspace.css');
  for (const source of [css, networkCSS]) {
    assert.doesNotMatch(source, /inset:(?:52|60)px|calc\(52px \+ \.25rem\)|calc\(100dvh - 52px/);
  }
  for (const family of ['system-update', 'system-monitor', 'storage-workspace', 'backups-workspace', 'minecraft-workspace', 'system-workspace', 'migration-workspace', 'version-workspace', 'plugins-workspace', 'network-workspace']) {
    const source = family === 'network-workspace' ? networkCSS : css;
    const rule = [...source.matchAll(new RegExp('\\.' + family + '-dialog\\{([^}]+)\\}', 'g'))].find(match => match[1].includes('inset:var(--jv-workspace-top-inset)'));
    assert.ok(rule, `${family} must use the shared compact inset`);
    assert.ok(rule[1].includes('inset:var(--jv-workspace-top-inset) .25rem .25rem .25rem!important'));
    if (['migration-workspace', 'version-workspace', 'plugins-workspace'].includes(family)) {
      assert.ok(rule[1].includes('height:calc(100dvh - var(--jv-workspace-top-inset) - .25rem)!important'));
      assert.ok(rule[1].includes('max-height:calc(100dvh - var(--jv-workspace-top-inset) - .25rem)'));
    }
  }
  const live = css.match(/\.live-console-dialog\{(inset:[^}]+)\}/)[1];
  for (const declaration of [
    'inset:var(--jv-workspace-top-inset) 8px auto!important',
    'width:calc(100vw - 16px)!important',
    'height:min(460px,calc(100dvh - var(--jv-workspace-top-inset) - 16px))!important',
    'max-height:calc(100dvh - var(--jv-workspace-top-inset) - 16px)',
  ]) assert.ok(live.includes(declaration), declaration);
});

const selectedMigrationURL = value => {
  const destination = new URL(value, 'http://example');
  if (!destination.searchParams.has('server')) destination.searchParams.set('server', 'jv-a1b2c3');
  return destination.pathname + destination.search;
};

const assertFreshExportURL = value => {
  const destination = new URL(value, 'http://example');
  assert.equal(destination.pathname, '/');
  assert.equal(destination.searchParams.get('workspace'), 'migration');
  assert.equal(destination.searchParams.get('tab'), 'export');
  assert.equal(destination.searchParams.get('server'), 'jv-a1b2c3');
  assert.equal(destination.searchParams.has('operation'), false);
};

test('Export Done clears operation navigation, selects Export, and reopening loads fresh Export', async () => {
  const loads = [];
  const root = { addEventListener(name, handler) { this[name] = handler; }, contains: () => true };
  const location = { href: 'http://example/?workspace=migration&operation=old-operation&server=jv-a1b2c3', origin: 'http://example', pathname: '/', search: '?workspace=migration&operation=old-operation&server=jv-a1b2c3' };
  let windowOptions;
  const context = vm.createContext({
    URL, URLSearchParams, Object, justVoxelSelectedURL: selectedMigrationURL, root, currentTab: 'export', currentURL: '/workspace/migration/progress/old-operation',
    justVoxelServerSelection: async (_, refresh) => { assert.equal(refresh, true); },
    migrationDialog: {querySelector: () => ({})}, migrationSourceSMBPassword: '', migrationExportSMBPassword: '', migrationSubmitController: null,
    tabURLs: { export: '/workspace/migration/export', import: '/workspace/migration/import', recovery: '/workspace/migration/recovery' },
    window: { location, history: { state: {}, replaceState(_, __, next) { const url = new URL(next, location.href); location.href = url.href; location.search = url.search; } } },
    setupWorkspaceWindow: (_, options) => { windowOptions = options; return { close: options.onClose }; },
    syncMigrationTabs: () => { loads.push('select-export'); },
    loadMigration: async url => { loads.push(url); }, loadMigrationEntry: async () => { loads.push('normal-entry'); }, abortMigrationRequests() {},
  });
  const update = app.slice(app.indexOf('  const updateMigrationLocation ='), app.indexOf('  const setMigrationBusy ='));
  const start = app.indexOf('    root.addEventListener("click", async (event) => {', app.indexOf('const renderMigrationMarkup ='));
  const click = app.slice(start, app.indexOf('    initializeMigrationRoot(root);', start));
  const windowStart = app.indexOf('  const refreshSelection = () => globalThis.justVoxelServerSelection(migrationDialog');
  const windowSource = app.slice(windowStart, app.indexOf('  migrationOpen.addEventListener', windowStart));
  vm.runInContext(update + windowSource + click, context);
  const link = { getAttribute: () => selectedMigrationURL('/?workspace=migration&tab=export') };
  let prevented = false;
  await root.click({ target: { closest: () => link }, preventDefault() { prevented = true; } });
  assert.ok(prevented);
  assertFreshExportURL(location.href);
  assert.equal(context.currentTab, 'export');
  assert.equal(context.currentURL, '/workspace/migration/export');
  assert.deepEqual(loads, ['select-export', '/workspace/migration/export']);
  windowOptions.onClose();
  await windowOptions.onOpen();
  assert.equal(loads.at(-1), 'normal-entry');
  assert.ok(!loads.some(value => value.includes('/progress/')));
});

const diagnosticTimestampSource = app.slice(app.indexOf('function formatDiagnosticLogTimestamp('), app.indexOf('if (topbarClock) {'));
const logSource = app.slice(app.indexOf('  const systemLogDestination ='), app.indexOf('  const renderUsers =', app.indexOf('  const systemLogDestination =')));
async function openLogs(search, logs, live = {}) {
  const listCalls = [];
  const calls = [];
  const content = new Element('div');
  const context = vm.createContext({
    URLSearchParams, Number, JSON, Array, Math, String, encodeURIComponent, loadSequence: 1, content,
    topbarClock: { dataset: { systemTimezone: 'UTC' } }, clockPreferences: { time: '24', date: 'mdy' },
    window: { location: { search } }, document: { createElement: tag => new Element(tag) },
    localStorage: { getItem: () => null }, ResizeObserver: class { observe() {} disconnect() {} },
    systemSplitter: () => new Element('div'), systemFetchJSON: async url => { listCalls.push(url); return { logs, ...live }; }, systemHandleAuth: () => false,
    fetch: async url => { calls.push(url); return { ok: !live.journalError, text: async () => live.journalText || 'Export evidence' }; },
  });
  vm.runInContext(diagnosticTimestampSource + '\n' + logSource + '\nglobalThis.renderLogs = renderLogs;', context);
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
    const panel = {
      closest(selector) {
        assert.equal(selector, '[data-server-id]');
        return { dataset: { serverId: 'jv-a1b2c3' } };
      },
    };
    const context = vm.createContext({ panel, supportedType: () => true, terminalState: () => false, operationName: () => 'Server Export', stateLabel: () => state, stageLabel: () => '', reconnectNote: null, nameText: null, statusText: null, stateBadge: null, stageText: null, successNote: null, rollbackNote: null, attentionNote: null, recoveryLink: null, dashboardLink, exportLogDetails, backLink });
    vm.runInContext(render + '\nrender({ operation_type: "migration_export", state: ' + JSON.stringify(state) + ' });', context);
    assert.equal(dashboardLink.hidden, state !== 'succeeded');
    assert.equal(dashboardLink.textContent, 'Done');
    assertFreshExportURL(dashboardLink.href);
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
          querySelectorAll: () => [],
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
  const minimum = Number(setup.match(/readonly SETUP_MIN_SYSTEM_RESERVE_MIB=(\d+)/)[1]);
  const recommended = Number(setup.match(/readonly SETUP_RECOMMENDED_SYSTEM_RESERVE_MIB=(\d+)/)[1]);
  assert.equal(minimum, 1024);
  assert.equal(recommended, 1536);
  assert.match(setup, /remaining_mib < SETUP_MIN_SYSTEM_RESERVE_MIB/);
  assert.match(setup, /remaining_mib < SETUP_RECOMMENDED_SYSTEM_RESERVE_MIB/);
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
    ['Setup', 'Migration', 'Restore', 'Plugins', 'Reset', 'Network', 'Update', 'Support', 'Failed units']);
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
  assert.ok(app.includes('refreshButton?.addEventListener("click", () => loadCurrentSystemTab())'));
  assert.ok(!app.includes('loadCurrentSystemTab(currentTab === "terminal")'));
  assert.ok(app.includes('failedUnitsSelected = false;'));
});

test('Plugins Logs selects and downloads the exact Agent operation log without reloading', async () => {
  const id = '12345678-1234-4123-8123-123456789abc';
  const entry = { category: 'plugins', log_id: id, operation_id: id, operation_type: 'plugin_disable', timestamp: 'now', state: 'needs_attention' };
  const page = await openLogs('?workspace=system&tab=logs&log_group=plugins&log_id=' + id, [entry]);
  assert.equal(logGroup(page, 'Plugins').attrs['aria-pressed'], 'true');
  assert.deepEqual(page.calls, ['/api/system/workspace/logs/plugins/' + id]);
  const download = page.content.all().find(node => node.textContent === 'Download log');
  assert.equal(download.href, '/api/system/workspace/logs/plugins/' + id + '?download=1');
  assert.equal(download.hidden, false);
  const unsafe = await openLogs('?workspace=system&tab=logs&log_group=plugins&log_id=../etc/passwd', [entry]);
  assert.deepEqual(unsafe.calls, []);
});

test('appliance Reset Minecraft review preserves external data and removes internal data and all runtimes', () => {
  const start = app.indexOf('  const resetImpactFromPlan =');
  const end = app.indexOf('  const renderResetPlan =', start);
  assert.ok(start >= 0 && end > start);
  const context = vm.createContext({});
  vm.runInContext(app.slice(start, end) + '\nthis.impact = resetImpactFromPlan;', context);
  const result = context.impact('minecraft', { data_action: 'preserve', plugins_action: 'remove_managed_jars' });
  assert.ok(result.remove.includes('ALL configured Minecraft server definitions, running or stopped.'));
  assert.ok(result.remove.includes('Minecraft containers/runtime units and exact owned firewall/runtime state.'));
  assert.ok(result.remove.includes('JustVoxel-owned INTERNAL Minecraft data, including managed plugins/runtime content.'));
  assert.ok(result.keep.includes('External, USB, NFS, and SMB data are never erased.'));
  assert.ok(result.keep.includes('All backup archives and archive metadata.'));
  assert.ok(!result.remove.includes('Plugins-managed JARs installed through JustVoxel.'));
});

test('selected Reset Minecraft review retains managed JAR cleanup with preserved external data', () => {
  const start = app.indexOf('  const resetImpactFromPlan =');
  const end = app.indexOf('  const renderResetPlan =', start);
  assert.ok(start >= 0 && end > start);
  const context = vm.createContext({});
  vm.runInContext(app.slice(start, end) + '\nthis.impact = resetImpactFromPlan;', context);
  const result = context.impact('minecraft', { instance_id: 'jv-a1b2c3', data_action: 'preserve', plugins_action: 'remove_managed_jars' });
  assert.ok(result.remove.includes('Plugins-managed JARs installed through JustVoxel.'));
  assert.ok(result.keep.includes('Plugin configuration/data folders and manual plugins.'));
  assert.ok(!result.remove.some(item => item.includes('world/data')));
});

const lifecycleElement = (tag, value = '', className = '') => {
  const node = new Element(tag);
  node.textContent = value;
  node.className = className;
  node.classList = {
    add(name) { node.className += ' ' + name; },
    toggle(name, enabled) { const classes = new Set(node.className.split(' ').filter(Boolean)); if (enabled) classes.add(name); else classes.delete(name); node.className = [...classes].join(' '); },
  };
  return node;
};

test('Installed lifecycle actions derive one toggle and preserve read-only ownership and icons', () => {
  const start = app.indexOf('  const renderInstalled =');
  const end = app.indexOf('  // Review, progress', start);
  let headerBadges = [];
  let headerStatus;
  const pluginBadge = (value, semanticClass) => lifecycleElement('span', value, 'plugins-badge ' + semanticClass);
  const context = vm.createContext({
    document: { createElement: tag => lifecycleElement(tag) }, text: lifecycleElement,
    pluginBadge, acceptedServer: {content_kind: "plugins"},
    installedStatus: () => pluginBadge('Installed', 'plugins-badge-installed plugins-overlay-installed'),
    overlayHeader: (_, __, badges, ___, status) => { headerBadges = badges; headerStatus = status; },
    overlaySection: () => lifecycleElement('section'), infoRows: () => () => {},
    addInfo: (card, _, render, projectID) => { card.info = render; card.iconProjectID = projectID; },
    pluginAction: (label, handler, style) => { const button = lifecycleElement('button', label, style); button.addEventListener('click', handler); return button; },
    reviewManagedPlugin() {},
  });
  vm.runInContext(app.slice(start, end) + '\nthis.renderInstalled = renderInstalled;', context);
  for (const [management, state, labels, badge, kind = "plugins"] of [
    ['plugins', 'enabled', ['Disable', 'Remove'], 'Enabled'],
    ['plugins', 'disabled', ['Enable', 'Remove'], 'Disabled'],
    ['plugins', 'installed', ['Remove'], '', 'mods'],
    ['plugins', 'attention', [], '', 'mods'],
    ['manual', 'installed', [], '', 'mods'],
    ['justvoxel', 'installed', [], '', 'mods'],
    ['manual', 'enabled', [], ''],
    ['justvoxel', 'enabled', [], ''],
  ]) {
    context.acceptedServer.content_kind = kind;
    const card = context.renderInstalled({ management, state, name: 'Root', filename: 'root.jar', version: '1.0', project_id: management === 'plugins' ? 'root' : undefined, icon_project_id: management === 'justvoxel' ? 'wKkoqHrH' : undefined, metadata_status: 'Metadata read' });
    const buttons = card.info() || [];
    assert.deepEqual(Array.from(buttons, node => node.textContent), labels);
    assert.ok(!(buttons.some(node => node.textContent === 'Enable') && buttons.some(node => node.textContent === 'Disable')));
    if (kind === 'mods') {
      assert.ok(!buttons.some(node => ['Enable', 'Disable'].includes(node.textContent)));
      if (management === 'plugins') assert.ok(card.all().some(node => node.textContent === 'Managed by Plugins'));
    }
    if (badge) {
      const stateClass = state === 'disabled' ? 'plugins-badge-disabled' : 'plugins-badge-enabled';
      for (const [label, semanticClass] of [['Installed', 'plugins-badge-installed'], [badge, stateClass]]) {
        assert.ok(card.all().some(node => node.textContent === label && node.className.split(' ').includes(semanticClass)));
        assert.ok(headerBadges.some(descriptor => Array.isArray(descriptor) && descriptor[0] === label && descriptor[1] === semanticClass));
      }
      assert.equal(headerStatus.tag, 'span');
      assert.equal(headerStatus.textContent, 'Installed');
      assert.ok(headerStatus.className.split(' ').includes('plugins-badge-installed'));
      assert.ok(headerStatus.className.split(' ').includes('plugins-overlay-installed'));
      assert.equal(buttons[0].className, 'plugins-state-action');
      assert.equal(buttons[1].className, 'plugins-remove-action');
      assert.equal(card.iconProjectID, 'root');
    } else if (management !== 'plugins') {
      assert.equal(headerStatus, null);
      if (management === 'justvoxel') assert.equal(card.iconProjectID, 'wKkoqHrH');
      else assert.equal(card.iconProjectID, undefined);
    }
  }
});

async function pluginLifecycleReview(action, kind = "plugins") {
  const start = app.indexOf('  const reviewManagedPlugin =');
  const end = app.indexOf('  const pluginRequest =', start);
  const body = lifecycleElement('div');
  const submissions = [];
  let installed = 0;
  const plan = { action, project_id: 'root', project_name: 'Root', state: action === 'enable' ? 'disabled' : 'enabled', plan_fingerprint: 'sha256:' + 'a'.repeat(64), confirmation_required: false };
  const context = vm.createContext({
    AbortController, Array, Number, Math, String, installGeneration: 0, installController: null, operationIdentity: null, pollTimer: null,
    document: { createElement: tag => lifecycleElement(tag) }, text: lifecycleElement, panelBody: body,
    stopPolling: () => { context.installGeneration++; }, showPanel: () => body.replaceChildren(),
    acceptedServer: {content_kind: kind},
    overlayHeader() {}, blockPluginInstall: async () => false,
    returnToInstalled: () => { installed++; },
    panelAction: (label, handler) => { const button = lifecycleElement('button', label); button.addEventListener('click', handler); return button; },
    pluginAction: (label, handler, style) => { const button = lifecycleElement('button', label, style); button.addEventListener('click', handler); return button; },
    overlayActions: (...buttons) => body.append(...buttons),
    pluginRequest: async (route, request) => { if (route === 'manage/plan') return plan; submissions.push({ route, request }); return { operation: { operation_id: '12345678-1234-4123-8123-123456789abc', state: 'succeeded' } }; },
    showOperation: () => true, panelError: error => { throw error; },
  });
  vm.runInContext(app.slice(start, end) + '\nthis.review = reviewManagedPlugin;', context);
  await context.review({ project_id: 'root', name: 'Root', version: '1.0' }, action);
  return { body, submissions, installed: () => installed };
}

test('Enable and Disable reviews have Cancel and one yellow final action with no destructive slider', async () => {
  for (const action of ['enable', 'disable']) {
    const page = await pluginLifecycleReview(action);
    const buttons = page.body.all().filter(node => node.tag === 'button');
    assert.deepEqual(buttons.map(node => node.textContent), ['Cancel', action === 'enable' ? 'Enable' : 'Disable']);
    assert.equal(buttons[1].className, 'plugins-state-action');
    assert.ok(!page.body.all().some(node => node.type === 'range'));
    assert.equal(buttons[1].disabled, false);
    await buttons[0].click();
    assert.equal(page.installed(), 1);
    assert.equal(page.submissions.length, 0);
  }
});

test('Remove review requires both a fully armed slider and the final confirmation switch', async () => {
  const page = await pluginLifecycleReview('remove', 'mods');
  const buttons = page.body.all().filter(node => node.tag === 'button');
  assert.deepEqual(buttons.map(node => node.textContent), ['Cancel', 'Remove']);
  const remove = buttons[1];
  assert.equal(remove.className, 'plugins-remove-action');
  assert.equal(remove.disabled, true);
  const slider = page.body.all().find(node => node.type === 'range');
  const confirm = page.body.all().find(node => node.className === 'destructive-confirm-toggle');
  slider.value = '100'; slider.events.input();
  assert.equal(remove.disabled, true);
  confirm.checked = true; confirm.events.change();
  assert.equal(remove.disabled, false);
  slider.value = '99'; slider.events.input();
  assert.equal(confirm.checked, false);
  assert.equal(remove.disabled, true);
  slider.value = '100'; slider.events.input(); confirm.checked = true; confirm.events.change();
  await remove.click();
  assert.equal(page.submissions.length, 1);
  assert.equal(page.submissions[0].route, 'manage');
  assert.equal(page.submissions[0].request.action, 'remove');
});

test('Add-ons sections render host-aware local file controls and exact managed content', () => {
  const start = app.indexOf('  const renderAddonSection =');
  const end = app.indexOf('  const load =', start);
  let reviewed;
  const context = vm.createContext({
    document: { createElement: tag => lifecycleElement(tag), createTextNode: value => lifecycleElement('text', value) },
    text: lifecycleElement, pluginBadge: (value, style) => lifecycleElement('span', value, style),
    panelAction: (label, action) => { const button = lifecycleElement('button', label); button.addEventListener('click', action); return button; },
    reviewAddon: (section, file) => { reviewed = { section, file }; },
  });
  vm.runInContext(app.slice(start, end) + '\nthis.render = renderAddonSection;', context);
  const section = { kind: 'skript_script', name: 'Skript Scripts', accept: '.sk', destination: 'plugins/Skript/scripts', host: { available: false, status: 'disabled', message: 'Host disabled' }, files: [{ filename: 'test.sk', state: 'installed', target_relative_path: 'plugins/Skript/scripts/test.sk' }] };
  const unavailable = context.render(section);
  const picker = unavailable.all().find(node => node.tag === 'input');
  const button = unavailable.all().find(node => node.tag === 'button');
  assert.equal(picker.type, 'file');
  assert.equal(picker.accept, '.sk');
  assert.equal(picker.disabled, true);
  assert.equal(button.disabled, true);
  const labels = unavailable.all().map(node => node.textContent);
  for (const value of ['Host disabled', 'Installed', 'Managed by Add-ons', 'Updates are not available yet', 'plugins/Skript/scripts/test.sk']) assert.ok(labels.includes(value), value);
  const available = context.render({ ...section, host: { available: true, status: 'installed', message: 'Host installed' } });
  const choose = available.all().find(node => node.tag === 'input');
  const review = available.all().find(node => node.tag === 'button');
  const file = { name: 'selected.sk' };
  choose.files = [file];
  choose.events.change();
  assert.equal(review.disabled, false);
  review.click();
  assert.equal(reviewed.file, file);
  assert.equal(available.all().filter(node => node.tag === 'input').length, 1);
});

test('Add-ons plan and apply send the same selected File with a reviewed fingerprint', async () => {
  const start = app.indexOf('  const addonRequest =');
  const end = app.indexOf('  const reviewAddon =', start);
  const requests = [];
  class FormData { constructor() { this.fields = []; } append(...field) { this.fields.push(field); } }
  const context = vm.createContext({
    FormData, installController: { signal: 'signal' },
    justVoxelSelectionGeneration: { plugins: 0 },
    justVoxelStaleSelection: () => Object.assign(new Error('Server selection changed.'), { staleSelection: true }),
    justVoxelSelectedURL: route => route,
    document: { querySelector: () => ({ value: 'csrf-token' }) },
    fetch: async (route, options) => { requests.push({ route, options }); return { status: 200, ok: true, json: async () => ({}) }; },
  });
  vm.runInContext(app.slice(start, end) + '\nthis.request = addonRequest;', context);
  const file = { name: 'castle.schem' };
  await context.request('plan', 'worldedit_schematic', file);
  await context.request('install', 'worldedit_schematic', file, { plan_fingerprint: 'reviewed-fingerprint' }, false);
  for (const request of requests) {
    assert.equal(request.options.headers['X-CSRF-Token'], 'csrf-token');
    assert.equal(request.options.body.fields[0][0], 'type');
    assert.equal(request.options.body.fields[1][1], file);
    assert.equal(request.options.body.fields[1][2], file.name);
    assert.ok(!request.options.body.fields.some(([key]) => ['path', 'destination', 'url'].includes(key)));
  }
  assert.equal(requests[0].options.body.fields.length, 2);
  assert.equal(requests[1].options.body.fields[2][1], 'reviewed-fingerprint');
  assert.equal(requests[1].options.body.fields[3][1], 'false');
});

test('Add-ons remain exactly five fixed kinds within the third Plugins tab', () => {
  const header = read('webui/internal/server/templates/header.html');
  const tabs = [...header.matchAll(/data-plugins-tab="([^"]+)"/g)].map(match => match[1]);
  assert.equal(tabs.length, 4);
  assert.deepEqual(tabs, ['discover', 'installed', 'addons', 'curseforge-api']);
  const agent = read('management/cmd/justvoxel-management-agent/admin_plugin_addons.go');
  const definitions = agent.slice(agent.indexOf('var addonDefinitions ='), agent.indexOf('func addonDefinitionFor'));
  const kinds = [...definitions.matchAll(/\{"([a-z_]+)"/g)].map(match => match[1]);
  assert.deepEqual(kinds, ['placeholderapi_expansion', 'geyser_extension', 'bluemap_addon', 'skript_script', 'worldedit_schematic']);
  assert.ok(app.includes('BlueMap native add-on belongs in Plugins -> Add-ons -> BlueMap Add-ons. Select its local file there.'));
  assert.ok(!app.includes('BlueMap addon - separate installation path not supported'));
});

test('Recommended memory tiers agree in both forms and do not grow with player count', () => {
  const settings = read('webui/internal/server/static/settings.js');
  const start = app.indexOf('  const initNativeMemoryControls =');
  const native = app.slice(start, app.indexOf('  const buildSettingsForm =', start));
  for (const [mib, heap, maximum] of [
    [3072, '1G', '2G'], [3840, '2G', '2816M'], [6144, '3G', '4G'],
    [7867, '4G', '6G'], [12288, '4G', '6G'], [16384, '6G', '8G'],
    [32768, '6G', '8G'], [65536, '6G', '8G'],
  ]) {
    for (const kind of ['settings', 'native']) {
      const game = { value: '1G', addEventListener() {} };
      const max = { value: '2G', addEventListener() {} };
      let click;
      const button = {
        dataset: { memoryPreset: 'recommended', minecraftMemoryPreset: 'recommended' },
        classList: { toggle() {} }, addEventListener(name, fn) { if (name === 'click') click = fn; },
      };
      const panel = { dataset: { systemMemoryMib: String(mib) }, querySelectorAll: () => [button] };
      const context = vm.createContext({
        document: { querySelectorAll: () => [], querySelector: selector => selector === '[data-memory-settings]' ? panel : null,
          getElementById: id => ({ 'java-memory': game, 'container-memory': max })[id] || null },
        window: { location: { hash: '' } },
        root: { dataset: { maxPlayers: '200' }, querySelectorAll: () => [button],
          querySelector: selector => ({ '[name="java_memory"]': game, '[name="container_memory"]': max })[selector] || null },
      });
      if (kind === 'settings') vm.runInContext(settings, context);
      else vm.runInContext(native + `\ninitNativeMemoryControls(root, {system_memory_mib:${mib}});`, context);
      click();
      assert.deepEqual([game.value, max.value], [heap, maximum], `${kind} ${mib}`);
    }
  }
});

test('installed memory above simultaneous capacity is informational', () => {
  const classes = new Set();
  const status = { classList: { remove: (...names) => names.forEach(name => classes.delete(name)), add: name => classes.add(name) } };
  const game = { value: '4G', addEventListener() {} };
  const max = { value: '6G', addEventListener() {} };
  const panel = { dataset: { systemMemoryMib: '7867', otherMemoryMib: '6144' }, querySelectorAll: () => [] };
  const context = vm.createContext({ document: {
    querySelectorAll: () => [],
    querySelector: selector => selector === '[data-memory-settings]' ? panel : null,
    getElementById: id => ({ 'java-memory': game, 'container-memory': max, 'memory-status': status })[id] || null,
  }, window: { location: { hash: '' } } });
  vm.runInContext(read('webui/internal/server/static/settings.js'), context);
  assert.equal(classes.has('danger'), false);
  assert.equal(classes.has('warning'), true);
  assert.match(status.textContent, /can remain installed/);
  assert.match(status.textContent, /cannot all run at the same time/);
});

test('System accepts bounded dynamic Logs navigation and preserves exact Plugins diagnostic IDs', async () => {
  const allowed = app.match(/  const allowedSystemTabs = new Set\([^\n]+/)[0];
  const start = app.indexOf('  window.JustVoxelSystem = {');
  const end = app.indexOf('\n  systemWorkspaceOpen.addEventListener', start);
  let opened = 0;
  const context = vm.createContext({
    window: {}, settingsPanels: new Map(), administratorTabs: new Set(['logs']), upsAvailable: false,
    loadIdentity: async () => ({role: 'administrator'}), initialized: false, currentTab: 'health',
    requestedMigrationLog: null, syncSystemTabs() {}, controlCenter: {open: true},
    workspaceWindow: {open() { opened++; }},
  });
  vm.runInContext(allowed + '\n' + app.slice(start, end), context);
  const id = '12345678-1234-4234-8234-123456789abc';
  await context.window.JustVoxelSystem.openPluginLogs(id);
  assert.equal(context.currentTab, 'logs');
  assert.equal(context.requestedMigrationLog.group, 'Plugins');
  assert.equal(context.requestedMigrationLog.id, id);
  assert.equal(opened, 1);
  await context.window.JustVoxelSystem.openPluginLogs(id);
  assert.equal(opened, 2, 'already open workspace is brought to front again');
  for (const invalid of ['../bad', 'latest', '', '12345678-1234-1234-8234-123456789abc']) {
    await context.window.JustVoxelSystem.openPluginLogs(invalid);
  }
  await context.window.JustVoxelSystem.openTab('arbitrary');
  await context.window.JustVoxelSystem.openTab('ups');
  assert.equal(opened, 2);
  assert.ok(app.includes('if (allowedSystemTabs.has(requested)'));
  const setup = app.slice(app.indexOf('const setupWorkspaceWindow ='), app.indexOf('const setupWorkspaceWindow =') + 4500);
  assert.match(setup, /const open = \(\) => \{[\s\S]*?bringToFront\(\);[\s\S]*?options.onOpen\?\.\(\);/);
  assert.ok(app.includes('entry.category === requestedMigrationLog.group.toLowerCase() && entry.log_id === requestedMigrationLog.id'));
});
