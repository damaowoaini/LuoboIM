import http from 'node:http';
import https from 'node:https';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { resolve } from 'node:path';

const assets = new Map([
  ['/picker', ['index.html', 'text/html; charset=utf-8']],
  ['/picker/', ['index.html', 'text/html; charset=utf-8']],
  ['/picker/picker.js', ['picker.js', 'text/javascript; charset=utf-8']],
  ['/picker/picker.css', ['picker.css', 'text/css; charset=utf-8']],
]);
const services = new Set([
  '/v3/assistant/coordinate/convert',
  '/v3/geocode/regeo',
  '/v3/place/text',
  '/v3/assistant/inputtips',
]);
const contentPolicy = [
  "default-src 'none'",
  "script-src 'self' https://webapi.amap.com https://cache.amap.com",
  "style-src 'self' 'unsafe-inline' https://webapi.amap.com https://cache.amap.com",
  "img-src 'self' data: blob: https://*.amap.com https://*.autonavi.com",
  "connect-src 'self' https://*.amap.com https://*.autonavi.com",
  "worker-src 'self' blob:",
  "font-src 'self' https://*.amap.com",
  "frame-src 'none'", "frame-ancestors 'none'", "object-src 'none'",
  "base-uri 'none'", "form-action 'none'",
].join('; ');

export function configuration(env = process.env) {
  let origin;
  try {
    const u = new URL(env.LUOBO_MAP_ORIGIN);
    if (u.protocol !== 'https:' || u.username || u.password || u.pathname !== '/' ||
        u.search || u.hash || !u.hostname.includes('.') || u.hostname.endsWith('.')) throw Error();
    origin = u.origin;
  } catch { origin = null; }
  const key = env.LUOBO_AMAP_JS_KEY ?? '';
  const securityCode = env.LUOBO_AMAP_SECURITY_CODE ?? '';
  return Object.freeze({
    origin,
    key: /^[A-Za-z0-9]{32}$/.test(key) ? key : null,
    securityCode: /^[a-fA-F0-9]{32}$/.test(securityCode) ? securityCode : null,
    enabled: Boolean(origin && /^[A-Za-z0-9]{32}$/.test(key) && /^[a-fA-F0-9]{32}$/.test(securityCode)),
  });
}

export function upstreamRequest(path, query) {
  return new Promise((resolve, reject) => {
    const request = https.get({
      hostname: 'restapi.amap.com', port: 443, path: path + '?' + query,
      headers: { accept: 'application/json, text/javascript', 'user-agent': 'LuoboMapPicker/1' },
    }, response => {
      const parts = []; let size = 0;
      response.on('data', part => {
        size += part.length;
        if (size > 512 * 1024) { request.destroy(Error('response_limit')); return; }
        parts.push(part);
      });
      response.on('error', reject);
      response.on('end', () => resolve({
        status: response.statusCode,
        type: response.headers['content-type'] ?? '',
        body: Buffer.concat(parts),
      }));
    });
    request.setTimeout(8000, () => request.destroy(Error('provider_timeout')));
    request.on('error', reject);
  });
}

