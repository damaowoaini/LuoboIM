#!/usr/bin/env python3
"""Manual new-group seed dependency targets only; no full regression or runtime adoption."""
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
SCOPE = 'group_new_seed_source1170_target8_dependency_only_not_runtime'
PREFIX = 'github.com/openimsdk/open-im-server/v3/'
MONGO = 'mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration'
TARGETS = {
    "internal/groupauthority": {
        "tops": [
            "TestNewGroupSeedCommandBindsOwnerAndFrozenRowsWithoutGrantingScope"
        ],
        "subs": [
            "TestNewGroupSeedCommandBindsOwnerAndFrozenRowsWithoutGrantingScope/owner",
            "TestNewGroupSeedCommandBindsOwnerAndFrozenRowsWithoutGrantingScope/seed-manager",
            "TestNewGroupSeedCommandBindsOwnerAndFrozenRowsWithoutGrantingScope/foreign-owner",
            "TestNewGroupSeedCommandBindsOwnerAndFrozenRowsWithoutGrantingScope/duplicate",
            "TestNewGroupSeedCommandBindsOwnerAndFrozenRowsWithoutGrantingScope/second-owner",
            "TestNewGroupSeedCommandBindsOwnerAndFrozenRowsWithoutGrantingScope/unknown-role",
            "TestNewGroupSeedCommandBindsOwnerAndFrozenRowsWithoutGrantingScope/dismissed"
        ]
    },
    "internal/messagedelivery": {
        "tops": [
            "TestNewGroupSeedProtocolPairAndClaimsUseExactIndependentNamespaces",
            "TestNewGroupSeedCommitsProtocolPairAndNeverResetsAdvancedHead",
            "TestNewGroupSeedRejectsPostWriteIdentityOrRoleDrift",
            "TestNewGroupSeedAbortRollsBackRealModelsHistoryClaimsAndPair",
            "TestNewGroupSeedRejectsOccupiedNamespacesBeforeJoinedUpsert",
            "TestNewGroupSeedPermanentOriginSurvivesGroupDeletionAndRejectsOverflow"
        ],
        "subs": [
            "TestNewGroupSeedRejectsPostWriteIdentityOrRoleDrift/group-field",
            "TestNewGroupSeedRejectsPostWriteIdentityOrRoleDrift/owner-role",
            "TestNewGroupSeedRejectsPostWriteIdentityOrRoleDrift/extra-member",
            "TestNewGroupSeedRejectsOccupiedNamespacesBeforeJoinedUpsert/direct-notification",
            "TestNewGroupSeedRejectsOccupiedNamespacesBeforeJoinedUpsert/legacy-normal",
            "TestNewGroupSeedRejectsOccupiedNamespacesBeforeJoinedUpsert/old-history",
            "TestNewGroupSeedRejectsOccupiedNamespacesBeforeJoinedUpsert/permanent-other-epoch",
            "TestNewGroupSeedRejectsOccupiedNamespacesBeforeJoinedUpsert/missing-cutover",
            "TestNewGroupSeedPermanentOriginSurvivesGroupDeletionAndRejectsOverflow/negative",
            "TestNewGroupSeedPermanentOriginSurvivesGroupDeletionAndRejectsOverflow/max-minus-one",
            "TestNewGroupSeedPermanentOriginSurvivesGroupDeletionAndRejectsOverflow/max"
        ]
    },
    "internal/rpc/group": {
        "tops": [
            "TestCreateGroupActualRPCSeedsOnlyAuthorizedOwnerOrManagerAndAbortsAtomically"
        ],
        "subs": [
            "TestCreateGroupActualRPCSeedsOnlyAuthorizedOwnerOrManagerAndAbortsAtomically/owner",
            "TestCreateGroupActualRPCSeedsOnlyAuthorizedOwnerOrManagerAndAbortsAtomically/seed-manager",
            "TestCreateGroupActualRPCSeedsOnlyAuthorizedOwnerOrManagerAndAbortsAtomically/foreign"
        ]
    }
}
REUSED_PRIOR_TARGETS = {
    "source1165": "1c15130e028787ab8b4ebf9d36746c7038e9fbbdad171b97d71a335c73a8eb2b",
    "newCIDRun": 38955834761,
    "newCIDTopPASS": 2,
    "newCIDSubPASS": 2,
    "priorCIDRun": 38950791915,
    "priorTopPASS": 5,
    "priorSubPASS": 3,
    "claim": "Historical unchanged CID results reused; none are newly executed by this target8 lane. Old pure13 lane is untouched."
}


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
    expected_targets = {
        'server/internal/groupauthority/new_group_seed_scope.go',
        'server/internal/groupauthority/new_group_seed_scope_test.go',
        'server/internal/messagedelivery/group_namespace_seed.go',
        'server/internal/messagedelivery/group_namespace_seed_integration_test.go',
        'server/internal/rpc/group/group.go',
        'server/internal/rpc/group/managed_start.go',
        'server/internal/rpc/group/new_group_seed_rpc_integration_test.go',
    }
    existing = {'server/internal/rpc/group/group.go', 'server/internal/rpc/group/managed_start.go'}
    result, seen = dict(previous), set()
    if len(c['overlay']) != 7:
        raise ValueError('exact seven-source seed overlay required')
    for row in c['overlay']:
        target = name(row['target'])
        if (target not in expected_targets or target in seen
                or row['path'] != 'ci/candidates/group-new-seed/source/' + target
                or row['mode'] != '0o644'):
            raise ValueError('unexpected seed source or mode')
        if target in existing:
            if target not in previous or row['beforeSHA256'] != sha(previous[target]):
                raise ValueError('exact two existing parent source bodies required')
        elif target in previous or row['beforeSHA256'] is not None:
            raise ValueError('five new source paths require proven parent absence')
        path = REPO / row['path']
        if stat.S_IMODE(path.stat().st_mode) != 0o644:
            raise ValueError('seed payload mode differs')
        seen.add(target)
        result[target] = pinned(row)
    if seen != expected_targets:
        raise ValueError('incomplete source closure')
    source = REPO / 'ci/candidates/group-new-seed/source'
    actual = {str(p.relative_to(REPO)) for p in source.rglob('*') if p.is_file() or p.is_symlink()}
    if actual != {r['path'] for r in c['overlay']}:
        raise ValueError('extra or missing seed source')
    return result


