#!/usr/bin/env python3
"""Incremental selection rejects false pass reuse and omitted real cases."""
import copy
import importlib.util
import json
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('original_lifecycle_incremental', Path(__file__).with_name('run.py'))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


class IncrementalSelectionControls(unittest.TestCase):
    def test_new_and_direct_existing_cases_run_without_replaying_unaffected_passes(self):
        c = json.loads(runner.INPUTS.read_bytes())
        previous = copy.deepcopy(c['priorPassedTargets'])
        runner.check_selection(previous, c)
        for label in ('replay-retained', 'omit-retained', 'omit-new', 'omit-affected',
                      'duplicate-selected', 'duplicate-retained', 'foreign-new',
                      'fabricated-pass', 'fabricated-failure', 'changed-impact',
                      'existing-as-new', 'existing-as-passed'):
            bad = copy.deepcopy(c)
            retained = next(iter(bad['reusedTargets']))
            if label == 'replay-retained':
                bad['targets'].setdefault(retained, []).append(bad['reusedTargets'][retained][0])
            elif label == 'omit-retained':
                bad['reusedTargets'][retained].pop()
            elif label in ('omit-new', 'omit-affected'):
                field = 'newTargets' if label == 'omit-new' else 'directlyAffectedTargets'
                package = next(iter(bad[field]))
                bad['targets'][package].remove(bad[field][package][0])
            elif label.startswith('duplicate-'):
                field = 'targets' if label == 'duplicate-selected' else 'reusedTargets'
                package = next(iter(bad[field]))
                bad[field][package].append(bad[field][package][0])
            elif label == 'foreign-new':
                package = next(iter(bad['newTargets']))
                bad['newTargets'][package][0] = 'TestUnreviewedReplacement'
            elif label == 'fabricated-pass':
                package = next(iter(bad['priorPassedTargets']))
                bad['priorPassedTargets'][package][0] = 'TestInventedPriorSuccess'
            elif label == 'fabricated-failure':
                bad['failedTargets'] = {'internal/msgtransfer': ['TestInventedOldFailure']}
            elif label == 'changed-impact':
                package = next(iter(bad['directlyAffectedTargets']))
                bad['directlyAffectedTargets'][package].pop()
            else:
                package = next(iter(bad['directExistingTargets']))
                name = bad['directExistingTargets'][package].pop()
                field = 'newTargets' if label == 'existing-as-new' else 'priorPassedTargets'
                bad[field].setdefault(package, []).append(name)
            with self.subTest(label=label), self.assertRaises(ValueError):
                runner.check_selection(previous, bad)


if __name__ == '__main__':
    unittest.main()
