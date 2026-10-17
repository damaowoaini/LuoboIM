"""R6: two directly affected pure controls; third unchanged R5 control is reused."""
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
    def test_short_exact_gate_and_postcheck_prevent_any_large_after_failure(self):
        expected = SUBJECT.expected()
        self.assertEqual((len(expected['short']['tops']), len(expected['short']['subs'])), (3, 13))
        self.assertEqual(expected['large'], {'tops': [SUBJECT.LARGE], 'subs': []})
        for change in ['good', 'missing-new', 'missing-old-sub', 'duplicate-pass', 'skip', 'nonzero',
                       'race', 'missing-footer', 'foreign', 'post-failure', 'large-nonzero']:
            with self.subTest(change=change), tempfile.TemporaryDirectory() as folder:
                root = Path(folder).resolve()
                calls, posts = [], []

                def command(label, argv, timeout):
                    calls.append(label)
                    self.assertEqual(timeout, 900)
                    expression = '^(' + '|'.join(expected[label]['tops']) + ')$'
                    if label == 'short':
                        names = sorted(s.split('/', 1)[1] for s in expected[label]['subs'])
                        expression += '/^(' + '|'.join(names) + ')$'
                        self.assertNotIn('legacy-checkpoint', expression)
                        self.assertNotIn('TestNamespaceProgressContinuesAcrossEveryStage', expression)
                    self.assertEqual(argv, ['fixed-isolation', 'go', 'test', '-mod=readonly', '-p=1',
                        '-parallel=1', '-race', '-json', '-count=1', '-vet=off', '-timeout=600s',
                        '-run', expression, './internal/namespaceinventory'])
                    raw, code = events(label), 0
                    if label == 'short':
                        if change in ('missing-new', 'missing-old-sub'):
                            name = SUBJECT.NEW_TOPS[0] if change == 'missing-new' else 'TestNamespaceProgressMemoKeepsCustody/raw-tamper'
                            raw = [e for e in raw if e.get('Test') != name]
                        elif change == 'duplicate-pass':
                            raw.insert(2, dict(next(e for e in raw if e.get('Test') and e['Action'] == 'pass')))
                        elif change == 'skip':
                            next(e for e in raw if e.get('Test') and e['Action'] == 'pass')['Action'] = 'skip'
                        elif change == 'nonzero':
                            code = 1
                        elif change == 'race':
                            raw.insert(1, {'Package': raw[0]['Package'], 'Action': 'output', 'Output': 'WARNING: DATA RACE'})
                        elif change == 'missing-footer':
                            raw.pop()
                        elif change == 'foreign':
                            raw[1]['Test'] = SUBJECT.LARGE
                    elif change == 'large-nonzero':
                        code = 1
                    output = root / label
                    output.mkdir()
                    (output / 'stdout.jsonl').write_text(''.join(json.dumps(e) + '\n' for e in raw))
                    return output, code

                def post(label):
                    posts.append(label)
                    if change == 'post-failure':
                        raise ValueError('synthetic source changed')

                if change == 'post-failure':
                    with self.assertRaisesRegex(ValueError, 'synthetic source changed'):
                        SUBJECT.execute_phases(command, ['fixed-isolation'], post)
                    self.assertEqual(calls, ['short'])
                else:
                    result = SUBJECT.execute_phases(command, ['fixed-isolation'], post)
                    self.assertEqual(calls, ['short', 'large'] if change in ('good', 'large-nonzero') else ['short'])
                    self.assertEqual(list(result), calls)
                    self.assertEqual(all(v['accepted'] for v in result.values()), change == 'good')
                self.assertEqual(posts, calls)
                self.assertTrue((root / 'short/summary.json').is_file())
        with self.assertRaises(ValueError):
            SUBJECT.audit(events('short'), 0, 'large')

    def test_overlay_is_exact_three_modified_one_new_and_rejects_collisions_or_drift(self):
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
            self.assertEqual(len(SUBJECT.apply_overlay({'overlay': overlay}, previous, root)), 4)
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
