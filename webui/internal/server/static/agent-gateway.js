(() => {
  'use strict';
  const dialog = document.querySelector('[data-agent-gateway-dialog]');
  const launcher = document.querySelector('[data-agent-gateway-open]');
  if (!dialog || !launcher) return;
  const find = selector => dialog.querySelector(selector);
  const content = find('[data-gateway-content]');
  const feedback = find('[data-gateway-feedback]');
  const cards = find('[data-gateway-cards]');
  const details = find('[data-gateway-details]');
  const gatewaySwitch = find('[data-gateway-enabled]');
  const tokenDialog = document.querySelector('[data-gateway-token-dialog]');
  const tokenNode = tokenDialog.querySelector('[data-gateway-token]');
  const levelDialog = document.querySelector('[data-gateway-level4-dialog]');
  let snapshot = {enabled: false, connections: []};
  let instances = [];
  let selectedID = '';
  let pendingLevel4 = null;
  let busy = false;
  let generation = 0;
  const primary = () => document.body.classList.contains('primary-administrator');
  const message = text => { feedback.textContent = text; };
  const text = (tag, value) => { const element = document.createElement(tag); element.textContent = value; return element; };
  const send = async payload => {
    if (!primary()) throw new Error('Sign in as the primary voxel administrator.');
    const options = {credentials: 'same-origin', cache: 'no-store', headers: {Accept: 'application/json'}};
    if (payload) {
      options.method = 'POST';
      options.body = new URLSearchParams({csrf: content.dataset.gatewayCsrf, request: JSON.stringify(payload)});
    }
    const response = await fetch('/api/agent-gateway', options);
    if (!response.ok || response.redirected) throw new Error('Connection settings could not be loaded or saved. Check your session, settings, and any pending Factory Reset.');
    return response.json();
  };
  const clearToken = () => { tokenNode.textContent = ''; tokenDialog.querySelector('[data-gateway-copy-status]').textContent = ''; };
  tokenDialog.addEventListener('close', clearToken);
  tokenDialog.addEventListener('cancel', clearToken);
  window.addEventListener('pagehide', clearToken);
  tokenDialog.querySelector('[data-gateway-token-done]').addEventListener('click', () => tokenDialog.close());
  tokenDialog.querySelector('[data-gateway-token-copy]').addEventListener('click', async () => {
    try { await navigator.clipboard.writeText(tokenNode.textContent); tokenDialog.querySelector('[data-gateway-copy-status]').textContent = 'Copied. Store it securely.'; }
    catch (_) { tokenDialog.querySelector('[data-gateway-copy-status]').textContent = 'Copy is unavailable here. Select and copy the token manually.'; }
  });
  const showTokenOnce = result => {
    if (!result.token) return;
    clearToken();
    tokenNode.textContent = result.token;
    result.token = ''; // Never retain a credential in the connection registry or browser storage.
    tokenDialog.showModal();
  };
  const closeLevel4 = () => { pendingLevel4 = null; };
  levelDialog.addEventListener('close', closeLevel4);
  levelDialog.addEventListener('cancel', closeLevel4);
  levelDialog.querySelector('[data-gateway-level4-cancel]').addEventListener('click', () => levelDialog.close());
  initDestructiveConfirmations(levelDialog);
  const levelSlider = levelDialog.querySelector('[data-destructive-slider]');
  const levelToggle = levelDialog.querySelector('[data-destructive-toggle]');
  const requestLevel4 = payload => {
    pendingLevel4 = payload;
    levelDialog.querySelector('[data-gateway-level4-name]').textContent = payload.connection.name;
    levelSlider.value = '0'; levelToggle.checked = false;
    levelDialog.querySelector('[data-destructive-confirmation]')._justVoxelDestructiveSync();
    levelDialog.showModal();
  };
  const load = async () => {
    if (!primary()) return;
    const ticket = ++generation;
    message('Loading connections…');
    try {
      const next = await send();
      // Use the established inventory presentation endpoint; only immutable IDs
      // are submitted as privileged selectors.
      const response = await fetch('/api/minecraft/workspace/selection?selection=none', {credentials: 'same-origin', cache: 'no-store'});
      if (!response.ok || response.redirected) throw new Error('Minecraft instance inventory is unavailable. Refresh before changing scope.');
      const inventory = await response.json();
      if (ticket !== generation) return;
      snapshot = next; instances = inventory.servers || [];
      gatewaySwitch.checked = snapshot.enabled;
      find('[data-gateway-summary]').textContent = `${snapshot.connections.length} configured · ${snapshot.connections.filter(c => c.enabled).length} enabled · Gateway ${snapshot.enabled ? 'Enabled' : 'Disabled'}`;
      renderCards();
      const selected = snapshot.connections.find(c => c.id === selectedID);
      if (selected) renderSettings(selected);
      else { selectedID = ''; details.hidden = true; details.replaceChildren(); }
      message('');
    } catch (error) { message(error.message); }
  };
  const mutate = async payload => {
    if (busy) return;
    busy = true;
    const controls = [...dialog.querySelectorAll('button, input, select')];
    const disabled = controls.map(control => control.disabled);
    controls.forEach(control => { control.disabled = true; });
    try {
      const result = await send(payload);
      if (result.connection?.id) selectedID = result.connection.id;
      showTokenOnce(result);
      await load();
    } catch (error) { message(error.message); }
    finally { busy = false; controls.forEach((control, index) => { control.disabled = disabled[index]; }); }
  };
  levelDialog.querySelector('form').addEventListener('submit', event => {
    event.preventDefault();
    if (busy || !pendingLevel4 || Number(levelSlider.value) !== 100 || !levelToggle.checked || !primary()) return;
    const payload = {...pendingLevel4, acknowledged: true, slider: 100};
    levelDialog.close();
    mutate(payload);
  });
  gatewaySwitch.addEventListener('change', () => {
    const enabled = gatewaySwitch.checked;
    gatewaySwitch.checked = snapshot.enabled;
    mutate({action: 'enable', enabled});
  });
  const renderCards = () => {
    cards.replaceChildren();
    for (const connection of snapshot.connections) {
      const card = text('section', ''); card.className = 'panel'; card.dataset.gatewayConnection = connection.id;
      card.append(text('h3', connection.name));
      card.append(text('p', `${connection.purpose || 'Connection'} · ${connection.auth_mode === 'token' ? 'Scoped API Token' : 'OAuth'} · Level ${connection.level} · ${connection.enabled ? 'Enabled' : 'Disabled'}`));
      card.append(text('p', `Last Activity: ${connection.last_activity ? new Date(connection.last_activity).toLocaleString() : 'No successful activity recorded'}`));
      const button = text('button', 'Settings'); button.type = 'button';
      button.addEventListener('click', () => { if (busy) return; selectedID = connection.id; renderSettings(connection); });
      card.append(button); cards.append(card);
    }
  };
  const renderSettings = connection => {
    const c = structuredClone(connection);
    details.replaceChildren(document.querySelector('[data-gateway-settings-template]').content.cloneNode(true));
    details.hidden = false;
    const form = details.querySelector('form');
    const field = name => form.elements.namedItem(name);
    form.querySelector('[data-gateway-settings-title]').textContent = c.id ? `${c.name} — settings` : 'Add Connection';
    for (const name of ['name', 'purpose', 'auth_mode', 'level']) field(name).value = c[name] ?? '';
    field('auth_mode').disabled = Boolean(c.id);
    for (const name of ['enabled', 'all_instances']) field(name).checked = Boolean(c[name]);
    if (c.expires_at) {
      const date = new Date(c.expires_at);
      field('expires_at').value = new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
    }
    const oauth = c.oauth || {audience: `${location.origin}/mcp`, scope: 'justvoxel:connect'};
    for (const name of ['issuer', 'audience', 'jwks_uri', 'scope', 'subject', 'client_id']) field(name).value = oauth[name] || '';
    const instanceOptions = form.querySelector('[data-gateway-instance-options]');
    const known = new Map(instances.map(instance => [instance.id, instance.display_name || instance.name || instance.id]));
    for (const id of c.instance_ids || []) if (!known.has(id)) known.set(id, 'Unavailable / retired instance');
    for (const [id, name] of known) {
      const label = text('label', `${name} (${id})`);
      const checkbox = document.createElement('input'); checkbox.type = 'checkbox'; checkbox.name = 'instance_ids'; checkbox.value = id; checkbox.checked = (c.instance_ids || []).includes(id);
      label.append(checkbox); instanceOptions.append(label);
    }
    if (!known.size) instanceOptions.append(text('p', 'No registered Minecraft instances. Selected scope grants access only to checked immutable IDs.'));
    const sync = () => {
      const oauthMode = field('auth_mode').value === 'oauth';
      form.querySelector('[data-gateway-oauth]').hidden = !oauthMode;
      for (const name of ['issuer', 'audience', 'jwks_uri']) field(name).required = oauthMode;
      form.querySelector('[data-gateway-scope]').hidden = field('all_instances').checked;
      for (const action of ['rotate', 'revoke']) form.querySelector(`[data-gateway-${action}]`).hidden = !c.id || oauthMode;
    };
    field('auth_mode').addEventListener('change', sync); field('all_instances').addEventListener('change', sync); sync();
    const safeDetails = form.querySelector('[data-gateway-safe-details]');
    for (const [name, value] of [['Connection ID', c.id], ['Safe credential fingerprint', c.fingerprint || (c.auth_mode === 'token' ? 'No active token' : 'OAuth binding')], ['Created', c.created_at], ['Updated', c.updated_at], ['Last Activity', c.last_activity || 'No successful activity recorded']]) {
      safeDetails.append(text('dt', name), text('dd', value || 'Available after creation'));
    }
    const setting = action => form.querySelector(`[data-gateway-${action}]`);
    for (const action of ['delete', 'toggle', 'allowance']) setting(action).hidden = !c.id;
    setting('toggle').textContent = c.enabled ? 'Disable' : 'Enable';
    setting('toggle').addEventListener('click', () => mutate({action: 'apply', connection: {...c, enabled: !c.enabled}}));
    for (const action of ['rotate', 'revoke', 'delete', 'reset-allowance']) {
      const button = action === 'reset-allowance' ? setting('allowance') : setting(action);
      button.addEventListener('click', () => {
        if (['delete', 'revoke'].includes(action) && !window.confirm(`${action === 'delete' ? 'Delete' : 'Revoke the token for'} ${c.name}? Existing credentials will stop working.`)) return;
        mutate({action, id: c.id});
      });
    }
    form.addEventListener('submit', event => {
      event.preventDefault();
      if (busy || !form.reportValidity()) return;
      const updated = {...c, name: field('name').value.trim(), purpose: field('purpose').value.trim(), auth_mode: field('auth_mode').value, level: Number(field('level').value), enabled: field('enabled').checked, all_instances: field('all_instances').checked};
      updated.instance_ids = updated.all_instances ? [] : [...form.querySelectorAll('input[name="instance_ids"]:checked')].map(input => input.value);
      if (field('expires_at').value) updated.expires_at = new Date(field('expires_at').value).toISOString();
      else delete updated.expires_at;
      if (updated.auth_mode === 'oauth') updated.oauth = Object.fromEntries(['issuer', 'audience', 'jwks_uri', 'scope', 'subject', 'client_id'].map(name => [name, field(name).value.trim()]));
      else delete updated.oauth;
      const payload = {action: c.id ? 'apply' : 'create', connection: updated};
      if (updated.level === 4 && c.level !== 4) requestLevel4(payload);
      else mutate(payload);
    });
  };
  find('[data-gateway-add]').addEventListener('click', () => {
    if (busy) return;
    selectedID = '';
    renderSettings({name: '', purpose: '', auth_mode: 'token', level: 1, enabled: true, all_instances: true, instance_ids: []});
  });
  for (const [selector, path] of [['[data-gateway-mcp]', '/mcp'], ['[data-gateway-rest]', '/api/v1'], ['[data-gateway-openapi]', '/api/v1/openapi.json']]) {
    const element = find(selector); element.textContent = location.origin + path;
    if (element.tagName === 'A') element.href = path;
  }
  const workspace = setupWorkspaceWindow(dialog, {onOpen: load});
  launcher.addEventListener('click', () => {
    if (!primary()) return;
    const center = document.querySelector('[data-control-center]'); if (center) center.open = false;
    workspace.open();
  });
  find('[data-gateway-close]').addEventListener('click', () => workspace.close());
  find('[data-gateway-refresh]').addEventListener('click', () => { if (!busy) load(); });
  const restore = () => {
    if (document.body.classList.contains('role-pending')) return false;
    if (primary() && readWorkspaceWindowState('agent-gateway').open) workspace.open();
    return true;
  };
  if (!restore()) {
    const observer = new MutationObserver(() => { if (restore()) observer.disconnect(); });
    observer.observe(document.body, {attributes: true, attributeFilter: ['class']});
  }
})();
