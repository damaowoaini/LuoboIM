#!/usr/bin/env python3
"""Manual isolated announcement26 overlay; App354 and all historical CI identities stay intact."""
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
INPUTS = REPO / 'ci/announcement26/inputs.json'
PAYLOAD = 'ci/candidates/mobile-announcement26/after'
PRODUCT = ['app/lib/features/chat/presentation/chat_page.dart', 'app/lib/features/conversation/data/conversation_repository.dart', 'app/lib/features/conversation/domain/conversation.dart', 'app/lib/features/group/data/group_repository.dart', 'app/lib/features/group/domain/group.dart', 'app/lib/providers.dart']
SELECTED = ['app/lib/features/chat/data/group_announcement_store.dart', 'app/lib/features/chat/presentation/chat_page.dart', 'app/lib/features/chat/presentation/group_announcement_panel.dart', 'app/lib/features/conversation/data/conversation_repository.dart', 'app/lib/features/conversation/domain/conversation.dart', 'app/lib/features/group/data/group_repository.dart', 'app/lib/features/group/domain/group.dart', 'app/lib/providers.dart', 'app/test/features/chat/group_announcement_fixture.dart', 'app/test/features/chat/group_announcement_store_test.dart', 'app/test/features/chat/group_announcement_workflow_test.dart']
ANALYSIS = ['lib/features/chat/data/group_announcement_store.dart', 'lib/features/chat/presentation/chat_page.dart', 'lib/features/chat/presentation/group_announcement_panel.dart', 'lib/features/conversation/data/conversation_repository.dart', 'lib/features/conversation/domain/conversation.dart', 'lib/features/group/data/group_repository.dart', 'lib/features/group/domain/group.dart', 'lib/providers.dart', 'test/features/chat/group_announcement_fixture.dart', 'test/features/chat/group_announcement_store_test.dart', 'test/features/chat/group_announcement_workflow_test.dart']
TWENTY_SIX = ['SDK hint4 is announcement while true at1to3 and preview stripping preserve independent identity', 'fresh snapshot bounds reject controls bidi malformed unicode and unavailable membership while URL text stays plain', 'acknowledged exact-version dismissal survives reopening without consuming a republished version or another owner group', 'preferences failure leaves the version unacknowledged and one explicit retry persists only a boolean digest key', 'queued preference dispatch rechecks its original source before physical acknowledgement', 'an already dispatched dismissal keeps its original key after source retirement without acknowledging another owner', 'actual group chat opens a fresh ordinary-member announcement and its own detail stays readable', 'acknowledged announcement close survives a new real chat route while a republished version appears again', 'a preferences failure retains the visible announcement until one explicit retry is acknowledged', 'a delayed versionA dismissal never consumes a newly loaded versionB', 'a genuine at-me hint does not create an announcement from historical group text', 'failed fresh group metadata offers an actual read-only retry instead of an empty successful announcement', 'fresh duplicate group metadata withdraws the announcement before any acknowledgement', 'fresh foreign group metadata withdraws the announcement before any acknowledgement', 'fresh left group metadata withdraws the announcement before any acknowledgement', 'original owner replacement withdraws an open announcement and rejects its retained close callback', 'original issuer replacement withdraws an open announcement and rejects its retained close callback', 'original tenant replacement withdraws an open announcement and rejects its retained close callback', 'owner replacement while original group metadata waits prevents its next real member lookup', 'a same-State CID ABA cannot renew the original announcement dialog or its acknowledgement', 'foreign coverage retires only the old owned announcement and a new explicit read remains usable', 'foreground loss immediately rejects the old callback and the next frame withdraws announcement text', 'sameCID metadata refresh preserves composer draft and never consumes genuine strong hints', 'muted-admin announcement controls and full URL-like text remain usable at360 large text without automatic link behavior', 'SDK paging, drafts, read and preference mutations preserve the exact conversation target', 'group flags keep prohibition semantics and booleans serialize to SDK status 1']
TESTS = ['test/features/chat/group_announcement_store_test.dart', 'test/features/chat/group_announcement_workflow_test.dart', 'test/features/conversation/conversation_repository_test.dart', 'test/features/contacts/contacts_repository_test.dart']
WIDGET_NAMES = ['actual group chat opens a fresh ordinary-member announcement and its own detail stays readable', 'acknowledged announcement close survives a new real chat route while a republished version appears again', 'a preferences failure retains the visible announcement until one explicit retry is acknowledged', 'a delayed versionA dismissal never consumes a newly loaded versionB', 'a genuine at-me hint does not create an announcement from historical group text', 'failed fresh group metadata offers an actual read-only retry instead of an empty successful announcement', 'fresh duplicate group metadata withdraws the announcement before any acknowledgement', 'fresh foreign group metadata withdraws the announcement before any acknowledgement', 'fresh left group metadata withdraws the announcement before any acknowledgement', 'original owner replacement withdraws an open announcement and rejects its retained close callback', 'original issuer replacement withdraws an open announcement and rejects its retained close callback', 'original tenant replacement withdraws an open announcement and rejects its retained close callback', 'owner replacement while original group metadata waits prevents its next real member lookup', 'a same-State CID ABA cannot renew the original announcement dialog or its acknowledgement', 'foreign coverage retires only the old owned announcement and a new explicit read remains usable', 'foreground loss immediately rejects the old callback and the next frame withdraws announcement text', 'sameCID metadata refresh preserves composer draft and never consumes genuine strong hints', 'muted-admin announcement controls and full URL-like text remain usable at360 large text without automatic link behavior']
MARKERS = ['sdk.begin', 'sdk.end', 'unmount.begin', 'unmount.end']
HELPERS = ['ci/mobile-source.py', 'ci/mobile-check.py', 'ci/mobile-setup.sh', 'ci/mobile/inputs.json', 'ci/mobile/required-test-names.json']

