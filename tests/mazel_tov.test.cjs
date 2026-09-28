const {test} = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');

test('mazel tov mode shows cracked glass before delayed confetti and message', () => {
  const timers = new Map();
  let frame, removed = 0, completed = 0;
  const appended = [];
  const context = {
    setTransform() {}, clearRect() {}, save() {}, translate() {}, rotate() {}, fillRect() {}, restore() {},
  };
  const element = (name) => ({
    name,
    setAttribute() {},
    remove() {removed++;},
    getContext: () => context,
  });
  const source = fs.readFileSync('web/static/mazel-tov.js', 'utf8')
    .replace('export function startMazelTov', 'function startMazelTov') + '\ndocument.startMazelTov = startMazelTov;';
  const document = {
    createElement: element,
    body: {append(...nodes) {appended.push(...nodes);}},
  };
  vm.runInNewContext(source, {
    document,
    window: {
      innerWidth: 390, innerHeight: 844, devicePixelRatio: 2,
      matchMedia: () => ({matches: false}), addEventListener() {}, removeEventListener() {},
    },
    performance: {now: () => 0}, Math,
    setTimeout: (callback, ms) => {timers.set(ms, callback); return ms;}, clearTimeout() {},
    requestAnimationFrame: (callback) => {frame = callback; return 1;}, cancelAnimationFrame() {},
  });

  document.startMazelTov(() => {completed++;});
  assert.equal(appended.length, 1);
  assert.equal(appended[0].className, 'mazel-tov-glass');
  assert.equal(timers.has(2000), true);
  assert.equal(timers.has(30000), true);
  timers.get(2000)();
  assert.equal(appended[1].className, 'mazel-tov-confetti');
  assert.equal(appended[2].className, 'mazel-tov-message');
  assert.equal(appended[2].textContent, 'MAZEL TOV!');
  assert.equal(typeof frame, 'function');
  timers.get(30000)();
  assert.equal(removed, 3);
  assert.equal(completed, 1);
});

test('mazel tov starts after five language clicks and toggles off', async () => {
  let languageClick;
  let link, complete, loads = 0, starts = 0, stops = 0;
  const source = fs.readFileSync('web/static/app.js', 'utf8').split('// Five clicks on the language switch')[1]
    .replace(/import\('\/static\/mazel-tov\.js(?:\?v=[^']+)?'\)/, 'loadMazelTov()');
  vm.runInNewContext('// Five clicks on the language switch'+source, {
    loadMazelTov: async () => ({startMazelTov: (onStop) => {starts++; complete = onStop; return () => {stops++; onStop();};}}),
    document: {
      body: {dataset: {easterEggMode: 'mazel_tov'}},
      querySelector: () => ({addEventListener: (name, callback) => {languageClick = callback;}}),
      createElement: () => ({remove() {}}),
      head: {append(node) {loads++; link = node;}},
    },
  });

  assert.equal(loads, 0);
  for (let i = 0; i < 4; i++) languageClick();
  assert.equal(loads, 0);
  languageClick();
  assert.equal(loads, 1);
  link.onload();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(starts, 1);

  complete();
  for (let i = 0; i < 5; i++) languageClick();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(starts, 2);
  assert.equal(loads, 1);

  for (let i = 0; i < 5; i++) languageClick();
  assert.equal(stops, 1);
  assert.equal(loads, 1);
});
