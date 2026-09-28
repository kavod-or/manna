const {test} = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');

test('configured languages match browser preference and otherwise use the first option', () => {
  for (const {browserLanguages, expected} of [
    {browserLanguages: ['ru-RU'], expected: 'ru'},
    {browserLanguages: ['es-ES'], expected: 'de'},
  ]) {
    const languages = ['de', 'en', 'ru'];
    const buttons = languages.map((language) => ({
      dataset: {language},
      setAttribute(name, value) { if (name === 'aria-pressed') this.pressed = value; },
      addEventListener() {},
    }));
    const content = languages.map((language) => ({dataset: {langContent: language}, hidden: language !== 'de'}));
    const root = {lang: 'de'};
    const document = {
      documentElement: root,
      body: {dataset: {timezone: 'Europe/Berlin'}},
      querySelectorAll: (selector) => ({'[data-language]': buttons, '[data-lang-content]': content}[selector] || []),
      querySelector: (selector) => ['.app-version', '.brand'].includes(selector) ? null : {setAttribute() {}},
      getElementById: () => null,
      addEventListener() {},
    };
    vm.runInNewContext(fs.readFileSync('web/static/app.js', 'utf8'), {
      document, performance: {getEntriesByType: () => []}, Intl, Date,
      navigator: {languages: browserLanguages}, location: {hash: ''},
      window: {addEventListener() {}}, setInterval() {},
    });
    const selected = languages.indexOf(expected);
    assert.equal(root.lang, expected);
    assert.equal(content[selected].hidden, false);
    assert.equal(buttons[selected].pressed, 'true');
  }
});

test('conference clock selects days and meals, while preserving manual selection', () => {
  let now = '2026-10-12T10:45:00Z'; // 12:45 in Berlin
  let tick;
  const button = (index, date) => ({dataset: {dayButton: String(index), date}, setAttribute() {}, querySelector: () => ({}), addEventListener(type, callback) {this.click = callback;}});
  const days = [button(0, '2026-10-12'), button(1, '2026-10-13')];
  const panels = days.map((day) => ({dataset: {dayPanel: day.dataset.dayButton}, classList: {toggle() {}}, meals: [
    {dataset: {featuredFrom: '08:00', featuredUntil: '10:00'}},
    {dataset: {featuredFrom: '12:30', featuredUntil: '14:00'}},
    {dataset: {featuredFrom: '18:00', featuredUntil: '20:00'}},
  ], querySelectorAll() {return this.meals;}}));
  const root = {lang: 'de'};
  const document = {documentElement: root, body: {dataset: {timezone: 'Europe/Berlin'}},
    querySelectorAll: (selector) => ({'[data-day-button]': days, '[data-day-panel]': panels}[selector] || []),
    querySelector: (selector) => [".app-version", ".brand"].includes(selector) ? null : ({setAttribute() {}}), getElementById: () => null, addEventListener() {}};
  class ClockDate extends Date { constructor(value) {super(value === undefined ? now : value);} }
  vm.runInNewContext(fs.readFileSync('web/static/app.js', 'utf8'), {document, performance: {getEntriesByType: () => []}, Date: ClockDate, Intl, navigator: {languages: ['de']}, location: {hash: ''}, window: {addEventListener() {}}, setInterval: (callback) => {tick = callback;}});
  assert.equal(panels[0].hidden, false);
  assert.equal(panels[0].meals[1].hidden, false);
  now = '2026-10-12T12:00:00Z'; tick(); // lunch ends, dinner next
  assert.equal(panels[0].meals[2].hidden, false);
  now = '2026-10-12T22:01:00Z'; tick(); // midnight in Berlin
  assert.equal(panels[1].hidden, false);
  assert.equal(panels[1].meals[0].hidden, false);
  days[0].click(); tick();
  assert.equal(panels[0].hidden, false);
  assert.equal(panels[0].meals[2].hidden, false);
});

test('only reload clears fragments; navigation and topic clicks preserve them', () => {
  for (const navigationType of ['navigate', 'reload', 'back_forward']) {
    const location = {pathname: '/event', search: '?lang=en', hash: '#trucks-0'};
    const state = {example: true};
    let scrolled = 0;
    let hashchange;
    const panel = {dataset: {dayPanel: '0'}};
    const target = {closest: () => panel, scrollIntoView: () => {scrolled++;}};
    const history = {state, replaceState(nextState, title, url) {
      assert.equal(nextState, state);
      assert.equal(url, '/event?lang=en');
      location.hash = '';
    }};
    const document = {
      documentElement: {lang: 'en'}, body: {dataset: {timezone: 'Europe/Berlin'}},
      querySelectorAll: () => [],
      querySelector: (selector) => ['.brand', '.app-version'].includes(selector) ? null : {setAttribute() {}},
      getElementById: (id) => id === 'trucks-0' ? target : null,
      addEventListener() {},
    };
    vm.runInNewContext(fs.readFileSync('web/static/app.js', 'utf8'), {
      document, location, history, Intl, navigator: {languages: ['en']},
      performance: {getEntriesByType: () => [{type: navigationType}]},
      window: {addEventListener(type, callback) {if (type === 'hashchange') hashchange = callback;}},
      setInterval() {},
    });
    assert.equal(location.hash, navigationType === 'reload' ? '' : '#trucks-0');
    assert.equal(scrolled, navigationType === 'reload' ? 0 : 1);
    for (let click = 0; click < 2; click++) {
      location.hash = '#trucks-0';
      const before = scrolled;
      hashchange();
      assert.equal(scrolled, before + 1);
      assert.equal(location.hash, '#trucks-0');
    }
  }
});
