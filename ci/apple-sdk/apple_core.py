#!/usr/bin/env python3
"""Explicit SDK-only Apple build. Default is read-only; no acquisition commands."""
import argparse
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import plistlib
import re
import shlex
import stat
import subprocess
import sys
import tarfile
from types import SimpleNamespace

HERE = Path(__file__).resolve().parent
COHORT = 'auditable-sdk-apple-arm64-v1'
TARGETS = ('ios/arm64', 'iossimulator/arm64')
FLOOR, INITIAL, CAP = 1024**3, 4*1024**3, 3*1024**3


def digest(body):
    return hashlib.sha256(body).hexdigest()


def read(path, limit=256*1024**2, single=True):
    path = Path(path)
    if path.resolve(strict=True) != path:
        raise ValueError('canonical input required')
    with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW), 'rb') as f:
        a = os.fstat(f.fileno())
        if not stat.S_ISREG(a.st_mode) or (single and a.st_nlink != 1) or a.st_size > limit:
            raise ValueError('bounded regular single-link input required')
        body = f.read(limit+1)
        b = os.fstat(f.fileno())
    identity = lambda s: (s.st_dev, s.st_ino, s.st_size, s.st_mtime_ns, s.st_ctime_ns)
    if len(body) > limit or identity(a) != identity(b) or identity(b) != identity(path.lstat()):
        raise ValueError('input changed while reading')
    return body


def sync(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def put(path, body):
    path = Path(path)
    if not path.parent.exists():
        put_dir(path.parent)
    with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600), 'wb') as f:
        f.write(body); f.flush(); os.fsync(f.fileno())
    sync(path.parent)


def put_dir(path):
    path = Path(path)
    if not path.exists():
        put_dir(path.parent)
        path.mkdir(mode=0o700); sync(path.parent)
    if path.resolve(strict=True) != path or not path.is_dir() or path.stat().st_uid != os.getuid():
        raise ValueError('canonical owned directory required')


def record(path, value):
    put(path, (json.dumps(value, sort_keys=True, indent=2)+'\n').encode())


def load(path, name):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def inputs():
    manifest = json.loads(read(HERE/'manifest.json'))
    for row in manifest['files']:
        if digest(read(HERE/row['path'])) != row['sha256']:
            raise ValueError('candidate source pin mismatch')
    value = json.loads(read(HERE/'inputs.json'))
    # New explicit cloud derivation; original local R1/host233 is immutable.
    if (manifest.get('schema') != 'luobo.apple-sdk.cloud-derived.v1'
            or manifest.get('controller') != 'apple-sdk-only-cloud-arm64-r1'
            or value.get('cloud_controller') != 'apple-sdk-only-cloud-arm64-r1'
            or manifest.get('runtime_inputs_sha256') != digest(read(HERE/'inputs.json'))):
        raise ValueError('explicit cloud SDK derivation required')
    for alias, target in value['aliases'].items():
        if Path(alias).resolve(strict=True) != Path(target):
            raise ValueError('fixed official tool alias changed')
    for row in value['pins']:
        body = read(row['path'], single=not row.get('official_system_hardlink', False))
        if len(body) != row['bytes'] or digest(body) != row['sha256']:
            raise ValueError('borrowed input pin mismatch: '+row['path'])
    return value


def admit(output, host, free=None):
    output, host = Path(output), Path(host)
    parent = output.parent
    if (not output.is_absolute() or output.exists() or output.is_symlink()
            or parent != host/'.qa' or parent.resolve(strict=True) != parent
            or not re.fullmatch(r'apple-[A-Za-z0-9_-]{1,40}', output.name)
            or parent.stat().st_uid != os.getuid() or parent.stat().st_mode & 0o077):
        raise ValueError('new private .qa/apple-* output required')
    space = free or (lambda p: os.statvfs(p).f_bavail * os.statvfs(p).f_frsize)
    if min(space(host), space(parent)) < INITIAL:
        raise ValueError('initial host/output4GiB required')
    return output


