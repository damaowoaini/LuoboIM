const { test } = require('node:test');
const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const fs = require('node:fs');
const path = require('node:path');
const { verifyFixtureCertificate } = require('./fixture-certificate.cjs');
const directory = path.resolve(__dirname, '../../../references/integration/tls');
// Only public certificates are read. No fixture or user private key is loaded.
const authority = new crypto.X509Certificate(fs.readFileSync(path.join(directory, 'ca.pem')));
const certificate = fs.readFileSync(path.join(directory, 'server.pem'));
const manifest = JSON.parse(fs.readFileSync(path.join(directory, 'manifest.json'), 'utf8'));
const leaf = new crypto.X509Certificate(certificate);
const now = Math.max(Date.parse(leaf.validFrom), Date.parse(authority.validFrom)) + 60000;
const valid = {hostname:'admin.luobo.fixture.test', certificate, authority, manifest, now};
test('task CA validates every exact fixture hostname', () => {
  for (const hostname of Object.keys(manifest.hosts))
    assert.equal(verifyFixtureCertificate({...valid,hostname}), 0);
});
test('unrelated host uses normal system verifier even if added to manifest', () => {
  assert.equal(verifyFixtureCertificate({...valid,hostname:'unrelated.example',manifest:{...manifest,hosts:{...manifest.hosts,'unrelated.example':true}}}), -3);
});
test('missing fixture host and malformed certificate fail closed', () => {
  assert.equal(verifyFixtureCertificate({...valid,manifest:{...manifest,hosts:{}}}), -2);
  assert.equal(verifyFixtureCertificate({...valid,certificate:'invalid'}), -2);
});
test('wrong SPKI and wrong authority fail closed', () => {
  assert.equal(verifyFixtureCertificate({...valid,manifest:{...manifest,leafSPKI_SHA256_base64:'wrong'}}), -2);
  assert.equal(verifyFixtureCertificate({...valid,authority:leaf}), -2);
});
test('expired task manifest and non-finite time fail closed', () => {
  assert.equal(verifyFixtureCertificate({...valid,manifest:{...manifest,expires:new Date(now-1).toISOString()}}), -2);
  assert.equal(verifyFixtureCertificate({...valid,now:NaN}), -2);
});
test('certificate not-yet-valid and expired dates fail closed', () => {
  assert.equal(verifyFixtureCertificate({...valid,now:Date.parse(leaf.validFrom)-1}), -2);
  assert.equal(verifyFixtureCertificate({...valid,now:Date.parse(leaf.validTo)+1}), -2);
});
