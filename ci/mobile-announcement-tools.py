#!/usr/bin/env python3
"""Only new announcement overlay, scope, lineage, teardown and argv controls."""
import copy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import pwd
import re
import unittest

spec = importlib.util.spec_from_file_location('announcement_focused', Path(__file__).with_name('mobile-announcement.py'))
focused = importlib.util.module_from_spec(spec)
spec.loader.exec_module(focused)
INPUT = Path(__file__).parent / 'announcement26/inputs.json'


class AnnouncementFocusedTests(unittest.TestCase):
    def test_exact_six_before_and_five_new_collisions_fail_closed(self):
        base = {name: ('original-' + name).encode() for name in focused.PRODUCT}
        payload = {name: ('selected-' + name).encode() for name in focused.SELECTED}

        def row(name, body):
            return {'path': name, 'bytes': len(body), 'sha256': hashlib.sha256(body).hexdigest()}

        before = [row(name, base[name]) for name in focused.PRODUCT]
        selected = [dict(row(name, payload[name]), new=name not in base) for name in focused.SELECTED]
        self.assertEqual(set(focused.apply_overlay(base, before, selected, payload)), set(focused.SELECTED))
        altered = dict(base)
        altered[focused.PRODUCT[0]] = b'foreign source'
        with self.assertRaisesRegex(ValueError, 'source pin differs'):
            focused.apply_overlay(altered, before, selected, payload)
        collision = dict(base)
        collision[next(p for p in focused.SELECTED if p not in focused.PRODUCT)] = b'undeclared original'
        with self.assertRaisesRegex(ValueError, 'collision'):
            focused.apply_overlay(collision, before, selected, payload)

    def test_names_analysis_and_historical_card_revoke_lineage_stay_exact(self):
        cfg = json.loads(INPUT.read_text())
        focused.validate_descriptor(cfg)
        changed = copy.deepcopy(cfg)
        changed['target']['names'][0] = 'different passing case'
        with self.assertRaisesRegex(ValueError, 'scope'):
            focused.validate_descriptor(changed)
        changed = copy.deepcopy(cfg)
        changed['target']['analysis_paths'].append('lib/app.dart')
        with self.assertRaisesRegex(ValueError, 'scope'):
            focused.validate_descriptor(changed)
        original = cfg['lineage']
        deps = cfg['unchanged_dependencies']
        base = {
            'chat_card': {'selected_files': original['card351_original17'],
                          'current_source_lineage': original['card351_to354']},
            'group_revoke': {'selected_files': original['group_revoke354_original6']},
            'current354': {'files': [deps['pubspec'], deps['lock']]},
            'toolchain': deps['official_toolchain'], 'native_source_snapshot': deps['native_snapshot'],
        }
        self.assertTrue(focused.lineage_contract(cfg, base))
        for key in ('card351_original17', 'group_revoke354_original6'):
            changed = copy.deepcopy(cfg)
            changed['lineage'][key][0]['sha256'] = '0' * 64
            with self.assertRaisesRegex(ValueError, 'lineage'):
                focused.lineage_contract(changed, base)
        changed = copy.deepcopy(cfg)
        changed['lineage']['card354_to359'][0]['before354']['sha256'] = '0' * 64
        with self.assertRaisesRegex(ValueError, 'lineage'):
            focused.lineage_contract(changed, base)

    def test_all_eighteen_owned_fixtures_need_their_own_four_ordered_markers(self):
        events = []
        for identity, name in enumerate(focused.WIDGET_NAMES):
            events.append({'type': 'testStart', 'test': {'id': identity, 'name': name}})
            events += [{'type': 'print', 'testID': identity,
                        'message': 'group_announcement_cleanup ' + marker} for marker in focused.MARKERS]
        text = lambda items: '\n'.join(map(json.dumps, items))
        self.assertEqual(focused.cleanup_summary(text(events))['events'], 72)
        with self.assertRaisesRegex(ValueError, 'order'):
            focused.cleanup_summary(text(events[:-1]))
        with self.assertRaisesRegex(ValueError, 'order'):
            focused.cleanup_summary(text(events + [events[-1]]))
        changed = copy.deepcopy(events)
        changed[0]['test']['name'] = focused.TWENTY_SIX[0]  # A pure store case has no SDK teardown.
        with self.assertRaisesRegex(ValueError, 'foreign'):
            focused.cleanup_summary(text(changed))

    def test_only_analysis_eleven_then_exact_new24_old2_and_real_home(self):
        work = Path('/tmp/fresh-mobile-announcement')
        analyses, command = focused.phase_commands(work)
        self.assertEqual([args[-1] for args in analyses], focused.RUN_ANALYSIS)
        self.assertEqual(len(analyses), 6)
        self.assertEqual(focused.RUN_ANALYSIS, focused.ANALYSIS[5:])
        cfg = json.loads(INPUT.read_text())
        rows = [r['source_pin'] for r in cfg['target']['analysis_reuse_entries']]
        self.assertEqual(focused.reused_analysis_contract(cfg, rows)['reused_successes'], 5)
        changed = copy.deepcopy(cfg)
        changed['target']['analysis_reuse_entries'][0]['log_utf8'] += 'changed'
        with self.assertRaisesRegex(ValueError, 'source pin differs'):
            focused.reused_analysis_contract(changed, rows)
        altered = copy.deepcopy(rows)
        altered[0]['sha256'] = '0' * 64
        with self.assertRaisesRegex(ValueError, 'source body differs'):
            focused.reused_analysis_contract(cfg, altered)
        self.assertTrue(all(args[1:3] == ['analyze', '--fatal-infos'] for args in analyses))
        self.assertEqual(command[-4:], focused.TESTS)
        self.assertEqual(command.count('--name'), 1)
        pattern = re.compile(command[command.index('--name') + 1])
        self.assertTrue(all(pattern.fullmatch(name) for name in focused.TWENTY_SIX))
        self.assertIsNone(pattern.fullmatch('an old group revoke case'))
        self.assertNotIn('full-regression', command)
        self.assertNotIn('reference-before-entry', command)
        account = pwd.getpwuid(os.getuid())
        env = focused.run_environment(work, account.pw_name)
        self.assertEqual(env['HOME'], account.pw_dir)
        self.assertNotEqual(env['HOME'], str(work/'home'))
        self.assertEqual(env['PUB_CACHE'], str(work/'pub-cache'))
        self.assertEqual(env['XDG_CONFIG_HOME'], str(work/'config'))
        self.assertEqual(env['XDG_CACHE_HOME'], str(work/'config/cache'))
        self.assertTrue(all(env[key] == str(work/'tmp') for key in ('TMPDIR', 'TMP', 'TEMP')))


    def test_diagnostic_collection_continues_but_infrastructure_and_unknown_output_stop(self):
        commands = focused.phase_commands(Path('/tmp/fresh-mobile-announcement'))[0][:3]
        calls, reports = [], []

        def result(label, command, diagnostic=False):
            heading = 'Analyzing ' + Path(command[-1]).name + '...\n'
            text = (heading + '\n   info - ' + Path(command[-1]).name
                    + ':1:1 - Add braces. - curly_braces_in_flow_control_structures\n\n1 issue found.\n'
                    if diagnostic else heading + 'No issues found!\n')
            return {'phase': label, 'command': command, 'child_exit_code': 1 if diagnostic else 0,
                    'latched_failure': None, 'log_sha256': focused.sha(text.encode())}, text

        def execute(label, command):
            calls.append(label)
            return result(label, command, diagnostic=len(calls) == 2)

        summary = focused.collect_analyses(commands, execute, lambda: None, reports.append)
        self.assertEqual(len(calls), 3)
        self.assertEqual(len(reports), 3)
        self.assertFalse(summary['all_passed'])
        self.assertEqual(summary['failed_paths'], [commands[1][-1]])
        calls.clear()

        def resource_failure(label, command):
            calls.append(label)
            if len(calls) == 2:
                raise RuntimeError('resource/timeout/source boundary stopped')
            return result(label, command)

        with self.assertRaisesRegex(RuntimeError, 'boundary stopped'):
            focused.collect_analyses(commands, resource_failure, lambda: None, lambda _: None)
        self.assertEqual(len(calls), 2)
        calls.clear()

        def unknown_output(label, command):
            calls.append(label)
            record, _ = result(label, command)
            text = 'Unexpected SDK failure\n'
            record['log_sha256'] = focused.sha(text.encode())
            return record, text

        with self.assertRaisesRegex(ValueError, 'unknown successful analyzer'):
            focused.collect_analyses(commands, unknown_output, lambda: None, lambda _: None)
        self.assertEqual(len(calls), 1)


if __name__ == '__main__':
    unittest.main(verbosity=2)
