#!/usr/bin/env python3
"""Exact official archived MinIO source -> actual private ELF. Cloud only caller."""
import hashlib,json,os,stat,struct,re,time,signal,urllib.request,urllib.error,urllib.parse,zipfile
from pathlib import Path

def sha(raw):return hashlib.sha256(raw).hexdigest()
def private_json(path,value):
 raw=(json.dumps(value,separators=(',',':'),ensure_ascii=False)+'\n').encode()
 fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
 with os.fdopen(fd,'wb') as f:
  if f.write(raw)!=len(raw):raise ValueError('short private write')
  f.flush();os.fsync(f.fileno())
 fd=os.open(path.parent,os.O_RDONLY|os.O_DIRECTORY)
 try:os.fsync(fd)
 finally:os.close(fd)
 return sha(raw)
def stable(path,maximum=128<<20):
 fd=os.open(path,os.O_RDONLY|os.O_NOFOLLOW)
 try:
  a=os.fstat(fd)
  if not stat.S_ISREG(a.st_mode) or a.st_uid!=os.getuid() or a.st_nlink!=1 or a.st_size>maximum:raise ValueError('owned stable file required')
  raw=b''
  while len(raw)<=maximum:
   part=os.read(fd,min(1<<20,maximum+1-len(raw)))
   if not part:break
   raw+=part
  b=os.fstat(fd)
  if (a.st_dev,a.st_ino,a.st_size,a.st_mtime_ns,a.st_ctime_ns)!=(b.st_dev,b.st_ino,b.st_size,b.st_mtime_ns,b.st_ctime_ns) or len(raw)!=a.st_size:raise ValueError('source changed while reading')
  return raw
 finally:os.close(fd)
def tracked_vector(source,listing):
 rows=[];seen=set()
 for value in listing.split(b'\0'):
  if not value:continue
  identity,name=value.split(b'\t',1);mode,object_id,stage=identity.decode().split(' ');name=name.decode('utf-8','strict')
  if stage!='0' or name in seen or Path(name).is_absolute() or '..' in Path(name).parts:raise ValueError('exact tracked path required')
  seen.add(name);path=source/name
  if mode=='120000':
   target=os.readlink(path)
   if Path(target).is_absolute() or not (path.parent/target).resolve().is_relative_to(source):raise ValueError('source symlink escapes owned checkout')
   raw=target.encode();observed_mode=0o777
  elif mode in ('100644','100755'):
   raw=stable(path,16<<20);observed_mode=stat.S_IMODE(path.stat().st_mode)
   if observed_mode & 0o022:raise ValueError('source may not be group/other writable')
  else:raise ValueError('submodule or unsupported tracked type')
  # Original Git object identity is independently checked, not replaced by a
  # new bare SHA256 vector. Checkout may remove read/exec bits under umask077.
  if hashlib.sha1(b'blob '+str(len(raw)).encode()+b'\0'+raw).hexdigest()!=object_id:raise ValueError('tracked Git body differs')
  rows.append({'path':name,'mode':observed_mode,'bytes':len(raw),'sha256':sha(raw)})
 if not rows:raise ValueError('empty source cannot build MinIO')
 rows.sort(key=lambda r:r['path'])
 raw=json.dumps(rows,sort_keys=True,separators=(',',':')).encode()
 return rows,sha(raw)
def validate_static_elf(raw):
 if raw[:4]!=b'\x7fELF' or len(raw)<64 or raw[4:6]!=b'\x02\x01' or struct.unpack_from('<H',raw,18)[0]!=62:raise ValueError('actual linux amd64 ELF required')
 start=struct.unpack_from('<Q',raw,32)[0];size,count=struct.unpack_from('<HH',raw,54)
 if size<56 or start+size*count>len(raw):raise ValueError('ELF program table rejected')
 if any(struct.unpack_from('<I',raw,start+i*size)[0]==3 for i in range(count)):raise ValueError('CGO0 ELF must not require a loader outside base')

