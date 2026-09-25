(() => {
  const storageForm = document.querySelector('[data-setup-storage-form]');
  if (storageForm) {
    const csrf = storageForm.dataset.csrf || '';
    const type = storageForm.querySelector('[data-setup-storage-type]');
    const device = storageForm.querySelector('[data-setup-storage-device]');
    const mount = storageForm.querySelector('[data-setup-storage-mount]');
    const path = storageForm.querySelector('[data-setup-storage-path]');
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

    function minecraftPath(mountPoint) {
      return mountPoint.replace(/\/$/, '') + '/minecraft';
    }

    function setPanels(kind) {
      if (systemPanel) systemPanel.hidden = kind !== 'system';
      if (internalPanel) internalPanel.hidden = kind === 'system';
      systemButton?.classList.toggle('is-selected', kind === 'system');
      internalButton?.classList.toggle('is-selected', kind !== 'system');
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
      if (selectedBox) selectedBox.hidden = false;
      if (selectedDevice) selectedDevice.textContent = selectedDeviceValue;
      if (selectedPath) selectedPath.textContent = minecraftPath(selectedMount);
      setPanels('partition');
    }

    function chooseInternal() {
      setPanels('partition');
      if (type?.value === 'system') {
        type.value = '';
        device.value = '';
        mount.value = '';
        path.value = '';
        if (selectedBox) selectedBox.hidden = false;
        if (selectedDevice) selectedDevice.textContent = 'Choose a ready filesystem above';
        if (selectedPath) selectedPath.textContent = '';
      }
    }

    function resetReview() {
      reviewed = null;
      if (review) review.hidden = true;
      if (reviewError) {
        reviewError.hidden = true;
        reviewError.textContent = '';
      }
      if (confirmSlider) confirmSlider.value = '0';
      if (confirmToggle) {
        confirmToggle.checked = false;
        confirmToggle.disabled = true;
      }
      if (applyButton) {
        applyButton.disabled = true;
        applyButton.textContent = 'Apply reviewed action';
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
      resetReview();
      const operation = button.dataset.setupStoragePrepare || '';
      const request = {
        operation,
        device: button.dataset.device || '',
        freeStart: button.dataset.freeStart || '',
        sizeGiB: operation === 'create_partition' ? 'all' : '',
      };
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
        if (review) review.hidden = false;
        review.scrollIntoView({behavior: 'smooth', block: 'nearest'});
      } catch (error) {
        if (reviewError) {
          reviewError.textContent = error.message;
          reviewError.hidden = false;
        }
        if (review) review.hidden = false;
      } finally {
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
        window.location.reload();
      } catch (error) {
        applying = false;
        if (reviewError) {
          reviewError.textContent = error.message;
          reviewError.hidden = false;
        }
        applyButton.textContent = 'Apply reviewed action';
        updateApplyState();
      }
    }

    systemButton?.addEventListener('click', selectSystem);
    internalButton?.addEventListener('click', chooseInternal);
    existingButtons.forEach((button) => button.addEventListener('click', () => selectExisting(button)));
    prepareButtons.forEach((button) => button.addEventListener('click', () => void reviewPreparation(button)));
    reviewClose?.addEventListener('click', resetReview);
    reviewBack?.addEventListener('click', resetReview);
    confirmSlider?.addEventListener('input', updateApplyState);
    confirmToggle?.addEventListener('change', updateApplyState);
    applyButton?.addEventListener('click', () => void applyPreparation());

    if (type?.value === 'partition') {
      setPanels('partition');
      const current = existingButtons.find((button) => button.dataset.device === device?.value);
      if (current) selectExisting(current);
    } else {
      selectSystem();
    }
  }

  const backupForm = document.querySelector('[data-setup-backup-form]');
  if (backupForm) {
    const type = backupForm.querySelector('#setup-backup-type');
    const sections = [...backupForm.querySelectorAll('[data-setup-backup-kind]')];
    const device = backupForm.querySelector('#setup-backup-device');

    const fields = (kind) => [...backupForm.querySelectorAll(`[data-setup-backup-field="${kind}"]`)];
    const named = (kind, name) => fields(kind).find((field) => field.name === name);

    function ensurePath(kind, force) {
      const mount = named(kind, 'backup_mount_point');
      const path = named(kind, 'backup_path');
      const fallback = mount?.dataset.default || '/var/mnt/justvoxel-backup';
      const target = mount?.value || fallback;
      if (mount && !mount.value) mount.value = fallback;
      if (path && (force || !path.value)) path.value = `${target.replace(/\/$/, '')}/backups`;
    }

    function applyLocalDefaults(force) {
      if (!device || !device.value) return;
      const option = device.selectedOptions[0];
      const mount = named('partition', 'backup_mount_point');
      const path = named('partition', 'backup_path');
      const existing = option?.dataset.mountpoint || '';
      const fallback = mount?.dataset.default || '/var/mnt/justvoxel-backup';
      const target = existing || mount?.value || fallback;
      if (mount && (force || !mount.value)) mount.value = target;
      if (path && (force || !path.value)) path.value = `${target.replace(/\/$/, '')}/backups`;
    }

    function showBackupKind() {
      const kind = type?.value || 'system';
      sections.forEach((section) => {
        const active = section.dataset.setupBackupKind === kind;
        section.hidden = !active;
        section.querySelectorAll('input,select').forEach((field) => {
          field.disabled = !active;
        });
      });
      if (kind === 'partition') applyLocalDefaults(false);
      if (kind === 'nfs' || kind === 'smb') ensurePath(kind, false);
    }

    type?.addEventListener('change', () => {
      showBackupKind();
      const kind = type.value;
      if (kind === 'partition') applyLocalDefaults(true);
      if (kind === 'nfs' || kind === 'smb') ensurePath(kind, true);
    });
    device?.addEventListener('change', () => applyLocalDefaults(true));
    ['nfs', 'smb'].forEach((kind) => {
      named(kind, 'backup_mount_point')?.addEventListener('change', () => ensurePath(kind, true));
    });
    showBackupKind();
  }

})();
