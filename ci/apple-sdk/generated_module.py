"""Finite metadata warm closure and post-tidy provenance; never edits real bind locks."""
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import stat

HERE = Path(__file__).resolve().parent
TEMPLATE = 'generated-module-before.mod'
TEMPLATE_SHA = '05e30d386898edc45e01458e5dcd79d166965d89cb4537a436ed5beeaba21088'
SUM_SHA = '9d8d7d82e6702218bfe964f7005b3b858d44b41efca66c89d70d02aab5539d9f'
CORE_SUM = '8eb9b73313a7f774bfef48814bfc1b73e7ff7f57bd37f9d40555e8d24f70262f'
CORE = 'github.com/openimsdk/openim-sdk-core/v3'
PROTOCOL = 'github.com/OpenIMSDK/protocol'
CORE_GENERATED_VERSION = 'v3.0.0-00010101000000-000000000000'
REQUIRE_UPDATES = (
    ('cloud.google.com/go/compute', 'v1.19.1', 'v1.20.1'),
    ('github.com/google/go-cmp', 'v0.5.9', 'v0.6.0'),
    ('github.com/stretchr/objx', 'v0.1.0', 'v0.5.0'),
    ('golang.org/x/crypto', 'v0.16.0', 'v0.58.0'),
    ('golang.org/x/net', 'v0.19.0', 'v0.61.0'),
    ('golang.org/x/oauth2', 'v0.7.0', 'v0.8.0'),
    ('golang.org/x/term', 'v0.15.0', 'v0.47.0'),
    ('golang.org/x/xerrors', 'v0.0.0-20191204190536-9bdfabe68543',
     'v0.0.0-20200804184101-5ec99f83aff1'),
)


def digest(body):
    return hashlib.sha256(body).hexdigest()


def read(path, owned=False):
    path = Path(path)
    if path.resolve(strict=True) != path:
        raise ValueError('module canonical input required')
    with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW), 'rb') as f:
        a = os.fstat(f.fileno())
        if (not stat.S_ISREG(a.st_mode) or a.st_nlink != 1 or a.st_size > 1024**2
                or (owned and a.st_uid != os.getuid())):
            raise ValueError('module bounded single-link input required')
        body = f.read(1024**2+1); b = os.fstat(f.fileno())
    identity = lambda s: (s.st_dev, s.st_ino, s.st_size, s.st_mode, s.st_nlink,
                          s.st_uid, s.st_gid, s.st_mtime_ns, s.st_ctime_ns)
    if (len(body) > 1024**2 or identity(a) != identity(b) or identity(b) != identity(path.lstat())
            or path.resolve(strict=True) != path):
        raise ValueError('module input changed')
    return body


def cache_escape(value, version=False):
    # Called only after finite catalogue admission; implement official escapeString,
    # not a replacement for module.CheckPath on arbitrary user input.
    if (not value or '!' in value or not value.isascii()
            or not re.fullmatch(r'[A-Za-z0-9._~+/-]+', value)
            or any(p in ('', '.', '..') for p in value.split('/'))
            or (version and '/' in value)):
        raise ValueError('finite module cache escape refused')
    return ''.join('!'+c.lower() if 'A' <= c <= 'Z' else c for c in value)


def go_mod_h1(body):
    # cmd/go modfetch.goModSum -> dirhash.Hash1(single file named go.mod).
    line = (hashlib.sha256(body).hexdigest()+'  go.mod\n').encode('ascii')
    return 'h1:'+base64.b64encode(hashlib.sha256(line).digest()).decode('ascii')


def cache_gomod(cache, path, version):
    expected = catalogue()[3].get((path, version+'/go.mod'))
    if expected is None:
        raise ValueError('module identity outside independent cache catalogue')
    cache = Path(cache)
    if cache.resolve(strict=True) != cache or not cache.is_dir() or cache.stat().st_uid != os.getuid():
        raise ValueError('fixed owned module cache required')
    relative = cache_escape(path)+'/@v/'+cache_escape(version, True)+'.mod'
    file = cache/'cache/download'/relative
    if not file.is_relative_to(cache):
        raise ValueError('module cache boundary refused')
    body = read(file, owned=True)
    if go_mod_h1(body) != expected:
        raise ValueError('cached go.mod independent h1 differs')
    return {'path': path, 'version': version, 'cache_relative': 'cache/download/'+relative,
            'bytes': len(body), 'sha256': digest(body), 'go_mod_h1': expected}


def catalogue():
    before, after, sums = (read(HERE/n) for n in
                          (TEMPLATE, 'generated-module-after.mod', 'generated-module-acquired.sum'))
    if digest(before) != TEMPLATE_SHA or digest(sums) != SUM_SHA:
        raise ValueError('independent generated module catalogue differs')
    expected = before
    for module, old, new in REQUIRE_UPDATES:
        line = ('\t'+module+' '+old+'\n').encode()
        if expected.count(line) != 1:
            raise ValueError('reviewed require source line missing or duplicated')
        expected = expected.replace(line, ('\t'+module+' '+new+'\n').encode(), 1)
    if after != expected:
        raise ValueError('acquired template differs outside exact eight updates')
    rows = sum_rows(sums)
    if len(rows) != 610 or len({(p, v.removesuffix('/go.mod')) for p, v in rows}) != 359:
        raise ValueError('finite generated module inventory differs')
    return before, after, sums, rows


