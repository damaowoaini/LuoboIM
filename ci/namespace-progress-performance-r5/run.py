#!/usr/bin/env python3
"""R8 unchanged Large10001/600s once; owned CPU/alloc profiles, no short rerun."""
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
SCOPE = 'namespace_progress_performance_r8_1205_original_large1_CPU_alloc_diagnostic_privateFS_BSON_noDB_no_runtime'
PREFIX = 'github.com/openimsdk/open-im-server/v3/'
PACKAGE = 'internal/namespaceinventory'
PARENT_PATH = 'ci/namespace-progress-performance-r4/run.py'
PARENT_INPUTS = 'ci/namespace-progress-performance-r4/inputs.json'
PARENT_RUNNER_SHA = '0e740711a364b5ff14f52c815e966df4d3d1320f9cb48f19b69380f51f8c8275'
PARENT_INPUTS_SHA = '50ec67c768015dd9754088bd0418cd649108f6a118375eddae3452dbc7e06d28'
PARENT_DIGEST = '2134b7ddbb9202f85878d665ac4f44ca3566d757fe51b2941d9f48796af42465'
FINAL_DIGEST = '75dde0dd7d846cf83d0eb08cf876dd2cd14b56b97a04ec5b17ccd44b4cfb3779'
DIGEST_ALGORITHM = 'ascending UTF8(path)+NUL+ASCII SHA256+LF then SHA256'
SOURCE_MANIFEST = '061e5339513ebae41a92766528109585efa3173d4970597b7b9136a133531a40'
HISTORY = {'R6Actual': {'run': 39001760279, 'job': 117305993492, 'head': '8608b7f313a58aaef46ecaaacb1806baae1c754a', 'source1204Digest': '3f2056a4907185381fc7c3a07ce8ac9a2735722128a15b3d14587a92e31ff98e', 'short3top13sub': 'actual PASS; reused historical only, not selected again', 'successfulNames': {'tops': ['TestNamespaceProgressMemoKeepsCustody', 'TestNamespaceProgressMetadataLookupOncePerReference', 'TestNamespaceProgressResolvedMetadataRetainsBindings'], 'subs': ['TestNamespaceProgressMemoKeepsCustody/bounded-eviction', 'TestNamespaceProgressMemoKeepsCustody/copy-isolation', 'TestNamespaceProgressMemoKeepsCustody/foreign-reference', 'TestNamespaceProgressMemoKeepsCustody/invocation-local', 'TestNamespaceProgressMemoKeepsCustody/limits', 'TestNamespaceProgressMemoKeepsCustody/private-mode', 'TestNamespaceProgressMemoKeepsCustody/raw-tamper', 'TestNamespaceProgressMemoKeepsCustody/strict-redecode', 'TestNamespaceProgressMetadataLookupOncePerReference/checked-row', 'TestNamespaceProgressMetadataLookupOncePerReference/summary-seen', 'TestNamespaceProgressResolvedMetadataRetainsBindings/number-and-row', 'TestNamespaceProgressResolvedMetadataRetainsBindings/pass-and-metaSHA', 'TestNamespaceProgressResolvedMetadataRetainsBindings/private-page-and-row']}, 'Large10001': 'actually started, Go600s timeout/packageFAIL; no completed top result', 'receipt': {'path': 'references/integration/native-r8-diag-perf-r6-observer-20261017/custody-perf-39001760279/independent-terminal-receipt.json', 'bytes': 8954, 'sha256': 'af73ee46c77d1dafe071394f7eec3ee8838543ce999db8972f6fb701506e515c'}}, 'earlierHistoryUnchanged': {'parent1203SourceOnly': True, 'oldRun': 39000056079, 'oldActual': 'short original3top6sub and new legacy-checkpoint1sub passed; failed13sub stopped in shared fixture; Large unstarted', 'reusedOriginalPassedTops': ['TestNamespaceProgressContinuesAcrossEveryStage', 'TestNamespaceProgressImportsOldPrefixWithoutRescanning', 'TestNamespaceProgressRejectsUnboundDerivedState'], 'reusedPassedSub': 'TestNamespaceProgressResolvedMetadataRetainsBindings/legacy-checkpoint', 'oldCheckpoint4_6': 'historical R4 evidence only; unchanged checkpoint logic, not a fixture-R1 run', 'unselectedOriginal': 'strictJSON26 and classifier3 are not fresh passes here', 'fixtureR1GoTestsAtPreparation': 0}, 'unselectedOriginal': 'No full11/49, strictJSON26, classifier3 or current short3/13 rerun', 'R7GoAtPreparation': 0}
ORIGIN = {'R7SourceCandidateSHA256': 'a1a9b71a5e521a9a551a2191e39f83cc4f5ed2a9f4763ea6c1750c1a7728e1f8', 'RootActualFormat3': {'path': '.qa/namespace-progress-r7-source-format3-1/root-acceptance.json', 'bytes': 1283, 'sha256': '2d7b014cfb1356910439c2823bfd614ec434cd350668fe8b8309f8bf172e5bad'}, 'actualFormattedSources': [{'target': 'server/internal/namespaceinventory/progress_batch.go', 'formattedSource': {'path': '.qa/namespace-progress-r7-source-format3-1/source/server/internal/namespaceinventory/progress_batch.go', 'bytes': 5549, 'sha256': '275768fedbe6c8fd078f398c7a5f3f8351695a4f982db6cb18dfadd5189927cf'}, 'CI': 'ci/namespace-progress-performance-r5/source/server/internal/namespaceinventory/progress_batch.go'}, {'target': 'server/internal/namespaceinventory/progress_index.go', 'formattedSource': {'path': '.qa/namespace-progress-r7-source-format3-1/source/server/internal/namespaceinventory/progress_index.go', 'bytes': 7522, 'sha256': '0f46357ed071b3c7f89fdb5b7a361d73bb690ae8f60ec589598acdb8331a2fb5'}, 'CI': 'ci/namespace-progress-performance-r5/source/server/internal/namespaceinventory/progress_index.go'}, {'target': 'server/internal/namespaceinventory/progress_index_decode_test.go', 'formattedSource': {'path': '.qa/namespace-progress-r7-source-format3-1/source/server/internal/namespaceinventory/progress_index_decode_test.go', 'bytes': 2815, 'sha256': 'ce7ee655eb3c4fc2a36c242b3c8d7320b955c65b30bcdac3639043a7531a3500'}, 'CI': 'ci/namespace-progress-performance-r5/source/server/internal/namespaceinventory/progress_index_decode_test.go'}], 'R6OriginUnchanged': {'originalR5SourceManifestSHA256': '061e5339513ebae41a92766528109585efa3173d4970597b7b9136a133531a40', 'GoFormatAtPreparation': 0, 'rootGofmtBeforeAdoptionRequired': True, 'GoTestsAtPreparation': 0, 'lineage': 'immutable R4.verify source1203 -> R5 product2/test1 unchanged plus exact shared-fixture one-byte fix -> source1204', 'original11top49subNamesAssertionsPreserved': True, 'onlySharedFixtureSteps1to2': True, 'original10001_and_600s_preserved': True}, 'lineage': 'verified immutable R4 source1203 + exact borrowed R6 overlay4 -> same1204 digest + Root actual formatted R7 overlay3 ->1205', 'original10001_and_600s_preserved': True, 'genericDecodeStrictAndUniqueProgressJSONUnchanged': True, 'GoTestsAtPreparation': 0}
SOURCE_PARENT = {'count': 1204, 'digest': '3f2056a4907185381fc7c3a07ce8ac9a2735722128a15b3d14587a92e31ff98e', 'originalR6InputsSHA256': 'aec9a0835f1f4a588f4bc6fe358e454966a23f81171cf8bbc1cdc2cdb1d0f812', 'borrowedOverlayTargets': ['server/internal/namespaceinventory/progress_capture.go', 'server/internal/namespaceinventory/progress_journal.go', 'server/internal/namespaceinventory/progress_metadata_test.go', 'server/internal/namespaceinventory/progress_page_memo_test.go']}
SOURCE_ROOT = 'ci/namespace-progress-performance-r5/source/'
MODIFIED = frozenset('server/internal/namespaceinventory/' + n for n in
                     ['progress_capture.go', 'progress_journal.go', 'progress_page_memo_test.go',
                      'progress_batch.go', 'progress_index.go'])
