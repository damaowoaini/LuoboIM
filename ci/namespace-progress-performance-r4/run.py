#!/usr/bin/env python3
"""Independent R4 source1203 / exact Progress11/49; parent verification is source-only."""
import argparse
from collections import Counter
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import time

REPO = Path(__file__).resolve().parents[2]
INPUTS = Path(__file__).with_name('inputs.json')
SCOPE = 'namespace_progress_performance_r4_1203_exact11_49_privateFS_BSON_noDB_no_runtime'
PREFIX = 'github.com/openimsdk/open-im-server/v3/'
PACKAGE = 'internal/namespaceinventory'
PARENT_PATH = 'ci/namespace-progress-performance-r3/run.py'
PARENT_INPUTS = 'ci/namespace-progress-performance-r3/inputs.json'
PARENT_RUNNER_SHA = '9122af3be2a27be5154e2319d5afaf8fc857121ee1bd85efbfca5283824e42a4'
PARENT_INPUTS_SHA = '4997ecf9a03b1e11d6a873b5f387b0628830ab7c27d4eb8922b07bfa057851a0'
PARENT_DIGEST = '5ccbdf4279df6043396f5445cf2a157e2dbfa67c49e32076c620dbf5c8cd60e5'
FINAL_DIGEST = '2134b7ddbb9202f85878d665ac4f44ca3566d757fe51b2941d9f48796af42465'
DIGEST_ALGORITHM = 'ascending UTF8(path)+NUL+ASCII SHA256+LF then SHA256'
SOURCE_MANIFEST = 'e5b36b54cde4c057777d883de9722031b426128bbb5521339712450c644932b0'
HISTORY = {
    'parent1201SourceOnly': True,
    'oldRun': 38990944134,
    'oldResult': '3top6sub passed then original10001 hit package600s timeout; not a performance pass',
    'priorPassedButNowAffectedTops': [
        'TestNamespaceProgressContinuesAcrossEveryStage',
        'TestNamespaceProgressImportsOldPrefixWithoutRescanning',
        'TestNamespaceProgressRejectsUnboundDerivedState'],
    'priorNeverStartedTops': [
        'TestNamespaceProgressRowSelectionPreservesEvidence',
        'TestNamespaceProgressFastJSONPreservesOriginalStrictness',
        'TestNamespaceProgressMemoKeepsCustody'],
    'priorLarge': 'started and timed out; must run unchanged10001/600s',
    'R4GoTestsExecutedAtPreparation': 0,
}
ORIGIN = {'sourceManifestSHA256': SOURCE_MANIFEST, 'gofmtFiles': 5,
          'gofmtBytesUnchanged': True, 'GoTestsAtPreparation': 0,
          'lineage': 'immutable R3.verify source1201 -> exactly3modified2new -> independent1203',
          'original7top43subBodiesPreserved': True, 'original10001_and_600s_preserved': True}
SOURCE_ROOT = 'ci/namespace-progress-performance-r4/source/'
MODIFIED = frozenset('server/internal/namespaceinventory/' + part for part in
                     ['progress_capture.go', 'progress_index.go', 'progress_journal.go'])
NEW = frozenset('server/internal/namespaceinventory/' + part for part in
                ['progress_batch.go', 'progress_batch_test.go'])
OVERLAY_TARGETS = MODIFIED | NEW
DEPENDENCY_PATHS = frozenset([
    PARENT_PATH, PARENT_INPUTS,
    'ci/namespace-progress-performance-r4/run.py', 'ci/namespace-progress-performance-r4/test_run.py',
    '.github/workflows/namespace-progress-performance-r4-targeted.yml'])
ZERO_FLAGS = {'LUOBO_EMAIL_INTEGRATION': '0', 'LUOBO_RUN_LEGACY_LOAD_TESTS': '0',
              'LUOBO_RUN_LEGACY_EXAMPLES': '0'}
NEW_TOPS = [
    'TestNamespaceProgressCheckpointBatchPreservesBudgetAndChain',
    'TestNamespaceProgressBatchKeepsUnpublishedStateUntrusted',
    'TestNamespaceProgressCheckpointReadsLegacyAndRejectsWorkGap',
    'TestNamespaceProgressBatchRechecksIdentityBeforePublishing']
NEW_CASES = {
    NEW_TOPS[1]: ['discard', 'bounded-spill', 'blocked-private-object', 'CID-origin-closure'],
    NEW_TOPS[2]: ['legacy-V2', 'oversized-logical-gap'],
}
_PARENT = None


