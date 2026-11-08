// One real P20 Web login, inside the caller's existing private Linux netns.
// Never emit CDP messages, response bodies, URLs, credentials or Chrome logs.
import { spawn } from 'node:child_process';
import { createHash, X509Certificate } from 'node:crypto';
import { lstat, mkdtemp, readFile, realpath, rm } from 'node:fs/promises';
import path from 'node:path';
import { connect as connectTLS } from 'node:tls';

const CHROME = '/opt/google/chrome/chrome';
const ORIGIN = 'https://127.0.0.1';
const result = {
  configAccepted: false, loginAccepted: false, gatewayConnected: false,
  selfAccepted: false, workspaceVisible: false,
};
const stop = new AbortController();
const deadline = setTimeout(() => stop.abort(), 95000);
const interrupt = () => stop.abort();
process.once('SIGTERM', interrupt);
process.once('SIGINT', interrupt);
const check = ok => { if (!ok) throw new Error('browser smoke rejected'); };
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(read, ms, signal = stop.signal) {
  const end = Date.now() + ms;
  while (!signal.aborted && Date.now() < end) {
    const value = await read();
    if (value) return value;
    await sleep(100);
  }
  throw new Error('browser smoke deadline');
}

async function privateInput() {
  const chunks = [];
  let bytes = 0;
  for await (const chunk of process.stdin) {
    bytes += chunk.length;
    check(bytes <= 8192);
    chunks.push(chunk);
  }
  const data = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(Buffer.concat(chunks)));
  check(data && !Array.isArray(data) && typeof data === 'object' &&
    Object.keys(data).sort().join(',') === 'certificateSPKI,email,password,userID');
  check(typeof data.email === 'string' && data.email.length <= 254 && /^[^\s@]+@[^\s@]+$/.test(data.email));
  check(typeof data.password === 'string' && data.password.length > 0 && data.password.length <= 256 && !/[\x00-\x1f\x7f]/.test(data.password));
  check(typeof data.userID === 'string' && data.userID.length > 0 && data.userID.length <= 256 && !/[\s\x00-\x1f\x7f]/.test(data.userID));
  check(typeof data.certificateSPKI === 'string' && /^[A-Za-z0-9+/]{43}=$/.test(data.certificateSPKI));
  const pin = Buffer.from(data.certificateSPKI, 'base64');
  check(pin.length === 32 && pin.toString('base64') === data.certificateSPKI);
  return data;
}

// This one local, credential-free handshake accepts no application bytes.
// Check the actual leaf, SAN, validity and exact SPKI before launching Chrome.
async function checkTLSLeaf(pin) {
  await new Promise((resolve, reject) => {
    const socket = connectTLS({ host: '127.0.0.1', port: 443, minVersion: 'TLSv1.2', rejectUnauthorized: false });
    let timer;
    const finish = ok => {
      clearTimeout(timer);
      socket.destroy();
      if (ok) resolve(); else reject(new Error('local leaf rejected'));
    };
    timer = setTimeout(() => finish(false), 5000);
    socket.once('error', () => finish(false));
    socket.once('secureConnect', () => {
      try {
        const leaf = new X509Certificate(socket.getPeerCertificate().raw);
        const spki = createHash('sha256').update(leaf.publicKey.export({ type: 'spki', format: 'der' })).digest('base64');
        const now = Date.now();
        finish(spki === pin && leaf.checkIP('127.0.0.1') === '127.0.0.1' &&
          Date.parse(leaf.validFrom) <= now && now <= Date.parse(leaf.validTo));
      } catch { finish(false); }
    });
  });
}

class CDP {
  constructor(socket) {
    this.socket = socket;
    this.id = 0;
    this.pending = new Map();
    this.event = () => {};
    this.abort = () => this.rejectPending();
    stop.signal.addEventListener('abort', this.abort, { once: true });
    socket.addEventListener('message', ({ data }) => {
      try {
        check(typeof data === 'string' && data.length <= (2 << 20));
        const message = JSON.parse(data);
        const pending = this.pending.get(message.id);
        if (pending) {
          clearTimeout(pending.timer);
          this.pending.delete(message.id);
          if (message.error) pending.reject(new Error('CDP command rejected'));
          else pending.resolve(message.result);
        } else if (message.method) {
          this.event(message);
        }
      } catch { stop.abort(); }
    });
    socket.addEventListener('close', () => this.rejectPending());
    socket.addEventListener('error', () => this.rejectPending());
  }
  call(method, params = {}, sessionId, timeout = 7000) {
    if (stop.signal.aborted && method !== 'Browser.close') return Promise.reject(new Error('CDP cancelled'));
    return new Promise((resolve, reject) => {
      const id = ++this.id;
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error('CDP deadline'));
      }, timeout);
      this.pending.set(id, { resolve, reject, timer });
      try { this.socket.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) })); }
      catch {
        clearTimeout(timer);
        this.pending.delete(id);
        reject(new Error('CDP send rejected'));
      }
    });
  }
  rejectPending() {
    for (const { timer, reject } of this.pending.values()) {
      clearTimeout(timer);
      reject(new Error('CDP closed'));
    }
    this.pending.clear();
  }
  close() {
    stop.signal.removeEventListener('abort', this.abort);
    this.event = () => {};
    this.rejectPending();
    this.socket.close();
  }
}

