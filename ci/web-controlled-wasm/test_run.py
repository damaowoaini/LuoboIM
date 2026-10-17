import copy
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest

spec=importlib.util.spec_from_file_location('controlled_wasm_source_only',Path(__file__).with_name('run.py'))
lane=importlib.util.module_from_spec(spec);spec.loader.exec_module(lane)

class ControllerContract(unittest.TestCase):
    def test_exact_scope_names_failures_skips_and_footer_are_enforced(self):
        for label,selected in lane.SPECS.items():
            package=lane.PREFIX+selected['package'];events=[]
            for test in selected['tops']+selected['subs']:
                events.extend([{'Package':package,'Test':test,'Action':'run'},{'Package':package,'Test':test,'Action':'pass'}])
            events.append({'Package':package,'Action':'pass'})
            actual=lane.audit_test(events,0,label)
            self.assertEqual(actual['topPASS'],len(selected['tops']))
            self.assertEqual(actual['subPASS'],len(selected['subs']))
            for altered in [events[:-1],events+[events[0]],events+[{'Package':package,'Action':'skip'}],events+[{'Package':package,'Test':'TestUnselected','Action':'run'}],events+[{'Package':package,'Action':'output','Output':'WARNING: DATA RACE'}]]:
                with self.assertRaises(ValueError):lane.audit_test(altered,0,label)
            with self.assertRaises(ValueError):lane.audit_test(events,1,label)

    def test_final_manifest_checksum_and_source_before_contract_are_required(self):
        # No materialization, Go, Git, network or external fixture. Only synthetic metadata.
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp).resolve();folder=root/'ci/web-controlled-wasm';folder.mkdir(parents=True)
            raw=b'package synthetic\n';source=folder/'source/core/new.go';source.parent.mkdir(parents=True);source.write_bytes(raw);source.chmod(0o644)
            items=json.loads(lane.regular(lane.HERE/'base-inputs.json'))['core']+[{'path':'new.go','bytes':len(raw),'sha256':lane.sha(raw),'mode':'0o644'}]
            items.sort(key=lambda r:r['path'])
            manifest={'schemaVersion':1,'baseCommit':lane.COMMIT,'baseInputsSHA256':lane.sha(lane.regular(lane.HERE/'base-inputs.json')),'harnessInputsSHA256':lane.sha(lane.regular(lane.HERE/'inputs.json')),'goSumSHA256':'8eb9b73313a7f774bfef48814bfc1b73e7ff7f57bd37f9d40555e8d24f70262f','requiredABI':145,'core':items,'coreDigest':lane.digest(items),'support':[],'overlay':[{'path':'ci/web-controlled-wasm/source/core/new.go','target':'new.go','bytes':len(raw),'sha256':lane.sha(raw),'beforeSHA256':None,'mode':'0o644'}]}
            target=folder/'synthetic.json'
            def load(value):
                target.write_text(json.dumps(value));return lane.load_contract(Path('ci/web-controlled-wasm/synthetic.json'),lane.sha(target.read_bytes()),root)
            self.assertEqual(load(manifest)['requiredABI'],145)
            for key,value in [('baseCommit','0'*40),('requiredABI',144),('goSumSHA256','0'*64),('coreDigest','0'*64)]:
                changed=copy.deepcopy(manifest);changed[key]=value
                with self.assertRaises(ValueError):load(changed)
            changed=copy.deepcopy(manifest);changed['overlay'][0]['target']='../escape.go'
            with self.assertRaises(ValueError):load(changed)
            changed=copy.deepcopy(manifest);changed['overlay'][0]['beforeSHA256']='0'*64
            with self.assertRaises(ValueError):load(changed)
            source.write_bytes(raw+b'//changed')
            with self.assertRaises(ValueError):load(manifest)
            with self.assertRaises(ValueError):lane.load_contract(Path('ci/web-controlled-wasm/synthetic.json'),'0'*64,root)

    def test_compile_test_and_ABI_phases_require_ordered_offline_isolation(self):
        for index,label in enumerate(lane.PHASES):
            self.assertEqual(lane.phase_allowed(label,lane.PHASES[:index]),label not in ['fetch','download'])
            if index:
                with self.assertRaises(ValueError):lane.phase_allowed(label,[])
        env=lane.clean_environment({'HOME':'/synthetic/home','PATH':'/synthetic/bin','GITHUB_TOKEN':'do-not-copy','GOPROXY':'https://bad.invalid,direct','GOPRIVATE':'*'},Path('/synthetic/owned'),'/synthetic/go')
        self.assertEqual(env['HOME'],'/synthetic/home')
        self.assertEqual(env['GOPROXY'],'off');self.assertEqual(env['GOAUTH'],'off');self.assertEqual(env['GOFLAGS'],'-mod=readonly')
        self.assertEqual(env['GOPRIVATE'],'');self.assertNotIn('GITHUB_TOKEN',env)
        with self.assertRaises(ValueError):lane.require_guard({'status':'passed','exit_code':0,'minimum_observed_free_bytes':1024},0)

    def test_real_tar_extraction_normalizes_fixed_git_file_modes_and_still_refuses_unsafe_members(self):
        # Invoke the production extractor on real tar bytes and owned files.
        def archive(entries):
            stream=lane.io.BytesIO()
            with lane.tarfile.open(fileobj=stream,mode='w') as tar:
                for name,mode,kind,body in entries:
                    member=lane.tarfile.TarInfo(name);member.mode=mode;member.type=kind
                    if kind==lane.tarfile.REGTYPE:
                        member.size=len(body);tar.addfile(member,lane.io.BytesIO(body))
                    else:
                        member.linkname='../outside';tar.addfile(member)
            return stream.getvalue()
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp).resolve();destination=root/'safe';destination.mkdir()
            bodies={'ordinary.go':b'package exact\n','executable.sh':b'#!/bin/sh\nexit 0\n','special.go':b'package special\n'}
            entries=[('ordinary.go',0o664,lane.tarfile.REGTYPE,bodies['ordinary.go']),
                     ('executable.sh',0o775,lane.tarfile.REGTYPE,bodies['executable.sh']),
                     ('special.go',0o4777,lane.tarfile.REGTYPE,bodies['special.go'])]
            lane.extract_archive(archive(entries),destination)
            actual=lane.vector(destination)
            self.assertEqual({r['path']:r['mode'] for r in actual},
                             {'ordinary.go':'0o644','executable.sh':'0o755','special.go':'0o755'})
            for row in actual:
                self.assertEqual((destination/row['path']).read_bytes(),bodies[row['path']])
                self.assertEqual(row['sha256'],lane.sha(bodies[row['path']]))
            invalid=[('parent',[('../outside',0o664,lane.tarfile.REGTYPE,b'x')]),
                     ('absolute',[('/outside',0o664,lane.tarfile.REGTYPE,b'x')]),
                     ('symlink',[('link',0o777,lane.tarfile.SYMTYPE,b'')]),
                     ('hardlink',[('link',0o777,lane.tarfile.LNKTYPE,b'')]),
                     ('duplicate',[('same',0o664,lane.tarfile.REGTYPE,b'x'),('same',0o664,lane.tarfile.REGTYPE,b'y')])]
            for label,entries in invalid:
                with self.subTest(invalid=label):
                    target=root/label;target.mkdir()
                    with self.assertRaises(ValueError):lane.extract_archive(archive(entries),target)
            self.assertFalse((root/'outside').exists())


    def test_wasm_runtime_uses_one_processor_only_for_the_two_continuation_targets(self):
        base=lane.clean_environment({'HOME':os.environ['HOME'],'PATH':'/synthetic/bin'},Path('/synthetic/owned'),'/synthetic/go')
        self.assertEqual(base['GOMAXPROCS'],'2')
        for label in ['wasmwrapper','ownread']:
            current=lane.runtime_environment(base,label,{'GOOS':'js','GOARCH':'wasm','GOMAXPROCS':'8'})
            self.assertEqual(current['GOMAXPROCS'],'1')
            self.assertEqual(current['HOME'],os.environ['HOME'])
            self.assertEqual(current['GOPROXY'],'off')
        for label in ['fetch','metadata','download','module-verify']:
            self.assertEqual(lane.runtime_environment(base,label)['GOMAXPROCS'],'2')
        for label in ['build','bufferplan','abi','unselected']:
            with self.assertRaises(ValueError):lane.runtime_environment(base,label)
        self.assertEqual(base['GOMAXPROCS'],'2')
        self.assertEqual(lane.ACTIVE_TESTS,['wasmwrapper','ownread'])
        self.assertEqual(sum(len(lane.SPECS[x]['tops']) for x in lane.ACTIVE_TESTS),8)
        self.assertEqual(sum(len(lane.SPECS[x]['subs']) for x in lane.ACTIVE_TESTS),20)

    def test_real_readonly_owned_module_cache_cleanup_never_chmods_source_or_external_tree(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp).resolve();owned=root/'owned';owned.mkdir()
            module=owned/'cache/mod/example.test/lib@v1';module.mkdir(parents=True)
            body=module/'migrator.go';body.write_text('package exact\n');body.chmod(0o444)
            module.chmod(0o555)
            source=owned/'source';source.mkdir();source.chmod(0o555)
            outside=root/'external';outside.mkdir();outside.chmod(0o555)
            try:
                result=lane.remove_owned_module_cache(owned)
                self.assertTrue(result['removed'])
                self.assertGreaterEqual(result['directoriesOwnerWriteRestored'],1)
                self.assertFalse((owned/'cache/mod').exists())
                self.assertEqual(source.stat().st_mode & 0o777,0o555)
                self.assertEqual(outside.stat().st_mode & 0o777,0o555)
                (owned/'cache/mod').symlink_to(outside,target_is_directory=True)
                with self.assertRaises(ValueError):lane.remove_owned_module_cache(owned)
                self.assertTrue((owned/'cache/mod').is_symlink())
                self.assertEqual(outside.stat().st_mode & 0o777,0o555)
                (owned/'cache/mod').unlink()
            finally:
                source.chmod(0o755);outside.chmod(0o755)


if __name__=='__main__':unittest.main(verbosity=2)
