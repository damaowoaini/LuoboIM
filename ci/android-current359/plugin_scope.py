"""Independent finite plugin-source producer and strict report consumer.

This never learns an allowed artifact/version from a running Gradle graph.
No producer is called on import. Acquisition is setup-only, before app models.
"""
import base64
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import tarfile
import time
import urllib.request

import rich_current as rich
import support as s

CONTRACT_SHA = 'ed0894e5ef85da81c7693597fb62707b99f6a2b2a8e1312df5e63f90c794e6f5'
SOURCE_REF = '0e1cb1ae5f3edc3db8a0a94970b363a4cd6ae3b1'
SOURCE_DIGEST = 'e56ccf115d0299ef01241fc6f871af3e938fcdf356d77c7caa67191b026c87c2'
SEAL = 'evidence/plugin-source-seal.json'
PREFIX = 'evidence/plugin-sources'
CONFIGS = {'kotlinAbiValidationCompatClasspath', 'kotlinBouncyCastleConfiguration'}


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':'))


def file_row(path, limit=16 << 20, budget=None):
    path = Path(path)
    if path.resolve(strict=True) != path:
        raise ValueError('plugin source path alias')
    with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW), 'rb') as stream:
        a = os.fstat(stream.fileno())
        if (not stat.S_ISREG(a.st_mode) or a.st_uid != os.getuid() or a.st_nlink != 1
                or a.st_size < 0 or a.st_size > limit):
            raise ValueError('plugin owned bounded single-link source required')
        h, size = hashlib.sha256(), 0
        for block in iter(lambda: stream.read(65536), b''):
            if budget is not None:
                budget.check()
            h.update(block); size += len(block)
        z = os.fstat(stream.fileno())
    keys = ('st_dev', 'st_ino', 'st_size', 'st_mode', 'st_uid', 'st_nlink', 'st_mtime_ns', 'st_ctime_ns')
    if size != a.st_size or any(getattr(a, k) != getattr(z, k) or getattr(z, k) != getattr(path.lstat(), k) for k in keys):
        raise ValueError('plugin source changed during read')
    return {'bytes': size, 'sha256': h.hexdigest()}


def contract(here, inputs):
    here = Path(here)
    raw = rich.read(here / 'plugin-scope-inputs.json')
    if rich.sha(raw) != CONTRACT_SHA:
        raise ValueError('fixed plugin source contract differs')
    value = json.loads(raw)
    projects = value['projects']
    indexed = {x['path']: x for x in inputs['files']}
    if (value['schema'] != 1 or value['source_ref'] != SOURCE_REF or value['source535_digest'] != SOURCE_DIGEST
            or inputs['source_digest'] != SOURCE_DIGEST or inputs['members'] != 535
            or value['pub_lock'] != indexed['app/pubspec.lock'] or value['locked_package_count'] != 178
            or len(projects) != 22 or len({p['project'] for p in projects}) != 22
            or sum(p['kind'] == 'local-source535' for p in projects) != 1
            or set(value['configurations']) != CONFIGS):
        raise ValueError('exact535/native plugin source identity required')
    catalog, _, _ = rich.approved(here)
    if (value['App_catalog_sha256'] != rich.CATALOG_SHA or value['old_App_library_sha256'] != rich.SCRIPT_SHA
            or value['App_scope_sources'] != catalog['scope']['sources']
            or rich.sha(rich.read(here / 'plugin-rich-consumer.gradle')) != value['plugin_library_sha256']):
        raise ValueError('original18 and derived plugin library identity differs')
    for entry in projects:
        if (not re.fullmatch(':[a-z][a-z0-9_]*', entry['project'])
                or entry['slot'] != rich.sha(entry['project'].encode())):
            raise ValueError('finite exact project identity required')
        if entry['kind'] == 'local-source535':
            expected = [r for r in inputs['files'] if r['path'].startswith('packages/flutter_openim_sdk/')]
            if (entry['project'] != ':flutter_openim_sdk' or entry['source_rows'] != expected
                    or entry['project_relative'] != 'source/packages/flutter_openim_sdk/android'):
                raise ValueError('local SDK plugin must come from original535 rows')
        elif (entry['kind'] != 'locked-official-pub-archive'
                or entry['project'] != ':' + entry['package']
                or entry['project_relative'] != entry['package_relative'] + '/android'
                or entry['package_relative'] != 'pub-cache/hosted/pub.dev/' + entry['package'] + '-' + entry['version']
                or entry['archive_url'] != 'https://pub.dev/api/archives/' + entry['package'] + '-' + entry['version'] + '.tar.gz'):
            raise ValueError('finite official Pub package tuple required')
    return value


