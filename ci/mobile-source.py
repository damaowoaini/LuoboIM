#!/usr/bin/env python3
"""Verify minimal pinned source; optionally copy it to a new CI-only tree.

No local task archive, mutable reference directory, native SDK binary or old
generated cache is an input. Current294 must already be adopted in the checkout.
The optional reference is full294 with exactly four pinned historical search baseline files.
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
    if (plan['schema'] != 'luobo.mobile.portable-ci-inputs.v8'
            or plan['current294']['members'] != 294
            or plan['sdk_portable']['members'] != 51
            or plan['portable_members'] != {'current294': 346}
            or plan['current294']['expected_target_tests'] != 184
            or plan['current294']['expected_full_tests'] != 872
            or plan['current294']['target_paths'] != ['test/features/qr', 'test/features/search', 'test/features/group/create_group_entry_test.dart', 'test/features/group/create_group_repository_test.dart', 'test/features/group/create_group_workflow_test.dart', 'test/features/group/group_creation_entry_test.dart', 'test/features/group/group_creation_entry_workflow_test.dart', 'test/features/group/chat_cid_route_test.dart', 'test/features/conversation/contact_shortcuts_test.dart']
            or any(key in plan for key in ('current230', 'candidate249', 'current257', 'current269', 'current282', 'current287', 'current291', 'current292'))):
        raise ValueError('current294 schema v8 inventory differs; no historical aliases')
    reference = plan['reference_entry']
    if (reference['working_cohort'] != 'current294' or reference['members'] != 294
            or reference['default_enabled'] is not False
            or len(reference['replacement_files']) != 4
            or len({row['path'] for row in reference['replacement_files']}) != 4
            or reference['test_path'] != 'test/features/search/global_search_entry_test.dart'
            or reference['expected_visible_tests'] != 2
            or reference['expected_assertion_failures'] != 1
            or reference['expected_passed'] != 1 or reference['expected_child_exit'] != 1):
        raise ValueError('reference entry must be the explicit two-test full294 variant')
    return plan


def load_sources(cohort):
    if cohort != 'current294':
        raise ValueError('schema v8 supports current294 only; historical cohorts are not aliases')
    plan = descriptor()
    selected = {}
    for row in plan['current294']['files']:
        name = row['path']
        if name in selected or not name.startswith('app/'):
            raise ValueError('duplicate or out-of-scope current source')
        selected[name] = check(regular(REPO / safe_name(name)), row)
    if len(selected) != 294 or source_digest(selected) != plan['current294']['source_digest']:
        raise ValueError('current294 aggregate differs')
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
    """Full346 portable closure; only the four named App source bodies differ."""
    result = dict(files)
    baseline = plan['reference_entry']
    for row in baseline['replacement_files']:
        name = str(safe_name(row['path']))
        if name not in result or not name.startswith('app/lib/'):
            raise ValueError('reference replacement outside original production files')
        result[name] = check(regular(REPO / baseline['baseline_prefix'] / name), row)
    authored = {name: body for name, body in result.items() if name.startswith('app/')}
    if len(authored) != 294 or source_digest(authored) != baseline['reference_source_digest']:
        raise ValueError('reference is not the exact full294 variant')
    return result



def required_names(plan):
    item = plan['required_test_names']
    if item['path'] != 'ci/mobile/required-test-names.json':
        raise ValueError('required test names path differs')
    value = json.loads(check(regular(REPO / safe_name(item['path'])), item))
    if value['schema'] != 'luobo.mobile.required-successful-names.v4' or value['cohort'] != 'current294':
        raise ValueError('required test names identity differs')
    for key, expected in [('parent_full854', 854), ('new_shortcuts18', 18), ('current_target184', 184)]:
        rows = value[key]
        if (not isinstance(rows, list) or len({r['name'] for r in rows}) != len(rows)
                or any(not isinstance(r['name'], str) or not r['name']
                       or type(r['count']) is not int or r['count'] < 1 for r in rows)
                or sum(r['count'] for r in rows) != expected):
            raise ValueError('required names inventory differs: ' + key)
    value['current_full872'] = value['parent_full854'] + value['new_shortcuts18']
    if len({r['name'] for r in value['current_full872']}) != len(value['current_full872']):
        raise ValueError('new names overlap parent names')
    return value


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
    p.add_argument('--cohort', choices=('current294',), required=True)
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
