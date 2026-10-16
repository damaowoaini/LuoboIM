"""Seal builtin producers before the job's first Gradle model; never run on import."""
import hashlib
import json
import os
from pathlib import Path
import stat

import run as parent
import support as s

GRADLE_ZIP = '17f277867f6914d61b1aa02efab1ba7bb439ad652ca485cd8ca6842fccec6e43'
JDK_ARCHIVE = '3808d1d15e3ec6bd5b84057fb5d84c33d8a1536a258146bcea2e603fc726e08e'
NOTATIONS = ('GRADLE_API', 'LOCAL_GROOVY', 'GRADLE_TEST_KIT')
CLASS = 'org.gradle.internal.component.local.model.OpaqueComponentIdentifier'
ROOT = 'evidence/classpath-preseal'


def file_row(work, path):
    work, path = Path(work), Path(path)
    if path.resolve(strict=True) != path or not path.is_relative_to(work):
        raise ValueError('canonical owned classpath path required')
    with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW), 'rb') as stream:
        before = os.fstat(stream.fileno())
        if (not stat.S_ISREG(before.st_mode) or before.st_uid != os.getuid()
                or before.st_nlink != 1 or not 0 < before.st_size <= 512 * s.GIB // 1024):
            raise ValueError('bounded owned regular single-link classpath required')
        digest = hashlib.sha256()
        for block in iter(lambda: stream.read(65536), b''):
            digest.update(block)
        after = os.fstat(stream.fileno())
        keys = ('st_dev', 'st_ino', 'st_size', 'st_mtime_ns', 'st_ctime_ns')
        if any(getattr(before, k) != getattr(after, k) for k in keys):
            raise ValueError('classpath file changed while hashing')
    return {'path': str(path.relative_to(work)), 'bytes': before.st_size, 'sha256': digest.hexdigest()}


def read_json(work, path, limit=2 * 1024 ** 2):
    row = file_row(work, path)
    if row['bytes'] > limit:
        raise ValueError('bounded producer JSON required')
    body = Path(path).read_bytes()
    if len(body) != row['bytes'] or hashlib.sha256(body).hexdigest() != row['sha256']:
        raise ValueError('producer JSON changed while reading')
    return json.loads(body)


def distribution(work):
    rows = [file_row(work, p) for p in sorted((work / 'gradle/lib').rglob('*.jar'))]
    if not rows or len(rows) > 2048 or sum(r['bytes'] for r in rows) > 4 * s.GIB:
        raise ValueError('bounded official Gradle classloader inputs required')
    return rows


def verify_raw(work, label, raw, original_distribution):
    if (raw['schema'] != 1 or raw['cache'] != label or raw['Gradle'] != '9.3.1'
            or raw['application_or_fixture_plugins'] is not False
            or set(raw['notations']) != set(NOTATIONS)):
        raise ValueError('exact builtin preseal report required')
    prefix = 'gradle-cache' if label == 'actual' else 'selector-gradle-home'
    installed = {r['path']: r for r in original_distribution}
    for notation, value in raw['notations'].items():
        if (value['component_class'] != CLASS or value['notation'] != notation
                or not value['files'] or len(value['files']) > 512
                or len({r['path'] for r in value['files']}) != len(value['files'])):
            raise ValueError('exact bounded builtin notation files required')
        for row in value['files']:
            body = {k: row[k] for k in ('path', 'bytes', 'sha256')}
            if file_row(work, work / s.name(row['path'])) != body:
                raise ValueError('presealed builtin body differs')
            if row['origin'] == 'verified-Gradle-distribution':
                if installed.get(row['path']) != body:
                    raise ValueError('builtin distribution file lacks independent producer input')
            elif row['origin'] == 'first-builtin-model-runtime-shaded-output':
                allowed = {prefix + '/caches/9.3.1/generated-gradle-jars/gradle-' + n + '-9.3.1.jar'
                           for n in ('api', 'test-kit')}
                if row['path'] not in allowed:
                    raise ValueError('unexpected builtin generated jar')
            else:
                raise ValueError('unknown builtin producer')


def seal(job, here):
    work, here = job.w, Path(here)
    if (work / ROOT).exists() or (work / ROOT).is_symlink():
        raise ValueError('first producer only; no partial preseal resume')
    if any((work / n).exists() for n in ('gradle-project-cache', 'selector-project-cache')):
        raise ValueError('classpath preseal must precede app/fixture model parsing')
    for prefix in ('gradle-cache', 'selector-gradle-home'):
        if (work / prefix / 'caches/9.3.1/generated-gradle-jars').exists():
            raise ValueError('preexisting generated classpath is not a producer proof')
    archives = {}
    for name, expected in [('gradle', GRADLE_ZIP), ('jdk', JDK_ARCHIVE)]:
        raw = read_json(work, work / 'evidence' / ('download-' + name + '.json'))
        if raw['sha256'] != expected or raw['extracted_root'] != name:
            raise ValueError('pinned official Gradle/JDK producer missing')
        archives[name] = raw
    reviewed_sources = json.loads((here / 'gradle-producer-sources.json').read_text())
    initial = distribution(work)
    s.write_json(work / ROOT / 'intent.json', {'state': 'before_first_Gradle_models',
        'producer_archives': archives, 'distribution_files': initial,
        'generated_directories_initially_absent': True,
        'script_sha256': s.sha(here / 'preseal-classpaths.gradle'),
        'reviewed_generator_source_metadata_sha256': s.sha(here / 'gradle-producer-sources.json'),
        'reviewed_generator_sources': reviewed_sources})
    reports = {}
    for label, cache in [('actual', 'gradle-cache'), ('selector', 'selector-gradle-home')]:
        project = work / ROOT / ('project-' + label)
        s.put(project / 'settings.gradle', b"rootProject.name = 'luobo-builtin-classpath-preseal'\n")
        s.put(project / 'build.gradle', b'// No application, fixture or external plugins.\n')
        env = parent.environment(work, True)
        env['GRADLE_USER_HOME'] = str(work / cache)
        command = [work / 'gradle/bin/gradle', '--offline', '--no-daemon', '--max-workers=1',
            '--no-watch-fs', '--console=plain', '--project-cache-dir', work / ('preseal-cache-' + label),
            '-Dorg.gradle.jvmargs=-Xmx1536m -XX:MaxMetaspaceSize=512m',
            '-I', here / 'preseal-classpaths.gradle', '-PluoboPresealCache=' + label, 'sealOwnedClasspaths']
        job.cmd(command, project, env, 'classpath-preseal-' + label, 180)
        path = work / ROOT / (label + '.json')
        reports[label] = read_json(work, path)
        verify_raw(work, label, reports[label], initial)
        s.sync(path.parent)
    if distribution(work) != initial:
        raise ValueError('official Gradle classloader inputs changed during preseal')
    catalog = {'schema': 1, 'Gradle': '9.3.1', 'producer_archives': archives,
        'distribution_files': initial, 'reports': reports,
        'reviewed_generator_source_metadata_sha256': s.sha(here / 'gradle-producer-sources.json'),
        'reviewed_generator_sources': reviewed_sources,
        'application_or_fixture_parsed_before_producer': False}
    path = work / ROOT / 'catalog.json'
    s.write_json(path, catalog)
    s.write_json(work / ROOT / 'complete.json', {'state': 'passed', 'catalog': {
        'path': str(path.relative_to(work)), 'bytes': path.stat().st_size, 'sha256': s.sha(path)},
        'producer_models': ['actual', 'selector'], 'no_module_network_or_app_plugins': True})
    return verified_catalog(work)


def verified_catalog(work):
    work = Path(work)
    receipt = read_json(work, work / ROOT / 'complete.json')
    if receipt['state'] != 'passed' or receipt['producer_models'] != ['actual', 'selector']:
        raise ValueError('complete independent producer required')
    row = receipt['catalog']
    if row['path'] != ROOT + '/catalog.json' or row['bytes'] > 2 * 1024 ** 2:
        raise ValueError('fixed bounded catalog required')
    path = work / row['path']
    if file_row(work, path) != row:
        raise ValueError('producer catalog bytes differ')
    return path, row['sha256']
