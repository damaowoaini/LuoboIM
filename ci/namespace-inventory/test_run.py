#!/usr/bin/env python3
"""Three prepared inventory controls only. No Go/verify/materialization/Docker/DB calls."""
import hashlib
import importlib.util
import os
from pathlib import Path
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location('readonly_namespace_inventory_controller', Path(__file__).with_name('run.py'))
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
    def test_exact_eight_and_twentyfour_require_real_integration_footer(self):
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
        self.assertEqual(totals, [8, 24])
        for resource, code in [({'status': 'monitor_error', 'exit_code': None}, 75),
                               ({'status': 'passed', 'exit_code': 0}, 1),
                               ({'status': 'stopped_task_allocation_gate', 'exit_code': -15}, 75)]:
            with self.assertRaises(ValueError):
                SUBJECT.require_guard(resource, code)

    def test_owned_inventory_fixture_and_zero_residuals_are_exact(self):
        env = {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux',
               'GITHUB_RUN_ID': '42', 'GITHUB_RUN_ATTEMPT': '1', 'GITHUB_SHA': 'a' * 40,
               'GOTOOLCHAIN': 'local', 'GOWORK': 'off',
               'LUOBO_NAMESPACE_INVENTORY_TEST_MONGO_URI': SUBJECT.MONGO,
               'LUOBO_NAMESPACE_INVENTORY_TEST_REDIS_URL': SUBJECT.REDIS,
               'LUOBO_NAMESPACE_INVENTORY_TEST_REPLICA_SET': 'luoboIntegration',
               'LUOBO_EMAIL_INTEGRATION': '0', 'LUOBO_RUN_LEGACY_LOAD_TESTS': '0', 'LUOBO_RUN_LEGACY_EXAMPLES': '0'}
        self.assertEqual(SUBJECT.fixture_environment(env), {k: v for k, v in env.items() if k.startswith('LUOBO_')})
        for key, value in [('LUOBO_NAMESPACE_INVENTORY_TEST_MONGO_URI', None),
                           ('LUOBO_NAMESPACE_INVENTORY_TEST_REDIS_URL', 'redis://127.0.0.1:36379/0'),
                           ('LUOBO_NAMESPACE_INVENTORY_TEST_REPLICA_SET', 'foreign'),
                           ('LUOBO_NAMESPACE_INVENTORY_TEST_MONGO_URI', 'mongodb://foreign/'),
                           ('LUOBO_RISK_TEST_MYSQL_FIXTURE', '/tmp/private'),
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
        zero = {'state': 'no_fixture_residue', 'mongo_user_database_count': 0,
                'redis_db15_dbsize_before_info': 0, 'redis_db15_key_count': 0,
                'redis_db15_expiring_keys': 0, 'unexpected_redis_database_count': 0}
        SUBJECT.require_zero_residuals(zero)
        for key in zero:
            bad = dict(zero)
            bad[key] = 'expiring_cache_residue_pending_container_disposal' if key == 'state' else 1
            with self.subTest(residue=key), self.assertRaises(ValueError):
                SUBJECT.require_zero_residuals(bad)
        with self.assertRaises(ValueError):
            SUBJECT.require_zero_residuals({})

    def test_nine_new_sources_cannot_override_parent_or_escape_namespace(self):
        targets = sorted(SUBJECT.NEW_TARGETS)
        self.assertEqual(len(targets), 9)
        original_repo = SUBJECT.REPO
        try:
            with tempfile.TemporaryDirectory() as temporary:
                SUBJECT.REPO = Path(temporary).resolve()
                overlay, after = [], {}
                for target in targets:
                    name = 'ci/candidates/namespace-inventory/source/' + target
                    updated = b'package candidate\n'
                    after[target] = updated
                    out = SUBJECT.REPO / name
                    out.parent.mkdir(parents=True, exist_ok=True)
                    out.write_bytes(updated)
                    out.chmod(0o644)
                    overlay.append({'path': name, 'target': target, 'bytes': len(updated),
                                    'sha256': hashlib.sha256(updated).hexdigest(), 'mode': '0o644',
                                    'beforeSHA256': None})
                before = {'server/old.go': b'package old\n'}
                self.assertEqual(SUBJECT.apply_overlay({'overlay': overlay}, before), {**before, **after})
                for change in ('new-was-present', 'nonempty-before', 'mode', 'duplicate', 'extra', 'foreign-path', 'missing'):
                    bad, previous = [dict(r) for r in overlay], dict(before)
                    changed_file = SUBJECT.REPO / overlay[0]['path']
                    extra = changed_file.with_name('extra.go')
                    if change == 'new-was-present':
                        previous[targets[0]] = b'old hidden source\n'
                    elif change == 'nonempty-before':
                        bad[0]['beforeSHA256'] = '0' * 64
                    elif change == 'mode':
                        changed_file.chmod(0o600)
                    elif change == 'duplicate':
                        bad[-1] = dict(bad[0])
                    elif change == 'extra':
                        extra.write_bytes(b'package extra\n')
                    elif change == 'foreign-path':
                        bad[0]['path'] = 'ci/candidates/group-receiver/source/' + targets[0]
                    else:
                        bad.pop()
                    with self.subTest(change=change), self.assertRaises(ValueError):
                        SUBJECT.apply_overlay({'overlay': bad}, previous)
                    changed_file.chmod(0o644)
                    if extra.exists():
                        extra.unlink()
        finally:
            SUBJECT.REPO = original_repo


if __name__ == '__main__':
    unittest.main(verbosity=2)
