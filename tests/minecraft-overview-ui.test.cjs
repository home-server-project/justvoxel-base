// Approved Minecraft presentation and existing-endpoint coordination regressions.
const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {test} = require('node:test');
const vm = require('node:vm');
const read = name => readFileSync(`${__dirname}/../webui/internal/server/${name}`, 'utf8');
const source = read('static/app.js');
const css = read('static/app.css');
const header = read('templates/header.html');
const between = (start, end) => source.slice(source.indexOf(start), source.indexOf(end, source.indexOf(start)));
const overview = between('  const renderOverview =', '  const formatRemainingMemory =');
const deletion = between('  const openDeleteDialog =', '  const renderOverview =');
const gameplay = between('    } else if (tab === "gameplay")', '    } else if (tab === "crossplay")');

test('Minecraft navigation orders Recent logs after Overview with operator-plus permissions', () => {
  const nav = header.slice(header.indexOf('<nav class="minecraft-workspace-tabs"'), header.indexOf('<div class="minecraft-workspace-body">'));
  assert.deepEqual([...nav.matchAll(/data-minecraft-tab="([^"]+)"[^>]*>([^<]+)/g)].map(m => [m[1], m[2]]),
    [['overview', 'Overview'], ['logs', 'Recent logs'], ['memory', 'Memory'], ['gameplay', 'Gameplay'], ['players', 'Players'], ['crossplay', 'Cross-play']]);
  assert.match(nav, /class="nav-operator-plus"[^>]*data-minecraft-tab="logs"/);
  assert.match(source, /else if \(currentTab === "logs"\) await renderLogs\(sequence, message\)/);
  assert.match(between('  const renderLogs =', '  async function loadCurrentTab'), /requestWorkspaceJSON\("\/api\/minecraft\/workspace\/logs"\)/);
  assert.doesNotMatch(overview, /workspace\/logs|Recent logs|log-box/);
});

test('Overview puts Online directly after summary and uses the same label typography', () => {
  const grid = overview.indexOf('root.appendChild(grid)');
  const online = overview.indexOf('root.appendChild(playerLine)');
  const admin = overview.indexOf('if (document.body.classList.contains("role-administrator"))');
  assert.ok(grid >= 0 && grid < online && online < admin);
  assert.match(overview, /playerLabel.className = "minecraft-summary-label"/);
  const label = css.match(/\.minecraft-overview-playerline>\.minecraft-summary-label\{([^}]+)\}/)[1];
  for (const declaration of ['font-size:.72rem', 'font-weight:800', 'letter-spacing:.05em', 'text-transform:uppercase', 'color:var(--jv-muted)']) {
    assert.ok(label.includes(declaration));
    assert.ok(css.includes(declaration));
  }
});

