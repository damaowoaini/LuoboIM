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
SCOPE = 'backend_runtime_join1341_incremental21_compile_cmd_not_deployed_or_full_regression'
BASE_DIGEST = '79764348e1502237a08b039cc521708aea27f97f086672c05a819754bbeeb8f4'
FINAL_DIGEST = '95034c897d50d4c28a26cc26ad892f69c8950024cf1f1ae8138903578db26d44'
SELECTION_SHA = '855f5406cadff5d9588bedd792b16bbb04aa9d7a752d7e8895cc2f1adae544cb'
PREFIX = 'github.com/openimsdk/open-im-server/v3/'
SOURCE_ROOT = 'ci/candidates/backend-runtime-join-r3/source/'
ALLOWED = frozenset(['server/internal/messagedelivery/created_membership_fixture_test.go', 'server/internal/messagedelivery/kafka_ephemeral_integration_test.go', 'server/internal/messagedelivery/kafka_intake_integration_test.go', 'server/internal/messagedelivery/member_removal_visibility_integration_test.go', 'server/internal/messagedelivery/membership_boundary_integration_test.go', 'server/internal/msgtransfer/managed_ordinary_start.go', 'server/internal/rpc/auth/auth.go', 'server/internal/rpc/conversation/conversaion.go', 'server/internal/rpc/encryption/encryption.go', 'server/internal/rpc/friend/friend.go', 'server/internal/rpc/group/managed_start.go', 'server/internal/rpc/msg/send.go', 'server/internal/rpc/msg/server.go', 'server/internal/rpc/rtc/start.go', 'server/internal/rpc/user/user.go', 'server/pkg/common/cmd/rpc.go', 'server/pkg/common/db/cache/process_runtime_redis.go', 'server/pkg/common/db/controller/msg.go', 'server/pkg/common/db/unrelation/process_runtime_mongo.go', 'server/pkg/common/discoveryregister/kubernetes/kubernetes.go', 'server/pkg/common/discoveryregister/owned.go', 'server/pkg/common/discoveryregister/owned_test.go', 'server/pkg/common/discoveryregister/ownedzookeeper/LICENSE', 'server/pkg/common/discoveryregister/ownedzookeeper/conf.go', 'server/pkg/common/discoveryregister/ownedzookeeper/discover.go', 'server/pkg/common/discoveryregister/ownedzookeeper/owned.go', 'server/pkg/common/discoveryregister/ownedzookeeper/owned_test.go', 'server/pkg/common/discoveryregister/ownedzookeeper/register.go', 'server/pkg/common/discoveryregister/ownedzookeeper/resolver.go', 'server/pkg/common/discoveryregister/ownedzookeeper/zk.go', 'server/pkg/common/kafka/producer.go', 'server/pkg/common/startrpc/owned_bootstrap_unix.go', 'server/pkg/common/startrpc/owned_lifecycle_test.go', 'server/pkg/common/startrpc/owned_resources.go', 'server/pkg/common/startrpc/owned_start_unix.go'])
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
        raise ValueError('complete frozen lifecycle and failed-fixture overlay required')
    return bodies


def check_selection(previous, c):
    pairs = lambda value: {(p, n) for p, names in value.items() for n in names}
    original = pairs(previous)
    passed, failed = pairs(c['priorPassedTargets']), pairs(c['failedTargets'])
    affected, added = pairs(c['affectedTargets']), pairs(c['newTargets'])
    selected, reused = pairs(c['targets']), pairs(c['reusedTargets'])
    if len(original) != 99 or len(passed) != 88 or len(failed) != 11 or passed & failed or passed | failed != original:
        raise ValueError('exact original99 and actual88 passes/11 failures required')
    if len(affected) != 1 or not affected <= passed or len(added) != 9 or added & original:
        raise ValueError('one affected old pass and nine new owned lifecycle tops required')
    if selected != failed | affected | added or reused != passed - affected or len(selected) != 21 or len(reused) != 87 or selected & reused:
        raise ValueError('only failed/affected/new cases may run; other87 passes must be reused')
    for key in ('priorPassedTargets', 'failedTargets', 'affectedTargets', 'newTargets', 'targets', 'reusedTargets'):
        if any(len(names) != len(set(names)) for names in c[key].values()):
            raise ValueError('duplicate selected or retained name')


def verify():
    c = json.loads(regular(INPUTS))
    if c['schema'] != 1 or c['scope'] != SCOPE or c['base'] != {'members': 1324, 'digest': BASE_DIGEST} or c['source'] != {'members': 1341, 'digest': FINAL_DIGEST}:
        raise ValueError('exact composed source contract required')
    wanted = {'ci/backend-runtime-join-r2/run.py', 'ci/backend-runtime-join-r2/inputs.json', 'ci/group-effects/resource_guard.py', 'ci/group-effects/capture.py', 'ci/backend-integration.py', 'ci/backend-integration.sh'}
    if len(c['dependencies']) != 6 or {r['path'] for r in c['dependencies']} != wanted:
        raise ValueError('exact frozen parent and owned-fixture dependencies required')
    for row in c['dependencies']:
        pinned(row)
    ancestor = module('ci/backend-runtime-join-r2/run.py', 'accepted_join1324_source_only')
    prior, before, source = ancestor.verify()
    if prior['source'] != c['base']:
        raise ValueError('current1324 source parent differs')
    bodies = apply_overlay(c, before)
    actual = {str(p.relative_to(REPO)) for p in (REPO / SOURCE_ROOT).rglob('*') if p.is_file() or p.is_symlink()}
    modes = {target: prior['sourceModes'].get(target, 0o644) for target in bodies}
    if actual != {r['path'] for r in c['overlay']} or len(bodies) != 1341 or source.digest(source.rows(bodies)) != FINAL_DIGEST or c['sourceModes'] != modes:
        raise ValueError('composed bodies, members, modes or digest differ')
    original = {p: list(names) for p, names in prior['targets'].items()}
    for p, names in prior['reusedTargets'].items():
        original.setdefault(p, []).extend(names)
    check_selection(original, c)
    if c['selectionSHA256'] != SELECTION_SHA or sha(json.dumps(c['targets'], sort_keys=True, separators=(',', ':')).encode()) != SELECTION_SHA:
        raise ValueError('exact incremental21 selection fingerprint required')
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
    work = temporary / ('backend-runtime-join-r3-' + os.environ['GITHUB_RUN_ID'] + '-' + os.environ['GITHUB_RUN_ATTEMPT'])
    fs = os.statvfs(temporary)
    if not temporary.is_absolute() or temporary.resolve() != temporary or work.exists() or fs.f_bavail * fs.f_frsize < 4 * 1024**3:
        raise ValueError('fresh path and 4GiB initial floor required')
    work.mkdir(mode=0o700)
    output = REPO / 'ci-results/backend-runtime-join-r3'
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
    testenv = dict(env, GOPROXY='off', GOSUMDB='off', **fixtures)
    _, compile_code = command('compile-actual-role-entries', clean(testenv) + ['go', 'build', '-mod=readonly', '-p=1', './pkg/common/cmd'], 1200)
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
            print(json.dumps({'source': c['source'], 'targetedTops': 21, 'packages': 4, 'GoExecuted': 0, 'DBExecuted': 0}))
        else:
            raise SystemExit(run())
    except (ValueError, KeyError, TypeError, OSError, subprocess.SubprocessError):
        print('Incremental validation incomplete; original evidence retained.', file=sys.stderr)
        raise SystemExit(2)
