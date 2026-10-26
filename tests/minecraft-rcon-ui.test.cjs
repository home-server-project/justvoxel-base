const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {test} = require('node:test');
const vm = require('node:vm');
const source = readFileSync(`${__dirname}/../webui/internal/server/static/app.js`, 'utf8');
const implementation = source.slice(source.indexOf('function newMinecraftRconState()'), source.indexOf('if (minecraftOpen && minecraftDialog)'));
class Element {
  constructor(tag) {
    this.tag = tag; this.children = []; this.events = {}; this.attributes = {}; this.value = '';
    this.classList = {toggle() {}};
  }
  append(...children) { this.children.push(...children); }
  replaceChildren(...children) { this.children = children; }
  setAttribute(key, value) { this.attributes[key] = value; }
  addEventListener(name, listener) { this.events[name] = listener; }
  focus() { this.focused = true; }
  all() { return [this, ...this.children.flatMap(child => child.all())]; }
  requestSubmit() { this.submitted = this.events.submit({preventDefault() {}}); }
}
const flush = () => new Promise(resolve => setImmediate(resolve));
function fixture({running = true, request} = {}) {
  const calls = [];
  const context = vm.createContext({document: {createElement: tag => new Element(tag)}});
  vm.runInContext(implementation, context);
  const model = context.newMinecraftRconState();
  let current = true;
  const root = context.createMinecraftRconView({model, running, isCurrent: () => current, request: async values => {
    calls.push(values);
    return request ? request(values) : values.discover ? {commands: [
      {name:'list', description:'Show players'}, {name:'weather', usage:'weather <clear|rain>'},
      {name:'version'}, {name:'plugin:warp', description:'<img src=x onerror=alert(1)>'},
    ]} : {output:'There are 3 players:\nAlex, Steve, .Illia'};
  }});
  const all = () => root.all();
  const button = label => all().find(el => el.tag === 'button' && el.textContent === label);
  const input = all().find(el => el.tag === 'input' && el.type === 'text');
  const transcript = all().find(el => el.tag === 'pre' && el.attributes['aria-label']);
  const form = all().find(el => el.tag === 'form');
  const send = async command => { input.value = command; await form.events.submit({preventDefault() {}}); };
  return {context, model, root, all, button, input, transcript, form, calls, send, stale: () => { current = false; }};
}

