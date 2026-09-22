document.addEventListener("DOMContentLoaded", () => {
  const panel = document.querySelector("[data-data-migration-operation]");
  if (!panel) return;

  const statusURL = panel.dataset.statusUrl;
  const statusText = document.getElementById("data-migration-operation-status");
  const stateBadge = document.getElementById("data-migration-operation-state");
  const stageText = document.getElementById("data-migration-operation-stage");
  const reconnectNote = document.getElementById("data-migration-reconnect-note");
  const successNote = document.getElementById("data-migration-operation-success");
  const rollbackNote = document.getElementById("data-migration-operation-rollback");
  const attentionNote = document.getElementById("data-migration-operation-attention");
  const dashboardLink = document.getElementById("data-migration-dashboard-link");
  let failures = 0;
  let finished = false;

  const stateLabel = (state) => {
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
  };

  const stageLabel = (stage) => {
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
  };

  const terminalState = (state) => ["succeeded", "rolled_back", "needs_attention"].includes(state);

  const render = (operation) => {
    if (!operation || operation.operation_type !== "data_migration") return;
    failures = 0;
    if (reconnectNote) reconnectNote.hidden = true;
    if (statusText) statusText.textContent = operation.status || "Minecraft data migration is running.";
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
