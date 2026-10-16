#!/usr/bin/env python3
"""Explicit current354 production consumer; historical257 libraries stay frozen."""
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

import observed_bind as old
import run as parent
import support as s
import verify_inputs
import gradle_classpath_catalog as classpaths
import rich_consumer
import sdk_platform_producer as sdk_sources

HERE=Path(__file__).resolve().parent
COHORT='current354-auditable-arm64-production-v1'
SOURCE_REF='ff7bc1f588f935d6315ad0211e68e15e58cd037e'
STAGES=['setup','core','preseal','gradle-setup','apk']


def digest(rows):
    return hashlib.sha256(json.dumps(sorted(rows,key=lambda r:r['path']),ensure_ascii=False,
        sort_keys=True,separators=(',',':')).encode()).hexdigest()


def business(rows):
    return [r for r in rows if r['path'].startswith(('app/lib/','app/test/','app/tool/'))
        or r['path'] in ('app/analysis_options.yaml','app/pubspec.yaml','app/pubspec.lock')]


def validate_inputs(value,context):
    rows=value['files']
    if (value['cohort']!=COHORT or context['cohort']!=COHORT or len(rows)!=530
            or value['members']!=530 or len({r['path'] for r in rows})!=530
            or any(set(r)!={'path','bytes','sha256'} for r in rows)
            or digest(rows)!=context['source_digest'] or value['source_digest']!=context['source_digest']
            or len(business(rows))!=354 or digest(business(rows))!=context['business_rows_digest']):
        raise ValueError('exact current354/source530 identity required')
    for row in rows:
        if s.name(row['path'])!=row['path'] or not re.fullmatch('[0-9a-f]{64}',row['sha256']):
            raise ValueError('source row refused')
    native=context['native_source_snapshot']
    portable=business(rows)+[r for r in rows if
        r['path'].startswith('packages/flutter_openim_sdk/lib/') or r['path'] in (
            'packages/flutter_openim_sdk/pubspec.yaml',
            'packages/flutter_openim_sdk/tool/fixture_native_endpoint_messenger.dart',
            'migration/legacy_personal_export/legacy_personal_export.dart')]
    indexed={r['path']:r for r in rows}
    if (context['source_ref']!=SOURCE_REF or context['business_members']!=354
            or context['source_members']!=530 or context['portable_members']!=406
            or len(portable)!=406 or digest(portable)!=context['portable_digest']
            or native['members']!=7 or len(native['files'])!=7
            or len({r['path'] for r in native['files']})!=7
            or digest(native['files'])!=native['digest']
            or any(indexed.get(r['path'])!=r for r in native['files'])
            or context['pub_lock_after']!=indexed['app/pubspec.lock']
            or context['locked_packages']!=178):
        raise ValueError('exact published354/portable406/native7/lock178 binding required')
    return value


def metadata():
    # Validates the borrowed execution libraries, never treats old428 as354.
    _,_,tools=old.metadata()
    context=json.loads((HERE/'current-app-cohort.json').read_text())
    if context['cohort']!=COHORT or context['schema']!='luobo.android.current-app.v2':
        raise ValueError('explicit current App context required')
    for row in context['controller_inputs']:
        path=verify_inputs.checked_path(parent.REPO,row['path'])
        if path.stat().st_size!=row['bytes'] or s.sha(path)!=row['sha256']:
            raise ValueError('current controller input differs')
    inputs=validate_inputs(json.loads((HERE/'current-app-inputs.json').read_text()),context)
    if (not re.fullmatch('[0-9a-f]{40}',context['source_ref'])
            or context['pub_lock_after']!=next(r for r in inputs['files'] if r['path']=='app/pubspec.lock')):
        raise ValueError('fixed source ref/current pub lock identity required')
    tools=copy.deepcopy(tools)
    if tools['locks']['app_pubspec_lock']!=context['pub_lock_before']:
        raise ValueError('historical toolchain lock changed')
    tools['locks']['app_pubspec_lock']=context['pub_lock_after']
    return context,inputs,tools


