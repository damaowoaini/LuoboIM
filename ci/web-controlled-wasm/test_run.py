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

if __name__=='__main__':unittest.main(verbosity=2)
