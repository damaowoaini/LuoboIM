#!/usr/bin/env python3
"""Cloud-only applicant-identity regression at the actual JoinGroup RPC entry."""
import argparse
from collections import Counter
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys

REPO = Path(__file__).resolve().parent.parent
CONTRACT = REPO / 'ci/group-join-auth.inputs.json'


def pin(row):
    path = Path(row['path'])
    if path.is_absolute() or '..' in path.parts or str(path) != row['path']:
        raise ValueError('noncanonical pinned path')
    item = REPO
    for part in path.parts:
        item = item / part
        if item.is_symlink():
            raise ValueError('symlink input')
    data = item.read_bytes()
    if len(data) != row['bytes'] or hashlib.sha256(data).hexdigest() != row['sha256']:
        raise ValueError('source pin mismatch')


def verify():
    contract = json.loads(CONTRACT.read_text())
    if contract['schema'] != 1 or len(contract['current']) != 2 or len(contract['supporting']) != 7:
        raise ValueError('unexpected source contract')
    for row in [*contract['current'], *contract['supporting'], contract['before']]:
        # The supporting group member test remains byte-exact b53. The group
        # proto/user/HTTP/helper pins are dependencies, not new claimed tests.
        pin(row)
    return contract


def audit(events, package, exit_code, shared=None, before=False, full_expected=None):
    terminal, started, output, per_test = {}, set(), [], {}
    footer = []
    for event in events:
        if not isinstance(event, dict) or event.get('Package') != package:
            raise ValueError('malformed or foreign test event')
        action, test = event.get('Action'), event.get('Test')
        if action == 'run' and test:
            if test in started:
                raise ValueError('duplicate test start')
            started.add(test)
        if action == 'output':
            text = event.get('Output', '')
            if not isinstance(text, str):
                raise ValueError('invalid output event')
            output.append(text)
            if test:
                per_test.setdefault(test, []).append(text)
        if action in ('pass', 'fail', 'skip'):
            if test:
                if test in terminal:
                    raise ValueError('duplicate test completion')
                terminal[test] = action
            else:
                footer.append(action)
    text = ''.join(output)
    if (not terminal or started != set(terminal) or 'skip' in terminal.values() or
            re.search(r'panic:|WARNING: DATA RACE|\[build failed\]|test timed out', text)):
        raise ValueError('incomplete, skipped, panicked, raced or uncompiled suite')
    counts = dict(Counter(('sub_' if '/' in name else 'top_') + state
                          for name, state in terminal.items()))
    if shared is not None:
        tops = {name for name in terminal if '/' not in name}
        if tops != set(shared['expected_top_tests']):
            raise ValueError('shared test set changed')
    if before:
        leaves = {name: state for name, state in terminal.items()
                  if not any(other.startswith(name + '/') for other in terminal)}
        failures = {name for name, state in leaves.items() if state == 'fail'}
        if exit_code != 1 or footer != ['fail'] or failures != set(shared['expected_failing_assertion_leaves']):
            raise ValueError('before did not reproduce exactly the expected identity assertions')
        if counts != shared['expected_before_counts']:
            raise ValueError('before test inventory changed')
        for name in shared['mandatory_positive_controls']:
            if terminal.get(name) != 'pass':
                raise ValueError('before legitimate join control failed')
        for name in failures:
            observed = ''.join(per_test.get(name, []))
            if not all(marker in observed for marker in shared['required_failure_evidence']):
                raise ValueError('before failure did not prove writer admission through the complete dependency chain')
    else:
        if exit_code != 0 or footer != ['pass'] or any(state != 'pass' for state in terminal.values()):
            raise ValueError('required suite failed')
        expected = shared['expected_after_counts'] if shared is not None else full_expected
        if expected is not None and counts != expected:
            raise ValueError('after test inventory changed')
    return {'state': 'expected_before_assertions' if before else 'passed',
            'test_exit': exit_code, 'terminal': terminal, 'counts': counts}


def run(stage):
    if any(os.environ.get(key) != value for key, value in {
        'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux'
    }.items()):
        raise ValueError('fresh GitHub-hosted Linux runner required')
    contract = verify()
    output = REPO / 'ci-results' / ('group-join-auth-' + stage)
    output.mkdir(parents=True, exist_ok=False)
    config = REPO / 'server/config/config.yaml'
    if config.exists() or config.is_symlink():
        raise ValueError('existing runtime configuration refused')
    shutil.copyfile(REPO / 'server/config/config.example.yaml', config)
    if stage == 'before':
        shutil.copyfile(REPO / contract['before']['path'], REPO / 'server/internal/rpc/group/group.go')
    summaries = []

    def test(label, pattern=None, shared=False):
        args = ['go', 'test', '-mod=readonly', '-race', '-json', '-count=1', '-p=1',
                '-parallel=1', '-timeout=5m', './internal/rpc/group']
        if pattern:
            args += ['-run', pattern]
        with (output / (label + '.jsonl')).open('x') as stdout, (output / (label + '.stderr.log')).open('x') as stderr:
            process = subprocess.run(args, cwd=REPO / 'server', stdout=stdout, stderr=stderr, timeout=360)
        events = [json.loads(line) for line in (output / (label + '.jsonl')).read_text().splitlines() if line.strip()]
        result = audit(events, 'github.com/openimsdk/open-im-server/v3/internal/rpc/group',
                       process.returncode, contract['shared'] if shared else None,
                       before=shared and stage == 'before',
                       full_expected=contract['full_after_expected'] if not shared else None)
        summaries.append({'phase': label, **result})

    test('shared-rpc', contract['shared']['run_regex'], shared=True)
    if stage == 'after':
        test('full-group')
        with (output / 'vet.log').open('x') as log:
            process = subprocess.run(['go', 'vet', '-mod=readonly', '-p=1', './internal/rpc/group'],
                                     cwd=REPO / 'server', stdout=log, stderr=subprocess.STDOUT, timeout=360)
        if process.returncode:
            raise ValueError('vet failed')
    receipt = {'state': 'accepted', 'stage': stage, 'commit': os.environ.get('GITHUB_SHA'),
               'before_context': contract['before_context'], 'phases': summaries,
               'native_runtime_or_database_acceptance': False}
    (output / 'summary.json').write_text(json.dumps(receipt, indent=2) + '\n')
    print(json.dumps({'state': 'accepted', 'stage': stage,
                      'phases': [{key: value for key, value in row.items() if key != 'terminal'} for row in summaries]}))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--stage', choices=('before', 'after'))
    parser.add_argument('--verify', action='store_true')
    args = parser.parse_args()
    try:
        if args.verify:
            verify()
            print('Exact JoinGroup applicant authorization inputs verified; no tests executed.')
        elif args.stage:
            run(args.stage)
        else:
            raise ValueError('explicit mode required')
    except (ValueError, KeyError, TypeError, OSError, subprocess.TimeoutExpired) as error:
        # Raw Go JSON/stderr/vet files remain uploadable by the always() step.
        # Only a fixed class is printed; no URI, token or callback body output.
        if args.stage:
            output = REPO / 'ci-results' / ('group-join-auth-' + args.stage)
            if output.is_dir():
                try:
                    with (output / 'failure.json').open('x') as failure:
                        json.dump({'state': 'failed', 'stage': args.stage, 'errorClass': type(error).__name__}, failure)
                except OSError:
                    pass
        print('JoinGroup applicant authorization CI failed: ' + type(error).__name__, file=sys.stderr)
        sys.exit(2)
