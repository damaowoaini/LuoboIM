"""CI-owned argv adapter. Every admitted invocation execs Apple's real xcrun."""
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import stat
import sys


REAL_XCRUN = '/usr/bin/xcrun'
REAL_ARCH = '/usr/bin/arch'
IDENTITY = 'current359-null2-apple-native-resolution-v2'
SCRIPT_CAP = 64 * 1024
VALUE_FLAGS = {'-project', '-workspace', '-scheme', '-configuration', '-target',
               '-sdk', '-destination', '-clonedSourcePackagesDirPath',
               '-derivedDataPath', '-packageCachePath', '-resultBundlePath'}
BOOLEAN_FLAGS = {'-alltargets', '-skipPackagePluginValidation', '-skipPackageSignatureValidation'}
OPERATIONS = {'-resolvePackageDependencies', '-list', '-showBuildSettings'}


def owned(work, value, cwd):
    """No symlink, parent traversal or external path can become an owned argument."""
    if not isinstance(value, str) or not value or len(value) > 4096:
        raise ValueError('owned path shape')
    if any(ord(c) < 32 or ord(c) == 127 for c in value) or '..' in value.split('/'):
        raise ValueError('owned path syntax')
    p = Path(value)
    if not p.is_absolute(): p = cwd/p
    # resolve(False) checks existing ancestors as well as an existing leaf.
    absolute = Path(os.path.abspath(p))
    if absolute.resolve() != absolute or not absolute.is_relative_to(work):
        raise ValueError('owned canonical path required')
    return absolute


