"""Owned App packaging adapters. Original R7 framework and source472 never change."""
import hashlib
import json
import os
from pathlib import Path
import plistlib
import stat
import zipfile


def sha(body):
    return hashlib.sha256(body).hexdigest()


def regular(path, cap=256*1024**2):
    p = Path(path)
    if p.resolve(strict=True) != p:
        raise ValueError('canonical regular input required')
    with os.fdopen(os.open(p, os.O_RDONLY | os.O_NOFOLLOW), 'rb') as f:
        before = os.fstat(f.fileno())
        if (not stat.S_ISREG(before.st_mode) or before.st_nlink != 1
                or before.st_uid != os.getuid() or not 0 <= before.st_size <= cap):
            raise ValueError('owned bounded single-link input required')
        body = f.read(cap+1)
        after = os.fstat(f.fileno())
    fields = lambda s: (s.st_dev, s.st_ino, s.st_size, s.st_mode, s.st_nlink,
                        s.st_uid, s.st_mtime_ns, s.st_ctime_ns)
    if fields(before) != fields(after) or fields(after) != fields(p.lstat()):
        raise ValueError('input changed during read')
    return body


def vector(root, owned_root=None):
    """Canonical files plus narrowly internal generated links, never outside aliases."""
    root = Path(root).resolve(strict=True)
    owned_root = root if owned_root is None else Path(owned_root).resolve(strict=True)
    if not root.is_relative_to(owned_root):
        raise ValueError('snapshot root outside owned tree')
    rows = []
    for p in sorted(root.rglob('*')):
        name = p.relative_to(root).as_posix()
        info = p.lstat()
        if stat.S_ISLNK(info.st_mode):
            target = os.readlink(p)
            resolved = p.resolve(strict=True)
            if not resolved.is_relative_to(owned_root):
                raise ValueError('generated link escaped its owned subtree')
            rows.append({'path': name, 'symlink': target})
        elif stat.S_ISREG(info.st_mode):
            body = regular(p)
            rows.append({'path': name, 'bytes': len(body), 'sha256': sha(body)})
        elif not stat.S_ISDIR(info.st_mode):
            raise ValueError('nonregular tree entry')
    if len(rows) > 100000:
        raise ValueError('source tree row budget')
    return rows


def derived_plist(body):
    original = plistlib.loads(body)
    if (original.get('MinimumOSVersion') != '100.0'
            or original.get('CFBundleExecutable') != 'OpenIMCore'):
        raise ValueError('original audited gomobile plist100 required')
    after = dict(original, MinimumOSVersion='15.0')
    result = plistlib.dumps(after, fmt=plistlib.FMT_XML, sort_keys=True)
    if {k: v for k, v in plistlib.loads(result).items() if k != 'MinimumOSVersion'} != {
            k: v for k, v in original.items() if k != 'MinimumOSVersion'}:
        raise ValueError('only static framework wrapper minimum may change')
    return result


def stage_core(original, destination, rows, put):
    """17 pinned originals ->17 separate files; exactly two plist adaptations."""
    original, destination = Path(original), Path(destination)
    if destination.exists() or len(rows) != 17 or len({r['path'] for r in rows}) != 17:
        raise ValueError('fresh complete core17 destination required')
    adapted = {
        'ios-arm64/OpenIMCore.framework/Info.plist',
        'ios-arm64-simulator/OpenIMCore.framework/Info.plist',
    }
    result = []
    for row in rows:
        name = row['path']
        if Path(name).is_absolute() or '..' in Path(name).parts:
            raise ValueError('core relative path')
        before = regular(original/name)
        if len(before) != row['bytes'] or sha(before) != row['sha256']:
            raise ValueError('original accepted core17 differs')
        after = derived_plist(before) if name in adapted else before
        put(destination/name, after)
        result.append({'path': name, 'before_sha256': sha(before), 'after_sha256': sha(after),
                       'wrapper_only_changed': name in adapted,
                       'static_binary_changed': False})
    if {r['path'] for r in result if r['wrapper_only_changed']} != adapted:
        raise ValueError('two explicit wrapper adaptations required')
    return {'original_core17_unchanged': True, 'derived_core17': result,
            'actual_static_platform_minimum_proof': 'R7 binary + R10 link; not plist100',
            'full_App_or_device_acceptance': False}


def seed_asset(download, row, source, put):
    """Seed exact new hook shared bytes, never old hook snapshots/stamps."""
    body = regular(download, 32*1024**2)
    if sha(body) != row['sha256']:
        raise ValueError('locked sqlite3 hook asset checksum differs')
    target = Path(source)/'app/.dart_tool/hooks_runner/shared/sqlite3/build'/(
        'download-'+row['sha256'][:8])/'libsqlite3.dylib'
    put(target, body)
    return {'filename': row['filename'], 'sha256': sha(body), 'bytes': len(body),
            'seed_relative': target.relative_to(source).as_posix(),
            'old_hook_snapshot_or_stamp_copied': False}