async function connectCDP(endpoint) {
  const socket = new WebSocket(endpoint);
  try {
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('CDP open deadline')), 5000);
      socket.addEventListener('open', () => { clearTimeout(timer); resolve(); }, { once: true });
      socket.addEventListener('error', () => { clearTimeout(timer); reject(new Error('CDP open rejected')); }, { once: true });
    });
    return new CDP(socket);
  } catch {
    socket.close();
    throw new Error('CDP unavailable');
  }
}

function httpsPath(raw) {
  try {
    const url = new URL(raw);
    return url.origin === ORIGIN && !url.username && !url.password && !url.search && !url.hash ? url.pathname : '';
  } catch { return ''; }
}
function gatewayURL(raw) {
  try {
    const url = new URL(raw);
    return url.origin === 'wss://127.0.0.1' && url.pathname === '/msg_gateway' &&
      !url.username && !url.password && !url.hash;
  } catch { return false; }
}
const LOGIN_DOCUMENT = `location.origin === ${JSON.stringify(ORIGIN)} && location.pathname === '/' && location.hash === '#/login' && window.top === window`;

async function runBrowser(cdp, input) {
  const { targetId } = await cdp.call('Target.createTarget', { url: 'about:blank' });
  const { sessionId } = await cdp.call('Target.attachToTarget', { targetId, flatten: true });
  check(typeof sessionId === 'string' && sessionId.length > 0);
  const call = (method, params) => cdp.call(method, params, sessionId);
  const evaluate = async expression => {
    const r = await call('Runtime.evaluate', { expression, returnByValue: true });
    check(!r.exceptionDetails);
    return r.result?.value;
  };
  const selected = new Map();
  const sockets = new Set(), connected = new Set();
  let armed = false, loginPosts = 0, rejected = false;
  const inspect = async (requestId, kind) => {
    const body = await call('Network.getResponseBody', { requestId });
    check(typeof body.body === 'string' && body.body.length <= (384 << 10));
    const bytes = Buffer.from(body.body, body.base64Encoded ? 'base64' : 'utf8');
    check(bytes.length <= (256 << 10));
    const envelope = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes));
    const data = envelope?.data;
    if (kind === 'config') {
      if (envelope?.errCode === 0 && data && typeof data.config === 'object' && data.config !== null && !Array.isArray(data.config) &&
          Object.values(data.config).every(v => typeof v === 'string')) result.configAccepted = true;
    } else if (kind === 'login') {
      result.loginAccepted = envelope?.errCode === 0 && data?.userID === input.userID &&
        typeof data?.chatToken === 'string' && data.chatToken.length > 0 &&
        typeof data?.imToken === 'string' && data.imToken.length > 0 &&
        typeof data?.serverInfo?.serverID === 'string' && data.serverInfo.serverID.length > 0 &&
        Array.isArray(data.serverInfo.domains) && data.serverInfo.domains.length === 1 && data.serverInfo.domains[0] === '127.0.0.1';
      if (!result.loginAccepted) rejected = true;
    } else if (kind === 'self' && envelope?.errCode === 0 && Array.isArray(data?.users) &&
        data.users.length === 1 && data.users[0]?.userID === input.userID) {
      result.selfAccepted = true;
    }
  };
  cdp.event = ({ method, params, sessionId: observedSession }) => {
    if (observedSession !== sessionId || !params) return;
    const { requestId } = params;
    if (method === 'Network.requestWillBeSent') {
      if (params.redirectResponse) selected.delete(requestId);
      const endpoint = httpsPath(params.request?.url);
      if (params.request?.method !== 'POST') return;
      let kind;
      if (!armed && endpoint === '/api/client_config/get') kind = 'config';
      if (endpoint === '/api/auth/login') {
        if (!armed || ++loginPosts !== 1 || params.redirectResponse) { rejected = true; return; }
        kind = 'login';
      }
      if (armed && endpoint === '/chat/user/find/full') kind = 'self';
      if (kind && !params.redirectResponse && selected.size < 32) selected.set(requestId, { endpoint, kind, received: false });
    } else if (method === 'Network.responseReceived') {
      const pending = selected.get(requestId), response = params.response;
      if (pending && response) pending.received = response.status === 200 &&
        !response.fromDiskCache && !response.fromServiceWorker && !response.fromPrefetchCache &&
        httpsPath(response.url) === pending.endpoint;
    } else if (method === 'Network.loadingFinished' || method === 'Network.loadingFailed') {
      const pending = selected.get(requestId);
      selected.delete(requestId);
      if (!pending) return;
      if (pending.received && method === 'Network.loadingFinished') {
        void inspect(requestId, pending.kind).catch(() => { if (pending.kind === 'login') rejected = true; });
      } else if (pending.kind === 'login') rejected = true;
    } else if (method === 'Network.webSocketCreated' && armed && sockets.size < 16 && gatewayURL(params.url)) {
      sockets.add(requestId);
    } else if (method === 'Network.webSocketHandshakeResponseReceived' && sockets.has(requestId) && params.response?.status === 101) {
      connected.add(requestId);
      result.gatewayConnected = true;
    } else if (method === 'Network.webSocketClosed') {
      sockets.delete(requestId);
      connected.delete(requestId);
      result.gatewayConnected = connected.size > 0;
    }
  };
  await call('Page.enable');
  await call('Runtime.enable');
  await call('Network.enable', { maxTotalBufferSize: 4 << 20, maxResourceBufferSize: 256 << 10, maxPostDataSize: 0 });
  await call('Network.setCacheDisabled', { cacheDisabled: true });
  await call('Emulation.setDeviceMetricsOverride', { width: 1280, height: 900, deviceScaleFactor: 1, mobile: false });
  const navigation = await call('Page.navigate', { url: ORIGIN + '/#/login' });
  check(!navigation.errorText);
  // Admit the actual same-origin issuer and original WASM exports before filling
  // credentials. The existing wrapper also waits for its SQL worker and initSDK.
  await until(async () => !rejected && result.configAccepted && await evaluate(`(${LOGIN_DOCUMENT}) &&
    window.isSecureContext && typeof window.initSDK === 'function' && typeof window.login === 'function' && !!document.querySelector('form')`), 30000);

  const click = async (selector, text) => {
    const point = await until(() => evaluate(`(() => {
      if (!(${LOGIN_DOCUMENT})) return null;
      const matches = [...document.querySelectorAll(${JSON.stringify(selector)})].filter(e =>
        e.getClientRects().length && !e.disabled && ${text === undefined ? 'true' : `e.textContent.trim() === ${JSON.stringify(text)}`});
      if (matches.length !== 1) return null;
      const e = matches[0], r = e.getBoundingClientRect(), x = (r.left + r.right) / 2, y = (r.top + r.bottom) / 2;
      return e.contains(document.elementFromPoint(x, y)) ? { x, y } : null;
    })()`), 5000);
    check(Number.isFinite(point.x) && Number.isFinite(point.y));
    await call('Input.dispatchMouseEvent', { ...point, type: 'mousePressed', button: 'left', clickCount: 1 });
    await call('Input.dispatchMouseEvent', { ...point, type: 'mouseReleased', button: 'left', clickCount: 1 });
  };
  const type = async (selector, value) => {
    await click(selector);
    check(await evaluate(`(${LOGIN_DOCUMENT}) && document.activeElement === document.querySelector(${JSON.stringify(selector)})`));
    await call('Input.insertText', { text: value });
  };
  await click('[role="tab"]', '邮箱');
  await type('form input#email', input.email);
  await type('form input[type="password"]', input.password);
  armed = true;
  await click('form:has(input#email):has(input[type="password"]) button[type="submit"]');

  let settled = 0;
  await until(async () => {
    check(!rejected);
    if (!result.loginAccepted || !result.gatewayConnected || !result.selfAccepted) { settled = 0; return false; }
    const workspace = await evaluate(`(() => {
      if (location.origin !== ${JSON.stringify(ORIGIN)} || location.pathname !== '/' || !/^#\\/chat(?:\\/|$)/.test(location.hash)) return false;
      const shell = document.querySelector('.app-shell'), work = shell?.querySelector('.app-workspace');
      return !!work?.getClientRects().length && getComputedStyle(work).visibility === 'visible' && !!shell.querySelector('button[aria-label="账号与个人设置"]') &&
        !shell.closest('.ant-spin-blur') && !shell.textContent.includes('正在连接消息服务') &&
        !shell.textContent.includes('消息服务连接已中断');
    })()`);
    // The real shell must be visible after the SDK removes the connection
    // banner and blocking login/sync spinner, not just after route navigation.
    settled = workspace === true ? settled + 1 : 0;
    return settled >= 3;
  }, 35000);
  check(loginPosts === 1 && !rejected);
  result.workspaceVisible = true;
  cdp.event = () => {};
}

