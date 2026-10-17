#!/usr/bin/env python3
"""Exact Progress6/17 only; synthetic private FS/BSON, no DB/full/runtime."""
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
SCOPE = 'namespace_progress_performance1199_exact6_17_privateFS_BSON_noDB_no_runtime'
PREFIX = 'github.com/openimsdk/open-im-server/v3/'
PACKAGE = 'internal/namespaceinventory'
EXPECTED = {'subs': ['TestNamespaceProgressMemoKeepsCustody/bounded-eviction',
          'TestNamespaceProgressMemoKeepsCustody/copy-isolation',
          'TestNamespaceProgressMemoKeepsCustody/foreign-reference',
          'TestNamespaceProgressMemoKeepsCustody/invocation-local',
          'TestNamespaceProgressMemoKeepsCustody/limits',
          'TestNamespaceProgressMemoKeepsCustody/private-mode',
          'TestNamespaceProgressMemoKeepsCustody/raw-tamper',
          'TestNamespaceProgressMemoKeepsCustody/strict-redecode',
          'TestNamespaceProgressRejectsUnboundDerivedState/cancelled',
          'TestNamespaceProgressRejectsUnboundDerivedState/index-tamper',
          'TestNamespaceProgressRejectsUnboundDerivedState/partial-head',
          'TestNamespaceProgressRejectsUnboundDerivedState/raw-tamper',
          'TestNamespaceProgressRejectsUnboundDerivedState/restart',
          'TestNamespaceProgressRejectsUnboundDerivedState/valid-hash-foreign-reference',
          'TestNamespaceProgressRowSelectionPreservesEvidence/invalid-position',
          'TestNamespaceProgressRowSelectionPreservesEvidence/mongo-erased-and-ambiguous',
          'TestNamespaceProgressRowSelectionPreservesEvidence/redis-head-and-reasons'],
 'tops': ['TestNamespaceProgressContinuesAcrossEveryStage',
          'TestNamespaceProgressImportsOldPrefixWithoutRescanning',
          'TestNamespaceProgressLargeHistoryHasNoFinalFullSweep',
          'TestNamespaceProgressMemoKeepsCustody',
          'TestNamespaceProgressRejectsUnboundDerivedState',
          'TestNamespaceProgressRowSelectionPreservesEvidence']}