NEW = frozenset(['server/internal/namespaceinventory/progress_metadata_test.go',
                 'server/internal/namespaceinventory/progress_index_decode_test.go'])
OVERLAY_TARGETS = MODIFIED | NEW
R6_OVERLAY_TARGETS = frozenset(SOURCE_PARENT['borrowedOverlayTargets'])
INDEX_TOP = 'TestNamespaceProgressIndexDecodePreservesOriginalStrictOracle'
PUBLICATION_TOP = 'TestNamespaceProgressBatchKeepsUnpublishedStateUntrusted'
INDEX_SUBS = ['valid-raw-value', 'unknown-top-field', 'escaped-case-alias', 'raw-nested-duplicate', 'raw-unicode-fold-alias', 'raw-depth-accepted', 'raw-depth-overflow', 'raw-float64-overflow', 'raw-float64-underflow', 'raw-largest-finite-float64', 'typed-depth-range', 'typed-depth-fraction', 'typed-depth-shape', 'typed-items-shape', 'trailing-second-value']
DEPENDENCY_PATHS = frozenset([PARENT_PATH, PARENT_INPUTS,
    'ci/namespace-progress-performance-r5/run.py', 'ci/namespace-progress-performance-r5/test_run.py',
    '.github/workflows/namespace-progress-performance-r5-targeted.yml'])
