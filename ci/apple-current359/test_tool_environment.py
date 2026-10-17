"""New environment controls only; simulated host commands, no sysctl/native execution."""
import copy
import os
from pathlib import Path
import stat
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock

import run as driver


class BootstrapEnvironment(unittest.TestCase):
    def test_real_env_adds_only_system_sbin_without_HOME_or_sandbox_grants(self):
        home = os.environ['HOME']
        selected = {'ruby': {'path': '/opt/homebrew/Cellar/ruby@3.3/fixed/bin/ruby'},
                    'gem_root': '/opt/homebrew/lib/ruby/gems/3.3.0'}
        pod = Path(selected['gem_root'])/'bin/pod'
        original_exists = Path.exists
        guarded_home = {Path(home)/n for n in ['.flutter_settings', '.flutter_tool_state', '.dart-tool']}
        def exists(path):
            return False if path in guarded_home else original_exists(path)
        with tempfile.TemporaryDirectory() as temp:
            work = Path(temp)
            with mock.patch.object(driver.pod_selector, 'recheck', return_value=pod), \
                    mock.patch.object(Path, 'exists', exists), \
                    mock.patch.dict(os.environ, {'PATH': '/untrusted', 'HTTPS_PROXY': 'https://untrusted.invalid'}):
                actual = driver.env(work, selected)
            self.assertEqual(actual['HOME'], home)
            self.assertEqual(actual['PATH'], ':'.join([str(work/'flutter/bin'), str(pod.parent),
                             str(Path(selected['ruby']['path']).parent), '/usr/bin', '/bin', '/usr/sbin']))
            self.assertEqual(actual['TMPDIR'], str(work/'tmp'))
            self.assertEqual(actual['GIT_CONFIG_GLOBAL'], '/dev/null')
            self.assertEqual(actual['xcrun_nocache'], '1')
            self.assertNotIn('HTTPS_PROXY', actual)
            for stage in ['acquire', 'resolve', 'package']:
                profile = driver.profile(work, stage).decode()
                self.assertIn('(deny file-write*)', profile)
                self.assertNotIn('sysctl', profile)
                self.assertNotIn('/var/folders', profile)
                self.assertNotIn('(allow sysctl', profile)
            self.assertNotIn('allow network-outbound', driver.profile(work, 'package').decode())

    def test_actual_bootstrap_requires_exact_read_query_signature_stable_tool_and_clean_result(self):
        with tempfile.TemporaryDirectory() as temp:
            work = Path(temp); (work/'evidence').mkdir()
            environment = {'PATH': '/usr/bin:/bin:/usr/sbin', 'HOME': os.environ['HOME']}
            row = {'path': '/usr/sbin/sysctl', 'bytes': 1024, 'sha256': 'a'*64,
                   'identity': [1, 2, stat.S_IFREG | 0o755, 0, 0, 2, 1024, 3, 4],
                   'nlink': 2, 'system_hardlink_exception': True}
            for variant in ['valid', 'wrong-resolution', 'wrong-result', 'stderr', 'nonzero', 'changed']:
                with self.subTest(variant=variant):
                    calls = []
                    def command(_work, label, argv, cwd, env):
                        calls.append((label, argv, cwd, env))
                        self.assertIs(env, environment)
                        if label == 'sysctl-signature':
                            self.assertEqual(argv, ['/usr/bin/codesign', '--verify', '--strict', '-R', '=anchor apple', '/usr/sbin/sysctl'])
                            return b''
                        self.assertEqual(argv, ['/usr/sbin/sysctl', '-n', 'hw.optional.arm64'])
                        if variant == 'nonzero':
                            raise ValueError('fixed command failed: sysctl-arm64')
                        (work/'evidence/sysctl-arm64.stderr.log').write_bytes(b'cache warning' if variant == 'stderr' else b'')
                        return b'0\n' if variant == 'wrong-result' else b'1\n'
                    after = copy.deepcopy(row)
                    if variant == 'changed': after['sha256'] = 'b'*64
                    fingerprints = mock.Mock(side_effect=[copy.deepcopy(row), after])
                    host = SimpleNamespace(fingerprint=fingerprints,
                        apple_signature_argv=lambda tool: ['/usr/bin/codesign', '--verify', '--strict', '-R', '=anchor apple', tool])
                    with mock.patch.object(driver.shutil, 'which', return_value='/elsewhere/sysctl' if variant == 'wrong-resolution' else '/usr/sbin/sysctl'), \
                            mock.patch.object(driver, 'load', return_value=host), \
                            mock.patch.object(driver, 'command', side_effect=command), \
                            mock.patch.object(driver, 'record') as record:
                        if variant == 'valid':
                            driver.flutter_bootstrap_architecture(work, environment)
                            self.assertEqual([x[0] for x in calls], ['sysctl-signature', 'sysctl-arm64'])
                            self.assertEqual(record.call_args.args[1]['before'], record.call_args.args[1]['after'])
                            self.assertFalse(record.call_args.args[1]['kernel_state_written'])
                            self.assertEqual(fingerprints.call_count, 2)
                        else:
                            with self.assertRaises(ValueError): driver.flutter_bootstrap_architecture(work, environment)
                            record.assert_not_called()
                            if variant == 'wrong-resolution': self.assertEqual(calls, [])
                            else: self.assertEqual(len(calls), 2)


if __name__ == '__main__':
    unittest.main()
