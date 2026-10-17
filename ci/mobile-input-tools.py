#!/usr/bin/env python3
"""New current363 identity/dispatch/cleanup controls only; no Flutter or suite."""
import copy
import importlib.util
import json
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('mobile_input_controlled', ROOT/'ci/mobile-input.py')
lane = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lane)


class ChatInput363Tests(unittest.TestCase):
    def test_current363_vector_reverses_only_exact_five_before_and_four_new_without_historical_alias(self):
        cfg, old = lane.descriptor()
        lane.validate_inventory(cfg, cfg['source_rows'], old)
        self.assertEqual(len(cfg['source_rows']), 363)
        self.assertNotEqual(lane.FINAL_SOURCE, lane.BASE_SOURCE)
        lane.validate_local_reuse(cfg)
        changed_reuse = copy.deepcopy(cfg)
        changed_reuse['local_validation_reuse']['analyses9'][0]['source']['sha256'] = '0'*64
        with self.assertRaisesRegex(ValueError, 'local analysis9/gateway1 proof'):
            lane.validate_local_reuse(changed_reuse)
        wrong = copy.deepcopy(cfg['source_rows'])
        wrong[0]['sha256'] = '0'*64
        with self.assertRaisesRegex(ValueError, 'current363 source inventory'):
            lane.validate_inventory(cfg, wrong, old)
        wrong_cfg = copy.deepcopy(cfg)
        index = next(i for i, r in enumerate(wrong_cfg['delta9']) if r['before'] is not None)
        wrong_cfg['delta9'][index]['before']['sha256'] = '0'*64
        with self.assertRaisesRegex(ValueError, 'historical359 reverse5'):
            lane.validate_inventory(wrong_cfg, wrong_cfg['source_rows'], old)
        extra = copy.deepcopy(cfg['source_rows'])
        extra.append({'path': 'app/tool/check.sh', 'bytes': 1, 'sha256': '0'*64})
        with self.assertRaisesRegex(ValueError, 'current363 source inventory'):
            lane.validate_inventory(cfg, extra, old)

    def test_actual_dispatch_selects_only_unstarted25_with_exact_local9_plus1_and_preserves_manual_old359(self):
        cfg, _ = lane.descriptor()
        analyses, tests = lane.phase_commands(Path('/tmp/fresh-input363'), cfg)
        self.assertEqual(analyses, [])
        self.assertEqual(tests[-4:], lane.TEST_PATHS4)
        self.assertNotIn('test/core/im/open_im_gateway_test.dart', tests)
        self.assertEqual(tests[1:6], ['test', '--no-pub', '--machine', '--concurrency=1', '--no-enable-vmservice'])
        pattern = re.compile(tests[tests.index('--name')+1])
        self.assertTrue(all(pattern.fullmatch(n) for n in lane.NAMES25))
        self.assertIsNone(pattern.fullmatch(lane.OLD5[-1]))
        self.assertEqual(cfg['names26'], cfg['names25'] + cfg['reused_names1'])
        old = json.loads((ROOT/'ci/mobile/required-test-names.json').read_text())['current_full1308']
        untouched = [r['name'] for r in old if r['name'] not in lane.OLD5]
        self.assertTrue(untouched)
        self.assertTrue(all(pattern.fullmatch(n) is None for n in untouched))
        workflow = (ROOT/'.github/workflows/mobile-input.yml').read_text()
        self.assertIn('  workflow_dispatch:', workflow)
        self.assertNotIn('  push:', workflow)
        self.assertNotIn('  pull_request:', workflow)
        self.assertNotIn('  schedule:', workflow)
        self.assertNotIn('full-regression', workflow)
        self.assertIn('cancel-in-progress: false', workflow)
        historical = (ROOT/'.github/workflows/mobile-ci.yml').read_text()
        self.assertIn('ref: dfa0b855c3f90668f36aaa4fab95e32d671fd36e', historical)
        # The official toolchain/archives/SQLite and real-HOME setup remain
        # byte-identical after the four declared entry substitutions.
        setup = (ROOT/'ci/mobile-input-setup.sh').read_text()
        setup = setup.replace('usage: mobile-input-setup.sh COHORT FRESH_ROOT', 'usage: mobile-setup.sh COHORT FRESH_ROOT')
        setup = setup.replace('[[ "$cohort" == current363 ]]', '[[ "$cohort" == current359 ]]')
        setup = setup.replace('python3 "$repo/ci/mobile-input.py" materialize --output "$work/source"',
                              'python3 "$repo/ci/mobile-source.py" --cohort "$cohort" --output "$work/source"')
        setup = setup.replace('python3 "$repo/ci/mobile-input.py" verify --work "$work"',
                              'python3 "$repo/ci/mobile-check.py" verify --cohort "$cohort" --work "$work"')
        self.assertEqual(setup, (ROOT/'ci/mobile-setup.sh').read_text())

    def test_exact_machine25_and_real_fixture24_reject_reused_name_skip_missing_cleanup_and_ui_warning(self):
        cfg, _ = lane.descriptor()
        events = []
        for i, name in enumerate(lane.NAMES25):
            events.append({'type': 'testStart', 'test': {'id': i, 'name': name}})
            if name in cfg['cleanup']['widget_names']:
                events += [{'type': 'print', 'testID': i, 'message': 'group_revoke_cleanup '+m}
                           for m in cfg['cleanup']['widget_markers']]
            events.append({'type': 'testDone', 'testID': i, 'result': 'success', 'hidden': False, 'skipped': False})
        events += [{'type': 'testStart', 'test': {'id': 25, 'name': 'loading actual files'}},
                   {'type': 'testDone', 'testID': 25, 'result': 'success', 'hidden': True, 'skipped': False},
                   {'type': 'done', 'success': True}]
        def text(value):
            return '\n'.join(json.dumps(e) for e in value)
        result = lane.result_summary(text(events), cfg)
        self.assertEqual(result['tests']['passed'], 25)
        self.assertEqual(result['cleanup']['widget_names'], 24)
        self.assertTrue(result['cleanup']['exact_owned_four_marker_sequences'])
        wrong = copy.deepcopy(events)
        wrong[0]['test']['name'] = lane.OLD5[-1]
        with self.assertRaisesRegex(ValueError, 'name multiset'):
            lane.result_summary(text(wrong), cfg)
        wrong = copy.deepcopy(events)
        next(e for e in wrong if e['type'] == 'testDone')['skipped'] = True
        with self.assertRaisesRegex(ValueError, 'incomplete/failing'):
            lane.result_summary(text(wrong), cfg)
        wrong = [e for e in events if not (e['type'] == 'print' and e['testID'] == 0 and e['message'].endswith('sdk.end'))]
        with self.assertRaisesRegex(ValueError, 'missing/duplicate/out-of-order'):
            lane.result_summary(text(wrong), cfg)
        warning = copy.deepcopy(events)
        warning.insert(-1, {'type': 'print', 'testID': 0, 'message': 'A RenderFlex overflowed by 10 pixels'})
        with self.assertRaisesRegex(ValueError, 'warning'):
            lane.result_summary(text(warning), cfg)


if __name__ == '__main__':
    unittest.main(verbosity=2)
