"""New targeted pure controls only; no Flutter/Xcode/subprocess/network execution."""
from pathlib import Path
import plistlib
import tempfile
import unittest
import zipfile

import packaging


def put(path, body):
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    with path.open('xb') as f: f.write(body)


class Current354Packaging(unittest.TestCase):
    def test_static_wrapper_only_two_plists_preserves_original17_and_binary_identity(self):
        with tempfile.TemporaryDirectory() as name:
            root = Path(name).resolve(); original, target = root/'original', root/'derived'
            body = plistlib.dumps({'MinimumOSVersion': '100.0', 'CFBundleExecutable': 'OpenIMCore',
                                   'CFBundleIdentifier': 'org.golang.OpenIMCore'})
            data = {'Info.plist': b'top-level-unchanged'}
            for kind in ['ios-arm64', 'ios-arm64-simulator']:
                base = kind+'/OpenIMCore.framework/'
                data[base+'Info.plist'] = body
                data[base+'OpenIMCore'] = (kind+' static archive fixture').encode()
                for number in range(6): data[base+f'Headers/{number}.h'] = str(number).encode()
            self.assertEqual(len(data), 17)
            rows = []
            for path, b in data.items():
                put(original/path, b)
                rows.append({'path': path, 'bytes': len(b), 'sha256': packaging.sha(b)})
            result = packaging.stage_core(original, target, rows, put)
            self.assertEqual(sum(r['wrapper_only_changed'] for r in result['derived_core17']), 2)
            self.assertEqual({p: (original/p).read_bytes() for p in data}, data)
            for path, b in data.items():
                if path.endswith('.framework/Info.plist'):
                    actual = plistlib.loads((target/path).read_bytes())
                    self.assertEqual(actual['MinimumOSVersion'], '15.0')
                    self.assertEqual({k: v for k, v in actual.items() if k != 'MinimumOSVersion'},
                                     {k: v for k, v in plistlib.loads(b).items() if k != 'MinimumOSVersion'})
                else: self.assertEqual((target/path).read_bytes(), b)

    def test_wrong_original_metadata_never_authorizes_replacement(self):
        for version in ['15.0', '100', None]:
            with self.subTest(version=version), self.assertRaises(ValueError):
                packaging.derived_plist(plistlib.dumps({'MinimumOSVersion': version or '',
                                                      'CFBundleExecutable': 'OpenIMCore'}))
        with self.assertRaises(ValueError):
            packaging.derived_plist(plistlib.dumps({'MinimumOSVersion': '100.0',
                                                  'CFBundleExecutable': 'UnknownCore'}))

    def test_no_self_issued_or_missing_native_approval_can_enter_App_stage(self):
        raw = b'{"source_ref":"fixed"}'
        lane = {'identity': 'test', 'source_ref': 'fixed', 'approved_native_resolution': None}
        with self.assertRaisesRegex(ValueError, 'Root-accepted'):
            packaging.require_approval(lane, raw)
        lane['approved_native_resolution'] = {'identity': 'test', 'source_ref': 'wrong',
            'catalogue_sha256': packaging.sha(raw), 'acceptance_receipt_sha256': 'accepted'}
        with self.assertRaises(ValueError): packaging.require_approval(lane, raw)
        lane['approved_native_resolution']['source_ref'] = 'fixed'
        self.assertEqual(packaging.require_approval(lane, raw), {'source_ref': 'fixed'})
        with self.assertRaises(ValueError): packaging.require_approval(lane, raw+b' ')

    def test_new_sqlite_hook_seed_requires_exact_bytes_and_never_copies_stamps(self):
        with tempfile.TemporaryDirectory() as name:
            root = Path(name).resolve(); source = root/'source'; source.mkdir(mode=0o700)
            asset = root/'asset'; asset.write_bytes(b'exact-locked-native-source')
            row = {'filename': 'libsqlite3.arm64.ios.dylib', 'sha256': packaging.sha(asset.read_bytes())}
            result = packaging.seed_asset(asset, row, source, put)
            self.assertEqual((source/result['seed_relative']).read_bytes(), asset.read_bytes())
            self.assertFalse(result['old_hook_snapshot_or_stamp_copied'])
            self.assertEqual([p.name for p in source.rglob('*') if p.is_file()], ['libsqlite3.dylib'])
            row['sha256'] = '0'*64
            with self.assertRaises(ValueError): packaging.seed_asset(asset, row, source, put)

    def test_consumed_WebRTC_is_bound_to_archive_body_and_complete_members(self):
        with tempfile.TemporaryDirectory() as name:
            root = Path(name).resolve(); archive = root/'source.zip'
            framework = root/'WebRTC.xcframework'; framework.mkdir(mode=0o700)
            data = {'Info.plist': b'plist fixture', 'ios-arm64/WebRTC.framework/WebRTC': b'binary fixture'}
            with zipfile.ZipFile(archive, 'w') as z:
                for path, b in data.items(): z.writestr('WebRTC.xcframework/'+path, b)
            for path, b in data.items(): put(framework/path, b)
            proof = packaging.webrtc_consumed(archive, framework)
            self.assertTrue(proof['all_actual_members_match'])
            binary = framework/'ios-arm64/WebRTC.framework/WebRTC'
            binary.write_bytes(b'changed')
            with self.assertRaises(ValueError): packaging.webrtc_consumed(archive, framework)
            binary.write_bytes(data['ios-arm64/WebRTC.framework/WebRTC'])
            (framework/'unlisted').write_bytes(b'unknown')
            with self.assertRaises(ValueError): packaging.webrtc_consumed(archive, framework)

    def test_unsigned_two_platform_commands_use_one_warmed_cache_and_no_resolution_override(self):
        rows = packaging.app_commands(Path('/tmp/synthetic-owned'), '/fixed/real/xcodebuild')
        self.assertEqual([kind for kind, _ in rows], ['device', 'simulator'])
        for _, command in rows:
            self.assertEqual(command[0], '/fixed/real/xcodebuild')
            self.assertIn('ARCHS=arm64', command)
            self.assertIn('CODE_SIGNING_ALLOWED=NO', command)
            self.assertIn('-onlyUsePackageVersionsFromResolvedFile', command)
            self.assertIn('-disableAutomaticPackageResolution', command)
            self.assertIn('/tmp/synthetic-owned/source/app/build/ios/SourcePackages', command)
            self.assertNotIn('-allowProvisioningUpdates', command)


if __name__ == '__main__': unittest.main()
