"""One direct help-pipe and independent post-check control, never macOS CLI."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location('log_help_contract', Path(__file__).with_name('run.py'))
lane = importlib.util.module_from_spec(spec); spec.loader.exec_module(lane)


class LogCLIContract(unittest.TestCase):
    def test_fixed_help_real_pipes_are_bounded_private_and_all_post_checks_are_independent(self):
        help_text = '--[no-]pager --style ndjson --color --start --end --predicate --[no-]info --[no-]debug'
        secret = 'PRIVATE-NONHELP-TEXT-NOT-RETAINED'
        real_popen = subprocess.Popen
        cases = [('complete', 'print('+repr(help_text+' '+secret)+')', 'none'),
                 ('missing', 'print("--style")', 'missing_help_tokens'),
                 ('nonzero', 'import sys;sys.stderr.write('+repr(secret)+');sys.exit(64)', 'cli_usage_exit64'),
                 ('overflow', 'print("x"*10000)', 'output_limit'),
                 ('timeout', 'import time;time.sleep(4)', 'timeout')]
        for label, script, reason in cases:
            with self.subTest(label=label), tempfile.TemporaryDirectory() as folder:
                work = Path(folder); (work/'evidence').mkdir(); commands = []; children = []
                def launch(command, **kwargs):
                    commands.append(command)
                    self.assertEqual(command, ['/usr/bin/log', 'help', 'show'])
                    self.assertEqual(kwargs['stdin'], subprocess.DEVNULL)
                    self.assertEqual(kwargs['stdout'], subprocess.PIPE)
                    self.assertEqual(kwargs['stderr'], subprocess.PIPE)
                    child = real_popen([sys.executable, '-B', '-c', script], **kwargs)
                    children.append(child); return child
                with mock.patch.object(lane, 'home_configuration_present', return_value=False), \
                     mock.patch.object(lane.subprocess, 'Popen', side_effect=launch):
                    result = lane.help_observation(work, dict(os.environ))
                self.assertEqual(commands, [lane.COMMAND]); self.assertEqual(result['reason'], reason)
                self.assertTrue(all(child.poll() is not None for child in children))
                self.assertNotIn(secret, json.dumps(result)); self.assertFalse(result['raw_help_or_system_log_retained'])
                self.assertLessEqual(result['combined_observed_bytes'], 8193)
                self.assertEqual(list(work.iterdir()), [work/'evidence'])
                if label == 'complete':
                    self.assertTrue(all(result['supports'].values())); self.assertEqual(result['exit_code'], 0)
                    self.assertTrue(result['streams_complete'])
                elif label == 'missing':
                    self.assertEqual(result['status'], 'inconclusive')
                    self.assertFalse(result['supports']['no_pager'])
                    self.assertTrue(result['support_false_means_unobserved_not_unsupported'])
                else:
                    self.assertEqual(result['supports'], {})
                seen = []
                def tool(name):
                    seen.append(name)
                    if name == 'log':
                        raise OSError(secret)
                    return {'actual': name}
                def controllers():
                    seen.append('controllers'); return {'actual': 'controllers'}
                before = {name: {'actual': name} for name in ['log', 'python', 'controllers']}
                with mock.patch.object(lane, 'tool_snapshot', side_effect=tool), \
                     mock.patch.object(lane, 'controller_snapshot', side_effect=controllers):
                    post = lane.post_boundaries(work, before)
                self.assertEqual(seen, ['log', 'python', 'controllers'])
                self.assertEqual(post, {'log': False, 'python': True, 'controllers': True})
                for name in post:
                    raw = (work/'evidence'/('post-'+name+'.json')).read_bytes()
                    self.assertNotIn(secret.encode(), raw)
        with tempfile.TemporaryDirectory() as folder:
            with mock.patch.object(lane, 'home_configuration_present', return_value=True), \
                 mock.patch.object(lane.subprocess, 'Popen') as launch:
                result = lane.help_observation(Path(folder), dict(os.environ))
            launch.assert_not_called()
            self.assertFalse(result['attempted']); self.assertEqual(result['supports'], {})
            self.assertEqual(result['reason'], 'user_log_configuration_present')


if __name__ == '__main__':
    unittest.main(verbosity=2)
