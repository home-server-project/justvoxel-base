(() => {
  const formSelector = '[data-setup-storage-form], [data-setup-backup-form]';
  const form = () => document.querySelector(formSelector);
  const selectableChoices = root => [...root.querySelectorAll('[data-setup-existing-partition], [data-setup-backup-existing]')]
    .filter(button => button.dataset.system !== 'Yes' && button.dataset.readonly !== 'Yes');
  async function refreshWizardInventory(successMessage = '') {
    const response = await fetch('/setup', {credentials: 'same-origin', cache: 'no-store', headers: {Accept: 'text/html'}});
    if (!response.ok || response.redirected) throw new Error('Inventory could not be refreshed. Sign in again if your session expired.');
    const page = new DOMParser().parseFromString(await response.text(), 'text/html');
    const replacement = page.querySelector(formSelector), current = form();
    if (!current || !replacement || current.getAttribute('action') !== replacement.getAttribute('action')) throw new Error('Setup changed. Return to the current setup step.');
    // Snapshot after discovery to retain edits made during the request. No browser storage.
    const fields = (root) => [...root.querySelectorAll('input, select, textarea')].filter(field => !field.closest('dialog') && !field.matches('[data-storage-action-csrf]'));
    const key = (field) => field.name || JSON.stringify(field.dataset);
    const snapshot = () => fields(current).map(field => ({key: key(field), type: field.type, value: field.value, checked: field.checked}));
    let saved = snapshot();
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
    const oldMount = current.querySelector('[data-setup-storage-mount], [data-setup-backup-mount]')?.value;
    const oldChoice = selectableChoices(current).find(button => button.dataset.device === oldDevice);
    const nextChoice = selectableChoices(replacement).find(button => button.dataset.device === oldDevice);
    const oldDisk = oldChoice?.closest('[data-setup-disk-partitions]')?.dataset.setupDiskPartitions;
    const nextDisk = nextChoice?.closest('[data-setup-disk-partitions]')?.dataset.setupDiskPartitions;
    const diskIdentity = (root, disk) => [...root.querySelectorAll('[data-setup-disk]')].find(button => button.dataset.setupDisk === disk)?.dataset.diskIdentity;
    let available = Boolean(nextChoice && oldChoice?.dataset.uuid && oldChoice.dataset.uuid === nextChoice.dataset.uuid && oldDisk === nextDisk && diskIdentity(current, oldDisk) === diskIdentity(replacement, nextDisk));
    let selectionError = '';
    if (available) try {
      const response = await fetch('/api/new-storage/mounts/status?device=' + encodeURIComponent(oldDevice), {credentials: 'same-origin', cache: 'no-store', headers: {Accept: 'application/json'}});
      const payload = await response.json();
      if (!response.ok || !payload?.ok) {
        const conflict = /conflict|inconsistent|another filesystem/i.test(payload?.error || '');
        throw new Error(conflict ? 'Saved mount location conflicts with another filesystem. Choose another partition or ask your administrator for help.' : 'Partition cannot be selected safely. Refresh the drives before continuing.');
      }
      const status = payload.proposed;
      available = Boolean(status && status.device === oldDevice && status.uuid === nextChoice.dataset.uuid &&
        ['none', 'justvoxel', 'external'].includes(status.persistence) &&
        (status.current_mount_point || status.mount_point) === oldMount &&
        (!status.mounted || (status.current_mount_point && status.current_mount_point === nextChoice.dataset.mountpoint &&
          (status.persistence === 'none' || status.current_mount_point === status.mount_point))));
      if (available && (status.current_mount_point || status.mount_point)) {
        nextChoice.dataset.mountpoint = status.current_mount_point || status.mount_point;
      }
    } catch (error) {
      // Discovery succeeded: keep the fresh cards, but require a new verified choice.
      available = false;
      selectionError = error.message;
    }
    // System storage and other draft edits remain usable while verification waits.
    // Do not overwrite a newer explicit choice with the earlier snapshot.
    const latestDevice = current.querySelector('[data-setup-storage-device], [data-setup-backup-device]')?.value;
    const latestMount = current.querySelector('[data-setup-storage-mount], [data-setup-backup-mount]')?.value;
    available = available && latestDevice === oldDevice && latestMount === oldMount;
    saved = snapshot();
    restore();
    const selectedDisk = current.querySelector('[data-setup-disk][aria-pressed="true"]')?.dataset.setupDisk;
    const lostSelection = latestDevice && !available;
    if (lostSelection) {
      replacement.querySelectorAll('[data-setup-storage-device], [data-setup-storage-mount], [data-setup-storage-path], [data-setup-backup-device], [data-setup-backup-mount], [data-setup-backup-path]').forEach(field => { field.value = ''; });
    }
    current.replaceWith(replacement);
    [...replacement.querySelectorAll('[data-setup-disk]')].forEach(button => button.setAttribute('aria-pressed', String(button.dataset.setupDisk === selectedDisk)));
    window.JustVoxelSetupStorage.init(replacement.parentElement);
    restore(true);
    const notice = document.querySelector('[data-setup-inventory-error]');
    if (notice) {
      notice.hidden = !lostSelection && !successMessage;
      notice.className = lostSelection ? 'notice error' : 'notice success';
      notice.setAttribute('role', lostSelection ? 'alert' : 'status');
      notice.textContent = lostSelection ? [successMessage, selectionError, 'The previous partition selection was cleared. Choose another partition or system storage, or open the partition details to try verification again.'].filter(Boolean).join(' ') : successMessage;
    }
    [...replacement.querySelectorAll('[data-setup-disk]')].find(button => button.dataset.setupDisk === selectedDisk)?.click();
  }
  function init(root = document) {
    root.querySelectorAll(formSelector).forEach(current => {
      if (current.dataset.setupOperationsInitialized) return;
      current.dataset.setupOperationsInitialized = 'true';
      current.addEventListener('submit', event => {
        if (event.submitter?.value === 'back' || event.submitter?.getAttribute('formaction') === '/setup/cancel') return;
        if (current.dataset.inventoryStale === 'true' && current.querySelector('[data-setup-storage-type], [data-setup-backup-type]')?.value === 'partition') event.preventDefault();
      });
      const choices = () => selectableChoices(current);
      const canSelect = (path, status) => {
        if (current.dataset.inventoryStale === 'true') return false;
        const choice = choices().find(button => button.dataset.device === path);
        if (!choice) return false;
        if (!status) return false;
        if (status.device !== path || !status.uuid || status.uuid !== choice.dataset.uuid) return false;
        if (!['none', 'justvoxel', 'external'].includes(status.persistence)) return false;
        if (status.mounted && choice.dataset.mountpoints && status.current_mount_point !== choice.dataset.mountpoints) return false;
        if (status.mounted && (!status.current_mount_point ||
          (status.persistence !== 'none' && status.current_mount_point !== status.mount_point))) return false;
        return true;
      };
      window.JustVoxelStorageBrowser.init(current, {
        inlineDetails: false,
        canSelect,
        onSelect(path, status) {
          const choice = choices().find(button => button.dataset.device === path);
          if (!choice || !canSelect(path, status)) return;
          const knownMount = status?.current_mount_point || status?.mount_point;
          if (knownMount && ['none', 'justvoxel', 'external'].includes(status.persistence)) choice.dataset.mountpoint = knownMount;
          current.dispatchEvent(new CustomEvent('setup-destination-select', {detail: path}));
        },
        async onChanged(successMessage = 'Storage updated.') {
          // Invalidate old inventory immediately without blocking Back, Cancel or system storage.
          current.dataset.inventoryStale = 'true';
          current.querySelectorAll('[data-storage-partition], [data-storage-free-space]').forEach(button => { button.disabled = true; });
          const retry = document.createElement('button');
          retry.type = 'button'; retry.className = 'secondary'; retry.textContent = 'Refresh inventory';
          const notice = current.querySelector('[data-setup-inventory-error]');
          const message = document.createElement('p');
          notice.replaceChildren(message, retry); notice.hidden = false; notice.className = 'notice error'; notice.setAttribute('role', 'alert');
          message.textContent = successMessage + ' Refreshing drives…';
          async function refresh() {
            retry.disabled = true;
            try { await refreshWizardInventory(successMessage); }
            catch (error) {
              if (current.querySelector('[data-setup-storage-type], [data-setup-backup-type]')?.value === 'partition') {
                current.querySelectorAll('[data-setup-storage-device], [data-setup-storage-mount], [data-setup-storage-path], [data-setup-backup-device], [data-setup-backup-mount], [data-setup-backup-path]').forEach(field => { field.value = ''; });
              }
              message.textContent = successMessage + ' ' + error.message + ' Partition choices are unavailable until drives can be refreshed. Retry, choose system storage, go Back, or cancel setup.';
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
