#!/usr/bin/env python3
"""Isolated full-package stage3 source validation; no runtime adoption."""
import argparse
from collections import Counter
import hashlib
import importlib.util
import json
import os
from pathlib import Path, PurePosixPath
import re
import stat
import subprocess
import sys

REPO = Path(__file__).resolve().parents[2]
INPUTS = REPO / 'ci/group-stage3/inputs.json'
SCOPE = 'stage3_source_full_packages_no_runtime_or_complete14RPC_claim'
PREFIX = 'github.com/openimsdk/open-im-server/v3/'
MONGO = 'mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration'
REDIS = 'redis://127.0.0.1:36379/15'
PACKAGES = {'cmd/openim-rpc/openim-rpc-group', 'internal/groupauthority',
            'internal/groupcleanup', 'internal/groupdelivery', 'internal/groupeffects',
            'internal/messagedelivery', 'internal/msgtransfer', 'internal/push',
            'internal/retention', 'internal/rpc/group', 'pkg/common/cmd',
            'pkg/common/db/cache', 'pkg/common/db/controller', 'pkg/common/db/mgo',
            'pkg/common/db/table/relation', 'pkg/common/db/unrelation', 'pkg/common/startrpc'}
LEGACY_SKIPS = {
    'pkg/common/db/controller': {'Test_BatchInsertChat2DB', 'Test_Insert', 'Test_Revoke',
                                 'Test_FindBySeq', 'TestName'},
    'internal/msgtransfer': {'TestActualKafkaMongoFailureRetainsOffsetAndCacheUntilRecovery'},
    'pkg/common/startrpc': {'TestStart'},
}


def sha(body):
    return hashlib.sha256(body).hexdigest()


def name(value):
    p = PurePosixPath(value)
    if (not value or p.is_absolute() or str(p) != value or '\\' in value
            or any(x in ('', '.', '..') for x in value.split('/'))):
        raise ValueError('noncanonical input path')
    return value


def regular(path):
    if path.resolve() != path or not stat.S_ISREG(path.lstat().st_mode):
        raise ValueError('canonical regular input required')
    return path.read_bytes()


def pinned(row):
    body = regular(REPO / name(row['path']))
    if len(body) != row['bytes'] or sha(body) != row['sha256']:
        raise ValueError('input identity mismatch')
    return body


def rows(bodies):
    return [{'path':p, 'bytes':len(b), 'sha256':sha(b)}
            for p, b in sorted(bodies.items())]


def digest(items):
    return sha(b''.join(r['path'].encode() + b'\0' + r['sha256'].encode() + b'\0'
                        for r in sorted(items, key=lambda r: r['path'])))


def load_receiver(row):
    pinned(row)
    spec = importlib.util.spec_from_file_location('stage3_parent_receiver', REPO / row['path'])
    receiver = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(receiver)
    return receiver


def apply_layer(c, key, prefix, bodies):
    targets, paths = set(), set()
    for row in c[key]:
        target = name(row['target'])
        if (not target.startswith('server/') or not target.endswith('.go')
                or row['path'] != prefix + target or row['mode'] != '0o644'
                or target in targets or row['path'] in paths):
            raise ValueError('foreign/duplicate overlay target')
        expected_before = sha(bodies[target]) if target in bodies else None
        if row['beforeSHA256'] != expected_before:
            raise ValueError('overlay parent differs')
        targets.add(target)
        paths.add(row['path'])
        bodies[target] = pinned(row)
    actual = {str(p.relative_to(REPO)) for p in (REPO / prefix).rglob('*')
              if p.is_file() or p.is_symlink()}
    if actual != paths:
        raise ValueError('extra/missing overlay member')


