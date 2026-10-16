"""Prepared actual-Gradle controls of the SAME library, using tiny local Maven.

No production alternate catalog or source-enrolment switch is introduced.
prepare/check are pure file helpers; the controller dispatches fixed offline Gradle.
"""
import copy
import io
import json
from pathlib import Path
import zipfile
import rich_consumer as rich
import support as s


def constraint(version, strict=''):
    return dict(required=version,strict=strict,preferred='',rejected=[],branch=None)


def ident(group,name,version):
    return dict(group=group,module=name,version=version)


def request(group,name,version,strict=''):
    return dict(group=group,module=name,version=version,constraint=constraint(version,strict),
                attributes={},requestedCapabilities=[],capabilitySelectors=[])


def jar():
    data=io.BytesIO()
    with zipfile.ZipFile(data,'w') as z:
        z.writestr(zipfile.ZipInfo('synthetic-control.txt',(2020,1,1,0,0,0)),b'not production Kotlin or BC\n')
    return data.getvalue()


def model():
    k,b='org.jetbrains.kotlin','org.bouncycastle'
    kr='[2.4.0-Beta2, 2.5.0)';br='[1.80,1.81)'
    roots=[[request(k,'kotlin-build-tools-compat','2.4.0'),request(k,'kotlin-build-tools-impl',kr,kr)],
           [request(b,'bcpg-jdk18on','1.80'),request(b,'bcpkix-jdk18on','1.80')]]
    edges=[[dict(from_=None,requested=roots[0][0],selected=ident(k,'kotlin-build-tools-compat','2.4.0')),
            dict(from_=None,requested=roots[0][1],selected=ident(k,'kotlin-build-tools-impl','2.4.21'))],
           [dict(from_=None,requested=roots[1][0],selected=ident(b,'bcpg-jdk18on','1.80')),
            dict(from_=None,requested=roots[1][1],selected=ident(b,'bcpkix-jdk18on','1.80')),
            dict(from_=ident(b,'bcpg-jdk18on','1.80'),requested=request(b,'bcprov-jdk18on',br),selected=ident(b,'bcprov-jdk18on','1.80.2')),
            dict(from_=ident(b,'bcpkix-jdk18on','1.80'),requested=request(b,'bcutil-jdk18on',br),selected=ident(b,'bcutil-jdk18on','1.80.2')),
            dict(from_=ident(b,'bcutil-jdk18on','1.80.2'),requested=request(b,'bcprov-jdk18on','1.80.2'),selected=ident(b,'bcprov-jdk18on','1.80.2'))]]
    names=['kotlinAbiValidationCompatClasspath','kotlinBouncyCastleConfiguration']
    configs=[]
    for name,declared,rows in zip(names,roots,edges):
        fixed=[]
        for row in rows:
            row['from']=row.pop('from_') or {'kind':'producer-root'}
            if row['selected'] not in fixed: fixed.append(row['selected'])
        artifacts=[]
        for i in fixed:
            artifacts.append(dict(id=i,filename=i['module']+'-'+i['version']+'.jar',bytes=len(jar()),sha256=rich.sha(jar()),
                variant=dict(owner=i,attributes={'artifactType':dict(type='java.lang.String',value='jar'),
                   'org.gradle.status':dict(type='java.lang.String',value='release')},
                   capabilities=[dict(group=i['group'],name=i['module'],version=i['version'])],external=None)))
        configs.append(dict(name=name,attributes={},declared=[{k:v for k,v in r.items() if k not in ('version','capabilitySelectors')} for r in declared],
                            edges=rows,artifacts=artifacts))
    return configs


