"""One fixed Google Maven Linux tool, separate from App18/rich admission.

Native bytes are read only inside the owned cloud job. This module never
downloads, executes, installs or overrides an SDK/AGP executable.
"""
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import stat
import struct
import xml.etree.ElementTree as ET
import zipfile

import rich_current as historical

POLICY_SHA = '4e13f5fbe7351a8d2e511e152b65e0397346d145a3ac6c9612514c8c2362bd1f'
COORDINATE = 'com.android.tools.build:aapt2:9.1.1-14792394'
CONFIGURATION = 'luoboFixedAapt2Linux'
CACHE = 'gradle-cache/caches/modules-2/files-2.1/com.android.tools.build/aapt2/9.1.1-14792394'


def digest(body):
    return hashlib.sha256(body).hexdigest()


def load_policy(here):
    body = historical.read(Path(here) / 'aapt2-tool.json', 16384)
    if digest(body) != POLICY_SHA:
        raise ValueError('fixed AAPT2 tool policy bytes differ')
    value = json.loads(body)
    if (value['coordinate'] != COORDINATE or value['configuration'] != CONFIGURATION
            or value['classifier'] != 'linux' or value['extension'] != 'jar'
            or len(value['files']) != 2):
        raise ValueError('finite fixed Linux AAPT2 policy required')
    return value


def origin_rows(policy, proof):
    if proof.get('all_actual_artifact_sources_accounted') is not True:
        raise ValueError('complete original acquisition audit required')
    actual = [row for row in proof['artifacts'] if row['coordinate'] == COORDINATE]
    if len(actual) != 2:
        raise ValueError('exact AAPT2 POM and Linux JAR origins required')
    for expected in policy['files']:
        rows = [row for row in actual if row['filename'] == expected['filename']]
        if (len(rows) != 1 or any(rows[0][key] != expected[key] for key in ('bytes', 'sha256'))
                or rows[0]['observed_sources'] != [expected['url']]
                or 'published_file_alias' in rows[0]):
            raise ValueError('fixed AAPT2 origin or body differs')
    return actual


def _read(path, work, expected):
    path, work = Path(path), Path(work)
    if path.resolve(strict=True) != path or not path.is_relative_to(work):
        raise ValueError('canonical owned AAPT2 file required')
    fields = lambda s: (s.st_dev, s.st_ino, s.st_mode, s.st_uid, s.st_nlink,
                        s.st_size, s.st_mtime_ns, s.st_ctime_ns)
    with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW), 'rb') as stream:
        before = os.fstat(stream.fileno())
        if (not stat.S_ISREG(before.st_mode) or before.st_uid != os.getuid()
                or before.st_nlink != 1 or before.st_size != expected['bytes']):
            raise ValueError('fixed AAPT2 file ownership or size differs')
        body = stream.read(expected['bytes'] + 1)
        after = os.fstat(stream.fileno())
    if (len(body) != expected['bytes'] or digest(body) != expected['sha256']
            or fields(before) != fields(after) or fields(after) != fields(path.lstat())
            or path.resolve(strict=True) != path):
        raise ValueError('fixed AAPT2 file changed or body differs')
    return body


def archive(body):
    """Structure audit only. Caller must first enforce the official whole-JAR pin."""
    members = []
    with zipfile.ZipFile(io.BytesIO(body)) as source:
        names = set()
        total = 0
        for info in source.infolist():
            name = info.filename
            path = PurePosixPath(name)
            mode = info.external_attr >> 16
            if (len(names) >= 32 or name in names or path.is_absolute()
                    or '..' in path.parts or '\\' in name or not name
                    or info.flag_bits & 1 or stat.S_ISLNK(mode)):
                raise ValueError('bounded unique safe AAPT2 ZIP members required')
            names.add(name)
            total += info.file_size
            if total > 64 * 1024**2:
                raise ValueError('bounded AAPT2 ZIP expansion required')
            data = source.read(info)  # Every member CRC, including notices.
            members.append({'name': name, 'bytes': len(data), 'sha256': digest(data)})
        if 'aapt2' not in names:
            raise ValueError('fixed AAPT2 executable member missing')
        executable = source.read('aapt2')
    if len(executable) < 64 or executable[:7] != b'\x7fELF\x02\x01\x01':
        raise ValueError('Linux64 little-endian AAPT2 ELF required')
    kind, machine, version = struct.unpack_from('<HHI', executable, 16)
    phoff = struct.unpack_from('<Q', executable, 32)[0]
    ehsize, phsize, phnum = struct.unpack_from('<HHH', executable, 52)
    if (kind not in (2, 3) or machine != 62 or version != 1 or ehsize != 64
            or phsize != 56 or not 1 <= phnum <= 64
            or phoff + phsize * phnum > len(executable)):
        raise ValueError('bounded Linux x86_64 ELF program headers required')
    executable_load = False
    interpreter = None
    for index in range(phnum):
        row = struct.unpack_from('<IIQQQQQQ', executable, phoff + phsize * index)
        segment, flags, offset, _, _, filesz, memsz, _ = row
        if offset + filesz > len(executable) or (segment == 1 and filesz > memsz):
            raise ValueError('AAPT2 ELF segment exceeds member')
        if segment == 1 and flags & 1:
            executable_load = True
        if segment == 3:
            if interpreter is not None or executable[offset:offset + filesz] != b'/lib64/ld-linux-x86-64.so.2\0':
                raise ValueError('fixed Linux loader or static AAPT2 required')
            interpreter = '/lib64/ld-linux-x86-64.so.2'
    if not executable_load:
        raise ValueError('AAPT2 executable load segment required')
    return {'members': members, 'all_member_CRC_checked': True,
            'ELF': {'class': 64, 'machine': 62, 'type': kind, 'program_headers': phnum,
                    'interpreter': interpreter, 'bytes': len(executable),
                    'sha256': digest(executable)}, 'executed': False}


