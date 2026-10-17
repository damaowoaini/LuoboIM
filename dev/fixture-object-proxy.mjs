// Explicit local fixture transport only. Never logs requests, signatures or bodies.
import http from 'node:http';
import https from 'node:https';
import tls from 'node:tls';
import { readFile, lstat } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { Transform } from 'node:stream';

export const objectPrefix = '/__luobo_dev_objects__';
export const objectProfile = 'luobo-task-objects-v1';
const caSHA256 = '1b6710c6160b1625a6535639f3ca43a8e1708895867eeefbb7ae7b98060b50ce';
function loopbackOrigin(origin) {
  try {
    const url = new URL(origin);
    return url.origin === origin && url.protocol === 'http:' &&
      ['127.0.0.1', 'localhost'].includes(url.hostname) && !url.username && !url.password;
  } catch { return false; }
}
const routes = [
  { route: 'storage', host: 'objects.luobo.fixture.test', prefix: '/luobo-stack/' },
  { route: 'alias', host: 'luobo.fixture.test', prefix: '/api/object/' },
];
const omitted = new Set(['host', 'connection', 'transfer-encoding', 'keep-alive', 'proxy-authorization',
  'proxy-authenticate', 'te', 'trailer', 'upgrade', 'cookie', 'set-cookie', 'token']);
