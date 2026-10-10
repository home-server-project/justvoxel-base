(() => {
  const dialog = document.querySelector('[data-setup-storage-manager]');
  if (!dialog) return;
  const content = dialog.querySelector('[data-storage-workspace-content]');
  const error = dialog.querySelector('[data-setup-storage-manager-error]');
  const refresh = dialog.querySelector('[data-storage-refresh]');
  const close = dialog.querySelector('[data-setup-storage-manager-close]');
  let loading = false;
  let inventoryStale = false;
  const formSelector = '[data-setup-storage-form], [data-setup-backup-form]';
  const choiceSelector = '[data-setup-existing-partition], [data-setup-backup-existing]';
  const form = () => document.querySelector(formSelector);
  const choice = (device) => [...(form()?.querySelectorAll(choiceSelector) || [])]
    .find((button) => button.dataset.device === device);

  async function readMarkup(url) {
    const response = await fetch(url, { credentials: 'same-origin', cache: 'no-store', headers: { Accept: 'text/html' } });
    if (response.redirected) {
      const path = new URL(response.url).pathname;
      if (path === '/login' || path === '/password') window.location.assign(path);
      throw new Error('Storage is unavailable. Sign in again if your session has expired.');
    }
    if (response.status === 403) throw new Error('Administrator access required.');
    if (!response.ok) throw new Error('Storage discovery is unavailable. Refresh to try again.');
    return response.text();
  }

  async function refreshWizardInventory() {
    const markup = await readMarkup('/setup');
    const page = new DOMParser().parseFromString(markup, 'text/html');
    const replacement = page.querySelector(formSelector);
    const current = form();
    if (!current || !replacement || current.getAttribute('action') !== replacement.getAttribute('action')) throw new Error('Setup changed. Close Storage and return to the current setup step.');
    // Take the snapshot after the request: retain edits made while discovery was running.
    // Passwords stay in this in-memory form snapshot and never enter browser storage.
    const fields = [...current.querySelectorAll('input[name], select[name], textarea[name]')];
    const values = fields.map((field) => ({ name: field.name, value: field.value, checked: field.checked, type: field.type }));
    const selectedDisk = current.querySelector('[data-setup-disk][aria-pressed="true"]')?.dataset.setupDisk;
    const nextFields = [...replacement.querySelectorAll('input[name], select[name], textarea[name]')];
    const localSelection = values.some((saved) => ['storage_type', 'backup_type'].includes(saved.name) && saved.value === 'partition');
    const restoreValues = (afterInit = false) => {
      const used = new Set();
      values.forEach((saved) => {
        if (saved.name === 'csrf' || (afterInit && localSelection && /^(storage|backup)_(device|mount_point|path)$/.test(saved.name))) return;
        const field = nextFields.find((candidate) => !used.has(candidate) && candidate.name === saved.name && candidate.type === saved.type);
        if (!field) return;
        used.add(field);
        field.value = saved.value;
        if (saved.type === 'checkbox' || saved.type === 'radio') field.checked = saved.checked;
      });
    };
    restoreValues();
    const selectedDevice = replacement.querySelector('[data-setup-storage-device], [data-setup-backup-device]')?.value;
    const stillEligible = [...replacement.querySelectorAll(choiceSelector)].some((button) => button.dataset.device === selectedDevice);
    current.replaceWith(replacement);
    window.JustVoxelSetupStorage.init(replacement.parentElement);
    // Local mount paths follow fresh discovery; preserve entered network paths and other values.
    restoreValues(true);
    if (selectedDevice && !stillEligible) {
      replacement.querySelectorAll('[data-setup-storage-device], [data-setup-storage-mount], [data-setup-storage-path], [data-setup-backup-device], [data-setup-backup-mount], [data-setup-backup-path]')
        .forEach((field) => { field.value = ''; });
      error.textContent = 'The previously selected partition is no longer available. Choose a destination again.';
      error.hidden = false;
    }
    const disk = [...replacement.querySelectorAll('[data-setup-disk]')].find((button) => button.dataset.setupDisk === selectedDisk);
    disk?.click();
  }

  async function load(target, refreshInventory = false) {
    if (loading) return;
    loading = true;
    refresh.disabled = true;
    close.disabled = true;
    error.hidden = true;
    // Disable the old inventory while loading so stale targets cannot be opened.
    content.inert = true;
    try {
      if (refreshInventory || inventoryStale) await refreshWizardInventory();
      const markup = await readMarkup('/workspace/storage');
      content.innerHTML = markup;
      const root = content.querySelector('[data-storage-browser-root]');
      if (!root || !window.JustVoxelStorageBrowser?.init) throw new Error('Storage browser could not be initialized.');
      window.JustVoxelStorageBrowser.init(root, {
        canSelect: (device) => !inventoryStale && Boolean(choice(device)),
        onSelect: (device) => {
          const button = choice(device);
          if (!button || inventoryStale) return;
          button.click();
          dialog.close();
        },
        onChanged: () => {
          inventoryStale = true;
          void load(null, true);
        },
      });
      inventoryStale = false;
      content.inert = false;
      if (target) {
        const partition = [...root.querySelectorAll('[data-storage-partition]')].find((button) => button.dataset.path === target.device);
        const free = [...root.querySelectorAll('[data-storage-free-space]')].find((button) => button.dataset.device === target.device && button.dataset.start === target.freeStart);
        const selected = partition || free;
        const group = selected?.closest('[data-storage-partitions]');
        const disk = [...root.querySelectorAll('[data-storage-disk]')].find((button) => button.dataset.storageDisk === group?.dataset.storagePartitions);
        disk?.click();
        selected?.click();
      }
    } catch (failure) {
      content.replaceChildren();
      error.textContent = failure.message || 'Storage is unavailable.';
      error.hidden = false;
    } finally {
      loading = false;
      refresh.disabled = false;
      close.disabled = false;
    }
  }

  function open(target) {
    if (!dialog.open) dialog.showModal();
    void load(target);
  }
  window.JustVoxelSetupStorageManager = { open };
  document.addEventListener('click', (event) => {
    if (event.target.closest('[data-setup-storage-manage]')) open();
  });
  close.addEventListener('click', () => { if (!loading) dialog.close(); });
  dialog.addEventListener('cancel', (event) => { if (loading) event.preventDefault(); });
  refresh.addEventListener('click', () => void load(null, true));
})();
