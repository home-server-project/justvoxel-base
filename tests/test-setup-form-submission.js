// DOM fixture in the existing dependency-free Node style. Input is real rendered
// Go-template HTML, not a hand-written approximation of the Storage component.
// Native required-field validation runs before submit listeners. Hidden ancestors
// and closed dialogs do NOT exempt ordinary required inputs from validation.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const pages = JSON.parse(fs.readFileSync(0, 'utf8'));
class Element {
  constructor(tag, attributes = {}) {
    this.tag = tag; this.attributes = attributes; this.children = []; this.events = {};
    this.dataset = Object.fromEntries(Object.entries(attributes).filter(([key]) => key.startsWith('data-'))
      .map(([key, value]) => [key.slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase()), value]));
    this.value = attributes.value || ''; this.type = attributes.type || 'text';
    this.hidden = 'hidden' in attributes; this.disabled = 'disabled' in attributes;
    this.required = 'required' in attributes; this.readOnly = 'readonly' in attributes;
    this.checked = 'checked' in attributes;
    this.classList = { toggle() {}, remove() {} };
  }
  matches(selector) {
    const tag = selector.match(/^[\w-]+/);
    if (tag && this.tag !== tag[0]) return false;
    if (selector.startsWith('.')) return (this.attributes.class || '').split(/\s+/).includes(selector.slice(1));
    return [...selector.matchAll(/\[([^=\]]+)(?:="([^"]*)")?\]/g)]
      .every(([, name, value]) => name in this.attributes && (value === undefined || this.attributes[name] === value));
  }
  all() { return this.children.flatMap(child => [child, ...child.all()]); }
  querySelectorAll(selector) { return this.all().filter(child => child.matches(selector)); }
  querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
  hasAttribute(name) { return name in this.attributes; }
  setAttribute(name, value) { this.attributes[name] = value; }
  addEventListener(type, callback) { (this.events[type] ||= []).push(callback); }
  dispatchEvent(event) { for (const callback of this.events[event.type] || []) callback(event); }
  click() { this.dispatchEvent({ type: 'click' }); }
  checkValidity() {
    return this.all().filter(child => ['input', 'select', 'textarea'].includes(child.tag)).every(child =>
      child.disabled || child.readOnly || ['hidden', 'button', 'submit', 'reset', 'range'].includes(child.type) ||
      !child.required || (child.type === 'checkbox' ? child.checked : Boolean(child.value)));
  }
  requestSubmit(submitter) {
    if (submitter.disabled || (!submitter.hasAttribute('formnovalidate') && !this.checkValidity())) return false;
    const event = { type: 'submit', submitter, defaultPrevented: false, preventDefault() { this.defaultPrevented = true; } };
    this.dispatchEvent(event);
    return !event.defaultPrevented;
  }
}
function parse(html) {
  const document = new Element('document'), stack = [document];
  const voidTags = new Set(['input', 'img', 'meta', 'link', 'br', 'hr']);
  for (const [token, closing, tag, rest] of html.matchAll(/<(\/)?([a-z][\w-]*)([^>]*)>/gi)) {
    if (closing) {
      const index = stack.findLastIndex(node => node.tag === tag);
      if (index > 0) stack.length = index;
      continue;
    }
    const attributes = {};
    for (const [, name, quoted, unquoted] of rest.matchAll(/([\w-]+)(?:\s*=\s*(?:"([^"]*)"|([^\s>]+)))?/g)) attributes[name] = quoted ?? unquoted ?? '';
    const node = new Element(tag, attributes);
    stack.at(-1).children.push(node);
    if (!voidTags.has(tag)) stack.push(node);
  }
  return document;
}
const source = fs.readFileSync(path.join(__dirname, '../webui/internal/server/static/setup-storage.js'), 'utf8');
for (const page of pages) {
  const document = parse(page.html);
  const context = vm.createContext({ document, window: {} });
  vm.runInContext(source, context);
  const form = document.querySelector('[data-setup-storage-form]') || document.querySelector('[data-setup-backup-form]');
  assert(form, page.name);
  assert.equal(form.querySelector('[data-storage-minecraft-dialog]'), null, page.name);
  assert.equal(form.querySelector('[data-storage-minecraft-mount]'), null, page.name);
  assert.equal(form.querySelector('[data-storage-minecraft-path]'), null, page.name);
  const next = form.querySelector('button[name="direction"][value="next"]');
  assert(next && !next.hasAttribute('formnovalidate'));
  let submitted = 0;
  form.addEventListener('submit', () => submitted++);
  assert(form.requestSubmit(next), `${page.name}: native validation blocked Continue`);
  assert.equal(submitted, 1);
  if (page.name.startsWith('storage')) {
    form.querySelector('[data-setup-storage-internal]').click();
    assert.equal(form.querySelector('[data-setup-storage-type]').value, 'partition');
    assert.equal(form.querySelector('[data-setup-storage-device]').value, '');
    assert(form.requestSubmit(next), 'Custom must reach authoritative server validation, including empty selection');
    const target = form.querySelector('[data-setup-existing-partition]');
    assert(target, 'fixture must include a selectable safe internal partition');
    form.dispatchEvent({ type: 'setup-destination-select', detail: target.dataset.device });
    assert.equal(form.querySelector('[data-setup-storage-device]').value, target.dataset.device);
    assert(form.requestSubmit(next), 'Valid Custom selection must submit');
    form.querySelector('[data-setup-storage-system]').click();
    assert.equal(form.querySelector('[data-setup-storage-type]').value, 'system');
    assert(form.requestSubmit(next), 'Simple must submit after returning from Custom');
  } else {
    for (const choice of form.querySelectorAll('[data-setup-backup-choice]')) {
      choice.click();
      assert(form.requestSubmit(next), `Backup ${choice.dataset.setupBackupChoice} has an unrelated blocker`);
    }
  }
  // Positive control: closed/hidden ordinary required fields really do prevent
  // submission before listeners in this fixture, reproducing the original bug.
  const dialog = new Element('dialog', { hidden: '' });
  dialog.children.push(new Element('input', { required: '', 'data-storage-minecraft-mount': '' }));
  form.children.push(dialog);
  const before = submitted;
  assert.equal(form.requestSubmit(next), false);
  assert.equal(submitted, before);
}
