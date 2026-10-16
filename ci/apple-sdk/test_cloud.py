"""Prepared incremental pure controls; no tools/network/CLI stages are executed."""
import base64
import copy
import io
import json
import os
from pathlib import Path
import signal
import sys
import tarfile
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch
import urllib.request
import zipfile
import cloud
import resource_dual


class CloudInputs(unittest.TestCase):
    def test_crc32c_real_algorithm_known_vector(self):
        actual=cloud.crc_update(0xffffffff,b'123456789')^0xffffffff
        self.assertEqual(actual,0xe3069283)

    def test_finite_checksums_generation_size_and_kind_fail_closed(self):
        s=json.loads((cloud.HERE/'portable-inputs.json').read_text())
        go=dict(bytes=s['go']['bytes'],sha256=s['go']['sha256'])
        engine=dict(bytes=s['engine']['bytes'],sha256='first-acquired-not-preexisting',generation=s['engine']['generation'],md5_base64=s['engine']['md5_base64'],crc32c_base64=s['engine']['crc32c_base64'])
        cloud.check_download('go',s['go'],go);cloud.check_download('engine',s['engine'],engine)
        for label,original,keys in [('go',go,['bytes','sha256']),('engine',engine,['bytes','generation','md5_base64','crc32c_base64'])]:
            for key in keys:
                with self.subTest(label=label,key=key):
                    bad=copy.deepcopy(original);bad[key]=0 if key=='bytes' else 'unknown'
                    with self.assertRaises(ValueError):cloud.check_download(label,s[label],bad)
        with self.assertRaises(ValueError):cloud.check_download('extra',s['go'],go)

    def test_redirect_only_exact_Go_official_body(self):
        redirect=cloud.Redirect();request=urllib.request.Request('https://go.dev/dl/go1.27.2.darwin-arm64.tar.gz')
        exact='https://dl.google.com/go/go1.27.2.darwin-arm64.tar.gz'
        approved=redirect.redirect_request(request,None,302,'',{},exact)
        self.assertEqual(approved.full_url,exact)
        for url in ['https://unreviewed.invalid/file','http://dl.google.com/go/go1.27.2.darwin-arm64.tar.gz',exact+'?extra=1']:
            with self.assertRaises(ValueError):redirect.redirect_request(request,None,302,'',{},url)

    def test_actual_go_archive_extraction_root_and_paths(self):
        with tempfile.TemporaryDirectory() as value:
            root=Path(value).resolve();archive=root/'tiny.tgz'
            with tarfile.open(archive,'w:gz') as t:
                directory=tarfile.TarInfo('go');directory.type=tarfile.DIRTYPE;t.addfile(directory)
                body=b'synthetic-not-a-tool';item=tarfile.TarInfo('go/bin/go');item.size=len(body);item.mode=0o755;t.addfile(item,io.BytesIO(body))
            rows=cloud.extract_go(archive,root/'out')
            self.assertEqual(len(rows),1);self.assertEqual((root/'out/go/bin/go').read_bytes(),body)
            self.assertEqual((root/'out/go/bin/go').stat().st_mode&0o700,0o700)
            with tarfile.open(root/'bad.tgz','w:gz') as t:
                item=tarfile.TarInfo('../outside');item.size=1;t.addfile(item,io.BytesIO(b'x'))
            with self.assertRaises(ValueError):cloud.extract_go(root/'bad.tgz',root/'rejected')
            self.assertFalse((root/'outside').exists())

    def test_engine_unselected_unsafe_member_not_granted(self):
        with tempfile.TemporaryDirectory() as value:
            root=Path(value).resolve()
            with zipfile.ZipFile(root/'engine.zip','w') as z:z.writestr('../unselected',b'not granted')
            with self.assertRaises(ValueError):cloud.extract_engine(root/'engine.zip',root/'out')
            self.assertFalse((root/'out').exists())

    def test_clean_worker_does_not_inherit_proxy_CA_or_credentials(self):
        import apple_core
        actual_home=os.environ['HOME']
        with patch.dict(os.environ,{'HTTPS_PROXY':'synthetic','SSL_CERT_FILE':'synthetic','ACTIONS_RUNTIME_TOKEN':'synthetic','GOFLAGS':'-mod=mod'}):
            actual=cloud.worker_environment()
            acquire=cloud.env(Path('/owned/job'),Path('/owned/Go'),'/official/Xcode')
            build=apple_core.environment(dict(go='/owned/Go/bin/go',gomobile='/owned/mobile/gomobile',developer='/official/Xcode',module_cache='/owned/modules',python='/owned/python',go_sha256='a'*64,observer_helper='/fixed/helper',observer_helper_sha256='b'*64),Path('/owned/build'))
        for key in ['HTTPS_PROXY','SSL_CERT_FILE','ACTIONS_RUNTIME_TOKEN','GOFLAGS']:self.assertNotIn(key,actual)
        self.assertEqual(actual['PYTHONDONTWRITEBYTECODE'],'1')
        self.assertEqual(actual['HOME'],actual_home)
        for child in [acquire,build]:
            self.assertEqual(child['HOME'],actual_home)
            self.assertEqual(child['GOAUTH'],'off')
            self.assertNotIn('HTTPS_PROXY',child)
            for key,value in {'GIT_CONFIG_NOSYSTEM':'1','GIT_CONFIG_GLOBAL':'/dev/null','GIT_TERMINAL_PROMPT':'0','GIT_NO_REPLACE_OBJECTS':'1'}.items():
                self.assertEqual(child[key],value)

    def test_actual_initial_gate_precedes_source_scan_and_writes(self):
        with tempfile.TemporaryDirectory() as value:
            root=Path(value).resolve();work=root/'luobo-apple-sdk-r1'
            with patch.dict(os.environ,{'RUNNER_TEMP':str(root)}),patch.object(cloud.platform,'system',return_value='Darwin'),patch.object(cloud.platform,'machine',return_value='arm64'),patch.object(cloud,'free',return_value=8*cloud.GIB-1) as free,patch.object(cloud,'metadata') as metadata,patch.object(cloud,'load') as tools,patch.object(cloud.sys,'argv',['cloud.py','--cohort',cloud.IDENTITY,'--stage','setup','--work',str(work)]):
                with self.assertRaisesRegex(ValueError,'cold8GiB'):cloud.main()
                free.assert_called();metadata.assert_not_called();tools.assert_not_called()
            self.assertFalse(work.exists())

    def test_runtime_tool_refuses_links_hardlinks(self):
        with tempfile.TemporaryDirectory() as value:
            root=Path(value).resolve();p=root/'tool';p.write_bytes(b'synthetic')
            (root/'link').symlink_to(p)
            with self.assertRaises(ValueError):cloud.toolrow(root/'link')
            os.link(p,root/'hard')
            with self.assertRaises(ValueError):cloud.toolrow(p)

    def test_each_active_quota_latches_and_stops_exact_worker_even_if_final_recovers(self):
        for label in ['all_owned','build_subtree']:
            with self.subTest(quota=label),tempfile.TemporaryDirectory() as value:
                root=Path(value).resolve();subtree=root/'build';subtree.mkdir()
                args=SimpleNamespace(command=['synthetic-never-executed'],cwd=root,log=root/'raw',record=root/'resource.json',
                    min_free_mib=1024,allocated_root=root,max_allocated_mib=6144,secondary_root=subtree,secondary_cap_mib=3072,timeout=1800,poll_interval=.2)
                child=Mock(pid=424242,returncode=0);child.poll.side_effect=[None,0];child.wait.return_value=0
                values={root:iter([0,6144*1048576+1 if label=='all_owned' else 0,0]),subtree:iter([0,3072*1048576+1 if label=='build_subtree' else 0,0])}
                with patch.object(resource_dual.os,'statvfs',return_value=SimpleNamespace(f_bavail=9*cloud.GIB,f_frsize=1)),patch.object(resource_dual,'allocated',side_effect=lambda p:next(values[p])),patch.object(resource_dual.subprocess,'Popen',return_value=child) as spawn,patch.object(resource_dual,'stop_group') as stop,patch.object(resource_dual.os,'killpg',side_effect=ProcessLookupError):
                    self.assertEqual(resource_dual.run(args),75)
                self.assertTrue(spawn.call_args.kwargs['start_new_session']);stop.assert_called_once_with(child)
                actual=json.loads(args.record.read_text());self.assertEqual(actual['exit_code'],0)
                self.assertEqual(actual['status'],'stopped_'+('task_allocation_gate' if label=='all_owned' else 'build_subtree_allocation_gate'))

    def test_cancel_or_monitor_error_cleans_exact_worker_and_never_passes(self):
        for failure in [InterruptedError('synthetic cancel'),OSError('synthetic monitor')]:
            with self.subTest(error=type(failure).__name__),tempfile.TemporaryDirectory() as value:
                root=Path(value).resolve()
                args=SimpleNamespace(command=['synthetic-never-executed'],cwd=root,log=root/'raw',record=root/'resource.json',
                    min_free_mib=1024,allocated_root=root,max_allocated_mib=6144,secondary_root=None,secondary_cap_mib=3072,timeout=1800,poll_interval=.2)
                child=Mock(pid=424242,returncode=0);child.poll.side_effect=[None,0]
                with patch.object(resource_dual.os,'statvfs',return_value=SimpleNamespace(f_bavail=9*cloud.GIB,f_frsize=1)),patch.object(resource_dual,'allocated',side_effect=[0,failure]),patch.object(resource_dual.subprocess,'Popen',return_value=child),patch.object(resource_dual,'stop_group') as stop:
                    self.assertEqual(resource_dual.run(args),70)
                stop.assert_called_once_with(child)
                actual=json.loads(args.record.read_text());self.assertEqual(actual['status'],'infrastructure_failed')
                self.assertEqual(actual['monitor_exception_type'],type(failure).__name__)

    def test_final_quota_after_child_zero_is_failure(self):
        with tempfile.TemporaryDirectory() as value:
            root=Path(value).resolve();subtree=root/'build';subtree.mkdir()
            args=SimpleNamespace(command=['synthetic-never-executed'],cwd=root,log=root/'raw',record=root/'resource.json',
                min_free_mib=1024,allocated_root=root,max_allocated_mib=6144,secondary_root=subtree,secondary_cap_mib=3072,timeout=1800,poll_interval=.2)
            child=Mock(pid=424242,returncode=0);child.poll.return_value=0;child.wait.return_value=0
            sizes=iter([0,3072*1048576+1])
            with patch.object(resource_dual.os,'statvfs',return_value=SimpleNamespace(f_bavail=9*cloud.GIB,f_frsize=1)),patch.object(resource_dual,'allocated',side_effect=lambda p:0 if p==root else next(sizes)),patch.object(resource_dual.subprocess,'Popen',return_value=child),patch.object(resource_dual.os,'killpg',side_effect=ProcessLookupError):
                self.assertEqual(resource_dual.run(args),75)
            actual=json.loads(args.record.read_text());self.assertEqual(actual['status'],'failed_final_build_subtree_allocation_gate')
            self.assertEqual(actual['exit_code'],0)

    def test_actual_main_installs_cancel_handler_before_imported_run_and_returns_its_failure(self):
        previous={number:signal.getsignal(number) for number in [signal.SIGINT,signal.SIGTERM]}
        try:
            with tempfile.TemporaryDirectory() as value:
                root=Path(value).resolve();work=root/'luobo-apple-sdk-r1'
                def guarded(args):
                    for number in previous:
                        with self.assertRaises(InterruptedError):signal.getsignal(number)(number,None)
                    self.assertEqual(args.min_free_mib,1024);self.assertEqual(args.poll_interval,.2)
                    self.assertEqual(args.timeout,1200);self.assertIsNone(args.secondary_root)
                    return 75
                with patch.dict(os.environ,{'RUNNER_TEMP':str(root)}),patch.object(cloud.platform,'system',return_value='Darwin'),patch.object(cloud.platform,'machine',return_value='arm64'),patch.object(cloud,'free',return_value=9*cloud.GIB),patch.object(cloud,'metadata'),patch.object(cloud,'load',return_value=SimpleNamespace(run=guarded)),patch.object(cloud.sys,'argv',['cloud.py','--cohort',cloud.IDENTITY,'--stage','setup','--work',str(work)]):
                    self.assertEqual(cloud.main(),75)
        finally:
            for number,handler in previous.items():signal.signal(number,handler)


