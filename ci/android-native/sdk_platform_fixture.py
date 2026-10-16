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
    return results + prepare_full(work)


def check(case, log, captured):
    if case.get('SDK_full_closure_contract'):
        return check_full(case, log, captured)
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


FULL_CASES = ('sdk-all-classpath-members', 'sdk-source-only-jar', 'sdk-system-samepath-tamper')
SOURCE_ONLY = 'unknown or unapproved local artifact component refused: source-only-sdk-member-not-classpath'


def prepare_full(work):
    work = Path(work)
    rows = sdk.contract()
    classpaths = ['android/' + item['path'] for row in rows for item in row['jar_inventory']
                  if item['role'] == 'classpath']
    source_only = 'android/platforms/android-37.0/android-stubs-src.jar'
    system = 'android/platforms/android-37.0/core-for-system-modules.jar'
    result = []
    for label in FULL_CASES:
        case = work / 'evidence/selector-controls' / label
        if case.exists() or case.is_symlink():
            raise ValueError('fresh full SDK closure control only')
        s.mkdir(case / 'evidence')
        s.mkdir(case / 'empty')
        s.put(case / 'settings.gradle', b"rootProject.name = 'sdk-finite-closure-control'\ninclude ':plugin', ':empty'\n")
        fixed_sha = original.module(case / 'm2', 'fixture.synthetic', 'fixed', '1.0')
        s.put(case / 'plugin/build.gradle', b"plugins { id 'java-library' }\n"
            b"group='fixture.synthetic.plugin'\nversion='1.0-SNAPSHOT'\n"
            b"repositories { maven { url = uri('../m2') } }\n"
            b"dependencies { api 'fixture.synthetic:fixed:1.0' }\n")
        paths = classpaths if label == FULL_CASES[0] else [source_only if label == FULL_CASES[1] else system]
        script = "plugins { id 'java-library' }\nrepositories { maven { url = uri('m2') } }\n"
        script += 'def finiteSDKFiles = ' + json.dumps(paths) + ".collect { new File(System.getenv('ANDROID_NATIVE_WORK'), it) }\n"
        script += "dependencies { api project(':plugin'); api files(finiteSDKFiles) }\n"
        s.put(case / 'build.gradle', script.encode())
        s.put(case / 'gradle.properties', b'org.gradle.daemon=false\norg.gradle.parallel=false\n')
        row = {'label': label, 'cwd': str(case), 'variant_contract': True,
            'local_file_contract': True, 'SDK_platform_contract': True,
            'SDK_full_closure_contract': True, 'kind': 'project',
            'expected_rejection': None if label == FULL_CASES[0] else SOURCE_ONLY if label == FULL_CASES[1] else REFUSAL,
            'expected_fixed_artifact_sha256': fixed_sha, 'original18_preserved': True}
        s.write_json(case / 'input.json', row)
        result.append(row)
    s.write_json(work / 'evidence/selector-controls/prepared-SDK-full-closure.json', {
        'state': 'prepared_not_executed', 'cases': result, 'new_real_Gradle_processes_required': 3,
        'old18_preserved': True, 'all26_classpath_vs3_source_only_separate': True})
    return result


def check_full(case, log, captured):
    work = Path(case['cwd']).parents[2]
    _, catalog_sha = producer.verified_catalog(work)
    complete = sdk.verified_sources(work)
    if case['label'] == FULL_CASES[0]:
        proof = original.check(case, log, captured)
        rows = producer.read_json(work, Path(case['cwd']) / 'evidence' / original.OUTPUTS[0])
        expected_paths = set()
        for source in complete['platforms']:
            for jar in source['classpath_JARs']:
                expected_paths.add(jar['path'])
                if not any(x.get('selected', {}).get('kind') == 'presealed-SDK-platform-file'
                           and x['selected'].get('source', {}).get('archive') == source['archive']
                           and x['selected']['source'].get('package') == source['package']
                           and x['selected']['source'].get('ZIP_member') == jar['ZIP_member']
                           and x['selected']['source'].get('role') == 'classpath'
                           and all(x.get(k) == jar[k] for k in ('path', 'bytes', 'sha256')) for x in rows):
                    raise ValueError('every finite SDK compile member needs actual selected source proof')
        actual = {x.get('path') for x in rows if x.get('selected', {}).get('kind') == 'presealed-SDK-platform-file'}
        if len(expected_paths) != 26 or actual != expected_paths:
            raise ValueError('actual SDK classpath must cover exact26, no source-only members')
        if not any(x.get('selected', {}).get('kind') == 'module'
                   and x['selected'].get('module') == 'fixed'
                   and x.get('sha256') == case['expected_fixed_artifact_sha256'] for x in rows):
            raise ValueError('finite SDK positive must retain actual external fixed module SHA')
        return {**proof, 'actual26_independent_classpath_members': True, 'source_only_excluded': True}
    text = Path(log).read_text(errors='replace')
    records = [json.loads(line.split('LUOBO_COMPONENT_REFUSAL ', 1)[1])
               for line in text.splitlines() if 'LUOBO_COMPONENT_REFUSAL ' in line]
    source_only = case['label'] == FULL_CASES[1]
    entries = complete['platforms'][0]['source_only_JARs' if source_only else 'classpath_JARs']
    target = next(x for x in entries if x['ZIP_member'].endswith('/android-stubs-src.jar' if source_only else '/core-for-system-modules.jar'))
    body = original.jar_bytes()
    expected = {k: target[k] for k in ('path', 'bytes', 'sha256')} if source_only else {
        'path': target['path'], 'bytes': len(body), 'sha256': hashlib.sha256(body).hexdigest()}
    reason = 'source-only-sdk-member-not-classpath' if source_only else 'file-not-in-independent-producer'
    if (captured or case['expected_rejection'] not in text or not any(
            x.get('component_class') == FILE_CLASS and x.get('reason') == reason
            and x.get('catalog_sha256') == catalog_sha and x.get('file') == expected for x in records)):
        raise ValueError('finite SDK negative lacks actual typed provenance and correct refusal')
    if not source_only:
        restored = sdk.read_json(work, Path(case['cwd']) / 'fault-restored.json')
        before, after = restored['before_stat'], restored['after_stat']
        if ((before['st_dev'], before['st_ino']) != (after['st_dev'], after['st_ino'])
                or restored['original'] != {k: target[k] for k in ('path', 'bytes', 'sha256')}
                or restored['state'] != 'original_inode_and_bytes_restored'):
            raise ValueError('system module tamper must restore original inode/body')
    return {'actual_source_only_rejection': source_only,
            'actual_system_module_replacement_rejected_and_original_restored': not source_only}