def build(work,catalog,command,go,toolchain_sha,env):
 c=catalog['minio']
 if c['mode']!='source-elf' or c['status']!='historical_unmaintained_test_only' or c['repository']!='https://github.com/minio/minio' or c['cgoEnabled']!='0' or c['expectedELFSHA256'] is not None:raise ValueError('explicit exact official source build required')
 root=work/'minio-source-build';root.mkdir(mode=0o700)
 source=root/'source';source.mkdir(mode=0o700)
 git_env=dict(env,GIT_CONFIG_NOSYSTEM='1',GIT_CONFIG_GLOBAL='/dev/null',GIT_TERMINAL_PROMPT='0',GIT_ASKPASS='/bin/false')
 command('minio-git-init',['/usr/bin/git','init',str(source)],30,git_env)
 # Only an exact approved commit is fetched; no moving branch or credential
 # helper/user global Git configuration participates in source identity.
 command('minio-git-fetch',['/usr/bin/git','-C',str(source),'-c','credential.helper=','fetch','--depth=1','--no-tags',c['repository'],c['commit']],180,git_env)
 command('minio-git-checkout',['/usr/bin/git','-C',str(source),'checkout','--detach','FETCH_HEAD'],30,git_env)
 def git(label,args):return command(label,['/usr/bin/git','-C',str(source),*args],30,git_env).strip()
 if git('minio-git-head',['rev-parse','HEAD']).decode()!=c['commit'] or git('minio-git-tree',['rev-parse','HEAD^{tree}']).decode()!=c['tree']:raise ValueError('official fixed commit/tree differs')
 listing=git('minio-git-files',['ls-files','--stage','-z'])
 before,digest=tracked_vector(source,listing)
 for path,key in (('go.mod','goMod'),('go.sum','goSum')):
  body=stable(source/path)
  if sha(body)!=c[key]['sha256'] or len(body)!=c[key]['bytes']:raise ValueError('original official module lock differs')
 private_json(root/'source-vector.json',before)
 primary=None
 try:
  local=dict(env,CGO_ENABLED='0',GOAUTH='off',GOPRIVATE='',GONOPROXY='',GONOSUMDB='')
  for key,name in (('GOMODCACHE','modcache'),('GOCACHE','buildcache'),('GOPATH','gopath'),('TMPDIR','tmp')):
   path=root/name;path.mkdir(mode=0o700);local[key]=str(path)
  local['GOTMPDIR']=local['TMPDIR']
  download=dict(local,GOPROXY='https://proxy.golang.org',GOSUMDB='sum.golang.org')
  command('minio-module-download',[go,'mod','download','-json'],600,download,cwd=source)
  if tracked_vector(source,listing)[0]!=before:raise ValueError('source/lock changed during dependency acquisition')
  binary=root/'minio';offline=dict(local,GOPROXY='off',GOSUMDB='off')
  command('minio-source-compile',[go,'build','-mod=readonly','-p=1','-trimpath','-buildvcs=false','-o',str(binary),'./'],1200,offline,cwd=source)
  raw=stable(binary,256<<20);validate_static_elf(raw)
 except BaseException as error:primary=error;raise
 finally:
  post={'accepted':False,'sourceDigestSHA256':digest,'commit':c['commit'],'tree':c['tree']}
  try:
   if tracked_vector(source,listing)[0]!=before or git('minio-post-head',['rev-parse','HEAD']).decode()!=c['commit'] or git('minio-post-tree',['rev-parse','HEAD^{tree}']).decode()!=c['tree']:raise ValueError('source/lock/vector/commit changed during compile')
   post['accepted']=True
  except Exception as error:post['exceptionType']=type(error).__name__
  try:private_json(root/'source-post.json',post)
  except Exception:
   if primary is None:raise
  if not post['accepted'] and primary is None:raise ValueError('MinIO source post rejected')
 # Keys/order/LF match the actual strict Go receipt codec exactly.
 receipt={'version':'owned-minio-source-build-receipt-v1','repository':c['repository'],'commit':c['commit'],'sourceDigestSHA256':digest,'goModSHA256':c['goMod']['sha256'],'goSumSHA256':c['goSum']['sha256'],'toolchainSHA256':toolchain_sha,'cgoEnabled':'0','binarySHA256':sha(raw),'binaryBytes':len(raw)}
 receipt_path=root/'build-receipt.json';receipt_sha=private_json(receipt_path,receipt)
 dto={'version':'owned-minio-source-elf-v1','path':str(binary),'SHA256':sha(raw),'bytes':len(raw),'receiptPath':str(receipt_path),'receiptSHA256':receipt_sha,'repository':c['repository'],'commit':c['commit'],'sourceDigestSHA256':digest,'goModSHA256':c['goMod']['sha256'],'goSumSHA256':c['goSum']['sha256'],'toolchainSHA256':toolchain_sha,'cgoEnabled':'0'}
 return dto,receipt

