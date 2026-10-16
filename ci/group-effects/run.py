#!/usr/bin/env python3
"""Test isolated typed group obligations; no RPC or runtime adoption."""
import argparse
from collections import Counter
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import stat
import subprocess
import sys

REPO = Path(__file__).resolve().parents[2]
INPUTS = REPO / 'ci/group-effects/inputs.json'
SCOPE = 'typed_effects_stage1_only_no_receiver_RPC_or_runtime_adoption'
PREFIX = 'github.com/openimsdk/open-im-server/v3/'


def sha(body):
    return hashlib.sha256(body).hexdigest()


def regular(path):
    if path.resolve() != path or not stat.S_ISREG(path.lstat().st_mode):
        raise ValueError('regular canonical source required')
    return path.read_bytes()


def pinned(row):
    name = row['path']
    p = PurePosixPath(name)
    if (not name or p.is_absolute() or str(p) != name or '\\' in name
            or any(x in ('', '.', '..') for x in name.split('/'))):
        raise ValueError('noncanonical input')
    body = regular(REPO / name)
    if len(body) != row['bytes'] or sha(body) != row['sha256']:
        raise ValueError('input differs: ' + name)
    return body


def verify():
    c = json.loads(regular(INPUTS))
    if c['schema'] != 1 or c['scope'] != SCOPE:
        raise ValueError('explicit stage1-only contract required')
    if (len(c['base_source']) != 927 or len(c['foundation']) != 12
            or len(c['effects']) != 15 or len(c['packages']) != 2):
        raise ValueError('exact source inventories required')
    seen, targets = set(), set()
    for section in ('base_source', 'foundation', 'effects'):
        for row in c[section]:
            name = row['path']
            if name in seen:
                raise ValueError('duplicate source')
            seen.add(name)
            if section == 'base_source':
                target = name
            elif section == 'foundation':
                target = name.removeprefix('ci/candidates/group-authority-foundation/source/')
                if not target.startswith('server/internal/groupauthority/'):
                    raise ValueError('foreign foundation input')
            else:
                target = name.removeprefix('ci/candidates/group-effects/source/')
                if not target.startswith(('server/internal/groupeffects/', 'server/cmd/tools/group-effect-recovery/')):
                    raise ValueError('foreign effect input')
            if not target.startswith('server/') or target in targets:
                raise ValueError('source target collision')
            targets.add(target)
            pinned(row)
    for row in c['controller_inputs']:
        pinned(row)
    vector = sorted(c['base_source'] + c['foundation'] + c['effects'], key=lambda r: r['path'])
    if sha(json.dumps(vector, ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode()) != c['source_vector_sha256']:
        raise ValueError('aggregate source identity differs')
    for pkg, spec in c['packages'].items():
        if pkg not in ('internal/groupeffects', 'cmd/tools/group-effect-recovery'):
            raise ValueError('unexpected package')
        names = spec['top_tests']
        if not names or len(set(names)) != len(names) or any(not re.fullmatch(r'Test\w+', x) for x in names):
            raise ValueError('explicit unique test names required')
    if sum(len(x['top_tests']) for x in c['packages'].values()) != 27:
        raise ValueError('all 27 prepared tests required')
    if sum(x['sub_tests'] for x in c['packages'].values()) != 18:
        raise ValueError('all 18 prepared subtests required')
    for section, prefix in (('foundation', 'ci/candidates/group-authority-foundation/source/'),
                            ('effects', 'ci/candidates/group-effects/source/')):
        actual = {str(p.relative_to(REPO)) for p in (REPO / prefix).rglob('*') if p.is_file() or p.is_symlink()}
        if actual != {r['path'] for r in c[section]}:
            raise ValueError('extra or missing capsule source')
    return c


def audit(events, code, package, expected, sub_count):
    starts, ends, footer, outputs = set(), {}, [], []
    for e in events:
        if not isinstance(e, dict) or e.get('Package') != PREFIX + package or footer:
            raise ValueError('foreign, malformed or post-terminal event')
        name, action = e.get('Test'), e.get('Action')
        if action == 'run' and name:
            if name in starts:
                raise ValueError('duplicate start')
            starts.add(name)
        elif action == 'output':
            outputs.append(e.get('Output', ''))
        elif action in ('pass', 'fail', 'skip'):
            if name:
                if name not in starts or name in ends:
                    raise ValueError('unstarted or duplicate completion')
                ends[name] = action
            else:
                footer.append(action)
    if (code != 0 or footer != ['pass'] or not starts or starts != set(ends)
            or any(v != 'pass' for v in ends.values())
            or {n for n in ends if '/' not in n} != set(expected)
            or sum('/' in n for n in ends) != sub_count
            or re.search(r'panic:|WARNING: DATA RACE|\[build failed\]|test timed out', ''.join(outputs))):
        raise ValueError('incomplete, skipped, failed or raced suite')
    return {'counts': dict(Counter(('sub_' if '/' in n else 'top_') + v for n, v in ends.items())),
            'terminal': ends, 'test_exit': code, 'scope': SCOPE}


def run():
    if any(os.environ.get(k) != v for k, v in {
            'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux',
            'LUOBO_STORAGE_TEST_MONGO_URI': 'mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration'}.items()):
        raise ValueError('fresh owned GitHub Linux fixture required')
    c = verify()
    version = subprocess.check_output(['go', 'version'], text=True, timeout=15).strip()
    if version != 'go version go1.27.2 linux/amd64':
        raise ValueError('exact Go compiler required')
    fixture = REPO / 'ci-results/backend-integration'
    subprocess.run([sys.executable, '-B', str(REPO / 'ci/backend-integration.py'), 'check-ready',
                    str(fixture), os.environ['GITHUB_RUN_ID'] + ':' + os.environ['GITHUB_RUN_ATTEMPT'],
                    os.environ['GITHUB_SHA']], check=True, timeout=20)
    temporary = Path(os.environ['RUNNER_TEMP'])
    work = temporary / 'group-effects-stage1'
    if not temporary.is_absolute() or temporary.resolve() != temporary or work.exists():
        raise ValueError('fresh canonical owned workspace required')
    vfs = os.statvfs(temporary)
    if vfs.f_bavail * vfs.f_frsize < 4 * 1024 ** 3:
        raise ValueError('4GiB bootstrap floor required')
    work.mkdir(mode=0o700)
    output = REPO / 'ci-results/group-effects-stage1'
    output.mkdir(parents=True, exist_ok=False)
    bodies = {}
    for section in ('base_source', 'foundation', 'effects'):
        for row in c[section]:
            name = row['path']
            target = name if section == 'base_source' else name.split('/source/', 1)[1]
            bodies[target] = pinned(row)
    for name, body in bodies.items():
        target = work / name
        target.parent.mkdir(parents=True, exist_ok=True)
        with target.open('xb') as stream:
            stream.write(body)
    for key, directory in (('GOMODCACHE', 'modcache'), ('GOCACHE', 'buildcache'), ('TMPDIR', 'tmp')):
        path = work / directory
        path.mkdir(mode=0o700)
        os.environ[key] = str(path)
    record = {'scope': SCOPE, 'commit': os.environ['GITHUB_SHA'], 'input_sha256': sha(regular(INPUTS)),
              'base_members': len(c['base_source']), 'foundation_members': 12, 'effects_members': 15,
              'source_vector_sha256': c['source_vector_sha256'], 'go_version': version,
              'live_runtime_or_product_adoption': False}
    (output / 'source.json').write_text(json.dumps(record, indent=2) + '\n')
    summaries = {}
    for package, spec in c['packages'].items():
        label = package.replace('/', '_')
        folder = output / label
        folder.mkdir()
        def command(args, phase, timeout):
            log, receipt = folder / (phase + '.log'), folder / (phase + '.resource.json')
            args = [sys.executable, '-B', str(REPO / 'ci/group-effects/resource_guard.py'),
                    '--cwd', str(work / 'server'), '--log', str(log), '--record', str(receipt),
                    '--min-free-mib', '1024', '--timeout', str(timeout), '--poll-interval', '0.2',
                    '--allocated-root', str(work), '--max-allocated-mib', '16384', '--'] + args
            p = subprocess.run(args, check=False, timeout=timeout + 30)
            resource = json.loads(regular(receipt))
            if p.returncode != 0 or resource['status'] != 'passed' or resource['exit_code'] != 0:
                raise ValueError('guarded ' + phase + ' failed; raw evidence retained')
            return log
        log = command(['go', 'test', '-mod=readonly', '-p=1', '-parallel=1', '-race', '-json',
                       '-count=1', '-timeout=180s', './' + package], 'tests', 900)
        events = [json.loads(line) for line in regular(log).decode().splitlines() if line.strip()]
        summary = audit(events, 0, package, spec['top_tests'], spec['sub_tests'])
        command(['go', 'vet', '-mod=readonly', '-p=1', './' + package], 'vet', 180)
        summary['vet_exit'] = 0
        (folder / 'summary.json').write_text(json.dumps(summary, indent=2) + '\n')
        summaries[package] = summary
    for name, body in bodies.items():
        if regular(work / name) != body:
            raise ValueError('source or dependency lock changed')
    verify()
    record.update(state='passed_stage1_only', packages=summaries)
    (output / 'summary.json').write_text(json.dumps(record, indent=2) + '\n')
    print('27 prepared stage1 top tests completed; receiver and RPC wiring remain separate.')


if __name__ == '__main__':
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--verify', action='store_true')
    a = p.parse_args()
    try:
        if a.verify:
            verify()
            print('Exact isolated typed-effects inputs verified; no Go or database execution.')
        else:
            run()
    except (ValueError, KeyError, TypeError, OSError, subprocess.SubprocessError):
        print('Typed-effects validation failed; preserve raw logs and fixture evidence.', file=sys.stderr)
        raise SystemExit(2)
