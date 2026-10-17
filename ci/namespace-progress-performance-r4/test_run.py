"""Only three new pure R4 controller controls; no Go, database or network."""
import copy
import hashlib
import importlib.util
from pathlib import Path
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location('namespace_progress_r4_controller', Path(__file__).with_name('run.py'))
SUBJECT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SUBJECT)


def events():
    selected = SUBJECT.expected()
    package = SUBJECT.PREFIX + SUBJECT.PACKAGE
    result = [{'Package': package, 'Action': 'start'}]
    for top in selected['tops']:
        result.append({'Package': package, 'Action': 'run', 'Test': top})
        for sub in selected['subs']:
            if sub.startswith(top + '/'):
                result.extend([{'Package': package, 'Action': 'run', 'Test': sub},
                               {'Package': package, 'Action': 'pass', 'Test': sub}])
        result.append({'Package': package, 'Action': 'pass', 'Test': top})
    result.append({'Package': package, 'Action': 'pass'})
    return result


class R4ControllerTests(unittest.TestCase):
    def test_exact_eleven_fortynine_rejects_every_missing_old_or_new_case_and_foreign_events(self):
        raw = events()
        audit = lambda value, code=0: SUBJECT.audit(value, code, SUBJECT.PACKAGE, SUBJECT.expected())
        good = audit(raw)
        self.assertEqual((good['topPASS'], good['subPASS']), (11, 49))
        selected = SUBJECT.expected()
        self.assertEqual((len(selected['tops']), len(selected['subs'])), (11, 49))
        self.assertTrue(set(SUBJECT.parent().expected()['tops']).issubset(selected['tops']))
        self.assertTrue(set(SUBJECT.parent().expected()['subs']).issubset(selected['subs']))
        for missing in selected['tops'] + selected['subs']:
            with self.subTest(missing=missing), self.assertRaises(ValueError):
                audit([e for e in raw if e.get('Test') != missing])
        for change in ['missing-run', 'duplicate-pass', 'skip', 'fail', 'foreign-chunk', 'extra-package',
                       'race', 'panic-timeout', 'missing-footer', 'post-footer', 'nonzero', 'old-only-selection']:
            bad, code = copy.deepcopy(raw), 0
            if change == 'missing-run':
                bad.pop(1)
            elif change == 'duplicate-pass':
                bad.insert(2, dict(next(e for e in bad if e.get('Test') and e['Action'] == 'pass')))
            elif change in ('skip', 'fail'):
                next(e for e in bad if e.get('Test') and e['Action'] == 'pass')['Action'] = change
            elif change == 'foreign-chunk':
                bad[1]['Test'] = 'TestNamespaceChunksOwnedPagingAndPrivateResume'
            elif change == 'extra-package':
                bad[1]['Package'] += '/foreign'
            elif change in ('race', 'panic-timeout'):
                bad.insert(1, {'Package': raw[0]['Package'], 'Action': 'output',
                               'Output': 'WARNING: DATA RACE' if change == 'race' else 'panic: test timed out after 10m0s'})
            elif change == 'missing-footer':
                bad.pop()
            elif change == 'post-footer':
                bad.append(dict(raw[0]))
            elif change == 'old-only-selection':
                with self.assertRaises(ValueError):
                    SUBJECT.audit(raw, 0, SUBJECT.PACKAGE, SUBJECT.parent().expected())
                continue
            else:
                code = 1
            with self.subTest(change=change), self.assertRaises(ValueError):
                audit(bad, code)

    def test_three_modified_two_new_overlay_rejects_parent_drift_collisions_modes_and_extra_sources(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder).resolve()
            parent = {name: b'package previous\n' for name in SUBJECT.MODIFIED}
            overlay = []
            for target in sorted(SUBJECT.OVERLAY_TARGETS):
                relative = SUBJECT.SOURCE_ROOT + target
                path = root / relative
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(b'package candidate\n'); path.chmod(0o644)
                overlay.append({'path': relative, 'target': target, 'bytes': path.stat().st_size,
                                'sha256': hashlib.sha256(path.read_bytes()).hexdigest(), 'mode': '0o644',
                                'beforeSHA256': hashlib.sha256(parent[target]).hexdigest() if target in parent else None})
            self.assertEqual(len(SUBJECT.apply_overlay({'overlay': overlay}, parent, root)), 5)
            for change in ['modified-missing', 'modified-drift', 'new-present', 'new-before', 'mode',
                           'duplicate', 'extra', 'missing', 'wrong-lane', 'body-drift']:
                bad, before = copy.deepcopy(overlay), dict(parent)
                at = next(n for n, r in enumerate(bad) if r['target'] in SUBJECT.NEW)
                changed = root / bad[at]['path']; extra = changed.with_name('unexpected.go')
                if change == 'modified-missing':
                    before.pop(next(iter(before)))
                elif change == 'modified-drift':
                    before[next(iter(before))] += b'// drift\n'
                elif change == 'new-present':
                    before[bad[at]['target']] = b'old collision\n'
                elif change == 'new-before':
                    bad[at]['beforeSHA256'] = '0' * 64
                elif change == 'mode':
                    changed.chmod(0o600)
                elif change == 'duplicate':
                    bad[-1] = dict(bad[0])
                elif change == 'extra':
                    extra.write_bytes(b'package unexpected\n')
                elif change == 'missing':
                    bad.pop()
                elif change == 'wrong-lane':
                    bad[at]['path'] = 'ci/namespace-progress-performance-r3/source/' + bad[at]['target']
                else:
                    changed.write_bytes(b'package drift\n')
                with self.subTest(change=change), self.assertRaises(ValueError):
                    SUBJECT.apply_overlay({'overlay': bad}, before, root)
                changed.write_bytes(b'package candidate\n'); changed.chmod(0o644)
                if extra.exists():
                    extra.unlink()

    def test_original_limits_clean_environment_and_empty_netns_reject_anomalies(self):
        base = {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux',
                'GOTOOLCHAIN': 'local', 'GOWORK': 'off', 'GITHUB_RUN_ID': '123', 'GITHUB_RUN_ATTEMPT': '1',
                'GITHUB_SHA': 'a' * 40, 'PATH': '/usr/bin:/bin', 'HOME': '/actual-home', **SUBJECT.ZERO_FLAGS}
        SUBJECT.hosted_environment(base)
        for key, value in [('LUOBO_NAMESPACE_INVENTORY_TEST_MONGO_URI', 'synthetic-forbidden'),
                           ('GITHUB_RUN_ATTEMPT', ''), ('GITHUB_SHA', 'not-a-commit'),
                           ('LUOBO_EMAIL_INTEGRATION', '1')]:
            with self.subTest(key=key), self.assertRaises(ValueError):
                SUBJECT.hosted_environment(dict(base, **{key: value}))
        env = SUBJECT.test_environment(base, Path('/owned'), '/fixed-go')
        self.assertEqual(env['HOME'], base['HOME'])
        self.assertFalse(any(k.startswith('GITHUB_') for k in env))
        command = SUBJECT.isolation_command(env, 1001, 1002)
        self.assertEqual(command[:6], ['sudo', '-n', 'unshare', '--net', '--', 'setpriv'])
        self.assertIn('--reuid=1001', command)
        self.assertIn('--regid=1002', command)
        self.assertIn('--clear-groups', command)
        for key in ('GOPROXY', 'GOSUMDB', 'GOAUTH', 'GOWORK', 'GOENV'):
            with self.subTest(key=key), self.assertRaises(ValueError):
                SUBJECT.isolation_command(dict(env, **{key: 'unexpected'}), 1001, 1002)
        healthy = {'status': 'passed', 'exit_code': 0, 'minimum_free_required_bytes': 1 << 30,
                   'max_allocated_allowed_bytes': 16 << 30, 'minimum_observed_free_bytes': 2 << 30,
                   'maximum_observed_task_allocated_bytes': 1 << 20}
        SUBJECT.require_guard(healthy, 0)
        for key, value in [('status', 'infrastructure_failed'), ('status', 'stopped_timeout'), ('exit_code', None),
                           ('minimum_observed_free_bytes', (1 << 30) - 1),
                           ('maximum_observed_task_allocated_bytes', (16 << 30) + 1)]:
            with self.subTest(key=key), self.assertRaises(ValueError):
                SUBJECT.require_guard(dict(healthy, **{key: value}), 0)


if __name__ == '__main__':
    unittest.main(verbosity=2)
