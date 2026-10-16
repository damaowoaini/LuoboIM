#!/usr/bin/env python3
"""Isolated source1099 full26-package validation; no ordinary runtime adoption."""
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

REPO = Path(__file__).resolve().parents[2]
INPUTS = REPO / 'ci/group-ordinary/inputs.json'
SCOPE = 'ordinary_source1099_full26_packages_no_runtime_adoption'
PREFIX = 'github.com/openimsdk/open-im-server/v3/'
MONGO = 'mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration'
REDIS = 'redis://127.0.0.1:36379/15'
PACKAGES = {'internal/groupeffects', 'internal/groupdelivery', 'internal/push/offlinepush/fcm', 'internal/rpc/group', 'internal/groupcleanup', 'pkg/common/startrpc', 'internal/api', 'internal/groupauthority', 'pkg/common/db/controller', 'internal/tools', 'internal/push/offlinepush', 'internal/rpc/msg', 'pkg/common/cmd', 'internal/msgtransfer', 'pkg/common/db/unrelation', 'internal/retention', 'cmd/openim-rpc/openim-rpc-group', 'pkg/common/db/table/relation', 'pkg/common/db/mgo', 'pkg/common/db/cache', 'internal/push', 'internal/messagedelivery', 'pkg/msgprocessor', 'cmd/tools/ordinary-device-receipts', 'internal/push/offlinepush/aliasvendor', 'cmd/tools/ordinary-alias-receipts'}
LEGACY_SKIPS = {
    'pkg/common/db/controller': {'Test_BatchInsertChat2DB', 'Test_Insert', 'Test_Revoke',
                                 'Test_FindBySeq', 'TestName'},
    'internal/msgtransfer': {'TestActualKafkaMongoFailureRetainsOffsetAndCacheUntilRecovery'},
    'pkg/common/startrpc': {'TestStart'},
}


def sha(body):
    return hashlib.sha256(body).hexdigest()


def name(value):
    p = PurePosixPath(value)
    if (not value or p.is_absolute() or str(p) != value or '\\' in value
            or any(x in ('', '.', '..') for x in value.split('/'))):
        raise ValueError('noncanonical input path')
    return value


def regular(path):
    if path.resolve() != path or not stat.S_ISREG(path.lstat().st_mode):
        raise ValueError('canonical regular input required')
    return path.read_bytes()


def pinned(row):
    body = regular(REPO / name(row['path']))
    if len(body) != row['bytes'] or sha(body) != row['sha256']:
        raise ValueError('input identity mismatch')
    return body


def rows(bodies):
    return [{'path':p, 'bytes':len(b), 'sha256':sha(b)}
            for p, b in sorted(bodies.items())]


def digest(items):
    return sha(b''.join(r['path'].encode() + b'\0' + r['sha256'].encode() + b'\0'
                        for r in sorted(items, key=lambda r: r['path'])))


def load_parent(row):
    pinned(row)
    spec = importlib.util.spec_from_file_location('ordinary_accepted_stage3', REPO / row['path'])
    parent = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(parent)
    return parent


def apply_overlay(c, bodies):
    prefix = 'ci/candidates/group-ordinary/source/'
    seen = set()
    for row in c['overlay']:
        target = name(row['target'])
        expected_before = sha(bodies[target]) if target in bodies else None
        if (not target.startswith('server/') or not target.endswith('.go')
                or row['path'] != prefix + target or row['mode'] != '0o644'
                or target in seen or row['beforeSHA256'] != expected_before):
            raise ValueError('foreign/duplicate/wrong-parent source overlay')
        path = REPO / row['path']
        if stat.S_IMODE(path.lstat().st_mode) != 0o644:
            raise ValueError('source overlay mode differs')
        seen.add(target)
        bodies[target] = pinned(row)
    actual = {str(p.relative_to(REPO)) for p in (REPO / prefix).rglob('*')
              if p.is_file() or p.is_symlink()}
    expected = {prefix + target for target in seen}
    if actual != expected:
        raise ValueError('extra/missing source overlay file')
    return bodies


