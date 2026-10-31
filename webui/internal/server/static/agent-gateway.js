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
    find('[data-gateway-connections]').setAttribute('aria-selected', String(name === 'overview' || (name === 'settings' && Boolean(selectedID))));
    find('[data-gateway-help-open]').setAttribute('aria-selected', String(name === 'help'));
    find('[data-gateway-add]').setAttribute('aria-selected', String(name === 'settings' && !selectedID));
    content.scrollTop = 0;
  };
  const showOverview = () => { if (busy) return; selectedID = ''; details.replaceChildren(); showSurface('overview'); };
  const levelLabel = level => ({1: 'Level 1', 2: 'Level 2', 3: 'Level 3', 4: 'Level 4'}[level] || 'Unknown access level');
  const providers = new Map([
    ['OpenAI', {icon: 'openai', label: 'OpenAI · ChatGPT & Codex'}],
    ['Claude', {icon: 'claude', label: 'Claude'}],
    ['Grok', {icon: 'grok', label: 'Grok'}],
    ['Gemini', {icon: 'googlegemini', label: 'Gemini'}],
    ['GitHub Copilot', {icon: 'githubcopilot', label: 'GitHub Copilot'}],
    ['Other AI / MCP app', {label: 'Other AI / MCP app'}],
    ['Automation / REST API', {label: 'Automation / REST API'}],
  ]);
  const providerAuthMode = purpose => providers.get(purpose)?.icon ? 'oauth_local' : 'token';
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
  const scopeLabel = connection => connection.all_instances ? 'All servers' : (connection.instance_ids || []).map(id => {
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
    if (result.connection?.auth_mode === 'oauth_local' || !result.token) return;
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
      showGatewayState(snapshot.enabled);
      const selected = snapshot.connections.find(c => c.id === selectedID);
      if (surface === 'settings' && selected) renderSettings(selected);
      else if (surface === 'settings' && selectedID) { selectedID = ''; details.replaceChildren(); showSurface('overview'); }
      message('');
    } catch (error) { message(error.message); }
  };
  const mutate = async (payload, onFailure) => {
    if (busy) return;
    busy = true;
    const controls = [...dialog.querySelectorAll('button, input, select')];
    const disabled = controls.map(control => control.disabled);
    controls.forEach(control => { control.disabled = true; });
    try {
      const result = await send(payload);
      if (result.connection?.id) { selectedID = result.connection.id; showSurface('settings'); }
      if (result.connection?.auth_mode !== 'oauth_local') showTokenOnce(result);
      await load();
    } catch (error) { if (onFailure) onFailure(); message(error.message); }
    finally { busy = false; controls.forEach((control, index) => { control.disabled = disabled[index]; }); }
  };
  levelDialog.querySelector('form').addEventListener('submit', event => {
    event.preventDefault();
    if (busy || !pendingLevel4 || Number(levelSlider.value) !== 100 || !levelToggle.checked || !primary()) return;
    const payload = {...pendingLevel4, acknowledged: true, slider: 100};
    levelDialog.close();
    mutate(payload);
  });
  const showGatewayState = enabled => {
    gatewaySwitch.checked = enabled;
    find('[data-gateway-state]').textContent = `Gateway ${enabled ? 'Enabled' : 'Disabled'}`;
    renderCards(enabled);
  };
  gatewaySwitch.addEventListener('change', () => {
    if (busy) return;
    const enabled = gatewaySwitch.checked;
    const previous = snapshot.enabled;
    showGatewayState(enabled);
    mutate({action: 'enable', enabled}, () => showGatewayState(previous));
  });
  const renderCards = (gatewayEnabled = snapshot.enabled) => {
    cards.replaceChildren();
    if (!snapshot.connections.length) cards.append(text('p', 'No connections yet. Select Add to get started.'));
    for (const connection of snapshot.connections) {
      const active = gatewayEnabled && connection.enabled;
      const card = text('article', ''); card.className = `panel minecraft-server-card minecraft-server-health-${active ? 'success' : 'neutral'}`; card.dataset.gatewayConnection = connection.id;
      const heading = text('div', ''); heading.className = 'minecraft-server-heading';
      const state = text('strong', active ? 'Enabled' : 'Disabled'); state.className = `minecraft-server-state ${active ? 'success' : 'neutral'}`;
      heading.append(text('h2', connection.name), state); card.append(heading);
      const identity = text('div', ''); identity.className = 'minecraft-server-software-row';
      const provider = text('p', ''); provider.className = 'muted';
      provider.append(providerIcon(connection.purpose), text('span', purposeLabel(connection.purpose)));
      identity.append(provider); card.append(identity);
      const metrics = text('div', ''); metrics.className = 'minecraft-server-metrics';
      const auth = connection.auth_mode === 'oauth' || connection.auth_mode === 'oauth_local' ? 'OAuth' : 'Token';
      for (const [label, value] of [['Access', levelLabel(connection.level)], ['Scope', scopeLabel(connection)], ['Auth', auth]]) {
        const metric = text('div', ''); metric.append(text('span', label), text('strong', value)); metrics.append(metric);
      }
      card.append(metrics);
      const activity = text('div', ''); activity.className = 'gateway-card-activity';
      activity.append(text('span', 'Last activity'), text('p', activityLabel(connection.last_activity))); card.append(activity);
      const actions = text('div', ''); actions.className = 'action-row gateway-card-actions';
      const button = text('button', 'Settings'); button.type = 'button'; button.className = 'secondary';
      button.addEventListener('click', () => { if (busy) return; selectedID = connection.id; renderSettings(connection); });
      actions.append(button); card.append(actions); cards.append(card);
    }
  };
  // Presentation only: neither provider previews nor Help submit connection changes.
  const copyText = async (value, field, status) => {
    if (navigator.clipboard?.writeText) {
      try {
        await navigator.clipboard.writeText(value);
        status.textContent = 'Copied';
        return;
      } catch (_) { /* LAN HTTP and browser permissions can prevent clipboard access. */ }
    }
    const temporary = document.createElement('textarea');
    temporary.value = value;
    temporary.readOnly = true;
    temporary.className = 'gateway-copy-buffer';
    // Keep the fallback inside the active dialog so modal focus stays usable.
    field.parentNode.append(temporary);
    let copied = false;
    try {
      temporary.focus();
      temporary.select();
      copied = Boolean(document.execCommand && document.execCommand('copy'));
    } catch (_) { /* Leave the original field selected for keyboard copying below. */ }
    finally { temporary.remove(); }
    if (copied) { status.textContent = 'Copied'; return; }
    field.focus();
    field.select();
    field.setSelectionRange(0, value.length);
    status.textContent = 'Selected — press Ctrl+C';
  };
  const settingsDate = value => {
    if (!value) return 'Unavailable';
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : date.toLocaleString(undefined, {
      year: 'numeric', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit',
    });
  };
  const localAddress = address => {
    const url = new URL(address);
    const host = url.hostname.toLowerCase().replace(/^\[|\]$/g, '');
    const octets = host.split('.').map(Number);
    return url.protocol === 'http:' || host === 'localhost' || host.endsWith('.localhost') || host === '::1' ||
      host.startsWith('fc') && host.includes(':') || host.startsWith('fd') && host.includes(':') || host.startsWith('fe80:') ||
      (octets.length === 4 && (octets[0] === 127 || octets[0] === 10 ||
        octets[0] === 192 && octets[1] === 168 || octets[0] === 172 && octets[1] >= 16 && octets[1] <= 31));
  };
  const renderProviderGuide = (host, purpose, address, options = {}) => {
    const mode = options.mode || 'ChatGPT';
    const automation = purpose === 'Automation / REST API';
    host.replaceChildren();
    const identity = text('div', ''); identity.className = 'gateway-connection-identity';
    const titles = text('div', '');
    const subtitle = {OpenAI: 'ChatGPT & Codex', Claude: 'Custom connector', Grok: 'Custom MCP connector', Gemini: 'Custom connected app', 'GitHub Copilot': 'Copilot CLI'}[purpose];
    titles.append(text('h3', purpose));
    if (subtitle) titles.append(text('p', subtitle));
    identity.append(providerIcon(purpose), titles); host.append(identity);
    if (options.ready) host.append(text('h4', 'Ready to connect'));
    const copyBlock = (value, label) => {
      const row = text('div', ''); row.className = 'gateway-address';
      const copy = text('button', label); copy.type = 'button'; copy.className = 'secondary';
      const status = text('p', ''); status.setAttribute('role', 'status');
      const field = document.createElement('input');
      field.type = 'text'; field.readOnly = true; field.value = value;
      field.className = 'gateway-copy-field'; field.setAttribute('aria-label', label === 'Copy command' ? 'Command' : 'Address');
      field.addEventListener('click', () => field.select());
      copy.addEventListener('click', () => copyText(value, field, status));
      row.append(field, copy); host.append(row, status);
    };
    host.append(text('h4', automation ? 'REST API address' : 'MCP address'));
    if (options.pending) host.append(text('p', 'Create the connection, then copy its MCP address from Settings.'));
    else if (options.help && !automation && providers.get(purpose)?.icon) host.append(text('p', 'Copy the connection-specific MCP address from Settings.'));
    else copyBlock(address, 'Copy');
    host.append(text('h4', 'How to connect'));
    if (purpose === 'OpenAI') {
      const tabs = text('div', ''); tabs.className = 'gateway-guide-modes'; tabs.setAttribute('role', 'group'); tabs.setAttribute('aria-label', 'OpenAI app');
      for (const choice of ['ChatGPT', 'Codex']) {
        const button = text('button', choice); button.type = 'button'; button.className = 'secondary'; button.setAttribute('aria-pressed', String(choice === mode));
        button.addEventListener('click', () => renderProviderGuide(host, purpose, address, {...options, mode: choice})); tabs.append(button);
      }
      host.append(tabs);
    }
    const steps = values => {
      const list = text('ol', ''); list.className = 'gateway-help-steps';
      for (const value of values) list.append(text('li', value)); host.append(list);
    };
    const note = (title, body, warning = false) => {
      const box = text('aside', ''); box.className = warning ? 'notice warning' : 'notice';
      box.append(text('strong', title), text('p', body)); host.append(box); return box;
    };
    const commandAddress = options.help || options.pending ? '<MCP_URL>' : address;
    let guide;
    if (purpose === 'OpenAI' && mode === 'ChatGPT') {
      steps(['Open ChatGPT on the web and open Plugins.', 'Select +, then Add custom MCP server.', 'Enter a name and paste the JustVoxel MCP address.', 'Configure the authentication offered by JustVoxel.', 'Review the risk warning and select “I understand and want to continue”.', 'Complete the normal JustVoxel sign-in and approval when ChatGPT starts authentication.', 'Create as a plugin, then install the plugin.']);
      if (localAddress(address)) note('Local address detected', 'ChatGPT cloud cannot connect directly to this local address. Use a reachable HTTPS endpoint or ChatGPT Secure MCP Tunnel.', true);
      else note('Network access', 'ChatGPT cloud needs a reachable HTTPS endpoint or ChatGPT Secure MCP Tunnel.');
      guide = 'https://developers.openai.com/api/docs/guides/custom-mcp-server';
    } else if (purpose === 'OpenAI') {
      copyBlock(`codex mcp add justvoxel --url ${commandAddress}`, 'Copy command');
      host.append(text('p', 'Verify the connection:'), text('code', 'codex mcp list'));
      host.append(text('p', 'Codex opens the JustVoxel OAuth flow when authentication is required. Codex CLI and IDE share MCP configuration.'));
      note('Network access', 'This address can work with Codex when the computer running Codex can reach JustVoxel.');
      guide = 'https://developers.openai.com/learn/docs-mcp';
    } else if (purpose === 'Claude') {
      steps(['Open Customize → Connectors.', 'Add a custom connector.', 'Enter a name and the JustVoxel MCP address, then Continue.', 'Review the detected authentication settings and choose Sign in now or Sign in when needed.', 'Under OAuth client choose Register automatically.', 'Select Add to add the connector.', 'Claude starts the OAuth flow and opens JustVoxel sign-in and approval.']);
      note('OAuth client: Register automatically', 'JustVoxel currently supports Dynamic Client Registration.');
      note('Network access', 'Claude remote connectors require an address reachable by Claude.', localAddress(address));
      guide = 'https://support.claude.com/en/articles/11175166-get-started-with-custom-connectors-using-remote-mcp';
    } else if (purpose === 'Grok') {
      steps(['Open grok.com/connectors.', 'Select New Connector.', 'Select Custom.', 'Enter the JustVoxel MCP address.', 'Complete JustVoxel authentication.']);
      note('Remote access required', 'Grok requires custom MCP servers to be reachable over the public Internet.' + (localAddress(address) ? ' This JustVoxel address is local-only and cannot be reached directly by Grok.' : ''), true);
      guide = 'https://docs.x.ai/grok/connectors';
    } else if (purpose === 'Gemini') {
      steps(['Open gemini.google.com.', 'Open Settings → Connected Apps (or Personal Intelligence → Connected Apps).', 'Under Custom apps, select Add a custom app.', 'Enter the JustVoxel MCP address.', 'Select Next.', 'Complete JustVoxel authentication.']);
      const availability = note('Current Gemini requirements', 'Google may change availability.');
      const list = text('ul', ''); list.className = 'gateway-requirements';
      for (const requirement of ['Age 18+', 'United States', 'Personal Google Account', 'Keep Activity enabled', 'English', 'Initial setup in Gemini web']) list.append(text('li', '✓ ' + requirement));
      availability.append(list);
      host.append(text('p', 'JustVoxel supports Dynamic Client Registration, so manual OAuth client credentials under Advanced features should not normally be required.'));
      note('Network access', 'Gemini remote custom apps require a reachable MCP endpoint.', localAddress(address));
      guide = 'https://support.google.com/gemini/answer/17209137';
    } else if (purpose === 'GitHub Copilot') {
      copyBlock(`copilot mcp add --transport http justvoxel ${commandAddress}`, 'Copy command');
      host.append(text('p', 'If authentication or re-authentication is required, open browser sign-in with:'), text('code', '/mcp auth justvoxel'));
      host.append(text('p', 'Interactive setup is also available:'), text('code', '/mcp add'));
      note('Network access', 'Copilot CLI can use this LAN address when the machine running Copilot can reach JustVoxel.');
      note('Compatibility', 'Copilot cloud agent and Copilot code review currently do not support remote OAuth MCP servers. IDE MCP support varies by IDE and configuration.', true);
      guide = 'https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-mcp-servers';
    } else if (automation) {
      host.append(text('h4', 'Authentication'), text('p', 'JustVoxel API token'));
      steps(['Create the connection.', 'Copy the one-time scoped JustVoxel API token.', 'Store it securely; the token is shown only once.', 'Send it as the connection credential from the automation or client.', 'Requests remain limited by the configured JustVoxel access level and Minecraft scope.']);
    } else {
      steps(['Add a remote Streamable HTTP MCP server in a compatible client.', 'Use this MCP address.', 'Complete the authentication method supported by that client; use the JustVoxel token for token connections.', 'Confirm the connection permissions in JustVoxel.']);
      note('Network access', 'Cloud clients need a reachable endpoint. Local clients may use a LAN endpoint if they can reach JustVoxel.');
    }
    if (guide) {
      const link = text('a', 'Official setup guide ↗'); link.href = guide; link.target = '_blank'; link.rel = 'noopener noreferrer'; host.append(link);
    }
  };
  const setupHelpGuide = () => {
    const selector = find('[data-gateway-help-providers]');
    const guide = find('[data-gateway-help-guide]');
    const buttons = [];
    const select = purpose => {
      buttons.forEach(([name, button]) => button.setAttribute('aria-pressed', String(name === purpose)));
      renderProviderGuide(guide, purpose, location.origin + (purpose === 'Automation / REST API' ? '/api/v1' : '/mcp'), {help: true});
    };
    for (const purpose of providers.keys()) {
      const button = text('button', ''); button.type = 'button'; button.className = 'secondary';
      button.append(providerIcon(purpose), text('span', purpose));
      button.addEventListener('click', () => select(purpose)); selector.append(button); buttons.push([purpose, button]);
    }
    select('OpenAI');
  };
  const renderSettings = connection => {
    const c = structuredClone(connection);
    details.replaceChildren(document.querySelector('[data-gateway-settings-template]').content.cloneNode(true));
    const form = details.querySelector('form');
    const field = name => form.elements.namedItem(name);
    if (c.id) {
      form.className = 'gateway-edit-settings';
      const top = form.querySelector('.gateway-name-access-row');
      const enabled = form.querySelector('.gateway-switch');
      const enabledRow = text('div', ''); enabledRow.className = 'gateway-enabled-control';
      enabledRow.append(text('span', 'Enabled'), enabled);
      top.append(enabledRow);
      const expiration = field('expires_at').closest('label');
      expiration.className = 'gateway-expiration'; top.after(expiration);
      const choices = text('div', ''); choices.className = 'gateway-scope-choices';
      const scope = form.querySelector('[aria-labelledby="gateway-scope-label"]');
      scope.querySelector('h4').after(choices);
      scope.querySelectorAll('.gateway-choice').forEach(choice => choices.append(choice));
      // Advanced stays last; the existing external OAuth fields are moved into it below.
      form.append(form.querySelector('[data-gateway-advanced]'));
    }
    const heading = form.querySelector('[data-gateway-settings-title]');
    heading.hidden = !c.id;
    heading.textContent = c.id ? `${c.name} — settings` : '';
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
    field('enabled').checked = Boolean(c.enabled);
    const syncEnabled = () => {
      if (!c.id) return;
      const label = form.querySelector('.gateway-switch span');
      label.textContent = field('enabled').checked ? 'Enabled' : 'Disabled';
      label.className = field('enabled').checked ? 'gateway-enabled-success' : 'gateway-enabled-danger';
    };
    field('enabled').addEventListener('change', syncEnabled); syncEnabled();
    field('server_scope').value = c.all_instances ? 'all' : 'selected';
    if (c.expires_at) {
      const date = new Date(c.expires_at);
      field('expires_at').value = new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
    }
    const oauthMode = Boolean(c.id) && c.auth_mode === 'oauth';
    const localOAuth = Boolean(c.id) && c.auth_mode === 'oauth_local';
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
      const label = text('label', c.id ? '' : name); label.className = c.id ? 'gateway-server-item' : 'gateway-choice';
      const checkbox = document.createElement('input'); checkbox.type = 'checkbox'; checkbox.name = 'instance_ids'; checkbox.value = id; checkbox.checked = (c.instance_ids || []).includes(id);
      if (c.id) {
        checkbox.className = 'date-time-switch'; checkbox.setAttribute('role', 'switch');
        const icon = document.createElement('img'); icon.className = 'minecraft-server-software-icon'; icon.alt = '';
        icon.src = instances.find(instance => instance.id === id)?.software_icon || '/static/justvoxel-logo.png';
        label.append(checkbox, icon, text('span', name));
      } else label.prepend(checkbox);
      instanceOptions.append(label);
    }
    if (!known.size) instanceOptions.append(text('p', 'No Minecraft servers are available to choose.'));
    const sync = () => {
      const all = field('server_scope').value === 'all';
      form.querySelector('[data-gateway-scope]').hidden = !c.id && all;
      if (c.id) {
        instanceOptions.setAttribute('aria-disabled', String(all));
        instanceOptions.querySelectorAll('input').forEach(input => { input.disabled = all; });
      }
      for (const action of ['rotate', 'revoke']) form.querySelector(`[data-gateway-${action}]`).hidden = !c.id || c.auth_mode !== 'token';
    };
    form.querySelectorAll('input[name="server_scope"]').forEach(input => input.addEventListener('change', sync)); sync();
    const onboarding = text('section', ''); onboarding.className = 'gateway-guide';
    onboarding.setAttribute('aria-live', 'polite');
    form.querySelector(c.id ? '.gateway-primary' : '[data-gateway-settings-title]').after(onboarding);
    const previewProvider = () => {
      const purpose = field('purpose').value;
      const automation = purpose === 'Automation / REST API';
      const address = location.origin + (automation ? '/api/v1' : localOAuth ? '/mcp/' + encodeURIComponent(c.id) : '/mcp');
      renderProviderGuide(onboarding, purpose, address, {ready: Boolean(c.id), pending: !c.id && providerAuthMode(purpose) === 'oauth_local'});
    };
    form.querySelectorAll('input[name="purpose"]').forEach(input => input.addEventListener('change', previewProvider));
    previewProvider();
    const safeDetails = form.querySelector('[data-gateway-safe-details]');
    if (c.id) {
      for (const [name, value] of [['Connection ID', c.id], ['Safe credential fingerprint', c.fingerprint || 'None'], ['Created', settingsDate(c.created_at)], ['Updated', settingsDate(c.updated_at)]]) {
        safeDetails.append(text('dt', name), text('dd', value || 'Unavailable'));
      }
      const status = form.querySelector('[data-gateway-status-details]');
      status.className = 'minecraft-server-metrics gateway-status-metrics';
      const state = !c.enabled ? 'Disabled' : !snapshot.enabled ? 'Gateway off' : 'Ready';
      const auth = c.auth_mode === 'oauth_local' || c.auth_mode === 'oauth' ? 'OAuth' : 'Token';
      for (const [label, value] of [['STATUS', state], ['LAST ACTIVITY', activityLabel(c.last_activity)], ['AUTH', auth], ['SCOPE', c.all_instances ? 'All servers' : `${(c.instance_ids || []).length} servers`]]) {
        const metric = text('div', ''); metric.append(text('span', label), text('strong', value)); status.append(metric);
      }
    }
    const setting = action => form.querySelector(`[data-gateway-${action}]`);
    for (const action of ['rotate', 'revoke', 'delete']) {
      const button = setting(action);
      button.addEventListener('click', () => {
        if (['delete', 'revoke'].includes(action) && !window.confirm(`${action === 'delete' ? 'Delete' : 'Revoke the token for'} ${c.name}? Existing credentials will stop working.`)) return;
        mutate({action, id: c.id});
      });
    }
    form.addEventListener('submit', event => {
      event.preventDefault();
      if (busy || !form.reportValidity()) return;
      const updated = {...c, name: field('name').value.trim(), purpose: field('purpose').value.trim(), auth_mode: c.id ? c.auth_mode : providerAuthMode(field('purpose').value.trim()), level: Number(field('level').value), enabled: field('enabled').checked, all_instances: (field('server_scope').value === 'all')};
      updated.instance_ids = updated.all_instances ? [] : [...form.querySelectorAll('input[name="instance_ids"]:checked')].map(input => input.value);
      if (field('expires_at').value) updated.expires_at = new Date(field('expires_at').value).toISOString();
      else delete updated.expires_at;
      if (updated.auth_mode === 'oauth') updated.oauth = Object.fromEntries(['issuer', 'audience', 'jwks_uri', 'scope', 'subject', 'client_id'].map(name => [name, field(name).value.trim()]));
      else delete updated.oauth;
      const payload = {action: c.id ? 'apply' : 'create', connection: updated};
      if (updated.level === 4 && c.level !== 4) requestLevel4(payload);
      else mutate(payload);
    });
    showSurface('settings');
  };
  find('[data-gateway-add]').addEventListener('click', () => {
    if (busy) return;
    selectedID = '';
    renderSettings({name: '', purpose: 'OpenAI', auth_mode: 'oauth_local', level: 1, enabled: true, all_instances: true, instance_ids: []});
  });
  find('[data-gateway-connections]').addEventListener('click', showOverview);
  find('[data-gateway-help-open]').addEventListener('click', () => { if (!busy) showSurface('help'); });
  for (const button of dialog.querySelectorAll('[data-gateway-copy-address]')) {
    button.addEventListener('click', async () => {
      const status = find('[data-gateway-address-status]');
      const field = find(`[data-gateway-${button.dataset.gatewayCopyAddress}]`);
      await copyText(field.value, field, status);
    });
  }
  for (const [selector, path] of [['[data-gateway-mcp]', '/mcp'], ['[data-gateway-rest]', '/api/v1'], ['[data-gateway-openapi]', '/api/v1/openapi.json'], ['[data-gateway-metadata]', '/.well-known/oauth-protected-resource/mcp']]) {
    const element = find(selector);
    if (element.tagName === 'INPUT') {
      element.value = location.origin + path;
      element.addEventListener('click', () => element.select());
    } else element.textContent = location.origin + path;
    if (element.tagName === 'A') element.href = path;
  }
  setupHelpGuide();
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
