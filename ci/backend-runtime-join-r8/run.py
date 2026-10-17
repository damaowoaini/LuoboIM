#!/usr/bin/env python3
"""Exact formatted historical source, fourteen actual main builds, new and directly affected named tests; no deployment."""
import argparse
from collections import Counter
from contextlib import contextmanager
import hashlib, importlib.util, json, os
from pathlib import Path, PurePosixPath
import re, stat, subprocess, sys, time
REPO = Path(__file__).resolve().parents[2]
INPUTS = Path(__file__).with_name('inputs.json')
PREFIX = "github.com/openimsdk/open-im-server/v3/"
SCOPE = 'backend_history_final14_entries_and_new65_plus_direct_only_no_deployment'
BASE = {'members': 1407, 'digest': '5359b5cae2c92096dd2e0548926c6439c96583b46be1b03ce9b78df3aa91a2f9'}
FINAL = {'members': 1555, 'digest': '686ba84ba24fd1c9e9c5e54bc9f27a41e74baf78eb0a0118cbc10044f14ee09f'}
SOURCE_ROOT = 'ci/candidates/backend-runtime-join-r8/source/'
SELECTION_SHA = 'fa60269119a63804032ea8e870006be69775899188de97c95c37db8710416784'
RESOURCE = {'initialBytes': 4294967296, 'floorMiB': 1024, 'allocatedMiB': 16384, 'pollSeconds': .2, 'controllerSeconds': 3000, 'compileTotalSeconds': 1200, 'testPhaseSeconds': 2250, 'testGoTimeoutSeconds': 180}
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

def pairs(values):
    out = [(p,n) for p,names in values.items() for n in names]
    if len(out) != len(set(out)):
        raise ValueError('duplicate selected or reused name')
    return set(out)


def check_selection(c):
    fields = ('priorPassedTargets','newTargets','directlyAffectedTargets','directExistingTargets',
              'targets','reusedTargets','pendingTargets','requiredSubtests')
    selected = {k:c[k] for k in fields}
    if (SELECTION_SHA is None or c['selectionSHA256'] != SELECTION_SHA
            or sha(json.dumps(selected,sort_keys=True,separators=(',',':')).encode()) != SELECTION_SHA):
        raise ValueError('source-bound actual173 selection/impact proof required')
    previous, new, affected = pairs(c['priorPassedTargets']), pairs(c['newTargets']), pairs(c['directlyAffectedTargets'])
    direct_existing, active = pairs(c['directExistingTargets']), pairs(c['targets'])
    reused, pending = pairs(c['reusedTargets']), pairs(c['pendingTargets'])
    if (len(previous) != 173 or len(new) != 65 or previous & new
            or new & (affected | direct_existing | pending) or active != new | affected | direct_existing
            or affected - previous or direct_existing & previous or active & pending
            or reused != previous - affected - pending or reused & active
            or pending != {('pkg/common/startrpc','TestOwnedPushActualOwnerClosesOriginalResourcesBeforeRedisUnknownNonpermit')}):
        raise ValueError('fresh/reused/affected/pending must not alias old required-token v1 or replay known COS/Redis')


def validate_entries(bodies,c):
    rows = c['entrypoints']
    if len(rows) != 14 or len({r['role'] for r in rows}) != 14 or len({r['package'] for r in rows}) != 14:
        raise ValueError('fourteen actual distinct main packages required')
    families = Counter(r['lifecycleFamily'] for r in rows)
    if families != Counter({'rpc':10,'api':1,'crontask':1,'msggateway':1,'msgtransfer-managed':1}):
        raise ValueError('actual five lifecycle families required')
    if [r['package'] for r in rows] != c['compileOnlyPackages']:
        raise ValueError('compile list is not actual fourteen mains')
    for row in rows:
        raw = bodies.get(row['target']); identity = row['actualMain']
        if (raw is None or row['target'] != 'server/'+row['package'].removeprefix('./')+'/main.go'
                or row['package'].startswith('./cmd/') is not True
                or len(raw) != identity['bytes'] or sha(raw) != identity['sha256']
                or row['outputName'] != row['role'] or not re.fullmatch('[a-z][a-z0-9-]*',row['outputName'])):
            raise ValueError('actual main identity or owned binary output differs')
    for family in c['lifecycleFamilies']:
        for key in ('command','ownedOwner'):
            binding=family[key];raw=bodies.get(binding['target']);identity=binding['source']
            if raw is None or len(raw)!=identity['bytes'] or sha(raw)!=identity['sha256']:
                raise ValueError('actual lifecycle dispatch/owner source differs')
    group = next(r for r in rows if r['role'] == 'rpc-group')
    if b'StartSvrManaged' not in bodies[group['target']]:
        raise ValueError('actual managed group entry cannot be replaced by old main')


