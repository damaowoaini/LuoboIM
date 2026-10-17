#!/usr/bin/env python3
"""Current359 input/engine audit only. No acquisition, Go, build or signing entry."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path, PurePosixPath
import stat
import struct

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
IDENTITY = 'current359-null2-apple-source-consumer-v2'
REF = '0e1cb1ae5f3edc3db8a0a94970b363a4cd6ae3b1'
SOURCE = '633e257bcc0c161eb9ad109b61ca689acfcf5fc454472d02f38b942b69f0d199'
PORTABLE = '66f60a7c2f93618b0f2eff179e545694f5978cdbd19ae8cd223f86d1333fa8cb'
FILES = '1f3cb96a3b21e4fa5b66cab4eef5c2dbbfb76cba4ecb1df9a22c72c6800c7857'


def digest(body):
    return hashlib.sha256(body).hexdigest()


def safe_name(text):
    p = PurePosixPath(text)
    if not text or p.is_absolute() or str(p) != text or any(x in {'', '.', '..'} for x in text.split('/')) or '\\' in text:
        raise ValueError('unsafe relative input')
    return text


def read_file(root, row, cap=128*1024**2):
    root = Path(root)
    if root.resolve(strict=True) != root or not root.is_dir():
        raise ValueError('canonical input root required')
    p = root
    for part in safe_name(row['path']).split('/'):
        p = p/part
        if p.is_symlink():
            raise ValueError('input symlink refused')
    with os.fdopen(os.open(p, os.O_RDONLY | os.O_NOFOLLOW), 'rb') as f:
        before = os.fstat(f.fileno())
        if not stat.S_ISREG(before.st_mode) or before.st_nlink != 1 or not 0 <= before.st_size <= cap:
            raise ValueError('bounded single-link input required')
        body = f.read(cap+1)
        after = os.fstat(f.fileno())
    identity = lambda s: (s.st_dev, s.st_ino, s.st_mode, s.st_nlink, s.st_size, s.st_mtime_ns, s.st_ctime_ns)
    if identity(before) != identity(after) or identity(after) != identity(p.lstat()):
        raise ValueError('input changed during read')
    if len(body) != row['bytes'] or digest(body) != row['sha256']:
        raise ValueError('fixed input bytes differ')
    return body


def checked_identity(spec):
    if (spec.get('schema') != 'luobo.apple.current359.consumer-inputs.v1'
            or spec.get('identity') != IDENTITY or spec.get('source_ref') != REF
            or spec.get('business_members') != 359 or spec.get('source_digest') != SOURCE
            or spec.get('locked_packages') != 178 or spec.get('portable_members') != 411
            or spec.get('portable_digest') != PORTABLE or spec.get('files_digest') != FILES):
        raise ValueError('explicit current359 source identity required')
    rows = spec['business_files']
    actual = digest(json.dumps(sorted(rows, key=lambda x: x['path']), ensure_ascii=False,
                               sort_keys=True, separators=(',', ':')).encode())
    if len(rows) != 359 or len({r['path'] for r in rows}) != 359 or actual != SOURCE:
        raise ValueError('current359 source vector differs')
    if any(r['path'] == 'app/ios/Podfile.lock' for r in spec['files']):
        raise ValueError('historical two-pod lock cannot be an active App dependency lock')
    all_rows = spec['files']
    if (len(all_rows) != 477 or len({r['path'] for r in all_rows}) != 477
            or any(set(r) != {'path', 'bytes', 'sha256'} or type(r['bytes']) is not int
                   or r['bytes'] < 0 for r in all_rows)
            or digest(''.join(r['path']+'\0'+r['sha256']+'\n' for r in sorted(all_rows, key=lambda r: r['path'])).encode()) != FILES):
        raise ValueError('exact current359 source477 union required')
    indexed = {r['path']: r for r in all_rows}
    paths = spec['portable_paths']
    if (len(paths) != 411 or len(set(paths)) != 411
            or not {r['path'] for r in rows} <= set(paths)
            or any(indexed.get(r['path']) != r for r in rows)
            or any(name not in indexed for name in paths)
            or digest(json.dumps(sorted([indexed[name] for name in paths], key=lambda r: r['path']),
                                 ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode()) != PORTABLE):
        raise ValueError('exact current359 portable411 closure required')
    return spec


def verify_source(root, spec):
    checked_identity(spec)
    for row in spec['files']:
        read_file(root, row)
    return {'identity': IDENTITY, 'members': len(spec['files']), 'business_members': 359,
            'source_ref': REF, 'source_digest': SOURCE, 'App_build_executed': False}


def arm64_image(body, kind, binary_audit):
    """Keep the full original engine byte identity; inspect its actual ARM64 slice."""
    if body[:4] in {bytes.fromhex('cafebabe'), bytes.fromhex('cafebabf')}:
        wide = body[:4] == bytes.fromhex('cafebabf')
        if len(body) < 8:
            raise ValueError('fat engine header truncated')
        count = struct.unpack_from('>I', body, 4)[0]
        stride = 32 if wide else 20
        if count != 2 or len(body) < 8+stride*count or kind != 'simulator':
            raise ValueError('only original two-architecture simulator engine fat image allowed')
        slices, seen = [], set()
        for i in range(count):
            values = struct.unpack_from('>IIQQII' if wide else '>IIIII', body, 8+i*stride)
            cpu, start, size = values[0], values[2], values[3]
            if cpu not in {0x1000007, 0x100000c} or cpu in seen or start < 8+stride*count or size < 32 or start+size > len(body):
                raise ValueError('engine fat slice identity/boundary')
            seen.add(cpu); slices.append((start, start+size, cpu))
        order = sorted(slices)
        if order[0][1] > order[1][0] or seen != {0x1000007, 0x100000c}:
            raise ValueError('overlapping or unknown engine slices')
        start, end, _ = next(s for s in slices if s[2] == 0x100000c)
        selected = body[start:end]
    else:
        if kind != 'device':
            raise ValueError('fixed simulator engine must preserve original fat identity')
        selected = body
    result = binary_audit.macho(selected, kind, binary_audit.MH_DYLIB)
    return {'architecture': 'arm64', 'original_bytes': len(body), 'original_sha256': digest(body),
            'selected_slice_bytes': len(selected), **result}


def audit_engine(root, spec):
    helper = spec['binary_audit_helper']
    body = read_file(REPO, helper)
    loader = importlib.util.spec_from_file_location('accepted_core_binary', REPO/helper['path'])
    binary = importlib.util.module_from_spec(loader); loader.loader.exec_module(binary)
    result = {}
    for kind, row in spec['engine_binaries'].items():
        result[kind] = arm64_image(read_file(root, row), kind, binary)
    return {'engine': spec['engine_revision'], 'helper_sha256': digest(body), 'actual_arm64_images': result,
            'source_and_engine_audit_only': True, 'App_compile_or_link_executed': False}


def verify_plugin_graph(generated, plugins, roots):
    """Validate a fresh Flutter-produced graph; never copy the stale local graph."""
    expected = {p['name']: p for p in plugins['implementations']}
    rows = generated.get('plugins', {}).get('ios', [])
    if len(rows) != len(expected) or {r.get('name') for r in rows} != set(expected):
        raise ValueError('current178 iOS implementation set differs')
    for actual in rows:
        wanted = expected[actual['name']]
        root = Path(roots[actual['name']])
        if (not root.is_absolute() or root.resolve(strict=True) != root or not root.is_dir()
                or Path(actual['path']) != root or Path(actual['path']).is_symlink()
                or actual.get('native_build') is not wanted['native_build']
                or bool(actual.get('shared_darwin_source', False)) != wanted['shared_darwin_source']
                or actual.get('dev_dependency') is not False
                or sorted(actual.get('dependencies', [])) != wanted['dependencies']):
            raise ValueError('current iOS plugin path/role/dependency differs')
    return {'implementations': len(rows), 'native': sum(p['native_build'] for p in expected.values()),
            'native_Pod_or_SPM_resolution_accepted': False}


def packaging_plan(spec):
    checked_identity(spec)
    return {'identity': IDENTITY, 'source_ref': REF, 'unsigned_bundle_id': 'com.luoboduihua.imapp',
            'deployment_target': '15.0', 'architectures': ['device/arm64', 'simulator/arm64'],
            'ready_for_App_build': False, 'blockers': spec['packaging_blockers'],
            'legacy_Framework_fallback_allowed': False, 'Go_rebind': False,
            'signing_device_service_acceptance': False}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source-root', type=Path)
    parser.add_argument('--engine-root', type=Path)
    args = parser.parse_args()
    spec = checked_identity(json.loads((HERE/'inputs.json').read_bytes()))
    result = {'packaging': packaging_plan(spec)}
    if args.source_root is not None:
        result['source'] = verify_source(args.source_root, spec)
    if args.engine_root is not None:
        result['engine'] = audit_engine(args.engine_root, spec)
    print(json.dumps(result, indent=2))


if __name__ == '__main__':
    main()
