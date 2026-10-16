#!/usr/bin/env python3
"""Isolated cloud validation of the group authority foundation, never RPC adoption."""
import argparse
from collections import Counter
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import signal
import stat
import subprocess
import sys

REPO = Path(__file__).resolve().parent.parent
INPUTS = REPO / 'ci/group-authority-foundation.inputs.json'
PACKAGE = 'github.com/openimsdk/open-im-server/v3/internal/groupauthority'
CAPSULE = 'ci/candidates/group-authority-foundation/source/server/internal/groupauthority/'


def sha(body):
    return hashlib.sha256(body).hexdigest()


def regular(path):
    if path.resolve() != path or not stat.S_ISREG(path.lstat().st_mode):
        raise ValueError('regular source with no symlink ancestors required')
    return path.read_bytes()


def pinned(row):
    name = row['path']
    path = PurePosixPath(name)
    if not name or path.is_absolute() or str(path) != name or '\\' in name or any(x in ('', '.', '..') for x in name.split('/')):
        raise ValueError('noncanonical input')
    body = regular(REPO / name)
    if len(body) != row['bytes'] or sha(body) != row['sha256']:
        raise ValueError('source pin differs: ' + name)
    return body


def verify():
    contract = json.loads(regular(INPUTS))
    if contract['schema'] != 1 or contract['scope'] != 'foundation_only_not_real_RPC_or_controller_adoption':
        raise ValueError('foundation scope contract required')
    rows = contract['source']
    paths = [row['path'] for row in rows]
    if len(set(paths)) != len(paths) or paths != sorted(paths) or len(paths) != contract['source_members']:
        raise ValueError('source inventory differs')
    for name in paths:
        relative = name.removeprefix(CAPSULE)
        if not name.startswith(CAPSULE) or '/' in relative or not relative.endswith('.go'):
            raise ValueError('exact isolated helper package required')
    actual = {str(p.relative_to(REPO)) for p in (REPO / CAPSULE).rglob('*') if p.is_file() or p.is_symlink()}
    if actual != set(paths):
        raise ValueError('extra or missing helper source')
    modules = contract['module_inputs']
    if {r['path'] for r in modules} != {'server/go.mod', 'server/go.sum', 'server/pkg/protocol/go.mod', 'server/pkg/protocol/go.sum'} or len(modules) != 4:
        raise ValueError('original locked module inputs required')
    for row in rows + contract['base_inputs'] + modules + [contract['before_adapter']]:
        pinned(row)
    digest = sha(json.dumps(rows, ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode())
    if digest != contract['source_digest']:
        raise ValueError('aggregate source digest differs')
    tops = contract['required_top_tests']
    if len(tops) != len(set(tops)) or not tops or any(not re.fullmatch(r'TestGroupAuthority\w+', n) for n in tops):
        raise ValueError('explicit complete top-test inventory required')
    return contract


def audit(events, code, contract, before=False):
    started, terminal, outputs, footer = set(), {}, [], []
    for event in events:
        if not isinstance(event, dict) or event.get('Package') != PACKAGE or footer:
            raise ValueError('malformed, foreign, or post-terminal event')
        action, name = event.get('Action'), event.get('Test')
        if action == 'run' and name:
            if name in started:
                raise ValueError('duplicate test start')
            started.add(name)
        elif action == 'output':
            outputs.append(event.get('Output', ''))
        elif action in ('pass', 'fail', 'skip'):
            if name:
                if name not in started or name in terminal:
                    raise ValueError('unstarted or repeated test terminal')
                terminal[name] = action
            else:
                footer.append(action)
    raw = ''.join(outputs)
    if not started or started != terminal.keys() or 'skip' in terminal.values() or re.search(r'panic:|WARNING: DATA RACE|\[build failed\]|test timed out', raw):
        raise ValueError('incomplete, skipped, panicked, raced or uncompiled test execution')
    tops = {name for name in terminal if '/' not in name}
    if before:
        if tops != set(contract['before_top_tests']) or code != 1 or footer != ['fail']:
            raise ValueError('before inventory or exit differs')
        if {name for name, state in terminal.items() if state == 'fail'} != set(contract['expected_before_failures']):
            raise ValueError('before did not reach exactly the required assertion')
        if contract['expected_before_message'] not in raw:
            raise ValueError('before lacks exact unauthorized-state evidence')
        if any(terminal.get(name) != 'pass' for name in contract['before_positive_controls']):
            raise ValueError('before legitimate positive control failed')
        if dict(Counter(('sub_' if '/' in n else 'top_') + v for n, v in terminal.items())) != {'top_fail': 1, 'top_pass': 1, 'sub_pass': 2}:
            raise ValueError('before full test inventory differs')
    elif code != 0 or footer != ['pass'] or tops != set(contract['required_top_tests']) or any(v != 'pass' for v in terminal.values()):
        raise ValueError('full foundation suite did not pass every required test')
    elif sum('/' in n for n in terminal) != contract['expected_sub_tests']:
        raise ValueError('full foundation subtest inventory differs')
    return {'state': 'expected_reference_before_assertion' if before else 'passed_foundation_only',
            'test_exit': code, 'counts': dict(Counter(('sub_' if '/' in n else 'top_') + v for n, v in terminal.items())),
            'terminal': terminal, 'real_RPCs_wired': 0, 'controller_writers_wired': 0,
            'runtime_or_full_permission_acceptance': False}


def run_process(command, cwd, stdout, stderr, timeout):
    with stdout.open('xb') as output, stderr.open('xb') as errors:
        process = subprocess.Popen(command, cwd=cwd, stdout=output, stderr=errors, start_new_session=True)
        try:
            return process.wait(timeout=timeout)
        except BaseException:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGTERM)
                try:
                    process.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait()
            raise


