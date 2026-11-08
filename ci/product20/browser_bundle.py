"""Receive the previously built Web bundle for the isolated Product20 browser."""

import hashlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import stat
import sys
from urllib.error import HTTPError
from urllib.parse import urlsplit
from urllib.request import HTTPRedirectHandler, ProxyHandler, Request, build_opener
from zipfile import ZIP_DEFLATED, ZIP_STORED, ZipFile


REPOSITORY = 'damaowoaini/LuoboIM'
COHORT = 'web4343-voice-retirement-v1'
SOURCE = {'members': 4343, 'digest': 'f634ba9dd77d871a9512442faafbe719c05c671cb3ae885b57d5088d6588deb8'}
DESCRIPTOR = (821990, '17de8919da602aa6c6874400a13a2fce4985b28828118ffaf5e465dc940ef6b4')
ARTIFACT = {'id': 13551666399, 'run': 42317450188, 'attempt': 1,
            'head': '48f54316186cf73dca84634350da8133f74fc9e2', 'bytes': 10500270,
            'sha256': '7fde317e5f90b30e123b2b23ddb7bdabb801de9ae9dafa8ab69dfa8ed4e7efa4'}
DIST = {'members': 47, 'bytes': 42623958, 'digest': '2081b66e4b1c3d765a29e4d10d01540a33ecce8bf0455ee16fb6c07e99746385'}
METADATA = {
    'dist-manifest.json': (7303, 'c576453133a0a3320e739d30f1e06fa2e641594bf2d3ef5e809af3ac2b6078fe'),
    'dist-proof.json': (744, 'e8bb9aea9f772e75792e864a1ba871b2c0db4375a623677893a05d6d320ec78a'),
}


def _sha(raw):
    return hashlib.sha256(raw).hexdigest()


def _safe(name):
    return (isinstance(name, str) and len(name) <= 1024 and '\\' not in name and ':' not in name
            and not any(ord(c) < 32 or ord(c) == 127 for c in name)
            and all(p not in ('', '.', '..') for p in name.split('/')))


def _rows(rows, count):
    if type(rows) is not list or len(rows) != count:
        raise ValueError('Web inventory count differs')
    result = {}
    aliases = set()
    for row in rows:
        if (type(row) is not dict or set(row) != {'path', 'bytes', 'sha256'} or not _safe(row['path'])
                or type(row['bytes']) is not int or not 0 <= row['bytes'] <= 64 << 20
                or not isinstance(row['sha256'], str) or not re.fullmatch('[0-9a-f]{64}', row['sha256'])
                or row['path'].casefold() in aliases):
            raise ValueError('Web inventory row differs')
        result[row['path']] = (row['bytes'], row['sha256'])
        aliases.add(row['path'].casefold())
    return result


def _vector(rows):
    return _sha(b''.join(name.encode() + b'\0' + rows[name][1].encode() + b'\n' for name in sorted(rows, key=str.encode)))


def _file(path, pin):
    size, digest = pin
    if path.resolve() != path:
        raise ValueError('Web source alias refused')
    with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK), 'rb') as f:
        before = os.fstat(f.fileno())
        if not stat.S_ISREG(before.st_mode) or before.st_nlink != 1 or before.st_size != size:
            raise ValueError('Web source file identity differs')
        raw = f.read(size + 1)
        def identity(s):
            return (s.st_dev, s.st_ino, s.st_mode, s.st_nlink, s.st_size, s.st_mtime_ns, s.st_ctime_ns)
        if identity(before) != identity(os.fstat(f.fileno())) or identity(before) != identity(path.lstat()):
            raise ValueError('Web source changed while read')
    if len(raw) != size or _sha(raw) != digest:
        raise ValueError('Web source body differs')
    return raw


def verify_source(repo):
    """Check the descriptor and all 4,343 Web bodies against the accepted build."""
    repo = Path(repo).absolute()
    source = repo / 'web'
    if source.resolve() != source or not source.is_dir():
        raise ValueError('Canonical Web source root required')
    descriptor = json.loads(_file(repo / 'ci/web-build.inputs.json', DESCRIPTOR))
    rows = _rows(descriptor['files'], SOURCE['members'])
    if (descriptor['cohort'] != COHORT or descriptor['sourceFiles'] != SOURCE['members']
            or descriptor['sourceDigest'] != SOURCE['digest'] or _vector(rows) != SOURCE['digest']):
        raise ValueError('Web source vector differs')
    observed = set()
    for directory, directories, files in os.walk(source):
        parent = Path(directory)
        if any(not stat.S_ISDIR((parent / child).lstat().st_mode) for child in directories):
            raise ValueError('Web source directory alias refused')
        for name in files:
            path = parent / name
            relative = path.relative_to(source).as_posix()
            if relative not in rows or relative in observed:
                raise ValueError('Unexpected Web source file')
            _file(path, rows[relative])
            observed.add(relative)
    if observed != set(rows):
        raise ValueError('Incomplete Web source tree')
    return dict(SOURCE)


