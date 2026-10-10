(() => {
  const dialog = document.querySelector('[data-setup-disk-review]');
  if (!dialog) return;
  const q = (name) => dialog.querySelector('[data-setup-disk-' + name + ']');
  const title = q('title'), target = q('target'), result = q('result'), warnings = q('warnings'), error = q('error');
  const size = q('size'), all = q('all'), mount = q('mount');
  const slider = q('slider'), toggle = q('toggle'), confirm = q('confirm');
  const planButton = q('plan'), applyButton = q('apply'), close = q('close');
  const formSelector = '[data-setup-storage-form], [data-setup-backup-form]';
  let request, reviewed, busy = false, applied = false, generation = 0;
  const labels = {create_partition: 'Create partition', format: 'Format partition', delete_partition: 'Delete partition', mount: 'Mount now', persist: 'Configure persistent mount', inspect: 'Partition details'};
  const form = () => document.querySelector(formSelector);

  async function json(url, options = {}) {
    const response = await fetch(url, {credentials: 'same-origin', cache: 'no-store', ...options});
    const payload = await response.json().catch(() => null);
    if (!response.ok || !payload?.ok) throw new Error(payload?.error || 'Storage information or action is unavailable.');
    return payload;
  }
  async function post(phase, values) {
    const body = new URLSearchParams({csrf: form()?.dataset.csrf || '', ...values});
    const family = values.operation === 'persist' ? 'mounts' : 'actions';
    return json('/api/new-storage/' + family + '/' + phase, {
      method: 'POST', headers: {'Content-Type': 'application/x-www-form-urlencoded;charset=UTF-8', Accept: 'application/json'}, body,
    });
  }
  function invalidate() {
    reviewed = null;
    generation++;
    applyButton.hidden = true;
    applyButton.disabled = true;
    slider.value = '0'; toggle.checked = false; toggle.disabled = true;
    q('slider-shell').style.setProperty('--confirm-progress', '0');
    q('slider-shell').classList.toggle('is-armed', false);
    q('toggle-row').hidden = true;
    confirm.hidden = true;
    result.textContent = ''; warnings.replaceChildren();
    planButton.hidden = false;
  }
  function updateConfirmation() {
    const armed = Number(slider.value) >= 100;
    q('slider-shell').style.setProperty('--confirm-progress', String(Number(slider.value) / 100));
    q('slider-shell').classList.toggle('is-armed', armed);
    q('toggle-row').hidden = !armed;
    toggle.disabled = !armed;
    if (!armed) toggle.checked = false;
    applyButton.disabled = busy || !reviewed || (Boolean(reviewed.confirmation) && !(armed && toggle.checked));
  }
  function values() {
    const data = {...request};
    if (data.operation === 'create_partition') {
      const value = Number(size.value);
      if (!all.checked && (!Number.isInteger(value) || value < 1)) throw new Error('Choose a whole number of GiB, at least 1.');
      data.size_gib = all.checked ? 'all' : String(value);
    }
    if (data.operation === 'mount' || data.operation === 'persist') data.mount_point = mount.value.trim();
    return data;
  }
  async function plan() {
    if (busy || !request || request.operation === 'inspect') return;
    if (applied) { await refreshAfterApply(); return; }
    invalidate(); error.hidden = true;
    const ticket = generation;
    busy = true; planButton.disabled = true;
    try {
      const sent = values();
      const payload = await post('plan', sent);
      if (ticket !== generation) return;
      if (!payload.proposed?.fingerprint) throw new Error('Storage review did not return a fingerprint.');
      reviewed = {...payload.proposed, request: sent};
      result.textContent = reviewed.operation === 'create_partition'
        ? `Create XFS partition: ${reviewed.size_gib === 'all' ? 'all safely usable space' : reviewed.size_gib + ' GiB'} · ${reviewed.free_start} → ${reviewed.planned_end}`
        : `${labels[sent.operation]} · ${reviewed.device || sent.device}${reviewed.mount_point ? ' · ' + reviewed.mount_point : ''}`;
      (payload.warnings || []).forEach((text) => {
        const notice = document.createElement('p'); notice.className = 'notice warning compact'; notice.textContent = text; warnings.append(notice);
      });
      confirm.hidden = !reviewed.confirmation;
      q('confirm-summary').textContent = reviewed.confirmation ? `${reviewed.device || sent.device}: ${labels[sent.operation]}. ${sent.operation === 'create_partition' ? 'Creates and formats a new partition in the reviewed unallocated space.' : 'This action destroys data.'}` : '';
      planButton.hidden = true; applyButton.hidden = false;
    } catch (e) { error.textContent = e.message; error.hidden = false; }
    finally { busy = false; planButton.disabled = false; updateConfirmation(); }
  }
  async function refreshWizardInventory() {
    const response = await fetch('/setup', {credentials: 'same-origin', cache: 'no-store', headers: {Accept: 'text/html'}});
    if (!response.ok || response.redirected) throw new Error('Inventory could not be refreshed. Sign in again if your session expired.');
    const page = new DOMParser().parseFromString(await response.text(), 'text/html');
    const replacement = page.querySelector(formSelector), current = form();
    if (!current || !replacement || current.getAttribute('action') !== replacement.getAttribute('action')) throw new Error('Setup changed. Return to the current setup step.');
    // Snapshot after discovery to retain edits made during the request. No browser storage.
    const fields = (root) => [...root.querySelectorAll('input, select, textarea')];
    const key = (field) => field.name || JSON.stringify(field.dataset);
    const saved = fields(current).map(field => ({key: key(field), type: field.type, value: field.value, checked: field.checked}));
    const selectedDisk = current.querySelector('[data-setup-disk][aria-pressed="true"]')?.dataset.setupDisk;
    const restore = (afterInit = false) => {
      const used = new Set();
      saved.forEach(value => {
        if (value.key === 'csrf' || (afterInit && /^(storage|backup)_(device|mount_point|path)$/.test(value.key) && replacement.querySelector('[data-setup-storage-type], [data-setup-backup-type]')?.value === 'partition')) return;
        const field = fields(replacement).find(field => !used.has(field) && key(field) === value.key && field.type === value.type);
        if (!field) return;
        used.add(field); field.value = value.value;
        if (value.type === 'checkbox' || value.type === 'radio') field.checked = value.checked;
      });
    };
    restore();
    const oldDevice = current.querySelector('[data-setup-storage-device], [data-setup-backup-device]')?.value;
    const choices = '[data-setup-existing-partition], [data-setup-backup-existing]';
    const oldChoice = [...current.querySelectorAll(choices)].find(button => button.dataset.device === oldDevice);
    const nextChoice = [...replacement.querySelectorAll(choices)].find(button => button.dataset.device === oldDevice);
    const available = Boolean(nextChoice && oldChoice?.dataset.uuid === nextChoice.dataset.uuid);
    if (oldDevice && !available) {
      replacement.querySelectorAll('[data-setup-storage-device], [data-setup-storage-mount], [data-setup-storage-path], [data-setup-backup-device], [data-setup-backup-mount], [data-setup-backup-path]').forEach(field => { field.value = ''; });
    }
    current.replaceWith(replacement);
    window.JustVoxelSetupStorage.init(replacement.parentElement);
    restore(true);
    const notice = document.querySelector('[data-setup-inventory-error]');
    if (notice) {
      notice.hidden = !oldDevice || available;
      notice.textContent = 'The previously selected partition is no longer available or its identity changed. Choose a destination again.';
    }
    [...replacement.querySelectorAll('[data-setup-disk]')].find(button => button.dataset.setupDisk === selectedDisk)?.click();
  }
  async function refreshAfterApply() {
    busy = true; planButton.disabled = true; close.disabled = true;
    try { await refreshWizardInventory(); dialog.close(); }
    catch (e) {
      error.textContent = 'The action completed. ' + e.message; error.hidden = false;
      planButton.hidden = false; planButton.textContent = 'Refresh inventory';
      // Block stale cards and Continue until a successful inventory refresh.
      if (form()) form().inert = true;
    } finally { busy = false; planButton.disabled = false; close.disabled = Boolean(form()?.inert); }
  }
  async function apply() {
    if (busy || !reviewed || applyButton.disabled) return;
    if (reviewed.confirmation && (Number(slider.value) < 100 || !toggle.checked)) return;
    busy = true; close.disabled = true; applyButton.disabled = true; error.hidden = true;
    try {
      await post('apply', {...reviewed.request, fingerprint: reviewed.fingerprint, confirmation: reviewed.confirmation || ''});
      applied = true; reviewed = null; applyButton.hidden = true;
    } catch (e) { error.textContent = e.message; error.hidden = false; }
    finally { busy = false; close.disabled = false; updateConfirmation(); }
    if (applied) await refreshAfterApply();
  }
  function open(data) {
    if (busy || dialog.open) return;
    applied = false; invalidate(); error.hidden = true; planButton.textContent = 'Review action';
    const operation = data.setupStoragePrepare || data.setupBackupPrepare || data.setupDiskAction;
    request = {operation, device: data.device, ...(data.freeStart ? {free_start: data.freeStart} : {})};
    title.textContent = labels[operation]; target.textContent = `${data.device}${data.size ? ' · ' + data.size : ''}`;
    q('size-field').hidden = q('all-field').hidden = operation !== 'create_partition';
    q('mount-field').hidden = !['mount', 'persist'].includes(operation);
    all.checked = true; size.value = ''; size.disabled = true;
    mount.readOnly = data.savedMount === 'true';
    mount.value = data.mountpoint || (form()?.matches('[data-setup-backup-form]') ? '/var/mnt/justvoxel-backup' : '/var/mnt/justvoxel-data');
    if (operation === 'inspect') {
      result.textContent = `Filesystem: ${data.filesystem || 'Not formatted'} · UUID: ${data.uuid || 'None'} · Label: ${data.label || 'None'} · Mount: ${data.mountpoint || 'Not mounted'}${data.protected === 'true' ? ' · Protected partition' : ''}`;
      planButton.hidden = true;
    }
    dialog.showModal();
    if (['format', 'delete_partition'].includes(operation)) void plan();
  }
  async function loadActions(group) {
    const data = group.dataset;
    const statusText = group.querySelector('[data-setup-partition-status]');
    if (data.protected === 'true') { statusText.textContent = 'Protected partition: management actions unavailable.'; return; }
    const show = (action, visible) => { const button = group.querySelector('[data-setup-disk-action="' + action + '"]'); if (button) button.hidden = !visible; };
    // Use real plans to determine destructive eligibility. Re-plan on click and apply.
    await Promise.all(['format', 'delete_partition'].map(async operation => {
      try {
        await post('plan', {operation, device: data.device});
        const blank = group.parentElement.querySelector('[data-setup-storage-prepare="format"], [data-setup-backup-prepare="format"]');
        show(operation, operation !== 'format' || !blank);
        if (operation === 'format' && blank) blank.disabled = false;
      } catch (e) {
        show(operation, false);
        statusText.textContent = e.message;
      }
    }));
    if (!data.filesystem || !data.uuid) return;
    try {
      const payload = await json('/api/new-storage/mounts/status?device=' + encodeURIComponent(data.device));
      const status = payload.proposed;
      if (!status) throw new Error('Mount status is unavailable.');
      const knownMount = status.current_mount_point || status.mount_point;
      if (knownMount && ['none', 'justvoxel', 'external'].includes(status.persistence)) {
        data.mountpoint = knownMount;
        const choice = [...(form()?.querySelectorAll('[data-setup-existing-partition], [data-setup-backup-existing]') || [])].find(button => button.dataset.device === data.device && button.dataset.uuid === data.uuid);
        if (choice) {
          choice.dataset.mountpoint = knownMount;
          if (form()?.querySelector('[data-setup-storage-device], [data-setup-backup-device]')?.value === data.device) choice.click();
        }
      }
      if (status.persistence === 'none') {
        show('mount', !status.mounted); show('persist', true);
        if (status.mounted) data.mountpoint = status.current_mount_point || data.mountpoint;
      } else if (status.persistence === 'external' && !status.mounted && status.mount_point) {
        // Mount at its saved location without rewriting externally owned fstab.
        data.mountpoint = status.mount_point; data.savedMount = 'true'; show('mount', true);
      } else if (status.persistence === 'justvoxel' && !status.mounted && status.mount_point) {
        // Persist revalidates the saved identity and mounts at the saved location.
        data.mountpoint = status.mount_point; data.savedMount = 'true'; show('persist', true);
        const button = group.querySelector('[data-setup-disk-action="persist"]'); button.textContent = 'Mount now';
      }
    } catch (e) { statusText.textContent = e.message; }
  }
  function init(root = document) {
    root.querySelectorAll('[data-setup-partition-actions]').forEach(group => {
      if (group.dataset.initialized) return;
      group.dataset.initialized = 'true';
      group.querySelectorAll('[data-setup-disk-action]').forEach(button => button.addEventListener('click', () => open({...group.dataset, setupDiskAction: button.dataset.setupDiskAction})));
      void loadActions(group);
    });
  }
  all.addEventListener('change', () => { size.disabled = all.checked; invalidate(); });
  size.addEventListener('input', invalidate); mount.addEventListener('input', invalidate);
  slider.addEventListener('input', updateConfirmation); toggle.addEventListener('change', updateConfirmation);
  planButton.addEventListener('click', () => void plan()); applyButton.addEventListener('click', () => void apply());
  close.addEventListener('click', () => { if (!busy && !form()?.inert) dialog.close(); });
  dialog.addEventListener('cancel', event => { if (busy || form()?.inert) event.preventDefault(); });
  document.addEventListener('submit', event => { if (dialog.open && event.target.matches(formSelector)) event.preventDefault(); });
  window.JustVoxelSetupDiskActions = {open, init};
  init();
})();
