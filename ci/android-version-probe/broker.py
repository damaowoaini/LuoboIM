"""Finite Maven-origin recorder, not a proxy: fixed HTTPS origin, GET/HEAD only."""
import hashlib
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
import os
from pathlib import Path
import re
import ssl
import stat
import threading
import urllib.error
import urllib.request
import xml.etree.ElementTree as ET

ORIGIN = 'https://repo.maven.apache.org'
TOKEN = re.compile(r'[A-Za-z0-9_][A-Za-z0-9_.-]{0,159}\Z')
FIXED = re.compile(r'[A-Za-z0-9][A-Za-z0-9_.-]{0,127}\Z')
MAX_BODY = 128 * 1024 ** 2


def digest(body):
    return hashlib.sha256(body).hexdigest()


def write_new(path, body):
    path = Path(path)
    path.parent.mkdir(parents=True, mode=0o700, exist_ok=True)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'wb') as stream:
        stream.write(body); stream.flush(); os.fsync(stream.fileno())
    fd = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def json_new(path, value):
    write_new(path, (json.dumps(value, sort_keys=True, indent=2) + '\n').encode())


def clean_path(value):
    if (not value.startswith('/maven2/') or len(value) > 1024 or '%' in value
            or any(x in value for x in ('\\', '?', '#', ':', '//'))):
        raise ValueError('unsafe_maven_path')
    parts = value[len('/maven2/'):].split('/')
    if len(parts) < 3 or any(p in ('', '.', '..') or not TOKEN.fullmatch(p) for p in parts):
        raise ValueError('unsafe_maven_path')
    return parts


def fixed(version):
    return bool(FIXED.fullmatch(version)) and not any(x in version.lower() for x in ('snapshot', 'latest'))


def constraint(required='', strict='', preferred='', rejected=(), branch=None):
    return dict(required=required, strict=strict, preferred=preferred, rejected=list(rejected), branch=branch)


def rich(c):
    return (any(v and not fixed(v) for v in (c['required'], c['strict'], c['preferred']))
            or bool(c['rejected']) or c['branch'] is not None)


def unique_json(body):
    def pairs(items):
        result = {}
        for key, value in items:
            if key in result:
                raise ValueError('duplicate_metadata_JSON_key')
            result[key] = value
        return result
    return json.loads(body, object_pairs_hook=pairs)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise ValueError('upstream_redirect_refused')


class Central:
    """No caller URL, headers, credential, proxy or ambient CA configuration."""
    def __init__(self, budget):
        self.budget = budget
        self.ca = Path('/etc/ssl/certs/ca-certificates.crt')
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
        context.load_verify_locations(cafile=str(self.ca))
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}),
            urllib.request.HTTPSHandler(context=context), NoRedirect())

    def get(self, path, limit):
        clean_path(path)
        request = urllib.request.Request(ORIGIN + path,
            headers={'Accept-Encoding': 'identity', 'User-Agent': 'Luobo-R11-origin-evidence/1'})
        try:
            response = self.opener.open(request, timeout=30)
        except urllib.error.HTTPError as error:
            if error.code != 404:
                raise ValueError('upstream_http_failure') from error
            response = error
        with response:
            if response.url != ORIGIN + path or response.headers.get('Content-Encoding', 'identity') != 'identity':
                raise ValueError('upstream_origin_or_encoding_changed')
            body = bytearray()
            while True:
                self.budget.check()
                block = response.read(min(65536, limit + 1 - len(body)))
                if not block:
                    break
                body.extend(block)
                if len(body) > limit:
                    raise ValueError('upstream_body_limit')
            length = response.headers.get('Content-Length')
            if length is not None and (not length.isdigit() or int(length) != len(body)):
                raise ValueError('upstream_length_mismatch')
            return response.code, bytes(body)


