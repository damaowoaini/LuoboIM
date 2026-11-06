#!/usr/bin/env python3
"""Current363/source539 Android: one fresh fixed-source ARM64 debug-QA build."""
import argparse
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import sys
import tarfile

HERE=Path(__file__).resolve().parent
REPO=HERE.parents[1]
sys.path.insert(0,str(REPO/'ci/android-native'))
import current_app as generic

# Share source-independent rich/Maven/KGP libraries without mutating their globals.
sys.path.append(str(REPO/'ci/android-current359'))
import support as s
import verify_inputs
import rich_current
import maven_file_origins
import plugin_scope
import plugin_scope_fixture
import classpaths_current as classpaths

parent=generic.parent
sdk_sources=generic.sdk_sources
effective_environment=generic.effective_environment
home_admission=generic.home_admission
check_flutter_environment_sources=generic.check_flutter_environment_sources
check_current_packages=generic.check_current_packages

COHORT='current363-fresh-fixed-core-arm64-debug-v1'
SOURCE_REF='3942c2acadd0b6343b9f64e3a444a52d0a28e3d7'
SOURCE_DIGEST='07de9d07dcd2a9da2b98a29194d3442a174d60fc46a6dcd472276d80171d4c3f'
BUSINESS_DIGEST='a187df95c16c9d177d0160e0d4d393cb0f346c68c11c009128e9ac9d69e05475'
PORTABLE_DIGEST='0b855344a87445abe3e9235e5a629da6bc83b658477d760b70095561f0c7ea6f'
STAGES=['setup','core','preseal','gradle-setup','apk']


def read(path):return json.loads(Path(path).read_bytes())
def nonbusiness(rows):
    names={r['path']for r in generic.business(rows)}
    return [r for r in rows if r['path']not in names]


def validate_inputs(inputs,context):
    rows=inputs['files'];business=generic.business(rows)
    portable=business+[r for r in rows if
        r['path'].startswith('packages/flutter_openim_sdk/lib/')or r['path']in (
            'packages/flutter_openim_sdk/pubspec.yaml',
            'packages/flutter_openim_sdk/tool/fixture_native_endpoint_messenger.dart',
            'migration/legacy_personal_export/legacy_personal_export.dart')]
    if (context.get('core_build_mode')!='fresh-fixed-source' or context.get('restore_fallback')is not False
            or inputs['schema']!='luobo.android.current363-inputs.v1'
            or context['schema']!='luobo.android.current363-context.v1'
            or inputs['cohort']!=COHORT or context['cohort']!=COHORT
            or context['source_ref']!=SOURCE_REF
            or inputs['members']!=539 or context['source_members']!=539
            or len(rows)!=539 or len({r['path']for r in rows})!=539
            or any(set(r)!={'path','bytes','sha256'}for r in rows)
            or inputs['source_digest']!=SOURCE_DIGEST or context['source_digest']!=SOURCE_DIGEST
            or generic.digest(rows)!=context['source_digest']
            or len(business)!=363 or context['business_members']!=363
            or generic.digest(business)!=BUSINESS_DIGEST or context['business_rows_digest']!=BUSINESS_DIGEST
            or len(portable)!=415 or context['portable_members']!=415
            or generic.digest(portable)!=PORTABLE_DIGEST or context['portable_digest']!=PORTABLE_DIGEST):
        raise ValueError('exact published363/source539/portable415 identity required')
    for row in rows:
        if (s.name(row['path'])!=row['path']or type(row['bytes'])is not int
                or row['bytes']<0 or len(row['sha256'])!=64
                or any(c not in '0123456789abcdef'for c in row['sha256'])):
            raise ValueError('exact finite source row required')
    indexed={r['path']:r for r in rows};native=context['native_source_snapshot']
    if (context['pub_lock_after']!=indexed['app/pubspec.lock']or context['locked_packages']!=178
            or len(native['files'])!=7 or native['members']!=7
            or generic.digest(native['files'])!=native['digest']
            or len({r['path']for r in native['files']})!=7
            or any(indexed.get(r['path'])!=r for r in native['files'])):
        raise ValueError('native7 and unchanged178 lock binding required')
    return inputs


