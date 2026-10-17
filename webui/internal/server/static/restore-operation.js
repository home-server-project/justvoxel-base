(() => {
  function initRestoreOperation(root = document) {
    const panel = root?.querySelector("[data-selected-restore]");
    if (!panel || panel.dataset.restoreInitialized === "true") return;
    panel.dataset.restoreInitialized = "true";
    const selectedID = panel.dataset.selectedServerId;
    const statusURL = new URL(panel.dataset.statusUrl, window.location.href);
    if (!selectedID || statusURL.origin !== window.location.origin ||
        statusURL.pathname !== "/workspace/backups/restore/status" ||
        statusURL.searchParams.getAll("server").length !== 1 ||
        statusURL.searchParams.get("server") !== selectedID) return;
    const node = (name) => panel.querySelector(`[data-restore-${name}]`);
    let failures = 0;
    const poll = async () => {
      if (!panel.isConnected) return;
      let finished = false;
      try {
        const response = await fetch(statusURL, {
          method: "GET", credentials: "same-origin",
          headers: { Accept: "application/json" }, cache: "no-store",
        });
        if (response.status === 401) { window.location.assign("/login"); return; }
        if (!response.ok) throw new Error("Restore status unavailable");
        const progress = await response.json();
        // Never render another server's status or enable its recovery controls.
        if (progress.id !== selectedID) throw new Error("Restore identity mismatch");
        for (const field of ["state_label", "stage_label", "status"]) {
          if (typeof progress[field] !== "string") throw new Error("Restore status unavailable");
        }
        failures = 0;
        node("reconnect").hidden = true;
        node("state").textContent = progress.state_label;
        node("stage").textContent = progress.stage_label;
        node("status").textContent = progress.status;
        node("success").hidden = progress.complete !== true;
        node("rollback").hidden = progress.rolled_back !== true;
        node("attention").hidden = progress.attention !== true;
        node("recovery").hidden = progress.recovery_required !== true;
        node("dashboard").hidden = progress.complete !== true;
        finished = progress.terminal === true;
      } catch (_) {
        // A failed read must never leave a recovery action enabled on stale evidence.
        node("recovery").hidden = true;
        failures += 1;
        if (failures >= 2) node("reconnect").hidden = false;
      }
      if (!finished && panel.isConnected) window.setTimeout(poll, 2500);
    };
    poll();
  }
  window.JustVoxelRestoreOperation = { init: initRestoreOperation };
  initRestoreOperation();
})();
