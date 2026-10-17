"""Two prepared source/controller controls; neither executes an Apple command."""
import importlib.util
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]


def load(path, name):
    s = importlib.util.spec_from_file_location(name, path)
    m = importlib.util.module_from_spec(s);s.loader.exec_module(m)
    return m


run = load(HERE/'run.py', 'temp_probe_control_subject')
fixtures = load(HERE/'fixtures.py', 'temp_probe_public_inputs')


class TempDiagnostic(unittest.TestCase):
    def test_actual_adapter_accepts_only_the_fixed_local_spm_form_and_swift_command_has_no_App_or_fetch(self):
        adapter = load(REPO/'ci/apple-current359/xcrun_adapter.py', 'temp_probe_original_adapter')
        with tempfile.TemporaryDirectory() as q:
            work = Path(q).resolve()/'luobo-apple-current359';work.mkdir()
            for name, body in fixtures.sources().items():run.put(work/name,body)
            argv = run.native_argv(work,'spm')
            forwarded, public = adapter.forwarding(work,argv[3:],work/'source/app/ios')
            self.assertEqual(forwarded, ['/usr/bin/xcrun','--no-cache','xcodebuild',
                '-resolvePackageDependencies','-project','Runner.xcodeproj',
                '-clonedSourcePackagesDirPath',str(work/'source/app/build/ios/SourcePackages'),
                '-scheme','Runner','-derivedDataPath',str(work/'xcode/DerivedData'),
                '-packageCachePath',str(work/'xcode/PackageCache')])
            self.assertEqual(adapter.apple_execution(forwarded)[:4],
                ['/usr/bin/arch','-arm64e','/usr/bin/xcrun','--no-cache'])
            self.assertIn('work/xcode/DerivedData',public)
            swift = run.native_argv(work,'swift')
            self.assertEqual(swift[:4],['/usr/bin/arch','-arm64',run.SWIFTC,'-v'])
            self.assertEqual(swift[-1],str(work/'source/Probe.swift'))
            self.assertIn('-emit-library',swift)
            swift_command = run.native_command(work,'swift')
            spm_command = run.native_command(work,'spm')
            self.assertEqual(swift_command['architecture'],'arm64')
            self.assertEqual(spm_command['architecture'],'arm64e')
            self.assertEqual(spm_command['argv'],argv)
            self.assertEqual(run.FRESH_PHASES,('swift',))
            spec = json.loads((HERE/'inputs.json').read_bytes())
            prior = run.prior_spm_failure(spec)
            self.assertEqual(prior['phase'],'spm')
            self.assertEqual(prior['exit_code'],74)
            self.assertEqual(prior['status'],'failed')
            self.assertFalse(prior['executed_this_run'])
            with self.assertRaises(ValueError):
                run.prior_spm_failure({**spec,'priorSPMFailureReused':{**prior,'status':'passed'}})
            for phase, command in [('swift',swift_command),('spm',spm_command)]:
                self.assertEqual(command['phase'],phase)
                self.assertEqual(command['outer_exec'],'/usr/bin/arch')
                self.assertIsNone(command['actual_tool_started'])
            launch_failed = run.native_observation('swift',swift,1,False,True,b'')
            self.assertTrue(launch_failed['outer_launcher_started'])
            self.assertTrue(launch_failed['capture_completed'])
            self.assertIsNone(launch_failed['actual_tool_started'])
            self.assertFalse(launch_failed['native_command_succeeded'])
            xcode_failed = run.native_observation('spm',argv,74,False,True,
                ('Command line invocation:\n    '+run.DEVELOPER+'/usr/bin/xcodebuild -resolvePackageDependencies\n').encode())
            self.assertTrue(xcode_failed['actual_tool_started'])
            self.assertFalse(xcode_failed['native_command_succeeded'])
            adapter_only = run.native_observation('spm',argv,74,False,True,b'')
            self.assertIsNone(adapter_only['actual_tool_started'])
            succeeded = run.native_observation('swift',swift,0,False,True,b'')
            self.assertTrue(succeeded['actual_tool_started'])
            self.assertTrue(succeeded['native_command_succeeded'])
            interrupted = run.native_observation('swift',swift,0,True,False,b'')
            self.assertFalse(interrupted['native_command_succeeded'])
            self.assertIsNone(interrupted['actual_tool_started'])
            all_source = b'\n'.join(fixtures.sources().values()).decode()
            for absent in ['https:', 'http:', 'XCRemoteSwiftPackageReference',
                           'PBXShellScriptBuildPhase', 'binaryTarget', 'product-type.application']:
                self.assertNotIn(absent,all_source)
            self.assertIn('XCLocalSwiftPackageReference',all_source)
            self.assertEqual(len(run.source_snapshot(work,fixtures)),5)
            with self.assertRaises(ValueError):run.native_argv(work,'build')

    def test_actual_guard_call_combines_both_owned_phases_and_exact_cache_without_masking_failures(self):
        with tempfile.TemporaryDirectory() as q:
            root=Path(q).resolve();works=[root/n/'luobo-apple-current359' for n in ['swift','spm']]
            secondary=root/'synthetic-darwin-T';secondary.mkdir()
            for work in works:(work/'xcode/ToolCache').mkdir(parents=True)
            boundary={'canonical':str(secondary),'uid':7,'protected_preexisting':['xcrun_db']}
            adapter=SimpleNamespace(cache_boundary=lambda _:boundary,
                private_cache_path=lambda work:work/'xcode/ToolCache/xcrun_db',
                cache_directory_identity=lambda p,u:('original',str(p),u))
            def leaves(row,_check):return 7 if row['canonical']==str(secondary) else 3
            cache=SimpleNamespace(allocated_cache_leaves=leaves)
            guard=SimpleNamespace(allocated=lambda p:100 if p==root else 9000)
            original=guard.allocated;actual=[]
            def invoke(a):
                actual.append(a)
                self.assertEqual(guard.allocated(root),107)
                self.assertEqual(guard.allocated(secondary),13)
                self.assertEqual(a.max_allocated_mib,64);self.assertEqual(a.secondary_cap_mib,64)
                self.assertEqual(a.min_free_mib,1024);self.assertEqual(a.poll_interval,.2)
                self.assertEqual(a.timeout,120);self.assertEqual(a.allocated_root,root)
                self.assertEqual(a.command,['fixed','worker']);return 1
            guard.run=invoke
            code=run.guarded_probe(guard,adapter,cache,root,works,works[0],['fixed','worker'])
            self.assertEqual(code,1);self.assertIs(guard.allocated,original)
            self.assertEqual(len(actual),1)
            native={'native_invoked':True,'interrupted':False,'exit_code':65}
            post={'exact':True}
            worker_boundary={'schema':1,'identity':run.IDENTITY,'phase':'swift','exact':True}
            self.assertTrue(run.may_continue({'status':'failed','exit_code':1},native,post,worker_boundary,'swift'))
            self.assertTrue(run.may_continue({'status':'passed','exit_code':0},
                {**native,'exit_code':0},post,worker_boundary,'swift'))
            for status in ['stopped_timeout','stopped_disk_gate','infrastructure_failed',None]:
                self.assertFalse(run.may_continue({'status':status},native,post,worker_boundary,'swift'))
            for failed in [None, {**native,'interrupted':True},
                           {**native,'native_invoked':False}, {**native,'exit_code':0}]:
                self.assertFalse(run.may_continue({'status':'failed'},failed,post,worker_boundary,'swift'))
            for missing_post in [None, {'exact':False}]:
                self.assertFalse(run.may_continue({'status':'failed'},native,missing_post,worker_boundary,'swift'))
            for bad_boundary in [None, {**worker_boundary,'exact':False}, {**worker_boundary,'identity':'other'},
                                 {**worker_boundary,'phase':'spm'}, {**worker_boundary,'schema':0}]:
                self.assertFalse(run.may_continue({'status':'failed'},native,post,bad_boundary,'swift'))
            def broken(_a):raise OSError('synthetic guard infrastructure')
            guard.run=broken
            with self.assertRaises(OSError):
                run.guarded_probe(guard,adapter,cache,root,works,works[1],['fixed','worker'])
            self.assertIs(guard.allocated,original)

            for failed_check in [None, 'adapter', 'pod', 'source']:
                work=root/('post-'+str(failed_check));work.mkdir()
                for name,body in fixtures.sources().items():run.put(work/name,body)
                before=run.source_snapshot(work,fixtures);calls=[]
                def step(name):
                    calls.append(name)
                    if failed_check == name:raise ValueError('synthetic postcheck failure')
                subject=SimpleNamespace(xcrun_adapter=SimpleNamespace(check=lambda *_:step('adapter')),
                    pod_selector=SimpleNamespace(recheck=lambda *_:step('pod')))
                original_verify=run.verify;run.verify=lambda:step('source')
                try:
                    if failed_check is None:
                        run.finish_worker_boundary(work,'swift',subject,fixtures,before,{}, {'HOME':str(root)})
                    else:
                        with self.assertRaises(ValueError):
                            run.finish_worker_boundary(work,'swift',subject,fixtures,before,{}, {'HOME':str(root)})
                finally:run.verify=original_verify
                target=work/'evidence/worker-boundary-post.json'
                self.assertEqual(target.exists(),failed_check is None)
                self.assertTrue((work/'evidence/source-post.json').is_file())
                self.assertEqual(calls, {'adapter':['adapter'], 'pod':['adapter','pod'],
                    'source':['adapter','pod','source'], None:['adapter','pod','source']}[failed_check])
                if failed_check is None:
                    self.assertEqual(json.loads(target.read_text()),
                        {'schema':1,'identity':run.IDENTITY,'phase':'swift','exact':True})
