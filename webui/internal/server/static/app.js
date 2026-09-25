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
    if (stagedValue) stagedValue.textContent = deploymentVersion(status.staged, "None");
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
  let currentURL = "/settings/new-backups";
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

  const extractBackupsRoot = (markup) => {
    const parsed = new DOMParser().parseFromString(markup, "text/html");
    const main = parsed.querySelector("main.new-backups-shell");
    if (!main) return null;
    const root = document.createElement("div");
    root.className = "new-backups-shell backups-workspace-content";
    root.dataset.backupsWorkspaceRoot = "true";
    root.innerHTML = main.innerHTML;
    root.querySelector(".title-row")?.remove();
    root.querySelector(".backup-library-note")?.remove();
    return root;
  };

  const renderBackupsMarkup = (markup) => {
    if (!content) throw new Error("Backups workspace is unavailable.");
    const root = extractBackupsRoot(markup);
    if (!root) throw new Error("Backups response could not be rendered.");
    content.replaceChildren(root);

    const handleSubmit = async (event) => {
      const form = event.target.closest("form");
      if (!form || !root.contains(form)) return;
      const action = new URL(form.getAttribute("action") || currentURL, window.location.href);
      if (action.origin !== window.location.origin || !action.pathname.startsWith("/settings/new-backups")) return;

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
          if (target.origin === window.location.origin && target.pathname === "/settings/new-backups") {
            currentURL = target.pathname + target.search;
          }
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
      if (href.startsWith("/settings/new-backups")) {
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
      if (response.status === 403) throw new Error("Administrator access required.");

      const markup = await response.text();
      if (sequence !== loadSequence) return;
      const responseURL = new URL(response.url);
      if (responseURL.origin === window.location.origin && responseURL.pathname === "/settings/new-backups") {
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
    reload: async (url = "/settings/new-backups") => {
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
  const tabURLs = {
    export: "/settings/server-migration/export",
    import: "/settings/server-migration/import",
    recovery: "/settings/server-migration/recovery",
  };
  let currentTab = "export";
  let currentURL = tabURLs.export;
  let loadSequence = 0;

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

  const inferMigrationTab = (url, root = null) => {
    const pathname = new URL(url, window.location.href).pathname;
    if (pathname.includes("/server-migration/export")) return "export";
    if (pathname.includes("/server-migration/import")) return "import";
    if (pathname.includes("/server-migration/recovery")) return "recovery";
    const operationName = root?.querySelector("#server-migration-operation-name")?.textContent || "";
    if (operationName.includes("Export")) return "export";
    if (operationName.includes("Import")) return "import";
    if (operationName.includes("Recovery")) return "recovery";
    return currentTab;
  };

  const extractMigrationRoot = (markup) => {
    const parsed = new DOMParser().parseFromString(markup, "text/html");
    const main = parsed.querySelector("main");
    if (!main) return null;
    const root = document.createElement("div");
    root.className = "migration-workspace-content";
    root.dataset.migrationWorkspaceRoot = "true";
    root.innerHTML = main.innerHTML;
    root.querySelector(".title-row")?.remove();
    root.querySelector(".foot")?.remove();
    return root;
  };

  const handleMigrationAuth = (response) => {
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
    content.replaceChildren(root);

    root.addEventListener("submit", async (event) => {
      const form = event.target.closest("form");
      if (!form || !root.contains(form)) return;
      const action = new URL(form.getAttribute("action") || currentURL, window.location.href);
      if (action.origin !== window.location.origin || !action.pathname.startsWith("/settings/server-migration")) return;

      event.preventDefault();
      const submitter = event.submitter;
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
        if (handleMigrationAuth(response)) return;
        if (response.status === 403) throw new Error("Administrator access required.");

        const responseMarkup = await response.text();
        if (!response.ok && !responseMarkup.includes("<main")) {
          throw new Error("Migration operation could not be completed.");
        }
        renderMigrationMarkup(responseMarkup, response.url || action.pathname);
        if (state) state.textContent = "";
      } catch (error) {
        if (submitter && submitter.isConnected) submitter.disabled = false;
        if (state) state.textContent = error?.message || "Migration operation could not be completed.";
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
      if (!href.startsWith("/settings/server-migration")) return;
      event.preventDefault();
      if (href === "/settings/server-migration") await loadMigrationEntry();
      else await loadMigration(href);
    });

    initializeMigrationRoot(root);
  };

  const loadMigration = async (url = currentURL) => {
    const sequence = ++loadSequence;
    if (state) state.textContent = "Loading migration…";
    if (refreshButton) refreshButton.disabled = true;
    try {
      await ensureMigrationAssets();
      const response = await fetch(url, {
        method: "GET",
        credentials: "same-origin",
        headers: { Accept: "text/html" },
        cache: "no-store",
        redirect: "follow",
      });
      if (handleMigrationAuth(response)) return;
      if (response.status === 403) throw new Error("Administrator access required.");
      if (!response.ok) throw new Error("Migration is unavailable.");

      const finalURL = new URL(response.url || url, window.location.href);
      if (finalURL.pathname === "/settings/server-migration") {
        await loadMigration(tabURLs[currentTab]);
        return;
      }
      const markup = await response.text();
      if (sequence !== loadSequence) return;
      renderMigrationMarkup(markup, finalURL.href);
      if (state) state.textContent = "";
    } catch (error) {
      if (sequence !== loadSequence) return;
      if (content) content.replaceChildren();
      if (state) state.textContent = error?.message || "Migration is unavailable.";
    } finally {
      if (sequence === loadSequence && refreshButton) refreshButton.disabled = false;
    }
  };

  const loadMigrationEntry = async () => {
    const sequence = ++loadSequence;
    if (state) state.textContent = "Loading migration…";
    if (refreshButton) refreshButton.disabled = true;
    try {
      await ensureMigrationAssets();
      const response = await fetch("/settings/server-migration", {
        method: "GET",
        credentials: "same-origin",
        headers: { Accept: "text/html" },
        cache: "no-store",
        redirect: "follow",
      });
      if (handleMigrationAuth(response)) return;
      if (response.status === 403) throw new Error("Administrator access required.");
      if (!response.ok) throw new Error("Migration is unavailable.");
      const finalURL = new URL(response.url, window.location.href);
      if (sequence !== loadSequence) return;

      if (finalURL.pathname !== "/settings/server-migration") {
        const markup = await response.text();
        renderMigrationMarkup(markup, finalURL.href);
        if (state) state.textContent = "";
        return;
      }
      await loadMigration(tabURLs[currentTab]);
    } catch (error) {
      if (sequence !== loadSequence) return;
      if (content) content.replaceChildren();
      if (state) state.textContent = error?.message || "Migration is unavailable.";
    } finally {
      if (sequence === loadSequence && refreshButton) refreshButton.disabled = false;
    }
  };

  const workspaceWindow = setupWorkspaceWindow(migrationDialog, { onOpen: loadMigrationEntry });

  migrationOpen.addEventListener("click", () => {
    if (controlCenter) controlCenter.open = false;
    workspaceWindow?.open();
  });
  closeButton?.addEventListener("click", () => workspaceWindow?.close());
  refreshButton?.addEventListener("click", () => loadMigration(currentURL));
  tabs.forEach((button) => {
    button.addEventListener("click", async () => {
      currentTab = button.dataset.migrationTab;
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
  let currentTab = "overview";
  const settingsTabs = new Set(["memory", "players", "version"]);
  let loadSequence = 0;

  const ensureStylesheet = (href) => {
    if (document.querySelector(`link[href="${href}"]`)) return;
    const stylesheet = document.createElement("link");
    stylesheet.rel = "stylesheet";
    stylesheet.href = href;
    document.head.appendChild(stylesheet);
  };

  const handleWorkspaceAuth = (response) => {
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

  const namespaceIDs = (root, prefix) => {
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

  const messageNode = (textValue) => {
    const p = document.createElement("p");
    p.className = "muted compact";
    p.textContent = textValue;
    return p;
  };

  const initSettingsForm = (root) => {
    const form = root.querySelector("form.settings-form");
    if (!form) return;

    const memoryPanel = form.querySelector("[data-memory-settings]");
    if (memoryPanel) {
      const gameMemory = form.querySelector('[name="java_memory"]');
      const maxMemory = form.querySelector('[name="container_memory"]');
      const maxPlayers = form.querySelector('[name="max_players"]');
      const memoryStatus = memoryPanel.querySelector(".memory-status");
      const buttons = Array.from(memoryPanel.querySelectorAll("[data-memory-preset]"));
      const totalMiB = Number(memoryPanel.dataset.systemMemoryMib || 0);
      const minimumReserveMiB = Number(memoryPanel.dataset.minReserveMib || 1024);
      const recommendedReserveMiB = Number(memoryPanel.dataset.recommendedReserveMib || 2048);
      let activePreset = "";

      const parseMemoryMiB = (value) => {
        const match = String(value || "").trim().match(/^([1-9][0-9]*)([mMgG])$/);
        if (!match) return 0;
        const amount = Number(match[1]);
        return match[2].toUpperCase() === "G" ? amount * 1024 : amount;
      };
      const memoryValue = (mib) => mib % 1024 === 0 ? `${mib / 1024}G` : `${mib}M`;
      const formatRemaining = (mib) => mib >= 1024
        ? `${(mib / 1024).toFixed(mib % 1024 === 0 ? 0 : 1)} GB`
        : `${Math.max(0, Math.round(mib))} MB`;
      const markPreset = (name) => {
        activePreset = name;
        buttons.forEach((button) => button.classList.toggle("is-selected", button.dataset.memoryPreset === name));
      };
      const updateStatus = () => {
        if (!memoryStatus || !maxMemory) return;
        const maximumMiB = parseMemoryMiB(maxMemory.value);
        memoryStatus.classList.remove("warning", "danger");
        if (!totalMiB) {
          memoryStatus.textContent = "System memory could not be detected. JustVoxel will validate the values again before Apply.";
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
          memoryStatus.textContent = `Not enough memory remains outside Minecraft: about ${formatRemaining(remaining)}. Reduce Maximum Minecraft memory before continuing.`;
          memoryStatus.classList.add("danger");
        } else if (remaining < recommendedReserveMiB) {
          memoryStatus.textContent = `Tight memory configuration: about ${formatRemaining(remaining)} remains outside Minecraft.`;
          memoryStatus.classList.add("warning");
        } else {
          memoryStatus.textContent = `Memory remaining outside Minecraft: about ${formatRemaining(remaining)}.`;
        }
      };
      const applyPreset = (name) => {
        if (name === "custom") {
          markPreset("custom");
          gameMemory?.focus();
          updateStatus();
          return;
        }
        if (!gameMemory || !maxMemory || !maxPlayers) return;
        const players = Math.max(1, Number(maxPlayers.value || 10));
        const playerGroups = Math.max(1, Math.ceil(players / 10));
        const playerExtra = Math.min(Math.max(playerGroups - 1, 0), 4);
        const totalGiB = totalMiB > 0 ? totalMiB / 1024 : 8;
        let baseRecommended;
        if (totalGiB < 6) baseRecommended = 2;
        else baseRecommended = Math.max(4, Math.min(8, Math.floor((totalGiB - 2) / 2)));
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

      buttons.forEach((button) => button.addEventListener("click", () => applyPreset(button.dataset.memoryPreset)));
      [gameMemory, maxMemory].forEach((input) => input?.addEventListener("input", () => {
        if (activePreset && activePreset !== "custom") markPreset("custom");
        updateStatus();
      }));
      maxPlayers?.addEventListener("change", () => {
        if (activePreset && activePreset !== "custom") applyPreset(activePreset);
      });
      updateStatus();
    }
  };

  const renderOverview = async (sequence) => {
    const response = await fetch("/api/dashboard-status", {
      method: "GET",
      credentials: "same-origin",
      headers: { Accept: "application/json" },
      cache: "no-store",
    });
    if (handleWorkspaceAuth(response)) return;
    if (response.status === 403) {
      window.location.assign("/password");
      return;
    }
    if (!response.ok) throw new Error("Minecraft status is unavailable.");
    const snapshot = await response.json();
    if (sequence !== loadSequence || !content) return;

    const minecraft = snapshot?.status?.minecraft || {};
    const players = snapshot?.players || {};
    const root = document.createElement("div");
    root.className = "minecraft-overview-compact";

    const grid = document.createElement("div");
    grid.className = "minecraft-overview-grid";
    [["Status", minecraft.state || "—"], ["Version", minecraft.version || "—"], ["Players", minecraft.configured ? `${players.online ?? 0} / ${players.max ?? minecraft.max_players ?? "—"}` : "Not configured"]].forEach(([label, value]) => {
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
    const label = document.createElement("strong");
    label.textContent = "Online";
    playerLine.appendChild(label);
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
    } else playerLine.appendChild(messageNode(`${players.online ?? 0} players online.`));
    root.appendChild(playerLine);
    content.replaceChildren(root);
  };

  const renderSettingsMarkup = (markup, view = currentTab) => {
    if (!content) return;
    const parsed = new DOMParser().parseFromString(markup, "text/html");
    const sourceForm = parsed.querySelector("form.settings-form");
    const root = document.createElement("div");
    parsed.querySelectorAll("main > .notice.success, main > .notice.error").forEach((notice) => root.appendChild(notice.cloneNode(true)));

    if (!sourceForm) {
      const unavailable = parsed.querySelector("main > .notice");
      if (unavailable) root.appendChild(unavailable.cloneNode(true));
      else root.appendChild(messageNode("Minecraft settings are unavailable."));
      namespaceIDs(root, "minecraft-workspace-");
      content.replaceChildren(root);
      return;
    }

    const backupKeep = sourceForm.querySelector('[name="backup_keep"]');
    const backupSchedule = sourceForm.querySelector('[name="backup_schedule"]');
    const backupTimer = sourceForm.querySelector('[name="backup_timer_enabled"]');
    const preservedBackup = { keep: backupKeep?.value || "", schedule: backupSchedule?.value || "", timerEnabled: Boolean(backupTimer?.checked) };

    const form = sourceForm.cloneNode(true);
    form.classList.add("minecraft-workspace-section", "minecraft-settings-split");
    form.querySelector('[name="backup_keep"]')?.closest("section")?.remove();

    const sectionView = (section) => {
      const heading = section.querySelector(".section-heading h2")?.textContent.trim() || "";
      if (heading === "Minecraft memory") return "memory";
      if (heading === "Players & identity" || heading === "Java & Bedrock") return "players";
      if (heading === "Minecraft version policy") return "version";
      return "";
    };
    Array.from(form.querySelectorAll("section")).forEach((section) => {
      const target = sectionView(section);
      if (!target) { section.remove(); return; }
      section.dataset.minecraftSettingsSection = target;
      section.hidden = target !== view;
    });

    const addHidden = (name, value) => {
      if (!value) return;
      const input = document.createElement("input");
      input.type = "hidden";
      input.name = name;
      input.value = value;
      form.appendChild(input);
    };
    addHidden("backup_keep", preservedBackup.keep);
    addHidden("backup_schedule", preservedBackup.schedule);
    if (preservedBackup.timerEnabled) addHidden("backup_timer_enabled", "on");

    root.appendChild(form);
    const review = parsed.querySelector("#review");
    if (review) root.appendChild(review.cloneNode(true));
    namespaceIDs(root, "minecraft-workspace-");
    content.replaceChildren(root);
    initSettingsForm(root);
  };

  const loadSettings = async (sequence, url = "/settings/server") => {
    ensureStylesheet("/static/settings.css");
    const response = await fetch(url, {
      method: "GET",
      credentials: "same-origin",
      headers: { Accept: "text/html" },
      cache: "no-store",
      redirect: "follow",
    });
    if (handleWorkspaceAuth(response)) return;
    if (response.status === 403) throw new Error("Administrator access required.");
    const markup = await response.text();
    if (sequence !== loadSequence) return;
    renderSettingsMarkup(markup, currentTab);
  };

  const renderOperationsMarkup = (markup, sectionID) => {
    if (!content) return;
    const parsed = new DOMParser().parseFromString(markup, "text/html");
    const source = parsed.querySelector(sectionID);
    if (!source) throw new Error("Minecraft operation view is unavailable.");
    const root = document.createElement("div");
    parsed.querySelectorAll("main > .notice.success, main > .notice.error").forEach((notice) => {
      root.appendChild(notice.cloneNode(true));
    });
    const section = source.cloneNode(true);
    section.classList.add("minecraft-workspace-section");
    root.appendChild(section);
    namespaceIDs(root, "minecraft-workspace-");
    content.replaceChildren(root);
  };

  const loadOperationsSection = async (sequence, sectionID, url = "/operations") => {
    const response = await fetch(url, {
      method: "GET",
      credentials: "same-origin",
      headers: { Accept: "text/html" },
      cache: "no-store",
      redirect: "follow",
    });
    if (handleWorkspaceAuth(response)) return;
    if (response.status === 403) throw new Error("Operator or Administrator access required.");
    const markup = await response.text();
    if (sequence !== loadSequence) return;
    renderOperationsMarkup(markup, sectionID);
  };

  const loadCurrentTab = async () => {
    const sequence = ++loadSequence;
    if (state) state.textContent = "Loading…";
    if (refreshButton) refreshButton.disabled = true;
    if (content) content.replaceChildren();
    try {
      if (settingsTabs.has(currentTab)) await loadSettings(sequence);
      else if (currentTab === "whitelist") await loadOperationsSection(sequence, "#whitelist");
      else if (currentTab === "logs") await loadOperationsSection(sequence, "#minecraft-logs");
      else await renderOverview(sequence);
      if (sequence === loadSequence && state) state.textContent = "";
    } catch (error) {
      if (sequence !== loadSequence) return;
      if (content) content.replaceChildren();
      if (state) state.textContent = error?.message || "Minecraft workspace is unavailable.";
    } finally {
      if (sequence === loadSequence && refreshButton) refreshButton.disabled = false;
    }
  };

  const selectTab = (tab) => {
    currentTab = tab;
    tabs.forEach((button) => button.setAttribute("aria-selected", button.dataset.minecraftTab === tab ? "true" : "false"));
    loadCurrentTab();
  };

  const submitWorkspaceForm = async (form, submitter) => {
    const action = new URL(form.getAttribute("action") || window.location.href, window.location.href);
    const allowedSettings = action.pathname === "/settings/server/plan" || action.pathname === "/settings/server/apply";
    const allowedWhitelist = action.pathname === "/operations/whitelist";
    if (!allowedSettings && !allowedWhitelist) return false;

    if (submitter) submitter.disabled = true;
    if (state) state.textContent = "Working…";
    const body = new URLSearchParams();
    new FormData(form).forEach((value, key) => body.append(key, String(value)));

    try {
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
      if (handleWorkspaceAuth(response)) return true;
      const markup = await response.text();
      if (allowedSettings) renderSettingsMarkup(markup, currentTab);
      else renderOperationsMarkup(markup, "#whitelist");
      if (state) state.textContent = "";
    } catch (error) {
      if (state) state.textContent = error?.message || "Minecraft operation could not be completed.";
    } finally {
      if (submitter && submitter.isConnected) submitter.disabled = false;
    }
    return true;
  };

  content?.addEventListener("submit", async (event) => {
    const form = event.target.closest("form");
    if (!form || !content.contains(form)) return;
    const action = new URL(form.getAttribute("action") || window.location.href, window.location.href);
    if (!["/settings/server/plan", "/settings/server/apply", "/operations/whitelist"].includes(action.pathname)) return;
    event.preventDefault();
    await submitWorkspaceForm(form, event.submitter);
  });

  content?.addEventListener("click", (event) => {
    const link = event.target.closest("a");
    if (!link || !content.contains(link)) return;
    const href = link.getAttribute("href") || "";
    if (href === "/settings/server" || href.startsWith("/settings/server?")) {
      event.preventDefault();
      selectTab(settingsTabs.has(currentTab) ? currentTab : "memory");
    }
  });

  const workspaceWindow = setupWorkspaceWindow(minecraftDialog, { onOpen: loadCurrentTab });

  minecraftOpen.addEventListener("click", () => {
    if (controlCenter) controlCenter.open = false;
    workspaceWindow?.open();
  });
  closeButton?.addEventListener("click", () => workspaceWindow?.close());
  refreshButton?.addEventListener("click", loadCurrentTab);
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
  const administratorTabs = new Set(["health", "users", "security"]);
  const upsTabButton = systemWorkspaceDialog.querySelector("[data-system-ups-tab]");
  const upsCSRF = document.querySelector("[data-system-ups-csrf]");
  let identity = null;
  let upsAvailable = false;
  let securityPane = "authentication";
  let currentTab = "health";
  let initialized = false;
  let loadSequence = 0;

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

  const parsePage = (markup) => new DOMParser().parseFromString(markup, "text/html");

  const cloneNotices = (parsed, root) => {
    parsed.querySelectorAll("main > .notice.success, main > .notice.error, main > .alert").forEach((notice) => {
      root.appendChild(notice.cloneNode(true));
    });
  };

  const renderHealthMarkup = (markup) => {
    if (!content) return;
    const parsed = parsePage(markup);
    const root = document.createElement("div");
    root.className = "system-health-view";
    parsed.querySelectorAll("main > .notice, main > .panel, main > .action-row").forEach((node) => {
      root.appendChild(node.cloneNode(true));
    });
    if (!root.children.length) throw new Error("System health result is unavailable.");
    systemReplaceText(root, [
      ["Validation service unavailable", "System health check unavailable"],
      ["JustVoxel could not run validation right now. No validation result was produced.", "JustVoxel could not check system health right now. No result was produced."],
      ["Validation passed", "System health check passed"],
      ["The authoritative validation backend completed without detecting a problem.", "JustVoxel completed the health check without detecting a problem."],
      ["Validation completed with detected problems", "System health check found problems"],
      ["The authoritative validation backend reported one or more problems. Review the details below.", "JustVoxel reported one or more problems. Review the details below."],
      ["Authoritative result", "System health"],
      ["Validation details", "Health check details"],
      ["The output below is shown as returned by the Management Agent. WebUI does not rerun or reinterpret these checks.", "Detailed result from the JustVoxel health checker."],
      ["Run validation again", "Check again"],
      ["Try again", "Check again"],
    ]);
    systemNamespaceIDs(root, "system-health-");
    content.replaceChildren(root);
  };

  const loadHealth = async (sequence) => {
    const response = await systemFetchPage("/settings/validation");
    if (!response) return;
    if (response.status === 403) throw new Error("Administrator access required.");
    if (!response.ok) throw new Error("System health check is unavailable.");
    const markup = await response.text();
    if (sequence !== loadSequence) return;
    renderHealthMarkup(markup);
  };

  const renderHistoryMarkup = (markup, administrator) => {
    if (!content) return;
    const parsed = parsePage(markup);
    const root = document.createElement("div");
    cloneNotices(parsed, root);
    parsed.querySelectorAll("main > section.panel.details").forEach((section) => {
      root.appendChild(section.cloneNode(true));
    });
    if (!root.children.length) throw new Error("System history is unavailable.");
    if (administrator) {
      systemReplaceText(root, [
        ["Recent appliance actions", "Detailed history"],
        ["Audit", "Administrator history"],
      ]);
    }
    systemNamespaceIDs(root, "system-history-");
    content.replaceChildren(root);
  };

  const loadHistory = async (sequence) => {
    const user = await loadIdentity();
    if (!user) return;
    const url = user.role === "administrator" ? "/settings/activity" : "/activity";
    const response = await systemFetchPage(url);
    if (!response) return;
    if (!response.ok) throw new Error("System history is unavailable.");
    const markup = await response.text();
    if (sequence !== loadSequence) return;
    renderHistoryMarkup(markup, user.role === "administrator");
  };

  const renderUsersMarkup = (markup) => {
    if (!content) return;
    const parsed = parsePage(markup);
    const root = document.createElement("div");
    cloneNotices(parsed, root);
    parsed.querySelectorAll("main > section.panel.details").forEach((section) => {
      root.appendChild(section.cloneNode(true));
    });
    if (!root.children.length) throw new Error("Users are unavailable.");
    systemNamespaceIDs(root, "system-users-");
    content.replaceChildren(root);
  };

  const loadUsers = async (sequence) => {
    const response = await systemFetchPage("/settings/users");
    if (!response) return;
    if (response.status === 403) throw new Error("Administrator access required.");
    if (!response.ok) throw new Error("Users are unavailable.");
    const markup = await response.text();
    if (sequence !== loadSequence) return;
    renderUsersMarkup(markup);
  };

  const compactSecurityRequirements = (root) => {
    root.querySelectorAll("p.muted").forEach((paragraph) => {
      const textValue = paragraph.textContent.trim();
      if (!textValue.startsWith("Password requirements:")) return;
      paragraph.textContent = textValue.split(" A stronger")[0];
    });
  };

  const buildSecurityPane = (markup, kind) => {
    const parsed = parsePage(markup);
    const panel = parsed.querySelector("main .settings-panel");
    const form = panel?.querySelector("form");
    if (!panel || !form) return null;
    const pane = document.createElement("section");
    pane.className = "system-security-pane";
    pane.dataset.systemSecurityPane = kind;
    panel.querySelectorAll(":scope > .alert, :scope > .notice").forEach((notice) => pane.appendChild(notice.cloneNode(true)));
    const heading = document.createElement("div");
    heading.className = "system-security-pane-heading";
    const title = document.createElement("h3");
    title.textContent = kind === "authentication" ? "Authentication" : "Password";
    const description = document.createElement("p");
    description.className = "muted compact";
    description.textContent = kind === "authentication"
      ? "Choose whether WebUI uses the voxel system password or its own separate password."
      : "Change the password used by the current WebUI authentication mode.";
    heading.append(title, description);
    pane.appendChild(heading);
    if (kind === "authentication") {
      const status = document.createElement("div");
      status.className = "system-security-status";
      const paragraphs = Array.from(panel.querySelectorAll(":scope > p"));
      [paragraphs.find((node) => node.textContent.includes("Administrator:")), paragraphs.find((node) => node.textContent.includes("Mode:"))].filter(Boolean).forEach((node) => {
        const item = document.createElement("span");
        item.textContent = node.textContent.trim();
        status.appendChild(item);
      });
      if (status.children.length) pane.appendChild(status);
    }
    const clonedForm = form.cloneNode(true);
    clonedForm.classList.add("system-security-form");
    compactSecurityRequirements(clonedForm);
    pane.appendChild(clonedForm);
    return pane;
  };

  const renderSecurityMarkup = (authenticationMarkup, passwordMarkup) => {
    if (!content) return;
    const root = document.createElement("div");
    root.className = "system-security-view";
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
    const authentication = buildSecurityPane(authenticationMarkup, "authentication");
    const password = buildSecurityPane(passwordMarkup, "password");
    if (authentication) root.appendChild(authentication);
    if (password) root.appendChild(password);
    if (!authentication && !password) throw new Error("Security settings are unavailable.");
    const showPane = (kind) => {
      securityPane = kind;
      root.querySelectorAll("[data-system-security-choice]").forEach((button) => button.setAttribute("aria-selected", button.dataset.systemSecurityChoice === kind ? "true" : "false"));
      root.querySelectorAll("[data-system-security-pane]").forEach((pane) => { pane.hidden = pane.dataset.systemSecurityPane !== kind; });
    };
    root.querySelectorAll("[data-system-security-choice]").forEach((button) => button.addEventListener("click", () => showPane(button.dataset.systemSecurityChoice)));
    systemNamespaceIDs(root, "system-security-");
    content.replaceChildren(root);
    showPane(securityPane);
  };

  const fetchSecurityPages = async () => {
    const [authenticationResponse, passwordResponse] = await Promise.all([
      systemFetchPage("/settings/authentication"),
      systemFetchPage("/password"),
    ]);
    if (!authenticationResponse || !passwordResponse) return null;
    if (authenticationResponse.status === 403 || passwordResponse.status === 403) {
      throw new Error("Administrator access required.");
    }
    if (!authenticationResponse.ok || !passwordResponse.ok) {
      throw new Error("Security settings are unavailable.");
    }
    return {
      authentication: await authenticationResponse.text(),
      password: await passwordResponse.text(),
    };
  };

  const loadSecurity = async (sequence) => {
    const pages = await fetchSecurityPages();
    if (!pages || sequence !== loadSequence) return;
    renderSecurityMarkup(pages.authentication, pages.password);
  };

  const renderAboutMarkup = (markup) => {
    if (!content) return;
    const parsed = parsePage(markup);
    const root = document.createElement("div");
    const badge = parsed.querySelector("main.about-shell .title-row .badge");
    if (badge) {
      const heading = document.createElement("div");
      heading.className = "system-about-variant";
      heading.appendChild(badge.cloneNode(true));
      root.appendChild(heading);
    }
    const panel = parsed.querySelector("main.about-shell .about-panel");
    if (panel) root.appendChild(panel.cloneNode(true));
    const disclaimer = parsed.querySelector("main.about-shell .disclaimer");
    if (disclaimer) root.appendChild(disclaimer.cloneNode(true));
    if (!root.children.length) throw new Error("About information is unavailable.");
    systemNamespaceIDs(root, "system-about-");
    content.replaceChildren(root);
  };

  const loadAbout = async (sequence) => {
    const response = await systemFetchPage("/about");
    if (!response) return;
    if (!response.ok) throw new Error("About information is unavailable.");
    const markup = await response.text();
    if (sequence !== loadSequence) return;
    renderAboutMarkup(markup);
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
      if (currentTab === "health") await loadHealth(sequence);
      else if (currentTab === "history") await loadHistory(sequence);
      else if (currentTab === "users") await loadUsers(sequence);
      else if (currentTab === "security") await loadSecurity(sequence);
      else if (currentTab === "ups") await loadUPS(sequence);
      else await loadAbout(sequence);
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
    currentTab = tab;
    syncSystemTabs();
    loadCurrentSystemTab();
  };

  const submitSystemForm = async (form, kind) => {
    const action = new URL(form.getAttribute("action") || window.location.href, window.location.href);
    const body = new URLSearchParams();
    new FormData(form).forEach((value, key) => body.append(key, String(value)));
    const submitter = form.querySelector('button[type="submit"],input[type="submit"]');
    if (submitter) submitter.disabled = true;
    if (state) state.textContent = "Working…";

    try {
      const response = await systemFetchPage(action.pathname + action.search, {
        method: (form.method || "POST").toUpperCase(),
        headers: {
          Accept: "text/html",
          "Content-Type": "application/x-www-form-urlencoded",
        },
        body: body.toString(),
      });
      if (!response) return;
      const markup = await response.text();

      if (kind === "history") {
        renderHistoryMarkup(markup, true);
      } else if (kind === "users") {
        renderUsersMarkup(markup);
      } else if (kind === "security-auth") {
        securityPane = "authentication";
        const passwordResponse = await systemFetchPage("/password");
        if (!passwordResponse) return;
        renderSecurityMarkup(markup, await passwordResponse.text());
      } else if (kind === "security-password") {
        securityPane = "password";
        const authenticationResponse = await systemFetchPage("/settings/authentication");
        if (!authenticationResponse) return;
        renderSecurityMarkup(await authenticationResponse.text(), markup);
      }
      if (state) state.textContent = response.ok ? "" : "The request was rejected. Review the message below.";
    } catch (error) {
      if (state) state.textContent = error?.message || "System operation could not be completed.";
    } finally {
      if (submitter && submitter.isConnected) submitter.disabled = false;
    }
  };

  content?.addEventListener("submit", async (event) => {
    const form = event.target.closest("form");
    if (!form || !content.contains(form)) return;
    const action = new URL(form.getAttribute("action") || window.location.href, window.location.href);

    if (action.pathname === "/api/ups/source") {
      event.preventDefault();
      await submitUPSSource(form);
      return;
    }

    let kind = "";
    if (action.pathname.startsWith("/settings/activity/notifications/")) kind = "history";
    else if (action.pathname.startsWith("/settings/users/")) kind = "users";
    else if (action.pathname === "/settings/authentication") kind = "security-auth";
    else if (action.pathname === "/password") kind = "security-password";
    if (!kind) return;

    event.preventDefault();
    await submitSystemForm(form, kind);
  });

  content?.addEventListener("click", (event) => {
    const link = event.target.closest("a");
    if (!link || !content.contains(link)) return;
    const href = link.getAttribute("href") || "";
    if (href === "/settings/validation" || href.startsWith("/settings/validation?")) {
      event.preventDefault();
      selectSystemTab("health");
    }
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
  closeButton?.addEventListener("click", () => workspaceWindow?.close());
  refreshButton?.addEventListener("click", loadCurrentSystemTab);
  tabs.forEach((button) => button.addEventListener("click", () => selectSystemTab(button.dataset.systemTab)));

  if (readWorkspaceWindowState("system").open) workspaceWindow?.open();
}
