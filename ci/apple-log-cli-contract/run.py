#!/usr/bin/env python3
"""Fixed static help outside worker sandbox, under original resource supervisor."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import pwd
import re
import selectors
import stat
import subprocess
import sys
import time
from types import SimpleNamespace

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
IDENTITY = 'apple-macos15-log-help-show-supervisor-v3'
WORK_NAME = 'luobo-apple-log-cli-supervisor-v3'
EXECUTION_SURFACE = {'help_process': 'resource-supervisor-without-worker-sandbox',
    'worker_sandbox_applied': False, 'same_original_diagnostic_rules': False,
    'file_write_network_sandbox_enforced_for_help': False,
    'fixed_static_help_only': True, 'system_log_query_executed': False}
COMMAND = ['/usr/bin/log', 'help', 'show']
HELP_EXIT_CONTRACT = {'allowedExitCodes': [0, 64], 'showUsageHeaders': ['usage: log show [options]', 'usage: log show [options] <archive>', 'usage: log show [options] [<archive>]', 'usage: log show [options] --archive <archive>', 'usage: log show [options] [--archive <archive>|--file <file>]'], 'allNineTokensRequired': True, 'usage64RequiresClosedUsageClass': True, 'permissionRefusalAlwaysRejected': True, 'exitCodeAloneGrantsNothing': True, 'historicalObservationReclassified': False}
CAP = 8192
SECONDS = 2
ORIGINAL_PATHS = {'gem_root': '/opt/homebrew/lib/ruby/gems/3.3.0',
    'ruby': {'path': '/opt/homebrew/Cellar/ruby@3.3/3.3.12/bin/ruby'},
    'script': {'path': '/opt/homebrew/lib/ruby/gems/3.3.0/bin/pod'}}
SUPPORT_TOKENS = {'no_pager': rb'--(?:no-pager|\[no-\]pager)',
    'style': rb'--style\b', 'ndjson': rb'\bndjson\b', 'color': rb'--color\b',
    'start': rb'--start\b', 'end': rb'--end\b', 'predicate': rb'--predicate\b',
    'info': rb'--(?:info|\[no-\]info)\b', 'debug': rb'--(?:debug|\[no-\]debug)\b'}
_HOST_FINGERPRINTER = None


def put(path, value):
    raw = (json.dumps(value, sort_keys=True, indent=2)+'\n').encode()
    if len(raw) > 65536:
        raise ValueError('bounded-record')
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'wb') as stream:
        stream.write(raw); stream.flush(); os.fsync(stream.fileno())


def source_body(row):
    path = REPO
    for part in row['path'].split('/'):
        if part in {'', '.', '..'}:
            raise ValueError('source-path')
        path = path/part
        if path.is_symlink():
            raise ValueError('source-alias')
    fields = lambda s: (s.st_dev, s.st_ino, s.st_mode, s.st_uid, s.st_gid,
        s.st_nlink, s.st_size, s.st_mtime_ns, s.st_ctime_ns)
    with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW), 'rb') as stream:
        before = os.fstat(stream.fileno())
        if not stat.S_ISREG(before.st_mode) or before.st_nlink != 1 or not 0 < before.st_size <= 2 << 20:
            raise ValueError('source-shape')
        raw = stream.read((2 << 20)+1)
        if fields(before) != fields(os.fstat(stream.fileno())) or fields(before) != fields(path.lstat()):
            raise ValueError('source-changed')
    if len(raw) != row['bytes'] or hashlib.sha256(raw).hexdigest() != row['sha256']:
        raise ValueError('source-body')
    return raw


def metadata():
    raw = (HERE/'inputs.json').read_bytes()
    if not 0 < len(raw) <= 65536 or (HERE/'inputs.json').is_symlink():
        raise ValueError('descriptor-shape')
    value = json.loads(raw)
    if (value.get('schema') != 1 or value.get('identity') != IDENTITY
            or value.get('executionSurface') != EXECUTION_SURFACE
            or value.get('command') != COMMAND or value.get('originalEnvironmentPaths') != ORIGINAL_PATHS
            or value.get('helpExitContract') != HELP_EXIT_CONTRACT
            or value.get('limits') != {'seconds': 2, 'outputBytes': 8192, 'floorBytes': 1 << 30,
                'allocatedBytes': 64 << 20, 'pollSeconds': .2, 'guardSeconds': 30}):
        raise ValueError('fixed-contract')
    for row in value['owned']+value['borrowed']:
        source_body(row)
    return value


def load(path, name):
    spec = importlib.util.spec_from_file_location(name, path)
    value = importlib.util.module_from_spec(spec); spec.loader.exec_module(value)
    return value


def controller_snapshot():
    spec = metadata()
    rows = [{'path': 'ci/apple-log-cli-contract/inputs.json',
        'bytes': (HERE/'inputs.json').stat().st_size,
        'sha256': hashlib.sha256((HERE/'inputs.json').read_bytes()).hexdigest()}]
    for row in spec['owned']+spec['borrowed']:
        source_body(row)
        rows.append(dict(row))
    return rows


def host():
    global _HOST_FINGERPRINTER
    if _HOST_FINGERPRINTER is None:
        metadata()
        _HOST_FINGERPRINTER = load(REPO/'ci/apple-host-tool/run.py', 'log_help_host_fingerprint')
    # Keep the original verified callable for independent tool post-checks,
    # even when the separate controller post-check later detects source drift.
    return _HOST_FINGERPRINTER


def tool_snapshot(name):
    if name == 'log':
        path, cap = Path('/usr/bin/log'), 32 << 20
    elif name == 'python':
        path, cap = Path(sys.executable).resolve(strict=True), 64 << 20
    else:
        raise ValueError('finite-tool')
    return host().fingerprint(path, cap, True)


def home_configuration_present(environment):
    home = Path(environment['HOME'])
    actual = Path(pwd.getpwuid(os.getuid()).pw_dir)
    if (home.resolve(strict=True) != actual or str(home) != str(actual)
            or not home.is_dir() or home.stat().st_uid != os.getuid()
            or environment['HOME'] != os.environ['HOME']):
        raise ValueError('original-home')
    return os.path.lexists(home/'.logrc')


def classify_static_help_stderr(body):
    """Classify only complete bounded literal stderr, never an exit-code cause.

    These finite grammars are observation labels. They do not reconstruct the
    old32-byte stderr, grant permission or classify arbitrary runtime log text.
    """
    if not isinstance(body, bytes) or len(body) > CAP:
        return 'unknown'
    try:
        body.decode('utf-8', 'strict')
    except UnicodeError:
        return 'unknown'
    patterns = {
        'sandbox_refusal': rb'(?:sandbox-exec: sandbox_(?:apply|init): operation not permitted|log: (?:not permitted|not allowed) (?:in|within) (?:a )?sandbox)\.?',
        'eperm': rb'(?:log: )?(?:operation not permitted|eperm)\.?',
        'usage': rb'usage: log(?: [^\r\n]*)?'}
    found = {name for name, pattern in patterns.items() for line in body.splitlines()
        if re.fullmatch(pattern, line.strip(), re.IGNORECASE)}
    return next(iter(found)) if len(found) == 1 else 'unknown'

def help_observation(work, environment):
    result = {'schema': 1, 'command': COMMAND, 'attempted': False,
        'status': 'inconclusive', 'reason': 'other', 'exit_code': None,
        'streams_complete': False, 'output_within_cap': True, 'supports': {},
        'support_false_means_unobserved_not_unsupported': True,
        'raw_help_or_system_log_retained': False, 'execution_surface': dict(EXECUTION_SURFACE),
        'stderr_class': 'unknown', 'stderr_class_is_literal_observation_only': True,
        'cause_inferred_from_exit_code': False, 'show_usage_header': False,
        'permission_refusal_observed': False, 'static_help_exit_contract_accepted': False}
    if home_configuration_present(environment):
        result.update(reason='user_log_configuration_present')
        return result
    child = None; selector = None; total = 0
    streams = {k: {'body': bytearray(), 'sha': hashlib.sha256(), 'bytes': 0} for k in ['stdout', 'stderr']}
    try:
        child = subprocess.Popen(COMMAND, cwd=work, env=environment, stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        result['attempted'] = True
        selector = selectors.DefaultSelector()
        for name in streams:
            selector.register(getattr(child, name), selectors.EVENT_READ, name)
        deadline = time.monotonic()+SECONDS
        while selector.get_map():
            remaining = deadline-time.monotonic()
            if remaining <= 0:
                raise TimeoutError()
            for key, _ in selector.select(min(.2, remaining)):
                body = os.read(key.fd, min(4096, CAP+1-total))
                if not body:
                    selector.unregister(key.fileobj); key.fileobj.close(); continue
                total += len(body)
                current = streams[key.data]
                current['bytes'] += len(body); current['sha'].update(body); current['body'].extend(body)
                if total > CAP:
                    result['output_within_cap'] = False
                    raise OverflowError()
        result['exit_code'] = child.wait(timeout=max(.001, deadline-time.monotonic()))
        result['streams_complete'] = True
        # Both complete bounded streams remain memory-only. The fixed help
        # command may return EX_USAGE64; neither that code nor generic usage
        # text proves the exact show help contract without its full evidence.
        body = bytes(streams['stdout']['body'])+b'\n'+bytes(streams['stderr']['body'])
        body.decode('utf-8', 'strict')
        result['supports'] = {name: bool(re.search(token, body)) for name, token in SUPPORT_TOKENS.items()}
        result['show_usage_header'] = any(line.strip() in {
            header.encode('ascii') for header in HELP_EXIT_CONTRACT['showUsageHeaders']}
            for line in body.splitlines())
        result['stderr_class'] = classify_static_help_stderr(bytes(streams['stderr']['body']))
        result['permission_refusal_observed'] = any(classify_static_help_stderr(line) in {
            'eperm','sandbox_refusal'} for line in body.splitlines())
        if result['exit_code'] not in HELP_EXIT_CONTRACT['allowedExitCodes']:
            result['reason'] = 'cli_nonzero'
        elif result['permission_refusal_observed']:
            result['reason'] = 'help_permission_refusal'
        elif not result['show_usage_header']:
            result['reason'] = 'missing_show_usage_header'
        elif not all(result['supports'].values()):
            result['reason'] = 'missing_help_tokens'
        elif result['exit_code'] == 64 and result['stderr_class'] != 'usage':
            result['reason'] = 'usage64_not_static_help'
        else:
            result.update(status='observed_complete_cli_help', reason='none',
                          static_help_exit_contract_accepted=True)
    except (TimeoutError, subprocess.TimeoutExpired):
        result['reason'] = 'timeout'
    except OverflowError:
        result['reason'] = 'output_limit'
    except UnicodeError:
        result['reason'] = 'invalid_utf8'
    except OSError:
        result['reason'] = 'os_error'
    finally:
        try:
            if child is not None and child.poll() is None:
                child.terminate()
                try:
                    child.wait(timeout=.5)
                except subprocess.TimeoutExpired:
                    child.kill(); child.wait(timeout=.5)
            if child is not None and result['exit_code'] is None:
                result['exit_code'] = child.returncode
        finally:
            if selector is not None:
                selector.close()
            if child is not None:
                for stream in [child.stdout, child.stderr]:
                    if stream is not None and not stream.closed:
                        stream.close()
            result['streams'] = {k: {'bytes': s['bytes'], 'sha256': s['sha'].hexdigest(),
                'complete': result['streams_complete']} for k, s in streams.items()}
            result['combined_observed_bytes'] = total
            if result['streams_complete'] and result['output_within_cap']:
                result['stderr_class'] = classify_static_help_stderr(bytes(streams['stderr']['body']))
    return result


def post_boundaries(work, before):
    outcomes = {}
    for name, snapshot in [('log', lambda: tool_snapshot('log')),
                           ('python', lambda: tool_snapshot('python')),
                           ('controllers', controller_snapshot)]:
        value = {'exact': False, 'reason': 'not_observed'}
        try:
            current = snapshot()
            value = {'exact': before.get(name) is not None and current == before[name], 'snapshot': current}
            if not value['exact']:
                value['reason'] = 'source_changed_or_missing_baseline'
        except Exception:
            value['reason'] = 'post_check_failed'
        finally:
            # A failed fingerprint never skips the other two independent
            # checks. No exception text or runtime response is retained.
            try:
                put(work/'evidence'/('post-'+name+'.json'), value)
            except Exception:
                value = {'exact': False, 'reason': 'post_record_failed'}
            outcomes[name] = value['exact']
    return outcomes


def env(work, selected, xcrun_adapter):
    work = Path(work)
    # Public selected paths are environment configuration only; no Pod/Ruby observation.
    pod = Path(selected['script']['path'])
    ruby = Path(selected['ruby']['path'])
    # No inherited credentials/proxies/Java/Ruby options, no HOME reassignment.
    e = {k: os.environ[k] for k in ['HOME', 'USER', 'LOGNAME'] if k in os.environ}
    e.update(PATH=str(work/'tool-adapters')+':'+str(work/'flutter/bin')+':'+str(pod.parent)+':'+str(ruby.parent)+':/usr/bin:/bin:/usr/sbin',
             GEM_HOME=selected['gem_root'], GEM_PATH=selected['gem_root'],
             LANG='en_US.UTF-8', CI='true', TMPDIR=str(work/'tmp'),
             DEVELOPER_DIR='/Applications/Xcode_16.4.app/Contents/Developer',
             PUB_CACHE=str(work/'pub-cache'), XDG_CONFIG_HOME=str(work/'config'),
             XDG_CACHE_HOME=str(work/'cache'), CLANG_MODULE_CACHE_PATH=str(work/'cache/clang'),
             SWIFTPM_MODULECACHE_OVERRIDE=str(work/'cache/swift-modules'),
             CFFIXED_USER_HOME=str(work/'cf-home'),
             CP_HOME_DIR=str(work/'cocoapods'), CP_CACHE_DIR=str(work/'cocoapods/cache'),
             CP_REPOS_DIR=str(work/'cocoapods/repos'), COCOAPODS_DISABLE_STATS='true',
             COCOAPODS_SKIP_UPDATE_MESSAGE='true', FLUTTER_SUPPRESS_ANALYTICS='true',
             DART_SUPPRESS_ANALYTICS='true', GIT_CONFIG_NOSYSTEM='1',
             GIT_CONFIG_GLOBAL='/dev/null', GIT_TERMINAL_PROMPT='0', GIT_NO_REPLACE_OBJECTS='1',
             PYTHONDONTWRITEBYTECODE='1', xcrun_nocache='1',
             xcrun_db=str(xcrun_adapter.private_cache_path(work)))
    if any((Path(e['HOME'])/n).exists() for n in ['.flutter_settings', '.flutter_tool_state', '.dart-tool']):
        raise ValueError('ambient Flutter/Dart home state refused; HOME remains real')
    return e


def profile(work, stage, xcrun_adapter):
    text = '(version 1)(allow default)(deny network*)(deny file-write*)'
    text += '(allow file-write* (subpath '+json.dumps(str(work))+') (literal "/dev/null"))'
    text += xcrun_adapter.private_cache_profile(work)
    if stage in {'acquire', 'resolve'}:
        text += '(allow network-outbound (remote ip "*:443") (remote ip "*:53") (literal "/private/var/run/mDNSResponder"))'
    protected = ['acquire.sb', 'resolve.sb', 'package.sb', 'activation.json', 'tool-adapters']
    if stage != 'acquire':
        protected += ['original-source', 'raw', 'products', 'origins', 'engine',
                      'evidence/selected-cocoapods.json', 'evidence/acquire-complete.json']
    for p in protected:
        text += '(deny file-write* (subpath '+json.dumps(str(work/p))+'))'
    return text.encode()


def supervisor_help(work):
    spec = metadata()
    activation = json.loads((work/'activation.json').read_bytes())
    if activation.get('identity') != IDENTITY or activation.get('controllers') != controller_snapshot():
        raise ValueError('supervisor-source-binding')
    for name in ['log', 'python']:
        if tool_snapshot(name) != activation['before'][name]:
            raise ValueError('supervisor-tool-binding')
    xcrun_adapter = load(REPO/'ci/apple-resolution-temp-diagnostic/xcrun_adapter.py', 'log_help_original_cache_adapter')
    environment = env(work, spec['originalEnvironmentPaths'], xcrun_adapter)
    result = help_observation(work, environment)
    put(work/'evidence/help-observation.json', result)
    return 0 if result['status'] == 'observed_complete_cli_help' else 1


def execute():
    spec = metadata()
    if platform.system() != 'Darwin' or platform.machine() != 'arm64' or sys.version_info[:3] != (3, 12, 10):
        raise ValueError('fixed-macos15-arm-python')
    parent = Path(os.environ['RUNNER_TEMP']).resolve(strict=True)
    work = parent/WORK_NAME
    if parent.stat().st_uid != os.getuid() or os.path.lexists(work):
        raise ValueError('fresh-owned-root')
    free = os.statvfs(parent)
    if free.f_bavail*free.f_frsize < 1 << 30:
        raise ValueError('disk-admission')
    work.mkdir(mode=0o700)
    for part in ['evidence', 'tmp', 'pub-cache', 'config', 'cache/clang', 'cache/swift-modules',
                 'cocoapods/cache', 'cocoapods/repos', 'cf-home', 'tool-adapters', 'xcode/ToolCache']:
        (work/part).mkdir(mode=0o700, parents=True, exist_ok=True)
    before = {}; code = 1; post = {}; resource = None; failure = 'setup_incomplete'
    try:
        before['controllers'] = controller_snapshot()
        put(work/'evidence/controllers-before.json', before['controllers'])
        for name in ['log', 'python']:
            before[name] = tool_snapshot(name)
            put(work/'evidence'/('tool-'+name+'-before.json'), before[name])
        expected = spec['priorLogBody']
        if any(before['log'][k] != expected[k] for k in ['path', 'bytes', 'sha256']):
            raise ValueError('original-log-body')
        xcrun_adapter = load(REPO/'ci/apple-resolution-temp-diagnostic/xcrun_adapter.py', 'log_help_original_profile_cache')
        xcrun_adapter.seal_cache_boundary(work)
        put(work/'activation.json', {'identity': IDENTITY, 'controllers': before['controllers'], 'before': before})
        # Only this fixed static-help execution moves outside the worker
        # sandbox. No SPM/Swift/App profile/env source is changed or applied.
        put(work/'evidence/execution-boundary.json', dict(EXECUTION_SURFACE,
            command=COMMAND, original_resource_guard=True, clean_environment_unchanged=True,
            seconds=SECONDS, combined_output_cap_bytes=CAP))
        guard = load(REPO/'ci/apple-sdk/resource_dual.py', 'log_help_original_resource_guard')
        cache = load(REPO/'ci/apple-current359/cache_resource.py', 'log_help_original_cache_meter')
        boundary = xcrun_adapter.cache_boundary(work)
        command = [sys.executable, '-B', str(HERE/'run.py'), '--identity', IDENTITY,
            '--supervisor-help', str(work)]
        code = cache.run(guard, xcrun_adapter, work, SimpleNamespace(command=command, cwd=work,
            log=work/'evidence/supervisor-help.log', record=work/'evidence/resource.json', allocated_root=work,
            max_allocated_mib=64, min_free_mib=1024, secondary_root=Path(boundary['canonical']),
            secondary_cap_mib=64, poll_interval=.2, timeout=30))
        resource = json.loads((work/'evidence/resource.json').read_bytes())
        failure = 'none' if code == 0 else 'supervisor_help_or_resource_failed'
    except Exception:
        failure = 'setup_or_boundary_failed'
    finally:
        try:
            post = post_boundaries(work, before)
        finally:
            accepted = code == 0 and all(post.get(k) is True for k in ['log', 'python', 'controllers'])
            put(work/'evidence/summary.json', {'identity': IDENTITY, 'exit_code': 0 if accepted else 1,
                'help_cli_contract_observed': accepted, 'failure': failure, 'post_exact': post,
                'execution_surface': dict(EXECUTION_SURFACE),
                'resource_exit_code': None if resource is None else resource.get('exit_code'),
                'system_log_query_executed': False, 'SPM_Swift_App_or_acquire_executed': False,
                'old20_tools_or_SDK_observed': False, 'raw_help_or_error_text_retained': False,
                'SPM_permission_or_feature_acceptance': False})
    return 0 if accepted else 1


def main():
    os.umask(0o077)
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--identity', required=True, choices=[IDENTITY])
    p.add_argument('--supervisor-help', type=Path, help=argparse.SUPPRESS)
    a = p.parse_args()
    if a.supervisor_help is None:
        return execute()
    expected = Path(os.environ['RUNNER_TEMP']).resolve(strict=True)/WORK_NAME
    if a.supervisor_help != expected or a.supervisor_help.resolve(strict=True) != a.supervisor_help:
        raise ValueError('fixed-supervisor-root')
    return supervisor_help(a.supervisor_help)


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception:
        print(json.dumps({'event': 'log_help_refused', 'reason': 'controller_failure'}), flush=True)
        raise SystemExit(1)
