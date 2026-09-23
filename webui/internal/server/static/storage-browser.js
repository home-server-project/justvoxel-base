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
    mountType: detailDialog?.querySelector("[data-detail-mount-type]"),
    mountTypeRow: detailDialog?.querySelector("[data-detail-mount-type-row]"),
    system: detailDialog?.querySelector("[data-detail-system]"),
    readonly: detailDialog?.querySelector("[data-detail-readonly]"),
  };

  let selectedPartition = null;
  let selectedMountStatus = null;
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
    return (data.filesystem || "").trim();
  }

  function filesystemDisplay(filesystem) {
    const value = (filesystem || "").toLowerCase();
    if (value === "xfs") return "XFS";
    if (value === "ext4") return "ext4";
    if (value === "btrfs") return "Btrfs";
    if (value === "ntfs" || value === "ntfs-3g") return "NTFS";
    if (value === "vfat" || value === "fat" || value === "fat32") return "FAT / FAT32";
    if (value === "exfat") return "exFAT";
    if (!value) return "Not formatted";
    return filesystem || "Unknown";
  }

  function showStorageAction(action, visible) {
    const button = actionButtons.find((candidate) => candidate.dataset.storageAction === action);
    if (button) button.hidden = !visible;
  }

  function configurePartitionActions(data, status) {
    const protectedPartition = data.system === "Yes" || data.readonly === "Yes";
    if (detailActions) detailActions.hidden = protectedPartition;
    if (protectedNote) protectedNote.hidden = !protectedPartition;
    actionButtons.forEach((button) => { button.hidden = true; });
    setOptional(detail.mountTypeRow, detail.mountType, "");

    if (protectedPartition) {
      if (actionMenu) actionMenu.open = false;
      if (migrateButton) migrateButton.hidden = true;
      return;
    }

    if (migrateButton) migrateButton.hidden = data.minecraftCandidate !== "Yes";

    const filesystem = normalizedFilesystem(data);
    const filesystemKey = filesystem.toLowerCase();
    const mountable = ["xfs", "ext4", "btrfs", "ntfs", "vfat", "exfat"].includes(filesystemKey);
    const managedLinux = ["xfs", "ext4", "btrfs"].includes(filesystemKey);

    if (!filesystem) {
      showStorageAction("format", true);
      return;
    }
    if (!mountable) return;

    const mounted = status ? Boolean(status.mounted) : data.mounted === "Yes";
    const persistence = status?.persistence || "";

    if (!status) {
      if (mounted) showStorageAction("unmount-for-now", true);
      return;
    }

    if (persistence === "justvoxel") {
      setOptional(detail.mountTypeRow, detail.mountType, "Permanent");
      if (mounted) showStorageAction("unmount-for-now", true);
      else if (status.mount_point) showStorageAction("mount-now", true);
      showStorageAction("remove-permanent", true);
      return;
    }

    if (persistence === "external") {
      setOptional(detail.mountTypeRow, detail.mountType, "Permanent");
      if (mounted) showStorageAction("unmount-for-now", true);
      return;
    }

    if (persistence === "none") {
      showStorageAction("format", managedLinux);
      setOptional(detail.mountTypeRow, detail.mountType, mounted ? "For now" : "Not mounted");
      if (mounted) {
        showStorageAction("unmount-for-now", true);
        showStorageAction("make-permanent", true);
      } else {
        showStorageAction("mount-for-now", true);
        showStorageAction("mount-permanently", true);
      }
      return;
    }

    setOptional(detail.mountTypeRow, detail.mountType, "Needs attention");
    if (mounted) showStorageAction("unmount-for-now", true);
  }

  async function loadMountStatus(data) {
    const filesystem = normalizedFilesystem(data);
    const mountable = ["xfs", "ext4", "btrfs", "ntfs", "vfat", "exfat"].includes(filesystem.toLowerCase());
    if (!mountable || data.system === "Yes" || data.readonly === "Yes") return;

    const selectedPath = data.path || "";
    try {
      const response = await fetch("/api/new-storage/mounts/status?device=" + encodeURIComponent(selectedPath), {
        method: "GET",
        headers: { "Accept": "application/json" },
        credentials: "same-origin",
      });
      let payload = null;
      try { payload = await response.json(); } catch (_) { payload = null; }
      if (!response.ok || !payload?.ok) throw new Error(payload?.error || "Mount status is unavailable.");
      if (selectedPartition?.path !== selectedPath) return;
      selectedMountStatus = payload.proposed || null;
      configurePartitionActions(selectedPartition, selectedMountStatus);
    } catch (_) {
      if (selectedPartition?.path !== selectedPath) return;
      selectedMountStatus = null;
      configurePartitionActions(selectedPartition, null);
    }
  }

  partitionButtons.forEach((button) => {
    button.addEventListener("click", () => {
      if (!detailDialog) return;
      const data = button.dataset;
      selectedPartition = { ...data };
      selectedMountStatus = null;
      if (detail.title) detail.title.textContent = data.path || "Partition";
      if (detail.role) detail.role.textContent = data.role || "Partition";
      if (detail.path) detail.path.textContent = data.path || "";
      if (detail.size) detail.size.textContent = data.size || "Unknown";
      if (detail.filesystem) detail.filesystem.textContent = data.filesystemDisplay || filesystemDisplay(data.filesystem);
      if (detail.mounted) detail.mounted.textContent = data.mounted || "No";
      if (detail.system) detail.system.textContent = data.system || "No";
      if (detail.readonly) detail.readonly.textContent = data.readonly || "No";
      setOptional(detail.labelRow, detail.label, data.label);
      setOptional(detail.uuidRow, detail.uuid, data.uuid);
      setOptional(detail.mountRow, detail.mount, data.mountpoints);
      configurePartitionActions(selectedPartition, null);
      detailDialog.showModal();
      detailClose?.focus();
      void loadMountStatus(selectedPartition);
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
        : "This filesystem is not mounted. The migration backend can create a permanent mount at this location.";
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
    if (action === "mount-for-now") return "Mount for now";
    if (action === "mount-permanently") return "Mount permanently";
    if (action === "make-permanent") return "Make permanent";
    if (action === "mount-now") return "Mount now";
    if (action === "unmount-for-now") return "Unmount for now";
    if (action === "remove-permanent") return "Remove permanent mount";
    return "Format partition";
  }

  function actionCopy(action) {
    if (action === "mount-for-now") return "This storage will stay mounted at the selected location until you unmount it or reboot JustVoxel.";
    if (action === "mount-permanently") return "This storage will be mounted at the selected location and JustVoxel will mount it automatically after every reboot.";
    if (action === "make-permanent") return "JustVoxel will remember the current mount location and mount this storage automatically after every reboot.";
    if (action === "mount-now") return "This storage already has a permanent mount location. It will be mounted there now.";
    if (action === "unmount-for-now") {
      if (selectedMountStatus?.persistence === "justvoxel") {
        return "This storage will be unmounted now. JustVoxel will mount it automatically again after the next reboot.";
      }
      if (selectedMountStatus?.persistence === "external") {
        return "This storage will be unmounted now. Its existing saved mount setting will not be changed.";
      }
      return "This storage will be unmounted now. It will stay unmounted until you mount it again.";
    }
    if (action === "remove-permanent") return "This storage will be unmounted and JustVoxel will stop mounting it automatically after reboot. Your files will not be deleted.";
    return "Review exactly what will be destroyed before formatting this partition as XFS.";
  }

  function defaultMountPoint(data) {
    const name = data.name || (data.path || "").split("/").pop() || "storage";
    return "/var/mnt/" + name;
  }

  function actionRequest() {
    if (selectedAction === "mount-for-now") {
      return { family: "actions", operation: "mount", mountPoint: mountInput?.value.trim() || "" };
    }
    if (selectedAction === "unmount-for-now") {
      return { family: "actions", operation: "unmount", mountPoint: "" };
    }
    if (selectedAction === "format") {
      return { family: "actions", operation: "format", mountPoint: "" };
    }
    if (selectedAction === "mount-permanently") {
      return { family: "mounts", operation: "persist", mountPoint: mountInput?.value.trim() || "" };
    }
    if (selectedAction === "make-permanent") {
      return { family: "mounts", operation: "persist", mountPoint: selectedMountStatus?.current_mount_point || "" };
    }
    if (selectedAction === "mount-now") {
      return { family: "mounts", operation: "persist", mountPoint: selectedMountStatus?.mount_point || "" };
    }
    if (selectedAction === "remove-permanent") {
      return { family: "mounts", operation: "remove", mountPoint: "" };
    }
    return null;
  }

  function resetActionDialog(action) {
    selectedAction = action;
    reviewedPlan = null;
    if (actionTitle) actionTitle.textContent = actionLabel(action);
    if (actionDescription) actionDescription.textContent = actionCopy(action);
    const asksForMountPoint = action === "mount-for-now" || action === "mount-permanently";
    if (mountField) mountField.hidden = !asksForMountPoint;
    if (mountInput) mountInput.value = asksForMountPoint ? defaultMountPoint(selectedPartition || {}) : "";
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
      if (selectedAction === "mount-for-now" || selectedAction === "mount-permanently") mountInput?.focus();
      else reviewButton?.focus();
    });
  });

  function closeActionDialog() {
    actionDialog?.close();
  }
  actionClose?.addEventListener("click", closeActionDialog);
  actionCancel?.addEventListener("click", closeActionDialog);

  async function postAction(phase, extra) {
    const request = actionRequest();
    if (!request) throw new Error("Storage action could not be prepared.");
    const body = new URLSearchParams();
    body.set("csrf", csrf);
    body.set("operation", request.operation);
    body.set("device", selectedPartition?.path || "");
    if (request.mountPoint) body.set("mount_point", request.mountPoint);
    Object.entries(extra || {}).forEach(([key, value]) => body.set(key, value || ""));
    const response = await fetch("/api/new-storage/" + request.family + "/" + phase, {
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
    if (selectedAction === "mount-for-now") return "Mounted for now at " + (plan.mount_point || "selected location");
    if (selectedAction === "mount-permanently" || selectedAction === "make-permanent") return "Permanent mount at " + (plan.mount_point || "selected location");
    if (selectedAction === "mount-now") return "Mounted now at " + (plan.mount_point || "saved location");
    if (selectedAction === "unmount-for-now") {
      if (selectedMountStatus?.persistence === "justvoxel") return "Unmounted for now; returns after reboot";
      if (selectedMountStatus?.persistence === "external") return "Unmounted for now; saved setting kept";
      return "Unmounted";
    }
    if (selectedAction === "remove-permanent") return "Permanent mount removed";
    return "New XFS filesystem";
  }

  reviewButton?.addEventListener("click", async () => {
    if (!selectedPartition || !selectedAction) return;
    if (actionError) { actionError.hidden = true; actionError.textContent = ""; }
    reviewButton.disabled = true;
    reviewButton.textContent = "Reviewing…";
    try {
      const payload = await postAction("plan");
      reviewedPlan = payload.proposed;
      if (reviewPanel) reviewPanel.hidden = false;
      if (reviewDevice) reviewDevice.textContent = reviewedPlan.device || selectedPartition.path;
      if (reviewRole) reviewRole.textContent = reviewedPlan.role || selectedPartition.role || "Available";
      if (reviewFilesystem) reviewFilesystem.textContent = filesystemDisplay(reviewedPlan.filesystem);
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
      await postAction("apply", {
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
