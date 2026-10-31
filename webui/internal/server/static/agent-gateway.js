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
  let surface = 'overview';
  const showSurface = name => {
    surface = name;
    find('[data-gateway-overview]').hidden = name !== 'overview';
    details.hidden = name !== 'settings';
    find('[data-gateway-help]').hidden = name !== 'help';
    find('[data-gateway-help-open]').setAttribute('aria-selected', String(name === 'help'));
    find('[data-gateway-add]').setAttribute('aria-selected', String(name === 'settings' && !selectedID));
  };
  const showOverview = () => { if (busy) return; selectedID = ''; details.replaceChildren(); showSurface('overview'); };
  const levelLabel = level => ({1: 'Level 1 — Read Only', 2: 'Level 2 — Basic Management', 3: 'Level 3 — Full Management', 4: 'Level 4 — Full System Control'}[level] || 'Unknown access level');
  const providers = new Map([
    ['OpenAI', {icon: 'openai', label: 'OpenAI · ChatGPT & Codex'}],
    ['Claude', {icon: 'claude', label: 'Claude'}],
    ['Grok', {icon: 'grok', label: 'Grok'}],
    ['Gemini', {icon: 'googlegemini', label: 'Gemini'}],
    ['GitHub Copilot', {icon: 'githubcopilot', label: 'GitHub Copilot'}],
    ['Other AI / MCP app', {label: 'Other AI / MCP app'}],
    ['Automation / REST API', {label: 'Automation / REST API'}],
  ]);
  const legacyPurposes = new Map([['AI Agent', 'AI assistant'], ['API Client', 'Automation or app'], ['Other', 'Other']]);
  const purposeLabel = purpose => providers.get(purpose)?.label || legacyPurposes.get(purpose) || purpose || 'Other AI / MCP app';
  const providerIcon = purpose => {
    const icon = document.createElement('img');
    const provider = providers.get(purpose);
    icon.className = 'gateway-provider-icon';
    icon.alt = ''; // The adjacent provider name supplies the accessible identity.
    icon.src = provider?.icon ? `/static/ai-providers/${provider.icon}.svg` : '/static/justvoxel-logo.png';
    return icon;
  };
  const activityLabel = value => value ? new Date(value).toLocaleString() : 'No activity yet';
  const scopeLabel = connection => connection.all_instances ? 'All Minecraft servers' : (connection.instance_ids || []).map(id => {
    const instance = instances.find(item => item.id === id);
    return instance ? (instance.display_name || instance.name || 'Minecraft server') : 'Unavailable server';
  }).join(', ') || 'No servers selected';
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
      find('[data-gateway-state]').textContent = `Gateway ${snapshot.enabled ? 'Enabled' : 'Disabled'}`;
      find('[data-gateway-summary]').textContent = `${snapshot.connections.length} connections · ${snapshot.connections.filter(c => c.enabled).length} enabled`;
      renderCards();
      const selected = snapshot.connections.find(c => c.id === selectedID);
      if (surface === 'settings' && selected) renderSettings(selected);
      else if (surface === 'settings' && selectedID) { selectedID = ''; details.replaceChildren(); showSurface('overview'); }
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
      if (result.connection?.id) { selectedID = result.connection.id; showSurface('settings'); }
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
    if (!snapshot.connections.length) cards.append(text('p', 'No connections yet. Select Add Connection to get started.'));
    for (const connection of snapshot.connections) {
      const card = text('article', ''); card.className = 'panel minecraft-server-card'; card.dataset.gatewayConnection = connection.id;
      const heading = text('div', ''); heading.className = 'minecraft-server-heading';
      const state = text('strong', connection.enabled ? 'Enabled' : 'Disabled'); state.className = 'minecraft-server-state';
      const identity = text('div', ''); identity.className = 'gateway-connection-identity';
      const summary = text('div', ''); summary.append(text('h2', connection.name), text('p', purposeLabel(connection.purpose)));
      identity.append(providerIcon(connection.purpose), summary);
      heading.append(identity, state); card.append(heading);
      card.append(text('p', levelLabel(connection.level)), text('p', scopeLabel(connection)));
      const activity = text('div', ''); activity.append(text('p', 'Last activity'), text('p', activityLabel(connection.last_activity))); card.append(activity);
      const actions = text('div', ''); actions.className = 'action-row gateway-card-actions';
      const button = text('button', 'Settings'); button.type = 'button'; button.className = 'secondary';
      button.addEventListener('click', () => { if (busy) return; selectedID = connection.id; renderSettings(connection); });
      actions.append(button); card.append(actions); cards.append(card);
    }
  };
  const renderSettings = connection => {
    const c = structuredClone(connection);
    details.replaceChildren(document.querySelector('[data-gateway-settings-template]').content.cloneNode(true));
    showSurface('settings');
    const form = details.querySelector('form');
    const field = name => form.elements.namedItem(name);
    form.querySelector('[data-gateway-settings-title]').textContent = c.id ? `${c.name} — settings` : 'Add Connection';
    for (const name of ['name', 'level']) field(name).value = c[name] ?? '';
    // Keep custom purposes from deployed connections without interpreting them as markup or asset paths.
    if (c.purpose && !providers.has(c.purpose)) {
      const label = text('label', ''); label.className = 'gateway-provider-card';
      const input = document.createElement('input'); input.type = 'radio'; input.name = 'purpose'; input.value = c.purpose;
      label.append(input, providerIcon(c.purpose), text('strong', purposeLabel(c.purpose)));
      form.querySelector('[data-gateway-providers]').append(label);
    }
    field('purpose').value = c.purpose || 'OpenAI';
    if (c.id) {
      form.querySelector('[data-gateway-purpose-label]').textContent = 'Provider';
      form.querySelector('[data-gateway-level-label]').textContent = 'Access level';
    }
    form.querySelector('[data-gateway-save]').textContent = c.id ? 'Apply' : 'Create';
    form.querySelector('[data-gateway-advanced]').hidden = !c.id;
    for (const selector of ['[data-gateway-status]', '[data-gateway-actions]', '[data-gateway-live-note]']) form.querySelector(selector).hidden = !c.id;
    form.querySelectorAll('[data-gateway-back]').forEach(button => button.addEventListener('click', showOverview));
    field('enabled').checked = Boolean(c.enabled);
    field('server_scope').value = c.all_instances ? 'all' : 'selected';
    if (c.expires_at) {
      const date = new Date(c.expires_at);
      field('expires_at').value = new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
    }
    const oauthMode = Boolean(c.id) && c.auth_mode === 'oauth';
    const oauthFields = form.querySelector('[data-gateway-oauth]');
    if (oauthMode) {
      form.querySelector('[data-gateway-advanced]').append(oauthFields);
      oauthFields.hidden = false;
      for (const name of ['issuer', 'audience', 'jwks_uri', 'scope', 'subject', 'client_id']) field(name).value = c.oauth?.[name] || '';
      for (const name of ['issuer', 'audience', 'jwks_uri']) field(name).required = true;
    } else oauthFields.remove();
    const instanceOptions = form.querySelector('[data-gateway-instance-options]');
    const known = new Map(instances.map(instance => [instance.id, instance.display_name || instance.name || 'Minecraft server']));
    for (const id of c.instance_ids || []) if (!known.has(id)) known.set(id, 'Unavailable server');
    for (const [id, name] of known) {
      const label = text('label', name); label.className = 'gateway-choice';
      const checkbox = document.createElement('input'); checkbox.type = 'checkbox'; checkbox.name = 'instance_ids'; checkbox.value = id; checkbox.checked = (c.instance_ids || []).includes(id);
      label.prepend(checkbox); instanceOptions.append(label);
    }
    if (!known.size) instanceOptions.append(text('p', 'No Minecraft servers are available to choose.'));
    const sync = () => {
      form.querySelector('[data-gateway-scope]').hidden = (field('server_scope').value === 'all');
      for (const action of ['rotate', 'revoke']) form.querySelector(`[data-gateway-${action}]`).hidden = !c.id || oauthMode;
    };
    form.querySelectorAll('input[name="server_scope"]').forEach(input => input.addEventListener('change', sync)); sync();
    const safeDetails = form.querySelector('[data-gateway-safe-details]');
    if (c.id) {
      for (const [name, value] of [['Connection ID', c.id], ['Safe credential fingerprint', c.fingerprint || 'None'], ['Created', c.created_at], ['Updated', c.updated_at]]) {
        safeDetails.append(text('dt', name), text('dd', value || 'Unavailable'));
      }
      const status = form.querySelector('[data-gateway-status-details]');
      const credentialStatus = c.auth_mode === 'oauth' ? 'OAuth configured' : c.fingerprint ? 'Token issued' : 'No active token';
      status.append(text('dt', 'Last activity'), text('dd', activityLabel(c.last_activity)), text('dt', 'Credential status'), text('dd', credentialStatus));
      if (c.expires_at) status.append(text('dt', 'Expiration'), text('dd', new Date(c.expires_at).toLocaleString()));
    }
    const setting = action => form.querySelector(`[data-gateway-${action}]`);
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
      const updated = {...c, name: field('name').value.trim(), purpose: field('purpose').value.trim(), auth_mode: c.id ? c.auth_mode : 'token', level: Number(field('level').value), enabled: field('enabled').checked, all_instances: (field('server_scope').value === 'all')};
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
    renderSettings({name: '', purpose: 'OpenAI', auth_mode: 'token', level: 1, enabled: true, all_instances: true, instance_ids: []});
  });
  find('[data-gateway-help-open]').addEventListener('click', () => { if (!busy) showSurface('help'); });
  find('[data-gateway-help]').querySelector('[data-gateway-back]').addEventListener('click', showOverview);
  for (const button of dialog.querySelectorAll('[data-gateway-copy-address]')) {
    button.addEventListener('click', async () => {
      const status = find('[data-gateway-address-status]');
      try { await navigator.clipboard.writeText(find(`[data-gateway-${button.dataset.gatewayCopyAddress}]`).textContent); status.textContent = 'Address copied.'; }
      catch (_) { status.textContent = 'Copy is unavailable here. Select and copy the address manually.'; }
    });
  }
  for (const [selector, path] of [['[data-gateway-mcp]', '/mcp'], ['[data-gateway-rest]', '/api/v1'], ['[data-gateway-openapi]', '/api/v1/openapi.json'], ['[data-gateway-metadata]', '/.well-known/oauth-protected-resource/mcp']]) {
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
