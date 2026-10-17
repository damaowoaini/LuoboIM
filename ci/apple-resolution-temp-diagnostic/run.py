#!/usr/bin/env python3
"""One manual hosted Swift/SPM diagnostic; no App, acquisition or permission fix."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import pwd
import selectors
import signal
import stat
import subprocess
import sys
from types import SimpleNamespace

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
IDENTITY = 'apple-xcode164-owned-temp-swift-spm-diagnostic-r23-v1'
DEVELOPER = '/Applications/Xcode_16.4.app/Contents/Developer'
SWIFTC = DEVELOPER + '/Toolchains/XcodeDefault.xctoolchain/usr/bin/swiftc'
LOG_CAP = 256 * 1024
CAP = 64 * 1048576


def load(path, name):
    s = importlib.util.spec_from_file_location(name, path)
    m = importlib.util.module_from_spec(s); s.loader.exec_module(m)
    return m


def checked(root, row):
    p = Path(root)
    for part in row['path'].split('/'):
        if part in {'', '.', '..'}: raise ValueError('source path shape')
        p = p/part
        if p.is_symlink(): raise ValueError('source alias refused')
    with os.fdopen(os.open(p, os.O_RDONLY | os.O_NOFOLLOW), 'rb') as f:
        a = os.fstat(f.fileno())
        if not stat.S_ISREG(a.st_mode) or a.st_nlink != 1 or not 0 <= a.st_size <= 2 << 20:
            raise ValueError('bounded original source required')
        b = f.read((2 << 20)+1); z = os.fstat(f.fileno())
    fields = lambda s: (s.st_dev, s.st_ino, s.st_mode, s.st_nlink, s.st_size, s.st_mtime_ns, s.st_ctime_ns)
    if (fields(a) != fields(z) or fields(z) != fields(p.lstat())
            or len(b) != row['bytes'] or hashlib.sha256(b).hexdigest() != row['sha256']):
        raise ValueError('source changed')
    return b


def verify():
    spec = json.loads((HERE/'inputs.json').read_bytes())
    if spec['identity'] != IDENTITY or spec['schema'] != 1:
        raise ValueError('diagnostic identity required')
    for r in spec['owned'] + spec['borrowed']: checked(REPO, r)
    return spec


def driver():
    parent = REPO/'ci/apple-current359'
    prior = sys.modules.get('consume')
    if prior is not None and Path(prior.__file__).resolve() != parent/'consume.py':
        raise ValueError('foreign source consumer module')
    sys.path.insert(0, str(parent))
    try: return load(parent/'run.py', 'owned_temp_diagnostic_original_driver')
    finally: sys.path.pop(0)


def put(path, body):
    path.parent.mkdir(parents=True, mode=0o700, exist_ok=True)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'wb') as out:
        out.write(body); out.flush(); os.fsync(out.fileno())


def record(path, value):
    put(path, (json.dumps(value, sort_keys=True, indent=2)+'\n').encode())


def native_argv(work, phase):
    if phase == 'swift':
        return ['/usr/bin/arch', '-arm64e', SWIFTC, '-v', '-emit-library',
                '-parse-as-library', '-module-name', 'OwnedResolutionProbe',
                '-module-cache-path', str(work/'cache/swift-modules'),
                '-o', str(work/'tmp/libOwnedResolutionProbe.dylib'), str(work/'source/Probe.swift')]
    if phase != 'spm': raise ValueError('finite phase required')
    return ['/usr/bin/arch', '-arm64e', 'xcrun', 'xcodebuild',
            '-resolvePackageDependencies', '-project', 'Runner.xcodeproj',
            '-clonedSourcePackagesDirPath', str(work/'source/app/build/ios/SourcePackages')]


def source_snapshot(work, fixtures):
    rows = []
    for name, expected in fixtures.sources().items():
        value = checked(work, {'path': name, 'bytes': len(expected),
                              'sha256': hashlib.sha256(expected).hexdigest()})
        rows.append({'path': name, 'bytes': len(value), 'sha256': hashlib.sha256(value).hexdigest()})
    return rows


def tools_snapshot(spec, host):
    rows = []
    swift = Path(SWIFTC)
    if swift.resolve(strict=True) != Path(spec['swiftc_alias_target']):
        raise ValueError('original Swift compiler alias changed')
    for r in spec['host_pins']:
        value = host.fingerprint(Path(r['path']), 512 << 20, r['official_system_hardlink'])
        if value['bytes'] != r['bytes'] or value['sha256'] != r['sha256']:
            raise ValueError('fixed original tool body differs')
        rows.append(value)
    python = Path(sys.executable).resolve(strict=True)
    rows.append(host.fingerprint(python, 64 << 20, True))
    return rows


def capture(work, argv, environment):
    """Drain both actual streams with a bounded prefix; no invented stdout."""
    state = {n: {'body': bytearray(), 'bytes': 0, 'sha': hashlib.sha256()} for n in ['stdout', 'stderr']}
    child = subprocess.Popen(argv, cwd=work/'source/app/ios', env=environment,
        stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    selector = selectors.DefaultSelector()
    for name in state: selector.register(getattr(child, name), selectors.EVENT_READ, name)
    interrupted = False
    try:
        while selector.get_map():
            for key, _ in selector.select(.2):
                block = os.read(key.fd, 65536)
                if not block:
                    selector.unregister(key.fileobj); key.fileobj.close(); continue
                s = state[key.data]; s['bytes'] += len(block); s['sha'].update(block)
                s['body'].extend(block[:max(0, LOG_CAP-len(s['body']))])
        return child.wait(), any(s['bytes'] > LOG_CAP for s in state.values())
    except BaseException:
        interrupted = True
        raise
    finally:
        if child.poll() is None:
            child.terminate()
            try: child.wait(timeout=2)
            except subprocess.TimeoutExpired:
                child.kill(); child.wait(timeout=2)
        selector.close()
        for name, s in state.items():
            put(work/'evidence'/(name+'.log'), bytes(s['body']))
        record(work/'evidence/native-result.json', {'exit_code': child.returncode,
            'interrupted': interrupted, 'native_invoked': True,
            'streams': {n: {'total_bytes': s['bytes'], 'sha256': s['sha'].hexdigest(),
                           'retained_prefix_bytes': len(s['body']), 'truncated': s['bytes'] > LOG_CAP}
                        for n, s in state.items()}, 'generated_probe_binary_executed': False})


def worker(work, phase):
    spec = verify(); d = driver(); fixtures = load(HERE/'fixtures.py', 'temp_diagnostic_fixtures')
    a = json.loads((work/'activation.json').read_bytes())
    if (a.get('diagnostic_identity') != IDENTITY or a.get('phase') != phase
            or a.get('materialized_App_members') != 0 or a.get('source_ref') is not None):
        raise ValueError('diagnostic-only activation required')
    d.xcrun_adapter.check(work, REPO/'ci/apple-current359/xcrun_adapter.py', sys.executable)
    d.xcrun_adapter.cache_boundary(work)
    before = source_snapshot(work, fixtures)
    selected = json.loads((work/'evidence/selected-cocoapods.json').read_bytes())
    d.pod_selector.require_identity(selected, json.loads((REPO/'ci/apple-current359/lane.json').read_bytes())['cocoapods_preinstalled'])
    e = d.env(work, selected)  # exact original environment; no local alternative
    original_home = pwd.getpwuid(os.getuid()).pw_dir
    if e['HOME'] != os.environ['HOME'] or str(Path(e['HOME']).resolve(strict=True)) != original_home:
        raise ValueError('original system HOME must remain unchanged')
    allowed = ['TMPDIR','CFFIXED_USER_HOME','DEVELOPER_DIR','CLANG_MODULE_CACHE_PATH',
               'SWIFTPM_MODULECACHE_OVERRIDE','xcrun_db']
    record(work/'evidence/child-boundary.json', {'identity': IDENTITY, 'phase': phase,
        'HOME_preserved_original_system_home': True, 'environment_subset': {k:e[k] for k in allowed},
        'parent_environment_source': 'unchanged ci/apple-current359/run.py:env',
        'internal_Xcode_XPC_child_environment_observed': False,
        'specific_denied_syscall_observed': False,
        'network_granted': False, 'App_compilation': False})
    argv = native_argv(work, phase)
    record(work/'evidence/native-command.json', {'argv': argv, 'cwd': 'work/source/app/ios',
        'outer_exec': '/usr/bin/arch', 'architecture': 'arm64e', 'full_environment_recorded': False})
    code = 1
    try:
        code, truncated = capture(work, argv, e)
        if phase == 'swift' and code == 0:
            host = load(REPO/'ci/apple-host-tool/run.py', 'temp_diagnostic_output_fingerprint')
            output = host.fingerprint(work/'tmp/libOwnedResolutionProbe.dylib', 16 << 20)
            record(work/'evidence/swift-output.json', {'relative_path':'tmp/libOwnedResolutionProbe.dylib',
                'bytes':output['bytes'],'sha256':output['sha256'],
                'binary_executed':False,'binary_uploaded':False})
        if phase == 'spm':
            audit_paths = sorted((work/'evidence/xcrun-adapter').glob('*.json'))
            if len(audit_paths) > 256: raise ValueError('bounded actual adapter audit')
            audits = [json.loads(p.read_bytes()) for p in audit_paths]
            _, public = d.xcrun_adapter.forwarding(work, argv[3:], work/'source/app/ios')
            expected = ['-arm64e','/usr/bin/xcrun','--no-cache',*public]
            matched = any(r.get('outer_exec') == '/usr/bin/arch'
                and r.get('outer_argv') == expected and r.get('response_synthesized') is False
                for r in audits)
            record(work/'evidence/spm-adapter-check.json', {'actual_request_matched':matched,
                'audit_count':len(audits),'responses_synthesized':False})
            if code == 0 and not matched: return 1
        if truncated: return 1
        return code
    finally:
        finish_worker_boundary(work, phase, d, fixtures, before, selected, e)



def finish_worker_boundary(work, phase, d, fixtures, before, selected, environment):
    after = source_snapshot(work, fixtures)
    record(work/'evidence/source-post.json', {'exact': after == before, 'files': after,
        'scope': 'new public diagnostic inputs only; not source359/477 validation'})
    d.xcrun_adapter.check(work, REPO/'ci/apple-current359/xcrun_adapter.py', sys.executable)
    d.pod_selector.recheck(selected, Path(environment['HOME']))
    verify()
    # This success receipt is written LAST; an earlier source-post is not a
    # proof that the adapter, selected Pod, or controller recheck completed.
    record(work/'evidence/worker-boundary-post.json', {'schema':1, 'exact':True,
        'identity':IDENTITY, 'phase':phase})


def guarded_probe(guard, adapter, cache, root, works, work, command):
    # The primary meter includes BOTH phase directories and all newly allowed
    # external exact cache leaves. Private caches are already inside root.
    boundary = adapter.cache_boundary(work)
    secondary = Path(boundary['canonical'])
    original = guard.allocated
    private = []
    for w in works:
        p = adapter.private_cache_path(w).parent
        private.append({'canonical':str(p),'uid':boundary['uid'],
            'directory_identity':adapter.cache_directory_identity(p,boundary['uid']),
            'protected_preexisting':[]})
    def measured(p):
        if p is not None and Path(p) == secondary:
            return cache.allocated_cache_leaves(boundary, adapter.cache_directory_identity) + sum(
                cache.allocated_cache_leaves(r, adapter.cache_directory_identity) for r in private)
        if p is not None and Path(p) == root:
            return original(root) + cache.allocated_cache_leaves(boundary, adapter.cache_directory_identity)
        return original(p)
    guard.allocated = measured
    try:
        return guard.run(SimpleNamespace(command=command, cwd=root,
            log=work/'evidence/worker.log', record=work/'evidence/resource.json',
            allocated_root=root, max_allocated_mib=64, min_free_mib=1024,
            secondary_root=secondary, secondary_cap_mib=64, poll_interval=.2, timeout=120))
    finally: guard.allocated = original


def may_continue(resource, native, source_post, boundary_post, phase):
    # A resource record alone cannot distinguish a native failure from a
    # Python/setup/validation failure. Only a completed actual command plus
    # exact input post-check may proceed to the second independent probe.
    if (phase not in {'swift','spm'} or not isinstance(boundary_post, dict)
            or boundary_post.get('schema') != 1 or boundary_post.get('exact') is not True
            or boundary_post.get('identity') != IDENTITY or boundary_post.get('phase') != phase):
        return False
    if resource.get('status') not in {'passed', 'failed'}:
        return False
    if (not isinstance(native, dict) or native.get('native_invoked') is not True
            or native.get('interrupted') is not False
            or not isinstance(source_post, dict) or source_post.get('exact') is not True
            or type(native.get('exit_code')) is not int):
        return False
    if resource['status'] == 'failed':
        return native['exit_code'] != 0
    return native['exit_code'] == 0


def optional_record(path):
    if not path.is_file(): return None
    if path.is_symlink() or path.stat().st_size > 65536:
        raise ValueError('bounded diagnostic record required')
    return json.loads(path.read_bytes())


def execute():
    spec = verify(); d = driver(); fixtures = load(HERE/'fixtures.py', 'temp_diagnostic_parent_fixtures')
    if (platform.system() != 'Darwin' or platform.machine() != 'arm64'
            or sys.version_info[:3] != (3,12,10)):
        raise ValueError('fixed hosted macOS15 ARM/Python3.12.10 required')
    parent = Path(os.environ['RUNNER_TEMP']).resolve(strict=True)
    root = parent/'luobo-apple-temp-diagnostic-r23'
    if parent.stat().st_uid != os.getuid() or root.exists() or root.is_symlink():
        raise ValueError('fresh owned root required')
    s = os.statvfs(parent)
    if s.f_bavail*s.f_frsize < 1 << 30: raise ValueError('1GiB admission required')
    root.mkdir(mode=0o700)
    (root/'evidence').mkdir(mode=0o700)
    host = load(REPO/'ci/apple-host-tool/run.py', 'temp_diagnostic_host_fingerprint')
    before_tools = tools_snapshot(spec, host)
    record(root/'evidence/tools-before.json', {'files': before_tools})
    selected = d.pod_selector.select(Path(os.environ['HOME']))
    lane = json.loads((REPO/'ci/apple-current359/lane.json').read_bytes())
    d.pod_selector.require_identity(selected, lane['cocoapods_preinstalled'])
    works = [root/n/'luobo-apple-current359' for n in ['swift','spm']]
    for phase, work in zip(['swift','spm'], works):
        work.mkdir(mode=0o700, parents=True)
        for path in ['evidence','tmp','pub-cache','config','cache/clang','cache/swift-modules','cocoapods/cache','cocoapods/repos']:
            (work/path).mkdir(mode=0o700, parents=True, exist_ok=True)
        d.xcrun_adapter.install(work, REPO/'ci/apple-current359/xcrun_adapter.py', sys.executable)
        d.xcrun_adapter.seal_cache_boundary(work)
        # The original adapter identity is ONLY its unchanged entry ABI. This
        # explicit extra scope does not claim that App359/477 was materialized.
        record(work/'activation.json', {'identity':d.xcrun_adapter.IDENTITY,
            'diagnostic_identity':IDENTITY,'phase':phase,'source_ref':None,
            'materialized_App_members':0,'scope':'new-public-local-only-native-diagnostic',
            'controller_head':os.environ.get('GITHUB_SHA')})
        record(work/'evidence/selected-cocoapods.json', selected)
        for name, body in fixtures.sources().items(): put(work/name, body)
        profile = d.profile(work, 'diagnostic')  # original primary/private-T policy, no network grants
        for name in ['diagnostic.sb','evidence/selected-cocoapods.json',*fixtures.sources()]:
            profile += ('(deny file-write* (literal '+json.dumps(str(work/name))+'))').encode()
        put(work/'diagnostic.sb', profile)
        put(work/'evidence/profile.sb', profile)
        record(work/'evidence/source-pre.json', {'files':source_snapshot(work,fixtures)})
    record(root/'evidence/budget.json', {'primary_plus_secondary_unique_cap_bytes':CAP,
        'all_phase_outputs_share_primary':True,'private_cache_double_counted_in_primary':False,
        'secondary_cap_bytes':CAP,'minimum_free_bytes':1<<30,'poll_seconds':.2,
        'phase_timeout_seconds':120,'original_shared_guard_changed':False,
        'system_T_or_HOME_write_grant_added':False,'cache_bodies_read_or_deleted':False})
    outcome, rows = 0, []
    try:
        for phase, work in zip(['swift','spm'],works):
            guard = load(REPO/'ci/apple-sdk/resource_dual.py', 'temp_diagnostic_guard_'+phase)
            command = ['/usr/bin/sandbox-exec','-f',str(work/'diagnostic.sb'),sys.executable,'-B',
                       str(HERE/'run.py'),'--identity',IDENTITY,'--worker',str(work),'--phase',phase]
            code = guarded_probe(guard,d.xcrun_adapter,d.cache_resource,root,works,work,command)
            result = json.loads((work/'evidence/resource.json').read_bytes())
            complete = may_continue(result, optional_record(work/'evidence/native-result.json'),
                optional_record(work/'evidence/source-post.json'),
                optional_record(work/'evidence/worker-boundary-post.json'), phase)
            rows.append({'phase':phase,'exit_code':code,'resource_status':result['status'],
                         'worker_observation_complete':complete})
            outcome = outcome or code
            verify()
            record(work/'evidence/controller-source-post.json', {'exact':True,'scope':'pinned borrowed and new controllers only'})
            if not complete:
                outcome = outcome or 1
                break
    finally:
        tools_post_exact = controller_post_exact = False
        try:
            after_tools = tools_snapshot(spec,host)
            tools_post_exact = after_tools == before_tools
            record(root/'evidence/tools-after.json', {'exact':tools_post_exact,'files':after_tools})
            if not tools_post_exact: raise ValueError('original tool source changed')
            verify()
            controller_post_exact = True
        finally:
            all_phases_complete = (len(rows) == 2 and [r['phase'] for r in rows] == ['swift','spm']
                and all(r['worker_observation_complete'] for r in rows))
            outcome = outcome or (0 if all_phases_complete and tools_post_exact
                                  and controller_post_exact else 1)
            record(root/'evidence/summary.json', {'identity':IDENTITY,'phases':rows,
                'exit_code':outcome,'diagnostic_only':True,'permission_fix_proven':False,
                'diagnostic_complete':all_phases_complete and tools_post_exact and controller_post_exact,
                'worker_phase_boundaries_complete':all_phases_complete,
                'all_native_commands_passed':all_phases_complete and all(r['exit_code'] == 0 for r in rows),
                'tools_post_exact':tools_post_exact,'controller_post_exact':controller_post_exact,
                'specific_denied_syscall_proven':False,'old_preflight_repeated':False,
                'App359_or477_acquired_or_compiled':False,'generated_binaries_executed_or_uploaded':False})
    return outcome


def main():
    os.umask(0o077)
    p = argparse.ArgumentParser();p.add_argument('--identity',choices=[IDENTITY],required=True)
    p.add_argument('--worker',type=Path);p.add_argument('--phase',choices=['swift','spm'])
    p.add_argument('--verify',action='store_true')
    a = p.parse_args()
    def interrupted(_signum,_frame): raise InterruptedError('diagnostic interrupted')
    for s in [signal.SIGTERM,signal.SIGINT]:signal.signal(s,interrupted)
    if a.verify:
        if a.worker or a.phase:raise ValueError('readonly verify has no native worker')
        verify();return 0
    if bool(a.worker) != bool(a.phase):raise ValueError('worker and phase pair required')
    if a.worker:
        parent = Path(os.environ['RUNNER_TEMP']).resolve(strict=True)/'luobo-apple-temp-diagnostic-r23'
        if a.worker != parent/a.phase/'luobo-apple-current359' or a.worker.resolve(strict=True) != a.worker:
            raise ValueError('fixed diagnostic worker root')
        return worker(a.worker,a.phase)
    return execute()


if __name__ == '__main__':
    try:raise SystemExit(main())
    except Exception as e:
        print('APPLE_TEMP_DIAGNOSTIC_FAILED '+type(e).__name__,file=sys.stderr)
        raise SystemExit(1)