def verify():
    c = json.loads(regular(INPUTS))
    if (c['schema'] != 1 or c['scope'] != SCOPE or c['parentCount'] != 1165
            or c['effectiveCount'] != 1170 or len(c['overlay']) != 7
            or c['parentRunner']['path'] != 'ci/group-action-cid/run.py'
            or c['parentInputs']['path'] != 'ci/group-action-cid/inputs.json'
            or c['parentDigest'] != '1c15130e028787ab8b4ebf9d36746c7038e9fbbdad171b97d71a335c73a8eb2b'
            or c['targets'] != TARGETS or c['reusePriorCIDTargets'] != REUSED_PRIOR_TARGETS
            or c['expected'] != {'topPASS': 8, 'subPASS': 21, 'skip': 0, 'fail': 0, 'packages': 3, 'vet': False, 'fullRegression': False}):
        raise ValueError('exact source1170 new target8 contract required')
    for row in c['dependencies']:
        pinned(row)
    pinned(c['parentRunner'])
    pinned(c['parentInputs'])
    spec = importlib.util.spec_from_file_location('source_only_accepted_CID_parent', REPO / c['parentRunner']['path'])
    parent = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(parent)
    prior, before = parent.verify()  # Source only: never calls parent.run(), Go or full29.
    if len(before) != 1165 or prior['effectiveDigest'] != c['parentDigest']:
        raise ValueError('accepted CID1165 parent differs')
    bodies = apply_overlay(c, before)
    modes = dict(prior['sourceModes'])
    modes.update({r['target']: 0o644 for r in c['overlay']})
    if (len(bodies) != 1170 or rows(bodies) != c['effectiveSource']
            or digest(rows(bodies)) != c['effectiveDigest'] or modes != c['sourceModes']):
        raise ValueError('seed source bodies/modes/digest differ')
    for package, selected in TARGETS.items():
        present = []
        for path, raw in bodies.items():
            if str(PurePosixPath(path).parent) == 'server/' + package and path.endswith('_test.go'):
                present += [n.decode() for n in re.findall(rb'^func (Test\w+)\(', raw, re.M)]
        if any(present.count(n) != 1 for n in selected['tops']):
            raise ValueError('selected new top not unique in actual whole package source')
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
    selected = {'LUOBO_STORAGE_TEST_MONGO_URI': MONGO, 'LUOBO_GROUP_RECEIVER_TEST_MONGO_URI': MONGO,
                'LUOBO_GROUP_ROLE_TEST_MONGO_URI': MONGO,
                'LUOBO_RISK_TEST_MYSQL_FIXTURE': '/tmp/luobo-risk-' + run + '-' + attempt + '/private/fixture.json',
                'LUOBO_EMAIL_INTEGRATION': '0', 'LUOBO_RUN_LEGACY_LOAD_TESTS': '0', 'LUOBO_RUN_LEGACY_EXAMPLES': '0'}
    # Fixed URI is a fixture identity contract, not kernel-wide network isolation.
    for key, value in selected.items():
        if key == 'LUOBO_RISK_TEST_MYSQL_FIXTURE':
            if key in env and env[key] != value:
                raise ValueError('foreign SQL private path')
        elif env.get(key) != value:
            raise ValueError('missing or foreign fixture selection')
    if any(k.startswith('LUOBO_') and k not in selected for k in env):
        raise ValueError('unexpected business fixture environment')
    return selected


