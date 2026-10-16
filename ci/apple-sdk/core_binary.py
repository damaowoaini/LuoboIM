"""Audit actual archive/Mach-O bytes; textual tool outputs remain raw evidence."""
import hashlib
from pathlib import PurePosixPath
import re
import struct

ARM64, MH_OBJECT, MH_EXECUTE, MH_DYLIB = 0x100000C, 1, 2, 6
MAX_IOS = 15 << 16
PLATFORMS = {'device': 2, 'simulator': 7}
ANCHORS = {'_Open_im_sdkInitSDK', '_Open_im_sdkGetAtAllTag'}


def macho(body, kind, filetype, require_version=True):
    expected = PLATFORMS[kind]
    endian = {b'\xcf\xfa\xed\xfe': '<', b'\xfe\xed\xfa\xcf': '>'}.get(body[:4])
    if endian is None or len(body) < 32:
        raise ValueError('real 64bit Mach-O required')
    _, cpu, _, actual_type, count, size, _, _ = struct.unpack_from(endian+'8I', body)
    if cpu != ARM64 or actual_type != filetype or count > 4096 or 32+size > len(body):
        raise ValueError('ARM64/filetype/load command boundary')
    offset, versions, legacy = 32, [], []
    for _ in range(count):
        if offset+8 > 32+size:
            raise ValueError('truncated Mach-O load command')
        cmd, length = struct.unpack_from(endian+'2I', body, offset)
        if length < 8 or offset+length > 32+size:
            raise ValueError('invalid Mach-O load command size')
        if cmd == 0x32:  # LC_BUILD_VERSION: authoritative device/simulator platform.
            if length < 24:
                raise ValueError('short LC_BUILD_VERSION')
            platform, minimum, sdk, tools = struct.unpack_from(endian+'4I', body, offset+8)
            if length != 24+8*tools or platform != expected or not 0 < minimum <= MAX_IOS:
                raise ValueError('core/image wrong platform or minimum above15')
            versions.append({'platform': platform, 'minimum': minimum, 'sdk': sdk})
        elif cmd in {0x24, 0x25, 0x2F, 0x30}:
            if length != 16:
                raise ValueError('short legacy minimum version')
            minimum, sdk = struct.unpack_from(endian+'2I', body, offset+8)
            if cmd != 0x25 or kind != 'device' or not 0 < minimum <= MAX_IOS:
                raise ValueError('wrong or ambiguous legacy platform/minimum')
            legacy.append({'minimum': minimum, 'sdk': sdk})
        offset += length
    if offset != 32+size or len(versions) > 1 or len(legacy) > 1:
        raise ValueError('unaccounted/duplicate Mach-O version commands')
    if require_version and len(versions) != 1:
        raise ValueError('explicit actual LC_BUILD_VERSION required')
    return {'filetype': actual_type, 'build_versions': versions, 'legacy_ios_versions': legacy,
            'classification': 'platform_and_minimum_verified' if versions else
                              ('legacy_ios_device_minimum_only' if legacy else 'unversioned_object_platform_not_proven')}


def thin_archive(body):
    if body[:8] == b'!<arch>\n':
        return body
    magic = body[:4]
    if magic not in {b'\xca\xfe\xba\xbe', b'\xca\xfe\xba\xbf'} or len(body) < 8:
        raise ValueError('static ar archive required, no dylib substitute')
    count = struct.unpack_from('>I', body, 4)[0]
    wide = magic == b'\xca\xfe\xba\xbf'
    if count != 1 or len(body) < (40 if wide else 28):
        raise ValueError('only one ARM64 fat archive slice allowed')
    if wide:
        cpu, _, start, size, _, _ = struct.unpack_from('>IIQQII', body, 8)
    else:
        cpu, _, start, size, _ = struct.unpack_from('>5I', body, 8)
    if cpu != ARM64 or start < (40 if wide else 28) or start+size != len(body):
        raise ValueError('fat archive boundary/architecture')
    result = body[start:start+size]
    if result[:8] != b'!<arch>\n':
        raise ValueError('ARM64 slice must itself be a static archive')
    return result


