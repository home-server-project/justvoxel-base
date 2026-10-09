// Exercise the production reviewed-submission fallback without browser dependencies.
const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {test} = require('node:test');
const vm = require('node:vm');
const source = readFileSync(`${__dirname}/../webui/internal/server/static/app.js`, 'utf8');
const block = source.slice(0, source.indexOf('const identityDashboard ='));
class Element {
  constructor(tag) { this.tag = tag; this.children = []; this.events = {}; this.attributes = {}; this.valid = true; }
  append(...children) { this.children.push(...children); }
  setAttribute(key, value) { this.attributes[key] = value; }
  addEventListener(name, listener) { this.events[name] = listener; }
  reportValidity() { return this.valid; }
  showModal() { this.open = true; }
  close() { this.open = false; }
  remove() { this.removed = true; }
  focus() {}
  all() { return [this, ...this.children.flatMap(child => child.all())]; }
}
function renderer() {
  const calls = [];
  const body = new Element('body');
  const context = vm.createContext({
    document: {createElement: tag => new Element(tag), body}, URLSearchParams,
    fetch: async (url, options) => {
      calls.push({url, options});
      if (context.manualError) return {ok: false, json: async () => ({error: context.manualError})};
      return {ok: true, json: async () => ({status: 'available', id: 'jv-abc123'})};
    },
  });
  vm.runInContext(block + '\nglobalThis.resume = resumeReviewedIdentitySubmission;', context);
  return {context, calls, body};
}
const flush = () => new Promise(resolve => setImmediate(resolve));
const identityRequired = () => ({status: 409, headers: {get: () => 'application/json'}, clone: () => ({json: async () => ({code: 'identity_required'})})});

test('automatic success continues silently without a popup or manual request', async () => {
  const r = renderer();
  const accepted = {status: 202};
  let submissions = 0;
  assert.equal(await r.context.resume(async () => { submissions++; return accepted; }, 'csrf-token'), accepted);
  assert.equal(submissions, 1);
  assert.equal(r.body.children.length, 0);
  assert.equal(r.calls.length, 0);
});

for (const workflow of ['setup', 'fresh import']) {
  test(`${workflow}: unresolved registration pauses, manual Save resumes the same reviewed submission`, async () => {
    const r = renderer();
    // Credentials and reviewed approvals stay in the caller's memory.
    const reviewed = new URLSearchParams({plan_fingerprint: 'sha256:reviewed', eula_accepted: 'yes', csrf: 'csrf-token', smb_password: 'secret', import_confirmation: 'IMPORT'});
    const submissions = [];
    const accepted = {status: 202};
    const pending = r.context.resume(async () => {
      submissions.push(reviewed.toString());
      return submissions.length === 1 ? identityRequired() : accepted;
    }, reviewed.get('csrf'));
    await flush();
    assert.equal(submissions.length, 1);
    const dialog = r.body.children[0];
    assert.ok(dialog.open);
    const form = dialog.all().find(node => node.tag === 'form');
    const input = dialog.all().find(node => node.tag === 'input');
    assert.equal(input.minLength, 6); assert.equal(input.maxLength, 12);
    assert.equal(input.pattern, '[A-Za-z0-9]{6,12}');
    form.valid = false;
    await form.events.submit({preventDefault() {}});
    assert.equal(r.calls.length, 0);
    form.valid = true; input.value = 'ABC123';
    for (const error of ['Use 6-12 letters and numbers after jv-.', 'Instance ID is already used. Choose another value.', 'permission denied']) {
      r.context.manualError = error;
      await form.events.submit({preventDefault() {}});
      assert.ok(dialog.all().some(node => node.textContent === error));
      assert.equal(submissions.length, 1);
    }
    r.context.manualError = '';
    await form.events.submit({preventDefault() {}});
    assert.equal(await pending, accepted);
    assert.equal(submissions.length, 2);
    assert.equal(submissions[0], submissions[1]);
    assert.ok(dialog.removed); assert.equal(dialog.open, false);
    const saved = r.calls.at(-1);
    assert.equal(new URLSearchParams(saved.options.body).get('csrf'), 'csrf-token');
    assert.equal(new URLSearchParams(saved.options.body).get('suffix'), 'ABC123');
  });
}

test('unrelated conflicts never open an identity popup or resubmit', async () => {
  const r = renderer();
  const conflict = {status: 409, headers: {get: () => 'application/json'}, clone: () => ({json: async () => ({code: 'stale_plan'})})};
  let calls = 0;
  assert.equal(await r.context.resume(async () => { calls++; return conflict; }, 'csrf-token'), conflict);
  assert.equal(calls, 1); assert.equal(r.body.children.length, 0);
});

test('Overview contains no identity display, form, automatic registration or storage workaround', () => {
  const overview = source.slice(source.indexOf('  const renderOverview ='), source.indexOf('  const formatRemainingMemory ='));
  assert.doesNotMatch(overview, /identity|Instance ID|sessionStorage/);
  assert.doesNotMatch(source, /instanceAttemptKey|renderInstanceIdentity|minecraft-instance-identity|justvoxel\.minecraft\.identity\.attempted/);
  assert.doesNotMatch(readFileSync(`${__dirname}/../webui/internal/server/static/app.css`, 'utf8'), /minecraft-instance-identity/);
});
