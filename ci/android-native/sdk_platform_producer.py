"""Independent official SDK archive producer; no I/O or network on import."""
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import xml.etree.ElementTree as ET
import zipfile

import run as parent
import support as s

ROOT = 'sdk-platform-source-proof'
HERE = Path(__file__).resolve().parent
PACKAGES = ('platforms;android-37.0', 'platforms;android-36', 'platforms;android-35')
ARCHIVE_BYTES = 197434099
TAMPER_LABEL = 'selector-control-sdk-samepath-tamper'
SYSTEM_TAMPER_LABEL = 'selector-control-sdk-system-samepath-tamper'
KEYS = ('st_dev', 'st_ino', 'st_mode', 'st_uid', 'st_gid', 'st_nlink',
        'st_size', 'st_mtime_ns', 'st_ctime_ns')


def identity(value):
    return {k: getattr(value, k) for k in KEYS}


def open_owned(work, path):
    work, path = Path(work), Path(path)
    if path.resolve(strict=True) != path or not path.is_relative_to(work):
        raise ValueError('canonical job-owned SDK source required')
    stream = os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW), 'rb')
    st = os.fstat(stream.fileno())
    if (not stat.S_ISREG(st.st_mode) or st.st_uid != os.getuid()
            or st.st_nlink != 1 or not 0 < st.st_size <= 512 * 1024 ** 2):
        stream.close()
        raise ValueError('bounded owned regular single-link SDK source required')
    return stream, st


def file_row(work, path):
    stream, before = open_owned(work, path)
    with stream:
        digest = hashlib.sha256()
        for block in iter(lambda: stream.read(65536), b''):
            digest.update(block)
        if identity(before) != identity(os.fstat(stream.fileno())):
            raise ValueError('SDK source changed during read')
    if identity(before) != identity(Path(path).lstat()):
        raise ValueError('SDK source path changed during read')
    return {'path': str(Path(path).relative_to(work)), 'bytes': before.st_size,
            'sha256': digest.hexdigest()}


def read_body(work, path, limit=2 * 1024 ** 2):
    stream, before = open_owned(work, path)
    with stream:
        if before.st_size > limit:
            raise ValueError('bounded SDK evidence required')
        body = stream.read(limit + 1)
        if (len(body) != before.st_size or identity(before) != identity(os.fstat(stream.fileno()))
                or identity(before) != identity(Path(path).lstat())):
            raise ValueError('SDK evidence changed during read')
    return body


def read_json(work, path):
    return json.loads(read_body(work, path))


def contract():
    value = json.loads((HERE / 'sdk-platform-sources.json').read_text())
    if (value['schema'] != 2 or tuple(x['official']['package'] for x in value['platforms']) != PACKAGES
            or sum(x['official']['size'] for x in value['platforms']) != ARCHIVE_BYTES):
        raise ValueError('exact three reviewed SDK platform contracts required')
    inventories = [row['jar_inventory'] for row in value['platforms']]
    if ([len(x) for x in inventories] != [10, 10, 9]
            or sum(item['role'] == 'classpath' for x in inventories for item in x) != 26
            or sum(item['role'] == 'source-only' for x in inventories for item in x) != 3):
        raise ValueError('exact finite29 JAR inventory/classification required')
    for row in value['platforms']:
        inventory = row['jar_inventory']
        prefix = row['official']['package'].split(';')[1] + '/'
        if (not re.fullmatch('[0-9a-f]{64}', row['archive_sha256'])
                or len({x['member'].casefold() for x in inventory}) != len(inventory)):
            raise ValueError('reviewed archive SHA and unique finite members required')
        for item in inventory:
            s.name(item['member'])
            if (not item['member'].startswith(prefix) or not item['member'].endswith('.jar')
                    or item['path'] != 'platforms/' + item['member']
                    or item['role'] not in ('classpath', 'source-only')
                    or (item['role'] == 'source-only') != (item['member'] == prefix + 'android-stubs-src.jar')):
                raise ValueError('finite member path/role differs')
    return value['platforms']


