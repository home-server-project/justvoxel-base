// Run with node. Exercises production wizard code with mocked DOM and APIs only.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const base = path.join(__dirname, '../webui/internal/server/static');
const selectionSource = fs.readFileSync(path.join(base, 'setup-storage.js'), 'utf8');
const actionSource = fs.readFileSync(path.join(base, 'setup-storage-actions.js'), 'utf8');
const browserSource = fs.readFileSync(path.join(base, 'storage-browser.js'), 'utf8');
const storageMarkup = fs.readFileSync(path.join(base, '../templates/storage_browser.html'), 'utf8');
class Node {
  constructor(dataset = {}, value = '') {
    this.dataset = dataset; this.value = value; this.checked = false; this.hidden = false;
    this.disabled = false; this.type = 'text'; this.name = ''; this.textContent = '';
    this.children = []; this.events = {}; this.one = {}; this.many = {}; this.attributes = {};
    this.classes = new Set();
    this.classList = {contains: name => this.classes.has(name), toggle() {}, remove() {}}; this.style = {setProperty() {}};
  }
  querySelector(s) {
    if (s === '[data-setup-disk][aria-pressed="true"]') return (this.many['[data-setup-disk]'] || []).find(node => node.attributes['aria-pressed'] === 'true') || null;
    if (s === 'dialog[open]') return Object.values(this.one).find(node => node.tag === 'dialog' && node.open) || null;
    return this.one[s] || null;
  }
  querySelectorAll(s) { return this.many[s] || []; }
  addEventListener(name, fn) { (this.events[name] ||= []).push(fn); }
  dispatchEvent(event) { (this.events[event.type] || []).forEach(fn => fn(event)); }
  async emit(type) { if (!this.disabled) await Promise.all((this.events[type] || []).map(fn => fn({target: this, preventDefault() {}}))); }
  click() { if (!this.disabled) this.dispatchEvent({type: 'click', target: this}); }
  setAttribute(k, v) { this.attributes[k] = v; }
  removeAttribute(k) { delete this.attributes[k]; }
  getAttribute(k) { return this.attributes[k] || null; }
  hasAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attributes, k); }
  scrollIntoView() { throw new Error('Wizard must not scroll to reveal controls'); } focus() { this.focused = true; } remove() {}
  after(node) {this.following = node;}
  closest(s) {
    if (s === '[data-setup-disk-partitions]') return this.diskGroup;
    if (s === '[data-storage-workspace-dialog]') return this.workspace || null;
    return null;
  }
  append(...nodes) { this.children.push(...nodes); }
  appendChild(node) { this.children.push(node); }
  prepend(...nodes) { this.children.unshift(...nodes); }
  replaceChildren(...nodes) { this.children = nodes; }
  contains(node) { return this.children.includes(node); }
  showModal() { this.open = true; this.openCount = (this.openCount || 0) + 1; }
  close() { this.open = false; }
  matches(s) {
    if (s === '[data-storage-action-csrf]') return 'storageActionCsrf' in this.dataset;
    return this.kind === 'backup' ? s.includes('backup-form') : s.includes('storage-form');
  }
}
function fixture(kind, controlCenter = false, paged = false) {
  let current, fresh;
  let workspaceRefreshes = 0;
  const requests = [];
  const workspace = new Node(), workspaceRefresh = new Node();
  workspace.one['[data-storage-refresh]'] = workspaceRefresh;
  workspaceRefresh.addEventListener('click', () => { workspaceRefreshes++; });
  const formSelector = '[data-setup-storage-form], [data-setup-backup-form]';
  const document = {
    querySelector(s) {
      if (s === formSelector) return current;
      if (s === '[data-setup-storage-form]') return kind === 'storage' ? current : null;
      if (s === '[data-setup-backup-form]') return kind === 'backup' ? current : null;
      if (s === '[data-setup-inventory-error]') return current.one[s];
      return null;
    },
    querySelectorAll(s) { return s === formSelector ? [current] : []; },
    addEventListener() {}, createElement() { return new Node(); },
  };
  function makeForm(devices = []) {
    const f = new Node({csrf: 'fixture-csrf'}); f.kind = kind;
    f.workspace = workspace;
    f.attributes.action = kind === 'backup' ? '/setup/backups' : '/setup/storage';
    f.parentElement = document;
    f.replaceWith = replacement => { current = replacement; };
    const fields = [];
    const add = (selector, name, value, dataset = {}) => {
      const field = new Node(dataset, value); field.name = name; field.type = name ? 'hidden' : 'text';
      f.one[selector] = field; fields.push(field); return field;
    };
    const prefix = kind === 'backup' ? 'backup' : 'storage';
    add(`[data-setup-${prefix}-type]`, `${prefix}_type`, 'system');
    add(`[data-setup-${prefix}-device]`, `${prefix}_device`, '');
    add(`[data-setup-${prefix}-mount]`, `${prefix}_mount_point`, '');
    add(`[data-setup-${prefix}-path]`, `${prefix}_path`, `/var/lib/justvoxel/${kind === 'backup' ? 'backups' : 'minecraft'}`);
    const csrf = add('[csrf]', 'csrf', 'new-csrf');
    f.one['[data-setup-storage-type], [data-setup-backup-type]'] = f.one[`[data-setup-${prefix}-type]`];
    f.one['[data-setup-storage-device], [data-setup-backup-device]'] = f.one[`[data-setup-${prefix}-device]`];
    f.one['[data-setup-storage-mount], [data-setup-backup-mount]'] = f.one[`[data-setup-${prefix}-mount]`];
    if (kind === 'backup') {
      for (const field of ['source', 'username', 'domain']) add(`[data-setup-backup-${field}]`, 'backup_' + field, '');
      for (const net of ['nfs', 'smb']) {
        for (const field of ['source', 'mount', 'path']) add(`[data-setup-network-${field}="${net}"]`, '', '', {[`setupNetwork${field[0].toUpperCase() + field.slice(1)}`]: net});
      }
      for (const field of ['username', 'domain']) add(`[data-setup-smb-${field}]`, '', '', {[`setupSmb${field}`]: ''});
      add('[backup-keep]', 'backup_keep', '7');
      add('[backup-time]', 'backup_daily_time', '04:00').type = 'time';
      const automatic = add('[backup-automatic]', 'backup_automatic', 'on'); automatic.type = 'checkbox'; automatic.checked = true;
      // This fixture also ensures future password inputs stay in memory only.
      add('[password]', 'backup_password', 'memory-only-secret').type = 'password';
    }
    const choices = devices.map(device => new Node({storagePartition: '', path: device, device, type: 'part', filesystem: 'xfs', system: 'No', readonly: 'No', uuid: 'uuid-' + device, mountpoint: '/srv/' + device.split('/').pop()}));
    f.many[kind === 'backup' ? '[data-setup-backup-existing]' : '[data-setup-existing-partition]'] = choices;
    f.many['[data-setup-existing-partition], [data-setup-backup-existing]'] = choices;
    f.many['input, select, textarea'] = fields;
    f.many['[data-setup-storage-device], [data-setup-storage-mount], [data-setup-storage-path], [data-setup-backup-device], [data-setup-backup-mount], [data-setup-backup-path]'] = fields.filter(field => new RegExp('^' + prefix + '_(device|mount_point|path)$').test(field.name));
    f.many['[data-setup-disk]'] = [new Node({setupDisk: '/dev/vdc', diskIdentity: 'fixture-disk'})];
    const group = new Node({setupDiskPartitions: '/dev/vdc'}); group.many['[data-device]'] = choices; choices.forEach(choice => {choice.diskGroup = group;});
    f.many['[data-setup-disk-partitions]'] = [group];
    f.many['[data-storage-disk]'] = f.many['[data-setup-disk]'];
    f.many['[data-storage-partitions]'] = [group];
    const system = new Node({systemPath: '/var/lib/justvoxel/' + (kind === 'backup' ? 'backups' : 'minecraft'), setupBackupChoice: 'system'});
    if (kind === 'storage') f.one['[data-setup-storage-system]'] = system;
    else f.many['[data-setup-backup-choice]'] = [system, new Node({setupBackupChoice: 'partition'}), new Node({setupBackupChoice: 'nfs'}), new Node({setupBackupChoice: 'smb'})];
    // Build shared operation controls from their production template selectors.
    const components = [
      ['storage_partition_operation', 'data-storage-detail-dialog'],
      ['storage_free_operation', 'data-storage-free-detail-dialog'],
      ['storage_partition_create_review', 'data-storage-create-dialog'],
      ['storage_partition_action_review', 'data-storage-action-dialog'],
    ];
    for (const [name, attr] of components) {
      const block = storageMarkup.split('{{define "' + name + '"}}')[1].split('{{end}}')[0];
      const container = new Node();
      container.tag = 'dialog';
      f.one['[' + attr + ']'] = container;
      for (const match of block.matchAll(/\b(data-[a-z-]+)(?:="([^"]*)")?/g)) {
        const selector = '[' + match[1] + ']';
        const node = new Node(); node.hidden = true;
        if (match[1] === attr) continue;
        container.one[selector] = node; f.one[selector] = node;
        if (match[1] === 'data-storage-action') {
          node.dataset.storageAction = match[2];
          (f.many[selector] ||= []).push(node);
        }
      }
    }
    f.one['[data-storage-action-menu]'].one['.storage-action-menu-popover'] = new Node();
    f.one['[data-setup-inventory-error]'] = new Node();
    f.one['[data-storage-action-csrf]'] = new Node({storageActionCsrf: ''}, 'new-csrf');
    fields.push(f.one['[data-storage-action-csrf]']);
    f.many['[data-storage-partition]'] = [...choices, new Node({path: '/dev/protected', system: 'Yes', type: 'part', filesystem: 'xfs'})];
    f.many['[data-storage-free-space]'] = [new Node({device: '/dev/vdc', start: '1MiB', size: '40 GiB', sizeBytes: String(40 * 1024 ** 3)})];
    f.many['[data-storage-partition], [data-storage-free-space]'] = [...f.many['[data-storage-partition]'], ...f.many['[data-storage-free-space]']];
    if (paged) {
      const disks = f.many['[data-setup-disk]'];
      for (let i = 1; i < 9; i++) disks.push(new Node({setupDisk: '/dev/disk' + i, diskIdentity: 'disk-' + i}));
      const driveGrid = new Node(), partitionGrid = new Node();
      driveGrid.classes.add('storage-disk-grid'); driveGrid.children = disks;
      partitionGrid.classes.add('storage-partition-grid');
      partitionGrid.children = Array.from({length: 19}, (_, i) => new Node({device: '/dev/part' + i}));
      f.many['.storage-disk-grid, .storage-partition-grid'] = [driveGrid, partitionGrid];
    }
    return f;
  }
  current = makeForm(['/dev/vdb1']);
  let deny = false, refreshFails = false, statusError = null, statusErrorDevice = '', applyError = null, status = {device: '/dev/vdb1', uuid: 'uuid-/dev/vdb1', persistence: 'none', mounted: false, mount_point: '/srv/vdb1'};
  const context = {
    document, window: {}, URLSearchParams, console,
    CustomEvent: class {constructor(type, options) {this.type = type; this.detail = options.detail;}},
    sessionStorage: {getItem() { return null; }, removeItem() {}, setItem() { throw new Error('Secret or form state entered browser storage'); }},
    DOMParser: class {parseFromString() { return {querySelector: () => fresh}; }},
    async fetch(url, options = {}) {
      requests.push({url, body: options.body ? new URLSearchParams(options.body) : null});
      if (url === '/setup') { if (refreshFails) return {ok: false}; fresh = makeForm(['/dev/vdb1', '/dev/vdc1']); if (status.mounted) fresh.many['[data-storage-partition]'][0].dataset.mountpoint = status.current_mount_point; return {ok: true, text: async () => 'mocked wizard'}; }
      if (url.includes('/mounts/status')) return statusError && (!statusErrorDevice || url.endsWith(encodeURIComponent(statusErrorDevice))) ? {ok: false, json: async () => ({ok: false, error: statusError})} : {ok: true, json: async () => ({ok: true, proposed: status})};
      if (applyError && url.endsWith('/mounts/apply')) return {ok: false, json: async () => ({ok: false, error: applyError})};
      if (deny) return {ok: false, json: async () => ({ok: false, error: 'Protected or unavailable'})};
      const values = Object.fromEntries(new URLSearchParams(options.body));
      if (url.endsWith('/mounts/apply')) status = {device: values.device, uuid: 'uuid-' + values.device, persistence: 'justvoxel', mounted: true, current_mount_point: values.mount_point, mount_point: values.mount_point};
      return {ok: true, json: async () => ({ok: true, proposed: {...values, device: values.device, fingerprint: 'disk-fingerprint', confirmation: ['create_partition', 'format', 'delete_partition'].includes(values.operation) ? 'CONFIRM ' + values.device : '', planned_end: '20481MiB'}, warnings: ['fixture warning']})};
    },
  };
  vm.createContext(context);
  vm.runInContext(browserSource, context);
  if (controlCenter) context.window.JustVoxelStorageBrowser.init(current);
  else {
    vm.runInContext(actionSource, context);
    vm.runInContext(selectionSource, context);
  }
  const get = key => current.one['[data-storage-' + key + ']'];
  return {context, get, requests, workspaceRefreshes: () => workspaceRefreshes, statusFailure(value, device = '') {statusError = value; statusErrorDevice = device;}, applyFailure(value) {applyError = value;}, refreshFailure(value) {refreshFails = value;}, form: () => current, deny(value) {deny = value;}, status(value) {status = {device: "/dev/vdb1", uuid: "uuid-/dev/vdb1", mount_point: "/srv/vdb1", ...value};}, changedUUID() {current.many['[data-setup-existing-partition], [data-setup-backup-existing]'][0].dataset.uuid = 'old-uuid';}, diskChanged() {current.many['[data-setup-disk]'][0].dataset.diskIdentity = 'old-disk';}};
}
const settle = async () => { for (let i = 0; i < 15; i++) await Promise.resolve(); };
(async () => {
  // A formatted card with stale selection markup must still be blocked by the adapter.
  for (const kind of ['storage', 'backup']) {
    for (const protection of ['system', 'readonly']) {
      const f = fixture(kind);
      const card = f.form().many['[data-storage-partition]'][0];
      const choice = f.get('detail-actions').children[0];
      const device = f.form().one[`[data-setup-${kind}-device]`];
      let selections = 0;
      f.form().addEventListener('setup-destination-select', () => { selections++; });
      card.dataset[protection] = 'Yes';
      card.click(); await settle();
      assert.equal(f.get('detail-dialog').open, true);
      assert.equal(f.form().one['[data-detail-path]'].textContent, '/dev/vdb1');
      assert.equal(f.form().one['[data-detail-filesystem]'].textContent, 'XFS');
      assert.equal(choice.hidden, true);
      choice.click();
      assert.equal(device.value, '');
      assert.equal(selections, 0);
      // Recheck eligibility even if protection changes after the menu was opened.
      card.dataset[protection] = 'No';
      card.click(); await settle();
      assert.equal(choice.hidden, false);
      card.dataset[protection] = 'Yes';
      choice.click();
      assert.equal(device.value, '');
      assert.equal(selections, 0);
    }
  }
  for (const kind of ['storage', 'backup']) {
    const f = fixture(kind), prefix = kind === 'backup' ? 'backup' : 'storage';
    const card = f.form().many['[data-storage-partition]'][0];
    card.dataset.mounted = 'Yes';
    if (kind === 'backup') { f.form().dataset.dataDevice = '/dev/vdb1'; f.form().dataset.dataMount = '/srv/stale'; }
    f.status({persistence: 'justvoxel', mounted: true, current_mount_point: '/var/mnt/vdb1', mount_point: '/var/mnt/vdb1'});
    card.click();
    assert.equal(f.get('detail-actions').children[0].hidden, true);
    await settle();
    const choose = f.get('detail-actions').children[0];
    assert.equal(choose.hidden, false);
    assert.equal(choose.textContent, 'Use this partition');
    assert(!f.get('action-menu').one['.storage-action-menu-popover'].children.includes(choose));
    const mountRequests = f.requests.filter(r => r.url.endsWith('/mounts/apply')).length;
    choose.click();
    assert.equal(f.requests.filter(r => r.url.endsWith('/mounts/apply')).length, mountRequests);
    assert.equal(f.form().one[`[data-setup-${prefix}-mount]`].value, '/var/mnt/vdb1');
    // An unrelated storage operation refreshes the inventory without changing the selected mounted destination.
    f.form().many['[data-storage-free-space]'][0].click(); f.get('create-partition').click();
    f.get('create-all').checked = true;
    await f.get('create-review').emit('click');
    f.get('create-confirm-slider').value = '100'; await f.get('create-confirm-slider').emit('input');
    f.get('create-confirm-toggle').checked = true; await f.get('create-confirm-toggle').emit('change');
    await f.get('create-apply').emit('click'); await settle();
    assert.equal(f.form().one[`[data-setup-${prefix}-type]`].value, 'partition');
    assert.equal(f.form().one[`[data-setup-${prefix}-device]`].value, '/dev/vdb1');
    assert.equal(f.form().one[`[data-setup-${prefix}-mount]`].value, '/var/mnt/vdb1');
    const refreshedCard = f.form().many['[data-storage-partition]'][0];
    const refreshedChoose = f.get('detail-actions').children[0];
    for (const status of [
      {persistence: 'conflict', mounted: true},
      {persistence: 'conflict', mounted: false},
      {device: '/dev/changed', persistence: 'justvoxel', mounted: true},
      {persistence: 'justvoxel', mounted: true, current_mount_point: '/srv/changed', mount_point: '/srv/saved'},
      {persistence: 'justvoxel', mounted: true, uuid: 'changed-uuid', current_mount_point: '/var/mnt/vdb1', mount_point: '/var/mnt/vdb1'},
    ]) {
      f.status(status); refreshedCard.click(); await settle(); assert.equal(refreshedChoose.hidden, true);
      refreshedChoose.click(); assert.equal(f.form().one[`[data-setup-${prefix}-mount]`].value, '/var/mnt/vdb1');
    }
  }
  for (const kind of ['storage', 'backup']) {
    const f = fixture(kind), prefix = kind === 'backup' ? 'backup' : 'storage';
    const field = name => f.form().one[`[data-setup-${prefix}-${name}]`];
    const choose = async () => {
      f.form().many['[data-storage-partition]'][0].click(); await settle();
      const card = f.get('detail-dialog');
      assert.equal(card.open, true);
      assert(card.openCount >= 1);
      let prevented = false;
      f.form().dispatchEvent({type: 'submit', preventDefault() { prevented = true; }});
      assert(prevented, 'Enter inside a storage modal must not advance the outer wizard form');
      assert.equal(f.form().one['[data-detail-path]'].textContent, '/dev/vdb1');
      assert.equal(f.form().one['[data-detail-filesystem]'].textContent, 'XFS');
      assert(!f.get('action-dialog').open && !f.get('create-dialog').open);
      f.get('detail-close').click();
      assert.equal(card.open, false);
      f.form().many['[data-storage-partition]'][0].click(); await settle();
      assert.equal(card.open, true);
      f.get('detail-actions').children[0].click();
      assert.equal(card.open, false);
    };
    await choose();
    assert.equal(field('device').value, '/dev/vdb1');
    if (kind === 'backup') {
      f.form().one['[data-setup-network-source="smb"]'].value = '//nas/unsaved';
      f.form().one['[data-setup-network-path="nfs"]'].value = '/var/mnt/custom/backups';
      f.form().one['[backup-keep]'].value = '19';
      f.form().one['[backup-time]'].value = '09:37';
      f.form().one['[backup-automatic]'].checked = false;
    }
    f.form().many['[data-storage-free-space]'][0].click();
    assert.equal(f.get('free-detail-dialog').open, true);
    assert.equal(f.get('detail-dialog').open, false);
    f.get('create-partition').click();
    assert.equal(f.get('free-detail-dialog').open, false);
    assert.equal(f.get('create-dialog').open, true);
    assert(!f.get('action-dialog').open);
    f.get('create-all').checked = false; f.get('create-size').value = '20';
    await f.get('create-review').emit('click');
    let sent = f.requests.at(-1);
    assert.equal(sent.url, '/api/new-storage/actions/plan');
    assert.equal(sent.body.get('size_gib'), '20'); assert.equal(sent.body.get('free_start'), '1MiB');
    assert.equal(f.get('create-apply').disabled, true);
    const count = f.requests.length;
    await f.get('create-apply').emit('click'); assert.equal(f.requests.length, count);
    f.get('create-confirm-slider').value = '100';
    await f.get('create-confirm-slider').emit('input');
    f.get('create-confirm-toggle').checked = true;
    await f.get('create-confirm-toggle').emit('change');
    const oldForm = f.form();
    const inventoryRefreshes = () => f.requests.filter(r => r.url === '/setup').length;
    const beforeCreate = inventoryRefreshes();
    await f.get('create-apply').emit('click'); await settle();
    assert.equal(inventoryRefreshes(), beforeCreate + 1, 'Wizard creation refreshes inventory exactly once');
    assert.equal(f.workspaceRefreshes(), 0, 'Wizard creation must not also refresh a workspace');
    sent = f.requests.find(r => r.url.endsWith('/apply'));
    assert.equal(sent.body.get('fingerprint'), 'disk-fingerprint');
    assert.equal(sent.body.get('confirmation'), 'CONFIRM /dev/vdc');
    assert.equal(sent.body.get('size_gib'), '20');
    assert.notEqual(oldForm.inert, true);
    assert.equal(oldForm.dataset.inventoryStale, 'true');
    assert.notEqual(f.form(), oldForm);
    assert.equal(field('device').value, '/dev/vdb1');
    assert.equal(f.form().one['[csrf]'].value, 'new-csrf');
    assert.equal(f.get('action-csrf').value, 'new-csrf');
    assert.equal(f.form().getAttribute('action'), kind === 'backup' ? '/setup/backups' : '/setup/storage');
    assert.equal(f.form().many['[data-setup-disk]'][0].getAttribute('aria-pressed'), 'true');
    if (kind === 'backup') {
      assert.equal(f.form().one['[data-setup-network-source="smb"]'].value, '//nas/unsaved');
      assert.equal(f.form().one['[data-setup-network-path="nfs"]'].value, '/var/mnt/custom/backups');
      assert.equal(f.form().one['[backup-keep]'].value, '19');
      assert.equal(f.form().one['[backup-time]'].value, '09:37');
      assert.equal(f.form().one['[backup-automatic]'].checked, false);
      assert.equal(f.form().one['[password]'].value, 'memory-only-secret');
    }
    // The shared confirmation review also handles destructive partition actions.
    for (const operation of ['format', 'delete_partition']) {
      f.form().many['[data-storage-partition]'][0].click(); await settle();
      const action = f.form().many['[data-storage-action]'].find(n => n.dataset.storageAction === operation);
      action.click();
      assert.equal(f.get('detail-dialog').open, false);
      assert.equal(f.get('action-dialog').open, true);
      await f.get('action-review-button').emit('click');
      assert.equal(f.get('action-apply-button').disabled, true);
      f.get('confirm-slider').value = '100'; await f.get('confirm-slider').emit('input');
      f.get('confirm-toggle').checked = true; await f.get('confirm-toggle').emit('change');
      const beforeAction = inventoryRefreshes();
      await f.get('action-apply-button').emit('click'); await settle();
      assert.equal(inventoryRefreshes(), beforeAction + 1, 'Wizard actions refresh inventory exactly once');
      assert.equal(f.workspaceRefreshes(), 0);
      assert.equal(f.requests.filter(r => r.url.endsWith('/apply')).at(-1).body.get('fingerprint'), 'disk-fingerprint');
    }
    f.form().many['[data-storage-partition]'][0].click(); await settle();
    f.form().many['[data-storage-action]'].find(n => n.dataset.storageAction === 'mount-permanently').click();
    f.get('mount-input').value = '/srv/custom';
    await f.get('action-review-button').emit('click');
    assert.equal(f.requests.at(-1).url, '/api/new-storage/mounts/plan');
    assert.equal(f.get('action-apply-button').disabled, false);
    const beforeMount = inventoryRefreshes();
    await f.get('action-apply-button').emit('click'); await settle();
    assert.equal(inventoryRefreshes(), beforeMount + 1, 'Wizard mount refreshes inventory exactly once');
    assert.equal(f.workspaceRefreshes(), 0);
    const mounted = f.requests.filter(r => r.url.endsWith('/apply')).at(-1);
    assert.equal(mounted.url, '/api/new-storage/mounts/apply');
    assert.equal(mounted.body.get('mount_point'), '/srv/custom');
    assert.equal(mounted.body.get('fingerprint'), 'disk-fingerprint');
    assert.equal(field('device').value, '', 'A changed mount location requires an explicit new choice');
    assert.equal(field('mount').value, '');
    assert.equal(field('path').value, '');
    assert(f.form().one['[data-setup-inventory-error]'].textContent.startsWith('Drive mounted successfully.'));
    f.form().many['[data-storage-partition]'][0].click(); await settle();
    f.get('detail-actions').children[0].click();
    assert.equal(field('mount').value, '/srv/custom');
    f.status({persistence: 'none', mounted: false});
    // Protected cards use the same safeguards as Control Center.
    f.form().many['[data-storage-partition]'].at(-1).click();
    assert.equal(f.get('detail-actions').hidden, true);
    assert(f.form().many['[data-storage-action]'].every(n => n.hidden));
    for (const change of [f.changedUUID, f.diskChanged]) {
      await choose(); change();
      f.form().many['[data-storage-free-space]'][0].click(); f.get('create-partition').click();
      f.get('create-all').checked = true;
      await f.get('create-review').emit('click');
      f.get('create-confirm-slider').value = '100'; await f.get('create-confirm-slider').emit('input');
      f.get('create-confirm-toggle').checked = true; await f.get('create-confirm-toggle').emit('change');
      await f.get('create-apply').emit('click'); await settle();
      assert.equal(field('device').value, ''); assert.equal(field('path').value, '');
    }
    f.form().many['[data-storage-free-space]'][0].click(); f.get('create-partition').click();
    f.get('create-all').checked = true;
    await f.get('create-review').emit('click');
    f.get('create-size').value = '10'; await f.get('create-size').emit('input');
    assert.equal(f.get('create-apply').hidden, true);
    f.get('create-all').checked = true;
    await f.get('create-review').emit('click');
    f.get('create-confirm-slider').value = '100'; await f.get('create-confirm-slider').emit('input');
    f.get('create-confirm-toggle').checked = true; await f.get('create-confirm-toggle').emit('change');
    f.refreshFailure(true);
    const stale = f.form();
    await f.get('create-apply').emit('click'); await settle();
    assert.equal(f.form(), stale); assert.notEqual(stale.inert, true);
    assert.equal(stale.dataset.inventoryStale, 'true');
    assert(stale.many['[data-storage-partition], [data-storage-free-space]'].every(card => card.disabled));
    const recovery = stale.one['[data-setup-inventory-error]'];
    assert.equal(recovery.hidden, false);
    assert.equal(recovery.getAttribute('role'), 'alert');
    assert(recovery.children[0].textContent.startsWith('Storage updated.'));
    assert(recovery.children[0].textContent.includes('go Back, or cancel setup'));
    for (const submitter of [new Node({}, 'back'), Object.assign(new Node(), {attributes: {formaction: '/setup/cancel'}})]) {
      let blocked = false;
      stale.dispatchEvent({type: 'submit', submitter, preventDefault() {blocked = true;}});
      assert.equal(blocked, false);
    }
    let blocked = false;
    stale.dispatchEvent({type: 'submit', submitter: new Node({}, 'next'), preventDefault() {blocked = true;}});
    assert.equal(blocked, true, 'Stale partition inventory must block Continue');
    recovery.children[1].click(); await settle();
    assert.equal(f.form(), stale, 'Persistent discovery failure remains recoverable');
    const system = kind === 'storage' ? stale.one['[data-setup-storage-system]'] : stale.many['[data-setup-backup-choice]'][0];
    system.click();
    blocked = false;
    stale.dispatchEvent({type: 'submit', submitter: new Node({}, 'next'), preventDefault() {blocked = true;}});
    assert.equal(blocked, false, 'Explicit system storage remains usable without inventory');
    recovery.children[1].click(); await settle();
    assert.equal(field('path').value, system.dataset.systemPath, 'A failed retry must retain explicit system storage');
    f.refreshFailure(false);
    recovery.children[1].click(); await settle();
    assert.notEqual(f.form(), stale);
    f.form().many['[data-storage-free-space]'][0].click(); f.get('create-partition').click();
    f.get('create-all').checked = true; f.deny(true);
    await f.get('create-review').emit('click');
    assert.equal(f.get('create-apply').hidden, true);
    assert.equal(f.get('create-error').hidden, false);
    assert(f.requests.every(r => !r.url.includes('/workspace/')));
  }
  // A successful action stays successful when the selected partition's status
  // repeatedly fails. Fresh alternatives and navigation remain usable.
  for (const kind of ['storage', 'backup']) {
    const f = fixture(kind), field = name => f.form().one[`[data-setup-${kind}-${name}]`];
    f.form().many['[data-storage-partition]'][0].click(); await settle();
    f.get('detail-actions').children[0].click();
    assert.equal(field('device').value, '/dev/vdb1');
    f.statusFailure('Could not verify the host mount state for this partition.', '/dev/vdb1');
    f.form().many['[data-storage-free-space]'][0].click(); f.get('create-partition').click();
    f.get('create-all').checked = true;
    await f.get('create-review').emit('click');
    f.get('create-confirm-slider').value = '100'; await f.get('create-confirm-slider').emit('input');
    f.get('create-confirm-toggle').checked = true; await f.get('create-confirm-toggle').emit('change');
    const oldForm = f.form();
    await f.get('create-apply').emit('click'); await settle();
    assert.notEqual(f.form(), oldForm, 'Status failure must not discard successful discovery');
    assert.notEqual(f.form().inert, true);
    assert.equal(field('type').value, 'partition', 'Never silently choose system storage');
    for (const name of ['device', 'mount', 'path']) assert.equal(field(name).value, '');
    const notice = f.form().one['[data-setup-inventory-error]'];
    assert.equal(notice.getAttribute('role'), 'alert');
    assert(notice.textContent.startsWith('Storage updated.'));
    assert(notice.textContent.includes('selection was cleared'));
    const applied = f.requests.filter(r => r.url.endsWith('/actions/apply')).length;
    for (let attempt = 0; attempt < 2; attempt++) {
      f.form().many['[data-storage-partition]'][0].click(); await settle();
      const choice = f.get('detail-actions').children[0];
      assert.equal(choice.hidden, true);
      choice.click(); assert.equal(field('device').value, '');
      f.get('detail-close').click();
      assert.equal(f.get('detail-dialog').open, false);
    }
    for (const submitter of [new Node({}, 'back'), Object.assign(new Node(), {attributes: {formaction: '/setup/cancel'}})]) {
      let blocked = false;
      f.form().dispatchEvent({type: 'submit', submitter, preventDefault() {blocked = true;}});
      assert.equal(blocked, false, 'Recovery must preserve wizard navigation');
    }
    // The original device still fails; a different verified destination is usable.
    f.status({device: '/dev/vdc1', uuid: 'uuid-/dev/vdc1', persistence: 'none', mounted: false, mount_point: '/srv/vdc1'});
    f.form().many['[data-storage-partition]'][1].click(); await settle();
    assert.equal(f.get('detail-actions').children[0].hidden, false);
    f.get('detail-actions').children[0].click();
    assert.equal(field('device').value, '/dev/vdc1');
    assert.equal(field('mount').value, '/srv/vdc1');
    const system = kind === 'storage' ? f.form().one['[data-setup-storage-system]'] : f.form().many['[data-setup-backup-choice]'][0];
    system.click();
    assert.equal(field('type').value, 'system');
    assert.equal(field('device').value, '');
    assert.equal(field('path').value, system.dataset.systemPath);
    assert.equal(f.requests.filter(r => r.url.endsWith('/actions/apply')).length, applied, 'Refresh failure must not repeat a successful mutation');
  }
  for (const kind of ['storage', 'backup']) {
    for (const persistence of ['none', 'justvoxel', 'external']) {
      const f = fixture(kind);
      const card = f.form().many['[data-storage-partition]'][0];
      card.dataset.mountpoints = '/srv/vdb1';
      f.status({persistence, mounted: true, current_mount_point: '/srv/vdb1', mount_point: '/srv/vdb1'});
      card.click(); await settle();
      assert.equal(f.get('detail-actions').children[0].hidden, false);
      f.get('detail-actions').children[0].click();
      assert.equal(f.form().one[`[data-setup-${kind}-mount]`].value, '/srv/vdb1');
      f.status({persistence, mounted: true, current_mount_point: '/srv/changed', mount_point: '/srv/changed'});
      card.click(); await settle();
      assert.equal(f.get('detail-actions').children[0].hidden, true, 'Changed inventory mount locations must remain blocked');
    }
  }
  for (const kind of ['storage', 'backup']) {
    const f = fixture(kind);
    const card = f.form().many['[data-storage-partition]'][0];
    f.statusFailure('Could not verify the host mount state for this partition.');
    card.click(); await settle();
    assert.equal(f.get('detail-actions').children[0].hidden, true);
    const feedback = f.get('detail-dialog').children[0];
    assert.equal(feedback.getAttribute('role'), 'alert');
    assert(feedback.textContent.includes('Partition cannot be selected safely.'));
    f.statusFailure(null);
    f.status({persistence: 'conflict', mounted: false});
    card.click(); await settle();
    assert(feedback.textContent.includes('Saved mount location conflicts with another filesystem.'));
    assert.equal(f.get('detail-actions').children[0].hidden, true);
    f.status({persistence: 'none', mounted: false});
    card.click(); await settle();
    f.form().many['[data-storage-action]'].find(n => n.dataset.storageAction === 'mount-permanently').click();
    f.get('mount-input').value = '/var/mnt/vdb1';
    await f.get('action-review-button').emit('click');
    f.applyFailure('The saved mount location conflicts with another filesystem.');
    await f.get('action-apply-button').emit('click');
    assert.equal(f.get('action-error').hidden, false);
    assert(f.get('action-error').textContent.includes('Saved mount location conflicts with another filesystem.'));
    assert.equal(f.get('action-dialog').open, true);
    assert.equal(f.form().inert, undefined);
    f.applyFailure('The drive did not respond.');
    await f.get('action-apply-button').emit('click');
    assert(f.get('action-error').textContent.includes('Mount operation failed. The drive did not respond.'));
    assert.equal(f.requests.filter(r => r.url === '/setup').length, 0, 'Failed actions must retain their error dialog without refreshing');
    assert.equal(f.workspaceRefreshes(), 0);
  }
  for (const kind of ['storage', 'backup']) {
    const f = fixture(kind, false, true);
    for (const grid of f.form().many['.storage-disk-grid, .storage-partition-grid']) {
      const size = 4;
      const [previous, label, next] = grid.following.children;
      const visited = new Set();
      assert(previous.disabled);
      do {
        const visible = grid.children.filter(card => !card.hidden);
        assert(visible.length > 0 && visible.length <= size);
        visible.forEach(card => visited.add(card));
        assert(label.textContent.startsWith('Page '));
        if (next.disabled) break;
        next.click();
      } while (true);
      assert.equal(visited.size, grid.children.length, 'Every inventory card must remain reachable');
      assert(previous.focused, 'Focus stays on a reachable paging action');
      while (!previous.disabled) previous.click();
      assert(next.focused);
      assert.equal(grid.children.filter(card => !card.hidden).length, size);
    }
  }
  // Control Center uses the same component with its original modal presentation.
  const control = fixture('storage', true);
  control.form().many['[data-storage-partition]'][0].click(); await settle();
  assert.equal(control.get('detail-dialog').open, true);
  assert.equal(control.get('detail-dialog').openCount, 1);
  assert.equal(control.form().one['[data-detail-path]'].textContent, '/dev/vdb1');
  assert.equal(control.form().one['[data-detail-filesystem]'].textContent, 'XFS');
  assert.equal(control.get('action-menu').one['.storage-action-menu-popover'].children.length, 0);
  control.get('action-menu').open = true;
  control.get('detail-close').click();
  assert.equal(control.get('detail-dialog').open, false);
  assert.equal(control.get('action-menu').open, false);
  control.form().many['[data-storage-partition]'].at(-1).click();
  assert.equal(control.get('detail-actions').hidden, true);
  assert(control.form().many['[data-storage-action]'].every(node => node.hidden));
  for (const operation of ['create_partition', 'format', 'mount-permanently']) {
    const f = fixture('storage', true);
    if (operation === 'create_partition') {
      f.form().many['[data-storage-free-space]'][0].click();
      f.get('create-partition').click(); f.get('create-all').checked = true;
      await f.get('create-review').emit('click');
      f.get('create-confirm-slider').value = '100'; await f.get('create-confirm-slider').emit('input');
      f.get('create-confirm-toggle').checked = true; await f.get('create-confirm-toggle').emit('change');
      await f.get('create-apply').emit('click');
    } else {
      f.form().many['[data-storage-partition]'][0].click(); await settle();
      f.form().many['[data-storage-action]'].find(node => node.dataset.storageAction === operation).click();
      if (operation === 'mount-permanently') f.get('mount-input').value = '/srv/custom';
      await f.get('action-review-button').emit('click');
      if (operation === 'format') {
        f.get('confirm-slider').value = '100'; await f.get('confirm-slider').emit('input');
        f.get('confirm-toggle').checked = true; await f.get('confirm-toggle').emit('change');
      }
      await f.get('action-apply-button').emit('click');
    }
    await settle();
    assert.equal(f.workspaceRefreshes(), 1, 'Successful Control Center ' + operation + ' refreshes its workspace exactly once');
    assert.equal(f.requests.filter(r => r.url === '/setup').length, 0, 'Control Center must not refresh wizard inventory');
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
