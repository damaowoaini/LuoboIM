#!/usr/bin/env python3
"""Transparent real-Go forwarding; two actual Apple tidy snapshots, no Go edits."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import stat
import subprocess
import sys
import time


def configuration():
    path = Path(os.environ['APPLE_OBSERVER_CONFIG'])
    if path.resolve(strict=True) != path or path.stat().st_mode & 0o077:
        raise ValueError('private canonical observer config required')
    body = path.read_bytes()
    if hashlib.sha256(body).hexdigest() != os.environ['APPLE_OBSERVER_SHA']:
        raise ValueError('literal observer configuration digest required')
    c = json.loads(body)
    if c['cohort'] != 'auditable-sdk-apple-arm64-v1':
        raise ValueError('Apple SDK cohort required')
    root = Path(c['root'])
    if (root.resolve(strict=True) != root or root.stat().st_uid != os.getuid()
            or root.stat().st_mode & 0o077 or path != root/'observer.json'):
        raise ValueError('owned private exact root required')
    spec = importlib.util.spec_from_file_location('old_observer_io', c['observer_helper'])
    if hashlib.sha256(Path(c['observer_helper']).read_bytes()).hexdigest() != c['observer_helper_sha256']:
        raise ValueError('borrowed observer primitive pin')
    helper = importlib.util.module_from_spec(spec); spec.loader.exec_module(helper)
    if hashlib.sha256(helper.regular(c['go'], 256*1024**2)[0]).hexdigest() != c['go_sha256']:
        raise ValueError('fixed real Go pin')
    if (os.environ['PATH'] != str(root/'bin')+':'+c['base_path']
            or str(root/'bin') in c['base_path'].split(':')
            or not Path.cwd().resolve().is_relative_to(root)):
        raise ValueError('one observer PATH prefix and owned cwd required')
    return c, helper, root


def generated(cwd, root):
    platform = cwd.parent.name
    work = cwd.parent.parent
    if (cwd.resolve(strict=True) != cwd or cwd.name != 'src-arm64'
            or platform not in {'ios', 'iossimulator'} or work.parent != root/'tmp'
            or not re.fullmatch('gomobile-work-[A-Za-z0-9_-]+', work.name)):
        raise ValueError('exact actual Apple platform/arm64 generated cwd required')
    if (os.environ.get('GOOS') != 'ios' or os.environ.get('GOARCH') != 'arm64'
            or os.environ.get('DARWIN_SDK') != ('iphoneos' if platform == 'ios' else 'iphonesimulator')):
        raise ValueError('actual Apple compiler environment required')
    return platform, cwd.parent


def snapshot(helper, source, output):
    rows, size, error = [], 0, None
    try:
        for name in ['src', 'src-arm64']:
            for current, dirs, files in os.walk(source/name, followlinks=False, onerror=lambda e: (_ for _ in ()).throw(e)):
                for entry in dirs:
                    if (Path(current)/entry).is_symlink():
                        raise ValueError('generated symlink directory')
                for entry in sorted(files):
                    p = Path(current)/entry
                    body, info = helper.regular(p)
                    size += len(body)
                    if size > 64*1024**2 or len(rows) >= 4096:
                        raise ValueError('generated snapshot cap')
                    relative = str(p.relative_to(source))
                    helper.put(output/relative, body)
                    rows.append({'path': relative, 'bytes': len(body), 'sha256': helper.digest(body)})
        names = {x['path'] for x in rows}
        if ('src-arm64/go.mod' not in names or not any(x.endswith('.go') for x in names)
                or not any(x.endswith('.m') for x in names) or not any(x.endswith('.h') for x in names)):
            raise ValueError('actual generated Go/ObjC/header/module required')
    except BaseException as exc:
        error = type(exc).__name__
        raise
    finally:
        helper.record(output/'manifest.json', {'files': rows, 'bytes': size, 'error': error,
                     'complete_source_snapshot': error is None})
    return rows


def main():
    c, helper, root = configuration()
    argv = sys.argv[1:]
    if len(argv) > 512 or sum(len(a.encode()) for a in argv) > 65536:
        raise ValueError('Go argv cap')
    tidy = argv == ['mod', 'tidy']
    if not tidy and 'tidy' in argv:
        raise ValueError('unexpected tidy argv')
    child, received, signal_time, before, after, evidence = None, None, None, None, None, None
    handlers = {}
    def forward(number, _frame):
        nonlocal received, signal_time
        if received is None:
            received, signal_time = number, time.monotonic()
            if child is not None and child.poll() is None:
                child.send_signal(number)
    for number in [signal.SIGINT, signal.SIGTERM, signal.SIGHUP]:
        handlers[number] = signal.signal(number, forward)
    code, failure = None, None
    try:
        if tidy:
            platform, source = generated(Path.cwd(), root)
            evidence = root/'evidence/generated'/platform
            helper.record(evidence/'intent.json', {'argv': argv, 'cwd': str(Path.cwd()), 'platform': platform,
                          'GOOS': 'ios', 'GOARCH': 'arm64', 'real_generated_sources': True})
            before = snapshot(helper, source, evidence/'before')
        if received:
            raise InterruptedError('signal before Go dispatch')
        env = dict(os.environ); env['PATH'] = c['base_path']
        # Inherit stdio and the outer exact process group; no shell/recursive go.
        child = subprocess.Popen([c['go'], *argv], env=env)
        while child.poll() is None:
            if signal_time and time.monotonic()-signal_time > 8:
                child.kill()
            time.sleep(0.05)
        code = child.wait()
    except BaseException as exc:
        failure = type(exc).__name__
        raise
    finally:
        try:
            if child is not None and child.poll() is None:
                child.kill(); child.wait()
            if tidy and before is not None:
                after = snapshot(helper, source, evidence/'after')
                ordinary = lambda rows: {x['path']: x['sha256'] for x in rows if not x['path'].endswith(('/go.mod', '/go.sum'))}
                unchanged = ordinary(before) == ordinary(after)
                helper.record(evidence/'result.json', {'exit_code': code, 'signal': received,
                              'failure_type': failure, 'source_unchanged': unchanged})
                if not unchanged:
                    raise ValueError('tidy changed actual generated source')
        finally:
            for number, handler in handlers.items():
                signal.signal(number, handler)
    if received is not None:
        return 128+received
    if code is not None and code < 0:
        return 128-code
    return code


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception as error:
        print('Apple observer refused: '+type(error).__name__, file=sys.stderr)
        raise SystemExit(75)
