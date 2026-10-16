#!/usr/bin/python3 -B
"""Owned Go command observer. No Go semantics, dependency edits, or network code.

Only the exact official generated-module `go mod tidy` receives snapshots.
The pinned real Go inherits argv/stdin/stdout/stderr/environment unchanged except
removing our single injected PATH prefix. Same process group preserves the outer
resource guard's ability to kill every child. Failure/signal never becomes PASS.
"""
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import stat
import subprocess
import sys
import time

COHORT = 'current257-auditable-arm64-observed-bind-v1'
FLOOR = 1024 ** 3
FILE_LIMIT, TREE_LIMIT, COUNT_LIMIT = 8 * 1024 ** 2, 64 * 1024 ** 2, 4096
CONFIG_KEYS = {'schema', 'cohort', 'job_root', 'host_root', 'real_go', 'real_go_sha256',
               'observer_go', 'observer_sha256', 'base_path'}


def digest(body):
    return hashlib.sha256(body).hexdigest()


def sync(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def directory(path):
    path = Path(path)
    info = path.lstat()
    if path.resolve(strict=True) != path or not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid():
        raise ValueError('canonical owned real directory required')
    return path


def regular(path, limit=FILE_LIMIT):
    path = Path(path)
    if path.resolve(strict=True) != path:
        raise ValueError('source symlink/alias refused')
    with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW), 'rb') as f:
        before = os.fstat(f.fileno())
        if (not stat.S_ISREG(before.st_mode) or before.st_uid != os.getuid()
                or before.st_nlink != 1 or before.st_size > limit):
            raise ValueError('bounded owned regular single-link file required')
        body = f.read(limit + 1)
        after = os.fstat(f.fileno())
    identity = lambda x: (x.st_dev, x.st_ino, x.st_size, x.st_mtime_ns, x.st_ctime_ns)
    current = path.lstat()
    if len(body) > limit or identity(before) != identity(after) or identity(after) != identity(current):
        raise ValueError('source changed during snapshot read')
    return body, after


def put(path, body):
    path = Path(path)
    if not path.parent.exists():
        put_dir(path.parent)
    with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600), 'wb') as f:
        f.write(body)
        f.flush()
        os.fsync(f.fileno())
    sync(path.parent)


def put_dir(path):
    path = Path(path)
    if not path.exists():
        put_dir(path.parent)
        path.mkdir(mode=0o700)
        sync(path.parent)
    directory(path)


def record(path, value):
    body = (json.dumps(value, sort_keys=True, indent=2) + '\n').encode()
    if len(body) > 4 * 1024 ** 2:
        raise ValueError('bounded4MiB JSON evidence required')
    put(path, body)


def space(config):
    values = []
    for key in ['job_root', 'host_root']:
        info = os.statvfs(config[key])
        values.append(info.f_bavail * info.f_frsize)
    if min(values) < FLOOR:
        raise ValueError('observer host/output1GiB floor')
    return min(values)


def load_config(path, expected_sha, executable):
    body, info = regular(path, 16384)
    if info.st_mode & 0o077 or digest(body) != expected_sha:
        raise ValueError('private exact observer configuration required')
    config = json.loads(body)
    if set(config) != CONFIG_KEYS or config['schema'] != 1 or config['cohort'] != COHORT:
        raise ValueError('explicit observed-bind configuration only')
    job = directory(config['job_root'])
    if job.stat().st_mode & 0o077:
        raise ValueError('private0700 job root required')
    directory(config['host_root'])
    directory(job / 'tmp')
    directory(job / 'observer')
    directory(job / 'evidence')
    if Path(path) != job / 'observer/config.json' or Path(config['real_go']) != job / 'go/bin/go':
        raise ValueError('fixed job real Go/config path required')
    if Path(config['observer_go']) != job / 'observer/go' or Path(executable) != Path(config['observer_go']):
        raise ValueError('fixed observer path required')
    for name, key in [('real_go', 'real_go_sha256'), ('observer_go', 'observer_sha256')]:
        data, executable_info = regular(config[name], 256 * 1024 ** 2)
        if digest(data) != config[key] or not executable_info.st_mode & 0o100:
            raise ValueError('fixed executable checksum/mode mismatch')
    if os.path.samefile(config['real_go'], config['observer_go']):
        raise ValueError('recursive real Go refused')
    expected_path = str(job / 'observer') + os.pathsep + config['base_path']
    if (os.environ.get('PATH') != expected_path
            or str(job / 'observer') in config['base_path'].split(os.pathsep)
            or config['base_path'].split(os.pathsep)[0] != str(job / 'go/bin')):
        raise ValueError('only one injected observer PATH prefix allowed')
    cwd = directory(Path.cwd())
    if cwd != job and not cwd.is_relative_to(job):
        raise ValueError('Go cwd outside this job')
    space(config)
    return config


