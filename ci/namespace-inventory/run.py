#!/usr/bin/env python3
"""Read-only namespace inventory target8; no importer, runtime or full regression."""
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
SCOPE = 'untrusted_namespace_inventory_source1184_target8_only_no_apply_or_runtime'
PREFIX = 'github.com/openimsdk/open-im-server/v3/'
MONGO = 'mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration'
REDIS = 'redis://127.0.0.1:36379/15'
TARGETS = {'internal/namespaceinventory': {'tops': ['TestInventoryPreservesCollisionAndErasedEvidence',
                                          'TestInventoryRejectsChangeRestartAndLimits',
                                          'TestInventoryCounterClassesAndMissingHeadNeverInferZero',
                                          'TestInventoryPrivateRoundTripAndNoClobber',
                                          'TestInventoryRejectsPrivateTamperAndPathAliases',
                                          'TestInventoryPrivateConnectionIsExplicitBoundInput',
                                          'TestNamespaceInventoryOwnedStoresReadOnly'],
                                 'subs': ['TestInventoryRejectsChangeRestartAndLimits/changed-document',
                                          'TestInventoryRejectsChangeRestartAndLimits/mongo-restart',
                                          'TestInventoryRejectsChangeRestartAndLimits/redis-failover',
                                          'TestInventoryRejectsChangeRestartAndLimits/changed-redis-head',
                                          'TestInventoryRejectsChangeRestartAndLimits/bounded-before-retention',
                                          'TestInventoryCounterClassesAndMissingHeadNeverInferZero/raw-counter',
                                          'TestInventoryCounterClassesAndMissingHeadNeverInferZero/schema-with-maximum',
                                          'TestInventoryCounterClassesAndMissingHeadNeverInferZero/nonstring-kind',
                                          'TestInventoryCounterClassesAndMissingHeadNeverInferZero/negative',
                                          'TestInventoryCounterClassesAndMissingHeadNeverInferZero/overflow',
                                          'TestInventoryRejectsPrivateTamperAndPathAliases/changed-derived-data',
                                          'TestInventoryRejectsPrivateTamperAndPathAliases/duplicate-json-field',
                                          'TestInventoryRejectsPrivateTamperAndPathAliases/world-readable',
                                          'TestInventoryRejectsPrivateTamperAndPathAliases/hard-link',
                                          'TestInventoryRejectsPrivateTamperAndPathAliases/symlink-parent',
                                          'TestInventoryPrivateConnectionIsExplicitBoundInput/valid-explicit',
                                          'TestInventoryPrivateConnectionIsExplicitBoundInput/wrong-database',
                                          'TestInventoryPrivateConnectionIsExplicitBoundInput/wrong-replica-set',
                                          'TestInventoryPrivateConnectionIsExplicitBoundInput/direct-primary-ambiguity',
                                          'TestInventoryPrivateConnectionIsExplicitBoundInput/wrong-redis-database',
                                          'TestInventoryPrivateConnectionIsExplicitBoundInput/duplicate-uri-option']},
 'cmd/tools/group-namespace-capture': {'tops': ['TestNamespaceCaptureCLIInspectAndSafeErrors'],
                                       'subs': ['TestNamespaceCaptureCLIInspectAndSafeErrors/default-inspect-no-connection',
                                                'TestNamespaceCaptureCLIInspectAndSafeErrors/no-implicit-capture',
                                                'TestNamespaceCaptureCLIInspectAndSafeErrors/no-activation-or-credential-flags']}}
NEW_TARGETS = frozenset(['server/cmd/tools/group-namespace-capture/main.go',
 'server/cmd/tools/group-namespace-capture/main_test.go',
 'server/internal/namespaceinventory/capture.go',
 'server/internal/namespaceinventory/capture_test.go',
 'server/internal/namespaceinventory/files.go',
 'server/internal/namespaceinventory/files_test.go',
 'server/internal/namespaceinventory/live.go',
 'server/internal/namespaceinventory/live_integration_test.go',
 'server/internal/namespaceinventory/operator.go'])