def forwarding(work, argv, cwd):
    """Pure argv/path decision; never runs a tool or invents a tool response."""
    work, cwd = Path(work), Path(cwd)
    if not work.is_absolute() or work.resolve(strict=True) != work or not work.is_dir():
        raise ValueError('canonical work root required')
    cwd = owned(work, str(cwd), work)
    if not cwd.is_dir(): raise ValueError('existing owned cwd required')
    if (not isinstance(argv, list) or not 1 <= len(argv) <= 48
            or any(not isinstance(a, str) or not a or len(a) > 4096
                   or any(ord(c) < 32 or ord(c) == 127 for c in a) for a in argv)
            or sum(map(len, argv)) > 16384):
        raise ValueError('finite argv required')
    passthrough = [['xcodebuild', '-version'], ['clang'],
                   ['simctl', 'list', 'devices', 'booted'], ['devicectl', '--version']]
    sdk_query = (len(argv) == 3 and argv[0] == '--sdk'
                 and argv[1] in {'iphoneos', 'iphonesimulator'}
                 and argv[2] in {'--show-sdk-path', '--show-sdk-platform-version'})
    if argv in passthrough or sdk_query:
        return [REAL_XCRUN, '--no-cache', *argv], argv.copy()
    # Flutter's device inventory query is read-only; no UDID or device operation.
    if (len(argv) == 4 and argv[:3] == ['xcdevice', 'list', '--timeout']
            and argv[3].isascii() and argv[3].isdecimal() and 1 <= int(argv[3]) <= 60):
        return [REAL_XCRUN, '--no-cache', *argv], argv.copy()
    if argv[0] != 'xcodebuild': raise ValueError('unproven xcrun tool or form')
    ios = work/'source/app/ios'
    if not cwd.is_relative_to(ios): raise ValueError('owned iOS project cwd required')
    seen, values, operations, public = set(), {}, [], ['xcodebuild']
    i = 1
    while i < len(argv):
        flag = argv[i]
        if flag in seen: raise ValueError('duplicate or conflicting xcodebuild flag')
        seen.add(flag)
        if flag in OPERATIONS:
            operations.append(flag); public.append(flag); i += 1; continue
        if flag in BOOLEAN_FLAGS:
            public.append(flag); i += 1; continue
        if flag in VALUE_FLAGS:
            if i + 1 >= len(argv): raise ValueError('missing xcodebuild value')
            value = argv[i + 1]; values[flag] = value
            if flag in {'-project', '-workspace', '-clonedSourcePackagesDirPath',
                        '-derivedDataPath', '-packageCachePath', '-resultBundlePath'}:
                path = owned(work, value, cwd)
                if flag in {'-project', '-workspace'}:
                    suffix = '.xcodeproj' if flag == '-project' else '.xcworkspace'
                    proven = ({ios/'Runner.xcodeproj', ios/'Pods/Pods.xcodeproj'}
                              if flag == '-project' else {ios/'Runner.xcworkspace'})
                    if path not in proven or path.suffix != suffix or not path.is_dir():
                        raise ValueError('owned existing project/workspace required')
                elif flag == '-clonedSourcePackagesDirPath':
                    if path != work/'source/app/build/ios/SourcePackages':
                        raise ValueError('fixed original Flutter cloned packages path required')
                elif flag == '-resultBundlePath':
                    if path != work/'xcode/Resolve.xcresult':
                        raise ValueError('fixed owned package-resolution result bundle required')
                elif path != work/'xcode'/('DerivedData' if flag == '-derivedDataPath' else 'PackageCache'):
                    raise ValueError('conflicting cache path')
                public.extend([flag, 'work/'+path.relative_to(work).as_posix()])
            else:
                allowed = {'-scheme': {'Runner'}, '-target': {'Runner'},
                           '-configuration': {'Debug', 'Release', 'Profile'},
                           '-sdk': {'iphoneos', 'iphonesimulator'},
                           '-destination': {'generic/platform=iOS', 'generic/platform=iOS Simulator'}}
                if value not in allowed[flag]: raise ValueError('unproven xcodebuild value')
                public.extend([flag, value])
            i += 2; continue
        if '=' in flag:
            name, value = flag.split('=', 1)
            if name not in {'BUILD_DIR', 'OBJROOT'} or name in values:
                raise ValueError('unproven or duplicate build setting')
            path = owned(work, value, cwd)
            if path != work/'source/app/build/ios': raise ValueError('fixed owned Flutter build path required')
            values[name] = value; public.append(name+'=work/'+path.relative_to(work).as_posix())
            i += 1; continue
        raise ValueError('unproven xcodebuild argument')
    if len(operations) != 1 or ('-project' in values and '-workspace' in values):
        raise ValueError('one non-conflicting project operation required')
    if not ({'-project', '-workspace'} & values.keys()):
        # The fixed Flutter prefetch and list forms discover Runner from ios cwd.
        if (cwd != ios or '-clonedSourcePackagesDirPath' not in values
                or operations[0] not in {'-resolvePackageDependencies', '-list'}
                or not (ios/'Runner.xcodeproj').is_dir()
                or (ios/'Runner.xcodeproj').resolve() != ios/'Runner.xcodeproj'):
            raise ValueError('unproven implicit project form')
    operation = operations[0]
    if '-resultBundlePath' in values and operation != '-resolvePackageDependencies':
        raise ValueError('result bundle permitted only for package resolution')
    pod_query = (values.get('-project') is not None
                 and owned(work, values['-project'], cwd) == ios/'Pods/Pods.xcodeproj')
    if pod_query and (operation != '-showBuildSettings' or '-alltargets' not in seen):
        raise ValueError('unproven Pods query')
    # Listing discovers schemes. It must not be turned into a scheme action.
    # Pods' all-target settings query also has no single Runner scheme.
    if operation == '-list':
        if {'-scheme', '-target'} & values.keys() or '-alltargets' in seen:
            raise ValueError('unproven list form')
        scheme_action = False
    elif operation == '-resolvePackageDependencies':
        if ('-clonedSourcePackagesDirPath' not in values or '-target' in values
                or '-alltargets' in seen or pod_query):
            raise ValueError('unproven package prefetch form')
        scheme_action = True
    else:
        scheme_action = '-target' not in values and '-alltargets' not in seen
        if not scheme_action and '-scheme' in values:
            raise ValueError('scheme conflicts with target settings query')
        if '-alltargets' in seen and (values.get('-project') is None
                or owned(work, values['-project'], cwd) != ios/'Pods/Pods.xcodeproj'
                or '-target' in values):
            raise ValueError('unproven all-target settings query')
    if not scheme_action:
        if {'-derivedDataPath', '-packageCachePath'} & values.keys():
            raise ValueError('cache injection into non-scheme query refused')
        return [REAL_XCRUN, '--no-cache', *argv], public
    additions = []
    if '-scheme' not in values:
        # The source477 shared scheme is pinned separately by the unchanged
        # source controller: Runner -> Runner.app in container:Runner.xcodeproj.
        # Do not pass -derivedDataPath without naming that reviewed scheme.
        additions.extend(['-scheme', 'Runner']); public.extend(['-scheme', 'Runner'])
    for flag, name in [('-derivedDataPath', 'DerivedData'), ('-packageCachePath', 'PackageCache')]:
        if flag not in values:
            additions.extend([flag, str(work/'xcode'/name)])
            public.extend([flag, 'work/xcode/'+name])
    return [REAL_XCRUN, '--no-cache', *argv, *additions], public


