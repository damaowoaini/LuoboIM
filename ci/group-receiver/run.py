#!/usr/bin/env python3
"""Isolated receiver mechanisms; no RPC, product, migration or runtime adoption."""
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
INPUTS = REPO / 'ci/group-receiver/inputs.json'
SCOPE = 'durable_group_receiver_stage2_mechanisms_only_no_RPC_or_runtime_adoption'
PREFIX = 'github.com/openimsdk/open-im-server/v3/'
REPLACEMENTS = {
    'server/pkg/common/db/unrelation/msg.go',
    'server/pkg/common/db/unrelation/msg_floor.go',
    'server/pkg/common/db/mgo/group.go',
    'server/pkg/common/db/mgo/group_member.go',
    'server/internal/retention/mongo.go',
    'server/internal/retention/commit.go',
}
PACKAGES = {'internal/groupeffects', 'internal/messagedelivery', 'internal/msgtransfer',
            'internal/push', 'internal/groupcleanup', 'internal/groupdelivery'}
VET = PACKAGES | {'internal/retention', 'pkg/common/db/unrelation', 'pkg/common/db/mgo'}


def sha(body):
    return hashlib.sha256(body).hexdigest()


def regular(path):
    if path.resolve() != path or not stat.S_ISREG(path.lstat().st_mode):
        raise ValueError('canonical regular source required')
    return path.read_bytes()


def name(value):
    p = PurePosixPath(value)
    if (not value or p.is_absolute() or str(p) != value or '\\' in value
            or any(x in ('', '.', '..') for x in value.split('/'))):
        raise ValueError('noncanonical source path')
    return value


def pinned(row):
    body = regular(REPO / name(row['path']))
    if len(body) != row['bytes'] or sha(body) != row['sha256']:
        raise ValueError('source identity differs')
    return body


def vector(rows):
    return sha(json.dumps(rows, sort_keys=True, ensure_ascii=False,
                          separators=(',', ':')).encode())


def verify():
    c = json.loads(regular(INPUTS))
    if c['schema'] != 1 or c['scope'] != SCOPE:
        raise ValueError('explicit receiver-only contract required')
    parent_bytes = regular(REPO / name(c['parent_inputs']['path']))
    if sha(parent_bytes) != c['parent_inputs']['sha256']:
        raise ValueError('accepted stage1 inputs differ')
    parent = json.loads(parent_bytes)
    if (len(parent['base_source']) != 927 or len(parent['foundation']) != 12
            or len(parent['effects']) != 15 or len(c['overlay']) != 41):
        raise ValueError('exact composition required')
    parent_vector = sorted(parent['base_source'] + parent['foundation'] + parent['effects'],
                           key=lambda r: r['path'])
    if (vector(parent_vector) != parent['source_vector_sha256']
            or parent['source_vector_sha256'] != c['parent_inputs']['source_vector_sha256']):
        raise ValueError('parent source aggregate differs')
    bodies = {}
    for section in ('base_source', 'foundation', 'effects'):
        for row in parent[section]:
            target = row['path'] if section == 'base_source' else row['path'].split('/source/', 1)[1]
            if not target.startswith('server/') or target in bodies:
                raise ValueError('parent source collision')
            bodies[name(target)] = pinned(row)
    changed, added, targets, owned = set(), set(), set(), set()
    for row in c['overlay']:
        target = name(row['target'])
        expected_path = 'ci/candidates/group-receiver/source/' + target
        if (not target.startswith('server/') or not target.endswith('.go')
                or row['path'] != expected_path or target in targets or row['mode'] != '0o644'):
            raise ValueError('foreign or duplicate receiver source')
        targets.add(target)
        owned.add(row['path'])
        if target in bodies:
            if target not in REPLACEMENTS or row['beforeSHA256'] != sha(bodies[target]):
                raise ValueError('replacement is not the exact accepted before')
            changed.add(target)
        else:
            if row['beforeSHA256'] is not None:
                raise ValueError('new source falsely claims a before')
            added.add(target)
        bodies[target] = pinned(row)
    if changed != REPLACEMENTS or len(added) != 35:
        raise ValueError('six replacements and35 new sources required')
    actual = {str(p.relative_to(REPO)) for p in
              (REPO / 'ci/candidates/group-receiver/source').rglob('*')
              if p.is_file() or p.is_symlink()}
    if actual != owned:
        raise ValueError('extra or missing receiver source')
    rows = [{'path':p, 'bytes':len(b), 'sha256':sha(b)} for p, b in sorted(bodies.items())]
    if (len(rows) != 989 or c['effective_source_count'] != 989
            or rows != c['effective_source'] or vector(rows) != c['effective_source_sha256']):
        raise ValueError('effective source identity differs')
    for row in c['dependencies']:
        pinned(row)
    if set(c['packages']) != PACKAGES or set(c['vet_packages']) != VET:
        raise ValueError('exact test/vet scope required')
    for package, spec in c['packages'].items():
        names = spec['top_tests']
        if (not names or names != sorted(set(names)) or any(not re.fullmatch(r'Test\w+', n) for n in names)
                or spec['run'] != '^(' + '|'.join(names) + ')$'):
            raise ValueError('explicit test set required')
    if (sum(len(s['top_tests']) for s in c['packages'].values()) != c['expected_top']
            or c['expected_top'] != 63
            or sum(s['sub_tests'] for s in c['packages'].values()) != 44
            or c['expected_sub'] != 44):
        raise ValueError('63top44sub required')
    return c, bodies