def require_approval(lane, catalogue):
    """No runtime/self-issued approval flag: reviewed source must pin this future result."""
    approved = lane.get('approved_native_resolution')
    if not isinstance(approved, dict):
        raise ValueError('Root-accepted native resolution source successor required before App build')
    if (approved.get('identity') != lane['identity'] or approved.get('source_ref') != lane['source_ref']
            or approved.get('catalogue_sha256') != sha(catalogue)
            or approved.get('acceptance_receipt_sha256') is None):
        raise ValueError('approved complete native/tool/source resolution differs')
    return json.loads(catalogue)


def native_catalogue(lane, resolution, snapshots, gem_sources):
    """Stable source identities, not per-job inode/time or arbitrary new cache outputs."""
    source_rows = []
    suffixes = ('.swift', '.m', '.mm', '.c', '.h', '.modulemap', '.podspec', '.podspec.json',
                '.xcconfig', '.pbxproj', '.xcscheme', '.plist', '.xcprivacy')
    for tree in snapshots:
        if tree['root'].endswith('/.dart_tool') or tree['root'] == 'cocoapods':
            continue
        for row in tree['files']:
            if any(p in {'xcuserdata', '.git'} for p in Path(row['path']).parts):
                continue
            if row['path'].endswith(suffixes) or Path(row['path']).name in {
                    'Package.resolved', 'Podfile', 'Podfile.lock', 'Manifest.lock'}:
                if 'sha256' in row: source_rows.append(dict(row, path=tree['root']+'/'+row['path']))
    return {'schema': 1, 'identity': lane['identity'], 'source_ref': resolution['source_ref'],
            'source_digest': resolution['source_digest'], 'native_locks': resolution['native_locks'],
            'host': [{k: r[k] for k in ['path', 'bytes', 'sha256']} for r in resolution['accepted_host']],
            'flutter_release': lane['flutter'], 'original_origins': resolution['origins'],
            'native_gems': gem_sources, 'generated_build_sources': sorted(source_rows, key=lambda r: r['path']),
            'ephemeral_state_is_not_source_authority': True,
            'approval_scope': 'sources/tools/locked native objects; not App binary or runtime acceptance'}


def webrtc_consumed(archive, framework):
    """Compare the actually consumed SPM framework to independently SHA-pinned ZIP bytes."""
    expected = {}
    with zipfile.ZipFile(archive) as z:
        if z.testzip() is not None:
            raise ValueError('WebRTC source CRC failure')
        for item in z.infolist():
            marker = 'WebRTC.xcframework/'
            if marker not in item.filename or item.is_dir():
                continue
            prefix, relative = item.filename.split(marker, 1)
            if prefix not in {'', 'WebRTC/'} or not relative or '..' in Path(relative).parts:
                raise ValueError('unknown WebRTC ZIP root')
            if relative.casefold() in {p.casefold() for p in expected}:
                raise ValueError('WebRTC ZIP alias')
            body = z.read(item)
            mode = item.external_attr >> 16
            if stat.S_ISLNK(mode):
                target = body.decode('utf-8')
                if Path(target).is_absolute(): raise ValueError('absolute framework source link')
                expected[relative] = {'path': relative, 'symlink': target}
            elif stat.S_IFMT(mode) in {0, stat.S_IFREG}:
                expected[relative] = {'path': relative, 'bytes': len(body), 'sha256': sha(body)}
            else:
                raise ValueError('WebRTC archive nonregular input')
    actual = vector(framework)
    if not expected or actual != [expected[p] for p in sorted(expected)]:
        raise ValueError('actual WebRTC SPM framework differs from declared ZIP source')
    return {'archive_sha256': hashlib.sha256(Path(archive).read_bytes()).hexdigest(),
            'actual_framework_members': len(actual), 'all_actual_members_match': True}


def app_commands(work, xcodebuild):
    """Actual unsigned current354 device/sim commands; current lane hard-held by approval."""
    work = Path(work)
    common = [xcodebuild, '-workspace', 'Runner.xcworkspace', '-scheme', 'Runner',
              '-configuration', 'Debug', '-clonedSourcePackagesDirPath',
              str(work/'source/app/build/ios/SourcePackages'),
              '-disableAutomaticPackageResolution', '-onlyUsePackageVersionsFromResolvedFile',
              'CODE_SIGNING_ALLOWED=NO', 'CODE_SIGNING_REQUIRED=NO', 'CODE_SIGN_IDENTITY=',
              'DEVELOPMENT_TEAM=', 'ARCHS=arm64', 'ONLY_ACTIVE_ARCH=YES',
              'IPHONEOS_DEPLOYMENT_TARGET=15.0', 'PRODUCT_BUNDLE_IDENTIFIER=com.luoboduihua.imapp',
              'COMPILER_INDEX_STORE_ENABLE=NO']
    return [(kind, common+['-sdk', sdk, '-destination', destination,
                         '-derivedDataPath', str(work/'build'/kind), 'build'])
            for kind, sdk, destination in [
                ('device', 'iphoneos', 'generic/platform=iOS'),
                ('simulator', 'iphonesimulator', 'generic/platform=iOS Simulator')]]
