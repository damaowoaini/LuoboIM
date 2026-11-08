// Real P20 browser acceptance inside the caller's existing private Linux netns.
// Never emit CDP messages, response bodies, URLs, credentials or Chrome logs.
import { spawn } from 'node:child_process';
import { createHash, randomBytes, X509Certificate } from 'node:crypto';
import { once } from 'node:events';
import { lstat, mkdtemp, readFile, realpath, rm } from 'node:fs/promises';
import { createServer } from 'node:net';
import path from 'node:path';
import { connect as connectTLS } from 'node:tls';

const CHROME = '/opt/google/chrome/chrome';
const ORIGIN = 'https://127.0.0.1';
const probe = process.argv.length === 3 && process.argv[2] === '--probe';
const listen = process.argv.length === 3 && process.argv[2] === '--listen';
const observation = {
  stage: 'input', documentStatus: 0, configStatus: 0, configRequested: false, chromeFailure: 'none',
};
const loginChecks = () => ({
  configAccepted: false, loginAccepted: false, gatewayConnected: false,
  selfAccepted: false, workspaceVisible: false,
});
const result = loginChecks();
const stop = new AbortController();
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

async function privateInput(stream = process.stdin) {
  const chunks = [];
  let bytes = 0;
  for await (const chunk of stream) {
    bytes += chunk.length;
    check(bytes <= 8192);
    chunks.push(chunk);
  }
  const data = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(Buffer.concat(chunks)));
  check(data && !Array.isArray(data) && typeof data === 'object');
  const fields = Object.keys(data).sort().join(',');
  check(fields === 'certificateSPKI,email,password,userID' || fields === 'certificateSPKI,email,password,peer,userID');
  const checkAccount = account => {
    check(typeof account.email === 'string' && account.email.length <= 254 && /^[^\s@]+@[^\s@]+$/.test(account.email));
    check(typeof account.password === 'string' && account.password.length > 0 && account.password.length <= 256 && !/[\x00-\x1f\x7f]/.test(account.password));
    check(typeof account.userID === 'string' && account.userID.length > 0 && account.userID.length <= 256 && !/[\s\x00-\x1f\x7f]/.test(account.userID));
  };
  checkAccount(data);
  if (fields.includes(',peer,')) {
    check(data.peer && typeof data.peer === 'object' && Object.keys(data.peer).sort().join(',') === 'email,password,userID');
    checkAccount(data.peer);
    check(data.peer.userID !== data.userID && data.peer.email !== data.email);
  }
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
const CHAT_DOCUMENT = `location.origin === ${JSON.stringify(ORIGIN)} && location.pathname === '/' && /^#\\/chat(?:\\/|$)/.test(location.hash) && window.top === window`;