def extract_source(body, destination):
    with tarfile.open(fileobj=io.BytesIO(body)) as archive:
        members = archive.getmembers()
        names = set()
        if len(members) > 4096 or sum(m.size for m in members) > 80*1024**2:
            raise ValueError('source archive cap')
        for m in members:
            p = Path(m.name)
            if (p.is_absolute() or '..' in p.parts or m.name in names
                    or not (m.isfile() or m.isdir())):
                raise ValueError('unsafe source archive')
            names.add(m.name)
        for m in members:
            p = destination/m.name
            if m.isdir():
                put_dir(p)
            else:
                put(p, archive.extractfile(m).read())


def environment(c, root):
    # No ambient proxy, Go env file, workspace, GOFLAGS, SDK/NDK, or credentials.
    return {'PATH': str(root/'bin')+':'+str(Path(c['go']).parent)+':'+str(Path(c['gomobile']).parent)+':/usr/bin:/bin',
            'HOME': os.environ['HOME'], 'TMPDIR': str(root/'tmp'), 'LANG': 'en_US.UTF-8',
            'DEVELOPER_DIR': c['developer'], 'GOROOT': str(Path(c['go']).parents[1]),
            'GOPATH': str(root/'gopath'), 'GOMODCACHE': c['module_cache'],
            'GOCACHE': str(root/'cache/go'), 'GOENV': 'off', 'GOAUTH': 'off', 'GOWORK': 'off',
            'GOTOOLCHAIN': 'local', 'GOPROXY': 'off', 'GOSUMDB': 'off',
            'GOFLAGS': '-mod=readonly',
            'GIT_CONFIG_NOSYSTEM': '1', 'GIT_CONFIG_GLOBAL': '/dev/null',
            'GIT_TERMINAL_PROMPT': '0', 'GIT_NO_REPLACE_OBJECTS': '1',
            'CGO_ENABLED': '1', 'GOMAXPROCS': '2',
            'CLANG_MODULE_CACHE_PATH': str(root/'cache/clang'),
            'APPLE_OBSERVER_CONFIG': str(root/'observer.json'),
            'APPLE_OBSERVER_SHA': digest(controls(c, root)['observer.json'])}


def controls(c, root):
    base_path = str(Path(c['go']).parent)+':'+str(Path(c['gomobile']).parent)+':/usr/bin:/bin'
    config = {'cohort': COHORT, 'root': str(root), 'go': c['go'], 'go_sha256': c['go_sha256'],
              'base_path': base_path, 'observer_helper': c['observer_helper'],
              'observer_helper_sha256': c['observer_helper_sha256']}
    shell = '#!/bin/sh\nexec '+shlex.quote(c['python'])+' -B '+shlex.quote(str(HERE/'apple_observer.py'))+' "$@"\n'
    protected = [root/'offline.sb', root/'observer.json', root/'bin/go', root/'bin']
    literals = ' '.join('(literal '+json.dumps(str(p))+')' for p in protected)
    profile = '(version 1)(allow default)(deny network*)(deny file-write*)(allow file-write* (subpath '+json.dumps(str(root))+') (literal "/dev/null"))(deny file-write* '+literals+')'
    return {'observer.json': (json.dumps(config, sort_keys=True, indent=2)+'\n').encode(),
            'bin/go': shell.encode(), 'offline.sb': profile.encode()}


def check_controls(c, root):
    for relative, body in controls(c, root).items():
        if read(root/relative) != body:
            raise ValueError('immutable task control changed')


def command(root, name, argv, cwd, env, output=None):
    record(root/'evidence'/(name+'.intent.json'), {'argv': list(map(str, argv)), 'cwd': str(cwd)})
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW
    with os.fdopen(os.open(root/'evidence'/(name+'.log'), flags, 0o600), 'wb') as log:
        if output is None:
            code = subprocess.call(list(map(str, argv)), cwd=cwd, env=env, stdout=log, stderr=subprocess.STDOUT)
        else:
            with os.fdopen(os.open(output, flags, 0o600), 'wb') as target:
                code = subprocess.call(list(map(str, argv)), cwd=cwd, env=env, stdout=target, stderr=log)
    record(root/'evidence'/(name+'.result.json'), {'exit_code': code})
    if code:
        raise RuntimeError('command failed: '+name)


