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
  let activePreset = "";

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
    activePreset = name;
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

  const applyPreset = (name) => {
    if (name === "custom") {
      markPreset("custom");
      if (gameMemory) gameMemory.focus();
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

    let overheadGiB = heapGiB >= 4 ? 2 : 1;
    let maximumGiB = heapGiB + overheadGiB;

    if (totalMiB > 0) {
      const reserveMiB = name === "high" ? minimumReserveMiB : recommendedReserveMiB;
      const allowedMaximumGiB = Math.floor(Math.max(0, totalMiB - reserveMiB) / 1024);
      if (allowedMaximumGiB >= 2 && maximumGiB > allowedMaximumGiB) {
        maximumGiB = allowedMaximumGiB;
        heapGiB = Math.max(1, Math.min(heapGiB, maximumGiB - 1));
        overheadGiB = maximumGiB - heapGiB;
      }
    }

    gameMemory.value = memoryValue(heapGiB * 1024);
    maxMemory.value = memoryValue(maximumGiB * 1024);
    markPreset(name);
    updateStatus();
  };

  buttons.forEach((button) => {
    button.addEventListener("click", () => applyPreset(button.dataset.memoryPreset));
  });

  [gameMemory, maxMemory].forEach((input) => {
    if (!input) return;
    input.addEventListener("input", () => {
      if (activePreset && activePreset !== "custom") markPreset("custom");
      updateStatus();
    });
  });

  if (maxPlayers) {
    maxPlayers.addEventListener("change", () => {
      if (activePreset && activePreset !== "custom") applyPreset(activePreset);
    });
  }

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
  const channelLabel = (channel) => ({ STABLE: "Stable", BETA: "Beta", ALPHA: "Alpha" })[channel] || "Pre-release";
  const show = (status, policy) => {
    const rows = [];
    if (status.selected_candidate) rows.push(`${policy === "latest" ? "Will install now" : "Will install"}: ${status.selected_candidate}`);
    if (status.available) rows.push(`Newest available: ${status.available}${status.available_channel && status.available_channel !== "STABLE" ? ` · ${channelLabel(status.available_channel)}` : ""}`);
    if (status.crossplay_enabled && status.geyser_supported_version && (status.geyser_supported_version !== status.selected_candidate || status.available !== status.selected_candidate)) rows.push(`Bedrock-supported version: ${status.geyser_supported_version}`);
    if (policy === "recommended" && status.crossplay_enabled && status.selected_candidate && status.available !== status.selected_candidate) rows.push("This version keeps Bedrock players compatible.");
    if (policy === "latest") rows.push("Latest follows newer Minecraft server versions when available.");
    if (status.candidate_channel && status.candidate_channel !== "STABLE") rows.push(`Paper release: ${channelLabel(status.candidate_channel)}. This is a pre-release server build.`);
    if (status.crossplay_enabled && !status.crossplay_compatible && status.selected_candidate) rows.push("This version is not currently compatible with Bedrock cross-play. Choose Recommended, choose a compatible Specific version, or turn off Bedrock to continue.");
    if (!status.selected_candidate) rows.push(status.reason || "No usable server build was found for this version.");
    preview.replaceChildren(...rows.map((value) => { const p = document.createElement("p"); p.textContent = value; return p; }));
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
    if (bedrockPortField) bedrockPortField.hidden = !bedrockToggle?.checked;
  };
  bedrockToggle?.addEventListener("change", syncBedrockPort);
  syncBedrockPort();
}
