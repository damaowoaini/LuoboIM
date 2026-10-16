#!/usr/bin/env python3
"""Manual SDK-only Apple cloud acquisition + unchanged R1 bind/link gates."""
import argparse
import base64
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import ssl
import signal
import stat
import subprocess
import sys
import tarfile
import urllib.parse
import urllib.request
import zipfile

HERE=Path(__file__).resolve().parent
REPO=HERE.parents[1]
IDENTITY='apple-sdk-only-cloud-arm64-r1'
GIB=1024**3


def sha(p):
    h=hashlib.sha256()
    with Path(p).open('rb') as f:
        for b in iter(lambda:f.read(1024**2),b''):h.update(b)
    return h.hexdigest()


def mkdir(p):
    p=Path(p)
    if not p.exists():mkdir(p.parent);p.mkdir(mode=0o700);sync(p.parent)
    if p.resolve()!=p or not p.is_dir() or p.stat().st_uid!=os.getuid():raise ValueError('owned canonical directory')


def sync(p):
    fd=os.open(p,os.O_RDONLY|os.O_DIRECTORY)
    try:os.fsync(fd)
    finally:os.close(fd)


def put(p,b,mode=0o600):
    mkdir(p.parent)
    with os.fdopen(os.open(p,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,mode),'wb') as f:
        f.write(b);f.flush();os.fsync(f.fileno())
    sync(p.parent)


def record(p,value):put(p,(json.dumps(value,sort_keys=True,indent=2)+'\n').encode())
def load(p,name):
    spec=importlib.util.spec_from_file_location(name,p);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m


def name(s):
    if not s or s.startswith('/') or '\\' in s or any(x in ('','..','.') for x in s.split('/')) or any(ord(c)<32 for c in s):raise ValueError('archive path refused')
    return s


def metadata():
    registry=json.loads((HERE/'registry.json').read_text())
    if registry['identity']!=IDENTITY:raise ValueError('cloud controller identity')
    for row in registry['files']:
        p=HERE/name(row['path'])
        if p.resolve()!=p or p.stat().st_size!=row['bytes'] or sha(p)!=row['sha256']:raise ValueError('cloud source pin differs')
    for row in registry['repository_files']:
        p=REPO/name(row['path'])
        if p.resolve()!=p or p.stat().st_size!=row['bytes'] or sha(p)!=row['sha256']:raise ValueError('cloud workflow pin differs')
    spec=json.loads((HERE/'portable-inputs.json').read_text())
    for row in json.loads((HERE/'public-source-inputs.json').read_text())['files']:
        p=REPO/name(row['path'])
        if p.resolve()!=p or not p.is_file() or p.stat().st_size!=row['bytes'] or sha(p)!=row['sha256']:raise ValueError('public SDK source input differs')
    return spec


def free(p):
    s=os.statvfs(p);return s.f_bavail*s.f_frsize


def work_path(text,new):
    p=Path(text);parent=Path(os.environ['RUNNER_TEMP']).resolve()
    if (not p.is_absolute() or p.parent!=parent or p.name!='luobo-apple-sdk-r1' or parent.stat().st_uid!=os.getuid()
            or p.is_symlink() or (new and p.exists()) or (not new and (not p.is_dir() or p.resolve()!=p or p.stat().st_mode&0o077))):raise ValueError('fresh fixed RUNNER_TEMP root required')
    return p


def crc_table():
    out=[]
    for i in range(256):
        for _ in range(8):i=(i>>1)^0x82F63B78 if i&1 else i>>1
        out.append(i)
    return out


def crc_update(value,body):
    table=CRC_TABLE
    for b in body:value=table[(value^b)&255]^(value>>8)
    return value
CRC_TABLE=crc_table()


def check_download(label,spec,result):
    if result['bytes']!=spec['bytes']:raise ValueError('download incomplete')
    if label=='go' and result['sha256']!=spec['sha256']:raise ValueError('official Go SHA differs')
    if label=='engine' and (result['md5_base64']!=spec['md5_base64'] or result['crc32c_base64']!=spec['crc32c_base64'] or result['generation']!=spec['generation']):raise ValueError('engine published checksum differs')
    if label not in ('go','engine'):raise ValueError('finite download kind')


class Redirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self,request,fp,code,msg,headers,newurl):
        # The Go release endpoint alone may redirect to its exact official body.
        original=getattr(request,'original_cloud_url',request.full_url)
        expected={'https://go.dev/dl/go1.27.2.darwin-arm64.tar.gz':'https://dl.google.com/go/go1.27.2.darwin-arm64.tar.gz'}
        if expected.get(original)!=newurl:raise ValueError('unreviewed download redirect')
        result=super().redirect_request(request,fp,code,msg,headers,newurl);result.original_cloud_url=original;return result


def download(w,label,spec):
    approved=json.loads((HERE/'portable-inputs.json').read_text())
    if spec!=approved[label] or label not in ('go','engine'):raise ValueError('finite download source required')
    target=w/'setup/archives'/(label+('.tar.gz' if label=='go' else '.zip'))
    h=hashlib.sha256();md5=hashlib.md5();crc=0xffffffff;size=0
    opener=urllib.request.build_opener(urllib.request.ProxyHandler({}),Redirect(),urllib.request.HTTPSHandler(context=ssl.create_default_context()))
    request=urllib.request.Request(spec['url'],headers={'User-Agent':'LuoboIM-AppleSDK/1'})
    with opener.open(request,timeout=60) as response:
        if response.status!=200 or int(response.headers.get('Content-Length','-1'))!=spec['bytes']:raise ValueError('official download size/status')
        if label=='engine' and response.headers.get('x-goog-generation')!=spec['generation']:raise ValueError('fixed engine object generation')
        mkdir(target.parent)
        with target.open('xb') as out:
            for b in iter(lambda:response.read(1024**2),b''):
                size+=len(b)
                if size>spec['bytes']:raise ValueError('download body cap')
                out.write(b);h.update(b);md5.update(b)
                if label=='engine':crc=crc_update(crc,b)
            out.flush();os.fsync(out.fileno())
        actual_url=response.url
    result=dict(bytes=size,sha256=h.hexdigest(),url=actual_url,official_SHA256_preexisting=label=='go',generation=spec.get('generation'),
        md5_base64=base64.b64encode(md5.digest()).decode(),crc32c_base64=base64.b64encode(((crc^0xffffffff)&0xffffffff).to_bytes(4,'big')).decode())
    record(w/'evidence'/('download-'+label+'-actual.json'),result)
    check_download(label,spec,result)
    record(w/'evidence'/('download-'+label+'.json'),dict(**result,verified=True))
    return target


def extract_go(archive,destination):
    rows=[]
    with tarfile.open(archive,'r:gz') as t:
        members=t.getmembers()
        if len(members)>30000 or sum(x.size for x in members)>512*1024**2:raise ValueError('Go archive cap')
        seen=set()
        for x in members:
            n=name(x.name.rstrip('/'))
            if not (n.startswith('go/') or n=='go' and x.isdir()) or n in seen or not (x.isfile() or x.isdir()):raise ValueError('Go archive member refused')
            seen.add(n)
            if x.isdir():mkdir(destination/n)
            else:
                b=t.extractfile(x).read();put(destination/n,b,0o700 if x.mode&0o111 else 0o600)
                rows.append(dict(path=n,bytes=len(b),sha256=hashlib.sha256(b).hexdigest()))
    return rows


def extract_engine(archive,destination):
    expected=json.loads((HERE/'flutter-engine-members.json').read_text())['files'];wanted={x['path']:x for x in expected};found={}
    with zipfile.ZipFile(archive) as z:
        entries=z.infolist()
        if len(entries)>4096 or sum(x.file_size for x in entries)>GIB:raise ValueError('engine ZIP budget')
        seen=set()
        for x in entries:
            n=name(x.filename.rstrip('/'))
            if n in seen or stat.S_ISLNK(x.external_attr>>16):raise ValueError('engine ZIP alias/duplicate')
            seen.add(n)
        if z.testzip() is not None:raise ValueError('engine ZIP CRC')
        for x in entries:
            prefix='Flutter.xcframework/'
            if x.filename.startswith(prefix) and x.filename[len(prefix):] in wanted:
                rel=x.filename[len(prefix):];body=z.read(x);row=wanted[rel]
                if len(body)!=row['bytes'] or hashlib.sha256(body).hexdigest()!=row['sha256']:raise ValueError('fixed original Flutter member differs')
                put(destination/'Flutter.xcframework'/rel,body);found[rel]=row
    if set(found)!=set(wanted):raise ValueError('complete R1 Flutter50 required')
    return expected