def _binding(work, here):
    work = Path(work)
    policy = load_policy(here)
    graph_path = work / 'evidence/fixed-aapt2-tool-graph.json'
    graph_raw = historical.read(graph_path, 16384)
    if len(graph_raw) > 16384:
        raise ValueError('bounded fixed AAPT2 tool graph required')
    graph = json.loads(graph_raw)
    if (set(graph) != {'schema', 'kind', 'owner', 'configuration', 'coordinate', 'classifier',
                      'no_force', 'no_attributes', 'no_transitive', 'artifact'}
            or graph['schema'] != 1 or graph['kind'] != 'fixed-aapt2-tool-graph-v1'
            or graph['owner'] != policy['owner'] or graph['configuration'] != CONFIGURATION
            or graph['coordinate'] != COORDINATE or graph['classifier'] != 'linux'
            or any(graph[key] is not True for key in ('no_force', 'no_attributes', 'no_transitive'))):
        raise ValueError('actual closed AAPT2 tool graph differs')
    origins_raw = historical.read(work / 'evidence/dependency-origin-audit.json', 64 * 1024**2)
    origins = json.loads(origins_raw)
    rows = origin_rows(policy, origins)
    artifact = graph['artifact']
    jar, pom = policy['files'][1], policy['files'][0]
    if (set(artifact) != {'path', 'bytes', 'sha256'}
            or artifact['bytes'] != jar['bytes'] or artifact['sha256'] != jar['sha256']):
        raise ValueError('actual fixed AAPT2 artifact differs')
    path = PurePosixPath(artifact['path'])
    if (len(path.parts) != 9 or path.parts[:7] != PurePosixPath(CACHE).parts
            or path.parts[-1] != jar['filename'] or path.is_absolute() or '..' in path.parts):
        raise ValueError('fixed AAPT2 Gradle cache member required')
    jar_body = _read(work / str(path), work, jar)
    pom_paths = list((work / CACHE).glob('*/' + pom['filename']))
    if len(pom_paths) != 1:
        raise ValueError('one actual fixed AAPT2 POM required')
    _read(pom_paths[0], work, pom)
    xml_raw = historical.read(work / 'evidence/gradle-verification-metadata.xml', 64 * 1024**2)
    if len(xml_raw) > 64 * 1024**2 or b'<!DOCTYPE' in xml_raw.upper() or b'<!ENTITY' in xml_raw.upper():
        raise ValueError('bounded actual verification XML required')
    ns = '{https://schema.gradle.org/dependency-verification}'
    xml = ET.fromstring(xml_raw)
    components = [node for node in xml.findall(ns + 'components/' + ns + 'component')
                  if node.attrib == {'group': 'com.android.tools.build', 'name': 'aapt2', 'version': '9.1.1-14792394'}]
    if len(components) != 1:
        raise ValueError('one actual AAPT2 verification component required')
    for expected in policy['files']:
        artifacts = [node for node in components[0] if node.attrib == {'name': expected['filename']}]
        if (len(artifacts) != 1 or [node.attrib.get('value') for node in artifacts[0].findall(ns + 'sha256')] != [expected['sha256']]):
            raise ValueError('actual AAPT2 JAR and POM verification SHA required')
    return {'schema': 1, 'kind': 'fixed-aapt2-linux-origin-binding-v1', 'policy_sha256': POLICY_SHA,
            'graph_sha256': digest(graph_raw), 'origin_sha256': digest(origins_raw),
            'verification_xml_sha256': digest(xml_raw), 'files': rows,
            'archive': archive(jar_body), 'stable_FD_body_checks': True,
            'acquisition_only_not_App18_admission': True, 'APK_or_device_accepted': False}


def bind(work, here, write_json):
    write_json(Path(work) / 'evidence/fixed-aapt2-tool-binding.json', _binding(work, here))


def require(work, here):
    value = json.loads(historical.read(Path(work) / 'evidence/fixed-aapt2-tool-binding.json', 16384))
    if value != _binding(work, here):
        raise ValueError('fixed AAPT2 origin/body/XML/tool graph changed')