def archive_rows(path, maximum, expected_sha, budget=None):
    """Hash safe regular members of an ALREADY checksum-verified exact archive."""
    result, seen, decoded = [], set(), 0
    path = Path(path)
    if path.resolve(strict=True) != path:
        raise ValueError('Pub archive path alias')
    with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW), 'rb') as source:
        before = os.fstat(source.fileno())
        if (not stat.S_ISREG(before.st_mode) or before.st_uid != os.getuid() or before.st_nlink != 1
                or before.st_size > maximum['each']):
            raise ValueError('owned bounded archive required')
        h = hashlib.sha256()
        for block in iter(lambda: source.read(65536), b''):
            if budget is not None: budget.check()
            h.update(block)
        if h.hexdigest() != expected_sha:
            raise ValueError('Pub archive independent checksum differs')
        source.seek(0)
        bundle = tarfile.open(fileobj=source, mode='r:gz')
        for member in bundle:
            if budget is not None:
                budget.check()
            name = s.name(member.name.rstrip('/'))
            if name in seen:
                raise ValueError('duplicate Pub archive path')
            seen.add(name)
            if len(seen) > maximum['files_sum'] * 2:
                raise ValueError('Pub archive entry cap')
            if member.isdir():
                continue
            if not member.isfile() or member.size < 0 or member.size > maximum['file_each']:
                raise ValueError('Pub archive link/special/large member refused')
            decoded += member.size
            if decoded > maximum['decoded_sum'] or len(result) >= maximum['files_sum']:
                raise ValueError('Pub source archive inventory cap')
            h, size = hashlib.sha256(), 0
            with bundle.extractfile(member) as stream:
                for block in iter(lambda: stream.read(65536), b''):
                    if budget is not None:
                        budget.check()
                    h.update(block); size += len(block)
            if size != member.size:
                raise ValueError('short Pub source member')
            result.append({'path': name, 'bytes': size, 'sha256': h.hexdigest()})
        bundle.close()
        after = os.fstat(source.fileno())
        keys = ('st_dev', 'st_ino', 'st_size', 'st_mode', 'st_uid', 'st_nlink', 'st_mtime_ns', 'st_ctime_ns')
        if any(getattr(before, k) != getattr(after, k) or getattr(after, k) != getattr(path.lstat(), k) for k in keys):
            raise ValueError('Pub archive changed during authenticated inventory')
    if not result or not any(x['path'] == 'pubspec.yaml' for x in result):
        raise ValueError('real Pub source inventory required')
    return sorted(result, key=lambda x: x['path'])