def private_fixture_sha(path, expected):
    if path != expected or path.resolve() != path:
        raise ValueError('foreign private fixture path')
    info, parent = path.lstat(), path.parent.lstat()
    if (not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600
            or info.st_uid != os.getuid() or info.st_nlink != 1 or info.st_size > 4096
            or not stat.S_ISDIR(parent.st_mode) or stat.S_IMODE(parent.st_mode) != 0o700
            or parent.st_uid != os.getuid()):
        raise ValueError('private fixture ownership differs')
    with path.open('rb') as source:
        opened = os.fstat(source.fileno())
        if (opened.st_dev, opened.st_ino) != (info.st_dev, info.st_ino):
            raise ValueError('private fixture identity changed')
        raw = source.read(4097)
    if len(raw) != info.st_size:
        raise ValueError('private fixture length changed')
    return sha(raw)  # Never return, log or upload the credential document.


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
    work = temporary / ('group-new-seed-' + os.environ['GITHUB_RUN_ID'] + '-' + os.environ['GITHUB_RUN_ATTEMPT'])
    if not temporary.is_absolute() or temporary.resolve() != temporary or work.exists():
        raise ValueError('fresh canonical work required')
    fs = os.statvfs(temporary)
    if fs.f_bavail * fs.f_frsize < 4 * 1024**3:
        raise ValueError('4GiB bootstrap floor; no child started')
    work.mkdir(mode=0o700)
    output = REPO / 'ci-results/group-new-seed'
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
    for key, folder in [('GOMODCACHE', 'modcache'), ('GOCACHE', 'buildcache'), ('GOPATH', 'gopath'), ('TMPDIR', 'tmp')]:
        p = work / folder
        p.mkdir(mode=0o700)
        env[key] = str(p)
    env['GOTMPDIR'] = env['TMPDIR']
    expected_locks = {p: bodies[p] for p in ('server/go.mod', 'server/go.sum')}
    write(output / 'source.json', {'scope': SCOPE, 'commit': os.environ['GITHUB_SHA'], 'inputsSHA256': sha(regular(INPUTS)),
          'parent1165Digest': c['parentDigest'], 'effectiveCount': 1170, 'effectiveDigest': c['effectiveDigest'],
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
    _, code = command('mysql-ready', [sys.executable, '-B', str(REPO / 'ci/group-ordinary/mysql_fixture.py'), 'check'], 60)
    if code != 0:
        raise ValueError('owned SQL readiness rejected before Go test dispatch')
    private = Path(bindings['LUOBO_RISK_TEST_MYSQL_FIXTURE'])
    private_sha = private_fixture_sha(private, private)
    ready = json.loads(regular(REPO / 'ci-results/group-ordinary-mysql/ready.json'))
    if (ready.get('owner') != os.environ['GITHUB_RUN_ID'] + ':' + os.environ['GITHUB_RUN_ATTEMPT']
            or ready.get('commit') != os.environ['GITHUB_SHA'] or ready.get('UUIDAndClaimConfirmed') is not True
            or ready.get('fixtureSHA256') != private_sha):
        raise ValueError('SQL private ready binding differs')
    write(output / 'fixture-admission.json', {'MongoReadyAndPrivateSQLCheckExit0': True, 'SQLPrivateSHA256': private_sha,
          'owner': ready['owner'], 'commit': ready['commit'], 'networkBoundary': 'fixed owned cloud loopback contracts; no kernel egress isolation claim'})
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
            raise ValueError('source/mode changed during seed target8 checks')
        post[target] = actual
    if regular(work / 'server/config/config.yaml') != template:
        raise ValueError('generated public config changed')
    verify()
    write(output / 'source-post.json', {'effectiveCount': len(post), 'effectiveDigest': digest(rows(post)),
          'allSourceBodiesAndModesExact': True, 'locksAfter': rows({p: post[p] for p in expected_locks}),
          'generatedPublicConfigSHA256': sha(template)})
    accepted = not errors and sum(x['topPASS'] for x in outcomes.values()) == 8 and sum(x['subPASS'] for x in outcomes.values()) == 21
    write(output / 'summary.json', {'state': 'new_group_seed_target8_passed' if accepted else 'failed_or_incomplete', 'scope': SCOPE,
          'packages': outcomes, 'wholeRegression': False, 'vetRun': False, 'runtimeSwitch': False,
          'notDeployable': True, 'unclosed': ['historical inventory importer', 'future allocator/direct namespace admission', 'persisted positive n_ reader'],
          'fixtureCollectAndCleanup': 'mandatory subsequent workflow steps; this summary alone is not overall acceptance',
          'sourceDigest': c['effectiveDigest'], 'unchangedPriorCIDResultsReused': True,
          'newExecutedExpected': {'topPASS': 8, 'subPASS': 21}, 'priorCIDTargetsReused': REUSED_PRIOR_TARGETS,
          'networkBoundary': 'cleanenv and fixed owned loopback fixture; not the empty pure13 network namespace'})
    return 0 if accepted else 1


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--verify', action='store_true')
    args = parser.parse_args()
    try:
        if args.verify:
            verify()
            print('Exact new-group seed source1170/target8 verified; no Go/fixture execution.')
        else:
            raise SystemExit(run())
    except (ValueError, KeyError, TypeError, OSError, subprocess.SubprocessError, json.JSONDecodeError):
        print('New-group seed targeted validation incomplete; original phase evidence retained.', file=sys.stderr)
        raise SystemExit(2)
