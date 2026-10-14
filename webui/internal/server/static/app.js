// Identity fallback belongs to the active reviewed workflow or administrator session.
const saveRequiredMinecraftIdentity = (csrf) => new Promise((resolve) => {
  const dialog = document.createElement("dialog");
  dialog.className = "system-action-dialog instance-id-dialog";
  const content = document.createElement("div");
  content.className = "system-action-dialog-content";
  const title = document.createElement("h2");
  title.textContent = "Instance ID could not be generated.";
  const form = document.createElement("form");
  form.className = "instance-id-form";
  const label = document.createElement("label");
  label.textContent = "jv- ";
  const input = document.createElement("input");
  input.required = true;
  input.minLength = 6;
  input.maxLength = 12;
  input.pattern = "[A-Za-z0-9]{6,12}";
  input.autocomplete = "off";
  input.setAttribute("aria-label", "Instance ID suffix");
  input.title = "Use 6-12 letters and numbers after jv-.";
  const save = document.createElement("button");
  save.type = "submit";
  save.textContent = "Save";
  const feedback = document.createElement("p");
  feedback.textContent = "Valid input: 6-12 letters and numbers.";
  feedback.setAttribute("role", "status");
  label.append(input);
  form.append(label, save);
  content.append(title, form, feedback);
  dialog.append(content);
  document.body.append(dialog);
  dialog.addEventListener("cancel", (event) => event.preventDefault());
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (!form.reportValidity()) return;
    save.disabled = true;
    try {
      const response = await fetch("/api/minecraft/workspace/identity/manual", {
        method: "POST", credentials: "same-origin", cache: "no-store",
        headers: {Accept: "application/json", "Content-Type": "application/x-www-form-urlencoded"},
        body: new URLSearchParams({csrf, suffix: input.value}).toString(),
      });
      const result = await response.json();
      if (!response.ok || result.status !== "available" || !result.id) {
        throw new Error(result.error || "Instance ID was not saved.");
      }
      dialog.close();
      dialog.remove();
      resolve();
    } catch (error) {
      feedback.textContent = error?.message || "Instance ID could not be saved.";
      save.disabled = false;
    }
  });
  dialog.showModal();
  input.focus();
});
const resumeReviewedIdentitySubmission = async (send, csrf) => {
  const response = await send();
  if (response.status !== 409 || !response.headers.get("Content-Type")?.includes("application/json")) return response;
  const result = await response.clone().json();
  if (result.code !== "identity_required") return response;
  await saveRequiredMinecraftIdentity(csrf);
  return send();
};
const identityDashboard = document.querySelector('[data-instance-required="true"]');
if (identityDashboard) {
  void saveRequiredMinecraftIdentity(document.querySelector('input[name="csrf"]')?.value || "");
}


const enhancePasswordFields = (root = document) => {
  root.querySelectorAll('input[type="password"]:not([data-password-reveal-ready])').forEach((input) => {
    input.dataset.passwordRevealReady = "true";
    const shell = document.createElement("span");
    shell.className = "password-field-shell";
    input.parentNode.insertBefore(shell, input);
    shell.appendChild(input);
    const button = document.createElement("button");
    button.type = "button";
    button.className = "password-reveal-button";
    button.setAttribute("aria-label", "Show password");
    button.setAttribute("title", "Show password");
    button.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M2.5 12s3.5-6 9.5-6 9.5 6 9.5 6-3.5 6-9.5 6-9.5-6-9.5-6Z"></path><circle cx="12" cy="12" r="2.8"></circle></svg>';
    button.addEventListener("click", () => {
      const showing = input.type === "text";
      input.type = showing ? "password" : "text";
      button.classList.toggle("is-showing", !showing);
      button.setAttribute("aria-label", showing ? "Show password" : "Hide password");
      button.setAttribute("title", showing ? "Show password" : "Hide password");
    });
    shell.appendChild(button);
    if (input.autocomplete === "new-password" && !/confirm/i.test(input.name)) {
      const strength = document.createElement("span");
      strength.className = "password-strength is-unacceptable";
      strength.setAttribute("role", "status");
      strength.textContent = "Strength: Not acceptable";
      shell.after(strength);
      input.addEventListener("input", () => {
        const value = input.value;
        const classes = [/[a-z]/, /[A-Z]/, /[0-9]/, /[^a-zA-Z0-9]/].filter((pattern) => pattern.test(value)).length;
        const minimum = Math.max(8, input.minLength > 0 ? input.minLength : 8);
        const level = value.length < minimum || classes < 2 ? "unacceptable" : value.length >= 12 && classes >= 3 ? "strong" : "acceptable";
        strength.className = "password-strength is-" + level;
        strength.textContent = "Strength: " + (level === "unacceptable" ? "Not acceptable" : level === "strong" ? "Strong" : "Acceptable");
      });
    }
  });
};

const initDestructiveConfirmations = (root = document) => {
  root.querySelectorAll("[data-destructive-confirmation]:not([data-destructive-ready])").forEach((control) => {
    control.dataset.destructiveReady = "true";
    const slider = control.querySelector("[data-destructive-slider]");
    const shell = control.querySelector("[data-destructive-slider-shell]");
    const text = control.querySelector("[data-destructive-slider-text]");
    const toggleRow = control.querySelector("[data-destructive-toggle-row]");
    const toggle = control.querySelector("[data-destructive-toggle]");
    const value = control.querySelector("[data-destructive-value]");
    const button = control.closest("form")?.querySelector("[data-destructive-submit]") || control.parentElement?.querySelector("[data-destructive-submit]");
    const idleText = control.dataset.destructiveIdle || "Slide to confirm";

    const sync = () => {
      const progress = Math.max(0, Math.min(100, Number(slider?.value || 0)));
      shell?.style.setProperty("--confirm-progress", String(progress / 100));
      const armed = progress >= 100;
      shell?.classList.toggle("is-armed", armed);
      if (text) text.textContent = armed ? (control.dataset.destructiveArmed || "Ready to confirm") : idleText;
      if (toggleRow) toggleRow.hidden = !armed;
      if (!armed && toggle) toggle.checked = false;
      const confirmed = armed && Boolean(toggle?.checked);
      if (value) value.value = confirmed ? (control.dataset.confirmValue || "") : "";
      if (button) button.disabled = !confirmed;
      slider?.setAttribute("aria-valuetext", armed ? "Ready for final confirmation" : progress + " percent");
    };
    slider?.addEventListener("input", sync);
    slider?.addEventListener("change", sync);
    toggle?.addEventListener("change", sync);
    control._justVoxelDestructiveSync = sync;
    sync();
  });
};

const initializeSharedWebUIControls = (root = document) => {
  enhancePasswordFields(root);
  initDestructiveConfirmations(root);
};
initializeSharedWebUIControls(document);
new MutationObserver((mutations) => {
  mutations.forEach((mutation) => mutation.addedNodes.forEach((node) => {
    if (node.nodeType !== Node.ELEMENT_NODE) return;
    initializeSharedWebUIControls(node);
  }));
}).observe(document.documentElement, { childList: true, subtree: true });

document.addEventListener("submit", (event) => {
  const form = event.target.closest(".action-form");
  if (!form) return;

  const submitted = form.querySelector('button[type="submit"]');
  document.querySelectorAll(".action-form button").forEach((button) => {
    button.disabled = true;
  });

  if (submitted && form.dataset.progress) {
    submitted.textContent = form.dataset.progress;
  }
});

const navigationGroups = Array.from(document.querySelectorAll(".nav-group"));
if (navigationGroups.length > 0) {
  navigationGroups.forEach((group) => {
    group.addEventListener("toggle", () => {
      if (!group.open) return;
      navigationGroups.forEach((other) => {
        if (other !== group) other.open = false;
      });
    });
  });

  document.addEventListener("click", (event) => {
    if (event.target.closest(".primary-nav")) return;
    navigationGroups.forEach((group) => {
      group.open = false;
    });
  });

  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape") return;
    navigationGroups.forEach((group) => {
      group.open = false;
    });
    const active = document.activeElement;
    if (active && active.closest && active.closest(".nav-menu")) {
      const group = active.closest(".nav-group");
      const trigger = group && group.querySelector(".nav-trigger");
      if (trigger) trigger.focus();
    }
  });

  const path = window.location.pathname;
  const brand = document.querySelector(".brand-link");
  if (brand) {
    if (path === "/") brand.setAttribute("aria-current", "page");
    else brand.removeAttribute("aria-current");
  }

  let currentGroup = "";
  let currentHref = "";
  if (path === "/password") {
    currentGroup = "administration";
    currentHref = "/password";
  }

  if (currentGroup) {
    const group = document.querySelector(`[data-nav-group="${currentGroup}"]`);
    const trigger = group && group.querySelector(".nav-trigger");
    if (trigger) trigger.setAttribute("aria-current", "page");
  }
  if (currentHref) {
    document.querySelectorAll(".nav-menu a").forEach((link) => {
      if (link.getAttribute("href") === currentHref) {
        link.setAttribute("aria-current", "page");
      }
    });
  }
}

const dashboard = document.querySelector("#dashboard[data-dashboard-status]");
if (dashboard) {
  const statusURL = dashboard.dataset.dashboardStatus;
  const sessionURL = dashboard.dataset.sessionInfo;
  let pendingAction = dashboard.dataset.pendingAction || "";
  let restartSawTransition = false;

  const text = (id, value) => {
    const el = document.getElementById(id);
    if (el) el.textContent = value;
  };

  const loadRole = async () => {
    try {
      const response = await fetch(sessionURL, {
        method: "GET",
        credentials: "same-origin",
        headers: { Accept: "application/json" },
        cache: "no-store",
      });
      if (response.status === 401) {
        window.location.assign("/login");
        return;
      }
      if (!response.ok) return;
      const identity = await response.json();
      const role = String(identity.role || "").toLowerCase();
      if (!["administrator", "operator", "viewer"].includes(role)) return;
      document.body.classList.remove("role-pending");
      document.body.classList.add(`role-${role}`);
      text("session-role", role);
    } catch (_) {
      // Fail closed: role-gated controls remain hidden until identity is known.
    }
  };

  const actionProgress = (action) => {
    if (action === "start") return "Starting…";
    if (action === "stop") return "Stopping…";
    if (action === "restart") return "Restarting…";
    return "Working…";
  };

  const setButtons = (status) => {
    const running = status.minecraft.state === "Running";
    document.querySelectorAll("[data-minecraft-action]").forEach((form) => {
      const button = form.querySelector('button[type="submit"]');
      if (!button) return;
      if (pendingAction) {
        button.disabled = true;
        return;
      }
      const action = form.dataset.minecraftAction;
      if (action === "start") {
        button.disabled = !status.minecraft.configured || running;
      } else {
        button.disabled = !running;
      }
    });
  };

  const updatePendingState = (status, players) => {
    if (!pendingAction) return;

    if (status.minecraft.state === "Failed") {
      pendingAction = "";
      return;
    }

    if (pendingAction === "start") {
      if (status.minecraft.state === "Running" && players.state === "running") {
        pendingAction = "";
      }
      return;
    }

    if (pendingAction === "stop") {
      if (status.minecraft.state === "Stopped") {
        pendingAction = "";
      }
      return;
    }

    if (pendingAction === "restart") {
      if (status.minecraft.state !== "Running" || players.state !== "running") {
        restartSawTransition = true;
      }
      if (restartSawTransition && status.minecraft.state === "Running" && players.state === "running") {
        pendingAction = "";
      }
    }
  };

  const renderDashboardAttention = (attention) => {
    const panel = document.getElementById("dashboard-attention");
    const title = document.getElementById("dashboard-attention-title");
    const status = document.getElementById("dashboard-attention-status");
    const action = document.getElementById("dashboard-attention-action");
    if (!panel) return;
    panel.hidden = !attention;
    if (!attention) return;
    if (title) title.textContent = attention.title || "Administrator attention required";
    if (status) status.textContent = attention.status || "";
    if (action) {
      action.hidden = !attention.action;
      action.href = attention.action || "#";
      action.textContent = attention.label || "Review";
    }
  };

  const renderDashboard = (snapshot) => {
    const { status, players, attention } = snapshot;
    updatePendingState(status, players);
    renderDashboardAttention(attention || null);
    const setupArea = document.querySelector("[data-dashboard-setup-area]");
    if (setupArea) setupArea.hidden = Boolean(status.minecraft.configured);
    dashboard.dataset.configured = String(Boolean(status.minecraft.configured));

    const displayedState = pendingAction ? actionProgress(pendingAction) : status.minecraft.state;
    text("minecraft-control-state", displayedState);

    setButtons(status);

    if (!pendingAction && window.location.search.includes("result=")) {
      window.history.replaceState(null, "", window.location.pathname);
    }
  };

  const refreshDashboard = async () => {
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
      if (!response.ok) return;
      renderDashboard(await response.json());
    } catch (_) {
      // Keep the last known dashboard state and try again on the next poll.
    }
  };

  if (pendingAction) {
    const progress = actionProgress(pendingAction);
    text("minecraft-state-summary", progress);
    text("minecraft-control-state", progress);
    document.querySelectorAll("[data-minecraft-action] button").forEach((button) => {
      button.disabled = true;
    });
  }

  loadRole();
  refreshDashboard();
  window.setInterval(refreshDashboard, 5000);
  window.addEventListener("justvoxel:minecraft-reset-complete", () => void refreshDashboard());
}




const quickLook = document.querySelector("[data-quick-look]");
const quickLookToggle = document.querySelector("[data-quick-look-toggle]");
if (quickLook && quickLookToggle) {
  const identityURL = "/api/session-info";
  const statusURL = "/api/dashboard-status";
  const actionMenus = Array.from(quickLook.querySelectorAll("[data-quick-look-actions]"));
  let identity = null;
  let refreshTimer = null;
  let lastMonitorData = null;
  const topbar = document.querySelector(".topbar");
  const quickLookPanel = quickLook.querySelector("[data-quick-look-panel]");

  const syncQuickLookTop = () => {
    const bounds = topbar?.getBoundingClientRect();
    quickLook.style.top = Math.round(bounds?.bottom || 48) + "px";
    if (bounds) {
      quickLook.style.left = bounds.left + "px";
      quickLook.style.width = bounds.width + "px";
      quickLook.style.right = "auto";
    }
  };
  syncQuickLookTop();
  window.addEventListener("resize", syncQuickLookTop);

  const stateKey = (username) => `justvoxel-quick-look-v1:${encodeURIComponent(username)}`;

  const setOpen = (open, persist = true) => {
    quickLook.classList.toggle("is-open", open);
    quickLookToggle.setAttribute("aria-expanded", open ? "true" : "false");
    if (quickLookPanel) {
      quickLookPanel.inert = !open;
      quickLookPanel.setAttribute("aria-hidden", open ? "false" : "true");
    }
    if (identity?.role === "administrator" && persist) {
      try {
        window.localStorage.setItem(stateKey(identity.username), open ? "open" : "closed");
      } catch (_) {
        // Drawer state persistence is optional.
      }
    }
    if (open) {
      refreshQuickLook();
      if (!refreshTimer) refreshTimer = window.setInterval(refreshQuickLook, 5000);
    } else if (refreshTimer) {
      window.clearInterval(refreshTimer);
      refreshTimer = null;
    }
  };

  const setValue = (selector, value) => {
    const node = quickLook.querySelector(selector);
    if (node) node.textContent = value || "—";
  };

  const backupCreate = quickLook.querySelector("[data-quick-look-backup-create]");
  const backupCSRF = quickLook.querySelector("[data-quick-look-backup-csrf]");
  backupCreate?.addEventListener("click", async () => {
    if (backupCreate.disabled) return;
    backupCreate.disabled = true;
    backupCreate.textContent = "Starting…";
    try {
      const response = await fetch("/api/backups/manual", {
        method: "POST",
        credentials: "same-origin",
        headers: { Accept: "application/json" },
        body: new URLSearchParams({ csrf: backupCSRF?.value || "" }),
      });
      if (response.status === 401) {
        window.location.assign("/login");
        return;
      }
      const result = await response.json();
      setValue("[data-quick-look-backup]", response.ok && result.ok
        ? result.message
        : result.error || "Manual backup could not be started.");
    } catch (_) {
      setValue("[data-quick-look-backup]", "Manual backup could not be started.");
    } finally {
      backupCreate.disabled = false;
      backupCreate.textContent = "Create backup";
    }
  });

  const setActionAvailability = (status) => {
    const running = status?.minecraft?.state === "Running";
    quickLook.querySelectorAll("[data-minecraft-action]").forEach((form) => {
      const button = form.querySelector('button[type="submit"]');
      if (!button) return;
      const action = form.dataset.minecraftAction;
      button.disabled = action === "start"
        ? !status.minecraft.configured || running
        : !running;
    });
  };

  const renderQuickLook = (snapshot, providers = []) => {
    const status = snapshot?.status || {};
    const minecraft = status.minecraft || {};
    const players = snapshot?.players || {};
    const system = status.system || {};
    const backup = status.backup || {};

    setValue("[data-quick-look-minecraft]", minecraft.state);
    setValue("[data-quick-look-version]", minecraft.version);
    setValue(
      "[data-quick-look-players]",
      minecraft.configured ? `${players.online ?? 0} / ${players.max ?? minecraft.max_players ?? "—"}` : "Not configured",
    );
    setValue("[data-quick-look-system]", system.health);
    setValue("[data-quick-look-ipv4]", system.ipv4);
    const overlays = quickLook.querySelector("[data-quick-look-overlays]");
    if (overlays) {
      overlays.replaceChildren();
      [["Tailscale", "tailscale"], ["NetBird", "netbird"], ["Playit", "playit"]].forEach(([label, id]) => {
        const provider = providers.find((item) => item.id === id);
        let value = provider?.summary || "Unavailable";
        if (provider?.connected && id !== "playit" && provider.ip) value = provider.ip.split("/")[0];
        if (id === "playit" && provider?.connected && Array.isArray(provider.tunnels)) {
          const count = provider.tunnels.filter((tunnel) => !tunnel.is_disabled).length;
          value = count === 0 ? "No tunnels" : `${count} tunnel${count === 1 ? "" : "s"}`;
        }
        const line = document.createElement("span");
        const name = document.createElement("span");
        name.textContent = label;
        const address = document.createElement("strong");
        address.textContent = value;
        line.append(name, address);
        overlays.appendChild(line);
      });
    }
    setValue("[data-quick-look-backup]", backup.enabled ? "Enabled" : "Disabled");
    setActionAvailability(status);
  };

  async function refreshQuickLook() {
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
      if (!response.ok) return;
      const snapshot = await response.json();
      let providers = [];
      try {
        const remote = await fetch("/api/network/remote-access", {
          credentials: "same-origin", headers: { Accept: "application/json" }, cache: "no-store",
        });
        if (remote.ok) providers = (await remote.json()).providers || [];
      } catch (_) {
        // Missing provider evidence is presented conservatively.
      }
      renderQuickLook(snapshot, providers);
    } catch (_) {
      // Keep the last values and retry on the next refresh.
    }
  }

  const loadQuickLookIdentity = async () => {
    try {
      const response = await fetch(identityURL, {
        method: "GET",
        credentials: "same-origin",
        headers: { Accept: "application/json" },
        cache: "no-store",
      });
      if (response.status === 401) {
        window.location.assign("/login");
        return;
      }
      if (!response.ok) return;

      const result = await response.json();
      const role = String(result.role || "").toLowerCase();
      const username = String(result.username || "");
      if (!username || !["administrator", "operator", "viewer"].includes(role)) return;

      identity = { username, role };
      document.body.classList.remove("role-pending");
      document.body.classList.add(`role-${role}`);

      if (role === "viewer" || role === "operator") {
        setOpen(true, false);
        return;
      }

      let saved = "closed";
      try {
        saved = window.localStorage.getItem(stateKey(username)) || "closed";
      } catch (_) {
        saved = "closed";
      }
      setOpen(saved === "open", false);
    } catch (_) {
      // Keep role-gated controls closed if identity cannot be loaded.
    }
  };

  quickLookToggle.addEventListener("click", () => {
    setOpen(!quickLook.classList.contains("is-open"));
  });

  document.addEventListener("click", (event) => {
    actionMenus.forEach((menu) => {
      if (menu.open && !menu.contains(event.target)) menu.open = false;
    });
  });

  loadQuickLookIdentity();
}

const controlCenter = document.querySelector("[data-control-center]");
let clockPreferences = { time: "12", date: "mdy" };
window.JustVoxelClockDisplay = {
  update: (profile) => {
    if (["12", "24"].includes(profile.time_format)) clockPreferences.time = profile.time_format;
    if (["mdy", "dmy"].includes(profile.date_format)) clockPreferences.date = profile.date_format;
    window.dispatchEvent(new Event("justvoxel-clock-display"));
  },
};

const topbarClock = document.querySelector("[data-topbar-clock]");
if (topbarClock) {
  const initialSystemTime = Number(topbarClock.dataset.systemNow);
  let systemClockOffset = Number.isFinite(initialSystemTime) && initialSystemTime > 0 ? initialSystemTime - Date.now() : 0;
  const updateTopbarClock = () => {
    const now = new Date(Date.now() + systemClockOffset);
    const zone = topbarClock.dataset.systemTimezone || undefined;
    const clockTime = new Intl.DateTimeFormat("en-US", { hour: "2-digit", minute: "2-digit", ...(clockPreferences.time === "12" ? { hour12: true } : { hourCycle: "h23" }), timeZone: zone }).format(now);
    const parts = Object.fromEntries(new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", year: "numeric", timeZone: zone }).formatToParts(now).map(({ type, value }) => [type, value]));
    const clockDate = clockPreferences.date === "dmy" ? `${parts.day} ${parts.month} ${parts.year}` : `${parts.month} ${parts.day} ${parts.year}`;
    const time = document.createElement("span");
    time.textContent = clockTime;
    const date = document.createElement("span");
    date.className = "topbar-clock-date";
    date.textContent = clockDate;
    const separator = document.createElement("span");
    separator.className = "topbar-clock-separator";
    separator.textContent = " · ";
    topbarClock.replaceChildren(time, separator, date);
    topbarClock.setAttribute("aria-label", `${clockTime} · ${clockDate}`);
    topbarClock.title = new Intl.DateTimeFormat([], {
      ...(clockPreferences.time === "12" ? { hour12: true } : { hourCycle: "h23" }),
      weekday: "long",
      year: "numeric",
      month: "long",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
      timeZone: topbarClock.dataset.systemTimezone || undefined,
    }).format(now);
  };
  const refreshClockDisplay = async () => {
    try {
      const response = await fetch("/api/session-info", { credentials: "same-origin", cache: "no-store" });
      if (response.ok) window.JustVoxelClockDisplay.update(await response.json());
    } catch (_) { /* Retry shared settings on the next refresh. */ }
  };
  refreshClockDisplay();
  window.addEventListener("focus", refreshClockDisplay);
  updateTopbarClock();
  window.setInterval(updateTopbarClock, 30000);
  window.addEventListener("justvoxel-clock-display", updateTopbarClock);
  window.addEventListener("justvoxel-timezone", (event) => {
    const { zone, localDate, localTime } = event.detail || {};
    if (/^\d{4}-\d{2}-\d{2}$/.test(localDate || "") && /^\d{2}:\d{2}$/.test(localTime || "") && zone) {
      const now = new Date(Date.now() + systemClockOffset);
      const parts = Object.fromEntries(new Intl.DateTimeFormat("en-US", {
        timeZone: zone, year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hourCycle: "h23",
      }).formatToParts(now).map(({ type, value }) => [type, value]));
      const displayedMinute = Date.parse(`${parts.year}-${parts.month}-${parts.day}T${parts.hour}:${parts.minute}:00Z`);
      const systemMinute = Date.parse(`${localDate}T${localTime}:00Z`);
      if (Number.isFinite(systemMinute) && Number.isFinite(displayedMinute)) systemClockOffset += systemMinute - displayedMinute;
    }
    updateTopbarClock();
  });
}

if (controlCenter) {
  const brand = document.querySelector(".brand-link");
  if (brand) {
    if (window.location.pathname === "/") brand.setAttribute("aria-current", "page");
    else brand.removeAttribute("aria-current");
  }

  document.querySelectorAll(".control-tile").forEach((link) => {
    const target = new URL(link.href, window.location.origin);
    const samePath = target.pathname === window.location.pathname;
    const sameHash = !target.hash || target.hash === window.location.hash;
    if (samePath && sameHash) link.setAttribute("aria-current", "page");
  });

  document.addEventListener("click", (event) => {
    if (!controlCenter.open || event.target.closest("[data-control-center]")) return;
    controlCenter.open = false;
  });

  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || !controlCenter.open) return;
    controlCenter.open = false;
    const trigger = controlCenter.querySelector(".control-center-trigger");
    if (trigger) trigger.focus();
  });
}

const systemPower = document.querySelector("[data-system-power]");
const systemDialog = document.querySelector("[data-system-action-dialog]");
if (systemPower && systemDialog) {
  const csrfInput = document.querySelector("[data-system-actions-csrf]");
  const menuError = systemPower.querySelector("[data-system-action-error]");
  const firmwareButton = systemPower.querySelector('[data-system-action="firmware-reboot"]');
  const actionButtons = Array.from(systemPower.querySelectorAll("[data-system-action]"));
  const dialogTitle = systemDialog.querySelector("[data-system-action-title]");
  const dialogMessage = systemDialog.querySelector("[data-system-action-message]");
  const dialogPlayers = systemDialog.querySelector("[data-system-action-players]");
  const dialogUpdate = systemDialog.querySelector("[data-system-action-update]");
  const dialogError = systemDialog.querySelector("[data-system-action-dialog-error]");
  const cancelButton = systemDialog.querySelector("[data-system-action-cancel]");
  const confirmButton = systemDialog.querySelector("[data-system-action-confirm]");

  let selectedAction = "";
  let confirmPlayers = false;
  let latestStatus = null;

  const actionCopy = {
    reboot: {
      title: "Reboot JustVoxel?",
      message: "Minecraft will be stopped safely first when it is running. The server will then reboot.",
      confirm: "Reboot",
      working: "Rebooting JustVoxel…",
    },
    poweroff: {
      title: "Power off JustVoxel?",
      message: "Minecraft will be stopped safely first when it is running. The server will then power off.",
      confirm: "Power off",
      working: "Powering off JustVoxel…",
    },
    "firmware-reboot": {
      title: "Reboot to UEFI/BIOS?",
      message: "Minecraft will be stopped safely first. The server will reboot into the physical machine's UEFI/BIOS setup.",
      confirm: "Reboot to UEFI/BIOS",
      working: "Rebooting to UEFI/BIOS…",
    },
  };

  const setMenuAvailable = (available) => {
    actionButtons.forEach((button) => {
      button.disabled = !available;
    });
  };

  const loadSystemActionsStatus = async () => {
    setMenuAvailable(false);
    if (menuError) menuError.hidden = true;
    if (firmwareButton) firmwareButton.hidden = true;
    try {
      const response = await fetch("/api/system-actions", {
        method: "GET",
        credentials: "same-origin",
        headers: { Accept: "application/json" },
        cache: "no-store",
      });
      if (response.status === 401) {
        window.location.assign("/login");
        return false;
      }
      if (response.status === 403) {
        if (controlCenter) controlCenter.open = false;
        return false;
      }
      if (!response.ok) throw new Error("status unavailable");
      latestStatus = await response.json();
      const firmwareAvailable =
        latestStatus.variant === "hws" &&
        latestStatus.firmware &&
        latestStatus.firmware.available === true;
      if (firmwareButton) firmwareButton.hidden = !firmwareAvailable;
      actionButtons.forEach((button) => {
        button.disabled = button === firmwareButton && !firmwareAvailable;
      });
      return true;
    } catch (_) {
      if (menuError) menuError.hidden = false;
      return false;
    }
  };

  const resetDialog = () => {
    confirmPlayers = false;
    if (dialogPlayers) {
      dialogPlayers.hidden = true;
      dialogPlayers.replaceChildren();
    }
    if (dialogError) {
      dialogError.hidden = true;
      dialogError.textContent = "";
    }
    if (dialogUpdate) dialogUpdate.hidden = true;
    if (cancelButton) cancelButton.hidden = false;
    if (confirmButton) confirmButton.hidden = false;
  };

  const openConfirmation = (action) => {
    const copy = actionCopy[action];
    if (!copy) return;
    selectedAction = action;
    resetDialog();
    if (dialogTitle) dialogTitle.textContent = copy.title;
    if (dialogMessage) dialogMessage.textContent = copy.message;
    if (confirmButton) {
      confirmButton.textContent = copy.confirm;
      confirmButton.disabled = false;
    }
    if (dialogUpdate) {
      dialogUpdate.hidden = !(latestStatus && latestStatus.staged_update && action !== "poweroff");
    }
    if (controlCenter) controlCenter.open = false;
    systemDialog.showModal();
  };

  const showPlayerConfirmation = (result) => {
    confirmPlayers = true;
    const copy = actionCopy[selectedAction];
    if (dialogTitle) dialogTitle.textContent = "Players are online";
    if (dialogMessage) {
      const count = Number(result.online || 0);
      const noun = count === 1 ? "player is" : "players are";
      dialogMessage.textContent =
        String(count) + " " + noun + " online. Continue with the normal graceful shutdown warning and " + copy.confirm.toLowerCase() + "?";
    }
    if (dialogPlayers) {
      dialogPlayers.replaceChildren();
      (Array.isArray(result.players) ? result.players : []).forEach((name) => {
        const chip = document.createElement("span");
        chip.className = "player-chip";
        chip.textContent = name;
        dialogPlayers.appendChild(chip);
      });
      dialogPlayers.hidden = dialogPlayers.childElementCount === 0;
    }
    if (confirmButton) {
      confirmButton.textContent = selectedAction === "poweroff" ? "Power off anyway" : "Reboot anyway";
      confirmButton.disabled = false;
    }
  };

  const showActionError = (message) => {
    if (dialogError) {
      dialogError.textContent = message || "The system action was not accepted.";
      dialogError.hidden = false;
    }
    if (cancelButton) cancelButton.hidden = false;
    if (confirmButton) confirmButton.disabled = false;
  };

  const submitSystemAction = async () => {
    const copy = actionCopy[selectedAction];
    if (!copy || !csrfInput) return;
    if (confirmButton) {
      confirmButton.disabled = true;
      confirmButton.textContent = "Working…";
    }
    if (dialogError) dialogError.hidden = true;

    const body = new URLSearchParams();
    body.set("csrf", csrfInput.value);
    if (confirmPlayers) body.set("confirm_players", "yes");

    try {
      const response = await fetch("/api/system-actions/" + selectedAction, {
        method: "POST",
        credentials: "same-origin",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/x-www-form-urlencoded",
        },
        body: body.toString(),
      });
      if (response.status === 401) {
        window.location.assign("/login");
        return;
      }
      if (response.status === 403) {
        window.location.assign("/password");
        return;
      }

      let result = {};
      try {
        result = await response.json();
      } catch (_) {
        // A structured error is preferred, but the UI still fails safely.
      }

      if (response.ok && result.confirmation_required && result.reason === "players_online") {
        showPlayerConfirmation(result);
        return;
      }
      if (response.status === 202 && result.accepted) {
        if (dialogTitle) dialogTitle.textContent = copy.working;
        if (dialogMessage) dialogMessage.textContent = result.message || "JustVoxel accepted the system action.";
        if (dialogPlayers) dialogPlayers.hidden = true;
        if (dialogUpdate) dialogUpdate.hidden = true;
        if (cancelButton) cancelButton.hidden = true;
        if (confirmButton) confirmButton.hidden = true;
        return;
      }

      showActionError(result.message || result.reason || "The system action was not accepted.");
    } catch (_) {
      showActionError("The system action service is unavailable. Nothing was submitted again automatically.");
    }
  };

  if (controlCenter) {
    controlCenter.addEventListener("toggle", () => {
      if (controlCenter.open) loadSystemActionsStatus();
    });
  } else {
    loadSystemActionsStatus();
  }

  actionButtons.forEach((button) => {
    button.addEventListener("click", () => openConfirmation(button.dataset.systemAction));
  });

  if (cancelButton) {
    cancelButton.addEventListener("click", () => systemDialog.close());
  }
  if (confirmButton) {
    confirmButton.addEventListener("click", submitSystemAction);
  }

  document.addEventListener("click", (event) => {
    if (!systemPower.open || event.target.closest("[data-system-power]")) return;
    if (controlCenter) controlCenter.open = false;
  });
}