def env(w,go,developer):
    # Process-only task paths; never modify machine/user environment settings.
    return dict(PATH=str(go/'bin')+':'+str(w/'setup/mobile-bin')+':/usr/bin:/bin',HOME=os.environ['HOME'],TMPDIR=str(w/'setup/tmp'),
        DEVELOPER_DIR=str(developer),GOROOT=str(go),GOPATH=str(w/'setup/gopath'),GOMODCACHE=str(w/'setup/modcache'),GOCACHE=str(w/'setup/host-go-cache'),
        GOBIN=str(w/'setup/mobile-bin'),GOENV='off',GOAUTH='off',GOWORK='off',GOTOOLCHAIN='local',GOPROXY='https://proxy.golang.org',GOSUMDB='sum.golang.org',
        GOMAXPROCS='2',GOFLAGS='',CGO_ENABLED='1',LANG='en_US.UTF-8',GIT_CONFIG_NOSYSTEM='1',GIT_CONFIG_GLOBAL='/dev/null',
        GIT_TERMINAL_PROMPT='0',GIT_NO_REPLACE_OBJECTS='1',xcrun_nocache='1')


def worker_environment():
    # Do not pass Actions tokens, proxy/CA/Go/Java configuration to producers.
    allowed=['HOME','USER','LOGNAME','RUNNER_TEMP','ImageOS','ImageVersion','GITHUB_SHA']
    result={k:os.environ[k] for k in allowed if k in os.environ}
    result.update(PATH='/usr/bin:/bin',LANG='en_US.UTF-8',PYTHONDONTWRITEBYTECODE='1')
    return result


def command(w,label,args,cwd,e):
    record(w/'evidence'/(label+'.intent.json'),dict(argv=list(map(str,args)),cwd=str(cwd)))
    with (w/'evidence'/(label+'.log')).open('xb') as out:code=subprocess.call(list(map(str,args)),cwd=cwd,env=e,stdout=out,stderr=subprocess.STDOUT)
    record(w/'evidence'/(label+'.result.json'),dict(exit_code=code))
    if code:raise RuntimeError('command failed '+label)
    return (w/'evidence'/(label+'.log')).read_text()


def toolrow(p,system=False):
    p=Path(p);info=p.stat()
    if p.resolve()!=p or not stat.S_ISREG(info.st_mode) or (not system and info.st_nlink!=1):raise ValueError('runtime tool regular required')
    return dict(path=str(p),bytes=info.st_size,sha256=sha(p),official_system_hardlink=system)


