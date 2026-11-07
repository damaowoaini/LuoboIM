"""Exact four live SQL/Redis admin-session tests; no graph/full-suite/deployment."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import stat
import subprocess
import sys

import fixture

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
PACKAGE = 'github.com/OpenIMSDK/chat/pkg/common/admindelegation'
TESTS = ['TestAdminDisableRevocationSurvivesEnableAndRedisFailure',
         'TestAdminDisableOutboxFailureRollsBackAccount',
         'TestDelegationLiveSQLAndSessionRevocation',
         'TestAdminLogoutOnlyRevokesExactBoundSession']
COHORT = 'chart-startup1-prior-admin4-chart5-v2'
BUILDS = [('chart', './cmd/api/chat-api', 'chat-api'), ('chart', './cmd/api/admin-api', 'admin-api'),
          ('chart', './cmd/rpc/chat-rpc', 'chat-rpc'), ('chart', './cmd/rpc/admin-rpc', 'admin-rpc'),
          ('chart', './cmd/rpc/office-rpc', 'office-rpc')]


def sha(raw): return hashlib.sha256(raw).hexdigest()

def stable(path, maximum=256 << 20):
    path = Path(path)
    if path.resolve() != path:
        raise ValueError('canonical source/tool required')
    before = path.lstat()
    if not stat.S_ISREG(before.st_mode) or before.st_nlink != 1 or before.st_size > maximum:
        raise ValueError('bounded regular single-link source/tool required')
    with path.open('rb') as stream:
        opened = os.fstat(stream.fileno())
        digest = hashlib.sha256()
        for part in iter(lambda: stream.read(65536), b''): digest.update(part)
        after = os.fstat(stream.fileno())
    keys = ('st_dev', 'st_ino', 'st_mode', 'st_uid', 'st_nlink', 'st_size', 'st_mtime_ns', 'st_ctime_ns')
    if any(getattr(before, k) != getattr(opened, k) or getattr(opened, k) != getattr(after, k)
           or getattr(after, k) != getattr(path.lstat(), k) for k in keys):
        raise ValueError('source/tool changed during read')
    return {'bytes': before.st_size, 'sha256': digest.hexdigest(), 'mode': stat.S_IMODE(before.st_mode)}

def write(path, value):
    fixture.images.private_write(path, (json.dumps(value, sort_keys=True, indent=2) + '\n').encode())

def tree(root):
    root = Path(root)
    selected = set(root.rglob('*.go'))
    selected.update(root / name for name in ['go.mod', 'go.sum', 'pkg/common/config/version', 'pkg/common/config/template.xlsx'])
    return [{'path': path.relative_to(root).as_posix(), **stable(path)} for path in sorted(selected)]

def verify(expected, source_required=True):
    raw = (HERE / 'inputs.json').read_bytes()
    if not re.fullmatch('[0-9a-f]{64}', expected) or sha(raw) != expected:
        raise ValueError('exact reviewed inputs SHA required')
    value = json.loads(raw)
    if value['cohort'] != COHORT or value['tests'] != TESTS or value['expectedSubtests'] != 0:
        raise ValueError('exact four names only')
    if sha(json.dumps(value['source'], sort_keys=True, separators=(',', ':')).encode()) != value['sourceDigest']:
        raise ValueError('exact source vector digest required')
    for row in value['controllerInputs'] + value['borrowedInputs']:
        if stable(REPO / row['path']) != {k: row[k] for k in ['bytes', 'sha256', 'mode']}:
            raise ValueError('controller/fixture dependency changed')
    if value.get('executionMode') != 'fresh-chart5-startup1-prior-admin4-closed-v2':
        raise ValueError('only explicit startup successor scope accepted')
    prior_admin_source_bridge(value)
    if not source_required:
        return value
    mysql = json.loads((REPO / 'ci/product20/inputs.json').read_text())['mysql']
    catalog = json.loads((REPO / 'ci/backend-fullgraph/image-catalog.json').read_text())
    redis = next(row for row in catalog['officialImages'] if row['role'] == 'redis')
    for role, selected in [('mysql', mysql), ('redis', redis)]:
        if sha(json.dumps(selected, sort_keys=True, separators=(',', ':')).encode()) != value['imagePolicyFieldSHA256'][role]:
            raise ValueError('reviewed image policy selected field differs')
    provider = json.loads((REPO / 'ci/chart-route-build/inputs.json').read_text())
    frame = {key: provider[key] for key in ('sources', 'locks', 'entries')}
    if sha(json.dumps(frame, sort_keys=True, separators=(',', ':')).encode()) != value['chartProviderFrameSHA256']:
        raise ValueError('current chart provider selected frame differs')
    if provider['entries'][:5] != [list(row) for row in BUILDS]:
        raise ValueError('exact five existing chart build recipes required')
    if tree(REPO / 'product/chart') != value['source']:
        raise ValueError('complete formatted chart source differs')
    return value

def go_events(raw, exit_code):
    if exit_code != 0 or not raw or len(raw) > 16 << 20 or not raw.endswith(b'\n'):
        raise ValueError('complete successful Go JSON required')
    rows = [json.loads(line) for line in raw.splitlines()]
    runs, passed, starts, package_pass, footer = [], [], 0, 0, 0
    for row in rows:
        action, test, package = row.get('Action'), row.get('Test'), row.get('Package')
        if action in ('skip', 'fail', 'build-fail'):
            raise ValueError('skip/failure cannot be PASS')
        if test:
            if test not in TESTS or package != PACKAGE or action not in ('run', 'pass', 'output'):
                raise ValueError('unexpected test/subtest/parallel event')
            if action == 'run': runs.append(test)
            if action == 'pass': passed.append(test)
        elif package == PACKAGE:
            if action == 'start': starts += 1
            if action == 'pass': package_pass += 1
            if action == 'output' and re.fullmatch(r'ok\s+' + re.escape(PACKAGE) + r'\s+[0-9]+(?:\.[0-9]+)?s\n', row.get('Output', '')):
                footer += 1
    if (sorted(runs) != sorted(TESTS) or sorted(passed) != sorted(TESTS)
            or starts != 1 or package_pass != 1 or footer != 1
            or rows[-1].get('Action') != 'pass' or rows[-1].get('Package') != PACKAGE):
        raise ValueError('exact4 run/pass/package/footer required')
    return {'actualTopRun': sorted(runs), 'actualTopPASS': sorted(passed), 'subtests': 0,
            'skips': 0, 'packagePASS': True, 'actualFooter': True, 'rawOutputExported': False}

def module_download_plan(go):
    return [go, 'mod', 'download', '-json']


def source_difference(root, expected):
    root = Path(root)
    indexed = {row['path']: row for row in expected}
    changes = []
    for name, before in indexed.items():
        path = root / name
        try:
            after = stable(path)
            kind = 'changed'
        except FileNotFoundError:
            after, kind = None, 'missing'
        except (ValueError, OSError):
            after, kind = None, 'unsafe'
        if after != {key: before[key] for key in ('bytes', 'sha256', 'mode')}:
            changes.append({'path': name, 'kind': kind,
                'before': {key: before[key] for key in ('bytes', 'sha256', 'mode')}, 'after': after})
    added = {'regular-go': 0, 'alias-or-nonregular': 0}
    for path in root.rglob('*.go'):
        if path.relative_to(root).as_posix() not in indexed:
            category = 'alias-or-nonregular' if path.is_symlink() or not path.is_file() else 'regular-go'
            added[category] += 1
    return {'knownChangedCount': len(changes), 'knownChanges': changes[:16],
        'knownProjectionTruncated': len(changes) > 16, 'unknownAddedCategories': added,
        'unknownNamesOrBodiesOrHashesExported': False, 'rawSourceOrPrivatePathsExported': False,
        'diagnosticOnly': True, 'driftStillRejectsRun': True}


def test_frame_limits(inputs):
    limits = {}
    for name in ('service_test.go', 'admin_disable_revocation_test.go'):
        path = 'pkg/common/admindelegation/' + name
        row = next(item for item in inputs['source'] if item['path'] == path)
        raw = (REPO / 'product/chart' / path).read_bytes()
        if len(raw) != row['bytes'] or sha(raw) != row['sha256']:
            raise ValueError('known test source frame boundary differs')
        limits[name] = len(raw.splitlines())
    return limits


def failed_go_observation(raw, limits, code):
    counts = {name: {action: 0 for action in ('run', 'pass', 'fail', 'skip')} for name in TESTS}
    result = {'observationOnly': True, 'wholeInvocationAccepted': False,
        'wrapperExitCode': code if type(code) is int and -128 <= code <= 255 else None,
        'knownTests': counts, 'knownFrames': [], 'completeJSON': False,
        'expectedSelectionOnly': True, 'rawOutputOrValuesExported': False}
    if not isinstance(raw, bytes) or not raw or len(raw) > 16 << 20:
        result['individualPASSObserved'] = []
        return result
    result.update(originalMachineJSON_SHA256=sha(raw), originalMachineJSON_Bytes=len(raw))
    starts, frames, valid = 0, set(), True
    for line in raw.splitlines():
        try:
            value = json.loads(line)
            if not isinstance(value, dict): raise ValueError('object required')
        except (ValueError, UnicodeError):
            valid = False
            break
        if value.get('Package', value.get('ImportPath')) != PACKAGE:
            continue
        action, name = value.get('Action'), value.get('Test')
        if name is not None and not isinstance(name, str):
            result['expectedSelectionOnly'] = False
            continue
        if action == 'start' and not name: starts += 1
        if name and action in ('run', 'pass', 'fail', 'skip'):
            if name in counts:
                counts[name][action] = min(2, counts[name][action] + 1)
            else:
                result['expectedSelectionOnly'] = False
        if (name is None or name in counts) and action in ('output', 'build-output') and isinstance(value.get('Output'), str):
            for text in value['Output'].splitlines()[:32]:
                match = re.match(r'^[ \t]*(?:\./)?(?:pkg/common/admindelegation/)?(service_test\.go|admin_disable_revocation_test\.go):([1-9][0-9]{0,5}):', text)
                if match and 1 <= int(match[2]) <= limits.get(match[1], 0):
                    frames.add((name or '', match[1], int(match[2])))
    result['completeJSON'] = valid and raw.endswith(b'\n')
    result['knownFrames'] = [{'test': name or None, 'basename': file, 'line': line}
                             for name, file, line in sorted(frames)[:8]]
    result['framesTruncated'] = len(frames) > 8
    result['individualPASSObserved'] = [name for name, seen in counts.items()
        if starts == 1 and seen == {'run': 1, 'pass': 1, 'fail': 0, 'skip': 0}]
    return result


def command_observations(private):
    labels = ['go-version', 'module-download', 'test-closure', 'go-test'] + ['build-' + name for _, _, name in BUILDS]
    result = {}
    for label in labels:
        path = private / (label + '.resource.json')
        if not path.is_file(): continue
        try:
            stable(path, 65536)
            value = json.loads(path.read_bytes())
            if not isinstance(value, dict): raise ValueError('resource object required')
            code = value.get('exit_code')
            result[label] = {'exit': code if type(code) is int and -128 <= code <= 255 else None,
                'guardStatus': value.get('status') if value.get('status') in ('passed', 'failed') else 'other'}
        except (ValueError, OSError):
            result[label] = {'exit': None, 'guardStatus': 'unconfirmed'}
    return result


def command(root, label, args, cwd, env, seconds):
    private = root / 'private'
    out, err = private / (label + '.stdout'), private / (label + '.stderr')
    record = private / (label + '.resource.json')
    argv = [sys.executable, '-B', str(REPO / 'ci/group-effects/resource_guard.py'),
        '--cwd', str(cwd), '--allocated-root', str(root), '--max-allocated-mib', '8192',
        '--min-free-mib', '1024', '--poll-interval', '.2', '--timeout', str(seconds),
        '--log', str(private / (label + '.guard.log')), '--record', str(record), '--',
        sys.executable, '-B', str(REPO / 'ci/group-effects/capture.py'),
        '--stdout', str(out), '--stderr', str(err), '--', *map(str, args)]
    result = subprocess.run(argv, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                            timeout=seconds + 45, check=False)
    proof = json.loads(record.read_text())
    if out.stat().st_size > 16 << 20 or err.stat().st_size > 2 << 20:
        raise ValueError('private command output cap exceeded')
    return result.returncode, out.read_bytes(), proof

def toolset(go):
    tools = {'go': go, 'gcc': Path(shutil.which('gcc')).resolve(),
             'docker': Path(shutil.which('docker')).resolve()}
    toolroot = go.parents[1] / 'pkg/tool/linux_amd64'
    tools.update({name: toolroot / name for name in ['compile', 'asm', 'link', 'cgo']})
    return {name: {'path': str(path), **stable(path)} for name, path in tools.items()}

# Inserted into the existing controller after toolset; not a standalone runner.
STARTUP_PACKAGE = 'github.com/OpenIMSDK/chat/pkg/common/chatrpcstart'
STARTUP_NAME = 'TestStartupErrorPreservesCauseAndClosedDiagnostic'
STARTUP_KIND = 'current-chart-five-startup1-prior-admin4-compatible-v2'
PRIOR_REVIEW = 'e22941a1365bd9f89bc371a2fc58bb4c7a219db17533fb22871fcab0e73f232e'
PRIOR_CHART = {'members': 259, 'sha256': '432310bf9869e1960964c84c442e45ffbd85a7e4634b3052bb4e8dd2092ac7cc'}
PRIOR_SOURCE = '40f8e478567a98cb080f107ca5eb80e05580db6b531c9108780f9815ec027647'
PRIOR_RECEIPTS = {'events.json': '98cf718a3d7ed9a9f29e6e4533f656a1e0401ec64c75eb7fb80e9e375da68d4d', 'summary.json': '1eb4721422f4101659f98a91c0852dff495bf4bab9bae25b15c0fa9037c5c9d6', 'source-tools.json': 'e7c7f980256fe07b93e954f2700d25a71d7aa0368018feba4649099a3cf6cf1c', 'cleanup.json': '3364a137647f1e40618938a9ca29f7b61937aa045da1e15b1fc62d5498fe57fd', 'fixture-after.json': 'f6bb7138fd7d0ad1d4f94b59ac20b22d289c953fc3d06b90175b49eef33c7373', 'cleanup-final.json': '3364a137647f1e40618938a9ca29f7b61937aa045da1e15b1fc62d5498fe57fd', 'builds.json': 'f39f43f8dc8e22e1472bd35df78550e9583554bd51a1efc340e00fa4622e0355'}
CHANGED_STARTUP_PATHS = {'tools/component/component.go', 'pkg/common/chatrpcstart/start.go', 'pkg/common/chatrpcstart/startup_error_test.go'}


def prior_admin_source_bridge(inputs):
    prior = inputs['priorAdmin4']
    if (prior['kind'] != 'historical-selective4-not-fresh' or prior['rootReviewSHA256'] != PRIOR_REVIEW
            or prior['head'] != '98d6a73a3b6342bbfaf525f478bc7ccf701ceb77'
            or prior['run'] != 42283432687 or prior['job'] != 128118049210
            or prior['chartSource'] != PRIOR_CHART or prior['sourceDigest'] != PRIOR_SOURCE
            or prior['receiptPins'] != PRIOR_RECEIPTS):
        raise ValueError('exact original historical live4 identity required')
    event = prior['events']
    if (event['actualTopRun'] != sorted(TESTS) or event['actualTopPASS'] != sorted(TESTS)
            or event['subtests'] != 0 or event['skips'] != 0 or event['actualExitCode'] != 0
            or any(event[k] is not True for k in ('packagePASS', 'actualFooter', 'actualResourceGuardPassed'))
            or sha((json.dumps(event, sort_keys=True, indent=2) + '\n').encode()) != prior['receiptPins']['events.json']
            or prior['receiptPins']['events.json'] != '98cf718a3d7ed9a9f29e6e4533f656a1e0401ec64c75eb7fb80e9e375da68d4d'):
        raise ValueError('original four completed events cannot be fabricated')
    current = {r['path']: r for r in inputs['source']}
    if len(current) != len(inputs['source']) or len({r['path'] for r in prior['source']}) != len(prior['source']):
        raise ValueError('unique whole source rows required')
    deltas = prior['sourceDelta']
    if {r['path'] for r in deltas} != CHANGED_STARTUP_PATHS or len(deltas) != len(CHANGED_STARTUP_PATHS):
        raise ValueError('only the exact related startup source delta is admitted')
    for row in deltas:
        if set(row) != {'path', 'before', 'after'} or current.get(row['path']) != row['after']:
            raise ValueError('current source delta body differs')
        if row['before'] is None:
            del current[row['path']]
        else:
            current[row['path']] = row['before']
    reconstructed = [current[k] for k in sorted(current)]
    if (reconstructed != prior['source'] or sha(json.dumps(reconstructed, sort_keys=True, separators=(',', ':')).encode()) != PRIOR_SOURCE
            or any(inputs['chartLocks'][n] != next(r['sha256'] for r in reconstructed if r['path'] == n) for n in ('go.mod', 'go.sum'))):
        raise ValueError('complete inverse source and original locks required')
    chart_rows = [{'path': 'chart/' + r['path'], 'bytes': r['bytes'], 'sha256': r['sha256']} for r in reconstructed]
    if {'members': len(chart_rows), 'sha256': sha(json.dumps(chart_rows, sort_keys=True, separators=(',', ':')).encode())} != PRIOR_CHART:
        raise ValueError('original whole chart provider not reconstructed')
    return {'sourceInverseExact': True, 'oldChartSource': PRIOR_CHART, 'oldSourceDigest': PRIOR_SOURCE,
            'currentChartSource': inputs['chartSource'], 'changedPaths': sorted(CHANGED_STARTUP_PATHS),
            'originalRootReviewSHA256': PRIOR_REVIEW, 'currentAdmin4TestCommands': 0,
            'compatibilityStillRequiresActualClosureAndOriginalTools': True}


def admin_closure_plan(go):
    return [go, 'list', '-race', '-p=1', '-deps', '-test', '-mod=readonly',
            '-json=ImportPath,Name,ForTest,Standard,Module,Error,DepsErrors,Incomplete',
            './pkg/common/admindelegation']


def admin_test_closure(raw, exit_code, go_sum, tools, inputs):
    prior_admin_source_bridge(inputs)
    actual_tools = {n: {k: r[k] for k in ('bytes', 'sha256', 'mode')} for n, r in tools.items()}
    if actual_tools != inputs['priorAdmin4']['toolsBodyMode'] or set(actual_tools) != {'go', 'gcc', 'docker', 'compile', 'asm', 'link', 'cgo'}:
        raise ValueError('original Go and tool bodies/modes required for historical reuse')
    if exit_code != 0 or not raw or len(raw) > 16 << 20 or not raw.endswith(b'\n') or sha(go_sum) != inputs['chartLocks']['go.sum']:
        raise ValueError('complete successful readonly closure and original lock required')
    sums = {}
    for line in go_sum.decode('ascii').splitlines():
        fields = line.split()
        if len(fields) != 3 or not re.fullmatch('h1:[A-Za-z0-9+/]{43}=', fields[2]):
            raise ValueError('original module checksum shape differs')
        key = tuple(fields[:2])
        if key in sums and sums[key] != fields[2]: raise ValueError('ambiguous module checksum')
        sums[key] = fields[2]
    def unique_object(pairs):
        result = {}
        for key, value in pairs:
            if key in result: raise ValueError('duplicate closure metadata field')
            result[key] = value
        return result
    text = raw.decode('utf8'); decoder = json.JSONDecoder(object_pairs_hook=unique_object); at = 0; packages = set(); modules = {}
    root, augmented, wrapper = 0, 0, 0
    blocked = ('github.com/OpenIMSDK/chat/tools/component', STARTUP_PACKAGE)
    while at < len(text):
        while at < len(text) and text[at].isspace(): at += 1
        if at == len(text): break
        value, at = decoder.raw_decode(text, at)
        if (type(value) is not dict or set(value) - {'ImportPath', 'Name', 'ForTest', 'Standard', 'Module', 'Error', 'DepsErrors', 'Incomplete'}
                or value.get('Error') is not None or value.get('DepsErrors') not in (None, [])
                or value.get('Incomplete', False) is not False or type(value.get('Standard', False)) is not bool):
            raise ValueError('error or incomplete package cannot prove closure')
        name, for_test = value.get('ImportPath'), value.get('ForTest', '')
        if type(name) is not str or type(for_test) is not str or for_test not in ('', PACKAGE):
            raise ValueError('unexpected test provenance')
        match = re.fullmatch(r'([A-Za-z0-9_./~-]+)(?: \[(' + re.escape(PACKAGE + '.test') + r')\])?', name)
        if match is None or name in packages or len(packages) >= 4096:
            raise ValueError('closed unique package/test wrapper set required')
        base = match[1]
        if any(base == p or base == p + '.test' or base.startswith(p + '/') or for_test == p for p in blocked):
            raise ValueError('startup source intersects original live4 test closure')
        if match[2] and for_test != PACKAGE: raise ValueError('augmented test package has no exact owner')
        packages.add(name); root += name == PACKAGE
        augmented += name == PACKAGE + ' [' + PACKAGE + '.test]'
        wrapper += name == PACKAGE + '.test' and value.get('Name') == 'main'
        if value.get('Standard') is True:
            if value.get('Module') is not None: raise ValueError('standard package has unexpected module')
            continue
        module = value.get('Module')
        # Go's synthetic test-main may have no Module. Its exact import path,
        # owner and the separately present ordinary/augmented roots bind it.
        if base == PACKAGE + '.test' and value.get('Name') == 'main' and module is None and not match[2]:
            continue
        if type(module) is not dict or module.get('Replace') is not None: raise ValueError('actual versioned module required; replacements refused')
        path, version = module.get('Path'), module.get('Version', '')
        if module.get('Main') is True:
            if path != 'github.com/OpenIMSDK/chat' or version or not (base.startswith(path + '/') or base == PACKAGE + '.test'):
                raise ValueError('main module owner differs')
            continue
        if type(path) is not str or type(version) is not str or not version or not (base == path or base.startswith(path + '/')):
            raise ValueError('actual external module/version missing')
        body_sum, mod_sum = sums.get((path, version)), sums.get((path, version + '/go.mod'))
        if not body_sum or not mod_sum or module.get('Sum', body_sum) != body_sum or module.get('GoModSum', mod_sum) != mod_sum:
            raise ValueError('external version or observed checksum not in original lock')
        row = {'path': path, 'version': version, 'sum': body_sum, 'goModSum': mod_sum}
        if path in modules and modules[path] != row: raise ValueError('multiple external module versions refused')
        modules[path] = row
    if root != 1 or augmented != 1 or wrapper != 1 or not modules:
        raise ValueError('complete original root/augmented/test-main and external closure required')
    public_modules = [modules[n] for n in sorted(modules)]
    return {'accepted': True, 'root': PACKAGE, 'race': True, 'CGO_ENABLED': '1', 'packageCount': len(packages),
            'augmentedRootCount': augmented, 'testMainCount': wrapper, 'externalModuleCount': len(modules),
            'lockedExternalModulesSHA256': sha(json.dumps(public_modules, sort_keys=True, separators=(',', ':')).encode()),
            'startupPackagesAbsent': True, 'sourceInverseExact': True, 'originalToolBodiesExact': True,
            'rawMetadataBytes': len(raw), 'rawMetadataSHA256': sha(raw), 'exit': 0,
            'currentAdmin4TestCommands': 0, 'historicalSelectiveFactsOnly': True, 'rawPathsErrorsOrModuleDirsExported': False}


def startup_go_events(raw, code):
    if code != 0 or not raw or len(raw) > 16 << 20 or not raw.endswith(b'\n'):
        raise ValueError('actual startup1 machine completion required')
    rows = [json.loads(line) for line in raw.splitlines()]; run = passed = starts = package_pass = footer = 0
    for row in rows:
        action, test, package = row.get('Action'), row.get('Test'), row.get('Package')
        if action in ('skip', 'fail', 'build-fail'): raise ValueError('startup1 skip/failure refused')
        if test:
            if test != STARTUP_NAME or package != STARTUP_PACKAGE or action not in ('run', 'pass', 'output'):
                raise ValueError('only exact startup1 without subtests allowed')
            run += action == 'run'; passed += action == 'pass'
        elif package == STARTUP_PACKAGE:
            starts += action == 'start'; package_pass += action == 'pass'
            footer += action == 'output' and bool(re.fullmatch(r'ok\s+' + re.escape(STARTUP_PACKAGE) + r'\s+[0-9]+(?:\.[0-9]+)?s\n', row.get('Output', '')))
    if (run, passed, starts, package_pass, footer) != (1, 1, 1, 1, 1) or rows[-1].get('Action') != 'pass' or rows[-1].get('Package') != STARTUP_PACKAGE:
        raise ValueError('startup1 exact run/pass/package/footer required')
    return {'actualTopRun': [STARTUP_NAME], 'actualTopPASS': [STARTUP_NAME], 'package': STARTUP_PACKAGE,
            'subtests': 0, 'skips': 0, 'packagePASS': True, 'actualFooter': True, 'actualExitCode': 0,
            'originalMachineJSON_SHA256': sha(raw), 'originalMachineJSON_Bytes': len(raw), 'rawOutputExported': False}


def build_only_cleanup(root):
    intent = root / 'private/producer-intent.json'
    expected = {'cohort': COHORT, 'head': os.environ['GITHUB_SHA'], 'run': os.environ['GITHUB_RUN_ID'],
                'attempt': os.environ['GITHUB_RUN_ATTEMPT'], 'uid': os.getuid(), 'gid': os.getgid()}
    if (root / 'private/fixture-state.json').exists(): raise ValueError('new build-only scope cannot adopt a live fixture')
    if intent.exists():
        stable(intent, 4096)
        if json.loads(intent.read_bytes()) != expected: raise ValueError('original build-only allocation intent differs')
    return {'accepted': True, 'ownedAllocationExact': True, 'SQLRedisFixturesStarted': 0,
            'containerCleanupNotApplicable': True, 'noForeignResourcesAdopted': True,
            'historicalSQLRedisCleanupIsNotCurrentCleanup': True}


def public_controller_failure_frames(error, inputs):
    allowed = {'ci/admin-session-targeted/run.py', 'ci/admin-session-targeted/fixture.py',
               'ci/chart-route-build/run.py', 'ci/backend-fullgraph/resources.py',
               'ci/group-ordinary/mysql_fixture.py'}
    pinned = {row['path']: row for row in inputs['controllerInputs'] + inputs['borrowedInputs']
              if row['path'] in allowed}
    filenames = {str(REPO / name): name for name in pinned}
    if Path.cwd() == REPO: filenames.update({name: name for name in pinned})
    trace = error.__traceback__; chain = []; seen = set()
    while trace is not None and len(chain) < 128 and id(trace) not in seen:
        seen.add(id(trace)); chain.append(trace); trace = trace.tb_next
    frames = []; keys = set(); sources = {}; unconfirmed = False; extra = False
    for item in reversed(chain):
        name = filenames.get(item.tb_frame.f_code.co_filename); line = item.tb_lineno
        if name is None or type(line) is not int or line < 1: continue
        if name not in sources:
            row = pinned[name]; path = REPO / name
            try:
                expected = {key: row[key] for key in ('bytes', 'sha256', 'mode')}
                before = stable(path, 2 << 20)
                with path.open('rb') as stream: raw = stream.read((2 << 20) + 1)
                if before != expected or stable(path, 2 << 20) != expected or len(raw) != row['bytes'] or sha(raw) != row['sha256']:
                    raise ValueError('public controller source pin differs')
                sources[name] = len(raw.splitlines())
            except BaseException:
                sources[name] = 0; unconfirmed = True
        if line > sources[name] or (name, line) in keys: continue
        keys.add((name, line))
        if len(frames) == 8: extra = True; continue
        frames.append({'path': name, 'line': line, 'sourceSHA256': pinned[name]['sha256']})
    return {'frames': frames, 'tracebackScanTruncated': trace is not None, 'framesTruncated': extra,
            'pinnedSourceUnconfirmed': unconfirmed, 'rawErrorArgsTextPrivatePathsOrLogsExported': False}


def run_startup_build(root, inputs, input_sha):
    output = REPO / 'ci-results/admin-session'; output.mkdir(mode=0o700)
    private = root / 'private'; private.mkdir(mode=0o700)
    write(private / 'producer-intent.json', {'cohort': COHORT, 'head': os.environ['GITHUB_SHA'],
        'run': os.environ['GITHUB_RUN_ID'], 'attempt': os.environ['GITHUB_RUN_ATTEMPT'], 'uid': os.getuid(), 'gid': os.getgid()})
    builder = fixture.module('ci/chart-route-build/run.py', 'startup_five_original_elf_audit')
    failure = None; source = go = before_tools = None; builds = []; resources = {}; events = prior = None
    phase = 'source-tools'; post = False
    try:
        source = root / 'source/chart'; source.mkdir(mode=0o700, parents=True)
        for row in inputs['source']:
            p = source / row['path']; p.parent.mkdir(parents=True, exist_ok=True)
            fixture.images.private_write(p, (REPO / 'product/chart' / row['path']).read_bytes()); p.chmod(row['mode'])
        if tree(source) != inputs['source']: raise ValueError('current materialized source differs')
        bridge = prior_admin_source_bridge(inputs); write(output / 'source-bridge.json', bridge)
        go = Path(shutil.which('go')).resolve(); before_tools = toolset(go)
        env = {'PATH': str(go.parent) + ':/usr/bin:/bin', 'HOME': os.environ['HOME'], 'GOTOOLCHAIN': 'local',
            'GOWORK': 'off', 'GOENV': 'off', 'GOAUTH': 'off', 'GOMAXPROCS': '2', 'GOMEMLIMIT': '900MiB',
            'CGO_ENABLED': '1', 'CC': before_tools['gcc']['path'], 'GOCACHE': str(root / 'go-build'),
            'GOMODCACHE': str(root / 'go-mod'), 'GOPATH': str(root / 'go-path'), 'TMPDIR': str(root / 'tmp'),
            'GOFLAGS': '-mod=readonly', 'GOPROXY': 'https://proxy.golang.org', 'GOSUMDB': 'sum.golang.org'}
        (root / 'tmp').mkdir(mode=0o700)
        code, raw, guard = command(root, 'go-version', [go, 'version'], source, env, 15)
        if code != 0 or raw.decode().strip() != 'go version go1.27.2 linux/amd64' or guard.get('status') != 'passed' or guard.get('exit_code') != 0: raise ValueError('fixed Go1.27.2 and actual resource proof required')
        phase = 'module-download'
        code, _, guard = command(root, 'module-download', module_download_plan(go), source, env, 600)
        if code != 0 or guard.get('status') != 'passed' or guard.get('exit_code') != 0 or tree(source) != inputs['source']:
            raise ValueError('locked module download/source failed')
        env.update(GOPROXY='off', GOSUMDB='off')
        phase = 'prior-admin4-test-closure'
        code, raw, guard = command(root, 'test-closure', admin_closure_plan(go), source, env, 600)
        closure = admin_test_closure(raw, code, (source / 'go.sum').read_bytes(), before_tools, inputs)
        if guard.get('status') != 'passed' or guard.get('exit_code') != 0: raise ValueError('actual closure resource proof required')
        closure['resourceSHA256'] = sha((private / 'test-closure.resource.json').read_bytes()); write(output / 'test-closure.json', closure)
        original = inputs['priorAdmin4']
        prior = {'kind': 'historical-selective4-source-compatible-not-fresh', 'originalHead': original['head'],
            'originalRun': original['run'], 'originalJob': original['job'], 'originalChartSource': PRIOR_CHART,
            'originalSourceDigest': PRIOR_SOURCE, 'originalRootReviewSHA256': PRIOR_REVIEW,
            'originalReceiptPins': original['receiptPins'], 'sourceBridgeSHA256': sha((output / 'source-bridge.json').read_bytes()),
            'closureReceiptSHA256': sha((output / 'test-closure.json').read_bytes()), 'compatibilityAccepted': True,
            'currentAdmin4TestCommands': 0, 'currentFreshAdmin4PASS': False}
        write(output / 'prior-admin4-reuse.json', prior)
        phase = 'exact-startup-one'
        test_env = dict(env, CGO_ENABLED='0', GOOS='linux', GOARCH='amd64')
        args = [go, 'test', '-mod=readonly', '-p=1', '-parallel=1', '-count=1', '-json', '-vet=off', '-timeout=180s',
                '-run', '^' + STARTUP_NAME + '$', './pkg/common/chatrpcstart']
        code, raw, guard = command(root, 'go-test', args, source, test_env, 600)
        if guard.get('status') != 'passed' or guard.get('exit_code') != 0: raise ValueError('startup1 resource/exit not passed')
        events = startup_go_events(raw, code); events.update(actualResourceGuardPassed=True,
            resourceSHA256=sha((private / 'go-test.resource.json').read_bytes()))
        write(output / 'events.json', events)
        phase = 'five-chart-builds'
        if builder.ENTRIES[:5] != BUILDS: raise ValueError('original five build entries differ')
        current = builder.vector(builder.sources(source.parent, 'chart'))
        if current != inputs['chartSource']: raise ValueError('actual fresh chart provider differs')
        product_dir = root / 'products'; product_dir.mkdir(mode=0o700)
        build_env = dict(test_env, LUOBO_EMAIL_INTEGRATION='0')
        for module, package, name in BUILDS:
            binary = product_dir / name
            args = [go, 'build', '-mod=readonly', '-p=1', '-trimpath', '-buildvcs=false', '-ldflags=-s -w', '-o', binary, package]
            code, _, guard = command(root, 'build-' + name, args, source, build_env, 600)
            if code != 0 or guard.get('status') != 'passed' or guard.get('exit_code') != 0: raise ValueError('fresh chart build failed')
            builds.append(builder.binary_metadata(binary, module, package, current))
            resources[name] = {'exit': code, 'status': guard['status'], 'resourceSHA256': sha((private / ('build-' + name + '.resource.json')).read_bytes())}
        write(output / 'builds.json', {'freshChartBuilds': builds, 'buildResources': resources, 'freshRouteBuilds': 0,
            'parserOracleOrFullSuiteExecuted': False, 'runtimeOrP20Accepted': False})
        phase = 'post-source-tools'; verify(input_sha)
    except BaseException as error:
        failure = {'phase': phase, 'class': type(error).__name__ if type(error).__name__ in ['ValueError', 'OSError', 'TimeoutExpired', 'InterruptedError', 'JSONDecodeError'] else 'other', 'rawErrorExported': False}
        try: failure['publicControllerFrames'] = public_controller_failure_frames(error, inputs)
        except BaseException:
            failure['publicControllerFrames'] = {'frames': [], 'projectionUnconfirmed': True,
                'rawErrorArgsTextPrivatePathsOrLogsExported': False}
    finally:
        if source is not None:
            try: write(output / 'source-difference.json', source_difference(source, inputs['source']))
            except (ValueError, OSError): write(output / 'source-difference.json', {'unconfirmed': True, 'driftStillRejectsRun': True})
        if before_tools is not None:
            try: source_exact = tree(source) == inputs['source']
            except (ValueError, OSError): source_exact = False
            try: tools_exact = toolset(go) == before_tools
            except (ValueError, OSError): tools_exact = False
            post = source_exact and tools_exact
            write(output / 'source-tools.json', {'sourceMembers': len(inputs['source']), 'sourceDigest': inputs['sourceDigest'],
                'sourceAndLocksPostExact': source_exact, 'toolsBeforeAndAfterExact': tools_exact, 'tools': before_tools,
                'GoVersion': 'go1.27.2 linux/amd64', 'sourceOrPrivateLogsExported': False})
            if not post: failure = failure or {'phase': 'post-source-tools', 'class': 'ValueError', 'rawErrorExported': False}
        try: cleanup = build_only_cleanup(root)
        except BaseException: cleanup = {'accepted': False, 'cleanupUnconfirmed': True}
        write(output / 'cleanup.json', cleanup)
    accepted = failure is None and post and cleanup.get('accepted') is True and events is not None and prior is not None and len(builds) == 5
    if accepted:
        final = [builder.binary_metadata(root / 'products' / n, m, p, inputs['chartSource']) for m, p, n in BUILDS]
        if final != builds or {p.name for p in (root / 'products').iterdir()} != {n for _, _, n in BUILDS} or sum(r['bytes'] for r in final) > builder.TOTAL_BINARY_LIMIT:
            raise ValueError('final five ELF body/output set differs')
        write(root / 'products/manifest.json', {'schema': 2, 'kind': STARTUP_KIND, 'head': os.environ['GITHUB_SHA'],
            'run': os.environ['GITHUB_RUN_ID'], 'attempt': os.environ['GITHUB_RUN_ATTEMPT'], 'source': inputs['chartSource'],
            'locks': inputs['chartLocks'], 'binaries': final, 'buildResources': resources, 'freshChartBuilds': 5,
            'freshRouteBuilds': 0, 'sourceToolHelperPostExact': True, 'GoVersion': 'go1.27.2 linux/amd64',
            'startupTest': events, 'priorAdmin4': prior, 'currentAdmin4TestCommands': 0, 'SQLRedisFixturesStarted': 0,
            'parserOracleExecuted': False, 'ReadyOrP20OrDeploymentAccepted': False, 'outerResourceGuardStillRequired': True})
    write(output / 'summary.json', {'accepted': accepted, 'cohort': COHORT, 'head': os.environ['GITHUB_SHA'],
        'actualCurrentStartupTopPASS': 1 if events is not None else 0, 'currentAdmin4TestCommands': 0,
        'historicalAdmin4Compatible': prior is not None, 'historicalAdmin4IsNotFreshPASS': True,
        'freshChartBuilds': len(builds), 'freshRouteBuilds': 0, 'SQLRedisFixturesStarted': 0,
        'failure': failure, 'commands': command_observations(private), 'cleanupAccepted': cleanup.get('accepted') is True,
        'HTTPOrProduct20OrFullRegressionAccepted': False, 'outerResourceGuardStillRequired': True})
    return 0 if accepted else 1


def admit(root):
    if (any(os.environ.get(k) != v for k, v in {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux'}.items())
            or os.environ.get('ADMIN_SESSION_COHORT') != COHORT
            or sys.platform != 'linux' or os.getuid() == 0 or os.getgid() == 0
            or not re.fullmatch('[0-9]+', os.environ.get('GITHUB_RUN_ID', ''))
            or not re.fullmatch('[0-9]+', os.environ.get('GITHUB_RUN_ATTEMPT', ''))
            or not re.fullmatch('[0-9a-f]{40}', os.environ.get('GITHUB_SHA', ''))):
        raise ValueError('disposable ordinary-user hosted Linux only')
    wanted = Path(os.environ['RUNNER_TEMP']) / ('admin-session-' + os.environ['GITHUB_RUN_ID'] + '-' + os.environ['GITHUB_RUN_ATTEMPT'])
    if root != wanted or root.resolve() != root or stat.S_IMODE(root.stat().st_mode) != 0o700 or root.stat().st_uid != os.getuid():
        raise ValueError('exact fresh owned allocation required')


def run(root, inputs, input_sha):
    output = REPO / 'ci-results/admin-session'
    output.mkdir(mode=0o700)
    live = fixture.Fixture(root, inputs['images'])
    failure, events, cleanup, before_tools, source, go = None, None, None, None, None, None
    builds, source_post, build_resources = [], False, {}
    observed_passes = []
    builder = fixture.module('ci/chart-route-build/run.py', 'admin_session_five_elf_audit')
    phase = 'fixture'
    try:
        ready = live.prepare(); write(output / 'fixture-ready.json', ready)
        phase = 'source-tools'
        source = root / 'source/chart'; source.mkdir(mode=0o700, parents=True)
        for row in inputs['source']:
            path = source / row['path']; path.parent.mkdir(parents=True, exist_ok=True)
            fixture.images.private_write(path, (REPO / 'product/chart' / row['path']).read_bytes())
            path.chmod(row['mode'])
        if tree(source) != inputs['source']: raise ValueError('materialized source differs')
        go = Path(shutil.which('go')).resolve(); before_tools = toolset(go)
        env = {'PATH': str(go.parent) + ':/usr/bin:/bin', 'HOME': os.environ['HOME'],
            'GOTOOLCHAIN': 'local', 'GOWORK': 'off', 'GOENV': 'off', 'GOAUTH': 'off',
            'GOMAXPROCS': '2', 'GOMEMLIMIT': '900MiB', 'CGO_ENABLED': '1',
            'CC': before_tools['gcc']['path'], 'GOCACHE': str(root / 'go-build'),
            'GOMODCACHE': str(root / 'go-mod'), 'GOPATH': str(root / 'go-path'),
            'TMPDIR': str(root / 'tmp'), 'GOFLAGS': '-mod=readonly',
            'GOPROXY': 'https://proxy.golang.org', 'GOSUMDB': 'sum.golang.org'}
        (root / 'tmp').mkdir(mode=0o700)
        code, raw, version_guard = command(root, 'go-version', [go, 'version'], source, env, 15)
        if code != 0 or raw.decode().strip() != 'go version go1.27.2 linux/amd64':
            raise ValueError('fixed Go1.27.2 required')
        phase = 'module-download'
        code, _, guard = command(root, 'module-download', module_download_plan(go), source, env, 600)
        if code != 0 or guard.get('status') != 'passed' or guard.get('exit_code') != 0:
            raise ValueError('locked module download failed')
        if tree(source) != inputs['source']: raise ValueError('module source/locks changed')
        env.update(GOPROXY='off', GOSUMDB='off', LUOBO_INTEGRATION_MYSQL_DSN_FILE=str(live.private / 'credentials/mysql.dsn'),
                   LUOBO_TEST_REDIS_ADDRESS='127.0.0.1:' + live.state['containers']['redis']['port'])
        phase = 'exact-four-tests'
        live.check()
        args = [go, 'test', '-mod=readonly', '-race', '-p=1', '-parallel=1', '-count=1', '-json',
                '-vet=off', '-timeout=180s', '-run', '^(' + '|'.join(TESTS) + ')$', './pkg/common/admindelegation']
        code, raw, guard = command(root, 'go-test', args, source, env, 600)
        if code != 0 or guard.get('status') != 'passed' or guard.get('exit_code') != 0:
            observation = failed_go_observation(raw, test_frame_limits(inputs), code)
            observed_passes = observation['individualPASSObserved']
            write(output / 'events-observation.json', observation)
        if guard.get('status') != 'passed' or guard.get('exit_code') != 0:
            raise ValueError('actual Go resource/exit not passed')
        events = go_events(raw, code)
        events.update(originalMachineJSON_SHA256=sha(raw), originalMachineJSON_Bytes=len(raw), actualExitCode=code,
            actualResourceGuardPassed=True, resourceSHA256=sha((live.private / 'go-test.resource.json').read_bytes()))
        write(output / 'events.json', events)
        write(output / 'fixture-after.json', live.residuals())
        phase = 'five-chart-builds'
        if builder.ENTRIES[:5] != BUILDS:
            raise ValueError('original five entries differ')
        current_provider = builder.vector(builder.sources(source.parent, 'chart'))
        if current_provider != inputs['chartSource']:
            raise ValueError('actual current259 provider differs')
        product_dir = root / 'products'; product_dir.mkdir(mode=0o700)
        build_env = {k: v for k, v in env.items() if not k.startswith('LUOBO_')}
        build_env.update(CGO_ENABLED='0', GOOS='linux', GOARCH='amd64', LUOBO_EMAIL_INTEGRATION='0')
        for module, package, name in BUILDS:
            binary = product_dir / name
            argv = [go, 'build', '-mod=readonly', '-p=1', '-trimpath', '-buildvcs=false',
                    '-ldflags=-s -w', '-o', binary, package]
            code, _, guard = command(root, 'build-' + name, argv, source, build_env, 600)
            if code != 0 or guard.get('status') != 'passed' or guard.get('exit_code') != 0:
                raise ValueError('fresh selected chart build failed')
            builds.append(builder.binary_metadata(binary, module, package, current_provider))
            build_resources[name] = {'exit': code, 'status': guard['status'],
                'resourceSHA256': sha((live.private / ('build-' + name + '.resource.json')).read_bytes())}
        write(output / 'builds.json', {'freshChartBuilds': builds, 'buildResources': build_resources, 'freshRouteBuilds': 0,
              'parserOracleOrFullSuiteExecuted': False, 'runtimeOrP20Accepted': False})
        phase = 'post-source-tools'
        verify(input_sha)
    except BaseException as error:
        failure = {'phase': phase, 'class': type(error).__name__ if type(error).__name__ in ['ValueError', 'OSError', 'TimeoutExpired', 'InterruptedError', 'JSONDecodeError'] else 'other', 'rawErrorExported': False}
    finally:
        if source is not None:
            try: write(output / 'source-difference.json', source_difference(source, inputs['source']))
            except (ValueError, OSError):
                write(output / 'source-difference.json', {'diagnosticOnly': True, 'unconfirmed': True,
                      'rawSourceOrPrivatePathsExported': False, 'driftStillRejectsRun': True})
        if before_tools is not None:
            try: source_exact = tree(source) == inputs['source']
            except (ValueError, OSError): source_exact = False
            try: tools_exact = toolset(go) == before_tools
            except (ValueError, OSError): tools_exact = False
            source_post = source_exact
            write(output / 'source-tools.json', {'sourceMembers': len(inputs['source']),
                'sourceDigest': inputs['sourceDigest'], 'sourceAndLocksPostExact': source_exact,
                'toolsBeforeAndAfterExact': tools_exact, 'tools': before_tools,
                'GoVersion': 'go1.27.2 linux/amd64', 'sourceOrPrivateLogsExported': False})
            if not source_exact or not tools_exact:
                failure = failure or {'phase': 'post-source-tools', 'class': 'ValueError', 'rawErrorExported': False}
        partial = live.private / 'go-test.stdout'
        if events is None and not (output / 'events-observation.json').exists() and partial.is_file():
            try:
                stable(partial, 16 << 20)
                observation = failed_go_observation(partial.read_bytes(), test_frame_limits(inputs), None)
                observed_passes = observation['individualPASSObserved']
                write(output / 'events-observation.json', observation)
            except (ValueError, OSError): pass
        commands = command_observations(live.private)
        try: cleanup = live.cleanup()
        except BaseException: cleanup = {'accepted': False, 'cleanupUnconfirmed': True, 'rawErrorExported': False}
        write(output / 'cleanup.json', cleanup)
    accepted = failure is None and cleanup.get('accepted') is True and events is not None and len(builds) == 5
    if accepted:
        product_dir = root / 'products'
        final = [builder.binary_metadata(product_dir / name, module, package, inputs['chartSource'])
                 for module, package, name in BUILDS]
        if (final != builds or {p.name for p in product_dir.iterdir()} != {name for _, _, name in BUILDS}
                or sum(row['bytes'] for row in final) > builder.TOTAL_BINARY_LIMIT or not source_post):
            raise ValueError('final five current ELF bodies or source post differ')
        write(product_dir / 'manifest.json', {'schema': 1, 'kind': 'current259-five-chart-build-only-v1',
            'head': os.environ['GITHUB_SHA'], 'run': os.environ['GITHUB_RUN_ID'],
            'attempt': os.environ['GITHUB_RUN_ATTEMPT'], 'source': inputs['chartSource'],
            'locks': inputs['chartLocks'], 'binaries': final, 'buildResources': build_resources, 'freshChartBuilds': 5, 'freshRouteBuilds': 0,
            'sourceToolHelperPostExact': True, 'GoVersion': 'go1.27.2 linux/amd64',
            'selectedLive4Accepted': True, 'SQLRedisOriginalOwnerCleanupAccepted': True,
            'parserOracleExecuted': False, 'ReadyOrP20OrDeploymentAccepted': False,
            'outerResourceGuardStillRequired': True})
    write(output / 'summary.json', {'accepted': accepted, 'cohort': COHORT, 'head': os.environ['GITHUB_SHA'],
        'actualTopPASS': 4 if events is not None else 0, 'freshChartBuilds': len(builds), 'freshRouteBuilds': 0,
        'subtests': 0, 'skipsAccepted': 0,
        'failure': failure, 'commands': commands, 'observedIndividualPASS': observed_passes,
        'individualFactsAreNotWholePackagePASS': True, 'cleanupAccepted': cleanup.get('accepted') is True,
        'fixtureRuntimeOnly': True, 'IMExternalCallsStubbed': True,
        'HTTPOrProduct20OrFullRegressionAccepted': False, 'outerResourceGuardStillRequired': True})
    return 0 if accepted else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--mode', choices=['verify', 'run', 'cleanup'], required=True)
    parser.add_argument('--inputs-sha256', required=True)
    parser.add_argument('--work', type=Path)
    os.umask(0o077)
    args = parser.parse_args(); inputs = verify(args.inputs_sha256, source_required=args.mode != 'cleanup')
    if args.mode == 'verify': print('Admin-session exact source and controller inputs bound.'); return 0
    admit(args.work)
    if args.mode == 'cleanup':
        result = build_only_cleanup(args.work)
        output = REPO / 'ci-results/admin-session'; output.mkdir(mode=0o700, exist_ok=True)
        write(output / 'cleanup-final.json', result)
        return 0 if result.get('accepted') is True else 1
    def interrupted(_signal, _frame): raise InterruptedError('owned targeted run interrupted')
    signal.signal(signal.SIGTERM, interrupted); signal.signal(signal.SIGINT, interrupted)
    return run_startup_build(args.work, inputs, args.inputs_sha256)

if __name__ == '__main__':
    try: raise SystemExit(main())
    except (ValueError, OSError, KeyError, subprocess.SubprocessError, json.JSONDecodeError):
        print('Admin-session targeted operation NOT PASS; private diagnostics retained.', file=sys.stderr)
        raise SystemExit(2)