RUN_ANALYSIS = ANALYSIS[5:]
REUSE_RUNS = ['38974011605', '38975527239']
REUSE_EXPECTED = [{'run': '38974011605', 'job': '117218966812', 'analysis_path': 'lib/features/chat/data/group_announcement_store.dart', 'source_pin': {'path': 'app/lib/features/chat/data/group_announcement_store.dart', 'bytes': 5012, 'sha256': 'b5da8994042e75f703e4cae3ac3f4a4e1b773fdb166b31a46323a15d0c7a6be4'}, 'log_pin': {'path': 'announcement-analyze-1.log', 'bytes': 60, 'sha256': 'e5c0d71055c201ca9443a5438be1a7fcf90646588c51a0b1ffca258dde61a1a2'}, 'resource_pin': {'path': 'announcement-analyze-1.resource.json', 'bytes': 489, 'sha256': '425a82b4253eebfc1c2989593b7f13adbfea7f48fecc3b02d08db43ad6af7f79'}}, {'run': '38974011605', 'job': '117218966812', 'analysis_path': 'lib/features/chat/presentation/chat_page.dart', 'source_pin': {'path': 'app/lib/features/chat/presentation/chat_page.dart', 'bytes': 99227, 'sha256': 'b77248a2b7e1b21ab340d63a9396b88e5fe55107bc300265cb931f57975d8de9'}, 'log_pin': {'path': 'announcement-analyze-2.log', 'bytes': 45, 'sha256': 'ee013d51a98c383aaeef5a9d30dd360e41b03e39bbf831cc58a28a83be608aa0'}, 'resource_pin': {'path': 'announcement-analyze-2.resource.json', 'bytes': 483, 'sha256': '80f7d82dab8a8baab1d14610e9553ee15cb3c61934f7db52117be693fe6798cf'}}, {'run': '38975527239', 'job': '117223929591', 'analysis_path': 'lib/features/chat/presentation/group_announcement_panel.dart', 'source_pin': {'path': 'app/lib/features/chat/presentation/group_announcement_panel.dart', 'bytes': 17053, 'sha256': '5c45548163ed6dc9fc8ef0f96dd405199e4e19bc1b8e078694772955491dbecf'}, 'log_pin': {'path': 'announcement-analyze-3.log', 'bytes': 60, 'sha256': '37c5d8b80e1275336578f7a67e33f0adfc976f455c58107d307e7aaf759e6343'}, 'resource_pin': {'path': 'announcement-analyze-3.resource.json', 'bytes': 497, 'sha256': '32a237685abe1da2a16b7f72d8c66009c3939e5857a79d798500c5bee62343c5'}}, {'run': '38975527239', 'job': '117223929591', 'analysis_path': 'lib/features/conversation/data/conversation_repository.dart', 'source_pin': {'path': 'app/lib/features/conversation/data/conversation_repository.dart', 'bytes': 7551, 'sha256': '299b465182570c55a2b829c36b13b7db6c62454f157e4549ffd26417373501e8'}, 'log_pin': {'path': 'announcement-analyze-4.log', 'bytes': 59, 'sha256': 'b2edb2535cb7e727ababeab90707bc44dfd565377af333dd980ba8a4e0db599d'}, 'resource_pin': {'path': 'announcement-analyze-4.resource.json', 'bytes': 496, 'sha256': '98d64d9fee144c0695fe5db44eb08dde4eab672b75d441e2432048dcbc84fb50'}}, {'run': '38975527239', 'job': '117223929591', 'analysis_path': 'lib/features/conversation/domain/conversation.dart', 'source_pin': {'path': 'app/lib/features/conversation/domain/conversation.dart', 'bytes': 1952, 'sha256': '5e114b9a1c8559f24236662f6bb050dd73550d7f38551b9955fffdc8ddf3d098'}, 'log_pin': {'path': 'announcement-analyze-5.log', 'bytes': 48, 'sha256': 'daab4b4fe08b20c8598f0b6bd27571407e7e64a0b01dc22341f5f19128f3376b'}, 'resource_pin': {'path': 'announcement-analyze-5.resource.json', 'bytes': 487, 'sha256': '020902974c2f1c2ddb6cd675c0eaf9c4cda7fc9abcd06383ef94ad1dd8e24d3d'}}]


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
    selected = cfg['candidate']['selected11']
    if (cfg['schema'] != 'luobo.mobile.announcement26.isolated-cloud.v1'
            or cfg['cohort'] != 'candidate359-announcement26'
            or (cfg['base']['cohort'], cfg['base']['source_members'], cfg['base']['portable_members'])
            != ('current354', 354, 406)
            or [r['path'] for r in cfg['base']['helper_pins']] != HELPERS
            or cfg['base']['source_digest'] != '113f8a3ff3cfaa3d17ba77b67cb28605a1220fa86359b8f0661535180075364c'
            or cfg['base']['portable_digest'] != '81f0628bda255611d9a624aee518d1a77e63cd1dfdc07d91f9a932c33c7f199c'
            or cfg['candidate']['source_digest'] != '1fd4048febdce707a91822f7b7ca56833a894c1d0251866734cd42d2ef2d8c8e'
            or cfg['candidate']['portable_digest'] != 'a2f024ce7225b6815d13e17ef40d3837be1824103cf47a56b885474849e050c5'
            or cfg['candidate']['payload_prefix'] != PAYLOAD
            or [r['path'] for r in cfg['base']['before6']] != PRODUCT
            or [r['path'] for r in selected] != SELECTED
            or any(type(r['new']) is not bool or r['new'] != (r['path'] not in PRODUCT) for r in selected)
            or any(r['payload_path'] != PAYLOAD + '/' + r['path'] for r in selected)
            or cfg['candidate']['source_members'] != 359 or cfg['candidate']['portable_members'] != 411
            or cfg['target']['analysis_paths'] != ANALYSIS
            or cfg['target']['analysis_run_paths'] != RUN_ANALYSIS
            or cfg['target']['analysis_total_coverage'] != 11
            or cfg['target']['analysis_reuse_runs'] != REUSE_RUNS
            or cfg['target']['names'] != TWENTY_SIX or len(set(TWENTY_SIX)) != 26
            or cfg['target']['new24'] != TWENTY_SIX[:24]
            or [r['name'] for r in cfg['target']['old2']] != TWENTY_SIX[24:]
            or [r['path'] for r in cfg['target']['old2']] != TESTS[2:]
            or cfg['target']['expected_tests'] != 26 or cfg['target']['test_paths'] != TESTS
            or cfg['target']['cleanup_prefix'] != 'group_announcement_cleanup '
            or cfg['target']['cleanup_names'] != WIDGET_NAMES or len(set(WIDGET_NAMES)) != 18
            or cfg['target']['cleanup_markers'] != MARKERS
            or cfg['target']['expected_owned_fixtures'] != 18 or cfg['target']['expected_markers'] != 72
            or cfg['resource'] != {'initial_bytes': 4294967296, 'floor_bytes': 1073741824,
                                   'allocated_cap_bytes': 805306368, 'poll_seconds': .2,
                                   'phase_seconds': 120}):
        raise ValueError('announcement scope or resource contract differs')
    for row in selected + cfg['base']['before6'] + cfg['base']['helper_pins']:
        safe(row['path'])
        if type(row['bytes']) is not int or row['bytes'] <= 0 or not re.fullmatch('[0-9a-f]{64}', row['sha256']):
            raise ValueError('invalid byte/hash pin')
    return cfg


