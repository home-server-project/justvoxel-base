(() => {
  const panel = document.querySelector("[data-wallpaper-panel]");
  const open = document.querySelector("[data-wallpaper-open]");
  if (!panel || !open) return;

  const wallpaper = document.querySelector("[data-dashboard-wallpaper]");
  if (!wallpaper) return;
  const form = panel.querySelector("[data-wallpaper-form]");
  const error = form.querySelector("[data-wallpaper-error]");
  const defaultImage = "/static/wallpapers/jv-wp-v2-day.webp";
  const defaultPreference = () => ({
    mode: "builtin", theme: "v2", appearance: "auto", dayStart: "07:00", nightStart: "19:00",
  });
  const preferenceKey = "justvoxel-wallpaper-preference";
  const allowedTypes = new Set(["image/jpeg", "image/png", "image/webp", "image/avif"]);
  const maxSize = 10 * 1024 * 1024;
  let preference = defaultPreference();
  let automaticTimer = null;
  let activeObjectURL = null;
  let pendingTheme = "v2";
  let busy = false;
  let committing = false;
  let operation = 0;
  let restoreCanceled = false;

  const validateURL = (value) => {
    if (typeof value !== "string" || !/^https?:\/\//i.test(value)) {
      throw new Error("Use a web address starting with http:// or https://.");
    }
    const url = new URL(value);
    if (url.protocol !== "http:" && url.protocol !== "https:") {
      throw new Error("Use a web address starting with http:// or https://.");
    }
    return url.href;
  };
  const validateBlob = (blob) => {
    if (!(blob instanceof Blob) || !allowedTypes.has(blob.type)) {
      throw new Error("Choose a JPEG, PNG, WebP or AVIF picture.");
    }
    if (blob.size === 0 || blob.size > maxSize) {
      throw new Error("Choose a picture up to 10 MiB.");
    }
    return blob;
  };
  const loadImage = (src) => new Promise((resolve, reject) => {
    const image = new Image();
    image.onload = () => resolve();
    image.onerror = () => reject(new Error("The image could not be loaded. Try another picture or web address."));
    image.src = src;
  });

  // Uploaded picture bytes stay in this browser, separate from the small preference.
  const storedImage = (action, blob) => new Promise((resolve, reject) => {
    let blocked = false;
    const request = indexedDB.open("justvoxel-wallpaper", 1);
    request.onupgradeneeded = () => request.result.createObjectStore("images");
    request.onerror = () => reject(new Error("This browser could not save the picture."));
    request.onblocked = () => {
      blocked = true;
      reject(new Error("Close other JustVoxel tabs and try again."));
    };
    request.onsuccess = () => {
      const db = request.result;
      if (blocked) { db.close(); return; }
      let transaction;
      try {
        transaction = db.transaction("images", action === "get" ? "readonly" : "readwrite");
        const images = transaction.objectStore("images");
        const result = action === "get" ? images.get("uploaded") :
          action === "put" ? images.put(blob, "uploaded") : images.delete("uploaded");
        transaction.oncomplete = () => { db.close(); resolve(result.result); };
        transaction.onabort = transaction.onerror = () => {
          db.close();
          reject(new Error("This browser could not save the picture."));
        };
      } catch (failure) {
        db.close();
        reject(failure);
      }
    };
  });

  const applyImage = (src, objectURL = null) => {
    wallpaper.classList.toggle("wallpaper-theme-v1", preference.mode === "builtin" && preference.theme === "v1");
    wallpaper.classList.toggle("wallpaper-theme-v2", preference.mode === "builtin" && preference.theme === "v2");
    if (wallpaper.getAttribute("src") !== src) wallpaper.src = src;
    if (activeObjectURL) URL.revokeObjectURL(activeObjectURL);
    activeObjectURL = objectURL;
  };
  const timeMinutes = (value) => {
    if (typeof value !== "string" || !/^([01]\d|2[0-3]):[0-5]\d$/.test(value)) {
      throw new Error("Choose valid Day and Night start times.");
    }
    const [hours, minutes] = value.split(":").map(Number);
    return hours * 60 + minutes;
  };
  const builtinPreference = (saved) => {
    // Legacy default preferences migrate without requiring browser storage removal.
    if (saved.mode === "default") return defaultPreference();
    const next = { ...defaultPreference(), ...saved, mode: "builtin" };
    if (!["v1", "v2"].includes(next.theme) || !["auto", "day", "night"].includes(next.appearance)) {
      throw new Error("Choose a built-in theme and appearance.");
    }
    if (timeMinutes(next.dayStart) === timeMinutes(next.nightStart)) {
      throw new Error("Day and Night must start at different times.");
    }
    return next;
  };
  const automaticAppearance = (next, now = new Date()) => {
    const day = timeMinutes(next.dayStart);
    const night = timeMinutes(next.nightStart);
    const minutes = now.getHours() * 60 + now.getMinutes();
    // The second case includes the Day interval crossing midnight.
    const isDay = day < night ? minutes >= day && minutes < night :
      minutes >= day || minutes < night;
    return isDay ? "day" : "night";
  };
  const builtinImage = (next, now = new Date()) => {
    const appearance = next.appearance === "auto" ? automaticAppearance(next, now) : next.appearance;
    return `/static/wallpapers/jv-wp-${next.theme}-${appearance}.webp`;
  };
  const clearAutomaticTimer = () => {
    if (automaticTimer !== null) window.clearTimeout(automaticTimer);
    automaticTimer = null;
  };
  const refreshBuiltin = () => {
    clearAutomaticTimer();
    if (preference.mode !== "builtin") return;
    const now = new Date();
    applyImage(builtinImage(preference, now));
    if (preference.appearance !== "auto") return;
    const boundaries = [preference.dayStart, preference.nightStart].map((time) => {
      const minutes = timeMinutes(time);
      const boundary = new Date(now);
      boundary.setHours(Math.floor(minutes / 60), minutes % 60, 0, 0);
      if (boundary <= now) boundary.setDate(boundary.getDate() + 1);
      return boundary.getTime();
    });
    const nextBoundary = Math.min(...boundaries);
    automaticTimer = window.setTimeout(refreshBuiltin, Math.max(1, nextBoundary - now.getTime()));
  };
  const savePreference = (next) => {
    try {
      localStorage.setItem(preferenceKey, JSON.stringify(next));
    } catch {
      throw new Error("This browser could not save the wallpaper preference.");
    }
    preference = next;
    clearAutomaticTimer();
  };
  const showError = (failure) => {
    error.textContent = failure.message || "The wallpaper could not be saved.";
    error.hidden = false;
  };
  const setBusy = (value) => {
    busy = value;
    form.querySelectorAll("input, button").forEach((control) => { control.disabled = value && (committing || !control.hasAttribute("data-wallpaper-cancel")); });
  };
  const updateFields = () => {
    const mode = form.elements.mode.value;
    const builtin = mode === "builtin";
    const automatic = builtin && form.elements.appearance.value === "auto";
    form.querySelector("[data-wallpaper-builtin-fields]").hidden = !builtin;
    form.querySelector("[data-wallpaper-schedule]").hidden = !automatic;
    form.querySelectorAll('input[name="theme"]').forEach((input) => {
      input.checked = builtin && input.value === pendingTheme;
    });
    form.elements.dayStart.disabled = !automatic;
    form.elements.nightStart.disabled = !automatic;
    form.querySelector("[data-wallpaper-url-fields]").hidden = mode !== "url";
    form.querySelector("[data-wallpaper-upload-fields]").hidden = mode !== "upload";
    form.elements.url.disabled = mode !== "url";
    form.elements.url.required = mode === "url";
    form.elements.picture.disabled = mode !== "upload";
    form.querySelector('button[type="submit"]').disabled = busy;
  };
  const reset = async () => {
    preference = defaultPreference();
    // Always attempt both removals, even when one browser storage is unavailable.
    let failure;
    try { savePreference(preference); } catch (problem) { failure = problem; }
    refreshBuiltin();
    try { await storedImage("delete"); } catch (problem) { failure = failure || problem; }
    if (failure) throw failure;
  };
  refreshBuiltin();
  void (async () => {
    let restoredObjectURL = null;
    try {
      const saved = JSON.parse(localStorage.getItem(preferenceKey) || '{"mode":"default"}');
      if (saved.mode === "default" || saved.mode === "builtin") {
        preference = builtinPreference(saved);
        refreshBuiltin();
        return;
      }
      if (saved.mode === "url") {
        const url = validateURL(saved.url);
        await loadImage(url);
        if (restoreCanceled) return;
        preference = { mode: "url", url };
        clearAutomaticTimer();
        applyImage(url);
      } else if (saved.mode === "upload") {
        const blob = validateBlob(await storedImage("get"));
        restoredObjectURL = URL.createObjectURL(blob);
        await loadImage(restoredObjectURL);
        if (restoreCanceled) {
          URL.revokeObjectURL(restoredObjectURL);
          return;
        }
        preference = { mode: "upload" };
        clearAutomaticTimer();
        applyImage(restoredObjectURL, restoredObjectURL);
        restoredObjectURL = null;
      }
    } catch {
      if (restoredObjectURL) URL.revokeObjectURL(restoredObjectURL);
      if (!restoreCanceled) {
        preference = defaultPreference();
        refreshBuiltin();
      }
    }
  })();
  wallpaper.addEventListener("error", () => {
    if (preference.mode !== "builtin") {
      preference = defaultPreference();
      refreshBuiltin();
    } else if (wallpaper.getAttribute("src") !== defaultImage) {
      clearAutomaticTimer();
      applyImage(defaultImage);
    }
  });
  document.addEventListener("visibilitychange", () => {
    if (!document.hidden) refreshBuiltin();
  });
  window.addEventListener("focus", refreshBuiltin);
  window.addEventListener("pageshow", refreshBuiltin);

  const prepare = () => {
    if (busy) return;
    restoreCanceled = true;
    form.querySelectorAll('input[name="mode"]').forEach((input) => { input.checked = input.value === preference.mode; });
    pendingTheme = preference.theme || "v2";
    form.elements.appearance.value = preference.appearance || "auto";
    form.elements.dayStart.value = preference.dayStart || "07:00";
    form.elements.nightStart.value = preference.nightStart || "19:00";
    form.elements.url.value = preference.url || "";
    form.elements.picture.value = "";
    error.hidden = true;
    updateFields();
  };
  window.JustVoxelWallpaper = {
    prepare,
    canLeave: () => !committing,
    leave: () => { if (!committing) operation += 1; },
  };
  open.addEventListener("click", () => window.JustVoxelSystem?.openTab("wallpaper"));
  form.querySelectorAll('input[name="mode"]').forEach((input) => input.addEventListener("change", () => {
    error.hidden = true;
    updateFields();
  }));
  form.querySelectorAll('input[name="theme"]').forEach((input) => input.addEventListener("change", () => {
    pendingTheme = input.value;
    form.elements.mode.value = "builtin";
    error.hidden = true;
    updateFields();
  }));
  form.querySelectorAll('input[name="appearance"]').forEach((input) => input.addEventListener("change", () => {
    error.hidden = true;
    updateFields();
  }));
  form.querySelector("[data-wallpaper-cancel]").addEventListener("click", () => {
    if (!committing) { operation += 1; prepare(); }
  });
  form.querySelector("[data-wallpaper-reset]").addEventListener("click", async () => {
    committing = true;
    setBusy(true);
    error.hidden = true;
    try { await reset(); } catch (failure) { showError(failure); }
    finally {
      committing = false; setBusy(false);
      const message = error.hidden ? "" : error.textContent;
      prepare();
      if (message) showError(new Error(message));
    }
  });
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (busy) return;
    const mode = form.elements.mode.value;
    if (!["builtin", "url", "upload"].includes(mode)) return;
    const currentOperation = ++operation;
    let candidateObjectURL = null;
    setBusy(true);
    error.hidden = true;
    try {
      if (mode === "builtin") {
        const next = builtinPreference({
          mode, theme: pendingTheme, appearance: form.elements.appearance.value,
          dayStart: form.elements.dayStart.value, nightStart: form.elements.nightStart.value,
        });
        await loadImage(builtinImage(next));
        if (currentOperation !== operation) return;
        committing = true;
        setBusy(true);
        savePreference(next);
        refreshBuiltin();
      } else if (mode === "url") {
        let url;
        try { url = validateURL(form.elements.url.value.trim()); }
        catch { throw new Error("Use a web address starting with http:// or https://."); }
        await loadImage(url);
        if (currentOperation !== operation) return;
        committing = true;
        setBusy(true);
        savePreference({ mode: "url", url });
        applyImage(url);
      } else if (mode === "upload") {
        // With no replacement selected, keep the previously saved upload.
        const blob = validateBlob(form.elements.picture.files[0] ||
          (preference.mode === "upload" ? await storedImage("get") : null));
        candidateObjectURL = URL.createObjectURL(blob);
        await loadImage(candidateObjectURL);
        if (currentOperation !== operation) return;
        committing = true;
        setBusy(true);
        await storedImage("put", blob);
        savePreference({ mode: "upload" });
        applyImage(candidateObjectURL, candidateObjectURL);
        candidateObjectURL = null;
      }
      // Keep the controls inside System after applying the preference.
    } catch (failure) {
      showError(failure);
    } finally {
      if (candidateObjectURL) URL.revokeObjectURL(candidateObjectURL);
      committing = false;
      setBusy(false);
      if (currentOperation !== operation) prepare();
      else updateFields();
    }
  });
  window.addEventListener("pagehide", (event) => {
    clearAutomaticTimer();
    if (!event.persisted) {
      restoreCanceled = true;
      operation += 1;
    }
    // A cached page still owns its URL and can be shown again without reloading.
    if (!event.persisted && activeObjectURL) {
      URL.revokeObjectURL(activeObjectURL);
      activeObjectURL = null;
    }
  });
})();
