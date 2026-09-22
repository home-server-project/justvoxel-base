(() => {
  const checkboxes = [...document.querySelectorAll("[data-backup-select]")];
  const selectedCount = document.querySelector("[data-backup-selected-count]");
  const deleteButton = document.querySelector("[data-backup-delete-selected]");
  const csrf = document.querySelector("[data-backup-delete-csrf]")?.value || "";
  const pageError = document.querySelector("[data-backup-delete-page-error]");

  const dialog = document.querySelector("[data-backup-delete-dialog]");
  const closeButton = dialog?.querySelector("[data-backup-delete-close]");
  const cancelButton = dialog?.querySelector("[data-backup-delete-cancel]");
  const applyButton = dialog?.querySelector("[data-backup-delete-apply]");
  const summary = dialog?.querySelector("[data-backup-delete-summary]");
  const reviewList = dialog?.querySelector("[data-backup-delete-review-list]");
  const warnings = dialog?.querySelector("[data-backup-delete-warnings]");
  const phrase = dialog?.querySelector("[data-backup-delete-confirmation-phrase]");
  const confirmation = dialog?.querySelector("[data-backup-delete-confirmation]");
  const dialogError = dialog?.querySelector("[data-backup-delete-dialog-error]");

  let reviewedPlan = null;
  let reviewedIDs = [];

  function selectedIDs() {
    return checkboxes.filter((box) => box.checked).map((box) => box.value);
  }

  function updateSelection() {
    const count = selectedIDs().length;
    if (selectedCount) selectedCount.textContent = String(count);
    if (deleteButton) deleteButton.disabled = count === 0;
  }

  checkboxes.forEach((box) => box.addEventListener("change", updateSelection));
  updateSelection();

  function showError(node, message) {
    if (!node) return;
    node.textContent = message;
    node.hidden = false;
  }

  function clearError(node) {
    if (!node) return;
    node.textContent = "";
    node.hidden = true;
  }

  function formatBytes(bytes) {
    const value = Number(bytes || 0);
    if (!Number.isFinite(value) || value <= 0) return "0 B";
    const units = ["B", "KiB", "MiB", "GiB", "TiB"];
    let amount = value;
    let unit = 0;
    while (amount >= 1024 && unit < units.length - 1) {
      amount /= 1024;
      unit += 1;
    }
    return (unit === 0 ? amount.toFixed(0) : amount.toFixed(1)) + " " + units[unit];
  }

  async function postDelete(path, ids, extra = {}) {
    const body = new URLSearchParams();
    body.set("csrf", csrf);
    ids.forEach((id) => body.append("backup_id", id));
    Object.entries(extra).forEach(([key, value]) => body.set(key, value || ""));

    const response = await fetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: body.toString(),
      credentials: "same-origin",
    });
    let payload = null;
    try {
      payload = await response.json();
    } catch (_) {
      payload = null;
    }
    if (!response.ok || !payload?.ok) {
      throw new Error(payload?.error || "Backup deletion could not be completed.");
    }
    return payload;
  }

  function renderPlan(payload) {
    reviewedPlan = payload.proposed;
    if (summary) {
      const noun = reviewedPlan.count === 1 ? "backup" : "backups";
      summary.textContent = "Delete " + reviewedPlan.count + " " + noun + " using " + formatBytes(reviewedPlan.total_size_bytes) + "?";
    }
    if (reviewList) {
      reviewList.replaceChildren();
      reviewedPlan.backups.forEach((backup) => {
        const row = document.createElement("div");
        row.className = "backup-delete-review-item";
        const name = document.createElement("code");
        name.textContent = backup.id;
        const size = document.createElement("span");
        size.textContent = formatBytes(backup.size_bytes);
        row.append(name, size);
        reviewList.appendChild(row);
      });
    }
    if (warnings) {
      warnings.replaceChildren();
      (payload.warnings || []).forEach((message) => {
        const notice = document.createElement("div");
        notice.className = "notice warning";
        notice.textContent = message;
        warnings.appendChild(notice);
      });
    }
    if (phrase) phrase.textContent = reviewedPlan.confirmation || "";
    if (confirmation) confirmation.value = "";
    clearError(dialogError);
  }

  deleteButton?.addEventListener("click", async () => {
    const ids = selectedIDs();
    if (ids.length === 0 || !dialog) return;

    clearError(pageError);
    deleteButton.disabled = true;
    const originalLabel = deleteButton.textContent;
    deleteButton.textContent = "Reviewing…";
    try {
      const payload = await postDelete("/api/new-backups/delete/plan", ids);
      reviewedIDs = [...ids];
      renderPlan(payload);
      dialog.showModal();
      confirmation?.focus();
    } catch (error) {
      showError(pageError, error.message);
    } finally {
      deleteButton.textContent = originalLabel;
      updateSelection();
    }
  });

  function closeDialog() {
    reviewedPlan = null;
    reviewedIDs = [];
    clearError(dialogError);
    dialog?.close();
  }

  closeButton?.addEventListener("click", closeDialog);
  cancelButton?.addEventListener("click", closeDialog);

  applyButton?.addEventListener("click", async () => {
    if (!reviewedPlan || reviewedIDs.length === 0) return;
    const entered = confirmation?.value || "";
    if (entered !== reviewedPlan.confirmation) {
      showError(dialogError, "Type the confirmation phrase exactly before deleting.");
      confirmation?.focus();
      return;
    }

    clearError(dialogError);
    applyButton.disabled = true;
    const originalLabel = applyButton.textContent;
    applyButton.textContent = "Deleting…";
    try {
      const payload = await postDelete("/api/new-backups/delete/apply", reviewedIDs, {
        fingerprint: reviewedPlan.fingerprint,
        confirmation: entered,
      });
      const deleted = Number(payload.deleted || reviewedPlan.count || 0);
      window.location.assign("/settings/new-backups?result=deleted&count=" + encodeURIComponent(String(deleted)));
    } catch (error) {
      showError(dialogError, error.message);
      applyButton.disabled = false;
      applyButton.textContent = originalLabel;
    }
  });

  const destinationForm = document.querySelector("[data-backup-destination-form]");
  if (destinationForm) {
    const destinationType = destinationForm.querySelector("[data-backup-destination-type]");
    const destinationKinds = [...destinationForm.querySelectorAll("[data-backup-destination-kind]")];
    const destinationDevice = destinationForm.querySelector("[data-backup-destination-device]");
    const destinationInput = (name, kind) => destinationForm.querySelector(`[data-backup-destination-${name}="${kind}"]`);

    function updateDestinationPartition(force) {
      if (!destinationDevice?.value) return;
      const option = destinationDevice.selectedOptions[0];
      const mount = destinationInput("mount", "partition");
      const path = destinationInput("path", "partition");
      const mountedAt = option?.dataset.mountpoint || "";
      const targetMount = mountedAt || mount?.value || "/var/mnt/justvoxel-backup";
      if (mount && (force || !mount.value)) mount.value = targetMount;
      if (path && (force || !path.value)) path.value = targetMount.replace(/\/$/, "") + "/backups";
    }

    function updateDestinationNetwork(kind, force) {
      const mount = destinationInput("mount", kind);
      const path = destinationInput("path", kind);
      if (mount && !mount.value) mount.value = "/var/mnt/justvoxel-backup";
      if (path && (force || !path.value)) path.value = (mount?.value || "/var/mnt/justvoxel-backup").replace(/\/$/, "") + "/backups";
    }

    function showDestinationKind() {
      const kind = destinationType?.value || "system";
      destinationKinds.forEach((section) => {
        const active = section.dataset.backupDestinationKind === kind;
        section.hidden = !active;
        section.querySelectorAll("input,select").forEach((input) => { input.disabled = !active; });
      });
      if (kind === "partition") updateDestinationPartition(false);
      if (kind === "nfs" || kind === "smb") updateDestinationNetwork(kind, false);
    }

    destinationType?.addEventListener("change", () => {
      showDestinationKind();
      const kind = destinationType.value;
      if (kind === "partition") updateDestinationPartition(true);
      if (kind === "nfs" || kind === "smb") updateDestinationNetwork(kind, true);
    });
    destinationDevice?.addEventListener("change", () => updateDestinationPartition(true));
    ["nfs", "smb"].forEach((kind) => destinationInput("mount", kind)?.addEventListener("change", () => updateDestinationNetwork(kind, true)));
    showDestinationKind();
  }

})();
