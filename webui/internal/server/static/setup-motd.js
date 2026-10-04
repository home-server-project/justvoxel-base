"use strict";

const setupMOTD = document.querySelector("[data-setup-motd]");
const setupMOTDAutomatic = document.querySelector("[data-setup-motd-automatic]");
if (setupMOTD && setupMOTDAutomatic) {
  const softwareChoices = document.querySelectorAll('input[name="server_type"]');
  const updatePlaceholder = () => {
    if (setupMOTDAutomatic.value !== "true") return;
    const selected = Array.from(softwareChoices).find((choice) => choice.checked);
    const software = { paper: "Paper", purpur: "Purpur", vanilla: "Vanilla" }[selected?.value];
    if (software) {
      setupMOTD.placeholder = `Automatic: JustVoxel ${software} Minecraft <resolved version> Server`;
    }
  };
  setupMOTD.addEventListener("input", () => {
    setupMOTDAutomatic.value = "false";
    setupMOTD.placeholder = "";
  });
  softwareChoices.forEach((choice) => choice.addEventListener("change", updatePlaceholder));
  updatePlaceholder();
}
