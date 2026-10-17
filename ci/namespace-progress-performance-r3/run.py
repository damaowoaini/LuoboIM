#!/usr/bin/env python3
"""R3 source1201 / exact Progress7/43 only; never execute the parent lane."""
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
SCOPE = 'namespace_progress_performance_r3_1201_exact7_43_privateFS_BSON_noDB_no_runtime'
PREFIX = 'github.com/openimsdk/open-im-server/v3/'
PACKAGE = 'internal/namespaceinventory'
PARENT_PATH = 'ci/namespace-progress-performance/run.py'
PARENT_INPUTS = 'ci/namespace-progress-performance/inputs.json'
PARENT_RUNNER_SHA = 'bcd07002891dd8bf4973783ebb5e91494c2ac215867fca6e03484d8fb55482ec'
PARENT_INPUTS_SHA = '68a2504db41f29119cc0fa9f8e0d3603e400bde04c215d88965b1a92f5d9bb49'
PARENT_DIGEST = '362adde7b3b009e3c575ee18bb28f510c210cf3b9e24091cab7accfee4e76f12'
FINAL_DIGEST = '5ccbdf4279df6043396f5445cf2a157e2dbfa67c49e32076c620dbf5c8cd60e5'
FORMATTED_MANIFEST = '4f10a4c96cbb941e224133ef21dd2e49f0cfb6db8fb9070316fe80cd7d399901'
HISTORY = {'parent1199SourceOnly': True, 'oldRun': 38987537444,
           'oldResult': 'original600s package timeout; not a performance pass',
           'priorUnchangedChunkCLI': 'historical parent acceptance only; not selected here',
           'R3TestsExecutedAtPreparation': 0}
SOURCE_ROOT = 'ci/namespace-progress-performance-r3/source/'
MODIFIED = frozenset(['server/internal/namespaceinventory/progress_page_memo.go'])
NEW = frozenset(['server/internal/namespaceinventory/progress_json.go',
                 'server/internal/namespaceinventory/progress_json_test.go'])
OVERLAY_TARGETS = MODIFIED | NEW
DEPENDENCY_PATHS = frozenset([
    PARENT_PATH, PARENT_INPUTS,
    'ci/namespace-progress-performance-r3/run.py', 'ci/namespace-progress-performance-r3/test_run.py',
    '.github/workflows/namespace-progress-performance-r3-targeted.yml'])
ZERO_FLAGS = {'LUOBO_EMAIL_INTEGRATION': '0', 'LUOBO_RUN_LEGACY_LOAD_TESTS': '0',
              'LUOBO_RUN_LEGACY_EXAMPLES': '0'}
NEW_TOP = 'TestNamespaceProgressFastJSONPreservesOriginalStrictness'
NEW_SUBS = ['page', 'same-key-different-objects', 'duplicate-root', 'case-fold-root',
            'escaped-key-alias', 'nested-object-alias', 'unicode-key-alias', 'surrogate-key-alias',
            'strings-are-not-objects', 'depth64', 'depth65', 'extra-value', 'bad-comma', 'empty',
            'unknown-field', 'invalid-typed-page', 'number-max-finite', 'number-overflow-positive',
            'number-overflow-negative', 'number-underflow-positive', 'number-underflow-negative',
            'number-min-positive-subnormal', 'known-number-overflow', 'known-uint64-overflow',
            'known-number-noninteger', 'known-number-wrong-shape']
_PARENT = None


def legacy():
    """Import definitions from the immutable1199 controller; no parent CLI/run."""
    global _PARENT
    if _PARENT is None:
        path = REPO / PARENT_PATH
        if path.resolve() != path or path.is_symlink() or not path.is_file():
            raise ValueError('canonical frozen1199 runner required')
        raw = path.read_bytes()
        if hashlib.sha256(raw).hexdigest() != PARENT_RUNNER_SHA or stat.S_IMODE(path.stat().st_mode) != 0o644:
            raise ValueError('frozen1199 runner differs')
        spec = importlib.util.spec_from_file_location('source_only_frozen_namespace1199', path)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        _PARENT = module
    return _PARENT


def expected():
    parent = legacy().EXPECTED
    return {'tops': sorted(parent['tops'] + [NEW_TOP]),
            'subs': sorted(parent['subs'] + [NEW_TOP + '/' + sub for sub in NEW_SUBS])}


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def regular(path):
    return legacy().regular(path)


def rows(bodies):
    return legacy().rows(bodies)


def digest(items):
    return legacy().digest(items)


def apply_overlay(c, previous, root):
    parent = legacy()
    result, seen = dict(previous), set()
    if len(c['overlay']) != 3:
        raise ValueError('exact three formatted R3 sources required')
    for row in c['overlay']:
        target = parent.name(row['target'])
        before = previous.get(target)
        if (target not in OVERLAY_TARGETS or target in seen
                or (before is not None) != (target in MODIFIED)
                or row['beforeSHA256'] != (sha(before) if before is not None else None)
                or row['path'] != SOURCE_ROOT + target or row['mode'] != '0o644'):
            raise ValueError('exact one-modified/two-new1199-before overlay required')
        seen.add(target)
        result[target] = parent.pinned(row, root)
    source = root / SOURCE_ROOT
    actual = {str(path.relative_to(root)) for path in source.rglob('*') if path.is_file() or path.is_symlink()}
    if seen != OVERLAY_TARGETS or actual != {row['path'] for row in c['overlay']}:
        raise ValueError('extra or missing R3 source')
    return result


