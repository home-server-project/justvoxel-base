const versionLauncher = document.querySelector("[data-version-open]");
const versionDialog = document.querySelector("[data-version-workspace-dialog]");
if (versionLauncher && versionDialog) {
  const content = versionDialog.querySelector("[data-version-content]");
  const state = versionDialog.querySelector("[data-version-state]");
  const tabs = Array.from(versionDialog.querySelectorAll("[data-version-tab]"));
  const csrf = document.querySelector("[data-minecraft-workspace-csrf]")?.value || "";
  let currentTab = "software";
  let sequence = 0;
  let updateOperation = "";
  const request = async (url, options = {}) => {
    const response = await fetch(url, { credentials: "same-origin", cache: "no-store", ...options });
    const payload = await response.json();
    if (!response.ok) throw new Error(payload.error || "Version information is unavailable.");
    return payload;
  };
  const statusURL = (policy = "", version = "") => "/api/version/workspace/status?" + new URLSearchParams({ policy, version });
  const policyLabel = (policy) => ({ recommended: "Recommended", latest: "Latest", pinned: "Specific version" })[policy] || "Unknown";
  const releaseLabel = (channel) => ({ STABLE: "Stable", BETA: "Beta", ALPHA: "Alpha" })[channel] || "Pre-release";
  const line = (label, value) => {
    const row = document.createElement("div");
    const name = document.createElement("span");
    const detail = document.createElement("strong");
    name.textContent = label;
    detail.textContent = value || "Unavailable";
    row.append(name, detail);
    return row;
  };
  const card = (title) => {
    const panel = document.createElement("section");
    panel.className = "panel details";
    const heading = document.createElement("h2");
    heading.textContent = title;
    panel.appendChild(heading);
    return panel;
  };
  const stackLabel = (value) => ({ up_to_date: "Up to date", updates_available: "Updates available", waiting_for_compatibility: "Waiting for compatibility", unavailable: "Unavailable", managed_automatically: "Managed automatically", disabled: "Cross-play off" })[value] || "Unavailable";
  const watchUpdate = async (id) => {
    updateOperation = id;
    try {
      const result = await request("/api/version/workspace/update-operation?" + new URLSearchParams({ id }));
      const operation = result.operation;
      state.textContent = operation?.status || "Server update status is unavailable.";
      if (["succeeded", "resolved"].includes(operation?.state)) {
        updateOperation = "";
        await render(operation.status);
      } else if (["needs_attention", "failed"].includes(operation?.state)) {
        updateOperation = "";
        await render(operation.status);
      } else {
        setTimeout(() => watchUpdate(id), 2000);
      }
    } catch (failure) { state.textContent = failure.message; updateOperation = ""; }
  };
  const reviewUpdates = async (root, button) => {
    button.disabled = true;
    try {
      const plan = await request(statusURL());
      if (!plan.update_available || plan.stack_state !== "updates_available") throw new Error(plan.reason || "No safe server update is currently available.");
      root.querySelector("[data-stack-review]")?.remove();
      const review = card("Review updates");
      review.dataset.stackReview = "";
      const grid = document.createElement("div");
      grid.className = "version-status-grid";
      grid.append(line("Minecraft", `${plan.installed} → ${plan.selected_candidate}`), line("Geyser", stackLabel(plan.geyser_state)), line("Floodgate", stackLabel(plan.floodgate_state)), line("ViaVersion", "Managed automatically"));
      const notice = document.createElement("p");
      notice.className = "notice warning";
      notice.textContent = "JustVoxel will create a cold backup and safely restart Minecraft. Online players will receive a shutdown countdown. A stopped server may start temporarily for verification and will be stopped again afterward.";
      const consent = document.createElement("label");
      const checkbox = document.createElement("input");
      checkbox.type = "checkbox";
      consent.append(checkbox, document.createTextNode("Allow the update to interrupt online players."));
      const actions = document.createElement("div");
      actions.className = "action-row";
      const cancel = document.createElement("button");
      cancel.type = "button";
      cancel.className = "secondary";
      cancel.textContent = "Cancel review";
      cancel.addEventListener("click", () => review.remove());
      const apply = document.createElement("button");
      apply.type = "button";
      apply.textContent = "Apply update";
      apply.addEventListener("click", async () => {
        apply.disabled = true;
        try {
          const body = new URLSearchParams({ csrf, plan_fingerprint: plan.plan_fingerprint, confirm_players: checkbox.checked ? "yes" : "no" });
          const result = await request("/api/version/workspace/update", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body });
          review.remove();
          if (!result.operation?.operation_id) throw new Error("Server update tracking is unavailable.");
          watchUpdate(result.operation.operation_id);
        } catch (failure) { state.textContent = failure.message; apply.disabled = false; }
      });
      actions.append(cancel, apply);
      review.append(grid, notice, consent, actions);
      root.appendChild(review);
    } catch (failure) { state.textContent = failure.message; }
    finally { button.disabled = false; }
  };
  const showReview = (root, form, plan, params, status) => {
    root.querySelector("[data-version-review]")?.remove();
    const review = card("Review changes");
    review.dataset.versionReview = "";
    if (plan.changes?.length) {
      plan.changes.forEach((change) => {
        const item = document.createElement("p");
        const before = change.before === "LATEST" ? "Follows new versions" : change.before || "—";
        const after = change.field === "version" && change.after === "LATEST" ? status?.selected_candidate || "Unavailable" : change.after || "—";
        item.textContent = `${change.label || change.field}: ${before} → ${after}`;
        review.appendChild(item);
      });
    } else {
      const empty = document.createElement("p");
      empty.textContent = "No changes were found.";
      review.appendChild(empty);
    }
    if (plan.restart_required) {
      const notice = document.createElement("p");
      notice.className = "notice warning";
      notice.textContent = "Minecraft must restart before these changes take effect. Online players may be disconnected.";
      review.appendChild(notice);
    }
    (plan.warnings || []).forEach((warning) => {
      const notice = document.createElement("p");
      notice.className = "notice warning";
      notice.textContent = warning;
      review.appendChild(notice);
    });
    if (status?.crossplay_enabled && !status.crossplay_compatible) {
      const warning = document.createElement("p");
      warning.className = "notice warning";
      warning.textContent = status.reason || "Bedrock cross-play compatibility could not be confirmed.";
      review.appendChild(warning);
    }
    if (plan.confirmation_required) {
      const warning = document.createElement("p");
      warning.className = "notice warning";
      warning.textContent = `${plan.online || 0} player(s) are online. Applying this change will disconnect them.`;
      review.appendChild(warning);
    }
    const actions = document.createElement("div");
    actions.className = "action-row";
    const cancel = document.createElement("button");
    cancel.type = "button";
    cancel.className = "secondary";
    cancel.textContent = "Cancel review";
    cancel.addEventListener("click", () => { review.remove(); form.hidden = false; });
    actions.appendChild(cancel);
    if (plan.changes?.length && !plan.compatibility_blocked && !(status?.crossplay_enabled && !status.crossplay_compatible)) {
      const apply = document.createElement("button");
      apply.type = "button";
      apply.textContent = plan.confirmation_required ? "Confirm and apply" : "Apply changes";
      apply.addEventListener("click", async () => {
        apply.disabled = true;
        state.textContent = "Applying changes…";
        try {
          const body = new URLSearchParams(params);
          if (plan.confirmation_required) body.set("confirm_players", "yes");
          const result = await request("/api/minecraft/workspace/settings/apply", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body });
          if (!result.applied) throw new Error(result.error || "The change was not applied. Review again.");
          await render("Changes saved.");
        } catch (failure) { state.textContent = failure.message; apply.disabled = false; }
      });
      actions.appendChild(apply);
    }
    review.appendChild(actions);
    root.appendChild(review);
    form.hidden = true;
  };
  const wireForm = (root, form, minecraft) => {
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      state.textContent = "Reviewing changes…";
      const channel = form.elements.channel?.value || minecraft.image_tag || "stable";
      const imageTag = channel === "custom" ? form.elements.custom_tag.value.trim() : channel;
      const policy = form.elements.policy?.value || minecraft.version_mode || "pinned";
      const version = policy === "pinned" ? (form.elements.version?.value.trim() || minecraft.version || "") : "";
      const params = new URLSearchParams({ csrf, tab: "version", image_tag: imageTag, version_policy: policy, version });
      try {
        const [plan, candidate] = await Promise.all([
          request("/api/minecraft/workspace/settings/plan", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: params }),
          request(statusURL(policy, version)),
        ]);
        showReview(root, form, plan, params, candidate);
        state.textContent = "";
      } catch (failure) { state.textContent = failure.message; }
    });
  };
  const render = async (message = "") => {
    const ticket = ++sequence;
    state.textContent = "Loading version information…";
    try {
      const [settings, status, pending] = await Promise.all([
        request("/api/minecraft/workspace/settings"), request(statusURL()),
        request("/api/version/workspace/update-operation").catch(() => ({})),
      ]);
      if (ticket !== sequence) return;
      const minecraft = settings.minecraft || {};
      const root = document.createElement("div");
      if (pending.operation) {
        const progress = card("Server update");
        const notice = document.createElement("p");
        notice.textContent = pending.operation.status;
        progress.appendChild(notice);
        if (["needs_attention", "failed"].includes(pending.operation.state)) {
          updateOperation = "";
          const acknowledge = document.createElement("button");
          acknowledge.type = "button";
          acknowledge.textContent = "Verify and keep current server";
          acknowledge.addEventListener("click", async () => {
            acknowledge.disabled = true;
            try {
              const body = new URLSearchParams({ csrf, acknowledge: "yes", operation_id: pending.operation.operation_id });
              await request("/api/version/workspace/update", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body });
              await render("Current server verified and kept. Review updates again when ready.");
            } catch (failure) { state.textContent = failure.message; acknowledge.disabled = false; }
          });
          progress.appendChild(acknowledge);
        } else if (!updateOperation) {
          watchUpdate(pending.operation.operation_id);
        }
        root.appendChild(progress);
      }

      if (currentTab === "software") {
        const software = card("Server software");
        const choices = document.createElement("div");
        choices.className = "version-software-choices";
        [["Paper", "Supported now · plugins · Bedrock cross-play where compatible", true], ["Purpur", "Coming soon", false], ["Vanilla", "Coming soon", false]].forEach(([name, description, enabled]) => {
          const choice = document.createElement("div");
          choice.className = "version-software-choice" + (enabled ? " is-current" : "");
          if (!enabled) choice.setAttribute("aria-disabled", "true");
          const title = document.createElement("strong");
          const text = document.createElement("small");
          title.textContent = name + (enabled ? " · Current" : "");
          text.textContent = description;
          choice.append(title, text);
          choices.appendChild(choice);
        });
        software.appendChild(choices);
        root.appendChild(software);
        const panel = card("Container release channel");
        const form = document.createElement("form");
        form.innerHTML = '<label>Channel<select name="channel"><option value="stable">Stable</option><option value="latest">Latest</option><option value="custom">Custom</option></select></label><label data-version-custom hidden>Custom tag<input name="custom_tag" autocomplete="off"></label><div class="action-row"><button type="submit">Review changes</button></div>';
        const channel = form.elements.channel;
        channel.value = ["stable", "latest"].includes(minecraft.image_tag) ? minecraft.image_tag : "custom";
        form.elements.custom_tag.value = channel.value === "custom" ? minecraft.image_tag || "" : "";
        const sync = () => { form.querySelector("[data-version-custom]").hidden = channel.value !== "custom"; form.elements.custom_tag.required = channel.value === "custom"; };
        channel.addEventListener("change", sync);
        sync();
        panel.appendChild(form);
        root.appendChild(panel);
        wireForm(root, form, minecraft);
      } else {
        const panel = card("Minecraft Version");
        const grid = document.createElement("div");
        grid.className = "version-status-grid";
        grid.append(line("Installed", status.installed || "Not detected"), line("Newest available", status.available ? `${status.available} · ${releaseLabel(status.available_channel)}` : "Unavailable"), line("Recommended", status.recommended), line("Configured", status.configured_version === "LATEST" ? "Follows new versions" : status.configured_version), line("Server software", status.server_software || "Paper"), line("Container channel", status.image_tag), line("Version policy", policyLabel(minecraft.version_mode)), line("Overall", stackLabel(status.stack_state)));
        grid.append(line("Minecraft update", stackLabel(status.minecraft_state)), line("Geyser", stackLabel(status.geyser_state)), line("Floodgate", stackLabel(status.floodgate_state)), line("ViaVersion", "Managed automatically"));
        if (status.crossplay_enabled && status.policy === "recommended" && status.available && status.recommended && status.available !== status.recommended) {
          grid.append(line("New Minecraft version", `${status.available} · Waiting for cross-play support`));
        }
        panel.appendChild(grid);
        if (status.update_available && status.stack_state === "updates_available") {
          const updates = document.createElement("button");
          updates.type = "button";
          updates.textContent = "Review updates";
          updates.disabled = Boolean(updateOperation || pending.operation);
          updates.addEventListener("click", () => reviewUpdates(root, updates));
          panel.appendChild(updates);
        }
        const compatibility = document.createElement("p");
        compatibility.className = status.crossplay_enabled && !status.crossplay_compatible ? "notice warning" : "muted compact";
        compatibility.textContent = status.reason || (status.stack_state === "up_to_date" ? "Your JustVoxel server is up to date. No action needed." : status.stack_state === "updates_available" ? "Compatible server updates are ready to review." : "Server update information could not be verified.");
        panel.appendChild(compatibility);
        root.appendChild(panel);
        const policyPanel = card("Version policy");
        const form = document.createElement("form");
        form.innerHTML = '<label>Policy<select name="policy"><option value="recommended">Recommended</option><option value="latest">Latest</option><option value="pinned">Specific version</option></select></label><label data-version-specific>Exact Minecraft version<input name="version" autocomplete="off"></label><div class="version-status-grid" data-version-preview></div><div class="action-row"><button type="submit">Review changes</button></div>';
        form.elements.policy.value = ["recommended", "latest", "pinned"].includes(minecraft.version_mode) ? minecraft.version_mode : "recommended";
        form.elements.version.value = minecraft.version === "LATEST" ? "" : minecraft.version || "";
        const sync = async () => {
          const policy = form.elements.policy.value;
          form.querySelector("[data-version-specific]").hidden = policy !== "pinned";
          form.elements.version.required = policy === "pinned";
          const preview = form.querySelector("[data-version-preview]");
          preview.textContent = "Checking available version…";
          try {
            const candidate = await request(statusURL(policy, policy === "pinned" ? form.elements.version.value.trim() : ""));
            if (!preview.isConnected) return;
            preview.replaceChildren(line("Newest available", candidate.available ? `${candidate.available} · ${releaseLabel(candidate.available_channel)}` : "Unavailable"), line(policy === "latest" ? "Will install now" : "Will install", candidate.selected_candidate), line("Paper release", candidate.candidate_channel ? releaseLabel(candidate.candidate_channel) : "Unknown"), line("Geyser/Floodgate", candidate.crossplay_enabled ? candidate.crossplay_compatible ? `Compatible with ${candidate.selected_candidate}` : "Not compatible" : "Cross-play off"));
          } catch (_) { preview.textContent = "Available version and compatibility could not be confirmed."; }
        };
        form.elements.policy.addEventListener("change", sync);
        form.elements.version.addEventListener("change", sync);
        policyPanel.appendChild(form);
        root.appendChild(policyPanel);
        wireForm(root, form, minecraft);
        sync();
      }
      if (pending.operation) {
        root.querySelectorAll("form input, form select, form button").forEach((control) => { control.disabled = true; });
      }
      content.replaceChildren(root);
      state.textContent = message;
    } catch (failure) { if (ticket === sequence) state.textContent = failure.message; }
  };
  const windowControl = setupWorkspaceWindow(versionDialog, { onOpen: () => render() });
  versionLauncher.addEventListener("click", () => { document.querySelector("[data-control-center]")?.removeAttribute("open"); windowControl?.open(); });
  versionDialog.querySelector("[data-version-close]").addEventListener("click", () => windowControl?.close());
  versionDialog.querySelector("[data-version-refresh]").addEventListener("click", () => render());
  tabs.forEach((tab) => tab.addEventListener("click", () => { currentTab = tab.dataset.versionTab; tabs.forEach((item) => item.setAttribute("aria-selected", String(item === tab))); render(); }));
  if (readWorkspaceWindowState("version").open) windowControl?.open();
}
