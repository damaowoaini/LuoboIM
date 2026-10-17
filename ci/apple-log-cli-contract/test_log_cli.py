"""Static-help protocol and supervisor controls, never macOS CLI."""
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
                 ('nonzero', 'import sys;sys.stderr.write('+repr(secret)+');sys.exit(64)', 'cli_nonzero'),
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

        # Exercise the actual controller setup and its first tool-record sink.
        # External native/resource calls are finite test doubles; no macOS CLI.
        with tempfile.TemporaryDirectory() as folder:
            parent=Path(folder).resolve()
            contract=lane.metadata()
            snapshots={'log':dict(contract['priorLogBody']),
                       'python':{'path':sys.executable,'bytes':1,'sha256':'test-python'}}
            calls=[]
            class Adapter:
                seal_cache_boundary=staticmethod(lambda work:None)
                private_cache_profile=staticmethod(lambda work:'(deny file-write* (subpath "/synthetic-T"))')
                cache_boundary=staticmethod(lambda work:{'canonical':str(work/'synthetic-T')})
            class Cache:
                @staticmethod
                def run(guard,adapter,work,arguments):
                    calls.append(arguments.command)
                    self.assertEqual(arguments.command,[sys.executable,'-B',str(lane.HERE/'run.py'),'--identity',lane.IDENTITY,'--supervisor-help',str(work)])
                    self.assertNotIn('/usr/bin/sandbox-exec',arguments.command)
                    boundary=json.loads((work/'evidence/execution-boundary.json').read_bytes())
                    self.assertEqual(boundary['worker_sandbox_applied'],False)
                    self.assertFalse((work/'diagnostic.sb').exists())
                    self.assertEqual(arguments.timeout,30)
                    self.assertEqual(arguments.min_free_mib,1024)
                    self.assertEqual(arguments.poll_interval,.2)
                    for name in ('log','python'):
                        row=json.loads((work/'evidence'/('tool-'+name+'-before.json')).read_bytes())
                        self.assertEqual(row,snapshots[name])
                    lane.put(arguments.record,{'status':'passed','exit_code':0})
                    return 0
            def helper(path,name):
                if path.name=='xcrun_adapter.py':return Adapter
                if path.name=='cache_resource.py':return Cache
                if path.name=='resource_dual.py':return object()
                self.fail('unexpected helper load')
            with mock.patch.dict(os.environ,{'RUNNER_TEMP':str(parent)}), \
                 mock.patch.object(lane.platform,'system',return_value='Darwin'), \
                 mock.patch.object(lane.platform,'machine',return_value='arm64'), \
                 mock.patch.object(lane.sys,'version_info',(3,12,10)), \
                 mock.patch.object(lane,'tool_snapshot',side_effect=lambda name:dict(snapshots[name])), \
                 mock.patch.object(lane,'load',side_effect=helper):
                self.assertEqual(lane.execute(),0)
            self.assertEqual(len(calls),1)
            evidence=parent/lane.WORK_NAME/'evidence'
            summary=json.loads((evidence/'summary.json').read_bytes())
            self.assertEqual(summary['post_exact'],{'log':True,'python':True,'controllers':True})
            self.assertEqual(summary['failure'],'none')

    def test_supervisor_real_fixed_help_path_and_closed_stderr_labels_never_store_text_or_accept_commands(self):
        # These are synthetic complete stderr examples, not recovered bytes
        # from any original macOS run. No old stderr hash-to-text lookup exists.
        private = b'https://user:PRIVATE@private.invalid/object?token=PRIVATE'
        examples = [(b'log: operation not permitted\n','eperm'),
                    (b'sandbox-exec: sandbox_apply: operation not permitted\n','sandbox_refusal'),
                    (b'usage: log show [options]\n','usage'),
                    (b'log: '+private+b'\n','unknown'),
                    (b'\xff','unknown'),
                    (b'log: operation not permitted\nusage: log show\n','unknown')]
        real_popen = subprocess.Popen
        for stderr, expected in examples:
            with self.subTest(expected=expected), tempfile.TemporaryDirectory() as folder:
                work=Path(folder).resolve();(work/'evidence').mkdir();commands=[]
                def launch(command,**kwargs):
                    commands.append(command);self.assertEqual(command,lane.COMMAND)
                    script='import os,sys;os.write(2,'+repr(stderr)+');sys.exit(64)'
                    return real_popen([sys.executable,'-B','-c',script],**kwargs)
                with mock.patch.object(lane,'home_configuration_present',return_value=False), mock.patch.object(lane.subprocess,'Popen',side_effect=launch):
                    observed=lane.help_observation(work,dict(os.environ))
                self.assertEqual(commands,[lane.COMMAND]);self.assertEqual(observed['stderr_class'],expected)
                self.assertEqual(observed['reason'],'cli_nonzero');self.assertFalse(observed['cause_inferred_from_exit_code'])
                self.assertNotIn('PRIVATE',json.dumps(observed));self.assertNotIn('private.invalid',json.dumps(observed))
                self.assertFalse(observed['execution_surface']['worker_sandbox_applied'])
        self.assertEqual(lane.classify_static_help_stderr(b'x'*(lane.CAP+1)),'unknown')

        # Execute the actual supervisor helper, its activation/controller/tool
        # gates and real pipe observer. Only the native Popen is replaced with
        # a finite Python process; no native/macOS/service command is run.
        with tempfile.TemporaryDirectory() as folder:
            work=Path(folder).resolve();(work/'evidence').mkdir()
            before={'log':{'actual':'log'},'python':{'actual':'python'}};controllers=[{'actual':'source'}]
            lane.put(work/'activation.json',{'identity':lane.IDENTITY,'controllers':controllers,'before':before})
            allowed={'HOME':os.environ['HOME'],'PATH':'/usr/bin:/bin','TMPDIR':str(work)};commands=[]
            help_text='--[no-]pager --style ndjson --color --start --end --predicate --[no-]info --[no-]debug'
            def launch(command,**kwargs):
                self.assertEqual(command,lane.COMMAND);commands.append(command);self.assertEqual(kwargs['env'],allowed)
                return real_popen([sys.executable,'-B','-c','print('+repr(help_text)+')'],**kwargs)
            with mock.patch.object(lane,'metadata',return_value={'originalEnvironmentPaths':lane.ORIGINAL_PATHS}), mock.patch.object(lane,'controller_snapshot',return_value=controllers), mock.patch.object(lane,'tool_snapshot',side_effect=lambda name:before[name]), mock.patch.object(lane,'load',return_value=object()), mock.patch.object(lane,'env',return_value=allowed), mock.patch.object(lane,'home_configuration_present',return_value=False), mock.patch.object(lane.subprocess,'Popen',side_effect=launch):
                self.assertEqual(lane.supervisor_help(work),0)
            self.assertEqual(commands,[lane.COMMAND])
            proof=json.loads((work/'evidence/help-observation.json').read_bytes())
            self.assertTrue(all(proof['supports'].values()));self.assertFalse(proof['execution_surface']['worker_sandbox_applied'])
            # An altered private identity is refused before any native attempt.
            (work/'activation.json').write_text(json.dumps({'identity':'foreign','controllers':controllers,'before':before}))
            with mock.patch.object(lane,'metadata',return_value={}), mock.patch.object(lane.subprocess,'Popen') as launch:
                with self.assertRaisesRegex(ValueError,'supervisor-source-binding'):lane.supervisor_help(work)
            launch.assert_not_called()


if __name__ == '__main__':
    unittest.main(verbosity=2)
