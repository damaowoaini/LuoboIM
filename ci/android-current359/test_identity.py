"""Only directly affected source/core/consumer guards; no native tool invocation."""
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch, call

HERE=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('current359_controller',HERE/'run.py')
r=importlib.util.module_from_spec(spec);spec.loader.exec_module(r)


class Current359(unittest.TestCase):
    def test_exact359_535_411_native7_and_fixed_published_ref_reject_old_alias_or_drift(self):
        context=r.read(HERE/'cohort.json');inputs=r.read(HERE/'inputs.json')
        r.validate_inputs(inputs,context)
        self.assertEqual(context['source_ref'],'0e1cb1ae5f3edc3db8a0a94970b363a4cd6ae3b1')
        by_path={row['path']:row for row in inputs['files']}
        self.assertEqual(by_path['app/lib/core/privacy/message_privacy.dart'],{'path': 'app/lib/core/privacy/message_privacy.dart', 'bytes': 18789, 'sha256': 'c0da515e6ca3b935ac7b96c3dbae5302125c72e0c2ad38727bd84539b4f3073a'})
        self.assertEqual(by_path['app/test/features/chat/message_privacy_test.dart'],{'path': 'app/test/features/chat/message_privacy_test.dart', 'bytes': 13002, 'sha256': '6a80b8f4e79d2457ebb6799475b4fce6a81b6c76b8037e2b56365354bc275c90'})
        for key,value in [('source_members',530),('business_members',354),('portable_members',406),('source_ref','0'*40),('source_ref','bdffc675e6ec0f193bcb5cf786859a7746869061'),('cohort','current359-immutable-core-reuse-v1')]:
            bad=copy.deepcopy(context);bad[key]=value
            with self.subTest(key=key),self.assertRaises(ValueError):r.validate_inputs(inputs,bad)
        for field,value in [('members',530),('source_digest','0'*64)]:
            bad=copy.deepcopy(inputs);bad[field]=value
            with self.subTest(field=field),self.assertRaises(ValueError):r.validate_inputs(bad,context)

    def test_exact_original176_and_lock_reused_without_dispatching_new_bind_or_Go(self):
        context=r.read(HERE/'cohort.json');inputs=r.read(HERE/'inputs.json')
        original=r.read(r.generic.HERE/'current-app-inputs.json')
        r.require_core_equivalence(inputs,original,context)
        for prefix in ['server/pkg/protocol/','packages/flutter_openim_sdk/tool/native_core/',
                       'packages/flutter_openim_sdk/android/']:
            bad=copy.deepcopy(inputs);row=next(x for x in bad['files']if x['path'].startswith(prefix));row['sha256']='0'*64
            with self.subTest(prefix=prefix),self.assertRaises(ValueError):r.require_core_equivalence(bad,original,context)
        bad=copy.deepcopy(inputs);next(x for x in bad['files']if x['path']=='app/pubspec.lock')['sha256']='0'*64
        with self.assertRaises(ValueError):r.require_core_equivalence(bad,original,context)
        job=object.__new__(r.Job);job.w=Path('/tmp/never-created-current359-fixture')
        with self.assertRaisesRegex(ValueError,'fresh Go bind disabled'):job.build_core()
        with self.assertRaisesRegex(ValueError,'not current359 App tools'):
            job.cmd([job.w/'go/bin/go','version'],job.w,{},'go-version')

    def test_accepted_new_four_key_catalogue_keeps_original_sources_and_rejects_scope_variant_or_approval_drift(self):
        context=r.read(HERE/'cohort.json');r.check_catalogue(context)
        old=r.read(r.generic.HERE/'rich-catalog.json');new,_,_=r.rich_current.approved(HERE)
        self.assertNotEqual(new['identity'],old['identity'])
        for field in ['scope','rich','metadata_sources','origin_policy']:
            self.assertEqual(new[field],old[field])
        changed=0
        for c in new['configurations']:
            previous=next(v for v in old['configurations']if v['name']==c['name'])
            self.assertEqual(c['edges'],previous['edges']);self.assertEqual(c['declared'],previous['declared'])
            for a in c['artifacts']:
                before=next(v for v in previous['artifacts']if v['id']==a['id'])
                self.assertEqual(set(a['variant']),{'owner','attributes','capabilities','external'})
                for field in ['id','filename','bytes','sha256','origin']:self.assertEqual(a[field],before[field])
                changed+=a['variant']!=before['variant']
        self.assertEqual(changed,16)
        config=new['configurations'][0]
        report={'configuration':config['name'],'catalog_sha256':r.rich_current.CATALOG_SHA,
            'project':new['scope']['project'],'scope_sources':new['scope']['sources'],
            'no_force_or_substitution':True,'edges':copy.deepcopy(config['edges']),
            'artifacts':[{k:v for k,v in a.items()if k!='origin'}for a in config['artifacts']]}
        r.rich_current.validate_report(new,report)
        for label in ['owner','attribute','display','parent','source','catalog']:
            bad=copy.deepcopy(report)
            if label=='owner':bad['artifacts'][0]['variant']['owner']['version']='999'
            elif label=='attribute':bad['artifacts'][0]['variant']['attributes']['org.gradle.usage']['value']='java-api'
            elif label=='display':bad['artifacts'][0]['variant']['display']='runtime'
            elif label=='parent':bad['edges'][0]['from']={'kind':'foreign'}
            elif label=='source':bad['scope_sources']['app/android/app/build.gradle.kts']='0'*64
            else:bad['catalog_sha256']='0'*64
            with self.subTest(label=label),self.assertRaises(ValueError):r.rich_current.validate_report(new,bad)
        for sha in ['', '0'*64]:
            with self.subTest(approval=sha),self.assertRaises(ValueError):r.require_reviewed_consumer(context,sha)

    def test_actual_dispatch_binds_new_catalogue_resolver_and_original_owned_output_without_changing_gate_argv(self):
        job=object.__new__(r.Job);job.w=Path('/tmp/never-created-current359-dispatch')
        job.b=object();job.context=r.read(HERE/'cohort.json');job.inputs=r.read(HERE/'inputs.json');job._typed_resolver_dispatch=False
        binding=(job.w/'evidence/classpath-preseal/catalog.json','a'*64)
        dispatched=[]
        def capture(args,*unused):dispatched.append(list(map(str,args)));return Path('/tmp/not-read-log')
        with patch.object(r.plugin_scope,'verified',return_value=({},b'fixed-source-seal')) as plugin_verified,\
                patch.object(r.plugin_scope,'require_origin_binding') as plugin_origin,\
                patch.object(r.plugin_scope,'reports',return_value={'reports':[],
                    'actual_project_configurations':[],'runtime_or_APK_accepted':False}) as plugin_reports,\
                patch.object(r.s,'sha',return_value='b'*64),patch.object(r.s,'write_json'),\
                patch.object(r.s,'run',side_effect=capture),\
                patch.object(r.classpaths,'verified_catalog',return_value=binding),\
                patch.object(r.rich_current,'require_origin_binding'),\
                patch.object(r.rich_current,'check_apk_reports',return_value=[]):
            env=r.parent.environment(job.w,False)
            args=job.gradle('dependencies',offline=False)+['--info','-I',r.parent.HERE/'resolve.gradle',
                '--write-verification-metadata','sha256']
            job.cmd(args,job.w/'source/app/android',env,'gradle-dependency-setup',1800)
            self.assertTrue(job._typed_resolver_dispatch)
            actual=dispatched[-1]
            self.assertEqual(actual[actual.index('-I')+1],str(HERE/'resolve.gradle'))
            self.assertIn('-DluoboClasspathCatalogSha256='+binding[1],actual)
            self.assertIn(str(r.generic.HERE/'owned-outputs.gradle'),actual)
            self.assertEqual(actual.count('--stacktrace'),1)
            self.assertEqual(plugin_verified.call_args_list,[call(job.w,HERE,job.inputs,job.b)]*2)
            plugin_origin.assert_not_called();plugin_reports.assert_not_called()
            job.cmd(job.gradle('assembleDebug'),job.w/'source/app/android',
                r.parent.environment(job.w,True),'assemble-debug',1800)
            self.assertIn(str(HERE/'resolve.gradle'),dispatched[-1])
            self.assertNotIn('--stacktrace',dispatched[-1])
            plugin_origin.assert_called_once_with(job.w,HERE,job.inputs)
            plugin_reports.assert_called_once_with(job.w,HERE,job.inputs,'apk',False)
            self.assertEqual(plugin_verified.call_args_list,[call(job.w,HERE,job.inputs,job.b)]*4)
            # Original seven rejection commands use the exact inherited method;
            # no rich bypass/init mutation is added to those labels.
            negative=job.gradle('assembleRelease')
            job.cmd(negative,job.w/'source/app/android',r.parent.environment(job.w,True),
                'gate-assemblerelease',180,'QA core requires assembleDebug')
            self.assertEqual(dispatched[-1][:len(negative)],list(map(str,negative)))
            self.assertNotIn(str(HERE/'resolve.gradle'),dispatched[-1])
            self.assertNotIn('--stacktrace',dispatched[-1])
            size=len(dispatched);job._typed_resolver_dispatch=False
            bad=list(args)+['--offline']
            with self.assertRaises(ValueError):job.cmd(bad,job.w/'source/app/android',env,'gradle-dependency-setup',1800)
            self.assertEqual(len(dispatched),size)
            plugin_origin.assert_called_once_with(job.w,HERE,job.inputs)
            plugin_reports.assert_called_once_with(job.w,HERE,job.inputs,'apk',False)
            self.assertEqual(plugin_verified.call_args_list,[call(job.w,HERE,job.inputs,job.b)]*4)

    def test_builtin_then_three_guarded_offline_plugin_controls_require_actual_exit_and_check_before_completion(self):
        original_prepare=r.plugin_scope_fixture.prepare;original_check=r.plugin_scope_fixture.check
        modes=['two-projects-positive','changed-plugin-source','unregistered-project']
        for fault in [None,'command','resource','bad-exit','checker']:
            with self.subTest(fault=fault),tempfile.TemporaryDirectory() as folder:
                job=object.__new__(r.Job);job.w=Path(folder).resolve();events=[];checks=[]
                job.b=SimpleNamespace(check=lambda:None);job.verify=lambda:events.append('verify')
                def prepare(work,here):
                    events.append('prepare');return original_prepare(work,here)
                def checked(case,log,work,exit_code):
                    events.append('check-'+case['case']);checks.append(exit_code)
                    if fault=='checker':raise ValueError('controlled checker failure')
                    return original_check(case,log,work,exit_code)
                def dispatch(args,cwd,env,work,budget,label,timeout,reject,stdin):
                    events.append(label);mode=label.removeprefix('plugin-control-')
                    self.assertIn(mode,modes);self.assertEqual(events[0:4],['netns','verify','builtin','prepare'])
                    self.assertEqual((work,budget,timeout,stdin),(job.w,job.b,120,None))
                    self.assertEqual(cwd,job.w/'plugin-scope-controls'/mode/'project')
                    argv=list(map(str,args));self.assertIn('--offline',argv)
                    self.assertEqual(argv[argv.index('--gradle-user-home')+1],str(job.w/'selector-gradle-home'))
                    self.assertEqual(argv[argv.index('--project-cache-dir')+1],str(cwd.parent/'project-cache'))
                    self.assertEqual(argv[-4:],['-I',str(r.generic.HERE/'owned-outputs.gradle'),
                        '-DluoboProductionWork='+str(job.w),'-DluoboProductionDispatch='+label])
                    self.assertEqual(env['HOME'],r.os.environ['HOME'])
                    self.assertEqual(env['GRADLE_USER_HOME'],str(job.w/'selector-gradle-home'))
                    self.assertEqual(r.parent.environment(job.w,True)['GRADLE_USER_HOME'],str(job.w/'gradle-cache'))
                    if fault=='command':raise ValueError('controlled dispatch failure')
                    data=json.loads((cwd/'control-input.json').read_bytes());output=[]
                    for project in ([':app',':plugin']if reject is None else[':app']):
                        for config in data['App_source']['configurations']:
                            marker={'writer_sha256':data['writer_sha256'],'project':project,'configuration':config['name']}
                            output.append(r.plugin_scope_fixture.MARKER+json.dumps(marker))
                            directory=Path(data['owned'])/'evidence'
                            directory=(directory/'rich-consumed/metadata'if project==':app'else
                                directory/'plugin-rich-consumed/metadata'/data['entry']['slot'])
                            r.s.write_json(directory/(config['name']+'.json'),{'project':project,
                                'edges':config['edges'],'artifacts':[{k:v for k,v in a.items()if k!='origin'}for a in config['artifacts']]})
                    if reject is not None:output.append(reject)
                    log=job.w/'evidence'/(label+'.log');r.s.put(log,('\n'.join(output)+'\n').encode())
                    r.s.write_json(job.w/'evidence'/(label+'.resource.json'),{'exit_code':None if fault=='bad-exit'else(0 if reject is None else 1),
                        'expectation_met':True,'error':None,'latched_failure':'simulated'if fault=='resource'else None,
                        'expected_rejection':reject,'log_sha256':r.s.sha(log)})
                    return log
                with patch.object(r.os,'geteuid',return_value=501),\
                        patch.object(r.parent,'netns',side_effect=lambda:events.append('netns')),\
                        patch.object(r.classpaths,'seal',side_effect=lambda *a:events.append('builtin')),\
                        patch.object(r.classpaths,'verified_catalog',return_value=(job.w/'catalog.json','a'*64)),\
                        patch.object(r.plugin_scope_fixture,'prepare',side_effect=prepare),\
                        patch.object(r.plugin_scope_fixture,'check',side_effect=checked),\
                        patch.object(r.s,'run',side_effect=dispatch):
                    if fault is None:job.preseal()
                    else:
                        with self.assertRaises(ValueError):job.preseal()
                complete=job.w/'evidence/plugin-controls-complete.json'
                self.assertEqual(complete.exists(),fault is None)
                if fault is None:
                    self.assertEqual(checks,[0,1,1]);summary=json.loads(complete.read_bytes())
                    self.assertEqual([x['case']for x in summary['cases']],modes)
                    self.assertEqual([x['postreturn_markers']for x in summary['cases']],[4,2,2])
                    self.assertEqual(events[-1],'verify')
                else:self.assertEqual(len(checks),1 if fault=='checker'else 0)

    def test_component_refusal_diagnostics_bind_context_without_relaxing_component_or_writer_admission(self):
        # Static source-contract guard, not a Groovy compile or runtime claim.
        text=(HERE/'resolve.gradle').read_text()
        prefix=text[:text.index('def selectorIdentity = ')]
        self.assertEqual(hashlib.sha256(prefix.encode()).hexdigest(),'4cfbb4c5ccc8da2b68c1eae45de77d31ae575b8c7bf3d7943760fc99f60bd484')
        body=text.split('def componentIdentity = { component,p,c,String role ->\n',1)[1].split('\ngradle.beforeSettings',1)[0]
        accepted=body.split('    // Diagnostic-only;',1)[0]
        self.assertEqual(hashlib.sha256(accepted.encode()).hexdigest(),'782366408a433075377b35ec0b2e9fe9acd40eac487a04fa7981a187f796325e')
        self.assertTrue(body.endswith("    componentRefusalDiagnostic(component,p,c,role)\n    throw new GradleException('unknown selected component identifier refused')\n}"))
        self.assertEqual(body.count('return '),2)
        for expression in ["componentIdentity(edge.selected.id,p,c,'edge.selected')",
                "componentIdentity(edge.from.id,p,c,'edge.from')",
                "componentIdentity(component,p,c,'artifact')"]:
            with self.subTest(call=expression):self.assertEqual(text.count(expression),1)
        self.assertEqual(text.count('componentIdentity('),3)
        diagnostic=text.split('// Refusal diagnostics only:',1)[1].split('def componentIdentity = ',1)[0]
        for fragment in ['value.toString().take(limit).replaceAll(',
                'project:componentRefusalText(p.path,256)',
                'configuration:componentRefusalText(c.name,256)',
                'component_class:componentRefusalText(component.getClass().name,256)',
                'display_name:componentRefusalText(component.displayName,512)',
                'c.incoming.resolutionResult.rootComponent.get().id',
                'detail.same_resolution_root = (component == resolutionRoot)',
                'same_resolution_root:null', "resolution_root_status:'unavailable'",
                'JsonOutput.toJson(detail)', 'encoded.length() > 8192',
                "logger.lifecycle('LUOBO_COMPONENT_IDENTITY_REFUSAL ' + encoded)",
                'catch (Throwable unavailableDiagnostic)', 'catch (Throwable unavailableLogger)']:
            with self.subTest(fragment=fragment):self.assertIn(fragment,diagnostic)
        self.assertIn(r'/[\u0000-\u001f\u007f-\u009f\u061c\u200e\u200f\u2028-\u202e\u2066-\u2069]/',diagnostic)
        self.assertNotIn('.message',diagnostic)
        self.assertEqual(hashlib.sha256(r.plugin_scope_fixture.writer_bytes(HERE)).hexdigest(),'edb7753928b27ef2d6dd8a51fb0822554563f9bcdf3a8b63cd98fbe85786008e')



if __name__=='__main__':unittest.main()