def prepare(c, root, env):
    parent = load(c['parent_builder'], 'frozen_android_source_primitives')
    manifest = json.loads(read(c['core_manifest']))
    for name in ['home', 'tmp', 'bin', 'evidence', 'cache/go', 'cache/clang', 'gopath/pkg/gomobile', 'core', 'protocol', 'bridge']:
        put_dir(root/name)
    git_env = {**env, 'GIT_NO_REPLACE_OBJECTS': '1', 'GIT_CONFIG_NOSYSTEM': '1', 'GIT_CONFIG_GLOBAL': '/dev/null'}
    command(root, 'source-archive', [c['git'], '-C', c['upstream'], 'archive', manifest['base_commit']], root, git_env, root/'source.tar')
    extract_source(read(root/'source.tar', 80*1024**2), root/'core')
    command(root, 'apply-reviewed-patch', [c['git'], 'apply', c['patch']], root/'core', git_env)
    for row in manifest['modified_files']:
        if digest(read(root/'core'/row['path'])) != row['sha256']:
            raise ValueError('prepared core differs from current78')
    parent.verify_protocol(Path(c['protocol']), manifest['protocol']['files'])
    for row in manifest['protocol']['files']:
        put(root/'protocol'/row['path'], read(Path(c['protocol'])/row['path']))
    original = read(root/'core/go.mod')
    old = ('replace github.com/OpenIMSDK/protocol => '+c['original_protocol_replacement']).encode()
    if original.count(old) != 1:
        raise ValueError('exact original protocol replacement required')
    # Only this owned copy is remapped. Original go.mod/810 remain evidence.
    record(root/'evidence/source-locks.json', {'go_mod_sha256': digest(original), 'go_sum_sha256': digest(read(root/'core/go.sum'))})
    put(root/'evidence/source-go.mod', original)
    p = root/'core/go.mod'
    put(p.with_suffix('.new'), original.replace(old, ('replace github.com/OpenIMSDK/protocol => '+str(root/'protocol')).encode()))
    os.replace(p.with_suffix('.new'), p); sync(p.parent)
    parent.install_acquired_go_sum(root/'core', Path(c['acquired_sum']), c['acquired_sha256'], c['source_sum_sha256'])
    for row in c['bridge_files']:
        put(root/'bridge'/row['relative'], read(row['path']))
    record(root/'prepared.json', {'cohort': COHORT, 'SDK_only': True, 'protocol_count': 52,
           'source_modified_count': 78, 'source_module_sha256': digest(original),
           'ready_module_sha256': digest(read(p)), 'ready_sum_sha256': digest(read(root/'core/go.sum')),
           'TLS_overlay': False, 'targets': TARGETS, 'minimum_ios': '15.0'})


def locks(root):
    ready = json.loads(read(root/'prepared.json'))
    if (digest(read(root/'core/go.mod')) != ready['ready_module_sha256']
            or digest(read(root/'core/go.sum')) != ready['ready_sum_sha256']):
        raise ValueError('core source/acquired locks changed')


def framework_slices(path):
    info = plistlib.loads(read(path/'Info.plist'))
    result = {}
    for entry in info['AvailableLibraries']:
        kind = entry.get('SupportedPlatformVariant', 'device')
        if (entry['SupportedPlatform'] != 'ios' or entry['SupportedArchitectures'] != ['arm64']
                or kind not in {'device', 'simulator'} or kind in result
                or entry['LibraryPath'] != 'OpenIMCore.framework'
                or not re.fullmatch(r'ios-arm64(?:-simulator)?', entry['LibraryIdentifier'])):
            raise ValueError('exact two Apple arm64 slices required')
        result[kind] = path/entry['LibraryIdentifier']/entry['LibraryPath']
    if set(result) != {'device', 'simulator'}:
        raise ValueError('both Apple slices required')
    return result


