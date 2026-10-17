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
  let assigned;
  const context = vm.createContext({URL, URLSearchParams,
   location: {origin:'https://justvoxel.test',search:''},
   window: {location:{href:'https://justvoxel.test/?workspace=backups&created=yes', assign: target => {assigned = target;}}},
   document: {createElement: tag => new Element(tag)},
   fetch: async () => ({ok:true,json:async () => ({servers:inventory,selected:{id:selectedId}})}),
  });
  await vm.runInContext(helper+'\njustVoxelServerSelection', context)(slot);
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
   assert.equal(assigned.searchParams.get('server'),inventory.at(-1).id);
   assert.equal(assigned.searchParams.get('workspace'),'plugins');
   assert.equal(assigned.searchParams.get('tab'),'installed');
   assert.equal(assigned.searchParams.get('created'),'yes');
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
