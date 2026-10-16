#!/usr/bin/env python3
"""Only retry the pure failed collision fixture; no DB/full/regression/runtime."""
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
SCOPE = 'namespace_inventory_collision_fixture_target1_only_reuse_prior7_24_no_runtime'
PREFIX = 'github.com/openimsdk/open-im-server/v3/'
PACKAGE = 'internal/namespaceinventory'
TOP = 'TestInventoryPreservesCollisionAndErasedEvidence'
TARGET = 'server/internal/namespaceinventory/capture_test.go'
EXPECTED = {'tops': [TOP], 'subs': []}
OLD_NOTICE = b'\tnotice := table.MsgDocModel{DocID: "n_a_b:0", Msg: []*table.MsgInfoModel{{Msg: &table.MsgDataModel{SendID: "a", RecvID: "b", SessionType: constant.NotificationChatType, Seq: 1}}}}\n'
NEW_NOTICE = b'\tnotice := table.MsgDocModel{DocID: "n_a_b:0", Msg: []*table.MsgInfoModel{{Msg: &table.MsgDataModel{SendID: "a", RecvID: "b", SessionType: constant.NotificationChatType, Options: msgprocessor.NewOptions(msgprocessor.WithNotNotification(false)), Seq: 1}}}}\n'
ROWS_ANCHOR = b'\trows := []MongoRow{rawRow(t, "msg", "single", doc), rawRow(t, "msg", "notice", notice), rawRow(t, "group", "group", bson.M{"group_id": "a_b"})}\n'
PRECONDITION = b'\tif msgprocessor.Options(notice.Msg[0].Msg.Options).IsNotNotification() {\n\t\tt.Fatal("synthetic n_ fixture must explicitly select notification options")\n\t}\n'
REUSED = {'run': 38975531275,
 'job': 117223944022,
 'head': '60b1d128faf853912cb2a77ba999e34ecc321a8b',
 'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909',
 'rootPartialReceiptSHA256': '1d2d0bbd0c09328f2dc59e37611898edf6d4dd4cb629d45e9b3c5763476e8b18',
 'acceptedTops7': [{'package': 'internal_namespaceinventory',
                    'name': 'TestInventoryRejectsChangeRestartAndLimits',
                    'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                   {'package': 'internal_namespaceinventory',
                    'name': 'TestInventoryCounterClassesAndMissingHeadNeverInferZero',
                    'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                   {'package': 'internal_namespaceinventory',
                    'name': 'TestInventoryPrivateRoundTripAndNoClobber',
                    'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                   {'package': 'internal_namespaceinventory',
                    'name': 'TestInventoryRejectsPrivateTamperAndPathAliases',
                    'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                   {'package': 'internal_namespaceinventory',
                    'name': 'TestInventoryPrivateConnectionIsExplicitBoundInput',
                    'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                   {'package': 'internal_namespaceinventory',
                    'name': 'TestNamespaceInventoryOwnedStoresReadOnly',
                    'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                   {'package': 'cmd_tools_group-namespace-capture',
                    'name': 'TestNamespaceCaptureCLIInspectAndSafeErrors',
                    'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'}],
 'acceptedSubs24': [{'package': 'internal_namespaceinventory',
                     'name': 'TestInventoryRejectsChangeRestartAndLimits/changed-document',
                     'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                    {'package': 'internal_namespaceinventory',
                     'name': 'TestInventoryRejectsChangeRestartAndLimits/mongo-restart',
                     'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                    {'package': 'internal_namespaceinventory',
                     'name': 'TestInventoryRejectsChangeRestartAndLimits/redis-failover',
                     'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                    {'package': 'internal_namespaceinventory',
                     'name': 'TestInventoryRejectsChangeRestartAndLimits/changed-redis-head',
                     'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                    {'package': 'internal_namespaceinventory',
                     'name': 'TestInventoryRejectsChangeRestartAndLimits/bounded-before-retention',
                     'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                    {'package': 'internal_namespaceinventory',
                     'name': 'TestInventoryCounterClassesAndMissingHeadNeverInferZero/raw-counter',
                     'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                    {'package': 'internal_namespaceinventory',
                     'name': 'TestInventoryCounterClassesAndMissingHeadNeverInferZero/schema-with-maximum',
                     'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                    {'package': 'internal_namespaceinventory',
                     'name': 'TestInventoryCounterClassesAndMissingHeadNeverInferZero/nonstring-kind',
                     'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                    {'package': 'internal_namespaceinventory',
                     'name': 'TestInventoryCounterClassesAndMissingHeadNeverInferZero/negative',
                     'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                    {'package': 'internal_namespaceinventory',
                     'name': 'TestInventoryCounterClassesAndMissingHeadNeverInferZero/overflow',
                     'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                    {'package': 'internal_namespaceinventory',
                     'name': 'TestInventoryRejectsPrivateTamperAndPathAliases/changed-derived-data',
                     'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                    {'package': 'internal_namespaceinventory',
                     'name': 'TestInventoryRejectsPrivateTamperAndPathAliases/duplicate-json-field',
                     'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                    {'package': 'internal_namespaceinventory',
                     'name': 'TestInventoryRejectsPrivateTamperAndPathAliases/world-readable',
                     'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                    {'package': 'internal_namespaceinventory',
                     'name': 'TestInventoryRejectsPrivateTamperAndPathAliases/hard-link',
                     'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                    {'package': 'internal_namespaceinventory',
                     'name': 'TestInventoryRejectsPrivateTamperAndPathAliases/symlink-parent',
                     'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                    {'package': 'internal_namespaceinventory',
                     'name': 'TestInventoryPrivateConnectionIsExplicitBoundInput/valid-explicit',
                     'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                    {'package': 'internal_namespaceinventory',
                     'name': 'TestInventoryPrivateConnectionIsExplicitBoundInput/wrong-database',
                     'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                    {'package': 'internal_namespaceinventory',
                     'name': 'TestInventoryPrivateConnectionIsExplicitBoundInput/wrong-replica-set',
                     'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                    {'package': 'internal_namespaceinventory',
                     'name': 'TestInventoryPrivateConnectionIsExplicitBoundInput/direct-primary-ambiguity',
                     'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                    {'package': 'internal_namespaceinventory',
                     'name': 'TestInventoryPrivateConnectionIsExplicitBoundInput/wrong-redis-database',
                     'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                    {'package': 'internal_namespaceinventory',
                     'name': 'TestInventoryPrivateConnectionIsExplicitBoundInput/duplicate-uri-option',
                     'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                    {'package': 'cmd_tools_group-namespace-capture',
                     'name': 'TestNamespaceCaptureCLIInspectAndSafeErrors/default-inspect-no-connection',
                     'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                    {'package': 'cmd_tools_group-namespace-capture',
                     'name': 'TestNamespaceCaptureCLIInspectAndSafeErrors/no-implicit-capture',
                     'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'},
                    {'package': 'cmd_tools_group-namespace-capture',
                     'name': 'TestNamespaceCaptureCLIInspectAndSafeErrors/no-activation-or-credential-flags',
                     'sourceDigest': '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'}],
 'priorPackageState': {'internal_namespaceinventory': 'failed_one_top_6top21sub_passed_partial_case_reuse_only',
                       'cmd_tools_group-namespace-capture': '1top3sub_and_package_footer_passed'},
 'rawPins': [{'path': 'namespace-inventory/internal_namespaceinventory/stdout.jsonl',
              'bytes': 30824,
              'sha256': '6529d3122116f596b1a53794dcb189086fa2aed5348956813c23f749d06c61fb'},
             {'path': 'namespace-inventory/cmd_tools_group-namespace-capture/stdout.jsonl',
              'bytes': 5168,
              'sha256': '5ee006e49cc74e78e80e63170e43d3abdf3216127e4b0598131ca018f180b399'},
             {'path': 'namespace-inventory/source.json',
              'bytes': 3481,
              'sha256': '3a24c5b9329692d017e59b0b1e95b02b78355b383b7630990158eee6b25de0da'},
             {'path': 'namespace-inventory/source-post.json',
              'bytes': 578,
              'sha256': '9d89cefd4fbef3aa901779f3ad85b4c7e1a7dbf6c1d74ef96bc3cf256b2530f9'},
             {'path': 'backend-integration/residual-check.json',
              'bytes': 451,
              'sha256': 'f51f18373267df70bb24348c60f6ffe35baba7502209d1cd9c23c68f517b6b95'},
             {'path': 'backend-integration/container-cleanup.json',
              'bytes': 79,
              'sha256': '1e8af2429fc836352d3a2ecb6160fcb61e328d9e79ed6aa5f621a4f6873b7c55'}],
 'claim': 'Previously Root-verified per-case PASS metadata only, not new execution or prior full internal package '
          'PASS. No raw verification logs are published in this payload.'}

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


