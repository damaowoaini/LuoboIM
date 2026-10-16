#!/usr/bin/env python3
"""Verify minimal pinned source; optionally copy it to a new CI-only tree.

No local task archive, mutable reference directory, native SDK binary or old
generated cache is an input. Current323 must already be adopted in the checkout.
The optional reference is full323 with exactly four pinned historical search baseline files.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import stat

REPO = Path(__file__).resolve().parent.parent
INPUTS = REPO / 'ci/mobile/inputs.json'
FLOOR = 1024 ** 3
INITIAL = 4 * FLOOR


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


def descriptor():
    plan = json.loads(regular(INPUTS))
    if (plan['schema'] != 'luobo.mobile.portable-ci-inputs.v13'
            or plan['current323']['members'] != 323
            or plan['sdk_portable']['members'] != 51
            or plan['portable_members'] != {'current323': 375}
            or plan['current323']['expected_target_tests'] != 345
            or plan['current323']['expected_full_tests'] != 1027
            or plan['current323']['target_paths'] != ['test/features/qr', 'test/features/search', 'test/features/group/create_group_entry_test.dart', 'test/features/group/create_group_repository_test.dart', 'test/features/group/create_group_workflow_test.dart', 'test/features/group/group_creation_entry_test.dart', 'test/features/group/group_creation_entry_workflow_test.dart', 'test/features/group/chat_cid_route_test.dart', 'test/features/conversation/contact_shortcuts_test.dart', 'test/features/rtc/call_records_repository_test.dart', 'test/features/rtc/call_history_pipeline_test.dart', 'test/features/rtc/call_records_page_test.dart', 'test/features/rtc/call_history_lifetime_test.dart', 'test/features/conversation/next_unread_repository_test.dart', 'test/features/conversation/next_unread_controller_test.dart', 'test/features/conversation/next_unread_page_test.dart', 'test/features/meeting/meeting_routes_test.dart', 'test/features/meeting/booking/booking_capability_discovery_test.dart', 'test/features/chat/chat_reading_preferences_test.dart', 'test/features/chat/chat_font_size_page_test.dart', 'test/features/chat/chat_font_entry_test.dart', 'test/features/chat/chat_background_store_test.dart', 'test/features/chat/chat_background_page_test.dart', 'test/features/chat/chat_background_entry_test.dart', 'test/features/chat/chat_background_recovery_test.dart']
            or any(key in plan for key in ('current230', 'candidate249', 'current257', 'current269', 'current282', 'current287', 'current291', 'current292', 'current294', 'current304', 'current309', 'current310', 'current316'))):
        raise ValueError('current323 schema v13 inventory differs; no historical aliases')
    reference = plan['reference_entry']
    if (reference['working_cohort'] != 'current323' or reference['members'] != 323
            or reference['default_enabled'] is not False
            or len(reference['replacement_files']) != 4
            or len({row['path'] for row in reference['replacement_files']}) != 4
            or reference['test_path'] != 'test/features/search/global_search_entry_test.dart'
            or reference['expected_visible_tests'] != 2
            or reference['expected_assertion_failures'] != 1
            or reference['expected_passed'] != 1 or reference['expected_child_exit'] != 1):
        raise ValueError('reference entry must be the explicit two-test full323 variant')
    return plan


def load_sources(cohort):
    if cohort != 'current323':
        raise ValueError('schema v13 supports current323 only; historical cohorts are not aliases')
    plan = descriptor()
    selected = {}
    for row in plan['current323']['files']:
        name = row['path']
        if name in selected or not name.startswith('app/'):
            raise ValueError('duplicate or out-of-scope current source')
        selected[name] = check(regular(REPO / safe_name(name)), row)
    if len(selected) != 323 or source_digest(selected) != plan['current323']['source_digest']:
        raise ValueError('current323 aggregate differs')
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
    """Full375 portable closure; only the four named App source bodies differ."""
    result = dict(files)
    baseline = plan['reference_entry']
    for row in baseline['replacement_files']:
        name = str(safe_name(row['path']))
        if name not in result or not name.startswith('app/lib/'):
            raise ValueError('reference replacement outside original production files')
        result[name] = check(regular(REPO / baseline['baseline_prefix'] / name), row)
    authored = {name: body for name, body in result.items() if name.startswith('app/')}
    if len(authored) != 323 or source_digest(authored) != baseline['reference_source_digest']:
        raise ValueError('reference is not the exact full323 variant')
    return result



def required_names(plan):
    item = plan['required_test_names']
    if item['path'] != 'ci/mobile/required-test-names.json':
        raise ValueError('required test names path differs')
    value = json.loads(check(regular(REPO / safe_name(item['path'])), item))
    if value['schema'] != 'luobo.mobile.required-successful-names.v9' or value['cohort'] != 'current323':
        raise ValueError('required test names identity differs')
    for key, expected in [('parent_full922', 922), ('new_next_unread29', 29),
                          ('parent_target281', 281), ('parent_target303', 303), ('current_target345', 345),
                          ('parent_full985', 985), ('new_chat_background42', 42),
                          ('parent_full963', 963), ('new_chat_font22', 22), ('new_call_records50', 50),
                          ('new_meeting_entry4', 4), ('meeting_routes10', 10),
                          ('parent_full955', 955), ('parent_target273', 273),
                          ('new_booking_capability8', 8)]:
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
            or value['current_target345'] != sorted(value['parent_target303'] + value['new_chat_background42'], key=lambda row: row['name'])):
        raise ValueError('background names do not preserve exact parent names')
    value['current_full1027'] = value['parent_full985'] + value['new_chat_background42']
    if len({r['name'] for r in value['current_full1027']}) != len(value['current_full1027']):
        raise ValueError('new names overlap parent names')
    meeting_names = {row['name'] for row in value['meeting_routes10']}
    if (not {row['name'] for row in value['new_meeting_entry4']} <= meeting_names
            or not meeting_names <= {row['name'] for row in value['current_full1027']}):
        raise ValueError('meeting route names do not preserve parent plus new cases')
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
              'old_generated_cache_files_copied': 0, 'native_acceptance': False}
    return result


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--cohort', choices=('current323',), required=True)
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