def script_body(source, python):
    if (not isinstance(python, str) or not re.fullmatch(r'/[A-Za-z0-9_./-]{1,119}', python)
            or '..' in python.split('/')):
        raise ValueError('fixed absolute Python entry required')
    if not source or len(source) > SCRIPT_CAP: raise ValueError('adapter source cap')
    # arch applies its subtype preference to the interpreter of a shebang.
    # The platform shell supplies that entry; the pinned Python is an ordinary
    # shell exec. The final real Apple exec below explicitly reapplies arm64e.
    # This header is also a Python multiline string, so __file__ stays this file.
    return ('#!/bin/sh\n""":"\nexec '+python+' -B "$0" "$@"\n":"""\n').encode()+source


def apple_execution(argv):
    if not isinstance(argv, list) or argv[:2] != [REAL_XCRUN, '--no-cache']:
        raise ValueError('reviewed real Apple argv required')
    return [REAL_ARCH, '-arm64e', *argv]


def cache_directory_identity(path, uid):
    """Stable, current-user private directory. No creation and no contents read."""
    path = Path(path)
    if not path.is_absolute() or path.resolve(strict=True) != path:
        raise ValueError('canonical Darwin temporary directory required')
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        before = os.fstat(fd)
        if (not stat.S_ISDIR(before.st_mode) or before.st_uid != uid
                or stat.S_IMODE(before.st_mode) != 0o700):
            raise ValueError('current-user private Darwin temporary directory required')
        after = os.fstat(fd)
        at = path.lstat()
        keys = ('st_dev', 'st_ino', 'st_uid', 'st_gid', 'st_mode')
        identity = [getattr(before, key) for key in keys]
        if identity != [getattr(after, key) for key in keys] or identity != [getattr(at, key) for key in keys]:
            raise ValueError('Darwin temporary directory changed')
        return identity
    finally:
        os.close(fd)


def discover_cache_directory():
    # Apple unistd.h fixes _CS_DARWIN_USER_TEMP_DIR=65537. Reject its documented
    # TMPDIR/P_tmpdir fallbacks: only the actual hosted-user /var/folders T.
    if sys.platform != 'darwin': raise ValueError('Darwin cache query required')
    value = os.confstr(65537)
    if (not isinstance(value, str) or not re.fullmatch(
            r'/(?:private/)?var/folders/[A-Za-z0-9_-]{2}/[A-Za-z0-9_-]{20,64}/T/', value)):
        raise ValueError('fixed Darwin user temporary directory shape required')
    lexical = Path(value)
    canonical = lexical.resolve(strict=True)
    if canonical != Path('/private')/str(lexical).lstrip('/') and canonical != lexical:
        raise ValueError('unexpected Darwin temporary directory alias')
    if not str(canonical).startswith('/private/var/folders/'):
        raise ValueError('fixed canonical Darwin temporary directory root required')
    return {'schema': 1, 'lexical': str(lexical), 'canonical': str(canonical),
            'uid': os.getuid(), 'directory_identity': cache_directory_identity(canonical, os.getuid())}


def cache_leaf_pattern(directory):
    # Eight alphanumeric suffix characters are the exact observed xcrun form.
    # Spell out each character class for the SBPL regex compiler. This keeps
    # the exact same filename language without counted-repetition syntax.
    return '^'+re.escape(str(directory))+'/xcrun_db-'+'[A-Za-z0-9]'*8+'$'