async function login(cdp, events, input, result, observation, browserContextId) {
  const { targetId } = await cdp.call('Target.createTarget', { url: 'about:blank', browserContextId });
  const { sessionId } = await cdp.call('Target.attachToTarget', { targetId, flatten: true });
  check(typeof sessionId === 'string' && sessionId.length > 0);
  const call = (method, params) => cdp.call(method, params, sessionId);
  const evaluate = async expression => {
    const r = await call('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
    check(!r.exceptionDetails);
    return r.result?.value;
  };
  const selected = new Map();
  const sockets = new Set(), connected = new Set();
  let armed = false, loginPosts = 0, rejected = false;
  let logoutSocket, logoutPosts = 0, logoutRevoked = false, logoutClosed = false;
  let loginTokens;
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
      else if (browserContextId) loginTokens = { chat: data.chatToken, im: data.imToken };
    } else if (kind === 'logout') {
      logoutRevoked = envelope?.errCode === 0 && data?.revoked === true &&
        data.sessionClosed === true && data.pending === false;
      if (!logoutRevoked) rejected = true;
    } else if (kind === 'self' && envelope?.errCode === 0 && Array.isArray(data?.users) &&
        data.users.length === 1 && data.users[0]?.userID === input.userID) {
      result.selfAccepted = true;
    }
  };
  events.set(sessionId, ({ method, params }) => {
    if (!params) return;
    const { requestId } = params;
    if (method === 'Network.requestWillBeSent') {
      if (params.redirectResponse) selected.delete(requestId);
      const endpoint = httpsPath(params.request?.url);
      if (params.request?.method !== 'POST') return;
      let kind;
      if (!armed && endpoint === '/api/client_config/get') {
        kind = 'config';
        observation.configRequested = true;
      }
      if (endpoint === '/api/auth/login') {
        if (!armed || ++loginPosts !== 1 || params.redirectResponse) { rejected = true; return; }
        kind = 'login';
      }
      if (armed && !logoutSocket && endpoint === '/chat/user/find/full') kind = 'self';
      if (endpoint === '/chat/account/logout') {
        if (!logoutSocket || ++logoutPosts !== 1 || params.redirectResponse) { rejected = true; return; }
        kind = 'logout';
      }
      if (kind && !params.redirectResponse && selected.size < 32) selected.set(requestId, { endpoint, kind, received: false });
    } else if (method === 'Network.responseReceived') {
      const pending = selected.get(requestId), response = params.response;
      if (Number.isInteger(response?.status) && response.status >= 100 && response.status <= 599) {
        if (params.type === 'Document' && httpsPath(response.url) === '/') observation.documentStatus = response.status;
        if (pending?.kind === 'config') observation.configStatus = response.status;
      }
      if (pending && response) pending.received = response.status === 200 &&
        !response.fromDiskCache && !response.fromServiceWorker && !response.fromPrefetchCache &&
        httpsPath(response.url) === pending.endpoint;
    } else if (method === 'Network.loadingFinished' || method === 'Network.loadingFailed') {
      const pending = selected.get(requestId);
      selected.delete(requestId);
      if (!pending) return;
      if (pending.received && method === 'Network.loadingFinished') {
        void inspect(requestId, pending.kind).catch(() => { if (pending.kind === 'login' || pending.kind === 'logout') rejected = true; });
      } else if (pending.kind === 'login' || pending.kind === 'logout') rejected = true;
    } else if (method === 'Network.webSocketCreated' && armed && sockets.size < 16 && gatewayURL(params.url)) {
      sockets.add(requestId);
    } else if (method === 'Network.webSocketHandshakeResponseReceived' && sockets.has(requestId) && params.response?.status === 101) {
      connected.add(requestId);
      result.gatewayConnected = true;
    } else if (method === 'Network.webSocketClosed') {
      sockets.delete(requestId);
      connected.delete(requestId);
      if (!result.workspaceVisible) result.gatewayConnected = connected.size > 0;
      if (requestId === logoutSocket) logoutClosed = true;
    }
  });
  await call('Page.enable');
  await call('Runtime.enable');
  await call('Network.enable', { maxTotalBufferSize: 4 << 20, maxResourceBufferSize: 256 << 10, maxPostDataSize: 0 });
  await call('Network.setCacheDisabled', { cacheDisabled: true });
  await call('Emulation.setDeviceMetricsOverride', { width: 1280, height: 900, deviceScaleFactor: 1, mobile: false });
  await call('Page.bringToFront');
  observation.stage = 'navigation';
  const navigation = await call('Page.navigate', { url: ORIGIN + '/#/login' });
  check(!navigation.errorText);
  observation.stage = 'page-form';
  await until(() => evaluate(`(${LOGIN_DOCUMENT}) && window.isSecureContext && !!document.querySelector('form')`), 25000);
  observation.stage = 'configuration';
  await until(() => !rejected && result.configAccepted, 10000);
  observation.stage = 'sdk-ready';
  // Admit the actual same-origin issuer and original WASM exports before filling
  // credentials. The existing wrapper also waits for its SQL worker and initSDK.
  await until(() => evaluate(`(${LOGIN_DOCUMENT}) &&
    window.isSecureContext && typeof window.initSDK === 'function' && typeof window.login === 'function' && !!document.querySelector('form')`), 10000);
  observation.stage = 'login-form';

  const click = async (selector, text, guard = LOGIN_DOCUMENT) => {
    const point = await until(() => evaluate(`(() => {
      if (!(${guard})) return null;
      const matches = [...document.querySelectorAll(${JSON.stringify(selector)})].filter(e =>
        e.getClientRects().length && !e.disabled && ${text === undefined ? 'true' : `e.textContent.trim() === ${JSON.stringify(text)}`});
      if (matches.length !== 1) return null;
      const e = matches[0], r = e.getBoundingClientRect(), x = (r.left + r.right) / 2, y = (r.top + r.bottom) / 2;
      return e.contains(document.elementFromPoint(x, y)) ? { x, y } : null;
    })()`), 8000);
    check(Number.isFinite(point.x) && Number.isFinite(point.y));
    await call('Input.dispatchMouseEvent', { ...point, type: 'mousePressed', button: 'left', clickCount: 1 });
    await call('Input.dispatchMouseEvent', { ...point, type: 'mouseReleased', button: 'left', clickCount: 1 });
  };
  const type = async (selector, value, guard = LOGIN_DOCUMENT) => {
    await click(selector, undefined, guard);
    check(await evaluate(`(${guard}) && document.activeElement === document.querySelector(${JSON.stringify(selector)})`));
    await call('Input.insertText', { text: value });
  };
  await click('[role="tab"]', '邮箱');
  await type('form input#email', input.email);
  await type('form input[type="password"]', input.password);
  armed = true;
  await click('form:has(input#email):has(input[type="password"]) button[type="submit"]');

  observation.stage = 'workspace';
  let settled = 0;
  await until(async () => {
    check(!rejected);
    if (!result.loginAccepted || !result.gatewayConnected || !result.selfAccepted) { settled = 0; return false; }
    const workspace = await evaluate(`(() => {
      if (!(${CHAT_DOCUMENT})) return false;
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
  return {
    click, type, evaluate, front: () => call('Page.bringToFront'),
    connected: () => !rejected && connected.size > 0,
    armLogout() {
      check(!rejected && connected.size === 1 && !logoutSocket && logoutPosts === 0);
      [logoutSocket] = connected;
    },
    logoutResult() {
      check(!rejected);
      return { revoked: logoutRevoked && logoutPosts === 1, closed: logoutClosed && connected.size === 0 };
    },
    async rejectsRevokedTokens() {
      check(loginTokens && logoutRevoked && logoutClosed);
      const { chat, im } = loginTokens;
      loginTokens = undefined;
      return evaluate(`(async () => {
        if (!(${LOGIN_DOCUMENT})) return null;
        const denied = async (endpoint, token) => {
          const response = await fetch(endpoint, {
            method: 'POST', headers: { 'Content-Type': 'application/json', token, operationID: crypto.randomUUID() },
            body: JSON.stringify({userIDs: [${JSON.stringify(input.userID)}]}),
            credentials: 'omit', redirect: 'error', cache: 'no-store', signal: AbortSignal.timeout(5000),
          });
          const body = await response.json();
          return response.status === 200 && body?.errCode === 1507 && !body.data;
        };
        return Promise.all([
          denied('/chat/user/find/full', ${JSON.stringify(chat)}),
          denied('/api/user/get_users_info', ${JSON.stringify(im)}),
        ]);
      })()`);
    },
  };
}

const EDITOR = '#editable-div[role="textbox"][aria-label="消息输入框"][contenteditable="true"]';

async function openConversation(page, peerID, guard) {
  observation.stage = 'contact-search';
  await page.front();
  await page.click('button[aria-label="更多操作"]', undefined, CHAT_DOCUMENT);
  await page.click('button', '添加好友', CHAT_DOCUMENT);
  await page.type('[role="dialog"] #contact-lookup-keyword', peerID, CHAT_DOCUMENT);
  await page.click('[role="dialog"] button', '确认', CHAT_DOCUMENT);
  observation.stage = 'contact-card';
  // The hydrated card must show the exact account before opening the chat.
  const card = `[role="dialog"]:has(button[aria-label=${JSON.stringify('复制 ID: ' + peerID)}])`;
  await page.click(card + ' button', '发送消息', CHAT_DOCUMENT);
  observation.stage = 'conversation-ready';
  await until(() => page.evaluate(`(${guard}) && !!document.querySelector(${JSON.stringify(EDITOR)})`), 12000);
}

async function messageRow(page, text, guard, requireAcknowledged) {
  return page.evaluate(`(() => {
    if (!(${guard})) return null;
    const rows = [...document.querySelectorAll('#chat-main #scrollableDiv [id^="chat_"]')].filter(row =>
      row.getClientRects().length && [...row.querySelectorAll('*')].some(node =>
        node.childElementCount === 0 && node.textContent === ${JSON.stringify(text)}));
    if (rows.length !== 1) return null;
    const row = rows[0];
    if (${requireAcknowledged} && ![...row.querySelectorAll('div')].some(node =>
      node.childElementCount === 0 && ['已读', '未读'].includes(node.textContent))) return null;
    return row.id;
  })()`);
}

async function sendAndReceive(from, to, guard) {
  observation.stage = 'compose';
  check(from.connected() && to.connected());
  const text = 'Luobo Web ' + randomBytes(12).toString('hex');
  await from.front();
  await from.type(EDITOR, text, guard);
  check(await from.evaluate(`(${guard}) && document.querySelector(${JSON.stringify(EDITOR)})?.innerText.trim() === ${JSON.stringify(text)}`));
  await from.click('.app-send-actions button', '发送', guard);
  observation.stage = 'send-ack';
  const id = await until(() => messageRow(from, text, guard, true), 20000);
  check(typeof id === 'string' && /^chat_[A-Za-z0-9_-]{1,256}$/.test(id));
  observation.stage = 'peer-receive';
  await to.front();
  const received = await until(() => messageRow(to, text, guard, false), 20000);
  check(received === id && from.connected() && to.connected());
}

async function runBrowser(cdp, input) {
  const events = new Map();
  cdp.event = message => events.get(message.sessionId)?.(message);
  if (!input.peer) {
    await login(cdp, events, input, result, observation);
    return;
  }
  const createContext = async () => {
    const { browserContextId } = await cdp.call('Target.createBrowserContext', { disposeOnDetach: true });
    check(typeof browserContextId === 'string' && browserContextId.length > 0);
    return browserContextId;
  };
  const senderContext = await createContext(), peerContext = await createContext();
  check(senderContext !== peerContext);
  const sender = await login(cdp, events, input, result, observation, senderContext);
  observation.stage = 'peer-login';
  const peerChecks = loginChecks();
  const peerObservation = { ...observation, documentStatus: 0, configStatus: 0, configRequested: false };
  const peer = await login(cdp, events, input.peer, peerChecks, peerObservation, peerContext);
  check(Object.values(peerChecks).every(value => value === true) && peerObservation.documentStatus === 200);
  result.interaction.peerReady = true;

  const chatHash = '#/chat/si_' + [input.userID, input.peer.userID].sort().join('_');
  const conversation = `(${CHAT_DOCUMENT}) && location.hash === ${JSON.stringify(chatHash)}`;
  await openConversation(sender, input.peer.userID, conversation);
  await openConversation(peer, input.userID, conversation);
  result.interaction.conversationOpened = true;
  await sendAndReceive(sender, peer, conversation);
  result.interaction.senderToPeer = true;
  await sendAndReceive(peer, sender, conversation);
  result.interaction.peerToSender = true;

  observation.stage = 'logout';
  await sender.front();
  await sender.click('.app-nav-logout button[aria-label="退出"]', undefined, CHAT_DOCUMENT);
  const confirm = `(${CHAT_DOCUMENT}) && [...document.querySelectorAll('.ant-modal-confirm-content')].some(
    e => e.getClientRects().length && e.textContent.trim() === '确认退出当前账号吗？')`;
  sender.armLogout();
  await sender.click('.ant-modal-confirm-btns button.ant-btn-primary', undefined, confirm);
  await until(async () => {
    const { revoked, closed } = sender.logoutResult();
    result.interaction.logoutRevoked = revoked;
    result.interaction.logoutSocketClosed = closed;
    result.interaction.loginRestored = await sender.evaluate(`(${LOGIN_DOCUMENT}) &&
      !!document.querySelector('form input[type="password"]') && !document.querySelector('.app-shell')`);
    return revoked && closed && result.interaction.loginRestored;
  }, 18000);
  observation.stage = 'logout-token-check';
  const denied = await sender.rejectsRevokedTokens();
  check(Array.isArray(denied) && denied.length === 2 && denied.every(flag => typeof flag === 'boolean'));
  [result.interaction.oldChatTokenDenied, result.interaction.oldIMTokenDenied] = denied;
  check(denied.every(flag => flag === true));
  check(peer.connected() && await peer.evaluate(conversation));
  result.interaction.peerStillConnected = true;
}

async function main(input) {
  let profile, chromeTemp, child, exit, cdp;
  let exited = false, cleanExit = false, success = false, cleanup = true;
  try {
    check(process.platform === 'linux' && process.getuid() !== 0 && typeof WebSocket === 'function');
    const temporary = await realpath(process.env.TMPDIR);
    const directory = await lstat(temporary);
    check(directory.isDirectory() && directory.uid === process.getuid() && (directory.mode & 0o077) === 0);
    if (input) {
      observation.stage = 'tls-leaf';
      await checkTLSLeaf(input.certificateSPKI);
    }
    check(!stop.signal.aborted);
    observation.stage = 'chrome-start';
    profile = await mkdtemp(path.join(temporary, 'product20-chrome-'));
    // Chromium creates a Unix singleton socket below TMPDIR. The runner's
    // nested work/profile paths leave too little room for that socket name.
    chromeTemp = await mkdtemp('/tmp/luobo-p20-chrome-');
    child = spawn(CHROME, [
      '--headless=new', '--remote-debugging-address=127.0.0.1', '--remote-debugging-port=0',
      '--user-data-dir=' + profile,
      ...(input ? ['--ignore-certificate-errors-spki-list=' + input.certificateSPKI] : []),
      '--no-first-run', '--no-default-browser-check', '--disable-background-networking',
      '--disable-component-update', '--disable-sync', '--no-proxy-server', 'about:blank',
    ], { stdio: ['ignore', 'ignore', 'pipe'], cwd: process.cwd(), env: { PATH: '/usr/bin:/bin', HOME: profile, TMPDIR: chromeTemp } });
    let startupText = '';
    child.stderr.on('data', chunk => {
      if (observation.stage !== 'chrome-start' || startupText.length >= 8192) return;
      startupText += chunk.toString('utf8').slice(0, 8192 - startupText.length);
      if (/no usable sandbox|suid sandbox helper|failed to move to new namespace|namespace sandbox/i.test(startupText)) {
        observation.chromeFailure = 'sandbox';
      } else if (/error while loading shared libraries/i.test(startupText)) {
        observation.chromeFailure = 'shared-library';
      }
    });
    exit = new Promise(resolve => {
      child.once('exit', (code, signal) => { exited = true; cleanExit = code === 0 && signal === null; resolve(true); });
      child.on('error', error => {
        if (!child.pid) {
          if (['ENOENT', 'EACCES', 'ENOEXEC'].includes(error?.code)) observation.chromeFailure = 'executable';
          exited = true;
          resolve(true);
        }
      });
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
    observation.stage = 'devtools';
    cdp = await connectCDP(endpoint);
    if (probe) {
      const { targetId } = await cdp.call('Target.createTarget', { url: 'about:blank' });
      check(typeof targetId === 'string' && targetId.length > 0);
      success = true;
    } else {
      await runBrowser(cdp, input);
      success = Object.entries(result).every(([key, value]) => key === 'interaction'
        ? Object.values(value).every(flag => flag === true)
        : value === true);
    }
  } catch {
    // Only the bounded facts below leave the task-owned process.
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
    if (!child || exited) {
      for (const directory of [profile, chromeTemp]) {
        if (!directory) continue;
        try { await rm(directory, { recursive: true, force: true }); } catch { cleanup = false; }
      }
    } else cleanup = false;
  }
  const accepted = success && cleanup && cleanExit && !stop.signal.aborted;
  if (success) observation.stage = accepted ? 'complete' : 'cleanup';
  return accepted;
}

async function accept(input) {
  if (input.peer) result.interaction = {
    peerReady: false, conversationOpened: false, senderToPeer: false, peerToSender: false,
    logoutRevoked: false, logoutSocketClosed: false, loginRestored: false,
    oldChatTokenDenied: false, oldIMTokenDenied: false, peerStillConnected: false,
  };
  const deadline = setTimeout(interrupt, input.peer ? 240000 : 95000);
  try { return await main(input); } finally { clearTimeout(deadline); }
}

function reply(client) {
  return new Promise(resolve => {
    if (client.destroyed || !client.writable) { resolve(false); return; }
    const timer = setTimeout(() => finish(false), 2000);
    const closed = () => finish(false);
    function finish(sent) {
      clearTimeout(timer);
      client.removeListener('close', closed);
      resolve(sent);
    }
    client.once('close', closed);
    client.end(JSON.stringify({ ...result, observation }) + '\n', error => finish(!error));
  });
}

async function serve() {
  let server, client, socketPath;
  let bound = false, accepted = false;
  const waiting = setTimeout(interrupt, 1200000);
  const cancelInput = () => client?.destroy();
  stop.signal.addEventListener('abort', cancelInput, { once: true });
  try {
    check(process.platform === 'linux' && process.getuid() !== 0 && typeof WebSocket === 'function');
    const directory = await realpath(process.env.TMPDIR);
    const info = await lstat(directory);
    check(directory === process.cwd() && directory === process.env.HOME && directory === process.env.TMPDIR &&
      info.isDirectory() && info.uid === process.getuid() && (info.mode & 0o777) === 0o700);
    socketPath = path.join(directory, 'browser-smoke.sock');
    check(Buffer.byteLength(socketPath) < 108);
    check(!(await lstat(socketPath).catch(error => { if (error?.code !== 'ENOENT') throw error; })));
    check(!stop.signal.aborted);

    server = createServer({ allowHalfOpen: true }, socket => {
      if (client || stop.signal.aborted) { socket.destroy(); return; }
      client = socket;
      client.on('error', () => {});
    });
    server.on('error', interrupt);
    server.once('listening', () => { bound = true; });
    const mask = process.umask(0o177);
    try {
      // Bind at mode 0600, before Go can inspect or connect to the socket.
      server.listen(socketPath);
      await once(server, 'connection', { signal: stop.signal });
    } finally { process.umask(mask); }
    const socketInfo = await lstat(socketPath);
    check(client && socketInfo.isSocket() && socketInfo.uid === process.getuid() && (socketInfo.mode & 0o777) === 0o600);
    const input = await privateInput(client.iterator({ destroyOnReturn: false }));
    clearTimeout(waiting);
    stop.signal.removeEventListener('abort', cancelInput);
    check(!stop.signal.aborted);
    accepted = await accept(input);
  } catch {
    // Transport and input failures also leave only the bounded result.
  } finally {
    clearTimeout(waiting);
    stop.signal.removeEventListener('abort', cancelInput);
    if (client) {
      let sent = false;
      try { sent = await reply(client); } catch { /* no raw diagnostic */ }
      client.destroy();
      accepted = accepted && sent;
    }
    if (server) {
      // Node closes and unlinks its own bound Unix socket. Never unlink a
      // pathname whose listener could not be established by this process.
      const closed = await new Promise(resolve => server.close(error => resolve(!error)));
      const removed = bound && await lstat(socketPath).then(() => false, error => error?.code === 'ENOENT');
      accepted = accepted && closed && removed;
    }
  }
  return accepted && !stop.signal.aborted;
}

let accepted = false;
try {
  if (listen) accepted = await serve();
  else if (probe || process.argv.length === 2) accepted = await accept(probe ? undefined : await privateInput());
} catch { /* no raw diagnostic */ }
process.removeListener('SIGTERM', interrupt);
process.removeListener('SIGINT', interrupt);
if (!listen) process.stdout.write(JSON.stringify(probe ? observation : { ...result, observation }) + '\n');
process.exitCode = accepted ? 0 : 1;
