"""Preserve three local-file controls; add real inherited-catalog control."""
import base64
import json
from pathlib import Path

import gradle_classpath_catalog as producer
import gradle_selector_fixture as original
import support as s
import sdk_platform_fixture as sdk_fixture

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
def catalog = new JsonSlurper().parse(file(gradle.startParameter.getSystemPropertiesArgs()['luoboClasspathCatalog']))
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
    return result + prepare_included(work) + sdk_fixture.prepare(work)


def check(case, log, captured):
    if case.get('SDK_platform_contract'):
        return sdk_fixture.check(case, log, captured)
    if case.get('included_catalog_contract'):
        return check_included(case, log, captured)
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


def prepare_included(work):
    """Builtin Java projects only; this function never starts Gradle."""
    case = Path(work) / 'evidence/selector-controls/included-build-catalog'
    if case.exists() or case.is_symlink():
        raise ValueError('fresh included-build catalog control required')
    s.mkdir(case / 'evidence')
    s.put(case / 'settings.gradle', b"rootProject.name = 'catalog-parent'\nincludeBuild('included')\n")
    s.put(case / 'build.gradle', b"plugins { id 'java-library' }\n"
        b"dependencies { api 'fixture.composite:included:1.0' }\n")
    s.put(case / 'included/settings.gradle', b"rootProject.name = 'included'\n")
    s.put(case / 'included/build.gradle', b"plugins { id 'java-library' }\n"
        b"group = 'fixture.composite'\nversion = '1.0'\n")
    s.put(case / 'gradle.properties', b'org.gradle.daemon=false\norg.gradle.parallel=false\n')
    row = {'label': 'included-build-catalog', 'cwd': str(case), 'variant_contract': True,
        'local_file_contract': True, 'included_catalog_contract': True,
        'expected_rejection': None, 'actual_expected_init_directories': [
            'evidence/selector-controls/included-build-catalog',
            'evidence/selector-controls/included-build-catalog/included']}
    s.write_json(case / 'input.json', row)
    s.write_json(case.parent / 'prepared-included-catalog.json', {
        'state': 'prepared_not_executed', 'case': row, 'original15_preserved': True,
        'Gradle_processes': 1, 'builtin_java_only_no_external_repository': True})
    return [row]


def check_included(case, log, captured):
    """Requires actual nested model, literal digest and complete real outputs."""
    work = Path(case['cwd']).parents[2]
    _, catalog_sha = producer.verified_catalog(work)
    if set(captured) != set(original.OUTPUTS):
        raise ValueError('included control requires complete actual artifact/graph/project outputs')
    text = Path(log).read_text(errors='replace')
    bindings = [json.loads(line.split('LUOBO_CATALOG_BINDING ', 1)[1])
                for line in text.splitlines() if 'LUOBO_CATALOG_BINDING ' in line]
    for directory in case['actual_expected_init_directories']:
        if not any(x.get('state') == 'catalog_bytes_bound'
                   and x.get('source') == 'StartParameter.systemPropertiesArgs'
                   and x.get('current_directory_owned_relative') == directory
                   and x.get('catalog_sha256') == catalog_sha
                   and x.get('checks') and all(v is True for v in x['checks'].values())
                   for x in bindings):
            raise ValueError('main and actual included initialization must bind same full producer SHA')
    base = Path(case['cwd']) / 'evidence'
    artifacts, selections, projects = [producer.read_json(work, base / n) for n in original.OUTPUTS]
    if not projects or not all(x.get('canonicalOwned') is True for x in projects):
        raise ValueError('included control lacks canonical owned source check')
    def from_included(selected):
        return (selected.get('kind') == 'project' and selected.get('projectPath') == ':'
                and selected.get('buildTreePath') == ':included')
    if not any(x.get('requested', {}).get('kind') == 'module'
               and x['requested'].get('group') == 'fixture.composite'
               and x['requested'].get('module') == 'included'
               and x['requested'].get('version') == '1.0'
               and from_included(x.get('selected', {})) for x in selections):
        raise ValueError('actual included project was not selected for fixed module dependency')
    if not any(from_included(x.get('selected', {}))
               and x.get('deferred_to_actual_APK_compile') is True for x in artifacts):
        raise ValueError('included project artifact deferral must retain real graph identity')
    return {'actual_main_and_included_init_directories': case['actual_expected_init_directories'],
        'same_full_independent_catalog_sha256': catalog_sha,
        'actual_included_project_selected': True, 'real_Gradle_success_required': True}
