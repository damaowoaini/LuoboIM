import importlib.util
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('effect_run', Path(__file__).with_name('run.py'))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)
PACKAGE = 'internal/groupeffects'


def events():
    return [{'Package': runner.PREFIX + PACKAGE, 'Action': action, **extra} for action, extra in [
        ('start', {}), ('run', {'Test': 'TestOne'}), ('run', {'Test': 'TestOne/actual_Mongo'}),
        ('pass', {'Test': 'TestOne/actual_Mongo'}), ('pass', {'Test': 'TestOne'}), ('pass', {})]]


class ReporterTests(unittest.TestCase):
    def check(self, e, code=0):
        return runner.audit(e, code, PACKAGE, ['TestOne'], 1)

    def test_complete_real_subtest_required(self):
        self.assertEqual(self.check(events())['counts'], {'top_pass': 1, 'sub_pass': 1})

    def test_missing_or_repeated_completion_rejected(self):
        for e in (events()[:-1], events()[:3] + events()[4:], events()[:4] + events()[3:]):
            with self.assertRaises(ValueError):
                self.check(e)

    def test_skipped_mongo_is_not_pass(self):
        e = events()
        e[3]['Action'] = 'skip'
        with self.assertRaises(ValueError):
            self.check(e)

    def test_foreign_and_after_footer_rejected(self):
        e = events()
        e[1]['Package'] = 'foreign'
        for bad in (e, events() + [events()[1]]):
            with self.assertRaises(ValueError):
                self.check(bad)

    def test_race_and_nonzero_exit_rejected(self):
        e = events()
        e.insert(2, {'Package': runner.PREFIX + PACKAGE, 'Action': 'output', 'Output': 'WARNING: DATA RACE'})
        for bad, code in ((e, 0), (events(), 1)):
            with self.assertRaises(ValueError):
                self.check(bad, code)

    def test_missing_required_inventory_rejected(self):
        with self.assertRaises(ValueError):
            runner.audit(events(), 0, PACKAGE, ['TestOne', 'TestMissingMongo'], 1)

    def test_capture_preserves_distinct_streams_and_child_failure(self):
        with tempfile.TemporaryDirectory() as temporary:
            out, err = Path(temporary) / 'out', Path(temporary) / 'err'
            args = [sys.executable, '-B', str(Path(__file__).with_name('capture.py')),
                    '--stdout', str(out), '--stderr', str(err), '--', sys.executable, '-c',
                    'import sys;sys.stdout.write("actual stdout");sys.stderr.write("actual stderr");sys.exit(7)']
            p = subprocess.run(args, capture_output=True, check=False)
            self.assertEqual(p.returncode, 7)
            self.assertEqual(out.read_text(), 'actual stdout')
            self.assertEqual(err.read_text(), 'actual stderr')
            self.assertEqual(p.stdout, b'')


if __name__ == '__main__':
    unittest.main()
