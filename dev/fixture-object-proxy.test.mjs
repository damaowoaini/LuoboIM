import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createFixtureObjectProxy, objectRedirect, objectRoute } from './fixture-object-proxy.mjs';

test('keeps the fixed signing authority and exact encoded object path and query', () => {
  const suffix = '/luobo-stack/synthetic%2Fobject.png?X-Amz-Signature=synthetic%2Bvalue&partNumber=1';
  assert.deepEqual(objectRoute('/__luobo_dev_objects__/storage' + suffix), {
    route: 'storage', host: 'objects.luobo.fixture.test', prefix: '/luobo-stack/', path: suffix,
  });
  assert.equal(objectRedirect('https://objects.luobo.fixture.test' + suffix), '/__luobo_dev_objects__/storage' + suffix);
  assert.equal(objectRedirect('https://luobo.fixture.test/api/object/synthetic%2Ffile?x=%2B'),
    '/__luobo_dev_objects__/alias/api/object/synthetic%2Ffile?x=%2B');
  for (const raw of ['/__luobo_dev_objects__/storage/other-bucket/file', '/__luobo_dev_objects__/alias/api/other/file',
    '/__luobo_dev_objects__/storage/luobo-stack/%2e%2e/file', '/__luobo_dev_objects__/storage/luobo-stack/file#x',
    '/__luobo_dev_objects__/storage/luobo-stack/%broken', '/__luobo_dev_objects__/storage/luobo-stack/'])
    assert.equal(objectRoute(raw), undefined);
});

test('rejects foreign redirects and nonexplicit origin or CA before any transport', async () => {
  for (const url of ['http://objects.luobo.fixture.test/luobo-stack/file', 'https://objects.luobo.fixture.test.evil/luobo-stack/file',
    'https://objects.luobo.fixture.test:34443/luobo-stack/file', 'https://other.example/luobo-stack/file',
    '/luobo-stack/file', 'https://luobo.fixture.test/api/object/%2e%2e/file']) assert.equal(objectRedirect(url), undefined);
  await assert.rejects(createFixtureObjectProxy({ origin: 'http://app.example:7785', caFile: '/not-read' }), /origin refused/);
  const root = await mkdtemp(join(tmpdir(), 'luobo-media-ca-'));
  try {
    const caFile = join(root, 'wrong-public-ca.pem'); await writeFile(caFile, 'synthetic untrusted CA');
    await assert.rejects(createFixtureObjectProxy({ origin: 'http://127.0.0.1:7778', caFile }), /CA differs/);
  } finally { await rm(root, { recursive: true, force: true }); }
});
