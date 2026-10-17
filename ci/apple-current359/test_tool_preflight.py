import copy
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock
import tool_preflight as p

class ActualToolPreflight(unittest.TestCase):
 def test_all_actual_probe_environments_bind_private_cache_without_inheriting_foreign_values(self):
  with tempfile.TemporaryDirectory() as temp:
   work=Path(temp).resolve()
   with mock.patch.dict(os.environ, {'xcrun_db':'/tmp/foreign-cache','HTTPS_PROXY':'https://foreign.invalid'}):
    value=p.environment(work)
   self.assertEqual(value['xcrun_db'],str(work/'xcode/ToolCache/xcrun_db'))
   self.assertEqual(value['HOME'],os.environ['HOME'])
   self.assertEqual(value['xcrun_nocache'],'1')
   self.assertNotIn('HTTPS_PROXY',value)

 def test_actual_cli_gate_cannot_substitute_a_mock_version_for_adapter_or_git(self):
  rows={name:{'exit_code':0,'stdout':p.EXPECTED_VERSION,'stderr':b''} for name,_ in p.probes()}
  rows['system-git-version']['stdout']=b'git version 2.39.5 (Apple Git-154)\n'
  rows['real-find-git']['stdout']=(p.DEVELOPER+'/usr/bin/git\n').encode()
  audit=[{'outer_exec':'/usr/bin/arch',
          'outer_argv':['-arm64e','/usr/bin/xcrun','--no-cache','xcodebuild','-version'],
          'real_tool':'/usr/bin/xcrun','tool_argv':['--no-cache','xcodebuild','-version'],
          'response_synthesized':False}]
  with self.subTest('all real CLI results required'):
   self.assertTrue(p.inspect_results(rows,audit)['actual_cli_preflight_passed'])
  for name in ['flutter-arm-adapter-version','real-no-cache-version','system-git-version','real-find-git']:
   with self.subTest(name):
    bad=copy.deepcopy(rows);bad[name]['exit_code']=1
    with self.assertRaises(ValueError):p.inspect_results(bad,audit)
  for label,change in [('no adapter',[]),('fabricated result',[dict(audit[0],response_synthesized=True)]),('wrong argv',[dict(audit[0],tool_argv=['xcodebuild','-version'])]),('wrong exec',[dict(audit[0],outer_exec='/usr/bin/xcrun')]),('lost ARM request',[dict(audit[0],outer_argv=['/usr/bin/xcrun','--no-cache','xcodebuild','-version'])])]:
   with self.subTest(label):
    with self.assertRaises(ValueError):p.inspect_results(rows,change)
  with self.subTest('unknown git target'):
   bad=copy.deepcopy(rows);bad['real-find-git']['stdout']=b'/tmp/foreign/git\n'
   with self.assertRaises(ValueError):p.inspect_results(bad,audit)
  for name in rows:
   with self.subTest('stderr remains a failure: '+name):
    bad=copy.deepcopy(rows);bad[name]['stderr']=b'cache creation refused'
    with self.assertRaises(ValueError):p.inspect_results(bad,audit)

if __name__=='__main__':unittest.main()
