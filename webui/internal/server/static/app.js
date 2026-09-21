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
      title: "Restart JustVoxel?",
      message: "Minecraft will be stopped safely first when it is running. The server will then restart.",
      confirm: "Restart",
      working: "Restarting JustVoxel…",
    },
    poweroff: {
      title: "Power off JustVoxel?",
      message: "Minecraft will be stopped safely first when it is running. The server will then power off.",
      confirm: "Power off",
      working: "Powering off JustVoxel…",
    },
    "firmware-reboot": {
      title: "Restart to UEFI/BIOS?",
      message: "Minecraft will be stopped safely first. The server will restart into the physical machine's UEFI/BIOS setup.",
      confirm: "Restart to UEFI/BIOS",
      working: "Restarting to UEFI/BIOS…",
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
        systemPower.open = false;
        return false;
      }
      if (!response.ok) throw new Error("status unavailable");
      latestStatus = await response.json();
      const firmwareAvailable =
        latestStatus.variant === "hwe" &&
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
    systemPower.open = false;
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
      confirmButton.textContent = selectedAction === "poweroff" ? "Power off anyway" : "Restart anyway";
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

  systemPower.addEventListener("toggle", () => {
    if (systemPower.open) loadSystemActionsStatus();
  });

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
    systemPower.open = false;
  });
}
