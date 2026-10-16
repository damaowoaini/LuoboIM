#!/usr/bin/env python3
"""Isolated scoped19 successor; immutable announcement26 lane remains untouched."""
import argparse
from collections import Counter
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import threading
from urllib.parse import unquote, urlparse

REPO = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('announcement26_inherited', REPO/'ci/mobile-announcement.py')
BASE = importlib.util.module_from_spec(spec)
spec.loader.exec_module(BASE)
INPUTS = REPO/'ci/announcement19/inputs.json'
SELECTED = ['app/test/features/chat/group_announcement_fixture.dart', 'app/test/features/chat/group_announcement_store_test.dart']
ANALYSIS = ['test/features/chat/group_announcement_fixture.dart', 'test/features/chat/group_announcement_store_test.dart']
TESTS = ['test/features/chat/group_announcement_store_test.dart', 'test/features/chat/group_announcement_workflow_test.dart']
NINETEEN = ['acknowledged exact-version dismissal survives reopening without consuming a republished version or another owner group', 'actual group chat opens a fresh ordinary-member announcement and its own detail stays readable', 'acknowledged announcement close survives a new real chat route while a republished version appears again', 'a preferences failure retains the visible announcement until one explicit retry is acknowledged', 'a delayed versionA dismissal never consumes a newly loaded versionB', 'a genuine at-me hint does not create an announcement from historical group text', 'failed fresh group metadata offers an actual read-only retry instead of an empty successful announcement', 'fresh duplicate group metadata withdraws the announcement before any acknowledgement', 'fresh foreign group metadata withdraws the announcement before any acknowledgement', 'fresh left group metadata withdraws the announcement before any acknowledgement', 'original owner replacement withdraws an open announcement and rejects its retained close callback', 'original issuer replacement withdraws an open announcement and rejects its retained close callback', 'original tenant replacement withdraws an open announcement and rejects its retained close callback', 'owner replacement while original group metadata waits prevents its next real member lookup', 'a same-State CID ABA cannot renew the original announcement dialog or its acknowledgement', 'foreign coverage retires only the old owned announcement and a new explicit read remains usable', 'foreground loss immediately rejects the old callback and the next frame withdraws announcement text', 'sameCID metadata refresh preserves composer draft and never consumes genuine strong hints', 'muted-admin announcement controls and full URL-like text remain usable at360 large text without automatic link behavior']
SEVEN = ['SDK hint4 is announcement while true at1to3 and preview stripping preserve independent identity', 'fresh snapshot bounds reject controls bidi malformed unicode and unavailable membership while URL text stays plain', 'preferences failure leaves the version unacknowledged and one explicit retry persists only a boolean digest key', 'queued preference dispatch rechecks its original source before physical acknowledgement', 'an already dispatched dismissal keeps its original key after source retirement without acknowledging another owner', 'SDK paging, drafts, read and preference mutations preserve the exact conversation target', 'group flags keep prohibition semantics and booleans serialize to SDK status 1']
FAILED_ORIGINAL = ['acknowledged exact-version dismissal survives reopening without consuming a republished version or another owner group', 'acknowledged announcement close survives a new real chat route while a republished version appears again']
HELPERS = ['ci/mobile-source.py', 'ci/mobile-check.py', 'ci/mobile-setup.sh', 'ci/mobile/inputs.json', 'ci/mobile/required-test-names.json', 'ci/mobile-announcement.py', 'ci/announcement26/inputs.json']
BASE_SOURCE = '1fd4048febdce707a91822f7b7ca56833a894c1d0251866734cd42d2ef2d8c8e'
BASE_PORTABLE = 'a2f024ce7225b6815d13e17ef40d3837be1824103cf47a56b885474849e050c5'
FINAL_SOURCE = '2e5a8afd7ee7370485954061b17be776c1da6162323807b48909ae2030477899'
FINAL_PORTABLE = '71cbb95b92c7a4603165a8d5e5cbba1d99cfb34cc66371a27af51db4d23b9ed3'
PAYLOAD = 'ci/candidates/mobile-announcement19/after'


def sha(body):
    return hashlib.sha256(body).hexdigest()


