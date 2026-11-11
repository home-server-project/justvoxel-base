const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');
const vm = require('node:vm');
const staticDir = path.join(__dirname, '../webui/internal/server/static');
const source = readFileSync(path.join(staticDir, 'network-workspace.js'), 'utf8');

test('Remote Access titles use only allowlisted decorative icons and literal provider text', () => {
  // Exercise the actual mapping and title construction without running network operations.
  const mapping = source.slice(source.indexOf('  const providerIcons ='), source.indexOf('  const renderRemoteAccess ='));
  const titleCode = source.slice(source.indexOf('      const title = document.createElement("h3");', source.indexOf('  const renderRemoteAccess =')), source.indexOf('      card.append(title,'));
  assert.ok(mapping.length > 0 && titleCode.length > 0);
  assert.match(titleCode, /title\.textContent = provider\.display_name;/);
  assert.doesNotMatch(mapping + titleCode, /innerHTML|insertAdjacentHTML/);
  for (const id of ['tailscale', 'netbird', 'playit', 'nordvpn', 'unknown', '../tailscale', '__proto__', 'constructor', 'https://example.test/icon.svg']) {
    const created = [];
    const document = { createElement(tag) {
      const element = { tag, children: [], setAttribute(key, value) { this[key] = value; }, prepend(child) { this.children.unshift(child); } };
      Object.defineProperty(element, 'innerHTML', { set() { throw new Error('unsafe HTML insertion'); } });
      created.push(element);
      return element;
    } };
    const provider = { id, display_name: '<img src=x onerror=alert(1)>' };
    vm.runInNewContext(mapping + titleCode, { document, provider });
    const title = created[0];
    assert.equal(title.tag, 'h3');
    assert.equal(title.textContent, provider.display_name);
    if (['tailscale', 'netbird', 'playit', 'nordvpn'].includes(id)) {
      assert.equal(created.length, 2);
      const icon = title.children[0];
      assert.equal(icon.tag, 'img');
      assert.equal(icon.src, `/static/provider-icons/${id}.svg`);
      assert.equal(icon.alt, '');
      assert.equal(icon['aria-hidden'], 'true');
    } else {
      assert.equal(created.length, 1);
      assert.equal(title.children.length, 0);
    }
  }
});