def pinned(row):
    raw = regular(REPO / name(row['path']))
    if len(raw) != row['bytes'] or sha(raw) != row['sha256']:
        raise ValueError('pinned input differs')
    return raw


def rows(bodies):
    return [{'path': p, 'bytes': len(b), 'sha256': sha(b)} for p, b in sorted(bodies.items())]


def digest(items):
    return sha(b''.join(r['path'].encode() + b'\0' + r['sha256'].encode() + b'\0'
                        for r in sorted(items, key=lambda r: r['path'])))


def apply_fixture(c, previous):
    if len(c['overlay']) != 1 or TARGET not in previous:
        raise ValueError('one existing collision fixture required')
    row = c['overlay'][0]
    if (row['target'] != TARGET or row['path'] != 'ci/candidates/namespace-inventory-collision/source/' + TARGET
            or row['mode'] != '0o644' or row['beforeSHA256'] != sha(previous[TARGET])):
        raise ValueError('exact prior collision fixture required')
    path = REPO / row['path']
    if stat.S_IMODE(path.stat().st_mode) != 0o644:
        raise ValueError('fixture mode differs')
    original = previous[TARGET]
    if original.count(OLD_NOTICE) != 1 or original.count(ROWS_ANCHOR) != 1:
        raise ValueError('unique frozen fixture anchors required')
    expected = original.replace(OLD_NOTICE, NEW_NOTICE, 1).replace(ROWS_ANCHOR, PRECONDITION + ROWS_ANCHOR, 1)
    updated = pinned(row)
    if updated != expected:
        raise ValueError('production/helper/other-test/assertion change forbidden')
    prefix = REPO / 'ci/candidates/namespace-inventory-collision/source'
    if {str(p.relative_to(REPO)) for p in prefix.rglob('*') if p.is_file() or p.is_symlink()} != {row['path']}:
        raise ValueError('extra collision overlay path')
    result = dict(previous); result[TARGET] = updated
    return result


