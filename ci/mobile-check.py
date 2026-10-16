#!/usr/bin/env python3
"""Cloud-only validation: exact sources, completed JSON tests, guarded phases.

`run` is entered only inside the fresh Linux network namespace. Setup is the
only network-enabled phase. These checks do not claim native/device acceptance.
"""
import argparse
from collections import Counter
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



def required_name_summary(text, expected, required):
    # Established complete count/hidden/error/skip/footer gate remains first.
    machine_summary(text, expected)
    starts, done = {}, {}
    for line in text.splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if not isinstance(event, dict):
            continue
        if event.get('type') == 'testStart':
            starts[event['test']['id']] = event['test'].get('name')
        elif event.get('type') == 'testDone':
            done[event['testID']] = event
    if any(not isinstance(name, str) or not name for name in starts.values()):
        raise ValueError('successful reporter test name missing')
    actual = Counter(starts[key] for key, event in done.items() if not event['hidden'])
    wanted = Counter()
    for row in required:
        name, count = row['name'], row['count']
        if (not isinstance(name, str) or not name or name in wanted
                or type(count) is not int or count < 1):
            raise ValueError('invalid required successful name row')
        wanted[name] = count
    if sum(wanted.values()) != expected or actual != wanted:
        raise ValueError('required successful name multiset differs; missing='
                         + repr(dict(wanted - actual)) + '; unexpected=' + repr(dict(actual - wanted)))
    return {'required': expected, 'matched': sum(actual.values()), 'exact_successful_name_multiset': True}


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
            used = allocated(self.work / 'source') + allocated(self.work / 'source-reference-entry')
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