def verify():
    parent = legacy()
    c = json.loads(regular(INPUTS))
    selected = expected()
    if (c['schema'] != 1 or c['scope'] != SCOPE or c['parentCount'] != 1199
            or c['parentDigest'] != PARENT_DIGEST or c['effectiveCount'] != 1201
            or c['effectiveDigest'] != FINAL_DIGEST
            or c['parentRunner']['path'] != PARENT_PATH or c['parentRunner']['sha256'] != PARENT_RUNNER_SHA
            or c['parentInputs']['path'] != PARENT_INPUTS or c['parentInputs']['sha256'] != PARENT_INPUTS_SHA
            or c['targets'] != {PACKAGE: selected}
            or c['history'] != HISTORY
            or c['expected'] != {'topPASS': 7, 'subPASS': 43, 'skip': 0, 'fail': 0, 'packages': 1,
                                 'vet': False, 'fullRegression': False, 'databaseFixture': False}
            or c['origin']['formattedManifestSHA256'] != FORMATTED_MANIFEST
            or len(c['dependencies']) != len(DEPENDENCY_PATHS)
            or {row['path'] for row in c['dependencies']} != DEPENDENCY_PATHS):
        raise ValueError('exact1199-to1201 R3 / Progress7/43 contract required')
    for row in c['dependencies']:
        parent.pinned(row, REPO)
    parent.pinned(c['parentRunner'], REPO)
    parent.pinned(c['parentInputs'], REPO)
    prior, before = parent.verify()  # Source-only. Never parent.run(), Go, DB, Chunk or CLI.
    if len(before) != 1199 or prior['effectiveDigest'] != PARENT_DIGEST:
        raise ValueError('exact original1199 source-only parent required')
    bodies = apply_overlay(c, before, REPO)
    modes = dict(prior['sourceModes'])
    modes.update({row['target']: 0o644 for row in c['overlay']})
    if (len(bodies) != 1201 or rows(bodies) != c['effectiveSource']
            or digest(rows(bodies)) != FINAL_DIGEST or modes != c['sourceModes']):
        raise ValueError('formatted1201 bodies/modes/digest differ')
    if any(bodies[path] != body for path, body in before.items() if path.endswith('_test.go')):
        raise ValueError('all existing tests including original10001 must remain exact')
    for path in ('server/go.mod', 'server/go.sum', 'server/config/config.example.yaml'):
        if bodies[path] != before[path]:
            raise ValueError('original locks/public configuration must remain exact')
    new_test = bodies['server/internal/namespaceinventory/progress_json_test.go']
    if Counter(x.decode() for x in re.findall(rb'^func (Test\w+)\(', new_test, re.M)) != Counter({NEW_TOP: 1}):
        raise ValueError('one exact strict equivalence top required')
    names = re.findall(rb'\{"([^"\n]+)",', new_test)
    if Counter(x.decode() for x in names) != Counter(NEW_SUBS):
        raise ValueError('all original16 plus new10 strict equivalence cases required')
    return c, bodies


def audit(events, code, package, selected):
    if package != PACKAGE or selected != expected():
        raise ValueError('only exact Progress7/43 package allowed')
    return legacy().audit(events, code, package, selected)


def require_guard(record, code):
    legacy().require_guard(record, code)


def hosted_environment(env):
    legacy().hosted_environment(env)


def test_environment(base, work, goroot):
    return legacy().test_environment(base, work, goroot)


def isolation_command(env, uid, gid):
    if any(env.get(k) != 'off' for k in ('GOPROXY', 'GOSUMDB', 'GOAUTH', 'GOWORK', 'GOENV')):
        raise ValueError('offline no-auth test environment required')
    return legacy().isolation_command(env, uid, gid)


def write(path, value):
    legacy().write(path, value)


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
    work = temporary / ('namespace-progress-performance-r3-' + os.environ['GITHUB_RUN_ID'] + '-' + os.environ['GITHUB_RUN_ATTEMPT'])
    if not temporary.is_absolute() or temporary.resolve() != temporary or work.exists():
        raise ValueError('fresh canonical work required')
    fs = os.statvfs(temporary)
    if fs.f_bavail * fs.f_frsize < 4 * 1024**3:
        raise ValueError('4GiB bootstrap floor; no Go child started')
    work.mkdir(mode=0o700)
    output = REPO / 'ci-results/namespace-progress-performance-r3'
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
          'parent1199Digest': PARENT_DIGEST, 'parentCorrected1184Digest': 'c94e475afef6c437ca913a4691fe6d84e694984e5dc503a6ac7f7b33bc1b36d0',
          'effectiveCount': 1201, 'effectiveDigest': FINAL_DIGEST, 'goVersion': version, 'targets': {PACKAGE: selected},
          'locksBefore': rows(locks), 'generatedPublicConfigSHA256': sha(template), 'noRuntimeAdoption': True,
          'DBFixtureSelected': False, 'newTop': 1, 'affectedTop': 6})

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
    write(output / 'summary.json', {'state': 'pure_progress_r3_7_43_passed' if accepted else 'failed_or_incomplete',
          'scope': SCOPE, 'packages': {PACKAGE: outcome}, 'effectiveCount': 1201, 'sourceDigest': FINAL_DIGEST,
          'currentExecutionExpected': {'topPASS': 7, 'subPASS': 43}, 'historicalResultsOnly': c['history'],
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
            print('Exact original1199 + formatted R3 overlay3 =1201 / target7/43; no Go/DB execution.')
        else:
            raise SystemExit(run())
    except (ValueError, KeyError, TypeError, OSError, subprocess.SubprocessError, json.JSONDecodeError):
        print('R3 performance validation incomplete; original raw retained.', file=sys.stderr)
        raise SystemExit(2)