def verify():
    c = json.loads(regular(INPUTS))
    if (c['schema'] != 1 or c['scope'] != SCOPE or c['parentCount'] != 1184 or c['effectiveCount'] != 1184
            or c['parentDigest'] != '47d710a1afee53f3ce0551a3e8e16851e545c857e39b74ea915d062ea2ec8909'
            or c['parentRunner']['path'] != 'ci/namespace-inventory/run.py'
            or c['parentInputs']['path'] != 'ci/namespace-inventory/inputs.json'
            or c['reuse'] != REUSED or c['expected'] != {'newTopPASS': 1, 'newSubPASS': 0, 'priorTopPASS': 7, 'priorSubPASS': 24, 'skip': 0, 'packages': 1, 'vet': False, 'fullRegression': False}):
        raise ValueError('exact failed1 / reused7+24 contract required')
    for row in c['dependencies']: pinned(row)
    pinned(c['parentInputs']); pinned(c['parentRunner'])
    spec = importlib.util.spec_from_file_location('source_only_previous_inventory1184', REPO / c['parentRunner']['path'])
    parent = importlib.util.module_from_spec(spec); spec.loader.exec_module(parent)
    prior, previous = parent.verify()  # Source only; no old target8/Go/fixture execution.
    if len(previous) != 1184 or prior['effectiveDigest'] != c['parentDigest']:
        raise ValueError('previous1184 differs')
    bodies = apply_fixture(c, previous)
    if len(bodies) != 1184 or rows(bodies) != c['effectiveSource'] or digest(rows(bodies)) != c['effectiveDigest'] or c['sourceModes'] != prior['sourceModes']:
        raise ValueError('fixture successor composition differs')
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
    exact = Counter({n: 1 for n in wanted})
    if code != 0 or starts != exact or passes != exact or len(footer) != 1:
        raise ValueError('missing duplicate or failed exact selection')
    return {'topPASS': len(expected['tops']), 'subPASS': len(expected['subs']), 'skip': 0, 'fail': 0,
            'tops': sorted(expected['tops']), 'subs': sorted(expected['subs']), 'footer': footer[0]}