def worker(root, prepare_only):
    c = inputs(); check_controls(c, root); env = environment(c, root)
    prepare(c, root, env)
    if prepare_only:
        return
    for key in ['go', 'gomobile', 'gobind']:
        command(root, key+'-buildinfo', [c['go'], 'version', '-m', c[key]], root, env)
    if 'go1.27.2' not in (root/'evidence/go-buildinfo.log').read_text():
        raise ValueError('Go1.27.2 required')
    for key in ['gomobile', 'gobind']:
        if c['mobile_version'] not in (root/'evidence'/(key+'-buildinfo.log')).read_text():
            raise ValueError('fixed x/mobile module buildinfo required')
    command(root, 'go-mod-verify', [c['go'], 'mod', 'verify'], root/'core', env)
    locks(root)
    before = {x: digest(read(root/'core'/x)) for x in ['go.mod', 'go.sum']}
    try:
        check_controls(c, root)
        locks(root)
        command(root, 'apple-bind', [c['gomobile'], 'bind', '-work', '-v', '-target='+','.join(TARGETS),
                '-iosversion=15.0', '-trimpath', '-ldflags=-s -w', '-o', root/'OpenIMCore.xcframework',
                './open_im_sdk', './open_im_sdk_callback'], root/'core', env)
    finally:
        record(root/'evidence/core-locks-after.json', {'before': before,
               'after': {x: digest(read(root/'core'/x)) for x in before}})
        locks(root)
        check_controls(c, root)
    slices = framework_slices(root/'OpenIMCore.xcframework')
    for platform in ['ios', 'iossimulator']:
        proof = json.loads(read(root/'evidence/generated'/platform/'result.json'))
        if proof['exit_code'] != 0 or not proof['source_unchanged']:
            raise ValueError('actual platform tidy proof required')
    link = load(HERE/'link_probe.py', 'apple_link_probe')
    link.run(c, root, env, slices)
    inputs(); locks(root); check_controls(c, root)
    record(root/'sdk-passed.json', {'cohort': COHORT, 'SDK_only': True, 'runtime': False,
           'App_source_cohort': None, 'targets': TARGETS, 'iOS_minimum': '15.0'})


def main(argv=None):
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--output', type=Path)
    p.add_argument('--execute', action='store_true')
    p.add_argument('--prepare-only', action='store_true')
    p.add_argument('--worker', action='store_true', help=argparse.SUPPRESS)
    args = p.parse_args(argv)
    if args.worker:
        worker(args.output, args.prepare_only); return 0
    if not args.execute:
        c = inputs()
        print(json.dumps({'state': 'verified_read_only', 'cohort': COHORT, 'targets': TARGETS,
                          'ios': '15.0', 'native_commands_executed': 0})); return 0
    if args.output is None:
        p.error('--execute requires new --output')
    # Cheap disk admission precedes full dependency/tool hashing and source copy.
    light = json.loads(read(HERE/'inputs.json'))
    root = admit(args.output, Path(light['host_root']))
    c = inputs(); put_dir(root)
    if Path(sys.executable).resolve(strict=True) != Path(c['python']):
        raise ValueError('fixed existing Python3.12 runtime required')
    for relative, body in controls(c, root).items():
        put(root/relative, body)
    os.chmod(root/'bin/go', 0o700)
    guard = load(c['guard'], 'frozen_resource_guard')
    command_line = ['/usr/bin/sandbox-exec', '-f', str(root/'offline.sb'), c['python'], '-B', str(HERE/'apple_core.py'), '--worker', '--output', str(root)]
    if args.prepare_only:
        command_line.append('--prepare-only')
    result = guard.run(SimpleNamespace(command=command_line, cwd=root, log=root/'run.log', record=root/'resource.json',
                min_free_mib=1024, timeout=1800, allocated_root=root, max_allocated_mib=3072, poll_interval=0.2))
    if result == 0:
        print('PREPARED_ONLY' if args.prepare_only else 'SDK_APPLE_BIND_AND_LINK_PASSED_NO_RUNTIME')
    return result


if __name__ == '__main__':
    raise SystemExit(main())
