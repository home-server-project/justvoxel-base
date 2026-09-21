document.addEventListener("DOMContentLoaded", () => {
  const panel = document.querySelector("[data-restore-operation]");
  if (!panel) return;

  const statusURL = panel.dataset.statusUrl;
  const statusText = document.getElementById("restore-operation-status");
  const stateBadge = document.getElementById("restore-operation-state");
  const stageText = document.getElementById("restore-operation-stage");
  const reconnectNote = document.getElementById("restore-reconnect-note");
  const successNote = document.getElementById("restore-operation-success");
  const rollbackNote = document.getElementById("restore-operation-rollback");
  const attentionNote = document.getElementById("restore-operation-attention");
  const dashboardLink = document.getElementById("restore-dashboard-link");
  let failures = 0;
  let finished = false;

  const stateLabel = (state) => {
    if (state === "queued") return "Queued";
    if (state === "validating") return "Validating";
    if (state === "running") return "Restoring";
    if (state === "verifying") return "Runtime validation";
    if (state === "failed") return "Recovering";
    if (state === "rolling_back") return "Rolling back";
    if (state === "rolled_back") return "Rolled back";
    if (state === "needs_attention") return "Needs attention";
    if (state === "succeeded") return "Complete";
    return "Working";
  };

  const stageLabel = (stage) => {
    if (stage === "queued") return "Waiting to start";
    if (stage === "restore_preflight" || stage === "backup_source") return "Checking backup storage";
    if (stage === "archive_integrity") return "Checking archive integrity";
    if (stage === "archive_safety") return "Checking archive safety";
    if (stage === "staging_space") return "Checking staging space";
    if (stage === "staging") return "Preparing Restore data";
    if (stage === "minecraft_stop") return "Stopping Minecraft safely";
    if (stage === "switching") return "Applying Restore data";
    if (stage === "minecraft_runtime") return "Validating restored Minecraft";
    if (stage === "restore_failed") return "Preparing recovery";
    if (stage === "rollback") return "Restoring previous Minecraft data";
    if (stage === "restore_rolled_back") return "Rolled back";
    if (stage === "restore_needs_attention" || stage === "interrupted") return "Administrator attention required";
    if (stage === "completed") return "Complete";
    return "Working";
  };

  const terminalState = (state) => ["succeeded", "rolled_back", "needs_attention"].includes(state);

  const render = (operation) => {
    if (!operation || operation.operation_type !== "restore") return;
    failures = 0;
    if (reconnectNote) reconnectNote.hidden = true;
    if (statusText) statusText.textContent = operation.status || "Restore is running.";
    if (stateBadge) stateBadge.textContent = stateLabel(operation.state);
    if (stageText) stageText.textContent = stageLabel(operation.stage);

    const succeeded = operation.state === "succeeded";
    const rolledBack = operation.state === "rolled_back";
    const needsAttention = operation.state === "needs_attention";
    if (successNote) successNote.hidden = !succeeded;
    if (rollbackNote) rollbackNote.hidden = !rolledBack;
    if (attentionNote) attentionNote.hidden = !needsAttention;
    if (dashboardLink) dashboardLink.hidden = !succeeded;
    if (terminalState(operation.state)) finished = true;
  };

  const poll = async () => {
    if (finished) return;
    try {
      const response = await fetch(statusURL, {
        method: "GET",
        credentials: "same-origin",
        headers: { Accept: "application/json" },
        cache: "no-store",
      });
      if (response.status === 401) {
        window.location.assign("/login");
        return;
      }
      if (response.status === 403) {
        window.location.assign("/password");
        return;
      }
      if (!response.ok) throw new Error("status unavailable");
      const payload = await response.json();
      render(payload.operation);
    } catch (_) {
      failures += 1;
      if (failures >= 2 && reconnectNote) reconnectNote.hidden = false;
    }
    if (!finished) window.setTimeout(poll, 2500);
  };

  poll();
});
