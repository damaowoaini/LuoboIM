#!/usr/bin/env python3
"""Exact formatted source repair5 in three processes; prior-source66+7+10 and graph obligations retained."""
import argparse
from collections import Counter
from contextlib import contextmanager
import hashlib, importlib.util, json, os
from pathlib import Path, PurePosixPath
import re, stat, subprocess, sys, time
REPO = Path(__file__).resolve().parents[2]
INPUTS = Path(__file__).with_name('inputs.json')
PREFIX = "github.com/openimsdk/open-im-server/v3/"
SCOPE = 'backend_history_repair5_split3_only_reuse_source_bound66_and7_and10_prior14_no_deployment'
BASE = {'members': 1407, 'digest': '5359b5cae2c92096dd2e0548926c6439c96583b46be1b03ce9b78df3aa91a2f9'}
FINAL = {'members': 1556, 'digest': '1e12a108dc0949d3b3df232a2a422e2562f3013eb23cfb5d92e7595366d39132'}
SOURCE_ROOT = 'ci/candidates/backend-runtime-join-r8/source/'
SELECTION_SHA = 'e027f5e42844d67777ecd2ba446974f63b403f9533f8393b821cea78a6382e9c'
RESOURCE = {'initialBytes': 4294967296, 'floorMiB': 1024, 'allocatedMiB': 16384, 'pollSeconds': .2, 'controllerSeconds': 3000, 'compileTotalSeconds': 1200, 'testPhaseSeconds': 2250, 'testGoTimeoutSeconds': 180}
MONGO = 'mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration'

REUSE_PROOF_SHA = 'a2273b8d18c15fc98fb65317067d89584a44c4570b899e619672f6ea47826a97'
PENDING_POSITIVE = ('pkg/common/startrpc','TestOwnedPreparedReaderRegistersBeforeServeAndRetainsFinalWriterFence')
PENDING_TOKEN = ('pkg/common/startrpc','TestOwnedPushActualOwnerClosesOriginalResourcesBeforeRedisUnknownNonpermit')
PREVIOUS_REPAIR_PROOF_SHA = '096dadcfbd249cc64e36f90e0361a13577908e1fc1a778543a8cc4023b2fe543'
REPAIR_DELTA_SHA = '2c417cbaaa6e9b44af83cafbe31b1c33dc9d0511deafcafffce867aa68124f5c'
DIRECT_PRIOR = {('internal/namespaceimport/ownedcontrol', 'TestNamespaceRuntimeActualMongoTargetRejectsReplacementAndForeignClient')}
DIRECT_CURRENT = {('internal/namespaceimport/ownedcontrol', 'TestHistoricalSourceProducesSignedPagesWithoutInventingMissingHistory'), ('internal/namespaceimport/ownedcontrol', 'TestHistoricalAmbiguousCurrentRosterCannotDiscardProvenBaseOrAdmitFuture'), ('internal/namespaceimport/ownedcontrol', 'TestHistoricalUnattributedEvidenceIsInspectableAndCannotSilentlyPublish')}
LATEST_REPAIR_PROOF_SHA = '2b6e93f014f13bf8e0a7830b7b94f15b02149f78c24229066b03629c3bbae3ff'
REPAIR_SCOPES = [{'label': 'targeted-tests-messagedelivery', 'targets': {'internal/messagedelivery': ['TestGroupHistoryMongoOriginalGenerationExportsClosedRangeWithoutLeaveGap', 'TestOriginalGroupHistoryMongoFiniteRangeCannotLaunderTargetedNotice', 'TestOriginalHistoryMongoPreparedPagerReusesCandidateButNeverSequencePermission']}}, {'label': 'targeted-tests-ownedcontrol-controller', 'targets': {'internal/namespaceimport/ownedcontrol': ['TestHistoricalSourceMongoControllerPublishesExactPagesAndKeepsReadinessSeparate']}}, {'label': 'targeted-tests-ownedcontrol-target', 'targets': {'internal/namespaceimport/ownedcontrol': ['TestNamespaceRuntimeActualMongoTargetRejectsReplacementAndForeignClient']}}]



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


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':')).encode()


