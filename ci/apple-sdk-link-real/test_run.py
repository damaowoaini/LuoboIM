"""Two new pure caller/source controls. No native tools, artifacts or network."""
import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

import run


class RealToolLinkControls(unittest.TestCase):
    def spec(self):
        return json.loads((run.HERE/'inputs.json').read_bytes())

    def test_actual_query_passes_fixed_real_tool_and_preserves_strict_clean_streams(self):
        spec = self.spec()
        host = run.load(run.REPO/'ci/apple-host-tool/run.py', 'test_R9_accepted_host_functions')
        calls = []
        with tempfile.TemporaryDirectory() as text:
            root = Path(text).resolve(); (root/'evidence').mkdir()
            def command(directory, label, argv, cwd, env, output):
                self.assertEqual(directory, root); self.assertEqual(cwd, root)
                calls.append(list(map(str, argv)))
                output.write_bytes(b'Xcode 16.4\nBuild version 16F6\n' if label == 'xcode-version' else b'')
                (root/'evidence'/(label+'.log')).write_bytes(b'')
                (root/'evidence'/(label+'.result.json')).write_text('{"exit_code":0}')
            run.query_real_tool(spec, root, {}, command, host)
            self.assertEqual(calls, [
                ['/usr/bin/codesign', '--verify', '--strict', '-R', '=anchor apple', run.REAL_TOOL],
                [run.REAL_TOOL, '-version'],
            ])
            (root/'evidence/xcode-version.log').write_bytes(b'cache warning\n')
            with self.assertRaisesRegex(ValueError, 'exact clean version'):
                host.strict_version(0, (root/'evidence/xcode-version.stdout.log').read_bytes(),
                                    (root/'evidence/xcode-version.log').read_bytes(),
                                    'Xcode 16.4\nBuild version 16F6')

    def test_unreviewed_source_is_refused_and_original_R8_identity_and_inputs_remain(self):
        spec = self.spec()
        original_path = run.REPO/'ci/apple-sdk-link/inputs.json'
        original = json.loads(original_path.read_bytes())
        run.derived_inputs(spec, original)
        self.assertEqual(original['identity'], 'apple-sdk-r7-link-only-arm64-v1')
        self.assertNotEqual(original['identity'], run.IDENTITY)
        self.assertNotIn('xcodebuild', original['native'])
        self.assertEqual(hashlib.sha256((run.REPO/'ci/apple-sdk-link/run.py').read_bytes()).hexdigest(),
                         '4fbf3556540490bd8635e2135ddd4064b2f39cfc88fe15c08de02bfd46ee5dca')
        for label in ['tool', 'hash', 'origin', 'parent', 'bridge']:
            with self.subTest(label=label):
                changed = copy.deepcopy(spec)
                if label == 'tool':
                    changed['native']['xcodebuild'] = '/usr/bin/xcodebuild'
                elif label == 'hash':
                    changed['accepted_host_tool']['host_pins'][0]['sha256'] = '0'*64
                elif label == 'origin':
                    changed['accepted_host_tool']['run'] += 1
                elif label == 'parent':
                    changed['parent']['artifacts'][0]['sha256'] = '0'*64
                else:
                    changed['bridge_files'][0]['sha256'] = '0'*64
                with self.assertRaises(ValueError):
                    run.derived_inputs(changed, original)
        calls = []
        bad = copy.deepcopy(spec); bad['native']['xcodebuild'] = '/usr/bin/xcodebuild'
        with self.assertRaises(ValueError):
            run.query_real_tool(bad, Path('/unused'), {}, lambda *a, **k: calls.append(a), None)
        self.assertEqual(calls, [])


if __name__ == '__main__':
    unittest.main(verbosity=2)