def package_revision(body, expected):
    if b'<!DOCTYPE' in body or b'<!ENTITY' in body:
        raise ValueError('package XML entities refused')
    root = ET.fromstring(body)
    local = [x for x in root.iter() if x.tag.split('}')[-1] == 'localPackage']
    if len(local) != 1 or local[0].attrib.get('path') != expected['package']:
        raise ValueError('installed package XML identity differs')
    revision = [x for x in local[0] if x.tag.split('}')[-1] == 'revision']
    if len(revision) != 1:
        raise ValueError('one installed package revision required')
    fields = {x.tag.split('}')[-1]: x.text for x in revision[0]}
    if (len(fields) != len(revision[0]) or set(fields) - {'major', 'minor', 'micro'}
            or fields.get('major') != expected['revision']
            or fields.get('minor', '0') != '0' or fields.get('micro', '0') != '0'):
        raise ValueError('installed package XML revision differs')


def safe_zip(bundle):
    rows = bundle.infolist()
    if not rows or len(rows) > 20000 or sum(x.file_size for x in rows) > 2 * s.GIB:
        raise ValueError('bounded platform ZIP required')
    seen = set()
    for item in rows:
        name = item.filename[:-1] if item.is_dir() else item.filename
        s.name(name)
        mode = item.external_attr >> 16
        if (item.filename in seen or item.flag_bits & 1
                or stat.S_IFMT(mode) not in (0, stat.S_IFREG, stat.S_IFDIR)
                or (stat.S_ISDIR(mode) and not item.is_dir())):
            raise ValueError('unsafe duplicate/encrypted/nonregular SDK ZIP entry')
        seen.add(item.filename)
    if bundle.testzip() is not None:
        raise ValueError('platform ZIP CRC failed')


def compare_member(work, bundle, member, pin, budget):
    info = bundle.getinfo(member)
    installed = Path(work) / 'android' / s.name(pin['path'])
    stream, before = open_owned(work, installed)
    if info.is_dir() or info.file_size != pin['bytes'] or before.st_size != pin['bytes']:
        stream.close()
        raise ValueError('SDK member/installed size differs from original static pin')
    digest = hashlib.sha256()
    with stream, bundle.open(info) as source:
        while True:
            block = source.read(65536)
            if stream.read(len(block) if block else 1) != block:
                raise ValueError('official ZIP member differs from installed SDK bytes')
            if not block:
                break
            digest.update(block)
            budget.check()
        if identity(before) != identity(os.fstat(stream.fileno())):
            raise ValueError('installed SDK identity changed during comparison')
    if identity(before) != identity(installed.lstat()) or digest.hexdigest() != pin['sha256']:
        raise ValueError('installed SDK differs from original frozen SHA/identity')
    return {'path': str(installed.relative_to(work)), 'bytes': pin['bytes'], 'sha256': pin['sha256'],
            'ZIP_member': member, 'installed_stat': identity(before), 'byte_comparison': True}


def jar_inventory(bundle, expected):
    """Enumerate the official archive, never the installed SDK directory."""
    actual = [x for x in bundle.infolist() if x.filename.lower().endswith('.jar')]
    names = [x.filename for x in actual]
    wanted = [x['member'] for x in expected['jar_inventory']]
    if (len(names) > 16 or len(names) != len(wanted) or set(names) != set(wanted)
            or len({x.casefold() for x in names}) != len(names)):
        raise ValueError('official ZIP finite JAR inventory missing/extra/case collision')
    for info in actual:
        s.name(info.filename)
        mode = info.external_attr >> 16
        if (info.is_dir() or info.flag_bits & 1 or stat.S_IFMT(mode) not in (0, stat.S_IFREG)
                or not 0 < info.file_size <= 128 * 1024 ** 2):
            raise ValueError('bounded regular official JAR member required')
    return sorted(names)


