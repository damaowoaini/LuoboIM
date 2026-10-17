#!/usr/bin/env python3
"""Four directly affected scope controls; original failure/postproof control is reused."""
import copy
import importlib.util
import json
from pathlib import Path
import re
import tempfile
import threading
import types
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('mobile_input_controlled', ROOT/'ci/mobile-input.py')
lane = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lane)


def events_for(cfg, phase, *, fail=False):
    events = []
    for i, name in enumerate(phase['names']):
        events.append({'type': 'testStart', 'test': {'id': i, 'name': name}})
        if name in cfg['cleanup']['widget_names']:
            events += [{'type': 'print', 'testID': i, 'message': 'group_revoke_cleanup '+m}
                       for m in cfg['cleanup']['widget_markers']]
        if fail and i == 0:
            # Real Flutter testWidgets can report isFailure=false and a fixed
            # TestFailure print. This is an assertion, not a loader exception.
            events += [{'type': 'print', 'testID': i,
                        'message': 'The following TestFailure was thrown running a test:'},
                       {'type': 'error', 'testID': i, 'isFailure': False}]
        events.append({'type': 'testDone', 'testID': i, 'result': 'error' if fail and i == 0 else 'success',
                       'hidden': False, 'skipped': False})
    events += [{'type': 'done', 'success': not fail}]
    return events


def machine_text(events):
    return '\n'.join(json.dumps(e) for e in events)