def verify():
    c = json.loads(regular(INPUTS))
    if c['schema'] != 1 or c['scope'] != SCOPE:
        raise ValueError('explicit source-only scope required')
    for row in c['dependencies']:
        pinned(row)
    if (c['receiver_runner']['path'] != 'ci/group-receiver/run.py'
            or c['receiver_inputs']['path'] != 'ci/group-receiver/inputs.json'):
        raise ValueError('exact accepted receiver gate required')
    pinned(c['receiver_inputs'])
    receiver = load_receiver(c['receiver_runner'])
    parent, bodies = receiver.verify()  # Same accepted source989/slot-fix gate.
    if (len(bodies) != 989 or parent['effective_source_sha256'] != c['parent989_digest']):
        raise ValueError('accepted parent989 differs')
    if len(c['stage3']) != 37 or len(c['fixture']) != 2:
        raise ValueError('exact stage3 and fixture layers required')
    apply_layer(c, 'stage3', 'ci/candidates/group-stage3/source/', bodies)
    if len(bodies) != 1016 or rows(bodies) != c['source1016'] or digest(rows(bodies)) != c['source1016_digest']:
        raise ValueError('pre-fixture source1016 differs')
    apply_layer(c, 'fixture', 'ci/candidates/group-stage3-fixture/source/', bodies)
    if len(bodies) != 1017 or rows(bodies) != c['fixture_source1017'] or digest(rows(bodies)) != c['fixture_source1017_digest']:
        raise ValueError('source1017 differs')
    if len(c['repair']) != 4:
        raise ValueError('exact two production and two new test repair files required')
    apply_layer(c, 'repair', 'ci/candidates/group-stage3-fix/source/', bodies)
    if len(bodies) != 1019 or rows(bodies) != c['source1019'] or digest(rows(bodies)) != c['source1019_digest']:
        raise ValueError('source1019 differs')
    if len(c['model_fixture']) != 2:
        raise ValueError('exact scoped membership fixture and helper required')
    apply_layer(c, 'model_fixture', 'ci/candidates/group-stage3-model-fixture/source/', bodies)
    if len(bodies) != 1020 or rows(bodies) != c['effective_source'] or digest(rows(bodies)) != c['effective_digest']:
        raise ValueError('source1020 differs')
    if set(c['packages']) != PACKAGES or sum(len(s['top_tests']) for s in c['packages'].values()) != 217:
        raise ValueError('complete17package217 source-name contract required')
    for package, spec in c['packages'].items():
        if name(package) != package:
            raise ValueError('canonical package required')
        actual = []
        for target, body in bodies.items():
            if str(PurePosixPath(target).parent) == 'server/' + package and target.endswith('_test.go'):
                actual.extend(re.findall(rb'^func (Test\w+)\(t \*testing.T\)', body, re.M))
        actual = sorted(x.decode() for x in actual)
        if actual != spec['top_tests'] or len(actual) != len(set(actual)):
            raise ValueError('full package test inventory differs')
        if (spec['allowed_legacy_skips'] != sorted(LEGACY_SKIPS.get(package, set()))
                or any(x not in actual for x in spec['allowed_legacy_skips'])):
            raise ValueError('foreign skip allowance')
    return c, bodies


def audit(events, code, package, spec):
    starts, ends, footer, output = set(), {}, [], []
    for e in events:
        if not isinstance(e, dict) or e.get('Package') != PREFIX + package or footer:
            raise ValueError('foreign/malformed/post-terminal JSON event')
        test, action = e.get('Test'), e.get('Action')
        if action == 'run' and test:
            if test in starts:
                raise ValueError('duplicate start')
            starts.add(test)
        elif action == 'output':
            output.append(e.get('Output', ''))
        elif action in ('pass', 'fail', 'skip'):
            if test:
                if test not in starts or test in ends:
                    raise ValueError('duplicate/unstarted terminal')
                ends[test] = action
            else:
                footer.append(action)
    expected = set(spec['top_tests'])
    compile_only = not expected
    permitted_skips = set(spec['allowed_legacy_skips'])
    skipped = sorted(n for n, a in ends.items() if a == 'skip')
    unexpected_skip = [n for n in skipped if n.split('/', 1)[0] not in permitted_skips]
    failures = sorted(n for n, a in ends.items() if a == 'fail')
    required_leaves = spec['required_leaves']
    missing_leaves = [n for n in required_leaves if ends.get(n) != 'pass']
    poison = bool(re.search(r'panic:|WARNING: DATA RACE|\[build failed\]|test timed out', ''.join(output)))
    complete = (starts == set(ends) and {n for n in ends if '/' not in n} == expected)
    good_footer = footer == ['pass'] or (compile_only and footer == ['skip'] and not ends
                                          and '[no test files]' in ''.join(output))
    sub_count = sum('/' in n for n in ends)
    sub_contract = spec.get('expected_sub')
    accepted = (code == 0 and complete and good_footer and not poison and not failures
                and not unexpected_skip and not missing_leaves
                and (sub_contract is None or sub_count == sub_contract))
    return {'accepted':accepted, 'counts':dict(Counter(('sub_' if '/' in n else 'top_') + a
                                                      for n, a in ends.items())),
            'terminal':ends, 'footer':footer, 'test_exit':code,
            'legacy_skips':skipped, 'unexpected_skip':unexpected_skip,
            'failed_tests':failures, 'missing_required_leaves':missing_leaves,
            'actual_sub':sub_count, 'compile_only_package':compile_only,
            'complete':complete, 'poison_output':poison, 'scope':SCOPE}


