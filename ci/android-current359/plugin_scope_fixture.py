"""Prepare three offline builtin/file-Maven controls; no tool dispatch here.

Both factory scripts and the durable writer are the exact production bodies.
Tiny synthetic JARs/metadata are never production18 or package source proof.
"""
import copy
import hashlib
import json
from pathlib import Path

import rich_current as rich
import rich_fixture as old_fixture
import support as s

RESOLVER_SHA = 'f39e063249a2175412b2ca412900a59a67cef30410ecf0e48c5d66c3372dd6c5'
START = 'def richPersist = '
END = 'def richWrite = '
MARKER = 'LUOBO_PLUGIN_WRITER_RETURNED '


def writer_bytes(here):
    body = rich.read(Path(here) / 'resolve.gradle')
    text = body.decode()
    if rich.sha(body) != RESOLVER_SHA or text.count(START) != 1 or text.count(END) != 1:
        raise ValueError('exact production resolver and unique durable writer required')
    a, z = text.index(START), text.index(END)
    if a >= z:
        raise ValueError('ordered production writer markers required')
    return text[a:z].encode()


INIT = r'''
import groovy.json.JsonOutput
import groovy.json.JsonSlurper
import java.nio.file.Files
import java.nio.file.LinkOption
import java.security.MessageDigest
gradle.projectsEvaluated {
    def root=gradle.rootProject
    def input=new JsonSlurper().parse(new File(root.projectDir,'control-input.json'))
    def owned=new File(input.owned).canonicalFile
    def hash={ b -> MessageDigest.getInstance('SHA-256').digest(b).encodeHex().toString() }
    def fileDigest={ f -> hash(f.bytes) }
    def localRef={ f -> [path:owned.toPath().relativize(f.canonicalFile.toPath()).toString(),bytes:f.length(),sha256:fileDigest(f)] }
    def originalBytes=new File(root.projectDir,'App-library.gradle').bytes
    def pluginBytes=new File(root.projectDir,'plugin-library.gradle').bytes
    def writerBytes=new File(root.projectDir,'writer.gradle').bytes
    assert hash(originalBytes)==input.App_library_sha256
    assert hash(pluginBytes)==input.plugin_library_sha256
    assert hash(writerBytes)==input.writer_sha256
    def original=new GroovyShell(getClass().classLoader).evaluate(new String(originalBytes,'UTF-8'))
    def plugin=new GroovyShell(getClass().classLoader).evaluate(new String(pluginBytes,'UTF-8'))
    def richSource=input.App_source
    def pluginSlots=[(input.entry.slot):input.entry]
    def binding=new Binding([owned:owned,richSource:richSource,richPhase:'metadata',pluginSlots:pluginSlots,
        ownedPrincipal:Files.getOwner(owned.toPath(),LinkOption.NOFOLLOW_LINKS)])
    def writer=new GroovyShell(getClass().classLoader,binding).evaluate(
        """import groovy.json.JsonOutput
         import org.gradle.api.GradleException
         import java.nio.file.Files
         import java.nio.file.LinkOption
         """+new String(writerBytes,'UTF-8')+'\nreturn richPersist\n')
    root.tasks.register('checkRegisteredPluginScope') {
        doLast {
            [':app',':plugin'].each { projectName ->
                def p=root.project(projectName)
                def source=projectName==':app' ? input.App_source : input.plugin_source
                def gate=(projectName==':app' ? original : plugin)(owned,source,localRef,fileDigest,input.template_catalog_sha256)
                source.configurations.each { item ->
                    def c=p.configurations.getByName(item.name)
                    if (gate.scoped(p,c)) c.incoming.beforeResolve { gate.declarations(p,c) }
                    c.resolutionStrategy.dependencySubstitution.all { d ->
                        if (d.requested instanceof org.gradle.api.artifacts.component.ModuleComponentSelector &&
                                gate.isRich(d.requested.versionConstraint)) gate.requested(p,c,d.requested)
                    }
                    c.files // Real Gradle requests/selection, not fabricated graph rows.
                    def proof=gate.audit(p,c)
                    def slot=projectName==':app' ? null : input.entry.slot
                    if (slot!=null) proof += [plugin_seal_sha256:input.seal_sha256,plugin_origin:input.entry.origin]
                    writer(c,proof,slot==null ? 'rich-consumed' : 'plugin-rich-consumed',slot)
                    logger.lifecycle('LUOBO_PLUGIN_WRITER_RETURNED '+JsonOutput.toJson(
                        [writer_sha256:input.writer_sha256,project:projectName,configuration:c.name]))
                }
            }
        }
    }
}
'''


