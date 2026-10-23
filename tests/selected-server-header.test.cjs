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
const workspaces = ['minecraft', 'version', 'plugins', 'backups', 'migration'];
const inventory = [{id:'jv-family1',name:'Family'}, {id:'jv-kids001',name:'Kids'}, {id:'jv-third01',name:'Third'}];
const key = (username, workspace) => `justvoxel-workspace-window-v1:${encodeURIComponent(username)}:${workspace}`;
function page({username = 'admin', storage = new Map(), servers = inventory, search = '', unavailable = false, pathname = '/', boundary = false} = {}) {
  const slots = Object.fromEntries(workspaces.map(workspace => {
    const slot = new Element('span'); slot.dataset.serverWorkspace = workspace; return [workspace, slot];
  }));
  const events = [], requests = [];
  const dashboard = {dataset:{workspaceUsername:username, selectedMinecraftId:''}};
  const location = {origin:'https://justvoxel.test', href:`https://justvoxel.test/${search}`, search, pathname};
  const context = vm.createContext({URL, URLSearchParams, location,
    CustomEvent: class { constructor(type, options) { this.type = type; this.detail = options.detail; } },
    window:{location, localStorage:{
      getItem(k) { if (unavailable) throw Error('storage denied'); return storage.get(k); },
      setItem(k,v) { if (unavailable) throw Error('storage denied'); storage.set(k,v); },
    }, history:{state:{desktop:true},replaceState(state,_,target) { const u = new URL(target,location.href); location.search=u.search; location.href=u.href; }},dispatchEvent:event=>events.push(event)},
    document:{createElement:tag=>new Element(tag),
      querySelector:selector=>selector.startsWith('#dashboard') ? dashboard : boundary ? {} : null,
      querySelectorAll:selector=>{
        const workspace = selector.match(/data-server-workspace="([^"]+)"/)?.[1];
        return workspace && slots[workspace].children.length ? [slots[workspace].children[0].children[0]] : [];
      }},
    fetch:async url=>{ requests.push(url); return {ok:true,json:async()=>({servers,selected:{id:''}})}; },
  });
  vm.runInContext(helper,context);
  return {context,slots,storage,events,requests,dashboard,location,
    async init() { for (const workspace of workspaces) await context.justVoxelServerSelection(slots[workspace]); },
    select(workspace) { return slots[workspace].children[0].children[0]; },
    saved(workspace) { return JSON.parse(storage.get(key(username,workspace)) || '{}'); },
    change(workspace,id) { const control=this.select(workspace); control.value=id; control.events.change(); },
  };
}
for (const count of [0,1,2]) {
  test(`${count} servers: authoritative choices without first-server guessing`, async () => {
    const p=page({servers:inventory.slice(0,count)}); await p.init();
    for (const workspace of workspaces) {
      const select=p.select(workspace);
      assert.equal(select.disabled,count===0);
      if (count!==1) {
        assert.equal(select.children[0].textContent,count?'Choose server':'No servers');
        assert.equal(select.children[0].value,''); assert.equal(select.children[0].selected,true);
      } else assert.equal(select.children.find(option=>option.selected).value,inventory[0].id);
      for (const server of inventory.slice(0,count)) assert.equal(select.children.find(option=>option.value===server.id).textContent,server.name);
    }
    assert.ok(p.requests.length > 0);
    assert.ok(p.requests.every(url=>url === '/api/minecraft/workspace/selection?selection=none'));
  });
}
test('five independent selections persist by username and workspace across login/pages',async()=>{
  const p=page(); await p.init();
  const choices=['jv-family1','jv-family1','jv-kids001','jv-family1','jv-kids001'];
  workspaces.forEach((workspace,i)=>p.change(workspace,choices[i]));
  for (const [i,workspace] of workspaces.entries()) {
    assert.equal(p.saved(workspace).selectedServer,choices[i]);
    assert.equal(p.select(workspace).value,choices[i]);
  }
  assert.equal(p.dashboard.dataset.selectedMinecraftId,'jv-family1');
  const before=p.events.length;
  p.change('plugins','jv-third01');
  assert.equal(p.events.length,before+1);
  assert.equal(p.events.at(-1).detail.workspace,'plugins');
  assert.equal(p.events.at(-1).detail.serverId,'jv-third01');
  assert.equal(p.context.justVoxelSelectionGeneration.plugins,2);
  assert.equal(p.context.justVoxelSelectionGeneration.minecraft,1);
  assert.equal(p.dashboard.dataset.selectedMinecraftId,'jv-family1');
  for (const workspace of workspaces.filter(w=>w!=='plugins')) assert.equal(p.saved(workspace).selectedServer,choices[workspaces.indexOf(workspace)]);
  assert.equal(new URL(p.location.href).searchParams.get('workspace'),'plugins');
  assert.equal(new URL(p.location.href).searchParams.get('tab'),'installed');
  p.change('plugins','jv-third01'); assert.equal(p.events.length,before+1);
  p.change('minecraft','jv-third01');
  assert.equal(p.dashboard.dataset.selectedMinecraftId,'jv-third01');
  assert.equal(p.saved('version').selectedServer,'jv-family1');
  assert.equal(p.saved('backups').selectedServer,'jv-family1');
  assert.equal(p.saved('migration').selectedServer,'jv-kids001');
  const next=page({storage:p.storage}); await next.init();
  for (const workspace of workspaces) assert.equal(next.select(workspace).children.find(option=>option.selected).value,p.saved(workspace).selectedServer);
  for (const username of ['operator','viewer']) {
    const other=page({username,storage:p.storage}); await other.init();
    for (const workspace of workspaces) assert.equal(other.select(workspace).children[0].textContent,'Choose server');
  }
});
test('all five workspaces can hold five different immutable targets', async()=>{
  const servers = [...inventory,{id:'jv-fourth1',name:'Fourth'},{id:'jv-fifth01',name:'Fifth'}];
  const p=page({servers}); await p.init();
  workspaces.forEach((workspace,i)=>p.change(workspace,servers[i].id));
  for (const [i,workspace] of workspaces.entries()) {
    assert.equal(p.saved(workspace).selectedServer,servers[i].id);
    assert.equal(p.select(workspace).value,servers[i].id);
    assert.equal(new URL(p.context.justVoxelSelectedURL('/api/read',workspace),p.location.origin).searchParams.get('server'),servers[i].id);
  }
});
test('explicit workspace links override and save only their workspace; legacy links apply only to Minecraft',async()=>{
  for (const search of ['?workspace=plugins&server=jv-kids001&operation=stale&created=yes','?server=jv-kids001']) {
    const storage=new Map(workspaces.map(w=>[key('admin',w),JSON.stringify({selectedServer:'jv-family1',open:true,width:500})]));
    const p=page({search,storage}); await p.init();
    const destination=search.includes('workspace')?'plugins':'minecraft';
    for (const workspace of workspaces) {
      assert.equal(p.saved(workspace).selectedServer,workspace===destination?'jv-kids001':'jv-family1');
      assert.equal(p.saved(workspace).width,500); assert.equal(p.saved(workspace).open,true);
    }
    p.change('plugins','jv-third01');
    assert.equal(new URL(p.location.href).searchParams.has('operation'),false);
    if (search.includes('created')) assert.equal(new URL(p.location.href).searchParams.get('created'),'yes');
  }
});
test('deleted saved IDs clear only their workspace, with sole-server automatic fallback',async()=>{
  for (const servers of [inventory,inventory.slice(0,1)]) {
    const storage=new Map([[key('admin','plugins'),JSON.stringify({selectedServer:'jv-deleted',open:true})],[key('admin','minecraft'),JSON.stringify({selectedServer:'jv-family1'})]]);
    const p=page({storage,servers}); await p.init();
    assert.equal(p.saved('plugins').selectedServer,servers.length===1?'jv-family1':'');
    assert.equal(p.saved('plugins').open,true);
    assert.equal(p.saved('minecraft').selectedServer,'jv-family1');
    if (servers.length>1) assert.equal(p.select('plugins').children[0].textContent,'Choose server');
    assert.equal(p.requests[0],'/api/minecraft/workspace/selection?selection=none');
  }
});
test('storage failure leaves current-session selection and workspace request targeting usable',async()=>{
  const p=page({unavailable:true}); await p.init();
  workspaces.forEach((workspace,i)=>p.change(workspace,inventory[i%inventory.length].id));
  for (const [i,workspace] of workspaces.entries()) {
    const url=new URL(p.context.justVoxelSelectedURL('/api/read',workspace),p.location.origin);
    assert.equal(url.searchParams.get('server'),inventory[i%inventory.length].id);
  }
  assert.equal(p.context.justVoxelSelectedURL('/api/read?server=jv-bound01','plugins'),'/api/read?server=jv-bound01');
  assert.equal(new URL(p.context.justVoxelSelectedURL('/api/dashboard-status'),p.location.origin).searchParams.get('server'),'jv-family1');
});
test('credential boundaries never persist server selections',async()=>{
  for (const options of [{pathname:'/login'},{pathname:'/password'},{boundary:true}]) {
    const p=page(options); await p.init(); p.change('plugins','jv-kids001');
    assert.equal(p.storage.size,0);
  }
});
test('selector rejects malformed and out-of-inventory IDs without events or persistence',async()=>{
  const p=page(); await p.init();
  for (const id of ['','family','jv-short','jv-ABCDEF','jv-abcdefghijklm','jv-forged1']) p.change('plugins',id);
  assert.equal(p.events.length,0); assert.equal(p.storage.size,0);
  for (const id of ['','family','jv-short','jv-ABCDEF','jv-abcdefghijklm']) assert.equal(p.context.justVoxelSelectServer(id,'plugins'),false);
  assert.equal(p.context.justVoxelSelectServer('jv-family1','unknown'),false);
  await p.context.justVoxelServerSelection(p.slots.plugins); assert.equal(p.slots.plugins.children.length,1);
});
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
  const start = script.indexOf('  window.addEventListener("justvoxel:server-selection-changed", (event) => {', script.indexOf(anchor));
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
    calls.length = 0;
    context.change({detail:{workspace:workspace === 'minecraft' ? 'plugins' : 'minecraft'}});
    assert.deepEqual(calls, [], 'unrelated workspace must not detach or refresh');
    context.change({detail:{workspace}});
    assert.ok(calls.includes('clear'));
    assert.ok(calls.includes('refresh'));
    assert.ok(context[ticket] > 0);
    assert.doesNotMatch(handler, /location\.(?:assign|reload)|cancel|rollback/);
    calls.length = 0; dialog.open = false; context.change({detail:{workspace}});
    assert.ok(!calls.includes('refresh'), 'closed workspace loads when opened later');
  });
}

