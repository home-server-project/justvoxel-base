(() => {
  // Destination selection stays inline; preparations use reviewed storage APIs.
  function diskBrowser(form, selectedDevice) {
    // Physical disk selection belongs to the shared Storage browser.
    form.querySelectorAll('[data-setup-disk]').forEach(button => { button.dataset.storageDisk = button.dataset.setupDisk; });
    form.querySelectorAll('[data-setup-disk-partitions]').forEach(panel => { panel.dataset.storagePartitions = panel.dataset.setupDiskPartitions; });
    if (!form.querySelector('[data-setup-disk][aria-pressed="true"]')) {
      const group = [...form.querySelectorAll('[data-setup-disk-partitions]')].find(panel => [...panel.querySelectorAll('[data-device]')].some(button => button.dataset.device === selectedDevice));
      [...form.querySelectorAll('[data-setup-disk]')].find(button => button.dataset.setupDisk === group?.dataset.setupDiskPartitions)?.setAttribute('aria-pressed', 'true');
    }
    return {reveal() { form.querySelector('[data-setup-disk][aria-pressed="true"]')?.scrollIntoView({block: 'nearest', inline: 'nearest'}); }};
  }

  function initSetupStorage(root = document) {
    const storageForm = root.querySelector('[data-setup-storage-form]');
    if (storageForm) {
      const type = storageForm.querySelector('[data-setup-storage-type]');
      const device = storageForm.querySelector('[data-setup-storage-device]');
      const mount = storageForm.querySelector('[data-setup-storage-mount]');
      const path = storageForm.querySelector('[data-setup-storage-path]');
      const disks = diskBrowser(storageForm, device?.value);
      const systemButton = storageForm.querySelector('[data-setup-storage-system]');
      const internalButton = storageForm.querySelector('[data-setup-storage-internal]');
      const systemPanel = storageForm.querySelector('[data-setup-storage-system-panel]');
      const internalPanel = storageForm.querySelector('[data-setup-storage-internal-panel]');
      const existingButtons = Array.from(storageForm.querySelectorAll('[data-setup-existing-partition]'));
      const selectedBox = storageForm.querySelector('[data-setup-storage-selected]');
      const selectedDevice = storageForm.querySelector('[data-setup-storage-selected-device]');
      const selectedPath = storageForm.querySelector('[data-setup-storage-selected-path]');

      function minecraftPath(mountPoint) {
        return mountPoint.replace(/\/$/, '') + '/minecraft';
      }

      function setPanels(kind) {
        if (systemPanel) systemPanel.hidden = kind !== 'system';
        if (internalPanel) internalPanel.hidden = kind === 'system';
        systemButton?.classList.toggle('is-selected', kind === 'system');
        internalButton?.classList.toggle('is-selected', kind !== 'system');
        if (kind !== 'system') disks.reveal();
      }

      function selectSystem() {
        if (type) type.value = 'system';
        if (device) device.value = '';
        if (mount) mount.value = '';
        if (path) path.value = systemButton?.dataset.systemPath || '/var/lib/justvoxel/minecraft';
        existingButtons.forEach((button) => button.classList.remove('is-selected'));
        if (selectedBox) selectedBox.hidden = true;
        setPanels('system');
      }

      function selectExisting(button) {
        const selectedMount = button.dataset.mountpoint || '/var/mnt/justvoxel-data';
        const selectedDeviceValue = button.dataset.device || '';
        if (type) type.value = 'partition';
        if (device) device.value = selectedDeviceValue;
        if (mount) mount.value = selectedMount;
        if (path) path.value = minecraftPath(selectedMount);
        existingButtons.forEach((candidate) => candidate.classList.toggle('is-selected', candidate === button));
        existingButtons.forEach((candidate) => {
          const badge = candidate.querySelector('.setup-storage-segment-state');
          if (badge) badge.textContent = candidate === button ? 'Selected' : 'Ready';
        });
        if (selectedBox) selectedBox.hidden = false;
        if (selectedDevice) selectedDevice.textContent = selectedDeviceValue;
        if (selectedPath) selectedPath.textContent = minecraftPath(selectedMount);
        setPanels('partition');
      }

      function chooseInternal() {
        setPanels('partition');
        if (type) type.value = 'partition';
        if (device) device.value = '';
        if (mount) mount.value = '';
        if (path) path.value = '';
        existingButtons.forEach((button) => button.classList.remove('is-selected'));
        if (selectedBox) selectedBox.hidden = true;
      }

      systemButton?.addEventListener('click', selectSystem);
      internalButton?.addEventListener('click', chooseInternal);
      storageForm.addEventListener('setup-destination-select', event => {
        const button = existingButtons.find(candidate => candidate.dataset.device === event.detail);
        if (button) selectExisting(button);
      });

      if (type?.value === 'partition') {
        setPanels('partition');
        if (type) type.value = 'partition';
        const current = existingButtons.find((button) => button.dataset.device === device?.value);
        if (current) selectExisting(current);
        else {
          // A refreshed inventory may no longer contain the draft's old partition.
          if (device) device.value = '';
          if (mount) mount.value = '';
          if (path) path.value = '';
          if (selectedBox) selectedBox.hidden = true;
        }
      } else {
        selectSystem();
      }
    }

    const backupForm = root.querySelector('[data-setup-backup-form]');
    if (backupForm) {
      const type = backupForm.querySelector('[data-setup-backup-type]');
      const device = backupForm.querySelector('[data-setup-backup-device]');
      const mount = backupForm.querySelector('[data-setup-backup-mount]');
      const path = backupForm.querySelector('[data-setup-backup-path]');
      const disks = diskBrowser(backupForm, device?.value);
      const source = backupForm.querySelector('[data-setup-backup-source]');
      const username = backupForm.querySelector('[data-setup-backup-username]');
      const domain = backupForm.querySelector('[data-setup-backup-domain]');
      const choices = Array.from(backupForm.querySelectorAll('[data-setup-backup-choice]'));
      const panels = Array.from(backupForm.querySelectorAll('[data-setup-backup-panel]'));
      const existingButtons = Array.from(backupForm.querySelectorAll('[data-setup-backup-existing]'));
      const selectedBox = backupForm.querySelector('[data-setup-backup-selected]');
      const selectedDevice = backupForm.querySelector('[data-setup-backup-selected-device]');
      const selectedPath = backupForm.querySelector('[data-setup-backup-selected-path]');
      const sameDiskWarning = backupForm.querySelector('[data-setup-same-disk-warning]');
      const warningType = type?.value;
      const warningDevice = device?.value;

      const backupPath = (mountPoint) => mountPoint.replace(/\/$/, '') + '/backups';

      function clearLocalSelection() {
        existingButtons.forEach((button) => button.classList.remove('is-selected'));
        if (device) device.value = '';
        if (selectedBox) selectedBox.hidden = true;
      }

      function showKind(kind) {
        if (type) type.value = kind;
        choices.forEach((choice) => choice.classList.toggle('is-selected', choice.dataset.setupBackupChoice === kind));
        panels.forEach((panel) => {
          panel.hidden = panel.dataset.setupBackupPanel !== kind;
        });
        if (kind === 'partition') disks.reveal();
        if (kind !== 'partition') clearLocalSelection();
        else if (selectedBox) selectedBox.hidden = !device?.value;
        syncNetworkFields(kind);
        if (sameDiskWarning) sameDiskWarning.hidden = kind !== warningType || (kind === 'partition' && device?.value !== warningDevice);
      }

      function selectSystem(choice) {
        showKind('system');
        if (mount) mount.value = '';
        if (path) path.value = choice?.dataset.systemPath || '/var/lib/justvoxel/backups';
        if (source) source.value = '';
        if (username) username.value = '';
        if (domain) domain.value = '';
      }

      function selectExisting(button) {
        showKind('partition');
        const targetDevice = button.dataset.device || '';
        const targetMount = targetDevice === backupForm.dataset.dataDevice && backupForm.dataset.dataMount
          ? backupForm.dataset.dataMount : (button.dataset.mountpoint || '/var/mnt/justvoxel-backup');
        if (device) device.value = targetDevice;
        if (mount) mount.value = targetMount;
        if (path) path.value = backupPath(targetMount);
        if (source) source.value = '';
        if (username) username.value = '';
        if (domain) domain.value = '';
        existingButtons.forEach((candidate) => candidate.classList.toggle('is-selected', candidate === button));
        existingButtons.forEach((candidate) => {
          const badge = candidate.querySelector('.setup-storage-segment-state');
          if (badge) badge.textContent = candidate === button ? 'Selected' : 'Ready';
        });
        if (selectedBox) selectedBox.hidden = false;
        if (selectedDevice) selectedDevice.textContent = targetDevice;
        if (selectedPath) selectedPath.textContent = backupPath(targetMount);
        if (sameDiskWarning) sameDiskWarning.hidden = targetDevice !== warningDevice;
      }

      function syncNetworkFields(kind) {
        if (kind !== 'nfs' && kind !== 'smb') return;
        const sourceInput = backupForm.querySelector('[data-setup-network-source="' + kind + '"]');
        const mountInput = backupForm.querySelector('[data-setup-network-mount="' + kind + '"]');
        const pathInput = backupForm.querySelector('[data-setup-network-path="' + kind + '"]');
        const fallback = '/var/mnt/justvoxel-backup';
        if (mountInput && !mountInput.value) mountInput.value = fallback;
        if (pathInput && !pathInput.value) pathInput.value = backupPath(mountInput?.value || fallback);
        if (source) source.value = sourceInput?.value || '';
        if (mount) mount.value = mountInput?.value || '';
        if (path) path.value = pathInput?.value || '';
        if (kind === 'smb') {
          if (username) username.value = backupForm.querySelector('[data-setup-smb-username]')?.value || '';
          if (domain) domain.value = backupForm.querySelector('[data-setup-smb-domain]')?.value || '';
        } else {
          if (username) username.value = '';
          if (domain) domain.value = '';
        }
        if (device) device.value = '';
      }

      choices.forEach((choice) => {
        choice.addEventListener('click', () => {
          const kind = choice.dataset.setupBackupChoice || 'system';
          if (kind === 'system') {
            selectSystem(choice);
            return;
          }
          showKind(kind);
          if (kind === 'partition') {
            if (type) type.value = 'partition';
            if (!device?.value) {
              if (mount) mount.value = '';
              if (path) path.value = '';
            }
          }
        });
      });
      backupForm.addEventListener('setup-destination-select', event => {
        const button = existingButtons.find(candidate => candidate.dataset.device === event.detail);
        if (button) selectExisting(button);
      });

      ['nfs', 'smb'].forEach((kind) => {
        backupForm.querySelector('[data-setup-network-source="' + kind + '"]')?.addEventListener('input', () => syncNetworkFields(kind));
        const mountInput = backupForm.querySelector('[data-setup-network-mount="' + kind + '"]');
        const pathInput = backupForm.querySelector('[data-setup-network-path="' + kind + '"]');
        mountInput?.addEventListener('input', () => {
          if (pathInput && (!pathInput.value || pathInput.value.endsWith('/backups'))) {
            pathInput.value = backupPath(mountInput.value || '/var/mnt/justvoxel-backup');
          }
          syncNetworkFields(kind);
        });
        pathInput?.addEventListener('input', () => syncNetworkFields(kind));
      });
      backupForm.querySelector('[data-setup-smb-username]')?.addEventListener('input', () => syncNetworkFields('smb'));
      backupForm.querySelector('[data-setup-smb-domain]')?.addEventListener('input', () => syncNetworkFields('smb'));


      backupForm.addEventListener('submit', () => syncNetworkFields(type?.value || 'system'));

      if (type?.value === 'partition') {
        showKind('partition');
        const current = existingButtons.find((button) => button.dataset.device === device?.value);
        if (current) selectExisting(current);
        else {
          if (device) device.value = '';
          if (mount) mount.value = '';
          if (path) path.value = '';
          if (selectedBox) selectedBox.hidden = true;
        }
      } else if (type?.value === 'nfs' || type?.value === 'smb') {
        showKind(type.value);
      } else {
        const systemChoice = choices.find((choice) => choice.dataset.setupBackupChoice === 'system');
        selectSystem(systemChoice);
      }
    }

    window.JustVoxelSetupDiskActions?.init(root);
  }
  window.JustVoxelSetupStorage = { init: initSetupStorage };
  initSetupStorage();
})();