# Fixed fixture reuse is separate from the product14 accepted/build-only gates.
FIXTURE_POLICY={'version':'owned-minio-fixture-artifact-v1','enabled':True,
 'binaryLimitBytes':256<<20,'archiveLimitBytes':257<<20,'metadataLimitBytes':65536,
 'downloadSeconds':300,'names':['minio','build-receipt.json','manifest.json'],
 'retentionDays':7,'compressionLevel':1,'fixtureOnly':True,'runtimeAccepted':False,
 'readyPermit':False,'copyBinary':False,'sameRepositoryOnly':True}
PROOF_KEYS={'version','repository','commit','sourceDigestSHA256','goModSHA256','goSumSHA256',
 'toolchainSHA256','cgoEnabled','binarySHA256','binaryBytes'}

def fixture_policy(c):
 if c.get('minioFixturePolicy')!=FIXTURE_POLICY or 'minioFixtureBinding' not in c:raise ValueError('explicit MinIO fixture policy and binding required')
 binding=c['minioFixtureBinding']
 if binding is not None:
  keys={'rootFixtureAccepted','rootAcceptanceSHA256','repository','run','head','attempt','jobID',
   'jobConclusion','runConclusion','artifactID','artifactBytes','artifactSHA256','manifestSHA256','receipt'}
  if not isinstance(binding,dict) or set(binding)!=keys or binding['rootFixtureAccepted'] is not True:raise ValueError('Root accepted fixture binding required')
  if any(type(binding[n]) is not int or binding[n]<=0 for n in ('run','attempt','jobID','artifactID','artifactBytes')) or binding['attempt']!=1 or binding['artifactBytes']>FIXTURE_POLICY['archiveLimitBytes']:raise ValueError('finite fixture API identity required')
  if binding['jobConclusion'] not in ('success','failure') or binding['runConclusion'] not in ('success','failure') or not re.fullmatch('[0-9a-f]{40}',binding['head']):raise ValueError('actual terminal fixture producer required')
  if any(not re.fullmatch('[0-9a-f]{64}',binding[n]) for n in ('rootAcceptanceSHA256','artifactSHA256','manifestSHA256')) or not re.fullmatch('[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+',binding['repository']):raise ValueError('finite fixture hash/repository required')
  proof(binding['receipt'])
 return binding

def proof(value,catalog=None,toolchain_sha=None):
 if not isinstance(value,dict) or set(value)!=PROOF_KEYS or value['version']!='owned-minio-source-build-receipt-v1' or value['repository']!='https://github.com/minio/minio' or value['cgoEnabled']!='0' or not re.fullmatch('[0-9a-f]{40}',value['commit']):raise ValueError('exact original MinIO proof required')
 if any(not re.fullmatch('[0-9a-f]{64}',value[n]) for n in ('sourceDigestSHA256','goModSHA256','goSumSHA256','toolchainSHA256','binarySHA256')) or type(value['binaryBytes']) is not int or not 64<=value['binaryBytes']<=256<<20:raise ValueError('bounded original fixture body required')
 if catalog is not None:
  c=catalog['minio']
  if c['mode']!='source-elf' or c['status']!='historical_unmaintained_test_only' or c['repository']!=value['repository'] or c['commit']!=value['commit'] or c['goMod']['sha256']!=value['goModSHA256'] or c['goSum']['sha256']!=value['goSumSHA256'] or c['cgoEnabled']!=value['cgoEnabled']:raise ValueError('current fixed fixture source/locks differ')
 if toolchain_sha is not None and value['toolchainSHA256']!=toolchain_sha:raise ValueError('original fixture Go toolchain differs')
 return value

