"""Current359 origin audit: published file names may differ from cache names.

Only a resolved, byte-matched artifact can use a same-coordinate downloaded
Gradle module's files.name -> files.url relation. KMP back-pointers require an
independently downloaded reciprocal parent; neither relation grants graph scope.
"""
import hashlib
import json
import os
from pathlib import Path
import re
import stat
from urllib.parse import unquote, urljoin, urlsplit

ALLOWED = {'dl.google.com', 'maven.google.com', 'repo.maven.apache.org', 'repo1.maven.org',
           'plugins.gradle.org', 'plugins-artifacts.gradle.org', 'storage.googleapis.com', 'jitpack.io'}
JITPACK = '/com/github/davidliu/audioswitch/039a35aefab7747c557242fa216c9ea11743b604/'


def _pairs(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError('duplicate published metadata key')
        result[key] = value
    return result


def _stable(path, root, maximum):
    if path.resolve(strict=True) != path or not path.is_relative_to(root):
        raise ValueError('canonical owned origin file required')
    before = path.stat()
    if (not stat.S_ISREG(before.st_mode) or before.st_uid != os.getuid()
            or before.st_nlink != 1 or before.st_size > maximum):
        raise ValueError('bounded owned regular origin file required')
    body = path.read_bytes()
    after = path.stat()
    fields = ('st_dev', 'st_ino', 'st_mode', 'st_uid', 'st_gid', 'st_nlink', 'st_size', 'st_mtime_ns', 'st_ctime_ns')
    if len(body) != before.st_size or any(getattr(before, key) != getattr(after, key) for key in fields):
        raise ValueError('origin file changed during read')
    return body


def _json(body):
    def bad_constant(_value):
        raise ValueError('non-JSON published metadata number')
    return json.loads(body, object_pairs_hook=_pairs, parse_constant=bad_constant)


def _coordinate(value):
    if (not isinstance(value, tuple) or len(value) != 3
            or any(not isinstance(x, str) or not re.fullmatch(r'[A-Za-z0-9_.+\-]+', x) for x in value)):
        raise ValueError('finite Maven coordinate required')
    if any(x in ('.', '..') for x in value):
        raise ValueError('Maven coordinate traversal refused')
    return value


def _suffix(coordinate, filename):
    group, module, version = _coordinate(coordinate)
    return '/' + group.replace('.', '/') + '/' + module + '/' + version + '/' + filename


def _component(value):
    if not isinstance(value, dict):
        raise ValueError('published component identity required')
    return _coordinate(tuple(value.get(key) for key in ('group', 'module', 'version')))


def _same_repository_url(base, relative, coordinate, filename):
    # KMP's component/available-at backlinks may use ../, but their normalized
    # destination must be exactly the declared Maven coordinate on this origin.
    if not isinstance(relative, str) or not relative or '\\' in relative or '%' in relative:
        raise ValueError('canonical Maven metadata relation required')
    ref = urlsplit(relative)
    if ref.scheme or ref.netloc or ref.query or ref.fragment:
        raise ValueError('external metadata relation refused')
    old, new = urlsplit(base), urlsplit(urljoin(base, relative))
    old_suffix = _suffix(coordinate, filename)
    # Caller passes a base URL whose repository prefix is separately derived.
    if new.scheme != 'https' or new.netloc != old.netloc or not new.path.endswith(old_suffix):
        raise ValueError('metadata relation leaves permitted origin or coordinate')
    return new.geturl()


class PublishedFiles:
    def __init__(self, work, downloads):
        self.work = Path(work)
        self.cache = self.work / 'gradle-cache/caches/modules-2/files-2.1'
        self.downloads = set(downloads)
        report = self.work / 'evidence/gradle-resolved-artifacts.json'
        raw = _stable(report, self.work, 64 * 1024 * 1024)
        self.rows = _json(raw)
        if not isinstance(self.rows, list):
            raise ValueError('actual resolved artifact rows required')
        self.report_sha = hashlib.sha256(raw).hexdigest()

    def module(self, coordinate, required_url=None):
        group, module, version = _coordinate(coordinate)
        filename = module + '-' + version + '.module'
        directory = self.cache / group / module / version
        found = list(directory.glob('*/' + filename))
        if len(found) != 1:
            raise ValueError('unique actual downloaded module body required')
        path = found[0]
        raw = _stable(path, self.work, 2 * 1024 * 1024)
        sources = sorted(u for u in self.downloads if urlsplit(u).path.endswith(_suffix(coordinate, filename)))
        if len(sources) != 1 or (required_url is not None and sources != [required_url]):
            raise ValueError('unique actual allowed module download required')
        data = _json(raw)
        if not isinstance(data, dict) or data.get('formatVersion') not in ('1.0', '1.1') or not isinstance(data.get('variants'), list):
            raise ValueError('known Gradle module schema required')
        url = sources[0]
        if '%' in url:
            raise ValueError('ambiguous encoded metadata origin refused')
        repository = url[:-len(_suffix(coordinate, filename))]
        proof = {'coordinate': ':'.join(coordinate), 'path': str(path.relative_to(self.work)),
                 'url': url, 'bytes': len(raw), 'sha256': hashlib.sha256(raw).hexdigest(),
                 'cacheKey': path.parent.name, 'cacheKeyIsOpaque': True}
        return data, proof, repository

    def alias(self, coordinate, filename, path, size, digest):
        info = path.stat()
        if (path.resolve(strict=True) != path or not path.is_relative_to(self.cache)
                or not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_size != size):
            raise ValueError('canonical owned resolved artifact required')
        relative = str(path.relative_to(self.work))
        matching = [x for x in self.rows if isinstance(x, dict) and x.get('path') == relative]
        if not matching:
            raise ValueError('file alias lacks actual resolved component')
        for row in matching:
            selected = row.get('selected')
            if (not isinstance(selected, dict) or selected.get('kind') != 'module'
                    or _component(selected) != coordinate or selected.get('identity') != ':'.join(coordinate)
                    or row.get('coordinate') != ':'.join(coordinate)
                    or type(row.get('bytes')) is not int or row['bytes'] != size or row.get('sha256') != digest):
                raise ValueError('file alias resolved identity or body differs')
        data, child, repository = self.module(coordinate)
        component = _component(data.get('component'))
        proofs = [child]
        if component != coordinate:
            backlink = data['component'].get('url')
            parent_name = component[1] + '-' + component[2] + '.module'
            parent_url = _same_repository_url(child['url'], backlink, component, parent_name)
            if parent_url != repository + _suffix(component, parent_name):
                raise ValueError('KMP parent leaves original Maven repository')
            parent, parent_proof, parent_repository = self.module(component, parent_url)
            if _component(parent.get('component')) != component or parent_repository != repository:
                raise ValueError('KMP parent identity differs')
            child_name = coordinate[1] + '-' + coordinate[2] + '.module'
            reciprocal = []
            for variant in parent['variants']:
                if not isinstance(variant, dict):
                    raise ValueError('typed published variant required')
                available = variant.get('available-at')
                if available is None:
                    continue
                if not isinstance(available, dict):
                    raise ValueError('typed KMP available-at required')
                if _component(available) == coordinate:
                    reciprocal.append(_same_repository_url(parent_url, available.get('url'), coordinate, child_name))
            if not reciprocal or any(x != child['url'] for x in reciprocal):
                raise ValueError('reciprocal actual KMP parent relation required')
            proofs.append(parent_proof)
        elif data['component'].get('url') is not None:
            own = coordinate[1] + '-' + coordinate[2] + '.module'
            if _same_repository_url(child['url'], data['component']['url'], coordinate, own) != child['url']:
                raise ValueError('same-component backlink changed origin')
        files = []
        for variant in data['variants']:
            if not isinstance(variant, dict) or not isinstance(variant.get('files', []), list):
                raise ValueError('typed published files required')
            for entry in variant.get('files', []):
                if not isinstance(entry, dict):
                    raise ValueError('typed published file required')
                if entry.get('name') == filename:
                    files.append(entry)
        if not files or any(x != files[0] for x in files):
            raise ValueError('unique published cached-name mapping required')
        entry = files[0]
        relative_url = entry.get('url')
        if (not isinstance(relative_url, str) or not re.fullmatch(r'[A-Za-z0-9_.+\-]+', relative_url)
                or relative_url in ('.', '..') or type(entry.get('size')) is not int or entry['size'] != size):
            raise ValueError('published file URL or size differs')
        url = repository + _suffix(coordinate, relative_url)
        if url not in self.downloads:
            raise ValueError('actual published artifact download missing')
        strong = False
        for name in ('sha256', 'sha512'):
            expected = entry.get(name)
            if expected is None:
                continue
            if not isinstance(expected, str) or not re.fullmatch('[0-9a-f]{' + str(hashlib.new(name).digest_size * 2) + '}', expected):
                raise ValueError('strong published file hash malformed')
            actual = digest if name == 'sha256' else _hash(path, name)
            if actual != expected:
                raise ValueError('published file strong hash differs')
            strong = True
        if not strong:
            raise ValueError('strong published file hash required')
        return [url], {'resolved_report_sha256': self.report_sha, 'resolved_coordinate': ':'.join(coordinate),
                       'cached_filename': filename, 'published_relative_url': relative_url,
                       'metadata': proofs, 'target_bytes': size, 'target_sha256': digest,
                       'target_cache_key': path.parent.name, 'cache_keys_are_opaque': True,
                       'KMP_relation_grants_no_new_component_or_repository': True}


def _hash(path, name='sha256'):
    before = path.stat()
    result = hashlib.new(name)
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(65536), b''):
            result.update(block)
    after = path.stat()
    fields = ('st_dev', 'st_ino', 'st_mode', 'st_uid', 'st_gid', 'st_nlink', 'st_size', 'st_mtime_ns', 'st_ctime_ns')
    if any(getattr(before, key) != getattr(after, key) for key in fields):
        raise ValueError('artifact changed during source hash')
    return result.hexdigest()


