#!/usr/bin/env python3
"""R5 source1204: exact short6/20, then only unchanged Large10001/600s."""
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
SCOPE = 'namespace_progress_performance_r5_1204_short6_20_then_large1_privateFS_BSON_noDB_no_runtime'
PREFIX = 'github.com/openimsdk/open-im-server/v3/'
PACKAGE = 'internal/namespaceinventory'
PARENT_PATH = 'ci/namespace-progress-performance-r4/run.py'
PARENT_INPUTS = 'ci/namespace-progress-performance-r4/inputs.json'
PARENT_RUNNER_SHA = '0e740711a364b5ff14f52c815e966df4d3d1320f9cb48f19b69380f51f8c8275'
PARENT_INPUTS_SHA = '50ec67c768015dd9754088bd0418cd649108f6a118375eddae3452dbc7e06d28'
PARENT_DIGEST = '2134b7ddbb9202f85878d665ac4f44ca3566d757fe51b2941d9f48796af42465'
FINAL_DIGEST = '5c1e62df1876f797228f36a260fb3e1c09a0c4a073af7b98b1faaa367370ccb8'
DIGEST_ALGORITHM = 'ascending UTF8(path)+NUL+ASCII SHA256+LF then SHA256'
SOURCE_MANIFEST = '061e5339513ebae41a92766528109585efa3173d4970597b7b9136a133531a40'
HISTORY = {'parent1203SourceOnly': True, 'oldRun': 38997758491,
           'oldActual': 'new4/6 and affectedold3/6 passed; Large package600.019s timeout; remaining3top37sub not started',
           'oldCheckpoint4_6': 'historical R4 evidence only; unchanged checkpoint logic, not an R5 run',
           'unselectedOriginal': 'strictJSON26 and classifier3 are not fresh passes here',
           'R5GoTestsAtPreparation': 0}
ORIGIN = {'sourceManifestSHA256': SOURCE_MANIFEST, 'GoFormatAtPreparation': 0,
          'rootGofmtBeforeAdoptionRequired': True, 'GoTestsAtPreparation': 0,
          'lineage': 'immutable R4.verify source1203 -> exactly2modified1new -> source1204',
          'original11top49subBodiesPreserved': True, 'original10001_and_600s_preserved': True}
SOURCE_ROOT = 'ci/namespace-progress-performance-r5/source/'
MODIFIED = frozenset('server/internal/namespaceinventory/' + n for n in
                     ['progress_capture.go', 'progress_journal.go'])
NEW = frozenset(['server/internal/namespaceinventory/progress_metadata_test.go'])
OVERLAY_TARGETS = MODIFIED | NEW
DEPENDENCY_PATHS = frozenset([PARENT_PATH, PARENT_INPUTS,
    'ci/namespace-progress-performance-r5/run.py', 'ci/namespace-progress-performance-r5/test_run.py',
    '.github/workflows/namespace-progress-performance-r5-targeted.yml'])
NEW_TOPS = ['TestNamespaceProgressMetadataLookupOncePerReference',
            'TestNamespaceProgressResolvedMetadataRetainsBindings']
NEW_CASES = {NEW_TOPS[0]: ['checked-row', 'summary-seen'],
             NEW_TOPS[1]: ['pass-and-metaSHA', 'number-and-row', 'legacy-checkpoint', 'private-page-and-row']}
OLD_SHORT = ['TestNamespaceProgressContinuesAcrossEveryStage',
             'TestNamespaceProgressImportsOldPrefixWithoutRescanning',
             'TestNamespaceProgressRejectsUnboundDerivedState',
             'TestNamespaceProgressMemoKeepsCustody']
LARGE = 'TestNamespaceProgressLargeHistoryHasNoFinalFullSweep'
_PARENT = None


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def parent():
    global _PARENT
    if _PARENT is None:
        path = REPO / PARENT_PATH
        if (path.resolve() != path or path.is_symlink() or not path.is_file()
                or sha(path.read_bytes()) != PARENT_RUNNER_SHA or stat.S_IMODE(path.stat().st_mode) != 0o644):
            raise ValueError('exact canonical frozen R4 runner required')
        spec = importlib.util.spec_from_file_location('source_only_frozen_namespace1203', path)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        _PARENT = module
    return _PARENT


def core():
    return parent().parent().legacy()


def expected():
    old = parent().expected()
    short = {'tops': sorted(OLD_SHORT + NEW_TOPS),
             'subs': sorted([s for s in old['subs'] if s.split('/')[0] in OLD_SHORT]
                            + [top + '/' + s for top, subs in NEW_CASES.items() for s in subs])}
    return {'short': short, 'large': {'tops': [LARGE], 'subs': []}}


