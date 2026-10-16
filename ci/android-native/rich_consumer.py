"""Independent accepted-R12 source catalog; never enroll a runtime selection."""
import base64
import hashlib
import json
import os
from pathlib import Path
import stat

CATALOG_SHA = 'db185c038d3c490e2b1edee4592bac448b9f2765d092aa1b8c079a3cb1bc1dbe'
SCRIPT_SHA = '3f53eb18ea0731665edc2f00c111743fef37dcec011c931344110c9946912fa3'


def sha(body):
    return hashlib.sha256(body).hexdigest()


def read(path, limit=2*1024**2):
    path = Path(path)
    if path.resolve(strict=True) != path:
        raise ValueError('rich input canonical file required')
    with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW), 'rb') as f:
        a = os.fstat(f.fileno())
        if not stat.S_ISREG(a.st_mode) or a.st_nlink != 1 or a.st_uid != os.getuid() or a.st_size > limit:
            raise ValueError('rich input owned bounded single-link required')
        body = f.read(limit+1); z = os.fstat(f.fileno())
    identity = lambda s: (s.st_dev,s.st_ino,s.st_size,s.st_mtime_ns,s.st_ctime_ns)
    if len(body)>limit or identity(a)!=identity(z) or identity(z)!=identity(path.lstat()):
        raise ValueError('rich input changed during read')
    return body


def approved(here):
    body = read(Path(here)/'rich-catalog.json')
    script = read(Path(here)/'rich-consumer.gradle')
    if sha(body)!=CATALOG_SHA or sha(script)!=SCRIPT_SHA:
        raise ValueError('independent rich catalog/script pin differs')
    catalog = json.loads(body)
    if (catalog['schema']!=1 or catalog['Gradle']!='9.3.1' or len(catalog['rich'])!=3
            or len(catalog['configurations'])!=2
            or sum(len(c['artifacts']) for c in catalog['configurations'])!=18
            or catalog['accepted_provenance']['online_offline_equal'] is not True):
        raise ValueError('accepted finite3/source18 identity required')
    return catalog, body, script


def embedded(here):
    _, body, script = approved(here)
    return {'catalog_sha256': CATALOG_SHA, 'catalog_base64': base64.b64encode(body).decode('ascii'),
            'script_sha256': SCRIPT_SHA, 'script_base64': base64.b64encode(script).decode('ascii')}


def canonical(value):
    return json.dumps(value,sort_keys=True,separators=(',',':'))


def validate_report(catalog, report):
    expected = next((c for c in catalog['configurations'] if c['name']==report.get('configuration')),None)
    if (expected is None or report.get('catalog_sha256')!=CATALOG_SHA
            or report.get('project')!=catalog['scope']['project']
            or report.get('scope_sources')!=catalog['scope']['sources']
            or report.get('no_force_or_substitution') is not True):
        raise ValueError('rich result scope/source/identity differs')
    if sorted(map(canonical,report['edges']))!=sorted(map(canonical,expected['edges'])):
        raise ValueError('rich result fourth/parent/constraint/selected edge differs')
    fields = ('id','filename','bytes','sha256','variant')
    project = lambda rows: sorted(canonical({k:r[k] for k in fields}) for r in rows)
    if project(report['artifacts'])!=project(expected['artifacts']):
        raise ValueError('rich selected artifact18 set/body differs')
    return expected


def inspect_refusal(catalog, value):
    """Explain bounded failed evidence; never convert it to an accepted report."""
    config = next((c for c in catalog['configurations'] if c['name']==value.get('configuration')),None)
    if (value.get('kind')!='rich-artifact-refusal-v1' or value.get('accepted') is not False
            or value.get('catalog_sha256')!=CATALOG_SHA or config is None
            or value.get('project')!=catalog['scope']['project']):
        raise ValueError('untrusted refusal scope/catalog')
    if value.get('snapshot_omitted')=='bounded-evidence-cap':
        return dict(accepted=False,configuration=config['name'],evidence_complete=False,
                    actual_artifact_count=value['actual_artifact_count'])
    expected=[{k:v for k,v in a.items() if k!='origin'} for a in config['artifacts']]
    if (value.get('scope_sources')!=catalog['scope']['sources']
            or value.get('no_force_or_substitution') is not True
            or sorted(map(canonical,value['edges']))!=sorted(map(canonical,config['edges']))
            or sorted(map(canonical,value['expected_artifacts']))!=sorted(map(canonical,expected))
            or len(value['actual_artifacts'])>64):
        raise ValueError('refusal does not prove unchanged pre-artifact admission')
    actual=value['actual_artifacts']; fields=['id','filename','bytes','sha256','variant']
    if any(set(a)!=set(fields) for a in actual):
        raise ValueError('unexpected refusal artifact fields')
    differences=[]; used=set()
    for wanted in expected:
        matches=[(i,a) for i,a in enumerate(actual) if a['id']==wanted['id']]
        if len(matches)!=1:
            differences.append(dict(id=wanted['id'],fields=['missing-or-duplicate-component']))
            continue
        i,observed=matches[0];used.add(i)
        changed=[k for k in fields if observed[k]!=wanted[k]]
        if changed: differences.append(dict(id=wanted['id'],fields=changed))
    extras=len(actual)-len(used)
    if not differences and extras==0:
        raise ValueError('equal artifacts are not an artifact refusal')
    return dict(accepted=False,configuration=config['name'],evidence_complete=True,
                differences=differences,extra_rows=extras)


