const initializeTimezoneSearch = (root = document) => {
const timezoneInput = root.querySelector("[data-timezone-search]");
const timezoneResults = root.querySelector("[data-timezone-results]");
const timezoneSource = Array.from(root.querySelectorAll("[data-timezone-value]"))
  .map((node) => node.dataset.timezoneValue || "")
  .filter(Boolean);

if (timezoneInput && timezoneResults && timezoneSource.length > 0) {
  let activeIndex = -1;
  let currentMatches = [];

  const cityLabel = (zone) => {
    if (zone === "UTC") return "UTC";
    const parts = zone.split("/");
    return (parts[parts.length - 1] || zone).replaceAll("_", " ");
  };

  const scoreZone = (zone, query) => {
    const lower = zone.toLowerCase();
    const city = cityLabel(zone).toLowerCase();
    if (city === query) return 0;
    if (city.startsWith(query)) return 1;
    if (lower.startsWith(query)) return 2;
    if (city.includes(query)) return 3;
    if (lower.includes(query)) return 4;
    return 99;
  };

  const hideTimezoneResults = () => {
    timezoneResults.hidden = true;
    timezoneResults.replaceChildren();
    timezoneInput.setAttribute("aria-expanded", "false");
    activeIndex = -1;
    currentMatches = [];
  };

  const chooseTimezone = (zone) => {
    timezoneInput.value = zone;
    timezoneInput.dispatchEvent(new Event("change", { bubbles: true }));
    hideTimezoneResults();
    timezoneInput.focus();
  };

  const renderTimezoneResults = () => {
    const query = timezoneInput.value.trim().toLowerCase();
    if (!query) {
      hideTimezoneResults();
      return;
    }

    currentMatches = timezoneSource
      .map((zone) => ({ zone, score: scoreZone(zone, query) }))
      .filter((item) => item.score < 99)
      .sort((a, b) => a.score - b.score || a.zone.localeCompare(b.zone))
      .slice(0, 10)
      .map((item) => item.zone);

    timezoneResults.replaceChildren();
    activeIndex = -1;
    if (currentMatches.length === 0) {
      timezoneResults.hidden = true;
      timezoneInput.setAttribute("aria-expanded", "false");
      return;
    }

    currentMatches.forEach((zone, index) => {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "setup-timezone-option";
      button.setAttribute("role", "option");
      button.setAttribute("aria-selected", "false");
      button.dataset.timezoneIndex = String(index);

      const city = document.createElement("strong");
      city.textContent = cityLabel(zone);
      const canonical = document.createElement("small");
      canonical.textContent = zone;
      button.append(city, canonical);
      button.addEventListener("click", () => chooseTimezone(zone));
      timezoneResults.appendChild(button);
    });

    timezoneResults.hidden = false;
    timezoneInput.setAttribute("aria-expanded", "true");
  };

  const setActiveTimezone = (next) => {
    const buttons = Array.from(timezoneResults.querySelectorAll(".setup-timezone-option"));
    if (buttons.length === 0) return;
    activeIndex = (next + buttons.length) % buttons.length;
    buttons.forEach((button, index) => {
      button.setAttribute("aria-selected", index === activeIndex ? "true" : "false");
    });
    buttons[activeIndex].scrollIntoView({ block: "nearest" });
  };

  timezoneInput.addEventListener("input", renderTimezoneResults);
  timezoneInput.addEventListener("focus", renderTimezoneResults);
  timezoneInput.addEventListener("keydown", (event) => {
    if (event.key === "ArrowDown" && !timezoneResults.hidden) {
      event.preventDefault();
      setActiveTimezone(activeIndex + 1);
    } else if (event.key === "ArrowUp" && !timezoneResults.hidden) {
      event.preventDefault();
      setActiveTimezone(activeIndex - 1);
    } else if (event.key === "Enter" && !timezoneResults.hidden && currentMatches.length > 0) {
      event.preventDefault();
      chooseTimezone(currentMatches[activeIndex >= 0 ? activeIndex : 0]);
    } else if (event.key === "Escape") {
      hideTimezoneResults();
    }
  });

  root.addEventListener("click", (event) => {
    if (event.target === timezoneInput || timezoneResults.contains(event.target)) return;
    hideTimezoneResults();
  });
}

};
window.initializeJustVoxelTimezoneSearch = initializeTimezoneSearch;
