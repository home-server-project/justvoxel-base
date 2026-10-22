const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const app = fs.readFileSync(path.join(__dirname, '..', 'webui/internal/server/static/app.js'), 'utf8');
const start = app.indexOf('  const systemFetchJSON =');
const end = app.indexOf('  const systemSplitter =', start);
assert.ok(start >= 0 && end > start, 'production System Health rendering path exists');
const healthCode = app.slice(start, end);

class Element {
  constructor(tag) {
    this.tag = tag;
    this.children = [];
    this.className = '';
    this.textContent = '';
  }
  // Health details must remain text, even when the output contains markup.
  set innerHTML(_) { throw new Error('Health output must not be interpreted as HTML'); }
  append(...nodes) { this.children.push(...nodes); }
  appendChild(node) { this.append(node); return node; }
  replaceChildren(...nodes) { this.children = nodes; }
  all() { return [this, ...this.children.flatMap(node => node.all())]; }
}

function harness(result) {
  const content = new Element('div');
  const reads = [];
  const context = vm.createContext({
    content, loadSequence: 1,
    document: { createElement: tag => new Element(tag) },
    fetch: async (url, options) => {
      reads.push({ url, options });
      return { status: 200, ok: true, json: async () => ({ result: await result }) };
    },
  });
  vm.runInContext(healthCode + '\nglobalThis.clean = cleanSystemHealthOutput; globalThis.render = renderHealth;', context);
  return { content, reads, context, clean: context.clean, render: context.render };
}

const cases = [
  ['plain text', 'OK Paper 1.21.8 build 60\nWARNING Geyser-Spigot 2.8.2\nERROR Floodgate 2.2.4\nhttps://example.com/plugins?q=paper&version=1.21.8',
    'OK Paper 1.21.8 build 60\nWARNING Geyser-Spigot 2.8.2\nERROR Floodgate 2.2.4\nhttps://example.com/plugins?q=paper&version=1.21.8'],
  ['ANSI foreground, background and reset', '\x1b[32mOK\x1b[0m \x1b[41;97mERROR\x1b[m \x1b[38;2;10;20;30mWARNING\x1b[0m', 'OK ERROR WARNING'],
  ['other ANSI terminal controls', '\x1b[2K\x1b[1GOK\x1b(B\x1b[?25l\x1b7\x1b8\x07', 'OK'],
  ['ANSI control strings', '\x1b]0;window title\x07OK \x1b]8;;https://example.com\x1b\\https://example.com\x1b]8;;\x1b\\\x1bPignored\x1b\\\x1bXignored\x1b\\\x1b^ignored\x1b\\\x1b_ignored\x1b\\', 'OK https://example.com'],
  ['eight-bit ANSI controls', '\x9b32mOK\x9b0m \x9d8;;https://example.com\x9chttps://example.com\x9d8;;\x9c\x90ignored\x9c', 'OK https://example.com'],
  ['Minecraft legacy colors and formats', '§aOK§r §cERROR§r §eWARNING§r §k§l§m§n§oFloodgate§r §BPaper§R', 'OK ERROR WARNING Floodgate Paper'],
  ['Minecraft RGB colors', '§x§1§2§a§b§c§dGeyser-Spigot§r §X§F§F§0§0§A§APaper§R', 'Geyser-Spigot Paper'],
  ['orphaned Minecraft RGB prefixes', '§xPaper 1.21.8 §XGeyser-Spigot 2.8.2 §x §X', 'Paper 1.21.8 Geyser-Spigot 2.8.2  '],
  ['Plugins RGB prefixes followed by ANSI colors', 'Plugins: §x\x1b[38;2;85;255;85mGeyser-Spigot 2.8.2\x1b[0m, §X\x1b[32mFloodgate 2.2.4\x1b[0m\n  https://example.com/plugins?q=paper&version=1.21.8\n',
    'Plugins: Geyser-Spigot 2.8.2, Floodgate 2.2.4\n  https://example.com/plugins?q=paper&version=1.21.8\n'],
  ['mixed formatting and meaningful details', '\x1b[32m§aOK§r Paper 1.21.8\x1b[0m\n§x§1§2§3§4§5§6Geyser-Spigot 2.8.2\n\x1b[43m§eWARNING§r https://example.com/releases/2.8.2\x1b[0m\n§cERROR§r Floodgate 2.2.4',
    'OK Paper 1.21.8\nGeyser-Spigot 2.8.2\nWARNING https://example.com/releases/2.8.2\nERROR Floodgate 2.2.4'],
  ['newlines and indentation', '\x1b[32mOK\x1b[0m\r\n  §aPaper§r\n\tGeyser-Spigot\n\n    WARNING\n', 'OK\r\n  Paper\n\tGeyser-Spigot\n\n    WARNING\n'],
  ['literal markup and ordinary section signs', '§aOK§r <script>alert("test")</script> <b>Paper</b> & https://example.com/?a=1&b=2 §z § §x',
    'OK <script>alert("test")</script> <b>Paper</b> & https://example.com/?a=1&b=2 §z § '],
];

