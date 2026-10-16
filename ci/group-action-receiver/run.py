#!/usr/bin/env python3
"""Manual-only, exact source1165 pure13. No SQL/Mongo/Kafka/vendor fixture."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess
import sys
import time

REPO = Path(__file__).resolve().parents[2]
INPUTS = Path(__file__).with_name('inputs.json')
PREFIX = 'github.com/openimsdk/open-im-server/v3/'
SCOPE = 'group_action_receiver_source1165_pure13_only_not_runtime'
PURE13 = {'cmd/tools/ordinary-callback-receipts': ['TestOwnedGroupOperatorInspectsAndConfirmsExactHighRevisionAndRejectsStale', 'TestOwnedGroupOperatorUnknownDoesNotReplayAndRejectsNestedAmbiguity'], 'internal/deliveryprofile': ['TestOwnedCallbackDefaultTimeoutKeepsSignedBinding', 'TestOwnedCutFrozenHeadShapeCannotOverflow'], 'internal/msgtransfer': ['TestOwnedGroupActionLegacyRowDoesNotStarveIndependentSource', 'TestOwnedGroupActionStopBeforeStartClosesAdmissionDuringBlockedPoolClose', 'TestOwnedGroupActionStopCancelsConcurrentReadinessBeforeClosingPools', 'TestOwnedGroupActionTimedOutStopRetainsJoinOwnershipAndNeverClosesLiveTransport', 'TestOwnedGroupBootstrapPrivateExactBindingAndDisabledCallbacks'], 'internal/push': ['TestOwnedBusinessAfterRegistryRequiresCurrentAndRetainedSigningChannels'], 'internal/rpc/group': ['TestGroupOwnedLifecycleDoesNotCloseUnjoinedWorker', 'TestGroupOwnedLifecycleJoinsBeforeClosingPools', 'TestGroupOwnedLifecycleReportsCleanupFailure']}


def sha(body):
    return hashlib.sha256(body).hexdigest()


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
        raise ValueError('frozen input differs')
    return raw


def rows(bodies):
    return [{'path': p, 'bytes': len(b), 'sha256': sha(b)} for p, b in sorted(bodies.items())]


def digest(items):
    return sha(b''.join(r['path'].encode() + b'\0' + r['sha256'].encode() + b'\0'
                        for r in sorted(items, key=lambda r: r['path'])))


def apply_overlay(c, previous):
    result = dict(previous); seen = set()
    for row in c['overlay']:
        target = name(row['target'])
        if (not target.startswith('server/') or not target.endswith('.go') or target in seen
                or row['path'] != 'ci/candidates/group-action-receiver/source/' + target
                or row['mode'] != '0o644'
                or row['beforeSHA256'] != (sha(previous[target]) if target in previous else None)):
            raise ValueError('overlay target or exact parent mismatch')
        if (REPO / row['path']).stat().st_mode & 0o777 != 0o644:
            raise ValueError('overlay mode differs')
        seen.add(target); result[target] = pinned(row)
    source = REPO / 'ci/candidates/group-action-receiver/source'
    actual = {str(p.relative_to(REPO)) for p in source.rglob('*') if p.is_file() or p.is_symlink()}
    if actual != {r['path'] for r in c['overlay']}:
        raise ValueError('overlay has extra or missing source')
    return result


def verify():
    c = json.loads(regular(INPUTS))
    if (c['schema'] != 1 or c['scope'] != SCOPE or c['parentCount'] != 1133
            or c['effectiveCount'] != 1165 or len(c['overlay']) != 58
            or c['parentRunner']['path'] != 'ci/group-ordinary/run.py'
            or c['parentInputs']['path'] != 'ci/group-ordinary/inputs.json'
            or c['packages'] != PURE13):
        raise ValueError('exact manual pure13 source contract required')
    for row in c['dependencies']: pinned(row)
    pinned(c['parentInputs']); pinned(c['parentRunner'])
    spec = importlib.util.spec_from_file_location('accepted_ordinary1133_source_parent', REPO / c['parentRunner']['path'])
    parent = importlib.util.module_from_spec(spec); spec.loader.exec_module(parent)
    prior, before = parent.verify()  # source-only; never calls parent's run/full/fixtures
    if len(before) != 1133 or prior['effective_digest'] != c['parentDigest']:
        raise ValueError('accepted1133 source parent differs')
    bodies = apply_overlay(c, before)
    if len(bodies) != 1165 or rows(bodies) != c['effectiveSource'] or digest(rows(bodies)) != c['effectiveDigest']:
        raise ValueError('whole1165 source composition differs')
    if any(r['target'].endswith('_test.go') and r['beforeSHA256'] is not None for r in c['overlay']):
        raise ValueError('existing parent test file changed')
    if sum(r['beforeSHA256'] is None for r in c['overlay']) != 32:
        raise ValueError('exact26 modified plus32 new required')
    for package, selected in c['packages'].items():
        name(package)
        if len(selected) != len(set(selected)) or sorted(selected) != selected:
            raise ValueError('unique exact test names required')
        all_names = []
        for path, body in bodies.items():
            if str(PurePosixPath(path).parent) == 'server/' + package and path.endswith('_test.go'):
                all_names.extend(x.decode() for x in re.findall(rb'^func (Test\w+)\(', body, re.M))
        if not set(selected) <= set(all_names):
            raise ValueError('selected test not present in actual source')
    return c, bodies


def audit(events, code, package, selected):
    starts, terminals, footer = {}, {}, []
    for event in events:
        if not isinstance(event, dict) or event.get('Package') != PREFIX + package or footer:
            raise ValueError('foreign or post-terminal JSON event')
        action, test = event.get('Action'), event.get('Test')
        if action in ('fail', 'skip') or any(text in event.get('Output', '') for text in ('WARNING: DATA RACE','panic:','fatal error:')):
            raise ValueError('test/build/race/skip failure')
        if test:
            if test not in selected or '/' in test:
                raise ValueError('unrequested test or subtest')
            if action == 'run': starts[test] = starts.get(test, 0) + 1
            if action == 'pass': terminals[test] = terminals.get(test, 0) + 1
        elif action == 'pass': footer.append(event)
    expected = {n: 1 for n in selected}
    if code != 0 or starts != expected or terminals != expected or len(footer) != 1:
        raise ValueError('missing/duplicate exact tests or package terminal')
    return {'topPASS': len(selected), 'subPASS': 0, 'skip': 0, 'fail': 0,
            'names': sorted(selected), 'footer': footer[0]}


def require_guard(record, code):
    if (record.get('status') not in ('passed', 'failed') or record.get('exit_code') is None
            or code != record['exit_code'] or (record['status'] == 'passed') != (code == 0)):
        raise ValueError('resource/timeout/monitor/remaining-process failure')


def write(path, value):
    with path.open('x') as out:
        json.dump(value, out, indent=2); out.write('\n'); out.flush(); os.fsync(out.fileno())
    directory = os.open(path.parent, os.O_RDONLY)
    try: os.fsync(directory)
    finally: os.close(directory)


def cloud():
    if any(os.environ.get(k) != v for k, v in {'GITHUB_ACTIONS':'true', 'RUNNER_ENVIRONMENT':'github-hosted',
            'RUNNER_OS':'Linux', 'GOTOOLCHAIN':'local', 'GOWORK':'off'}.items()):
        raise ValueError('GitHub-hosted Linux manual lane only')
    if (os.getuid() == 0 or any(not re.fullmatch('[0-9]+', os.environ.get(k, '')) for k in ('GITHUB_RUN_ID','GITHUB_RUN_ATTEMPT')) or not re.fullmatch('[0-9a-f]{40}', os.environ.get('GITHUB_SHA',''))):
        raise ValueError('exact nonroot cloud owner required')
    if any(k.startswith('LUOBO_') and k not in ('LUOBO_EMAIL_INTEGRATION', 'LUOBO_RUN_LEGACY_LOAD_TESTS',
            'LUOBO_RUN_LEGACY_EXAMPLES') for k in os.environ):
        raise ValueError('no business fixture environment allowed')


def run():
    started = time.monotonic()
    cloud(); c, bodies = verify()
    version = subprocess.check_output(['go', 'version'], text=True, timeout=15).strip()
    if version != 'go version go1.27.2 linux/amd64':
        raise ValueError('exact compiler required')
    temporary = Path(os.environ['RUNNER_TEMP'])
    work = temporary / ('group-action-receiver-pure13-' + os.environ['GITHUB_RUN_ID'] + '-' + os.environ['GITHUB_RUN_ATTEMPT'])
    if not temporary.is_absolute() or temporary.resolve() != temporary or work.exists():
        raise ValueError('fresh canonical work root required')
    space = os.statvfs(temporary)
    if space.f_bavail * space.f_frsize < 4 * 1024**3:
        raise ValueError('4GiB initial gate; no child started')
    work.mkdir(mode=0o700)
    output = REPO / 'ci-results/group-action-receiver'
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    for target, body in bodies.items():
        path = work / target; path.parent.mkdir(parents=True, exist_ok=True)
        with path.open('xb') as out: out.write(body)
        path.chmod(c['sourceModes'][target])
    for key, folder in [('GOMODCACHE','modcache'), ('GOCACHE','buildcache'), ('TMPDIR','tmp'), ('GOPATH','gopath')]:
        path = work / folder; path.mkdir(mode=0o700); os.environ[key] = str(path)
    os.environ.update(GOTOOLCHAIN='local', GOWORK='off', GOMAXPROCS='2', GOMEMLIMIT='900MiB',
                      CGO_ENABLED='1', GOTMPDIR=os.environ['TMPDIR'])
    write(output / 'source.json', {'scope':SCOPE, 'commit':os.environ['GITHUB_SHA'], 'inputsSHA256':sha(regular(INPUTS)),
          'parent1133Digest':c['parentDigest'], 'effectiveCount':1165, 'effectiveDigest':c['effectiveDigest'],
          'goVersion':version, 'actualExpectedNames':c['packages'], 'noRuntimeAdoption':True})

    def command(label, argv, timeout):
        remaining = 1800 - (time.monotonic() - started)
        if remaining < 1: raise ValueError('whole30minute controller budget exhausted')
        timeout = max(1, int(min(timeout, remaining)))
        folder = output / label; folder.mkdir()
        capture = [sys.executable, '-B', str(REPO / 'ci/group-effects/capture.py'),
                   '--stdout', str(folder / 'stdout.jsonl'), '--stderr', str(folder / 'stderr.log'), '--'] + argv
        wrapped = [sys.executable, '-B', str(REPO / 'ci/group-effects/resource_guard.py'), '--cwd', str(work / 'server'),
                   '--log', str(folder / 'guard.log'), '--record', str(folder / 'resource.json'), '--min-free-mib', '1024',
                   '--timeout', str(timeout), '--poll-interval', '.2', '--allocated-root', str(work),
                   '--max-allocated-mib', '16384', '--'] + capture
        child = subprocess.run(wrapped, check=False, timeout=timeout + 30)
        resource = json.loads(regular(folder / 'resource.json')); require_guard(resource, child.returncode)
        return folder, resource['exit_code']

    # Cloud-only public dependency preparation; tests below get GOPROXY=off
    # inside a fresh network namespace with no host loopback or vendor access.
    folder, code = command('module-download', ['go', 'mod', 'download', '-json'], 600)
    if code != 0: raise ValueError('public locked dependency preparation failed')
    if bodies['server/go.mod'] != regular(work / 'server/go.mod') or bodies['server/go.sum'] != regular(work / 'server/go.sum'):
        raise ValueError('module preparation changed locks')
    uid, gid = os.getuid(), os.getgid(); host_net = os.stat('/proc/self/ns/net').st_ino
    environment = {'PATH':os.environ['PATH'], 'GOROOT':subprocess.check_output(['go','env','GOROOT'], text=True, timeout=15).strip(),
        'GOTOOLCHAIN':'local', 'GOWORK':'off', 'GOPROXY':'off', 'GOSUMDB':'off', 'GOENV':'off',
        'GOCACHE':os.environ['GOCACHE'], 'GOPATH':os.environ['GOPATH'], 'GOMODCACHE':os.environ['GOMODCACHE'], 'GOTMPDIR':os.environ['TMPDIR'],
        'TMPDIR':os.environ['TMPDIR'], 'CGO_ENABLED':'1', 'GOMAXPROCS':'2', 'GOMEMLIMIT':'900MiB',
        'LUOBO_EMAIL_INTEGRATION':'0', 'LUOBO_RUN_LEGACY_LOAD_TESTS':'0', 'LUOBO_RUN_LEGACY_EXAMPLES':'0'}
    isolation = ['sudo','-n','unshare','--net','--','setpriv','--reuid='+str(uid),'--regid='+str(gid),
                 '--clear-groups','--','env','-i'] + [k+'='+v for k,v in environment.items()]
    probe = 'import os,sys;sys.exit(0 if os.getuid()==int(sys.argv[1]) and os.stat("/proc/self/ns/net").st_ino!=int(sys.argv[2]) else 1)'
    _, code = command('network-isolation', isolation + [sys.executable,'-c',probe,str(uid),str(host_net)], 15)
    if code != 0: raise ValueError('fresh nonroot network isolation unavailable; tests not started')
    outcomes = {}; errors = []
    for package, selected in c['packages'].items():
        expression = '^(' + '|'.join(selected) + ')$'
        argv = isolation + ['go','test','-mod=readonly','-p=1','-parallel=1','-race','-json','-count=1',
                            '-vet=off','-timeout=60s','-run',expression,'./'+package]
        folder, code = command(package.replace('/','_'), argv, 900)
        try:
            events = [json.loads(line) for line in regular(folder / 'stdout.jsonl').decode().splitlines() if line.strip()]
            outcomes[package] = audit(events, code, package, selected)
        except (ValueError, UnicodeError):
            outcomes[package] = {'accepted':False, 'exit':code, 'rawRetained':True}; errors.append(package)
        write(folder / 'summary.json', outcomes[package])
    for target, body in bodies.items():
        if regular(work / target) != body or (work / target).stat().st_mode & 0o777 != c['sourceModes'][target]: raise ValueError('source/mode changed during pure13')
    verify()
    accepted = not errors and sum(x['topPASS'] for x in outcomes.values()) == 13
    write(output / 'summary.json', {'state':'pure13_passed' if accepted else 'failed_or_incomplete', 'scope':SCOPE,
          'sourceCount':1165, 'effectiveDigest':c['effectiveDigest'], 'packages':outcomes,
          'testsOnly':True, 'wholeRegression':False, 'vetRun':False, 'SQLMongoKafkaVendorFixture':False,
          'runtimeSwitch':False, 'workRetainedForEphemeralRunnerCleanup':True})
    return 0 if accepted else 1


if __name__ == '__main__':
    parser = argparse.ArgumentParser(); parser.add_argument('--verify', action='store_true'); args = parser.parse_args()
    if args.verify:
        c, _ = verify(); print('Exact source1165/pure13 verified; no Go/fixture execution.')
    else:
        raise SystemExit(run())