def effective_environment(value,work,actual_home):
    """Explicit process adapter; no global/module mutation or inherited ambient env."""
    work=Path(work);home=Path(actual_home)
    if not home.is_absolute() or not value.get('PATH') or value.get('ANDROID_NATIVE_WORK')!=str(work):
        raise ValueError('fixed environment context required')
    if value.get('HOME') not in (str(work/'home'),str(home)):
        raise ValueError('unexpected source HOME')
    result=dict(value)
    result.update(HOME=str(home),GOENV='off',GOAUTH='off',GIT_CONFIG_GLOBAL='/dev/null',
        GIT_CONFIG_NOSYSTEM='1',GIT_TERMINAL_PROMPT='0',GIT_NO_REPLACE_OBJECTS='1',
        JAVA_TOOL_OPTIONS='-Duser.home='+str(work/'home')+' -Djava.io.tmpdir='+str(work/'tmp')+' -XX:-UsePerfData')
    if result.get('GOPROXY')=='https://proxy.golang.org,direct':result['GOPROXY']='https://proxy.golang.org'
    owned=('TMPDIR','XDG_CONFIG_HOME','XDG_CACHE_HOME','PUB_CACHE','GOPATH','GOBIN','GOCACHE',
        'GOMODCACHE','GRADLE_USER_HOME','ANDROID_HOME','ANDROID_NDK_HOME','ANDROID_USER_HOME')
    for key in owned:
        path=Path(result[key])
        if not path.is_absolute() or not path.is_relative_to(work) or '..' in path.parts:
            raise ValueError('cache/config outside owned workspace')
    for key in result:
        if key.upper().endswith('_PROXY') or key.startswith(('ACTIONS_','GITHUB_TOKEN')) or key in ('SSL_CERT_FILE','SSL_CERT_DIR'):
            raise ValueError('ambient network/credential environment refused')
    if result['FLUTTER_SUPPRESS_ANALYTICS']!='true' or result['DART_SUPPRESS_ANALYTICS']!='true':
        raise ValueError('analytics must remain suppressed before initialization')
    return result


def home_admission(home):
    # Fixed Flutter Config prioritizes these existing HOME files over XDG.
    # Reject without reading/removing them; no user configuration migration.
    home=Path(home)
    for name in ('.flutter_settings','.flutter_tool_state','.flutter_features'):
        path=home/name
        if path.exists() or path.is_symlink():
            raise ValueError('existing HOME Flutter configuration requires separate review')


def check_flutter_environment_sources(work,rows):
    for row in rows:
        path=verify_inputs.checked_path(Path(work)/'flutter',row['path'])
        if path.stat().st_size!=row['bytes'] or s.sha(path)!=row['sha256']:
            raise ValueError('fixed Flutter environment entry source differs')


def check_current_packages(work,context):
    """Same enforced lock plus four official Pub hash receipts, not native proof."""
    work=Path(work)
    lock=verify_inputs.checked_path(work/'source','app/pubspec.lock')
    pin=context['pub_lock_after']
    if (lock.stat().st_size!=pin['bytes'] or s.sha(lock)!=pin['sha256']
            or len(re.findall(r'^  [a-zA-Z0-9_]+:$',lock.read_text(),re.M))!=178):
        raise ValueError('exact current178 pub lock required after pub get')
    rows=context['webview_archive_identities']
    if len(rows)!=4 or {r['name'] for r in rows}!={
            'webview_flutter','webview_flutter_android','webview_flutter_platform_interface','webview_flutter_wkwebview'}:
        raise ValueError('finite four official WebView archive identities required')
    evidence=[]
    for row in rows:
        name=row['name']+'-'+row['version']
        receipt=verify_inputs.checked_path(work/'pub-cache','hosted-hashes/pub.dev/'+name+'.sha256')
        package=work/'pub-cache/hosted/pub.dev'/name
        if (receipt.read_bytes()!=row['archive_sha256'].encode('ascii')
                or not package.is_dir() or package.resolve(strict=True)!=package):
            raise ValueError('fresh official WebView archive hash/package differs')
        evidence.append(dict(row,hash_file_SHA256=s.sha(receipt)))
    return {'entries':evidence,'locked_packages':178,'pub_enforce_lockfile':True,
        'independent_archive_body_rehash':False,'old_cache_copied':False,'native_acceptance':False}


