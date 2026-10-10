const dateTimePanel = document.querySelector("[data-date-time-panel]");
if (dateTimePanel) {
  const timeFormat = dateTimePanel.querySelector("[data-topbar-time-format]");
  const dateFormat = dateTimePanel.querySelector("[data-topbar-date-format]");
  const display = window.JustVoxelClockDisplay;
  if (display && timeFormat && dateFormat) {
    const preferences = display.read();
    timeFormat.value = preferences.time;
    dateFormat.value = preferences.date;
    const saveDisplay = () => display.save({ time: timeFormat.value, date: dateFormat.value });
    timeFormat.addEventListener("change", saveDisplay);
    dateFormat.addEventListener("change", saveDisplay);
  }
  const form = dateTimePanel.querySelector("[data-date-time-form]");
  const timezone = form.elements.timezone;
  const automatic = form.elements.automatic;
  const date = form.elements.date;
  const time = form.elements.time;
  const state = form.querySelector("[data-date-time-state]");
  const error = form.querySelector("[data-date-time-error]");
  const canonical = form.querySelector("[data-date-time-canonical]");
  const options = form.querySelector("[data-timezone-options]");
  const warning = form.querySelector("[data-date-time-warning]");
  const csrf = document.querySelector("[data-system-workspace-csrf]")?.value || "";
  let loadedDate = "";
  let loadedTime = "";
  const syncManual = () => {
    date.disabled = automatic.checked;
    time.disabled = automatic.checked;
    warning.hidden = automatic.checked;
  };
  const updateClockZone = (zone, localDate = "", localTime = "") => {
    const clock = document.querySelector("[data-topbar-clock]");
    if (clock) clock.dataset.systemTimezone = zone;
    window.dispatchEvent(new CustomEvent("justvoxel-timezone", { detail: { zone, localDate, localTime } }));
  };
  const read = async () => {
    const response = await fetch("/api/system/workspace/date-time", { credentials: "same-origin", cache: "no-store" });
    const payload = await response.json();
    if (!response.ok) throw new Error(payload.error || "Date and time are unavailable.");
    timezone.value = payload.timezone;
    canonical.textContent = payload.timezone;
    automatic.checked = Boolean(payload.automatic);
    date.value = payload.local_date;
    time.value = payload.local_time;
    loadedDate = date.value;
    loadedTime = time.value;
    state.textContent = payload.automatic ? (payload.synchronized ? "Automatic time is synchronized." : "Automatic time is on; synchronization is pending.") : "Automatic time is off.";
    options.replaceChildren(...(payload.timezones || []).map((zone) => {
      const item = document.createElement("span");
      item.dataset.timezoneValue = zone;
      return item;
    }));
    if (!options.dataset.searchReady) {
      window.initializeJustVoxelTimezoneSearch?.(form);
      options.dataset.searchReady = "true";
    }
    syncManual();
    updateClockZone(payload.timezone, payload.local_date, payload.local_time);
  };
  const startRead = () => {
    if (!document.body.classList.contains("role-administrator")) return;
    read().catch((failure) => { error.textContent = failure.message; error.hidden = false; });
  };
  if (document.body.classList.contains("role-pending")) {
    const observer = new MutationObserver(() => {
      if (document.body.classList.contains("role-pending")) return;
      observer.disconnect();
      startRead();
    });
    observer.observe(document.body, { attributes: true, attributeFilter: ["class"] });
  } else startRead();
  const refresh = async () => {
    error.hidden = true;
    state.textContent = "Loading date and time…";
    try { await read(); } catch (failure) { state.textContent = ""; error.textContent = failure.message; error.hidden = false; }
  };
  window.JustVoxelDateTime = { refresh };
  form.querySelector("[data-date-time-cancel]").addEventListener("click", refresh);
  automatic.addEventListener("change", syncManual);
  timezone.addEventListener("input", () => { canonical.textContent = timezone.value; });
  timezone.addEventListener("change", () => { canonical.textContent = timezone.value; });
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    error.hidden = true;
    if (!Array.from(options.children).some((item) => item.dataset.timezoneValue === timezone.value)) {
      error.textContent = "Choose a timezone from the city search results.";
      error.hidden = false;
      return;
    }
    const body = new URLSearchParams({ csrf, timezone: timezone.value, automatic: automatic.checked ? "on" : "off" });
    if (!automatic.checked && (date.value !== loadedDate || time.value !== loadedTime)) { body.set("date", date.value); body.set("time", time.value); }
    const submit = form.querySelector('[type="submit"]');
    submit.disabled = true;
    try {
      const response = await fetch("/api/system/workspace/date-time", { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body });
      const payload = await response.json();
      if (!response.ok) throw new Error(payload.error || "Date and time could not be changed.");
      updateClockZone(payload.timezone, payload.local_date, payload.local_time);
      await read();
    } catch (failure) { error.textContent = failure.message; error.hidden = false; }
    finally { submit.disabled = false; }
  });
}
