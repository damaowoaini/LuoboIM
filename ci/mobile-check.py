#!/usr/bin/env python3
"""Cloud-only validation: exact sources, completed JSON tests, guarded phases.

`run` is entered only inside the fresh Linux network namespace. Setup is the
only network-enabled phase. These checks do not claim native/device acceptance.
"""
import argparse
import errno
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import threading
import time
from urllib.parse import unquote, urlparse

REPO = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('mobile_source', REPO / 'ci/mobile-source.py')
source = importlib.util.module_from_spec(spec)
spec.loader.exec_module(source)
FLOOR = 1024 ** 3
INITIAL = 4 * FLOOR
CAP = 768 * 1024 ** 2


def write_json(path, value):
    with path.open('x') as f:
        json.dump(value, f, indent=2, sort_keys=True)
        f.write('\n')
        f.flush()
        os.fsync(f.fileno())


def verify(work, cohort):
    plan, files, digest = source.load_sources(cohort)
    for name, body in files.items():
        actual = source.regular(work / 'source' / source.safe_name(name))
        if actual != body:
            raise ValueError('workspace source changed: ' + name)
    config = work / 'source/app/.dart_tool/package_config.json'
    if config.exists():
        for item in json.loads(source.regular(config))['packages']:
            u = urlparse(item['rootUri'])
            if u.scheme not in ('', 'file') or u.netloc:
                raise ValueError('nonlocal package mapping')
            p = Path(unquote(u.path))
            p = (p if p.is_absolute() else config.parent / p).resolve()
            roots = [work / 'flutter', work / 'pub-cache',
                     work / 'source/app', work / 'source/packages/flutter_openim_sdk']
            if not any(p == root or p.is_relative_to(root) for root in roots):
                raise ValueError('package outside fresh source/tool/dependency roots')
            if item['name'] == 'imapp' and p != work / 'source/app':
                raise ValueError('self package must be this cohort')
            if item['name'] == 'flutter_openim_sdk' and p != work / 'source/packages/flutter_openim_sdk':
                raise ValueError('SDK must be pinned portable SDK source')
    return plan, digest


def machine_summary(text, expected):
    starts, finished, footers = set(), {}, []
    errors = 0
    for line in text.splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue  # Hook/tool informational output is retained in the log.
        if not isinstance(event, dict):
            continue
        kind = event.get('type')
        if footers and kind is not None:
            raise ValueError('reporter event after terminal done')
        if kind == 'testStart':
            identity = event['test']['id']
            if identity in starts:
                raise ValueError('duplicate test start')
            starts.add(identity)
        elif kind == 'testDone':
            identity = event['testID']
            if identity in finished or identity not in starts:
                raise ValueError('duplicate or unstarted test completion')
            if not isinstance(event.get('hidden'), bool) or not isinstance(event.get('skipped'), bool):
                raise ValueError('reporter completion flags missing')
            finished[identity] = event
        elif kind == 'error':
            errors += 1
        elif kind == 'done':
            footers.append(event.get('success'))
    visible = [e for e in finished.values() if not e['hidden']]
    result = {'passed': sum(e['result'] == 'success' and not e['skipped'] for e in visible),
              'failed_including_hidden': sum(e['result'] != 'success' for e in finished.values()),
              'skipped_including_hidden': sum(e['skipped'] for e in finished.values()),
              'hidden': sum(e['hidden'] for e in finished.values()),
              'started': len(starts), 'completed': len(finished), 'error_events': errors,
              'complete_success_footer': len(footers) == 1 and footers[0] is True}
    if (not result['complete_success_footer'] or not starts or starts != finished.keys()
            or errors or result['failed_including_hidden'] or result['skipped_including_hidden']
            or result['passed'] != expected):
        raise ValueError('incomplete/failing/unexpected test result: ' + json.dumps(result))
    return result


def allocated(root):
    total = 0
    def visit(directory):
        nonlocal total
        try:
            with os.scandir(directory) as entries:
                for entry in entries:
                    try:
                        info = entry.stat(follow_symlinks=False)
                        total += info.st_blocks * 512
                        if entry.is_dir(follow_symlinks=False):
                            visit(entry.path)
                    except FileNotFoundError:
                        pass  # Generated hook directories can disappear mid-scan.
        except FileNotFoundError:
            pass
    visit(root)
    return total


class Guard:
    def __init__(self, work):
        self.work = work
        self.reason = None
        self.minimum = None
        self.peak = 0
        self.stop = threading.Event()
        self.lock = threading.Lock()

    def sample(self):
        with self.lock:
            v = os.statvfs(self.work)
            free = v.f_bavail * v.f_frsize
            used = allocated(self.work / 'source')
            self.minimum = free if self.minimum is None else min(self.minimum, free)
            self.peak = max(self.peak, used)
            self.reason = self.reason or ('disk_floor' if free < FLOOR else None)
            self.reason = self.reason or ('workspace_cap' if used > CAP else None)
            return free

    def poll(self):
        while not self.stop.wait(.2):
            try:
                self.sample()
            except BaseException:
                with self.lock:
                    self.reason = self.reason or 'monitor_infrastructure_failed'


