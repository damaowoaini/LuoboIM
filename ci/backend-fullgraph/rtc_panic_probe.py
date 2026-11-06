#!/usr/bin/env python3
"""One private stdlib-only real panic; its output grants no runtime authority."""
import importlib.util,json,os,re,resource,stat,subprocess,sys
from pathlib import Path
LIMIT=65536
TARGET='server/internal/rpc/rtc/owned_startup.go'
LABELS=['rtc-user-activation','rtc-phase-lookup','rtc-cause-sdk-user-missing']

def closed_payload(raw):
 if not isinstance(raw,bytes) or len(raw)>65536:raise ValueError('bounded original RTC source required')
 text=raw.decode('utf-8');error=re.findall(r'func \(e \*ownedRTCActivationFailure\) Error\(\) string \{([^{}]+)\}',text)
 expected='return errOwnedRTCUserActivation.Error() + "\\nowned RTC activation phase " + e.phase.label() + "\\nowned RTC activation cause " + e.causeClass'
 if len(error)!=1 or ' '.join(error[0].split())!=expected:raise ValueError('exact original closed Error writer required')
 if len(re.findall(r'var errOwnedRTCUserActivation = errors.New\("owned RTC service user activation incomplete"\)',text))!=1 or len(re.findall(r'case rtcActivationLookup:\s*return "lookup"',text))!=1 or len(re.findall(r'case errs.ErrUserIDNotFound.Code\(\):\s*return "sdk-user-missing"',text))!=1:raise ValueError('original sentinel and phase/cause classes required')
 return 'owned RTC service user activation incomplete\nowned RTC activation phase lookup\nowned RTC activation cause sdk-user-missing'

def probe_source(raw):
 payload=closed_payload(raw)
 return ('package main\n\ntype runtimePanic struct{}\nvar _ error = runtimePanic{}\nfunc (runtimePanic) Error() string { return '+json.dumps(payload)+' }\nfunc main() { panic(runtimePanic{}) }\n').encode()

def project_observed(raw,source,bodies,collector):
 prefix=('panic: '+closed_payload(source).replace('\n','\n\t')+'\n\n').encode()
 if not isinstance(raw,bytes) or len(raw)>LIMIT or not raw.startswith(prefix):raise ValueError('real panic must retain exact runtime TAB prefix')
 value=collector.project_service_log_bytes(raw,bodies,collector.run.ORIGINAL_CONSTRUCTOR_LOG_POLICY,lambda status:collector.constructor_projection_empty(status,'rpc-rtc'),collector.constructor_error_prefix)
 if value['status']!='projected' or value['headerClass']!='go-panic' or value['errorPrefixClass']!='rtc-user-activation' or value['panicPrefixClasses']!=LABELS or value['scanTruncated']:raise ValueError('real runtime bytes did not pass original closed projector')
 return {'version':'rtc-real-runtime-continuation-control-v1','source':collector.run.FINAL_SOURCE,'probeExitCode':2,'headerClass':value['headerClass'],'panicPrefixClasses':value['panicPrefixClasses'],'continuation':'single-tab','originalRTCWaitResult':'not-exported','rawOutput':False,'privateDerivedMetadata':False,'acceptanceEvidence':False,'runtimePIDReadyP20Accepted':False}

def _identity(value):return (value.st_dev,value.st_ino,value.st_mode,value.st_uid,value.st_nlink,value.st_size,value.st_mtime_ns,value.st_ctime_ns)
def _regular(path,limit):
 flags=os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK|os.O_CLOEXEC;fd=os.open(path,flags)
 try:
  before=os.fstat(fd)
  if not stat.S_ISREG(before.st_mode) or before.st_uid!=os.getuid() or before.st_nlink!=1 or before.st_size>limit:raise ValueError('bounded private regular control file required')
  chunks=[];remaining=limit+1
  while remaining:
   part=os.read(fd,min(65536,remaining))
   if not part:break
   chunks.append(part);remaining-=len(part)
  raw=b''.join(chunks)
  if len(raw)>limit or _identity(before)!=_identity(os.fstat(fd)) or _identity(before)!=_identity(path.lstat()):raise ValueError('stable private control file required')
  return raw
 finally:os.close(fd)

