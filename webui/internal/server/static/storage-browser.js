(() => {
  function initStorageBrowser(root = document) {
    if (!root) return;
    if (root.dataset?.storageBrowserInitialized === "true") return;
    if (root.dataset) root.dataset.storageBrowserInitialized = "true";
  const diskButtons = [...root.querySelectorAll("[data-storage-disk]")];
  const diskPanels = [...root.querySelectorAll("[data-storage-partitions]")];
  const partitionButtons = [...root.querySelectorAll("[data-storage-partition]")];
  const freeSpaceButtons = [...root.querySelectorAll("[data-storage-free-space]")];
  const detailDialog = root.querySelector("[data-storage-detail-dialog]");
  const detailClose = detailDialog?.querySelector("[data-storage-detail-close]");
  const detailActions = detailDialog?.querySelector("[data-storage-detail-actions]");
  const protectedNote = detailDialog?.querySelector("[data-storage-protected-note]");
  const actionMenu = detailDialog?.querySelector("[data-storage-action-menu]");
  const actionButtons = [...root.querySelectorAll("[data-storage-action]")];
  const migrateButton = detailDialog?.querySelector("[data-storage-minecraft-migrate]");
  const csrf = root.querySelector("[data-storage-action-csrf]")?.value || "";

  const wholeDiskButtons = [...root.querySelectorAll("[data-storage-whole-disk]")];
  const wholeDiskDialog = root.querySelector("[data-storage-whole-disk-dialog]");
  const wholeDiskClose = wholeDiskDialog?.querySelector("[data-storage-whole-close]");
  const wholeDiskCancel = wholeDiskDialog?.querySelector("[data-storage-whole-cancel]");
  const wholeDiskTitle = wholeDiskDialog?.querySelector("[data-storage-whole-title]");
  const wholeDiskDescription = wholeDiskDialog?.querySelector("[data-storage-whole-description]");
  const wholeDiskDevice = wholeDiskDialog?.querySelector("[data-storage-whole-device-label]");
  const wholeDiskSize = wholeDiskDialog?.querySelector("[data-storage-whole-size-label]");
  const wholeDiskPlanRow = wholeDiskDialog?.querySelector("[data-storage-whole-plan-row]");
  const wholeDiskAfter = wholeDiskDialog?.querySelector("[data-storage-whole-after]");
  const wholeDiskMountRow = wholeDiskDialog?.querySelector("[data-storage-whole-mount-row]");
  const wholeDiskMount = wholeDiskDialog?.querySelector("[data-storage-whole-mount]");
  const wholeDiskWarnings = wholeDiskDialog?.querySelector("[data-storage-whole-warnings]");
  const wholeDiskPlayersField = wholeDiskDialog?.querySelector("[data-storage-whole-players-field]");
  const wholeDiskPlayers = wholeDiskDialog?.querySelector("[data-storage-whole-players]");
  const wholeDiskPlayersNote = wholeDiskDialog?.querySelector("[data-storage-whole-players-note]");
  const wholeDiskConfirmationField = wholeDiskDialog?.querySelector("[data-storage-whole-confirmation-field]");
  const wholeDiskConfirmationSummary = wholeDiskDialog?.querySelector("[data-storage-whole-confirmation-summary]");
  const wholeDiskConfirmSliderShell = wholeDiskDialog?.querySelector("[data-storage-whole-confirm-slider-shell]");
  const wholeDiskConfirmSliderText = wholeDiskDialog?.querySelector("[data-storage-whole-confirm-slider-text]");
  const wholeDiskConfirmSlider = wholeDiskDialog?.querySelector("[data-storage-whole-confirm-slider]");
  const wholeDiskConfirmToggleRow = wholeDiskDialog?.querySelector("[data-storage-whole-confirm-toggle-row]");
  const wholeDiskConfirmToggle = wholeDiskDialog?.querySelector("[data-storage-whole-confirm-toggle]");
  const wholeDiskError = wholeDiskDialog?.querySelector("[data-storage-whole-error]");
  const wholeDiskReview = wholeDiskDialog?.querySelector("[data-storage-whole-review]");
  const wholeDiskApply = wholeDiskDialog?.querySelector("[data-storage-whole-apply]");

  const migrationDialog = root.querySelector("[data-storage-minecraft-dialog]");
  const migrationClose = migrationDialog?.querySelector("[data-storage-minecraft-close]");
  const migrationTitle = migrationDialog?.querySelector("[data-storage-minecraft-title]");
  const migrationSetup = migrationDialog?.querySelector("[data-storage-minecraft-setup]");
  const migrationCancel = migrationDialog?.querySelector("[data-storage-minecraft-cancel]");
  const migrationReview = migrationDialog?.querySelector("[data-storage-minecraft-review]");
  const migrationDevice = migrationDialog?.querySelector("[data-storage-minecraft-device]");
  const migrationSelected = migrationDialog?.querySelector("[data-storage-minecraft-selected]");
  const migrationMount = migrationDialog?.querySelector("[data-storage-minecraft-mount]");
  const migrationMountNote = migrationDialog?.querySelector("[data-storage-minecraft-mount-note]");
  const migrationPath = migrationDialog?.querySelector("[data-storage-minecraft-path]");
  const migrationError = migrationDialog?.querySelector("[data-storage-minecraft-error]");
  const migrationReviewPanel = migrationDialog?.querySelector("[data-storage-minecraft-review-panel]");
  const migrationCurrentPath = migrationDialog?.querySelector("[data-storage-minecraft-current-path]");
  const migrationReviewDevice = migrationDialog?.querySelector("[data-storage-minecraft-review-device]");
  const migrationReviewFilesystem = migrationDialog?.querySelector("[data-storage-minecraft-review-filesystem]");
  const migrationReviewMount = migrationDialog?.querySelector("[data-storage-minecraft-review-mount]");
  const migrationReviewPath = migrationDialog?.querySelector("[data-storage-minecraft-review-path]");
  const migrationReviewCapacity = migrationDialog?.querySelector("[data-storage-minecraft-review-capacity]");
  const migrationWarnings = migrationDialog?.querySelector("[data-storage-minecraft-warnings]");
  const migrationPlayersField = migrationDialog?.querySelector("[data-storage-minecraft-players-field]");
  const migrationPlayers = migrationDialog?.querySelector("[data-storage-minecraft-players]");
  const migrationPlayersNote = migrationDialog?.querySelector("[data-storage-minecraft-players-note]");
  const migrationDestructiveField = migrationDialog?.querySelector("[data-storage-minecraft-destructive-field]");
  const migrationDestructivePhrase = migrationDialog?.querySelector("[data-storage-minecraft-destructive-phrase]");
  const migrationDestructive = migrationDialog?.querySelector("[data-storage-minecraft-destructive]");
  const migrationConfirmation = migrationDialog?.querySelector("[data-storage-minecraft-confirmation]");
  const migrationReviewError = migrationDialog?.querySelector("[data-storage-minecraft-review-error]");
  const migrationBack = migrationDialog?.querySelector("[data-storage-minecraft-back]");
  const migrationApply = migrationDialog?.querySelector("[data-storage-minecraft-apply]");
  const migrationProgress = migrationDialog?.querySelector("[data-storage-minecraft-progress]");
  const migrationProgressState = migrationDialog?.querySelector("[data-storage-minecraft-progress-state]");
  const migrationProgressStage = migrationDialog?.querySelector("[data-storage-minecraft-progress-stage]");
  const migrationProgressStatus = migrationDialog?.querySelector("[data-storage-minecraft-progress-status]");
  const migrationProgressID = migrationDialog?.querySelector("[data-storage-minecraft-progress-id]");
  const migrationReconnect = migrationDialog?.querySelector("[data-storage-minecraft-reconnect]");
  const migrationSuccess = migrationDialog?.querySelector("[data-storage-minecraft-success]");
  const migrationRollback = migrationDialog?.querySelector("[data-storage-minecraft-rollback]");
  const migrationAttention = migrationDialog?.querySelector("[data-storage-minecraft-attention]");
  const migrationProgressClose = migrationDialog?.querySelector("[data-storage-minecraft-progress-close]");
  const migrationRefresh = migrationDialog?.querySelector("[data-storage-minecraft-refresh]");

  const actionDialog = root.querySelector("[data-storage-action-dialog]");
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
  const confirmationSummary = actionDialog?.querySelector("[data-storage-confirmation-summary]");
  const confirmSliderShell = actionDialog?.querySelector("[data-storage-confirm-slider-shell]");
  const confirmSliderText = actionDialog?.querySelector("[data-storage-confirm-slider-text]");
  const confirmSlider = actionDialog?.querySelector("[data-storage-confirm-slider]");
  const confirmToggleRow = actionDialog?.querySelector("[data-storage-confirm-toggle-row]");
  const confirmToggle = actionDialog?.querySelector("[data-storage-confirm-toggle]");
  const actionError = actionDialog?.querySelector("[data-storage-action-error]");
  const reviewButton = actionDialog?.querySelector("[data-storage-action-review-button]");
  const applyButton = actionDialog?.querySelector("[data-storage-action-apply-button]");

  const freeDetailDialog = root.querySelector("[data-storage-free-detail-dialog]");
  const freeDetailClose = freeDetailDialog?.querySelector("[data-storage-free-detail-close]");
  const freeDetailDevice = freeDetailDialog?.querySelector("[data-storage-free-device]");
  const freeDetailSize = freeDetailDialog?.querySelector("[data-storage-free-size]");
  const freeActionMenu = freeDetailDialog?.querySelector("[data-storage-free-action-menu]");
  const createPartitionButton = freeDetailDialog?.querySelector("[data-storage-create-partition]");

  const createDialog = root.querySelector("[data-storage-create-dialog]");
  const createClose = createDialog?.querySelector("[data-storage-create-close]");
  const createCancel = createDialog?.querySelector("[data-storage-create-cancel]");
  const createDevice = createDialog?.querySelector("[data-storage-create-device]");
  const createAvailable = createDialog?.querySelector("[data-storage-create-available]");
  const createUseAll = createDialog?.querySelector("[data-storage-create-all]");
  const createSize = createDialog?.querySelector("[data-storage-create-size]");
  const createReviewPanel = createDialog?.querySelector("[data-storage-create-review-panel]");
  const createAfter = createDialog?.querySelector("[data-storage-create-after]");
  const createWarnings = createDialog?.querySelector("[data-storage-create-warnings]");
  const createConfirmationField = createDialog?.querySelector("[data-storage-create-confirmation-field]");
  const createConfirmationSummary = createDialog?.querySelector("[data-storage-create-confirmation-summary]");
  const createConfirmSliderShell = createDialog?.querySelector("[data-storage-create-confirm-slider-shell]");
  const createConfirmSliderText = createDialog?.querySelector("[data-storage-create-confirm-slider-text]");
  const createConfirmSlider = createDialog?.querySelector("[data-storage-create-confirm-slider]");
  const createConfirmToggleRow = createDialog?.querySelector("[data-storage-create-confirm-toggle-row]");
  const createConfirmToggle = createDialog?.querySelector("[data-storage-create-confirm-toggle]");
  const createError = createDialog?.querySelector("[data-storage-create-error]");
  const createReview = createDialog?.querySelector("[data-storage-create-review]");
  const createApply = createDialog?.querySelector("[data-storage-create-apply]");

  const detail = {
    title: detailDialog?.querySelector("[data-detail-title]"),
    role: detailDialog?.querySelector("[data-detail-role]"),
    path: detailDialog?.querySelector("[data-detail-path]"),
    size: detailDialog?.querySelector("[data-detail-size]"),
    used: detailDialog?.querySelector("[data-detail-used]"),
    usedRow: detailDialog?.querySelector("[data-detail-used-row]"),
    free: detailDialog?.querySelector("[data-detail-free]"),
    freeRow: detailDialog?.querySelector("[data-detail-free-row]"),
    usageUnavailableRow: detailDialog?.querySelector("[data-detail-usage-unavailable-row]"),
    filesystem: detailDialog?.querySelector("[data-detail-filesystem]"),
    label: detailDialog?.querySelector("[data-detail-label]"),
    labelRow: detailDialog?.querySelector("[data-detail-label-row]"),
    uuid: detailDialog?.querySelector("[data-detail-uuid]"),
    uuidRow: detailDialog?.querySelector("[data-detail-uuid-row]"),
    mount: detailDialog?.querySelector("[data-detail-mount]"),
    mountRow: detailDialog?.querySelector("[data-detail-mount-row]"),
    mountLabel: detailDialog?.querySelector("[data-detail-mount-label]"),
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
  let selectedWholeDisk = null;
  let reviewedWholeDisk = null;
  let selectedFreeSpace = null;
  let reviewedCreatePartition = null;
  let wholeDiskApplying = false;
  let actionApplying = false;
  let createApplying = false;
  let reviewedMigrationPlan = null;
  let migrationApplying = false;
  let migrationPollTimer = null;
  let migrationPollFailures = 0;

  function closeActionMenu() {
    if (actionMenu) actionMenu.open = false;
  }

  function closeFreeActionMenu() {
    if (freeActionMenu) freeActionMenu.open = false;
  }

  function selectDisk(name) {
    closeActionMenu();
    closeFreeActionMenu();
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
      closeActionMenu();
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
      closeActionMenu();
      selectedPartition = { ...data };
      selectedMountStatus = null;
      if (detail.title) detail.title.textContent = data.path || "Partition";
      if (detail.role) detail.role.textContent = data.role || "Partition";
      if (detail.path) detail.path.textContent = data.path || "";
      if (detail.size) detail.size.textContent = data.size || "Unknown";
      const usageKnown = data.filesystemUsageKnown === "Yes";
      if (detail.usedRow) detail.usedRow.hidden = !usageKnown;
      if (detail.freeRow) detail.freeRow.hidden = !usageKnown;
      if (detail.usageUnavailableRow) detail.usageUnavailableRow.hidden = usageKnown || !data.filesystem;
      if (detail.used) detail.used.textContent = usageKnown ? (data.filesystemUsed || "Unknown") : "";
      if (detail.free) detail.free.textContent = usageKnown ? (data.filesystemFree || "Unknown") : "";
      if (detail.filesystem) detail.filesystem.textContent = data.filesystemDisplay || filesystemDisplay(data.filesystem);
      if (detail.mounted) detail.mounted.textContent = data.mounted || "No";
      if (detail.system) detail.system.textContent = data.system || "No";
      if (detail.readonly) detail.readonly.textContent = data.readonly || "No";
      setOptional(detail.labelRow, detail.label, data.label);
      setOptional(detail.uuidRow, detail.uuid, data.uuid);
      if (detail.mountLabel) detail.mountLabel.textContent = "Mounted at";
      setOptional(detail.mountRow, detail.mount, data.system === "Yes" ? "" : data.mountpoints);
      configurePartitionActions(selectedPartition, null);
      detailDialog.showModal();
      detailClose?.focus();
      void loadMountStatus(selectedPartition);
    });
  });

  function closeDetailDialog() {
    closeActionMenu();
    detailDialog?.close();
  }

  detailClose?.addEventListener("click", closeDetailDialog);
  detailDialog?.addEventListener("click", (event) => { if (event.target === detailDialog) closeDetailDialog(); });
  detailDialog?.addEventListener("close", closeActionMenu);
  detailDialog?.addEventListener("cancel", closeActionMenu);
  root.addEventListener("pointerdown", (event) => {
    if (actionMenu?.open) {
      if (!actionMenu.contains(event.target)) closeActionMenu();
    }
    if (freeActionMenu?.open && !freeActionMenu.contains(event.target)) closeFreeActionMenu();
  });

  function confirmationSliderArmed(slider, shell, text, armedText, idleText) {
    const value = Number(slider?.value || 0);
    const progress = Math.max(0, Math.min(100, value));
    shell?.style.setProperty("--confirm-progress", String(progress / 100));
    const armed = progress >= 100;
    shell?.classList.toggle("is-armed", armed);
    if (text) text.textContent = armed ? armedText : idleText;
    slider?.setAttribute("aria-valuetext", armed ? "Ready for final confirmation" : progress + " percent");
    return armed;
  }

  function resetConfirmationControl(field, summary, slider, shell, text, toggleRow, toggle, idleText) {
    if (field) field.hidden = true;
    if (summary) summary.textContent = "";
    if (slider) slider.value = "0";
    if (toggle) toggle.checked = false;
    if (toggleRow) toggleRow.hidden = true;
    confirmationSliderArmed(slider, shell, text, "", idleText);
  }

  function renderWholeDiskWarnings(warnings) {
    if (!wholeDiskWarnings) return;
    wholeDiskWarnings.replaceChildren();
    (warnings || []).forEach((message) => {
      const notice = document.createElement("div");
      notice.className = "notice warning";
      notice.textContent = message;
      wholeDiskWarnings.appendChild(notice);
    });
  }

  function resetWholeDiskDialog(button) {
    selectedWholeDisk = {
      purpose: button.dataset.storageWholePurpose || "",
      device: button.dataset.storageWholeDevice || "",
      size: button.dataset.storageWholeSize || "",
    };
    reviewedWholeDisk = null;
    const minecraft = selectedWholeDisk.purpose === "minecraft";
    if (wholeDiskTitle) wholeDiskTitle.textContent = minecraft ? "Use this disk for Minecraft data" : "Use this disk for backups";
    if (wholeDiskDescription) {
      wholeDiskDescription.textContent = minecraft
        ? "JustVoxel will review this whole disk, then safely migrate Minecraft data onto a new XFS filesystem."
        : "JustVoxel will review this whole disk, then create and activate a new XFS backup filesystem.";
    }
    if (wholeDiskDevice) wholeDiskDevice.textContent = selectedWholeDisk.device;
    if (wholeDiskSize) wholeDiskSize.textContent = selectedWholeDisk.size;
    if (wholeDiskPlanRow) wholeDiskPlanRow.hidden = true;
    if (wholeDiskMountRow) wholeDiskMountRow.hidden = true;
    renderWholeDiskWarnings([]);
    if (wholeDiskPlayersField) wholeDiskPlayersField.hidden = true;
    if (wholeDiskPlayers) wholeDiskPlayers.checked = false;
    if (wholeDiskPlayersNote) wholeDiskPlayersNote.textContent = "";
    resetConfirmationControl(
      wholeDiskConfirmationField,
      wholeDiskConfirmationSummary,
      wholeDiskConfirmSlider,
      wholeDiskConfirmSliderShell,
      wholeDiskConfirmSliderText,
      wholeDiskConfirmToggleRow,
      wholeDiskConfirmToggle,
      "Slide to confirm erasing",
    );
    if (wholeDiskError) { wholeDiskError.hidden = true; wholeDiskError.textContent = ""; }
    if (wholeDiskReview) { wholeDiskReview.hidden = false; wholeDiskReview.disabled = false; wholeDiskReview.textContent = "Review action"; }
    if (wholeDiskApply) { wholeDiskApply.hidden = true; wholeDiskApply.disabled = false; wholeDiskApply.textContent = "Apply destructive action"; }
  }

  async function postWholeDisk(phase) {
    const body = new URLSearchParams();
    body.set("csrf", csrf);
    body.set("purpose", selectedWholeDisk?.purpose || "");
    body.set("device", selectedWholeDisk?.device || "");
    if (reviewedWholeDisk?.fingerprint) body.set("fingerprint", reviewedWholeDisk.fingerprint);
    if (
      phase === "apply" &&
      reviewedWholeDisk?.confirmation &&
      Number(wholeDiskConfirmSlider?.value || 0) >= 100 &&
      wholeDiskConfirmToggle?.checked
    ) {
      body.set("confirmation", reviewedWholeDisk.confirmation);
    }
    if (wholeDiskPlayers?.checked) body.set("players_confirmed", "yes");
    const response = await fetch("/api/new-storage/whole-disk/" + phase, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: body.toString(),
      credentials: "same-origin",
    });
    let payload = null;
    try { payload = await response.json(); } catch (_) { payload = null; }
    if (!response.ok || !payload?.ok) throw new Error(payload?.error || "Disk preparation could not be completed.");
    return payload;
  }

  wholeDiskButtons.forEach((button) => {
    button.addEventListener("click", () => {
      if (!wholeDiskDialog) return;
      resetWholeDiskDialog(button);
      wholeDiskDialog.showModal();
      wholeDiskReview?.focus();
    });
  });

  function closeWholeDiskDialog() {
    if (wholeDiskApplying) return;
    wholeDiskDialog?.close();
  }
  wholeDiskClose?.addEventListener("click", closeWholeDiskDialog);
  wholeDiskCancel?.addEventListener("click", closeWholeDiskDialog);
  wholeDiskDialog?.addEventListener("click", (event) => { if (event.target === wholeDiskDialog) closeWholeDiskDialog(); });
  wholeDiskDialog?.addEventListener("cancel", (event) => { if (wholeDiskApplying) event.preventDefault(); });

  wholeDiskReview?.addEventListener("click", async () => {
    if (!selectedWholeDisk) return;
    if (wholeDiskError) { wholeDiskError.hidden = true; wholeDiskError.textContent = ""; }
    wholeDiskReview.disabled = true;
    wholeDiskReview.textContent = "Reviewing…";
    try {
      const payload = await postWholeDisk("plan");
      reviewedWholeDisk = payload;
      if (wholeDiskAfter) wholeDiskAfter.textContent = payload.purpose === "minecraft" ? "Minecraft data on a new XFS filesystem" : "Backups on a new XFS filesystem";
      if (wholeDiskPlanRow) wholeDiskPlanRow.hidden = false;
      if (wholeDiskMount) wholeDiskMount.textContent = payload.mount_point || "";
      if (wholeDiskMountRow) wholeDiskMountRow.hidden = !payload.mount_point;
      renderWholeDiskWarnings(payload.warnings);
      const needsPlayers = Boolean(payload.players_confirmation_required);
      if (wholeDiskPlayersField) wholeDiskPlayersField.hidden = !needsPlayers;
      if (wholeDiskPlayersNote) {
        const names = (payload.players || []).join(", ");
        wholeDiskPlayersNote.textContent = needsPlayers ? (names ? " Online: " + names : " Players are online.") : "";
      }
      const confirmation = payload.confirmation || "";
      const destructiveSummary = (payload.device || selectedWholeDisk.device) + " will be erased";
      resetConfirmationControl(
        wholeDiskConfirmationField,
        wholeDiskConfirmationSummary,
        wholeDiskConfirmSlider,
        wholeDiskConfirmSliderShell,
        wholeDiskConfirmSliderText,
        wholeDiskConfirmToggleRow,
        wholeDiskConfirmToggle,
        "Slide to confirm erasing",
      );
      if (wholeDiskConfirmationSummary) wholeDiskConfirmationSummary.textContent = destructiveSummary;
      if (wholeDiskConfirmationField) wholeDiskConfirmationField.hidden = !confirmation;
      wholeDiskReview.hidden = true;
      if (wholeDiskApply) {
        wholeDiskApply.hidden = false;
        wholeDiskApply.disabled = Boolean(confirmation) || Boolean(payload.players_confirmation_required);
      }
      if (confirmation) wholeDiskConfirmSlider?.focus();
      else if (payload.players_confirmation_required) wholeDiskPlayers?.focus();
      else wholeDiskApply?.focus();
    } catch (error) {
      if (wholeDiskError) { wholeDiskError.textContent = error.message; wholeDiskError.hidden = false; }
    } finally {
      wholeDiskReview.disabled = false;
      wholeDiskReview.textContent = "Review action";
    }
  });

  function updateWholeDiskApplyState() {
    if (!wholeDiskApply || !reviewedWholeDisk) return;
    const needsConfirmation = Boolean(reviewedWholeDisk.confirmation);
    const sliderArmed = confirmationSliderArmed(
      wholeDiskConfirmSlider,
      wholeDiskConfirmSliderShell,
      wholeDiskConfirmSliderText,
      (reviewedWholeDisk.device || selectedWholeDisk?.device || "Selected disk") + " will be erased",
      "Slide to confirm erasing",
    );
    if (wholeDiskConfirmToggleRow) wholeDiskConfirmToggleRow.hidden = !needsConfirmation || !sliderArmed;
    if (!sliderArmed && wholeDiskConfirmToggle) wholeDiskConfirmToggle.checked = false;
    const confirmationReady = !needsConfirmation || (sliderArmed && Boolean(wholeDiskConfirmToggle?.checked));
    const playersReady = !reviewedWholeDisk.players_confirmation_required || Boolean(wholeDiskPlayers?.checked);
    wholeDiskApply.disabled = !(confirmationReady && playersReady);
  }

  wholeDiskConfirmSlider?.addEventListener("input", updateWholeDiskApplyState);
  wholeDiskConfirmToggle?.addEventListener("change", updateWholeDiskApplyState);
  wholeDiskPlayers?.addEventListener("change", updateWholeDiskApplyState);

  wholeDiskApply?.addEventListener("click", async () => {
    if (!reviewedWholeDisk) return;
    if (wholeDiskError) { wholeDiskError.hidden = true; wholeDiskError.textContent = ""; }
    if (
      reviewedWholeDisk.confirmation &&
      (Number(wholeDiskConfirmSlider?.value || 0) < 100 || !wholeDiskConfirmToggle?.checked)
    ) {
      if (wholeDiskError) { wholeDiskError.textContent = "Slide fully to the right, then switch Confirm on before applying."; wholeDiskError.hidden = false; }
      wholeDiskConfirmSlider?.focus();
      return;
    }
    if (reviewedWholeDisk.players_confirmation_required && !wholeDiskPlayers?.checked) {
      if (wholeDiskError) { wholeDiskError.textContent = "Confirm that online players may be interrupted before applying."; wholeDiskError.hidden = false; }
      wholeDiskPlayers?.focus();
      return;
    }
    wholeDiskApplying = true;
    wholeDiskApply.disabled = true;
    if (wholeDiskClose) wholeDiskClose.disabled = true;
    if (wholeDiskCancel) wholeDiskCancel.disabled = true;
    wholeDiskApply.textContent = "Applying…";
    try {
      const payload = await postWholeDisk("apply");
      if (payload.operation) {
        wholeDiskApplying = false;
        if (wholeDiskClose) wholeDiskClose.disabled = false;
        if (wholeDiskCancel) wholeDiskCancel.disabled = false;
        wholeDiskDialog?.close();
        if (migrationDialog && !migrationDialog.open) migrationDialog.showModal();
        showMigrationProgress(payload.operation);
      } else if (payload.redirect) {
        window.location.assign(payload.redirect);
      } else {
        window.location.reload();
      }
    } catch (error) {
      wholeDiskApplying = false;
      if (wholeDiskError) { wholeDiskError.textContent = error.message; wholeDiskError.hidden = false; }
      if (wholeDiskClose) wholeDiskClose.disabled = false;
      if (wholeDiskCancel) wholeDiskCancel.disabled = false;
      wholeDiskApply.disabled = false;
      wholeDiskApply.textContent = "Apply destructive action";
    }
  });


  freeSpaceButtons.forEach((button) => {
    button.addEventListener("click", () => {
      if (!freeDetailDialog) return;
      closeActionMenu();
      closeFreeActionMenu();
      selectedFreeSpace = { ...button.dataset };
      reviewedCreatePartition = null;
      if (freeDetailDevice) freeDetailDevice.textContent = selectedFreeSpace.device || "";
      if (freeDetailSize) freeDetailSize.textContent = selectedFreeSpace.size || "Unknown";
      freeDetailDialog.showModal();
      freeDetailClose?.focus();
    });
  });

  function closeFreeDetailDialog() {
    closeFreeActionMenu();
    freeDetailDialog?.close();
  }

  freeDetailClose?.addEventListener("click", closeFreeDetailDialog);
  freeDetailDialog?.addEventListener("click", (event) => { if (event.target === freeDetailDialog) closeFreeDetailDialog(); });
  freeDetailDialog?.addEventListener("close", closeFreeActionMenu);
  freeDetailDialog?.addEventListener("cancel", closeFreeActionMenu);

  function renderCreateWarnings(warnings) {
    if (!createWarnings) return;
    createWarnings.replaceChildren();
    (warnings || []).forEach((message) => {
      const notice = document.createElement("div");
      notice.className = "notice warning";
      notice.textContent = message;
      createWarnings.appendChild(notice);
    });
  }

  function resetCreateReviewState() {
    reviewedCreatePartition = null;
    if (createReviewPanel) createReviewPanel.hidden = true;
    renderCreateWarnings([]);
    resetConfirmationControl(
      createConfirmationField,
      createConfirmationSummary,
      createConfirmSlider,
      createConfirmSliderShell,
      createConfirmSliderText,
      createConfirmToggleRow,
      createConfirmToggle,
      "Slide to confirm partition creation",
    );
    if (createReview) {
      createReview.hidden = false;
      createReview.disabled = false;
      createReview.textContent = "Review action";
    }
    if (createApply) {
      createApply.hidden = true;
      createApply.disabled = false;
      createApply.textContent = "Apply destructive action";
    }
  }

  function resetCreateDialog() {
    if (!selectedFreeSpace) return;
    if (createDevice) createDevice.textContent = selectedFreeSpace.device || "";
    if (createAvailable) createAvailable.textContent = selectedFreeSpace.size || "Unknown";
    if (createUseAll) createUseAll.checked = true;
    if (createSize) {
      createSize.value = "";
      createSize.disabled = true;
      const availableGiB = Math.floor(Number(selectedFreeSpace.sizeBytes || 0) / (1024 * 1024 * 1024));
      if (availableGiB > 0) createSize.max = String(availableGiB);
      else createSize.removeAttribute("max");
    }
    if (createError) {
      createError.hidden = true;
      createError.textContent = "";
    }
    resetCreateReviewState();
  }

  function createSizeGiB() {
    if (createUseAll?.checked) return "all";
    const value = Number(createSize?.value || 0);
    if (!Number.isInteger(value) || value < 1) return "";
    return String(value);
  }

  createUseAll?.addEventListener("change", () => {
    if (createSize) {
      createSize.disabled = Boolean(createUseAll.checked);
      if (!createUseAll.checked) createSize.focus();
    }
    resetCreateReviewState();
  });
  createSize?.addEventListener("input", resetCreateReviewState);

  createPartitionButton?.addEventListener("click", () => {
    if (!selectedFreeSpace || !createDialog) return;
    closeFreeActionMenu();
    freeDetailDialog?.close();
    resetCreateDialog();
    createDialog.showModal();
    createReview?.focus();
  });

  function closeCreateDialog() {
    if (createApplying) return;
    createDialog?.close();
  }

  createClose?.addEventListener("click", closeCreateDialog);
  createCancel?.addEventListener("click", closeCreateDialog);
  createDialog?.addEventListener("click", (event) => { if (event.target === createDialog) closeCreateDialog(); });
  createDialog?.addEventListener("cancel", (event) => { if (createApplying) event.preventDefault(); });

  async function postCreatePartition(phase) {
    const sizeGiB = createSizeGiB();
    if (!sizeGiB) throw new Error("Enter a whole-number partition size in GiB.");
    const body = new URLSearchParams();
    body.set("csrf", csrf);
    body.set("operation", "create_partition");
    body.set("device", selectedFreeSpace?.device || "");
    body.set("free_start", selectedFreeSpace?.start || "");
    body.set("size_gib", sizeGiB);
    if (reviewedCreatePartition?.fingerprint) body.set("fingerprint", reviewedCreatePartition.fingerprint);
    if (
      phase === "apply" &&
      reviewedCreatePartition?.confirmation &&
      Number(createConfirmSlider?.value || 0) >= 100 &&
      createConfirmToggle?.checked
    ) {
      body.set("confirmation", reviewedCreatePartition.confirmation);
    }

    const response = await fetch("/api/new-storage/actions/" + phase, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: body.toString(),
      credentials: "same-origin",
    });
    let payload = null;
    try { payload = await response.json(); } catch (_) { payload = null; }
    if (!response.ok || !payload?.ok) throw new Error(payload?.error || "Partition creation could not be completed.");
    return payload;
  }

  createReview?.addEventListener("click", async () => {
    if (!selectedFreeSpace) return;
    if (createError) {
      createError.hidden = true;
      createError.textContent = "";
    }
    createReview.disabled = true;
    createReview.textContent = "Reviewing…";
    try {
      const payload = await postCreatePartition("plan");
      reviewedCreatePartition = payload.proposed || null;
      if (!reviewedCreatePartition) throw new Error("Partition creation returned an incomplete plan.");
      if (createReviewPanel) createReviewPanel.hidden = false;
      const requested = createSizeGiB();
      const plannedSize = requested === "all" ? (selectedFreeSpace.size || "available space") : requested + " GiB";
      if (createAfter) createAfter.textContent = "New " + plannedSize + " XFS partition";
      renderCreateWarnings(payload.warnings);
      const confirmation = reviewedCreatePartition.confirmation || "";
      resetConfirmationControl(
        createConfirmationField,
        createConfirmationSummary,
        createConfirmSlider,
        createConfirmSliderShell,
        createConfirmSliderText,
        createConfirmToggleRow,
        createConfirmToggle,
        "Slide to confirm partition creation",
      );
      if (createConfirmationSummary) {
        createConfirmationSummary.textContent =
          (reviewedCreatePartition.device || selectedFreeSpace.device || "Selected disk") +
          " will get a new " + plannedSize + " XFS partition";
      }
      if (createConfirmationField) createConfirmationField.hidden = !confirmation;
      createReview.hidden = true;
      if (createApply) {
        createApply.hidden = false;
        createApply.disabled = Boolean(confirmation);
      }
      if (confirmation) createConfirmSlider?.focus();
      else createApply?.focus();
    } catch (error) {
      if (createError) {
        createError.textContent = error.message;
        createError.hidden = false;
      }
    } finally {
      createReview.disabled = false;
      createReview.textContent = "Review action";
    }
  });

  function updateCreateApplyState() {
    if (!createApply || !reviewedCreatePartition) return;
    const needsConfirmation = Boolean(reviewedCreatePartition.confirmation);
    const requested = createSizeGiB();
    const plannedSize = requested === "all" ? (selectedFreeSpace?.size || "available space") : requested + " GiB";
    const sliderArmed = confirmationSliderArmed(
      createConfirmSlider,
      createConfirmSliderShell,
      createConfirmSliderText,
      (reviewedCreatePartition.device || selectedFreeSpace?.device || "Selected disk") +
        " will get a new " + plannedSize + " XFS partition",
      "Slide to confirm partition creation",
    );
    if (createConfirmToggleRow) createConfirmToggleRow.hidden = !needsConfirmation || !sliderArmed;
    if (!sliderArmed && createConfirmToggle) createConfirmToggle.checked = false;
    createApply.disabled = needsConfirmation && !(sliderArmed && Boolean(createConfirmToggle?.checked));
  }

  createConfirmSlider?.addEventListener("input", updateCreateApplyState);
  createConfirmToggle?.addEventListener("change", updateCreateApplyState);

  createApply?.addEventListener("click", async () => {
    if (!reviewedCreatePartition) return;
    if (createError) {
      createError.hidden = true;
      createError.textContent = "";
    }
    if (
      reviewedCreatePartition.confirmation &&
      (Number(createConfirmSlider?.value || 0) < 100 || !createConfirmToggle?.checked)
    ) {
      if (createError) {
        createError.textContent = "Slide fully to the right, then switch Confirm on before applying.";
        createError.hidden = false;
      }
      createConfirmSlider?.focus();
      return;
    }

    createApplying = true;
    createApply.disabled = true;
    if (createClose) createClose.disabled = true;
    if (createCancel) createCancel.disabled = true;
    createApply.textContent = "Applying…";
    try {
      await postCreatePartition("apply");
      window.location.reload();
    } catch (error) {
      createApplying = false;
      if (createError) {
        createError.textContent = error.message;
        createError.hidden = false;
      }
      if (createClose) createClose.disabled = false;
      if (createCancel) createCancel.disabled = false;
      createApply.disabled = false;
      createApply.textContent = "Apply destructive action";
    }
  });

  function minecraftDataPath(mountPoint) {
    return (mountPoint || "/var/mnt/justvoxel-data").replace(/\/$/, "") + "/minecraft";
  }

  function migrationBytes(value) {
    const bytes = Number(value || 0);
    if (!Number.isFinite(bytes) || bytes <= 0) return "Unknown";
    const gib = bytes / (1024 * 1024 * 1024);
    if (gib >= 1) return gib.toFixed(gib >= 10 ? 1 : 2) + " GiB";
    return (bytes / (1024 * 1024)).toFixed(0) + " MiB";
  }

  function migrationStateLabel(state) {
    if (state === "queued") return "Queued";
    if (state === "validating") return "Validating";
    if (state === "running") return "Migrating";
    if (state === "verifying") return "Runtime validation";
    if (state === "failed") return "Recovering";
    if (state === "rolling_back") return "Rolling back";
    if (state === "rolled_back") return "Rolled back";
    if (state === "needs_attention") return "Needs attention";
    if (state === "succeeded") return "Complete";
    return "Working";
  }

  function migrationStageLabel(stage) {
    if (stage === "queued") return "Waiting to start";
    if (stage === "migration_preflight" || stage === "target_recheck") return "Checking reviewed migration";
    if (stage === "target_prepare") return "Preparing target storage";
    if (stage === "staging_space") return "Checking free space";
    if (stage === "player_recheck") return "Rechecking players";
    if (stage === "minecraft_stop") return "Stopping Minecraft safely";
    if (stage === "cold_backup") return "Creating verified backup";
    if (stage === "copying") return "Copying Minecraft data";
    if (stage === "copy_verification") return "Verifying copied data";
    if (stage === "switching") return "Switching Minecraft data storage";
    if (stage === "minecraft_runtime") return "Validating migrated Minecraft";
    if (stage === "migrated_storage") return "Validating migrated storage";
    if (stage === "migration_failed") return "Preparing recovery";
    if (stage === "rollback") return "Restoring previous configuration";
    if (stage === "migration_rolled_back") return "Rolled back";
    if (["migration_needs_attention","migration_backend_interrupted","migration_backend_incomplete","invalid_execution_plan","interrupted"].includes(stage)) return "Administrator attention required";
    if (stage === "completed") return "Complete";
    return "Working";
  }

  function migrationTerminal(state) {
    return ["succeeded", "rolled_back", "needs_attention"].includes(state);
  }

  function stopMigrationPolling() {
    if (migrationPollTimer) window.clearTimeout(migrationPollTimer);
    migrationPollTimer = null;
  }

  async function migrationJSON(url, options = {}) {
    const response = await fetch(url, {
      credentials: "same-origin",
      cache: "no-store",
      ...options,
      headers: {
        Accept: "application/json",
        ...(options.headers || {}),
      },
    });
    let payload = {};
    try { payload = await response.json(); } catch (_) { payload = {}; }
    if (response.status === 401) {
      window.location.assign("/login");
      return null;
    }
    if (response.status === 403 && String(payload.error || "").toLowerCase().includes("password change")) {
      window.location.assign("/password");
      return null;
    }
    if (!response.ok || !payload?.ok) {
      const error = new Error(payload?.error || "Minecraft data migration could not be completed.");
      error.payload = payload;
      error.status = response.status;
      throw error;
    }
    return payload;
  }

  function migrationRequestBody() {
    const body = new URLSearchParams();
    body.set("csrf", csrf);
    body.set("operation", "use_partition");
    body.set("device", migrationDevice?.value || "");
    body.set("mount_point", migrationMount?.value.trim() || "");
    body.set("path", migrationPath?.value.trim() || "");
    body.set("size_gib", "all");
    return body;
  }

  function resetMigrationDialog() {
    stopMigrationPolling();
    reviewedMigrationPlan = null;
    migrationApplying = false;
    migrationPollFailures = 0;
    if (migrationTitle) migrationTitle.textContent = "Use this filesystem";
    if (migrationSetup) migrationSetup.hidden = false;
    if (migrationReviewPanel) migrationReviewPanel.hidden = true;
    if (migrationProgress) migrationProgress.hidden = true;
    if (migrationError) { migrationError.hidden = true; migrationError.textContent = ""; }
    if (migrationReviewError) { migrationReviewError.hidden = true; migrationReviewError.textContent = ""; }
    if (migrationWarnings) migrationWarnings.replaceChildren();
    if (migrationPlayersField) migrationPlayersField.hidden = true;
    if (migrationPlayers) migrationPlayers.checked = false;
    if (migrationPlayersNote) migrationPlayersNote.textContent = "";
    if (migrationDestructiveField) migrationDestructiveField.hidden = true;
    if (migrationDestructivePhrase) migrationDestructivePhrase.textContent = "";
    if (migrationDestructive) migrationDestructive.value = "";
    if (migrationConfirmation) migrationConfirmation.value = "";
    if (migrationApply) migrationApply.disabled = true;
    if (migrationRefresh) migrationRefresh.hidden = true;
  }

  function renderMigrationWarnings(warnings) {
    if (!migrationWarnings) return;
    migrationWarnings.replaceChildren();
    (warnings || []).forEach((warning) => {
      const notice = document.createElement("div");
      notice.className = "notice warning";
      notice.textContent = warning.message || warning.Message || String(warning);
      migrationWarnings.appendChild(notice);
    });
  }

  function updateMigrationApplyState() {
    if (!migrationApply || !reviewedMigrationPlan) return;
    const requirements = reviewedMigrationPlan.requirements || {};
    const migrateReady = (migrationConfirmation?.value.trim() || "") === "MIGRATE";
    const playersReady = !requirements.players_confirmation_required || Boolean(migrationPlayers?.checked);
    const phrase = requirements.confirmation_phrase || "";
    const destructiveReady = !requirements.destructive_confirmation_required || (migrationDestructive?.value.trim() || "") === phrase;
    migrationApply.disabled = !(migrateReady && playersReady && destructiveReady);
  }

  function renderMigrationPlan(plan) {
    reviewedMigrationPlan = plan;
    const normalized = plan.normalized || {};
    const requirements = plan.requirements || {};
    if (migrationSetup) migrationSetup.hidden = true;
    if (migrationReviewPanel) migrationReviewPanel.hidden = false;
    if (migrationProgress) migrationProgress.hidden = true;
    if (migrationTitle) migrationTitle.textContent = "Review Minecraft data migration";
    if (migrationCurrentPath) migrationCurrentPath.textContent = normalized.current_data_path || "Unknown";
    if (migrationReviewDevice) migrationReviewDevice.textContent = normalized.device || "";
    if (migrationReviewFilesystem) migrationReviewFilesystem.textContent = normalized.filesystem || "Unknown";
    if (migrationReviewMount) migrationReviewMount.textContent = normalized.mount_point || "";
    if (migrationReviewPath) migrationReviewPath.textContent = normalized.path || "";
    if (migrationReviewCapacity) migrationReviewCapacity.textContent = migrationBytes(normalized.target_capacity_bytes);
    renderMigrationWarnings(plan.warnings);
    const playersRequired = Boolean(requirements.players_confirmation_required);
    if (migrationPlayersField) migrationPlayersField.hidden = !playersRequired;
    if (migrationPlayers) migrationPlayers.checked = false;
    if (migrationPlayersNote) {
      const names = (requirements.players || []).join(", ");
      migrationPlayersNote.textContent = playersRequired ? (names ? "Online: " + names : String(requirements.online || 0) + " player(s) online.") : "";
    }
    const destructiveRequired = Boolean(requirements.destructive_confirmation_required);
    if (migrationDestructiveField) migrationDestructiveField.hidden = !destructiveRequired;
    if (migrationDestructivePhrase) migrationDestructivePhrase.textContent = requirements.confirmation_phrase || "";
    if (migrationDestructive) migrationDestructive.value = "";
    if (migrationConfirmation) migrationConfirmation.value = "";
    if (migrationReviewError) { migrationReviewError.hidden = true; migrationReviewError.textContent = ""; }
    updateMigrationApplyState();
    migrationConfirmation?.focus();
  }

  function showMigrationProgress(operation) {
    if (!operation || operation.operation_type !== "data_migration") return;
    reviewedMigrationPlan = null;
    if (migrationSetup) migrationSetup.hidden = true;
    if (migrationReviewPanel) migrationReviewPanel.hidden = true;
    if (migrationProgress) migrationProgress.hidden = false;
    if (migrationTitle) migrationTitle.textContent = "Minecraft data migration";
    if (migrationProgressState) migrationProgressState.textContent = migrationStateLabel(operation.state);
    if (migrationProgressStage) migrationProgressStage.textContent = migrationStageLabel(operation.stage);
    if (migrationProgressStatus) migrationProgressStatus.textContent = operation.status || "Minecraft data migration is running.";
    if (migrationProgressID) migrationProgressID.textContent = operation.operation_id || "";
    if (migrationReconnect) migrationReconnect.hidden = true;
    if (migrationSuccess) migrationSuccess.hidden = operation.state !== "succeeded";
    if (migrationRollback) migrationRollback.hidden = operation.state !== "rolled_back";
    if (migrationAttention) migrationAttention.hidden = operation.state !== "needs_attention";
    const terminal = migrationTerminal(operation.state);
    if (migrationRefresh) migrationRefresh.hidden = !terminal;
    stopMigrationPolling();
    if (!terminal && operation.operation_id) {
      migrationPollFailures = 0;
      migrationPollTimer = window.setTimeout(() => pollMigrationProgress(operation.operation_id), 2500);
    }
  }

  async function pollMigrationProgress(operationID) {
    try {
      const payload = await migrationJSON("/api/new-storage/minecraft-data/progress/" + encodeURIComponent(operationID));
      if (!payload) return;
      migrationPollFailures = 0;
      showMigrationProgress(payload.operation);
    } catch (_) {
      migrationPollFailures += 1;
      if (migrationPollFailures >= 2 && migrationReconnect) migrationReconnect.hidden = false;
      migrationPollTimer = window.setTimeout(() => pollMigrationProgress(operationID), 2500);
    }
  }

  async function showCurrentMigrationIfAny() {
    try {
      const payload = await migrationJSON("/api/new-storage/minecraft-data/current");
      if (payload?.operation) {
        showMigrationProgress(payload.operation);
        return true;
      }
    } catch (error) {
      if (migrationError) {
        migrationError.textContent = error.message;
        migrationError.hidden = false;
      }
    }
    return false;
  }

  function closeMigrationDialog() {
    if (migrationApplying) return;
    stopMigrationPolling();
    migrationDialog?.close();
  }

  migrateButton?.addEventListener("click", async () => {
    if (!selectedPartition || selectedPartition.minecraftCandidate !== "Yes" || !migrationDialog) return;
    closeActionMenu();
    detailDialog?.close();
    resetMigrationDialog();

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
        ? "This filesystem is already mounted. Review will verify this exact mount point."
        : "This filesystem is not mounted. The migration backend can create a permanent mount at this location.";
    }
    if (migrationPath) migrationPath.value = minecraftDataPath(mountPoint);
    migrationDialog.showModal();
    const active = await showCurrentMigrationIfAny();
    if (!active) migrationPath?.focus();
  });

  migrationReview?.addEventListener("click", async () => {
    if (migrationError) { migrationError.hidden = true; migrationError.textContent = ""; }
    migrationReview.disabled = true;
    migrationReview.textContent = "Reviewing…";
    try {
      const body = migrationRequestBody();
      const payload = await migrationJSON("/api/new-storage/minecraft-data/plan", {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body: body.toString(),
      });
      if (payload?.plan) renderMigrationPlan(payload.plan);
    } catch (error) {
      if (error.payload?.operation) {
        showMigrationProgress(error.payload.operation);
      } else if (migrationError) {
        migrationError.textContent = error.message;
        migrationError.hidden = false;
      }
    } finally {
      migrationReview.disabled = false;
      migrationReview.textContent = "Review migration";
    }
  });

  migrationApply?.addEventListener("click", async () => {
    if (!reviewedMigrationPlan || migrationApplying) return;
    updateMigrationApplyState();
    if (migrationApply.disabled) return;
    migrationApplying = true;
    migrationApply.disabled = true;
    if (migrationClose) migrationClose.disabled = true;
    if (migrationBack) migrationBack.disabled = true;
    migrationApply.textContent = "Starting…";
    try {
      const body = migrationRequestBody();
      body.set("plan_fingerprint", reviewedMigrationPlan.plan_fingerprint || "");
      body.set("migration_confirmation", migrationConfirmation?.value.trim() || "");
      if (migrationPlayers?.checked) body.set("players_confirmed", "yes");
      if (!(migrationDestructiveField?.hidden)) body.set("destructive_confirmation", migrationDestructive?.value.trim() || "");
      const payload = await migrationJSON("/api/new-storage/minecraft-data/apply", {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body: body.toString(),
      });
      if (payload?.operation) showMigrationProgress(payload.operation);
    } catch (error) {
      if (error.payload?.plan) renderMigrationPlan(error.payload.plan);
      if (migrationReviewError) {
        migrationReviewError.textContent = error.message;
        migrationReviewError.hidden = false;
      }
    } finally {
      migrationApplying = false;
      if (migrationClose) migrationClose.disabled = false;
      if (migrationBack) migrationBack.disabled = false;
      migrationApply.textContent = "Start migration";
      updateMigrationApplyState();
    }
  });

  migrationMount?.addEventListener("input", () => {
    if (!migrationMount.readOnly && migrationPath) migrationPath.value = minecraftDataPath(migrationMount.value.trim());
  });
  migrationConfirmation?.addEventListener("input", updateMigrationApplyState);
  migrationDestructive?.addEventListener("input", updateMigrationApplyState);
  migrationPlayers?.addEventListener("change", updateMigrationApplyState);
  migrationBack?.addEventListener("click", () => {
    reviewedMigrationPlan = null;
    if (migrationSetup) migrationSetup.hidden = false;
    if (migrationReviewPanel) migrationReviewPanel.hidden = true;
    if (migrationTitle) migrationTitle.textContent = "Use this filesystem";
    migrationPath?.focus();
  });
  migrationClose?.addEventListener("click", closeMigrationDialog);
  migrationCancel?.addEventListener("click", closeMigrationDialog);
  migrationProgressClose?.addEventListener("click", closeMigrationDialog);
  migrationRefresh?.addEventListener("click", () => window.location.reload());
  migrationDialog?.addEventListener("click", (event) => { if (event.target === migrationDialog) closeMigrationDialog(); });
  migrationDialog?.addEventListener("cancel", (event) => { if (migrationApplying) event.preventDefault(); else stopMigrationPolling(); });

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
    resetConfirmationControl(
      confirmationField,
      confirmationSummary,
      confirmSlider,
      confirmSliderShell,
      confirmSliderText,
      confirmToggleRow,
      confirmToggle,
      "Slide to confirm formatting",
    );
    if (actionError) { actionError.hidden = true; actionError.textContent = ""; }
    if (reviewButton) { reviewButton.hidden = false; reviewButton.disabled = false; reviewButton.textContent = "Review action"; }
    if (applyButton) { applyButton.hidden = true; applyButton.disabled = false; applyButton.textContent = "Apply action"; }
  }

  actionButtons.forEach((button) => {
    button.addEventListener("click", () => {
      if (!selectedPartition || !actionDialog) return;
      closeActionMenu();
      resetActionDialog(button.dataset.storageAction);
      actionDialog.showModal();
      if (selectedAction === "mount-for-now" || selectedAction === "mount-permanently") mountInput?.focus();
      else reviewButton?.focus();
    });
  });

  function closeActionDialog() {
    if (actionApplying) return;
    actionDialog?.close();
  }
  actionClose?.addEventListener("click", closeActionDialog);
  actionCancel?.addEventListener("click", closeActionDialog);
  actionDialog?.addEventListener("click", (event) => { if (event.target === actionDialog) closeActionDialog(); });
  actionDialog?.addEventListener("cancel", (event) => { if (actionApplying) event.preventDefault(); });

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
      const destructiveSummary = (reviewedPlan.device || selectedPartition.path) + " will be formatted";
      resetConfirmationControl(
        confirmationField,
        confirmationSummary,
        confirmSlider,
        confirmSliderShell,
        confirmSliderText,
        confirmToggleRow,
        confirmToggle,
        "Slide to confirm formatting",
      );
      if (confirmationSummary) confirmationSummary.textContent = destructiveSummary;
      if (confirmationField) confirmationField.hidden = !needsConfirmation;
      reviewButton.hidden = true;
      if (applyButton) {
        applyButton.hidden = false;
        applyButton.disabled = needsConfirmation;
        applyButton.textContent = reviewedPlan.destructive ? "Apply destructive action" : "Apply action";
      }
      if (needsConfirmation) confirmSlider?.focus();
      else applyButton?.focus();
    } catch (error) {
      if (actionError) { actionError.textContent = error.message; actionError.hidden = false; }
    } finally {
      reviewButton.disabled = false;
      reviewButton.textContent = "Review action";
    }
  });

  function updateActionApplyState() {
    if (!applyButton || !reviewedPlan) return;
    const needsConfirmation = Boolean(reviewedPlan.confirmation);
    const sliderArmed = confirmationSliderArmed(
      confirmSlider,
      confirmSliderShell,
      confirmSliderText,
      (reviewedPlan.device || selectedPartition?.path || "Selected partition") + " will be formatted",
      "Slide to confirm formatting",
    );
    if (confirmToggleRow) confirmToggleRow.hidden = !needsConfirmation || !sliderArmed;
    if (!sliderArmed && confirmToggle) confirmToggle.checked = false;
    applyButton.disabled = needsConfirmation && !(sliderArmed && Boolean(confirmToggle?.checked));
  }

  confirmSlider?.addEventListener("input", updateActionApplyState);
  confirmToggle?.addEventListener("change", updateActionApplyState);

  applyButton?.addEventListener("click", async () => {
    if (!reviewedPlan) return;
    if (actionError) { actionError.hidden = true; actionError.textContent = ""; }
    const expected = reviewedPlan.confirmation || "";
    const confirmationReady = Number(confirmSlider?.value || 0) >= 100 && Boolean(confirmToggle?.checked);
    if (expected && !confirmationReady) {
      if (actionError) { actionError.textContent = "Slide fully to the right, then switch Confirm on before applying."; actionError.hidden = false; }
      confirmSlider?.focus();
      return;
    }
    const entered = expected && confirmationReady ? expected : "";
    actionApplying = true;
    applyButton.disabled = true;
    if (actionClose) actionClose.disabled = true;
    if (actionCancel) actionCancel.disabled = true;
    applyButton.textContent = "Applying…";
    try {
      await postAction("apply", {
        fingerprint: reviewedPlan.fingerprint,
        confirmation: entered,
      });
      window.location.reload();
    } catch (error) {
      actionApplying = false;
      if (actionError) { actionError.textContent = error.message; actionError.hidden = false; }
      if (actionClose) actionClose.disabled = false;
      if (actionCancel) actionCancel.disabled = false;
      applyButton.disabled = false;
      applyButton.textContent = reviewedPlan.destructive ? "Apply destructive action" : "Apply action";
    }
  });
  }

  window.JustVoxelStorageBrowser = { init: initStorageBrowser };
  const initialStorageBrowser = document.querySelector("[data-storage-browser-root]");
  if (initialStorageBrowser) initStorageBrowser(initialStorageBrowser);
})();
