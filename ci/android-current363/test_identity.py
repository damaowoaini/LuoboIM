"""Current363 source admission only; never runs native tools or phases."""
import copy
import importlib.util
from pathlib import Path
import sys
import unittest

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


if __name__ == '__main__':
    unittest.main()