const requestCap = 1024 * 1024, responseCap = 2 * 1024 * 1024;
function rawSuffix(raw) {
  if (typeof raw !== 'string' || raw.length > 16384 || /[\u0000-\u0020\u007f\\#]/u.test(raw)) return;
  const path = raw.split('?')[0];
  try { if (decodeURIComponent(path).split('/').some(part => part === '.' || part === '..')) return; }
  catch { return; }
  return raw;
}
export function objectRoute(raw) {
  for (const route of routes) {
    const prefix = `${objectPrefix}/${route.route}`;
    if (typeof raw === 'string' && raw.startsWith(prefix + route.prefix)) {
      const path = rawSuffix(raw.slice(prefix.length));
      if (path && path.split('?')[0].length > route.prefix.length) return { ...route, path };
    }
  }
}
export function objectRedirect(location) {
  if (typeof location !== 'string') return;
  for (const route of routes) {
    const base = `https://${route.host}`;
    if (location.startsWith(base + route.prefix)) {
      const path = rawSuffix(location.slice(base.length));
      if (path && path.split('?')[0].length > route.prefix.length) return `${objectPrefix}/${route.route}${path}`;
    }
  }
}
function fixtureAgent(ca) {
  const agent = new https.Agent({ keepAlive: false, maxSockets: 2, ca, rejectUnauthorized: true });
  agent.createConnection = (options, callback) => {
    const host = options.hostname ?? options.host;
    if (!routes.some(route => route.host === host) || Number(options.port) !== 443) {
      callback(new Error('fixture upstream refused')); return;
    }
    let settled = false;
    const finish = (error, socket) => { if (!settled) { settled = true; callback(error, socket); } };
    const tunnel = http.request({ hostname: '127.0.0.1', port: 38081, method: 'CONNECT',
      path: `${host}:443`, headers: { Host: `${host}:443` }, agent: false });
    tunnel.setTimeout(10000, () => tunnel.destroy(new Error('fixture CONNECT timed out')));
    tunnel.once('error', () => finish(new Error('fixture CONNECT unavailable')));
    tunnel.once('connect', (response, socket, head) => {
      if (response.statusCode !== 200 || head.length) { socket.destroy(); finish(new Error('fixture CONNECT refused')); return; }
      tunnel.setTimeout(0);
      const secure = tls.connect({ socket, servername: host, ca, rejectUnauthorized: true });
      secure.setTimeout(20000, () => secure.destroy(new Error('fixture TLS timed out')));
      secure.once('secureConnect', () => finish(null, secure));
      secure.once('error', () => finish(new Error('fixture verified TLS unavailable')));
    });
    tunnel.end();
  };
  return agent;
}
function bounded(cap, exceeded) {
  let size = 0;
  return new Transform({ transform(chunk, encoding, callback) {
    size += chunk.length;
    if (size > cap) { exceeded(); callback(new Error('fixture stream limit')); }
    else callback(null, chunk);
  } });
}
export async function createFixtureObjectProxy({ origin, caFile }) {
  if (!loopbackOrigin(origin)) throw new Error('fixture object origin refused');
  const info = await lstat(caFile);
  if (!info.isFile() || info.isSymbolicLink() || info.nlink !== 1 || info.size > 65536) throw new Error('fixture CA refused');
  const ca = await readFile(caFile);
  if (createHash('sha256').update(ca).digest('hex') !== caSHA256) throw new Error('fixture CA differs');
  const agent = fixtureAgent(ca);
  const middleware = (request, response, next) => {
    if (request.url === '/__luobo_dev__/object-transport') {
      if (request.method !== 'GET' || request.headers.host !== new URL(origin).host) { response.writeHead(404); response.end(); return; }
      response.setHeader('Content-Type', 'application/json'); response.setHeader('Cache-Control', 'no-store');
      response.end(JSON.stringify({ version: 1, issuer: origin, profile: objectProfile })); return;
    }
    if (!request.url?.startsWith(objectPrefix + '/')) return next();
    const route = objectRoute(request.url);
    const sourceOrigin = request.headers.origin;
    const crossSite = request.headers['sec-fetch-site'] === 'cross-site';
    if (!route || request.headers.host !== new URL(origin).host || crossSite || sourceOrigin && sourceOrigin !== origin ||
      !(route.route === 'storage' ? ['PUT', 'GET', 'HEAD'] : ['GET', 'HEAD']).includes(request.method)) {
      response.writeHead(403); response.end('fixture object route refused'); return;
    }
    const size = request.headers['content-length'];
    if (size !== undefined && (!/^\d+$/.test(size) || Number(size) > requestCap)) {
      response.writeHead(413); response.end('fixture object size refused'); return;
    }
    const headers = {};
    for (const [name, value] of Object.entries(request.headers)) if (!omitted.has(name.toLowerCase())) headers[name] = value;
    // Preserve signing authority and exact encoded path/query after stripping
    // only our local transport prefix. No URL normalization or re-signing.
    headers.host = route.host;
    const upstream = https.request({ hostname: route.host, port: 443, method: request.method,
      path: route.path, headers, agent, servername: route.host, rejectUnauthorized: true });
    let replied = false;
    const fail = () => {
      upstream.destroy();
      if (replied || response.headersSent) response.destroy();
      else { replied = true; response.writeHead(502); response.end('fixture object transport unavailable'); }
    };
    upstream.once('error', fail); upstream.setTimeout(25000, fail);
    request.once('aborted', () => upstream.destroy());
    response.once('close', () => upstream.destroy());
    upstream.once('response', incoming => {
      if (incoming.statusCode >= 300 && incoming.statusCode < 400) {
        const location = objectRedirect(incoming.headers.location);
        incoming.destroy();
        if (!location) { fail(); return; }
        replied = true; response.writeHead(incoming.statusCode, { Location: location, 'Cache-Control': 'no-store' }); response.end(); return;
      }
      const size = incoming.headers['content-length'];
      if (size !== undefined && (!/^\d+$/.test(size) || Number(size) > responseCap)) { incoming.destroy(); fail(); return; }
      for (const [name, value] of Object.entries(incoming.headers)) if (!omitted.has(name.toLowerCase()) && value !== undefined) response.setHeader(name, value);
      replied = true; response.writeHead(incoming.statusCode ?? 502);
      const stream = bounded(responseCap, () => { incoming.destroy(); response.destroy(); });
      incoming.once('error', () => response.destroy()); stream.once('error', () => response.destroy());
      incoming.pipe(stream).pipe(response);
    });
    const stream = bounded(requestCap, fail);
    stream.once('error', () => upstream.destroy()); request.once('error', () => upstream.destroy());
    request.pipe(stream).pipe(upstream);
  };
  return { middleware, close: () => agent.destroy() };
}