class CommandStreams(unittest.TestCase):
    def query(self, root, label, stdout, stderr, code=0):
        program='import os;os.write(1,'+repr(stdout)+');os.write(2,'+repr(stderr)+');raise SystemExit('+str(code)+')'
        return cloud.command(root,label,[sys.executable,'-I','-c',program],root,
            dict(HOME=os.environ['HOME'],PATH='/usr/bin:/bin',LANG='C',TMPDIR=str(root)))

    def test_real_warning_is_preserved_separately_and_query_returns_only_stdout(self):
        with tempfile.TemporaryDirectory() as value:
            root=Path(value).resolve();(root/'evidence').mkdir()
            stdout=b'7b156fc5557878ad6e6a07e572694af5699c6624\n'
            stderr=b'git: warning: synthetic cache write denied\n'
            self.assertEqual(self.query(root,'source-object',stdout,stderr),stdout.decode())
            self.assertEqual((root/'evidence/source-object.log').read_bytes(),stdout)
            self.assertEqual((root/'evidence/source-object.stderr.log').read_bytes(),stderr)
            self.assertEqual(json.loads((root/'evidence/source-object.result.json').read_text()),dict(exit_code=0))

    def test_actual_setup_retains_exact_commit_admission_with_diagnostic_stderr(self):
        class AfterExactCommit(Exception):
            pass
        spec=json.loads((cloud.HERE/'portable-inputs.json').read_text())
        expected=spec['core_commit']
        original=cloud.command
        for stdout,accepted in [(expected+'\n',True),('unexpected-prefix\n'+expected+'\n',False),(expected.upper()+'\n',False)]:
            with self.subTest(stdout=stdout,accepted=accepted),tempfile.TemporaryDirectory() as value:
                root=Path(value).resolve()
                def command(w,label,args,cwd,environment):
                    if label=='source-object':
                        program='import os;os.write(1,'+repr(stdout.encode())+');os.write(2,'+repr(('synthetic diagnostic '+expected+'\n').encode())+')'
                        return original(w,label,[sys.executable,'-I','-c',program],cwd,
                            dict(HOME=os.environ['HOME'],PATH='/usr/bin:/bin',LANG='C',TMPDIR=str(root)))
                    if label=='mobile-module':
                        return json.dumps(dict(Sum=spec['mobile']['h1'],GoModSum=spec['mobile']['go_mod_h1']))
                    if label.endswith('-buildinfo'):
                        return 'go1.27.2 '+spec['mobile']['version']+' -trimpath=true'
                    return ''
                with patch.object(cloud,'metadata',return_value=spec),patch.object(cloud,'download',return_value=root/'unused-archive'),patch.object(cloud,'extract_go',return_value=[]),patch.object(cloud,'extract_engine',return_value=[]),patch.object(cloud,'command',side_effect=command),patch.object(cloud,'derive_inputs',side_effect=AfterExactCommit) as derive:
                    if accepted:
                        with self.assertRaises(AfterExactCommit):cloud.setup(root)
                        derive.assert_called_once()
                    else:
                        with self.assertRaisesRegex(ValueError,'^exact upstream commit differs$'):cloud.setup(root)
                        derive.assert_not_called()
                self.assertEqual((root/'evidence/source-object.log').read_text(),stdout)
                self.assertEqual(json.loads((root/'evidence/source-object.result.json').read_text()),dict(exit_code=0))

    def test_real_nonzero_keeps_both_raw_streams_and_never_returns_success(self):
        with tempfile.TemporaryDirectory() as value:
            root=Path(value).resolve();(root/'evidence').mkdir()
            stdout=b'7b156fc5557878ad6e6a07e572694af5699c6624\n';stderr=b'synthetic failure\n'
            with self.assertRaisesRegex(RuntimeError,'^command failed source-object$'):
                self.query(root,'source-object',stdout,stderr,7)
            self.assertEqual((root/'evidence/source-object.log').read_bytes(),stdout)
            self.assertEqual((root/'evidence/source-object.stderr.log').read_bytes(),stderr)
            self.assertEqual(json.loads((root/'evidence/source-object.result.json').read_text()),dict(exit_code=7))


