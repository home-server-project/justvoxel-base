const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {test} = require('node:test');
const vm = require('node:vm');
const source = readFileSync(`${__dirname}/../webui/internal/server/static/app.js`, 'utf8');
const helper = source.slice(0, source.indexOf('// Identity fallback'));
class Element {
  constructor(tag) { this.tag = tag; this.children = []; this.dataset = {}; this.events = {}; }
  append(child) { this.children.push(child); }
  replaceChildren(child) { this.children = [child]; }
  setAttribute() {}
  querySelector() { return this.children.find(child => Object.hasOwn(child.dataset, 'selectedServer')); }
  closest() { return {querySelector: () => ({getAttribute: () => 'installed'})}; }
  addEventListener(event, handler) { this.events[event] = handler; }
}
for (const inventory of [[], [{id:'jv-first01',name:'Family Survival'}], [{id:'jv-first01',name:'Family Survival'},{id:'jv-second1',name:'Creative'}]]) {
 for (const explicit of inventory.length > 1 ? [false,true] : [false]) {
 const selectedId = inventory.length === 1 || explicit ? inventory.at(-1).id : '';
 test(`${inventory.length} servers, explicit=${explicit}: honest labels and selection`, async () => {
  const slot = new Element('span'); slot.dataset.serverWorkspace = 'plugins';
  let replaced;
  const events = [];
  const others = Array.from({length:5}, () => new Element("select"));
  const dashboard = {dataset:{}};
  const location = {origin:"https://justvoxel.test",href:"https://justvoxel.test/?workspace=backups&created=yes&operation=stale",search:"?workspace=backups&created=yes&operation=stale"};
  const context = vm.createContext({URL, URLSearchParams,
   location,
   CustomEvent: class { constructor(type, options) { this.type = type; this.detail = options.detail; } },
   window: {location, history:{state:{desktop:true}, replaceState(state, _, target) { replaced = new URL(target, location.href); location.search = replaced.search; location.href = replaced.href; }}, dispatchEvent: event => events.push(event)},
   document: {createElement: tag => new Element(tag), querySelector: () => dashboard, querySelectorAll: () => others},
   fetch: async () => ({ok:true,json:async () => ({servers:inventory,selected:{id:selectedId}})}),
  });
  vm.runInContext(helper, context);
  const selectServer = context.justVoxelSelectServer;
  const selections = [];
  context.justVoxelSelectServer = (...args) => { selections.push(args); return selectServer(...args); };
  await context.justVoxelServerSelection(slot);
  const select = slot.children[0].children[0];
  assert.equal(slot.children[0].textContent, 'Server: ');
  assert.equal(select.disabled, inventory.length === 0);
  if (!selectedId) {
   assert.equal(select.children[0].textContent, inventory.length ? 'Choose server' : 'No servers');
   assert.equal(select.children[0].selected,true);
   assert.equal(select.children[0].value,'');
  }
  for (const server of inventory) {
   const option = select.children.find(option => option.value === server.id);
   assert.equal(option.textContent, server.name);
   assert.equal(option.selected,server.id === selectedId);
  }
  if (inventory.length) {
   select.value = inventory.at(-1).id; select.events.change();
   assert.equal(replaced.searchParams.get('server'),inventory.at(-1).id);
   assert.equal(replaced.searchParams.get('workspace'),'plugins');
   assert.equal(replaced.searchParams.get('tab'),'installed');
   assert.equal(replaced.searchParams.get('created'),'yes');
   assert.equal(replaced.searchParams.has('operation'),false);
   assert.deepEqual(selections, [[inventory.at(-1).id, 'plugins', 'installed']]);
   assert.equal(context.justVoxelSelectionGeneration, 1);
   for (const control of others) assert.equal(control.value, inventory.at(-1).id);
   assert.equal(dashboard.dataset.selectedMinecraftId, inventory.at(-1).id);
   assert.equal(events.length, 1);
   assert.equal(events[0].type, 'justvoxel:server-selection-changed');
   assert.equal(events[0].detail.serverId, inventory.at(-1).id);
   assert.equal(events[0].detail.workspace, 'plugins');
   assert.equal(context.justVoxelSelectedURL('/api/version/workspace/status'), '/api/version/workspace/status?server=' + inventory.at(-1).id);
   select.events.change();
   assert.equal(events.length, 1, 'same server does not dispatch a fabricated selection change');
   assert.equal(context.justVoxelSelectionGeneration, 1);
   for (const invalid of ['', 'family', 'jv-short', 'jv-ABCDEF', 'jv-abcdefghijklm']) {
     assert.equal(selectServer(invalid, 'plugins'), false);
     assert.equal(events.length, 1);
     assert.equal(context.justVoxelSelectionGeneration, 1);
   }
   select.value = 'jv-forged1'; select.events.change();
   assert.equal(events.length, 1, 'reject IDs outside the selector inventory');
   assert.equal(selections.length, 2, 'invalid inventory selection never reaches shared mutation');
  }
  await context.justVoxelServerSelection(slot);
  assert.equal(slot.children.length,1);
 });
 }
}
test('all five workspaces target explicit slots and inventory opens through normal windows', () => {
 const header = readFileSync(`${__dirname}/../webui/internal/server/templates/header.html`, 'utf8');
 for (const name of ['minecraft','version','plugins','backups','migration']) assert.ok(header.includes(`data-server-selector-slot data-server-workspace="${name}"`));
 assert.equal((header.match(/data-server-selector-slot/g)||[]).length,5);
 assert.doesNotMatch(helper,/\.prepend\(/);
 assert.doesNotMatch(source,/querySelector\("\.workspace-window-header"\)/);
 assert.match(source,/"minecraft-servers": "\[data-minecraft-servers-open\]"/);
 assert.match(source,/setupWorkspaceWindow\(minecraftServersDialog/);
 assert.match(source,/justVoxelMinecraftServers\(content\)/);
});

test('selection helper never navigates the document or touches wallpaper', () => {
  assert.doesNotMatch(helper, /location\.(?:assign|reload)|location\.href\s*=/);
  assert.doesNotMatch(helper, /wallpaper/i);
});

const versionSource = readFileSync(`${__dirname}/../webui/internal/server/static/version-workspace.js`, 'utf8');
function selectionHandler(script, anchor) {
  const start = script.indexOf('  window.addEventListener("justvoxel:server-selection-changed", () => {', script.indexOf(anchor));
  assert.ok(start >= 0);
  return script.slice(start, script.indexOf('\n  });', start) + 6);
}
for (const [workspace, refresh, ticket] of [
  ['minecraft', 'loadCurrentTab', 'loadSequence'], ['version', 'render', 'sequence'],
  ['plugins', 'load', 'generation'], ['backups', 'loadBackups', 'loadSequence'],
  ['migration', 'loadMigration', 'loadSequence'],
]) {
  test(`${workspace} detaches pending local state, refreshes open views, and leaves submitted operations alive`, () => {
    const script = workspace === 'version' ? versionSource : source;
    const anchor = workspace === 'version' ? 'const versionLauncher' : `if (${workspace === 'plugins' ? 'pluginsDialog && pluginsOpen' : workspace + 'Open && ' + workspace + 'Dialog'})`;
    const handler = selectionHandler(script, anchor);
    const calls = [];
    const dialog = {open:true};
    const content = {replaceChildren() { calls.push('clear'); }};
    const context = vm.createContext({
      URL, URLSearchParams, location:{origin:'https://justvoxel.test',search:'?server=jv-second1'},
      window:{addEventListener: (_, callback) => { context.change = callback; }},
      [`${workspace}Dialog`]:dialog, content, results:content, panelBody:content,
      [ticket]:0, currentTab:'export', tabURLs:{export:'/workspace/migration/export'},
      currentURL:'/old?server=jv-first01', updateOperation:'old', completedOperation:{}, operationKinds:new Map([['old',{}]]),
      migrationLoadController:{abort() { calls.push('detach-read'); }},
      migrationSubmitController:{abort() { assert.fail('submitted migration must not be aborted'); }},
      installController:{abort() { assert.fail('submitted plugin action must not be aborted'); }},
      controller:{abort() { calls.push('detach-read'); }},
      migrationSourceSMBPassword:'old',migrationExportSMBPassword:'old',
      capacityTimer:1,searchTimer:2,clearTimeout() {},clearInventoryTimer() {},
      closePanel() { assert.equal(context.installController, null); calls.push('clear'); },
      resetCursors() {},operationIdentity:{},discoverServerKey:'old',
      setMigrationBusy() {},resumePluginInstall() {},
      [refresh]: () => { calls.push('refresh'); },
    });
    vm.runInContext(handler, context);
    context.change();
    assert.ok(calls.includes('clear'));
    assert.ok(calls.includes('refresh'));
    assert.ok(context[ticket] > 0);
    assert.doesNotMatch(handler, /location\.(?:assign|reload)|cancel|rollback/);
    calls.length = 0; dialog.open = false; context.change();
    assert.ok(!calls.includes('refresh'), 'closed workspace loads when opened later');
  });
}

for (const workspace of ['minecraft', 'version', 'plugins']) {
  test(`${workspace} ignores old async responses without aborting submitted requests`, async () => {
    const script = workspace === 'version' ? versionSource : source;
    const startText = workspace === 'minecraft' ? '  const requestWorkspaceJSON = async' : workspace === 'version' ? '  const request = async' : '  const pluginRequest = async';
    const start = script.indexOf(startText);
    const requestSource = script.slice(start, script.indexOf('\n  };', start) + 5);
    const name = workspace === 'minecraft' ? 'requestWorkspaceJSON' : workspace === 'version' ? 'request' : 'pluginRequest';
    let complete;
    const calls = [];
    const context = vm.createContext({
      URLSearchParams,
      document:{querySelector:()=>null},
      justVoxelSelectionGeneration:0,
      justVoxelSelectedURL:url => url + '?server=jv-first01',
      justVoxelStaleSelection:()=>Object.assign(new Error('stale'),{staleSelection:true}),
      fetch:(url, options) => { calls.push({url,options}); return new Promise(resolve => { complete = resolve; }); },
    });
    vm.runInContext(requestSource + `\nglobalThis.requestUnderTest = ${name};`, context);
    const pending = workspace === 'plugins' ? context.requestUnderTest('install', {plan_fingerprint:'old'}) : context.requestUnderTest('/api/action', {method:'POST'});
    context.justVoxelSelectionGeneration++;
    complete({ok:true,json:async()=>({operation:{operation_id:'old'}})});
    await assert.rejects(pending, error => error.staleSelection === true);
    assert.match(calls[0].url, /server=jv-first01/);
    assert.equal(calls[0].options.method, 'POST');
    assert.equal(calls[0].options.signal, undefined);
  });
}

for (const workspace of ['backups', 'migration']) {
  test(`${workspace} rejects old fragment responses after the selection ticket changes`, async () => {
    const name = workspace === 'backups' ? 'loadBackups' : 'loadMigration';
    const start = source.indexOf(`  const ${name} = async`);
    const block = source.slice(start, source.indexOf('\n  };', start) + 5);
    const pendingRequests = [];
    const renders = [];
    const dialog = {open:true};
    const location = {href:'https://justvoxel.test/?server=jv-first01',origin:'https://justvoxel.test',search:'?server=jv-first01'};
    let changeSelection;
    const context = vm.createContext({
      URL, URLSearchParams, AbortController, currentURL:`/workspace/${workspace}/export`, loadSequence:0,
      location, window:{location,addEventListener:(_, callback)=>{changeSelection=callback;}},
      [`${workspace}Dialog`]:dialog,
      state:{}, refreshButton:{}, content:{replaceChildren:()=>renders.push('clear')},
      currentTab:'export', tabURLs:{export:'/workspace/migration/export'},
      migrationLoadController:null, migrationSubmitController:null,
      migrationSourceSMBPassword:'', migrationExportSMBPassword:'',
      ensureBackupsAssets:async()=>{},ensureMigrationAssets:async()=>{},
      setMigrationBusy:()=>{},handleMigrationAuth:async()=>false,
      justVoxelSelectedURL:url=>url+'?server='+new URLSearchParams(location.search).get('server'),
      renderBackupsMarkup:markup=>renders.push(markup),renderMigrationMarkup:markup=>renders.push(markup),
      // Keep aborted reads resolvable to exercise the sequence guard even when
      // a response wins the race with aborting the old Migration request.
      fetch:(url, options)=>new Promise(resolve=>{pendingRequests.push({url,options,resolve});}),
    });
    const handler = selectionHandler(source, `if (${workspace}Open && ${workspace}Dialog)`);
    vm.runInContext(block + '\n' + handler + `\nglobalThis.loadUnderTest = ${name};`, context);
    const pending = context.loadUnderTest();
    // Asset loading crosses the VM's promise realm. Wait an event-loop turn
    // so its continuation reaches fetch before resolving either response.
    await new Promise(resolve=>setImmediate(resolve));
    assert.equal(pendingRequests.length, 1, 'open workspace must begin the old-server fragment request');
    const oldRequest = pendingRequests[0];
    assert.equal(typeof oldRequest.resolve, 'function');
    assert.match(oldRequest.url, /server=jv-first01/);

    location.search = '?server=jv-second1';
    location.href = location.origin + '/' + location.search;
    changeSelection();
    await new Promise(resolve=>setImmediate(resolve));
    assert.equal(pendingRequests.length, 2, 'selection handler must refetch the open workspace');
    const newRequest = pendingRequests[1];
    assert.match(newRequest.url, /server=jv-second1/);
    const response = (request, markup) => ({ok:true,status:200,url:new URL(request.url,location.origin).href,text:async()=>markup});
    newRequest.resolve(response(newRequest, '<new-server-content>'));
    await new Promise(resolve=>setImmediate(resolve));
    assert.deepEqual(renders, ['clear', '<new-server-content>']);

    oldRequest.resolve(response(oldRequest, '<old-server-review>'));
    await pending;
    assert.deepEqual(renders, ['clear', '<new-server-content>'], 'late old response must not replace the selected server');
  });
}
