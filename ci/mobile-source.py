#!/usr/bin/env python3
"""Verify minimal pinned source; optionally copy it to a new CI-only tree.

No local task archive, mutable reference directory, native SDK binary or old
generated cache is an input. The candidate is never applied to the checkout.
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
    if (plan['schema'] != 'luobo.mobile.portable-ci-inputs.v1'
            or plan['current230']['members'] != 230
            or plan['candidate249']['members'] != 249
            or plan['sdk_portable']['members'] != 51):
        raise ValueError('portable input inventory differs')
    return plan


def load_sources(cohort):
    plan = descriptor()
    base = {}
    for row in plan['current230']['files']:
        name = row['path']
        if name in base or not name.startswith('app/'):
            raise ValueError('duplicate or out-of-scope current source')
        base[name] = check(regular(REPO / safe_name(name)), row)
    if len(base) != 230 or source_digest(base) != plan['current230']['source_digest']:
        raise ValueError('current230 aggregate differs')
    # Do not let an unpinned extra Dart file quietly enter analysis or full test.
    actual = set()
    for prefix in ('app/lib', 'app/test', 'app/tool'):
        for directory, dirs, names in os.walk(REPO / prefix, followlinks=False):
            for name in dirs:
                if (Path(directory) / name).is_symlink():
                    raise ValueError('authored-source directory symlink')
            for name in names:
                actual.add(str((Path(directory) / name).relative_to(REPO)))
    if actual != {p for p in base if p.startswith(('app/lib/', 'app/test/', 'app/tool/'))}:
        raise ValueError('unlocked app authored-source inventory')

    selected = dict(base)
    if cohort == 'booking-custom249':
        overlay = plan['candidate249']['overlay']
        before = plan['candidate249']['before']
        if len(overlay) != 22 or len(before) != 3:
            raise ValueError('exact22 overlay/3 replacements required')
        expected = {r['path'] for r in overlay}
        root = REPO / 'ci/mobile/candidate'
        actual = {str(p.relative_to(root)) for p in root.rglob('*') if p.is_file()}
        if actual != expected:
            raise ValueError('candidate overlay missing or extra file')
        for row in overlay:
            name = row['path']
            if name in base:
                if name not in before:
                    raise ValueError('unapproved replacement')
                check(base[name], {'path': name, **before[name]})
            elif name in before:
                raise ValueError('missing replacement base')
            selected[name] = check(regular(root / safe_name(name)), row)
        if (len(selected) != 249
                or source_digest(selected) != plan['candidate249']['source_digest']):
            raise ValueError('candidate249 aggregate differs')

    authored_digest = source_digest(selected)
    for row in [*plan['sdk_portable']['files'], plan['migration']]:
        name = row['path']
        if name in selected:
            raise ValueError('portable closure overlap')
        selected[name] = check(regular(REPO / safe_name(name)), row)
    if len(selected) != plan['portable_members'][cohort]:
        raise ValueError('portable closure count differs')
    return plan, selected, authored_digest


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
    p.add_argument('--cohort', choices=('current230', 'booking-custom249'), required=True)
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
