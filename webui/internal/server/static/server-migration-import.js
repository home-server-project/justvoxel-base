function requestStandaloneImportSMBPassword() {
  return new Promise((resolve) => {
    const dialog = document.createElement("dialog");
    dialog.className = "system-action-dialog";
    dialog.innerHTML = '<div class="system-action-dialog-content"><h2>SMB password</h2><p>Enter the SMB password so JustVoxel can inspect or reopen this Import source.</p><label>SMB password<input type="password" autocomplete="off" required></label><div class="action-row"><button type="button" class="secondary" data-cancel>Cancel</button><button type="button" data-continue>Continue</button></div></div>';
    document.body.appendChild(dialog);
    const input = dialog.querySelector('input[type="password"]');
    const finish = (value) => {
      if (input) input.value = "";
      if (dialog.open) dialog.close();
      dialog.remove();
      resolve(value);
    };
    dialog.querySelector("[data-cancel]")?.addEventListener("click", () => finish(null), { once: true });
    dialog.addEventListener("cancel", (event) => { event.preventDefault(); finish(null); }, { once: true });
    dialog.querySelector("[data-continue]")?.addEventListener("click", () => {
      if (!input?.value) { input?.focus(); return; }
      finish(input.value);
    }, { once: true });
    dialog.showModal();
    input?.focus();
  });
}

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

  form.addEventListener("submit", async (event) => {
    const selectedKind = form.querySelector("[data-server-import-source-kind]")?.value || form.querySelector('input[name="source_kind"]')?.value || "";
    const needsSMB = selectedKind === "smb" || form.dataset.serverImportRequireSmb === "true";
    if (!needsSMB || form.dataset.serverImportCredentialReady === "true") return;
    event.preventDefault();
    const password = await requestStandaloneImportSMBPassword();
    if (password === null) return;
    const hidden = document.createElement("input");
    hidden.type = "hidden";
    hidden.name = "source_smb_password";
    hidden.value = password;
    form.appendChild(hidden);
    form.dataset.serverImportCredentialReady = "true";
    form.requestSubmit(event.submitter || undefined);
    hidden.value = "";
  });
}

window.JustVoxelServerMigrationImport = { init: initServerMigrationImport };
if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", () => initServerMigrationImport(document), { once: true });
} else {
  initServerMigrationImport(document);
}
