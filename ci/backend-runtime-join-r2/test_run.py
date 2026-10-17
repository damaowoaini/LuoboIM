#!/usr/bin/env python3
"""Only new repair input/selection controls; existing audit4 is reused."""
import copy
import importlib.util
import json
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('repair_controller', Path(__file__).with_name('run.py'))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


class RepairControls(unittest.TestCase):
    def test_repair_requires_both_exact_existing_parents(self):
        paths = ['server/internal/messagedelivery/modifier_custody.go', 'server/internal/namespaceimport/ownedcontrol/launcher_linux.go']
        previous = {name: name.encode() for name in paths}
        config = {'overlay': [{'target': name, 'path': runner.SOURCE_ROOT + name, 'beforeSHA256': runner.sha(previous[name])} for name in paths]}
        with patch.object(runner, 'pinned', return_value=b'changed'):
            self.assertEqual(set(runner.apply_repair(config, previous)), set(paths))
            for label in ('missing', 'duplicate', 'foreign', 'wrong-parent'):
                bad = copy.deepcopy(config)
                if label == 'missing':
                    bad['overlay'].pop()
                elif label == 'duplicate':
                    bad['overlay'].append(copy.deepcopy(bad['overlay'][0]))
                elif label == 'foreign':
                    bad['overlay'][0]['target'] = 'server/internal/unrelated.go'
                else:
                    bad['overlay'][0]['beforeSHA256'] = '0' * 64
                with self.subTest(label=label), self.assertRaises(ValueError):
                    runner.apply_repair(bad, previous)

    def test_completed_old_tops_cannot_be_silently_replayed_or_discarded(self):
        c = json.loads(runner.INPUTS.read_bytes())
        original = {p: list(names) for p, names in c['targets'].items()}
        for p, names in c['reusedTargets'].items():
            original.setdefault(p, []).extend(names)
        runner.check_selection(original, c['targets'], c['reusedTargets'])
        for label in ('replay', 'omit', 'duplicate', 'foreign'):
            selected, reused = copy.deepcopy(c['targets']), copy.deepcopy(c['reusedTargets'])
            p = next(iter(reused))
            if label == 'replay':
                selected.setdefault(p, []).append(reused[p][0])
            elif label == 'omit':
                reused[p].pop()
            elif label == 'duplicate':
                reused[p].append(reused[p][0])
            else:
                reused[p][0] = 'TestForeign'
            with self.subTest(label=label), self.assertRaises(ValueError):
                runner.check_selection(original, selected, reused)


if __name__ == '__main__':
    unittest.main()