def guarded(command, work, guard, label, timeout=900):
    log = work / 'evidence' / (label + '.log')
    child = None
    started = time.monotonic()
    guard.sample()
    if guard.reason:
        raise ValueError('resource admission failed: ' + guard.reason)
    print(json.dumps({'event': 'mobile_phase_start', 'phase': label}), flush=True)
    try:
        with log.open('x') as stream:
            child = subprocess.Popen(command, cwd=work / 'source/app', stdout=stream,
                                     stderr=subprocess.STDOUT, start_new_session=True)
            while child.poll() is None:
                if time.monotonic() - started > timeout:
                    guard.reason = guard.reason or 'phase_timeout'
                if guard.reason:
                    os.killpg(child.pid, signal.SIGTERM)
                    try:
                        child.wait(timeout=3)
                    except subprocess.TimeoutExpired:
                        os.killpg(child.pid, signal.SIGKILL)
                        child.wait()
                    break
                time.sleep(.1)
        guard.sample()  # A fast-exiting child's final allocation still counts.
        record = {'phase': label, 'command': command, 'child_exit_code': child.returncode,
                  'elapsed_seconds': round(time.monotonic() - started, 3),
                  'minimum_free_bytes': guard.minimum, 'peak_workspace_allocated_bytes': guard.peak,
                  'latched_failure': guard.reason, 'log_sha256': source.sha(log.read_bytes())}
        write_json(work / 'evidence' / (label + '.resource.json'), record)
        print(json.dumps({'event': 'mobile_phase_end', 'phase': label,
                          'child_exit_code': child.returncode,
                          'latched_failure': guard.reason,
                          'elapsed_seconds': record['elapsed_seconds']}), flush=True)
        if guard.reason or child.returncode != 0:
            raise ValueError('phase failed: ' + label)
        return log
    finally:
        if child is not None and child.poll() is None:
            os.killpg(child.pid, signal.SIGKILL)
            child.wait()


def prove_namespace():
    # /proc exposes the current network namespace's interfaces and routes.
    links = subprocess.check_output(['ip', '-o', 'link', 'show'], text=True).splitlines()
    if len(links) != 1 or ': lo:' not in links[0]:
        raise ValueError('fresh loopback-only network namespace required')
    for family in ('-4', '-6'):
        if subprocess.check_output(['ip', family, 'route', 'show', 'default']).strip():
            raise ValueError('external route present')
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as client:
        client.settimeout(.2)
        try:
            client.connect(('192.0.2.1', 9))
        except OSError as e:
            if e.errno != errno.ENETUNREACH:
                raise ValueError('external namespace denial must be no-route') from e
        else:
            raise ValueError('external network reachable')


def run(work, cohort):
    prove_namespace()
    plan, digest = verify(work, cohort)
    guard = Guard(work)
    if guard.sample() < INITIAL or guard.reason:
        raise ValueError('initial free space 4GiB and workspace cap required')
    thread = threading.Thread(target=guard.poll, daemon=True)
    thread.start()
    try:
        flutter = str(work / 'flutter/bin/flutter')
        tests = [flutter, 'test', '--no-pub', '--machine', '--concurrency=1', '--no-enable-vmservice']
        counts = {}
        if cohort == 'booking-custom249':
            target_expected = plan['candidate249']['expected_target_tests']
            target_label = 'target' + str(target_expected)
            log = guarded(tests + plan['candidate249']['target_paths'], work, guard, target_label)
            counts['target'] = machine_summary(log.read_text(), target_expected)
            write_json(work / 'evidence' / (target_label + '.summary.json'), counts['target'])
        expected = 548 if cohort == 'current230' else plan['candidate249']['expected_full_tests']
        log = guarded(tests, work, guard, 'full' + str(expected))
        counts['full'] = machine_summary(log.read_text(), expected)
        write_json(work / 'evidence/full.summary.json', counts['full'])
        guarded([flutter, 'analyze', '--no-pub', '--fatal-infos', '--fatal-warnings'],
                work, guard, 'analyze')
        verify(work, cohort)
        passed = {'state': 'completed_candidate_pending_native_acceptance' if cohort != 'current230'
                  else 'completed_current_source_unit_widget_analysis',
                  'cohort': cohort, 'authored_source_digest': digest,
                  'counts': counts, 'native_or_service_acceptance': False,
                  'network': 'fresh network namespace; synthetic in-process loopback only'}
        write_json(work / 'evidence/validation-passed.json', passed)
        guard.sample()  # Receipt write/fsync remains under the continuous poll.
    except BaseException as error:
        write_json(work / 'evidence/validation-failed.json',
                   {'state': 'failed_or_incomplete_not_passed', 'type': type(error).__name__,
                    'detail': str(error), 'latched_failure': guard.reason})
        raise
    finally:
        try:
            write_json(work / 'evidence/final-resource.json',
                       {'minimum_free_bytes_before_resource_receipt': guard.minimum,
                        'peak_workspace_allocated_bytes': guard.peak,
                        'latched_failure': guard.reason, 'failure_wins_over_passed_receipt': True})
            guard.sample()
        finally:
            guard.stop.set()
            thread.join()
        # Even final receipt writes are polled. A failure from an already
        # running sample wins over any earlier validation-passed candidate.
    if guard.reason:
        raise ValueError('latched final failure; no acceptance: ' + guard.reason)
    print(json.dumps({'cohort': cohort, 'state': 'PASS', 'counts': counts}))


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('mode', choices=('verify', 'run'))
    p.add_argument('--work', type=Path, required=True)
    p.add_argument('--cohort', choices=('current230', 'booking-custom249'), required=True)
    a = p.parse_args()
    if not a.work.is_absolute() or a.work.resolve() != a.work or not a.work.is_dir():
        raise ValueError('canonical fresh CI root required')
    if a.mode == 'verify':
        _, digest = verify(a.work, a.cohort)
        print(json.dumps({'state': 'verified', 'cohort': a.cohort, 'source_digest': digest}))
    else:
        run(a.work, a.cohort)


if __name__ == '__main__':
    main()
