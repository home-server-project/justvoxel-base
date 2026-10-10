const memoryPanel = document.querySelector("[data-memory-settings]");
if (memoryPanel) {
  const gameMemory = document.getElementById("java-memory");
  const maxMemory = document.getElementById("container-memory");
  const maxPlayers = document.getElementById("max-players");
  const status = document.getElementById("memory-status");
  const buttons = Array.from(memoryPanel.querySelectorAll("[data-memory-preset]"));
  const totalMiB = Number(memoryPanel.dataset.systemMemoryMib || 0);
  const minimumReserveMiB = Number(memoryPanel.dataset.minReserveMib || 1024);
  const recommendedReserveMiB = Number(memoryPanel.dataset.recommendedReserveMib || 2048);

  const parseMemoryMiB = (value) => {
    const match = String(value || "").trim().match(/^([1-9][0-9]*)([mMgG])$/);
    if (!match) return 0;
    const amount = Number(match[1]);
    return match[2].toUpperCase() === "G" ? amount * 1024 : amount;
  };

  const memoryValue = (mib) => {
    if (mib % 1024 === 0) return `${mib / 1024}G`;
    return `${mib}M`;
  };

  const formatRemaining = (mib) => {
    if (mib >= 1024) return `${(mib / 1024).toFixed(mib % 1024 === 0 ? 0 : 1)} GB`;
    return `${Math.max(0, Math.round(mib))} MB`;
  };

  const markPreset = (name) => {
    buttons.forEach((button) => button.classList.toggle("is-selected", button.dataset.memoryPreset === name));
  };

  const updateStatus = () => {
    if (!status || !maxMemory) return;
    const maximumMiB = parseMemoryMiB(maxMemory.value);
    status.classList.remove("warning", "danger");
    if (!totalMiB) {
      status.textContent = "System memory could not be detected. JustVoxel will validate the values again before Apply.";
      status.classList.add("warning");
      return;
    }
    if (!maximumMiB) {
      status.textContent = "Enter memory as a size such as 6G or 6144M.";
      status.classList.add("warning");
      return;
    }
    const remaining = totalMiB - maximumMiB;
    if (remaining < minimumReserveMiB) {
      status.textContent = `Not enough memory remains outside Minecraft: about ${formatRemaining(remaining)}. JustVoxel requires at least 1 GiB outside the Minecraft limit; reduce Maximum Minecraft memory before continuing.`;
      status.classList.add("danger");
    } else if (remaining < recommendedReserveMiB) {
      status.textContent = `Tight memory configuration: about ${formatRemaining(remaining)} remains outside Minecraft. JustVoxel itself typically uses roughly 0.6–1.0 GiB; around 2 GiB gives extra room for cache, backups, updates and temporary spikes.`;
      status.classList.add("warning");
    } else {
      status.textContent = `Memory remaining outside Minecraft: about ${formatRemaining(remaining)}. JustVoxel itself typically uses roughly 0.6–1.0 GiB; the rest stays available for Linux cache, backups, updates, management services and temporary workload spikes.`;
    }
  };

  const presetValues = (name) => {
    // Match suggest_memory_values: round the sizing class, not the reserve.
    const totalGiB = totalMiB > 0 ? Math.round(totalMiB / 1024) : 8;

    let baseRecommended, baseMaximum;
    if (totalGiB >= 16) { baseRecommended = 8; baseMaximum = 12; }
    else if (totalGiB >= 12) { baseRecommended = 8; baseMaximum = 10; }
    else if (totalGiB >= 8) { baseRecommended = 4; baseMaximum = 6; }
    else if (totalGiB >= 6) { baseRecommended = 3; baseMaximum = 4; }
    else if (totalGiB >= 4) { baseRecommended = 2; baseMaximum = 3; }
    else { baseRecommended = 1; baseMaximum = 2; }

    let heapGiB = baseRecommended;
    if (name === "light") heapGiB = Math.max(2, baseRecommended - 2);
    if (name === "high") heapGiB = Math.min(14, baseRecommended + 2);
    let heapMiB = heapGiB * 1024;
    let maximumMiB = (name === "recommended" ? baseMaximum : heapGiB + (heapGiB >= 4 ? 2 : 1)) * 1024;

    if (totalMiB > 0) {
      const allowedMaximumMiB = Math.floor(totalMiB - Math.max(1024, minimumReserveMiB));
      if (allowedMaximumMiB < 2) return null;
      // High retains whole-GiB sizing; Recommended and Light can use MiB
      // precision to preserve their heap despite firmware/kernel reservations.
      const limitMiB = name === "high" && allowedMaximumMiB >= 2048
        ? Math.floor(allowedMaximumMiB / 1024) * 1024 : allowedMaximumMiB;
      maximumMiB = Math.min(maximumMiB, limitMiB);
      if (heapMiB >= maximumMiB) {
        heapMiB = maximumMiB >= 2048 ? maximumMiB - 1024 : Math.floor(maximumMiB / 2);
      }
    }

    return { heapMiB, maximumMiB };
  };

  const detectPreset = () => {
    const matching = ["recommended", "light", "high"].find((name) => {
      const values = presetValues(name);
      return values && parseMemoryMiB(gameMemory?.value) === values.heapMiB &&
        parseMemoryMiB(maxMemory?.value) === values.maximumMiB;
    });
    markPreset(matching || "custom");
  };

  const applyPreset = (name) => {
    if (name === "custom") {
      markPreset("custom");
      if (gameMemory) gameMemory.focus();
      updateStatus();
      return;
    }
    if (!gameMemory || !maxMemory) return;
    const values = presetValues(name);
    if (!values) return;
    gameMemory.value = memoryValue(values.heapMiB);
    maxMemory.value = memoryValue(values.maximumMiB);
    markPreset(name);
    updateStatus();
  };

  buttons.forEach((button) => {
    button.addEventListener("click", () => applyPreset(button.dataset.memoryPreset));
  });

  [gameMemory, maxMemory].forEach((input) => {
    if (!input) return;
    const memoryChanged = () => {
      detectPreset();
      updateStatus();
    };
    input.addEventListener("input", memoryChanged);
    input.addEventListener("change", memoryChanged);
  });

  if (maxPlayers) {
    maxPlayers.addEventListener("change", () => {
      detectPreset();
      updateStatus();
    });
  }

  detectPreset();
  updateStatus();
}