def check_repair(c, p):
    proof = p['acceptedPreviousRun']; current = p['previousRepairRun']; latest = p['latestRepairRun']
    old = {'members':1555,'digest':'686ba84ba24fd1c9e9c5e54bc9f27a41e74baf78eb0a0118cbc10044f14ee09f'}
    before = {'members':1555,'digest':'68f9418e1540a4d4122edd227ff0ccaee2a96feb6d30f29a5b507acfd7e86f55'}
    if (p.get('schema') != 1 or p.get('state') != 'FROZEN_REPAIR5' or p.get('source') != c['source']
            or sha(canonical(proof)) != REUSE_PROOF_SHA or p['reuseProofSHA256'] != REUSE_PROOF_SHA
            or proof['source'] != old or proof['run'] != 39041607488 or proof['attempt'] != 1
            or proof['head'] != '6384db596a54e1d27e3db7369f39fbd55e9b8867'
            or proof['actualTopPASS'] != 67 or proof['actualTopFAIL'] != 22
            or not proof['sourceModesLocksPostExact'] or not proof['fixtureNoResidueAndOwnedCleanupPASS']
            or sha(canonical(current)) != PREVIOUS_REPAIR_PROOF_SHA or p['previousRepairProofSHA256'] != PREVIOUS_REPAIR_PROOF_SHA
            or current['source'] != before or current['run'] != 39043896233 or current['attempt'] != 1
            or current['head'] != '230a16a43a9d1a44c9a6477119a73b2ea4ff04bb'
            or current['actualTopStarted'] != 21 or current['actualTopPASS'] != 10
            or current['actualTopFAIL'] != 10 or current['actualTopIncomplete'] != 1
            or not current['sourceModesLocksGeneratedConfigAndRepairControllerPostExact']
            or current['wholeFixtureNoResidueAccepted'] is not False or current['mongoResidualDatabaseCount'] != 1
            or current['RedisResidualKeys'] != 0 or current['exactOwnedContainerCleanupExitCode'] != 0
            or sha(canonical(latest)) != LATEST_REPAIR_PROOF_SHA or p['latestRepairProofSHA256'] != LATEST_REPAIR_PROOF_SHA
            or latest['source'] != {'members':1556,'digest':'c108a37cac20e390c99411e2b92dc8368cde204f85be9ac0347a0907f1ea7cec'}
            or latest['run'] != 39046539739 or latest['attempt'] != 1
            or latest['head'] != '1dbe0412baa8a3a04199a5ab80a559dcdf126e65'
            or (latest['actualTopStarted'], latest['actualTopPASS'], latest['actualTopFAIL'], latest['actualTopIncomplete'], latest['actualTopUnstarted']) != (15,10,5,0,0)
            or not latest['allActualPassedTopChildrenCompleteAndRequiredExact']
            or not latest['sourceModesLocksGeneratedConfigAndRepairControllerPostExact']
            or not latest['wholeFixtureNoResidueAccepted'] or latest['mongoResidualDatabaseCount'] != 0
            or latest['RedisResidualKeys'] != 0 or latest['exactOwnedContainerCleanupExitCode'] != 0
            or latest['currentEntryBuilds'] != 0 or latest['runtimeGraphAccepted'] is not False
            or p['currentMainBuilds'] != 0 or p['reusedMainBuilds'] != 14
            or p['newBinaries'] is not False or p['runtimeGraphAccepted'] is not False or p['fullRegression'] is not False):
        raise ValueError('actual source-bound evidence or repair-only boundary differs')
    universe = pairs(c['targets'])
    passed, failed = pairs(proof['passedTargets']), pairs(proof['failedTargets'])
    current_pass, current_fail, current_incomplete = pairs(current['passedTargets']), pairs(current['failedTargets']), pairs(current['incompleteTargets'])
    active, reused, pending = pairs(p['repairTargets']), pairs(p['actualPassedTargets']), pairs(p['pendingPositiveTargets'])
    old_direct, current_direct = pairs(p['directlyAffectedPriorTargets']), pairs(p['directlyAffectedCurrentTargets'])
    latest_pass, latest_fail = pairs(latest['passedTargets']), pairs(latest['failedTargets'])
    if (len(universe) != 89 or len(passed) != 67 or len(failed) != 22 or passed & failed
            or passed | failed != universe or pending != {PENDING_POSITIVE}
            or len(current_pass) != 10 or len(current_fail) != 10 or len(current_incomplete) != 1
            or current_pass & (current_fail | current_incomplete) or current_fail & current_incomplete
            or current_pass | current_fail | current_incomplete != failed - pending
            or old_direct != DIRECT_PRIOR or current_direct != DIRECT_CURRENT
            or not old_direct <= passed or not current_direct <= current_pass
            or latest_pass & latest_fail or len(latest_pass) != 10 or len(latest_fail) != 5
            or pairs(latest['incompleteTargets']) or latest_pass | latest_fail != current_fail | current_incomplete | old_direct | current_direct
            or active != latest_fail or len(active) != 5
            or reused != (passed - old_direct) | (current_pass - current_direct) | latest_pass or len(reused) != 83
            or active & (reused | pending) or active | reused | pending != universe
            or pairs(p['pendingRequiredTokenTargets']) != {PENDING_TOKEN} or pairs(c['pendingTargets']) != {PENDING_TOKEN}
            or p['sourceReuse'] != [{'source':old,'run':39041607488,'retainedTopCount':66,'derivation':'acceptedPreviousRun.passedTargets minus directlyAffectedPriorTargets'}, {'source':before,'run':39043896233,'retainedTopCount':7,'derivation':'previousRepairRun.passedTargets minus directlyAffectedCurrentTargets'}, {'source':latest['source'],'run':39046539739,'retainedTopCount':10,'derivation':'latestRepairRun.passedTargets; exact original source only'}]):
        raise ValueError('repair5/reuse66+7+10/positive1/required-token1 must retain exact original names')
    entries = proof['compiledEntries']
    if (entries['completed'] != 14 or entries['uploadedBinaries'] is not False or entries['executed'] is not False
            or [(r['role'],r['package']) for r in entries['entries']] != [(r['role'],r['package']) for r in c['entrypoints']]):
        raise ValueError('old fourteen actual distinct entry evidence differs')
    if (len(p['sourceDelta']) != 2 or len({r['target'] for r in p['sourceDelta']}) != 2
            or p['sourceDeltaSHA256'] != REPAIR_DELTA_SHA or sha(canonical(p['sourceDelta'])) != REPAIR_DELTA_SHA):
        raise ValueError('exact formatted two-source repair required')
    if p['executionScopes'] != REPAIR_SCOPES:
        raise ValueError('exact MD3 and isolated ownedcontrol1+1 process scopes required')
    scoped = [pair for scope in REPAIR_SCOPES for pair in pairs(scope['targets'])]
    if len(scoped) != 5 or len(set(scoped)) != 5 or set(scoped) != active:
        raise ValueError('repair process scopes must partition exactly the five failed names')
    overlay = {r['target']:r for r in c['overlay']}
    for row in p['sourceDelta']:
        now = overlay.get(row['target'])
        if (now is None or now['sha256'] != row['afterSHA256'] or now['bytes'] != row['afterBytes']
                or row['beforeSHA256'] is not None and (not re.fullmatch('[0-9a-f]{64}',row['beforeSHA256']) or row['beforeSHA256'] == row['afterSHA256'])):
            raise ValueError('repair delta must bind actual current capsule and original source preimage')