class Job(old.Job):
    def __init__(self,work,inputs,tools,budget,context):
        super().__init__(work,inputs,tools,budget)
        self.context=context

    def verify(self):
        validate_inputs(self.inputs,self.context)
        for row in self.inputs['files']:
            path=verify_inputs.checked_path(self.w/'source',row['path'])
            if path.stat().st_size!=row['bytes'] or s.sha(path)!=row['sha256']:
                raise ValueError('current354 source differs')

    def source(self,env):
        checkout=self.w/'source-git'
        self.git(checkout,parent.SOURCE_REPO,self.context['source_ref'],env,'current-source')
        archive=self.w/'downloads/current-source.tar'
        wanted={r['path']:r for r in self.inputs['files']}
        self.cmd(['git','-C',checkout,'archive','--format=tar','--output='+str(archive),
            self.context['source_ref'],'--',*wanted],self.w,env,'current-source-archive')
        seen=set()
        with tarfile.open(archive) as bundle:
            for member in bundle:
                if member.isdir():continue
                if not member.isfile() or member.name not in wanted or member.name in seen:
                    raise ValueError('unexpected exact source archive member')
                body=bundle.extractfile(member).read();row=wanted[member.name]
                if len(body)!=row['bytes'] or hashlib.sha256(body).hexdigest()!=row['sha256']:
                    raise ValueError('fixed public current354 source differs')
                s.put(self.w/'source'/s.name(member.name),body);seen.add(member.name)
        if seen!=wanted.keys():raise ValueError('incomplete source530')
        self.verify()
        s.write_json(self.w/'evidence/current-source.json',{'cohort':COHORT,
            'source_ref':self.context['source_ref'],'members':530,'App_members':354,
            'source_digest':self.inputs['source_digest'],'archive_sha256':s.sha(archive),
            'historical_helper_identity_does_not_label_this_App':True})

    def cmd(self,args,cwd,env,label,timeout=900,reject=None,stdin=None):
        # Original helper calls provide legacy *intent* env. Validate those
        # boundaries first, then use the explicit effective env at dispatch.
        binding=None
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
            args=list(args);args[args.index('-I')+1]=HERE/'resolve-observed.gradle'
            binding=classpaths.verified_catalog(self.w);self._typed_resolver_dispatch=True
        elif label=='assemble-debug':
            if (list(map(str,args))!=list(map(str,self.gradle('assembleDebug')))
                    or cwd!=self.w/'source/app/android' or env!=parent.environment(self.w,True)
                    or timeout!=1800 or reject is not None or stdin is not None):
                raise ValueError('fixed production APK dispatch required')
            rich_consumer.require_origin_binding(self.w,HERE)
            binding=classpaths.verified_catalog(self.w);args=list(args)+['-I',HERE/'resolve-observed.gradle']
        if binding is not None:
            args=list(args)+['-DluoboClasspathCatalog='+str(binding[0]),'-DluoboClasspathCatalogSha256='+binding[1]]
        if Path(args[0])==self.w/'gradle/bin/gradle':
            args=list(args)+['-I',HERE/'owned-outputs.gradle','-DluoboProductionWork='+str(self.w),
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
                    rich_consumer.check_apk_reports(self.w,HERE),'unused_configurations_not_claimed':True})
            return result
        finally:
            if binding is not None and classpaths.verified_catalog(self.w)!=binding:
                raise ValueError('producer catalog changed during actual Gradle dispatch')

    def gradle_prepare(self):
        self._typed_resolver_dispatch=False
        try:
            # Same actual base resolution/origin/verification XML, no old6 fixture.
            result=parent.Job.gradle_prepare(self)
            if self._typed_resolver_dispatch is not True:raise ValueError('production resolver not dispatched')
            rich_consumer.bind_origins(self.w,HERE,s.write_json)
            return result
        finally:self._typed_resolver_dispatch=None

    def preseal(self):
        parent.netns();self.verify();classpaths.seal(self,HERE)

    def build_core(self):
        super().build_core()
        # Preserve the inherited internal SDK-only filename/proof, publish an
        # explicitly labelled copy. This is not reuse of an old binary.
        source=self.w/'artifacts/core-current257-arm64.aar'
        for a,b in [(source,self.w/'artifacts/core-current354-arm64.aar'),
                (source.with_suffix('.build.json'),self.w/'artifacts/core-current354-arm64.build.json'),
                (source.with_name(source.stem+'-sources.jar'),self.w/'artifacts/core-current354-arm64-sources.jar')]:
            s.put(b,a.read_bytes())
        s.write_json(self.w/'evidence/current-core-publication.json',{'cohort':COHORT,
            'source_members':530,'App_members':354,'source_digest':self.inputs['source_digest'],
            'internal_helper_observer_protocol':old.COHORT,'not_a_historical_App_or_binary':True,
            'AAR_sha256':s.sha(source),'original_manifest_sha256':s.sha(source.with_suffix('.build.json'))})

    def build_apk(self):
        parent.Job.build_apk(self)  # Actual original7 gates and actual App compile.
        inherited=self.w/'evidence/APK-audit.json';proof=json.loads(inherited.read_text())
        if proof['source_members']!=427 or proof['source_digest']!=self.inputs['source_digest']:
            raise ValueError('unexpected inherited audit template')
        source=self.w/'artifacts/luobo-current257-auditable-arm64-debug.apk'
        target=self.w/'artifacts/luobo-current354-auditable-arm64-debug.apk';s.put(target,source.read_bytes())
        s.write_json(self.w/'evidence/current-APK-audit.json',{**proof,'cohort':COHORT,
            'source_members':530,'App_members':354,'source_ref':self.context['source_ref'],
            'published_APK':str(target.relative_to(self.w)),'published_SHA256':s.sha(target),
            'inherited_template':{'path':str(inherited.relative_to(self.w)),'sha256':s.sha(inherited),
                'hardcoded427_is_not_current_source_count':True},'device_acceptance':False})


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--cohort',required=True,choices=[COHORT]);p.add_argument('--stage',choices=['readonly','admission',*STAGES],default='readonly');p.add_argument('--work');a=p.parse_args()
    if a.stage!='readonly':
        if not a.work or sys.platform!='linux' or os.geteuid()==0:p.error('fresh ordinary-user Linux cloud root required')
        w=parent.work_path(a.work,a.stage in ('admission','setup'))
    context,inputs,tools=metadata()
    if a.stage=='readonly':print(json.dumps({'cohort':COHORT,'source_members':530,'App_members':354,'state':'readonly_no_native'}));return
    if a.stage=='admission':print('COLD20GiB_ADMITTED_NO_SETUP');return
    home_admission(os.environ['HOME'])
    if a.stage=='setup':
        s.mkdir(w);s.mkdir(w/'evidence');s.write_json(w/'activation.json',{'cohort':COHORT,
            'context_sha256':s.sha(HERE/'current-app-cohort.json'),'inputs_sha256':s.sha(HERE/'current-app-inputs.json'),
            'source_ref':context['source_ref'],'controller_commit':os.environ.get('GITHUB_SHA')})
    else:
        activation=json.loads((w/'activation.json').read_text());previous=STAGES[STAGES.index(a.stage)-1]
        if (activation['cohort']!=COHORT or activation['context_sha256']!=s.sha(HERE/'current-app-cohort.json')
                or activation['inputs_sha256']!=s.sha(HERE/'current-app-inputs.json')
                or activation['source_ref']!=context['source_ref']
                or json.loads((w/'evidence'/(previous+'-complete.json')).read_text())['state']!='passed'
                or json.loads((w/'evidence'/(previous+'-final-resource.json')).read_text())['state']!='passed'
                or (w/'evidence'/(previous+'-failure.json')).exists()):raise ValueError('exact successful prior activation required')
        if any(os.statvfs(path).f_bavail*os.statvfs(path).f_frsize<s.INITIAL for path in (w,parent.REPO)):
            raise ValueError('next phase4GiB admission')
    s.write_json(w/'evidence'/(a.stage+'-claim.json'),{'claimed_once':True})
    def interrupted(_number,_frame):raise InterruptedError('current Android guard interrupted')
    for number in (signal.SIGINT,signal.SIGTERM):signal.signal(number,interrupted)
    budget=s.Budget(w,parent.REPO);failure=None
    try:
        budget.start();job=Job(w,inputs,tools,budget,context)
        {'setup':job.prepare,'core':job.build_core,'preseal':job.preseal,'gradle-setup':job.gradle_prepare,'apk':job.build_apk}[a.stage]()
        metadata();home_admission(os.environ['HOME'])
        s.write_json(w/'evidence'/(a.stage+'-complete.json'),{'state':'passed','cohort':COHORT,**budget.record()})
        s.write_json(w/'evidence'/(a.stage+'-final-resource.json'),{'state':'passed','cohort':COHORT,**budget.record()})
        budget.finish();print(json.dumps({'stage':a.stage,'state':'passed','cohort':COHORT,**budget.record()}))
    except BaseException as error:
        failure=type(error).__name__+': '+str(error);budget.latch('phase_or_infrastructure_failed');raise
    finally:
        budget.stop.set()
        if budget.thread is not None:budget.thread.join()
        if failure or budget.reason:
            s.write_json(w/'evidence'/(a.stage+'-failure.json'),{'failure':failure,**budget.record()})


if __name__=='__main__':main()