DEPENDENCY_PATHS = frozenset(['.github/workflows/group-namespace-origin-targeted.yml',
 '.github/workflows/namespace-inventory-targeted.yml',
 'ci/backend-integration.json',
 'ci/backend-integration.py',
 'ci/backend-integration.sh',
 'ci/group-effects/capture.py',
 'ci/group-effects/resource_guard.py',
 'ci/group-namespace-origin/run.py',
 'ci/group-namespace-origin/test_run.py',
 'ci/namespace-inventory/run.py',
 'ci/namespace-inventory/test_run.py',
 'ci/secret_scan.py'])
REUSED_PRIOR_TARGETS = {'namespaceOriginRun': 38970214094,
 'historicalNamespaceTopPASS': 10,
 'historicalNamespaceSubPASS': 15,
 'seedRun': 38964544432,
 'historicalSeedTopPASS': 8,
 'historicalSeedSubPASS': 21,
 'claim': 'Historical accepted results retained, not rerun or counted as new inventory checks. No full '
          'regression or deployment claim.'}

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


def pinned(row):
    raw = regular(REPO / name(row['path']))
    if len(raw) != row['bytes'] or sha(raw) != row['sha256']:
        raise ValueError('pinned input differs')
    return raw


def rows(bodies):
    return [{'path': p, 'bytes': len(b), 'sha256': sha(b)} for p, b in sorted(bodies.items())]


def digest(items):
    return sha(b''.join(r['path'].encode() + b'\0' + r['sha256'].encode() + b'\0'
                        for r in sorted(items, key=lambda r: r['path'])))


def apply_overlay(c, previous):
    result, seen = dict(previous), set()
    if len(c['overlay']) != 9:
        raise ValueError('exact nine new inventory source paths required')
    for row in c['overlay']:
        target = name(row['target'])
        if (target not in NEW_TARGETS or target in seen or target in previous
                or row['beforeSHA256'] is not None
                or row['path'] != 'ci/candidates/namespace-inventory/source/' + target
                or row['mode'] != '0o644'):
            raise ValueError('inventory source must be new and namespace-private')
        path = REPO / row['path']
        if stat.S_IMODE(path.stat().st_mode) != 0o644:
            raise ValueError('inventory payload mode differs')
        seen.add(target)
        result[target] = pinned(row)
    source = REPO / 'ci/candidates/namespace-inventory/source'
    actual = {str(p.relative_to(REPO)) for p in source.rglob('*') if p.is_file() or p.is_symlink()}
    if seen != NEW_TARGETS or actual != {r['path'] for r in c['overlay']}:
        raise ValueError('extra or missing inventory source')
    return result


