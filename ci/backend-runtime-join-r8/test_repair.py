#!/usr/bin/env python3
"""Only the two new repair-plan/phase controls; no Go, service or fixture."""
import copy, importlib.util, json, tempfile, unittest
from pathlib import Path
from unittest import mock
spec = importlib.util.spec_from_file_location('repair21_controller',Path(__file__).with_name('run.py'))
runner = importlib.util.module_from_spec(spec); spec.loader.exec_module(runner)

class RepairControls(unittest.TestCase):
    def contract(self):
        c=json.loads(runner.INPUTS.read_bytes())
        return c,json.loads((runner.REPO/c['repairPlan']['path']).read_bytes())

    def test_repair21_reuses_only_actual_old67_and14_keeps_both_graph_obligations(self):
        c,p=self.contract();runner.check_repair(c,p)
        for field in ('repairTargets','actualPassedTargets','pendingPositiveTargets','pendingRequiredTokenTargets'):
            bad=copy.deepcopy(p);package=next(iter(bad[field]));bad[field][package].append('TestInventedAuthority')
            with self.subTest(field=field),self.assertRaises(ValueError):runner.check_repair(c,bad)
        bad=copy.deepcopy(p);bad['repairTargets']['pkg/common/startrpc'].append(runner.PENDING_POSITIVE[1])
        with self.assertRaises(ValueError):runner.check_repair(c,bad)
        for key,value in (('currentMainBuilds',14),('newBinaries',True),('runtimeGraphAccepted',True),('fullRegression',True)):
            bad=copy.deepcopy(p);bad[key]=value
            with self.subTest(boundary=key),self.assertRaises(ValueError):runner.check_repair(c,bad)
        bad=copy.deepcopy(p);bad['acceptedPreviousRun']['source']=c['source']
        with self.assertRaises(ValueError):runner.check_repair(c,bad)
        bad=copy.deepcopy(p);bad['sourceDelta'][0]['afterSHA256']='0'*64
        with self.assertRaises(ValueError):runner.check_repair(c,bad)

    def test_actual_repair_plan_has_no_build_rejects_partial_events_and_retains_primary_post_failure(self):
        c,p=self.contract();active=runner.repair_execution_plan(c,p)
        self.assertEqual(active['compile'],[]);self.assertEqual(len(runner.pairs(active['targets'])),21)
        self.assertFalse(runner.pairs(active['targets']) & runner.pairs(p['actualPassedTargets']))
        events=[]
        for package,tops in active['targets'].items():
            pkg=runner.PREFIX+package;events.append({'Action':'start','Package':pkg})
            for top in tops:
                events.append({'Action':'run','Package':pkg,'Test':top})
                for child in active['requiredSubtests'].get(package,{}).get(top,[]):
                    events += [{'Action':'run','Package':pkg,'Test':top+'/'+child},{'Action':'pass','Package':pkg,'Test':top+'/'+child}]
                events.append({'Action':'pass','Package':pkg,'Test':top})
            events.append({'Action':'pass','Package':pkg})
        result=runner.require_children(runner.audit(events,0,active['targets']),active)
        self.assertEqual(result['topPASS'],21)
        with self.assertRaises(ValueError):runner.audit(events,1,active['targets'])
        result['namedCompletions']=[r for r in result['namedCompletions'] if '/' not in r['name']]
        with self.assertRaises(ValueError):runner.require_children(result,active)
        bad=copy.deepcopy(events);bad.insert(1,{'Action':'run','Package':runner.PREFIX+runner.PENDING_POSITIVE[0],'Test':runner.PENDING_POSITIVE[1]})
        with self.assertRaises(ValueError):runner.audit(bad,0,active['targets'])
        # Exercise the production repair wrapper, not a mirrored finally.
        for primary in (False,True):
            with self.subTest(primary=primary),tempfile.TemporaryDirectory() as folder:
                work=Path(folder).resolve();output=work/'evidence';output.mkdir()
                target='server/go.mod';raw=b'module actual\n';path=work/target;path.parent.mkdir();path.write_bytes(raw);path.chmod(0o644)
                template=b'public: example\n';config=work/'server/config/config.yaml';config.parent.mkdir();config.write_bytes(template)
                local={'source':{'members':1,'digest':'source-bound-wrapper-fixture'},'sourceModes':{target:0o644}}
                with mock.patch.object(runner,'verify',return_value=(local,{target:raw},None)),mock.patch.object(runner,'load_repair_plan',side_effect=ValueError('controller-drift')):
                    with self.assertRaisesRegex(RuntimeError if primary else ValueError,'primary-command-failure' if primary else 'repair-plan-post failed'):
                        with runner.retain_repair_source_post(work,output,local,{target:raw},template):
                            if primary:raise RuntimeError('primary-command-failure')
                self.assertTrue(json.loads((output/'source-post.json').read_bytes())['accepted'])
                proof=json.loads((output/'repair-plan-post.json').read_bytes());self.assertFalse(proof['accepted']);self.assertEqual(proof['exceptionType'],'ValueError')
                before=(output/'repair-plan-post.json').read_bytes()
                with self.assertRaises(FileExistsError):runner.write(output/'repair-plan-post.json',{'accepted':True})
                self.assertEqual((output/'repair-plan-post.json').read_bytes(),before)

if __name__=='__main__':unittest.main(verbosity=2)
