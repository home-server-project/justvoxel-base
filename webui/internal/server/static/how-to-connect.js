(() => {
  'use strict';
  const page = document.querySelector('[data-connect-page]');
  const serverSelect = page?.querySelector('.connect-server');
  serverSelect?.addEventListener('change', () => {
    const params = new URLSearchParams();
    if (serverSelect.value) params.set('server', serverSelect.value);
    if (new URLSearchParams(window.location.search).get('invite') === '1') params.set('invite', '1');
    const query = params.toString();
    window.location.href = '/minecraft/how-to-connect' + (query ? '?' + query : '');
  });
  const guide = page?.querySelector('[data-connect-guide]');
  if (!guide) return;
  const switches = Array.from(page.querySelectorAll('[data-connect-method]'));
  const cards = Array.from(page.querySelectorAll('[data-connect-card]'));
  const domain = page.querySelector('#connect-domain');
  const printButton = page.querySelector('[data-connect-print]');
  const inviteButton = page.querySelector('[data-connect-invite]');
  const invitation = page.querySelector('[data-connect-invitation]');
  const invitationText = page.querySelector('[data-connect-invitation-text]');
  const copyStatus = page.querySelector('[data-connect-copy-status]');
  const summary = page.querySelector('[data-connect-summary]');
  // Presentation only: no DNS lookup, storage, HTML insertion or API request.
  const validHostname = (value) => value.length > 0 && value.length <= 253 &&
    !/^[\d.]+$/.test(value) && value.split('.').every(label =>
      /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/i.test(label));
  const invitationContent = () => {
    const methods = cards.filter(card => !card.hidden).map(card => {
      const lines = [card.dataset.connectName];
      card.querySelectorAll('[data-connect-endpoint]').forEach(endpoint => {
        const edition = endpoint.dataset.connectEndpoint;
        const custom = card.dataset.connectCard === 'custom';
        if (edition === 'Java') {
          const address = custom ? page.querySelector('[data-connect-domain-java]').textContent : endpoint.dataset.connectJoin;
          if (address) lines.push(`Java: ${address}`);
        } else if (edition === 'Bedrock') {
          const address = custom ? endpoint.querySelector('[data-connect-domain-address]').textContent : endpoint.dataset.connectAddress;
          const port = endpoint.dataset.connectPort;
          // A bare Playit hostname has no reported public Bedrock port.
          // Preserve availability, but never turn incomplete data into a joining claim.
          if (address && port) lines.push(`Bedrock: ${address.includes(':') ? `[${address}]` : address}:${port}`);
        }
      });
      return lines.length > 1 ? lines.join('\n') : '';
    }).filter(Boolean);
    return methods.length ? `Join ${page.dataset.connectServerName} on Minecraft\n\n${methods.join('\n\n')}` : '';
  };
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
    inviteButton.disabled = !ready || !invitationContent();
    document.body.classList.toggle('connect-print-ready', ready);
    return ready;
  };
  const openInvitation = () => {
    invitationText.value = '';
    copyStatus.textContent = '';
    if (!update()) return;
    invitationText.value = invitationContent();
    if (!invitationText.value) return;
    invitation.showModal();
  };
  inviteButton.addEventListener('click', openInvitation);
  page.querySelector('[data-connect-invitation-close]').addEventListener('click', () => invitation.close());
  page.querySelector('[data-connect-copy]').addEventListener('click', async () => {
    try {
      await navigator.clipboard.writeText(invitationText.value);
      copyStatus.textContent = 'Copied.';
    } catch (_) {
      copyStatus.textContent = 'Copy unavailable. Select the invitation text and copy it manually.';
    }
  });
  switches.forEach(input => input.addEventListener('change', update));
  domain.addEventListener('input', update);
  printButton.addEventListener('click', () => { if (update()) window.print(); });
  // Also block a misleading guide when printing through the browser menu.
  window.addEventListener('beforeprint', update);
  update();
  if (new URLSearchParams(window.location.search).get('invite') === '1') openInvitation();
})();