def derive_inputs(w,s,e):
    d=Path(s['runner']['developer']).resolve(strict=True)
    version=command(w,'xcode-version',['/usr/bin/xcodebuild','-version'],w,e)
    if version.strip()!='Xcode '+s['runner']['xcode_version']+'\nBuild version '+s['runner']['xcode_build']:raise ValueError('fixed installed Xcode missing')
    SDKs={}
    for kind,sdk in [('device','iphoneos'),('simulator','iphonesimulator')]:
        SDKs[kind]=command(w,'sdk-path-'+kind,['/usr/bin/xcrun','--sdk',sdk,'--show-sdk-path'],w,e).strip()
        actual=command(w,'sdk-version-'+kind,['/usr/bin/xcrun','--sdk',sdk,'--show-sdk-version'],w,e).strip()
        if actual!=s['runner']['SDK_version']:raise ValueError('fixed installed Apple SDK missing')
        SDKs[kind]=str(Path(SDKs[kind]).resolve(strict=True))
    pins=[toolrow(REPO/x['path']) for x in json.loads((HERE/'public-source-inputs.json').read_text())['files']]
    aliases={};tools={};base=d/'Toolchains/XcodeDefault.xctoolchain/usr/bin'
    for key,file in [('lipo','lipo'),('ar','ar'),('nm','llvm-nm'),('otool','otool')]:tools[key]=str((base/file).resolve(strict=True))
    for p in [base/n for n in ['swiftc','clang','ld','lipo','ar','llvm-nm','otool']]:
        actual=p.resolve(strict=True)
        if actual!=p:aliases[str(p)]=str(actual)
        if str(actual) not in {x['path'] for x in pins}:pins.append(toolrow(actual,True))
    for path in ['/usr/bin/git','/usr/bin/file','/usr/bin/sandbox-exec','/usr/bin/xcrun','/usr/bin/xcodebuild',str(Path(sys.executable).resolve())]:pins.append(toolrow(Path(path).resolve(),True))
    for path in SDKs.values():pins.append(toolrow(Path(path)/'SDKSettings.plist',True))
    g=w/'setup/go'
    for name_ in ['bin/go','VERSION','pkg/tool/darwin_arm64/compile','pkg/tool/darwin_arm64/link','pkg/tool/darwin_arm64/asm']:pins.append(toolrow(g/name_))
    for name_ in ['gomobile','gobind']:pins.append(toolrow(w/'setup/mobile-bin'/name_))
    for x in json.loads((HERE/'flutter-engine-members.json').read_text())['files']:pins.append(toolrow(w/'setup/engine/Flutter.xcframework'/x['path']))
    bridge=[]
    for x in json.loads((HERE/'public-source-inputs.json').read_text())['files']:
        if x['role'].startswith('bridge actual '):bridge.append(dict(path=str(REPO/x['path']),relative=x['role'][len('bridge actual '):],sha256=x['sha256']))
    c=dict(cloud_controller=IDENTITY,cohort=s['SDK_cohort'],host_root=str(w),upstream=str(w/'setup/upstream'),
        core_manifest=str(REPO/s['core_manifest']),patch=str(REPO/s['patch']),protocol=str(REPO/'server/pkg/protocol'),parent_builder=str(REPO/s['parent_builder']),
        original_protocol_replacement=s['original_protocol_replacement'],observer_helper=str(REPO/s['observer_helper']),observer_helper_sha256=sha(REPO/s['observer_helper']),guard=str(REPO/s['guard']),
        go=str(g/'bin/go'),go_sha256=sha(g/'bin/go'),gomobile=str(w/'setup/mobile-bin/gomobile'),gobind=str(w/'setup/mobile-bin/gobind'),
        python=str(Path(sys.executable).resolve()),mobile_version=s['mobile']['version'],mobile_module_h1=s['mobile']['h1'],source_sum_sha256=s['source_sum'],
        acquired_sum=str(REPO/s['acquired_sum']),acquired_sha256=s['acquired_sum_sha256'],developer=str(d),apple_sdk=SDKs,
        flutter_xcframework=str(w/'setup/engine/Flutter.xcframework'),module_cache=str(w/'setup/modcache'),bridge_files=bridge,aliases=aliases,pins=pins,
        git='/usr/bin/git',file='/usr/bin/file',**tools)
    record(w/'evidence/cloud-toolchain.json',dict(os=platform.platform(),machine=platform.machine(),python=sys.version,
        ImageOS=os.environ.get('ImageOS'),ImageVersion=os.environ.get('ImageVersion'),Xcode=version,SDKs=SDKs,
        tool_pins=pins,hermetic_SDK_or_host233_identity=False))
    return c


