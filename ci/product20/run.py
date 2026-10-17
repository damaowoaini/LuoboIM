#!/usr/bin/env python3
"""One fresh Linux HTTP/WS chain; reuses twenty accepted product ELFs. Locked pending Root."""
import argparse,hashlib,importlib.util,json,os,re,secrets,shutil,stat,subprocess,sys,time,urllib.request,urllib.parse,zipfile
from pathlib import Path
REPO=Path(__file__).resolve().parents[2]
HERE=Path(__file__).resolve().parent
NAMES=('admin-rpc','chat-rpc','office-rpc','admin-api','chat-api','route-service')
OVERLAYS=('server/internal/runtimeacceptance/product20_linux.go','server/cmd/tools/product20-acceptance/main.go')
BOOT='server/internal/runtimeacceptance/bootstrap_linux.go'
ANCHOR='"secret": c["serviceSecret"], "multiLoginPolicy": 1'
REPLACEMENT='"secret": c["serviceSecret"], "adminDelegation": map[string]any{"issuer": b.Header.Tenant}, "mysql": fixtureSQL, "messageVerify": map[string]bool{"friendVerify": false}, "multiLoginPolicy": 1'
def sha(b):return hashlib.sha256(b).hexdigest()
def regular(path,maximum=32<<20):
 p=Path(path)
 if p.resolve()!=p:raise ValueError('canonical source required')
 fd=os.open(p,os.O_RDONLY|os.O_NOFOLLOW)
 try:
  a=os.fstat(fd)
  if not stat.S_ISREG(a.st_mode) or a.st_nlink!=1 or a.st_size>maximum:raise ValueError('bounded regular input required')
  chunks=[];left=maximum+1
  while left:
   part=os.read(fd,min(left,1<<20))
   if not part:break
   chunks.append(part);left-=len(part)
  b=os.fstat(fd);c=p.lstat();key=lambda x:(x.st_dev,x.st_ino,x.st_mode,x.st_size,x.st_mtime_ns,x.st_ctime_ns)
  if key(a)!=key(b) or key(a)!=key(c):raise ValueError('source changed')
  raw=b''.join(chunks)
  if len(raw)!=a.st_size:raise ValueError('short read')
  return raw
 finally:os.close(fd)
def write(p,value):
 raw=(json.dumps(value,separators=(',',':'),ensure_ascii=False)+'\n').encode()
 fd=os.open(p,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
 with os.fdopen(fd,'wb') as f:f.write(raw);f.flush();os.fsync(f.fileno())
 return sha(raw)
def load(path,label):
 spec=importlib.util.spec_from_file_location(label,REPO/path);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m
def vector(bodies):
 rows=[{'path':n,'bytes':len(b),'sha256':sha(b)} for n,b in sorted(bodies.items())]
 return {'members':len(rows),'digest':sha(json.dumps(rows,sort_keys=True,separators=(',',':')).encode())}
def verify():
 c=json.loads(regular(HERE/'inputs.json'))
 if c['schema']!=1 or c['scope']!='one-fresh-product20-http-ws-only' or c['productRebuilds']!=0 or c['oldTestsToRerun']!=[]:raise ValueError('exact chain scope required')
 for path,expected in c['helpers'].items():
  if sha(regular(REPO/path))!=expected:raise ValueError('source helper drift')
 graph=load(c['graphProvider']['path'],'p20_graph_source');g,bodies,*_=graph.verify()
 if g['source']!=c['graphProvider']['source']:raise ValueError('explicit graph source differs')
 chart=load('ci/chart-route-build/run.py','p20_chart_source');cc,chart_bodies=chart.verify()
 if cc['sources']!=c['chartProvider']['sources'] or cc['locks']!=c['chartProvider']['locks']:raise ValueError('accepted chart6 source/locks differ')
 original=dict(bodies);body=bodies[BOOT]
 if sha(body)!=c['fixtureConfiguration']['beforeSHA256'] or body.count(ANCHOR.encode())!=1:raise ValueError('single original fixture map anchor required')
 config_anchor=b'\tvalue := map[string]any{\"envs\"';config_prefix=b'\tfixtureSQL, e := product20FixtureSQL(b)\n\tif e != nil {\n\t\treturn "", e\n\t}\n'
 if body.count(config_anchor)!=1:raise ValueError('one original fixture constructor required')
 bodies=dict(bodies);bodies[BOOT]=body.replace(config_anchor,config_prefix+config_anchor,1).replace(ANCHOR.encode(),REPLACEMENT.encode(),1)
 for target in OVERLAYS:
  raw=regular(HERE/'source'/target)
  if target in bodies or sha(raw)!=c['overlay'][target]:raise ValueError('new tool overlay identity differs')
  bodies[target]=raw
 if vector(bodies)!=c['derivedHarnessSource']:raise ValueError('explicit auxiliary-only derived source differs')
 return c,graph,g,original,bodies,chart_bodies

def require_bound(c):
 binding=c.get('executionBinding')
 if not isinstance(binding,dict) or binding.get('rootAccepted') is not True or binding.get('source')!=c['graphProvider']['source'] or not re.fullmatch('[0-9a-f]{64}',binding.get('rootAcceptanceSHA256','')):raise ValueError('Root accepted successful current core14 artifact remains required')
 for key in ('core14','chart6'):
  a=binding[key]
  if set(a)!={'run','head','attempt','artifactID','artifactBytes','artifactSHA256','manifestSHA256'} or any(not re.fullmatch('[0-9a-f]{64}',a[n]) for n in ('artifactSHA256','manifestSHA256')) or not re.fullmatch('[0-9a-f]{40}',a['head']) or a['attempt']!=1 or not 0<a['artifactBytes']<=2<<30:raise ValueError('exact successful artifact binding required')
 if binding['chart6']!=c['acceptedChartArtifact']:raise ValueError('original accepted chart6 artifact binding differs')
 mysql=c.get('mysql')
 if not isinstance(mysql,dict) or not re.fullmatch('docker.io/library/mysql@sha256:[0-9a-f]{64}',mysql.get('image','')) or not mysql.get('officialSourceReviewSHA256'):raise ValueError('reviewed exact MySQL fixture binding required')
 return binding
class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,req,fp,code,msg,headers,newurl):return None

