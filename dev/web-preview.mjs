import { parseArgs } from 'node:util';
import { createRequire } from 'node:module';
import { realpath, stat } from 'node:fs/promises';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { createFixtureObjectProxy } from './fixture-object-proxy.mjs';

export function localTarget(raw, websocket = false) {
  const url = new URL(raw);
  const protocols = websocket ? ['ws:', 'wss:'] : ['http:', 'https:'];
  if (!protocols.includes(url.protocol) || !['127.0.0.1', '[::1]', 'localhost'].includes(url.hostname) ||
      url.username || url.password || url.pathname !== '/' || url.search || url.hash) {
    throw new Error('Local preview targets must be loopback origins without credentials or paths.');
  }
  return url.origin;
}

export async function startWebPreview({ dist, routeTarget, chatTarget, imTarget, wsTarget, port = 7778,
  devTenant, devDomain, fixtureObjectCA, webRoot = resolve(import.meta.dirname, '../web') }) {
  if (!dist || !Number.isInteger(port) || port < 0 || port > 65535) throw new Error('A built dist directory and valid port are required.');
  const root = await realpath(dist);
  if (!(await stat(resolve(root, 'index.html'))).isFile()) throw new Error('The selected build has no index.html.');
  const proxy = {};
  const add = (prefix, target, strip = false, ws = false) => {
    if (!target) return;
    proxy[`^${prefix}(?:/|\\?|$)`] = {
      target: localTarget(target, ws), changeOrigin: false, ws,
      proxyTimeout: 25000, timeout: 30000,
      ...(strip ? { rewrite: (path) => { const suffix = path.slice(prefix.length); return suffix.startsWith('/') ? suffix : '/' + suffix; } } : {}),
    };
  };
  if (!routeTarget) throw new Error('An actual route service target is required.');
  if (Boolean(devTenant) !== Boolean(devDomain)) throw new Error('Both explicit development tenant and domain are required.');
  if (devTenant) {
    const domain = new URL(`https://${devDomain}`);
    if (!/^[A-Za-z0-9_-]{1,50}$/.test(devTenant) || domain.hostname !== devDomain ||
        domain.port || domain.username || domain.password || domain.pathname !== '/' || domain.search || domain.hash) {
      throw new Error('Invalid explicit development tenant identity.');
    }
  }
  add('/api', routeTarget);
  add('/chat', chatTarget, true);
  add('/im', imTarget, true);
  add('/ws', wsTarget, true, true);
  add('/msg_gateway', wsTarget, true, true);
  let objectProxy;
  if (fixtureObjectCA) {
    if (port < 1 || devTenant !== 'fixture-tenant' || devDomain !== 'luobo.fixture.test')
      throw new Error('Fixture object proxy requires the exact isolated media preview.');
    objectProxy = await createFixtureObjectProxy({ origin: `http://127.0.0.1:${port}`, caFile: fixtureObjectCA });
  }
  const require = createRequire(resolve(webRoot, 'package.json'));
  const { preview } = await import(pathToFileURL(require.resolve('vite')).href);
  return preview({
    configFile: false, envFile: false, root, base: '/', appType: 'spa', logLevel: 'silent',
    build: { outDir: root },
    plugins: [{ name: 'explicit-loopback-development-endpoints', configurePreviewServer(server) {
      if (objectProxy) { server.middlewares.use(objectProxy.middleware); server.httpServer.once('close', objectProxy.close); }
      server.middlewares.use((request, response, next) => {
        if (request.url !== '/__luobo_dev__/endpoints') return next();
        response.setHeader('Content-Type', 'application/json; charset=utf-8');
        response.setHeader('Cache-Control', 'no-store');
        if (!devTenant || request.method !== 'GET') { response.statusCode = 404; response.end('{}'); return; }
        const address = server.httpServer.address();
        response.end(JSON.stringify({ version: 1, issuer: `http://127.0.0.1:${address.port}`,
          serverID: devTenant, domain: devDomain }));
      });
    } }],
    preview: { host: '127.0.0.1', port, strictPort: true, open: false, cors: false, proxy,
      headers: { 'Cross-Origin-Embedder-Policy': 'credentialless', 'Cross-Origin-Opener-Policy': 'same-origin' } },
  });
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  try {
    const { values } = parseArgs({ options: Object.fromEntries(
      ['dist', 'route-target', 'chat-target', 'im-target', 'ws-target', 'port', 'dev-tenant', 'dev-domain', 'fixture-object-ca'].map((key) => [key, { type: 'string' }])) });
    const port = values.port === undefined ? 7778 : Number(values.port);
    if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error('Invalid port.');
    const server = await startWebPreview({ dist: values.dist, port, routeTarget: values['route-target'],
      chatTarget: values['chat-target'], imTarget: values['im-target'], wsTarget: values['ws-target'],
      devTenant: values['dev-tenant'], devDomain: values['dev-domain'], fixtureObjectCA: values['fixture-object-ca'] });
    const stop = () => { server.httpServer.closeAllConnections(); server.httpServer.close(() => process.exit(0)); };
    process.once('SIGINT', stop); process.once('SIGTERM', stop);
    console.log(JSON.stringify({ state: 'web_preview_with_real_service_proxy', url: `http://127.0.0.1:${port}/#/login`,
      routeTarget: localTarget(values['route-target']), backendReadinessNotAssumed: true }));
  } catch {
    console.error('Web preview startup failed. Supply an existing build, installed Web dependencies, and explicit local service targets.');
    process.exitCode = 1;
  }
}