def sum_rows(body):
    result = {}
    for line in body.decode('ascii').splitlines():
        fields = line.split()
        if (len(fields) != 3 or not re.fullmatch(r'h1:[A-Za-z0-9+/]{43}=', fields[2])
                or (fields[0], fields[1]) in result):
            raise ValueError('module checksum syntax or duplicate')
        result[fields[0], fields[1]] = fields[2]
    return result


def expected_projected_acquisition(before, core, protocol):
    """Reuse the reviewed Android eight-update validator without its Job/imports."""
    template = (Path(__file__).resolve().parent / TEMPLATE).read_bytes()
    if hashlib.sha256(template).hexdigest() != TEMPLATE_SHA:
        raise ValueError('reviewed projection template differs')
    normalized = before
    for module, actual, placeholder in [
        ('github.com/openimsdk/openim-sdk-core/v3', core, '/__LUOBO_JOB_CORE__'),
        ('github.com/OpenIMSDK/protocol', protocol, '/__LUOBO_JOB_PROTOCOL__'),
    ]:
        path = str(actual)
        if not Path(path).is_absolute() or any(c.isspace() or c in '\\"' for c in path):
            raise ValueError('exact same-job absolute replace path required')
        line = ('replace ' + module + ' => ' + path + '\n').encode()
        if normalized.count(line) != 1:
            raise ValueError('exact same-job replace line missing or duplicated')
        normalized = normalized.replace(line, ('replace ' + module + ' => ' + placeholder + '\n').encode(), 1)
    if normalized != template:
        raise ValueError('original97-require projection differs from real r1')
    expected = before
    for module, old, new in REQUIRE_UPDATES:
        source = ('\t' + module + ' ' + old + '\n').encode()
        if expected.count(source) != 1:
            raise ValueError('reviewed require source line missing or duplicated')
        expected = expected.replace(source, ('\t' + module + ' ' + new + '\n').encode(), 1)
    return expected


def render(core, protocol):
    before = catalogue()[0]
    for marker, path in [('/__LUOBO_JOB_CORE__', core), ('/__LUOBO_JOB_PROTOCOL__', protocol)]:
        path = Path(path)
        if path.resolve(strict=True) != path or not path.is_dir() or path.stat().st_uid != os.getuid():
            raise ValueError('module owned replacement directory required')
        before = before.replace(marker.encode(), str(path).encode(), 1)
    expected_projected_acquisition(before, core, protocol)
    return before


def validate_acquired(before, after, sums, core, protocol):
    if after != expected_projected_acquisition(before, core, protocol) or sums != catalogue()[2]:
        raise ValueError('module acquisition changed outside exact eight and independent sum')


def validate_generated(module, sums, graph, cwd, core, protocol, cache):
    """Actual tidy may prune; every surviving identity/hash must have independent provenance."""
    approved = catalogue()[3]; actual = sum_rows(sums)
    if not actual or any(approved.get(k) != h for k, h in actual.items()):
        raise ValueError('actual module checksum outside finite provenance')
    local = {CORE: (str(core), CORE_GENERATED_VERSION), PROTOCOL: (str(protocol), 'v0.0.40')}
    lines = module.decode().splitlines(); required = {}; replaces = {}; in_require = False
    for line in lines:
        if not line or line in ('module gobind', 'go 1.27'):
            continue
        if line == 'require (' and not in_require:
            in_require = True; continue
        if line == ')' and in_require:
            in_require = False; continue
        match = re.fullmatch(r'(?:\t|require )([^\s]+) ([^\s]+)(?: // indirect)?', line)
        if match and (in_require == line.startswith('\t')):
            p, v = match.groups()
            if p in required or (p in local and v != local[p][1]) or (p not in local and (p, v+'/go.mod') not in approved):
                raise ValueError('actual module require outside finite provenance')
            required[p] = v; continue
        match = re.fullmatch(r'replace ([^\s]+) => ([^\s]+)', line)
        if match and not in_require:
            p, path = match.groups()
            if p not in local or p in replaces or path != local[p][0]:
                raise ValueError('actual module replacement differs')
            replaces[p] = path; continue
        raise ValueError('actual module directive refused')
    if (in_require or lines.count('module gobind') != 1 or lines.count('go 1.27') != 1
            or set(replaces) != set(local) or required.get(CORE) != CORE_GENERATED_VERSION):
        raise ValueError('actual module identity incomplete')
    selected = validate_graph(graph, cwd, core, protocol, required, cache)
    return {'state': 'passed_finite_actual_tidy_provenance', 'sum_rows': len(actual),
            **selected, 'module_sha256': digest(module), 'sum_sha256': digest(sums),
            'graph_sha256': digest(graph.encode()), 'independent_catalogue_sha256': SUM_SHA,
            'metadata_is_not_full_bind_lock': True}


