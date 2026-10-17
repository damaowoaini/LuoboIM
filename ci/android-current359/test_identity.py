"""Only directly affected source/core/consumer guards; no native tool invocation."""
import copy
import importlib.util
import json
from pathlib import Path
import unittest
from unittest.mock import patch

HERE=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('current359_controller',HERE/'run.py')
r=importlib.util.module_from_spec(spec);spec.loader.exec_module(r)


class Current359(unittest.TestCase):
    def test_exact359_535_411_native7_and_fixed_published_ref_reject_old_alias_or_drift(self):
        context=r.read(HERE/'cohort.json');inputs=r.read(HERE/'inputs.json')
        r.validate_inputs(inputs,context)
        self.assertEqual(context['source_ref'],'bdffc675e6ec0f193bcb5cf786859a7746869061')
        for key,value in [('source_members',530),('business_members',354),('portable_members',406),('source_ref','0'*40)]:
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
        job.b=object();job.context=r.read(HERE/'cohort.json');job._typed_resolver_dispatch=False
        binding=(job.w/'evidence/classpath-preseal/catalog.json','a'*64)
        dispatched=[]
        def capture(args,*unused):dispatched.append(list(map(str,args)));return Path('/tmp/not-read-log')
        with patch.object(r.s,'sha',return_value='b'*64),patch.object(r.s,'write_json'),\
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
            job.cmd(job.gradle('assembleDebug'),job.w/'source/app/android',
                r.parent.environment(job.w,True),'assemble-debug',1800)
            self.assertIn(str(HERE/'resolve.gradle'),dispatched[-1])
            # Original seven rejection commands use the exact inherited method;
            # no rich bypass/init mutation is added to those labels.
            negative=job.gradle('assembleRelease')
            job.cmd(negative,job.w/'source/app/android',r.parent.environment(job.w,True),
                'gate-assemblerelease',180,'QA core requires assembleDebug')
            self.assertEqual(dispatched[-1][:len(negative)],list(map(str,negative)))
            self.assertNotIn(str(HERE/'resolve.gradle'),dispatched[-1])
            size=len(dispatched);job._typed_resolver_dispatch=False
            bad=list(args)+['--offline']
            with self.assertRaises(ValueError):job.cmd(bad,job.w/'source/app/android',env,'gradle-dependency-setup',1800)
            self.assertEqual(len(dispatched),size)


if __name__=='__main__':unittest.main()
