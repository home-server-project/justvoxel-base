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
const mounts = read('management/cmd/justvoxel-management-agent/filebrowser_mounts.go');
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
  assert.match(workspace, /system-monitor-header-button system-monitor-settings-button[^>]*data-files-settings-toggle[^>]*>Settings<\/button>/);
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
  assert.match(container,/--cap-add=DAC_OVERRIDE/);
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
test('Files discovers actual mounted filesystems through Storage and mountinfo', () => {
  const discovery = read('management/cmd/justvoxel-management-agent/admin_discovery.go');
  const helper = read('mjust/libexec/admin-discovery-json');
  assert.match(discovery, /MajorMinor\s+string\s+`json:"major_minor,omitempty"`/);
  const lsblkCalls = helper.match(/\blsblk\s+[^\n]+/g) || [];
  assert.equal(lsblkCalls.length, 1, 'extend the existing lsblk call only');
  assert.match(lsblkCalls[0], /-o [A-Z,:]*\bMAJ:MIN\b/);
  assert.match(helper, /major_minor:\(\.\["maj:min"\] \/\/ ""\)/);
  assert.match(mounts, /runAdminDiscoveryHelper\(ctx, "storage"\)/);
  assert.match(mounts, /os.ReadFile\("\/proc\/self\/mountinfo"\)/);
  assert.match(mounts, /device.System/);
  assert.match(mounts, /byPath\[point\]/);
  assert.match(mounts, /mount.Filesystem == device.Filesystem/);
  assert.match(mounts, /device.MajorMinor == ""/);
  assert.match(mounts, /mount.MajorMinor == device.MajorMinor/);
  assert.match(mounts, /mount.Root == "\/"/);
  assert.match(mounts, /point == "\/" \|\| counts\[point\] > 1\s*\{\s*continue/);
  assert.match(mounts, /protocol == "" \|\| mount.Mountpoint == "\/"/);
  assert.match(mounts, /counts\[mount.Mountpoint\] > 1\s*\{\s*continue/);
  assert.doesNotMatch(mounts, /ambiguous File Browser mountpoint/);
  assert.match(mounts, /device.ReadOnly \|\| mount.ReadOnly/);
  assert.match(mounts, /case "nfs", "nfs4"/);
  assert.match(mounts, /case "cifs", "smb3"/);
  assert.match(mounts, /sha256.Sum256/);
  assert.match(mounts, /"\/data\/filesystems\/"/);
  assert.match(container, /discoverFileBrowserFilesystems\(ctx\)/);
  assert.match(container, /fileBrowserPathCovered\(record.Storage.DataPath, filesystems\)/);
  assert.match(container, /fileBrowserPathCovered\(root, filesystems\)/);
  assert.match(container, /revalidateFileBrowserMounts\(sources, readFileBrowserMountInfo, os.Lstat\)/);
  assert.ok(container.indexOf('revalidateFileBrowserMounts(sources') < container.indexOf('return syscall.Exec('));
  for (const forbidden of ['automaticFileBrowserSources', 'discoverAutomaticFileBrowserSources', 'Mounted storage', 'Removable media', 'System /usr', 'System /boot', 'rslave', '/data/mounted/', '/data/system/']) {
    assert.ok(!(mounts + container).includes(forbidden), forbidden);
  }
  // Discovery uses device/mount identity, with no mountpoint directory allowlist.
  for (const forbidden of ['/var/mnt', '/media', '/run/media', '/mnt', '/usr', '/boot']) assert.ok(!mounts.includes(`"${forbidden}"`));
  assert.doesNotMatch(mounts, /(?:HasPrefix|HasSuffix|instanceRuntimeInside)\((?:mount\.Mountpoint|point),/);
  assert.doesNotMatch(mounts, /(?:allowed|approved|supported)(?:Mountpoints|MountRoots|Directories)|mountpointAllowlist/i);
  assert.doesNotMatch(mounts, /exec\.|findmnt|lsblk|systemctl|udev|NewTicker|time\.Tick/);
  assert.match(container, /"bind-nonrecursive=true"/);
  assert.match(container, /if source.ReadOnly/);
  const render = container.slice(container.indexOf('func renderFileBrowserConfig('), container.indexOf('func runFileBrowserContainer('));
  assert.match(render, /"readOnly":\s*source.ReadOnly/);
  for (const permission of ['create','modify','delete']) assert.ok(new RegExp(`"${permission}":\\s*!source.ReadOnly`).test(render));
  for (const permission of ['view','download','configured']) assert.ok(new RegExp(`"${permission}":\\s*true`).test(render));
  assert.match(render, /"defaultUserScope":\s*"\/"/);
  assert.equal(config.frontend.disableUsedPercentage, true);
  assert.equal(config.frontend.disableNavButtons, undefined);
  assert.equal(config.userDefaults.account.permissions.admin, false);
});

test('Refresh is a fixed primary-admin action, without mount watchers', () => {
  const client = read('webui/internal/api/filebrowser.go');
  for (const source of [client, proxy]) assert.match(source, /case "start", "stop", "refresh", "availability":/);
  assert.match(manager, /case "refresh":[\s\S]*?fileBrowserCommand\(ctx, "systemctl", "restart", fileBrowserUnit\)/);
  assert.match(manager, /const fileBrowserUnit = "justvoxel-filebrowser.service"/);
  assert.match(manager, /requirePrimaryAdministrator/);
  const unit = read('system_files/usr/lib/systemd/system/justvoxel-filebrowser.service');
  for (const forbidden of [/--make-(?:rshared|shared|rslave|slave)/, /\bMS_(?:SHARED|SLAVE)\b/, /--device/, /--privileged/, /--network=host/, /:[zZ]\b/]) assert.doesNotMatch(container + mounts + manager + unit, forbidden);
  assert.doesNotMatch(mounts + manager, /udev|NewTicker|time\.Tick|mount-watcher/);
});

test('supported proxy auth disables second login and internal settings', () => {
  assert.equal(config.http.baseURL,'/files/');
  assert.equal(config.http.disableWebDAV,true);
  assert.deepEqual(config.userDefaults.account.permissions,{admin:false,share:false,api:false});
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
test('opening waits for health, refresh restarts and reconnects, X requests managed stop',async()=>{
  const b=browser();await b.launcher.click();await b.flush();
  assert.equal(b.elements.frame.src,'/files/');assert.equal(b.elements.frame.hidden,false);
  await b.elements.refresh.click();await b.flush();
  assert.equal(b.elements.frame.src,'/files/');
  assert.equal(b.calls.filter(c=>c.url.endsWith('/start')).length,0);
  assert.equal(b.calls.filter(c=>c.url.endsWith('/refresh')).length,1);
  assert.equal(b.calls.find(c=>c.url.endsWith('/refresh')).options.method,'POST');
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

test('refresh detaches the old iframe and waits for new readiness in both modes', async()=>{
  for (const availability of ['always','on-demand']) {
    const b=browser({availability});await b.launcher.click();await b.flush();
    b.setReady(false);
    await b.elements.refresh.click();
    assert.equal(b.elements.frame.src,undefined);
    assert.equal(b.elements.frame.hidden,true);
    assert.equal(b.elements.state.textContent,'Refreshing File Browser…');
    for(let i=0;i<20&&!b.calls.some(c=>c.url.endsWith('/refresh'));i++)await Promise.resolve();
    assert.equal(b.calls.filter(c=>c.url.endsWith('/refresh')).length,1);
    assert.equal(b.elements.frame.src,undefined);
    b.setReady(true);await b.flush();
    assert.equal(b.elements.frame.src,'/files/');assert.equal(b.elements.frame.hidden,false);
  }
});
test('refresh failure keeps the iframe detached and Retry repeats managed refresh',async()=>{
  const b=browser();await b.launcher.click();await b.flush();
  b.setFailed(true);await b.elements.refresh.click();await b.flush();
  assert.equal(b.elements.frame.src,undefined);assert.equal(b.elements.frame.hidden,true);
  assert.equal(b.elements.retry.hidden,false);
  b.setFailed(false);await b.elements.retry.click();await b.flush();
  assert.equal(b.calls.filter(c=>c.url.endsWith('/refresh')).length,2);
  assert.equal(b.elements.frame.src,'/files/');assert.equal(b.elements.retry.hidden,true);
});
test('closing during refresh prevents reconnecting a stale iframe',async()=>{
  const b=browser();await b.launcher.click();await b.flush();
  b.setReady(false);await b.elements.refresh.click();
  for(let i=0;i<20&&!b.calls.some(c=>c.url.endsWith('/refresh'));i++)await Promise.resolve();
  await b.elements.close.click();b.setReady(true);await b.flush();
  assert.equal(b.elements.frame.src,undefined);
  assert.equal(b.calls.at(-1).url,'/api/files/stop');
});

test('closing immediately after refresh does not restart after the managed stop',async()=>{
  const b=browser();await b.launcher.click();await b.flush();
  b.elements.refresh.click();await b.elements.close.click();await b.flush();
  assert.equal(b.elements.frame.src,undefined);
  assert.equal(b.calls.at(-1).url,'/api/files/stop');
  assert.equal(b.calls.filter(c=>c.url.endsWith('/refresh')).length,0);
});
