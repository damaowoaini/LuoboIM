#!/usr/bin/env python3
"""Three prepared chunk/progress targeted controls only. No Go/verify/materialization/Docker/DB calls."""
import hashlib
import importlib.util
import os
from pathlib import Path
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location('readonly_namespace_chunk_controller', Path(__file__).with_name('run.py'))
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
    def test_exact_fourteen_and_thirtysix_require_all_owned_subs_and_footer(self):
        totals = [0, 0]
        for package, selected in SUBJECT.TARGETS.items():
            raw = events(package, selected)
            result = SUBJECT.audit(raw, 0, package, selected)
            totals[0] += result['topPASS']
            totals[1] += result['subPASS']
            for change in ('missing-run', 'missing-bounded-integration-sub', 'duplicate-pass', 'missing-footer', 'foreign-sub',
                           'skip', 'fail', 'race', 'post-footer', 'nonzero'):
                bad = [dict(r) for r in raw]
                code = 0
                if change == 'missing-run':
                    bad.pop(1)
                elif change == 'missing-bounded-integration-sub':
                    bounded = 'TestNamespaceChunksOwnedPagingAndPrivateResume/bounded-progress-private-entry'
                    if package == 'internal/namespaceinventory':
                        bad = [r for r in bad if r.get('Test') != bounded]
                    else:
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
        self.assertEqual(totals, [14, 36])
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

    def test_fourteen_overlay_requires_corrected_parent_and_only_one_existing_cli(self):
        targets = sorted(SUBJECT.OVERLAY_TARGETS)
        self.assertEqual(len(targets), 14)
        self.assertIn(SUBJECT.CLI_TARGET, targets)
        original_repo = SUBJECT.REPO
        try:
            with tempfile.TemporaryDirectory() as temporary:
                SUBJECT.REPO = Path(temporary).resolve()
                before = {SUBJECT.CLI_TARGET: b'package main\n// exact original CLI\n',
                          'server/unchanged.go': b'package unchanged\n'}
                overlay, after = [], {}
                for target in targets:
                    path = 'ci/candidates/namespace-chunk/source/' + target
                    body = b'package candidate\n'
                    out = SUBJECT.REPO / path
                    out.parent.mkdir(parents=True, exist_ok=True)
                    out.write_bytes(body)
                    out.chmod(0o644)
                    after[target] = body
                    overlay.append({'path':path, 'target':target, 'bytes':len(body),
                                    'sha256':hashlib.sha256(body).hexdigest(), 'mode':'0o644',
                                    'beforeSHA256':hashlib.sha256(before[target]).hexdigest() if target in before else None})
                self.assertEqual(SUBJECT.apply_overlay({'overlay':overlay},before), {**before,**after})
                for change in ('new-was-present','CLI-missing','CLI-wrong-before','new-has-before','mode',
                               'duplicate','extra','foreign-path','missing','body-drift'):
                    bad, previous = [dict(r) for r in overlay], dict(before)
                    new_index = next(i for i,r in enumerate(bad) if r['target'] != SUBJECT.CLI_TARGET)
                    cli_index = next(i for i,r in enumerate(bad) if r['target'] == SUBJECT.CLI_TARGET)
                    changed = SUBJECT.REPO / bad[new_index]['path']
                    extra = changed.with_name('extra.go')
                    if change == 'new-was-present':
                        previous[bad[new_index]['target']] = b'parent collision\n'
                    elif change == 'CLI-missing':
                        previous.pop(SUBJECT.CLI_TARGET)
                    elif change == 'CLI-wrong-before':
                        previous[SUBJECT.CLI_TARGET] += b'// drift\n'
                    elif change == 'new-has-before':
                        bad[new_index]['beforeSHA256'] = '0'*64
                    elif change == 'mode':
                        changed.chmod(0o600)
                    elif change == 'duplicate':
                        bad[-1] = dict(bad[0])
                    elif change == 'extra':
                        extra.write_bytes(b'package extra\n')
                    elif change == 'foreign-path':
                        bad[new_index]['path'] = 'ci/candidates/namespace-inventory/source/' + bad[new_index]['target']
                    elif change == 'missing':
                        bad.pop()
                    else:
                        changed.write_bytes(b'package changed\n')
                    with self.subTest(change=change),self.assertRaises(ValueError):
                        SUBJECT.apply_overlay({'overlay':bad}, previous)
                    changed.write_bytes(b'package candidate\n');changed.chmod(0o644)
                    if extra.exists():extra.unlink()
        finally:
            SUBJECT.REPO = original_repo


if __name__ == '__main__':
    unittest.main(verbosity=2)
