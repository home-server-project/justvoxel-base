(() => {
  const storageGroup = document.querySelector('[data-nav-group="storage"]');
  storageGroup?.querySelector('.nav-trigger')?.setAttribute('aria-current', 'page');
  document.querySelector('a[href="/settings/backup-storage"]')?.setAttribute('aria-current', 'page');

  const form = document.querySelector('[data-storage-form]');
  if (!form) return;

  const type = form.querySelector('#storage-type');
  const sections = [...form.querySelectorAll('[data-storage-kind]')];
  const partition = form.querySelector('#storage-partition');

  const activeInput = (name, kind) => form.querySelector(`[data-storage-${name}="${kind}"]`);

  function showKind() {
    const kind = type.value;
    sections.forEach((section) => {
      const active = section.dataset.storageKind === kind;
      section.hidden = !active;
      section.querySelectorAll('input,select').forEach((input) => {
        input.disabled = !active;
      });
    });
    if (kind === 'partition') updatePartitionDefaults(false);
    if (kind === 'nfs' || kind === 'smb') updateNetworkDefaults(kind, false);
  }

  function updatePartitionDefaults(force) {
    if (!partition || !partition.value) return;
    const option = partition.selectedOptions[0];
    const mount = activeInput('mount', 'partition');
    const path = activeInput('path', 'partition');
    const mountedAt = option?.dataset.mountpoint || '';
    const targetMount = mountedAt || (mount?.value || '/var/mnt/justvoxel-backup');
    if (mount && (force || !mount.value)) mount.value = targetMount;
    if (path && (force || !path.value)) path.value = `${targetMount.replace(/\/$/, '')}/backups`;
  }

  function updateNetworkDefaults(kind, force) {
    const mount = activeInput('mount', kind);
    const path = activeInput('path', kind);
    if (mount && !mount.value) mount.value = '/var/mnt/justvoxel-backup';
    if (path && (force || !path.value)) path.value = `${(mount?.value || '/var/mnt/justvoxel-backup').replace(/\/$/, '')}/backups`;
  }

  type.addEventListener('change', () => {
    showKind();
    const kind = type.value;
    if (kind === 'partition') updatePartitionDefaults(true);
    if (kind === 'nfs' || kind === 'smb') updateNetworkDefaults(kind, true);
  });
  partition?.addEventListener('change', () => updatePartitionDefaults(true));
  ['nfs', 'smb'].forEach((kind) => {
    activeInput('mount', kind)?.addEventListener('change', () => updateNetworkDefaults(kind, true));
  });

  showKind();
})();