def prepare(work,here):
    work,here=Path(work),Path(here)
    catalog,_,lib=rich.approved(here)
    cases=[]
    modes={'positive':None,'fourth-selector':'rich fourth or changed complete selector refused',
           'changed-strict':'rich declared roots/generator differ','changed-parent':'rich parent/constraint/selected complete graph differs',
           'changed-source':'rich owner project source/generator scope differs','changed-body':'rich artifact18 selected/body differs'}
    for mode,error in modes.items():
        root=work/'rich-controls'/mode
        s.mkdir(root)
        settings=b"rootProject.name='controlled-rich'; include ':app'\n"
        s.put(root/'settings.gradle',settings);s.put(root/'build.gradle',b'// builtin only\n')
        configs=model()
        # Each test source has exactly the same three finite constraints, roots,
        # and parent identities before mutation; no binaries are represented as real.
        app="repositories { maven { url = rootProject.file('m2') } }\n"
        app+="['kotlinAbiValidationCompatClasspath','kotlinBouncyCastleConfiguration'].each { n -> configurations.create(n) { canBeResolved=true; canBeConsumed=false } }\n"
        for c in configs:
            for d in c['declared']:
                v=d['constraint'];call='strictly' if v['strict'] else 'require'
                app+="dependencies.add('%s', dependencies.create('%s:%s') { version { %s '%s' } })\n"%(c['name'],d['group'],d['module'],call,v['strict'] or v['required'])
        if mode=='fourth-selector':
            # The known config and coordinate cannot exempt a new full tuple.
            app=app.replace("strictly '[2.4.0-Beta2, 2.5.0)'","strictly '[2.3, 2.5)' ")
        if mode=='changed-strict': app=app.replace("strictly '[2.4.0-Beta2, 2.5.0)'","require '[2.4.0-Beta2, 2.5.0)'")
        s.put(root/'app/build.gradle',app.encode())
        spec=copy.deepcopy(catalog)
        spec['configurations']=configs
        spec['scope']=dict(project=':app',root_relative=str(root.relative_to(work)),
             file_prefix=str(root.relative_to(work))+'/',sources={p:s.sha(root/p) for p in ['settings.gradle','build.gradle','app/build.gradle']})
        if mode=='fourth-selector':
            # Let declarations match the intentionally new root, then test the
            # independent finite requested-selector guard (not a setup mismatch).
            spec['configurations'][0]['declared'][1]['constraint']=constraint('[2.3, 2.5)','[2.3, 2.5)')
        spec['library_sha256']=rich.SCRIPT_SHA
        s.write_json(root/'control-catalog.json',spec)
        s.put(root/'rich-consumer.gradle',lib)
        s.put(root/'control-init.gradle',rich.read(here/'rich-fixture.gradle'))
        modules={}
        for c in configs:
            for a in c['artifacts']: modules[tuple(a['id'].values())]=a['id']
        for g,n,v in modules:
            deps=[]
            for c in configs:
                for e in c['edges']:
                    if e['from']==ident(g,n,v):
                        q=e['requested'];deps.append(q)
            if mode=='changed-parent' and n=='bcpg-jdk18on': deps=[]
            if mode=='changed-parent' and n=='bcpkix-jdk18on': deps.append(request('org.bouncycastle','bcprov-jdk18on','[1.80,1.81)'))
            body='<project><modelVersion>4.0.0</modelVersion><groupId>'+g+'</groupId><artifactId>'+n+'</artifactId><version>'+v+'</version><dependencies>'
            for d in deps: body+='<dependency><groupId>'+d['group']+'</groupId><artifactId>'+d['module']+'</artifactId><version>'+d['version']+'</version></dependency>'
            body+='</dependencies></project>\n'
            base=root/'m2'/g.replace('.','/')/n/v
            s.put(base/(n+'-'+v+'.pom'),body.encode());s.put(base/(n+'-'+v+'.jar'),jar())
        groups={}
        for g,n,v in modules: groups.setdefault((g,n),[]).append(v)
        for (g,n),versions in groups.items():
            body='<metadata><groupId>'+g+'</groupId><artifactId>'+n+'</artifactId><versioning><versions>'+''.join('<version>'+v+'</version>' for v in sorted(versions))+'</versions></versioning></metadata>\n'
            s.put(root/'m2'/g.replace('.','/')/n/'maven-metadata.xml',body.encode())
        if mode=='changed-source':
            with (root/'app/build.gradle').open('ab') as f:f.write(b'// changed after independent source pin\n')
        if mode=='changed-body':
            path=root/'m2/org/jetbrains/kotlin/kotlin-build-tools-impl/2.4.21/kotlin-build-tools-impl-2.4.21.jar'
            path.write_bytes(jar()+b'changed')
        cases.append(dict(label=mode,cwd=str(root),expected_rejection=error))
    return cases


def run(job,here):
    # Separate local-Maven home cannot contaminate real Maven origin receipts.
    import run as parent
    cases=prepare(job.w,here);results=[]
    for case in cases:
        root=Path(case['cwd']);env={**parent.environment(job.w,True),'GRADLE_USER_HOME':str(job.w/'rich-control-home')}
        args=[job.w/'gradle/bin/gradle','--no-daemon','--max-workers=1','--no-watch-fs','--console=plain','--offline',
            '--project-cache-dir',job.w/'rich-control-cache'/case['label'],'-I',root/'control-init.gradle','checkRichConsumer']
        log=job.cmd(args,root,env,'rich-control-'+case['label'],120,case['expected_rejection'])
        if case['expected_rejection']:
            if case['expected_rejection'] not in log.read_text(): raise ValueError('specific rich control rejection absent')
        else:
            reports=json.loads(rich.read(root/'actual-controls.json'))
            if len(reports)!=2: raise ValueError('both actual control configurations required')
        results.append(dict(case=case['label'],state='passed',actual_Gradle=True,synthetic_Maven_not_production_artifacts=True))
    s.write_json(job.w/'evidence/rich-controls-complete.json',dict(cases=results,Gradle_offline=True))
