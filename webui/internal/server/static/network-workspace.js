(() => {
  if (!document.querySelector('link[href="/static/network-workspace.css"]')) {
    const stylesheet = document.createElement("link");
    stylesheet.rel = "stylesheet";
    stylesheet.href = "/static/network-workspace.css";
    document.head.appendChild(stylesheet);
  }

  const openButton = document.querySelector("[data-network-open]");
  const dialog = document.querySelector("[data-network-workspace-dialog]");
  if (!openButton || !dialog || typeof setupWorkspaceWindow !== "function") return;

  const closeButton = dialog.querySelector("[data-network-close]");
  const refreshButton = dialog.querySelector("[data-network-refresh]");
  const state = dialog.querySelector("[data-network-state]");
  const content = dialog.querySelector("[data-network-content]");
  const csrf = document.querySelector("[data-network-csrf]")?.value || "";
  const controlCenter = document.querySelector("[data-control-center]");
  const checkpointStorageKey = "justvoxel-network-checkpoint";

  let loadSequence = 0;
  let currentSnapshot = null;
  let currentNetworks = new Map();
  let currentCheckpoint = null;
  let connectionDraft = null;
  let checkpointTimer = null;
  let recoveryTimer = null;

  const escapePath = (value) => encodeURIComponent(String(value || ""));

  const label = (value) => String(value || "unknown").replaceAll("-", " ");

  const signalBars = (strength) => {
    const value = Number(strength || 0);
    if (value >= 75) return "Excellent";
    if (value >= 50) return "Good";
    if (value >= 30) return "Fair";
    if (value > 0) return "Weak";
    return "Unknown";
  };

  const firstAddress = (config) => {
    const address = config?.addresses?.[0];
    if (!address?.address) return "Not assigned";
    return address.prefix ? address.address + "/" + address.prefix : address.address;
  };

  const clearRecoveryTimer = () => {
    if (recoveryTimer) window.clearTimeout(recoveryTimer);
    recoveryTimer = null;
  };

  const scheduleRecovery = () => {
    clearRecoveryTimer();
    recoveryTimer = window.setTimeout(() => loadNetwork(), 3000);
  };

  const requestJSON = async (url, options = {}) => {
    const response = await fetch(url, {
      method: options.method || "GET",
      credentials: "same-origin",
      headers: options.headers || { Accept: "application/json" },
      body: options.body,
      cache: "no-store",
    });
    if (response.redirected) {
      const target = new URL(response.url);
      if (target.pathname === "/login" || target.pathname === "/password") {
        window.location.assign(target.pathname + target.search);
        return null;
      }
    }
    if (response.status === 401) {
      window.location.assign("/login");
      return null;
    }
    const result = await response.json().catch(() => ({}));
    if (response.status === 403 && result.error === "password change required") {
      window.location.assign("/password");
      return null;
    }
    if (!response.ok) {
      const error = new Error(result.error || "Network request failed.");
      error.status = response.status;
      throw error;
    }
    return result;
  };

  const postForm = (url, values = {}) => {
    const body = new URLSearchParams({ csrf });
    Object.entries(values).forEach(([key, value]) => {
      if (Array.isArray(value)) {
        value.forEach((item) => body.append(key, String(item)));
      } else if (value !== undefined && value !== null) {
        body.set(key, String(value));
      }
    });
    return requestJSON(url, {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/x-www-form-urlencoded",
      },
      body: body.toString(),
    });
  };

  const row = (name, value) => {
    const item = document.createElement("div");
    item.className = "network-detail-row";
    const key = document.createElement("span");
    key.textContent = name;
    const val = document.createElement("strong");
    val.textContent = value || "—";
    item.append(key, val);
    return item;
  };

  const badge = (text, tone = "") => {
    const item = document.createElement("span");
    item.className = "network-badge" + (tone ? " " + tone : "");
    item.textContent = text;
    return item;
  };

  const actionButton = (text, datasetName, datasetValue, kind = "secondary") => {
    const button = document.createElement("button");
    button.type = "button";
    button.textContent = text;
    button.className = kind + " nav-admin-only network-action-button";
    button.dataset[datasetName] = datasetValue;
    return button;
  };

  const readStoredCheckpointID = () => {
    try {
      return window.sessionStorage.getItem(checkpointStorageKey) || "";
    } catch (_) {
      return "";
    }
  };

  const storeCheckpoint = (checkpoint) => {
    if (!checkpoint?.id) return;
    try {
      window.sessionStorage.setItem(checkpointStorageKey, checkpoint.id);
    } catch (_) {
      // Automatic NetworkManager rollback still protects connectivity.
    }
  };

  const clearStoredCheckpoint = () => {
    try {
      window.sessionStorage.removeItem(checkpointStorageKey);
    } catch (_) {
      // Nothing else is required.
    }
  };

  const createCheckpoint = async (interfaces) => {
    const unique = [...new Set((interfaces || []).filter(Boolean))];
    if (!unique.length) throw new Error("No Wi-Fi interface is available for a safety checkpoint.");
    const checkpoint = await postForm("/api/network/checkpoints", {
      interface: unique,
      rollback_timeout_seconds: 90,
    });
    if (!checkpoint?.id) throw new Error("Network safety checkpoint was not created.");
    storeCheckpoint(checkpoint);
    return checkpoint;
  };

  const rollbackStoredCheckpoint = async (id) => {
    if (!id) return null;
    try {
      return await postForm("/api/network/checkpoints/" + escapePath(id) + "/rollback");
    } catch (error) {
      if (error?.status === 404) return null;
      throw error;
    }
  };

  const runCheckpointedMutation = async (interfaces, mutate) => {
    const checkpoint = await createCheckpoint(interfaces);
    try {
      const result = await mutate(checkpoint.id);
      if (result?.checkpoint?.id) storeCheckpoint(result.checkpoint);
      return result;
    } catch (error) {
      try {
        await rollbackStoredCheckpoint(checkpoint.id);
        clearStoredCheckpoint();
      } catch (_) {
        // Keep the stored ID. NetworkManager still has the automatic timeout.
      }
      throw error;
    }
  };

  const renderOverview = (snapshot) => {
    const section = document.createElement("section");
    section.className = "panel details network-overview";

    const heading = document.createElement("div");
    heading.className = "section-heading";

    const primary = document.createElement("div");
    primary.className = "network-overview-primary";
    const eyebrow = document.createElement("p");
    eyebrow.className = "eyebrow";
    eyebrow.textContent = "Connectivity";

    const connectivity = snapshot.connectivity || "unknown";
    const title = document.createElement("h2");
    title.textContent =
      connectivity === "full" ? "Internet connected" :
      connectivity === "limited" ? "Limited connectivity" :
      connectivity === "portal" ? "Sign-in network detected" :
      connectivity === "none" ? "No internet connection" :
      "Network status unknown";
    primary.append(eyebrow, title);

    const status = document.createElement("div");
    status.className = "network-overview-status";
    status.append(
      badge(label(snapshot.state), snapshot.state?.startsWith("connected") ? "good" : ""),
      badge(snapshot.networking_enabled ? "Networking on" : "Networking off", snapshot.networking_enabled ? "good" : "danger")
    );

    const wifiDevices = (snapshot.devices || []).filter((device) => device.kind === "wifi");
    if (wifiDevices.length) {
      status.appendChild(
        badge(snapshot.wireless_enabled ? "Wi-Fi on" : "Wi-Fi off", snapshot.wireless_enabled ? "" : "muted")
      );
      const toggle = actionButton(
        snapshot.wireless_enabled ? "Turn Wi-Fi off" : "Turn Wi-Fi on",
        "networkWifiRadio",
        snapshot.wireless_enabled ? "off" : "on"
      );
      status.appendChild(toggle);
    } else {
      status.appendChild(badge("Wi-Fi not available", "muted"));
    }

    heading.append(primary, status);

    const subtitle = document.createElement("p");
    subtitle.className = "state-text network-overview-version";
    subtitle.textContent = "NetworkManager " + (snapshot.version || "unknown version");

    section.append(heading, subtitle);
    return section;
  };

  const renderCheckpoint = (checkpoint) => {
    const section = document.createElement("section");
    section.className = "network-checkpoint-banner";
    section.dataset.networkCheckpointBanner = checkpoint.id;

    const text = document.createElement("div");
    const title = document.createElement("strong");
    title.textContent = "Keep these network settings?";
    const detail = document.createElement("span");
    detail.dataset.networkCheckpointCountdown = checkpoint.expires_at || "";
    text.append(title, detail);

    const actions = document.createElement("div");
    const keep = document.createElement("button");
    keep.type = "button";
    keep.dataset.networkCheckpointKeep = checkpoint.id;
    keep.textContent = "Keep settings";
    const revert = document.createElement("button");
    revert.type = "button";
    revert.className = "secondary";
    revert.dataset.networkCheckpointRevert = checkpoint.id;
    revert.textContent = "Revert now";
    actions.append(revert, keep);

    section.append(text, actions);
    return section;
  };

  const updateCheckpointCountdown = () => {
    const element = content.querySelector("[data-network-checkpoint-countdown]");
    if (!element) return;
    const expires = Date.parse(element.dataset.networkCheckpointCountdown || "");
    if (!Number.isFinite(expires)) {
      element.textContent = "NetworkManager will restore the previous connection unless you confirm.";
      return;
    }
    const seconds = Math.max(0, Math.ceil((expires - Date.now()) / 1000));
    element.textContent = seconds > 0
      ? "Automatic rollback in " + seconds + " seconds."
      : "Rollback timeout reached. Checking network state…";
    if (seconds === 0) {
      clearStoredCheckpoint();
      currentCheckpoint = null;
      if (checkpointTimer) window.clearInterval(checkpointTimer);
      checkpointTimer = null;
      scheduleRecovery();
    }
  };

  const startCheckpointCountdown = () => {
    if (checkpointTimer) window.clearInterval(checkpointTimer);
    updateCheckpointCountdown();
    checkpointTimer = window.setInterval(updateCheckpointCountdown, 1000);
  };

  const renderDevice = (device) => {
    const card = document.createElement("article");
    card.className = "network-device-card";

    const heading = document.createElement("div");
    heading.className = "network-device-heading";
    const titleWrap = document.createElement("div");
    const kind = document.createElement("span");
    kind.className = "network-device-kind";
    kind.textContent = device.kind === "wifi" ? "Wi-Fi" : device.kind === "ethernet" ? "Ethernet" : "Network";
    const title = document.createElement("strong");
    title.textContent = device.interface || "Unknown interface";
    titleWrap.append(kind, title);

    const stateBadge = badge(label(device.state), device.state === "activated" ? "good" : device.state === "failed" ? "danger" : "");
    heading.append(titleWrap, stateBadge);
    card.appendChild(heading);

    const summary = document.createElement("div");
    summary.className = "network-device-summary";
    if (device.kind === "wifi") {
      const ssid = device.wireless?.ssid || "Not connected";
      summary.append(
        row("Network", ssid),
        row("Signal", device.wireless?.ssid ? signalBars(device.wireless?.signal) + " · " + (device.wireless?.signal || 0) + "%" : "—"),
        row("IPv4", firstAddress(device.ipv4))
      );
      if (device.active_connection) {
        const actions = document.createElement("div");
        actions.className = "network-inline-actions nav-admin-only";
        actions.appendChild(actionButton("Disconnect", "networkWifiDisconnect", device.interface, "secondary"));
        summary.appendChild(actions);
      }
    } else {
      summary.append(
        row("Link", device.carrier === false ? "Cable disconnected" : device.speed_mbps ? device.speed_mbps + " Mbps" : "Connected"),
        row("IPv4", firstAddress(device.ipv4)),
        row("Profile", device.active_connection?.id || "No active profile")
      );
    }

    const details = document.createElement("details");
    details.className = "network-device-details";
    const detailsSummary = document.createElement("summary");
    detailsSummary.textContent = "Connection details";
    const body = document.createElement("div");
    body.className = "network-detail-grid";
    body.append(
      row("Managed", device.managed ? "Yes" : "No"),
      row("Hardware address", device.hardware_address || "—"),
      row("MTU", device.mtu ? String(device.mtu) : "—"),
      row("Gateway", device.ipv4?.gateway || "—"),
      row("DNS", device.ipv4?.dns?.join(", ") || "—"),
      row("IPv6", firstAddress(device.ipv6)),
      row("Autoconnect", device.active_connection ? (device.active_connection.autoconnect ? "On" : "Off") : "—"),
      row("IPv4 mode", device.active_connection?.ipv4_method || "—"),
      row("IPv6 mode", device.active_connection?.ipv6_method || "—")
    );
    details.append(detailsSummary, body);
    card.append(summary, details);
    return card;
  };

  const supportedNewNetwork = (network) =>
    ["open", "owe", "wpa-personal", "wpa3-personal"].includes(network.security);

  const renderWiFiNetworks = (device, response) => {
    const section = document.createElement("section");
    section.className = "network-wifi-section";

    const heading = document.createElement("div");
    heading.className = "network-section-heading";
    const headingText = document.createElement("div");
    const title = document.createElement("h3");
    title.textContent = "Nearby Wi-Fi";
    const subtitle = document.createElement("span");
    subtitle.textContent = device.interface;
    headingText.append(title, subtitle);

    const headingActions = document.createElement("div");
    headingActions.className = "network-inline-actions";
    const other = actionButton("Other network…", "networkWifiOther", device.interface, "secondary");
    const scan = document.createElement("button");
    scan.type = "button";
    scan.className = "secondary network-scan-button";
    scan.dataset.networkScan = device.interface;
    scan.textContent = "Scan";
    headingActions.append(other, scan);
    heading.append(headingText, headingActions);
    section.appendChild(heading);

    const list = document.createElement("div");
    list.className = "network-wifi-list";
    const networks = response?.networks || [];
    if (networks.length === 0) {
      const empty = document.createElement("p");
      empty.className = "network-empty";
      empty.textContent = "No Wi-Fi networks are currently visible.";
      list.appendChild(empty);
    } else {
      networks.forEach((network) => {
        const item = document.createElement("div");
        item.className = "network-wifi-row" + (network.active ? " active" : "");
        const main = document.createElement("div");
        const name = document.createElement("strong");
        name.textContent = network.hidden ? "Hidden network" : network.ssid || "Unnamed network";
        const meta = document.createElement("span");
        const parts = [signalBars(network.strength), String(network.strength || 0) + "%", label(network.security)];
        if (network.known) parts.push("Saved");
        meta.textContent = parts.join(" · ");
        main.append(name, meta);
        item.appendChild(main);

        const actions = document.createElement("div");
        actions.className = "network-wifi-row-actions";
        if (network.active) {
          actions.appendChild(badge("Connected", "good"));
        } else if (network.profile_uuid) {
          const connect = actionButton("Connect", "networkWifiSavedConnect", device.interface);
          connect.dataset.profileUuid = network.profile_uuid;
          actions.appendChild(connect);
        } else if (!network.hidden && supportedNewNetwork(network)) {
          const join = actionButton("Join", "networkWifiJoin", device.interface);
          join.dataset.ssid = network.ssid || "";
          join.dataset.bssid = network.bssid || "";
          join.dataset.keyManagement = network.key_management || "";
          join.dataset.security = network.security || "";
          actions.appendChild(join);
        } else {
          const unavailable = document.createElement("span");
          unavailable.className = "network-wifi-unavailable";
          unavailable.textContent = network.security === "enterprise" ? "Saved profile required" : "Unsupported";
          actions.appendChild(unavailable);
        }
        item.appendChild(actions);
        list.appendChild(item);
      });
    }
    section.appendChild(list);
    return section;
  };

  const activeProfileUUIDs = (snapshot) => new Set(
    (snapshot.devices || [])
      .map((device) => device.active_connection?.uuid)
      .filter(Boolean)
  );

  const defaultWiFiInterface = (snapshot, profile) => {
    const devices = (snapshot.devices || []).filter((device) => device.kind === "wifi");
    if (!devices.length) return "";
    if (profile.interface_name && devices.some((device) => device.interface === profile.interface_name)) {
      return profile.interface_name;
    }
    return devices[0].interface;
  };

  const renderSavedWiFi = (snapshot) => {
    const profiles = (snapshot.profiles || []).filter((profile) => profile.type === "802-11-wireless");
    if (profiles.length === 0) return null;
    const active = activeProfileUUIDs(snapshot);
    const details = document.createElement("details");
    details.className = "network-saved";
    const summary = document.createElement("summary");
    summary.textContent = "Saved Wi-Fi networks (" + profiles.length + ")";
    const list = document.createElement("div");
    list.className = "network-saved-list";
    profiles.forEach((profile) => {
      const item = document.createElement("div");
      item.className = "network-saved-row";
      const info = document.createElement("div");
      const name = document.createElement("strong");
      name.textContent = profile.ssid || profile.id || "Saved Wi-Fi";
      const meta = document.createElement("span");
      meta.textContent = (profile.autoconnect ? "Autoconnect" : "Manual") + " · " + label(profile.key_management || "open");
      info.append(name, meta);
      item.appendChild(info);

      const actions = document.createElement("div");
      actions.className = "network-wifi-row-actions nav-admin-only";
      if (active.has(profile.uuid)) {
        actions.appendChild(badge("Connected", "good"));
      } else {
        const interfaceName = defaultWiFiInterface(snapshot, profile);
        if (interfaceName) {
          const connect = actionButton("Connect", "networkWifiSavedConnect", interfaceName);
          connect.dataset.profileUuid = profile.uuid;
          actions.appendChild(connect);
        }
        const forget = actionButton("Forget", "networkWifiForget", profile.uuid, "secondary");
        actions.appendChild(forget);
      }
      item.appendChild(actions);
      list.appendChild(item);
    });
    details.append(summary, list);
    return details;
  };

  const renderConnectPanel = () => {
    if (!connectionDraft) return null;
    const panel = document.createElement("section");
    panel.className = "network-connect-panel nav-admin-only";
    panel.dataset.networkConnectPanel = "true";

    const heading = document.createElement("div");
    const title = document.createElement("strong");
    title.textContent = connectionDraft.hidden ? "Join hidden network" : "Join " + connectionDraft.ssid;
    const subtitle = document.createElement("span");
    subtitle.textContent = connectionDraft.interface;
    heading.append(title, subtitle);
    panel.appendChild(heading);

    const form = document.createElement("form");
    form.dataset.networkConnectForm = "true";
    form.dataset.interface = connectionDraft.interface;
    form.dataset.bssid = connectionDraft.bssid || "";
    form.dataset.hidden = connectionDraft.hidden ? "true" : "false";

    if (connectionDraft.hidden) {
      const ssidLabel = document.createElement("label");
      ssidLabel.textContent = "Network name";
      const ssid = document.createElement("input");
      ssid.name = "ssid";
      ssid.required = true;
      ssid.maxLength = 32;
      ssid.autocomplete = "off";
      ssidLabel.appendChild(ssid);
      form.appendChild(ssidLabel);

      const securityLabel = document.createElement("label");
      securityLabel.textContent = "Security";
      const security = document.createElement("select");
      security.name = "key_management";
      [
        ["", "Open"],
        ["wpa-psk", "WPA/WPA2 Personal"],
        ["sae", "WPA3 Personal"],
        ["owe", "Enhanced Open (OWE)"],
      ].forEach(([value, text]) => {
        const option = document.createElement("option");
        option.value = value;
        option.textContent = text;
        security.appendChild(option);
      });
      securityLabel.appendChild(security);
      form.appendChild(securityLabel);
    } else {
      const ssid = document.createElement("input");
      ssid.type = "hidden";
      ssid.name = "ssid";
      ssid.value = connectionDraft.ssid;
      form.appendChild(ssid);
      const keyManagement = document.createElement("input");
      keyManagement.type = "hidden";
      keyManagement.name = "key_management";
      keyManagement.value = connectionDraft.keyManagement || "";
      form.appendChild(keyManagement);
    }

    const passwordLabel = document.createElement("label");
    passwordLabel.className = "network-connect-password";
    passwordLabel.textContent = "Password";
    const password = document.createElement("input");
    password.type = "password";
    password.name = "password";
    password.autocomplete = "new-password";
    password.spellcheck = false;
    passwordLabel.appendChild(password);
    form.appendChild(passwordLabel);

    const hint = document.createElement("small");
    hint.className = "network-connect-hint";
    hint.textContent = "Enterprise and WEP setup are not supported here. Saved Enterprise profiles can still be connected.";
    form.appendChild(hint);

    const actions = document.createElement("div");
    actions.className = "network-connect-actions";
    const cancel = document.createElement("button");
    cancel.type = "button";
    cancel.className = "secondary";
    cancel.dataset.networkConnectCancel = "true";
    cancel.textContent = "Cancel";
    const connect = document.createElement("button");
    connect.type = "submit";
    connect.textContent = "Connect";
    actions.append(cancel, connect);
    form.appendChild(actions);
    panel.appendChild(form);

    const updatePasswordVisibility = () => {
      const key = form.elements.key_management?.value || connectionDraft.keyManagement || "";
      const needsPassword = key === "wpa-psk" || key === "sae";
      passwordLabel.hidden = !needsPassword;
      password.required = needsPassword;
      if (!needsPassword) password.value = "";
    };
    form.elements.key_management?.addEventListener("change", updatePasswordVisibility);
    updatePasswordVisibility();

    return panel;
  };

  const renderNetwork = (checkpoint = currentCheckpoint) => {
    if (!currentSnapshot) return;
    currentCheckpoint = checkpoint || null;
    const root = document.createElement("div");
    root.className = "network-workspace-view";
    if (currentCheckpoint) root.appendChild(renderCheckpoint(currentCheckpoint));
    root.appendChild(renderOverview(currentSnapshot));

    const devices = document.createElement("section");
    devices.className = "network-devices";
    (currentSnapshot.devices || []).forEach((device) => devices.appendChild(renderDevice(device)));
    if (!currentSnapshot.devices?.length) {
      const empty = document.createElement("p");
      empty.className = "network-empty";
      empty.textContent = "No NetworkManager devices were reported.";
      devices.appendChild(empty);
    }
    root.appendChild(devices);

    const wifiDevices = (currentSnapshot.devices || []).filter((device) => device.kind === "wifi");
    wifiDevices.forEach((device) => {
      root.appendChild(renderWiFiNetworks(device, currentNetworks.get(device.interface)));
    });

    const saved = renderSavedWiFi(currentSnapshot);
    if (saved) root.appendChild(saved);

    const connectPanel = renderConnectPanel();
    if (connectPanel) root.appendChild(connectPanel);

    content.replaceChildren(root);
    if (currentCheckpoint) startCheckpointCountdown();
  };

  const resumeCheckpoint = async () => {
    const id = readStoredCheckpointID();
    if (!id) return null;
    try {
      return await requestJSON("/api/network/checkpoints/" + escapePath(id));
    } catch (error) {
      if (error?.status === 404) {
        clearStoredCheckpoint();
        return null;
      }
      throw error;
    }
  };

  const loadNetwork = async () => {
    const sequence = ++loadSequence;
    clearRecoveryTimer();
    refreshButton.disabled = true;
    state.textContent = "Loading network…";
    try {
      const [snapshot, checkpoint] = await Promise.all([
        requestJSON("/api/network"),
        resumeCheckpoint(),
      ]);
      if (!snapshot || sequence !== loadSequence) return;

      currentSnapshot = snapshot;
      currentNetworks = new Map();
      const wifiDevices = (snapshot.devices || []).filter((device) => device.kind === "wifi");
      for (const device of wifiDevices) {
        const networks = await requestJSON("/api/network/wifi/" + escapePath(device.interface) + "/networks");
        if (sequence !== loadSequence) return;
        currentNetworks.set(device.interface, networks);
      }

      currentCheckpoint = checkpoint;
      renderNetwork();
      state.textContent = "";
    } catch (error) {
      if (sequence !== loadSequence) return;
      state.textContent = error?.message || "Network information is unavailable.";
      if (readStoredCheckpointID()) {
        state.textContent += " Waiting for the network to recover…";
        scheduleRecovery();
      }
    } finally {
      if (sequence === loadSequence) refreshButton.disabled = false;
    }
  };

  const requestScan = async (interfaceName, button) => {
    button.disabled = true;
    button.textContent = "Scanning…";
    try {
      await postForm("/api/network/wifi/" + escapePath(interfaceName) + "/scan");
      window.setTimeout(loadNetwork, 900);
    } catch (error) {
      state.textContent = error?.message || "Wi-Fi scan could not be started.";
    } finally {
      button.disabled = false;
      button.textContent = "Scan";
    }
  };

  const setRadio = async (enabled, button) => {
    button.disabled = true;
    state.textContent = enabled ? "Turning Wi-Fi on…" : "Turning Wi-Fi off…";
    try {
      if (enabled) {
        await postForm("/api/network/wifi/radio", { enabled: true });
        await loadNetwork();
        return;
      }
      const interfaces = (currentSnapshot?.devices || [])
        .filter((device) => device.kind === "wifi")
        .map((device) => device.interface);
      await runCheckpointedMutation(interfaces, (checkpointID) =>
        postForm("/api/network/wifi/radio", { enabled: false, checkpoint_id: checkpointID })
      );
      await loadNetwork();
    } catch (error) {
      state.textContent = error?.message || "Wi-Fi radio state could not be changed.";
      if (readStoredCheckpointID()) scheduleRecovery();
    } finally {
      button.disabled = false;
    }
  };

  const connectSaved = async (interfaceName, profileUUID, button) => {
    button.disabled = true;
    state.textContent = "Connecting Wi-Fi…";
    try {
      await runCheckpointedMutation([interfaceName], (checkpointID) =>
        postForm("/api/network/wifi/" + escapePath(interfaceName) + "/connect", {
          checkpoint_id: checkpointID,
          profile_uuid: profileUUID,
        })
      );
      await loadNetwork();
    } catch (error) {
      state.textContent = error?.message || "Saved Wi-Fi network could not be connected.";
      if (readStoredCheckpointID()) scheduleRecovery();
    } finally {
      button.disabled = false;
    }
  };

  const disconnectWiFi = async (interfaceName, button) => {
    button.disabled = true;
    state.textContent = "Disconnecting Wi-Fi…";
    try {
      await runCheckpointedMutation([interfaceName], (checkpointID) =>
        postForm("/api/network/wifi/" + escapePath(interfaceName) + "/disconnect", {
          checkpoint_id: checkpointID,
        })
      );
      await loadNetwork();
    } catch (error) {
      state.textContent = error?.message || "Wi-Fi could not be disconnected.";
      if (readStoredCheckpointID()) scheduleRecovery();
    } finally {
      button.disabled = false;
    }
  };

  const forgetWiFi = async (profileUUID, button) => {
    button.disabled = true;
    state.textContent = "Forgetting saved Wi-Fi…";
    try {
      await postForm("/api/network/wifi/profiles/" + escapePath(profileUUID) + "/forget");
      await loadNetwork();
    } catch (error) {
      state.textContent = error?.message || "Saved Wi-Fi could not be forgotten.";
    } finally {
      button.disabled = false;
    }
  };

  const submitNewWiFi = async (form) => {
    const button = form.querySelector('button[type="submit"]');
    if (button) button.disabled = true;
    const interfaceName = form.dataset.interface || "";
    const passwordInput = form.elements.password;
    const password = passwordInput?.value || "";
    if (passwordInput) passwordInput.value = "";

    state.textContent = "Connecting Wi-Fi…";
    try {
      await runCheckpointedMutation([interfaceName], (checkpointID) =>
        postForm("/api/network/wifi/" + escapePath(interfaceName) + "/connect", {
          checkpoint_id: checkpointID,
          ssid: form.elements.ssid?.value || "",
          bssid: form.dataset.bssid || "",
          key_management: form.elements.key_management?.value || connectionDraft?.keyManagement || "",
          password,
          hidden: form.dataset.hidden === "true",
        })
      );
      connectionDraft = null;
      await loadNetwork();
    } catch (error) {
      state.textContent = error?.message || "Wi-Fi network could not be connected.";
      if (readStoredCheckpointID()) scheduleRecovery();
      if (button) button.disabled = false;
    }
  };

  const confirmCheckpoint = async (id, button) => {
    button.disabled = true;
    state.textContent = "Keeping network settings…";
    try {
      await postForm("/api/network/checkpoints/" + escapePath(id) + "/confirm");
      clearStoredCheckpoint();
      currentCheckpoint = null;
      await loadNetwork();
    } catch (error) {
      state.textContent = error?.message || "Network settings could not be confirmed.";
      button.disabled = false;
    }
  };

  const revertCheckpoint = async (id, button) => {
    button.disabled = true;
    state.textContent = "Restoring previous network settings…";
    try {
      await rollbackStoredCheckpoint(id);
      clearStoredCheckpoint();
      currentCheckpoint = null;
      connectionDraft = null;
      scheduleRecovery();
    } catch (error) {
      state.textContent = error?.message || "Previous network settings could not be restored.";
      button.disabled = false;
    }
  };

  content.addEventListener("click", (event) => {
    const scan = event.target.closest("[data-network-scan]");
    if (scan) {
      requestScan(scan.dataset.networkScan, scan);
      return;
    }

    const radio = event.target.closest("[data-network-wifi-radio]");
    if (radio) {
      setRadio(radio.dataset.networkWifiRadio === "on", radio);
      return;
    }

    const saved = event.target.closest("[data-network-wifi-saved-connect]");
    if (saved) {
      connectSaved(saved.dataset.networkWifiSavedConnect, saved.dataset.profileUuid, saved);
      return;
    }

    const disconnect = event.target.closest("[data-network-wifi-disconnect]");
    if (disconnect) {
      disconnectWiFi(disconnect.dataset.networkWifiDisconnect, disconnect);
      return;
    }

    const forget = event.target.closest("[data-network-wifi-forget]");
    if (forget) {
      forgetWiFi(forget.dataset.networkWifiForget, forget);
      return;
    }

    const join = event.target.closest("[data-network-wifi-join]");
    if (join) {
      connectionDraft = {
        interface: join.dataset.networkWifiJoin,
        ssid: join.dataset.ssid || "",
        bssid: join.dataset.bssid || "",
        keyManagement: join.dataset.keyManagement || "",
        security: join.dataset.security || "",
        hidden: false,
      };
      renderNetwork();
      content.querySelector("[data-network-connect-panel]")?.scrollIntoView({ behavior: "smooth", block: "nearest" });
      return;
    }

    const other = event.target.closest("[data-network-wifi-other]");
    if (other) {
      connectionDraft = {
        interface: other.dataset.networkWifiOther,
        ssid: "",
        bssid: "",
        keyManagement: "",
        hidden: true,
      };
      renderNetwork();
      content.querySelector("[data-network-connect-panel]")?.scrollIntoView({ behavior: "smooth", block: "nearest" });
      return;
    }

    if (event.target.closest("[data-network-connect-cancel]")) {
      connectionDraft = null;
      renderNetwork();
      return;
    }

    const keep = event.target.closest("[data-network-checkpoint-keep]");
    if (keep) {
      confirmCheckpoint(keep.dataset.networkCheckpointKeep, keep);
      return;
    }

    const revert = event.target.closest("[data-network-checkpoint-revert]");
    if (revert) {
      revertCheckpoint(revert.dataset.networkCheckpointRevert, revert);
    }
  });

  content.addEventListener("submit", (event) => {
    const form = event.target.closest("[data-network-connect-form]");
    if (!form) return;
    event.preventDefault();
    submitNewWiFi(form);
  });

  const workspace = setupWorkspaceWindow(dialog, { onOpen: loadNetwork });
  openButton.addEventListener("click", () => {
    if (controlCenter) controlCenter.open = false;
    workspace?.open();
  });
  closeButton?.addEventListener("click", () => workspace?.close());
  refreshButton?.addEventListener("click", loadNetwork);

  if (readWorkspaceWindowState("network").open) workspace?.open();
})();
