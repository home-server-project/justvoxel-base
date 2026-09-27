const versionLauncher = document.querySelector("[data-version-open]");
const versionDialog = document.querySelector("[data-version-workspace-dialog]");
if (versionLauncher && versionDialog) {
  const content = versionDialog.querySelector("[data-version-content]");
  const state = versionDialog.querySelector("[data-version-state]");
  const tabs = Array.from(versionDialog.querySelectorAll("[data-version-tab]"));
  const csrf = document.querySelector("[data-minecraft-workspace-csrf]")?.value || "";
  let currentTab = "software";
  let sequence = 0;
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
      const [settings, status] = await Promise.all([
        request("/api/minecraft/workspace/settings"), request(statusURL()),
      ]);
      if (ticket !== sequence) return;
      const minecraft = settings.minecraft || {};
      const root = document.createElement("div");
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
        grid.append(line("Installed", status.installed || "Not detected"), line("Newest available", status.available ? `${status.available} · ${releaseLabel(status.available_channel)}` : "Unavailable"), line("Recommended", status.recommended), line("Configured", status.configured_version === "LATEST" ? "Follows new versions" : status.configured_version), line("Server software", status.server_software || "Paper"), line("Container channel", status.image_tag), line("Version policy", policyLabel(minecraft.version_mode)), line("Update", status.update_available ? "Available" : "No update confirmed"));
        panel.appendChild(grid);
        const compatibility = document.createElement("p");
        compatibility.className = status.crossplay_enabled && !status.crossplay_compatible ? "notice warning" : "muted compact";
        compatibility.textContent = status.reason || (status.crossplay_enabled ? `Paper supported · Geyser/Floodgate supports ${status.geyser_supported_version || "an unknown version"}.` : status.paper_supported ? "Paper supports the available version." : "Compatibility could not be confirmed.");
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