REUSED = {'fullRegression': False,
 'head': 'bdffc675e6ec0f193bcb5cf786859a7746869061',
 'job': 117248827341,
 'newExecutionExpected': {'subPASS': 17, 'topPASS': 6},
 'notRetainedAsNewExecution': 'Previously accepted unchanged Chunk7/22 plus CLI3/8 only. Three successful '
                              'affected Progress tops/6sub are deliberately selected anew; Large previously '
                              'timed out. Historical metadata is not new execution.',
 'passNamesByPackage': {'cmd/tools/group-namespace-capture': ['TestNamespaceCaptureCLIChunksRequireExplicitModes',
                                                              'TestNamespaceCaptureCLIChunksRequireExplicitModes/default-inspect-does-not-open-resume',
                                                              'TestNamespaceCaptureCLIChunksRequireExplicitModes/no-implicit-connection-or-activation',
                                                              'TestNamespaceCaptureCLIChunksRequireExplicitModes/offline-explicit-inspect',
                                                              'TestNamespaceCaptureCLIInspectAndSafeErrors',
                                                              'TestNamespaceCaptureCLIInspectAndSafeErrors/default-inspect-no-connection',
                                                              'TestNamespaceCaptureCLIInspectAndSafeErrors/no-activation-or-credential-flags',
                                                              'TestNamespaceCaptureCLIInspectAndSafeErrors/no-implicit-capture',
                                                              'TestNamespaceCaptureCLIResumableProgressIsExplicit',
                                                              'TestNamespaceCaptureCLIResumableProgressIsExplicit/explicit-input-no-implicit-resume-or-activation',
                                                              'TestNamespaceCaptureCLIResumableProgressIsExplicit/recorded-progress-not-fresh-full-custody'],
                        'internal/namespaceinventory': ['TestNamespaceChunkScanByteBudgetPreservesPendingTail',
                                                        'TestNamespaceChunkScanHintKeepsWholeBoundedReply',
                                                        'TestNamespaceChunkScanHintKeepsWholeBoundedReply/257-final-cursor',
                                                        'TestNamespaceChunkScanHintKeepsWholeBoundedReply/257-next-nonzero',
                                                        'TestNamespaceChunkScanHintKeepsWholeBoundedReply/maximum-pending',
                                                        'TestNamespaceChunkScanHintKeepsWholeBoundedReply/oversized-reply',
                                                        'TestNamespaceChunksCheckpointOwnershipAndLostReply',
                                                        'TestNamespaceChunksCheckpointOwnershipAndLostReply/cancelled',
                                                        'TestNamespaceChunksCheckpointOwnershipAndLostReply/locked',
                                                        'TestNamespaceChunksCheckpointOwnershipAndLostReply/lost-reply',
                                                        'TestNamespaceChunksCheckpointOwnershipAndLostReply/orphan-page',
                                                        'TestNamespaceChunksCheckpointOwnershipAndLostReply/partial-checkpoint',
                                                        'TestNamespaceChunksCheckpointOwnershipAndLostReply/public-mode',
                                                        'TestNamespaceChunksCheckpointOwnershipAndLostReply/tampered-page',
                                                        'TestNamespaceChunksExceedFormerAggregateRowLimit',
                                                        'TestNamespaceChunksOwnedPagingAndPrivateResume',
                                                        'TestNamespaceChunksOwnedPagingAndPrivateResume/bounded-progress-private-entry',
                                                        'TestNamespaceChunksOwnedPagingAndPrivateResume/heterogeneous-id-pages-and-real-private-entry',
                                                        'TestNamespaceChunksOwnedPagingAndPrivateResume/real-write-between-passes-is-rejected',
                                                        'TestNamespaceChunksRejectObservedDriftAndRestart',
                                                        'TestNamespaceChunksRejectObservedDriftAndRestart/binding-changed',
                                                        'TestNamespaceChunksRejectObservedDriftAndRestart/changed-redis',
                                                        'TestNamespaceChunksRejectObservedDriftAndRestart/changed-row',
                                                        'TestNamespaceChunksRejectObservedDriftAndRestart/missing-row',
                                                        'TestNamespaceChunksRejectObservedDriftAndRestart/mongo-restart',
                                                        'TestNamespaceChunksRejectObservedDriftAndRestart/new-row',
                                                        'TestNamespaceChunksRejectObservedDriftAndRestart/redis-restart',
                                                        'TestNamespaceChunksRejectObservedDriftAndRestart/ttl-refreshed',
                                                        'TestNamespaceChunksResumePreservesCrossPageEvidence']},
 'retainedSubPASS': 30,
 'retainedTopPASS': 10,
 'rootPartialReceiptSHA256': '4aa8cab1a8589a91264f99a5819d6d2e6a29c8b6514bf95563f74a46cd806036',
 'run': 38983276895,
 'runtime': False,
 'sourceCount': 1197,
 'sourceDigest': '3ff219f0f480e6ff30b7fc2f0d2d468c160fe0a96f43f632e59962404568efec'}
SOURCE_ROOT = 'ci/candidates/namespace-progress-performance/source/'
MODIFIED = frozenset(['server/internal/namespaceinventory/progress_capture.go',
                      'server/internal/namespaceinventory/progress_journal.go'])
NEW = frozenset(['server/internal/namespaceinventory/progress_page_memo.go',
                 'server/internal/namespaceinventory/progress_page_memo_test.go'])
OVERLAY_TARGETS = MODIFIED | NEW
DEPENDENCY_PATHS = frozenset([
    '.github/workflows/namespace-progress-performance-targeted.yml',
    'ci/namespace-progress-performance/run.py',
    'ci/namespace-progress-performance/test_run.py',
    'ci/namespace-chunk/run.py', 'ci/namespace-chunk/inputs.json',
    'ci/group-effects/capture.py', 'ci/group-effects/resource_guard.py', 'ci/secret_scan.py'])