def parent():
    """Import immutable R3 definitions, never its CLI or execution method."""
    global _PARENT
    if _PARENT is None:
        path = REPO / PARENT_PATH
        if path.resolve() != path or path.is_symlink() or not path.is_file():
            raise ValueError('canonical frozen1201 runner required')
        if sha(path.read_bytes()) != PARENT_RUNNER_SHA or stat.S_IMODE(path.stat().st_mode) != 0o644:
            raise ValueError('frozen1201 runner differs')
        spec = importlib.util.spec_from_file_location('source_only_frozen_namespace1201', path)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        _PARENT = module
    return _PARENT


def expected():
    previous = parent().expected()
    return {'tops': sorted(previous['tops'] + NEW_TOPS),
            'subs': sorted(previous['subs'] + [top + '/' + sub for top, subs in NEW_CASES.items() for sub in subs])}


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def regular(path):
    return parent().regular(path)


def rows(bodies):
    return parent().rows(bodies)


def digest(items):
    # Keep the original R4 source-manifest identity. The immutable R3 parent
    # retains its own NUL-terminated algorithm and verifies itself separately.
    return sha(b''.join(row['path'].encode() + b'\0' + row['sha256'].encode() + b'\n'
                        for row in sorted(items, key=lambda row: row['path'])))


def apply_overlay(c, previous, root):
    core = parent().legacy()
    result, seen = dict(previous), set()
    if len(c['overlay']) != 5:
        raise ValueError('exact five R4 sources required')
    for row in c['overlay']:
        target = core.name(row['target'])
        before = previous.get(target)
        if (target not in OVERLAY_TARGETS or target in seen
                or (before is not None) != (target in MODIFIED)
                or row['beforeSHA256'] != (sha(before) if before is not None else None)
                or row['path'] != SOURCE_ROOT + target or row['mode'] != '0o644'):
            raise ValueError('exact three-modified/two-new1201-before overlay required')
        seen.add(target)
        result[target] = core.pinned(row, root)
    source = root / SOURCE_ROOT
    actual = {str(path.relative_to(root)) for path in source.rglob('*') if path.is_file() or path.is_symlink()}
    if seen != OVERLAY_TARGETS or actual != {row['path'] for row in c['overlay']}:
        raise ValueError('extra or missing R4 source')
    return result


def verify():
    previous = parent()
    core = previous.legacy()
    c = json.loads(regular(INPUTS))
    selected = expected()
    if (c['schema'] != 1 or c['scope'] != SCOPE or c['parentCount'] != 1201
            or c['parentDigest'] != PARENT_DIGEST or c['effectiveCount'] != 1203
            or c['effectiveDigest'] != FINAL_DIGEST
            or c['effectiveDigestAlgorithm'] != DIGEST_ALGORITHM
            or c['parentRunner']['path'] != PARENT_PATH or c['parentRunner']['sha256'] != PARENT_RUNNER_SHA
            or c['parentInputs']['path'] != PARENT_INPUTS or c['parentInputs']['sha256'] != PARENT_INPUTS_SHA
            or c['targets'] != {PACKAGE: selected} or c['history'] != HISTORY or c['origin'] != ORIGIN
            or c['expected'] != {'topPASS': 11, 'subPASS': 49, 'skip': 0, 'fail': 0, 'packages': 1,
                                 'vet': False, 'fullRegression': False, 'databaseFixture': False}
            or len(c['dependencies']) != len(DEPENDENCY_PATHS)
            or {row['path'] for row in c['dependencies']} != DEPENDENCY_PATHS):
        raise ValueError('exact1201-to1203 R4 / Progress11/49 contract required')
    for row in c['dependencies']:
        core.pinned(row, REPO)
    core.pinned(c['parentRunner'], REPO)
    core.pinned(c['parentInputs'], REPO)
    prior, before = previous.verify()  # Source-only; never any parent execution.
    if len(before) != 1201 or prior['effectiveDigest'] != PARENT_DIGEST:
        raise ValueError('exact original1201 source-only parent required')
    bodies = apply_overlay(c, before, REPO)
    modes = dict(prior['sourceModes'])
    modes.update({row['target']: 0o644 for row in c['overlay']})
    if (len(bodies) != 1203 or rows(bodies) != c['effectiveSource']
            or digest(rows(bodies)) != FINAL_DIGEST or modes != c['sourceModes']):
        raise ValueError('source1203 bodies/modes/digest differ')
    if any(bodies[path] != body for path, body in before.items() if path.endswith('_test.go')):
        raise ValueError('all original7/43 tests including10001 must remain exact')
    for path in ('server/go.mod', 'server/go.sum', 'server/config/config.example.yaml'):
        if bodies[path] != before[path]:
            raise ValueError('original locks/public configuration must remain exact')
    new_test = bodies['server/internal/namespaceinventory/progress_batch_test.go']
    if Counter(x.decode() for x in re.findall(rb'^func (Test\w+)\(', new_test, re.M)) != Counter(NEW_TOPS):
        raise ValueError('four exact checkpoint-batch test tops required')
    loops = re.findall(rb'for _, kind := range \[\]string\{([^}]+)\}', new_test)
    cases = [re.findall(rb'"([^"\n]+)"', loop) for loop in loops]
    if cases != [[s.encode() for s in subs] for subs in NEW_CASES.values()]:
        raise ValueError('all six exact checkpoint-batch subcases required')
    return c, bodies


