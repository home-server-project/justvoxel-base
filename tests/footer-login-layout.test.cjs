const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');
const vm = require('node:vm');
const read = file => readFileSync(path.join(__dirname, '../webui/internal/server', file), 'utf8');
const css = read('static/app.css');
const setupCSS = read('static/setup.css');

// Source layout contracts, not a substitute for browser/VM visual acceptance.
// Read declarations from production selectors, including shared rules, rather
// than duplicating the stylesheet in a fixture.
function rules(source) {
  return [...source.replace(/\/\*[\s\S]*?\*\//g, '').matchAll(/([^{}]+)\{([^{}]*)\}/g)]
    .map(([, selector, body]) => ({
      selector: selector.trim(),
      properties: Object.fromEntries(body.split(';').filter(value => value.trim()).map(value => {
        const colon = value.indexOf(':');
        return [value.slice(0, colon).trim(), value.slice(colon + 1).trim()];
      })),
    }));
}
function rule(source, selector) {
  const matches = rules(source).filter(item => item.selector === selector);
  assert.equal(matches.length, 1, `expected one production rule for ${selector}`);
  return matches[0].properties;
}

test('setup footers use the desktop width and gutter independently of wizard panels', () => {
  const shared = rule(css, '#dashboard,.setup-body>.project-footer');
  const footer = rule(setupCSS, '.setup-body>.project-footer');
  const shell = rules(setupCSS).filter(item => item.selector === '.setup-shell').at(-1).properties;
  assert.equal(shared['max-width'], '1320px');
  assert.equal(shared['padding-inline'], '1.25rem');
  assert.equal(shared['padding-bottom'], '1rem');
  assert.equal(footer.width, '100%');
  assert.equal(footer['margin-inline'], 'auto');
  assert.equal(footer['text-align'], 'left');
  assert.equal(footer['line-height'], '1.5', 'retain existing setup footer line spacing');
  assert.equal(shell.width, 'min(1180px,100%)', 'wizard panel width stays unchanged');
  assert.equal(shell['padding-bottom'], '0', 'bottom spacing belongs to the page footer');
  assert.equal(rule(css, '.project-footer')['margin-top'], 'auto');
  assert.equal(rule(css, '.project-links').gap, '.15rem .45rem');
  for (const item of rules(setupCSS)) {
    if (!item.selector.includes('.project-footer')) continue;
    assert.ok(!item.selector.includes('.setup-shell'), 'footer must not depend on wizard shell geometry');
    assert.ok(!['absolute', 'fixed'].includes(item.properties.position), 'footer stays in natural flow');
    assert.notEqual(item.properties['text-align'], 'center');
    assert.notEqual(item.properties['justify-content'], 'center');
  }
  for (const template of ['setup_wizard.html', 'setup_review.html', 'setup_progress.html']) {
    const markup = read(`templates/${template}`);
    assert.match(markup, /<main class="setup-shell">/);
    assert.match(markup, /<\/main>\s*\{\{template "project-footer" \.\}\}/);
  }
  // Both containers shrink to the viewport; no viewport offset or panel width
  // enters the horizontal calculation, including on narrow screens.
  for (const viewport of [1920, 1440, 1024, 390]) {
    const width = Math.min(viewport, parseFloat(shared['max-width']));
    const left = (viewport - width) / 2 + parseFloat(shared['padding-inline']) * 16;
    assert.ok(left >= 0 && left < viewport / 2);
    assert.ok(width - 2 * parseFloat(shared['padding-inline']) * 16 > 0);
  }
});

test('login submit reuses Control Center action dimensions and aligns right without changing fields', () => {
  const selector = '.login-panel>form[action="/login"]>button[type=submit]';
  const shared = rule(css, `.control-power-actions button,.control-session .nav-logout button,${selector}`);
  assert.deepEqual(shared, { 'min-height': '34px', padding: '.45rem .65rem', 'font-size': '.8rem' });
  assert.deepEqual(rule(css, selector), { display: 'block', 'margin-left': 'auto' });
  const base = rule(css, 'button,.button-link');
  assert.equal(base['border-radius'], '8px', 'both buttons retain the standard radius');
  assert.equal(base.font, 'inherit', 'both buttons retain the inherited font and line height');
  const size = rules(css).find(item => item.properties['font-size'] === '.84rem!important');
  assert.ok(size.selector.startsWith(':is(button,.button-link)'), 'both buttons use the same final font sizing');
  assert.ok(!size.selector.includes('login-panel'), 'login has no separate font-size override');
  const login = read('templates/login.html');
  assert.match(login, /<form method="post" action="\/login"><label>Username<input name="username" value="voxel" autocomplete="username" required><\/label><label>Password<input type="password" name="password" autocomplete="current-password" required autofocus><\/label><button type="submit">Sign in<\/button><\/form>/);
  assert.match(read('templates/header.html'), /class="control-power-actions[^"\n]*"/);
});

test('Wallpaper inherits link typography and shares color, underline and vertical alignment', () => {
  const wallpaper = rule(css, '.project-wallpaper-button');
  assert.equal(wallpaper.font, 'inherit', 'inherit family, size, weight and line height from the footer');
  assert.equal(wallpaper.padding, '0');
  assert.equal(wallpaper.border, '0');
  assert.equal(wallpaper.background, 'transparent');
  assert.equal(rule(css, '.project-footer')['font-size'], '.72rem');
  const globalSize = rules(css).find(item => item.properties['font-size'] === '.84rem!important');
  assert.match(globalSize.selector, /:not\([^)]*\.project-wallpaper-button\)/, 'global important button sizing must exclude Wallpaper');
  assert.equal(rule(css, '.project-links')['align-items'], 'center');
  const linkRules = rules(css).filter(item => item.selector === '.project-links :is(a,.project-wallpaper-button)');
  const links = linkRules[0].properties;
  assert.equal(links.color, '#9ca7b0');
  assert.equal(links['text-decoration'], 'underline');
  assert.equal(links['text-underline-offset'], '2px');
  assert.equal(linkRules.at(-1).properties.color, '#52616b', 'light theme also shares link color');
  assert.equal(rule(css, '#dashboard[data-configured="true"] .project-wallpaper-button').display, 'inline');
});

test('the real Wallpaper button still opens the wallpaper tab through its production handler', () => {
  const footer = read('templates/project_footer.html');
  assert.match(footer, /<button class="project-wallpaper-button" type="button" data-wallpaper-open>Wallpaper<\/button>/);
  class Control {
    constructor() { this.listeners = {}; }
    addEventListener(name, handler) { this.listeners[name] = handler; }
  }
  const open = new Control();
  const form = new Control();
  const controls = new Map();
  form.querySelectorAll = () => [];
  form.querySelector = selector => {
    if (!controls.has(selector)) controls.set(selector, new Control());
    return controls.get(selector);
  };
  const wallpaper = new Control();
  wallpaper.classList = { toggle() {} };
  wallpaper.getAttribute = () => '/static/wallpapers/jv-wp-v2-day.webp';
  const elements = {
    '[data-wallpaper-open]': open,
    '[data-wallpaper-panel]': { querySelector: () => form },
    '[data-dashboard-wallpaper]': wallpaper,
  };
  const tabs = [];
  const context = vm.createContext({
    document: { querySelector: selector => elements[selector], addEventListener() {} },
    window: { setTimeout: () => 1, clearTimeout() {}, addEventListener() {}, JustVoxelSystem: { openTab: tab => tabs.push(tab) } },
    localStorage: { getItem: () => null },
    URL, Date,
  });
  vm.runInContext(read('static/wallpaper.js'), context);
  assert.equal(typeof open.listeners.click, 'function');
  open.listeners.click();
  assert.deepEqual(tabs, ['wallpaper']);
  assert.equal(typeof context.window.JustVoxelWallpaper.prepare, 'function');
  assert.equal(context.window.JustVoxelWallpaper.canLeave(), true);
});
