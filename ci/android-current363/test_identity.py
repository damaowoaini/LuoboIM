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

    def test_fresh_core_mode_cannot_select_old_artifact_restore_or_old_context(self):
        context, inputs, tools = controller.metadata()
        self.assertEqual(context['core_build_mode'], 'fresh-fixed-source')
        self.assertFalse(context['restore_fallback'])
        self.assertIn('core', controller.STAGES)
        self.assertNotIn('restore', controller.STAGES)
        self.assertFalse(hasattr(controller.Job, 'restore'))
        self.assertIs(controller.Job.builder, controller.generic.old.Job.builder)
        self.assertEqual(controller.require_reviewed_consumer(context, controller.s.sha(HERE / 'cohort.json')), None)
        with self.assertRaises(ValueError):
            controller.require_reviewed_consumer(context, '0' * 64)


if __name__ == '__main__':
    unittest.main()
