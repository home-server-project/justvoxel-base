// Exercise the actual Players renderer without installing browser dependencies.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const source = fs.readFileSync(`${__dirname}/../webui/internal/server/static/app.js`, 'utf8');
const block = source.slice(source.indexOf('  const renderPlayers ='), source.indexOf('  const renderLogs ='));
class Element {
  constructor(tag) { this.tagName = tag; this.children = []; this.dataset = {}; this.attributes = {}; this.events = {}; this.disabled = false; this.textContent = ''; }
  append(...nodes) { this.children.push(...nodes); }
  appendChild(node) { this.append(node); return node; }
  replaceChildren(...nodes) { this.children = nodes; }
  setAttribute(key, value) { this.attributes[key] = value; }
  addEventListener(type, listener) { this.events[type] = listener; }
  set innerHTML(html) {
    this.html = html;
    // Supply only queried elements; static markup is checked independently below.
    if (html.includes('minecraft-whitelist-state')) {
      const header = new Element('div'); header.className = 'minecraft-whitelist-state';
      const list = new Element('div'); list.attributes['data-minecraft-native-whitelist'] = '';
      this.append(header, list);
    }
  }
  querySelector(selector) {
    return all(this).find(node => selector.startsWith('.') ? node.className === selector.slice(1) : Object.hasOwn(node.attributes, selector.slice(1, -1)));
  }
}
function all(node) { return [node, ...node.children.flatMap(all)]; }
const document = { createElement: tag => new Element(tag) };
const messageNode = text => { const node = new Element('p'); node.textContent = text; return node; };
const noticeNode = messageNode;
const content = new Element('div');
const state = new Element('p');
const csrf = 'csrf';
const loadSequence = 1;
let enabled = true, canToggle = true, online = ['.SirGiggles0'];
let calls = [], confirmations = [], reloads = [], confirmAnswer = true, fail = false;
const window = { confirm: message => { confirmations.push(message); return confirmAnswer; } };
const entries = [
  {identity: '.SirGiggles0', display: 'SirGiggles0', platform: 'bedrock'},
  {identity: 'SirGiggles0', display: 'SirGiggles0', platform: 'java'},
];
const requestWorkspaceJSON = async (url, options) => {
  if (options) { calls.push({url, options}); if (fail) throw new Error('Live whitelist change failed'); return {message:'Updated'}; }
  if (url === '/api/dashboard-status') return {status:{minecraft:{configured:true}}, players:{online:3,max:10,names:online}};
  return {whitelist_enabled:enabled, can_toggle:canToggle, parsed:true, entries};
};
const loadCurrentTab = async message => { reloads.push(message); };
const FormData = class { forEach(callback) { callback('bedrock','platform'); callback('PlayerOne','name'); } };
const renderPlayers = new Function('document','messageNode','noticeNode','content','state','csrf','loadSequence','window','requestWorkspaceJSON','loadCurrentTab','FormData', block+'return renderPlayers;')(
  document,messageNode,noticeNode,content,state,csrf,loadSequence,window,requestWorkspaceJSON,loadCurrentTab,FormData);
const switchNode = () => all(content).find(node => node.attributes.role === 'switch');
(async () => {
  await renderPlayers(1);
  assert(all(content).some(node => node.textContent === 'Online players: 3 / 10'));
  assert(!block.includes('<h2>Online</h2>') && !block.includes('No players online.'));
  for (const column of ['Platform', 'Player', 'Status', 'Actions']) assert(block.includes(`${column}</th>`));
  const rows = all(content).filter(node => node.tagName === 'tr');
  assert.equal(rows.length,2);
  assert.equal(rows[0].dataset.playerIdentity,'.SirGiggles0');
  assert.equal(rows[1].dataset.playerIdentity,'SirGiggles0');
  assert.equal(rows[0].children[1].textContent,'SirGiggles0');
  assert.equal(rows[0].children[2].textContent,'Online');
  assert.equal(rows[1].children[2].textContent,'Offline');
  const toggle = switchNode();
  assert.equal(toggle.tagName,'button'); assert.equal(toggle.type,'button');
  assert.equal(toggle.attributes['aria-checked'],'true');
  assert(all(content).some(node => node.textContent === 'Enabled'));
  confirmAnswer = false;
  await toggle.events.click();
  assert.equal(calls.length,0); assert.equal(toggle.attributes['aria-checked'],'true');
  assert(confirmations[0].includes('Players not on the whitelist will be allowed to join.'));
  confirmAnswer = true;
  await toggle.events.click();
  assert.equal(new URLSearchParams(calls[0].options.body).get('enabled'),'false');
  assert.equal(reloads.length,1);
  enabled = false; confirmations = []; calls = []; reloads = [];
  await renderPlayers(1);
  assert.equal(switchNode().attributes['aria-checked'],'false');
  assert(all(content).some(node => node.textContent === 'Disabled'));
  await switchNode().events.click();
  assert.equal(confirmations.length,0);
  assert.equal(new URLSearchParams(calls[0].options.body).get('enabled'),'true');
  fail = true; reloads = [];
  await switchNode().events.click();
  assert.equal(switchNode().attributes['aria-checked'],'false');
  assert.equal(reloads.length,1); assert(state.textContent.includes('failed'));
  fail = false; canToggle = false; calls = [];
  await renderPlayers(1); assert.equal(switchNode(),undefined);
  all(content).find(node => node.tagName === 'form').events.submit({preventDefault(){},submitter:new Element('button')});
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(calls[0].url,'/api/minecraft/workspace/whitelist');
  assert.equal(new URLSearchParams(calls[0].options.body).get('action'),'add');
  calls = [];
  all(content).find(node => node.textContent === 'Remove').events.click();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(new URLSearchParams(calls[0].options.body).get('name'),'SirGiggles0');
  assert.equal(new URLSearchParams(calls[0].options.body).get('action'),'remove');
  console.log('PASS: Players identity statuses, toggle state/permissions, confirmation/cancel, failure reload, Add/Remove');
})().catch(error => { console.error(error); process.exitCode = 1; });