def setup(w):
    s=metadata();mkdir(w/'setup');mkdir(w/'evidence')
    for n in ['tmp','mobile-bin','gopath','modcache','host-go-cache']:mkdir(w/'setup'/n)
    go_rows=extract_go(download(w,'go',s['go']),w/'setup');record(w/'evidence/go-source-members.json',dict(files=go_rows))
    engine_rows=extract_engine(download(w,'engine',s['engine']),w/'setup/engine');record(w/'evidence/engine-source-members.json',dict(files=engine_rows,selected50_only=True))
    e=env(w,w/'setup/go',s['runner']['developer'])
    command(w,'go-version',[w/'setup/go/bin/go','version'],w,e)
    data=command(w,'mobile-module',[w/'setup/go/bin/go','mod','download','-json','golang.org/x/mobile@'+s['mobile']['version']],w,e)
    module=json.loads(data)
    if module['Sum']!=s['mobile']['h1'] or module['GoModSum']!=s['mobile']['go_mod_h1']:raise ValueError('official mobile module h1 differs')
    command(w,'build-mobile-tools',[w/'setup/go/bin/go','install','-trimpath','golang.org/x/mobile/cmd/gomobile@'+s['mobile']['version'],'golang.org/x/mobile/cmd/gobind@'+s['mobile']['version']],w,e)
    for n in ['gomobile','gobind']:
        info=command(w,n+'-buildinfo',[w/'setup/go/bin/go','version','-m',w/'setup/mobile-bin'/n],w,e)
        if 'go1.27.2' not in info or s['mobile']['version'] not in info or '-trimpath=true' not in info:raise ValueError('fixed tool source/buildinfo differs')
    # No checkout hooks/submodules/credential helpers; exact public Git object.
    git=['/usr/bin/git','-c','core.hooksPath=/dev/null','-c','credential.helper=','-c','protocol.allow=never','-c','protocol.https.allow=always']
    upstream=w/'setup/upstream';mkdir(upstream)
    command(w,'source-init',git+['init',str(upstream)],w,e)
    command(w,'source-fetch',git+['-C',upstream,'fetch','--no-tags','--depth=1',s['core_repository'],s['core_commit']],w,e)
    commit=command(w,'source-object',git+['-C',upstream,'rev-parse','FETCH_HEAD'],w,e).strip()
    if commit!=s['core_commit']:raise ValueError('exact upstream commit differs')
    c=derive_inputs(w,s,e)
    controller=w/'setup/controller';mkdir(controller)
    files=[]
    for n in ['apple_core.py','apple_observer.py','link_probe.py','core_binary.py']:
        b=(HERE/n).read_bytes();put(controller/n,b);files.append(dict(path=n,sha256=hashlib.sha256(b).hexdigest()))
    body=(json.dumps(c,sort_keys=True,indent=2)+'\n').encode();put(controller/'inputs.json',body)
    files.append(dict(path='inputs.json',sha256=hashlib.sha256(body).hexdigest()))
    record(controller/'manifest.json',dict(schema='luobo.apple-sdk.cloud-derived.v1',controller=IDENTITY,
       runtime_inputs_sha256=hashlib.sha256(body).hexdigest(),files=files,source_registry_sha256=sha(HERE/'registry.json')))
    sys.path.insert(0,str(controller));core=load(controller/'apple_core.py','apple_core')
    prefetch=w/'setup/prefetch';mkdir(prefetch)
    core.prepare(c,prefetch,e)
    before={n:sha(prefetch/'core'/n) for n in ['go.mod','go.sum']}
    record(w/'evidence/prefetch-before.json',before)
    try:command(w,'core-dependencies',[c['go'],'mod','download','all'],prefetch/'core',e)
    finally:
        after={n:sha(prefetch/'core'/n) for n in before};record(w/'evidence/prefetch-after.json',after)
        if after!=before or after['go.sum']!=s['acquired_sum_sha256']:raise ValueError('core acquired lock changed')
    command(w,'core-module-verify',[c['go'],'mod','verify'],prefetch/'core',e)
    core.inputs()
    record(w/'evidence/setup-derived.json',dict(controller_manifest_sha256=sha(controller/'manifest.json'),runtime_inputs_sha256=sha(controller/'inputs.json'),
       source_registry_sha256=sha(HERE/'registry.json'),module_warm_not_actual_generated_lock=True))


def profile(w):
    # Acquisition destinations are fixed by code/Go proxy+sumdb; only TLS/DNS
    # transport is admitted here. Actual bind/link inherits absolute network deny.
    policy=w/'setup-policy.sb'
    return ('(version 1)(allow default)(deny network*)(deny file-write*)'
       '(allow file-write* (require-all (subpath '+json.dumps(str(w))+') (require-not (literal '+json.dumps(str(policy))+'))) (literal "/dev/null"))'
       '(allow network-outbound (remote ip "*:443") (remote ip "*:53"))').encode()