for (const [name, output, expected] of cases) {
  test(`System Health cleans ${name} through the production helper and renderer`, async () => {
    const result = Object.freeze({ ok: true, output });
    const h = harness(result);
    assert.equal(h.clean(output), expected);
    await h.render(1);
    const pre = h.content.all().find(node => node.tag === 'pre');
    assert.equal(pre.className, 'log-box');
    assert.equal(pre.textContent, expected);
    assert.equal(pre.children.length, 0);
    assert.equal(result.output, output, 'raw validation output is unchanged');
    assert.equal(h.reads[0].url, '/api/system/workspace/health');
  });
}

test('Health success and failure notices depend only on the reported status', async () => {
  for (const ok of [true, false]) {
    for (const output of ['§cERROR§r diagnostic text', '\x1b[32mOK\x1b[0m diagnostic text']) {
      const h = harness(Object.freeze({ ok, output }));
      await h.render(1);
      const notice = h.content.all().find(node => node.className.startsWith('notice '));
      assert.equal(notice.className, ok ? 'notice success' : 'notice error');
      assert.equal(notice.textContent, ok ? 'System health check passed.' : 'System health check found problems.');
    }
  }
});

test('missing Health output retains the existing fallback', async () => {
  for (const output of [undefined, '']) {
    const h = harness({ ok: true, output });
    await h.render(1);
    assert.equal(h.content.all().find(node => node.tag === 'pre').textContent, 'No detailed health output was returned.');
  }
});

test('stale Health responses do not render', async () => {
  const h = harness({ ok: true, output: '§aOK§r' });
  await h.render(0);
  assert.equal(h.content.children.length, 0);
});

test('Health panel and running terminal precede the response; real output replaces the placeholder', async () => {
  let resolve;
  const h = harness(new Promise(done => { resolve = done; }));
  const pending = h.render(1);
  const root = h.content.children[0];
  assert.equal(root.className, 'system-health-view');
  assert.equal(root.children[0].className, 'panel details');
  const pre = h.content.all().find(node => node.tag === 'pre');
  assert.equal(pre.className, 'log-box');
  assert.equal(pre.textContent, 'Running system health checks…');
  assert.equal(h.content.all().some(node => node.className.startsWith('notice ')), false);
  assert.equal(h.reads[0].url, '/api/system/workspace/health');
  resolve({ok: false, output: '§cERROR§r Real validation output'});
  await pending;
  assert.equal(h.content.all().find(node => node.tag === 'pre'), pre);
  assert.equal(pre.textContent, 'ERROR Real validation output');
  assert.equal(root.children[0].className, 'notice error');
});

test('stale in-flight Health output cannot replace newer workspace content', async () => {
  let resolve;
  const h = harness(new Promise(done => { resolve = done; }));
  const pending = h.render(1);
  h.context.loadSequence++;
  const newer = new Element('section');
  h.content.replaceChildren(newer);
  resolve({ok: true, output: 'Old health output'});
  await pending;
  assert.deepEqual(h.content.children, [newer]);
  assert.equal(h.content.all().some(node => node.className.startsWith('notice ')), false);
});