def artifact(a,names,directory,limit,core_source=None,chart_sources=None):
 # Only API metadata receives the task token. Signed blob URL is never logged
 # or sent with Bearer; no binary is downloaded on the local workstation.
 repo=os.environ['GITHUB_REPOSITORY'];token=os.environ['GH_TOKEN']
 if not re.fullmatch('[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+',repo):raise ValueError('repository shape differs')
 base='https://api.github.com/repos/'+repo+'/actions/artifacts/'+str(a['artifactID'])
 headers={'Authorization':'Bearer '+token,'Accept':'application/vnd.github+json','X-GitHub-Api-Version':'2022-11-28'}
 def api(path):
  with urllib.request.urlopen(urllib.request.Request(path,headers=headers),timeout=30) as r:
   raw=r.read((2<<20)+1)
   if len(raw)>2<<20:raise ValueError('metadata limit')
   return json.loads(raw)
 meta=api(base)
 if meta['id']!=a['artifactID'] or meta['expired'] or meta['size_in_bytes']!=a['artifactBytes'] or meta.get('digest')!='sha256:'+a['artifactSHA256'] or meta['workflow_run']['id']!=a['run'] or meta['workflow_run']['head_sha']!=a['head']:raise ValueError('artifact API body differs')
 run=api('https://api.github.com/repos/'+repo+'/actions/runs/'+str(a['run']))
 if run['head_sha']!=a['head'] or run['run_attempt']!=a['attempt'] or run['status']!='completed' or run['conclusion']!='success':raise ValueError('actual successful original producer required')
 try:urllib.request.build_opener(NoRedirect()).open(urllib.request.Request(base+'/zip',headers=headers),timeout=30);raise ValueError('signed download redirect required')
 except urllib.error.HTTPError as e:
  if e.code!=302:raise
  url=e.headers['Location']
 u=urllib.parse.urlsplit(url)
 if u.scheme!='https' or not u.hostname or not u.hostname.endswith('.blob.core.windows.net') or u.username or u.password or u.port not in (None,443):raise ValueError('anonymous GitHub blob required')
 archive=directory.parent/(directory.name+'.zip');h=hashlib.sha256();size=0
 with urllib.request.urlopen(url,timeout=30) as source,archive.open('xb') as target:
  while part:=source.read(1<<20):
   size+=len(part)
   if size>a['artifactBytes']:raise ValueError('artifact size cap')
   target.write(part);h.update(part)
 if size!=a['artifactBytes'] or h.hexdigest()!=a['artifactSHA256']:raise ValueError('artifact ZIP body differs')
 directory.mkdir(mode=0o700)
 with zipfile.ZipFile(archive) as z:
  rows=z.infolist()
  if len(rows)!=len(names)+1 or {r.filename for r in rows}!=set(names)|{'manifest.json'} or len({r.filename for r in rows})!=len(rows) or sum(r.file_size for r in rows)>limit+(1<<20):raise ValueError('exact bounded flat artifact required')
  for row in rows:
   kind=stat.S_IFMT(row.external_attr>>16)
   if row.flag_bits&1 or kind not in (0,stat.S_IFREG) or row.file_size>(1<<20 if row.filename=='manifest.json' else (256<<20 if core_source else 128<<20)):raise ValueError('artifact entry type/cap')
   with z.open(row) as src,(directory/row.filename).open('xb') as dst:
    total=0
    while part:=src.read(1<<20):dst.write(part);total+=len(part)
    if total!=row.file_size:raise ValueError('complete CRC-checked body required')
 manifest_raw=regular(directory/'manifest.json',1<<20)
 if sha(manifest_raw)!=a['manifestSHA256']:raise ValueError('original producer manifest differs')
 m=json.loads(manifest_raw);products=m['binaries']
 if len(products)!=len(names) or {r['name'] for r in products}!=set(names) or sum(r['bytes'] for r in products)>limit:raise ValueError('exact product manifest required')
 if core_source:
  if m['source']!=core_source or m['head']!=a['head'] or str(m['runID'])!=str(a['run']) or str(m['attempt'])!=str(a['attempt']) or m['CGO_ENABLED']!='1' or m['configsLogsKeysAuxiliariesIncluded'] is not False:raise ValueError('core product source/platform differs')
 else:
  if m['sourceVectors']!=chart_sources or m['head']!=a['head'] or str(m['run'])!=str(a['run']) or str(m['attempt'])!=str(a['attempt']) or m['sourceHelperToolPostExact'] is not True or m['configsLogsOrSourceIncluded'] is not False:raise ValueError('six product source/platform differs')
 for row in products:
  p=directory/row['name'];rawhead=b'';h=hashlib.sha256();n=0
  with p.open('rb') as f:
   while part:=f.read(1<<20):
    if not rawhead:rawhead=part[:64]
    n+=len(part);h.update(part)
  if n!=row['bytes'] or h.hexdigest()!=row['sha256'] or rawhead[:6]!=b'\x7fELF\x02\x01' or rawhead[18:20]!=b'\x3e\x00' or row['GOOS']!='linux' or row['GOARCH']!='amd64' or row['CGO_ENABLED']!=('1' if core_source else '0') or row['mode']!=0o700:raise ValueError('original ELF body/mode/platform differs')
  p.chmod(0o700) # upload-artifact does not preserve Unix permissions.
 return m