def members(body):
    body = thin_archive(body)
    offset, result = 8, []
    while offset < len(body):
        if len(result) >= 16384 or offset+60 > len(body):
            raise ValueError('archive member cap/header boundary')
        header = body[offset:offset+60]
        if header[58:60] != b'`\n' or not header[48:58].strip().isdigit():
            raise ValueError('archive member header')
        length = int(header[48:58]); start = offset+60; end = start+length
        if end > len(body):
            raise ValueError('archive member size')
        name = header[:16].decode('ascii').strip()
        data = body[start:end]
        if name.startswith('#1/'):
            count = int(name[3:])
            if count <= 0 or count > min(length, 1024):
                raise ValueError('BSD archive extended name bound')
            name = data[:count].rstrip(b'\0').decode('utf-8'); data = data[count:]
        else:
            name = name.rstrip('/')
        if not name or '/' in name or '\\' in name or '\0' in name:
            raise ValueError('unknown archive name/table encoding')
        if end % 2 and (end >= len(body) or body[end:end+1] != b'\n'):
            raise ValueError('archive padding')
        result.append((name, data)); offset = end+(end % 2)
    if not result:
        raise ValueError('empty core archive')
    return result


def audit_core(body, kind, ar_listing, file_description, nm_output):
    if 'archive' not in file_description.lower():
        raise ValueError('file tool did not identify an archive')
    rows, symbols, positive = [], {}, 0
    for line in nm_output.splitlines():
        fields = line.split()
        if len(fields) >= 2 and fields[-2] == 'T':
            symbols[fields[-1]] = True
    if not ANCHORS.issubset(symbols):
        raise ValueError('real core defined text symbols required')
    names = []
    for name, content in members(body):
        if name in {'__.SYMDEF', '__.SYMDEF SORTED', '__.SYMDEF_64', '__.SYMDEF_64 SORTED'}:
            continue
        names.append(name)
        version = macho(content, kind, MH_OBJECT, require_version=False)
        positive += bool(version['build_versions'])
        rows.append({'name': name, 'bytes': len(content), 'sha256': hashlib.sha256(content).hexdigest(), **version})
    table_names = {'__.SYMDEF', '__.SYMDEF SORTED', '__.SYMDEF_64', '__.SYMDEF_64 SORTED'}
    if names != [x.strip() for x in ar_listing.splitlines() if x.strip() and x.strip() not in table_names]:
        raise ValueError('ar tool listing differs from actual member bytes')
    if positive < 1:
        raise ValueError('at least one actual core object platform/minimum proof required')
    return {'static_ar_archive': True, 'versioned_objects': positive, 'members': rows,
            'unversioned_objects': sum(r['classification'] == 'unversioned_object_platform_not_proven' for r in rows),
            'all_members_platform_proven': all(r['classification'] == 'platform_and_minimum_verified' for r in rows),
            'defined_binding_text_anchors': sorted(ANCHORS)}


def plugin_dependencies(text, own_path, flutter_path):
    lines = text.splitlines()
    if not lines or not lines[0].endswith(':'):
        raise ValueError('otool dependency image header required')
    paths = []
    for line in lines[1:]:
        match = re.fullmatch(r'\s*(.+?)\s+\(compatibility version [^)]+\)\s*', line)
        if match is None:
            raise ValueError('unparsed dependency evidence')
        path = match[1]
        if '..' in PurePosixPath(path).parts or '\0' in path:
            raise ValueError('noncanonical dynamic dependency path')
        if 'openimcore' in path.lower():
            raise ValueError('plugin dynamically depends on OpenIMCore')
        if not (path == str(own_path) or path == '@rpath/libflutter_openim_sdk.dylib'
                or path == '@rpath/Flutter.framework/Flutter' or path == str(flutter_path)
                or re.fullmatch(r'@rpath/libswift[A-Za-z0-9_]+\.dylib', path)
                or path.startswith(('/usr/lib/', '/System/Library/Frameworks/'))):
            raise ValueError('old/unapproved dynamic dependency path')
        paths.append(path)
    if not paths:
        raise ValueError('missing actual dylib dependency records')
    return paths