def archive_pin(work, archive, expected, acquired):
    row = file_row(work, archive)
    official = expected['official']
    if (acquired['source_url'] != official['url'] or acquired['official_sha1_checked'] is not True
            or acquired['sha256'] != expected['archive_sha256']
            or row['sha256'] != expected['archive_sha256']
            or row['bytes'] != acquired['bytes'] or row['bytes'] != official['size']):
        raise ValueError('official archive differs from reviewed prior-acquisition full lock')
    return row


def member_pin(bundle, member, installed_path, budget):
    """Hash source bytes first; installed content cannot self-authorize a pin."""
    info = bundle.getinfo(member)
    if info.is_dir() or not 0 < info.file_size <= 128 * 1024 ** 2:
        raise ValueError('bounded selected official member required')
    digest, count = hashlib.sha256(), 0
    with bundle.open(info) as stream:
        for block in iter(lambda: stream.read(65536), b''):
            count += len(block)
            if count > info.file_size:
                raise ValueError('official member exceeds declared length')
            digest.update(block)
            budget.check()
    if count != info.file_size:
        raise ValueError('official member length differs')
    return {'path': installed_path, 'bytes': count, 'sha256': digest.hexdigest()}


def optional_metadata(body, inventory):
    if len(body) > 1024 ** 2:
        raise ValueError('bounded optional library metadata required')
    def unique_pairs(items):
        value = {}
        for key, item in items:
            if key in value:
                raise ValueError('duplicate optional metadata key')
            value[key] = item
        return value
    rows = json.loads(body, object_pairs_hook=unique_pairs)
    expected = {Path(x['member']).name for x in inventory
                if '/optional/' in x['member'] and x['role'] == 'classpath'}
    if not isinstance(rows, list) or len(rows) != len(expected):
        raise ValueError('optional metadata must cover exact finite optional JARs')
    names, jars = set(), set()
    for row in rows:
        if (not isinstance(row, dict) or set(row) != {'name', 'jar', 'manifest'}
                or not isinstance(row['name'], str) or not re.fullmatch(r'[A-Za-z0-9_.-]{1,128}', row['name'])
                or not isinstance(row['jar'], str) or row['jar'] not in expected
                or row['jar'] != Path(row['jar']).name or type(row['manifest']) is not bool
                or row['name'] in names or row['jar'] in jars):
            raise ValueError('optional metadata unknown/unsafe/duplicate declaration')
        names.add(row['name'])
        jars.add(row['jar'])
    if jars != expected:
        raise ValueError('optional metadata differs from finite official members')
    return rows


def validate_jar_claim(expected, source):
    wanted = {x['member']: x for x in expected['jar_inventory']}
    rows = source['JAR_inventory']
    if len(rows) != len(wanted) or len({x['ZIP_member'] for x in rows}) != len(rows):
        raise ValueError('complete finite official JAR claim required')
    for row in rows:
        pin = wanted.get(row['ZIP_member'])
        if (pin is None or row['path'] != 'android/' + pin['path'] or row['role'] != pin['role']
                or row['pin_authority'] != 'locked-official-archive-member'
                or row['byte_comparison'] is not True or not re.fullmatch('[0-9a-f]{64}', row['sha256'])
                or type(row['bytes']) is not int or not 0 < row['bytes'] <= 128 * 1024 ** 2):
            raise ValueError('JAR claim cannot expand member/path/role/source authority')
    if (source['classpath_JARs'] != [x for x in rows if x['role'] == 'classpath']
            or source['source_only_JARs'] != [x for x in rows if x['role'] == 'source-only']):
        raise ValueError('classpath/source-only projection differs from finite producer')