NEW_TOPS = ['TestNamespaceProgressMetadataLookupOncePerReference',
            'TestNamespaceProgressResolvedMetadataRetainsBindings']
NEW_CASES = {NEW_TOPS[0]: ['checked-row', 'summary-seen'],
             NEW_TOPS[1]: ['pass-and-metaSHA', 'number-and-row', 'legacy-checkpoint', 'private-page-and-row']}
OLD_SHORT = ['TestNamespaceProgressMemoKeepsCustody']
LARGE = 'TestNamespaceProgressLargeHistoryHasNoFinalFullSweep'
FIXTURE_TARGET = 'server/internal/namespaceinventory/progress_page_memo_test.go'
FIXTURE_OLD = b'if _, e := BeginResumableCapture(context.Background(), s, s.id.Binding, dir, l, ProgressBudget{Steps: 1, SourcePages: 1}); e != nil {'
FIXTURE_NEW = FIXTURE_OLD.replace(b'Steps: 1,', b'Steps: 2,')
HISTORY['R7Actual'] = {
    'run': 39004002126, 'job': 117312617652,
    'head': 'd1403c6864eb185afe9bdec522f30a6bb68da93b',
    'source1205Digest': FINAL_DIGEST,
    'short2top19sub': 'actual PASS; exact successful names retained below, historical only, not selected again',
    'successfulNames': {'tops': sorted([INDEX_TOP, PUBLICATION_TOP]),
        'subs': sorted([INDEX_TOP + '/' + s for s in INDEX_SUBS]
                       + [PUBLICATION_TOP + '/' + s for s in ['discard', 'bounded-spill', 'blocked-private-object', 'CID-origin-closure']])},
    'Large10001': 'actually started; original600s timeout/packageFAIL in summary-rows, no named completion',
    'receipt': {'path': 'references/integration/namespace-progress-r7-d140-observer-20261017/independent-terminal-receipt.json',
        'bytes': 13913, 'sha256': 'f2409335251c610c01f3ce1abd8a6cb5fa81fdedcd09022718eea42f8881d5ad'}}
PROFILE_CONTRACT = {'profileMaxBytes': 8 << 20, 'reportMaxBytes': 128 << 10,
    'binaryMaxBytes': 256 << 20, 'topNodes': 30, 'toolTimeoutSeconds': 60,
    'memProfileRate': 'Go default sampled; no override', 'uploadTestBinary': False,
    'primaryExitPreserved': True, 'freshSourceBeforeAndAfterDiagnostics': True}
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
    return {'large': {'tops': [LARGE], 'subs': []}}


