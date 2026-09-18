(() => {
  const dashboard = document.querySelector("#dashboard[data-dashboard-status][data-session-info]");
  if (!dashboard) return;

  let role = "";
  let configured;
  let redirected = false;

  const maybeRedirect = () => {
    if (redirected || role !== "administrator" || configured !== false) return;
    redirected = true;
    window.location.replace("/setup");
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
      maybeRedirect();
    } catch (_) {
      // The normal dashboard handles management-service errors.
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
      maybeRedirect();
    } catch (_) {
      // The normal dashboard handles management-service errors.
    }
  };

  void Promise.all([loadRole(), loadConfigurationState()]);
})();
