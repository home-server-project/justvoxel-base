(() => {
  let pending = false;
  const closeTab = () => {
    try {
      if (window.opener && !window.opener.closed &&
          window.opener.location.origin === window.location.origin) window.opener.focus();
    } catch {
      // An unavailable opener must not prevent closing.
    }
    try { window.close(); } catch {
      // Keep the cancelled state available when browser policy denies close.
    }
  };
  const showCancelled = () => {
    const main = document.createElement("main");
    for (const [tag, text] of [
      ["h1", "Setup cancelled."],
      ["p", "Minecraft Servers remains open in your original JustVoxel tab."],
      ["p", "You can close this tab."],
    ]) {
      const node = document.createElement(tag);
      node.textContent = text;
      main.appendChild(node);
    }
    const retry = document.createElement("button");
    retry.type = "button";
    retry.textContent = "Close tab";
    retry.addEventListener("click", closeTab);
    main.appendChild(retry);
    document.body.className = "add-server-cancelled";
    document.body.replaceChildren(main);
    document.title = "JustVoxel - Setup cancelled";
    closeTab();
  };
  document.addEventListener("submit", async (event) => {
    const form = event.target;
    if (!(form instanceof HTMLFormElement)) return;
    const action = new URL(event.submitter?.getAttribute("formaction") || form.action, window.location.href);
    if (action.origin !== window.location.origin ||
        !["/minecraft/servers/add/cancel", "/minecraft/servers/add/review/cancel"].includes(action.pathname) ||
        form.method.toLowerCase() !== "post") return;
    event.preventDefault();
    if (pending) return;
    pending = true;
    const button = event.submitter;
    if (button) button.disabled = true;
    let error = form.querySelector("[data-add-server-cancel-error]");
    if (error) error.remove();
    try {
      const response = await fetch(action.href, {
        method: "POST", body: new FormData(form), credentials: "same-origin",
        headers: { Accept: "application/json" }, redirect: "error",
      });
      if (response.status !== 204) throw new Error("Cancellation was not confirmed");
      showCancelled();
    } catch {
      error = document.createElement("p");
      error.className = "notice error";
      error.dataset.addServerCancelError = "";
      error.setAttribute("role", "alert");
      error.textContent = "Setup could not be cancelled. Please try Cancel again.";
      form.appendChild(error);
      pending = false;
      if (button) button.disabled = false;
    }
  });
})();
