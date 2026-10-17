#!/usr/bin/env python3
"""Only new facts5/4 plus one affected real RPC stored-n1/1. No full/runtime."""
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
SCOPE = 'positive_notification_non_authority_facts1202_target6_5_no_history_readgrant_runtime'
PREFIX = 'github.com/openimsdk/open-im-server/v3/'
TARGETS = {'internal/messagedelivery': {'tops': ['TestOwnedNotificationFactsCommitSurviveTerminalCleanupAndOriginalReceipt', 'TestOwnedNotificationFactsFailureRollsBackSlotCounterAndSourceReceipt', 'TestOwnedNotificationFactsDoNotTurnSeq0IntoPersistedHistory', 'TestOwnedNotificationFactsLostCommitACKResolvesWithoutBackfillOrReexecution', 'TestNotificationFactsPagesBoundDocumentsWithoutRosterCap'], 'subs': ['TestOwnedNotificationFactsCommitSurviveTerminalCleanupAndOriginalReceipt/frozen-head', 'TestOwnedNotificationFactsCommitSurviveTerminalCleanupAndOriginalReceipt/accepted-normal', 'TestOwnedNotificationFactsFailureRollsBackSlotCounterAndSourceReceipt/abort-after-facts', 'TestOwnedNotificationFactsFailureRollsBackSlotCounterAndSourceReceipt/foreign-page-collision']}, 'internal/rpc/group': {'tops': ['TestOwnedRoleRPCReceiverRiskSQLSignedCallbackUnknownRestartAndReceiptFirst'], 'subs': ['TestOwnedRoleRPCReceiverRiskSQLSignedCallbackUnknownRestartAndReceiptFirst/stored-n']}}
SOURCE_ROOT = 'ci/candidates/notification-facts/source/'
FIXTURE_TARGET = 'server/internal/rpc/group/owned_receiver_integration_test.go'
OLD_TOP = 'TestOwnedRoleRPCReceiverRiskSQLSignedCallbackUnknownRestartAndReceiptFirst'
MODIFIED = frozenset(['server/internal/groupeffects/owned_notification_delivery.go',
                      'server/internal/messagedelivery/action_receiver.go', FIXTURE_TARGET])
NEW = frozenset(['server/internal/messagedelivery/notification_facts.go',
                 'server/internal/messagedelivery/notification_facts_test.go',
                 'server/internal/messagedelivery/notification_facts_integration_test.go'])
OVERLAY_TARGETS = MODIFIED | NEW
PARENT_DIGEST = '362adde7b3b009e3c575ee18bb28f510c210cf3b9e24091cab7accfee4e76f12'
FINAL_DIGEST = '6b3e82ae99693aa9c2d390a745cd810bb3ee006529bf1ef41ad9582416a65f22'
MONGO = 'mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration'
ZERO_FLAGS = {'LUOBO_EMAIL_INTEGRATION': '0', 'LUOBO_RUN_LEGACY_LOAD_TESTS': '0',
              'LUOBO_RUN_LEGACY_EXAMPLES': '0'}
