#!/usr/bin/env python3
"""Incremental original-service lifecycle validation; no deployment or full regression."""
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
import time

REPO = Path(__file__).resolve().parents[2]
INPUTS = Path(__file__).with_name('inputs.json')
SCOPE = 'backend_owned_lifecycle1406_incremental36_actual_entries_not_deployed_or_full_regression'
BASE_DIGEST = '9592d7003a869017a0ae184d851ae8162308fdff1cb806f9a8a1f3c1d244659f'
FINAL_DIGEST = '884713cc6c7a3eb97f024bf733ee49623b8adb0662397c1a0adaeeec8fa4ee05'
SELECTION_SHA = '0ba40767d6dc374a99be75f3796a248e932cc6ca50628817ac93b30e71ae0ccf'
PREFIX = "github.com/openimsdk/open-im-server/v3/"
SOURCE_ROOT = "ci/candidates/backend-runtime-join-r6/source/"
ALLOWED = frozenset(['server/internal/msgtransfer/mongo_persistence_test.go', 'server/internal/msgtransfer/online_history_msg_handler.go', 'server/internal/msgtransfer/online_msg_to_mongo_handler.go', 'server/internal/msgtransfer/owned_legacy_claims.go', 'server/internal/msgtransfer/owned_legacy_claims_test.go', 'server/internal/msgtransfer/owned_legacy_resources.go', 'server/internal/msgtransfer/owned_legacy_runtime_test.go', 'server/internal/msgtransfer/owned_legacy_runtime_unix.go', 'server/internal/push/offlinepush/fcm/owned.go', 'server/internal/push/offlinepush/fcm/owned_test.go', 'server/internal/push/offlinepush/fcm/push.go', 'server/internal/push/offlinepush/getui/push.go', 'server/internal/push/owned_provider.go', 'server/internal/push/owned_provider_test.go', 'server/internal/push/owned_runtime.go', 'server/internal/push/push_rpc_server.go', 'server/internal/push/push_to_client.go', 'server/internal/rpc/third/owned_provider.go', 'server/internal/rpc/third/owned_provider_test.go', 'server/internal/rpc/third/third.go', 'server/pkg/common/cmd/msg_transfer.go', 'server/pkg/common/cmd/rpc.go', 'server/pkg/common/db/cache/group_runtime_redis.go', 'server/pkg/common/db/cache/msg.go', 'server/pkg/common/db/cache/owned_cleanup_error_test.go', 'server/pkg/common/db/cache/owned_constructor_redis_integration_test.go', 'server/pkg/common/db/cache/owned_source_redis.go', 'server/pkg/common/db/cache/owned_source_redis_test.go', 'server/pkg/common/db/cache/process_runtime_redis.go', 'server/pkg/common/db/controller/msg.go', 'server/pkg/common/db/controller/owned_cache_error_test.go', 'server/pkg/common/db/mgo/log.go', 'server/pkg/common/db/mgo/object.go', 'server/pkg/common/db/s3/cos/cos.go', 'server/pkg/common/db/s3/minio/minio.go', 'server/pkg/common/db/s3/oss/oss.go', 'server/pkg/common/db/s3/owned_provider.go', 'server/pkg/common/db/s3/owned_transport.go', 'server/pkg/common/db/s3/owned_transport_test.go', 'server/pkg/common/http/owned_callback.go', 'server/pkg/common/kafka/owned_consumer.go', 'server/pkg/common/kafka/owned_consumer_run.go', 'server/pkg/common/kafka/owned_consumer_test.go', 'server/pkg/common/kafka/owned_context.go', 'server/pkg/common/kafka/owned_producer.go', 'server/pkg/common/kafka/owned_producer_test.go', 'server/pkg/common/kafka/owned_producer_wire_test.go', 'server/pkg/common/kafka/owned_source_outcome_test.go', 'server/pkg/common/kafka/producer.go', 'server/pkg/common/ownedsource/observer.go', 'server/pkg/common/startrpc/owned_bootstrap_unix.go', 'server/pkg/common/startrpc/owned_gateway_admission.go', 'server/pkg/common/startrpc/owned_legacy_identity_test.go', 'server/pkg/common/startrpc/owned_push_admission.go', 'server/pkg/common/startrpc/owned_push_lifecycle_test.go', 'server/pkg/common/startrpc/owned_resources.go', 'server/pkg/common/startrpc/owned_server_identity_unix.go', 'server/pkg/common/startrpc/owned_start_unix.go'])
PRIOR_PASS_SHA = 'dd59ead49179a41d444d5d8d50dcc8d0cd56c097da63687e386c21a95bf49edf'
AFFECTED_SHA = '274fccdf1c8725579e3779251e3bf04da11fdd51f9b1a9335e82f8fd4686978e'
COMPILE_PACKAGES = ['./pkg/common/cmd', './cmd/openim-api', './cmd/openim-rpc/openim-rpc-third', './cmd/openim-push', './cmd/openim-msgtransfer']
MONGO = 'mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration'


