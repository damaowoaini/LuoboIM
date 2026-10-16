#!/usr/bin/env python3
"""Cloud-only regression of the original and corrected group member writer."""
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
CONTRACT = REPO / 'ci/group-member-auth.inputs.json'


def read_json(path):
    return json.loads(path.read_text())


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
        raise ValueError('source pin mismatch: ' + row['path'])


def verify():
    contract = read_json(CONTRACT)
    if contract['schema'] != 1 or len(contract['current']) != 8:
        raise ValueError('unexpected source contract')
    for row in [*contract['current'], contract['before']]:
        pin(row)
    return contract


def audit(events, package, exit_code, contract=None, before=False):
    terminal, started, output = {}, set(), []
    footer = []
    for event in events:
        if not isinstance(event, dict) or event.get('Package') != package:
            raise ValueError('malformed or foreign test event')
        action, test = event.get('Action'), event.get('Test')
        if action == 'run' and test:
            started.add(test)
        if action == 'output':
            output.append(event.get('Output', ''))
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
    if before:
        leaves = {name: state for name, state in terminal.items()
                  if not any(other.startswith(name + '/') for other in terminal)}
        failures = {name for name, state in leaves.items() if state == 'fail'}
        expected = contract['expected_failing_assertion_leaves']
        if exit_code != 1 or footer != ['fail'] or failures != set(expected):
            raise ValueError('before did not reproduce exactly the expected permission assertions')
        for name in contract['mandatory_positive_controls']:
            if terminal.get(name) != 'pass':
                raise ValueError('before positive control failed')
        counts = Counter(('sub_' if '/' in name else 'top_') + state
                         for name, state in terminal.items())
        if dict(counts) != {'top_pass': 2, 'top_fail': 4, 'sub_pass': 7, 'sub_fail': 6}:
            raise ValueError('before test inventory changed')
    elif exit_code != 0 or footer != ['pass'] or any(state != 'pass' for state in terminal.values()):
        raise ValueError('required suite failed')
    return {'state': 'expected_before_assertions' if before else 'passed',
            'test_exit': exit_code, 'terminal': terminal,
            'top_pass': sum('/' not in name and state == 'pass' for name, state in terminal.items()),
            'sub_pass': sum('/' in name and state == 'pass' for name, state in terminal.items())}


def run(stage):
    if any(os.environ.get(k) != v for k, v in {
        'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux'
    }.items()):
        raise ValueError('fresh GitHub-hosted Linux runner required')
    contract = verify()
    output = REPO / 'ci-results' / ('group-member-auth-' + stage)
    output.mkdir(parents=True, exist_ok=False)
    config = REPO / 'server/config/config.yaml'
    if config.exists() or config.is_symlink():
        raise ValueError('existing runtime configuration refused')
    shutil.copyfile(REPO / 'server/config/config.example.yaml', config)
    if stage == 'before':
        shutil.copyfile(REPO / contract['before']['path'], REPO / 'server/internal/rpc/group/group.go')
    summaries = []

    def test(label, package, pattern=None, shared=False):
        args = ['go', 'test', '-mod=readonly', '-race', '-json', '-count=1', '-p=1',
                '-parallel=1', '-timeout=5m', package]
        if pattern:
            args += ['-run', pattern]
        with (output / (label + '.jsonl')).open('x') as stdout, (output / (label + '.stderr.log')).open('x') as stderr:
            process = subprocess.run(args, cwd=REPO / 'server', stdout=stdout, stderr=stderr, timeout=360)
        events = [json.loads(line) for line in (output / (label + '.jsonl')).read_text().splitlines() if line.strip()]
        summary = audit(events, 'github.com/openimsdk/open-im-server/v3/' + package[2:],
                        process.returncode, contract['shared'] if shared else None,
                        before=shared and stage == 'before')
        if shared and stage == 'after' and (summary['top_pass'] != 6 or summary['sub_pass'] != 13):
            raise ValueError('shared after regression inventory changed')
        summaries.append({'phase': label, **summary})

    test('shared-rpc', './internal/rpc/group', contract['shared']['run_regex'], shared=True)
    if stage == 'after':
        test('full-group', './internal/rpc/group')
        test('authority-controller', './pkg/common/db/controller', '^TestGroupPermissionReaderBypassesCacheAndPreservesContext$')
        test('bounded-mongo-query', './pkg/common/db/mgo', '^TestGroupMemberPermissionReadRejectsUnboundedInputs$')
        with (output / 'vet.log').open('x') as log:
            process = subprocess.run(['go', 'vet', '-mod=readonly', '-p=1', './internal/rpc/group',
                                      './pkg/common/db/controller', './pkg/common/db/mgo'],
                                     cwd=REPO / 'server', stdout=log, stderr=subprocess.STDOUT, timeout=360)
        if process.returncode:
            raise ValueError('vet failed')
    receipt = {'state': 'accepted', 'stage': stage, 'commit': os.environ.get('GITHUB_SHA'),
               'before_context': contract['before_context'], 'phases': summaries,
               'native_runtime_or_database_acceptance': False}
    (output / 'summary.json').write_text(json.dumps(receipt, indent=2) + '\n')
    print(json.dumps({'state': 'accepted', 'stage': stage,
                      'phases': [{k: v for k, v in row.items() if k != 'terminal'} for row in summaries]}))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--stage', choices=('before', 'after'))
    parser.add_argument('--verify', action='store_true')
    args = parser.parse_args()
    try:
        if args.verify:
            verify()
            print('Exact group member authorization inputs verified; no tests executed.')
        elif args.stage:
            run(args.stage)
        else:
            raise ValueError('explicit mode required')
    except (ValueError, KeyError, TypeError, OSError, subprocess.TimeoutExpired) as error:
        print('Group member authorization CI failed: ' + type(error).__name__, file=sys.stderr)
        sys.exit(2)
