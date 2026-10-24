const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {test} = require('node:test');
const vm = require('node:vm');
const read = path => readFileSync(`${__dirname}/../${path}`, 'utf8');
const app = read('webui/internal/server/static/app.js');
const lifecycle = app.slice(app.indexOf('function createTerminalWorkspace('), app.indexOf('const terminalDialog ='));

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
test('standalone Terminal uses the shared header, only Refresh and SVG Close, and state/retry/frame body', () => {
  assert.match(terminalMarkup, /data-workspace-window="terminal"/);
  assert.match(terminalMarkup, /class="system-workspace-shell"/);
  assert.match(terminalMarkup, /class="system-workspace-header" data-workspace-drag-handle/);
  assert.match(terminalMarkup, /<p class="eyebrow">JustVoxel<\/p>\s*<h2 id="terminal-workspace-title">Terminal<\/h2>/);
  const buttons = [...terminalMarkup.matchAll(/<button[^>]*>[\s\S]*?<\/button>/g)].map(match => match[0]);
  assert.equal(buttons.length, 3);
  assert.match(buttons[0], /workspace-header-refresh[^>]*data-terminal-refresh[^>]*>Refresh<\/button>/);
  assert.match(buttons[1], /workspace-header-close[^>]*data-terminal-close aria-label="Close Terminal"><svg/);
  assert.ok(buttons[1].includes('<path d="M5 5l14 14M19 5L5 19"/>'));
  assert.match(buttons[2], /data-terminal-retry hidden>Retry/);
  assert.match(terminalMarkup, /data-terminal-state/);
  assert.match(terminalMarkup, /<iframe title="JustVoxel Terminal" data-terminal-frame hidden><\/iframe>/);
  assert.doesNotMatch(terminalMarkup, /Settings|<nav|data-system-tab/);
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
function browser({ready=true,failed=false}={}) {
  const frame=new Element(),state=new Element(),retry=new Element();
  const dialog={open:true,querySelector:s=>s==='[data-terminal-frame]'?frame:s==='[data-terminal-state]'?state:retry};
  const calls=[];let now=0,release=null;
  const context={URLSearchParams,Date:{now:()=>now},
    setTimeout:fn=>{now+=1000;fn();},
    fetch:async(url,options)=>{
      calls.push({url,options});
      if(release && url.endsWith('/start'))await release;
      return {ok:!failed,json:async()=>({ready,state:ready?'active':'activating'})};
    }};
  vm.runInNewContext(lifecycle+'\nglobalThis.terminal=createTerminalWorkspace(dialog,"csrf");',Object.assign(context,{dialog}));
  return {frame,state,retry,dialog,calls,terminal:context.terminal,
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

function workspaceBrowser({username='voxel',role='administrator',pending=false,savedOpen=false}={}) {
  const elements = new Map();
  for (const name of ['frame','state','retry','open','close','refresh']) elements.set(`[data-terminal-${name}]`, new Element());
  const dialog = {open:false,querySelector:selector=>elements.get(selector)};
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
    URLSearchParams, Date, setTimeout,
    controlCenter,
    document: {
      body:{classList:{contains:name=>classes.has(name)}},
      querySelector:selector=>selector==='[data-terminal-workspace-dialog]' ? dialog :
        selector==='[data-system-workspace-csrf]' ? {value:'csrf'} : elements.get(selector),
    },
    fetch:async(url,options)=>{
      calls.push({url,options});
      return {ok:true,json:async()=>({ready:true,state:'active'})};
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
    elements,dialog,calls,controlCenter,
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