def validate(cfg):
    if (cfg['schema'] != 'luobo.announcement.scoped19.v1'
            or cfg['cohort'] != 'candidate359-announcement-fixture-r5-scoped19'
            or cfg['base']['setup_cohort'] != 'current354'
            or (cfg['base']['source_members'], cfg['base']['portable_members']) != (359, 411)
            or cfg['base']['source_digest'] != BASE_SOURCE or cfg['base']['portable_digest'] != BASE_PORTABLE
            or [r['path'] for r in cfg['base']['helper_pins']] != HELPERS
            or [r['path'] for r in cfg['base']['before2']] != SELECTED
            or cfg['candidate']['source_digest'] != FINAL_SOURCE
            or cfg['candidate']['portable_digest'] != FINAL_PORTABLE
            or (cfg['candidate']['source_members'], cfg['candidate']['portable_members']) != (359, 411)
            or cfg['candidate']['payload_prefix'] != PAYLOAD
            or [r['path'] for r in cfg['candidate']['selected2']] != SELECTED
            or any(r['payload_path'] != PAYLOAD+'/'+r['path'] for r in cfg['candidate']['selected2'])
            or cfg['target']['analysis_paths'] != ANALYSIS or cfg['target']['test_paths'] != TESTS
            or cfg['target']['names'] != NINETEEN or len(set(NINETEEN)) != 19
            or cfg['target']['expected19'] != 19
            or cfg['target']['cleanup_names'] != BASE.WIDGET_NAMES
            or cfg['target']['cleanup_markers'] != BASE.MARKERS
            or cfg['target']['cleanup_prefix'] != 'group_announcement_cleanup '
            or cfg['target']['fixtures'] != 18 or cfg['target']['cleanup_events'] != 72
            or cfg['reuse']['success_names'] != SEVEN
            or cfg['reuse']['old_failure_names'] != FAILED_ORIGINAL
            or cfg['reuse']['old_names26'] != BASE.TWENTY_SIX
            or cfg['resource'] != {'initial_bytes': 4294967296, 'floor_bytes': 1073741824,
                                   'allocated_cap_bytes': 805306368, 'poll_seconds': .2,
                                   'phase_seconds': 120}):
        raise ValueError('scoped19 identity, scope or resource differs')
    return cfg


def descriptor():
    cfg = validate(json.loads(BASE.regular(INPUTS)))
    for r in cfg['base']['helper_pins']:
        BASE.pinned(BASE.regular(REPO/BASE.safe(r['path'])), r)
    return cfg


def overlay_bytes(original, before, after, payload):
    if ([r['path'] for r in before] != SELECTED or [r['path'] for r in after] != SELECTED
            or set(payload) != set(SELECTED)):
        raise ValueError('scoped overlay requires exactly two existing paths')
    files = dict(original)
    for r in before:
        if r['path'] not in original:
            raise ValueError('missing original scoped source')
        BASE.pinned(original[r['path']], r)
    for r in after:
        files[r['path']] = BASE.pinned(payload[r['path']], r)
    return files


def case_block(body, name):
    text = body.decode('utf-8')
    starts = list(re.finditer(r'(?m)^  test\(', text))
    found = []
    for index, start in enumerate(starts):
        end = starts[index+1].start() if index+1 < len(starts) else len(text)
        part = text[start.start():end]
        if part.startswith("  test('"+name+"',"):
            found.append(part.encode('utf-8'))
    if len(found) != 1:
        raise ValueError('reused test registration block not unique')
    return found[0]


def successful_subset(text, expected_old, expected_failures, wanted):
    """A false-footer prior run proves only selected successful cases, never full26."""
    starts, done, errors, footer = {}, {}, [], []
    for line in text.splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if not isinstance(event, dict):
            continue
        kind = event.get('type')
        if footer and kind is not None:
            raise ValueError('prior reporter event after done')
        if kind == 'testStart':
            identity = event['test']['id']
            if identity in starts:
                raise ValueError('prior duplicate start')
            starts[identity] = event['test']['name']
        elif kind == 'testDone':
            identity = event['testID']
            if identity not in starts or identity in done:
                raise ValueError('prior unstarted or duplicate done')
            if type(event.get('hidden')) is not bool or type(event.get('skipped')) is not bool:
                raise ValueError('prior completion flags missing')
            done[identity] = event
        elif kind == 'error':
            errors.append(event['testID'])
        elif kind == 'done':
            footer.append(event.get('success'))
    if (len(footer) != 1 or footer[0] is not False or starts.keys() != done.keys()
            or any(e['skipped'] for e in done.values())
            or any(e['result'] != 'success' for e in done.values() if e['hidden'])):
        raise ValueError('prior run completion/false footer differs')
    visible = {starts[i]: e for i, e in done.items() if not e['hidden']}
    if (len(visible) != len(expected_old) or Counter(visible.keys()) != Counter(expected_old)
            or {n for n, e in visible.items() if e['result'] != 'success'} != set(expected_failures)
            or Counter(starts[i] for i in errors) != Counter(expected_failures)
            or any(n not in visible or visible[n]['result'] != 'success' for n in wanted)):
        raise ValueError('prior successful subset differs; cannot reuse a failing case')
    return {'successful_names': wanted, 'reused_successes': len(wanted),
            'original_false_footer': True, 'original_failures': expected_failures,
            'not_a_full26_pass': True}


