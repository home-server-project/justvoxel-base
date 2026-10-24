const assert = require('node:assert/strict');
const { readFileSync, readdirSync } = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');
const root = path.join(__dirname, '../webui/internal/server');
const read = name => readFileSync(path.join(root, name), 'utf8');

test('Add Server reuses the maintained wizard, software controls and six-step presentation', () => {
  const wizard = read('templates/setup_wizard.html');
  const review = read('templates/setup_review.html');
  for (const control of ['server-software-card', 'data-memory-settings', 'setup-backup-topline', 'data-setup-connections']) assert.ok(wizard.includes(control));
  assert.ok(wizard.includes('action="{{.BasePath}}/simple"'));
  assert.ok(wizard.includes('{{template "setup-instance-name" .}}'));
  assert.ok(wizard.includes('name="display_name"'));
  assert.ok(wizard.includes('name="motd"'));
  for (const [index, name] of ['Server', 'Cross-play', 'Memory', 'Storage', 'Backups', 'Review'].entries()) assert.ok(review.includes(`<span class="setup-number">${index + 1}</span><span>${name}</span>`));
  const designs = readdirSync(path.join(root, 'templates')).filter(name => name.endsWith('.html') && read('templates/' + name).includes('data-setup-connections'));
  assert.deepEqual(designs, ['setup_wizard.html']);
});

test('Add Server is a keyboard accessible native action outside the ordered cards', () => {
  const markup = read('templates/minecraft_servers.html');
  assert.ok(markup.includes('<a href="{{if .Configured}}/minecraft/servers/add?new=1{{else}}/setup{{end}}"{{if .Configured}} target="justvoxel-add-server"{{end}}>Add Server</a>'));
  assert.ok(!markup.includes('minecraft-add-server-card'));
  assert.ok(markup.indexOf('>Add Server</a>') < markup.indexOf('minecraft-server-entries'));
  assert.ok(read('static/minecraft-servers.css').includes(':focus-visible'));
});

test('How to Connect remains a single implementation with no Add Server connection alternative', () => {
  for (const [folder, expected] of [
    ['templates', ['how_to_connect.html']],
    ['static', ['how-to-connect.css', 'how-to-connect.js']],
  ]) {
    const names = readdirSync(path.join(root, folder)).filter(name => /how[-_]to[-_]connect/.test(name)).sort();
    assert.deepEqual(names, expected);
  }
  assert.ok(read('how_to_connect.go').includes('how_to_connect.html'));
  assert.ok(!read('templates/minecraft_servers.html').includes('connect-method'));
  assert.ok(!read('templates/setup_wizard.html').includes('connect-method'));
});

test('Add Server Review retains the compact stretched grid without height sizing', () => {
  const css = read('static/setup.css');
  const rules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)]
    .filter(([, selector]) => selector.includes('.setup-review-summary-add-server'));
  assert.ok(css.includes('grid-template-areas:"server cross-play" "server startup" "minecraft storage";align-items:stretch'));
  assert.ok(!css.includes('"minecraft startup"'));
  assert.ok(!css.includes('"storage storage"'));
  for (const [, , declarations] of rules) {
    assert.doesNotMatch(declarations, /(?:^|;)(?:min-|max-)?height\s*:|grid-auto-rows\s*:|grid-template-rows\s*:/);
  }
  assert.ok(css.includes('@media(max-width:700px){\n  .setup-review-summary.setup-review-summary-add-server{grid-template-areas:"server" "cross-play" "startup" "minecraft" "storage"}'));
  assert.ok(css.includes('.setup-body.setup-add-server-review{height:auto;min-height:100dvh;overflow:visible}'));
});

test('Add Server exits submit native POST forms with CSRF before loading the completion page', () => {
  const wizard = read('templates/setup_wizard.html');
  assert.ok(wizard.includes('<form method="post" action="{{$wizard.BasePath}}/cancel"><input type="hidden" name="csrf" value="{{.CSRF}}"><button class="secondary setup-dashboard-exit" type="submit">Cancel</button></form>'));
  assert.ok(wizard.includes('formaction="{{$wizard.BasePath}}/cancel" formnovalidate>Cancel setup</button>'));
  const review = read('templates/setup_review.html');
  assert.ok(review.includes('<form method="post" action="{{$wizard.BasePath}}/review/cancel"><input type="hidden" name="csrf" value="{{.CSRF}}">'));
  assert.ok(!read('static/setup-operation.js').includes('closeWizard'));
  assert.ok(!read('static/setup-operation.js').includes('data-add-server-review-cancel'));
  const completion = read('templates/add_server_cancelled.html');
  assert.ok(completion.includes('/static/add-server-cancelled.js'));
  assert.ok(completion.includes('Setup cancelled.'));
  assert.ok(completion.includes('You can close this tab.'));
  assert.ok(!completion.includes('/?workspace=minecraft-servers'));
});

