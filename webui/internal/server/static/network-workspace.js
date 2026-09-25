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
  let loadSequence = 0;

  const escapePath = (value) => encodeURIComponent(String(value || ""));

  const handleAuth = (response) => {
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
      window.location.assign("/password");
      return true;
    }
    return false;
  };

  const getJSON = async (url, options = {}) => {
    const response = await fetch(url, {
      method: options.method || "GET",
      credentials: "same-origin",
      headers: options.headers || { Accept: "application/json" },
      body: options.body,
      cache: "no-store",
    });
    if (handleAuth(response)) return null;
    const result = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(result.error || "Network information is unavailable.");
    return result;
  };

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

  const renderOverview = (snapshot) => {
    const section = document.createElement("section");
    section.className = "network-overview";

    const primary = document.createElement("div");
    primary.className = "network-overview-primary";
    const connectivity = snapshot.connectivity || "unknown";
    const title = document.createElement("strong");
    title.textContent =
      connectivity === "full" ? "Internet connected" :
      connectivity === "limited" ? "Limited connectivity" :
      connectivity === "portal" ? "Sign-in network detected" :
      connectivity === "none" ? "No internet connection" :
      "Network status unknown";
    const subtitle = document.createElement("span");
    subtitle.textContent = "NetworkManager " + (snapshot.version || "unknown version");
    primary.append(title, subtitle);

    const status = document.createElement("div");
    status.className = "network-overview-status";
    status.append(
      badge(label(snapshot.state), snapshot.state?.startsWith("connected") ? "good" : ""),
      badge(snapshot.networking_enabled ? "Networking on" : "Networking off", snapshot.networking_enabled ? "good" : "danger"),
      badge(snapshot.wireless_enabled ? "Wi-Fi on" : "Wi-Fi off", snapshot.wireless_enabled ? "" : "muted")
    );

    section.append(primary, status);
    return section;
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

    const scan = document.createElement("button");
    scan.type = "button";
    scan.className = "secondary network-scan-button";
    scan.dataset.networkScan = device.interface;
    scan.textContent = "Scan";
    heading.append(headingText, scan);
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
        if (network.active) item.appendChild(badge("Connected", "good"));
        list.appendChild(item);
      });
    }
    section.appendChild(list);
    return section;
  };

  const renderSavedWiFi = (snapshot) => {
    const profiles = (snapshot.profiles || []).filter((profile) => profile.type === "802-11-wireless");
    if (profiles.length === 0) return null;
    const details = document.createElement("details");
    details.className = "network-saved";
    const summary = document.createElement("summary");
    summary.textContent = "Saved Wi-Fi networks (" + profiles.length + ")";
    const list = document.createElement("div");
    list.className = "network-saved-list";
    profiles.forEach((profile) => {
      const item = document.createElement("div");
      item.className = "network-saved-row";
      const name = document.createElement("strong");
      name.textContent = profile.ssid || profile.id || "Saved Wi-Fi";
      const meta = document.createElement("span");
      meta.textContent = (profile.autoconnect ? "Autoconnect" : "Manual") + " · " + label(profile.key_management || "open");
      item.append(name, meta);
      list.appendChild(item);
    });
    details.append(summary, list);
    return details;
  };

  const loadNetwork = async () => {
    const sequence = ++loadSequence;
    refreshButton.disabled = true;
    state.textContent = "Loading network…";
    content.replaceChildren();
    try {
      const snapshot = await getJSON("/api/network");
      if (!snapshot || sequence !== loadSequence) return;

      const root = document.createElement("div");
      root.className = "network-workspace-view";
      root.appendChild(renderOverview(snapshot));

      const devices = document.createElement("section");
      devices.className = "network-devices";
      (snapshot.devices || []).forEach((device) => devices.appendChild(renderDevice(device)));
      if (!snapshot.devices?.length) {
        const empty = document.createElement("p");
        empty.className = "network-empty";
        empty.textContent = "No NetworkManager devices were reported.";
        devices.appendChild(empty);
      }
      root.appendChild(devices);

      const wifiDevices = (snapshot.devices || []).filter((device) => device.kind === "wifi");
      for (const device of wifiDevices) {
        const networks = await getJSON("/api/network/wifi/" + escapePath(device.interface) + "/networks");
        if (sequence !== loadSequence) return;
        root.appendChild(renderWiFiNetworks(device, networks));
      }

      const saved = renderSavedWiFi(snapshot);
      if (saved) root.appendChild(saved);

      content.replaceChildren(root);
      state.textContent = "";
    } catch (error) {
      if (sequence !== loadSequence) return;
      state.textContent = error?.message || "Network information is unavailable.";
    } finally {
      if (sequence === loadSequence) refreshButton.disabled = false;
    }
  };

  const requestScan = async (interfaceName, button) => {
    button.disabled = true;
    button.textContent = "Scanning…";
    try {
      const body = new URLSearchParams({ csrf });
      const response = await fetch("/api/network/wifi/" + escapePath(interfaceName) + "/scan", {
        method: "POST",
        credentials: "same-origin",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/x-www-form-urlencoded",
        },
        body: body.toString(),
        cache: "no-store",
      });
      if (handleAuth(response)) return;
      const result = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(result.error || "Wi-Fi scan could not be started.");
      window.setTimeout(loadNetwork, 900);
    } catch (error) {
      state.textContent = error?.message || "Wi-Fi scan could not be started.";
    } finally {
      button.disabled = false;
      button.textContent = "Scan";
    }
  };

  content.addEventListener("click", (event) => {
    const button = event.target.closest("[data-network-scan]");
    if (!button) return;
    requestScan(button.dataset.networkScan, button);
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