class ChatInput363Tests(unittest.TestCase):
    # Original method names stay stable; R5 has exact9 replacements/4 new
    # and phase3 then isolated1. Failure/postproof control body stays exact.
    def test_current363_vector_reverses_only_exact_five_before_and_four_new_without_historical_alias(self):
        cfg, old = lane.descriptor()
        lane.validate_inventory(cfg, cfg['source_rows'], old)
        self.assertEqual(len(cfg['delta13']), 13)
        self.assertEqual(sum(r['before'] is not None for r in cfg['delta13']), 9)
        self.assertNotEqual(lane.FINAL_SOURCE, lane.BASE_SOURCE)
        lane.validate_local_reuse(cfg)
        lane.validate_cloud_reuse(cfg)
        for key, operation, pattern in [
            ('local_validation_reuse', lane.validate_local_reuse, 'local analysis13/gateway1 proof'),
            ('cloud_validation_reuse', lane.validate_cloud_reuse, 'prior cloud22 proof')]:
            changed = copy.deepcopy(cfg)
            changed[key]['forged'] = True
            with self.assertRaisesRegex(ValueError, pattern):
                operation(changed)
        wrong = copy.deepcopy(cfg['source_rows'])
        wrong[0]['sha256'] = '0'*64
        with self.assertRaisesRegex(ValueError, 'current363 source inventory'):
            lane.validate_inventory(cfg, wrong, old)
        wrong_cfg = copy.deepcopy(cfg)
        next(r for r in wrong_cfg['delta13'] if r['before'] is not None)['before']['sha256'] = '0'*64
        with self.assertRaisesRegex(ValueError, 'historical359 reverse9'):
            lane.validate_inventory(wrong_cfg, wrong_cfg['source_rows'], old)
        extra = copy.deepcopy(cfg['source_rows'])
        extra.append({'path': 'app/tool/check.sh', 'bytes': 1, 'sha256': '0'*64})
        with self.assertRaisesRegex(ValueError, 'current363 source inventory'):
            lane.validate_inventory(cfg, extra, old)
        self.assertFalse(cfg['cloud_validation_reuse']['prior_source_post_exact_recorded'])
        self.assertEqual(len(cfg['local_validation_reuse']['analyses13']), 13)

    def test_actual_dispatch_selects_only_unstarted25_with_exact_local9_plus1_and_preserves_manual_old359(self):
        cfg, _ = lane.descriptor()
        phases = lane.phase_commands(Path('/tmp/fresh-input363'), cfg)
        self.assertEqual([p['expected'] for p in phases], [3, 1])
        self.assertEqual(phases[0]['command'][-2:], lane.PHASE3_PATHS)
        self.assertEqual(phases[1]['command'][-1:], lane.PHASE1_PATHS)
        selected = []
        for phase in phases:
            command = phase['command']
            self.assertEqual(command[1:6], ['test', '--no-pub', '--machine', '--concurrency=1', '--no-enable-vmservice'])
            pattern = re.compile(command[command.index('--name')+1])
            self.assertTrue(all(pattern.fullmatch(n) for n in phase['names']))
            self.assertTrue(all(pattern.fullmatch(n) is None for n in lane.REUSED22+[lane.OLD5[-1]]))
            selected += phase['names']
        first = re.compile(phases[0]['command'][phases[0]['command'].index('--name')+1])
        self.assertIsNone(first.fullmatch(lane.PHASE1[0]))
        self.assertEqual(set(selected+lane.REUSED22+[lane.OLD5[-1]]), set(lane.UNIVERSE27))
        self.assertEqual(len(set(selected)), 4)
        self.assertEqual([len(lane.phase_cleanup(cfg, p)['widget_names']) for p in phases], [3, 1])
        workflow = (ROOT/'.github/workflows/mobile-input.yml').read_text()
        self.assertIn('  workflow_dispatch:', workflow)
        for forbidden in ['  push:', '  pull_request:', '  schedule:', 'full-regression']:
            self.assertNotIn(forbidden, workflow)
        self.assertIn('cancel-in-progress: false', workflow)
        historical = (ROOT/'.github/workflows/mobile-ci.yml').read_text()
        self.assertIn('ref: dfa0b855c3f90668f36aaa4fab95e32d671fd36e', historical)
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
        for phase, fixtures in zip(lane.phase_commands(Path('/tmp/fresh-input363'), cfg), [3, 1]):
            with self.subTest(phase=phase['label']):
                events = events_for(cfg, phase)
                result = lane.result_summary(machine_text(events), cfg, phase)
                self.assertEqual(result['tests']['passed'], phase['expected'])
                self.assertEqual(result['cleanup']['widget_names'], fixtures)
                wrong = copy.deepcopy(events)
                wrong[0]['test']['name'] = lane.REUSED22[0]
                with self.assertRaisesRegex(ValueError, 'name multiset'):
                    lane.result_summary(machine_text(wrong), cfg, phase)
                wrong = copy.deepcopy(events)
                next(e for e in wrong if e['type'] == 'testDone')['skipped'] = True
                with self.assertRaisesRegex(ValueError, 'incomplete/failing'):
                    lane.result_summary(machine_text(wrong), cfg, phase)
                wrong = [e for e in events if not (e['type'] == 'print' and e['testID'] == 0 and e['message'].endswith('sdk.end'))]
                with self.assertRaisesRegex(ValueError, 'missing/duplicate/out-of-order'):
                    lane.result_summary(machine_text(wrong), cfg, phase)
                warning = copy.deepcopy(events)
                warning.insert(-1, {'type': 'print', 'testID': 0, 'message': 'A RenderFlex overflowed by 10 pixels'})
                with self.assertRaisesRegex(ValueError, 'warning'):
                    lane.result_summary(machine_text(warning), cfg, phase)

    def test_failure_source_post_is_retained_and_never_masks_primary_or_claims_old_post(self):
        cfg, old = lane.descriptor()
        phase = lane.phase_commands(Path('/tmp/fresh-input363'), cfg)[1]
        for source_fails in [False, True]:
            with self.subTest(source_fails=source_fails), tempfile.TemporaryDirectory() as raw:
                work = Path(raw)
                (work/'evidence').mkdir()
                guard = types.SimpleNamespace(reason=None)
                def failed(*args, **kwargs):
                    (work/'evidence'/(phase['label']+'.log')).write_text(machine_text(events_for(cfg, phase)))
                    lane.CHECK.write_json(work/'evidence'/(phase['label']+'.resource.json'), {'child_exit_code': 0, 'latched_failure': None})
                    raise RuntimeError('primary command failure')
                files = {str(i): b'x' for i in range(415)}
                with mock.patch.object(lane.CHECK, 'guarded', side_effect=failed), \
                     mock.patch.object(lane, 'verify_work', side_effect=ValueError('post drift') if source_fails else None, return_value=files), \
                     mock.patch.object(lane.SOURCE, 'source_digest', return_value=lane.FINAL_PORTABLE):
                    result, primary = lane.run_phase(work, cfg, old, guard, phase)
                self.assertIsInstance(primary, RuntimeError)
                self.assertFalse(result['accepted'])
                post = json.loads((work/'evidence'/(phase['label']+'.source-post.json')).read_text())
                self.assertEqual(post['accepted'], not source_fails)
                self.assertEqual(post['exception_type'], 'ValueError' if source_fails else None)
                self.assertEqual(result['exception_type'], 'RuntimeError')
                self.assertFalse(cfg['cloud_validation_reuse']['prior_source_post_exact_recorded'])

    def test_assertion9_can_diagnose_last1_but_child_zero_timeout_stops_and_whole_pass_requires_both(self):
        cfg, old = lane.descriptor()
        class Guard:
            def __init__(self, work):
                self.reason = None
                self.minimum = lane.CHECK.INITIAL
                self.peak = 0
                self.stop = threading.Event()
            def sample(self):
                return self.minimum
            def poll(self):
                self.stop.wait()
        for scenario, count, passed in [('assertion', 2, False), ('timeout-child0', 1, False), ('both-success', 2, True)]:
            with self.subTest(scenario=scenario), tempfile.TemporaryDirectory() as raw:
                work = Path(raw)
                (work/'evidence').mkdir()
                called = []
                def execute(command, actual_work, guard, label, **kwargs):
                    called.append(label)
                    phase = next(p for p in lane.phase_commands(work, cfg) if p['label'] == label)
                    fail = scenario == 'assertion' and len(called) == 1
                    log = work/'evidence'/(label+'.log')
                    log.write_text(machine_text(events_for(cfg, phase, fail=fail)))
                    if scenario == 'timeout-child0':
                        guard.reason = 'phase_timeout'
                    lane.CHECK.write_json(work/'evidence'/(label+'.resource.json'),
                                          {'child_exit_code': 1 if fail else 0, 'latched_failure': guard.reason})
                    if guard.reason:
                        raise ValueError('phase failed: '+label)
                    return log
                files = {str(i): b'x' for i in range(415)}
                with mock.patch.object(lane, 'descriptor', return_value=(cfg, old)), \
                     mock.patch.object(lane, 'verify_work', return_value=files), \
                     mock.patch.object(lane.SOURCE, 'source_digest', return_value=lane.FINAL_PORTABLE), \
                     mock.patch.object(lane.CHECK, 'prove_namespace'), \
                     mock.patch.object(lane.CHECK, 'Guard', Guard), \
                     mock.patch.object(lane.CHECK, 'guarded', side_effect=execute):
                    if passed:
                        lane.run(work)
                    else:
                        with self.assertRaises(ValueError):
                            lane.run(work)
                self.assertEqual(len(called), count)
                self.assertEqual((work/'evidence/input-scoped-passed.json').exists(), passed)
                self.assertEqual(len(list((work/'evidence').glob('*.source-post.json'))), count)
                if scenario == 'timeout-child0':
                    self.assertEqual(json.loads((work/'evidence/input-failed.json').read_text())['latched_failure'], 'phase_timeout')
                if passed:
                    result = json.loads((work/'evidence/input-scoped-passed.json').read_text())
                    self.assertEqual(result['cloud_tests_executed'], 4)
                    self.assertEqual(result['cleanup']['prior_cloud_reused_markers'], 84)
                    self.assertEqual(result['cleanup']['new_markers'], 16)
                    self.assertFalse(result['cleanup']['newly_executed_25_fixtures'])


if __name__ == '__main__':
    unittest.main(verbosity=2)
