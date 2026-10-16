"""Prepared controller-only tests; no Go, fixture, module download or network."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('stage3_runner', Path(__file__).with_name('run.py'))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


class Stage3Evidence(unittest.TestCase):
    def spec(self, legacy=False):
        return {'top_tests':['TestRequired', 'TestLegacy'],
                'required_leaves':['TestRequired/positive'],
                'allowed_legacy_skips':['TestLegacy'] if legacy else [],
                'expected_sub':1}

    def events(self):
        package = runner.PREFIX + 'internal/rpc/group'
        return [{'Package':package, 'Action':a, **({'Test':n} if n else {})}
                for a, n in [('run','TestRequired'), ('run','TestRequired/positive'),
                             ('pass','TestRequired/positive'), ('pass','TestRequired'),
                             ('run','TestLegacy'), ('skip','TestLegacy'), ('pass',None)]]

    def test_legacy_skip_is_reported_without_calling_it_an_integration_pass(self):
        summary = runner.audit(self.events(), 0, 'internal/rpc/group', self.spec(True))
        self.assertTrue(summary['accepted'])
        self.assertEqual(summary['legacy_skips'], ['TestLegacy'])
        self.assertEqual(summary['counts'], {'top_pass':1, 'sub_pass':1, 'top_skip':1})

    def test_required_skip_fail_or_missing_positive_is_rejected_and_counted(self):
        self.assertFalse(runner.audit(self.events(), 0, 'internal/rpc/group', self.spec())['accepted'])
        for action in ('skip','fail'):
            with self.subTest(action=action):
                events = self.events()
                events[2]['Action'] = action
                summary = runner.audit(events, 1 if action == 'fail' else 0,
                                       'internal/rpc/group', self.spec(True))
                self.assertFalse(summary['accepted'])
                self.assertIn('TestRequired/positive', summary['missing_required_leaves'])
                self.assertEqual(summary['counts']['sub_' + action], 1)

    def test_partial_poison_foreign_and_post_terminal_never_pass(self):
        self.assertFalse(runner.audit(self.events()[:-1], 0, 'internal/rpc/group', self.spec(True))['accepted'])
        for output in ('panic: synthetic', 'WARNING: DATA RACE', '[build failed]', 'test timed out'):
            events = self.events()
            events.insert(1, {'Package':runner.PREFIX + 'internal/rpc/group',
                              'Action':'output', 'Output':output})
            self.assertFalse(runner.audit(events, 0, 'internal/rpc/group', self.spec(True))['accepted'])
        for events in (self.events() + self.events()[-1:],
                       [{'Package':'foreign','Action':'pass'}],
                       self.events()[:1] + self.events()):
            with self.assertRaises(ValueError):
                runner.audit(events, 0, 'internal/rpc/group', self.spec(True))

    def test_no_test_files_is_only_compile_coverage_for_an_empty_inventory(self):
        package = 'pkg/common/db/table/relation'
        events = [{'Package':runner.PREFIX + package, 'Action':'output', 'Output':'[no test files]\n'},
                  {'Package':runner.PREFIX + package, 'Action':'skip'}]
        summary = runner.audit(events, 0, package,
                               {'top_tests':[], 'required_leaves':[], 'allowed_legacy_skips':[]})
        self.assertTrue(summary['accepted'])
        self.assertTrue(summary['compile_only_package'])
        self.assertEqual(summary['counts'], {})
        self.assertFalse(runner.audit(events, 0, package, self.spec(True))['accepted'])

    def test_missing_environment_is_controller_rejection_before_any_fixture_access(self):
        with patch.dict(runner.os.environ, {}, clear=True):
            with self.assertRaises(ValueError):
                runner.cloud_environment()

    def test_alias_source_path_and_duplicate_receipt_are_refused(self):
        for path in ('../server/a.go','/server/a.go','server//a.go','server/./a.go','server\\a.go',''):
            with self.subTest(path=path), self.assertRaises(ValueError):
                runner.name(path)
        with tempfile.TemporaryDirectory(prefix='luobo_stage3_controller_') as temp:
            target = Path(temp) / 'receipt.json'
            runner.write(target, {'state':'partial'})
            original = target.read_bytes()
            with self.assertRaises(FileExistsError):
                runner.write(target, {'state':'passed'})
            self.assertEqual(target.read_bytes(), original)
            self.assertEqual(json.loads(original), {'state':'partial'})


if __name__ == '__main__':
    unittest.main()
