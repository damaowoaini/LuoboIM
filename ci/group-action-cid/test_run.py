#!/usr/bin/env python3
"""Three new pure controls; no Go, parent.verify, fixture or Docker execution."""
import hashlib
import importlib.util
import os
from pathlib import Path
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location('cid_target7_controller', Path(__file__).with_name('run.py'))
SUBJECT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SUBJECT)


def events(expected):
    package = SUBJECT.PREFIX + 'internal/synthetic'
    result = [{'Package': package, 'Action': 'start'}]
    for top in expected['tops']:
        result.append({'Package': package, 'Action': 'run', 'Test': top})
        for sub in expected['subs']:
            if sub.startswith(top + '/'):
                result.extend([{'Package': package, 'Action': 'run', 'Test': sub},
                               {'Package': package, 'Action': 'pass', 'Test': sub}])
        result.append({'Package': package, 'Action': 'pass', 'Test': top})
    result.append({'Package': package, 'Action': 'pass'})
    return result


class ControllerTests(unittest.TestCase):
    def test_exact_top_and_sub_run_pass_footer(self):
        expected = {'tops': ['TestOne'], 'subs': ['TestOne/first', 'TestOne/second']}
        raw = events(expected)
        self.assertEqual(SUBJECT.audit(raw, 0, 'internal/synthetic', expected)['subPASS'], 2)
        self.assertEqual(sum(len(v['tops']) for v in SUBJECT.TARGETS.values()), 7)
        self.assertEqual(sum(len(v['subs']) for v in SUBJECT.TARGETS.values()), 5)
        for bad, code in [(raw[:2] + raw[3:], 0), (raw[:2] + [raw[2]] + raw[2:], 0),
                          (raw, 1), ([raw[0], raw[-1]], 0),
                          (raw + [{'Package': raw[0]['Package'], 'Action': 'output'}], 0)]:
            with self.subTest(code=code, length=len(bad)), self.assertRaises(ValueError):
                SUBJECT.audit(bad, code, 'internal/synthetic', expected)
        for change in ('foreign-sub', 'skip', 'fail', 'race', 'panic'):
            bad = [dict(r) for r in raw]
            if change == 'foreign-sub':
                bad[2]['Test'] = 'TestOne/unexpected'
            elif change in ('skip', 'fail'):
                bad[3]['Action'] = change
            else:
                bad.insert(1, {'Package': raw[0]['Package'], 'Action': 'output',
                               'Output': 'WARNING: DATA RACE' if change == 'race' else 'panic: synthetic'})
            with self.subTest(change=change), self.assertRaises(ValueError):
                SUBJECT.audit(bad, 0, 'internal/synthetic', expected)
        for resource, code in [({'status': 'monitor_error', 'exit_code': None}, 75),
                               ({'status': 'passed', 'exit_code': 0}, 1),
                               ({'status': 'stopped_task_allocation_gate', 'exit_code': -15}, 75)]:
            with self.assertRaises(ValueError):
                SUBJECT.require_guard(resource, code)

    def test_fixture_admission_and_private_path(self):
        env = {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux',
               'GITHUB_RUN_ID': '42', 'GITHUB_RUN_ATTEMPT': '1', 'GITHUB_SHA': 'a' * 40,
               'GOTOOLCHAIN': 'local', 'GOWORK': 'off', 'LUOBO_STORAGE_TEST_MONGO_URI': SUBJECT.MONGO,
               'LUOBO_GROUP_RECEIVER_TEST_MONGO_URI': SUBJECT.MONGO, 'LUOBO_GROUP_ROLE_TEST_MONGO_URI': SUBJECT.MONGO,
               'LUOBO_EMAIL_INTEGRATION': '0', 'LUOBO_RUN_LEGACY_LOAD_TESTS': '0', 'LUOBO_RUN_LEGACY_EXAMPLES': '0'}
        self.assertEqual(SUBJECT.fixture_environment(env)['LUOBO_RISK_TEST_MYSQL_FIXTURE'],
                         '/tmp/luobo-risk-42-1/private/fixture.json')
        for key, value in [('LUOBO_STORAGE_TEST_MONGO_URI', None), ('LUOBO_GROUP_RECEIVER_TEST_MONGO_URI', 'mongodb://foreign/'),
                           ('LUOBO_RISK_TEST_MYSQL_FIXTURE', '/tmp/foreign.json'), ('LUOBO_INTEGRATION_KAFKA_BROKER', 'foreign'),
                           ('GITHUB_SHA', 'unknown'), ('GITHUB_RUN_ID', '../42')]:
            bad = dict(env)
            if value is None:
                bad.pop(key)
            else:
                bad[key] = value
            with self.subTest(key=key), self.assertRaises(ValueError):
                SUBJECT.fixture_environment(bad)
        with tempfile.TemporaryDirectory() as temporary:
            private = Path(temporary).resolve() / 'private'
            private.mkdir(mode=0o700)
            fixture = private / 'fixture.json'
            body = b'{"fixture":"synthetic"}\n'
            fixture.write_bytes(body)
            fixture.chmod(0o600)
            self.assertEqual(SUBJECT.private_fixture_sha(fixture, fixture), hashlib.sha256(body).hexdigest())
            with self.assertRaises(ValueError):
                SUBJECT.private_fixture_sha(fixture, private / 'other.json')
            fixture.chmod(0o640)
            with self.assertRaises(ValueError):
                SUBJECT.private_fixture_sha(fixture, fixture)
            fixture.chmod(0o600)
            linked = private / 'linked.json'
            os.link(fixture, linked)
            with self.assertRaises(ValueError):
                SUBJECT.private_fixture_sha(fixture, fixture)
            linked.unlink()
            linked.symlink_to(fixture)
            with self.assertRaises(ValueError):
                SUBJECT.private_fixture_sha(linked, linked)

    def test_four_source_overlay_and_custody(self):
        previous_repo = SUBJECT.REPO
        try:
            with tempfile.TemporaryDirectory() as temporary:
                SUBJECT.REPO = Path(temporary).resolve()
                before, after, overlays = {}, {}, []
                for n in range(4):
                    target = 'server/internal/source%d.go' % n
                    path = 'ci/candidates/group-action-cid/source/' + target
                    original, updated = b'package old\n', b'package changed\n'
                    before[target], after[target] = original, updated
                    p = SUBJECT.REPO / path
                    p.parent.mkdir(parents=True, exist_ok=True)
                    p.write_bytes(updated)
                    p.chmod(0o644)
                    overlays.append({'path': path, 'target': target, 'mode': '0o644', 'bytes': len(updated),
                                     'sha256': hashlib.sha256(updated).hexdigest(), 'beforeSHA256': hashlib.sha256(original).hexdigest()})
                self.assertEqual(SUBJECT.apply_overlay({'overlay': overlays}, before), after)
                for change in ('before', 'mode', 'new', 'extra'):
                    bad = [dict(r) for r in overlays]
                    p = SUBJECT.REPO / bad[0]['path']
                    if change == 'before':
                        bad[0]['beforeSHA256'] = '0' * 64
                    elif change == 'mode':
                        p.chmod(0o600)
                    elif change == 'new':
                        bad[0]['target'] = 'server/internal/new.go'
                    else:
                        (p.parent / 'extra.go').write_bytes(b'package extra\n')
                    with self.subTest(change=change), self.assertRaises(ValueError):
                        SUBJECT.apply_overlay({'overlay': bad}, before)
                    p.chmod(0o644)
        finally:
            SUBJECT.REPO = previous_repo


if __name__ == '__main__':
    unittest.main(verbosity=2)
