function registerLuoboOwnReadTimeWindow(register) {
  let epoch = 0, binding;
  const capture = register('captureOwnReadTimeBinding');
  const scope = register('captureOwnReadTimeScopeDB');
  const apply = register('applyOwnReadTimeDB');
  const invalidate = register('invalidateOwnReadTimeSession');
  const error = () => new Error('own read session unavailable');
  const valid = (generation, token) => generation === epoch && !!token && token === binding;
  // Retire local admission synchronously before posting the worker invalidation.
  // The buffer-owned InitSDK/Login/Logout gate invokes this optional hook.
  window.wasmOwnReadTimeSessionInvalidate = () => {
    binding = undefined;
    if (!Number.isSafeInteger(epoch + 1)) throw error();
    ++epoch;
    return invalidate();
  };
  window.captureOwnReadTimeBinding = async (...args) => {
    const generation = epoch;
    const raw = await capture(...args);
    if (generation !== epoch) throw error();
    const response = typeof raw === 'string' ? JSON.parse(raw) : raw;
    if (!response || response.errCode !== 0 || typeof response.data !== 'string' || !response.data) throw error();
    binding = response.data;
    return raw;
  };
  window.captureOwnReadTimeScopeDB = async (...args) => {
    const generation = epoch, token = args[1];
    if (!valid(generation,token)) throw error();
    const result = await scope(...args);
    if (!valid(generation,token)) throw error();
    return result;
  };
  window.applyOwnReadTimeDB = async (...args) => {
    const generation = epoch, token = args[2];
    if (!valid(generation,token)) throw error();
    const result = await apply(...args);
    if (!valid(generation,token)) throw error();
    return result;
  };
}