def lineage_contract(cfg, base_plan):
    # Historical Card17 and Revoke6 remain exact in the pinned current354 inputs.
    # Only this isolated overlay records the next before354 -> after359 edges.
    card, revoke, lineage = base_plan['chat_card'], base_plan['group_revoke'], cfg['lineage']
    before = {r['path']: r for r in cfg['base']['before6']}
    after = {r['path']: {k: r[k] for k in ('path', 'bytes', 'sha256')}
             for r in cfg['candidate']['selected11']}
    expected_edges = lambda rows: [{'before354': before[r['path']], 'after359': after[r['path']]}
                                   for r in rows if r['path'] in before]
    if (lineage['card351_original17'] != card['selected_files']
            or len(lineage['card351_original17']) != 17
            or lineage['card351_to354'] != card['current_source_lineage']
            or lineage['card354_to359'] != expected_edges(card['selected_files'])
            or len(lineage['card354_to359']) != 2
            or lineage['group_revoke354_original6'] != revoke['selected_files']
            or len(lineage['group_revoke354_original6']) != 6
            or lineage['group_revoke354_to359'] != expected_edges(revoke['selected_files'])
            or len(lineage['group_revoke354_to359']) != 1
            or lineage['historical_identities_immutable'] is not True
            or lineage['native530_ref_unchanged'] != 'ff7bc1f588f935d6315ad0211e68e15e58cd037e'):
        raise ValueError('Card351->354->359 or Revoke354->359 lineage differs')
    unchanged = cfg['unchanged_dependencies']
    baseline = {r['path']: r for r in base_plan['current354']['files']}
    if (unchanged['locked_packages'] != 178 or unchanged['pubspec'] != baseline['app/pubspec.yaml']
            or unchanged['lock'] != baseline['app/pubspec.lock']
            or unchanged['official_toolchain'] != base_plan['toolchain']
            or unchanged['native_snapshot'] != base_plan['native_source_snapshot']):
        raise ValueError('existing dependency/tool/native contract differs')
    return True


