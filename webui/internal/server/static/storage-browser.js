(() => {
  const diskButtons = [...document.querySelectorAll("[data-storage-disk]")];
  const diskPanels = [...document.querySelectorAll("[data-storage-partitions]")];
  const partitionButtons = [...document.querySelectorAll("[data-storage-partition]")];
  const dialog = document.querySelector("[data-storage-detail-dialog]");
  const closeButton = dialog?.querySelector("[data-storage-detail-close]");

  const detail = {
    title: dialog?.querySelector("[data-detail-title]"),
    role: dialog?.querySelector("[data-detail-role]"),
    path: dialog?.querySelector("[data-detail-path]"),
    size: dialog?.querySelector("[data-detail-size]"),
    filesystem: dialog?.querySelector("[data-detail-filesystem]"),
    label: dialog?.querySelector("[data-detail-label]"),
    labelRow: dialog?.querySelector("[data-detail-label-row]"),
    uuid: dialog?.querySelector("[data-detail-uuid]"),
    uuidRow: dialog?.querySelector("[data-detail-uuid-row]"),
    mount: dialog?.querySelector("[data-detail-mount]"),
    mountRow: dialog?.querySelector("[data-detail-mount-row]"),
    mounted: dialog?.querySelector("[data-detail-mounted]"),
    system: dialog?.querySelector("[data-detail-system]"),
    readonly: dialog?.querySelector("[data-detail-readonly]"),
  };

  function selectDisk(name) {
    diskButtons.forEach((button) => {
      const selected = button.dataset.storageDisk === name;
      button.classList.toggle("is-selected", selected);
      button.setAttribute("aria-pressed", selected ? "true" : "false");
    });
    diskPanels.forEach((panel) => {
      panel.hidden = panel.dataset.storagePartitions !== name;
    });
  }

  diskButtons.forEach((button) => {
    button.addEventListener("click", () => selectDisk(button.dataset.storageDisk));
  });

  function setOptional(row, node, value) {
    if (!row || !node) return;
    const visible = Boolean(value);
    row.hidden = !visible;
    node.textContent = value || "";
  }

  partitionButtons.forEach((button) => {
    button.addEventListener("click", () => {
      if (!dialog) return;
      const data = button.dataset;
      if (detail.title) detail.title.textContent = data.path || "Partition";
      if (detail.role) detail.role.textContent = data.role || "Partition";
      if (detail.path) detail.path.textContent = data.path || "";
      if (detail.size) detail.size.textContent = data.size || "Unknown";
      if (detail.filesystem) detail.filesystem.textContent = data.filesystem || "Unknown";
      if (detail.mounted) detail.mounted.textContent = data.mounted || "No";
      if (detail.system) detail.system.textContent = data.system || "No";
      if (detail.readonly) detail.readonly.textContent = data.readonly || "No";
      setOptional(detail.labelRow, detail.label, data.label);
      setOptional(detail.uuidRow, detail.uuid, data.uuid);
      setOptional(detail.mountRow, detail.mount, data.mountpoints);
      dialog.showModal();
      closeButton?.focus();
    });
  });

  closeButton?.addEventListener("click", () => dialog?.close());

  dialog?.addEventListener("cancel", (event) => {
    event.preventDefault();
  });
})();
