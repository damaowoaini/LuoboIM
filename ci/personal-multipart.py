#!/usr/bin/env python3
"""Draft manual successor: one original setup, exact legacy8, fresh12/browser1.

Uses existing resource, namespace, phase and machine-name collectors. Never
enters tag903.run, old34/39, native builds, device I/O or a full regression.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import stat
import subprocess
import threading
from urllib.parse import unquote, urlparse

REPO = Path(__file__).resolve().parent.parent
INPUTS = 'ci/personal-multipart/inputs.json'
CANDIDATE_PREFIX = 'ci/candidates/personal-multipart/after'
CHROME_PATH = Path('/opt/google/chrome/chrome')
CHROME_LIMIT = 1 << 30
CHROME_CHUNK = 1 << 20
PARENT_HEAD = '1ef9d4920278da3aa407a2c8f6d4fc37fd12896b'
CODEC = 'app/lib/features/personal/data/personal_migration.dart'
HELPER = 'migration/legacy_personal_export/legacy_personal_export.dart'
INTEGRATION = 'migration/legacy_personal_export/integration.json'
SIBLING_CODEC = 'migration/legacy_personal_export/personal_migration.dart'
BROWSER_NAME = 'part/body source-index manifest and cross-account claims fail closed'
NAMES12 = [
    'legacy export partitions 10001 items and preserves IDs and source order',
    'actual multibyte envelope stays bounded beyond the old 16MiB total',
    'oversized invalid and unknown-field items retain exact raw text without truncation',
    BROWSER_NAME,
    'multipart receipts preserve ordered duplicate IDs and stable aliases across resume and accounts',
    'logout during a part rolls back both rows and receipt then resumes only that part',
    'additive v1 receipts retain old single imports and rollback reader compatibility',
    'late multipart picker cannot import or confirm the replacement account',
    'multipart ownership confirmation expires on account switch',
    'multipart unknown owner needs explicit consent before same-account writes',
    'an old OS picker result cannot import into the replacement account',
    'same-account OS picker confirmation still imports once',
]
TEST_PATHS = [
    'test/features/personal/personal_migration_test.dart',
    'test/features/personal/personal_migration_repository_test.dart',
    'test/features/personal/personal_action_epoch_test.dart',
]
RESOURCE = {'initial_bytes': 4 << 30, 'floor_bytes': 1 << 30,
            'allocated_cap_bytes': 768 << 20, 'poll_seconds': .2,
            'phase_seconds': 120}


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def load(path, label):
    spec = importlib.util.spec_from_file_location(label, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def descriptor(parent):
    # The inherited module is pinned before import; no child command on import.
    raw = (REPO / INPUTS).read_bytes()
    cfg = json.loads(raw)
    if cfg['schema'] != 1 or cfg['parent_head'] != PARENT_HEAD or cfg['resource'] != RESOURCE:
        raise ValueError('exact parent/resource contract required')
    if cfg['selected_names12'] != NAMES12 or cfg['test_paths'] != TEST_PATHS:
        raise ValueError('closed new10/picker2 selection required')
    if cfg['browser_name'] != BROWSER_NAME or cfg['old_tests_redispatched'] != 0:
        raise ValueError('portable one-name reuse and no old redispatch required')
    parent = parent.resolve(strict=True)
    if parent == REPO or parent.is_relative_to(REPO) or REPO.is_relative_to(parent):
        raise ValueError('independent exact parent checkout required')
    first = next(row for row in cfg['parent_pins'] if row['path'] == 'ci/mobile-tag903.py')
    body = (parent / first['path']).read_bytes()
    if len(body) != first['bytes'] or sha(body) != first['sha256']:
        raise ValueError('inherited controller differs before import')
    tag = load(parent / first['path'], 'personal_inherited_tag903')
    for row in cfg['parent_pins']:
        tag.pinned(parent / tag.safe(row['path']), row)
        tag.pinned(REPO / tag.safe(row['path']), row)
    for row in cfg['controller_pins']:
        tag.pinned(REPO / tag.safe(row['path']), row)
    c, old, original, base = tag.descriptor()
    if c['source'] != cfg['source_before'] or c['portable'] != cfg['portable_before']:
        raise ValueError('original current363/415 differs')
    # HEAD is still canonical363/415. Candidate sources live only under the
    # fixed prefix and can never stand in for adopted App/migration bodies.
    before = tag.checkout_sources(c, original, base, repo=REPO)
    expected_inputs = [{'originalPath': row['path'], 'path': CANDIDATE_PREFIX + '/' + row['path'],
                        'bytes': row['after']['bytes'], 'sha256': row['after']['sha256']}
                       for row in cfg['delta8']]
    if cfg.get('candidate_input_prefix') != CANDIDATE_PREFIX or cfg.get('candidateInputPaths') != expected_inputs:
        raise ValueError('fixed candidate-prefix legacy8 mapping required')
    files = dict(before)
    for delta in cfg['delta8']:
        name = delta['path']
        if delta['before'] is None:
            if name in before or (parent / name).exists() or (REPO / name).exists():
                raise ValueError('new migration source already exists in canonical parent/HEAD')
        elif tag.pinned(REPO / tag.safe(name), delta['before']) != before.get(name, tag.regular(REPO / name)):
            raise ValueError('migration preimage differs')
        files[name] = tag.pinned(REPO / CANDIDATE_PREFIX / tag.safe(name), delta['after'])
    source = {n: b for n, b in files.items() if n.startswith('app/')}
    portable = {n: b for n, b in files.items() if n != INTEGRATION}
    for values, expected in [(source, cfg['source_after']), (portable, cfg['portable_after'])]:
        if len(values) != expected['members'] or tag.digest(values) != expected['digest']:
            raise ValueError('explicit successor inventory differs')
    expected_authored = {n for n in before if n.startswith(('app/lib/', 'app/test/', 'app/tool/'))}
    if tag.authored_paths(REPO) != expected_authored:
        raise ValueError('canonical HEAD baseline authored inventory differs')
    if len(cfg['delta8']) != 8 or len({r['path'] for r in cfg['delta8']}) != 8:
        raise ValueError('exact legacy8 required')
    app_paths = [r['path'][4:] for r in cfg['delta8'] if r['path'].startswith('app/')]
    if len(app_paths) != 6 or cfg['analysis_paths6'] != app_paths or cfg['format_paths7'] != app_paths + ['../' + HELPER]:
        raise ValueError('exact affected six App Dart and one helper paths required')
    files[SIBLING_CODEC] = files[CODEC]
    if not cfg['ready_to_dispatch']:
        raise ValueError('frozen draft: source/pure review required before dispatch')
    return cfg, raw, tag, c, old, original, base, files


def config_body(work):
    """Same package map, absolute local roots; no dependency resolution or lock edits."""
    config = work / 'source/app/.dart_tool/package_config.json'
    value = json.loads(config.read_bytes())
    allowed = [work / n for n in ('flutter', 'pub-cache', 'source/app',
                                  'source/packages/flutter_openim_sdk')]
    for row in value['packages']:
        uri = urlparse(row['rootUri'])
        if uri.scheme not in ('', 'file') or uri.netloc:
            raise ValueError('nonlocal package mapping')
        target = Path(unquote(uri.path))
        target = (target if target.is_absolute() else config.parent / target).resolve()
        if not any(target == root or target.is_relative_to(root) for root in allowed):
            raise ValueError('package outside original fresh allocation')
        if row['name'] == 'imapp' and target != work / 'source/app':
            raise ValueError('original imapp package required')
        if row['name'] == 'flutter_openim_sdk' and target != work / 'source/packages/flutter_openim_sdk':
            raise ValueError('original portable SDK required')
        row['rootUri'] = target.as_uri() + '/'
    return (json.dumps(value, sort_keys=True, indent=2) + '\n').encode()


def write_body(path, body):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    with path.open('xb') as stream:
        stream.write(body)
        stream.flush()
        os.fsync(stream.fileno())


def candidate_head():
    return subprocess.check_output(['/usr/bin/git', '-C', str(REPO), 'rev-parse',
                                    '--verify', 'HEAD^{commit}'], text=True, timeout=10).strip()


def chrome_pin(path=CHROME_PATH, *, limit=CHROME_LIMIT):
    """Pin one stable installed Linux64 executable without reading it into RAM.

    Production callers always use the fixed default path. A smaller limit and
    real small ELF fixture can exercise bounds locally without a large file.
    """
    path = Path(path)
    if type(limit) is not int or not 64 <= limit <= CHROME_LIMIT:
        raise ValueError('Chrome pin limit must be64..1GiB')
    if not path.is_absolute() or path.resolve(strict=True) != path:
        raise ValueError('Chrome path is not canonical without symlink ancestors')

    def identity(info):
        return {'dev': info.st_dev, 'ino': info.st_ino, 'mode': info.st_mode,
                'uid': info.st_uid, 'gid': info.st_gid, 'nlink': info.st_nlink,
                'size': info.st_size, 'mtime_ns': info.st_mtime_ns, 'ctime_ns': info.st_ctime_ns}

    def validate(info):
        if not stat.S_ISREG(info.st_mode):
            raise ValueError('Chrome type is not regular: mode=' + str(info.st_mode))
        if info.st_uid not in (0, os.getuid()) or info.st_mode & 0o022:
            raise ValueError('Chrome requires root/runner owner and no group/other write: uid=' +
                             str(info.st_uid) + ' mode=' + oct(stat.S_IMODE(info.st_mode)))
        if not info.st_mode & 0o111:
            raise ValueError('Chrome has no executable permission: mode=' + oct(stat.S_IMODE(info.st_mode)))
        if not 64 <= info.st_size <= limit:
            raise ValueError('Chrome size=' + str(info.st_size) + ' outside64..' + str(limit))

    path_before = path.lstat()
    validate(path_before)
    if not os.access(path, os.X_OK):
        raise ValueError('Chrome is not executable by the original runner')
    flags = os.O_RDONLY | os.O_CLOEXEC | os.O_NOFOLLOW | os.O_NONBLOCK
    descriptor = os.open(path, flags)
    try:
        before = os.fstat(descriptor)
        validate(before)
        if identity(before) != identity(path_before):
            raise ValueError('Chrome path identity changed while opening')
        digest = hashlib.sha256()
        header = bytearray()
        remaining = before.st_size
        while remaining:
            chunk = os.read(descriptor, min(CHROME_CHUNK, remaining))
            if not chunk:
                raise ValueError('Chrome ended before its pinned size')
            digest.update(chunk)
            header.extend(chunk[:max(0, 64 - len(header))])
            remaining -= len(chunk)
        after = os.fstat(descriptor)
        path_after = path.lstat()
        if (identity(before) != identity(after) or identity(before) != identity(path_after)
                or path.resolve(strict=True) != path or not os.access(path, os.X_OK)):
            raise ValueError('Chrome descriptor/path identity, size or times changed')
        machine = int.from_bytes(header[18:20], 'little')
        elf_type = int.from_bytes(header[16:18], 'little')
        if (header[:4] != b'\x7fELF' or header[4:7] != b'\x02\x01\x01'
                or header[7] not in (0, 3) or machine != 62 or elf_type not in (2, 3)
                or int.from_bytes(header[20:24], 'little') != 1
                or int.from_bytes(header[52:54], 'little') != 64):
            raise ValueError('Chrome requires Linux64 little-endian x86_64 ELF executable: class=' +
                             str(header[4]) + ' data=' + str(header[5]) + ' osabi=' +
                             str(header[7]) + ' machine=' + str(machine) + ' type=' + str(elf_type))
        return {'path': str(path), 'bytes': before.st_size, 'sha256': digest.hexdigest(),
                'identity': identity(before), 'elf64_linux_x86_64': True}
    finally:
        os.close(descriptor)


def verify_work(work, cfg, tag, files, receipt):
    tag.canonical_work(work)
    helper_config = 'source/migration/legacy_personal_export/.dart_tool/package_config.json'
    if (receipt.get('source') != cfg['source_after'] or receipt.get('portable') != cfg['portable_after']
            or receipt.get('delta8') != cfg['delta8'] or receipt.get('setup_calls') != 1
            or receipt.get('new_pub_get_calls') != 0 or receipt.get('helper_config') != helper_config
            or set(receipt.get('package_configs', {})) != {'source/app/.dart_tool/package_config.json', helper_config}):
        raise ValueError('explicit setup successor receipt differs')
    for name, body in files.items():
        if tag.regular(work / 'source' / tag.safe(name)) != body:
            raise ValueError('owned successor source differs: ' + name)
    expected = {n for n in files if n.startswith(('app/lib/', 'app/test/', 'app/tool/'))}
    if tag.authored_paths(work / 'source') != expected:
        raise ValueError('owned authored inventory differs')
    for name, expected_hash in receipt['package_configs'].items():
        if sha(tag.regular(work / name)) != expected_hash:
            raise ValueError('original/derived package map changed')
    if tag.regular(work / receipt['helper_config']) != config_body(work):
        raise ValueError('helper map is not an exact local derivation')
    for key, want in cfg['toolchain'].items():
        version = json.loads(tag.regular(work / 'evidence/flutter-version.json'))
        if version.get(key) != want:
            raise ValueError('official SDK differs: ' + key)


def setup(parent, allocation):
    cfg, raw, tag, c, old, original, base, files = descriptor(parent)
    if subprocess.check_output(['/usr/bin/git', '-C', str(parent), 'rev-parse', 'HEAD'], text=True, timeout=10).strip() != PARENT_HEAD:
        raise ValueError('fixed original parent HEAD required')
    head = candidate_head()
    if not re.fullmatch('[a-f0-9]{40}', os.environ.get('GITHUB_SHA', '')) or head != os.environ['GITHUB_SHA']:
        raise ValueError('candidate checkout must be the workflow event commit')
    # Do not create the original allocation on a failed Chrome admission.
    try:
        chrome = chrome_pin()
    except (OSError, ValueError, RuntimeError) as error:
        print(json.dumps({'event': 'personal_chrome_preflight', 'accepted': False,
                          'exceptionType': type(error).__name__, 'reason': str(error)[:512]},
                         sort_keys=True), flush=True)
        raise
    print(json.dumps({'event': 'personal_chrome_preflight', 'accepted': True,
                      'path': chrome['path'], 'bytes': chrome['bytes'],
                      'sha256': chrome['sha256'], 'elf64_linux_x86_64': True},
                     sort_keys=True), flush=True)
    # Exactly one inherited setup. It fetches verified Flutter, enforces the old
    # app lock and seeds original hash-bound SQLite; it never enters old tests.
    tag.setup(allocation)
    work = allocation / 'work'
    original_receipts = tag.origin_snapshot(work, c, original, base)
    for delta in cfg['delta8']:
        path = work / 'source' / tag.safe(delta['path'])
        if delta['before'] is not None and delta['path'] != INTEGRATION:
            tag.pinned(path, delta['before'])
            path.unlink()
        write_body(path, files[delta['path']])
    write_body(work / 'source' / SIBLING_CODEC, files[CODEC])
    helper_config = 'source/migration/legacy_personal_export/.dart_tool/package_config.json'
    write_body(work / helper_config, config_body(work))
    env = dict(os.environ, PUB_CACHE=str(work / 'pub-cache'), TMPDIR=str(work / 'tmp'),
               XDG_CONFIG_HOME=str(work / 'config'), XDG_CACHE_HOME=str(work / 'config/cache'),
               FLUTTER_SUPPRESS_ANALYTICS='true', DART_SUPPRESS_ANALYTICS='true', CI='true')
    # Only extra network-enabled SDK operation: the same exact SDK's web assets.
    with (work / 'evidence/personal-web-precache.log').open('xb') as log:
        subprocess.run([str(work / 'flutter/bin/flutter'), 'precache', '--web'], env=env,
                       stdout=log, stderr=subprocess.STDOUT, timeout=600, check=True)
    receipt = {'source': cfg['source_after'], 'portable': cfg['portable_after'],
               'candidate_head': head,
               'delta8': cfg['delta8'], 'derived_codec': {'path': SIBLING_CODEC,
               'source': CODEC, 'sha256': sha(files[CODEC])}, 'original_receipts': original_receipts,
               'helper_config': helper_config, 'inputs_sha256': sha(raw),
               'package_configs': {name: sha(tag.regular(work / name)) for name in
                   ['source/app/.dart_tool/package_config.json', helper_config]},
               'chrome': chrome,
               'setup_calls': 1, 'new_pub_get_calls': 0, 'old_generated_outputs_copied': 0,
               'old_device_io_verified': False, 'native_acceptance': False}
    verify_work(work, cfg, tag, files, receipt)
    tag.write(work / 'evidence/personal-source.json', receipt)


def phase_commands(work, cfg):
    dart = str(work / 'flutter/bin/cache/dart-sdk/bin/dart')
    flutter = str(work / 'flutter/bin/flutter')
    fmt = [dart, 'format', '--output=write', *cfg['format_paths7']]
    analyses = [[dart, 'analyze', '--fatal-infos', p] for p in cfg['analysis_paths6']]
    helper = [dart, 'analyze', '--fatal-infos', 'legacy_personal_export.dart']
    common = [flutter, 'test', '--no-pub', '--machine', '--concurrency=1', '--no-enable-vmservice']
    selected = common + ['--name', '^(?:' + '|'.join(map(re.escape, NAMES12)) + ')$', *TEST_PATHS]
    browser = common + ['--platform=chrome', '--name', '^' + re.escape(BROWSER_NAME) + '$', TEST_PATHS[0]]
    return fmt, analyses, helper, selected, browser


def format_owned(work, cfg, tag, files, receipt, check, guard, command):
    """One official formatter invocation, then bind the exact derived source.

    HEAD, input manifest, lockfiles and original setup stay immutable. Only the
    seven declared Dart sources may change; the colocated codec is a derivation.
    Save bounded Dart texts even when the formatter child fails part way.
    """
    names = [r['path'] for r in cfg['delta8'] if r['path'].endswith('.dart')]
    if len(names) != 7:
        raise ValueError('exact seven formatter targets required')
    primary = None
    try:
        check.guarded(command, work, guard, 'personal-format7', timeout=120)
    except BaseException as error:
        primary = error
    derived = dict(files)
    try:
        for name in names:
            derived[name] = tag.regular(work / 'source' / tag.safe(name))
        # Prove all undeclared source bytes unchanged before updating the only
        # explicit duplicate. It was not an eighth formatter target.
        verify_work(work, cfg, tag, derived, receipt)
        derived[SIBLING_CODEC] = derived[CODEC]
        sibling = work / 'source' / SIBLING_CODEC
        with sibling.open('wb') as stream:
            stream.write(derived[CODEC])
            stream.flush()
            os.fsync(stream.fileno())
        if sum(len(derived[name]) for name in names) > 1 << 20:
            raise ValueError('formatted-source artifact exceeds1MiB')
        changes = []
        for name in names:
            before = {'path': name, 'bytes': len(files[name]), 'sha256': sha(files[name])}
            after = {'path': name, 'bytes': len(derived[name]), 'sha256': sha(derived[name])}
            changes.append({'path': name, 'before': before, 'after': after})
            write_body(work / 'evidence/formatted-source' / tag.safe(name), derived[name])
        source = {n: b for n, b in derived.items() if n.startswith('app/')}
        portable = {n: b for n, b in derived.items() if n not in (INTEGRATION, SIBLING_CODEC)}
        derivation = {'input_source': cfg['source_after'], 'input_portable': cfg['portable_after'],
                      'formatter_derived_source': {'members': len(source), 'digest': tag.digest(source)},
                      'formatter_derived_portable': {'members': len(portable), 'digest': tag.digest(portable)},
                      'source_delta7': changes, 'integration_json_unchanged_sha256': sha(files[INTEGRATION]),
                      'derived_sibling_codec_sha256': sha(derived[CODEC]),
                      'inputs_sha256': receipt['inputs_sha256'], 'legacy_manifest': cfg['legacy_manifest'],
                      'controller_pins': cfg['controller_pins'], 'toolchain': cfg['toolchain'],
                      'candidate_head': receipt['candidate_head'],
                      'setup_receipt_sha256': sha(tag.regular(work / 'evidence/personal-source.json')),
                      'formatter_tools_before_sha256': sha(tag.regular(work / 'evidence/personal-tools-before.json')),
                      'setup_parent_head': PARENT_HEAD, 'format_command': command,
                      'format_resource': json.loads(tag.regular(work / 'evidence/personal-format7.resource.json')),
                      'formatter_child_succeeded': primary is None,
                      'input_HEAD_direct_PASS': False, 'tests_executed_before_analysis': False}
        tag.write(work / 'evidence/personal-formatter-derived.json', derivation)
        verify_work(work, cfg, tag, derived, receipt)
    except BaseException as error:
        if primary is None:
            primary = error
    if primary is not None:
        raise primary
    return derived, derivation


def run(parent, work):
    cfg, raw, tag, c, old, original, base, files = descriptor(parent)
    input_files = dict(files)
    check = old.CHECK
    receipt_raw = tag.regular(work / 'evidence/personal-source.json')
    receipt = json.loads(receipt_raw)
    if receipt['inputs_sha256'] != sha(raw) or candidate_head() != receipt['candidate_head']:
        raise ValueError('setup inputs changed')
    verify_work(work, cfg, tag, files, receipt)
    check.prove_namespace()
    guard = check.Guard(work)
    if guard.sample() < check.INITIAL or guard.reason:
        raise ValueError('original initial4GiB/floor1GiB/cap768MiB required')
    thread = threading.Thread(target=guard.poll, daemon=True)
    thread.start()
    primary = None
    tools = None
    summaries = {}
    derivation = None
    derivation_raw = None
    post = {'accepted': False}
    try:
        tools = tag.tool_snapshot(work)
        chrome = receipt['chrome']
        if chrome_pin() != chrome:
            raise ValueError('setup Chrome executable changed')
        os.environ['CHROME_EXECUTABLE'] = chrome['path']
        tag.write(work / 'evidence/personal-tools-before.json', tools)
        # JSON is the eighth changed file: exact pin plus parse/integration-map
        # checks, not a Dart formatter/analyzer invocation on JSON text.
        integration = json.loads(files[INTEGRATION])
        if integration['target_application_id'] != 'com.luoboduihua.imapp' or integration['format']['owner'] is not None:
            raise ValueError('integration identity/ownership differs')
        tag.write(work / 'evidence/personal-integration-json.json', {'parsed': True,
                  'sha256': sha(files[INTEGRATION]), 'old_device_io_verified': False})
        fmt, analyses, helper, selected, browser = phase_commands(work, cfg)
        files, derivation = format_owned(work, cfg, tag, files, receipt, check, guard, fmt)
        derivation_raw = tag.regular(work / 'evidence/personal-formatter-derived.json')
        analysis_failures = []
        for index, command in enumerate(analyses + [helper], 1):
            verify_work(work, cfg, tag, files, receipt)
            cwd = work / 'source/migration/legacy_personal_export' if command == helper else None
            label = 'personal-analyze-' + str(index)
            log = check.guarded(command, work, guard, label, timeout=120, cwd=cwd,
                                accepted_exits=(0, 1, 2, 3))
            record = json.loads(tag.regular(work / 'evidence' / (label + '.resource.json')))
            result = old.ANALYSIS.analysis_outcome(record, log.read_text(), label, command)
            tag.write(work / 'evidence' / (label + '.summary.json'), result)
            if not result['passed']:
                analysis_failures.append(result)
        tag.write(work / 'evidence/personal-analysis-batch.json', {
            'analysed_paths': len(analyses) + 1,
            'all_passed': not analysis_failures,
            'failed_paths': [row['path'] for row in analysis_failures]})
        if analysis_failures:
            raise ValueError('affected-path analyses failed: ' +
                             ', '.join(row['phase'] for row in analysis_failures))
        for label, command, names in [('personal-target12', selected, NAMES12),
                                       ('personal-browser1', browser, [BROWSER_NAME])]:
            verify_work(work, cfg, tag, files, receipt)
            log = check.guarded(command, work, guard, label, timeout=120)
            summaries[label] = check.required_name_summary(log.read_text(), len(names),
                                  [{'name': name, 'count': 1} for name in names])
            tag.write(work / 'evidence' / (label + '.summary.json'), summaries[label])
    except BaseException as error:
        primary = error
    finally:
        def expired(signum, frame):
            raise TimeoutError('bounded personal source/tools post')
        previous = signal.signal(signal.SIGALRM, expired)
        signal.setitimer(signal.ITIMER_REAL, 30)
        try:
            verify_work(work, cfg, tag, files, receipt)
            current = descriptor(parent)
            if current[0] != cfg or current[1] != raw or current[-1] != input_files:
                raise ValueError('original input/source/controller changed')
            if candidate_head() != receipt['candidate_head']:
                raise ValueError('candidate workflow event HEAD changed')
            if tag.regular(work / 'evidence/personal-source.json') != receipt_raw:
                raise ValueError('projection receipt changed')
            if tag.origin_snapshot(work, c, original, base) != receipt['original_receipts']:
                raise ValueError('original setup receipts changed')
            if derivation is None or tag.regular(work / 'evidence/personal-formatter-derived.json') != derivation_raw:
                raise ValueError('formatter derivation incomplete or changed')
            for row in derivation['source_delta7']:
                if tag.pinned(work / 'evidence/formatted-source' / tag.safe(row['path']), row['after']) != files[row['path']]:
                    raise ValueError('formatted source artifact differs')
            after = tag.tool_snapshot(work)
            tag.write(work / 'evidence/personal-tools-after.json', after)
            chrome_after = chrome_pin()
            if tools is None or after != tools or chrome_after != receipt['chrome']:
                raise ValueError('original Flutter/Dart/Chrome tools changed')
            post = {'accepted': True, 'input_source': cfg['source_after'],
                    'input_portable': cfg['portable_after'],
                    'formatter_derived_source': derivation['formatter_derived_source'],
                    'formatter_derived_portable': derivation['formatter_derived_portable'],
                    'input_HEAD_direct_PASS': False}
        except BaseException as error:
            post['exceptionType'] = type(error).__name__
            if primary is None:
                primary = error
        finally:
            signal.setitimer(signal.ITIMER_REAL, 0)
            signal.signal(signal.SIGALRM, previous)
            try:
                tag.write(work / 'evidence/personal-source-post.json', post)
                guard.sample()
                tag.write(work / 'evidence/personal-final-resource.json', {
                    'minimum_free_bytes': guard.minimum, 'peak_source_allocated_bytes': guard.peak,
                    'latched_failure': guard.reason})
                guard.sample()
            except BaseException as error:
                if primary is None:
                    primary = error
            finally:
                guard.stop.set()
                thread.join()
    if primary is not None or guard.reason or not post['accepted'] or len(summaries) != 2:
        tag.write(work / 'evidence/personal-failed.json', {'accepted': False,
                  'exceptionType': type(primary).__name__ if primary else 'ValueError',
                  'latched_failure': guard.reason, 'completed_summaries': list(summaries),
                  'full_regression': False, 'native_acceptance': False})
        if primary is not None:
            raise primary
        raise ValueError('personal targeted scope/post/resource incomplete')
    tag.write(work / 'evidence/personal-passed.json', {'accepted': True,
              'input_source': cfg['source_after'], 'input_portable': cfg['portable_after'],
              'formatter_derived_source': derivation['formatter_derived_source'],
              'formatter_derived_portable': derivation['formatter_derived_portable'],
              'formatter_derivation_sha256': sha(derivation_raw), 'input_HEAD_direct_PASS': False,
              'setup_receipt_sha256': sha(receipt_raw),
              'tools_before_sha256': sha(tag.regular(work / 'evidence/personal-tools-before.json')),
              'tools_after_sha256': sha(tag.regular(work / 'evidence/personal-tools-after.json')),
              'new_tests': 10, 'affected_existing_picker_tests': 2,
              'browser_reused_codec_tests': 1, 'old34_or39_redispatched': 0,
              'format_dart_paths': 7, 'analysis_dart_paths': 7, 'integration_json': 1,
              'old_sdk_compile_or_device_io_verified': False, 'full_regression': False,
              'native_acceptance': False, 'whole_goal_complete': False})


def netns(parent, work, run_as):
    if os.geteuid() != 0 or not run_as or run_as == 'root':
        raise ValueError('root netns launcher and original nonroot tester required')
    cfg, raw, tag, c, old, original, base, files = descriptor(parent)
    tag.canonical_work(work)
    home = old.CHECK.run_user_home(run_as)
    env = {'HOME': home, 'USER': run_as, 'LOGNAME': run_as,
           'PATH': '/usr/local/bin:/usr/bin:/bin', 'LANG': 'C.UTF-8', 'LC_ALL': 'C.UTF-8',
           'PUB_CACHE': str(work / 'pub-cache'), 'TMPDIR': str(work / 'tmp'),
           'TMP': str(work / 'tmp'), 'TEMP': str(work / 'tmp'),
           'XDG_CONFIG_HOME': str(work / 'config'), 'XDG_CACHE_HOME': str(work / 'config/cache'),
           'FLUTTER_ROOT': str(work / 'flutter'), 'FLUTTER_SUPPRESS_ANALYTICS': 'true',
           'DART_SUPPRESS_ANALYTICS': 'true', 'CI': 'true', 'TERM': 'dumb'}
    subprocess.run(['ip', 'link', 'set', 'lo', 'up'], check=True)
    old.CHECK.prove_namespace()
    argv = ['/usr/sbin/runuser', '-u', run_as, '--', 'env', '-i']
    argv += [key + '=' + value for key, value in env.items()]
    argv += ['python3', '-B', str(Path(__file__).resolve()), 'run', '--parent', str(parent), '--work', str(work)]
    os.execv(argv[0], argv)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('mode', choices=['setup', 'netns', 'run'])
    parser.add_argument('--parent', type=Path, required=True)
    parser.add_argument('--allocation', type=Path)
    parser.add_argument('--work', type=Path)
    parser.add_argument('--run-as')
    args = parser.parse_args()
    os.umask(0o077)
    if args.mode == 'setup':
        if args.allocation is None:
            parser.error('--allocation required')
        setup(args.parent, args.allocation)
    elif args.mode == 'netns':
        if args.work is None or args.run_as is None:
            parser.error('--work and --run-as required')
        netns(args.parent, args.work, args.run_as)
    else:
        if args.work is None:
            parser.error('--work required')
        run(args.parent, args.work)


if __name__ == '__main__':
    main()
