// Synchronous entry claims protect the interval before queued SDK/worker calls.
function createWasmBufferFileMapBridge(getTransport) {
    let epoch = '0';
    const leases = new Map(), claims = new Map();
    const failure = () => new Error('buffer file map acknowledgement unavailable');
    const key = value => typeof value === 'string' && value.length > 0 && value.length <= 1024 &&
        value.trim().length > 0 && !/[\u0000-\u001f\u007f-\u009f]/u.test(value);
    const sameTransport = transport => { const now = getTransport(); return now.rpc === transport.rpc && now.worker === transport.worker; };
    const call = async (transport, name, ...args) => {
        if (!transport.rpc || !transport.worker) throw failure();
        const result = await transport.rpc.invoke(name, ...args, { timeout: 5000 });
        if (!result || result.errCode !== 0) throw failure(); return result.data;
    };
    const encode = data => JSON.stringify({ errCode: 0, errMsg: '', data });
    const current = (requestedEpoch, token) => {
        const lease = leases.get(token);
        return !!lease && lease.epoch === epoch && epoch === requestedEpoch && lease.ready && sameTransport(lease.transport);
    };
    const prepare = (requestedEpoch, token, ids) => {
        const lease = leases.get(token);
        if (requestedEpoch !== epoch || !lease || lease.epoch !== epoch || lease.ready ||
            !sameTransport(lease.transport) || ids.length !== lease.ids.length ||
            ids.some((id, i) => id !== lease.ids[i])) throw failure();
        return lease;
    };
    const acknowledge = (requestedEpoch, token, lease, data, expectedSizes) => {
        if (epoch !== requestedEpoch || leases.get(token) !== lease || !sameTransport(lease.transport) || !data ||
            data.epoch !== requestedEpoch || data.token !== token || !Array.isArray(data.files) || data.files.length !== lease.ids.length ||
            data.files.some((entry, i) => entry.uuid !== lease.ids[i] || !Number.isSafeInteger(entry.size) || entry.size < 0 || entry.size > 2147483647 ||
                (expectedSizes && entry.size !== expectedSizes[i]))) throw failure();
        lease.ready = true; return encode(data);
    };
    return {
        invalidate(next) {
            epoch = next; leases.clear(); claims.clear(); const transport = getTransport();
            if (transport.rpc && transport.worker) void call(transport, 'wasmBufferSessionInvalidate', next).catch(() => {});
        },
        // Called synchronously by BOTH ordinary and buffer WASM send entries.
        claim(requestedEpoch, token, ids) {
            if (requestedEpoch !== epoch || !key(token) || leases.has(token) || !Array.isArray(ids) ||
                ids.length < 1 || ids.length > 2 || leases.size >= 1024 || new Set(ids).size !== ids.length ||
                ids.some(id => !key(id) || claims.has(id))) return false;
            const lease = { epoch, transport: getTransport(), ready: false, ids: ids.slice() };
            if (!lease.transport.rpc || !lease.transport.worker) return false;
            leases.set(token, lease); for (const id of ids) claims.set(id, lease); return true;
        },
        byFile(uuid, file, write) {
            if (claims.has(uuid)) return Promise.resolve(JSON.stringify({ errCode: 10005, errMsg: 'media UUID already in flight', data: '' }));
            return write(uuid, file);
        },
        current,
        async set(requestedEpoch, token, entries) {
            if (!Array.isArray(entries)) throw failure();
            const lease = prepare(requestedEpoch, token, entries.map(entry => entry.uuid));
            const files = entries.map(entry => {
                const size = Object.getOwnPropertyDescriptor(ArrayBuffer.prototype, 'byteLength').get.call(entry.buffer);
                if (!Number.isSafeInteger(size) || size !== entry.size) throw failure();
                const file = new Blob([entry.buffer]); if (file.size !== size) throw failure();
                return { uuid: entry.uuid, size, file };
            });
            const data = await call(lease.transport, 'wasmBufferFileMapSet', requestedEpoch, token, files);
            return acknowledge(requestedEpoch, token, lease, data, files.map(file => file.size));
        },
        async reserve(requestedEpoch, token) {
            const stored = leases.get(token); if (!stored) throw failure();
            const lease = prepare(requestedEpoch, token, stored.ids);
            const data = await call(lease.transport, 'wasmBufferFileMapReserve', requestedEpoch, token, lease.ids);
            return acknowledge(requestedEpoch, token, lease, data);
        },
        async release(requestedEpoch, token) {
            const lease = leases.get(token);
            if (!lease) return encode({ epoch: requestedEpoch, token, files: [] });
            if (lease.epoch !== requestedEpoch) throw failure();
            const data = await call(lease.transport, 'wasmBufferFileMapRelease', requestedEpoch, token);
            if (!data || data.epoch !== requestedEpoch || data.token !== token || !Array.isArray(data.files) || data.files.length) throw failure();
            if (leases.get(token) === lease) {
                for (const id of lease.ids) if (claims.get(id) === lease) claims.delete(id);
                leases.delete(token);
            }
            return encode(data);
        },
    };
}
