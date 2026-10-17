#!/usr/bin/env python3
"""Manual current363/415 incremental25; local9analysis/gateway1 reused. Historical current359 is not an alias."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import threading
from urllib.parse import unquote, urlparse

REPO = Path(__file__).resolve().parent.parent
INPUTS = REPO/'ci/mobile-input/inputs.json'
NAMES = REPO/'ci/mobile-input/required-test-names.json'

def module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value

CHECK = module('input_inherited_check', REPO/'ci/mobile-check.py')
SOURCE = CHECK.source
ANALYSIS = module('input_inherited_analysis', REPO/'ci/mobile-announcement.py')
BASE_SOURCE = '633e257bcc0c161eb9ad109b61ca689acfcf5fc454472d02f38b942b69f0d199'
BASE_PORTABLE = '66f60a7c2f93618b0f2eff179e545694f5978cdbd19ae8cd223f86d1333fa8cb'
FINAL_SOURCE = 'a4915d9f88c8f16d694abe0e8fb2cba6a99075c6b869a0ed0527618d2389961e'
FINAL_PORTABLE = '47ed38574138262db0fea7b44c4a0f08c55e88659a5af78152c2c03d4ac51413'
LOCAL_REUSE_SHA = 'fb1e8615999c4acf4e6319c7c76cbbbba402c66f1d953394df31de970f5b2821'
RESOURCE = {'initial_bytes': 4294967296, 'floor_bytes': 1073741824,
            'allocated_cap_bytes': 805306368, 'poll_seconds': .2, 'phase_seconds': 120}

ANALYSIS_PATHS9 = ['lib/core/im/im_event.dart', 'lib/core/im/open_im_gateway.dart', 'lib/features/chat/data/chat_repository.dart', 'lib/features/chat/presentation/chat_page.dart', 'lib/features/chat/presentation/chat_typing_controller.dart', 'lib/features/chat/presentation/mention_text_controller.dart', 'test/features/chat/chat_input_fixture.dart', 'test/features/chat/chat_reedit_workflow_test.dart', 'test/features/chat/chat_typing_workflow_test.dart']
TEST_PATHS4 = ['test/features/chat/chat_reedit_workflow_test.dart', 'test/features/chat/chat_typing_workflow_test.dart', 'test/features/chat/group_revoke_workflow_test.dart', 'test/features/chat/chat_page_test.dart']
NEW21 = ['self revoke ACK restores text once through the real composer without sending', 'self at-text ACK restores exact mention identities instead of guessing from draft text', 'unknown revoke never offers re-edit even when the native side removed the original', 'an acknowledged moderated peer text never becomes the local sender draft', 'private self text can be revoked but cannot be retained as a re-edit draft', 'incomplete attached privacy metadata cannot be retained by successful self revoke', 'explicit re-edit preserves an existing draft until the user clears it', 'foreign route coverage permanently retires the old re-edit callback after return', 'history clear after ACK immediately blocks a retained re-edit action', 'new sticky private facts after ACK prevent the old re-edit callback from copying text', 'an explicit delete notice after self revoke cancels its retained re-edit action', 'same-State CID ABA during a dispatched revoke cannot restore its late acknowledged text', 'owner logout while revoke waits keeps the ACK from writing the retired composer', 'real focused composer sends bounded yes and throttles further input without sending a message', 'input idle and focus loss stop typing through no without replaying a yes', 'live exact-peer typing updates the real header and each yes renews only a short TTL', 'wrong-peer wrong-recipient malformed and offline typing never enters history or the active header', 'online-only typing uses the same transient yes-no path while a group composer sends none', 'live online-only no clears the header without an ordinary receive refresh', 'background and foreign coverage discard typing state until a new foreground event arrives', 'real back navigation cancels outgoing timers and a fresh chat does not resurrect old typing']
OLD5 = ['actual self message menu keeps ordinary single chat revoke usable', 'actual long press group moderation inspects roles then confirms one standard revoke', 'real doubletap dispatches once and native unknown offers only read review even after reopen', 'chat restores a draft and sends through the real method codec without overflow at large text', 'repeated exit is idempotent and old listener generations cannot enter a new account']
NAMES26 = NEW21 + OLD5
NAMES25 = NAMES26[:-1]
HELPERS = ['ci/mobile-source.py', 'ci/mobile-check.py', 'ci/mobile-announcement.py', 'ci/mobile/inputs.json', 'ci/mobile/required-test-names.json', 'ci/mobile-setup.sh', 'ci/mobile-input-setup.sh', '.github/workflows/mobile-input.yml', '.github/workflows/mobile-ci.yml', 'ci/mobile-input.py']


def pin(body, row):
    if len(body) != row['bytes'] or SOURCE.sha(body) != row['sha256']:
        raise ValueError('current363 source pin differs: '+row['path'])
    return body


def inventory_digest(rows):
    return SOURCE.sha(json.dumps(sorted(rows, key=lambda r: r['path']),
                                ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode())


def validate_inventory(cfg, actual_rows, old_plan):
    """Actual current bodies first; reverse explicit metadata, never a cohort alias."""
    if actual_rows != cfg['source_rows'] or len(actual_rows) != 363 or inventory_digest(actual_rows) != FINAL_SOURCE:
        raise ValueError('current363 source inventory differs')
    previous = {r['path']: r for r in actual_rows}
    selected = cfg['delta9']
    if len(selected) != 9 or len({r['path'] for r in selected}) != 9:
        raise ValueError('exact delta9 required')
    replaced = added = 0
    for row in selected:
        if previous.get(row['path']) != row['after']:
            raise ValueError('delta after identity differs')
        if row['before'] is None:
            del previous[row['path']]
            added += 1
        else:
            if row['before']['path'] != row['path']:
                raise ValueError('delta before target differs')
            previous[row['path']] = row['before']
            replaced += 1
    if (replaced, added) != (5, 4) or list(sorted(previous.values(), key=lambda r: r['path'])) != old_plan['current359']['files']:
        raise ValueError('historical359 reverse5/remove4 differs')
    if inventory_digest(list(previous.values())) != BASE_SOURCE:
        raise ValueError('historical359 digest differs')


def validate_local_reuse(cfg):
    # The reviewed local artifacts are immutable provenance. No cloud-stage
    # download/read of QA paths, and no fabricated fresh analysis/gateway pass.
    proof = cfg['local_validation_reuse']
    canonical = json.dumps(proof, ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode()
    if SOURCE.sha(canonical) != LOCAL_REUSE_SHA:
        raise ValueError('local analysis9/gateway1 proof differs')
    rows = {r['path']: r for r in cfg['source_rows']}
    if (proof['source363'] != FINAL_SOURCE or proof['portable415'] != FINAL_PORTABLE
            or [r['path'] for r in proof['analyses9']] != ANALYSIS_PATHS9
            or len(proof['analyses9']) != 9
            or proof['gateway1']['name'] != OLD5[-1]
            or proof['gateway1']['source'] != rows['app/test/core/im/open_im_gateway_test.dart']
            or proof['cloudReexecution'] != {'analysis': 0, 'gateway1': False, 'test25': True}
            or proof['originalInvocation']['doneSuccess'] is not False
            or proof['originalInvocation']['unstartedBusiness'] != 25):
        raise ValueError('local reuse scope/source differs')
    for entry in proof['analyses9']:
        if (entry['source'] != rows['app/'+entry['path']]
                or entry['result'] != {'path': entry['path'], 'exitCode': 0, 'noIssues': True}):
            raise ValueError('local analysis body/result differs')


def descriptor():
    cfg = json.loads(SOURCE.regular(INPUTS))
    if (cfg['schema'] != 'luobo.mobile.input.current363.scoped25.reuse9plus1.v2'
            or cfg['cohort'] != 'current363' or cfg['resource'] != RESOURCE
            or cfg['source_digest'] != FINAL_SOURCE or cfg['portable_digest'] != FINAL_PORTABLE
            or cfg['source_members'] != 363 or cfg['portable_members'] != 415
            or cfg['analysis_paths'] != ANALYSIS_PATHS9 or cfg['test_paths'] != TEST_PATHS4
            or cfg['new_names21'] != NEW21 or cfg['old_names5'] != OLD5
            or cfg['names26'] != NAMES26
            or cfg['names25'] != NAMES25 or cfg['reused_names1'] != [OLD5[-1]]
            or cfg['cloud_analysis_paths'] != []
            or len(cfg['names25']) != 25 or len(set(cfg['names25'])) != 25
            or [r['path'] for r in cfg['helper_pins']] != HELPERS
            or len(cfg['names26']) != 26 or len(set(cfg['names26'])) != 26
            or len(cfg['new_names21']) != 21 or len(cfg['old_names5']) != 5
            or cfg['names26'] != cfg['new_names21'] + cfg['old_names5']
            or cfg['cleanup']['widget_names'] != cfg['new_names21'] + cfg['old_names5'][:3]
            or cfg['cleanup']['repository_names'] != []
            or cfg['cleanup']['widget_markers'] != ['sdk.begin', 'sdk.end', 'unmount.begin', 'unmount.end']
            or cfg['cleanup']['repository_markers'] != ['sdk.begin', 'sdk.end', 'storage.begin', 'storage.end']):
        raise ValueError('current363 scope/resource identity differs')
    for row in cfg['helper_pins']:
        pin(SOURCE.regular(REPO/SOURCE.safe_name(row['path'])), row)
    old = SOURCE.descriptor()
    if old['current359']['source_digest'] != BASE_SOURCE or old['portable_digests']['current359'] != BASE_PORTABLE:
        raise ValueError('historical current359 descriptor differs')
    names = json.loads(pin(SOURCE.regular(NAMES), cfg['names_file']))
    old_names = SOURCE.required_names(old)
    new_rows = sorted([{'name': n, 'count': 1} for n in cfg['new_names21']], key=lambda r: r['name'])
    if (names['historical_full1308'] != old_names['current_full1308']
            or names['new21'] != new_rows
            or names['expected_full1329'] != sorted(names['historical_full1308'] + new_rows, key=lambda r: r['name'])
            or len({r['name'] for r in names['expected_full1329']}) != 1329
            or names['executed_full1329'] is not False):
        raise ValueError('historical1308/new21 expected names differ')
    validate_inventory(cfg, cfg['source_rows'], old)
    validate_local_reuse(cfg)
    return cfg, old


def sources(cfg, old):
    files = {r['path']: pin(SOURCE.regular(REPO/SOURCE.safe_name(r['path'])), r)
             for r in cfg['source_rows']}
    actual = set()
    for prefix in ('app/lib', 'app/test', 'app/tool'):
        for directory, dirs, names in os.walk(REPO/prefix, followlinks=False):
            if any((Path(directory)/n).is_symlink() for n in dirs):
                raise ValueError('current363 source directory symlink')
            for name in names:
                path = Path(directory)/name
                SOURCE.regular(path)
                actual.add(str(path.relative_to(REPO)))
    wanted = {p for p in files if p.startswith(('app/lib/', 'app/test/', 'app/tool/'))}
    if actual != wanted:
        raise ValueError('current363 authored inventory differs; no unknown exceptions')
    validate_inventory(cfg, SOURCE.rows(files), old)
    SOURCE.webview_dependency_contract(old, files['app/pubspec.lock'])
    # Native snapshots retain their old independent identity, never363 approval.
    for row in SOURCE.native_source_contract(old):
        pin(SOURCE.regular(REPO/SOURCE.safe_name(row['path'])), row)
    for row in [*old['sdk_portable']['files'], old['migration']]:
        if row['path'] in files:
            raise ValueError('portable closure overlap')
        files[row['path']] = pin(SOURCE.regular(REPO/SOURCE.safe_name(row['path'])), row)
    if len(files) != 415 or SOURCE.source_digest(files) != FINAL_PORTABLE:
        raise ValueError('current363 portable415 differs')
    if cfg['analysis_paths'] != [r['path'][4:] for r in cfg['delta9']]:
        raise ValueError('changed analysis nine differs')
    for row in cfg['old_test_source_pins']:
        pin(files[row['path']], row)
    return files


def canonical_work(work):
    ANALYSIS.canonical_work(work)


def verify_work(work, cfg, old):
    current, parent = descriptor()
    if current != cfg or parent != old:
        raise ValueError('current363 controller inputs changed')
    canonical_work(work)
    files = sources(cfg, old)
    for name, body in files.items():
        if SOURCE.regular(work/'source'/SOURCE.safe_name(name)) != body:
            raise ValueError('current363 workspace changed: '+name)
    actual = set()
    for prefix in ('app/lib', 'app/test', 'app/tool'):
        for directory, dirs, names in os.walk(work/'source'/prefix, followlinks=False):
            if any((Path(directory)/n).is_symlink() for n in dirs):
                raise ValueError('current363 workspace directory symlink')
            actual.update(str((Path(directory)/n).relative_to(work/'source')) for n in names)
    if actual != {p for p in files if p.startswith(('app/lib/', 'app/test/', 'app/tool/'))}:
        raise ValueError('current363 workspace inventory differs')
    config = work/'source/app/.dart_tool/package_config.json'
    for item in json.loads(SOURCE.regular(config))['packages']:
        uri = urlparse(item['rootUri'])
        if uri.scheme not in ('', 'file') or uri.netloc:
            raise ValueError('nonlocal package root')
        root = Path(unquote(uri.path))
        root = (root if root.is_absolute() else config.parent/root).resolve()
        allowed = [work/'flutter', work/'pub-cache', work/'source/app', work/'source/packages/flutter_openim_sdk']
        if not any(root == p or root.is_relative_to(p) for p in allowed):
            raise ValueError('package outside fresh owned roots')
        if (item['name'] == 'imapp' and root != work/'source/app'
                or item['name'] == 'flutter_openim_sdk' and root != work/'source/packages/flutter_openim_sdk'):
            raise ValueError('wrong actual app/SDK package root')
    version = json.loads(SOURCE.regular(work/'evidence/flutter-version.json'))
    for key, expected in [('frameworkVersion', old['toolchain']['tag']),
                          ('frameworkRevision', old['toolchain']['framework_revision']),
                          ('engineRevision', old['toolchain']['engine_revision']),
                          ('dartSdkVersion', old['toolchain']['dart_version'])]:
        if version.get(key) != expected:
            raise ValueError('official toolchain differs')
    return files


def materialize(output):
    if (not output.is_absolute() or output.resolve() != output or output.exists()
            or output.is_symlink() or output.is_relative_to(REPO) or not output.parent.is_dir()):
        raise ValueError('fresh absolute source root outside checkout required')
    if os.statvfs(output.parent).f_bavail * os.statvfs(output.parent).f_frsize < CHECK.INITIAL:
        raise ValueError('initial4GiB gate')
    cfg, old = descriptor()
    files = sources(cfg, old)
    output.mkdir(mode=0o700)
    for path, body in files.items():
        if os.statvfs(output).f_bavail * os.statvfs(output).f_frsize < CHECK.FLOOR:
            raise ValueError('partial copy stopped at1GiB floor')
        target = output/SOURCE.safe_name(path)
        target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        with target.open('xb') as stream:
            stream.write(body)
    return {'cohort': 'current363', 'source363': FINAL_SOURCE, 'portable415': FINAL_PORTABLE,
            'rows': SOURCE.rows(files), 'old_generated_outputs_copied': 0, 'native_acceptance': False}


def phase_commands(work, cfg):
    # These nine final files were actually analyzed locally. The one gateway
    # test passed before four other files failed to load unavailable packages.
    # Only the 25 unstarted business tests execute on the locked cloud closure.
    validate_local_reuse(cfg)
    tests = [str(work/'flutter/bin/flutter'), 'test', '--no-pub', '--machine', '--concurrency=1',
             '--no-enable-vmservice', '--name', '^(?:'+'|'.join(map(re.escape, cfg['names25']))+')$'] + cfg['test_paths']
    return [], tests


def result_summary(text, cfg):
    result = {'tests': CHECK.machine_summary(text, 25),
              'names': CHECK.required_name_summary(text, 25, [{'name': n, 'count': 1} for n in cfg['names25']]),
              'cleanup': CHECK.group_revoke_cleanup_summary(text, cfg['cleanup'])}
    if ANALYSIS.ui_warnings(text):
        raise ValueError('tap/overflow warning in incremental25')
    return result


def run(work):
    cfg, old = descriptor()
    verify_work(work, cfg, old)
    CHECK.prove_namespace()
    guard = CHECK.Guard(work)
    if guard.sample() < CHECK.INITIAL or guard.reason:
        raise ValueError('initial4GiB/cap gate')
    monitor = threading.Thread(target=guard.poll, daemon=True)
    monitor.start()
    try:
        commands, test_command = phase_commands(work, cfg)
        if commands:
            raise ValueError('local analysis9 must not be executed again')
        CHECK.write_json(work/'evidence/input-local-reuse9-plus1.json', cfg['local_validation_reuse'])
        log = CHECK.guarded(test_command, work, guard, 'input-target25', timeout=120)
        summary = result_summary(log.read_text(), cfg)
        verify_work(work, cfg, old)
        CHECK.write_json(work/'evidence/input-scoped-passed.json',
                         {'state': 'current363_cloud25_with_pinned_local_analysis9_gateway1', 'summary': summary,
                          'cloud_analysis_executed': 0, 'cloud_tests_executed': 25,
                          'local_reuse_sha256': LOCAL_REUSE_SHA,
                          'source363': FINAL_SOURCE, 'portable415': FINAL_PORTABLE,
                          'full1329_executed': False, 'native_device_service_acceptance': False})
        guard.sample()
    except BaseException as error:
        CHECK.write_json(work/'evidence/input-failed.json',
                         {'type': type(error).__name__, 'detail': str(error), 'latched_failure': guard.reason})
        raise
    finally:
        try:
            guard.sample()
            CHECK.write_json(work/'evidence/input-final-resource.json',
                             {'minimum_free_bytes': guard.minimum, 'peak_workspace_allocated_bytes': guard.peak,
                              'latched_failure': guard.reason, 'failure_wins_over_passed_receipt': True})
        finally:
            guard.stop.set()
            monitor.join()
    if guard.reason:
        raise ValueError('final resource failure')


def netns(work, run_as):
    if os.geteuid() != 0 or not run_as or run_as == 'root':
        raise ValueError('root netns launcher and nonroot tester required')
    canonical_work(work)
    descriptor()
    home = CHECK.run_user_home(run_as)
    env = {'HOME': home, 'USER': run_as, 'LOGNAME': run_as,
           'PATH': '/usr/local/bin:/usr/bin:/bin', 'LANG': 'C.UTF-8', 'LC_ALL': 'C.UTF-8',
           'PUB_CACHE': str(work/'pub-cache'), 'TMPDIR': str(work/'tmp'),
           'TMP': str(work/'tmp'), 'TEMP': str(work/'tmp'), 'XDG_CONFIG_HOME': str(work/'config'),
           'XDG_CACHE_HOME': str(work/'config/cache'), 'FLUTTER_ROOT': str(work/'flutter'),
           'FLUTTER_SUPPRESS_ANALYTICS': 'true', 'DART_SUPPRESS_ANALYTICS': 'true', 'CI': 'true', 'TERM': 'dumb'}
    subprocess.run(['ip', 'link', 'set', 'lo', 'up'], check=True)
    CHECK.prove_namespace()
    command = ['/usr/sbin/runuser', '-u', run_as, '--', 'env', '-i']
    command += [k+'='+v for k, v in env.items()]
    command += ['python3', str(Path(__file__).resolve()), 'run', '--work', str(work)]
    os.execv(command[0], command)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=('verify', 'materialize', 'netns', 'run'))
    parser.add_argument('--work', type=Path)
    parser.add_argument('--output', type=Path)
    parser.add_argument('--run-as')
    args = parser.parse_args()
    if args.mode == 'materialize':
        if args.output is None:
            parser.error('output required')
        print(json.dumps(materialize(args.output)))
    elif args.mode == 'verify':
        cfg, old = descriptor()
        files = verify_work(args.work, cfg, old) if args.work else sources(cfg, old)
        print(json.dumps({'cohort': 'current363', 'source363': FINAL_SOURCE,
                          'portable415': SOURCE.source_digest(files), 'full1329_executed': False}))
    elif args.work is None:
        parser.error('work required')
    elif args.mode == 'netns':
        netns(args.work, args.run_as)
    else:
        run(args.work)


if __name__ == '__main__':
    main()