def write(path, value):
    with path.open('x') as f:
        json.dump(value, f, indent=2)
        f.write('\n')
        f.flush()
        os.fsync(f.fileno())
    fd = os.open(path.parent, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def cloud_environment():
    required = {'GITHUB_ACTIONS':'true', 'RUNNER_ENVIRONMENT':'github-hosted', 'RUNNER_OS':'Linux',
                'LUOBO_STORAGE_TEST_MONGO_URI':MONGO, 'LUOBO_RETENTION_TEST_REDIS':REDIS,
                'LUOBO_RUN_LEGACY_EXAMPLES':'0', 'LUOBO_RUN_LEGACY_LOAD_TESTS':'0',
                'LUOBO_EMAIL_INTEGRATION':'0'}
    if any(os.environ.get(k) != v for k, v in required.items()):
        raise ValueError('exact cloud fixture environment required; tests not started')
    if os.environ.get('LUOBO_INTEGRATION_KAFKA_BROKER'):
        raise ValueError('Kafka is outside this owned fixture')
    run_id, attempt = os.environ['GITHUB_RUN_ID'], os.environ['GITHUB_RUN_ATTEMPT']
    if not re.fullmatch(r'[0-9]+', run_id) or not re.fullmatch(r'[0-9]+', attempt):
        raise ValueError('exact fixture owner required')
    socket = Path('/tmp/luobo-it-' + run_id + '-' + attempt + '/references/integration/redis8.2/redis.sock')
    info = socket.lstat()
    if socket.resolve() != socket or not stat.S_ISSOCK(info.st_mode) or info.st_uid != os.getuid():
        raise ValueError('owned exact fixture socket missing')
    inherited = {'LUOBO_INTEGRATION_MONGO_URI':MONGO, 'LUOBO_RETENTION_TEST_MONGO':MONGO,
                 'LUOBO_MONGO_TEST_URI':MONGO, 'LUOBO_BOOKING_TEST_MONGO_URI':MONGO,
                 'LUOBO_BOOKING_TEST_REDIS_ADDR':'127.0.0.1:36379',
                 'LUOBO_INTEGRATION_REDIS_SOCKET':str(socket)}
    if any(k in os.environ and os.environ[k] != v for k, v in inherited.items()):
        raise ValueError('inherited fixture configuration differs')
    os.environ.update(inherited)


def run():
    cloud_environment()  # Missing/foreign fixture is a controller rejection, not a Go skip.
    c, bodies = verify()
    version = subprocess.check_output(['go', 'version'], text=True, timeout=15).strip()
    if version != 'go version go1.27.2 linux/amd64':
        raise ValueError('exact Go compiler required')
    subprocess.run([sys.executable, '-B', str(REPO / 'ci/backend-integration.py'), 'check-ready',
                    str(REPO / 'ci-results/backend-integration'),
                    os.environ['GITHUB_RUN_ID'] + ':' + os.environ['GITHUB_RUN_ATTEMPT'],
                    os.environ['GITHUB_SHA']], check=True, timeout=20)
    temporary = Path(os.environ['RUNNER_TEMP'])
    work = temporary / 'group-stage3-source'
    if not temporary.is_absolute() or temporary.resolve() != temporary or work.exists():
        raise ValueError('fresh canonical work path required')
    fs = os.statvfs(temporary)
    if fs.f_bavail * fs.f_frsize < 4 * 1024 ** 3:
        raise ValueError('4GiB bootstrap floor required')
    work.mkdir(mode=0o700)
    output = REPO / 'ci-results/group-stage3-source'
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    for target, body in bodies.items():
        path = work / target
        path.parent.mkdir(parents=True, exist_ok=True)
        with path.open('xb') as f:
            f.write(body)
        path.chmod(0o644)
    # Only the same public example the owned prepare step uses. Never copy a
    # runtime/private config from the checkout or a local service directory.
    template = bodies['server/config/config.example.yaml']
    with (work / 'server/config/config.yaml').open('xb') as f:
        f.write(template)
    for key, directory in (('GOMODCACHE','modcache'), ('GOCACHE','buildcache'), ('TMPDIR','tmp')):
        path = work / directory
        path.mkdir(mode=0o700)
        os.environ[key] = str(path)
    os.environ.update(GOTOOLCHAIN='local', GOMAXPROCS='2', GOMEMLIMIT='900MiB', GOWORK='off')
    write(output / 'source.json', {'scope':SCOPE, 'commit':os.environ['GITHUB_SHA'],
          'inputs_sha256':sha(regular(INPUTS)), 'effective_source_count':1020,
          'effective_digest':c['effective_digest'], 'go_version':version,
          'generated_public_config_sha256':sha(template), 'no_runtime_adoption':True})

    def command(package, args, phase, timeout):
        folder = output / package.replace('/', '_')
        folder.mkdir(exist_ok=True)
        stdout, stderr = folder / (phase + '.log'), folder / (phase + '.stderr.log')
        receipt = folder / (phase + '.resource.json')
        capture = [sys.executable, '-B', str(REPO / 'ci/group-effects/capture.py'),
                   '--stdout', str(stdout), '--stderr', str(stderr), '--'] + args
        wrapped = [sys.executable, '-B', str(REPO / 'ci/group-effects/resource_guard.py'),
                   '--cwd', str(work / 'server'), '--log', str(folder / (phase + '.guard.log')),
                   '--record', str(receipt), '--min-free-mib', '1024', '--timeout', str(timeout),
                   '--poll-interval', '0.2', '--allocated-root', str(work),
                   '--max-allocated-mib', '16384', '--'] + capture
        process = subprocess.run(wrapped, check=False, timeout=timeout + 30)
        resource = json.loads(regular(receipt))
        # Healthy child failure is still parsed and preserved as a real FAIL.
        # Disk/monitor/timeout/remaining-child failure can never be accepted.
        if (resource['status'] not in ('passed', 'failed') or resource['exit_code'] is None
                or process.returncode != resource['exit_code']
                or (resource['status'] == 'passed') != (resource['exit_code'] == 0)):
            raise ValueError('guarded phase incomplete; retain partial evidence')
        return stdout, resource['exit_code']

    packages = list(c['packages'])
    for package in packages:
        _, code = command(package, ['go', 'test', '-mod=readonly', '-p=1', '-race', '-json',
                          '-run', '^$', './' + package], 'compile', 900)
        if code != 0:
            raise ValueError('compile failed; no test-pass claim')
    summaries = {}
    for package, spec in c['packages'].items():
        log, code = command(package, ['go', 'test', '-mod=readonly', '-p=1', '-parallel=1',
                            '-race', '-json', '-count=1', '-timeout=240s', './' + package], 'tests', 900)
        events = [json.loads(line) for line in regular(log).decode().splitlines() if line.strip()]
        summary = audit(events, code, package, spec)
        write(output / package.replace('/', '_') / 'summary.json', summary)
        summaries[package] = summary
        if not summary['accepted']:
            raise ValueError('full package rejected; retain actual events')
    for package in packages:
        _, code = command(package, ['go', 'vet', '-mod=readonly', '-p=1', './' + package], 'vet', 240)
        if code != 0:
            raise ValueError('vet failed')
    for target, body in bodies.items():
        if regular(work / target) != body:
            raise ValueError('source/lock changed during validation')
    if regular(work / 'server/config/config.yaml') != template:
        raise ValueError('generated public config changed')
    verify()
    final_space = os.statvfs(work)
    final_free = final_space.f_bavail * final_space.f_frsize
    if final_free < 1024 ** 3:
        raise ValueError('final disk floor not met')
    counts = Counter()
    for summary in summaries.values():
        counts.update(summary['counts'])
    write(output / 'summary.json', {'state':'passed_full_source_packages_only', 'scope':SCOPE,
          'effective_digest':c['effective_digest'], 'counts':dict(counts),
          'packages':summaries, 'vet_exit':{p:0 for p in packages},
          'final_free_bytes':final_free,
          'legacy_skips_are_not_integration_passes':True, 'no_runtime_adoption':True})
    print('Stage3 isolated full-package source validation complete; runtime remains unadopted.')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--verify', action='store_true')
    args = parser.parse_args()
    try:
        if args.verify:
            verify()
            print('Stage3 isolated source closure verified; no Go/DB execution.')
        else:
            run()
    except (ValueError, KeyError, TypeError, OSError, subprocess.SubprocessError):
        print('Stage3 validation incomplete; preserve raw evidence and exact owned fixture diagnostics.', file=sys.stderr)
        raise SystemExit(2)