def regular(path):
    return parent().regular(path)


def rows(bodies):
    return parent().rows(bodies)


def digest(items):
    return parent().digest(items)


def apply_overlay(c, previous, root):
    result, seen = dict(previous), set()
    if len(c['overlay']) != 3:
        raise ValueError('exact two-modified/one-new R5 overlay required')
    for row in c['overlay']:
        target = core().name(row['target'])
        before = previous.get(target)
        if (target not in OVERLAY_TARGETS or target in seen
                or (before is not None) != (target in MODIFIED)
                or row['beforeSHA256'] != (sha(before) if before is not None else None)
                or row['path'] != SOURCE_ROOT + target or row['mode'] != '0o644'):
            raise ValueError('exact1203-before R5 sources required')
        seen.add(target)
        result[target] = core().pinned(row, root)
    source = root / SOURCE_ROOT
    actual = {str(p.relative_to(root)) for p in source.rglob('*') if p.is_file() or p.is_symlink()}
    if seen != OVERLAY_TARGETS or actual != {r['path'] for r in c['overlay']}:
        raise ValueError('extra or missing R5 source')
    return result


def verify():
    c = json.loads(regular(INPUTS))
    if (c['schema'] != 1 or c['scope'] != SCOPE or c['parentCount'] != 1203
            or c['parentDigest'] != PARENT_DIGEST or c['effectiveCount'] != 1204
            or c['effectiveDigest'] != FINAL_DIGEST or c['effectiveDigestAlgorithm'] != DIGEST_ALGORITHM
            or c['sourceModeSource'] != 'verified parent1203 modes plus exact overlay3 mode0644'
            or c['parentRunner']['path'] != PARENT_PATH or c['parentRunner']['sha256'] != PARENT_RUNNER_SHA
            or c['parentInputs']['path'] != PARENT_INPUTS or c['parentInputs']['sha256'] != PARENT_INPUTS_SHA
            or c['phases'] != expected() or c['preservedOriginalTargets'] != parent().expected()
            or c['history'] != HISTORY or c['origin'] != ORIGIN
            or c['expected'] != {'short': {'topPASS': 6, 'subPASS': 20}, 'large': {'topPASS': 1, 'subPASS': 0},
                                 'skip': 0, 'fail': 0, 'vet': False, 'fullRegression': False, 'databaseFixture': False}
            or len(c['dependencies']) != len(DEPENDENCY_PATHS)
            or {r['path'] for r in c['dependencies']} != DEPENDENCY_PATHS):
        raise ValueError('exact R5 short6/20 then Large1 source1204 contract required')
    for row in c['dependencies'] + [c['parentRunner'], c['parentInputs']]:
        core().pinned(row, REPO)
    prior, before = parent().verify()  # Source only; no parent run/CLI/Go.
    if len(before) != 1203 or prior['effectiveDigest'] != PARENT_DIGEST:
        raise ValueError('frozen1203 parent differs')
    bodies = apply_overlay(c, before, REPO)
    modes = dict(prior['sourceModes'])
    modes.update({r['target']: 0o644 for r in c['overlay']})
    if len(bodies) != 1204 or digest(rows(bodies)) != FINAL_DIGEST or set(modes) != set(bodies):
        raise ValueError('source1204 bodies/modes/digest differ')
    if any(bodies[p] != raw for p, raw in before.items() if p.endswith('_test.go')):
        raise ValueError('all original11/49 test bodies including10001 must remain exact')
    for p in ('server/go.mod', 'server/go.sum', 'server/config/config.example.yaml'):
        if bodies[p] != before[p]:
            raise ValueError('original source locks/configuration differ')
    test = bodies['server/internal/namespaceinventory/progress_metadata_test.go']
    if Counter(x.decode() for x in re.findall(rb'^func (Test\w+)\(', test, re.M)) != Counter(NEW_TOPS):
        raise ValueError('exact two R5 metadata test tops required')
    loops = re.findall(rb'for _, kind := range \[\]string\{([^}]+)\}', test)
    if [re.findall(rb'"([^"\n]+)"', x) for x in loops] != [[s.encode() for s in v] for v in NEW_CASES.values()]:
        raise ValueError('exact six R5 metadata subcases required')
    return dict(c, sourceModes=modes), bodies


def audit(events, code, label):
    if label not in expected():
        raise ValueError('only fixed short or Large phase allowed')
    return core().audit(events, code, PACKAGE, expected()[label])