def stream_static_elf(path):
 # No whole-ELF allocation and no executable invocation, including on reuse.
 path=Path(path)
 if not path.is_absolute() or path.resolve()!=path:raise ValueError('canonical fixture executable required')
 fd=os.open(path,os.O_RDONLY|os.O_NOFOLLOW)
 try:
  before=os.fstat(fd)
  if not stat.S_ISREG(before.st_mode) or before.st_uid!=os.getuid() or before.st_nlink!=1 or not 64<=before.st_size<=256<<20 or stat.S_IMODE(before.st_mode)!=0o700:raise ValueError('private single-link fixture executable required')
  header=os.pread(fd,64,0)
  if header[:6]!=b'\x7fELF\x02\x01' or struct.unpack_from('<H',header,18)[0]!=62:raise ValueError('linux amd64 fixture ELF required')
  start=struct.unpack_from('<Q',header,32)[0];size,count=struct.unpack_from('<HH',header,54)
  if size<56 or count>4096 or start+size*count>before.st_size:raise ValueError('bounded fixture program headers required')
  if any(struct.unpack('<I',os.pread(fd,4,start+i*size))[0]==3 for i in range(count)):raise ValueError('CGO0 fixture may not require PT_INTERP')
  h=hashlib.sha256();total=0
  while True:
   block=os.read(fd,1<<20)
   if not block:break
   total+=len(block)
   if total>before.st_size:raise ValueError('fixture grew while hashing')
   h.update(block)
  keys=lambda s:(s.st_dev,s.st_ino,s.st_uid,s.st_gid,s.st_mode,s.st_nlink,s.st_size,s.st_mtime_ns,s.st_ctime_ns)
  if total!=before.st_size or keys(before)!=keys(os.fstat(fd)) or keys(before)!=keys(path.lstat()):raise ValueError('fixture body changed while hashing')
  return {'name':'minio','bytes':total,'sha256':h.hexdigest(),'mode':0o700,'GOOS':'linux','GOARCH':'amd64','CGO_ENABLED':'0'}
 finally:os.close(fd)

def fixture_dto(root,value):
 receipt=root/'build-receipt.json'
 raw=stable(receipt,65536)
 if stat.S_IMODE(receipt.stat().st_mode)!=0o600 or raw!=json.dumps(value,separators=(',',':')).encode()+b'\n':raise ValueError('original canonical private receipt required')
 actual=stream_static_elf(root/'minio')
 if actual['sha256']!=value['binarySHA256'] or actual['bytes']!=value['binaryBytes']:raise ValueError('fixture body differs from original source receipt')
 return {'version':'owned-minio-source-elf-v1','path':str(root/'minio'),'SHA256':actual['sha256'],'bytes':actual['bytes'],
  'receiptPath':str(receipt),'receiptSHA256':sha(raw),**{k:value[k] for k in ('repository','commit','sourceDigestSHA256','goModSHA256','goSumSHA256','toolchainSHA256','cgoEnabled')}}

class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,req,fp,code,msg,headers,newurl):return None

def acquire_archive(binding,destination,token,repository):
 if repository!=binding['repository'] or not token:raise ValueError('same-repository actions read required')
 if signal.getitimer(signal.ITIMER_REAL)!=(0.0,0.0):raise ValueError('fixture transfer must not replace another timer')
 previous=signal.getsignal(signal.SIGALRM)
 def expired(_signal,_frame):raise TimeoutError('fixture transfer time cap')
 until=time.monotonic()+300
 def remaining():
  left=until-time.monotonic()
  if left<=0:raise ValueError('fixture transfer time cap')
  return min(30,left)
 opener=urllib.request.build_opener(NoRedirect());base='https://api.github.com/repos/'+repository
 headers={'Authorization':'Bearer '+token,'Accept':'application/vnd.github+json','X-GitHub-Api-Version':'2022-11-28'}
 def api(suffix):
  with opener.open(urllib.request.Request(base+suffix,headers=headers),timeout=remaining()) as response:
   raw=response.read((2<<20)+1)
   if len(raw)>2<<20:raise ValueError('fixture API metadata cap')
   return json.loads(raw)
 try:
  signal.signal(signal.SIGALRM,expired);signal.setitimer(signal.ITIMER_REAL,300)
  a=api('/actions/artifacts/'+str(binding['artifactID']))
  if a.get('id')!=binding['artifactID'] or a.get('name')!='backend-fullgraph-minio-fixture-'+str(binding['run'])+'-'+str(binding['attempt']) or a.get('expired') is not False or a.get('size_in_bytes')!=binding['artifactBytes'] or a.get('digest')!='sha256:'+binding['artifactSHA256'] or a.get('workflow_run',{}).get('id')!=binding['run'] or a['workflow_run'].get('head_sha')!=binding['head']:raise ValueError('fixture artifact API identity differs')
  r=api('/actions/runs/'+str(binding['run']));j=api('/actions/jobs/'+str(binding['jobID']))
  if r.get('head_sha')!=binding['head'] or r.get('run_attempt')!=binding['attempt'] or r.get('status')!='completed' or r.get('conclusion')!=binding['runConclusion'] or j.get('run_id')!=binding['run'] or j.get('run_attempt')!=binding['attempt'] or j.get('head_sha')!=binding['head'] or j.get('status')!='completed' or j.get('conclusion')!=binding['jobConclusion']:raise ValueError('actual terminal fixture producer differs')
  try:
   opener.open(urllib.request.Request(base+'/actions/artifacts/'+str(binding['artifactID'])+'/zip',headers=headers),timeout=remaining())
   raise ValueError('fixture signed redirect required')
  except urllib.error.HTTPError as error:
   if error.code!=302:raise
   url=error.headers['Location']
  u=urllib.parse.urlsplit(url)
  if u.scheme!='https' or not u.hostname or not u.hostname.endswith('.blob.core.windows.net') or u.username or u.password or u.port not in (None,443):raise ValueError('fixed anonymous artifact host required')
  digest=hashlib.sha256();total=0
  # The anonymous request has no API headers or Bearer and follows no redirect.
  with opener.open(urllib.request.Request(url),timeout=remaining()) as response,destination.open('xb') as target:
   if response.status!=200:raise ValueError('complete original fixture blob required')
   while True:
    if time.monotonic()>until:raise ValueError('fixture transfer time cap')
    block=response.read(1<<20)
    if not block:break
    total+=len(block)
    if total>binding['artifactBytes']:raise ValueError('fixture compressed cap')
    target.write(block);digest.update(block)
   target.flush();os.fsync(target.fileno())
  if total!=binding['artifactBytes'] or digest.hexdigest()!=binding['artifactSHA256']:raise ValueError('fixture archive body differs')
 except Exception:
  # No signed URL, credential, raw API body or dynamic HTTP error is logged.
  raise ValueError('fixed fixture artifact acquisition rejected') from None
 finally:
  signal.setitimer(signal.ITIMER_REAL,0);signal.signal(signal.SIGALRM,previous)

