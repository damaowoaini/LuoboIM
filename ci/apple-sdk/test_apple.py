"""Targeted new Python gates only. No native tools, network or product tests."""
import io
import os
from pathlib import Path
import plistlib
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import apple_core as build
import link_probe


class Gates(unittest.TestCase):
    def test_exact_target_triples(self):
        self.assertEqual(build.TARGETS, ('ios/arm64', 'iossimulator/arm64'))
        self.assertEqual(link_probe.target('device'), 'arm64-apple-ios15.0')
        self.assertEqual(link_probe.target('simulator'), 'arm64-apple-ios15.0-simulator')
        with self.assertRaises(ValueError):
            link_probe.target('macos')

    def test_read_rejects_symlink_and_hardlink(self):
        with tempfile.TemporaryDirectory() as t:
            p = Path(t)/'body'; p.write_bytes(b'fixed')
            a = Path(t)/'alias'; a.symlink_to(p)
            with self.assertRaises(ValueError):
                build.read(a)
            a.unlink(); os.link(p, a)
            with self.assertRaises(ValueError):
                build.read(p)

    def test_archive_traversal_and_link_refused_before_write(self):
        with tempfile.TemporaryDirectory() as t:
            for name, kind in [('../outside', tarfile.REGTYPE), ('alias', tarfile.SYMTYPE)]:
                body = io.BytesIO()
                with tarfile.open(fileobj=body, mode='w') as a:
                    item = tarfile.TarInfo(name); item.type = kind
                    a.addfile(item)
                with self.assertRaises(ValueError):
                    build.extract_source(body.getvalue(), Path(t))
                self.assertEqual(list(Path(t).iterdir()), [])

    def test_low_disk_admission_is_real_gate(self):
        with tempfile.TemporaryDirectory() as t:
            host = Path(t).resolve(); (host/'.qa').mkdir(mode=0o700)
            calls = []
            def free(p):
                calls.append(p); return build.INITIAL-1
            with self.assertRaisesRegex(ValueError, '4GiB'):
                build.admit(host/'.qa/apple-short', host, free=free)
            self.assertEqual(set(calls), {host, host/'.qa'})
            self.assertFalse((host/'.qa/apple-short').exists())

    def test_input_environment_discards_ambient_proxy_and_flags(self):
        c = {'go': '/fixed/go/bin/go', 'gomobile': '/fixed/mobile/gomobile',
             'developer': '/fixed/Xcode', 'module_cache': '/fixed/modules',
             'python': '/fixed/python3',
             'go_sha256': 'a'*64, 'observer_helper': '/fixed/helper', 'observer_helper_sha256': 'b'*64}
        with patch.dict(os.environ, {'HTTPS_PROXY': 'synthetic', 'GOFLAGS': '-mod=mod', 'GOTOOLCHAIN': 'auto'}):
            env = build.environment(c, Path('/owned/job'))
        self.assertNotIn('HTTPS_PROXY', env)
        self.assertEqual(env['GOFLAGS'], '-mod=readonly')
        self.assertEqual(env['GOPROXY'], 'off')
        self.assertEqual(env['GOTOOLCHAIN'], 'local')

    def test_control_tamper_detected(self):
        c = {'go': '/fixed/go/bin/go', 'gomobile': '/fixed/mobile/gomobile',
             'python': '/fixed/python3',
             'go_sha256': 'a'*64, 'observer_helper': '/fixed/helper', 'observer_helper_sha256': 'b'*64}
        with tempfile.TemporaryDirectory() as t:
            root = Path(t).resolve()
            for p, body in build.controls(c, root).items():
                build.put(root/p, body)
            build.check_controls(c, root)
            (root/'offline.sb').write_bytes(b'changed')
            with self.assertRaisesRegex(ValueError, 'control'):
                build.check_controls(c, root)

    def test_xcframework_requires_both_exact_arm64_slices(self):
        rows = [{'LibraryIdentifier': 'ios-arm64', 'LibraryPath': 'OpenIMCore.framework',
                 'SupportedPlatform': 'ios', 'SupportedArchitectures': ['arm64']},
                {'LibraryIdentifier': 'ios-arm64-simulator', 'LibraryPath': 'OpenIMCore.framework',
                 'SupportedPlatform': 'ios', 'SupportedPlatformVariant': 'simulator', 'SupportedArchitectures': ['arm64']}]
        with tempfile.TemporaryDirectory() as t:
            root = Path(t).resolve(); p = root/'Info.plist'
            p.write_bytes(plistlib.dumps({'AvailableLibraries': rows}))
            self.assertEqual(set(build.framework_slices(root)), {'device', 'simulator'})
            rows[1]['SupportedArchitectures'].append('x86_64')
            p.write_bytes(plistlib.dumps({'AvailableLibraries': rows}))
            with self.assertRaises(ValueError):
                build.framework_slices(root)

    def test_writes_never_overwrite_existing_file(self):
        with tempfile.TemporaryDirectory() as t:
            p = Path(t).resolve()/'record.json'; build.put(p, b'first')
            with self.assertRaises(FileExistsError):
                build.put(p, b'other')
            self.assertEqual(p.read_bytes(), b'first')


if __name__ == '__main__':
    unittest.main()