const initSystemUpdateWorkspace = () => {
  const systemUpdateOpen = document.querySelector("[data-system-update-open]");
  const systemUpdateDialog = document.querySelector("[data-system-update-dialog]");
  if (!systemUpdateOpen || !systemUpdateDialog) return;

  const csrfInput = document.querySelector("[data-system-update-csrf]");
  const closeButton = systemUpdateDialog.querySelector("[data-system-update-close]");
  const refreshButton = systemUpdateDialog.querySelector("[data-system-update-refresh]");
  const runningValue = systemUpdateDialog.querySelector("[data-system-update-running]");
  const runningImage = systemUpdateDialog.querySelector("[data-system-update-running-image]");
  const stagedValue = systemUpdateDialog.querySelector("[data-system-update-staged]");
  const stagedImage = systemUpdateDialog.querySelector("[data-system-update-staged-image]");
  const availableValue = systemUpdateDialog.querySelector("[data-system-update-available]");
  const availableImage = systemUpdateDialog.querySelector("[data-system-update-available-image]");
  const state = systemUpdateDialog.querySelector("[data-system-update-state]");
  const error = systemUpdateDialog.querySelector("[data-system-update-error]");
  const updateButton = systemUpdateDialog.querySelector("[data-system-update-button]");
  const rebootPanel = systemUpdateDialog.querySelector("[data-system-update-reboot]");
  const rebootTitle = systemUpdateDialog.querySelector("[data-system-update-reboot-title]");
  const backupToggle = systemUpdateDialog.querySelector("[data-system-update-backup]");
  const quickToggle = systemUpdateDialog.querySelector("[data-system-update-quick]");
  const warningText = systemUpdateDialog.querySelector("[data-system-update-warning]");
  const rebootPlayers = systemUpdateDialog.querySelector("[data-system-update-reboot-players]");
  const rebootProgress = systemUpdateDialog.querySelector("[data-system-update-reboot-progress]");
  const rebootCountdown = systemUpdateDialog.querySelector("[data-system-update-countdown]");
  const rebootMessage = systemUpdateDialog.querySelector("[data-system-update-reboot-message]");
  const rebootButton = systemUpdateDialog.querySelector("[data-system-update-reboot-button]");

  let updating = false;
  let checking = false;
  let latestStatus = null;
  let pendingPlayerConfirmation = false;
  let rebootWorkflowActive = false;
  let rebootPollTimer = null;
  let countdownTimer = null;
  let checkSequence = 0;
  let minecraftConfigured = dashboard?.dataset.configured === "true";

  if (dashboard) {
    new MutationObserver(() => {
      minecraftConfigured = dashboard.dataset.configured === "true";
      syncActionAvailability();
    }).observe(dashboard, { attributes: true, attributeFilter: ["data-configured"] });
  }

  const refreshMinecraftAvailability = async () => {
    if (dashboard) return;
    minecraftConfigured = false;
    syncActionAvailability();
    try {
      const response = await fetch("/api/dashboard-status", {
        method: "GET",
        credentials: "same-origin",
        headers: { Accept: "application/json" },
        cache: "no-store",
      });
      if (handleAuthResponse(response) || !response.ok) return;
      const snapshot = await response.json();
      minecraftConfigured = snapshot?.status?.minecraft?.configured === true;
      syncActionAvailability();
    } catch (_) {
      // Keep backup unavailable until Minecraft configuration can be confirmed.
    }
  };

  const deploymentVersion = (deployment, fallback) => {
    if (!deployment) return fallback;
    return deployment.version || fallback || "Deployment";
  };

  const deploymentDetail = (deployment) => {
    if (!deployment) return "";
    const parts = [];
    if (deployment.image) parts.push(deployment.image);
    if (deployment.digest) {
      const digest = String(deployment.digest);
      parts.push(digest.length > 24 ? digest.slice(0, 19) + "…" : digest);
    }
    return parts.join(" · ");
  };

  const clearRebootTimers = () => {
    if (rebootPollTimer) {
      window.clearTimeout(rebootPollTimer);
      rebootPollTimer = null;
    }
    if (countdownTimer) {
      window.clearInterval(countdownTimer);
      countdownTimer = null;
    }
  };

  const updateWarningText = () => {
    if (!warningText) return;
    warningText.textContent = quickToggle && quickToggle.checked
      ? "Player warning: 10 seconds"
      : "Player warning: 60 seconds";
  };

  const freshUpdateAvailable = () => latestStatus?.check_state === "update_available";

  const baseRebootLabel = () => freshUpdateAvailable() ? "Update & reboot" : "Reboot into update";

  const showRebootPlayers = (result) => {
    if (!rebootPlayers) return;
    const players = Array.isArray(result.players) ? result.players.filter(Boolean) : [];
    const count = Number(result.online || players.length || 0);
    const names = players.length ? " — " + players.join(", ") : "";
    rebootPlayers.textContent = count + " player" + (count === 1 ? "" : "s") + " online" + names + ". Press again to confirm.";
    rebootPlayers.hidden = false;
  };

  const hideRebootPlayers = () => {
    if (!rebootPlayers) return;
    rebootPlayers.hidden = true;
    rebootPlayers.textContent = "";
  };

  const syncActionAvailability = () => {
    const readOnly = Boolean(latestStatus?.read_only);
    const checkState = latestStatus?.check_state || "";
    const canStage = !readOnly && !checking && !updating && !rebootWorkflowActive &&
      (checkState === "update_available" || checkState === "unknown");
    if (updateButton) updateButton.disabled = !canStage;
    if (refreshButton) refreshButton.disabled = checking || updating || rebootWorkflowActive;
    if (backupToggle) {
      if (!minecraftConfigured) backupToggle.checked = false;
      backupToggle.disabled = !minecraftConfigured || checking || updating || rebootWorkflowActive;
    }
    if (quickToggle) quickToggle.disabled = checking || updating || rebootWorkflowActive;
    if (rebootButton) {
      rebootButton.disabled = checking || updating || rebootWorkflowActive ||
        (!freshUpdateAvailable() && !Boolean(latestStatus?.reboot_required));
    }
  };

  const resetPlayerConfirmation = () => {
    if (rebootWorkflowActive) return;
    pendingPlayerConfirmation = false;
    hideRebootPlayers();
    if (rebootButton) rebootButton.textContent = baseRebootLabel();
  };

  const startVisibleCountdown = (deadlineUnix) => {
    if (!rebootCountdown) return;
    if (countdownTimer) window.clearInterval(countdownTimer);
    const tick = () => {
      const remaining = Math.max(0, Number(deadlineUnix || 0) - Math.floor(Date.now() / 1000));
      rebootCountdown.textContent = String(remaining);
      if (remaining <= 0 && countdownTimer) {
        window.clearInterval(countdownTimer);
        countdownTimer = null;
      }
    };
    tick();
    countdownTimer = window.setInterval(tick, 250);
  };

  const renderRebootStatus = (result) => {
    if (!result || !result.state) return;

    if (result.confirmation_required) {
      rebootWorkflowActive = false;
      pendingPlayerConfirmation = true;
      showRebootPlayers(result);
      if (rebootProgress) rebootProgress.hidden = true;
      syncActionAvailability();
      if (rebootButton) {
        rebootButton.disabled = false;
        rebootButton.textContent = "Confirm reboot";
      }
      return;
    }

    const activeStates = new Set(["queued", "countdown", "stopping", "backup", "rebooting"]);
    rebootWorkflowActive = activeStates.has(result.state);

    if (result.state === "idle") {
      pendingPlayerConfirmation = false;
      if (rebootProgress) rebootProgress.hidden = true;
      hideRebootPlayers();
      if (rebootButton) rebootButton.textContent = baseRebootLabel();
      syncActionAvailability();
      return;
    }

    if (result.state === "backup_failed" || result.state === "failed") {
      rebootWorkflowActive = false;
      pendingPlayerConfirmation = false;
      clearRebootTimers();
      hideRebootPlayers();
      if (rebootProgress) rebootProgress.hidden = true;
      if (rebootButton) rebootButton.textContent = baseRebootLabel();
      if (error) {
        error.textContent = result.message || "Reboot was cancelled.";
        error.hidden = false;
      }
      syncActionAvailability();
      return;
    }

    if (rebootWorkflowActive) {
      pendingPlayerConfirmation = false;
      hideRebootPlayers();
      syncActionAvailability();
      if (rebootProgress) rebootProgress.hidden = false;
      if (rebootButton) rebootButton.textContent = "Rebooting…";

      if (result.state === "countdown") {
        if (rebootMessage) {
          const warning = Number(result.warning_seconds || 60);
          rebootMessage.textContent = "Player warning in progress (" + warning + " seconds).";
        }
        startVisibleCountdown(result.deadline_unix);
      } else {
        if (countdownTimer) {
          window.clearInterval(countdownTimer);
          countdownTimer = null;
        }
        if (rebootCountdown) rebootCountdown.textContent = "";
        if (rebootMessage) {
          if (result.state === "queued") rebootMessage.textContent = "Preparing reboot…";
          if (result.state === "stopping") rebootMessage.textContent = "Stopping Minecraft…";
          if (result.state === "backup") rebootMessage.textContent = "Creating Minecraft backup…";
          if (result.state === "rebooting") rebootMessage.textContent = "Rebooting JustVoxel…";
        }
      }
    }
  };

  const renderSystemUpdate = (status) => {
    latestStatus = status;
    if (runningValue) runningValue.textContent = deploymentVersion(status.running, "Current image");
    if (runningImage) runningImage.textContent = deploymentDetail(status.running);
    if (stagedValue) stagedValue.textContent = status.staged ? deploymentVersion(status.staged, "Staged image") : "None";
    if (stagedImage) stagedImage.textContent = deploymentDetail(status.staged);

    if (availableValue) {
      if (!status.checked) {
        availableValue.textContent = checking ? "Checking…" : "Not checked";
      } else if (status.check_state === "update_available") {
        availableValue.textContent = deploymentVersion(status.available, "New image available");
      } else if (status.check_state === "staged_current") {
        availableValue.textContent = "Already staged";
      } else if (status.check_state === "current") {
        availableValue.textContent = "No newer image";
      } else {
        availableValue.textContent = "Unknown";
      }
    }
    if (availableImage) {
      availableImage.textContent = status.checked && status.check_state === "update_available"
        ? deploymentDetail(status.available)
        : "";
    }

    if (state) {
      if (status.read_only) {
        state.textContent = "System updates are unavailable on this read-only deployment.";
      } else if (status.checked) {
        state.textContent = status.message || "Registry check complete.";
      } else if (status.reboot_required) {
        state.textContent = "An update is staged. Checking the registry for anything newer…";
      } else {
        state.textContent = checking ? "Checking the registry for updates…" : "Ready to check for updates.";
      }
    }

    const showReboot = Boolean(status.reboot_required) || status.check_state === "update_available";
    if (rebootPanel) rebootPanel.hidden = !showReboot;
    if (rebootTitle) rebootTitle.textContent = freshUpdateAvailable() ? "Update and reboot" : "Reboot to apply staged update";
    if (!status.reboot_required && !freshUpdateAvailable()) {
      rebootWorkflowActive = false;
      pendingPlayerConfirmation = false;
      clearRebootTimers();
      hideRebootPlayers();
      if (rebootProgress) rebootProgress.hidden = true;
    }
    if (rebootButton && !pendingPlayerConfirmation && !rebootWorkflowActive) {
      rebootButton.textContent = baseRebootLabel();
    }

    if (error) {
      error.hidden = true;
      error.textContent = "";
    }
    syncActionAvailability();
  };

  const showSystemUpdateError = (message, preserveState = false) => {
    if (error) {
      error.textContent = message || "System update information is unavailable.";
      error.hidden = false;
    }
    if (state && !preserveState) state.textContent = "System update unavailable.";
    syncActionAvailability();
  };

  const readResponse = async (response) => {
    try {
      return await response.json();
    } catch (_) {
      return {};
    }
  };

  const handleAuthResponse = (response) => {
    if (response.status === 401) {
      window.location.assign("/login");
      return true;
    }
    if (response.status === 403) {
      window.location.assign("/password");
      return true;
    }
    return false;
  };

  const pollRebootStatus = async () => {
    if (!latestStatus || !latestStatus.reboot_required || !systemUpdateDialog.open) return;
    try {
      const response = await fetch("/api/system-updates/reboot", {
        method: "GET",
        credentials: "same-origin",
        headers: { Accept: "application/json" },
        cache: "no-store",
      });
      if (handleAuthResponse(response)) return;
      const result = await readResponse(response);
      if (response.ok) {
        renderRebootStatus(result);
        if (rebootWorkflowActive) {
          rebootPollTimer = window.setTimeout(pollRebootStatus, 1000);
        }
      }
    } catch (_) {
      if (rebootWorkflowActive) {
        rebootPollTimer = window.setTimeout(pollRebootStatus, 1000);
      }
    }
  };

  const loadLocalSystemUpdateStatus = async () => {
    const response = await fetch("/api/system-updates", {
      method: "GET",
      credentials: "same-origin",
      headers: { Accept: "application/json" },
      cache: "no-store",
    });
    if (handleAuthResponse(response)) return null;
    const result = await readResponse(response);
    if (!response.ok) throw new Error(result.error || "System update status is unavailable.");
    renderSystemUpdate(result);
    if (result.reboot_required) await pollRebootStatus();
    return result;
  };

  const checkSystemUpdate = async () => {
    const sequence = ++checkSequence;
    checking = true;
    if (error) error.hidden = true;
    if (availableValue) availableValue.textContent = "Checking…";
    if (availableImage) availableImage.textContent = "";
    if (state) state.textContent = "Checking the registry for updates…";
    syncActionAvailability();

    try {
      const local = await loadLocalSystemUpdateStatus();
      if (!local || sequence !== checkSequence) return;
      if (local.read_only) {
        if (availableValue) availableValue.textContent = "Unavailable";
        return;
      }
      if (!csrfInput) throw new Error("Update check is unavailable.");

      if (state) {
        state.textContent = local.reboot_required
          ? "An update is staged. Checking the registry for anything newer…"
          : "Checking the registry for updates…";
      }
      if (availableValue) availableValue.textContent = "Checking…";

      const body = new URLSearchParams({ csrf: csrfInput.value });
      const response = await fetch("/api/system-updates/check", {
        method: "POST",
        credentials: "same-origin",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/x-www-form-urlencoded",
        },
        body: body.toString(),
        cache: "no-store",
      });
      if (handleAuthResponse(response)) return;
      const result = await readResponse(response);
      if (!response.ok) throw new Error(result.error || "Registry update check failed.");
      if (sequence !== checkSequence) return;
      renderSystemUpdate(result);
      if (result.reboot_required) await pollRebootStatus();
    } catch (caught) {
      if (sequence !== checkSequence) return;
      if (availableValue) availableValue.textContent = "Check failed";
      if (availableImage) availableImage.textContent = "";
      if (state) {
        state.textContent = latestStatus?.reboot_required
          ? "Staged update shown. Registry freshness could not be verified."
          : "Running deployment shown. Registry freshness could not be verified.";
      }
      showSystemUpdateError(caught?.message || "Could not check the registry for updates.", true);
    } finally {
      if (sequence === checkSequence) {
        checking = false;
        syncActionAvailability();
      }
    }
  };

  const requestUpdateReboot = async () => {
    if (freshUpdateAvailable() && !updating) {
      await applySystemUpdate(true);
      return;
    }
    if (rebootWorkflowActive || updating || checking || !csrfInput || !rebootButton) return;

    rebootButton.disabled = true;
    rebootButton.textContent = "Checking…";
    if (error) error.hidden = true;

    const body = new URLSearchParams();
    body.set("csrf", csrfInput.value);
    if (backupToggle && backupToggle.checked) body.set("backup_minecraft", "yes");
    if (quickToggle && quickToggle.checked) body.set("quick_reboot", "yes");
    if (pendingPlayerConfirmation) body.set("confirm_players", "yes");

    try {
      const response = await fetch("/api/system-updates/reboot", {
        method: "POST",
        credentials: "same-origin",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/x-www-form-urlencoded",
        },
        body: body.toString(),
      });
      if (handleAuthResponse(response)) return;
      const result = await readResponse(response);
      if (result.confirmation_required) {
        renderRebootStatus(result);
        return;
      }
      if (!response.ok) {
        renderRebootStatus(result);
        if (error && error.hidden) {
          error.textContent = result.message || result.error || "Reboot workflow could not be started.";
          error.hidden = false;
        }
        return;
      }
      renderRebootStatus(result);
      if (result.accepted) rebootPollTimer = window.setTimeout(pollRebootStatus, 250);
    } catch (_) {
      rebootWorkflowActive = false;
      if (rebootButton) rebootButton.textContent = baseRebootLabel();
      if (error) {
        error.textContent = "Reboot workflow could not be started.";
        error.hidden = false;
      }
      syncActionAvailability();
    }
  };

  const applySystemUpdate = async (rebootAfter = false) => {
    if (updating || checking || rebootWorkflowActive || !csrfInput || !updateButton) return;
    updating = true;
    updateButton.disabled = true;
    updateButton.classList.add("is-busy");
    updateButton.setAttribute("aria-busy", "true");
    updateButton.textContent = rebootAfter ? "Downloading…" : "Downloading…";
    if (rebootButton) rebootButton.disabled = true;
    if (state) state.textContent = "Downloading and staging the latest JustVoxel image… Minecraft keeps running.";
    if (error) error.hidden = true;
    syncActionAvailability();

    const body = new URLSearchParams({ csrf: csrfInput.value });
    try {
      const response = await fetch("/api/system-updates", {
        method: "POST",
        credentials: "same-origin",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/x-www-form-urlencoded",
        },
        body: body.toString(),
      });
      if (handleAuthResponse(response)) return;
      const result = await readResponse(response);
      if (!response.ok) {
        showSystemUpdateError(result.error || "System update failed.");
        return;
      }
      renderSystemUpdate(result);
      if (result.reboot_required) {
        await pollRebootStatus();
        if (rebootAfter) {
          updating = false;
          syncActionAvailability();
          await requestUpdateReboot();
        }
      }
    } catch (_) {
      showSystemUpdateError("System update service is unavailable. Nothing was submitted again automatically.");
    } finally {
      updating = false;
      updateButton.classList.remove("is-busy");
      updateButton.removeAttribute("aria-busy");
      updateButton.textContent = "Download & stage";
      syncActionAvailability();
    }
  };

  const workspaceWindow = setupWorkspaceWindow(systemUpdateDialog, {
    onOpen: () => {
      void refreshMinecraftAvailability();
      updateWarningText();
      checkSystemUpdate();
    },
    onClose: () => {
      checkSequence += 1;
      checking = false;
      clearRebootTimers();
    },
  });

  systemUpdateOpen.addEventListener("click", () => {
    if (controlCenter) controlCenter.open = false;
    workspaceWindow?.open();
  });

  closeButton?.addEventListener("click", () => workspaceWindow?.close());
  refreshButton?.addEventListener("click", checkSystemUpdate);
  updateButton?.addEventListener("click", () => applySystemUpdate(false));
  rebootButton?.addEventListener("click", requestUpdateReboot);
  quickToggle?.addEventListener("change", () => {
    updateWarningText();
    resetPlayerConfirmation();
  });
  backupToggle?.addEventListener("change", resetPlayerConfirmation);

  const restoreSystemUpdateWhenRoleKnown = () => {
    if (!readWorkspaceWindowState("system-update").open) return;
    if (document.body.classList.contains("role-administrator")) {
      workspaceWindow?.open();
      return;
    }
    if (!document.body.classList.contains("role-pending")) return;
    const observer = new MutationObserver(() => {
      if (document.body.classList.contains("role-pending")) return;
      observer.disconnect();
      if (document.body.classList.contains("role-administrator")) workspaceWindow?.open();
    });
    observer.observe(document.body, { attributes: true, attributeFilter: ["class"] });
  };
  restoreSystemUpdateWhenRoleKnown();
};

const workspaceWindows = new Set();
let workspaceWindowZ = 120;
// Keep workspace windows below the Control Center's reserved topbar layer (1000).
const workspaceWindowZMax = 900;
const workspaceCompactQuery = window.matchMedia("(max-width: 700px)");

const workspaceUsername = dashboard?.dataset.workspaceUsername || "";
const workspaceWindowStateKey = (id) => workspaceUsername
  ? `justvoxel-workspace-window-${id === "system-monitor" ? "v2" : "v1"}:${encodeURIComponent(workspaceUsername)}:${id}`
  : null;

const workspaceRestoreAllowed = () => !["/login", "/password"].includes(window.location.pathname) && !document.querySelector("[data-credential-boundary]");

const readWorkspaceWindowState = (id) => {
  const key = workspaceWindowStateKey(id);
  if (!key) return {};
  try {
    const saved = JSON.parse(window.localStorage.getItem(key) || "{}");
    return workspaceRestoreAllowed() ? saved : { ...saved, open: false };
  } catch (_) {
    return {};
  }
};

const writeWorkspaceWindowState = (id, patch) => {
  const key = workspaceWindowStateKey(id);
  if (!key) return;
  try {
    const current = readWorkspaceWindowState(id);
    window.localStorage.setItem(key, JSON.stringify({ ...current, ...patch }));
  } catch (_) {
    // Layout persistence is optional. The window still works without browser storage.
  }
};

const workspaceTopInset = () => {
  const topbar = document.querySelector(".topbar");
  return Math.max(8, Math.round((topbar?.getBoundingClientRect().bottom || 0) + 8));
};

const clampWorkspaceWindow = (element) => {
  if (!element.open || workspaceCompactQuery.matches) return;
  const minTop = workspaceTopInset();
  const width = Math.max(1, document.documentElement.clientWidth - 16);
  const height = Math.max(1, window.innerHeight - minTop - 8);
  // First fit the whole window, including saved dimensions from a larger screen.
  element.style.setProperty("--workspace-available-width", width + "px");
  element.style.setProperty("--workspace-available-height", height + "px");
  const rect = element.getBoundingClientRect();
  const left = Math.min(Math.max(rect.left, 8), Math.max(8, document.documentElement.clientWidth - rect.width - 8));
  const top = Math.min(Math.max(rect.top, minTop), Math.max(minTop, window.innerHeight - rect.height - 8));
  element.style.left = Math.round(left) + "px";
  element.style.top = Math.round(top) + "px";
  // Native resizing must stop at the remaining space, before it can overflow.
  element.style.setProperty("--workspace-available-width", Math.max(1, document.documentElement.clientWidth - Math.round(left) - 8) + "px");
  element.style.setProperty("--workspace-available-height", Math.max(1, window.innerHeight - Math.round(top) - 8) + "px");
};

const setupWorkspaceWindow = (element, options = {}) => {
  const id = element.dataset.workspaceWindow;
  if (!id) return null;

  workspaceWindows.add(element);
  const dragHandle = element.querySelector("[data-workspace-drag-handle]");
  let resizeSaveTimer = null;

  const bringToFront = () => {
    if (workspaceWindowZ >= workspaceWindowZMax) {
      const ordered = Array.from(workspaceWindows).filter((window) => window.open)
        .sort((a, b) => Number(a.style.zIndex || 0) - Number(b.style.zIndex || 0));
      workspaceWindowZ = 120;
      ordered.forEach((window) => { window.style.zIndex = String(++workspaceWindowZ); });
    }
    workspaceWindowZ += 1;
    element.style.zIndex = String(workspaceWindowZ);
  };

  const persistGeometry = () => {
    if (!element.open || workspaceCompactQuery.matches) return;
    const rect = element.getBoundingClientRect();
    writeWorkspaceWindowState(id, {
      left: Math.round(rect.left),
      top: Math.round(rect.top),
      width: Math.round(rect.width),
      height: Math.round(rect.height),
    });
  };

  const applySavedGeometry = () => {
    if (workspaceCompactQuery.matches) return;
    const saved = readWorkspaceWindowState(id);
    if (Number.isFinite(saved.width) && saved.width > 0) element.style.width = saved.width + "px";
    if (Number.isFinite(saved.height) && saved.height > 0) element.style.height = saved.height + "px";
    if (Number.isFinite(saved.left)) element.style.left = saved.left + "px";
    if (Number.isFinite(saved.top)) element.style.top = saved.top + "px";

    // Bound restored geometry synchronously; an off-screen saved window must
    // never contribute document overflow for an animation frame.
    clampWorkspaceWindow(element);
    if (!Number.isFinite(saved.left) || !Number.isFinite(saved.top)) {
      const rect = element.getBoundingClientRect();
      element.style.left = Math.max(8, Math.round((document.documentElement.clientWidth - rect.width) / 2)) + "px";
      element.style.top = Math.max(workspaceTopInset(), Math.round((window.innerHeight - rect.height) / 2)) + "px";
    }
    clampWorkspaceWindow(element);
    persistGeometry();
  };

  const open = () => {
    if (!element.open) element.show();
    bringToFront();
    applySavedGeometry();
    writeWorkspaceWindowState(id, { open: true });
    options.onOpen?.();
  };

  const close = () => {
    if (element.open) element.close();
  };

  element.addEventListener("pointerdown", bringToFront);

  if (dragHandle) {
    dragHandle.addEventListener("pointerdown", (event) => {
      if (workspaceCompactQuery.matches) return;
      if (event.button !== 0) return;
      if (event.target.closest("button,a,input,select,label")) return;

      bringToFront();
      const rect = element.getBoundingClientRect();
      const startX = event.clientX;
      const startY = event.clientY;
      const startLeft = rect.left;
      const startTop = rect.top;
      const minTop = workspaceTopInset();

      event.preventDefault();
      dragHandle.setPointerCapture(event.pointerId);

      const move = (moveEvent) => {
        const left = Math.min(
          Math.max(startLeft + moveEvent.clientX - startX, 8),
          Math.max(8, document.documentElement.clientWidth - rect.width - 8),
        );
        const top = Math.min(
          Math.max(startTop + moveEvent.clientY - startY, minTop),
          Math.max(minTop, window.innerHeight - rect.height - 8),
        );
        element.style.left = Math.round(left) + "px";
        element.style.top = Math.round(top) + "px";
        clampWorkspaceWindow(element);
      };

      const finish = () => {
        dragHandle.removeEventListener("pointermove", move);
        dragHandle.removeEventListener("pointerup", finish);
        dragHandle.removeEventListener("pointercancel", finish);
        persistGeometry();
      };

      dragHandle.addEventListener("pointermove", move);
      dragHandle.addEventListener("pointerup", finish);
      dragHandle.addEventListener("pointercancel", finish);
    });
  }

  const resizeObserver = new ResizeObserver(() => {
    if (!element.open || workspaceCompactQuery.matches) return;
    clampWorkspaceWindow(element);
    window.clearTimeout(resizeSaveTimer);
    resizeSaveTimer = window.setTimeout(() => {
      persistGeometry();
    }, 120);
  });
  resizeObserver.observe(element);

  element.addEventListener("close", () => {
    writeWorkspaceWindowState(id, { open: false });
    options.onClose?.();
  });

  return { open, close, bringToFront, id };
};

// One explicit-open diagnostic viewer shared by setup and persistent resets.
window.JustVoxelLiveConsole = (() => {
  let dialog, viewer, label, indicator, workspace;
  let selected = null;
  let generation = 0;
  let timer = null;
  let controller = null;
  let reconnecting = false;
  const labels = { setup: "First-run Setup", minecraft_reset: "Reset Minecraft", factory_reset: "Full Factory Reset" };
  const stageLabels = {
    storage_preflight: "Storage", storage_snapshot: "Storage", storage_verified: "Storage",
    runtime_preflight: "Runtime", runtime_config: "Runtime", minecraft_verify: "Minecraft",
    final_validation: "Verification", verifying: "Verification", validating: "Validation",
    runtime_rollback: "Recovery", storage_rollback: "Recovery", runtime_recovery: "Recovery", storage_recovery: "Recovery",
    resetting: "Removing runtime", resetting_runtime: "Cleaning appliance state", resetting_identity: "Resetting users",
    safety_backup: "Safety backup",
  };
  const stateLabel = (state) => ({
    succeeded: "Complete", resolved: "Complete", rolled_back: "Rolled back",
    needs_attention: "Needs attention", running: "Running", verifying: "Verifying",
    queued: "Queued", validating: "Validating", rolling_back: "Rolling back",
  }[state] || "Live");
  const showState = () => {
    const stage = Object.prototype.hasOwnProperty.call(stageLabels, selected.stage) ? stageLabels[selected.stage] : "";
    label.textContent = labels[selected.kind] + (stage ? ` · ${stage}` : "");
    indicator.textContent = reconnecting ? "Reconnecting…" : stateLabel(selected.state);
  };
  const stop = () => {
    generation += 1;
    window.clearTimeout(timer);
    timer = null;
    controller?.abort();
    controller = null;
  };
  const close = () => {
    stop();
    workspace?.close();
  };
  const ensureWindow = () => {
    if (dialog) return;
    const make = (tag, className, text) => {
      const node = document.createElement(tag);
      node.className = className;
      if (text !== undefined) node.textContent = text;
      return node;
    };
    dialog = make("dialog", "workspace-window live-console-dialog");
    dialog.dataset.workspaceWindow = "live-console";
    dialog.setAttribute("aria-labelledby", "live-console-title");
    const shell = make("div", "live-console-shell");
    const header = make("header", "system-workspace-header live-console-header");
    header.setAttribute("data-workspace-drag-handle", "");
    const heading = make("div", "");
    const title = make("h2", "", "Live Console");
    title.id = "live-console-title";
    heading.append(make("p", "eyebrow", "JustVoxel"), title);
    const dismiss = make("button", "secondary system-monitor-header-button workspace-header-close");
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("viewBox", "0 0 24 24");
    svg.setAttribute("aria-hidden", "true");
    svg.setAttribute("fill", "none");
    svg.setAttribute("stroke", "currentColor");
    svg.setAttribute("stroke-width", "2");
    svg.setAttribute("stroke-linecap", "round");
    const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
    path.setAttribute("d", "M5 5l14 14M19 5L5 19");
    svg.append(path);
    dismiss.append(svg);
    dismiss.type = "button";
    dismiss.setAttribute("aria-label", "Close Live Console");
    dismiss.addEventListener("click", close);
    header.append(heading, dismiss);
    const context = make("div", "live-console-context");
    label = make("span", "");
    indicator = make("span", "badge");
    indicator.setAttribute("role", "status");
    context.append(label, indicator);
    viewer = make("pre", "live-console-transcript", "");
    viewer.tabIndex = 0;
    viewer.setAttribute("aria-label", "Operation execution trace");
    shell.append(header, context, viewer);
    dialog.append(shell);
    document.body.append(dialog);
    workspace = setupWorkspaceWindow(dialog, { onClose: () => { if (!dialog.open) stop(); } });
    // Never restore open state: each page begins with the console closed.
  };
  const read = async (token) => {
    const current = () => token === generation && dialog.open;
    if (!current()) return;
    const source = selected;
    controller = new AbortController();
    try {
      const response = await fetch(source.url, {
        credentials: "same-origin", cache: "no-store",
        headers: { Accept: "text/plain" }, signal: controller.signal,
      });
      if (!current()) return;
      if (response.status === 401) {
        close();
        if (source.onUnauthorized) source.onUnauthorized();
        else window.location.assign("/login");
        return;
      }
      if (response.redirected) {
        const target = new URL(response.url, window.location.href);
        if (target.origin === window.location.origin && ["/login", "/password"].includes(target.pathname)) {
          close();
          window.location.assign(target.pathname + target.search);
          return;
        }
      }
      if (response.status === 403) {
        if (source.kind === "setup") {
          close();
          window.location.assign("/password");
          return;
        }
        const payload = await response.json().catch(() => ({}));
        if (!current()) return;
        if (String(payload.error || "").toLowerCase().includes("password change")) {
          close();
          window.location.assign("/password");
          return;
        }
      }
      if (!response.ok || !/^text\/plain(?:;|$)/i.test(response.headers.get("Content-Type") || "")) {
        throw new Error("Transcript unavailable");
      }
      const transcript = await response.text();
      if (!current()) return;
      const follow = viewer.scrollHeight - viewer.clientHeight - viewer.scrollTop <= 40;
      viewer.textContent = transcript;
      if (follow) viewer.scrollTop = viewer.scrollHeight;
      reconnecting = false;
      showState();
    } catch (_) {
      if (!current()) return;
      reconnecting = true;
      showState();
    }
    if (current()) timer = window.setTimeout(() => read(token), 2500);
  };
  const open = (config) => {
    if (!["setup", "minecraft_reset", "factory_reset"].includes(config.kind) || !/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(config.operationID)) return;
    ensureWindow();
    if (dialog.open && selected?.operationID === config.operationID && selected.kind === config.kind) {
      selected.state = config.state;
      selected.stage = config.stage;
      showState();
      workspace.bringToFront();
      return;
    }
    stop();
    const id = encodeURIComponent(config.operationID);
    selected = {
      kind: config.kind, operationID: config.operationID, state: config.state, stage: config.stage,
      onUnauthorized: config.onUnauthorized,
      url: `/api/operations/${id}/trace`,
    };
    reconnecting = false;
    label.textContent = labels[config.kind];
    viewer.textContent = "";
    viewer.scrollTop = 0;
    showState();
    workspace.open();
    void read(generation);
  };
  const updateState = (operationID, state, stage) => {
    if (selected?.operationID !== operationID || !dialog?.open) return;
    selected.state = state;
    selected.stage = stage;
    showState();
  };
  window.addEventListener("pagehide", close);
  window.addEventListener("beforeunload", close);
  return { open, close, updateState };
})();

document.addEventListener("keydown", (event) => {
  if (event.key !== "Escape") return;
  if (document.querySelector("dialog:modal")) return;
  const openWindows = Array.from(workspaceWindows).filter((element) => element.open);
  if (openWindows.length === 0) return;
  openWindows.sort((a, b) => Number(b.style.zIndex || 0) - Number(a.style.zIndex || 0));
  if (openWindows[0] === systemWorkspaceDialog && window.JustVoxelWallpaper?.canLeave() === false) return;
  openWindows[0].close();
});

window.addEventListener("resize", () => {
  workspaceWindows.forEach((element) => clampWorkspaceWindow(element));
});

initSystemUpdateWorkspace();

const storageOpen = document.querySelector("[data-storage-open]");
const storageDialog = document.querySelector("[data-storage-workspace-dialog]");
if (storageOpen && storageDialog) {
  const closeButton = storageDialog.querySelector("[data-storage-close]");
  const refreshButton = storageDialog.querySelector("[data-storage-refresh]");
  const state = storageDialog.querySelector("[data-storage-state]");
  const content = storageDialog.querySelector("[data-storage-workspace-content]");
  let loadSequence = 0;

  const ensureStorageBrowserAssets = async () => {
    let stylesheet = document.querySelector('link[href="/static/storage-browser.css"]');
    if (!stylesheet) {
      stylesheet = document.createElement("link");
      stylesheet.rel = "stylesheet";
      stylesheet.href = "/static/storage-browser.css";
      document.head.appendChild(stylesheet);
    }

    if (!document.querySelector('link[href="/static/new-backups.css"]')) {
      const css = document.createElement("link"); css.rel = "stylesheet"; css.href = "/static/new-backups.css"; document.head.appendChild(css);
    }
    if (!window.JustVoxelNewBackups?.init) {
      await new Promise((resolve, reject) => {
        let script = document.querySelector('script[src="/static/new-backups.js"]');
        if (!script) { script = document.createElement("script"); script.src = "/static/new-backups.js"; document.head.appendChild(script); }
        script.addEventListener("load", resolve, { once: true });
        script.addEventListener("error", reject, { once: true });
      });
    }
    if (window.JustVoxelStorageBrowser?.init) return;
    let script = document.querySelector('script[src="/static/storage-browser.js"]');
    await new Promise((resolve, reject) => {
      if (window.JustVoxelStorageBrowser?.init) {
        resolve();
        return;
      }
      if (!script) {
        script = document.createElement("script");
        script.src = "/static/storage-browser.js";
        script.defer = true;
        document.head.appendChild(script);
      }
      script.addEventListener("load", resolve, { once: true });
      script.addEventListener("error", reject, { once: true });
    });
  };

  const loadStorage = async (url = "/workspace/storage", request = {}) => {
    const sequence = ++loadSequence;
    if (state) state.textContent = "Loading storage…";
    if (refreshButton) refreshButton.disabled = true;
    try {
      await ensureStorageBrowserAssets();
      const response = await fetch(url, {
        ...request,
        method: request.method || "GET",
        credentials: "same-origin",
        headers: { Accept: "text/html" },
        cache: "no-store",
      });
      if (response.redirected && new URL(response.url).pathname === "/login") {
        window.location.assign("/login");
        return;
      }
      if (response.status === 401) {
        window.location.assign("/login");
        return;
      }
      if (response.status === 403) throw new Error("Administrator access required.");
      if (!response.ok) throw new Error("Storage discovery is unavailable.");
      const markup = await response.text();
      if (sequence !== loadSequence || !content) return;
      content.innerHTML = markup;
      const root = content.querySelector("[data-storage-browser-root]");
      if (!root || !window.JustVoxelStorageBrowser?.init) throw new Error("Storage browser could not be initialized.");
      window.JustVoxelStorageBrowser.init(root);
      window.JustVoxelNewBackups?.init(root);
      root.querySelectorAll('form[action^="/workspace/storage/destination/"]').forEach(form => {
        form.addEventListener("submit", async event => {
          event.preventDefault();
          const submitter = event.submitter;
          if (submitter) submitter.disabled = true;
          await loadStorage(form.action, { method: "POST", body: new URLSearchParams(new FormData(form)) });
        });
      });
      root.querySelectorAll('a[href="/workspace/storage"]').forEach(link => link.addEventListener("click", event => { event.preventDefault(); loadStorage(); }));
      if (state) state.textContent = "";
    } catch (error) {
      if (sequence !== loadSequence) return;
      if (content) content.replaceChildren();
      if (state) state.textContent = error?.message || "Storage discovery is unavailable.";
    } finally {
      if (sequence === loadSequence && refreshButton) refreshButton.disabled = false;
    }
  };

  const workspaceWindow = setupWorkspaceWindow(storageDialog, { onOpen: () => loadStorage() });

  storageOpen.addEventListener("click", () => {
    if (controlCenter) controlCenter.open = false;
    workspaceWindow?.open();
  });
  closeButton?.addEventListener("click", () => workspaceWindow?.close());
  refreshButton?.addEventListener("click", () => loadStorage());

  const restoreStorageWhenRoleKnown = () => {
    if (!readWorkspaceWindowState("storage").open) return;
    if (document.body.classList.contains("role-administrator")) {
      workspaceWindow?.open();
      return;
    }
    if (!document.body.classList.contains("role-pending")) return;
    const observer = new MutationObserver(() => {
      if (document.body.classList.contains("role-pending")) return;
      observer.disconnect();
      if (document.body.classList.contains("role-administrator")) workspaceWindow?.open();
    });
    observer.observe(document.body, { attributes: true, attributeFilter: ["class"] });
  };
  restoreStorageWhenRoleKnown();
}

