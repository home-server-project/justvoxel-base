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
    ['OpenClaw', {icon: 'openclaw', label: 'OpenClaw'}],
    ['Other AI / MCP app', {label: 'Other AI / MCP app'}],
    ['Automation / REST API', {label: 'Automation / REST API'}],
  ]);
  const providerAuthMode = purpose => providers.get(purpose)?.icon ? 'oauth_local' : 'token';
  const providerCompatibility = new Map([
    ['OpenAI', 'ChatGPT: Secure MCP Tunnel or reachable public HTTPS · Codex: local/LAN supported'],
    ['Claude', 'Claude: remotely reachable MCP required · Claude Code: local/LAN supported'],
    ['Grok', 'Public Internet MCP endpoint required'],
    ['Gemini', 'Public HTTPS Streamable HTTP endpoint required'],
    ['GitHub Copilot', 'CLI & VS Code: local/LAN supported'],
    ['OpenClaw', 'Control UI & CLI: local/LAN supported'],
    ['Other AI / MCP app', 'Depends on whether the MCP client runs locally or in the cloud'],
    ['Automation / REST API', 'Client must be able to reach the REST API endpoint'],
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
      const actions = text('details', ''); actions.className = 'storage-action-menu minecraft-server-more';
      const button = text('summary', '⋯'); button.setAttribute('aria-label', `Settings for ${connection.name}`);
      button.addEventListener('click', event => {
        event.preventDefault();
        if (busy) return;
        selectedID = connection.id; renderSettings(connection);
      });
      actions.append(button); identity.append(provider, actions); card.append(identity);
      const metrics = text('div', ''); metrics.className = 'minecraft-server-metrics';
      const auth = connection.auth_mode === 'oauth' || connection.auth_mode === 'oauth_local' ? 'OAuth' : 'Token';
      for (const [label, value] of [['Access', levelLabel(connection.level)], ['Scope', scopeLabel(connection)], ['Auth', auth]]) {
        const metric = text('div', ''); metric.append(text('span', label), text('strong', value)); metrics.append(metric);
      }
      card.append(metrics);
      const activity = text('div', ''); activity.className = 'gateway-card-activity';
      activity.append(text('span', 'Last activity'), text('p', activityLabel(connection.last_activity))); card.append(activity);
      cards.append(card);
    }
  };
  // Presentation only: neither provider previews nor Help submit connection changes.
  const copyText = async (value, field, status, button) => {
    const originalLabel = button.gatewayCopyLabel || button.textContent;
    button.gatewayCopyLabel = originalLabel;
    const feedback = copied => {
      clearTimeout(button.gatewayCopyTimer);
      button.textContent = copied ? 'Copied' : 'Selected';
      button.title = copied ? 'Copied' : 'Address selected — press Ctrl+C';
      status.textContent = button.title;
      button.gatewayCopyTimer = setTimeout(() => {
        button.textContent = originalLabel;
        button.title = '';
        status.textContent = '';
      }, 1800);
    };
    if (navigator.clipboard?.writeText) {
      try {
        await navigator.clipboard.writeText(value);
        feedback(true);
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
    if (copied) { feedback(true); return; }
    field.focus();
    field.select();
    field.setSelectionRange(0, value.length);
    feedback(false);
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
    return host === 'localhost' || host.endsWith('.localhost') || host === '::1' ||
      host.includes(':') && (/^f[cd][0-9a-f]{2}:/.test(host) || /^fe[89ab][0-9a-f]:/.test(host)) ||
      (octets.length === 4 && (octets[0] === 127 || octets[0] === 10 ||
        octets[0] === 192 && octets[1] === 168 || octets[0] === 172 && octets[1] >= 16 && octets[1] <= 31));
  };
  const secureAddress = address => new URL(address).protocol === 'https:';
  // Modes affect instructions only; connection purpose and authentication stay unchanged.
  const clientModes = {
    OpenAI: {'ChatGPT': 'cloud', 'Codex': 'local'},
    Claude: {'Claude': 'cloud', 'Claude Code': 'local'},
    'GitHub Copilot': {'CLI': 'local', 'VS Code': 'local'},
    OpenClaw: {'Control UI': 'local', 'CLI': 'local'},
    Grok: {'Grok': 'cloud'}, Gemini: {'Gemini': 'cloud'},
    'Other AI / MCP app': {'Other AI / MCP app': 'client'},
    'Automation / REST API': {'Automation / REST API': 'rest'},
  };
  const renderProviderGuide = (host, purpose, address, options = {}) => {
    const modes = Object.keys(clientModes[purpose] || {});
    const mode = modes.includes(options.mode) ? options.mode : modes[0];
    const reachability = clientModes[purpose]?.[mode];
    const automation = purpose === 'Automation / REST API';
    const privateEndpoint = localAddress(address);
    const httpsEndpoint = secureAddress(address);
    const definitelyPrivate = privateEndpoint === true;
    const cloudCandidate = privateEndpoint === false && httpsEndpoint === true;
    const prerequisite = reachability === 'cloud' && !cloudCandidate;
    host.replaceChildren();
    if (!options.settings) {
      const identity = text('div', ''); identity.className = 'gateway-connection-identity';
      const titles = text('div', '');
      const subtitle = {OpenAI: 'ChatGPT & Codex', Claude: 'Claude & Claude Code', Grok: 'Custom MCP connector', Gemini: 'Custom connected app', 'GitHub Copilot': 'CLI & VS Code', OpenClaw: 'Control UI & CLI'}[purpose];
      titles.append(text('h3', purpose));
      if (subtitle) titles.append(text('p', subtitle));
      identity.append(providerIcon(purpose), titles); host.append(identity);
    }
    const copyBlock = (value, label, caption) => {
      const row = text('div', ''); row.className = 'gateway-address';
      const copy = text('button', label); copy.type = 'button'; copy.className = 'secondary';
      copy.setAttribute('data-gateway-copy-label', label === 'Copy' ? 'Selected' : label);
      const status = text('p', ''); status.className = 'gateway-visually-hidden'; status.setAttribute('role', 'status');
      const field = document.createElement('input');
      field.type = 'text'; field.readOnly = true; field.value = value;
      field.className = 'gateway-copy-field'; field.setAttribute('aria-label', caption || (label === 'Copy command' ? 'Command' : 'Address'));
      field.addEventListener('click', () => field.select());
      copy.addEventListener('click', () => copyText(value, field, status, copy));
      if (caption) row.append(text('span', caption));
      row.append(field, copy); host.append(row, status);
    };
    const addressLabel = automation ? 'REST API address' : 'MCP address';
    if (!prerequisite) {
      if (options.help && !automation && providers.get(purpose)?.icon) host.append(text('p', 'Copy the connection-specific MCP address from Settings if this client can reach it.'));
      else copyBlock(address, 'Copy', addressLabel);
    }
    const toolbar = options.settings ? text('div', '') : host;
    if (options.settings) { toolbar.className = 'gateway-guide-toolbar'; host.append(toolbar); }
    toolbar.append(text('h4', 'How to connect'));
    if (modes.length > 1) {
      const tabs = text('div', ''); tabs.className = 'gateway-guide-modes'; tabs.setAttribute('role', 'group'); tabs.setAttribute('aria-label', purpose + ' app');
      for (const choice of modes) {
        const button = text('button', choice); button.type = 'button'; button.className = 'secondary'; button.setAttribute('aria-pressed', String(choice === mode));
        button.addEventListener('click', () => renderProviderGuide(host, purpose, address, {...options, mode: choice})); tabs.append(button);
      }
      toolbar.append(tabs);
    }
    const steps = values => {
      const list = text('ol', ''); list.className = 'gateway-help-steps';
      for (const value of values) list.append(text('li', value)); host.append(list);
    };
    const inline = value => {
      const line = text('p', value); line.className = 'gateway-inline-note'; host.append(line);
    };
    const link = (label, url, parent = host) => {
      const anchor = text('a', label); anchor.href = url; anchor.target = '_blank'; anchor.rel = 'noopener noreferrer'; parent.append(anchor);
    };
    const tunnelGuide = 'https://developers.openai.com/api/docs/guides/secure-mcp-tunnels';
    const official = {
      OpenAI: mode === 'Codex' ? 'https://developers.openai.com/learn/docs-mcp' : 'https://help.openai.com/en/articles/12584461-connecting-custom-mcp-servers-in-chatgpt',
      Claude: mode === 'Claude Code' ? 'https://code.claude.com/docs/en/mcp' : 'https://support.claude.com/en/articles/11175166-get-started-with-custom-connectors-using-remote-mcp',
      Grok: 'https://docs.x.ai/grok/connectors', Gemini: 'https://support.google.com/gemini/answer/17209137',
      'GitHub Copilot': mode === 'VS Code' ? 'https://code.visualstudio.com/docs/agent-customization/mcp-servers' : 'https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-mcp-servers',
      OpenClaw: mode === 'CLI' ? 'https://docs.openclaw.ai/cli/mcp/transports' : 'https://docs.openclaw.ai/tools/mcp',
    }[purpose];
    if (official && !prerequisite) link('Official setup guide ↗', official, toolbar);
    if (purpose === 'Claude' && mode === 'Claude' && options.help) {
      inline('For Team/Enterprise, an Owner or Primary Owner first adds the connector from Organization settings → Connectors.');
    }
    if (purpose === 'Gemini') {
      inline('Requirements: 18+ · United States · Personal Google Account · Keep Activity on · English. Initial setup in Gemini web.');
      inline('JustVoxel supports Dynamic Client Registration. Do not manually enter OAuth client credentials unless Google explicitly reports that automatic registration failed.');
    }
    if (options.help) {
      if (purpose === 'OpenAI' && mode === 'ChatGPT') {
        host.append(text('h4', 'Option 1 — Secure MCP Tunnel'));
        inline('ChatGPT → OpenAI Secure MCP Tunnel → tunnel-client on a machine that can reach JustVoxel → private JustVoxel MCP endpoint.');
        inline('The tunnel-client makes an outbound connection to OpenAI; the JustVoxel MCP server itself can remain private on the LAN. Inbound exposure of port 8099 is not required merely for the tunnel.');
        inline('Tunnel setup depends on OpenAI Platform/workspace availability and permissions. It requires Platform tunnel configuration and a running tunnel-client.');
        link('Official Secure MCP Tunnel guide', tunnelGuide);
        host.append(text('h4', 'Option 2 — Public HTTPS endpoint'));
        inline('Expose the JustVoxel MCP endpoint through a secure public HTTPS endpoint reachable by ChatGPT, then configure the ChatGPT custom MCP/plugin with that HTTPS endpoint. JustVoxel OAuth remains the authentication flow.');
        link('Official ChatGPT MCP guide', official);
      } else if (purpose === 'Claude' && mode === 'Claude') {
        inline('Claude remote connector requests originate from Anthropic infrastructure. A private JustVoxel LAN URL is not directly reachable; use an MCP endpoint reachable from Anthropic infrastructure.');
      } else if (purpose === 'OpenAI' && mode === 'Codex') {
        inline('Codex may use the JustVoxel LAN MCP URL directly when the machine running Codex can reach JustVoxel.');
      } else if (purpose === 'Claude' && mode === 'Claude Code') {
        inline('Claude Code is a local client. It can use the JustVoxel LAN HTTP MCP endpoint when its machine can reach JustVoxel.');
      } else if (purpose === 'Grok') {
        inline('Grok custom MCP requires public Internet reachability. Private LAN URLs do not work directly. xAI documents tunneling for local MCP servers in its official tunneling guide.');
      } else if (purpose === 'Gemini') {
        inline('Gemini custom Connected Apps require a publicly reachable HTTPS Streamable HTTP MCP endpoint. Private LAN HTTP does not qualify. JustVoxel does not provide an automatic Google tunnel.');
        if (!prerequisite) link('Official endpoint prerequisites', 'https://developers.google.com/connected-apps/gpc/prerequisites');
      } else if (purpose === 'GitHub Copilot') {
        inline('Copilot CLI and VS Code run locally. Their machine must be able to reach JustVoxel, so a private LAN URL may work directly.');
      } else if (purpose === 'OpenClaw') {
        inline('The OpenClaw Gateway owns the MCP connection for Control UI and CLI. If that Gateway can reach JustVoxel on the LAN, it can use the private endpoint directly. Public Internet exposure is not required just for that OpenClaw connection.');
      }
    }
    if (prerequisite) {
      host.append(text('h4', 'Connection prerequisite'));
      if (purpose === 'OpenAI') {
        host.append(text('strong', 'ChatGPT needs a reachable connection'));
        host.append(text('p', definitelyPrivate
          ? `The current JustVoxel MCP endpoint is inside your local network. Do not paste this ${new URL(address).hostname} address into ChatGPT.`
          : 'The current JustVoxel MCP endpoint is not HTTPS. Use a public HTTPS MCP endpoint that ChatGPT can reach, or Secure MCP Tunnel.'));
        if (!options.help) {
          host.append(text('p', 'For a private JustVoxel server, use ChatGPT Secure MCP Tunnel. Alternatively expose JustVoxel through a public HTTPS MCP endpoint.'));
          inline('Secure MCP Tunnel runs OpenAI’s tunnel-client on a machine that can already reach JustVoxel and creates an outbound path to OpenAI. The JustVoxel MCP server itself can remain private.');
          link('Secure MCP Tunnel guide', tunnelGuide);
          link('Official ChatGPT MCP guide', official);
        }
        if (definitelyPrivate) inline(`Local JustVoxel endpoint (LOCAL reference only): ${address}`);
      } else if (purpose === 'Claude') {
        host.append(text('strong', 'Claude needs a remotely reachable MCP endpoint'));
        host.append(text('p', definitelyPrivate
          ? 'Claude remote connectors connect from Anthropic infrastructure. The current JustVoxel address is private and cannot be reached directly.'
          : 'Claude remote connectors connect from Anthropic infrastructure. Open JustVoxel through an HTTPS MCP endpoint reachable from that infrastructure.'));
        host.append(text('p', 'Make the MCP endpoint reachable from Anthropic infrastructure before continuing. This also applies to the normal remote-connector flow in Claude Desktop.'));
        link('Official setup guide ↗', official);
      } else if (purpose === 'Grok') {
        host.append(text('strong', 'Public endpoint required'));
        host.append(text('p', definitelyPrivate
          ? 'Grok cannot use the current private JustVoxel address directly. xAI requires a custom MCP server to be reachable over the public internet.'
          : 'Use a secure public HTTPS MCP endpoint that Grok can reach over the public internet.'));
        host.append(text('p', 'Use a secure public endpoint or a tunnel, then return here with JustVoxel opened through that endpoint.'));
        link('Official Grok tunneling guide', 'https://docs.x.ai/grok/connectors/custom-mcp-tunneling');
        link('Official setup guide ↗', official);
      } else if (purpose === 'Gemini') {
        host.append(text('strong', 'Public HTTPS endpoint required'));
        host.append(text('p', 'Gemini custom Connected Apps require a publicly reachable HTTPS Streamable HTTP MCP endpoint. The current JustVoxel endpoint does not meet that requirement.'));
        link('Official setup guide ↗', official);
        link('Official endpoint prerequisites', 'https://developers.google.com/connected-apps/gpc/prerequisites');
      }
      return;
    }
    const commandAddress = options.help ? '<MCP_URL>' : address;
    if (reachability === 'cloud') inline(`The HTTPS MCP endpoint must be reachable by ${purpose === 'OpenAI' ? 'ChatGPT' : purpose === 'Claude' ? 'Anthropic infrastructure' : purpose}. Its URL alone does not prove reachability.`);
    if (purpose === 'OpenAI' && mode === 'ChatGPT') {
      steps(['Open ChatGPT → Plugins.', 'Select + → Add custom MCP server.', 'Under Connection, enter the public HTTPS MCP endpoint. If using Secure MCP Tunnel, choose Tunnel instead and select the tunnel.', 'Configure authentication.', 'Review the risk warning.', 'Select Create as a plugin.', 'Complete JustVoxel sign-in/approval when prompted.']);
      inline('If your ChatGPT desktop Settings shows Plugins → MCPs → Add, choose Streamable HTTP for a URL-based JustVoxel connection. STDIO is for launching a local MCP process and is not the JustVoxel HTTP endpoint. Enter the reachable HTTPS MCP address in URL. Leave Bearer token env var and Headers empty for JustVoxel OAuth.');
      link('Secure MCP Tunnel guide', tunnelGuide);
      if (options.help) link('Official ChatGPT plugin guide', 'https://developers.openai.com/plugins/deploy/connect-chatgpt');
    } else if (purpose === 'OpenAI') {
      copyBlock(`codex mcp add justvoxel --url ${commandAddress}`, 'Copy command');
      host.append(text('code', 'codex mcp list'));
      inline('If authentication is required, Codex opens the JustVoxel sign-in flow.');
      if (definitelyPrivate) inline('This local address can work when the computer running Codex can reach JustVoxel.');
    } else if (purpose === 'Claude' && mode === 'Claude Code') {
      copyBlock(`claude mcp add --transport http justvoxel ${commandAddress}`, 'Copy command');
      for (const command of ['claude mcp login justvoxel', 'claude mcp get justvoxel', 'claude mcp list']) host.append(text('code', command));
      inline('Claude Code connects from the machine where the CLI is running. This local JustVoxel address can work when that machine can reach the server.');
      inline('Remote HTTP is the recommended transport. HTTP servers support OAuth; claude mcp login runs the configured server’s OAuth flow. No static bearer token is required for JustVoxel oauth_local. Interactive alternative inside Claude Code: /mcp');
    } else if (purpose === 'Claude') {
      steps(['For Pro/Max: open Claude → Customize → Connectors.', 'Select + Add → Add custom connector.', 'Enter a name and the JustVoxel MCP URL.', 'Select Continue.', 'Choose Sign in now or Sign in when needed.', 'Under OAuth client choose Register automatically.', 'Add the connector.', 'Complete JustVoxel sign-in and approval.']);
      inline('JustVoxel supports Dynamic Client Registration.');
    } else if (purpose === 'Grok') {
      steps(['Open grok.com/connectors.', 'Select New Connector → Custom.', 'Enter the reachable JustVoxel MCP URL.', 'Complete authentication.']);
      link('Official Grok tunneling guide', 'https://docs.x.ai/grok/connectors/custom-mcp-tunneling');
    } else if (purpose === 'Gemini') {
      steps(['Open gemini.google.com.', 'Settings → Connected Apps. If needed: Personal Intelligence → Connected Apps.', 'Under Custom apps select Add a custom app.', 'Enter the JustVoxel MCP URL.', 'Select Next.', 'Complete JustVoxel authentication.']);
    } else if (purpose === 'GitHub Copilot') {
      if (mode === 'VS Code') {
        steps(['Open Command Palette.', 'Run MCP: Add Server.', 'Choose HTTP.', 'Paste the JustVoxel MCP endpoint.', 'Enter server ID: justvoxel.', 'Choose Global.', 'Choose Copilot Global.', 'Complete OAuth/browser authentication if requested.']);
        inline('HTTP is correct because JustVoxel provides a URL. Command/stdio is for launching a local MCP process.');
        if (options.help) inline('Alternative: Chat → Open Customizations → MCP Servers → Add Server.');
      } else {
        copyBlock(`copilot mcp add --transport http justvoxel ${commandAddress}`, 'Copy command');
        host.append(text('code', '/mcp auth justvoxel'), text('code', 'copilot mcp list'));
        inline('Interactive alternative: /mcp add — choose HTTP and press Ctrl+S to save. JustVoxel is a remote HTTP server; STDIO is for launching a local MCP process. /mcp auth justvoxel starts OAuth re-authentication for a remote MCP server.');
      }
      if (definitelyPrivate) inline(`This local address can work when the computer running ${mode === 'VS Code' ? 'VS Code' : 'Copilot CLI'} can reach JustVoxel.`);
      inline('Repository-level Copilot cloud agent and code review do not currently support remote OAuth MCP servers.');
    } else if (purpose === 'OpenClaw') {
      if (mode === 'CLI') {
        copyBlock(`openclaw mcp add justvoxel --url ${commandAddress} --transport streamable-http`, 'Copy command');
        const config = JSON.stringify({url: commandAddress, transport: 'streamable-http', auth: 'oauth'}).replace(/'/g, "'\\''");
        inline('Configure OAuth if needed:');
        copyBlock(`openclaw mcp set justvoxel '${config}'`, 'Copy command');
        host.append(text('code', 'openclaw mcp login justvoxel'));
        inline('OpenClaw opens the JustVoxel OAuth authorization flow during login.');
      } else {
        steps(['Open OpenClaw Control UI → Settings → MCP.', 'Under Configured servers select Add server.', 'Name: JustVoxel.', 'Transport: Streamable HTTP.', 'Paste the JustVoxel MCP URL.', 'Select Add server.', 'Configure auth as OAuth and select Sign in when available.', 'Verify: openclaw mcp doctor justvoxel --probe']);
        if (options.help) inline('Composer: + → Plugins → Add MCP server…');
      }
      host.append(text('code', 'openclaw mcp doctor justvoxel --probe'), text('code', 'openclaw mcp status --verbose'));
      if (definitelyPrivate) inline('This local address can work when the OpenClaw Gateway can reach JustVoxel.');
    } else if (automation) {
      steps(['Copy the REST API address. The REST client/automation must be able to reach it.', 'Copy the one-time JustVoxel API token after creating the connection.', 'Store the token securely.', 'Use it as the connection credential in the automation/client.']);
    } else {
      inline('This connection type depends on where your MCP client runs. A local client can use this LAN address when it can reach JustVoxel. A cloud-hosted client usually needs an endpoint reachable from its own infrastructure.');
      steps(['Find the MCP server settings in your client.', 'Choose Streamable HTTP / HTTP for a URL-based server.', 'Paste the JustVoxel MCP address if that client can reach it.', 'Use the client’s OAuth flow when supported.']);
      inline('Compatibility depends on the client. For token connections, use the scoped JustVoxel API token.');
    }
  };
  // Both form modes keep individual draft selections while All servers is on.
  const renderMinecraftScope = (form, c) => {
    const field = name => form.elements.namedItem(name);
    const allServers = form.querySelector('[data-gateway-all-servers]');
    allServers.checked = Boolean(c.all_instances);
    const instanceOptions = form.querySelector('[data-gateway-instance-options]');
    const known = new Map(instances.map(instance => [instance.id, instance.display_name || instance.name || 'Minecraft server']));
    for (const id of c.instance_ids || []) if (!known.has(id)) known.set(id, 'Unavailable server');
    for (const [id, name] of known) {
      const label = text('label', ''); label.className = 'gateway-server-item';
      const checkbox = document.createElement('input'); checkbox.type = 'checkbox'; checkbox.name = 'instance_ids'; checkbox.value = id; checkbox.checked = (c.instance_ids || []).includes(id);
      checkbox.className = 'date-time-switch'; checkbox.setAttribute('role', 'switch');
      const icon = document.createElement('img'); icon.className = 'minecraft-server-software-icon'; icon.alt = '';
      icon.src = instances.find(instance => instance.id === id)?.software_icon || '/static/justvoxel-logo.png';
      label.append(checkbox, icon, text('span', name));
      instanceOptions.append(label);
    }
    if (!known.size) instanceOptions.append(text('p', 'No Minecraft servers are available to choose.'));
    const sync = () => {
      const all = allServers.checked;
      field('server_scope').value = all ? 'all' : 'selected';
      form.querySelector('[data-gateway-scope]').hidden = false;
      instanceOptions.querySelectorAll('input[name="instance_ids"]').forEach(input => { input.disabled = all; });
    };
    allServers.addEventListener('change', sync); sync();
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
    form.className = c.id ? 'gateway-edit-settings' : 'gateway-add-settings';
    for (const name of ['name', 'level']) field(name).value = c[name] ?? '';
    // Keep custom purposes from deployed connections without interpreting them as markup or asset paths.
    if (c.purpose && !providers.has(c.purpose)) {
      const label = text('label', ''); label.className = 'gateway-provider-card';
      const input = document.createElement('input'); input.type = 'radio'; input.name = 'purpose'; input.value = c.purpose;
      label.append(input, providerIcon(c.purpose), text('strong', purposeLabel(c.purpose)));
      form.querySelector('[data-gateway-providers]').append(label);
    }
    field('purpose').value = c.purpose || 'OpenAI';
    const compatibility = form.querySelector('[data-gateway-compatibility-summary]');
    compatibility.hidden = Boolean(c.id);
    if (!c.id) {
      const previewCompatibility = () => {
        compatibility.textContent = providerCompatibility.get(field('purpose').value) || providerCompatibility.get('Other AI / MCP app');
      };
      form.querySelectorAll('input[name="purpose"]').forEach(input => input.addEventListener('change', previewCompatibility));
      previewCompatibility();
    }
    if (c.id) {
      form.querySelector('[data-gateway-purpose-label]').textContent = 'Provider';
      form.querySelector('[data-gateway-level-label]').textContent = 'Access level';
    }
    form.querySelector('[data-gateway-save]').textContent = c.id ? 'Apply' : 'Create';
    form.querySelector('[data-gateway-advanced]').hidden = !c.id;
    for (const selector of ['[data-gateway-delete]', '[data-gateway-live-note]']) form.querySelector(selector).hidden = !c.id;
    field('enabled').checked = Boolean(c.enabled);
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
    renderMinecraftScope(form, c);
    form.querySelector('[data-gateway-actions]').hidden = !c.id || c.auth_mode !== 'token';
    for (const action of ['rotate', 'revoke']) form.querySelector(`[data-gateway-${action}]`).hidden = !c.id || c.auth_mode !== 'token';
    if (c.id) {
      const onboarding = text('section', ''); onboarding.className = 'gateway-guide gateway-settings-guide';
      onboarding.setAttribute('aria-live', 'polite');
      form.querySelector('[data-gateway-status]').after(onboarding);
      const previewProvider = () => {
        const purpose = field('purpose').value;
        const automation = purpose === 'Automation / REST API';
        const address = location.origin + (automation ? '/api/v1' : localOAuth ? '/mcp/' + encodeURIComponent(c.id) : '/mcp');
        renderProviderGuide(onboarding, purpose, address, {settings: true});
      };
      form.querySelectorAll('input[name="purpose"]').forEach(input => input.addEventListener('change', previewProvider));
      previewProvider();
    }
    const status = form.querySelector('[data-gateway-status-details]');
    form.querySelector('[data-gateway-status]').hidden = !c.id;
    status.className = 'minecraft-server-metrics gateway-status-metrics';
    const expiration = text('div', '');
    if (c.id) {
      expiration.append(text('span', 'EXPIRATION'), field('expires_at')); status.append(expiration);
    }
    const safeDetails = form.querySelector('[data-gateway-safe-details]');
    if (c.id) {
      for (const [name, value] of [['Connection ID', c.id], ['Safe credential fingerprint', c.fingerprint || 'None'], ['Created', settingsDate(c.created_at)], ['Updated', settingsDate(c.updated_at)]]) {
        safeDetails.append(text('dt', name), text('dd', value || 'Unavailable'));
      }
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
      if (c.id && field('expires_at').value) updated.expires_at = new Date(field('expires_at').value).toISOString();
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
      await copyText(field.value, field, status, button);
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