def require_guard(record, code):
    if (record.get('status') not in ('passed', 'failed') or record.get('exit_code') is None
            or code != record['exit_code'] or (record['status'] == 'passed') != (code == 0)):
        raise ValueError('guard/monitor/timeout/remaining-process rejection')


def write(path, value):
    with path.open('x') as out:
        json.dump(value, out, indent=2)
        out.write('\n')
        out.flush()
        os.fsync(out.fileno())
    fd = os.open(path.parent, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def cloud(env):
    for key, value in {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux', 'GOTOOLCHAIN': 'local', 'GOWORK': 'off'}.items():
        if env.get(key) != value: raise ValueError('hosted Linux controller only')
    if os.getuid() == 0 or any(not re.fullmatch('[0-9]+', env.get(k, '')) for k in ('GITHUB_RUN_ID', 'GITHUB_RUN_ATTEMPT')) or not re.fullmatch('[0-9a-f]{40}', env.get('GITHUB_SHA', '')):
        raise ValueError('exact nonroot run/head required')
    allowed = {'LUOBO_EMAIL_INTEGRATION': '0', 'LUOBO_RUN_LEGACY_LOAD_TESTS': '0', 'LUOBO_RUN_LEGACY_EXAMPLES': '0'}
    if any(env.get(k) != v for k, v in allowed.items()) or any(k.startswith('LUOBO_') and k not in allowed for k in env):
        raise ValueError('no DB/vendor fixture environment permitted')


def run():
    started = time.monotonic(); cloud(os.environ); c, bodies = verify()
    version = subprocess.check_output(['go', 'version'], text=True, timeout=15).strip()
    if version != 'go version go1.27.2 linux/amd64': raise ValueError('exact Go required')
    temporary = Path(os.environ['RUNNER_TEMP'])
    work = temporary / ('namespace-collision-' + os.environ['GITHUB_RUN_ID'] + '-' + os.environ['GITHUB_RUN_ATTEMPT'])
    if not temporary.is_absolute() or temporary.resolve() != temporary or work.exists(): raise ValueError('fresh canonical work required')
    v = os.statvfs(temporary)
    if v.f_bavail * v.f_frsize < 4 * 1024**3: raise ValueError('4GiB bootstrap floor; no Go test child')
    work.mkdir(mode=0o700); output = REPO / 'ci-results/namespace-inventory-collision'; output.mkdir(mode=0o700, parents=True, exist_ok=False)
    for target, body in bodies.items():
        p = work / target; p.parent.mkdir(parents=True, exist_ok=True)
        with p.open('xb') as f: f.write(body)
        p.chmod(c['sourceModes'][target])
    template = bodies['server/config/config.example.yaml']; (work / 'server/config/config.yaml').write_bytes(template)
    env = {'PATH':os.environ['PATH'], 'GOROOT':subprocess.check_output(['go', 'env', 'GOROOT'], text=True, timeout=15).strip(), 'GOTOOLCHAIN':'local', 'GOWORK':'off', 'GOENV':'off', 'CGO_ENABLED':'1', 'GOMAXPROCS':'2', 'GOMEMLIMIT':'900MiB'}
    if 'HOME' in os.environ: env['HOME'] = os.environ['HOME']
    for key, folder in [('GOMODCACHE','modcache'), ('GOCACHE','buildcache'), ('GOPATH','gopath'), ('TMPDIR','tmp')]:
        p = work / folder; p.mkdir(mode=0o700); env[key] = str(p)
    env['GOTMPDIR'] = env['TMPDIR']; locks = {p:bodies[p] for p in ('server/go.mod','server/go.sum')}
    write(output / 'source.json', {'scope':SCOPE, 'commit':os.environ['GITHUB_SHA'], 'count':1184, 'digest':c['effectiveDigest'], 'parentDigest':c['parentDigest'], 'Go':version, 'locksBefore':rows(locks), 'selected':[TOP], 'priorCaseMetadataOnly':REUSED})
    def command(label, argv, timeout):
        remaining = 1800 - (time.monotonic() - started)
        if remaining < 1: raise ValueError('whole budget exhausted')
        timeout = max(1, int(min(timeout, remaining))); folder = output / label; folder.mkdir()
        capture = [sys.executable,'-B',str(REPO / 'ci/group-effects/capture.py'),'--stdout',str(folder / 'stdout.jsonl'),'--stderr',str(folder / 'stderr.log'),'--'] + argv
        wrapped = [sys.executable,'-B',str(REPO / 'ci/group-effects/resource_guard.py'),'--cwd',str(work / 'server'),'--log',str(folder / 'guard.log'),'--record',str(folder / 'resource.json'),'--min-free-mib','1024','--timeout',str(timeout),'--poll-interval','.2','--allocated-root',str(work),'--max-allocated-mib','16384','--'] + capture
        child = subprocess.run(wrapped, check=False, timeout=timeout + 30); record = json.loads(regular(folder / 'resource.json')); require_guard(record, child.returncode); return folder, record['exit_code']
    clean = lambda values:['env','-i'] + [k+'='+v for k,v in values.items()]
    download = dict(env, GOPROXY='https://proxy.golang.org', GOSUMDB='sum.golang.org', GOAUTH='off', GOPRIVATE='', GONOPROXY='', GONOSUMDB='')
    _, code = command('module-download', clean(download) + ['go','mod','download','-json'], 600)
    if code or any(regular(work / p) != b for p,b in locks.items()): raise ValueError('locked public module preparation failed')
    uid,gid = os.getuid(),os.getgid(); host_net = os.stat('/proc/self/ns/net').st_ino
    test_env = dict(env,GOPROXY='off',GOSUMDB='off',LUOBO_EMAIL_INTEGRATION='0',LUOBO_RUN_LEGACY_LOAD_TESTS='0',LUOBO_RUN_LEGACY_EXAMPLES='0')
    isolation = ['sudo','-n','unshare','--net','--','setpriv','--reuid='+str(uid),'--regid='+str(gid),'--clear-groups','--'] + clean(test_env)
    probe = 'import os,sys;sys.exit(0 if os.getuid()==int(sys.argv[1]) and os.stat("/proc/self/ns/net").st_ino!=int(sys.argv[2]) else 1)'
    _,code = command('network-isolation',isolation+[sys.executable,'-c',probe,str(uid),str(host_net)],15)
    if code: raise ValueError('nonroot empty network namespace unavailable; no test dispatch')
    folder,code = command('collision-only',isolation+['go','test','-mod=readonly','-p=1','-parallel=1','-race','-json','-count=1','-vet=off','-timeout=60s','-run','^'+TOP+'$','./'+PACKAGE],900)
    accepted=False
    try:
        result=audit([json.loads(x) for x in regular(folder/'stdout.jsonl').decode().splitlines() if x.strip()],code,PACKAGE,EXPECTED);accepted=True
    except (ValueError,UnicodeError,TypeError,KeyError): result={'accepted':False,'exit':code,'rawRetained':True}
    write(folder/'summary.json',result)
    for target,body in bodies.items():
        p=work/target
        if regular(p)!=body or stat.S_IMODE(p.stat().st_mode)!=c['sourceModes'][target]:raise ValueError('source/mode changed')
    if regular(work/'server/config/config.yaml')!=template:raise ValueError('public config changed')
    verify();write(output/'source-post.json',{'count':1184,'digest':digest(rows(bodies)),'allBodiesModesExact':True,'locksAfter':rows(locks)})
    write(output/'summary.json',{'state':'pure_collision_fixture_target1_passed' if accepted else 'failed_or_incomplete','scope':SCOPE,'newExecuted':{'topPASS':1 if accepted else 0,'subPASS':0},'priorCaseReuse':REUSED,'fresh8OrWholePackageClaimed':False,'SQLMongoRedisDockerStarted':False,'vet':False,'fullRegression':False,'runtime':False,'workRetainedOnEphemeralRunner':True})
    return 0 if accepted else 1


if __name__=='__main__':
    parser=argparse.ArgumentParser();parser.add_argument('--verify',action='store_true');args=parser.parse_args()
    try:
        if args.verify:verify();print('Exact1184 pure collision successor verified; no Go/DB execution.')
        else:raise SystemExit(run())
    except (ValueError,KeyError,TypeError,OSError,subprocess.SubprocessError,json.JSONDecodeError):
        print('Pure collision validation incomplete; original raw retained.',file=sys.stderr);raise SystemExit(2)