def run_expression(label):
    selected = expected()[label]
    expression = '^(' + '|'.join(selected['tops']) + ')$'
    return expression


def regular(path):
    return parent().regular(path)


def rows(bodies):
    return parent().rows(bodies)


def digest(items):
    return parent().digest(items)


def apply_overlay(c, previous, root):
    result, seen = dict(previous), set()
    if len(c['overlay']) != 7:
        raise ValueError('exact borrowed R6 four plus formatted R7 three overlay required')
    for row in c['overlay']:
        target = core().name(row['target'])
        before = previous.get(target)
        if (target not in OVERLAY_TARGETS or target in seen
                or (before is not None) != (target in MODIFIED)
                or row['beforeSHA256'] != (sha(before) if before is not None else None)
                or row['path'] != SOURCE_ROOT + target or row['mode'] != '0o644'):
            raise ValueError('exact1203-before borrowed R6 and formatted R7 sources required')
        seen.add(target)
        result[target] = core().pinned(row, root)
    source = root / SOURCE_ROOT
    actual = {str(p.relative_to(root)) for p in source.rglob('*') if p.is_file() or p.is_symlink()}
    if seen != OVERLAY_TARGETS or actual != {r['path'] for r in c['overlay']}:
        raise ValueError('extra or missing R6/R7 source')
    return result


def check_r6_source_parent(before, bodies):
    inherited = dict(before)
    for target in R6_OVERLAY_TARGETS:
        inherited[target] = bodies[target]
    if len(inherited) != SOURCE_PARENT['count'] or digest(rows(inherited)) != SOURCE_PARENT['digest']:
        raise ValueError('exact inherited R6 source1204 digest required before R7')
    return inherited


def check_fixture_change(before, after):
    if before.count(FIXTURE_OLD) != 1 or after != before.replace(FIXTURE_OLD, FIXTURE_NEW, 1):
        raise ValueError('only shared fixture Steps1-to2 exact byte change allowed')


def verify():
    c = json.loads(regular(INPUTS))
    if (c['schema'] != 1 or c['scope'] != SCOPE or c['parentCount'] != 1203
            or c['parentDigest'] != PARENT_DIGEST or c['effectiveCount'] != 1205
            or c['effectiveDigest'] != FINAL_DIGEST or c['effectiveDigestAlgorithm'] != DIGEST_ALGORITHM
            or c['sourceModeSource'] != 'verified R4 modes plus borrowed R6 overlay4 and formatted R7 overlay3 mode0644'
            or c['parentRunner']['path'] != PARENT_PATH or c['parentRunner']['sha256'] != PARENT_RUNNER_SHA
            or c['parentInputs']['path'] != PARENT_INPUTS or c['parentInputs']['sha256'] != PARENT_INPUTS_SHA
            or c['phases'] != expected() or c['preservedOriginalTargets'] != parent().expected()
            or c['history'] != HISTORY or c['origin'] != ORIGIN or c['sourceParent'] != SOURCE_PARENT
            or c['profileContract'] != PROFILE_CONTRACT
            or c['expected'] != {'large': {'topPASS': 1, 'subPASS': 0},
                                 'skip': 0, 'fail': 0, 'vet': False, 'fullRegression': False, 'databaseFixture': False}
            or len(c['dependencies']) != len(DEPENDENCY_PATHS)
            or {r['path'] for r in c['dependencies']} != DEPENDENCY_PATHS):
        raise ValueError('exact R8 unchanged Large1 profile-only source1205 contract required')
    for row in c['dependencies'] + [c['parentRunner'], c['parentInputs']]:
        core().pinned(row, REPO)
    prior, before = parent().verify()  # Source only; no parent run/CLI/Go.
    if len(before) != 1203 or prior['effectiveDigest'] != PARENT_DIGEST:
        raise ValueError('frozen1203 parent differs')
    bodies = apply_overlay(c, before, REPO)
    check_r6_source_parent(before, bodies)
    modes = dict(prior['sourceModes'])
    modes.update({r['target']: 0o644 for r in c['overlay']})
    if len(bodies) != 1205 or digest(rows(bodies)) != FINAL_DIGEST or set(modes) != set(bodies):
        raise ValueError('source1205 bodies/modes/digest differ')
    for p, raw in before.items():
        if p == FIXTURE_TARGET:
            check_fixture_change(raw, bodies[p])
        elif p.endswith('_test.go') and bodies[p] != raw:
            raise ValueError('all other original11/49 test bodies including10001 must remain exact')
    for p in ('server/go.mod', 'server/go.sum', 'server/config/config.example.yaml'):
        if bodies[p] != before[p]:
            raise ValueError('original source locks/configuration differ')
    test = bodies['server/internal/namespaceinventory/progress_metadata_test.go']
    if Counter(x.decode() for x in re.findall(rb'^func (Test\w+)\(', test, re.M)) != Counter(NEW_TOPS):
        raise ValueError('exact two R5 metadata test tops required')
    loops = re.findall(rb'for _, kind := range \[\]string\{([^}]+)\}', test)
    if [re.findall(rb'"([^"\n]+)"', x) for x in loops] != [[s.encode() for s in v] for v in NEW_CASES.values()]:
        raise ValueError('exact six R5 metadata subcases required')
    index = bodies['server/internal/namespaceinventory/progress_index_decode_test.go']
    if Counter(x.decode() for x in re.findall(rb'^func (Test\w+)\(', index, re.M)) != Counter([INDEX_TOP]):
        raise ValueError('only the new original-strict-index oracle top required')
    if re.findall(rb'^\s*\{"([^"\n]+)",', index, re.M) != [name.encode() for name in INDEX_SUBS]:
        raise ValueError('exact fifteen index oracle subcases required')
    return dict(c, sourceModes=modes), bodies