test('initial helper is explanatory; first response becomes text transcript and empty success is neutral', async () => {
  const f = fixture({request: async () => ({output:'<script>unsafe</script>\nsecond line'})});
  assert.match(f.transcript.textContent, /# See who is playing\nlist/);
  assert.match(f.transcript.textContent, /No leading slash is required/);
  assert.equal(f.calls.length, 0);
  await f.send('whitelist list');
  assert.equal(f.transcript.textContent, '> whitelist list\n<script>unsafe</script>\nsecond line');
  assert.equal(f.transcript.children.length, 0);
  assert.doesNotMatch(f.transcript.textContent, /# See who is playing/);
  assert.equal(f.input.focused, true);
  const empty = fixture({request: async () => ({output:''})});
  await empty.send('say hello');
  assert.equal(empty.transcript.textContent, '> say hello\nCommand completed.');
});

test('failed request preserves the initial helper and stopped servers cannot execute or discover', async () => {
  const f = fixture({request: async () => { throw new Error('RCON is unavailable.'); }});
  await f.send('list');
  assert.equal(f.model.executed, false);
  assert.match(f.transcript.textContent, /# See who is playing/);
  const stopped = fixture({running:false});
  assert.equal(stopped.input.disabled, true); assert.equal(stopped.button('Send').disabled, true);
  assert.ok(stopped.all().some(el => /Server is stopped/.test(el.textContent)));
  await stopped.send('list'); stopped.button('Commands').events.click(); await flush();
  assert.equal(stopped.calls.length, 0);
});

test('Commands are discovered, grouped and filtered locally; clicking fills input without execution', async () => {
  const f = fixture();
  f.button('Commands').events.click(); await flush();
  assert.equal(f.calls.length, 1); assert.equal(f.calls[0].discover, 'yes');
  assert.deepEqual(f.all().filter(el => el.tag === 'summary').map(el => el.textContent), ['Players & permissions','World & gameplay','Server & performance','Plugins']);
  f.button('plugin:warp').events.click();
  assert.equal(f.input.value, 'plugin:warp'); assert.equal(f.input.focused, true);
  assert.equal(f.calls.length, 1);
  assert.ok(f.all().some(el => el.tag === 'span' && el.textContent === '<img src=x onerror=alert(1)>' && el.children.length === 0));
  const search = f.all().find(el => el.type === 'search');
  search.value = 'warp'; search.events.input();
  assert.ok(f.button('plugin:warp')); assert.equal(f.button('list'), undefined);
  assert.equal(f.calls.length, 1);
  await f.form.events.submit({preventDefault() {}});
  assert.equal(f.calls[1].command, 'plugin:warp');
  assert.equal(f.transcript.hidden, false);
  assert.equal(f.button('Console').attributes['aria-pressed'], 'true');
  assert.match(f.transcript.textContent, /^> plugin:warp\n/);
});

test('Enter submits, ArrowUp/Down navigate session history and Ctrl+L clears visible transcript', async () => {
  const f = fixture();
  f.input.value = 'list';
  let prevented = 0;
  const key = (value, extra = {}) => ({key:value, preventDefault(){prevented++;}, ...extra});
  f.input.events.keydown(key('Enter')); await f.form.submitted;
  await f.send('whitelist list');
  f.input.value = '';
  f.input.events.keydown(key('ArrowUp')); assert.equal(f.input.value, 'whitelist list');
  f.input.events.keydown(key('ArrowUp')); assert.equal(f.input.value, 'list');
  f.input.events.keydown(key('ArrowUp')); assert.equal(f.input.value, 'list');
  f.input.events.keydown(key('ArrowDown')); assert.equal(f.input.value, 'whitelist list');
  f.input.events.keydown(key('ArrowDown')); assert.equal(f.input.value, '');
  f.root.events.keydown(key('l', {ctrlKey:true}));
  assert.equal(f.transcript.textContent, ''); assert.equal(f.model.history.length, 2);
  assert.equal(prevented, 7);
});

test('rows have no Help buttons or detail pane; unavailable discovery invents no commands', async () => {
  const f = fixture(); f.button('Commands').events.click(); await flush();
  assert.equal(f.button('Help'), undefined);
  assert.equal(f.all().some(el => el.className === 'minecraft-rcon-detail'), false);
  assert.doesNotMatch(implementation, /command: "help "|help\.addEventListener/);
  const empty = fixture({request: async () => ({commands:[],partial:true})});
  empty.button('Commands').events.click(); await flush();
  assert.equal(empty.all().filter(el => el.tag === 'details').length, 0);
  assert.ok(empty.all().some(el => /Command discovery is unavailable/.test(el.textContent)));
});

const catalogEntries = [
  {name:'ban', description:' A MOJANG provided command. '}, {name:'minecraft:ban'},
  {name:'msg'}, {name:'tell'}, {name:'w'},
  {name:'tp'}, {name:'teleport'}, {name:'weather', usage:'weather <clear|rain>'}, {name:'minecraft:weather'},
  {name:'plugins', description:'Gets a list of plugins running on the server'}, {name:'bukkit:plugins'},
  {name:'mspt', description:'View server tick times'}, {name:'paper:mspt'},
  {name:'Geyser', description:'Easy way to whitelist Bedrock players'}, {name:'geyser-spigot:geyser'},
  {name:'spark'}, {name:'paper:spark'}, {name:'unique:warp', usage:'unique:warp <place>'},
  {name:'paper', description:'Paper related commands'},
  {name:'purpur', description:'Purpur related commands'},
  {name:'viaversion', description:'Shows ViaVersion Version and more.'},
  {name:'execute'}, {name:'minecraft:unique-core'},
  {name:'stop'}, {name:'minecraft:stop'},
  {name:'Aliases', description:'Lists command aliases'},
  ...['Bukkit', 'Minecraft', 'Paper', 'Geyser-Spigot', 'ViaVersion'].map(name => ({name, description:`All commands for ${name}`})),
];
const sections = f => f.all().filter(el => el.tag === 'details');
const rows = section => section.all().filter(el => el.className === 'minecraft-rcon-command');
const rowNames = f => f.all().filter(el => el.className === 'minecraft-rcon-command').map(el => el.children[0].textContent);
const catalogFixture = () => fixture({request: async () => ({commands:catalogEntries})});

test('presentation cleans filler, help topics, namespaced duplicates and stop without mutating discovery', async () => {
  const f = catalogFixture();
  f.button('Commands').events.click(); await flush();
  const names = rowNames(f);
  for (const name of ['ban', 'msg', 'tell', 'w', 'tp', 'teleport', 'weather', 'plugins', 'mspt', 'Geyser', 'spark', 'unique:warp', 'paper', 'purpur', 'viaversion', 'execute', 'minecraft:unique-core']) {
    assert.ok(names.includes(name), `${name} remains available`);
  }
  for (const name of ['minecraft:ban', 'minecraft:weather', 'bukkit:plugins', 'paper:mspt', 'geyser-spigot:geyser', 'paper:spark', 'stop', 'minecraft:stop', 'Aliases', 'Bukkit', 'Minecraft', 'Paper', 'Geyser-Spigot', 'ViaVersion']) {
    assert.ok(!names.includes(name), `${name} is hidden`);
  }
  assert.equal(names.length, 17);
  const rendered = f.all().map(el => el.textContent || '').join('\n');
  assert.doesNotMatch(rendered, /A Mojang provided command\./i);
  for (const description of ['Gets a list of plugins running on the server', 'Easy way to whitelist Bedrock players', 'View server tick times', 'Purpur related commands', 'Shows ViaVersion Version and more.', 'Paper related commands']) {
    assert.ok(rendered.includes(description));
  }
  assert.match(rendered, /weather <clear\|rain>/);
  assert.equal(f.model.commands, catalogEntries);
  assert.equal(f.model.commands[0].description, ' A MOJANG provided command. ');
  assert.ok(f.model.commands.some(entry => entry.name === 'minecraft:ban'));
  assert.equal(f.button('Help'), undefined);
  f.button('Geyser').events.click();
  assert.equal(f.input.value, 'Geyser'); assert.equal(f.input.focused, true);
  assert.equal(f.calls.length, 1);
  assert.ok(f.all().some(el => el.textContent === 'Commands exposed by this server.'));
});

test('five native collapsible groups count cleaned commands and permit independent toggles', async () => {
  const f = catalogFixture(); f.button('Commands').events.click(); await flush();
  const groups = sections(f);
  assert.deepEqual(groups.map(el => el.children[0].textContent), ['Players & permissions', 'World & gameplay', 'Server & performance', 'Plugins', 'Advanced Minecraft']);
  assert.deepEqual(groups.map(el => el.open), [true, false, false, false, false]);
  assert.deepEqual(groups.map(el => rows(el).length), [4, 3, 2, 3, 5]);
  groups.forEach(el => {
    assert.equal(el.children[0].tag, 'summary');
    assert.equal(el.children[0].children[0].className, 'minecraft-rcon-group-count');
    assert.equal(el.children[0].children[0].textContent, ` (${rows(el).length})`);
  });
  assert.deepEqual(rows(groups[3]).map(el => el.children[0].textContent), ['Geyser', 'spark', 'unique:warp']);
  assert.deepEqual(rows(groups[2]).map(el => el.children[0].textContent), ['plugins', 'mspt']);
  groups[0].open = false; groups[0].events.toggle();
  groups[3].open = true; groups[3].events.toggle();
  assert.deepEqual(groups.map(el => el.open), [false, false, false, true, false]);
  f.button('Console').events.click(); f.button('Commands').events.click();
  assert.deepEqual(sections(f).map(el => el.open), [false, false, false, true, false]);
});

test('search filters cleaned fields, opens matching groups, hides empty groups and clears to defaults', async () => {
  const f = catalogFixture(); f.button('Commands').events.click(); await flush();
  const search = f.all().find(el => el.type === 'search');
  const filter = value => {search.value = value; search.events.input();};
  filter('commands');
  assert.deepEqual(sections(f).map(el => el.children[0].textContent), ['Advanced Minecraft']);
  assert.ok(sections(f).every(el => el.open));
  assert.deepEqual(rowNames(f), ['paper', 'purpur']);
  filter('place');
  assert.deepEqual(rowNames(f), ['unique:warp']);
  assert.equal(sections(f)[0].open, true);
  filter('t');
  assert.ok(sections(f).length > 1);
  assert.ok(sections(f).every(el => el.open));
  for (const group of sections(f)) assert.equal(group.children[0].children[0].textContent, ` (${rows(group).length})`);
  filter('A Mojang provided command.');
  assert.equal(sections(f).length, 0); assert.equal(rowNames(f).length, 0);
  filter('');
  assert.equal(sections(f).length, 5);
  assert.deepEqual(sections(f).map(el => el.open), [true, false, false, false, false]);
  assert.equal(f.calls.length, 1);
});

test('selection invalidation prevents detached execution and ignores old in-flight results', async () => {
  let finish;
  const f = fixture({request: () => new Promise(resolve => { finish = resolve; })});
  const pending = f.send('list');
  f.stale(); finish({output:'old server response'}); await pending;
  assert.equal(f.model.executed, false);
  await f.send('say stale'); assert.equal(f.calls.length, 1);
  const next = f.context.newMinecraftRconState();
  assert.equal(next.history.length, 0); assert.equal(next.transcript.length, 0);
  assert.equal(next.commands, null); assert.equal(next.executed, false);
  const listener = source.slice(source.indexOf('  window.addEventListener("justvoxel:server-selection-changed",', source.indexOf('if (minecraftOpen && minecraftDialog)')), source.indexOf('  const refreshWorkspace =', source.indexOf('if (minecraftOpen && minecraftDialog)')));
  assert.match(listener, /rconModel = newMinecraftRconState\(\)/);
});

test('selection invalidation ignores discovery responses and detached command choices', async () => {
  let finish;
  const f = fixture({request: () => new Promise(resolve => {finish = resolve;})});
  f.button('Commands').events.click();
  f.stale(); finish({commands:[{name:'old:command'}], partial:true}); await flush();
  assert.equal(f.model.commands, null); assert.equal(f.model.partial, false);
  assert.equal(f.button('old:command'), undefined);
  const loaded = fixture(); loaded.button('Commands').events.click(); await flush();
  loaded.stale(); loaded.button('list').events.click();
  assert.equal(loaded.input.value, ''); assert.equal(loaded.calls.length, 1);
});

test('existing selected request guard rejects pre-send changes and stale in-flight results', async () => {
  const start = source.indexOf('  const requestWorkspaceJSON =', source.indexOf('if (minecraftOpen && minecraftDialog)'));
  const code = source.slice(start, source.indexOf('  const openDeleteDialog =', start));
  let ready, complete; const calls = [];
  const context = vm.createContext({
    justVoxelSelectionGeneration:{minecraft:1},
    justVoxelSelectionReady:new Promise(resolve => {ready=resolve;}),
    justVoxelSelectedURL:url => url+'?server=jv-family1',
    justVoxelStaleSelection:() => Object.assign(new Error('stale'),{staleSelection:true}),
    fetch:async (url,options) => {calls.push({url,options}); return new Promise(resolve => {complete=resolve;});},
  });
  vm.runInContext(code+'\nglobalThis.request=requestWorkspaceJSON;', context);
  const waiting = context.request('/api/minecraft/workspace/console',{method:'POST'});
  context.justVoxelSelectionGeneration.minecraft++; ready();
  await assert.rejects(waiting, error => error.staleSelection); assert.equal(calls.length, 0);
  context.justVoxelSelectionReady = null;
  const pending = context.request('/api/minecraft/workspace/console',{method:'POST'});
  assert.equal(calls[0].url, '/api/minecraft/workspace/console?server=jv-family1');
  context.justVoxelSelectionGeneration.minecraft++;
  complete({ok:true,status:200,json:async()=>({id:'jv-family1',output:'old'})});
  await assert.rejects(pending, error => error.staleSelection);
});

test('RCON uses selected console context, Administrator tab, text nodes and page memory only', () => {
  const header = readFileSync(`${__dirname}/../webui/internal/server/templates/header.html`, 'utf8');
  assert.match(header, /data-minecraft-tab="crossplay"[^]*?class="nav-admin-only"[^>]*data-minecraft-tab="rcon"/);
  const integration = source.slice(source.indexOf('  const renderRcon ='), source.indexOf('  async function loadCurrentTab',source.indexOf('  const renderRcon =')));
  assert.match(integration, /role-administrator/);
  assert.match(integration, /justVoxelSelectedURL\("\/api\/minecraft\/workspace\/console", "minecraft"\)/);
  assert.match(integration, /server_id: id/);
  assert.doesNotMatch(implementation, /innerHTML|outerHTML|insertAdjacentHTML|localStorage|sessionStorage|document\.cookie/);
});

test('Console and Commands pressed states use the existing green selected style', async () => {
  const f = fixture();
  const pressed = (console, commands) => {
    assert.equal(f.button('Console').attributes['aria-pressed'], String(console));
    assert.equal(f.button('Commands').attributes['aria-pressed'], String(commands));
    assert.equal(f.button('Console').className, 'secondary');
    assert.equal(f.button('Commands').className, 'secondary');
  };
  pressed(true, false);
  f.button('Commands').events.click(); await flush(); pressed(false, true);
  assert.equal(f.transcript.hidden, true);
  f.button('Console').events.click(); pressed(true, false);
  assert.equal(f.transcript.hidden, false);
  const css = readFileSync(`${__dirname}/../webui/internal/server/static/app.css`, 'utf8');
  assert.match(css, /\.minecraft-rcon-toolbar button\[aria-pressed="true"\]\{background:#25382e;border-color:#4f8b69;color:#c8f2d7}/);
});

test('Clear and Ctrl+L empty only the visible transcript and preserve session command history/catalog', async () => {
  const f = fixture();
  assert.equal(f.button('Clear').type, 'button');
  assert.equal(f.button('Clear').className, 'secondary');
  const lane = f.form.children;
  assert.deepEqual(lane.map(el => el.tag === 'input' ? 'input' : el.textContent), ['input', 'Clear', 'Send']);
  f.button('Clear').events.click();
  assert.equal(f.transcript.textContent, '');
  f.button('Commands').events.click(); await flush();
  await f.send('list'); await f.send('version');
  const commands = f.model.commands, history = [...f.model.history], calls = f.calls.length;
  f.input.value = 'unsent draft';
  f.button('Clear').events.click();
  assert.equal(f.transcript.textContent, '');
  assert.equal(f.model.executed, true);
  assert.deepEqual([...f.model.history], history);
  assert.equal(f.model.commands, commands); assert.equal(f.calls.length, calls);
  assert.equal(f.input.value, 'unsent draft');
  f.button('Commands').events.click(); f.button('Console').events.click();
  assert.equal(f.transcript.textContent, '');
  f.input.events.keydown({key:'ArrowUp', preventDefault(){}});
  assert.equal(f.input.value, 'version');
  await f.send('list');
  assert.match(f.transcript.textContent, /^> list\n/);
  let prevented = false;
  f.root.events.keydown({key:'l', ctrlKey:true, preventDefault(){prevented=true;}});
  assert.equal(prevented, true); assert.equal(f.transcript.textContent, '');
  assert.equal(f.model.history.length, 3); assert.equal(f.model.commands, commands);
  // Both controls call the same implementation, rather than duplicating clear logic.
  assert.match(implementation, /clear\.addEventListener\("click", clearTranscript\)/);
  assert.match(implementation, /event\.preventDefault\(\); clearTranscript\(\)/);
  assert.equal(f.context.newMinecraftRconState().transcriptCleared, false);
});

test('Refresh replaces the server-derived catalog; partial results retain discovered commands', async () => {
  let discovery = 0;
  const f = fixture({request: async () => ({commands:[{name: ++discovery === 1 ? 'actual:one' : 'actual:two'}], partial:true})});
  f.button('Commands').events.click(); await flush();
  assert.ok(f.button('actual:one'));
  assert.ok(f.all().some(el => /Partial command list/.test(el.textContent)));
  await f.button('Refresh Commands').events.click();
  assert.equal(f.button('actual:one'), undefined); assert.ok(f.button('actual:two'));
  assert.equal(f.calls.length, 2);
  f.button('Console').events.click(); f.button('Commands').events.click();
  assert.ok(f.all().some(el => /Partial command list/.test(el.textContent)));
  assert.equal(f.all().filter(el => el.tag === 'details').length, 1);
});

test('terminal and command list scroll inside the resizable upper area, with a standard non-stretched Send', () => {
  const css = readFileSync(`${__dirname}/../webui/internal/server/static/app.css`, 'utf8');
  assert.match(css, /\.minecraft-rcon-upper\{[^}]*flex:1;min-height:0;overflow:hidden/);
  assert.match(css, /\.minecraft-rcon-transcript\{[^}]*flex:1;[^}]*min-height:0;overflow:auto;[^}]*border:1px solid #303942;[^}]*background:#0f1317;[^}]*monospace/);
  assert.match(css, /\.minecraft-rcon-transcript\{white-space:pre-wrap;overflow-wrap:anywhere/);
  assert.match(css, /\.minecraft-rcon-commands\{[^}]*flex-direction:column;[^}]*min-height:0;[^}]*overflow:hidden/);
  assert.match(css, /\.minecraft-rcon-command-list\{flex:1;min-height:0;overflow:auto/);
  assert.match(css, /\.minecraft-rcon-lane\{display:flex;flex:none;align-items:center/);
  assert.match(css, /\.minecraft-rcon-lane button\{flex:none;margin:0}/);
  assert.doesNotMatch(css, /\.minecraft-rcon-lane button\{[^}]*(?:padding:|height:|font-weight:|border-radius:)/);
  assert.match(implementation, /transcript\.scrollTop = transcript\.scrollHeight/);
});