def check_tree(root, expected, budget=None):
    root = Path(root)
    if root.resolve(strict=True) != root or not root.is_dir() or root.stat().st_uid != os.getuid():
        raise ValueError('canonical owned plugin package required')
    actual = []
    for directory, names, files in os.walk(root, followlinks=False):
        for name in names:
            p = Path(directory) / name
            if p.is_symlink() or not p.is_dir() or p.stat().st_uid != os.getuid():
                raise ValueError('plugin directory alias/owner refused')
        actual.extend(str((Path(directory) / name).relative_to(root)) for name in files)
    if sorted(actual) != sorted(r['path'] for r in expected):
        raise ValueError('installed plugin differs from independent full source inventory')
    for row in expected:
        if file_row(root / s.name(row['path']), budget=budget) != {k: row[k] for k in ('bytes', 'sha256')}:
            raise ValueError('plugin source body differs from independent origin')


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def acquire(entry, output, budget, maximum, deadline):
    """Finite public URL only; no proxy, auth, alternate host or redirect."""
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    with opener.open(entry['archive_url'], timeout=30) as response, output.open('xb') as destination:
        if response.status != 200 or response.url != entry['archive_url']:
            raise ValueError('exact official Pub archive response required')
        total, h = 0, hashlib.sha256()
        deadline = min(deadline, time.monotonic() + 90)
        while True:
            budget.check()
            if time.monotonic() > deadline: raise ValueError('finite plugin source acquisition timeout')
            block = response.read1(65536)
            if not block: break
            total += len(block)
            if total > maximum:
                raise ValueError('Pub archive acquisition cap')
            h.update(block); destination.write(block)
        destination.flush(); os.fsync(destination.fileno())
    s.sync(output.parent)
    if h.hexdigest() != entry['archive_sha256']:
        raise ValueError('official Pub archive checksum differs from original178 lock')
    return {'url': entry['archive_url'], 'bytes': total, 'sha256': h.hexdigest()}


def produce(job, here):
    work, b = job.w, job.b
    spec = contract(here, job.inputs)
    if (work / PREFIX).exists() or (work / SEAL).exists():
        raise ValueError('first plugin source producer only; no partial resume')
    if any((work / n).exists() for n in ('gradle-project-cache', 'selector-project-cache')):
        raise ValueError('plugin source producer must precede app models')
    s.mkdir(work / PREFIX); s.mkdir(work / 'downloads/plugin-source-origins')
    lock = spec['pub_lock']
    if file_row(work / 'source/app/pubspec.lock') != {k: lock[k] for k in ('bytes', 'sha256')}:
        raise ValueError('fixed178 pub lock differs before plugin producer')
    s.write_json(work / PREFIX / 'intent.json', {'contract_sha256': CONTRACT_SHA,
        'before_app_models': True, 'independent_archives': 21, 'local_source535': 1})
    entries, archive_total, decoded, count = [], 0, 0, 0
    deadline = time.monotonic() + 600
    for entry in spec['projects']:
        b.check()
        if entry['kind'] == 'local-source535':
            prefix = 'packages/flutter_openim_sdk/'
            files = [{**row, 'path': row['path'][len(prefix):]} for row in entry['source_rows']]
            origin = {'kind': 'local-source535', 'source_ref': SOURCE_REF, 'source_digest': SOURCE_DIGEST}
        else:
            archive = work / 'downloads/plugin-source-origins' / (entry['package'] + '-' + entry['version'] + '.tar.gz')
            origin = {'kind': 'locked-official-pub-archive', **acquire(entry, archive, b, spec['archive_limits']['each'], deadline)}
            archive_total += origin['bytes']
            if archive_total > spec['archive_limits']['sum']:
                raise ValueError('aggregate plugin archive cap')
            files = archive_rows(archive, spec['archive_limits'], entry['archive_sha256'], b)
        decoded += sum(r['bytes'] for r in files); count += len(files)
        if decoded > spec['archive_limits']['decoded_sum'] or count > spec['archive_limits']['files_sum']:
            raise ValueError('aggregate plugin source inventory cap')
        check_tree(work / entry['package_relative'], files, b)
        gate_sources = {'source/' + p: h for p, h in spec['App_scope_sources'].items()}
        for row in files:
            p = row['path']
            if p == 'pubspec.yaml' or p.startswith('android/') and p.endswith(('.gradle', '.gradle.kts', '.properties', '.toml')):
                gate_sources[entry['package_relative'] + '/' + p] = row['sha256']
        if not any(p in ('android/build.gradle', 'android/build.gradle.kts') for p in (r['path'] for r in files)):
            raise ValueError('exact Android plugin build script required')
        manifest = {'project': entry['project'], 'origin': origin, 'files': files}
        path = work / PREFIX / (entry['slot'] + '.json'); s.write_json(path, manifest)
        entries.append({'project': entry['project'], 'slot': entry['slot'], 'project_relative': entry['project_relative'],
            'package_relative': entry['package_relative'], 'origin': origin, 'gate_sources': gate_sources,
            'source_manifest': {'path': str(path.relative_to(work)), **file_row(path, 16 << 20)}})
    seal = {'schema': 1, 'contract_sha256': CONTRACT_SHA, 'source_ref': SOURCE_REF, 'source_digest': SOURCE_DIGEST,
            'root_relative': spec['root_relative'], 'entries': entries, 'archive_bytes': archive_total,
            'decoded_source_bytes': decoded, 'source_files': count, 'before_app_models': True,
            'actual_plugin_graphs_accepted': False}
    s.write_json(work / SEAL, seal)
    return verified(work, here, job.inputs, b)


