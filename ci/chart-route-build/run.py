#!/usr/bin/env python3
"""Six missing product entry builds and one embedded-template parser regression."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import sys
import time

REPO = Path(__file__).resolve().parents[2]
HERE = Path(__file__).resolve().parent
GUARD = REPO / 'ci/group-effects/resource_guard.py'
CAPTURE = REPO / 'ci/group-effects/capture.py'
TEST = 'TestEmbeddedImportTemplateParsesOneCompleteUser'
ENTRIES = [('chart', './cmd/api/chat-api', 'chat-api'),
           ('chart', './cmd/api/admin-api', 'admin-api'),
           ('chart', './cmd/rpc/chat-rpc', 'chat-rpc'),
           ('chart', './cmd/rpc/admin-rpc', 'admin-rpc'),
           ('chart', './cmd/rpc/office-rpc', 'office-rpc'),
           ('route', '.', 'route-service')]
PER_BINARY_LIMIT = 128 * 1024**2
TOTAL_BINARY_LIMIT = 512 * 1024**2


def sha(body):
    return hashlib.sha256(body).hexdigest()


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':')).encode()


def regular(path):
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
        raise ValueError('regular single-link source required')
    body = path.read_bytes()
    end = path.stat()
    if (info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns) != (end.st_dev, end.st_ino, end.st_size, end.st_mtime_ns):
        raise ValueError('source changed during read')
    return body


def sources(root, module):
    selected = {str(p.relative_to(root)): regular(p) for p in sorted((root / module).rglob('*.go'))}
    extras = ['go.mod', 'go.sum'] + (['pkg/common/config/version', 'pkg/common/config/template.xlsx'] if module == 'chart' else [])
    for name in extras:
        selected[module + '/' + name] = regular(root / module / name)
    return selected


def vector(files):
    rows = [{'path': n, 'bytes': len(b), 'sha256': sha(b)} for n, b in sorted(files.items())]
    return {'members': len(rows), 'sha256': sha(canonical(rows))}


def verify(root=REPO):
    cfg = json.loads(regular(HERE / 'inputs.json'))
    if cfg['entries'] != [list(row) for row in ENTRIES] or cfg['test'] != TEST:
        raise ValueError('exact six entries and one regression required')
    if cfg['artifactPolicy'] != {'binaryArtifactEnabled': True, 'perBinaryLimitBytes': PER_BINARY_LIMIT,
                                 'totalBinaryLimitBytes': TOTAL_BINARY_LIMIT,
                                 'allowedBasenames': sorted([name for _, _, name in ENTRIES] + ['manifest.json']),
                                 'onlyAfterSixBuildsParserAndPost': True, 'scriptClaimsUploadOrDeployment': False}:
        raise ValueError('bounded six-ELF artifact policy differs')
    files = {}
    for module in ('chart', 'route'):
        actual = sources(root, module)
        if vector(actual) != cfg['sources'][module]:
            raise ValueError(module + ' build-source vector differs')
        if any(sha(actual[module + '/' + n]) != cfg['locks'][module][n] for n in ('go.mod', 'go.sum')):
            raise ValueError('original module locks differ')
        if b'go 1.27.2\n' not in actual[module + '/go.mod']:
            raise ValueError('original Go version differs')
        files.update(actual)
    for name, expected in cfg['helpers'].items():
        if sha(regular(REPO / name)) != expected:
            raise ValueError('original guard or capture differs')
    return cfg, files


def write(path, value):
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    with path.open('x') as stream:
        json.dump(value, stream, indent=2)
        stream.write('\n')


def controller_pins():
    names = ['ci/chart-route-build/run.py', 'ci/chart-route-build/inputs.json',
             '.github/workflows/chart-route-build.yml']
    return {name: sha(regular(REPO / name)) for name in names}


def tool_pins(go_file):
    directory = go_file.parents[1] / 'pkg/tool/linux_amd64'
    paths = [go_file] + [directory / name for name in ('compile', 'link', 'asm')]
    return {str(path): sha(regular(path)) for path in paths}


def binary_metadata(path, module, package, source):
    # No symlink traversal or size-limited truncation: oversize is an error.
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, 'rb') as stream:
        info = os.fstat(stream.fileno())
        if (not stat.S_ISREG(info.st_mode) or info.st_nlink != 1
                or info.st_uid != os.getuid() or info.st_size > PER_BINARY_LIMIT
                or info.st_mode & (stat.S_ISUID | stat.S_ISGID | stat.S_ISVTX | 0o022)
                or not info.st_mode & 0o111):
            raise ValueError('owned single-link bounded executable required')
        digest, size, header = hashlib.sha256(), 0, b''
        while chunk := stream.read(1024 * 1024):
            if not header:
                header = chunk[:20]
            size += len(chunk)
            if size > PER_BINARY_LIMIT:
                raise ValueError('binary exceeds128MiB')
            digest.update(chunk)
        final, current = os.fstat(stream.fileno()), path.lstat()
        identity = lambda value: (value.st_dev, value.st_ino, value.st_uid, value.st_gid,
                                  value.st_mode, value.st_nlink, value.st_size,
                                  value.st_mtime_ns, value.st_ctime_ns)
        if identity(info) != identity(final) or identity(info) != identity(current) or size != info.st_size:
            raise ValueError('binary changed while sealing')
    if header[:6] != b'\x7fELF\x02\x01' or header[16:18] not in (b'\x02\x00', b'\x03\x00') or header[18:20] != b'\x3e\x00':
        raise ValueError('actual Linux amd64 ELF required')
    return {'module': module, 'package': package, 'name': path.name, 'sourceVector': source,
            'bytes': size, 'sha256': digest.hexdigest(), 'mode': stat.S_IMODE(info.st_mode),
            'GOOS': 'linux', 'GOARCH': 'amd64', 'CGO_ENABLED': '0'}


def prepare_binary_manifest(directory, completed, cfg, tools, controllers, source_post):
    if source_post is not True:
        raise ValueError('exact source/helper/tool post required')
    info = directory.lstat()
    expected = {name for _, _, name in ENTRIES}
    if (not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid()
            or directory.resolve() != directory or {p.name for p in directory.iterdir()} != expected
            or len(completed) != 6):
        raise ValueError('only exact six owned binaries may be packaged')
    final = [binary_metadata(directory / name, module, package, cfg['sources'][module])
             for module, package, name in ENTRIES]
    if final != completed or sum(row['bytes'] for row in final) > TOTAL_BINARY_LIMIT:
        raise ValueError('binary bodies changed or total exceeds512MiB')
    manifest = {'schema': 1, 'sourceVectors': cfg['sources'], 'locks': cfg['locks'],
                'head': os.environ['GITHUB_SHA'], 'run': os.environ['GITHUB_RUN_ID'],
                'attempt': os.environ['GITHUB_RUN_ATTEMPT'], 'goVersion': 'go1.27.2 linux/amd64',
                'toolSHA256': tools, 'controllerSHA256': controllers,
                'sourceHelperToolPostExact': True, 'parserRegression': TEST,
                'binaries': final, 'totalBinaryBytes': sum(row['bytes'] for row in final),
                'perBinaryLimitBytes': PER_BINARY_LIMIT, 'totalBinaryLimitBytes': TOTAL_BINARY_LIMIT,
                'uploadedOrDeployedByThisScript': False, 'configsLogsOrSourceIncluded': False}
    write(directory / 'manifest.json', manifest)
    if {p.name for p in directory.iterdir()} != expected | {'manifest.json'}:
        raise ValueError('release directory contains unapproved files')
    manifest_file = directory / 'manifest.json'
    regular(manifest_file)
    if manifest_file.stat().st_uid != os.getuid():
        raise ValueError('manifest owner differs')
    return manifest


def test_summary(path):
    starts, terminals, package = [], [], []
    for line in regular(path).decode().splitlines():
        row = json.loads(line)
        if not isinstance(row, dict) or row.get('Package') != 'github.com/OpenIMSDK/chat/pkg/common/config':
            raise ValueError('unexpected test event package')
        name, action = row.get('Test'), row.get('Action')
        if name is not None and name != TEST:
            raise ValueError('unselected test executed')
        if action == 'run' and name:
            starts.append(name)
        if action in ('pass', 'fail', 'skip'):
            if name:
                terminals.append((name, action))
            else:
                package.append(action)
    if starts != [TEST] or terminals != [(TEST, 'pass')] or package != ['pass']:
        raise ValueError('one actual parser regression must pass with no skips')
    return {'top': TEST, 'actualPassed': 1, 'skipped': 0}


def run():
    if sys.platform != 'linux' or os.getuid() == 0 or os.environ.get('GITHUB_ACTIONS') != 'true':
        raise ValueError('nonroot hosted Linux execution only')
    run_id, attempt = os.environ.get('GITHUB_RUN_ID', ''), os.environ.get('GITHUB_RUN_ATTEMPT', '')
    if not run_id.isdigit() or not attempt.isdigit() or not re.fullmatch('[0-9a-f]{40}', os.environ.get('GITHUB_SHA', '')):
        raise ValueError('exact run/attempt/head required')
    cfg, original = verify()
    go_file = Path(shutil.which('go')).resolve(strict=True)
    tools = tool_pins(go_file)
    controls = controller_pins()
    version = subprocess.check_output([str(go_file), 'version'], text=True, timeout=10).strip()
    if version != 'go version go1.27.2 linux/amd64':
        raise ValueError('fixed Go compiler required')
    temporary = Path(os.environ['RUNNER_TEMP'])
    work = temporary / ('chart-route-build-' + run_id + '-' + attempt)
    if not temporary.is_absolute() or temporary.resolve() != temporary or work.exists() or os.statvfs(temporary).f_bavail * os.statvfs(temporary).f_frsize < 4 * 1024**3:
        raise ValueError('fresh owned directory and 4GiB admission required')
    work.mkdir(mode=0o700)
    os.umask(0o077)
    output = REPO / 'ci-results/chart-route-build'
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    started = time.monotonic()
    for name, body in original.items():
        path = work / 'source' / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(body)
        path.chmod(0o600)
    env = {'PATH': os.environ['PATH'], 'HOME': os.environ['HOME'], 'GOTOOLCHAIN': 'local', 'GOWORK': 'off', 'GOENV': 'off', 'GOAUTH': 'off', 'CGO_ENABLED': '0', 'GOOS': 'linux', 'GOARCH': 'amd64', 'GOMAXPROCS': '2', 'GOMEMLIMIT': '900MiB', 'LUOBO_EMAIL_INTEGRATION': '0'}
    for key, directory in [('GOCACHE', 'buildcache'), ('GOMODCACHE', 'modcache'), ('GOPATH', 'gopath'), ('TMPDIR', 'tmp')]:
        path = work / directory
        path.mkdir(mode=0o700)
        env[key] = str(path)
    env['GOTMPDIR'] = env['TMPDIR']
    write(output / 'source-pre.json', {'sources': cfg['sources'], 'locks': cfg['locks'], 'head': os.environ['GITHUB_SHA'], 'run': run_id, 'attempt': attempt, 'goVersion': version, 'toolSHA256': tools, 'controllerSHA256': controls})
    completed, passed, source_post, primary = [], None, False, None
    def command(label, module, argv, values, seconds):
        remaining = 1800 - (time.monotonic() - started)
        if remaining < 1:
            raise ValueError('total1800s budget exhausted')
        seconds = max(1, int(min(seconds, remaining)))
        folder = output / label
        folder.mkdir(mode=0o700)
        launch = [sys.executable, '-B', str(CAPTURE), '--stdout', str(folder / 'stdout.jsonl'), '--stderr', str(folder / 'stderr.log'), '--', 'env', '-i'] + [k + '=' + v for k, v in values.items()] + argv
        wrapped = [sys.executable, '-B', str(GUARD), '--cwd', str(work / 'source' / module), '--log', str(folder / 'guard.log'), '--record', str(folder / 'resource.json'), '--min-free-mib', '1024', '--poll-interval', '.2', '--timeout', str(seconds), '--allocated-root', str(work), '--max-allocated-mib', '8192', '--'] + launch
        result = subprocess.run(wrapped, check=False, timeout=seconds + 30)
        record = json.loads(regular(folder / 'resource.json'))
        if result.returncode != 0 or record.get('status') != 'passed' or record.get('exit_code') != 0:
            raise ValueError('guarded phase failed: ' + label)
        return folder
    try:
        for module in ('chart', 'route'):
            command('download-' + module, module, [str(go_file), 'mod', 'download', '-json'], dict(env, GOPROXY='https://proxy.golang.org', GOSUMDB='sum.golang.org', GOPRIVATE='', GONOPROXY='', GONOSUMDB=''), 600)
            for name in ('go.mod', 'go.sum'):
                if regular(work / 'source' / module / name) != original[module + '/' + name]:
                    raise ValueError('module download changed original lock')
        offline = dict(env, GOPROXY='off', GOSUMDB='off')
        folder = command('embedded-template-parser', 'chart', [str(go_file), 'test', '-mod=readonly', '-p=1', '-json', '-count=1', '-run', '^' + TEST + '$', '-timeout=60s', './pkg/common/config'], offline, 300)
        passed = test_summary(folder / 'stdout.jsonl')
        (work / 'bin').mkdir(mode=0o700)
        for module, package, name in ENTRIES:
            binary = work / 'bin' / name
            command('build-' + name, module, [str(go_file), 'build', '-mod=readonly', '-p=1', '-trimpath', '-buildvcs=false', '-ldflags=-s -w', '-o', str(binary), package], offline, 600)
            completed.append(binary_metadata(binary, module, package, cfg['sources'][module]))
            write(output / ('binary-' + name + '.json'), completed[-1])
    except BaseException as error:
        primary = error
        raise
    finally:
        try:
            # Every category is attempted even after another postcheck fails.
            checks, errors = {}, {}
            operations = []
            for module in ('chart', 'route'):
                expected = {n: b for n, b in original.items() if n.startswith(module + '/')}
                operations += [('source-' + module, lambda m=module, e=expected: sources(REPO, m) == e),
                               ('staged-' + module, lambda m=module, e=expected: sources(work / 'source', m) == e)]
            operations += [('helpers', lambda: {name: sha(regular(REPO / name)) for name in cfg['helpers']} == cfg['helpers']),
                           ('controllers', lambda: controller_pins() == controls),
                           ('tools', lambda: tool_pins(go_file) == tools)]
            for name, operation in operations:
                try:
                    checks[name] = operation() is True
                except BaseException as error:
                    checks[name] = False
                    errors[name] = type(error).__name__
            source_post = all(checks.values())
            write(output / 'source-post.json', {'exact': source_post, 'checks': checks, 'exceptions': errors, 'sources': cfg['sources'], 'locks': cfg['locks'], 'toolSHA256': tools, 'controllerSHA256': controls})
            if not source_post and primary is None:
                raise ValueError('source or locks changed')
        finally:
            write(output / 'summary.json', {'actualEntryBuilds': len(completed), 'expectedEntryBuilds': 6, 'binaries': completed, 'totalBinaryBytes': sum(row['bytes'] for row in completed), 'actualParserRegression': passed, 'binaryUploadClaimedByScript': False, 'configsOrCredentialsIncluded': False, 'server14Built': False, 'fullRegression': False, 'servicesStarted': False, 'deploymentAccepted': False})
    if len(completed) != 6 or passed is None:
        raise ValueError('six builds and parser test required')
    manifest = prepare_binary_manifest(work / 'bin', completed, cfg, tools, controls, source_post)
    write(output / 'binary-artifact-prepared.json', {'prepared': True, 'manifestSHA256': sha(regular(work / 'bin/manifest.json')), 'binaryCount': 6, 'totalBinaryBytes': manifest['totalBinaryBytes'], 'uploadedOrDeployedByScript': False})


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--verify', action='store_true')
    args = parser.parse_args()
    if args.verify:
        cfg, _ = verify()
        print(json.dumps({'sources': cfg['sources'], 'entries': ENTRIES, 'test': TEST, 'GoExecuted': False}))
    else:
        run()
