function initServerMigrationOperation(root = document) {
  const panel = root.querySelector("[data-server-migration-operation]");
  if (!panel || panel.dataset.serverMigrationOperationInitialized === "true") return;
  panel.dataset.serverMigrationOperationInitialized = "true";

  const statusURL = panel.dataset.statusUrl;
  const statusText = root.querySelector("#server-migration-operation-status");
  const stateBadge = root.querySelector("#server-migration-operation-state");
  const stageText = root.querySelector("#server-migration-operation-stage");
  const nameText = root.querySelector("#server-migration-operation-name");
  const reconnectNote = root.querySelector("#server-migration-reconnect-note");
  const successNote = root.querySelector("#server-migration-operation-success");
  const rollbackNote = root.querySelector("#server-migration-operation-rollback");
  const attentionNote = root.querySelector("#server-migration-operation-attention");
  const dashboardLink = root.querySelector("#server-migration-dashboard-link");
  const recoveryLink = root.querySelector("#server-migration-recovery-link");
  let failures = 0;
  let finished = false;

  const operationName = (type) => {
    if (type === "migration_export") return "Server Export";
    if (type === "migration_import") return "Server Import";
    if (type === "migration_recovery") return "Migration Recovery";
    return "Server Migration";
  };

  const stateLabel = (state) => {
    if (state === "queued") return "Queued";
    if (state === "validating") return "Validating";
    if (state === "running") return "Running";
    if (state === "verifying") return "Verifying";
    if (state === "failed") return "Recovering";
    if (state === "rolling_back") return "Rolling back";
    if (state === "succeeded") return "Succeeded";
    if (state === "rolled_back") return "Rolled back";
    if (state === "needs_attention") return "Needs attention";
    return "Working";
  };

  const friendlyToken = (value) => {
    if (!value) return "Working";
    const text = value.replaceAll("_", " ");
    return text.charAt(0).toUpperCase() + text.slice(1);
  };

  const stageLabel = (type, stage) => {
    if (stage === "queued") return "Waiting to start";
    if (stage === "completed") return "Complete";
    if (stage === "player_recheck") return "Rechecking online players";
    if (stage === "minecraft_stop") return "Stopping Minecraft safely";
    if (["invalid_execution_plan", "interrupted"].includes(stage)) return "Administrator attention required";
    if (type === "migration_export") {
      if (stage === "export_preflight") return "Rechecking reviewed Export";
      if (stage === "archive_integrity" || stage === "integrity_verification") return "Verifying migration bundle";
      if (stage === "export_failed") return "Preparing safe Export recovery";
      if (stage === "rollback") return "Finalizing Export recovery state";
      if (stage === "export_rolled_back") return "Export rolled back";
      if (["export_needs_attention", "export_backend_interrupted", "export_backend_incomplete"].includes(stage)) return "Administrator attention required";
    }
    if (type === "migration_import") {
      if (stage === "recovery_handoff") return "Recovery responsibility transferred";
      if (stage === "import_preflight") return "Rechecking reviewed Import";
      if (stage === "import_storage") return "Preparing fresh destination storage";
      if (stage === "import_execute") return "Importing Minecraft server data";
      if (stage === "import_verify") return "Validating imported Minecraft";
      if (stage === "import_failed") return "Preparing safe Import rollback";
      if (stage === "rollback") return "Finalizing Import rollback";
      if (stage === "import_rolled_back") return "Import rolled back";
      if (["import_needs_attention", "import_backend_interrupted", "import_backend_incomplete"].includes(stage)) return "Administrator attention required";
    }
    if (type === "migration_recovery") {
      if (stage === "recovery_preflight") return "Rechecking retained recovery state";
      if (stage === "recovery_finalize") return "Finalizing retained recovery state";
      if (stage === "recovery_verify") return "Verifying recovery finalization";
      if (["recovery_backend_failed", "recovery_backend_invalid", "recovery_backend_incomplete"].includes(stage)) return "Administrator attention required";
    }
    return friendlyToken(stage);
  };

  const supportedType = (type) => ["migration_export", "migration_import", "migration_recovery"].includes(type);
  const terminalState = (state) => ["succeeded", "rolled_back", "needs_attention"].includes(state);

  const render = (operation) => {
    if (!operation || !supportedType(operation.operation_type)) return;
    failures = 0;
    if (reconnectNote) reconnectNote.hidden = true;
    if (nameText) nameText.textContent = operationName(operation.operation_type);
    if (statusText) statusText.textContent = operation.status || "Server migration is running.";
    if (stateBadge) stateBadge.textContent = stateLabel(operation.state);
    if (stageText) stageText.textContent = stageLabel(operation.operation_type, operation.stage);

    const succeeded = operation.state === "succeeded";
    const rolledBack = operation.state === "rolled_back";
    const needsAttention = operation.state === "needs_attention";
    if (successNote) successNote.hidden = !succeeded;
    if (rollbackNote) rollbackNote.hidden = !rolledBack;
    if (attentionNote) attentionNote.hidden = !needsAttention;
    if (dashboardLink) dashboardLink.hidden = !succeeded;
    if (recoveryLink) recoveryLink.hidden = !(needsAttention && operation.operation_type === "migration_import");
    if (terminalState(operation.state)) finished = true;
  };

  const poll = async () => {
    if (finished || !panel.isConnected) return;
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
    if (!finished && panel.isConnected) window.setTimeout(poll, 2500);
  };

  poll();
}

window.JustVoxelServerMigrationOperation = { init: initServerMigrationOperation };
if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", () => initServerMigrationOperation(document), { once: true });
} else {
  initServerMigrationOperation(document);
}