def exact_delta(parent, bodies):
    if set(parent) - set(bodies):
        raise ValueError('old source removed')
    result = []
    for path, body in sorted(bodies.items()):
        before = sha(parent[path]) if path in parent else None
        if before == sha(body):
            continue
        # All accepted parent test bodies remain immutable. The former nine
        # retired SendAll draft tests were never in source1023, not removed here.
        if path in parent and path.endswith('_test.go'):
            raise ValueError('accepted old test body changed')
        result.append({'target':path, 'beforeSHA256':before, 'sha256':sha(body), 'bytes':len(body)})
    return result


def verify():
    c = json.loads(regular(INPUTS))
    if (c['schema'] != 1 or c['scope'] != SCOPE or c['effective_count'] != 1099
            or c['parent_count'] != 1023 or c['expected_top_source_inventory'] != 371
            or c['expected_required_pass'] != 364 or c['allowed_legacy_skip_count'] != 7
            or c['parent_runner']['path'] != 'ci/group-stage3/run.py'
            or c['parent_inputs']['path'] != 'ci/group-stage3/inputs.json'
            or len(c['overlay']) != 93):
        raise ValueError('exact source1099 full-package contract required')
    for row in c['dependencies']:
        pinned(row)
    pinned(c['parent_inputs'])
    accepted = load_parent(c['parent_runner'])
    parent, old = accepted.verify()
    if len(old) != 1023 or parent['effective_digest'] != c['parent_digest']:
        raise ValueError('accepted source1023 differs')
    bodies = apply_overlay(c, dict(old))
    if (len(bodies) != 1099 or rows(bodies) != c['effective_source']
            or digest(rows(bodies)) != c['effective_digest']
            or exact_delta(old, bodies) != c['delta']):
        raise ValueError('complete source or before delta differs')
    if set(c['packages']) != PACKAGES or len(PACKAGES) != 26:
        raise ValueError('complete26package contract required')
    if sum(len(s['top_tests']) for s in c['packages'].values()) != 371:
        raise ValueError('full371 source-name inventory required')
    for package, spec in c['packages'].items():
        if name(package) != package or spec['full_package'] is not True:
            raise ValueError('full canonical package required')
        actual = []
        for target, body in bodies.items():
            if str(PurePosixPath(target).parent) == 'server/' + package and target.endswith('_test.go'):
                actual.extend(re.findall(rb'^func (Test\w+)\(t \*testing.T\)', body, re.M))
        actual = sorted(x.decode() for x in actual)
        if actual != spec['top_tests'] or len(actual) != len(set(actual)):
            raise ValueError('full package test inventory differs')
        if spec['allowed_legacy_skips'] != sorted(LEGACY_SKIPS.get(package, set())):
            raise ValueError('foreign skip allowance')
        if package in parent['packages'] and not set(parent['packages'][package]['top_tests']) <= set(actual):
            raise ValueError('accepted old test name disappeared')
    fcm = c['packages']['internal/push/offlinepush/fcm']
    device = [n for n in c['packages']['internal/messagedelivery']['top_tests'] if n.startswith('TestKafkaFCMDeviceMongo')]
    if len(fcm['top_tests']) != 13 or fcm['expected_sub'] != 31 or fcm['allowed_legacy_skips'] or len(device) != 3:
        raise ValueError('new FCM8/26 and deviceMongo3 mandatory contract required')
    if len(c['accepted1064_test_files']) != 189 or len(c['frozen1080_test_files']) != 5:
        raise ValueError('exact inherited test-file custody required')
    for row in c['accepted1064_test_files'] + c['frozen1080_test_files']:
        if row['path'] not in bodies or sha(bodies[row['path']]) != row['sha256']:
            raise ValueError('inherited test body differs')
    mandatory = c['mandatory_new_mongo']
    if (len(mandatory) != 9 or len(set(mandatory)) != 9
            or any(t not in c['packages']['internal/messagedelivery']['top_tests'] for t in mandatory)):
        raise ValueError('exact9 new real Mongo cases required')
    previous = dict(bodies)
    wanted_before = {'server/internal/messagedelivery/' + n for n in
                     ('kafka_alias_vendor.go', 'kafka_vendor.go', 'kafka_fcm_devices.go')}
    if {r['target'] for r in c['physical_before']} != wanted_before or len(c['physical_before']) != 3:
        raise ValueError('only exact3 previous permit products allowed')
    for row in c['physical_before']:
        if (row['path'] != 'ci/candidates/group-ordinary/before-physical/' + row['target']
                or sha(bodies[row['target']]) != row['afterSHA256']):
            raise ValueError('physical before context differs')
        previous[row['target']] = pinned(row)
    if digest(rows(previous)) != c['physical_before_effective_digest']:
        raise ValueError('complete physical before source closure differs')
    before_root = REPO / 'ci/candidates/group-ordinary/before-physical'
    if {str(p.relative_to(REPO)) for p in before_root.rglob('*') if p.is_file() or p.is_symlink()} != {r['path'] for r in c['physical_before']}:
        raise ValueError('extra/missing physical before source')
    return c, bodies


