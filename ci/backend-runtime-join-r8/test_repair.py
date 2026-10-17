#!/usr/bin/env python3
"""Repair5 contract controls plus actual split-process dispatcher; no Go, service or fixture."""
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
        bad=copy.deepcopy(p);bad['repairTargets'].setdefault('pkg/common/startrpc',[]).append(runner.PENDING_POSITIVE[1])
        with self.assertRaises(ValueError):runner.check_repair(c,bad)
        for key,value in (('currentMainBuilds',14),('newBinaries',True),('runtimeGraphAccepted',True),('fullRegression',True)):
            bad=copy.deepcopy(p);bad[key]=value
            with self.subTest(boundary=key),self.assertRaises(ValueError):runner.check_repair(c,bad)
        bad=copy.deepcopy(p);bad['acceptedPreviousRun']['source']=c['source']
        with self.assertRaises(ValueError):runner.check_repair(c,bad)
        bad=copy.deepcopy(p);bad['sourceDelta'][0]['afterSHA256']='0'*64
        with self.assertRaises(ValueError):runner.check_repair(c,bad)
        for key,value in (('wholeFixtureNoResidueAccepted',True),('actualTopIncomplete',0),('mongoResidualDatabaseCount',0)):
            bad=copy.deepcopy(p);bad['previousRepairRun'][key]=value
            with self.subTest(previousRepair=key),self.assertRaises(ValueError):runner.check_repair(c,bad)
        bad=copy.deepcopy(p);bad['latestRepairRun']['actualTopPASS']=15
        with self.assertRaises(ValueError):runner.check_repair(c,bad)
        bad=copy.deepcopy(p);bad['executionScopes'].append(copy.deepcopy(bad['executionScopes'][0]))
        with self.assertRaises(ValueError):runner.check_repair(c,bad)
        bad=copy.deepcopy(p);bad['previousSplitRepairRun']['actualTopPASS']=5
        with self.assertRaises(ValueError):runner.check_repair(c,bad)
        for field in ('directlyAffectedPriorTargets','directlyAffectedCurrentTargets','directlyAffectedCaptureFixtureTargets'):
            bad=copy.deepcopy(p);package=next(iter(bad[field]));bad[field][package].append('TestInventedAuthority')
            with self.subTest(impact=field),self.assertRaises(ValueError):runner.check_repair(c,bad)

    def test_actual_repair_plan_has_no_build_rejects_partial_events_and_retains_primary_post_failure(self):
        c,p=self.contract();active=runner.repair_execution_plan(c,p)
        self.assertEqual(active['compile'],[]);self.assertEqual(len(runner.pairs(active['targets'])),5)
        self.assertFalse(runner.pairs(active['targets']) & runner.pairs(p['actualPassedTargets']))
        audited=copy.deepcopy(active)
        first_package=next(iter(active['targets']));first_top=active['targets'][first_package][0]
        # Parser-only child fixture; not added to actual Go source/selection.
        audited['requiredSubtests'].setdefault(first_package,{})[first_top]=['control-required-descendant']
        events=[]
        for package,tops in active['targets'].items():
            pkg=runner.PREFIX+package;events.append({'Action':'start','Package':pkg})
            for top in tops:
                events.append({'Action':'run','Package':pkg,'Test':top})
                for child in audited['requiredSubtests'].get(package,{}).get(top,[]):
                    events += [{'Action':'run','Package':pkg,'Test':top+'/'+child},{'Action':'pass','Package':pkg,'Test':top+'/'+child}]
                events.append({'Action':'pass','Package':pkg,'Test':top})
            events.append({'Action':'pass','Package':pkg})
        result=runner.require_children(runner.audit(events,0,active['targets']),audited)
        self.assertEqual(result['topPASS'],5)
        with self.assertRaises(ValueError):runner.audit(events,1,active['targets'])
        result['namedCompletions']=[r for r in result['namedCompletions'] if '/' not in r['name']]
        with self.assertRaises(ValueError):runner.require_children(result,audited)
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

    def test_split_process_dispatch_keeps_md_failure_and_missing_child_failed_but_runs_both_owned_scopes(self):
        c,p=self.contract();active=runner.repair_execution_plan(c,p)
        self.assertEqual([len(runner.pairs(scope['targets'])) for scope in active['scopes']],[1,4])
        self.assertEqual([list(scope['targets']) for scope in active['scopes']],[['internal/namespaceimport/ownedcontrol'],['internal/namespaceimport/ownedcontrol']])
        real_child='real_commit_custody_failure_never_becomes_normal_capture_pending'
        self.assertIn(real_child,c['requiredSubtests']['internal/namespaceimport/ownedcontrol']['TestHistoricalSourceProducesSignedPagesWithoutInventingMissingHistory'])
        self.assertFalse(runner.pairs(p['actualPassedTargets']) & runner.pairs(p['directlyAffectedCaptureFixtureTargets']))
        active=copy.deepcopy(active);first=active['scopes'][0];package=next(iter(first['targets']));top=first['targets'][package][0]
        child='control-required-descendant';first['requiredSubtests']={package:{top:[child]}}
        diagnostic={'schema':'historical-controller-finite-diagnostic-v1','wall_micros':5,'cpu_available':True,'user_cpu_micros':2,'system_cpu_micros':1,'context':'active','final_phase':'published','final_next_page':1,'trace':{'calls':{'controller.step':{'calls':1,'wall_micros':3,'max_wall_micros':3,'error_tags':{'ok':1}}},'events':[{'operation':'controller.step','call':1,'phase':'published','next_page':1,'wall_micros':3,'elapsed_micros':5,'error_tags':['ok'],'context':'active'}],'event_overflow':0}}
        def output_event(value):
            return {'Action':'output','Package':runner.PREFIX+package,'Test':top,'Output':'    historical_mongo_composition_linux_test.go:1: '+runner.DIAGNOSTIC_PREFIX+json.dumps(value)+'\n'}
        for mode in ('all-pass','first-nonzero','first-missing-child','missing-diagnostic'):
            with self.subTest(mode=mode),tempfile.TemporaryDirectory() as temporary:
                output=Path(temporary).resolve();calls=[]
                def command(label,argv,timeout):
                    index=len(calls);scope=active['scopes'][index]
                    self.assertEqual(label,scope['label']);self.assertEqual(timeout,2249-index)
                    for flag in ('-timeout=180s','-race','-mod=readonly'):self.assertIn(flag,argv)
                    self.assertEqual([arg for arg in argv if arg.startswith('./')],['./'+package])
                    self.assertEqual(argv[:2],['env','-i']);self.assertIn('GOAUTH=off',argv)
                    expected='^('+'|'.join(sorted(scope['targets'][package]))+')$'
                    self.assertEqual(argv[argv.index('-run')+1],expected)
                    calls.append(label);folder=output/label;folder.mkdir();pkg=runner.PREFIX+package;events=[{'Action':'start','Package':pkg}]
                    for name in scope['targets'][package]:
                        events.append({'Action':'run','Package':pkg,'Test':name})
                        for descendant in scope['requiredSubtests'].get(package,{}).get(name,[]):
                            if mode=='first-missing-child' and index==0 and descendant==child:continue
                            events += [{'Action':'run','Package':pkg,'Test':name+'/'+descendant},{'Action':'pass','Package':pkg,'Test':name+'/'+descendant}]
                        if index==0 and mode!='missing-diagnostic':events.append(output_event(diagnostic))
                        events.append({'Action':'pass','Package':pkg,'Test':name})
                    events.append({'Action':'pass','Package':pkg})
                    (folder/'stdout.jsonl').write_text('\n'.join(json.dumps(e) for e in events)+'\n')
                    return folder,1 if mode=='first-nonzero' and index==0 else 0
                with mock.patch.object(runner.time,'monotonic',side_effect=[10.,11.,12.]):result=runner.execute_repair_scopes(active,command,{'GOAUTH':'off'},output)
                self.assertEqual(calls,[x['label'] for x in active['scopes']]);self.assertEqual(result['accepted'],mode=='all-pass')
                self.assertTrue(result['processes'][1]['accepted']);self.assertFalse(result['processes'][1]['diagnosticRequired'])
                self.assertFalse((output/active['scopes'][1]['label']/'controller-diagnostic.json').exists())
                self.assertEqual(json.loads((output/'targeted-tests-union.json').read_bytes()),result)
                record=json.loads((output/first['label']/'controller-diagnostic.json').read_bytes());self.assertEqual(record['accepted'],mode!='missing-diagnostic')
                if mode=='first-nonzero':self.assertTrue(record['accepted']);self.assertFalse(result['accepted'])
                before=(output/'targeted-tests-union.json').read_bytes()
                with self.assertRaises(FileExistsError):runner.write(output/'targeted-tests-union.json',{'accepted':True})
                self.assertEqual((output/'targeted-tests-union.json').read_bytes(),before)
        event=output_event(diagnostic);text=event['Output'];split=len(text)//2
        self.assertEqual(runner.extract_controller_diagnostic([dict(event,Output=text[:split]),dict(event,Output=text[split:])]),diagnostic)
        for field,value in [('schema','foreign'),('config','must-not-be-exported')]:
            bad=copy.deepcopy(diagnostic);bad[field]=value
            with self.subTest(diagnostic=field),self.assertRaises(ValueError):runner.extract_controller_diagnostic([output_event(bad)])
        for events in ([event,event],[dict(event,Test=top+'Foreign')],[dict(event,Output='x'*32769+'\n')]):
            with self.assertRaises(ValueError):runner.extract_controller_diagnostic(events)
        with tempfile.TemporaryDirectory() as temporary:
            command=mock.Mock(side_effect=ValueError('guard, timeout or remaining process rejection'))
            with self.assertRaisesRegex(ValueError,'guard, timeout'):runner.execute_repair_scopes(active,command,{'GOAUTH':'off'},Path(temporary).resolve())
            self.assertEqual(command.call_count,1)

if __name__=='__main__':unittest.main(verbosity=2)
