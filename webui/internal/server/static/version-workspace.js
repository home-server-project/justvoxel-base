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
  const operationKinds = new Map();
  let completedOperation = null;
  const request = async (url, options = {}) => {
    const response = await fetch(url, { credentials: "same-origin", cache: "no-store", ...options });
    const payload = await response.json();
    if (!response.ok) throw new Error(payload.error || "Version information is unavailable.");
    return payload;
  };
  const statusURL = (policy = "", version = "", server_type = "", image_tag = "") => "/api/version/workspace/status?" + new URLSearchParams({ policy, version, server_type, image_tag });
  const policyLabel = (policy) => ({ recommended: "Recommended", latest: "Latest", pinned: "Specific version" })[policy] || "Unknown";
  const releaseLabel = (channel) => ({ STABLE: "Stable", BETA: "Beta", ALPHA: "Alpha", RELEASE: "Release" })[channel] || "Pre-release";
  const line = (label, value) => {
    const row = document.createElement("div");
    const name = document.createElement("span");
    const detail = document.createElement("strong");
    row.className = "version-detail-line";
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
  const operationFrame = (operation) => {
    const succeeded = operation.state === "succeeded";
    const failed = ["failed", "needs_attention"].includes(operation.state);
    const resolved = operation.state === "resolved";
    const kind = operationKinds.get(operation.operation_id);
    const heading = succeeded ? kind === "switch" ? "Switch succeeded" : kind === "container" ? "Container update succeeded" : "Server update succeeded"
      : failed ? "Server update needs attention" : resolved ? "Current server verified and kept" : "Server update in progress";
    const progress = card(heading);
    progress.className += " version-operation-status " + (succeeded || resolved ? "is-succeeded" : failed ? "is-failed" : "is-running");
    progress.dataset.versionOperation = "";
    progress.setAttribute("role", "status");
    const notice = document.createElement("p");
    notice.textContent = operation.status;
    progress.appendChild(notice);
    return progress;
  };
  const watchUpdate = async (id) => {
    updateOperation = id;
    try {
      const result = await request("/api/version/workspace/update-operation?" + new URLSearchParams({ id }));
      const operation = result.operation;
      if (!operation) throw new Error("Server update status is unavailable.");
      const progress = content.querySelector("[data-version-operation]");
      if (progress) {
        const refreshed = operationFrame(operation);
        progress.className = refreshed.className;
        progress.replaceChildren(...refreshed.children);
      }
      state.textContent = "";
      if (["succeeded", "resolved", "needs_attention", "failed"].includes(operation.state)) {
        updateOperation = "";
        completedOperation = operation;
        await render("", operation);
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
    const channelChange = !versionReview && Boolean(plan.changes?.length);
    const needsUpdate = versionReview ? requiresRuntimeUpdate(plan, status) : status?.image_state === "updates_available";
    const runtimePossible = needsUpdate || channelChange;
    const blocked = plan.compatibility_blocked || (status?.crossplay_enabled && !status.crossplay_compatible) || (versionReview && (!(status.server_supported ?? status.paper_supported) || !status.installed)) || (needsUpdate && !channelChange && status.stack_state !== "updates_available");
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
    if (channelChange) {
      const note = document.createElement("p");
      note.textContent = "Applying saves the channel, then re-checks the configured image. If an update is needed, JustVoxel will update the container.";
      review.appendChild(note);
    }
    if (runtimePossible) {
      const grid = document.createElement("div");
      grid.className = "version-status-grid";
      if (versionReview) grid.append(line("Minecraft", `${status.installed} → ${status.selected_candidate}`), line("Geyser", stackLabel(status.geyser_state)), line("Floodgate", stackLabel(status.floodgate_state)), line("ViaVersion", stackLabel(status.viaversion_state)));
      else grid.append(line("Container image", needsUpdate ? "Update available" : "Re-check after saving"));
      review.appendChild(grid);
      const notice = document.createElement("p");
      notice.className = "notice warning";
      notice.textContent = (channelChange ? "If a container update is needed, JustVoxel" : "JustVoxel") + " will create a cold backup and safely restart Minecraft. Online players will receive a shutdown countdown. A stopped server may start temporarily for verification and will be stopped again afterward.";
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
    const confirmationRequired = versionReview ? plan.confirmation_required || (needsUpdate && Number(players?.online || 0) > 0) : runtimePossible && Number(players?.online || 0) > 0;
    const checkbox = document.createElement("input");
    checkbox.type = "checkbox";
    checkbox.setAttribute("role", "switch");
    checkbox.checked = false;
    if (!versionReview && confirmationRequired) {
      const warning = document.createElement("p");
      warning.className = "notice warning";
      warning.textContent = `${players?.online || 0} player(s) are online. Applying this change will disconnect them.`;
      review.appendChild(warning);
    }
    if (confirmationRequired && !blocked) {
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
          if (!versionReview && runtimePossible) {
            state.textContent = "Checking configured container image…";
            const refreshed = await request(statusURL());
            if (refreshed.image_state === "updates_available") {
              if (refreshed.stack_state !== "updates_available" || !refreshed.update_available || !refreshed.plan_fingerprint || !(refreshed.server_supported ?? refreshed.paper_supported) || (refreshed.crossplay_enabled && !refreshed.crossplay_compatible)) throw new Error(refreshed.reason || "A safe container update could not be verified. Review changes again.");
              const body = new URLSearchParams({ csrf, plan_fingerprint: refreshed.plan_fingerprint, confirm_players: checkbox.checked ? "yes" : "no" });
              const result = await request("/api/version/workspace/update", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body });
              if (!result.operation?.operation_id) throw new Error("Server update tracking is unavailable. Refresh Version to check its status.");
              review.remove();
              updateOperation = result.operation.operation_id;
              await render("Container update started.");
              watchUpdate(result.operation.operation_id);
              return;
            }
            if (refreshed.image_state !== "up_to_date") throw new Error("Unable to check the configured container image. Review changes again.");
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
        const [plan, candidate, snapshot] = await Promise.all([
          request("/api/minecraft/workspace/settings/plan", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: params }),
          request(statusURL(policy, version)),
          request("/api/dashboard-status"),
        ]);
        showReview(root, form, plan, params, candidate, snapshot?.players);
        state.textContent = "";
      } catch (failure) { state.textContent = failure.message; }
    });
  };
  const render = async (message = "", confirmedOperation = completedOperation) => {
    const ticket = ++sequence;
    await settleSelectChange();
    if (ticket !== sequence) return;
    state.textContent = "Loading version information…";
    try {
      const [settings, status, pending] = await Promise.all([
        request("/api/minecraft/workspace/settings"), request(statusURL()),
        request("/api/version/workspace/update-operation").catch((failure) => {
          // Software changes require authoritative operation tracking, including
          // reconnecting after a lost Apply response.
          if (currentTab === "software") throw failure;
          return {};
        }),
      ]);
      if (ticket !== sequence) return;
      const minecraft = settings.minecraft || {};
      const root = document.createElement("div");
      let initializePreview;
      const operation = pending.operation || confirmedOperation;
      if (operation) {
        const progress = operationFrame(operation);
        if (["needs_attention", "failed"].includes(operation.state)) {
          updateOperation = "";
          const acknowledge = document.createElement("button");
          acknowledge.type = "button";
          acknowledge.textContent = "Verify and keep current server";
          acknowledge.addEventListener("click", async () => {
            acknowledge.disabled = true;
            try {
              const body = new URLSearchParams({ csrf, acknowledge: "yes", operation_id: operation.operation_id });
              await request("/api/version/workspace/update", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body });
              completedOperation = null;
              await render("Current server verified and kept. Review changes again when ready.");
            } catch (failure) { state.textContent = failure.message; acknowledge.disabled = false; }
          });
          progress.appendChild(acknowledge);
        } else if (!["succeeded", "resolved"].includes(operation.state) && !updateOperation) {
          watchUpdate(operation.operation_id);
        }
        root.appendChild(progress);
      }

      if (currentTab === "software") {
        const software = card("");
        const choices = document.createElement("div");
        choices.className = "version-software-choices";
        const currentSoftware = minecraft.server_type || "paper";
        const configuredTag = minecraft.image_tag || "stable";
        let selectedSoftware = currentSoftware;
        let selectionSequence = 0;
        let softwareReview;
        let applying = false;
        const softwareCards = [];
        const panel = card("Software & container updates");
        panel.dataset.versionCombinedControls = "";
        const form = document.createElement("form");
        form.className = "version-combined-form";
        form.innerHTML = '<div class="version-channel-row"><label>Change update channel to:<select name="channel" aria-label="Container update channel"><option value="stable">Stable</option><option value="latest">Latest</option><option value="custom">Custom</option></select></label><label data-version-custom>Custom tag<input name="custom_tag" autocomplete="off" maxlength="128" pattern="[A-Za-z0-9][A-Za-z0-9._-]*"></label></div>';
        const channel = form.elements.channel;
        const customTag = form.elements.custom_tag;
        const resetChannel = () => {
          channel.value = ["stable", "latest"].includes(configuredTag) ? configuredTag : "custom";
          customTag.value = channel.value === "custom" ? configuredTag : "";
        };
        const selectedTag = () => channel.value === "custom" ? customTag.value.trim() : channel.value;
        const unsupported = () => selectedSoftware !== currentSoftware && [selectedSoftware, currentSoftware].includes("vanilla");
        const imageLabel = (value) => ({ up_to_date: "Current", updates_available: "Update available", not_applicable: "Not applicable" })[value] || "Unable to check";
        const imageStatus = line("Container image:", imageLabel(status.image_state));
        imageStatus.className += " version-container-detail";
        const compatibility = document.createElement("p");
        compatibility.dataset.versionCompatibility = "";
        const updateNotice = (result, fallback = "") => {
          const crossplayWarning = result.crossplay_enabled && (
            result.crossplay_compatible === false || result.stack_state === "waiting_for_compatibility" ||
            (["up_to_date", "updates_available"].includes(result.stack_state) && result.available && result.recommended && result.available !== result.recommended)
          );
          compatibility.className = crossplayWarning ? "notice warning" : "muted compact";
          compatibility.textContent = result.reason || (crossplayWarning ? "Bedrock cross-play compatibility could not be confirmed." : fallback);
          compatibility.hidden = !compatibility.textContent;
        };
        updateNotice(status);
        const vanillaWarning = document.createElement("p");
        vanillaWarning.className = "notice warning";
        vanillaWarning.textContent = "Requires Reset Minecraft and setup again. Multi-container support is coming in future JustVoxel updates.";
        vanillaWarning.hidden = true;
        const clearReview = () => {
          ++selectionSequence;
          softwareReview?.remove();
          softwareReview = undefined;
          form.hidden = false;
          updateNotice({});
          state.textContent = "";
        };
        const sync = () => {
          const custom = channel.value === "custom";
          customTag.required = custom;
          customTag.disabled = !custom || Boolean(pending.operation);
          vanillaWarning.hidden = !unsupported();
          panel.hidden = unsupported();
        };
        const selectSoftware = (target) => {
          if (applying || pending.operation) return;
          clearReview();
          selectedSoftware = target;
          state.textContent = "";
          softwareCards.forEach(({ node, type }) => {
            node.className = node.className.replace(/ is-selected/g, "") + (type === target ? " is-selected" : "");
            node.setAttribute("aria-pressed", String(type === target));
          });
          sync();
        };
        for (const [target, name, description] of [
          ["paper", "Paper", "Plugins and managed Bedrock cross-play."],
          ["purpur", "Purpur", "Plugins and managed Bedrock cross-play."],
          ["vanilla", "Vanilla", "Original Minecraft"],
        ]) {
          const isCurrent = target === currentSoftware;
          const selectable = ["paper", "purpur"].includes(currentSoftware) && ["paper", "purpur"].includes(target);
          const change = document.createElement("button");
          change.className = "version-software-choice server-software-card" + (isCurrent ? " is-current is-selected" : selectable ? " secondary" : "");
          const title = document.createElement("strong");
          title.textContent = name;
          const availability = document.createElement("span");
          availability.textContent = isCurrent ? "Current" : selectable ? "Switch Available" : "Switch Unavailable";
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
          if (target === "vanilla") {
            const javaOnly = document.createElement("small");
            javaOnly.textContent = "Java only, no plugins";
            copy.appendChild(javaOnly);
          }
          softwareCards.push({ node: change, type: target });
          change.append(icon, copy);
          choices.appendChild(change);
          change.type = "button";
          change.setAttribute("aria-pressed", String(isCurrent));
          change.disabled = Boolean(pending.operation);
          change.addEventListener("click", () => { selectSoftware(target); });
        }
        software.append(choices, vanillaWarning);
        root.appendChild(software);
        resetChannel();
        channel.disabled = Boolean(pending.operation);
        channel.addEventListener("change", () => { clearReview(); sync(); });
        customTag.addEventListener("input", clearReview);
        sync();
        const configuredChannel = line("Current container channel:", ({ stable: "Stable", latest: "Latest" })[configuredTag] || "Custom");
        configuredChannel.className += " version-container-detail";
        const containerStatus = document.createElement("div");
        containerStatus.className = "version-container-status";
        containerStatus.append(imageStatus, configuredChannel);
        panel.append(containerStatus, form, compatibility);
        const channelRow = form.querySelector(".version-channel-row");
        const check = document.createElement("button");
        check.type = "button";
        check.className = "secondary";
        check.textContent = "Check for updates";
        const reviewButton = document.createElement("button");
        reviewButton.type = "submit";
        reviewButton.textContent = "Review changes";
        channelRow.append(check, reviewButton);
        check.disabled = reviewButton.disabled = Boolean(pending.operation);
        const preview = async (reviewRequested) => {
          if (applying || pending.operation || unsupported()) return;
          const target = selectedSoftware;
          const imageTag = selectedTag();
          if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(imageTag)) {
            state.textContent = "Enter a valid Custom container tag.";
            return;
          }
          clearReview();
          const selectionTicket = selectionSequence;
          check.disabled = reviewButton.disabled = true;
          state.textContent = "Checking for updates…";
          try {
            const [plan, snapshot] = await Promise.all([
              request(statusURL("", "", target, imageTag)), request("/api/dashboard-status"),
            ]);
            if (selectionTicket !== selectionSequence || ticket !== sequence) return;
            imageStatus.replaceChildren(...line("Container image:", imageLabel(plan.image_state)).children);
            updateNotice(plan, plan.stack_state === "unavailable" ? "Server update information could not be verified. Try again later." : plan.update_available ? "Changes are ready to review." : "No changes were found.");
            state.textContent = "";
            if (!reviewRequested) return;
            const review = card("Review changes");
            review.dataset.versionReview = "";
            const summary = document.createElement("div");
            summary.className = "version-software-review-summary";
            summary.append(line("Server software", `${status.server_software || currentSoftware} → ${plan.server_software || target}`),
              line("Container channel", `${configuredTag} → ${imageTag}`),
              line("Minecraft version", plan.installed === plan.selected_candidate ? plan.selected_candidate : `${plan.installed || "Unknown"} → ${plan.selected_candidate || "Unknown"}`),
              line("Players online", String(snapshot?.players?.online ?? "Unknown")),
              line("Container image", imageLabel(plan.image_state)),
              line("Overall", stackLabel(plan.stack_state)));
            review.appendChild(summary);
            const actionable = plan.stack_state === "updates_available" && plan.update_available;
            const safe = actionable && plan.server_supported && (!plan.crossplay_enabled || plan.crossplay_compatible) && Boolean(plan.plan_fingerprint);
            const notice = document.createElement("p");
            if (!actionable && ["up_to_date", "waiting_for_compatibility"].includes(plan.stack_state)) {
              notice.textContent = plan.reason || "No changes were found.";
            } else if (!safe) {
              notice.className = "notice warning";
              notice.textContent = plan.reason || "A safe server update could not be verified. Review changes again.";
            } else if (plan.runtime_update_required) {
              notice.className = "notice warning";
              notice.textContent = "JustVoxel will create a cold backup and safely restart Minecraft. Online players will receive a shutdown countdown. A stopped server may start temporarily for verification and will be stopped again afterward.";
            } else {
              notice.textContent = "The selected channel uses the verified current image. Minecraft can keep its current running or stopped state.";
            }
            if (notice.textContent !== compatibility.textContent) review.appendChild(notice);
            const consent = document.createElement("input");
            consent.type = "checkbox";
            consent.setAttribute("role", "switch");
            consent.checked = false;
            if (safe) {
              const label = document.createElement("label");
              label.className = "minecraft-native-toggle system-ups-shutdown-switch";
              const confirmation = document.createElement("span");
              confirmation.textContent = plan.runtime_update_required && Number(snapshot?.players?.online || 0) > 0
                ? "Confirm changes and allow interruption of online players."
                : "Confirm changes";
              label.append(confirmation, consent);
              review.appendChild(label);
            }
            const cancel = document.createElement("button");
            cancel.type = "button";
            cancel.className = "secondary";
            cancel.textContent = "Cancel review";
            cancel.addEventListener("click", () => { resetChannel(); selectSoftware(currentSoftware); });
            const apply = document.createElement("button");
            apply.type = "button";
            apply.textContent = "Apply changes";
            apply.disabled = true;
            consent.addEventListener("change", () => { apply.disabled = !safe || !consent.checked; });
            apply.addEventListener("click", async () => {
              if (!safe || !consent.checked || applying || apply.disabled) return;
              applying = true;
              apply.disabled = cancel.disabled = consent.disabled = true;
              softwareCards.forEach(({ node }) => { node.disabled = true; });
              state.textContent = "Starting server update…";
              try {
                const body = new URLSearchParams({ csrf, server_type: target, image_tag: imageTag, plan_fingerprint: plan.plan_fingerprint, confirm_players: consent.checked ? "yes" : "no" });
                const result = await request("/api/version/workspace/update", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body });
                if (!result.operation?.operation_id) throw new Error("Server update tracking is unavailable. Refresh Version to check its status.");
                // Only the reviewed plan can establish a container-only update.
                // Missing or inconclusive component evidence keeps the generic label.
                const containerOnly = plan.image_state === "updates_available"
                  && Boolean(plan.installed && plan.selected_candidate && plan.installed === plan.selected_candidate)
                  && plan.minecraft_state === "up_to_date"
                  && [plan.geyser_state, plan.floodgate_state, plan.viaversion_state]
                    .every((component) => ["up_to_date", "disabled", "not_applicable", "managed_automatically"].includes(component));
                operationKinds.set(result.operation.operation_id, target !== currentSoftware ? "switch" : containerOnly ? "container" : "server");
                updateOperation = result.operation.operation_id;
                await render();
                watchUpdate(result.operation.operation_id);
              } catch (failure) {
                // A lost response may still have started maintenance. Reconnect before another review.
                await render(failure.message);
              }
            });
            const reviewActions = document.createElement("div");
            reviewActions.className = "action-row version-software-review-actions";
            reviewActions.append(cancel, apply);
            review.appendChild(reviewActions);
            softwareReview = review;
            panel.insertBefore(review, compatibility);
            form.hidden = true;
            state.textContent = "";
          } catch (failure) {
            if (selectionTicket === selectionSequence && ticket === sequence) state.textContent = failure.message;
          } finally {
            if (!applying) check.disabled = reviewButton.disabled = Boolean(pending.operation);
          }
        };
        check.addEventListener("click", () => preview(false));
        form.addEventListener("submit", (event) => { event.preventDefault(); preview(true); });
        root.appendChild(panel);
      } else {
        const panel = card("Minecraft Version");
        const grid = document.createElement("div");
        grid.className = "version-status-grid";
        grid.append(line("Installed", status.installed || "Not detected"), line("Newest available", status.available ? `${status.available} · ${releaseLabel(status.available_channel)}` : "Unavailable"), line("Recommended", status.recommended), line("Configured", status.configured_version === "LATEST" ? "Follows new versions" : status.configured_version), line("Server software", status.server_software || "Paper"), line("Container channel", status.image_tag), line("Version", policyLabel(minecraft.version_mode)), line("Overall", stackLabel(status.stack_state)));
        grid.append(line("Minecraft update", stackLabel(status.minecraft_state)), line("Geyser", stackLabel(status.geyser_state)), line("Floodgate", stackLabel(status.floodgate_state)), line("ViaVersion", stackLabel(status.viaversion_state)));
        if (status.crossplay_enabled && status.policy === "recommended" && status.available && status.recommended && status.available !== status.recommended) {
          const newVersion = line("New Minecraft version", `${status.available} · Waiting for cross-play support`);
          newVersion.className += " version-new-minecraft";
          grid.appendChild(newVersion);
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
      state.textContent = operation && message === operation.status ? "" : message;
    } catch (failure) { if (ticket === sequence) state.textContent = failure.message; }
  };
  const windowControl = setupWorkspaceWindow(versionDialog, { onOpen: () => render() });
  versionLauncher.addEventListener("click", () => { document.querySelector("[data-control-center]")?.removeAttribute("open"); windowControl?.open(); });
  versionDialog.querySelector("[data-version-close]").addEventListener("click", () => windowControl?.close());
  versionDialog.querySelector("[data-version-refresh]").addEventListener("click", () => render());
  tabs.forEach((tab) => tab.addEventListener("click", () => { currentTab = tab.dataset.versionTab; tabs.forEach((item) => item.setAttribute("aria-selected", String(item === tab))); render(); }));
  if (readWorkspaceWindowState("version").open) windowControl?.open();
}
