document.addEventListener("DOMContentLoaded", () => {
  const form = document.querySelector("[data-server-export-form]");
  if (!form) return;

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
});
