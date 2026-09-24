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

  const renderDashboard = (snapshot) => {
    const { status, players } = snapshot;
    updatePendingState(status, players);

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
        if (dialogMessage) dialogMessage.textContent = result.message || "The Management Agent accepted the system action.";
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


const systemUpdateOpen = document.querySelector("[data-system-update-open]");
const systemUpdateDialog = document.querySelector("[data-system-update-dialog]");
if (systemUpdateOpen && systemUpdateDialog) {
  const csrfInput = document.querySelector("[data-system-update-csrf]");
  const closeButton = systemUpdateDialog.querySelector("[data-system-update-close]");
  const runningValue = systemUpdateDialog.querySelector("[data-system-update-running]");
  const runningImage = systemUpdateDialog.querySelector("[data-system-update-running-image]");
  const stagedValue = systemUpdateDialog.querySelector("[data-system-update-staged]");
  const stagedImage = systemUpdateDialog.querySelector("[data-system-update-staged-image]");
  const state = systemUpdateDialog.querySelector("[data-system-update-state]");
  const error = systemUpdateDialog.querySelector("[data-system-update-error]");
  const updateButton = systemUpdateDialog.querySelector("[data-system-update-button]");
  const rebootPanel = systemUpdateDialog.querySelector("[data-system-update-reboot]");
  const backupToggle = systemUpdateDialog.querySelector("[data-system-update-backup]");
  const quickToggle = systemUpdateDialog.querySelector("[data-system-update-quick]");
  const warningText = systemUpdateDialog.querySelector("[data-system-update-warning]");
  const rebootPlayers = systemUpdateDialog.querySelector("[data-system-update-reboot-players]");
  const rebootProgress = systemUpdateDialog.querySelector("[data-system-update-reboot-progress]");
  const rebootCountdown = systemUpdateDialog.querySelector("[data-system-update-countdown]");
  const rebootMessage = systemUpdateDialog.querySelector("[data-system-update-reboot-message]");
  const rebootButton = systemUpdateDialog.querySelector("[data-system-update-reboot-button]");

  let updating = false;
  let latestStatus = null;
  let pendingPlayerConfirmation = false;
  let rebootWorkflowActive = false;
  let rebootPollTimer = null;
  let countdownTimer = null;

  const deploymentVersion = (deployment, fallback) => {
    if (!deployment) return fallback;
    return deployment.version || "Deployment";
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

  const showRebootPlayers = (result) => {
    if (!rebootPlayers) return;
    const players = Array.isArray(result.players) ? result.players.filter(Boolean) : [];
    const count = Number(result.online || players.length || 0);
    const names = players.length ? " — " + players.join(", ") : "";
    rebootPlayers.textContent = count + " player" + (count === 1 ? "" : "s") + " online" + names + ". Press Reboot again to confirm.";
    rebootPlayers.hidden = false;
  };

  const hideRebootPlayers = () => {
    if (!rebootPlayers) return;
    rebootPlayers.hidden = true;
    rebootPlayers.textContent = "";
  };

  const setRebootControlsDisabled = (disabled) => {
    if (backupToggle) backupToggle.disabled = disabled;
    if (quickToggle) quickToggle.disabled = disabled;
    if (rebootButton) rebootButton.disabled = disabled;
  };

  const resetPlayerConfirmation = () => {
    if (rebootWorkflowActive) return;
    pendingPlayerConfirmation = false;
    hideRebootPlayers();
    if (rebootButton) rebootButton.textContent = "Reboot";
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
      setRebootControlsDisabled(false);
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
      setRebootControlsDisabled(false);
      if (rebootButton) rebootButton.textContent = "Reboot";
      return;
    }

    if (result.state === "backup_failed" || result.state === "failed") {
      rebootWorkflowActive = false;
      pendingPlayerConfirmation = false;
      clearRebootTimers();
      hideRebootPlayers();
      if (rebootProgress) rebootProgress.hidden = true;
      setRebootControlsDisabled(false);
      if (rebootButton) rebootButton.textContent = "Reboot";
      if (error) {
        error.textContent = result.message || "Reboot was cancelled.";
        error.hidden = false;
      }
      return;
    }

    if (rebootWorkflowActive) {
      pendingPlayerConfirmation = false;
      hideRebootPlayers();
      setRebootControlsDisabled(true);
      if (updateButton) updateButton.disabled = true;
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
    if (runningValue) runningValue.textContent = deploymentVersion(status.running, "Unavailable");
    if (runningImage) runningImage.textContent = status.running && status.running.image ? status.running.image : "";
    if (stagedValue) stagedValue.textContent = deploymentVersion(status.staged, "None");
    if (stagedImage) stagedImage.textContent = status.staged && status.staged.image ? status.staged.image : "";

    if (state) {
      if (status.read_only) {
        state.textContent = "System updates are unavailable on this read-only deployment.";
      } else if (status.reboot_required) {
        state.textContent = "Update staged — reboot required.";
      } else {
        state.textContent = status.message || "JustVoxel is current.";
      }
    }

    if (rebootPanel) rebootPanel.hidden = !status.reboot_required;
    if (!status.reboot_required) {
      rebootWorkflowActive = false;
      pendingPlayerConfirmation = false;
      clearRebootTimers();
      hideRebootPlayers();
      if (rebootProgress) rebootProgress.hidden = true;
    }

    if (error) {
      error.hidden = true;
      error.textContent = "";
    }
    if (updateButton) {
      updateButton.disabled = Boolean(status.read_only) || updating || rebootWorkflowActive;
    }
  };

  const showSystemUpdateError = (message) => {
    if (error) {
      error.textContent = message || "System update information is unavailable.";
      error.hidden = false;
    }
    if (state) state.textContent = "System update unavailable.";
    if (updateButton) updateButton.disabled = updating || rebootWorkflowActive;
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

  const loadSystemUpdate = async () => {
    if (state) state.textContent = "Loading system update status…";
    if (error) error.hidden = true;
    if (updateButton) updateButton.disabled = true;
    try {
      const response = await fetch("/api/system-updates", {
        method: "GET",
        credentials: "same-origin",
        headers: { Accept: "application/json" },
        cache: "no-store",
      });
      if (handleAuthResponse(response)) return;
      const result = await readResponse(response);
      if (!response.ok) {
        showSystemUpdateError(result.error || "System update status is unavailable.");
        return;
      }
      renderSystemUpdate(result);
      if (result.reboot_required) {
        await pollRebootStatus();
      }
    } catch (_) {
      showSystemUpdateError("System update service is unavailable.");
    }
  };

  const applySystemUpdate = async () => {
    if (updating || rebootWorkflowActive || !csrfInput || !updateButton) return;
    updating = true;
    updateButton.disabled = true;
    updateButton.classList.add("is-busy");
    updateButton.setAttribute("aria-busy", "true");
    updateButton.textContent = "Updating…";
    if (state) state.textContent = "Updating JustVoxel… Minecraft keeps running.";
    if (error) error.hidden = true;

    const body = new URLSearchParams();
    body.set("csrf", csrfInput.value);

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
      }
    } catch (_) {
      showSystemUpdateError("System update service is unavailable. Nothing was submitted again automatically.");
    } finally {
      updating = false;
      updateButton.classList.remove("is-busy");
      updateButton.removeAttribute("aria-busy");
      updateButton.textContent = "Update system";
      updateButton.disabled = Boolean(latestStatus && latestStatus.read_only) || rebootWorkflowActive;
    }
  };

  const requestUpdateReboot = async () => {
    if (rebootWorkflowActive || !csrfInput || !rebootButton) return;
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
      if (result.accepted) {
        rebootPollTimer = window.setTimeout(pollRebootStatus, 250);
      }
    } catch (_) {
      rebootWorkflowActive = false;
      setRebootControlsDisabled(false);
      rebootButton.textContent = "Reboot";
      if (error) {
        error.textContent = "Reboot workflow could not be started.";
        error.hidden = false;
      }
    }
  };

  systemUpdateOpen.addEventListener("click", () => {
    if (controlCenter) controlCenter.open = false;
    systemUpdateDialog.showModal();
    updateWarningText();
    loadSystemUpdate();
  });

  closeButton?.addEventListener("click", () => systemUpdateDialog.close());
  updateButton?.addEventListener("click", applySystemUpdate);
  rebootButton?.addEventListener("click", requestUpdateReboot);
  quickToggle?.addEventListener("change", () => {
    updateWarningText();
    resetPlayerConfirmation();
  });
  backupToggle?.addEventListener("change", resetPlayerConfirmation);

  systemUpdateDialog.addEventListener("click", (event) => {
    if (event.target === systemUpdateDialog) systemUpdateDialog.close();
  });
  systemUpdateDialog.addEventListener("close", clearRebootTimers);
}


