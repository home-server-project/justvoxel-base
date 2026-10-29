(() => {
  'use strict';

  const initialized = new WeakSet();
  globalThis.justVoxelMinecraftServers = (root = document) => {
  const list = root.querySelector('.minecraft-server-entries');
  const arrange = root.querySelector('[data-server-arrange]');
  const done = root.querySelector('[data-server-arrange-done]');
  if (!list || !arrange || !done || initialized.has(list)) return;
  initialized.add(list);

  const key = 'justvoxel:minecraft-server-order:v1';
  const idPattern = /^jv-[a-z0-9]{6,12}$/;
  const cards = () => Array.from(list.querySelectorAll('[data-minecraft-server-card]'));
  const rendered = cards();
  const byID = new Map(rendered.map(card => [card.dataset.serverId, card]));
  let saved;
  try {
    saved = JSON.parse(window.localStorage.getItem(key));
  } catch (_) {
    // Browser storage is optional. Preserve the server-rendered order.
  }
  if (Array.isArray(saved)) {
    const seen = new Set();
    const ordered = [];
    for (const id of saved) {
      if (typeof id !== 'string' || !idPattern.test(id) || seen.has(id) || !byID.has(id)) continue;
      seen.add(id);
      ordered.push(byID.get(id));
    }
    for (const card of rendered) {
      if (!seen.has(card.dataset.serverId)) ordered.push(card);
    }
    // Ordering contains only real server cards, identified by immutable ID.
    for (const card of ordered) list.appendChild(card);
  }

  let arranging = false;
  const updateBounds = () => {
    const current = cards();
    current.forEach((card, index) => {
      card.querySelector('[data-server-move-earlier]').disabled = index === 0;
      card.querySelector('[data-server-move-later]').disabled = index === current.length - 1;
    });
  };
  const setArranging = active => {
    arranging = active;
    arrange.hidden = active;
    done.hidden = !active;
    for (const card of cards()) {
      for (const normal of card.querySelectorAll('[data-server-normal-actions]')) {
        normal.hidden = active;
        normal.inert = active;
      }
      card.querySelector('[data-server-arrange-actions]').hidden = !active;
      if (active) card.querySelector('details').open = false;
    }
    updateBounds();
    (active ? done : arrange).focus();
  };
  const persist = () => {
    try {
      window.localStorage.setItem(key, JSON.stringify(cards().map(card => card.dataset.serverId).filter(id => idPattern.test(id))));
    } catch (_) {
      // Keep this page's presentation usable when storage is unavailable.
    }
  };
  arrange.hidden = rendered.length === 0;
  arrange.addEventListener('click', () => setArranging(true));
  done.addEventListener('click', () => setArranging(false));
  for (const card of rendered) {
    for (const direction of ['earlier', 'later']) {
      const button = card.querySelector(`[data-server-move-${direction}]`);
      button.addEventListener('click', () => {
        if (!arranging || button.disabled) return;
        const current = cards();
        const index = current.indexOf(card);
        const neighbor = current[index + (direction === 'earlier' ? -1 : 1)];
        if (!neighbor) return;
        if (direction === 'earlier') list.insertBefore(card, neighbor);
        else list.insertBefore(neighbor, card);
        updateBounds();
        persist();
        // Moving to a boundary disables this button; keep keyboard focus usable.
        (button.disabled ? card.querySelector(`[data-server-move-${direction === 'earlier' ? 'later' : 'earlier'}]`) : button).focus();
      });
    }
    const suppress = event => {
      if (!arranging) return;
      event.preventDefault();
      event.stopImmediatePropagation();
    };
    for (const normal of card.querySelectorAll('[data-server-normal-actions]')) {
      normal.addEventListener('click', suppress, true);
      normal.addEventListener('submit', suppress, true);
    }
    initializeCard(card, root, () => arranging);
  }
  updateBounds();
  initializeLiveMemory(list, cards);
  };
  function initializeLiveMemory(list, cards) {
    const history = new Map();
    const validBytes = value => typeof value === 'number' && Number.isFinite(value) && value >= 0;
    const format = bytes => bytes >= 1024 ** 3
      ? `${(bytes / 1024 ** 3).toFixed(2)} GB`
      : `${Math.round(bytes / 1024 ** 2)} MB`;
    const update = rows => {
      const byID = new Map(rows.filter(row => row && /^jv-[a-z0-9]{6,12}$/.test(row.id)).map(row => [row.id, row]));
      const now = Date.now();
      for (const card of cards()) {
        const row = byID.get(card.dataset.serverId);
        const tile = card.querySelector('[data-server-ram]');
        if (row && typeof row.state === 'string' && typeof row.state_class === 'string'
            && typeof row.running === 'boolean' && typeof row.can_switch === 'boolean') {
          if (['neutral', 'success', 'warning', 'error'].includes(row.card_health)) {
            for (const health of ['neutral', 'success', 'warning', 'error']) card.classList.toggle(`minecraft-server-health-${health}`, health === row.card_health);
          }
          const status = card.querySelector('.minecraft-server-state');
          if (status) {
            status.textContent = row.state;
            status.className = `minecraft-server-state ${row.state_class}`;
          }
          const running = card.querySelector('[data-server-running]');
          if (running) {
            running.checked = row.running;
            running.disabled = !row.can_switch;
          }
          if (tile) tile.dataset.serverState = row.state;
        }
        if (!tile) continue;
        const value = tile.querySelector('[data-server-ram-value]');
        const graph = tile.querySelector('[data-server-ram-graph]');
        const id = card.dataset.serverId;
        graph.replaceChildren();
        // Only authoritative lifecycle state can declare a stop.
        if (tile.dataset.serverState === 'Stopped') {
          value.textContent = '---';
          history.delete(id);
          continue;
        }
        if (!row || !validBytes(row.usage_bytes)) {
          value.textContent = 'Unavailable';
          history.delete(id);
          continue;
        }
        value.textContent = format(row.usage_bytes);
        const samples = (history.get(id) || []).filter(sample => now - sample.at < 60000);
        samples.push({usage: row.usage_bytes, at: now});
        if (samples.length > 30) samples.splice(0, samples.length - 30);
        history.set(id, samples);
        if (!validBytes(row.limit_bytes) || row.limit_bytes <= 0 || samples.length < 2) continue;
        const line = document.createElementNS('http://www.w3.org/2000/svg', 'polyline');
        line.setAttribute('points', samples.map((sample, index) => {
          const x = (30 - samples.length + index) * 4;
          const y = 12 * (1 - Math.min(sample.usage, row.limit_bytes) / row.limit_bytes);
          return `${x},${y}`;
        }).join(' '));
        graph.appendChild(line);
      }
    };
    let pending = false;
    const poll = async () => {
      if (!list.isConnected) { clearInterval(timer); return; }
      if (pending) return;
      pending = true;
      try {
        const response = await fetch('/api/minecraft/servers/live-memory', {
          credentials: 'same-origin', cache: 'no-store', headers: {Accept: 'application/json'},
        });
        if (!response.ok || response.redirected) throw new Error('Live RAM unavailable');
        const rows = await response.json();
        if (!Array.isArray(rows)) throw new Error('Invalid live RAM response');
        if (list.isConnected) update(rows);
      } catch (_) {
        if (list.isConnected) update([]);
      } finally {
        pending = false;
        if (!list.isConnected) clearInterval(timer);
      }
    };
    const timer = setInterval(poll, 2000);
    poll();
  }
  function initializeCard(card, root, isArranging) {
    const id = card.dataset.serverId;
    const inventory = root.querySelector('[data-server-csrf]');
    if (!inventory || !/^jv-[a-z0-9]{6,12}$/.test(id)) return;
    const csrf = inventory.dataset.serverCsrf;
    const selected = path => path + (path.includes('?') ? '&' : '?') + new URLSearchParams({server: id});
    const settingsURL = selected('/?workspace=minecraft');
    const connectURL = selected('/minecraft/how-to-connect');
    const feedback = (message, connect = false, review = false) => {
      const node = root.querySelector('[data-server-feedback]');
      if (!node) return;
      node.textContent = message;
      node.hidden = !message;
      if (connect || review) {
        const link = document.createElement('a');
        link.href = connect ? connectURL : settingsURL;
        link.textContent = connect ? ' How to connect' : ' Open Minecraft Settings';
        node.appendChild(link);
      }
    };
    const request = async (path, options = {}) => {
      const response = await fetch(selected(path), {credentials: 'same-origin', cache: 'no-store', ...options, headers: {Accept: 'application/json'}});
      let payload;
      try { payload = await response.json(); } catch (_) { throw new Error('Minecraft operation could not be completed. Refresh Minecraft Servers.'); }
      if (!response.ok || response.redirected) throw new Error(payload.error || 'Minecraft operation could not be completed.');
      return payload;
    };
    const refresh = async () => {
      if (root.justVoxelRefreshServers) await root.justVoxelRefreshServers();
    };
    let pending = false;
    const controls = Array.from(card.querySelectorAll('input, button'));
    const originalDisabled = new Map(controls.map(control => [control, control.disabled]));
    const operation = async (action, {connect = false} = {}) => {
      if (pending || isArranging()) return;
      pending = true;
      controls.forEach(control => { control.disabled = true; });
      feedback('');
      try { await action(); }
      catch (error) { feedback(error.message, connect); }
      finally {
        pending = false;
        controls.forEach(control => { control.disabled = originalDisabled.get(control); });
      }
    };
    const mutate = async action => {
      let failure;
      try { await action(); } catch (error) { failure = error; }
      // Even a refused/failed request may have changed state. Reload authoritative cards.
      await refresh();
      if (failure) throw failure;
    };
    const running = card.querySelector('[data-server-running]');
    running?.addEventListener('change', () => {
      const requested = running.checked;
      running.checked = !requested;
      operation(() => mutate(async () => {
        const status = card.querySelector('.minecraft-server-state');
        if (status) {
          status.textContent = requested ? 'Starting' : 'Stopping';
          status.className = 'minecraft-server-state warning';
        }
        const result = await request('/api/minecraft/servers/lifecycle', {method: 'POST', body: new URLSearchParams({csrf, action: requested ? 'start' : 'stop'})});
        if (!result.ok) throw new Error('Server action was not completed.');
      }));
    });
    const requiresReview = plan => Object.entries(plan).some(([key, value]) => key.endsWith('_required') && value === true);
    const boot = card.querySelector('[data-server-boot]');
    boot?.addEventListener('change', () => {
      const requested = boot.checked;
      boot.checked = !requested;
      operation(async () => {
        let review = false;
        await mutate(async () => {
          const body = new URLSearchParams({csrf, tab: 'overview', start_on_boot: String(requested)});
          const plan = await request('/api/minecraft/workspace/settings/plan', {method: 'POST', body});
          if (requiresReview(plan)) { review = true; return; }
          if (!plan.review) throw new Error('The settings review is unavailable. Open Minecraft Settings.');
          body.set('review', plan.review);
          const result = await request('/api/minecraft/workspace/settings/apply', {method: 'POST', body});
          if (requiresReview(result) && !result.applied) { review = true; return; }
          if (!result.applied) throw new Error(result.error || 'Minecraft settings were not applied.');
        });
        if (review) feedback('Review this change in Minecraft Settings.', false, true);
      });
    });
    card.querySelector('[data-server-restart]')?.addEventListener('click', () => {
      closeMenu();
      operation(() => mutate(async () => {
        const result = await request('/minecraft/servers/view/' + encodeURIComponent(id) + '/restart', {method: 'POST', body: new URLSearchParams({csrf})});
        if (!result.ok) throw new Error('Restart was not completed.');
      }));
    });
    card.querySelector('[data-server-delete]')?.addEventListener('click', () => {
      if (pending || isArranging()) return;
      closeMenu();
      globalThis.justVoxelOpenDeleteServer({name: card.dataset.serverName,
        description: 'Permanently removes this active server and its world data. Existing backup archives are preserved as historical archives.',
        csrf, request, dismissOnDeleted: true, isCurrent: () => card.isConnected && !isArranging(),
        onError: error => feedback(error.message), onDeleted: () => refresh(),
      });
    });
    card.querySelector('[data-server-invite]')?.addEventListener('click', () => {
      closeMenu();
      operation(async () => {
        const payload = await request('/api/minecraft/servers/invitation');
        if (!card.isConnected || isArranging()) return;
        if (!payload.invitation) throw new Error('No default invitation is available.');
        const dialog = document.createElement('dialog');
        dialog.className = 'minecraft-server-invitation';
        dialog.setAttribute('aria-label', 'Invite friends');
        const title = document.createElement('h2'); title.textContent = 'Invite friends';
        const name = document.createElement('p'); name.textContent = payload.name;
        const label = document.createElement('label'); label.textContent = 'Invitation';
        const text = document.createElement('textarea'); text.readOnly = true; text.rows = 10; text.spellcheck = false; text.value = payload.invitation;
        label.appendChild(text);
        const status = document.createElement('p'); status.setAttribute('role', 'status');
        const actions = document.createElement('div'); actions.className = 'action-row';
        const close = document.createElement('button'); close.type = 'button'; close.className = 'secondary'; close.textContent = 'Close';
        const copy = document.createElement('button'); copy.type = 'button'; copy.textContent = 'Copy invitation';
        close.addEventListener('click', () => dialog.close());
        copy.addEventListener('click', async () => {
          try { await navigator.clipboard.writeText(text.value); status.textContent = 'Copied.'; }
          catch (_) { status.textContent = 'Copy unavailable. Select the invitation text and copy it manually.'; }
        });
        actions.append(close, copy); dialog.append(title, name, label, status, actions);
        dialog.addEventListener('click', event => {
          if (event.target !== dialog) return;
          const rect = dialog.getBoundingClientRect();
          if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) dialog.close();
        });
        dialog.addEventListener('close', () => dialog.remove());
        document.body.appendChild(dialog); dialog.showModal();
      }, {connect: true});
    });
    for (const link of card.querySelectorAll('a[data-server-workspace]')) {
      link.addEventListener('click', event => {
        if (event.defaultPrevented || event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey || isArranging()) return;
        const workspace = link.dataset.serverWorkspace;
        const selector = {minecraft: '[data-minecraft-open]', backups: '[data-backups-open]', version: '[data-version-open]', plugins: '[data-plugins-open]', migration: '[data-migration-open]'}[workspace];
        const launcher = selector && document.querySelector(selector);
        if (!launcher || typeof globalThis.justVoxelSelectServer !== 'function') return;
        event.preventDefault();
        closeMenu();
        if (globalThis.justVoxelSelectServer(id, workspace)) launcher.click();
      });
    }
    const menu = card.querySelector('details');
    const choices = menu?.querySelector('[popover]');
    const trigger = menu?.querySelector('summary');
    function closeMenu() {
      if (choices?.matches(':popover-open')) choices.hidePopover();
      if (menu) menu.open = false;
    }
    if (menu && choices && trigger) {
      menu.addEventListener('toggle', () => {
        if (!menu.isConnected) return;
        if (!menu.open || isArranging()) { closeMenu(); return; }
        choices.showPopover();
        const rect = trigger.getBoundingClientRect();
        const bounds = choices.getBoundingClientRect();
        choices.style.left = Math.max(8, Math.min(rect.right - bounds.width, window.innerWidth - bounds.width - 8)) + 'px';
        choices.style.top = Math.max(8, Math.min(rect.bottom + 4, window.innerHeight - bounds.height - 8)) + 'px';
      });
      choices.addEventListener('toggle', event => { if (event.newState === 'closed') menu.open = false; });
      choices.addEventListener('click', event => { if (event.target.closest('a')) closeMenu(); });
    }
  }
  window.addEventListener?.('resize', () => {
    document.querySelectorAll('.minecraft-server-more[open]').forEach(menu => { menu.open = false; });
  });
  globalThis.justVoxelMinecraftServers();
})();
