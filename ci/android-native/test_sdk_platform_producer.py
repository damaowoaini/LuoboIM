"""Prepared pure Python file/ZIP tests. No downloads, CLI, Go, or Gradle."""
import hashlib
import io
import json
import os
from copy import deepcopy
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


def finite_fixture():
    prefix = 'android-test/'
    names = [('android.jar', 'classpath'), ('android-stubs-src.jar', 'source-only'),
             ('optional/synthetic.jar', 'classpath')]
    return {'jar_inventory': [{'member': prefix + name, 'path': 'platforms/' + prefix + name,
                              'role': role, 'purpose': 'synthetic test only'} for name, role in names]}


def fixture_zip(expected, omit=None, extra=None, mode=None):
    output = io.BytesIO()
    with zipfile.ZipFile(output, 'w') as bundle:
        for row in expected['jar_inventory']:
            if row['member'] == omit:
                continue
            info = zipfile.ZipInfo(row['member'])
            info.external_attr = (mode if mode is not None and row == expected['jar_inventory'][0]
                                  else 0o100600) << 16
            bundle.writestr(info, b'fixture source bytes')
        if extra:
            bundle.writestr(extra, b'fixture extra')
        bundle.writestr('android-test/source.properties', b'Pkg.Revision=2\n')
    return output.getvalue()


def fixture_claim(expected):
    rows = [{'path': 'android/' + x['path'], 'ZIP_member': x['member'], 'role': x['role'],
             'bytes': 20, 'sha256': hashlib.sha256(b'fixture source bytes').hexdigest(),
             'pin_authority': 'locked-official-archive-member', 'byte_comparison': True}
            for x in expected['jar_inventory']]
    return {'JAR_inventory': rows, 'classpath_JARs': [x for x in rows if x['role'] == 'classpath'],
            'source_only_JARs': [x for x in rows if x['role'] == 'source-only']}


