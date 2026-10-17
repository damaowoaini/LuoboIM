const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { test } = require('node:test');
const web = path.resolve(__dirname, '..');
const bundles = ['index.es.js', 'index.js', 'index.umd.js'];
const params = { userID: 'synthetic-user', token: 'synthetic-token', platformID: 5,
  apiAddr: 'https://service.example/api', wsAddr: 'wss://service.example/msg_gateway' };
function facade(bundle, initResult) {
  const source = fs.readFileSync(path.join(web, 'sdk-patches', `lib__${bundle}.patched`), 'utf8');
  const begin = 'this.login = async (params, operationID = v4()) => {';
  const end = 'this.logout = (operationID = v4()) => {';
  assert.equal(source.split(begin).length, 2); assert.equal(source.split(end).length, 2);
  const body = source.slice(source.indexOf(begin), source.indexOf(end));
  const calls = { init: [], login: [] };
  const window = {
    commonEventFunc() {},
    initSDK(operation, raw) { calls.init.push({ operation, config: JSON.parse(raw) }); return initResult; },
    login(...args) { calls.login.push(args); return Promise.resolve('synthetic-ACK'); },
  };
  const sdk = { _logWrap() {}, emit() {}, wasmInitializedPromise: Promise.resolve() };
  vm.runInNewContext(`(function () { ${body} }).call(sdk);`, {
    sdk, window, workerPromise: Promise.resolve(), v4: () => 'synthetic-operation',
  });
  return { sdk, calls };
}
test('all actual SDK facades reject false and unknown InitSDK shapes before Login', async () => {
  for (const bundle of bundles) for (const value of [[false], true, false, [], [true, true], [1], ['true'], null, undefined, {}]) {
    const f = facade(bundle, value);
    await assert.rejects(f.sdk.login(params), error => error.errCode === 10004 && error.errMsg === 'SDK initialization rejected');
    assert.equal(f.calls.init.length, 1); assert.equal(f.calls.login.length, 0);
  }
});
test('all actual SDK facades accept exact true and forward only a boolean loopback opt-in', async () => {
  for (const bundle of bundles) for (const value of [undefined, false, true, 'true', 1]) {
    const f = facade(bundle, [true]);
    assert.equal(await f.sdk.login({ ...params, allowInsecureLoopbackForTesting: value }, 'explicit-operation'), 'synthetic-ACK');
    assert.deepEqual(f.calls.login, [['explicit-operation', params.userID, params.token]]);
    assert.equal(f.calls.init[0].operation, 'explicit-operation');
    assert.equal(f.calls.init[0].config.allowInsecureLoopbackForTesting, value === true);
    assert.equal(f.calls.init[0].config.apiAddr, params.apiAddr);
    assert.equal(f.calls.init[0].config.wsAddr, params.wsAddr);
  }
});
test('the actual App prepareLogin removes caller opt-in and installs only the validated mapping decision', async () => {
  const source = fs.readFileSync(path.join(web, 'src/layout/MainContentWrap.tsx'), 'utf8');
  const start = 'prepareLogin: async (args) => {';
  const end = '        afterLogout: async () => {';
  assert.equal(source.split(start).length, 2); assert.equal(source.split(end).length, 2);
  const body = source.slice(source.indexOf(start) + 'prepareLogin: '.length, source.indexOf(end)).trim().replace(/,$/, '')
    .replace('const params = args[0] as Record<string, unknown>;', 'const params = args[0];')
    .replace('const prepared: Record<string, unknown> =', 'const prepared =');
  for (const allowed of [false, true]) {
    const session = { userID: params.userID };
    const prepare = vm.runInNewContext(`(${body})`, {
      sessionCoordinator: { current: session, namespace: 'synthetic-scope' },
      sdkDataDirectory: async () => './synthetic_scope_', runtimeBoundary: { beforeLogin() {} },
      browserSdkLoopbackOptIn: captured => { assert.equal(captured, session); return allowed; },
    });
    const result = await prepare([{ ...params, allowInsecureLoopbackForTesting: true }, 'original-operation']);
    assert.equal(Object.hasOwn(result[0], 'allowInsecureLoopbackForTesting'), allowed);
    if (allowed) assert.equal(result[0].allowInsecureLoopbackForTesting, true);
    assert.equal(result[0].dataDir, './synthetic_scope_');
    assert.equal(result[0].isLogStandardOutput, false);
    assert.equal(result[1], 'original-operation');
    await assert.rejects(prepare([{ ...params, userID: 'foreign-user', allowInsecureLoopbackForTesting: true }]));
  }
});