def execute(directory,collector,bodies):
 directory=Path(directory);folder=directory.lstat()
 if directory.resolve()!=directory or not stat.S_ISDIR(folder.st_mode) or stat.S_IMODE(folder.st_mode)!=0o700 or folder.st_uid!=os.getuid():raise ValueError('canonical private probe directory required')
 source=bodies[TARGET]
 if _regular(directory/'panic.go',65536)!=probe_source(source):raise ValueError('original source-derived probe differs')
 binary=directory/'panic';before=binary.lstat()
 if binary.resolve()!=binary or not stat.S_ISREG(before.st_mode) or before.st_uid!=os.getuid() or before.st_nlink!=1 or stat.S_IMODE(before.st_mode)!=0o700 or not 0<before.st_size<=16<<20:raise ValueError('private bounded compiled probe required')
 def bounds():resource.setrlimit(resource.RLIMIT_FSIZE,(LIMIT,LIMIT));resource.setrlimit(resource.RLIMIT_CORE,(0,0))
 descriptors=[]
 try:
  for name in ('stdout','stderr'):
   fd=os.open(directory/name,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW|os.O_CLOEXEC,0o600);descriptors.append(os.fdopen(fd,'wb',buffering=0))
  child=subprocess.run([str(binary)],stdin=subprocess.DEVNULL,stdout=descriptors[0],stderr=descriptors[1],cwd=directory,env={'GOTRACEBACK':'single'},timeout=10,check=False,preexec_fn=bounds)
 finally:
  for file in descriptors:file.close()
 if child.returncode!=2 or _identity(before)!=_identity(binary.lstat()) or _regular(directory/'stdout',LIMIT):raise ValueError('isolated runtime probe did not finish with exact exit2')
 raw=_regular(directory/'stderr',LIMIT)
 return project_observed(raw,source,bodies,collector)

def run_control(go,env,private,out,bodies,command,controller):
 directory=private/'rtc-panic-runtime';directory.mkdir(mode=0o700)
 fd=os.open(directory/'panic.go',os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW|os.O_CLOEXEC,0o600)
 with os.fdopen(fd,'wb') as file:file.write(probe_source(bodies[TARGET]))
 buildenv=dict(env,CGO_ENABLED='0',GO111MODULE='off',GOFLAGS='',GOPROXY='off',GOSUMDB='off')
 command('rtc-panic-runtime-build',[go,'build','-trimpath','-o',str(directory/'panic'),str(directory/'panic.go')],180,buildenv,cwd=directory)
 (directory/'panic').chmod(0o700)
 raw=command('rtc-panic-runtime-execute',[sys.executable,'-B',str(Path(__file__).resolve()),'--execute',str(directory)],30,env,cwd=directory)
 value=json.loads(raw)
 expected={'version':'rtc-real-runtime-continuation-control-v1','source':controller.FINAL_SOURCE,'probeExitCode':2,'headerClass':'go-panic','panicPrefixClasses':LABELS,'continuation':'single-tab','originalRTCWaitResult':'not-exported','rawOutput':False,'privateDerivedMetadata':False,'acceptanceEvidence':False,'runtimePIDReadyP20Accepted':False}
 if value!=expected:raise ValueError('closed real runtime control receipt required')
 controller.write(out/'rtc-panic-runtime-control.json',value)
 return value

if __name__=='__main__':
 if len(sys.argv)!=3 or sys.argv[1]!='--execute':raise ValueError('private probe execute only')
 path=Path(__file__).resolve().parent/'collect.py';spec=importlib.util.spec_from_file_location('rtc_real_panic_collector',path);collector=importlib.util.module_from_spec(spec);spec.loader.exec_module(collector)
 _,bodies,_,_,_,_=collector.run.verify()
 print(json.dumps(execute(Path(sys.argv[2]),collector,bodies),sort_keys=True,separators=(',',':')))
