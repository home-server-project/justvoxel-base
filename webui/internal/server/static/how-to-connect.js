(() => {
  'use strict';
  const page = document.querySelector('[data-connect-page]');
  const guide = page?.querySelector('[data-connect-guide]');
  if (!guide) return;
  const switches = Array.from(page.querySelectorAll('[data-connect-method]'));
  const cards = Array.from(page.querySelectorAll('[data-connect-card]'));
  const domain = page.querySelector('#connect-domain');
  const printButton = page.querySelector('[data-connect-print]');
  const message = page.querySelector('[data-connect-message]');
  const summary = page.querySelector('[data-connect-summary]');
  // Presentation only: no DNS lookup, storage, HTML insertion or API request.
  const validHostname = (value) => value.length > 0 && value.length <= 253 &&
    !/^[\d.]+$/.test(value) && value.split('.').every(label =>
      /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/i.test(label));
  const update = () => {
    const custom = switches.find(input => input.dataset.connectMethod === 'custom');
    domain.disabled = !custom?.checked || custom.disabled;
    const hostname = domain.value.trim();
    const invalid = !domain.disabled && !validHostname(hostname);
    domain.setAttribute('aria-invalid', String(invalid));
    domain.setCustomValidity(invalid ? 'Enter a valid hostname without a URL, path or port.' : '');
    page.querySelectorAll('[data-connect-domain-address]').forEach(node => { node.textContent = invalid ? '' : hostname; });
    const javaAddress = page.querySelector('[data-connect-domain-java]');
    if (javaAddress) javaAddress.textContent = invalid ? '' : `${hostname}:${page.dataset.javaPort}`;
    const selected = switches.filter(input => input.checked && !input.disabled).map(input => input.dataset.connectMethod);
    const ready = selected.length > 0 && !invalid;
    cards.forEach(card => { card.hidden = !selected.includes(card.dataset.connectCard) || (card.dataset.connectCard === 'custom' && invalid); });
    summary.textContent = cards.filter(card => !card.hidden).map(card => card.dataset.connectName).join(' · ');
    guide.hidden = !ready;
    printButton.disabled = !ready;
    document.body.classList.toggle('connect-print-ready', ready);
    message.textContent = invalid ? 'Enter a valid custom hostname to preview or save the guide.' :
      selected.length === 0 ? 'Choose at least one available connection method.' : 'Your guide is ready. Save as PDF, then choose Save as PDF in the print dialog.';
    return ready;
  };
  switches.forEach(input => input.addEventListener('change', update));
  domain.addEventListener('input', update);
  printButton.addEventListener('click', () => { if (update()) window.print(); });
  // Also block a misleading guide when printing through the browser menu.
  window.addEventListener('beforeprint', update);
  update();
})();
