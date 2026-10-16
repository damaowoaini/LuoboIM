"""Three real Gradle local-file controls; preparation/check only, no runner."""
import base64
import json
from pathlib import Path

import gradle_classpath_catalog as producer
import gradle_selector_fixture as original
import support as s

CASES = ('file-project-module', 'same-name-unlisted-file', 'file-project-transitive-snapshot')
UNLISTED = 'unknown or unapproved local artifact component refused: file-not-in-independent-producer'
FILE_CLASS = 'org.gradle.internal.component.local.model.OpaqueComponentArtifactIdentifier'


def prepare(work):
    work = Path(work)
    root = work / 'evidence/selector-controls'
    result = []
    for label in CASES:
        case = root / label
        if case.exists() or case.is_symlink():
            raise ValueError('fresh mixed file/project/module case required')
        s.mkdir(case / 'evidence')
        s.mkdir(case / 'empty')
        s.put(case / 'settings.gradle', b"rootProject.name = 'local-file-control'\ninclude ':plugin', ':empty'\n")
        fixed_sha = original.module(case / 'm2', 'fixture.synthetic', 'fixed', '1.0')
        original.module(case / 'm2', 'fixture.synthetic', 'child', '1.0-SNAPSHOT')
        original.module(case / 'm2', 'fixture.synthetic', 'parent', '1.0',
                        ('fixture.synthetic', 'child', '1.0-SNAPSHOT'))
        dependency = 'fixture.synthetic:parent:1.0' if label == CASES[2] else 'fixture.synthetic:fixed:1.0'
        plugin = "plugins { id 'java-library' }\ngroup = 'fixture.synthetic.plugin'\nversion = '1.0-SNAPSHOT'\n" + \
            "repositories { maven { url = uri('../m2') } }\ndependencies { api " + json.dumps(dependency) + " }\n"
        s.put(case / 'plugin/build.gradle', plugin.encode())
        script = """import groovy.json.JsonSlurper
plugins { id 'java-library' }
repositories { maven { url = uri('m2') } }
// The separately pinned resolver authenticates these exact bytes before this
// build script runs. No file first seen in this app/fixture model is enrolled.
def catalog = new JsonSlurper().parse(file(findProperty('luoboClasspathCatalog')))
def approved = new File(System.getenv('ANDROID_NATIVE_WORK'),
    catalog.reports.selector.notations.LOCAL_GROOVY.files[0].path)
"""
        if label == CASES[1]:
            script += "def unlisted = file('unlisted/' + approved.name)\nunlisted.parentFile.mkdirs()\n" + \
                "unlisted.bytes = " + json.dumps(base64.b64encode(original.jar_bytes()).decode()) + ".decodeBase64()\n"
        script += "dependencies {\n    api project(':plugin')\n    api files(" + \
            ('unlisted' if label == CASES[1] else 'approved') + ")\n    api gradleApi()\n}\n"
        s.put(case / 'build.gradle', script.encode())
        s.put(case / 'gradle.properties', b'org.gradle.daemon=false\norg.gradle.parallel=false\n')
        row = {'label': label, 'cwd': str(case), 'variant_contract': True,
            'local_file_contract': True, 'kind': 'project', 'group': 'fixture.synthetic.plugin',
            'module': 'plugin', 'version': '1.0-SNAPSHOT', 'expected_rejection': None,
            'expected_fixed_artifact_sha256': fixed_sha}
        if label == CASES[1]:
            row['expected_rejection'] = UNLISTED
        elif label == CASES[2]:
            row.update(kind='transitive', group='fixture.synthetic', module='child',
                       expected_rejection='dynamic dependency version refused')
        s.write_json(case / 'input.json', row)
        result.append(row)
    s.write_json(root / 'prepared-local-files.json', {'state': 'prepared_not_executed',
        'cases': result, 'original12_unchanged': True, 'Gradle_processes': 3,
        'producer_is_separate_first_builtin_models': True})
    return result


def check(case, log, captured):
    work = Path(case['cwd']).parents[2]
    catalog_path, catalog_sha = producer.verified_catalog(work)
    catalog = producer.read_json(work, catalog_path)
    expected = catalog['reports']['selector']['notations']['LOCAL_GROOVY']['files'][0]
    if case['label'] == CASES[1]:
        text = Path(log).read_text(errors='replace')
        details = [json.loads(line.split('LUOBO_COMPONENT_REFUSAL ', 1)[1])
                   for line in text.splitlines() if 'LUOBO_COMPONENT_REFUSAL ' in line]
        target = Path(case['cwd']) / 'unlisted' / Path(expected['path']).name
        row = producer.file_row(work, target)
        if (case['expected_rejection'] not in text or captured or not any(
                x.get('component_class') == FILE_CLASS and x.get('reason') == 'file-not-in-independent-producer'
                and x.get('catalog_sha256') == catalog_sha and x.get('file') == row for x in details)):
            raise ValueError('unlisted same-name file needs exact real typed rejection/provenance')
        if row['sha256'] != case['expected_fixed_artifact_sha256']:
            raise ValueError('unlisted fixture body changed')
        return {'same_name_is_not_authorization': True, 'actual_Gradle_refusal_and_file_provenance': row}
    proof = original.check(case, log, captured)
    if case['label'] == CASES[0]:
        rows = producer.read_json(work, Path(case['cwd']) / 'evidence' / original.OUTPUTS[0])
        if not any(x.get('selected', {}).get('kind') == 'presealed-file'
                   and x['selected'].get('component_class') == FILE_CLASS
                   and x.get('path') == expected['path'] and x.get('sha256') == expected['sha256']
                   and x.get('bytes') == expected['bytes'] for x in rows):
            raise ValueError('real files() component did not consume independently presealed file')
        if not any(x.get('selected', {}).get('kind') == 'presealed-gradle-classpath'
                   and x['selected'].get('notation') == 'GRADLE_API'
                   and x['selected'].get('cache') == 'selector' for x in rows):
            raise ValueError('real builtin notation did not consume producer catalog')
        if not any(x.get('selected', {}).get('kind') == 'module'
                   and x['selected'].get('module') == 'fixed' and x.get('sha256') == case['expected_fixed_artifact_sha256']
                   for x in rows):
            raise ValueError('file/project control lost external fixed artifact audit')
        proof['real_files_builtin_project_and_module_all_verified'] = True
    return proof
