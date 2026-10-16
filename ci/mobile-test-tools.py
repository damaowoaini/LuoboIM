#!/usr/bin/env python3
"""Pure Python CI gate checks, separate from current287 target134/full822."""
import copy
import importlib.util
import json
from pathlib import Path
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location('mobile_check', Path(__file__).with_name('mobile-check.py'))
check = importlib.util.module_from_spec(spec)
spec.loader.exec_module(check)


def reporter(*, footer=True, skip=False, hidden_failure=False):
    events = [
        {'type': 'testStart', 'test': {'id': 0}},
        {'type': 'testDone', 'testID': 0, 'hidden': True, 'skipped': False,
         'result': 'error' if hidden_failure else 'success'},
        {'type': 'testStart', 'test': {'id': 1}},
        {'type': 'testDone', 'testID': 1, 'hidden': False, 'skipped': skip, 'result': 'success'},
    ]
    if footer:
        events.append({'type': 'done', 'success': True})
    return '\n'.join(map(json.dumps, events))


class ReporterTests(unittest.TestCase):
    def test_visible_count_excludes_hidden_loader(self):
        result = check.machine_summary(reporter(), 1)
        self.assertEqual((result['passed'], result['hidden']), (1, 1))

    def test_incomplete_runner_is_not_pass(self):
        with self.assertRaisesRegex(ValueError, 'incomplete'):
            check.machine_summary(reporter(footer=False), 1)

    def test_hidden_load_failure_is_not_pass(self):
        with self.assertRaisesRegex(ValueError, 'incomplete'):
            check.machine_summary(reporter(hidden_failure=True), 1)

    def test_skipped_case_is_not_pass(self):
        with self.assertRaisesRegex(ValueError, 'incomplete'):
            check.machine_summary(reporter(skip=True), 1)

    def test_wrong_expected_count_is_not_pass(self):
        with self.assertRaisesRegex(ValueError, 'unexpected'):
            check.machine_summary(reporter(), 621)

    def test_event_after_success_footer_is_not_pass(self):
        with self.assertRaisesRegex(ValueError, 'after terminal'):
            check.machine_summary(reporter() + '\n' + json.dumps({'type': 'done', 'success': True}), 1)


def reference_report():
    plan = check.source.descriptor()['reference_entry']
    return [
        {'type': 'testStart', 'test': {'id': 0, 'name': 'loading synthetic entry.dart'}},
        {'type': 'testDone', 'testID': 0, 'hidden': True, 'skipped': False, 'result': 'success'},
        {'type': 'testStart', 'test': {'id': 1, 'name': plan['failed_test_name']}},
        {'type': 'error', 'testID': 1, 'isFailure': True,
         'error': 'Expected: exactly one matching candidate\nActual: Found 0 widgets with tooltip "全局搜索"'},
        {'type': 'testDone', 'testID': 1, 'hidden': False, 'skipped': False, 'result': 'error'},
        {'type': 'testStart', 'test': {'id': 2, 'name': plan['passed_test_name']}},
        {'type': 'testDone', 'testID': 2, 'hidden': False, 'skipped': False, 'result': 'success'},
        {'type': 'done', 'success': False},
    ]


