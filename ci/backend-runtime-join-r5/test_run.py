#!/usr/bin/env python3
"""New incremental-selection controls; prior immutable audit controls are reused."""
import copy
import importlib.util
import json
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('owner_failure_gateway_batch', Path(__file__).with_name('run.py'))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


class IncrementalSelectionControls(unittest.TestCase):
    def test_actual_failures_and_changed_owner_cases_run_while_unaffected_passes_are_reused(self):
        c = json.loads(runner.INPUTS.read_bytes())
        previous = copy.deepcopy(c['priorPassedTargets'])
        for package, names in c['failedTargets'].items():
            previous.setdefault(package, []).extend(names)
        runner.check_selection(previous, c)
        for label in ('replay-retained', 'omit-retained', 'omit-failed', 'omit-new',
                      'omit-affected', 'duplicate', 'foreign-new', 'fabricated-pass', 'changed-impact'):
            bad = copy.deepcopy(c)
            retained = next(iter(bad['reusedTargets']))
            if label == 'replay-retained':
                bad['targets'].setdefault(retained, []).append(bad['reusedTargets'][retained][0])
            elif label == 'omit-retained':
                bad['reusedTargets'][retained].pop()
            elif label.startswith('omit-'):
                field = {'omit-failed': 'failedTargets', 'omit-new': 'newTargets',
                         'omit-affected': 'directlyAffectedTargets'}[label]
                package = next(iter(bad[field]))
                bad['targets'][package].remove(bad[field][package][0])
            elif label == 'duplicate':
                bad['reusedTargets'][retained].append(bad['reusedTargets'][retained][0])
            elif label == 'foreign-new':
                package = next(iter(bad['newTargets']))
                bad['newTargets'][package][0] = 'TestUnreviewedReplacement'
            elif label == 'fabricated-pass':
                package = next(iter(bad['priorPassedTargets']))
                bad['priorPassedTargets'][package][0] = 'TestInventedPriorSuccess'
            else:
                package = next(iter(bad['directlyAffectedTargets']))
                bad['directlyAffectedTargets'][package].pop()
            with self.subTest(label=label), self.assertRaises(ValueError):
                runner.check_selection(previous, bad)


if __name__ == '__main__':
    unittest.main()
