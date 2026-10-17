#!/usr/bin/env python3
"""Always inspect exact retained task custody; never adopt PID or fake a drain."""
import hashlib,importlib.util,json,os,stat,sys
from pathlib import Path
HERE=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('fullgraph_controller',HERE/'run.py')
run=importlib.util.module_from_spec(spec);spec.loader.exec_module(run)

def public_terminal(raw,scenario):
 value=json.loads(raw)
 if value.get('version')!='owned-fullgraph-token-v2-result-v1' or value.get('scenario')!=scenario or type(value.get('accepted')) is not bool:raise ValueError('finite actual terminal result required')
 bootstrap=value.get('bootstrapStage','')
 if bootstrap not in ('','plane-inputs','mongo','redis','kafka','kafka-coordinator','config','service-plan','capture-inputs','launcher-probe'):raise ValueError('closed bootstrap stage required')
 phase=value.get('phase','');kind=value.get('failureClass','');failure=value.get('failureSHA256','')
 tag=value.get('bootstrapFailureTag','')
 if tag not in ('','mongo-auth','mongo-unauthorized','mongo-not-primary','mongo-namespace-exists','mongo-other','kafka-auth','kafka-topic-or-cluster-authorization','kafka-unsupported-version','kafka-coordinator-unavailable','kafka-other','permission','no-space','not-found','connection-refused','deadline','cancel','other') or tag and phase!='actual-resource-bootstrap':raise ValueError('closed original bootstrap failure tag required')
 import re
 if not re.fullmatch('[a-z0-9-]{1,128}',phase) or kind not in ('','source-error-private','owned-evidence-rejected','commit-unconfirmed','deadline','cancelled') or failure and not re.fullmatch('[0-9a-f]{64}',failure):raise ValueError('unsafe terminal enum/hash')
 if any(v and not re.fullmatch('[0-9a-f]{64}',v) for v in (value.get('sourceSHA256',''),value.get('resourcePlaneSHA256',''))) or type(value.get('actualStartedRoles',0)) is not int or not 0<=value.get('actualStartedRoles',0)<=14:raise ValueError('finite source/role fields required')
 return {'version':value['version'],'scenario':scenario,'accepted':value['accepted'],'phase':phase,'bootstrapStage':bootstrap,'bootstrapFailureTag':tag,'failureClass':kind,'failureSHA256':failure,'sourceSHA256':value.get('sourceSHA256',''),'resourcePlaneSHA256':value.get('resourcePlaneSHA256',''),'actualStartedRoles':value.get('actualStartedRoles',0),'originalResultSHA256':run.sha(raw),'rawErrorText':False}

