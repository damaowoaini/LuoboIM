#!/usr/bin/env python3
"""New result-audit controls only; no Go or fixture invocation."""
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('runtime_join_audit', Path(__file__).with_name('run.py'))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


class ResultAuditTests(unittest.TestCase):
    def setUp(self):
        self.targets = {'internal/example': ['TestNew']}
        self.events = [self.event('start'), self.event('run', 'TestNew'), self.event('run', 'TestNew/same-cut'), self.event('pass', 'TestNew/same-cut'), self.event('pass', 'TestNew'), self.event('pass')]

    def event(self, action, test=None):
        value = {'Action': action, 'Package': runner.PREFIX + 'internal/example'}
        if test:
            value['Test'] = test
        return value

    def test_actual_named_descendants_and_package_footer_are_required(self):
        result = runner.audit(self.events, 0, self.targets)
        self.assertEqual((result['topPASS'], result['subPASS'], result['packages']), (1, 1, 1))
        for events, code in [(self.events[:-1], 0), (self.events, 1), (self.events[:3] + self.events[4:], 0)]:
            with self.subTest(events=events, code=code), self.assertRaises(ValueError):
                runner.audit(events, code, self.targets)

    def test_skips_failures_and_race_output_never_become_pass(self):
        for event in [self.event('skip', 'TestNew/same-cut'), self.event('fail', 'TestNew'), dict(self.event('output'), Output='WARNING: DATA RACE')]:
            with self.subTest(event=event), self.assertRaises(ValueError):
                runner.audit(self.events[:2] + [event] + self.events[2:], 0, self.targets)

    def test_extra_top_foreign_package_and_post_terminal_events_refuse(self):
        for event in [self.event('run', 'TestOther'), dict(self.event('output'), Package=runner.PREFIX + 'internal/foreign')]:
            with self.subTest(event=event), self.assertRaises(ValueError):
                runner.audit(self.events[:1] + [event] + self.events[1:], 0, self.targets)
        with self.assertRaises(ValueError):
            runner.audit(self.events + [self.event('output')], 0, self.targets)

    def test_duplicate_completion_and_incomplete_subtest_refuse(self):
        for events in [self.events[:-1] + [self.event('pass', 'TestNew')] + self.events[-1:], self.events[:3] + self.events[4:], self.events[:1] + self.events[2:]]:
            with self.subTest(events=events), self.assertRaises(ValueError):
                runner.audit(events, 0, self.targets)


if __name__ == '__main__':
    unittest.main()