def reused_sources(cfg, files):
    entries = cfg['reuse']['analysis9']
    all_paths = [p for p in BASE.ANALYSIS if 'app/'+p not in SELECTED]
    if len(entries) != 9 or [r['analysis_path'] for r in entries] != all_paths:
        raise ValueError('unchanged nine-analysis set differs')
    actual_rows = {p: {'path': p, 'bytes': len(b), 'sha256': sha(b)} for p, b in files.items()}
    for r in entries:
        if actual_rows.get(r['source_pin']['path']) != r['source_pin']:
            raise ValueError('reused static source changed')
        log = BASE.pinned(r['log_utf8'].encode('utf-8'), r['log_pin'])
        resource = json.loads(BASE.pinned(r['resource_utf8'].encode('utf-8'), r['resource_pin']))
        if (type(resource['child_exit_code']) is not int or resource['child_exit_code'] != 0
                or resource['latched_failure'] is not None
                or resource['log_sha256'] != sha(log)
                or resource['command'][-3:] != ['analyze', '--fatal-infos', r['analysis_path']]
                or 'No issues found!' not in log.decode('utf-8')):
            raise ValueError('reused static result is not successful')
    rows = cfg['reuse']['cases7']
    if [r['name'] for r in rows] != SEVEN:
        raise ValueError('reused seven case identities differ')
    for r in rows:
        body = files[r['source_path']]
        if r['mode'] == 'exact_registration_block':
            body = case_block(body, r['name'])
            if len(body) != r['block_bytes'] or sha(body) != r['block_sha256']:
                raise ValueError('reused test body changed')
        elif r['mode'] == 'exact_whole_file':
            BASE.pinned(body, r['source_pin'])
        else:
            raise ValueError('unknown reuse source mode')
    log = BASE.pinned(cfg['reuse']['raw_log_utf8'].encode('utf-8'), cfg['reuse']['raw_log_pin'])
    resource = json.loads(BASE.pinned(cfg['reuse']['raw_resource_utf8'].encode('utf-8'),
                                    cfg['reuse']['raw_resource_pin']))
    if (resource['child_exit_code'] != 1 or resource['latched_failure'] is not None
            or resource['log_sha256'] != sha(log)):
        raise ValueError('original partial test result changed')
    subset = successful_subset(log.decode('utf-8'), BASE.TWENTY_SIX, FAILED_ORIGINAL, SEVEN)
    return {'unchanged_static_sources_reused': 9, 'changed2_analysis_still_required': True,
            'real19_compilation_still_required': True, 'prior_tests': subset}


def sources(cfg):
    source, check = BASE.legacy()
    inherited = BASE.descriptor()
    plan, original = BASE.sources(inherited, source)
    if (len(original) != 411 or source.source_digest(original) != BASE_PORTABLE
            or source.source_digest({p: b for p, b in original.items() if p.startswith('app/')}) != BASE_SOURCE):
        raise ValueError('original R3 359/411 differs')
    root = REPO/PAYLOAD
    actual = set()
    for directory, dirs, names in os.walk(root, followlinks=False,
                                        onerror=lambda error: (_ for _ in ()).throw(error)):
        if any((Path(directory)/name).is_symlink() for name in dirs):
            raise ValueError('scoped payload symlink')
        for name in names:
            p = Path(directory)/name
            BASE.regular(p)
            actual.add(str(p.relative_to(root)))
    if actual != set(SELECTED):
        raise ValueError('scoped payload must be exact two')
    payload = {r['path']: BASE.pinned(BASE.regular(REPO/BASE.safe(r['payload_path'])), r)
               for r in cfg['candidate']['selected2']}
    files = overlay_bytes(original, cfg['base']['before2'], cfg['candidate']['selected2'], payload)
    if (len(files) != 411 or source.source_digest(files) != FINAL_PORTABLE
            or source.source_digest({p: b for p, b in files.items() if p.startswith('app/')}) != FINAL_SOURCE):
        raise ValueError('formatted successor aggregate differs')
    reused_sources(cfg, files)
    return plan, files, source, check