def require_core_equivalence(inputs,original,context):
    # All176 unchanged nonbusiness entries include protocol52, SDK source/tools,
    # wrapper/build files and native7. No Dart business equality is asserted.
    now=nonbusiness(inputs['files']);before=nonbusiness(original['files'])
    if (len(now)!=176 or sorted(now,key=lambda r:r['path'])!=sorted(before,key=lambda r:r['path'])
            or generic.digest(now)!=context['core_compatibility']['nonbusiness176_digest']):
        raise ValueError('original354 Go/protocol/SDK/native subset differs')
    old={r['path']:r for r in original['files']};new={r['path']:r for r in inputs['files']}
    if any(new[p]!=old[p]for p in ['app/pubspec.yaml','app/pubspec.lock']):
        raise ValueError('original178 package/native dependency identity differs')


def check_catalogue(context):
    catalog,_,_=rich_current.approved(HERE)
    proof=read(HERE/'projection-acceptance.json')
    if (context['catalogue_approval']['catalog_sha256']!=rich_current.CATALOG_SHA
            or s.sha(HERE/'projection-acceptance.json')!=catalog['accepted_provenance']['root_acceptance_sha256']
            or context['catalogue_approval']['graph_sha256']!=catalog['accepted_provenance']['graph_sha256']
            or proof['graphSHA256']!=catalog['accepted_provenance']['graph_sha256']
            or proof['run']!=38983280410 or proof['job']!=117248837126
            or proof['nativeOrAppAccepted']is not False):
        raise ValueError('independently accepted JVM18 and source-only four-key successor required')


def require_reviewed_consumer(context,approved_context_sha256):
    check_catalogue(context)
    # Explicit frozen-scope binding, not a claim of device/runtime acceptance.
    # Workflow defaults empty; Root supplies this literal only after source/pure
    # review. Same exact tuple is required for EVERY execution phase.
    if approved_context_sha256!=s.sha(HERE/'cohort.json'):
        raise ValueError('exact reviewed current363 context SHA required before setup')


def metadata():
    # Read-only source/lock validation of the existing fresh-bind libraries.
    # No artifact importer, old binary token or restore fallback is involved.
    old_context,original,tools=generic.metadata()
    context=read(HERE/'cohort.json');inputs=validate_inputs(read(HERE/'inputs.json'),context)
    for row in context['controller_inputs']+context['borrowed_inputs']:
        p=verify_inputs.checked_path(REPO,row['path'])
        if p.stat().st_size!=row['bytes']or s.sha(p)!=row['sha256']:
            raise ValueError('explicit363 source controller or borrowed input differs')
    require_core_equivalence(inputs,original,context)
    plugin_scope.contract(HERE,inputs)
    plugin_scope_fixture.writer_bytes(HERE)
    check_catalogue(context)
    if (context['original_context_sha256']!=s.sha(generic.HERE/'current-app-cohort.json')
            or context['pub_lock_after']!=old_context['pub_lock_after']
            or context['flutter_environment_sources']!=old_context['flutter_environment_sources']
            or context['webview_archive_identities']!=old_context['webview_archive_identities']):
        raise ValueError('original fresh-bind libraries and unchanged178 dependency sources required')
    return context,inputs,tools




