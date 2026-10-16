"""Four pure controls; no native subprocess, network, download or link."""
import hashlib
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import run


class HostToolControls(unittest.TestCase):
    def test_version_requires_whole_clean_stdout_and_success(self):
        expected = 'Xcode 16.4\nBuild version 16F6'
        run.strict_version(0, (expected+'\n').encode(), b'', expected)
        for code, out, err in [(0, (expected+'\n').encode(), b'cache error\n'),
                               (0, b'warning\n'+expected.encode(), b''),
                               (0, b'Xcode 16.3\nBuild version 16E140', b''),
                               (1, expected.encode(), b'')]:
            with self.subTest(code=code, out=out, err=err), self.assertRaises(ValueError):
                run.strict_version(code, out, err, expected)

    def test_environment_keeps_home_owned_paths_and_no_cache_bypass_claim(self):
        home = os.environ['HOME']
        with patch.dict(os.environ, {'GITHUB_TOKEN': 'synthetic-credential',
                                    'HTTPS_PROXY': 'https://unused.invalid', 'xcrun_nocache': 'ambient'}):
            env = run.native_environment(Path('/owned/probe'), '/fixed/Developer')
        self.assertEqual(env['HOME'], home)
        self.assertEqual(env['TMPDIR'], '/owned/probe/tmp')
        self.assertEqual(env['CLANG_MODULE_CACHE_PATH'], '/owned/probe/cache')
        self.assertEqual(env['DEVELOPER_DIR'], '/fixed/Developer')
        self.assertNotIn('GITHUB_TOKEN', env)
        self.assertNotIn('HTTPS_PROXY', env)
        self.assertNotIn('xcrun_nocache', env)

    def test_streaming_fingerprint_keeps_official_hardlink_exception_bounded(self):
        # Real owned files span three stream chunks; no large host tool copied.
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve(strict=True)
            tool, alias = root/'tool', root/'alias'
            chunk = b'x' * (1024**2)
            with tool.open('xb') as stream:
                for _ in range(3):
                    stream.write(chunk)
            os.link(tool, alias)
            cap = 4 * 1024**2
            with self.assertRaisesRegex(ValueError, 'canonical bounded regular'):
                run.fingerprint(tool, cap)  # A repository input cannot borrow the exception.
            actual = run.fingerprint(tool, cap, hardlink=True)
            self.assertEqual(actual['bytes'], 3 * 1024**2)
            self.assertEqual(actual['sha256'], hashlib.sha256(chunk * 3).hexdigest())
            self.assertEqual(actual['nlink'], 2)
            self.assertEqual(actual['identity'][5], 2)
            self.assertEqual(actual['identity'][1], tool.stat().st_ino)
            self.assertTrue(actual['system_hardlink_exception'])
            with self.assertRaisesRegex(ValueError, 'canonical bounded regular'):
                run.fingerprint(tool, actual['bytes'] - 1, hardlink=True)
            alias.unlink()
            regular = run.fingerprint(tool, cap)
            self.assertEqual(regular['sha256'], actual['sha256'])
            self.assertEqual(regular['nlink'], 1)
            self.assertFalse(regular['system_hardlink_exception'])

    def test_signature_argv_uses_inline_Apple_anchor_and_strict_verification(self):
        tool = Path('/Applications/Xcode_16.4.app/Contents/Developer/usr/bin/xcodebuild')
        self.assertEqual(run.apple_signature_argv(tool), [
            '/usr/bin/codesign', '--verify', '--strict', '-R', '=anchor apple', str(tool),
        ])


if __name__ == '__main__':
    unittest.main(verbosity=2)