def unpack_fixture(archive,root,binding,catalog,toolchain_sha):
 value=proof(binding['receipt'],catalog,toolchain_sha)
 with zipfile.ZipFile(archive) as zipped:
  rows=zipped.infolist()
  if len(rows)!=3 or {r.filename for r in rows}!=set(FIXTURE_POLICY['names']) or len({r.filename for r in rows})!=3 or sum(r.file_size for r in rows)>(256<<20)+(128<<10):raise ValueError('three exact flat fixture entries required')
  for r in rows:
   if r.flag_bits&1 or stat.S_IFMT(r.external_attr>>16) not in (0,stat.S_IFREG) or not 0<r.file_size<=(256<<20 if r.filename=='minio' else 65536):raise ValueError('fixture entry type or size rejected')
   path=root/r.filename;fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
   with zipped.open(r) as source,os.fdopen(fd,'wb') as target:
    total=0
    while True:
     block=source.read(1<<20)
     if not block:break
     total+=len(block)
     if total>r.file_size:raise ValueError('fixture uncompressed cap')
     target.write(block)
    target.flush();os.fsync(target.fileno())
   if total!=r.file_size:raise ValueError('complete CRC-checked fixture body required')
 raw=stable(root/'manifest.json',65536)
 if sha(raw)!=binding['manifestSHA256']:raise ValueError('fixture producer manifest differs')
 manifest=json.loads(raw)
 if (manifest.get('version')!=FIXTURE_POLICY['version'] or manifest.get('fixtureOnly') is not True
  or manifest.get('runtimeAccepted') is not False or manifest.get('readyPermit') is not False
  or manifest.get('repository')!=binding['repository'] or manifest.get('head')!=binding['head']
  or manifest.get('run')!=binding['run'] or manifest.get('attempt')!=binding['attempt']
  or manifest.get('receipt')!=value or manifest.get('sourceToolPostExact') is not True
  or manifest.get('receiptSHA256')!=sha(stable(root/'build-receipt.json',65536))
  or manifest.get('actualPlanePrepared') is not True or manifest.get('exactSevenContainerCleanup') is not True
  or manifest.get('binary')!={'name':'minio','bytes':value['binaryBytes'],'sha256':value['binarySHA256'],'mode':0o700,'GOOS':'linux','GOARCH':'amd64','CGO_ENABLED':'0'}):raise ValueError('fixture-only original producer obligations differ')
 (root/'minio').chmod(0o700)  # The standard artifact transport loses executable mode.
 return fixture_dto(root,value),value

