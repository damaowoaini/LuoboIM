#!/usr/bin/env python3
"""Explicit current359/source535 identity; immutable354 core, no fresh bind."""
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
import current_reuse as recovery
import support as s
import verify_inputs
import rich_current
import plugin_scope
import plugin_scope_fixture
import classpaths_current as classpaths

parent=generic.parent
sdk_sources=generic.sdk_sources
effective_environment=generic.effective_environment
home_admission=generic.home_admission
check_flutter_environment_sources=generic.check_flutter_environment_sources
check_current_packages=generic.check_current_packages

COHORT='current359-null2-plugin-scope-v2'
SOURCE_REF='0e1cb1ae5f3edc3db8a0a94970b363a4cd6ae3b1'
STAGES=['setup','restore','preseal','gradle-setup','apk']


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
    if (inputs['schema']!='luobo.android.current359-inputs.v1'
            or context['schema']!='luobo.android.current359-context.v1'
            or inputs['cohort']!=COHORT or context['cohort']!=COHORT
            or context['source_ref']!=SOURCE_REF
            or inputs['members']!=535 or context['source_members']!=535
            or len(rows)!=535 or len({r['path']for r in rows})!=535
            or any(set(r)!={'path','bytes','sha256'}for r in rows)
            or inputs['source_digest']!=context['source_digest']
            or generic.digest(rows)!=context['source_digest']
            or len(business)!=359 or context['business_members']!=359
            or generic.digest(business)!=context['business_rows_digest']
            or len(portable)!=411 or context['portable_members']!=411
            or generic.digest(portable)!=context['portable_digest']):
        raise ValueError('exact published359/source535/portable411 identity required')
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
        raise ValueError('exact reviewed current359 context SHA required before setup')


def metadata():
    # Read-only validation of historical libraries; no old CLI/stage invocation.
    spec,old_context,original,tools=recovery.metadata()
    context=read(HERE/'cohort.json');inputs=validate_inputs(read(HERE/'inputs.json'),context)
    for row in context['controller_inputs']+context['borrowed_inputs']:
        p=verify_inputs.checked_path(REPO,row['path'])
        if p.stat().st_size!=row['bytes']or s.sha(p)!=row['sha256']:
            raise ValueError('explicit359 source controller or borrowed input differs')
    require_core_equivalence(inputs,original,context)
    plugin_scope.contract(HERE,inputs)
    check_catalogue(context)
    if (context['original_core_spec_sha256']!=s.sha(generic.HERE/'core-reuse.json')
            or context['original_context_sha256']!=s.sha(generic.HERE/'current-app-cohort.json')
            or context['pub_lock_after']!=old_context['pub_lock_after']
            or context['flutter_environment_sources']!=old_context['flutter_environment_sources']
            or context['webview_archive_identities']!=old_context['webview_archive_identities']):
        raise ValueError('exact original helper/core and unchanged native dependency provenance required')
    return context,inputs,tools,spec,old_context,original


class CoreImporter(recovery.Job):
    """Use the exact original restore/audits with the ORIGINAL354 source identity."""
    def __init__(self,owner,spec,old_context,original):
        super().__init__(owner.w,original,owner.tools,owner.b,old_context,spec)
        self.owner=owner

    def verify(self):
        self.owner.verify()
        require_core_equivalence(self.owner.inputs,self.inputs,self.owner.context)