for (const workspace of ['minecraft', 'version', 'plugins']) {
 for (const changedWorkspace of [workspace, workspace === 'minecraft' ? 'plugins' : 'minecraft']) {
  test(`${workspace} pending response after ${changedWorkspace} change preserves workspace stale protection`, async () => {
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
      justVoxelSelectionGeneration:Object.fromEntries(workspaces.map(w=>[w,0])),
      justVoxelSelectedURL:url => url + '?server=jv-first01',
      justVoxelStaleSelection:()=>Object.assign(new Error('stale'),{staleSelection:true}),
      fetch:(url, options) => { calls.push({url,options}); return new Promise(resolve => { complete = resolve; }); },
    });
    vm.runInContext(requestSource + `\nglobalThis.requestUnderTest = ${name};`, context);
    const pending = workspace === 'plugins' ? context.requestUnderTest('install', {plan_fingerprint:'old'}) : context.requestUnderTest('/api/action', {method:'POST'});
    await new Promise(resolve=>setImmediate(resolve));
    context.justVoxelSelectionGeneration[changedWorkspace]++;
    complete({ok:true,json:async()=>({operation:{operation_id:'old'}})});
    if (changedWorkspace === workspace) await assert.rejects(pending, error => error.staleSelection === true);
    else assert.equal((await pending).operation.operation_id,'old', 'another workspace must not invalidate this request');
    assert.match(calls[0].url, /server=jv-first01/);
    assert.equal(calls[0].options.method, 'POST');
    assert.equal(calls[0].options.signal, undefined);
  });
 }
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
      justVoxelSelectionGeneration:Object.fromEntries(workspaces.map(w=>[w,0])),
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

    context.justVoxelSelectionGeneration[workspace === 'backups' ? 'migration' : 'backups']++;
    changeSelection({detail:{workspace:workspace === 'backups' ? 'migration' : 'backups'}});
    assert.equal(pendingRequests.length,1,'another workspace does not refetch this view');
    context.justVoxelSelectionGeneration[workspace]++;
    location.search = '?server=jv-second1';
    location.href = location.origin + '/' + location.search;
    changeSelection({detail:{workspace}});
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