def sha(raw):
    return hashlib.sha256(raw).hexdigest()

def regular(path):
    if path.resolve() != path or not path.is_file() or path.is_symlink():
        raise ValueError('canonical regular file required')
    return path.read_bytes()

def pinned(row):
    path = REPO / row['path']
    raw = regular(path)
    if len(raw) != row['bytes'] or sha(raw) != row['sha256'] or stat.S_IMODE(path.stat().st_mode) != 0o644:
        raise ValueError('pinned source body/mode differs: ' + row['path'])
    return raw

def module(path, label):
    spec = importlib.util.spec_from_file_location(label, REPO / path)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value

def apply_overlay(c, before):
    bodies, seen = dict(before), set()
    for row in c['overlay']:
        target = row['target']
        old = before.get(target)
        if target not in ALLOWED or target in seen or row['path'] != SOURCE_ROOT + target or row['beforeSHA256'] != (sha(old) if old is not None else None):
            raise ValueError('exact existing or absent source parent required')
        seen.add(target)
        bodies[target] = pinned(row)
    if seen != ALLOWED:
        raise ValueError('complete frozen original-service overlay required')
    return bodies


def check_selection(previous, c):
    pairs = lambda value: {(p, n) for p, ns in value.items() for n in ns}
    fingerprint = lambda value: sha(json.dumps(value, sort_keys=True, separators=(',', ':')).encode())
    original = pairs(previous)
    passed, failed = pairs(c['priorPassedTargets']), pairs(c['failedTargets'])
    added, affected = pairs(c['newTargets']), pairs(c['directlyAffectedTargets'])
    extra = pairs(c['directExistingTargets'])
    selected, retained = pairs(c['targets']), pairs(c['reusedTargets'])
    if len(original) != 144 or passed != original or failed or fingerprint(c['priorPassedTargets']) != PRIOR_PASS_SHA:
        raise ValueError('only the actual original144 passes may be reused')
    if len(added) != 22 or added & original or len(affected) != 14 or added & affected or fingerprint(c['directlyAffectedTargets']) != AFFECTED_SHA:
        raise ValueError('exact new tests and reviewed direct impact required')
    if len(extra) != 3 or extra != affected - original:
        raise ValueError('existing but not previously executed tests must not be represented as new or passed')
    if selected != added | affected or retained != passed - affected or len(selected) != 36 or len(retained) != 133 or selected & retained:
        raise ValueError('one new/direct batch and complete unaffected pass reuse required')
    for field in ('priorPassedTargets', 'failedTargets', 'directlyAffectedTargets', 'directExistingTargets', 'newTargets', 'targets', 'reusedTargets'):
        if any(not ns or len(ns) != len(set(ns)) for ns in c[field].values()):
            raise ValueError('empty package or duplicate test name')


