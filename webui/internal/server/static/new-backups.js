(() => {
  function initNewBackups(root = document) {
    if (!root) return;
    if (root.dataset?.newBackupsInitialized === "true") return;
    if (root.dataset) root.dataset.newBackupsInitialized = "true";

    const checkboxes = [...root.querySelectorAll("[data-backup-select]")];
    const selectedCount = root.querySelector("[data-backup-selected-count]");
    const deleteButton = root.querySelector("[data-backup-delete-selected]");
    const restoreButton = root.querySelector("[data-backup-restore-selected]");
    const restoreBusy = restoreButton?.dataset.restoreBusy === "1";
    const csrf = root.querySelector("[data-backup-delete-csrf]")?.value || "";
    const pageError = root.querySelector("[data-backup-delete-page-error]");

    const dialog = root.querySelector("[data-backup-delete-dialog]");
    const closeButton = dialog?.querySelector("[data-backup-delete-close]");
    const cancelButton = dialog?.querySelector("[data-backup-delete-cancel]");
    const applyButton = dialog?.querySelector("[data-backup-delete-apply]");
    const summary = dialog?.querySelector("[data-backup-delete-summary]");
    const reviewList = dialog?.querySelector("[data-backup-delete-review-list]");
    const warnings = dialog?.querySelector("[data-backup-delete-warnings]");
    const confirmationControl = dialog?.querySelector("[data-backup-delete-confirmation-control]");
    const confirmation = dialog?.querySelector("[data-backup-delete-confirmation]");
    const confirmationSlider = dialog?.querySelector("[data-destructive-slider]");
    const confirmationToggle = dialog?.querySelector("[data-destructive-toggle]");
    const dialogError = dialog?.querySelector("[data-backup-delete-dialog-error]");

    let reviewedPlan = null;
    let reviewedIDs = [];
    let deleteApplying = false;

    function selectedIDs() {
      return checkboxes.filter((box) => box.checked).map((box) => box.value);
    }

    function updateSelection() {
      const count = selectedIDs().length;
      if (selectedCount) selectedCount.textContent = String(count);
      if (deleteButton) deleteButton.disabled = count === 0;
      if (restoreButton) restoreButton.disabled = count !== 1 || restoreBusy;
    }

    checkboxes.forEach((box) => box.addEventListener("change", updateSelection));
    updateSelection();

    const restoreDialog = root.querySelector("[data-backup-restore-dialog]");
    const restoreClose = restoreDialog?.querySelector("[data-backup-restore-close]");
    const restoreCancel = restoreDialog?.querySelector("[data-backup-restore-cancel]");
    const restoreID = restoreDialog?.querySelector("[data-backup-restore-id]");
    const restoreName = restoreDialog?.querySelector("[data-backup-restore-name]");

    function closeRestoreDialog() {
      restoreDialog?.close();
    }

    restoreButton?.addEventListener("click", () => {
      const ids = selectedIDs();
      if (ids.length !== 1 || !restoreDialog || restoreBusy) return;
      if (restoreID) restoreID.value = ids[0];
      if (restoreName) restoreName.textContent = ids[0];
      restoreDialog.showModal();
    });
    restoreClose?.addEventListener("click", closeRestoreDialog);
    restoreCancel?.addEventListener("click", closeRestoreDialog);
    restoreDialog?.addEventListener("click", (event) => {
      if (event.target === restoreDialog) closeRestoreDialog();
    });

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
      if (confirmationControl) confirmationControl.dataset.confirmValue = reviewedPlan.confirmation || "";
      if (confirmation) confirmation.value = "";
      if (confirmationSlider) confirmationSlider.value = "0";
      if (confirmationToggle) confirmationToggle.checked = false;
      confirmationControl?._justVoxelDestructiveSync?.();
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
        confirmationSlider?.focus();
      } catch (error) {
        showError(pageError, error.message);
      } finally {
        deleteButton.textContent = originalLabel;
        updateSelection();
      }
    });

    function closeDialog() {
      if (deleteApplying) return;
      reviewedPlan = null;
      reviewedIDs = [];
      clearError(dialogError);
      dialog?.close();
    }

    closeButton?.addEventListener("click", closeDialog);
    cancelButton?.addEventListener("click", closeDialog);
    dialog?.addEventListener("click", (event) => {
      if (event.target === dialog) closeDialog();
    });
    dialog?.addEventListener("cancel", (event) => {
      if (deleteApplying) event.preventDefault();
    });

    applyButton?.addEventListener("click", async () => {
      if (!reviewedPlan || reviewedIDs.length === 0) return;
      const entered = confirmation?.value || "";
      if (entered !== reviewedPlan.confirmation) {
        showError(dialogError, "Slide fully and use the Confirm toggle before deleting.");
        confirmationSlider?.focus();
        return;
      }

      clearError(dialogError);
      deleteApplying = true;
      applyButton.disabled = true;
      if (closeButton) closeButton.disabled = true;
      if (cancelButton) cancelButton.disabled = true;
      const originalLabel = applyButton.textContent;
      applyButton.textContent = "Deleting…";
      try {
        const payload = await postDelete("/api/new-backups/delete/apply", reviewedIDs, {
          fingerprint: reviewedPlan.fingerprint,
          confirmation: entered,
        });
        const deleted = Number(payload.deleted || reviewedPlan.count || 0);
        const url = "/settings/new-backups?result=deleted&count=" + encodeURIComponent(String(deleted));
        if (window.JustVoxelBackupsWorkspace?.reload) {
          await window.JustVoxelBackupsWorkspace.reload(url);
        } else {
          window.location.assign(url);
        }
      } catch (error) {
        deleteApplying = false;
        showError(dialogError, error.message);
        if (closeButton) closeButton.disabled = false;
        if (cancelButton) cancelButton.disabled = false;
        applyButton.disabled = false;
        applyButton.textContent = originalLabel;
      }
    });

    const destinationForm = root.querySelector("[data-backup-destination-form]");
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

    const reviewSections = [...root.querySelectorAll(".backup-restore-review,.backup-automatic-review,.backup-destination-review")];
    reviewSections.forEach((section) => {
      if (section.closest("[data-backup-review-dialog]")) return;
      const reviewDialog = document.createElement("dialog");
      reviewDialog.className = "backup-workflow-review-dialog";
      reviewDialog.dataset.backupReviewDialog = "true";
      const card = document.createElement("div");
      card.className = "backup-workflow-review-card";
      const close = document.createElement("button");
      close.className = "backup-workflow-review-close";
      close.dataset.backupWorkflowReviewClose = "true";
      close.type = "button";
      close.setAttribute("aria-label", "Close review");
      close.textContent = "×";
      close.addEventListener("click", () => { if (reviewDialog.dataset.busy !== "true") reviewDialog.close(); });
      card.append(close);
      section.replaceWith(reviewDialog);
      card.append(section);
      reviewDialog.append(card);
      root.append(reviewDialog);
      reviewDialog.addEventListener("click", (event) => {
        if (event.target === reviewDialog && reviewDialog.dataset.busy !== "true") reviewDialog.close();
      });
      reviewDialog.addEventListener("cancel", (event) => {
        if (reviewDialog.dataset.busy === "true") event.preventDefault();
      });
      reviewDialog.showModal();
    });
  }

  window.JustVoxelNewBackups = { init: initNewBackups };
  const initialRoot = document.querySelector("[data-backups-workspace-root]") || document.querySelector("main.new-backups-shell");
  if (initialRoot) initNewBackups(initialRoot);
})();