def audit(events, code, label):
    if label not in expected():
        raise ValueError('only unchanged original Large phase allowed')
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


def owned_profile_file(path, root, limit, load=True):
    """Fixed owned leaf only; stable body snapshots, not a native/source trust grant."""
    if (root.resolve(strict=True) != root or not root.is_dir()
            or path.parent != root or path.resolve(strict=True) != path):
        raise ValueError('canonical owned profile leaf required')
    before = path.lstat()
    if (not stat.S_ISREG(before.st_mode) or before.st_uid != os.getuid()
            or before.st_nlink != 1 or before.st_mode & 0o022 or before.st_size < 1
            or before.st_size > limit):
        raise ValueError('bounded owned profile body required')
    digestor, parts, total = hashlib.sha256(), [], 0
    with path.open('rb') as handle:
        opened = os.fstat(handle.fileno())
        if (opened.st_dev, opened.st_ino) != (before.st_dev, before.st_ino):
            raise ValueError('profile leaf replaced before read')
        while True:
            block = handle.read(min(1 << 20, limit + 1 - total))
            if not block:
                break
            total += len(block)
            if total > limit:
                raise ValueError('profile body grew beyond cap')
            digestor.update(block)
            if load:
                parts.append(block)
        after = os.fstat(handle.fileno())
    final = path.lstat()
    identity = lambda value: (value.st_dev, value.st_ino, value.st_mode, value.st_uid,
        value.st_gid, value.st_nlink, value.st_size, value.st_mtime_ns, value.st_ctime_ns)
    if identity(before) != identity(after) or identity(before) != identity(final) or total != before.st_size:
        raise ValueError('profile body changed during read')
    return {'bytes': total, 'sha256': digestor.hexdigest(), 'mode': stat.S_IMODE(before.st_mode)}, b''.join(parts)