const backupsOpen = document.querySelector("[data-backups-open]");
const backupsDialog = document.querySelector("[data-backups-workspace-dialog]");
if (backupsOpen && backupsDialog) {
  const closeButton = backupsDialog.querySelector("[data-backups-close]");
  const refreshButton = backupsDialog.querySelector("[data-backups-refresh]");
  const state = backupsDialog.querySelector("[data-backups-state]");
  const content = backupsDialog.querySelector("[data-backups-workspace-content]");
  const backupsDestination = new URLSearchParams(window.location.search);
  const backupParams = new URLSearchParams();
  if (backupsDestination.get("workspace") === "backups") {
    for (const key of ["result", "count", "restore_operation"]) {
      if (backupsDestination.has(key)) backupParams.set(key, backupsDestination.get(key));
    }
  }
  let currentURL = "/workspace/backups" + (backupParams.size ? "?" + backupParams : "");
  let loadSequence = 0;

  const loadBackupsScript = async (src, ready) => {
    if (ready()) return;
    let script = document.querySelector(`script[src="${src}"]`);
    await new Promise((resolve, reject) => {
      if (ready()) {
        resolve();
        return;
      }
      if (!script) {
        script = document.createElement("script");
        script.src = src;
        script.defer = true;
        document.head.appendChild(script);
      }
      script.addEventListener("load", resolve, { once: true });
      script.addEventListener("error", reject, { once: true });
    });
  };

  const ensureBackupsAssets = async () => {
    if (!document.querySelector('link[href="/static/new-backups.css"]')) {
      const stylesheet = document.createElement("link");
      stylesheet.rel = "stylesheet";
      stylesheet.href = "/static/new-backups.css";
      document.head.appendChild(stylesheet);
    }
    await loadBackupsScript("/static/new-backups.js", () => Boolean(window.JustVoxelNewBackups?.init));
    await loadBackupsScript("/static/restore-operation.js", () => Boolean(window.JustVoxelRestoreOperation?.init));
  };

  const renderBackupsMarkup = (markup) => {
    if (!content) throw new Error("Backups workspace is unavailable.");
    content.innerHTML = markup;
    const root = content.querySelector("[data-backups-workspace-root]");
    if (!root) throw new Error("Backups response could not be rendered.");

    const handleSubmit = async (event) => {
      const form = event.target.closest("form");
      if (!form || !root.contains(form)) return;
      const action = new URL(form.getAttribute("action") || currentURL, window.location.href);
      if (action.origin !== window.location.origin || !action.pathname.startsWith("/workspace/backups")) return;

      event.preventDefault();
      const reviewDialog = form.closest("[data-backup-review-dialog]");
      const reviewClose = reviewDialog?.querySelector("[data-backup-workflow-review-close]");
      const submitter = event.submitter;
      if (reviewDialog) reviewDialog.dataset.busy = "true";
      if (reviewClose) reviewClose.disabled = true;
      if (submitter) submitter.disabled = true;
      if (state) state.textContent = "Working…";

      try {
        const body = new URLSearchParams();
        new FormData(form).forEach((value, key) => body.append(key, String(value)));
        const response = await fetch(action.pathname + action.search, {
          method: (form.method || "POST").toUpperCase(),
          credentials: "same-origin",
          headers: {
            Accept: "text/html",
            "Content-Type": "application/x-www-form-urlencoded",
          },
          body: body.toString(),
          cache: "no-store",
          redirect: "follow",
        });

        if (response.redirected) {
          const target = new URL(response.url);
          if (target.pathname === "/login" || target.pathname === "/password") {
            window.location.assign(target.pathname + target.search);
            return;
          }
          if (target.origin === window.location.origin && target.pathname === "/workspace/backups") {
            currentURL = target.pathname + target.search;
          }
        }

        if (response.status === 401) {
          window.location.assign("/login");
          return;
        }
        if (response.status === 403) {
          const message = await response.text();
          if (message.toLowerCase().includes("password change")) {
            window.location.assign("/password");
            return;
          }
          throw new Error("Administrator access required.");
        }
        if (!response.ok) {
          const message = await response.text();
          throw new Error(message.trim() || "Backup operation could not be completed.");
        }

        const responseMarkup = await response.text();
        renderBackupsMarkup(responseMarkup);
        if (state) state.textContent = "";
      } catch (error) {
        if (reviewDialog) reviewDialog.dataset.busy = "false";
        if (reviewClose) reviewClose.disabled = false;
        if (submitter) submitter.disabled = false;
        if (state) state.textContent = error?.message || "Backup operation could not be completed.";
      }
    };

    const handleClick = async (event) => {
      const link = event.target.closest("a");
      if (!link || !root.contains(link)) return;
      const href = link.getAttribute("href") || "";

      if (href === "#backup-library") {
        event.preventDefault();
        root.querySelector("#backup-library")?.scrollIntoView({ behavior: "smooth", block: "start" });
        return;
      }
      if (href === "/" && link.id === "restore-dashboard-link") {
        event.preventDefault();
        workspaceWindow?.close();
        return;
      }
      if (href.startsWith("/workspace/backups")) {
        event.preventDefault();
        await loadBackups(href);
      }
    };

    root.addEventListener("submit", handleSubmit);
    root.addEventListener("click", handleClick);
    window.JustVoxelNewBackups?.init(root);
    window.JustVoxelRestoreOperation?.init(root);
  };

  const loadBackups = async (url = currentURL) => {
    const sequence = ++loadSequence;
    if (state) state.textContent = "Loading backups…";
    if (refreshButton) refreshButton.disabled = true;
    try {
      await ensureBackupsAssets();
      const response = await fetch(url, {
        method: "GET",
        credentials: "same-origin",
        headers: { Accept: "text/html" },
        cache: "no-store",
        redirect: "follow",
      });

      if (response.redirected) {
        const target = new URL(response.url);
        if (target.pathname === "/login" || target.pathname === "/password") {
          window.location.assign(target.pathname + target.search);
          return;
        }
      }
      if (response.status === 401) {
        window.location.assign("/login");
        return;
      }
      if (response.status === 403) {
        const message = await response.text();
        if (message.toLowerCase().includes("password change")) {
          window.location.assign("/password");
          return;
        }
        throw new Error("Administrator access required.");
      }
      if (!response.ok) throw new Error("Backups are unavailable.");

      const markup = await response.text();
      if (sequence !== loadSequence) return;
      const responseURL = new URL(response.url);
      if (responseURL.origin === window.location.origin && responseURL.pathname === "/workspace/backups") {
        currentURL = responseURL.pathname + responseURL.search;
      } else {
        currentURL = url;
      }
      renderBackupsMarkup(markup);
      if (state) state.textContent = "";
    } catch (error) {
      if (sequence !== loadSequence) return;
      if (content) content.replaceChildren();
      if (state) state.textContent = error?.message || "Backups are unavailable.";
    } finally {
      if (sequence === loadSequence && refreshButton) refreshButton.disabled = false;
    }
  };

  const workspaceWindow = setupWorkspaceWindow(backupsDialog, { onOpen: () => loadBackups(currentURL) });

  window.JustVoxelBackupsWorkspace = {
    reload: async (url = "/workspace/backups") => {
      currentURL = url;
      await loadBackups(url);
    },
  };

  backupsOpen.addEventListener("click", () => {
    if (controlCenter) controlCenter.open = false;
    workspaceWindow?.open();
  });
  closeButton?.addEventListener("click", () => workspaceWindow?.close());
  refreshButton?.addEventListener("click", () => loadBackups(currentURL));

  const restoreBackupsWhenRoleKnown = () => {
    if (!readWorkspaceWindowState("backups").open) return;
    if (document.body.classList.contains("role-administrator")) {
      workspaceWindow?.open();
      return;
    }
    if (!document.body.classList.contains("role-pending")) return;
    const observer = new MutationObserver(() => {
      if (document.body.classList.contains("role-pending")) return;
      observer.disconnect();
      if (document.body.classList.contains("role-administrator")) workspaceWindow?.open();
    });
    observer.observe(document.body, { attributes: true, attributeFilter: ["class"] });
  };
  restoreBackupsWhenRoleKnown();
}


const migrationOpen = document.querySelector("[data-migration-open]");
document.addEventListener("click", (event) => {
  const open = event.target.closest("[data-recovery-open-confirmation]");
  if (open) {
    open.closest("[data-recovery-cleanup-form]")?.querySelector("[data-recovery-confirmation-dialog]")?.showModal();
    return;
  }
  const cancel = event.target.closest("[data-recovery-cancel]");
  if (cancel) { cancel.closest("dialog")?.close(); return; }
  const confirm = event.target.closest("[data-recovery-confirm]");
  if (confirm) {
    const form = confirm.closest("[data-recovery-cleanup-form]");
    if (!form) return;
    form.querySelector("[data-recovery-confirmed]").value = "yes";
    confirm.closest("dialog")?.close();
    form.requestSubmit();
  }
});

const migrationDialog = document.querySelector("[data-migration-workspace-dialog]");
if (migrationOpen && migrationDialog) {
  const closeButton = migrationDialog.querySelector("[data-migration-close]");
  const refreshButton = migrationDialog.querySelector("[data-migration-refresh]");
  const state = migrationDialog.querySelector("[data-migration-state]");
  const content = migrationDialog.querySelector("[data-migration-workspace-content]");
  const tabs = Array.from(migrationDialog.querySelectorAll("[data-migration-tab]"));
  const recoveryTab = tabs.find((button) => button.dataset.migrationTab === "recovery");
  const tabURLs = {
    export: "/workspace/migration/export",
    import: "/workspace/migration/import",
    recovery: "/workspace/migration/recovery",
  };
  const migrationDestination = new URLSearchParams(window.location.search);
  const migrationRequested = migrationDestination.get("workspace") === "migration";
  const initialMigrationTab = migrationRequested ? migrationDestination.get("tab") : "";
  let currentTab = Object.hasOwn(tabURLs, initialMigrationTab) ? initialMigrationTab : "export";
  let currentURL = tabURLs[currentTab];
  let loadSequence = 0;
  let migrationSourceSMBPassword = "";
  let migrationExportSMBPassword = "";
  let migrationLoadController = null;
  let migrationSubmitController = null;

  const updateMigrationLocation = (tab = "", operation = "") => {
    const destination = new URL("/?workspace=migration", window.location.href);
    if (operation) destination.searchParams.set("operation", operation);
    else if (Object.hasOwn(tabURLs, tab)) destination.searchParams.set("tab", tab);
    const nextURL = destination.pathname + destination.search;
    if (window.location.pathname + window.location.search !== nextURL) {
      window.history.replaceState(window.history.state, "", nextURL);
    }
  };

  const setMigrationBusy = (message = "", kind = "busy") => {
    if (state) {
      state.textContent = message;
      state.classList.remove("is-busy", "is-error", "is-info");
      if (message) state.classList.add(kind === "error" ? "is-error" : kind === "info" ? "is-info" : "is-busy");
    }
    migrationDialog.classList.toggle("is-busy", Boolean(message) && kind === "busy");
  };

  const abortMigrationRequests = () => {
    migrationLoadController?.abort();
    migrationSubmitController?.abort();
    migrationLoadController = null;
    migrationSubmitController = null;
    setMigrationBusy("");
  };

  const requestMigrationSMBPassword = (purpose) => new Promise((resolve) => {
    const dialog = document.createElement("dialog");
    dialog.className = "system-action-dialog";
    dialog.setAttribute("aria-label", "SMB credentials");
    const shell = document.createElement("div");
    shell.className = "system-action-dialog-content";
    const title = document.createElement("h2");
    title.textContent = "SMB password";
    const copy = document.createElement("p");
    copy.textContent = purpose === "import"
      ? "Enter the SMB password so JustVoxel can inspect and reopen this Import source."
      : "Enter the SMB password so JustVoxel can write the reviewed Export.";
    const label = document.createElement("label");
    label.textContent = "SMB password";
    const input = document.createElement("input");
    input.type = "password";
    input.autocomplete = "off";
    input.required = true;
    label.appendChild(input);
    const actions = document.createElement("div");
    actions.className = "action-row";
    const cancel = document.createElement("button");
    cancel.type = "button";
    cancel.className = "secondary";
    cancel.textContent = "Cancel";
    const use = document.createElement("button");
    use.type = "button";
    use.textContent = "Continue";
    actions.append(cancel, use);
    shell.append(title, copy, label, actions);
    dialog.appendChild(shell);
    document.body.appendChild(dialog);
    initializeSharedWebUIControls(dialog);

    const finish = (value) => {
      input.value = "";
      if (dialog.open) dialog.close();
      dialog.remove();
      resolve(value);
    };
    cancel.addEventListener("click", () => finish(null), { once: true });
    dialog.addEventListener("cancel", (event) => {
      event.preventDefault();
      finish(null);
    }, { once: true });
    use.addEventListener("click", () => {
      if (!input.value) {
        input.focus();
        return;
      }
      finish(input.value);
    }, { once: true });
    input.addEventListener("keydown", (event) => {
      if (event.key === "Enter") {
        event.preventDefault();
        use.click();
      }
    });
    dialog.showModal();
    input.focus();
  });

  const loadMigrationScript = async (src, ready) => {
    if (ready()) return;
    let script = document.querySelector('script[src="' + src + '"]');
    await new Promise((resolve, reject) => {
      if (ready()) {
        resolve();
        return;
      }
      if (!script) {
        script = document.createElement("script");
        script.src = src;
        script.defer = true;
        document.head.appendChild(script);
      }
      script.addEventListener("load", resolve, { once: true });
      script.addEventListener("error", reject, { once: true });
    });
  };

  const ensureMigrationAssets = async () => {
    await loadMigrationScript("/static/server-migration-export.js", () => Boolean(window.JustVoxelServerMigrationExport?.init));
    await loadMigrationScript("/static/server-migration-import.js", () => Boolean(window.JustVoxelServerMigrationImport?.init));
    await loadMigrationScript("/static/server-migration-operation.js", () => Boolean(window.JustVoxelServerMigrationOperation?.init));
  };

  const syncMigrationTabs = () => {
    tabs.forEach((button) => {
      button.setAttribute("aria-selected", button.dataset.migrationTab === currentTab ? "true" : "false");
    });
  };

  const syncMigrationRecoveryAvailability = (root) => {
    if (!recoveryTab || !root) return;
    if (!Object.hasOwn(root.dataset, "migrationRecoveryAvailable")) return;
    const available = root.dataset.migrationRecoveryAvailable === "true";
    recoveryTab.hidden = !available && currentTab !== "recovery";
  };

  const inferMigrationTab = (url, root = null) => {
    const pathname = new URL(url, window.location.href).pathname;
    if (pathname.includes("/migration/export")) return "export";
    if (pathname.includes("/migration/import")) return "import";
    if (pathname.includes("/migration/recovery")) return "recovery";
    const operationName = root?.querySelector("#server-migration-operation-name")?.textContent || "";
    if (operationName.includes("Export")) return "export";
    if (operationName.includes("Import")) return "import";
    if (operationName.includes("Recovery")) return "recovery";
    return currentTab;
  };

  const extractMigrationRoot = (markup) => {
    const shell = document.createElement("div");
    shell.innerHTML = markup;
    return shell.querySelector("[data-migration-workspace-root]");
  };

  const handleMigrationAuth = async (response) => {
    if (response.redirected) {
      const target = new URL(response.url);
      if (target.pathname === "/login" || target.pathname === "/password") {
        window.location.assign(target.pathname + target.search);
        return true;
      }
    }
    if (response.status === 401) {
      window.location.assign("/login");
      return true;
    }
    if (response.status === 403) {
      const message = await response.clone().text();
      if (message.toLowerCase().includes("password change")) {
        window.location.assign("/password");
        return true;
      }
    }
    return false;
  };

  const initializeMigrationRoot = (root) => {
    window.JustVoxelServerMigrationExport?.init(root);
    window.JustVoxelServerMigrationImport?.init(root);
    window.JustVoxelServerMigrationOperation?.init(root);
  };

  const renderMigrationMarkup = (markup, responseURL) => {
    if (!content) throw new Error("Migration workspace is unavailable.");
    const root = extractMigrationRoot(markup);
    if (!root) throw new Error("Migration response could not be rendered.");

    const renderedURL = new URL(responseURL, window.location.href);
    currentURL = renderedURL.pathname + renderedURL.search;
    const progress = renderedURL.origin === window.location.origin
      ? renderedURL.pathname.match(/^\/workspace\/migration\/progress\/([A-Za-z0-9_-]+)$/)
      : null;
    if (progress) updateMigrationLocation("", progress[1]);
    currentTab = inferMigrationTab(responseURL, root);
    syncMigrationTabs();
    syncMigrationRecoveryAvailability(root);

    if (currentTab === "import") {
      const sourceEntries = Array.from(root.querySelectorAll('input[type="radio"][name="source_path"]'));
      if (sourceEntries.length === 1) sourceEntries[0].checked = true;
      const roots = Array.from(root.querySelectorAll('input[type="radio"][name="selected_root"]'));
      if (roots.length === 1) roots[0].checked = true;
      if (migrationSourceSMBPassword) {
        root.querySelectorAll("[data-migration-source-password-repeat]").forEach((node) => node.remove());
      }
    }
    content.replaceChildren(root);

    const attachFailedResetRecovery = async () => {
      if (!root.textContent.includes("Full Factory Reset")) return;
      const csrf = root.querySelector('input[name="csrf"]')?.value || "";
      if (!csrf) return;
      try {
        const statusResponse = await fetch("/api/system/workspace/reset/factory/current", {
          method: "GET",
          credentials: "same-origin",
          headers: { Accept: "application/json" },
          cache: "no-store",
        });
        if (await handleMigrationAuth(statusResponse) || !statusResponse.ok) return;
        const status = await statusResponse.json();
        const operation = status?.operation;
        if (!operation || operation.operation_type !== "factory_reset" || operation.state !== "needs_attention") return;

        const target = root.querySelector(".notice.error");
        if (!target || root.querySelector("[data-migration-reset-recovery]")) return;
        const recovery = document.createElement("div");
        recovery.className = "notice warning";
        recovery.dataset.migrationResetRecovery = "true";
        recovery.innerHTML = "<strong>A previous Full Factory Reset stopped before completion.</strong><br>Keep the current server data, release the blocked operation, and review this migration again.";
        const actions = document.createElement("div");
        actions.className = "action-row";
        const button = document.createElement("button");
        button.type = "button";
        button.className = "secondary";
        button.textContent = "Keep current server and review again";
        actions.appendChild(button);
        recovery.appendChild(actions);
        target.insertAdjacentElement("afterend", recovery);

        button.addEventListener("click", async () => {
          button.disabled = true;
          setMigrationBusy("Resolving previous failed Factory Reset…");
          const body = new URLSearchParams({
            csrf,
            operation_id: operation.operation_id,
            keep_current_state: "yes",
          });
          try {
            const response = await fetch("/api/system/workspace/reset/factory/resolve", {
              method: "POST",
              credentials: "same-origin",
              headers: {
                Accept: "application/json",
                "Content-Type": "application/x-www-form-urlencoded",
              },
              body: body.toString(),
              cache: "no-store",
            });
            if (await handleMigrationAuth(response)) return;
            const result = await response.json().catch(() => ({}));
            if (!response.ok) throw new Error(result.error || "Failed Factory Reset could not be resolved.");
            setMigrationBusy("Previous failed Factory Reset resolved. Reloading migration…", "info");
            await loadMigration(tabURLs[currentTab]);
          } catch (error) {
            button.disabled = false;
            setMigrationBusy(error?.message || "Failed Factory Reset could not be resolved.", "error");
          }
        });
      } catch (_) {
        // Leave the original exact blocker visible if recovery status cannot be loaded.
      }
    };
    void attachFailedResetRecovery();

    root.addEventListener("submit", async (event) => {
      const form = event.target.closest("form");
      if (!form || !root.contains(form)) return;
      const action = new URL(form.getAttribute("action") || currentURL, window.location.href);
      if (action.origin !== window.location.origin || !action.pathname.startsWith("/workspace/migration")) return;

      event.preventDefault();
      migrationSubmitController?.abort();
      const submitController = new AbortController();
      migrationSubmitController = submitController;
      const submitter = event.submitter;
      if (submitter) submitter.disabled = true;
      const isReview = action.pathname.endsWith("/review");
      setMigrationBusy(isReview ? "Inspecting source and building review…" : "Starting reviewed migration…");

      try {
        const body = new URLSearchParams();
        new FormData(form).forEach((value, key) => body.append(key, String(value)));
        const sourceKind = body.get("source_kind") || "";
        const exportKind = body.get("kind") || "";
        if (sourceKind === "smb") {
          if (!migrationSourceSMBPassword) {
            const password = await requestMigrationSMBPassword("import");
            if (password === null) {
              if (submitter) submitter.disabled = false;
              setMigrationBusy("");
              return;
            }
            migrationSourceSMBPassword = password;
          }
          body.set("source_smb_password", migrationSourceSMBPassword);
        } else if (sourceKind) {
          migrationSourceSMBPassword = "";
          body.delete("source_smb_password");
        }

        if (exportKind === "smb" && action.pathname.endsWith("/export/apply")) {
          if (!migrationExportSMBPassword) {
            const password = await requestMigrationSMBPassword("export");
            if (password === null) {
              if (submitter) submitter.disabled = false;
              setMigrationBusy("");
              return;
            }
            migrationExportSMBPassword = password;
          }
          body.set("smb_password", migrationExportSMBPassword);
        }
        form.querySelectorAll('input[name="source_smb_password"],input[name="smb_password"]').forEach((input) => { input.value = ""; });
        const sendMigration = () => fetch(action.pathname + action.search, {
          method: (form.method || "POST").toUpperCase(),
          credentials: "same-origin",
          headers: {
            Accept: action.pathname === "/workspace/migration/import/apply" ? "text/html, application/json" : "text/html",
            "Content-Type": "application/x-www-form-urlencoded",
            "X-JustVoxel-Migration-Fragment": "1",
          },
          body: body.toString(),
          cache: "no-store",
          redirect: "follow",
          signal: submitController.signal,
        });
        const response = action.pathname === "/workspace/migration/import/apply"
          ? await resumeReviewedIdentitySubmission(sendMigration, body.get("csrf") || "")
          : await sendMigration();
        if (await handleMigrationAuth(response)) return;
        if (response.status === 403) throw new Error("Administrator access required.");

        const responseMarkup = await response.text();
        if (!response.ok && !responseMarkup.includes("data-migration-workspace-root")) {
          const detail = responseMarkup.replace(/<[^>]*>/g, " ").replace(/\s+/g, " ").trim();
          throw new Error(detail || `Migration request failed (HTTP ${response.status}).`);
        }
        const responseURL = response.url || action.pathname;
        if (action.pathname === "/workspace/migration/recovery/resolve" && new URL(responseURL, window.location.href).pathname === "/workspace/migration") {
          currentTab = "export";
          updateMigrationLocation();
          await loadMigrationEntry();
        } else {
          renderMigrationMarkup(responseMarkup, response.url || action.pathname);
        }
        if ((response.url || "").includes("/migration/progress/")) {
          migrationSourceSMBPassword = "";
          migrationExportSMBPassword = "";
        }
        setMigrationBusy("");
      } catch (error) {
        if (error?.name === "AbortError") return;
        if (submitter && submitter.isConnected) submitter.disabled = false;
        setMigrationBusy(error?.message || "Migration operation could not be completed.", "error");
      } finally {
        if (migrationSubmitController === submitController) migrationSubmitController = null;
      }
    });

    root.addEventListener("click", async (event) => {
      const link = event.target.closest("a");
      if (!link || !root.contains(link)) return;
      const href = link.getAttribute("href") || "";
      if (href === "/") {
        event.preventDefault();
        workspaceWindow?.close();
        return;
      }
      const destination = new URL(href, window.location.href);
      if (destination.origin === window.location.origin && destination.pathname === "/" && destination.searchParams.get("workspace") === "migration") {
        event.preventDefault();
        migrationSubmitController?.abort();
        migrationSubmitController = null;
        const operation = destination.searchParams.get("operation");
        const tab = destination.searchParams.get("tab");
        updateMigrationLocation(tab, operation);
        if (operation) await loadMigration("/workspace/migration/progress/" + encodeURIComponent(operation));
        else if (Object.hasOwn(tabURLs, tab)) {
          if (tab === "import") migrationSourceSMBPassword = "";
          currentTab = tab;
          currentURL = tabURLs[tab];
          syncMigrationTabs();
          await loadMigration(currentURL);
        } else await loadMigrationEntry();
        return;
      }
      if (!href.startsWith("/workspace/migration")) return;
      event.preventDefault();
      migrationSubmitController?.abort();
      migrationSubmitController = null;
      if (href === "/workspace/migration") await loadMigrationEntry();
      else {
        if (href === "/workspace/migration/import") migrationSourceSMBPassword = "";
        await loadMigration(href);
      }
    });

    initializeMigrationRoot(root);
  };

  const loadMigration = async (url = currentURL) => {
    const sequence = ++loadSequence;
    migrationLoadController?.abort();
    const loadController = new AbortController();
    migrationLoadController = loadController;
    setMigrationBusy("Loading migration…");
    if (refreshButton) refreshButton.disabled = true;
    try {
      await ensureMigrationAssets();
      const response = await fetch(url, {
        method: "GET",
        credentials: "same-origin",
        headers: { Accept: "text/html", "X-JustVoxel-Migration-Fragment": "1" },
        cache: "no-store",
        redirect: "follow",
        signal: loadController.signal,
      });
      if (await handleMigrationAuth(response)) return;
      if (response.status === 403) throw new Error("Administrator access required.");
      if (!response.ok) throw new Error("Migration is unavailable.");

      const finalURL = new URL(response.url || url, window.location.href);
      if (finalURL.pathname === "/workspace/migration") {
        await loadMigration(tabURLs[currentTab]);
        return;
      }
      const markup = await response.text();
      if (sequence !== loadSequence) return;
      renderMigrationMarkup(markup, finalURL.href);
      setMigrationBusy("");
    } catch (error) {
      if (error?.name === "AbortError" || sequence !== loadSequence) return;
      if (content) content.replaceChildren();
      setMigrationBusy(error?.message || "Migration is unavailable.");
      state?.classList.remove("is-busy");
      migrationDialog.classList.remove("is-busy");
    } finally {
      if (migrationLoadController === loadController) migrationLoadController = null;
      if (sequence === loadSequence && refreshButton) refreshButton.disabled = false;
    }
  };

  const loadMigrationEntry = async () => {
    syncMigrationTabs();
    const sequence = ++loadSequence;
    migrationLoadController?.abort();
    const loadController = new AbortController();
    migrationLoadController = loadController;
    setMigrationBusy("Loading migration…");
    if (refreshButton) refreshButton.disabled = true;
    try {
      await ensureMigrationAssets();
      const response = await fetch("/workspace/migration", {
        method: "GET",
        credentials: "same-origin",
        headers: { Accept: "text/html", "X-JustVoxel-Migration-Fragment": "1" },
        cache: "no-store",
        redirect: "follow",
        signal: loadController.signal,
      });
      if (await handleMigrationAuth(response)) return;
      if (response.status === 403) throw new Error("Administrator access required.");
      if (!response.ok) throw new Error("Migration is unavailable.");
      const finalURL = new URL(response.url, window.location.href);
      if (sequence !== loadSequence) return;

      if (finalURL.pathname !== "/workspace/migration") {
        const markup = await response.text();
        renderMigrationMarkup(markup, finalURL.href);
        setMigrationBusy("");
        return;
      }
      const entryMarkup = await response.text();
      const entryRoot = extractMigrationRoot(entryMarkup);
      if (entryRoot) syncMigrationRecoveryAvailability(entryRoot);
      await loadMigration(tabURLs[currentTab]);
    } catch (error) {
      if (error?.name === "AbortError" || sequence !== loadSequence) return;
      if (content) content.replaceChildren();
      setMigrationBusy(error?.message || "Migration is unavailable.");
      state?.classList.remove("is-busy");
      migrationDialog.classList.remove("is-busy");
    } finally {
      if (migrationLoadController === loadController) migrationLoadController = null;
      if (sequence === loadSequence && refreshButton) refreshButton.disabled = false;
    }
  };

  const workspaceWindow = setupWorkspaceWindow(migrationDialog, {
    onOpen: () => {
      const destination = new URLSearchParams(window.location.search);
      const migrationRequested = destination.get("workspace") === "migration";
      const requestedMigrationOperation = migrationRequested ? destination.get("operation") : "";
      const requestedMigrationTab = migrationRequested ? destination.get("tab") : "";
      if (requestedMigrationOperation) return loadMigration("/workspace/migration/progress/" + encodeURIComponent(requestedMigrationOperation));
      if (requestedMigrationTab === "recovery") {
        currentTab = requestedMigrationTab;
        return loadMigration(tabURLs[currentTab]);
      }
      if (Object.hasOwn(tabURLs, requestedMigrationTab)) currentTab = requestedMigrationTab;
      return loadMigrationEntry();
    },
    onClose: () => {
      abortMigrationRequests();
      migrationSourceSMBPassword = "";
      migrationExportSMBPassword = "";
    },
  });

  migrationOpen.addEventListener("click", () => {
    if (controlCenter) controlCenter.open = false;
    workspaceWindow?.open();
  });
  closeButton?.addEventListener("click", () => workspaceWindow?.close());
  refreshButton?.addEventListener("click", () => loadMigration(currentURL));
  tabs.forEach((button) => {
    button.addEventListener("click", async () => {
      abortMigrationRequests();
      const nextTab = button.dataset.migrationTab;
      if (currentTab === "import" && nextTab !== "import") migrationSourceSMBPassword = "";
      if (currentTab === "export" && nextTab !== "export") migrationExportSMBPassword = "";
      currentTab = nextTab;
      currentURL = tabURLs[currentTab];
      updateMigrationLocation(currentTab);
      syncMigrationTabs();
      await loadMigration(currentURL);
    });
  });

  const restoreMigrationWhenRoleKnown = () => {
    if (!migrationRequested && !readWorkspaceWindowState("migration").open) return;
    if (document.body.classList.contains("role-administrator")) {
      workspaceWindow?.open();
      return;
    }
    if (!document.body.classList.contains("role-pending")) return;
    const observer = new MutationObserver(() => {
      if (document.body.classList.contains("role-pending")) return;
      observer.disconnect();
      if (document.body.classList.contains("role-administrator")) workspaceWindow?.open();
    });
    observer.observe(document.body, { attributes: true, attributeFilter: ["class"] });
  };
  restoreMigrationWhenRoleKnown();
}