def repair_execution_plan(c, p):
    check_repair(c, p)
    targets = p['repairTargets']
    required = {package:{top:children for top,children in names.items() if top in targets.get(package,[])}
                for package,names in c['requiredSubtests'].items()}
    required = {package:names for package,names in required.items() if names}
    scopes = [{'label':scope['label'], 'targets':scope['targets'],
               'requiredSubtests':{package:{top:children for top,children in required.get(package,{}).items()
                                          if top in tops}
                                  for package,tops in scope['targets'].items()}}
              for scope in p['executionScopes']]
    return {'compile': [], 'targets': targets, 'requiredSubtests': required, 'scopes': scopes}


def execute_repair_scopes(active, command, environment, output):
    # One original2250s phase budget, three real Go test processes. A normal
    # Go nonzero still retains/audits that scope and runs the remaining scopes.
    # The original command guard raises on latched resource/owner failures.
    deadline = time.monotonic() + RESOURCE['testPhaseSeconds']
    results = []
    for scope in active['scopes']:
        remaining = int(deadline - time.monotonic())
        if remaining < 1:
            raise ValueError('original test phase budget exhausted')
        names = sorted({name for tops in scope['targets'].values() for name in tops})
        pattern = '^(' + '|'.join(re.escape(name) for name in names) + ')$'
        args = ['env', '-i'] + [key+'='+value for key,value in environment.items()]
        args += ['go', 'test', '-mod=readonly', '-p=1', '-parallel=1', '-race', '-json', '-count=1', '-vet=off', '-timeout=180s', '-run', pattern]
        args += ['./'+package for package in scope['targets']]
        folder, code = command(scope['label'], args, remaining)
        raw = regular(folder/'stdout.jsonl')
        try:
            events = [json.loads(line) for line in raw.decode().splitlines() if line.strip()]
            result = dict(require_children(audit(events, code, scope['targets']), scope), accepted=True)
        except (ValueError, TypeError, KeyError, UnicodeError) as error:
            result = {'accepted':False,'exitCode':code,'originalRawRetained':True,
                      'exceptionType':type(error).__name__,'targets':scope['targets']}
        result.update(label=scope['label'], stdoutSHA256=sha(raw), stdoutBytes=len(raw))
        write(folder/'result.json',result)
        results.append(result)
    accepted = len(results) == 3 and all(result['accepted'] for result in results)
    union = {'accepted':accepted, 'phaseBudgetSeconds':RESOURCE['testPhaseSeconds'],
             'goTestTimeoutSeconds':RESOURCE['testGoTimeoutSeconds'], 'processes':results,
             'topPASS':sum(result.get('topPASS',0) for result in results),
             'subPASS':sum(result.get('subPASS',0) for result in results),
             'namedCompletions':[entry for result in results if result['accepted']
                                 for entry in result['namedCompletions']],
             'allSelectedFiveAccepted':accepted,'originalRawRetained':True}
    write(output/'targeted-tests-union.json',union)
    return union