def guarded(command, work, guard, label, timeout=900, *, cwd=None, accepted_exits=(0,)):
    log = work / 'evidence' / (label + '.log')
    child = None
    started = time.monotonic()
    guard.sample()
    if guard.reason:
        raise ValueError('resource admission failed: ' + guard.reason)
    print(json.dumps({'event': 'mobile_phase_start', 'phase': label}), flush=True)
    try:
        with log.open('x') as stream:
            child = subprocess.Popen(command, cwd=cwd or work / 'source/app', stdout=stream,
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
        if guard.reason or child.returncode not in accepted_exits:
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


def reference_entry_summary(text, child_exit, reference):
    """Accept only the named missing-entry assertion plus the old filter control.

    A nonzero process/loader/timeout/panic is never a reference reproduction.
    Hidden successful loads are counted separately; hidden failure is rejected.
    """
    starts, finished, errors, footers = {}, {}, [], []
    for line in text.splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if not isinstance(event, dict):
            continue
        kind = event.get('type')
        if footers and kind is not None:
            raise ValueError('reference event after terminal done')
        if kind == 'testStart':
            test = event['test']
            if test['id'] in starts:
                raise ValueError('reference duplicate test start')
            starts[test['id']] = test['name']
        elif kind == 'testDone':
            identity = event['testID']
            if identity in finished or identity not in starts:
                raise ValueError('reference duplicate or unstarted test completion')
            if not isinstance(event.get('hidden'), bool) or not isinstance(event.get('skipped'), bool):
                raise ValueError('reference completion flags missing')
            finished[identity] = event
        elif kind == 'error':
            errors.append(event)
        elif kind == 'done':
            footers.append(event.get('success'))
    visible_rows = [(starts[key], key, event) for key, event in finished.items() if not event['hidden']]
    if len(visible_rows) != 2 or len({row[0] for row in visible_rows}) != 2:
        raise ValueError('reference requires exactly two distinct visible test names')
    visible = {name: (key, event) for name, key, event in visible_rows}
    fail_name, pass_name = reference['failed_test_name'], reference['passed_test_name']
    if (child_exit != reference['expected_child_exit'] or len(footers) != 1
            or footers[0] is not False or set(starts) != set(finished)
            or len(visible) != 2 or set(visible) != {fail_name, pass_name}
            or any(event['skipped'] for event in finished.values())
            or any(event['result'] != 'success' for event in finished.values() if event['hidden'])
            or visible[pass_name][1]['result'] != 'success'
            or visible[fail_name][1]['result'] not in ('failure', 'error')
            or len(errors) != 1):
        raise ValueError('reference is not complete named 1assertFAIL/1controlPASS')
    error = errors[0]
    message = error.get('error', '')
    if (error.get('testID') != visible[fail_name][0] or error.get('isFailure') is not True
            or not isinstance(message, str)
            or not all(fragment in message for fragment in
                       ('Expected:', 'Actual:', 'Found 0', reference['expected_missing_tooltip']))
            or any(fragment in message.lower() for fragment in
                   ('timeout', 'timed out', 'panic', 'compilation failed', 'failed to load',
                    'unhandled exception', 'syntax error'))):
        raise ValueError('reference must be the exact missing-entry assertion, not load/runtime failure')
    return {'state': 'reference_only_expected_entry_assertion_failure', 'passed': 1,
            'assertion_failed': 1, 'visible': 2,
            'hidden_successful_loads': sum(event['hidden'] for event in finished.values()),
            'skipped': 0, 'unexpected_errors': 0, 'child_exit_code': child_exit,
            'not_historical257_full': True}


def run_reference_entry(work, cohort, plan, tests, guard):
    """Separate full336 tree; restore exact after4 even if the reference fails."""
    _, current, _ = source.load_sources(cohort)
    reference = source.reference_sources(plan, current)
    root = work / 'source-reference-entry'
    if root.exists() or root.is_symlink():
        raise ValueError('reference tree must be fresh; partial prior trees cannot resume')
    root.mkdir(mode=0o700)
    for name, body in reference.items():
        guard.sample()
        if guard.reason:
            raise ValueError('reference materialization stopped; partial retained')
        target = root / source.safe_name(name)
        target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        with target.open('xb') as stream:
            stream.write(body)
    # Only JSON dependency mappings are rebased. No build/stamp/dill/hook result
    # or package cache is copied. Official SQLite raw bytes get a new shared seed.
    config_path = work / 'source/app/.dart_tool/package_config.json'
    config = json.loads(source.regular(config_path))
    for item in config['packages']:
        uri = urlparse(item['rootUri'])
        if uri.scheme not in ('', 'file') or uri.netloc:
            raise ValueError('nonlocal reference package mapping')
        path = Path(unquote(uri.path))
        path = (path if path.is_absolute() else config_path.parent / path).resolve()
        if path.is_relative_to(work / 'source'):
            path = root / path.relative_to(work / 'source')
        if not any(path == allowed or path.is_relative_to(allowed) for allowed in
                   (root / 'app', root / 'packages/flutter_openim_sdk',
                    work / 'flutter', work / 'pub-cache')):
            raise ValueError('reference dependency escapes fresh roots')
        item['rootUri'] = path.as_uri() + '/'
    target_config = root / 'app/.dart_tool/package_config.json'
    target_config.parent.mkdir(parents=True, mode=0o700)
    write_json(target_config, config)
    sqlite = plan['sqlite_linux']
    native = source.regular(work / 'source' / sqlite['seed_path'])
    if source.sha(native) != sqlite['sha256']:
        raise ValueError('reference SQLite must be original verified raw binary')
    target = root / sqlite['seed_path']
    target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    with target.open('xb') as stream:
        stream.write(native)

    def verify_tree(expected):
        for name, body in expected.items():
            if source.regular(root / name) != body:
                raise ValueError('reference source changed: ' + name)

    try:
        verify_tree(reference)
        log = guarded(tests + [plan['reference_entry']['test_path']], work, guard,
                      'reference-entry2', cwd=root / 'app', accepted_exits=(1,))
        # guarded() records the actual child status; a TERM/guard failure wins.
        resource = json.loads((work / 'evidence/reference-entry2.resource.json').read_text())
        result = reference_entry_summary(log.read_text(), resource['child_exit_code'], plan['reference_entry'])
        verify_tree(reference)
        write_json(work / 'evidence/reference-entry2.summary.json', result)
        return result
    finally:
        for row in plan['reference_entry']['replacement_files']:
            target = root / row['path']
            if target.resolve() != target or not target.is_file():
                raise ValueError('reference restore path changed; partial retained')
            with target.open('wb') as stream:
                stream.write(current[row['path']])
                stream.flush()
                os.fsync(stream.fileno())
        verify_tree(current)
        verify(work, cohort)
        write_json(work / 'evidence/reference-after-restored.json',
                   {'state': 'after336_and_primary_source_reverified',
                    'source_digest': plan['current336']['source_digest'],
                    'restored_existing_files': 4, 'native_or_full257_result': False})


def call_records_cleanup_summary(text, contract):
    """Check source-enumerated cleanup per machine test ID after result/name gates.

    Expected close failures omit recorder.end but must finish SDK/storage. The
    repository-only tests use awaited teardown, not these fixture markers.
    """
    starts, call_rows, parent_rows, safe_rows = {}, {}, {}, {}
    prefixes = [('call_records_cleanup ', call_rows),
                ('creation_fixture_cleanup ', parent_rows),
                ('creation_safe_exit ', safe_rows)]
    for line in text.splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if not isinstance(event, dict):
            continue
        if event.get('type') == 'testStart':
            identity = event['test']['id']
            if identity in starts:
                raise ValueError('cleanup duplicate test start')
            starts[identity] = event['test'].get('name')
        if event.get('type') != 'print':
            continue
        message = event.get('message', '')
        if not isinstance(message, str):
            continue
        for prefix, target in prefixes:
            if message.startswith(prefix):
                identity = event.get('testID')
                if identity not in starts:
                    raise ValueError('cleanup print has no started test')
                target.setdefault(identity, []).append(message[len(prefix):])
    expected = {row['name']: row for row in contract['named_fixtures']}
    actual = {}
    for identity, markers in call_rows.items():
        name = starts[identity]
        if name not in expected or name in actual:
            raise ValueError('cleanup unexpected/duplicate call-record fixture')
        if markers != expected[name]['markers']:
            raise ValueError('cleanup call-record sequence differs: ' + str(name))
        actual[name] = markers
    if set(actual) != set(expected):
        raise ValueError('cleanup missing call-record fixture')
    if (len(parent_rows) != contract['parent_creation_instances']
            or any(markers != contract['parent_creation_markers'] for markers in parent_rows.values())):
        raise ValueError('cleanup parent creation sequence/count differs')
    expected_safe = contract['parent_safe_exit_markers']
    if ((expected_safe and len(safe_rows) != 1)
            or (not expected_safe and safe_rows)
            or any(markers != expected_safe for markers in safe_rows.values())):
        raise ValueError('cleanup parent safe-exit sequence/count differs')
    return {'call_record_fixtures': len(actual),
            'normal_recorder_closes': sum(row['kind'] != 'expected_close_failure'
                                           for row in expected.values()),
            'expected_close_failures': sum(row['kind'] == 'expected_close_failure'
                                            for row in expected.values()),
            'page_unmounts': sum(row['kind'] == 'page' for row in expected.values()),
            'repository_awaited_teardown_tests': len(contract['repository_awaited_teardown_names']),
            'parent_creation_fixtures': len(parent_rows),
            'parent_safe_exit_sequences': len(safe_rows),
            'all_emitted_sequences_exact': True,
            'repository_teardown_is_framework_awaited_not_marker_proof': True}


def next_unread_cleanup_summary(text, contract):
    starts, sequences = {}, {}
    prefix = 'next_unread_cleanup '
    for line in text.splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if not isinstance(event, dict):
            continue
        if event.get('type') == 'testStart':
            identity = event['test']['id']
            if identity in starts:
                raise ValueError('next unread cleanup duplicate test start')
            starts[identity] = event['test'].get('name')
        if event.get('type') == 'print' and isinstance(event.get('message'), str) and event['message'].startswith(prefix):
            identity = event.get('testID')
            if identity not in starts:
                raise ValueError('next unread cleanup print without test')
            sequences.setdefault(identity, []).append(event['message'][len(prefix):])
    actual = []
    for identity, sequence in sequences.items():
        name = starts[identity]
        if name not in contract['fixture_names'] or name in actual or sequence != contract['markers']:
            raise ValueError('next unread cleanup sequence/name differs')
        actual.append(name)
    if set(actual) != set(contract['fixture_names']):
        raise ValueError('next unread cleanup missing SDK shutdown/storage/unmount tail')
    return {'sdk_fixture_sequences': len(actual), 'four_markers_exact': True,
            'unit_awaited_teardown_cases': len(contract['unit_names']),
            'owned_geometry_finally_cases': len(contract['geometry_names']),
            'SDK_end_includes_awaited_session_gateway_support_directory_cleanup': True,
            'no_own_SQLite_or_marker_claim_for_other18': True}


def run(work, cohort, reference=False):
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
        if reference:
            counts['reference_entry'] = run_reference_entry(work, cohort, plan, tests, guard)
        verify(work, cohort)
        target_expected = plan['current336']['expected_target_tests']
        target_label = 'target' + str(target_expected)
        log = guarded(tests + plan['current336']['target_paths'], work, guard, target_label)
        counts['target'] = machine_summary(log.read_text(), target_expected)
        required = source.required_names(plan)
        counts['target_names'] = required_name_summary(log.read_text(), target_expected, required['current_target422'])
        cleanup = source.cleanup_contract(plan, required)
        counts['target_cleanup'] = call_records_cleanup_summary(log.read_text(), cleanup)
        next_cleanup = source.next_unread_contract(plan, required)
        counts['target_next_unread_cleanup'] = next_unread_cleanup_summary(log.read_text(), next_cleanup)
        write_json(work / 'evidence' / (target_label + '.next-unread-cleanup.json'), counts['target_next_unread_cleanup'])
        write_json(work / 'evidence' / (target_label + '.cleanup.json'), counts['target_cleanup'])
        write_json(work / 'evidence' / (target_label + '.summary.json'), counts['target'])
        verify(work, cohort)
        expected = plan['current336']['expected_full_tests']
        log = guarded(tests, work, guard, 'full' + str(expected))
        counts['full'] = machine_summary(log.read_text(), expected)
        counts['full_names'] = required_name_summary(log.read_text(), expected, required['current_full1104'])
        counts['full_cleanup'] = call_records_cleanup_summary(log.read_text(), cleanup)
        counts['full_next_unread_cleanup'] = next_unread_cleanup_summary(log.read_text(), next_cleanup)
        write_json(work / 'evidence/full.next-unread-cleanup.json', counts['full_next_unread_cleanup'])
        counts['parent922_names_preserved'] = True
        counts['new_next_unread29_exact'] = True
        write_json(work / 'evidence/full.cleanup.json', counts['full_cleanup'])
        counts['parent872_preserved'] = True
        counts['new_call_records50_exact'] = True
        counts['parent822_plus29_plus3_preserved'] = True
        counts['new_shortcuts18_exact'] = True
        write_json(work / 'evidence/full.summary.json', counts['full'])
        verify(work, cohort)
        guarded([flutter, 'analyze', '--no-pub', '--fatal-infos', '--fatal-warnings'],
                work, guard, 'analyze')
        verify(work, cohort)
        passed = {'state': 'completed_current_source_unit_widget_analysis',
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
    p.add_argument('--cohort', choices=('current336',), required=True)
    p.add_argument('--reference-before-entry', action='store_true',
                   help='explicit full336 variant; exactly one entry assertion fails and one old filter passes')
    a = p.parse_args()
    if not a.work.is_absolute() or a.work.resolve() != a.work or not a.work.is_dir():
        raise ValueError('canonical fresh CI root required')
    if a.mode == 'verify':
        _, digest = verify(a.work, a.cohort)
        print(json.dumps({'state': 'verified', 'cohort': a.cohort, 'source_digest': digest}))
    else:
        run(a.work, a.cohort, reference=a.reference_before_entry)


if __name__ == '__main__':
    main()