async function main() {
  let profile, child, exit, cdp;
  let exited = false, cleanExit = false, success = false, cleanup = true;
  try {
    check(process.platform === 'linux' && process.getuid() !== 0 && typeof WebSocket === 'function');
    const input = await privateInput();
    const temporary = await realpath(process.env.TMPDIR);
    const directory = await lstat(temporary);
    check(directory.isDirectory() && directory.uid === process.getuid() && (directory.mode & 0o077) === 0);
    await checkTLSLeaf(input.certificateSPKI);
    check(!stop.signal.aborted);
    profile = await mkdtemp(path.join(temporary, 'product20-chrome-'));
    child = spawn(CHROME, [
      '--headless=new', '--remote-debugging-address=127.0.0.1', '--remote-debugging-port=0',
      '--user-data-dir=' + profile, '--ignore-certificate-errors-spki-list=' + input.certificateSPKI,
      '--no-first-run', '--no-default-browser-check', '--disable-background-networking',
      '--disable-component-update', '--disable-sync', '--no-proxy-server', 'about:blank',
    ], { stdio: 'ignore', cwd: process.cwd(), env: { PATH: '/usr/bin:/bin', HOME: profile, TMPDIR: profile } });
    exit = new Promise(resolve => {
      child.once('exit', (code, signal) => { exited = true; cleanExit = code === 0 && signal === null; resolve(true); });
      child.on('error', () => { if (!child.pid) { exited = true; resolve(true); } });
    });
    const endpoint = await until(async () => {
      check(!exited);
      const file = path.join(profile, 'DevToolsActivePort');
      try {
        const info = await lstat(file);
        check(info.isFile() && info.uid === process.getuid() && info.size <= 256);
        const [port, browserPath] = (await readFile(file, 'utf8')).trim().split('\n');
        if (!/^\d{1,5}$/.test(port) || Number(port) < 1 || Number(port) > 65535 ||
            !/^\/devtools\/browser\/[a-f0-9-]{36}$/.test(browserPath)) return undefined;
        return 'ws://127.0.0.1:' + port + browserPath;
      } catch (error) { if (error?.code === 'ENOENT') return undefined; throw error; }
    }, 12000);
    cdp = await connectCDP(endpoint);
    await runBrowser(cdp, input);
    success = Object.values(result).every(value => value === true);
  } catch {
    // Only the five bounded facts below leave the task-owned process.
  } finally {
    if (cdp) {
      cdp.event = () => {};
      await cdp.call('Browser.close', {}, undefined, 2000).catch(() => undefined);
      try { cdp.close(); } catch { cleanup = false; }
    }
    const waitExit = ms => Promise.race([exit, sleep(ms).then(() => false)]);
    if (child && !exited) {
      if (!(await waitExit(1500))) {
        try { child.kill('SIGTERM'); } catch { cleanup = false; }
      }
      if (!exited && !(await waitExit(2500))) {
        cleanup = false;
        try { child.kill('SIGKILL'); } catch { cleanup = false; }
        if (!exited && !(await waitExit(2500))) cleanup = false;
      }
    }
    if (profile && (!child || exited)) {
      try { await rm(profile, { recursive: true, force: true }); } catch { cleanup = false; }
    } else if (profile) cleanup = false;
  }
  return success && cleanup && cleanExit && !stop.signal.aborted;
}

let accepted = false;
try { accepted = await main(); } catch { /* no raw diagnostic */ }
clearTimeout(deadline);
process.removeListener('SIGTERM', interrupt);
process.removeListener('SIGINT', interrupt);
process.stdout.write(JSON.stringify(result) + '\n');
process.exitCode = accepted ? 0 : 1;
