"""Finite controller contracts only; no Docker, SQL, Go or network dispatch."""
import copy
import importlib.util
import json
from pathlib import Path
import sys
import unittest
import tempfile
from unittest.mock import patch

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
spec = importlib.util.spec_from_file_location('admin_session_targeted', HERE / 'run.py')
controller = importlib.util.module_from_spec(spec)
spec.loader.exec_module(controller)

class TargetedContracts(unittest.TestCase):
    def test_actual_go_event_acceptance_requires_all_four_runs_passes_package_footer_no_skip(self):
        rows = [{'Action': 'start', 'Package': controller.PACKAGE}]
        for test in controller.TESTS:
            rows += [{'Action': 'run', 'Package': controller.PACKAGE, 'Test': test},
                     {'Action': 'pass', 'Package': controller.PACKAGE, 'Test': test}]
        rows += [{'Action': 'output', 'Package': controller.PACKAGE, 'Output': 'ok\t' + controller.PACKAGE + '\t0.035s\n'},
                 {'Action': 'pass', 'Package': controller.PACKAGE}]
        encode = lambda v: ('\n'.join(json.dumps(row) for row in v) + '\n').encode()
        self.assertEqual(len(controller.go_events(encode(rows), 0)['actualTopPASS']), 4)
        bad = []
        missing = copy.deepcopy(rows); del missing[1:3]; bad.append(missing)
        skipped = copy.deepcopy(rows); skipped[2]['Action'] = 'skip'; bad.append(skipped)
        no_footer = copy.deepcopy(rows); del no_footer[-2]; bad.append(no_footer)
        unknown = copy.deepcopy(rows); unknown[1]['Test'] += '/unexpected'; bad.append(unknown)
        duplicate = copy.deepcopy(rows); duplicate.insert(1, duplicate[1]); bad.append(duplicate)
        bad.append(rows[:-1])
        for value in bad:
            with self.assertRaises(ValueError): controller.go_events(encode(value), 0)
        with self.assertRaises(ValueError): controller.go_events(encode(rows), 1)
        with self.assertRaises(ValueError): controller.go_events(encode(rows)[:-1], 0)

    def test_container_cleanup_identity_cannot_adopt_foreign_image_owner_id_or_network(self):
        expected = {'id': 'a' * 64, 'name': 'finite-name', 'config': 'sha256:' + 'b' * 64,
                    'user': '1001:1001', 'owner': '1:1', 'nonce': 'c' * 32, 'role': 'mysql'}
        row = {'Id': expected['id'], 'Name': '/' + expected['name'], 'Image': expected['config'],
               'Config': {'User': expected['user'], 'Labels': {'luobo.admin.owner': expected['owner'],
                   'luobo.admin.nonce': expected['nonce'], 'luobo.admin.role': 'mysql'}},
               'HostConfig': {'NetworkMode': 'bridge'}}
        self.assertIs(controller.fixture.container_identity(row, expected), row)
        for case in ['Id', 'Name', 'Image', 'User', 'owner', 'nonce', 'role', 'network']:
            value = copy.deepcopy(row)
            if case in ['Id', 'Name', 'Image']: value[case] = 'foreign'
            elif case == 'User': value['Config']['User'] = '0:0'
            elif case == 'network': value['HostConfig']['NetworkMode'] = 'host'
            else: value['Config']['Labels']['luobo.admin.' + case] = 'foreign'
            with self.subTest(case=case), self.assertRaises(ValueError):
                controller.fixture.container_identity(value, expected)

        raw = (HERE / 'inputs.json').read_bytes()
        pin = controller.sha(raw)
        with patch.object(controller, 'tree', side_effect=ValueError('product source changed')):
            self.assertEqual(controller.verify(pin, source_required=False)['cohort'], controller.COHORT)
            with self.assertRaises(ValueError): controller.verify(pin)
        with self.assertRaises(ValueError): controller.verify('0' * 64, source_required=False)
        original = controller.stable
        def changed(path, *args, **kwargs):
            value = original(path, *args, **kwargs)
            if Path(path) == HERE / 'fixture.py': value['sha256'] = '0' * 64
            return value
        with patch.object(controller, 'stable', side_effect=changed), self.assertRaises(ValueError):
            controller.verify(pin, source_required=False)