def outer_worker(w,stage):
    metadata()
    if stage=='setup':setup(w)
    else:
        data=json.loads((w/'evidence/setup-derived.json').read_text());controller=w/'setup/controller'
        if sha(controller/'manifest.json')!=data['controller_manifest_sha256'] or sha(controller/'inputs.json')!=data['runtime_inputs_sha256'] or sha(HERE/'registry.json')!=data['source_registry_sha256']:raise ValueError('fixed cloud derivation changed')
        output=w/'.qa/apple-cloud-1';mkdir(w/'.qa')
        # ONE monitored process group. Reuse original worker/controls directly,
        # not its CLI (which would create a second, detached guard session).
        sys.path.insert(0,str(controller));core=load(controller/'apple_core.py','apple_core')
        c=core.inputs();core.admit(output,w);core.put_dir(output)
        for relative,body in core.controls(c,output).items():core.put(output/relative,body)
        os.chmod(output/'bin/go',0o700)
        args=['/usr/bin/sandbox-exec','-f',output/'offline.sb',sys.executable,'-B',controller/'apple_core.py','--worker','--output',output]
        # Inherit this guard's group; timeout/signal kills core, observer and all
        # compile children together. resource_dual samples allowned6 + build3.
        command(w,'actual-sdk-build',args,w,dict(os.environ,PYTHONDONTWRITEBYTECODE='1'))
        if not (output/'sdk-passed.json').exists():raise ValueError('real SDK bind/link incomplete')
        if sha(controller/'manifest.json')!=data['controller_manifest_sha256'] or sha(controller/'inputs.json')!=data['runtime_inputs_sha256']:raise ValueError('derived controller changed')
    metadata()
    record(w/'evidence'/(stage+'-passed.json'),dict(identity=IDENTITY,SDK_only=True,App=False,runtime=False))


def main():
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--cohort',choices=[IDENTITY],required=True)
    p.add_argument('--work');p.add_argument('--stage',choices=['readonly','setup','build'],default='readonly');p.add_argument('--worker',action='store_true',help=argparse.SUPPRESS);a=p.parse_args()
    if a.stage=='readonly':metadata();print('VERIFIED_CLOUD_SDK_SOURCE_ONLY');return 0
    s=json.loads((HERE/'portable-inputs.json').read_text())
    w=work_path(a.work,a.stage=='setup' and not a.worker)
    if platform.system()!='Darwin' or platform.machine()!='arm64' or sys.version_info[:2]!=(3,12):raise ValueError('standard macOS ARM64 / Python3.12 required')
    if a.worker:outer_worker(w,a.stage);return 0
    minimum=(8 if a.stage=='setup' else 4)*GIB
    if min(free(w.parent),free(REPO))<minimum:raise ValueError('cold8GiB/build4GiB admission required')
    metadata()
    if a.stage=='setup':
        mkdir(w);mkdir(w/'evidence');put(w/'setup-policy.sb',profile(w))
        record(w/'activation.json',dict(identity=IDENTITY,source_registry_sha256=sha(HERE/'registry.json'),controller_commit=os.environ.get('GITHUB_SHA')))
    else:
        activation=json.loads((w/'activation.json').read_text());previous=json.loads((w/'setup-resource.json').read_text())
        if activation['identity']!=IDENTITY or activation['source_registry_sha256']!=sha(HERE/'registry.json') or previous['status']!='passed' or not (w/'evidence/setup-passed.json').exists():raise ValueError('complete exact setup activation required')
    record(w/'evidence'/(a.stage+'-claim.json'),dict(claimed_once=True))
    guard=load(REPO/s['guard'] if a.stage=='setup' else HERE/'resource_dual.py','apple_cloud_resource_guard')
    cmd=['/usr/bin/env','-i',*[k+'='+v for k,v in worker_environment().items()],sys.executable,'-B',str(HERE/'cloud.py'),'--cohort',IDENTITY,'--work',str(w),'--stage',a.stage,'--worker']
    if a.stage=='setup':cmd=['/usr/bin/sandbox-exec','-f',str(w/'setup-policy.sb'),*cmd]
    from types import SimpleNamespace
    def interrupted(_number,_frame):raise InterruptedError('cloud guard interrupted')
    for number in [signal.SIGINT,signal.SIGTERM]:signal.signal(number,interrupted)
    result=guard.run(SimpleNamespace(command=cmd,cwd=w,log=w/(a.stage+'-raw.log'),record=w/(a.stage+'-resource.json'),
        min_free_mib=1024,timeout=1200 if a.stage=='setup' else 1800,allocated_root=w,max_allocated_mib=3072 if a.stage=='setup' else 6144,poll_interval=.2,
        secondary_root=None if a.stage=='setup' else w/'.qa/apple-cloud-1',secondary_cap_mib=3072))
    # One guard covers all-owned6GiB + build3GiB; failure wins regardless
    # of apparent passed JSON. No artifact/cache deletion or partial resumes.
    return result


if __name__=='__main__':raise SystemExit(main())
