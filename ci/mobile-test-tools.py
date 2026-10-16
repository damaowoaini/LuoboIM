#!/usr/bin/env python3
"""Prepared offline CI-tool checks, separate from the 548/73/621 product tests."""
import importlib.util
import json
from pathlib import Path
import unittest

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


if __name__ == '__main__':
    unittest.main()
