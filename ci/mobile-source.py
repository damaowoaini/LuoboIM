#!/usr/bin/env python3
"""Verify minimal pinned source; optionally copy it to a new CI-only tree.

No local task archive, mutable reference directory, native SDK binary or old
generated cache is an input. Current359 must already be adopted in the checkout.
The optional reference is full359 with exactly four pinned historical search baseline files.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import stat

REPO = Path(__file__).resolve().parent.parent
INPUTS = REPO / 'ci/mobile/inputs.json'
FLOOR = 1024 ** 3
INITIAL = 4 * FLOOR
GROUP_REVOKE_TEST_PATHS = ['test/features/chat/group_revoke_repository_test.dart', 'test/features/chat/group_revoke_workflow_test.dart']
GROUP_REVOKE_ANALYSIS_PATHS = ['lib/features/chat/data/chat_repository.dart', 'lib/features/chat/presentation/chat_controller.dart', 'lib/features/chat/presentation/chat_page.dart', 'test/features/chat/group_revoke_fixture.dart', 'test/features/chat/group_revoke_repository_test.dart', 'test/features/chat/group_revoke_workflow_test.dart']
CARD_REVOKE_BEFORE3 = [{'path': 'app/lib/features/chat/data/chat_repository.dart', 'bytes': 50484, 'sha256': '4c1dfa2d10ca21f2d6ac302dacc5aeb1c9b897f9923c2ef5a4219165d1e2efdb'}, {'path': 'app/lib/features/chat/presentation/chat_controller.dart', 'bytes': 22967, 'sha256': 'd8a9e14bc26a25eb86c88dbffc95a04e7ff5463e7629021571f3e8274a1297c9'}, {'path': 'app/lib/features/chat/presentation/chat_page.dart', 'bytes': 87839, 'sha256': '08869981fe618ef7661fb120b96de5f017c6439c9dac32f2ff3fcc269526fb4b'}]
CARD_TEST_PATHS = ['test/features/chat/chat_card_model_test.dart', 'test/features/chat/chat_card_repository_test.dart', 'test/features/chat/chat_card_workflow_test.dart']
CARD_ANALYSIS_PATHS = ['lib/design/components/feedback.dart', 'lib/features/chat/data/chat_repository.dart', 'lib/features/chat/data/message_mapper.dart', 'lib/features/chat/domain/chat_card.dart', 'lib/features/chat/domain/chat_message.dart', 'lib/features/chat/presentation/chat_card_picker_page.dart', 'lib/features/chat/presentation/chat_card_view.dart', 'lib/features/chat/presentation/chat_controller.dart', 'lib/features/chat/presentation/chat_media_actions.dart', 'lib/features/chat/presentation/chat_page.dart', 'lib/features/contacts/data/contacts_repository.dart', 'lib/features/contacts/presentation/user_profile_page.dart', 'lib/features/group/data/group_repository.dart', 'test/features/chat/chat_card_fixture.dart', 'test/features/chat/chat_card_model_test.dart', 'test/features/chat/chat_card_repository_test.dart', 'test/features/chat/chat_card_workflow_test.dart']
ANNOUNCEMENT_TEST_PATHS = ['test/features/chat/group_announcement_store_test.dart', 'test/features/chat/group_announcement_workflow_test.dart']
ANNOUNCEMENT_ANALYSIS_PATHS = ['lib/features/chat/data/group_announcement_store.dart', 'lib/features/chat/presentation/chat_page.dart', 'lib/features/chat/presentation/group_announcement_panel.dart', 'lib/features/conversation/data/conversation_repository.dart', 'lib/features/conversation/domain/conversation.dart', 'lib/features/group/data/group_repository.dart', 'lib/features/group/domain/group.dart', 'lib/providers.dart', 'test/features/chat/group_announcement_fixture.dart', 'test/features/chat/group_announcement_store_test.dart', 'test/features/chat/group_announcement_workflow_test.dart']
ANNOUNCEMENT_BEFORE6 = [{'bytes': 98364, 'path': 'app/lib/features/chat/presentation/chat_page.dart', 'sha256': '6c48c1224cb98870a38ada5e7b0c05dc944321a762bbda19cc40f38c42a68590'}, {'bytes': 7335, 'path': 'app/lib/features/conversation/data/conversation_repository.dart', 'sha256': '005dd617e6bbdb8f5597bca03934d5b4fa66bae9845333ca14401c795745de0f'}, {'bytes': 1424, 'path': 'app/lib/features/conversation/domain/conversation.dart', 'sha256': 'b089eb93ec54af4020ee4e618cab69d104a9984c6a380da6e03098632203eeb3'}, {'bytes': 18412, 'path': 'app/lib/features/group/data/group_repository.dart', 'sha256': '80d74efba7edff2eac89171757b0cf345d63c60ef4347af07ebb5725cb0e3207'}, {'bytes': 1541, 'path': 'app/lib/features/group/domain/group.dart', 'sha256': '46f1c047de1130d4de0939de656e25214e0d7f57d0ded56f92a53be5b4eebbfc'}, {'bytes': 7441, 'path': 'app/lib/providers.dart', 'sha256': 'd56739337d0a4bebdfed496c6e2f2a6f7a3164be4177623de48c81520014ea73'}]
ANNOUNCEMENT_NEW5 = ['app/lib/features/chat/data/group_announcement_store.dart', 'app/lib/features/chat/presentation/group_announcement_panel.dart', 'app/test/features/chat/group_announcement_fixture.dart', 'app/test/features/chat/group_announcement_store_test.dart', 'app/test/features/chat/group_announcement_workflow_test.dart']
ANNOUNCEMENT_NAMES24 = ['SDK hint4 is announcement while true at1to3 and preview stripping preserve independent identity', 'fresh snapshot bounds reject controls bidi malformed unicode and unavailable membership while URL text stays plain', 'acknowledged exact-version dismissal survives reopening without consuming a republished version or another owner group', 'preferences failure leaves the version unacknowledged and one explicit retry persists only a boolean digest key', 'queued preference dispatch rechecks its original source before physical acknowledgement', 'an already dispatched dismissal keeps its original key after source retirement without acknowledging another owner', 'actual group chat opens a fresh ordinary-member announcement and its own detail stays readable', 'acknowledged announcement close survives a new real chat route while a republished version appears again', 'a preferences failure retains the visible announcement until one explicit retry is acknowledged', 'a delayed versionA dismissal never consumes a newly loaded versionB', 'a genuine at-me hint does not create an announcement from historical group text', 'failed fresh group metadata offers an actual read-only retry instead of an empty successful announcement', 'fresh duplicate group metadata withdraws the announcement before any acknowledgement', 'fresh foreign group metadata withdraws the announcement before any acknowledgement', 'fresh left group metadata withdraws the announcement before any acknowledgement', 'original owner replacement withdraws an open announcement and rejects its retained close callback', 'original issuer replacement withdraws an open announcement and rejects its retained close callback', 'original tenant replacement withdraws an open announcement and rejects its retained close callback', 'owner replacement while original group metadata waits prevents its next real member lookup', 'a same-State CID ABA cannot renew the original announcement dialog or its acknowledgement', 'foreign coverage retires only the old owned announcement and a new explicit read remains usable', 'foreground loss immediately rejects the old callback and the next frame withdraws announcement text', 'sameCID metadata refresh preserves composer draft and never consumes genuine strong hints', 'muted-admin announcement controls and full URL-like text remain usable at360 large text without automatic link behavior']

WEBVIEW = {
    'webview_flutter': ('4.14.1', 'd53e1ccf5516f25017e3c9d44c39034db352d20fa34fe200674270242c2c5111', 'direct main'),
    'webview_flutter_android': ('4.12.0', 'ad5182eff9a550925330cb9f0cb038eddfdd5712aba8b77aa0f0400e50f6e688', 'direct main'),
    'webview_flutter_platform_interface': ('2.15.1', '1221c1b12f5278791042f2ec2841743784cf25c5a644e23d6680e5d718824f04', 'direct dev'),
    'webview_flutter_wkwebview': ('3.25.1', '82648217f537573e1ca9ae9952d3eacedca6ab5aee69dc84445fc763766dcea2', 'transitive'),
}


def sha(body):
    return hashlib.sha256(body).hexdigest()


def safe_name(name):
    p = PurePosixPath(name)
    if (not name or p.is_absolute() or str(p) != name or '\\' in name
            or any(c in name for c in ('\x00', ':'))
            or any(x in ('', '.', '..', '.dart_tool', 'build') for x in name.split('/'))):
        raise ValueError('noncanonical authored-source path')
    return p


def regular(path):
    if path.resolve() != path or not stat.S_ISREG(path.lstat().st_mode):
        raise ValueError('source must be a regular file without symlink ancestors')
    return path.read_bytes()


def check(body, row):
    if len(body) != row['bytes'] or sha(body) != row['sha256']:
        raise ValueError('pinned source mismatch: ' + row['path'])
    return body


def rows(files):
    return [{'path': p, 'bytes': len(b), 'sha256': sha(b)} for p, b in sorted(files.items())]


def source_digest(files):
    return sha(json.dumps(rows(files), ensure_ascii=False, sort_keys=True,
                          separators=(',', ':')).encode())


def webview_dependency_contract(plan, lock):
    contract = plan['webview_dependencies']
    entries = contract['files']
    blocks = {m.group(1): m.group(0) for m in re.finditer(
        r'^  ([A-Za-z_][A-Za-z_0-9]*):\n(?=    dependency:).*?(?=^  [A-Za-z_][A-Za-z_0-9]*:|^sdks:|\Z)',
        lock.decode(), re.M | re.S)}
    if (contract['locked_packages'] != 178 or len(blocks) != 178
            or len(entries) != 4 or {r['name'] for r in entries} != set(WEBVIEW)):
        raise ValueError('location WebView dependency inventory differs')
    for row in entries:
        version, digest, dependency = WEBVIEW[row['name']]
        block = blocks[row['name']]
        if (row['version'] != version or row['archive_sha256'] != digest
                or row['dependency'] != dependency
                or row['archive_url'] != 'https://pub.dev/api/archives/' + row['name'] + '-' + version + '.tar.gz'
                or sha(block.encode()) != row['lock_block_sha256']
                or 'version: "' + version + '"' not in block or digest not in block
                or 'dependency: ' + ('"' + dependency + '"' if ' ' in dependency else dependency) not in block):
            raise ValueError('location WebView archive or lock identity differs')
    return entries


def native_source_contract(plan):
    contract = plan['native_source_snapshot']
    expected = {
        'app/android/app/src/main/AndroidManifest.xml',
        'app/android/app/src/main/kotlin/com/luoboduihua/imapp/LocationOnceBridge.kt',
        'app/android/app/src/main/kotlin/com/luoboduihua/imapp/MainActivity.kt',
        'app/ios/Runner/AppDelegate.swift', 'app/ios/Runner/Info.plist',
        'app/ios/Runner/LocationOnceBridge.swift', 'app/ios/Runner.xcodeproj/project.pbxproj',
    }
    entries = contract['files']
    if (contract['members'] != 7 or len(entries) != 7
            or {r['path'] for r in entries} != expected
            or contract['R1_manifest_sha256'] != 'e8795862c794e6feb2eadbc5bc15a37f654dc0c8db49188dab1329228c38a894'
            or contract['scene8_device_GPS_accepted'] is not False
            or contract['portable396_includes_native'] is not False):
        raise ValueError('native location source snapshot differs')
    metadata = json.dumps(sorted(entries, key=lambda r: r['path']), ensure_ascii=False,
                          sort_keys=True, separators=(',', ':')).encode()
    if sha(metadata) != contract['digest']:
        raise ValueError('native location source snapshot digest differs')
    return entries


def descriptor():
    plan = json.loads(regular(INPUTS))
    if (plan['schema'] != 'luobo.mobile.portable-ci-inputs.v20'
            or plan['current359']['members'] != 359
            or plan['sdk_portable']['members'] != 51
            or plan['portable_members'] != {'current359': 411}
            or plan['current359']['expected_target_tests'] != 24
            or plan['current359']['expected_full_tests'] != 1308
            or plan['current359']['target_paths'] != ANNOUNCEMENT_TEST_PATHS
            or any(key in plan for key in ('current230', 'candidate249', 'current257', 'current269', 'current282', 'current287', 'current291', 'current292', 'current294', 'current304', 'current309', 'current310', 'current316', 'current323', 'current331', 'current336', 'current344', 'current351', 'current354'))):
        raise ValueError('current359 schema v20 inventory differs; no historical aliases')
    reference = plan['reference_entry']
    if (reference['working_cohort'] != 'current359' or reference['members'] != 359
            or reference['default_enabled'] is not False
            or len(reference['replacement_files']) != 4
            or len({row['path'] for row in reference['replacement_files']}) != 4
            or reference['test_path'] != 'test/features/search/global_search_entry_test.dart'
            or reference['expected_visible_tests'] != 2
            or reference['expected_assertion_failures'] != 1
            or reference['expected_passed'] != 1 or reference['expected_child_exit'] != 1):
        raise ValueError('reference entry must be the explicit two-test full359 variant')
    native_source_contract(plan)
    announcement_contract(plan)
    attached_info_null_contract(plan)
    card_contract(plan)
    group_revoke_contract(plan)
    return plan


def load_sources(cohort):
    if cohort != 'current359':
        raise ValueError('schema v20 supports current359 only; historical cohorts are not aliases')
    plan = descriptor()
    selected = {}
    for row in plan['current359']['files']:
        name = row['path']
        if name in selected or not name.startswith('app/'):
            raise ValueError('duplicate or out-of-scope current source')
        selected[name] = check(regular(REPO / safe_name(name)), row)
    if len(selected) != 359 or source_digest(selected) != plan['current359']['source_digest']:
        raise ValueError('current359 aggregate differs')
    actual = set()
    for prefix in ('app/lib', 'app/test', 'app/tool'):
        for directory, dirs, names in os.walk(REPO / prefix, followlinks=False):
            for name in dirs:
                if (Path(directory) / name).is_symlink():
                    raise ValueError('authored-source directory symlink')
            for name in names:
                actual.add(str((Path(directory) / name).relative_to(REPO)))
    expected = {p for p in selected if p.startswith(('app/lib/', 'app/test/', 'app/tool/'))}
    if actual != expected:
        raise ValueError('unlocked app authored-source inventory')
    legacy = REPO / 'ci/mobile/candidate'
    if legacy.is_symlink() or (legacy.exists() and any(legacy.rglob('*'))):
        raise ValueError('legacy candidate body tree must be removed; history is available by commit')
    authored_digest = source_digest(selected)
    webview_dependency_contract(plan, selected['app/pubspec.lock'])
    for row in native_source_contract(plan):
        check(regular(REPO / safe_name(row['path'])), row)
    for row in [*plan['sdk_portable']['files'], plan['migration']]:
        name = row['path']
        if name in selected:
            raise ValueError('portable closure overlap')
        selected[name] = check(regular(REPO / safe_name(name)), row)
    if (len(selected) != plan['portable_members'][cohort]
            or source_digest(selected) != plan['portable_digests'][cohort]):
        raise ValueError('portable closure count or digest differs')
    reference_sources(plan, selected)  # Missing/wrong before pins fail even if reference execution is off.
    required_names(plan)  # Pinned successful names supplement count gates.
    return plan, selected, authored_digest


def reference_sources(plan, files):
    """Full411 portable closure; only the four named App source bodies differ."""
    result = dict(files)
    baseline = plan['reference_entry']
    for row in baseline['replacement_files']:
        name = str(safe_name(row['path']))
        if name not in result or not name.startswith('app/lib/'):
            raise ValueError('reference replacement outside original production files')
        result[name] = check(regular(REPO / baseline['baseline_prefix'] / name), row)
    authored = {name: body for name, body in result.items() if name.startswith('app/')}
    if len(authored) != 359 or source_digest(authored) != baseline['reference_source_digest']:
        raise ValueError('reference is not the exact full359 variant')
    return result



def required_names(plan):
    item = plan['required_test_names']
    if item['path'] != 'ci/mobile/required-test-names.json':
        raise ValueError('required test names path differs')
    value = json.loads(check(regular(REPO / safe_name(item['path'])), item))
    if value['schema'] != 'luobo.mobile.required-successful-names.v16' or value['cohort'] != 'current359':
        raise ValueError('required test names identity differs')
    for key, expected in [('parent_full922', 922), ('new_next_unread29', 29),
                          ('parent_target281', 281), ('parent_target303', 303), ('parent_target345', 345), ('parent_target386', 386), ('current_target422', 422), ('new_location_receive36', 36), ('parent_full1068', 1068), ('new_saved_emoji41', 41), ('parent_full1027', 1027),
                          ('parent_full985', 985), ('new_chat_background42', 42),
                          ('parent_full963', 963), ('new_chat_font22', 22), ('new_call_records50', 50),
                          ('new_meeting_entry4', 4), ('meeting_routes10', 10),
                          ('parent_full955', 955), ('parent_target273', 273),
                          ('new_booking_capability8', 8), ('parent_target422', 422),
                          ('parent_full1104', 1104), ('new_location_send38', 38),
                          ('current_target460', 460), ('parent_target460', 460),
                          ('parent_full1142', 1142), ('new_chat_card80', 80),
                          ('current_target540', 540), ('parent_full1222', 1222),
                          ('new_group_revoke58', 58), ('current_full1280', 1280),
                          ('new_announcement24', 24), ('current_full1304', 1304),
                          ('new_attached_info_null4', 4), ('current_full1308', 1308)]:
        entries = value[key]
        if (not isinstance(entries, list) or len({r['name'] for r in entries}) != len(entries)
                or any(not isinstance(r['name'], str) or not r['name']
                       or type(r['count']) is not int or r['count'] < 1 for r in entries)
                or sum(r['count'] for r in entries) != expected):
            raise ValueError('required names inventory differs: ' + key)
    if (value['parent_full955'] != sorted(value['parent_full922'] + value['new_next_unread29'] + value['new_meeting_entry4'], key=lambda row: row['name'])
            or value['parent_target281'] != sorted(value['parent_target273'] + value['new_booking_capability8'], key=lambda row: row['name'])):
        raise ValueError('booking capability names do not preserve exact parent names')
    if (value['parent_full963'] != sorted(value['parent_full955'] + value['new_booking_capability8'], key=lambda row: row['name'])
            or value['parent_target303'] != sorted(value['parent_target281'] + value['new_chat_font22'], key=lambda row: row['name'])):
        raise ValueError('font names do not preserve exact parent names')
    if (value['parent_full985'] != sorted(value['parent_full963'] + value['new_chat_font22'], key=lambda row: row['name'])
            or value['parent_target345'] != sorted(value['parent_target303'] + value['new_chat_background42'], key=lambda row: row['name'])):
        raise ValueError('background names do not preserve exact parent names')
    if (value['parent_full1027'] != sorted(value['parent_full985'] + value['new_chat_background42'], key=lambda row: row['name'])
            or value['parent_target386'] != sorted(value['parent_target345'] + value['new_saved_emoji41'], key=lambda row: row['name'])):
        raise ValueError('saved emoji names do not preserve exact parent names')
    if (value['parent_full1068'] != sorted(value['parent_full1027'] + value['new_saved_emoji41'], key=lambda row: row['name'])
            or value['current_target422'] != sorted(value['parent_target386'] + value['new_location_receive36'], key=lambda row: row['name'])):
        raise ValueError('location receive names do not preserve exact parent names')
    value['current_full1104'] = value['parent_full1068'] + value['new_location_receive36']
    if len({r['name'] for r in value['current_full1104']}) != len(value['current_full1104']):
        raise ValueError('new names overlap parent names')
    if (value['parent_target422'] != value['current_target422']
            or value['parent_full1104'] != sorted(value['current_full1104'], key=lambda r: r['name'])
            or value['current_target460'] != sorted(value['parent_target422'] + value['new_location_send38'], key=lambda r: r['name'])):
        raise ValueError('location send names do not preserve exact parent names')
    value['current_full1142'] = value['parent_full1104'] + value['new_location_send38']
    if len({r['name'] for r in value['current_full1142']}) != len(value['current_full1142']):
        raise ValueError('location send names overlap parent names')
    meeting_names = {row['name'] for row in value['meeting_routes10']}
    if (not {row['name'] for row in value['new_meeting_entry4']} <= meeting_names
            or not meeting_names <= {row['name'] for row in value['current_full1104']}):
        raise ValueError('meeting route names do not preserve parent plus new cases')
    if (value['parent_target460'] != value['current_target460']
            or value['parent_full1142'] != sorted(value['current_full1142'], key=lambda r: r['name'])
            or value['current_target540'] != sorted(value['parent_target460'] + value['new_chat_card80'], key=lambda r: r['name'])):
        raise ValueError('card names do not preserve exact parent names')
    value['current_full1222'] = value['parent_full1142'] + value['new_chat_card80']
    if len({r['name'] for r in value['current_full1222']}) != 1222:
        raise ValueError('card names overlap parent names')
    if (value['parent_full1222'] != sorted(value['current_full1222'], key=lambda r: r['name'])
            or value['current_full1280'] != sorted(value['parent_full1222'] + value['new_group_revoke58'], key=lambda r: r['name'])):
        raise ValueError('group revoke names do not preserve exact parent names')
    if value['current_full1304'] != sorted(value['current_full1280'] + value['new_announcement24'], key=lambda r: r['name']):
        raise ValueError('announcement names do not preserve exact parent names')
    announcement_contract(plan, value)
    attached_info_null_contract(plan, value)
    group_revoke_contract(plan, value)
    card_contract(plan, value)
    cleanup_contract(plan, value)
    next_unread_contract(plan, value)
    return value


def cleanup_contract(plan, names):
    contract = plan['call_records_cleanup']
    fixtures = contract['named_fixtures']
    repository = contract['repository_awaited_teardown_names']
    fixture_names = {row['name'] for row in fixtures}
    new_names = {row['name'] for row in names['new_call_records50']}
    normal = ['recorder.begin', 'recorder.end', 'sdk.begin', 'sdk.end', 'storage.end']
    patterns = {'unit': normal, 'page': normal + ['unmount.begin', 'unmount.end'],
                'expected_close_failure': ['recorder.begin', 'sdk.begin', 'sdk.end', 'storage.end']}
    if (contract['expected_fixture_instances'] != 35
            or contract['expected_successful_recorder_closes'] != 33
            or contract['expected_intentional_close_failures'] != 2
            or contract['expected_page_unmounts'] != 14
            or len(fixtures) != 35 or len(fixture_names) != 35 or len(repository) != 15
            or len(set(repository)) != 15 or fixture_names & set(repository)
            or fixture_names | set(repository) != new_names
            or sum(row['kind'] == 'page' for row in fixtures) != 14
            or sum(row['kind'] == 'expected_close_failure' for row in fixtures) != 2
            or any(row['kind'] not in patterns or row['markers'] != patterns[row['kind']] for row in fixtures)
            or contract['parent_creation_instances'] != 76
            or contract['parent_creation_markers'] != ['release.begin', 'native.drained', 'sdk.begin', 'sdk.end', 'unmount.begin', 'unmount.end']
            or contract['parent_safe_exit_markers'] != ['create.entered', 'signout.begin', 'signout.end', 'route.removed', 'create.released', 'create.returned']):
        raise ValueError('call records cleanup source contract differs')
    return contract


def next_unread_contract(plan, names):
    contract = plan['next_unread_cleanup']
    fixtures, units, geometry = (contract['fixture_names'], contract['unit_names'], contract['geometry_names'])
    union = fixtures + units + geometry
    if (len(fixtures) != 11 or len(units) != 16 or len(geometry) != 2
            or len(set(union)) != 29
            or set(union) != {row['name'] for row in names['new_next_unread29']}
            or contract['markers'] != ['sdk.begin', 'sdk.end', 'unmount.begin', 'unmount.end']):
        raise ValueError('next unread cleanup source contract differs')
    return contract


def announcement_contract(plan, names=None):
    contract = plan['announcement']
    selected = contract['selected_files']
    inventory = {r['path']: r for r in plan['current359']['files']}
    lineage = contract['lineage']
    if (contract['test_paths'] != ANNOUNCEMENT_TEST_PATHS
            or contract['analysis_paths'] != ANNOUNCEMENT_ANALYSIS_PATHS
            or len(selected) != 11 or len({r['path'] for r in selected}) != 11
            or {r['path'] for r in selected} != {'app/' + p for p in ANNOUNCEMENT_ANALYSIS_PATHS}
            or any(r != inventory.get(r['path']) for r in selected)
            or lineage['parent_cohort'] != 'current354'
            or lineage['parent_ref'] != 'ff7bc1f588f935d6315ad0211e68e15e58cd037e'
            or lineage['parent_source_digest'] != '113f8a3ff3cfaa3d17ba77b67cb28605a1220fa86359b8f0661535180075364c'
            or [r['before'] for r in lineage['replacements']] != ANNOUNCEMENT_BEFORE6
            or any(r['before']['path'] != r['after']['path'] or r['after'] != inventory.get(r['after']['path']) for r in lineage['replacements'])
            or lineage['new_paths'] != ANNOUNCEMENT_NEW5
            or contract['new_names'] != ANNOUNCEMENT_NAMES24
            or contract['widget_names'] != ANNOUNCEMENT_NAMES24[6:]
            or contract['unit_names'] != ANNOUNCEMENT_NAMES24[:6]
            or contract['widget_markers'] != ['sdk.begin', 'sdk.end', 'unmount.begin', 'unmount.end']
            or (names is not None and sorted(contract['new_names']) != sorted(r['name'] for r in names['new_announcement24']))):
        raise ValueError('announcement source/names/lineage/cleanup inventory differs')
    return contract



ATTACHED_INFO_NULL_PATHS = ['app/lib/core/privacy/message_privacy.dart', 'app/test/features/chat/message_privacy_test.dart']
ATTACHED_INFO_NULL_BEFORE2 = [
    {'path': 'app/lib/core/privacy/message_privacy.dart', 'bytes': 18768, 'sha256': 'a63582d594e96a3f16c6bde0e085ef4c67c5c2e7a23758b0a3f79d4c8396d00e'},
    {'path': 'app/test/features/chat/message_privacy_test.dart', 'bytes': 8160, 'sha256': 'b65542e5e27f3161e64d89d20ef8a3d6026818a65413593885880e34e6879eaf'},
]
ATTACHED_INFO_NULL_NAMES4 = [
    'encoded JSON null keeps ordinary SDK previews and forwarding available',
    'encoded null never masks modeled private metadata or real private descendants',
    'later encoded null cannot erase remembered private expiry or incomplete inspection',
    'all other encoded nonobjects malformed fields and bounded tree violations remain rejected',
]


def attached_info_null_contract(plan, names=None):
    contract = plan['attached_info_null']
    inventory = {row['path']: row for row in plan['current359']['files']}
    if (contract['before_files'] != ATTACHED_INFO_NULL_BEFORE2
            or len(contract['selected_files']) != 2
            or {row['path'] for row in contract['selected_files']} != set(ATTACHED_INFO_NULL_PATHS)
            or any(row != inventory.get(row['path']) for row in contract['selected_files'])
            or contract['new_names'] != ATTACHED_INFO_NULL_NAMES4
            or contract['expected_full_tests'] != 1308
            or contract['source_count_changed'] is not False
            or contract['native_acceptance_inherited'] is not False):
        raise ValueError('attachedInfo null source/name contract differs')
    if names is not None:
        if (names['new_attached_info_null4'] != sorted(
                [{'name': name, 'count': 1} for name in ATTACHED_INFO_NULL_NAMES4], key=lambda row: row['name'])
                or names['current_full1308'] != sorted(
                    names['current_full1304'] + names['new_attached_info_null4'], key=lambda row: row['name'])):
            raise ValueError('attachedInfo null names do not preserve exact historical1304 plus new4')
    return contract



def attached_info_null_parent_inventory(plan):
    """Restore only the explicit null2 delta before historical announcement354 reversal."""
    attached_info_null_contract(plan)
    inventory = {row['path']: row for row in plan['current359']['files']}
    for row in ATTACHED_INFO_NULL_BEFORE2:
        inventory[row['path']] = dict(row)
    encoded = json.dumps(sorted(inventory.values(), key=lambda row: row['path']),
                         ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode()
    if len(inventory) != 359 or sha(encoded) != '2e5a8afd7ee7370485954061b17be776c1da6162323807b48909ae2030477899':
        raise ValueError('attachedInfo historical current359 parent differs')
    return inventory


def announcement_parent_inventory(plan):
    """Reverse only the accepted eleven-file delta for historical351/354 contracts."""
    contract = announcement_contract(plan)
    inventory = attached_info_null_parent_inventory(plan)
    for replacement in contract['lineage']['replacements']:
        before = replacement['before']
        inventory[before['path']] = before
    for name in contract['lineage']['new_paths']:
        del inventory[name]
    digest = sha(json.dumps(sorted(inventory.values(), key=lambda r: r['path']),
                            ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode())
    if len(inventory) != 354 or digest != contract['lineage']['parent_source_digest']:
        raise ValueError('announcement historical current354 parent differs')
    return inventory


def card_contract(plan, names=None):
    contract = plan['chat_card']
    selected = contract['selected_files']
    inventory = announcement_parent_inventory(plan)
    lineage = contract['current_source_lineage']
    replacements = lineage['replacements']
    if (lineage['historical_cohort'] != 'current351'
            or lineage['historical_ref'] != '50ff7d3d705c52775bf37840e6bef10fa2a55ae0'
            or lineage['historical_source_digest'] != 'fc2644ca9333596900be65f95fe0250f2d2106bb8429bc8f2f595c501bb2aba6'
            or lineage['product_manifest_sha256'] != plan['group_revoke']['product_manifest_sha256']
            or len(replacements) != 3
            or [r['before'] for r in replacements] != CARD_REVOKE_BEFORE3
            or any(r['before']['path'] != r['after']['path'] or r['after'] != inventory.get(r['after']['path']) for r in replacements)):
        raise ValueError('card source/analysis/cleanup inventory lineage differs')
    historical = dict(inventory)
    historical.update({r['before']['path']: r['before'] for r in replacements})
    if (contract['test_paths'] != CARD_TEST_PATHS or contract['analysis_paths'] != CARD_ANALYSIS_PATHS
            or type(contract['expected_focused_tests']) is not int or contract['expected_focused_tests'] != 80
            or len(selected) != 17 or len({r['path'] for r in selected}) != 17
            or {r['path'] for r in selected} != {'app/' + p for p in CARD_ANALYSIS_PATHS}
            or any(r != historical.get(r['path']) for r in selected)
            or contract['widget_markers'] != ['sdk.begin', 'sdk.end', 'unmount.begin', 'unmount.end']
            or contract['repository_markers'] != ['sdk.begin', 'sdk.end', 'storage.begin', 'storage.end']):
        raise ValueError('card source/analysis/cleanup inventory differs')
    groups = [contract['model_names'], contract['repository_names'], contract['widget_names']]
    union = [n for group in groups for n in group]
    if (list(map(len, groups)) != [7, 32, 41] or len(set(union)) != 80
            or any(not isinstance(n, str) or not n for n in union)
            or (names is not None and sorted(union) != sorted(r['name'] for r in names['new_chat_card80']))):
        raise ValueError('card exact successful names/cleanup ownership differs')
    return contract



def group_revoke_contract(plan, names=None):
    contract = plan['group_revoke']
    selected = contract['selected_files']
    inventory = announcement_parent_inventory(plan)
    groups = [contract['repository_names'], contract['widget_names']]
    union = [name for group in groups for name in group]
    if (contract['product_manifest_sha256'] != '88f56b2c18547415c3001e3693d0f42095b1c7b0d74005a3b4799906565cf60e'
            or contract['test_paths'] != GROUP_REVOKE_TEST_PATHS
            or contract['analysis_paths'] != GROUP_REVOKE_ANALYSIS_PATHS
            or type(contract['expected_focused_tests']) is not int or contract['expected_focused_tests'] != 58
            or len(selected) != 6 or len({r['path'] for r in selected}) != 6
            or {r['path'] for r in selected} != {'app/' + p for p in GROUP_REVOKE_ANALYSIS_PATHS}
            or any(r != inventory.get(r['path']) for r in selected)
            or contract['widget_markers'] != ['sdk.begin', 'sdk.end', 'unmount.begin', 'unmount.end']
            or contract['repository_markers'] != ['sdk.begin', 'sdk.end', 'storage.begin', 'storage.end']
            or list(map(len, groups)) != [40, 18] or len(set(union)) != 58
            or any(not isinstance(n, str) or not n for n in union)
            or (names is not None and sorted(union) != sorted(r['name'] for r in names['new_group_revoke58']))):
        raise ValueError('group revoke source/names/analysis/cleanup inventory differs')
    return contract


def materialize(output, cohort):
    # Initial admission precedes reading the full source inventory.
    if (not output.is_absolute() or output.resolve() != output or output.exists()
            or output.is_symlink() or output.is_relative_to(REPO)
            or not output.parent.is_dir()):
        raise ValueError('new absolute output outside checkout required')
    if os.statvfs(output.parent).f_bavail * os.statvfs(output.parent).f_frsize < INITIAL:
        raise ValueError('initial output free space must be at least 4GiB')
    plan, files, digest = load_sources(cohort)
    output.mkdir(mode=0o700)
    for name, body in files.items():
        if os.statvfs(output).f_bavail * os.statvfs(output).f_frsize < FLOOR:
            raise ValueError('partial copy retained: 1GiB floor')
        target = output / safe_name(name)
        target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        with target.open('xb') as stream:
            stream.write(body)
    result = {'cohort': cohort, 'authored_source_digest': digest,
              'portable_members': len(files), 'portable_digest': source_digest(files),
              'input_manifest_sha256': sha(regular(INPUTS)),
              'old_generated_cache_files_copied': 0, 'native_acceptance': False,
              'native_source_snapshot_members': 7,
              'native_source_snapshot_digest': plan['native_source_snapshot']['digest']}
    return result


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--cohort', choices=('current359',), required=True)
    p.add_argument('--output', type=Path, help='explicit fresh output; omitted means readonly')
    args = p.parse_args()
    if args.output:
        result = materialize(args.output, args.cohort)
    else:
        _, files, digest = load_sources(args.cohort)
        result = {'state': 'verified_readonly', 'cohort': args.cohort,
                  'authored_source_digest': digest, 'portable_members': len(files),
                  'portable_digest': source_digest(files)}
    print(json.dumps(result, sort_keys=True))


if __name__ == '__main__':
    main()