class NewDownloadObservationControls(unittest.TestCase):
    def test_original_download_plan_and_known_source_difference_projection_remain_strict_and_private(self):
        self.assertEqual(controller.module_download_plan('/fixed/go'), ['/fixed/go', 'mod', 'download', '-json'])
        with tempfile.TemporaryDirectory(prefix='private-only-source-') as directory:
            root = Path(directory).resolve()
            (root / 'pkg').mkdir()
            module = root / 'go.mod'; module.write_text('module public.example\n')
            source = root / 'pkg/public.go'; source.write_text('package public\n')
            expected = [{'path': 'go.mod', **controller.stable(module)},
                        {'path': 'pkg/public.go', **controller.stable(source)}]
            module.write_text('module public.example\n// PRIVATE-DO-NOT-EXPORT\n')
            (root / 'pkg/private-added.go').write_text('PRIVATE-NEW-CONTENT')
            projection = controller.source_difference(root, expected)
            self.assertEqual(projection['knownChangedCount'], 1)
            self.assertEqual(projection['knownChanges'][0]['path'], 'go.mod')
            self.assertEqual(projection['knownChanges'][0]['after'], controller.stable(module))
            self.assertEqual(projection['unknownAddedCategories']['regular-go'], 1)
            self.assertTrue(projection['driftStillRejectsRun'])
            encoded = json.dumps(projection)
            for private in ['PRIVATE-', 'private-added', directory]: self.assertNotIn(private, encoded)
            module.unlink()
            missing = controller.source_difference(root, expected)['knownChanges'][0]
            self.assertEqual(missing['kind'], 'missing'); self.assertIsNone(missing['after'])
            private = root / 'private'; private.mkdir()
            (private / 'module-download.resource.json').write_text(json.dumps(
                {'status': 'failed', 'exit_code': 7, 'command': ['PRIVATE-DSN']}))
            (private / 'go-test.resource.json').write_text('[]')
            (private / 'unknown-secret.resource.json').write_text('PRIVATE-UNKNOWN')
            statuses = controller.command_observations(private)
            self.assertEqual(statuses, {'module-download': {'exit': 7, 'guardStatus': 'failed'},
                                       'go-test': {'exit': None, 'guardStatus': 'unconfirmed'}})
            self.assertNotIn('PRIVATE-', json.dumps(statuses))

    def test_failed_go_observation_keeps_fixed_individual_facts_without_output_or_package_acceptance(self):
        rows = [{'Action': 'start', 'Package': controller.PACKAGE}]
        for name in controller.TESTS[:2]:
            rows += [{'Action': 'run', 'Package': controller.PACKAGE, 'Test': name},
                     {'Action': 'pass', 'Package': controller.PACKAGE, 'Test': name}]
        rows += [{'Action': 'run', 'Package': controller.PACKAGE, 'Test': controller.TESTS[2]},
                 {'Action': 'output', 'Package': controller.PACKAGE, 'Test': controller.TESTS[2],
                  'Output': '    service_test.go:42: PRIVATE-DSN\n/private/place/service_test.go:44: SECRET\nservice_test.go:999999: PRIVATE-OUTSIDE-SOURCE\nunknown_test.go:1: UNKNOWN'},
                 {'Action': 'fail', 'Package': controller.PACKAGE, 'Test': controller.TESTS[2]},
                 {'Action': 'fail', 'Package': controller.PACKAGE}]
        encode = lambda values: ('\n'.join(json.dumps(row) for row in values) + '\n').encode()
        limits = {'service_test.go': 300, 'admin_disable_revocation_test.go': 200}
        value = controller.failed_go_observation(encode(rows), limits, 1)
        self.assertEqual(value['individualPASSObserved'], controller.TESTS[:2])
        self.assertEqual(value['knownFrames'], [{'test': controller.TESTS[2], 'basename': 'service_test.go', 'line': 42}])
        self.assertFalse(value['wholeInvocationAccepted'])
        for private in ['PRIVATE-DSN', '/private/', 'SECRET', 'unknown_test', 'UNKNOWN']:
            self.assertNotIn(private, json.dumps(value))
        with self.assertRaises(ValueError): controller.go_events(encode(rows), 1)
        partial = controller.failed_go_observation(encode(rows) + b'{unfinished', limits, 1)
        self.assertFalse(partial['completeJSON']); self.assertFalse(partial['wholeInvocationAccepted'])
        build = [{'Action': 'build-output', 'ImportPath': controller.PACKAGE,
                  'Output': 'pkg/common/admindelegation/admin_disable_revocation_test.go:54: SECRET'},
                 {'Action': 'build-fail', 'ImportPath': controller.PACKAGE}]
        self.assertEqual(controller.failed_go_observation(encode(build), limits, 1)['knownFrames'],
            [{'test': None, 'basename': 'admin_disable_revocation_test.go', 'line': 54}])
        self.assertFalse(controller.failed_go_observation(encode(build), limits, 1)['wholeInvocationAccepted'])

if __name__ == '__main__': unittest.main()
