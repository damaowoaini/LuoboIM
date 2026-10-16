"""Two real Gradle controls; source preparation/checking never starts Gradle."""
import hashlib
import json
from pathlib import Path

import gradle_classpath_catalog as producer
import gradle_selector_fixture as original
import sdk_platform_producer as sdk
import support as s

CASES = ('sdk-three-platform-files', 'sdk-samepath-tamper')
REFUSAL = 'unknown or unapproved local artifact component refused: file-not-in-independent-producer'
FILE_CLASS = 'org.gradle.internal.component.local.model.OpaqueComponentArtifactIdentifier'


def prepare(work):
    work = Path(work)
    results = []
    for label in CASES:
        case = work / 'evidence/selector-controls' / label
        if case.exists() or case.is_symlink():
            raise ValueError('fresh SDK control only; no partial resume')
        s.mkdir(case / 'evidence')
        s.mkdir(case / 'empty')
        s.put(case / 'settings.gradle', b"rootProject.name = 'sdk-source-control'\ninclude ':plugin', ':empty'\n")
        fixed_sha = original.module(case / 'm2', 'fixture.synthetic', 'fixed', '1.0')
        s.put(case / 'plugin/build.gradle', b"plugins { id 'java-library' }\n"
            b"group='fixture.synthetic.plugin'\nversion='1.0-SNAPSHOT'\n"
            b"repositories { maven { url = uri('../m2') } }\n"
            b"dependencies { api 'fixture.synthetic:fixed:1.0' }\n")
        paths = ['android/' + row['jar']['path'] for row in sdk.contract()]
        script = "plugins { id 'java-library' }\nrepositories { maven { url = uri('m2') } }\n"
        script += 'def sdkFiles = ' + json.dumps(paths) + ".collect { new File(System.getenv('ANDROID_NATIVE_WORK'), it) }\n"
        script += "dependencies { api project(':plugin'); api files(sdkFiles) }\n"
        s.put(case / 'build.gradle', script.encode())
        s.put(case / 'gradle.properties', b'org.gradle.daemon=false\norg.gradle.parallel=false\n')
        row = {'label': label, 'cwd': str(case), 'variant_contract': True,
            'local_file_contract': True, 'SDK_platform_contract': True, 'kind': 'project',
            'expected_rejection': REFUSAL if label == CASES[1] else None,
            'expected_fixed_artifact_sha256': fixed_sha, 'original16_preserved': True}
        s.write_json(case / 'input.json', row)
        results.append(row)
    s.write_json(work / 'evidence/selector-controls/prepared-SDK-platforms.json', {
        'state': 'prepared_not_executed', 'cases': results, 'actual_Gradle_processes_required': 2,
        'fault_scope': 'only fresh owned SDK37 jar; original inode restored in cmd finally'})
    return results


def check(case, log, captured):
    work = Path(case['cwd']).parents[2]
    _, catalog_sha = producer.verified_catalog(work)
    sources = sdk.verified_sources(work)
    if case['label'] == CASES[1]:
        text = Path(log).read_text(errors='replace')
        records = [json.loads(line.split('LUOBO_COMPONENT_REFUSAL ', 1)[1])
                   for line in text.splitlines() if 'LUOBO_COMPONENT_REFUSAL ' in line]
        target = sources['platforms'][0]['jar']
        synthetic_sha = hashlib.sha256(original.jar_bytes()).hexdigest()
        if (case['expected_rejection'] not in text or captured or not any(
                x.get('component_class') == FILE_CLASS and x.get('reason') == 'file-not-in-independent-producer'
                and x.get('catalog_sha256') == catalog_sha
                and x.get('file') == {'path': target['path'], 'bytes': len(original.jar_bytes()), 'sha256': synthetic_sha}
                for x in records)):
            raise ValueError('SDK samepath replacement requires actual typed SHA refusal and no completed outputs')
        restored = sdk.read_json(work, Path(case['cwd']) / 'fault-restored.json')
        a, b = restored['before_stat'], restored['after_stat']
        if (restored['state'] != 'original_inode_and_bytes_restored'
                or (a['st_dev'], a['st_ino']) != (b['st_dev'], b['st_ino'])
                or restored['original'] != {k: target[k] for k in ('path', 'bytes', 'sha256')}):
            raise ValueError('samepath fault control must prove original inode/body restoration')
        return {'actual_Gradle_samepath_refusal': True, 'original_inode_and_bytes_restored': True}
    proof = original.check(case, log, captured)
    rows = producer.read_json(work, Path(case['cwd']) / 'evidence' / original.OUTPUTS[0])
    for source in sources['platforms']:
        jar = source['jar']
        if not any(x.get('selected', {}).get('kind') == 'presealed-SDK-platform-file'
                   and x['selected'].get('component_class') == FILE_CLASS
                   and x['selected'].get('source', {}).get('package') == source['package']
                   and x['selected']['source'].get('archive') == source['archive']
                   and x['selected']['source'].get('ZIP_member') == jar['ZIP_member']
                   and all(x.get(k) == jar[k] for k in ('path', 'bytes', 'sha256')) for x in rows):
            raise ValueError('all three actual SDK files need independent official ZIP source identity')
    if not any(x.get('selected', {}).get('kind') == 'module'
               and x['selected'].get('module') == 'fixed'
               and x.get('sha256') == case['expected_fixed_artifact_sha256'] for x in rows):
        raise ValueError('SDK/project/fixed-module control lost actual module SHA audit')
    return {**proof, 'all3_official_SDK_members_consumed': True,
            'same_graph_project_and_fixed_module_preserved': True}
