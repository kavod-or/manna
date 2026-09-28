const {test} = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');

function element(initial = {}) {
  const listeners = {};
  const classes = new Set();
  return Object.assign({
    value: '', textContent: '', innerHTML: '', className: '', disabled: false,
    hidden: false, href: '', children: [], attributes: {}, dataset: {},
    get options() { return this.children; },
    addEventListener(type, callback) { listeners[type] = callback; },
    dispatch(type, event = {}) { return listeners[type]?.(event); },
    replaceChildren() { this.children = []; },
    append(child) { this.children.push(child); },
    setAttribute(name, value) { this.attributes[name] = value; },
    querySelectorAll() { return []; },
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

function config() {
  return {
    conference: {
      currency: 'euro', hide_prices: false, payment: {}, languages: ['de', 'en'],
      serious_mode: false, easter_egg_mode: '', timezone: 'Europe/Berlin', logo: '',
      name: {de: 'Test', en: 'Test'}, location: {de: 'Berlin', en: 'Berlin'},
    },
    tags: {}, regulatory: {},
    permanent: {coffee: [], drinks: [], snacks: []},
    days: [{date: '2026-09-26', food_trucks: [], services: [{
      id: 'lunch', sold_out: false, title: {de: 'Mittagessen', en: 'Lunch'},
      subtitle: {de: '', en: ''}, from: '12:00', until: '13:00', items: [],
    }]}],
  };
}

function adminContext(replies, promptReplies = []) {
  const elements = {
    'event-select': element(), 'menu-form': element(), 'editor-tabs': element(),
    'editor-status': element(), 'validate-button': element(), 'publish-button': element(),
    'reload-button': element(), 'add-event-button': element(), 'delete-event-button': element(), 'public-menu-link': element(),
    'preview-panel': element({hidden: true}), 'menu-preview': element(),
    'preview-language': element(), 'preview-day': element(),
  };
  const requests = [];
  const windowListeners = {};
  const confirmations = [];
  const prompts = [];
  const hook = {};
  class TestFormData {
    constructor() { this.entries = []; }
    append(name, value) { this.entries.push([name, value]); }
  }
  class TestFileReader {
    constructor() { this.listeners = {}; this.result = ''; }
    addEventListener(type, callback) { this.listeners[type] = callback; }
    readAsDataURL() {
      this.result = 'data:image/png;base64,dGVzdA==';
      this.listeners.load?.();
    }
  }
  const context = {
    __mannaAdminTest: hook,
    FormData: TestFormData,
    FileReader: TestFileReader,
    setTimeout,
    clearTimeout,
    document: {
      getElementById(id) { return elements[id]; },
      createElement() { return element(); },
    },
    window: {
      confirm(message) { confirmations.push(message); return true; },
      prompt(message) { prompts.push(message); return promptReplies.shift() ?? null; },
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
  return {elements, requests, replies, hook, window: context.window, windowListeners, confirmations, prompts};
}

test('event management creates blank events and permanently removes confirmed events', async () => {
  const created = config();
  created.conference.name.en = 'New event';
  const setup = adminContext([
    response(200, {events: [{path: '/alpha'}], revision: 'manifest-1', can_manage: true}),
    response(200, {path: '/alpha', config: config(), yaml: 'alpha', revision: 'rev-alpha'}),
  ], ['/team-day', '/team-day']);
  await setup.hook.ready;

  setup.replies.push(
    response(201, {path: '/team-day', config: created, yaml: 'new menu', revision: 'rev-new', manifest_revision: 'manifest-2'}),
    response(200, {events: [{path: '/alpha'}, {path: '/team-day'}], revision: 'manifest-2', can_manage: true}),
    response(200, {path: '/team-day', config: created, yaml: 'new menu', revision: 'rev-new'}),
  );
  await setup.hook.addEvent();
  let request = setup.requests.find((entry) => entry.url === '/admin/api/events' && entry.options.method === 'POST');
  assert.deepEqual(JSON.parse(request.options.body), {path: '/team-day', revision: 'manifest-1'});
  assert.equal(request.options.headers['X-Manna-Admin'], '1');
  assert.equal(setup.hook.state.path, '/team-day');

  setup.replies.push(
    response(200, {deleted: '/team-day', revision: 'manifest-3'}),
    response(200, {events: [{path: '/alpha'}], revision: 'manifest-3', can_manage: true}),
    response(200, {path: '/alpha', config: config(), yaml: 'alpha', revision: 'rev-alpha'}),
  );
  await setup.hook.deleteEvent();
  request = setup.requests.find((entry) => entry.url === '/admin/api/events/team-day' && entry.options.method === 'DELETE');
  assert.deepEqual(JSON.parse(request.options.body), {revision: 'manifest-2', confirmation: '/team-day'});
  assert.equal(setup.hook.state.path, '/alpha');
  assert.match(setup.elements['editor-status'].textContent, /permanently removed/i);
});

test('event removal requires the exact path before sending a request', async () => {
  const setup = adminContext([
    response(200, {events: [{path: '/alpha'}], revision: 'manifest-1', can_manage: true}),
    response(200, {path: '/alpha', config: config(), yaml: 'alpha', revision: 'rev-alpha'}),
  ], ['/wrong']);
  await setup.hook.ready;
  const requestsBefore = setup.requests.length;
  await setup.hook.deleteEvent();
  assert.equal(setup.requests.length, requestsBefore);
  assert.match(setup.elements['editor-status'].textContent, /must exactly match/);
});

test('visual editor loads structured menu and publishes form changes', async () => {
  const published = config();
  published.conference.name.en = 'Updated event';
  const setup = adminContext([
    response(200, {events: [{path: '/alpha'}, {path: '/beta'}]}),
    response(200, {path: '/alpha', config: config(), yaml: 'name: Test\n', revision: 'rev-1'}),
  ]);
  await setup.hook.ready;

  assert.equal(setup.elements['event-select'].options.length, 2);
  assert.equal(setup.elements['event-select'].value, '/alpha');
  assert.match(setup.elements['menu-form'].innerHTML, /Event details/);
  assert.match(setup.elements['menu-form'].innerHTML, /Girly vibes \(default\)/);
  assert.match(setup.elements['menu-form'].innerHTML, /Mazel Tov/);
  assert.doesNotMatch(setup.elements['menu-form'].innerHTML, /yaml-editor/);
  assert.equal(setup.elements['public-menu-link'].href, '/alpha');
  assert.equal(setup.elements['preview-panel'].hidden, false);
  assert.match(setup.elements['menu-preview'].srcdoc, /focus-card/);
  assert.match(setup.elements['menu-preview'].srcdoc, /Mittagessen/);
  assert.equal(setup.hook.isDirty(), false);

  setup.hook.updateField({dataset: {path: encodeURIComponent(JSON.stringify(['conference', 'name', 'en']))}, value: 'Updated event'});
  assert.equal(setup.hook.state.config.conference.name.en, 'Updated event');
  setup.hook.state.previewLanguage = 'en';
  setup.hook.render();
  assert.match(setup.elements['menu-preview'].srcdoc, /Updated event/);
  assert.equal(setup.hook.isDirty(), true);
  assert.equal(setup.elements['publish-button'].disabled, false);

  setup.replies.push(response(200, {path: '/alpha', config: published, yaml: 'name: Updated event\n', revision: 'rev-2'}));
  await setup.hook.publishMenu();
  const request = setup.requests.at(-1);
  assert.equal(request.url, '/admin/api/events/alpha');
  assert.equal(request.options.method, 'PUT');
  const payload = JSON.parse(request.options.body);
  assert.equal(payload.config.conference.name.en, 'Updated event');
  assert.equal(payload.revision, 'rev-1');
  assert.equal(setup.hook.isDirty(), false);
});

test('draft preview updates its existing document without reloading the iframe', async () => {
  const setup = adminContext([
    response(200, {events: [{path: '/alpha'}]}),
    response(200, {path: '/alpha', config: config(), yaml: 'menu', revision: 'rev-1'}),
  ]);
  await setup.hook.ready;

  const preview = setup.elements['menu-preview'];
  const initialSource = preview.srcdoc;
  preview.contentDocument = {
    documentElement: {lang: 'de', dir: 'ltr'},
    body: {innerHTML: ''},
    scrollingElement: {scrollTop: 120},
  };
  setup.hook.updateField({dataset: {path: encodeURIComponent(JSON.stringify(['conference', 'name', 'en']))}, value: 'Smooth update'});
  assert.equal(preview.contentDocument.body.innerHTML, '');

  setup.hook.state.previewLanguage = 'en';
  setup.hook.flushPreview();
  assert.equal(preview.srcdoc, initialSource);
  assert.match(preview.contentDocument.body.innerHTML, /Smooth update/);
  assert.equal(preview.contentDocument.scrollingElement.scrollTop, 120);
});

test('entries and items can be added and removed without YAML editing', async () => {
  const setup = adminContext([
    response(200, {events: [{path: '/alpha'}]}),
    response(200, {path: '/alpha', config: config(), revision: 'rev-1'}),
  ]);
  await setup.hook.ready;

  setup.hook.addAt(['permanent', 'coffee'], 'item');
  assert.equal(setup.hook.state.config.permanent.coffee.length, 1);
  setup.hook.addAt(['days'], 'day');
  assert.equal(setup.hook.state.config.days.length, 2);
  setup.hook.addAt(['tags'], 'tag');
  assert.equal(Object.keys(setup.hook.state.config.tags.new_tag).length, 0);
  setup.hook.removeAt(['permanent', 'coffee', 0]);
  assert.equal(setup.hook.state.config.permanent.coffee.length, 0);
  assert.equal(setup.hook.isDirty(), true);
});

test('validation and conflicts preserve unpublished form data', async () => {
  const setup = adminContext([
    response(200, {events: [{path: '/alpha'}]}),
    response(200, {path: '/alpha', config: config(), revision: 'rev-1'}),
  ]);
  await setup.hook.ready;
  setup.hook.updateField({dataset: {path: encodeURIComponent(JSON.stringify(['days', 0, 'date']))}, value: ''});

  setup.replies.push(response(422, {error: 'days[0].date must use YYYY-MM-DD'}));
  await setup.hook.validateMenu();
  assert.equal(setup.hook.state.config.days[0].date, '');
  assert.match(setup.elements['editor-status'].textContent, /YYYY-MM-DD/);

  setup.replies.push(response(409, {error: 'the event changed after it was loaded'}));
  await setup.hook.publishMenu();
  assert.equal(setup.hook.state.config.days[0].date, '');
  assert.match(setup.elements['editor-status'].textContent, /reload before publishing/i);

  const unload = {preventDefault() { this.prevented = true; }};
  setup.windowListeners.beforeunload(unload);
  assert.equal(unload.prevented, true);
});

test('YAML can be edited directly and synchronized back to forms', async () => {
  const original = 'conference:\n  name: {de: Test, en: Test}\n';
  const updated = 'conference:\n  name: {de: Test, en: Imported}\n';
  const imported = config();
  imported.conference.name.en = 'Imported';
  const setup = adminContext([
    response(200, {events: [{path: '/alpha'}]}),
    response(200, {path: '/alpha', config: config(), yaml: original, revision: 'rev-1'}),
  ]);
  await setup.hook.ready;

  setup.replies.push(response(200, {valid: true, yaml: original, config: config()}));
  await setup.hook.switchTab('yaml');
  assert.match(setup.elements['menu-form'].innerHTML, /Direct YAML editor/);
  assert.equal(setup.hook.isDirty(), false);

  setup.hook.updateField({dataset: {yaml: 'true'}, value: updated});
  assert.equal(setup.hook.isDirty(), true);
  setup.replies.push(response(200, {path: '/alpha', config: imported, yaml: updated, revision: 'rev-2'}));
  await setup.hook.publishMenu();
  const payload = JSON.parse(setup.requests.at(-1).options.body);
  assert.equal(payload.yaml, updated);
  assert.equal(payload.config, undefined);
  assert.equal(setup.hook.isDirty(), false);

  setup.replies.push(response(200, {valid: true, yaml: updated, config: imported}));
  await setup.hook.switchTab('event');
  assert.equal(setup.hook.state.config.conference.name.en, 'Imported');
  assert.equal(setup.hook.isDirty(), false);
});

test('invalid stored YAML opens in the advanced editor and can be repaired', async () => {
  const invalid = 'conference: [invalid';
  const repaired = 'conference:\n  name: repaired\n';
  const repairedConfig = config();
  const setup = adminContext([
    response(200, {events: [{path: '/alpha'}]}),
    response(200, {path: '/alpha', yaml: invalid, revision: 'rev-invalid'}),
  ]);
  await setup.hook.ready;

  assert.equal(setup.hook.state.tab, 'yaml');
  assert.equal(setup.hook.state.config, null);
  assert.match(setup.elements['menu-form'].innerHTML, /conference: \[invalid/);
  assert.equal(setup.elements['validate-button'].disabled, false);
  assert.match(setup.elements['editor-status'].textContent, /invalid YAML/i);

  setup.hook.updateField({dataset: {yaml: 'true'}, value: repaired});
  assert.equal(setup.elements['publish-button'].disabled, false);
  setup.replies.push(response(200, {path: '/alpha', revision: 'rev-fixed', yaml: repaired, config: repairedConfig}));
  await setup.hook.publishMenu();
  const payload = JSON.parse(setup.requests.at(-1).options.body);
  assert.equal(payload.yaml, repaired);
  assert.equal(payload.revision, 'rev-invalid');
  assert.equal(setup.hook.isDirty(), false);
});

test('publishing synchronizes YAML and form dirty baselines', async () => {
  const originalYAML = 'name: A\n';
  const publishedYAML = 'name: B\n';
  const original = config();
  const published = config();
  published.conference.name.en = 'B';
  const setup = adminContext([
    response(200, {events: [{path: '/alpha'}]}),
    response(200, {path: '/alpha', config: original, yaml: originalYAML, revision: 'rev-1'}),
  ]);
  await setup.hook.ready;

  setup.replies.push(response(200, {valid: true, yaml: originalYAML, config: original}));
  await setup.hook.switchTab('yaml');
  setup.replies.push(response(200, {valid: true, yaml: originalYAML, config: original}));
  await setup.hook.switchTab('event');
  setup.hook.updateField({dataset: {path: encodeURIComponent(JSON.stringify(['conference', 'name', 'en']))}, value: 'B'});
  setup.replies.push(response(200, {path: '/alpha', revision: 'rev-2', yaml: publishedYAML, config: published}));
  await setup.hook.publishMenu();
  assert.equal(setup.hook.isDirty(), false);

  setup.hook.updateField({dataset: {path: encodeURIComponent(JSON.stringify(['conference', 'name', 'en']))}, value: 'Test'});
  setup.replies.push(response(200, {valid: true, yaml: originalYAML, config: original}));
  await setup.hook.switchTab('yaml');
  assert.equal(setup.hook.state.yaml, originalYAML);
  assert.equal(setup.hook.isDirty(), true);
  assert.equal(setup.elements['publish-button'].disabled, false);
  const unload = {preventDefault() { this.prevented = true; }};
  setup.windowListeners.beforeunload(unload);
  assert.equal(unload.prevented, true);
});

test('comment-only YAML drafts survive switching to forms and back', async () => {
  const originalYAML = 'conference:\n  name: A\n';
  const draftYAML = '# keep this planning note\n' + originalYAML;
  const menu = config();
  const setup = adminContext([
    response(200, {events: [{path: '/alpha'}]}),
    response(200, {path: '/alpha', config: menu, yaml: originalYAML, revision: 'rev-1'}),
  ]);
  await setup.hook.ready;

  setup.replies.push(response(200, {valid: true, yaml: originalYAML, config: menu}));
  await setup.hook.switchTab('yaml');
  setup.hook.updateField({dataset: {yaml: 'true'}, value: draftYAML});

  setup.replies.push(response(200, {valid: true, yaml: draftYAML, config: menu}));
  await setup.hook.switchTab('event');
  assert.equal(JSON.parse(setup.requests.at(-1).options.body).yaml, draftYAML);
  assert.equal(setup.hook.isDirty(), true);
  assert.equal(setup.elements['publish-button'].disabled, false);

  setup.replies.push(response(200, {valid: true, yaml: draftYAML, config: menu}));
  await setup.hook.switchTab('yaml');
  const payload = JSON.parse(setup.requests.at(-1).options.body);
  assert.equal(payload.source_yaml, draftYAML);
  assert.ok(payload.config);
  assert.equal(setup.hook.state.yaml, draftYAML);
  assert.equal(setup.hook.isDirty(), true);
});

test('publishing forms uses draft YAML as the comment source', async () => {
  const originalYAML = 'conference:\n  name: A\n';
  const mixedYAML = '# supplier note\nconference:\n  name: B\n';
  const publishedYAML = '# supplier note\nconference:\n  name: B\n  location: New Hall\n';
  const original = config();
  const mixed = config();
  mixed.conference.name.en = 'B';
  const published = config();
  published.conference.name.en = 'B';
  published.conference.location.en = 'New Hall';
  const setup = adminContext([
    response(200, {events: [{path: '/alpha'}]}),
    response(200, {path: '/alpha', config: original, yaml: originalYAML, revision: 'rev-1'}),
  ]);
  await setup.hook.ready;

  setup.replies.push(response(200, {valid: true, yaml: originalYAML, config: original}));
  await setup.hook.switchTab('yaml');
  setup.hook.updateField({dataset: {yaml: 'true'}, value: mixedYAML});
  setup.replies.push(response(200, {valid: true, yaml: mixedYAML, config: mixed}));
  await setup.hook.switchTab('event');
  setup.hook.updateField({dataset: {path: encodeURIComponent(JSON.stringify(['conference', 'location', 'en']))}, value: 'New Hall'});

  setup.replies.push(response(200, {path: '/alpha', revision: 'rev-2', yaml: publishedYAML, config: published}));
  await setup.hook.publishMenu();
  const payload = JSON.parse(setup.requests.at(-1).options.body);
  assert.equal(payload.source_yaml, mixedYAML);
  assert.equal(payload.config.conference.location.en, 'New Hall');
  assert.equal(setup.hook.state.yaml, publishedYAML);
  assert.equal(setup.hook.isDirty(), false);
});

test('structural edits preserve open item panels and scroll position', async () => {
  const setup = adminContext([
    response(200, {events: [{path: '/alpha'}]}),
    response(200, {path: '/alpha', config: config(), yaml: 'menu', revision: 'rev-1'}),
  ]);
  await setup.hook.ready;

  const oldDetail = {dataset: {detail: 'item-path'}, open: true};
  const replacementDetail = {dataset: {detail: 'item-path'}, open: false};
  const form = setup.elements['menu-form'];
  form.querySelectorAll = (selector) => {
    if (selector === 'details[open][data-detail]') return [oldDetail];
    if (selector === 'details[data-detail]') return [replacementDetail];
    return [];
  };
  let restoredScroll;
  setup.window.scrollY = 480;
  setup.window.scrollTo = ({top}) => { restoredScroll = top; };

  setup.hook.render();
  assert.equal(replacementDetail.open, true);
  assert.equal(restoredScroll, 480);
});

test('schedule always labels each day with its number, weekday, and date', async () => {
  const setup = adminContext([
    response(200, {events: [{path: '/alpha'}]}),
    response(200, {path: '/alpha', config: config(), yaml: 'menu', revision: 'rev-1'}),
  ]);
  await setup.hook.ready;
  setup.hook.state.tab = 'schedule';
  setup.hook.render();

  assert.match(setup.elements['menu-form'].innerHTML, /Day 1/);
  assert.match(setup.elements['menu-form'].innerHTML, /Samstag/);
  assert.match(setup.elements['menu-form'].innerHTML, /2026/);
  assert.match(setup.elements['menu-form'].innerHTML, /September/);
  assert.doesNotMatch(setup.elements['menu-form'].innerHTML, /class="day-card"[^>]* open/);
});

test('live preview follows the selected language and day', async () => {
  const menu = config();
  menu.days.push({date: '2026-09-27', food_trucks: [], services: [{
    id: 'dinner', sold_out: false, title: {de: 'Abendessen', en: 'Dinner'},
    subtitle: {de: '', en: ''}, from: '18:00', until: '19:00', items: [],
  }]});
  const setup = adminContext([
    response(200, {events: [{path: '/alpha'}]}),
    response(200, {path: '/alpha', config: menu, yaml: 'menu', revision: 'rev-1'}),
  ]);
  await setup.hook.ready;

  setup.hook.state.previewLanguage = 'en';
  setup.hook.state.previewDay = 1;
  setup.hook.render();
  const preview = setup.elements['menu-preview'].srcdoc;
  assert.match(preview, /Dinner/);
  assert.match(preview, /Drinks &amp; Snacks/);
  assert.doesNotMatch(preview, /Abendessen/);
});

test('permanent items show every configured size price in their summary', async () => {
  const menu = config();
  menu.permanent.coffee.push({
    id: 'coffee', sold_out: false, name: {de: 'Kaffee', en: 'Coffee'},
    description: {}, variants: [], tags: [], price_small: 150, price_normal: 200, price_large: 280,
  });
  const setup = adminContext([
    response(200, {events: [{path: '/alpha'}]}),
    response(200, {path: '/alpha', config: menu, yaml: 'menu', revision: 'rev-1'}),
  ]);
  await setup.hook.ready;
  setup.hook.state.tab = 'permanent';
  setup.hook.render();

  const rendered = setup.elements['menu-form'].innerHTML;
  assert.match(rendered, /Small €1\.50/);
  assert.match(rendered, /Normal €2\.00/);
  assert.match(rendered, /Large €2\.80/);
});

test('event logo upload accepts a small PNG and updates the form filename', async () => {
  const setup = adminContext([
    response(200, {events: [{path: '/alpha'}]}),
    response(200, {path: '/alpha', config: config(), yaml: 'menu', revision: 'rev-1'}),
  ]);
  await setup.hook.ready;

  const file = {name: 'brand.png', type: 'image/png', size: 1024};
  setup.replies.push(response(201, {logo: 'alpha-logo.png'}));
  await setup.hook.uploadLogo({files: [file], value: 'brand.png'});
  const request = setup.requests.at(-1);
  assert.equal(request.url, '/admin/api/events/alpha/logo');
  assert.equal(request.options.method, 'POST');
  assert.equal(request.options.headers['Content-Type'], undefined);
  assert.equal(request.options.body.entries[0][0], 'logo');
  assert.equal(setup.hook.state.config.conference.logo, 'alpha-logo.png');
  assert.equal(setup.hook.state.previewLogo.filename, 'alpha-logo.png');
  assert.match(setup.elements['menu-preview'].srcdoc, /data:image\/png;base64,dGVzdA==/);
  assert.equal(setup.hook.isDirty(), true);
  assert.match(setup.elements['editor-status'].textContent, /publish the menu/i);

  setup.hook.updateField({dataset: {path: encodeURIComponent(JSON.stringify(['conference', 'logo']))}, value: ''});
  assert.equal(setup.hook.state.previewLogo, null);
  assert.doesNotMatch(setup.elements['menu-preview'].srcdoc, /data:image\/png/);
  assert.match(setup.elements['menu-preview'].srcdoc, /\/static\/logo.png/);

  setup.hook.updateField({dataset: {path: encodeURIComponent(JSON.stringify(['conference', 'logo']))}, value: 'draft-logo.png'});
  assert.match(setup.elements['menu-preview'].srcdoc, /Logo preview unavailable/);
  assert.doesNotMatch(setup.elements['menu-preview'].srcdoc, /\/branding\/alpha/);

  setup.replies.push(response(201, {logo: 'alpha-logo.png'}));
  await setup.hook.uploadLogo({files: [file], value: 'brand.png'});
  setup.hook.state.tab = 'yaml';
  const withoutLogo = config();
  setup.replies.push(response(200, {valid: true, yaml: 'logo removed', config: withoutLogo}));
  await setup.hook.validateMenu();
  assert.equal(setup.hook.state.previewLogo, null);
});
