#!/usr/bin/env python3
"""Independent finite3/source18 JVM-context reprojection. No native/App/Go entry."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import signal
import sys
import traceback
import uuid

HERE=Path(__file__).resolve().parent
REPO=HERE.parents[1]
COHORT='android-rich-jvm-context-projection-r1'
GIB=1024**3
ORIGINAL_GRAPH_SHA256='9c1a08f41f9cb50ab563b40d8abe7d94d8595b194451f6ef1ad34111216d8f36'
JVM_ATTRIBUTES={
    'org.gradle.usage':{'type':'java.lang.String','value':'java-runtime'},
    'org.gradle.libraryelements':{'type':'java.lang.String','value':'jar'},
    'org.gradle.category':{'type':'java.lang.String','value':'library'},
}
# First configuration: 12 actual consumer observations. Second configuration:
# four source-predicted POMs, NOT observed production variants or a new grant.
POM_INCREMENTS={
    'kotlinAbiValidationCompatClasspath':frozenset([
        ('org.jetbrains','annotations','13.0'),
        ('org.jetbrains.kotlin','kotlin-build-tools-api','2.4.21'),
        ('org.jetbrains.kotlin','kotlin-build-tools-compat','2.4.0'),
        ('org.jetbrains.kotlin','kotlin-build-tools-cri-impl','2.4.21'),
        ('org.jetbrains.kotlin','kotlin-build-tools-impl','2.4.21'),
        ('org.jetbrains.kotlin','kotlin-compiler-embeddable','2.4.21'),
        ('org.jetbrains.kotlin','kotlin-compiler-runner','2.4.21'),
        ('org.jetbrains.kotlin','kotlin-daemon-client','2.4.21'),
        ('org.jetbrains.kotlin','kotlin-daemon-embeddable','2.4.21'),
        ('org.jetbrains.kotlin','kotlin-reflect','1.6.10'),
        ('org.jetbrains.kotlin','kotlin-script-runtime','2.4.21'),
        ('org.jetbrains.kotlin','kotlin-tooling-core','2.4.21'),
    ]),
    'kotlinBouncyCastleConfiguration':frozenset([
        ('org.bouncycastle','bcpg-jdk18on','1.80'),
        ('org.bouncycastle','bcpkix-jdk18on','1.80'),
        ('org.bouncycastle','bcprov-jdk18on','1.80.2'),
        ('org.bouncycastle','bcutil-jdk18on','1.80.2'),
    ]),
}


def load(path,name):
    spec=importlib.util.spec_from_file_location(name,path)
    value=importlib.util.module_from_spec(spec);spec.loader.exec_module(value)
    return value


# Explicit read-only library imports; no old CLI, execute(), environment() or
# module-global rebinding. Its finite HTTP parser/source proofs stay byte exact.
sys.path.insert(0,str(REPO/'ci/android-version-probe'))
prior=load(REPO/'ci/android-version-probe/run.py','jvm_prior_source_verifier')
broker=prior.broker


def read(path): return json.loads(Path(path).read_bytes())
def sha(path): return prior.sha(path)


def metadata():
    registry=read(HERE/'cohort.json')
    if registry['cohort']!=COHORT or registry['native_execution']is not False:
        raise ValueError('independent JVM reprojection source identity')
    rows=registry['files']
    if len({r['path']for r in rows})!=len(rows):raise ValueError('registry overlap')
    for row in rows:
        name=row['path']
        if name.startswith('/')or any(p in {'','.','..'}for p in name.split('/')):
            raise ValueError('finite source path required')
        p=REPO/name
        if prior.regular(p,4*1024**2).st_size!=row['bytes']or sha(p)!=row['sha256']:
            raise ValueError('exact new or borrowed input differs')
    original=read(REPO/'ci/android-version-probe/inputs.json')
    approved=read(REPO/'ci/android-native/rich-catalog.json')
    if (len(original['rich'])!=3 or len(approved['configurations'])!=2
            or sum(len(c['artifacts'])for c in approved['configurations'])!=18
            or sha(REPO/'ci/android-native/rich-catalog.json')!=registry['old_catalog_sha256']):
        raise ValueError('original finite3/18 source authority required')
    original_graph(approved)
    return registry,original,approved


def environment(work):
    # Real HOME retained. Do not reuse historical prior.environment()/offline.sh.
    env={k:os.environ[k]for k in ['HOME','USER','LOGNAME']if k in os.environ}
    env.update(JAVA_HOME=str(work/'jdk'),GRADLE_USER_HOME=str(work/'gradle-home'),
        PATH=str(work/'jdk/bin')+':/usr/bin:/bin',TMPDIR=str(work/'tmp'),TMP=str(work/'tmp'),
        TEMP=str(work/'tmp'),XDG_CACHE_HOME=str(work/'cache'),XDG_CONFIG_HOME=str(work/'config'),
        LANG='C.UTF-8',LC_ALL='C.UTF-8',TZ='UTC',CI='true',PYTHONDONTWRITEBYTECODE='1',
        GIT_CONFIG_NOSYSTEM='1',GIT_CONFIG_GLOBAL='/dev/null',GIT_TERMINAL_PROMPT='0',
        GIT_NO_REPLACE_OBJECTS='1',JAVA_OPTS='-XX:-UsePerfData -Duser.home='+str(work/'user-home'))
    return env


def command(work,endpoint,phase):
    args=prior.command(work,endpoint,phase)
    prefix='-Dorg.gradle.jvmargs='
    matches=[i for i,a in enumerate(args)if isinstance(a,str)and a.startswith(prefix)]
    if len(matches)!=1:raise ValueError('original one JVM argument required')
    args[matches[0]]+=' -XX:-UsePerfData -Djava.io.tmpdir='+str(work/'tmp')+' -Duser.home='+str(work/'user-home')
    return args


def clean_process_environment():
    values={k:os.environ[k]for k in ['HOME','USER','LOGNAME']if k in os.environ}
    if 'HOME'not in values:raise ValueError('actual user HOME required')
    values.update(PATH='/usr/bin:/bin',LANG='C.UTF-8',LC_ALL='C.UTF-8',PYTHONDONTWRITEBYTECODE='1')
    os.environ.clear();os.environ.update(values)  # preserve, never relocate HOME


def original_graph(catalog):
    path=HERE/'original-graph.json'
    prior.regular(path,256*1024)
    if (sha(path)!=ORIGINAL_GRAPH_SHA256
            or catalog['accepted_provenance']['graph_sha256']!=ORIGINAL_GRAPH_SHA256):
        raise ValueError('original accepted complete raw graph required')
    original=read(path)
    # Catalog deliberately projects only four keys. Raw display is not imported
    # into that production contract or authorized by a new live observation.
    for expected in catalog['configurations']:
        raw=next(c for c in original['configurations']if c['name']==expected['name'])
        for a in expected['artifacts']:
            old=next(b for b in raw['artifacts']if b['id']==a['id'])
            if {k:v for k,v in old['variant'].items()if k!='display'}!=a['variant']:
                raise ValueError('original raw/production variant lineage differs')
    return original


def compare_projection(actual,catalog):
    """Only finite POM three-attribute increments; all four-key identities exact."""
    if (set(actual)!={'schema','Gradle','metadataContext','configurations'}
            or actual['schema']!=1 or actual['Gradle']!='9.3.1'
            or actual['metadataContext']!='org.gradle.jvm-ecosystem/9.3.1'):
        raise ValueError('actual fixed builtin JVM context required')
    configs={c['name']:c for c in actual['configurations']}
    if len(actual['configurations'])!=2 or len(configs)!=2 or set(configs)!={c['name']for c in catalog['configurations']}:
        raise ValueError('both exact accepted configurations required')
    canonical=lambda value:json.dumps(value,sort_keys=True,separators=(',',':'))
    original={c['name']:c for c in original_graph(catalog)['configurations']}
    displays=[]
    def identity(value):return tuple(value[k]for k in ['group','module','version'])
    def variant(got,old,name,selected,surface,key):
        if set(got)!=set(old)or set(old)!={'display','owner','attributes','capabilities','external'}:
            raise ValueError('exact raw variant fields required')
        wanted={k:v for k,v in old.items()if k!='display'}
        wanted=json.loads(json.dumps(wanted))
        if identity(selected)in POM_INCREMENTS[name]:
            if set(wanted['attributes'])&set(JVM_ATTRIBUTES):
                raise ValueError('POM increment must add three previously absent attributes')
            wanted['attributes'].update(JVM_ATTRIBUTES)
        if {k:v for k,v in got.items()if k!='display'}!=wanted:
            raise ValueError('four-key variant identity or finite attribute increment differs')
        if not isinstance(got['display'],str)or len(got['display'])>2048:
            raise ValueError('bounded public raw variant display required')
        if got['display']!=old['display']:
            displays.append({'configuration':name,'surface':surface,'key':key,
                'before':old['display'],'after':got['display']})
    for expected in catalog['configurations']:
        got=configs[expected['name']]
        old=original[expected['name']];name=expected['name']
        if set(got)!=set(old):raise ValueError('exact original graph fields required')
        if got['attributes']!=expected['attributes']or sorted(map(canonical,got['declared']))!=sorted(map(canonical,expected['declared'])):
            raise ValueError('requesting configuration/roots changed')
        edges=[]
        for edge in got['edges']:
            if any(r['cause']in {'FORCED','SELECTED_BY_RULE'}for r in edge['selectionReasons']):
                raise ValueError('forced or substituted reprojection refused')
            edges.append({k:edge[k]for k in ['from','requested','selected']})
        if sorted(map(canonical,edges))!=sorted(map(canonical,expected['edges'])):
            raise ValueError('nonvariant parent/selector/selected graph changed')
        bodies=[dict(id=a['id'],filename=Path(a['path']).name,bytes=a['bytes'],sha256=a['sha256'])for a in got['artifacts']]
        want=[{k:a[k]for k in ['id','filename','bytes','sha256']}for a in expected['artifacts']]
        if sorted(map(canonical,bodies))!=sorted(map(canonical,want)):
            raise ValueError('independent original18 body/identity changed')
        for a in got['artifacts']:
            prior_artifact=next(b for b in old['artifacts']if b['id']==a['id'])
            if set(a)!=set(prior_artifact):raise ValueError('exact original artifact fields required')
            variant(a['variant'],prior_artifact['variant'],name,a['id'],'artifact',a['id'])
        components={canonical(c['id']):c for c in got['components']}
        if (len(components)!=len(got['components'])
                or set(components)!={canonical(c['id'])for c in old['components']}):
            raise ValueError('original selected component set differs')
        for c in old['components']:
            selected=components[canonical(c['id'])]
            if set(selected)!=set(c)or len(selected['variants'])!=len(c['variants']):
                raise ValueError('original component variant set differs')
            for index,(new_variant,old_variant)in enumerate(zip(selected['variants'],c['variants'])):
                variant(new_variant,old_variant,name,c['id'],'component',{'id':c['id'],'index':index})
        edge_key=lambda e:canonical({k:e[k]for k in ['from','requested','selected','constraint']})
        old_edges=sorted(old['edges'],key=edge_key);new_edges=sorted(got['edges'],key=edge_key)
        if len(old_edges)!=len(new_edges):raise ValueError('original edge count differs')
        for new_edge,old_edge in zip(new_edges,old_edges):
            if set(new_edge)!=set(old_edge)or edge_key(new_edge)!=edge_key(old_edge):
                raise ValueError('original edge identity or constraint differs')
            variant(new_edge['variant'],old_edge['variant'],name,new_edge['selected'],
                'edge',{k:new_edge[k]for k in ['from','requested','selected','constraint']})
    return {'configurations':2,'artifact_rows':18,'original_requests_selections_components_and_bodies_exact':True,
            'four_key_variant_identity_exact':True,'finite_POM_attribute_increments':16,
            'POM_increment_provenance':{'observed_first_configuration':12,'source_predicted_second_configuration':4},
            'raw_display_deltas_pending_Root':displays,'raw_display_is_production_authority':False,
            'new_variants_are_proposal_only':True,'trusted_catalog_updated':False}


def metadata_bodies(catalog,captured):
    guard=load(REPO/'ci/android-native/rich_consumer.py','jvm_old_metadata_authority')
    known={row['origin'].removeprefix(broker.ORIGIN):row for row in guard.metadata_sources(catalog)}
    coordinates={':'.join(a['id'][k]for k in ['group','module','version'])
                 for c in catalog['configurations']for a in c['artifacts']}
    checked=[]
    for row in captured['responses']:
        parts=row['path'].split('/')
        if not row['path'].endswith(('.pom','.module'))or len(parts)<6:continue
        coordinate='.'.join(parts[2:-3])+':'+parts[-3]+':'+parts[-2]
        if coordinate not in coordinates and row['path']not in known:continue
        expected=known.get(row['path'])
        if (expected is None or row['body']['bytes']!=expected['bytes']
                or row['body']['sha256']!=expected['sha256']):
            raise ValueError('original selected metadata body changed')
        checked.append(row['path'])
    return checked


def execute(stage,work,registry,inputs,catalog):
    prior.admission(work,8*GIB if stage=='acquire'else 4*GIB,stage=='acquire')
    parent,s=prior.load_parent()
    class ProjectionBudget(s.Budget):
        def sample(self):
            free=super().sample()
            if self.peak>4*GIB:self.latch('jvm_projection_owned4GiB_cap')
            self.check();return free
        def record(self):return {**super().record(),'cap_bytes':4*GIB}
    if stage=='acquire':s.mkdir(work)
    for name in ['evidence','downloads','tmp','user-home','cache','config']:s.mkdir(work/name)
    s.write_json(work/'evidence'/(stage+'-claim.json'),{'cohort':COHORT,'state':'reserved_not_passed',
        'registry_sha256':sha(HERE/'cohort.json'),'nonce':uuid.uuid4().hex})
    budget=ProjectionBudget(work,REPO);budget.start();failure=None
    handlers={n:signal.getsignal(n)for n in [signal.SIGTERM,signal.SIGINT]}
    for n in handlers:signal.signal(n,lambda number,frame:budget.latch('external_signal_'+str(number)))
    try:
        env=environment(work)
        if stage=='acquire':
            job=parent.Job(work,None,None,budget)
            for key in ['jdk','gradle']:job.tool(key,inputs['tools'][key],work/key,env)
            s.run([work/'jdk/bin/java','-version'],work,env,work,budget,'java-version',60)
            s.run([work/'gradle/bin/gradle','--no-daemon','--version'],work,env,work,budget,'gradle-version',120)
            s.mkdir(work/'project')
            s.put(work/'project/build.gradle',(HERE/'producer.gradle').read_bytes())
            s.put(work/'project/inputs.json',(REPO/'ci/android-version-probe/inputs.json').read_bytes())
            s.put(work/'project/settings.gradle',b"rootProject.name='independent-jvm-context-source18'\n")
            s.put(work/'project/gradle.properties',('org.gradle.daemon=false\norg.gradle.parallel=false\n'
                'org.gradle.caching=false\norg.gradle.java.installations.auto-download=false\n'
                'org.gradle.java.installations.auto-detect=false\norg.gradle.java.installations.paths='+str(work/'jdk')+'\n').encode())
            sources=prior.source_state(work)
            recorder=broker.Recorder(work,inputs,budget,broker.Central(budget))
            for pin in inputs['metadata_pins']:
                if pin.get('bootstrap',True):recorder.obtain(pin['path'])
            with broker.Broker(recorder)as server:
                s.write_json(work/'evidence/activation.json',{'cohort':COHORT,'work':str(work),
                    'registry_sha256':sha(HERE/'cohort.json'),'endpoint':server.url,'project_sources':sources,
                    'real_HOME_preserved':True,'system_CA_sha256':sha(recorder.origin.ca),
                    'network_boundary':'fixed broker origin/software checks; not OS online isolation'})
                try:s.run(command(work,server.url,'online'),work/'project',env,work,budget,'online-producer',900)
                finally:recorder.snapshot('after-online')
            captured,events=prior.evidence_rows(work)
            actual,origins=prior.verify_graph(work,'online',inputs,captured['responses'],events,captured['published_artifact_pins'])
            projection=compare_projection(actual,catalog)
            pinned_metadata=metadata_bodies(catalog,captured)
            if prior.source_state(work)!=sources:raise ValueError('project sources changed')
            s.write_json(work/'evidence/acquired-catalog.json',{'cohort':COHORT,'graph_sha256':sha(work/'evidence/online-actual.json'),
                'state':'JVM_context_online_only_pending_offline_and_Root','selected_source_rows':origins,
                'strict_projection':projection,'fixed_selected_metadata':pinned_metadata,
                'broker_snapshot_sha256':sha(work/'evidence/broker/after-online.json')})
        else:
            parent.netns()
            activation=read(work/'evidence/activation.json');acquired=read(work/'evidence/acquired-catalog.json')
            if (activation['cohort']!=COHORT or activation['work']!=str(work)
                    or activation['registry_sha256']!=sha(HERE/'cohort.json')
                    or prior.source_state(work)!=activation['project_sources']
                    or acquired['graph_sha256']!=sha(work/'evidence/online-actual.json')):
                raise ValueError('exact new successful acquisition required')
            captured,events=prior.evidence_rows(work)
            if acquired['broker_snapshot_sha256']!=sha(work/'evidence/broker/after-online.json'):
                raise ValueError('original origin capture changed')
            s.run(command(work,activation['endpoint'],'offline'),work/'project',env,work,budget,'offline-producer',900)
            actual,origins=prior.verify_graph(work,'offline',inputs,captured['responses'],events,captured['published_artifact_pins'])
            compare_projection(actual,catalog);metadata_bodies(catalog,captured)
            if ((work/'evidence/online-actual.json').read_bytes()!=(work/'evidence/offline-actual.json').read_bytes()
                    or prior.source_state(work)!=activation['project_sources']):
                raise ValueError('complete online/offline source or graph differs')
            s.write_json(work/'evidence/jvm-catalogue-proposal.json',{'cohort':COHORT,
                'state':'actual_JVM_context_source18_proposal_pending_Root_acceptance',
                'registry_sha256':sha(HERE/'cohort.json'),'graph':actual,'acquired':acquired,
                'trusted_native_catalog_overwritten':False,'App_or_native_claim':False})
        metadata()
        s.write_json(work/'evidence'/(stage+'-candidate.json'),{'cohort':COHORT,
            'state':'candidate_while_polling_terminal_failure_wins','resource':budget.record()})
    except BaseException as error:
        failure=error;budget.latch('phase_or_infrastructure_failed')
        # No response/signed URL/auth strings. Public-source label frames remain in command raw logs.
        frames=[{'path':str(Path(f.filename).relative_to(REPO)),'line':f.lineno,'function':f.name}
                for f in traceback.extract_tb(error.__traceback__)if Path(f.filename).is_relative_to(REPO/'ci')]
        s.write_json(work/'evidence'/(stage+'-failure.json'),{'state':'failed_not_passed',
            'exception_type':type(error).__name__,'source_frames':frames[-8:],'resource':budget.record()})
    finally:
        try:budget.finish()
        except BaseException as error:failure=failure or error
        print(json.dumps({'stage':stage,'state':'failed'if failure else'passed',**budget.record()}),flush=True)
        for n,h in handlers.items():signal.signal(n,h)
    if failure:raise failure


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--cohort',choices=[COHORT],required=True)
    p.add_argument('--stage',choices=['readonly','acquire','offline','upload-admission'],default='readonly')
    p.add_argument('--work',type=Path)
    a=p.parse_args();registry,inputs,catalog=metadata()
    if a.stage=='readonly':return
    if a.work is None:p.error('explicit new work root required')
    os.umask(0o077)
    clean_process_environment()
    if a.stage=='upload-admission':prior.upload_admission(a.work)
    else:execute(a.stage,a.work,registry,inputs,catalog)


if __name__=='__main__':main()
