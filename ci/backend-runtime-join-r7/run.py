#!/usr/bin/env python3
"""Only original three failed children and new four test functions; no deployment."""
import argparse
from collections import Counter
import hashlib, importlib.util, json, os
from pathlib import Path, PurePosixPath
import re, stat, subprocess, sys, time
REPO = Path(__file__).resolve().parents[2]
INPUTS = Path(__file__).with_name('inputs.json')
PREFIX = "github.com/openimsdk/open-im-server/v3/"
SCOPE = 'backend1407_original_Redis_COS_failed3sub_new4top27sub_only_no_full_build_or_regression'
BASE = {'members': 1406, 'digest': '884713cc6c7a3eb97f024bf733ee49623b8adb0662397c1a0adaeeec8fa4ee05'}
FINAL = {'members': 1407, 'digest': '5359b5cae2c92096dd2e0548926c6439c96583b46be1b03ce9b78df3aa91a2f9'}
SOURCE_ROOT = 'ci/candidates/backend-runtime-join-r7/source/'
ALLOWED = frozenset(['server/internal/rpc/third/owned_provider_test.go', 'server/pkg/common/db/cache/owned_connection_setup_redis_test.go', 'server/pkg/common/db/cache/owned_source_redis.go', 'server/pkg/common/db/s3/cos/cos.go'])
SELECTION_SHA = '378dffc301400c5c394dbec925e2fe8556d4dfdb5a1a55983c62dabb0b98c072'
MONGO = 'mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration'


def sha(raw):
    return hashlib.sha256(raw).hexdigest()

def regular(path):
    if path.resolve() != path or not path.is_file() or path.is_symlink():
        raise ValueError('canonical regular file required')
    return path.read_bytes()

def pinned(row):
    path = REPO / row['path']
    raw = regular(path)
    if len(raw) != row['bytes'] or sha(raw) != row['sha256'] or stat.S_IMODE(path.stat().st_mode) != 0o644:
        raise ValueError('pinned source body/mode differs: ' + row['path'])
    return raw

def module(path, label):
    spec = importlib.util.spec_from_file_location(label, REPO / path)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value

def check_selection(c):
    fields = ('priorPassedTargets','failedTargets','newTargets','targets','reusedTargets','requiredSubtests','reusedSubtests')
    selected = {k:c[k] for k in fields}
    if sha(json.dumps(selected,sort_keys=True,separators=(',',':')).encode()) != SELECTION_SHA or c['selectionSHA256'] != SELECTION_SHA:
        raise ValueError('exact actual167 passes, failed3 children and new27 children required')
    pairs = lambda x:{(p,n) for p,ns in x.items() for n in ns}
    if pairs(c['targets']) != pairs(c['failedTargets']) | pairs(c['newTargets']) or len(pairs(c['targets'])) != 6 or len(pairs(c['reusedTargets'])) != 167 or pairs(c['reusedTargets']) & pairs(c['targets']):
        raise ValueError('no replay or invented pass reuse')

