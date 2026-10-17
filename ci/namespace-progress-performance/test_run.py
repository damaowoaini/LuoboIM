#!/usr/bin/env python3
"""Only three new pure controller tests; no verify/materialization/Go/DB."""
import copy
import hashlib
import importlib.util
from pathlib import Path
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location('namespace_progress_performance_controller', Path(__file__).with_name('run.py'))
SUBJECT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SUBJECT)


def events():
    package = SUBJECT.PREFIX + SUBJECT.PACKAGE
    raw = [{'Package': package, 'Action': 'start'}]
    for top in SUBJECT.EXPECTED['tops']:
        raw.append({'Package': package, 'Action': 'run', 'Test': top})
        for sub in SUBJECT.EXPECTED['subs']:
            if sub.startswith(top + '/'):
                raw.extend([{'Package': package, 'Action': 'run', 'Test': sub},
                            {'Package': package, 'Action': 'pass', 'Test': sub}])
        raw.append({'Package': package, 'Action': 'pass', 'Test': top})
    raw.append({'Package': package, 'Action': 'pass'})
    return raw


class ControllerTests(unittest.TestCase):
    def test_exact_progress_six_seventeen_rejects_missing_memo_large_or_footer(self):
        raw = events()
        audit = lambda value, code=0: SUBJECT.audit(value, code, SUBJECT.PACKAGE, SUBJECT.EXPECTED)
        accepted = audit(raw)
        self.assertEqual((accepted['topPASS'], accepted['subPASS']), (6, 17))
        large = 'TestNamespaceProgressLargeHistoryHasNoFinalFullSweep'
        memo_sub = 'TestNamespaceProgressMemoKeepsCustody/raw-tamper'
        for change in ['missing-large', 'missing-memo-sub', 'missing-run', 'duplicate-pass', 'foreign-chunk',
                       'missing-footer', 'skip', 'fail', 'race', 'panic-timeout', 'post-footer', 'nonzero']:
            bad = copy.deepcopy(raw)
            code = 0
            if change in ['missing-large', 'missing-memo-sub']:
                missing = large if change == 'missing-large' else memo_sub
                bad = [event for event in bad if event.get('Test') != missing]
            elif change == 'missing-run':
                bad.pop(1)
            elif change == 'duplicate-pass':
                at = next(n for n, event in enumerate(bad) if event.get('Test') and event['Action'] == 'pass')
                bad.insert(at, dict(bad[at]))
            elif change == 'foreign-chunk':
                bad[1]['Test'] = 'TestNamespaceChunksOwnedPagingAndPrivateResume'
            elif change == 'missing-footer':
                bad.pop()
            elif change in ['skip', 'fail']:
                at = next(n for n, event in enumerate(bad) if event.get('Test') and event['Action'] == 'pass')
                bad[at]['Action'] = change
            elif change in ['race', 'panic-timeout']:
                bad.insert(1, {'Package': raw[0]['Package'], 'Action': 'output',
                               'Output': 'WARNING: DATA RACE' if change == 'race' else 'panic: test timed out after 10m0s'})
            elif change == 'post-footer':
                bad.append({'Package': raw[0]['Package'], 'Action': 'output'})
            else:
                code = 1
            with self.subTest(change=change), self.assertRaises(ValueError):
                audit(bad, code)

    def test_exact_two_modified_two_new_overlay_keeps_parent_before_and_modes(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder).resolve()
            parent = {target: b'package previous\n' for target in SUBJECT.MODIFIED}
            overlay = []
            for target in sorted(SUBJECT.OVERLAY_TARGETS):
                relative = SUBJECT.SOURCE_ROOT + target
                path = root / relative
                path.parent.mkdir(parents=True, exist_ok=True)
                body = b'package candidate\n'
                path.write_bytes(body)
                path.chmod(0o644)
                overlay.append({'path': relative, 'target': target, 'bytes': len(body),
                                'sha256': hashlib.sha256(body).hexdigest(), 'mode': '0o644',
                                'beforeSHA256': hashlib.sha256(parent[target]).hexdigest() if target in parent else None})
            self.assertEqual(len(SUBJECT.apply_overlay({'overlay': overlay}, parent, root)), 4)
            for change in ['modified-missing', 'modified-drift', 'new-was-present', 'new-has-before', 'mode',
                           'duplicate', 'extra', 'foreign-lane', 'missing', 'body-drift']:
                bad, before = copy.deepcopy(overlay), dict(parent)
                new_at = next(n for n, row in enumerate(bad) if row['target'] in SUBJECT.NEW)
                changed = root / bad[new_at]['path']
                extra = changed.with_name('unexpected.go')
                if change == 'modified-missing':
                    before.pop(next(iter(SUBJECT.MODIFIED)))
                elif change == 'modified-drift':
                    before[next(iter(SUBJECT.MODIFIED))] += b'// drift\n'
                elif change == 'new-was-present':
                    before[bad[new_at]['target']] = b'old collision\n'
                elif change == 'new-has-before':
                    bad[new_at]['beforeSHA256'] = '0' * 64
                elif change == 'mode':
                    changed.chmod(0o600)
                elif change == 'duplicate':
                    bad[-1] = dict(bad[0])
                elif change == 'extra':
                    extra.write_bytes(b'package unexpected\n')
                elif change == 'foreign-lane':
                    bad[new_at]['path'] = 'ci/candidates/namespace-chunk/source/' + bad[new_at]['target']
                elif change == 'missing':
                    bad.pop()
                else:
                    changed.write_bytes(b'package drift\n')
                with self.subTest(change=change), self.assertRaises(ValueError):
                    SUBJECT.apply_overlay({'overlay': bad}, before, root)
                changed.write_bytes(b'package candidate\n')
                changed.chmod(0o644)
                if extra.exists():
                    extra.unlink()

    def test_pure_environment_isolation_and_resource_fail_closed(self):
        base = {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux',
                'GOTOOLCHAIN': 'local', 'GOWORK': 'off', 'GITHUB_RUN_ID': '123', 'GITHUB_RUN_ATTEMPT': '1',
                'GITHUB_SHA': 'a' * 40, 'PATH': '/usr/bin:/bin', 'HOME': '/original-home', **SUBJECT.ZERO_FLAGS}
        SUBJECT.hosted_environment(base)
        for key, value in [('LUOBO_NAMESPACE_INVENTORY_TEST_MONGO_URI', 'synthetic-forbidden'),
                           ('GITHUB_RUN_ATTEMPT', ''), ('GITHUB_SHA', 'not-a-commit'),
                           ('LUOBO_EMAIL_INTEGRATION', '1')]:
            with self.subTest(key=key), self.assertRaises(ValueError):
                SUBJECT.hosted_environment(dict(base, **{key: value}))
        env = SUBJECT.test_environment(base, Path('/owned'), '/fixed-go')
        self.assertEqual(env['HOME'], base['HOME'])
        self.assertEqual((env['GOPROXY'], env['GOSUMDB'], env['GOAUTH']), ('off', 'off', 'off'))
        self.assertFalse(any(key.startswith('GITHUB_') for key in env))
        command = SUBJECT.isolation_command(env, 1001, 1002)
        self.assertEqual(command[:6], ['sudo', '-n', 'unshare', '--net', '--', 'setpriv'])
        self.assertIn('--reuid=1001', command)
        self.assertIn('--regid=1002', command)
        self.assertIn('--clear-groups', command)
        with self.assertRaises(ValueError):
            SUBJECT.isolation_command(env, 0, 0)
        healthy = {'status': 'passed', 'exit_code': 0, 'minimum_free_required_bytes': 1 << 30,
                   'max_allocated_allowed_bytes': 16 << 30, 'minimum_observed_free_bytes': 2 << 30,
                   'maximum_observed_task_allocated_bytes': 1 << 20}
        SUBJECT.require_guard(healthy, 0)
        for key, value in [('status', 'infrastructure_failed'), ('status', 'stopped_timeout'), ('exit_code', None),
                           ('minimum_observed_free_bytes', (1 << 30) - 1),
                           ('maximum_observed_task_allocated_bytes', (16 << 30) + 1)]:
            with self.subTest(key=key, value=value), self.assertRaises(ValueError):
                SUBJECT.require_guard(dict(healthy, **{key: value}), 0)


if __name__ == '__main__':
    unittest.main(verbosity=2)
