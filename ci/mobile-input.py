#!/usr/bin/env python3
"""Manual current363/415 only isolated negative1; source-bound prior25 and gateway1 reused."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
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
FINAL_SOURCE = 'ccec31d13c6e64bb4d0c9ff0b1b85baae3d50d198c743d34e9935a053d17ecd8'
FINAL_PORTABLE = '30879a35431337689eced3a9b5ce9a7c528342740be0be502053100b7be24d7d'
LOCAL_REUSE_SHA = '06d10baf4cec974f32af7eae7be228c6ffc0023422fbc80ae824f1dea3b71605'
ORIGINAL_CLOUD15_SHA = 'b2438f78ab68b2e34c111cc8052e85e1563c3fcc0c136a397acb80bcc87387ba'
CLOUD_REUSE_SHA = '8fe4cd26676632f2dc83032daac6d136b01e33b2d332e6bac5cd8257890c0e28'
VM_DIAGNOSTIC_SHA = '2e36b5d515473426644f7f8cf11d2a0c7465501b3e2e3ded39148f9f86a8a6e8'
SOURCE_POST_SECONDS = 30
RESOURCE = {'initial_bytes': 4294967296, 'floor_bytes': 1073741824,
            'allocated_cap_bytes': 805306368, 'poll_seconds': .2, 'phase_seconds': 120}

ANALYSIS_PATHS9 = ['lib/core/im/im_event.dart', 'lib/core/im/open_im_gateway.dart', 'lib/features/chat/data/chat_repository.dart', 'lib/features/chat/presentation/chat_page.dart', 'lib/features/chat/presentation/chat_typing_controller.dart', 'lib/features/chat/presentation/mention_text_controller.dart', 'test/features/chat/chat_input_fixture.dart', 'test/features/chat/chat_reedit_workflow_test.dart', 'test/features/chat/chat_typing_workflow_test.dart']
TEST_PATHS4 = ['test/features/chat/chat_reedit_workflow_test.dart', 'test/features/chat/chat_typing_workflow_test.dart', 'test/features/chat/group_revoke_workflow_test.dart', 'test/features/chat/chat_page_test.dart']
NEW21 = ['self revoke ACK restores text once through the real composer without sending', 'self at-text ACK restores exact mention identities instead of guessing from draft text', 'unknown revoke never offers re-edit even when the native side removed the original', 'an acknowledged moderated peer text never becomes the local sender draft', 'private self text can be revoked but cannot be retained as a re-edit draft', 'incomplete attached privacy metadata cannot be retained by successful self revoke', 'explicit re-edit preserves an existing draft until the user clears it', 'foreign route coverage permanently retires the old re-edit callback after return', 'history clear after ACK immediately blocks a retained re-edit action', 'new sticky private facts after ACK prevent the old re-edit callback from copying text', 'an explicit delete notice after self revoke cancels its retained re-edit action', 'same-State CID ABA during a dispatched revoke cannot restore its late acknowledged text', 'owner logout while revoke waits keeps the ACK from writing the retired composer', 'real focused composer sends bounded yes and throttles further input without sending a message', 'input idle and focus loss stop typing through no without replaying a yes', 'live exact-peer typing updates the real header and each yes renews only a short TTL', 'wrong-peer wrong-recipient malformed and offline typing never enters history or the active header', 'online-only typing uses the same transient yes-no path while a group composer sends none', 'live online-only no clears the header without an ordinary receive refresh', 'background and foreign coverage discard typing state until a new foreground event arrives', 'real back navigation cancels outgoing timers and a fresh chat does not resurrect old typing']
OLD5 = ['actual self message menu keeps ordinary single chat revoke usable', 'actual long press group moderation inspects roles then confirms one standard revoke', 'real doubletap dispatches once and native unknown offers only read review even after reopen', 'chat restores a draft and sends through the real method codec without overflow at large text', 'repeated exit is idempotent and old listener generations cannot enter a new account']
NAMES26 = NEW21 + OLD5
NAMES25 = NAMES26[:-1]
ANALYSIS_PATHS10 = ANALYSIS_PATHS9[:6] + ['test/core/im/sdk_harness.dart'] + ANALYSIS_PATHS9[6:]
CHANGED_ANALYSIS1 = ['test/features/chat/chat_typing_workflow_test.dart']
ANALYSIS_PATHS13 = ['lib/core/im/im_event.dart', 'lib/core/im/open_im_gateway.dart', 'lib/features/chat/data/chat_repository.dart', 'lib/features/chat/presentation/chat_page.dart', 'lib/features/chat/presentation/chat_typing_controller.dart', 'lib/features/chat/presentation/mention_text_controller.dart', 'test/core/im/sdk_harness.dart', 'test/features/chat/chat_input_fixture.dart', 'test/features/chat/chat_location_fixture.dart', 'test/features/chat/chat_reedit_workflow_test.dart', 'test/features/chat/chat_typing_workflow_test.dart', 'test/features/chat/group_revoke_fixture.dart', 'test/features/chat/group_revoke_workflow_test.dart']
REUSED_ANALYSIS12 = [p for p in ANALYSIS_PATHS13 if p not in CHANGED_ANALYSIS1]
ORIGINAL_PHASE9 = [OLD5[0], OLD5[1], OLD5[2], OLD5[3], NEW21[1], *NEW21[17:]]
PHASE1 = [NEW21[16]]
REUSED15 = [name for name in NAMES25 if name not in ORIGINAL_PHASE9 + PHASE1]
RETRY = 'actual role read failure stays an error and explicit retry remains read only until confirmation'
FOCUSED = NEW21[13]
PHASE3 = [OLD5[1], RETRY, FOCUSED]
PHASE3_PATHS = ['test/features/chat/group_revoke_workflow_test.dart', 'test/features/chat/chat_typing_workflow_test.dart']
UNIVERSE27 = NAMES26 + [RETRY]
REUSED22 = [n for n in NAMES25 if n not in [OLD5[1], PHASE1[0], FOCUSED]]
REUSED_WIDGET21 = [n for n in REUSED22 if n != OLD5[3]]
REUSED25 = [n for n in UNIVERSE27 if n not in PHASE1+[OLD5[-1]]]
REUSED_WIDGET24 = [n for n in REUSED25 if n != OLD5[3]]
PHASE1_PATHS = ['test/features/chat/chat_typing_workflow_test.dart']
HELPERS = ['ci/mobile-source.py', 'ci/mobile-check.py', 'ci/mobile-announcement.py', 'ci/mobile/inputs.json', 'ci/mobile/required-test-names.json', 'ci/mobile-setup.sh', 'ci/mobile-input-setup.sh', '.github/workflows/mobile-input.yml', '.github/workflows/mobile-ci.yml', 'ci/mobile-input.py', 'ci/mobile-input-vm.py', '.github/workflows/mobile-input-vm-diagnostic.yml']


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
    selected = cfg['delta13']
    if len(selected) != 13 or len({r['path'] for r in selected}) != 13:
        raise ValueError('exact delta13 required')
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
    if (replaced, added) != (9, 4) or list(sorted(previous.values(), key=lambda r: r['path'])) != old_plan['current359']['files']:
        raise ValueError('historical359 reverse9/remove4 differs')
    if inventory_digest(list(previous.values())) != BASE_SOURCE:
        raise ValueError('historical359 digest differs')


def validate_local_reuse(cfg):
    # Twelve exact previous analyses plus Root's current one, not new cloud
    # analysis. Old15's missing source-post is never backfilled by later runs.
    proof = cfg['local_validation_reuse']
    canonical = json.dumps(proof, ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode()
    if SOURCE.sha(canonical) != LOCAL_REUSE_SHA:
        raise ValueError('local analysis13/gateway1 proof differs')
    rows = {r['path']: r for r in cfg['source_rows']}
    if (proof['source363'] != FINAL_SOURCE or proof['portable415'] != FINAL_PORTABLE
            or proof['all415AuthoredSourcesPostExact'] is not True
            or proof['cloud15SourcePostExactClaimed'] is not False
            or [r['path'] for r in proof['analyses13']] != ANALYSIS_PATHS13
            or proof['reused_analysis_paths12'] != REUSED_ANALYSIS12
            or proof['new_analysis_paths1'] != CHANGED_ANALYSIS1
            or len(proof['analyses13']) != 13
            or proof['gateway1']['name'] != OLD5[-1]
            or proof['gateway1']['source'] != rows['app/test/core/im/open_im_gateway_test.dart']
            or proof['cloudReexecution'] != {'analysis': 0, 'gateway1': False, 'tests': 1}):
        raise ValueError('local analysis union scope/source differs')
    for entry in proof['analyses13']:
        if (entry['source'] != rows['app/'+entry['path']]
                or entry['result'] != {'path': entry['path'], 'exitCode': 0, 'noIssues': True}):
            raise ValueError('local analysis body/result differs')

def validate_original_cloud15_reuse(cfg):
    proof = cfg['cloud_validation_reuse']
    canonical = json.dumps(proof, ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode()
    if SOURCE.sha(canonical) != ORIGINAL_CLOUD15_SHA:
        raise ValueError('prior cloud15 proof differs')
    if (proof['run'] != 39034473775 or proof['job'] != 117401417624
            or proof['head'] != '6e1449c6027d4d768999a85afd9e9d027697ba22'
            or proof['attempt'] != 1 or proof['actual_passed_names15'] != REUSED15
            or proof['prior_source_post_exact_recorded'] is not False
            or proof['prior_phase_latched_failure'] != 'phase_timeout'
            or proof['new_execution_claimed'] is not False
            or proof['accepted_cleanup_fixtures'] != 15
            or proof['accepted_cleanup_markers'] != 60
            or len(proof['actual_cleanup15']) != 15
            or proof['source_digest_at_initial_gate'] != 'a4915d9f88c8f16d694abe0e8fb2cba6a99075c6b869a0ed0527618d2389961e'
            or proof['portable_digest_at_initial_gate'] != '47ed38574138262db0fea7b44c4a0f08c55e88659a5af78152c2c03d4ac51413'):
        raise ValueError('prior cloud15 scope/failed source-post boundary differs')
    if [r['name'] for r in proof['actual_cleanup15']] != REUSED15:
        raise ValueError('prior cloud15 cleanup names differ')
    for row in proof['actual_cleanup15']:
        if row['markers'] != cfg['cleanup']['widget_markers']:
            raise ValueError('prior cloud15 cleanup sequence differs')


def validate_cloud22_reuse(cfg):
    proof = cfg['cloud_validation_reuse']['prior_cloud22']
    canonical = json.dumps(proof, ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode()
    if SOURCE.sha(canonical) != '5a78a8ae9e9640577f8f1ea6b149dd6847d7c91e7390abe6ae3ba46f2d206d5e':
        raise ValueError('prior cloud22 proof differs')
    validate_original_cloud15_reuse({'cloud_validation_reuse': proof['original_cloud15'], 'cleanup': cfg['cleanup']})
    r3 = proof['continuation_cloud8']
    if (r3['run'] != 39036060511 or r3['job'] != 117405946599 or r3['attempt'] != 1
            or r3['head'] != '992b7447bb1177e44d3407c11853f3e6f5ab578d'
            or r3['actual_passed_names8'] != [n for n in REUSED22 if n not in REUSED15]
            or r3['accepted_cleanup_fixtures'] != 7 or r3['accepted_cleanup_markers'] != 28
            or [r['name'] for r in r3['actual_cleanup7']] != [n for n in REUSED_WIDGET21 if n not in REUSED15]
            or r3['passed_draft_names1'] != [OLD5[3]]
            or r3['source363_at_gate_and_post'] != '2820dddc3f7213871c9d14a566eb4ca9f267f8010f6d5bd2d9443e0855bac393'
            or r3['portable415_at_gate_and_post'] != '02b8360346ae1f7bb9cfd2af03dc85d873681507e7a043e3cf60b3961b1f6979'
            or r3['both_phase_source_post_exact'] is not True
            or r3['failed_group_complete_cleanup_counted_as_pass'] is not False
            or proof['original_cloud15_reused_names14'] != [n for n in REUSED15 if n != FOCUSED]
            or [r['name'] for r in proof['original_cloud15_reused_cleanup14']] != [n for n in REUSED15 if n != FOCUSED]
            or proof['previous_pass_excluded_for_direct_recheck'] != [FOCUSED]
            or proof['latest_R4_retry_pass']['passed_names'] != [RETRY]
            or proof['latest_R4_retry_pass']['reused_for_current_scope'] is not False
            or proof['latest_R4_retry_pass']['retained_as_history_only'] is not True
            or proof['actual_passed_names22'] != REUSED22
            or [r['name'] for r in proof['actual_cleanup21']] != REUSED_WIDGET21
            or proof['accepted_cleanup_fixtures'] != 21 or proof['accepted_cleanup_markers'] != 84
            or proof['prior_source_post_exact_recorded'] is not False
            or proof['new_execution_claimed'] is not False):
        raise ValueError('prior cloud22 scope/source/cleanup lineage differs')
    for row in [*r3['actual_cleanup7'], *proof['actual_cleanup21']]:
        if row['markers'] != cfg['cleanup']['widget_markers']:
            raise ValueError('prior cloud22 cleanup sequence differs')


def validate_cloud_reuse(cfg):
    proof = cfg['cloud_validation_reuse']
    canonical = json.dumps(proof, ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode()
    if SOURCE.sha(canonical) != CLOUD_REUSE_SHA:
        raise ValueError('prior cloud25 proof differs')
    validate_cloud22_reuse(cfg)
    r5 = proof['continuation_cloud3']
    if (r5['run'] != 39038340794 or r5['job'] != 117412592904 or r5['attempt'] != 1
            or r5['head'] != '52e4a2f9cd1c4530f455d5e5adad17cdafdab9ae'
            or r5['actual_passed_names3'] != PHASE3
            or [r['name'] for r in r5['actual_cleanup3']] != PHASE3
            or r5['accepted_cleanup_fixtures'] != 3 or r5['accepted_cleanup_markers'] != 12
            or r5['source363_at_gate_and_post'] != '0df8e3655a8fa697d4cc0a3c86bd4b194def70d3e37cd4199d8afcc68a72da30'
            or r5['portable415_at_gate_and_post'] != '87f68117111647c8cff1c66b8d9eb30fdf41d0bf617849d31c6dcfeb65a2d93b'
            or r5['both_phase_source_post_exact'] is not True
            or r5['negative_phase_timeout_not_accepted'] is not True
            or r5['new_execution_claimed'] is not False
            or proof['actual_passed_names25'] != REUSED25
            or [r['name'] for r in proof['actual_cleanup24']] != REUSED_WIDGET24
            or proof['accepted_cleanup_fixtures'] != 24 or proof['accepted_cleanup_markers'] != 96
            or proof['prior_source_post_exact_recorded'] is not False
            or proof['new_execution_claimed'] is not False):
        raise ValueError('prior cloud25 scope/source/cleanup lineage differs')
    for row in [*r5['actual_cleanup3'], *proof['actual_cleanup24']]:
        if row['markers'] != cfg['cleanup']['widget_markers']:
            raise ValueError('prior cloud25 cleanup sequence differs')


def descriptor():
    cfg = json.loads(SOURCE.regular(INPUTS))
    if SOURCE.sha(json.dumps(cfg['vm_diagnostic'],ensure_ascii=False,sort_keys=True,separators=(',',':')).encode()) != VM_DIAGNOSTIC_SHA:
        raise ValueError('bounded VM diagnostic contract differs')
    if (cfg['schema'] != 'luobo.mobile.input.current363.diagnostic1.reuse25.v6'
            or cfg['cohort'] != 'current363' or cfg['resource'] != RESOURCE
            or cfg['source_digest'] != FINAL_SOURCE or cfg['portable_digest'] != FINAL_PORTABLE
            or cfg['source_members'] != 363 or cfg['portable_members'] != 415
            or cfg['analysis_paths'] != ANALYSIS_PATHS13 or cfg['test_paths'] != TEST_PATHS4
            or cfg['new_names21'] != NEW21 or cfg['old_names5'] != OLD5
            or cfg['names26'] != NAMES26
            or cfg['universe_names27'] != UNIVERSE27
            or len(set(cfg['universe_names27'])) != 27
            or cfg['names25'] != NAMES25 or cfg['reused_names1'] != [OLD5[-1]]
            or cfg['phase3_names'] != [] or cfg['phase1_names'] != PHASE1
            or cfg['reused_cloud_names25'] != REUSED25
            or cfg['phase3_paths'] != [] or cfg['phase1_paths'] != PHASE1_PATHS
            or cfg['cloud_analysis_paths'] != []
            or len(cfg['names25']) != 25 or len(set(cfg['names25'])) != 25
            or [r['path'] for r in cfg['helper_pins']] != HELPERS
            or len(cfg['names26']) != 26 or len(set(cfg['names26'])) != 26
            or len(cfg['new_names21']) != 21 or len(cfg['old_names5']) != 5
            or cfg['names26'] != cfg['new_names21'] + cfg['old_names5']
            or cfg['cleanup']['widget_names'] != cfg['new_names21'] + cfg['old_names5'][:3] + [RETRY]
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
    validate_cloud_reuse(cfg)
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
    if cfg['analysis_paths'] != [r['path'][4:] for r in cfg['delta13']]:
        raise ValueError('changed analysis thirteen differs')
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


def phase_commands(work, cfg, vm_diagnostic=False):
    validate_local_reuse(cfg)
    validate_cloud_reuse(cfg)
    phases = []
    for label, names, paths in [('input-typing-diagnostic1', PHASE1, PHASE1_PATHS)]:
        command = [str(work/'flutter/bin/flutter'), 'test', '--no-pub', '--machine', '--concurrency=1',
                   '--no-enable-vmservice', '--name', '^(?:'+'|'.join(map(re.escape, names))+')$'] + paths
        if vm_diagnostic:
            label = 'input-typing-vm-diagnostic1'
            command = [sys.executable, str(REPO/'ci/mobile-input-vm.py'), '--work', str(work)]
        phases.append({'label': label, 'names': names, 'expected': len(names),
                       'paths': paths, 'command': command})
    return phases


def phase_cleanup(cfg, phase):
    cleanup = dict(cfg['cleanup'])
    cleanup['widget_names'] = [n for n in cleanup['widget_names'] if n in phase['names']]
    cleanup['repository_names'] = [n for n in cleanup['repository_names'] if n in phase['names']]
    return cleanup


def result_summary(text, cfg, phase):
    expected = phase['expected']
    result = {'tests': CHECK.machine_summary(text, expected),
              'names': CHECK.required_name_summary(text, expected, [{'name': n, 'count': 1} for n in phase['names']]),
              'cleanup': CHECK.group_revoke_cleanup_summary(text, phase_cleanup(cfg, phase))}
    if ANALYSIS.ui_warnings(text):
        raise ValueError('tap/overflow warning in targeted continuation')
    return result


def failure_observation(text, cfg, phase):
    # Only a complete selected-test assertion failure may proceed to the final
    # diagnostic. Loader failures, unknown IDs, missing cleanup, warnings,
    # skips and guard failures are not an excuse to continue.
    starts, finished, errors, footers, assertions = {}, {}, [], [], set()
    malformed = False
    assertion_headers = ('The following TestFailure was thrown',
                         'The following AssertionError was thrown',
                         'The following _AssertionError was thrown')
    for line in text.splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if not isinstance(event, dict):
            continue
        kind = event.get('type')
        if footers and kind is not None:
            malformed = True
        if kind == 'testStart':
            item = event.get('test', {})
            if not isinstance(item, dict) or type(item.get('id')) is not int:
                malformed = True
                continue
            identity = item.get('id')
            if identity in starts:
                malformed = True
            starts[identity] = item.get('name')
        elif kind == 'testDone':
            identity = event.get('testID')
            if type(identity) is not int:
                malformed = True
                continue
            if identity not in starts or identity in finished:
                malformed = True
            finished[identity] = event
        elif kind == 'error':
            errors.append(event)
            if event.get('isFailure') is True and type(event.get('testID')) is int:
                assertions.add(event.get('testID'))
        elif kind == 'print' and isinstance(event.get('message'), str):
            if type(event.get('testID')) is int and any(line.startswith(assertion_headers) for line in event['message'].splitlines()):
                assertions.add(event.get('testID'))
        elif kind == 'done':
            footers.append(event.get('success'))
    visible = {key: event for key, event in finished.items() if event.get('hidden') is False}
    actual_names = [starts[key] for key in visible]
    failed = {key for key, event in visible.items() if event.get('result') != 'success'}
    complete = (not malformed and len(footers) == 1 and footers[0] is False
                and starts.keys() == finished.keys()
                and all(isinstance(name, str) for name in actual_names)
                and sorted(actual_names) == sorted(phase['names'])
                and all(type(e.get('hidden')) is bool and e.get('skipped') is False for e in finished.values())
                and all(e.get('result') == 'success' for key, e in finished.items() if key not in visible))
    cleanup = None
    try:
        cleanup = CHECK.group_revoke_cleanup_summary(text, phase_cleanup(cfg, phase))
    except ValueError:
        complete = False
    assertion_only = (complete and bool(failed) and bool(errors)
                      and all(type(e.get('testID')) is int and e.get('testID') in failed and e.get('testID') in assertions for e in errors)
                      and not ANALYSIS.ui_warnings(text))
    return {'started': len(starts), 'completed': len(finished),
            'visible_names': actual_names,
            'passed_names': [starts[k] for k, e in visible.items() if e.get('result') == 'success' and e.get('skipped') is False],
            'failed_names': [starts[k] for k in failed],
            'complete_selected_assertion_failure': bool(assertion_only), 'cleanup': cleanup}


def bounded_source_post(work, cfg, old, label):
    # Read-only even after a latched child/resource failure. The primary error
    # and guard.reason are never cleared. One exact authored415 check per phase,
    # including failed phases; no duplicate successful whole-source postwalk.
    record = {'accepted': False, 'exception_type': None, 'authored_bodies': 0,
              'source363': FINAL_SOURCE, 'portable415': FINAL_PORTABLE,
              'deadline_seconds': SOURCE_POST_SECONDS}
    previous_handler = signal.getsignal(signal.SIGALRM)
    previous_timer = signal.getitimer(signal.ITIMER_REAL)
    def timeout(signum, frame):
        raise TimeoutError('bounded source-post expired')
    armed = changed_handler = False
    try:
        if previous_timer != (0.0, 0.0):
            raise ValueError('source-post cannot replace an existing timer')
        signal.signal(signal.SIGALRM, timeout)
        changed_handler = True
        signal.setitimer(signal.ITIMER_REAL, SOURCE_POST_SECONDS)
        armed = True
        files = verify_work(work, cfg, old)
        if len(files) != 415 or SOURCE.source_digest(files) != FINAL_PORTABLE:
            raise ValueError('source-post portable415 differs')
        record.update(accepted=True, authored_bodies=415, current_inventory_exact=True)
    except BaseException as error:
        record['exception_type'] = type(error).__name__
    finally:
        if armed:
            signal.setitimer(signal.ITIMER_REAL, 0)
        if changed_handler:
            signal.signal(signal.SIGALRM, previous_handler)
    CHECK.write_json(work/'evidence'/(label+'.source-post.json'), record)
    return record


def run_phase(work, cfg, old, guard, phase):
    label = phase['label']
    record = {'phase': label, 'accepted': False, 'exception_type': None,
              'expected_names': phase['names'], 'expected_tests': phase['expected']}
    primary = None
    command_returned = False
    resource = {}
    log = work/'evidence'/(label+'.log')
    try:
        # Exit1 is retained for actual selected assertions. Timeout/cap/floor
        # still raise before any code can interpret a child exit0 as success.
        CHECK.guarded(phase['command'], work, guard, label, timeout=120, accepted_exits=(0, 1))
        command_returned = True
        resource = json.loads(SOURCE.regular(work/'evidence'/(label+'.resource.json')))
        if resource['latched_failure'] or guard.reason:
            raise ValueError('latched phase/resource failure')
        text = SOURCE.regular(log).decode()
        record['observation'] = failure_observation(text, cfg, phase)
        record['summary'] = result_summary(text, cfg, phase)
        if resource['child_exit_code'] != 0:
            raise ValueError('nonzero child cannot pass')
        record['accepted'] = True
    except BaseException as error:
        primary = error
        record['exception_type'] = type(error).__name__
        if log.exists() and 'observation' not in record:
            try:
                record['observation'] = failure_observation(SOURCE.regular(log).decode(), cfg, phase)
            except BaseException as secondary:
                record['observation_exception_type'] = type(secondary).__name__
    finally:
        try:
            post = bounded_source_post(work, cfg, old, label)
        except BaseException as secondary:
            post = {'accepted': False, 'exception_type': type(secondary).__name__,
                    'receipt_write_failed': True}
            if primary is None:
                primary = secondary
        record['source_post'] = post
        record['latched_failure'] = guard.reason
        if not post['accepted']:
            record['accepted'] = False
            if primary is None:
                primary = ValueError('source-post refused')
                record['exception_type'] = type(primary).__name__
        if guard.reason:
            record['accepted'] = False
        try:
            CHECK.write_json(work/'evidence'/(label+'.summary.json'), record)
        except BaseException as secondary:
            record['accepted'] = False
            record['summary_write_exception_type'] = type(secondary).__name__
            if primary is None:
                primary = secondary
    record['continue_assertion_diagnostic'] = bool(
        command_returned and not resource.get('latched_failure')
        and not record['accepted'] and guard.reason is None and post['accepted']
        and record.get('observation', {}).get('complete_selected_assertion_failure')
        and json.loads(SOURCE.regular(work/'evidence'/(label+'.resource.json')))['child_exit_code'] == 1)
    return record, primary


def run(work, vm_diagnostic=False):
    cfg, old = descriptor()
    verify_work(work, cfg, old)
    CHECK.prove_namespace()
    guard = CHECK.Guard(work)
    if guard.sample() < CHECK.INITIAL or guard.reason:
        raise ValueError('initial4GiB/cap gate')
    monitor = threading.Thread(target=guard.poll, daemon=True)
    monitor.start()
    phases, primary = [], None
    try:
        CHECK.write_json(work/'evidence/input-local-reuse13-plus1.json', cfg['local_validation_reuse'])
        CHECK.write_json(work/'evidence/input-prior-cloud25.json', cfg['cloud_validation_reuse'])
        for phase in phase_commands(work, cfg, vm_diagnostic):
            result, error = run_phase(work, cfg, old, guard, phase)
            phases.append(result)
            if error is not None and primary is None:
                primary = error
            if guard.reason or not result['accepted'] and not result['continue_assertion_diagnostic']:
                break
    except BaseException as error:
        if primary is None:
            primary = error
    finally:
        try:
            guard.sample()
            CHECK.write_json(work/'evidence/input-final-resource.json',
                             {'minimum_free_bytes': guard.minimum, 'peak_workspace_allocated_bytes': guard.peak,
                              'latched_failure': guard.reason, 'failure_wins_over_passed_receipt': True})
        except BaseException as secondary:
            if primary is None:
                primary = secondary
        finally:
            guard.stop.set()
            monitor.join()
    if primary is not None or guard.reason or len(phases) != 1 or not all(p['accepted'] for p in phases):
        try:
            CHECK.write_json(work/'evidence/input-failed.json',
                             {'type': type(primary).__name__ if primary is not None else 'ValueError',
                              'phases': phases, 'latched_failure': guard.reason,
                              'entire_batch_passed': False, 'prior_source_post_exact_claimed': False})
        finally:
            if primary is not None:
                raise primary
            raise ValueError('targeted continuation failed')
    if vm_diagnostic:
        diagnostic = module('owned_mobile_vm_diagnostic', REPO/'ci/mobile-input-vm.py')
        diagnostic.write_terminal_pass(work, phases, FINAL_SOURCE, FINAL_PORTABLE)
        return
    CHECK.write_json(work/'evidence/input-scoped-passed.json',
                     {'state': 'current363_diagnostic1_with_explicit_prior25', 'phases': phases,
                      'cloud_analysis_executed': 0, 'cloud_tests_executed': 1,
                      'local_reuse_sha256': LOCAL_REUSE_SHA, 'prior_cloud_reuse_sha256': CLOUD_REUSE_SHA,
                      'source363': FINAL_SOURCE, 'portable415': FINAL_PORTABLE,
                      'cleanup': {'new_fixture_sequences': 1, 'new_markers': 4,
                                  'prior_cloud_reused_fixtures': 24, 'prior_cloud_reused_markers': 96,
                                  'combined_fixture_sequences': 25, 'combined_markers': 100,
                                  'newly_executed_25_fixtures': False,
                                  'cloud_named_union27_includes_local_gateway1': True},
                      'full1329_executed': False, 'native_device_service_acceptance': False})


def netns(work, run_as, vm_diagnostic=False):
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
    if vm_diagnostic:
        command.append('--vm-wait-diagnostic')
    os.execv(command[0], command)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=('verify', 'materialize', 'netns', 'run'))
    parser.add_argument('--work', type=Path)
    parser.add_argument('--output', type=Path)
    parser.add_argument('--run-as')
    parser.add_argument('--vm-wait-diagnostic', action='store_true')
    args = parser.parse_args()
    if args.vm_wait_diagnostic and args.mode not in ('netns','run'):
        parser.error('VM diagnostic mode only applies to this one guarded run')
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
        netns(args.work, args.run_as, args.vm_wait_diagnostic)
    else:
        run(args.work, args.vm_wait_diagnostic)


if __name__ == '__main__':
    main()