def generated_root(config, cwd):
    job, cwd = Path(config['job_root']), directory(cwd)
    root = directory(cwd.parent)
    if (cwd.name != 'src-android-arm64' or root.parent != job / 'tmp'
            or not re.fullmatch(r'gomobile-work-[A-Za-z0-9_-]+', root.name)):
        raise ValueError('tidy requires this job real Android ARM64 generated cwd')
    return root


def snapshot(config, work, output, require_complete=True, cancelled=lambda: False):
    """Full regular-file bodies in src/java/src-android-arm64; bounded, no links.

    A partial copy retains its manifest then raises. Missing go.sum is explicit;
    a complete snapshot requires actual Go+Java files and generated go.mod.
    """
    output = Path(output)
    put_dir(output)
    rows, total, error, missing, visited = [], 0, None, [], 0
    try:
        for root_name in ['src', 'java', 'src-android-arm64']:
            source = Path(work) / root_name
            if not source.exists() and not source.is_symlink():
                missing.append(root_name)
                continue
            directory(source)
            def walk(path, depth=0):
                nonlocal total, visited
                visited += 1
                if visited > 8192 or depth > 64:
                    raise ValueError('generated directory/depth cap')
                directory(path)
                with os.scandir(path) as entries:
                    children = []
                    for entry in entries:
                        if len(children) >= 8192:
                            raise ValueError('generated directory entry cap')
                        children.append(entry)
                children.sort(key=lambda e: e.name)
                for entry in children:
                    if cancelled():
                        raise InterruptedError('observer snapshot interrupted')
                    space(config)
                    p = Path(entry.path)
                    info = entry.stat(follow_symlinks=False)
                    if stat.S_ISDIR(info.st_mode):
                        walk(p, depth + 1)
                    elif stat.S_ISREG(info.st_mode):
                        body, stable = regular(p)
                        total += len(body)
                        if total > TREE_LIMIT or len(rows) >= COUNT_LIMIT:
                            raise ValueError('snapshot64MiB/4096file cap')
                        relative = str(p.relative_to(work))
                        put(output / relative, body)
                        rows.append({'path': relative, 'bytes': len(body), 'sha256': digest(body),
                                     'mode': stat.S_IMODE(stable.st_mode)})
                    else:
                        raise ValueError('generated symlink/special file refused')
            walk(source)
        paths = {x['path'] for x in rows}
        go_count = sum(x['path'].endswith('.go') for x in rows)
        java_count = sum(x['path'].endswith('.java') for x in rows)
        if require_complete and (missing or not go_count or not java_count or 'src-android-arm64/go.mod' not in paths):
            raise ValueError('complete actual Go/Java/generated go.mod required')
        space(config)
    except BaseException as cause:
        error = type(cause).__name__ + ': ' + str(cause)
        raise
    finally:
        record(output / 'manifest.json', {'complete': error is None and require_complete,
            'error': error, 'missing_roots': missing, 'files': rows, 'body_bytes': total,
            'go_sum_exists': any(x['path'] == 'src-android-arm64/go.sum' for x in rows),
            'source_Go_Java_not_metadata_stub': True, 'limits': {'bytes': TREE_LIMIT, 'files': COUNT_LIMIT, 'file_bytes': FILE_LIMIT}})
    return rows


def same_generated_sources(before, after):
    ordinary = lambda rows: {x['path']: (x['bytes'], x['sha256']) for x in rows
                             if x['path'] not in {'src-android-arm64/go.mod', 'src-android-arm64/go.sum'}}
    if ordinary(before) != ordinary(after):
        raise ValueError('tidy changed generated source outside module locks')