def compile_plan(c,work):
    # These are real separate executable packages, not a general RPC binary
    # with a synthetic role argument. Outputs stay owned and are never run.
    return [{'role':r['role'],'package':r['package'],'output':str(work/'bin'/r['outputName']),
             'argv':['go','build','-mod=readonly','-p=1','-o',str(work/'bin'/r['outputName']),r['package']]}
            for r in c['entrypoints']]


def verify():
    c = json.loads(regular(INPUTS))
    if (FINAL is None or c.get('state') != 'FROZEN_SOURCE' or c['schema'] != 1
            or c['scope'] != SCOPE or c['base'] != BASE or c['source'] != FINAL or c['resource'] != RESOURCE):
        raise ValueError('exact Root final formatted source proof required; WIP cannot execute')
    wanted = {'ci/backend-runtime-join-r7/run.py','ci/backend-runtime-join-r7/inputs.json','ci/group-effects/resource_guard.py','ci/group-effects/capture.py','ci/backend-integration.py','ci/backend-integration.sh'}
    if len(c['dependencies']) != 6 or {r['path'] for r in c['dependencies']} != wanted:
        raise ValueError('immutable actual R7 source and original fixture helpers required')
    for row in c['dependencies']: pinned(row)
    ancestor = module('ci/backend-runtime-join-r7/run.py','actual1407_source_only_parent')
    old,before,recipe = ancestor.verify()
    if old['source'] != BASE: raise ValueError('wrong actual R7 source')
    bodies=dict(before);seen=set();allowed=frozenset(c['allowedOverlayTargets'])
    for row in c['overlay']:
        t=row['target'];previous=before.get(t)
        if (t not in allowed or t in seen or row['path'] != SOURCE_ROOT+t
                or row['beforeSHA256'] != (sha(previous) if previous is not None else None)):
            raise ValueError('explicit final source delta required')
        seen.add(t);bodies[t]=pinned(row)
    actual={str(p.relative_to(REPO)) for p in (REPO/SOURCE_ROOT).rglob('*') if p.is_file() or p.is_symlink()}
    if (seen != allowed or actual != {r['path'] for r in c['overlay']}
            or len(bodies) != FINAL['members'] or recipe.digest(recipe.rows(bodies)) != FINAL['digest']
            or c['sourceModes'] != {t:old['sourceModes'].get(t,0o644) for t in bodies}):
        raise ValueError('complete source/vector/mode or overlay drift')
    for t in ('server/go.mod','server/go.sum','server/config/config.example.yaml'):
        if bodies[t] != before[t]:raise ValueError('original module locks/public configuration changed')
    names=lambda data:{(str(PurePosixPath(p).parent.relative_to('server')),n) for p,b in data.items() if p.endswith('_test.go') for n in re.findall(r'^func (Test[A-Za-z0-9_]+)\(t \*testing\.T\)',b.decode(),re.M)}
    old_names,new_names=names(before),names(bodies)
    if old_names-new_names or new_names-old_names != pairs(c['newTargets']):
        raise ValueError('original test removed or actual R7 source new-name delta differs')
    check_selection(c);validate_entries(bodies,c)
    if not pairs(c['targets']) <= new_names or not pairs(c['pendingTargets']) <= new_names:
        raise ValueError('selected or pending original method absent from exact source')
    return c,bodies,recipe


def require_children(result,c):
    # Exact declared children for reviewed finite controls; every other named
    # descendant is still required to have exactly one run/pass, no skip/fail.
    for package,tests in c['requiredSubtests'].items():
        for top,children in tests.items():
            actual={r['name'] for r in result['namedCompletions'] if r['package']==package and r['name'].startswith(top+'/')}
            if actual != {top+'/'+child for child in children}:
                raise ValueError('required original/new child omitted or foreign descendant')
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

def record_source_post(work, output, c, bodies, template):
    record = {'accepted': False, 'source': c['source'], 'materializationComplete': True,
              'bodiesModesLocksGeneratedConfigExact': False, 'controllerVerifyExact': False}
    try:
        for target, raw in bodies.items():
            path = work / target
            if regular(path) != raw or stat.S_IMODE(path.stat().st_mode) != c['sourceModes'][target]:
                raise ValueError('materialized source body or mode drift')
        config = work / 'server/config/config.yaml'
        if regular(config) != template:
            raise ValueError('generated public configuration drift')
        record['bodiesModesLocksGeneratedConfigExact'] = True
        checked, _, _ = verify()
        if checked['source'] != c['source']:
            raise ValueError('controller source identity changed')
        record['controllerVerifyExact'] = True
        record['accepted'] = True
    except Exception as error:
        record['exceptionType'] = type(error).__name__
    # This independent evidence is never overwritten, including on failed compile.
    write(output / 'source-post.json', record)
    return record