def verify():
    c = json.loads(regular(INPUTS))
    if c['schema'] != 1 or c['scope'] != SCOPE or c['base'] != {'members': 1378, 'digest': BASE_DIGEST} or c['source'] != {'members': 1406, 'digest': FINAL_DIGEST} or c['compileOnlyPackages'] != COMPILE_PACKAGES:
        raise ValueError('exact composed source and entry-point contract required')
    wanted = {'ci/backend-runtime-join-r5/run.py', 'ci/backend-runtime-join-r5/inputs.json', 'ci/group-effects/resource_guard.py', 'ci/group-effects/capture.py', 'ci/backend-integration.py', 'ci/backend-integration.sh'}
    if len(c['dependencies']) != 6 or {r['path'] for r in c['dependencies']} != wanted:
        raise ValueError('exact immutable parent and owned-fixture dependencies required')
    for row in c['dependencies']:
        pinned(row)
    ancestor = module('ci/backend-runtime-join-r5/run.py', 'source1378_immutable_parent')
    prior, before, source = ancestor.verify()
    if prior['source'] != c['base']:
        raise ValueError('actual1378 source parent differs')
    bodies = apply_overlay(c, before)
    actual = {str(p.relative_to(REPO)) for p in (REPO / SOURCE_ROOT).rglob('*') if p.is_file() or p.is_symlink()}
    modes = {target: prior['sourceModes'].get(target, 0o644) for target in bodies}
    if actual != {r['path'] for r in c['overlay']} or len(bodies) != 1406 or source.digest(source.rows(bodies)) != FINAL_DIGEST or c['sourceModes'] != modes:
        raise ValueError('composed bodies, modes or source digest differ')
    original = {p: list(ns) for p, ns in prior['targets'].items()}
    for p, ns in prior['reusedTargets'].items():
        original.setdefault(p, []).extend(ns)
    check_selection(original, c)
    old_names, new_names = {}, {}
    for index, mapping in ((old_names, before), (new_names, bodies)):
        for target, raw in mapping.items():
            if target.endswith('_test.go'):
                package = str(PurePosixPath(target).parent.relative_to('server'))
                index.setdefault(package, set()).update(re.findall(r'^func (Test[A-Za-z0-9_]+)\(t \*testing\.T\)', raw.decode(), re.M))
    discovered = {(p, n) for p, ns in new_names.items() for n in ns - old_names.get(p, set())}
    if discovered != {(p, n) for p, ns in c['newTargets'].items() for n in ns} or any(not ns <= new_names.get(p, set()) for p, ns in old_names.items()):
        raise ValueError('actual new source functions or preserved original tests differ')
    if any(n not in old_names.get(p, set()) for p, ns in c['directlyAffectedTargets'].items() for n in ns):
        raise ValueError('directly affected existing test is not in the actual parent')
    if c['selectionSHA256'] != SELECTION_SHA or sha(json.dumps(c['targets'], sort_keys=True, separators=(',', ':')).encode()) != SELECTION_SHA:
        raise ValueError('exact incremental selection fingerprint required')
    return c, bodies, source


def audit(events, code, targets):
    starts, passes, footers = Counter(), Counter(), Counter()
    for event in events:
        if not isinstance(event, dict):
            raise ValueError('malformed event')
        package = event.get('Package', '').removeprefix(PREFIX)
        if event.get('Package') != PREFIX + package or package not in targets or footers[package]:
            raise ValueError('foreign or post-terminal event')
        action, test = event.get('Action'), event.get('Test')
        if action not in ('start', 'run', 'output', 'pass', 'pause', 'cont') or any(s in event.get('Output', '') for s in ('WARNING: DATA RACE', 'panic:', 'fatal error:')):
            raise ValueError('failure, skip, race or malformed action')
        if test:
            if test.split('/')[0] not in targets[package]:
                raise ValueError('unselected top or descendant')
            if action == 'run':
                starts[(package, test)] += 1
            if action == 'pass':
                passes[(package, test)] += 1
        elif action == 'pass':
            footers[package] += 1
    expected = {(p, n) for p, names in targets.items() for n in names}
    actual = {key for key in starts if '/' not in key[1]}
    if code != 0 or actual != expected or starts != passes or any(v != 1 for v in starts.values()) or footers != Counter({p: 1 for p in targets}):
        raise ValueError('incomplete or duplicate named completion')
    return {'topPASS': len(expected), 'subPASS': len(starts) - len(expected), 'skip': 0, 'fail': 0, 'packages': len(footers), 'namedCompletions': [{'package': p, 'name': n} for p, n in sorted(passes)]}

def write(path, value):
    with path.open('x') as out:
        json.dump(value, out, indent=2)
        out.write('\n')
        out.flush()
        os.fsync(out.fileno())

