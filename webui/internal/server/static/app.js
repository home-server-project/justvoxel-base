
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
  const hash = window.location.hash;
  const brand = document.querySelector(".brand-link");
  if (brand) {
    if (path === "/") brand.setAttribute("aria-current", "page");
    else brand.removeAttribute("aria-current");
  }

  let currentGroup = "";
  let currentHref = "";
  if (path === "/activity") {
    currentGroup = "server";
    currentHref = "/activity";
  } else if (path === "/operations") {
    if (hash === "#manual-backup") {
      currentGroup = "storage";
      currentHref = "/operations#manual-backup";
    } else {
      currentGroup = "server";
      if (hash === "#whitelist") currentHref = "/operations#whitelist";
      if (hash === "#minecraft-logs") currentHref = "/operations#minecraft-logs";
    }
  } else if (path === "/settings/activity") {
    currentGroup = "administration";
    currentHref = "/settings/activity";
  } else if (path === "/settings/users") {
    currentGroup = "administration";
    currentHref = "/settings/users";
  } else if (path === "/settings/authentication") {
    currentGroup = "administration";
    currentHref = "/settings/authentication";
  } else if (path === "/password") {
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

  const renderPlayers = (players) => {
    const count = document.getElementById("players-panel-count");
    if (count) {
      count.textContent = players.configured ? `${players.online} / ${players.max}` : "";
    }

    const body = document.getElementById("players-panel-body");
    if (!body) return;
    body.replaceChildren();

    const message = (value) => {
      const p = document.createElement("p");
      p.className = "muted compact";
      p.textContent = value;
      body.appendChild(p);
    };

    if (players.state === "not_configured") {
      message("Minecraft is not configured yet.");
      return;
    }
    if (players.state === "stopped") {
      message("Minecraft is stopped. Player information will appear when the server is running.");
      return;
    }
    if (players.state === "unavailable") {
      message(players.error || "Player information is temporarily unavailable.");
      return;
    }
    if (players.online === 0) {
      message("No players online.");
      return;
    }
    if (Array.isArray(players.names) && players.names.length > 0) {
      const list = document.createElement("div");
      list.className = "player-list";
      players.names.forEach((name) => {
        const chip = document.createElement("span");
        chip.className = "player-chip";
        chip.textContent = name;
        list.appendChild(chip);
      });
      body.appendChild(list);
      return;
    }
    message(`${players.online} players online.`);
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

    const displayedState = pendingAction ? actionProgress(pendingAction) : status.minecraft.state;
    text("minecraft-state-summary", displayedState);
    text("minecraft-control-state", displayedState);
    text(
      "minecraft-players-summary",
      status.minecraft.configured ? `${players.online} / ${players.max}` : "Not configured",
    );
    text("minecraft-version-summary", status.minecraft.version);
    text("system-health-summary", status.system.health);
    text("system-ipv4-summary", status.system.ipv4);
    const overlaySummary = document.getElementById("system-overlay-summary");
    if (overlaySummary) {
      overlaySummary.replaceChildren();
      [["Tailscale", status.system.tailscale], ["NetBird", status.system.netbird]].forEach(([label, value]) => {
        if (!value) return;
        const line = document.createElement("span");
        line.textContent = label + ": " + value;
        overlaySummary.appendChild(line);
      });
    }
    text("backup-summary", status.backup.enabled ? "Enabled" : "Disabled");

    renderPlayers(players);
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
}




const quickLook = document.querySelector("[data-quick-look]");
const quickLookToggle = document.querySelector("[data-quick-look-toggle]");
if (quickLook && quickLookToggle) {
  const identityURL = "/api/session-info";
  const statusURL = "/api/dashboard-status";
  const actionMenus = Array.from(quickLook.querySelectorAll("[data-quick-look-actions]"));
  let identity = null;
  let refreshTimer = null;
  const topbar = document.querySelector(".topbar");

  const syncQuickLookTop = () => {
    quickLook.style.top = Math.round(topbar?.getBoundingClientRect().height || 48) + "px";
  };
  syncQuickLookTop();
  window.addEventListener("resize", syncQuickLookTop);

  const stateKey = (username) => `justvoxel-quick-look-v1:${encodeURIComponent(username)}`;

  const setOpen = (open, persist = true) => {
    quickLook.classList.toggle("is-open", open);
    quickLookToggle.setAttribute("aria-expanded", open ? "true" : "false");
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

  const renderQuickLook = (snapshot) => {
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
      [["Tailscale", system.tailscale], ["NetBird", system.netbird]].forEach(([label, value]) => {
        if (!value) return;
        const line = document.createElement("span");
        line.textContent = label + ": " + value;
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
      renderQuickLook(await response.json());
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
const topbarClock = document.querySelector("[data-topbar-clock]");
if (topbarClock) {
  const updateTopbarClock = () => {
    const now = new Date();
    topbarClock.textContent = new Intl.DateTimeFormat([], {
      hour: "2-digit",
      minute: "2-digit",
    }).format(now);
    topbarClock.title = new Intl.DateTimeFormat([], {
      weekday: "long",
      year: "numeric",
      month: "long",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    }).format(now);
  };
  updateTopbarClock();
  window.setInterval(updateTopbarClock, 30000);
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
    if (backupToggle) backupToggle.disabled = checking || updating || rebootWorkflowActive;
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
const workspaceCompactQuery = window.matchMedia("(max-width: 700px)");

const workspaceWindowStateKey = (id) => `justvoxel-workspace-window-${id === "system-monitor" ? "v2" : "v1"}:${id}`;

const readWorkspaceWindowState = (id) => {
  try {
    return JSON.parse(window.localStorage.getItem(workspaceWindowStateKey(id)) || "{}");
  } catch (_) {
    return {};
  }
};

const writeWorkspaceWindowState = (id, patch) => {
  try {
    const current = readWorkspaceWindowState(id);
    window.localStorage.setItem(workspaceWindowStateKey(id), JSON.stringify({ ...current, ...patch }));
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
  const rect = element.getBoundingClientRect();
  const minTop = workspaceTopInset();
  const left = Math.min(Math.max(rect.left, 8), Math.max(8, window.innerWidth - rect.width - 8));
  const top = Math.min(Math.max(rect.top, minTop), Math.max(minTop, window.innerHeight - rect.height - 8));
  element.style.left = Math.round(left) + "px";
  element.style.top = Math.round(top) + "px";
};

const setupWorkspaceWindow = (element, options = {}) => {
  const id = element.dataset.workspaceWindow;
  if (!id) return null;

  workspaceWindows.add(element);
  const dragHandle = element.querySelector("[data-workspace-drag-handle]");
  const resizeHandle = element.querySelector("[data-workspace-resize-handle]");
  let resizeSaveTimer = null;

  const bringToFront = () => {
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

    window.requestAnimationFrame(() => {
      if (!Number.isFinite(saved.left) || !Number.isFinite(saved.top)) {
        const rect = element.getBoundingClientRect();
        element.style.left = Math.max(8, Math.round((window.innerWidth - rect.width) / 2)) + "px";
        element.style.top = Math.max(workspaceTopInset(), Math.round((window.innerHeight - rect.height) / 2)) + "px";
      }
      clampWorkspaceWindow(element);
      persistGeometry();
    });
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
          Math.max(8, window.innerWidth - rect.width - 8),
        );
        const top = Math.min(
          Math.max(startTop + moveEvent.clientY - startY, minTop),
          Math.max(minTop, window.innerHeight - rect.height - 8),
        );
        element.style.left = Math.round(left) + "px";
        element.style.top = Math.round(top) + "px";
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

  if (resizeHandle) {
    resizeHandle.addEventListener("pointerdown", (event) => {
      if (workspaceCompactQuery.matches || event.button !== 0) return;
      bringToFront();
      const rect = element.getBoundingClientRect();
      const startX = event.clientX;
      const startY = event.clientY;
      const startWidth = rect.width;
      const startHeight = rect.height;
      const computed = window.getComputedStyle(element);
      const minWidth = Number.parseFloat(computed.minWidth) || 360;
      const minHeight = Number.parseFloat(computed.minHeight) || 260;
      const maxWidth = Math.max(minWidth, window.innerWidth - rect.left - 8);
      const maxHeight = Math.max(minHeight, window.innerHeight - rect.top - 8);
      event.preventDefault();
      resizeHandle.setPointerCapture(event.pointerId);
      const move = (moveEvent) => {
        element.style.width = Math.round(Math.min(maxWidth, Math.max(minWidth, startWidth + moveEvent.clientX - startX))) + "px";
        element.style.height = Math.round(Math.min(maxHeight, Math.max(minHeight, startHeight + moveEvent.clientY - startY))) + "px";
      };
      const finish = () => {
        resizeHandle.removeEventListener("pointermove", move);
        resizeHandle.removeEventListener("pointerup", finish);
        resizeHandle.removeEventListener("pointercancel", finish);
        clampWorkspaceWindow(element);
        persistGeometry();
      };
      resizeHandle.addEventListener("pointermove", move);
      resizeHandle.addEventListener("pointerup", finish);
      resizeHandle.addEventListener("pointercancel", finish);
    });
  }

    const resizeObserver = new ResizeObserver(() => {
    if (!element.open || workspaceCompactQuery.matches) return;
    window.clearTimeout(resizeSaveTimer);
    resizeSaveTimer = window.setTimeout(() => {
      clampWorkspaceWindow(element);
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

document.addEventListener("keydown", (event) => {
  if (event.key !== "Escape") return;
  if (document.querySelector("dialog:modal")) return;
  const openWindows = Array.from(workspaceWindows).filter((element) => element.open);
  if (openWindows.length === 0) return;
  openWindows.sort((a, b) => Number(b.style.zIndex || 0) - Number(a.style.zIndex || 0));
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

  const loadStorage = async () => {
    const sequence = ++loadSequence;
    if (state) state.textContent = "Loading storage…";
    if (refreshButton) refreshButton.disabled = true;
    try {
      await ensureStorageBrowserAssets();
      const response = await fetch("/workspace/storage", {
        method: "GET",
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
      if (state) state.textContent = "";
    } catch (error) {
      if (sequence !== loadSequence) return;
      if (content) content.replaceChildren();
      if (state) state.textContent = error?.message || "Storage discovery is unavailable.";
    } finally {
      if (sequence === loadSequence && refreshButton) refreshButton.disabled = false;
    }
  };

  const workspaceWindow = setupWorkspaceWindow(storageDialog, { onOpen: loadStorage });

  storageOpen.addEventListener("click", () => {
    if (controlCenter) controlCenter.open = false;
    workspaceWindow?.open();
  });
  closeButton?.addEventListener("click", () => workspaceWindow?.close());
  refreshButton?.addEventListener("click", loadStorage);

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
  let currentURL = "/workspace/backups";
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
  let currentTab = "export";
  let currentURL = tabURLs.export;
  let loadSequence = 0;
  let migrationSourceSMBPassword = "";
  let migrationExportSMBPassword = "";
  let migrationLoadController = null;
  let migrationSubmitController = null;

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
    const available = root.dataset.migrationRecoveryAvailable === "true";
    recoveryTab.hidden = !available;
    if (!available && currentTab === "recovery") {
      currentTab = "export";
      currentURL = tabURLs.export;
      syncMigrationTabs();
    }
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
    currentTab = inferMigrationTab(responseURL, root);
    syncMigrationTabs();

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
          signal: submitController.signal,
        });
        if (await handleMigrationAuth(response)) return;
        if (response.status === 403) throw new Error("Administrator access required.");

        const responseMarkup = await response.text();
        if (!response.ok && !responseMarkup.includes("data-migration-workspace-root")) {
          const detail = responseMarkup.replace(/<[^>]*>/g, " ").replace(/\s+/g, " ").trim();
          throw new Error(detail || `Migration request failed (HTTP ${response.status}).`);
        }
        renderMigrationMarkup(responseMarkup, response.url || action.pathname);
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
        headers: { Accept: "text/html" },
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
        headers: { Accept: "text/html" },
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
    onOpen: loadMigrationEntry,
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
      syncMigrationTabs();
      await loadMigration(currentURL);
    });
  });

  const restoreMigrationWhenRoleKnown = () => {
    if (!readWorkspaceWindowState("migration").open) return;
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
    const processes = (Array.isArray(data.processlist) ? data.processlist : []).slice()
      .sort((a,b)=>Number(b.cpu_percent || 0)-Number(a.cpu_percent || 0)).slice(0, Number(profile.process_count || 10));
    renderTable("[data-monitor-processes]", processes, [
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
  const refreshMonitor = async () => {
    try {
      const response = await fetch("/api/system-monitor", {method:"GET", credentials:"same-origin", headers:{Accept:"application/json"}, cache:"no-store"});
      if (response.status === 401) { window.location.assign("/login"); return; }
      if (!response.ok) throw new Error("monitor unavailable");
      renderMonitor(await response.json());
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
  profileInputs.forEach((input) => input.addEventListener("change", () => {
    profile[input.dataset.monitorProfile] = input.checked;
    syncProfile();
    if (profileStatus) profileStatus.textContent = "Not saved";
  }));
  if (processCount) processCount.addEventListener("change", () => {
    profile.process_count = Number(processCount.value);
    refreshMonitor();
    if (profileStatus) profileStatus.textContent = "Not saved";
  });
  if (resetButton) resetButton.addEventListener("click", () => {
    profile = { ...defaultProfile };
    syncProfile();
    refreshMonitor();
    if (profileStatus) profileStatus.textContent = "Not saved";
  });
  if (saveButton) saveButton.addEventListener("click", saveProfile);

  const restoreWhenRoleKnown = () => {
    if (!readWorkspaceWindowState("system-monitor").open) return;
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
  const settingsTabs = new Set(["memory", "players", "crossplay", "version"]);
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
      ["Players", minecraft.configured ? `${players.online ?? 0} / ${players.max ?? minecraft.max_players ?? "—"}` : "Not configured"],
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
    const recommendedReserveMiB = Number(defaults?.system_reserve_recommended_mib || 2048);
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

  const syncVersionPolicy = (root) => {
    const policy = root.querySelector('[name="version_policy"]');
    const version = root.querySelector('[name="version"]');
    const field = root.querySelector("[data-minecraft-version-field]");
    if (!policy || !version || !field) return;
    const sync = () => {
      const pinned = policy.value === "pinned";
      field.hidden = !pinned;
      version.required = pinned;
      version.readOnly = !pinned;
      if (policy.value === "latest") version.value = "LATEST";
      if (policy.value === "recommended") version.value = "";
    };
    policy.addEventListener("change", sync);
    sync();
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
    } else if (tab === "players") {
      section.innerHTML = `
        <div class="section-heading"><div><p class="eyebrow">Server</p><h2>Players & identity</h2></div></div>
        <div class="minecraft-native-grid">
          <label>Maximum players<input name="max_players" type="number" min="1" step="1" required></label>
          <label>Server welcome message (MOTD)<input name="motd" required></label>
          <label>Timezone<input name="timezone" required autocomplete="off"><span class="minecraft-native-help">IANA timezone such as America/Toronto or Europe/Kyiv.</span></label>
        </div>`;
      section.querySelector('[name="max_players"]').value = String(minecraft.max_players || 10);
      section.querySelector('[name="motd"]').value = minecraft.motd || "";
      section.querySelector('[name="timezone"]').value = minecraft.timezone || "";
      form.appendChild(section);
    } else if (tab === "crossplay") {
      section.innerHTML = `
        <div class="section-heading"><div><p class="eyebrow">Network</p><h2>Java & Bedrock</h2></div></div>
        <div class="minecraft-native-grid">
          <label>Minecraft Java port<input name="java_port" type="number" min="1" max="65535" required></label>
          <label>Bedrock UDP port<input name="bedrock_port" type="number" min="1" max="65535" required></label>
        </div>
        <label class="minecraft-native-toggle"><input name="bedrock_enabled" type="checkbox"> Enable Bedrock cross-play</label>
        <p class="muted compact">Port and Bedrock changes update the appliance firewall when applied. Minecraft is not restarted automatically for these non-memory changes.</p>`;
      section.querySelector('[name="java_port"]').value = String(minecraft.java_port || 25565);
      section.querySelector('[name="bedrock_port"]').value = String(minecraft.bedrock_port || 19132);
      section.querySelector('[name="bedrock_enabled"]').checked = Boolean(minecraft.bedrock_enabled);
      form.appendChild(section);
    } else if (tab === "version") {
      section.innerHTML = `
        <div class="section-heading"><div><p class="eyebrow">Software</p><h2>Minecraft version policy</h2></div></div>
        <div class="minecraft-native-grid">
          <label>Minecraft container channel or tag<input name="image_tag" required autocomplete="off"><span class="minecraft-native-help">stable, latest, or an exact upstream image tag.</span></label>
          <label>Version policy<select name="version_policy"><option value="pinned">Pinned exact Minecraft version</option><option value="recommended">Resolve newest stable Paper-supported version now</option><option value="latest">LATEST when Minecraft starts</option></select></label>
          <label data-minecraft-version-field>Exact Minecraft version<input name="version" autocomplete="off"><span class="minecraft-native-help">Used when the policy is Pinned.</span></label>
        </div>`;
      section.querySelector('[name="image_tag"]').value = minecraft.image_tag || "stable";
      section.querySelector('[name="version_policy"]').value = minecraft.version_mode || "pinned";
      section.querySelector('[name="version"]').value = minecraft.version || "";
      form.appendChild(section);
      syncVersionPolicy(section);
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
      const before = document.createElement("code");
      before.textContent = change.before || "—";
      const arrow = document.createElement("span");
      arrow.textContent = "→";
      const after = document.createElement("code");
      after.textContent = change.after || "—";
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

  const renderWhitelist = async (sequence, message = "") => {
    const payload = await requestWorkspaceJSON("/api/minecraft/workspace/whitelist");
    if (!payload || sequence !== loadSequence || !content) return;
    const root = document.createElement("div");
    root.className = "minecraft-native-operations";
    if (message) root.appendChild(noticeNode(message));

    const section = document.createElement("section");
    section.className = "panel details minecraft-workspace-section";
    section.innerHTML = `
      <div class="section-heading"><div><p class="eyebrow">Minecraft</p><h2>Whitelist</h2></div></div>
      <pre class="log-box" data-minecraft-native-whitelist></pre>
      <form class="minecraft-native-form" data-minecraft-native-whitelist-form>
        <div class="minecraft-native-grid">
          <label>Platform<select name="platform"><option value="java">Java</option><option value="bedrock">Bedrock</option></select></label>
          <label>Action<select name="action"><option value="add">Add</option><option value="remove">Remove</option></select></label>
          <label>Player name, Xbox gamertag, or Floodgate UUID<input name="name" required maxlength="64" autocomplete="off"></label>
        </div>
        <p class="muted compact">For Bedrock, use the Xbox gamertag without the leading dot. If Floodgate cannot resolve it, use the player's Floodgate UUID.</p>
        <div class="minecraft-native-actions"><button type="submit">Apply whitelist change</button></div>
      </form>`;
    section.querySelector("[data-minecraft-native-whitelist]").textContent = payload.output || "No whitelist information available.";
    root.appendChild(section);
    content.replaceChildren(root);

    const form = section.querySelector("[data-minecraft-native-whitelist-form]");
    form?.addEventListener("submit", async (event) => {
      event.preventDefault();
      const submitter = event.submitter;
      if (submitter) submitter.disabled = true;
      if (state) state.textContent = "Updating whitelist…";
      try {
        const params = new URLSearchParams();
        new FormData(form).forEach((value, key) => params.append(key, String(value)));
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
        if (submitter) submitter.disabled = false;
      }
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
      else if (currentTab === "whitelist") await renderWhitelist(sequence, message);
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
  const administratorTabs = new Set(["health", "users", "security", "reset"]);
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

  const systemFetchJSON = async (url, options = {}) => {
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

  const renderHistory = async (sequence, message = "") => {
    const payload = await systemFetchJSON("/api/system/workspace/history");
    if (!payload || sequence !== loadSequence || !content) return;
    const root = document.createElement("div");
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
      root.appendChild(auditPanel);
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

  const renderUsers = async (sequence, message = "") => {
    const payload = await systemFetchJSON("/api/system/workspace/users");
    if (!payload || sequence !== loadSequence || !content) return;
    const root = document.createElement("div");
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
      users.forEach((user) => {
        const card = document.createElement("article");
        card.className = "user-card";

        const heading = document.createElement("div");
        heading.className = "user-card-heading";
        const identityWrap = document.createElement("div");
        const username = document.createElement("strong");
        username.textContent = user.username || "User";
        const enabled = document.createElement("span");
        enabled.className = "muted";
        enabled.textContent = user.enabled ? "Enabled" : "Disabled";
        identityWrap.append(username, enabled);
        const roleBadge = document.createElement("span");
        roleBadge.className = "badge";
        roleBadge.textContent = user.role || "viewer";
        heading.append(identityWrap, roleBadge);
        card.appendChild(heading);

        const quota = document.createElement("div");
        quota.className = "quota-grid";
        [["Restart allowance", user.restart_used, user.restart_limit], ["Backup allowance", user.backup_used, user.backup_limit]].forEach(([label, used, limit]) => {
          const box = document.createElement("div");
          box.className = "quota-box";
          const labelNode = document.createElement("span");
          labelNode.textContent = label;
          const valueNode = document.createElement("strong");
          valueNode.textContent = String(used ?? 0) + " / " + String(limit ?? 0);
          box.append(labelNode, valueNode);
          quota.appendChild(box);
        });
        card.appendChild(quota);

        const actions = document.createElement("div");
        actions.className = "user-actions-grid";

        const roleForm = document.createElement("form");
        roleForm.innerHTML = '<label>Role<select name="role"><option value="operator">Operator</option><option value="viewer">Viewer</option></select></label><button class="secondary" type="submit">Save role</button>';
        roleForm.querySelector("select").value = user.role || "viewer";
        roleForm.addEventListener("submit", async (event) => {
          event.preventDefault();
          const result = await systemPostForm("/api/system/workspace/users/" + user.id + "/role", {
            role: roleForm.querySelector("select").value,
          });
          if (result) await renderUsers(++loadSequence, result.message || "WebUI user updated.");
        });
        actions.appendChild(roleForm);

        const enabledButton = document.createElement("button");
        enabledButton.type = "button";
        enabledButton.className = "secondary";
        enabledButton.textContent = user.enabled ? "Disable" : "Enable";
        enabledButton.addEventListener("click", async () => {
          enabledButton.disabled = true;
          const result = await systemPostForm("/api/system/workspace/users/" + user.id + "/enabled", { enabled: !user.enabled });
          if (result) await renderUsers(++loadSequence, result.message || "WebUI user updated.");
        });
        actions.appendChild(enabledButton);

        const restartButton = document.createElement("button");
        restartButton.type = "button";
        restartButton.className = "secondary";
        restartButton.textContent = "Reset restart allowance";
        restartButton.disabled = Number(user.restart_used || 0) === 0;
        restartButton.addEventListener("click", async () => {
          const result = await systemPostForm("/api/system/workspace/users/" + user.id + "/restart-allowance/reset");
          if (result) await renderUsers(++loadSequence, result.message || "Restart allowance reset.");
        });
        actions.appendChild(restartButton);

        const backupButton = document.createElement("button");
        backupButton.type = "button";
        backupButton.className = "secondary";
        backupButton.textContent = "Reset backup allowance";
        backupButton.disabled = Number(user.backup_used || 0) === 0;
        backupButton.addEventListener("click", async () => {
          const result = await systemPostForm("/api/system/workspace/users/" + user.id + "/backup-allowance/reset");
          if (result) await renderUsers(++loadSequence, result.message || "Backup allowance reset.");
        });
        actions.appendChild(backupButton);
        card.appendChild(actions);

        const passwordDetails = document.createElement("details");
        passwordDetails.className = "user-details";
        const passwordSummary = document.createElement("summary");
        passwordSummary.textContent = "Change password";
        const passwordForm = document.createElement("form");
        passwordForm.className = "admin-form-grid compact-form";
        passwordForm.innerHTML =
          '<label>New password<input type="password" name="password" required autocomplete="new-password"></label>' +
          '<label>Confirm password<input type="password" name="confirm_password" required autocomplete="new-password"></label>' +
          '<div class="form-actions"><button class="secondary" type="submit">Change password</button></div>';
        passwordForm.querySelectorAll('input[type="password"]').forEach((input) => {
          input.minLength = Number(payload.minimum_password_len || 8);
        });
        passwordForm.addEventListener("submit", async (event) => {
          event.preventDefault();
          const data = Object.fromEntries(new FormData(passwordForm).entries());
          const result = await systemPostForm("/api/system/workspace/users/" + user.id + "/password", data);
          if (result) await renderUsers(++loadSequence, result.message || "WebUI user password changed.");
        });
        passwordDetails.append(passwordSummary, passwordForm);
        card.appendChild(passwordDetails);

        const deleteDetails = document.createElement("details");
        deleteDetails.className = "user-details danger-zone";
        const deleteSummary = document.createElement("summary");
        deleteSummary.textContent = "Delete account";
        const deleteText = document.createElement("p");
        deleteText.className = "muted compact";
        deleteText.textContent = "Deleting this WebUI identity also removes its saved Operator quota history.";
        const deleteButton = document.createElement("button");
        deleteButton.type = "button";
        deleteButton.className = "danger";
        deleteButton.textContent = "Delete " + (user.username || "account");
        deleteButton.addEventListener("click", async () => {
          deleteButton.disabled = true;
          const result = await systemPostForm("/api/system/workspace/users/" + user.id + "/delete");
          if (result) await renderUsers(++loadSequence, result.message || "WebUI user deleted.");
        });
        deleteDetails.append(deleteSummary, deleteText, deleteButton);
        card.appendChild(deleteDetails);

        list.appendChild(card);
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
    root.appendChild(switcher);

    const authPane = document.createElement("section");
    authPane.className = "system-security-pane";
    authPane.dataset.systemSecurityPane = "authentication";
    const authHeading = document.createElement("div");
    authHeading.className = "system-security-pane-heading";
    const authTitle = document.createElement("h3");
    authTitle.textContent = "Authentication";
    const authDescription = document.createElement("p");
    authDescription.className = "muted compact";
    authDescription.textContent = "Choose whether WebUI uses the voxel system password or its own separate password.";
    authHeading.append(authTitle, authDescription);
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
    passwordHeading.append(passwordTitle, passwordDescription);
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

    if (operation.operation_type === "factory_reset") {
      const note = document.createElement("div");
      note.className = "notice warning";
      note.textContent = "When Full Factory Reset completes, this WebUI session will be signed out and the voxel password must be changed on the next sign-in.";
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
        const payload = await systemFetchJSON("/api/system/workspace/reset/operations/" + operation.operation_id);
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
      remove.push("Current WebUI sessions; the voxel system password will be marked for mandatory change.");
      keep.push("USB, external, NFS, and SMB data.");
      keep.push("Storage partitions, filesystems, mounts, and /etc/fstab.");
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
    const built = systemPanel("JustVoxel", "About");
    const badge = document.createElement("span");
    badge.className = "badge";
    badge.textContent = payload.variant || "Unknown";
    built.heading.appendChild(badge);
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

    if (user?.role === "administrator") {
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
      hostLabel.textContent = "NUT server";
      const host = document.createElement("input");
      host.name = "host";
      host.type = "text";
      host.autocomplete = "off";
      host.placeholder = "192.168.0.51 or blackbox.lan";
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

      const save = document.createElement("button");
      save.type = "submit";
      save.className = "button primary";
      save.textContent = "Save and verify";

      const syncRemoteFields = () => {
        const remote = mode.value === "remote";
        host.disabled = !remote;
        port.disabled = !remote;
        host.required = remote;
      };
      mode.addEventListener("change", syncRemoteFields);
      syncRemoteFields();

      form.append(csrf, modeLabel, hostLabel, portLabel, save);
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

  const loadCurrentSystemTab = async () => {
    const sequence = ++loadSequence;
    if (state) state.textContent = "Loading…";
    if (refreshButton) refreshButton.disabled = true;
    if (content) content.replaceChildren();

    try {
      const user = await loadIdentity();
      if (!user) return;
      if (administratorTabs.has(currentTab) && user.role !== "administrator") {
        currentTab = "history";
        syncSystemTabs();
      }
      if (currentTab === "health") await renderHealth(sequence);
      else if (currentTab === "history") await renderHistory(sequence);
      else if (currentTab === "users") await renderUsers(sequence);
      else if (currentTab === "security") await renderSecurity(sequence);
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
    const user = await loadIdentity();
    if (!user) return;
    if (administratorTabs.has(tab) && user.role !== "administrator") return;
    if (tab === "ups" && !upsAvailable) return;
    if (currentTab === "reset" && tab !== "reset") clearResetPoll();
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
    onOpen: async () => {
      try {
        const user = await loadIdentity();
        if (!user) return;
        await refreshUPSCapability();
        if (!initialized) {
          initialized = true;
          currentTab = user.role === "administrator" ? "health" : "history";
          syncSystemTabs();
        }
        loadCurrentSystemTab();
      } catch (error) {
        if (state) state.textContent = error?.message || "System workspace is unavailable.";
      }
    },
  });

  systemWorkspaceOpen.addEventListener("click", () => {
    if (controlCenter) controlCenter.open = false;
    workspaceWindow?.open();
  });
  closeButton?.addEventListener("click", () => {
    clearResetPoll();
    workspaceWindow?.close();
  });
  refreshButton?.addEventListener("click", loadCurrentSystemTab);
  tabs.forEach((button) => button.addEventListener("click", () => selectSystemTab(button.dataset.systemTab)));

  if (readWorkspaceWindowState("system").open) workspaceWindow?.open();
}
