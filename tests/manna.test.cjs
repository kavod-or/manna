const {test} = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');

test('animation cleans up at 30 seconds and prevents overlapping runs', () => {
  let click, timeout, duration, count = 0, removed = 0;
  const canvas = {setAttribute() {}, getContext: () => ({setTransform() {}}), remove() {removed++;}};
  const document = {querySelector: () => ({addEventListener: (_, cb) => {click = cb;}}), createElement: () => canvas, body: {append() {count++;}}};
  vm.runInNewContext(fs.readFileSync('web/static/manna.js', 'utf8').replace('export function startManna', 'function startManna') + '\nstartManna(); document.start = startManna;', {
    document, window: {innerWidth: 400, innerHeight: 800, matchMedia: () => ({matches: false}), addEventListener() {}, removeEventListener() {}},
    performance: {now: () => 0}, requestAnimationFrame: () => 1, cancelAnimationFrame() {},
    setTimeout: (cb, ms) => {timeout = cb; duration = ms; return 1;}, clearTimeout() {},
  });
  assert.equal(count, 1); assert.equal(duration, 30000);
  document.start(); assert.equal(count, 1);
  timeout(); assert.equal(removed, 1);
  document.start(); assert.equal(count, 2);
});

test('animation loads only on fifth click and failed downloads can retry', async () => {
  let click, loads = 0, starts = 0;
  const source = fs.readFileSync('web/static/app.js', 'utf8').split('// Keep only the trigger here;')[1].split('// A deliberate logo hold')[0]
    .replace(/import\('\/static\/manna\.js(?:\?v=[^']+)?'\)/, 'loadAnimation()');
  vm.runInNewContext('// Keep only the trigger here;' + source, {
    document: {querySelector: () => ({addEventListener: (_, cb) => {click = cb;}})},
    loadAnimation: async () => {loads++; if (loads === 1) throw Error('offline'); return {startManna: () => {starts++;}};},
  });
  assert.equal(loads, 0);
  for (let i = 0; i < 4; i++) await click();
  assert.equal(loads, 0);
  await click(); assert.equal(loads, 1); assert.equal(starts, 0);
  for (let i = 0; i < 5; i++) await click();
  assert.equal(loads, 2); assert.equal(starts, 1);
  assert.ok(!fs.readFileSync('web/templates/index.html', 'utf8').includes('/static/manna.js'));
});