def require_initial_boundary(job, args, cwd, env, timeout, reject, stdin):
    original = ['git', 'init', '--quiet', job.w / 'flutter']
    if (list(map(str, args)) != list(map(str, original)) or cwd != job.w
            or env != parent.environment(job.w, False) or timeout != 900
            or reject is not None or stdin is not None):
        raise ValueError('SDK producer requires exact inherited online flutter-init boundary')
    # parent.git has already mkdir'd this empty destination, before calling cmd.
    flutter = job.w / 'flutter'
    if (flutter.resolve(strict=True) != flutter or flutter.is_symlink() or not flutter.is_dir()
            or flutter.stat().st_uid != os.getuid() or any(flutter.iterdir())):
        raise ValueError('original git helper requires fresh empty owned flutter directory')
    if any((job.w / p).exists() for p in ('gradle-project-cache', 'selector-project-cache',
                                         'evidence/classpath-preseal', ROOT)):
        raise ValueError('SDK source producer is fresh and precedes all Gradle models')


def produce(job, args, cwd, env, timeout, reject, stdin):
    require_initial_boundary(job, args, cwd, env, timeout, reject, stdin)
    w, rows = job.w, contract()
    receipt = read_json(w, w / 'evidence/sdk-packages.json')
    resource = read_json(w, w / 'evidence/sdk-packages.resource.json')
    packages = job.tools['android_sdk']['required_packages'] + job.tools['cloud_dependency_additions']['required_SDK_packages']
    sdk_args = [w / 'android/cmdline-tools/22.0/bin/sdkmanager', '--verbose', '--sdk_root=' + str(w / 'android')]
    sdk_args += [x['path'] for x in packages]
    if (resource['exit_code'] != 0 or resource['expectation_met'] is not True or resource['error'] is not None
            or resource['latched_failure'] is not None or resource['argv'] != list(map(str, sdk_args))
            or resource['log_sha256'] != file_row(w, w / 'evidence/sdk-packages.log')['sha256']):
        raise ValueError('original SDK installation was not confirmed successful')
    commandline = read_json(w, w / 'evidence/download-commandline.json')
    if (commandline['sha256'] != job.tools['android_commandline_tools']['sha256']
            or commandline['source_url'] != job.tools['android_commandline_tools']['url']
            or commandline['extracted_root'] != 'android/cmdline-tools/22.0'):
        raise ValueError('pinned sdkmanager producer archive differs')
    xml = read_body(w, w / 'evidence/official-SDK-repository2-3.xml', 16 * 1024 ** 2)
    metadata = receipt['official_repository_metadata']
    if (metadata['source_url'] != 'https://dl.google.com/android/repository/repository2-3.xml'
            or metadata['bytes'] != len(xml) or metadata['sha256'] != hashlib.sha256(xml).hexdigest()):
        raise ValueError('same-job official repository XML bytes differ')
    selected = parent.sdk_catalog(xml, [{'path': x['official']['package'], 'revision': x['official']['revision']} for x in rows])
    if selected != [x['official'] for x in rows]:
        raise ValueError('reviewed official SDK package/URL/version/size/SHA1 differs')
    installed = {x['package']: x for x in receipt['packages']}
    if any(x not in receipt['selected_official_Linux_archives'] for x in selected):
        raise ValueError('SDK install receipt lacks reviewed archive selection')
    pins = job.tools['android_sdk']['same_crossplatform_API_inputs'] + job.tools['cloud_dependency_additions']['crossplatform_API_inputs']
    if any(pin not in pins for row in rows for pin in (row['jar'], row['properties'])):
        raise ValueError('SDK source authority must match original toolchain/additions pins')
    proof = w / ROOT
    s.write_json(proof / 'intent.json', {'state': 'before_official_platform_downloads_and_first_model',
        'contract_sha256': s.sha(HERE / 'sdk-platform-sources.json'),
        'original_SDK_receipt': file_row(w, w / 'evidence/sdk-packages.json'),
        'metadata': metadata, 'selected': selected, 'archive_total_bytes': ARCHIVE_BYTES})
    s.put(w / 'evidence/sdk-platform-source-intent.json', (proof / 'intent.json').read_bytes())
    results = []
    for row in rows:
        official = row['official']
        tag = official['package'].split(';')[1]
        archive = proof / 'archives' / (tag + '.zip')
        s.mkdir(archive.parent)
        acquired = s.download({'url': official['url'], 'bytes': official['size'],
            'max_bytes': official['size'], 'official_sha1': official['checksum'],
            'sha256': row['archive_sha256']}, archive, job.b)
        s.sync(archive.parent)
        s.write_json(proof / (tag + '-download.json'), acquired)
        s.write_json(w / 'evidence' / ('SDK-source-' + tag + '-download.json'), acquired)
        if acquired['official_sha1_checked'] is not True:
            raise ValueError('published official platform SHA1 not checked')
        archive_row = archive_pin(w, archive, row, acquired)
        with zipfile.ZipFile(archive) as bundle:
            safe_zip(bundle)
            observed_JAR_names = jar_inventory(bundle, row)
            jar = compare_member(w, bundle, row['jar_member'], row['jar'], job.b)
            prop = compare_member(w, bundle, row['properties_member'], row['properties'], job.b)
            all_jars = []
            for item in row['jar_inventory']:
                pin = member_pin(bundle, item['member'], item['path'], job.b)
                if item['member'] == row['jar_member'] and pin != row['jar']:
                    raise ValueError('archive-derived API JAR differs from original static pin')
                compared = compare_member(w, bundle, item['member'], pin, job.b)
                all_jars.append({**compared, 'role': item['role'], 'purpose': item['purpose'],
                    'pin_authority': 'locked-official-archive-member'})
            optional = row['optional_manifest']
            optional_pin = member_pin(bundle, optional['member'], optional['path'], job.b)
            if optional_pin['bytes'] > 1024 ** 2:
                raise ValueError('bounded official optional metadata required')
            optional_row = compare_member(w, bundle, optional['member'], optional_pin, job.b)
            optional_body = read_body(w, w / optional_row['path'], 1024 ** 2)
            optional_libraries = optional_metadata(optional_body, row['jar_inventory'])
        if file_row(w, archive) != archive_row:
            raise ValueError('SDK archive changed during full CRC/member proof')
        prop_body = read_body(w, w / prop['path'], 64 * 1024)
        if not re.search(rb'^Pkg.Revision\s*=\s*' + official['revision'].encode() + rb'\s*$', prop_body, re.M):
            raise ValueError('installed source.properties revision differs')
        package = w / 'android' / row['properties']['path'].replace('source.properties', 'package.xml')
        package_body = read_body(w, package, 1024 ** 2)
        package_revision(package_body, official)
        if (hashlib.sha256(package_body).hexdigest() != installed[official['package']]['installed_package_xml_sha256']
                or prop['sha256'] != installed[official['package']]['source_properties_sha256']):
            raise ValueError('same-job installed raw package/properties evidence differs')
        raw = w / 'evidence/sdk-platform-raw'
        s.put(raw / (tag + '-package.xml'), package_body)
        s.put(raw / (tag + '-source.properties'), prop_body)
        s.put(raw / (tag + '-optional.json'), optional_body)
        source = {'kind': 'official-SDK-platform-ZIP-member', 'package': official['package'],
            'revision': official['revision'], 'official': official, 'archive': archive_row,
            'official_sha1_checked': True, 'full_ZIP_CRC_and_safety_checked': True,
            'selected_members_only': [*all_jars, prop, optional_row],
            'jar': jar, 'properties': prop, 'JAR_inventory': all_jars,
            'complete_official_JAR_names': observed_JAR_names,
            'classpath_JARs': [x for x in all_jars if x['role'] == 'classpath'],
            'source_only_JARs': [x for x in all_jars if x['role'] == 'source-only'],
            'optional_manifest': optional_row, 'optional_libraries': optional_libraries,
            'raw_optional_manifest': file_row(w, raw / (tag + '-optional.json')),
            'package_xml': file_row(w, raw / (tag + '-package.xml')),
            'raw_properties': file_row(w, raw / (tag + '-source.properties'))}
        s.write_json(proof / (tag + '-proof.json'), source)
        s.write_json(w / 'evidence' / ('SDK-source-' + tag + '-proof.json'), source)
        results.append(source)
    if (sum(len(x['JAR_inventory']) for x in results) != 29
            or sum(len(x['classpath_JARs']) for x in results) != 26
            or sum(x['bytes'] for p in results for x in p['classpath_JARs']) > 256 * 1024 ** 2):
        raise ValueError('bounded complete26 classpath/29 installed JAR closure required')
    result = {'schema': 2, 'state': 'passed', 'before_first_model': True, 'platforms': results,
        'contract_sha256': s.sha(HERE / 'sdk-platform-sources.json'),
        'inventory_JAR_count': 29, 'classpath_JAR_count': 26, 'source_only_JAR_count': 3,
        'archive_total_bytes': ARCHIVE_BYTES, 'archive_SHA256_preexisting_lock': True,
        'archive_SHA256_provenance': 'R9 cloud acquisition receipt, not local ZIP rehash; independently rechecked this job',
        'authority': 'published official metadata SHA1/size + prior archive SHA256 + complete finite ZIP members/installed bytes; original static API pins retained'}
    s.write_json(proof / 'complete.json', result)
    # Compact artifact contains receipts/raw XML/properties, never the 197MB archives.
    s.write_json(w / 'evidence/sdk-platform-source-complete.json', result)
    job.b.sample()
    return result


