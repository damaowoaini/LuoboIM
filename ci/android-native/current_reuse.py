#!/usr/bin/env python3
"""Import one immutable successful354 core, then continue real current App QA."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import sys

import audit
import current_app as current
import support as s
import verify_inputs

HERE=current.HERE
COHORT='current354-immutable-core-reuse-v1'
STAGES=['setup','restore','preseal','gradle-setup','apk']


def metadata():
    context,inputs,tools=current.metadata()
    spec=json.loads((HERE/'core-reuse.json').read_bytes())
    validate_binding(spec,context,inputs)
    for row in spec['controller_inputs']+spec['borrowed_inputs']:
        path=verify_inputs.checked_path(current.parent.REPO,row['path'])
        if path.stat().st_size!=row['bytes'] or s.sha(path)!=row['sha256']:
            raise ValueError('exact recovery source input required')
    if s.sha(HERE/'current-app-cohort.json')!=spec['current_context_sha256']:
        raise ValueError('exact IO-fixed current354 context required')
    return spec,context,inputs,tools


def validate_binding(spec,context,inputs):
    if (spec['schema']!=1 or spec['cohort']!=COHORT
            or context['source_ref']!=spec['source_ref']
            or context['source_digest']!=spec['source_digest']
            or inputs['members']!=530 or inputs['source_digest']!=spec['source_digest']
            or spec['parent']['run']!=38970232673 or spec['parent']['job']!=117206523122
            or spec['parent']['head']!='f348ae881a7462d0f53643c1c05a6d8d222c7a1c'):
        raise ValueError('only exact successful original354 core may be imported')


def custody(spec):
    path=current.parent.REPO/spec['custody_source']
    module=importlib.util.spec_from_file_location('current354_immutable_custody',path)
    value=importlib.util.module_from_spec(module);module.loader.exec_module(value)
    return value


def product_files(directory,spec):
    expected={r['path']:r for r in spec['product_pins']}
    found={str(p.relative_to(directory)) for p in directory.rglob('*') if p.is_file()}
    if found!=set(expected):raise ValueError('complete exact successful-core product members required')
    for name,row in expected.items():
        path=verify_inputs.checked_path(directory,name)
        if s.sha(path)!=row['sha256']:
            raise ValueError('immutable product member hash differs')
    return expected


class Job(current.Job):
    def __init__(self,work,inputs,tools,budget,context,spec):
        super().__init__(work,inputs,tools,budget,context);self.spec=spec

    def build_core(self):
        raise ValueError('fresh Go bind is disabled for this explicit reuse cohort')

    def restore(self):
        self.verify()
        spec=self.spec;c=custody(spec);root=self.w/'recovered-core';c.mkdir(root)
        token=os.environ.pop('RESTORE_GITHUB_TOKEN','')
        if not token:raise ValueError('ephemeral artifact-read token required')
        identity=c.check_remote(spec,token)
        for artifact in spec['parent']['artifacts']:
            archive=root/(artifact['role']+'.zip');c.download(artifact,token,archive)
            allowed=(set(r['path'] for r in spec['product_pins']) if artifact['role']=='products' else None)
            rows=c.extract(archive,artifact,root/artifact['role'],allowed)
            s.write_json(self.w/'evidence'/('import-'+artifact['role']+'-members.json'),rows)
        token=None
        raw=root/'raw';products=root/'products'
        for row in spec['raw_markers']:
            path=verify_inputs.checked_path(raw,row['path'])
            if path.stat().st_size!=row['bytes'] or s.sha(path)!=row['sha256']:
                raise ValueError('original raw source/core marker differs')
        original=json.loads((raw/'activation.json').read_bytes())
        source=json.loads((raw/'evidence/current-source.json').read_bytes())
        for name in ['setup','core']:
            resource=json.loads((raw/('evidence/'+name+'-final-resource.json')).read_bytes())
            if resource['state']!='passed' or resource['latched_failure'] is not None:
                raise ValueError('original complete successful resource proof required')
        if (original['source_ref']!=self.context['source_ref']
                or original['inputs_sha256']!=s.sha(HERE/'current-app-inputs.json')
                or source['source_digest']!=self.inputs['source_digest'] or source['members']!=530):
            raise ValueError('imported core must preserve exact source354/530 identity')
        product_files(products,spec)
        aar=products/'core-current354-arm64.aar';meta=json.loads(aar.with_suffix('.build.json').read_bytes())
        if (meta.get('readonly_dependency_mode') is not True
                or meta.get('acquired_dependency_lock',{}).get('acquired_go_sum_sha256')!=self.tools['acquired_go_sum']['sha256']
                or meta.get('acquired_dependency_lock',{}).get('source_go_sum_sha256')!=self.tools['locks']['patched_core_go_sum']['sha256']):
            raise ValueError('original locked core provenance required')
        proof=audit.core(aar,meta,spec['original_public_CA_sha256'])
        proof['binding_API']=audit.bindings(products/'core-current354-arm64-sources.jar',json.loads((HERE/'bindings.json').read_bytes()))
        if proof!=json.loads((raw/'evidence/core-audit.json').read_bytes()):
            raise ValueError('actual imported AAR/ELF/API differs from original successful raw proof')
        for name in product_files(products,spec):
            target='recovered-core-ca-public.pem' if name=='job-ca-public.pem' else name
            s.put(self.w/'artifacts'/target,(products/name).read_bytes())
        for suffix in ['.aar','.build.json','-sources.jar']:
            s.put(self.w/'artifacts'/('core-current257-arm64'+suffix),(products/('core-current354-arm64'+suffix)).read_bytes())
        s.write_json(self.w/'evidence/core-audit.json',proof)
        s.write_json(self.w/'evidence/core-import.json',{'cohort':COHORT,'original':identity,
            'source_ref':self.context['source_ref'],'source_digest':self.inputs['source_digest'],
            'AAR_sha256':s.sha(aar),'actual_binary_members_verified_in_this_job':True,
            'Go_bind_executed_in_this_job':False,'original_public_CA_sha256':spec['original_public_CA_sha256'],
            'new_setup_CA_sha256':s.sha(self.w/'job-private/job-ca.pem'),
            'setup_CA_not_source_of_imported_core':True,'old_private_key_obtained_or_published':False,
            'fixture_TLS_compatibility_or_runtime':False})
        self.verify()

    def build_apk(self):
        super().build_apk()
        inherited=self.w/'evidence/current-APK-audit.json'
        source=json.loads((self.w/'evidence/core-import.json').read_bytes())
        s.write_json(self.w/'evidence/reused-core-APK-audit.json',{
            **json.loads(inherited.read_bytes()),'cohort':COHORT,'core_origin':source,
            'inherited_current_audit_sha256':s.sha(inherited),
            'legacy_business_defines_or_old_CA_or_device_operations_is_template_only':True,
            'legacy_false_does_not_mean_newly_built_core_or_new_core_CA':True,
            'new_setup_CA_was_not_installed_into_old_core_or_manifest':True,
            'new_fixture_TLS_service_compatibility':False,'device_acceptance':False})


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--cohort',required=True,choices=[COHORT]);p.add_argument('--stage',required=True,choices=STAGES);p.add_argument('--work',required=True);a=p.parse_args()
    if sys.platform!='linux' or os.geteuid()==0:p.error('ordinary-user Linux cloud only')
    w=current.parent.work_path(a.work,a.stage=='setup')
    spec,context,inputs,tools=metadata()
    current.home_admission(os.environ['HOME'])
    if a.stage=='setup':
        s.mkdir(w);s.mkdir(w/'evidence')
        s.write_json(w/'reuse-activation.json',{'cohort':COHORT,'spec_sha256':s.sha(HERE/'core-reuse.json'),
            'context_sha256':s.sha(HERE/'current-app-cohort.json'),'source_ref':context['source_ref'],
            'source_digest':inputs['source_digest'],'controller_commit':os.environ.get('GITHUB_SHA')})
    else:
        actual=json.loads((w/'reuse-activation.json').read_bytes());previous=STAGES[STAGES.index(a.stage)-1]
        if (actual['cohort']!=COHORT or actual['spec_sha256']!=s.sha(HERE/'core-reuse.json')
                or actual['context_sha256']!=s.sha(HERE/'current-app-cohort.json')
                or actual['source_digest']!=inputs['source_digest']
                or json.loads((w/'evidence'/(previous+'-complete.json')).read_bytes())['state']!='passed'
                or json.loads((w/'evidence'/(previous+'-final-resource.json')).read_bytes())['state']!='passed'
                or (w/'evidence'/(previous+'-failure.json')).exists()):
            raise ValueError('exact successful prior recovery activation required')
        if any(os.statvfs(v).f_bavail*os.statvfs(v).f_frsize<s.INITIAL for v in (w,current.parent.REPO)):
            raise ValueError('next phase4GiB admission')
    s.write_json(w/'evidence'/(a.stage+'-claim.json'),{'cohort':COHORT,'claimed_once':True})
    def interrupted(_number,_frame):raise InterruptedError('recovery stage interrupted')
    for number in (signal.SIGINT,signal.SIGTERM):signal.signal(number,interrupted)
    if a.stage=='restore':
        def expired(_number,_frame):raise TimeoutError('immutable artifact restore exceeded600seconds')
        signal.signal(signal.SIGALRM,expired);signal.alarm(600)
    budget=s.Budget(w,current.parent.REPO);failure=None
    try:
        budget.start();job=Job(w,inputs,tools,budget,context,spec)
        {'setup':job.prepare,'restore':job.restore,'preseal':job.preseal,'gradle-setup':job.gradle_prepare,'apk':job.build_apk}[a.stage]()
        metadata();current.home_admission(os.environ['HOME'])
        for suffix in ['complete','final-resource']:
            s.write_json(w/'evidence'/(a.stage+'-'+suffix+'.json'),{'state':'passed','cohort':COHORT,**budget.record()})
        budget.finish();print(json.dumps({'stage':a.stage,'cohort':COHORT,'state':'passed',**budget.record()}))
    except BaseException as error:
        failure=type(error).__name__+': '+str(error);budget.latch('phase_or_infrastructure_failed');raise
    finally:
        if a.stage=='restore':signal.alarm(0)
        budget.stop.set()
        if budget.thread is not None:budget.thread.join()
        if failure or budget.reason:s.write_json(w/'evidence'/(a.stage+'-failure.json'),{'failure':failure,**budget.record()})


if __name__=='__main__':main()