def run():
    started = time.monotonic()
    for key, value in {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux', 'GOTOOLCHAIN': 'local', 'GOWORK': 'off'}.items():
        if os.environ.get(key) != value:
            raise ValueError('hosted disposable Linux only')
    if os.getuid() == 0 or not re.fullmatch(r'[0-9]+', os.environ.get('GITHUB_RUN_ID', '')) or not re.fullmatch(r'[0-9]+', os.environ.get('GITHUB_RUN_ATTEMPT', '')) or not re.fullmatch(r'[0-9a-f]{40}', os.environ.get('GITHUB_SHA', '')):
        raise ValueError('exact nonroot run/attempt/commit required')
    c, bodies, parent = verify()
    version = subprocess.check_output(['go', 'version'], text=True, timeout=15).strip()
    if version != 'go version go1.27.2 linux/amd64':
        raise ValueError('exact Go compiler required')
    temporary = Path(os.environ['RUNNER_TEMP'])
    work = temporary / ('backend-runtime-join-r6-' + os.environ['GITHUB_RUN_ID'] + '-' + os.environ['GITHUB_RUN_ATTEMPT'])
    fs = os.statvfs(temporary)
    if not temporary.is_absolute() or temporary.resolve() != temporary or work.exists() or fs.f_bavail * fs.f_frsize < 4 * 1024**3:
        raise ValueError('fresh path and 4GiB initial floor required')
    work.mkdir(mode=0o700)
    output = REPO / 'ci-results/backend-runtime-join-r6'
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    for target, raw in bodies.items():
        path = work / target
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(raw)
        path.chmod(c['sourceModes'][target])
    template = bodies['server/config/config.example.yaml']
    (work / 'server/config/config.yaml').write_bytes(template)
    env = {'PATH': os.environ['PATH'], 'GOROOT': subprocess.check_output(['go', 'env', 'GOROOT'], text=True, timeout=15).strip(), 'GOTOOLCHAIN': 'local', 'GOWORK': 'off', 'GOENV': 'off', 'CGO_ENABLED': '1', 'GOMAXPROCS': '2', 'GOMEMLIMIT': '900MiB'}
    for key, folder in [('GOMODCACHE', 'modcache'), ('GOCACHE', 'buildcache'), ('GOPATH', 'gopath'), ('TMPDIR', 'tmp')]:
        path = work / folder
        path.mkdir(mode=0o700)
        env[key] = str(path)
    env['GOTMPDIR'] = env['TMPDIR']
    write(output / 'source.json', {'scope': SCOPE, 'commit': os.environ['GITHUB_SHA'], 'run': os.environ['GITHUB_RUN_ID'], 'attempt': os.environ['GITHUB_RUN_ATTEMPT'], 'inputsSHA256': sha(regular(INPUTS)), 'source': c['source'], 'targets': c['targets'], 'goVersion': version, 'runtimeChanged': False, 'fullRegression': False})

    def command(label, argv, timeout):
        remaining = 3000 - (time.monotonic() - started)
        if remaining < 1:
            raise ValueError('controller budget exhausted')
        timeout = max(1, int(min(remaining, timeout)))
        folder = output / label
        folder.mkdir()
        capture = [sys.executable, '-B', str(REPO / 'ci/group-effects/capture.py'), '--stdout', str(folder / 'stdout.jsonl'), '--stderr', str(folder / 'stderr.log'), '--'] + argv
        wrapped = [sys.executable, '-B', str(REPO / 'ci/group-effects/resource_guard.py'), '--cwd', str(work / 'server'), '--log', str(folder / 'guard.log'), '--record', str(folder / 'resource.json'), '--min-free-mib', '1024', '--timeout', str(timeout), '--poll-interval', '.2', '--allocated-root', str(work), '--max-allocated-mib', '16384', '--'] + capture
        child = subprocess.run(wrapped, check=False, timeout=timeout + 30)
        record = json.loads(regular(folder / 'resource.json'))
        if record.get('status') not in ('passed', 'failed') or record.get('exit_code') != child.returncode or (record['status'] == 'passed') != (child.returncode == 0):
            raise ValueError('guard, timeout or remaining process rejection')
        return folder, child.returncode

    _, code = command('fixture-ready', [sys.executable, '-B', str(REPO / 'ci/backend-integration.py'), 'check-ready', str(REPO / 'ci-results/backend-integration'), os.environ['GITHUB_RUN_ID'] + ':' + os.environ['GITHUB_RUN_ATTEMPT'], os.environ['GITHUB_SHA']], 30)
    if code:
        raise ValueError('owned fixture readiness rejected')
    clean = lambda values: ['env', '-i'] + [k + '=' + v for k, v in values.items()]
    download = dict(env, GOPROXY='https://proxy.golang.org', GOSUMDB='sum.golang.org', GOAUTH='off', GOPRIVATE='', GONOPROXY='', GONOSUMDB='')
    _, code = command('module-download', clean(download) + ['go', 'mod', 'download', '-json'], 600)
    if code or any(regular(work / p) != bodies[p] for p in ('server/go.mod', 'server/go.sum')):
        raise ValueError('locked public dependencies rejected')
    fixtures = {'LUOBO_STORAGE_TEST_MONGO_URI': MONGO, 'LUOBO_GROUP_RECEIVER_TEST_MONGO_URI': MONGO, 'LUOBO_GROUP_ROLE_TEST_MONGO_URI': MONGO, 'LUOBO_RETENTION_TEST_REDIS': 'redis://127.0.0.1:36379/15', 'LUOBO_EMAIL_INTEGRATION': '0', 'LUOBO_RUN_LEGACY_LOAD_TESTS': '0', 'LUOBO_RUN_LEGACY_EXAMPLES': '0'}
    socket = Path('/tmp') / ('luobo-it-' + os.environ['GITHUB_RUN_ID'] + '-' + os.environ['GITHUB_RUN_ATTEMPT']) / 'references/integration/redis8.2/redis.sock'
    if socket.resolve() != socket or not stat.S_ISSOCK(socket.lstat().st_mode):
        raise ValueError('exact owned Redis fixture socket required')
    fixtures.update(LUOBO_INTEGRATION_MONGO_URI=MONGO, LUOBO_INTEGRATION_REDIS_SOCKET=str(socket), LUOBO_BOOKING_TEST_REDIS_ADDR='127.0.0.1:36379')
    testenv = dict(env, GOPROXY='off', GOSUMDB='off', **fixtures)
    _, compile_code = command('compile-actual-role-entries', clean(testenv) + ['go', 'build', '-mod=readonly', '-p=1'] + c['compileOnlyPackages'], 1200)
    names = sorted({n for selected in c['targets'].values() for n in selected})
    args = clean(testenv) + ['go', 'test', '-mod=readonly', '-p=1', '-parallel=1', '-race', '-json', '-count=1', '-vet=off', '-timeout=180s', '-run', '^(' + '|'.join(names) + ')$'] + ['./' + p for p in c['targets']]
    folder, code = command('targeted-tests', args, 2250)
    try:
        events = [json.loads(line) for line in regular(folder / 'stdout.jsonl').decode().splitlines() if line.strip()]
        result = dict(audit(events, code, c['targets']), accepted=True)
    except (ValueError, TypeError, KeyError, UnicodeError):
        result = {'accepted': False, 'exitCode': code, 'originalRawRetained': True}
    for target, raw in bodies.items():
        path = work / target
        if regular(path) != raw or stat.S_IMODE(path.stat().st_mode) != c['sourceModes'][target]:
            raise ValueError('source or mode drift')
    if regular(work / 'server/config/config.yaml') != template:
        raise ValueError('generated public config drift')
    verify()
    write(output / 'summary.json', {'state': 'targeted_pass' if result['accepted'] and compile_code == 0 else 'failed_or_incomplete', 'actualRoleEntriesCompileExit': compile_code, 'scope': SCOPE, 'result': result, 'sourcePostExact': c['source'], 'sourceModesAndLocksPostExact': True, 'runtimeChanged': False, 'fullRegression': False, 'fixtureCollectCleanup': 'subsequent workflow steps must also pass', 'networkBoundary': 'clean environment and exact owned loopback fixture; no kernel egress isolation claim'})
    return 0 if result['accepted'] and compile_code == 0 else 1

if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--verify', action='store_true')
    args = parser.parse_args()
    try:
        if args.verify:
            c, _, _ = verify()
            print(json.dumps({'source': c['source'], 'targetedTops': 36, 'newTops': 22, 'directExistingTops': 3, 'reusedTops': 133, 'packages': 12, 'GoExecuted': 0, 'DBExecuted': 0}))
        else:
            raise SystemExit(run())
    except (ValueError, KeyError, TypeError, OSError, subprocess.SubprocessError):
        print('Incremental validation incomplete; original evidence retained.', file=sys.stderr)
        raise SystemExit(2)