class _NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def _download(token):
    # This token is used only for fixed GitHub API requests. The signed blob
    # request carries no credentials and cannot redirect again.
    opener = build_opener(ProxyHandler({}), _NoRedirect())
    base = f'https://api.github.com/repos/{REPOSITORY}/actions'
    artifact_url = f'{base}/artifacts/{ARTIFACT["id"]}'
    headers = {'Authorization': 'Bearer ' + token, 'Accept': 'application/vnd.github+json',
               'X-GitHub-Api-Version': '2022-11-28'}

    def read(url, maximum, request_headers=None):
        with opener.open(Request(url, headers=request_headers or {}), timeout=30) as response:
            raw = response.read(maximum + 1)
            if response.status != 200 or len(raw) > maximum:
                raise ValueError('Web artifact response refused')
            return raw

    try:
        meta = json.loads(read(artifact_url, 65536, headers))
        if (meta['id'] != ARTIFACT['id'] or meta['expired'] is not False
                or meta['size_in_bytes'] != ARTIFACT['bytes'] or meta['digest'] != 'sha256:' + ARTIFACT['sha256']
                or meta['workflow_run']['id'] != ARTIFACT['run'] or meta['workflow_run']['head_sha'] != ARTIFACT['head']):
            raise ValueError('Original Web artifact identity differs')
        run = json.loads(read(f'{base}/runs/{ARTIFACT["run"]}', 2 << 20, headers))
        if (run['id'] != ARTIFACT['run'] or run['head_sha'] != ARTIFACT['head'] or run['run_attempt'] != ARTIFACT['attempt']
                or run['status'] != 'completed' or run['conclusion'] != 'success'):
            raise ValueError('Successful original Web producer required')
        try:
            with opener.open(Request(artifact_url + '/zip', headers=headers), timeout=30):
                raise ValueError('Web artifact signed redirect required')
        except HTTPError as response:
            with response:
                if response.code != 302:
                    raise ValueError('Web artifact signed redirect required')
                url = response.headers['Location']
        u = urlsplit(url)
        if (u.scheme != 'https' or not u.hostname or u.username is not None or u.password is not None
                or u.port not in (None, 443) or u.fragment
                or not u.hostname.endswith(('.blob.core.windows.net', '.actions.githubusercontent.com'))):
            raise ValueError('Anonymous Web artifact blob required')
        return read(url, ARTIFACT['bytes'])
    except Exception:
        # API and transport exceptions can contain response URLs. Keep both
        # signed locations and credentials out of the controller diagnostics.
        raise ValueError('Fixed Web artifact metadata or download rejected') from None


def receive(repo, target, token):
    """Extract the exact accepted ZIP into a new private directory and return dist."""
    if (sys.platform != 'linux' or os.getuid() == 0 or not token
            or any(os.environ.get(k) != v for k, v in {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted',
                                                       'RUNNER_OS': 'Linux', 'GITHUB_REPOSITORY': REPOSITORY}.items())):
        raise ValueError('Hosted Linux Web artifact reception required')
    target = Path(target).absolute()
    parent = target.parent.lstat()
    if (target.resolve() != target or target.exists() or not stat.S_ISDIR(parent.st_mode)
            or parent.st_uid != os.getuid() or parent.st_mode & 0o077):
        raise ValueError('Fresh private Web artifact destination required')
    verify_source(repo)
    raw = _download(token)
    if len(raw) != ARTIFACT['bytes'] or _sha(raw) != ARTIFACT['sha256']:
        raise ValueError('Original Web ZIP body differs')
    with ZipFile(io.BytesIO(raw)) as archive:
        entries = archive.infolist()
        names = {entry.filename for entry in entries}
        if (len(entries) != DIST['members'] + len(METADATA) or len(names) != len(entries)
                or not set(METADATA) <= names or sum(e.file_size for e in entries) != DIST['bytes'] + sum(p[0] for p in METADATA.values())):
            raise ValueError('Exact Web ZIP inventory required')
        for e in entries:
            if (e.orig_filename != e.filename or not _safe(e.filename) or e.is_dir() or e.flag_bits & 1
                    or e.compress_type not in (ZIP_STORED, ZIP_DEFLATED)
                    or stat.S_IFMT(e.external_attr >> 16) not in (0, stat.S_IFREG)):
                raise ValueError('Web ZIP alias or entry type refused')

        def checked_body(name, pin):
            entry = archive.getinfo(name)
            if entry.file_size != pin[0]:
                raise ValueError('Web ZIP entry size differs')
            body = archive.read(entry)  # ZipFile verifies CRC when the complete body is consumed.
            if len(body) != pin[0] or _sha(body) != pin[1]:
                raise ValueError('Web ZIP entry body differs')
            return body

        metadata = {name: checked_body(name, pin) for name, pin in METADATA.items()}
        rows = _rows(json.loads(metadata['dist-manifest.json']), DIST['members'])
        proof = json.loads(metadata['dist-proof.json'])
        if (not all(name.startswith('dist/') for name in rows) or 'dist/index.html' not in rows
                or names != set(rows) | set(METADATA) or _vector(rows) != DIST['digest']
                or sum(size for size, _ in rows.values()) != DIST['bytes']
                or any(proof[key] != value for key, value in {
                    'cohort': COHORT, 'commit': ARTIFACT['head'], 'sourceFiles': SOURCE['members'], 'sourceDigest': SOURCE['digest'],
                    'files': DIST['members'], 'bytes': DIST['bytes'], 'filesDigest': DIST['digest'],
                    'manifestSHA256': METADATA['dist-manifest.json'][1]}.items())):
            raise ValueError('Accepted Web manifest and build proof differ')
        target.mkdir(mode=0o700)
        try:
            for name in sorted(names):
                body = metadata[name] if name in metadata else checked_body(name, rows[name])
                path = target / name
                path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
                with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600), 'wb') as f:
                    f.write(body)
        except BaseException:
            shutil.rmtree(target)
            raise
    return target / 'dist'
