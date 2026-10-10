// Run with node. Exercises production wizard code with mocked DOM and APIs only.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const base = path.join(__dirname, '../webui/internal/server/static');
const selectionSource = fs.readFileSync(path.join(base, 'setup-storage.js'), 'utf8');
const actionSource = fs.readFileSync(path.join(base, 'setup-storage-actions.js'), 'utf8');
class Node {
  constructor(dataset = {}, value = '') {
    this.dataset = dataset; this.value = value; this.checked = false; this.hidden = false;
    this.disabled = false; this.type = 'text'; this.name = ''; this.textContent = '';
    this.children = []; this.events = {}; this.one = {}; this.many = {};
    this.classList = {toggle() {}, remove() {}}; this.style = {setProperty() {}};
    this.attributes = {};
  }
  querySelector(s) {
    if (s === '[data-setup-disk][aria-pressed="true"]') return (this.many['[data-setup-disk]'] || []).find(node => node.attributes['aria-pressed'] === 'true') || null;
    return this.one[s] || null;
  }
  querySelectorAll(s) { return this.many[s] || []; }
  addEventListener(name, fn) { (this.events[name] ||= []).push(fn); }
  click() { if (!this.disabled) (this.events.click || []).forEach(fn => fn({})); }
  setAttribute(k, v) { this.attributes[k] = v; }
  getAttribute(k) { return this.attributes[k] || null; }
  scrollIntoView() {}
  append(...nodes) { this.children.push(...nodes); }
  replaceChildren(...nodes) { this.children = nodes; }
  showModal() { this.open = true; this.openCount = (this.openCount || 0) + 1; }
  close() { this.open = false; }
  matches(s) { return this.kind === 'backup' ? s.includes('backup-form') : s.includes('storage-form'); }
}
function fixture(kind) {
  let current, fresh;
  const requests = [];
  const dialog = new Node();
  for (const key of ['title', 'target', 'result', 'warnings', 'error', 'size', 'all', 'mount', 'slider', 'toggle', 'confirm', 'plan', 'apply', 'close', 'size-field', 'all-field', 'mount-field', 'confirm-summary', 'slider-shell', 'toggle-row']) dialog.one[`[data-setup-disk-${key}]`] = new Node();
  const get = key => dialog.one[`[data-setup-disk-${key}]`];
  const formSelector = '[data-setup-storage-form], [data-setup-backup-form]';
  const document = {
    querySelector(s) {
      if (s === '[data-setup-disk-review]') return dialog;
      if (s === formSelector) return current;
      if (s === '[data-setup-storage-form]') return kind === 'storage' ? current : null;
      if (s === '[data-setup-backup-form]') return kind === 'backup' ? current : null;
      return null;
    }, querySelectorAll() { return []; }, addEventListener() {}, createElement() { return new Node(); },
  };
  function makeForm(devices = []) {
    const f = new Node({csrf: 'fixture-csrf'}); f.kind = kind;
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
    if (kind === 'backup') {
      for (const field of ['source', 'username', 'domain']) add(`[data-setup-backup-${field}]`, 'backup_' + field, '');
      for (const net of ['nfs', 'smb']) {
        for (const field of ['source', 'mount', 'path']) add(`[data-setup-network-${field}="${net}"]`, '', '', {[`setupNetwork${field[0].toUpperCase() + field.slice(1)}`]: net});
      }
      for (const field of ['username', 'domain']) add(`[data-setup-smb-${field}]`, '', '', {[`setupSmb${field}`]: ''});
      add('[backup-keep]', 'backup_keep', '7');
      // This fixture also ensures future password inputs stay in memory only.
      add('[password]', 'backup_password', 'memory-only-secret').type = 'password';
    }
    const choices = devices.map(device => new Node({device, uuid: 'uuid-' + device, mountpoint: '/srv/' + device.split('/').pop()}));
    f.many[kind === 'backup' ? '[data-setup-backup-existing]' : '[data-setup-existing-partition]'] = choices;
    f.many['[data-setup-existing-partition], [data-setup-backup-existing]'] = choices;
    f.many['input, select, textarea'] = fields;
    f.many['[data-setup-storage-device], [data-setup-storage-mount], [data-setup-storage-path], [data-setup-backup-device], [data-setup-backup-mount], [data-setup-backup-path]'] = fields.filter(field => new RegExp('^' + prefix + '_(device|mount_point|path)$').test(field.name));
    f.many['[data-setup-disk]'] = [new Node({setupDisk: '/dev/vdc'})];
    const group = new Node({setupDiskPartitions: '/dev/vdc'}); group.many['[data-device]'] = choices;
    f.many['[data-setup-disk-partitions]'] = [group];
    const system = new Node({systemPath: '/var/lib/justvoxel/' + (kind === 'backup' ? 'backups' : 'minecraft'), setupBackupChoice: 'system'});
    if (kind === 'storage') f.one['[data-setup-storage-system]'] = system;
    else f.many['[data-setup-backup-choice]'] = [system, new Node({setupBackupChoice: 'partition'}), new Node({setupBackupChoice: 'nfs'}), new Node({setupBackupChoice: 'smb'})];
    return f;
  }
  current = makeForm(['/dev/vdb1']);
  let deny = false, status = {persistence: 'none', mounted: false};
  const context = {
    document, window: {}, URLSearchParams, console,
    sessionStorage: {getItem() { return null; }, removeItem() {}, setItem() { throw new Error('Secret or form state entered browser storage'); }},
    DOMParser: class {parseFromString() { return {querySelector: () => fresh}; }},
    async fetch(url, options = {}) {
      requests.push({url, body: options.body ? new URLSearchParams(options.body) : null});
      if (url === '/setup') { fresh = makeForm(['/dev/vdb1', '/dev/vdc1']); return {ok: true, text: async () => 'mocked wizard'}; }
      if (url.includes('/mounts/status')) return {ok: true, json: async () => ({ok: true, proposed: status})};
      if (deny) return {ok: false, json: async () => ({ok: false, error: 'Protected or unavailable'})};
      const values = Object.fromEntries(new URLSearchParams(options.body));
      return {ok: true, json: async () => ({ok: true, proposed: {...values, device: values.device, fingerprint: 'disk-fingerprint', confirmation: ['create_partition', 'format', 'delete_partition'].includes(values.operation) ? 'CONFIRM ' + values.device : '', planned_end: '20481MiB'}, warnings: ['fixture warning']})};
    },
  };
  vm.createContext(context);
  vm.runInContext(selectionSource, context);
  // Expose production functions to the harness without changing shipped code.
  vm.runInContext(actionSource.replace('window.JustVoxelSetupDiskActions = {open, init};', 'window.JustVoxelSetupDiskActions = {open, init, plan, apply, refreshWizardInventory, loadActions};'), context);
  return {context, api: context.window.JustVoxelSetupDiskActions, get, dialog, requests, form: () => current, deny(value) {deny = value;}, status(value) {status = value;}};
}
(async () => {
  for (const kind of ['storage', 'backup']) {
    const f = fixture(kind), prefix = kind === 'backup' ? 'backup' : 'storage';
    const field = name => f.form().one[`[data-setup-${prefix}-${name}]`];
    // Existing /dev/vdb1 is selected by the actual destination-selection handler.
    f.form().many[kind === 'backup' ? '[data-setup-backup-existing]' : '[data-setup-existing-partition]'][0].click();
    assert.equal(field('device').value, '/dev/vdb1');
    assert.equal(field('type').value, 'partition');
    if (kind === 'backup') {
      f.form().one['[data-setup-network-source="smb"]'].value = '//nas/unsaved';
      f.form().one['[data-setup-network-path="nfs"]'].value = '/var/mnt/custom/backups';
      f.form().one['[backup-keep]'].value = '19';
    }
    f.api.open({[kind === 'backup' ? 'setupBackupPrepare' : 'setupStoragePrepare']: 'create_partition', device: '/dev/vdc', freeStart: '1MiB'});
    assert.equal(f.dialog.openCount, 1);
    assert.equal(f.requests.length, 0); // No intermediary workspace or information dialog.
    f.get('all').checked = false; f.get('size').value = '20';
    await f.api.plan();
    let sent = f.requests.at(-1);
    assert.equal(sent.url, '/api/new-storage/actions/plan');
    assert.equal(sent.body.get('size_gib'), '20'); assert.equal(sent.body.get('free_start'), '1MiB');
    assert.equal(f.get('apply').disabled, true);
    await f.api.apply(); assert.equal(f.requests.length, 1); // Slider is required.
    f.get('slider').value = '100'; f.get('toggle').checked = true; f.get('toggle').events.change[0]();
    await f.api.apply();
    sent = f.requests.find(r => r.url.endsWith('/apply'));
    assert.equal(sent.body.get('fingerprint'), 'disk-fingerprint');
    assert.equal(sent.body.get('confirmation'), 'CONFIRM /dev/vdc');
    assert.equal(sent.body.get('size_gib'), '20');
    assert.equal(f.requests.at(-1).url, '/setup');
    assert.equal(f.dialog.open, false); assert.equal(field('device').value, '/dev/vdb1');
    assert.equal(f.form().many['[data-setup-disk]'][0].getAttribute('aria-pressed'), 'true');
    assert.equal(f.form().one['[csrf]'].value, 'new-csrf');
    if (kind === 'backup') {
      assert.equal(f.form().one['[data-setup-network-source="smb"]'].value, '//nas/unsaved');
      assert.equal(f.form().one['[data-setup-network-path="nfs"]'].value, '/var/mnt/custom/backups');
      assert.equal(f.form().one['[backup-keep]'].value, '19');
      assert.equal(f.form().one['[password]'].value, 'memory-only-secret');
    }
    f.form().many[kind === 'backup' ? '[data-setup-backup-existing]' : '[data-setup-existing-partition]'][1].click();
    assert.equal(field('device').value, '/dev/vdc1');
    assert(f.requests.every(r => !r.url.includes('/workspace/')));
    // Changing size invalidates review and requires a fresh plan.
    f.api.open({setupStoragePrepare: 'create_partition', device: '/dev/vdc', freeStart: '1MiB'});
    f.get('all').checked = false; f.get('size').value = '10'; await f.api.plan();
    f.get('size').value = '15'; f.get('size').events.input[0]();
    assert.equal(f.get('apply').disabled, true);
    f.dialog.close();
    // API rejection never enables apply.
    f.deny(true); f.api.open({setupStoragePrepare: 'create_partition', device: '/dev/protected', freeStart: '1MiB'});
    await f.api.plan(); assert.equal(f.get('apply').disabled, true); assert.equal(f.get('error').hidden, false);
    f.dialog.close(); f.deny(false);
    function actionGroup(protectedValue) {
      const group = new Node({device: '/dev/vdb1', filesystem: 'xfs', uuid: 'fixture-uuid', protected: protectedValue});
      group.one['[data-setup-partition-status]'] = new Node(); group.parentElement = new Node();
      for (const action of ['format', 'delete_partition', 'mount', 'persist']) { const n = new Node(); n.hidden = true; group.one[`[data-setup-disk-action="${action}"]`] = n; }
      return group;
    }
    let group = actionGroup('true'), count = f.requests.length;
    await f.api.loadActions(group); assert.equal(f.requests.length, count);
    assert(Object.values(group.one).filter(n => 'hidden' in n).slice(1).every(n => n.hidden));
    group = actionGroup('false'); await f.api.loadActions(group);
    for (const action of ['format', 'delete_partition', 'mount', 'persist']) assert.equal(group.one[`[data-setup-disk-action="${action}"]`].hidden, false);
    for (const operation of ['mount', 'persist']) {
      f.api.open({setupDiskAction: operation, device: '/dev/vdb1', mountpoint: '/srv/data'});
      await f.api.plan();
      assert.equal(f.get('apply').disabled, false);
      assert.equal(f.requests.at(-1).url, '/api/new-storage/' + (operation === 'persist' ? 'mounts' : 'actions') + '/plan');
      assert.equal(f.requests.at(-1).body.get('mount_point'), '/srv/data');
      await f.api.apply();
    }
    // The same device path with a different UUID must not silently stay selected.
    f.form().many[kind === 'backup' ? '[data-setup-backup-existing]' : '[data-setup-existing-partition]'][0].click();
    f.form().many[kind === 'backup' ? '[data-setup-backup-existing]' : '[data-setup-existing-partition]'][0].dataset.uuid = 'old-filesystem';
    await f.api.refreshWizardInventory();
    assert.equal(field('device').value, '');
    assert.equal(field('path').value, '');
    f.deny(true); group = actionGroup('false'); await f.api.loadActions(group);
    assert.equal(group.one['[data-setup-disk-action="format"]'].hidden, true);
    assert.equal(group.one['[data-setup-disk-action="delete_partition"]'].hidden, true);
  }
  console.log('Inline setup storage behavior checks passed.');
})().catch(error => { console.error(error); process.exitCode = 1; });
