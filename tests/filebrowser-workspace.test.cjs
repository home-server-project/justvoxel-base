const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {test} = require('node:test');
const vm = require('node:vm');
const read = path => readFileSync(`${__dirname}/../${path}`, 'utf8');
const header = read('webui/internal/server/templates/header.html');
const app = read('webui/internal/server/static/app.js');
const filesJS = app.slice(app.indexOf('// Files owns only its lifecycle;'));
const manager = read('management/cmd/justvoxel-management-agent/filebrowser.go');
const container = read('management/cmd/justvoxel-management-agent/filebrowser_container.go');
const proxy = read('webui/internal/server/filebrowser.go');
const config = JSON.parse(read('system_files/usr/share/justvoxel/filebrowser/config.yaml'));

test('Files follows Migration in the existing two-column Storage & Data grid', () => {
  const section = header.match(/<span class="control-section-label">Storage &amp; Data<\/span>([\s\S]*?)<\/section>/)[1];
  assert.deepEqual([...section.matchAll(/data-(storage|backups|migration|files)-open/g)].map(m=>m[1]), ['storage','backups','migration','files']);
  assert.match(section, /control-tile nav-admin-only[^>]*data-files-open/);
  assert.match(section, /control-tile-grid/);
});
test('Files uses shared workspace header controls in approved order', () => {
  const workspace = header.match(/<dialog[^>]*data-workspace-window="files"[\s\S]*?<\/dialog>/)[0];
  assert.deepEqual([...workspace.matchAll(/data-files-(settings-toggle|refresh|close)/g)].map(m=>m[1]), ['settings-toggle','refresh','close']);
  assert.match(workspace, /system-monitor-header-button[^>]*data-files-settings-toggle[^>]*>Settings<\/button>/);
  assert.match(workspace, /workspace-header-refresh[^>]*data-files-refresh/);
  assert.match(workspace, /workspace-header-close[^>]*data-files-close[^>]*aria-label="Close Files"><svg[^>]*[\s\S]*?aria-hidden="true"/);
  assert.doesNotMatch(workspace,/system-monitor-profile|date-time-automatic|date-time-switch|data-files-mode|data-files-settings-dialog/);
  assert.match(workspace,/data-files-frame/);
  assert.equal((header.match(/data-files-csrf/g)||[]).length,1);
});
test('File Browser settings is a separate compact dialog with one ordered availability enum', () => {
  const settings = header.match(/<dialog class="system-action-dialog"[^>]*data-files-settings-dialog[\s\S]*?<\/dialog>/)[0];
  assert.match(settings,/class="system-action-dialog-content"/);
  assert.doesNotMatch(settings,/data-workspace-window|<dialog[\s\S]*<dialog|system-monitor-profile|date-time-automatic|date-time-switch/);
  for (const text of ['Files','File Browser settings','Availability','On demand','Starts when Files is opened and stops when Files is closed.','Always available','Starts with JustVoxel and remains available in the background.','Close']) assert.ok(settings.includes(text));
  assert.deepEqual([...settings.matchAll(/type="radio" name="([^"]+)" value="([^"]+)"/g)].map(m=>[m[1],m[2]]),[['filebrowser-availability','on-demand'],['filebrowser-availability','always']]);
  assert.equal((header.match(/data-files-mode\b/g)||[]).length,2);
  assert.match(settings,/value="on-demand" data-files-mode checked/);
  assert.equal((settings.match(/<button\b/g)||[]).length,1);
  assert.match(settings,/class="action-row"[\s\S]*class="secondary"[^>]*data-files-settings-dismiss>Close/);
  // The modal follows the complete workspace, rather than nesting in it.
  assert.match(header, /data-workspace-window="files"(?:(?!<\/dialog>)[\s\S])*<\/dialog>\s*<dialog[^>]*data-files-settings-dialog/);
});
test('one persisted enum defaults to on-demand; lifecycle preserves open workspace', () => {
  const store = read('management/cmd/justvoxel-management-agent/webui_store.go');
  assert.match(store,/availability IN \('always', 'on-demand'\)/);
  assert.match(store,/INSERT INTO filebrowser_settings\(id, availability\) VALUES\(1, 'on-demand'\)/);
  assert.match(manager,/action := "disable"/);
  assert.match(manager,/action = "enable"/);
  assert.match(manager,/mode == "always"[\s\S]*?startFileBrowser\(ctx\)/);
  assert.match(manager,/if err != nil \|\| mode == "always"/);
  assert.match(manager,/fileBrowserCommand\(ctx, "systemctl", "stop", fileBrowserUnit\)/);
  assert.match(manager,/requirePrimaryAdministrator/);
});
test('container image, persistence, isolated listener and mounts follow the contract', () => {
  assert.match(container,/"ghcr.io\/gtsteffaniak\/filebrowser:stable-slim"/);
  assert.match(container,/127\.0\.0\.1:61209:8080/);
  assert.match(container,/source=justvoxel-filebrowser-state,target=\/home\/filebrowser\/data/);
  assert.match(container,/--cap-drop=ALL/);
  assert.match(container,/--user", "1000:1000/);
  assert.match(container,/no-new-privileges/);
  assert.match(container,/label=disable/);
  assert.match(container,/bind-nonrecursive=true/);
  assert.match(container,/record.Storage.DataPath/);
  assert.match(container,/record.Backups.Root/);
  assert.match(container,/"private":\s*true/);
  assert.match(container,/"ignoreSymlinks":\s*true/);
  for (const forbidden of [/:[zZ]\b/,/\bchcon\b/,/\bsemanage\b/,/\brestorecon\b/,/firewall-cmd/,/--privileged/,/--network=host/,/digest/]) assert.doesNotMatch(container,forbidden);
  const unit = read('system_files/usr/lib/systemd/system/justvoxel-filebrowser.service');
  assert.match(unit,/WantedBy=multi-user.target/);
  assert.match(unit,/management-agent filebrowser-run/);
  assert.equal(config.server.database.path,'/home/filebrowser/data/database.sqlite');
});
test('supported proxy auth disables second login and internal settings', () => {
  assert.equal(config.http.baseURL,'/files/');
  assert.equal(config.http.trustProxyHeaders,true);
  assert.equal(config.auth.methods.proxy.enabled,true);
  assert.equal(config.auth.methods.proxy.header,'X-JustVoxel-User');
  assert.equal(config.auth.methods.password.enabled,false);
  assert.equal(config.auth.methods.noauth,undefined);
  assert.equal(config.userDefaults.account.loginMethod,'proxy');
  assert.equal(config.userDefaults.account.disableSettings,true);
  assert.equal(config.userDefaults.account.disableUpdateNotifications,true);
  assert.equal(config.server.disableUpdateCheck,true);
  for (const name of ['X-JustVoxel-User','X-Forwarded-User','X-Forwarded-For','X-Forwarded-Host','X-Forwarded-Proto','X-Real-IP']) assert.ok(proxy.includes(`"${name}"`));
  assert.match(proxy,/p.Out.Header.Del\(name\)/);
  assert.match(proxy,/p.Out.Header.Set\("X-JustVoxel-User", identity.Username\)/);
  assert.match(proxy,/identity.MustChange \|\| identity.Role != "administrator"/);
  const global = read('webui/internal/server/server.go');
  assert.match(global,/X-Frame-Options", "DENY"/);
  assert.match(global,/frame-ancestors 'none'/);
  assert.match(proxy,/X-Frame-Options", "SAMEORIGIN"/);
  assert.match(proxy,/frame-ancestors 'self'/);
  assert.match(global,/mux\.HandleFunc\("GET \/", a\.dashboard\)/);
  assert.match(global,/strings\.HasPrefix\(r\.URL\.Path, "\/files\/"\)/);
  assert.match(global,/a\.fileBrowserProxy\(w, r\)/);
  assert.match(global,/mux\.ServeHTTP\(w, r\)/);
  assert.match(global,/return securityHeaders\(handler\)/);
  assert.doesNotMatch(proxy,/mux\.Handle(?:Func)?\(\s*"\/files\/"\s*,/);
});

class Element {
  constructor() { this.events={}; this.hidden=false; this.checked=false; this.value=''; this.textContent=''; this.srcWrites=0; this.srcRemovals=0; this.open=false; }
  set src(value) { this.frameSrc=value; this.srcWrites++; }
  get src() { return this.frameSrc; }
  showModal() { this.open=true; }
  close() { this.open=false; }
  addEventListener(type,fn) { (this.events[type] ||= []).push(fn); }
  setAttribute(name,value) { this[name]=value; }
  removeAttribute(name) { if(name==='src'){this.frameSrc=undefined;this.srcRemovals++;}else delete this[name]; }
  dispatchEvent(event) { return Promise.all((this.events[event.type]||[]).map(fn=>fn(event))); }
  click() { return this.dispatchEvent({type:'click'}); }
}
function browser({availability='on-demand', ready=true, failed=false, failedAvailability=false}={}) {
  const selectors=['frame','state','retry','settings-dialog','settings-toggle','settings-dismiss','settings-status','csrf','close','refresh'];
  const elements=Object.fromEntries(selectors.map(key=>[key,new Element()]));
  elements.csrf.value='csrf';
  const modes=['on-demand','always'].map(value=>Object.assign(new Element(),{value,checked:value==='on-demand'}));
  const dialog=new Element();dialog.open=false;
  dialog.querySelector=selector=>elements[selector.match(/data-files-(.+)\]/)[1]];
  elements['settings-dialog'].querySelector=selector=>elements[selector.match(/data-files-(.+)\]/)[1]];
  elements['settings-dialog'].querySelectorAll=()=>modes;
  const launcher=new Element();const calls=[];let callbacks;
  const context={URLSearchParams, Date, Event:class{constructor(type){this.type=type;}},
    setTimeout:fn=>{fn();return 0;},
    document:{querySelector:selector=>selector==='[data-files-dialog]'?dialog:selector==='[data-files-settings-dialog]'?elements['settings-dialog']:launcher,body:{classList:{contains:role=>role==='primary-administrator'}}},
    controlCenter:{open:true},readWorkspaceWindowState:()=>({open:false}),
    setupWorkspaceWindow:(_,options)=>{callbacks=options;return {open(){dialog.open=true;options.onOpen();},close(){dialog.open=false;options.onClose();}};},
    fetch:async (url, options)=>{
      calls.push({url,options});
      if(url==='/api/files/availability')availability=options.body.get('availability');
      return {ok:!failed && !(failedAvailability && url==='/api/files/availability'),json:async()=>url==='/api/files'?{availability,ready,state:ready?'active':'activating'}:{ok:true}};
    },
  };
  vm.runInNewContext(filesJS,context);
  return {elements,modes,dialog,launcher,calls,setReady(value){ready=value;},setFailed(value){failed=value;},setAvailability(value){availability=value;},setFailedAvailability(value){failedAvailability=value;},async flush(){for(let i=0;i<40;i++)await Promise.resolve();}};
}
test('opening waits for health, refresh reloads iframe without restarting, X requests managed stop',async()=>{
  const b=browser();await b.launcher.click();await b.flush();
  assert.equal(b.elements.frame.src,'/files/');assert.equal(b.elements.frame.hidden,false);
  await b.elements.refresh.click();await b.flush();
  assert.equal(b.elements.frame.src,'/files/');
  assert.equal(b.calls.filter(c=>c.url.endsWith('/start')).length,0);
  await b.elements.close.click();await b.flush();
  assert.equal(b.dialog.open,false);assert.equal(b.elements.frame.src,undefined);
  assert.equal(b.calls.at(-1).url,'/api/files/stop');
});
test('settings select one enum and save one authoritative value',async()=>{
  const b=browser();await b.launcher.click();await b.flush();
  await b.elements['settings-toggle'].click();await b.flush();
  assert.equal(b.elements['settings-dialog'].open,true);
  const saving=b.modes[1].dispatchEvent({type:'change'});
  assert.equal(b.elements['settings-status'].textContent,'Saving…');
  assert.ok(b.modes.every(m=>m.disabled));
  await saving;await b.flush();
  assert.deepEqual(b.modes.map(m=>m.checked),[false,true]);
  assert.equal(b.elements['settings-status'].textContent,'Saved.');
  assert.ok(b.modes.every(m=>!m.disabled));
  const saved=b.calls.find(c=>c.url==='/api/files/availability');
  assert.equal(saved.options.body.get('availability'),'always');
  assert.equal(saved.options.body.get('csrf'),'csrf');
  assert.equal(saved.options.method,'POST');
});
test('opening settings rereads authoritative mode and closing only dismisses the modal',async()=>{
  const b=browser();await b.launcher.click();await b.flush();
  const frame=b.elements.frame;
  const writes=frame.srcWrites,removals=frame.srcRemovals;
  const actions=b.calls.filter(c=>c.options.method==='POST').length;
  for(const mode of ['always','on-demand']){
    b.setAvailability(mode);
    b.elements['settings-status'].textContent='Saved.';
    const reads=b.calls.filter(c=>c.url==='/api/files').length;
    await b.elements['settings-toggle'].click();await b.flush();
    assert.equal(b.elements['settings-dialog'].open,true);
    assert.equal(b.elements['settings-status'].textContent,'');
    assert.equal(b.calls.filter(c=>c.url==='/api/files').length,reads+1);
    assert.deepEqual(b.modes.map(m=>m.checked),[mode==='on-demand',mode==='always']);
    await b.elements['settings-dismiss'].click();await b.flush();
    assert.equal(b.elements['settings-dialog'].open,false);
    assert.equal(b.dialog.open,true);
    assert.equal(frame.src,'/files/');assert.equal(frame.hidden,false);
    assert.equal(frame.srcWrites,writes);assert.equal(frame.srcRemovals,removals);
    assert.equal(b.calls.filter(c=>c.options.method==='POST').length,actions);
  }
});
test('availability failure rereads persisted mode and the selected radio can retry',async()=>{
  const b=browser({failedAvailability:true});await b.launcher.click();await b.flush();
  await b.elements['settings-toggle'].click();await b.flush();
  await b.modes[1].dispatchEvent({type:'change'});await b.flush();
  assert.equal(b.calls.at(-1).url,'/api/files');
  assert.deepEqual(b.modes.map(m=>m.checked),[false,true]);
  assert.equal(b.elements['settings-status'].textContent,'Availability could not be applied. Select the mode again to retry.');
  assert.ok(b.modes.every(m=>!m.disabled));
  b.setFailedAvailability(false);await b.modes[1].click();await b.flush();
  assert.equal(b.calls.filter(c=>c.url==='/api/files/availability').length,2);
  assert.equal(b.elements['settings-status'].textContent,'Saved.');
  assert.equal(b.elements.frame.src,'/files/');
  assert.equal(b.dialog.open,true);
});
test('starting never embeds the browser until health succeeds',async()=>{
  const b=browser({ready:false});await b.launcher.click();
  // Set readiness after the start request; intermediate states stay JustVoxel-owned.
  for(let i=0;i<20&&!b.calls.some(c=>c.url.endsWith('/start'));i++)await Promise.resolve();
  assert.equal(b.elements.frame.src,undefined);assert.equal(b.elements.frame.hidden,true);
  assert.equal(b.elements.state.textContent,'Starting File Browser…');
  b.setReady(true);await b.flush();
  assert.equal(b.elements.frame.src,'/files/');
});

test('startup failure shows a JustVoxel retry state and retry can recover',async()=>{
  const b=browser({failed:true});await b.launcher.click();await b.flush();
  assert.equal(b.elements.frame.src,undefined);
  assert.equal(b.elements.frame.hidden,true);
  assert.equal(b.elements.retry.hidden,false);
  assert.match(b.elements.state.textContent,/unavailable.*Retry/);
  b.setFailed(false);await b.elements.retry.click();await b.flush();
  assert.equal(b.elements.frame.src,'/files/');
  assert.equal(b.elements.retry.hidden,true);
});
test('closing during startup prevents a stale iframe and queues stop after start',async()=>{
  const b=browser({ready:false});await b.launcher.click();
  for(let i=0;i<20&&!b.calls.some(c=>c.url.endsWith('/start'));i++)await Promise.resolve();
  await b.elements.close.click();b.setReady(true);await b.flush();
  assert.equal(b.elements.frame.src,undefined);
  assert.equal(b.elements.frame.hidden,true);
  const actions=b.calls.filter(c=>c.options.method==='POST').map(c=>c.url);
  assert.deepEqual(actions,['/api/files/start','/api/files/stop']);
});

test('Files visibility and both security boundaries require the primary administrator', () => {
  assert.match(header, /data-primary-admin-only data-files-open/);
  assert.match(filesJS, /contains\("primary-administrator"\)/);
  assert.match(proxy, /identity.Username != "voxel"/);
  const authorization = read('management/cmd/justvoxel-management-agent/authorization.go');
  assert.match(authorization, /sess.Username != systemAdminUsername/);
  assert.match(authorization, /s.requireAdministrator\(w, r\)/);
});