class Recorder:
    def __init__(self, work, inputs, budget, origin):
        self.work, self.inputs, self.budget, self.origin = Path(work), inputs, budget, origin
        self.cache, self.versions, self.ranged, self.files, self.sources, self.file_pins = {}, {}, set(), {}, {}, {}
        self.sequence, self.bytes, self.failure = 0, 0, None
        for root in inputs['roots']:
            self.grant(root['group'], root['module'], root['constraint'], {'kind': 'reviewed-generator', 'scope': root['configuration']})
        self.source_only = {x['path']: x for x in inputs['metadata_pins'] if x.get('source_only')}

    def grant(self, group, module, version, source):
        if not TOKEN.fullmatch(group) or not TOKEN.fullmatch(module):
            raise ValueError('invalid_module_identity')
        key = (group, module)
        v = version['strict'] or version['required'] or version['preferred']
        if fixed(v) and not rich(version):
            self.versions.setdefault(key, set()).add(v)
        else:
            matches = [x for x in self.inputs['rich'] if x['group'] == group and x['module'] == module
                and x['constraint'] == version and (source.get('kind') == 'reviewed-generator'
                    or source.get('coordinate') in x.get('source_parents', [x.get('source_parent')]))]
            if len(matches) != 1:
                raise ValueError('unreviewed_rich_metadata_edge')
            self.versions.setdefault(key, set())
            self.ranged.add(key)
        self.sources.setdefault(group + ':' + module + ':' + v, []).append(source)
        if len(self.versions) > 512 or sum(len(vs) for vs in self.versions.values()) > 4096:
            raise ValueError('metadata_authority_node_limit')

    def admit(self, path):
        parts = clean_path(path)
        if path in self.source_only:
            return 'source-only', None
        if parts[-1] in ('maven-metadata.xml', 'maven-metadata.xml.sha512', 'maven-metadata.xml.sha256', 'maven-metadata.xml.sha1', 'maven-metadata.xml.md5'):
            group, module = '.'.join(parts[:-2]), parts[-2]
            if (group, module) not in self.ranged:
                raise ValueError('unapproved_version_listing')
            return ('listing' if parts[-1] == 'maven-metadata.xml' else 'checksum'), (group, module, None)
        if len(parts) < 4:
            raise ValueError('incomplete_module_path')
        group, module, version, leaf = '.'.join(parts[:-3]), parts[-3], parts[-2], parts[-1]
        if version not in self.versions.get((group, module), ()):
            raise ValueError('unapproved_module_version')
        base = module + '-' + version
        if leaf in (base + '.pom', base + '.module'):
            return 'metadata', (group, module, version)
        suffix = next((s for s in ('.sha512', '.sha256', '.sha1', '.md5') if leaf.endswith(s)), '')
        core = leaf[:-len(suffix)] if suffix else leaf
        if (core == base + '.jar' or core in self.files.get((group, module, version), set())
                or suffix and core in (base + '.pom', base + '.module')):
            return 'checksum' if suffix else 'artifact', (group, module, version)
        raise ValueError('unapproved_artifact_name')

    def obtain(self, path, depth=0):
        self.budget.check()
        if depth > 8 or len(self.cache) >= 2048:
            raise ValueError('metadata_depth_or_count_limit')
        kind, coordinate = self.admit(path)
        if path in self.cache:
            return self.cache[path]
        limit = MAX_BODY if kind == 'artifact' else 4 * 1024 ** 2
        status, body = self.origin.get(path, limit)
        if not isinstance(body, bytes) or len(body) > limit:
            raise ValueError('origin_body_contract_or_limit')
        if status not in (200, 404):
            raise ValueError('upstream_status_refused')
        self.bytes += len(body)
        if self.bytes > 1024 ** 3:
            raise ValueError('origin_aggregate_body_limit')
        expected = next((x for x in self.inputs['metadata_pins'] if x['path'] == path), None)
        if expected and (status != 200 or len(body) != expected['bytes'] or digest(body) != expected['sha256']):
            raise ValueError('published_root_metadata_pin_mismatch')
        storage = ('body-cache/artifacts/' if kind == 'artifact' else 'evidence/broker/raw/') + digest(path.encode()) + '.body'
        write_new(self.work / storage, body)
        row = {'upstream': ORIGIN + path, 'path': path, 'status': status, 'kind': kind,
            'body': {'path': storage, 'bytes': len(body), 'sha256': digest(body)}}
        self.cache[path] = row
        if status == 200 and kind in ('metadata', 'listing'):
            self.learn(path, kind, coordinate, body, row['body'], depth)
        return row

    def learn(self, path, kind, coordinate, body, authority, depth):
        group, module, version = coordinate
        if b'<!DOCTYPE' in body.upper() or b'<!ENTITY' in body.upper():
            raise ValueError('XML_external_entity_refused')
        source = {'kind': 'published-metadata', 'coordinate': ':'.join(coordinate) if version else group + ':' + module,
            'metadata_path': path, 'body': authority}
        if kind == 'listing':
            root = ET.fromstring(body)
            if root.findtext('groupId', group) != group or root.findtext('artifactId', module) != module:
                raise ValueError('version_listing_identity')
            versions = root.findall('./versioning/versions/version')
            if not versions or len(versions) > 1024:
                raise ValueError('version_listing_limit')
            for entry in versions:
                if fixed(entry.text or ''):
                    self.versions[(group, module)].add(entry.text)
            return
        if path.endswith('.module'):
            data = unique_json(body)
            c = data['component']
            if (c['group'], c['module'], c['version']) != coordinate or len(data['variants']) > 128:
                raise ValueError('module_metadata_identity')
            for variant in data['variants']:
                if 'available-at' in variant:
                    a = variant['available-at']
                    from urllib.parse import urljoin
                    target = '/maven2/' + a['group'].replace('.', '/') + '/' + a['module'] + '/' + a['version'] + '/' + a['module'] + '-' + a['version'] + '.module'
                    if not fixed(a['version']) or urljoin(ORIGIN + path, a['url']) != ORIGIN + target:
                        raise ValueError('available_at_origin_or_identity')
                    self.grant(a['group'], a['module'], constraint(a['version']), source)
                for item in variant.get('files', []):
                    name = item['name']
                    if item['url'] != name or not TOKEN.fullmatch(name):
                        raise ValueError('external_module_file_URL')
                    if name.startswith(module + '-' + version) and name.endswith('.jar'):
                        self.files.setdefault(coordinate, set()).add(name)
                        pin = {k:item[k] for k in ('size','sha256','sha512','sha1','md5') if k in item}
                        if 'size' in pin and (not isinstance(pin['size'], int) or not 0 <= pin['size'] <= MAX_BODY):
                            raise ValueError('published_artifact_size_limit')
                        for k, length in (('sha256',64),('sha512',128),('sha1',40),('md5',32)):
                            if k in pin and not re.fullmatch('[0-9a-f]{' + str(length) + '}', pin[k]):
                                raise ValueError('invalid_published_artifact_checksum')
                        artifact_path = path.rsplit('/',1)[0] + '/' + name
                        value = {'declared':pin, 'metadata_source':source}
                        if artifact_path in self.file_pins and self.file_pins[artifact_path] != value:
                            # Different variants can declare the same exact file; their source body is identical.
                            if self.file_pins[artifact_path]['declared'] != pin:
                                raise ValueError('conflicting_published_artifact_pin')
                        self.file_pins[artifact_path] = value
                for d in variant.get('dependencies', []) + variant.get('dependencyConstraints', []):
                    v = d.get('version', {})
                    if set(v) - {'requires', 'strictly', 'prefers', 'rejects', 'branch'}:
                        raise ValueError('unknown_metadata_version_constraint')
                    self.grant(d['group'], d['module'], constraint(v.get('requires', ''), v.get('strictly', ''),
                        v.get('prefers', ''), v.get('rejects', []), v.get('branch')), source)
            return
        root = ET.fromstring(body)
        def child(node, name):
            return next((n for n in node if n.tag.rsplit('}', 1)[-1] == name), None)
        def text(node, name, default=''):
            found = child(node, name)
            return (found.text or '').strip() if found is not None else default
        props = {'project.groupId': group, 'pom.groupId': group, 'project.artifactId': module,
            'project.version': version, 'pom.version': version}
        pn = child(root, 'properties')
        if pn is not None:
            props.update({n.tag.rsplit('}', 1)[-1]: (n.text or '').strip() for n in pn})
        def expand(value):
            for _ in range(12):
                found = re.search(r'\$\{([^}]+)\}', value)
                if not found:
                    return value
                if found[1] not in props:
                    raise ValueError('unresolved_POM_property')
                value = value[:found.start()] + props[found[1]] + value[found.end():]
            raise ValueError('recursive_POM_property')
        parent = child(root, 'parent')
        if parent is not None:
            # Inherited properties/dependency-management require explicit support,
            # never guess them or turn an unversioned edge into a network grant.
            raise ValueError('unreviewed_POM_parent_inheritance')
        if expand(text(root, 'groupId')) != group or expand(text(root, 'artifactId')) != module or expand(text(root, 'version')) != version:
            raise ValueError('POM_identity_mismatch')
        dependencies = child(root, 'dependencies')
        if dependencies is not None:
            for d in dependencies:
                if text(d, 'scope') in ('test', 'provided', 'system') or text(d, 'optional') == 'true':
                    continue
                self.grant(expand(text(d, 'groupId')), expand(text(d, 'artifactId')),
                    constraint(expand(text(d, 'version'))), source)

    def event(self, value):
        try:
            self.sequence += 1
            if self.sequence > 4096:
                raise ValueError('HTTP_event_limit')
            json_new(self.work / 'evidence/broker/events' / ('%04d.json' % self.sequence), value)
        except BaseException:
            self.failure = 'HTTP_event_persistence_failed'
            self.budget.latch('broker_evidence_failed')
            raise

    def open_body(self, row):
        path = self.work / row['body']['path']
        if path.resolve(strict=True) != path or not path.is_relative_to(self.work):
            raise ValueError('captured_body_path_changed')
        stream = os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW), 'rb')
        try:
            before = os.fstat(stream.fileno())
            if (not stat.S_ISREG(before.st_mode) or before.st_uid != os.getuid() or before.st_nlink != 1
                    or before.st_size != row['body']['bytes']):
                raise ValueError('captured_body_stat_changed')
            h = hashlib.sha256()
            for block in iter(lambda: stream.read(65536), b''):
                self.budget.check(); h.update(block)
            after = os.fstat(stream.fileno())
            if (h.hexdigest() != row['body']['sha256'] or any(getattr(before,k) != getattr(after,k)
                    for k in ('st_dev','st_ino','st_size','st_mtime_ns','st_ctime_ns'))):
                raise ValueError('captured_body_bytes_changed')
            stream.seek(0)
            return stream
        except BaseException:
            stream.close()
            raise

    def snapshot(self, label):
        json_new(self.work / 'evidence/broker' / (label + '.json'), {'state': 'captured_not_native_acceptance',
            'origin': ORIGIN, 'responses': list(self.cache.values()), 'requests': self.sequence,
            'body_bytes': self.bytes, 'failure': self.failure, 'source_authority': self.sources,
            'published_artifact_pins': self.file_pins,
            'network_boundary': 'online Gradle configured only for literal loopback repository; not an OS-wide egress-denial claim'})


