// Isolated local integration browser. Never bundled with the product.
// Trusts the task CA only for the exact fixture hosts after full checks;
// does not install a CA, ignore certificate errors, or touch an existing profile.
const { app, BrowserWindow, session } = require('electron');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const crypto = require('node:crypto');
const { verifyFixtureCertificate, FIXTURE_HOSTS } = require('./fixture-certificate.cjs');
const project = path.resolve(__dirname, '../..');
const tlsDirectory = path.resolve(project, '../references/integration/tls');
const manifest = JSON.parse(fs.readFileSync(path.join(tlsDirectory, 'manifest.json'), 'utf8'));
const authority = new crypto.X509Certificate(fs.readFileSync(path.join(tlsDirectory, 'ca.pem')));
const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'luobo-https-fixture-'));
const observationPath = path.resolve(project, '../references/integration/stack-config/parity-browser-observation.json');
const observation = { fixtureOnly: true, requests: [], failures: [], console: [] };
const saveObservation = () => fs.writeFileSync(observationPath, JSON.stringify(observation), {mode:0o600});
app.setPath('userData', profile);
app.setPath('sessionData', profile);
app.setName('萝卜对话隔离验收');
app.on('window-all-closed', () => app.quit());
app.whenReady().then(async () => {
  const isolated = session.fromPartition('persist:luobo-fixture');
  isolated.webRequest.onCompleted((details) => {
    const url = new URL(details.url);
    if (FIXTURE_HOSTS.has(url.hostname)) {
      observation.requests.push({method:details.method,host:url.hostname,path:url.pathname,status:details.statusCode,resourceType:details.resourceType});
      saveObservation();
    }
  });
  isolated.webRequest.onErrorOccurred((details) => {
    const url = new URL(details.url);
    if (FIXTURE_HOSTS.has(url.hostname)) {
      observation.failures.push({host:url.hostname,path:url.pathname,error:details.error});
      saveObservation();
    }
  });
  await isolated.setProxy({proxyRules: 'http://' + manifest.connect_proxy});
  isolated.setPermissionRequestHandler((_contents, _permission, answer) => answer(false));
  isolated.setCertificateVerifyProc((request, answer) => {
    answer(verifyFixtureCertificate({hostname:request.hostname,certificate:request.certificate.data,authority,manifest}));
  });
  const window = new BrowserWindow({width:1440,height:920,title:'萝卜对话 · 隔离 HTTPS 验收',webPreferences:{session:isolated,sandbox:true,contextIsolation:true,nodeIntegration:false,webSecurity:true,devTools:process.env.LUOBO_FIXTURE_DEVTOOLS==='1'}});
  window.webContents.setWindowOpenHandler(() => ({action:'deny'}));
  window.webContents.on('console-message', (event) => {
    const message = event.message || '';
    const categories = ['SDK登录失败','WebAssembly','SharedArrayBuffer','SQL close','Failed to fetch','Content Security Policy'].filter((label) => message.includes(label));
    if (categories.length) {
      observation.console.push({level:event.level,categories,sdkErrorCode:message.match(/^SDK登录失败 (-?\d{1,8}|unknown)$/)?.[1]});
      saveObservation();
    }
  });
  window.webContents.on('did-fail-load', (_event, code) => console.log('LUOBO_FIXTURE_LOAD_FAILED',code));
  const target = new URL(process.env.LUOBO_FIXTURE_URL || 'https://admin.luobo.fixture.test/');
  if (target.protocol !== 'https:' || target.port || !FIXTURE_HOSTS.has(target.hostname))
    throw new Error('Only exact HTTPS fixture destinations are allowed');
  await window.loadURL(target.toString());
  console.log('LUOBO_FIXTURE_HTTPS_READY');
});