def run(stage):
    if any(os.environ.get(k) != v for k, v in {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux'}.items()):
        raise ValueError('fresh GitHub-hosted Linux runner required')
    if os.environ.get('LUOBO_STORAGE_TEST_MONGO_URI') != 'mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration':
        raise ValueError('explicit disposable loopback replica set required')
    contract = verify()
    label = os.environ['GITHUB_RUN_ID'] + ':' + os.environ['GITHUB_RUN_ATTEMPT']
    subprocess.run(['python3', '-B', str(REPO / 'ci/backend-integration.py'), 'check-ready',
                    str(REPO / 'ci-results/backend-integration'), label, os.environ['GITHUB_SHA']], check=True, timeout=15)
    temporary = Path(os.environ['RUNNER_TEMP'])
    work = temporary / ('group-authority-foundation-' + stage)
    if not temporary.is_absolute() or temporary.resolve() != temporary or not temporary.is_dir() or work.exists() or work.is_symlink():
        raise ValueError('canonical new runner workspace required')
    vfs = os.statvfs(temporary)
    if vfs.f_bavail * vfs.f_frsize < 4 * 1024 ** 3:
        raise ValueError('4GiB bootstrap floor required')
    work.mkdir(mode=0o700)
    package = work / 'server/internal/groupauthority'
    package.mkdir(parents=True)
    for row in contract['source']:
        (package / Path(row['path']).name).write_bytes(pinned(row))
    for row in contract['module_inputs']:
        target = work / row['path']
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(pinned(row))
    if stage == 'before':
        (package / 'barrier_adapter_test.go').write_bytes(pinned(contract['before_adapter']))
    output = REPO / 'ci-results' / ('group-authority-foundation-' + stage)
    output.mkdir(parents=True, exist_ok=False)
    source_record = {'commit': os.environ['GITHUB_SHA'], 'stage': stage, 'input_manifest_sha256': sha(regular(INPUTS)),
                     'source_digest': contract['source_digest'], 'source_members': contract['source_members'],
                     'module_inputs': contract['module_inputs'], 'before_adapter_applied': stage == 'before',
                     'scope': contract['scope'], 'existing_product_source_modified': False}
    (output / 'source.json').write_text(json.dumps(source_record, indent=2) + '\n')
    args = ['go', 'test', '-mod=readonly', '-race', '-json', '-count=1', '-p=1', '-parallel=1', '-timeout=8m', './internal/groupauthority']
    if stage == 'before':
        args += ['-run', '^TestGroupAuthorityBarrier']
    code = run_process(args, work / 'server', output / 'tests.jsonl', output / 'tests.stderr.log', 540)
    events = [json.loads(line) for line in (output / 'tests.jsonl').read_text().splitlines() if line.strip()]
    summary = audit(events, code, contract, before=stage == 'before')
    if stage == 'after':
        vet = run_process(['go', 'vet', '-mod=readonly', '-p=1', './internal/groupauthority'], work / 'server', output / 'vet.log', output / 'vet.stderr.log', 180)
        if vet != 0:
            raise ValueError('foundation vet failed')
        summary['vet_exit'] = vet
    for row in contract['module_inputs']:
        if regular(work / row['path']) != pinned(row):
            raise ValueError('module inputs changed during validation')
    for row in contract['source']:
        expected = pinned(contract['before_adapter']) if stage == 'before' and Path(row['path']).name == 'barrier_adapter_test.go' else pinned(row)
        if regular(package / Path(row['path']).name) != expected:
            raise ValueError('helper source changed during validation')
    verify()
    summary.update({'commit': os.environ['GITHUB_SHA'], 'scope': contract['scope'], 'stage': stage,
                    'actual_live_database_changes': False, 'fixture_residual_and_container_cleanup_gate_separate': True})
    (output / 'summary.json').write_text(json.dumps(summary, indent=2) + '\n')
    print(json.dumps({k: v for k, v in summary.items() if k != 'terminal'}))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--verify', action='store_true')
    parser.add_argument('--stage', choices=('before', 'after'))
    args = parser.parse_args()
    try:
        if args.verify and not args.stage:
            verify()
            print('Exact foundation inputs verified; no Go/Mongo/RPC execution.')
        elif args.stage and not args.verify:
            run(args.stage)
        else:
            raise ValueError('explicit single mode required')
    except (ValueError, KeyError, TypeError, OSError, subprocess.SubprocessError):
        print('Foundation CI rejected: incomplete or invalid evidence; see preserved raw outputs.', file=sys.stderr)
        sys.exit(2)
