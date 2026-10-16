#!/usr/bin/env python3
"""Run one task-owned command, retaining evidence even if monitoring fails."""
import argparse
import datetime
import json
import os
from pathlib import Path
import signal
import stat
import subprocess
import time


def allocated(root):
    if root is None:
        return 0
    if not stat.S_ISDIR(Path(root).lstat().st_mode):
        raise ValueError('allocation root must be a real directory')
    total = 0
    pending = [root]
    while pending:
        directory = pending.pop()
        try:
            with os.scandir(directory) as entries:
                for entry in entries:
                    try:
                        info = entry.stat(follow_symlinks=False)
                    except FileNotFoundError:
                        continue
                    if stat.S_ISREG(info.st_mode):
                        total += info.st_blocks * 512
                    elif stat.S_ISDIR(info.st_mode):
                        pending.append(entry.path)
        except FileNotFoundError:
            # Only disappearance races are ignored; permission/I/O failures stop.
            continue
    return total


def stop_group(child):
    """Stop only the exact new session, including any surviving descendants."""
    try:
        os.killpg(child.pid, signal.SIGTERM)
    except ProcessLookupError:
        child.wait()
        return
    until = time.monotonic() + 8
    while time.monotonic() < until:
        child.poll()
        try:
            os.killpg(child.pid, 0)
        except ProcessLookupError:
            child.wait()
            return
        time.sleep(0.05)
    try:
        os.killpg(child.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    child.wait()


def run(a):
    command = a.command[1:] if a.command[:1] == ['--'] else a.command
    started = time.monotonic()
    record = {'started_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
              'cwd': str(a.cwd), 'command': command,
              'minimum_free_required_bytes': a.min_free_mib * 1048576,
              'environment_values_recorded': False, 'guard_revision': 2}
    if a.allocated_root is not None:
        record.update(allocated_root=str(a.allocated_root),
                      max_allocated_allowed_bytes=a.max_allocated_mib * 1048576)
    minimum = maximum = final_free = code = None
    secondary = getattr(a, 'secondary_root', None)
    secondary_maximum = 0
    if secondary is not None:
        record.update(secondary_root=str(secondary), secondary_cap_bytes=a.secondary_cap_mib * 1048576)
    child = None
    reason = None

    def sample():
        nonlocal minimum, maximum, final_free, secondary_maximum
        info = os.statvfs(a.cwd)
        final_free = info.f_bavail * info.f_frsize
        minimum = final_free if minimum is None else min(minimum, final_free)
        size = allocated(a.allocated_root)
        maximum = size if maximum is None else max(maximum, size)
        secondary_size = allocated(secondary) if secondary is not None and (Path(secondary).exists() or Path(secondary).is_symlink()) else 0
        secondary_maximum = max(secondary_maximum, secondary_size)
        if final_free < a.min_free_mib * 1048576:
            return 'disk_gate'
        if a.max_allocated_mib is not None and size > a.max_allocated_mib * 1048576:
            return 'task_allocation_gate'
        if secondary is not None and secondary_size > a.secondary_cap_mib * 1048576:
            return 'build_subtree_allocation_gate'
        return None

    try:
        gate = sample()
        record['initial_free_bytes'] = final_free
        if gate:
            reason = 'not_started_' + gate
        else:
            a.log.parent.mkdir(parents=True, exist_ok=True)
            fd = os.open(a.log, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
            with os.fdopen(fd, 'wb') as output:
                child = subprocess.Popen(command, cwd=a.cwd, stdout=output,
                                         stderr=subprocess.STDOUT, start_new_session=True)
                record['child_process_group'] = child.pid
                while child.poll() is None:
                    gate = sample()
                    if gate:
                        reason = 'stopped_' + gate
                    elif time.monotonic() - started > a.timeout:
                        reason = 'stopped_timeout'
                    if reason:
                        stop_group(child)
                        break
                    time.sleep(a.poll_interval)
                code = child.wait()
                try:
                    os.killpg(child.pid, 0)
                except ProcessLookupError:
                    pass
                else:
                    reason = reason or 'stopped_remaining_process_group'
                    stop_group(child)
            gate = sample()
            if gate and reason is None:
                reason = 'failed_final_' + gate
    except BaseException as error:
        reason = 'infrastructure_failed'
        record['monitor_exception_type'] = type(error).__name__
        if child is not None:
            stop_group(child)
            code = child.returncode
    finally:
        if child is not None and child.poll() is None:
            stop_group(child)
            code = child.returncode
        record.update(status=reason or ('passed' if code == 0 else 'failed'), exit_code=code,
                      minimum_observed_free_bytes=minimum, final_free_bytes=final_free,
                      maximum_observed_task_allocated_bytes=maximum,
                      maximum_observed_secondary_allocated_bytes=secondary_maximum,
                      elapsed_seconds=round(time.monotonic() - started, 3), log=str(a.log))
        a.record.parent.mkdir(parents=True, exist_ok=True)
        descriptor = os.open(a.record, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(descriptor, 'w') as output:
            output.write(json.dumps(record, indent=2) + '\n')
            output.flush()
            os.fsync(output.fileno())
    print(json.dumps({key: record[key] for key in
                      ['status', 'exit_code', 'minimum_observed_free_bytes', 'elapsed_seconds']}))
    return 70 if reason == 'infrastructure_failed' else (75 if reason else code)


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--cwd', type=Path, required=True)
    p.add_argument('--log', type=Path, required=True)
    p.add_argument('--record', type=Path, required=True)
    p.add_argument('--min-free-mib', type=int, default=800)
    p.add_argument('--timeout', type=int, default=1800)
    p.add_argument('--allocated-root', type=Path)
    p.add_argument('--max-allocated-mib', type=int)
    p.add_argument('--poll-interval', type=float, default=1)
    p.add_argument('command', nargs=argparse.REMAINDER)
    a = p.parse_args()
    if not a.command or a.command == ['--']:
        p.error('command required')
    if (a.allocated_root is None) != (a.max_allocated_mib is None):
        p.error('allocated-root and max-allocated-mib must be provided together')
    if a.poll_interval < 0.1:
        p.error('poll interval must be at least 0.1 seconds')
    def interrupted(_number, _frame):
        raise InterruptedError('guard interrupted')
    signal.signal(signal.SIGTERM, interrupted)
    return run(a)


if __name__ == '__main__':
    raise SystemExit(main())
