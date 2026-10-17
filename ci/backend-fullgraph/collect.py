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
 phase=value.get('phase','');kind=value.get('failureClass','');failure=value.get('failureSHA256','')
 import re
 if not re.fullmatch('[a-z0-9-]{1,128}',phase) or kind not in ('','source-error-private','owned-evidence-rejected','commit-unconfirmed','deadline','cancelled') or failure and not re.fullmatch('[0-9a-f]{64}',failure):raise ValueError('unsafe terminal enum/hash')
 if any(v and not re.fullmatch('[0-9a-f]{64}',v) for v in (value.get('sourceSHA256',''),value.get('resourcePlaneSHA256',''))) or type(value.get('actualStartedRoles',0)) is not int or not 0<=value.get('actualStartedRoles',0)<=14:raise ValueError('finite source/role fields required')
 return {'version':value['version'],'scenario':scenario,'accepted':value['accepted'],'phase':phase,'failureClass':kind,'failureSHA256':failure,'sourceSHA256':value.get('sourceSHA256',''),'resourcePlaneSHA256':value.get('resourcePlaneSHA256',''),'actualStartedRoles':value.get('actualStartedRoles',0),'originalResultSHA256':run.sha(raw),'rawErrorText':False}

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

if __name__=='__main__':raise SystemExit(collect())