def reports(work, here, phase, require_all):
    catalog,_,_=approved(here)
    root = Path(work)/'evidence/rich-consumed'/phase
    values, pins = [], []
    if root.exists():
        if root.resolve(strict=True)!=root or not root.is_dir():
            raise ValueError('rich report root alias')
        for path in sorted(root.iterdir()):
            body=read(path); value=json.loads(body); spec=validate_report(catalog,value)
            if path.name!=spec['name']+'.json':
                raise ValueError('rich report filename differs')
            values.append(value);pins.append({'path':str(path.relative_to(work)),'sha256':sha(body)})
    names=[x['configuration'] for x in values]
    if len(set(names))!=len(names) or (require_all and set(names)!={c['name'] for c in catalog['configurations']}):
        raise ValueError('complete actual rich configurations required')
    return catalog,values,pins


def metadata_sources(catalog):
    # Generator metadata is source-only, kept outside the selected18 universe.
    row=catalog['scope']['KGP_published_metadata']
    return catalog['metadata_sources']+[dict(coordinate='org.jetbrains.kotlin:kotlin-gradle-plugin:2.4.0',
        filename='kotlin-gradle-plugin-2.4.0.module',bytes=row['bytes'],sha256=row['sha256'],
        origin='https://repo.maven.apache.org'+row['path'])]


def verify_origin_rows(catalog, actual):
    rows={}
    for row in actual['artifacts']:
        key=(row['coordinate'],row['filename'])
        if key in rows and rows[key]!=row:
            raise ValueError('ambiguous actual Maven origin')
        rows[key]=row
    selected=[]
    for config in catalog['configurations']:
        for expected in config['artifacts']:
            ident=expected['id'];coordinate=':'.join(ident[k] for k in ['group','module','version'])
            row=rows.get((coordinate,expected['filename']))
            if (row is None or any(row[k]!=expected[k] for k in ['bytes','sha256'])
                    or row['observed_sources']!=[expected['origin']]):
                raise ValueError('actual rich artifact origin/body differs')
            selected.append(coordinate)
    # All present selected metadata is independently pinned; a changed POM or
    # module cannot borrow an unchanged JAR. Proof-only KMP parent grants none.
    known={(x['coordinate'],x['filename']):x for x in metadata_sources(catalog)}
    seen=set()
    for key,row in rows.items():
        if key[0] in selected and key[1].endswith(('.pom','.module')):
            expected=known.get(key)
            if expected is None or any(row[k]!=expected[k] for k in ['bytes','sha256']) or row['observed_sources']!=[expected['origin']]:
                raise ValueError('actual selected metadata source differs')
            seen.add(key[0])
    if seen!=set(selected):
        raise ValueError('selected component lacks actual pinned metadata')
    mandatory=[('org.jetbrains.kotlin:kotlin-gradle-plugin:2.4.0','kotlin-gradle-plugin-2.4.0.module'),
               ('org.bouncycastle:bcpg-jdk18on:1.80','bcpg-jdk18on-1.80.pom'),
               ('org.bouncycastle:bcpkix-jdk18on:1.80','bcpkix-jdk18on-1.80.pom')]
    for key in mandatory:
        row,expected=rows.get(key),known[key]
        if row is None or any(row[k]!=expected[k] for k in ['bytes','sha256']) or row['observed_sources']!=[expected['origin']]:
            raise ValueError('actual generator/parent metadata provenance differs')
    return selected


def bind_origins(work, here, write_json):
    work=Path(work);catalog,_,pins=reports(work,here,'metadata',True)
    origin=read(work/'evidence/dependency-origin-audit.json')
    selected=verify_origin_rows(catalog,json.loads(origin))
    write_json(work/'evidence/rich-origin-binding.json',{'catalog_sha256':CATALOG_SHA,
        'origin_receipt_sha256':sha(origin),'actual_metadata_reports':pins,
        'checked_artifact_rows':len(selected),'runtime_or_App_claim':False})


def require_origin_binding(work,here):
    work=Path(work);catalog,_,pins=reports(work,here,'metadata',True)
    value=json.loads(read(work/'evidence/rich-origin-binding.json'))
    body=read(work/'evidence/dependency-origin-audit.json')
    if (value['catalog_sha256']!=CATALOG_SHA or value['origin_receipt_sha256']!=sha(body)
            or value['actual_metadata_reports']!=pins or value['checked_artifact_rows']!=18):
        raise ValueError('independent rich origin binding changed')
    verify_origin_rows(catalog,json.loads(body))


def check_apk_reports(work,here):
    require_origin_binding(work,here)
    _,values,_=reports(work,here,'apk',False)
    return [v['configuration'] for v in values]  # unused configs are not claimed consumed