PARENT_DIGEST = '3ff219f0f480e6ff30b7fc2f0d2d468c160fe0a96f43f632e59962404568efec'
FINAL_DIGEST = '362adde7b3b009e3c575ee18bb28f510c210cf3b9e24091cab7accfee4e76f12'
ZERO_FLAGS = {'LUOBO_EMAIL_INTEGRATION': '0', 'LUOBO_RUN_LEGACY_LOAD_TESTS': '0',
              'LUOBO_RUN_LEGACY_EXAMPLES': '0'}


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
    if len(c['overlay']) != 4:
        raise ValueError('exact four formatted Progress sources required')
    for row in c['overlay']:
        target = name(row['target'])
        before = previous.get(target)
        if (target not in OVERLAY_TARGETS or target in seen
                or (before is not None) != (target in MODIFIED)
                or row['beforeSHA256'] != (sha(before) if before is not None else None)
                or row['path'] != SOURCE_ROOT + target or row['mode'] != '0o644'):
            raise ValueError('exact two-modified/two-new parent-before overlay required')
        seen.add(target)
        result[target] = pinned(row, root)
    source = root / SOURCE_ROOT
    actual = {str(path.relative_to(root)) for path in source.rglob('*') if path.is_file() or path.is_symlink()}
    if seen != OVERLAY_TARGETS or actual != {row['path'] for row in c['overlay']}:
        raise ValueError('extra or missing performance source')
    return result


def verify():
    c = json.loads(regular(INPUTS))
    if (c['schema'] != 1 or c['scope'] != SCOPE or c['parentCount'] != 1197
            or c['parentDigest'] != PARENT_DIGEST or c['effectiveCount'] != 1199
            or c['effectiveDigest'] != FINAL_DIGEST
            or c['parentRunner']['path'] != 'ci/namespace-chunk/run.py'
            or c['parentInputs']['path'] != 'ci/namespace-chunk/inputs.json'
            or c['targets'] != {PACKAGE: EXPECTED} or c['reusePriorAcceptedTargets'] != REUSED
            or c['expected'] != {'topPASS': 6, 'subPASS': 17, 'skip': 0, 'fail': 0, 'packages': 1,
                                 'vet': False, 'fullRegression': False, 'databaseFixture': False}
            or c['origin']['formattedManifestSHA256'] != '57779bad5e01f60446162900bf67b3d56cfd8a189b68aa0b14856c1569a9dc26'
            or {row['path'] for row in c['dependencies']} != DEPENDENCY_PATHS
            or len(c['dependencies']) != len(DEPENDENCY_PATHS)):
        raise ValueError('exact c94-to1197-to1199 / pure Progress6/17 contract required')
    for row in c['dependencies']:
        pinned(row, REPO)
    pinned(c['parentRunner'], REPO)
    pinned(c['parentInputs'], REPO)
    spec = importlib.util.spec_from_file_location('source_only_frozen_namespace1197', REPO / c['parentRunner']['path'])
    parent = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(parent)
    prior, before = parent.verify()  # Only source verification: never parent.run(), old Go targets or fixture setup.
    if (len(before) != 1197 or prior['effectiveDigest'] != PARENT_DIGEST
            or prior['parentCount'] != 1184
            or prior['parentDigest'] != 'c94e475afef6c437ca913a4691fe6d84e694984e5dc503a6ac7f7b33bc1b36d0'):
        raise ValueError('exact fixture-corrected c94 lineage required; old47d forbidden')
    bodies = apply_overlay(c, before, REPO)
    modes = dict(prior['sourceModes'])
    modes.update({row['target']: 0o644 for row in c['overlay']})
    if (len(bodies) != 1199 or rows(bodies) != c['effectiveSource']
            or digest(rows(bodies)) != FINAL_DIGEST or modes != c['sourceModes']):
        raise ValueError('formatted1199 bodies/modes/digest differ')
    for path, body in before.items():
        if path.endswith('_test.go') and bodies[path] != body:
            raise ValueError('all previous test bodies including original10001 must remain exact')
    new_test = bodies['server/internal/namespaceinventory/progress_page_memo_test.go']
    new_names = re.findall(rb'^func (Test\w+)\(', new_test, re.M)
    if Counter(name.decode() for name in new_names) != Counter({
            'TestNamespaceProgressMemoKeepsCustody': 1,
            'TestNamespaceProgressRowSelectionPreservesEvidence': 1}):
        raise ValueError('exact two new test tops required')
    combined = bodies['server/internal/namespaceinventory/progress_capture_test.go'] + new_test
    if any(('func ' + test + '(').encode() not in combined for test in EXPECTED['tops']):
        raise ValueError('selected Progress top missing in source')
    if any(json.dumps(sub.split('/', 1)[1]).encode() not in combined for sub in EXPECTED['subs']):
        raise ValueError('selected fixed Progress subcase missing in source')
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


