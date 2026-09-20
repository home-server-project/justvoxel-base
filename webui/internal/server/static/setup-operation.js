document.addEventListener("DOMContentLoaded", () => {
  const applyForm = document.querySelector("[data-setup-apply]");
  if (applyForm) {
    applyForm.addEventListener("submit", () => {
      const button = applyForm.querySelector('button[type="submit"]');
      if (button) {
        button.disabled = true;
        button.textContent = "Starting setup…";
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

  let failures = 0;
  let finished = false;

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

  const terminalState = (state) => ["succeeded", "rolled_back", "needs_attention"].includes(state);

  const render = (operation) => {
    if (!operation) return;
    failures = 0;
    if (reconnectNote) reconnectNote.hidden = true;
    if (statusText) statusText.textContent = operation.status || "Setup is running.";
    if (stateBadge) stateBadge.textContent = stateLabel(operation.state);
    if (stageText) stageText.textContent = operation.stage || "working";

    const succeeded = operation.state === "succeeded";
    const rolledBack = operation.state === "rolled_back";
    const needsAttention = operation.state === "needs_attention";
    if (successNote) successNote.hidden = !succeeded;
    if (rollbackNote) rollbackNote.hidden = !rolledBack;
    if (attentionNote) attentionNote.hidden = !needsAttention;
    if (dashboardLink) dashboardLink.hidden = !succeeded;
    if (reviewLink) reviewLink.hidden = !rolledBack;

    if (terminalState(operation.state)) {
      finished = true;
      if (succeeded) {
        window.setTimeout(() => window.location.assign("/"), 2500);
      }
    }
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
