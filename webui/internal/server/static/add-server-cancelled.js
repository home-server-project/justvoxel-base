(() => {
  const closeTab = () => {
    try {
      if (window.opener && !window.opener.closed &&
          window.opener.location.origin === window.location.origin) {
        window.opener.focus();
      }
    } catch {
      // An unavailable or cross-origin opener must not prevent closing.
    }
    try {
      window.close();
    } catch {
      // The completion message stays visible when closing is denied.
    }
  };
  document.querySelector("[data-close-add-server]")?.addEventListener("click", closeTab);
  closeTab();
})();
