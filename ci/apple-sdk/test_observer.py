"""Only the new observer argv contract; real Python child, never Go/native/network."""
import contextlib
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

import apple_observer as observer


class TidyArgv(unittest.TestCase):
    @contextlib.contextmanager
    def fixture(self, platform):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            (root / 'bin').mkdir(mode=0o700)
            parent = root / 'tmp/gomobile-work-fixed' / platform
            cwd = parent / 'src-arm64'
            for name in ['src', 'src-arm64']:
                source = parent / name / 'gobind'
                source.mkdir(parents=True, mode=0o700)
                for file, body in [('source.go', b'package gobind\n'),
                                   ('source.m', b'// synthetic Objective-C\n'),
                                   ('source.h', b'// synthetic header\n')]:
                    (source / file).write_bytes(body)
            (cwd / 'go.mod').write_bytes(b'module fixture.invalid/generated\n')
            real_go = root / 'python-go-stub'
            # A genuine child process records exact argv and modifies only the synthetic lock.
            real_go.write_text('#!' + sys.executable + '\n'
                               'import json, os, sys\n'
                               'from pathlib import Path\n'
                               'Path(os.environ["FIXTURE_ROOT"]).joinpath("argv.json").write_text(json.dumps(sys.argv[1:]))\n'
                               'Path("go.sum").write_bytes(b"synthetic acquired lock\\n")\n')
            real_go.chmod(0o700)
            helper_path = Path(__file__).resolve().parents[1] / 'android-native/go_observer.py'
            spec = importlib.util.spec_from_file_location('fixture_owned_io', helper_path)
            helper = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(helper)
            config = {'cohort': 'auditable-sdk-apple-arm64-v1', 'root': str(root),
                      'go': str(real_go), 'go_sha256': hashlib.sha256(real_go.read_bytes()).hexdigest(),
                      'base_path': '/usr/bin:/bin', 'observer_helper': str(helper_path),
                      'observer_helper_sha256': hashlib.sha256(helper_path.read_bytes()).hexdigest()}
            helper.record(root / 'observer.json', config)
            env = {'PATH': str(root / 'bin') + ':' + config['base_path'],
                   'APPLE_OBSERVER_CONFIG': str(root / 'observer.json'),
                   'APPLE_OBSERVER_SHA': hashlib.sha256((root / 'observer.json').read_bytes()).hexdigest(),
                   'GOOS': 'ios', 'GOARCH': 'arm64',
                   'DARWIN_SDK': 'iphoneos' if platform == 'ios' else 'iphonesimulator',
                   'FIXTURE_ROOT': str(root), 'PYTHONDONTWRITEBYTECODE': '1'}
            with patch.dict(os.environ, env, clear=True), contextlib.chdir(cwd):
                yield root, parent

    def accepted(self, argv, platform):
        with self.fixture(platform) as (root, parent):
            original = (parent / 'src-arm64/go.mod').read_bytes()
            with patch.object(sys, 'argv', ['observer', *argv]):
                self.assertEqual(observer.main(), 0)
            self.assertEqual(json.loads((root / 'argv.json').read_bytes()), argv)
            evidence = root / 'evidence/generated' / platform
            intent = json.loads((evidence / 'intent.json').read_bytes())
            self.assertEqual(intent['argv'], argv)
            self.assertEqual(intent['platform'], platform)
            for name in ['before', 'after']:
                manifest = json.loads((evidence / name / 'manifest.json').read_bytes())
                self.assertTrue(manifest['complete_source_snapshot'])
                self.assertIsNone(manifest['error'])
                paths = {x['path'] for x in manifest['files']}
                self.assertIn('src-arm64/go.mod', paths)
                self.assertIn('src/gobind/source.m', paths)
                self.assertIn('src-arm64/gobind/source.h', paths)
                self.assertEqual((evidence / name / 'src-arm64/go.mod').read_bytes(), original)
            self.assertFalse((evidence / 'before/src-arm64/go.sum').exists())
            self.assertEqual((evidence / 'after/src-arm64/go.sum').read_bytes(), b'synthetic acquired lock\n')
            result = json.loads((evidence / 'result.json').read_bytes())
            self.assertEqual(result['exit_code'], 0)
            self.assertIsNone(result['failure_type'])
            self.assertTrue(result['source_unchanged'])

    def test_actual_main_verbose_tidy_preserves_argv_and_snapshots(self):
        self.accepted(['mod', 'tidy', '-v'], 'ios')

    def test_actual_main_plain_tidy_preserves_argv_and_snapshots(self):
        self.accepted(['mod', 'tidy'], 'iossimulator')

    def test_unknown_flags_and_combinations_refused_before_dispatch(self):
        for argv in [['mod', 'tidy', '-e'], ['mod', 'tidy', '-v', '-e'],
                     ['mod', 'tidy', '-v', '-v'], ['mod', 'tidy', '-mod=mod']]:
            with self.subTest(argv=argv), self.fixture('ios') as (root, _):
                with patch.object(sys, 'argv', ['observer', *argv]), patch.object(observer.subprocess, 'Popen') as dispatch:
                    with self.assertRaisesRegex(ValueError, '^unexpected tidy argv$'):
                        observer.main()
                    dispatch.assert_not_called()
                self.assertFalse((root / 'argv.json').exists())
                self.assertFalse((root / 'evidence').exists())


if __name__ == '__main__':
    unittest.main()
