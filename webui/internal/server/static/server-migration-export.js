function initServerMigrationExport(root = document) {
  const form = root.querySelector("[data-server-export-form]");
  if (!form || form.dataset.serverMigrationExportInitialized === "true") return;
  form.dataset.serverMigrationExportInitialized = "true";

  const kind = form.querySelector("[data-server-export-kind]");
  const groups = Array.from(form.querySelectorAll("[data-server-export-fields]"));
  if (!kind) return;

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

window.JustVoxelServerMigrationExport = { init: initServerMigrationExport };
if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", () => initServerMigrationExport(document), { once: true });
} else {
  initServerMigrationExport(document);
}
