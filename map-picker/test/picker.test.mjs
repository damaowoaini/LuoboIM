import { test } from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import { readFile } from 'node:fs/promises';

const source = await readFile(new URL('../public/picker.js', import.meta.url), 'utf8');
const nonce = 'a'.repeat(64);
const tick = () => new Promise(resolve => setImmediate(resolve));
const coordinate = (lng, lat) => ({ getLng: () => lng, getLat: () => lat });

function fixture({ configError = false, pois = [] } = {}) {
  class Element {
    constructor() { this.value = ''; this.dataset = {}; this.listeners = {}; this.children = []; this.textContent = ''; }
    addEventListener(name, callback) { this.listeners[name] = callback; }
    appendChild(value) { this.children.push(value); }
    replaceChildren() { this.children = []; }
    get childElementCount() { return this.children.length; }
  }
  const elements = new Map(['status', 'name', 'query', 'search', 'results'].map(name => [name, new Element()]));
  const maps = [], scripts = [], calls = [], candidates = [], states = [], conversions = [], timers = new Map();
  let timerId = 0, browserGPS = 0, destroyed = 0;
  const window = {
    location: { origin: 'https://map.example.test' }, addEventListener() {},
    navigator: { geolocation: { getCurrentPosition() { browserGPS += 1; throw Error('browser GPS forbidden'); } } },
    LuoboMapCandidateV1: { postMessage: body => candidates.push(JSON.parse(body)) },
    LuoboMapStateV1: { postMessage: body => states.push(JSON.parse(body)) },
    AMap: {
      Map: class {
        constructor(id, options) { this.options = options; this.events = {}; this.center = coordinate(0, 0); maps.push(this); }
        on(name, callback) { this.events[name] = callback; }
        getCenter() { return this.center; }
        setCenter(value) { this.center = coordinate(...value); }
        destroy() { destroyed += 1; }
      },
      Marker: class { hide() {} show() {} setPosition() {} },
      convertFrom(pair, type, callback) { conversions.push({ pair: [...pair], type, callback }); },
    },
  };
  const context = vm.createContext({
    window, document: { getElementById: id => elements.get(id), createElement: () => new Element(), head: { appendChild: script => scripts.push(script) } },
    TextEncoder, AbortController, URLSearchParams,
    setTimeout: (callback, ms) => { timers.set(++timerId, { callback, ms }); return timerId; },
    clearTimeout: id => timers.delete(id),
    fetch: async path => {
      calls.push(path);
      if (path === '/picker/config') {
        if (configError) return { ok: false };
        return { ok: true, headers: { get: () => null }, text: async () => JSON.stringify({ key: 'k'.repeat(32), serviceHost: window.location.origin + '/_AMapService' }) };
      }
      assert.ok(path.startsWith('/_AMapService/v3/place/text?'));
      return { ok: true, headers: { get: () => null }, text: async () => JSON.stringify({ status: '1', pois }) };
    },
  });
  vm.runInContext(source, context);
  const call = (method, args) => JSON.parse(vm.runInContext(`window.LuoboMapPickerV1.${method}(${JSON.stringify(args)})`, context));
  const init = () => call('init', { requestNonce: nonce, initialCenter: { lng: 0, lat: 0 } });
  const load = async () => { assert.equal(init().accepted, true); await tick(); scripts[0].onload(); maps[0].events.complete(); };
  const manual = (lng, lat) => maps[0].events.click({ lnglat: coordinate(lng, lat) });
  const convert = (commandId = 1, expectedSequence = 0) => call('convertGps', { requestNonce: nonce, commandId, expectedSequence, lng: 12, lat: 34 });
  return { window, context, elements, maps, scripts, calls, candidates, states, conversions, timers, call, init, load, manual, convert, browserGPS: () => browserGPS, destroyed: () => destroyed };
}

test('invalid init cannot load config or maps; nonce is a string and zero is a real center', async () => {
  const f = fixture();
  for (const args of [
    { requestNonce: [nonce], initialCenter: { lng: 0, lat: 0 } },
    { requestNonce: nonce, initialCenter: { lng: 181, lat: 0 } },
    { requestNonce: nonce, initialCenter: { lng: 0, lat: 0 }, gps: true },
  ]) assert.equal(f.call('init', args).accepted, false);
  assert.equal(f.calls.length, 0); assert.equal(f.maps.length, 0);
  await f.load();
  assert.equal(f.maps[0].options.center[0], 0); assert.equal(f.maps[0].options.center[1], 0);
  assert.equal(f.candidates.length, 0); assert.equal(f.browserGPS(), 0); assert.equal(f.conversions.length, 0);
  assert.deepEqual(Object.keys(f.window.LuoboMapPickerV1).sort(), ['convertGps', 'dispose', 'init']);
  assert.equal(f.init().accepted, false); assert.equal(f.calls.length, 1);
});

