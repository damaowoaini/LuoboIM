#!/usr/bin/env python3
"""Only four failed Mongo facts tops/four subs. Prior pure/RPC/SQL/HTTP are evidence reuse."""
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
SCOPE = 'non_authority_facts_fixture_repair_only4Mongo4sub_no_SQL_HTTP_runtime'
PREFIX = 'github.com/openimsdk/open-im-server/v3/'
PACKAGE = 'internal/messagedelivery'
EXPECTED = {'tops': ['TestOwnedNotificationFactsCommitSurviveTerminalCleanupAndOriginalReceipt', 'TestOwnedNotificationFactsFailureRollsBackSlotCounterAndSourceReceipt', 'TestOwnedNotificationFactsDoNotTurnSeq0IntoPersistedHistory', 'TestOwnedNotificationFactsLostCommitACKResolvesWithoutBackfillOrReexecution'], 'subs': ['TestOwnedNotificationFactsCommitSurviveTerminalCleanupAndOriginalReceipt/frozen-head', 'TestOwnedNotificationFactsCommitSurviveTerminalCleanupAndOriginalReceipt/accepted-normal', 'TestOwnedNotificationFactsFailureRollsBackSlotCounterAndSourceReceipt/abort-after-facts', 'TestOwnedNotificationFactsFailureRollsBackSlotCounterAndSourceReceipt/foreign-page-collision']}
TARGET = 'server/internal/messagedelivery/notification_facts_integration_test.go'
SOURCE = 'ci/candidates/notification-facts-r1/source/' + TARGET
PARENT_DIGEST = '6b3e82ae99693aa9c2d390a745cd810bb3ee006529bf1ef41ad9582416a65f22'
FINAL_DIGEST = '084efcb91831b7df9b2c9fd623681218dac9d282e3b77bb3df74ccbf3d292098'
MONGO = 'mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration'
MONGO_KEYS = ('LUOBO_STORAGE_TEST_MONGO_URI', 'LUOBO_GROUP_RECEIVER_TEST_MONGO_URI')
ZERO_FLAGS = {'LUOBO_EMAIL_INTEGRATION': '0', 'LUOBO_RUN_LEGACY_LOAD_TESTS': '0', 'LUOBO_RUN_LEGACY_EXAMPLES': '0'}
OUTPUT = 'ci-results/notification-facts-r1'
FIXTURE_MANIFEST_SHA256 = 'fbfd8d3b74808e155aa51c84325795c6520743a2f07207d289e26b68f32b1a97'
REUSED = {'run': 38989457633, 'job': 117268371094, 'head': '3855410715a29605f4cb07d154c5d90643f405b3', 'artifactID': 12084756606, 'artifactSHA256': '3c8c01e4e42f99e77b5bbd318c7a29f77e159cf0e9c6954c6805497aa657396c', 'sourceDigest': '6b3e82ae99693aa9c2d390a745cd810bb3ee006529bf1ef41ad9582416a65f22', 'topPASS': 2, 'subPASS': 1, 'tops': ['TestNotificationFactsPagesBoundDocumentsWithoutRosterCap', 'TestOwnedRoleRPCReceiverRiskSQLSignedCallbackUnknownRestartAndReceiptFirst'], 'subs': ['TestOwnedRoleRPCReceiverRiskSQLSignedCallbackUnknownRestartAndReceiptFirst/stored-n'], 'rawSHA256': {'pureAndFailedMongo': 'a1d50947dff8f6a7b33a5f9469169963b5ce32f57e454ad6bbc4b496f48f0545', 'RPCStoredN': 'b1c6b640f4ffd7f8a6651edc346543ea881bb39195568d0f9539a9c577de7ce7'}, 'agentPartialReviewSHA256': '2aac0b5592175bd1bc7b256e3ca6dc862d9b272f34d133b5d159188c0ba29dbc', 'SQLHMACAndCleanup': 'actual prior evidence only; no new SQL/HTTP preparation or replay', 'notNewExecution': True}
DEPENDENCIES = frozenset(['.github/workflows/notification-facts-mongo-retry.yml',
 'ci/notification-facts-r1/run.py', 'ci/notification-facts-r1/test_run.py',
 'ci/notification-facts/run.py', 'ci/notification-facts/inputs.json',
 'ci/backend-integration.sh', 'ci/backend-integration.py', 'ci/backend-integration.json',
 'ci/group-effects/capture.py', 'ci/group-effects/resource_guard.py', 'ci/secret_scan.py'])

