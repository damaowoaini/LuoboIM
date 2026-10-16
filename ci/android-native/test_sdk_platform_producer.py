"""Prepared pure Python file/ZIP tests. No downloads, CLI, Go, or Gradle."""
import hashlib
import io
from pathlib import Path
from types import SimpleNamespace
import tempfile
import unittest
from unittest.mock import Mock, patch
import zipfile

import sdk_platform_producer as sdk
import support as s


class SDKSourceTests(unittest.TestCase):
    def test_original_git_boundary_accepts_only_real_fresh_empty_flutter_destination(self):
        with tempfile.TemporaryDirectory() as value:
            work = Path(value).resolve()
            s.mkdir(work / 'flutter')
            job = SimpleNamespace(w=work)
            args = ['git', 'init', '--quiet', work / 'flutter']
            env = sdk.parent.environment(work, False)
            sdk.require_initial_boundary(job, args, work, env, 900, None, None)
            s.put(work / 'flutter/not-empty', b'x')
            with self.assertRaisesRegex(ValueError, 'fresh empty'):
                sdk.require_initial_boundary(job, args, work, env, 900, None, None)

    def test_original_boundary_rejects_changed_command_cwd_and_offline_environment(self):
        with tempfile.TemporaryDirectory() as value:
            work = Path(value).resolve()
            s.mkdir(work / 'flutter')
            job, args = SimpleNamespace(w=work), ['git', 'init', '--quiet', work / 'flutter']
            online = sdk.parent.environment(work, False)
            for command, cwd, env in [(args + ['extra'], work, online),
                    (args, work / 'flutter', online), (args, work, sdk.parent.environment(work, True))]:
                with self.subTest(command=list(map(str, command)), cwd=str(cwd)):
                    with self.assertRaisesRegex(ValueError, 'exact inherited'):
                        sdk.require_initial_boundary(job, command, cwd, env, 900, None, None)

    def test_zip_crc_and_canonical_regular_members(self):
        data = io.BytesIO()
        with zipfile.ZipFile(data, 'w') as z:
            z.writestr('android-test/', b'')
            z.writestr('android-test/android.jar', b'synthetic')
        with zipfile.ZipFile(io.BytesIO(data.getvalue())) as z:
            sdk.safe_zip(z)
        for name, mode in [('../escape', 0o100600), ('link', 0o120777)]:
            data = io.BytesIO()
            with zipfile.ZipFile(data, 'w') as z:
                entry = zipfile.ZipInfo(name)
                entry.external_attr = mode << 16
                z.writestr(entry, b'synthetic')
            with zipfile.ZipFile(io.BytesIO(data.getvalue())) as z:
                with self.assertRaises(ValueError):
                    sdk.safe_zip(z)

    def test_selected_member_matches_real_installed_bytes_and_static_sha(self):
        with tempfile.TemporaryDirectory() as value:
            work = Path(value).resolve()
            body = b'synthetic official member'
            pin = {'path': 'platforms/test/android.jar', 'bytes': len(body),
                   'sha256': hashlib.sha256(body).hexdigest()}
            s.put(work / 'android' / pin['path'], body)
            data = io.BytesIO()
            with zipfile.ZipFile(data, 'w') as z:
                z.writestr('test/android.jar', body)
            with zipfile.ZipFile(io.BytesIO(data.getvalue())) as z:
                row = sdk.compare_member(work, z, 'test/android.jar', pin, Mock())
            self.assertTrue(row['byte_comparison'])
            self.assertEqual(row['sha256'], pin['sha256'])

    def test_selected_member_same_size_different_body_and_symlink_are_rejected(self):
        with tempfile.TemporaryDirectory() as value:
            work = Path(value).resolve()
            path = work / 'android/platforms/test/android.jar'
            s.put(path, b'bad')
            pin = {'path': 'platforms/test/android.jar', 'bytes': 3, 'sha256': hashlib.sha256(b'yes').hexdigest()}
            data = io.BytesIO()
            with zipfile.ZipFile(data, 'w') as z:
                z.writestr('test/android.jar', b'yes')
            with zipfile.ZipFile(io.BytesIO(data.getvalue())) as z:
                with self.assertRaisesRegex(ValueError, 'differs from installed'):
                    sdk.compare_member(work, z, 'test/android.jar', pin, Mock())
            path.rename(path.with_name('original'))
            path.symlink_to('original')
            with self.assertRaisesRegex(ValueError, 'canonical'):
                sdk.file_row(work, path)

    def test_installed_package_xml_requires_exact_identity_and_revision(self):
        body = b'<sdk><localPackage path="platforms;android-test"><revision><major>2</major></revision></localPackage></sdk>'
        expected = {'package': 'platforms;android-test', 'revision': '2'}
        sdk.package_revision(body, expected)
        for altered in [body.replace(b'android-test', b'android-other'),
                        body.replace(b'<major>2', b'<major>3'), b'<!DOCTYPE x>' + body]:
            with self.assertRaises(ValueError):
                sdk.package_revision(altered, expected)

    def test_actual_fault_context_restores_original_inode_when_child_raises(self):
        with tempfile.TemporaryDirectory() as value:
            work = Path(value).resolve()
            target = work / 'android/platforms/test/android.jar'
            s.put(target, b'original synthetic SDK')
            source = sdk.file_row(work, target)
            before = target.stat()
            with patch.object(sdk, 'verified_sources', return_value={'platforms': [{'jar': source}]}):
                with self.assertRaisesRegex(RuntimeError, 'synthetic child failure'):
                    with sdk.tamper_for_control(work, b'synthetic replacement'):
                        self.assertEqual(target.read_bytes(), b'synthetic replacement')
                        raise RuntimeError('synthetic child failure')
            self.assertEqual((target.stat().st_dev, target.stat().st_ino), (before.st_dev, before.st_ino))
            self.assertEqual(target.read_bytes(), b'original synthetic SDK')
            self.assertTrue((work / 'evidence/selector-controls/sdk-samepath-tamper/fault-restored.json').is_file())

    def test_uncertain_fault_input_is_not_overwritten_original_backup_is_retained(self):
        with tempfile.TemporaryDirectory() as value:
            work = Path(value).resolve()
            target = work / 'android/platforms/test/android.jar'
            s.put(target, b'original synthetic SDK')
            source = sdk.file_row(work, target)
            with patch.object(sdk, 'verified_sources', return_value={'platforms': [{'jar': source}]}):
                with self.assertRaisesRegex(ValueError, 'retain original backup'):
                    with sdk.tamper_for_control(work, b'synthetic replacement'):
                        target.write_bytes(b'unexpected third body')
            self.assertEqual(target.read_bytes(), b'unexpected third body')
            backup = work / 'evidence/selector-controls/sdk-samepath-tamper/original-android.jar'
            self.assertEqual(backup.read_bytes(), b'original synthetic SDK')
            self.assertFalse((backup.parent / 'fault-restored.json').exists())


if __name__ == '__main__':
    unittest.main()
