(() => {
  const root = document.querySelector('[data-plus-setup]');
  if (!root) return;
  const form = root.querySelector('[data-setup-form]');
  const field = name => form.elements.namedItem(name);
  const checked = name => field(name).checked;
  const notice = root.querySelector('[data-setup-notice]');
  const next = root.querySelector('[data-setup-next]');
  const back = root.querySelector('[data-setup-back]');
  const save = root.querySelector('[data-setup-save]');
  const names = ['panel', 'database', 'cache', 'wings'];
  let step = 0;
  let loaded = false;
  let deployed = false;
  let busy = false;
  let running = false;
  let locked = false;
  let pollTimer;
  const progress = root.querySelector('[data-setup-progress]');
  const retry = root.querySelector('[data-deployment-retry]');
  field('host').value = window.location.hostname.replace(/^\[|\]$/g, '');

  function sync() {
    const partial = names.some(name => !checked(name));
    root.querySelector('[data-partial-warning]').hidden = !partial;
    field('stack').checked = names.every(checked);
    field('stack').setAttribute('aria-checked', String(field('stack').checked));
    root.querySelector('[data-host-label]').textContent = checked('use_domain') ? 'Your domain name' : 'Host IP address or hostname';
    root.querySelector('[data-tls-fields]').hidden = !checked('use_tls');
    root.querySelector('[data-http-warning]').hidden = checked('use_tls');
    field('certificate').required = checked('use_tls');
    field('private_key').required = checked('use_tls');
    for (const name of ['certificate', 'private_key']) field(name).disabled = !checked('use_tls');
    root.querySelector('[data-storage-fields]').hidden = !checked('external_storage');
    root.querySelector('[data-system-storage]').hidden = checked('external_storage');
    field('storage_mount').required = checked('external_storage');
    field('storage_mount').disabled = !checked('external_storage');
    const account = checked('panel') || checked('drydock');
    root.querySelector('[data-account-fields]').hidden = !account;
    root.querySelector('[data-no-account]').hidden = account;
    root.querySelector('[data-email-field]').hidden = !checked('panel');
    field('username').readOnly = !checked('separate_account');
    if (!checked('separate_account')) field('username').value = root.dataset.username;
    for (const name of ['username', 'password', 'confirm_password']) { field(name).required = account; field(name).disabled = !account; }
    field('email').required = checked('panel');
    field('email').disabled = !checked('panel');
    root.querySelectorAll('[data-setup-step]').forEach((section, index) => { section.hidden = index !== step; });
    root.querySelectorAll('.plus-steps span').forEach((item, index) => {
      if (index === step) item.setAttribute('aria-current', 'step'); else item.removeAttribute('aria-current');
    });
    form.hidden = running || locked || deployed;
    back.disabled = step === 0 || busy;
    next.hidden = step === 4;
    save.hidden = step !== 4;
    next.disabled = save.disabled = !loaded || deployed || running || locked || busy;
    if (step === 4) review();
  }

  function request() {
    return {
      components: Object.fromEntries([...names, 'drydock'].map(name => [name, checked(name)])),
      use_domain: checked('use_domain'), host: field('host').value.trim(), use_tls: checked('use_tls'),
      certificate: checked('use_tls') ? field('certificate').value : '',
      private_key: checked('use_tls') ? field('private_key').value : '',
      storage_mount: checked('external_storage') ? field('storage_mount').value : '',
      separate_account: checked('separate_account'), username: field('username').value,
      email: checked('panel') ? field('email').value : '',
      password: checked('panel') || checked('drydock') ? field('password').value : ''
    };
  }

  function review() {
    const data = request();
    const target = root.querySelector('[data-setup-review]');
    target.replaceChildren();
    const entries = [
      ['Components', Object.entries(data.components).filter(([, on]) => on).map(([name]) => ({database: 'MariaDB', cache: 'Redis', panel: 'Panel', wings: 'Wings', drydock: 'Drydock'}[name])).join(', ')],
      ['Address', `${data.use_tls ? 'HTTPS' : 'HTTP'} · ${data.host}`],
      ['Storage', data.storage_mount ? `${data.storage_mount}/justvoxel-plus` : '/var/lib/justvoxel-plus'],
      ['Application user', data.components.panel || data.components.drydock ? data.username : 'Not needed'],
      ['Accounts', 'Independent from the host account']
    ];
    for (const [label, value] of entries) {
      const dt = document.createElement('dt'); dt.textContent = label;
      const dd = document.createElement('dd'); dd.textContent = value;
      target.append(dt, dd);
    }
  }

  function validateStep() {
    if (step === 0 && ![...names, 'drydock'].some(checked)) {
      notice.textContent = 'Enable at least one component, or choose Look around.'; return false;
    }
    if (step === 3 && (checked('panel') || checked('drydock')) && field('password').value !== field('confirm_password').value) {
      notice.textContent = 'The passwords must match.'; return false;
    }
    const section = root.querySelector(`[data-setup-step="${step}"]`);
    for (const input of section.querySelectorAll('input, select, textarea')) {
      if (input.required && !input.reportValidity()) return false;
    }
    notice.textContent = ''; return true;
  }

  form.addEventListener('change', event => {
    if (event.target === field('stack')) for (const name of names) field(name).checked = checked('stack');
    sync();
  });
  next.addEventListener('click', () => { if (validateStep()) { step++; sync(); } });
  back.addEventListener('click', () => { step--; notice.textContent = ''; sync(); });
  for (const [selector, name] of [['[data-certificate-file]', 'certificate'], ['[data-key-file]', 'private_key']]) {
    root.querySelector(selector).addEventListener('change', async event => {
      const file = event.target.files[0];
      if (!file) return;
      if (file.size > 48 * 1024) { notice.textContent = 'Each PEM file must be smaller than 48 KB.'; event.target.value = ''; return; }
      try { field(name).value = await file.text(); } catch { notice.textContent = 'The PEM file could not be read.'; }
    });
  }
  function deploymentState(state) {
    deployed = Boolean(state.deployed);
    running = Boolean(state.running);
    locked = Boolean(state.locked);
    const deployment = state.deployment || {};
    progress.hidden = !(deployment.started || deployed || running);
    root.querySelector('[data-deployment-message]').textContent = deployment.message || (deployed ? 'Setup complete.' : 'Starting deployment…');
    root.querySelector('[data-deployment-panel]').hidden = !deployed || !deployment.panel_url;
    root.querySelector('[data-deployment-drydock]').hidden = !deployed || !deployment.drydock_url;
    retry.hidden = deployed || running || !state.prepared;
    retry.disabled = busy;
    sync();
    if (pollTimer) clearTimeout(pollTimer);
    if (running) pollTimer = setTimeout(pollDeployment, 3000);
  }

  async function pollDeployment() {
    try {
      const response = await fetch('/api/plus/setup/state', {credentials: 'same-origin', cache: 'no-store'});
      const state = await response.json();
      if (!response.ok) throw new Error('Progress is temporarily unavailable.');
      deploymentState(state);
    } catch {
      root.querySelector('[data-deployment-message]').textContent = 'Progress is temporarily unavailable. Deployment continues on the host.';
      pollTimer = setTimeout(pollDeployment, 5000);
    }
  }

  async function startDeployment() {
    const body = new URLSearchParams({csrf: field('csrf').value});
    const response = await fetch('/api/plus/setup/deploy', {method: 'POST', credentials: 'same-origin', body});
    const result = await response.json();
    if (!response.ok || !result.ok) throw new Error(result.error || 'Deployment could not be started.');
    notice.textContent = '';
    deploymentState({running: true, prepared: true, deployment: result.deployment});
  }

  retry.addEventListener('click', async () => {
    if (busy || running || deployed) return;
    busy = true; retry.disabled = true;
    try { await startDeployment(); }
    catch (error) { root.querySelector('[data-deployment-message]').textContent = error.message || 'Deployment could not be started.'; }
    finally { busy = false; retry.disabled = false; sync(); }
  });

  form.addEventListener('submit', async event => {
    event.preventDefault();
    if (!loaded || deployed || running || locked || busy || step !== 4) return;
    busy = true; sync(); notice.textContent = 'Preparing deployment…';
    let prepared = false;
    try {
      const body = new URLSearchParams({csrf: field('csrf').value, setup: JSON.stringify(request())});
      const response = await fetch('/api/plus/setup/prepare', {method: 'POST', credentials: 'same-origin', body});
      const result = await response.json();
      if (!response.ok || !result.ok) throw new Error(result.error || 'Setup could not be saved.');
      prepared = true;
      for (const name of ['password', 'confirm_password', 'private_key', 'certificate']) field(name).value = '';
      root.querySelectorAll('input[type="file"]').forEach(input => { input.value = ''; });
      await startDeployment();
    } catch (error) {
      notice.textContent = error.message || 'Setup could not be started.';
      if (prepared) {
        progress.hidden = false;
        retry.hidden = false;
        root.querySelector('[data-deployment-message]').textContent = 'Choices are saved. Retry deployment when the setup service is available.';
      }
    } finally { busy = false; retry.disabled = false; sync(); }
  });
  sync();
  fetch('/api/plus/setup/state', {credentials: 'same-origin', cache: 'no-store'})
    .then(async response => {
      const state = await response.json();
      if (!response.ok) throw new Error(state.error || 'Setup information is unavailable.');
      deployed = Boolean(state.deployed);
      for (const mount of state.mounts || []) {
        const option = document.createElement('option'); option.value = mount.target;
        option.textContent = `${mount.target} (${mount.fstype})`; field('storage_mount').append(option);
      }
      field('external_storage').disabled = !(state.mounts || []).length;
      loaded = true;
      notice.textContent = deployed ? 'This appliance is already deployed.' : state.prepared ? 'Saved choices are ready. Enter new choices to replace them before deployment.' : 'Choose your components, then continue.';
      deploymentState(state);
    }).catch(() => { notice.textContent = 'Setup information is unavailable. Return to the desktop and try again.'; });
})();