def reused_analysis_contract(cfg, actual_rows):
    entries = cfg['target']['analysis_reuse_entries']
    fields = ('run', 'job', 'analysis_path', 'source_pin', 'log_pin', 'resource_pin')
    if ([{k: r[k] for k in fields} for r in entries] != REUSE_EXPECTED
            or cfg['target']['analysis_reuse_runs'] != REUSE_RUNS):
        raise ValueError('reused analysis identity differs')
    actual = {r['path']: r for r in actual_rows}
    for entry in entries:
        source_pin = entry['source_pin']
        if actual.get(source_pin['path']) != source_pin:
            raise ValueError('reused analysis source body differs')
        log = pinned(entry['log_utf8'].encode('utf-8'), entry['log_pin'])
        resource = json.loads(pinned(entry['resource_utf8'].encode('utf-8'), entry['resource_pin']))
        if (type(resource['child_exit_code']) is not int or resource['child_exit_code'] != 0
                or resource['latched_failure'] is not None
                or resource['log_sha256'] != sha(log)
                or resource['command'][-3:] != ['analyze', '--fatal-infos', entry['analysis_path']]
                or 'No issues found!' not in log.decode('utf-8')):
            raise ValueError('reused analysis is not a successful fatal-info result')
    return {'prior_runs': REUSE_RUNS, 'exact_source_and_raw_resource_pins': REUSE_EXPECTED,
            'reused_successes': 5, 'not_executed_again': True}


def descriptor():
    cfg = validate_descriptor(json.loads(regular(INPUTS)))
    for row in cfg['base']['helper_pins']:
        pinned(regular(REPO / safe(row['path'])), row)
    return cfg


