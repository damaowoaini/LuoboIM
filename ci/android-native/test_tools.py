#!/usr/bin/env python3
"""Synthetic Python-only guards; no Go/Flutter/Gradle/network/device execution."""
import json
import os
from pathlib import Path
import struct
import tempfile
import types
import unittest
from unittest import mock
import zipfile

import audit
import run
import support as s
import verify_inputs


class Tools(unittest.TestCase):
    def test_exact_metadata_does_not_read_mutable_App(self):
        registry, inputs, tools = run.metadata()
        self.assertEqual(registry['source_ref'], '314ac556fdc4be7f98dbd74370201922c71e67c5')
        self.assertEqual(len(inputs['files']), 426)
        self.assertEqual(inputs['accepted_business']['members'], 257)
        self.assertEqual(tools['flutter']['tag'], '3.47.7')

    def test_metadata_rejects_wrong_pin(self):
        with mock.patch.object(s, 'sha', return_value='0' * 64):
            with self.assertRaisesRegex(ValueError, 'metadata differs'):
                run.metadata()

    def test_cold20_gate_zero_materialize(self):
        with tempfile.TemporaryDirectory() as tmp:
            parent = Path(tmp).resolve(); parent.chmod(0o700)
            target = parent / 'new'
            with mock.patch.object(os, 'statvfs', return_value=types.SimpleNamespace(f_bavail=19, f_frsize=s.GIB)) as stat:
                with self.assertRaisesRegex(ValueError, 'cold20GiB'):
                    run.work_path(str(target), True)
                stat.assert_called_once()
            self.assertFalse(target.exists())

    def test_cold_gate_accepts_exact20_and_rejects_resume(self):
        with tempfile.TemporaryDirectory() as tmp:
            parent = Path(tmp).resolve(); parent.chmod(0o700); target = parent / 'new'
            with mock.patch.object(os, 'statvfs', return_value=types.SimpleNamespace(f_bavail=20, f_frsize=s.GIB)):
                self.assertEqual(run.work_path(str(target), True), target)
                target.mkdir()
                with self.assertRaisesRegex(ValueError, 'fresh output'):
                    run.work_path(str(target), True)

    def test_budget_failure_latches_after_free_recovers(self):
        b = s.Budget(Path('/tmp'))
        with mock.patch.object(s, 'allocated', return_value=0), mock.patch.object(os, 'statvfs') as v:
            v.return_value = types.SimpleNamespace(f_bavail=1, f_frsize=1)
            with self.assertRaisesRegex(ValueError, 'disk_floor'): b.sample()
            v.return_value = types.SimpleNamespace(f_bavail=100, f_frsize=s.GIB)
            with self.assertRaisesRegex(ValueError, 'disk_floor'): b.sample()
        self.assertEqual(b.minimum, 1)

    def test_monitor_exception_not_hidden_by_recovery(self):
        b = s.Budget(Path('/tmp'))
        stop = mock.Mock(); stop.wait.side_effect = [False, True]; b.stop = stop
        with mock.patch.object(b, 'sample', side_effect=PermissionError('synthetic EIO')):
            b.poll()
        self.assertEqual(b.reason, 'monitor_infrastructure_failed')
        with self.assertRaisesRegex(ValueError, 'monitor_infrastructure_failed'): b.check()

    def test_host_floor_even_if_output_is_healthy(self):
        b = s.Budget(Path('/tmp/output'), Path('/tmp/host'))
        with mock.patch.object(s, 'allocated', return_value=0), mock.patch.object(os, 'statvfs') as v:
            v.side_effect = [types.SimpleNamespace(f_bavail=10, f_frsize=s.GIB),
                             types.SimpleNamespace(f_bavail=1, f_frsize=1)]
            with self.assertRaisesRegex(ValueError, 'disk_floor'): b.sample()
        self.assertEqual(b.minimum_host, 1)
        self.assertEqual(b.minimum_output, 10 * s.GIB)

    def test_finish_final_sample_has_failure_priority(self):
        b = s.Budget(Path('/tmp'))
        def sample():
            b.latch('disk_floor'); b.check()
        with mock.patch.object(b, 'sample', side_effect=sample):
            with self.assertRaisesRegex(ValueError, 'disk_floor'): b.finish()
        self.assertTrue(b.stop.is_set())

    def test_receipts_never_overwrite(self):
        with tempfile.TemporaryDirectory() as tmp:
            p = Path(tmp) / 'receipt.json'
            s.write_json(p, {'state': 'failed'})
            with self.assertRaises(FileExistsError): s.write_json(p, {'state': 'passed'})
            self.assertEqual(json.loads(p.read_text())['state'], 'failed')

    def test_source_tamper_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            p = Path(tmp); (p / 'x').write_bytes(b'one')
            rows = [{'path': 'x', 'bytes': 3, 'sha256': s.sha(p / 'x')}]
            inputs = {'schema': 'luobo.cloud-android.source-inputs.v1', 'cohort': run.COHORT,
                      'files': rows, 'members': 1, 'source_digest': verify_inputs.digest(rows)}
            verify_inputs.verify(p, inputs)
            (p / 'x').write_bytes(b'two')
            with self.assertRaisesRegex(ValueError, 'SHA256'): verify_inputs.verify(p, inputs)

    def test_source_symlink_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            p = Path(tmp); (p / 'a').write_bytes(b'x'); (p / 'b').symlink_to('a')
            with self.assertRaisesRegex(ValueError, 'symlink'): verify_inputs.checked_path(p, 'b')

    def test_unsafe_ZIP_entries_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            for value in ['../escape', '/escape', 'a//b']:
                p = Path(tmp) / 'bad.zip'
                with zipfile.ZipFile(p, 'w') as z: z.writestr(value, b'x')
                with self.assertRaisesRegex(ValueError, 'unsafe'): audit.checked_zip(p)

    def test_generated_signature_types_not_parameter_names(self):
        a = audit.signatures('public static native void ping(String owner, long n);')
        b = audit.signatures('public static native void ping(String actor, long count);')
        self.assertEqual(a, b)
        self.assertNotEqual(a, audit.signatures('public static native void ping(String owner, int n);'))

    def test_ELF_arch_and_16KiB_proof(self):
        header = b'\x7fELF\x02\x01\x01' + b'\0' * 9
        header += struct.pack('<HHIQQQIHHHHHH', 3, 183, 1, 0, 64, 0, 0, 64, 56, 2, 0, 0, 0)
        body = header + struct.pack('<IIQQQQQQ', 1, 5, 0, 0, 0, 176, 16384, 16384)
        body += struct.pack('<IIQQQQQQ', 0x6474e552, 4, 0, 0, 0, 0, 16384, 1)
        self.assertTrue(audit.elf(body)['LOAD_16KiB'])
        self.assertTrue(audit.elf(body)['RELRO_16KiB'])
        bad = bytearray(body); struct.pack_into('<H', bad, 18, 62)
        with self.assertRaisesRegex(ValueError, 'AArch64'): audit.elf(bad)

    def test_actual_Maven_origin_accounting(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); p = root / 'gradle-cache/caches/modules-2/files-2.1/test.group/demo/1.0/hash/demo-1.0.jar'
            p.parent.mkdir(parents=True); p.write_bytes(b'synthetic')
            url = 'https://repo.maven.apache.org/maven2/test/group/demo/1.0/demo-1.0.jar'
            proof = run.maven_origins(root, 'Documentation https://docs.gradle.org/x\nDownloading ' + url + '\n')
            self.assertEqual(proof['artifacts'][0]['observed_sources'], [url])
            with self.assertRaisesRegex(ValueError, 'no captured'): run.maven_origins(root, '')
            with self.assertRaisesRegex(ValueError, 'unapproved'): run.maven_origins(root, 'Downloading https://unknown.invalid/x')

    def test_child_environment_no_credentials_and_offline_controls(self):
        with mock.patch.dict(os.environ, {'GITHUB_TOKEN': 'synthetic', 'HTTPS_PROXY': 'synthetic', 'HOME': '/untrusted'}):
            e = run.environment(Path('/tmp/fresh-owned'), True)
        self.assertNotIn('GITHUB_TOKEN', e); self.assertNotIn('HTTPS_PROXY', e)
        self.assertEqual(e['GOPROXY'], 'off'); self.assertEqual(e['GOSUMDB'], 'off')
        self.assertEqual(e['GOTOOLCHAIN'], 'local'); self.assertEqual(e['HOME'], '/tmp/fresh-owned/home')

    def test_official_SDK_catalog_revision_and_host(self):
        raw = b'''<repository><remotePackage path="build-tools;36.0.0"><revision><major>36</major><minor>0</minor><micro>0</micro></revision><archives>
          <archive><host-os>linux</host-os><complete><size>100</size><checksum type="sha1">0000000000000000000000000000000000000000</checksum><url>synthetic-linux.zip</url></complete></archive>
          <archive><host-os>macosx</host-os><complete><size>100</size><checksum type="sha1">0000000000000000000000000000000000000000</checksum><url>synthetic-mac.zip</url></complete></archive>
        </archives></remotePackage></repository>'''
        rows = run.sdk_catalog(raw, [{'path': 'build-tools;36.0.0', 'revision': '36.0.0'}])
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0]['url'], 'https://dl.google.com/android/repository/synthetic-linux.zip')
        with self.assertRaisesRegex(ValueError, 'unavailable'):
            run.sdk_catalog(raw, [{'path': 'build-tools;36.0.0', 'revision': '36.0.1'}])

    def test_official_SDK_catalog_refuses_external_archive(self):
        raw = b'''<repository><remotePackage path="platforms;android-36"><revision><major>2</major></revision><archives><archive><complete>
          <size>100</size><checksum type="sha1">0000000000000000000000000000000000000000</checksum><url>https://unknown.invalid/package.zip</url>
        </complete></archive></archives></remotePackage></repository>'''
        with self.assertRaisesRegex(ValueError, 'URL'):
            run.sdk_catalog(raw, [{'path': 'platforms;android-36', 'revision': '2'}])


if __name__ == '__main__':
    unittest.main()