def profile_diagnostics(command, isolation, profile_dir, output):
    """Local pprof only after test exit, including nonzero exit. Binary stays in work."""
    output.mkdir(mode=0o700)
    result = {'accepted': False, 'profiles': {}, 'reports': {}, 'testBinaryUploaded': False}
    binary = profile_dir / 'large.test'
    try:
        binary_row, _ = owned_profile_file(binary, profile_dir, PROFILE_CONTRACT['binaryMaxBytes'], False)
    except (FileNotFoundError, ValueError):
        result['binaryState'] = 'missing_or_rejected'
        write(output / 'summary.json', result)
        return result
    result['binaryBody'] = binary_row
    profiles = {}
    for name in ['cpu', 'alloc']:
        path = profile_dir / (name + '.pprof')
        try:
            body, raw = owned_profile_file(path, profile_dir, PROFILE_CONTRACT['profileMaxBytes'])
            if raw[:2] != b'\x1f\x8b':
                raise ValueError('gzip profile header required')
            profiles[name] = (path, body, raw)
            result['profiles'][name] = dict(body, state='bounded_gzip_pending_pprof_validation')
        except (FileNotFoundError, ValueError):
            result['profiles'][name] = {'state': 'missing_or_rejected'}
    views = [('cpu-top', 'cpu', []), ('alloc-objects-top', 'alloc', ['-sample_index=alloc_objects']),
             ('alloc-space-top', 'alloc', ['-sample_index=alloc_space'])]
    for label, name, flags in views:
        if name not in profiles:
            continue
        path, before, raw = profiles[name]
        argv = isolation + ['go', 'tool', 'pprof', '-top', '-cum', '-nodecount=30', '-symbolize=none']
        argv += flags + [str(binary), str(path)]
        # The regular command wrapper still enforces original resource/timeout/netns.
        # A pprof parse error is evidence, not a successful test result.
        folder, code = command('pprof-' + label, argv, PROFILE_CONTRACT['toolTimeoutSeconds'])
        binary_after, _ = owned_profile_file(binary, profile_dir, PROFILE_CONTRACT['binaryMaxBytes'], False)
        after, _ = owned_profile_file(path, profile_dir, PROFILE_CONTRACT['profileMaxBytes'])
        if after != before or binary_after != binary_row:
            raise ValueError('pprof changed its selected input body')
        report = {'exit': code, 'accepted': False}
        for filename in ['stdout.jsonl', 'stderr.log', 'resource.json', 'guard.log']:
            source = folder / filename
            if source.exists() and source.stat().st_size == 0:
                continue
            try:
                body, content = owned_profile_file(source, folder, PROFILE_CONTRACT['reportMaxBytes'])
            except (FileNotFoundError, ValueError):
                report[filename] = {'state': 'missing_or_rejected'}
                continue
            destination = output / (label + '-' + filename)
            with destination.open('xb') as handle:
                handle.write(content)
            destination.chmod(0o600)
            report[filename] = body
        report['accepted'] = code == 0 and 'sha256' in report.get('stdout.jsonl', {})
        result['reports'][label] = report
        if report['accepted'] and not (output / path.name).exists():
            with (output / path.name).open('xb') as handle:
                handle.write(raw)
            (output / path.name).chmod(0o600)
            result['profiles'][name]['state'] = 'pprof_accepted_and_body_copied_under_cap'
    for path, before, _ in profiles.values():
        after, _ = owned_profile_file(path, profile_dir, PROFILE_CONTRACT['profileMaxBytes'])
        if after != before:
            raise ValueError('pprof changed an earlier selected profile body')
    result['accepted'] = (set(result['reports']) == {v[0] for v in views}
                          and all(v['accepted'] for v in result['reports'].values()))
    write(output / 'summary.json', result)
    return result


def execute_phases(command, isolation, post, profile_dir, diagnose):
    argv = isolation + ['go', 'test', '-mod=readonly', '-p=1', '-parallel=1', '-race', '-json', '-count=1',
        '-vet=off', '-timeout=600s', '-run', run_expression('large'),
        '-cpuprofile', str(profile_dir / 'cpu.pprof'), '-memprofile', str(profile_dir / 'alloc.pprof'),
        '-o', str(profile_dir / 'large.test'), './' + PACKAGE]
    try:
        folder, code = command('large', argv, 900)
        try:
            events = [json.loads(x) for x in regular(folder / 'stdout.jsonl').decode().splitlines() if x.strip()]
            outcome = {'accepted': True, 'exit': code, **audit(events, code, 'large')}
        except (ValueError, UnicodeError, TypeError, KeyError):
            outcome = {'accepted': False, 'exit': code, 'rawRetained': True}
        # Freeze the main test outcome BEFORE any successful diagnostic command.
        write(folder / 'summary.json', outcome)
    finally:
        post('large')
    try:
        diagnostics = diagnose()  # Natural failed test still reaches read-only diagnostics.
    finally:
        post('diagnostics')
    return {'large': outcome, 'diagnostics': diagnostics}


