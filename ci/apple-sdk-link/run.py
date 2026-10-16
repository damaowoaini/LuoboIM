#!/usr/bin/env python3
"""Manual R7 SDK link-only successor. No Go, binding, App or runtime execution."""
import argparse
import base64
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
import zipfile

from custody import (audit_raw, check_remote, download, extract, mkdir, put, record,
                     safe_name, sha, sync)

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
IDENTITY = 'apple-sdk-r7-link-only-arm64-v1'
GIB = 1024**3


def load(path, name):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def metadata():
    spec = json.loads((HERE/'inputs.json').read_bytes())
    if spec['identity'] != IDENTITY:
        raise ValueError('independent link-only identity required')
    for row in spec['source_files'] + spec['bridge_files']:
        path = REPO/safe_name(row['path'])
        info = path.lstat()
        if (path.resolve() != path or not stat.S_ISREG(info.st_mode) or info.st_nlink != 1
                or info.st_size != row['bytes'] or sha(path) != row['sha256']):
            raise ValueError('exact consumed source pin differs')
    return spec


def space(path):
    s = os.statvfs(path)
    return s.f_bavail*s.f_frsize


def work_path(text, fresh):
    path = Path(text)
    parent = Path(os.environ['RUNNER_TEMP']).resolve(strict=True)
    if (not path.is_absolute() or path.parent != parent or path.name != 'luobo-apple-r7-link-only'
            or parent.stat().st_uid != os.getuid() or path.is_symlink()
            or (fresh and path.exists()) or (not fresh and not path.is_dir())):
        raise ValueError('fresh fixed private RUNNER_TEMP root required')
    if not fresh and (path.resolve() != path or path.stat().st_mode & 0o077):
        raise ValueError('private canonical task root required')
    return path


def snapshot(root):
    rows = []
    for p in sorted(root.rglob('*')):
        info = p.lstat()
        if p.is_symlink() or not (p.is_dir() or stat.S_ISREG(info.st_mode)):
            raise ValueError('nonregular consumed input refused')
        if p.is_file():
            if info.st_nlink != 1:
                raise ValueError('consumed input alias refused')
            rows.append({'path': str(p), 'bytes': info.st_size, 'sha256': sha(p)})
    return rows


def extract_engine(w, spec):
    archive = w/'products/setup/archives/engine.zip'
    original = spec['engine']
    if archive.stat().st_size != original['bytes'] or sha(archive) != original['sha256']:
        raise ValueError('original official-generation engine archive required')
    # Original publication checksums remain provenance, not a new version fetch.
    import hashlib
    table = []
    for value in range(256):
        for _ in range(8):
            value = (value >> 1) ^ 0x82F63B78 if value & 1 else value >> 1
        table.append(value)
    md5, crc = hashlib.md5(), 0xffffffff
    with archive.open('rb') as stream:
        for body in iter(lambda: stream.read(1024**2), b''):
            md5.update(body)
            for value in body:
                crc = table[(crc ^ value) & 255] ^ (crc >> 8)
    if (base64.b64encode(md5.digest()).decode() != original['md5_base64']
            or base64.b64encode(((crc ^ 0xffffffff) & 0xffffffff).to_bytes(4, 'big')).decode() != original['crc32c_base64']):
        raise ValueError('original published engine checksums differ')
    wanted = {r['path']: r for r in spec['engine_files']}
    found = set()
    with zipfile.ZipFile(archive) as z:
        entries = z.infolist()
        if len(entries) > 4096 or sum(x.file_size for x in entries) > GIB:
            raise ValueError('official engine decoded budget')
        seen = set()
        for x in entries:
            name = safe_name(x.filename.rstrip('/'))
            if name.casefold() in seen or stat.S_ISLNK(x.external_attr >> 16):
                raise ValueError('official engine alias refused')
            seen.add(name.casefold())
        if z.testzip() is not None:
            raise ValueError('official engine CRC failure')
        for name, row in wanted.items():
            body = z.read('Flutter.xcframework/' + name)
            if len(body) != row['bytes'] or hashlib.sha256(body).hexdigest() != row['sha256']:
                raise ValueError('complete original engine50 body required')
            put(w/'engine/Flutter.xcframework'/name, body)
            found.add(name)
    if found != set(wanted) or len(found) != 50:
        raise ValueError('complete original engine50 required')


