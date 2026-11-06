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
COHORT = 'admin-session-live4-chart5-v1'
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
        code, _, guard = command(root, 'module-download', [go, 'mod', 'download', 'all'], source, env, 600)
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
        if before_tools is not None:
            try:
                source_exact = tree(source) == inputs['source']
                source_post = source_exact
                tools_exact = toolset(go) == before_tools
                write(output / 'source-tools.json', {'sourceMembers': len(inputs['source']),
                    'sourceDigest': inputs['sourceDigest'], 'sourceAndLocksPostExact': source_exact,
                    'toolsBeforeAndAfterExact': tools_exact, 'tools': before_tools,
                    'GoVersion': 'go1.27.2 linux/amd64', 'sourceOrPrivateLogsExported': False})
                if not source_exact or not tools_exact:
                    failure = failure or {'phase': 'post-source-tools', 'class': 'ValueError', 'rawErrorExported': False}
            except BaseException:
                failure = failure or {'phase': 'post-source-tools', 'class': 'other', 'rawErrorExported': False}
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
        'failure': failure, 'cleanupAccepted': cleanup.get('accepted') is True,
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
        result = fixture.Fixture(args.work, inputs['images']).cleanup()
        output = REPO / 'ci-results/admin-session'; output.mkdir(mode=0o700, exist_ok=True)
        write(output / 'cleanup-final.json', result)
        return 0 if result.get('accepted') is True else 1
    def interrupted(_signal, _frame): raise InterruptedError('owned targeted run interrupted')
    signal.signal(signal.SIGTERM, interrupted); signal.signal(signal.SIGINT, interrupted)
    return run(args.work, inputs, args.inputs_sha256)

if __name__ == '__main__':
    try: raise SystemExit(main())
    except (ValueError, OSError, KeyError, subprocess.SubprocessError, json.JSONDecodeError):
        print('Admin-session targeted operation NOT PASS; private diagnostics retained.', file=sys.stderr)
        raise SystemExit(2)
