#!/usr/bin/env python3
"""Exact official archived MinIO source -> actual private ELF. Cloud only caller."""
import hashlib,json,os,stat,struct
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
