(() => {
  const dashboard = document.querySelector("#dashboard[data-dashboard-status][data-session-info]");
  if (!dashboard) return;

  const choice = document.querySelector("[data-first-run-choice]");
  const explore = document.querySelector("[data-first-run-explore]");
  const invitation = document.querySelector("[data-minecraft-setup-invitation]");
  const exploreStorageKey = "justvoxel:first-run:explore";
  let role = "";
  let configured;
  let refreshTimer = 0;

  const stopRefreshTimer = () => {
    if (!refreshTimer) return;
    window.clearInterval(refreshTimer);
    refreshTimer = 0;
  };

  const hideFirstRunUI = () => {
    if (choice) choice.hidden = true;
    if (invitation) invitation.hidden = true;
  };

  const showInvitation = () => {
    if (choice) choice.hidden = true;
    if (invitation) invitation.hidden = false;
  };

  const showChoice = () => {
    if (invitation) invitation.hidden = true;
    if (choice) choice.hidden = false;
  };

  const applyFirstRunState = () => {
    if (role !== "administrator" || configured === undefined) {
      hideFirstRunUI();
      return;
    }

    if (configured === true) {
      hideFirstRunUI();
      stopRefreshTimer();
      try {
        window.localStorage.removeItem(exploreStorageKey);
      } catch (_) {
        // Storage can be unavailable in hardened/private browser modes.
      }
      return;
    }

    let explored = false;
    try {
      explored = window.localStorage.getItem(exploreStorageKey) === "true";
    } catch (_) {
      // Fall back to showing the choice when browser storage is unavailable.
    }

    if (explored) showInvitation();
    else showChoice();

    if (!refreshTimer) {
      refreshTimer = window.setInterval(() => {
        void loadConfigurationState();
      }, 15000);
    }
  };

  const loadRole = async () => {
    try {
      const response = await fetch(dashboard.dataset.sessionInfo, {
        method: "GET",
        credentials: "same-origin",
        headers: { Accept: "application/json" },
        cache: "no-store",
      });
      if (!response.ok) return;
      const identity = await response.json();
      role = String(identity.role || "").toLowerCase();
      applyFirstRunState();
    } catch (_) {
      hideFirstRunUI();
    }
  };

  const loadConfigurationState = async () => {
    try {
      const response = await fetch(dashboard.dataset.dashboardStatus, {
        method: "GET",
        credentials: "same-origin",
        headers: { Accept: "application/json" },
        cache: "no-store",
      });
      if (!response.ok) return;
      const snapshot = await response.json();
      configured = Boolean(snapshot?.status?.minecraft?.configured);
      applyFirstRunState();
    } catch (_) {
      // Keep the last known first-run state when the management service is temporarily unavailable.
    }
  };

  explore?.addEventListener("click", () => {
    try {
      window.localStorage.setItem(exploreStorageKey, "true");
    } catch (_) {
      // The invitation still appears for this page load when storage is unavailable.
    }
    showInvitation();
  });

  void Promise.all([loadRole(), loadConfigurationState()]);
})();