DEPENDENCY_PATHS = frozenset([
 '.github/workflows/notification-facts-targeted.yml', 'ci/notification-facts/run.py',
 'ci/notification-facts/test_run.py', 'ci/namespace-progress-performance/run.py',
 'ci/namespace-progress-performance/inputs.json', 'ci/backend-integration.sh',
 'ci/backend-integration.py', 'ci/backend-integration.json', 'ci/group-ordinary/mysql_fixture.py',
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


def apply_overlay(c, previous, root):
    result, seen = dict(previous), set()
    if len(c['overlay']) != 6:
        raise ValueError('exact five formatted facts sources plus one declared RPC fixture required')
    for row in c['overlay']:
        target = name(row['target'])
        before = previous.get(target)
        if (target not in OVERLAY_TARGETS or target in seen
                or (before is not None) != (target in MODIFIED)
                or row['beforeSHA256'] != (sha(before) if before is not None else None)
                or row['path'] != SOURCE_ROOT + target or row['mode'] != '0o644'):
            raise ValueError('exact three-modified/three-new parent-before overlay required')
        seen.add(target)
        result[target] = pinned(row, root)
    source = root / SOURCE_ROOT
    actual = {str(path.relative_to(root)) for path in source.rglob('*') if path.is_file() or path.is_symlink()}
    if seen != OVERLAY_TARGETS or actual != {row['path'] for row in c['overlay']}:
        raise ValueError('extra or missing performance source')
    return result



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




EXPECTED = {'topPASS': 6, 'subPASS': 5, 'skip': 0, 'fail': 0, 'packages': 2,
            'vet': False, 'fullRegression': False}
MONGO_KEYS = ('LUOBO_STORAGE_TEST_MONGO_URI', 'LUOBO_GROUP_ROLE_TEST_MONGO_URI',
              'LUOBO_GROUP_RECEIVER_TEST_MONGO_URI')
SQL_KEY = 'LUOBO_RISK_TEST_MYSQL_FIXTURE'
OUTPUT = 'ci-results/notification-facts'


def expression(package, selected):
    if package == 'internal/messagedelivery':
        return '^(' + '|'.join(selected['tops']) + ')$'  # No slash: all four new subtests execute.
    if package == 'internal/rpc/group' and selected == TARGETS[package]:
        return '^' + OLD_TOP + '$/^stored-n$'  # Parent SQL setup runs; sibling silent is not selected.
    raise ValueError('exact two package selections required')


def old_business_body(raw):
    text = raw.decode()
    start = text.index('func ' + OLD_TOP + '(')
    return text[start:text.index('\ntype ownedSilentReader', start)]


def declared_fixture_only(before, after):
    original, adapted = old_business_body(before), old_business_body(after)
    old_query = 'count, err = db.Collection(messagedelivery.Receipts).CountDocuments(ctx, bson.M{"source_id": id})'
    new_query = 'count, err = db.Collection(messagedelivery.Receipts).CountDocuments(ctx, bson.M{"_id": result.ID, "epoch": epoch, "source_id": id, "branches": bson.M{"$exists": true}, "accepted_at": bson.M{"$exists": true}})'
    changes = [('ctx, s, db, _ := ownedRPCFreshFixture(', 'ctx, s, db, _ := roleEffectsFixture('),
               (new_query, old_query),
               ('\t\t\tassertOwnedRPCFactsRows(t, ctx, db, epoch, activation, rows[0], result)\n', '')]
    for new, old in changes:
        if adapted.count(new) != 1:
            raise ValueError('declared fixture transformation missing or repeated')
        adapted = adapted.replace(new, old, 1)
    if adapted != original:
        raise ValueError('old RPC business body changed outside declared fixture/count adaptation')


def verify():
    c = json.loads(regular(INPUTS))
    if (c['schema'] != 1 or c['scope'] != SCOPE or c['parentCount'] != 1199
            or c['parentDigest'] != PARENT_DIGEST or c['effectiveCount'] != 1202
            or c['effectiveDigest'] != FINAL_DIGEST or c['targets'] != TARGETS or c['expected'] != EXPECTED
            or c['parentRunner']['path'] != 'ci/namespace-progress-performance/run.py'
            or c['parentInputs']['path'] != 'ci/namespace-progress-performance/inputs.json'
            or c['origin']['formattedFactsManifestSHA256'] != 'df56d6a60970055ca19451887ea93dfa851b796602401cce55e53cd96d483afc'
            or c['origin']['fixtureFormattedSuccessorManifestSHA256'] != 'acf5fb8586c197bf3ab272a506b5c55fee5714c8654ec59f1b729271c7ee0bfd'
            or c['execution'] != {'Go': 0, 'database': 0, 'cloud': 0, 'parent1199CloudAccepted': False}
            or {x['path'] for x in c['dependencies']} != DEPENDENCY_PATHS
            or len(c['dependencies']) != len(DEPENDENCY_PATHS)):
        raise ValueError('exact formatted facts + declared fixture / 6top5sub source-only contract required')
    for row in c['dependencies']:
        pinned(row, REPO)
    pinned(c['parentRunner'], REPO)
    pinned(c['parentInputs'], REPO)
    spec = importlib.util.spec_from_file_location('facts_source_only_parent1199', REPO / c['parentRunner']['path'])
    parent = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(parent)
    prior, before = parent.verify()  # Never parent.run(), parent targets, namespace tests or old fixtures.
    if len(before) != 1199 or prior['effectiveDigest'] != PARENT_DIGEST:
        raise ValueError('exact formatted1199 source parent required; not a Go result')
    bodies = apply_overlay(c, before, REPO)
    modes = dict(prior['sourceModes'])
    modes.update({row['target']: 0o644 for row in c['overlay']})
    if (len(bodies) != 1202 or rows(bodies) != c['effectiveSource']
            or digest(rows(bodies)) != FINAL_DIGEST or modes != c['sourceModes']):
        raise ValueError('facts1202 source bodies/modes/digest differ')
    for target, body in before.items():
        if target.endswith('_test.go') and target != FIXTURE_TARGET and bodies[target] != body:
            raise ValueError('unrelated previous test body changed')
    declared_fixture_only(before[FIXTURE_TARGET], bodies[FIXTURE_TARGET])
    new_tests = bodies['server/internal/messagedelivery/notification_facts_test.go'] + bodies['server/internal/messagedelivery/notification_facts_integration_test.go']
    if Counter(x.decode() for x in re.findall(rb'^func (Test\w+)\(', new_tests, re.M)) != Counter({x: 1 for x in TARGETS['internal/messagedelivery']['tops']}):
        raise ValueError('exact five new test tops required')
    for sub in TARGETS['internal/messagedelivery']['subs']:
        if json.dumps(sub.split('/', 1)[1]).encode() not in new_tests:
            raise ValueError('fixed new subtest missing')
    if expression('internal/rpc/group', TARGETS['internal/rpc/group']) != '^' + OLD_TOP + '$/^stored-n$':
        raise ValueError('old sibling selection changed')
    return c, bodies


def fixture_environment(env):
    for key, value in {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux',
                       'GOTOOLCHAIN': 'local', 'GOWORK': 'off'}.items():
        if env.get(key) != value:
            raise ValueError('hosted disposable fixture environment required')
    run, attempt, commit = env.get('GITHUB_RUN_ID', ''), env.get('GITHUB_RUN_ATTEMPT', ''), env.get('GITHUB_SHA', '')
    if not re.fullmatch('[0-9]+', run) or not re.fullmatch('[0-9]+', attempt) or not re.fullmatch('[0-9a-f]{40}', commit):
        raise ValueError('exact fixture run/attempt/commit required')
    selected = dict(ZERO_FLAGS, **{key: MONGO for key in MONGO_KEYS})
    private = '/tmp/luobo-risk-' + run + '-' + attempt + '/private/fixture.json'
    if any(env.get(key) != value for key, value in selected.items()):
        raise ValueError('missing or foreign fixture selection')
    if SQL_KEY in env and env[SQL_KEY] != private:
        raise ValueError('foreign SQL fixture input refused')
    if any(key.startswith('LUOBO_') and key not in selected and key != SQL_KEY for key in env):
        raise ValueError('unexpected business integration environment')
    return dict(selected, **{SQL_KEY: private})


def require_private_fixture(path):
    info, parent = path.lstat(), path.parent.lstat()
    if (path.resolve() != path or not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600
            or info.st_uid != os.getuid() or info.st_nlink != 1 or info.st_size > 16384
            or not stat.S_ISDIR(parent.st_mode) or stat.S_IMODE(parent.st_mode) != 0o700
            or parent.st_uid != os.getuid()):
        raise ValueError('owned private SQL file required; no test dispatch')
    # Actual UUID/nonce/password/schema checked by unchanged mysql_fixture.py check,
    # and again by the real riskmysql Go helper. No contents enter public evidence.


def test_environment(base, work, goroot, bindings):
    env = {'PATH': base['PATH'], 'GOROOT': goroot, 'GOTOOLCHAIN': 'local', 'GOWORK': 'off',
           'GOENV': 'off', 'CGO_ENABLED': '1', 'GOMAXPROCS': '2', 'GOMEMLIMIT': '900MiB',
           'GOPROXY': 'off', 'GOSUMDB': 'off', 'GOAUTH': 'off', 'GOPRIVATE': '', 'GONOPROXY': '', 'GONOSUMDB': '', **bindings}
    if 'HOME' in base:
        env['HOME'] = base['HOME']  # Actual original value only; never an owned replacement.
    for key, folder in [('GOMODCACHE', 'modcache'), ('GOCACHE', 'buildcache'), ('GOPATH', 'gopath'), ('TMPDIR', 'tmp')]:
        env[key] = str(work / folder)
    env['GOTMPDIR'] = env['TMPDIR']
    return env


def require_collected(mongo, sql):
    fields = ('mongo_user_database_count', 'redis_db15_dbsize_before_info', 'redis_db15_key_count',
              'redis_db15_expiring_keys', 'unexpected_redis_database_count')
    if mongo.get('state') != 'no_fixture_residue' or any(type(mongo.get(k)) is not int or mongo[k] != 0 for k in fields):
        raise ValueError('owned Mongo/Redis must have zero residual data')
    if (any(type(sql.get(k)) is not int or sql[k] != v for k, v in {
            'ownedDatabaseCount': 1, 'ownedUserCount': 1, 'ownedTableCount': 2, 'unexpectedDatabases': 0}.items())
            or sql.get('UUIDAndClaimConfirmed') is not True or sql.get('privateDiagnosticsUploaded') is not False):
        raise ValueError('owned SQL exact fixture collection missing or contaminated')


def require_cleanup(mongo, sql):
    if (type(mongo.get('container_cleanup_exit_code')) is not int or mongo['container_cleanup_exit_code'] != 0
            or mongo.get('scope') != 'exact_run_label_and_two_names_only'
            or sql.get('state') != 'passed' or sql.get('scope') != 'single_exact_id_name_run_nonce_image'
            or any(sql.get(k) is not True for k in ('logicalDatabaseAndUserZero', 'normalShutdownJoined', 'exactContainerAbsent'))):
        raise ValueError('exact fixture cleanup unconfirmed')


def check_collected():
    fixture_environment(os.environ)
    require_collected(json.loads(regular(REPO / 'ci-results/backend-integration/residual-check.json')),
                      json.loads(regular(REPO / 'ci-results/group-ordinary-mysql/collected.json')))
    write(REPO / OUTPUT / 'collected-acceptance.json', {'state': 'owned_fixtures_exactly_collected',
          'zeroMongoRedisRequired': True, 'SQLFixtureDatabaseUserTables': [1, 1, 2], 'wholeJobAccepted': False})


def check_cleanup():
    fixture_environment(os.environ)
    require_cleanup(json.loads(regular(REPO / 'ci-results/backend-integration/container-cleanup.json')),
                    json.loads(regular(REPO / 'ci-results/group-ordinary-mysql/cleanup.json')))
    collected = json.loads(regular(REPO / OUTPUT / 'collected-acceptance.json'))
    result = json.loads(regular(REPO / OUTPUT / 'summary.json'))
    if (collected.get('state') != 'owned_fixtures_exactly_collected' or result.get('state') != 'target6_5_passed_pending_fixture_collection_cleanup'):
        raise ValueError('target and collection receipts incomplete')
    write(REPO / OUTPUT / 'cleanup-acceptance.json', {'state': 'target6_5_and_exact_owned_fixture_cleanup_passed',
          'scope': SCOPE, 'notDeployable': True, 'historyReadGrant': False, 'ReadyOrRuntimeActivation': False,
          'sourceDigest': FINAL_DIGEST, 'topPASS': 6, 'subPASS': 5, 'wholeRegression': False})


def run():
    started = time.monotonic()
    bindings = fixture_environment(os.environ)
    if os.getuid() == 0:
        raise ValueError('nonroot test owner required')
    c, bodies = verify()
    temporary = Path(os.environ['RUNNER_TEMP'])
    work = temporary / ('notification-facts-' + os.environ['GITHUB_RUN_ID'] + '-' + os.environ['GITHUB_RUN_ATTEMPT'])
    if not temporary.is_absolute() or temporary.resolve() != temporary or work.exists():
        raise ValueError('fresh canonical work required')
    fs = os.statvfs(temporary)
    if fs.f_bavail * fs.f_frsize < 4 * 1024**3:
        raise ValueError('4GiB bootstrap floor; no Go child started')
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
    for folder in ['modcache', 'buildcache', 'gopath', 'tmp']:
        (work / folder).mkdir(mode=0o700)
    env = test_environment(os.environ, work, goroot, bindings)
    locks = {path: bodies[path] for path in ['server/go.mod', 'server/go.sum']}
    write(output / 'source.json', {'scope': SCOPE, 'commit': os.environ['GITHUB_SHA'], 'inputsSHA256': sha(regular(INPUTS)),
          'parent1199Digest': PARENT_DIGEST, 'parent1199CloudPassClaimed': False,
          'effectiveCount': 1202, 'effectiveDigest': FINAL_DIGEST, 'goVersion': version, 'targets': TARGETS,
          'locksBefore': rows(locks), 'generatedPublicConfigSHA256': sha(template),
          'newTopSub': [5, 4], 'directlyAffectedOldTopSub': [1, 1],
          'oldRPCFixtureExplicitlyAdapted': True, 'noRuntimeOrReadGrantOrReady': True})

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

    _, code = command('mongo-redis-ready', [sys.executable, '-B', str(REPO / 'ci/backend-integration.py'), 'check-ready',
                      str(REPO / 'ci-results/backend-integration'), os.environ['GITHUB_RUN_ID'] + ':' + os.environ['GITHUB_RUN_ATTEMPT'],
                      os.environ['GITHUB_SHA']], 20)
    if code != 0:
        raise ValueError('owned Mongo/Redis readiness rejected before test dispatch')
    _, code = command('mysql-ready', [sys.executable, '-B', str(REPO / 'ci/group-ordinary/mysql_fixture.py'), 'check'], 60)
    if code != 0:
        raise ValueError('owned private SQL readiness rejected before test dispatch')
    require_private_fixture(Path(bindings[SQL_KEY]))
    write(output / 'fixture-admission.json', {'MongoRedisCheckReadyExit0': True, 'privateSQLCheckExit0': True,
          'owner': os.environ['GITHUB_RUN_ID'] + ':' + os.environ['GITHUB_RUN_ATTEMPT'], 'commit': os.environ['GITHUB_SHA'],
          'gateBoundary': 'Go version/env metadata may run first; Go tests require exact owned-ready checks',
          'networkBoundary': 'clean environment and fixed owned cloud loopback; no kernel egress isolation claim',
          'credentialsInPublicEvidence': False, 'realSignedLoopbackHTTPSelected': True, 'realVendorSelected': False})
    clean = lambda values: ['env', '-i'] + [key + '=' + value for key, value in values.items()]
    download = {key: value for key, value in env.items() if not key.startswith('LUOBO_')}
    download.update(GOPROXY='https://proxy.golang.org', GOSUMDB='sum.golang.org')
    _, code = command('module-download', clean(download) + ['go', 'mod', 'download', '-json'], 600)
    if code != 0 or any(regular(work / path) != body for path, body in locks.items()):
        raise ValueError('locked public dependency preparation rejected')
    outcomes, errors = {}, []
    for package, selected in TARGETS.items():
        argv = clean(env) + ['go', 'test', '-mod=readonly', '-p=1', '-parallel=1', '-race', '-json', '-count=1',
                             '-vet=off', '-timeout=600s', '-run', expression(package, selected), './' + package]
        folder, code = command(package.replace('/', '_'), argv, 900)
        try:
            events = [json.loads(line) for line in regular(folder / 'stdout.jsonl').decode().splitlines() if line.strip()]
            outcomes[package] = audit(events, code, package, selected)
        except (ValueError, UnicodeError, TypeError, KeyError):
            outcomes[package] = {'accepted': False, 'exit': code, 'rawRetained': True}
            errors.append(package)
        write(folder / 'summary.json', outcomes[package])
    post = {}
    for target, body in bodies.items():
        actual = regular(work / target)
        if actual != body or stat.S_IMODE((work / target).stat().st_mode) != c['sourceModes'][target]:
            raise ValueError('source/mode changed during selected targets')
        post[target] = actual
    if regular(work / 'server/config/config.yaml') != template:
        raise ValueError('generated public config changed')
    verify()
    write(output / 'source-post.json', {'effectiveCount': len(post), 'effectiveDigest': digest(rows(post)),
          'allSourceBodiesAndModesExact': True, 'locksAfter': rows({path: post[path] for path in locks}),
          'generatedPublicConfigSHA256': sha(template)})
    accepted = not errors and sum(x['topPASS'] for x in outcomes.values()) == 6 and sum(x['subPASS'] for x in outcomes.values()) == 5
    write(output / 'summary.json', {'state': 'target6_5_passed_pending_fixture_collection_cleanup' if accepted else 'failed_or_incomplete',
          'scope': SCOPE, 'packages': outcomes, 'effectiveCount': 1202, 'sourceDigest': FINAL_DIGEST,
          'currentExecutionExpected': {'topPASS': 6, 'subPASS': 5}, 'newTopSub': [5, 4], 'adaptedOldTopSub': [1, 1],
          'wholeRegression': False, 'vetRun': False, 'runtimeSwitch': False, 'notDeployable': True,
          'readerAuthorization': False, 'ReadyOrMigrationActivated': False,
          'fixtureCollectionAndCleanup': 'mandatory separate always workflow steps; this test summary is not overall acceptance',
          'networkBoundary': 'fixed owned cloud loopback with clean env; not empty netns',
          'workRetainedForEphemeralRunnerCleanup': True,
          'unclosed': ['history read policy/later join/removed-self and lawful alias proof',
                       'trusted historical drain/lifecycle/importer', 'StartTransfer final runtime activation']})
    return 0 if accepted else 1


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    selection = parser.add_mutually_exclusive_group()
    selection.add_argument('--verify', action='store_true')
    selection.add_argument('--check-residuals', action='store_true')
    selection.add_argument('--check-cleanup', action='store_true')
    args = parser.parse_args()
    try:
        if args.verify:
            verify()
            print('Exact formatted1199 + facts5 + declared RPC fixture =1202 / target6top5sub source verified; no tests run.')
        elif args.check_residuals:
            check_collected()
        elif args.check_cleanup:
            check_cleanup()
        else:
            raise SystemExit(run())
    except (ValueError, KeyError, TypeError, OSError, subprocess.SubprocessError, json.JSONDecodeError):
        print('Facts targeted validation incomplete; original public evidence retained, private fixture diagnostics stay private.', file=sys.stderr)
        raise SystemExit(2)