def prepare(work, here):
    work, here = Path(work), Path(here)
    catalog, _, old_lib = rich.approved(here)
    plugin = rich.read(here / 'plugin-rich-consumer.gradle'); writer = writer_bytes(here)
    cases = []
    for mode, rejection in [('two-projects-positive', None),
                             ('changed-plugin-source', 'plugin rich independently sealed source differs'),
                             ('unregistered-project', 'dynamic dependency version refused')]:
        owner = work / 'plugin-scope-controls' / mode
        root = owner / 'project'
        s.put(root / 'settings.gradle', b"rootProject.name='controlled-plugin-scope'; include ':app',':plugin'\n")
        s.put(root / 'build.gradle', b'// builtin-only root\n')
        configs = old_fixture.model()
        project = "repositories { maven { url=rootProject.file('m2') } }\n"
        for c in configs:
            project += "configurations.create('%s') { canBeResolved=true; canBeConsumed=false }\n" % c['name']
            for d in c['declared']:
                v = d['constraint']; version = v['strict'] or v['required']
                project += "dependencies.add('%s', dependencies.create('%s:%s') { version { %s '%s' } })\n" % (
                    c['name'], d['group'], d['module'], 'strictly' if v['strict'] else 'require', version)
        for n in ('app', 'plugin'): s.put(root / n / 'build.gradle', project.encode())
        s.put(root / 'plugin/pubspec.yaml', b'name: synthetic_control_only\n')
        source = copy.deepcopy(catalog); source['configurations'] = configs
        source['scope'] = dict(project=':app', root_relative=str(root.relative_to(owner)),
            file_prefix=str(root.relative_to(owner))+'/', sources={n: s.sha(root/n) for n in ['settings.gradle','build.gradle','app/build.gradle']})
        scoped = copy.deepcopy(source)
        critical = {str((root/n).relative_to(owner)):s.sha(root/n) for n in ['settings.gradle','build.gradle','plugin/build.gradle','plugin/pubspec.yaml']}
        scoped['scope'] = dict(project=':unregistered' if mode=='unregistered-project' else ':plugin',
            root_relative=str(root.relative_to(owner)),project_relative=str((root/'plugin').relative_to(owner)),sources=critical)
        entry = dict(project=':plugin',slot=hashlib.sha256(b':plugin').hexdigest(),gate_sources=critical,origin={'kind':'synthetic-file-Maven-control-only'})
        s.put(root / 'App-library.gradle', old_lib);s.put(root / 'plugin-library.gradle', plugin);s.put(root / 'writer.gradle', writer)
        s.write_json(root / 'control-input.json', dict(owned=str(owner),App_source=source,plugin_source=scoped,entry=entry,
            App_library_sha256=rich.sha(old_lib),plugin_library_sha256=rich.sha(plugin),writer_sha256=rich.sha(writer),
            template_catalog_sha256=rich.CATALOG_SHA,seal_sha256='a'*64))
        s.put(root / 'control-init.gradle', INIT.encode())
        modules = {tuple(a['id'].values()):a['id'] for c in configs for a in c['artifacts']}
        for (g,n,v), ident in modules.items():
            dependencies=[e['requested'] for c in configs for e in c['edges'] if e['from']==ident]
            pom='<project><modelVersion>4.0.0</modelVersion><groupId>'+g+'</groupId><artifactId>'+n+'</artifactId><version>'+v+'</version><dependencies>'
            for d in dependencies: pom+='<dependency><groupId>'+d['group']+'</groupId><artifactId>'+d['module']+'</artifactId><version>'+d['version']+'</version></dependency>'
            pom+='</dependencies></project>\n'; base=root/'m2'/g.replace('.','/')/n/v
            s.put(base/(n+'-'+v+'.pom'),pom.encode());s.put(base/(n+'-'+v+'.jar'),old_fixture.jar())
        for g,n in sorted({(g,n) for g,n,v in modules}):
            versions=sorted(v for a,b,v in modules if a==g and b==n)
            body='<metadata><groupId>'+g+'</groupId><artifactId>'+n+'</artifactId><versioning><versions>'+''.join('<version>'+v+'</version>'for v in versions)+'</versions></versioning></metadata>\n'
            s.put(root/'m2'/g.replace('.','/')/n/'maven-metadata.xml',body.encode())
        if mode=='changed-plugin-source': (root/'plugin/pubspec.yaml').write_bytes(b'name: changed_after_seal\n')
        cases.append({'case':mode,'cwd':str(root),'expected_rejection':rejection,
            'command_tail':['--offline','--gradle-user-home',str(owner/'gradle-home'),'--project-cache-dir',str(owner/'project-cache'),'-I',str(root/'control-init.gradle'),'checkRegisteredPluginScope'],
            'synthetic_only':True,'production18_or_App_not_claimed':True})
    return cases


def check(case, log, work, exit_code):
    if (exit_code==0) != (case['expected_rejection'] is None):
        raise ValueError('actual Gradle exit must match exact control expectation')
    text = Path(log).read_text(); root=Path(case['cwd']); data=json.loads(rich.read(root/'control-input.json'))
    markers=[json.loads(l.split(MARKER,1)[1]) for l in text.splitlines() if MARKER in l]
    wanted=[dict(writer_sha256=data['writer_sha256'],project=p,configuration=c['name'])
            for p in ([':app',':plugin'] if case['expected_rejection'] is None else [':app']) for c in data['App_source']['configurations']]
    if sorted(map(rich.canonical,markers))!=sorted(map(rich.canonical,wanted)):
        raise ValueError('exact post-return durable writer markers required')
    if case['expected_rejection'] and case['expected_rejection'] not in text:
        raise ValueError('specific production source/scope rejection missing')
    for marker in wanted:
        prefix=Path(data['owned'])/'evidence'
        path=(prefix/'rich-consumed/metadata' if marker['project']==':app' else prefix/'plugin-rich-consumed/metadata'/data['entry']['slot'])/(marker['configuration']+'.json')
        actual=json.loads(rich.read(path))
        spec=next(c for c in data['App_source']['configurations'] if c['name']==marker['configuration'])
        projected=[{k:v for k,v in a.items()if k!='origin'}for a in spec['artifacts']]
        if (actual['project']!=marker['project'] or sorted(map(rich.canonical,actual['edges']))!=sorted(map(rich.canonical,spec['edges']))
                or sorted(map(rich.canonical,actual['artifacts']))!=sorted(map(rich.canonical,projected))):
            raise ValueError('actual tiny Gradle graph/report differs')
    if case['expected_rejection'] and list((Path(data['owned'])/'evidence/plugin-rich-consumed').rglob('*.json')):
        raise ValueError('refused plugin must produce no consumed reports')
    return {'postreturn_markers':len(markers),'actual_library_and_production_writer':True,'real_Gradle_required':True,
            'synthetic_Maven_not_production18':True}