const versionPolicy = document.getElementById("version-policy");
const versionInput = document.getElementById("minecraft-version");
const specificVersionField = document.getElementById("specific-version-field");
if (versionPolicy && versionInput && specificVersionField) {
  const form = versionPolicy.closest("form");
  const preview = document.getElementById("setup-version-preview");
  const next = form?.querySelector('button[name="direction"][value="next"]');
  let sequence = 0;
  let timer;
  const channelLabel = (channel) => ({ STABLE: "Stable", BETA: "Beta", ALPHA: "Alpha", RELEASE: "Release" })[channel] || channel || "Unavailable";
  const show = (status, policy) => {
    const make = (tag, className, value) => {
      const node = document.createElement(tag);
      node.className = className;
      node.textContent = value;
      return node;
    };
    const summary = make("div", "notice " + (status.selected_candidate ? "success" : "warning") + " setup-version-panel", "");
    summary.append(make("span", "setup-version-kicker", policy === "recommended" ? "Recommended" : policy === "latest" ? "Latest" : "Specific"));
    if (status.selected_candidate) {
      summary.append(make("strong", "setup-version-candidate", status.selected_candidate));
      summary.append(make("span", "setup-version-note", `Minecraft build classification: ${channelLabel(status.candidate_channel)}`));
    } else {
      summary.append(make("p", "setup-version-explanation", status.reason || "No usable server build was found for this version."));
    }
    const compatibility = make("div", "notice " + (status.crossplay_compatible ? "success" : "warning") + " setup-version-compatibility", "");
    compatibility.append(make("p", "", status.crossplay_compatible ? "Bedrock cross-play is supported." : "Bedrock cross-play is not supported."));
    if (!status.crossplay_compatible) compatibility.append(make("p", "", "Choose Recommended, select a compatible Specific version, or disable Bedrock cross-play."));
    compatibility.append(make("p", "", `Current supported Minecraft version: ${status.geyser_supported_version || "Unavailable"}.`));
    preview.replaceChildren(summary, compatibility);
    next.disabled = !status.selected_candidate || (status.crossplay_enabled && !status.crossplay_compatible);
  };
  const refresh = async () => {
    const ticket = ++sequence;
    next.disabled = true;
    preview.textContent = "Checking available versions…";
    const policy = versionPolicy.value;
    const version = policy === "pinned" ? versionInput.value.trim() : "";
    if (policy === "pinned" && !version) { preview.textContent = "Enter a Minecraft version to check availability."; return; }
    try {
      const response = await fetch("/setup/version-preview?" + new URLSearchParams({ policy, version }), { credentials: "same-origin", cache: "no-store" });
      if (!response.ok) throw new Error();
      const status = await response.json();
      if (ticket === sequence) show(status, policy);
    } catch (_) { if (ticket === sequence) preview.textContent = "Version information is unavailable. Try again before continuing."; }
  };
  const updateVersionHelp = () => {
    const specific = versionPolicy.value === "pinned";
    specificVersionField.hidden = !specific;
    versionInput.required = specific;
    versionInput.readOnly = !specific;
    if (!specific) versionInput.value = "";
    refresh();
  };
  versionPolicy.addEventListener("change", updateVersionHelp);
  versionInput.addEventListener("input", () => { clearTimeout(timer); timer = setTimeout(refresh, 300); });
  form?.addEventListener("submit", (event) => { if (event.submitter?.value === "next" && next.disabled) event.preventDefault(); });
  updateVersionHelp();
}

