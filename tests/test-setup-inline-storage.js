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
    this.classList = {toggle() {}, remove() {}}; this.style = {setProperty() {}};
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
  scrollIntoView() {} focus() {} remove() {}
  after(node) {this.following = node;}
  closest(s) { return s === '[data-setup-disk-partitions]' ? this.diskGroup : null; }
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
function fixture(kind, controlCenter = false) {
  let current, fresh;
  const requests = [];
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
      const container = new Node(); container.hidden = true;
      container.tag = controlCenter || name.includes('review') ? 'dialog' : 'section';
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
    return f;
  }
  current = makeForm(['/dev/vdb1']);
  let deny = false, refreshFails = false, status = {device: '/dev/vdb1', uuid: 'uuid-/dev/vdb1', persistence: 'none', mounted: false};
  const context = {
    document, window: {}, URLSearchParams, console,
    CustomEvent: class {constructor(type, options) {this.type = type; this.detail = options.detail;}},
    sessionStorage: {getItem() { return null; }, removeItem() {}, setItem() { throw new Error('Secret or form state entered browser storage'); }},
    DOMParser: class {parseFromString() { return {querySelector: () => fresh}; }},
    async fetch(url, options = {}) {
      requests.push({url, body: options.body ? new URLSearchParams(options.body) : null});
      if (url === '/setup') { if (refreshFails) return {ok: false}; fresh = makeForm(['/dev/vdb1', '/dev/vdc1']); if (status.mounted) fresh.many['[data-storage-partition]'][0].dataset.mountpoint = status.current_mount_point; return {ok: true, text: async () => 'mocked wizard'}; }
      if (url.includes('/mounts/status')) return {ok: true, json: async () => ({ok: true, proposed: status})};
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
  return {context, get, requests, refreshFailure(value) {refreshFails = value;}, form: () => current, deny(value) {deny = value;}, status(value) {status = {device: "/dev/vdb1", uuid: "uuid-/dev/vdb1", ...value};}, changedUUID() {current.many['[data-setup-existing-partition], [data-setup-backup-existing]'][0].dataset.uuid = 'old-uuid';}, diskChanged() {current.many['[data-setup-disk]'][0].dataset.diskIdentity = 'old-disk';}};
}
const settle = async () => { for (let i = 0; i < 15; i++) await Promise.resolve(); };
(async () => {
  // A formatted card with stale selection markup must still be blocked by the adapter.
  for (const kind of ['storage', 'backup']) {
    for (const protection of ['system', 'readonly']) {
      const f = fixture(kind);
      const card = f.form().many['[data-storage-partition]'][0];
      const choice = f.get('action-menu').one['.storage-action-menu-popover'].children[0];
      const device = f.form().one[`[data-setup-${kind}-device]`];
      let selections = 0;
      f.form().addEventListener('setup-destination-select', () => { selections++; });
      card.dataset[protection] = 'Yes';
      card.click(); await settle();
      assert.equal(f.get('detail-dialog').hidden, false);
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
    card.click(); await settle();
    const choose = f.get('action-menu').one['.storage-action-menu-popover'].children[0];
    assert.equal(choose.hidden, false); choose.click();
    assert.equal(f.form().one[`[data-setup-${prefix}-mount]`].value, '/var/mnt/vdb1');
    for (const status of [
      {persistence: 'conflict', mounted: true},
      {persistence: 'justvoxel', mounted: true, current_mount_point: '/srv/changed', mount_point: '/srv/saved'},
      {persistence: 'justvoxel', mounted: true, uuid: 'changed-uuid', current_mount_point: '/var/mnt/vdb1', mount_point: '/var/mnt/vdb1'},
    ]) {
      f.status(status); card.click(); await settle(); assert.equal(choose.hidden, true);
      choose.click(); assert.equal(f.form().one[`[data-setup-${prefix}-mount]`].value, '/var/mnt/vdb1');
    }
  }
  for (const kind of ['storage', 'backup']) {
    const f = fixture(kind), prefix = kind === 'backup' ? 'backup' : 'storage';
    const field = name => f.form().one[`[data-setup-${prefix}-${name}]`];
    const choose = async () => {
      f.form().many['[data-storage-partition]'][0].click(); await settle();
      const card = f.get('detail-dialog');
      assert.equal(card.hidden, false); assert(!card.open);
      assert.equal(card.openCount || 0, 0);
      assert.equal(f.form().one['[data-detail-path]'].textContent, '/dev/vdb1');
      assert.equal(f.form().one['[data-detail-filesystem]'].textContent, 'XFS');
      assert(!f.get('action-dialog').open && !f.get('create-dialog').open);
      f.get('detail-close').click();
      assert.equal(card.hidden, true);
      f.form().many['[data-storage-partition]'][0].click(); await settle();
      assert.equal(card.hidden, false);
      f.get('action-menu').one['.storage-action-menu-popover'].children[0].click();
      assert.equal(card.hidden, true);
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
    assert.equal(f.get('free-detail-dialog').hidden, false);
    assert.equal(f.get('detail-dialog').hidden, true);
    f.get('create-partition').click();
    assert.equal(f.get('free-detail-dialog').hidden, true);
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
    await f.get('create-apply').emit('click'); await settle();
    sent = f.requests.find(r => r.url.endsWith('/apply'));
    assert.equal(sent.body.get('fingerprint'), 'disk-fingerprint');
    assert.equal(sent.body.get('confirmation'), 'CONFIRM /dev/vdc');
    assert.equal(sent.body.get('size_gib'), '20');
    assert.equal(oldForm.inert, true);
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
      assert.equal(f.get('detail-dialog').hidden, true);
      assert.equal(f.get('action-dialog').open, true);
      await f.get('action-review-button').emit('click');
      assert.equal(f.get('action-apply-button').disabled, true);
      f.get('confirm-slider').value = '100'; await f.get('confirm-slider').emit('input');
      f.get('confirm-toggle').checked = true; await f.get('confirm-toggle').emit('change');
      await f.get('action-apply-button').emit('click'); await settle();
      assert.equal(f.requests.filter(r => r.url.endsWith('/apply')).at(-1).body.get('fingerprint'), 'disk-fingerprint');
    }
    f.form().many['[data-storage-partition]'][0].click(); await settle();
    f.form().many['[data-storage-action]'].find(n => n.dataset.storageAction === 'mount-permanently').click();
    f.get('mount-input').value = '/srv/custom';
    await f.get('action-review-button').emit('click');
    assert.equal(f.requests.at(-1).url, '/api/new-storage/mounts/plan');
    assert.equal(f.get('action-apply-button').disabled, false);
    await f.get('action-apply-button').emit('click'); await settle();
    const mounted = f.requests.filter(r => r.url.endsWith('/apply')).at(-1);
    assert.equal(mounted.url, '/api/new-storage/mounts/apply');
    assert.equal(mounted.body.get('mount_point'), '/srv/custom');
    assert.equal(mounted.body.get('fingerprint'), 'disk-fingerprint');
    assert.equal(field('mount').value, '/srv/custom');
    assert.equal(field('path').value, '/srv/custom/' + (kind === 'backup' ? 'backups' : 'minecraft'));
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
    assert.equal(f.form(), stale); assert.equal(stale.inert, true);
    assert.equal(stale.following.hidden, false);
    assert(stale.following.children[0].textContent.startsWith('The action completed.'));
    f.refreshFailure(false);
    stale.following.children[1].click(); await settle();
    assert.notEqual(f.form(), stale);
    f.form().many['[data-storage-free-space]'][0].click(); f.get('create-partition').click();
    f.get('create-all').checked = true; f.deny(true);
    await f.get('create-review').emit('click');
    assert.equal(f.get('create-apply').hidden, true);
    assert.equal(f.get('create-error').hidden, false);
    assert(f.requests.every(r => !r.url.includes('/workspace/')));
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
})().catch(error => { console.error(error); process.exitCode = 1; });