def seal_cache_boundary(work):
    row = discover_cache_directory()
    root = Path(row['canonical'])
    protected = []
    for number, child in enumerate(root.iterdir()):
        if number >= 4096: raise ValueError('bounded Darwin temporary directory inventory required')
        if child.name == 'xcrun_db' or child.name.startswith('xcrun_db-'):
            if len(protected) >= 256 or len(child.name) > 128:
                raise ValueError('bounded preexisting cache protection required')
            # Only public tool cache names; no body, environment or private path
            # outside this fixed current-user directory is inventoried.
            if not re.fullmatch(r'xcrun_db(?:-[A-Za-z0-9_-]{1,96})?', child.name):
                raise ValueError('unexpected preexisting xcrun cache name')
            protected.append(child.name)
    row['protected_preexisting'] = sorted(protected)
    target = Path(work)/'tool-adapters/darwin-cache.json'
    body = (json.dumps(row, sort_keys=True)+'\n').encode()
    fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o400)
    with os.fdopen(fd, 'wb') as out:
        out.write(body); out.flush(); os.fsync(out.fileno())
    return row


def cache_boundary(work):
    path = Path(work)/'tool-adapters/darwin-cache.json'
    st = path.lstat()
    if (path.resolve(strict=True) != path or not stat.S_ISREG(st.st_mode)
            or st.st_uid != os.getuid() or stat.S_IMODE(st.st_mode) != 0o400
            or st.st_nlink != 1 or not 1 <= st.st_size <= 65536):
        raise ValueError('immutable owned Darwin cache boundary required')
    row = json.loads(path.read_bytes())
    current = discover_cache_directory()
    if {k: row.get(k) for k in current} != current:
        raise ValueError('Darwin cache boundary changed')
    names = row.get('protected_preexisting')
    if (not isinstance(names, list) or len(names) > 256 or names != sorted(set(names))
            or any(not isinstance(n, str) or not re.fullmatch(r'xcrun_db(?:-[A-Za-z0-9_-]{1,96})?', n) for n in names)):
        raise ValueError('preexisting cache protection differs')
    return row


def cache_profile(work):
    row = cache_boundary(work)
    # Discovery proves both spellings resolve to this same original directory.
    # Match either spelling without granting another directory or a subtree.
    directories = list(dict.fromkeys([row['canonical'], row['lexical']]))
    text = ''.join('(allow file-write* (regex #'+json.dumps(cache_leaf_pattern(p))+'))'
                   for p in directories)
    # The actual R18 tools created the bounded temporary leaves, then failed
    # to rename them to this one final leaf. Admit that exact destination only
    # when it was absent from the sealed inventory; an existing cache stays
    # protected. This is not a subtree grant or a claim of exclusive ownership.
    if 'xcrun_db' not in row['protected_preexisting']:
        text += ''.join('(allow file-write* (literal '+json.dumps(p+'/xcrun_db')+'))'
                        for p in directories)
    for directory in directories:
        for name in row['protected_preexisting']:
            text += '(deny file-write* (literal '+json.dumps(directory+'/'+name)+'))'
    return text


def private_cache_path(work):
    # Apple's shipped libxcrun accepts xcrun_db as an absolute cache filename.
    # Bind every shim and direct tool to this job's private directory instead
    # of renaming over another process's existing Darwin-user cache.
    work = Path(work)
    path = owned(work, str(work/'xcode/ToolCache/xcrun_db'), work)
    directory = path.parent
    info = directory.lstat()
    if (not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid()
            or stat.S_IMODE(info.st_mode) != 0o700):
        raise ValueError('private owned tool cache directory required')
    if path.exists():
        info = path.lstat()
        if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid()
                or info.st_nlink != 1 or stat.S_IMODE(info.st_mode) & 0o077
                or info.st_size > 64 << 20):
            raise ValueError('private owned tool cache file required')
    return path


def private_cache_profile(work):
    # No write exception in the original user T directory is necessary after
    # redirection. Keep both validated aliases entirely read-only. The private
    # cache is already covered by the original primary allocation guard.
    row = cache_boundary(work)
    return ''.join('(deny file-write* (subpath '+json.dumps(p)+'))'
                   for p in dict.fromkeys([row['canonical'], row['lexical']]))


