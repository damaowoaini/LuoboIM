"""Bounded immutable R7 artifact reuse. This module never executes native tools."""
import hashlib
import json
import os
from pathlib import Path
import ssl
import stat
import urllib.error
import urllib.parse
import urllib.request
import zipfile

API = 'https://api.github.com/repos/damaowoaini/LuoboIM'


def sha(path):
    h = hashlib.sha256()
    with Path(path).open('rb') as f:
        for block in iter(lambda: f.read(1024**2), b''):
            h.update(block)
    return h.hexdigest()


def safe_name(value):
    if (not value or value.startswith('/') or '\\' in value
            or any(p in {'', '.', '..'} for p in value.split('/'))
            or any(ord(c) < 32 for c in value)):
        raise ValueError('unsafe artifact member')
    return value


def mkdir(path):
    path = Path(path)
    if not path.exists():
        if not path.parent.exists():
            mkdir(path.parent)
        parent = path.parent.lstat()
        if (path.parent.resolve() != path.parent or not stat.S_ISDIR(parent.st_mode)
                or parent.st_uid != os.getuid()):
            raise ValueError('owned canonical parent required')
        path.mkdir(mode=0o700)
        sync(path.parent)
    info = path.lstat()
    if (path.resolve() != path or not stat.S_ISDIR(info.st_mode)
            or info.st_uid != os.getuid() or info.st_mode & 0o077):
        raise ValueError('private owned canonical directory required')


def sync(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def put(path, body):
    mkdir(path.parent)
    with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600), 'wb') as f:
        f.write(body)
        f.flush()
        os.fsync(f.fileno())
    sync(path.parent)


def record(path, value):
    put(path, (json.dumps(value, sort_keys=True, indent=2) + '\n').encode())


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def opener():
    return urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect(),
                                      urllib.request.HTTPSHandler(context=ssl.create_default_context()))


def blob_url(url):
    p = urllib.parse.urlsplit(url)
    if (p.scheme != 'https' or not p.hostname or not p.hostname.endswith('.blob.core.windows.net')
            or p.username or p.password or p.port not in {None, 443} or p.fragment):
        raise ValueError('anonymous artifact blob endpoint refused')
    return url


def api_json(path, token):
    if not path.startswith('/actions/') or '?' in path:
        raise ValueError('finite artifact API path required')
    request = urllib.request.Request(API + path, headers={
        'Authorization': 'Bearer ' + token, 'Accept': 'application/vnd.github+json',
        'X-GitHub-Api-Version': '2022-11-28'})
    with opener().open(request, timeout=60) as response:
        body = response.read(2*1024**2 + 1)
        if response.status != 200 or len(body) > 2*1024**2:
            raise ValueError('bounded API result required')
    return json.loads(body)


def check_remote(spec, token):
    parent = spec['parent']
    run = api_json('/actions/runs/' + str(parent['run']), token)
    jobs = api_json('/actions/runs/' + str(parent['run']) + '/jobs', token)
    actual = [j for j in jobs['jobs'] if j['id'] == parent['job']]
    if (run['id'] != parent['run'] or run['head_sha'] != parent['head'] or run['run_attempt'] != 1
            or run['status'] != 'completed' or run['conclusion'] != 'failure'
            or len(actual) != 1 or actual[0]['conclusion'] != 'failure'):
        raise ValueError('exact original terminal R7 required')
    for artifact in parent['artifacts']:
        value = api_json('/actions/artifacts/' + str(artifact['id']), token)
        if (value['expired'] or value['size_in_bytes'] != artifact['bytes']
                or value['name'] != artifact['name'] or value['digest'] != 'sha256:' + artifact['sha256']
                or value['workflow_run']['id'] != parent['run']
                or value['workflow_run']['head_sha'] != parent['head']):
            raise ValueError('exact original artifact metadata required')
    return {'run': run['id'], 'job': actual[0]['id'], 'head': run['head_sha'], 'attempt': 1}


