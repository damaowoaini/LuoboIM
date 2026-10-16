#!/usr/bin/env python3
"""Only the four new isolated-overlay/scope/cleanup/argv controls; no Dart."""
import copy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import pwd
import re
import unittest

spec = importlib.util.spec_from_file_location('revoke_focused', Path(__file__).with_name('mobile-group-revoke.py'))
focused = importlib.util.module_from_spec(spec)
spec.loader.exec_module(focused)
INPUT = Path(__file__).parent / 'group-revoke-v3/inputs.json'


class GroupRevokeFocusedTests(unittest.TestCase):
    def test_exact_before_and_new_path_collision_fail_closed(self):
        base = {name: ('original-' + name).encode() for name in focused.PRODUCT}
        payload = {name: ('selected-' + name).encode() for name in focused.SELECTED}
        def row(name, body):
            return {'path': name, 'bytes': len(body), 'sha256': hashlib.sha256(body).hexdigest()}
        before = [row(name, base[name]) for name in focused.PRODUCT]
        selected = [dict(row(name, payload[name]), new=name not in base) for name in focused.SELECTED]
        self.assertEqual(len(focused.apply_overlay(base, before, selected, payload)), 6)
        changed = dict(base)
        changed[focused.PRODUCT[1]] = b'foreign current source'
        with self.assertRaisesRegex(ValueError, 'source pin differs'):
            focused.apply_overlay(changed, before, selected, payload)
        changed = dict(base)
        changed[focused.SELECTED[3]] = b'undeclared existing file'
        with self.assertRaisesRegex(ValueError, 'collision'):
            focused.apply_overlay(changed, before, selected, payload)

    def test_same_count_name_swap_or_extra_analysis_cannot_expand_scope(self):
        cfg = json.loads(INPUT.read_text())
        focused.validate_descriptor(cfg)
        swapped = copy.deepcopy(cfg)
        swapped['target']['names'][0] = 'a different passing case'
        with self.assertRaisesRegex(ValueError, 'scope'):
            focused.validate_descriptor(swapped)
        expanded = copy.deepcopy(cfg)
        expanded['target']['analysis_paths'].append('lib/app.dart')
        with self.assertRaisesRegex(ValueError, 'scope'):
            focused.validate_descriptor(expanded)
        omitted = copy.deepcopy(cfg)
        omitted['base']['helper_pins'] = []
        with self.assertRaisesRegex(ValueError, 'scope'):
            focused.validate_descriptor(omitted)

    def test_each_selected_fixture_requires_its_own_ordered_shutdown(self):
        events = []
        for identity, name in enumerate(focused.FIVE):
            events.append({'type': 'testStart', 'test': {'id': identity, 'name': name}})
            events += [{'type': 'print', 'testID': identity,
                        'message': 'group_revoke_cleanup ' + marker} for marker in focused.MARKERS]
        text = lambda items: '\n'.join(map(json.dumps, items))
        self.assertEqual(focused.cleanup_summary(text(events))['events'], 20)
        with self.assertRaisesRegex(ValueError, 'missing'):
            focused.cleanup_summary(text(events[:-1]))
        with self.assertRaisesRegex(ValueError, 'order'):
            focused.cleanup_summary(text(events + [events[-1]]))
        changed = copy.deepcopy(events)
        changed[0]['test']['name'] = 'another owner'
        with self.assertRaisesRegex(ValueError, 'foreign'):
            focused.cleanup_summary(text(changed))

    def test_command_has_only_changed3_and_one_exact_widget5(self):
        work = Path('/tmp/fresh-mobile-ci')
        analyses, command = focused.phase_commands(work)
        self.assertEqual([args[-1] for args in analyses], focused.ANALYSIS)
        self.assertTrue(all(args[1:3] == ['analyze', '--fatal-infos'] for args in analyses))
        self.assertEqual(command[-1], focused.TEST)
        self.assertEqual(command.count('--name'), 1)
        pattern = re.compile(command[command.index('--name') + 1])
        self.assertTrue(all(pattern.fullmatch(name) for name in focused.FIVE))
        self.assertIsNone(pattern.fullmatch('an old repository case'))
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


if __name__ == '__main__':
    unittest.main(verbosity=2)
