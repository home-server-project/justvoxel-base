function requestStandaloneExportSMBPassword() {
  return new Promise((resolve) => {
    const dialog = document.createElement("dialog");
    dialog.className = "system-action-dialog";
    dialog.innerHTML = '<div class="system-action-dialog-content"><h2>SMB password</h2><p>Enter the SMB password to start this reviewed Export.</p><label>SMB password<input type="password" autocomplete="off" required></label><div class="action-row"><button type="button" class="secondary" data-cancel>Cancel</button><button type="button" data-continue>Continue</button></div></div>';
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

function initServerMigrationExport(root = document) {
  const form = root.querySelector("[data-server-export-form]");
  if (!form || form.dataset.serverMigrationExportInitialized === "true") return;
  form.dataset.serverMigrationExportInitialized = "true";

  const kind = form.querySelector("[data-server-export-kind]");
  const groups = Array.from(form.querySelectorAll("[data-server-export-fields]"));

  if (kind) {
    const render = () => {
      const selected = kind.value;
      groups.forEach((group) => {
        const active = group.dataset.serverExportFields === selected;
        group.hidden = !active;
        group.querySelectorAll("input, select").forEach((field) => {
          field.disabled = !active;
        });
      });
    };
    kind.addEventListener("change", render);
    render();
  }

  const workspaceManaged = Boolean(form.closest("[data-migration-workspace-root]"));
  if (workspaceManaged) return;

  form.addEventListener("submit", async (event) => {
    if (form.dataset.serverExportRequireSmb !== "true" || form.dataset.serverExportCredentialReady === "true") return;
    event.preventDefault();
    const password = await requestStandaloneExportSMBPassword();
    if (password === null) return;
    const hidden = document.createElement("input");
    hidden.type = "hidden";
    hidden.name = "smb_password";
    hidden.value = password;
    form.appendChild(hidden);
    form.dataset.serverExportCredentialReady = "true";
    form.requestSubmit(event.submitter || undefined);
    hidden.value = "";
  });
}

window.JustVoxelServerMigrationExport = { init: initServerMigrationExport };
if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", () => initServerMigrationExport(document), { once: true });
} else {
  initServerMigrationExport(document);
}