def rootdir():return Path(os.environ['RUNNER_TEMP'])/('product20-'+os.environ['GITHUB_RUN_ID']+'-'+os.environ['GITHUB_RUN_ATTEMPT'])
def outdir():return REPO/'ci-results/product20'
def envbase():return {'PATH':os.environ['PATH'],'HOME':os.environ['HOME'],'GOTOOLCHAIN':'local','GOWORK':'off','GOENV':'off','GOAUTH':'off','GOMAXPROCS':'2','GOMEMLIMIT':'900MiB','CGO_ENABLED':'1'}
def allocation_owner():return {'run':os.environ['GITHUB_RUN_ID'],'attempt':os.environ['GITHUB_RUN_ATTEMPT'],'head':os.environ['GITHUB_SHA'],'uid':os.getuid()}
def admit():
 c,*_=verify();require_bound(c);root=rootdir()
 if os.getuid()==0 or os.environ.get('RUNNER_ENVIRONMENT')!='github-hosted' or os.environ.get('RUNNER_OS')!='Linux' or root.exists() or root.resolve()!=root or os.statvfs(root.parent).f_bavail*os.statvfs(root.parent).f_frsize<4<<30:raise ValueError('fresh hosted cold4GiB admission required')
 root.mkdir(mode=0o700);write(root/'allocation.json',allocation_owner());return 0

