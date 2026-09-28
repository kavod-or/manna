const {test} = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');

test('theme is fetched after five language clicks and reused on subsequent toggles', async () => {
  let languageClick;
  let downloads = 0, link, shown = false, message;
  let effectLoads = 0, starts = 0, stops = 0;
  const source = fs.readFileSync('web/static/app.js', 'utf8').split('// Five clicks on the language switch')[1]
    .replace(/import\('\/static\/hearts\.js(?:\?v=[^']+)?'\)/, 'loadHearts()');
  vm.runInNewContext('// Five clicks on the language switch'+source, {
    loadHearts: async () => { effectLoads++; return {startHearts: () => { starts++; return () => {stops++;}; }}; },
    document: {
      body: {dataset: {easterEggMode: 'girly_vibes'}},
      querySelector: (selector) => selector === '.language-switch' ? {addEventListener: (name, fn) => {languageClick = fn;}} : {after: (node) => {message = node;}},
      createElement: () => ({setAttribute() {}, remove() {}, addEventListener(name, fn) {this[name] = fn;}}),
      head: {append: (node) => {downloads++; link = node;}},
      documentElement: {classList: {add: () => {shown = true;}, toggle: () => {shown = !shown;}, contains: () => shown}},
    },
  });
  assert.equal(downloads, 0);
  for (let i = 0; i < 4; i++) languageClick();
  assert.equal(downloads, 0);
  languageClick();
  assert.equal(downloads, 1); assert.equal(shown, false);
  link.onload(); assert.equal(shown, true); assert.equal(message.textContent, 'For the girly vibes');
  assert.equal(effectLoads, 0);
  for (let i = 0; i < 4; i++) await message.click();
  assert.equal(effectLoads, 0);
  await message.click(); assert.equal(effectLoads, 1); assert.equal(starts, 1);
  for (let i = 0; i < 5; i++) languageClick();
  assert.equal(shown, false); assert.equal(downloads, 1); assert.equal(stops, 1);
  for (let i = 0; i < 5; i++) await message.click();
  assert.equal(effectLoads, 1);
  assert.ok(!fs.readFileSync('web/templates/index.html', 'utf8').includes('girly.css'));
});

test('no easter egg mode disables girly vibes and heart rain together', () => {
  const source = fs.readFileSync('web/static/app.js', 'utf8').split('// Five clicks on the language switch')[1];
  let queried = false;
  vm.runInNewContext('// Five clicks on the language switch'+source, {
    document: {body: {dataset: {easterEggMode: 'none'}}, querySelector: () => {queried = true;}},
  });
  assert.equal(queried, false);
});
