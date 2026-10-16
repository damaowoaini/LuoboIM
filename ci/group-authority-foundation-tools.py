#!/usr/bin/env python3
"""Regression of the evidence gate; no Go, database or product execution."""
import copy
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('foundation_ci', Path(__file__).with_name('group-authority-foundation-ci.py'))
ci = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ci)

DENIED = 'TestGroupAuthorityBarrierRevocationACKPreventsDelayedWrite'
POSITIVE = 'TestGroupAuthorityBarrierPreservesAuthorizedWrite'
MESSAGE = 'revocation ACK did not fence delayed helper: revokedRole=20 staleWrite=true permissionDenied=false'
CONTRACT = {'required_top_tests': [DENIED, POSITIVE], 'expected_sub_tests': 2,
            'before_top_tests': [DENIED, POSITIVE], 'expected_before_failures': [DENIED],
            'expected_before_message': MESSAGE,
            'before_positive_controls': [POSITIVE, POSITIVE + '/admin', POSITIVE + '/owner']}


def event(action, name=None, **extra):
    result = {'Package': ci.PACKAGE, 'Action': action, **extra}
    if name is not None:
        result['Test'] = name
    return result


def suite(before=False):
    result = []
    for name in [DENIED, POSITIVE, POSITIVE + '/admin', POSITIVE + '/owner']:
        result.append(event('run', name))
        if before and name == DENIED:
            result.append(event('output', name, Output=MESSAGE + '\n'))
            result.append(event('fail', name))
        else:
            result.append(event('pass', name))
    result.append(event('fail' if before else 'pass'))
    return result


class EvidenceGate(unittest.TestCase):
    def test_after_complete_pass(self):
        self.assertEqual(ci.audit(suite(), 0, CONTRACT)['counts'], {'top_pass': 2, 'sub_pass': 2})

    def test_before_exact_assertion_and_positive_controls(self):
        self.assertEqual(ci.audit(suite(True), 1, CONTRACT, before=True)['counts'], {'top_fail': 1, 'top_pass': 1, 'sub_pass': 2})

    def test_rejects_incomplete_or_false_before_and_after(self):
        cases = []
        rows = suite(); rows[1]['Action'] = 'skip'; cases.append(('skip', rows, 0, False))
        rows = suite(); rows.pop(1); cases.append(('missing_done', rows, 0, False))
        rows = suite(); rows.insert(2, copy.deepcopy(rows[1])); cases.append(('duplicate_done', rows, 0, False))
        rows = suite(); rows.insert(1, copy.deepcopy(rows[0])); cases.append(('duplicate_run', rows, 0, False))
        rows = suite(); rows[0]['Package'] = 'foreign'; cases.append(('foreign', rows, 0, False))
        rows = suite(); rows.append(event('output', Output='late')); cases.append(('late_event', rows, 0, False))
        rows = suite(); rows.insert(1, event('output', DENIED, Output='WARNING: DATA RACE')); cases.append(('race', rows, 0, False))
        rows = suite(); rows.insert(1, event('output', DENIED, Output='panic: fixture')); cases.append(('panic', rows, 0, False))
        rows = suite(); rows.insert(1, event('output', DENIED, Output='test timed out')); cases.append(('timeout', rows, 0, False))
        rows = suite(); rows = [x for x in rows if x.get('Test') != DENIED]; cases.append(('missing_top', rows, 0, False))
        rows = suite(); rows = [x for x in rows if x.get('Test') != POSITIVE + '/owner']; cases.append(('missing_sub', rows, 0, False))
        rows = suite(); rows.pop(); cases.append(('missing_footer', rows, 0, False))
        cases.append(('wrong_after_exit', suite(), 1, False))
        cases.append(('before_succeeded', suite(), 0, True))
        rows = suite(True); rows[1]['Output'] = 'different failure'; cases.append(('wrong_assertion', rows, 1, True))
        rows = suite(True); rows[1]['Output'] += 'panic: fixture'; cases.append(('panic_before', rows, 1, True))
        rows = suite(True); rows[-2]['Action'] = 'fail'; cases.append(('positive_failed', rows, 1, True))
        cases.append(('compile_failure', [event('output', Output='[build failed]'), event('fail')], 1, True))
        cases.append(('malformed', [[], *suite()], 0, False))
        for label, rows, code, before in cases:
            with self.subTest(label):
                with self.assertRaises(ValueError):
                    ci.audit(rows, code, CONTRACT, before=before)


if __name__ == '__main__':
    unittest.main()