def verified(work, here, inputs, budget=None):
    work = Path(work); spec = contract(here, inputs)
    body = rich.read(work / SEAL, 16 << 20); seal = json.loads(body)
    if (seal['schema'] != 1 or seal['contract_sha256'] != CONTRACT_SHA or seal['source_ref'] != SOURCE_REF
            or seal['source_digest'] != SOURCE_DIGEST or seal['before_app_models'] is not True
            or seal['actual_plugin_graphs_accepted'] is not False or len(seal['entries']) != 22
            or seal['root_relative'] != spec['root_relative']):
        raise ValueError('complete independent plugin source seal required')
    for entry, approved in zip(seal['entries'], spec['projects']):
        for key in ('project', 'slot', 'project_relative', 'package_relative'):
            if entry[key] != approved[key]:
                raise ValueError('plugin source scope entry differs')
        manifest = entry['source_manifest']; p = work / s.name(manifest['path'])
        if manifest['path'] != PREFIX + '/' + approved['slot'] + '.json' or file_row(p, 16 << 20, budget) != {k: manifest[k] for k in ('bytes', 'sha256')}:
            raise ValueError('plugin source manifest differs')
        value = json.loads(rich.read(p, 16 << 20))
        if value['project'] != entry['project'] or value['origin'] != entry['origin']:
            raise ValueError('plugin manifest origin or project differs')
        if approved['kind'] == 'local-source535':
            if value['origin'] != {'kind': 'local-source535', 'source_ref': SOURCE_REF, 'source_digest': SOURCE_DIGEST}:
                raise ValueError('local SDK535 origin differs')
            files = [{**row, 'path': row['path'][len('packages/flutter_openim_sdk/'):]} for row in approved['source_rows']]
        else:
            archive = work / 'downloads/plugin-source-origins' / (approved['package'] + '-' + approved['version'] + '.tar.gz')
            actual = file_row(archive, spec['archive_limits']['each'], budget)
            if actual['sha256'] != approved['archive_sha256'] or entry['origin'] != {'kind': 'locked-official-pub-archive', 'url': approved['archive_url'], **actual}:
                raise ValueError('independent locked Pub origin differs')
            files = archive_rows(archive, spec['archive_limits'], approved['archive_sha256'], budget)
        if value['files'] != files:
            raise ValueError('source manifest must derive from exact independent origin')
        check_tree(work / entry['package_relative'], files, budget)
        expected = {'source/' + p: h for p, h in spec['App_scope_sources'].items()}
        for row in files:
            path = row['path']
            if path == 'pubspec.yaml' or path.startswith('android/') and path.endswith(('.gradle', '.gradle.kts', '.properties', '.toml')):
                expected[entry['package_relative'] + '/' + path] = row['sha256']
        if entry['gate_sources'] != expected:
            raise ValueError('plugin critical source set differs from independent archive')
        for path, expected_sha in expected.items():
            if file_row(work / s.name(path), budget=budget)['sha256'] != expected_sha:
                raise ValueError('bound plugin/App source differs')
    return seal, body