def acquisition(w):
    spec = metadata()
    token = os.environ.get('GITHUB_TOKEN', '')
    if not token:
        raise ValueError('read-only Actions artifact token required')
    remote = check_remote(spec, token)
    proofs = {}
    for artifact in spec['parent']['artifacts']:
        archive = w/(artifact['kind'] + '.zip')
        download(artifact, token, archive)
        rows = extract(archive, artifact, w/artifact['kind'],
                       spec['product_members'] if artifact['kind'] == 'products' else None)
        proofs[artifact['kind']] = {'id': artifact['id'], 'bytes': artifact['bytes'],
                                  'sha256': artifact['sha256'], 'members': rows}
    # No credential or signed URL enters any receipt, native argv or native env.
    del token
    raw_proof = audit_raw(w/'raw', spec)
    core = w/'products/.qa/apple-cloud-1/OpenIMCore.xcframework'
    raw_core = w/'raw/.qa/apple-cloud-1/OpenIMCore.xcframework'
    plist_proof = []
    for rel in ['Info.plist', 'ios-arm64/OpenIMCore.framework/Info.plist',
                'ios-arm64-simulator/OpenIMCore.framework/Info.plist']:
        if (core/rel).read_bytes() != (raw_core/rel).read_bytes():
            raise ValueError('original R7 framework metadata differs')
        d = plistlib.loads((core/rel).read_bytes())
        if rel != 'Info.plist' and (d.get('MinimumOSVersion') != '100.0'
                                     or d.get('CFBundleExecutable') != 'OpenIMCore'):
            raise ValueError('exact static gomobile wrapper metadata required')
        plist_proof.append({'relative': rel, 'sha256': sha(core/rel),
                            'MinimumOSVersion': d.get('MinimumOSVersion')})
    extract_engine(w, spec)
    for row in spec['bridge_files']:
        put(w/'.qa/apple-link-only-1/bridge'/row['relative'], (REPO/row['path']).read_bytes())
    record(w/'evidence/acquisition.json', {'identity': IDENTITY, 'original': remote,
           'artifacts': proofs, 'raw_proof': raw_proof, 'framework_metadata': plist_proof,
           'static_plist100_is_not_App_deployment_acceptance': True})
    record(w/'evidence/acquire-passed.json', {'identity': IDENTITY, 'original_bind_reused': True,
                                             'new_Go_or_bind_executions': 0})


def native_environment(root, developer):
    return {'HOME': os.environ['HOME'], 'PATH': '/usr/bin:/bin',
            'TMPDIR': str(root/'tmp'), 'LANG': 'en_US.UTF-8', 'DEVELOPER_DIR': developer,
            'CLANG_MODULE_CACHE_PATH': str(root/'cache/clang'),
            'PYTHONDONTWRITEBYTECODE': '1'}


def build(w):
    spec = metadata()
    if json.loads((w/'acquire-resource.json').read_bytes())['status'] != 'passed':
        raise ValueError('original acquisition guard must pass')
    if json.loads((w/'evidence/acquire-passed.json').read_bytes())['identity'] != IDENTITY:
        raise ValueError('explicit acquisition receipt required')
    old = REPO/'ci/apple-sdk'
    sys.path.insert(0, str(old))
    primitives = load(old/'apple_core.py', 'apple_core')
    link = load(old/'link_probe.py', 'r7_reused_sdk_link_probe')
    c = dict(spec['native'])
    c['flutter_xcframework'] = str(w/'engine/Flutter.xcframework')
    c['bridge_files'] = [{'relative': r['relative']} for r in spec['bridge_files']]
    for row in spec['host_pins']:
        p = Path(row['path'])
        if p.resolve(strict=True) != p:
            raise ValueError('exact original official tool/SDK path required')
        if str(p) in primitives.runtime_host_tool_paths(spec['native']):
            primitives.verify_runtime_host_tool(row)
        elif p.stat().st_size != row['bytes'] or sha(p) != row['sha256']:
            raise ValueError('official tool/SDK input differs')
    for alias, target in spec['native']['aliases'].items():
        if str(Path(alias).resolve(strict=True)) != target:
            raise ValueError('official tool alias differs')
    output = w/'.qa/apple-link-only-1'
    for rel in ['tmp', 'evidence', 'cache/clang', 'bridge']:
        mkdir(output/rel)
    consumed = snapshot(w/'raw') + snapshot(w/'products') + snapshot(w/'engine') + snapshot(output/'bridge')
    consumed += [{'path': str(w/(a['kind']+'.zip')), 'bytes': a['bytes'], 'sha256': a['sha256']}
                 for a in spec['parent']['artifacts']]
    inputs_sha = sha(HERE/'inputs.json')

    def validate():
        metadata()
        if sha(HERE/'inputs.json') != inputs_sha:
            raise ValueError('link-only descriptor changed')
        for row in consumed + spec['host_pins']:
            p = Path(row['path'])
            if p.stat().st_size != row['bytes'] or sha(p) != row['sha256']:
                raise ValueError('consumed artifact/tool/source changed')

    # Actual original framework paths: the audit thin copy is never substituted.
    framework = w/'products/.qa/apple-cloud-1/OpenIMCore.xcframework'
    slices = primitives.framework_slices(framework)
    record(output/'consumed-inputs.json', {'identity': IDENTITY, 'descriptor_sha256': inputs_sha,
                                         'files': consumed, 'host_pins': spec['host_pins']})
    e = native_environment(output, c['developer'])
    primitives.command(output, 'xcode-version', ['/usr/bin/xcodebuild', '-version'], output, e)
    if (output/'evidence/xcode-version.log').read_text().strip() != 'Xcode 16.4\nBuild version 16F6':
        raise ValueError('exact original Xcode16.4/16F6 required')
    # Existing linker implementation, exact original18 Swift + ObjC sources.
    link.run(c, output, e, slices, validate_inputs=validate)
    validate()
    record(output/'link-only-passed.json', {'identity': IDENTITY, 'SDK_only': True,
           'original_bind_run': spec['parent']['run'], 'original_bind_reused': True,
           'actual_new_bind_executions': 0, 'Swift_sources_per_platform': 18,
           'ObjC_shim_per_platform': 1, 'real_link_platforms': ['device', 'simulator'],
           'App': False, 'device_or_simulator_execution': False,
           'static_original_framework_embedded': False})


