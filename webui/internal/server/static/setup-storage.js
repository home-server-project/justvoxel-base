(() => {
  // Presentation only: setup retains its own plan/apply requests below.
  function diskBrowser(form, selectedDevice, preference) {
    const disks = Array.from(form.querySelectorAll('[data-setup-disk]'));
    const groups = Array.from(form.querySelectorAll('[data-setup-disk-partitions]'));
    let selected = '';
    let restored = '';
    // Store only a physical-disk UI preference across preparation's inventory reload.
    try {
      restored = sessionStorage.getItem(preference) || '';
      sessionStorage.removeItem(preference);
    } catch (_) { /* Disk preferences are optional when browser storage is unavailable. */ }
    function select(disk) {
      selected = disk.dataset.setupDisk;
      disks.forEach((button) => {
        const active = button === disk;
        button.classList.toggle('is-selected', active);
        button.setAttribute('aria-pressed', String(active));
      });
      groups.forEach((group) => { group.hidden = group.dataset.setupDiskPartitions !== selected; });
      // Large inventories may scroll; keep the chosen physical disk in view.
      disk.scrollIntoView({block: 'nearest', inline: 'nearest'});
    }
    disks.forEach((disk) => disk.addEventListener('click', () => select(disk)));
    const existing = groups.find((group) => Array.from(group.querySelectorAll('[data-device]'))
      .some((button) => button.dataset.device === selectedDevice));
    const initial = disks.find((disk) => disk.dataset.setupDisk === restored)
      || disks.find((disk) => disk.dataset.setupDisk === existing?.dataset.setupDiskPartitions) || disks[0];
    if (initial) select(initial);
    return {
      restored: !!restored && !!initial,
      reveal() {
        const disk = disks.find((button) => button.dataset.setupDisk === selected);
        disk?.scrollIntoView({block: 'nearest', inline: 'nearest'});
      },
      reload() {
        try { if (selected) sessionStorage.setItem(preference, selected); } catch (_) { /* Optional UI preference. */ }
        window.location.reload();
      },
    };
  }

  function syncPreparationSlider(slider) {
    const shell = slider?.closest('.destructive-confirm-slider');
    if (!shell) return;
    const progress = Number(slider.value || 0) / 100;
    shell.style.setProperty('--confirm-progress', String(progress));
    shell.classList.toggle('is-armed', progress >= 1);
    const text = shell.querySelector('.destructive-confirm-slider-text');
    if (text) text.textContent = progress >= 1 ? 'Ready to confirm' : 'Slide to confirm';
  }

  const storageForm = document.querySelector('[data-setup-storage-form]');
  if (storageForm) {
    const csrf = storageForm.dataset.csrf || '';
    const type = storageForm.querySelector('[data-setup-storage-type]');
    const device = storageForm.querySelector('[data-setup-storage-device]');
    const mount = storageForm.querySelector('[data-setup-storage-mount]');
    const path = storageForm.querySelector('[data-setup-storage-path]');
    const disks = diskBrowser(storageForm, device?.value, 'justvoxel-setup-data-disk');
    const systemButton = storageForm.querySelector('[data-setup-storage-system]');
    const internalButton = storageForm.querySelector('[data-setup-storage-internal]');
    const systemPanel = storageForm.querySelector('[data-setup-storage-system-panel]');
    const internalPanel = storageForm.querySelector('[data-setup-storage-internal-panel]');
    const existingButtons = Array.from(storageForm.querySelectorAll('[data-setup-existing-partition]'));
    const prepareButtons = Array.from(storageForm.querySelectorAll('[data-setup-storage-prepare]'));
    const selectedBox = storageForm.querySelector('[data-setup-storage-selected]');
    const selectedDevice = storageForm.querySelector('[data-setup-storage-selected-device]');
    const selectedPath = storageForm.querySelector('[data-setup-storage-selected-path]');

    const review = storageForm.querySelector('[data-setup-storage-action-review]');
    const reviewTitle = storageForm.querySelector('[data-setup-storage-action-title]');
    const reviewDevice = storageForm.querySelector('[data-setup-storage-action-device]');
    const reviewResult = storageForm.querySelector('[data-setup-storage-action-result]');
    const reviewWarnings = storageForm.querySelector('[data-setup-storage-action-warnings]');
    const reviewError = storageForm.querySelector('[data-setup-storage-action-error]');
    const reviewClose = storageForm.querySelector('[data-setup-storage-action-close]');
    const reviewBack = storageForm.querySelector('[data-setup-storage-action-back]');
    const confirmSlider = storageForm.querySelector('[data-setup-storage-confirm-slider]');
    const confirmToggle = storageForm.querySelector('[data-setup-storage-confirm-toggle]');
    const applyButton = storageForm.querySelector('[data-setup-storage-action-apply]');

    let reviewed = null;
    let applying = false;
    let planning = false;

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

    function resetReview() {
      if (applying) return;
      reviewed = null;
      review?.close();
      if (reviewError) {
        reviewError.hidden = true;
        reviewError.textContent = '';
      }
      if (confirmSlider) confirmSlider.value = '0';
      syncPreparationSlider(confirmSlider);
      if (confirmToggle) {
        confirmToggle.checked = false;
        confirmToggle.disabled = true;
      }
      if (applyButton) {
        applyButton.disabled = true;
        applyButton.textContent = 'Apply';
      }
    }

    function renderWarnings(warnings) {
      if (!reviewWarnings) return;
      reviewWarnings.replaceChildren();
      (warnings || []).forEach((warning) => {
        const notice = document.createElement('div');
        notice.className = 'notice warning compact';
        notice.textContent = warning;
        reviewWarnings.appendChild(notice);
      });
    }

    function updateApplyState() {
      syncPreparationSlider(confirmSlider);
      if (!reviewed || !applyButton || !confirmSlider || !confirmToggle) return;
      const armed = Number(confirmSlider.value || 0) >= 100;
      confirmToggle.disabled = !armed;
      if (!armed) confirmToggle.checked = false;
      applyButton.disabled = !(armed && confirmToggle.checked);
    }

    async function postStorageAction(phase, request) {
      const body = new URLSearchParams();
      body.set('csrf', csrf);
      body.set('operation', request.operation);
      body.set('device', request.device);
      if (request.freeStart) body.set('free_start', request.freeStart);
      if (request.sizeGiB) body.set('size_gib', request.sizeGiB);
      if (phase === 'apply') {
        body.set('fingerprint', reviewed?.proposed?.fingerprint || '');
        body.set('confirmation', reviewed?.proposed?.confirmation || '');
      }
      const response = await fetch('/api/new-storage/actions/' + phase, {
        method: 'POST',
        credentials: 'same-origin',
        headers: {'Content-Type': 'application/x-www-form-urlencoded;charset=UTF-8', 'Accept': 'application/json'},
        body,
      });
      const payload = await response.json().catch(() => null);
      if (!response.ok || !payload?.ok) {
        throw new Error(payload?.error || 'Storage preparation could not be completed.');
      }
      return payload;
    }

    async function reviewPreparation(button) {
      if (applying || planning) return;
      planning = true;
      resetReview();
      const operation = button.dataset.setupStoragePrepare || '';
      const request = {
        operation,
        device: button.dataset.device || '',
        freeStart: button.dataset.freeStart || '',
        sizeGiB: operation === 'create_partition' ? 'all' : '',
      };
      if (reviewTitle) reviewTitle.textContent = operation === 'create_partition' ? 'Create partition' : 'Format blank partition';
      if (reviewDevice) reviewDevice.textContent = request.device;
      if (reviewResult) reviewResult.textContent = operation === 'create_partition'
        ? 'Create an XFS partition in the selected unallocated space' : 'Format this blank partition as XFS';
      renderWarnings([]);
      button.disabled = true;
      try {
        const payload = await postStorageAction('plan', request);
        reviewed = {request, proposed: payload.proposed || {}, warnings: payload.warnings || []};
        if (reviewTitle) reviewTitle.textContent = operation === 'create_partition' ? 'Create Minecraft partition' : 'Format blank partition';
        if (reviewDevice) reviewDevice.textContent = reviewed.proposed.device || request.device;
        if (reviewResult) {
          reviewResult.textContent = operation === 'create_partition'
            ? 'Create one XFS partition using the selected unallocated space'
            : 'Create a new XFS filesystem on this blank partition';
        }
        renderWarnings(reviewed.warnings);
        if (review && !review.open) review.showModal();
      } catch (error) {
        if (reviewError) {
          reviewError.textContent = error.message;
          reviewError.hidden = false;
        }
        if (review && !review.open) review.showModal();
      } finally {
        planning = false;
        button.disabled = false;
      }
    }

    async function applyPreparation() {
      if (!reviewed || applying || applyButton?.disabled) return;
      applying = true;
      applyButton.disabled = true;
      applyButton.textContent = 'Applying…';
      if (reviewError) reviewError.hidden = true;
      try {
        await postStorageAction('apply', reviewed.request);
        disks.reload();
      } catch (error) {
        applying = false;
        if (reviewError) {
          reviewError.textContent = error.message;
          reviewError.hidden = false;
        }
        applyButton.textContent = 'Apply';
        updateApplyState();
      }
    }

    systemButton?.addEventListener('click', selectSystem);
    internalButton?.addEventListener('click', chooseInternal);
    existingButtons.forEach((button) => button.addEventListener('click', () => selectExisting(button)));
    prepareButtons.forEach((button) => button.addEventListener('click', () => void reviewPreparation(button)));
    storageForm.addEventListener('submit', (event) => { if (review?.open) event.preventDefault(); });
    review?.addEventListener('cancel', (event) => { event.preventDefault(); resetReview(); });
    reviewClose?.addEventListener('click', resetReview);
    reviewBack?.addEventListener('click', resetReview);
    confirmSlider?.addEventListener('input', updateApplyState);
    confirmToggle?.addEventListener('change', updateApplyState);
    applyButton?.addEventListener('click', () => void applyPreparation());

    if (type?.value === 'partition' || disks.restored) {
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

  const backupForm = document.querySelector('[data-setup-backup-form]');
  if (backupForm) {
    const csrf = backupForm.dataset.csrf || '';
    const type = backupForm.querySelector('[data-setup-backup-type]');
    const device = backupForm.querySelector('[data-setup-backup-device]');
    const mount = backupForm.querySelector('[data-setup-backup-mount]');
    const path = backupForm.querySelector('[data-setup-backup-path]');
    const disks = diskBrowser(backupForm, device?.value, 'justvoxel-setup-backup-disk');
    const source = backupForm.querySelector('[data-setup-backup-source]');
    const username = backupForm.querySelector('[data-setup-backup-username]');
    const domain = backupForm.querySelector('[data-setup-backup-domain]');
    const choices = Array.from(backupForm.querySelectorAll('[data-setup-backup-choice]'));
    const panels = Array.from(backupForm.querySelectorAll('[data-setup-backup-panel]'));
    const existingButtons = Array.from(backupForm.querySelectorAll('[data-setup-backup-existing]'));
    const prepareButtons = Array.from(backupForm.querySelectorAll('[data-setup-backup-prepare]'));
    const selectedBox = backupForm.querySelector('[data-setup-backup-selected]');
    const selectedDevice = backupForm.querySelector('[data-setup-backup-selected-device]');
    const selectedPath = backupForm.querySelector('[data-setup-backup-selected-path]');
    const sameDiskWarning = backupForm.querySelector('[data-setup-same-disk-warning]');
    const warningType = type?.value;
    const warningDevice = device?.value;

    const review = backupForm.querySelector('[data-setup-backup-action-review]');
    const reviewTitle = backupForm.querySelector('[data-setup-backup-action-title]');
    const reviewDevice = backupForm.querySelector('[data-setup-backup-action-device]');
    const reviewResult = backupForm.querySelector('[data-setup-backup-action-result]');
    const reviewWarnings = backupForm.querySelector('[data-setup-backup-action-warnings]');
    const reviewError = backupForm.querySelector('[data-setup-backup-action-error]');
    const reviewClose = backupForm.querySelector('[data-setup-backup-action-close]');
    const reviewBack = backupForm.querySelector('[data-setup-backup-action-back]');
    const confirmSlider = backupForm.querySelector('[data-setup-backup-confirm-slider]');
    const confirmToggle = backupForm.querySelector('[data-setup-backup-confirm-toggle]');
    const applyButton = backupForm.querySelector('[data-setup-backup-action-apply]');

    let reviewed = null;
    let applying = false;
    let planning = false;

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

    function resetReview() {
      if (applying) return;
      reviewed = null;
      review?.close();
      if (reviewError) {
        reviewError.hidden = true;
        reviewError.textContent = '';
      }
      if (confirmSlider) confirmSlider.value = '0';
      syncPreparationSlider(confirmSlider);
      if (confirmToggle) {
        confirmToggle.checked = false;
        confirmToggle.disabled = true;
      }
      if (applyButton) {
        applyButton.disabled = true;
        applyButton.textContent = 'Apply';
      }
    }

    function renderWarnings(warnings) {
      if (!reviewWarnings) return;
      reviewWarnings.replaceChildren();
      (warnings || []).forEach((warning) => {
        const notice = document.createElement('div');
        notice.className = 'notice warning compact';
        notice.textContent = warning;
        reviewWarnings.appendChild(notice);
      });
    }

    function updateApplyState() {
      syncPreparationSlider(confirmSlider);
      if (!reviewed || !applyButton || !confirmSlider || !confirmToggle) return;
      const armed = Number(confirmSlider.value || 0) >= 100;
      confirmToggle.disabled = !armed;
      if (!armed) confirmToggle.checked = false;
      applyButton.disabled = !(armed && confirmToggle.checked);
    }

    async function postStorageAction(phase, request) {
      const body = new URLSearchParams();
      body.set('csrf', csrf);
      body.set('operation', request.operation);
      body.set('device', request.device);
      if (request.freeStart) body.set('free_start', request.freeStart);
      if (request.sizeGiB) body.set('size_gib', request.sizeGiB);
      if (phase === 'apply') {
        body.set('fingerprint', reviewed?.proposed?.fingerprint || '');
        body.set('confirmation', reviewed?.proposed?.confirmation || '');
      }
      const response = await fetch('/api/new-storage/actions/' + phase, {
        method: 'POST',
        credentials: 'same-origin',
        headers: {'Content-Type': 'application/x-www-form-urlencoded;charset=UTF-8', 'Accept': 'application/json'},
        body,
      });
      const payload = await response.json().catch(() => null);
      if (!response.ok || !payload?.ok) {
        throw new Error(payload?.error || 'Backup storage preparation could not be completed.');
      }
      return payload;
    }

    async function reviewPreparation(button) {
      if (applying || planning) return;
      planning = true;
      resetReview();
      showKind('partition');
      const operation = button.dataset.setupBackupPrepare || '';
      const request = {
        operation,
        device: button.dataset.device || '',
        freeStart: button.dataset.freeStart || '',
        sizeGiB: operation === 'create_partition' ? 'all' : '',
      };
      if (reviewTitle) reviewTitle.textContent = operation === 'create_partition' ? 'Create partition' : 'Format blank partition';
      if (reviewDevice) reviewDevice.textContent = request.device;
      if (reviewResult) reviewResult.textContent = operation === 'create_partition'
        ? 'Create an XFS partition in the selected unallocated space' : 'Format this blank partition as XFS';
      renderWarnings([]);
      button.disabled = true;
      try {
        const payload = await postStorageAction('plan', request);
        reviewed = {request, proposed: payload.proposed || {}, warnings: payload.warnings || []};
        if (reviewTitle) reviewTitle.textContent = operation === 'create_partition' ? 'Create backup partition' : 'Format blank backup partition';
        if (reviewDevice) reviewDevice.textContent = reviewed.proposed.device || request.device;
        if (reviewResult) {
          reviewResult.textContent = operation === 'create_partition'
            ? 'Create one XFS backup partition using the selected unallocated space'
            : 'Create a new XFS filesystem on this blank partition';
        }
        renderWarnings(reviewed.warnings);
        if (review && !review.open) review.showModal();
      } catch (error) {
        if (reviewError) {
          reviewError.textContent = error.message;
          reviewError.hidden = false;
        }
        if (review && !review.open) review.showModal();
      } finally {
        planning = false;
        button.disabled = false;
      }
    }

    async function applyPreparation() {
      if (!reviewed || applying || applyButton?.disabled) return;
      applying = true;
      applyButton.disabled = true;
      applyButton.textContent = 'Applying…';
      try {
        await postStorageAction('apply', reviewed.request);
        disks.reload();
      } catch (error) {
        applying = false;
        if (reviewError) {
          reviewError.textContent = error.message;
          reviewError.hidden = false;
        }
        applyButton.textContent = 'Apply';
        updateApplyState();
      }
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
    existingButtons.forEach((button) => button.addEventListener('click', () => selectExisting(button)));
    prepareButtons.forEach((button) => button.addEventListener('click', () => void reviewPreparation(button)));

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

    backupForm.addEventListener('submit', (event) => { if (review?.open) event.preventDefault(); });
    review?.addEventListener('cancel', (event) => { event.preventDefault(); resetReview(); });
    reviewClose?.addEventListener('click', resetReview);
    reviewBack?.addEventListener('click', resetReview);
    confirmSlider?.addEventListener('input', updateApplyState);
    confirmToggle?.addEventListener('change', updateApplyState);
    applyButton?.addEventListener('click', () => void applyPreparation());

    backupForm.addEventListener('submit', () => syncNetworkFields(type?.value || 'system'));

    if (type?.value === 'partition' || disks.restored) {
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

})();
