(() => {
  const formSelector = '[data-setup-storage-form], [data-setup-backup-form]';
  const form = () => document.querySelector(formSelector);
  const selectableChoices = root => [...root.querySelectorAll('[data-setup-existing-partition], [data-setup-backup-existing]')]
    .filter(button => button.dataset.system !== 'Yes' && button.dataset.readonly !== 'Yes');
  async function refreshWizardInventory() {
    const response = await fetch('/setup', {credentials: 'same-origin', cache: 'no-store', headers: {Accept: 'text/html'}});
    if (!response.ok || response.redirected) throw new Error('Inventory could not be refreshed. Sign in again if your session expired.');
    const page = new DOMParser().parseFromString(await response.text(), 'text/html');
    const replacement = page.querySelector(formSelector), current = form();
    if (!current || !replacement || current.getAttribute('action') !== replacement.getAttribute('action')) throw new Error('Setup changed. Return to the current setup step.');
    // Snapshot after discovery to retain edits made during the request. No browser storage.
    const fields = (root) => [...root.querySelectorAll('input, select, textarea')].filter(field => !field.closest('dialog') && !field.matches('[data-storage-action-csrf]'));
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
    const oldChoice = selectableChoices(current).find(button => button.dataset.device === oldDevice);
    const nextChoice = selectableChoices(replacement).find(button => button.dataset.device === oldDevice);
    const oldDisk = oldChoice?.closest('[data-setup-disk-partitions]')?.dataset.setupDiskPartitions;
    const nextDisk = nextChoice?.closest('[data-setup-disk-partitions]')?.dataset.setupDiskPartitions;
    const diskIdentity = (root, disk) => [...root.querySelectorAll('[data-setup-disk]')].find(button => button.dataset.setupDisk === disk)?.dataset.diskIdentity;
    const available = Boolean(nextChoice && oldChoice?.dataset.uuid && oldChoice.dataset.uuid === nextChoice.dataset.uuid && oldDisk === nextDisk && diskIdentity(current, oldDisk) === diskIdentity(replacement, nextDisk));
    if (oldDevice && !available) {
      replacement.querySelectorAll('[data-setup-storage-device], [data-setup-storage-mount], [data-setup-storage-path], [data-setup-backup-device], [data-setup-backup-mount], [data-setup-backup-path]').forEach(field => { field.value = ''; });
    }
    current.replaceWith(replacement);
    [...replacement.querySelectorAll('[data-setup-disk]')].forEach(button => button.setAttribute('aria-pressed', String(button.dataset.setupDisk === selectedDisk)));
    window.JustVoxelSetupStorage.init(replacement.parentElement);
    restore(true);
    const notice = document.querySelector('[data-setup-inventory-error]');
    if (notice) {
      notice.hidden = !oldDevice || available;
      notice.textContent = 'The previously selected partition is no longer available or its identity changed. Choose a destination again.';
    }
    [...replacement.querySelectorAll('[data-setup-disk]')].find(button => button.dataset.setupDisk === selectedDisk)?.click();
  }
  function init(root = document) {
    root.querySelectorAll(formSelector).forEach(current => {
      if (current.dataset.setupOperationsInitialized) return;
      current.dataset.setupOperationsInitialized = 'true';
      const choices = () => selectableChoices(current);
      const canSelect = (path, status) => {
        const choice = choices().find(button => button.dataset.device === path);
        if (!choice) return false;
        if (!status) return choice.dataset.mounted !== 'Yes';
        if (status.device !== path || !status.uuid || status.uuid !== choice.dataset.uuid) return false;
        if (!['none', 'justvoxel', 'external'].includes(status.persistence)) return false;
        if (status.mounted && (!status.current_mount_point ||
          (status.persistence !== 'none' && status.current_mount_point !== status.mount_point))) return false;
        return true;
      };
      window.JustVoxelStorageBrowser.init(current, {
        inlineDetails: true,
        canSelect,
        onSelect(path, status) {
          const choice = choices().find(button => button.dataset.device === path);
          if (!choice || !canSelect(path, status)) return;
          const knownMount = status?.current_mount_point || status?.mount_point;
          if (knownMount && ['none', 'justvoxel', 'external'].includes(status.persistence)) choice.dataset.mountpoint = knownMount;
          current.dispatchEvent(new CustomEvent('setup-destination-select', {detail: path}));
        },
        async onChanged() {
          // Successful operations invalidate all old cards immediately, including Continue.
          current.inert = true;
          const retry = document.createElement('button');
          retry.type = 'button'; retry.className = 'secondary'; retry.textContent = 'Refresh inventory';
          const notice = document.createElement('div'); notice.className = 'notice error'; notice.setAttribute('role', 'alert');
          const message = document.createElement('p');
          notice.append(message, retry); current.after(notice); notice.hidden = true;
          async function refresh() {
            retry.disabled = true;
            try { await refreshWizardInventory(); notice.remove(); }
            catch (error) {
              message.textContent = 'The action completed. ' + error.message;
              notice.hidden = false;
            } finally { retry.disabled = false; }
          }
          retry.addEventListener('click', () => void refresh());
          await refresh();
        },
      });
    });
  }
  window.JustVoxelSetupDiskActions = {init};
})();
