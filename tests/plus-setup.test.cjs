const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {test} = require('node:test');
const vm = require('node:vm');
const path = require('node:path');
const source = readFileSync(path.join(__dirname, '../webui/internal/server/static/plus-setup.js'), 'utf8');

class Element {
  constructor(value = '') { this.value = value; this.listeners = {}; this.children = []; this.checked = false; this.hidden = false; }
  addEventListener(name, listener) { this.listeners[name] = listener; }
  setAttribute(name, value) { this[name] = value; }
  removeAttribute(name) { delete this[name]; }
  append(...children) { this.children.push(...children); }
  replaceChildren() { this.children = []; }
  reportValidity() { return !this.required || Boolean(this.value); }
}
async function wizard({deployed = false, fail = false} = {}) {
  const names = ['stack','panel','database','cache','wings','drydock','use_domain','host','use_tls','certificate','private_key','external_storage','storage_mount','separate_account','username','email','password','confirm_password','csrf'];
  const fields = Object.fromEntries(names.map(name => [name, new Element()]));
  for (const name of ['stack','panel','database','cache','wings','drydock']) fields[name].checked = true;
  fields.csrf.value = 'token';
  const form = new Element(); form.elements = {namedItem: name => fields[name]};
  const selectors = Object.fromEntries(['data-setup-notice','data-setup-next','data-setup-back','data-setup-save','data-partial-warning','data-host-label','data-tls-fields','data-http-warning','data-storage-fields','data-system-storage','data-account-fields','data-no-account','data-email-field','data-setup-review','data-certificate-file','data-key-file'].map(name => [`[${name}]`, new Element()]));
  selectors['[data-setup-form]'] = form;
  const stepFields = [['stack','panel','database','cache','wings','drydock'],['host','certificate','private_key'],['storage_mount'],['username','email','password','confirm_password'],[]];
  const steps = stepFields.map(names => Object.assign(new Element(), {querySelectorAll: () => names.map(name => fields[name])}));
  steps.forEach((step, index) => { selectors[`[data-setup-step="${index}"]`] = step; });
  const progress = steps.map(() => new Element());
  const root = {dataset:{username:'hostadmin'},querySelector: selector => selectors[selector],querySelectorAll: selector => selector === '[data-setup-step]' ? steps : selector === '.plus-steps span' ? progress : [selectors['[data-certificate-file]'],selectors['[data-key-file]']]};
  const calls = [];
  vm.runInNewContext(source, {
    document:{querySelector:()=>root,createElement:()=>new Element()}, window:{location:{hostname:'192.168.1.10'}}, URLSearchParams,
    fetch:async (url, options) => {
      calls.push({url, options});
      return {ok:!fail,json:async()=> url.endsWith('state') ? {deployed,mounts:[{target:'/mnt/data',fstype:'xfs'}]} : {ok:true,message:'Saved'}};
    }
  });
  await new Promise(resolve => setImmediate(resolve));
  return {fields,steps,selectors,calls,form,change: name => form.listeners.change({target:fields[name]}),next:()=>selectors['[data-setup-next]'].listeners.click()};
}

test('One wizard supports group and individual choices, storage, and independent account credentials', async () => {
  const w = await wizard(); const f=w.fields;
  assert.equal(f.host.value,'192.168.1.10');
  f.stack.checked=false;w.change('stack');for(const name of ['panel','database','cache','wings'])assert.equal(f[name].checked,false);
  f.stack.checked=true;w.change('stack');
  f.database.checked=false;w.change('database');assert.equal(w.selectors['[data-partial-warning]'].hidden,false);
  f.database.checked=true;w.change('database');w.next();
  f.use_tls.checked=true;w.change('use_tls');assert.equal(f.private_key.required,true);
  f.use_tls.checked=false;w.change('use_tls');assert.equal(f.private_key.disabled,true);w.next();
  f.external_storage.checked=true;w.change('external_storage');f.storage_mount.value='/mnt/data';w.next();
  assert.equal(f.username.value,'hostadmin');assert.equal(f.username.readOnly,true);
  f.email.value='admin@example.com';f.password.value='one-password-123';f.confirm_password.value='different';w.next();
  assert.equal(w.steps[3].hidden,false);
  f.confirm_password.value=f.password.value;w.next();assert.equal(w.steps[4].hidden,false);
  assert.equal(JSON.stringify(w.selectors['[data-setup-review]'].children).includes(f.password.value),false);
  await w.form.listeners.submit({preventDefault(){}});
  const call=w.calls.find(call=>call.url.endsWith('prepare'));const saved=JSON.parse(call.options.body.get('setup'));
  assert.equal(saved.password,'one-password-123');assert.equal(saved.storage_mount,'/mnt/data');assert.equal(saved.components.database,true);
  for(const name of ['password','confirm_password','private_key','certificate'])assert.equal(f[name].value,'');
});

test('No component selection, unavailable state, and deployed systems cannot prepare setup', async () => {
  const w=await wizard();for(const name of ['panel','database','cache','wings','drydock'])w.fields[name].checked=false;
  w.fields.stack.checked=false;w.change('stack');w.fields.drydock.checked=false;w.change('drydock');w.next();
  assert.equal(w.steps[0].hidden,false);
  assert.match(w.selectors['[data-setup-notice]'].textContent,/at least one/);
  for(const options of [{deployed:true},{fail:true}]){
    const stopped=await wizard(options);assert.equal(stopped.selectors['[data-setup-next]'].disabled,true);
    await stopped.form.listeners.submit({preventDefault(){}});
    assert.equal(stopped.calls.some(call=>call.url.endsWith('prepare')),false);
  }
});