test('Overview uses the setup-style toggle, combined framed Save and compact delete', () => {
  assert.doesNotMatch(overview, /<select|>Rename<|Danger zone|Type the current server name|name="confirm_name"/);
  assert.match(overview, /date-time-switch" type="checkbox" name="start_on_boot"/);
  assert.match(overview, /boot.dataset.minecraftSettingsTab = "overview"/);
  assert.match(overview, /params.set\("start_on_boot", String\(boot.querySelector\("input"\).checked\)\)/);
  assert.match(overview, /minecraft-overview-details/);
  assert.match(overview, /minecraft-native-grid.*Server name.*Server welcome message \(MOTD\).*minecraft-native-actions.*>Save</);
  assert.equal((overview.match(/>Save</g) || []).length, 1);
  assert.doesNotMatch(gameplay, /Server welcome message|<label[^>]*>.*MOTD/);
  assert.match(gameplay, /Maximum players/);
  assert.match(gameplay, /Game mode/);
  assert.match(gameplay, /name="motd" type="hidden"/);
  assert.match(overview, /title.textContent = `Delete \$\{name\}`/);
  assert.match(css, /\.minecraft-overview-delete h3\{[^}]*color:var\(--jv-error\);font-weight:800/);
  assert.match(overview, /remove.addEventListener\("click", \(\) => openDeleteDialog\(name, description, sequence\)\)/);
  assert.match(overview, /Permanently removes this active server and its world data\. Existing backup archives are preserved as historical archives\./);
});

test('Cross-play reuses Version opener inline and Support has a primary button and scoped gap', () => {
  assert.match(source, /minecraft-crossplay-status.*data-crossplay-compatibility.*· open.*data-open-version>Version</);
  assert.ok(source.includes('node.textContent = `Cross-play enabled · Minecraft ${installed} · Geyser/Floodgate compatible with ${status.geyser_supported_version}`;'));
  assert.match(source, /querySelector\('\[data-open-version\]'\).*document.querySelector\("\[data-version-open\]"\)\?\.click\(\)/);
  assert.match(css, /\.minecraft-crossplay-status\{[^}]*flex-wrap:wrap/);
  assert.doesNotMatch(source, /generateSupport.className = "secondary"/);
  assert.match(source, /generateSupport.textContent = "Generate support report"/);
  assert.match(source, /fileList.className = selectedGroup === "Support"\s*\? "system-logs-list system-support-report-files" : "system-logs-list"/);
  assert.match(css, /\.system-logs-list.system-support-report-files\{margin-top:\.8rem\}/);
  assert.doesNotMatch(header + read('templates/dashboard.html'), /(?:href|src)="[^"']*(?:setup|storage-browser)\.css/);
});

// Minimal DOM for the production Overview and deletion functions, including forms
// inserted through innerHTML. No browser or extra dependency is needed.
class Element {
  constructor(tag) {
    this.tag = tag; this.children = []; this.events = {}; this.attrs = {}; this.dataset = {};
    this.value = ''; this.checked = false; this.disabled = false;
    this.style = {setProperty() {}};
    this.classList = {contains: name => name === 'role-administrator', toggle() {}};
  }
  append(...nodes) { nodes.forEach(node => { node.parentElement = this; this.children.push(node); }); }
  appendChild(node) { this.append(node); }
  replaceChildren(...nodes) { this.children = []; this.append(...nodes); }
  setAttribute(key, value) { this.attrs[key] = value; }
  addEventListener(name, fn) { this.events[name] = fn; }
  all() { return [this, ...this.children.flatMap(node => node.all())]; }
  matches(selector) {
    if (selector.startsWith('[')) return Object.hasOwn(this.attrs, selector.slice(1, -1));
    return this.tag === selector;
  }
  querySelector(selector) { return this.all().slice(1).find(node => node.matches(selector)); }
  querySelectorAll(selector) {
    if (selector.startsWith('[data-destructive-confirmation]')) return this.all().filter(node => node.matches('[data-destructive-confirmation]') && !node.dataset.destructiveReady);
    return this.all().slice(1).filter(node => node.matches(selector));
  }
  closest(selector) { return this.matches(selector) ? this : this.parentElement?.closest(selector); }
  get elements() { return Object.fromEntries(this.all().filter(node => node.attrs.name).map(node => [node.attrs.name, node])); }
  set innerHTML(html) {
    this.children = []; const stack = [this];
    for (const match of html.matchAll(/<\/?([\w-]+)([^>]*)>/g)) {
      if (match[0].startsWith('</')) { stack.pop(); continue; }
      const node = new Element(match[1]);
      for (const attr of match[2].matchAll(/([\w-]+)(?:="([^"]*)")?/g)) {
        node.attrs[attr[1]] = attr[2] ?? '';
        if (attr[1].startsWith('data-')) node.dataset[attr[1].slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase())] = attr[2] ?? '';
      }
      node.value = node.attrs.value || ''; node.disabled = Object.hasOwn(node.attrs, 'disabled');
      stack.at(-1).append(node);
      if (!['input', 'br', 'hr'].includes(node.tag)) stack.push(node);
    }
  }
  showModal() { this.open = true; }
  close() { this.open = false; this.events.close?.(); }
  remove() { this.removed = true; }
}
const submit = {preventDefault() {}};
function harness({motd = 'Old welcome', plan = {review: 'real-fingerprint', warnings: ['The server stays stopped. Start it normally when ready.']}, apply = {applied: true}, renameError, deleteError} = {}) {
  const calls = [], reviews = [], redirects = [];
  const body = new Element('body'), content = new Element('content'), state = {};
  let reloads = 0, refreshes = 0;
  const context = vm.createContext({
    URLSearchParams, document: {body, createElement: tag => new Element(tag), querySelectorAll: () => []}, content, state,
    csrf: 'csrf-token', loadSequence: 1, justVoxelSelectionGeneration: 0,
    justVoxelStaleSelection: () => Object.assign(new Error('Selection changed'), {staleSelection: true}),
    window: {location: {reload() { reloads++; }, assign(url) { redirects.push(url); }}, addEventListener() {}, removeEventListener() {}},
    messageNode: text => Object.assign(new Element('p'), {textContent: text}), noticeNode: text => Object.assign(new Element('p'), {textContent: text}),
    settingsParams() { return new URLSearchParams({csrf: 'csrf-token', tab: 'overview'}); },
    renderSettingsReview: (...args) => reviews.push(args),
    loadCurrentTab: async () => { refreshes++; },
    requestWorkspaceJSON: async (url, options) => {
      calls.push({url: url + '?server=jv-family1', body: options?.body});
      if (url.endsWith('/snapshot') || url === '/api/dashboard-status') return {status: {minecraft: {configured: true}}, players: {online: 0}};
      if (url.endsWith('/selection')) return {selected: {id: 'jv-family1', name: 'Family'}, servers: [{id: 'jv-family1', name: 'Family'}]};
      if (url.endsWith('/settings')) return {minecraft: {motd, max_players: 23, game_mode: 'creative', start_on_boot: true}};
      if (url.endsWith('/plan')) return plan;
      if (url.endsWith('/apply')) return apply;
      if (url.endsWith('/rename')) { if (renameError) throw new Error(renameError); return {redirect: '/rename-is-not-delete'}; }
      if (url.endsWith('/delete')) { if (deleteError) throw new Error(deleteError); return {redirect: '/?deleted=1'}; }
      throw new Error(`Unexpected ${url}`);
    },
  });
  const shared = between('const initDestructiveConfirmations =', 'const initializeSharedWebUIControls =');
  const sharedDelete = between('globalThis.justVoxelOpenDeleteServer =', 'const minecraftDialog =');
  vm.runInContext(shared + sharedDelete + deletion + overview + '\nglobalThis.render = renderOverview; globalThis.openDelete = openDeleteDialog; globalThis.init = initDestructiveConfirmations;', context);
  return {context, body, content, state, calls, reviews, redirects, get reloads() { return reloads; }, get refreshes() { return refreshes; }};
}
async function editDetails(r, name = 'New family') {
  await r.context.render(1);
  const form = r.content.all().find(node => node.tag === 'form' && node.elements.display_name);
  assert.equal(form.elements.display_name.value, 'Family');
  assert.equal(form.elements.motd.value, 'Old welcome');
  form.elements.display_name.value = name; form.elements.motd.value = 'New welcome';
  await form.events.submit(submit);
}

test('Save plans and applies MOTD with current gameplay and real review before selected rename', async () => {
  const r = harness(); await editDetails(r);
  const mutations = r.calls.filter(call => call.body);
  assert.deepEqual(mutations.map(call => call.url), ['plan', 'apply', 'rename'].map(action => `/api/minecraft/workspace/${action === 'rename' ? action : 'settings/' + action}?server=jv-family1`));
  for (const call of mutations.slice(0, 2)) {
    assert.equal(call.body.get('motd'), 'New welcome'); assert.equal(call.body.get('max_players'), '23');
    assert.equal(call.body.get('game_mode'), 'creative'); assert.equal(call.body.get('tab'), 'gameplay');
    assert.equal(call.body.has('confirm_players'), false);
  }
  assert.equal(mutations[1].body.get('review'), 'real-fingerprint');
  assert.equal(mutations[2].body.get('display_name'), 'New family');
  assert.equal(r.reloads, 1); assert.deepEqual(r.redirects, []);
});

for (const safety of [{confirmation_required: true}, {restart_required: true}, {memory_restart_required: true}]) {
  test(`Save presents existing review for ${Object.keys(safety)[0]}`, async () => {
    const r = harness({plan: {review: 'fingerprint', ...safety}}); await editDetails(r);
    assert.equal(r.reviews.length, 1);
    assert.equal(r.calls.some(call => /\/apply|\/rename/.test(call.url)), false);
    assert.equal(typeof r.reviews[0][3].onApplied, 'function');
    assert.equal(r.reloads, 0);
  });
}

test('Partial save failure reloads authoritative state and displays rename error without success', async () => {
  const r = harness({renameError: 'Name already used.'}); await editDetails(r);
  assert.ok(r.calls.some(call => call.url.includes('/apply')));
  assert.ok(r.calls.at(-1).url.includes('/selection'));
  assert.equal(r.refreshes, 1); assert.equal(r.state.textContent, 'Name already used.'); assert.equal(r.reloads, 0);
});

test('Unapplied settings do not rename or claim success; unexpected confirmation presents review', async () => {
  const failure = harness({apply: {applied: false, error: 'Settings changed.'}}); await editDetails(failure);
  assert.equal(failure.calls.some(call => call.url.includes('/rename')), false);
  assert.equal(failure.state.textContent, 'Settings changed.'); assert.equal(failure.reloads, 0);
  const review = harness({apply: {applied: false, confirmation_required: true}}); await editDetails(review);
  assert.equal(review.reviews.length, 1); assert.equal(review.reloads, 0);
});

for (const deleteError of [undefined, 'Stop this server normally before deleting.']) {
  test(`Delete uses shared two-stage gate and authoritative name: ${deleteError ? 'recoverable error' : 'redirect'}`, async () => {
    const r = harness({deleteError}); r.context.openDelete('Family', 'Deletion description', 1);
    const dialog = r.body.children[0], form = dialog.querySelector('form');
    assert.equal(dialog.open, true); assert.equal(r.calls.length, 0);
    // Mirrors the existing MutationObserver initializing inserted controls.
    r.context.init(dialog);
    const slider = dialog.querySelector('[data-destructive-slider]'), toggle = dialog.querySelector('[data-destructive-toggle]');
    const apply = dialog.querySelector('[data-destructive-submit]');
    assert.equal(apply.disabled, true);
    await form.events.submit(submit); assert.equal(r.calls.length, 0);
    toggle.checked = true; toggle.events.change(); assert.equal(apply.disabled, true);
    slider.value = '100'; slider.events.input(); toggle.checked = false; toggle.events.change();
    assert.equal(apply.disabled, true); await form.events.submit(submit); assert.equal(r.calls.length, 0);
    toggle.checked = true; toggle.events.change(); assert.equal(apply.disabled, false);
    await form.events.submit(submit);
    assert.equal(r.calls[0].body.get('confirm_name'), 'Family');
    assert.equal(r.calls[0].url, '/api/minecraft/workspace/delete?server=jv-family1');
    if (deleteError) {
      assert.deepEqual(r.redirects, []); assert.equal(dialog.open, true);
      assert.equal(dialog.querySelector('[data-delete-error]').textContent, deleteError);
      assert.equal(apply.disabled, false); assert.equal(slider.disabled, false);
    } else assert.deepEqual(r.redirects, ['/?deleted=1']);
  });
}

test('Cancel and X dismiss without requests; stale selection cannot delete', async () => {
  for (const selector of ['[data-delete-close]', '[data-delete-cancel]']) {
    const r = harness(); r.context.openDelete('Family', '', 1);
    r.body.children[0].querySelector(selector).events.click();
    assert.equal(r.body.children[0].open, false); assert.equal(r.calls.length, 0);
  }
  const r = harness(); r.context.openDelete('Family', '', 1);
  const dialog = r.body.children[0];
  dialog.querySelector('[data-destructive-slider]').value = '100';
  dialog.querySelector('[data-destructive-toggle]').checked = true;
  r.context.justVoxelSelectionGeneration++;
  await dialog.querySelector('form').events.submit(submit);
  assert.equal(r.calls.length, 0);
});

test('Production workspace requests keep rename and settings scoped to the selected immutable ID', async () => {
  const urls = [];
  const context = vm.createContext({
    URL, URLSearchParams, document: {querySelector: () => null},
    location: {origin: 'https://example.test', search: '?server=jv-family1'},
    fetch: async url => { urls.push(url); return {ok: true, status: 200, json: async () => ({ok: true})}; },
  });
  const selectedURLs = source.slice(0, source.indexOf('globalThis.justVoxelServerSelection ='));
  const requests = between('  const requestWorkspaceJSON =', '  const openDeleteDialog =');
  vm.runInContext(selectedURLs + requests + '\nglobalThis.request = requestWorkspaceJSON;', context);
  for (const action of ['rename', 'settings/plan', 'settings/apply']) {
    await context.request(`/api/minecraft/workspace/${action}`, {method: 'POST', body: new URLSearchParams({display_name: 'New name'})});
  }
  assert.deepEqual(urls, ['rename', 'settings/plan', 'settings/apply'].map(action => `/api/minecraft/workspace/${action}?server=jv-family1`));
});

test('Empty MOTD blocks both operations and unchanged name skips rename', async () => {
  const empty = harness(); await empty.context.render(1);
  const form = empty.content.all().find(node => node.tag === 'form' && node.elements.display_name);
  form.elements.motd.value = '   ';
  await form.events.submit(submit);
  assert.equal(empty.calls.some(call => call.body), false);
  assert.equal(empty.state.textContent, 'Server welcome message is required.');
  const unchanged = harness(); await editDetails(unchanged, 'Family');
  assert.equal(unchanged.calls.some(call => call.url.includes('/rename')), false);
  assert.equal(unchanged.reloads, 1);
});