@contextmanager
def retain_materialized_source_post(work, output, c, bodies, template):
    primary = None
    try:
        yield
    except BaseException as error:
        primary = error
        raise
    finally:
        try:
            record = record_source_post(work, output, c, bodies, template)
            if not record['accepted']:
                raise ValueError('source-post failed; original raw retained')
        except Exception:
            # Source-post cannot turn failure into success or mask an earlier
            # command/guard failure. A clean command path still fails closed.
            if primary is None:
                raise


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
    work = temporary / ('backend-runtime-join-r8-' + os.environ['GITHUB_RUN_ID'] + '-' + os.environ['GITHUB_RUN_ATTEMPT'])
    fs = os.statvfs(temporary)
    if not temporary.is_absolute() or temporary.resolve() != temporary or work.exists() or fs.f_bavail * fs.f_frsize < 4 * 1024**3:
        raise ValueError('fresh path and 4GiB initial floor required')
    work.mkdir(mode=0o700)
    output = REPO / 'ci-results/backend-runtime-join-r8'
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    for target, raw in bodies.items():
        path = work / target
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(raw)
        path.chmod(c['sourceModes'][target])
    template = bodies['server/config/config.example.yaml']
    (work / 'server/config/config.yaml').write_bytes(template)
    # Enter only after every authored source and generated config is materialized.
    with retain_materialized_source_post(work, output, c, bodies, template):
        env = {'PATH': os.environ['PATH'], 'GOROOT': subprocess.check_output(['go', 'env', 'GOROOT'], text=True, timeout=15).strip(), 'GOTOOLCHAIN': 'local', 'GOWORK': 'off', 'GOENV': 'off', 'CGO_ENABLED': '1', 'HOME': os.environ['HOME'], 'GOAUTH': 'off', 'GOMAXPROCS': '2', 'GOMEMLIMIT': '900MiB'}
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
        (work/'bin').mkdir(mode=0o700)
        compiled=[];compile_started=time.monotonic()
        for item in compile_plan(c,work):
            remaining=RESOURCE['compileTotalSeconds']-(time.monotonic()-compile_started)
            if remaining<1:raise ValueError('fourteen-entry compile budget exhausted')
            _,compile_code=command('compile-'+item['role'],clean(testenv)+item['argv'],min(1200,int(remaining)))
            if compile_code:raise ValueError('actual main compile failed: '+item['role'])
            binary=Path(item['output']);info=binary.lstat()
            if not stat.S_ISREG(info.st_mode) or binary.is_symlink() or info.st_nlink!=1 or info.st_uid!=os.getuid():
                raise ValueError('owned output identity differs')
            raw=regular(binary)
            if not raw.startswith(b'\x7fELF'):raise ValueError('actual main ELF output required')
            compiled.append({'role':item['role'],'package':item['package'],'bytes':len(raw),'sha256':sha(raw),'executed':False})
            write(output/('compiled-entry-'+item['role']+'.json'),compiled[-1])
        write(output/'compiled-entries.json',{'completed':len(compiled),'entries':compiled,'uploadedBinaries':False,'executed':False})
        names = sorted({n for selected in c['targets'].values() for n in selected})
        pattern = '^(' + '|'.join(re.escape(n) for n in names) + ')$'
        args = clean(testenv) + ['go', 'test', '-mod=readonly', '-p=1', '-parallel=1', '-race', '-json', '-count=1', '-vet=off', '-timeout=180s', '-run', pattern] + ['./' + p for p in c['targets']]
        folder, code = command('targeted-tests', args, 2250)
        try:
            events = [json.loads(line) for line in regular(folder / 'stdout.jsonl').decode().splitlines() if line.strip()]
            result = dict(require_children(audit(events, code, c['targets']), c), accepted=True)
        except (ValueError, TypeError, KeyError, UnicodeError):
            result = {'accepted': False, 'exitCode': code, 'originalRawRetained': True}
    write(output / 'summary.json', {'state': 'targeted_pass' if result['accepted'] else 'failed_or_incomplete', 'actualEntryBuilds':len(compiled),'currentEntryBuildReexecuted':True,'binariesUploaded':False,'pendingTargets':c['pendingTargets'],'full14GraphOrV2TokenFixtureAccepted':False, 'scope': SCOPE, 'result': result, 'sourcePostExact': c['source'], 'sourceModesAndLocksPostExact': True, 'runtimeChanged': False, 'fullRegression': False, 'fixtureCollectCleanup': 'subsequent workflow steps must also pass', 'networkBoundary': 'clean environment and exact owned loopback fixture; no kernel egress isolation claim'})
    return 0 if result['accepted'] else 1

if __name__ == '__main__':
    p=argparse.ArgumentParser(); p.add_argument('--verify',action='store_true'); a=p.parse_args()
    if a.verify:
        c,_,_=verify(); print(json.dumps({'source':c['source'],'newTops':len(pairs(c['newTargets'])),'affectedTops':len(pairs(c['directlyAffectedTargets']))+len(pairs(c['directExistingTargets'])),'reusedHistoricalTops':len(pairs(c['reusedTargets'])),'pendingTargets':c['pendingTargets'],'actualEntries':14,'GoExecuted':0,'DBExecuted':0}))
    else: raise SystemExit(run())
