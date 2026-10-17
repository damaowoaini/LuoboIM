#!/usr/bin/env python3
"""Only new batch-selection controls; unchanged audit/source-parent controls reused."""
import copy
import importlib.util
import json
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('failed_owned_api_batch', Path(__file__).with_name('run.py'))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


class IncrementalSelectionControls(unittest.TestCase):
    def test_actual_passes_are_retained_and_every_failed_unstarted_new_or_affected_case_is_selected(self):
        c = json.loads(runner.INPUTS.read_bytes())
        previous = copy.deepcopy(c['priorPassedTargets'])
        for field in ('failedTargets', 'unstartedTargets'):
            for p, names in c[field].items():
                previous.setdefault(p, []).extend(names)
        runner.check_selection(previous, c)
        for label in ('replay-retained', 'omit-retained', 'omit-failed', 'omit-unstarted',
                      'omit-new', 'omit-affected', 'duplicate', 'foreign'):
            bad = copy.deepcopy(c)
            retained = next(iter(bad['reusedTargets']))
            if label == 'replay-retained':
                bad['targets'].setdefault(retained, []).append(bad['reusedTargets'][retained][0])
            elif label == 'omit-retained':
                bad['reusedTargets'][retained].pop()
            elif label.startswith('omit-'):
                field = {'omit-failed': 'failedTargets', 'omit-unstarted': 'unstartedTargets',
                         'omit-new': 'newTargets', 'omit-affected': 'additionalAffectedTargets'}[label]
                p = next(iter(bad[field]))
                bad['targets'][p].remove(bad[field][p][0])
            elif label == 'duplicate':
                bad['reusedTargets'][retained].append(bad['reusedTargets'][retained][0])
            else:
                p = next(iter(bad['newTargets']))
                bad['newTargets'][p][0] = 'TestUnreviewedReplacement'
            with self.subTest(label=label), self.assertRaises(ValueError):
                runner.check_selection(previous, bad)


if __name__ == '__main__':
    unittest.main()