def verify_work(work, cfg):
    BASE.canonical_work(work)
    plan, files, source, check = sources(cfg)
    for name, body in files.items():
        if BASE.regular(work/'source'/BASE.safe(name)) != body:
            raise ValueError('scoped source changed: '+name)
    actual = set()
    for prefix in ('app/lib', 'app/test', 'app/tool'):
        for directory, dirs, names in os.walk(work/'source'/prefix, followlinks=False,
                                            onerror=lambda error: (_ for _ in ()).throw(error)):
            if any((Path(directory)/name).is_symlink() for name in dirs):
                raise ValueError('scoped workspace symlink')
            actual.update(str((Path(directory)/name).relative_to(work/'source')) for name in names)
    expected = {p for p in files if p.startswith(('app/lib/', 'app/test/', 'app/tool/'))}
    if actual != expected:
        raise ValueError('scoped authored inventory differs')
    config = work/'source/app/.dart_tool/package_config.json'
    for item in json.loads(BASE.regular(config))['packages']:
        uri = urlparse(item['rootUri'])
        if uri.scheme not in ('', 'file') or uri.netloc:
            raise ValueError('nonlocal package root')
        root = Path(unquote(uri.path))
        root = (root if root.is_absolute() else config.parent/root).resolve()
        allowed = [work/'flutter', work/'pub-cache', work/'source/app',
                   work/'source/packages/flutter_openim_sdk']
        if not any(root == p or root.is_relative_to(p) for p in allowed):
            raise ValueError('package outside fresh roots')
        if (item['name'] == 'imapp' and root != work/'source/app'
                or item['name'] == 'flutter_openim_sdk' and root != work/'source/packages/flutter_openim_sdk'):
            raise ValueError('wrong app/SDK package root')
    version = json.loads(BASE.regular(work/'evidence/flutter-version.json'))
    for key, wanted in [('frameworkVersion', plan['toolchain']['tag']),
                        ('frameworkRevision', plan['toolchain']['framework_revision']),
                        ('engineRevision', plan['toolchain']['engine_revision']),
                        ('dartSdkVersion', plan['toolchain']['dart_version'])]:
        if version.get(key) != wanted:
            raise ValueError('official toolchain differs')
    return files, source, check


def overlay(work):
    cfg = descriptor()
    BASE.canonical_work(work)
    # Genuine354 setup first; original26 overlay remains byte-identical and creates R3.
    BASE.overlay(work)
    files, source, check = verify_original(work)
    _, wanted, _, _ = sources(cfg)
    receipt = work/'evidence/announcement19-overlay.json'
    if receipt.exists() or any(p.suffix == '.dill' or p.name.endswith('.stamp')
                               for p in (work/'source').rglob('*') if p.is_file()):
        raise ValueError('scoped overlay must be fresh, without old compiled output')
    if os.statvfs(work).f_bavail * os.statvfs(work).f_frsize < check.INITIAL:
        raise ValueError('scoped initial4GiB admission')
    for r in cfg['candidate']['selected2']:
        if os.statvfs(work).f_bavail * os.statvfs(work).f_frsize < check.FLOOR:
            raise ValueError('scoped partial overlay stopped at resource floor')
        target = work/'source'/BASE.safe(r['path'])
        BASE.pinned(BASE.regular(target), next(b for b in cfg['base']['before2'] if b['path'] == r['path']))
        backup = work/'evidence/announcement19-before'/BASE.safe(r['path'])
        backup.parent.mkdir(parents=True, exist_ok=True)
        with backup.open('xb') as output:
            output.write(target.read_bytes())
        target.write_bytes(wanted[r['path']])
    verify_work(work, cfg)
    check.write_json(receipt, {'state': 'formatted_scoped359_overlay_only',
                              'source359': FINAL_SOURCE, 'portable411': FINAL_PORTABLE,
                              'base_R3_source359': BASE_SOURCE, 'files': source.rows(wanted),
                              'current_App354_and_original26_lane_untouched': True})


def verify_original(work):
    source, check = BASE.legacy()
    cfg = BASE.descriptor()
    files = BASE.verify_work(work, cfg, source)
    return files, source, check


