document.addEventListener("DOMContentLoaded", () => {
  const applyForm = document.querySelector("[data-setup-apply]");
  if (applyForm) {
    const eulaDialog = document.querySelector("[data-setup-eula-dialog]");
    const passwordDialog = document.querySelector("[data-setup-password-dialog]");
    const password = passwordDialog?.querySelector("[data-setup-password]");
    const configure = document.querySelector("[data-setup-configure]");
    let passwordConfirmed = false;
    let submitting = false;

    configure?.addEventListener("click", (event) => {
      if (applyForm.dataset.smbRequired === "true" && !passwordConfirmed) {
        event.preventDefault();
        if (passwordDialog && !passwordDialog.open) passwordDialog.showModal();
      } else if (applyForm.dataset.eulaAccepted !== "true") {
        event.preventDefault();
        if (eulaDialog && !eulaDialog.open) eulaDialog.showModal();
      }
    });
    passwordDialog?.querySelector("[data-setup-password-cancel]")?.addEventListener("click", () => passwordDialog.close());
    passwordDialog?.addEventListener("close", () => {
      if (!passwordConfirmed && password) password.value = "";
    });
    const continueWithPassword = () => {
      if (!password?.reportValidity()) return;
      passwordConfirmed = true;
      passwordDialog.close();
      if (applyForm.dataset.eulaAccepted !== "true" && eulaDialog && !eulaDialog.open) eulaDialog.showModal();
      else applyForm.requestSubmit(configure);
    };
    passwordDialog?.querySelector("[data-setup-password-continue]")?.addEventListener("click", continueWithPassword);
    password?.addEventListener("keydown", (event) => {
      if (event.key !== "Enter" || event.isComposing) return;
      event.preventDefault();
      continueWithPassword();
    });
    eulaDialog?.querySelectorAll("[data-setup-eula-cancel], [data-setup-eula-decline]").forEach((button) => {
      button.addEventListener("click", () => eulaDialog.close());
    });
    eulaDialog?.addEventListener("close", () => {
      if (!submitting) {
        passwordConfirmed = false;
        if (password) password.value = "";
      }
    });
    applyForm.addEventListener("submit", (event) => {
      if (applyForm.dataset.smbRequired === "true" && !passwordConfirmed) {
        event.preventDefault();
        if (passwordDialog && !passwordDialog.open) passwordDialog.showModal();
        return;
      }
      if (applyForm.dataset.eulaAccepted !== "true" && event.submitter?.dataset.setupEulaAccept === undefined) {
        event.preventDefault();
        if (eulaDialog && !eulaDialog.open) eulaDialog.showModal();
        return;
      }
      submitting = true;
      if (configure) {
        configure.disabled = true;
        configure.textContent = "Starting setup…";
      }
    });
  }

  const panel = document.querySelector("[data-setup-operation]");
  if (!panel) return;

  const statusURL = panel.dataset.statusUrl;
  const statusText = document.getElementById("setup-operation-status");
  const stateBadge = document.getElementById("setup-operation-state");
  const stageText = document.getElementById("setup-operation-stage");
  const reconnectNote = document.getElementById("setup-reconnect-note");
  const successNote = document.getElementById("setup-operation-success");
  const rollbackNote = document.getElementById("setup-operation-rollback");
  const attentionNote = document.getElementById("setup-operation-attention");
  const dashboardLink = document.getElementById("setup-dashboard-link");
  const reviewLink = document.getElementById("setup-review-link");
  const startOverForm = document.getElementById("setup-start-over-form");
  const recoverForm = document.getElementById("setup-recover-form");
  const disabledStartOver = document.getElementById("setup-start-over-disabled");
  const diagnosticLogLink = document.getElementById("setup-diagnostic-log-link");
  const diagnosticLogView = document.getElementById("setup-diagnostic-log-view");
  const stages = Array.from(panel.querySelectorAll("[data-setup-stage]"));
  const verifyNote = document.getElementById("setup-minecraft-verify-note");
  const elapsed = document.getElementById("setup-operation-elapsed");

  let failures = 0;
  let finished = false;
  let elapsedInterval = null;
  let startedAt = NaN;
  let finalElapsed = null;

  const stateLabel = (state) => {
    if (state === "queued") return "Queued";
    if (state === "validating") return "Validating";
    if (state === "running") return "Configuring";
    if (state === "verifying") return "Verifying";
    if (state === "failed") return "Recovering";
    if (state === "rolling_back") return "Rolling back";
    if (state === "rolled_back") return "Rolled back";
    if (state === "needs_attention") return "Needs attention";
    if (state === "succeeded") return "Complete";
    return state || "Working";
  };

  const stageLabel = (stage) => {
    if (stage === "queued") return "Waiting to start";
    if (stage === "storage_preflight") return "Checking storage";
    if (stage === "storage_snapshot") return "Preparing storage changes";
    if (stage === "storage_verified") return "Storage ready";
    if (stage === "runtime_preflight") return "Checking Minecraft configuration";
    if (stage === "runtime_config") return "Writing Minecraft configuration";
    if (stage === "minecraft_verify") return "Starting Minecraft";
    if (stage === "final_validation") return "Final validation";
    if (stage === "completed") return "Complete";
    if (stage === "setup_failed") return "Preparing recovery";
    if (stage === "runtime_rollback") return "Restoring Minecraft configuration";
    if (stage === "storage_rollback") return "Restoring storage changes";
    if (stage === "storage_recovery") return "Retrying storage recovery";
    if (stage === "setup_rolled_back") return "Rolled back";
    if (stage === "interrupted") return "Interrupted";
    return "Working";
  };

  const terminalState = (state) => ["succeeded", "rolled_back", "needs_attention"].includes(state);

  const visibleStage = (stage) => {
    if (["storage_preflight", "storage_snapshot", "storage_verified"].includes(stage)) return 0;
    if (["runtime_preflight", "runtime_config"].includes(stage)) return 1;
    if (stage === "minecraft_verify") return 2;
    if (stage === "final_validation") return 3;
    if (stage === "completed") return 4;
    return -1;
  };

  const renderStages = (operation) => {
    const succeeded = operation.state === "succeeded";
    const recovering = ["failed", "rolling_back", "rolled_back", "needs_attention"].includes(operation.state);
    const current = visibleStage(operation.stage);
    stages.forEach((item, index) => {
      const complete = succeeded || (!recovering && current >= 0 && index < current);
      const active = !succeeded && !recovering && index === current;
      item.classList.toggle("is-complete", complete);
      item.classList.toggle("is-current", active);
      item.classList.toggle("is-pending", !complete && !active && !recovering);
      item.classList.toggle("is-rolled-back", operation.state === "rolled_back");
      if (active) item.setAttribute("aria-current", "step");
      else item.removeAttribute("aria-current");
    });
    if (verifyNote) verifyNote.hidden = operation.stage !== "minecraft_verify" || recovering || succeeded;
  };

  const renderElapsed = () => {
    if (!elapsed) return;
    if (!Number.isFinite(startedAt)) {
      elapsed.hidden = true;
      return;
    }
    const seconds = finalElapsed ?? Math.max(0, Math.floor((Date.now() - startedAt) / 1000));
    const minutes = Math.floor(seconds / 60);
    const remainder = seconds % 60;
    const formatted = minutes === 0 ? `${seconds} sec` : remainder === 0 ? `${minutes} min` : `${minutes} min ${String(remainder).padStart(2, "0")} sec`;
    elapsed.textContent = `Elapsed since setup started: ${formatted}`;
    elapsed.hidden = false;
  };

  const render = (operation) => {
    if (!operation) return;
    failures = 0;
    if (reconnectNote) reconnectNote.hidden = true;
    if (statusText) statusText.textContent = operation.status || "Setup is running.";
    if (stateBadge) stateBadge.textContent = stateLabel(operation.state);
    if (stageText) stageText.textContent = operation.state === "rolled_back" ? "Rolled back" : stageLabel(operation.stage);
    renderStages(operation);
    const parsedStart = Date.parse(operation.started_at || "");
    if (Number.isFinite(parsedStart)) startedAt = parsedStart;
    if (terminalState(operation.state)) {
      const finishedAt = Date.parse(operation.finished_at || "");
      finalElapsed = Number.isFinite(startedAt) ? Math.max(0, Math.floor(((Number.isFinite(finishedAt) ? finishedAt : Date.now()) - startedAt) / 1000)) : null;
      if (elapsedInterval !== null) { window.clearInterval(elapsedInterval); elapsedInterval = null; }
    } else if (elapsedInterval === null) {
      elapsedInterval = window.setInterval(renderElapsed, 1000);
    }
    renderElapsed();

    const succeeded = operation.state === "succeeded";
    const rolledBack = operation.state === "rolled_back";
    const needsAttention = operation.state === "needs_attention";
    if (successNote) successNote.hidden = !succeeded;
    if (rollbackNote) rollbackNote.hidden = !rolledBack;
    if (attentionNote) attentionNote.hidden = !needsAttention;
    if (dashboardLink) dashboardLink.hidden = !terminalState(operation.state);
    if (reviewLink) reviewLink.hidden = !rolledBack;
    if (startOverForm) startOverForm.hidden = !rolledBack;
    if (recoverForm) recoverForm.hidden = !(needsAttention && operation.stage === "storage_rollback");
    if (disabledStartOver) disabledStartOver.hidden = !needsAttention;
    if (diagnosticLogLink) diagnosticLogLink.hidden = !terminalState(operation.state);
    if (diagnosticLogView) diagnosticLogView.hidden = !terminalState(operation.state);

    if (terminalState(operation.state)) {
      finished = true;
      panel.classList.add("is-finished");
      panel.classList.toggle("is-rolled-back", rolledBack);
    }
  };

  render({
    state: panel.dataset.initialState,
    stage: panel.dataset.initialStage,
    started_at: panel.dataset.startedAt,
    finished_at: panel.dataset.finishedAt,
    status: statusText?.textContent,
  });

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