def run():
 c,graph,g,original,bodies,chart_bodies=verify();binding=require_bound(c)
 if any(os.environ.get(k)!=v for k,v in {'GITHUB_ACTIONS':'true','RUNNER_ENVIRONMENT':'github-hosted','RUNNER_OS':'Linux'}.items()) or os.getuid()==0:raise ValueError('fresh hosted nonroot Linux only')
 root=rootdir();out=outdir()
 if root.resolve()!=root or stat.S_IMODE(root.stat().st_mode)!=0o700 or root.stat().st_uid!=os.getuid() or not {'allocation.json'}<={p.name for p in root.iterdir()}<={'allocation.json','controller-private.log'} or json.loads(regular(root/'allocation.json'))!=allocation_owner():raise ValueError('original fresh admission required')
 out.mkdir(mode=0o700,parents=True,exist_ok=False);private=root/'private';private.mkdir(mode=0o700)
 write(private/'owner.json',{'run':os.environ['GITHUB_RUN_ID'],'attempt':os.environ['GITHUB_RUN_ATTEMPT'],'head':os.environ['GITHUB_SHA'],'uid':os.getuid()})
 controls={p:sha(regular(REPO/p)) for p in c['controllers']};tools=graph.tool_snapshot();write(out/'pre.json',{'controls':controls,'tools':tools,'coreProduct':c['graphProvider']['source'],'harness':c['derivedHarnessSource'],'chart':c['chartProvider']['sources']})
 stage=root/'source';stage.mkdir(mode=0o700)
 for target,raw in bodies.items():
  p=stage/target;p.parent.mkdir(parents=True,exist_ok=True);p.write_bytes(raw);p.chmod(g['sourceModes'].get(target,0o644))
 (stage/'server/config/config.yaml').write_bytes(bodies['server/config/config.example.yaml']);(stage/'server/config/config.yaml').chmod(0o600)
 env=envbase()
 for key,name in [('GOMODCACHE','modcache'),('GOCACHE','buildcache'),('GOPATH','gopath'),('TMPDIR','tmp')]:
  p=root/name;p.mkdir(mode=0o700);env[key]=str(p)
 env['GOTMPDIR']=env['TMPDIR'];index=0;started=time.monotonic();plane=None;mysql_id=None;primary=None
 def command(label,argv,timeout,environment=None,cwd=None):
  nonlocal index
  if timeout>10800-(time.monotonic()-started):raise ValueError('controller budget exhausted')
  index+=1;folder=private/(str(index)+'-'+label);folder.mkdir(mode=0o700)
  child=[sys.executable,'-B',str(REPO/'ci/group-effects/capture.py'),'--stdout',str(folder/'stdout'),'--stderr',str(folder/'stderr'),'--']
  if environment is not None:child+=['/usr/bin/env','-i']+[k+'='+v for k,v in environment.items()]
  child+=argv
  wrapped=[sys.executable,'-B',str(REPO/'ci/group-effects/resource_guard.py'),'--cwd',str(cwd or stage/'server'),'--log',str(folder/'guard.log'),'--record',str(folder/'resource.json'),'--min-free-mib','1024','--poll-interval','.2','--allocated-root',str(root),'--max-allocated-mib','16384','--timeout',str(timeout),'--']+child
  proc=subprocess.run(wrapped,timeout=timeout+30,check=False);record=json.loads(regular(folder/'resource.json'))
  write(out/(str(index)+'-phase.json'),{'phase':label,'exitCode':proc.returncode,'resource':record,'stdoutSHA256':sha(regular(folder/'stdout',32<<20)),'stderrSHA256':sha(regular(folder/'stderr',32<<20))})
  failed=proc.returncode!=0 or record.get('status')!='passed' or record.get('exit_code')!=0
  if failed and (label.startswith('compile-') or label=='minio-source-compile') and not (root/'plane').exists():
   # Identical boundary to the reviewed fullgraph compiler projection: these
   # are only fixed public sources, before private resource config/credentials.
   raw=regular(folder/'stderr',32<<20)
   write(out/(str(index)+'-public-compile-failure.json'),{'phase':label,'publicSourceOnly':True,'completeBytes':len(raw),'completeSHA256':sha(raw),'diagnostic':raw[:65536].decode('utf-8','replace'),'truncated':len(raw)>65536})
  if label=='actual-fresh-product20-http-ws-once' and (root/'chain/product20-result.json').exists():
   export_terminal(root/'chain/product20-result.json',out/'actual-terminal.json')
  if failed:raise ValueError('phase failed; private raw retained')
  return regular(folder/'stdout',32<<20)
 try:
  core=artifact(binding['core14'],graph.ROLES,root/'product14',2<<30,core_source=c['graphProvider']['source'])
  six=artifact(binding['chart6'],NAMES,root/'product6',512<<20,chart_sources=c['chartProvider']['sources'])
  if core['locks']!={n:sha(original['server/'+n]) for n in ('go.mod','go.sum')} or six['locks']!=c['chartProvider']['locks']:raise ValueError('original module locks differ from actual artifacts')
  # Only two necessary auxiliaries are compiled; product20 are never rebuilt.
  go=shutil.which('go');version=command('go-version',[go,'version'],15,env).decode().strip()
  if version!='go version go1.27.2 linux/amd64':raise ValueError('exact Go1.27.2 required')
  go_sha=sha(regular(Path(go).resolve()));env['GOROOT']=command('go-root',[go,'env','GOROOT'],15,env).decode().strip()
  command('auxiliary-module-download',[go,'mod','download','-json'],600,dict(env,GOPROXY='https://proxy.golang.org',GOSUMDB='sum.golang.org'))
  offline=dict(env,GOPROXY='off',GOSUMDB='off');binroot=root/'aux';binroot.mkdir(mode=0o700)
  for name,pkg in [('msgtransfer-legacy','./cmd/openim-msgtransfer'),('product20-acceptance','./cmd/tools/product20-acceptance')]:command('compile-'+name,[go,'build','-mod=readonly','-p=1','-o',str(binroot/name),pkg],1200,offline)
  selectors=load('ci/backend-fullgraph/service_selectors.py','p20_selectors');builds=[]
  for row in core['binaries']:
   receipt={'schema':1,'sourceSHA256':core['source']['digest'],'binarySHA256':row['sha256'],'binaryBytes':row['bytes'],'toolchainSHA256':core['toolSHA256']['go']}
   rp=private/(row['name']+'-receipt.json');rs=write(rp,receipt)
   if rs!=row['buildReceiptSHA256']:raise ValueError('original product receipt cannot be reconstructed exactly')
   builds.append({'role':row['name'],'path':str(root/'product14'/row['name']),'sha256':row['sha256'],'sourceSHA256':core['source']['digest'],'receiptPath':str(rp),'receiptSHA256':rs,'services':selectors.SERVICES[row['name']],'components':selectors.COMPONENTS[row['name']]})
  legacy=binroot/'msgtransfer-legacy';legacy_raw=regular(legacy,256<<20);rp=private/'legacy-receipt.json';rs=write(rp,{'schema':1,'sourceSHA256':core['source']['digest'],'binarySHA256':sha(legacy_raw),'binaryBytes':len(legacy_raw),'toolchainSHA256':go_sha})
  buildpath=private/'builds.json';bs=write(buildpath,{'version':'source-bound-fullgraph-builds-v1','sourceSHA256':core['source']['digest'],'binaries':builds,'legacyMessageTransfer':{'role':'msgtransfer-legacy','path':str(legacy),'sha256':sha(legacy_raw),'sourceSHA256':core['source']['digest'],'receiptPath':str(rp),'receiptSHA256':rs}})
  resources=load('ci/backend-fullgraph/resources.py','p20_resources');builder=load('ci/backend-fullgraph/minio_build.py','p20_minio');catalog=json.loads(regular(REPO/'ci/backend-fullgraph/image-catalog.json'))
  # Source-MinIO is fixture-only, using its original exact builder. No prior
  # fixture ELF is retained by product14 artifact, so this new job needs it.
  minio,proof=builder.build(root,catalog,command,go,go_sha,env);write(out/'fixture-minio.json',proof)
  for image in sorted(set(catalog['images'].values())|{c['mysql']['image']}):command('fixture-image-'+sha(image.encode())[:8],['/usr/bin/docker','--host','unix:///var/run/docker.sock','pull','--platform','linux/amd64',image],600)
  plane=resources.Plane(root/'plane',catalog['images'],minio_source=minio);claim=plane.prepare();keeper=next(r for r in claim['observations'] if r['role']=='keeper')
  mysql_id=create_mysql(plane,c['mysql'],secrets.token_hex(32))
  corework=root/'core';corework.mkdir(mode=0o700);work=root/'chain';work.mkdir(mode=0o700)
  template=REPO/'chart/config/config.example.yaml'
  products=[{'name':r['name'],'path':str(root/'product6'/r['name']),'sha256':r['sha256'],'bytes':r['bytes'],'sourceSHA256':r['sourceVector']['sha256']} for r in six['binaries']]
  p={'version':'fresh-product20-http-ws-v1','work':str(work),'core':{'version':'owned-fullgraph-acceptance-v1','scenario':'ack','work':str(corework),'planePath':str(plane.private/'plane.json'),'planeSHA256':sha(regular(plane.private/'plane.json')),'credentialsPath':str(plane.private/'credentials.json'),'credentialsSHA256':sha(regular(plane.private/'credentials.json')),'buildsPath':str(buildpath),'buildsSHA256':bs},'chartTemplatePath':str(template),'chartTemplateSHA256':c['helpers']['chart/config/config.example.yaml'],'sqlRootPassword':json.loads(regular(plane.private/'product20-mysql.json'))['rootPassword'],'products':products}
  pp=private/'product20-input.json';ps=write(pp,p);groups=','.join(map(str,sorted(set(os.getgroups()+[os.getgid()]))))
  argv=['sudo','-n','/usr/bin/nsenter','--net=/proc/'+str(keeper['PID'])+'/ns/net','--','/usr/bin/setpriv','--reuid',str(os.getuid()),'--regid',str(os.getgid()),'--groups',groups,'--no-new-privs','--','/usr/bin/env','-i','PATH=/usr/bin:/bin','HOME='+os.environ['HOME'],'TMPDIR='+str(work),str(binroot/'product20-acceptance'),'--private-input',str(pp),'--private-input-sha256',ps]
  write(private/'operator-intent.json',{'pidBindingAtOriginalStart':True,'inputSHA256':ps,'source':core['source'],'harness':c['derivedHarnessSource']})
  command('actual-fresh-product20-http-ws-once',argv,1380)
  result=json.loads(regular(work/'product20-result.json',128<<10));check_result(result,c)
  write(out/'actual-result.json',result)
 except BaseException as e:primary=e;raise
 finally:
  try:
   if plane is not None:
    cleanup_error=None
    try:cleanup_mysql(plane)
    except Exception as e:cleanup_error=e
    try:write(out/'resource-cleanup.json',graph.public_cleanup(plane.cleanup()))
    finally:
     if cleanup_error is not None:raise cleanup_error
  finally:
   checks={};failures={}
   for name,fn in [('source',lambda: all(regular(stage/n)==b and stat.S_IMODE((stage/n).stat().st_mode)==g['sourceModes'].get(n,0o644) for n,b in bodies.items()) and regular(stage/'server/config/config.yaml')==bodies['server/config/config.example.yaml']),('locks',lambda: all(regular(stage/'server'/n)==original['server/'+n] for n in ('go.mod','go.sum'))),('controllers',lambda:{p:sha(regular(REPO/p)) for p in c['controllers']}==controls),('tools',lambda:graph.tool_snapshot()==tools),('providers',lambda:verify()[0]==c)]:
    try:checks[name]=fn()
    except BaseException as e:checks[name]=False;failures[name]=type(e).__name__
   write(out/'post.json',{'exact':all(checks.values()),'checks':checks,'exceptions':failures,'coreProduct':c['graphProvider']['source'],'harness':c['derivedHarnessSource']})
   if not all(checks.values()) and primary is None:raise ValueError('original source/tool/controller post failed')
 return 0