def receive(work,catalog,binding,toolchain_sha,token,repository):
 fixture_policy({'minioFixturePolicy':FIXTURE_POLICY,'minioFixtureBinding':binding})
 root=work/'minio-source-build';root.mkdir(mode=0o700)
 proof(binding['receipt'],catalog,toolchain_sha)
 archive=work/'minio-fixture.zip.part'
 acquire_archive(binding,archive,token,repository)
 dto,value=unpack_fixture(archive,root,binding,catalog,toolchain_sha)
 private_json(root/'acquisition.json',{'mode':'root-accepted-fixture-artifact','artifactID':binding['artifactID'],
  'artifactSHA256':binding['artifactSHA256'],'manifestSHA256':binding['manifestSHA256'],
  'rootAcceptanceSHA256':binding['rootAcceptanceSHA256'],'freshSourceBuild':False,'fixtureOnly':True})
 return dto,value

def checked_prepared_planes(root,collection,dto,run):
 used=[]
 for scenario in collection['scenarios']:
  if scenario.get('scenario') not in run.SCENARIOS or type(scenario.get('resourceStarted')) is not bool:raise ValueError('finite original scenario required')
  if not scenario.get('resourceStarted'):continue
  folder=root/('plane-'+scenario['scenario']);private=folder/'private'
  claim=json.loads(run.regular(private/'plane.json',1<<20));owner=json.loads(run.regular(private/'owner.json',1<<20))
  cleanup=scenario.get('resourceCleanup',{});observations=claim.get('observations',[]);ids=cleanup.get('exactContainerIDs',[])
  roles=('keeper','mongo','redis','zookeeper','kafka','minio','kafka-format')
  expected={role:run.regular(private/(role+'.cid'),128).decode('ascii').strip() for role in roles}
  nonce=claim.get('nonce','')
  if (not re.fullmatch('[0-9a-f]{32}',nonce) or owner.get('nonce')!=nonce
   or owner.get('version')!='fullgraph-resource-owner-v1' or owner.get('minioSource')!=dto
   or owner.get('uid')!=os.getuid() or claim.get('resourceRoot')!=str(folder) or claim.get('uid')!=os.getuid()
   or claim.get('version')!='owned-fullgraph-resource-plane-v1' or claim.get('minioMode')!='source-elf'
   or claim.get('minioSource')!=dto or len(observations)!=6
   or {r['role'] for r in observations}!=set(roles)-{'kafka-format'}
   or any(type(r.get('PID')) is not int or r['PID']<=1 or r.get('id')!=expected[r['role']] for r in observations)
   or any(not re.fullmatch('[0-9a-f]{64}',i) for i in expected.values())
   or cleanup.get('accepted') is not True or cleanup.get('unconfirmedIDs')!=[]
   or cleanup.get('ownerNonceSHA256')!=sha(nonce.encode())
   or len(ids)!=7 or len(set(ids))!=7 or set(ids)!=set(expected.values())):
   raise ValueError('actual original prepared plane and exact seven-container cleanup required')
  used.append(scenario['scenario'])
 if not used:raise ValueError('fixture must actually be used by a prepared plane')
 return used

