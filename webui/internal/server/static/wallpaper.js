(() => {
  const dialog = document.querySelector("[data-wallpaper-dialog]");
  const open = document.querySelector("[data-wallpaper-open]");
  if (!dialog || !open) return;

  const wallpaper = document.querySelector("[data-dashboard-wallpaper]");
  if (!wallpaper) return;
  const form = dialog.querySelector("[data-wallpaper-form]");
  const error = form.querySelector("[data-wallpaper-error]");
  const defaultImage = "/static/justvoxel-default-wallpaper.jpg";
  const preferenceKey = "justvoxel-wallpaper-preference";
  const allowedTypes = new Set(["image/jpeg", "image/png", "image/webp", "image/avif"]);
  const maxSize = 10 * 1024 * 1024;
  let preference = { mode: "default" };
  let activeObjectURL = null;
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
    if (wallpaper) wallpaper.src = src;
    if (activeObjectURL) URL.revokeObjectURL(activeObjectURL);
    activeObjectURL = objectURL;
  };
  const savePreference = (next) => {
    try {
      localStorage.setItem(preferenceKey, JSON.stringify(next));
    } catch {
      throw new Error("This browser could not save the wallpaper preference.");
    }
    preference = next;
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
    form.querySelector("[data-wallpaper-url-fields]").hidden = mode !== "url";
    form.querySelector("[data-wallpaper-upload-fields]").hidden = mode !== "upload";
    form.elements.url.disabled = mode !== "url";
    form.elements.url.required = mode === "url";
    form.elements.picture.disabled = mode !== "upload";
  };
  const reset = async () => {
    applyImage(defaultImage);
    preference = { mode: "default" };
    // Always attempt both removals, even when one browser storage is unavailable.
    let failure;
    try { savePreference(preference); } catch (problem) { failure = problem; }
    try { await storedImage("delete"); } catch (problem) { failure = failure || problem; }
    if (failure) throw failure;
  };
  void (async () => {
    let restoredObjectURL = null;
    try {
      const saved = JSON.parse(localStorage.getItem(preferenceKey) || '{"mode":"default"}');
      if (saved.mode === "default") return;
      if (saved.mode === "url") {
        const url = validateURL(saved.url);
        await loadImage(url);
        if (restoreCanceled) return;
        preference = { mode: "url", url };
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
        applyImage(restoredObjectURL, restoredObjectURL);
        restoredObjectURL = null;
      }
    } catch {
      if (restoredObjectURL) URL.revokeObjectURL(restoredObjectURL);
      if (!restoreCanceled) {
        preference = { mode: "default" };
        applyImage(defaultImage);
      }
    }
  })();
  wallpaper?.addEventListener("error", () => {
    if (wallpaper.getAttribute("src") !== defaultImage) {
      preference = { mode: "default" };
      applyImage(defaultImage);
    }
  });

  open.addEventListener("click", () => {
    if (dialog.open || busy) return;
    restoreCanceled = true;
    form.elements.mode.value = preference.mode;
    form.elements.url.value = preference.url || "";
    form.elements.picture.value = "";
    error.hidden = true;
    updateFields();
    dialog.showModal();
  });
  form.querySelectorAll('input[name="mode"]').forEach((input) => input.addEventListener("change", () => {
    error.hidden = true;
    updateFields();
  }));
  form.querySelector("[data-wallpaper-cancel]").addEventListener("click", () => {
    if (!committing) dialog.close();
  });
  dialog.addEventListener("cancel", (event) => {
    if (committing) event.preventDefault();
  });
  dialog.addEventListener("close", () => { operation += 1; });
  form.querySelector("[data-wallpaper-reset]").addEventListener("click", async () => {
    committing = true;
    setBusy(true);
    error.hidden = true;
    try { await reset(); dialog.close(); } catch (failure) { showError(failure); }
    finally { committing = false; setBusy(false); updateFields(); }
  });
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (busy) return;
    const currentOperation = ++operation;
    let candidateObjectURL = null;
    setBusy(true);
    error.hidden = true;
    try {
      const mode = form.elements.mode.value;
      if (mode === "default") {
        committing = true;
        setBusy(true);
        await reset();
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
      dialog.close();
    } catch (failure) {
      showError(failure);
    } finally {
      if (candidateObjectURL) URL.revokeObjectURL(candidateObjectURL);
      committing = false;
      setBusy(false);
      updateFields();
    }
  });
  window.addEventListener("pagehide", (event) => {
    // A cached page still owns its URL and can be shown again without reloading.
    if (!event.persisted && activeObjectURL) {
      URL.revokeObjectURL(activeObjectURL);
      activeObjectURL = null;
    }
  });
})();