def load_repair_plan(c):
    p = json.loads(pinned(c['repairPlan']))
    check_repair(c, p)
    for row in c['repairControllerPins']: pinned(row)
    return p


@contextmanager
def retain_repair_source_post(work, output, c, bodies, template):
    primary = None
    try:
        with retain_materialized_source_post(work, output, c, bodies, template):
            yield
    except BaseException as error:
        primary = error
        raise
    finally:
        try:
            record = {'accepted':False,'source':c['source']}
            try:
                load_repair_plan(c)
                record['accepted'] = True
            except Exception as error:
                record['exceptionType'] = type(error).__name__
            write(output/'repair-plan-post.json',record)
            if not record['accepted']:
                raise ValueError('repair-plan-post failed; original raw retained')
        except Exception:
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
    repair = load_repair_plan(c)
    active = repair_execution_plan(c, repair)
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
    with retain_repair_source_post(work, output, c, bodies, template):
        env = {'PATH': os.environ['PATH'], 'GOROOT': subprocess.check_output(['go', 'env', 'GOROOT'], text=True, timeout=15).strip(), 'GOTOOLCHAIN': 'local', 'GOWORK': 'off', 'GOENV': 'off', 'CGO_ENABLED': '1', 'HOME': os.environ['HOME'], 'GOAUTH': 'off', 'GOMAXPROCS': '2', 'GOMEMLIMIT': '900MiB'}
        for key, folder in [('GOMODCACHE', 'modcache'), ('GOCACHE', 'buildcache'), ('GOPATH', 'gopath'), ('TMPDIR', 'tmp')]:
            path = work / folder
            path.mkdir(mode=0o700)
            env[key] = str(path)
        env['GOTMPDIR'] = env['TMPDIR']
        write(output / 'source.json', {'scope': SCOPE, 'commit': os.environ['GITHUB_SHA'], 'run': os.environ['GITHUB_RUN_ID'], 'attempt': os.environ['GITHUB_RUN_ATTEMPT'], 'inputsSHA256': sha(regular(INPUTS)), 'source': c['source'], 'targets': active['targets'], 'completeUniverseTargets': c['targets'], 'currentEntryBuilds': 0, 'goVersion': version, 'runtimeChanged': False, 'fullRegression': False})

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
        write(output/'compiled-entries.json', {'completed': 0, 'currentEntryBuildReexecuted': False, 'reusedActual14BuildSource': repair['acceptedPreviousRun']['source'], 'reusedActual14BuildRun': repair['acceptedPreviousRun']['run'], 'uploadedBinaries': False, 'executed': False})
        result = execute_repair_scopes(active, command, testenv, output)
    write(output / 'summary.json', {'state': 'repair5_split3_targeted_pass' if result['accepted'] else 'failed_or_incomplete', 'actualEntryBuilds': 0, 'currentEntryBuildReexecuted': False, 'newBinaries': False, 'reusedActualEntryBuilds': 14, 'reusedBuildSource': repair['acceptedPreviousRun']['source'], 'reusedBuildRun': repair['acceptedPreviousRun']['run'], 'previousActualTopPASSReused': 66, 'previousRepairTopPASSReused': 7, 'latestRepairTopPASSReused': 10, 'reusedPASSByOriginalSource': repair['sourceReuse'], 'currentTargets': active['targets'], 'completeUniverseTargets': c['targets'], 'pendingPositiveTargets': repair['pendingPositiveTargets'], 'pendingRequiredTokenTargets': repair['pendingRequiredTokenTargets'], 'full14GraphOrV2TokenFixtureAccepted': False, 'scope': SCOPE, 'result': result, 'sourcePostExact': c['source'], 'sourceModesAndLocksPostExact': True, 'runtimeChanged': False, 'fullRegression': False, 'fixtureCollectCleanup': 'subsequent workflow steps must also pass', 'networkBoundary': 'original clean environment and exact owned loopback fixture; no new egress isolation claim'})
    return 0 if result['accepted'] else 1

if __name__ == '__main__':
    p=argparse.ArgumentParser(); p.add_argument('--verify',action='store_true'); a=p.parse_args()
    if a.verify:
        c,_,_=verify(); repair=load_repair_plan(c); print(json.dumps({'source':c['source'],'newTops':len(pairs(c['newTargets'])),'affectedTops':len(pairs(c['directlyAffectedTargets']))+len(pairs(c['directExistingTargets'])),'reusedHistoricalTops':len(pairs(c['reusedTargets'])),'pendingTargets':c['pendingTargets'],'currentActualEntryBuilds':0,'priorSourceActualEntryBuildsReused':14,'currentTargets':5, 'retainedPriorSourceTop':66, 'retainedPreviousRepairSourceTop':7, 'retainedLatestRepairSourceTop':10,'pendingPositiveTargets':repair['pendingPositiveTargets'],'GoExecuted':0,'DBExecuted':0}))
    else: raise SystemExit(run())