class SDKFullClosureTests(unittest.TestCase):
    def test_reviewed_contract_is_finite29_and26_classpath_with_three_prior_archive_locks(self):
        rows = sdk.contract()
        self.assertEqual([len(x['jar_inventory']) for x in rows], [10, 10, 9])
        self.assertEqual(sum(x['role'] == 'classpath' for p in rows for x in p['jar_inventory']), 26)
        self.assertEqual(sum(x['role'] == 'source-only' for p in rows for x in p['jar_inventory']), 3)
        self.assertEqual(len({x['archive_sha256'] for x in rows}), 3)

    def test_full_archive_enumeration_preserves_source_only_separation(self):
        expected = finite_fixture()
        with zipfile.ZipFile(io.BytesIO(fixture_zip(expected))) as bundle:
            sdk.safe_zip(bundle)
            self.assertEqual(sdk.jar_inventory(bundle, expected), sorted(x['member'] for x in expected['jar_inventory']))
        claim = fixture_claim(expected)
        sdk.validate_jar_claim(expected, claim)
        self.assertEqual(len(claim['classpath_JARs']), 2)
        self.assertEqual(claim['source_only_JARs'][0]['ZIP_member'], 'android-test/android-stubs-src.jar')

    def test_missing_archive_jar_is_rejected_before_installed_file_lookup(self):
        expected = finite_fixture()
        with zipfile.ZipFile(io.BytesIO(fixture_zip(expected, omit='android-test/android.jar'))) as bundle:
            with self.assertRaisesRegex(ValueError, 'inventory missing/extra'):
                sdk.jar_inventory(bundle, expected)

    def test_extra_and_case_drifting_archive_jars_are_not_self_enrolled(self):
        expected = finite_fixture()
        for extra in ['android-test/new.jar', 'android-test/ANDROID.JAR']:
            with self.subTest(extra=extra):
                with zipfile.ZipFile(io.BytesIO(fixture_zip(expected, extra=extra))) as bundle:
                    with self.assertRaisesRegex(ValueError, 'inventory missing/extra'):
                        sdk.jar_inventory(bundle, expected)

    def test_archive_member_fifo_symlink_and_directory_modes_are_rejected(self):
        expected = finite_fixture()
        for mode in [0o010600, 0o120777, 0o040700]:
            with self.subTest(mode=mode):
                with zipfile.ZipFile(io.BytesIO(fixture_zip(expected, mode=mode))) as bundle:
                    with self.assertRaises(ValueError):
                        sdk.jar_inventory(bundle, expected)

    def test_new_pin_is_derived_from_locked_zip_not_installed_body(self):
        expected = finite_fixture()
        with tempfile.TemporaryDirectory() as value:
            work = Path(value).resolve()
            item = expected['jar_inventory'][0]
            target = work / 'android' / item['path']
            s.put(target, b'wrong installed body')
            with zipfile.ZipFile(io.BytesIO(fixture_zip(expected))) as bundle:
                pin = sdk.member_pin(bundle, item['member'], item['path'], Mock())
                self.assertEqual(pin['sha256'], hashlib.sha256(b'fixture source bytes').hexdigest())
                self.assertNotEqual(pin['sha256'], hashlib.sha256(target.read_bytes()).hexdigest())
                with self.assertRaises(ValueError):
                    sdk.compare_member(work, bundle, item['member'], pin, Mock())
                target.write_bytes(b'fixture source bytes')
                self.assertTrue(sdk.compare_member(work, bundle, item['member'], pin, Mock())['byte_comparison'])

    def test_installed_hardlink_cannot_satisfy_archive_member_proof(self):
        expected = finite_fixture()
        with tempfile.TemporaryDirectory() as value:
            work = Path(value).resolve()
            item = expected['jar_inventory'][0]
            target = work / 'android' / item['path']
            s.put(target, b'fixture source bytes')
            os.link(target, target.with_name('second-link'))
            with zipfile.ZipFile(io.BytesIO(fixture_zip(expected))) as bundle:
                pin = sdk.member_pin(bundle, item['member'], item['path'], Mock())
                with self.assertRaisesRegex(ValueError, 'single-link'):
                    sdk.compare_member(work, bundle, item['member'], pin, Mock())

    def test_prior_acquisition_lock_rejects_new_same_size_archive_bytes(self):
        with tempfile.TemporaryDirectory() as value:
            work = Path(value).resolve()
            archive = work / 'fixture.zip'
            s.put(archive, b'original fixture')
            digest = hashlib.sha256(archive.read_bytes()).hexdigest()
            expected = {'official': {'url': 'https://dl.google.com/android/repository/synthetic.zip',
                                      'size': archive.stat().st_size}, 'archive_sha256': digest}
            acquired = {'source_url': expected['official']['url'], 'bytes': archive.stat().st_size,
                        'sha256': digest, 'official_sha1_checked': True}
            self.assertEqual(sdk.archive_pin(work, archive, expected, acquired)['sha256'], digest)
            archive.write_bytes(b'changed! fixture')
            with self.assertRaisesRegex(ValueError, 'prior-acquisition'):
                sdk.archive_pin(work, archive, expected, acquired)

    def test_optional_metadata_positive_and_external_unknown_or_incomplete_references(self):
        inventory = finite_fixture()['jar_inventory']
        rows = [{'name': 'fixture.synthetic', 'jar': 'synthetic.jar', 'manifest': False}]
        self.assertEqual(sdk.optional_metadata(json.dumps(rows).encode(), inventory), rows)
        bad = [[], [{'name': 'x', 'jar': '../synthetic.jar', 'manifest': False}],
               [{'name': 'x', 'jar': 'https://example.invalid/synthetic.jar', 'manifest': False}],
               [{'name': 'x', 'jar': 'synthetic.jar', 'manifest': 'false'}],
               [{'name': 'x', 'jar': 'synthetic.jar', 'manifest': False, 'extra': 1}]]
        for rows in bad:
            with self.subTest(rows=rows):
                with self.assertRaises(ValueError):
                    sdk.optional_metadata(json.dumps(rows).encode(), inventory)

    def test_optional_metadata_duplicate_keys_and_duplicate_library_names_are_rejected(self):
        inventory = finite_fixture()['jar_inventory']
        body = b'[{"name":"x","jar":"synthetic.jar","jar":"synthetic.jar","manifest":false}]'
        with self.assertRaisesRegex(ValueError, 'duplicate optional metadata key'):
            sdk.optional_metadata(body, inventory)
        inventory = inventory + [{'member': 'android-test/optional/other.jar', 'role': 'classpath'}]
        rows = [{'name': 'x', 'jar': 'synthetic.jar', 'manifest': False},
                {'name': 'x', 'jar': 'other.jar', 'manifest': False}]
        with self.assertRaisesRegex(ValueError, 'duplicate declaration'):
            sdk.optional_metadata(json.dumps(rows).encode(), inventory)

    def test_claim_cannot_relabel_source_only_change_path_or_authorize_unknown_source(self):
        expected = finite_fixture()
        for field, bad in [('path', 'android/outside.jar'), ('role', 'classpath'),
                           ('pin_authority', 'installed-file-self-hash')]:
            claim = deepcopy(fixture_claim(expected))
            claim['JAR_inventory'][1][field] = bad
            with self.subTest(field=field):
                with self.assertRaisesRegex(ValueError, 'source authority'):
                    sdk.validate_jar_claim(expected, claim)

    def test_real_system_fault_context_restores_inode_and_unknown_dispatch_is_refused(self):
        with tempfile.TemporaryDirectory() as value:
            work = Path(value).resolve()
            target = work / 'android/platforms/android-37.0/core-for-system-modules.jar'
            s.put(target, b'original synthetic system module')
            source = {**sdk.file_row(work, target), 'ZIP_member': 'android-37.0/core-for-system-modules.jar'}
            before = target.stat()
            with patch.object(sdk, 'verified_sources', return_value={'platforms': [{'classpath_JARs': [source]}]}):
                with self.assertRaisesRegex(RuntimeError, 'fixture child'):
                    with sdk.tamper_system_control(work, b'synthetic fault'):
                        self.assertEqual(target.read_bytes(), b'synthetic fault')
                        raise RuntimeError('fixture child')
            self.assertEqual((target.stat().st_dev, target.stat().st_ino), (before.st_dev, before.st_ino))
            self.assertEqual(target.read_bytes(), b'original synthetic system module')
            with self.assertRaisesRegex(ValueError, 'reviewed SDK fault-control labels'):
                sdk.require_fault_dispatch(SimpleNamespace(w=work), [], work, {}, 120, None, None,
                                           Path('/unused'), ('unused', '0' * 64), 'unknown-control')


if __name__ == '__main__':
    unittest.main()