def create_mysql(plane,policy,password):
 # This seventh DB container is fresh and owned alongside the original six,
 # but outside Plane's finite role enum. Exact separate owner is never adopted.
 image=policy['image'];meta=json.loads(plane.call(['image','inspect',image]))
 if len(meta)!=1:raise ValueError('one MySQL image required')
 resources=load('ci/backend-fullgraph/resources.py','p20_mysql_identity');resources.require_repo_digest(meta[0],image)
 if meta[0]['Id']!=policy['configDigest'] or (meta[0]['Config'].get('Entrypoint') or [])!=['docker-entrypoint.sh']:raise ValueError('MySQL actual image config differs')
 data=plane.root/'mysql-data';data.mkdir(mode=0o700);envfile=plane.private/'mysql.env';envfile.write_text('MYSQL_ROOT_PASSWORD='+password+'\nMYSQL_ROOT_HOST=127.0.0.1\n');envfile.chmod(0o600)
 cidfile=plane.private/'product20-mysql.cid';write(plane.private/'product20-mysql.json',{'nonce':plane.nonce,'uid':os.getuid(),'image':image,'rootPassword':password,'configDigest':policy['configDigest']})
 argv=['create','--pull=never','--name','luobo-product20-'+plane.nonce+'-mysql','--cidfile',str(cidfile),'--label','luobo.product20.owner='+plane.nonce,'--label','luobo.product20.role=mysql','--user',str(os.getuid())+':'+str(os.getgid()),'--cap-drop=ALL','--security-opt=no-new-privileges','--pids-limit=512','--network','container:'+plane.ids[0],'--mount',f'type=bind,src={data},dst=/var/lib/mysql','--tmpfs','/var/run/mysqld:rw,nosuid,nodev,noexec,size=16777216,mode=0700,uid='+str(os.getuid())+',gid='+str(os.getgid()),'--env-file',str(envfile),image,'mysqld','--bind-address=127.0.0.1','--port=3306','--character-set-server=utf8mb4','--collation-server=utf8mb4_unicode_ci','--socket=/var/run/mysqld/mysqld.sock','--pid-file=/var/run/mysqld/mysqld.pid','--tmpdir=/var/run/mysqld','--secure-file-priv=NULL','--log-error=/var/lib/mysql/task-error.log']
 plane.call(argv)
 cid=regular(cidfile,1024).decode().strip()
 if not re.fullmatch('[0-9a-f]{64}',cid):raise ValueError('original MySQL id absent')
 write(plane.private/'product20-mysql-owner.json',{'id':cid,'nonce':plane.nonce,'uid':os.getuid(),'imageID':meta[0]['Id'],'keeperID':plane.ids[0]})
 plane.call(['start',cid]);return cid