const systemMonitorOpen = document.querySelector("[data-system-monitor-open]");
const systemMonitorDialog = document.querySelector("[data-system-monitor-dialog]");
if (systemMonitorOpen && systemMonitorDialog) {
  const closeButton = systemMonitorDialog.querySelector("[data-system-monitor-close]");
  const profileToggle = systemMonitorDialog.querySelector("[data-system-monitor-profile-toggle]");
  const profilePanel = systemMonitorDialog.querySelector("[data-system-monitor-profile]");
  const resetButton = systemMonitorDialog.querySelector("[data-system-monitor-reset]");
  const saveButton = systemMonitorDialog.querySelector("[data-system-monitor-save]");
  const profileStatus = systemMonitorDialog.querySelector("[data-system-monitor-profile-status]");
  const profileCSRF = document.querySelector("[data-system-monitor-csrf]");
  const processCount = systemMonitorDialog.querySelector("[data-monitor-process-count]");
  const state = systemMonitorDialog.querySelector("[data-system-monitor-state]");
  const error = systemMonitorDialog.querySelector("[data-system-monitor-error]");
  const cards = Array.from(systemMonitorDialog.querySelectorAll("[data-monitor-card]"));
  const profileInputs = Array.from(systemMonitorDialog.querySelectorAll("[data-monitor-profile]"));
  const defaultProfile = {
    system: true, cpu: true, memory: true, load: true, filesystem: true,
    diskio: true, network: true, processes: true, containers: true, sensors: true, alerts: true,
    process_count: 10,
  };
  let profile = { ...defaultProfile };
  let refreshTimer = null;

  const syncProfile = () => {
    profileInputs.forEach((input) => { input.checked = profile[input.dataset.monitorProfile] !== false; });
    if (processCount) processCount.value = String(profile.process_count || 10);
    cards.forEach((card) => { card.hidden = profile[card.dataset.monitorCard] === false; });
  };

  const loadProfile = async () => {
    try {
      const response = await fetch("/api/system-monitor/profile", {
        method: "GET",
        credentials: "same-origin",
        headers: { Accept: "application/json" },
        cache: "no-store",
      });
      if (response.status === 401) {
        window.location.assign("/login");
        return;
      }
      if (!response.ok) throw new Error("profile unavailable");
      profile = { ...defaultProfile, ...(await response.json()) };
      if (profileStatus) profileStatus.textContent = "";
    } catch (_) {
      profile = { ...defaultProfile };
      if (profileStatus) profileStatus.textContent = "Using default profile";
    }
    syncProfile();
  };

  const saveProfile = async () => {
    if (!saveButton || !profileCSRF) return;
    const body = new URLSearchParams({ csrf: profileCSRF.value });
    [
      "system", "cpu", "memory", "load", "filesystem", "diskio",
      "network", "processes", "containers", "sensors", "alerts",
    ].forEach((key) => body.set(key, profile[key] === false ? "false" : "true"));
    body.set("process_count", String(profile.process_count || 10));

    saveButton.disabled = true;
    if (profileStatus) profileStatus.textContent = "Saving…";
    try {
      const response = await fetch("/api/system-monitor/profile", {
        method: "POST",
        credentials: "same-origin",
        headers: { Accept: "application/json", "Content-Type": "application/x-www-form-urlencoded" },
        body: body.toString(),
      });
      if (response.status === 401) {
        window.location.assign("/login");
        return;
      }
      if (!response.ok) throw new Error("save failed");
      profile = { ...defaultProfile, ...(await response.json()) };
      syncProfile();
      refreshMonitor();
      if (profileStatus) profileStatus.textContent = "Saved";
      if (profilePanel) profilePanel.hidden = true;
    } catch (_) {
      if (profileStatus) profileStatus.textContent = "Could not save";
    } finally {
      saveButton.disabled = false;
    }
  };
  const fixed = (value, digits = 1) => {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed.toFixed(digits) : "—";
  };
  const percent = (value) => {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed.toFixed(1) + "%" : "—";
  };
  const bytes = (value) => {
    let parsed = Number(value);
    if (!Number.isFinite(parsed)) return "—";
    const units = ["B", "KiB", "MiB", "GiB", "TiB"];
    let index = 0;
    while (Math.abs(parsed) >= 1024 && index < units.length - 1) { parsed /= 1024; index += 1; }
    return parsed.toFixed(index === 0 ? 0 : 1) + " " + units[index];
  };
  const rate = (value) => {
    const rendered = bytes(value);
    return rendered === "—" ? rendered : rendered + "/s";
  };
  const setText = (selector, value) => {
    const node = systemMonitorDialog.querySelector(selector);
    if (node) node.textContent = value;
  };
  const renderLines = (selector, entries) => {
    const node = systemMonitorDialog.querySelector(selector);
    if (!node) return;
    node.replaceChildren();
    entries.filter((entry) => entry[1] !== undefined && entry[1] !== null && entry[1] !== "—").forEach(([label, value]) => {
      const row = document.createElement("div");
      const key = document.createElement("span");
      const val = document.createElement("strong");
      key.textContent = label; val.textContent = String(value);
      row.append(key, val); node.appendChild(row);
    });
  };
  const renderTable = (selector, rows, columns) => {
    const node = systemMonitorDialog.querySelector(selector);
    if (!node) return;
    node.replaceChildren();
    if (!Array.isArray(rows) || rows.length === 0) {
      const empty = document.createElement("p");
      empty.className = "muted compact"; empty.textContent = "No data";
      node.appendChild(empty); return;
    }
    const table = document.createElement("table");
    const head = document.createElement("thead");
    const headRow = document.createElement("tr");
    columns.forEach((column) => { const th = document.createElement("th"); th.textContent = column.label; headRow.appendChild(th); });
    head.appendChild(headRow);
    const body = document.createElement("tbody");
    rows.forEach((row) => {
      const tr = document.createElement("tr");
      columns.forEach((column) => {
        const td = document.createElement("td");
        const value = column.render(row);
        td.textContent = value === undefined || value === null || value === "" ? "—" : String(value);
        tr.appendChild(td);
      });
      body.appendChild(tr);
    });
    table.append(head, body); node.appendChild(table);
  };
  const renderMonitor = (data) => {
    const system = data.system || {}, uptime = data.uptime || {}, cpu = data.cpu || {};
    const mem = data.mem || {}, swap = data.memswap || {}, load = data.load || {};
    setText("[data-monitor-system-host]", "JustVoxel");
    renderLines("[data-monitor-system-lines]", [
      ["Hostname", system.hostname],
      ["Kernel", system.os_version],
      ["Uptime", typeof uptime === "string" ? uptime : uptime.value],
    ]);
    setText("[data-monitor-cpu-total]", percent(cpu.total));
    renderLines("[data-monitor-cpu-lines]", [["Cores", cpu.cpucore], ["User", percent(cpu.user)], ["System", percent(cpu.system)], ["I/O wait", percent(cpu.iowait)], ["Idle", percent(cpu.idle)]]);
    setText("[data-monitor-memory-main]", percent(mem.percent));
    renderLines("[data-monitor-memory-lines]", [["Used", bytes(mem.used)], ["Available", bytes(mem.available)], ["Total", bytes(mem.total)], ["Swap", swap.total ? bytes(swap.used) + " / " + bytes(swap.total) : "Not used"]]);
    setText("[data-monitor-load-main]", fixed(load.min1) + " / " + fixed(load.min5) + " / " + fixed(load.min15));
    renderLines("[data-monitor-load-lines]", [["1 minute", fixed(load.min1)], ["5 minutes", fixed(load.min5)], ["15 minutes", fixed(load.min15)]]);
    renderTable("[data-monitor-filesystem]", Array.isArray(data.fs) ? data.fs : [], [
      {label:"Mount", render:(row)=>row.mnt_point || row.mountpoint || row.device_name},
      {label:"Used", render:(row)=>percent(row.percent)}, {label:"Free", render:(row)=>bytes(row.free)}, {label:"Size", render:(row)=>bytes(row.size)}
    ]);
    renderTable("[data-monitor-diskio]", Array.isArray(data.diskio) ? data.diskio : [], [
      {label:"Disk", render:(row)=>row.disk_name || row.name}, {label:"Read", render:(row)=>rate(row.read_bytes_rate_per_sec)}, {label:"Write", render:(row)=>rate(row.write_bytes_rate_per_sec)}
    ]);
    renderTable("[data-monitor-network]", Array.isArray(data.network) ? data.network : [], [
      {label:"Interface", render:(row)=>row.interface_name || row.name}, {label:"RX", render:(row)=>rate(row.bytes_recv_rate_per_sec)}, {label:"TX", render:(row)=>rate(row.bytes_sent_rate_per_sec)}
    ]);
    const processLimit = [5, 10, 15].includes(Number(profile.process_count)) ? Number(profile.process_count) : 10;
    const processesCard = systemMonitorDialog.querySelector('[data-monitor-card="processes"]');
    if (processesCard) processesCard.dataset.processCount = String(processLimit);
    const processes = (Array.isArray(data.processlist) ? data.processlist : [])
      .slice().sort((a,b)=>Number(b.cpu_percent || 0)-Number(a.cpu_percent || 0));
    renderTable("[data-monitor-processes]", processes.slice(0, processLimit), [
      {label:"Process", render:(row)=>row.name}, {label:"CPU", render:(row)=>percent(row.cpu_percent)}, {label:"Memory", render:(row)=>percent(row.memory_percent)}
    ]);
    renderTable("[data-monitor-containers]", Array.isArray(data.containers) ? data.containers : [], [
      {label:"Container", render:(row)=>row.name}, {label:"Status", render:(row)=>row.status},
      {label:"CPU", render:(row)=>percent(row.cpu_percent ?? (row.cpu && row.cpu.total))},
      {label:"Memory", render:(row)=>bytes(row.memory_usage ?? (row.memory && row.memory.usage))},
      {label:"Limit", render:(row)=>bytes(row.memory_limit ?? (row.memory && row.memory.limit))}
    ]);
    renderTable("[data-monitor-sensors]", Array.isArray(data.sensors) ? data.sensors : [], [
      {label:"Sensor", render:(row)=>row.label || row.name},
      {label:"Value", render:(row)=>row.value === undefined || row.value === null ? "—" : String(row.value) + (row.unit || "")}
    ]);
    const alerts = (Array.isArray(data.alert) ? data.alert : []).filter((row) => row && (row.end === undefined || row.end === null || Number(row.end) < 0));
    const alertsCard = systemMonitorDialog.querySelector('[data-monitor-card="alerts"]');
    if (alertsCard) alertsCard.hidden = profile.alerts === false || alerts.length === 0;
    if (alerts.length > 0) {
      renderTable("[data-monitor-alerts]", alerts, [
        {label:"State", render:(row)=>row.state},
        {label:"Type", render:(row)=>row.type},
        {label:"Message", render:(row)=>row.global_msg || row.desc || "Active alert"}
      ]);
    }
    if (state) state.textContent = "Live resources · refresh every 2 seconds";
    if (error) error.hidden = true;
  };
  let monitorRequest = 0;
  const refreshMonitor = async () => {
    const request = ++monitorRequest;
    try {
      const response = await fetch("/api/system-monitor", {method:"GET", credentials:"same-origin", headers:{Accept:"application/json"}, cache:"no-store"});
      if (response.status === 401) { window.location.assign("/login"); return; }
      if (!response.ok) throw new Error("monitor unavailable");
      const data = await response.json();
      if (request === monitorRequest) { lastMonitorData = data; renderMonitor(data); }
    } catch (_) {
      if (state) state.textContent = "System Monitor unavailable";
      if (error) { error.textContent = "Glances is not responding yet."; error.hidden = false; }
    }
  };
  const stopRefresh = () => { if (refreshTimer) window.clearInterval(refreshTimer); refreshTimer = null; };
  const startRefresh = async () => {
    if (profilePanel) profilePanel.hidden = true;
    await loadProfile();
    refreshMonitor();
    stopRefresh();
    refreshTimer = window.setInterval(refreshMonitor, 2000);
  };

  const workspaceWindow = setupWorkspaceWindow(systemMonitorDialog, {
    onOpen: startRefresh,
    onClose: stopRefresh,
  });

  systemMonitorOpen.addEventListener("click", () => {
    if (controlCenter) controlCenter.open = false;
    workspaceWindow?.open();
  });
  if (closeButton) closeButton.addEventListener("click", () => workspaceWindow?.close());

  if (profileToggle && profilePanel) profileToggle.addEventListener("click", () => {
    profilePanel.hidden = !profilePanel.hidden;
    if (profileStatus) profileStatus.textContent = "";
  });
  document.addEventListener("pointerdown", (event) => {
    if (profilePanel?.hidden || !systemMonitorDialog.open) return;
    if (profilePanel.contains(event.target) || profileToggle?.contains(event.target)) return;
    profilePanel.hidden = true;
  });
  profileInputs.forEach((input) => input.addEventListener("change", () => {
    profile[input.dataset.monitorProfile] = input.checked;
    syncProfile();
    if (profileStatus) profileStatus.textContent = "Not saved";
  }));
  if (processCount) processCount.addEventListener("change", () => {
    profile.process_count = Number(processCount.value);
    if (lastMonitorData) renderMonitor(lastMonitorData);
    if (profileStatus) profileStatus.textContent = "Not saved";
  });
  if (resetButton) resetButton.addEventListener("click", () => {
    profile = { ...defaultProfile };
    syncProfile();
    refreshMonitor();
    if (profileStatus) profileStatus.textContent = "Not saved";
  });
  if (saveButton) saveButton.addEventListener("click", saveProfile);

  const monitorRestoreRoleAllowed = () => !document.body.classList.contains("role-pending") &&
    ["role-administrator", "role-operator", "role-viewer"].some((role) => document.body.classList.contains(role));
  const restoreWhenRoleKnown = () => {
    if (!readWorkspaceWindowState("system-monitor").open) return;
    if (monitorRestoreRoleAllowed()) {
      workspaceWindow?.open();
      return;
    }
    if (!document.body.classList.contains("role-pending")) return;
    const observer = new MutationObserver(() => {
      if (document.body.classList.contains("role-pending")) return;
      observer.disconnect();
      if (monitorRestoreRoleAllowed()) workspaceWindow?.open();
    });
    observer.observe(document.body, { attributes: true, attributeFilter: ["class"] });
  };
  restoreWhenRoleKnown();
}


const minecraftOpen = document.querySelector("[data-minecraft-open]");
const minecraftDialog = document.querySelector("[data-minecraft-workspace-dialog]");
if (minecraftOpen && minecraftDialog) {
  const closeButton = minecraftDialog.querySelector("[data-minecraft-close]");
  const refreshButton = minecraftDialog.querySelector("[data-minecraft-refresh]");
  const state = minecraftDialog.querySelector("[data-minecraft-state]");
  const content = minecraftDialog.querySelector("[data-minecraft-workspace-content]");
  const tabs = Array.from(minecraftDialog.querySelectorAll("[data-minecraft-tab]"));
  const csrf = document.querySelector("[data-minecraft-workspace-csrf]")?.value || "";
  const settingsTabs = new Set(["memory", "gameplay", "crossplay"]);
  let currentTab = "overview";
  let loadSequence = 0;

  const messageNode = (value, className = "muted compact") => {
    const node = document.createElement("p");
    node.className = className;
    node.textContent = value;
    return node;
  };

  const noticeNode = (value, kind = "success") => {
    const node = document.createElement("div");
    node.className = "notice " + kind;
    node.textContent = value;
    return node;
  };

  const requestWorkspaceJSON = async (url, options = {}) => {
    const response = await fetch(url, {
      credentials: "same-origin",
      cache: "no-store",
      ...options,
      headers: {
        Accept: "application/json",
        ...(options.headers || {}),
      },
    });
    let payload = {};
    try {
      payload = await response.json();
    } catch (_) {
      payload = {};
    }
    if (response.status === 401) {
      window.location.assign("/login");
      return null;
    }
    if (response.status === 403 && String(payload.error || "").toLowerCase().includes("password")) {
      window.location.assign("/password");
      return null;
    }
    if (!response.ok) {
      throw new Error(payload.error || "Minecraft operation could not be completed.");
    }
    return payload;
  };

  const renderOverview = async (sequence, message = "") => {
    const snapshot = await requestWorkspaceJSON("/api/dashboard-status");
    if (!snapshot || sequence !== loadSequence || !content) return;

    const minecraft = snapshot?.status?.minecraft || {};
    const players = snapshot?.players || {};
    const root = document.createElement("div");
    root.className = "minecraft-overview-compact";
    if (message) root.appendChild(noticeNode(message));

    const grid = document.createElement("div");
    grid.className = "minecraft-overview-grid";
    [
      ["Status", minecraft.state || "—"],
      ["Version", minecraft.version || "—"],
      ["Players", minecraft.configured ? (players.max ?? minecraft.max_players ?? "—") : "Not configured"],
      ["Online", minecraft.configured ? (players.online ?? 0) : "—"],
    ].forEach(([label, value]) => {
      const card = document.createElement("article");
      card.className = "minecraft-overview-card";
      const labelNode = document.createElement("span");
      labelNode.textContent = label;
      const valueNode = document.createElement("strong");
      valueNode.textContent = value;
      card.append(labelNode, valueNode);
      grid.appendChild(card);
    });
    root.appendChild(grid);

    const playerLine = document.createElement("div");
    playerLine.className = "minecraft-overview-playerline";
    const playerLabel = document.createElement("strong");
    playerLabel.textContent = "Online";
    playerLine.appendChild(playerLabel);
    if (!minecraft.configured || players.state === "not_configured") playerLine.appendChild(messageNode("Minecraft is not configured yet."));
    else if (players.state === "stopped") playerLine.appendChild(messageNode("Minecraft is stopped."));
    else if (players.state === "unavailable") playerLine.appendChild(messageNode(players.error || "Player information is temporarily unavailable."));
    else if ((players.online ?? 0) === 0) playerLine.appendChild(messageNode("No players online."));
    else if (Array.isArray(players.names) && players.names.length > 0) {
      const list = document.createElement("div");
      list.className = "player-list";
      players.names.forEach((name) => {
        const chip = document.createElement("span");
        chip.className = "player-chip";
        chip.textContent = name;
        list.appendChild(chip);
      });
      playerLine.appendChild(list);
    } else {
      playerLine.appendChild(messageNode(`${players.online ?? 0} players online.`));
    }
    root.appendChild(playerLine);

    const canViewLogs = document.body.classList.contains("role-administrator") || document.body.classList.contains("role-operator");
    if (canViewLogs) {
      const logs = await requestWorkspaceJSON("/api/minecraft/workspace/logs");
      if (!logs || sequence !== loadSequence || !content) return;
      const section = document.createElement("section");
      section.className = "panel details minecraft-overview-logs";
      const heading = document.createElement("div");
      heading.className = "section-heading";
      const headingText = document.createElement("div");
      const eyebrow = document.createElement("p");
      eyebrow.className = "eyebrow";
      eyebrow.textContent = "Diagnostics";
      const title = document.createElement("h2");
      title.textContent = "Recent logs";
      headingText.className = "diagnostics-heading";
      headingText.append(eyebrow, title);
      heading.appendChild(headingText);
      const pre = document.createElement("pre");
      pre.className = "log-box";
      pre.textContent = Array.isArray(logs.lines) && logs.lines.length ? logs.lines.join("\n") : "No recent log lines available.";
      section.append(heading, pre);
      root.appendChild(section);
    }

    content.replaceChildren(root);
  };

  const formatRemainingMemory = (mib) => {
    if (mib >= 1024) return `${(mib / 1024).toFixed(mib % 1024 === 0 ? 0 : 1)} GB`;
    return `${Math.max(0, Math.round(mib))} MB`;
  };

  const initNativeMemoryControls = (root, defaults) => {
    const gameMemory = root.querySelector('[name="java_memory"]');
    const maxMemory = root.querySelector('[name="container_memory"]');
    const maxPlayers = Number(root.dataset.maxPlayers || 10);
    const memoryStatus = root.querySelector("[data-minecraft-memory-status]");
    const buttons = Array.from(root.querySelectorAll("[data-minecraft-memory-preset]"));
    const totalMiB = Number(defaults?.system_memory_mib || 0);
    const minimumReserveMiB = Number(defaults?.system_reserve_minimum_mib || 1024);
    const recommendedReserveMiB = Number(defaults?.system_reserve_recommended_mib || 1536);
    let activePreset = "";

    const parseMemoryMiB = (value) => {
      const match = String(value || "").trim().match(/^([1-9][0-9]*)([mMgG])$/);
      if (!match) return 0;
      const amount = Number(match[1]);
      return match[2].toUpperCase() === "G" ? amount * 1024 : amount;
    };
    const memoryValue = (mib) => mib % 1024 === 0 ? `${mib / 1024}G` : `${mib}M`;
    const markPreset = (name) => {
      activePreset = name;
      buttons.forEach((button) => button.classList.toggle("is-selected", button.dataset.minecraftMemoryPreset === name));
    };
    const updateStatus = () => {
      if (!memoryStatus || !maxMemory) return;
      const maximumMiB = parseMemoryMiB(maxMemory.value);
      memoryStatus.classList.remove("warning", "danger");
      if (!totalMiB) {
        memoryStatus.textContent = "System memory could not be detected. JustVoxel will validate again before Apply.";
        memoryStatus.classList.add("warning");
        return;
      }
      if (!maximumMiB) {
        memoryStatus.textContent = "Enter memory as a size such as 6G or 6144M.";
        memoryStatus.classList.add("warning");
        return;
      }
      const remaining = totalMiB - maximumMiB;
      if (remaining < minimumReserveMiB) {
        memoryStatus.textContent = `Not enough memory remains outside Minecraft: about ${formatRemainingMemory(remaining)}. Reduce Maximum Minecraft memory before continuing.`;
        memoryStatus.classList.add("danger");
      } else if (remaining < recommendedReserveMiB) {
        memoryStatus.textContent = `Tight memory configuration: about ${formatRemainingMemory(remaining)} remains outside Minecraft.`;
        memoryStatus.classList.add("warning");
      } else {
        memoryStatus.textContent = `Memory remaining outside Minecraft: about ${formatRemainingMemory(remaining)}.`;
      }
    };
    const applyPreset = (name) => {
      if (name === "custom") {
        markPreset("custom");
        gameMemory?.focus();
        updateStatus();
        return;
      }
      if (!gameMemory || !maxMemory) return;
      const playerGroups = Math.max(1, Math.ceil(Math.max(1, maxPlayers) / 10));
      const playerExtra = Math.min(Math.max(playerGroups - 1, 0), 4);
      const totalGiB = totalMiB > 0 ? totalMiB / 1024 : 8;
      let baseRecommended = totalGiB < 6 ? 2 : Math.max(4, Math.min(8, Math.floor((totalGiB - 2) / 2)));
      const recommendedHeap = Math.min(12, baseRecommended + playerExtra);
      let heapGiB = recommendedHeap;
      if (name === "light") heapGiB = Math.max(2, recommendedHeap - 2);
      if (name === "high") heapGiB = Math.min(14, recommendedHeap + 2);
      let maximumGiB = heapGiB + (heapGiB >= 4 ? 2 : 1);
      if (totalMiB > 0) {
        const reserveMiB = name === "high" ? minimumReserveMiB : recommendedReserveMiB;
        const allowedMaximumGiB = Math.floor(Math.max(0, totalMiB - reserveMiB) / 1024);
        if (allowedMaximumGiB >= 2 && maximumGiB > allowedMaximumGiB) {
          maximumGiB = allowedMaximumGiB;
          heapGiB = Math.max(1, Math.min(heapGiB, maximumGiB - 1));
        }
      }
      gameMemory.value = memoryValue(heapGiB * 1024);
      maxMemory.value = memoryValue(maximumGiB * 1024);
      markPreset(name);
      updateStatus();
    };

    buttons.forEach((button) => button.addEventListener("click", () => applyPreset(button.dataset.minecraftMemoryPreset)));
    [gameMemory, maxMemory].forEach((input) => input?.addEventListener("input", () => {
      if (activePreset && activePreset !== "custom") markPreset("custom");
      updateStatus();
    }));
    updateStatus();
  };

  const buildSettingsForm = (tab, payload, message = "") => {
    const minecraft = payload?.minecraft || {};
    const defaults = payload?.defaults || {};
    const root = document.createElement("div");
    root.className = "minecraft-native-settings";
    if (message) root.appendChild(noticeNode(message));

    if (!payload?.configured) {
      root.appendChild(noticeNode("Minecraft is not configured yet. Use first-run setup to configure this appliance.", "warning"));
      return root;
    }

    const section = document.createElement("section");
    section.className = "panel details minecraft-workspace-section";
    const form = document.createElement("form");
    form.className = "minecraft-native-form";
    form.dataset.minecraftSettingsTab = tab;

    if (tab === "memory") {
      section.innerHTML = `
        <div class="section-heading"><div><p class="eyebrow">Memory</p><h2>Minecraft memory</h2></div></div>
        <p class="muted compact">Choose a starting point or enter exact memory values. JustVoxel validates system headroom before applying.</p>
        <div class="minecraft-native-presets" role="group" aria-label="Memory starting points">
          <button type="button" class="secondary" data-minecraft-memory-preset="light">Light</button>
          <button type="button" class="secondary" data-minecraft-memory-preset="recommended">Recommended</button>
          <button type="button" class="secondary" data-minecraft-memory-preset="high">High memory</button>
          <button type="button" class="secondary" data-minecraft-memory-preset="custom">Custom</button>
        </div>
        <div class="minecraft-native-grid">
          <label>Minecraft game memory<input name="java_memory" required autocomplete="off"><span class="minecraft-native-help">Memory available directly to the Java server, for example 4G or 6144M.</span></label>
          <label>Maximum Minecraft memory<input name="container_memory" required autocomplete="off"><span class="minecraft-native-help">Maximum for the complete Minecraft process including runtime overhead.</span></label>
        </div>
        <div class="minecraft-native-memory-status" data-minecraft-memory-status></div>`;
      section.dataset.maxPlayers = String(minecraft.max_players || defaults.max_players || 10);
      section.querySelector('[name="java_memory"]').value = minecraft.java_memory || "";
      section.querySelector('[name="container_memory"]').value = minecraft.container_memory || "";
      form.appendChild(section);
      initNativeMemoryControls(section, defaults);
    } else if (tab === "gameplay") {
      section.innerHTML = `
        <div class="section-heading"><div><p class="eyebrow">Server</p><h2>Gameplay</h2></div></div>
        <div class="minecraft-native-grid">
          <label>Maximum players<input name="max_players" type="number" min="1" step="1" required></label>
          <label>Server welcome message (MOTD)<input name="motd" required></label>
          <label>Game mode<select name="game_mode" required>
            <option value="survival">Survival</option>
            <option value="creative">Creative</option>
            <option value="adventure">Adventure</option>
            <option value="spectator">Spectator</option>
          </select></label>
        </div>`;
      section.querySelector('[name="max_players"]').value = String(minecraft.max_players || 10);
      section.querySelector('[name="motd"]').value = minecraft.motd || "";
      section.querySelector('[name="game_mode"]').value = minecraft.game_mode || "survival";
      form.appendChild(section);
    } else if (tab === "crossplay") {
      section.innerHTML = `
        <div class="section-heading"><div><p class="eyebrow">Network</p><h2>Java & Bedrock</h2></div></div>
        <div class="minecraft-native-grid">
          <label>Minecraft Java port<input name="java_port" type="number" min="1" max="65535" required></label>
          <label>Bedrock UDP port<input name="bedrock_port" type="number" min="1" max="65535" required></label>
        </div>
        <label class="minecraft-native-toggle system-ups-shutdown-switch"><span>Enable Bedrock cross-play</span><input name="bedrock_enabled" type="checkbox"></label>
        <p class="muted compact">Port and Bedrock changes update the appliance firewall when applied. Minecraft is not restarted automatically for these non-memory changes.</p>
        <div data-crossplay-compatibility class="muted compact">Checking Geyser/Floodgate compatibility…</div><button type="button" class="secondary" data-open-version>Open Version</button>`;
      section.querySelector('[name="java_port"]').value = String(minecraft.java_port || 25565);
      section.querySelector('[name="bedrock_port"]').value = String(minecraft.bedrock_port || 19132);
      section.querySelector('[name="bedrock_enabled"]').checked = Boolean(minecraft.bedrock_enabled);
      section.querySelector('[name="bedrock_enabled"]').disabled = minecraft.server_type === "vanilla";
      if (minecraft.server_type === "vanilla") section.querySelector('[data-crossplay-compatibility]').textContent = "Managed Bedrock cross-play currently requires Paper or Purpur.";
      section.querySelector('[data-open-version]')?.addEventListener("click", () => document.querySelector("[data-version-open]")?.click());
      requestWorkspaceJSON("/api/version/workspace/status").then((status) => {
        const node = section.querySelector("[data-crossplay-compatibility]");
        if (!node || !status) return;
        const installed = status.installed || "Unknown";
        if (minecraft.server_type === "vanilla") { node.textContent = "Managed Bedrock cross-play currently requires Paper or Purpur."; return; }
        if (!status.crossplay_enabled) node.textContent = `Cross-play disabled · Minecraft ${installed}.`;
        else if (status.crossplay_compatible) node.textContent = `Cross-play enabled · Minecraft ${installed} · Geyser/Floodgate compatible with ${status.geyser_supported_version}.`;
        else node.textContent = status.reason || "Geyser/Floodgate compatibility is unavailable.";
      }).catch(() => { const node = section.querySelector("[data-crossplay-compatibility]"); if (node && minecraft.server_type !== "vanilla") node.textContent = "Geyser/Floodgate compatibility is unavailable."; });
      form.appendChild(section);
    }

    const actions = document.createElement("div");
    actions.className = "minecraft-native-actions";
    const reviewButton = document.createElement("button");
    reviewButton.type = "submit";
    reviewButton.textContent = "Review changes";
    const note = document.createElement("span");
    note.className = "muted";
    note.textContent = "Nothing changes until Review and Apply.";
    actions.append(note, reviewButton);
    form.appendChild(actions);
    root.appendChild(form);
    return root;
  };

  const settingsParams = (form) => {
    const params = new URLSearchParams();
    new FormData(form).forEach((value, key) => params.append(key, String(value)));
    params.set("csrf", csrf);
    params.set("tab", form.dataset.minecraftSettingsTab || "");
    return params;
  };

  const minecraftSettingsReviewValue = (change, value) => {
    if (change.field !== "bedrock_enabled") return value;
    switch (String(value).trim().toLowerCase()) {
      case "yes":
      case "true":
      case "on":
        return "Enabled";
      case "no":
      case "false":
      case "off":
        return "Disabled";
      default:
        return value;
    }
  };

  const renderSettingsReview = (root, plan, params) => {
    root.querySelector("[data-minecraft-native-review]")?.remove();
    const settingsForm = root.querySelector("[data-minecraft-settings-tab]");
    if (settingsForm) settingsForm.hidden = false;
    const review = document.createElement("section");
    review.className = "panel details minecraft-native-review";
    review.dataset.minecraftNativeReview = "true";

    const heading = document.createElement("div");
    heading.className = "section-heading";
    const headingText = document.createElement("div");
    const eyebrow = document.createElement("p");
    eyebrow.className = "eyebrow";
    eyebrow.textContent = "Review";
    const title = document.createElement("h2");
    title.textContent = "Proposed changes";
    headingText.append(eyebrow, title);
    heading.appendChild(headingText);
    if (plan.restart_required) {
      const badge = document.createElement("span");
      badge.className = "badge";
      badge.textContent = "Restart required";
      heading.appendChild(badge);
    }
    review.appendChild(heading);

    (plan.warnings || []).forEach((warning) => review.appendChild(noticeNode(warning, "warning")));

    const changes = Array.isArray(plan.changes) ? plan.changes : [];
    if (changes.length === 0) {
      review.appendChild(messageNode("No configuration changes were detected."));
      root.appendChild(review);
      return;
    }

    if (settingsForm) settingsForm.hidden = true;
    const changeList = document.createElement("div");
    changeList.className = "minecraft-native-change-list";
    changes.forEach((change) => {
      const row = document.createElement("div");
      row.className = "minecraft-native-change-row";
      const label = document.createElement("div");
      const strong = document.createElement("strong");
      strong.textContent = change.label || change.field || "Setting";
      label.appendChild(strong);
      if (change.restart_required) label.appendChild(messageNode("Minecraft restart required.", "muted"));
      const values = document.createElement("div");
      const before = document.createElement("span");
      before.className = "minecraft-native-change-value";
      before.textContent = minecraftSettingsReviewValue(change, change.before || "—");
      const arrow = document.createElement("span");
      arrow.textContent = "→";
      const after = document.createElement("span");
      after.className = "minecraft-native-change-value";
      after.textContent = minecraftSettingsReviewValue(change, change.after || "—");
      values.append(before, arrow, after);
      row.append(label, values);
      changeList.appendChild(row);
    });
    review.appendChild(changeList);

    if (Number.isFinite(Number(plan.memory_remaining_mib)) && Number(plan.memory_remaining_mib) > 0) {
      review.appendChild(messageNode(`Memory remaining outside Minecraft after the configured maximum: ${plan.memory_remaining_mib} MiB.`));
    }

    if (plan.confirmation_required) {
      const names = Array.isArray(plan.players) && plan.players.length ? " Connected: " + plan.players.join(", ") + "." : "";
      review.appendChild(noticeNode(`Players are online. Applying these changes requires a Minecraft restart and will disconnect ${plan.online || 0} player(s).${names}`, "warning"));
    } else if (plan.memory_restart_required) {
      review.appendChild(noticeNode("Minecraft will restart automatically for these memory changes after JustVoxel checks for online players.", "warning"));
    } else if (plan.restart_required) {
      review.appendChild(noticeNode("The new settings will be saved, but Minecraft will not restart automatically for these non-memory changes.", "warning"));
    }

    const actions = document.createElement("div");
    actions.className = "minecraft-native-actions";
    const cancel = document.createElement("button");
    cancel.type = "button";
    cancel.className = "secondary";
    cancel.textContent = "Cancel review";
    cancel.addEventListener("click", () => {
      review.remove();
      if (settingsForm) settingsForm.hidden = false;
    });
    const apply = document.createElement("button");
    apply.type = "button";
    apply.textContent = plan.confirmation_required ? "Restart Minecraft and apply" : "Apply changes";
    actions.append(cancel, apply);
    review.appendChild(actions);
    root.prepend(review);

    apply.addEventListener("click", async () => {
      apply.disabled = true;
      cancel.disabled = true;
      if (state) state.textContent = plan.memory_restart_required ? "Applying and restarting Minecraft…" : "Applying…";
      try {
        const applyParams = new URLSearchParams(params);
        if (plan.confirmation_required) applyParams.set("confirm_players", "yes");
        const result = await requestWorkspaceJSON("/api/minecraft/workspace/settings/apply", {
          method: "POST",
          headers: { "Content-Type": "application/x-www-form-urlencoded" },
          body: applyParams.toString(),
        });
        if (!result) return;
        if (result.confirmation_required && !result.applied) {
          if (state) state.textContent = "Player confirmation required.";
          renderSettingsReview(root, result, params);
          return;
        }
        if (!result.applied) throw new Error(result.error || "Minecraft settings were not applied.");
        let savedMessage = result.message || "Minecraft settings saved.";
        if (result.restarted) savedMessage = "Minecraft settings saved and Minecraft restarted.";
        else if (result.restart_deferred) savedMessage = "Minecraft settings saved. Restart remains pending.";
        await loadCurrentTab(savedMessage);
      } catch (error) {
        if (state) state.textContent = error?.message || "Minecraft settings could not be applied.";
        apply.disabled = false;
        cancel.disabled = false;
      }
    });
  };

  const renderSettingsTab = async (sequence, message = "") => {
    const payload = await requestWorkspaceJSON("/api/minecraft/workspace/settings");
    if (!payload || sequence !== loadSequence || !content) return;
    const root = buildSettingsForm(currentTab, payload, message);
    content.replaceChildren(root);
    const form = root.querySelector("[data-minecraft-settings-tab]");
    form?.addEventListener("submit", async (event) => {
      event.preventDefault();
      const submitter = event.submitter;
      if (submitter) submitter.disabled = true;
      if (state) state.textContent = "Reviewing…";
      try {
        const params = settingsParams(form);
        const plan = await requestWorkspaceJSON("/api/minecraft/workspace/settings/plan", {
          method: "POST",
          headers: { "Content-Type": "application/x-www-form-urlencoded" },
          body: params.toString(),
        });
        if (!plan) return;
        renderSettingsReview(root, plan, params);
        if (state) state.textContent = "";
      } catch (error) {
        if (state) state.textContent = error?.message || "Minecraft settings could not be reviewed.";
      } finally {
        if (submitter) submitter.disabled = false;
      }
    });
  };

  const renderPlayers = async (sequence, message = "") => {
    const [snapshot, payload] = await Promise.all([
      requestWorkspaceJSON("/api/dashboard-status"),
      requestWorkspaceJSON("/api/minecraft/workspace/whitelist"),
    ]);
    if (!snapshot || !payload || sequence !== loadSequence || !content) return;
    const root = document.createElement("div");
    root.className = "minecraft-native-operations";
    if (message) root.appendChild(noticeNode(message));

    const minecraft = snapshot?.status?.minecraft || {};
    const players = snapshot?.players || {};
    const controls = document.createElement("section");
    controls.className = "minecraft-player-controls";
    controls.appendChild(messageNode(`Online players: ${players.online ?? 0} / ${players.max ?? minecraft.max_players ?? "—"}`));
    if (!minecraft.configured || players.state === "not_configured") controls.appendChild(messageNode("Minecraft is not configured yet."));
    else if (players.state === "unavailable") controls.appendChild(messageNode(players.error || "Player information is temporarily unavailable."));
    const form = document.createElement("form");
    form.className = "minecraft-native-form";
    form.innerHTML = `
      <div class="minecraft-player-add">
        <label class="minecraft-player-platform">Platform<select name="platform"><option value="java">Java</option><option value="bedrock">Bedrock</option></select></label>
        <label class="minecraft-player-input">Player name / Xbox gamertag / Floodgate UUID<input name="name" required maxlength="64" autocomplete="off"></label>
        <button type="submit">Add player</button>
      </div>
      <p class="muted compact">For Bedrock, use the Xbox gamertag without the leading dot.<br>If Floodgate cannot resolve it, use the player's Floodgate UUID.</p>`;
    controls.appendChild(form);
    root.appendChild(controls);

    const section = document.createElement("section");
    section.className = "minecraft-whitelist-section";
    section.innerHTML = '<div class="section-heading"><h2>Whitelist</h2><div class="minecraft-whitelist-state"></div></div><div class="minecraft-whitelist-list" data-minecraft-native-whitelist></div>';
    const enabled = payload.whitelist_enabled;
    const status = document.createElement("span");
    status.className = "badge";
    status.textContent = enabled === true ? "Enabled" : enabled === false ? "Disabled" : "Unavailable";
    const header = section.querySelector(".minecraft-whitelist-state");
    header.appendChild(status);
    if (payload.can_toggle && typeof enabled === "boolean") {
      const toggle = document.createElement("button");
      toggle.type = "button";
      toggle.className = "minecraft-whitelist-switch";
      toggle.setAttribute("role", "switch");
      toggle.setAttribute("aria-label", "Whitelist");
      toggle.setAttribute("aria-checked", String(enabled));
      toggle.addEventListener("click", async () => {
        if (enabled && !window.confirm("Disable whitelist?\n\nPlayers not on the whitelist will be allowed to join.")) return;
        toggle.disabled = true;
        try {
          const result = await requestWorkspaceJSON("/api/minecraft/workspace/whitelist/state", {
            method: "POST",
            headers: { "Content-Type": "application/x-www-form-urlencoded" },
            body: new URLSearchParams({ csrf, enabled: String(!enabled) }).toString(),
          });
          if (result) await loadCurrentTab(result.message || "Whitelist updated.");
        } catch (error) {
          const message = error?.message || "Whitelist change failed.";
          // Reload authoritative state after recovery; never optimistically flip.
          try { await loadCurrentTab(); } catch (_) { /* Keep the last authoritative state visible. */ }
          if (state) state.textContent = message;
        } finally {
          toggle.disabled = false;
        }
      });
      header.appendChild(toggle);
    }

    const changeWhitelist = async (params, submitter) => {
      if (submitter) submitter.disabled = true;
      if (state) state.textContent = "Updating whitelist…";
      try {
        params.set("csrf", csrf);
        const result = await requestWorkspaceJSON("/api/minecraft/workspace/whitelist", {
          method: "POST",
          headers: { "Content-Type": "application/x-www-form-urlencoded" },
          body: params.toString(),
        });
        if (!result) return;
        await loadCurrentTab(result.message || "Whitelist updated.");
      } catch (error) {
        if (state) state.textContent = error?.message || "Whitelist change could not be completed.";
      } finally {
        if (submitter) submitter.disabled = false;
      }
    };

    const list = section.querySelector("[data-minecraft-native-whitelist]");
    if (!payload.parsed) {
      list.appendChild(noticeNode("Whitelist information could not be interpreted. Backend output:", "warning"));
      const diagnostic = document.createElement("pre");
      diagnostic.className = "log-box";
      diagnostic.textContent = payload.output || "No whitelist information available.";
      list.appendChild(diagnostic);
    } else if (!payload.entries?.length) {
      list.appendChild(messageNode("No whitelisted players."));
    } else {
      const table = document.createElement("table");
      table.className = "minecraft-whitelist-table";
      table.innerHTML = '<thead><tr><th scope="col">Platform</th><th scope="col">Player</th><th scope="col">Status</th><th scope="col">Actions</th></tr></thead>';
      const body = document.createElement("tbody");
      const onlineIdentities = new Set(Array.isArray(players.names) ? players.names : []);
      payload.entries.forEach((entry) => {
        const row = document.createElement("tr");
        row.className = "minecraft-whitelist-row";
        row.dataset.playerIdentity = entry.identity;
        const remove = document.createElement("button");
        remove.type = "button";
        remove.className = "secondary";
        remove.textContent = "Remove";
        remove.setAttribute("aria-label", `Remove ${entry.display} (${entry.platform === "bedrock" ? "Bedrock" : "Java"}) from whitelist`);
        remove.addEventListener("click", () => changeWhitelist(new URLSearchParams({
          platform: entry.platform,
          action: "remove",
          name: entry.platform === "bedrock" ? entry.display : entry.identity,
        }), remove));
        [entry.platform === "bedrock" ? "Bedrock" : "Java", entry.display,
          onlineIdentities.has(entry.identity) ? "Online" : "Offline"].forEach((value) => {
          const cell = document.createElement("td");
          cell.textContent = value;
          row.appendChild(cell);
        });
        const actions = document.createElement("td");
        actions.appendChild(remove);
        row.appendChild(actions);
        body.appendChild(row);
      });
      table.appendChild(body);
      list.appendChild(table);
    }
    root.appendChild(section);
    content.replaceChildren(root);
    form.addEventListener("submit", (event) => {
      event.preventDefault();
      const params = new URLSearchParams();
      new FormData(form).forEach((value, key) => params.append(key, String(value)));
      params.set("action", "add");
      changeWhitelist(params, event.submitter);
    });
  };

  const renderLogs = async (sequence, message = "") => {
    const payload = await requestWorkspaceJSON("/api/minecraft/workspace/logs");
    if (!payload || sequence !== loadSequence || !content) return;
    const root = document.createElement("div");
    if (message) root.appendChild(noticeNode(message));
    const section = document.createElement("section");
    section.className = "panel details minecraft-workspace-section";
    const heading = document.createElement("div");
    heading.className = "section-heading";
    const headingText = document.createElement("div");
    const eyebrow = document.createElement("p");
    eyebrow.className = "eyebrow";
    eyebrow.textContent = "Diagnostics";
    const title = document.createElement("h2");
    title.textContent = "Recent Minecraft logs";
    headingText.append(eyebrow, title);
    heading.appendChild(headingText);
    const pre = document.createElement("pre");
    pre.className = "log-box";
    pre.textContent = Array.isArray(payload.lines) && payload.lines.length ? payload.lines.join("\n") : "No recent log lines available.";
    section.append(heading, pre);
    root.appendChild(section);
    content.replaceChildren(root);
  };

  async function loadCurrentTab(message = "") {
    const sequence = ++loadSequence;
    if (state) state.textContent = "Loading…";
    if (refreshButton) refreshButton.disabled = true;
    if (content) content.replaceChildren();
    try {
      if (settingsTabs.has(currentTab)) await renderSettingsTab(sequence, message);
      else if (currentTab === "players") await renderPlayers(sequence, message);
      else if (currentTab === "logs") await renderLogs(sequence, message);
      else await renderOverview(sequence, message);
      if (sequence === loadSequence && state?.textContent === "Loading…") state.textContent = "";
    } catch (error) {
      if (sequence !== loadSequence) return;
      if (content) content.replaceChildren();
      if (state) state.textContent = error?.message || "Minecraft workspace is unavailable.";
    } finally {
      if (sequence === loadSequence && refreshButton) refreshButton.disabled = false;
    }
  }

  const selectTab = (tab) => {
    currentTab = tab;
    tabs.forEach((button) => button.setAttribute("aria-selected", button.dataset.minecraftTab === tab ? "true" : "false"));
    loadCurrentTab();
  };

  const workspaceWindow = setupWorkspaceWindow(minecraftDialog, { onOpen: () => loadCurrentTab() });

  minecraftOpen.addEventListener("click", () => {
    if (controlCenter) controlCenter.open = false;
    workspaceWindow?.open();
  });
  closeButton?.addEventListener("click", () => workspaceWindow?.close());
  refreshButton?.addEventListener("click", () => loadCurrentTab());
  tabs.forEach((button) => button.addEventListener("click", () => selectTab(button.dataset.minecraftTab)));

  if (readWorkspaceWindowState("minecraft").open) workspaceWindow?.open();
}


