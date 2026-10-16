#!/usr/bin/env python3
"""Three prepared pure controls. No Go/verify/materialization/Docker/DB calls."""
import hashlib
import importlib.util
import os
from pathlib import Path
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location('permanent_namespace_origin_controller', Path(__file__).with_name('run.py'))
SUBJECT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SUBJECT)


def events(package, expected):
    name = SUBJECT.PREFIX + package
    result = [{'Package': name, 'Action': 'start'}]
    for top in expected['tops']:
        result.append({'Package': name, 'Action': 'run', 'Test': top})
        for sub in expected['subs']:
            if sub.startswith(top + '/'):
                result.extend([{'Package': name, 'Action': 'run', 'Test': sub},
                               {'Package': name, 'Action': 'pass', 'Test': sub}])
        result.append({'Package': name, 'Action': 'pass', 'Test': top})
    result.append({'Package': name, 'Action': 'pass'})
    return result


class ControllerTests(unittest.TestCase):
    def test_exact_new_ten_and_fifteen_no_missing_duplicate_or_skip(self):
        totals = [0, 0]
        for package, selected in SUBJECT.TARGETS.items():
            raw = events(package, selected)
            result = SUBJECT.audit(raw, 0, package, selected)
            totals[0] += result['topPASS']
            totals[1] += result['subPASS']
            for change in ('missing-run', 'duplicate-pass', 'missing-footer', 'foreign-sub',
                           'skip', 'fail', 'race', 'post-footer', 'nonzero'):
                bad = [dict(r) for r in raw]
                code = 0
                if change == 'missing-run':
                    bad.pop(1)
                elif change == 'duplicate-pass':
                    index = next(i for i, r in enumerate(bad) if r.get('Test') and r['Action'] == 'pass')
                    bad.insert(index, dict(bad[index]))
                elif change == 'missing-footer':
                    bad.pop()
                elif change == 'foreign-sub':
                    bad[1]['Test'] += '/unselected'
                elif change in ('skip', 'fail'):
                    index = next(i for i, r in enumerate(bad) if r.get('Test') and r['Action'] == 'pass')
                    bad[index]['Action'] = change
                elif change == 'race':
                    bad.insert(1, {'Package': raw[0]['Package'], 'Action': 'output', 'Output': 'WARNING: DATA RACE'})
                elif change == 'post-footer':
                    bad.append({'Package': raw[0]['Package'], 'Action': 'output'})
                else:
                    code = 1
                with self.subTest(package=package, change=change), self.assertRaises(ValueError):
                    SUBJECT.audit(bad, code, package, selected)
        self.assertEqual(totals, [10, 15])
        for resource, code in [({'status': 'monitor_error', 'exit_code': None}, 75),
                               ({'status': 'passed', 'exit_code': 0}, 1),
                               ({'status': 'stopped_task_allocation_gate', 'exit_code': -15}, 75)]:
            with self.assertRaises(ValueError):
                SUBJECT.require_guard(resource, code)

    def test_exact_owned_loopback_environment_rejects_SQL_vendor_or_foreign_identity(self):
        env = {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux',
               'GITHUB_RUN_ID': '42', 'GITHUB_RUN_ATTEMPT': '1', 'GITHUB_SHA': 'a' * 40,
               'GOTOOLCHAIN': 'local', 'GOWORK': 'off', 'LUOBO_STORAGE_TEST_MONGO_URI': SUBJECT.MONGO,
               'LUOBO_GROUP_RECEIVER_TEST_MONGO_URI': SUBJECT.MONGO,
               'LUOBO_EMAIL_INTEGRATION': '0', 'LUOBO_RUN_LEGACY_LOAD_TESTS': '0', 'LUOBO_RUN_LEGACY_EXAMPLES': '0'}
        expected = {k: v for k, v in env.items() if k.startswith('LUOBO_')}
        self.assertEqual(SUBJECT.fixture_environment(env), expected)
        for key, value in [('LUOBO_STORAGE_TEST_MONGO_URI', None),
                           ('LUOBO_GROUP_RECEIVER_TEST_MONGO_URI', 'mongodb://foreign/'),
                           ('LUOBO_RISK_TEST_MYSQL_FIXTURE', '/tmp/foreign.json'),
                           ('LUOBO_INTEGRATION_KAFKA_BROKER', 'foreign'),
                           ('LUOBO_FCM_CREDENTIALS', '/tmp/private'),
                           ('GITHUB_SHA', 'unknown'), ('GITHUB_RUN_ATTEMPT', '../1'),
                           ('RUNNER_ENVIRONMENT', 'self-hosted')]:
            bad = dict(env)
            if value is None:
                bad.pop(key)
            else:
                bad[key] = value
            with self.subTest(key=key), self.assertRaises(ValueError):
                SUBJECT.fixture_environment(bad)

    def test_seventeen_overlay_twelve_exact_before_five_new_and_no_extra_sources(self):
        targets = sorted(SUBJECT.EXISTING_TARGETS | SUBJECT.NEW_TARGETS)
        existing = SUBJECT.EXISTING_TARGETS
        self.assertEqual((len(targets), len(existing), len(SUBJECT.NEW_TARGETS)), (17, 12, 5))
        original_repo = SUBJECT.REPO
        try:
            with tempfile.TemporaryDirectory() as temporary:
                SUBJECT.REPO = Path(temporary).resolve()
                before, after, overlay = {}, {}, []
                for target in targets:
                    name = 'ci/candidates/group-namespace-origin/source/' + target
                    original, updated = b'package old\n', b'package changed\n'
                    if target in existing:
                        before[target] = original
                    after[target] = updated
                    out = SUBJECT.REPO / name
                    out.parent.mkdir(parents=True, exist_ok=True)
                    out.write_bytes(updated)
                    out.chmod(0o644)
                    overlay.append({'path': name, 'target': target, 'bytes': len(updated),
                                    'sha256': hashlib.sha256(updated).hexdigest(), 'mode': '0o644',
                                    'beforeSHA256': hashlib.sha256(original).hexdigest() if target in existing else None})
                self.assertEqual(SUBJECT.apply_overlay({'overlay': overlay}, before), after)
                for change in ('wrong-before', 'new-was-present', 'old-was-missing', 'mode', 'duplicate', 'extra'):
                    bad, previous = [dict(r) for r in overlay], dict(before)
                    changed_file = SUBJECT.REPO / overlay[0]['path']
                    extra = changed_file.with_name('extra.go')
                    if change == 'wrong-before':
                        next(r for r in bad if r['target'] in existing)['beforeSHA256'] = '0' * 64
                    elif change == 'new-was-present':
                        previous[sorted(SUBJECT.NEW_TARGETS)[0]] = b'old hidden source\n'
                    elif change == 'old-was-missing':
                        previous.pop(next(iter(existing)))
                    elif change == 'mode':
                        changed_file.chmod(0o600)
                    elif change == 'duplicate':
                        bad[-1] = dict(bad[0])
                    else:
                        extra.write_bytes(b'package extra\n')
                    with self.subTest(change=change), self.assertRaises(ValueError):
                        SUBJECT.apply_overlay({'overlay': bad}, previous)
                    changed_file.chmod(0o644)
                    if extra.exists():
                        extra.unlink()
        finally:
            SUBJECT.REPO = original_repo


if __name__ == '__main__':
    unittest.main(verbosity=2)