def verify():
    c = json.loads(regular(INPUTS))
    if c['schema'] != 1 or c['scope'] != SCOPE or c['base'] != BASE or c['source'] != FINAL:
        raise ValueError('exact failed-source parent and current source required')
    wanted = {'ci/backend-runtime-join-r6/run.py','ci/backend-runtime-join-r6/inputs.json','ci/group-effects/resource_guard.py','ci/group-effects/capture.py','ci/backend-integration.py','ci/backend-integration.sh'}
    if len(c['dependencies']) != 6 or {r['path'] for r in c['dependencies']} != wanted:
        raise ValueError('immutable original fixtures and source controller required')
    for row in c['dependencies']: pinned(row)
    ancestor = module('ci/backend-runtime-join-r6/run.py','immutable1406_failed_parent')
    old, before, recipe = ancestor.verify()
    if old['source'] != BASE: raise ValueError('wrong parent')
    bodies = dict(before); seen=set()
    for row in c['overlay']:
        t=row['target']; previous=before.get(t)
        if t not in ALLOWED or t in seen or row['path'] != SOURCE_ROOT+t or row['beforeSHA256'] != (sha(previous) if previous is not None else None):
            raise ValueError('exact four reviewed source changes required')
        seen.add(t); bodies[t]=pinned(row)
    actual={str(p.relative_to(REPO)) for p in (REPO/SOURCE_ROOT).rglob('*') if p.is_file() or p.is_symlink()}
    if seen != ALLOWED or actual != {r['path'] for r in c['overlay']} or len(bodies) != FINAL['members'] or recipe.digest(recipe.rows(bodies)) != FINAL['digest'] or c['sourceModes'] != {t:old['sourceModes'].get(t,0o644) for t in bodies}:
        raise ValueError('source, mode or complete overlay drift')
    check_selection(c)
    names = lambda data:{(str(PurePosixPath(p).parent.relative_to('server')),n) for p,b in data.items() if p.endswith('_test.go') for n in re.findall(r'^func (Test[A-Za-z0-9_]+)\(t \*testing\.T\)',b.decode(),re.M)}
    old_names,new_names=names(before),names(bodies)
    if old_names-new_names or new_names-old_names != {(p,n) for p,ns in c['newTargets'].items() for n in ns}:
        raise ValueError('original test omitted or exact new four functions differ')
    return c,bodies,recipe

def require_children(result,c):
    actual={(r['package'],r['name']) for r in result['namedCompletions'] if '/' in r['name']}
    expected={(p,n+'/'+child) for p,m in c['requiredSubtests'].items() for n,ns in m.items() for child in ns}
    if actual != expected: raise ValueError('failed/new child omitted or passing sibling replayed')
    return result


def audit(events, code, targets):
    starts, passes, footers = Counter(), Counter(), Counter()
    for event in events:
        if not isinstance(event, dict):
            raise ValueError('malformed event')
        package = event.get('Package', '').removeprefix(PREFIX)
        if event.get('Package') != PREFIX + package or package not in targets or footers[package]:
            raise ValueError('foreign or post-terminal event')
        action, test = event.get('Action'), event.get('Test')
        if action not in ('start', 'run', 'output', 'pass', 'pause', 'cont') or any(s in event.get('Output', '') for s in ('WARNING: DATA RACE', 'panic:', 'fatal error:')):
            raise ValueError('failure, skip, race or malformed action')
        if test:
            if test.split('/')[0] not in targets[package]:
                raise ValueError('unselected top or descendant')
            if action == 'run':
                starts[(package, test)] += 1
            if action == 'pass':
                passes[(package, test)] += 1
        elif action == 'pass':
            footers[package] += 1
    expected = {(p, n) for p, names in targets.items() for n in names}
    actual = {key for key in starts if '/' not in key[1]}
    if code != 0 or actual != expected or starts != passes or any(v != 1 for v in starts.values()) or footers != Counter({p: 1 for p in targets}):
        raise ValueError('incomplete or duplicate named completion')
    return {'topPASS': len(expected), 'subPASS': len(starts) - len(expected), 'skip': 0, 'fail': 0, 'packages': len(footers), 'namedCompletions': [{'package': p, 'name': n} for p, n in sorted(passes)]}

def write(path, value):
    with path.open('x') as out:
        json.dump(value, out, indent=2)
        out.write('\n')
        out.flush()
        os.fsync(out.fileno())