def check(work, source_path, python):
    work = Path(work)
    path = work/'tool-adapters/xcrun'
    if work.resolve(strict=True) != work or path.resolve(strict=True) != path:
        raise ValueError('owned immutable adapter location')
    expected = script_body(Path(source_path).read_bytes(), python)
    st = path.lstat()
    if (not stat.S_ISREG(st.st_mode) or stat.S_IMODE(st.st_mode) != 0o500
            or st.st_nlink != 1 or st.st_uid != os.getuid() or st.st_size != len(expected)
            or path.read_bytes() != expected):
        raise ValueError('installed source-pinned adapter differs')
    for relative in ['cf-home', 'xcode/DerivedData', 'xcode/PackageCache', 'xcode/ToolCache', 'evidence/xcrun-adapter']:
        target = work/relative
        if target.resolve(strict=True) != target or not target.is_dir():
            raise ValueError('owned adapter support directory differs')
    return {'script_sha256': hashlib.sha256(expected).hexdigest(), 'script_bytes': len(expected),
            'source_sha256': hashlib.sha256(Path(source_path).read_bytes()).hexdigest(),
            'entry_interpreter': '/bin/sh', 'fixed_python': python,
            'outer_exec': REAL_ARCH, 'real_tool': REAL_XCRUN,
            'required_architecture': 'arm64e', 'no_cache': True, 'tool_response_synthesized': False}


def install(work, source_path, python):
    work = Path(work)
    for relative in ['tool-adapters', 'cf-home', 'xcode/DerivedData', 'xcode/PackageCache', 'xcode/ToolCache',
                     'evidence/xcrun-adapter']:
        (work/relative).mkdir(mode=0o700, parents=True, exist_ok=False)
    path = work/'tool-adapters/xcrun'
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o500)
    with os.fdopen(fd, 'wb') as out:
        out.write(script_body(Path(source_path).read_bytes(), python)); out.flush(); os.fsync(out.fileno())
    fd = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try: os.fsync(fd)
    finally: os.close(fd)
    return check(work, source_path, python)


def audit(work, public, cwd):
    directory = work/'evidence/xcrun-adapter'
    if directory.resolve(strict=True) != directory: raise ValueError('owned audit directory required')
    if sum(1 for _ in directory.iterdir()) >= 256: raise ValueError('bounded argv audit cap')
    body = (json.dumps({'schema': 2, 'outer_exec': REAL_ARCH,
                       'outer_argv': ['-arm64e', REAL_XCRUN, '--no-cache', *public],
                       'real_tool': REAL_XCRUN, 'tool_argv': ['--no-cache', *public],
                       'cwd': 'work/'+cwd.relative_to(work).as_posix(),
                       'environment_recorded': False, 'response_synthesized': False},
                      sort_keys=True)+'\n').encode()
    if len(body) > 16384: raise ValueError('bounded argv audit size')
    path = directory/(str(os.getpid())+'-'+secrets.token_hex(6)+'.json')
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'wb') as out:
        out.write(body); out.flush(); os.fsync(out.fileno())


def main():
    os.umask(0o077)
    path = Path(__file__).absolute()
    work = path.parents[1]
    if (path != work/'tool-adapters/xcrun' or path.resolve(strict=True) != path
            or work.name != 'luobo-apple-current359'
            or json.loads((work/'activation.json').read_bytes()).get('identity') != IDENTITY):
        raise ValueError('fixed CI-owned activation required')
    cwd = Path.cwd()
    cache_boundary(work)
    if os.environ.get('xcrun_db') != str(private_cache_path(work)):
        raise ValueError('original private tool cache environment required')
    argv, public = forwarding(work, sys.argv[1:], cwd)
    audit(work, public, cwd)
    # The only execution boundary. stdout/stderr/exit/signal behavior belongs to Apple.
    os.execv(REAL_ARCH, apple_execution(argv))


if __name__ == '__main__':
    try: main()
    except Exception:
        print('APPLE_XCRUN_ADAPTER_REFUSED', file=sys.stderr)
        raise SystemExit(1)
