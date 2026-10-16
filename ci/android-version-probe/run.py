#!/usr/bin/env python3
"""Standalone stage1 only: selected-version evidence, never a native build."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import stat
import sys
import uuid

import broker

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
COHORT = 'current257-rich-version-provenance-v1'
GIB = 1024 ** 3


def sha(path):
    h = hashlib.sha256()
    with Path(path).open('rb') as f:
        for b in iter(lambda: f.read(65536), b''):
            h.update(b)
    return h.hexdigest()


def read(path):
    return json.loads(Path(path).read_text())


def regular(path, maximum=4 * 1024 ** 2):
    path = Path(path)
    info = path.lstat()
    if (path.resolve(strict=True) != path or not stat.S_ISREG(info.st_mode)
            or info.st_uid != os.getuid() or info.st_nlink != 1 or info.st_size > maximum):
        raise ValueError('owned canonical bounded single-link input required')
    return info


def readonly():
    registry = read(HERE / 'cohort.json')
    if registry['cohort'] != COHORT or registry['native_execution'] is not False:
        raise ValueError('exact provenance-only cohort required')
    own = ['broker.py', 'run.py', 'producer.gradle', 'inputs.json', 'test_probe.py', 'offline.sh',
        'fixtures/kotlinx-coroutines-core-jvm-1.8.0.module', 'fixtures/kotlinx-coroutines-core-1.8.0.module']
    expected = {'ci/android-version-probe/' + x for x in own} | {
        '.github/workflows/android-rich-version-provenance.yml', 'ci/secret_scan.py',
        *('ci/android-native/' + x for x in ('run.py','support.py','audit.py','verify_inputs.py','toolchain.json'))}
    if len(registry['files']) != len(expected) or {r['path'] for r in registry['files']} != expected:
        raise ValueError('exact complete producer and borrowed path set required')
    for r in registry['files']:
        parts = r['path'].split('/')
        if r['path'].startswith('/') or any(x in ('', '.', '..') for x in parts):
            raise ValueError('invalid registry input')
        p = REPO.joinpath(*parts)
        if regular(p, 1024 ** 2).st_size != r['bytes'] or sha(p) != r['sha256']:
            raise ValueError('frozen producer/borrowed source mismatch: ' + r['path'])
    return registry


def load_parent():
    native = REPO / 'ci/android-native'
    sys.path.insert(0, str(native))
    spec = importlib.util.spec_from_file_location('_r11_pinned_native_parent', native / 'run.py')
    parent = importlib.util.module_from_spec(spec); spec.loader.exec_module(parent)
    return parent, parent.s


def admission(work, initial, fresh):
    work = Path(work)
    parent = work.parent
    if (not work.is_absolute() or work.name in ('', '.', '..') or any(c in str(work) for c in ('\r','\n','\\','\x00')) or work.is_relative_to(REPO)
            or parent.resolve(strict=True) != parent or parent.is_symlink()):
        raise ValueError('canonical separate output required')
    info = parent.stat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) != 0o700:
        raise ValueError('owned0700 output parent required')
    if fresh and (work.exists() or work.is_symlink()):
        raise ValueError('new workspace only; no partial resume')
    if not fresh and (work.resolve(strict=True) != work or not work.is_dir() or work.stat().st_uid != os.getuid()):
        raise ValueError('exact existing job root required')
    for p in (REPO, parent):
        v = os.statvfs(p)
        if v.f_bavail * v.f_frsize < initial:
            raise ValueError('initial host/output disk gate')


def environment(work):
    return {'HOME': str(work / 'home'), 'JAVA_HOME': str(work / 'jdk'),
        'GRADLE_USER_HOME': str(work / 'gradle-home'), 'PATH': str(work / 'jdk/bin') + ':/usr/bin:/bin',
        'TMPDIR': str(work / 'tmp'), 'LANG': 'C.UTF-8', 'LC_ALL': 'C.UTF-8', 'TZ': 'UTC', 'CI': 'true'}


def command(work, endpoint, phase):
    return [work / 'gradle/bin/gradle', '--no-daemon', '--max-workers=1', '--no-watch-fs', '--console=plain',
        '--project-cache-dir', work / 'project-cache', '-Dorg.gradle.jvmargs=-Xmx1536m -XX:MaxMetaspaceSize=512m',
        '-Djava.net.useSystemProxies=false', '-DluoboProbeWork=' + str(work), '-DluoboProbeOrigin=' + endpoint,
        '-DluoboProbeOutput=' + str(work / 'evidence' / (phase + '-actual.json')),
        *(['--offline'] if phase == 'offline' else []), 'produceVersionCatalog']


def source_state(work):
    for name in ('build.gradle', 'settings.gradle', 'gradle.properties', 'inputs.json'):
        regular(work / 'project' / name)
    return {name: {'bytes': (work / 'project' / name).stat().st_size, 'sha256': sha(work / 'project' / name)}
        for name in ('build.gradle', 'settings.gradle', 'gradle.properties', 'inputs.json')}


def verify_graph(work, phase, inputs, responses, events, published=None):
    actual = read(work / 'evidence' / (phase + '-actual.json'))
    if actual['schema'] != 1 or actual['Gradle'] != '9.3.1' or len(actual['configurations']) != 2:
        raise ValueError('incomplete actual Gradle report')
    by_path = {r['path']: r for r in responses}
    for e in events:
        if e.get('response') and e['response'] != by_path.get(e['response']['path']):
            raise ValueError('HTTP event no longer matches captured response')
    consumed = {e['response']['path'] for e in events if e.get('response') and e['complete_GET']
        and e['method'] == 'GET' and e['response']['status'] == 200
        and e['HTTP_status'] == 200 and not e['partial_write_bytes_unknown']
        and e['delivered_body_bytes'] == e['response']['body']['bytes']
        and e['confirmed_body_sha256'] == e['response']['body']['sha256']}
    expected_rich = {(r['configuration'], r['group'], r['module'], json.dumps(r['constraint'], sort_keys=True)) for r in inputs['rich']}
    seen_rich, sources = set(), []
    for c in actual['configurations']:
        expected_roots = sorted((r['group'], r['module'], r['constraint']) for r in inputs['roots'] if r['configuration'] == c['name'])
        got_roots = sorted((r['group'], r['module'], r['constraint']) for r in c['declared'])
        if expected_roots != got_roots or c['attributes'] != {}:
            raise ValueError('producer root/configuration attributes differ from reviewed source')
        if len(c['components']) > 128 or len(c['edges']) > 256 or len(c['artifacts']) > 256:
            raise ValueError('selected graph exceeds bound')
        if not c['components'] or not c['artifacts']:
            raise ValueError('actual module graph and artifacts required')
        ids = {tuple(x['id'][k] for k in ('group', 'module', 'version')) for x in c['components']}
        selected_ids = {tuple(x['selected'][k] for k in ('group', 'module', 'version')) for x in c['edges']}
        if ids != selected_ids:
            raise ValueError('resolved component/edge closure incomplete')
        for root in (r for r in inputs['roots'] if r['configuration'] == c['name']):
            if not any(x['from'] == {'kind': 'producer-root'} and x['requested']['group'] == root['group']
                    and x['requested']['module'] == root['module'] and x['requested']['constraint'] == root['constraint']
                    and x['selected']['group'] == root['group'] and x['selected']['module'] == root['module'] for x in c['edges']):
                raise ValueError('declared root not actually resolved')
        for edge in c['edges']:
            q, selected = edge['requested'], edge['selected']
            v = q['constraint']['strict'] or q['constraint']['required'] or q['constraint']['preferred']
            if not broker.fixed(selected['version']):
                raise ValueError('nonfixed selected module')
            if broker.rich(q['constraint']):
                key = (c['name'], q['group'], q['module'], json.dumps(q['constraint'], sort_keys=True))
                if key not in expected_rich:
                    raise ValueError('unreviewed fourth rich selector')
                spec = next(r for r in inputs['rich'] if (r['configuration'], r['group'], r['module']) == key[:3])
                if spec.get('source_parent') or spec.get('source_parents'):
                    parent = edge['from']
                    if ':'.join(parent.get(x, '') for x in ('group', 'module', 'version')) not in spec.get('source_parents', [spec.get('source_parent')]):
                        raise ValueError('rich selector parent provenance differs')
                elif edge['from'] != {'kind': 'producer-root'}:
                    raise ValueError('Kotlin strict source is not reviewed direct root')
                seen_rich.add(key)
            if q['constraint']['branch'] is not None:
                raise ValueError('branch selector refused')
        for module in c['components']:
            identity = module['id']
            prefix = '/maven2/' + identity['group'].replace('.', '/') + '/' + identity['module'] + '/' + identity['version'] + '/'
            base = identity['module'] + '-' + identity['version']
            if not any(prefix + base + ext in consumed for ext in ('.pom', '.module')):
                raise ValueError('selected module lacks a real consumed broker metadata body')
        for artifact in c['artifacts']:
            path = work / artifact['path']
            if not path.is_relative_to(work / 'gradle-home/caches/modules-2/files-2.1'):
                raise ValueError('artifact outside fresh Gradle module cache')
            if regular(path, 128 * 1024 ** 2).st_size != artifact['bytes'] or sha(path) != artifact['sha256']:
                raise ValueError('actual selected artifact changed')
            i = artifact['id']
            origin = '/maven2/' + i['group'].replace('.', '/') + '/' + i['module'] + '/' + i['version'] + '/' + path.name
            r = by_path.get(origin)
            if (origin not in consumed or not r or r['kind'] != 'artifact' or r['body']['bytes'] != artifact['bytes']
                    or r['body']['sha256'] != artifact['sha256']):
                raise ValueError('selected artifact lacks exact actually consumed origin response')
            pin = (published or {}).get(origin)
            if pin:
                if pin['declared'].get('size', artifact['bytes']) != artifact['bytes']:
                    raise ValueError('published artifact size differs')
                for algorithm in ('sha256','sha512','sha1','md5'):
                    if algorithm in pin['declared']:
                        h = hashlib.new(algorithm)
                        with path.open('rb') as stream:
                            for b in iter(lambda:stream.read(65536), b''): h.update(b)
                        if h.hexdigest() != pin['declared'][algorithm]:
                            raise ValueError('published artifact checksum differs')
            sources.append({'configuration': c['name'], 'id': i, 'variant': artifact['variant'], 'source': r,
                'published_metadata_pin_checked_if_present':pin})
    if seen_rich != expected_rich:
        raise ValueError('not all three reviewed constraints were actually resolved')
    return actual, sources


def evidence_rows(work):
    snap = read(work / 'evidence/broker/after-online.json')
    if snap['failure'] is not None:
        raise ValueError('broker failure was latched')
    for r in snap['responses']:
        if (not re.fullmatch(r'(?:evidence/broker/raw|body-cache/artifacts)/[0-9a-f]{64}\.body', r['body']['path'])
                or r['upstream'] != broker.ORIGIN + r['path']):
            raise ValueError('unexpected body path or origin mapping')
        broker.clean_path(r['path'])
        p = work / r['body']['path']
        if regular(p, 128 * 1024 ** 2).st_size != r['body']['bytes'] or sha(p) != r['body']['sha256']:
            raise ValueError('captured origin body differs')
    events = [read(p) for p in sorted((work / 'evidence/broker/events').glob('*.json'))]
    if len(events) != snap['requests'] or any(e['failure'] is not None for e in events):
        raise ValueError('incomplete or failed actual HTTP event evidence')
    return snap, events


def execute(stage, work, registry):
    initial = 8 * GIB if stage == 'acquire' else 4 * GIB
    admission(work, initial, stage == 'acquire')
    parent, s = load_parent()
    class ProbeBudget(s.Budget):
        def sample(self):
            free = super().sample()
            if self.peak > 4 * GIB:
                self.latch('probe_owned4GiB_cap')
            self.check(); return free
        def record(self):
            return {**super().record(), 'cap_bytes': 4 * GIB}
    if stage == 'acquire':
        s.mkdir(work)
    for d in ('evidence', 'downloads', 'home', 'tmp'):
        s.mkdir(work / d)
    claim = work / 'evidence' / (stage + '-claim.json')
    s.write_json(claim, {'state': 'reserved_not_passed', 'cohort': COHORT, 'source_registry_sha256': sha(HERE / 'cohort.json'),
        'work': str(work), 'nonce': uuid.uuid4().hex})
    b = ProbeBudget(work, REPO); b.start()
    handlers = {sig: signal.getsignal(sig) for sig in (signal.SIGTERM, signal.SIGINT)}
    for sig in handlers:
        signal.signal(sig, lambda number, frame: b.latch('external_signal_' + str(number)))
    failure = None
    try:
        inputs = read(HERE / 'inputs.json')
        env = environment(work)
        if stage == 'acquire':
            job = parent.Job(work, None, None, b)
            for key in ('jdk', 'gradle'):
                job.tool(key, inputs['tools'][key], work / key, env)
            s.run([work / 'jdk/bin/java', '-version'], work, env, work, b, 'java-version', 60)
            s.run([work / 'gradle/bin/gradle', '--no-daemon', '--version'], work, env, work, b, 'gradle-version', 120)
            s.mkdir(work / 'project')
            s.put(work / 'project/build.gradle', (HERE / 'producer.gradle').read_bytes())
            s.put(work / 'project/inputs.json', (HERE / 'inputs.json').read_bytes())
            s.put(work / 'project/settings.gradle', b"rootProject.name = 'luobo-reviewed-rich-version-producer'\n")
            s.put(work / 'project/gradle.properties', ('org.gradle.daemon=false\norg.gradle.parallel=false\norg.gradle.caching=false\n'
                'org.gradle.java.installations.auto-download=false\norg.gradle.java.installations.auto-detect=false\n'
                'org.gradle.java.installations.paths=' + str(work / 'jdk') + '\n').encode())
            original_sources = source_state(work)
            r = broker.Recorder(work, inputs, b, broker.Central(b))
            # These are provenance inputs, not claimed to be Gradle-consumed responses.
            for pin in inputs['metadata_pins']:
                if pin.get('bootstrap', True):
                    r.obtain(pin['path'])
            with broker.Broker(r) as server:
                s.write_json(work / 'evidence/activation.json', {'cohort': COHORT, 'work': str(work), 'endpoint': server.url,
                    'source_registry_sha256': sha(HERE / 'cohort.json'), 'project_sources': original_sources,
                    'system_CA_sha256': sha(r.origin.ca), 'online_boundary': 'literal loopback repository plus fixed TLS upstream; not OS egress isolation'})
                try:
                    s.run(command(work, server.url, 'online'), work / 'project', env, work, b, 'online-producer', 900)
                finally:
                    r.snapshot('after-online')
            captured, events = evidence_rows(work)
            actual, sources = verify_graph(work, 'online', inputs, captured['responses'], events, captured['published_artifact_pins'])
            if source_state(work) != original_sources:
                raise ValueError('generator inputs changed')
            s.write_json(work / 'evidence/acquired-catalog.json', {'state': 'actual_online_only_pending_offline',
                'source_registry_sha256': sha(HERE / 'cohort.json'), 'graph_sha256': sha(work / 'evidence/online-actual.json'),
                'selected_source_rows': sources, 'selected_versions_are_actual_not_guessed': True,
                'broker_snapshot_sha256':sha(work / 'evidence/broker/after-online.json'),
                'origin_responses':captured['responses'], 'metadata_authority':captured['source_authority'],
                'published_artifact_pins':captured['published_artifact_pins'],
                'HTTP_events':[{ 'path':str(p.relative_to(work)), 'bytes':p.stat().st_size, 'sha256':sha(p) }
                    for p in sorted((work / 'evidence/broker/events').glob('*.json'))]})
        else:
            parent.netns()
            activation = read(work / 'evidence/activation.json')
            if (activation['cohort'] != COHORT or activation['work'] != str(work)
                    or activation['source_registry_sha256'] != sha(HERE / 'cohort.json')
                    or source_state(work) != activation['project_sources']):
                raise ValueError('exact successful acquire activation required')
            acquired = read(work / 'evidence/acquired-catalog.json')
            if acquired['graph_sha256'] != sha(work / 'evidence/online-actual.json'):
                raise ValueError('acquired report changed before offline')
            captured, events = evidence_rows(work)
            if acquired['broker_snapshot_sha256'] != sha(work / 'evidence/broker/after-online.json'):
                raise ValueError('acquired origin snapshot changed')
            # Server is deliberately absent; namespace is loopback-only and argv --offline.
            s.run(command(work, activation['endpoint'], 'offline'), work / 'project', env, work, b, 'offline-producer', 900)
            verify_graph(work, 'offline', inputs, captured['responses'], events, captured['published_artifact_pins'])
            if ((work / 'evidence/online-actual.json').read_bytes() != (work / 'evidence/offline-actual.json').read_bytes()
                    or source_state(work) != activation['project_sources']):
                raise ValueError('online/offline actual graph or source bytes differ')
            s.write_json(work / 'evidence/selected-catalog-candidate.json', {'state': 'actual_online_offline_match_pending_Root_acceptance',
                'cohort': COHORT, 'registry_sha256': sha(HERE / 'cohort.json'),
                'graph': read(work / 'evidence/offline-actual.json'), 'acquired': acquired,
                'no_native_or_fullbind_claim': True})
        readonly()
        s.write_json(work / 'evidence' / (stage + '-candidate.json'), {'state': 'phase_candidate_written_while_polling_failure_wins',
            'cohort': COHORT, 'resource_up_to_receipt': b.record(), 'terminal_record': 'stdout after finish/sample/join; no PASS before that'})
    except BaseException as error:
        failure = error
        b.latch('phase_or_infrastructure_failed')
        s.write_json(work / 'evidence' / (stage + '-failure.json'), {'state': 'failed_not_passed',
            'error': type(error).__name__ + ':' + str(error), **b.record()})
    finally:
        try:
            b.finish()
        except BaseException as error:
            failure = failure or error
        # No more owned writes after poll stops. The action job log captures the terminal sample.
        print(json.dumps({'state': 'failed' if failure else 'passed', 'stage': stage, **b.record()}), flush=True)
        for sig, handler in handlers.items():
            signal.signal(sig, handler)
    if failure:
        raise failure


def upload_admission(work):
    admission(work, GIB, False)
    parent, s = load_parent()
    files = sorted((work / 'body-cache/artifacts').glob('*.body'))
    if len(files) > 512:
        raise ValueError('source upload count limit')
    total = 0
    for p in files:
        if not re.fullmatch('[0-9a-f]{64}\\.body', p.name):
            raise ValueError('source upload exact captured filenames required')
        total += regular(p, 128 * 1024 ** 2).st_size
    v = os.statvfs(work)
    if (total > GIB or v.f_bavail * v.f_frsize < GIB + total + 8 * 1024 ** 2 or s.allocated(work) > 4 * GIB):
        raise ValueError('source upload worstcase disk gate')
    broker.json_new(work / 'evidence/source-upload-admission.json', {'state': 'admitted_not_native_acceptance',
        'files': len(files), 'bytes': total, 'temporary_copy_worstcase': total + 8 * 1024 ** 2,
        'artifact_action_outside_Python_continuous_poll': True})


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--cohort', required=True, choices=[COHORT])
    parser.add_argument('--stage', choices=['readonly', 'acquire', 'offline', 'upload-admission'], default='readonly')
    parser.add_argument('--work', type=Path)
    args = parser.parse_args()
    registry = readonly()
    if args.stage == 'readonly':
        print(json.dumps({'state': 'source_only_verified_zero_tools_or_network', 'cohort': COHORT, 'pins': len(registry['files'])}))
        return
    if args.work is None:
        parser.error('work required for explicit execution')
    os.umask(0o077)
    if args.stage == 'upload-admission':
        upload_admission(args.work)
        return
    execute(args.stage, args.work, registry)


if __name__ == '__main__':
    main()