def audit(events, code, package, spec):
    starts, ends, footer, output = set(), {}, [], []
    for e in events:
        if not isinstance(e, dict) or e.get('Package') != PREFIX + package or footer:
            raise ValueError('foreign/malformed/post-terminal JSON event')
        test, action = e.get('Test'), e.get('Action')
        if action == 'run' and test:
            if test in starts:
                raise ValueError('duplicate start')
            starts.add(test)
        elif action == 'output':
            output.append(e.get('Output', ''))
        elif action in ('pass', 'fail', 'skip'):
            if test:
                if test not in starts or test in ends:
                    raise ValueError('duplicate/unstarted terminal')
                ends[test] = action
            else:
                footer.append(action)
    expected = set(spec['top_tests'])
    compile_only = not expected
    permitted_skips = set(spec['allowed_legacy_skips'])
    skipped = sorted(n for n, a in ends.items() if a == 'skip')
    unexpected_skip = [n for n in skipped if n.split('/', 1)[0] not in permitted_skips]
    failures = sorted(n for n, a in ends.items() if a == 'fail')
    required_leaves = spec['required_leaves']
    missing_leaves = [n for n in required_leaves if ends.get(n) != 'pass']
    poison = bool(re.search(r'panic:|WARNING: DATA RACE|\[build failed\]|test timed out', ''.join(output)))
    complete = (starts == set(ends) and {n for n in ends if '/' not in n} == expected)
    good_footer = footer == ['pass'] or (compile_only and footer == ['skip'] and not ends
                                          and '[no test files]' in ''.join(output))
    sub_count = sum('/' in n for n in ends)
    sub_contract = spec.get('expected_sub')
    accepted = (code == 0 and complete and good_footer and not poison and not failures
                and not unexpected_skip and not missing_leaves
                and (sub_contract is None or sub_count == sub_contract))
    return {'accepted':accepted, 'counts':dict(Counter(('sub_' if '/' in n else 'top_') + a
                                                      for n, a in ends.items())),
            'terminal':ends, 'footer':footer, 'test_exit':code,
            'legacy_skips':skipped, 'unexpected_skip':unexpected_skip,
            'failed_tests':failures, 'missing_required_leaves':missing_leaves,
            'actual_sub':sub_count, 'compile_only_package':compile_only,
            'complete':complete, 'poison_output':poison, 'scope':SCOPE}



