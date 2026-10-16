"""Pure Python/synthetic files only. No Gradle, Go, CLI, or network execution."""
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch
import zipfile

import gradle_selector_fixture as controls
import observed_bind as observed
import support as s


def job_at(root):
    job = object.__new__(observed.Job)
    job.w = root
    s.put(root / 'artifacts/core-current257-arm64.aar', b'synthetic_not_an_AAR')
    job.verify = Mock()
    return job


class SelectorSourceTests(unittest.TestCase):
    def test_real_parent_prepare_reused_only_I_changes_and_provenance_remains(self):
        with tempfile.TemporaryDirectory() as value:
            root = Path(value).resolve()
            job = job_at(root)
            s.mkdir(root / 'evidence')
            original = job.gradle('dependencies', offline=False) + [
                '--info', '-I', observed.parent.HERE / 'resolve.gradle', '--write-verification-metadata', 'sha256']
            log = root / 'evidence/synthetic-source.log'
            s.put(log, b'Downloading https://repo.maven.apache.org/maven2/fixture/synthetic/fixed/1.0/fixed-1.0.jar\n')
            s.put(root / 'gradle-cache/caches/modules-2/files-2.1/fixture.synthetic/fixed/1.0/synthetic-content-key/fixed-1.0.jar',
                  controls.jar_bytes())
            xml = root / 'source/app/android/gradle/verification-metadata.xml'
            s.put(xml, b'<verification-metadata><sha256 value="synthetic"/></verification-metadata>\n')
            calls = []
            def fake_command(_job, args, cwd, env, label, timeout, reject, stdin):
                calls.append((list(args), cwd, env, label, timeout, reject, stdin))
                return log
            catalog = (root / 'evidence/classpath-preseal/catalog.json', '1' * 64)
            with patch.object(observed.classpaths, 'verified_catalog', return_value=catalog), \
                    patch.object(observed.parent.Job, 'cmd', fake_command):
                job.gradle_prepare()
            self.assertEqual(len(calls), 1)
            expected = list(original)
            expected[expected.index('-I') + 1] = observed.HERE / 'resolve-observed.gradle'
            expected += ['-DluoboClasspathCatalog=' + str(catalog[0]),
                         '-DluoboClasspathCatalogSha256=' + catalog[1]]
            self.assertEqual(calls[0], (expected, root / 'source/app/android',
                observed.parent.environment(root, False), 'gradle-dependency-setup', 1800, None, None))
            self.assertEqual(job.verify.call_count, 2)
            self.assertIsNone(job._typed_resolver_dispatch)
            self.assertEqual((root / 'evidence/gradle-verification-metadata.xml').read_bytes(), xml.read_bytes())
            proof = json.loads((root / 'evidence/dependency-origin-audit.json').read_text())
            self.assertEqual(proof['verification_metadata_sha256'], s.sha(xml))
            self.assertFalse(proof['preexisting_hermetic_lock'])
            self.assertEqual(len(proof['artifacts']), 1)
            dispatch = json.loads((root / 'evidence/observed-resolver-dispatch.json').read_text())
            changed = [i for i, (a, b) in enumerate(zip(dispatch['original_argv'], dispatch['derived_argv'])) if a != b]
            self.assertEqual(changed, [dispatch['original_argv'].index('-I') + 1])
            self.assertEqual(dispatch['derived_argv'][-2:], expected[-2:])
            self.assertEqual(dispatch['catalog_sha256'], catalog[1])

    def test_dispatch_outside_parent_or_changed_command_is_zero_execution(self):
        for mode in ['outside', 'wrong-I', 'extra-arg', 'wrong-env']:
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as value:
                root = Path(value).resolve()
                job = job_at(root)
                args = job.gradle('dependencies', offline=False) + [
                    '--info', '-I', observed.parent.HERE / 'resolve.gradle', '--write-verification-metadata', 'sha256']
                env = observed.parent.environment(root, False)
                job._typed_resolver_dispatch = None if mode == 'outside' else False
                if mode == 'wrong-I':
                    args[args.index('-I') + 1] = root / 'wrong.gradle'
                if mode == 'extra-arg':
                    args.append('--refresh-dependencies')
                if mode == 'wrong-env':
                    env = {**env, 'SYNTHETIC_EXTRA': 'unexpected'}
                with patch.object(observed.parent.Job, 'cmd') as run:
                    with self.assertRaises(ValueError):
                        job.cmd(args, root / 'source/app/android', env, 'gradle-dependency-setup', 1800)
                    run.assert_not_called()

    def test_nine_inputs_are_local_and_captured_outputs_keep_exact_bytes(self):
        with tempfile.TemporaryDirectory() as value:
            root = Path(value).resolve()
            s.mkdir(root / 'evidence')
            cases = controls.prepare(root)
            self.assertEqual(len(cases), 9)
            self.assertEqual(sum(c['expected_rejection'] is not None for c in cases), 6)
            self.assertIn("api project(':plugin')", (Path(cases[0]['cwd']) / 'build.gradle').read_text())
            self.assertIn('fixture.synthetic.plugin:plugin:1.0-SNAPSHOT',
                          (Path(cases[1]['cwd']) / 'build.gradle').read_text())
            child_pom = Path(cases[3]['cwd']) / 'm2/fixture/synthetic/parent/1.0/parent-1.0.pom'
            self.assertIn('<artifactId>child</artifactId><version>1.0-SNAPSHOT', child_pom.read_text())
            with zipfile.ZipFile(io.BytesIO(controls.jar_bytes())) as archive:
                self.assertIsNone(archive.testzip())
            body = b'["synthetic_exact_body"]\n'
            for name in controls.OUTPUTS:
                s.put(root / 'evidence' / name, body)
            moved = controls.capture_outputs(root, cases[0]['cwd'])
            self.assertEqual(set(moved), set(controls.OUTPUTS))
            for name in controls.OUTPUTS:
                self.assertFalse((root / 'evidence' / name).exists())
                self.assertEqual((Path(cases[0]['cwd']) / 'evidence' / name).read_bytes(), body)
            source = root / 'evidence' / controls.OUTPUTS[0]
            source.symlink_to(Path(cases[0]['cwd']) / 'evidence' / controls.OUTPUTS[0])
            with self.assertRaises(ValueError):
                controls.capture_outputs(root, cases[1]['cwd'])
            with self.assertRaises(ValueError):
                controls.prepare(root)

    def test_negative_needs_typed_module_and_exact_rejection_not_project_name(self):
        with tempfile.TemporaryDirectory() as value:
            root = Path(value).resolve()
            s.mkdir(root / 'evidence')
            case = controls.prepare(root)[1]
            log = root / 'synthetic.log'
            row = {'kind': 'project', 'projectPath': ':plugin'}
            log.write_text('LUOBO_SELECTOR_REQUEST ' + json.dumps({'requested': row}) + '\n' + case['expected_rejection'])
            with self.assertRaisesRegex(ValueError, 'typed module selector'):
                controls.check(case, log, {})
            row = {'kind': 'module', 'group': case['group'], 'module': case['module'], 'version': case['version']}
            log.write_text('LUOBO_SELECTOR_REQUEST ' + json.dumps({'requested': row}) + '\nunrelated failure')
            with self.assertRaisesRegex(ValueError, 'exact real resolver rejection'):
                controls.check(case, log, {})
            log.write_text('LUOBO_SELECTOR_REQUEST ' + json.dumps({'requested': row}) + '\n' + case['expected_rejection'])
            self.assertTrue(controls.check(case, log, {})['requested_module_guard_observed'])
            with self.assertRaises(ValueError):
                controls.check(case, log, {'unexpected': {}})


if __name__ == '__main__':
    unittest.main()