def phase_commands(work):
    return ([[str(work/'flutter/bin/cache/dart-sdk/bin/dart'), 'analyze', '--fatal-infos', p]
             for p in ANALYSIS],
            [str(work/'flutter/bin/flutter'), 'test', '--no-pub', '--machine', '--concurrency=1',
             '--no-enable-vmservice', '--name', '^(?:'+'|'.join(map(re.escape, NINETEEN))+')$']+TESTS)


def run(work):
    cfg = descriptor()
    files, source, check = verify_work(work, cfg)
    check.prove_namespace()
    guard = check.Guard(work)
    if guard.sample() < check.INITIAL or guard.reason:
        raise ValueError('scoped initial4GiB/cap admission')
    thread = threading.Thread(target=guard.poll, daemon=True)
    thread.start()
    try:
        analyses, tests = phase_commands(work)
        proof = reused_sources(cfg, files)
        check.write_json(work/'evidence/announcement19-reuse.json',
                         {'proof': proof, 'entries': cfg['reuse']})
        results = []
        for index, command in enumerate(analyses, 1):
            verify_work(work, cfg)
            label = 'announcement19-analyze-'+str(index)
            log = check.guarded(command, work, guard, label,
                                timeout=cfg['resource']['phase_seconds'], accepted_exits=(0, 1, 2, 3))
            result = BASE.analysis_outcome(json.loads(BASE.regular(work/'evidence'/(label+'.resource.json'))),
                                           log.read_text(), label, command)
            results.append(result)
            verify_work(work, cfg)
        check.write_json(work/'evidence/announcement19-analyses.json', results)
        if not all(r['passed'] for r in results):
            raise ValueError('changed2 Dart diagnostics; scoped19 not started')
        log = check.guarded(tests, work, guard, 'announcement19-target',
                            timeout=cfg['resource']['phase_seconds'])
        text = log.read_text()
        counts = {'tests': check.machine_summary(text, 19),
                  'names': check.required_name_summary(text, 19, [{'name': n, 'count': 1} for n in NINETEEN]),
                  'cleanup': BASE.cleanup_summary(text), 'reuse': proof}
        if BASE.ui_warnings(text):
            raise ValueError('scoped UI tap/overflow warnings')
        verify_work(work, cfg)
        check.write_json(work/'evidence/announcement19-passed.json',
                         {'state': 'target19_success_plus_changed2analysis0_only', 'counts': counts,
                          'union26_not_a_fresh26': True, 'source359': FINAL_SOURCE,
                          'portable411': FINAL_PORTABLE, 'device_service_acceptance': False})
        guard.sample()
    except BaseException as error:
        check.write_json(work/'evidence/announcement19-failed.json',
                         {'type': type(error).__name__, 'detail': str(error), 'latched_failure': guard.reason})
        raise
    finally:
        try:
            check.write_json(work/'evidence/announcement19-final-resource.json',
                             {'minimum_free_bytes': guard.minimum, 'peak_workspace_allocated_bytes': guard.peak,
                              'latched_failure': guard.reason, 'failure_wins_over_passed_receipt': True})
            guard.sample()
        finally:
            guard.stop.set()
            thread.join()
    if guard.reason:
        raise ValueError('scoped final resource failure')


def netns(work, run_as):
    if os.geteuid() != 0 or not run_as or run_as == 'root':
        raise ValueError('root namespace launcher/nonroot user required')
    BASE.canonical_work(work)
    env = BASE.run_environment(work, run_as)
    subprocess.run(['ip', 'link', 'set', 'lo', 'up'], check=True)
    descriptor()
    _, check = BASE.legacy()
    check.prove_namespace()
    command = ['/usr/sbin/runuser', '-u', run_as, '--', 'env', '-i']
    command += [k+'='+v for k, v in env.items()]
    command += ['python3', str(Path(__file__).resolve()), 'run', '--work', str(work)]
    os.execv(command[0], command)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=('verify', 'overlay', 'netns', 'run'))
    parser.add_argument('--work', type=Path)
    parser.add_argument('--run-as')
    args = parser.parse_args()
    if args.mode == 'verify' and args.work is None:
        cfg = descriptor()
        _, files, source, _ = sources(cfg)
        print(json.dumps({'state': 'readonly_scoped359', 'source359': FINAL_SOURCE,
                          'portable411': source.source_digest(files)}))
    elif args.work is None:
        parser.error('work required')
    elif args.mode == 'overlay':
        overlay(args.work)
    elif args.mode == 'netns':
        netns(args.work, args.run_as)
    elif args.mode == 'run':
        run(args.work)
    else:
        verify_work(args.work, descriptor())


if __name__ == '__main__':
    main()
