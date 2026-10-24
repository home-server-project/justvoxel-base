const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {test} = require('node:test');
const vm = require('node:vm');
const read = path => readFileSync(`${__dirname}/../${path}`, 'utf8');
const app = read('webui/internal/server/static/app.js');
const lifecycle = app.slice(app.indexOf('function validateTerminalAppearance('), app.indexOf('const terminalDialog ='));

test('native package and fixed on-demand service contract', () => {
  assert.match(read('build_files/packages.env'), /JUSTVOXEL_COMMON_PACKAGES="\\\nttyd \\/);
  const build = read('build_files/build-common.sh');
  assert.match(build, /command -v ttyd/); assert.match(build, /rpm -q ttyd/);
  const unit = read('system_files/usr/lib/systemd/system/justvoxel-terminal.service');
  for (const value of ['User=voxel','Group=voxel','WorkingDirectory=/home/voxel','UMask=0077','RuntimeDirectory=justvoxel-terminal','RuntimeDirectoryMode=0700',
    '/usr/bin/ttyd --writable --check-origin --exit-no-conn --interface /run/justvoxel-terminal/ttyd.sock --base-path /terminal --auth-header X-JustVoxel-Terminal -t disableLeaveAlert=true /bin/bash --login']) assert.ok(unit.includes(value));
  assert.doesNotMatch(unit, /127\.0\.0\.1:61210|--port|--interface (?!\/run\/justvoxel-terminal\/ttyd\.sock)|disableReconnect/);
  assert.deepEqual([...unit.matchAll(/(?:^| )-t (\S+)/gm)].map(match => match[1]), ['disableLeaveAlert=true']);
  assert.doesNotMatch(unit, /WantedBy|\[Install\]|NoNewPrivileges|sudo|ssh|podman|nsenter|chroot|--url-arg|--privileged/);
  assert.doesNotMatch(build, /enable[^\n]*justvoxel-terminal/);
});
test('WebUI delegates Terminal exclusively to the authenticated Management API', () => {
  for (const file of ['webui/internal/server/terminal.go', 'webui/internal/api/terminal.go']) {
    const source = read(file);
    assert.doesNotMatch(source, /127\.0\.0\.1:61210|\/run\/justvoxel-terminal\/ttyd\.sock/);
    assert.doesNotMatch(source, /Header\.Set\("X-JustVoxel-Terminal"/);
  }
  const client = read('webui/internal/api/terminal.go');
  assert.match(client, /Transport: c.http.Transport/);
  assert.match(client, /\/v1\/admin\/terminal\/proxy\//);
});
const header = read('webui/internal/server/templates/header.html');
const systemMarkup = header.match(/<dialog[^>]*data-system-workspace-dialog[\s\S]*?<\/dialog>/)[0];
const terminalMarkup = header.match(/<dialog[^>]*data-terminal-workspace-dialog[\s\S]*?<\/dialog>/)[0];
const systemScript = app.slice(app.indexOf('const systemWorkspaceOpen ='), app.indexOf('// Plugin workspace.'));
const workspaceScript = app.slice(app.indexOf('const terminalDialog ='), app.indexOf('const systemWorkspaceOpen ='));
test('Terminal is the fifth SYSTEM tile with primary administrator visibility', () => {
  const section = header.match(/<section class="control-section">\s*<span class="control-section-label">System<\/span>[\s\S]*?<\/section>/)[0];
  assert.deepEqual([...section.matchAll(/<strong>(.*?)<\/strong>/g)].map(match => match[1]),
    ['System', 'Network', 'System Monitor', 'System Update', 'Terminal']);
  const tile = section.match(/<button[^>]*data-terminal-open[^>]*>[\s\S]*?<\/button>/)[0];
  assert.match(tile, /class="control-tile nav-admin-only"/);
  assert.match(tile, /data-primary-admin-only/);
  assert.match(tile, /title="Open a command-line session on JustVoxel"/);
  assert.doesNotMatch(tile, /<svg|<img/);
  assert.match(read('webui/internal/server/static/app.css'), /body:not\(\.primary-administrator\) \.nav-admin-only\[data-primary-admin-only\]/);
  assert.match(app, /username === "voxel" && role === "administrator"/);
});
test('System retains its other tabs and has no Terminal markup or lifecycle', () => {
  assert.doesNotMatch(systemMarkup, /data-system-tab="terminal"|data-system-terminal|data-terminal-frame/);
  assert.deepEqual([...systemMarkup.matchAll(/data-system-tab="([^"]+)"/g)].map(match => match[1]),
    ['health', 'history', 'users', 'security', 'logs', 'date-time', 'wallpaper', 'reset', 'ups', 'about']);
  assert.doesNotMatch(systemScript, /createSystemTerminal|createTerminalWorkspace\s*\(/);
  assert.doesNotMatch(systemScript, /\bterminal\.(?:close|show|hide)\s*\(/);
  assert.doesNotMatch(systemScript, /data-system-terminal|data-terminal-frame|data-system-tab="terminal"/);
  assert.doesNotMatch(systemScript, /\b(?:currentTab|tab)\s*(?:===|!==)\s*["']terminal["']/);
  assert.match(systemScript, /refreshButton\?\.addEventListener\("click", \(\) => loadCurrentSystemTab\(\)\)/);
  assert.doesNotMatch(read('webui/internal/server/static/app.css'), /system-terminal-pane/);
});
test('standalone Terminal uses the shared header, Settings, Refresh and SVG Close, and state/retry/frame body', () => {
  assert.match(terminalMarkup, /data-workspace-window="terminal"/);
  assert.match(terminalMarkup, /class="system-workspace-shell"/);
  assert.match(terminalMarkup, /class="system-workspace-header" data-workspace-drag-handle/);
  assert.match(terminalMarkup, /<p class="eyebrow">JustVoxel<\/p>\s*<h2 id="terminal-workspace-title">Terminal<\/h2>/);
  const buttons = [...terminalMarkup.matchAll(/<button[^>]*>[\s\S]*?<\/button>/g)].map(match => match[0]);
  assert.equal(buttons.length, 4);
  assert.match(buttons[0], /class="system-monitor-header-button system-monitor-settings-button"[^>]*data-terminal-settings-toggle aria-haspopup="dialog">Settings<\/button>/);
  assert.match(buttons[1], /workspace-header-refresh[^>]*data-terminal-refresh[^>]*>Refresh<\/button>/);
  assert.match(buttons[2], /workspace-header-close[^>]*data-terminal-close aria-label="Close Terminal"><svg/);
  assert.ok(buttons[2].includes('<path d="M5 5l14 14M19 5L5 19"/>'));
  assert.match(buttons[3], /data-terminal-retry hidden>Retry/);
  assert.match(terminalMarkup, /data-terminal-state/);
  assert.match(terminalMarkup, /<iframe title="JustVoxel Terminal" data-terminal-frame hidden><\/iframe>/);
  assert.doesNotMatch(terminalMarkup, /<nav|data-system-tab/);
});
test('Terminal keeps existing proxy routing and primary administrator backend authorization', () => {
  const server = read('webui/internal/server/server.go');
  assert.match(server, /strings.HasPrefix\(r.URL.Path, "\/terminal\/"\)/);
  assert.match(server, /mux.HandleFunc\("GET \/", a.dashboard\)/);
  assert.doesNotMatch(server, /HandleFunc\("\/terminal\//);
  assert.match(read('management/cmd/justvoxel-management-agent/terminal.go'), /requirePrimaryAdministrator/);
  assert.match(read('webui/internal/server/terminal.go'), /a\.fileBrowserIdentity\(w, r\)/);
  assert.match(read('webui/internal/server/filebrowser.go'), /identity\.Role != "administrator" \|\| identity\.Username != "voxel"/);
});
class Element {
  constructor(){this.hidden=false;this.events={};this.attrs={};this.textContent='';}
  getAttribute(name){return this.attrs[name] || null;}
  removeAttribute(name){delete this.attrs[name];}
  set src(value){this.attrs.src=value;} get src(){return this.attrs.src;}
  addEventListener(type,fn){this.events[type]=fn;}
  click(){this.events.click?.();}
}
function appearanceStorage({stored=null,readFails=false,writeFails=false}={}) {
  return {
    value:stored, writes:[],
    getItem(key){
      assert.equal(key,'justvoxel.terminal.appearance');
      if(readFails) throw new Error('storage unavailable');
      return this.value;
    },
    setItem(key,value){
      assert.equal(key,'justvoxel.terminal.appearance');
      if(writeFails) throw new Error('storage unavailable');
      this.writes.push({key,value});this.value=value;
    },
    removeItem(key){
      assert.equal(key,'justvoxel.terminal.appearance');
      if(writeFails) throw new Error('storage unavailable');
      this.writes.push({key,value:null});this.value=null;
    },
  };
}
function browser({ready=true,failed=false,stored=null,readFails=false,writeFails=false}={}) {
  const frame=new Element(),state=new Element(),retry=new Element();
  const dialog={open:true,querySelector:s=>s==='[data-terminal-frame]'?frame:s==='[data-terminal-state]'?state:retry};
  const calls=[];let now=0,release=null;
  const storage=appearanceStorage({stored,readFails,writeFails});
  const context={URLSearchParams,localStorage:storage,Date:{now:()=>now},
    setTimeout:fn=>{now+=1000;fn();},
    fetch:async(url,options)=>{
      calls.push({url,options});
      if(release && url.endsWith('/start'))await release;
      return {ok:!failed,json:async()=>({ready,state:ready?'active':'activating'})};
    }};
  vm.runInNewContext(lifecycle+'\nglobalThis.terminal=createTerminalWorkspace(dialog,"csrf");',Object.assign(context,{dialog}));
  return {frame,state,retry,dialog,calls,storage,terminal:context.terminal,
    setReady:v=>ready=v,setFailed:v=>failed=v,
    holdStart:()=>{let resolve;release=new Promise(r=>resolve=r);return resolve;},
    async flush(){for(let i=0;i<100;i++)await Promise.resolve();}};
}
test('ready embeds; close disconnects and requests managed stop', async()=>{
  const b=browser();await b.terminal.show();assert.equal(b.frame.src,'/terminal/');
  await b.terminal.show();assert.equal(b.calls.length,1);
  b.dialog.open=false;b.terminal.close();await b.flush();
  assert.equal(b.frame.src,undefined);assert.ok(b.calls.at(-1).url.endsWith('/stop'));
  assert.equal(b.calls.at(-1).options.body.get('csrf'),'csrf');
});
test('startup waits for readiness and refresh serializes reconnect',async()=>{
  const b=browser({ready:false});const release=b.holdStart();const opened=b.terminal.show();
  await b.flush();assert.equal(b.frame.src,undefined);assert.equal(b.state.textContent,'Starting Terminal…');
  b.setReady(true);release();await opened;assert.equal(b.frame.src,'/terminal/');
  const reconnect=b.terminal.show(true);assert.equal(b.frame.src,undefined);assert.equal(b.frame.hidden,true);
  await reconnect;assert.equal(b.frame.src,'/terminal/');
  assert.deepEqual(b.calls.map(c=>c.url.split('/').at(-1)),['terminal','start','terminal','stop','terminal']);
});
test('error and timeout offer retry; no blank iframe or permanent polling',async()=>{
  const b=browser({failed:true});await b.terminal.show();
  assert.equal(b.frame.src,undefined);assert.equal(b.retry.hidden,false);
  b.setFailed(false);b.retry.click();await b.flush();assert.equal(b.frame.src,'/terminal/');
  const timeout=browser({ready:false});await timeout.terminal.show();
  assert.equal(timeout.frame.src,undefined);assert.equal(timeout.retry.hidden,false);
  assert.ok(timeout.calls.length<=33);
});
test('failed refresh disconnects the old frame and offers Retry', async () => {
  const b=browser();
  await b.terminal.show();
  b.setFailed(true);
  await b.terminal.show(true);
  assert.equal(b.frame.src,undefined);
  assert.equal(b.frame.hidden,true);
  assert.equal(b.retry.hidden,false);
  b.setFailed(false);
  b.retry.click();
  await b.flush();
  assert.equal(b.frame.src,'/terminal/');
});
test('close during startup prevents a stale iframe and serializes rapid reopen',async()=>{
  const b=browser({ready:false});const release=b.holdStart();const opening=b.terminal.show();await b.flush();
  b.dialog.open=false;b.terminal.close();b.setReady(true);b.dialog.open=true;
  const reopened=b.terminal.show();release();await opening;await reopened;await b.flush();
  assert.equal(b.frame.src,'/terminal/');
  assert.deepEqual(b.calls.filter(c=>c.options.method==='POST').map(c=>c.url.split('/').at(-1)),['start','stop']);
});

test('close during startup cannot attach an iframe after completion', async () => {
  const b = browser({ready:false});
  const release = b.holdStart();
  const opening = b.terminal.show();
  await b.flush();
  b.dialog.open = false;
  b.terminal.close();
  b.setReady(true);
  release();
  await opening;
  await b.flush();
  assert.equal(b.frame.src, undefined);
  assert.equal(b.frame.hidden, true);
  assert.equal(b.calls.at(-1).url.split('/').at(-1), 'stop');
});
test('closing an untouched Terminal workspace does not stop a service', async () => {
  const b = browser();
  b.terminal.close();
  await b.flush();
  assert.equal(b.calls.length, 0);
});

function workspaceBrowser({username='voxel',role='administrator',pending=false,savedOpen=false,stored=null,readFails=false,writeFails=false,ready=true}={}) {
  const elements = new Map();
  for (const name of ['frame','state','retry','open','close','refresh','settings-toggle','font-size','background','settings-error','settings-reset','settings-apply']) elements.set(`[data-terminal-${name}]`, new Element());
  const dialog = {open:false,querySelector:selector=>elements.get(selector)};
  const settings = {open:false,querySelector:selector=>elements.get(selector),
    showModal(){this.open=true;},close(){this.open=false;},cancel(){this.open=false;}};
  const storage=appearanceStorage({stored,readFails,writeFails});
  const classes = new Set(pending ? ['role-pending'] : []);
  const applyIdentity = () => {
    classes.delete('role-pending');
    classes.add(`role-${role}`);
    if (username === 'voxel' && role === 'administrator') classes.add('primary-administrator');
  };
  if (!pending) applyIdentity();
  let observer, queuedClose;
  const calls = [], controlCenter = {open:true};
  const context = {
    URLSearchParams, Date, setTimeout:fn=>fn(), localStorage:storage,
    controlCenter,
    document: {
      body:{classList:{contains:name=>classes.has(name)}},
      querySelector:selector=>selector==='[data-terminal-workspace-dialog]' ? dialog :
        selector==='[data-terminal-settings-dialog]' ? settings :
        selector==='[data-system-workspace-csrf]' ? {value:'csrf'} : elements.get(selector),
    },
    fetch:async(url,options)=>{
      calls.push({url,options});
      if(url.endsWith('/start')) ready=true;
      return {ok:true,json:async()=>({ready,state:ready?'active':'activating'})};
    },
    setupWorkspaceWindow:(element,options)=>{
      assert.equal(element, dialog);
      return {
        open:()=>{dialog.open=true;options.onOpen();},
        close:()=>{dialog.open=false;queuedClose=()=>options.onClose();},
      };
    },
    readWorkspaceWindowState:id=>{assert.equal(id,'terminal');return {open:savedOpen};},
    MutationObserver:class {
      constructor(callback){observer=callback;}
      observe(){}
      disconnect(){observer=null;}
    },
  };
  vm.runInNewContext(lifecycle+workspaceScript,context);
  return {
    elements,dialog,settings,storage,calls,controlCenter,
    setReady:value=>ready=value,
    resolveIdentity(){applyIdentity();observer?.();},
    dispatchClose(){const close=queuedClose;queuedClose=null;close?.();},
    async flush(){for(let i=0;i<100;i++)await Promise.resolve();},
  };
}
test('tile opens Terminal and closes Control Center; Refresh stops/reconnects; X immediately disconnects', async () => {
  const b=workspaceBrowser();
  b.elements.get('[data-terminal-open]').click();
  assert.equal(b.dialog.open,true);
  assert.equal(b.controlCenter.open,false);
  await b.flush();
  const frame=b.elements.get('[data-terminal-frame]');
  assert.equal(frame.src,'/terminal/');
  b.elements.get('[data-terminal-refresh]').click();
  assert.equal(frame.src,undefined);
  await b.flush();
  assert.equal(frame.src,'/terminal/');
  assert.deepEqual(b.calls.map(call=>call.url.split('/').at(-1)),['terminal','stop','terminal']);
  b.elements.get('[data-terminal-close]').click();
  assert.equal(b.dialog.open,false);
  assert.equal(frame.src,undefined);
  b.dispatchClose();
  await b.flush();
  assert.equal(b.calls.filter(call=>call.url.endsWith('/stop')).length,2);
});
test('delayed native close event cannot stop a rapid reopened Terminal', async () => {
  const b=workspaceBrowser();
  b.elements.get('[data-terminal-open]').click();
  await b.flush();
  b.elements.get('[data-terminal-close]').click();
  b.elements.get('[data-terminal-open]').click();
  b.dispatchClose();
  await b.flush();
  assert.equal(b.dialog.open,true);
  assert.equal(b.elements.get('[data-terminal-frame]').src,'/terminal/');
  assert.deepEqual(b.calls.map(call=>call.url.split('/').at(-1)),['terminal','stop','terminal']);
});
test('restore waits for identity and only the primary voxel administrator can restore or open', async () => {
  for (const [username,role] of [['voxel','administrator'],['secondary','administrator'],['voxel','operator'],['voxel','viewer'],['secondary','operator'],['secondary','viewer']]) {
    for (const pending of [false,true]) {
      const b=workspaceBrowser({username,role,pending,savedOpen:true});
      if (pending) {
        assert.equal(b.dialog.open,false);
        assert.equal(b.calls.length,0);
        b.elements.get('[data-terminal-open]').click();
        assert.equal(b.dialog.open,false);
        b.resolveIdentity();
      }
      await b.flush();
      const allowed=username==='voxel' && role==='administrator';
      assert.equal(b.dialog.open,allowed,`${username}/${role}, pending=${pending}`);
      b.elements.get('[data-terminal-open]').click();
      await b.flush();
      assert.equal(b.dialog.open,allowed);
      if (!allowed) assert.equal(b.calls.length,0);
    }
  }
});


test('Terminal appearance uses a separate compact dialog with exactly the approved options and actions', () => {
  const markup=header.match(/<dialog[^>]*data-terminal-settings-dialog[\s\S]*?<\/dialog>/)[0];
  assert.match(markup, /class="system-action-dialog"/);
  assert.match(markup, /<p class="eyebrow">Terminal<\/p>/);
  assert.match(markup, /<h2 id="terminal-settings-title">Terminal appearance<\/h2>/);
  const selects=[...markup.matchAll(/<label>([^<]+)\s*<select[^>]*>([\s\S]*?)<\/select>\s*<\/label>/g)];
  assert.deepEqual(selects.map(match=>match[1].trim()), ['Font size','Background']);
  const options=select=>[...select.matchAll(/<option value="([^"]*)">([^<]+)<\/option>/g)].map(match=>[match[1],match[2]]);
  assert.deepEqual(options(selects[0][2]), [['','Default'],['12','12'],['14','14'],['16','16'],['18','18'],['20','20'],['22','22']]);
  assert.deepEqual(options(selects[1][2]), [['','Default'],['black','Black'],['dark-gray','Dark gray']]);
  assert.equal([...markup.matchAll(/<select\b/g)].length,2);
  assert.match(markup, /class="muted compact">Applying appearance settings reconnects the Terminal\.<\/p>/);
  const buttons=[...markup.matchAll(/<button[^>]*>[\s\S]*?<\/button>/g)].map(match=>match[0]);
  assert.equal(buttons.length,2);
  assert.match(buttons[0], /class="secondary"[^>]*data-terminal-settings-reset>Reset defaults<\/button>/);
  assert.match(buttons[1], /data-terminal-settings-apply>Apply<\/button>/);
});

test('validated browser appearance builds only the approved native ttyd query options', async () => {
  for(const fontSize of ['', '12','14','16','18','20','22']) {
    for(const background of ['', 'black','dark-gray']) {
      const b=browser({stored:JSON.stringify({fontSize,background,command:'ignored',theme:{foreground:'red'}})});
      await b.terminal.show();
      if(!fontSize && !background) assert.equal(b.frame.src,'/terminal/');
      const url=new URL(b.frame.src,'https://justvoxel.test');
      assert.equal(url.pathname,'/terminal/');
      assert.equal(url.searchParams.get('fontSize'),fontSize || null);
      assert.equal(url.searchParams.get('theme'),background ? JSON.stringify({background:background==='black'?'#000000':'#202124'}) : null);
      assert.deepEqual([...url.searchParams.keys()], [...(fontSize?['fontSize']:[]),...(background?['theme']:[])]);
      if(background) assert.ok(b.frame.src.includes('theme=%7B%22background%22%3A%22%23'));
    }
  }
});

test('malformed, unknown or unreadable stored appearance falls back independently to defaults', async () => {
  const cases=[
    [null,'/terminal/'], ['{','/terminal/'], ['null','/terminal/'], ['[]','/terminal/'], ['42','/terminal/'],
    [JSON.stringify({fontSize:18,background:'pink'}),'/terminal/'],
    [JSON.stringify({fontSize:'99',background:'black'}),'/terminal/?theme=%7B%22background%22%3A%22%23000000%22%7D'],
    [JSON.stringify({fontSize:'18',background:'unknown'}),'/terminal/?fontSize=18'],
  ];
  for(const [stored,expected] of cases) {
    const b=browser({stored});await b.terminal.show();assert.equal(b.frame.src,expected);
  }
  const b=browser({stored:JSON.stringify({fontSize:'22',background:'black'}),readFails:true});
  await b.terminal.show();assert.equal(b.frame.src,'/terminal/');
});

const terminalElement=(b,name)=>b.elements.get(`[data-terminal-${name}]`);
async function openTerminalSettings(b) {
  terminalElement(b,'open').click();await b.flush();
  terminalElement(b,'settings-toggle').click();
}

test('opening, editing, cancelling and resetting appearance leave the existing session connected', async () => {
  const stored=JSON.stringify({fontSize:'18',background:'dark-gray'});
  const b=workspaceBrowser({stored});await openTerminalSettings(b);
  assert.equal(b.settings.open,true);
  assert.equal(terminalElement(b,'font-size').value,'18');
  assert.equal(terminalElement(b,'background').value,'dark-gray');
  const src=terminalElement(b,'frame').src, calls=b.calls.length;
  assert.equal(calls,1);
  assert.ok(src.includes('fontSize=18'));
  terminalElement(b,'settings-reset').click();
  assert.equal(terminalElement(b,'font-size').value,'');
  assert.equal(terminalElement(b,'background').value,'');
  assert.equal(b.storage.value,stored);
  assert.equal(b.storage.writes.length,0);
  assert.equal(terminalElement(b,'frame').src,src);
  assert.equal(b.calls.length,calls);
  b.settings.cancel();await b.flush();
  assert.equal(terminalElement(b,'frame').src,src);
  assert.equal(b.calls.length,calls);
  assert.equal(b.storage.value,stored);
  terminalElement(b,'settings-toggle').click();
  assert.equal(terminalElement(b,'font-size').value,'18');
  assert.equal(terminalElement(b,'background').value,'dark-gray');
  terminalElement(b,'font-size').value='22';b.settings.close();
  assert.equal(terminalElement(b,'frame').src,src);
  assert.equal(b.storage.value,stored);
  assert.equal(b.calls.length,calls);
});

test('Apply persists only approved enums, closes Settings and uses managed stop/start/readiness/reconnect', async () => {
  const b=workspaceBrowser();await openTerminalSettings(b);
  terminalElement(b,'font-size').value='18';terminalElement(b,'background').value='black';
  // Force readiness/start after stop, using the same lifecycle as normal Refresh.
  b.setReady(false);
  const initialCalls=b.calls.length;
  terminalElement(b,'settings-apply').click();
  assert.deepEqual(JSON.parse(b.storage.value),{fontSize:'18',background:'black'});
  assert.equal(b.settings.open,false);
  assert.equal(terminalElement(b,'frame').src,undefined);
  await b.flush();
  assert.deepEqual(b.calls.slice(initialCalls).map(call=>call.url.split('/').at(-1)),['stop','terminal','start','terminal']);
  assert.equal(terminalElement(b,'frame').src,'/terminal/?fontSize=18&theme=%7B%22background%22%3A%22%23000000%22%7D');
  terminalElement(b,'refresh').click();assert.equal(terminalElement(b,'frame').src,undefined);
  await b.flush();assert.ok(terminalElement(b,'frame').src.includes('fontSize=18'));
  terminalElement(b,'settings-toggle').click();terminalElement(b,'settings-reset').click();
  terminalElement(b,'settings-apply').click();await b.flush();
  assert.equal(b.storage.value,null);
  assert.deepEqual(b.storage.writes.at(-1),{key:'justvoxel.terminal.appearance',value:null});
  assert.equal(terminalElement(b,'frame').src,'/terminal/');
});

test('Apply validates each field before persisting and ignores unknown options', async () => {
  for(const [fontSize,background,expected] of [
    ['bogus','black',{fontSize:'',background:'black'}],
    ['20','bogus',{fontSize:'20',background:''}],
    [18,{background:'#ff0000'},null],
  ]) {
    const b=workspaceBrowser();await openTerminalSettings(b);
    terminalElement(b,'font-size').value=fontSize;terminalElement(b,'background').value=background;
    terminalElement(b,'settings-apply').click();await b.flush();
    assert.deepEqual(JSON.parse(b.storage.value),expected);
  }
});

test('storage write or removal failures preserve the connected Terminal and allow retry', async () => {
  for(const reset of [false,true]) {
    const stored=JSON.stringify({fontSize:'14',background:'black'});
    const b=workspaceBrowser({stored,writeFails:true});await openTerminalSettings(b);
    const src=terminalElement(b,'frame').src,calls=b.calls.length;
    if(reset) terminalElement(b,'settings-reset').click();
    else terminalElement(b,'font-size').value='22';
    terminalElement(b,'settings-apply').click();await b.flush();
    assert.equal(b.settings.open,true);
    assert.equal(terminalElement(b,'settings-error').hidden,false);
    assert.equal(terminalElement(b,'frame').src,src);
    assert.equal(b.calls.length,calls);
    assert.equal(b.storage.value,stored);
  }
});
