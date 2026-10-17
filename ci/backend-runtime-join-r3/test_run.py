#!/usr/bin/env python3
"""New source-parent and incremental selection controls; original audit reused."""
import copy
import importlib.util
import json
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('batch_controller', Path(__file__).with_name('run.py'))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


class IncrementalBatchControls(unittest.TestCase):
    def test_composition_refuses_missing_duplicate_foreign_or_colliding_source_parents(self):
        c = json.loads(runner.INPUTS.read_bytes())
        previous = {r['target']: r['target'].encode() for r in c['overlay'] if r['beforeSHA256'] is not None}
        config = {'overlay': [{'target': r['target'], 'path': runner.SOURCE_ROOT + r['target'],
                               'beforeSHA256': runner.sha(previous[r['target']]) if r['target'] in previous else None}
                              for r in c['overlay']]}
        existing = next(i for i, r in enumerate(config['overlay']) if r['beforeSHA256'] is not None)
        added = next(i for i, r in enumerate(config['overlay']) if r['beforeSHA256'] is None)
        with patch.object(runner, 'pinned', return_value=b'accepted-source'):
            self.assertEqual(set(runner.apply_overlay(config, previous)), runner.ALLOWED)
            for label in ('missing', 'duplicate', 'foreign', 'collision', 'lost-existing', 'wrong-parent'):
                bad, before = copy.deepcopy(config), dict(previous)
                if label == 'missing':
                    bad['overlay'].pop()
                elif label == 'duplicate':
                    bad['overlay'].append(copy.deepcopy(bad['overlay'][0]))
                elif label == 'foreign':
                    bad['overlay'][0]['target'] = 'server/unrelated.go'
                elif label == 'collision':
                    before[bad['overlay'][added]['target']] = b'preexisting-foreign-source'
                elif label == 'lost-existing':
                    del before[bad['overlay'][existing]['target']]
                else:
                    bad['overlay'][existing]['beforeSHA256'] = '0' * 64
                with self.subTest(label=label), self.assertRaises(ValueError):
                    runner.apply_overlay(bad, before)

    def test_selection_keeps_all_failed_affected_new_cases_and_reuses_other_completed_cases(self):
        c = json.loads(runner.INPUTS.read_bytes())
        previous = copy.deepcopy(c['priorPassedTargets'])
        for p, names in c['failedTargets'].items():
            previous.setdefault(p, []).extend(names)
        runner.check_selection(previous, c)
        for label in ('replay-old', 'omit-old', 'omit-failure', 'omit-affected', 'duplicate', 'foreign'):
            bad = copy.deepcopy(c)
            p = next(iter(bad['reusedTargets']))
            if label == 'replay-old':
                bad['targets'].setdefault(p, []).append(bad['reusedTargets'][p][0])
            elif label == 'omit-old':
                bad['reusedTargets'][p].pop()
            elif label == 'omit-failure':
                package = next(iter(bad['failedTargets']))
                bad['targets'][package].remove(bad['failedTargets'][package][0])
            elif label == 'omit-affected':
                package = next(iter(bad['affectedTargets']))
                bad['targets'][package].remove(bad['affectedTargets'][package][0])
            elif label == 'duplicate':
                bad['reusedTargets'][p].append(bad['reusedTargets'][p][0])
            else:
                bad['newTargets'][next(iter(bad['newTargets']))][0] = 'TestUnreviewed'
            with self.subTest(label=label), self.assertRaises(ValueError):
                runner.check_selection(previous, bad)


if __name__ == '__main__':
    unittest.main()