def checked_fixture_evidence(compact,c,allocation,run):
 # This is an independent fixed-tool gate. Product/runtime acceptance and
 # business-child cleanup are neither required nor inferred by this producer.
 custody_raw=run.regular(compact/'custody.json',1<<20);custody=json.loads(custody_raw)
 rows=custody['files']
 if not isinstance(rows,list) or not 1<=len(rows)<=256:raise ValueError('finite original compact custody required')
 names=[];evidence={};total=0
 for row in rows:
  name=row['path']
  if (not isinstance(name,str) or not re.fullmatch('[A-Za-z0-9][A-Za-z0-9_.-]{0,127}',name)
   or name in names or name=='custody.json' or set(row)!={'path','bytes','sha256'}
   or type(row['bytes']) is not int or not 0<=row['bytes']<=1<<20
   or not isinstance(row['sha256'],str) or not re.fullmatch('[0-9a-f]{64}',row['sha256'])):raise ValueError('exact finite custody row required')
  names.append(name);raw=run.regular(compact/name,1<<20);total+=len(raw)
  if len(raw)!=row['bytes'] or sha(raw)!=row['sha256']:raise ValueError('original compact evidence changed')
  evidence[name]=raw
 if ({p.name for p in compact.iterdir()}!=set(names)|{'custody.json'} or total>8<<20
  or type(custody.get('bodyBytes')) is not int or total!=custody['bodyBytes']
  or any(type(custody.get(k)) is not int or custody[k]!=0 for k in ('binaries','credentials','privateLogs'))):raise ValueError('bounded exact public compact custody required')
 def read(name):
  if name not in evidence:raise ValueError('original required fixture evidence missing')
  return json.loads(evidence[name])
 post=read('source-post.json');before=read('tools-before.json');after=read('tools-after.json')
 monitor=read('backend-fullgraph-controller.resource.json');collection=read('collection.json')
 if post.get('accepted') is not True or post.get('source')!=c['source'] or before!=after or run.tool_snapshot()!=before:raise ValueError('actual original source and hosted tool post required')
 def guard(record,root,failed=False):
  floor=record.get('minimum_observed_free_bytes');size=record.get('maximum_observed_task_allocated_bytes')
  if (record.get('guard_revision')!=2 or record.get('environment_values_recorded') is not False
   or record.get('allocated_root')!=str(root) or record.get('minimum_free_required_bytes')!=1<<30
   or type(floor) is not int or floor<1<<30 or record.get('max_allocated_allowed_bytes')!=16<<30
   or type(size) is not int or not 0<=size<=16<<30):raise ValueError('original healthy fixture resource boundary required')
  code=record.get('exit_code')
  if type(code) is not int or (failed and (record.get('status')!='failed' or not 1<=code<=125)) or (not failed and (record.get('status')!='passed' or code!=0)):raise ValueError('normal passed or finite runtime failure only')
 failed_controller=monitor.get('status')=='failed';guard(monitor,allocation,failed_controller)
 phase_names=sorted(name for name in names if re.fullmatch(r'phase-[0-9]{4}\.json',name))
 if not phase_names or phase_names!=['phase-%04d.json'%i for i in range(1,len(phase_names)+1)] or any(name.startswith('phase-') and name not in phase_names for name in names):raise ValueError('finite contiguous original phase names required')
 phases=[read(name) for name in phase_names];failed=[]
 for row in phases:
  code=row.get('exitCode');label=row.get('phase')
  if type(code) is not int or not isinstance(label,str) or not re.fullmatch('[a-z0-9-]{1,128}',label):raise ValueError('finite original command phase required')
  guard(row['resource'],allocation/'work',code!=0)
  if row['resource']['exit_code']!=code:raise ValueError('original phase and resource exits differ')
  if code:failed.append(row)
 if failed_controller:
  if len(failed)!=1 or phases[-1]!=failed[0] or failed[0]['phase'] not in {'runtime-'+s for s in run.SCENARIOS}:raise ValueError('only an original finite runtime failure may retain fixture')
 elif failed:raise ValueError('passed controller cannot hide a failed phase')
 actual=[row for row in phases if row['phase']=='minio-source-compile']
 if len(actual)!=1 or actual[0]['exitCode']!=0:raise ValueError('actual original MinIO compilation required')
 if (collection.get('started') is not True or type(collection.get('accepted')) is not bool
  or collection.get('controllerPassed') is not (not failed_controller)
  or collection.get('originalRetainedRuntimeJoinRequired') is not True
  or collection.get('containerAbsenceIsNotRuntimeDrain') is not True or collection.get('privateDataUploaded') is not False
  or [row['scenario'] for row in collection['scenarios']]!=list(run.SCENARIOS)):raise ValueError('original complete always collection required')
 unknown=[]
 for row in collection['scenarios']:
  started=row.get('resourceStarted');attempted=row.get('runtimeAttempted');joined=row.get('runtimeCleanupAccepted')
  if any(type(v) is not bool for v in (started,attempted,joined)) or attempted and not started:raise ValueError('original finite resource/runtime state required')
  if attempted and not joined:unknown.append(row['scenario'])
 if failed and next(row for row in collection['scenarios'] if 'runtime-'+row['scenario']==failed[0]['phase'])['runtimeAttempted'] is not True:raise ValueError('original failed runtime attempt missing')
 minio_post=read('source-minio-post.json')
 if minio_post.get('accepted') is not True:raise ValueError('original source-MinIO post rejected')
 return {'tools':before,'monitor':monitor,'collection':collection,'sourceMinioPost':minio_post,
  'sourceMinioBuild':read('source-minio-build.json'),'runtimeCleanupUnconfirmedScenarios':unknown,
  'custodySHA256':sha(custody_raw),'collectionSHA256':sha(evidence['collection.json']),'sourcePostSHA256':sha(evidence['source-post.json'])}