def embedded(work, here, inputs, budget=None):
    seal, body = verified(work, here, inputs, budget)
    spec = contract(here, inputs); script = rich.read(Path(here) / 'plugin-rich-consumer.gradle')
    return {'contract_sha256': CONTRACT_SHA, 'seal_sha256': rich.sha(body),
        'registry': {k: seal[k] for k in ('schema', 'contract_sha256', 'source_ref', 'source_digest', 'root_relative', 'entries')},
        'script_sha256': spec['plugin_library_sha256'], 'script_base64': base64.b64encode(script).decode('ascii')}


def validate_report(catalog, entry, seal_sha, value):
    keys = {'catalog_sha256', 'project', 'configuration', 'scope_sources', 'no_force_or_substitution',
            'edges', 'artifacts', 'plugin_seal_sha256', 'plugin_origin'}
    if (set(value) != keys or value['configuration'] not in CONFIGS or value['project'] != entry['project']
            or value['plugin_seal_sha256'] != seal_sha or value['plugin_origin'] != entry['origin']):
        raise ValueError('plugin report identity/source seal differs')
    projected = copy.deepcopy(catalog)
    projected['scope']['project'] = entry['project']; projected['scope']['sources'] = entry['gate_sources']
    return rich.validate_report(projected, value)  # Unchanged original18 graph/body/variant/origin template.


def reports(work, here, inputs, phase, require_all):
    if phase not in ('metadata', 'apk'):
        raise ValueError('finite plugin report phase required')
    seal, body = verified(work, here, inputs)
    catalog, _, _ = rich.approved(here)
    by_slot = {x['slot']: x for x in seal['entries']}
    root = Path(work) / 'evidence/plugin-rich-consumed' / phase
    values, pins = [], []
    if root.exists():
        if root.resolve(strict=True) != root or not root.is_dir():
            raise ValueError('plugin report root alias')
        for directory in sorted(root.iterdir()):
            entry = by_slot.get(directory.name)
            if entry is None or directory.resolve(strict=True) != directory or not directory.is_dir():
                raise ValueError('unknown plugin report slot')
            for p in sorted(directory.iterdir()):
                value = json.loads(rich.read(p)); config = value['configuration']
                if config not in CONFIGS or p.name != config + '.json':
                    raise ValueError('finite plugin report filename differs')
                validate_report(catalog, entry, rich.sha(body), value)
                values.append(value); pins.append({'path': str(p.relative_to(work)), 'sha256': s.sha(p)})
    seen = [(v['project'], v['configuration']) for v in values]
    wanted = {(entry['project'], c) for entry in seal['entries'] for c in CONFIGS}
    if len(set(seen)) != len(seen) or require_all and set(seen) != wanted:
        raise ValueError('complete registered plugin configuration reports required')
    return {'seal_sha256': rich.sha(body), 'reports': pins, 'actual_project_configurations': seen,
            'runtime_or_APK_accepted': False}


def bind_origins(work, here, inputs):
    # Existing App14+BC4 and every original18 source/body/origin remain mandatory.
    rich.require_origin_binding(work, here)
    proof = reports(work, here, inputs, 'metadata', True)
    s.write_json(Path(work) / 'evidence/plugin-rich-origin-binding.json', {
        **proof, 'App_origin_binding_sha256': s.sha(Path(work) / 'evidence/rich-origin-binding.json'),
        'template_catalog_sha256': rich.CATALOG_SHA, 'actual_registered_configurations': 44})


def require_origin_binding(work, here, inputs):
    rich.require_origin_binding(work, here)
    proof = reports(work, here, inputs, 'metadata', True)
    expected = {**proof, 'App_origin_binding_sha256': s.sha(Path(work) / 'evidence/rich-origin-binding.json'),
                'template_catalog_sha256': rich.CATALOG_SHA, 'actual_registered_configurations': 44}
    if json.loads(rich.read(Path(work) / 'evidence/plugin-rich-origin-binding.json', 16 << 20)) != expected:
        raise ValueError('registered plugin graph/source/origin proof changed')