def forward(config, argv):
    if len(argv) > 512 or sum(len(x.encode()) for x in argv) > 65536:
        raise ValueError('bounded Go argv required')
    tidy = argv == ['mod', 'tidy']
    if not tidy and 'mod' in argv and 'tidy' in argv:
        raise ValueError('unexpected tidy argument shape')
    job = Path(config['job_root'])
    evidence = job / 'evidence/bind-observer'
    put_dir(evidence)
    calls = evidence / 'calls'
    put_dir(calls)
    identity = None
    for slot in range(128):
        try:
            record(calls / ('%03d-claim.json' % slot), {'argv': argv, 'cwd': str(Path.cwd()),
                   'state': 'reserved_not_dispatched'})
            identity = '%03d' % slot
            break
        except FileExistsError:
            continue
    if identity is None:
        raise ValueError('observer128call cap')
    child, first_signal, signal_time, error, code = None, None, None, None, None
    before, after = None, None
    old_handlers = {}
    def receive(number, _frame):
        nonlocal first_signal, signal_time
        if first_signal is None:
            first_signal, signal_time = number, time.monotonic()
            if child is not None and child.poll() is None:
                try:
                    child.send_signal(number)
                except ProcessLookupError:
                    pass
    for number in [signal.SIGINT, signal.SIGTERM, signal.SIGHUP]:
        old_handlers[number] = signal.signal(number, receive)
    generated = None
    try:
        if tidy:
            generated = generated_root(config, Path.cwd())
            record(evidence / 'tidy-claim.json', {'cwd': str(Path.cwd()), 'work': str(generated), 'argv': argv,
                'state': 'claimed_once_before_child', 'real_go_sha256': config['real_go_sha256']})
            before = snapshot(config, generated, evidence / 'before', cancelled=lambda: first_signal is not None)
        if first_signal is not None:
            raise InterruptedError('signal before real Go dispatch')
        env = dict(os.environ)
        # Remove only our own prefix; all other variables and PATH components
        # retain exactly their pre-observer values. Absolute real_go + no shim
        # in child PATH prevents recursive Go dispatch.
        env['PATH'] = config['base_path']
        child = subprocess.Popen([config['real_go'], *argv], env=env)
        while child.poll() is None:
            if signal_time is not None and time.monotonic() - signal_time > 2:
                child.kill()
            try:
                space(config)
            except BaseException:
                child.kill()
                raise
            time.sleep(.05)
        code = child.returncode
    except BaseException as cause:
        error = type(cause).__name__ + ': ' + str(cause)
    finally:
        if child is not None and child.poll() is None:
            child.kill()
            child.wait()
        if child is not None:
            code = child.returncode
        if tidy and generated is not None:
            try:
                after = snapshot(config, generated, evidence / 'after')
                if before is not None:
                    same_generated_sources(before, after)
            except BaseException as cause:
                error = error or type(cause).__name__ + ': ' + str(cause)
        try:
            space(config)
        except BaseException as cause:
            error = error or type(cause).__name__ + ': ' + str(cause)
        complete = (tidy and before is not None and after is not None and error is None
                    and first_signal is None and code == 0)
        result = {'argv': argv, 'cwd': str(Path.cwd()), 'real_go': config['real_go'],
            'real_go_sha256': config['real_go_sha256'], 'child_exit_code': code,
            'received_signal': first_signal, 'error': error, 'tidy': tidy,
            'tidy_evidence_complete': complete, 'same_process_group_as_outer_guard': True,
            'PATH_only_injected_prefix_removed': True, 'stdin_stdout_stderr_inherited': True,
            'selected_env': {k: os.environ.get(k) for k in ['GOOS', 'GOARCH', 'CGO_ENABLED', 'CC', 'CXX',
                'GOFLAGS', 'GOWORK', 'GOPATH', 'GOMODCACHE', 'GOPROXY', 'GOSUMDB', 'TMPDIR']}}
        try:
            record(calls / (identity + '-result.json'), result)
            if tidy:
                record(evidence / 'tidy-result.json', result)
        finally:
            for number, handler in old_handlers.items():
                signal.signal(number, handler)
    if first_signal is not None:
        return -first_signal
    if error is not None:
        return code if code not in (None, 0) else 75
    return code


def main():
    try:
        config = load_config(os.environ['LUOBO_GO_OBSERVER_CONFIG'],
            os.environ['LUOBO_GO_OBSERVER_CONFIG_SHA256'], Path(__file__).absolute())
        code = forward(config, sys.argv[1:])
    except BaseException as error:
        print('Go observer failed: ' + type(error).__name__ + ': ' + str(error), file=sys.stderr)
        return 75
    if code < 0:
        signal.signal(-code, signal.SIG_DFL)
        os.kill(os.getpid(), -code)
        return 128 - code
    return code


if __name__ == '__main__':
    sys.exit(main())
