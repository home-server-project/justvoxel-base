(() => {
  const storageForm = document.querySelector('[data-setup-storage-form]');
  if (storageForm) {
    const type = storageForm.querySelector('#setup-storage-type');
    const sections = [...storageForm.querySelectorAll('[data-setup-storage-kind]')];
    const device = storageForm.querySelector('#setup-storage-device');

    const fields = (kind) => [...storageForm.querySelectorAll(`[data-setup-field-kind="${kind}"]`)];
    const mountField = (kind) => fields(kind).find((field) => field.name === 'storage_mount_point');
    const pathField = (kind) => fields(kind).find((field) => field.name === 'storage_path');

    function applyPartitionDefaults(force) {
      if (!device || !device.value) return;
      const option = device.selectedOptions[0];
      const mount = mountField('partition');
      const path = pathField('partition');
      const existing = option?.dataset.mountpoint || '';
      const fallback = mount?.dataset.default || '/var/mnt/justvoxel-data';
      const target = existing || mount?.value || fallback;
      if (mount && (force || !mount.value)) mount.value = target;
      if (path && (force || !path.value)) path.value = `${target.replace(/\/$/, '')}/minecraft`;
    }

    function showStorageKind() {
      const kind = type?.value || 'system';
      sections.forEach((section) => {
        const active = section.dataset.setupStorageKind === kind;
        section.hidden = !active;
        section.querySelectorAll('input,select').forEach((field) => {
          field.disabled = !active;
        });
      });
      if (kind === 'partition') applyPartitionDefaults(false);
    }

    type?.addEventListener('change', () => {
      showStorageKind();
      if (type.value === 'partition') applyPartitionDefaults(true);
    });
    device?.addEventListener('change', () => applyPartitionDefaults(true));
    showStorageKind();
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