def archive_upload_admission(work):
    """Bound the separate upload; never download, extract, or run a tool here."""
    w = Path(work)
    if w.resolve(strict=True) != w or w.is_symlink() or w.stat().st_uid != os.getuid():
        raise ValueError('canonical owned cloud work required')
    budget = s.Budget(w, parent.REPO)
    failure = None
    budget.start()
    try:
        if budget.minimum < s.INITIAL:
            raise ValueError('source archive upload requires phase4GiB admission')
        rows = []
        for row in contract():
            tag = row['official']['package'].split(';')[1]
            path = w / ROOT / 'archives' / (tag + '.zip')
            if not path.exists() and not path.is_symlink():
                continue
            item = file_row(w, path)
            if item['bytes'] > row['official']['size']:
                raise ValueError('source archive exceeds exact official byte bound')
            item['complete_download_receipt_present'] = (w / ROOT / (tag + '-download.json')).is_file()
            if item['complete_download_receipt_present']:
                receipt = read_json(w, w / ROOT / (tag + '-download.json'))
                if (receipt['bytes'] != item['bytes'] or receipt['sha256'] != item['sha256']
                        or receipt['sha256'] != row['archive_sha256']
                        or receipt['official_sha1_checked'] is not True):
                    raise ValueError('source upload differs from acquired archive')
            rows.append(item)
        total = sum(x['bytes'] for x in rows)
        temporary = total + 8 * 1024 ** 2  # uncompressed artifact framing/service overhead estimate
        if total > ARCHIVE_BYTES or budget.minimum - temporary < s.FLOOR or budget.peak + temporary > s.CAP:
            raise ValueError('source archive upload worstcase reserve/cap refused')
        s.write_json(w / 'evidence/sdk-platform-archive-upload-admission.json', {
            'state': 'admitted_for_separate_upload_not_native_acceptance', 'archives': rows,
            'bytes': total, 'maximum_source_bytes': ARCHIVE_BYTES, 'compression_level': 0,
            'temporary_copy_worstcase_bytes': temporary,
            'boundary': 'action upload itself is external to Python poll; no continuous action-resource claim',
            **budget.record()})
        budget.finish()
    except BaseException as error:
        failure = type(error).__name__
        raise
    finally:
        budget.stop.set()
        if budget.thread is not None:
            budget.thread.join()
        if failure or budget.reason:
            s.write_json(w / 'evidence/sdk-platform-archive-upload-failure.json', {
                'state': 'failed', 'error_class': failure, **budget.record()})


