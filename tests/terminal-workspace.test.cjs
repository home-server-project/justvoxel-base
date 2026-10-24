const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {test} = require('node:test');
const vm = require('node:vm');
const read = path => readFileSync(`${__dirname}/../${path}`, 'utf8');
const app = read('webui/internal/server/static/app.js');
const lifecycle = app.slice(app.indexOf('function createSystemTerminal('), app.indexOf('const systemWorkspaceOpen ='));

test('native package and fixed on-demand service contract', () => {
  assert.match(read('build_files/packages.env'), /JUSTVOXEL_COMMON_PACKAGES="\\\nttyd \\/);
  const build = read('build_files/build-common.sh');
  assert.match(build, /command -v ttyd/); assert.match(build, /rpm -q ttyd/);
  const unit = read('system_files/usr/lib/systemd/system/justvoxel-terminal.service');
  for (const value of ['User=voxel','Group=voxel','WorkingDirectory=/home/voxel','UMask=0077','RuntimeDirectory=justvoxel-terminal','RuntimeDirectoryMode=0700',
    '/usr/bin/ttyd --writable --check-origin --exit-no-conn --interface /run/justvoxel-terminal/ttyd.sock --base-path /terminal --auth-header X-JustVoxel-Terminal /bin/bash --login']) assert.ok(unit.includes(value));
  assert.doesNotMatch(unit, /127\.0\.0\.1:61210|--port 61210/);
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
test('Terminal belongs after Logs; primary administrator restriction and close wiring', () => {
  const header = read('webui/internal/server/templates/header.html');
  assert.match(header, /data-system-tab="logs"[^\n]*\n[^\n]*data-primary-admin-only data-system-tab="terminal"[^\n]*\n[^\n]*data-system-tab="date-time"/);
  assert.match(app, /const primaryAdmin = user => user\?\.username === "voxel" && user\?\.role === "administrator"/);
  assert.match(app, /currentTab === "terminal" && !primaryAdmin\(user\)/);
  assert.match(app, /tab === "terminal" && !primaryAdmin\(user\)\) tab = "history"/);
  assert.match(app, /requested !== "terminal" \|\| primaryAdmin\(user\)/);
  assert.match(app, /onClose: \(\) => \{ \+\+loadSequence; terminal.close\(\)/);
  assert.match(app, /terminal.hide\(\)/);
  assert.match(read('webui/internal/server/static/app.css'), /body:not\(\.primary-administrator\) \.nav-admin-only\[data-primary-admin-only\]/);
  const server = read('webui/internal/server/server.go');
  assert.match(server, /strings.HasPrefix\(r.URL.Path, "\/terminal\/"\)/);
  assert.match(server, /mux.HandleFunc\("GET \/", a.dashboard\)/);
  assert.doesNotMatch(server, /HandleFunc\("\/terminal\//);
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
  const frame=new Element(),state=new Element(),retry=new Element(),pane=new Element();
  pane.querySelector=s=>s==='iframe'?frame:s==='[data-terminal-state]'?state:retry;
  const dialog={open:true,querySelector:()=>pane};
  const calls=[];let now=0,release=null;
  const context={URLSearchParams,Date:{now:()=>now},
    setTimeout:fn=>{now+=1000;fn();},
    fetch:async(url,options)=>{
      calls.push({url,options});
      if(release && url.endsWith('/start'))await release;
      return {ok:!failed,json:async()=>({ready,state:ready?'active':'activating'})};
    }};
  vm.runInNewContext(lifecycle+'\nglobalThis.terminal=createSystemTerminal(dialog,"csrf");',Object.assign(context,{dialog}));
  return {frame,state,retry,pane,dialog,calls,terminal:context.terminal,
    setReady:v=>ready=v,setFailed:v=>failed=v,
    holdStart:()=>{let resolve;release=new Promise(r=>resolve=r);return resolve;},
    async flush(){for(let i=0;i<100;i++)await Promise.resolve();}};
}
test('ready embeds; tab switching keeps the connection; close requests stop', async()=>{
  const b=browser();await b.terminal.show();assert.equal(b.frame.src,'/terminal/');
  b.terminal.hide();assert.equal(b.pane.hidden,true);assert.equal(b.frame.src,'/terminal/');
  await b.terminal.show();assert.equal(b.calls.length,1);
  b.dialog.open=false;b.terminal.close();await b.flush();
  assert.equal(b.frame.src,undefined);assert.ok(b.calls.at(-1).url.endsWith('/stop'));
  assert.equal(b.calls.at(-1).options.body.get('csrf'),'csrf');
});
test('startup waits for readiness and refresh serializes reconnect',async()=>{
  const b=browser({ready:false});const release=b.holdStart();const opened=b.terminal.show();
  await b.flush();assert.equal(b.frame.src,undefined);assert.equal(b.state.textContent,'Starting Terminal…');
  b.setReady(true);release();await opened;assert.equal(b.frame.src,'/terminal/');
  await b.terminal.show(true);assert.equal(b.frame.src,'/terminal/');
  assert.ok(b.calls.some(c=>c.url.endsWith('/stop')));
});
test('error and timeout offer retry; no blank iframe or permanent polling',async()=>{
  const b=browser({failed:true});await b.terminal.show();
  assert.equal(b.frame.src,undefined);assert.equal(b.retry.hidden,false);
  b.setFailed(false);b.retry.click();await b.flush();assert.equal(b.frame.src,'/terminal/');
  const timeout=browser({ready:false});await timeout.terminal.show();
  assert.equal(timeout.frame.src,undefined);assert.equal(timeout.retry.hidden,false);
  assert.ok(timeout.calls.length<=33);
});
test('close during startup prevents a stale iframe and serializes rapid reopen',async()=>{
  const b=browser({ready:false});const release=b.holdStart();const opening=b.terminal.show();await b.flush();
  b.dialog.open=false;b.terminal.close();b.setReady(true);b.dialog.open=true;
  const reopened=b.terminal.show();release();await opening;await reopened;await b.flush();
  assert.equal(b.frame.src,'/terminal/');
  assert.deepEqual(b.calls.filter(c=>c.options.method==='POST').map(c=>c.url.split('/').at(-1)),['start','stop']);
});