def audit_physical_before(events, code):
    denied = 'TestKafkaPhysicalPermitMongoClearBeforeFinalAdmissionNeverGrantsPOST'
    normal = 'TestKafkaPhysicalPermitMongoKnownCommitIsOneStartedAttemptEvenIfClearCommitsLater'
    modes = ('alias', 'generic', 'fcm')
    spec = {'top_tests':[denied, normal], 'allowed_legacy_skips':[],
            'required_leaves':[normal + '/' + mode for mode in modes], 'expected_sub':6}
    result = audit(events, code, 'internal/messagedelivery', spec)
    failed = {denied} | {denied + '/' + mode for mode in modes}
    passed = {normal} | {normal + '/' + mode for mode in modes}
    result['accepted'] = (code == 1 and result['complete'] and result['footer'] == ['fail']
        and not result['poison_output'] and not result['unexpected_skip']
        and not result['legacy_skips'] and not result['missing_required_leaves']
        and set(result['failed_tests']) == failed
        and {n for n, state in result['terminal'].items() if state == 'pass'} == passed
        and result['actual_sub'] == 6)
    # Names alone do not distinguish a product assertion from fixture failure.
    # Bind the single exact frozen Fatal diagnostic to EACH denied leaf's raw
    # test2json Output. Reject other source diagnostics anywhere in the baseline.
    assertion = 'Clear committed before admission but old plaintext acquired a physical POST permit'
    diagnostic = re.compile(r'^\s*\S+\.go:[1-9][0-9]*:')
    actual_assertion = re.compile(r'^\s*kafka_physical_permit_integration_test\.go:[1-9][0-9]*: '
                                  + re.escape(assertion) + r'$')
    leaves = {denied + '/' + mode for mode in modes}
    evidence = {leaf:{'exact_assertion_lines':0, 'assertion_occurrences':0,
                      'source_diagnostics':0} for leaf in sorted(leaves)}
    other_diagnostics = 0
    for event in events:
        if event.get('Action') != 'output':
            continue
        output = event.get('Output', '')
        if not isinstance(output, str):
            raise ValueError('non-string original before Output')
        leaf = event.get('Test')
        if leaf in evidence:
            evidence[leaf]['assertion_occurrences'] += output.count(assertion)
        for line in output.splitlines():
            if not diagnostic.match(line):
                continue
            if leaf not in evidence:
                other_diagnostics += 1
                continue
            evidence[leaf]['source_diagnostics'] += 1
            if actual_assertion.fullmatch(line):
                evidence[leaf]['exact_assertion_lines'] += 1
    actual_red = (other_diagnostics == 0 and all(
        row == {'exact_assertion_lines':1, 'assertion_occurrences':1, 'source_diagnostics':1}
        for row in evidence.values()))
    result['accepted'] = result['accepted'] and actual_red
    result['before_assertion_evidence'] = evidence
    result['before_other_source_diagnostics'] = other_diagnostics
    result['meaning'] = 'exact3 leaf-bound Clear assertions fail once each plus3 valid positives; every other source failure diagnostic rejects'
    return result


def require_resource(resource, code):
    if (resource['status'] not in ('passed', 'failed') or resource['exit_code'] is None
            or code != resource['exit_code']
            or (resource['status'] == 'passed') != (resource['exit_code'] == 0)):
        raise ValueError('guarded phase incomplete; retain partial evidence')