const systemWorkspaceOpen = document.querySelector("[data-system-open]");
const systemWorkspaceDialog = document.querySelector("[data-system-workspace-dialog]");
if (systemWorkspaceOpen && systemWorkspaceDialog) {
  const closeButton = systemWorkspaceDialog.querySelector("[data-system-close]");
  const refreshButton = systemWorkspaceDialog.querySelector("[data-system-refresh]");
  const state = systemWorkspaceDialog.querySelector("[data-system-state]");
  const content = systemWorkspaceDialog.querySelector("[data-system-workspace-content]");
  const tabs = Array.from(systemWorkspaceDialog.querySelectorAll("[data-system-tab]"));
  const administratorTabs = new Set(["health", "users", "security", "logs", "date-time", "reset"]);
  const upsTabButton = systemWorkspaceDialog.querySelector("[data-system-ups-tab]");
  const upsCSRF = document.querySelector("[data-system-ups-csrf]");
  const systemWorkspaceCSRF = document.querySelector("[data-system-workspace-csrf]")?.value || "";
  let identity = null;
  let upsAvailable = false;
  let securityPane = "authentication";
  let currentTab = "health";
  let initialized = false;
  let loadSequence = 0;
  let resetPollTimer = 0;
  let activeFactoryResetOperationID = "";

  const showFactoryResetComplete = () => {
    window.JustVoxelLiveConsole.close();
    const secure = window.location.protocol === "https:" ? "; Secure" : "";
    document.cookie = "jv_factory_reset_complete=1; Path=/factory-reset-complete; Max-Age=120; SameSite=Strict" + secure;
    window.location.replace("/factory-reset-complete");
  };

  const resetLiveConsoleUnauthorized = (operation) => {
    // Preserve the existing factory-reset session invalidation transition.
    if (operation.operation_type === "factory_reset" && operation.operation_id === activeFactoryResetOperationID) {
      showFactoryResetComplete();
      return;
    }
    window.location.assign("/login");
  };

  const systemHandleAuth = (response) => {
    if (response.redirected) {
      const target = new URL(response.url);
      if (target.pathname === "/login" || target.pathname === "/password") {
        window.location.assign(target.pathname + target.search);
        return true;
      }
    }
    if (response.status === 401) {
      window.location.assign("/login");
      return true;
    }
    return false;
  };

  const systemFetchPage = async (url, options = {}) => {
    const response = await fetch(url, {
      method: options.method || "GET",
      credentials: "same-origin",
      headers: options.headers || { Accept: "text/html" },
      body: options.body,
      cache: "no-store",
      redirect: "follow",
    });
    if (systemHandleAuth(response)) return null;
    return response;
  };

  const systemNamespaceIDs = (root, prefix) => {
    const replacements = new Map();
    root.querySelectorAll("[id]").forEach((element) => {
      const oldID = element.id;
      const newID = prefix + oldID;
      replacements.set(oldID, newID);
      element.id = newID;
    });
    for (const attribute of ["for", "aria-controls", "aria-labelledby", "aria-describedby"]) {
      root.querySelectorAll(`[${attribute}]`).forEach((element) => {
        const tokens = String(element.getAttribute(attribute) || "").split(/\s+/).filter(Boolean);
        element.setAttribute(attribute, tokens.map((token) => replacements.get(token) || token).join(" "));
      });
    }
  };

  const systemReplaceText = (root, replacements) => {
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    const nodes = [];
    while (walker.nextNode()) nodes.push(walker.currentNode);
    nodes.forEach((node) => {
      let value = node.nodeValue || "";
      replacements.forEach(([before, after]) => {
        value = value.split(before).join(after);
      });
      node.nodeValue = value;
    });
  };

  const loadIdentity = async () => {
    if (identity) return identity;
    const response = await fetch("/api/session-info", {
      method: "GET",
      credentials: "same-origin",
      headers: { Accept: "application/json" },
      cache: "no-store",
    });
    if (systemHandleAuth(response)) return null;
    if (!response.ok) throw new Error("Session information is unavailable.");
    const result = await response.json();
    const role = String(result.role || "").toLowerCase();
    const username = String(result.username || "");
    if (!username || !["administrator", "operator", "viewer"].includes(role)) {
      throw new Error("Session role is unavailable.");
    }
    identity = { username, role };
    document.body.classList.remove("role-pending");
    document.body.classList.add(`role-${role}`);
    return identity;
  };

  const syncSystemTabs = () => {
    tabs.forEach((button) => {
      button.setAttribute("aria-selected", button.dataset.systemTab === currentTab ? "true" : "false");
    });
  };

  const systemFetchJSON = async (url, options = {}, factoryResetPollOperationID = "") => {
    const response = await fetch(url, {
      credentials: "same-origin",
      cache: "no-store",
      ...options,
      headers: {
        Accept: "application/json",
        ...(options.headers || {}),
      },
    });
    if (response.status === 401) {
      if (factoryResetPollOperationID && factoryResetPollOperationID === activeFactoryResetOperationID &&
          url === "/api/system/workspace/reset/operations/" + factoryResetPollOperationID) {
        showFactoryResetComplete();
        return null;
      }
      window.location.assign("/login");
      return null;
    }
    let payload = {};
    try {
      payload = await response.json();
    } catch (_) {
      payload = {};
    }
    if (response.status === 403 && String(payload.error || "").toLowerCase().includes("password change")) {
      window.location.assign("/password");
      return null;
    }
    if (!response.ok) {
      throw new Error(payload.error || "System operation could not be completed.");
    }
    return payload;
  };

  const systemNotice = (message, kind = "success") => {
    const node = document.createElement("div");
    node.className = "notice " + kind;
    node.textContent = message;
    return node;
  };

  const systemPanel = (eyebrowText, titleText) => {
    const panel = document.createElement("section");
    panel.className = "panel details";
    const heading = document.createElement("div");
    heading.className = "section-heading";
    const wrap = document.createElement("div");
    const eyebrow = document.createElement("p");
    eyebrow.className = "eyebrow";
    eyebrow.textContent = eyebrowText;
    const title = document.createElement("h2");
    title.textContent = titleText;
    wrap.append(eyebrow, title);
    heading.appendChild(wrap);
    panel.appendChild(heading);
    return { panel, heading };
  };

  const renderHealth = async (sequence) => {
    const payload = await systemFetchJSON("/api/system/workspace/health");
    if (!payload || sequence !== loadSequence || !content) return;
    const root = document.createElement("div");
    root.className = "system-health-view";
    const result = payload.result || {};
    root.appendChild(systemNotice(
      result.ok ? "System health check passed." : "System health check found problems.",
      result.ok ? "success" : "error"
    ));
    const built = systemPanel("System health", "Health check details");
    const pre = document.createElement("pre");
    pre.className = "log-box";
    pre.textContent = result.output || "No detailed health output was returned.";
    built.panel.appendChild(pre);
    root.appendChild(built.panel);
    content.replaceChildren(root);
  };

  const systemSplitter = (label, move) => {
    const handle = document.createElement("div");
    handle.className = "system-logs-splitter";
    handle.setAttribute("role", "separator");
    handle.setAttribute("aria-label", label);
    handle.setAttribute("aria-orientation", "vertical");
    handle.tabIndex = 0;
    handle.addEventListener("pointerdown", (event) => {
      if (event.button !== 0) return;
      event.preventDefault();
      handle.setPointerCapture(event.pointerId);
      let previous = event.clientX;
      const onMove = (next) => { const delta = next.clientX - previous; previous = next.clientX; move(delta); };
      const onEnd = () => { handle.removeEventListener("pointermove", onMove); handle.removeEventListener("pointerup", onEnd); handle.removeEventListener("pointercancel", onEnd); };
      handle.addEventListener("pointermove", onMove);
      handle.addEventListener("pointerup", onEnd);
      handle.addEventListener("pointercancel", onEnd);
    });
    handle.addEventListener("keydown", (event) => {
      if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
        event.preventDefault();
        move(event.key === "ArrowLeft" ? -16 : 16);
      }
    });
    return handle;
  };

  const renderHistory = async (sequence, message = "") => {
    const payload = await systemFetchJSON("/api/system/workspace/history");
    if (!payload || sequence !== loadSequence || !content) return;
    const root = document.createElement("div");
    root.className = "system-history-view";
    if (message) root.appendChild(systemNotice(message));

    if (payload.role === "administrator") {
      const notificationsPanel = systemPanel("Action required", "Open notifications").panel;
      const notifications = Array.isArray(payload.notifications) ? payload.notifications : [];
      if (!notifications.length) {
        const empty = document.createElement("p");
        empty.className = "muted";
        empty.textContent = "Nothing currently requires Administrator action.";
        notificationsPanel.appendChild(empty);
      } else {
        notifications.forEach((item) => {
          const row = document.createElement("div");
          row.className = "event-row";
          const info = document.createElement("div");
          const title = document.createElement("strong");
          title.textContent = item.title || "Notification";
          const messageNode = document.createElement("span");
          messageNode.textContent = item.message || "";
          const date = document.createElement("span");
          date.textContent = item.created_at || "";
          info.append(title, messageNode, date);
          const button = document.createElement("button");
          button.type = "button";
          button.className = "secondary";
          button.textContent = "Resolve";
          button.addEventListener("click", async () => {
            button.disabled = true;
            try {
              const body = new URLSearchParams({ csrf: systemWorkspaceCSRF });
              const result = await systemFetchJSON("/api/system/workspace/history/notifications/" + item.id + "/resolve", {
                method: "POST",
                headers: { "Content-Type": "application/x-www-form-urlencoded" },
                body: body.toString(),
              });
              if (result) await renderHistory(++loadSequence, result.message || "Notification resolved.");
            } catch (error) {
              if (state) state.textContent = error?.message || "Notification could not be resolved.";
              button.disabled = false;
            }
          });
          row.append(info, button);
          notificationsPanel.appendChild(row);
        });
      }
      root.appendChild(notificationsPanel);

      const auditPanel = systemPanel("Administrator history", "Detailed history").panel;
      const events = Array.isArray(payload.audit) ? payload.audit : [];
      if (!events.length) {
        const empty = document.createElement("p");
        empty.className = "muted";
        empty.textContent = "No audit events yet.";
        auditPanel.appendChild(empty);
      } else {
        const list = document.createElement("div");
        list.className = "event-list";
        events.forEach((event) => {
          const row = document.createElement("div");
          row.className = "event-row";
          const info = document.createElement("div");
          const action = document.createElement("strong");
          action.textContent = event.action || "Action";
          const meta = document.createElement("span");
          meta.textContent = [event.occurred_at, event.actor_username, event.actor_role].filter(Boolean).join(" · ");
          info.append(action, meta);
          if (event.target) {
            const target = document.createElement("span");
            target.textContent = "Target: " + event.target;
            info.appendChild(target);
          }
          if (event.context) {
            const context = document.createElement("span");
            context.textContent = event.context;
            info.appendChild(context);
          }
          const badge = document.createElement("span");
          badge.className = "badge";
          badge.textContent = event.success ? "Success" : "Failed";
          row.append(info, badge);
          list.appendChild(row);
        });
        auditPanel.appendChild(list);
      }
      let notificationWidth = 300;
      const divider = systemSplitter("Resize Open notifications and Detailed history", (delta) => {
        notificationWidth += delta;
        setHistoryWidth();
      });
      const setHistoryWidth = () => {
        notificationWidth = Math.max(240, Math.min(notificationWidth, (root.clientWidth || 800) - 270));
        root.style.setProperty("--history-notifications-width", notificationWidth + "px");
        divider.setAttribute("aria-valuemin", "240");
        divider.setAttribute("aria-valuemax", String(Math.max(240, root.clientWidth - 270)));
        divider.setAttribute("aria-valuenow", String(Math.round(notificationWidth)));
      };
      root.append(divider, auditPanel);
      const historyResizeObserver = new ResizeObserver(() => {
        if (!root.isConnected) { historyResizeObserver.disconnect(); return; }
        setHistoryWidth();
      });
      historyResizeObserver.observe(root);
    } else {
      const activityPanel = systemPanel("Activity", "Recent appliance actions").panel;
      const events = Array.isArray(payload.events) ? payload.events : [];
      if (!events.length) {
        const empty = document.createElement("p");
        empty.className = "muted";
        empty.textContent = "No recorded activity yet.";
        activityPanel.appendChild(empty);
      } else {
        const list = document.createElement("div");
        list.className = "event-list";
        events.forEach((event) => {
          const row = document.createElement("div");
          row.className = "event-row";
          const info = document.createElement("div");
          const action = document.createElement("strong");
          action.textContent = event.action || "Action";
          const date = document.createElement("span");
          date.textContent = event.occurred_at || "";
          info.append(action, date);
          const badge = document.createElement("span");
          badge.className = "badge";
          badge.textContent = event.success ? "Success" : "Failed";
          row.append(info, badge);
          list.appendChild(row);
        });
        activityPanel.appendChild(list);
      }
      root.appendChild(activityPanel);
    }

    content.replaceChildren(root);
  };

  const systemPostForm = async (url, fields = {}) => {
    const body = new URLSearchParams({ csrf: systemWorkspaceCSRF });
    Object.entries(fields).forEach(([key, value]) => body.set(key, String(value ?? "")));
    return systemFetchJSON(url, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: body.toString(),
    });
  };

  const systemLogDestination = new URLSearchParams(window.location.search);
  let requestedMigrationLog = systemLogDestination.get("workspace") === "system" && systemLogDestination.get("tab") === "logs" && ["migration", "plugins"].includes(systemLogDestination.get("log_group"))
    ? { group: systemLogDestination.get("log_group") === "plugins" ? "Plugins" : "Migration", id: systemLogDestination.get("log_id") || "" } : null;

  let failedUnitsSelected = false;

  const renderLogs = async (sequence) => {
    const payload = await systemFetchJSON("/api/system/workspace/logs");
    if (!payload || sequence !== loadSequence || !content) return;
    const groups = ["Setup", "Migration", "Restore", "Plugins", "Reset", "Support", "Failed units"];
    const logs = Array.isArray(payload.logs) ? payload.logs : [];
    const failedUnits = Array.isArray(payload.failed_units) ? payload.failed_units : [];
    const root = document.createElement("div");
    root.className = "system-logs-layout";
    root.dataset.systemLogsLayout = "";
    const saved = (() => { try { return JSON.parse(localStorage.getItem("justvoxel.system.logs.columns") || "{}") || {}; } catch { return {}; } })();
    let fileWidth = Number.isFinite(saved.files) ? saved.files : 280;
    const setWidths = () => {
      const width = root.clientWidth || 900;
      fileWidth = Math.max(160, Math.min(fileWidth, width - 220 - 10));
      root.style.setProperty("--logs-files-width", fileWidth + "px");
      handle.setAttribute("aria-valuemin", "160");
      handle.setAttribute("aria-valuemax", String(Math.max(160, width - 230)));
      handle.setAttribute("aria-valuenow", String(Math.round(fileWidth)));
    };
    const makeColumn = (name, className) => {
      const column = document.createElement("section");
      column.className = className;
      const heading = document.createElement("h3");
      heading.textContent = name;
      column.appendChild(heading);
      return column;
    };
    const groupColumn = document.createElement("nav");
    groupColumn.className = "system-logs-groups";
    groupColumn.setAttribute("aria-label", "Log categories");
    const fileColumn = makeColumn("Files", "system-logs-files");
    const fileList = document.createElement("div");
    fileList.className = "system-logs-list";
    fileColumn.appendChild(fileList);
    const reader = makeColumn("Viewer", "system-logs-reader");
    const viewer = document.createElement("div");
    viewer.className = "system-logs-viewer";
    const download = document.createElement("a");
    download.className = "button-link secondary";
    download.textContent = "Download log";
    download.hidden = true;
    const documentText = document.createElement("pre");
    documentText.textContent = "Select a file to read it here.";
    viewer.append(download, documentText);
    reader.appendChild(viewer);
    let selectedGroup = requestedMigrationLog?.group || (failedUnitsSelected ? "Failed units" : "Setup");
    let selectedFile = "";
    let requestedEntry = requestedMigrationLog ? logs.find((entry) => entry.category === requestedMigrationLog.group.toLowerCase() && entry.log_id === requestedMigrationLog.id) : null;
    requestedMigrationLog = null;
    const renderFiles = () => {
      fileList.replaceChildren();
      viewer.scrollTop = 0;
      download.hidden = true;
      const live = selectedGroup === "Failed units";
      fileColumn.children[0].textContent = live ? "Units" : "Files";
      documentText.textContent = live ? "Select a unit to read its last 60 current-boot journal lines here." : "Select a file to read it here.";
      const entries = live ? failedUnits : logs.filter((entry) => entry.category === selectedGroup.toLowerCase());
      if (!entries.length) {
        const empty = document.createElement("p");
        empty.className = "muted";
        empty.textContent = live ? (payload.failed_units_error ? "Failed systemd units are unavailable." : "No failed systemd units.") : selectedGroup === "Support" ? "No support bundles generated." : "No " + selectedGroup.toLowerCase() + " logs available.";
        fileList.appendChild(empty);
        return;
      }
      entries.forEach((entry) => {
        const identity = live ? entry.unit : entry.log_id;
        const button = document.createElement("button");
        button.type = "button";
        button.className = "system-log-entry secondary";
        button.setAttribute("aria-pressed", selectedFile === identity ? "true" : "false");
        const title = document.createElement("strong");
        title.textContent = live ? entry.unit : entry.log_id + ".log";
        const meta = document.createElement("span");
        meta.textContent = live ? [entry.description, entry.active_state, entry.sub_state].filter(Boolean).join(" · ") : [entry.operation_type?.replaceAll("_", " ") || "", entry.state || "", entry.timestamp || ""].filter(Boolean).join(" · ");
        button.append(title, meta);
        button.addEventListener("click", async () => {
          selectedFile = identity;
          const clickedGroup = selectedGroup;
          fileList.querySelectorAll("button").forEach((item) => item.setAttribute("aria-pressed", item === button ? "true" : "false"));
          const url = live ? "/api/system/workspace/logs/failed/" + encodeURIComponent(entry.unit) : "/api/system/workspace/logs/" + encodeURIComponent(entry.category) + "/" + encodeURIComponent(entry.log_id);
          documentText.textContent = "Loading log…";
          download.hidden = true;
          try {
            const response = await fetch(url, { credentials: "same-origin", cache: "no-store", headers: { Accept: "text/plain" } });
            if (systemHandleAuth(response)) return;
            if (!response.ok) throw new Error(live ? "Failed unit journal is unavailable." : "Diagnostic log is unavailable.");
            const data = await response.text();
            if (sequence !== loadSequence || selectedFile !== identity || selectedGroup !== clickedGroup) return;
            documentText.textContent = data;
            download.href = url + "?download=1";
            download.hidden = false;
          } catch (error) {
            if (sequence !== loadSequence || selectedFile !== identity || selectedGroup !== clickedGroup) return;
            documentText.textContent = live ? "Failed unit journal is unavailable." : error?.message || "Diagnostic log is unavailable.";
          }
        });
        fileList.appendChild(button);
        if (entry === requestedEntry) {
          requestedEntry = null;
          button.click();
        }
      });
    };
    groups.forEach((group) => {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "system-log-group secondary";
      button.textContent = group;
      button.setAttribute("aria-pressed", group === selectedGroup ? "true" : "false");
      button.addEventListener("click", () => {
        selectedGroup = group;
        failedUnitsSelected = group === "Failed units";
        selectedFile = "";
        groupColumn.querySelectorAll("button").forEach((item) => item.setAttribute("aria-pressed", item === button ? "true" : "false"));
        renderFiles();
      });
      groupColumn.appendChild(button);
    });
    const handle = systemSplitter("Resize Files and Viewer", (delta) => {
      fileWidth += delta;
      setWidths();
      try { localStorage.setItem("justvoxel.system.logs.columns", JSON.stringify({ files: fileWidth })); } catch {}
    });
    handle.dataset.logsSplitter = "files";
    root.append(groupColumn, fileColumn, handle, reader);
    content.replaceChildren(root);
    setWidths();
    const logsResizeObserver = new ResizeObserver(() => {
      if (!root.isConnected) { logsResizeObserver.disconnect(); return; }
      setWidths();
    });
    logsResizeObserver.observe(root);
    renderFiles();
  };

  const renderUsers = async (sequence, message = "") => {
    if (!document.querySelector('link[href="/static/storage-browser.css"]')) {
      const stylesheet = document.createElement("link");
      stylesheet.rel = "stylesheet";
      stylesheet.href = "/static/storage-browser.css";
      document.head.appendChild(stylesheet);
    }
    const payload = await systemFetchJSON("/api/system/workspace/users");
    if (!payload || sequence !== loadSequence || !content) return;
    const root = document.createElement("div");
    root.className = "system-users-view";
    if (message) root.appendChild(systemNotice(message));

    const primary = systemPanel("Primary administrator", payload.primary_administrator || "voxel").panel;
    const primaryText = document.createElement("p");
    primaryText.className = "muted compact";
    primaryText.textContent = "Permanent system administrator. This account cannot be deleted, disabled, or demoted here.";
    primary.appendChild(primaryText);
    root.appendChild(primary);

    const create = systemPanel("New WebUI account", "Create user").panel;
    const createForm = document.createElement("form");
    createForm.className = "admin-form-grid";
    createForm.innerHTML =
      '<label>Username<input name="username" required maxlength="64" autocomplete="off"></label>' +
      '<label>Role<select name="role" required><option value="operator">Operator</option><option value="viewer">Viewer</option></select></label>' +
      '<label>Password<input type="password" name="password" required autocomplete="new-password"></label>' +
      '<label>Confirm password<input type="password" name="confirm_password" required autocomplete="new-password"></label>' +
      '<div class="form-actions"><button type="submit">Create user</button></div>';
    createForm.querySelectorAll('input[type="password"]').forEach((input) => {
      input.minLength = Number(payload.minimum_password_len || 8);
    });
    createForm.addEventListener("submit", async (event) => {
      event.preventDefault();
      const submit = event.submitter;
      if (submit) submit.disabled = true;
      const data = Object.fromEntries(new FormData(createForm).entries());
      try {
        const result = await systemPostForm("/api/system/workspace/users/create", data);
        if (result) await renderUsers(++loadSequence, result.message || "WebUI user created.");
      } catch (error) {
        if (state) state.textContent = error?.message || "Could not create WebUI user.";
        if (submit) submit.disabled = false;
      }
    });
    create.appendChild(createForm);
    root.appendChild(create);

    const listPanel = systemPanel("WebUI identities", "Operators and Viewers").panel;
    const users = Array.isArray(payload.users) ? payload.users : [];
    if (!users.length) {
      const empty = document.createElement("p");
      empty.className = "muted compact";
      empty.textContent = "No Operator or Viewer accounts exist yet.";
      listPanel.appendChild(empty);
    } else {
      const list = document.createElement("div");
      list.className = "user-list";
      const table = document.createElement("table");
      table.className = "system-users-table";
      const head = document.createElement("thead");
      head.innerHTML = "<tr><th scope=\"col\">Username</th><th scope=\"col\">Role</th><th scope=\"col\">Status</th><th scope=\"col\">Restart</th><th scope=\"col\">Backup</th><th scope=\"col\">Actions</th></tr>";
      const rows = document.createElement("tbody");
      table.append(head, rows);
      list.appendChild(table);
      users.forEach((user, index) => {
        const row = document.createElement("tr");
        for (const value of [user.username || "User", user.role === "operator" ? "Operator" : "Viewer", user.enabled ? "Enabled" : "Disabled",
          `${user.restart_used ?? 0} / ${user.restart_limit ?? 0}`, `${user.backup_used ?? 0} / ${user.backup_limit ?? 0}`]) {
          const cell = document.createElement("td");
          cell.textContent = value;
          row.appendChild(cell);
        }
        const cell = document.createElement("td");
        const menu = document.createElement("details");
        menu.className = "storage-action-menu system-user-menu";
        const trigger = document.createElement("summary");
        trigger.textContent = "Manage";
        trigger.setAttribute("aria-label", "Manage " + user.username);
        const choices = document.createElement("div");
        choices.className = "storage-action-menu-popover";
        // Use the browser top layer so the compact scrolling table cannot clip the menu.
        choices.setAttribute("popover", "auto");
        menu.addEventListener("toggle", () => {
          if (!menu.isConnected) return;
          if (menu.open) {
            const rect = trigger.getBoundingClientRect();
            choices.style.right = Math.max(8, window.innerWidth - rect.right) + "px";
            choices.style.top = Math.max(8, Math.min(rect.bottom + 4, window.innerHeight - 300)) + "px";
            choices.showPopover();
          } else if (choices.matches(":popover-open")) choices.hidePopover();
        });
        choices.addEventListener("toggle", (event) => {
          if (event.newState === "closed") {
            menu.open = false;
            if (choices.contains(document.activeElement)) trigger.focus();
          }
        });
        const openAction = (title, fields, action, submitText, dangerous = false) => {
          menu.open = false;
          if (choices.matches(":popover-open")) choices.hidePopover();
          const dialog = document.createElement("dialog");
          dialog.className = "system-action-dialog system-user-dialog";
          const wrap = document.createElement("div");
          wrap.className = "system-action-dialog-content";
          const heading = document.createElement("h2");
          heading.id = "system-user-action-" + index;
          heading.textContent = title + " · " + user.username;
          dialog.setAttribute("aria-labelledby", heading.id);
          const form = document.createElement("form");
          form.innerHTML = fields;
          form.querySelectorAll('input[type="password"]').forEach((input) => { input.minLength = Number(payload.minimum_password_len || 8); });
          const error = document.createElement("p");
          error.className = "notice error";
          error.hidden = true;
          error.setAttribute("role", "alert");
          const actions = document.createElement("div");
          actions.className = "action-row";
          const cancel = document.createElement("button");
          cancel.type = "button";
          cancel.className = "secondary";
          cancel.textContent = "Cancel";
          cancel.addEventListener("click", () => dialog.close());
          const submit = document.createElement("button");
          submit.type = "submit";
          submit.textContent = submitText;
          if (dangerous) {
            submit.className = "danger";
            submit.dataset.destructiveSubmit = "";
            submit.disabled = true;
            const confirmation = document.createElement("div");
            confirmation.className = "destructive-confirmation";
            confirmation.dataset.destructiveConfirmation = "";
            confirmation.innerHTML = '<div class="destructive-confirm-slider" data-destructive-slider-shell><span class="destructive-confirm-slider-text" data-destructive-slider-text>Slide to confirm</span><span class="destructive-confirm-slider-thumb" aria-hidden="true">&gt;</span><input type="range" min="0" max="100" step="1" value="0" data-destructive-slider aria-label="Slide to confirm account action"></div><label class="destructive-confirm-toggle-row" data-destructive-toggle-row hidden><span class="destructive-confirm-toggle-callout">Confirm &gt;</span><input class="destructive-confirm-toggle" type="checkbox" data-destructive-toggle aria-label="Confirm account action"></label>';
            form.appendChild(confirmation);
          }
          actions.append(cancel, submit);
          form.append(error, actions);
          form.addEventListener("submit", async (event) => {
            event.preventDefault();
            if (submit.disabled || (dangerous && (!form.querySelector("[data-destructive-toggle]").checked || Number(form.querySelector("[data-destructive-slider]").value) < 100))) return;
            submit.disabled = true;
            cancel.disabled = true;
            const preventClose = (event) => event.preventDefault();
            dialog.addEventListener("cancel", preventClose);
            try {
              const fields = Object.fromEntries(new FormData(form).entries());
              const result = await systemPostForm("/api/system/workspace/users/" + user.id + "/" + action, fields);
              if (result) {
                dialog.close();
                await renderUsers(++loadSequence, result.message || "WebUI user updated.");
              }
            } catch (failure) {
              error.textContent = failure?.message || "Could not update WebUI user.";
              error.hidden = false;
            } finally {
              cancel.disabled = false;
              dialog.removeEventListener("cancel", preventClose);
              if (dangerous) form.querySelector("[data-destructive-confirmation]")._justVoxelDestructiveSync();
              else submit.disabled = false;
            }
          });
          wrap.append(heading, form);
          dialog.appendChild(wrap);
          document.body.appendChild(dialog);
          initializeSharedWebUIControls(dialog);
          dialog.addEventListener("close", () => { dialog.remove(); trigger.focus(); }, { once: true });
          dialog.showModal();
        };
        const addAction = (title, fields, action, submitText, dangerous = false, disabled = false) => {
          const button = document.createElement("button");
          button.type = "button";
          button.textContent = title;
          button.disabled = disabled;
          if (dangerous) button.className = "storage-action-danger";
          button.addEventListener("click", () => openAction(title, fields, action, submitText, dangerous));
          choices.appendChild(button);
        };
        addAction("Change role", '<label>Role<select name="role"><option value="operator"' + (user.role === "operator" ? ' selected' : '') + '>Operator</option><option value="viewer"' + (user.role !== "operator" ? ' selected' : '') + '>Viewer</option></select></label>', "role", "Save role");
        addAction("Reset restart allowance", '<p>Reset the used restart allowance for this account.</p>', "restart-allowance/reset", "Reset allowance", false, Number(user.restart_used || 0) === 0);
        addAction("Reset backup allowance", '<p>Reset the used backup allowance for this account.</p>', "backup-allowance/reset", "Reset allowance", false, Number(user.backup_used || 0) === 0);
        addAction(user.enabled ? "Disable account" : "Enable account", '<input type="hidden" name="enabled" value="' + String(!user.enabled) + '"><p>' + (user.enabled ? 'This account will no longer be able to sign in.' : 'Allow this account to sign in again.') + '</p>', "enabled", user.enabled ? "Disable account" : "Enable account", Boolean(user.enabled));
        addAction("Change password", '<label>New password<input type="password" name="password" required autocomplete="new-password"></label><label>Confirm password<input type="password" name="confirm_password" required autocomplete="new-password"></label>', "password", "Change password");
        addAction("Delete account", '<p>Deleting this WebUI identity also removes its saved Operator quota history.</p>', "delete", "Delete account", true);
        menu.append(trigger, choices);
        cell.appendChild(menu);
        row.appendChild(cell);
        rows.appendChild(row);
      });
      listPanel.appendChild(list);
    }
    root.appendChild(listPanel);
    content.replaceChildren(root);
  };

  const renderSecurity = async (sequence, message = "") => {
    const payload = await systemFetchJSON("/api/system/workspace/security");
    if (!payload || sequence !== loadSequence || !content) return;
    const root = document.createElement("div");
    root.className = "system-security-view";
    if (message) root.appendChild(systemNotice(message));

    const switcher = document.createElement("div");
    switcher.className = "system-security-switcher";
    switcher.setAttribute("role", "tablist");
    ["authentication", "password"].forEach((kind) => {
      const button = document.createElement("button");
      button.type = "button";
      button.dataset.systemSecurityChoice = kind;
      button.textContent = kind === "authentication" ? "Authentication" : "Password";
      button.setAttribute("aria-selected", securityPane === kind ? "true" : "false");
      switcher.appendChild(button);
    });
    const securityHeading = document.createElement("div");
    securityHeading.className = "system-security-heading";
    const securityLabel = document.createElement("p");
    securityLabel.className = "eyebrow";
    securityLabel.textContent = "Account security";
    const securityTitle = document.createElement("h2");
    securityTitle.textContent = "Security";
    securityHeading.append(securityLabel, securityTitle);
    root.append(securityHeading, switcher);

    const authPane = document.createElement("section");
    authPane.className = "system-security-pane";
    authPane.dataset.systemSecurityPane = "authentication";
    const authHeading = document.createElement("div");
    authHeading.className = "system-security-pane-heading";
    const authTitle = document.createElement("h3");
    authTitle.textContent = "Authentication";
    const authDescription = document.createElement("p");
    authDescription.className = "muted compact";
    authDescription.textContent = "Choose whether WebUI uses the voxel system password or its own password.";
    const authLabel = document.createElement("p");
    authLabel.className = "eyebrow";
    authLabel.textContent = "Sign-in method";
    authHeading.append(authLabel, authTitle, authDescription);
    authPane.appendChild(authHeading);

    const authStatus = document.createElement("div");
    authStatus.className = "system-security-status";
    const admin = document.createElement("span");
    admin.textContent = "Administrator: " + (payload.username || "voxel");
    const mode = document.createElement("span");
    mode.textContent = "Mode: " + (payload.mode === "system" ? "System account" : "Separate WebUI password");
    authStatus.append(admin, mode);
    authPane.appendChild(authStatus);

    const authForm = document.createElement("form");
    authForm.className = "system-security-form";
    if (payload.mode === "system") {
      authForm.innerHTML =
        '<input type="hidden" name="mode" value="separate">' +
        '<label>Current system password<input type="password" name="system_password" autocomplete="current-password" required></label>' +
        '<label>New WebUI password<input type="password" name="new_web_password" autocomplete="new-password" required></label>' +
        '<label>Confirm WebUI password<input type="password" name="confirm_web_password" autocomplete="new-password" required></label>' +
        '<p class="muted compact">The new browser password will be separate from Linux, SSH, and the local console.</p>' +
        '<button type="submit">Switch to Separate WebUI password</button>';
      authForm.querySelectorAll('input[name="new_web_password"],input[name="confirm_web_password"]').forEach((input) => {
        input.minLength = Number(payload.minimum_password_len || 8);
      });
    } else {
      authForm.innerHTML =
        '<input type="hidden" name="mode" value="system">' +
        '<label>System password<input type="password" name="system_password" autocomplete="current-password" required></label>' +
        '<p class="muted compact">After switching, the real voxel system password will sign in to WebUI.</p>' +
        '<button type="submit">Switch to System account</button>';
    }
    authForm.addEventListener("submit", async (event) => {
      event.preventDefault();
      const submit = event.submitter;
      if (submit) submit.disabled = true;
      try {
        const data = Object.fromEntries(new FormData(authForm).entries());
        const result = await systemPostForm("/api/system/workspace/security/authentication", data);
        if (!result) return;
        if (result.reauthenticate) {
          window.location.assign("/login?message=auth-mode-changed");
          return;
        }
        await renderSecurity(++loadSequence, result.message || "Authentication mode changed.");
      } catch (error) {
        if (state) state.textContent = error?.message || "Authentication mode change was rejected.";
        if (submit) submit.disabled = false;
      }
    });
    authPane.appendChild(authForm);
    root.appendChild(authPane);

    const passwordPane = document.createElement("section");
    passwordPane.className = "system-security-pane";
    passwordPane.dataset.systemSecurityPane = "password";
    const passwordHeading = document.createElement("div");
    passwordHeading.className = "system-security-pane-heading";
    const passwordTitle = document.createElement("h3");
    passwordTitle.textContent = "Password";
    const passwordDescription = document.createElement("p");
    passwordDescription.className = "muted compact";
    passwordDescription.textContent = "Change the password used by the current WebUI authentication mode.";
    const passwordLabel = document.createElement("p");
    passwordLabel.className = "eyebrow";
    passwordLabel.textContent = "Account password";
    passwordHeading.append(passwordLabel, passwordTitle, passwordDescription);
    passwordPane.appendChild(passwordHeading);

    const passwordForm = document.createElement("form");
    passwordForm.className = "system-security-form";
    passwordForm.innerHTML =
      '<label>Current password<input type="password" name="current_password" autocomplete="current-password" required></label>' +
      '<label>New password<input type="password" name="new_password" autocomplete="new-password" required></label>' +
      '<label>Confirm new password<input type="password" name="confirm_password" autocomplete="new-password" required></label>' +
      '<p class="muted compact">Use at least ' + String(payload.minimum_password_len || 8) + ' characters and follow the host password policy.</p>' +
      '<button type="submit">Change password</button>';
    passwordForm.querySelectorAll('input[name="new_password"],input[name="confirm_password"]').forEach((input) => {
      input.minLength = Number(payload.minimum_password_len || 8);
    });
    passwordForm.addEventListener("submit", async (event) => {
      event.preventDefault();
      const submit = event.submitter;
      if (submit) submit.disabled = true;
      try {
        const data = Object.fromEntries(new FormData(passwordForm).entries());
        const result = await systemPostForm("/api/system/workspace/security/password", data);
        if (!result) return;
        if (result.reauthenticate) {
          window.location.assign("/login?message=password-changed");
          return;
        }
      } catch (error) {
        if (state) state.textContent = error?.message || "Password change was rejected.";
        if (submit) submit.disabled = false;
      }
    });
    passwordPane.appendChild(passwordForm);
    root.appendChild(passwordPane);

    const showPane = (kind) => {
      securityPane = kind;
      root.querySelectorAll("[data-system-security-choice]").forEach((button) => {
        button.setAttribute("aria-selected", button.dataset.systemSecurityChoice === kind ? "true" : "false");
      });
      root.querySelectorAll("[data-system-security-pane]").forEach((pane) => {
        pane.hidden = pane.dataset.systemSecurityPane !== kind;
      });
    };
    root.querySelectorAll("[data-system-security-choice]").forEach((button) => {
      button.addEventListener("click", () => showPane(button.dataset.systemSecurityChoice));
    });
    content.replaceChildren(root);
    showPane(securityPane);
  };

  const clearResetPoll = () => {
    if (resetPollTimer) window.clearTimeout(resetPollTimer);
    resetPollTimer = 0;
  };

  const systemResetList = (title, items, kind = "") => {
    const box = document.createElement("section");
    box.className = "system-reset-impact " + kind;
    const heading = document.createElement("h4");
    heading.textContent = title;
    const list = document.createElement("ul");
    items.forEach((item) => {
      const row = document.createElement("li");
      row.textContent = item;
      list.appendChild(row);
    });
    box.append(heading, list);
    return box;
  };

  const systemResetOperationLabel = (operation) => {
    if (!operation) return "Working";
    if (operation.state === "queued") return "Queued";
    if (operation.state === "validating") return "Validating";
    if (operation.state === "running") return "Resetting";
    if (operation.state === "verifying") return "Verifying";
    if (operation.state === "succeeded") return "Complete";
    if (operation.state === "needs_attention") return "Needs attention";
    if (operation.state === "resolved") return "Resolved";
    if (operation.state === "rolled_back") return "Rolled back";
    return "Working";
  };

  const renderResetOperation = (operation) => {
    clearResetPoll();
    if (!content || !operation) return;
    window.JustVoxelLiveConsole.updateState(operation.operation_id, operation.state, operation.stage);
    if (operation.operation_type === "factory_reset" && operation.state === "succeeded") {
      showFactoryResetComplete();
      return;
    }
    activeFactoryResetOperationID = operation.operation_type === "factory_reset" &&
      !["needs_attention", "resolved", "rolled_back"].includes(operation.state)
      ? operation.operation_id : "";
    if (operation.operation_type === "minecraft_reset" && operation.state === "succeeded") {
      ++loadSequence;
      window.JustVoxelLiveConsole.close();
      workspaceWindow?.close();
      window.dispatchEvent(new Event("justvoxel:minecraft-reset-complete"));
      return;
    }
    const root = document.createElement("div");
    root.className = "system-reset-view";

    const built = systemPanel(
      operation.operation_type === "factory_reset" ? "Full Factory Reset" : "Reset Minecraft",
      systemResetOperationLabel(operation)
    );
    built.panel.classList.add("system-reset-progress");
    const badge = document.createElement("span");
    badge.className = "badge";
    badge.textContent = systemResetOperationLabel(operation);
    built.heading.appendChild(badge);

    const status = document.createElement("p");
    status.className = "system-reset-progress-status";
    status.textContent = operation.status || "Reset operation is running.";
    const stage = document.createElement("p");
    stage.className = "muted compact";
    stage.textContent = "Stage: " + String(operation.stage || "working").replaceAll("_", " ");
    built.panel.append(status, stage);
    const liveConsole = document.createElement("button");
    liveConsole.type = "button";
    liveConsole.className = "secondary";
    liveConsole.textContent = "Live Console";
    liveConsole.addEventListener("click", () => window.JustVoxelLiveConsole.open({
      kind: operation.operation_type,
      operationID: operation.operation_id,
      state: operation.state, stage: operation.stage,
      onUnauthorized: () => resetLiveConsoleUnauthorized(operation),
    }));
    built.panel.appendChild(liveConsole);

    if (operation.operation_type === "factory_reset") {
      const note = document.createElement("div");
      note.className = "notice warning";
      note.textContent = "When Full Factory Reset completes, this WebUI session will be signed out. Sign in with voxel / voxel, then choose a new administrator password before normal administration is available.";
      built.panel.appendChild(note);
      if (operation.state === "needs_attention") {
        const recovery = document.createElement("div");
        recovery.className = "notice warning";
        recovery.innerHTML = "<strong>Factory Reset stopped before completion.</strong><br>You can keep the server exactly as it is now and release the blocked Restore/Migration operations, or review and retry the destructive reset.";
        built.panel.appendChild(recovery);

        const actions = document.createElement("div");
        actions.className = "system-reset-actions";
        const keep = document.createElement("button");
        keep.type = "button";
        keep.className = "secondary";
        keep.textContent = "Keep current server";
        keep.addEventListener("click", async () => {
          if (!window.confirm("Keep the current server state and release the failed Factory Reset lock? This does not delete data or retry the reset.")) return;
          keep.disabled = true;
          retry.disabled = true;
          if (state) state.textContent = "Resolving failed Factory Reset…";
          try {
            const result = await systemPostForm("/api/system/workspace/reset/factory/resolve", {
              operation_id: operation.operation_id,
              keep_current_state: "yes",
            });
            if (result?.operation) renderResetOperation(result.operation);
            else await renderReset(++loadSequence);
          } catch (error) {
            if (state) state.textContent = error?.message || "Failed Factory Reset could not be resolved.";
            keep.disabled = false;
            retry.disabled = false;
          }
        });

        const retry = document.createElement("button");
        retry.type = "button";
        retry.className = "danger";
        retry.textContent = "Review and retry Factory Reset";
        retry.addEventListener("click", () => planReset("factory"));
        actions.append(keep, retry);
        built.panel.appendChild(actions);
      }
    } else if (operation.state === "needs_attention") {
      const recovery = document.createElement("div");
      recovery.className = "notice warning";
      recovery.innerHTML = "<strong>Minecraft Reset stopped before completion.</strong><br>Review the reset impact and retry the same persistent operation.";
      built.panel.appendChild(recovery);

      const actions = document.createElement("div");
      actions.className = "system-reset-actions";
      const retry = document.createElement("button");
      retry.type = "button";
      retry.className = "danger";
      retry.textContent = "Retry Reset Minecraft";
      retry.addEventListener("click", () => planReset("minecraft"));
      actions.appendChild(retry);
      built.panel.appendChild(actions);
    }

    root.appendChild(built.panel);
    content.replaceChildren(root);

    const terminal = ["succeeded", "needs_attention", "resolved", "rolled_back"].includes(operation.state);
    if (terminal) {
      if (state) state.textContent = operation.status || systemResetOperationLabel(operation);
      return;
    }

    resetPollTimer = window.setTimeout(async () => {
      if (currentTab !== "reset" || !content?.isConnected) return;
      try {
        const payload = await systemFetchJSON("/api/system/workspace/reset/operations/" + operation.operation_id, {},
          operation.operation_type === "factory_reset" ? operation.operation_id : "");
        if (payload?.operation) renderResetOperation(payload.operation);
      } catch (error) {
        if (state) state.textContent = error?.message || "Reset progress is temporarily unavailable.";
        resetPollTimer = window.setTimeout(() => renderReset(++loadSequence), 2500);
      }
    }, 1800);
  };

  const resetCurrentOperation = async () => {
    const factory = await systemFetchJSON("/api/system/workspace/reset/factory/current");
    if (factory?.operation) return factory.operation;
    const minecraft = await systemFetchJSON("/api/system/workspace/reset/minecraft/current");
    return minecraft?.operation || null;
  };

  const resetImpactFromPlan = (mode, plan) => {
    const remove = [];
    const keep = [];

    if (mode === "minecraft") {
      remove.push("Current Minecraft container and runtime configuration.");
      if (plan.data_action === "delete") remove.push("Minecraft world/data on appliance-owned internal storage.");
      else keep.push("Minecraft data outside appliance-owned internal storage.");
      if (plan.plugins_action === "remove_managed_jars" || plan.plugins_action === "delete_with_internal_data") remove.push("Plugins-managed JARs installed through JustVoxel.");
      if (plan.data_action === "preserve") keep.push("Plugin configuration/data folders and manual plugins.");
      keep.push("All backup archives.");
      keep.push("Current login password and WebUI accounts.");
      keep.push("Storage partitions, filesystems, mounts, and host configuration.");
      keep.push("USB, external, NFS, and SMB data.");
    } else {
      remove.push("Current Minecraft container and runtime configuration.");
      if (plan.data_action === "delete") remove.push("Minecraft world/data on appliance-owned internal storage.");
      else if (plan.data_action === "preserve") keep.push("Minecraft data outside appliance-owned internal storage.");
      if (plan.backup_action === "delete") remove.push("Backup archives on appliance-owned internal storage.");
      else if (plan.backup_action === "preserve") keep.push("Backup archives on USB, external, or network storage.");
      remove.push("Local JustVoxel configuration backups.");
      remove.push("Additional WebUI users, notifications, history, and local authentication state.");
      remove.push("Current WebUI sessions; the voxel system password returns to voxel / voxel and must be replaced immediately after sign-in.");
      keep.push("USB, external, NFS, and SMB data.");
      remove.push("JustVoxel-managed mounts and /etc/fstab entries are reset; storage data is not erased by removing these entries.");
      keep.push("Storage partitions, filesystems, and unrelated administrator /etc/fstab entries.");
      keep.push("Network configuration, SSH configuration, and the JustVoxel OS image.");
    }
    return { remove, keep };
  };

  const renderResetPlan = (mode, plan, requireSystemPassword = false) => {
    clearResetPoll();
    if (!content) return;
    const root = document.createElement("div");
    root.className = "system-reset-view";
    const title = mode === "factory" ? "Full Factory Reset" : "Reset Minecraft";
    const built = systemPanel("Review reset", title);
    built.panel.classList.add("system-reset-review");

    const description = document.createElement("p");
    description.className = "muted compact";
    description.textContent = mode === "factory"
      ? "This resets JustVoxel-owned local state. External and network storage are never erased."
      : "This removes the current Minecraft installation while preserving backups, accounts, and storage layout.";
    built.panel.appendChild(description);

    const impact = resetImpactFromPlan(mode, plan);
    const impactGrid = document.createElement("div");
    impactGrid.className = "system-reset-impact-grid";
    impactGrid.append(
      systemResetList("Will remove", impact.remove, "danger"),
      systemResetList("Will keep", impact.keep, "safe")
    );
    built.panel.appendChild(impactGrid);

    if (Array.isArray(plan.warnings) && plan.warnings.length) {
      const warning = document.createElement("div");
      warning.className = "notice warning";
      warning.textContent = plan.warnings.join(" ");
      built.panel.appendChild(warning);
    }

    const online = Number(plan.players_online || 0);
    let playersConfirm = null;
    if (online > 0) {
      const players = document.createElement("div");
      players.className = "notice warning";
      const names = Array.isArray(plan.players) && plan.players.length ? " (" + plan.players.join(", ") + ")" : "";
      players.textContent = String(online) + " player(s) are online" + names + ". Reset will disconnect them.";
      built.panel.appendChild(players);

      const playerLabel = document.createElement("label");
      playerLabel.className = "system-reset-check";
      playersConfirm = document.createElement("input");
      playersConfirm.type = "checkbox";
      const copy = document.createElement("span");
      copy.textContent = "I understand the online players will be disconnected.";
      playerLabel.append(playersConfirm, copy);
      built.panel.appendChild(playerLabel);
    }

    let password = null;
    if (mode === "factory" && requireSystemPassword) {
      const passwordLabel = document.createElement("label");
      passwordLabel.className = "system-reset-password";
      passwordLabel.textContent = "Current voxel system password";
      password = document.createElement("input");
      password.type = "password";
      password.autocomplete = "current-password";
      password.required = true;
      passwordLabel.appendChild(password);
      const help = document.createElement("small");
      help.className = "muted";
      help.textContent = "Required because WebUI is using a separate browser password.";
      passwordLabel.appendChild(help);
      built.panel.appendChild(passwordLabel);
    }

    const arm = document.createElement("div");
    arm.className = "destructive-confirmation";
    const sliderShell = document.createElement("div");
    sliderShell.className = "destructive-confirm-slider";
    const armedText = document.createElement("span");
    armedText.className = "destructive-confirm-slider-text";
    armedText.textContent = "Slide to confirm reset";
    const thumb = document.createElement("span");
    thumb.className = "destructive-confirm-slider-thumb";
    thumb.setAttribute("aria-hidden", "true");
    thumb.textContent = ">";
    const slider = document.createElement("input");
    slider.type = "range";
    slider.min = "0";
    slider.max = "100";
    slider.step = "1";
    slider.value = "0";
    slider.setAttribute("aria-label", "Slide to confirm reset");
    sliderShell.append(armedText, thumb, slider);

    const finalLabel = document.createElement("label");
    finalLabel.className = "destructive-confirm-toggle-row";
    finalLabel.hidden = true;
    const finalCopy = document.createElement("span");
    finalCopy.className = "destructive-confirm-toggle-callout";
    finalCopy.textContent = "Confirm >";
    const finalConfirm = document.createElement("input");
    finalConfirm.className = "destructive-confirm-toggle";
    finalConfirm.type = "checkbox";
    finalLabel.append(finalCopy, finalConfirm);
    arm.append(sliderShell, finalLabel);
    built.panel.appendChild(arm);

    const actions = document.createElement("div");
    actions.className = "system-reset-actions";
    const cancel = document.createElement("button");
    cancel.type = "button";
    cancel.className = "secondary";
    cancel.textContent = "Cancel";
    const apply = document.createElement("button");
    apply.type = "button";
    apply.className = "danger";
    apply.textContent = mode === "factory" ? "Full Factory Reset" : "Reset Minecraft";
    apply.disabled = true;
    actions.append(cancel, apply);
    built.panel.appendChild(actions);

    const syncApply = () => {
      const progress = Math.max(0, Math.min(100, Number(slider.value || 0)));
      sliderShell.style.setProperty("--confirm-progress", String(progress / 100));
      const armed = progress >= 100;
      sliderShell.classList.toggle("is-armed", armed);
      armedText.textContent = armed ? "Reset armed" : "Slide to confirm reset";
      finalLabel.hidden = !armed;
      if (!armed) finalConfirm.checked = false;
      const playersOK = !playersConfirm || playersConfirm.checked;
      const passwordOK = !password || password.value.length > 0;
      apply.disabled = !(armed && finalConfirm.checked && playersOK && passwordOK);
    };

    slider.addEventListener("input", syncApply);
    finalConfirm.addEventListener("change", syncApply);
    playersConfirm?.addEventListener("change", syncApply);
    password?.addEventListener("input", syncApply);
    cancel.addEventListener("click", () => renderResetChoices());

    apply.addEventListener("click", async () => {
      apply.disabled = true;
      cancel.disabled = true;
      slider.disabled = true;
      finalConfirm.disabled = true;
      if (playersConfirm) playersConfirm.disabled = true;
      if (password) password.disabled = true;
      if (state) state.textContent = "Starting reset…";
      try {
        const fields = {
          plan_fingerprint: plan.plan_fingerprint,
          confirm_players: online > 0 ? "yes" : "no",
        };
        if (password) fields.system_password = password.value;
        const endpoint = mode === "factory"
          ? "/api/system/workspace/reset/factory/apply"
          : "/api/system/workspace/reset/minecraft/apply";
        const result = await systemPostForm(endpoint, fields);
        if (password) password.value = "";
        if (!result?.operation) throw new Error("Reset operation did not start.");
        renderResetOperation(result.operation);
      } catch (error) {
        if (password) password.value = "";
        if (state) state.textContent = error?.message || "Reset could not start.";
        cancel.disabled = false;
        slider.disabled = false;
        if (playersConfirm) playersConfirm.disabled = false;
        if (password) password.disabled = false;
        syncApply();
      }
    });

    root.appendChild(built.panel);
    content.replaceChildren(root);
    syncApply();
  };

  const planReset = async (mode) => {
    if (state) state.textContent = "Checking current reset impact…";
    try {
      const endpoint = mode === "factory"
        ? "/api/system/workspace/reset/factory/plan"
        : "/api/system/workspace/reset/minecraft/plan";
      const plan = await systemPostForm(endpoint);
      if (!plan) return;
      let requireSystemPassword = false;
      if (mode === "factory") {
        const security = await systemFetchJSON("/api/system/workspace/security");
        if (!security) return;
        requireSystemPassword = security.mode === "separate";
      }
      renderResetPlan(mode, plan, requireSystemPassword);
      if (state) state.textContent = "";
    } catch (error) {
      if (state) state.textContent = error?.message || "Reset could not be planned.";
    }
  };

  const renderResetChoices = () => {
    clearResetPoll();
    activeFactoryResetOperationID = "";
    if (!content) return;
    const root = document.createElement("div");
    root.className = "system-reset-view";

    const intro = document.createElement("div");
    intro.className = "system-reset-intro";
    const title = document.createElement("h3");
    title.textContent = "Start over safely";
    const copy = document.createElement("p");
    copy.className = "muted compact";
    copy.textContent = "Choose how much JustVoxel should reset. External, USB, NFS, and SMB storage are never erased by Full Factory Reset.";
    intro.append(title, copy);
    root.appendChild(intro);

    const choices = document.createElement("div");
    choices.className = "system-reset-choices";

    const minecraft = document.createElement("button");
    minecraft.type = "button";
    minecraft.className = "system-reset-choice";
    minecraft.innerHTML =
      '<strong>Reset Minecraft</strong>' +
      '<span>Remove the current Minecraft server and internal Minecraft data.</span>' +
      '<small>Keeps backups, login/password, WebUI users, and storage layout.</small>';
    minecraft.addEventListener("click", () => planReset("minecraft"));

    const factory = document.createElement("button");
    factory.type = "button";
    factory.className = "system-reset-choice danger";
    factory.innerHTML =
      '<strong>Full Factory Reset</strong>' +
      '<span>Return JustVoxel-owned local state to first-use condition.</span>' +
      '<small>Deletes local Minecraft, local backups, WebUI users/history, and resets authentication. External/network storage stays untouched.</small>';
    factory.addEventListener("click", () => planReset("factory"));

    choices.append(minecraft, factory);
    root.appendChild(choices);
    content.replaceChildren(root);
  };

  const renderReset = async (sequence) => {
    clearResetPoll();
    const operation = await resetCurrentOperation();
    if (sequence !== loadSequence || currentTab !== "reset") return;
    if (operation) {
      renderResetOperation(operation);
      return;
    }
    renderResetChoices();
  };

  const renderAbout = async (sequence) => {
    const payload = await systemFetchJSON("/api/system/workspace/about");
    if (!payload || sequence !== loadSequence || !content) return;
    const root = document.createElement("div");
    root.className = "system-about-view";
    const variant = String(payload.variant || "").trim().toLowerCase();
    const variantName = ({ vm: "VM", hws: "HWS", "justvoxel-vm": "VM", "justvoxel-hws": "HWS" })[variant];
    const built = systemPanel("Installed appliance", variantName ? `JustVoxel ${variantName}` : "JustVoxel");
    const list = document.createElement("dl");
    [
      ["JustVoxel", payload.justvoxel || "—"],
      ["WebUI", payload.webui || "—"],
      ["Management API", payload.management_api || "—"],
      ["Source", payload.commit || "—"],
      ["Signed in as", (payload.username || "—") + " (" + (payload.role || "—") + ")"],
    ].forEach(([label, value]) => {
      const row = document.createElement("div");
      const dt = document.createElement("dt");
      dt.textContent = label;
      const dd = document.createElement("dd");
      dd.textContent = value;
      row.append(dt, dd);
      list.appendChild(row);
    });
    built.panel.appendChild(list);
    const project = document.createElement("div");
    project.className = "about-project";
    const projectHeading = document.createElement("h3");
    projectHeading.textContent = "Project";
    const projectList = document.createElement("dl");
    [
      ["Repository", "JustVoxel on GitHub", "https://github.com/home-server-project/justvoxel"],
      ["Organization", "Home Server Project", "https://github.com/home-server-project"],
      ["Support / Bugs", "Report an issue", "https://github.com/home-server-project/justvoxel/issues"],
    ].forEach(([label, text, url]) => {
      const row = document.createElement("div");
      const dt = document.createElement("dt");
      dt.textContent = label;
      const dd = document.createElement("dd");
      const link = document.createElement("a");
      link.href = url;
      link.target = "_blank";
      link.rel = "noopener noreferrer";
      link.textContent = text;
      dd.appendChild(link);
      row.append(dt, dd);
      projectList.appendChild(row);
    });
    project.append(projectHeading, projectList);
    built.panel.appendChild(project);
    const disclaimer = document.createElement("p");
    disclaimer.className = "disclaimer";
    disclaimer.textContent = "NOT AN OFFICIAL MINECRAFT PRODUCT. NOT APPROVED BY OR ASSOCIATED WITH MOJANG OR MICROSOFT.";
    root.append(built.panel, disclaimer);
    content.replaceChildren(root);
  };

  const systemUPSStateLabel = (value) => {
    const labels = {
      online: "Online",
      on_battery: "On battery",
      low_battery: "Low battery",
      bypass: "Bypass",
      unknown: "Unknown",
    };
    return labels[value] || "Unknown";
  };

  const systemUPSNumber = (value, suffix = "") => {
    if (value === null || value === undefined || Number.isNaN(Number(value))) return "—";
    const number = Number(value);
    const digits = Math.abs(number % 1) > 0.001 ? 1 : 0;
    return number.toFixed(digits) + suffix;
  };

  const systemUPSRuntime = (seconds) => {
    if (seconds === null || seconds === undefined || Number(seconds) < 0) return "—";
    const total = Math.round(Number(seconds));
    const hours = Math.floor(total / 3600);
    const minutes = Math.floor((total % 3600) / 60);
    if (hours > 0) return hours + "h " + String(minutes).padStart(2, "0") + "m";
    return minutes + "m";
  };

  const systemUPSMetric = (label, value) => {
    const item = document.createElement("div");
    item.className = "system-ups-metric";
    const name = document.createElement("span");
    name.textContent = label;
    const data = document.createElement("strong");
    data.textContent = value;
    item.append(name, data);
    return item;
  };

  const openUPSOptions = (snapshot, selectedMode) => {
    if (!snapshot.available) return;
    const dialog = document.createElement("dialog");
    dialog.className = "system-action-dialog";
    dialog.setAttribute("aria-label", "UPS options");
    const shell = document.createElement("div");
    shell.className = "system-action-dialog-content";
    const title = document.createElement("h2");
    title.textContent = "UPS options";
    const settingField = (label, name, type, value) => {
      const field = document.createElement("label");
      field.textContent = label;
      const input = document.createElement("input");
      input.name = name;
      input.type = type;
      input.autocomplete = "off";
      if (type !== "password") input.value = value || "";
      field.appendChild(input);
      return { field, input };
    };
    const shutdownSection = document.createElement("section");
    shutdownSection.className = "system-ups-option-section";
    const shutdownTitle = document.createElement("h3");
    shutdownTitle.textContent = "Automatic shutdown";
    const shutdownToggle = settingField("Shut down after UPS has been on battery", "enabled", "checkbox", "");
    shutdownToggle.field.classList.add("system-ups-shutdown-switch");
    shutdownToggle.input.setAttribute("role", "switch");
    shutdownToggle.input.checked = Boolean(snapshot.protection_enabled);
    const delay = settingField("Shutdown delay (seconds)", "delay_seconds", "number", String(snapshot.shutdown_delay_seconds || 120));
    delay.input.min = "1";
    delay.input.max = "86400";
    const monitorUser = settingField("Monitor username", "monitor_username", "text", "");
    const monitorPassword = settingField("Monitor password", "monitor_password", "password", "");
    const shutdownNote = document.createElement("p");
    shutdownNote.textContent = snapshot.monitor_credentials_configured ? "Credentials configured. Enter username and password again to update." : "Enter username and password to enable.";
    const shutdownState = document.createElement("p");
    shutdownState.textContent = "Monitor service: " + (snapshot.monitor_service_active ? "active" : "inactive") + " · NUT mode: " + (snapshot.nut_mode || "none");
    const shutdownSave = document.createElement("button");
    shutdownSave.type = "button";
    shutdownSave.className = "secondary";
    shutdownSave.textContent = "Save shutdown settings";
    shutdownSection.append(shutdownTitle, shutdownToggle.field, delay.field, monitorUser.field, monitorPassword.field, shutdownNote, shutdownState, shutdownSave);

    const sharingSection = document.createElement("section");
    sharingSection.className = "system-ups-option-section";
    const sharingAvailable = selectedMode === "local";
    const sharingTitle = document.createElement("h3");
    sharingTitle.textContent = "Network sharing";
    const sharingToggle = settingField("Share this UPS over the network", "enabled", "checkbox", "");
    sharingToggle.field.classList.add("system-ups-shutdown-switch");
    sharingToggle.input.setAttribute("role", "switch");
    sharingToggle.input.checked = Boolean(snapshot.sharing_enabled);
    const listenAddress = settingField("Listen address", "listen_address", "text", snapshot.sharing_listen_address);
    const listenPort = settingField("Port", "listen_port", "number", String(snapshot.sharing_listen_port || 3493));
    listenPort.input.min = "1";
    listenPort.input.max = "65535";
    const clientUser = settingField("Client username", "client_username", "text", "");
    const clientPassword = settingField("Client password", "client_password", "password", "");
    const sharingNote = document.createElement("p");
    sharingNote.textContent = snapshot.sharing_credentials_configured ? "Credentials configured. Enter username and password again to update." : "Enter username and password to enable.";
    const sharingState = document.createElement("p");
    sharingState.textContent = "Server service: " + (snapshot.server_service_active ? "active" : "inactive") + " · Driver service: " + (snapshot.driver_service_active ? "active" : "inactive");
    const sharingSave = document.createElement("button");
    sharingSave.type = "button";
    sharingSave.className = "secondary";
    sharingSave.textContent = "Save sharing settings";
    sharingSection.append(sharingTitle, sharingToggle.field, listenAddress.field, listenPort.field, clientUser.field, clientPassword.field, sharingNote, sharingState, sharingSave);
    if (!sharingAvailable) {
      sharingToggle.input.disabled = true;
      sharingSave.disabled = true;
    }

    const syncOptionFields = (toggle, fields) => {
      fields.forEach(({ input }) => { input.disabled = toggle.input.disabled || !toggle.input.checked; });
    };
    shutdownToggle.input.addEventListener("change", () => syncOptionFields(shutdownToggle, [delay, monitorUser, monitorPassword]));
    sharingToggle.input.addEventListener("change", () => syncOptionFields(sharingToggle, [listenAddress, listenPort, clientUser, clientPassword]));
    syncOptionFields(shutdownToggle, [delay, monitorUser, monitorPassword]);
    syncOptionFields(sharingToggle, [listenAddress, listenPort, clientUser, clientPassword]);

    const section = document.createElement("h3");
    section.textContent = "Saved UPS source";
    const copy = document.createElement("p");
    copy.textContent = "Remove this saved UPS connection and return to a UPS connected directly to this machine.";
    const error = document.createElement("p");
    error.className = "system-action-dialog-error";
    error.hidden = true;
    const actions = document.createElement("div");
    actions.className = "action-row";
    const cancel = document.createElement("button");
    cancel.type = "button";
    cancel.className = "secondary";
    cancel.textContent = "Cancel";
    const forget = document.createElement("button");
    forget.type = "button";
    forget.className = "danger";
    forget.textContent = "Remove saved source";
    actions.append(cancel, forget);
    const optionColumns = document.createElement("div");
    optionColumns.className = "system-ups-option-columns";
    optionColumns.append(shutdownSection, sharingSection);
    shell.append(title, optionColumns, section, copy, error, actions);
    dialog.appendChild(shell);
    document.body.appendChild(dialog);

    const close = () => {
      if (dialog.open) dialog.close();
      dialog.remove();
    };
    const saveOptions = async (path, toggle, fields, button, fallback) => {
      error.hidden = true;
      button.disabled = true;
      try {
        const body = new URLSearchParams({ csrf: upsCSRF?.value || "", enabled: String(toggle.input.checked) });
        if (toggle.input.checked) fields.forEach(({ input }) => body.set(input.name, input.value));
        const response = await systemFetchPage(path, {
          method: "POST",
          headers: { Accept: "application/json", "Content-Type": "application/x-www-form-urlencoded" },
          body: body.toString(),
        });
        if (!response) return;
        const result = await response.json().catch(() => ({}));
        if (!response.ok) throw new Error(result.error || fallback);
        close();
        upsAvailable = Boolean(result.available);
        if (upsTabButton) upsTabButton.hidden = !upsAvailable;
        renderUPSMarkup(result, await loadIdentity());
        if (state) state.textContent = "";
      } catch (failure) {
        error.textContent = failure?.message || fallback;
        error.hidden = false;
      } finally {
        button.disabled = false;
      }
    };
    shutdownSave.addEventListener("click", () => saveOptions("/api/ups/shutdown", shutdownToggle, [delay, monitorUser, monitorPassword], shutdownSave, "Shutdown settings could not be saved."));
    sharingSave.addEventListener("click", () => saveOptions("/api/ups/sharing", sharingToggle, [listenAddress, listenPort, clientUser, clientPassword], sharingSave, "Sharing settings could not be saved."));
    cancel.addEventListener("click", close);
    dialog.addEventListener("cancel", (event) => {
      event.preventDefault();
      if (!forget.disabled) close();
    });
    let confirmed = false;
    forget.addEventListener("click", async () => {
      if (!confirmed) {
        confirmed = true;
        title.textContent = "Remove saved source?";
        copy.textContent = "Remove the saved UPS source and return to local monitoring?";
        forget.textContent = "Confirm removal";
        forget.focus();
        return;
      }
      forget.disabled = true;
      cancel.disabled = true;
      error.hidden = true;
      try {
        const body = new URLSearchParams({ csrf: upsCSRF?.value || "" });
        const response = await systemFetchPage("/api/ups/source/forget", {
          method: "POST",
          headers: { Accept: "application/json", "Content-Type": "application/x-www-form-urlencoded" },
          body: body.toString(),
        });
        if (!response) return;
        const result = await response.json().catch(() => ({}));
        if (!response.ok) throw new Error(result.error || "UPS source could not be forgotten.");
        close();
        upsAvailable = Boolean(result.available);
        if (upsTabButton) upsTabButton.hidden = !upsAvailable;
        renderUPSMarkup(result, await loadIdentity());
        if (state) state.textContent = "";
      } catch (failure) {
        error.textContent = failure?.message || "UPS source could not be forgotten.";
        error.hidden = false;
      } finally {
        forget.disabled = false;
        cancel.disabled = false;
      }
    });
    dialog.showModal();
  };

  const renderUPSMarkup = (snapshot, user) => {
    if (!content) return;
    const root = document.createElement("div");
    root.className = "system-ups-overview";

    const sourceBar = document.createElement("div");
    sourceBar.className = "system-ups-source";
    const sourceLabel = document.createElement("span");
    sourceLabel.textContent = "Source";
    const sourceValue = document.createElement("strong");
    sourceValue.textContent = snapshot.source?.mode === "remote"
      ? (snapshot.source.host + ":" + snapshot.source.port)
      : "This machine";
    sourceBar.append(sourceLabel, sourceValue);
    root.appendChild(sourceBar);

    if (snapshot.message) {
      const notice = document.createElement("div");
      notice.className = "notice system-ups-notice";
      notice.textContent = snapshot.message;
      root.appendChild(notice);
    }

    if (Array.isArray(snapshot.devices) && snapshot.devices.length > 0) {
      const grid = document.createElement("div");
      grid.className = "system-ups-device-grid";
      snapshot.devices.forEach((device) => {
        const card = document.createElement("section");
        card.className = "system-ups-card";

        const heading = document.createElement("div");
        heading.className = "system-ups-card-heading";
        const titleWrap = document.createElement("div");
        const title = document.createElement("h3");
        title.textContent = device.display_name || device.name || "UPS";
        const technical = document.createElement("small");
        technical.textContent = device.name || "";
        titleWrap.append(title, technical);
        const status = document.createElement("span");
        status.className = "system-ups-status system-ups-status-" + String(device.state || "unknown");
        status.textContent = systemUPSStateLabel(device.state);
        heading.append(titleWrap, status);

        const metrics = document.createElement("div");
        metrics.className = "system-ups-metrics";
        metrics.append(
          systemUPSMetric("Battery", systemUPSNumber(device.battery_charge, "%")),
          systemUPSMetric("Runtime", systemUPSRuntime(device.battery_runtime_seconds)),
          systemUPSMetric("Load", systemUPSNumber(device.load_percent, "%")),
          systemUPSMetric("Input", systemUPSNumber(device.input_voltage, " V")),
          systemUPSMetric("Output", systemUPSNumber(device.output_voltage, " V")),
          systemUPSMetric("Input frequency", systemUPSNumber(device.input_frequency, " Hz")),
          systemUPSMetric("Output frequency", systemUPSNumber(device.output_frequency, " Hz")),
          systemUPSMetric("Battery voltage", systemUPSNumber(device.battery_voltage, " V")),
          systemUPSMetric("Temperature", systemUPSNumber(device.temperature, " °C"))
        );
        card.append(heading, metrics);
        grid.appendChild(card);
      });
      root.appendChild(grid);
    }

    if (snapshot.available && user?.role === "administrator") {
      const panel = document.createElement("section");
      panel.className = "system-ups-source-panel";
      const heading = document.createElement("h3");
      heading.textContent = "Monitoring source";
      const help = document.createElement("p");
      help.textContent = "Read a UPS connected to this HWS machine, or monitor a NUT server on another machine.";

      const form = document.createElement("form");
      form.method = "post";
      form.action = "/api/ups/source";
      form.className = "system-ups-source-form";

      const csrf = document.createElement("input");
      csrf.type = "hidden";
      csrf.name = "csrf";
      csrf.value = upsCSRF?.value || "";

      const modeLabel = document.createElement("label");
      modeLabel.textContent = "Source";
      const mode = document.createElement("select");
      mode.name = "mode";
      const localOption = document.createElement("option");
      localOption.value = "local";
      localOption.textContent = "UPS connected to this machine";
      const remoteOption = document.createElement("option");
      remoteOption.value = "remote";
      remoteOption.textContent = "UPS on another machine";
      mode.append(localOption, remoteOption);
      mode.value = snapshot.source?.mode === "remote" ? "remote" : "local";
      modeLabel.appendChild(mode);

      const hostLabel = document.createElement("label");
      hostLabel.textContent = "Host / IP";
      const host = document.createElement("input");
      host.name = "host";
      host.type = "text";
      host.autocomplete = "off";
      host.placeholder = "Hostname or IP address";
      host.value = snapshot.source?.host || "";
      hostLabel.appendChild(host);

      const portLabel = document.createElement("label");
      portLabel.textContent = "Port";
      const port = document.createElement("input");
      port.name = "port";
      port.type = "number";
      port.min = "1";
      port.max = "65535";
      port.value = String(snapshot.source?.port || 3493);
      portLabel.appendChild(port);

      const nameLabel = document.createElement("label");
      nameLabel.textContent = "UPS name";
      const name = document.createElement("input");
      name.name = "ups_name";
      name.type = "text";
      name.autocomplete = "off";
      name.value = snapshot.source?.ups_name || "";
      nameLabel.appendChild(name);

      const driverLabel = document.createElement("label");
      driverLabel.textContent = "NUT driver";
      const driver = document.createElement("input");
      driver.name = "driver";
      driver.type = "text";
      driver.autocomplete = "off";
      driver.value = snapshot.source?.driver || "";
      driverLabel.appendChild(driver);

      const deviceLabel = document.createElement("label");
      deviceLabel.textContent = "Device port";
      const device = document.createElement("input");
      device.name = "device_port";
      device.type = "text";
      device.autocomplete = "off";
      device.value = snapshot.source?.device_port || "";
      deviceLabel.appendChild(device);

      const save = document.createElement("button");
      save.type = "submit";
      save.className = "button primary";
      save.textContent = "Save and verify";

      const actions = document.createElement("div");
      actions.className = "system-ups-source-actions";
      const options = document.createElement("button");
      options.type = "button";
      options.className = "secondary";
      options.textContent = "Options";
      options.addEventListener("click", () => openUPSOptions(snapshot, mode.value));
      actions.append(options, save);
      const finalRow = document.createElement("div");
      finalRow.className = "system-ups-source-final-row";
      finalRow.append(nameLabel, actions);

      const syncRemoteFields = () => {
        const remote = mode.value === "remote";
        host.disabled = !remote;
        port.disabled = !remote;
        driver.disabled = remote;
        device.disabled = remote;
        hostLabel.hidden = !remote;
        portLabel.hidden = !remote;
        driverLabel.hidden = remote;
        deviceLabel.hidden = remote;
        host.required = remote;
      };
      mode.addEventListener("change", syncRemoteFields);
      syncRemoteFields();

      form.append(csrf, modeLabel, hostLabel, portLabel, driverLabel, deviceLabel, finalRow);
      panel.append(heading, help, form);
      root.appendChild(panel);
    }

    content.replaceChildren(root);
  };

  const fetchUPSStatus = async () => {
    const response = await fetch("/api/ups", {
      method: "GET",
      credentials: "same-origin",
      headers: { Accept: "application/json" },
      cache: "no-store",
    });
    if (systemHandleAuth(response)) return null;
    if (!response.ok) throw new Error("UPS monitoring is unavailable.");
    return response.json();
  };

  const refreshUPSCapability = async () => {
    const snapshot = await fetchUPSStatus();
    if (!snapshot) return null;
    upsAvailable = Boolean(snapshot.available);
    if (upsTabButton) upsTabButton.hidden = !upsAvailable;
    if (!upsAvailable && currentTab === "ups") {
      currentTab = identity?.role === "administrator" ? "health" : "history";
      syncSystemTabs();
    }
    return snapshot;
  };

  const loadUPS = async (sequence) => {
    const snapshot = await fetchUPSStatus();
    if (!snapshot || sequence !== loadSequence) return;
    upsAvailable = Boolean(snapshot.available);
    if (upsTabButton) upsTabButton.hidden = !upsAvailable;
    if (!upsAvailable) throw new Error("UPS monitoring is unavailable on this JustVoxel variant.");
    renderUPSMarkup(snapshot, await loadIdentity());
  };

  const submitUPSSource = async (form) => {
    const body = new URLSearchParams();
    new FormData(form).forEach((value, key) => body.append(key, String(value)));
    const submitter = form.querySelector('button[type="submit"]');
    if (submitter) submitter.disabled = true;
    if (state) state.textContent = "Verifying NUT source…";
    try {
      const response = await systemFetchPage("/api/ups/source", {
        method: "POST",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/x-www-form-urlencoded",
        },
        body: body.toString(),
      });
      if (!response) return;
      const result = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(result.error || "UPS source could not be saved.");
      upsAvailable = Boolean(result.available);
      if (upsTabButton) upsTabButton.hidden = !upsAvailable;
      renderUPSMarkup(result, await loadIdentity());
      if (state) state.textContent = "";
    } catch (error) {
      if (state) state.textContent = error?.message || "UPS source could not be saved.";
    } finally {
      if (submitter && submitter.isConnected) submitter.disabled = false;
    }
  };

  const settingsPanels = new Map(Array.from(document.querySelectorAll("[data-system-settings-panel]"), (panel) =>
    [panel.dataset.systemSettingsPanel, { panel, home: panel.parentNode }]));
  const parkSettingsPanels = () => {
    settingsPanels.forEach(({ panel, home }) => {
      panel.hidden = true;
      home.appendChild(panel);
    });
  };

  const loadCurrentSystemTab = async () => {
    if (window.JustVoxelWallpaper?.canLeave() === false) return;
    window.JustVoxelWallpaper?.leave();
    parkSettingsPanels();
    const sequence = ++loadSequence;
    if (state) state.textContent = "Loading…";
    if (refreshButton) refreshButton.disabled = true;
    if (content) content.replaceChildren();

    try {
      const user = await loadIdentity();
      if (!user) return;
      if (sequence !== loadSequence) return;
      if (administratorTabs.has(currentTab) && user.role !== "administrator") {
        currentTab = "history";
        syncSystemTabs();
      }
      if (currentTab === "health") await renderHealth(sequence);
      else if (currentTab === "history") await renderHistory(sequence);
      else if (currentTab === "users") await renderUsers(sequence);
      else if (currentTab === "security") await renderSecurity(sequence);
      else if (currentTab === "logs") await renderLogs(sequence);
      else if (settingsPanels.has(currentTab)) {
        const { panel } = settingsPanels.get(currentTab);
        if (sequence !== loadSequence) return;
        panel.hidden = false;
        content.appendChild(panel);
        if (currentTab === "date-time") await window.JustVoxelDateTime?.refresh();
        else window.JustVoxelWallpaper?.prepare();
      }
      else if (currentTab === "reset") await renderReset(sequence);
      else if (currentTab === "ups") await loadUPS(sequence);
      else await renderAbout(sequence);
      if (sequence === loadSequence && state) state.textContent = "";
    } catch (error) {
      if (sequence !== loadSequence) return;
      if (content) content.replaceChildren();
      if (state) state.textContent = error?.message || "System workspace is unavailable.";
    } finally {
      if (sequence === loadSequence && refreshButton) refreshButton.disabled = false;
    }
  };

  const selectSystemTab = async (tab) => {
    if (window.JustVoxelWallpaper?.canLeave() === false) return;
    const user = await loadIdentity();
    if (!user) return;
    if (administratorTabs.has(tab) && user.role !== "administrator") return;
    if (tab === "ups" && !upsAvailable) return;
    if (currentTab === "reset" && tab !== "reset") clearResetPoll();
    failedUnitsSelected = false;
    currentTab = tab;
    syncSystemTabs();
    loadCurrentSystemTab();
  };

  content?.addEventListener("submit", async (event) => {
    const form = event.target.closest("form");
    if (!form || !content.contains(form)) return;
    const action = new URL(form.getAttribute("action") || window.location.href, window.location.href);
    if (action.pathname !== "/api/ups/source") return;
    event.preventDefault();
    await submitUPSSource(form);
  });

  const workspaceWindow = setupWorkspaceWindow(systemWorkspaceDialog, {
    onClose: () => { failedUnitsSelected = false; window.JustVoxelWallpaper?.leave(); clearResetPoll(); },
    onOpen: async () => {
      try {
        const user = await loadIdentity();
        if (!user) return;
        await refreshUPSCapability();
        if (!initialized) {
          initialized = true;
          const target = new URLSearchParams(window.location.search);
          const requested = target.get("workspace") === "system" ? target.get("tab") : "";
          currentTab = user.role === "administrator" ? "health" : "history";
          if (["health", "history", "users", "security", "logs", "date-time", "wallpaper", "reset", "about"].includes(requested) &&
              (!administratorTabs.has(requested) || user.role === "administrator")) currentTab = requested;
          syncSystemTabs();
        }
        loadCurrentSystemTab();
      } catch (error) {
        if (state) state.textContent = error?.message || "System workspace is unavailable.";
      }
    },
  });

  window.JustVoxelSystem = {
    openPluginLogs: async (id) => {
      if (!/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(id)) return;
      requestedMigrationLog = { group: "Plugins", id };
      await window.JustVoxelSystem.openTab("logs");
    },
    openTab: async (tab) => {
      if (!settingsPanels.has(tab)) return;
      if (window.JustVoxelWallpaper?.canLeave() === false) return;
      const user = await loadIdentity();
      if (!user || administratorTabs.has(tab) && user.role !== "administrator") return;
      initialized = true;
      currentTab = tab;
      syncSystemTabs();
      if (controlCenter) controlCenter.open = false;
      workspaceWindow?.open();
    },
  };

  systemWorkspaceOpen.addEventListener("click", () => {
    if (controlCenter) controlCenter.open = false;
    workspaceWindow?.open();
  });
  closeButton?.addEventListener("click", () => {
    if (window.JustVoxelWallpaper?.canLeave() === false) return;
    window.JustVoxelWallpaper?.leave();
    clearResetPoll();
    workspaceWindow?.close();
  });
  refreshButton?.addEventListener("click", loadCurrentSystemTab);
  tabs.forEach((button) => button.addEventListener("click", () => selectSystemTab(button.dataset.systemTab)));

  if (readWorkspaceWindowState("system").open) workspaceWindow?.open();
}