def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def name(value):
    p = PurePosixPath(value)
    if (not value or p.is_absolute() or str(p) != value or '\\' in value
            or any(part in ('', '.', '..') for part in value.split('/'))):
        raise ValueError('canonical relative path required')
    return value


def regular(path):
    if path.resolve() != path or path.is_symlink() or not path.is_file():
        raise ValueError('canonical regular input required')
    return path.read_bytes()


def pinned(row, root):
    path = root / name(row['path'])
    raw = regular(path)
    mode = int(row['mode'], 8) if isinstance(row['mode'], str) else row['mode']
    if len(raw) != row['bytes'] or sha(raw) != row['sha256'] or stat.S_IMODE(path.stat().st_mode) != mode:
        raise ValueError('pinned body or mode differs')
    return raw


def rows(bodies):
    return [{'path': path, 'bytes': len(body), 'sha256': sha(body)} for path, body in sorted(bodies.items())]


def digest(items):
    return sha(b''.join(row['path'].encode() + b'\0' + row['sha256'].encode() + b'\0'
                        for row in sorted(items, key=lambda row: row['path'])))


def audit(events, code, package, expected):
    wanted = set(expected['tops'] + expected['subs'])
    starts, passes = Counter(), Counter()
    footer = []
    for event in events:
        if not isinstance(event, dict) or event.get('Package') != PREFIX + package or footer:
            raise ValueError('foreign or post-terminal event')
        action, test = event.get('Action'), event.get('Test')
        if (action not in ('start', 'run', 'output', 'pass', 'pause', 'cont')
                or any(x in event.get('Output', '') for x in ('WARNING: DATA RACE', 'panic:', 'fatal error:'))):
            raise ValueError('failed/skip/build/race event')
        if test:
            if test not in wanted:
                raise ValueError('unexpected exact top or subtest')
            if action == 'run':
                starts[test] += 1
            if action == 'pass':
                passes[test] += 1
        elif action == 'pass':
            footer.append(event)
    exact = Counter({test: 1 for test in wanted})
    if code != 0 or starts != exact or passes != exact or len(footer) != 1:
        raise ValueError('missing duplicate or failed exact selection')
    return {'topPASS': len(expected['tops']), 'subPASS': len(expected['subs']), 'skip': 0, 'fail': 0,
            'tops': sorted(expected['tops']), 'subs': sorted(expected['subs']), 'footer': footer[0]}


def require_guard(record, code):
    if (record.get('status') not in ('passed', 'failed') or record.get('exit_code') is None
            or code != record['exit_code'] or (record['status'] == 'passed') != (code == 0)
            or record.get('minimum_free_required_bytes') != 1 << 30
            or record.get('max_allocated_allowed_bytes') != 16 << 30
            or not isinstance(record.get('minimum_observed_free_bytes'), int)
            or record['minimum_observed_free_bytes'] < 1 << 30
            or not isinstance(record.get('maximum_observed_task_allocated_bytes'), int)
            or not 0 <= record['maximum_observed_task_allocated_bytes'] <= 16 << 30):
        raise ValueError('guard/monitor/timeout/remaining-process/resource rejection')


def write(path, value):
    with path.open('x') as output:
        json.dump(value, output, indent=2)
        output.write('\n')
        output.flush()
        os.fsync(output.fileno())
    parent = os.open(path.parent, os.O_RDONLY)
    try:
        os.fsync(parent)
    finally:
        os.close(parent)






def apply_fixture(c, previous, root):
    row = c['overlay']
    if (row['target'] != TARGET or row['path'] != SOURCE or row['mode'] != '0o644'
            or row['beforeSHA256'] != sha(previous[TARGET])):
        raise ValueError('exact existing facts fixture before/target/mode required')
    raw = pinned(row, root)
    marker = b'func TestOwnedNotificationFactsCommitSurviveTerminalCleanupAndOriginalReceipt('
    before = previous[TARGET]
    if before[before.index(marker):] != raw[raw.index(marker):]:
        raise ValueError('all existing test functions/assertions/names must remain byte exact')
    actual = {str(p.relative_to(root)) for p in (root / 'ci/candidates/notification-facts-r1/source').rglob('*') if p.is_file() or p.is_symlink()}
    if actual != {SOURCE}:
        raise ValueError('extra or absent retry source')
    result = dict(previous)
    result[TARGET] = raw
    return result