const vm = require('node:vm');
function completedTab({ opener = 'same-origin', closeThrows = false, focusThrows = false } = {}) {
  const events = [];
  let retry;
  const window = {
    location: { origin: 'https://example.test' },
    close() { events.push('close'); if (closeThrows) throw new Error('Closing denied'); },
  };
  if (opener !== 'absent') {
    window.opener = {
      closed: opener === 'closed',
      get location() {
        if (opener === 'inaccessible') throw new Error('Access denied');
        return { origin: opener === 'cross-origin' ? 'https://other.test' : 'https://example.test' };
      },
      focus() { events.push('focus'); if (focusThrows) throw new Error('Focus denied'); },
    };
  }
  vm.runInNewContext(read('static/add-server-cancelled.js'), {
    window,
    document: { querySelector(selector) {
      assert.equal(selector, '[data-close-add-server]');
      return { addEventListener(name, handler) { assert.equal(name, 'click'); retry = handler; } };
    } },
  });
  return { events, retry };
}

test('completion focuses the same-origin opener before close and allows retry', () => {
  const tab = completedTab();
  assert.deepEqual(tab.events, ['focus', 'close']);
  tab.retry();
  assert.deepEqual(tab.events, ['focus', 'close', 'focus', 'close']);
});

test('missing, closed or cross-origin opener and browser policy leave closing safe to retry', () => {
  for (const opener of ['absent', 'closed', 'cross-origin', 'inaccessible']) {
    const tab = completedTab({ opener, closeThrows: true });
    assert.deepEqual(tab.events, ['close']);
    tab.retry();
    assert.deepEqual(tab.events, ['close', 'close']);
  }
  assert.deepEqual(completedTab({ focusThrows: true }).events, ['focus', 'close']);
});

test('desktop Simple sizing is reserved without scroll and released at the paired-card breakpoint', () => {
  const css = read('static/setup.css');
  assert.ok(css.includes('.setup-entry-wizard .setup-simple-content{min-height:21rem}'));
  assert.ok(css.includes('@media(max-width:760px){\n  .setup-entry-wizard .setup-simple-content{min-height:0}'));
  assert.ok(css.includes('.setup-entry-wizard .setup-simple-actions,.setup-entry-wizard [data-simple-setup]>button{margin-top:auto}'));
  const simpleRules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)].filter(([, selector]) => /setup-simple|data-simple-setup/.test(selector));
  for (const [, , declarations] of simpleRules) assert.doesNotMatch(declarations, /overflow\s*:\s*(auto|scroll|hidden)/);
});

test('Advanced Add Server uses full-width lanes with four, two, then one column', () => {
  const css = read('static/setup.css');
  assert.ok(css.includes('.setup-software-lanes{grid-template-columns:1fr;grid-auto-rows:auto;align-content:start;align-items:start}'));
  assert.ok(css.includes('.setup-software-lanes .setup-server-types{grid-template-columns:repeat(4,minmax(0,1fr))'));
  assert.ok(css.includes('@media(max-width:1000px){\n  .setup-software-lanes .setup-server-types{grid-template-columns:repeat(2,minmax(0,1fr))}'));
  assert.ok(css.includes('@media(max-width:430px){\n  .setup-software-lanes .setup-server-types{grid-template-columns:1fr}'));
  assert.ok(css.includes('.setup-software-lanes .setup-server-types{grid-template-columns:repeat(4,minmax(0,1fr));grid-auto-rows:auto;align-content:start;min-width:0;margin:0}'));
  assert.ok(css.includes('.setup-server-types{grid-auto-rows:1fr}'));
  assert.ok(css.includes('.setup-wizard-panel .setup-software-lanes .setup-server-types legend{font-size:.9rem}'));
});

test('Add Server Simple software and Advanced share card proportions and typography', () => {
  const css = read('static/setup.css');
  const shared = ':is(.setup-entry-wizard .setup-simple-choices:not(.setup-simple-families),.setup-software-lanes)';
  assert.ok(css.includes(`${shared} .server-software-card{grid-template-columns:40px minmax(0,1fr);gap:.7rem;min-height:104px;padding:.65rem .75rem;box-sizing:border-box;align-items:start;align-content:start}`));
  assert.ok(css.includes(`${shared} .server-software-icon,${shared} .server-software-icon img{width:40px;min-width:40px;height:40px}`));
  assert.ok(css.includes(`${shared} .server-software-copy strong{font-size:1rem}`));
  assert.ok(css.includes(`${shared} .server-software-copy small{font-size:.82rem}`));
});

