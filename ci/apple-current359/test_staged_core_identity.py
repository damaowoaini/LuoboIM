"""Intentional framework replacement has its own exact provenance, not old pins."""
import ast
import copy
import hashlib
import json
from pathlib import Path
import plistlib
import tempfile
import unittest

import run as driver


class StagedCoreIdentity(unittest.TestCase):
    def fixture(self, work):
        sdk = work/'source/packages/flutter_openim_sdk'
        driver.mkdir(sdk); driver.mkdir(work/'evidence')
        public = {'path': 'ios/Plugin.swift', 'bytes': 18,
                  'sha256': hashlib.sha256(b'public plugin text').hexdigest()}
        driver.mkdir(sdk/'ios'); (sdk/public['path']).write_bytes(b'public plugin text')
        pubspec = b'name: flutter_openim_sdk\n'
        (sdk/'pubspec.yaml').write_bytes(pubspec)
        declaration = next(d for d in json.loads((driver.REPO/'ci/apple-current354/plugins.json').read_text())['declarations']
                           if d['name'] == 'flutter_openim_sdk')
        historical = [copy.deepcopy(r) for r in declaration['native_files']
                      if r['path'].startswith('ios/Framework/OpenIMCore.xcframework/')]
        plugins = {'declarations': [{'name': 'flutter_openim_sdk', 'native_files': historical + [public],
                    'pubspec': {'path': 'pubspec.yaml', 'bytes': len(pubspec), 'sha256': hashlib.sha256(pubspec).hexdigest()}}]}
        names = [r['path'] for r in json.loads((driver.HERE/'inputs.json').read_text())['core17']]
        original = work/'products/.qa/apple-cloud-1/OpenIMCore.xcframework'
        rows = []
        for name in names:
            body = (plistlib.dumps({'MinimumOSVersion': '100.0', 'CFBundleExecutable': 'OpenIMCore'})
                    if name.endswith('/Info.plist') else ('synthetic accepted original '+name).encode())
            path = original/name; driver.mkdir(path.parent); path.write_bytes(body)
            rows.append({'path': name, 'bytes': len(body), 'sha256': hashlib.sha256(body).hexdigest()})
        target = sdk/'ios/Framework/OpenIMCore.xcframework'
        driver.packaging.stage_core(original, target, rows, driver.put)
        return plugins, {'flutter_openim_sdk': sdk}, {'core17': rows}, target

    def test_both_source_checks_bind_current_derived_core_and_keep_other_pins(self):
        # The two real producer call sites must use this shared check exactly once.
        tree = ast.parse(Path(driver.__file__).read_text())
        for name, phase in [('packages', 'packages'), ('resolve', 'resolve')]:
            function = next(n for n in tree.body if isinstance(n, ast.FunctionDef) and n.name == name)
            calls = [n for n in ast.walk(function) if isinstance(n, ast.Call)
                     and isinstance(n.func, ast.Name) and n.func.id == 'check_plugin_sources']
            self.assertEqual(len(calls), 1)
            self.assertEqual(ast.literal_eval(calls[0].args[-1]), phase)
        for phase in ['packages', 'resolve']:
            for case in ['valid', 'foreign-root', 'missing-current-file', 'current-bytes-drift', 'other-native-drift']:
                with self.subTest(phase=phase, case=case), tempfile.TemporaryDirectory() as temp:
                    work = Path(temp).resolve()
                    plugins, roots, spec, target = self.fixture(work)
                    if case == 'foreign-root': roots['flutter_openim_sdk'] = work/'foreign-sdk'
                    elif case == 'missing-current-file': (target/'Info.plist').unlink()
                    elif case == 'current-bytes-drift': (target/'Info.plist').write_bytes(b'foreign current body')
                    elif case == 'other-native-drift': (roots['flutter_openim_sdk']/'ios/Plugin.swift').write_bytes(b'foreign plugin body')
                    if case != 'valid':
                        with self.assertRaises((ValueError, OSError)):
                            driver.check_plugin_sources(work, plugins, roots, spec, phase)
                        self.assertFalse((work/'evidence'/('current359-plugin-core17-'+phase+'.json')).exists())
                        continue
                    driver.check_plugin_sources(work, plugins, roots, spec, phase)
                    proof = json.loads((work/'evidence'/('current359-plugin-core17-'+phase+'.json')).read_text())
                    rows = proof['current17_from_accepted_originals_and_exact_two_plist_derivations']
                    self.assertEqual(len(rows), 17)
                    self.assertEqual(sum(row['wrapper_only_changed'] for row in rows), 2)
                    self.assertTrue(proof['historical_framework17_pins_unchanged'])
                    self.assertTrue(proof['all_other_plugin_native_pubspec_pins_checked'])
                    for row in rows:
                        body = (target/row['path']).read_bytes()
                        self.assertEqual(row['bytes'], len(body))
                        self.assertEqual(row['sha256'], hashlib.sha256(body).hexdigest())
                        if row['wrapper_only_changed']:
                            self.assertEqual(plistlib.loads(body)['MinimumOSVersion'], '15.0')


if __name__ == '__main__':
    unittest.main(verbosity=2)
