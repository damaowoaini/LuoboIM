import { test } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { once } from 'node:events';
import { configuration, handler } from '../server.mjs';

const env = { LUOBO_MAP_ORIGIN: 'https://map.example.test', LUOBO_AMAP_JS_KEY: 'k'.repeat(32), LUOBO_AMAP_SECURITY_CODE: 'a'.repeat(32) };
async function fixture(t, config, provider, now) {
  const server = http.createServer(handler(config, { provider, now }));
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise(resolve => { server.closeAllConnections(); server.close(resolve); }));
  const send = (path, headers = {}, method = 'GET') => new Promise((resolve, reject) => {
    const request = http.request({ host: '127.0.0.1', port: server.address().port, path, method,
      headers: { host: 'map.example.test', 'x-forwarded-proto': 'https', ...headers } }, response => {
      const chunks = []; response.on('data', chunk => chunks.push(chunk));
      response.on('end', () => resolve({ status: response.statusCode, body: Buffer.concat(chunks).toString(), headers: response.headers }));
    }); request.on('error', reject); request.end();
  });
  return send;
}

test('unconfigured service returns visible failure with zero upstream calls and restrictive headers', async t => {
  let calls = 0; const send = await fixture(t, configuration({}), () => { calls += 1; });
  const result = await send('/picker/config'); assert.equal(result.status, 503); assert.equal(calls, 0);
  assert.equal(result.headers['permissions-policy'].includes('geolocation=()'), true);
  assert.equal(result.headers['content-security-policy'].includes("frame-src 'none'"), true);
  assert.equal((await send('/picker')).status, 200); assert.equal((await send('/_AMapService/v3/place/text')).status, 503);
  assert.equal(configuration({ ...env, LUOBO_MAP_ORIGIN: 'http://map.example.test' }).enabled, false);
  assert.equal(configuration({ ...env, LUOBO_MAP_ORIGIN: 'https://u:p@map.example.test' }).enabled, false);
  assert.equal(configuration({ ...env, LUOBO_MAP_ORIGIN: 'https://map.example.test/path' }).enabled, false);
});

test('origin, method, route, key and JSONP abuse fail before the provider', async t => {
  let calls = 0; const send = await fixture(t, configuration(env), () => { calls += 1; });
  const denied = [
    ['/picker/config', { host: 'other.example.test' }, 'GET', 421],
    ['/picker/config', { origin: 'https://evil.test' }, 'GET', 403],
    ['/picker/config', { 'x-forwarded-proto': 'http' }, 'GET', 421],
    ['/picker/config', {}, 'POST', 405],
    ['/_AMapService/http://evil.test', {}, 'GET', 404],
    ['/_AMapService/v3/../v3/place/text', {}, 'GET', 400],
    ['/_AMapService/v3/place/text?key=other', {}, 'GET', 403],
    ['/_AMapService/v3/place/text?jscode=other', {}, 'GET', 400],
    ['/_AMapService/v3/place/text?callback=alert(1)', {}, 'GET', 400],
    ['/_AMapService/v3/place/text?keywords=a&keywords=b', {}, 'GET', 400],
  ];
  for (const [path, headers, method, expected] of denied) assert.equal((await send(path, headers, method)).status, expected, path);
  assert.equal(calls, 0);
});

test('fixed upstream query contains server code, while client responses contain no code or credentials', async t => {
  const calls = []; const send = await fixture(t, configuration(env), async (...args) => {
    calls.push(args); return { status: 200, type: 'application/json; charset=utf-8', body: '{"status":"1","pois":[]}' };
  });
  const config = await send('/picker/config'); assert.equal(config.status, 200);
  assert.equal(config.body.includes(env.LUOBO_AMAP_SECURITY_CODE), false);
  const result = await send('/_AMapService/v3/place/text?keywords=park', { cookie: 'secret', authorization: 'Bearer secret' });
  assert.equal(result.status, 200); assert.equal(calls[0].length, 2); assert.equal(calls[0][0], '/v3/place/text');
  const query = new URLSearchParams(calls[0][1]); assert.equal(query.get('key'), env.LUOBO_AMAP_JS_KEY);
  assert.equal(query.get('jscode'), env.LUOBO_AMAP_SECURITY_CODE);
  assert.equal(result.body.includes(env.LUOBO_AMAP_SECURITY_CODE), false); assert.equal(result.headers['set-cookie'], undefined);
});

test('upstream redirects, wrong types, oversize bodies and reflected code never reach the client', async t => {
  const responses = [
    { status: 302, type: 'application/json', body: 'redirect' },
    { status: 200, type: 'text/html', body: '<script>' },
    { status: 200, type: 'application/json', body: Buffer.alloc(512 * 1024 + 1) },
    { status: 200, type: 'application/json', body: env.LUOBO_AMAP_SECURITY_CODE },
  ];
  const send = await fixture(t, configuration(env), async () => responses.shift());
  for (let i = 0; i < 4; i += 1) {
    const result = await send('/_AMapService/v3/place/text'); assert.equal(result.status, 502);
    assert.equal(result.body, '{"error":"map_service_unavailable"}'); assert.equal(result.headers.location, undefined);
  }
});

test('concurrency cap rejects a fifth outstanding request and releases slots', async t => {
  const pending = []; const send = await fixture(t, configuration(env), () => new Promise(resolve => pending.push(resolve)));
  const active = Array.from({ length: 4 }, () => send('/_AMapService/v3/place/text'));
  for (let i = 0; pending.length < 4 && i < 100; i += 1) await new Promise(resolve => setTimeout(resolve, 2));
  assert.equal(pending.length, 4); assert.equal((await send('/_AMapService/v3/place/text')).status, 429);
  for (const resolve of pending) resolve({ status: 200, type: 'application/json', body: '{}' });
  assert.deepEqual((await Promise.all(active)).map(x => x.status), [200, 200, 200, 200]);
});
