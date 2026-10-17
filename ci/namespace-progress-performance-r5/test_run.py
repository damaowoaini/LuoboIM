"""R8: one directly affected dispatch control and one bounded-profile control; no Go invocation."""
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location('namespace_progress_r5_controller', Path(__file__).with_name('run.py'))
SUBJECT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SUBJECT)


def events(label):
    selected = SUBJECT.expected()[label]
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


class R5ControllerTests(unittest.TestCase):
    def test_original_large_profiles_owned_outputs_then_diagnostics_without_overwriting_main_failure(self):
        self.assertEqual(SUBJECT.expected(), {'large': {'tops': [SUBJECT.LARGE], 'subs': []}})
        for change in ['good', 'missing-done', 'skip', 'nonzero', 'race', 'missing-footer',
                       'foreign', 'post-failure', 'diagnostic-failure']:
            with self.subTest(change=change), tempfile.TemporaryDirectory() as folder:
                root = Path(folder).resolve()
                profiles = root / 'profiles'
                profiles.mkdir(mode=0o700)
                calls, order = [], []

                def command(label, argv, timeout):
                    calls.append(label); order.append(label)
                    self.assertEqual(label, 'large')
                    self.assertEqual(timeout, 900)
                    self.assertEqual(argv, ['fixed-isolation', 'go', 'test', '-mod=readonly', '-p=1',
                        '-parallel=1', '-race', '-json', '-count=1', '-vet=off', '-timeout=600s',
                        '-run', '^(' + SUBJECT.LARGE + ')$',
                        '-cpuprofile', str(profiles / 'cpu.pprof'), '-memprofile', str(profiles / 'alloc.pprof'),
                        '-o', str(profiles / 'large.test'), './internal/namespaceinventory'])
                    raw, code = events('large'), 0
                    if change == 'missing-done':
                        raw = [e for e in raw if not (e.get('Test') == SUBJECT.LARGE and e['Action'] == 'pass')]
                    elif change == 'skip':
                        next(e for e in raw if e.get('Test') and e['Action'] == 'pass')['Action'] = 'skip'
                    elif change == 'nonzero':
                        code = 4
                    elif change == 'race':
                        raw.insert(1, {'Package': raw[0]['Package'], 'Action': 'output', 'Output': 'WARNING: DATA RACE'})
                    elif change == 'missing-footer':
                        raw.pop()
                    elif change == 'foreign':
                        raw[1]['Test'] = SUBJECT.INDEX_TOP
                    output = root / label
                    output.mkdir()
                    (output / 'stdout.jsonl').write_text(''.join(json.dumps(e) + '\n' for e in raw))
                    return output, code

                def post(label):
                    order.append('post-' + label)
                    if change == 'post-failure':
                        raise ValueError('synthetic source changed')

                def diagnose():
                    order.append('diagnostics')
                    # Main result must already be frozen, including failed exit.
                    observed = json.loads((root / 'large/summary.json').read_text())
                    self.assertEqual(observed['exit'], 4 if change == 'nonzero' else 0)
                    return {'accepted': change != 'diagnostic-failure'}

                if change == 'post-failure':
                    with self.assertRaisesRegex(ValueError, 'synthetic source changed'):
                        SUBJECT.execute_phases(command, ['fixed-isolation'], post, profiles, diagnose)
                    self.assertEqual(order, ['large', 'post-large'])
                else:
                    result = SUBJECT.execute_phases(command, ['fixed-isolation'], post, profiles, diagnose)
                    self.assertEqual(order, ['large', 'post-large', 'diagnostics', 'post-diagnostics'])
                    self.assertEqual(result['large']['accepted'], change in ('good', 'diagnostic-failure'))
                    self.assertEqual(SUBJECT.final_exit(result), 0 if change == 'good' else 4 if change == 'nonzero' else 1)
                self.assertEqual(calls, ['large'])
        with self.assertRaises(ValueError):
            SUBJECT.audit(events('large'), 0, 'short')

    def test_profile_reports_are_bounded_local_stable_and_never_publish_test_binary(self):
        for change in ['good', 'missing-cpu', 'invalid-cpu', 'oversized-cpu', 'symlink-cpu',
                       'oversized-report', 'tool-error', 'changed-profile', 'cross-profile-change', 'resource-stop']:
            with self.subTest(change=change), tempfile.TemporaryDirectory() as folder:
                root = Path(folder).resolve()
                profiles, reports = root / 'profiles', root / 'reports'
                profiles.mkdir(mode=0o700); reports.mkdir(mode=0o700)
                binary = profiles / 'large.test'
                binary.write_bytes(b'controlled test binary'); binary.chmod(0o755)
                for name in ['cpu', 'alloc']:
                    path = profiles / (name + '.pprof')
                    path.write_bytes(b'\x1f\x8b' + b'OS boundary profile fixture'); path.chmod(0o600)
                cpu = profiles / 'cpu.pprof'
                if change == 'missing-cpu':
                    cpu.unlink()
                elif change == 'invalid-cpu':
                    cpu.write_bytes(b'not gzip')
                elif change == 'oversized-cpu':
                    cpu.write_bytes(b'\x1f\x8b' + b'x' * SUBJECT.PROFILE_CONTRACT['profileMaxBytes'])
                elif change == 'symlink-cpu':
                    outside = root / 'outside.pprof'; outside.write_bytes(cpu.read_bytes())
                    cpu.unlink(); cpu.symlink_to(outside)
                calls = []

                def command(label, argv, timeout):
                    calls.append(label)
                    self.assertEqual(timeout, 60)
                    name = 'cpu' if label == 'pprof-cpu-top' else 'alloc'
                    flags = [] if name == 'cpu' else ['-sample_index=alloc_objects' if label == 'pprof-alloc-objects-top'
                                                     else '-sample_index=alloc_space']
                    self.assertEqual(argv, ['fixed-isolation', 'go', 'tool', 'pprof', '-top', '-cum',
                        '-nodecount=30', '-symbolize=none'] + flags + [str(binary), str(profiles / (name + '.pprof'))])
                    if change == 'resource-stop':
                        raise ValueError('synthetic guard stopped')
                    out = reports / label; out.mkdir()
                    (out / 'stdout.jsonl').write_bytes(b'x' * (SUBJECT.PROFILE_CONTRACT['reportMaxBytes'] + 1)
                        if change == 'oversized-report' else b'Type: cpu or allocs\nShowing controlled top view\n')
                    (out / 'stderr.log').write_bytes(b'')
                    (out / 'resource.json').write_text('{"status":"passed","exit_code":0}\n')
                    (out / 'guard.log').write_text('controlled guard\n')
                    if change == 'changed-profile':
                        (profiles / (name + '.pprof')).write_bytes(b'\x1f\x8bchanged')
                    elif change == 'cross-profile-change' and label == 'pprof-alloc-objects-top':
                        cpu.write_bytes(b'\x1f\x8bchanged after CPU view')
                    return out, 1 if change == 'tool-error' else 0

                destination = root / 'artifact-diagnostics'
                if change in ('changed-profile', 'cross-profile-change', 'resource-stop'):
                    message = ('changed its selected input body' if change == 'changed-profile' else
                               'changed an earlier selected profile body' if change == 'cross-profile-change' else
                               'synthetic guard stopped')
                    with self.assertRaisesRegex(ValueError, message):
                        SUBJECT.profile_diagnostics(command, ['fixed-isolation'], profiles, destination)
                    self.assertEqual(len(calls), 3 if change == 'cross-profile-change' else 1)
                else:
                    result = SUBJECT.profile_diagnostics(command, ['fixed-isolation'], profiles, destination)
                    self.assertEqual(result['accepted'], change == 'good')
                    self.assertFalse(result['testBinaryUploaded'])
                    self.assertEqual(len(calls), 2 if change in ('missing-cpu', 'invalid-cpu', 'oversized-cpu', 'symlink-cpu') else 3)
                    if change in ('missing-cpu', 'invalid-cpu', 'oversized-cpu', 'symlink-cpu'):
                        self.assertEqual(result['profiles']['cpu']['state'], 'missing_or_rejected')
                        self.assertNotIn('cpu-top', result['reports'])
                    elif change == 'oversized-report':
                        self.assertEqual(result['reports']['cpu-top']['stdout.jsonl']['state'], 'missing_or_rejected')
                    elif change == 'tool-error':
                        self.assertEqual(result['reports']['cpu-top']['exit'], 1)
                    if change == 'good':
                        self.assertEqual(set(result['reports']), {'cpu-top', 'alloc-objects-top', 'alloc-space-top'})
                        self.assertTrue(all(v['accepted'] for v in result['reports'].values()))
                        self.assertEqual((destination / 'cpu.pprof').read_bytes(), cpu.read_bytes())
                        self.assertEqual((destination / 'alloc.pprof').read_bytes(), (profiles / 'alloc.pprof').read_bytes())
                    else:
                        self.assertFalse((destination / 'cpu.pprof').exists())
                    for path in destination.iterdir():
                        self.assertLessEqual(path.stat().st_size, SUBJECT.PROFILE_CONTRACT['profileMaxBytes'])
                self.assertEqual(binary.read_bytes(), b'controlled test binary')
                self.assertFalse(any(p.name.endswith('.test') for p in destination.rglob('*')))

    def test_overlay_is_borrowed_R6_four_plus_formatted_R7_three_and_rejects_collisions_or_drift(self):
        old = b'// exact fixture prefix\n' + SUBJECT.FIXTURE_OLD + b'\n// original assertions unchanged\n'
        new = old.replace(SUBJECT.FIXTURE_OLD, SUBJECT.FIXTURE_NEW, 1)
        SUBJECT.check_fixture_change(old, new)
        for before, after in [(old, old), (old, new + b'// another change\n'),
                              (old + SUBJECT.FIXTURE_OLD, new), (b'no fixture', new)]:
            with self.assertRaises(ValueError):
                SUBJECT.check_fixture_change(before, after)
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder).resolve()
            previous = {name: b'package previous\n' for name in SUBJECT.MODIFIED}
            overlay = []
            for target in sorted(SUBJECT.OVERLAY_TARGETS):
                path = root / (SUBJECT.SOURCE_ROOT + target)
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(b'package candidate\n'); path.chmod(0o644)
                overlay.append({'path': str(path.relative_to(root)), 'target': target, 'bytes': path.stat().st_size,
                    'sha256': hashlib.sha256(path.read_bytes()).hexdigest(), 'mode': '0o644',
                    'beforeSHA256': hashlib.sha256(previous[target]).hexdigest() if target in previous else None})
            self.assertEqual(len(SUBJECT.apply_overlay({'overlay': overlay}, previous, root)), 7)
            for change in ['missing-modified', 'before-drift', 'new-collision', 'duplicate', 'extra', 'mode', 'wrong-lane', 'body']:
                bad, before = copy.deepcopy(overlay), dict(previous)
                n = next(i for i, r in enumerate(bad) if r['target'] in SUBJECT.NEW)
                path = root / bad[n]['path']; extra = path.with_name('unexpected.go')
                if change == 'missing-modified':
                    before.pop(next(iter(before)))
                elif change == 'before-drift':
                    before[next(iter(before))] += b'// drift\n'
                elif change == 'new-collision':
                    before[bad[n]['target']] = b'old collision\n'
                elif change == 'duplicate':
                    bad[-1] = dict(bad[0])
                elif change == 'extra':
                    extra.write_bytes(b'package unexpected\n')
                elif change == 'mode':
                    path.chmod(0o600)
                elif change == 'wrong-lane':
                    bad[n]['path'] = 'ci/namespace-progress-performance-r4/source/' + bad[n]['target']
                else:
                    path.write_bytes(b'package drift\n')
                with self.subTest(change=change), self.assertRaises(ValueError):
                    SUBJECT.apply_overlay({'overlay': bad}, before, root)
                path.write_bytes(b'package candidate\n'); path.chmod(0o644)
                if extra.exists():
                    extra.unlink()

    def test_real_materialized_postcheck_rejects_source_mode_lock_and_config_changes(self):
        with tempfile.TemporaryDirectory() as folder:
            work = Path(folder).resolve()
            bodies = {'server/go.mod': b'module synthetic\n', 'server/go.sum': b'synthetic exact lock\n',
                      'server/internal/namespaceinventory/source.go': b'package namespaceinventory\n'}
            template = b'public: example\n'
            config = work / 'server/config/config.yaml'
            config.parent.mkdir(parents=True)
            config.write_bytes(template)
            for name, body in bodies.items():
                path = work / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(body); path.chmod(0o644)
            c = {'sourceModes': {name: 0o644 for name in bodies}}
            self.assertEqual(SUBJECT.check_materialized(c, bodies, work, template), bodies)
            for change in ['source-body', 'source-mode', 'go.mod', 'go.sum', 'config']:
                path = work / ('server/internal/namespaceinventory/source.go' if change.startswith('source-')
                               else 'server/' + change if change in ('go.mod', 'go.sum') else 'server/config/config.yaml')
                original = path.read_bytes()
                if change == 'source-mode':
                    path.chmod(0o600)
                else:
                    path.write_bytes(original + b'changed\n')
                with self.subTest(change=change), self.assertRaises(ValueError):
                    SUBJECT.check_materialized(c, bodies, work, template)
                path.write_bytes(original); path.chmod(0o644)


if __name__ == '__main__':
    unittest.main(verbosity=2)