def legacy():
    spec = importlib.util.spec_from_file_location('announcement_existing_check', REPO / 'ci/mobile-check.py')
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
    base, files, digest = source.load_sources('current354')
    if (digest != cfg['base']['source_digest'] or len(files) != 406
            or source.source_digest(files) != cfg['base']['portable_digest']):
        raise ValueError('original354/406 identity differs')
    lineage_contract(cfg, base)
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
        raise ValueError('payload tree is not exact eleven')
    payload = {r['path']: pinned(regular(REPO / safe(r['payload_path'])), r)
               for r in cfg['candidate']['selected11']}
    files = apply_overlay(files, cfg['base']['before6'], cfg['candidate']['selected11'], payload)
    app = {name: body for name, body in files.items() if name.startswith('app/')}
    if (len(app) != 359 or len(files) != 411
            or source.source_digest(app) != cfg['candidate']['source_digest']
            or source.source_digest(files) != cfg['candidate']['portable_digest']):
        raise ValueError('candidate359/411 aggregate differs')
    reused_analysis_contract(cfg, source.rows(files))
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
    check.verify(work, 'current354')  # Original setup remains354; only the owned overlay becomes359.
    _, files = sources(cfg, source)
    if any(p.suffix == '.dill' or p.name.endswith('.stamp') for p in (work/'source').rglob('*') if p.is_file()):
        raise ValueError('old compiled app output before overlay')
    receipt = work/'evidence/announcement-overlay.json'
    if receipt.exists():
        raise ValueError('overlay cannot be replayed')
    if os.statvfs(work).f_bavail * os.statvfs(work).f_frsize < check.INITIAL:
        raise ValueError('overlay initial4GiB gate')
    for row in cfg['candidate']['selected11']:
        if os.statvfs(work).f_bavail * os.statvfs(work).f_frsize < check.FLOOR:
            raise ValueError('overlay partial retained at resource floor')
        target = work/'source'/safe(row['path'])
        target.parent.mkdir(parents=True, exist_ok=True)
        if row['new']:
            with target.open('xb') as stream:
                stream.write(files[row['path']])
        else:
            pinned(regular(target), next(r for r in cfg['base']['before6'] if r['path'] == row['path']))
            backup = work/'evidence/announcement-before'/safe(row['path'])
            backup.parent.mkdir(parents=True, exist_ok=True)
            with backup.open('xb') as stream:
                stream.write(target.read_bytes())
            target.write_bytes(files[row['path']])
    verify_work(work, cfg, source)
    check.write_json(receipt, {'cohort': cfg['cohort'], 'base_setup_cohort': 'current354',
                              'source359': cfg['candidate']['source_digest'],
                              'portable411': cfg['candidate']['portable_digest'],
                              'files': source.rows(files), 'source_checkout_untouched': True,
                              'old_kernel_or_hook_stamp_copied': False})


def phase_commands(work):
    dart = str(work/'flutter/bin/cache/dart-sdk/bin/dart')
    flutter = str(work/'flutter/bin/flutter')
    return ([[dart, 'analyze', '--fatal-infos', path] for path in RUN_ANALYSIS],
            [flutter, 'test', '--no-pub', '--machine', '--concurrency=1',
             '--no-enable-vmservice', '--name', '^(?:' + '|'.join(map(re.escape, TWENTY_SIX)) + ')$'] + TESTS)


def analysis_outcome(record, text, label, command):
    """Only complete recognizable Dart diagnostics may defer lane failure."""
    code = record['child_exit_code']
    if (type(code) is not int or code not in (0, 1, 2, 3)
            or record['latched_failure'] is not None or record['phase'] != label
            or record['command'] != command or record['log_sha256'] != sha(text.encode('utf-8'))):
        raise ValueError('analysis resource, command or tool failure')
    lines = [line.strip() for line in text.splitlines() if line.strip()]
    heading = 'Analyzing ' + Path(command[-1]).name + '...'
    if code == 0:
        if lines != [heading, 'No issues found!']:
            raise ValueError('unknown successful analyzer output')
        return {'phase': label, 'path': command[-1], 'passed': True, 'diagnostics': 0}
    if len(lines) < 3 or lines[0] != heading:
        raise ValueError('unknown analyzer failure')
    footer = re.fullmatch(r'([1-9][0-9]*) issues? found\.', lines[-1])
    diagnostics = [re.fullmatch(r'(info|warning|error) - (.+?):([1-9][0-9]*):([1-9][0-9]*) - (.+) - ([a-z_][a-z_0-9]*)',
                                line) for line in lines[1:-1]]
    if (footer is None or not diagnostics or any(match is None for match in diagnostics)
            or int(footer[1]) != len(diagnostics)
            or max({'info': 1, 'warning': 2, 'error': 3}[match[1]] for match in diagnostics) != code):
        raise ValueError('incomplete or unknown analyzer diagnostic failure')
    return {'phase': label, 'path': command[-1], 'passed': False,
            'diagnostics': len(diagnostics), 'child_exit_code': code}