def validate_graph(graph, cwd, core, protocol, required, cache):
    approved = catalogue()[3]
    local = {CORE: (str(core), CORE_GENERATED_VERSION), PROTOCOL: (str(protocol), 'v0.0.40')}
    decoder = json.JSONDecoder(); text = graph; seen = {}; main = 0; cache_mods = []
    while text.strip():
        if len(seen) >= 512:
            raise ValueError('module graph cap')
        value, end = decoder.raw_decode(text.lstrip()); text = text.lstrip()[end:]
        p, v = value.get('Path'), value.get('Version')
        if p in seen or value.get('Error'):
            raise ValueError('module graph duplicate or error')
        seen[p] = v
        if value.get('Main') is True:
            if p != 'gobind' or v is not None or value.get('Dir') != str(cwd) or value.get('GoMod') != str(Path(cwd)/'go.mod'):
                raise ValueError('actual module graph root differs')
            main += 1; continue
        replacement = value.get('Replace')
        if p in local:
            path, version = local[p]
            if (v != version or not isinstance(replacement, dict) or replacement.get('Path') != path
                    or replacement.get('Dir') != path or replacement.get('GoMod') != str(Path(path)/'go.mod')
                    or value.get('Dir') != path or value.get('GoMod') != str(Path(path)/'go.mod')
                    or replacement.get('Version') or replacement.get('Error')):
                raise ValueError('module graph local replacement differs')
        else:
            if (replacement is not None or (p, str(v)+'/go.mod') not in approved
                    or ('GoModSum' in value and approved[p, v+'/go.mod'] != value['GoModSum'])
                    or ('Sum' in value and ((p, v) not in approved or approved[p, v] != value['Sum']))):
                raise ValueError('module graph identity or hash outside finite provenance')
            # Optional Go JSON fields are not evidence of an unchecked hash.
            # Read only the controller-bound cache path, never value['GoMod'].
            cache_mods.append(cache_gomod(cache, p, v))
    if main != 1 or any(seen.get(p) != v for p, v in required.items()):
        raise ValueError('actual module selected graph incomplete')
    return {'selected_modules': len(seen), 'cache_mods': cache_mods}


def prewarm(w, core, protocol, go, env, command, mkdir, put, record):
    """Setup-only acquisition in the same owned cache; command is cloud's real dispatcher."""
    output = w/'setup/generated-module'; evidence = w/'evidence/generated-module'
    mkdir(output); mkdir(evidence)
    before = render(core, protocol); sums = catalogue()[2]
    put(output/'go.mod', before); put(output/'go.sum', sums)
    baseline = {n: digest(read(core/n)) for n in ('go.mod', 'go.sum')}
    if baseline['go.sum'] != CORE_SUM:
        raise ValueError('core8eb9 required before generated warm')
    def snapshot(label):
        values = {n: read(output/n) for n in ('go.mod', 'go.sum')}
        for n, body in values.items():
            put(evidence/label/n, body)
        record(evidence/label/'manifest.json', {n: {'bytes': len(b), 'sha256': digest(b)} for n, b in values.items()})
        return values
    target = {**env, 'GOOS': 'ios', 'GOARCH': 'arm64', 'CGO_ENABLED': '1', 'GOFLAGS': ''}
    def run(label, args, process_env):
        if {n: digest(read(core/n)) for n in baseline} != baseline:
            raise ValueError('core lock changed before generated warm command')
        snapshot(label+'-before')
        try:
            return command(w, label, args, output, process_env)
        finally:
            snapshot(label+'-after')
            if {n: digest(read(core/n)) for n in baseline} != baseline:
                raise ValueError('core lock changed after generated warm command')
    run('generated-acquire', [go, 'mod', 'download', 'all'], target)
    validate_acquired(before, read(output/'go.mod'), read(output/'go.sum'), core, protocol)
    acquired = {n: read(output/n) for n in ('go.mod', 'go.sum')}
    offline = {**target, 'GOFLAGS': '-mod=readonly', 'GOPROXY': 'off', 'GOSUMDB': 'off'}
    for label, args in [('generated-verify', [go, 'mod', 'verify']),
                        ('generated-graph', [go, 'list', '-m', '-json', 'all'])]:
        text = run(label, args, offline)
        if {n: read(output/n) for n in acquired} != acquired:
            raise ValueError('warm acquired lock changed during offline verification')
    required = dict(re.findall(rb'^\t([^\s]+) ([^\s]+)$', acquired['go.mod'], re.M))
    selected = validate_graph(text, output, core, protocol,
                              {p.decode(): v.decode() for p, v in required.items()}, Path(env['GOMODCACHE']))
    record(evidence/'ready.json', {'state': 'passed_setup_metadata_only', 'original_go_mod_sha256': digest(before),
           'acquired_go_mod_sha256': digest(acquired['go.mod']), 'sum_sha256': SUM_SHA,
           **selected, 'graph_sha256': digest(text.encode()),
           'core_locks': baseline, 'cache': env['GOMODCACHE'], 'actual_bind_lock_installed': False})
