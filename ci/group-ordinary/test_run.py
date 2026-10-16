#!/usr/bin/env python3
"""Six pure controller gates; no Go, Docker, DB or source1064 materialization."""
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


if __name__ == '__main__':
    unittest.main()
