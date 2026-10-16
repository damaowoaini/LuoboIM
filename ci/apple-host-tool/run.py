#!/usr/bin/env python3
"""Manual host-tool evidence only. No products, Go, engine, native build or link."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import plistlib
import signal
import stat
import subprocess
import sys
from types import SimpleNamespace

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
IDENTITY = 'apple-r9-xcode164-host-tool-proof-v1'


def load(path, name):
    spec = importlib.util.spec_from_file_location(name, path)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


def fingerprint(path, cap, hardlink=False):
    path = Path(path)
    before = path.lstat()
    if (path.resolve(strict=True) != path or not stat.S_ISREG(before.st_mode)
            or (before.st_nlink != 1 and not hardlink) or before.st_size > cap):
        raise ValueError('canonical bounded regular host input required')
    fields = lambda x: (x.st_dev, x.st_ino, x.st_mode, x.st_uid, x.st_gid, x.st_nlink,
                        x.st_size, x.st_mtime_ns, x.st_ctime_ns)
    with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW), 'rb') as stream:
        if fields(os.fstat(stream.fileno())) != fields(before):
            raise ValueError('host input changed before read')
        digest, count = hashlib.sha256(), 0
        for body in iter(lambda: stream.read(min(1024**2, cap-count+1)), b''):
            count += len(body)
            if count > cap:
                raise ValueError('host input exceeded cap during read')
            digest.update(body)
        if fields(os.fstat(stream.fileno())) != fields(before):
            raise ValueError('host input changed during read')
    if fields(path.lstat()) != fields(before):
        raise ValueError('host path changed after read')
    return {'path': str(path), 'bytes': before.st_size, 'sha256': digest.hexdigest(),
            'identity': list(fields(before)), 'nlink': before.st_nlink,
            'system_hardlink_exception': hardlink}


def metadata():
    spec = json.loads((HERE/'inputs.json').read_bytes())
    if spec['identity'] != IDENTITY:
        raise ValueError('explicit host-tool proof identity required')
    for row in spec['source_files'] + spec['borrowed']:
        p = REPO/row['path']
        actual = fingerprint(p, 1024**2)
        if actual['bytes'] != row['bytes'] or actual['sha256'] != row['sha256']:
            raise ValueError('host proof source differs')
    return spec


def native_environment(root, developer):
    return {'HOME': os.environ['HOME'], 'PATH': '/usr/bin:/bin', 'LANG': 'en_US.UTF-8',
            'TMPDIR': str(root/'tmp'), 'DEVELOPER_DIR': developer,
            'CLANG_MODULE_CACHE_PATH': str(root/'cache'), 'PYTHONDONTWRITEBYTECODE': '1'}


def strict_version(code, stdout, stderr, expected):
    if code != 0 or stderr or stdout.decode('utf-8').strip() != expected:
        raise ValueError('exact clean version query required')


def probe(root):
    spec = metadata()
    custody = load(REPO/'ci/apple-sdk-link/custody.py', 'host_tool_custody')
    descriptor_sha = custody.sha(HERE/'inputs.json')
    activation = json.loads((root/'activation.json').read_bytes())
    if activation['identity'] != IDENTITY or activation['inputs_sha256'] != descriptor_sha:
        raise ValueError('exact activation descriptor required')
    developer = Path(spec['developer'])
    tool = Path(spec['tool'])
    if tool != developer/'usr/bin/xcodebuild' or developer.resolve(strict=True) != developer:
        raise ValueError('fixed real Developer tool required')
    cap = spec['limits']['host_member_bytes']
    paths = [tool, developer.parent/'Info.plist', developer.parent/'version.plist',
             Path(spec['shim']['path']), Path('/usr/bin/codesign')]
    # Only this fixed Apple-signed Developer tool joins the existing two
    # system-tool exceptions. Repository sources still require one hard link.
    official_tools = {tool, Path(spec['shim']['path']), Path('/usr/bin/codesign')}
    before = [fingerprint(p, cap, p in official_tools) for p in paths]
    shim = next(row for row in before if row['path'] == spec['shim']['path'])
    if any(shim[k] != spec['shim'][k] for k in ['bytes', 'sha256']):
        raise ValueError('original shim source differs')
    # These are independently observed public Apple bundle metadata; no local
    # different-Xcode tool hash is substituted for this future cloud input.
    info = plistlib.loads((developer.parent/'Info.plist').read_bytes())
    version = plistlib.loads((developer.parent/'version.plist').read_bytes())
    if (info.get('CFBundleIdentifier') != spec['bundle_identifier']
            or info.get('CFBundleShortVersionString') != spec['bundle_version']
            or version.get('CFBundleShortVersionString') != spec['bundle_version']
            or version.get('ProductBuildVersion') != spec['bundle_build']):
        raise ValueError('fixed Apple Xcode bundle metadata required')
    custody.record(root/'evidence/host-source-before.json', {'files': before,
        'host': {'os': platform.platform(), 'python': sys.version,
                 'ImageOS': os.environ.get('ImageOS'), 'ImageVersion': os.environ.get('ImageVersion')},
        'bundle': {k: info.get(k) for k in ['CFBundleIdentifier', 'CFBundleShortVersionString', 'DTXcodeBuild']},
        'version': {k: version.get(k) for k in ['CFBundleShortVersionString', 'ProductBuildVersion']}})
    env = native_environment(root, spec['developer'])

    def command(name, argv):
        custody.record(root/'evidence'/(name+'.intent.json'), {'argv': list(map(str, argv)), 'cwd': str(root)})
        out, err = root/'evidence'/(name+'.stdout.log'), root/'evidence'/(name+'.stderr.log')
        flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW
        with os.fdopen(os.open(out, flags, 0o600), 'wb') as stdout, os.fdopen(os.open(err, flags, 0o600), 'wb') as stderr:
            code = subprocess.call(list(map(str, argv)), cwd=root, env=env, stdout=stdout, stderr=stderr)
            stdout.flush(); os.fsync(stdout.fileno()); stderr.flush(); os.fsync(stderr.fileno())
        custody.sync(out.parent)
        custody.record(root/'evidence'/(name+'.result.json'), {'exit_code': code})
        if max(out.stat().st_size, err.stat().st_size) > spec['limits']['log_member_bytes']:
            raise ValueError('bounded tool output required')
        return code, out.read_bytes(), err.read_bytes()

    # A requirement is verified, not merely parsed from untrusted display text.
    code, _, _ = command('apple-signature-verify', ['/usr/bin/codesign', '--verify', '--strict', '-R', 'anchor apple', tool])
    if code != 0:
        raise ValueError('Apple anchored tool signature required')
    code, _, _ = command('apple-signature-display', ['/usr/bin/codesign', '--display', '--verbose=4', tool])
    if code != 0:
        raise ValueError('public Apple signature evidence required')
    # Keep the original shim as a diagnostic control, never relabel its cache
    # errors as success and never filter either version stream.
    control = command('original-shim-version', [spec['shim']['path'], '-version'])
    custody.record(root/'evidence/shim-control.json', {'exit_code': control[0],
        'strict_clean_match': control[0] == 0 and not control[2] and control[1].decode('utf-8').strip() == spec['exact_version']})
    actual = command('real-developer-version', [tool, '-version'])
    strict_version(*actual, spec['exact_version'])
    after = [fingerprint(p, cap, p in official_tools) for p in paths]
    custody.record(root/'evidence/host-source-after.json', {'files': after})
    if after != before:
        raise ValueError('host source changed during proof')
    metadata()
    if custody.sha(HERE/'inputs.json') != descriptor_sha:
        raise ValueError('host proof descriptor changed')
    custody.record(root/'evidence/probe-passed.json', {'identity': IDENTITY,
        'real_tool': before[0], 'clean_exact_version': True, 'Apple_signature_verified': True,
        'build_or_link_execution': False,
        'metadata_queries': {'codesign': 2, 'version': 2},
        'accepted_for_link_hostpins': False,
        'Root_review_required_before_consumption': True})


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--identity', choices=[IDENTITY], required=True)
    parser.add_argument('--execute', action='store_true')
    parser.add_argument('--work')
    parser.add_argument('--worker', action='store_true', help=argparse.SUPPRESS)
    args = parser.parse_args(argv)
    metadata()
    if not args.execute:
        print('VERIFIED_HOST_TOOL_PROOF_SOURCE_NOT_NATIVE_ACCEPTANCE')
        return 0
    if platform.system() != 'Darwin' or platform.machine() != 'arm64' or sys.version_info[:2] != (3, 12):
        raise ValueError('fixed standard macOS ARM64 Python3.12 required')
    parent = Path(os.environ['RUNNER_TEMP']).resolve(strict=True)
    work = Path(args.work)
    if (not work.is_absolute() or work.parent != parent or work.name != 'luobo-apple-r9-host-tool'
            or parent.stat().st_uid != os.getuid() or work.is_symlink()):
        raise ValueError('fixed private task root required')
    if args.worker:
        if work.resolve() != work or work.stat().st_uid != os.getuid() or stat.S_IMODE(work.stat().st_mode) != 0o700:
            raise ValueError('private canonical worker root required')
        probe(work)
        return 0
    if work.exists():
        raise ValueError('fresh work only; no partial resume')
    # No credential, proxy, XCRUN flag or alternate HOME enters the worker.
    env = {k: os.environ[k] for k in ['HOME', 'RUNNER_TEMP', 'GITHUB_SHA', 'ImageOS', 'ImageVersion'] if k in os.environ}
    env.update(PATH='/usr/bin:/bin', LANG='en_US.UTF-8', PYTHONDONTWRITEBYTECODE='1')
    os.environ.clear(); os.environ.update(env)
    custody = load(REPO/'ci/apple-sdk-link/custody.py', 'host_tool_custody')
    custody.mkdir(work); custody.mkdir(work/'tmp'); custody.mkdir(work/'cache'); custody.mkdir(work/'evidence')
    custody.record(work/'activation.json', {'identity': IDENTITY, 'head': os.environ.get('GITHUB_SHA'),
        'inputs_sha256': custody.sha(HERE/'inputs.json')})
    profile = '(version 1)(allow default)(deny network*)(deny file-write*)(allow file-write* (subpath '+json.dumps(str(work))+') (literal "/dev/null"))(deny file-write* (literal '+json.dumps(str(work/'network-deny.sb'))+'))'
    custody.put(work/'network-deny.sb', profile.encode())
    guard = load(REPO/'ci/apple-sdk/resource_dual.py', 'host_tool_guard')
    def interrupted(_number, _frame):
        raise InterruptedError('host proof interrupted')
    for number in [signal.SIGINT, signal.SIGTERM]:
        signal.signal(number, interrupted)
    return guard.run(SimpleNamespace(command=['/usr/bin/sandbox-exec', '-f', str(work/'network-deny.sb'),
        sys.executable, '-B', str(HERE/'run.py'), '--identity', IDENTITY, '--execute', '--work', str(work), '--worker'],
        cwd=work, log=work/'probe-raw.log', record=work/'resource.json', min_free_mib=1024,
        poll_interval=.2, allocated_root=work, max_allocated_mib=64, timeout=30, secondary_root=None, secondary_cap_mib=None))


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception as error:
        print('HOST_TOOL_PROOF_REFUSED '+type(error).__name__, file=sys.stderr)
        raise SystemExit(1)
