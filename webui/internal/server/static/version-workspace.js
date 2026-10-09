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
  const statusURL = (policy = "", version = "", server_type = "") => "/api/version/workspace/status?" + new URLSearchParams({ policy, version, server_type });
  const policyLabel = (policy) => ({ recommended: "Recommended", latest: "Latest", pinned: "Specific version" })[policy] || "Unknown";
  const releaseLabel = (channel) => ({ STABLE: "Stable", BETA: "Beta", ALPHA: "Alpha", RELEASE: "Release" })[channel] || "Pre-release";
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
    if (title) {
      const heading = document.createElement("h2");
      heading.textContent = title;
      panel.appendChild(heading);
    }
    return panel;
  };
  const stackLabel = (value) => ({ up_to_date: "Up to date", updates_available: "Updates available", waiting_for_compatibility: "Waiting for compatibility", unavailable: "Unavailable", managed_automatically: "Managed automatically", disabled: "Cross-play off", not_applicable: "Not applicable" })[value] || "Unavailable";
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
  // Let the native select change event settle before changing form geometry.
  const settleSelectChange = () => new Promise((resolve) => setTimeout(resolve, 0));
  const requiresRuntimeUpdate = (plan, status) => Boolean(status?.selected_candidate && status.installed && (
    status.installed !== status.selected_candidate ||
    (!plan.changes?.length && status.update_available && status.stack_state === "updates_available")
  ));
  const showReview = (root, form, plan, params, status, players) => {
    const versionReview = Boolean(form.elements.policy);
    const needsUpdate = versionReview && requiresRuntimeUpdate(plan, status);
    const blocked = plan.compatibility_blocked || (status?.crossplay_enabled && !status.crossplay_compatible) || (versionReview && (!(status.server_supported ?? status.paper_supported) || !status.installed)) || (needsUpdate && status.stack_state !== "updates_available");
    root.querySelector("[data-version-review]")?.remove();
    const review = card("Review changes");
    review.dataset.versionReview = "";
    if (plan.changes?.length) {
      plan.changes.forEach((change) => {
        if (versionReview && change.field === "version") return;
        const item = document.createElement("p");
        const before = change.before === "LATEST" ? "Follows new versions" : change.before || "—";
        const after = change.field === "version" && change.after === "LATEST" ? status?.selected_candidate || "Unavailable" : change.after || "—";
        item.textContent = versionReview && change.field === "version_policy"
          ? `Version: ${policyLabel(change.before)} → ${policyLabel(change.after)}`
          : `${change.label || change.field}: ${before} → ${after}`;
        review.appendChild(item);
      });
    } else if (!needsUpdate) {
      const empty = document.createElement("p");
      empty.textContent = "No changes were found.";
      review.appendChild(empty);
    }
    if (needsUpdate) {
      const grid = document.createElement("div");
      grid.className = "version-status-grid";
      grid.append(line("Minecraft", `${status.installed} → ${status.selected_candidate}`), line("Geyser", stackLabel(status.geyser_state)), line("Floodgate", stackLabel(status.floodgate_state)), line("ViaVersion", stackLabel(status.viaversion_state)));
      review.appendChild(grid);
      const notice = document.createElement("p");
      notice.className = "notice warning";
      notice.textContent = "JustVoxel will create a cold backup and safely restart Minecraft. Online players will receive a shutdown countdown. A stopped server may start temporarily for verification and will be stopped again afterward.";
      review.appendChild(notice);
    } else if (!versionReview && plan.restart_required) {
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
      warning.textContent = (status.reason || "Bedrock cross-play compatibility could not be confirmed.") + (versionReview ? " Disable Cross-play first before selecting this version." : "");
      review.appendChild(warning);
    }
    if (versionReview && blocked && !(status.crossplay_enabled && !status.crossplay_compatible)) {
      const warning = document.createElement("p");
      warning.className = "notice warning";
      warning.textContent = status.reason || "A safe Minecraft update could not be verified.";
      review.appendChild(warning);
    }
    const confirmationRequired = plan.confirmation_required || (needsUpdate && Number(players?.online || 0) > 0);
    const checkbox = document.createElement("input");
    checkbox.type = "checkbox";
    checkbox.setAttribute("role", "switch");
    checkbox.checked = !versionReview && Boolean(plan.confirmation_required);
    if (!versionReview && plan.confirmation_required) {
      const warning = document.createElement("p");
      warning.className = "notice warning";
      warning.textContent = `${plan.online || 0} player(s) are online. Applying this change will disconnect them.`;
      review.appendChild(warning);
    }
    if (versionReview && confirmationRequired && !blocked) {
      const consent = document.createElement("label");
      consent.className = "minecraft-native-toggle system-ups-shutdown-switch";
      const label = document.createElement("span");
      label.textContent = "Allow the update to interrupt online players.";
      consent.append(label, checkbox);
      review.appendChild(consent);
    }
    const actions = document.createElement("div");
    actions.className = "action-row";
    const cancel = document.createElement("button");
    cancel.type = "button";
    cancel.className = "secondary";
    cancel.textContent = "Cancel review";
    cancel.addEventListener("click", () => { review.remove(); form.hidden = false; });
    actions.appendChild(cancel);
    if ((plan.changes?.length || needsUpdate) && !blocked) {
      const apply = document.createElement("button");
      apply.type = "button";
      apply.textContent = !versionReview && plan.confirmation_required ? "Confirm and apply" : "Apply changes";
      apply.disabled = confirmationRequired && !checkbox.checked;
      checkbox.addEventListener("change", () => { apply.disabled = confirmationRequired && !checkbox.checked; });
      apply.addEventListener("click", async () => {
        apply.disabled = true;
        cancel.disabled = true;
        checkbox.disabled = true;
        state.textContent = "Applying changes…";
        let saved = false;
        try {
          if (confirmationRequired && !checkbox.checked) throw new Error("Confirm online player interruption before applying.");
          if (plan.changes?.length) {
            const body = new URLSearchParams(params);
            body.set("confirm_players", checkbox.checked ? "yes" : "no");
            const result = await request("/api/minecraft/workspace/settings/apply", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body });
            if (!result.applied) throw new Error(result.error || "The change was not applied. Review again.");
            saved = true;
          }
          if (versionReview) {
            state.textContent = "Refreshing version information…";
            const refreshed = await request(statusURL());
            if (needsUpdate) {
              if (refreshed.selected_candidate !== status.selected_candidate) throw new Error("The selected Minecraft version changed. Review changes again before continuing.");
              if (refreshed.crossplay_enabled && !refreshed.crossplay_compatible) throw new Error("Disable Cross-play first before updating to this Minecraft version.");
              if (refreshed.update_available) {
                if (refreshed.stack_state !== "updates_available" || !refreshed.plan_fingerprint) throw new Error(refreshed.reason || "A safe Minecraft update could not be verified.");
                state.textContent = "Starting Minecraft update…";
                const body = new URLSearchParams({ csrf, plan_fingerprint: refreshed.plan_fingerprint, confirm_players: checkbox.checked ? "yes" : "no" });
                const result = await request("/api/version/workspace/update", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body });
                if (!result.operation?.operation_id) throw new Error("Server update tracking is unavailable. Refresh Version to check its status.");
                review.remove();
                updateOperation = result.operation.operation_id;
                await render("Minecraft update started.");
                watchUpdate(result.operation.operation_id);
                return;
              }
              if (refreshed.installed !== refreshed.selected_candidate) throw new Error(refreshed.reason || "The Minecraft update is unavailable. Review changes again before continuing.");
            }
          }
          await render("Changes saved.");
        } catch (failure) {
          // A saved configuration is never automatically reapplied or retried.
          if (saved) await render("Changes saved. " + failure.message);
          else {
            state.textContent = failure.message;
            apply.disabled = confirmationRequired && !checkbox.checked;
            cancel.disabled = false;
            checkbox.disabled = false;
          }
        }
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
        const versionReview = Boolean(form.elements.policy);
        const [plan, candidate, snapshot] = await Promise.all([
          request("/api/minecraft/workspace/settings/plan", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: params }),
          request(statusURL(policy, version)),
          versionReview ? request("/api/dashboard-status") : Promise.resolve(null),
        ]);
        showReview(root, form, plan, params, candidate, snapshot?.players);
        state.textContent = "";
      } catch (failure) { state.textContent = failure.message; }
    });
  };
  const render = async (message = "") => {
    const ticket = ++sequence;
    await settleSelectChange();
    if (ticket !== sequence) return;
    state.textContent = "Loading version information…";
    try {
      const [settings, status, pending] = await Promise.all([
        request("/api/minecraft/workspace/settings"), request(statusURL()),
        request("/api/version/workspace/update-operation").catch(() => ({})),
      ]);
      if (ticket !== sequence) return;
      const minecraft = settings.minecraft || {};
      const root = document.createElement("div");
      let initializePreview;
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
              await render("Current server verified and kept. Review changes again when ready.");
            } catch (failure) { state.textContent = failure.message; acknowledge.disabled = false; }
          });
          progress.appendChild(acknowledge);
        } else if (!updateOperation) {
          watchUpdate(pending.operation.operation_id);
        }
        root.appendChild(progress);
      }

      if (currentTab === "software") {
        const software = card("");
        const choices = document.createElement("div");
        choices.className = "version-software-choices";
        const currentSoftware = minecraft.server_type || "paper";
        for (const [target, name, description] of [
          ["paper", "Paper", "Plugins and managed Bedrock cross-play."],
          ["purpur", "Purpur", "Plugins and managed Bedrock cross-play."],
          ["vanilla", "Vanilla", "Original Minecraft · Java only · no plugins."],
        ]) {
          const isCurrent = target === currentSoftware;
          const selectable = !isCurrent && ["paper", "purpur"].includes(currentSoftware) && ["paper", "purpur"].includes(target);
          const change = document.createElement(selectable ? "button" : "div");
          change.className = "version-software-choice server-software-card" + (isCurrent ? " is-current" : selectable ? " secondary" : "");
          const title = document.createElement("strong");
          title.textContent = name;
          const availability = document.createElement("span");
          availability.textContent = isCurrent ? "Current" : selectable ? "Available" : "Unavailable";
          const detail = document.createElement("small");
          detail.textContent = description;
          const icon = document.createElement("span");
          icon.className = "server-software-icon";
          icon.setAttribute("aria-hidden", "true");
          const image = document.createElement("img");
          image.src = `/static/server-software/${target}.svg`;
          image.alt = "";
          image.width = image.height = 48;
          icon.appendChild(image);
          const copy = document.createElement("span");
          copy.className = "server-software-copy";
          copy.append(title, availability, detail);
          change.append(icon, copy);
          choices.appendChild(change);
          if (!isCurrent && !selectable) {
            change.setAttribute("aria-disabled", "true");
            const note = document.createElement("small");
            note.textContent = "Requires Reset Minecraft and setup again.";
            copy.appendChild(note);
          }
          if (!selectable) continue;
          change.type = "button";
          change.addEventListener("click", async () => {
            change.disabled = true;
            try {
              const [plan, snapshot] = await Promise.all([request(statusURL("", "", target)), request("/api/dashboard-status")]);
              const review = card("Review server software change");
              const summary = document.createElement("div");
              summary.className = "version-software-review-summary";
              summary.append(line("Server software", `${status.server_software} → ${plan.server_software}`),
                line("Minecraft version", plan.selected_candidate),
                line("Players online", String(snapshot?.players?.online ?? "Unknown")));
              review.appendChild(summary);
              const consent = document.createElement("input"); consent.type = "checkbox";
              consent.setAttribute("role", "switch");
              consent.checked = false;
              const label = document.createElement("label");
              label.className = "minecraft-native-toggle system-ups-shutdown-switch";
              const confirmation = document.createElement("span");
              confirmation.textContent = "Confirm server software change";
              label.append(confirmation, consent);
              review.appendChild(label);
              const apply = document.createElement("button"); apply.type = "button"; apply.textContent = "Apply server software change";
              const safe = plan.stack_state === "updates_available" && plan.server_supported && (!plan.crossplay_enabled || plan.crossplay_compatible) && Boolean(plan.plan_fingerprint);
              apply.disabled = true;
              consent.addEventListener("change", () => { apply.disabled = !safe || !consent.checked; });
              if (!safe) review.appendChild(line("Unavailable", plan.reason));
              apply.addEventListener("click", async () => {
                if (!safe || !consent.checked || apply.disabled) return;
                apply.disabled = true;
                cancel.disabled = true;
                consent.disabled = true;
                try {
                  const body = new URLSearchParams({ csrf, server_type: target, plan_fingerprint: plan.plan_fingerprint, confirm_players: consent.checked ? "yes" : "no" });
                  const result = await request("/api/version/workspace/update", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body });
                  if (!result.operation?.operation_id) throw new Error("Server update tracking is unavailable.");
                  await render("Server software change started."); watchUpdate(result.operation.operation_id);
                } catch (failure) {
                  state.textContent = failure.message;
                  cancel.disabled = false;
                  consent.disabled = false;
                  apply.disabled = !safe || !consent.checked;
                }
              });
              const cancel = document.createElement("button"); cancel.type = "button"; cancel.className = "secondary"; cancel.textContent = "Cancel review";
              cancel.addEventListener("click", () => { review.remove(); change.disabled = false; });
              const actions = document.createElement("div");
              actions.className = "action-row version-software-review-actions";
              actions.append(cancel, apply);
              review.appendChild(actions);
              root.appendChild(review);
            } catch (failure) { state.textContent = failure.message; change.disabled = false; }
          });
        }
        software.appendChild(choices);
        root.appendChild(software);
        const panel = card("Container updates");
        const form = document.createElement("form");
        form.innerHTML = '<div class="version-selection-row"><select name="channel" aria-label="Container updates"><option value="stable">Stable</option><option value="latest">Latest</option><option value="custom">Custom</option></select><button type="submit">Review changes</button></div><label data-version-custom hidden>Custom tag<input name="custom_tag" autocomplete="off"></label>';
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
        grid.append(line("Installed", status.installed || "Not detected"), line("Newest available", status.available ? `${status.available} · ${releaseLabel(status.available_channel)}` : "Unavailable"), line("Recommended", status.recommended), line("Configured", status.configured_version === "LATEST" ? "Follows new versions" : status.configured_version), line("Server software", status.server_software || "Paper"), line("Container channel", status.image_tag), line("Version", policyLabel(minecraft.version_mode)), line("Overall", stackLabel(status.stack_state)));
        grid.append(line("Minecraft update", stackLabel(status.minecraft_state)), line("Geyser", stackLabel(status.geyser_state)), line("Floodgate", stackLabel(status.floodgate_state)), line("ViaVersion", stackLabel(status.viaversion_state)));
        if (status.crossplay_enabled && status.policy === "recommended" && status.available && status.recommended && status.available !== status.recommended) {
          grid.append(line("New Minecraft version", `${status.available} · Waiting for cross-play support`));
        }
        panel.appendChild(grid);
        const compatibility = document.createElement("p");
        compatibility.className = status.crossplay_enabled && !status.crossplay_compatible ? "notice warning" : "muted compact";
        compatibility.textContent = status.reason || (status.stack_state === "up_to_date" ? "Your JustVoxel server is up to date. No action needed." : status.stack_state === "updates_available" ? "Compatible server updates are ready to review." : "Server update information could not be verified.");
        panel.appendChild(compatibility);
        root.appendChild(panel);
        const policyPanel = card("Version configuration");
        const form = document.createElement("form");
        form.innerHTML = '<div class="version-status-grid" data-version-preview></div><div class="version-selection-row"><label data-version-select><select name="policy" aria-label="Version"><option value="recommended">Recommended</option><option value="latest">Latest</option><option value="pinned">Specific version</option></select></label><button type="submit">Review changes</button></div><label data-version-specific>Exact Minecraft version<input name="version" autocomplete="off"></label>';
        form.elements.policy.value = ["recommended", "latest", "pinned"].includes(minecraft.version_mode) ? minecraft.version_mode : "recommended";
        form.elements.version.value = minecraft.version === "LATEST" ? "" : minecraft.version || "";
        let previewSequence = 0;
        const sync = async () => {
          const previewTicket = ++previewSequence;
          await settleSelectChange();
          if (previewTicket !== previewSequence || !form.isConnected) return;
          const policy = form.elements.policy.value;
          form.querySelector("[data-version-specific]").hidden = policy !== "pinned";
          form.elements.version.required = policy === "pinned";
          const preview = form.querySelector("[data-version-preview]");
          preview.textContent = "Checking available version…";
          try {
            const candidate = await request(statusURL(policy, policy === "pinned" ? form.elements.version.value.trim() : ""));
            await settleSelectChange();
            if (!preview.isConnected || previewTicket !== previewSequence) return;
            preview.replaceChildren(line("Newest available", candidate.available ? `${candidate.available} · ${releaseLabel(candidate.available_channel)}` : "Unavailable"), line(policy === "latest" ? "Will install now" : "Will install", candidate.selected_candidate), line("Server release", candidate.candidate_channel ? releaseLabel(candidate.candidate_channel) : "Unknown"), line("Geyser/Floodgate", candidate.crossplay_enabled ? candidate.crossplay_compatible ? `Compatible with ${candidate.selected_candidate}` : "Not compatible" : "Cross-play off"));
          } catch (_) {
            await settleSelectChange();
            if (preview.isConnected && previewTicket === previewSequence) preview.textContent = "Available version and compatibility could not be confirmed.";
          }
        };
        form.elements.policy.addEventListener("change", sync);
        form.elements.version.addEventListener("change", sync);
        policyPanel.appendChild(form);
        root.appendChild(policyPanel);
        wireForm(root, form, minecraft);
        initializePreview = sync;
        form.querySelector("[data-version-specific]").hidden = form.elements.policy.value !== "pinned";
        form.elements.version.required = form.elements.policy.value === "pinned";
      }
      if (pending.operation) {
        root.querySelectorAll("form input, form select, form button").forEach((control) => { control.disabled = true; });
      }
      await settleSelectChange();
      if (ticket !== sequence) return;
      content.replaceChildren(root);
      initializePreview?.();
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
