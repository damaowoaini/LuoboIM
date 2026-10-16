#!/usr/bin/env python3
"""Manual isolated V3 overlay; App351 and its existing CI identities stay intact."""
import argparse
from collections import Counter
import hashlib
import importlib.util
import json
import os
from pathlib import Path, PurePosixPath
import pwd
import re
import stat
import subprocess
import threading
from urllib.parse import unquote, urlparse

REPO = Path(__file__).resolve().parent.parent
INPUTS = REPO / 'ci/group-revoke-v3/inputs.json'
PAYLOAD = 'ci/candidates/mobile-group-revoke-v3/after'
PRODUCT = ['app/lib/features/chat/data/chat_repository.dart',
           'app/lib/features/chat/presentation/chat_controller.dart',
           'app/lib/features/chat/presentation/chat_page.dart']
SELECTED = PRODUCT + ['app/test/features/chat/group_revoke_fixture.dart',
                      'app/test/features/chat/group_revoke_repository_test.dart',
                      'app/test/features/chat/group_revoke_workflow_test.dart']
ANALYSIS = ['lib/features/chat/presentation/chat_controller.dart',
            'lib/features/chat/presentation/chat_page.dart',
            'test/features/chat/group_revoke_workflow_test.dart']
FIVE = [
    'an unannounced downgrade is denied by the final real role lookup after an enabled confirm',
    'same ChatPage State CID ABA cannot renew its original enabled confirmation',
    'foreground loss retires moderation synchronously and withdraws its confirm on the next frame',
    'foreign coverage and return retire old confirm while a new explicit moderation still works',
    'late successful revoke ACK does not pop a foreign route or claim the dispatched write was cancelled',
]
TEST = 'test/features/chat/group_revoke_workflow_test.dart'
MARKERS = ['sdk.begin', 'sdk.end', 'unmount.begin', 'unmount.end']
HELPERS = ['ci/mobile-source.py', 'ci/mobile-check.py', 'ci/mobile-setup.sh',
           'ci/mobile/inputs.json', 'ci/mobile/required-test-names.json']


def sha(body):
    return hashlib.sha256(body).hexdigest()


def safe(name):
    p = PurePosixPath(name)
    if (p.is_absolute() or str(p) != name or not name or '\\' in name
            or ':' in name or '\x00' in name
            or any(x in ('', '.', '..', '.dart_tool', 'build') for x in name.split('/'))):
        raise ValueError('noncanonical input path')
    return p


def regular(path):
    if path.resolve() != path or not stat.S_ISREG(path.lstat().st_mode):
        raise ValueError('regular nonsymlink input required')
    return path.read_bytes()


def pinned(body, row):
    if len(body) != row['bytes'] or sha(body) != row['sha256']:
        raise ValueError('source pin differs: ' + row['path'])
    return body


def validate_descriptor(cfg):
    if (cfg['schema'] != 'luobo.mobile.group-revoke.V3.isolated-cloud5.v1'
            or cfg['cohort'] != 'candidate354-group-revoke-v3'
            or (cfg['base']['cohort'], cfg['base']['source_members'], cfg['base']['portable_members'])
            != ('current351', 351, 403)
            or [r['path'] for r in cfg['base']['helper_pins']] != HELPERS
            or cfg['base']['source_digest'] != 'fc2644ca9333596900be65f95fe0250f2d2106bb8429bc8f2f595c501bb2aba6'
            or cfg['base']['portable_digest'] != '3e87bd5ae01e2b9ee24b483981585820ab1b1e9745ba7cad66a42c0f8bb40ed9'
            or cfg['candidate']['source_digest'] != '113f8a3ff3cfaa3d17ba77b67cb28605a1220fa86359b8f0661535180075364c'
            or cfg['candidate']['portable_digest'] != '81f0628bda255611d9a624aee518d1a77e63cd1dfdc07d91f9a932c33c7f199c'
            or cfg['candidate']['payload_prefix'] != PAYLOAD
            or [r['path'] for r in cfg['base']['before3']] != PRODUCT
            or [r['path'] for r in cfg['candidate']['selected6']] != SELECTED
            or [r['new'] for r in cfg['candidate']['selected6']] != [False]*3 + [True]*3
            or any(r['payload_path'] != PAYLOAD + '/' + r['path'] for r in cfg['candidate']['selected6'])
            or cfg['candidate']['source_members'] != 354 or cfg['candidate']['portable_members'] != 406
            or cfg['target']['analysis_paths'] != ANALYSIS or cfg['target']['names'] != FIVE
            or cfg['target']['expected_tests'] != 5 or cfg['target']['test_path'] != TEST
            or cfg['target']['cleanup_prefix'] != 'group_revoke_cleanup '
            or cfg['target']['cleanup_markers'] != MARKERS
            or cfg['resource'] != {'initial_bytes': 4294967296, 'floor_bytes': 1073741824,
                                   'allocated_cap_bytes': 805306368, 'poll_seconds': .2,
                                   'phase_seconds': 120}):
        raise ValueError('isolated V3 scope or resource contract differs')
    for row in cfg['candidate']['selected6'] + cfg['base']['before3']:
        safe(row['path'])
        if type(row['bytes']) is not int or row['bytes'] <= 0 or not re.fullmatch('[0-9a-f]{64}', row['sha256']):
            raise ValueError('invalid byte/hash pin')
    if any(type(row['new']) is not bool for row in cfg['candidate']['selected6']):
        raise ValueError('new flag must be boolean')
    return cfg