def verify():
    c = json.loads(regular(INPUTS))
    if (c['schema'] != 1 or c['scope'] != SCOPE or c['parentCount'] != 1202 or c['parentDigest'] != PARENT_DIGEST
            or c['effectiveCount'] != 1202 or c['effectiveDigest'] != FINAL_DIGEST
            or c['expected'] != EXPECTED or c['totals'] != {'topPASS': 4, 'subPASS': 4, 'skip': 0, 'packages': 1}
            or c['parentRunner']['path'] != 'ci/notification-facts/run.py'
            or c['parentInputs']['path'] != 'ci/notification-facts/inputs.json'
            or c['reusedActual']['run'] != 38989457633
            or c['reusedActual']['head'] != '3855410715a29605f4cb07d154c5d90643f405b3'
            or c['reusedActual']['artifactSHA256'] != '3c8c01e4e42f99e77b5bbd318c7a29f77e159cf0e9c6954c6805497aa657396c'
            or c['reusedActual'] != REUSED or c['fixtureManifest']['sha256'] != FIXTURE_MANIFEST_SHA256
            or {x['path'] for x in c['dependencies']} != DEPENDENCIES
            or len(c['dependencies']) != len(DEPENDENCIES)):
        raise ValueError('exact one-fixture successor and failed4/4 selection required')
    for row in c['dependencies']:
        pinned(row, REPO)
    pinned(c['parentRunner'], REPO)
    pinned(c['parentInputs'], REPO)
    spec = importlib.util.spec_from_file_location('facts1202_parent_sources_only', REPO / c['parentRunner']['path'])
    parent = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(parent)
    previous, before = parent.verify()  # Source only; never old parent.run or its SQL/RPC targets.
    if previous['effectiveDigest'] != PARENT_DIGEST or len(before) != 1202:
        raise ValueError('exact original failed-run source parent required')
    bodies = apply_fixture(c, before, REPO)
    if rows(bodies) != c['effectiveSource'] or digest(rows(bodies)) != FINAL_DIGEST or c['sourceModes'] != previous['sourceModes']:
        raise ValueError('fixture-only1202 bodies/modes differ')
    # Every other source, including original3production/pure1/RPC stored-n, is
    # inherited verbatim. Only this common fixture prefix changes.
    return c, bodies


def fixture_environment(env):
    required = {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux',
                'GOTOOLCHAIN': 'local', 'GOWORK': 'off'}
    if any(env.get(k) != v for k, v in required.items()):
        raise ValueError('hosted owned fixture environment required')
    if (not re.fullmatch('[0-9]+', env.get('GITHUB_RUN_ID', '')) or not re.fullmatch('[0-9]+', env.get('GITHUB_RUN_ATTEMPT', ''))
            or not re.fullmatch('[0-9a-f]{40}', env.get('GITHUB_SHA', ''))):
        raise ValueError('exact run/attempt/head required')
    bindings = dict(ZERO_FLAGS, **{k: MONGO for k in MONGO_KEYS})
    if any(env.get(k) != v for k, v in bindings.items()) or any(k.startswith('LUOBO_') and k not in bindings for k in env):
        raise ValueError('missing/foreign Mongo or unwanted SQL/HTTP/business fixture')
    return bindings


def require_collected(record):
    fields = ('mongo_user_database_count', 'redis_db15_dbsize_before_info', 'redis_db15_key_count',
              'redis_db15_expiring_keys', 'unexpected_redis_database_count')
    if record.get('state') != 'no_fixture_residue' or any(type(record.get(k)) is not int or record[k] != 0 for k in fields):
        raise ValueError('Mongo/pairedRedis zero residue required')


def require_cleanup(record):
    if (type(record.get('container_cleanup_exit_code')) is not int or record['container_cleanup_exit_code'] != 0
            or record.get('scope') != 'exact_run_label_and_two_names_only'):
        raise ValueError('exact Mongo/pairedRedis cleanup unconfirmed')


def check_collected():
    fixture_environment(os.environ)
    require_collected(json.loads(regular(REPO / 'ci-results/backend-integration/residual-check.json')))
    write(REPO / OUTPUT / 'collected-acceptance.json', {'state': 'owned_Mongo_pairedRedis_zero_residue', 'newSQLOrHTTP': False})


def check_cleanup():
    fixture_environment(os.environ)
    require_cleanup(json.loads(regular(REPO / 'ci-results/backend-integration/container-cleanup.json')))
    collected = json.loads(regular(REPO / OUTPUT / 'collected-acceptance.json'))
    summary = json.loads(regular(REPO / OUTPUT / 'summary.json'))
    if collected.get('state') != 'owned_Mongo_pairedRedis_zero_residue' or summary.get('state') != 'target4_4_passed_pending_collection_cleanup':
        raise ValueError('new targeted passes and collection required before whole-job acceptance')
    write(REPO / OUTPUT / 'cleanup-acceptance.json', {'state': 'target4_4_and_exact_owned_cleanup_passed',
          'sourceDigest': FINAL_DIGEST, 'topPASS': 4, 'subPASS': 4, 'notReadGrantOrReadyOrRuntime': True,
          'reusedEvidenceNotNewExecution': summary['reusedActual']})