const workspaceWindows = new Set();
let workspaceWindowZ = 120;
const workspaceCompactQuery = window.matchMedia("(max-width: 700px)");

const workspaceWindowStateKey = (id) => `justvoxel-workspace-window-v1:${id}`;

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
  const openWindows = Array.from(workspaceWindows).filter((element) => element.open);
  if (openWindows.length === 0) return;
  openWindows.sort((a, b) => Number(b.style.zIndex || 0) - Number(a.style.zIndex || 0));
  openWindows[0].close();
});

window.addEventListener("resize", () => {
  workspaceWindows.forEach((element) => clampWorkspaceWindow(element));
});

const systemMonitorOpen = document.querySelector("[data-system-monitor-open]");
const systemMonitorDialog = document.querySelector("[data-system-monitor-dialog]");
if (systemMonitorOpen && systemMonitorDialog) {
  const closeButton = systemMonitorDialog.querySelector("[data-system-monitor-close]");
  const profileToggle = systemMonitorDialog.querySelector("[data-system-monitor-profile-toggle]");
  const profilePanel = systemMonitorDialog.querySelector("[data-system-monitor-profile]");
  const resetButton = systemMonitorDialog.querySelector("[data-system-monitor-reset]");
  const processCount = systemMonitorDialog.querySelector("[data-monitor-process-count]");
  const state = systemMonitorDialog.querySelector("[data-system-monitor-state]");
  const error = systemMonitorDialog.querySelector("[data-system-monitor-error]");
  const cards = Array.from(systemMonitorDialog.querySelectorAll("[data-monitor-card]"));
  const profileInputs = Array.from(systemMonitorDialog.querySelectorAll("[data-monitor-profile]"));
  const profileKey = "justvoxel-system-monitor-profile-v1";
  const defaultProfile = {
    system: true, cpu: true, memory: true, load: true, filesystem: true,
    diskio: true, network: true, processes: true, containers: true, sensors: true, alerts: true,
    processCount: 10,
  };
  let profile = { ...defaultProfile };
  let refreshTimer = null;

  const readProfile = () => {
    try {
      profile = { ...defaultProfile, ...JSON.parse(window.localStorage.getItem(profileKey) || "{}") };
    } catch (_) {
      profile = { ...defaultProfile };
    }
  };
  const saveProfile = () => window.localStorage.setItem(profileKey, JSON.stringify(profile));
  const syncProfile = () => {
    profileInputs.forEach((input) => { input.checked = profile[input.dataset.monitorProfile] !== false; });
    if (processCount) processCount.value = String(profile.processCount || 10);
    cards.forEach((card) => { card.hidden = profile[card.dataset.monitorCard] === false; });
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
      .sort((a,b)=>Number(b.cpu_percent || 0)-Number(a.cpu_percent || 0)).slice(0, Number(profile.processCount || 10));
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
  const startRefresh = () => {
    readProfile();
    syncProfile();
    if (profilePanel) profilePanel.hidden = true;
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

  if (profileToggle && profilePanel) profileToggle.addEventListener("click", () => { profilePanel.hidden = !profilePanel.hidden; });
  profileInputs.forEach((input) => input.addEventListener("change", () => {
    profile[input.dataset.monitorProfile] = input.checked; saveProfile(); syncProfile();
  }));
  if (processCount) processCount.addEventListener("change", () => {
    profile.processCount = Number(processCount.value); saveProfile(); refreshMonitor();
  });
  if (resetButton) resetButton.addEventListener("click", () => {
    profile = { ...defaultProfile }; saveProfile(); syncProfile(); refreshMonitor();
  });

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