def descriptor():
    cfg = validate_descriptor(json.loads(regular(INPUTS)))
    for row in cfg['base']['helper_pins']:
        pinned(regular(REPO / safe(row['path'])), row)
    return cfg


def legacy():
    spec = importlib.util.spec_from_file_location('group_revoke_existing_check', REPO / 'ci/mobile-check.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module.source, module


def apply_overlay(base, before, selected, payload):
    """Pure exact-before/collision contract; production also checks full counts/digests."""
    if [r['path'] for r in before] != PRODUCT or [r['path'] for r in selected] != SELECTED:
        raise ValueError('overlay paths differ')
    result = dict(base)
    for row in before:
        if row['path'] not in result:
            raise ValueError('missing original before')
        pinned(result[row['path']], row)
    if set(payload) != set(SELECTED):
        raise ValueError('missing or extra payload body')
    for row in selected:
        name = row['path']
        if row['new'] != (name not in base):
            raise ValueError('new payload collision or missing original')
        result[name] = pinned(payload[name], row)
    return result


def sources(cfg, source):
    base, files, digest = source.load_sources('current351')
    if (digest != cfg['base']['source_digest'] or len(files) != 403
            or source.source_digest(files) != cfg['base']['portable_digest']):
        raise ValueError('original351/403 identity differs')
    actual = set()
    root = REPO / PAYLOAD
    for directory, dirs, names in os.walk(root, followlinks=False,
                                          onerror=lambda error: (_ for _ in ()).throw(error)):
        if any((Path(directory)/name).is_symlink() for name in dirs):
            raise ValueError('payload directory symlink')
        for name in names:
            p = Path(directory)/name
            regular(p)
            actual.add(str(p.relative_to(root)))
    if actual != set(SELECTED):
        raise ValueError('payload tree is not exact six')
    payload = {r['path']: pinned(regular(REPO / safe(r['payload_path'])), r)
               for r in cfg['candidate']['selected6']}
    files = apply_overlay(files, cfg['base']['before3'], cfg['candidate']['selected6'], payload)
    app = {name: body for name, body in files.items() if name.startswith('app/')}
    if (len(app) != 354 or len(files) != 406
            or source.source_digest(app) != cfg['candidate']['source_digest']
            or source.source_digest(files) != cfg['candidate']['portable_digest']):
        raise ValueError('candidate354/406 aggregate differs')
    return base, files


def canonical_work(work):
    if (not work.is_absolute() or work.resolve() != work or not work.is_dir()
            or work.is_relative_to(REPO) or not (work/'evidence').is_dir()):
        raise ValueError('fresh owned CI root outside checkout required')


def verify_work(work, cfg, source):
    canonical_work(work)
    base, files = sources(cfg, source)
    for name, body in files.items():
        if regular(work/'source'/safe(name)) != body:
            raise ValueError('candidate workspace differs: ' + name)
    authored = set()
    for prefix in ('app/lib', 'app/test', 'app/tool'):
        for directory, dirs, names in os.walk(work/'source'/prefix, followlinks=False,
                                              onerror=lambda error: (_ for _ in ()).throw(error)):
            if any((Path(directory)/name).is_symlink() for name in dirs):
                raise ValueError('workspace source symlink')
            authored.update(str((Path(directory)/name).relative_to(work/'source')) for name in names)
    expected = {name for name in files if name.startswith(('app/lib/', 'app/test/', 'app/tool/'))}
    if authored != expected:
        raise ValueError('workspace authored inventory differs')
    config = work/'source/app/.dart_tool/package_config.json'
    for item in json.loads(regular(config))['packages']:
        uri = urlparse(item['rootUri'])
        if uri.scheme not in ('', 'file') or uri.netloc:
            raise ValueError('nonlocal package root')
        path = Path(unquote(uri.path))
        path = (path if path.is_absolute() else config.parent/path).resolve()
        roots = [work/'flutter', work/'pub-cache', work/'source/app',
                 work/'source/packages/flutter_openim_sdk']
        if not any(path == root or path.is_relative_to(root) for root in roots):
            raise ValueError('package outside fresh roots')
        if (item['name'] == 'imapp' and path != work/'source/app'
                or item['name'] == 'flutter_openim_sdk' and path != work/'source/packages/flutter_openim_sdk'):
            raise ValueError('wrong app/SDK package root')
    version = json.loads(regular(work/'evidence/flutter-version.json'))
    for key, wanted in [('frameworkVersion', base['toolchain']['tag']),
                        ('frameworkRevision', base['toolchain']['framework_revision']),
                        ('engineRevision', base['toolchain']['engine_revision']),
                        ('dartSdkVersion', base['toolchain']['dart_version'])]:
        if version.get(key) != wanted:
            raise ValueError('official toolchain identity differs')
    return files


def overlay(work):
    cfg = descriptor()
    source, check = legacy()
    canonical_work(work)
    check.verify(work, 'current351')  # Original setup context is genuinely351, never renamed354.
    _, files = sources(cfg, source)
    if any(p.suffix == '.dill' or p.name.endswith('.stamp') for p in (work/'source').rglob('*') if p.is_file()):
        raise ValueError('old compiled app output before overlay')
    receipt = work/'evidence/group-revoke-overlay.json'
    if receipt.exists():
        raise ValueError('overlay cannot be replayed')
    if os.statvfs(work).f_bavail * os.statvfs(work).f_frsize < check.INITIAL:
        raise ValueError('overlay initial4GiB gate')
    for row in cfg['candidate']['selected6']:
        if os.statvfs(work).f_bavail * os.statvfs(work).f_frsize < check.FLOOR:
            raise ValueError('overlay partial retained at resource floor')
        target = work/'source'/safe(row['path'])
        target.parent.mkdir(parents=True, exist_ok=True)
        if row['new']:
            with target.open('xb') as stream:
                stream.write(files[row['path']])
        else:
            pinned(regular(target), next(r for r in cfg['base']['before3'] if r['path'] == row['path']))
            backup = work/'evidence/group-revoke-before'/safe(row['path'])
            backup.parent.mkdir(parents=True, exist_ok=True)
            with backup.open('xb') as stream:
                stream.write(target.read_bytes())
            target.write_bytes(files[row['path']])
    verify_work(work, cfg, source)
    check.write_json(receipt, {'cohort': cfg['cohort'], 'base_setup_cohort': 'current351',
                              'source354': cfg['candidate']['source_digest'],
                              'portable406': cfg['candidate']['portable_digest'],
                              'files': source.rows(files), 'source_checkout_untouched': True,
                              'old_kernel_or_hook_stamp_copied': False})


def phase_commands(work):
    dart = str(work/'flutter/bin/cache/dart-sdk/bin/dart')
    flutter = str(work/'flutter/bin/flutter')
    return ([[dart, 'analyze', '--fatal-infos', path] for path in ANALYSIS],
            [flutter, 'test', '--no-pub', '--machine', '--concurrency=1',
             '--no-enable-vmservice', '--name', '^(?:' + '|'.join(map(re.escape, FIVE)) + ')$', TEST])


def cleanup_summary(text):
    starts, sequences = {}, {}
    for line in text.splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if not isinstance(event, dict):
            continue
        if event.get('type') == 'testStart':
            identity = event['test']['id']
            if identity in starts:
                raise ValueError('duplicate cleanup start')
            starts[identity] = event['test'].get('name')
        if event.get('type') == 'print' and isinstance(event.get('message'), str):
            message = event['message']
            if message.startswith('group_revoke_cleanup '):
                identity = event.get('testID')
                if identity not in starts or starts[identity] not in FIVE:
                    raise ValueError('foreign cleanup owner')
                sequences.setdefault(identity, []).append(message[len('group_revoke_cleanup '):])
    actual = []
    for identity, sequence in sequences.items():
        name = starts[identity]
        if sequence != MARKERS or name in actual:
            raise ValueError('missing/duplicate/out-of-order owned cleanup')
        actual.append(name)
    if Counter(actual) != Counter(FIVE):
        raise ValueError('missing selected cleanup')
    return {'owned_widget_fixtures': 5, 'events': 20, 'exact_four_marker_order': True}


def ui_warnings(text):
    result = []
    for line in text.splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if isinstance(event, dict) and event.get('type') == 'print':
            message = event.get('message', '')
            if re.search(r'RenderFlex.*overflow|was not hit-testable|Warning: A call to tester\.(?:tap|longPress)', message):
                result.append(message)
    return result


def run(work):
    cfg = descriptor()
    source, check = legacy()
    check.prove_namespace()
    verify_work(work, cfg, source)
    guard = check.Guard(work)
    if guard.sample() < check.INITIAL or guard.reason:
        raise ValueError('initial4GiB/cap admission')
    thread = threading.Thread(target=guard.poll, daemon=True)
    thread.start()
    counts = {}
    try:
        analyses, tests = phase_commands(work)
        for index, command in enumerate(analyses, 1):
            verify_work(work, cfg, source)
            check.guarded(command, work, guard, 'group-revoke-analyze-' + str(index),
                          timeout=cfg['resource']['phase_seconds'])
        verify_work(work, cfg, source)
        log = check.guarded(tests, work, guard, 'group-revoke-v3-target5',
                            timeout=cfg['resource']['phase_seconds'])
        text = log.read_text()
        counts['tests'] = check.machine_summary(text, 5)
        counts['exact_names'] = check.required_name_summary(text, 5, [{'name': n, 'count': 1} for n in FIVE])
        counts['cleanup'] = cleanup_summary(text)
        warnings = ui_warnings(text)
        if warnings:
            raise ValueError('UI warnings retained; target not accepted')
        verify_work(work, cfg, source)
        check.write_json(work/'evidence/group-revoke-v3.summary.json', counts)
        check.write_json(work/'evidence/group-revoke-v3-passed.json',
                         {'state': 'candidate_target5_analysis3_completed',
                          'cohort': cfg['cohort'], 'source354': cfg['candidate']['source_digest'],
                          'portable406': cfg['candidate']['portable_digest'], 'counts': counts,
                          'old55_local_greens_reused_not_rerun': True,
                          'App351_not_adopted': True, 'native_service_acceptance': False})
        guard.sample()
    except BaseException as error:
        check.write_json(work/'evidence/group-revoke-v3-failed.json',
                         {'state': 'failed_or_incomplete', 'type': type(error).__name__,
                          'detail': str(error), 'latched_failure': guard.reason})
        raise
    finally:
        try:
            check.write_json(work/'evidence/group-revoke-v3-final-resource.json',
                             {'minimum_free_bytes_before_receipt': guard.minimum,
                              'peak_workspace_allocated_bytes': guard.peak,
                              'latched_failure': guard.reason,
                              'failure_wins_over_passed_receipt': True})
            guard.sample()
        finally:
            guard.stop.set()
            thread.join()
    if guard.reason:
        raise ValueError('latched final resource failure: ' + guard.reason)
    print(json.dumps({'state': 'candidate5_PASS_analysis3_0', 'counts': counts}))


def run_environment(work, run_as):
    account = pwd.getpwnam(run_as)
    home = Path(account.pw_dir)
    if (account.pw_uid == 0 or not home.is_absolute() or home.resolve() != home
            or not stat.S_ISDIR(home.lstat().st_mode) or home.stat().st_uid != account.pw_uid):
        raise ValueError('real canonical account-owned system HOME required')
    return {'HOME': str(home), 'USER': run_as, 'LOGNAME': run_as,
           'PATH': '/usr/local/bin:/usr/bin:/bin', 'LANG': 'C.UTF-8', 'LC_ALL': 'C.UTF-8',
           'PUB_CACHE': str(work/'pub-cache'), 'TMPDIR': str(work/'tmp'),
           'TMP': str(work/'tmp'), 'TEMP': str(work/'tmp'),
           'XDG_CONFIG_HOME': str(work/'config'), 'XDG_CACHE_HOME': str(work/'config/cache'),
           'FLUTTER_ROOT': str(work/'flutter'), 'FLUTTER_SUPPRESS_ANALYTICS': 'true',
           'DART_SUPPRESS_ANALYTICS': 'true', 'CI': 'true', 'TERM': 'dumb'}


def netns(work, run_as):
    if os.geteuid() != 0 or not run_as or run_as == 'root':
        raise ValueError('root namespace launcher and nonroot target required')
    canonical_work(work)
    env = run_environment(work, run_as)
    subprocess.run(['ip', 'link', 'set', 'lo', 'up'], check=True)
    descriptor()
    _, check = legacy()
    check.prove_namespace()
    command = ['/usr/sbin/runuser', '-u', run_as, '--', 'env', '-i']
    command += [key + '=' + value for key, value in env.items()]
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
        source, _ = legacy()
        _, files = sources(cfg, source)
        print(json.dumps({'state': 'readonly_candidate_source', 'files': len(files),
                          'source354': cfg['candidate']['source_digest'],
                          'portable406': source.source_digest(files)}))
    elif args.work is None:
        parser.error('work required')
    elif args.mode == 'overlay':
        overlay(args.work)
    elif args.mode == 'netns':
        netns(args.work, args.run_as)
    elif args.mode == 'run':
        run(args.work)
    else:
        cfg = descriptor()
        source, _ = legacy()
        verify_work(args.work, cfg, source)


if __name__ == '__main__':
    main()