def run():
    started = time.monotonic()
    for key, value in {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux', 'GOTOOLCHAIN': 'local', 'GOWORK': 'off'}.items():
        if os.environ.get(key) != value:
            raise ValueError('hosted disposable Linux only')
    if os.getuid() == 0 or not re.fullmatch(r'[0-9]+', os.environ.get('GITHUB_RUN_ID', '')) or not re.fullmatch(r'[0-9]+', os.environ.get('GITHUB_RUN_ATTEMPT', '')) or not re.fullmatch(r'[0-9a-f]{40}', os.environ.get('GITHUB_SHA', '')):
        raise ValueError('exact nonroot run/attempt/commit required')
    c, bodies, parent = verify()
    version = subprocess.check_output(['go', 'version'], text=True, timeout=15).strip()
    if version != 'go version go1.27.2 linux/amd64':
        raise ValueError('exact Go compiler required')
    temporary = Path(os.environ['RUNNER_TEMP'])
    work = temporary / ('backend-runtime-join-r7-' + os.environ['GITHUB_RUN_ID'] + '-' + os.environ['GITHUB_RUN_ATTEMPT'])
    fs = os.statvfs(temporary)
    if not temporary.is_absolute() or temporary.resolve() != temporary or work.exists() or fs.f_bavail * fs.f_frsize < 4 * 1024**3:
        raise ValueError('fresh path and 4GiB initial floor required')
    work.mkdir(mode=0o700)
    output = REPO / 'ci-results/backend-runtime-join-r7'
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    for target, raw in bodies.items():
        path = work / target
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(raw)
        path.chmod(c['sourceModes'][target])
    template = bodies['server/config/config.example.yaml']
    (work / 'server/config/config.yaml').write_bytes(template)
    env = {'PATH': os.environ['PATH'], 'GOROOT': subprocess.check_output(['go', 'env', 'GOROOT'], text=True, timeout=15).strip(), 'GOTOOLCHAIN': 'local', 'GOWORK': 'off', 'GOENV': 'off', 'CGO_ENABLED': '1', 'GOMAXPROCS': '2', 'GOMEMLIMIT': '900MiB'}
    for key, folder in [('GOMODCACHE', 'modcache'), ('GOCACHE', 'buildcache'), ('GOPATH', 'gopath'), ('TMPDIR', 'tmp')]:
        path = work / folder
        path.mkdir(mode=0o700)
        env[key] = str(path)
    env['GOTMPDIR'] = env['TMPDIR']
    write(output / 'source.json', {'scope': SCOPE, 'commit': os.environ['GITHUB_SHA'], 'run': os.environ['GITHUB_RUN_ID'], 'attempt': os.environ['GITHUB_RUN_ATTEMPT'], 'inputsSHA256': sha(regular(INPUTS)), 'source': c['source'], 'targets': c['targets'], 'goVersion': version, 'runtimeChanged': False, 'fullRegression': False})

    def command(label, argv, timeout):
        remaining = 3000 - (time.monotonic() - started)
        if remaining < 1:
            raise ValueError('controller budget exhausted')
        timeout = max(1, int(min(remaining, timeout)))
        folder = output / label
        folder.mkdir()
        capture = [sys.executable, '-B', str(REPO / 'ci/group-effects/capture.py'), '--stdout', str(folder / 'stdout.jsonl'), '--stderr', str(folder / 'stderr.log'), '--'] + argv
        wrapped = [sys.executable, '-B', str(REPO / 'ci/group-effects/resource_guard.py'), '--cwd', str(work / 'server'), '--log', str(folder / 'guard.log'), '--record', str(folder / 'resource.json'), '--min-free-mib', '1024', '--timeout', str(timeout), '--poll-interval', '.2', '--allocated-root', str(work), '--max-allocated-mib', '16384', '--'] + capture
        child = subprocess.run(wrapped, check=False, timeout=timeout + 30)
        record = json.loads(regular(folder / 'resource.json'))
        if record.get('status') not in ('passed', 'failed') or record.get('exit_code') != child.returncode or (record['status'] == 'passed') != (child.returncode == 0):
            raise ValueError('guard, timeout or remaining process rejection')
        return folder, child.returncode

    _, code = command('fixture-ready', [sys.executable, '-B', str(REPO / 'ci/backend-integration.py'), 'check-ready', str(REPO / 'ci-results/backend-integration'), os.environ['GITHUB_RUN_ID'] + ':' + os.environ['GITHUB_RUN_ATTEMPT'], os.environ['GITHUB_SHA']], 30)
    if code:
        raise ValueError('owned fixture readiness rejected')
    clean = lambda values: ['env', '-i'] + [k + '=' + v for k, v in values.items()]
    download = dict(env, GOPROXY='https://proxy.golang.org', GOSUMDB='sum.golang.org', GOAUTH='off', GOPRIVATE='', GONOPROXY='', GONOSUMDB='')
    _, code = command('module-download', clean(download) + ['go', 'mod', 'download', '-json'], 600)
    if code or any(regular(work / p) != bodies[p] for p in ('server/go.mod', 'server/go.sum')):
        raise ValueError('locked public dependencies rejected')
    fixtures = {'LUOBO_STORAGE_TEST_MONGO_URI': MONGO, 'LUOBO_GROUP_RECEIVER_TEST_MONGO_URI': MONGO, 'LUOBO_GROUP_ROLE_TEST_MONGO_URI': MONGO, 'LUOBO_RETENTION_TEST_REDIS': 'redis://127.0.0.1:36379/15', 'LUOBO_EMAIL_INTEGRATION': '0', 'LUOBO_RUN_LEGACY_LOAD_TESTS': '0', 'LUOBO_RUN_LEGACY_EXAMPLES': '0'}
    socket = Path('/tmp') / ('luobo-it-' + os.environ['GITHUB_RUN_ID'] + '-' + os.environ['GITHUB_RUN_ATTEMPT']) / 'references/integration/redis8.2/redis.sock'
    if socket.resolve() != socket or not stat.S_ISSOCK(socket.lstat().st_mode):
        raise ValueError('exact owned Redis fixture socket required')
    fixtures.update(LUOBO_INTEGRATION_MONGO_URI=MONGO, LUOBO_INTEGRATION_REDIS_SOCKET=str(socket), LUOBO_BOOKING_TEST_REDIS_ADDR='127.0.0.1:36379')
    testenv = dict(env, GOPROXY='off', GOSUMDB='off', **fixtures)
    names = sorted({n for selected in c['targets'].values() for n in selected})
    children = sorted({n for group in c['requiredSubtests'].values() for values in group.values() for n in values})
    pattern = '^(' + '|'.join(re.escape(n) for n in names) + ')$/^(' + '|'.join(re.escape(n) for n in children) + ')$'
    args = clean(testenv) + ['go', 'test', '-mod=readonly', '-p=1', '-parallel=1', '-race', '-json', '-count=1', '-vet=off', '-timeout=180s', '-run', pattern] + ['./' + p for p in c['targets']]
    folder, code = command('targeted-tests', args, 2250)
    try:
        events = [json.loads(line) for line in regular(folder / 'stdout.jsonl').decode().splitlines() if line.strip()]
        result = dict(require_children(audit(events, code, c['targets']), c), accepted=True)
    except (ValueError, TypeError, KeyError, UnicodeError):
        result = {'accepted': False, 'exitCode': code, 'originalRawRetained': True}
    for target, raw in bodies.items():
        path = work / target
        if regular(path) != raw or stat.S_IMODE(path.stat().st_mode) != c['sourceModes'][target]:
            raise ValueError('source or mode drift')
    if regular(work / 'server/config/config.yaml') != template:
        raise ValueError('generated public config drift')
    verify()
    write(output / 'summary.json', {'state': 'targeted_pass' if result['accepted'] else 'failed_or_incomplete', 'entryCompileEvidenceReused': c['entryCompileEvidenceReused'], 'currentEntryBuildReexecuted': False, 'scope': SCOPE, 'result': result, 'sourcePostExact': c['source'], 'sourceModesAndLocksPostExact': True, 'runtimeChanged': False, 'fullRegression': False, 'fixtureCollectCleanup': 'subsequent workflow steps must also pass', 'networkBoundary': 'clean environment and exact owned loopback fixture; no kernel egress isolation claim'})
    return 0 if result['accepted'] else 1

if __name__ == '__main__':
    p=argparse.ArgumentParser(); p.add_argument('--verify',action='store_true'); a=p.parse_args()
    if a.verify:
        c,_,_=verify(); print(json.dumps({'source':c['source'],'newTops':4,'failedTops':2,'requiredChildren':30,'reusedTops':167,'reusedChildren':3,'GoExecuted':0,'DBExecuted':0}))
    else: raise SystemExit(run())
