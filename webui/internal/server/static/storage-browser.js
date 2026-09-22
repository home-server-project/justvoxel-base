(() => {
  const diskButtons = [...document.querySelectorAll("[data-storage-disk]")];
  const diskPanels = [...document.querySelectorAll("[data-storage-partitions]")];
  const partitionButtons = [...document.querySelectorAll("[data-storage-partition]")];
  const detailDialog = document.querySelector("[data-storage-detail-dialog]");
  const detailClose = detailDialog?.querySelector("[data-storage-detail-close]");
  const detailActions = detailDialog?.querySelector("[data-storage-detail-actions]");
  const protectedNote = detailDialog?.querySelector("[data-storage-protected-note]");
  const actionMenu = detailDialog?.querySelector("[data-storage-action-menu]");
  const actionButtons = [...document.querySelectorAll("[data-storage-action]")];
  const migrateButton = detailDialog?.querySelector("[data-storage-minecraft-migrate]");
  const csrf = document.querySelector("[data-storage-action-csrf]")?.value || "";

  const migrationDialog = document.querySelector("[data-storage-minecraft-dialog]");
  const migrationClose = migrationDialog?.querySelector("[data-storage-minecraft-close]");
  const migrationCancel = migrationDialog?.querySelector("[data-storage-minecraft-cancel]");
  const migrationDevice = migrationDialog?.querySelector("[data-storage-minecraft-device]");
  const migrationSelected = migrationDialog?.querySelector("[data-storage-minecraft-selected]");
  const migrationMount = migrationDialog?.querySelector("[data-storage-minecraft-mount]");
  const migrationMountNote = migrationDialog?.querySelector("[data-storage-minecraft-mount-note]");
  const migrationPath = migrationDialog?.querySelector("[data-storage-minecraft-path]");

  const actionDialog = document.querySelector("[data-storage-action-dialog]");
  const actionClose = actionDialog?.querySelector("[data-storage-action-close]");
  const actionCancel = actionDialog?.querySelector("[data-storage-action-cancel]");
  const actionTitle = actionDialog?.querySelector("[data-storage-action-title]");
  const actionDescription = actionDialog?.querySelector("[data-storage-action-description]");
  const mountField = actionDialog?.querySelector("[data-storage-mount-field]");
  const mountInput = actionDialog?.querySelector("[data-storage-mount-input]");
  const reviewPanel = actionDialog?.querySelector("[data-storage-action-review]");
  const reviewDevice = actionDialog?.querySelector("[data-storage-review-device]");
  const reviewRole = actionDialog?.querySelector("[data-storage-review-role]");
  const reviewFilesystem = actionDialog?.querySelector("[data-storage-review-filesystem]");
  const reviewTargetRow = actionDialog?.querySelector("[data-storage-review-target-row]");
  const reviewTarget = actionDialog?.querySelector("[data-storage-review-target]");
  const warningBox = actionDialog?.querySelector("[data-storage-action-warnings]");
  const confirmationField = actionDialog?.querySelector("[data-storage-confirmation-field]");
  const confirmationPhrase = actionDialog?.querySelector("[data-storage-confirmation-phrase]");
  const confirmationInput = actionDialog?.querySelector("[data-storage-confirmation-input]");
  const actionError = actionDialog?.querySelector("[data-storage-action-error]");
  const reviewButton = actionDialog?.querySelector("[data-storage-action-review-button]");
  const applyButton = actionDialog?.querySelector("[data-storage-action-apply-button]");

  const detail = {
    title: detailDialog?.querySelector("[data-detail-title]"),
    role: detailDialog?.querySelector("[data-detail-role]"),
    path: detailDialog?.querySelector("[data-detail-path]"),
    size: detailDialog?.querySelector("[data-detail-size]"),
    filesystem: detailDialog?.querySelector("[data-detail-filesystem]"),
    label: detailDialog?.querySelector("[data-detail-label]"),
    labelRow: detailDialog?.querySelector("[data-detail-label-row]"),
    uuid: detailDialog?.querySelector("[data-detail-uuid]"),
    uuidRow: detailDialog?.querySelector("[data-detail-uuid-row]"),
    mount: detailDialog?.querySelector("[data-detail-mount]"),
    mountRow: detailDialog?.querySelector("[data-detail-mount-row]"),
    mounted: detailDialog?.querySelector("[data-detail-mounted]"),
    system: detailDialog?.querySelector("[data-detail-system]"),
    readonly: detailDialog?.querySelector("[data-detail-readonly]"),
  };

  let selectedPartition = null;
  let selectedAction = "";
  let reviewedPlan = null;

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

  function normalizedFilesystem(data) {
    return data.filesystem && data.filesystem !== "Not formatted" ? data.filesystem : "";
  }

  function configurePartitionActions(data) {
    const protectedPartition = data.system === "Yes" || data.readonly === "Yes";
    if (detailActions) detailActions.hidden = protectedPartition;
    if (protectedNote) protectedNote.hidden = !protectedPartition;
    if (protectedPartition) {
      if (actionMenu) actionMenu.open = false;
      if (migrateButton) migrateButton.hidden = true;
      return;
    }

    const filesystem = normalizedFilesystem(data);
    const mounted = data.mounted === "Yes";
    const supportedMount = ["xfs", "ext4", "btrfs"].includes(filesystem.toLowerCase());
    actionButtons.forEach((button) => {
      const action = button.dataset.storageAction;
      if (action === "mount") button.hidden = mounted || !supportedMount;
      if (action === "unmount") button.hidden = !mounted;
      if (action === "format") button.hidden = false;
    });
    if (migrateButton) migrateButton.hidden = data.minecraftCandidate !== "Yes";
  }

  partitionButtons.forEach((button) => {
    button.addEventListener("click", () => {
      if (!detailDialog) return;
      const data = button.dataset;
      selectedPartition = { ...data };
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
      configurePartitionActions(data);
      detailDialog.showModal();
      detailClose?.focus();
    });
  });

  detailClose?.addEventListener("click", () => detailDialog?.close());
  detailDialog?.addEventListener("cancel", (event) => event.preventDefault());

  function minecraftDataPath(mountPoint) {
    return (mountPoint || "/var/mnt/justvoxel-data").replace(/\/$/, "") + "/minecraft";
  }

  function closeMigrationDialog() {
    migrationDialog?.close();
  }

  migrateButton?.addEventListener("click", () => {
    if (!selectedPartition || selectedPartition.minecraftCandidate !== "Yes" || !migrationDialog) return;
    if (actionMenu) actionMenu.open = false;
    detailDialog?.close();

    const existingMount = selectedPartition.minecraftMountPoint || "";
    const mountPoint = existingMount || "/var/mnt/justvoxel-data";
    if (migrationDevice) migrationDevice.value = selectedPartition.path || "";
    if (migrationSelected) migrationSelected.textContent = selectedPartition.path || "";
    if (migrationMount) {
      migrationMount.value = mountPoint;
      migrationMount.readOnly = Boolean(existingMount);
    }
    if (migrationMountNote) {
      migrationMountNote.textContent = existingMount
        ? "This filesystem is already mounted. The migration Review will verify this exact mount point."
        : "This filesystem is not mounted. The migration backend can create a persistent UUID mount at this location.";
    }
    if (migrationPath) migrationPath.value = minecraftDataPath(mountPoint);
    migrationDialog.showModal();
    migrationPath?.focus();
  });

  migrationMount?.addEventListener("input", () => {
    if (!migrationMount.readOnly && migrationPath) migrationPath.value = minecraftDataPath(migrationMount.value.trim());
  });
  migrationClose?.addEventListener("click", closeMigrationDialog);
  migrationCancel?.addEventListener("click", closeMigrationDialog);

  function actionLabel(action) {
    if (action === "mount") return "Mount partition";
    if (action === "unmount") return "Unmount partition";
    return "Format partition";
  }

  function actionCopy(action) {
    if (action === "mount") return "Choose where this filesystem should be mounted for the current boot.";
    if (action === "unmount") return "Review this partition before unmounting it.";
    return "Review exactly what will be destroyed before formatting this partition as XFS.";
  }

  function defaultMountPoint(data) {
    const name = data.name || (data.path || "").split("/").pop() || "storage";
    return "/var/mnt/" + name;
  }

  function resetActionDialog(action) {
    selectedAction = action;
    reviewedPlan = null;
    if (actionTitle) actionTitle.textContent = actionLabel(action);
    if (actionDescription) actionDescription.textContent = actionCopy(action);
    if (mountField) mountField.hidden = action !== "mount";
    if (mountInput) mountInput.value = action === "mount" ? defaultMountPoint(selectedPartition || {}) : "";
    if (reviewPanel) reviewPanel.hidden = true;
    if (warningBox) warningBox.replaceChildren();
    if (confirmationField) confirmationField.hidden = true;
    if (confirmationPhrase) confirmationPhrase.textContent = "";
    if (confirmationInput) confirmationInput.value = "";
    if (actionError) { actionError.hidden = true; actionError.textContent = ""; }
    if (reviewButton) { reviewButton.hidden = false; reviewButton.disabled = false; reviewButton.textContent = "Review action"; }
    if (applyButton) { applyButton.hidden = true; applyButton.disabled = false; applyButton.textContent = "Apply action"; }
  }

  actionButtons.forEach((button) => {
    button.addEventListener("click", () => {
      if (!selectedPartition || !actionDialog) return;
      if (actionMenu) actionMenu.open = false;
      resetActionDialog(button.dataset.storageAction);
      actionDialog.showModal();
      if (selectedAction === "mount") mountInput?.focus();
      else reviewButton?.focus();
    });
  });

  function closeActionDialog() {
    actionDialog?.close();
  }
  actionClose?.addEventListener("click", closeActionDialog);
  actionCancel?.addEventListener("click", closeActionDialog);

  async function postAction(path, extra) {
    const body = new URLSearchParams();
    body.set("csrf", csrf);
    body.set("operation", selectedAction);
    body.set("device", selectedPartition?.path || "");
    if (selectedAction === "mount") body.set("mount_point", mountInput?.value.trim() || "");
    Object.entries(extra || {}).forEach(([key, value]) => body.set(key, value || ""));
    const response = await fetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: body.toString(),
      credentials: "same-origin",
    });
    let payload = null;
    try { payload = await response.json(); } catch (_) { payload = null; }
    if (!response.ok || !payload?.ok) {
      throw new Error(payload?.error || "Storage action could not be completed.");
    }
    return payload;
  }

  function renderWarnings(warnings) {
    if (!warningBox) return;
    warningBox.replaceChildren();
    (warnings || []).forEach((message) => {
      const notice = document.createElement("div");
      notice.className = "notice warning";
      notice.textContent = message;
      warningBox.appendChild(notice);
    });
  }

  function targetDescription(plan) {
    if (plan.operation === "mount") return "Mounted at " + (plan.mount_point || "selected mount point");
    if (plan.operation === "unmount") return "Unmounted";
    return "New XFS filesystem";
  }

  reviewButton?.addEventListener("click", async () => {
    if (!selectedPartition || !selectedAction) return;
    if (actionError) { actionError.hidden = true; actionError.textContent = ""; }
    reviewButton.disabled = true;
    reviewButton.textContent = "Reviewing…";
    try {
      const payload = await postAction("/api/new-storage/actions/plan");
      reviewedPlan = payload.proposed;
      if (reviewPanel) reviewPanel.hidden = false;
      if (reviewDevice) reviewDevice.textContent = reviewedPlan.device || selectedPartition.path;
      if (reviewRole) reviewRole.textContent = reviewedPlan.role || selectedPartition.role || "Available";
      if (reviewFilesystem) reviewFilesystem.textContent = reviewedPlan.filesystem || "Not formatted";
      if (reviewTarget) reviewTarget.textContent = targetDescription(reviewedPlan);
      if (reviewTargetRow) reviewTargetRow.hidden = false;
      renderWarnings(payload.warnings);
      const needsConfirmation = Boolean(reviewedPlan.confirmation);
      if (confirmationField) confirmationField.hidden = !needsConfirmation;
      if (confirmationPhrase) confirmationPhrase.textContent = reviewedPlan.confirmation || "";
      if (confirmationInput) confirmationInput.value = "";
      reviewButton.hidden = true;
      if (applyButton) { applyButton.hidden = false; applyButton.textContent = reviewedPlan.destructive ? "Apply destructive action" : "Apply action"; }
      if (needsConfirmation) confirmationInput?.focus();
      else applyButton?.focus();
    } catch (error) {
      if (actionError) { actionError.textContent = error.message; actionError.hidden = false; }
    } finally {
      reviewButton.disabled = false;
      reviewButton.textContent = "Review action";
    }
  });

  applyButton?.addEventListener("click", async () => {
    if (!reviewedPlan) return;
    if (actionError) { actionError.hidden = true; actionError.textContent = ""; }
    const expected = reviewedPlan.confirmation || "";
    const entered = confirmationInput?.value || "";
    if (expected && entered !== expected) {
      if (actionError) { actionError.textContent = "Type the confirmation phrase exactly before applying."; actionError.hidden = false; }
      confirmationInput?.focus();
      return;
    }
    applyButton.disabled = true;
    applyButton.textContent = "Applying…";
    try {
      await postAction("/api/new-storage/actions/apply", {
        fingerprint: reviewedPlan.fingerprint,
        confirmation: entered,
      });
      window.location.reload();
    } catch (error) {
      if (actionError) { actionError.textContent = error.message; actionError.hidden = false; }
      applyButton.disabled = false;
      applyButton.textContent = reviewedPlan.destructive ? "Apply destructive action" : "Apply action";
    }
  });
})();