def cleanup_mysql(plane):
 intent=plane.private/'product20-mysql.json'
 if not intent.exists():return
 value=json.loads(regular(intent));name='luobo-product20-'+plane.nonce+'-mysql'
 if value['nonce']!=plane.nonce or value['uid']!=os.getuid():raise ValueError('original MySQL intent required')
 ids=plane.call(['ps','--all','--no-trunc','--filter','name=^/'+name+'$','--format','{{.ID}}']).decode().splitlines()
 if not ids:return
 if len(ids)!=1 or not re.fullmatch('[0-9a-f]{64}',ids[0]):raise ValueError('ambiguous MySQL owner')
 cid=ids[0];row=json.loads(plane.call(['inspect',cid]))[0];labels=row['Config']['Labels']
 if row['Id']!=cid or row['Name']!='/'+name or labels.get('luobo.product20.owner')!=plane.nonce or labels.get('luobo.product20.role')!='mysql' or row['Image']!=value['configDigest'] or row['HostConfig']['NetworkMode']!='container:'+plane.ids[0]:raise ValueError('foreign MySQL refused')
 if row['State']['Running']:plane.call(['stop','--time','30',cid],45)
 plane.call(['rm','--volumes',cid])
 if plane.call(['ps','--all','--no-trunc','--filter','id='+cid,'--format','{{.ID}}']).strip():raise ValueError('original MySQL absence unconfirmed')