// Plugin workspace. All local inventory and catalog access stays
// behind the Administrator-only WebUI and Management Agent routes.
const pluginsDialog = document.querySelector("[data-plugins-workspace-dialog]");
const pluginsOpen = document.querySelector("[data-plugins-open]");
if (pluginsDialog && pluginsOpen) {
  const form = pluginsDialog.querySelector("[data-plugins-search]");
  const results = pluginsDialog.querySelector("[data-plugins-results]");
  const state = pluginsDialog.querySelector("[data-plugins-state]");
  const context = pluginsDialog.querySelector("[data-plugins-context]");
  const pagination = pluginsDialog.querySelector("[data-plugins-pagination]");
  const previous = pluginsDialog.querySelector("[data-plugins-previous]");
  const next = pluginsDialog.querySelector("[data-plugins-next]");
  const pageLabel = pluginsDialog.querySelector("[data-plugins-page]");
  let totalPages = 1, discoverServerKey = null;
  let pageLimit = 8, capacityTimer = null;
  const firstCursor = () => ({ modrinth_offset: 0, hangar_offset: 0, hangar_first: false });
  let pageCursors = [firstCursor()], pageIndex = 0;
  const resetCursors = () => { pageCursors = [firstCursor()]; pageIndex = 0; cursor = firstCursor(); nextCursor = null; totalPages = 1; pageLabel.textContent = "Page 1 of 1"; };
  let tab = "discover", cursor = firstCursor(), nextCursor = null, generation = 0, controller = null;
  const text = (tag, value, className = "") => {
    const node = document.createElement(tag);
    node.textContent = value;
    node.className = className;
    return node;
  };
  const pluginBadge = (value, semanticClass = "") => text("span", value, `plugins-badge ${semanticClass}`.trim());
  const installedStatus = () => pluginBadge("Installed", "plugins-badge-installed plugins-overlay-installed");
  const identityTile = (name, projectID, iconURL, source = "Modrinth") => {
    const tile = text("span", Array.from(name || "?").slice(0, 2).join("").toUpperCase(), "plugins-icon-tile");
    tile.setAttribute("aria-hidden", "true");
    tile.dataset.tone = String(Array.from(name || "?").reduce((sum, char) => sum + char.codePointAt(0), 0) & 3);
    // Search logos get one metadata retry; absent search logos start with metadata.
    if (typeof projectID === "string" && /^[A-Za-z0-9]{1,64}$/.test(projectID)) {
      const image = document.createElement("img");
      image.alt = "";
      image.decoding = "async";
      image.hidden = true;
      const query = new URLSearchParams({ source, project_id: projectID });
      if (source === "Modrinth" && typeof iconURL === "string" && iconURL) query.set("icon_url", iconURL);
      let metadataAttempt = !query.has("icon_url");
      let finished = false;
      image.addEventListener("load", () => { finished = true; tile.classList.add("has-logo"); image.hidden = false; }, { once: true });
      const requestImage = () => { image.src = `/api/plugins/workspace/icon?${query}`; };
      image.addEventListener("error", () => {
        if (finished) return;
        image.hidden = true;
        tile.classList.remove("has-logo");
        if (!metadataAttempt) {
          metadataAttempt = true;
          query.delete("icon_url");
          requestImage();
        } else {
          finished = true;
          image.remove();
        }
      });
      requestImage();
      tile.append(image);
    }
    return tile;
  };
  const overlayHeader = (name, description, badges, projectID, action = null, iconURL, source = "Modrinth") => {
    const header = document.createElement("header");
    header.className = "plugins-overlay-header";
    const identity = document.createElement("div");
    identity.className = "plugins-overlay-identity";
    identity.append(identityTile(name, projectID, iconURL, source), text("h3", name));
    if (action) identity.append(action);
    header.append(identity);
    if (description) header.append(text("p", description, "plugins-overlay-description"));
    const meta = document.createElement("div");
    meta.className = "plugins-card-badges";
    badges.filter((badge) => Array.isArray(badge) ? badge[0] : badge).forEach((badge) => meta.append(Array.isArray(badge) ? pluginBadge(...badge) : pluginBadge(badge)));
    header.append(meta);
    panelBody.append(header);
  };
  const overlaySection = (label, area = panelBody) => {
    const section = document.createElement("section");
    section.className = "plugins-overlay-section";
    section.append(text("h4", label));
    area.append(section);
    return section;
  };
  const overlayActions = (...buttons) => {
    const footer = document.createElement("footer");
    footer.className = "plugins-overlay-actions";
    footer.append(...buttons);
    panelBody.append(footer);
    return footer;
  };
  const interactiveCard = (card, name, activate, projectID, iconURL, source = "Modrinth") => {
    card.tabIndex = 0;
    card.setAttribute("role", "button");
    card.setAttribute("aria-label", `Open ${name}`);
    card.setAttribute("aria-haspopup", "dialog");
    card.setAttribute("aria-controls", "plugins-overlay");
    card.setAttribute("aria-pressed", "false");
    const open = () => {
      results.querySelectorAll(".plugins-result-card").forEach((node) => {
        node.classList.toggle("is-selected", node === card);
        node.setAttribute("aria-pressed", String(node === card));
      });
      card.focus();
      activate();
    };
    card.addEventListener("click", open);
    card.addEventListener("keydown", (event) => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        open();
      }
    });
    const heading = card.querySelector("h3");
    const header = document.createElement("div");
    header.className = "plugins-card-heading";
    card.insertBefore(header, heading);
    const copy = document.createElement("div");
    copy.className = "plugins-card-copy";
    copy.append(heading);
    const description = card.querySelector(".plugins-card-description, .plugins-card-release");
    if (description) copy.append(description);
    header.append(identityTile(name, projectID, iconURL, source), copy);
  };
  const addInfo = (card, name, renderInfo, projectID, iconURL, source = "Modrinth") => {
    interactiveCard(card, name, () => {
      stopPolling();
      showPanel(`Info for ${name}`, true);
      const infoActions = renderInfo();
      const actions = Array.isArray(infoActions) ? infoActions : [];
      overlayActions(...actions, panelAction("Close", closePanel));
    }, projectID, iconURL, source);
  };
  const infoRows = (container) => {
    const rows = document.createElement("dl");
    rows.className = "plugins-info-rows";
    container.append(rows);
    return (label, value) => {
      const row = document.createElement("div");
      row.className = "plugins-info-row";
      row.append(text("dt", label));
      const cell = document.createElement("dd");
      if (["Project ID", "Release ID"].includes(label)) cell.className = "plugins-identity-metadata";
      if (typeof value === "string") cell.textContent = value;
      else cell.append(value);
      row.append(cell);
      rows.append(row);
    };
  };
  const dependencyGroups = (dependencies) => {
    const groups = document.createElement("div");
    groups.className = "plugins-dependency-groups";
    for (const [type, label] of [["required", "Required"], ["optional", "Optional"], ["incompatible", "Incompatible"], ["other", "Embedded / other"]]) {
      const entries = dependencies.filter((dependency) => type === "other" ? !["required", "optional", "incompatible"].includes(dependency.dependency_type) : dependency.dependency_type === type);
      if (!entries.length) continue;
      const row = document.createElement("p");
      row.className = "plugins-publisher-dependency-row";
      row.append(text("strong", `${label} publisher dependencies`));
      const values = entries.map((dependency) => {
        const identity = dependency.name || dependency.title || dependency.file_name || dependency.project_id || "Unspecified";
        return dependency.version_id ? `${identity} (release ${dependency.version_id})` : identity;
      });
      row.append(text("span", values.join(" / "), "plugins-dependency-ids"));
      groups.append(row);
    }
    if (!dependencies.length) groups.append(text("span", "None listed"));
    return groups;
  };
  const renderProject = (project, server) => {
    const card = document.createElement("article");
    card.className = "plugins-result-card";
    card.append(text("h3", project.title), text("p", project.description, "plugins-card-description"));
    const version = project.version;
    const badges = document.createElement("div");
    badges.className = "plugins-card-badges";
    const platform = server.server_software || server.server_type;
    const installedState = project.installed_state === "enabled" ? "Enabled" : project.installed_state === "disabled" ? "Disabled" : "Needs attention";
    const identityBadges = [[version?.version_number, "plugins-badge-version"], [platform, "plugins-badge-server"], [server.minecraft_version && `MC ${server.minecraft_version}`, "plugins-badge-minecraft"]];
    if (project.installed) {
      identityBadges.push(["Installed", "plugins-badge-installed"], [installedState, project.installed_state === "enabled" ? "plugins-badge-enabled" : project.installed_state === "disabled" ? "plugins-badge-disabled" : "plugins-badge-attention"]);
    }
    identityBadges.filter(([value]) => value).forEach((badge) => badges.append(pluginBadge(...badge)));
    if (!version || project.metadata_error) card.append(text("p", "Release metadata unavailable", "plugins-card-meta"));
    card.append(badges);
    const dependencies = version?.dependencies || [];
    if (dependencies.length) {
      const counts = new Map();
      dependencies.forEach((dependency) => {
        const kind = ["required", "optional", "incompatible", "embedded"].includes(dependency.dependency_type) ? dependency.dependency_type : "unspecified";
        counts.set(kind, (counts.get(kind) || 0) + 1);
      });
      card.append(text("p", Array.from(counts, ([kind, count]) => `${count} ${kind}`).join(" · "), "plugins-card-meta"));
    }
    const renderInfo = () => {
      overlayHeader(project.title, project.description, identityBadges, iconProjectID, project.installed && !managed ? installedStatus() : null, project.icon_url || "", project.source);
      const about = infoRows(overlaySection("About this version"));
      about("Release", version?.version_number || "Unavailable");
      about(project.source === "Hangar" ? "Channel" : "Release type", version?.version_type || "Not provided");
      about("Published", version?.date_published || "Not provided");
      about("Source", project.source);
      about("Project ID", project.project_id);
      about("Release ID", version?.id || "Unavailable");
      const compatibility = infoRows(overlaySection("Compatibility"));
      compatibility("Result", project.compatibility);
      compatibility("Server software", platform || "Unknown");
      compatibility("Active Minecraft version", server.minecraft_version || "Unknown");
      compatibility("Supported platforms", (version?.loaders || []).join(" / ") || "Not provided");
      const versions = [...new Set(version?.game_versions || [])];
      compatibility("Supported versions", versions.join(" / ") || "Not provided");
      overlaySection("Dependencies").append(dependencyGroups(dependencies));
      if (project.installed) {
        const management = infoRows(overlaySection("Management"));
        management("Ownership", "Managed by Plugins");
        management("State", installedState);
        overlaySection("Update status").append(text("p", "Updates are not available yet"));
      }
      if (project.metadata_error || !version) panelBody.append(text("p", project.metadata_error || "Release metadata unavailable.", "plugins-card-meta"));
    };
    const iconProjectID = ["Modrinth", "Hangar"].includes(project.source) ? project.project_id : undefined;
    const managed = project.source === "Modrinth" && ["geyser", "floodgate", "viaversion"].includes(project.slug);
    if (managed) badges.append(text("span", "JustVoxel managed", "plugins-badge"));
    const installable = ["Modrinth", "Hangar"].includes(project.source) && !managed && !project.installed && project.install_available && project.compatible && server.supported && server.minecraft_version && version?.id;
    let reviewRejection = "";
    const readOnlyReview = (reason) => {
      reviewRejection = reason;
      showPanel(project.title, true);
      renderInfo();
      if (reason) overlaySection("Installation").append(text("p", reason));
      overlayActions(panelAction("Close", closePanel));
    };
    if (installable) {
      interactiveCard(card, project.title, () => reviewRejection ? readOnlyReview(reviewRejection) : reviewPlugin(project.project_id, version.id, project, readOnlyReview), iconProjectID, project.icon_url || "", project.source);
    } else {
      addInfo(card, project.title, () => {
        renderInfo();
        if (managed) infoRows(overlaySection("Management"))("Status", "JustVoxel managed");
      }, iconProjectID, project.icon_url || "", project.source);
    }
    // Publisher data uses textContent; artifact URLs never reach this browser.
    return card;
  };
  const renderInstalled = (plugin) => {
    const card = document.createElement("article");
    card.className = "plugins-result-card";
    const name = plugin.name || plugin.filename;
    const projectID = plugin.project_id || plugin.icon_project_id || undefined;
    const management = plugin.management === "justvoxel" ? "JustVoxel managed" : plugin.management === "plugins" ? "Managed by Plugins" : "Manual / unmanaged";
    card.append(text("h3", name), text("p", `Version: ${plugin.version || "Unknown"}`, "plugins-card-release"));
    const badges = document.createElement("div");
    badges.className = "plugins-card-badges";
    badges.append(text("span", management, "plugins-badge plugins-management"));
    const managed = plugin.management === "plugins" && ["enabled", "disabled"].includes(plugin.state);
    const stateLabel = managed ? (plugin.state === "disabled" ? "Disabled" : "Enabled") : "";
    const stateClass = plugin.state === "disabled" ? "plugins-badge-disabled" : "plugins-badge-enabled";
    if (plugin.management === "plugins") badges.append(pluginBadge("Installed", "plugins-badge-installed"));
    if (stateLabel) badges.append(pluginBadge(stateLabel, `plugins-state ${stateClass}`));
    card.append(badges);
    const attention = plugin.update_status?.startsWith("Attention:");
    if (attention) badges.append(pluginBadge("Needs attention", "plugins-badge-attention"));
    const updateAvailable = plugin.update_status?.startsWith("Update available");
    const updateStatus = updateAvailable ? plugin.update_status : attention ? plugin.update_status.replace(/; manual \/ unmanaged$/, "") : plugin.management === "justvoxel" ? "Via server updates" : plugin.management === "plugins" ? "Updates are not available yet" : "Unknown";
    addInfo(card, name, () => {
      overlayHeader(name, "", [[plugin.version || "Version unknown", "plugins-badge-version"], management, [plugin.management === "plugins" && "Installed", "plugins-badge-installed"], [stateLabel, stateClass], [attention && "Needs attention", "plugins-badge-attention"]], projectID, plugin.management === "plugins" ? installedStatus() : null, undefined, plugin.source || "Modrinth");
      overlaySection("JAR").append(text("p", plugin.filename));
      overlaySection("Metadata").append(text("p", plugin.metadata_status || "Unknown"));
      overlaySection("Management").append(text("p", plugin.management === "justvoxel" ? "Generic Install is unavailable for this component." : plugin.management === "plugins" ? "Lifecycle actions verify the exact recorded JAR. Plugin configuration and data are preserved." : "Plugins will not adopt or overwrite this JAR."));
      overlaySection("Update status").append(text("p", updateStatus));
      if (plugin.project_id || plugin.version_id) {
        const identity = infoRows(overlaySection("Version details"));
        if (plugin.project_id) identity("Project ID", plugin.project_id);
        if (plugin.version_id) identity("Release ID", plugin.version_id);
      }
      if (managed && !attention) {
        const action = plugin.state === "disabled" ? "enable" : "disable";
        return [pluginAction(action === "enable" ? "Enable" : "Disable", () => reviewManagedPlugin(plugin, action), "plugins-state-action"), pluginAction("Remove", () => reviewManagedPlugin(plugin, "remove"), "plugins-remove-action")];
      }
    }, projectID, undefined, plugin.source || "Modrinth");
    let summary = plugin.metadata_status === "Metadata read" ? "Descriptor available" : "Metadata unavailable";
    if (updateAvailable) summary = plugin.update_status;
    if (attention) summary = "Needs attention";
    const status = text("p", summary, "plugins-inventory-status");
    if (attention) status.classList.add("is-attention");
    card.append(status);
    return card;
  };
  // Review, progress, Info and completion reuse a single bounded overlay.
  const panel = document.createElement("section");
  panel.className = "plugins-install-panel";
  panel.id = "plugins-overlay";
  panel.tabIndex = -1;
  panel.hidden = true;
  panel.setAttribute("role", "dialog");
  panel.setAttribute("aria-label", "Plugin installation");
  panel.setAttribute("aria-modal", "true");
  const panelBody = document.createElement("div");
  panelBody.className = "plugins-install-content";
  panel.append(panelBody);
  pluginsDialog.append(panel);
  let installGeneration = 0, pollTimer = null, installController = null, returnFocus = null;
  let operationIdentity = null;
  let operationViewID = null;
  let searchTimer = null, inventoryTimer = null;
  const clearInventoryTimer = () => { clearTimeout(inventoryTimer); inventoryTimer = null; };
  const pluginOperationSettled = (operation) => !operation || ["succeeded", "rolled_back", "resolved"].includes(operation.state);
  let overlayDismissible = false;
  const stopPolling = () => { clearTimeout(pollTimer); pollTimer = null; installController?.abort(); ++installGeneration; };
  const workspaceShell = pluginsDialog.querySelector(".plugins-workspace-shell");
  const closePanel = () => {
    stopPolling();
    panel.hidden = true;
    results.querySelectorAll(".plugins-result-card").forEach((node) => {
      node.classList.remove("is-selected");
      node.setAttribute("aria-pressed", "false");
    });
    operationViewID = null;
    overlayDismissible = false;
    workspaceShell.inert = false;
    if (returnFocus?.isConnected) returnFocus.focus();
    else pluginsDialog.querySelector('[data-plugins-tab][aria-selected="true"]')?.focus();
  };
  const showPanel = (label = "Plugin installation", dismissible = false, preserveIdentity = false) => {
    overlayDismissible = dismissible;
    const focusedLabel = panel.contains(document.activeElement) ? document.activeElement.textContent : null;
    if (panel.hidden) returnFocus = document.activeElement;
    panel.setAttribute("aria-label", label);
    panel.hidden = false;
    workspaceShell.inert = true;
    if (preserveIdentity) {
      // Retain the identity header (including its image) for this operation.
      Array.from(panelBody.children).slice(1).forEach((node) => node.remove());
    } else {
      operationViewID = null;
      panelBody.replaceChildren();
    }
    // Preserve keyboard position when authoritative operation polling rerenders.
    queueMicrotask(() => {
      if (panel.hidden) return;
      const target = focusedLabel && Array.from(panel.querySelectorAll("button,summary")).find((node) => node.textContent === focusedLabel);
      (target || panel).focus();
    });
  };
  const panelAction = (label, action, secondary = false) => {
    const button = text("button", label, secondary ? "secondary" : "");
    button.type = "button";
    button.addEventListener("click", action);
    return button;
  };
  const pluginAction = (label, action, style) => {
    const button = panelAction(label, action);
    button.classList.add(style);
    return button;
  };
  const returnToInstalled = () => {
    closePanel();
    pluginsDialog.querySelector('[data-plugins-tab="installed"]').click();
  };
  const reviewManagedPlugin = async (plugin, action) => {
    stopPolling();
    const current = installGeneration;
    installController = new AbortController();
    showPanel("Review plugin action", true);
    panelBody.append(text("p", "Preparing the exact managed plugin action…"));
    try {
      if (await blockPluginInstall(current)) return;
      const plan = await pluginRequest("manage/plan", { source: plugin.source || "Modrinth", action, project_id: plugin.project_id }, installController.signal);
      if (current !== installGeneration) return;
      showPanel("Review plugin action", true);
      const label = action === "enable" ? "Enable" : action === "disable" ? "Disable" : "Remove";
      overlayHeader(plan.project_name, "", [[plugin.version, "plugins-badge-version"], [plan.state === "disabled" ? "Disabled" : "Enabled", plan.state === "disabled" ? "plugins-badge-disabled" : "plugins-badge-enabled"]], plan.project_id, null, undefined, plan.source);
      const explanation = action === "enable"
        ? "The same verified stored JAR returns to /plugins. Plugin configuration/data is preserved. Minecraft restart and active-plugin verification follow."
        : action === "disable"
          ? "The plugin JAR will leave the active /plugins set. Plugin configuration/data is preserved. Minecraft restart is required when currently running; startup verification also runs when stopped."
          : "The exact managed JAR will be removed. Plugin configuration/data will remain. Dependencies are not automatically removed. Active plugin removal requires Minecraft restart; stopped-server verification may also start Minecraft temporarily.";
      panelBody.append(text("p", explanation));
      let players = null;
      if (plan.confirmation_required) {
        const row = text("label", "", "plugins-player-confirm");
        players = document.createElement("input"); players.type = "checkbox";
        row.append(players, text("span", "I understand online players will be disconnected."));
        panelBody.append(text("p", `${plan.online} player(s) are online${Array.isArray(plan.players) && plan.players.length ? ` (${plan.players.join(", ")})` : ""}. This action will disconnect them.`));
        panelBody.append(row);
      }
      const apply = pluginAction(label, async () => {
        apply.disabled = true;
        try {
          if (await blockPluginInstall(current)) return;
          const payload = await pluginRequest("manage", { source: plan.source || "Modrinth", action: plan.action, project_id: plan.project_id, plan_fingerprint: plan.plan_fingerprint, confirm_players: String(Boolean(players?.checked)) }, installController.signal);
          if (current !== installGeneration) return;
          operationIdentity = { operation_id: payload.operation.operation_id, source: plugin.source, project_id: plugin.project_id, name: plugin.name, version: plugin.version };
          if (!showOperation(payload.operation)) pollTimer = setTimeout(() => pollOperation(payload.operation.operation_id, current), 1000);
        } catch (error) { if (current === installGeneration && error.name !== "AbortError") panelError(error); }
      }, action === "remove" ? "plugins-remove-action" : "plugins-state-action");
      let slider = null, finalConfirm = null;
      if (action === "remove") {
        const arm = text("div", "", "destructive-confirmation");
        const shell = text("div", "", "destructive-confirm-slider");
        const armedText = text("span", "Slide to confirm removal", "destructive-confirm-slider-text");
        const thumb = text("span", ">", "destructive-confirm-slider-thumb"); thumb.setAttribute("aria-hidden", "true");
        slider = document.createElement("input"); slider.type = "range"; slider.min = "0"; slider.max = "100"; slider.step = "1"; slider.value = "0"; slider.setAttribute("aria-label", "Slide to confirm removal");
        shell.append(armedText, thumb, slider);
        const finalLabel = text("label", "", "destructive-confirm-toggle-row"); finalLabel.hidden = true;
        finalConfirm = document.createElement("input"); finalConfirm.type = "checkbox"; finalConfirm.className = "destructive-confirm-toggle";
        finalLabel.append(text("span", "Confirm >", "destructive-confirm-toggle-callout"), finalConfirm);
        arm.append(shell, finalLabel); panelBody.append(arm);
        slider.addEventListener("input", () => {
          const progress = Math.max(0, Math.min(100, Number(slider.value || 0)));
          shell.style.setProperty("--confirm-progress", String(progress / 100));
          shell.classList.toggle("is-armed", progress >= 100);
          finalLabel.hidden = progress < 100;
          armedText.textContent = progress >= 100 ? "Removal armed" : "Slide to confirm removal";
          if (progress < 100) finalConfirm.checked = false;
          sync();
        });
        finalConfirm.addEventListener("change", () => sync());
      }
      const sync = () => { apply.disabled = Boolean(players && !players.checked) || Boolean(slider && (Number(slider.value) !== 100 || !finalConfirm.checked)); };
      players?.addEventListener("change", sync);
      sync();
      overlayActions(panelAction("Cancel", returnToInstalled, true), apply);
    } catch (error) { if (current === installGeneration && error.name !== "AbortError") panelError(error); }
  };
  const pluginRequest = async (route, body, signal) => {
    const options = { credentials: "same-origin", cache: "no-store", headers: { Accept: "application/json" }, signal };
    if (body) {
      options.method = "POST";
      options.body = new URLSearchParams({ csrf: document.querySelector('[data-minecraft-workspace-csrf]')?.value || document.querySelector('input[name="csrf"]')?.value || "", ...body });
    }
    const response = await fetch(`/api/plugins/workspace/${route}`, options);
    const payload = await response.json();
    if (response.status === 401) { window.location.assign("/login"); throw new Error("Sign in again."); }
    if (response.status === 403 && String(payload.error || "").toLowerCase().includes("password")) { window.location.assign("/password"); throw new Error("Change your password first."); }
    if (!response.ok) throw new Error(payload.error || payload.message || "Plugin installation is unavailable; review again.");
    return payload;
  };
  const panelError = (error) => {
    showPanel();
    panelBody.append(text("h3", "Plugin operation could not continue"), text("p", error.message || "Status is unavailable. Reopen Plugins to check the persistent operation."));
    panelBody.append(text("p", "See System -> Logs -> Plugins for diagnostic details."));
    overlayActions(panelAction("Done", closePanel, true));
  };
  const showAddonOperation = (operation) => {
    const terminal = ["succeeded", "rolled_back", "needs_attention", "resolved"].includes(operation.state);
    showPanel("Add-ons installation", false);
    panelBody.append(text("h3", "Add-ons installation"), text("p", operation.status, "plugins-operation-status"));
    if (operation.state === "needs_attention") {
      const feedback = text("p", "Retry recovery verifies the preserved file and Minecraft state.");
      const retry = panelAction("Retry recovery", async () => {
        retry.disabled = true;
        try {
          const payload = await pluginRequest("addons/recover", { operation_id: operation.operation_id }, installController?.signal);
          showAddonOperation(payload.operation);
          load();
        } catch (error) { feedback.textContent = error.message; retry.disabled = false; }
      });
      panelBody.append(feedback);
      overlayActions(retry, panelAction("Close", closePanel, true));
    } else overlayActions(panelAction(terminal ? "Done" : "Close", () => { closePanel(); load(); }, true));
    return terminal;
  };
  const showOperation = (operation) => {
    if (operation.operation_type === "plugin_addon_install") return showAddonOperation(operation);
    const sameOperation = !panel.hidden && operationViewID === operation.operation_id;
       const lifecycle = operation.operation_type !== "plugin_install";
    showPanel(lifecycle ? "Plugin management" : "Plugin installation", false, sameOperation);
    const success = operation.state === "succeeded";
    const attention = operation.state === "needs_attention";
    const terminal = ["succeeded", "rolled_back", "needs_attention", "resolved"].includes(operation.state);
    const identity = operationIdentity?.operation_id === operation.operation_id ? operationIdentity : null;
    const heading = lifecycle ? (success ? "Plugin action verified" : attention ? "Administrator attention required" : operation.state === "rolled_back" ? "Plugin action did not complete" : operation.state === "resolved" ? "Plugin state verified" : "Changing managed plugin") : success ? "Plugin installation verified" : attention ? "Administrator attention required" : operation.state === "rolled_back" ? "Installation did not complete" : operation.state === "resolved" ? "Plugin state verified" : "Installing plugin";
    if (!sameOperation) {
      overlayHeader(identity?.name || heading, "", [[identity?.version, "plugins-badge-version"]], identity?.project_id, null, undefined, identity?.source || "Modrinth");
      operationViewID = operation.operation_id;
    } else if (!identity) panelBody.querySelector(".plugins-overlay-identity h3").textContent = heading;
    if (identity) panelBody.append(text("h3", heading));
    if (!terminal) {
      const stages = lifecycle ? ["Prepare", "Backup", "Change", "Restart", "Verify"] : ["Prepare", "Backup", "Install", "Restart", "Verify"];
      const index = ({ queued: 0, validating: 0, downloading: 0, player_check: 0, backup: 1, installing: 2, changing: 2, restarting: 3, verifying: 4 })[operation.stage];
      const sequence = document.createElement("ol");
      sequence.className = "plugins-install-stages";
      stages.forEach((label, position) => {
        const item = text("li", label);
        if (position === index) item.setAttribute("aria-current", "step");
        if (index !== undefined && position < index) item.className = "is-complete";
        sequence.append(item);
      });
      panelBody.append(sequence);
    }
    // The persisted status is the single source of result and final service state.
    const status = text("p", operation.status, "plugins-operation-status");
    status.setAttribute("role", "status");
    panelBody.append(status);
    if (attention || operation.state === "rolled_back" || operation.state === "failed") {
      panelBody.append(text("p", "See System -> Logs -> Plugins for diagnostic details."));
      if (window.JustVoxelSystem?.openPluginLogs) panelBody.append(panelAction("Open Plugins logs", () => { closePanel(); load(); window.JustVoxelSystem.openPluginLogs(operation.operation_id); }, true));
    }
    if (success) {
      const actions = overlayActions(panelAction("View in Installed", () => {
        closePanel();
        pluginsDialog.querySelector('[data-plugins-tab="installed"]').click();
      }, true), panelAction("Done", () => { closePanel(); load(); }));
      if (lifecycle) actions.lastElementChild.classList.add("plugins-done-action");
    }
    if (attention) {
      panelBody.append(text("p", "Recovery remains available through Re-check recovery. Maintenance remains reserved until verification succeeds.", "plugins-install-warning"));
      const feedback = text("p", "", "plugins-operation-feedback");
      const recheck = panelAction("Re-check recovery", async () => {
        recheck.disabled = true;
        feedback.textContent = "Re-checking plugin and Minecraft state…";
        const current = installGeneration;
        try {
          const payload = await pluginRequest(lifecycle ? "manage/acknowledge" : "install/acknowledge", { operation_id: operation.operation_id }, installController?.signal);
          if (current !== installGeneration) return;
          if (!payload.operation || payload.operation.state !== "resolved") throw new Error("Plugin state could not be resolved.");
          showOperation(payload.operation);
          load();
        } catch (error) {
          if (current === installGeneration && error.name !== "AbortError") {
            feedback.textContent = error.message || "Re-check failed. Evidence and maintenance exclusion were preserved.";
            recheck.disabled = false;
          }
        }
      });
      overlayActions(recheck, panelAction("Close", closePanel, true));
      panelBody.append(feedback);
    } else if (terminal && !success) overlayActions(panelAction("Done", () => { closePanel(); load(); }, true));
    else if (!terminal) overlayActions(panelAction("Close", closePanel, true));
    return terminal;
  };
  const pollOperation = async (id, current) => {
    if (current !== installGeneration) return;
    try {
      const payload = await pluginRequest(`operation?id=${encodeURIComponent(id)}`, null, installController?.signal);
      if (current !== installGeneration) return;
      if (!payload.operation) throw new Error("Installation status is unavailable.");
      if (!showOperation(payload.operation)) pollTimer = setTimeout(() => pollOperation(id, current), 1500);
    } catch (error) { if (current === installGeneration && error.name !== "AbortError") panelError(error); }
  };
  const resumeActiveOperation = (operation, current) => {
    if (!showOperation(operation)) pollTimer = setTimeout(() => pollOperation(operation.operation_id, current), 1000);
  };
  const blockPluginInstall = async (current) => {
    const payload = await pluginRequest("operation", null, installController.signal);
    if (current !== installGeneration) return true;
    if (pluginOperationSettled(payload.operation)) return false;
    resumeActiveOperation(payload.operation, current);
    return true;
  };
  const applyPlugin = async (plan) => {
    stopPolling();
    const current = installGeneration;
    installController = new AbortController();
    showPanel();
    panelBody.append(text("h3", "Submitting reviewed installation…"));
    try {
      if (await blockPluginInstall(current)) return;
      const payload = await pluginRequest("install", {
        source: plan.source, project_id: plan.project_id, version_id: plan.version_id, plan_fingerprint: plan.plan_fingerprint,
        confirm_players: String(plan.confirmation_required === true),
      }, installController.signal);
      if (current !== installGeneration) return;
      if (!payload.operation) throw new Error("Installation did not start. Review again.");
      operationIdentity = { operation_id: payload.operation.operation_id, source: plan.source, project_id: plan.project_id, name: plan.project_name, version: plan.version_number };
      if (!showOperation(payload.operation)) pollTimer = setTimeout(() => pollOperation(payload.operation.operation_id, current), 1000);
    } catch (error) { if (current === installGeneration && error.name !== "AbortError") panelError(error); }
  };
  const readableSize = (size) => {
    if (!Number.isFinite(size) || size < 0) return "Size unavailable";
    if (size < 1024) return `${size} B`;
    const unit = size >= 1048576 ? "MiB" : "KiB";
    const value = size / (unit === "MiB" ? 1048576 : 1024);
    return `${Number(value.toFixed(1))} ${unit}`;
  };
  const reviewPlugin = async (projectID, versionID, project, readOnlyReview) => {
    if (project?.installed) { readOnlyReview?.(); return; }
    stopPolling();
    const current = installGeneration;
    installController = new AbortController();
    showPanel("Plugin installation", true);
    panelBody.append(text("h3", "Preparing installation review…"));
    overlayActions(panelAction("Close", closePanel, true));
    try {
      if (await blockPluginInstall(current)) return;
      const plan = await pluginRequest("install/plan", { source: project?.source || "Modrinth", project_id: projectID, version_id: versionID }, installController.signal);
      if (current !== installGeneration) return;
      showPanel("Plugin installation", true);
      overlayHeader(plan.project_name, plan.description || project?.description, [[plan.version_number, "plugins-badge-version"], [plan.server.server_software || plan.server.server_type, "plugins-badge-server"], [plan.server.minecraft_version && `MC ${plan.server.minecraft_version}`, "plugins-badge-minecraft"]], plan.project_id, panelAction("Install", () => applyPlugin(plan)), project?.project_id === plan.project_id ? project.icon_url || "" : undefined, plan.source);
      const selectedVersion = project?.version?.id === plan.version_id ? project.version : null;
      const about = infoRows(overlaySection("About this version"));
      about("Release", plan.version_number);
      about(plan.source === "Hangar" ? "Channel" : "Release type", selectedVersion?.version_type || "Not provided");
      about("Published", selectedVersion?.date_published || "Not provided");
      about("Source", plan.source);
      about("Project ID", plan.project_id);
      about("Release ID", plan.version_id);
      const compatibility = infoRows(overlaySection("Compatibility"));
      compatibility("Result", plan.compatibility);
      compatibility("Server software", plan.server.server_software);
      compatibility("Active Minecraft version", plan.server.minecraft_version);
      compatibility("Supported platforms", (selectedVersion?.loaders || []).join(" / ") || "Not provided");
      const versions = [...new Set(selectedVersion?.game_versions || [])];
      compatibility("Supported versions", versions.join(" / ") || "Not provided");
      const dependencies = overlaySection("Dependencies");
      const required = plan.artifacts.filter((artifact) => !artifact.requested);
      const dependencyList = document.createElement("ul");
      dependencyList.className = "plugins-overlay-list";
      required.forEach((artifact) => {
        const item = text("li", `${artifact.project_name} · ${artifact.version_number}`);
        item.append(text("span", artifact.satisfied ? "Already satisfied" : "Will be installed", "plugins-list-secondary"));
        item.className = "plugins-file-row";
        dependencyList.append(item);
      });
      dependencies.append(dependencyList);
      const informational = plan.display_dependencies || [];
      if (informational.length) dependencies.append(dependencyGroups(informational));
      if (!required.length && !informational.length) dependencies.append(text("p", "No required dependencies."));
      const files = document.createElement("ul");
      files.className = "plugins-overlay-list";
      plan.artifacts.filter((artifact) => !artifact.satisfied).forEach((artifact) => {
        const item = text("li", artifact.filename, "plugins-file-row");
        item.append(text("span", readableSize(artifact.size), "plugins-list-secondary"));
        files.append(item);
      });
      overlaySection("Files to install").append(files);
      const installation = infoRows(overlaySection("Installation"));
      installation("Target plugin directory", plan.installation_location);
      installation("Changes", "Verified cold backup before changes; then install the listed files.");
      const restart = infoRows(overlaySection("Restart"));
      restart("Required", plan.restart_required ? "Yes" : "No");
      restart("Verification", "Minecraft starts/restarts to load and verify plugins");
      restart("Original stopped state", "Returns to stopped after verification");
      if (plan.confirmation_required) {
        const section = overlaySection("Players");
        const players = infoRows(section);
        players("Online", String(plan.online));
        if (plan.players?.length) players("Names", plan.players.join(", "));
        players("Install", "Confirms the player-aware shutdown and restart.");
      }
      panelBody.querySelector("button")?.focus();
    } catch (error) {
      if (current !== installGeneration || error.name === "AbortError") return;
      if (error.message === "This release is not a direct Paper/Bukkit plugin and cannot be installed into the plugins directory." && readOnlyReview) readOnlyReview("The publisher lists Paper support, but this JAR is not a direct Paper/Bukkit plugin. It may require an external loader or a different installation path, so JustVoxel will not place it in the plugins directory.");
      else if (error.message === "BlueMap native add-on belongs in Plugins -> Add-ons -> BlueMap Add-ons. Select its local file there." && readOnlyReview) readOnlyReview(error.message);
      else if (error.message === "Loader-based server mod - not supported by the current version of JustVoxel Plugin installer. Should be available in future updates." && readOnlyReview) readOnlyReview("Loader-based server mod - not supported by the current version of JustVoxel Plugin installer. Should be available in future updates.");
      else panelError(error);
    }
  };
  const resumePluginInstall = async () => {
    const requestedGeneration = installGeneration;
    try {
      const payload = await pluginRequest("operation");
      if (!pluginsDialog.open || pluginOperationSettled(payload.operation) || requestedGeneration !== installGeneration || !panel.hidden) return;
      stopPolling();
      installController = new AbortController();
      const current = installGeneration;
      if (!showOperation(payload.operation)) pollTimer = setTimeout(() => pollOperation(payload.operation.operation_id, current), 1000);
    } catch (error) { state.textContent = error.message || "Plugin installation status is unavailable."; }
  };
  panel.addEventListener("click", (event) => {
    if (event.target === panel && overlayDismissible) closePanel();
  });
  panel.addEventListener("keydown", (event) => {
    if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); closePanel(); }
    if (event.key === "Tab") {
      const buttons = Array.from(panel.querySelectorAll("button:not(:disabled),summary"));
      const first = buttons[0], last = buttons.at(-1);
      if (document.activeElement === panel) { event.preventDefault(); (event.shiftKey ? last : first)?.focus(); }
      else if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
    }
  });
  const pluginInventorySafe = pluginOperationSettled;
  const recheckBlockedInventory = async (current) => {
    inventoryTimer = null;
    if (!pluginsDialog.open || tab === "discover" || current !== generation) return;
    try {
      const payload = await pluginRequest("operation", null, controller.signal);
      if (!pluginsDialog.open || tab === "discover" || current !== generation) return;
      if (pluginInventorySafe(payload.operation)) { load(); return; }
    } catch (error) {
      if (current !== generation || error.name === "AbortError") return;
      state.textContent = error.message || "Plugin installation status is unavailable.";
    }
    inventoryTimer = setTimeout(() => recheckBlockedInventory(current), 1500);
  };
  const discoverQuery = (position) => new URLSearchParams({ q: form.elements.q.value, compatible_only: String(form.elements.compatible_only.checked), release_only: String(form.elements.release_only.checked), modrinth: String(form.elements.modrinth.checked), hangar: String(form.elements.hangar.checked), modrinth_offset: String(position.modrinth_offset), hangar_offset: String(position.hangar_offset), hangar_first: String(position.hangar_first), limit: String(pageLimit) });
  // Compare authoritative context before accepting cursor history from any response.
  const acceptDiscoverContext = (server) => {
    const key = JSON.stringify(server);
    const changed = discoverServerKey !== null && discoverServerKey !== key;
    discoverServerKey = key;
    if (changed) resetCursors();
    return !changed;
  };
  const addonRequest = async (route, kind, file, plan = null, confirmPlayers = false) => {
    const body = new FormData();
    body.append("type", kind);
    body.append("file", file, file.name);
    if (plan) {
      body.append("plan_fingerprint", plan.plan_fingerprint);
      body.append("confirm_players", String(confirmPlayers));
    }
    const response = await fetch(`/api/plugins/workspace/addons/${route}`, {
      method: "POST", credentials: "same-origin", cache: "no-store", body,
      signal: installController?.signal,
      headers: { Accept: "application/json", "X-CSRF-Token": document.querySelector('[data-minecraft-workspace-csrf]')?.value || document.querySelector('input[name="csrf"]')?.value || "" },
    });
    const payload = await response.json();
    if (response.status === 401) { window.location.assign("/login"); throw new Error("Sign in again."); }
    if (!response.ok) throw new Error(payload.error || "Add-ons upload could not continue; review again.");
    return payload;
  };
  const reviewAddon = async (section, file) => {
    stopPolling();
    const current = installGeneration;
    installController = new AbortController();
    showPanel("Review Add-ons installation", true);
    panelBody.append(text("p", "Checking the selected file and its host…"));
    try {
      if (await blockPluginInstall(current)) return;
      const plan = await addonRequest("plan", section.kind, file);
      if (current !== installGeneration) return;
      showPanel("Review Add-ons installation", true);
      overlayHeader(plan.display_name || plan.filename, plan.validation, [[section.name, ""]]);
      const rows = infoRows(overlaySection("Installation"));
      rows("Type", section.name);
      rows("File", plan.filename);
      rows("Size", readableSize(plan.size));
      rows("Destination", plan.destination);
      rows("Restart required", plan.restart_required ? "Yes — verified cold backup, restart and Minecraft health check" : "No — passive schematic content");
      if (plan.restart_required) rows("Originally stopped", "Minecraft returns to stopped after health verification");
      let players = null;
      if (plan.confirmation_required) {
        const label = document.createElement("label");
        players = document.createElement("input");
        players.type = "checkbox";
        label.append(players, document.createTextNode(` Confirm restart with ${plan.online} player(s) online${plan.players?.length ? ": " + plan.players.join(", ") : ""}.`));
        overlaySection("Players").append(label);
      }
      const apply = panelAction("Install", async () => {
        apply.disabled = true;
        try {
          // The exact selected File is sent again; the Agent revalidates everything.
          const payload = await addonRequest("install", section.kind, file, plan, Boolean(players?.checked));
          if (current !== installGeneration) return;
          if (!payload.operation) throw new Error("Add-ons installation did not start.");
          if (!showOperation(payload.operation)) pollTimer = setTimeout(() => pollOperation(payload.operation.operation_id, current), 1000);
        } catch (error) { if (current === installGeneration && error.name !== "AbortError") panelError(error); }
      });
      apply.disabled = Boolean(players && !players.checked);
      players?.addEventListener("change", () => { apply.disabled = !players.checked; });
      overlayActions(panelAction("Cancel", closePanel, true), apply);
    } catch (error) { if (current === installGeneration && error.name !== "AbortError") panelError(error); }
  };
  const renderAddonSection = (section) => {
    const card = document.createElement("article");
    card.className = "plugins-result-card";
    card.append(text("h3", section.name));
    const badges = document.createElement("div");
    badges.className = "plugins-card-badges";
    badges.append(pluginBadge(section.host.available ? "Host installed" : section.host.status === "disabled" ? "Host disabled" : "Host unavailable", section.host.available ? "plugins-badge-installed" : section.host.status === "disabled" ? "plugins-badge-disabled" : "plugins-badge-attention"));
    card.append(badges, text("p", section.host.message, "plugins-card-description"), text("p", section.destination, "plugins-card-release"));
    for (const item of section.files) {
      const content = document.createElement("section");
      content.className = "plugins-overlay-section";
      content.append(text("h4", item.display_name || item.filename), text("p", item.filename));
      const managed = document.createElement("div");
      managed.className = "plugins-card-badges";
      managed.append(pluginBadge(section.name), pluginBadge(item.state === "installed" ? "Installed" : "Needs attention", item.state === "installed" ? "plugins-badge-installed" : "plugins-badge-attention"), pluginBadge("Managed by Add-ons"));
      content.append(managed, text("p", item.target_relative_path), text("p", "Updates are not available yet"));
      card.append(content);
    }
    const picker = document.createElement("input");
    picker.type = "file";
    picker.accept = section.accept;
    picker.setAttribute("aria-label", `Choose file for ${section.name}`);
    picker.disabled = !section.host.available;
    const choose = document.createElement("label");
    choose.append(document.createTextNode("Choose file"), picker);
    const review = panelAction("Review / Install", () => { if (picker.files?.length === 1) reviewAddon(section, picker.files[0]); });
    review.disabled = true;
    picker.addEventListener("change", () => { review.disabled = !section.host.available || picker.files?.length !== 1; });
    card.append(choose, review);
    return card;
  };
  const load = async () => {
    if (!document.body.classList.contains("role-administrator")) return;
    clearInventoryTimer();
    const current = ++generation;
    controller?.abort();
    controller = new AbortController();
    state.textContent = "Loading plugins…";
    results.replaceChildren();
    results.classList.toggle("plugins-addon-results", tab === "addons");
    pagination.hidden = tab !== "discover";
    previous.disabled = true;
    next.disabled = true;
    form.hidden = tab !== "discover";
    const query = discoverQuery(cursor);
    try {
      if (tab !== "discover") {
        const installation = await pluginRequest("operation", null, controller.signal);
        if (current !== generation) return;
        if (!pluginInventorySafe(installation.operation)) {
          state.textContent = "Plugin operation is still being verified or recovered. Installed inventory will be available after the operation reaches a verified final state.";
          inventoryTimer = setTimeout(() => recheckBlockedInventory(current), 1500);
          return;
        }
      }
      const response = await fetch(`/api/plugins/workspace/${tab}${tab === "discover" ? "?" + query : ""}`, { credentials: "same-origin", cache: "no-store", headers: { Accept: "application/json" }, signal: controller.signal });
      const payload = await response.json();
      if (current !== generation) return;
      if (response.status === 401) { window.location.assign("/login"); return; }
      if (response.status === 403 && String(payload.error || "").toLowerCase().includes("password")) { window.location.assign("/password"); return; }
      if (!response.ok) throw new Error(payload.error || "Plugin information is unavailable.");
      const server = payload.server;
      if (tab === "discover" && !acceptDiscoverContext(server)) { load(); return; }
      context.textContent = `${server.server_software || "Minecraft"} · Minecraft ${server.minecraft_version || "version unavailable"}`;
      form.querySelectorAll("input,button").forEach((control) => { control.disabled = !server.supported; });
      if (!server.supported) { state.textContent = server.message; return; }
      if (tab === "addons") {
        payload.sections.forEach((section) => results.append(renderAddonSection(section)));
        state.textContent = "Select a local file. Host plugins perform runtime loading; updates and removal are not available yet.";
        return true;
      }
      const items = tab === "discover" ? payload.projects : payload.plugins;
      items.forEach((item) => results.append(tab === "discover" ? renderProject(item, server) : renderInstalled(item)));
      state.textContent = tab === "discover" && !payload.modrinth && !payload.hangar ? "Enable Modrinth or Hangar to browse plugins." : server.message || (items.length ? "" : tab === "discover" ? "No plugins match these filters on this page." : "No installed plugin JARs found.");
      if (tab === "discover") {
        cursor = payload.cursor;
        nextCursor = payload.next_cursor;
        pagination.hidden = false;
        previous.disabled = pageIndex === 0;
        const currentPage = pageIndex + 1;
        totalPages = Math.max(1, Math.ceil(payload.total / pageLimit));
        pageCursors[pageIndex] = cursor;
        if (nextCursor) pageCursors[pageIndex + 1] = nextCursor;
        else pageCursors.length = pageIndex + 1;
        next.disabled = !nextCursor;
        pageLabel.textContent = `Page ${currentPage} of ${totalPages}`;
      }
      return true;
    } catch (error) {
      if (current === generation && error.name !== "AbortError") state.textContent = error.message || "Plugin information is unavailable.";
    } finally {
      if (current === generation) {
        if (tab === "discover") {
          previous.disabled = pageIndex === 0;
          next.disabled = !nextCursor;
        }
      }
    }
  };
  const discoverCapacity = () => {
    const geometry = results.getBoundingClientRect();
    if (geometry.width <= 0 || geometry.height <= 0) return pageLimit;
    const style = getComputedStyle(results);
    const columns = style.gridTemplateColumns.split(" ").filter(Boolean).length;
    const cardHeight = parseFloat(style.getPropertyValue("--plugins-card-height"));
    const gap = parseFloat(style.rowGap) || 0;
    const rows = Math.max(1, Math.floor((results.clientHeight + gap) / (cardHeight + gap)));
    return Math.max(1, Math.min(12, columns * rows));
  };
  const updateCapacity = (reload = true) => {
    if (!pluginsDialog.open || tab !== "discover") return;
    const newLimit = discoverCapacity();
    if (newLimit === pageLimit) return;
    pageLimit = newLimit;
    resetCursors();
    if (reload) load();
  };
  const capacityObserver = new ResizeObserver(() => {
    clearTimeout(capacityTimer);
    capacityTimer = setTimeout(() => updateCapacity(), 200);
  });
  capacityObserver.observe(results);
  const workspaceWindow = setupWorkspaceWindow(pluginsDialog, { onOpen: () => { resetCursors(); updateCapacity(false); load(); resumePluginInstall(); }, onClose: () => { clearTimeout(capacityTimer); clearTimeout(searchTimer); clearInventoryTimer(); ++generation; controller?.abort(); resetCursors(); closePanel(); } });
  pluginsOpen.addEventListener("click", () => {
    if (!document.body.classList.contains("role-administrator")) return;
    const controlCenter = document.querySelector("[data-control-center]");
    if (controlCenter) controlCenter.open = false;
    workspaceWindow?.open();
  });
  pluginsDialog.querySelector("[data-plugins-close]").addEventListener("click", () => workspaceWindow?.close());
  pluginsDialog.querySelector("[data-plugins-refresh]").addEventListener("click", () => { resetCursors(); load(); });
  pluginsDialog.querySelectorAll("[data-plugins-tab]").forEach((button) => button.addEventListener("click", () => {
    clearTimeout(searchTimer);
    clearInventoryTimer();
    tab = button.dataset.pluginsTab;
    resetCursors();
    pluginsDialog.querySelectorAll("[data-plugins-tab]").forEach((node) => node.setAttribute("aria-selected", String(node === button)));
    updateCapacity(false);
    load();
  }));
  form.elements.q.addEventListener("input", () => {
    clearTimeout(searchTimer);
    ++generation;
    controller?.abort();
    resetCursors();
    previous.disabled = true;
    next.disabled = true;
    searchTimer = setTimeout(() => load(), 250);
  });
  form.addEventListener("submit", (event) => { event.preventDefault(); clearTimeout(searchTimer); resetCursors(); load(); });
  form.querySelectorAll('input[name="compatible_only"],input[name="release_only"],input[name="modrinth"],input[name="hangar"]').forEach((control) => control.addEventListener("change", () => { clearTimeout(searchTimer); resetCursors(); load(); }));
  previous.addEventListener("click", () => { if (pageIndex > 0) { cursor = pageCursors[--pageIndex]; load(); } });
  next.addEventListener("click", () => {
    if (!nextCursor) return;
    pageCursors[pageIndex + 1] = nextCursor;
    cursor = pageCursors[++pageIndex];
    load();
  });
  // Role discovery is asynchronous. Restore only after Administrator identity
  // has been established, matching the role-gated launcher.
  const restore = () => {
    if (document.body.classList.contains("role-pending")) return false;
    if (document.body.classList.contains("role-administrator") && readWorkspaceWindowState("plugins").open) workspaceWindow?.open();
    return true;
  };
  if (!restore()) {
    const observer = new MutationObserver(() => { if (restore()) observer.disconnect(); });
    observer.observe(document.body, { attributes: true, attributeFilter: ["class"] });
  }
}