def write(path, value):
    with path.open('x') as f:
        json.dump(value, f, indent=2)
        f.write('\n')
        f.flush()
        os.fsync(f.fileno())
    fd = os.open(path.parent, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def cloud_environment():
    required = {'GITHUB_ACTIONS':'true', 'RUNNER_ENVIRONMENT':'github-hosted', 'RUNNER_OS':'Linux',
                'LUOBO_STORAGE_TEST_MONGO_URI':MONGO, 'LUOBO_RETENTION_TEST_REDIS':REDIS,
                'LUOBO_RUN_LEGACY_EXAMPLES':'0', 'LUOBO_RUN_LEGACY_LOAD_TESTS':'0',
                'LUOBO_EMAIL_INTEGRATION':'0'}
    if any(os.environ.get(k) != v for k, v in required.items()):
        raise ValueError('exact cloud fixture environment required; tests not started')
    if os.environ.get('LUOBO_INTEGRATION_KAFKA_BROKER'):
        raise ValueError('Kafka is outside this owned fixture')
    run_id, attempt = os.environ['GITHUB_RUN_ID'], os.environ['GITHUB_RUN_ATTEMPT']
    if not re.fullmatch(r'[0-9]+', run_id) or not re.fullmatch(r'[0-9]+', attempt):
        raise ValueError('exact fixture owner required')
    socket = Path('/tmp/luobo-it-' + run_id + '-' + attempt + '/references/integration/redis8.2/redis.sock')
    info = socket.lstat()
    if socket.resolve() != socket or not stat.S_ISSOCK(info.st_mode) or info.st_uid != os.getuid():
        raise ValueError('owned exact fixture socket missing')
    inherited = {'LUOBO_INTEGRATION_MONGO_URI':MONGO, 'LUOBO_RETENTION_TEST_MONGO':MONGO,
                 'LUOBO_MONGO_TEST_URI':MONGO, 'LUOBO_BOOKING_TEST_MONGO_URI':MONGO,
                 'LUOBO_BOOKING_TEST_REDIS_ADDR':'127.0.0.1:36379',
                 'LUOBO_INTEGRATION_REDIS_SOCKET':str(socket)}
    if any(k in os.environ and os.environ[k] != v for k, v in inherited.items()):
        raise ValueError('inherited fixture configuration differs')
    os.environ.update(inherited)



def compile_packages(packages, command):
    # Keep every healthy child compiler result. Resource/monitor/timeout failure
    # still raises immediately inside command: continuing then is unsafe.
    codes = {}
    for package in packages:
        _, code = command(package, ['go', 'test', '-mod=readonly', '-p=1', '-race', '-json',
                          '-run', '^$', './' + package], 'compile', 900)
        codes[package] = code
    return codes


def run():
    cloud_environment()  # Missing/foreign fixture is a controller rejection, not a Go skip.
    c, bodies = verify()
    version = subprocess.check_output(['go', 'version'], text=True, timeout=15).strip()
    if version != 'go version go1.27.2 linux/amd64':
        raise ValueError('exact Go compiler required')
    subprocess.run([sys.executable, '-B', str(REPO / 'ci/backend-integration.py'), 'check-ready',
                    str(REPO / 'ci-results/backend-integration'),
                    os.environ['GITHUB_RUN_ID'] + ':' + os.environ['GITHUB_RUN_ATTEMPT'],
                    os.environ['GITHUB_SHA']], check=True, timeout=20)
    temporary = Path(os.environ['RUNNER_TEMP'])
    work = temporary / 'group-ordinary-source1099'
    if not temporary.is_absolute() or temporary.resolve() != temporary or work.exists():
        raise ValueError('fresh canonical work path required')
    fs = os.statvfs(temporary)
    if fs.f_bavail * fs.f_frsize < 4 * 1024 ** 3:
        raise ValueError('4GiB bootstrap floor required')
    work.mkdir(mode=0o700)
    output = REPO / 'ci-results/group-ordinary-source1099'
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    for target, body in bodies.items():
        path = work / target
        path.parent.mkdir(parents=True, exist_ok=True)
        with path.open('xb') as f:
            f.write(body)
        path.chmod(c['source_modes'][target])
    # Only the same public example the owned prepare step uses. Never copy a
    # runtime/private config from the checkout or a local service directory.
    template = bodies['server/config/config.example.yaml']
    with (work / 'server/config/config.yaml').open('xb') as f:
        f.write(template)
    for key, directory in (('GOMODCACHE','modcache'), ('GOCACHE','buildcache'), ('TMPDIR','tmp')):
        path = work / directory
        path.mkdir(mode=0o700)
        os.environ[key] = str(path)
    os.environ.update(GOTOOLCHAIN='local', GOMAXPROCS='2', GOMEMLIMIT='900MiB', GOWORK='off')
    write(output / 'source.json', {'scope':SCOPE, 'commit':os.environ['GITHUB_SHA'],
          'inputs_sha256':sha(regular(INPUTS)), 'effective_source_count':1099,
          'effective_digest':c['effective_digest'], 'go_version':version,
          'generated_public_config_sha256':sha(template), 'no_runtime_adoption':True})

    def command(package, args, phase, timeout, source_root=None):
        run_root = work if source_root is None else source_root
        if run_root not in (work, work / 'before-physical'):
            raise ValueError('unowned command source root')
        folder = output / package.replace('/', '_')
        folder.mkdir(exist_ok=True)
        stdout, stderr = folder / (phase + '.log'), folder / (phase + '.stderr.log')
        receipt = folder / (phase + '.resource.json')
        capture = [sys.executable, '-B', str(REPO / 'ci/group-effects/capture.py'),
                   '--stdout', str(stdout), '--stderr', str(stderr), '--'] + args
        wrapped = [sys.executable, '-B', str(REPO / 'ci/group-effects/resource_guard.py'),
                   '--cwd', str(run_root / 'server'), '--log', str(folder / (phase + '.guard.log')),
                   '--record', str(receipt), '--min-free-mib', '1024', '--timeout', str(timeout),
                   '--poll-interval', '0.2', '--allocated-root', str(work),
                   '--max-allocated-mib', '16384', '--'] + capture
        process = subprocess.run(wrapped, check=False, timeout=timeout + 30)
        resource = json.loads(regular(receipt))
        # Healthy child failure is still parsed and preserved as a real FAIL.
        # Disk/monitor/timeout/remaining-child failure can never be accepted.
        require_resource(resource, process.returncode)
        return stdout, resource['exit_code']

    packages = list(c['packages'])
    compile_codes = compile_packages(packages, command)
    write(output / 'compile-summary.json', {
        'state':'all_compilers_finished' if all(code == 0 for code in compile_codes.values()) else 'compile_failures_no_tests_or_vet',
        'scope':SCOPE, 'exit_codes':compile_codes,
        'tests_started':False, 'vet_started':False})
    if any(code != 0 for code in compile_codes.values()):
        raise ValueError('compile failed; complete raw diagnostics retained; no tests or vet')
    # Separate full1099 source context; only3 products are restored. The same
    # new Mongo test file is byte-identical, not a missing-helper compile RED.
    before_work = work / 'before-physical'
    before_work.mkdir(mode=0o700)
    previous = dict(bodies)
    for row in c['physical_before']:
        previous[row['target']] = pinned(row)
    for target, body in previous.items():
        path = before_work / target
        path.parent.mkdir(parents=True, exist_ok=True)
        with path.open('xb') as f:
            f.write(body)
        path.chmod(c['source_modes'][target])
    with (before_work / 'server/config/config.yaml').open('xb') as f:
        f.write(template)
    expression = '^TestKafkaPhysicalPermitMongo(ClearBeforeFinalAdmissionNeverGrantsPOST|KnownCommitIsOneStartedAttemptEvenIfClearCommitsLater)$'
    log, code = command('internal/messagedelivery',
        ['go', 'test', '-mod=readonly', '-p=1', '-parallel=1', '-race', '-json', '-count=1',
         '-timeout=240s', '-run', expression, './internal/messagedelivery'],
        'before-physical', 900, source_root=before_work)
    events = [json.loads(line) for line in regular(log).decode().splitlines() if line.strip()]
    baseline = audit_physical_before(events, code)
    write(output / 'physical-before-summary.json', baseline)
    if not baseline['accepted']:
        raise ValueError('physical before is not exact assertion-only RED with positive controls')
    for target, body in previous.items():
        if regular(before_work / target) != body:
            raise ValueError('before source changed during real baseline')
    summaries = {}
    for package, spec in c['packages'].items():
        log, code = command(package, ['go', 'test', '-mod=readonly', '-p=1', '-parallel=1',
                            '-race', '-json', '-count=1', '-timeout=240s', './' + package], 'tests', 900)
        events = [json.loads(line) for line in regular(log).decode().splitlines() if line.strip()]
        summary = audit(events, code, package, spec)
        write(output / package.replace('/', '_') / 'summary.json', summary)
        summaries[package] = summary
        if not summary['accepted']:
            raise ValueError('full package rejected; retain actual events')
    for package in packages:
        _, code = command(package, ['go', 'vet', '-mod=readonly', '-p=1', './' + package], 'vet', 240)
        if code != 0:
            raise ValueError('vet failed')
    for target, body in bodies.items():
        if regular(work / target) != body:
            raise ValueError('source/lock changed during validation')
    if regular(work / 'server/config/config.yaml') != template:
        raise ValueError('generated public config changed')
    verify()
    final_space = os.statvfs(work)
    final_free = final_space.f_bavail * final_space.f_frsize
    if final_free < 1024 ** 3:
        raise ValueError('final disk floor not met')
    counts = Counter()
    for summary in summaries.values():
        counts.update(summary['counts'])
    if counts.get('top_pass', 0) != 364 or counts.get('top_skip', 0) != 7 or counts.get('sub_skip', 0) != 0:
        raise ValueError('exact364 mandatory PASS and7 old top SKIP required')
    write(output / 'summary.json', {'state':'passed_full_source_packages_only', 'scope':SCOPE,
          'effective_digest':c['effective_digest'], 'counts':dict(counts),
          'packages':summaries, 'vet_exit':{p:0 for p in packages},
          'final_free_bytes':final_free,
          'legacy_skips_are_not_integration_passes':True, 'no_runtime_adoption':True})
    print('Ordinary source1099 full26-package validation complete; runtime remains unadopted.')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--verify', action='store_true')
    args = parser.parse_args()
    try:
        if args.verify:
            verify()
            print('Ordinary exact source1099 verified; no Go/DB execution.')
        else:
            run()
    except (ValueError, KeyError, TypeError, OSError, subprocess.SubprocessError):
        print('Ordinary source validation incomplete; preserve raw evidence and exact owned fixture diagnostics.', file=sys.stderr)
        raise SystemExit(2)