def prepare_fixture_artifact(run,collector):
 c,_,_,_,_,catalog=run.verify();fixture_policy(c)
 if c['minioFixtureBinding'] is not None:
  print(json.dumps({'kind':'minio-fixture-already-bound','artifactPrepared':False,'newBuild':False}));return 0
 allocation=run.allocation_root();root=allocation/'work';directory=root/'minio-source-build'
 if directory.resolve()!=directory or stat.S_IMODE(directory.stat().st_mode)!=0o700 or directory.stat().st_uid!=os.getuid() or json.loads(run.regular(allocation/'allocation-owner.json'))!=run.job_identity():raise ValueError('original fixture allocation required')
 compact=run.REPO/'ci-results/backend-fullgraph-compact'
 checked=checked_fixture_evidence(compact,c,allocation,run);monitor=checked['monitor']
 before=checked['tools'];inputs_raw=run.regular(run.INPUTS,1<<20)
 if run.tool_snapshot()!=before:raise ValueError('actual fixture hosted tool post differs')
 source_post=json.loads(run.regular(directory/'source-post.json',65536))
 value=proof(json.loads(run.regular(directory/'build-receipt.json',65536)),catalog,before['go']['sha256'])
 if source_post!=checked['sourceMinioPost'] or value!=checked['sourceMinioBuild'] or source_post.get('accepted') is not True or source_post.get('sourceDigestSHA256')!=value['sourceDigestSHA256'] or source_post.get('commit')!=catalog['minio']['commit'] or source_post.get('tree')!=catalog['minio']['tree']:raise ValueError('original source-MinIO post rejected')
 dto=fixture_dto(directory,value);collection=checked['collection']
 used=checked_prepared_planes(root,collection,dto,run)
 binary=stream_static_elf(directory/'minio');run.verify()
 if run.regular(run.INPUTS,1<<20)!=inputs_raw or run.tool_snapshot()!=before or fixture_dto(directory,value)!=dto:raise ValueError('fixture producer final boundary changed')
 job=run.job_identity()
 manifest={'version':FIXTURE_POLICY['version'],'fixtureOnly':True,'runtimeAccepted':False,'readyPermit':False,
  'repository':os.environ['GITHUB_REPOSITORY'],'head':job['head'],'run':int(job['runID']),'attempt':int(job['attempt']),
  'producerControllerStatus':monitor['status'],'producerControllerExit':monitor['exit_code'],
  'businessRuntimeAccepted':False,'businessRuntimeCleanupStatus':'not-asserted-by-fixture-producer',
  'businessRuntimeCleanupUnconfirmedScenarios':checked['runtimeCleanupUnconfirmedScenarios'],
  'producerJobConclusion':'not-observed-before-upload','graphSource':c['source'],'sourceToolPostExact':True,
  'actualPlanePrepared':True,'preparedScenarios':used,'exactSevenContainerCleanup':True,
  'receipt':value,'receiptSHA256':dto['receiptSHA256'],'binary':binary,
  'compactCustodySHA256':checked['custodySHA256'],'collectionSHA256':checked['collectionSHA256'],
  'sourcePostSHA256':checked['sourcePostSHA256'],'controllerInputsSHA256':sha(inputs_raw),
  'contents':['minio','build-receipt.json','manifest.json'],'noConfigLogSourceCredentialsOrProductELF':True,
  'artifactUploadedByScript':False,'deploymentAccepted':False}
 digest=private_json(directory/'manifest.json',manifest)
 private_json(allocation/'minio-fixture-artifact-ready.json',{'fixtureOnly':True,'manifestSHA256':digest,'binarySHA256':binary['sha256'],'bytes':binary['bytes'],'runtimeAccepted':False,'readyPermit':False})
 print(json.dumps({'kind':'minio-fixture-artifact-prepared','manifestSHA256':digest,'bytes':binary['bytes'],
  'fixtureOnly':True,'runtimeAccepted':False,'readyPermit':False,'uploadedByScript':False},separators=(',',':')))
 return 0

if __name__=='__main__':
 import argparse,importlib.util
 parser=argparse.ArgumentParser(description='Seal only this original source-built MinIO fixture; never build or start anything.')
 parser.add_argument('--seal-fixture',action='store_true',required=True);parser.parse_args()
 here=Path(__file__).resolve().parent
 def imported(name):
  spec=importlib.util.spec_from_file_location('minio_fixture_'+name,here/(name+'.py'));m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m
 try:raise SystemExit(prepare_fixture_artifact(imported('run'),imported('collect')))
 except Exception:
  print(json.dumps({'kind':'minio-fixture-artifact-refused','reason':'source-tool-receipt-use-or-cleanup-rejected','artifactPrepared':False}));raise SystemExit(1)