def verify():
    c = json.loads(regular(INPUTS))
    if (c['schema'] != 1 or c['scope'] != SCOPE or c['parentCount'] != 1175
            or c['effectiveCount'] != 1184 or c['parentDigest'] != 'da8413685e7ce04648e7c3ac48a694cd214b15f96afdaa6e1d9e34d0701d838a'
            or c['parentRunner']['path'] != 'ci/group-namespace-origin/run.py'
            or c['parentInputs']['path'] != 'ci/group-namespace-origin/inputs.json'
            or c['targets'] != TARGETS or c['reusePriorAcceptedTargets'] != REUSED_PRIOR_TARGETS
            or c['expected'] != {'topPASS': 8, 'subPASS': 24, 'skip': 0, 'fail': 0, 'packages': 2, 'vet': False, 'fullRegression': False}
            or {r['path'] for r in c['dependencies']} != DEPENDENCY_PATHS
            or len(c['dependencies']) != len(DEPENDENCY_PATHS)):
        raise ValueError('exact inventory1184 target8 contract required')
    for row in c['dependencies']:
        pinned(row)
    pinned(c['parentRunner'])
    pinned(c['parentInputs'])
    spec = importlib.util.spec_from_file_location('source_only_namespace1175_parent', REPO / c['parentRunner']['path'])
    parent = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(parent)
    prior, before = parent.verify()  # Source only: never calls parent.run() or full29.
    if len(before) != 1175 or prior['effectiveDigest'] != c['parentDigest']:
        raise ValueError('accepted build-fixed1175 parent differs')
    bodies = apply_overlay(c, before)
    modes = dict(prior['sourceModes'])
    modes.update({r['target']: 0o644 for r in c['overlay']})
    if (len(bodies) != 1184 or rows(bodies) != c['effectiveSource']
            or digest(rows(bodies)) != c['effectiveDigest'] or modes != c['sourceModes']):
        raise ValueError('inventory source bodies/modes/digest differ')
    for package, selected in TARGETS.items():
        present = []
        for path, raw in bodies.items():
            if str(PurePosixPath(path).parent) == 'server/' + package and path.endswith('_test.go'):
                present += [n.decode() for n in re.findall(rb'^func (Test\w+)\(', raw, re.M)]
        if Counter(present) != Counter(selected['tops']):
            raise ValueError('actual whole new package tops differ from exact selection')
    return c, bodies


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
    exact = Counter({n: 1 for n in wanted})
    if code != 0 or starts != exact or passes != exact or len(footer) != 1:
        raise ValueError('missing duplicate or failed exact selection')
    return {'topPASS': len(expected['tops']), 'subPASS': len(expected['subs']), 'skip': 0, 'fail': 0,
            'tops': sorted(expected['tops']), 'subs': sorted(expected['subs']), 'footer': footer[0]}


def require_guard(record, code):
    if (record.get('status') not in ('passed', 'failed') or record.get('exit_code') is None
            or code != record['exit_code'] or (record['status'] == 'passed') != (code == 0)):
        raise ValueError('guard/monitor/timeout/remaining-process rejection')


def fixture_environment(env):
    for key, value in {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux',
                       'GOTOOLCHAIN': 'local', 'GOWORK': 'off'}.items():
        if env.get(key) != value:
            raise ValueError('hosted fixture environment required')
    run, attempt = env.get('GITHUB_RUN_ID', ''), env.get('GITHUB_RUN_ATTEMPT', '')
    if not re.fullmatch('[0-9]+', run) or not re.fullmatch('[0-9]+', attempt) or not re.fullmatch('[0-9a-f]{40}', env.get('GITHUB_SHA', '')):
        raise ValueError('exact run/attempt/commit required')
    selected = {'LUOBO_NAMESPACE_INVENTORY_TEST_MONGO_URI': MONGO,
                'LUOBO_NAMESPACE_INVENTORY_TEST_REDIS_URL': REDIS,
                'LUOBO_NAMESPACE_INVENTORY_TEST_REPLICA_SET': 'luoboIntegration',
                'LUOBO_EMAIL_INTEGRATION': '0', 'LUOBO_RUN_LEGACY_LOAD_TESTS': '0', 'LUOBO_RUN_LEGACY_EXAMPLES': '0'}
    if any(env.get(key) != value for key, value in selected.items()):
        raise ValueError('missing or foreign inventory fixture selection')
    if any(key.startswith('LUOBO_') and key not in selected for key in env):
        raise ValueError('unexpected business fixture environment')
    return selected


def require_zero_residuals(record):
    # This lane performs no application cache writes. Unlike the borrowed
    # broader integration lane, even expiring residue is unexpected here.
    fields = ('mongo_user_database_count', 'redis_db15_dbsize_before_info',
              'redis_db15_key_count', 'redis_db15_expiring_keys', 'unexpected_redis_database_count')
    if (record.get('state') != 'no_fixture_residue'
            or any(type(record.get(key)) is not int or record[key] != 0 for key in fields)):
        raise ValueError('read-only inventory fixture must have zero residue')


