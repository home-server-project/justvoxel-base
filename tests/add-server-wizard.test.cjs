const assert = require('node:assert/strict');
const { readFileSync, readdirSync } = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');
const root = path.join(__dirname, '../webui/internal/server');
const read = name => readFileSync(path.join(root, name), 'utf8');

test('Add Server reuses the maintained wizard, software controls and six-step presentation', () => {
  const wizard = read('templates/setup_wizard.html');
  const review = read('templates/setup_review.html');
  for (const control of ['server-software-card', 'data-memory-settings', 'setup-backup-topline', 'data-setup-connections']) assert.ok(wizard.includes(control));
  assert.ok(wizard.includes('{{$wizard.BasePath}}/recommended'));
  assert.ok(wizard.includes('{{template "setup-instance-name" .}}'));
  assert.ok(wizard.includes('name="display_name"'));
  assert.ok(wizard.includes('name="motd"'));
  for (const [index, name] of ['Server', 'Cross-play', 'Memory', 'Storage', 'Backups', 'Review'].entries()) assert.ok(review.includes(`<span class="setup-number">${index + 1}</span><span>${name}</span>`));
  const designs = readdirSync(path.join(root, 'templates')).filter(name => name.endsWith('.html') && read('templates/' + name).includes('data-setup-connections'));
  assert.deepEqual(designs, ['setup_wizard.html']);
});

test('Add Server is a keyboard accessible native action outside the ordered cards', () => {
  const markup = read('templates/minecraft_servers.html');
  assert.ok(markup.includes('<a href="{{if .Configured}}/minecraft/servers/add{{else}}/setup{{end}}">Add Server</a>'));
  assert.ok(!markup.includes('minecraft-add-server-card'));
  assert.ok(markup.indexOf('>Add Server</a>') < markup.indexOf('minecraft-server-entries'));
  assert.ok(read('static/minecraft-servers.css').includes(':focus-visible'));
});

test('How to Connect remains a single implementation with no Add Server connection alternative', () => {
  for (const [folder, expected] of [
    ['templates', ['how_to_connect.html']],
    ['static', ['how-to-connect.css', 'how-to-connect.js']],
  ]) {
    const names = readdirSync(path.join(root, folder)).filter(name => /how[-_]to[-_]connect/.test(name)).sort();
    assert.deepEqual(names, expected);
  }
  assert.ok(read('how_to_connect.go').includes('how_to_connect.html'));
  assert.ok(!read('templates/minecraft_servers.html').includes('connect-method'));
  assert.ok(!read('templates/setup_wizard.html').includes('connect-method'));
});