def hosted_environment(env):
    for key, value in {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux',
                       'GOTOOLCHAIN': 'local', 'GOWORK': 'off'}.items():
        if env.get(key) != value:
            raise ValueError('hosted pure environment required')
    if (not re.fullmatch('[0-9]+', env.get('GITHUB_RUN_ID', ''))
            or not re.fullmatch('[0-9]+', env.get('GITHUB_RUN_ATTEMPT', ''))
            or not re.fullmatch('[0-9a-f]{40}', env.get('GITHUB_SHA', ''))):
        raise ValueError('exact run/attempt/commit required')
    if any(env.get(key) != value for key, value in ZERO_FLAGS.items()):
        raise ValueError('legacy side-effect examples must remain disabled')
    if any(key.startswith('LUOBO_') and key not in ZERO_FLAGS for key in env):
        raise ValueError('no database/business fixture allowed in pure lane')


def test_environment(base, work, goroot):
    env = {'PATH': base['PATH'], 'GOROOT': goroot, 'GOTOOLCHAIN': 'local', 'GOWORK': 'off',
           'GOENV': 'off', 'CGO_ENABLED': '1', 'GOMAXPROCS': '2', 'GOMEMLIMIT': '900MiB',
           'GOPROXY': 'off', 'GOSUMDB': 'off', 'GOAUTH': 'off',
           'GOPRIVATE': '', 'GONOPROXY': '', 'GONOSUMDB': '', **ZERO_FLAGS}
    if 'HOME' in base:
        env['HOME'] = base['HOME']  # Actual original value only; never an owned replacement.
    for key, folder in [('GOMODCACHE', 'modcache'), ('GOCACHE', 'buildcache'), ('GOPATH', 'gopath'), ('TMPDIR', 'tmp')]:
        env[key] = str(work / folder)
    env['GOTMPDIR'] = env['TMPDIR']
    return env


def isolation_command(env, uid, gid):
    if uid <= 0 or gid < 0 or env.get('GOPROXY') != 'off' or env.get('GOSUMDB') != 'off':
        raise ValueError('nonroot offline test boundary required')
    return ['sudo', '-n', 'unshare', '--net', '--', 'setpriv', '--reuid=' + str(uid), '--regid=' + str(gid),
            '--clear-groups', '--', 'env', '-i'] + [key + '=' + value for key, value in env.items()]


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