class RuntimeHostTools(unittest.TestCase):
    def tool(self, root, body=b'bounded synthetic tool bytes'):
        import hashlib
        p=root/'tool';p.write_bytes(body)
        return p,dict(path=str(p),bytes=len(body),sha256=hashlib.sha256(body).hexdigest(),official_system_hardlink=True)

    def test_stream_verifies_tool_over_injected_legacy_bound_without_raising_source_bound(self):
        import apple_core
        with tempfile.TemporaryDirectory() as value:
            root=Path(value).resolve();p,row=self.tool(root,b'x'*(3*1024**2))
            with self.assertRaisesRegex(ValueError,'bounded regular single-link'):
                apple_core.read(p,limit=2*1024**2)
            self.assertIsNone(apple_core.verify_runtime_host_tool(row,limit=4*1024**2))
            self.assertEqual(apple_core.read.__defaults__,(256*1024**2,True))

    def test_tool_size_hash_and_absolute_cap_reject(self):
        import apple_core
        with tempfile.TemporaryDirectory() as value:
            root=Path(value).resolve();p,row=self.tool(root,b'x'*64)
            with self.assertRaisesRegex(ValueError,'bounded official runtime'):
                apple_core.verify_runtime_host_tool(row,limit=32)
            with self.assertRaisesRegex(ValueError,'exact pin differs'):
                apple_core.verify_runtime_host_tool(dict(row,sha256='0'*64),limit=128)
            with self.assertRaisesRegex(ValueError,'size/type differs'):
                apple_core.verify_runtime_host_tool(dict(row,bytes=63),limit=128)
            with self.assertRaisesRegex(ValueError,'bounded official runtime'):
                apple_core.verify_runtime_host_tool(row,limit=512*1024**2+1)

    def test_stream_rejects_actual_same_fd_mutation_and_path_replacement(self):
        import apple_core
        for action in ['mutate','replace']:
            with self.subTest(action=action),tempfile.TemporaryDirectory() as value:
                root=Path(value).resolve();p,row=self.tool(root,b'x'*64)
                real_sha256=apple_core.hashlib.sha256
                class ChangedDuringHash:
                    def __init__(self):
                        self.actual=real_sha256();self.changed=False
                    def update(self,body):
                        self.actual.update(body)
                        if not self.changed:
                            self.changed=True
                            if action=='mutate':
                                with p.open('r+b') as f:
                                    f.write(b'y');f.flush();os.fsync(f.fileno())
                            else:
                                q=root/'replacement';q.write_bytes(p.read_bytes());q.replace(p)
                    def hexdigest(self):
                        return self.actual.hexdigest()
                with patch.object(apple_core.hashlib,'sha256',side_effect=ChangedDuringHash):
                    with self.assertRaisesRegex(ValueError,'changed while reading'):
                        apple_core.verify_runtime_host_tool(row,limit=128)

    def test_role_canonical_and_original_official_hardlink_boundary(self):
        import apple_core
        with tempfile.TemporaryDirectory() as value:
            root=Path(value).resolve();p,row=self.tool(root)
            link=root/'link';link.symlink_to(p)
            with self.assertRaisesRegex(ValueError,'canonical runtime'):
                apple_core.verify_runtime_host_tool(dict(row,path=str(link)),limit=128)
            os.link(p,root/'hard')
            with self.assertRaisesRegex(ValueError,'bounded regular single-link'):
                apple_core.read(p)
            with self.assertRaisesRegex(ValueError,'bounded official runtime'):
                apple_core.verify_runtime_host_tool(dict(row,official_system_hardlink=False),limit=128)
            self.assertIsNone(apple_core.verify_runtime_host_tool(row,limit=128))
            developer=root/'Developer';base=developer/'Toolchains/XcodeDefault.xctoolchain/usr/bin'
            fixed=str(base/'swift-frontend')
            paths=apple_core.runtime_host_tool_paths(dict(developer=str(developer),aliases={str(base/'swiftc'):fixed}))
            self.assertIn(fixed,paths)
            self.assertNotIn(str(p),paths)
            self.assertEqual(len(paths),7)


if __name__=='__main__':unittest.main()