def verified_sources(work):
    w = Path(work)
    result = read_json(w, w / ROOT / 'complete.json')
    if (result['schema'] != 2 or result['state'] != 'passed' or result['before_first_model'] is not True
            or result['contract_sha256'] != s.sha(HERE / 'sdk-platform-sources.json')
            or result['archive_total_bytes'] != ARCHIVE_BYTES or len(result['platforms']) != 3
            or result['inventory_JAR_count'] != 29 or result['classpath_JAR_count'] != 26
            or result['source_only_JAR_count'] != 3):
        raise ValueError('complete independent SDK source producer required')
    for expected, source in zip(contract(), result['platforms']):
        if (source['official'] != expected['official'] or source['kind'] != 'official-SDK-platform-ZIP-member'
                or source['official_sha1_checked'] is not True or source['full_ZIP_CRC_and_safety_checked'] is not True
                or source['jar']['ZIP_member'] != expected['jar_member']
                or source['properties']['ZIP_member'] != expected['properties_member']):
            raise ValueError('SDK producer scope differs')
        for key in ('jar', 'properties'):
            row = source[key]
            pin = expected[key]
            if {k: row[k] for k in ('path', 'bytes', 'sha256')} != {
                    'path': 'android/' + pin['path'], 'bytes': pin['bytes'], 'sha256': pin['sha256']}:
                raise ValueError('SDK producer cannot extend original static member authority')
            if file_row(w, w / s.name(row['path'])) != {k: row[k] for k in ('path', 'bytes', 'sha256')}:
                raise ValueError('independently proven SDK input changed')
        archive = source['archive']
        if (archive['path'] != ROOT + '/archives/' + expected['official']['package'].split(';')[1] + '.zip'
                or archive['bytes'] != expected['official']['size'] or archive['sha256'] != expected['archive_sha256']):
            raise ValueError('only exact acquired platform archive may authorize members')
        if file_row(w, w / s.name(source['archive']['path'])) != source['archive']:
            raise ValueError('retained official SDK archive changed')
        validate_jar_claim(expected, source)
        if source['complete_official_JAR_names'] != sorted(x['member'] for x in expected['jar_inventory']):
            raise ValueError('complete official archive JAR enumeration differs')
        for item in source['JAR_inventory']:
            if file_row(w, w / s.name(item['path'])) != {k: item[k] for k in ('path', 'bytes', 'sha256')}:
                raise ValueError('finite installed JAR body/source changed')
            current = (w / item['path']).lstat()
            if any(getattr(current, key) != item['installed_stat'][key]
                   for key in ('st_dev', 'st_ino', 'st_mode', 'st_uid', 'st_gid', 'st_nlink', 'st_mtime_ns')):
                raise ValueError('original installed JAR inode/metadata changed')
        for key in ('optional_manifest', 'raw_optional_manifest', 'package_xml', 'raw_properties'):
            item = source[key]
            if file_row(w, w / s.name(item['path'])) != {k: item[k] for k in ('path', 'bytes', 'sha256')}:
                raise ValueError('source metadata bytes changed')
        meta = source['optional_manifest']
        if (meta['path'] != 'android/' + expected['optional_manifest']['path']
                or meta['ZIP_member'] != expected['optional_manifest']['member']
                or source['raw_optional_manifest']['sha256'] != meta['sha256']
                or optional_metadata(read_body(w, w / meta['path'], 1024 ** 2), expected['jar_inventory']) != source['optional_libraries']):
            raise ValueError('optional library metadata/source projection differs')
    if sum(x['bytes'] for p in result['platforms'] for x in p['classpath_JARs']) > 256 * 1024 ** 2:
        raise ValueError('bounded complete platform classpath required')
    return result


