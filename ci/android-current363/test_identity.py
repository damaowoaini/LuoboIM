"""Current363 source admission only; never runs native tools or phases."""
import copy
import hashlib
import io
import json
import importlib.util
from pathlib import Path
import sys
import struct
import tempfile
import unittest
import zipfile
from unittest.mock import patch

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('android_current363_identity', HERE / 'run.py')
controller = importlib.util.module_from_spec(spec)
spec.loader.exec_module(controller)


class Current363SourceTests(unittest.TestCase):
    def test_exact_current_source_rejects_old_scope_and_partial_inventory(self):
        context, inputs, tools = controller.metadata()
        self.assertEqual((inputs['members'], context['business_members'], context['portable_members']), (539, 363, 415))
        self.assertEqual(context['source_ref'], controller.SOURCE_REF)
        self.assertEqual(context['locked_packages'], 178)
        for mode in ('old-ref', 'old-count', 'partial-source', 'restore-fallback'):
            c, value = copy.deepcopy(context), copy.deepcopy(inputs)
            if mode == 'old-ref': c['source_ref'] = '0e1cb1ae5f3edc3db8a0a94970b363a4cd6ae3b1'
            elif mode == 'old-count': value['members'] = 535
            elif mode == 'partial-source': value['files'].pop()
            else: c['restore_fallback'] = True
            with self.subTest(mode=mode), self.assertRaises(ValueError):
                controller.validate_inputs(value, c)

    def test_explicit_current_core_reuse_rejects_other_producer_and_fresh_bind(self):
        context, inputs, tools = controller.metadata()
        self.assertEqual(context['core_build_mode'], 'restore-verified-current363')
        self.assertEqual(controller.STAGES, ['setup','restore','preseal','gradle-setup','apk'])
        spec = controller.read(HERE / 'core-reuse.json')
        self.assertEqual(spec['parent']['run'], 42183531372)
        self.assertEqual(spec['source']['members'], 539)
        self.assertFalse(spec['producer_top_activation_included'])
        for case in ('old-producer', 'raw-sha', 'product-sha', 'missing-product', 'missing-marker', 'wrong-source', 'wrong-CA'):
            value = copy.deepcopy(spec)
            if case == 'old-producer': value['parent']['run'] = 38970232673
            elif case == 'raw-sha': value['parent']['artifacts'][0]['sha256'] = '0' * 64
            elif case == 'product-sha': value['product_pins'][0]['sha256'] = '0' * 64
            elif case == 'missing-product': value['product_pins'].pop()
            elif case == 'missing-marker': value['raw_markers'].pop()
            elif case == 'wrong-source': value['source']['members'] = 530
            else: value['publicCoreCASHA256'] = '0' * 64
            with self.subTest(case=case), self.assertRaises(ValueError):
                controller.core_restore.validate(value, context, inputs, tools)
        for name in ('build_core', 'builder'):
            with self.subTest(name=name), self.assertRaises(ValueError):
                getattr(controller.Job, name)(None)
        resolver = (HERE / 'resolve.gradle').read_text()
        self.assertIn('toolingWitness.scoped(rootForTooling,bouncyConfiguration)', resolver)
        self.assertNotIn('    def p=gradle.rootProject', resolver)
        self.assertEqual(controller.require_reviewed_consumer(context, controller.s.sha(HERE / 'cohort.json')), None)
        with self.assertRaises(ValueError):
            controller.require_reviewed_consumer(context, '0' * 64)


    def test_inherited_phase_fragment_and_unchanged_exclusive_writer_are_bound(self):
        fixture = controller.plugin_scope_fixture
        phase = fixture.phase_bytes(HERE)
        resolver = (HERE / 'resolve.gradle').read_text()
        self.assertIn("def richPhase = richPhaseFor(properties['luoboProductionDispatch'])", resolver)
        for forbidden in ('taskNames', 'System.', 'getenv', 'getProperty'):
            self.assertNotIn(forbidden, phase.decode())
        self.assertEqual(hashlib.sha256(fixture.writer_bytes(HERE)).hexdigest(),
            '4e65d325af40646f489734a518bdac97bb3f141f95739da434f682d6463d699b')
        self.assertIn('java.nio.file.StandardOpenOption.CREATE_NEW', fixture.writer_bytes(HERE).decode())
        # Actual Groovy assertions run inside the same existing three controls,
        # using the real extracted function and fixed Gradle StartParameter API.
        # This Python method verifies source binding, not Gradle execution.
        for contract in ("phaseFor('gradle-dependency-setup')=='metadata'",
                "phaseFor('assemble-debug')=='apk'", 'def included=outer.newBuild()',
                'assert included.taskNames.empty',
                'assert included.getSystemPropertiesArgs()==explicit',
                "missing.remove('luoboProductionDispatch')",
                "[null,'','dependencies','assembleDebug','unknown',false,['assemble-debug']]",
                "phaseFor(missing['luoboProductionDispatch'])"):
            self.assertIn(contract, fixture.INIT)
        with tempfile.TemporaryDirectory(prefix='current363-phase-source-') as directory:
            (Path(directory) / 'resolve.gradle').write_text(resolver + '\n')
            with self.assertRaises(ValueError):
                fixture.phase_bytes(Path(directory))


    def test_fixed_aapt2_tool_origin_and_native_structure_fail_closed(self):
        tool = controller.aapt2_tool
        policy = tool.load_policy(HERE)
        proof = {'all_actual_artifact_sources_accounted': True,
            'artifacts': [{'coordinate': tool.COORDINATE, 'filename': row['filename'],
                'bytes': row['bytes'], 'sha256': row['sha256'],
                'observed_sources': [row['url']]} for row in policy['files']]}
        self.assertEqual(len(tool.origin_rows(policy, proof)), 2)
        for case in ('body', 'URL', 'dual-source', 'classifier', 'duplicate', 'incomplete'):
            value = copy.deepcopy(proof)
            if case == 'body': value['artifacts'][1]['sha256'] = '0' * 64
            elif case == 'URL': value['artifacts'][1]['observed_sources'] = ['https://repo.maven.apache.org/unapproved.jar']
            elif case == 'dual-source': value['artifacts'][1]['observed_sources'].append(value['artifacts'][1]['observed_sources'][0])
            elif case == 'classifier': value['artifacts'][1]['filename'] = 'aapt2-9.1.1-14792394-osx.jar'
            elif case == 'duplicate': value['artifacts'].append(copy.deepcopy(value['artifacts'][1]))
            else: value['all_actual_artifact_sources_accounted'] = False
            with self.subTest(case=case), self.assertRaises(ValueError):
                tool.origin_rows(policy, value)
        # Tiny structure-only controls are not the pinned official executable.
        elf = bytearray(120)
        elf[:7] = b'\x7fELF\x02\x01\x01'
        struct.pack_into('<HHI', elf, 16, 2, 62, 1)
        struct.pack_into('<Q', elf, 32, 64)
        struct.pack_into('<HHH', elf, 52, 64, 56, 1)
        struct.pack_into('<IIQQQQQQ', elf, 64, 1, 5, 0, 0, 0, 120, 120, 4096)
        def packed(name, data):
            output = io.BytesIO()
            with zipfile.ZipFile(output, 'w') as archive:
                archive.writestr(name, data)
            return output.getvalue()
        synthetic = packed('aapt2', elf)
        self.assertNotEqual(tool.digest(synthetic), policy['files'][1]['sha256'])
        self.assertEqual(tool.archive(synthetic)['ELF']['machine'], 62)
        with self.assertRaises(ValueError): tool.archive(packed('../aapt2', elf))
        struct.pack_into('<H', elf, 18, 183)
        with self.assertRaises(ValueError): tool.archive(packed('aapt2', elf))


    def test_exact_app_debug_scope_retains_real_seven_gate_plan_and_rejects_other_targets(self):
        class Planned(Exception):
            pass
        class ReachedOrigin(Exception):
            pass
        with tempfile.TemporaryDirectory(prefix='current363-app-task-') as directory:
            job = object.__new__(controller.Job)
            job.w = Path(directory).resolve()
            artifact = job.w / 'artifacts/core-current257-arm64.aar'
            artifact.parent.mkdir(parents=True)
            artifact.write_bytes(b'synthetic gate-plan input, not a native AAR')
            artifact.with_suffix('.build.json').write_text(json.dumps({'qa_overlay_sha256': 'a' * 64}))
            (job.w / 'source/app/build').mkdir(parents=True)
            job.verify = lambda: None
            job.check_java_lock = lambda: None
            job.check_packages = lambda: None
            calls = []
            def capture(args, cwd, env, label, timeout=900, reject=None, stdin=None):
                calls.append((list(map(str, args)), label, timeout, reject))
                self.assertEqual(cwd, job.w / 'source/app/android')
                self.assertIn('--offline', args)
                self.assertEqual(args[-3:], ['--offline', '--dependency-verification', 'strict'])
                if label == 'assemble-debug':
                    raise Planned()
            job.cmd = capture
            # Execute the actual inherited gate-plan source; only tool dispatch
            # and namespace inspection are test doubles. Stop before acceptance.
            with patch.object(controller.parent, 'netns'), self.assertRaises(Planned):
                controller.parent.Job.build_apk(job)
            self.assertEqual([row[1] for row in calls], [
                'gate-assemblerelease', 'gate-assembleprofile', 'gate-assemble',
                'gate-missing-sha', 'gate-wrong-sha', 'gate-missing-manifest',
                'gate-wrong-overlay', 'assemble-debug'])
            for index, selector in enumerate(['assembleRelease', 'assembleProfile', 'assemble']):
                self.assertIn(selector, calls[index][0])
                self.assertEqual(calls[index][3], 'QA core requires assembleDebug')
            for row in calls[3:]:
                self.assertIn(':app:assembleDebug', row[0])
                self.assertNotIn('assembleDebug', row[0])
            self.assertEqual([row[2] for row in calls], [180] * 7 + [1800])
            self.assertEqual([row[3] for row in calls[:7]], [
                'QA core requires assembleDebug', 'QA core requires assembleDebug',
                'QA core requires assembleDebug',
                'QA core requires an explicit AAR path and lowercase SHA256.',
                'QA core AAR checksum mismatch.',
                'QA core requires its bounded, auditable build manifest.',
                'QA core build manifest does not match the reviewed debug trust artifact.'])
            self.assertIsNone(calls[-1][3])
            self.assertFalse(any(value.startswith('-PluoboQaCoreSha256=') for value in calls[3][0]))
            self.assertIn('-PluoboQaCoreSha256=' + '0' * 64, calls[4][0])
            alias = job.w / 'negative/missing-provenance.aar'
            for row in calls[5:7]:
                self.assertIn('-PluoboQaCoreAar=' + str(alias), row[0])
                self.assertIn('-PluoboQaCoreSha256=' + controller.s.sha(alias), row[0])
            self.assertEqual(json.loads(alias.with_suffix('.build.json').read_bytes())['qa_overlay_sha256'], '0' * 64)
            # Real cmd's exact argv guard must reject root/other project/non-Debug
            # targets before reaching any origin or native/tool requirement.
            env = controller.parent.environment(job.w, True)
            for selector in ['assembleDebug', ':audio_session:assembleDebug', ':app:assembleRelease']:
                bad = controller.parent.Job.gradle(job, selector)
                with self.subTest(selector=selector), self.assertRaises(ValueError):
                    controller.Job.cmd(job, bad, job.w / 'source/app/android', env, 'assemble-debug', 1800)
            with patch.object(controller.rich_origins, 'require_origin_binding', side_effect=ReachedOrigin):
                with self.assertRaises(ReachedOrigin):
                    controller.Job.cmd(job, job.gradle('assembleDebug'), job.w / 'source/app/android', env, 'assemble-debug', 1800)
            self.assertIn('dependencies', job.gradle('dependencies', offline=False))
            self.assertNotIn(':app:dependencies', job.gradle('dependencies', offline=False))


if __name__ == '__main__':
    unittest.main()
