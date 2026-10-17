#!/usr/bin/env python3
"""Manual incremental source/Go/owned-Mongo check; never activates a runtime."""
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
SCOPE = 'backend_runtime_join1358_incremental40_compile_roles_api_not_deployed_or_full_regression'
BASE_DIGEST = '95034c897d50d4c28a26cc26ad892f69c8950024cf1f1ae8138903578db26d44'
FINAL_DIGEST = 'a540a8e60d4f371607db03c67a484b8730869dd16667a5c29f1d2739bae726e2'
SELECTION_SHA = 'ea8dae6661d2be5ef98540b6b9383b87f1b15d6156b8c450fd0599bddd070ecf'
PREFIX = 'github.com/openimsdk/open-im-server/v3/'
SOURCE_ROOT = 'ci/candidates/backend-runtime-join-r4/source/'
ALLOWED = frozenset(['server/cmd/openim-api/main.go', 'server/internal/api/admin_preview.go', 'server/internal/api/favorite.go', 'server/internal/api/meeting_booking.go', 'server/internal/api/owned_mongo_source.go', 'server/internal/api/owned_mongo_source_test.go', 'server/internal/api/owned_routes.go', 'server/internal/api/owned_routes_integration_test.go', 'server/internal/api/owned_server.go', 'server/internal/api/owned_server_test.go', 'server/internal/api/route.go', 'server/internal/api/rtc_webhook.go', 'server/internal/api/session_logout.go', 'server/internal/messagedelivery/created_membership_fixture_test.go', 'server/internal/messagedelivery/membership_boundary_integration_test.go', 'server/internal/namespaceimport/ownedcontrol/server_source_observation_unix.go', 'server/internal/rpc/msg/server.go', 'server/pkg/common/db/controller/msg_owned.go', 'server/pkg/common/db/controller/msg_owned_test.go', 'server/pkg/common/db/mgo/sensitive_word.go', 'server/pkg/common/db/unrelation/managed_mongo.go', 'server/pkg/common/discoveryregister/ownedzookeeper/zk.go', 'server/pkg/common/ginprometheus/owned.go', 'server/pkg/common/ginprometheus/owned_test.go', 'server/pkg/common/kafka/owned_producer.go', 'server/pkg/common/kafka/owned_producer_test.go', 'server/pkg/common/kafka/producer.go', 'server/pkg/common/sensitiveword/owned_checker.go', 'server/pkg/common/sensitiveword/owned_checker_test.go', 'server/pkg/common/startrpc/owned_bootstrap_unix.go', 'server/pkg/common/startrpc/owned_lifecycle_test.go', 'server/pkg/common/startrpc/owned_resources.go', 'server/pkg/common/startrpc/owned_server_identity_test.go', 'server/pkg/common/startrpc/owned_server_identity_unix.go', 'server/pkg/common/startrpc/owned_start_unix.go'])
SOURCE_MEMBERS = 1358
TARGET_TOPS = 40
NEW_TOPS = 15
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
        raise ValueError('complete frozen failed-fixture and owned-role overlay required')
    return bodies


def check_selection(previous, c):
    pairs = lambda value: {(p, n) for p, names in value.items() for n in names}
    original = pairs(previous)
    passed, failed = pairs(c['priorPassedTargets']), pairs(c['failedTargets'])
    unstarted, added = pairs(c['unstartedTargets']), pairs(c['newTargets'])
    affected = pairs(c['additionalAffectedTargets'])
    selected, retained = pairs(c['targets']), pairs(c['reusedTargets'])
    if len(original) != 108 or len(passed) != 88 or len(failed) != 11 or len(unstarted) != 9 or passed & failed or passed & unstarted or failed & unstarted or passed | failed | unstarted != original:
        raise ValueError('exact original108 and actual88 passes/11 failures/9 unstarted required')
    if len(affected) != 5 or len(added) != NEW_TOPS or affected & original or added & (original | affected):
        raise ValueError('only actual new cases and five direct default-API controls may be added')
    if selected != failed | unstarted | added | affected or retained != passed or len(selected) != TARGET_TOPS or selected & retained:
        raise ValueError('only failed/unstarted/new/directly-affected cases; other88 passes must be reused')
    for key in ('priorPassedTargets', 'failedTargets', 'unstartedTargets', 'newTargets', 'additionalAffectedTargets', 'targets', 'reusedTargets'):
        if any(len(ns) != len(set(ns)) for ns in c[key].values()):
            raise ValueError('duplicate selected or retained name')


def verify():
    c = json.loads(regular(INPUTS))
    if c['schema'] != 1 or c['scope'] != SCOPE or c['base'] != {'members': 1341, 'digest': BASE_DIGEST} or c['source'] != {'members': SOURCE_MEMBERS, 'digest': FINAL_DIGEST}:
        raise ValueError('exact composed source contract required')
    wanted = {'ci/backend-runtime-join-r3/run.py', 'ci/backend-runtime-join-r3/inputs.json', 'ci/group-effects/resource_guard.py', 'ci/group-effects/capture.py', 'ci/backend-integration.py', 'ci/backend-integration.sh'}
    if len(c['dependencies']) != 6 or {r['path'] for r in c['dependencies']} != wanted:
        raise ValueError('exact frozen parent and owned-fixture dependencies required')
    for row in c['dependencies']:
        pinned(row)
    ancestor = module('ci/backend-runtime-join-r3/run.py', 'source1341_immutable_parent')
    prior, before, source = ancestor.verify()
    if prior['source'] != c['base']:
        raise ValueError('actual1341 parent differs')
    bodies = apply_overlay(c, before)
    actual = {str(p.relative_to(REPO)) for p in (REPO / SOURCE_ROOT).rglob('*') if p.is_file() or p.is_symlink()}
    modes = {target: prior['sourceModes'].get(target, 0o644) for target in bodies}
    if actual != {r['path'] for r in c['overlay']} or len(bodies) != SOURCE_MEMBERS or source.digest(source.rows(bodies)) != FINAL_DIGEST or c['sourceModes'] != modes:
        raise ValueError('composed bodies, modes, locks or digest differ')
    original = {p: list(ns) for p, ns in prior['targets'].items()}
    for p, ns in prior['reusedTargets'].items():
        original.setdefault(p, []).extend(ns)
    check_selection(original, c)
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
    work = temporary / ('backend-runtime-join-r4-' + os.environ['GITHUB_RUN_ID'] + '-' + os.environ['GITHUB_RUN_ATTEMPT'])
    fs = os.statvfs(temporary)
    if not temporary.is_absolute() or temporary.resolve() != temporary or work.exists() or fs.f_bavail * fs.f_frsize < 4 * 1024**3:
        raise ValueError('fresh path and 4GiB initial floor required')
    work.mkdir(mode=0o700)
    output = REPO / 'ci-results/backend-runtime-join-r4'
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
    _, compile_code = command('compile-actual-role-entries', clean(testenv) + ['go', 'build', '-mod=readonly', '-p=1', './pkg/common/cmd', './cmd/openim-api'], 1200)
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
            print(json.dumps({'source': c['source'], 'targetedTops': 40, 'packages': 9, 'GoExecuted': 0, 'DBExecuted': 0}))
        else:
            raise SystemExit(run())
    except (ValueError, KeyError, TypeError, OSError, subprocess.SubprocessError):
        print('Incremental validation incomplete; original evidence retained.', file=sys.stderr)
        raise SystemExit(2)