def audit(events, code, package, expected, sub_count):
    starts, ends, footer, output = set(), {}, [], []
    for e in events:
        if not isinstance(e, dict) or e.get('Package') != PREFIX + package or footer:
            raise ValueError('foreign/malformed/post-terminal event')
        n, action = e.get('Test'), e.get('Action')
        if action == 'run' and n:
            if n in starts:
                raise ValueError('duplicate test start')
            starts.add(n)
        elif action == 'output':
            output.append(e.get('Output', ''))
        elif action in ('pass', 'fail', 'skip'):
            if n:
                if n not in starts or n in ends:
                    raise ValueError('duplicate/unstarted completion')
                ends[n] = action
            else:
                footer.append(action)
    if (code != 0 or footer != ['pass'] or starts != set(ends)
            or any(v != 'pass' for v in ends.values())
            or {n for n in ends if '/' not in n} != set(expected)
            or sum('/' in n for n in ends) != sub_count
            or re.search(r'panic:|WARNING: DATA RACE|\[build failed\]|test timed out', ''.join(output))):
        raise ValueError('failed/skipped/raced/incomplete suite')
    return {'counts':dict(Counter(('sub_' if '/' in n else 'top_') + v for n, v in ends.items())),
            'terminal':ends, 'test_exit':code, 'scope':SCOPE}


