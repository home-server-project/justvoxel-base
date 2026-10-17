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
    // Only real cards move; the existing Add Server link stays first.
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
      const normal = card.querySelector('[data-server-normal-actions]');
      normal.hidden = active;
      normal.inert = active;
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
    const normal = card.querySelector('[data-server-normal-actions]');
    const suppress = event => {
      if (!arranging) return;
      event.preventDefault();
      event.stopImmediatePropagation();
    };
    normal.addEventListener('click', suppress, true);
    normal.addEventListener('submit', suppress, true);
  }
  updateBounds();
  };
  globalThis.justVoxelMinecraftServers();
})();
