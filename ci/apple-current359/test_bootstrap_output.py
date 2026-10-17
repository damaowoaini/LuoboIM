"""Only the new official-bootstrap/machine boundary; commands remain mocked."""
import json
from pathlib import Path
import unittest
from unittest import mock

import run as driver


class BootstrapOutput(unittest.TestCase):
    def test_bootstrap_precedes_strict_machine_metadata_without_discarding_release_gates(self):
        lane = {'flutter': {'commit': 'fixed-framework', 'engine': 'fixed-engine'}}
        expected = {'frameworkVersion': '3.47.7', 'frameworkRevision': 'fixed-framework',
                    'engineRevision': 'fixed-engine', 'dartSdkVersion': '3.13.5'}
        cases = ['valid', 'machine-prefix', 'bootstrap-fails', 'machine-fails'] + list(expected)
        for case in cases:
            with self.subTest(case=case):
                calls = []
                work = Path('/owned/task'); flutter = work/'flutter'; env = {'HOME': '/unchanged/home'}
                result = dict(expected)
                if case in expected:
                    result[case] = 'wrong-release'
                def command(actual_work, label, argv, cwd, actual_env):
                    self.assertEqual(actual_work, work)
                    self.assertEqual(cwd, work)
                    self.assertIs(actual_env, env)
                    calls.append((label, argv))
                    if label == 'flutter-bootstrap':
                        self.assertEqual(argv, [flutter/'bin/flutter', '--version'])
                        if case == 'bootstrap-fails':
                            raise ValueError('fixed bootstrap failed')
                        return b'  % Total Received\r100 215M\nordinary first official bootstrap output\n'
                    self.assertEqual(label, 'flutter-version')
                    self.assertEqual(argv, [flutter/'bin/flutter', '--version', '--machine'])
                    if case == 'machine-fails':
                        raise ValueError('fixed machine invocation failed')
                    encoded = json.dumps(result).encode()
                    return b'not-machine-prefix\n'+encoded if case == 'machine-prefix' else encoded
                with mock.patch.object(driver, 'command', side_effect=command):
                    if case == 'valid':
                        self.assertEqual(driver.flutter_bootstrap_version(work, lane, env, flutter), expected)
                    else:
                        with self.assertRaises(ValueError):
                            driver.flutter_bootstrap_version(work, lane, env, flutter)
                self.assertEqual([label for label, _argv in calls],
                                 ['flutter-bootstrap'] if case == 'bootstrap-fails' else ['flutter-bootstrap', 'flutter-version'])


if __name__ == '__main__':
    unittest.main(verbosity=2)