if (window.location.hash === "#review") {
  const review = document.getElementById("review");
  if (review) review.scrollIntoView({ block: "start" });
}


const imageChannel = document.getElementById("image-channel");
const imageTag = document.getElementById("image-tag");
const customImageTagField = document.getElementById("custom-image-tag-field");
const customImageTag = document.getElementById("custom-image-tag");
if (imageChannel && imageTag && customImageTagField && customImageTag) {
  const syncImageChannel = () => {
    const custom = imageChannel.value === "custom";
    customImageTagField.hidden = !custom;
    if (custom) {
      imageTag.value = customImageTag.value.trim();
      customImageTag.required = true;
    } else {
      imageTag.value = imageChannel.value;
      customImageTag.required = false;
    }
  };
  imageChannel.addEventListener("change", () => {
    syncImageChannel();
    if (imageChannel.value === "custom") customImageTag.focus();
  });
  customImageTag.addEventListener("input", syncImageChannel);
  syncImageChannel();
}


window.initializeJustVoxelTimezoneSearch?.();

const setupConnections = document.querySelector("[data-setup-connections]");
if (setupConnections) {
  const bedrockToggle = setupConnections.querySelector("[data-bedrock-toggle]");
  const bedrockPortField = setupConnections.querySelector("[data-bedrock-port-field]");
  const syncBedrockPort = () => {
    if (bedrockPortField) {
      const enabled = !!bedrockToggle?.checked;
      bedrockPortField.classList.toggle("is-disabled", !enabled);
      // Preserve submission of the existing port while cross-play is off.
      const port = bedrockPortField.querySelector("input");
      if (port) { port.readOnly = !enabled; port.setAttribute("aria-disabled", String(!enabled)); }
    }
  };
  bedrockToggle?.addEventListener("change", syncBedrockPort);
  syncBedrockPort();
}