def collect_analyses(commands, execute, verify, report):
    """Continue ordinary Dart diagnostics; exceptions immediately propagate."""
    results = []
    for index, command in enumerate(commands, 6):
        verify()
        label = 'announcement-analyze-' + str(index)
        record, text = execute(label, command)
        result = analysis_outcome(record, text, label, command)
        verify()
        results.append(result)
        report(result)
    return {'results': results, 'executed_new': len(results),
            'all_passed': all(result['passed'] for result in results),
            'failed_paths': [result['path'] for result in results if not result['passed']]}


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
            if message.startswith('group_announcement_cleanup '):
                identity = event.get('testID')
                if identity not in starts or starts[identity] not in WIDGET_NAMES:
                    raise ValueError('foreign cleanup owner')
                sequences.setdefault(identity, []).append(message[len('group_announcement_cleanup '):])
    actual = []
    for identity, sequence in sequences.items():
        name = starts[identity]
        if sequence != MARKERS or name in actual:
            raise ValueError('missing/duplicate/out-of-order owned cleanup')
        actual.append(name)
    if Counter(actual) != Counter(WIDGET_NAMES):
        raise ValueError('missing selected cleanup')
    return {'owned_widget_fixtures': 18, 'events': 72, 'exact_four_marker_order': True}


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
    files = verify_work(work, cfg, source)
    guard = check.Guard(work)
    if guard.sample() < check.INITIAL or guard.reason:
        raise ValueError('initial4GiB/cap admission')
    thread = threading.Thread(target=guard.poll, daemon=True)
    thread.start()
    counts = {}
    try:
        analyses, tests = phase_commands(work)
        reuse_proof = reused_analysis_contract(cfg, source.rows(files))
        check.write_json(work/'evidence/announcement-reused-analyses.json',
                         {'proof': reuse_proof, 'raw_entries': cfg['target']['analysis_reuse_entries']})
        def execute_analysis(label, command):
            # Exits1..3 are permitted only so a recognized diagnostic can be collected.
            # Guard resource/time failures and all other exits still immediately raise.
            log = check.guarded(command, work, guard, label,
                                timeout=cfg['resource']['phase_seconds'], accepted_exits=(0, 1, 2, 3))
            record = json.loads(regular(work/'evidence'/(label + '.resource.json')))
            return record, log.read_text()

        analysis_results = collect_analyses(
            analyses, execute_analysis, lambda: verify_work(work, cfg, source),
            lambda result: check.write_json(work/'evidence'/(result['phase'] + '.summary.json'), result))
        counts['analysis'] = dict(reuse_proof, **analysis_results, total_coverage=11)
        check.write_json(work/'evidence/announcement-analyses.summary.json', counts['analysis'])
        if not analysis_results['all_passed']:
            raise ValueError('Dart diagnostics collected; target26 was not started')
        verify_work(work, cfg, source)
        log = check.guarded(tests, work, guard, 'announcement-target26',
                            timeout=cfg['resource']['phase_seconds'])
        text = log.read_text()
        counts['tests'] = check.machine_summary(text, 26)
        counts['exact_names'] = check.required_name_summary(text, 26, [{'name': n, 'count': 1} for n in TWENTY_SIX])
        counts['cleanup'] = cleanup_summary(text)
        warnings = ui_warnings(text)
        if warnings:
            raise ValueError('UI warnings retained; target not accepted')
        verify_work(work, cfg, source)
        check.write_json(work/'evidence/announcement.summary.json', counts)
        check.write_json(work/'evidence/announcement-passed.json',
                         {'state': 'candidate_target26_analysis6new_plus5reused_completed',
                          'cohort': cfg['cohort'], 'source359': cfg['candidate']['source_digest'],
                          'portable411': cfg['candidate']['portable_digest'], 'counts': counts,
                          'oldCard80_Revoke58_full_not_rerun': True,
                          'App354_not_adopted': True, 'native_service_acceptance': False})
        guard.sample()
    except BaseException as error:
        check.write_json(work/'evidence/announcement-failed.json',
                         {'state': 'failed_or_incomplete', 'type': type(error).__name__,
                          'detail': str(error), 'latched_failure': guard.reason})
        raise
    finally:
        try:
            check.write_json(work/'evidence/announcement-final-resource.json',
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
    print(json.dumps({'state': 'candidate26_PASS_analysis6new_plus5reused_0', 'counts': counts}))


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
                          'source359': cfg['candidate']['source_digest'],
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
        cfg = descriptor()
        source, _ = legacy()
        verify_work(args.work, cfg, source)


if __name__ == '__main__':
    main()