def final_exit(outcomes):
    accepted = outcomes['large']['accepted'] and outcomes['diagnostics']['accepted']
    return 0 if accepted else (outcomes['large']['exit'] or 1)


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
    work = temporary / ('namespace-progress-performance-r8-' + os.environ['GITHUB_RUN_ID'] + '-' + os.environ['GITHUB_RUN_ATTEMPT'])
    if not temporary.is_absolute() or temporary.resolve() != temporary or work.exists():
        raise ValueError('fresh canonical work required')
    fs = os.statvfs(temporary)
    if fs.f_bavail * fs.f_frsize < 4 * 1024**3:
        raise ValueError('4GiB bootstrap floor; no Go child started')
    work.mkdir(mode=0o700)
    output = REPO / 'ci-results/namespace-progress-performance-r8'
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
    for folder in ['modcache', 'buildcache', 'gopath', 'tmp', 'profiles', 'profile-reports']:
        (work / folder).mkdir(mode=0o700)
    env = test_environment(os.environ, work, goroot)
    locks = {path: bodies[path] for path in ['server/go.mod', 'server/go.sum']}
    write(output / 'source.json', {'scope': SCOPE, 'commit': os.environ['GITHUB_SHA'], 'inputsSHA256': sha(regular(INPUTS)),
          'parent1203Digest': PARENT_DIGEST, 'parent1201Digest': parent().PARENT_DIGEST,
          'effectiveCount': 1205, 'effectiveDigest': FINAL_DIGEST, 'goVersion': version,
          'phases': selected, 'locksBefore': rows(locks), 'generatedPublicConfigSHA256': sha(template),
          'noRuntimeAdoption': True, 'DBFixtureSelected': False, 'currentLargeTop': 1, 'currentLargeSub': 0,
          'profileContract': PROFILE_CONTRACT,
          'sourceParent1204': SOURCE_PARENT,
          'originalLarge10001_600s': True, 'history': c['history']})

    def command(label, argv, timeout):
        remaining = 1800 - (time.monotonic() - started)
        if remaining < 1:
            raise ValueError('whole controller budget exhausted')
        timeout = max(1, int(min(remaining, timeout)))
        folder = (work / 'profile-reports' if label.startswith('pprof-') else output) / label
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

    outcomes = execute_phases(command, isolation, post, work / 'profiles',
        lambda: profile_diagnostics(command, isolation, work / 'profiles', output / 'diagnostics'))
    accepted = outcomes['large']['accepted'] and outcomes['diagnostics']['accepted']
    write(output / 'summary.json', {'state': 'r8_original_large_and_bounded_profiles_complete' if accepted else 'failed_or_incomplete',
          'scope': SCOPE, 'phases': outcomes, 'effectiveCount': 1205, 'sourceDigest': FINAL_DIGEST,
          'currentExecutionExpected': c['expected'], 'historicalResultsOnly': c['history'],
          'fullOriginal11_49Rerun': False, 'wholeRegression': False, 'vetRun': False,
          'runtimeSwitch': False, 'notDeployable': True, 'databaseVendorCallbackFixture': False,
          'networkBoundary': 'each phase in fresh empty network namespace with original nonroot UID/GID',
          'workRetainedForEphemeralRunnerCleanup': True,
          'fixtureCleanup': 'not applicable; no service or container started',
          'inventoryStateAlwaysUntrustedIncomplete': True})
    # Diagnostic success can never replace the exact failed main test exit.
    return final_exit(outcomes)


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--verify', action='store_true')
    args = parser.parse_args()
    try:
        if args.verify:
            verify()
            print('Exact unchanged R7 source1205; original Large1 CPU/alloc diagnostic only, old oracle2/19 historical; no Go/DB execution.')
        else:
            raise SystemExit(run())
    except (ValueError, KeyError, TypeError, OSError, subprocess.SubprocessError, json.JSONDecodeError):
        print('R8 Large profiling validation incomplete; original raw retained.', file=sys.stderr)
        raise SystemExit(2)
