// Task-only certificate validation; excluded from the application build.
const crypto = require('node:crypto');
const FIXTURE_HOSTS = new Set([
  'luobo.fixture.test', 'admin.luobo.fixture.test',
  'media.luobo.fixture.test', 'objects.luobo.fixture.test',
  'tenant-b.luobo.fixture.test',
]);
function verifyFixtureCertificate({ hostname, certificate, authority, manifest, now = Date.now() }) {
  // Unrelated hosts retain Chromium's normal system verification.
  if (!FIXTURE_HOSTS.has(hostname)) return -3;
  try {
    if (!manifest.hosts[hostname] || !Number.isFinite(now)) return -2;
    const leaf = new crypto.X509Certificate(certificate);
    const pin = crypto.createHash('sha256')
      .update(leaf.publicKey.export({ format: 'der', type: 'spki' })).digest('base64');
    const valid = authority.ca && !leaf.ca && authority.verify(authority.publicKey)
      && leaf.verify(authority.publicKey) && leaf.issuer === authority.subject
      && leaf.checkHost(hostname) === hostname
      && leaf.keyUsage?.includes('1.3.6.1.5.5.7.3.1')
      && now >= Date.parse(leaf.validFrom) && now <= Date.parse(leaf.validTo)
      && now >= Date.parse(authority.validFrom) && now <= Date.parse(authority.validTo)
      && now <= Date.parse(manifest.expires) && pin === manifest.leafSPKI_SHA256_base64;
    return valid ? 0 : -2;
  } catch { return -2; }
}
module.exports = { verifyFixtureCertificate, FIXTURE_HOSTS };