def clean_process_environment(stage):
    values = {k: os.environ[k] for k in ['HOME', 'USER', 'LOGNAME', 'RUNNER_TEMP', 'GITHUB_SHA']
              if k in os.environ}
    if stage == 'acquire' and os.environ.get('GITHUB_TOKEN'):
        values['GITHUB_TOKEN'] = os.environ['GITHUB_TOKEN']
    values.update(PATH='/usr/bin:/bin', LANG='en_US.UTF-8', PYTHONDONTWRITEBYTECODE='1')
    os.environ.clear()
    os.environ.update(values)  # HOME is retained exactly, never relocated.


def policy(w, stage):
    source = '(version 1)(allow default)(deny network*)(deny file-write*)'
    source += '(allow file-write* (subpath ' + json.dumps(str(w)) + ') (literal "/dev/null"))'
    protected = [w/'acquire.sb', w/'link.sb']
    if stage == 'acquire':
        source += '(allow network-outbound (remote ip "*:443") (remote ip "*:53") (literal "/private/var/run/mDNSResponder"))'
    else:
        protected += [w/'raw', w/'products', w/'engine', w/'raw.zip', w/'products.zip',
                      w/'evidence', w/'activation.json', w/'.qa/apple-link-only-1/bridge']
    for p in protected:
        source += '(deny file-write* (subpath ' + json.dumps(str(p)) + '))'
    return source.encode()


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--identity', choices=[IDENTITY], required=True)
    parser.add_argument('--stage', choices=['readonly', 'acquire', 'link'], default='readonly')
    parser.add_argument('--work')
    parser.add_argument('--worker', action='store_true', help=argparse.SUPPRESS)
    args = parser.parse_args(argv)
    if args.stage == 'readonly':
        metadata()
        print('VERIFIED_LINK_ONLY_SOURCE_NOT_NATIVE_ACCEPTANCE')
        return 0
    clean_process_environment(args.stage)
    if platform.system() != 'Darwin' or platform.machine() != 'arm64' or sys.version_info[:2] != (3, 12):
        raise ValueError('original macOS ARM64/Python3.12 host required')
    w = work_path(args.work, args.stage == 'acquire' and not args.worker)
    if args.worker:
        acquisition(w) if args.stage == 'acquire' else build(w)
        return 0
    initial = 8*GIB if args.stage == 'acquire' else 4*GIB
    if min(space(w.parent), space(REPO)) < initial:
        raise ValueError('original cold8GiB/build4GiB admission required')
    metadata()
    if args.stage == 'acquire':
        mkdir(w)
        mkdir(w/'evidence')
        record(w/'activation.json', {'identity': IDENTITY, 'descriptor_sha256': sha(HERE/'inputs.json'),
                                   'controller_head': os.environ.get('GITHUB_SHA')})
        put(w/'acquire.sb', policy(w, 'acquire'))
        put(w/'link.sb', policy(w, 'link'))
    elif json.loads((w/'activation.json').read_bytes())['descriptor_sha256'] != sha(HERE/'inputs.json'):
        raise ValueError('original link-only descriptor must remain fixed')
    guard = load(REPO/'ci/apple-sdk/resource_dual.py', 'link_only_resource_guard')
    from types import SimpleNamespace
    def interrupted(_number, _frame):
        raise InterruptedError('link-only guard interrupted')
    for number in [signal.SIGINT, signal.SIGTERM]:
        signal.signal(number, interrupted)
    command = ['/usr/bin/sandbox-exec', '-f', str(w/('acquire.sb' if args.stage == 'acquire' else 'link.sb')),
               sys.executable, '-B', str(HERE/'run.py'), '--identity', IDENTITY,
               '--stage', args.stage, '--work', str(w), '--worker']
    return guard.run(SimpleNamespace(command=command, cwd=w, log=w/(args.stage+'-raw.log'),
        record=w/(args.stage+'-resource.json'), min_free_mib=1024, poll_interval=.2,
        allocated_root=w, max_allocated_mib=3072 if args.stage == 'acquire' else 6144,
        timeout=1200 if args.stage == 'acquire' else 1800,
        secondary_root=None if args.stage == 'acquire' else w/'.qa/apple-link-only-1',
        secondary_cap_mib=3072))


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception as error:
        # Never expose signed URLs, bearer values, or HTTPError URL strings.
        print('LINK_ONLY_REFUSED ' + type(error).__name__, file=sys.stderr)
        raise SystemExit(1)
