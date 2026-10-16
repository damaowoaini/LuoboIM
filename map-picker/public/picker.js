(() => {
  'use strict';
  const MAX = Number.MAX_SAFE_INTEGER;
  const byId = id => document.getElementById(id);
  const status = byId('status');
  let nonce = null, retired = false, sequence = 0, map = null, marker = null, ready = false, mapFailed = false;
  let turn = 0, commandId = 0, chosen = null, searchTurn = 0, loadTimer = null;
  const requests = new Set();
  const exact = (value, keys) => value && Object.getPrototypeOf(value) === Object.prototype &&
    Object.keys(value).length === keys.length && keys.every(key => Object.hasOwn(value, key));
  const point = (lng, lat) => typeof lng === 'number' && typeof lat === 'number' &&
    Number.isFinite(lng) && Number.isFinite(lat) && Math.abs(lng) <= 180 && Math.abs(lat) <= 90;
  const name = value => typeof value === 'string' && [...value].length <= 128 &&
    !/[\u0000-\u001f\u007f-\u009f\u061c\u200e\u200f\u202a-\u202e\u2066-\u2069\ud800-\udfff]/u.test(value) &&
    !/(?:https?:|www\.|javascript:|data:|file:)/i.test(value) ? value.trim() : '';
  const live = () => nonce !== null && !retired;
  const acceptance = accepted => JSON.stringify({ accepted });
  function post(channel, value, limit) {
    if (!live()) return;
    const body = JSON.stringify(value);
    if (new TextEncoder().encode(body).length <= limit &&
        window[channel] && typeof window[channel].postMessage === 'function') window[channel].postMessage(body);
  }
  function state(kind, code) {
    const body = { requestNonce: nonce, sequence, state: kind };
    if (code) body.code = code;
    post('LuoboMapStateV1', body, 512);
  }
  function feedback(text, error = false) { status.textContent = text; status.dataset.error = String(error); }
  function error(code) {
    const messages = { config: '地图配置不可用，请返回后重试', map_load: '地图加载失败，请返回后重试',
      conversion: '当前位置转换失败，可以继续手动选点', stale_command: '选点已更新，请重新定位' };
    if (code === 'config' || code === 'map_load') { mapFailed = true; ready = false; }
    feedback(messages[code], true); state('error', code);
  }
  function publish(lng, lat, label, manual = true) {
    if (!live() || !ready || !point(lng, lat) || sequence >= MAX) return false;
    if (manual) turn += 1;
    sequence += 1; chosen = { lng, lat, name: name(label) };
    marker.setPosition([lng, lat]); marker.show();
    post('LuoboMapCandidateV1', { requestNonce: nonce, sequence, ...chosen }, 2048);
    feedback(chosen.name || '已选中位置，请使用上方按钮确认发送');
    return true;
  }
  function select(lng, lat, label = byId('name').value) {
    if (publish(lng, lat, label)) byId('name').value = chosen.name;
  }
  async function fetchJson(path) {
    const controller = new AbortController(); requests.add(controller);
    const timer = setTimeout(() => controller.abort(), 8000);
    try {
      const response = await fetch(path, { signal: controller.signal, credentials: 'omit', redirect: 'error', cache: 'no-store' });
      if (!response.ok) throw Error('request_failed');
      const length = Number(response.headers.get('content-length'));
      if (length > 512 * 1024) throw Error('response_limit');
      // The same-origin service imposes a 512KiB response bound before forwarding.
      const body = await response.text();
      if (new TextEncoder().encode(body).length > 512 * 1024) throw Error('response_limit');
      return JSON.parse(body);
    } finally { clearTimeout(timer); requests.delete(controller); }
  }
  async function load(center) {
    let config;
    try {
      config = await fetchJson('/picker/config');
      if (!live() || mapFailed) return;
      if (!exact(config, ['key', 'serviceHost']) || !/^[A-Za-z0-9]{32}$/.test(config.key) ||
          config.serviceHost !== window.location.origin + '/_AMapService') throw Error('invalid_config');
    } catch { if (live()) error('config'); return; }
    if (!live()) return;
    window._AMapSecurityConfig = { serviceHost: config.serviceHost };
    const script = document.createElement('script');
    script.src = 'https://webapi.amap.com/maps?v=2.0&key=' + encodeURIComponent(config.key);
    script.referrerPolicy = 'same-origin';
    loadTimer = setTimeout(() => { if (live() && !ready) error('map_load'); }, 18000);
    script.onerror = () => { if (live()) { clearTimeout(loadTimer); error('map_load'); } };
    script.onload = () => {
      if (!live() || mapFailed) return;
      try {
        map = new window.AMap.Map('map', { center: [center.lng, center.lat], zoom: 12, viewMode: '2D' });
        marker = new window.AMap.Marker({ map, position: [center.lng, center.lat] }); marker.hide();
        map.on('complete', () => {
          if (!live() || ready || mapFailed) return;
          ready = true; clearTimeout(loadTimer); feedback('点击或拖动地图选择位置'); state('ready');
        });
        map.on('click', event => { if (event.lnglat) select(event.lnglat.getLng(), event.lnglat.getLat()); });
        map.on('dragend', () => { if (!live() || !map || !ready) return; const p = map.getCenter(); select(p.getLng(), p.getLat()); });
      } catch { clearTimeout(loadTimer); error('map_load'); }
    };
    document.head.appendChild(script);
  }
  const api = {
    init(value) {
      if (nonce !== null || retired || !exact(value, ['requestNonce', 'initialCenter']) ||
          typeof value.requestNonce !== 'string' || !/^[a-f0-9]{64}$/.test(value.requestNonce) || !exact(value.initialCenter, ['lng', 'lat']) ||
          !point(value.initialCenter.lng, value.initialCenter.lat)) return acceptance(false);
      nonce = value.requestNonce; void load(value.initialCenter); return acceptance(true);
    },
    convertGps(value) {
      if (!live() || !ready || !exact(value, ['requestNonce', 'commandId', 'expectedSequence', 'lng', 'lat']) ||
          value.requestNonce !== nonce || !Number.isSafeInteger(value.commandId) || value.commandId <= commandId ||
          !Number.isSafeInteger(value.expectedSequence) || value.expectedSequence !== sequence ||
          !point(value.lng, value.lat)) return acceptance(false);
      commandId = value.commandId; const originalTurn = turn, originalCommand = commandId;
      try {
        window.AMap.convertFrom([value.lng, value.lat], 'gps', (code, result) => {
          if (!live() || originalTurn !== turn || originalCommand !== commandId) return;
          const p = code === 'complete' && result?.info === 'ok' && result.locations?.length === 1 ? result.locations[0] : null;
          if (!p || !point(p.getLng(), p.getLat())) { error('conversion'); return; }
          if (publish(p.getLng(), p.getLat(), '我的位置', false)) { map.setCenter([chosen.lng, chosen.lat]); byId('name').value = chosen.name; }
        });
      } catch { error('conversion'); }
      return acceptance(true);
    },
    dispose(value) {
      if (!live() || !exact(value, ['requestNonce']) || value.requestNonce !== nonce) return acceptance(false);
      retired = true; ready = false; turn += 1; clearTimeout(loadTimer);
      for (const request of requests) request.abort(); requests.clear();
      try { map?.destroy(); } catch { /* Retired callbacks remain fenced even if destruction fails. */ }
      chosen = null; map = null; marker = null; return acceptance(true);
    },
  };
  Object.defineProperty(window, 'LuoboMapPickerV1', { value: Object.freeze(api), writable: false, configurable: false });
  byId('name').addEventListener('input', () => { if (chosen) select(chosen.lng, chosen.lat); });
  byId('search').addEventListener('submit', async event => {
    event.preventDefault(); if (!live() || !ready) return;
    const query = name(byId('query').value); if (!query) return;
    const ownSearch = ++searchTurn; const results = byId('results'); results.replaceChildren();
    feedback('正在搜索地点…');
    try {
      const result = await fetchJson('/_AMapService/v3/place/text?' + new URLSearchParams({ keywords: query, offset: '5', page: '1', extensions: 'base', output: 'JSON' }));
      if (!live() || ownSearch !== searchTurn) return;
      if (result.status !== '1' || !Array.isArray(result.pois)) throw Error('search_failed');
      for (const poi of result.pois.slice(0, 5)) {
        if (typeof poi.location !== 'string' || poi.location.length > 64) continue;
        const coordinates = poi.location.split(','); if (coordinates.length !== 2 || coordinates.some(x => x.trim() === '')) continue;
        const [lng, lat] = coordinates.map(Number), title = name(poi.name);
        if (!point(lng, lat) || !title) continue;
        const button = document.createElement('button'); button.type = 'button'; button.textContent = title;
        button.addEventListener('click', () => { if (live() && ownSearch === searchTurn) { select(lng, lat, title); map.setCenter([lng, lat]); } });
        results.appendChild(button);
      }
      feedback(results.childElementCount ? '请选择搜索结果，或在地图上选点' : '没有找到地点，可以在地图上选点');
    } catch { if (live() && ownSearch === searchTurn) feedback('地点搜索失败，可以继续在地图上选点', true); }
  });
  window.addEventListener('pagehide', () => { if (live()) api.dispose({ requestNonce: nonce }); });
})();