export function handler(config, { provider = upstreamRequest, now = Date.now, assetRoot = new URL('./public/', import.meta.url) } = {}) {
  let tokens = 30, last = now(), pending = 0;
  return async (request, response) => {
    response.setHeader('content-security-policy', contentPolicy);
    response.setHeader('permissions-policy', 'geolocation=(), camera=(), microphone=(), accelerometer=(), gyroscope=()');
    response.setHeader('referrer-policy', 'same-origin');
    response.setHeader('x-content-type-options', 'nosniff');
    response.setHeader('x-frame-options', 'DENY');
    response.setHeader('cache-control', 'no-store');
    response.setHeader('cross-origin-resource-policy', 'same-origin');
    const finish = (status, body, type = 'application/json; charset=utf-8') => {
      if (response.destroyed) return;
      response.writeHead(status, { 'content-type': type }); response.end(body);
    };
    const fail = (status, code) => finish(status, JSON.stringify({ error: code }));
    if (request.method !== 'GET') return fail(405, 'method_not_allowed');
    if (!request.url || request.url.length > 4096) return fail(414, 'request_too_large');
    let url;
    try { url = new URL(request.url, 'http://local.invalid'); } catch { return fail(400, 'invalid_request'); }
    if (!request.url.startsWith('/') || request.url.startsWith('//') || request.url.split('?')[0] !== url.pathname) return fail(400, 'invalid_path');
    // Only a loopback reverse proxy serves this process; it must overwrite these headers.
    if (config.origin) {
      const expected = new URL(config.origin);
      if (request.headers.host !== expected.host || request.headers['x-forwarded-proto'] !== 'https') return fail(421, 'wrong_origin');
      if (request.headers.origin && request.headers.origin !== config.origin) return fail(403, 'wrong_origin');
      if (request.headers['sec-fetch-site'] && !['same-origin', 'none'].includes(request.headers['sec-fetch-site'])) return fail(403, 'wrong_origin');
    }
    if (url.pathname === '/picker/config') {
      if (!config.enabled) return fail(503, 'map_configuration_unavailable');
      return finish(200, JSON.stringify({ key: config.key, serviceHost: config.origin + '/_AMapService' }));
    }
    if (assets.has(url.pathname)) {
      const [name, type] = assets.get(url.pathname);
      try { return finish(200, await readFile(new URL(name, assetRoot)), type); }
      catch { return fail(503, 'map_page_unavailable'); }
    }
    if (!url.pathname.startsWith('/_AMapService/')) return fail(404, 'not_found');
    if (!config.enabled) return fail(503, 'map_configuration_unavailable');
    const service = url.pathname.slice('/_AMapService'.length);
    if (!services.has(service)) return fail(404, 'service_not_allowed');
    if (url.search.length > 3072) return fail(414, 'request_too_large');
    const query = new URLSearchParams();
    for (const [key, value] of url.searchParams) {
      if (!/^[A-Za-z][A-Za-z0-9_]{0,63}$/.test(key) || value.length > 1024 || query.has(key) ||
          key.toLowerCase() === 'jscode' || /[\u0000-\u001f\u007f]/.test(value)) return fail(400, 'invalid_parameter');
      if (key.toLowerCase() === 'key' && (key !== 'key' || value !== config.key)) return fail(403, 'wrong_project_key');
      if (key === 'callback' && !/^[A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*$/.test(value)) return fail(400, 'invalid_callback');
      query.set(key, value);
    }
    query.set('key', config.key); query.set('jscode', config.securityCode);
    const time = now(); tokens = Math.min(30, tokens + Math.max(0, time - last) / 500); last = time;
    if (tokens < 1 || pending >= 4) return fail(429, 'map_busy');
    tokens -= 1; pending += 1;
    try {
      const result = await provider(service, query.toString());
      const body = Buffer.from(result.body);
      if (result.status !== 200 || body.length > 512 * 1024 ||
          !/^(application\/(?:json|javascript)|text\/(?:json|javascript))(?:;|$)/i.test(result.type) ||
          body.includes(Buffer.from(config.securityCode))) return fail(502, 'map_service_unavailable');
      return finish(200, body, result.type);
    } catch { return fail(502, 'map_service_unavailable'); }
    finally { pending -= 1; }
  };
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) {
  const port = Number(process.env.LUOBO_MAP_PORT ?? '8089');
  if (!Number.isInteger(port) || port < 1024 || port > 65535) throw Error('invalid map listen port');
  const server = http.createServer({ requestTimeout: 10000, headersTimeout: 10000 }, handler(configuration()));
  server.maxHeadersCount = 40; server.maxConnections = 64;
  server.listen(port, '127.0.0.1');
  for (const signal of ['SIGINT', 'SIGTERM']) process.once(signal, () => server.close());
}