def pack(out):
 files=list(out.iterdir());total=0;bodies=[]
 for name in ('backend-fullgraph-admission.json','backend-fullgraph-controller.resource.json'):
  path=run.REPO/'ci-results'/name
  if path.exists():files.append(path)
 if len(files)>256:raise ValueError('compact artifact member limit')
 for path in sorted(files):
  if path.is_symlink() or not path.is_file() or path.name=='custody.json':raise ValueError('public artifact must be finite regular metadata')
  body=run.regular(path,1<<20);total+=len(body);bodies.append((path.name,body))
 if total>8<<20:raise ValueError('compact artifact exceeds8MiB before ZIP overhead')
 compact=run.REPO/'ci-results/backend-fullgraph-compact';compact.mkdir(mode=0o700,exist_ok=False)
 manifest=[]
 for name,body in bodies:
  fd=os.open(compact/name,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
  with os.fdopen(fd,'wb') as target:
   if target.write(body)!=len(body):raise ValueError('short compact copy')
   target.flush();os.fsync(target.fileno())
  manifest.append({'path':name,'bytes':len(body),'sha256':hashlib.sha256(body).hexdigest()})
 run.write(compact/'custody.json',{'files':manifest,'bodyBytes':total,'privateLogs':0,'binaries':0,'credentials':0,'decodedLimitBytes':64<<20,'compressedLimitBytes':16<<20})

def collect():
 allocation=run.allocation_root();out=run.REPO/'ci-results/backend-fullgraph';out.mkdir(mode=0o700,parents=True,exist_ok=True)
 if not allocation.exists():
  run.write(out/'collection.json',{'started':False,'accepted':False,'reason':'no-original-allocation','privateDataUploaded':False});pack(out);return 0
 if allocation.resolve()!=allocation or allocation.stat().st_uid!=os.getuid() or stat.S_IMODE(allocation.stat().st_mode)!=0o700 or json.loads(run.regular(allocation/'allocation-owner.json'))!=run.job_identity():raise ValueError('original job allocation required')
 root=allocation/'work';resources=run.module('ci/backend-fullgraph/resources.py','always_original_plane');rows=[];failed=False
 for scenario in run.SCENARIOS:
  folder=root/('plane-'+scenario);work=root/('scenario-'+scenario);entry={'scenario':scenario,'resourceStarted':False,'runtimeAttempted':False,'runtimeCleanupAccepted':False}
  # Merely having a partial folder does not authorize a cleanup. Only the
  # original durable owner record can reconstruct its exact attempted IDs.
  if (folder/'private/owner.json').exists():
   entry['resourceStarted']=True
   try:entry['resourceCleanup']=run.public_cleanup(resources.Plane.reopen(folder).cleanup())
   except Exception as error:entry['resourceCleanup']={'accepted':False,'exceptionType':type(error).__name__};failed=True
  if (work/'result.json').exists():
   try:run.write(out/('terminal-'+scenario+'.json'),public_terminal(run.regular(work/'result.json',1<<20),scenario))
   except Exception as error:entry['terminalExceptionType']=type(error).__name__;failed=True
  if (work/'operator-intent.json').exists():
   entry['runtimeAttempted']=True
   try:
    intent=json.loads(run.regular(work/'operator-intent.json'));planraw=run.regular(root/'private'/(scenario+'-input.json'));plan=json.loads(planraw);buildraw=run.regular(root/'private/builds.json')
    if run.sha(planraw)!=intent['privateInputSHA256'] or run.sha(buildraw)!=plan['buildsSHA256']:raise ValueError('original operator/build input body changed')
    builds=json.loads(buildraw);raw=run.regular(work/'result.json',1<<20);value=json.loads(raw)
    check=run.check_result(value,scenario,builds['sourceSHA256'],builds,plan['planeSHA256'])
    actual=value.get('cancellation',{}).get('roles',[]) if scenario.startswith('reader-') else value['roles']
    # Independent absence check only; no signal, /proc-derived capability,
    # recreation, Close or Stop is possible from this collector.
    if any(type(r['pid']) is not int or r['pid']<=1 or Path('/proc',str(r['pid'])).exists() for r in actual):raise ValueError('runtime original absence unconfirmed')
    entry['runtimeCleanupAccepted']=True;entry['actualAbsentRoles']=len(actual);entry['resultSHA256']=run.sha(raw)
   except Exception as error:entry['runtimeCleanupReason']='runtime-cleanup-unconfirmed';entry['runtimeExceptionType']=type(error).__name__;failed=True
  rows.append(entry)
 # Failure after complete source copy must still leave bounded body/mode/lock
 # evidence. A recovery inspection never overwrites a primary monitor failure.
 materialized=root/'private/source-materialized.json'
 if materialized.exists():
  postpath=out/'source-post.json'
  if not postpath.exists():
   try:
    c,bodies,_,_,_,_=run.verify();post=run.source_post(root,bodies,c,bodies['server/config/config.example.yaml'])
   except Exception as error:post={'accepted':False,'source':run.FINAL_SOURCE,'exceptionType':type(error).__name__}
   run.write(out/'source-post-recovery-inspection.json',post)
  else:post=json.loads(run.regular(postpath))
  failed=failed or post.get('accepted') is not True
 minio=root/'minio-source-build/source-post.json'
 if minio.exists():run.write(out/'source-minio-post.json',json.loads(run.regular(minio,65536)))
 controller=run.REPO/'ci-results/backend-fullgraph-controller.resource.json'
 monitor=json.loads(run.regular(controller)) if controller.exists() else {}
 summary=out/'summary.json';main=json.loads(run.regular(summary)) if summary.exists() else {}
 accepted=not failed and monitor.get('status')=='passed' and monitor.get('exit_code')==0 and main.get('accepted') is True and all(r['runtimeAttempted'] and r['runtimeCleanupAccepted'] and r.get('resourceCleanup',{}).get('accepted') is True for r in rows)
 run.write(out/'collection.json',{'started':True,'accepted':accepted,'scenarios':rows,'controllerPassed':monitor.get('status')=='passed','originalRetainedRuntimeJoinRequired':True,'privateDataUploaded':False,'containerAbsenceIsNotRuntimeDrain':True})
 pack(out)
 return 0 if accepted else 1

PRODUCT_FILE_LIMIT=256<<20
PRODUCT_TOTAL_LIMIT=2<<30
PRODUCT_ARTIFACT_POLICY={'enabled':True,'perBinaryLimitBytes':PRODUCT_FILE_LIMIT,'totalBinaryLimitBytes':PRODUCT_TOTAL_LIMIT,'allowedBasenames':sorted([*run.ROLES,'manifest.json']),'retentionDays':7,'compressionLevel':1,'copyBinaries':False,'deploymentAccepted':False}

def product_elf_metadata(path):
 # Read the original executable in bounded chunks; never copy a binary or
 # weaken the original build receipt to make an oversized artifact fit.
 path=Path(path)
 if path.resolve()!=path:raise ValueError('canonical original product executable required')
 fd=os.open(path,os.O_RDONLY|os.O_NOFOLLOW)
 try:
  before=os.fstat(fd)
  if not stat.S_ISREG(before.st_mode) or before.st_uid!=os.getuid() or before.st_nlink!=1 or not 64<=before.st_size<=PRODUCT_FILE_LIMIT or before.st_mode&(stat.S_ISUID|stat.S_ISGID|stat.S_ISVTX|0o022) or not before.st_mode&0o111:raise ValueError('owned bounded single-link executable required')
  digest=hashlib.sha256();size=0;header=b''
  while True:
   block=os.read(fd,1<<20)
   if not block:break
   if not header:header=block[:20]
   size+=len(block)
   if size>PRODUCT_FILE_LIMIT:raise ValueError('product executable exceeds256MiB')
   digest.update(block)
  after=os.fstat(fd);current=path.lstat()
  identity=lambda v:(v.st_dev,v.st_ino,v.st_uid,v.st_gid,v.st_mode,v.st_nlink,v.st_size,v.st_mtime_ns,v.st_ctime_ns)
  if identity(before)!=identity(after) or identity(before)!=identity(current) or size!=before.st_size:raise ValueError('original executable changed during sealing')
  if header[:6]!=b'\x7fELF\x02\x01' or header[16:18] not in (b'\x02\x00',b'\x03\x00') or header[18:20]!=b'\x3e\x00':raise ValueError('Linux amd64 ELF required')
  return {'name':path.name,'bytes':size,'sha256':digest.hexdigest(),'mode':stat.S_IMODE(before.st_mode),'GOOS':'linux','GOARCH':'amd64','CGO_ENABLED':'1'}
 finally:os.close(fd)

def checked_product_evidence(compact,c):
 # Consume the existing immutable compact custody; do not run collect again
 # or append/rewrite its already-uploadable evidence tree.
 custody=json.loads(run.regular(compact/'custody.json',1<<20))
 names=[r['path'] for r in custody['files']]
 if len(names)>256 or len(names)!=len(set(names)) or any(Path(n).name!=n for n in names) or {p.name for p in compact.iterdir()}!=set(names)|{'custody.json'}:raise ValueError('exact existing compact custody required')
 evidence={};total=0
 for row in custody['files']:
  raw=run.regular(compact/row['path'],1<<20);total+=len(raw)
  if len(raw)!=row['bytes'] or run.sha(raw)!=row['sha256']:raise ValueError('original compact evidence changed')
  evidence[row['path']]=raw
 if total>8<<20 or total!=custody['bodyBytes'] or custody['binaries']!=0 or custody['credentials']!=0 or custody['privateLogs']!=0:raise ValueError('bounded public custody required')
 def read(name):return json.loads(evidence[name])
 main=read('summary.json');post=read('source-post.json');monitor=read('backend-fullgraph-controller.resource.json');collection=read('collection.json')
 if main.get('accepted') is not True or main.get('source')!=c['source'] or main.get('actualCurrentMainBuilds')!=14 or main.get('freshGraphScenarios')!=4:raise ValueError('successful original graph and targeted oracles required')
 targeted=read('targeted-names.json');names=[(r['package'],r['name']) for r in targeted['namedCompletions']]
 top_count=sum('/' not in name for _,name in names);sub_count=len(names)-top_count
 if targeted.get('source')!=c['source'] or len(names)!=len(set(names)) or not top_count or targeted.get('skip')!=0 or targeted.get('fail')!=0 or targeted.get('topPASS')!=top_count or targeted.get('subPASS')!=sub_count or main.get('targetedTopPASS')!=top_count or main.get('targetedSubPASS')!=sub_count:raise ValueError('original source-bound named oracle completions required')
 if post.get('accepted') is not True or post.get('source')!=c['source'] or monitor.get('status')!='passed' or monitor.get('exit_code')!=0:raise ValueError('original source/tool post and controller success required')
 if collection.get('started') is not True or collection.get('accepted') is not True or collection.get('controllerPassed') is not True or collection.get('originalRetainedRuntimeJoinRequired') is not True:raise ValueError('independent always cleanup success required')
 rows=collection['scenarios']
 if [r['scenario'] for r in rows]!=list(run.SCENARIOS):raise ValueError('all four original cleanup records required')
 for row in rows:
  cleanup=row.get('resourceCleanup',{})
  if any(row.get(k) is not True for k in ('resourceStarted','runtimeAttempted','runtimeCleanupAccepted')) or cleanup.get('accepted') is not True or cleanup.get('unconfirmedIDs')!=[] or type(row.get('actualAbsentRoles')) is not int or not 0<row['actualAbsentRoles']<=14:raise ValueError('actual original PID absence and resource cleanup required')
 before=read('tools-before.json');after=read('tools-after.json')
 if before!=after:raise ValueError('original hosted tool post differs')
 return {'tools':before,'custodySHA256':run.sha(run.regular(compact/'custody.json',1<<20)),'collectionSHA256':run.sha(evidence['collection.json']),'sourcePostSHA256':run.sha(evidence['source-post.json'])}

def prepare_product_artifact():
 c,bodies,_,_,_,_=run.verify()
 inputs_raw=run.regular(run.INPUTS,1<<20)
 if c.get('productArtifactPolicy')!=PRODUCT_ARTIFACT_POLICY:raise ValueError('Root-bound product artifact policy required')
 allocation=run.allocation_root();root=allocation/'work';directory=root/'bin';private=root/'private'
 for path in (allocation,root,directory,private):
  info=path.lstat()
  if path.resolve()!=path or not stat.S_ISDIR(info.st_mode) or info.st_uid!=os.getuid() or stat.S_IMODE(info.st_mode)!=0o700:raise ValueError('original private allocation required')
 if json.loads(run.regular(allocation/'allocation-owner.json'))!=run.job_identity():raise ValueError('original hosted owner required')
 evidence=checked_product_evidence(run.REPO/'ci-results/backend-fullgraph-compact',c)
 if run.tool_snapshot()!=evidence['tools']:raise ValueError('hosted tool identity changed after cleanup')
 builds=json.loads(run.regular(private/'builds.json',1<<20))
 if builds.get('version')!='source-bound-fullgraph-builds-v1' or builds.get('sourceSHA256')!=c['source']['digest'] or [r['role'] for r in builds['binaries']]!=list(run.ROLES):raise ValueError('fourteen original source-bound build receipts required')
 original_names=set(run.ROLES)|{'acceptance-operator','msgtransfer-legacy'}
 expected=original_names|{name+'-receipt.json' for name in original_names}
 if {p.name for p in directory.iterdir()}!=expected:raise ValueError('original build directory closed inventory required')
 entries={r['role']:r for r in c['entrypointContract']['entrypoints']};products=[]
 for row in builds['binaries']:
  role=row['role'];path=directory/role;receipt_path=directory/(role+'-receipt.json')
  if row['path']!=str(path) or row['receiptPath']!=str(receipt_path) or row['sourceSHA256']!=c['source']['digest']:raise ValueError('original product build path/source differs')
  raw=run.regular(receipt_path,65536)
  if run.sha(raw)!=row['receiptSHA256']:raise ValueError('original build receipt body differs')
  product=product_elf_metadata(path)
  receipt={'schema':1,'sourceSHA256':c['source']['digest'],'binarySHA256':product['sha256'],'binaryBytes':product['bytes'],'toolchainSHA256':evidence['tools']['go']['sha256']}
  if json.loads(raw)!=receipt or row['sha256']!=product['sha256']:raise ValueError('actual product ELF differs from its build receipt')
  products.append(dict(product,package=entries[role]['package'],buildReceiptSHA256=row['receiptSHA256']))
 total=sum(row['bytes'] for row in products)
 if total>PRODUCT_TOTAL_LIMIT:raise ValueError('fourteen product executables exceed2GiB')
 # Source/helper/controller identity is checked after the streaming hashes as
 # well. No cleanup, tool command, compilation or business action is repeated.
 after_c,_,_,_,_,_=run.verify()
 if after_c!=c or run.regular(run.INPUTS,1<<20)!=inputs_raw:raise ValueError('original controller inputs changed while sealing')
 if run.tool_snapshot()!=evidence['tools']:raise ValueError('hosted tool identity changed while sealing')
 if {p.name for p in directory.iterdir()}!=expected:raise ValueError('original binary inventory changed while sealing')
 job=run.job_identity()
 manifest={'schema':1,'kind':'fullgraph-fourteen-product-elf-v1','source':c['source'],'head':job['head'],'runID':job['runID'],'attempt':job['attempt'],'locks':{name:run.sha(bodies['server/'+name]) for name in ('go.mod','go.sum')},'toolSHA256':{name:value['sha256'] for name,value in evidence['tools'].items()},'controllerInputsSHA256':run.sha(inputs_raw),'controllerPins':c['controllerPins'],'compactCustodySHA256':evidence['custodySHA256'],'independentCollectionSHA256':evidence['collectionSHA256'],'sourcePostSHA256':evidence['sourcePostSHA256'],'binaries':products,'totalBinaryBytes':total,'perBinaryLimitBytes':PRODUCT_FILE_LIMIT,'totalBinaryLimitBytes':PRODUCT_TOTAL_LIMIT,'GOOS':'linux','GOARCH':'amd64','CGO_ENABLED':'1','deploymentABIValidated':False,'uploadedOrDeployedByScript':False,'configsLogsKeysAuxiliariesIncluded':False}
 manifest_sha=run.write(directory/'manifest.json',manifest)
 # The original compact custody is immutable and remains complete. This one
 # finite producer line and the binary manifest are separate upload evidence.
 print(json.dumps({'kind':'fullgraph-product-artifact-prepared-v1','manifestSHA256':manifest_sha,'binaryCount':len(products),'totalBinaryBytes':total,'uploadedOrDeployedByScript':False},separators=(',',':')))
 return 0

BUILD_ONLY_POLICY={'enabled':True,'kind':'fullgraph-fourteen-product-build-only-elf-v1','runtimeAccepted':False,'readyPermit':False,'perBinaryLimitBytes':PRODUCT_FILE_LIMIT,'totalBinaryLimitBytes':PRODUCT_TOTAL_LIMIT,'retentionDays':7,'compressionLevel':1,'allowedBasenames':sorted([*run.ROLES,'manifest.json']),'copyBinaries':False,'neverAcceptedProduct20Provider':True}
def checked_build_only_evidence(compact,c,allocation):
 # Separate from accepted producer. A normal runtime failure does not become
 # a successful graph or canonical exclusion, even when its public ELF builds
 # are perfectly reusable. Existing compact custody is never rewritten.
 custody=json.loads(run.regular(compact/'custody.json',1<<20));names=[r['path'] for r in custody['files']]
 if len(names)>256 or len(names)!=len(set(names)) or any(Path(n).name!=n for n in names) or {p.name for p in compact.iterdir()}!=set(names)|{'custody.json'}:raise ValueError('exact original compact custody required')
 evidence={};total=0
 for row in custody['files']:
  raw=run.regular(compact/row['path'],1<<20);total+=len(raw)
  if len(raw)!=row['bytes'] or run.sha(raw)!=row['sha256']:raise ValueError('compact evidence body drift')
  evidence[row['path']]=raw
 if total>8<<20 or total!=custody['bodyBytes'] or any(custody[k]!=0 for k in ('binaries','credentials','privateLogs')):raise ValueError('bounded public metadata required')
 def read(name):return json.loads(evidence[name])
 post=read('source-post.json');before=read('tools-before.json');after=read('tools-after.json');monitor=read('backend-fullgraph-controller.resource.json');collection=read('collection.json')
 if post.get('accepted') is not True or post.get('source')!=c['source'] or before!=after:raise ValueError('source/mode/locks/config/controller/tools post exact required')
 def guard(record,root,failed=False):
  floor=record.get('minimum_observed_free_bytes');size=record.get('maximum_observed_task_allocated_bytes')
  if record.get('guard_revision')!=2 or record.get('environment_values_recorded') is not False or record.get('allocated_root')!=str(root) or record.get('minimum_free_required_bytes')!=1<<30 or type(floor) is not int or floor<1<<30 or record.get('max_allocated_allowed_bytes')!=16<<30 or type(size) is not int or not 0<=size<=16<<30:raise ValueError('original healthy resource/owner boundary required')
  if failed:
   if record.get('status')!='failed' or type(record.get('exit_code')) is not int or not 1<=record['exit_code']<=125:raise ValueError('normal finite nonzero controller/runtime only')
  elif record.get('status')!='passed' or record.get('exit_code')!=0:raise ValueError('actual build phase success required')
 guard(monitor,allocation,True)
 phases=[read(name) for name in names if __import__('re').fullmatch('phase-[0-9]{4}\\.json',name)]
 failed=[row for row in phases if row.get('exitCode')!=0]
 if len(failed)!=1 or failed[0].get('phase') not in {'runtime-'+s for s in run.SCENARIOS}:raise ValueError('one actual runtime failure after complete builds required')
 for row in phases:
  guard(row['resource'],allocation/'work',row in failed)
  if type(row.get('exitCode')) is not int or row['exitCode']!=row['resource']['exit_code']:raise ValueError('original command and resource exits differ')
 for role in run.ROLES:
  compiled=read('compiled-'+role+'.json');actual=[p for p in phases if p.get('phase')=='compile-'+role]
  entry=next(r for r in c['entrypointContract']['entrypoints'] if r['role']==role)
  if len(actual)!=1 or actual[0]['exitCode']!=0 or compiled.get('source')!=c['source'] or compiled.get('role')!=role or compiled.get('package')!=entry['package'] or compiled.get('executed') is not False:raise ValueError('fourteen exact actual compilation receipts required')
  guard(actual[0]['resource'],allocation/'work')
 if collection.get('started') is not True or collection.get('controllerPassed') is not False or collection.get('originalRetainedRuntimeJoinRequired') is not True or collection.get('containerAbsenceIsNotRuntimeDrain') is not True:raise ValueError('original always collection required')
 rows=collection['scenarios']
 if [r['scenario'] for r in rows]!=list(run.SCENARIOS) or not any(r.get('runtimeAttempted') for r in rows):raise ValueError('actual scenario attempt required')
 finite=[]
 for row in rows:
  started=row.get('resourceStarted');attempted=row.get('runtimeAttempted');runtime=row.get('runtimeCleanupAccepted')
  if any(type(x) is not bool for x in (started,attempted,runtime)):raise ValueError('actual cleanup booleans required')
  if started:
   cleanup=row.get('resourceCleanup',{})
   if cleanup.get('accepted') is not True or cleanup.get('unconfirmedIDs')!=[]:raise ValueError('original owned container cleanup unresolved')
  elif attempted:raise ValueError('runtime without original resource owner')
  boundary='not-started'
  if attempted:
   terminal=read('terminal-'+row['scenario']+'.json')
   if 'terminalExceptionType' in row or terminal.get('version')!='owned-fullgraph-token-v2-result-v1' or terminal.get('scenario')!=row['scenario'] or type(terminal.get('accepted')) is not bool or terminal.get('rawErrorText') is not False or terminal.get('sourceSHA256') not in ('',c['source']['digest']):raise ValueError('actual finite runtime terminal required')
   failed_here=failed[0]['phase']=='runtime-'+row['scenario']
   if terminal['accepted']==failed_here:raise ValueError('actual runtime terminal and command outcome differ')
   if runtime:
    if type(row.get('actualAbsentRoles')) is not int or not 0<row['actualAbsentRoles']<=14:raise ValueError('original collector PID absence required')
    boundary='canonical-runtime-cleanup'
   else:
    # RunAcceptance sets source identity after PrepareFresh returns and only
    # then calls Start. A zero role count alone never proves no child started.
    if terminal.get('phase')!='actual-resource-bootstrap' or type(terminal.get('actualStartedRoles')) is not int or terminal['actualStartedRoles']!=0 or terminal.get('sourceSHA256')!='' or terminal.get('bootstrapStage') not in ('plane-inputs','mongo','redis','kafka','kafka-coordinator','config','service-plan','capture-inputs','launcher-probe') or terminal.get('bootstrapFailureTag') not in ('mongo-auth','mongo-unauthorized','mongo-not-primary','mongo-namespace-exists','mongo-other','kafka-auth','kafka-topic-or-cluster-authorization','kafka-unsupported-version','kafka-coordinator-unavailable','kafka-other','permission','no-space','not-found','connection-refused','deadline','cancel','other'):raise ValueError('only exact pre-Start PrepareFresh failure may lack canonical cleanup')
    boundary='pre-start-PrepareFresh-failure'
  finite.append({'scenario':row['scenario'],'resourceStarted':started,'runtimeAttempted':attempted,'resourceCleanupAccepted':bool(started and row.get('resourceCleanup',{}).get('accepted')),'runtimeCleanupAccepted':runtime,'runtimeBoundary':boundary,'canonicalRuntimeAccepted':False})
 return {'tools':before,'custodySHA256':run.sha(run.regular(compact/'custody.json',1<<20)),'collectionSHA256':run.sha(evidence['collection.json']),'sourcePostSHA256':run.sha(evidence['source-post.json']),'controllerStatus':monitor['status'],'controllerExitCode':monitor['exit_code'],'failedRuntimePhase':failed[0]['phase'],'cleanup':finite,'compiled':{role:read('compiled-'+role+'.json') for role in run.ROLES}}

def prepare_build_only_product_artifact():
 c,bodies,_,_,_,_=run.verify();inputs_raw=run.regular(run.INPUTS,1<<20)
 if c.get('buildOnlyArtifactPolicy')!=BUILD_ONLY_POLICY:raise ValueError('explicit build-only Root policy required')
 allocation=run.allocation_root();root=allocation/'work';directory=root/'bin';private=root/'private'
 for path in (allocation,root,directory,private):
  info=path.lstat()
  if path.resolve()!=path or not stat.S_ISDIR(info.st_mode) or info.st_uid!=os.getuid() or stat.S_IMODE(info.st_mode)!=0o700:raise ValueError('original private allocation required')
 if json.loads(run.regular(allocation/'allocation-owner.json'))!=run.job_identity():raise ValueError('original hosted job owner required')
 proof=checked_build_only_evidence(run.REPO/'ci-results/backend-fullgraph-compact',c,allocation)
 if run.tool_snapshot()!=proof['tools']:raise ValueError('tool identity changed after cleanup')
 builds=json.loads(run.regular(private/'builds.json',1<<20))
 if builds.get('version')!='source-bound-fullgraph-builds-v1' or builds.get('sourceSHA256')!=c['source']['digest'] or [r['role'] for r in builds['binaries']]!=list(run.ROLES):raise ValueError('fourteen original source-bound build entries required')
 original_names=set(run.ROLES)|{'acceptance-operator','msgtransfer-legacy'};expected=original_names|{name+'-receipt.json' for name in original_names}
 if {p.name for p in directory.iterdir()}!=expected:raise ValueError('closed original build inventory required')
 entries={r['role']:r for r in c['entrypointContract']['entrypoints']};products=[]
 for row in builds['binaries']:
  role=row['role'];path=directory/role;rp=directory/(role+'-receipt.json')
  if row['path']!=str(path) or row['receiptPath']!=str(rp) or row['sourceSHA256']!=c['source']['digest']:raise ValueError('original build path/source differs')
  raw=run.regular(rp,65536);product=product_elf_metadata(path)
  receipt={'schema':1,'sourceSHA256':c['source']['digest'],'binarySHA256':product['sha256'],'binaryBytes':product['bytes'],'toolchainSHA256':proof['tools']['go']['sha256']}
  compiled=proof['compiled'][role]
  if run.sha(raw)!=row['receiptSHA256'] or json.loads(raw)!=receipt or row['sha256']!=product['sha256'] or compiled['sha256']!=product['sha256'] or compiled['bytes']!=product['bytes']:raise ValueError('actual ELF/public and private build receipts differ')
  products.append(dict(product,package=entries[role]['package'],buildReceiptSHA256=row['receiptSHA256']))
 total=sum(p['bytes'] for p in products)
 if total>PRODUCT_TOTAL_LIMIT:raise ValueError('fourteen product ELF total exceeds2GiB')
 staged_post=run.source_post(root,bodies,c,bodies['server/config/config.example.yaml'])
 if staged_post.get('accepted') is not True:raise ValueError('original staged body/mode/locks/config changed while sealing')
 after_c,after_bodies,_,_,_,_=run.verify()
 if after_c!=c or run.regular(run.INPUTS,1<<20)!=inputs_raw or after_bodies!=bodies or run.tool_snapshot()!=proof['tools'] or {p.name for p in directory.iterdir()}!=expected:raise ValueError('final original source/controller/tools/inventory drift')
 job=run.job_identity()
 manifest={'schema':1,'kind':BUILD_ONLY_POLICY['kind'],'runtimeAccepted':False,'readyPermit':False,'source':c['source'],'head':job['head'],'runID':job['runID'],'attempt':job['attempt'],'controllerStatus':proof['controllerStatus'],'controllerExitCode':proof['controllerExitCode'],'failedRuntimePhase':proof['failedRuntimePhase'],'actualJobConclusion':'not-observed-by-producer','cleanup':proof['cleanup'],'locks':{n:run.sha(bodies['server/'+n]) for n in ('go.mod','go.sum')},'toolSHA256':{n:v['sha256'] for n,v in proof['tools'].items()},'controllerInputsSHA256':run.sha(inputs_raw),'controllerPins':c['controllerPins'],'compactCustodySHA256':proof['custodySHA256'],'independentCollectionSHA256':proof['collectionSHA256'],'sourcePostSHA256':proof['sourcePostSHA256'],'binaries':products,'totalBinaryBytes':total,'perBinaryLimitBytes':PRODUCT_FILE_LIMIT,'totalBinaryLimitBytes':PRODUCT_TOTAL_LIMIT,'GOOS':'linux','GOARCH':'amd64','CGO_ENABLED':'1','deploymentABIValidated':False,'uploadedOrDeployedByScript':False,'configsLogsKeysAuxiliariesIncluded':False}
 digest=run.write(directory/'manifest.json',manifest)
 print(json.dumps({'kind':'fullgraph-build-only-unaccepted-artifact-prepared-v1','runtimeAccepted':False,'readyPermit':False,'manifestSHA256':digest,'binaryCount':14,'totalBinaryBytes':total,'uploadedOrDeployedByScript':False},separators=(',',':')))
 return 0

if __name__=='__main__':
 if sys.argv[1:]==['--prepare-build-only-product-artifact']:raise SystemExit(prepare_build_only_product_artifact())
 if sys.argv[1:]==['--prepare-product-artifact']:raise SystemExit(prepare_product_artifact())
 if sys.argv[1:]:raise SystemExit('unsupported collector arguments')
 raise SystemExit(collect())
