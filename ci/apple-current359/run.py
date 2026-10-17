#!/usr/bin/env python3
"""Manual current359 native-resolution producer; unsigned App build is source-held."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import signal
import shutil
import stat
import subprocess
import sys
import traceback
from types import SimpleNamespace
import urllib.parse
import urllib.request
import ssl
import zipfile

import consume

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
IDENTITY = 'current359-null2-apple-native-resolution-v2'
GIB = 1024**3


def load(path, name):
    s = importlib.util.spec_from_file_location(name, path)
    m = importlib.util.module_from_spec(s); s.loader.exec_module(m)
    return m


def borrowed_helper(row, name):
    consume.read_file(REPO, row)
    return load(REPO/row['path'], name)


xcrun_adapter = borrowed_helper({'path': 'ci/apple-current359/xcrun_adapter.py', 'bytes': 12726, 'sha256': '3097983491f995a4c93af02150a0414c7bb01e169b23fa2fa7bde5d51cd14fa5'}, 'current359_owned_xcrun_adapter')

packaging = borrowed_helper({'path': 'ci/apple-current354/packaging.py', 'bytes': 10141, 'sha256': '7e2f2e05ba18c7754c1dcffe9361b185ee41ea878069b35446cf8e111c5c1248'}, 'apple359_common_packaging')
pod_selector = borrowed_helper({'path': 'ci/apple-current354/pod_selector.py', 'bytes': 5603, 'sha256': '820076795d8e383677c194985f3a64097aa0f2bb1e3a68d77a33fef5be614769'}, 'apple359_reviewed117_selector')


gem_audit = borrowed_helper({'path': 'ci/apple-current359/gem_audit.py', 'bytes': 41364, 'sha256': '3b1745ac8d2b7bd4876b1e4c0304af846b5089aa11f52044de2a1c058dfde101'}, 'apple359_fixed_gem_audit')

custody = load(REPO/'ci/apple-sdk-link/custody.py', 'current359_custody')
put, record, mkdir, sha = (getattr(custody, n) for n in ['put', 'record', 'mkdir', 'sha'])


def metadata():
    registry = json.loads((HERE/'registry.json').read_bytes())
    if registry['identity'] != IDENTITY:
        raise ValueError('current359 source registry identity')
    for row in registry['files']:
        consume.read_file(REPO, row)
    lane = json.loads((HERE/'lane.json').read_bytes())
    if (lane['identity'] != IDENTITY or lane['source_ref'] != consume.REF
            or lane['source_members'] != 477 or lane['implementation_count'] != 20
            or lane['packages'] != 178):
        raise ValueError('independent current359/477/178/20 identity required')
    for row in lane['own_inputs'] + lane['borrowed']:
        consume.read_file(REPO, row)
    spec = consume.checked_identity(json.loads((HERE/'inputs.json').read_bytes()))
    plugins = json.loads((REPO/'ci/apple-current354/plugins.json').read_bytes())
    if (len(spec['files']) != 477 or plugins['implementation_count'] != 20
            or lane['business359'] != spec['source_digest']
            or lane['portable411'] != spec['portable_digest']):
        raise ValueError('sealed original input identity differs')
    return lane, spec, plugins


def env(work, selected):
    work = Path(work)
    pod = pod_selector.recheck(selected, Path(os.environ['HOME']))
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
             PYTHONDONTWRITEBYTECODE='1', xcrun_nocache='1')
    if any((Path(e['HOME'])/n).exists() for n in ['.flutter_settings', '.flutter_tool_state', '.dart-tool']):
        raise ValueError('ambient Flutter/Dart home state refused; HOME remains real')
    return e


def command(work, label, argv, cwd, environment):
    if not label.replace('-', '').replace('_', '').isalnum():
        raise ValueError('fixed command label required')
    record(work/'evidence'/f'{label}.command.json', {'argv': list(map(str, argv)),
           'cwd': str(cwd), 'environment_values_recorded': False})
    with (work/'evidence'/f'{label}.stdout.log').open('xb') as out, (
            work/'evidence'/f'{label}.stderr.log').open('xb') as err:
        result = subprocess.run(list(map(str, argv)), cwd=cwd, env=environment,
                                stdin=subprocess.DEVNULL, stdout=out, stderr=err)
        for stream in [out, err]: stream.flush(); os.fsync(stream.fileno())
    record(work/'evidence'/f'{label}.result.json', {'exit_code': result.returncode})
    body = packaging.regular(work/'evidence'/f'{label}.stdout.log', 32*1024**2)
    if result.returncode != 0:
        raise ValueError('fixed command failed: '+label)
    return body


def fetch_fixed(row, target):
    """One declared HTTPS object, no credentials/proxy; only exact approved redirects."""
    initial = row['url']
    class Redirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, request, fp, code, msg, headers, url):
            before, after = urllib.parse.urlsplit(request.full_url), urllib.parse.urlsplit(url)
            if (before.hostname != 'github.com' or after.scheme != 'https'
                    or after.hostname != 'release-assets.githubusercontent.com'
                    or after.username or after.password or after.port not in {None, 443}):
                raise ValueError('finite artifact redirect refused')
            return urllib.request.Request(url)  # anonymous fresh request
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), Redirect(),
        urllib.request.HTTPSHandler(context=ssl.create_default_context()))
    url = urllib.parse.urlsplit(initial)
    if (url.scheme != 'https' or url.hostname != 'github.com' or url.username or url.password
            or url.query or url.fragment or url.port not in {None, 443}):
        raise ValueError('fixed public release origin required')
    h, count = hashlib.sha256(), 0
    with opener.open(initial, timeout=90) as response:
        if response.status != 200:
            raise ValueError('fixed source response status')
        fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'wb') as out:
            while chunk := response.read(1024**2):
                count += len(chunk)
                if count > row['cap_bytes']: raise ValueError('fixed source download cap')
                out.write(chunk); h.update(chunk)
            out.flush(); os.fsync(out.fileno())
    custody.sync(target.parent)
    if count == 0 or h.hexdigest() != row['sha256']:
        raise ValueError('declared package source checksum differs')
    return {'url': initial, 'bytes': count, 'sha256': h.hexdigest()}


def source_stage(work, spec, e):
    # Only exact text/assets from the immutable existing Git ref; no source native binary.
    repo = work/'source-git'; mkdir(repo)
    command(work, 'source-init', ['/usr/bin/git', 'init', '--quiet', repo], work, e)
    command(work, 'source-fetch', ['/usr/bin/git', '-C', repo, 'fetch', '--depth=1',
            'https://github.com/damaowoaini/LuoboIM.git', spec['source_ref']], work, e)
    value = command(work, 'source-object', ['/usr/bin/git', '-C', repo, 'rev-parse', 'FETCH_HEAD'], work, e)
    if value != (spec['source_ref']+'\n').encode(): raise ValueError('exact source object required')
    warnings = 0
    with (work/'evidence/source-show.stderr.log').open('xb') as log:
        for row in spec['files']:
            with subprocess.Popen(['/usr/bin/git', '-C', str(repo), 'show',
                    spec['source_ref']+':'+row['path']], stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE, env=e) as process:
                body, error = process.communicate(timeout=30)
            log.write(error); warnings += bool(error)
            if log.tell() > 1024**2: raise ValueError('source Git stderr evidence cap')
            if process.returncode != 0 or len(body) != row['bytes'] or consume.digest(body) != row['sha256']:
                raise ValueError('477 source body differs')
            put(work/'original-source'/row['path'], body)
            put(work/'source'/row['path'], body)
        log.flush(); os.fsync(log.fileno())
    record(work/'evidence/source-show.result.json', {'exit_code': 0, 'exact_members': 477,
                                                  'stderr_members_retained': warnings})
    record(work/'evidence/source477.json', consume.verify_source(work/'original-source', spec))


def reuse_core(work, lane, spec):
    original = json.loads((REPO/'ci/apple-sdk-link-real/inputs.json').read_bytes())
    if original['parent'] != spec['R7_custody']:
        raise ValueError('original R7 artifact custody differs')
    token = os.environ.pop('GITHUB_TOKEN', '')
    if not token: raise ValueError('read-only artifact token required during custody only')
    remote = custody.check_remote(original, token)
    for row in original['parent']['artifacts']:
        archive = work/(row['kind']+'.zip')
        custody.download(row, token, archive)
        custody.extract(archive, row, work/row['kind'],
                        original['product_members'] if row['kind'] == 'products' else None)
    del token
    raw = custody.audit_raw(work/'raw', original)
    real = load(REPO/'ci/apple-sdk-link-real/run.py', 'app359_original_engine_extract')
    real.extract_engine(work, original)
    core = work/'products/.qa/apple-cloud-1/OpenIMCore.xcframework'
    target = work/'source/packages/flutter_openim_sdk/ios/Framework/OpenIMCore.xcframework'
    proof = packaging.stage_core(core, target, spec['core17'], put)
    consume.audit_engine(work/'engine/Flutter.xcframework', spec)
    record(work/'evidence/core-custody.json', {'parent': remote, 'raw': raw, 'adapter': proof,
           'R10_root_acceptance': lane['R10_root_acceptance'], 'new_Go_or_bind_executions': 0})


def flutter_bootstrap_architecture(work, environment):
    # Flutter's fixed shell bootstrap invokes this unqualified name. Resolve
    # the real OS utility, never synthesize a machine result or copy a tool.
    tool = '/usr/sbin/sysctl'
    if shutil.which('sysctl', path=environment['PATH']) != tool:
        raise ValueError('fixed system sysctl resolution required')
    host = load(REPO/'ci/apple-host-tool/run.py', 'app359_system_sysctl')
    before = host.fingerprint(Path(tool), 1024**2, True)
    mode, uid = before['identity'][2], before['identity'][3]
    if uid != 0 or not mode & 0o111 or mode & 0o022:
        raise ValueError('nonwritable root-owned system sysctl required')
    command(work, 'sysctl-signature', host.apple_signature_argv(tool), work, environment)
    value = command(work, 'sysctl-arm64', [tool, '-n', 'hw.optional.arm64'], work, environment)
    if (value != b'1\n' or packaging.regular(work/'evidence/sysctl-arm64.stderr.log', 64*1024)):
        raise ValueError('exact real ARM64 sysctl result required')
    after = host.fingerprint(Path(tool), 1024**2, True)
    if after != before:
        raise ValueError('system sysctl changed during bootstrap query')
    record(work/'evidence/system-sysctl.json', {'before': before, 'after': after,
           'query': [tool, '-n', 'hw.optional.arm64'], 'Apple_signature_verified': True,
           'origin': 'selected macOS installed Apple utility; first cloud body observation',
           'not_a_cross_runner_release_pin': True, 'kernel_state_written': False})


def flutter_bootstrap_version(work, lane, e, flutter):
    # A first official invocation can write curl progress to stdout. Complete
    # that acquisition once, then require a separate clean machine response.
    command(work, 'flutter-bootstrap', [flutter/'bin/flutter', '--version'], work, e)
    version = json.loads(command(work, 'flutter-version', [flutter/'bin/flutter', '--version', '--machine'], work, e))
    if not isinstance(version, dict):
        raise ValueError('Flutter machine metadata object required')
    for key, wanted in [('frameworkVersion', '3.47.7'), ('frameworkRevision', lane['flutter']['commit']),
                        ('engineRevision', lane['flutter']['engine']), ('dartSdkVersion', '3.13.5')]:
        if version.get(key) != wanted: raise ValueError('exact Flutter/Dart release required')
    return version


def flutter_setup(work, lane, e):
    flutter = work/'flutter'
    command(work, 'flutter-init', ['/usr/bin/git', 'init', '--quiet', flutter], work, e)
    tag = 'refs/tags/3.47.7'
    command(work, 'flutter-fetch', ['/usr/bin/git', '-C', flutter, 'fetch', '--depth=1',
            'https://github.com/flutter/flutter.git', tag+':'+tag], work, e)
    command(work, 'flutter-checkout', ['/usr/bin/git', '-C', flutter, 'checkout', '--quiet', '--detach', tag], work, e)
    if command(work, 'flutter-object', ['/usr/bin/git', '-C', flutter, 'rev-parse', 'HEAD'], work, e) != (
            lane['flutter']['commit']+'\n').encode(): raise ValueError('official Flutter source differs')
    for row in lane['flutter_source_pins']:
        consume.read_file(flutter, row)
    if (flutter/'bin/internal/engine.version').read_text().strip() != lane['flutter']['engine']:
        raise ValueError('fixed engine revision required')
    flutter_bootstrap_architecture(work, e)
    # Bootstrap output is kept as evidence, never parsed as machine metadata.
    flutter_bootstrap_version(work, lane, e, flutter)
    # Preserve engine cache stamp contract from official Flutter; pre-cache is source acquisition,
    # not App compilation. The independently audited debug iOS engine must match R7 engine50.
    command(work, 'flutter-precache', [flutter/'bin/flutter', 'precache', '--ios'], work, e)
    engine = flutter/'bin/cache/artifacts/engine/ios/Flutter.xcframework'
    for row in json.loads((HERE/'inputs.json').read_bytes())['engine50']:
        consume.read_file(engine, row)
    command(work, 'flutter-config', [flutter/'bin/flutter', 'config', '--enable-swift-package-manager',
                                   '--no-analytics'], work, e)
    record(work/'evidence/actual-Flutter-cache.json', {'official_tag': lane['flutter'],
           'cache_files': packaging.vector(flutter/'bin/cache', work),
           'tool_sources': lane['flutter_source_pins'], 'first_cloud_acquisition_not_hermetic': True,
           'original_engine50_exact': True, 'Root_native_dependency_acceptance': False})


def check_public_plugin_source(work, declaration, root, row):
    try:
        return consume.read_file(root, row)
    except (ValueError, OSError) as failure:
        # Only this fixed public plugin declaration boundary emits diagnostics.
        # Never export source text or an absolute cache/account path.
        actual = {'available': False}
        try:
            root = Path(root)
            if root.resolve(strict=True) != root or not root.is_dir() or not root.is_relative_to(work):
                raise ValueError('unsafe public root')
            path = root
            for part in consume.safe_name(row['path']).split('/'):
                path = path/part
                if path.is_symlink(): raise ValueError('public symlink refused')
            with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW), 'rb') as stream:
                before = os.fstat(stream.fileno())
                if (not stat.S_ISREG(before.st_mode) or before.st_nlink != 1
                        or before.st_uid != os.getuid() or not 0 <= before.st_size <= 128*1024**2):
                    raise ValueError('unsafe public file')
                digest = hashlib.sha256(); total = 0
                for block in iter(lambda: stream.read(65536), b''):
                    total += len(block)
                    if total > 128*1024**2: raise ValueError('public diagnostic limit')
                    digest.update(block)
                after = os.fstat(stream.fileno())
            identity = lambda s: (s.st_dev, s.st_ino, s.st_mode, s.st_uid, s.st_nlink,
                                  s.st_size, s.st_mtime_ns, s.st_ctime_ns)
            if identity(before) != identity(after) or identity(after) != identity(path.lstat()) or total != before.st_size:
                raise ValueError('unstable public file')
            actual = {'available': True, 'bytes': total, 'sha256': digest.hexdigest(), 'stable': True}
        except (ValueError, OSError) as diagnostic_error:
            actual['unavailableReason'] = type(diagnostic_error).__name__
        record(work/'evidence/public-plugin-input-failure.json', {
            'schema': 1, 'declaration': declaration['name'], 'publicRelativePath': row['path'],
            'declared': {'bytes': row['bytes'], 'sha256': row['sha256']}, 'actual': actual,
            'failureType': type(failure).__name__, 'originalGateStillRefused': True,
            'sourceContentsOrAbsoluteRootRecorded': False})
        raise


def check_plugin_sources(work, plugins, roots, spec, phase):
    """Keep fixed plugin sources, but bind the intentional core replacement to R7."""
    if phase not in {'packages', 'resolve'}:
        raise ValueError('explicit core source check phase required')
    work = Path(work)
    sdk = work/'source/packages/flutter_openim_sdk'
    if work.resolve(strict=True) != work or roots.get('flutter_openim_sdk') != sdk:
        raise ValueError('exact owned local SDK package root required')
    prefix = 'ios/Framework/OpenIMCore.xcframework/'
    historical = []
    for declaration in plugins['declarations']:
        for row in declaration['native_files'] + [declaration['pubspec']]:
            if declaration['name'] == 'flutter_openim_sdk' and row['path'].startswith(prefix):
                historical.append(row)
            else:
                check_public_plugin_source(work, declaration, roots[declaration['name']], row)
    if len(historical) != 17 or len({r['path'] for r in historical}) != 17:
        raise ValueError('sealed historical SDK framework17 declaration required')
    original = work/'products/.qa/apple-cloud-1/OpenIMCore.xcframework'
    target = sdk/prefix
    if target.resolve(strict=True) != target or not target.is_dir():
        raise ValueError('canonical staged current framework required')
    rows = spec['core17']
    if len(rows) != 17 or len({r['path'] for r in rows}) != 17:
        raise ValueError('exact current accepted core17 required')
    expected_names = {consume.safe_name(r['path']) for r in rows}
    observed = set()
    for path in target.rglob('*'):
        if path.is_symlink():
            raise ValueError('staged framework symlink refused')
        if path.is_file():
            observed.add(path.relative_to(target).as_posix())
            if len(observed) > 17:
                raise ValueError('unexpected staged framework file')
        elif not path.is_dir():
            raise ValueError('nonregular staged framework member refused')
    if observed != expected_names:
        raise ValueError('staged current framework17 file set differs')
    adapted = {'ios-arm64/OpenIMCore.framework/Info.plist',
               'ios-arm64-simulator/OpenIMCore.framework/Info.plist'}
    if not adapted <= expected_names:
        raise ValueError('both fixed current wrapper plists required')
    proof = []
    for row in rows:
        before = consume.read_file(original, row)
        after = packaging.derived_plist(before) if row['path'] in adapted else before
        expected = {'path': prefix + row['path'], 'bytes': len(after), 'sha256': consume.digest(after)}
        check_public_plugin_source(work, {'name': 'flutter_openim_sdk'}, sdk, expected)
        proof.append({'path': row['path'], 'bytes': len(after), 'sha256': expected['sha256'],
                      'accepted_original_sha256': row['sha256'], 'wrapper_only_changed': row['path'] in adapted})
    record(work/'evidence'/('current359-plugin-core17-'+phase+'.json'), {
        'phase': phase, 'historical_framework17_pins_unchanged': True,
        'current17_from_accepted_originals_and_exact_two_plist_derivations': proof,
        'all_other_plugin_native_pubspec_pins_checked': True,
        'no_binary_or_App_source_changed': True})


def packages(work, plugins, e, spec):
    command(work, 'pub-get', [work/'flutter/bin/flutter', 'pub', 'get', '--enforce-lockfile'], work/'source/app', e)
    config = json.loads(packaging.regular(work/'source/app/.dart_tool/package_config.json'))
    roots = {}
    base = (work/'source/app/.dart_tool/package_config.json').as_uri()
    for row in config['packages']:
        uri = urllib.parse.urlsplit(urllib.parse.urljoin(base, row['rootUri']))
        if uri.scheme != 'file' or uri.netloc:
            raise ValueError('owned package file URI required')
        root = Path(urllib.parse.unquote(uri.path)).resolve(strict=True)
        if not root.is_relative_to(work): raise ValueError('package path outside owned closure')
        roots[row['name']] = root
    check_plugin_sources(work, plugins, roots, spec, 'packages')
    for hook in plugins['hooks']:
        consume.read_file(roots[hook['name']], hook['hook'])
    lane = json.loads((HERE/'lane.json').read_bytes())
    for row in lane['hook_source_pins']:
        consume.read_file(roots[row['package']], {k: row[k] for k in ['path', 'bytes', 'sha256']})
    # Archive identities are enforced by pub from the fixed178 lock, then capture its exact receipts.
    hashes = packaging.vector(work/'pub-cache/hosted-hashes')
    record(work/'evidence/pub-archive-hashes.json', {'pub_enforce_lockfile': True, 'files': hashes,
           'independent_redownload_of_all178': False})
    return roots


def capture(work, label):
    roots = [work/'source/app/ios', work/'source/app/.dart_tool',
             work/'source/app/build/ios/SourcePackages', work/'cocoapods',
             work/'source/packages/flutter_openim_sdk/ios']
    rows = []
    copied = 0
    for root in roots:
        if root.exists():
            rows.append({'root': root.relative_to(work).as_posix(), 'files': packaging.vector(root, work)})
            for path in sorted(root.rglob('*')):
                if (path.is_symlink() or not path.is_file() or path.resolve() != path
                        or path.name not in {'Package.swift', 'Package.resolved', 'Podfile', 'Podfile.lock',
                                             'Manifest.lock', 'project.pbxproj'}
                        and not path.name.endswith(('.podspec', '.podspec.json', '.xcconfig'))):
                    continue
                body = packaging.regular(path, 1024**2)
                copied += len(body)
                if copied > 8*1024**2: raise ValueError('bounded native manifest raw budget')
                put(work/'evidence'/label/path.relative_to(work), body)
    record(work/'evidence'/f'{label}-native-snapshot.json', {'trees': rows})
    return rows


def host_snapshot(original, host):
    rows = []
    tool_dir = Path(original['native']['developer'])/'Toolchains/XcodeDefault.xctoolchain/usr/bin'
    streamed = {str(tool_dir/n) for n in ['swift-frontend', 'clang', 'ld', 'lipo', 'ar', 'llvm-nm', 'llvm-otool']}
    for row in original['host_pins']:
        value = host.fingerprint(Path(row['path']), 512*1024**2 if row['path'] in streamed else 256*1024**2,
                                 row['official_system_hardlink'])
        if value['bytes'] != row['bytes'] or value['sha256'] != row['sha256']:
            raise ValueError('original accepted Apple host input differs')
        rows.append(value)
    return rows


def official_pod_sources(selected, lane):
    """Check fixed host tools and immutable official source before loading the gem."""
    wanted = lane['cocoapods_preinstalled']
    pod_selector.require_identity(selected, wanted)
    root = Path(selected['gem_root'])/'gems'/('cocoapods-'+wanted['version'])
    rows = []
    for item in wanted['source_files']:
        consume.read_file(root, item)
        rows.append({k: item[k] for k in ['path', 'bytes', 'sha256']})
    return {'official_commit': wanted['official_commit'], 'version': wanted['version'],
            'gem_root': str(root), 'files': rows}


def pod_tool_sources(work, selected, e, lane):
    pod = pod_selector.recheck(selected, Path(e['HOME']))
    # Read the selected preinstalled script's interpreter, never install a gem or use user gems.
    header = pod.read_bytes().splitlines()[0].decode('ascii')
    if not header.startswith('#!/') or len(header.split()) != 1:
        raise ValueError('absolute preinstalled CocoaPods Ruby interpreter required')
    ruby = Path(header[2:]).resolve(strict=True)
    if ruby != Path(selected['ruby']['path']) or not str(ruby).startswith('/opt/homebrew/'):
        raise ValueError('preinstalled Homebrew Ruby source required')
    # Pinned probe distinguishes default specs, compiled startup markers and files.
    source = json.loads(command(work, 'pod-source', [ruby, HERE/'gem_probe.rb'], work, e))
    try:
        proof = gem_audit.audit(source, selected, Path(e['HOME']), lane['cocoapods_config'])
    except gem_audit.AuditFailure as error:
        record(work/'evidence/gem-source-rejected.json', error.proof)
        raise
    proof['script'] = {'path': str(pod), 'bytes': pod.stat().st_size, 'sha256': sha(pod)}
    record(work/'evidence/native-gem-sources.json', proof)
    pod_selector.recheck(selected, Path(e['HOME']))
    official_pod_sources(selected, lane)


def selected_pod(work, lane):
    raw = packaging.regular(work/'evidence/selected-cocoapods.json', 64*1024)
    acquired = json.loads(packaging.regular(work/'evidence/acquire-complete.json'))
    if acquired.get('selected_cocoapods_sha256') != packaging.sha(raw):
        raise ValueError('original acquired CocoaPods selection differs')
    selected = json.loads(raw)
    pod_selector.recheck(selected, Path(os.environ['HOME']))
    official_pod_sources(selected, lane)
    return selected


def acquire(work, lane, spec, plugins):
    selected = pod_selector.select(Path(os.environ['HOME']))
    record(work/'evidence/selected-cocoapods.json', selected)
    pod = pod_selector.recheck(selected, Path(os.environ['HOME']))
    e = env(work, selected)
    record(work/'evidence/cocoapods-official-source-pins.json', official_pod_sources(selected, lane))
    for name in ['tmp', 'cache', 'config', 'cocoapods', 'cocoapods/repos', 'cocoapods/cache', 'spm', 'origins']:
        mkdir(work/name)
    if command(work, 'pod-version', [pod, '--version'], work, e) != b'1.17.0\n':
        raise ValueError('preinstalled CocoaPods1.17.0 required; no gem install')
    pod_tool_sources(work, selected, e, lane)
    source_stage(work, spec, e)
    reuse_core(work, lane, spec)
    host = load(REPO/'ci/apple-host-tool/run.py', 'app359_host_tool')
    real = load(REPO/'ci/apple-sdk-link-real/run.py', 'app359_real_tool')
    original = json.loads((REPO/'ci/apple-sdk-link-real/inputs.json').read_bytes())
    real.accepted_tool(original)
    host_before = host_snapshot(original, host)
    for label, argv in [('host-signature', host.apple_signature_argv(lane['xcodebuild'])),
                        ('host-version', [lane['xcodebuild'], '-version'])]:
        command(work, label, argv, work, e)
    host.strict_version(0, (work/'evidence/host-version.stdout.log').read_bytes(),
                        (work/'evidence/host-version.stderr.log').read_bytes(), 'Xcode 16.4\nBuild version 16F6')
    flutter_setup(work, lane, e)
    packages(work, plugins, e, spec)
    origins = []
    for row in lane['native_origins']:
        target = work/'origins'/row['filename']
        proof = fetch_fixed(row, target)
        if row['kind'] == 'sqlite3_hook':
            proof['seed'] = packaging.seed_asset(target, row, work/'source', put)
        else:
            with zipfile.ZipFile(target) as z:
                if len(z.infolist()) > 8192 or sum(i.file_size for i in z.infolist()) > 2*GIB:
                    raise ValueError('WebRTC archive expanded budget')
                seen = set()
                for i in z.infolist():
                    name = custody.safe_name(i.filename.rstrip('/'))
                    if name.casefold() in seen or i.flag_bits & 1:
                        raise ValueError('WebRTC archive unsafe/duplicate member')
                    seen.add(name.casefold())
                if z.testzip() is not None: raise ValueError('WebRTC whole archive CRC failure')
                proof['members'] = [{'path': i.filename, 'bytes': i.file_size,
                     'mode': i.external_attr >> 16, 'sha256': hashlib.sha256(z.read(i)).hexdigest()}
                     for i in z.infolist() if not i.is_dir()]
        origins.append(proof)
    record(work/'evidence/native-origin-acquisition.json', {'objects': origins, 'Root_acceptance': False})
    pod_selector.recheck(selected, Path(e['HOME']))
    record(work/'evidence/acquire-complete.json', {'identity': IDENTITY,
           'selected_cocoapods_sha256': sha(work/'evidence/selected-cocoapods.json'),
           'source_ref': spec['source_ref'], 'accepted_host': host_before,
           'original_core17': spec['core17'], 'no_Go_or_bind': True})


def resolve(work, lane, spec, plugins):
    if json.loads(packaging.regular(work/'acquire-resource.json'))['status'] != 'passed':
        raise ValueError('successful guarded acquisition required')
    acquired = json.loads(packaging.regular(work/'evidence/acquire-complete.json'))
    if acquired['identity'] != IDENTITY or acquired['source_ref'] != spec['source_ref']:
        raise ValueError('acquisition identity differs')
    selected = selected_pod(work, lane)
    e = env(work, selected)
    configuration = json.loads(packaging.regular(work/'source/app/.dart_tool/package_config.json'))
    base = (work/'source/app/.dart_tool/package_config.json').as_uri()
    roots = {p['name']: Path(urllib.parse.unquote(urllib.parse.urlsplit(
             urllib.parse.urljoin(base, p['rootUri'])).path)).resolve(strict=True)
             for p in configuration['packages']}
    host = load(REPO/'ci/apple-host-tool/run.py', 'app359_resolve_host')
    real = load(REPO/'ci/apple-sdk-link-real/run.py', 'app359_resolve_real')
    original = json.loads((REPO/'ci/apple-sdk-link-real/inputs.json').read_bytes())
    real.accepted_tool(original)
    host_before = host_snapshot(original, host)
    if host_before != acquired['accepted_host']:
        raise ValueError('acquired official host identity differs')
    capture(work, 'before-resolution')
    try:
        # Official fixed tool generates real plugin packages, fetches SPM then installs fallback Pods.
        # This config-only path returns after processPodsIfNeeded, before actual xcodebuild build.
        command(work, 'ios-config-only', [work/'flutter/bin/flutter', 'build', 'ios', '--debug',
                '--no-codesign', '--no-pub', '--config-only', '--target=lib/main.dart'], work/'source/app', e)
    finally:
        after = capture(work, 'after-resolution')  # failures retain real locks/partial generated sources
    graph = json.loads(packaging.regular(work/'source/app/.flutter-plugins-dependencies'))
    graph_proof = consume.verify_plugin_graph(graph, plugins, roots)
    check_plugin_sources(work, plugins, roots, spec, 'resolve')
    frameworks = list((work/'source/app/build/ios/SourcePackages').rglob('WebRTC.xcframework'))
    if len(frameworks) != 1:
        raise ValueError('one actual resolved WebRTC binary target required')
    webrtc = packaging.webrtc_consumed(work/'origins/WebRTC.xcframework.zip', frameworks[0])
    record(work/'evidence/WebRTC-consumed.json', webrtc)
    locks = {}
    paths = list((work/'source/app/ios').rglob('Package.resolved')) + [work/'source/app/ios/Podfile.lock']
    for path in paths:
        body = packaging.regular(path, 2*1024**2)
        key = path.relative_to(work).as_posix()
        locks[key] = {'bytes': len(body), 'sha256': packaging.sha(body)}
        put(work/'evidence/native-locks'/key, body)
    if not locks or not any(p.endswith('Podfile.lock') for p in locks):
        raise ValueError('fresh fallback Pod lock required')
    # A graph with only local SPM packages can legally have no Package.resolved.
    # The complete package manifests + artifact caches remain required evidence, not a fabricated empty lock.
    if host_snapshot(original, host) != host_before:
        raise ValueError('accepted host tools changed')
    consume.verify_source(work/'original-source', spec)
    preserved = spec['business_files'] + [r for r in spec['native7']
                 if r['path'].startswith('app/ios/') and not r['path'].endswith('project.pbxproj')]
    for row in preserved:
        consume.read_file(work/'source', row)
    project = 'app/ios/Runner.xcodeproj/project.pbxproj'
    original_project = packaging.regular(work/'original-source'/project)
    derived_project = packaging.regular(work/'source'/project)
    put(work/'evidence/generated-project.before.pbxproj', original_project)
    put(work/'evidence/generated-project.after.pbxproj', derived_project)
    pod_selector.recheck(selected, Path(e['HOME']))
    metadata()
    resolution = {'identity': IDENTITY, 'source_ref': spec['source_ref'],
           'source_digest': spec['source_digest'], 'source_members': 477, 'plugin_graph': graph_proof,
           'native_locks': locks, 'accepted_host': host_before,
           'project_packaging_only': {'before_sha256': packaging.sha(original_project),
                                      'after_sha256': packaging.sha(derived_project),
                                      'derived_changes_require_Root_review': True},
           'origins': json.loads(packaging.regular(work/'evidence/native-origin-acquisition.json')),
           'pods_spm_sources_or_binary_acceptance': False, 'App_build_executed': False,
           'required_next': 'Root review complete native/tool/source bodies before a new approved packaging successor'}
    record(work/'evidence/resolution.json', resolution)
    record(work/'evidence/catalogue.json', packaging.native_catalogue(lane, resolution, after,
           json.loads(packaging.regular(work/'evidence/native-gem-sources.json'))))


def package(work, lane, spec):
    # Current descriptor contains null; this check runs before any native command.
    resolution = packaging.require_approval(lane, packaging.regular(work/'evidence/catalogue.json'))
    selected = selected_pod(work, lane)
    e = env(work, selected)
    before = capture(work, 'before-App')
    for kind, argv in packaging.app_commands(work, lane['xcodebuild']):
        command(work, 'App-'+kind, argv, work/'source/app/ios', e)
    after = capture(work, 'after-App')
    pod_selector.recheck(selected, Path(e['HOME']))
    record(work/'evidence/App-compile-results.json', {'identity': IDENTITY,
           'native_resolution_source_ref': resolution['source_ref'],
           'before_snapshot_trees': len(before), 'after_snapshot_trees': len(after),
           'compiled_without_signing': True, 'binary_embedding_audit_accepted': False,
           'device_simulator_launch_or_services': False})


def profile(work, stage):
    text = '(version 1)(allow default)(deny network*)(deny file-write*)'
    text += '(allow file-write* (subpath '+json.dumps(str(work))+') (literal "/dev/null"))'
    if stage in {'acquire', 'resolve'}:
        text += '(allow network-outbound (remote ip "*:443") (remote ip "*:53") (literal "/private/var/run/mDNSResponder"))'
    protected = ['acquire.sb', 'resolve.sb', 'package.sb', 'activation.json', 'tool-adapters']
    if stage != 'acquire':
        protected += ['original-source', 'raw', 'products', 'origins', 'engine',
                      'evidence/selected-cocoapods.json', 'evidence/acquire-complete.json']
    for p in protected:
        text += '(deny file-write* (subpath '+json.dumps(str(work/p))+'))'
    return text.encode()


def main():
    os.umask(0o077)  # only this task process and descendants, never user global settings
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--identity', choices=[IDENTITY], required=True)
    p.add_argument('--stage', choices=['readonly', 'acquire', 'resolve', 'package'], default='readonly')
    p.add_argument('--work', type=Path)
    p.add_argument('--worker', action='store_true', help=argparse.SUPPRESS)
    a = p.parse_args()
    lane, spec, plugins = metadata()
    if a.stage == 'readonly': return 0
    if (platform.system() != 'Darwin' or platform.machine() != 'arm64'
            or sys.version_info[:2] != (3, 12)):
        raise ValueError('standard macOS15 ARM64/Python3.12 runner required')
    root = Path(os.environ['RUNNER_TEMP']).resolve(strict=True)
    work = a.work
    if work != root/'luobo-apple-current359' or work.is_symlink():
        raise ValueError('fixed owned fresh run root required')
    if a.worker:
        xcrun_adapter.check(work, HERE/'xcrun_adapter.py', sys.executable)
        if a.stage == 'acquire': acquire(work, lane, spec, plugins)
        elif a.stage == 'resolve': resolve(work, lane, spec, plugins)
        else: package(work, lane, spec)
        return 0
    if a.stage == 'package':
        packaging.require_approval(lane, packaging.regular(work/'evidence/catalogue.json'))
    minimum = 20*GIB if a.stage == 'acquire' else 4*GIB
    s = os.statvfs(root)
    if s.f_bavail*s.f_frsize < minimum: raise ValueError('cold20GiB/phase4GiB admission required')
    if a.stage == 'acquire':
        if work.exists(): raise ValueError('no partial resume or old outputs')
        mkdir(work); mkdir(work/'evidence')
        installed = xcrun_adapter.install(work, HERE/'xcrun_adapter.py', sys.executable)
        record(work/'evidence/xcrun-adapter-installed.json', installed)
        record(work/'activation.json', {'identity': IDENTITY, 'lane_sha256': sha(HERE/'lane.json'),
               'source_ref': spec['source_ref'], 'controller_head': os.environ.get('GITHUB_SHA')})
        for stage in ['acquire', 'resolve', 'package']:
            put(work/(stage+'.sb'), profile(work, stage))
    elif json.loads(packaging.regular(work/'activation.json'))['lane_sha256'] != sha(HERE/'lane.json'):
        raise ValueError('fixed original activation differs')
    xcrun_adapter.check(work, HERE/'xcrun_adapter.py', sys.executable)
    guard = load(REPO/'ci/apple-sdk/resource_dual.py', 'current359_single_resource_guard')
    def interrupted(_number, _frame): raise InterruptedError('current359 native guard interrupted')
    for number in [signal.SIGTERM, signal.SIGINT]: signal.signal(number, interrupted)
    args = ['/usr/bin/sandbox-exec', '-f', str(work/(a.stage+'.sb')), sys.executable, '-B',
            str(HERE/'run.py'), '--identity', IDENTITY, '--stage', a.stage, '--work', str(work), '--worker']
    return guard.run(SimpleNamespace(command=args, cwd=work, log=work/(a.stage+'-raw.log'),
         record=work/(a.stage+'-resource.json'), min_free_mib=1024, poll_interval=.2,
         allocated_root=work, max_allocated_mib=16*1024, timeout=1800,
         secondary_root=None, secondary_cap_mib=None))


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception as error:
        # Raw command logs retain public source diagnostics; do not echo signed URLs/token exceptions.
        frames = []
        for frame in traceback.extract_tb(error.__traceback__):
            path = Path(frame.filename)
            if path.is_relative_to(REPO/'ci'):
                frames.append({'path': path.relative_to(REPO).as_posix(), 'line': frame.lineno,
                               'function': frame.name})
        print('CURRENT359_NATIVE_REFUSED '+json.dumps({'exception': type(error).__name__,
              'approved_source_frames': frames[-8:]}), file=sys.stderr)
        raise SystemExit(1)