@contextmanager
def tamper_for_control(work, synthetic):
    """Only fresh job SDK37; retain original inode, restore before next command."""
    w = Path(work)
    source = verified_sources(w)['platforms'][0]['jar']
    with tamper_member(w, synthetic, source, 'sdk-samepath-tamper'):
        yield


@contextmanager
def tamper_system_control(work, synthetic):
    w = Path(work)
    source = next(x for x in verified_sources(w)['platforms'][0]['classpath_JARs']
                  if x['ZIP_member'] == 'android-37.0/core-for-system-modules.jar')
    with tamper_member(w, synthetic, source, 'sdk-system-samepath-tamper'):
        yield


@contextmanager
def tamper_member(work, synthetic, source, case_name):
    if case_name not in ('sdk-samepath-tamper', 'sdk-system-samepath-tamper'):
        raise ValueError('only two explicit SDK fault-control identities required')
    w = Path(work)
    target = w / source['path']
    control = w / 'evidence/selector-controls' / case_name
    backup = control / ('original-' + target.name)
    if backup.exists() or backup.is_symlink():
        raise ValueError('no partial SDK fault-control resume')
    before = target.lstat()
    original = {k: source[k] for k in ('path', 'bytes', 'sha256')}
    s.write_json(control / 'fault-intent.json', {'state': 'before_original_inode_rename',
        'original': original, 'stat': identity(before), 'synthetic_SHA256': hashlib.sha256(synthetic).hexdigest()})
    os.rename(target, backup)
    s.sync(target.parent)
    s.sync(backup.parent)
    injected = None
    try:
        s.put(target, synthetic)
        injected = target.lstat()
        yield
    finally:
        # An uncertain replacement is never overwritten; keep original backup and fail.
        if injected is not None:
            if (identity(target.lstat()) != identity(injected)
                    or file_row(w, target)['sha256'] != hashlib.sha256(synthetic).hexdigest()):
                raise ValueError('fault input changed; retain original backup for review')
        elif target.exists() or target.is_symlink():
            raise ValueError('incomplete fault injection; retain original backup for review')
        saved = backup.lstat()
        if ((saved.st_dev, saved.st_ino) != (before.st_dev, before.st_ino)
                or file_row(w, backup)['sha256'] != original['sha256']):
            raise ValueError('original SDK backup identity/body uncertain')
        os.replace(backup, target)
        s.sync(backup.parent)
        s.sync(target.parent)
        after = target.lstat()
        if ((after.st_dev, after.st_ino) != (before.st_dev, before.st_ino)
                or file_row(w, target) != original):
            raise ValueError('original SDK inode/body restoration unconfirmed')
        s.write_json(control / 'fault-restored.json', {'state': 'original_inode_and_bytes_restored',
            'original': original, 'before_stat': identity(before), 'after_stat': identity(after)})


