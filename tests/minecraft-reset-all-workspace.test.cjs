const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {test} = require('node:test');
const vm = require('node:vm');
const source = readFileSync(`${__dirname}/../webui/internal/server/static/app.js`, 'utf8');
class Element {
  constructor(tag) { this.tag=tag; this.children=[]; this.events={}; this.style={setProperty(){}}; this.classList={add(){},toggle(){}}; this.checked=false; }
  append(...children) { this.children.push(...children); }
  appendChild(child) { this.children.push(child); return child; }
  replaceChildren(...children) { this.children=children; }
  addEventListener(name,handler) { this.events[name]=handler; }
  setAttribute() {}
}
const descendants = (node) => [node,...node.children.flatMap(descendants)];
const text = (node) => descendants(node).map(child => child.textContent || '').join(' ');
function review(plan,mode='minecraft') {
  const content=new Element('main');
  const context=vm.createContext({document:{createElement:tag=>new Element(tag)},content,
    clearResetPoll(){},systemPanel(title,subtitle){ const panel=new Element('section'); panel.textContent=title+' '+subtitle; return {panel}; },
    renderResetChoices(){},systemPostForm:async()=>{},renderResetOperation(){},state:{}});
  vm.runInContext(source.slice(source.indexOf('  const systemResetList ='),source.indexOf('  const systemResetOperationLabel ='))+
    source.slice(source.indexOf('  const resetImpactFromPlan ='),source.indexOf('  const planReset ='))+
    '\nglobalThis.renderReview = renderResetPlan;',context);
  context.renderReview(mode,plan);
  return {content,nodes:descendants(content)};
}
const plan={participants:[{id:'jv-first01',display_name:'Family Survival',state:'stopped'},{id:'jv-second1',display_name:'Creative',state:'running'}],players_online:1,players:['Alex']};
test('Review stays in Factory Reset and renders authoritative participants and preservation',()=>{
  const {content}=review(plan);
  const copy=text(content);
  for(const expected of ['Servers to remove: 2','Family Survival — Stopped','Creative — Running','Will remove','Will keep','ALL configured Minecraft server definitions','JustVoxel-owned INTERNAL Minecraft data','All backup archives and archive metadata','Login/password, authentication mode, and WebUI users','Storage layout, mounts, partitions, filesystems','External, USB, NFS, and SMB data are never erased']) assert.ok(copy.includes(expected),expected);
  assert.doesNotMatch(copy,/current Minecraft|selected server/i);
});
test('existing slider, final Confirm and additional online-player confirmation gate Apply',()=>{
  const {nodes}=review(plan);
  const apply=nodes.find(n=>n.tag==='button' && n.textContent==='Reset Minecraft');
  const slider=nodes.find(n=>n.type==='range');
  const confirm=nodes.find(n=>n.className==='destructive-confirm-toggle');
  const players=nodes.find(n=>n.type==='checkbox' && n!==confirm);
  const finalRow=nodes.find(n=>n.className==='destructive-confirm-toggle-row');
  assert.ok(nodes.some(n=>n.className==='destructive-confirmation'));
  assert.ok(nodes.some(n=>n.className==='destructive-confirm-slider'));
  assert.equal(apply.disabled,true); assert.equal(finalRow.hidden,true);
  slider.value='100'; slider.events.input();
  assert.equal(finalRow.hidden,false); assert.equal(apply.disabled,true);
  confirm.checked=true; confirm.events.change(); assert.equal(apply.disabled,true);
  players.checked=true; players.events.change(); assert.equal(apply.disabled,false);
  slider.value='50'; slider.events.input(); assert.equal(apply.disabled,true); assert.equal(confirm.checked,false);
});
test('no-player Review still requires both destructive controls',()=>{
  const {nodes}=review({...plan,players_online:0,players:[]});
  const apply=nodes.find(n=>n.tag==='button' && n.textContent==='Reset Minecraft');
  const slider=nodes.find(n=>n.type==='range');
  const confirm=nodes.find(n=>n.className==='destructive-confirm-toggle');
  confirm.checked=true; confirm.events.change(); assert.equal(apply.disabled,true);
  slider.value='100'; slider.events.input(); assert.equal(apply.disabled,true);
  confirm.checked=true; confirm.events.change(); assert.equal(apply.disabled,false);
});
test('card, continuation and in-page success preserve existing workflow architecture',()=>{
  const card=source.slice(source.indexOf('  const renderResetChoices ='),source.indexOf('  const renderReset ='));
  assert.match(card,/Remove ALL existing Minecraft servers, running or stopped, and JustVoxel-owned internal Minecraft data/);
  assert.doesNotMatch(card,/current Minecraft server/);
  const progress=source.slice(source.indexOf('  const renderResetOperation ='),source.indexOf('  const resetCurrentOperation ='));
  assert.match(progress,/operation\.instance_id \? "Retry Reset Minecraft" : "Continue removal"/);
  assert.match(progress,/Minecraft removal stopped before completion/);
  assert.match(progress,/target\.searchParams\.delete\("server"\)/);
  assert.match(progress,/justVoxelSelectionGeneration/);
  assert.match(progress,/justVoxelServerSelection\(slot\)/);
  assert.match(progress,/justvoxel:server-selection-changed/);
  assert.doesNotMatch(progress,/location\.(reload|assign)|wallpaper/i);
  assert.match(source,/apply\.disabled = !\(armed && finalConfirm\.checked && playersOK && passwordOK\)/);
});
test('Full Factory Reset retains its independent impact and password contract',()=>{
  const {content}=review({data_action:'delete',backup_action:'delete',warnings:[],players_online:0},'factory');
  const copy=text(content);
  for(const expected of ['Backup archives on appliance-owned internal storage','Additional WebUI users','voxel / voxel','JustVoxel-managed mounts and /etc/fstab entries']) assert.ok(copy.includes(expected));
});
