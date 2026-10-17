"""Actual hosted Apple CLI preflight; no Flutter/engine/acquisition or fake response.

The same pinned adapter, activation, ARM argv, real HOME and restrictive sandbox
run before the expensive acquisition. Failed probes are all retained; no native
source/product is fetched. A passing pure controller test is not Xcode evidence.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import stat
import subprocess
import sys

IDENTITY = 'current359-null2-apple-native-resolution-v2'
DEVELOPER = '/Applications/Xcode_16.4.app/Contents/Developer'
EXPECTED_VERSION = b'Xcode 16.4\nBuild version 16F6\n'
CAP = 1 << 20


def put(path, body):
    path = Path(path)
    with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600), 'wb') as f:
        f.write(body); f.flush(); os.fsync(f.fileno())


def record(path, value):
    put(path, (json.dumps(value, sort_keys=True, indent=2)+'\n').encode())


def environment(work):
    # Preserve the actual HOME. No environment values are captured in evidence.
    e = {k: os.environ[k] for k in ['HOME', 'USER', 'LOGNAME'] if k in os.environ}
    e.update(PATH=str(work/'tool-adapters')+':/usr/bin:/bin:/usr/sbin',
             TMPDIR=str(work/'tmp'), CFFIXED_USER_HOME=str(work/'cf-home'),
             DEVELOPER_DIR=DEVELOPER, LANG='en_US.UTF-8', CI='true',
             GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL='/dev/null',
             GIT_TERMINAL_PROMPT='0', GIT_NO_REPLACE_OBJECTS='1',
             PYTHONDONTWRITEBYTECODE='1', xcrun_nocache='1')
    return e


def probes():
    return [('flutter-arm-adapter-version', ['/usr/bin/arch', '-arm64e', 'xcrun', 'xcodebuild', '-version']),
            ('real-no-cache-version', ['/usr/bin/xcrun', '--no-cache', 'xcodebuild', '-version']),
            ('system-git-version', ['/usr/bin/git', '--version']),
            ('real-find-git', ['/usr/bin/xcrun', '--no-cache', '--find', 'git'])]


def file_identity(path):
    path = Path(path)
    # Apple tool symlinks are recorded with their real target, never granted as
    # an arbitrary command. Only these explicit, source-reviewed tool roots.
    real = path.resolve(strict=True)
    roots = [Path('/usr/bin'), Path(DEVELOPER)/'usr/bin']
    if not any(real.is_relative_to(root) for root in roots) and not (path == real == Path('/bin/sh')):
        raise ValueError('official fixed tool target required')
    with os.fdopen(os.open(real, os.O_RDONLY | os.O_NOFOLLOW), 'rb') as f:
        before = os.fstat(f.fileno())
        if not stat.S_ISREG(before.st_mode) or before.st_size < 1 or before.st_size > 64 << 20:
            raise ValueError('bounded official tool file required')
        h = hashlib.sha256()
        for block in iter(lambda: f.read(65536), b''): h.update(block)
        after = os.fstat(f.fileno())
    keys = ('st_dev', 'st_ino', 'st_size', 'st_mtime_ns', 'st_ctime_ns')
    at = real.stat()
    if any(getattr(before,k) != getattr(after,k) or getattr(after,k) != getattr(at,k) for k in keys):
        raise ValueError('tool file changed')
    return {'requested_path': str(path), 'real_path': str(real),
            'bytes': before.st_size, 'sha256': h.hexdigest()}


def inspect_results(rows, audit_rows):
    expected = {label for label,_ in probes()}
    if not expected <= rows.keys(): raise ValueError('all actual preflight probes required')
    for label in ['flutter-arm-adapter-version','real-no-cache-version']:
        r = rows[label]
        if r['exit_code'] != 0 or r['stdout'] != EXPECTED_VERSION or r['stderr']:
            raise ValueError('actual fixed Xcode version CLI failed')
    if not audit_rows or any(r.get('outer_exec') != '/usr/bin/arch'
            or r.get('outer_argv') != ['-arm64e','/usr/bin/xcrun','--no-cache','xcodebuild','-version']
            or r.get('real_tool') != '/usr/bin/xcrun'
            or r.get('tool_argv') != ['--no-cache','xcodebuild','-version']
            or r.get('response_synthesized') is not False for r in audit_rows):
        raise ValueError('actual installed adapter invocation missing or differs')
    git = rows['system-git-version']
    if git['exit_code'] != 0 or not git['stdout'].startswith(b'git version ') or git['stderr']:
        raise ValueError('actual system Git CLI failed; acquisition remains blocked')
    found = rows['real-find-git']
    if found['exit_code'] != 0 or found['stderr']:
        raise ValueError('actual Xcode Git lookup failed')
    try: value = found['stdout'].decode('utf-8').rstrip('\n')
    except UnicodeDecodeError as e: raise ValueError('Git path encoding') from e
    allowed = {DEVELOPER+'/usr/bin/git', '/usr/bin/git'}
    if value not in allowed or found['stdout'] != (value+'\n').encode():
        raise ValueError('unreviewed actual Git path')
    return {'actual_cli_preflight_passed': True, 'acquisition_started': False,
            'actual_git_path': value, 'Xcode': '16.4', 'build': '16F6'}


def execute(work):
    work = Path(work)
    if work.name != 'luobo-apple-current359' or work.resolve(strict=True) != work:
        raise ValueError('owned fixed preflight root required')
    activation = json.loads((work/'activation.json').read_bytes())
    if activation.get('identity') != IDENTITY: raise ValueError('source activation required')
    rows = {}
    def call(label,argv):
        out, err = work/'evidence'/(label+'.stdout.log'), work/'evidence'/(label+'.stderr.log')
        record(work/'evidence'/(label+'.command.json'), {'argv': argv, 'cwd': 'work/source/app/ios', 'environment_recorded': False})
        with out.open('xb') as stdout, err.open('xb') as stderr:
            result = subprocess.run(argv, cwd=work/'source/app/ios', env=environment(work),
                stdin=subprocess.DEVNULL, stdout=stdout, stderr=stderr, timeout=20)
            stdout.flush(); stderr.flush(); os.fsync(stdout.fileno()); os.fsync(stderr.fileno())
        if out.stat().st_size > CAP or err.stat().st_size > CAP: raise ValueError('bounded public CLI output required')
        rows[label] = {'exit_code': result.returncode, 'stdout': out.read_bytes(), 'stderr': err.read_bytes()}
        record(work/'evidence'/(label+'.result.json'), {'exit_code': result.returncode,
            'stdout_bytes': out.stat().st_size, 'stderr_bytes': err.stat().st_size,
            'stdout_sha256': hashlib.sha256(rows[label]['stdout']).hexdigest(),
            'stderr_sha256': hashlib.sha256(rows[label]['stderr']).hexdigest()})
    for label,argv in probes(): call(label,argv)
    # Probe an actual fixed-Xcode Git binary only after the real xcrun lookup.
    # Its success never overrides the system-Git or Flutter-argv failures.
    found = rows['real-find-git']
    exact = (DEVELOPER+'/usr/bin/git\n').encode()
    tools = [file_identity('/usr/bin/arch'),file_identity('/bin/sh'),
             file_identity('/usr/bin/git'),file_identity('/usr/bin/xcrun')]
    if found['exit_code'] == 0 and found['stdout'] == exact and not found['stderr']:
        target = DEVELOPER+'/usr/bin/git'; tools.append(file_identity(target)); call('xcode-git-version',[target,'--version'])
    record(work/'evidence/tool-identities.json', {'tools': tools, 'source_ref': activation.get('source_ref'),
            'controller_head': activation.get('controller_head'), 'credentials_read': False})
    audit_rows = [json.loads(p.read_bytes()) for p in sorted((work/'evidence/xcrun-adapter').glob('*.json'))]
    result = inspect_results(rows,audit_rows)
    record(work/'evidence/complete.json', result)
    return 0


def prepare(parent, adapter, adapter_path, python, lane_sha, head, source_ref):
    work = Path(parent)/'tooling-preflight/luobo-apple-current359'
    work.mkdir(parents=True,mode=0o700,exist_ok=False)
    for name in ['evidence','tmp','source/app/ios']: (work/name).mkdir(parents=True,mode=0o700,exist_ok=False)
    adapter.install(work,adapter_path,python)
    cache = adapter.seal_cache_boundary(work)
    record(work/'evidence/darwin-xcrun-cache-boundary.json', cache)
    record(work/'activation.json', {'identity': IDENTITY, 'lane_sha256': lane_sha,
        'controller_head': head, 'source_ref': source_ref, 'scope': 'actual-cli-preflight-only'})
    profile = '(version 1)(allow default)(deny network*)(deny file-write*)'
    profile += '(allow file-write* (subpath '+json.dumps(str(work))+') (literal "/dev/null"))'
    profile += adapter.cache_profile(work)
    for name in ['activation.json','tool-adapters','preflight.sb']:
        profile += '(deny file-write* (subpath '+json.dumps(str(work/name))+'))'
    put(work/'preflight.sb',profile.encode())
    put(work/'evidence/preflight-profile.sb',profile.encode())
    return work


if __name__ == '__main__':
    p = argparse.ArgumentParser(); p.add_argument('--worker',type=Path,required=True)
    a = p.parse_args()
    try: raise SystemExit(execute(a.worker))
    except Exception as e:
        # Logs above retain exact safe tool outputs. Do not dump ambient state.
        print('APPLE_ACTUAL_TOOL_PREFLIGHT_REFUSED '+type(e).__name__,file=sys.stderr)
        raise SystemExit(1)