def run():
    started = time.monotonic()
    bindings = fixture_environment(os.environ)
    if os.getuid() == 0:
        raise ValueError('nonroot owner required')
    c, bodies = verify()
    temporary = Path(os.environ['RUNNER_TEMP'])
    work = temporary / ('notification-facts-r1-' + os.environ['GITHUB_RUN_ID'] + '-' + os.environ['GITHUB_RUN_ATTEMPT'])
    if not temporary.is_absolute() or temporary.resolve() != temporary or work.exists():
        raise ValueError('fresh canonical work required')
    free = os.statvfs(temporary)
    if free.f_bavail * free.f_frsize < 4 * 1024**3:
        raise ValueError('4GiB work floor; no Go child started')
    work.mkdir(mode=0o700)
    output = REPO / OUTPUT
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    version = subprocess.check_output(['go', 'version'], text=True, timeout=15).strip()
    if version != 'go version go1.27.2 linux/amd64':
        raise ValueError('exact Go compiler required')
    goroot = subprocess.check_output(['go', 'env', 'GOROOT'], text=True, timeout=15).strip()
    for target, body in bodies.items():
        path = work / target
        path.parent.mkdir(parents=True, exist_ok=True)
        with path.open('xb') as handle:
            handle.write(body)
        path.chmod(c['sourceModes'][target])
    template = bodies['server/config/config.example.yaml']
    with (work / 'server/config/config.yaml').open('xb') as handle:
        handle.write(template)
    env = {'PATH': os.environ['PATH'], 'GOROOT': goroot, 'GOTOOLCHAIN': 'local', 'GOWORK': 'off', 'GOENV': 'off',
           'CGO_ENABLED': '1', 'GOMAXPROCS': '2', 'GOMEMLIMIT': '900MiB', 'GOPROXY': 'off', 'GOSUMDB': 'off',
           'GOAUTH': 'off', 'GOPRIVATE': '', 'GONOPROXY': '', 'GONOSUMDB': '', **bindings}
    if 'HOME' in os.environ:
        env['HOME'] = os.environ['HOME']  # Original only.
    for key, folder in [('GOMODCACHE', 'modcache'), ('GOCACHE', 'buildcache'), ('GOPATH', 'gopath'), ('TMPDIR', 'tmp')]:
        (work / folder).mkdir(mode=0o700)
        env[key] = str(work / folder)
    env['GOTMPDIR'] = env['TMPDIR']
    locks = {p: bodies[p] for p in ['server/go.mod', 'server/go.sum']}
    write(output / 'source.json', {'scope': SCOPE, 'commit': os.environ['GITHUB_SHA'], 'inputsSHA256': sha(regular(INPUTS)),
          'parentCount': 1202, 'parentDigest': PARENT_DIGEST, 'effectiveCount': 1202, 'effectiveDigest': FINAL_DIGEST,
          'goVersion': version, 'locksBefore': rows(locks), 'generatedPublicConfigSHA256': sha(template),
          'expectedCurrentOnly': EXPECTED, 'reusedActual': c['reusedActual'], 'notDeployable': True})

    def command(label, argv, timeout):
        remaining = 1800 - (time.monotonic() - started)
        if remaining < 1:
            raise ValueError('whole controller budget exhausted')
        timeout = max(1, int(min(remaining, timeout)))
        folder = output / label
        folder.mkdir()
        capture = [sys.executable, '-B', str(REPO / 'ci/group-effects/capture.py'), '--stdout', str(folder / 'stdout.jsonl'),
                   '--stderr', str(folder / 'stderr.log'), '--'] + argv
        wrapped = [sys.executable, '-B', str(REPO / 'ci/group-effects/resource_guard.py'), '--cwd', str(work / 'server'),
                   '--log', str(folder / 'guard.log'), '--record', str(folder / 'resource.json'), '--min-free-mib', '1024',
                   '--timeout', str(timeout), '--poll-interval', '.2', '--allocated-root', str(work),
                   '--max-allocated-mib', '16384', '--'] + capture
        child = subprocess.run(wrapped, check=False, timeout=timeout + 30)
        resource = json.loads(regular(folder / 'resource.json'))
        require_guard(resource, child.returncode)
        return folder, resource['exit_code']

    _, code = command('mongo-ready', [sys.executable, '-B', str(REPO / 'ci/backend-integration.py'), 'check-ready',
                      str(REPO / 'ci-results/backend-integration'), os.environ['GITHUB_RUN_ID'] + ':' + os.environ['GITHUB_RUN_ATTEMPT'],
                      os.environ['GITHUB_SHA']], 20)
    if code != 0:
        raise ValueError('owned Mongo/pairedRedis not ready; no test dispatch')
    write(output / 'fixture-admission.json', {'ownedMongoRedisCheckExit0': True, 'SQLPrepared': False, 'signedHTTPPrepared': False,
          'owner': os.environ['GITHUB_RUN_ID'] + ':' + os.environ['GITHUB_RUN_ATTEMPT'], 'commit': os.environ['GITHUB_SHA'],
          'gateBoundary': 'Go version/env metadata may precede ready; tests do not',
          'networkBoundary': 'clean env/fixed owned loopback, not empty kernel netns'})
    clean = lambda values: ['env', '-i'] + [k + '=' + v for k, v in values.items()]
    download = {k: v for k, v in env.items() if not k.startswith('LUOBO_')}
    download.update(GOPROXY='https://proxy.golang.org', GOSUMDB='sum.golang.org')
    _, code = command('module-download', clean(download) + ['go', 'mod', 'download', '-json'], 600)
    if code != 0 or any(regular(work / p) != body for p, body in locks.items()):
        raise ValueError('locked public dependency preparation rejected')
    expression = '^(' + '|'.join(EXPECTED['tops']) + ')$'  # all four selected subtests, no slash filter.
    folder, code = command('internal_messagedelivery', clean(env) + ['go', 'test', '-mod=readonly', '-p=1', '-parallel=1',
        '-race', '-json', '-count=1', '-vet=off', '-timeout=600s', '-run', expression, './' + PACKAGE], 900)
    try:
        events = [json.loads(line) for line in regular(folder / 'stdout.jsonl').decode().splitlines() if line.strip()]
        result = audit(events, code, PACKAGE, EXPECTED)
        accepted = True
    except (ValueError, KeyError, TypeError, UnicodeError):
        result = {'accepted': False, 'exit': code, 'rawRetained': True}
        accepted = False
    write(folder / 'summary.json', result)
    post = {}
    for target, body in bodies.items():
        actual = regular(work / target)
        if actual != body or stat.S_IMODE((work / target).stat().st_mode) != c['sourceModes'][target]:
            raise ValueError('source/mode changed during failed-only retry')
        post[target] = actual
    if regular(work / 'server/config/config.yaml') != template:
        raise ValueError('generated public config changed')
    verify()
    write(output / 'source-post.json', {'effectiveCount': len(post), 'effectiveDigest': digest(rows(post)),
          'allBodiesAndModesExact': True, 'locksAfter': rows({p: post[p] for p in locks}), 'generatedPublicConfigSHA256': sha(template)})
    write(output / 'summary.json', {'state': 'target4_4_passed_pending_collection_cleanup' if accepted else 'failed_or_incomplete',
          'scope': SCOPE, 'newExecutionOnly': result, 'reusedActual': c['reusedActual'], 'effectiveCount': 1202,
          'effectiveDigest': FINAL_DIGEST, 'noSQLHTTPPreparation': True, 'noOldPureOrRPCSelected': True,
          'wholeRegression': False, 'vetRun': False, 'readerAuthorityOrReadyOrRuntime': False, 'notDeployable': True,
          'collectAndCleanupStillMandatory': True})
    return 0 if accepted else 1


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    actions = parser.add_mutually_exclusive_group()
    actions.add_argument('--verify', action='store_true')
    actions.add_argument('--check-residuals', action='store_true')
    actions.add_argument('--check-cleanup', action='store_true')
    args = parser.parse_args()
    try:
        if args.verify:
            verify()
            print('Exact test-fixture-only1202 successor / failed-only4Mongo4sub source verified; no Go run.')
        elif args.check_residuals:
            check_collected()
        elif args.check_cleanup:
            check_cleanup()
        else:
            raise SystemExit(run())
    except (ValueError, KeyError, TypeError, OSError, subprocess.SubprocessError, json.JSONDecodeError):
        print('Facts failed-only retry incomplete; public raw retained, no private fixture output.', file=sys.stderr)
        raise SystemExit(2)
