"""CI-owned argv adapter. Every admitted invocation execs Apple's real xcrun."""
import hashlib
import json
import os
from pathlib import Path
import secrets
import stat
import sys


REAL_XCRUN = '/usr/bin/xcrun'
IDENTITY = 'current359-null2-apple-native-resolution-v2'
SCRIPT_CAP = 64 * 1024
VALUE_FLAGS = {'-project', '-workspace', '-scheme', '-configuration', '-target',
               '-sdk', '-destination', '-clonedSourcePackagesDirPath',
               '-derivedDataPath', '-packageCachePath'}
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
                        '-derivedDataPath', '-packageCachePath'}:
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
    if (not python.startswith('/') or len(python) > 120
            or any(c.isspace() for c in python) or '\x00' in python):
        raise ValueError('fixed absolute Python shebang required')
    if not source or len(source) > SCRIPT_CAP: raise ValueError('adapter source cap')
    return ('#!'+python+'\n').encode()+source


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
    for relative in ['cf-home', 'xcode/DerivedData', 'xcode/PackageCache', 'evidence/xcrun-adapter']:
        target = work/relative
        if target.resolve(strict=True) != target or not target.is_dir():
            raise ValueError('owned adapter support directory differs')
    return {'script_sha256': hashlib.sha256(expected).hexdigest(), 'script_bytes': len(expected),
            'source_sha256': hashlib.sha256(Path(source_path).read_bytes()).hexdigest(),
            'real_exec': REAL_XCRUN, 'no_cache': True, 'tool_response_synthesized': False}


def install(work, source_path, python):
    work = Path(work)
    for relative in ['tool-adapters', 'cf-home', 'xcode/DerivedData', 'xcode/PackageCache',
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
    body = (json.dumps({'schema': 1, 'real_exec': REAL_XCRUN, 'argv': ['--no-cache', *public],
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
    argv, public = forwarding(work, sys.argv[1:], cwd)
    audit(work, public, cwd)
    # The only execution boundary. stdout/stderr/exit/signal behavior belongs to Apple.
    os.execv(REAL_XCRUN, argv)


if __name__ == '__main__':
    try: main()
    except Exception:
        print('APPLE_XCRUN_ADAPTER_REFUSED', file=sys.stderr)
        raise SystemExit(1)
