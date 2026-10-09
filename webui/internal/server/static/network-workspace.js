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

  let selectedTab = "Overview";
  let remoteProviders = [];
  let remoteError = "";
  let playitSetup = { state: "idle" };
  let playitPopup = null;
  let playitClaimOpened = false;
  let remoteBusy = false;
  let netbirdLoginURL = "";
  let tailscaleLoginURL = "";
  const validTailscaleLoginURL = (value) => {
    if (typeof value !== "string" || value.length > 2048 || /\s/.test(value)) return false;
    try {
      const url = new URL(value);
      return url.protocol === "https:" && url.host === "login.tailscale.com" &&
        url.pathname.startsWith("/a/") && url.pathname.length > 3 && !url.username && !url.password;
    } catch (_) { return false; }
  };
  const providerDashboards = { tailscale: "https://console.tailscale.com/admin/", netbird: "https://app.netbird.io/" };
  const pendingDashboards = new Set();
  const providerProgress = new Map();
  const validNetbirdLoginURL = (value) => {
    if (typeof value !== "string" || value.length > 2048 || /\s/.test(value) || /[<>"\x27]/.test(value)) return false;
    try {
      const url = new URL(value);
      const host = url.hostname.toLowerCase();
      return url.protocol === "https:" && Boolean(host) && !url.username && !url.password &&
        host !== "tailscale.com" && !host.endsWith(".tailscale.com");
    } catch (_) { return false; }
  };
  const openActivatedDashboards = () => {
    for (const id of pendingDashboards) {
      const provider = remoteProviders.find((item) => item.id === id);
      if (!provider || provider.status_unavailable || providerTransitional(provider)) continue;
      if (!provider.service_active) {
        pendingDashboards.delete(id);
        providerProgress.set(id, "Service activation has not completed. Refresh to check its status.");
        continue;
      }
      pendingDashboards.delete(id);
      let popup = null;
      try {
        // Open the actual destination only after the service reports running.
        popup = window.open(providerDashboards[id], "_blank");
        if (popup) popup.opener = null;
      } catch (_) { /* The fixed dashboard link remains available. */ }
      providerProgress.set(id, popup
        ? "Service activated. Complete account setup in the provider dashboard."
        : "Service activated. Use Open provider dashboard to complete account setup; your browser may have blocked automatic opening.");
    }
  };
  let remotePollSequence = 0;
  const providerTransitional = (provider) => ["activating", "deactivating", "reloading", "refreshing"].includes(provider.service_state);
  const setupRunning = () => ["starting", "waiting"].includes(playitSetup.state);
  const providerControls = (provider) => {
    const blocked = !provider.installed || remoteBusy || provider.service_state === "deactivating";
    return {
      activateDisabled: blocked || (providerTransitional(provider) && !(provider.id === "netbird" && provider.service_state === "activating")) || (["netbird", "tailscale"].includes(provider.id) ? provider.connected : provider.service_active) || (provider.id === "playit" && setupRunning()),
      deactivateDisabled: blocked || (!(provider.id === "playit" && setupRunning()) && !provider.service_active && !provider.service_enabled && provider.service_state === "inactive")
    };
  };
  const effectiveTabs = (snapshot) => (snapshot?.workspace_tabs || ["Overview"])
    .filter((tab) => tab !== "Wi-Fi" || (snapshot?.devices || []).some((device) => device.kind === "wifi"));
  const connectivitySummary = (value) => ({
    full: "Internet connected", limited: "Limited connectivity", portal: "Sign-in network detected",
    none: "No internet connection"
  }[value] || "Network status unknown");
  const validPlayitClaim = (url) => /^https:\/\/playit\.gg\/claim\/[0-9a-fA-F]{10}(?![\s\S])$/.test(url || "");
  const renderPlayitPopup = (failed = false) => {
    if (!playitPopup || playitPopup.closed) return;
    playitPopup.document.open();
    if (failed) {
      playitPopup.document.write(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Playit setup</title>
<style>
:root{color-scheme:dark;font-family:system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#eef3f5;background:#0b1117}
*{box-sizing:border-box}body{margin:0;min-height:100vh;display:flex;flex-direction:column;background:#0b1117}
header{padding:18px 24px;border-bottom:1px solid #26343e;background:#0d151d;font-size:.75rem;font-weight:650;letter-spacing:.14em;color:#c8d0d6}
header .separator{margin:0 10px;color:#667782}header .brand,.eyebrow{color:#78c99d}
main{flex:1;display:grid;place-items:center;padding:32px 20px}.setup-card{width:100%;max-width:580px;padding:36px;border:1px solid #294c40;border-radius:18px;background:#111c24}
h1{margin:0 0 14px;font-size:clamp(1.4rem,4vw,1.8rem);line-height:1.25}p{font-size:.9rem;line-height:1.65;color:#ced8df}.muted{color:#9aa6b2}
.claim-panel{margin-top:28px;padding:20px;border:1px solid #273b49;border-radius:12px;background:#0d1822}.claim-heading{font-size:.82rem}
</style>
<link rel="stylesheet" href="/static/playit-setup.css">
</head>
<body>
<header aria-label="JustVoxel Playit"><span>JUSTVOXEL</span><span class="separator" aria-hidden="true">·</span><span class="brand">PLAYIT</span></header>
<main>
<section class="setup-card failure" aria-labelledby="setup-title">
<p class="eyebrow">PLAYIT SETUP</p>
<h1 id="setup-title">Playit setup could not be started.</h1>
<p>Return to JustVoxel and try Activate again.</p>
</section>
</main>
</body>
</html>`);
    } else {
      playitPopup.document.write(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Playit setup</title>
<style>
:root{color-scheme:dark;font-family:system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#eef3f5;background:#0b1117}
*{box-sizing:border-box}body{margin:0;min-height:100vh;display:flex;flex-direction:column;background:#0b1117}
header{padding:18px 24px;border-bottom:1px solid #26343e;background:#0d151d;font-size:.75rem;font-weight:650;letter-spacing:.14em;color:#c8d0d6}
header .separator{margin:0 10px;color:#667782}header .brand,.eyebrow{color:#78c99d}
main{flex:1;display:grid;place-items:center;padding:32px 20px}.setup-card{width:100%;max-width:580px;padding:36px;border:1px solid #294c40;border-radius:18px;background:#111c24}
h1{margin:0 0 14px;font-size:clamp(1.4rem,4vw,1.8rem);line-height:1.25}p{font-size:.9rem;line-height:1.65;color:#ced8df}.muted{color:#9aa6b2}
.claim-panel{margin-top:28px;padding:20px;border:1px solid #273b49;border-radius:12px;background:#0d1822}.claim-heading{font-size:.82rem}
</style>
<link rel="stylesheet" href="/static/playit-setup.css">
</head>
<body>
<header aria-label="JustVoxel Playit"><span>JUSTVOXEL</span><span class="separator" aria-hidden="true">·</span><span class="brand">PLAYIT</span></header>
<main>
<section class="setup-card" aria-labelledby="setup-title">
<p class="eyebrow">PLAYIT SETUP</p>
<h1 id="setup-title">Preparing Playit setup…</h1>
<p>Starting Playit and generating your secure setup link.</p>
<p class="muted">This page will open Playit automatically when ready.</p>
<div class="claim-panel" role="status">
<h2 class="claim-heading"><span class="pulse-dot" aria-hidden="true"></span>Claim link</h2>
<p>Waiting for Playit to generate the secure claim link.</p>
</div>
</section>
</main>
</body>
</html>`);
    }
    playitPopup.document.close();
  };
  const failPlayitPopup = () => {
    try { renderPlayitPopup(true); } catch (_) { /* Popup may be unavailable. */ }
    // Keep the fixed failure page open, outside subsequent popup cleanup.
    playitPopup = null;
  };
  const closePlayitPopup = () => {
    try { playitPopup?.close(); } catch (_) { /* Browser may have closed it already. */ }
    playitPopup = null;
  };
  const updatePlayitPopup = () => {
    if (playitSetup.state === "waiting" && validPlayitClaim(playitSetup.claim_url) && playitPopup) {
      try {
        if (!playitPopup.closed) {
          playitPopup.location.replace(playitSetup.claim_url);
          playitClaimOpened = true;
        }
      } catch (_) { closePlayitPopup(); /* Offer the validated fallback link. */ }
      playitPopup = null;
    } else if (playitSetup.state === "failed") {
      failPlayitPopup();
    } else if (["idle", "complete"].includes(playitSetup.state)) {
      closePlayitPopup();
    }
    if (playitSetup.state === "failed") remoteError = "Playit setup failed or timed out.";
  };
  const isAdministrator = () => (currentSnapshot?.workspace_tabs || []).includes("Ethernet");
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
    return address.prefix !== undefined ? address.address + "/" + address.prefix : address.address;
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
    item.className = "badge network-badge" + (tone ? " " + tone : "");
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
    if (!unique.length) throw new Error("No interface is available for a safety checkpoint.");
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

  const sectionHeading = (eyebrowText, titleText) => {
    const heading = document.createElement("div");
    heading.className = "section-heading";
    const copy = document.createElement("div");
    const eyebrow = document.createElement("p");
    eyebrow.className = "eyebrow";
    eyebrow.textContent = eyebrowText;
    const title = document.createElement("h2");
    title.textContent = titleText;
    copy.append(eyebrow, title);
    heading.appendChild(copy);
    return heading;
  };

  // Bound variable-length Wi-Fi lists without hiding networks or adding a scroller.
  const paginateList = (list, pageSize) => {
    const items = [...list.children];
    if (items.length <= pageSize) return null;
    let page = 0;
    const pages = Math.ceil(items.length / pageSize);
    const controls = document.createElement("div");
    controls.className = "network-list-pages";
    const previous = document.createElement("button");
    const next = document.createElement("button");
    const status = document.createElement("span");
    status.setAttribute("aria-live", "polite");
    previous.type = next.type = "button";
    previous.className = next.className = "secondary network-action-button";
    previous.textContent = "Previous";
    next.textContent = "Next";
    const update = () => {
      items.forEach((item, index) => { item.hidden = Math.floor(index / pageSize) !== page; });
      previous.disabled = page === 0;
      next.disabled = page === pages - 1;
      status.textContent = "Page " + (page + 1) + " of " + pages;
    };
    previous.addEventListener("click", () => { page--; update(); });
    next.addEventListener("click", () => { page++; update(); });
    controls.append(previous, status, next);
    update();
    return controls;
  };

  const renderConnectivity = (snapshot) => {
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
    title.textContent = connectivitySummary(connectivity);
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
      /* Radio controls belong in the Wi-Fi tab. */
      const toggle = actionButton(
        snapshot.wireless_enabled ? "Turn Wi-Fi off" : "Turn Wi-Fi on",
        "networkWifiRadio",
        snapshot.wireless_enabled ? "off" : "on"
      );
      if (selectedTab === "Wi-Fi" && isAdministrator()) status.appendChild(toggle);
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

  const renderOverview = (snapshot) => {
    const section = document.createElement("section");
    section.className = "panel details network-overview";
    const connectivity = snapshot.connectivity || "unknown";
    const heading = sectionHeading("Connectivity", connectivitySummary(connectivity));
    const devices = (snapshot.devices || []).filter((device) => ["ethernet", "wifi"].includes(device.kind));
    const primary = devices.find((device) => device.state === "activated" && device.ipv4?.gateway && device.ipv4?.addresses?.length)
      || devices.find((device) => device.state === "activated" && device.ipv4?.addresses?.length);
    const ethernet = devices.find((device) => device.kind === "ethernet" && device.state === "activated")
      || devices.find((device) => device.kind === "ethernet");
    section.append(heading,
      row("IPv4", primary?.ipv4?.addresses?.[0]?.address || "Unavailable"),
      row("Ethernet", ethernet ? ethernet.interface + " · " + (ethernet.state === "activated" && ethernet.carrier !== false ? "Connected" : "Disconnected") : "Unavailable"));
    for (const [id, name] of [["tailscale", "Tailscale"], ["netbird", "NetBird"], ["playit", "Playit"]]) {
      const provider = remoteProviders.find((item) => item.id === id);
      section.appendChild(row(name, provider?.summary || "Unavailable"));
    }
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
    card.className = "panel network-device-card";

    const heading = document.createElement("div");
    heading.className = "network-device-heading";
    const titleWrap = document.createElement("div");
    const kind = document.createElement("span");
    kind.className = "eyebrow network-device-kind";
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
        row("Profile", device.active_connection?.id || "No active profile"),
        row("Gateway", device.ipv4?.gateway || "—"),
        row("DNS", device.ipv4?.dns?.join(", ") || "—"),
        row("MTU", device.mtu ? String(device.mtu) : "—")
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
      row(device.kind === "ethernet" ? "Saved autoconnect" : "Autoconnect", device.active_connection ? (device.active_connection.autoconnect ? "On" : "Off") : "—"),
      row(device.kind === "ethernet" ? "Saved IPv4 mode" : "IPv4 mode", device.active_connection?.ipv4_method || "—"),
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
    section.className = "panel network-wifi-section";

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
    const pages = paginateList(list, 4);
    if (pages) section.appendChild(pages);
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
    details.className = "panel network-saved";
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
    const pages = paginateList(list, 3);
    if (pages) details.appendChild(pages);
    return details;
  };

  const renderConnectPanel = () => {
    if (!connectionDraft) return null;
    const panel = document.createElement("section");
    panel.className = "panel network-connect-panel nav-admin-only";
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

  const renderEthernetForm = (device) => {
    const form = document.createElement("form");
    form.className = "settings-form network-ethernet-form";
    form.dataset.networkEthernetForm = device.interface;
    form.dataset.profileUuid = device.active_connection.uuid;
    const field = (name, title, value, type = "text") => {
      const wrap = document.createElement("label");
      wrap.textContent = title;
      const input = document.createElement("input");
      input.name = name;
      input.type = type;
      input.value = value ?? "";
      if (type === "checkbox") {
        input.checked = Boolean(value);
        wrap.className = "toggle-line";
        wrap.prepend(input);
      }
      if (type !== "checkbox") wrap.appendChild(input);
      form.appendChild(wrap);
      return input;
    };
    const methodLabel = document.createElement("label");
    methodLabel.textContent = "IPv4 mode";
    const method = document.createElement("select");
    method.name = "method";
    [["auto", "Automatic (DHCP)"], ["manual", "Manual"]].forEach(([value, text]) => {
      const option = document.createElement("option");
      option.value = value;
      option.textContent = text;
      method.appendChild(option);
    });
    method.value = device.active_connection.ipv4_method === "manual" ? "manual" : "auto";
    methodLabel.appendChild(method);
    form.appendChild(methodLabel);
    const address = field("address", "IPv4 address", device.ipv4?.addresses?.[0]?.address);
    const prefix = field("prefix", "Prefix length", device.ipv4?.addresses?.[0]?.prefix ?? 24, "number");
    prefix.min = "0"; prefix.max = "32";
    const gateway = field("gateway", "Gateway (optional)", device.ipv4?.gateway);
    field("dns", "DNS servers (comma separated)", (device.ipv4?.dns || []).join(", "));
    const mtu = field("mtu", "MTU (0 for automatic)", device.mtu || 0, "number");
    mtu.min = "0"; mtu.max = "9000";
    field("autoconnect", "Connect automatically", device.active_connection.autoconnect, "checkbox");
    const updateMode = () => {
      [address, prefix, gateway].forEach((input) => { input.disabled = method.value === "auto"; });
      address.required = method.value === "manual";
      prefix.required = method.value === "manual";
    };
    method.addEventListener("change", updateMode);
    updateMode();
    const hint = document.createElement("p");
    hint.className = "state-text";
    hint.textContent = "Settings roll back after 90 seconds unless you Keep settings. Reconnect at the new address if needed. Blank DNS and MTU 0 use automatic settings.";
    const apply = document.createElement("button");
    apply.type = "submit";
    apply.textContent = "Apply with safety checkpoint";
    apply.disabled = Boolean(currentCheckpoint);
    const actions = document.createElement("div");
    actions.className = "network-form-actions";
    actions.appendChild(apply);
    form.append(hint, actions);
    return form;
  };

  const renderTroubleshoot = (snapshot) => {
    const section = document.createElement("section");
    section.className = "panel network-section network-troubleshoot";
    const heading = sectionHeading("Network health", "Connectivity");
    heading.appendChild(actionButton("Check connectivity now", "networkConnectivityCheck", "true"));
    section.append(heading, row("NetworkManager connectivity", connectivitySummary(snapshot.connectivity)));
    const grid = document.createElement("div");
    grid.className = "network-troubleshoot-devices";
    if (!snapshot.networking_enabled) section.appendChild(row("Condition", "Networking is disabled"));
    const devices = (snapshot.devices || []).filter((device) => ["ethernet", "wifi"].includes(device.kind));
    if (!devices.length) section.appendChild(row("Condition", "No Ethernet or Wi-Fi interface is available"));
    devices.forEach((device) => {
      const card = document.createElement("article");
      card.className = "panel network-device-card";
      const summary = document.createElement("div");
      summary.className = "network-health-summary";
      summary.append(
        row("Interface", device.interface), row("State", label(device.state)),
        row("Link", device.carrier === false ? "No carrier" : device.carrier === true ? "Carrier present" : "Not reported"),
        row("IPv4", firstAddress(device.ipv4)),
        row("Default gateway", device.ipv4?.gateway || "Missing"),
        row("DNS servers", device.ipv4?.dns?.join(", ") || "Missing"),
        row("Active profile", device.active_connection?.id || "Missing")
      );
      card.appendChild(summary);
      if (device.active_connection && device.managed) {
        const repair = actionButton("Reconnect active profile", "networkReconnect", device.interface);
        repair.dataset.profileUuid = device.active_connection.uuid;
        repair.disabled = Boolean(currentCheckpoint);
        const actions = document.createElement("div");
        actions.className = "network-inline-actions";
        const explanation = document.createElement("p");
        explanation.className = "state-text";
        explanation.textContent = "Reconnect this interface using its current settings. The connection may briefly pause; previous settings are restored unless you confirm.";
        actions.appendChild(repair);
        card.append(explanation, actions);
      }
      grid.appendChild(card);
    });
    section.appendChild(grid);
    return section;
  };

  const renderRemoteAccess = () => {
    const section = document.createElement("section");
    section.className = "panel network-section";
    section.appendChild(sectionHeading("Remote access", "Providers"));
    const grid = document.createElement("div");
    grid.className = "network-provider-grid";
    const hint = document.createElement("p");
    hint.className = "state-text";
    hint.textContent = remoteError || "Activate enables and starts a provider; Deactivate stops and disables it. Manage accounts and tunnels in the provider dashboard.";
    section.appendChild(hint);
    remoteProviders.forEach((provider) => {
      const card = document.createElement("article");
      card.className = "panel network-device-card network-provider-card";
      const title = document.createElement("h3");
      title.textContent = provider.display_name;
      card.append(title,
        row("Software", provider.status_unavailable ? "Unknown" : provider.installed ? "Installed" : "Unavailable"),
        row("Service enabled", provider.status_unavailable ? "Unknown" : provider.service_enabled ? "Yes" : "No"),
        row("Service", providerTransitional(provider) ? provider.service_state[0].toUpperCase() + provider.service_state.slice(1) + "…" : provider.service_active ? "Running" : label(provider.service_state).replace(/^./, (c) => c.toUpperCase())),
        row("Configuration", provider.status_unavailable ? "Unknown" : provider.configured ? "Configured" : "Not configured")
      );
      if (provider.id === "netbird" && provider.connected && provider.ip) card.appendChild(row("NetBird IP", provider.ip));
      if (provider.id === "tailscale") {
        card.appendChild(row("Connection", provider.summary || "Unknown"));
        if (provider.connected && provider.ip) card.appendChild(row("Tailscale IP", provider.ip));
      }
      if (providerProgress.has(provider.id)) {
        const progress = document.createElement("p");
        progress.className = "state-text";
        progress.setAttribute("role", "status");
        progress.textContent = providerProgress.get(provider.id);
        card.appendChild(progress);
      }
      const needsSetup = provider.id === "playit" && provider.installed && !provider.configured;
      const actions = document.createElement("div");
      actions.className = "network-inline-actions";
      if (needsSetup) {
        if (setupRunning()) card.appendChild(row("Setup", playitSetup.state === "waiting" ? "Waiting for approval" : "Preparing…"));
        if (playitSetup.state === "failed") card.appendChild(row("Setup", "Failed or timed out"));
      }
      const controls = providerControls(provider);
      const activate = actionButton("Activate", "networkProvider", provider.id, "warning");
      activate.dataset.action = "activate";
      activate.disabled = controls.activateDisabled;
      const deactivate = actionButton("Deactivate", "networkProvider", provider.id);
      deactivate.dataset.action = "deactivate";
      deactivate.disabled = controls.deactivateDisabled;
      if (needsSetup && playitSetup.state === "waiting" && !playitClaimOpened && validPlayitClaim(playitSetup.claim_url)) {
        const claim = document.createElement("a");
        claim.href = playitSetup.claim_url;
        claim.target = "_blank";
        claim.rel = "noopener noreferrer";
        claim.className = "button-link primary network-action-button";
        claim.textContent = "Open Playit setup";
        actions.appendChild(claim);
      } else {
        actions.appendChild(activate);
      }
      actions.appendChild(deactivate);
      if (provider.id === "tailscale" && !provider.connected && validTailscaleLoginURL(tailscaleLoginURL)) {
        const login = document.createElement("a");
        login.href = tailscaleLoginURL;
        login.target = "_blank";
        login.rel = "noopener noreferrer";
        login.className = "button-link primary network-action-button";
        login.textContent = "Complete Tailscale login";
        actions.appendChild(login);
      }
      if (provider.id === "netbird" && !provider.connected && validNetbirdLoginURL(netbirdLoginURL)) {
        const login = document.createElement("a");
        login.href = netbirdLoginURL;
        login.target = "_blank";
        login.rel = "noopener noreferrer";
        login.className = "button-link primary network-action-button";
        login.textContent = "Complete NetBird login";
        actions.appendChild(login);
      }
      const dashboard = document.createElement("a");
      // Fixed destinations only, even if a malformed status payload is received.
      dashboard.href = { tailscale: "https://console.tailscale.com/admin/", netbird: "https://app.netbird.io/", playit: "https://playit.gg/account/" }[provider.id];
      dashboard.target = "_blank";
      dashboard.rel = "noopener noreferrer";
      dashboard.className = "button-link primary network-action-button";
      dashboard.textContent = "Open provider dashboard";
      actions.appendChild(dashboard);
      card.appendChild(actions);
      grid.appendChild(card);
    });
    section.appendChild(grid);
    return section;
  };

  const renderNetwork = (checkpoint = currentCheckpoint) => {
    if (!currentSnapshot) return;
    currentCheckpoint = checkpoint || null;
    const root = document.createElement("div");
    root.className = "network-workspace-view";
    const tabs = effectiveTabs(currentSnapshot);
    if (!tabs.includes(selectedTab)) selectedTab = "Overview";
    const tablist = document.createElement("nav");
    tablist.className = "system-workspace-tabs network-tabs";
    tablist.setAttribute("role", "tablist");
    tablist.setAttribute("aria-label", "Network views");
    tabs.forEach((name) => {
      const button = document.createElement("button");
      button.type = "button";
      button.dataset.networkTab = name;
      button.textContent = name;
      button.setAttribute("role", "tab");
      button.setAttribute("aria-selected", String(name === selectedTab));
      button.tabIndex = name === selectedTab ? 0 : -1;
      button.setAttribute("aria-controls", "network-tab-panel");
      button.id = "network-tab-" + name.replaceAll(" ", "-");
      tablist.appendChild(button);
    });
    root.append(tablist, state);
    if (currentCheckpoint && isAdministrator()) root.appendChild(renderCheckpoint(currentCheckpoint));
    const panel = document.createElement("div");
    panel.id = "network-tab-panel";
    panel.dataset.networkView = selectedTab;
    panel.setAttribute("role", "tabpanel");
    panel.setAttribute("aria-labelledby", "network-tab-" + selectedTab.replaceAll(" ", "-"));
    const devices = (currentSnapshot.devices || []).filter((device) => ["ethernet", "wifi"].includes(device.kind));
    if (selectedTab === "Overview") {
      panel.appendChild(renderOverview(currentSnapshot));
    } else if (selectedTab === "Ethernet") {
      devices.filter((device) => device.kind === "ethernet").forEach((device) => {
        const card = renderDevice(device);
        if (currentCheckpoint?.interfaces?.includes(device.interface)) card.appendChild(row("Review", "Check the live address and connectivity, then Keep settings to save or Revert now."));
        else if (device.active_connection && device.state === "activated") card.appendChild(renderEthernetForm(device));
        else card.appendChild(row("Configuration", "An active Ethernet profile is required. Use nm-hsp or nmtui locally to activate one."));
        panel.appendChild(card);
      });
      if (!devices.some((device) => device.kind === "ethernet")) panel.appendChild(row("Ethernet", "No Ethernet interface is available"));
    } else if (selectedTab === "Wi-Fi") {
      panel.appendChild(renderConnectivity(currentSnapshot));
      const layout = document.createElement("div");
      layout.className = "network-wifi-layout";
      const settings = document.createElement("div");
      settings.className = "network-wifi-settings";
      const nearby = document.createElement("div");
      nearby.className = "network-wifi-nearby";
      devices.filter((device) => device.kind === "wifi").forEach((device) => {
        settings.appendChild(renderDevice(device));
        nearby.appendChild(renderWiFiNetworks(device, currentNetworks.get(device.interface)));
      });
      const saved = renderSavedWiFi(currentSnapshot);
      if (saved) nearby.appendChild(saved);
      const connectPanel = renderConnectPanel();
      if (connectPanel) settings.appendChild(connectPanel);
      layout.append(settings, nearby);
      panel.appendChild(layout);
    } else if (selectedTab === "Troubleshoot") {
      panel.appendChild(renderTroubleshoot(currentSnapshot));
    } else if (selectedTab === "Remote Access") {
      panel.appendChild(renderRemoteAccess());
    }
    root.appendChild(panel);
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
      const snapshot = await requestJSON("/api/network");
      if (!snapshot || sequence !== loadSequence) return;

      currentSnapshot = snapshot;
      currentCheckpoint = isAdministrator() ? (await resumeCheckpoint() || snapshot.pending_checkpoints?.[0] || null) : null;
      if (currentCheckpoint) storeCheckpoint(currentCheckpoint);
      if (sequence !== loadSequence) return;
      currentNetworks = new Map();
      const wifiDevices = (snapshot.devices || []).filter((device) => device.kind === "wifi");
      for (const device of isAdministrator() ? wifiDevices : []) {
        const networks = await requestJSON("/api/network/wifi/" + escapePath(device.interface) + "/networks");
        if (sequence !== loadSequence) return;
        currentNetworks.set(device.interface, networks);
      }

      remoteProviders = [
        { id: "tailscale", display_name: "Tailscale", status_unavailable: true },
        { id: "netbird", display_name: "NetBird", status_unavailable: true },
        { id: "playit", display_name: "Playit.gg", status_unavailable: true },
      ];
      remoteError = "";
      try {
        const remote = await requestJSON("/api/network/remote-access");
        if (sequence !== loadSequence) return;
        remoteProviders = (remote?.providers || []).filter((provider) => ["tailscale", "netbird", "playit"].includes(provider.id));
        if (isAdministrator() && selectedTab === "Remote Access") {
          playitSetup = await requestJSON("/api/network/remote-access/playit/setup") || { state: "idle" };
        }
      } catch (error) { remoteError = error?.message || "Remote access status unavailable."; }
      if (sequence !== loadSequence) return;
      updatePlayitPopup();
      openActivatedDashboards();
      renderNetwork();
      state.textContent = "";
      if (remoteProviders.some(providerTransitional) || setupRunning()) pollRemoteAccess();
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
      // A persistence failure can occur after successful checkpoint acceptance.
      // Recover terminal state without hiding the failure message.
      try {
        const pending = await resumeCheckpoint();
        if (!pending) { currentCheckpoint = null; renderNetwork(); }
      } catch (_) { /* Connectivity safety remains owned by NetworkManager. */ }
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

  const runConfigurationAction = async (button, action) => {
    button.disabled = true;
    try { await action(); await loadNetwork(); }
    catch (error) {
      state.textContent = error?.message || "Network change failed.";
      if (readStoredCheckpointID()) scheduleRecovery();
    } finally { button.disabled = false; }
  };

  const refreshRemoteAccess = async () => {
    if (selectedTab === "Remote Access") playitSetup = await requestJSON("/api/network/remote-access/playit/setup") || { state: "idle" };
    const remote = await requestJSON("/api/network/remote-access");
    remoteProviders = (remote?.providers || []).filter((provider) => ["tailscale", "netbird", "playit"].includes(provider.id));
    updatePlayitPopup();
    openActivatedDashboards();
    if (remoteProviders.some((provider) => provider.id === "netbird" && provider.connected)) {
      netbirdLoginURL = "";
      providerProgress.delete("netbird");
    }
    if (remoteProviders.some((provider) => provider.id === "tailscale" && provider.connected)) {
      tailscaleLoginURL = "";
      providerProgress.delete("tailscale");
    }
    renderNetwork();
  };

  // Each refresh/action starts a finite observation window. Closing the workspace
  // ends observation; the agent still owns the setup timeout.
  const pollRemoteAccess = async () => {
    const sequence = ++remotePollSequence;
    const deadline = Date.now() + (setupRunning() ? 610000 : (providerProgress.has("netbird") || providerProgress.has("tailscale")) ? 120000 : 20000);
    try {
      while (sequence === remotePollSequence && dialog.open && isAdministrator() && Date.now() < deadline &&
             (remoteProviders.some(providerTransitional) || setupRunning() || providerProgress.has("netbird") || providerProgress.has("tailscale"))) {
        await new Promise((resolve) => setTimeout(resolve, setupRunning() || providerProgress.has("netbird") || providerProgress.has("tailscale") ? 2000 : 750));
        if (sequence !== remotePollSequence || !dialog.open || !isAdministrator()) return;
        await refreshRemoteAccess();
      }
      if (sequence === remotePollSequence) {
        if (providerProgress.has("netbird")) providerProgress.set("netbird", "Finish NetBird login in your browser, then select Refresh to see the assigned IP.");
        if (providerProgress.has("tailscale")) providerProgress.set("tailscale", "Finish Tailscale login in your browser, then select Refresh to see the assigned IP.");
        pendingDashboards.forEach((id) => providerProgress.set(id, "Service activation is still pending. Refresh to check its status, or use the provider dashboard link."));
        pendingDashboards.clear();
        renderNetwork();
      }
    } catch (error) {
      failPlayitPopup();
      remoteError = error?.message || "Remote access status unavailable. Refresh to retry.";
      state.textContent = remoteError;
      renderNetwork();
    }
  };

  const runRemoteAction = async (action) => {
    if (remoteBusy) return;
    remoteBusy = true;
    remoteError = "";
    ++remotePollSequence;
    renderNetwork();
    try {
      await action();
      await refreshRemoteAccess();
      pollRemoteAccess();
    } catch (error) {
      failPlayitPopup();
      remoteError = error?.message || "Remote access change failed.";
      state.textContent = remoteError;
      try { await refreshRemoteAccess(); pollRemoteAccess(); } catch (_) { /* Keep the reported failure visible. */ }
    } finally {
      remoteBusy = false;
      renderNetwork();
    }
  };

  const submitEthernet = (form) => {
    const values = Object.fromEntries(new FormData(form));
    if (values.method === "auto") { values.address = ""; values.prefix = 0; values.gateway = ""; }
    values.autoconnect = form.elements.autoconnect.checked;
    values.profile_uuid = form.dataset.profileUuid;
    values.mtu = values.mtu || 0;
    const interfaceName = form.dataset.networkEthernetForm;
    return runConfigurationAction(form.querySelector('button[type="submit"]'), () =>
      runCheckpointedMutation([interfaceName], (checkpointID) =>
        postForm("/api/network/ethernet/" + escapePath(interfaceName), { ...values, checkpoint_id: checkpointID })
      )
    );
  };

  content.addEventListener("click", (event) => {
    const tab = event.target.closest("[data-network-tab]");
    if (tab) {
      selectedTab = tab.dataset.networkTab;
      connectionDraft = null;
      renderNetwork();
      if (selectedTab === "Remote Access") refreshRemoteAccess().then(pollRemoteAccess).catch((error) => { state.textContent = error?.message || "Remote access status unavailable."; });
      content.querySelector('[aria-selected="true"]')?.focus();
      return;
    }
    if (!isAdministrator()) return;
    const provider = event.target.closest("[data-network-provider]");
    if (provider) {
      if (provider.disabled || remoteBusy) return;
      const id = provider.dataset.networkProvider;
      const needsSetup = id === "playit" && provider.dataset.action === "activate" &&
        !remoteProviders.find((item) => item.id === id)?.configured;
      if (needsSetup) {
        closePlayitPopup();
        playitClaimOpened = false;
        // Open during the click; sever the opener before navigating the validated claim.
        try {
          playitPopup = window.open("about:blank", "_blank");
          if (playitPopup) {
            if (playitPopup.document.documentElement) {
              playitPopup.document.documentElement.style.backgroundColor = "#0b1117";
              playitPopup.document.documentElement.style.colorScheme = "dark";
            }
            try { renderPlayitPopup(); }
            finally { playitPopup.opener = null; }
          }
        } catch (_) { closePlayitPopup(); }
        runRemoteAction(async () => {
          // The existing setup endpoint enables/starts the fixed service before setup.
          playitSetup = await postForm("/api/network/remote-access/playit/setup");
          updatePlayitPopup();
        });
      } else if (id === "tailscale" && provider.dataset.action === "activate") {
        // Reserve the tab during the user click to avoid popup blockers.
        let loginTab = null;
        try { loginTab = window.open("about:blank", "_blank"); if (loginTab) loginTab.opener = null; }
        catch (_) { /* Keep the login link available in the card. */ }
        providerProgress.set(id, "Starting Tailscale login…");
        runRemoteAction(async () => {
          try {
            const result = await postForm("/api/network/remote-access/tailscale", { action: "activate" });
            if (result?.login_url && !validTailscaleLoginURL(result.login_url)) throw new Error("Tailscale returned an invalid login link.");
            tailscaleLoginURL = result?.login_url || "";
            if (tailscaleLoginURL) {
              providerProgress.set(id, "Complete Tailscale login in your browser.");
              if (loginTab && !loginTab.closed) {
                try { loginTab.location.replace(tailscaleLoginURL); }
                catch (_) { /* The login link remains available in the card. */ }
              }
            } else {
              try { if (loginTab && !loginTab.closed) loginTab.close(); } catch (_) {}
              providerProgress.set(id, "Checking Tailscale connection…");
            }
          } catch (error) {
            try { if (loginTab && !loginTab.closed) loginTab.close(); } catch (_) {}
            providerProgress.delete(id);
            throw error;
          }
        });
      } else if (id === "netbird" && provider.dataset.action === "activate") {
        // Reserve a browser tab during the click, not after the API request.
        let loginTab = null;
        try { loginTab = window.open("about:blank", "_blank"); if (loginTab) loginTab.opener = null; }
        catch (_) { /* The login button remains available in the card. */ }
        providerProgress.set(id, "Starting NetBird login…");
        runRemoteAction(async () => {
          try {
            const result = await postForm("/api/network/remote-access/netbird", { action: "activate" });
            if (result?.login_url && !validNetbirdLoginURL(result.login_url)) throw new Error("NetBird returned an invalid login link.");
            netbirdLoginURL = result?.login_url || "";
            if (netbirdLoginURL) {
              providerProgress.set(id, "Complete NetBird login in your browser.");
              if (loginTab && !loginTab.closed) {
                try { loginTab.location.replace(netbirdLoginURL); }
                catch (_) { /* The login button remains available in the card. */ }
              }
            } else {
              // NetBird may already have a saved login and connect without a URL.
              try { if (loginTab && !loginTab.closed) loginTab.close(); } catch (_) {}
              providerProgress.set(id, "Checking NetBird connection…");
            }
          } catch (error) {
            try { if (loginTab && !loginTab.closed) loginTab.close(); } catch (_) {}
            providerProgress.delete(id);
            throw error;
          }
        });
      } else {
        if (id === "playit" && provider.dataset.action === "deactivate") closePlayitPopup();
        const action = provider.dataset.action;
        if (action === "activate" && providerDashboards[id]) {
          providerProgress.set(id, "Activating service…");
        } else {
          pendingDashboards.delete(id);
          providerProgress.delete(id);
        }
        runRemoteAction(async () => {
          try {
            await postForm("/api/network/remote-access/" + escapePath(id), { action });
            if (action === "activate" && providerDashboards[id]) pendingDashboards.add(id);
          } catch (error) {
            providerProgress.delete(id);
            throw error;
          }
        });
      }
      return;
    }
    const check = event.target.closest("[data-network-connectivity-check]");
    if (check) { runConfigurationAction(check, () => postForm("/api/network/connectivity-check")); return; }
    const repair = event.target.closest("[data-network-reconnect]");
    if (repair) {
      runConfigurationAction(repair, () => runCheckpointedMutation([repair.dataset.networkReconnect], (checkpointID) =>
        postForm("/api/network/reconnect/" + escapePath(repair.dataset.networkReconnect), { profile_uuid: repair.dataset.profileUuid, checkpoint_id: checkpointID })
      ));
      return;
    }
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
      content.querySelector("[data-network-connect-panel] input")?.focus({ preventScroll: true });
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
      content.querySelector("[data-network-connect-panel] input")?.focus({ preventScroll: true });
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

  content.addEventListener("keydown", (event) => {
    const tab = event.target.closest("[data-network-tab]");
    if (!tab || !["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const tabs = effectiveTabs(currentSnapshot);
    let index = tabs.indexOf(selectedTab);
    if (event.key === "Home") index = 0;
    else if (event.key === "End") index = tabs.length - 1;
    else index = (index + (event.key === "ArrowRight" ? 1 : -1) + tabs.length) % tabs.length;
    selectedTab = tabs[index];
    connectionDraft = null;
    renderNetwork();
    if (selectedTab === "Remote Access") refreshRemoteAccess().then(pollRemoteAccess).catch((error) => { state.textContent = error?.message || "Remote access status unavailable."; });
    content.querySelector('[aria-selected="true"]')?.focus();
  });

  content.addEventListener("submit", (event) => {
    if (!isAdministrator()) { event.preventDefault(); return; }
    const ethernet = event.target.closest("[data-network-ethernet-form]");
    if (ethernet) { event.preventDefault(); submitEthernet(ethernet); return; }
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