def run():
    started = time.monotonic()
    hosted_environment(os.environ)
    if os.getuid() == 0:
        raise ValueError('nonroot test owner required')
    c, bodies = verify()
    temporary = Path(os.environ['RUNNER_TEMP'])
    work = temporary / ('namespace-progress-performance-' + os.environ['GITHUB_RUN_ID'] + '-' + os.environ['GITHUB_RUN_ATTEMPT'])
    if not temporary.is_absolute() or temporary.resolve() != temporary or work.exists():
        raise ValueError('fresh canonical work required')
    fs = os.statvfs(temporary)
    if fs.f_bavail * fs.f_frsize < 4 * 1024**3:
        raise ValueError('4GiB bootstrap floor; no Go child started')
    work.mkdir(mode=0o700)
    output = REPO / 'ci-results/namespace-progress-performance'
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
    env = test_environment(os.environ, work, goroot)
    locks = {path: bodies[path] for path in ['server/go.mod', 'server/go.sum']}
    write(output / 'source.json', {'scope': SCOPE, 'commit': os.environ['GITHUB_SHA'], 'inputsSHA256': sha(regular(INPUTS)),
          'parent1197Digest': PARENT_DIGEST, 'parentCorrected1184Digest': 'c94e475afef6c437ca913a4691fe6d84e694984e5dc503a6ac7f7b33bc1b36d0',
          'effectiveCount': 1199, 'effectiveDigest': FINAL_DIGEST, 'goVersion': version, 'targets': {PACKAGE: EXPECTED},
          'locksBefore': rows(locks), 'generatedPublicConfigSHA256': sha(template), 'noRuntimeAdoption': True,
          'DBFixtureSelected': False, 'newTop': 2, 'affectedTop': 4})

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

    clean = lambda values: ['env', '-i'] + [key + '=' + value for key, value in values.items()]
    download = dict(env, GOPROXY='https://proxy.golang.org', GOSUMDB='sum.golang.org')
    _, code = command('module-download', clean(download) + ['go', 'mod', 'download', '-json'], 600)
    if code != 0 or any(regular(work / path) != body for path, body in locks.items()):
        raise ValueError('public locked dependency preparation rejected')
    uid, gid = os.getuid(), os.getgid()
    host_net = os.stat('/proc/self/ns/net').st_ino
    isolation = isolation_command(env, uid, gid)
    probe = ('import json,os,sys;'
             'd={"nonroot":os.getuid()==int(sys.argv[1]) and os.geteuid()==int(sys.argv[1]) and os.getuid()!=0,'
             '"sameGID":os.getgid()==int(sys.argv[2]),'
             '"differentNetworkNamespace":os.stat("/proc/self/ns/net").st_ino!=int(sys.argv[3])};'
             'print(json.dumps(d));sys.exit(0 if all(d.values()) else 1)')
    folder, code = command('network-isolation', isolation + [sys.executable, '-c', probe, str(uid), str(gid), str(host_net)], 15)
    if code != 0 or json.loads(regular(folder / 'stdout.jsonl')) != {
            'nonroot': True, 'sameGID': True, 'differentNetworkNamespace': True}:
        raise ValueError('fresh nonroot network isolation unavailable; tests not started')
    expression = '^(' + '|'.join(EXPECTED['tops']) + ')$'
    argv = isolation + ['go', 'test', '-mod=readonly', '-p=1', '-parallel=1', '-race', '-json', '-count=1',
                        '-vet=off', '-timeout=600s', '-run', expression, './' + PACKAGE]
    folder, code = command('internal_namespaceinventory', argv, 900)
    try:
        events = [json.loads(line) for line in regular(folder / 'stdout.jsonl').decode().splitlines() if line.strip()]
        outcome = audit(events, code, PACKAGE, EXPECTED)
        accepted = True
    except (ValueError, UnicodeError, TypeError, KeyError):
        outcome = {'accepted': False, 'exit': code, 'rawRetained': True}
        accepted = False
    write(folder / 'summary.json', outcome)
    post = {}
    for target, body in bodies.items():
        actual = regular(work / target)
        if actual != body or stat.S_IMODE((work / target).stat().st_mode) != c['sourceModes'][target]:
            raise ValueError('source/mode changed during pure performance targets')
        post[target] = actual
    if regular(work / 'server/config/config.yaml') != template:
        raise ValueError('generated public config changed')
    verify()
    write(output / 'source-post.json', {'effectiveCount': len(post), 'effectiveDigest': digest(rows(post)),
          'allSourceBodiesAndModesExact': True, 'locksAfter': rows({path: post[path] for path in locks}),
          'generatedPublicConfigSHA256': sha(template)})
    write(output / 'summary.json', {'state': 'pure_progress6_17_passed' if accepted else 'failed_or_incomplete',
          'scope': SCOPE, 'packages': {PACKAGE: outcome}, 'effectiveCount': 1199, 'sourceDigest': FINAL_DIGEST,
          'currentExecutionExpected': {'topPASS': 6, 'subPASS': 17}, 'priorAcceptedResultsRetained': REUSED,
          'wholeRegression': False, 'vetRun': False, 'runtimeSwitch': False, 'notDeployable': True,
          'databaseVendorCallbackFixture': False, 'networkBoundary': 'tests in fresh empty network namespace with original nonroot UID/GID',
          'workRetainedForEphemeralRunnerCleanup': True, 'fixtureCleanup': 'not applicable; no service or container started',
          'inventoryStateAlwaysUntrustedIncomplete': True,
          'unclosed': ['trusted historical drain/lifecycle/importer/activation', 'lawful legacy alias compatibility', 'persisted positive n_ reader and final runtime activation']})
    return 0 if accepted else 1


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--verify', action='store_true')
    args = parser.parse_args()
    try:
        if args.verify:
            verify()
            print('Exact c94-to1197 + formatted Progress4 =1199 / targeted6/17 verified; no Go/DB execution.')
        else:
            raise SystemExit(run())
    except (ValueError, KeyError, TypeError, OSError, subprocess.SubprocessError, json.JSONDecodeError):
        print('Progress performance validation incomplete; original raw retained.', file=sys.stderr)
        raise SystemExit(2)
