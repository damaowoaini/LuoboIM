"""Prepared pure controller checks; no Go, containers, fixture or business I/O."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('receiver_runner', Path(__file__).with_name('run.py'))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


class ReceiverEvidence(unittest.TestCase):
    def events(self):
        package = runner.PREFIX + 'internal/groupdelivery'
        return [{'Package':package,'Action':'run','Test':'TestSynthetic'},
                {'Package':package,'Action':'run','Test':'TestSynthetic/child'},
                {'Package':package,'Action':'pass','Test':'TestSynthetic/child'},
                {'Package':package,'Action':'pass','Test':'TestSynthetic'},
                {'Package':package,'Action':'pass'}]

    def test_exact_events_have_one_complete_terminal_and_scope(self):
        result = runner.audit(self.events(), 0, 'internal/groupdelivery', ['TestSynthetic'], 1)
        self.assertEqual(result['counts'], {'top_pass':1,'sub_pass':1})
        self.assertEqual(result['scope'], runner.SCOPE)

    def test_rejects_skip_assertion_build_race_foreign_and_partial(self):
        variants = []
        for action in ('skip','fail'):
            rows = self.events()
            rows[2]['Action'] = action
            variants.append(rows)
        variants += [self.events()[:-1], self.events() + self.events()[-1:],
                     self.events()[:1] + self.events()[2:],
                     [{'Action':'build-output','ImportPath':'foreign','Output':'[build failed]'}]]
        for output in ('panic: synthetic', 'WARNING: DATA RACE', 'test timed out'):
            rows = self.events()
            rows.insert(2, {'Package':runner.PREFIX + 'internal/groupdelivery','Action':'output','Output':output})
            variants.append(rows)
        for rows in variants:
            with self.subTest(variant=variants.index(rows)):
                with self.assertRaises(ValueError):
                    runner.audit(rows, 0, 'internal/groupdelivery', ['TestSynthetic'], 1)
        with self.assertRaises(ValueError):
            runner.audit(self.events(), 1, 'internal/groupdelivery', ['TestSynthetic'], 1)
        with self.assertRaises(ValueError):
            runner.audit(self.events(), 0, 'internal/groupdelivery', ['TestDifferent'], 1)

    def test_source_names_cannot_escape_or_alias(self):
        self.assertEqual(runner.name('server/internal/groupcleanup/types.go'),
                         'server/internal/groupcleanup/types.go')
        for value in ('/server/a.go','server/../a.go','server//a.go','server/./a.go','server\\a.go',''):
            with self.subTest(value=value):
                with self.assertRaises(ValueError):
                    runner.name(value)

    def test_durable_evidence_is_fresh_no_clobber(self):
        with tempfile.TemporaryDirectory(prefix='luobo_receiver_controller_') as temp:
            p = Path(temp) / 'receipt.json'
            runner.write(p, {'state':'partial'})
            before = p.read_bytes()
            self.assertEqual(json.loads(before), {'state':'partial'})
            with self.assertRaises(FileExistsError):
                runner.write(p, {'state':'passed'})
            self.assertEqual(p.read_bytes(), before)


if __name__ == '__main__':
    unittest.main()