class ReferenceReporterTests(unittest.TestCase):
    def run_report(self, events, code=1):
        return check.reference_entry_summary('\n'.join(map(json.dumps, events)), code,
                                             check.source.descriptor()['reference_entry'])

    def test_reference_accepts_named_assertion_and_existing_filter_only(self):
        result = self.run_report(reference_report())
        self.assertEqual((result['passed'], result['assertion_failed'], result['hidden_successful_loads']), (1, 1, 1))

    def test_reference_rejects_duplicate_visible_positive_name(self):
        events = reference_report()
        name = check.source.descriptor()['reference_entry']['passed_test_name']
        events[-1:-1] = [
            {'type': 'testStart', 'test': {'id': 99, 'name': name}},
            {'type': 'testDone', 'testID': 99, 'hidden': False, 'skipped': False, 'result': 'success'},
        ]
        with self.assertRaisesRegex(ValueError, 'exactly two distinct'):
            self.run_report(events)

    def test_reference_rejects_hidden_load_failure(self):
        events = reference_report(); events[1]['result'] = 'error'
        with self.assertRaises(ValueError): self.run_report(events)

    def test_reference_rejects_timeout_even_with_other_assertion_words(self):
        events = reference_report(); events[3]['error'] += '\nTimeoutException timed out'
        with self.assertRaises(ValueError): self.run_report(events)

    def test_reference_rejects_panic(self):
        events = reference_report(); events[3]['error'] += '\npanic: native loader'
        with self.assertRaises(ValueError): self.run_report(events)

    def test_reference_rejects_arbitrary_assertion(self):
        events = reference_report(); events[3]['error'] = 'Expected: true\nActual: false'
        with self.assertRaises(ValueError): self.run_report(events)

    def test_reference_rejects_exit0(self):
        with self.assertRaises(ValueError): self.run_report(reference_report(), code=0)

    def test_reference_requires_footer(self):
        with self.assertRaises(ValueError): self.run_report(reference_report()[:-1])

    def test_reference_rejects_numeric_false_footer(self):
        events = reference_report(); events[-1]['success'] = 0
        with self.assertRaises(ValueError): self.run_report(events)

    def test_reference_rejects_duplicate_start(self):
        events = reference_report(); events.insert(3, copy.deepcopy(events[2]))
        with self.assertRaises(ValueError): self.run_report(events)

    def test_reference_rejects_event_after_footer(self):
        events = reference_report(); events.append({'type': 'testStart', 'test': {'id': 5, 'name': 'late'}})
        with self.assertRaises(ValueError): self.run_report(events)

    def test_reference_rejects_failed_positive(self):
        events = reference_report(); events[6]['result'] = 'error'
        with self.assertRaises(ValueError): self.run_report(events)

    def test_reference_rejects_skipped_case(self):
        events = reference_report(); events[6]['skipped'] = True
        with self.assertRaises(ValueError): self.run_report(events)


class DescriptorTests(unittest.TestCase):
    def rejects(self, change):
        plan = check.source.descriptor(); change(plan)
        with mock.patch.object(check.source, 'regular', return_value=json.dumps(plan).encode()):
            with self.assertRaises(ValueError): check.source.descriptor()

    def test_historical257_cannot_alias287(self):
        for historical in ('current257', 'current269', 'current282'):
            with self.subTest(historical=historical):
                self.rejects(lambda p: p.update({historical: {}}))

    def test_wrong_full_count_cannot_pass(self):
        self.rejects(lambda p: p['current287'].update(expected_full_tests=734))

    def test_reference_requires_all_four_before_files(self):
        self.rejects(lambda p: p['reference_entry']['replacement_files'].pop())

    def test_reference_is_not_enabled_by_default(self):
        self.rejects(lambda p: p['reference_entry'].update(default_enabled=True))



def named_report(names):
    events = []
    for identity, name in enumerate(names):
        events += [
            {'type': 'testStart', 'test': {'id': identity, 'name': name}},
            {'type': 'testDone', 'testID': identity, 'hidden': False, 'skipped': False, 'result': 'success'},
        ]
    return '\n'.join(map(json.dumps, events + [{'type': 'done', 'success': True}]))


class RequiredNamesTests(unittest.TestCase):
    def test_exact_names_preserve_parent_multiplicity_and_new_cases(self):
        result = check.required_name_summary(named_report(['old', 'old', 'new']), 3,
                                             [{'name': 'old', 'count': 2}, {'name': 'new', 'count': 1}])
        self.assertTrue(result['exact_successful_name_multiset'])

    def test_same_count_different_name_cannot_replace_old_case(self):
        with self.assertRaisesRegex(ValueError, 'multiset differs'):
            check.required_name_summary(named_report(['new', 'replacement']), 2,
                                        [{'name': 'old', 'count': 1}, {'name': 'new', 'count': 1}])

    def test_duplicate_parent_names_require_full_multiplicity(self):
        with self.assertRaisesRegex(ValueError, 'multiset differs'):
            check.required_name_summary(named_report(['old', 'replacement']), 2,
                                        [{'name': 'old', 'count': 2}])

    def test_names_never_override_incomplete_footer_or_hidden_failure(self):
        for text in [named_report(['old']).rsplit('\n', 1)[0], reporter(hidden_failure=True)]:
            with self.subTest(text=text):
                with self.assertRaises(ValueError):
                    check.required_name_summary(text, 1, [{'name': 'old', 'count': 1}])

    def test_required_count_boolean_is_rejected(self):
        with self.assertRaisesRegex(ValueError, 'invalid required'):
            check.required_name_summary(named_report(['old']), 1, [{'name': 'old', 'count': True}])


if __name__ == '__main__':
    unittest.main()