def export_terminal(path,output):
 raw=regular(path,128<<10);v=json.loads(raw)
 phases={'fresh-core-prepare','fresh-sql-and-six-real-services','actual-route-config-and-registration','actual-route-password-login-dual-token','actual-gateway-ordinary-message-once','ordinary-peer-history-accepted-local-cleanup-required'}
 if v.get('version')!='fresh-product20-http-ws-result-v1' or v.get('phase') not in phases or type(v.get('accepted')) is not bool or any(x and not re.fullmatch('[0-9a-f]{64}',x) for x in (v.get('failureSHA256',''),v.get('coreSourceSHA256',''))):raise ValueError('finite original terminal shape rejected')
 keys=('productProcesses','realRegistrations','realPasswordLogins','ordinaryDispatches','peerMessages','historyMessages')
 if any(type(v.get(k)) is not int or not 0<=v[k]<=20 for k in keys):raise ValueError('finite original terminal counts rejected')
 write(output,{'version':v['version'],'accepted':v['accepted'],'phase':v['phase'],'failureSHA256':v.get('failureSHA256',''),'coreSourceSHA256':v.get('coreSourceSHA256',''),'originalResultSHA256':sha(raw),'counts':{k:v[k] for k in keys},'rawLogsOrDataExported':False})

def check_result(v,c):
 flags=('accepted','dualTokens','chatTokenSelfRead','configHTTP','wrongPasswordRejected','businessPermissions','freshSQLSeedOnly','auxiliaryListenersJoined','originalSixChildrenExited','originalCoreCleanupComplete')
 if v.get('version')!='fresh-product20-http-ws-result-v1' or any(v.get(k) is not True for k in flags) or v['coreSourceSHA256']!=c['graphProvider']['source']['digest'] or v['productProcesses']!=20 or v['realRegistrations']!=2 or v['realPasswordLogins']!=2 or v['ordinaryDispatches']!=1 or v['peerMessages']!=1 or v['historyMessages']!=1 or v['messageSeq']<=0 or any(v.get(k) is not False for k in ('chartCanonicalDrainClaimed','webUIAccepted','deploymentAccepted')):raise ValueError('actual HTTP+WS result incomplete')
 pids=v.get('coreOriginalPIDs',[])+v.get('sixOriginalPIDs',[])
 if len(v.get('coreOriginalPIDs',[]))!=14 or len(v.get('sixOriginalPIDs',[]))!=6 or len(set(pids))!=20 or any(type(p) is not int or p<=1 or Path('/proc',str(p)).exists() for p in pids):raise ValueError('all original twenty process exits required')

