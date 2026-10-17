'use strict';
const test = require('node:test'), assert = require('node:assert/strict');
const fs = require('node:fs'), path = require('node:path'), vm = require('node:vm'), crypto = require('node:crypto');
const root = path.resolve(__dirname, '../sdk-patches');
const hash = body => crypto.createHash('sha256').update(body).digest('hex');
const manifest = JSON.parse(fs.readFileSync(path.join(root, 'manifest.json'), 'utf8'));
const text = name => {
    const body = fs.readFileSync(path.join(root, 'lib__' + name + '.patched'));
    assert.equal(hash(body), manifest.files.find(row => row.path === 'lib/' + name).patched_sha256);
    return body.toString('utf8');
};
const block = (source, begin, end) => {
    assert.equal(source.split(begin).length, 2); assert.equal(source.split(end).length, 2);
    return source.split(begin)[1].split(end)[0].replace(/\r\n/g, '\n').trim();
};
function fixture(wrapper, worker, alter = value => value) {
    const source = text(worker), handlers = new Map(), files = new Map();
    // Execute the actual pinned SDK producer, not an object-returning mock.
    const marker = 'function O(e,t,r){', endMarker = 'const k=';
    assert.equal(source.split(marker).length, 2);
    const start = source.indexOf(marker), end = source.indexOf(endMarker, start);
    assert.ok(end > start);
    const producer = source.slice(start, end);
    assert.equal(hash(Buffer.from(producer)), '71637ddbd852535e6ab59ee15d015e1b81ab328826b40e570135c38f0f9120da');
    const factory = block(source, '/* LUOBO BUFFER WORKER BEGIN */', '/* LUOBO BUFFER WORKER END */');
    const registrationStart = source.indexOf('const wasmBufferFiles = registerWasmBufferFileMap(Gt, zt, O);');
    const registrationEnd = source.indexOf('Gt.registerMethod("getUpload"', registrationStart);
    assert.ok(registrationStart > 0 && registrationEnd > registrationStart);
    const context = {
        Blob, ArrayBuffer, Uint8Array, zt: files,
        Gt: { registerMethod(name, fn) { assert.ok(!handlers.has(name)); handlers.set(name, fn); } },
    };
    vm.runInNewContext(producer + '\n' + factory + '\n' + source.slice(registrationStart, registrationEnd).replace(/,$/, ';'), context);
    let transport = { worker: {}, rpc: { async invoke(name, ...args) {
        assert.equal(args.pop().timeout, 5000);
        const result = handlers.get(name)(...args);
        if (name === 'wasmBufferFileMapReserve' || name === 'wasmBufferFileMapSet' || name === 'wasmBufferFileMapRelease') {
            assert.equal(typeof result.data, 'string');
        }
        return alter(result, name);
    } } };
    const windowSource = block(text(wrapper), '// LUOBO BUFFER WINDOW BEGIN', '// LUOBO BUFFER WINDOW END');
    assert.equal(windowSource, fs.readFileSync(path.join(root, 'buffer-source/window-buffer.js'), 'utf8').trim());
    const windowContext = { Blob, ArrayBuffer, Map, Error, Promise, Object };
    vm.runInNewContext(windowSource, windowContext);
    const bridge = windowContext.createWasmBufferFileMapBridge(() => transport);
    bridge.invalidate('1');
    return { bridge, handlers, files, replaceWorker() { transport = { worker: {}, rpc: transport.rpc }; } };
}
const id = 'public/image.png', token = '1:1';
const claim = f => assert.equal(f.bridge.claim('1', token, [id]), true);
const seed = f => assert.equal(f.handlers.get('fileMapSet')(id, new Blob(['png'])).errCode, 0);
const parseAck = value => {
    const ack = JSON.parse(value);
    assert.equal(ack.errCode, 0); assert.equal(typeof ack.data, 'object');
    assert.equal(ack.data.epoch, '1'); assert.equal(ack.data.token, token);
    return ack.data;
};

test('actual modern and legacy worker JSON receipts preserve media lease admission and reject malformed or retired ACKs', async () => {
    for (const worker of ['worker.js', 'worker-legacy.js']) {
        for (const wrapper of ['index.js', 'index.es.js', 'index.umd.js']) {
            for (const mode of ['ordinary', 'buffer']) {
                const f = fixture(wrapper, worker); claim(f);
                let reply;
                if (mode === 'ordinary') { seed(f); reply = await f.bridge.reserve('1', token); }
                else reply = await f.bridge.set('1', token, [{ uuid: id, size: 3, buffer: new Uint8Array([1, 2, 3]).buffer }]);
                assert.deepEqual(parseAck(reply).files, [{ uuid: id, size: 3 }]);
                assert.equal(f.bridge.current('1', token), true);
                await assert.rejects(f.bridge.reserve('1', token)); // no replay of an admitted lease
                await assert.rejects(f.bridge.release('2', token));
                assert.equal(f.files.size, 1);
                assert.deepEqual(parseAck(await f.bridge.release('1', token)).files, []);
                assert.equal(f.files.size, 0); assert.equal(f.bridge.current('1', token), false);
            }
        }
        const changes = [
            result => ({ ...result, errCode: 10005 }),
            result => ({ ...result, data: 'not JSON' }),
            result => ({ ...result, data: JSON.parse(result.data) }),
            result => ({ ...result, data: JSON.stringify(result.data) }),
            result => ({ ...result, data: 'null' }),
            result => ({ ...result, data: '[]' }),
            result => ({ ...result, data: ' '.repeat(32769) }),
            ...['epoch', 'token', 'uuid', 'size', 'oversize'].map(field => result => {
                const data = JSON.parse(result.data);
                if (field === 'epoch') data.epoch = '2';
                if (field === 'token') data.token = '1:other';
                if (field === 'uuid') data.files[0].uuid = 'other/image.png';
                if (field === 'size') data.files[0].size = -1;
                if (field === 'oversize') data.files[0].size = 2147483648;
                return { ...result, data: JSON.stringify(data) };
            }),
        ];
        for (const change of changes) {
            const f = fixture('index.es.js', worker, (result, name) => name === 'wasmBufferFileMapReserve' ? change(result) : result);
            seed(f); claim(f);
            await assert.rejects(f.bridge.reserve('1', token));
            assert.equal(f.bridge.current('1', token), false);
            await f.bridge.release('1', token); assert.equal(f.files.size, 0);
        }
        for (const retire of ['owner-epoch', 'worker']) {
            let finish;
            const f = fixture('index.es.js', worker, (result, name) => name === 'wasmBufferFileMapReserve' ? new Promise(resolve => { finish = () => resolve(result); }) : result);
            seed(f); claim(f);
            const pending = f.bridge.reserve('1', token);
            assert.equal(typeof finish, 'function');
            if (retire === 'owner-epoch') { f.bridge.invalidate('2'); f.bridge.invalidate('3'); }
            else f.replaceWorker();
            finish(); await assert.rejects(pending);
            assert.equal(f.bridge.current('1', token), false);
            await f.bridge.release('1', token); assert.equal(f.files.size, 0);
        }
    }
});