class Job(generic.Job):
    def __init__(self,work,inputs,tools,budget,context,spec,old_context,original):
        super().__init__(work,inputs,tools,budget,context)
        self.spec=spec;self.old_context=old_context;self.original=original

    def verify(self):
        validate_inputs(self.inputs,self.context)
        require_core_equivalence(self.inputs,self.original,self.context)
        for row in self.inputs['files']:
            path=verify_inputs.checked_path(self.w/'source',row['path'])
            if path.stat().st_size!=row['bytes']or s.sha(path)!=row['sha256']:
                raise ValueError('current359 exact source body differs')

    def source(self,env):
        checkout=self.w/'source-git'
        self.git(checkout,generic.parent.SOURCE_REPO,SOURCE_REF,env,'current359-source')
        archive=self.w/'downloads/current359-source.tar';wanted={r['path']:r for r in self.inputs['files']}
        self.cmd(['git','-C',checkout,'archive','--format=tar','--output='+str(archive),
            SOURCE_REF,'--',*wanted],self.w,env,'current359-source-archive')
        seen=set()
        with tarfile.open(archive)as bundle:
            for member in bundle:
                if member.isdir():continue
                if not member.isfile()or member.name not in wanted or member.name in seen:
                    raise ValueError('unexpected current359 source member')
                body=bundle.extractfile(member).read();row=wanted[member.name]
                if len(body)!=row['bytes']or hashlib.sha256(body).hexdigest()!=row['sha256']:
                    raise ValueError('fixed published535 body differs')
                s.put(self.w/'source'/s.name(member.name),body);seen.add(member.name)
        if seen!=wanted.keys():raise ValueError('incomplete source535')
        self.verify()
        s.write_json(self.w/'evidence/current359-source.json',{'cohort':COHORT,'source_ref':SOURCE_REF,
            'members':535,'App_members':359,'source_digest':self.inputs['source_digest'],
            'archive_sha256':s.sha(archive),'old354_source_not_relabelled':True})

    def build_core(self):
        raise ValueError('fresh Go bind disabled; only audited original354 core may be imported')

    def builder(self,*args,**kwargs):
        raise ValueError('core builder disabled for immutable-core current359 App scope')

    def restore(self):
        importer=CoreImporter(self,self.spec,self.old_context,self.original)
        importer.restore()  # Original custody, all members, raw/locks/ELF/API audits.
        original=self.w/'evidence/core-import.json'
        s.write_json(self.w/'evidence/current359-core-import.json',{'cohort':COHORT,
            'App_source_ref':SOURCE_REF,'App_source_digest':self.inputs['source_digest'],
            'original354_import_proof_sha256':s.sha(original),'original':read(original),
            'core_compatibility':self.context['core_compatibility'],
            'Go_bind_in_this_job':False,'imported_core_is_new_CA':False,
            'device_or_service_or_fixture_TLS_accepted':False})

    def prepare(self):
        w, t = self.w, self.tools
        for name in ['evidence', 'artifacts', 'downloads', 'tmp', 'home', 'config', 'pub-cache',
                     'gradle-cache', 'job-private']:
            s.mkdir(w / name)
        e = parent.environment(w, False)
        self.source(e)
        for key in ['jdk', 'gradle']:
            self.tool(key, t[key], w / key, e)
        self.tool('commandline', t['android_commandline_tools'], w / 'android/cmdline-tools/22.0', e)
        self.tool('ndk', t['ndk'], w / 'android/ndk/28.2.13676358', e)
        for label, args in [('java-version', [w / 'jdk/bin/java', '-version']),
                            ('gradle-version', [w / 'gradle/bin/gradle', '--version'])]:
            self.cmd(args, w, e, label)
        if '17.0.20.1' not in (w / 'evidence/java-version.log').read_text():
            raise ValueError('JDK version mismatch')
        sdk = w / 'android/cmdline-tools/22.0/bin/sdkmanager'
        self.cmd([sdk, '--sdk_root=' + str(w / 'android'), '--licenses'], w, e, 'sdk-licenses', stdin=b'y\n' * 1000)
        extra = t['cloud_dependency_additions']
        packages = t['android_sdk']['required_packages'] + extra['required_SDK_packages']
        catalog = w / 'evidence/official-SDK-repository2-3.xml'
        catalog_receipt = s.download({'url': 'https://dl.google.com/android/repository/repository2-3.xml',
                                     'max_bytes': 16 * 1024 ** 2}, catalog, self.b)
        selected_archives = parent.sdk_catalog(catalog.read_bytes(), packages)
        self.cmd([sdk, '--verbose', '--sdk_root=' + str(w / 'android')] + [x['path'] for x in packages], w, e, 'sdk-packages', 1200)
        sdk_rows = []
        for item in packages:
            root = w / 'android' / item['path'].replace(';', '/')
            prop = (root / 'source.properties').read_text()
            if not re.search(r'^Pkg.Revision\s*=\s*' + re.escape(item['revision']) + r'\s*$', prop, re.M):
                raise ValueError('SDK package revision mismatch: ' + item['path'])
            sdk_rows.append({'package': item['path'], 'revision': item['revision'],
                'source_properties_sha256': s.sha(root / 'source.properties'),
                'installed_package_xml_sha256': s.sha(root / 'package.xml')})
        for pin in t['android_sdk']['same_crossplatform_API_inputs'] + extra['crossplatform_API_inputs']:
            if s.sha(w / 'android' / pin['path']) != pin['sha256']:
                raise ValueError('Android platform API input SHA mismatch')
        s.write_json(w / 'evidence/sdk-packages.json', {'packages': sdk_rows,
            'official_repository_metadata': catalog_receipt, 'selected_official_Linux_archives': selected_archives,
            'source': 'hash-verified sdkmanager22.0 validates archive checksums; verbose log and exact platform bytes corroborate installation',
            'Linux_archive_SHA256_preexisting_lock': False})
        flutter = t['flutter']
        self.git(w / 'flutter', flutter['repository'], flutter['framework_revision'], e, 'flutter')
        self.cmd(['git', '-C', w / 'flutter', 'fetch', '--depth=1', 'origin', 'refs/tags/' + flutter['tag'] + ':refs/tags/' + flutter['tag']], w, e, 'flutter-tag')
        self.cmd(['git', '-C', w / 'flutter', 'checkout', '--quiet', '--detach', 'refs/tags/' + flutter['tag']], w, e, 'flutter-checkout')
        if (w / 'flutter/bin/internal/engine.version').read_text().strip() != flutter['engine_revision']:
            raise ValueError('Flutter engine revision mismatch')
        f = w / 'flutter/bin/flutter'
        self.cmd([f, 'config', '--no-analytics'], w, e, 'flutter-config')
        version = self.cmd([f, '--version', '--machine'], w, e, 'flutter-version')
        v = json.loads(version.read_text())
        for key, want in [('frameworkVersion', flutter['tag']), ('frameworkRevision', flutter['framework_revision']),
                          ('engineRevision', flutter['engine_revision']), ('dartSdkVersion', flutter['dart_version'])]:
            if v.get(key) != want:
                raise ValueError('official Flutter version mismatch: ' + key)
        self.cmd([f, 'precache', '--android'], w, e, 'android-engine', 1200)
        self.cmd([f, 'pub', 'get', '--enforce-lockfile'], w / 'source/app', e, 'pub-get', 1200)
        self.check_packages()
        for pin in extra['locked_pub_source_evidence']:
            if s.sha(w / 'pub-cache/hosted/pub.dev' / pin['path']) != pin['sha256']:
                raise ValueError('locked native dependency build source differs')
        asset = t['sqlite_Android_ARM64_if_hook_reachable']
        package = w / 'pub-cache/hosted/pub.dev/sqlite3-3.5.2'
        if ("'" + asset['filename'] + "': '" + asset['sha256'] + "'") not in (package / 'lib/src/hook/asset_hashes.dart').read_text():
            raise ValueError('locked SQLite Android native hash differs')
        target = w / 'source' / asset['fresh_seed_relative']
        s.mkdir(target.parent)
        receipt = s.download(asset, target, self.b)
        s.write_json(w / 'evidence/sqlite-Android-native.json', {**receipt, 'old_hook_snapshot_or_stamp': False})
        generator_files = ['packages/flutter_tools/lib/src/' + name for name in
                           ['commands/packages.dart', 'project.dart', 'android/gradle_utils.dart', 'base/utils.dart']]
        parent.update_job_local_properties(w, {'Flutter_commit': flutter['framework_revision'],
            'pub_get_resource_sha256': s.sha(w / 'evidence/pub-get.resource.json'),
            'generator_sources': [{'path': name, 'sha256': s.sha(w / 'flutter' / name)} for name in generator_files]})
        self.verify()
        ca = w / 'job-private/job-ca.pem'
        key = w / 'job-private/job-ca.key'
        self.cmd(['openssl', 'version'], w, e, 'openssl-version')
        if not (w / 'evidence/openssl-version.log').read_text().startswith('OpenSSL 3.'):
            raise ValueError('OpenSSL3 required for reviewed job CA generation')
        self.cmd(['openssl', 'req', '-x509', '-newkey', 'rsa:3072', '-sha256', '-noenc', '-days', '2',
                  '-keyout', key, '-out', ca, '-subj', '/CN=LuoboIM-current359-cloud-App-job',
                  '-addext', 'basicConstraints=critical,CA:TRUE', '-addext', 'keyUsage=critical,keyCertSign,cRLSign'], w, e, 'job-CA')
        key.chmod(0o600)
        key.unlink()
        s.sync(key.parent)
        s.put(w / 'artifacts/job-ca-public.pem', ca.read_bytes())
        s.write_json(w / 'evidence/job-CA.json', {'public_SHA256': s.sha(ca), 'private_key_deleted': True,
            'source': 'fresh job-only2day CA, not old fixture CA', 'system_trust_modified': False})
        s.write_json(w / 'evidence/runner-system.json', {'ImageOS': os.environ.get('ImageOS'),
            'ImageVersion': os.environ.get('ImageVersion'), 'RUNNER_ARCH': os.environ.get('RUNNER_ARCH'),
            'Python': sys.version, 'openssl_version_log_sha256': s.sha(w / 'evidence/openssl-version.log')})
        s.write_json(w/'evidence/current359-setup-scope.json',{'cohort':COHORT,
            'current_tools':'JDK/Gradle/Android/Flutter/Pub/SQLite only',
            'Go_acquisition_or_command':False,'gomobile_or_gobind_acquisition_or_command':False,
            'core_builder_or_metadata_warm':False,'original_core_tool_identity_from_imported_receipts':True})
        self.verify()
        plugin_scope.produce(self,HERE)

    def cmd(self,args,cwd,env,label,timeout=900,reject=None,stdin=None):
        # Original helper calls provide legacy *intent* env. Validate those
        # boundaries first, then use the explicit effective env at dispatch.
        executable=Path(args[0])
        if executable in (self.w/'go/bin/go',self.w/'mobile-tools/gomobile',self.w/'mobile-tools/gobind'):
            raise ValueError('Go and bind tools are not current359 App tools')
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
            result=parent.Job.gradle_prepare(self)
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
        target=self.w/'artifacts/luobo-current359-auditable-arm64-debug.apk';s.put(target,source.read_bytes())
        s.write_json(self.w/'evidence/current359-APK-audit.json',{**proof,'cohort':COHORT,
            'source_members':535,'App_members':359,'source_ref':SOURCE_REF,
            'published_APK':str(target.relative_to(self.w)),'published_SHA256':s.sha(target),
            'inherited_template_SHA256':s.sha(inherited),'core_origin':read(self.w/'evidence/current359-core-import.json'),
            'legacy_false_is_not_new_core_or_new_CA_proof':True,
            'new_setup_CA_not_installed_into_original_core':True,
            'device_or_service_or_fixture_TLS_accepted':False})


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--cohort',required=True,choices=[COHORT]);p.add_argument('--stage',choices=['readonly',*STAGES],default='readonly')
    p.add_argument('--work',type=Path)
    p.add_argument('--approved-context-sha256',default='');a=p.parse_args()
    context,inputs,tools,spec,old_context,original=metadata()
    if a.stage=='readonly':
        print(json.dumps({'cohort':COHORT,'source_members':535,'App_members':359,
            'state':'source_identity_and_approved_JVM18_only','native_execution':False}));return
    require_reviewed_consumer(context,a.approved_context_sha256)  # BEFORE work creation, tokens or downloads.
    if a.work is None or sys.platform!='linux'or os.geteuid()==0:p.error('ordinary-user Linux fresh root required')
    w=generic.parent.work_path(str(a.work),a.stage=='setup');generic.home_admission(os.environ['HOME'])
    if a.stage=='setup':
        s.mkdir(w);s.mkdir(w/'evidence')
        s.write_json(w/'current359-activation.json',{'cohort':COHORT,'source_ref':SOURCE_REF,
            'context_sha256':s.sha(HERE/'cohort.json'),'inputs_sha256':s.sha(HERE/'inputs.json'),
            'controller_commit':os.environ.get('GITHUB_SHA')})
    else:
        activation=read(w/'current359-activation.json');previous=STAGES[STAGES.index(a.stage)-1]
        if (activation['cohort']!=COHORT or activation['source_ref']!=SOURCE_REF
                or activation['context_sha256']!=s.sha(HERE/'cohort.json')
                or activation['inputs_sha256']!=s.sha(HERE/'inputs.json')
                or any(read(w/'evidence'/(previous+'-'+suffix+'.json'))['state']!='passed'for suffix in ['complete','final-resource'])
                or (w/'evidence'/(previous+'-failure.json')).exists()):
            raise ValueError('exact current359 previous phase required')
        if any(os.statvfs(v).f_bavail*os.statvfs(v).f_frsize<s.INITIAL for v in [w,REPO]):
            raise ValueError('phase4GiB admission')
    s.write_json(w/'evidence'/(a.stage+'-claim.json'),{'cohort':COHORT,'claimed_once':True})
    def interrupted(_number,_frame):raise InterruptedError('current359 phase interrupted')
    for number in [signal.SIGINT,signal.SIGTERM]:signal.signal(number,interrupted)
    if a.stage=='restore':
        def expired(_number,_frame):raise TimeoutError('original core import exceeded600seconds')
        signal.signal(signal.SIGALRM,expired);signal.alarm(600)
    budget=s.Budget(w,REPO);failure=None
    try:
        budget.start();job=Job(w,inputs,tools,budget,context,spec,old_context,original)
        {'setup':job.prepare,'restore':job.restore,'preseal':job.preseal,'gradle-setup':job.gradle_prepare,'apk':job.build_apk}[a.stage]()
        metadata();generic.home_admission(os.environ['HOME'])
        for suffix in ['complete','final-resource']:
            s.write_json(w/'evidence'/(a.stage+'-'+suffix+'.json'),{'state':'passed','cohort':COHORT,**budget.record()})
        budget.finish()
        print(json.dumps({'stage':a.stage,'cohort':COHORT,'state':'passed',**budget.record()}))
    except BaseException as error:
        failure=type(error).__name__;budget.latch('phase_or_infrastructure_failed');raise
    finally:
        if a.stage=='restore':signal.alarm(0)
        budget.stop.set()
        if budget.thread is not None:budget.thread.join()
        if failure or budget.reason:s.write_json(w/'evidence'/(a.stage+'-failure.json'),{'exception_type':failure,**budget.record()})


if __name__=='__main__':main()
