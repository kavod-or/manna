const {test} = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');

test('theme is fetched only after a full hold and reused on subsequent toggles', async () => {
  const handlers = {};
  let callback, delay, downloads = 0, link, shown = false, message;
  let effectLoads = 0, starts = 0, stops = 0;
  const source = fs.readFileSync('web/static/app.js', 'utf8').split('// A deliberate logo hold')[1]
    .replace(/import\('\/static\/hearts\.js(?:\?v=[^']+)?'\)/, 'loadHearts()');
  vm.runInNewContext('// A deliberate logo hold'+source, {
    loadHearts: async () => { effectLoads++; return {startHearts: () => { starts++; return () => {stops++;}; }}; },
    document: {
      body: {dataset: {easterEggMode: 'girly_vibes'}},
      querySelector: (selector) => selector === '.brand' ? {addEventListener: (name, fn) => {handlers[name] = fn;}} : {after: (node) => {message = node;}},
      createElement: () => ({setAttribute() {}, remove() {}, addEventListener(name, fn) {this[name] = fn;}}),
      head: {append: (node) => {downloads++; link = node;}},
      documentElement: {classList: {add: () => {shown = true;}, toggle: () => {shown = !shown;}, contains: () => shown}},
    }, setTimeout: (fn, ms) => {callback = fn; delay = ms; return 1;}, clearTimeout: () => {callback = null;},
  });
  assert.equal(downloads, 0);
  handlers.pointerdown({button: 0, clientX: 0, clientY: 0});
  assert.equal(delay, 3000); assert.equal(downloads, 0);
  handlers.pointerup(); assert.equal(callback, null); assert.equal(downloads, 0);
  handlers.pointerdown({button: 0, clientX: 0, clientY: 0}); callback();
  assert.equal(downloads, 1); assert.equal(shown, false);
  link.onload(); assert.equal(shown, true); assert.equal(message.textContent, 'For the girly vibes');
  assert.equal(effectLoads, 0);
  for (let i = 0; i < 4; i++) await message.click();
  assert.equal(effectLoads, 0);
  await message.click(); assert.equal(effectLoads, 1); assert.equal(starts, 1);
  handlers.pointerdown({button: 0, clientX: 0, clientY: 0}); callback();
  assert.equal(shown, false); assert.equal(downloads, 1); assert.equal(stops, 1);
  for (let i = 0; i < 5; i++) await message.click();
  assert.equal(effectLoads, 1);
  assert.ok(!fs.readFileSync('web/templates/index.html', 'utf8').includes('girly.css'));
});

test('no easter egg mode disables girly vibes and heart rain together', () => {
  const source = fs.readFileSync('web/static/app.js', 'utf8').split('// A deliberate logo hold')[1];
  let queried = false;
  vm.runInNewContext('// A deliberate logo hold'+source, {
    document: {body: {dataset: {easterEggMode: 'none'}}, querySelector: () => {queried = true;}},
  });
  assert.equal(queried, false);
});
