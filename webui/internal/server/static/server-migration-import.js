document.addEventListener("DOMContentLoaded", () => {
  const form = document.querySelector("[data-server-import-form]");
  if (!form) return;

  const sourceKind = form.querySelector("[data-server-import-source-kind]");
  const sourceGroups = Array.from(form.querySelectorAll("[data-server-import-source]"));
  const storageKind = form.querySelector("[data-server-import-storage-kind]");
  const storageGroups = Array.from(form.querySelectorAll("[data-server-import-storage]"));
  const backupKind = form.querySelector("[data-server-import-backup-kind]");
  const backupGroups = Array.from(form.querySelectorAll("[data-server-import-backup]"));

  const switchGroups = (control, groups, attribute) => {
    if (!control) return;
    const selected = control.value;
    groups.forEach((group) => {
      const active = group.dataset[attribute] === selected;
      group.hidden = !active;
      group.querySelectorAll("input, select").forEach((field) => {
        field.disabled = !active;
      });
    });
  };

  if (sourceKind) {
    sourceKind.addEventListener("change", () => switchGroups(sourceKind, sourceGroups, "serverImportSource"));
    switchGroups(sourceKind, sourceGroups, "serverImportSource");
  }
  if (storageKind) {
    storageKind.addEventListener("change", () => switchGroups(storageKind, storageGroups, "serverImportStorage"));
    switchGroups(storageKind, storageGroups, "serverImportStorage");
  }
  if (backupKind) {
    backupKind.addEventListener("change", () => switchGroups(backupKind, backupGroups, "serverImportBackup"));
    switchGroups(backupKind, backupGroups, "serverImportBackup");
  }
});