def write(path, value):
    with path.open('x') as f:
        json.dump(value, f, indent=2)
        f.write('\n')
        f.flush()
        os.fsync(f.fileno())
    fd = os.open(str(path.parent), os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def run():
    required = {'GITHUB_ACTIONS':'true', 'RUNNER_ENVIRONMENT':'github-hosted', 'RUNNER_OS':'Linux',
                'LUOBO_STORAGE_TEST_MONGO_URI':'mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration',
                'LUOBO_RETENTION_TEST_REDIS':'redis://127.0.0.1:36379/15'}
    if any(os.environ.get(k) != v for k, v in required.items()):
        raise ValueError('fresh owned GitHub fixture required')
    c, bodies = verify()
    version = subprocess.check_output(['go','version'], text=True, timeout=15).strip()
    if version != 'go version go1.27.2 linux/amd64':
        raise ValueError('exact Go compiler required')
    subprocess.run([sys.executable, '-B', str(REPO / 'ci/backend-integration.py'), 'check-ready',
                    str(REPO / 'ci-results/backend-integration'),
                    os.environ['GITHUB_RUN_ID'] + ':' + os.environ['GITHUB_RUN_ATTEMPT'],
                    os.environ['GITHUB_SHA']], check=True, timeout=20)
    temporary = Path(os.environ['RUNNER_TEMP'])
    work = temporary / 'group-receiver-stage2'
    if not temporary.is_absolute() or temporary.resolve() != temporary or work.exists():
        raise ValueError('fresh canonical work root required')
    v = os.statvfs(temporary)
    if v.f_bavail * v.f_frsize < 4 * 1024 ** 3:
        raise ValueError('4GiB bootstrap floor required')
    work.mkdir(mode=0o700)
    output = REPO / 'ci-results/group-receiver-stage2'
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    for target, body in bodies.items():
        path = work / target
        path.parent.mkdir(parents=True, exist_ok=True)
        with path.open('xb') as stream:
            stream.write(body)
    for key, directory in (('GOMODCACHE','modcache'),('GOCACHE','buildcache'),('TMPDIR','tmp')):
        path = work / directory
        path.mkdir(mode=0o700)
        os.environ[key] = str(path)
    os.environ.update(GOTOOLCHAIN='local', GOMAXPROCS='2', GOMEMLIMIT='900MiB', GOWORK='off')
    record = {'scope':SCOPE, 'commit':os.environ['GITHUB_SHA'], 'input_sha256':sha(regular(INPUTS)),
              'effective_source_count':989, 'effective_source_sha256':c['effective_source_sha256'],
              'go_version':version, 'SDK_overlay_executed':False,
              'no_RPC_or_runtime_adoption':True, 'excluded_known_requirements':c['excluded_known_requirements']}
    write(output / 'source.json', record)

    def command(package, args, phase, timeout):
        folder = output / package.replace('/', '_')
        folder.mkdir(exist_ok=True)
        stdout, stderr = folder / (phase + '.log'), folder / (phase + '.stderr.log')
        receipt = folder / (phase + '.resource.json')
        capture = [sys.executable, '-B', str(REPO / 'ci/group-effects/capture.py'),
                   '--stdout', str(stdout), '--stderr', str(stderr), '--'] + args
        wrapped = [sys.executable, '-B', str(REPO / 'ci/group-effects/resource_guard.py'),
                   '--cwd', str(work / 'server'), '--log', str(folder / (phase + '.guard.log')),
                   '--record', str(receipt), '--min-free-mib','1024','--timeout',str(timeout),
                   '--poll-interval','0.2','--allocated-root',str(work),'--max-allocated-mib','16384',
                   '--'] + capture
        p = subprocess.run(wrapped, check=False, timeout=timeout + 30)
        resource = json.loads(regular(receipt))
        if p.returncode != 0 or resource['status'] != 'passed' or resource['exit_code'] != 0:
            raise ValueError('guarded phase incomplete; raw files retained')
        return stdout

    summaries = {}
    for package, spec in c['packages'].items():
        log = command(package, ['go','test','-mod=readonly','-p=1','-parallel=1','-race','-json',
                               '-count=1','-timeout=180s','-run',spec['run'],'./' + package], 'tests',900)
        events = [json.loads(line) for line in regular(log).decode().splitlines() if line.strip()]
        summary = audit(events, 0, package, spec['top_tests'], spec['sub_tests'])
        write(output / package.replace('/', '_') / 'summary.json', summary)
        summaries[package] = summary
    for package in c['vet_packages']:
        command(package, ['go','vet','-mod=readonly','-p=1','./' + package], 'vet',180)
    for target, body in bodies.items():
        if regular(work / target) != body:
            raise ValueError('source or dependency lock changed')
    verify()
    record.update(state='passed_receiver_mechanisms_only', packages=summaries,
                  actual_top=63, actual_sub=44, fail_or_skip=0, vet_exit={p:0 for p in c['vet_packages']})
    write(output / 'summary.json', record)
    print('Receiver mechanism suite completed; no RPC, migration, SDK or runtime adoption.')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--verify', action='store_true')
    args = parser.parse_args()
    try:
        if args.verify:
            verify()
            print('Receiver-only exact source inputs verified; no Go/DB execution.')
        else:
            run()
    except (ValueError, KeyError, TypeError, OSError, subprocess.SubprocessError):
        print('Receiver validation incomplete; retain raw capture and owned fixture diagnostics.', file=sys.stderr)
        raise SystemExit(2)