class Job(generic.Job):
    def __init__(self,work,inputs,tools,budget,context):
        super().__init__(work,inputs,tools,budget,context)

    def verify(self):
        validate_inputs(self.inputs,self.context)
        for row in self.inputs['files']:
            path=verify_inputs.checked_path(self.w/'source',row['path'])
            if path.stat().st_size!=row['bytes']or s.sha(path)!=row['sha256']:
                raise ValueError('current363 exact source body differs')

    def source(self,env):
        checkout=self.w/'source-git'
        self.git(checkout,generic.parent.SOURCE_REPO,SOURCE_REF,env,'current363-source')
        archive=self.w/'downloads/current363-source.tar';wanted={r['path']:r for r in self.inputs['files']}
        self.cmd(['git','-C',checkout,'archive','--format=tar','--output='+str(archive),
            SOURCE_REF,'--',*wanted],self.w,env,'current363-source-archive')
        seen=set()
        with tarfile.open(archive)as bundle:
            for member in bundle:
                if member.isdir():continue
                if not member.isfile()or member.name not in wanted or member.name in seen:
                    raise ValueError('unexpected current363 source member')
                body=bundle.extractfile(member).read();row=wanted[member.name]
                if len(body)!=row['bytes']or hashlib.sha256(body).hexdigest()!=row['sha256']:
                    raise ValueError('fixed published539 body differs')
                s.put(self.w/'source'/s.name(member.name),body);seen.add(member.name)
        if seen!=wanted.keys():raise ValueError('incomplete source539')
        self.verify()
        s.write_json(self.w/'evidence/current363-source.json',{'cohort':COHORT,'source_ref':SOURCE_REF,
            'members':539,'App_members':363,'source_digest':self.inputs['source_digest'],
            'archive_sha256':s.sha(archive),'old354_source_not_relabelled':True})

    def build_core(self):
        # This invokes the original observed bind, generated-module lock checks,
        # callback/API/AAR/ELF audits and source/lock postconditions once.
        generic.old.Job.build_core(self)
        source=self.w/'artifacts/core-current257-arm64.aar'
        for a,b in [(source,self.w/'artifacts/core-current363-arm64.aar'),
                (source.with_suffix('.build.json'),self.w/'artifacts/core-current363-arm64.build.json'),
                (source.with_name(source.stem+'-sources.jar'),self.w/'artifacts/core-current363-arm64-sources.jar')]:
            s.put(b,a.read_bytes())
        s.write_json(self.w/'evidence/current363-core-build.json',{'cohort':COHORT,
            'source_members':539,'App_members':363,'source_digest':self.inputs['source_digest'],
            'source_ref':SOURCE_REF,'fresh_Go_bind_executed':True,'restored_binary':False,
            'internal_helper_filename_is_not_historical_App_identity':True,
            'AAR_sha256':s.sha(source),'build_receipt_sha256':s.sha(source.with_suffix('.build.json')),
            'only_debug_QA':True,'production_replacement':False,'device_acceptance':False})



    def prepare(self):
        # Original fresh setup installs the exact official Go/mobile/Android/
        # Flutter tools and prewarms the observed generated module once.
        super().prepare()
        self.verify()
        plugin_scope.produce(self,HERE)
        s.write_json(self.w/'evidence/current363-setup-scope.json',{'cohort':COHORT,
            'core_build_mode':'fresh-fixed-source','restore_fallback':False,
            'Go_and_gomobile_acquired_for_original_bind':True,
            'only_debug_QA':True,'production_replacement':False})

    def cmd(self,args,cwd,env,label,timeout=900,reject=None,stdin=None):
        # Original helper calls provide legacy *intent* env. Validate those
        # boundaries first, then use the explicit effective env at dispatch.
        binding=None;plugin_before=None
        if label=='flutter-init':sdk_sources.produce(self,args,cwd,env,timeout,reject,stdin)
        if label=='flutter-config':
            home_admission(os.environ['HOME'])
            check_flutter_environment_sources(self.w,self.context['flutter_environment_sources'])
            commit=self.cmd(['git','-C',self.w/'flutter','rev-parse','HEAD'],self.w,env,'current-flutter-checkout')
            if commit.read_text().strip()!=self.tools['flutter']['framework_revision']:
                raise ValueError('fixed Flutter checkout before first tool execution required')
        if label=='gradle-dependency-setup':
            expected=self.gradle('dependencies',offline=False)+['--info','-I',parent.HERE/'resolve.gradle','--write-verification-metadata','sha256']
            if (getattr(self,'_typed_resolver_dispatch',None) is not False
                    or list(map(str,args))!=list(map(str,expected)) or cwd!=self.w/'source/app/android'
                    or env!=parent.environment(self.w,False) or timeout!=1800 or reject is not None or stdin is not None):
                raise ValueError('inherited production dependency boundary differs')
            plugin_before=plugin_scope.verified(self.w,HERE,self.inputs,self.b)[1]
            args=list(args);args[args.index('-I')+1]=HERE/'resolve.gradle'
            args.append('--stacktrace')
            binding=classpaths.verified_catalog(self.w);self._typed_resolver_dispatch=True
        elif label=='assemble-debug':
            if (list(map(str,args))!=list(map(str,self.gradle('assembleDebug')))
                    or cwd!=self.w/'source/app/android' or env!=parent.environment(self.w,True)
                    or timeout!=1800 or reject is not None or stdin is not None):
                raise ValueError('fixed production APK dispatch required')
            rich_current.require_origin_binding(self.w,HERE)
            plugin_scope.require_origin_binding(self.w,HERE,self.inputs)
            plugin_before=plugin_scope.verified(self.w,HERE,self.inputs,self.b)[1]
            binding=classpaths.verified_catalog(self.w);args=list(args)+['-I',HERE/'resolve.gradle']
        if binding is not None:
            args=list(args)+['-DluoboClasspathCatalog='+str(binding[0]),'-DluoboClasspathCatalogSha256='+binding[1]]
        if Path(args[0])==self.w/'gradle/bin/gradle':
            args=list(args)+['-I',generic.HERE/'owned-outputs.gradle','-DluoboProductionWork='+str(self.w),
                '-DluoboProductionDispatch='+label]
        effective=effective_environment(env,self.w,os.environ['HOME'])
        s.write_json(self.w/'evidence'/('current-dispatch-'+label+'.json'),{'cohort':COHORT,
            'environment_adapter':'preserve HOME; explicit owned caches/JVM/Android config; no ambient proxy',
            'HOME_preserved':effective['HOME']==os.environ['HOME'],'actual_argv':list(map(str,args)),
            'catalog_sha256':None if binding is None else binding[1]})
        try:
            result=s.run(args,cwd,effective,self.w,self.b,label,timeout,reject,stdin)
            if label=='pub-get':
                s.write_json(self.w/'evidence/current-webview-archive-metadata.json',
                    check_current_packages(self.w,self.context))
            if label=='assemble-debug':
                s.write_json(self.w/'evidence/rich-apk-consumed.json',{'actual_configurations':
                    rich_current.check_apk_reports(self.w,HERE),
                    'actual_plugin_configurations':plugin_scope.reports(self.w,HERE,self.inputs,'apk',False),
                    'unused_configurations_not_claimed':True})
            return result
        finally:
            if plugin_before is not None and plugin_scope.verified(self.w,HERE,self.inputs,self.b)[1]!=plugin_before:
                raise ValueError('independent plugin source seal changed during actual Gradle dispatch')
            if binding is not None and classpaths.verified_catalog(self.w)!=binding:
                raise ValueError('producer catalog changed during actual Gradle dispatch')

    def preseal(self):
        parent.netns();self.verify()
        classpaths.seal(self,generic.HERE,HERE)
        self.plugin_controls()

    def plugin_controls(self):
        if os.geteuid()==0 or (self.w/'plugin-scope-controls').exists():
            raise ValueError('ordinary-user fresh plugin controls required')
        before=classpaths.verified_catalog(self.w)
        cases=plugin_scope_fixture.prepare(self.w,HERE)
        expected=[('two-projects-positive',None),
            ('changed-plugin-source','plugin rich independently sealed source differs'),
            ('unregistered-project','dynamic dependency version refused')]
        if [(c['case'],c['expected_rejection'])for c in cases]!=expected:
            raise ValueError('exact three plugin controls required')
        results=[]
        for case in cases:
            owner=self.w/'plugin-scope-controls'/case['case'];root=owner/'project'
            tail=['--offline','--gradle-user-home',str(owner/'gradle-home'),
                '--project-cache-dir',str(owner/'project-cache'),'-I',str(root/'control-init.gradle'),
                'checkRegisteredPluginScope']
            if (case['cwd']!=str(root) or case['command_tail']!=tail
                    or case['synthetic_only']is not True or case['production18_or_App_not_claimed']is not True):
                raise ValueError('fixed owned plugin control dispatch required')
            # The sealed selector cache is separate from production gradle-cache.
            # Keep the original owned-output audit's two-root policy unchanged.
            tail[2]=str(self.w/'selector-gradle-home')
            env=parent.environment(self.w,True)
            env['GRADLE_USER_HOME']=str(self.w/'selector-gradle-home')
            args=[self.w/'gradle/bin/gradle','--no-daemon','--max-workers=1','--no-watch-fs',
                '--console=plain','-Dorg.gradle.jvmargs=-Xmx1536m -XX:MaxMetaspaceSize=512m',*tail]
            label='plugin-control-'+case['case']
            log=self.cmd(args,root,env,label,120,case['expected_rejection'])
            resource_path=self.w/'evidence'/(label+'.resource.json');resource=read(resource_path)
            if (type(resource.get('exit_code'))is not int or resource.get('expectation_met')is not True
                    or resource.get('error')is not None or resource.get('latched_failure')is not None
                    or resource.get('expected_rejection')!=case['expected_rejection']
                    or resource.get('log_sha256')!=s.sha(log)):
                raise ValueError('actual plugin control resource proof required')
            checked=plugin_scope_fixture.check(case,log,self.w,resource['exit_code'])
            self.b.check()
            results.append({'case':case['case'],'actual_exit_code':resource['exit_code'],
                'resource_sha256':s.sha(resource_path),'log_sha256':s.sha(log),**checked})
        self.verify()
        if classpaths.verified_catalog(self.w)!=before:
            raise ValueError('builtin preseal changed during plugin controls')
        self.b.check()
        s.write_json(self.w/'evidence/plugin-controls-complete.json',{'state':'passed',
            'cohort':COHORT,'cases':results,'actual_Gradle_controls':3,'offline':True,
            'Gradle_home':'selector-gradle-home','production_Gradle_home_unchanged':'gradle-cache',
            'synthetic_file_Maven_not_production18':True,'native_or_App_acceptance':False})

    def gradle_prepare(self):
        check_catalogue(self.context)
        self._typed_resolver_dispatch=False
        try:
            # Preserve the original phase dispatch/XML/lock checks. Only the
            # origin proof uses current359's strict published-file relation.
            self.verify()
            e=parent.environment(self.w,False)
            command=self.gradle('dependencies',offline=False)+['--info','-I',parent.HERE/'resolve.gradle','--write-verification-metadata','sha256']
            log=self.cmd(command,self.w/'source/app/android',e,'gradle-dependency-setup',1800)
            xml=self.w/'source/app/android/gradle/verification-metadata.xml'
            if not xml.is_file() or '<sha256 ' not in xml.read_text():
                raise ValueError('new actual dependency verification metadata required')
            verification=maven_file_origins.verification_bytes(self.w)
            s.put(self.w/'evidence/gradle-verification-metadata.xml',verification)
            proof=maven_file_origins.audit(self.w,log.read_text(errors='replace'))
            proof['verification_metadata_sha256']=hashlib.sha256(verification).hexdigest()
            proof['preexisting_hermetic_lock']=False
            s.write_json(self.w/'evidence/dependency-origin-audit.json',proof)
            self.verify()
            result=None
            if self._typed_resolver_dispatch is not True:raise ValueError('production resolver not dispatched')
            rich_current.bind_origins(self.w,HERE,s.write_json)
            plugin_scope.bind_origins(self.w,HERE,self.inputs)
            return result
        finally:self._typed_resolver_dispatch=None

    def build_apk(self):
        check_catalogue(self.context)
        generic.parent.Job.build_apk(self)  # Retain actual seven gates, wrapper/App compilation, AAR-matched ELF audit.
        inherited=self.w/'evidence/APK-audit.json';proof=read(inherited)
        if proof['source_members']!=427 or proof['source_digest']!=self.inputs['source_digest']:
            raise ValueError('unexpected original audit template')
        source=self.w/'artifacts/luobo-current257-auditable-arm64-debug.apk'
        target=self.w/'artifacts/luobo-current363-auditable-arm64-debug.apk';s.put(target,source.read_bytes())
        s.write_json(self.w/'evidence/current363-APK-audit.json',{**proof,'cohort':COHORT,
            'source_members':539,'App_members':363,'source_ref':SOURCE_REF,
            'published_APK':str(target.relative_to(self.w)),'published_SHA256':s.sha(target),
            'inherited_template_SHA256':s.sha(inherited),'fresh_core_origin':read(self.w/'evidence/current363-core-build.json'),
            'fresh_Go_bind_and_current_App_compile':True,'only_debug_QA':True,'production_replacement':False,
            'device_or_service_or_fixture_TLS_accepted':False})


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--cohort',required=True,choices=[COHORT]);p.add_argument('--stage',choices=['readonly',*STAGES],default='readonly')
    p.add_argument('--work',type=Path)
    p.add_argument('--approved-context-sha256',default='');a=p.parse_args()
    context,inputs,tools=metadata()
    if a.stage=='readonly':
        print(json.dumps({'cohort':COHORT,'source_members':539,'App_members':363,
            'state':'source_identity_and_approved_JVM18_only','native_execution':False}));return
    require_reviewed_consumer(context,a.approved_context_sha256)  # BEFORE work creation, tokens or downloads.
    if a.work is None or sys.platform!='linux'or os.geteuid()==0:p.error('ordinary-user Linux fresh root required')
    w=generic.parent.work_path(str(a.work),a.stage=='setup');generic.home_admission(os.environ['HOME'])
    if a.stage=='setup':
        s.mkdir(w);s.mkdir(w/'evidence')
        s.write_json(w/'current363-activation.json',{'cohort':COHORT,'source_ref':SOURCE_REF,
            'context_sha256':s.sha(HERE/'cohort.json'),'inputs_sha256':s.sha(HERE/'inputs.json'),
            'controller_commit':os.environ.get('GITHUB_SHA')})
    else:
        activation=read(w/'current363-activation.json');previous=STAGES[STAGES.index(a.stage)-1]
        if (activation['cohort']!=COHORT or activation['source_ref']!=SOURCE_REF
                or activation['context_sha256']!=s.sha(HERE/'cohort.json')
                or activation['inputs_sha256']!=s.sha(HERE/'inputs.json')
                or any(read(w/'evidence'/(previous+'-'+suffix+'.json'))['state']!='passed'for suffix in ['complete','final-resource'])
                or (w/'evidence'/(previous+'-failure.json')).exists()):
            raise ValueError('exact current363 previous phase required')
        if any(os.statvfs(v).f_bavail*os.statvfs(v).f_frsize<s.INITIAL for v in [w,REPO]):
            raise ValueError('phase4GiB admission')
    s.write_json(w/'evidence'/(a.stage+'-claim.json'),{'cohort':COHORT,'claimed_once':True})
    def interrupted(_number,_frame):raise InterruptedError('current363 phase interrupted')
    for number in [signal.SIGINT,signal.SIGTERM]:signal.signal(number,interrupted)
    budget=s.Budget(w,REPO);failure=None
    try:
        budget.start();job=Job(w,inputs,tools,budget,context)
        {'setup':job.prepare,'core':job.build_core,'preseal':job.preseal,'gradle-setup':job.gradle_prepare,'apk':job.build_apk}[a.stage]()
        metadata();generic.home_admission(os.environ['HOME'])
        for suffix in ['complete','final-resource']:
            s.write_json(w/'evidence'/(a.stage+'-'+suffix+'.json'),{'state':'passed','cohort':COHORT,**budget.record()})
        budget.finish()
        print(json.dumps({'stage':a.stage,'cohort':COHORT,'state':'passed',**budget.record()}))
    except BaseException as error:
        failure=type(error).__name__;budget.latch('phase_or_infrastructure_failed');raise
    finally:
        budget.stop.set()
        if budget.thread is not None:budget.thread.join()
        if failure or budget.reason:s.write_json(w/'evidence'/(a.stage+'-failure.json'),{'exception_type':failure,**budget.record()})


if __name__=='__main__':main()
