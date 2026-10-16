#!/usr/bin/env python3
"""Fourteen pure controller gates; no Go, Docker, DB or source1118 materialization."""
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

PATH = Path(__file__).with_name('run.py')
SPEC = importlib.util.spec_from_file_location('ordinary_controller_unit_subject', PATH)
SUBJECT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SUBJECT)


class ControllerTests(unittest.TestCase):
    def test_canonical_paths_reject_traversal_and_aliases(self):
        self.assertEqual(SUBJECT.name('server/internal/example.go'), 'server/internal/example.go')
        for path in ('/server/a', '../server/a', 'server//a', 'server/./a', 'server/../a', 'server\\a'):
            with self.subTest(path=path), self.assertRaises(ValueError):
                SUBJECT.name(path)

    def overlay_fixture(self, root):
        target = 'server/internal/example.go'
        relative = 'ci/candidates/group-ordinary/source/' + target
        path = root / relative
        path.parent.mkdir(parents=True)
        body = b'package synthetic\n'
        path.write_bytes(body)
        path.chmod(0o644)
        return {'overlay':[{'path':relative, 'target':target, 'mode':'0o644',
                           'bytes':len(body), 'sha256':hashlib.sha256(body).hexdigest(),
                           'beforeSHA256':None}]}, target, body

    def test_actual_tiny_overlay_preserves_body_and_checks_parent(self):
        previous = SUBJECT.REPO
        try:
            with tempfile.TemporaryDirectory() as temporary:
                SUBJECT.REPO = Path(temporary).resolve()
                contract, target, body = self.overlay_fixture(SUBJECT.REPO)
                self.assertEqual(SUBJECT.apply_overlay(contract, {}), {target:body})
                with self.assertRaises(ValueError):
                    SUBJECT.apply_overlay(contract, {target:b'foreign parent'})
        finally:
            SUBJECT.REPO = previous

    def test_overlay_extra_mode_or_body_change_cannot_pass(self):
        previous = SUBJECT.REPO
        try:
            with tempfile.TemporaryDirectory() as temporary:
                SUBJECT.REPO = Path(temporary).resolve()
                contract, target, body = self.overlay_fixture(SUBJECT.REPO)
                path = SUBJECT.REPO / contract['overlay'][0]['path']
                path.chmod(0o600)
                with self.assertRaises(ValueError):
                    SUBJECT.apply_overlay(contract, {})
                path.chmod(0o644)
                path.write_bytes(b'package changed\n')
                with self.assertRaises(ValueError):
                    SUBJECT.apply_overlay(contract, {})
                path.write_bytes(body)
                (path.parent / 'extra.go').write_bytes(body)
                with self.assertRaises(ValueError):
                    SUBJECT.apply_overlay(contract, {})
        finally:
            SUBJECT.REPO = previous

    def test_accepted_parent_tests_cannot_be_changed_or_removed(self):
        before = {'server/a_test.go':b'package a\n', 'server/a.go':b'package a\n'}
        after = dict(before)
        after['server/new.go'] = b'package a\n'
        self.assertEqual(len(SUBJECT.exact_delta(before, after)), 1)
        after['server/a_test.go'] = b'package changed\n'
        with self.assertRaises(ValueError):
            SUBJECT.exact_delta(before, after)
        after = dict(before)
        del after['server/a.go']
        with self.assertRaises(ValueError):
            SUBJECT.exact_delta(before, after)

    def test_fcm_whole_inventory_missing_skip_or_footer_cannot_pass(self):
        contract = json.loads(Path(__file__).with_name('inputs.json').read_text())['packages']['internal/push/offlinepush/fcm']
        package = SUBJECT.PREFIX + 'internal/push/offlinepush/fcm'
        events = []
        for test in contract['top_tests']:
            events.append({'Package':package, 'Action':'run', 'Test':test})
            # Exact declared leaf labels are synthetic parser events only.
            for leaf in contract['required_leaves']:
                if leaf.startswith(test + '/'):
                    events.extend([{'Package':package, 'Action':'run', 'Test':leaf},
                                   {'Package':package, 'Action':'pass', 'Test':leaf}])
            events.append({'Package':package, 'Action':'pass', 'Test':test})
        events.append({'Package':package, 'Action':'pass'})
        self.assertTrue(SUBJECT.audit(events, 0, 'internal/push/offlinepush/fcm', contract)['accepted'])
        self.assertFalse(SUBJECT.audit(events[:-1], 0, 'internal/push/offlinepush/fcm', contract)['accepted'])
        changed = [dict(event) for event in events]
        changed[-2]['Action'] = 'skip'
        self.assertFalse(SUBJECT.audit(changed, 0, 'internal/push/offlinepush/fcm', contract)['accepted'])
        changed = [e for e in events if e.get('Test') != contract['top_tests'][-1]]
        self.assertFalse(SUBJECT.audit(changed, 0, 'internal/push/offlinepush/fcm', contract)['accepted'])

    def test_monitor_or_partial_failure_never_becomes_child_success(self):
        SUBJECT.require_resource({'status':'passed', 'exit_code':0}, 0)
        SUBJECT.require_resource({'status':'failed', 'exit_code':1}, 1)
        for resource, code in (
            ({'status':'monitor_error', 'exit_code':0}, 0),
            ({'status':'timed_out', 'exit_code':0}, 0),
            ({'status':'passed', 'exit_code':None}, 0),
            ({'status':'passed', 'exit_code':1}, 1),
            ({'status':'failed', 'exit_code':0}, 0),
            ({'status':'passed', 'exit_code':0}, 1),
        ):
            with self.subTest(status=resource['status']), self.assertRaises(ValueError):
                SUBJECT.require_resource(resource, code)



    def test_compile_collects_all_23_healthy_child_results_but_not_guard_errors(self):
        packages = ['synthetic/' + str(index) for index in range(23)]
        calls = []
        def command(package, args, phase, timeout):
            calls.append(package)
            self.assertEqual(phase, 'compile')
            self.assertEqual(timeout, 900)
            self.assertEqual(args[-1], './' + package)
            self.assertEqual(args[:-1], ['go', 'test', '-mod=readonly', '-p=1', '-race', '-json', '-run', '^$'])
            return None, 1 if package == packages[8] else 0
        codes = SUBJECT.compile_packages(packages, command)
        self.assertEqual(calls, packages)
        self.assertEqual(codes[packages[8]], 1)
        self.assertEqual(sum(code == 0 for code in codes.values()), 22)
        calls.clear()
        def monitor_error(package, args, phase, timeout):
            calls.append(package)
            if package == packages[3]:
                raise ValueError('synthetic monitor incomplete')
            return None, 0
        with self.assertRaises(ValueError):
            SUBJECT.compile_packages(packages, monitor_error)
        self.assertEqual(calls, packages[:4])



    def test_alias_and_operator_contracts_reject_missing_or_skipped_new_cases(self):
        contract = json.loads(Path(__file__).with_name('inputs.json').read_text())
        for short in ('internal/push/offlinepush/aliasvendor', 'cmd/tools/ordinary-alias-receipts'):
            spec = contract['packages'][short]
            package = SUBJECT.PREFIX + short
            events = []
            for test in spec['top_tests']:
                events.append({'Package':package, 'Action':'run', 'Test':test})
                for leaf in spec['required_leaves']:
                    if leaf.startswith(test + '/'):
                        events.extend([{'Package':package, 'Action':'run', 'Test':leaf},
                                       {'Package':package, 'Action':'pass', 'Test':leaf}])
                events.append({'Package':package, 'Action':'pass', 'Test':test})
            events.append({'Package':package, 'Action':'pass'})
            self.assertTrue(SUBJECT.audit(events, 0, short, spec)['accepted'])
            skipped = [dict(item) for item in events]
            skipped[-2]['Action'] = 'skip'
            self.assertFalse(SUBJECT.audit(skipped, 0, short, spec)['accepted'])
            missing = [item for item in events if item.get('Test') != spec['top_tests'][-1]]
            self.assertFalse(SUBJECT.audit(missing, 0, short, spec)['accepted'])

    def test_physical_before_requires_exact_assertions_and_all_positive_controls(self):
        package = SUBJECT.PREFIX + 'internal/messagedelivery'
        denied = 'TestKafkaPhysicalPermitMongoClearBeforeFinalAdmissionNeverGrantsPOST'
        normal = 'TestKafkaPhysicalPermitMongoKnownCommitIsOneStartedAttemptEvenIfClearCommitsLater'
        events = []
        for top, action in ((denied, 'fail'), (normal, 'pass')):
            events.append({'Package':package, 'Action':'run', 'Test':top})
            for mode in ('alias', 'generic', 'fcm'):
                events.append({'Package':package, 'Action':'run', 'Test':top + '/' + mode})
                if action == 'fail':
                    events.append({'Package':package, 'Action':'output', 'Test':top + '/' + mode,
                        'Output':'    kafka_physical_permit_integration_test.go:125: Clear committed before admission but old plaintext acquired a physical POST permit\n'})
                events.append({'Package':package, 'Action':action, 'Test':top + '/' + mode})
            events.append({'Package':package, 'Action':action, 'Test':top})
        events.append({'Package':package, 'Action':'fail'})
        self.assertTrue(SUBJECT.audit_physical_before(events, 1)['accepted'])
        self.assertFalse(SUBJECT.audit_physical_before(events, 0)['accepted'])
        self.assertFalse(SUBJECT.audit_physical_before(events[:-1], 1)['accepted'])
        skipped = [dict(item) for item in events]
        skipped[-2]['Action'] = 'skip'
        self.assertFalse(SUBJECT.audit_physical_before(skipped, 1)['accepted'])
        poisoned = events[:-1] + [{'Package':package, 'Action':'output', 'Output':'panic: fixture failure'}] + events[-1:]
        self.assertFalse(SUBJECT.audit_physical_before(poisoned, 1)['accepted'])


    def physical_before_raw_fixture(self):
        package = SUBJECT.PREFIX + 'internal/messagedelivery'
        denied = 'TestKafkaPhysicalPermitMongoClearBeforeFinalAdmissionNeverGrantsPOST'
        normal = 'TestKafkaPhysicalPermitMongoKnownCommitIsOneStartedAttemptEvenIfClearCommitsLater'
        assertion = 'Clear committed before admission but old plaintext acquired a physical POST permit'
        events = [{'Package':package, 'Action':'start'}]
        for top, action in ((denied, 'fail'), (normal, 'pass')):
            events.extend([{'Package':package, 'Action':'run', 'Test':top},
                           {'Package':package, 'Action':'output', 'Test':top, 'Output':'=== RUN   ' + top + '\n'}])
            for mode in ('alias', 'generic', 'fcm'):
                leaf = top + '/' + mode
                events.extend([{'Package':package, 'Action':'run', 'Test':leaf},
                               {'Package':package, 'Action':'output', 'Test':leaf, 'Output':'=== RUN   ' + leaf + '\n'}])
                if action == 'fail':
                    events.append({'Package':package, 'Action':'output', 'Test':leaf,
                        'Output':'    kafka_physical_permit_integration_test.go:125: ' + assertion + '\n'})
                events.extend([{'Package':package, 'Action':'output', 'Test':leaf,
                                'Output':('--- FAIL: ' if action == 'fail' else '--- PASS: ') + leaf + ' (0.01s)\n'},
                               {'Package':package, 'Action':action, 'Test':leaf}])
            events.append({'Package':package, 'Action':action, 'Test':top})
        events.extend([{'Package':package, 'Action':'output', 'Output':'FAIL\n'},
                       {'Package':package, 'Action':'fail'}])
        return events, denied, assertion

    def test_same_failed_names_with_wrong_assertion_or_fixture_only_reject(self):
        events, denied, assertion = self.physical_before_raw_fixture()
        correct = SUBJECT.audit_physical_before(events, 1)
        self.assertTrue(correct['accepted'])
        expected_failures = correct['failed_tests']
        for message in ('unexpected permission assertion', 'owned nonce database claim failed'):
            changed = [dict(event) for event in events]
            for event in changed:
                if event.get('Action') == 'output' and assertion in event.get('Output', ''):
                    event['Output'] = '    mongo_fixture_test.go:91: ' + message + '\n'
            actual = SUBJECT.audit_physical_before(changed, 1)
            self.assertEqual(actual['failed_tests'], expected_failures)
            self.assertTrue(actual['complete'])
            self.assertFalse(actual['poison_output'])
            self.assertFalse(actual['accepted'])

    def test_physical_assertions_must_be_unique_exact_and_bound_to_each_leaf(self):
        events, denied, assertion = self.physical_before_raw_fixture()
        indexes = [i for i, event in enumerate(events) if assertion in event.get('Output', '')]
        self.assertEqual(len(indexes), 3)
        for fault in ('missing', 'duplicate', 'foreign-test-id', 'extra-fixture', 'parent-fixture', 'substring-only', 'same-text-wrong-file'):
            changed = [dict(event) for event in events]
            index = indexes[-1]
            if fault == 'missing':
                del changed[index]
            elif fault == 'duplicate':
                changed.insert(index, dict(changed[index]))
            elif fault == 'foreign-test-id':
                changed[index]['Test'] = denied + '/alias'
            elif fault == 'extra-fixture':
                extra = dict(changed[index]);extra['Output'] = '    mongo_fixture_test.go:91: claim failed\n'
                changed.insert(index, extra)
            elif fault == 'parent-fixture':
                extra = dict(changed[index]);extra['Test'] = denied;extra['Output'] = '    mongo_fixture_test.go:91: parent cleanup failed\n'
                changed.insert(index, extra)
            elif fault == 'substring-only':
                changed[index]['Output'] = changed[index]['Output'].rstrip() + ' unrelated failure\n'
            else:
                changed[index]['Output'] = '    mongo_fixture_test.go:91: ' + assertion + '\n'
            actual = SUBJECT.audit_physical_before(changed, 1)
            self.assertEqual(actual['failed_tests'], SUBJECT.audit_physical_before(events, 1)['failed_tests'])
            self.assertFalse(actual['accepted'], fault)


    def test_booking_whole_inventory_requires_all_old_eight_and_new_mongo(self):
        inputs = json.loads(Path(__file__).with_name('inputs.json').read_text())
        short = 'internal/meetingbooking'
        spec = inputs['packages'][short]
        mandatory = inputs['mandatory_booking_mongo']
        self.assertEqual(len(spec['top_tests']), 31)
        self.assertEqual(len(mandatory), 9)
        self.assertEqual(spec['required_leaves'], mandatory)
        package = SUBJECT.PREFIX + short
        events = []
        for test in spec['top_tests']:
            events.extend([{'Package':package, 'Action':'run', 'Test':test},
                           {'Package':package, 'Action':'pass', 'Test':test}])
        events.append({'Package':package, 'Action':'pass'})
        self.assertTrue(SUBJECT.audit(events, 0, short, spec)['accepted'])
        for test in mandatory:
            with self.subTest(mongo=test):
                skipped = [dict(event) for event in events]
                for event in skipped:
                    if event.get('Test') == test and event['Action'] == 'pass':
                        event['Action'] = 'skip'
                self.assertFalse(SUBJECT.audit(skipped, 0, short, spec)['accepted'])
                missing = [event for event in events if event.get('Test') != test]
                self.assertFalse(SUBJECT.audit(missing, 0, short, spec)['accepted'])

    def test_compile_collects_all_28_packages_including_booking_and_callback_without_tests(self):
        inputs = json.loads(Path(__file__).with_name('inputs.json').read_text())
        packages = list(inputs['packages'])
        self.assertEqual(len(packages), 28)
        self.assertIn('internal/meetingbooking', packages)
        calls = []
        def command(package, args, phase, timeout):
            calls.append(package)
            self.assertEqual(phase, 'compile')
            self.assertEqual(timeout, 900)
            self.assertEqual(args[:-1], ['go', 'test', '-mod=readonly', '-p=1', '-race', '-json', '-run', '^$'])
            self.assertEqual(args[-1], './' + package)
            return None, 1 if package == 'internal/meetingbooking' else 0
        codes = SUBJECT.compile_packages(packages, command)
        self.assertEqual(calls, packages)
        self.assertEqual(codes['internal/meetingbooking'], 1)
        self.assertEqual(sum(code == 0 for code in codes.values()), 27)


    def test_callback_mongo_worker_and_lineage_cannot_be_missing_or_skipped(self):
        inputs = json.loads(Path(__file__).with_name('inputs.json').read_text())
        self.assertEqual(len(inputs['mandatory_callback_mongo']), 11)
        required = {'internal/messagedelivery':inputs['mandatory_callback_mongo'],
                    'internal/msgtransfer':[inputs['mandatory_callback_worker_http']],
                    'cmd/tools/ordinary-callback-receipts':[inputs['mandatory_callback_cli_lineage']]}
        for short, names in required.items():
            spec = inputs['packages'][short]
            package = SUBJECT.PREFIX + short
            events = []
            for test in spec['top_tests']:
                events.append({'Package':package, 'Action':'run', 'Test':test})
                for leaf in spec['required_leaves']:
                    if leaf.startswith(test + '/'):
                        events.extend([{'Package':package, 'Action':'run', 'Test':leaf},
                                       {'Package':package, 'Action':'pass', 'Test':leaf}])
                state = 'skip' if test in spec['allowed_legacy_skips'] else 'pass'
                events.append({'Package':package, 'Action':state, 'Test':test})
            events.append({'Package':package, 'Action':'pass'})
            self.assertTrue(SUBJECT.audit(events, 0, short, spec)['accepted'])
            for name in names:
                with self.subTest(case=name):
                    changed = [dict(e) for e in events]
                    for e in changed:
                        if e.get('Test') == name and e['Action'] == 'pass':
                            e['Action'] = 'skip'
                    self.assertFalse(SUBJECT.audit(changed, 0, short, spec)['accepted'])
                    missing = [e for e in events if e.get('Test', '').split('/', 1)[0] != name]
                    self.assertFalse(SUBJECT.audit(missing, 0, short, spec)['accepted'])

if __name__ == '__main__':
    unittest.main()