test('missing config sends only fixed config error; no provider script or GPS', async () => {
  const f = fixture({ configError: true }); f.init(); await tick();
  assert.deepEqual(f.states, [{ requestNonce: nonce, sequence: 0, state: 'error', code: 'config' }]);
  assert.equal(f.scripts.length, 0); assert.equal(f.maps.length, 0); assert.equal(f.browserGPS(), 0);
  assert.equal(f.convert().accepted, false);
});

test('manual zero point produces a bounded candidate, never a send or a GPS request', async () => {
  const f = fixture(); await f.load(); f.elements.get('name').value = '测试地点'; f.manual(0, 0);
  assert.deepEqual(f.candidates, [{ requestNonce: nonce, sequence: 1, lng: 0, lat: 0, name: '测试地点' }]);
  f.elements.get('name').value = 'https://evil.test'; f.manual(1, 2);
  assert.equal(f.candidates[1].name, ''); assert.equal(f.candidates[1].sequence, 2);
  f.manual(181, 2); assert.equal(f.candidates.length, 2);
  assert.equal(f.browserGPS(), 0); assert.equal(f.conversions.length, 0);
});

test('official GPS conversion publishes only its successful GCJ result', async () => {
  const f = fixture(); await f.load(); assert.equal(f.convert().accepted, true);
  assert.deepEqual(f.conversions[0].pair, [12, 34]); assert.equal(f.conversions[0].type, 'gps');
  assert.equal(f.candidates.length, 0);
  f.conversions[0].callback('complete', { info: 'ok', locations: [coordinate(12.1, 34.1)] });
  assert.deepEqual(f.candidates, [{ requestNonce: nonce, sequence: 1, lng: 12.1, lat: 34.1, name: '我的位置' }]);
  assert.equal(f.browserGPS(), 0);
});

test('manual reselection fences old conversions and failure preserves the chosen candidate', async () => {
  const f = fixture(); await f.load(); f.convert(); f.manual(8, 9);
  f.conversions[0].callback('complete', { info: 'ok', locations: [coordinate(12, 34)] });
  assert.equal(f.candidates.length, 1); assert.equal(f.candidates[0].lng, 8);
  assert.equal(f.convert(2, 0).accepted, false);
  assert.equal(f.convert(2, 1).accepted, true);
  f.conversions[1].callback('error', {});
  assert.equal(f.candidates.length, 1); assert.equal(f.states.at(-1).code, 'conversion');
  assert.equal(f.convert(2, 1).accepted, false);
});

test('new commands and disposal reject late callbacks without resurrecting a document', async () => {
  const f = fixture(); await f.load(); f.convert(1); f.convert(2);
  f.conversions[0].callback('complete', { info: 'ok', locations: [coordinate(1, 1)] });
  assert.equal(f.candidates.length, 0);
  assert.equal(f.call('dispose', { requestNonce: 'b'.repeat(64) }).accepted, false);
  assert.equal(f.call('dispose', { requestNonce: nonce }).accepted, true);
  f.conversions[1].callback('complete', { info: 'ok', locations: [coordinate(2, 2)] });
  assert.doesNotThrow(() => f.maps[0].events.dragend());
  assert.equal(f.candidates.length, 0); assert.equal(f.destroyed(), 1); assert.equal(f.init().accepted, false);
  assert.equal(f.convert(3).accepted, false);
});

test('map timeout remains terminal when script or completion arrives late', async () => {
  const f = fixture(); f.init(); await tick();
  [...f.timers.values()].find(x => x.ms === 18000).callback(); f.scripts[0].onload();
  assert.equal(f.maps.length, 0); assert.equal(f.states.at(-1).code, 'map_load');
  const g = fixture(); g.init(); await tick(); g.scripts[0].onload();
  [...g.timers.values()].find(x => x.ms === 18000).callback(); g.maps[0].events.complete();
  assert.equal(g.states.some(x => x.state === 'ready'), false); assert.equal(g.candidates.length, 0);
});

test('POI results require an explicit result click and preserve literal text', async () => {
  const f = fixture({ pois: [{ name: '<img src=x>', location: '0,0' }, { name: 'https://evil.test', location: '1,2' }] });
  await f.load(); f.elements.get('query').value = '公园';
  await f.elements.get('search').listeners.submit({ preventDefault() {} });
  assert.equal(f.candidates.length, 0); assert.equal(f.elements.get('results').childElementCount, 1);
  const button = f.elements.get('results').children[0]; assert.equal(button.textContent, '<img src=x>');
  button.listeners.click(); assert.equal(f.candidates[0].lng, 0); assert.equal(f.candidates[0].name, '<img src=x>');
  assert.equal(f.browserGPS(), 0);
});
