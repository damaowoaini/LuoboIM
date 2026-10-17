"""Only new public plugin failure diagnostics; actual source gate stays real."""
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

import run as driver


class PublicPluginDiagnostic(unittest.TestCase):
    def test_real_public_source_gate_keeps_refusal_and_records_only_safe_identity_hashes(self):
        for case in ['valid', 'wrong-body', 'symlink']:
            with self.subTest(case=case), tempfile.TemporaryDirectory() as temp:
                work = Path(temp).resolve(); (work/'evidence').mkdir(mode=0o700)
                root = work/'pub-cache/plugin'; root.mkdir(parents=True)
                body = b'synthetic public source text not exported'
                path = root/'Source.swift'; path.write_bytes(body)
                expected = {'path': path.name, 'bytes': len(body), 'sha256': hashlib.sha256(body).hexdigest()}
                if case == 'wrong-body': expected['sha256'] = '0'*64
                if case == 'symlink':
                    foreign = work/'other-source.swift'; foreign.write_bytes(b'not a permitted plugin file')
                    path.unlink(); path.symlink_to(foreign)
                proof_path = work/'evidence/public-plugin-input-failure.json'
                if case == 'valid':
                    self.assertEqual(driver.check_public_plugin_source(work, {'name': 'public_plugin'}, root, expected), body)
                    self.assertFalse(proof_path.exists())
                    continue
                with self.assertRaisesRegex(ValueError, 'fixed input bytes differ' if case == 'wrong-body' else 'input symlink refused'):
                    driver.check_public_plugin_source(work, {'name': 'public_plugin'}, root, expected)
                proof = json.loads(proof_path.read_text())
                self.assertEqual(proof['declaration'], 'public_plugin')
                self.assertEqual(proof['publicRelativePath'], 'Source.swift')
                self.assertEqual(proof['declared'], {key: expected[key] for key in ['bytes', 'sha256']})
                self.assertTrue(proof['originalGateStillRefused'])
                self.assertFalse(proof['sourceContentsOrAbsoluteRootRecorded'])
                self.assertNotIn(body.decode(), proof_path.read_text())
                self.assertNotIn(str(work), proof_path.read_text())
                if case == 'wrong-body':
                    self.assertEqual(proof['actual'], {'available': True, 'bytes': len(body),
                                     'sha256': hashlib.sha256(body).hexdigest(), 'stable': True})
                else:
                    self.assertFalse(proof['actual']['available'])
                    self.assertNotIn('sha256', proof['actual'])


if __name__ == '__main__':
    unittest.main(verbosity=2)
