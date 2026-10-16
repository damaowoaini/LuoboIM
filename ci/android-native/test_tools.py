#!/usr/bin/env python3
"""Synthetic Python-only guards; no Go/Flutter/Gradle/network/device execution."""
import json
import base64
import hashlib
import importlib.util
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
    def dependency_fixture(self, temporary):
        module_path = run.REPO / run.CORE_TOOLS / 'build_candidate.py'
        spec = importlib.util.spec_from_file_location('reviewed_builder', module_path)
        builder = importlib.util.module_from_spec(spec); spec.loader.exec_module(builder)
        root = Path(temporary).resolve(); work = root / 'fresh'; work.mkdir()
        old = ('example.invalid/original v1.0.0 h1:' + base64.b64encode(b'a' * 32).decode() + '\n').encode()
        new = old + ('example.invalid/added v1.0.0 h1:' + base64.b64encode(b'b' * 32).decode() + '\n').encode()
        (work / 'go.sum').write_bytes(old)
        (work / 'go.mod').write_bytes(b'module synthetic.invalid/core\n')
        reviewed = root / 'acquired-go.sum'; reviewed.write_bytes(new)
        return builder, work, reviewed, old, new

    def test_acquired_lock_preserves_source_and_installs_exact_additive_bytes(self):
        with tempfile.TemporaryDirectory() as temporary:
            builder, work, reviewed, old, new = self.dependency_fixture(temporary)
            module = (work / 'go.mod').read_bytes()
            proof = builder.install_acquired_go_sum(work, reviewed, hashlib.sha256(new).hexdigest(), hashlib.sha256(old).hexdigest())
            self.assertEqual((work / 'go.sum').read_bytes(), new)
            self.assertEqual((work / 'source-go.sum').read_bytes(), old)
            self.assertEqual((work / 'go.mod').read_bytes(), module)
            self.assertEqual(proof['added_checksums'], 1)
            self.assertEqual(json.loads((work / 'acquired-dependency-lock.json').read_text()), proof)

    def test_acquired_lock_requires_both_exact_source_and_acquired_hash(self):
        for which in ['source', 'acquired']:
            with self.subTest(which=which), tempfile.TemporaryDirectory() as temporary:
                builder, work, reviewed, old, new = self.dependency_fixture(temporary)
                source_sha = '0' * 64 if which == 'source' else hashlib.sha256(old).hexdigest()
                acquired_sha = '0' * 64 if which == 'acquired' else hashlib.sha256(new).hexdigest()
                with self.assertRaisesRegex(ValueError, 'SHA mismatch'):
                    builder.install_acquired_go_sum(work, reviewed, acquired_sha, source_sha)
                self.assertEqual((work / 'go.sum').read_bytes(), old)
                self.assertFalse((work / 'source-go.sum').exists())

    def test_acquired_lock_refuses_removed_changed_and_duplicate_source_keys(self):
        for which in ['removed', 'changed', 'duplicate']:
            with self.subTest(which=which), tempfile.TemporaryDirectory() as temporary:
                builder, work, reviewed, old, new = self.dependency_fixture(temporary)
                bad = new[len(old):] if which == 'removed' else old.replace(base64.b64encode(b'a' * 32), base64.b64encode(b'c' * 32)) if which == 'changed' else old + old
                reviewed.write_bytes(bad)
                with self.assertRaises(ValueError):
                    builder.install_acquired_go_sum(work, reviewed, hashlib.sha256(bad).hexdigest(), hashlib.sha256(old).hexdigest())
                self.assertEqual((work / 'go.sum').read_bytes(), old)

    def test_acquired_lock_refuses_links_and_nonowned_work(self):
        for which in ['symlink', 'hardlink', 'nonowned']:
            with self.subTest(which=which), tempfile.TemporaryDirectory() as temporary:
                builder, work, reviewed, old, new = self.dependency_fixture(temporary)
                if which == 'symlink':
                    original = reviewed.with_name('original'); reviewed.rename(original); reviewed.symlink_to(original)
                elif which == 'hardlink':
                    os.link(reviewed, reviewed.with_name('second-link'))
                if which == 'nonowned':
                    with mock.patch.object(builder.os, 'getuid', return_value=os.getuid() + 1), self.assertRaisesRegex(ValueError, 'owned'):
                        builder.install_acquired_go_sum(work, reviewed, hashlib.sha256(new).hexdigest(), hashlib.sha256(old).hexdigest())
                else:
                    with self.assertRaises(ValueError):
                        builder.install_acquired_go_sum(work, reviewed, hashlib.sha256(new).hexdigest(), hashlib.sha256(old).hexdigest())
                self.assertEqual((work / 'go.sum').read_bytes(), old)

    def test_acquired_lock_refuses_existing_backup_without_overwriting_it(self):
        with tempfile.TemporaryDirectory() as temporary:
            builder, work, reviewed, old, new = self.dependency_fixture(temporary)
            backup = work / 'source-go.sum'; backup.write_bytes(b'prior evidence')
            with self.assertRaises(FileExistsError):
                builder.install_acquired_go_sum(work, reviewed, hashlib.sha256(new).hexdigest(), hashlib.sha256(old).hexdigest())
            self.assertEqual(backup.read_bytes(), b'prior evidence')
            self.assertEqual((work / 'go.sum').read_bytes(), old)

    def test_acquired_lock_detects_source_tamper_before_atomic_replacement(self):
        with tempfile.TemporaryDirectory() as temporary:
            builder, work, reviewed, old, new = self.dependency_fixture(temporary)
            original_open = builder.os.open
            def interleave(path, flags, *args, **kwargs):
                if Path(path).name == '.acquired-go.sum.tmp':
                    (work / 'go.sum').write_bytes(b'changed by synthetic concurrent owner')
                return original_open(path, flags, *args, **kwargs)
            with mock.patch.object(builder.os, 'open', side_effect=interleave), self.assertRaisesRegex(ValueError, 'changed before'):
                builder.install_acquired_go_sum(work, reviewed, hashlib.sha256(new).hexdigest(), hashlib.sha256(old).hexdigest())
            self.assertEqual((work / 'go.sum').read_bytes(), b'changed by synthetic concurrent owner')
            self.assertEqual((work / 'source-go.sum').read_bytes(), old)

    def local_work(self, tmp):
        work = Path(tmp).resolve() / 'job'
        (work / 'source/app/android').mkdir(mode=0o700, parents=True)
        (work / 'evidence').mkdir(mode=0o700)
        path = work / 'source/app/android/local.properties'
        raw = ('sdk.dir=' + str(work / 'android') + '\nflutter.sdk=' + str(work / 'flutter')).encode()
        path.write_bytes(raw)
        return work, path, raw

    def test_fresh_generated_local_properties_is_bound_and_receipted(self):
        with tempfile.TemporaryDirectory() as tmp:
            work, path, raw = self.local_work(tmp)
            provenance = {'synthetic_only': True}
            run.update_job_local_properties(work, provenance)
            self.assertEqual((work / 'evidence/local-properties-before.properties').read_bytes(), raw)
            proof = json.loads((work / 'evidence/local-properties-complete.json').read_text())
            self.assertEqual(proof['before_sha256'], s.sha(work / 'evidence/local-properties-before.properties'))
            self.assertEqual(proof['after_sha256'], s.sha(path))
            self.assertEqual(proof['generation'], provenance)
            self.assertIn('cmake.dir=' + str(work / 'android/cmake/3.22.1'), path.read_text())
            self.assertFalse(path.with_name('local.properties.job-generated.tmp').exists())

    def test_generated_local_properties_refuses_external_toolchain_paths(self):
        for key in ['sdk.dir', 'flutter.sdk', 'cmake.dir']:
            with self.subTest(property=key), tempfile.TemporaryDirectory() as tmp:
                work, path, raw = self.local_work(tmp)
                lines = dict(line.split('=', 1) for line in raw.decode().split('\n'))
                lines[key] = '/outside-this-job'
                bad = '\n'.join(k + '=' + v for k, v in lines.items()).encode()
                path.write_bytes(bad)
                with self.assertRaisesRegex(ValueError, 'conflicting'):
                    run.update_job_local_properties(work, {})
                self.assertEqual(path.read_bytes(), bad)
                self.assertFalse((work / 'evidence/local-properties-intent.json').exists())

    def test_generated_local_properties_refuses_unknown_or_duplicate_properties(self):
        for extra in ['\norg.gradle.jvmargs=-Xmx99g', '\nsdk.dir=duplicate', '\nmalformed-line']:
            with self.subTest(suffix=extra), tempfile.TemporaryDirectory() as tmp:
                work, path, raw = self.local_work(tmp)
                bad = raw + extra.encode(); path.write_bytes(bad)
                with self.assertRaisesRegex(ValueError, 'unknown, duplicate or conflicting'):
                    run.update_job_local_properties(work, {})
                self.assertEqual(path.read_bytes(), bad)

    def test_generated_local_properties_refuses_leaf_and_parent_symlinks(self):
        for which in ['leaf', 'parent']:
            with self.subTest(link=which), tempfile.TemporaryDirectory() as tmp:
                work, path, raw = self.local_work(tmp)
                outside = Path(tmp) / 'outside'
                if which == 'leaf':
                    outside.write_bytes(b'untouched'); path.unlink(); path.symlink_to(outside)
                else:
                    path.parent.rename(outside); path.parent.symlink_to(outside, target_is_directory=True)
                with self.assertRaises((OSError, ValueError)):
                    run.update_job_local_properties(work, {})
                self.assertEqual(outside.read_bytes() if which == 'leaf' else (outside / 'local.properties').read_bytes(),
                                 b'untouched' if which == 'leaf' else raw)

    def test_generated_local_properties_does_not_overwrite_other_files(self):
        for occupied in ['evidence/local-properties-before.properties',
                         'source/app/android/local.properties.job-generated.tmp']:
            with self.subTest(occupied=occupied), tempfile.TemporaryDirectory() as tmp:
                work, path, raw = self.local_work(tmp)
                other = work / occupied; other.write_bytes(b'preexisting-sentinel')
                with self.assertRaises(FileExistsError):
                    run.update_job_local_properties(work, {})
                self.assertEqual(other.read_bytes(), b'preexisting-sentinel')
                self.assertEqual(path.read_bytes(), raw)

    def test_exact_metadata_does_not_read_mutable_App(self):
        registry, inputs, tools = run.metadata()
        self.assertEqual(registry['source_ref'], '314ac556fdc4be7f98dbd74370201922c71e67c5')
        self.assertEqual(len(inputs['files']), 427)
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