def audit(events, code, package, selected):
    if package != PACKAGE or selected != expected():
        raise ValueError('only exact Progress11/49 package allowed')
    # R3.audit is intentionally fixed7/43. Reuse the unchanged1199 event parser,
    # after this successor has required the complete11/49 activation.
    return parent().legacy().audit(events, code, package, selected)


def require_guard(record, code):
    parent().require_guard(record, code)


def hosted_environment(env):
    parent().hosted_environment(env)


def test_environment(base, work, goroot):
    return parent().test_environment(base, work, goroot)


def isolation_command(env, uid, gid):
    return parent().isolation_command(env, uid, gid)


def write(path, value):
    parent().write(path, value)


# The execution body below is derived narrowly from the fixed parent run().
# It runs only the new activation, never calls the parent's execution method.

def run():
    started = time.monotonic()
    hosted_environment(os.environ)
    if os.getuid() == 0:
        raise ValueError('nonroot test owner required')
    c, bodies = verify()
    selected = expected()
    temporary = Path(os.environ['RUNNER_TEMP'])
    work = temporary / ('namespace-progress-performance-r4-' + os.environ['GITHUB_RUN_ID'] + '-' + os.environ['GITHUB_RUN_ATTEMPT'])
    if not temporary.is_absolute() or temporary.resolve() != temporary or work.exists():
        raise ValueError('fresh canonical work required')
    fs = os.statvfs(temporary)
    if fs.f_bavail * fs.f_frsize < 4 * 1024**3:
        raise ValueError('4GiB bootstrap floor; no Go child started')
    work.mkdir(mode=0o700)
    output = REPO / 'ci-results/namespace-progress-performance-r4'
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
    previous1199_digest = parent().PARENT_DIGEST
    write(output / 'source.json', {'scope': SCOPE, 'commit': os.environ['GITHUB_SHA'], 'inputsSHA256': sha(regular(INPUTS)),
          'parent1201Digest': PARENT_DIGEST, 'parent1199Digest': previous1199_digest, 'parentCorrected1184Digest': 'c94e475afef6c437ca913a4691fe6d84e694984e5dc503a6ac7f7b33bc1b36d0',
          'effectiveCount': 1203, 'effectiveDigest': FINAL_DIGEST, 'goVersion': version, 'targets': {PACKAGE: selected},
          'locksBefore': rows(locks), 'generatedPublicConfigSHA256': sha(template), 'noRuntimeAdoption': True,
          'DBFixtureSelected': False, 'newTop': 4, 'originalRequiredTop': 7, 'history': c['history']})

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
    expression = '^(' + '|'.join(selected['tops']) + ')$'
    argv = isolation + ['go', 'test', '-mod=readonly', '-p=1', '-parallel=1', '-race', '-json', '-count=1',
                        '-vet=off', '-timeout=600s', '-run', expression, './' + PACKAGE]
    folder, code = command('internal_namespaceinventory', argv, 900)
    try:
        events = [json.loads(line) for line in regular(folder / 'stdout.jsonl').decode().splitlines() if line.strip()]
        outcome = audit(events, code, PACKAGE, selected)
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
    write(output / 'summary.json', {'state': 'pure_progress_r4_11_49_passed' if accepted else 'failed_or_incomplete',
          'scope': SCOPE, 'packages': {PACKAGE: outcome}, 'effectiveCount': 1203, 'sourceDigest': FINAL_DIGEST,
          'currentExecutionExpected': {'topPASS': 11, 'subPASS': 49}, 'historicalResultsOnly': c['history'],
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
            print('Exact immutable1201 + R4 overlay5 =1203 / target11/49; no Go/DB execution.')
        else:
            raise SystemExit(run())
    except (ValueError, KeyError, TypeError, OSError, subprocess.SubprocessError, json.JSONDecodeError):
        print('R4 performance validation incomplete; original raw retained.', file=sys.stderr)
        raise SystemExit(2)
