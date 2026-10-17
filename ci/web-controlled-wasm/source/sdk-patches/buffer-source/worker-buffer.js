// Web-only leases on the existing File/Blob map; no DB/network/filepath protocol.
function registerWasmBufferFileMap(rpc, fileMap, ok) {
    let epoch = '0';
    const claims = new Map(), tokens = new Map();
    const key = value => typeof value === 'string' && value.length > 0 && value.length <= 1024 &&
        value.trim().length > 0 && !/[\u0000-\u001f\u007f-\u009f]/u.test(value);
    const fail = () => ({ errCode: 10005, errMsg: 'buffer file registration refused', data: '' });
    const receipt = (requestedEpoch, token, files = []) => ({ epoch: requestedEpoch, token,
        files: files.map(entry => ({ uuid: entry.uuid, size: entry.size })) });
    const forget = token => {
        const lease = tokens.get(token);
        if (!lease) return;
        for (const entry of lease.files) if (claims.get(entry.uuid) === lease) {
            if (fileMap.get(entry.uuid) === entry.file) fileMap.delete(entry.uuid);
            claims.delete(entry.uuid);
        }
        tokens.delete(token);
    };
    const take = (requestedEpoch, token, entries, explicit) => {
        if (requestedEpoch !== epoch || !key(token) || tokens.has(token) || !Array.isArray(entries) ||
            entries.length < 1 || entries.length > 2 || tokens.size >= 1024) return fail();
        const seen = new Set(), files = [];
        for (const entry of entries) {
            if (!entry || !key(entry.uuid) || seen.has(entry.uuid) || claims.has(entry.uuid)) return fail();
            const file = explicit ? entry.file : fileMap.get(entry.uuid);
            if (!(file instanceof Blob) || !Number.isSafeInteger(file.size) || file.size > 2147483647 ||
                (explicit && (!Number.isSafeInteger(entry.size) || entry.size < 0 || entry.size !== file.size))) return fail();
            seen.add(entry.uuid); files.push({ uuid: entry.uuid, size: file.size, file });
        }
        // Both members validated before any mutation. An unleased ByFile map
        // is a legal predecessor; explicit buffers replace it by actual bytes.
        const lease = { epoch, token, files }; tokens.set(token, lease);
        for (const entry of files) { claims.set(entry.uuid, lease); fileMap.set(entry.uuid, entry.file); }
        return ok(receipt(epoch, token, files));
    };
    rpc.registerMethod('wasmBufferSessionInvalidate', next => {
        if (typeof next !== 'string' || !/^[0-9]{1,20}$/.test(next)) return fail();
        for (const token of Array.from(tokens.keys())) forget(token);
        epoch = next; return ok(next);
    });
    rpc.registerMethod('wasmBufferFileMapSet', (requestedEpoch, token, files) => take(requestedEpoch, token, files, true));
    rpc.registerMethod('wasmBufferFileMapReserve', (requestedEpoch, token, ids) =>
        take(requestedEpoch, token, Array.isArray(ids) ? ids.map(uuid => ({ uuid })) : ids, false));
    rpc.registerMethod('wasmBufferFileMapRelease', (requestedEpoch, token) => {
        if (!key(token) || typeof requestedEpoch !== 'string') return fail();
        const lease = tokens.get(token); if (lease && lease.epoch !== requestedEpoch) return fail();
        forget(token); return ok(receipt(requestedEpoch, token));
    });
    return {
        set(uuid, file) { if (claims.has(uuid)) return fail(); fileMap.set(uuid, file); return ok(uuid); },
        clear() { fileMap.clear(); claims.clear(); tokens.clear(); epoch = '0'; return ok(''); },
    };
}
