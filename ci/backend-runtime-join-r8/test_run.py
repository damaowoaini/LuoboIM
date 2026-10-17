#!/usr/bin/env python3
"""Only new source/selection/actual-entry/audit controls; no Go or fixture execution."""
import copy, importlib.util, json, tempfile
from unittest import mock
from pathlib import Path
import unittest
spec=importlib.util.spec_from_file_location('history_runtime_incremental',Path(__file__).with_name('run.py'))
runner=importlib.util.module_from_spec(spec);spec.loader.exec_module(runner)

class HistoricalRuntimeControls(unittest.TestCase):
    def test_actual173_difference_pending_v2_and_direct_existing_cannot_be_invented_or_replayed(self):
        c=json.loads(runner.INPUTS.read_bytes());runner.check_selection(c)
        for field in ('newTargets','directlyAffectedTargets','directExistingTargets','reusedTargets','pendingTargets'):
            bad=copy.deepcopy(c);package=next(iter(bad[field]));bad[field][package].append('TestInventedAuthority')
            with self.subTest(field=field),self.assertRaises(ValueError):runner.check_selection(bad)
        bad=copy.deepcopy(c);p='pkg/common/startrpc';bad['reusedTargets'].setdefault(p,[]).append('TestOwnedPushActualOwnerClosesOriginalResourcesBeforeRedisUnknownNonpermit')
        with self.assertRaises(ValueError):runner.check_selection(bad)
    def test_actual_fourteen_main_sources_and_five_lifecycles_are_built_without_general_role_or_launch(self):
        c,bodies,_=runner.verify();runner.validate_entries(bodies,c)
        plan=runner.compile_plan(c,Path('/tmp/exact-owned-work'))
        self.assertEqual(len(plan),14);self.assertEqual(len({r['package'] for r in plan}),14)
        self.assertIn('./cmd/openim-msgtransfer-managed',c['compileOnlyPackages'])
        for row in plan:
            self.assertEqual(row['argv'][:5],['go','build','-mod=readonly','-p=1','-o'])
            self.assertEqual(row['argv'][-1],row['package']);self.assertNotIn('--role',row['argv'])
        bad=copy.deepcopy(c);bad['entrypoints'][0]['actualMain']['sha256']='0'*64
        with self.assertRaises(ValueError):runner.validate_entries(bodies,bad)
        bad=copy.deepcopy(c);bad['entrypoints'][0]['package']='./cmd/openim-rpc'
        with self.assertRaises(ValueError):runner.validate_entries(bodies,bad)
    def test_named_audit_rejects_skips_foreign_tops_missing_required_real_children_and_nonzero_exit(self):
        c=json.loads(runner.INPUTS.read_bytes());events=[]
        for package,tops in c['targets'].items():
            pkg=runner.PREFIX+package;events.append({'Action':'start','Package':pkg})
            for top in tops:
                events.append({'Action':'run','Package':pkg,'Test':top})
                for child in c['requiredSubtests'].get(package,{}).get(top,[]):events += [{'Action':'run','Package':pkg,'Test':top+'/'+child},{'Action':'pass','Package':pkg,'Test':top+'/'+child}]
                events.append({'Action':'pass','Package':pkg,'Test':top})
            events.append({'Action':'pass','Package':pkg})
        runner.require_children(runner.audit(events,0,c['targets']),c)
        bad=copy.deepcopy(events);next(e for e in bad if e['Action']=='pass' and e.get('Test'))['Action']='skip'
        with self.assertRaises(ValueError):runner.audit(bad,0,c['targets'])
        with self.assertRaises(ValueError):runner.audit(events,1,c['targets'])
        result=runner.audit(events,0,c['targets']);result['namedCompletions']=[r for r in result['namedCompletions'] if '/' not in r['name']]
        with self.assertRaises(ValueError):runner.require_children(result,c)

    def test_early_failures_keep_exact_source_post_and_never_mask_primary_or_promote_drift(self):
        source = {'members': 1, 'digest': 'source-bound-fixture'}
        target = 'server/go.mod'; body = b'module actual\n'; template = b'public: example\n'
        for phase in ('module-download', 'compile-rpc-third', 'targeted-tests'):
            with self.subTest(phase=phase), tempfile.TemporaryDirectory() as folder:
                work = Path(folder).resolve(); output = work/'evidence'; output.mkdir()
                path = work/target; path.parent.mkdir(); path.write_bytes(body); path.chmod(0o644)
                config=work/'server/config/config.yaml';config.parent.mkdir();config.write_bytes(template)
                c={'source':source,'sourceModes':{target:0o644}}
                with mock.patch.object(runner,'verify',return_value=(c,{target:body},None)) as checked:
                    with self.assertRaisesRegex(RuntimeError, '^primary-command-failure$'):
                        with runner.retain_materialized_source_post(work,output,c,{target:body},template):
                            raise RuntimeError('primary-command-failure')
                    post=json.loads((output/'source-post.json').read_bytes())
                    self.assertTrue(post['accepted']);self.assertTrue(post['controllerVerifyExact']);checked.assert_called_once()
                original=(output/'source-post.json').read_bytes()
                with self.assertRaises(FileExistsError):runner.write(output/'source-post.json',{'accepted':False})
                self.assertEqual((output/'source-post.json').read_bytes(),original)
        for primary in (False,True):
            with self.subTest(primary=primary),tempfile.TemporaryDirectory() as folder:
                work=Path(folder).resolve();output=work/'evidence';output.mkdir()
                path=work/target;path.parent.mkdir();path.write_bytes(body);path.chmod(0o644)
                config=work/'server/config/config.yaml';config.parent.mkdir();config.write_bytes(template)
                c={'source':source,'sourceModes':{target:0o644}}
                with self.assertRaisesRegex(RuntimeError if primary else ValueError,'primary-command-failure' if primary else 'source-post failed'):
                    with runner.retain_materialized_source_post(work,output,c,{target:body},template):
                        path.write_bytes(b'changed lock\n')
                        if primary:raise RuntimeError('primary-command-failure')
                post=json.loads((output/'source-post.json').read_bytes())
                self.assertFalse(post['accepted']);self.assertEqual(post['exceptionType'],'ValueError')
                self.assertFalse(post['controllerVerifyExact'])

if __name__=='__main__':unittest.main(verbosity=2)
