"""KGP publishing tooling is an actual source owner, not App compilation.

Preserves original BC4 graph/body/variant/origin and every ABI14 producer. A
runtime CodeSource must match one already verified actual Maven artifact; no
cache basename/key hash, version-from-project-name or arbitrary download grant.
"""
import copy
import hashlib
import json
from pathlib import Path

ABI='kotlinAbiValidationCompatClasspath'
BC='kotlinBouncyCastleConfiguration'
OWNER={'build_root_relative':'flutter/packages/flutter_tools/gradle','project':':',
       'project_relative':'flutter/packages/flutter_tools/gradle'}
SOURCES={'flutter/packages/flutter_tools/gradle/build.gradle.kts': {'bytes': 2867, 'sha256': '8bce67c561f7a91bb743b443242b8d804ddc4190dc5524278bdecf8797515008'}, 'flutter/packages/flutter_tools/gradle/settings.gradle.kts': {'bytes': 165, 'sha256': '56fe45270044d31615e1d9b8aa1410511a05b83ef431c24613b7907f225f9779'}}
TASK_CLASSES={'generatePgpKeys':'org.jetbrains.kotlin.gradle.tasks.publishing.GeneratePgpKeys',
              'checkSigningConfiguration':'org.jetbrains.kotlin.gradle.tasks.publishing.CheckSigningTask'}

def canonical(v):return json.dumps(v,sort_keys=True,separators=(',',':'))
def digest(v):return hashlib.sha256(canonical(v).encode()).hexdigest()

def embedded(work,here,read):
    for name,row in SOURCES.items():
        body=read(Path(work)/name)
        if len(body)!=row['bytes'] or hashlib.sha256(body).hexdigest()!=row['sha256']:
            raise ValueError('fixed Flutter declaring-build source differs')
    script=read(Path(here)/'kgp-owner-witness.gradle')
    import base64
    return {'schema':1,'owner':copy.deepcopy(OWNER),'source_files':copy.deepcopy(SOURCES),
            'script_sha256':hashlib.sha256(script).hexdigest(),
            'script_base64':base64.b64encode(script).decode('ascii')}

def validate_owner_witness(value,catalog,origins):
    if (set(value)!={'schema','owner','configuration','registered_by_identity','source_files',
                    'implementation','consumers','report'} or value['schema']!=1
            or value['owner']!=OWNER or value['configuration']!=BC
            or value['registered_by_identity'] is not True or value['source_files']!=SOURCES):
        raise ValueError('exact source-owned KGP publishing witness required')
    implementation=value['implementation']
    if (set(implementation)!={'class_name','classloader','code_source'}
            or implementation['class_name']!='org.jetbrains.kotlin.gradle.plugin.KotlinBasePluginWrapper'
            or implementation['classloader']!='org.gradle.internal.classloader.VisitableURLClassLoader$InstrumentingVisitableURLClassLoader'):
        raise ValueError('fixed Gradle original-CodeSource instrumentation path required')
    code=implementation['code_source']
    if set(code)!={'path','bytes','sha256'} or not isinstance(code['path'],str) or not code['path'].startswith('gradle-cache/'):
        raise ValueError('owned actual KGP code source required')
    # Match body/size against actual strong XML+Downloading provenance. Version
    # and coordinate come from that typed audit, never from a cache key or name.
    matches=[r for r in origins['artifacts'] if r['coordinate'].startswith('org.jetbrains.kotlin:kotlin-gradle-plugin:')
             and r['filename'].endswith('.jar') and r['bytes']==code['bytes'] and r['sha256']==code['sha256']]
    if origins.get('all_actual_artifact_sources_accounted') is not True or len(matches)!=1:
        raise ValueError('actual KGP implementation lacks unique Maven body provenance')
    spec=next(c for c in catalog['configurations'] if c['name']==BC)
    report=value['report']; fields=('id','filename','bytes','sha256','variant')
    projection=lambda rows:sorted(canonical({k:r[k] for k in fields}) for r in rows)
    expected_sources={name:pin['sha256'] for name,pin in SOURCES.items()}
    if (report['catalog_sha256']!='00d559f734c28560b9f18a27ef2fedfd5488c7eb9fa0a6d8d18930d0927d2cc7'
            or report['project']!=':' or report['configuration']!=BC
            or report['scope_sources']!=expected_sources or report['no_force_or_substitution'] is not True
            or sorted(map(canonical,report['edges']))!=sorted(map(canonical,spec['edges']))
            or projection(report['artifacts'])!=projection(spec['artifacts'])):
        raise ValueError('original BC4 component/edge/body/variant gate differs')
    consumers=value['consumers']
    if len(consumers)!=2 or {r['task'] for r in consumers}!=set(TASK_CLASSES):
        raise ValueError('actual publishing task consumers incomplete')
    for row in consumers:
        if (set(row)!={'task','implementation_class','same_code_source','configuration_bound_by_identity'}
                or row['implementation_class']!=TASK_CLASSES[row['task']]
                or row['same_code_source'] is not True or row['configuration_bound_by_identity'] is not True):
            raise ValueError('actual publishing consumer identity differs')
    return {'kind':'actual-kgp-publishing-tooling-owner-v1','owner':copy.deepcopy(OWNER),
            'configuration':BC,'implementation':copy.deepcopy(matches[0]),'code_source':copy.deepcopy(code),
            'witness_sha256':digest(value),'compilation_consumed':False,'publishing_tasks_executed':False,
            'original_BC4_template_verified':True,'APK_accepted':False}

def require_report(work,here,catalog,phase,read):
    if phase not in {'metadata','apk'}:raise ValueError('finite tooling phase')
    work=Path(work);path=work/'evidence/kgp-tooling-owner'/phase/(BC+'.json')
    # Metadata is mandatory. APK can legitimately not resolve publishing tools;
    # it reuses source-bound metadata and never claims them App-consumed.
    if phase=='apk' and not path.exists():
        return {'actual_APK_publishing_configuration_consumed':False}
    value=json.loads(read(path));origins=json.loads(read(work/'evidence/dependency-origin-audit.json',64<<20))
    return validate_owner_witness(value,catalog,origins)