def download(artifact, token, destination):
    request = urllib.request.Request(API + '/actions/artifacts/' + str(artifact['id']) + '/zip',
                                     headers={'Authorization': 'Bearer ' + token})
    try:
        opener().open(request, timeout=60)
    except urllib.error.HTTPError as error:
        if error.code != 302:
            raise ValueError('artifact API must return one redirect') from None
        url = blob_url(error.headers['Location'])
        error.close()
    else:
        raise ValueError('artifact API did not return required redirect')
    # A new anonymous opener/request: no token, signed URL or redirect persisted.
    h = hashlib.sha256()
    count = 0
    with opener().open(urllib.request.Request(url), timeout=60) as response:
        if response.status != 200 or int(response.headers.get('Content-Length', '-1')) != artifact['bytes']:
            raise ValueError('exact artifact size/status required')
        with os.fdopen(os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600), 'wb') as f:
            while True:
                body = response.read(min(1024**2, artifact['bytes'] - count + 1))
                if not body:
                    break
                count += len(body)
                if count > artifact['bytes']:
                    raise ValueError('artifact byte cap exceeded')
                f.write(body)
                h.update(body)
            f.flush()
            os.fsync(f.fileno())
    sync(destination.parent)
    if count != artifact['bytes'] or h.hexdigest() != artifact['sha256']:
        raise ValueError('exact artifact whole SHA required')


def extract(archive, spec, destination, allowed=None):
    if archive.stat().st_size != spec['bytes'] or sha(archive) != spec['sha256']:
        raise ValueError('artifact whole SHA required before extraction')
    mkdir(destination)
    rows = []
    with zipfile.ZipFile(archive) as z:
        entries = z.infolist()
        if len(entries) > spec['members_cap'] or sum(x.file_size for x in entries) > spec['decoded_cap']:
            raise ValueError('artifact decoded budget exceeded')
        seen = set()
        for x in entries:
            n = safe_name(x.filename.rstrip('/'))
            mode = x.external_attr >> 16
            if (n.casefold() in seen or stat.S_IFMT(mode) not in {0, stat.S_IFREG, stat.S_IFDIR}
                    or x.flag_bits & 1 or x.file_size > spec['member_cap']):
                raise ValueError('artifact duplicate/type/member refused')
            seen.add(n.casefold())
            if not x.is_dir() and allowed is not None and n not in allowed:
                raise ValueError('unexpected product member')
        if z.testzip() is not None:
            raise ValueError('artifact CRC failure')
        for x in entries:
            n = safe_name(x.filename.rstrip('/'))
            if x.is_dir():
                continue
            body = z.read(x)
            if len(body) != x.file_size:
                raise ValueError('artifact member length differs')
            put(destination/n, body)
            rows.append({'path': n, 'bytes': len(body), 'sha256': hashlib.sha256(body).hexdigest()})
    if allowed is not None and {x['path'] for x in rows} != set(allowed):
        raise ValueError('complete product member set required')
    return rows


def audit_raw(root, spec):
    for row in spec['raw_markers']:
        p = root/row['path']
        if p.stat().st_size != row['bytes'] or sha(p) != row['sha256']:
            raise ValueError('original R7 raw marker differs')
    evidence = root/'.qa/apple-cloud-1/evidence'
    if json.loads((evidence/'apple-bind.result.json').read_bytes()) != {'exit_code': 0}:
        raise ValueError('real original bind0 required')
    locks = json.loads((evidence/'core-locks-after.json').read_bytes())
    if locks['before'] != locks['after'] or locks['after']['go.sum'] != spec['core_sum']:
        raise ValueError('original core8eb9 unchanged locks required')
    count = 0
    for platform in ['ios', 'iossimulator']:
        folder = evidence/'generated'/platform
        result = json.loads((folder/'result.json').read_bytes())
        proof = json.loads((folder/'module-provenance.json').read_bytes())
        if (result['exit_code'] != 0 or not result['source_unchanged']
                or result['module_provenance'] != proof or proof['actual_catalogue_rows'] != 632
                or proof['state'] != 'passed_finite_actual_tidy_provenance'
                or json.loads((folder/'selected-graph.result.json').read_bytes()) != {'exit_code': 0}):
            raise ValueError('both actual generated module proofs required')
        for phase in ['before', 'after']:
            snap = folder/phase
            manifest = json.loads((snap/'manifest.json').read_bytes())
            if manifest['complete_source_snapshot'] is not True or manifest['error'] is not None:
                raise ValueError('complete original generated snapshot required')
            for row in manifest['files']:
                p = snap/safe_name(row['path'])
                if p.stat().st_size != row['bytes'] or sha(p) != row['sha256']:
                    raise ValueError('original generated source differs')
                count += 1
    return {'actual_bind_exit': 0, 'platforms': 2, 'generated_snapshot_rows': count,
            'core_sum': spec['core_sum'], 'reused_not_new_bind': True}