def collect():
 # Always inspection/cleanup, never promotes an unknown runtime to canonical.
 c,graph,*_=verify();root=rootdir();out=outdir();out.mkdir(mode=0o700,parents=True,exist_ok=True)
 if not root.exists():write(out/'collection.json',{'accepted':False,'started':False});return 1
 owner=json.loads(regular(root/'private/owner.json'))
 if owner!={'run':os.environ['GITHUB_RUN_ID'],'attempt':os.environ['GITHUB_RUN_ATTEMPT'],'head':os.environ['GITHUB_SHA'],'uid':os.getuid()}:raise ValueError('original allocation owner required')
 errors=[];resources=load('ci/backend-fullgraph/resources.py','p20_always_cleanup')
 if (root/'plane/private/owner.json').exists():
  plane=resources.Plane.reopen(root/'plane')
  for name,call in [('mysql',lambda:cleanup_mysql(plane)),('original-six',plane.cleanup)]:
   try:call()
   except Exception as e:errors.append(name+':'+type(e).__name__)
 result_ok=False
 try:check_result(json.loads(regular(root/'chain/product20-result.json',128<<10)),c);result_ok=True
 except Exception as e:errors.append('original-process-result:'+type(e).__name__)
 post=json.loads(regular(out/'post.json')) if (out/'post.json').exists() else {}
 controller_path=REPO/'ci-results/product20-controller.resource.json';controller=json.loads(regular(controller_path)) if controller_path.exists() else {}
 accepted=not errors and result_ok and post.get('exact') is True and controller.get('status')=='passed' and controller.get('exit_code')==0
 write(out/'collection.json',{'accepted':accepted,'finiteFailures':errors,'actualHTTPWS':result_ok,'canonicalChartDrainClaimed':False,'webUIAccepted':False,'deploymentAccepted':False})
 return 0 if accepted else 1
if __name__=='__main__':
 p=argparse.ArgumentParser();p.add_argument('--verify',action='store_true');p.add_argument('--collect',action='store_true');p.add_argument('--admit',action='store_true');a=p.parse_args()
 if a.verify:
  c,*_=verify();print(json.dumps({'sourceOnly':True,'executionLocked':c.get('executionBinding') is None,'core':c['graphProvider']['source'],'harness':c['derivedHarnessSource'],'productRebuilds':0}));sys.exit(0)
 if a.admit:sys.exit(admit())
 if a.collect:sys.exit(collect())
 sys.exit(run())