def audit(work, log):
    """Retain the original ordinary-source audit; use strict metadata only for aliases."""
    roots = Path(work) / 'gradle-cache/caches/modules-2/files-2.1'
    downloads = []
    for url in re.findall(r'\bDownloading\s+(https://[^\s<>"\]]+)', log):
        parsed = urlsplit(url.rstrip(').,'))
        if parsed.username or parsed.password or parsed.query or parsed.fragment or parsed.hostname not in ALLOWED:
            raise ValueError('unapproved actual dependency URL')
        if parsed.hostname == 'jitpack.io' and not parsed.path.startswith(JITPACK):
            raise ValueError('unapproved JitPack module/version')
        downloads.append(parsed.geturl())
    rows = []
    aliases = None
    for path in sorted(roots.rglob('*')):
        if not path.is_file():
            continue
        rel = path.relative_to(roots)
        if path.is_symlink() or len(rel.parts) != 5:
            raise ValueError('unexpected fresh Maven cache path')
        group, module, version, _content_key, filename = rel.parts
        coordinate = (group, module, version)
        size, digest = path.stat().st_size, _hash(path)
        proof = None
        if group == 'luobo.qa.openim':
            origin = ['owned:new_hash_checked_job_AAR']
        else:
            suffix = _suffix(coordinate, filename)
            origin = sorted({u for u in downloads if unquote(urlsplit(u).path).endswith(suffix)})
            if not origin:
                if aliases is None:
                    aliases = PublishedFiles(Path(work), downloads)
                origin, proof = aliases.alias(coordinate, filename, path, size, digest)
        row = {'coordinate': ':'.join(coordinate), 'filename': filename, 'sha256': digest,
               'bytes': size, 'observed_sources': origin}
        if proof is not None:
            row['published_file_alias'] = proof
        rows.append(row)
    if not rows:
        raise ValueError('actual Maven acquisition evidence missing')
    return {'artifacts': rows, 'all_actual_artifact_sources_accounted': True,
            'sources_are_first_acquisition_evidence_not_prior_hermetic_pins': True}