def check_materialized(c, bodies, work, template):
    post = {}
    for target, body in bodies.items():
        actual = regular(work / target)
        if actual != body or stat.S_IMODE((work / target).stat().st_mode) != c['sourceModes'][target]:
            raise ValueError('source/mode changed during R5 performance phases')
        post[target] = actual
    if regular(work / 'server/config/config.yaml') != template:
        raise ValueError('generated public config changed')
    return post


def execute_phases(command, isolation, post):
    outcomes = {}
    for label, selected in expected().items():
        expression = '^(' + '|'.join(selected['tops']) + ')$'
        argv = isolation + ['go', 'test', '-mod=readonly', '-p=1', '-parallel=1', '-race', '-json', '-count=1',
                            '-vet=off', '-timeout=600s', '-run', expression, './' + PACKAGE]
        try:
            folder, code = command(label, argv, 900)
            try:
                events = [json.loads(x) for x in regular(folder / 'stdout.jsonl').decode().splitlines() if x.strip()]
                outcome = {'accepted': True, **audit(events, code, label)}
            except (ValueError, UnicodeError, TypeError, KeyError):
                outcome = {'accepted': False, 'exit': code, 'rawRetained': True}
            parent().write(folder / 'summary.json', outcome)
            outcomes[label] = outcome
        finally:
            post(label)  # Also recheck source after failure/timeout; never enter Large on failure.
        if not outcome['accepted']:
            break
    return outcomes


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


def run():
    started = time.monotonic()
    hosted_environment(os.environ)
    if os.getuid() == 0:
        raise ValueError('nonroot test owner required')
    c, bodies = verify()
    selected = expected()
    temporary = Path(os.environ['RUNNER_TEMP'])
    work = temporary / ('namespace-progress-performance-r5-' + os.environ['GITHUB_RUN_ID'] + '-' + os.environ['GITHUB_RUN_ATTEMPT'])
    if not temporary.is_absolute() or temporary.resolve() != temporary or work.exists():
        raise ValueError('fresh canonical work required')
    fs = os.statvfs(temporary)
    if fs.f_bavail * fs.f_frsize < 4 * 1024**3:
        raise ValueError('4GiB bootstrap floor; no Go child started')
    work.mkdir(mode=0o700)
    output = REPO / 'ci-results/namespace-progress-performance-r5'
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
          'parent1203Digest': PARENT_DIGEST, 'parent1201Digest': parent().PARENT_DIGEST,
          'effectiveCount': 1204, 'effectiveDigest': FINAL_DIGEST, 'goVersion': version,
          'phases': selected, 'locksBefore': rows(locks), 'generatedPublicConfigSHA256': sha(template),
          'noRuntimeAdoption': True, 'DBFixtureSelected': False, 'newTop': 2, 'directAffectedOldTop': 4,
          'originalLarge10001_600s': True, 'history': c['history']})

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
    def post(label):
        post_bodies = check_materialized(c, bodies, work, template)
        verify()
        write(output / ('source-post-' + label + '.json'), {
              'effectiveCount': len(post_bodies), 'effectiveDigest': digest(rows(post_bodies)),
              'allSourceBodiesAndModesExact': True,
              'locksAfter': rows({p: post_bodies[p] for p in locks}),
              'generatedPublicConfigSHA256': sha(template)})

    outcomes = execute_phases(command, isolation, post)
    accepted = list(outcomes) == ['short', 'large'] and all(x['accepted'] for x in outcomes.values())
    write(output / 'summary.json', {'state': 'r5_short6_20_and_original_large_passed' if accepted else 'failed_or_incomplete',
          'scope': SCOPE, 'phases': outcomes, 'effectiveCount': 1204, 'sourceDigest': FINAL_DIGEST,
          'currentExecutionExpected': c['expected'], 'historicalResultsOnly': c['history'],
          'fullOriginal11_49Rerun': False, 'wholeRegression': False, 'vetRun': False,
          'runtimeSwitch': False, 'notDeployable': True, 'databaseVendorCallbackFixture': False,
          'networkBoundary': 'each phase in fresh empty network namespace with original nonroot UID/GID',
          'workRetainedForEphemeralRunnerCleanup': True,
          'fixtureCleanup': 'not applicable; no service or container started',
          'inventoryStateAlwaysUntrustedIncomplete': True})
    return 0 if accepted else 1


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--verify', action='store_true')
    args = parser.parse_args()
    try:
        if args.verify:
            verify()
            print('Exact immutable1203 + R5 overlay3 =1204; short6/20 then Large1; no Go/DB execution.')
        else:
            raise SystemExit(run())
    except (ValueError, KeyError, TypeError, OSError, subprocess.SubprocessError, json.JSONDecodeError):
        print('R5 performance validation incomplete; original raw retained.', file=sys.stderr)
        raise SystemExit(2)
