"""The failure summary must identify pending selected tests without publishing log data."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

REPO = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('mobile_progress_controller', REPO / 'ci/mobile-tag903.py')
controller = importlib.util.module_from_spec(spec)
spec.loader.exec_module(controller)


class FailureProgressTests(unittest.TestCase):
    def test_incomplete_test_is_named_without_becoming_a_pass(self):
        events = [
            {'type': 'testStart', 'test': {'id': 1, 'name': 'first'}},
            {'type': 'testDone', 'testID': 1, 'result': 'success'},
            {'type': 'testStart', 'test': {'id': 2, 'name': 'pending'}},
        ]
        result = controller.finite_test_progress('\n'.join(map(json.dumps, events)), ['first', 'pending'])
        self.assertEqual(result['inFlightKnownNames'], ['pending'])
        self.assertEqual((result['knownStarted'], result['knownCompleted']), (2, 1))
        self.assertIsNone(result['machineDoneSuccess'])
        self.assertEqual(result['outcomes']['passed'], 1)

    def test_summary_drops_raw_errors_foreign_names_and_duplicate_completions(self):
        private = 'unrelated-account-private-value'
        events = [
            {'type': 'testStart', 'test': {'id': 1, 'name': private}},
            {'type': 'error', 'error': private, 'stackTrace': private},
            {'type': 'testStart', 'test': {'id': 2, 'name': 'selected'}},
            {'type': 'testDone', 'testID': 2, 'result': 'success', 'skipped': True},
            {'type': 'testDone', 'testID': 2, 'result': 'success'},
            {'type': 'done', 'success': False},
        ]
        result = controller.finite_test_progress('\n'.join(map(json.dumps, events)), ['selected'])
        self.assertNotIn(private, json.dumps(result))
        self.assertEqual((result['knownStarted'], result['knownCompleted']), (1, 1))
        self.assertEqual(result['outcomes']['skipped'], 1)
        self.assertEqual(result['outcomes']['passed'], 0)
        self.assertEqual(result['outcomes']['errors'], 1)
        self.assertFalse(result['machineDoneSuccess'])

    def test_missing_log_and_broken_summary_output_do_not_replace_primary_failure(self):
        with tempfile.TemporaryDirectory() as root:
            with patch.object(controller, 'write', side_effect=OSError), patch('builtins.print', side_effect=BrokenPipeError):
                controller.report_failed_test_progress(Path(root))


    def test_prior_analysis_reuse_rejects_foreign_proof_and_changed_current_body(self):
        import copy

        c = {'source': controller.FINAL_SOURCE, 'portable': controller.FINAL_PORTABLE,
             'prior_static5': copy.deepcopy(controller.PRIOR_STATIC5)}
        wrong = copy.deepcopy(c)
        wrong['prior_static5']['job'] = 0
        with self.assertRaises(ValueError):
            controller.prior_static5(wrong)
        wrong = copy.deepcopy(c)
        wrong['prior_static5']['boundary']['finalSourceAndToolSnapshotsAvailable'] = True
        with self.assertRaises(ValueError):
            controller.prior_static5(wrong)
        with tempfile.TemporaryDirectory() as root:
            repo = Path(root).resolve()
            for row in controller.PRIOR_STATIC5['rows']:
                path = repo / row['source']['path']
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes((REPO / row['source']['path']).read_bytes())
            result = controller.prior_static5(c, repo)
            self.assertEqual(result['sameBodyPriorBoundaryAnalysisCount'], 5)
            self.assertEqual(result['freshAnalysisOnCurrentSource'], 0)
            self.assertFalse(result['finalSourceAndToolSnapshotsAccepted'])
            path = repo / controller.PRIOR_STATIC5['rows'][0]['source']['path']
            path.write_bytes(path.read_bytes() + b'\n')
            with self.assertRaises(ValueError):
                controller.prior_static5(c, repo)

if __name__ == '__main__':
    unittest.main()