def require_fault_dispatch(job, args, cwd, env, timeout, reject, stdin, resolver, catalog,
                           label=TAMPER_LABEL):
    """No ambient test bypass: only the fixed real negative control can fault SDK37."""
    if label not in (TAMPER_LABEL, SYSTEM_TAMPER_LABEL):
        raise ValueError('only exact reviewed SDK fault-control labels allowed')
    name = label.removeprefix('selector-control-')
    case = job.w / 'evidence/selector-controls' / name
    expected = [job.w / 'gradle/bin/gradle', '--no-daemon', '--max-workers=1',
        '--no-watch-fs', '--console=plain', '--offline', '--project-cache-dir',
        job.w / 'selector-project-cache' / name,
        '-Dorg.gradle.jvmargs=-Xmx1536m -XX:MaxMetaspaceSize=512m',
        '-I', resolver, 'dependencies', '-DluoboClasspathCatalog=' + str(catalog[0]),
        '-DluoboClasspathCatalogSha256=' + catalog[1]]
    expected_env = parent.environment(job.w, True)
    expected_env['GRADLE_USER_HOME'] = str(job.w / 'selector-gradle-home')
    if (list(map(str, args)) != list(map(str, expected)) or cwd != case or env != expected_env
            or timeout != 120 or stdin is not None
            or reject != 'unknown or unapproved local artifact component refused: file-not-in-independent-producer'):
        raise ValueError('SDK fault injection only inside exact real negative control')


if __name__ == '__main__':
    import argparse
    parser = argparse.ArgumentParser()
    parser.add_argument('--archive-upload-admission', type=Path, required=True)
    archive_upload_admission(parser.parse_args().archive_upload_admission)