class Broker:
    def __init__(self, recorder):
        self.recorder = recorder
        outer = self
        class Handler(BaseHTTPRequestHandler):
            protocol_version = 'HTTP/1.1'
            def setup(self):
                self.request.settimeout(30)
                super().setup()
            def log_message(self, *args):
                pass
            def send_error(self, code, message=None, explain=None):
                recorder.failure = 'HTTP_protocol_refused'
                try:
                    self.send_response(code); self.send_header('Content-Length','0')
                    self.send_header('Connection','close'); self.end_headers()
                finally:
                    recorder.event({'method':self.command if getattr(self,'command',None) in ('GET','HEAD') else 'OTHER',
                        'response':None,'HTTP_status':code,'delivered_body_bytes':0,'complete_GET':False,
                        'generated_error_body_sha256':digest(b''),'failure':recorder.failure})
                    self.close_connection = True
            def do_GET(self):
                self.handle_body(False)
            def do_HEAD(self):
                self.handle_body(True)
            def do_CONNECT(self):
                recorder.failure = 'HTTP_method_refused'
                self.send_response(405); self.send_header('Content-Length', '0'); self.end_headers()
                recorder.event({'method': self.command, 'response': None, 'HTTP_status':405,
                    'delivered_body_bytes':0, 'complete_GET':False, 'generated_error_body_sha256':digest(b''),
                    'failure':recorder.failure})
                self.close_connection = True
            do_POST = do_CONNECT
            do_PUT = do_CONNECT
            do_DELETE = do_CONNECT
            do_OPTIONS = do_CONNECT
            do_PATCH = do_CONNECT
            do_TRACE = do_CONNECT
            def handle_body(self, head):
                sent, row, http_status, started, partial_unknown = 0, None, 502, False, False
                actual_hash = hashlib.sha256()
                try:
                    self.connection.settimeout(30)
                    if (self.client_address[0] != '127.0.0.1' or self.headers.get('Host') != outer.authority
                            or len(self.headers.get_all('Host', [])) != 1
                            or len(self.headers) > 32 or sum(len(k) + len(v) for k, v in self.headers.items()) > 8192
                            or self.headers.get('Content-Length') not in (None, '0') or self.headers.get('Transfer-Encoding')
                            or any(self.headers.get(k) is not None for k in ('Authorization', 'Proxy-Authorization', 'Cookie', 'Range'))):
                        raise ValueError('HTTP_authority_or_header_refused')
                    row = recorder.obtain(self.path)
                    with recorder.open_body(row) as stream:
                        self.send_response(row['status']); self.send_header('Content-Length', str(row['body']['bytes']))
                        self.send_header('Connection', 'close'); self.end_headers()
                        http_status, started = row['status'], True
                        if not head:
                            for block in iter(lambda: stream.read(65536), b''):
                                recorder.budget.check()
                                self.wfile.write(block); sent += len(block); actual_hash.update(block)
                            self.wfile.flush()
                            if actual_hash.hexdigest() != row['body']['sha256']:
                                raise ValueError('delivered_bytes_differ_from_captured_origin')
                except (BrokenPipeError, ConnectionResetError):
                    partial_unknown = True
                except BaseException as error:
                    recorder.failure = type(error).__name__ + ':' + (str(error) if isinstance(error, ValueError) else 'origin_or_IO_failure')
                    if not started:
                        try:
                            self.send_response(502); self.send_header('Content-Length', '0')
                            self.send_header('Connection', 'close'); self.end_headers()
                        except OSError:
                            pass
                finally:
                    try:
                        recorder.event({'method': 'HEAD' if head else 'GET', 'response': row,
                            'HTTP_status':http_status,
                            'generated_error_body_sha256':digest(b'') if row is None else None,
                            'delivered_body_bytes': sent, 'confirmed_body_sha256':actual_hash.hexdigest(),
                            'partial_write_bytes_unknown':partial_unknown,
                            'complete_GET':not head and row is not None and started and not partial_unknown
                                and recorder.failure is None and sent == row['body']['bytes'] and actual_hash.hexdigest() == row['body']['sha256'],
                            'failure': recorder.failure})
                    except BaseException:
                        recorder.failure = 'HTTP_event_persistence_failed'
                        recorder.budget.latch('broker_evidence_failed')
                    self.close_connection = True
        self.server = HTTPServer(('127.0.0.1', 0), Handler)
        self.authority = '127.0.0.1:' + str(self.server.server_port)
        self.url = 'http://' + self.authority + '/maven2/'
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)

    def __enter__(self):
        self.thread.start()
        return self

    def __exit__(self, *args):
        self.server.shutdown(); self.server.server_close(); self.thread.join(timeout=35)
        if self.thread.is_alive():
            raise ValueError('broker_did_not_stop')