def check_collected():
    fixture_environment(os.environ)
    record = json.loads(regular(REPO / 'ci-results/backend-integration/residual-check.json'))
    require_zero_residuals(record)
    write(REPO / 'ci-results/namespace-inventory/residual-acceptance.json', {
        'state': 'inventory_fixture_zero_residue', 'zeroMongoRedisRequired': True,
        'wholeJobAccepted': False, 'exactContainerCleanupStillSeparate': True})


def write(path, value):
    with path.open('x') as out:
        json.dump(value, out, indent=2)
        out.write('\n')
        out.flush()
        os.fsync(out.fileno())
    fd = os.open(path.parent, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def run():
    started = time.monotonic()
    bindings = fixture_environment(os.environ)
    if os.getuid() == 0:
        raise ValueError('nonroot test owner required')
    c, bodies = verify()
    version = subprocess.check_output(['go', 'version'], text=True, timeout=15).strip()
    if version != 'go version go1.27.2 linux/amd64':
        raise ValueError('exact Go compiler required')
    temporary = Path(os.environ['RUNNER_TEMP'])
    work = temporary / ('namespace-inventory-' + os.environ['GITHUB_RUN_ID'] + '-' + os.environ['GITHUB_RUN_ATTEMPT'])
    if not temporary.is_absolute() or temporary.resolve() != temporary or work.exists():
        raise ValueError('fresh canonical work required')
    fs = os.statvfs(temporary)
    if fs.f_bavail * fs.f_frsize < 4 * 1024**3:
        raise ValueError('4GiB work bootstrap floor; no Go test child started')
    work.mkdir(mode=0o700)
    output = REPO / 'ci-results/namespace-inventory'
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    for target, body in bodies.items():
        p = work / target
        p.parent.mkdir(parents=True, exist_ok=True)
        with p.open('xb') as out:
            out.write(body)
        p.chmod(c['sourceModes'][target])
    template = bodies['server/config/config.example.yaml']
    with (work / 'server/config/config.yaml').open('xb') as out:
        out.write(template)
    env = {'PATH': os.environ['PATH'], 'GOROOT': subprocess.check_output(['go', 'env', 'GOROOT'], text=True, timeout=15).strip(),
           'GOTOOLCHAIN': 'local', 'GOWORK': 'off', 'GOENV': 'off', 'CGO_ENABLED': '1',
           'GOMAXPROCS': '2', 'GOMEMLIMIT': '900MiB'}
    if 'HOME' in os.environ:
        env['HOME'] = os.environ['HOME']  # Preserve the real value, never reassign to owned paths.
    for key, folder in [('GOMODCACHE', 'modcache'), ('GOCACHE', 'buildcache'), ('GOPATH', 'gopath'), ('TMPDIR', 'tmp')]:
        p = work / folder
        p.mkdir(mode=0o700)
        env[key] = str(p)
    env['GOTMPDIR'] = env['TMPDIR']
    expected_locks = {p: bodies[p] for p in ('server/go.mod', 'server/go.sum')}
    write(output / 'source.json', {'scope': SCOPE, 'commit': os.environ['GITHUB_SHA'], 'inputsSHA256': sha(regular(INPUTS)),
          'parent1175Digest': c['parentDigest'], 'effectiveCount': 1184, 'effectiveDigest': c['effectiveDigest'],
          'goVersion': version, 'targets': TARGETS, 'locksBefore': rows(expected_locks),
          'generatedPublicConfigSHA256': sha(template), 'noRuntimeAdoption': True})

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
        raise ValueError('owned Mongo/Redis readiness rejected before Go test dispatch')
    write(output / 'fixture-admission.json', {'MongoRedisCheckReadyExit0': True,
          'owner': os.environ['GITHUB_RUN_ID'] + ':' + os.environ['GITHUB_RUN_ATTEMPT'],
          'commit': os.environ['GITHUB_SHA'], 'SQLSelected': False, 'VendorOrCallbackSelected': False,
          'gateBoundary': 'Go version/env metadata may run before fixture readiness; no Go test dispatch before owned-ready exit0',
          'networkBoundary': 'fixed owned cloud loopback contracts; no kernel egress isolation claim'})
    download_env = dict(env, GOPROXY='https://proxy.golang.org', GOSUMDB='sum.golang.org',
                        GOAUTH='off', GOPRIVATE='', GONOPROXY='', GONOSUMDB='')
    clean = lambda values: ['env', '-i'] + [k + '=' + v for k, v in values.items()]
    _, code = command('module-download', clean(download_env) + ['go', 'mod', 'download', '-json'], 600)
    if code != 0 or any(regular(work / p) != b for p, b in expected_locks.items()):
        raise ValueError('public locked dependency preparation rejected')
    test_env = dict(env, GOPROXY='off', GOSUMDB='off', **bindings)
    outcomes, errors = {}, []
    for package, selected in TARGETS.items():
        expression = '^(' + '|'.join(selected['tops']) + ')$'
        args = clean(test_env) + ['go', 'test', '-mod=readonly', '-p=1', '-parallel=1', '-race', '-json', '-count=1',
                                  '-vet=off', '-timeout=180s', '-run', expression, './' + package]
        folder, code = command(package.replace('/', '_'), args, 900)
        try:
            events = [json.loads(x) for x in regular(folder / 'stdout.jsonl').decode().splitlines() if x.strip()]
            outcomes[package] = audit(events, code, package, selected)
        except (ValueError, UnicodeError, TypeError, KeyError):
            outcomes[package] = {'accepted': False, 'exit': code, 'rawRetained': True}
            errors.append(package)
        write(folder / 'summary.json', outcomes[package])
    post = {}
    for target, body in bodies.items():
        actual = regular(work / target)
        if actual != body or stat.S_IMODE((work / target).stat().st_mode) != c['sourceModes'][target]:
            raise ValueError('source/mode changed during inventory target8 checks')
        post[target] = actual
    if regular(work / 'server/config/config.yaml') != template:
        raise ValueError('generated public config changed')
    verify()
    write(output / 'source-post.json', {'effectiveCount': len(post), 'effectiveDigest': digest(rows(post)),
          'allSourceBodiesAndModesExact': True, 'locksAfter': rows({p: post[p] for p in expected_locks}),
          'generatedPublicConfigSHA256': sha(template)})
    accepted = not errors and sum(x['topPASS'] for x in outcomes.values()) == 8 and sum(x['subPASS'] for x in outcomes.values()) == 24
    write(output / 'summary.json', {'state': 'untrusted_namespace_inventory_target8_passed' if accepted else 'failed_or_incomplete', 'scope': SCOPE,
          'packages': outcomes, 'wholeRegression': False, 'vetRun': False, 'runtimeSwitch': False,
          'notDeployable': True, 'inventoryStateAlwaysUntrustedIncomplete': True, 'unclosed': ['current inventory always untrusted/incomplete without external drain/lifecycle proof', 'chunked resumable full historical importer and lawful alias compatibility', 'persisted positive n_ reader and final activation'],
          'fixtureCollectAndCleanup': 'mandatory subsequent workflow steps; this summary alone is not overall acceptance',
          'sourceDigest': c['effectiveDigest'], 'historicalAcceptedResultsRetained': True,
          'newExecutedExpected': {'topPASS': 8, 'subPASS': 24}, 'priorAcceptedResultsRetained': REUSED_PRIOR_TARGETS,
          'networkBoundary': 'cleanenv and fixed owned loopback fixture; not the empty pure13 network namespace'})
    return 0 if accepted else 1


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument('--verify', action='store_true')
    mode.add_argument('--check-residuals', action='store_true')
    args = parser.parse_args()
    try:
        if args.verify:
            verify()
            print('Exact namespace inventory source1184/target8 verified; no Go/fixture execution.')
        elif args.check_residuals:
            check_collected()
        else:
            raise SystemExit(run())
    except (ValueError, KeyError, TypeError, OSError, subprocess.SubprocessError, json.JSONDecodeError):
        print('Namespace inventory targeted validation incomplete; original phase evidence retained.', file=sys.stderr)
        raise SystemExit(2)