test('shared setup normalizes only Paper and Purpur artwork while preserving icon slots', () => {
  const css = read('static/setup.css');
  const shared = ':is(.setup-entry-wizard .setup-simple-choices:not(.setup-simple-families),.setup-software-lanes)';
  assert.ok(css.includes(`${shared} .server-software-icon,${shared} .server-software-icon img{width:40px;min-width:40px;height:40px}`));
  assert.ok(css.includes('.setup-entry-wizard .setup-simple-families .setup-family-items img{width:24px;height:24px}'));
  const scaledIconRules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)]
    .filter(([, selector, declarations]) => /server-software-icon|setup-family-items/.test(selector) && /\bscale\s*\(/.test(declarations))
    .map(([, selector, declarations]) => [selector.replace(/\/\*[\s\S]*?\*\//g, '').trim(), declarations.trim()]);
  assert.deepEqual(scaledIconRules, [
    [':is(.server-software-icon img,.setup-family-items img)[src$="/paper.svg"]', 'transform:scale(.82);transform-origin:center'],
    [':is(.server-software-icon img,.setup-family-items img)[src$="/purpur.svg"]', 'transform:scale(.90);transform-origin:center'],
  ]);
});

test('Add Server family readability is scoped without changing the family layout', () => {
  const css = read('static/setup.css');
  assert.ok(css.includes('.setup-entry-wizard .setup-simple-families{grid-template-columns:1fr}'));
  assert.ok(css.includes('.setup-entry-wizard .setup-simple-families .server-software-copy strong{font-size:1rem}'));
  assert.ok(css.includes('.setup-entry-wizard .setup-simple-families .server-software-copy small,.setup-entry-wizard .setup-simple-families .setup-family-items{font-size:.82rem}'));
  assert.ok(css.includes('.setup-entry-wizard .setup-simple-families .setup-family-items img{width:24px;height:24px}'));
});

test('one wizard owns both contexts, stores and software layout', () => {
  const production = readdirSync(root).filter(name => name.endsWith('.go') && !name.endsWith('_test.go')).map(read).join('\n');
  for (const old of ['firstRunSetupDrafts', 'addServerDrafts', 'firstRunSetupReviews', 'addServerReviews', 'setupDraftsFor', 'setupReviewsFor', 'setupReviewStoreForDraft', 'addServerWizardPage', 'addServerWizardStart', 'addServerSimple', 'addServerWizardReview', 'addServerWizardReviewBack', 'setupWizardRecommended', 'addServerSoftwareGroups']) {
    assert.ok(!production.includes(old), `duplicate implementation: ${old}`);
  }
  assert.equal((production.match(/var setupDrafts = setupDraftStore/g) || []).length, 1);
  assert.equal((production.match(/var setupReviews = setupReviewStore/g) || []).length, 1);
  const first = read('setup_wizard.go');
  const add = read('add_server_wizard.go');
  for (const handler of ['setupWizardPage', 'setupWizardStart', 'setupWizardSimple']) {
    assert.ok(first.includes(`a.${handler}`));
    assert.ok(add.includes(`a.${handler}`));
    assert.equal((production.match(new RegExp(`func \\(a \\*App\\) ${handler}\\(`, 'g')) || []).length, 1);
  }
  assert.match(first, /POST \/setup\/simple", a.setupWizardSimple/);
  assert.match(first, /POST \/setup\/recommended", a.setupWizardSimple/);
  assert.match(add, /"POST \/simple":\s+a.setupWizardSimple/);
  assert.match(add, /"POST \/recommended":\s+a.setupWizardSimple/);
  assert.match(add, /"GET \/review":\s+a.setupWizardReviewPage/);
  assert.match(add, /"POST \/review\/back":\s+a.setupWizardReviewBack/);
  const wizard = read('templates/setup_wizard.html');
  assert.equal((wizard.match(/template "setup-simple-card"/g) || []).length, 1);
  assert.ok(wizard.includes('class="setup-software-groups setup-software-lanes"'));
  assert.ok(wizard.includes('class="panel setup-welcome setup-entry setup-entry-wizard"'));
  assert.ok(!wizard.includes('Recommended setup'));
  assert.ok(!wizard.includes('/recommended'));
  const css = read('static/setup.css');
  assert.ok(!css.includes('setup-entry-add-server'));
  assert.ok(!css.includes('setup-add-server-software-lanes'));
  assert.ok(css.includes('.setup-software-lanes{grid-template-columns:1fr'));
});
