const {test} = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');

function element(initial = {}) {
  const listeners = {};
  const classes = new Set();
  return Object.assign({
    value: '', textContent: '', className: '', disabled: false, href: '',
    selectionStart: 0, selectionEnd: 0, children: [], attributes: {},
    get options() { return this.children; },
    addEventListener(type, callback) { listeners[type] = callback; },
    dispatch(type, event = {}) { return listeners[type]?.(event); },
    replaceChildren() { this.children = []; },
    append(child) { this.children.push(child); },
    setAttribute(name, value) { this.attributes[name] = value; },
    setRangeText(text, start, end) {
      this.value = this.value.slice(0, start) + text + this.value.slice(end);
      this.selectionStart = this.selectionEnd = start + text.length;
    },
    classList: {
      add(name) { classes.add(name); },
      remove(name) { classes.delete(name); },
      contains(name) { return classes.has(name); },
    },
  }, initial);
}

function response(status, body) {
  return {ok: status >= 200 && status < 300, status, async json() { return body; }};
}

function adminContext(replies) {
  const elements = {
    'event-select': element(),
    'yaml-editor': element(),
    'editor-status': element(),
    'editor-position': element(),
    'validate-button': element(),
    'publish-button': element(),
    'reload-button': element(),
    'public-menu-link': element(),
  };
  const requests = [];
  const windowListeners = {};
  const confirmations = [];
  const hook = {};
  const context = {
    __manaAdminTest: hook,
    document: {
      getElementById(id) { return elements[id]; },
      createElement() { return element(); },
    },
    window: {
      confirm(message) { confirmations.push(message); return true; },
      addEventListener(type, callback) { windowListeners[type] = callback; },
    },
    async fetch(url, options = {}) {
      requests.push({url, options});
      const next = replies.shift();
      if (!next) throw new Error(`Unexpected request to ${url}`);
      return next;
    },
  };
  vm.runInNewContext(fs.readFileSync('web/static/admin.js', 'utf8'), context);
  return {elements, requests, replies, hook, windowListeners, confirmations};
}

test('admin editor loads existing YAML and publishes explicit changes', async () => {
  const setup = adminContext([
    response(200, {events: [{path: '/alpha'}, {path: '/beta'}]}),
    response(200, {path: '/alpha', yaml: 'conference: alpha\n', revision: 'rev-1'}),
  ]);
  await setup.hook.ready;

  assert.equal(setup.elements['event-select'].options.length, 2);
  assert.equal(setup.elements['event-select'].value, '/alpha');
  assert.equal(setup.elements['yaml-editor'].value, 'conference: alpha\n');
  assert.equal(setup.elements['public-menu-link'].href, '/alpha');
  assert.equal(setup.hook.isDirty(), false);
  assert.equal(setup.elements['publish-button'].disabled, true);

  setup.elements['yaml-editor'].value = 'conference: updated\n';
  setup.elements['yaml-editor'].selectionStart = setup.elements['yaml-editor'].value.length;
  setup.elements['yaml-editor'].dispatch('input');
  assert.equal(setup.hook.isDirty(), true);
  assert.equal(setup.elements['publish-button'].disabled, false);
  assert.match(setup.elements['editor-status'].textContent, /unpublished/i);

  setup.replies.push(response(200, {path: '/alpha', revision: 'rev-2'}));
  await setup.hook.publishMenu();
  assert.equal(setup.requests.at(-1).url, '/admin/api/events/alpha');
  assert.equal(setup.requests.at(-1).options.method, 'PUT');
  assert.equal(setup.requests.at(-1).options.headers['X-Mana-Admin'], '1');
  assert.deepEqual(JSON.parse(setup.requests.at(-1).options.body), {yaml: 'conference: updated\n', revision: 'rev-1'});
  assert.equal(setup.hook.state.revision, 'rev-2');
  assert.equal(setup.hook.isDirty(), false);
  assert.match(setup.elements['editor-status'].textContent, /published successfully/i);
  assert.equal(setup.confirmations.length, 1);
});

test('validation and conflicts preserve unpublished YAML', async () => {
  const setup = adminContext([
    response(200, {events: [{path: '/alpha'}]}),
    response(200, {path: '/alpha', yaml: 'conference: alpha\n', revision: 'rev-1'}),
  ]);
  await setup.hook.ready;

  setup.elements['yaml-editor'].value = 'invalid: [\n';
  setup.elements['yaml-editor'].dispatch('input');
  setup.replies.push(response(422, {error: 'decode menu YAML: line 1: did not find expected node content'}));
  await setup.hook.validateMenu();
  assert.equal(setup.elements['yaml-editor'].value, 'invalid: [\n');
  assert.equal(setup.hook.isDirty(), true);
  assert.match(setup.elements['editor-status'].textContent, /line 1/i);

  setup.replies.push(response(409, {error: 'the event changed after it was loaded'}));
  await setup.hook.publishMenu();
  assert.equal(setup.elements['yaml-editor'].value, 'invalid: [\n');
  assert.equal(setup.hook.isDirty(), true);
  assert.equal(setup.elements['reload-button'].disabled, false);
  assert.match(setup.elements['editor-status'].textContent, /reload before publishing/i);

  const unload = {preventDefault() { this.prevented = true; }};
  setup.windowListeners.beforeunload(unload);
  assert.equal(unload.prevented, true);
  assert.equal(unload.returnValue, '');
});

test('Tab inserts two spaces and the save shortcut validates only', async () => {
  const setup = adminContext([
    response(200, {events: [{path: '/alpha'}]}),
    response(200, {path: '/alpha', yaml: 'days:\n', revision: 'rev-1'}),
  ]);
  await setup.hook.ready;

  const editor = setup.elements['yaml-editor'];
  editor.selectionStart = editor.selectionEnd = editor.value.length;
  let prevented = false;
  editor.dispatch('keydown', {key: 'Tab', preventDefault() { prevented = true; }});
  assert.equal(prevented, true);
  assert.equal(editor.value, 'days:\n  ');
  assert.equal(setup.hook.isDirty(), true);

  setup.replies.push(response(200, {valid: true}));
  prevented = false;
  editor.dispatch('keydown', {key: 's', ctrlKey: true, metaKey: false, preventDefault() { prevented = true; }});
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(prevented, true);
  assert.equal(setup.requests.at(-1).options.method, 'POST');
  assert.match(setup.requests.at(-1).url, /\/validate$/);
  assert.equal(setup.hook.isDirty(), true);
});