// Bookmarked standalone pages land on the matching dashboard workspace.
if (dashboard) {
  const destination = new URLSearchParams(window.location.search);
  let workspace = destination.get("workspace");
  if (workspace === "minecraft" && window.location.hash === "#manual-backup") workspace = "backups";
  const launchers = {
    minecraft: "[data-minecraft-open]",
    system: "[data-system-open]",
    storage: "[data-storage-open]",
    backups: "[data-backups-open]",
    migration: "[data-migration-open]",
    version: "[data-version-open]",
    plugins: "[data-plugins-open]",
  };
  if (Object.hasOwn(launchers, workspace)) {
    const launch = () => {
      if (document.body.classList.contains("role-pending")) return false;
      const button = document.querySelector(launchers[workspace]);
      if (!button || button.classList.contains("nav-admin-only") && !document.body.classList.contains("role-administrator")) return true;
      button.click();
      let tab = destination.get("tab") || "";
      if (workspace === "minecraft") {
        if (window.location.hash === "#whitelist") tab = "players";
        if (window.location.hash === "#minecraft-logs") tab = "overview";
      }
      const allowedTabs = {
        minecraft: ["overview", "memory", "gameplay", "players", "crossplay"],
        version: ["software", "minecraft"],
        plugins: ["discover", "installed"],
        system: ["health", "history", "users", "security", "logs", "date-time", "wallpaper", "reset", "ups", "about"],
        migration: ["export", "import", "recovery"],
      };
      if (workspace !== "migration" && allowedTabs[workspace]?.includes(tab)) {
        const tabButton = document.querySelector(`[data-${workspace}-tab="${tab}"]`);
        const isAdmin = document.body.classList.contains("role-administrator");
        const isOperator = document.body.classList.contains("role-operator");
        if (tabButton && (!tabButton.classList.contains("nav-admin-only") || isAdmin) &&
            (!tabButton.classList.contains("nav-operator-plus") || isAdmin || isOperator)) tabButton.click();
      }
      return true;
    };
    if (!launch()) {
      const observer = new MutationObserver(() => {
        if (!launch()) return;
        observer.disconnect();
      });
      observer.observe(document.body, { attributes: true, attributeFilter: ["class"] });
    }
  }
}
