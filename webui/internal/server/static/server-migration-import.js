function initServerMigrationImport(root = document) {
  const form = root.querySelector("[data-server-import-form]");
  if (!form || form.dataset.serverMigrationImportInitialized === "true") return;
  form.dataset.serverMigrationImportInitialized = "true";

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
}

window.JustVoxelServerMigrationImport = { init: initServerMigrationImport };
if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", () => initServerMigrationImport(document), { once: true });
} else {
  initServerMigrationImport(document);
}
