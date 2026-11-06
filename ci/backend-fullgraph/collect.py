#!/usr/bin/env python3
"""Always inspect exact retained task custody; never adopt PID or fake a drain."""
import hashlib,importlib.util,json,os,stat,sys
from pathlib import Path
HERE=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('fullgraph_controller',HERE/'run.py')
run=importlib.util.module_from_spec(spec);spec.loader.exec_module(run)

def public_resource_preparation(raw,scenario):
 value=json.loads(raw)
 keys={'version','scenario','role','step','callState','errorClass','cliExitCode','rawErrorText'}
 if set(value)!=keys or value['version']!='owned-fullgraph-resource-prepare-observation-v1' or value['scenario']!=scenario or scenario not in run.SCENARIOS or value['rawErrorText'] is not False:raise ValueError('finite original resource preparation required')
 if value['role'] not in ('none','keeper','mongo','redis','zookeeper','kafka-format','kafka','minio') or value['step'] not in ('config','create-intent','image','volume','data','env','create','inspect','start','format-attach','format-result','final-observe','claim') or value['callState']!='returned' or value['errorClass'] not in ('none','command-nonzero','command-timeout','command-output-cap','permission','no-space','not-found','contract','other'):raise ValueError('closed resource prepare observation required')
 code=value['cliExitCode']
 if code is not None and (type(code) is not int or not -128<=code<=255):raise ValueError('actual bounded CLI exit required')
 if value['step'] in ('format-attach','format-result') and value['role']!='kafka-format' or value['step']=='claim' and value['role']!='none':raise ValueError('original resource prepare role/step differs')
 if value['errorClass']=='command-nonzero' and (code is None or code==0) or value['errorClass']=='none' and code not in (None,0):raise ValueError('original command exit/error relation differs')
 return value

def public_required_push_rpc(value,scenario):
 if scenario not in ('ack','redis-unknown') or value.get('phase')!='actual-required-push-rpc':raise ValueError('only original required-push phase may expose these facts')
 names=('actualUserReadExact','rawPushRejected','redisKeyPresentBefore','actualTokenRPCSucceeded','redisKeyAbsentAfter')
 if any(type(value.get(key)) is not bool for key in names) or type(value.get('actualTokenRPCDispatched')) is not int or value['actualTokenRPCDispatched'] not in (0,1):raise ValueError('closed original push facts required')
 flags={key:value[key] for key in names};dispatch=value['actualTokenRPCDispatched']
 if flags['rawPushRejected'] and not flags['actualUserReadExact'] or flags['redisKeyPresentBefore'] and not flags['rawPushRejected'] or dispatch and not flags['redisKeyPresentBefore'] or flags['actualTokenRPCSucceeded'] and dispatch!=1 or flags['redisKeyAbsentAfter'] and dispatch!=1:raise ValueError('original push sequence facts are inconsistent')
 loss=value.get('ackLoss');keys={'version','targetKeySHA256','exactDeleteRequests','actualUpstreamIntegerOne','actualReplyDiscarded','transportFailure'}
 if not isinstance(loss,dict) or set(loss)!=keys or type(loss.get('exactDeleteRequests')) is not int or not 0<=loss['exactDeleteRequests']<1<<63 or any(type(loss.get(key)) is not bool for key in ('actualUpstreamIntegerOne','actualReplyDiscarded','transportFailure')):raise ValueError('closed original ACK transport facts required')
 observed=loss['version']=='real-redis-exact-del-ack-loss-v1'
 if observed:
  if not __import__('re').fullmatch('[0-9a-f]{64}',loss['targetKeySHA256']) or not flags['redisKeyAbsentAfter']:raise ValueError('original proxy observation ordering differs')
 elif loss!={'version':'','targetKeySHA256':'','exactDeleteRequests':0,'actualUpstreamIntegerOne':False,'actualReplyDiscarded':False,'transportFailure':False}:raise ValueError('only exact unobserved ACK defaults permitted')
 boundary='proxy-observation-not-accepted' if observed else 'phase-completion-not-confirmed'
 for condition,name in ((not flags['actualUserReadExact'],'user-read-not-accepted'),(not flags['rawPushRejected'],'raw-push-rejection-not-accepted'),(not flags['redisKeyPresentBefore'],'token-seed-not-confirmed'),(dispatch==0,'token-dispatch-not-observed'),(flags['actualTokenRPCSucceeded']!=(scenario=='ack'),'token-result-not-accepted'),(not flags['redisKeyAbsentAfter'],'post-token-key-absence-not-confirmed')):
  if condition:boundary=name;break
 return dict(flags,version='original-required-push-rpc-facts-v1',actualTokenRPCDispatched=dispatch,sequenceBoundary=boundary,ackLoss={'observed':observed,'exactDeleteRequestsClass':('zero' if loss['exactDeleteRequests']==0 else 'one' if loss['exactDeleteRequests']==1 else 'more-than-one'),'actualUpstreamIntegerOne':loss['actualUpstreamIntegerOne'],'actualReplyDiscarded':loss['actualReplyDiscarded'],'transportFailure':loss['transportFailure']},uniqueFailurePredicate=False,rawErrorText=False,targetKeyOrHashExported=False,acceptanceAuthority=False)

def checked_raw_push_evidence(out):
 c,bodies,recipe,_,_,_=run.verify();control=run.CURRENT_RAW_PUSH_CONTROL;before=json.loads(run.regular(out/'tools-before.json'));after=json.loads(run.regular(out/'tools-after.json'))
 if before!=after or json.loads(run.regular(out/'source-post.json'))!={'accepted':True,'source':run.FINAL_SOURCE}:raise ValueError('current exact source and tools required for request contract')
 reuse=json.loads(run.regular(out/'raw-push-null-scope-compatibility.json',1<<20));run.check_raw_push_null_compatibility_receipt(reuse,bodies,recipe,before.get('go',{}))
 if json.loads(run.regular(out/'raw-push-cache-scope-deps.json',1<<20))!=reuse['dependencyEvidence']:raise ValueError('actual complete cache dependency receipt required')
 value=run.check_raw_push_control_receipt(json.loads(run.regular(out/'current-required-push-request-names.json',65536)));events=json.loads(run.regular(out/(control['phase']+'-events.json'),1<<20));package=next(iter(control['targets']));prefix='github.com/openimsdk/open-im-server/v3/'+package;expected={(r['package'],r['name']) for r in value['namedCompletions']}
 if not isinstance(events,list) or len(events)>4096 or any(not isinstance(row,dict) or set(row)-{'Time','Action','Package','Test','Elapsed'} or row.get('Package')!=prefix or row.get('Action') in ('fail','skip') or row.get('Test') and (package,row['Test']) not in expected for row in events):raise ValueError('actual closed request-contract events required')
 for action in ('run','pass'):
  names=[(package,row['Test']) for row in events if row.get('Action')==action and row.get('Test')]
  if len(names)!=len(expected) or set(names)!=expected:raise ValueError('actual request top and both children must run and pass exactly once')
 if sum(row.get('Action')=='start' and not row.get('Test') for row in events)!=1 or sum(row.get('Action')=='pass' and not row.get('Test') for row in events)!=1:raise ValueError('actual request package start/footer required')
 for label in (control['phase'],run.RAW_PUSH_DEPENDENCY_CONTROL['phase']):
  phases=[json.loads(run.regular(p)) for p in out.glob('phase-*.json')];selected=[v for v in phases if v.get('phase')==label]
  if len(selected)!=1 or selected[0].get('exitCode')!=0 or selected[0].get('resource',{}).get('status')!='passed' or selected[0]['resource'].get('exit_code')!=0:raise ValueError('successful actual request/dependency commands required')
 forbidden=('same-go-null-named-scope-reuse.json','current-owned-redis-null-ack-names.json','current-owned-redis-null-ack-events.json')
 if any((out/name).exists() for name in forbidden):raise ValueError('source-compatible Null history must not alias same-complete-source or fresh execution')
 return value

def checked_redis_null_passed_evidence(out):
 facts=run.check_redis_null_passed_facts(run.PASSED_REDIS_NULL_ACK_FACTS);value=json.loads(run.regular(out/'same-go-null-named-scope-reuse.json',65536))
 expected={'version':'same-complete-Go-null-named-scope-reuse-v1','source':run.FINAL_SOURCE,'originalRun':facts['originalRun'],'factsCanonicalSHA256':run.sha(json.dumps(facts,sort_keys=True,separators=(',',':')).encode()),'sameCompleteGraphGoSource':True,'originalTopPASS':1,'originalSubPASS':3,'actualCurrentNullGoCommands':0,'wholePackagePASS':False,'currentWholeProjectGoPASS':False,'runtimePIDReadyP20Accepted':False}
 if value!=expected or any((out/name).exists() for name in ('current-owned-redis-null-ack-events.json','current-owned-redis-null-ack-names.json')):raise ValueError('exact historical Null scope reuse with zero current dispatch required')
 if json.loads(run.regular(out/'source-post.json'))!={'accepted':True,'source':run.FINAL_SOURCE}:raise ValueError('current complete Go source post-check required')
 before=json.loads(run.regular(out/'tools-before.json'));after=json.loads(run.regular(out/'tools-after.json'))
 if before!=after:raise ValueError('current tools changed')
 run.check_redis_null_passed_facts(facts,before.get('go',{}));return value

def public_terminal(raw,scenario):
 value=json.loads(raw)
 if value.get('version')!='owned-fullgraph-token-v2-result-v1' or value.get('scenario')!=scenario or type(value.get('accepted')) is not bool:raise ValueError('finite actual terminal result required')
 bootstrap=value.get('bootstrapStage','')
 if bootstrap not in ('','plane-inputs','mongo','redis','kafka','kafka-coordinator','config','service-plan','capture-inputs','launcher-probe'):raise ValueError('closed bootstrap stage required')
 startup=value.get('legacyStartupStage','');retained=value.get('legacyChildRetained',False)
 observed=value.get('localCleanupObserved',False);cleanup=value.get('localCleanupState','');cleanup_kind=value.get('localCleanupFailureClass','')
 if startup not in ('', 'launcher-enter', 'launcher-admission', 'launcher-state', 'launcher-role', 'spec', 'receipt', 'binary', 'bootstrap', 'work', 'environment', 'owner-files', 'exec', 'process-identity', 'process-image', 'launcher-persist', 'await-exit', 'await-identity', 'await-announcement', 'await-context') or type(retained) is not bool or not startup and retained or startup.startswith('await-') and not retained:raise ValueError('closed original legacy startup facts required')
 if type(observed) is not bool or cleanup not in ('','no-session','complete','incomplete') or cleanup_kind not in ('','none','deadline','cancelled','owned-evidence','other') or observed!=bool(cleanup) or bool(cleanup)!=bool(cleanup_kind) or cleanup=='no-session' and (cleanup_kind!='none' or startup):raise ValueError('closed original local cleanup facts required')
 acquire_stage=value.get('acquireStage','');acquire_attempted=value.get('acquireAttempted',False);acquire_returned=value.get('acquireReturned',False);acquire_error=value.get('acquireErrorClass','')
 if acquire_stage not in ('','not-entered','validate-request','check-targets','hold','services','stop-producer','drain-consumers-live','stop-consumer','exclude-credentials','exclude-credentials-mongo','exclude-credentials-redis','exclude-credentials-kafka','exclude-credentials-kafka-input','exclude-credentials-kafka-admin-client','exclude-credentials-kafka-admin-handle','exclude-credentials-kafka-metadata-controller','exclude-credentials-kafka-metadata','exclude-credentials-kafka-acl-resource','exclude-credentials-kafka-legacy-client','exclude-credentials-kafka-broker-input','exclude-credentials-kafka-broker-handle','exclude-credentials-kafka-broker-open','exclude-credentials-kafka-live-denial','exclude-credentials-kafka-evidence','exclude-credentials-kafka-complete','exclude-credentials-kafka-legacy-close','exclude-credentials-kafka-admin-close','exclude-credentials-kafka-topic-guard','exclude-credentials-kafka-topic-write-controller','exclude-credentials-kafka-topic-write-call','exclude-credentials-kafka-topic-write-response','exclude-credentials-kafka-topic-read-controller','exclude-credentials-kafka-topic-read-call','exclude-credentials-kafka-topic-read-response','exclude-credentials-kafka-topic-exact-deny','exclude-credentials-kafka-group-guard','exclude-credentials-kafka-group-write-controller','exclude-credentials-kafka-group-write-call','exclude-credentials-kafka-group-write-response','exclude-credentials-kafka-group-read-controller','exclude-credentials-kafka-group-read-call','exclude-credentials-kafka-group-read-response','exclude-credentials-kafka-group-exact-deny','exclude-credentials-kafka-transaction-guard','exclude-credentials-kafka-transaction-write-controller','exclude-credentials-kafka-transaction-write-call','exclude-credentials-kafka-transaction-write-response','exclude-credentials-kafka-transaction-read-controller','exclude-credentials-kafka-transaction-read-call','exclude-credentials-kafka-transaction-read-response','exclude-credentials-kafka-transaction-exact-deny','exclude-credentials-kafka-cluster-guard','exclude-credentials-kafka-cluster-write-controller','exclude-credentials-kafka-cluster-write-call','exclude-credentials-kafka-cluster-write-response','exclude-credentials-kafka-cluster-read-controller','exclude-credentials-kafka-cluster-read-call','exclude-credentials-kafka-cluster-read-response','exclude-credentials-kafka-cluster-exact-deny','drain-consumers-empty','held-metadata','signed-observe') or type(acquire_attempted) is not bool or type(acquire_returned) is not bool or acquire_error not in ('','not-entered','in-progress','ok','owned-evidence','deadline','cancelled','other'):raise ValueError('closed original Acquire observation required')
 if not acquire_attempted:
  if acquire_returned or acquire_stage not in ('','not-entered') or acquire_error not in ('','not-entered'):raise ValueError('unattempted Acquire cannot report a stage or return')
 else:
  if acquire_stage in ('','not-entered') or acquire_returned and acquire_error not in ('ok','owned-evidence','deadline','cancelled','other') or not acquire_returned and acquire_error!='in-progress' or acquire_error=='ok' and acquire_stage!='signed-observe':raise ValueError('Acquire attempt return and error facts disagree')
 primary=value.get('primaryRunErrorClass','');reader_stage=value.get('readerStage','');reader_state=value.get('readerCallState','');reader_error=value.get('readerErrorClass','')
 if primary not in ('', 'none', 'cancelled', 'deadline', 'permission', 'not-found', 'already-exists', 'registration-pending', 'commit-unconfirmed', 'owned-evidence', 'import-evidence', 'import-conflict', 'import-invalid', 'preparation-unavailable', 'preparation-conflict', 'inventory-private', 'inventory-identity', 'inventory-changed', 'inventory-source', 'other'):raise ValueError('closed primary pre-cleanup error class required')
 if reader_stage not in ('', 'not-entered', 'start-rpc-conversation', 'start-rpc-msg', 'source-custody', 'observe-registration') or reader_state not in ('','not-entered','in-progress','returned') or reader_error not in ('', 'not-entered', 'in-progress', 'none', 'cancelled', 'deadline', 'permission', 'not-found', 'already-exists', 'registration-pending', 'commit-unconfirmed', 'owned-evidence', 'import-evidence', 'import-conflict', 'import-invalid', 'preparation-unavailable', 'preparation-conflict', 'inventory-private', 'inventory-identity', 'inventory-changed', 'inventory-source', 'other'):raise ValueError('closed original reader call observation required')
 if reader_state in ('','not-entered'):
  if reader_stage not in ('','not-entered') or reader_error not in ('','not-entered'):raise ValueError('unattempted reader call cannot report an operation')
 else:
  if reader_stage in ('','not-entered') or reader_state=='in-progress' and reader_error!='in-progress' or reader_state=='returned' and reader_error not in ('none', 'cancelled', 'deadline', 'permission', 'not-found', 'already-exists', 'registration-pending', 'commit-unconfirmed', 'owned-evidence', 'import-evidence', 'import-conflict', 'import-invalid', 'preparation-unavailable', 'preparation-conflict', 'inventory-private', 'inventory-identity', 'inventory-changed', 'inventory-source', 'other'):raise ValueError('reader attempted and returned facts disagree')
  if reader_error=='registration-pending' and reader_stage!='observe-registration':raise ValueError('only actual registration observation can be pending')
 constructor_stage=value.get('constructorStage','not-entered');constructor_role=value.get('constructorRole','none');constructor_state=value.get('constructorCallState','not-entered');constructor_error=value.get('constructorErrorClass','none');constructor_predicate=value.get('constructorPredicate','none')
 constructor_roles={'api','crontask','msggateway','msgtransfer-managed','push','rpc-auth','rpc-conversation','rpc-encryption','rpc-friend','rpc-group','rpc-msg','rpc-rtc','rpc-third','rpc-user'}
 constructor_errors={'none','cancelled','deadline','permission','not-found','already-exists','registration-pending','commit-unconfirmed','owned-evidence','import-evidence','import-conflict','import-invalid','preparation-unavailable','preparation-conflict','inventory-private','inventory-identity','inventory-changed','inventory-source','other'}
 constructor_start_predicates={'start-input','start-hold','start-bootstrap','start-requirement','start-server-environment','start-server-bootstrap','start-managed-environment','start-managed-shape','start-managed-fields','start-reader-input','start-nonreader-input','start-intent','start-reader','start-owner-record','start-service-spec','start-service-receipt','start-service-binary','start-service-bootstrap','start-service-work','start-service-environment','start-service-owner-files','start-service-exec','start-service-process-identity','start-service-process-image'}
 constructor_wait_outcomes=('wait-unobserved','wait-success','wait-other')+tuple('wait-exit-code-'+str(code) for code in range(-1,256))
 constructor_image_predicates={'start-service-process-image-'+step+'-'+state for step in ('context','before-identity','open','initial-stat','read','final-stat','size','hash','after-identity') for state in constructor_wait_outcomes}
 constructor_start_predicates|=constructor_image_predicates
 constructor_predicates={'process-set','graph-preconditions','bootstrap','requirement','input-state','handle','joined','owner','done','process-before','image','registration-absent','envelope','instance','tuple','mongo-binding','services','components','process-after'}
 constructor_late_stages={
 'release-processes':{'none','process-set','processes-complete'},
 'private-releases':{'none','release-input','release-local-graph','release-read-admission','release-graph-json','release-process','release-signing','release-write','release-complete'}|{'release-refresh-'+step for step in constructor_predicates|{'refresh-input','refresh-preconditions','refresh-row-identity','refresh-complete'}},
 'await-started':{'none','started-caller-check','started-input','started-validator','started-publication','started-state','started-announcement','started-owner-identity','started-announcement-evidence','started-row-identity','started-seal','started-record','started-complete'}|{'started-observe-'+step for step in constructor_predicates}|{'started-reobserve-'+step for step in constructor_predicates}|{'started-announcement-'+step for step in ('await-exit','await-context','await-identity','await-announcement')},
 'serving-readers':{'none','reader-original-inode','reader-endpoint','complete'}}
 if any(type(v) is not str for v in (constructor_stage,constructor_role,constructor_state,constructor_error,constructor_predicate)) or constructor_stage not in ('not-entered','start','source-custody','observe-registration',*constructor_late_stages) or constructor_role not in constructor_roles|{'none'} or constructor_state not in ('not-entered','in-progress','returned') or constructor_error not in constructor_errors|{'in-progress'} or constructor_predicate not in constructor_predicates|constructor_start_predicates|set().union(*constructor_late_stages.values())|{'none','complete','start-complete'}:raise ValueError('closed original constructor call observation required')
 if constructor_stage=='not-entered':
  if (constructor_role,constructor_state,constructor_error,constructor_predicate)!=('none','not-entered','none','none'):raise ValueError('unattempted constructor call cannot report an operation')
 else:
  if constructor_state=='not-entered' or constructor_state=='in-progress' and constructor_error!='in-progress' or constructor_state=='returned' and constructor_error not in constructor_errors:raise ValueError('constructor attempted and returned facts disagree')
  if constructor_stage=='start':
   if constructor_predicate in constructor_image_predicates and (constructor_state!='returned' or constructor_error in ('none','registration-pending')):raise ValueError('image rejection observation requires an actual returned failure')
   if constructor_role not in constructor_roles-{'rpc-conversation','rpc-msg'} or constructor_predicate not in constructor_start_predicates|{'none','start-complete'} or constructor_error=='registration-pending' or constructor_state=='in-progress' and constructor_predicate!='none' or constructor_predicate=='start-complete' and constructor_error!='none':raise ValueError('only original twelve constructor roles can report Start')
  elif constructor_stage in constructor_late_stages:
   allowed=constructor_late_stages[constructor_stage]
   if constructor_predicate not in allowed or constructor_role not in constructor_roles|{'none'}:raise ValueError('closed original release/start/reader call observation required')
   if constructor_stage=='serving-readers' and constructor_role not in {'none','rpc-msg','rpc-conversation'}:raise ValueError('only original two reader endpoints may report serving checks')
   in_progress={'release-processes':{'none'},'private-releases':{'none'},'await-started':{'none','started-caller-check'},'serving-readers':{'reader-original-inode','reader-endpoint'}}[constructor_stage]
   if constructor_state=='in-progress' and constructor_predicate not in in_progress:raise ValueError('unreturned lifecycle call may not invent a returned predicate')
   if constructor_state=='returned' and constructor_error=='none':
    complete={'release-processes':'processes-complete','private-releases':'release-complete','await-started':'started-complete','serving-readers':'complete'}[constructor_stage]
    if (constructor_role,constructor_predicate)!=('none',complete) and not (constructor_stage=='serving-readers' and constructor_role in {'rpc-msg','rpc-conversation'} and constructor_predicate=='reader-endpoint'):raise ValueError('successful original lifecycle calls require their exact complete labels')
  elif constructor_stage=='source-custody':
   if constructor_role!='none' or constructor_predicate!='none' or constructor_error=='registration-pending':raise ValueError('source custody cannot report a child predicate')
  elif constructor_state=='in-progress':
   if constructor_role!='none' or constructor_predicate!='none':raise ValueError('unreturned Observe cannot report a returned child predicate')
  elif constructor_error=='none':
   if constructor_role!='none' or constructor_predicate!='complete':raise ValueError('successful Observe requires its complete call observation')
  elif constructor_error=='registration-pending':
   if constructor_role not in constructor_roles or constructor_predicate!='registration-absent':raise ValueError('only original absent registration can report pending')
  elif constructor_predicate not in constructor_predicates or constructor_predicate=='registration-absent' or constructor_predicate=='graph-preconditions' and constructor_role!='none' or constructor_predicate not in ('process-set','graph-preconditions') and constructor_role not in constructor_roles:raise ValueError('Observe rejection must retain its actual finite role and predicate')
 phase=value.get('phase','');kind=value.get('failureClass','');failure=value.get('failureSHA256','')
 tag=value.get('bootstrapFailureTag','')
 if tag not in ('','mongo-auth','mongo-unauthorized','mongo-not-primary','mongo-namespace-exists','mongo-other','kafka-auth','kafka-topic-or-cluster-authorization','kafka-unsupported-version','kafka-coordinator-unavailable','kafka-other','permission','no-space','not-found','connection-refused','deadline','cancel','other') or tag and phase!='actual-resource-bootstrap':raise ValueError('closed original bootstrap failure tag required')
 import re
 if not re.fullmatch('[a-z0-9-]{1,128}',phase) or kind not in ('','source-error-private','owned-evidence-rejected','commit-unconfirmed','deadline','cancelled') or failure and not re.fullmatch('[0-9a-f]{64}',failure):raise ValueError('unsafe terminal enum/hash')
 if any(v and not re.fullmatch('[0-9a-f]{64}',v) for v in (value.get('sourceSHA256',''),value.get('resourcePlaneSHA256',''))) or type(value.get('actualStartedRoles',0)) is not int or not 0<=value.get('actualStartedRoles',0)<=14:raise ValueError('finite source/role fields required')
 projected={'version':value['version'],'scenario':scenario,'accepted':value['accepted'],'phase':phase,'bootstrapStage':bootstrap,'bootstrapFailureTag':tag,'failureClass':kind,'failureSHA256':failure,'sourceSHA256':value.get('sourceSHA256',''),'resourcePlaneSHA256':value.get('resourcePlaneSHA256',''),'actualStartedRoles':value.get('actualStartedRoles',0),'legacyStartupStage':startup,'legacyChildRetained':retained,'acquireStage':acquire_stage,'acquireAttempted':acquire_attempted,'acquireReturned':acquire_returned,'acquireErrorClass':acquire_error,'primaryRunErrorClass':primary,'readerStage':reader_stage,'readerCallState':reader_state,'readerErrorClass':reader_error,'constructorStage':constructor_stage,'constructorRole':constructor_role,'constructorCallState':constructor_state,'constructorErrorClass':constructor_error,'constructorPredicate':constructor_predicate,'localCleanupObserved':observed,'localCleanupState':cleanup,'localCleanupFailureClass':cleanup_kind,'originalResultSHA256':run.sha(raw),'rawErrorText':False}
 if phase=='actual-required-push-rpc':projected['requiredPushRPCObservation']=public_required_push_rpc(value,scenario)
 return projected

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

def checked_launcher_evidence(out):
 return run.check_launcher_reuse(json.loads(run.regular(out/'launcher-passed-reuse.json',65536)))

def checked_kafka_acl_evidence(out):
 receipt=run.check_kafka_acl_receipt(json.loads(run.regular(out/'kafka-acl-passed-reuse.json',65536)))
 post=json.loads(run.regular(out/'source-post.json',4096))
 before=json.loads(run.regular(out/'tools-before.json',65536));after=json.loads(run.regular(out/'tools-after.json',65536))
 if post!=receipt['sourcePost']['body'] or before!=after:raise ValueError('current original source/tool post differs from Kafka ACL reuse')
 run.check_kafka_acl_go_tool(receipt,before.get('go'))
 return receipt

def console_observation(rows,monitor,main,terminals,phases):
 # Only already-validated finite observations reach this diagnostic projection.
 # The result cannot replace the original result/cleanup/source/tool evidence.
 if [r.get('scenario') for r in rows]!=list(run.SCENARIOS):raise ValueError('four original scenario observations required')
 statuses={'passed','failed','stopped_timeout','stopped_remaining_process_group','infrastructure_failed'}
 statuses|={prefix+gate for prefix in ('not_started_','stopped_','failed_final_') for gate in ('disk_gate','task_allocation_gate')}
 def status(value):return value if value in statuses else 'unobserved'
 def code(value):return value if type(value) is int and -128<=value<=255 else None
 names={run.CURRENT_REDIS_NULL_ACK_CONTROL['phase'],run.CURRENT_OWNED_RESOLVER_SCHEME_CONTROL['phase'],run.CURRENT_CACHED_WAIT_CONTROL['phase'],'rtc-panic-runtime-build','rtc-panic-runtime-execute','go-root','go-version','app-module-download','minio-git-init','minio-git-fetch','minio-git-checkout','minio-git-head','minio-git-tree','minio-git-files','minio-module-download','minio-source-compile','minio-post-head','minio-post-tree','pending-two-original-oracles','launcher-ownedcontrol','redis-parser','kafka-acl-response',run.CURRENT_IMAGE_KAFKA_CONTROL['phase'],run.CURRENT_KAFKA_CONFIRM_CONTROL['phase'],run.CURRENT_RELEASE_CONTROL['phase'],run.CURRENT_MANAGED_SHAPE_CONTROL['phase'],run.CURRENT_PRODUCER_CONTROL['phase'],run.CURRENT_GROUP_RECOVERY_CONTROL['phase'],run.CURRENT_CACHE_TEST_DELTA_CONTROL['phase'],run.CURRENT_CRON_FIXTURE_CONTROL['phase'],run.CRON_FIXTURE_DEPENDENCY_CONTROL['phase'],run.CURRENT_GROUP_REGISTRY_FIXTURE_CONTROL['phase'],run.GROUP_REGISTRY_FIXTURE_DEPENDENCY_CONTROL['phase'],run.CURRENT_RUNTIME_RELEASE_CONTROL['phase'],run.CURRENT_RTC_ACTIVATION_CONTROL['phase']}|{r['label'] for r in run.CLEANUP_SCOPES}
 def phase(value):
  label=value.get('phase');result={'phase':'unobserved','role':'none','scenario':'none','exitCode':code(value.get('exitCode')),'resourceStatus':status(value.get('resource',{}).get('status'))}
  if label in names:result['phase']=label
  elif label in {'compile-'+r for r in (*run.ROLES,'acceptance-operator','msgtransfer-legacy')}:result.update(phase='compile',role=label[8:])
  elif label in {'runtime-'+s for s in run.SCENARIOS}:result.update(phase='runtime',scenario=label[8:])
  elif isinstance(label,str) and label.startswith('image-acquire-'):result['phase']='image-acquire'
  return result
 result={'event':'backend_graph_observation','summaryAvailable':True,'acceptanceEvidence':False,'rawErrorText':False,
  'controllerStatus':status(monitor.get('status')),'controllerExitCode':code(monitor.get('exit_code')),
  'mainAccepted':main.get('accepted') is True,'lastPhase':phase(phases[-1]) if phases else None,'scenarios':[]}
 runtime_keys=('accepted','bootstrapStage','bootstrapFailureTag','legacyStartupStage','legacyChildRetained','acquireStage','acquireAttempted','acquireReturned','acquireErrorClass','primaryRunErrorClass','readerStage','readerCallState','readerErrorClass','constructorStage','constructorRole','constructorCallState','constructorErrorClass','constructorPredicate','localCleanupObserved','localCleanupState','localCleanupFailureClass')
 for row in rows:
  scenario=row['scenario'];item={'scenario':scenario,'resourceStarted':row.get('resourceStarted') is True,
   'runtimeAttempted':row.get('runtimeAttempted') is True,'resourceCleanupAccepted':row.get('resourceCleanup',{}).get('accepted') is True,
   'runtimeCleanupAccepted':row.get('runtimeCleanupAccepted') is True,'resourcePreparation':None,'runtime':None}
  if row.get('resourcePreparation') is not None:
   observed=public_resource_preparation(json.dumps(row['resourcePreparation']).encode(),scenario)
   item['resourcePreparation']={k:observed[k] for k in ('role','step','callState','errorClass','cliExitCode')}
  if scenario in terminals:
   observed=public_terminal(json.dumps(terminals[scenario]).encode(),scenario)
   item['runtime']={k:observed[k] for k in runtime_keys}
  result['scenarios'].append(item)
 if len(json.dumps(result,separators=(',',':')).encode())>8192:raise ValueError('bounded console observation required')
 return result

def emit_console_observation(rows,monitor,main,terminals,out):
 # A console failure must not replace the independent collector's outcome.
 try:
  paths=sorted(out.glob('phase-*.json'))
  if len(paths)>256:raise ValueError('bounded original phase inventory required')
  phases=[json.loads(run.regular(p,1<<20)) for p in paths]
  value=console_observation(rows,monitor,main,terminals,phases)
  print(json.dumps(value,separators=(',',':')),flush=True)
 except Exception:
  try:print('{"event":"backend_graph_observation","summaryAvailable":false,"acceptanceEvidence":false,"rawErrorText":false}',flush=True)
  except Exception:pass

def checked_current_image_kafka_evidence(out):
 value=run.check_current_image_kafka_receipt(json.loads(run.regular(out/'current-image-kafka-names.json',65536)))
 control=run.CURRENT_IMAGE_KAFKA_CONTROL;package=next(iter(control['targets']));prefix='github.com/openimsdk/open-im-server/v3/'
 events=json.loads(run.regular(out/'current-image-kafka-events.json',1<<20))
 expected={(row['package'],row['name']) for row in value['namedCompletions']}
 if not isinstance(events,list) or not events or any(set(row)-{'Time','Action','Package','Test','Elapsed'} or row.get('Package')!=prefix+package or row.get('Action') in ('fail','skip') for row in events):raise ValueError('exact finite current image/Kafka events required')
 for action in ('run','pass'):
  actual=[(package,row['Test']) for row in events if row.get('Test') and row.get('Action')==action]
  if len(actual)!=6 or set(actual)!=expected:raise ValueError('every current image/Kafka run/pass required')
 if len([row for row in events if row.get('Action')=='pass' and not row.get('Test')])!=1:raise ValueError('original current package completion required')
 phases=[]
 for path in sorted(out.glob('phase-*.json')):
  phase=json.loads(run.regular(path,1<<20))
  if phase.get('phase')==control['phase']:phases.append(phase)
 if len(phases)!=1:raise ValueError('one original current image/Kafka phase required')
 phase=phases[0];resource=phase.get('resource',{})
 if type(phase.get('exitCode')) is not int or phase['exitCode']!=0 or resource.get('status')!='passed' or type(resource.get('exit_code')) is not int or resource['exit_code']!=0:raise ValueError('actual successful original image/Kafka process required')
 post=json.loads(run.regular(out/'source-post.json',4096));before=json.loads(run.regular(out/'tools-before.json',65536));after=json.loads(run.regular(out/'tools-after.json',65536))
 if post!={'accepted':True,'source':run.FINAL_SOURCE} or before!=after:raise ValueError('current original source/tool post differs')
 historical=run.check_kafka_acl_receipt(json.loads(run.regular(out/'kafka-acl-historical-pass.json',65536)))
 if historical['source']==run.FINAL_SOURCE or (out/'kafka-acl-passed-reuse.json').exists():raise ValueError('fresh current scope cannot alias historical Kafka reuse')
 return value

def checked_current_kafka_confirm_evidence(out):
 value=run.check_current_kafka_confirm_receipt(json.loads(run.regular(out/'current-kafka-confirm-names.json',65536)))
 control=run.CURRENT_KAFKA_CONFIRM_CONTROL;package=next(iter(control['targets']));prefix='github.com/openimsdk/open-im-server/v3/'
 events=json.loads(run.regular(out/'current-kafka-confirm-events.json',1<<20))
 expected={(row['package'],row['name']) for row in value['namedCompletions']}
 if not isinstance(events,list) or not events or any(set(row)-{'Time','Action','Package','Test','Elapsed'} or row.get('Package')!=prefix+package or row.get('Action') in ('fail','skip') for row in events):raise ValueError('exact finite current Kafka confirmation events required')
 for action in ('run','pass'):
  actual=[(package,row['Test']) for row in events if row.get('Test') and row.get('Action')==action]
  if len(actual)!=4 or set(actual)!=expected:raise ValueError('current Kafka parent and all three child run/pass pairs required')
 if len([row for row in events if row.get('Action')=='pass' and not row.get('Test')])!=1:raise ValueError('original current package completion required')
 phases=[]
 for path in sorted(out.glob('phase-*.json')):
  phase=json.loads(run.regular(path,1<<20))
  if phase.get('phase')==control['phase']:phases.append(phase)
 if len(phases)!=1:raise ValueError('one original current Kafka confirmation phase required')
 phase=phases[0];resource=phase.get('resource',{})
 if type(phase.get('exitCode')) is not int or phase['exitCode']!=0 or resource.get('status')!='passed' or type(resource.get('exit_code')) is not int or resource['exit_code']!=0:raise ValueError('actual successful original Kafka confirmation process required')
 post=json.loads(run.regular(out/'source-post.json',4096));before=json.loads(run.regular(out/'tools-before.json',65536));after=json.loads(run.regular(out/'tools-after.json',65536))
 if post!={'accepted':True,'source':run.FINAL_SOURCE} or before!=after:raise ValueError('current original source/tool post differs')
 historical=run.check_kafka_acl_receipt(json.loads(run.regular(out/'kafka-acl-historical-pass.json',65536)))
 run.check_process_image_prior(json.loads(run.regular(out/'process-image-historical-pass.json',65536)))
 if historical['source']==run.FINAL_SOURCE or (out/'kafka-acl-passed-reuse.json').exists() or (out/'current-image-kafka-names.json').exists():raise ValueError('current Kafka confirmation cannot alias older Kafka or image dispatch')
 return value

def checked_kafka_confirm_reuse_evidence(out):
 receipt=run.check_kafka_confirm_reuse_receipt(json.loads(run.regular(out/'kafka-confirm-passed-reuse.json',65536)))
 post=json.loads(run.regular(out/'source-post.json',4096));before=json.loads(run.regular(out/'tools-before.json',65536));after=json.loads(run.regular(out/'tools-after.json',65536))
 if post!={'accepted':True,'source':run.FINAL_SOURCE} or before!=after:raise ValueError('current source/tool post differs from original Kafka confirmation reuse')
 actual={key:before.get('go',{}).get(key) for key in ('bytes','sha256','mode')}
 if json.dumps(actual,sort_keys=True)!=json.dumps(receipt['goTool'],sort_keys=True):raise ValueError('current original Kafka confirmation Go tool differs')
 if any((out/name).exists() for name in ('kafka-acl-passed-reuse.json','current-kafka-confirm-names.json','current-image-kafka-names.json')):raise ValueError('whole-source Kafka reuse cannot alias old a41 or fresh dispatch')
 return receipt

def gateway_start_exit2(terminal):
 predicates={'start-service-process-image-'+step+'-wait-exit-code-2' for step in ('context','before-identity','open','initial-stat','read','final-stat','size','hash','after-identity')}
 return isinstance(terminal,dict) and terminal.get('version')=='owned-fullgraph-token-v2-result-v1' and terminal.get('accepted') is False and terminal.get('scenario') in run.SCENARIOS and terminal.get('sourceSHA256')==run.FINAL_SOURCE['digest'] and terminal.get('constructorStage')=='start' and terminal.get('constructorRole')=='msggateway' and terminal.get('constructorCallState')=='returned' and terminal.get('constructorErrorClass')=='owned-evidence' and terminal.get('constructorPredicate') in predicates

def gateway_projection_empty(status):
 return {'version':run.GATEWAY_START_LOG_POLICY['version'],'status':status,'headerClass':'none','execErrorStage':'none','errorPrefixClass':'none','panicPrefixClasses':[],'sourceFrames':[],'scanTruncated':False,'rawTextExported':False,'privatePathsExported':False,'diagnosticOnly':True,'acceptanceEvidence':False,'prefixIsCauseProof':False}

def gateway_wrapper_sites(bodies):
 # Tools v0.0.23 Wrap1/2/3 use a qualified function + line, not a Go path.
 # Only a unique call outside comments/literals in this exact source may map
 # that reference back to a public file. No symbol is exported or trusted.
 re=__import__('re');sites={}
 literals=re.compile(rb'//[^\n]*|/\*.*?\*/|"(?:\\.|[^"\\])*"|`[^`]*`|\'(?:\\.|[^\'\\])*\'',re.DOTALL)
 declaration=re.compile(rb'^func[ \t]+(?:\(([A-Za-z_][A-Za-z0-9_]*)[ \t]+(\*?)([A-Za-z_][A-Za-z0-9_]*)\)[ \t]*)?([A-Za-z_][A-Za-z0-9_]*)[ \t]*\(')
 call=re.compile(rb'\butils[ \t]*\.[ \t]*Wrap[123][ \t]*\(')
 for name,body in bodies.items():
  if not isinstance(name,str) or not name.startswith('server/') or not name.endswith('.go') or any(part in ('','.', '..') for part in name.split('/')) or '\\' in name or not isinstance(body,bytes) or b'"github.com/OpenIMSDK/tools/utils"' not in body:continue
  masked=literals.sub(lambda match:bytes(10 if char==10 else 32 for char in match[0]),body);owner=None
  for number,line in enumerate(masked.splitlines(),1):
   if line.startswith(b'func '):
    match=declaration.match(line);owner=None
    if match is not None:
     owner=match[4].decode('ascii')
     if match[3] is not None:
      receiver=match[3].decode('ascii');owner=('(*'+receiver+')' if match[2] else receiver)+'.'+owner
   if owner is not None and call.search(line):sites.setdefault((name.rsplit('/',1)[0][7:],number,owner),[]).append(name)
 return sites

def gateway_fixed_wrapper(payload,sites):
 pattern=rb'==> github\.com/openimsdk/open-im-server/v3/([A-Za-z0-9_-]+(?:/[A-Za-z0-9_-]+)*)\.((?:[A-Za-z_][A-Za-z0-9_]*|\(\*[A-Za-z_][A-Za-z0-9_]*\))(?:\.[A-Za-z_][A-Za-z0-9_]*)*)\(\)@([1-9][0-9]{0,6}): : '
 match=__import__('re').match(pattern,payload)
 if match is None:return None
 package,symbol=match[1].decode('ascii'),match[2].decode('ascii')
 owner=__import__('re').sub(r'(?:\.func[1-9][0-9]*)+$','',symbol);number=int(match[3])
 candidates=sites.get((package,number,owner),[])
 if len(candidates)!=1:return None
 return payload[match.end():],{'path':candidates[0],'line':number}

def gateway_error_prefix(payload,sites):
 policy=run.GATEWAY_START_LOG_POLICY;stage='unclassified';frames=[];truncated=False
 wrappers=((b'failed to get configuration from command: ','configuration'),(b'failed to initialize from config: ','logger'))
 for _ in range(policy['wrapperLimit']):
  wrapper=next(((prefix,kind) for prefix,kind in wrappers if payload.startswith(prefix)),None)
  if wrapper is not None:
   if stage=='unclassified':stage=wrapper[1]
   payload=payload[len(wrapper[0]):];continue
  fixed=gateway_fixed_wrapper(payload,sites)
  if fixed is None:break
  payload,frame=fixed;frames.append(frame)
 else:
  if any(payload.startswith(prefix) for prefix,_ in wrappers) or gateway_fixed_wrapper(payload,sites) is not None:truncated=True
 prefixes=(
  (b'runtime error: invalid memory address or nil pointer dereference','runtime-nil'),
  (b'unmarshal yaml error: ','config-yaml'),(b'yaml:','config-yaml'),
  (b'stat config path error: ','config-stat'),(b'read file error: ','config-read'),
  ('配置项 secret 不安全（为空、占位符、默认值或少于 16 位），请换成随机强密钥后再启动'.encode(),'config-secret'),
  (b'owned RPC lifecycle is unconfirmed','owned-rpc'),(b'owned gateway lifecycle is unconfirmed','owned-gateway'),
  (b'original owned Redis source is unconfirmed','owned-redis'),
  (b'owned namespace control evidence invalid','namespace-evidence'),
  (b'owned namespace runtime registration pending','namespace-registration-pending'),
  (b'owned history reader has not registered yet','history-registration-pending'),
  (b'original owned Mongo observer is required','owned-mongo-observer'),
  (b'original source outcome owner is required','owned-source-observer'),
  (b'owned discovery lifetime required','owned-discovery'),(b'owned discovery context required','owned-discovery'),
  (b'owned discovery type refused','owned-discovery'),(b'owned discovery returned nil connection','owned-discovery'),
  (b'owned discovery connection closed before ready','owned-discovery'),(b'owned discovery connection timeout','owned-discovery'),
  (b'owned discovery is closed','owned-discovery'),(b'owned discovery close is unconfirmed','owned-discovery'),
  (b'callback transport type is unconfirmed','callback-transport'),(b'callback transport is absent','callback-transport'),
  (b'context deadline exceeded','deadline'),(b'context canceled','cancelled'),
  (b'json:','json'),(b'invalid character ','json'),(b'open ','file-open'),(b'listen ','listen'),(b'dial ','dial'),(b'tls:','tls'),(b'x509:','x509'))
 kind=next((kind for prefix,kind in prefixes if payload.startswith(prefix)),'other')
 return stage,kind,frames,truncated

def project_service_log_bytes(raw,bodies,policy,empty,prefix_classify):
 if not isinstance(raw,bytes) or len(raw)>policy['logLimitBytes']:raise ValueError('bounded original private gateway output required')
 result=empty('projected');source_lines={}
 for name,body in bodies.items():
  if isinstance(name,str) and name.startswith('server/') and name.endswith('.go') and all(part not in ('','.', '..') for part in name.split('/')) and '\\' not in name and isinstance(body,bytes):source_lines[name]=body.count(b'\n')+1
 sites=gateway_wrapper_sites({name:bodies[name] for name in source_lines})
 pattern=__import__('re').compile(rb'[ \t]+([^\x00-\x1f\x7f\\]+\.go):([1-9][0-9]{0,6})(?: \+0x[0-9a-fA-F]+)?(?: fp=0x[0-9a-fA-F]+ sp=0x[0-9a-fA-F]+ pc=0x[0-9a-fA-F]+)?')
 stack_header=__import__('re').compile(rb'goroutine [1-9][0-9]* \[[^\]\r\n]{1,128}\]:')
 start=0;seen=set();classes=set();line_count=0;panic_region=False
 def add_frame(frame):
  key=(frame['path'],frame['line'])
  if key in seen:return
  seen.add(key)
  if len(result['sourceFrames'])>=policy['framesLimit']:result['scanTruncated']=True;return
  result['sourceFrames'].append(frame)
 while start<len(raw):
  if line_count>=policy['lineLimit']:result['scanTruncated']=True;break
  end=raw.find(b'\n',start)
  if end<0:end=len(raw)
  line_count+=1
  if end-start>policy['lineBytesLimit']:
   result['scanTruncated']=True;start=end+1;continue
  line=raw[start:end].rstrip(b'\r');start=end+1;payload=None;first=False
  if result['headerClass']=='none' and (line.startswith(b'panic: ') or line.startswith(b'fatal error: ')):
   panic=line.startswith(b'panic: ');payload=line[7:] if panic else line[13:]
   result['headerClass']='go-panic' if panic else 'go-runtime-fatal';panic_region=True;first=True
  elif panic_region:
   if stack_header.fullmatch(line) or line in (b'runtime stack:',b'stack trace:'):panic_region=False
   elif line:
    # Go1.27.2 runtime.printindented inserts exactly one TAB per panic newline.
    # Preserve the original line for stack boundaries and source-frame parsing.
    payload=line[1:] if result['headerClass']=='go-panic' and line.startswith(b'\t') else line
  if payload is not None:
   stage,kind,frames,truncated=prefix_classify(payload,sites)
   if first:result['execErrorStage']=stage;result['errorPrefixClass']=kind
   if kind!='other' and kind not in classes:
    classes.add(kind)
    if len(result['panicPrefixClasses'])>=policy['panicPrefixClassesLimit']:result['scanTruncated']=True
    else:result['panicPrefixClasses'].append(kind)
   for frame in frames:add_frame(frame)
   if truncated:result['scanTruncated']=True
  match=pattern.fullmatch(line)
  if match is None:continue
  try:path=match[1].decode('utf-8')
  except UnicodeError:continue
  if path.startswith('//') or ':' in path or any(part in ('.','..') for part in path.split('/')):continue
  candidates=[name for name in source_lines if path==name or path.endswith('/'+name)]
  if len(candidates)!=1:continue
  name=candidates[0];number=int(match[2])
  if number<=source_lines[name]:add_frame({'path':name,'line':number})
 return result

def project_gateway_log_bytes(raw,bodies):
 return project_service_log_bytes(raw,bodies,run.GATEWAY_START_LOG_POLICY,gateway_projection_empty,gateway_error_prefix)

def rpc_group_start_exit2(terminal):
 return isinstance(terminal,dict) and terminal.get('constructorRole')=='rpc-group' and gateway_start_exit2(dict(terminal,constructorRole='msggateway'))

def rpc_group_projection_empty(status):
 return dict(gateway_projection_empty(status),version=run.RPC_GROUP_START_LOG_POLICY['version'],role='rpc-group')

def rpc_group_error_prefix(payload,sites):
 stage,kind,frames,truncated=gateway_error_prefix(payload,sites)
 if kind=='other':
  fixed=((b'group storage initialization failed','group-storage'),(b'group cache initialization failed','group-cache'),(b'group cache recovery initialization failed','group-cache-recovery'),(b'group authority initialization failed','group-authority'),(b'group authority storage is not ready','group-authority-storage'),(b'group authority cache is not ready','group-authority-cache'),(b'group model initialization failed','group-model'),(b'group member model initialization failed','group-member-model'),(b'group request model initialization failed','group-request-model'),(b'owned group receiver manifest is required','group-receiver-manifest'),(b'group membership reader activation mismatch','group-membership-activation'),(b'owned group membership reader unavailable','group-membership-reader'),(b'owned group membership reader unprepared','group-membership-unprepared'),(b'new group namespace initializer unavailable','group-seed-initializer'),(b'new group namespace migration and complete writer admission required','group-seed-admission'),(b'owned gateway service identity unavailable','group-service-identity'),(b'owned group receiver initialization failed','group-receiver-initialization'),(b'owned group source profile initialization failed','group-source-profile'),(b'owned group receiver readiness rejected','group-receiver-readiness'))
  kind=next((label for prefix,label in fixed if payload.startswith(prefix)),'other')
 return stage,kind,frames,truncated

def project_rpc_group_log_bytes(raw,bodies):
 return project_service_log_bytes(raw,bodies,run.RPC_GROUP_START_LOG_POLICY,rpc_group_projection_empty,rpc_group_error_prefix)




def rpc_group_registration_done(terminal):
 return isinstance(terminal,dict) and terminal.get('version')=='owned-fullgraph-token-v2-result-v1' and terminal.get('accepted') is False and terminal.get('scenario') in run.SCENARIOS and terminal.get('sourceSHA256')==run.FINAL_SOURCE['digest'] and terminal.get('constructorRole')=='rpc-group' and terminal.get('constructorStage')=='observe-registration' and terminal.get('constructorCallState')=='returned' and terminal.get('constructorErrorClass')=='owned-evidence' and terminal.get('constructorPredicate')=='done'

def rpc_group_output_failure(terminal):return rpc_group_start_exit2(terminal) or rpc_group_registration_done(terminal)

def rpc_group_original_owner_nonce(work):
 directory=work/'runtime-owners';path=directory/'rpc-group-runtime-actual-owner.json'
 if directory.resolve()!=directory or path.resolve()!=path:raise ValueError('original owner record must be canonical')
 folder=directory.lstat();before=path.lstat()
 if not stat.S_ISDIR(folder.st_mode) or folder.st_uid!=os.getuid() or stat.S_IMODE(folder.st_mode)!=0o700 or not stat.S_ISREG(before.st_mode) or before.st_uid!=os.getuid() or before.st_nlink!=1 or stat.S_IMODE(before.st_mode)!=0o600:raise ValueError('original private owner record required')
 def unique(pairs):
  value={}
  for key,item in pairs:
   if key in value:raise ValueError('duplicate owner record field')
   value[key]=item
  return value
 raw=run.regular(path,run.RPC_GROUP_START_LOG_POLICY['ownerRecordLimitBytes']);owner=json.loads(raw,object_pairs_hook=unique);after=path.lstat();again=directory.lstat();identity=lambda v:(v.st_dev,v.st_ino,v.st_mode,v.st_uid,v.st_nlink,v.st_size,v.st_mtime_ns,v.st_ctime_ns)
 keys={'role','pid','uid','startToken','bootID','executable','binarySHA256','sourceSHA256','buildReceiptSHA256','bootstrapSHA256','ownerNonce'}
 if identity(before)!=identity(after) or (folder.st_dev,folder.st_ino,folder.st_mode,folder.st_uid)!=(again.st_dev,again.st_ino,again.st_mode,again.st_uid) or not isinstance(owner,dict) or set(owner)!=keys or owner['role']!='rpc-group' or owner['sourceSHA256']!=run.FINAL_SOURCE['digest'] or type(owner['pid']) is not int or owner['pid']<=1 or type(owner['uid']) is not int or owner['uid']!=os.getuid() or owner['executable']!=str(work.parent/'bin/rpc-group'):raise ValueError('original role/source/owner record differs')
 if any(not isinstance(owner[key],str) or not __import__('re').fullmatch('[0-9a-f]{64}',owner[key]) for key in ('binarySHA256','buildReceiptSHA256','bootstrapSHA256','ownerNonce')) or any(not isinstance(owner[key],str) or not owner[key] for key in ('startToken','bootID')):raise ValueError('finite original owner identity required')
 return owner['ownerNonce']

def rpc_group_output_boundary(terminal):
 done=rpc_group_registration_done(terminal)
 return {'failureBoundary':'observe-registration-done' if done else 'startup-cached-wait-exit2','originalWaitReturned':True,'originalWaitResult':'not-exported' if done else 'exit-code-2','stdoutSyncAndCloseCallsReturned':True,'stdoutFinalizationResult':'not-exported','stdoutOverflowResult':'not-exported','originalOwnerRecordStatus':'matched' if done else 'not-required-for-startup','exitCodeInferredFromLog':False}

def project_service_start_log(work,terminal,bodies,role):
 if role=='msggateway':predicate,empty,project,policy=gateway_start_exit2,gateway_projection_empty,project_gateway_log_bytes,run.GATEWAY_START_LOG_POLICY
 elif role=='rpc-group':predicate,empty,project,policy=rpc_group_output_failure,rpc_group_projection_empty,project_rpc_group_log_bytes,run.RPC_GROUP_START_LOG_POLICY
 else:return None
 if not predicate(terminal):return None
 result=empty('unavailable');result.update(source=run.FINAL_SOURCE,scenario=terminal['scenario'])
 if role=='rpc-group':
  result.update(rpc_group_output_boundary(terminal))
  if rpc_group_registration_done(terminal):result['originalOwnerRecordStatus']='not-observed'
 try:
  work=Path(work);directory=work/'services/required'/role
  if work.resolve()!=work or directory.resolve()!=directory:return dict(result,status='unsafe')
  for folder in (work,work/'services',work/'services/required',directory):
   info=folder.lstat()
   if not stat.S_ISDIR(info.st_mode) or info.st_uid!=os.getuid() or stat.S_IMODE(info.st_mode)!=0o700:return dict(result,status='unsafe')
  owner_nonce=None
  if role=='rpc-group' and rpc_group_registration_done(terminal):
   try:owner_nonce=rpc_group_original_owner_nonce(work)
   except OSError:return dict(result,status='unavailable',originalOwnerRecordStatus='unavailable')
   except (ValueError,RuntimeError):return dict(result,status='unsafe',originalOwnerRecordStatus='invalid')
  matches=[]
  with os.scandir(directory) as entries:
   for count,entry in enumerate(entries):
    if count>=policy['directoryEntriesLimit']:return dict(result,status='unsafe')
    if __import__('re').fullmatch('service-[0-9a-f]{64}\\.log',entry.name):matches.append(Path(entry.path))
  if len(matches)!=1:return dict(result,status='absent' if not matches else 'ambiguous')
  path=matches[0]
  if owner_nonce is not None and path.name!='service-'+owner_nonce+'.log':return dict(result,status='unsafe',originalOwnerRecordStatus='log-nonce-mismatch')
  before=path.lstat()
  if not stat.S_ISREG(before.st_mode) or before.st_uid!=os.getuid() or before.st_nlink!=1 or stat.S_IMODE(before.st_mode)!=0o600:return dict(result,status='unsafe')
  raw=run.regular(path,policy['logLimitBytes']);after=path.lstat()
  identity=lambda v:(v.st_dev,v.st_ino,v.st_mode,v.st_uid,v.st_nlink,v.st_size,v.st_mtime_ns,v.st_ctime_ns)
  if identity(before)!=identity(after):return dict(result,status='unsafe')
  projected=project(raw,bodies);projected.update(source=run.FINAL_SOURCE,scenario=terminal['scenario'])
  if role=='rpc-group':projected.update(rpc_group_output_boundary(terminal))
  return projected
 except RuntimeError:return dict(result,status='unsafe')  # A cyclic intermediate symlink cannot be canonical.
 except (OSError,ValueError,UnicodeError):return result

def project_gateway_start_log(work,terminal,bodies):
 return project_service_start_log(work,terminal,bodies,'msggateway')

def project_rpc_group_start_log(work,terminal,bodies):
 return project_service_start_log(work,terminal,bodies,'rpc-group')

def emit_gateway_start_log_diagnostic(out,work,terminal):
 # Best effort after the validated original cached Wait observation. No new
 # Wait, process probe, signal, log replay or outcome promotion is possible.
 if not gateway_start_exit2(terminal):return False
 try:
  _,bodies,_,_,_,_=run.verify()
  value=project_gateway_start_log(work,terminal,bodies)
  if value is not None:run.write(out/('gateway-start-log-'+terminal['scenario']+'.json'),value)
  return value is not None
 except Exception:return False

def emit_rpc_group_start_log_diagnostic(out,work,terminal):
 # Best effort after the validated original cached Wait observation. No new
 # Wait, process probe, signal, log replay or outcome promotion is possible.
 if not rpc_group_output_failure(terminal):return False
 try:
  _,bodies,_,_,_,_=run.verify()
  value=project_rpc_group_start_log(work,terminal,bodies)
  if value is not None:run.write(out/(('rpc-group-registration-done-log-' if rpc_group_registration_done(terminal) else 'rpc-group-start-log-')+terminal['scenario']+'.json'),value)
  return value is not None
 except Exception:return False

def checked_current_release_evidence(out):
 value=run.check_current_release_receipt(json.loads(run.regular(out/'current-release-controls-names.json',65536)))
 if json.loads(run.regular(out/'current-release-kafka-individual.json',65536))!=value['individualKafkaFact']:raise ValueError('same-invocation Kafka individual fact differs')
 control=run.CURRENT_RELEASE_CONTROL;package=next(iter(control['targets']));prefix='github.com/openimsdk/open-im-server/v3/'
 events=json.loads(run.regular(out/'current-release-controls-events.json',1<<20));expected={(row['package'],row['name']) for row in value['namedCompletions']}
 if not isinstance(events,list) or not events or any(set(row)-{'Time','Action','Package','Test','Elapsed'} or row.get('Package')!=prefix+package or row.get('Action') in ('fail','skip') for row in events):raise ValueError('finite original release-control events required')
 for action in ('run','pass'):
  actual=[(package,row['Test']) for row in events if row.get('Test') and row.get('Action')==action]
  if len(actual)!=26 or set(actual)!=expected:raise ValueError('all four release tops and every child run/pass pair required')
 if len([row for row in events if row.get('Action')=='pass' and not row.get('Test')])!=1:raise ValueError('one original package completion required')
 phases=[]
 for path in sorted(out.glob('phase-*.json')):
  phase=json.loads(run.regular(path,1<<20))
  if phase.get('phase')==control['phase']:phases.append(phase)
 if len(phases)!=1:raise ValueError('one original shared release/Kafka race phase required')
 phase=phases[0];resource=phase.get('resource',{})
 if type(phase.get('exitCode')) is not int or phase['exitCode']!=0 or resource.get('status')!='passed' or type(resource.get('exit_code')) is not int or resource['exit_code']!=0:raise ValueError('actual successful original release-control process required')
 post=json.loads(run.regular(out/'source-post.json',4096));before=json.loads(run.regular(out/'tools-before.json',65536));after=json.loads(run.regular(out/'tools-after.json',65536))
 if post!={'accepted':True,'source':run.FINAL_SOURCE} or before!=after:raise ValueError('current release source/tool post differs')
 run.check_kafka_confirm_prior_pass(json.loads(run.regular(out/'kafka-confirm-historical-pass.json',65536)))
 run.check_kafka_acl_receipt(json.loads(run.regular(out/'kafka-acl-historical-pass.json',65536)));run.check_process_image_prior(json.loads(run.regular(out/'process-image-historical-pass.json',65536)))
 if any((out/name).exists() for name in ('kafka-acl-passed-reuse.json','kafka-confirm-passed-reuse.json','current-kafka-confirm-names.json','current-image-kafka-names.json')):raise ValueError('new source release race cannot alias any prior mode')
 return value

def checked_current_managed_shape_evidence(out):
 value=run.check_current_managed_shape_receipt(json.loads(run.regular(out/'current-managed-shape-controls-names.json',65536)))
 if json.dumps(json.loads(run.regular(out/'current-managed-shape-kafka-individual.json',65536)),sort_keys=True)!=json.dumps(value['individualKafkaFact'],sort_keys=True):raise ValueError('same-command Kafka subset differs')
 control=run.CURRENT_MANAGED_SHAPE_CONTROL;prefix='github.com/openimsdk/open-im-server/v3/';packages={prefix+package for package in control['targets']}
 events=json.loads(run.regular(out/'current-managed-shape-controls-events.json',1<<20));expected={(row['package'],row['name']) for row in value['namedCompletions']}
 if not isinstance(events,list) or not events or any(set(row)-{'Time','Action','Package','Test','Elapsed'} or row.get('Package') not in packages or row.get('Action') in ('fail','skip') for row in events):raise ValueError('finite original two-package events required')
 for action in ('run','pass'):
  actual=[(row['Package'][len(prefix):],row['Test']) for row in events if row.get('Test') and row.get('Action')==action]
  if len(actual)!=18 or set(actual)!=expected:raise ValueError('both exact tops and sixteen child run/pass pairs required')
 for package in packages:
  if len([row for row in events if row.get('Package')==package and row.get('Action')=='pass' and not row.get('Test')])!=1:raise ValueError('one completion for each original package required')
 phases=[]
 for path in sorted(out.glob('phase-*.json')):
  phase=json.loads(run.regular(path,1<<20))
  if phase.get('phase')==control['phase']:phases.append(phase)
 if len(phases)!=1:raise ValueError('one original two-package Go command phase required')
 phase=phases[0];resource=phase.get('resource',{})
 if type(phase.get('exitCode')) is not int or phase['exitCode']!=0 or resource.get('status')!='passed' or type(resource.get('exit_code')) is not int or resource['exit_code']!=0:raise ValueError('actual successful original managed-shape command required')
 post=json.loads(run.regular(out/'source-post.json',4096));before=json.loads(run.regular(out/'tools-before.json',65536));after=json.loads(run.regular(out/'tools-after.json',65536))
 if post!={'accepted':True,'source':run.FINAL_SOURCE} or before!=after:raise ValueError('current two-package source/tool post differs')
 run.check_release_controls_prior_pass(json.loads(run.regular(out/'release-controls-historical-pass.json',65536)));run.check_kafka_confirm_prior_pass(json.loads(run.regular(out/'kafka-confirm-historical-pass.json',65536)))
 run.check_kafka_acl_receipt(json.loads(run.regular(out/'kafka-acl-historical-pass.json',65536)));run.check_process_image_prior(json.loads(run.regular(out/'process-image-historical-pass.json',65536)))
 if any((out/name).exists() for name in ('current-release-controls-names.json','kafka-acl-passed-reuse.json','kafka-confirm-passed-reuse.json','current-kafka-confirm-names.json','current-image-kafka-names.json')):raise ValueError('new two-package command cannot alias prior scopes or reuse')
 return value

def checked_completed_kafka_package_reuse_evidence(out):
 post=json.loads(run.regular(out/'source-post.json',4096));before=json.loads(run.regular(out/'tools-before.json',65536));after=json.loads(run.regular(out/'tools-after.json',65536))
 if post!={'accepted':True,'source':run.FINAL_SOURCE} or before!=after:raise ValueError('same-source completed Kafka package source/tool post differs')
 value=run.check_completed_kafka_package_reuse(json.loads(run.regular(out/'same-source-completed-kafka-package-reuse.json',65536)),before.get('go',{}))
 if json.dumps(json.loads(run.regular(out/'managed-shape-two-package-historical-attempt.json',65536)),sort_keys=True)!=json.dumps(run.MANAGED_SHAPE_TWO_PACKAGE_PRIOR_ATTEMPT,sort_keys=True):raise ValueError('original timed-out two-package scope must remain historical and failed')
 return value

def checked_current_producer_evidence(out):
 value=run.check_current_producer_receipt(json.loads(run.regular(out/'current-managed-producer-control-names.json',65536)))
 control=run.CURRENT_PRODUCER_CONTROL;package=next(iter(control['targets']));prefix='github.com/openimsdk/open-im-server/v3/'
 events=json.loads(run.regular(out/'current-managed-producer-control-events.json',1<<20));expected={(row['package'],row['name']) for row in value['namedCompletions']}
 if not isinstance(events,list) or not events or any(not isinstance(row,dict) or set(row)-{'Time','Action','Package','Test','Elapsed'} or row.get('Package')!=prefix+package or row.get('Action') not in ('start','run','output','pass','pause','cont') for row in events):raise ValueError('finite producer-only original events required')
 for action in ('run','pass'):
  actual=[(package,row['Test']) for row in events if row.get('Test') and row.get('Action')==action]
  if len(actual)!=14 or set(actual)!=expected:raise ValueError('all producer top/thirteen children run/pass pairs required')
 if len([row for row in events if row.get('Action')=='start' and not row.get('Test')])!=1 or len([row for row in events if row.get('Action')=='pass' and not row.get('Test')])!=1:raise ValueError('one original producer package start and completion required')
 phases=[]
 for path in sorted(out.glob('phase-*.json')):
  phase=json.loads(run.regular(path,1<<20))
  if phase.get('phase')==control['phase']:phases.append(phase)
 if len(phases)!=1:raise ValueError('one original producer-only Go command phase required')
 phase=phases[0];resource=phase.get('resource',{})
 if type(phase.get('exitCode')) is not int or phase['exitCode']!=0 or resource.get('status')!='passed' or type(resource.get('exit_code')) is not int or resource['exit_code']!=0:raise ValueError('successful original producer command required')
 checked_completed_kafka_package_reuse_evidence(out)
 run.check_release_controls_prior_pass(json.loads(run.regular(out/'release-controls-historical-pass.json',65536)));run.check_kafka_confirm_prior_pass(json.loads(run.regular(out/'kafka-confirm-historical-pass.json',65536)))
 run.check_kafka_acl_receipt(json.loads(run.regular(out/'kafka-acl-historical-pass.json',65536)));run.check_process_image_prior(json.loads(run.regular(out/'process-image-historical-pass.json',65536)))
 if any((out/name).exists() for name in ('current-managed-shape-controls-names.json','current-managed-shape-kafka-individual.json','current-release-controls-names.json','kafka-acl-passed-reuse.json','kafka-confirm-passed-reuse.json','current-kafka-confirm-names.json','current-image-kafka-names.json')):raise ValueError('fresh producer cannot alias old two-package or Kafka invocations')
 return value

def checked_current_group_recovery_evidence(out):
 value=run.check_current_group_recovery_receipt(json.loads(run.regular(out/'current-group-recovery-controls-names.json',65536)))
 control=run.CURRENT_GROUP_RECOVERY_CONTROL;prefix='github.com/openimsdk/open-im-server/v3/';packages={prefix+package for package in control['targets']}
 events=json.loads(run.regular(out/'current-group-recovery-controls-events.json',1<<20));expected={(row['package'],row['name']) for row in value['namedCompletions']}
 if not isinstance(events,list) or not events or any(not isinstance(row,dict) or set(row)-{'Time','Action','Package','Test','Elapsed'} or row.get('Package') not in packages or row.get('Action') not in ('start','run','output','pass','pause','cont') for row in events):raise ValueError('finite fresh three-package original events required')
 for action in ('run','pass'):
  actual=[(row['Package'][len(prefix):],row['Test']) for row in events if row.get('Test') and row.get('Action')==action]
  if len(actual)!=27 or set(actual)!=expected:raise ValueError('all four original tops/twenty-three children run/pass pairs required')
 for package in packages:
  if len([row for row in events if row.get('Package')==package and row.get('Action')=='start' and not row.get('Test')])!=1 or len([row for row in events if row.get('Package')==package and row.get('Action')=='pass' and not row.get('Test')])!=1:raise ValueError('one original start/footer for each package required')
 phases=[]
 for path in sorted(out.glob('phase-*.json')):
  phase=json.loads(run.regular(path,1<<20))
  if phase.get('phase')==control['phase']:phases.append(phase)
 if len(phases)!=1:raise ValueError('one original three-package Go command phase required')
 phase=phases[0];resource=phase.get('resource',{})
 if type(phase.get('exitCode')) is not int or phase['exitCode']!=0 or resource.get('status')!='passed' or type(resource.get('exit_code')) is not int or resource['exit_code']!=0:raise ValueError('original fresh three-package command must have succeeded')
 post=json.loads(run.regular(out/'source-post.json',4096));before=json.loads(run.regular(out/'tools-before.json',65536));after=json.loads(run.regular(out/'tools-after.json',65536))
 if post!={'accepted':True,'source':run.FINAL_SOURCE} or before!=after:raise ValueError('new-source three-package source/tool post differs')
 run.check_group_recovery_historical_facts(json.loads(run.regular(out/'group-recovery-historical-facts.json',65536)))
 run.check_release_controls_prior_pass(json.loads(run.regular(out/'release-controls-historical-pass.json',65536)));run.check_kafka_confirm_prior_pass(json.loads(run.regular(out/'kafka-confirm-historical-pass.json',65536)))
 run.check_kafka_acl_receipt(json.loads(run.regular(out/'kafka-acl-historical-pass.json',65536)));run.check_process_image_prior(json.loads(run.regular(out/'process-image-historical-pass.json',65536)))
 if any((out/name).exists() for name in ('same-source-completed-kafka-package-reuse.json','current-managed-producer-control-names.json','current-managed-shape-controls-names.json','current-managed-shape-kafka-individual.json','current-release-controls-names.json','kafka-acl-passed-reuse.json','kafka-confirm-passed-reuse.json','current-kafka-confirm-names.json','current-image-kafka-names.json')):raise ValueError('new source cannot alias prior completed packages or older scopes')
 return value

def checked_cache_test_delta_reuse_evidence(out):
 value=json.loads(run.regular(out/'completed-package-test-delta-reuse.json',65536));post=json.loads(run.regular(out/'source-post.json',4096));before=json.loads(run.regular(out/'tools-before.json',65536));after=json.loads(run.regular(out/'tools-after.json',65536))
 if post!={'accepted':True,'source':run.FINAL_SOURCE} or before!=after:raise ValueError('test-delta compatibility requires exact current source and unchanged tools')
 _,bodies,recipe,*_=run.verify()
 return run.check_cache_test_delta_reuse(value,bodies,recipe,before.get('go',{}))

def checked_current_cache_test_delta_evidence(out):
 value=run.check_current_cache_test_delta_receipt(json.loads(run.regular(out/'current-cache-test-delta-controls-names.json',65536)))
 control=run.CURRENT_CACHE_TEST_DELTA_CONTROL;package=next(iter(control['targets']));prefix='github.com/openimsdk/open-im-server/v3/'
 events=json.loads(run.regular(out/'current-cache-test-delta-controls-events.json',1<<20));expected={(row['package'],row['name']) for row in value['namedCompletions']}
 if not isinstance(events,list) or not events or any(not isinstance(row,dict) or set(row)-{'Time','Action','Package','Test','Elapsed'} or row.get('Package')!=prefix+package or row.get('Action') not in ('start','run','output','pass','pause','cont') for row in events):raise ValueError('finite original fresh cache events required')
 for action in ('run','pass'):
  actual=[(package,row['Test']) for row in events if row.get('Test') and row.get('Action')==action]
  if len(actual)!=9 or set(actual)!=expected:raise ValueError('both original cache tops/seven children run/pass pairs required')
 if len([row for row in events if row.get('Action')=='start' and not row.get('Test')])!=1 or len([row for row in events if row.get('Action')=='pass' and not row.get('Test')])!=1:raise ValueError('one original cache package start/footer required')
 phases=[]
 for path in sorted(out.glob('phase-*.json')):
  phase=json.loads(run.regular(path,1<<20))
  if phase.get('phase')==control['phase']:phases.append(phase)
 if len(phases)!=1:raise ValueError('one original cache-only Go command phase required')
 phase=phases[0];resource=phase.get('resource',{})
 if type(phase.get('exitCode')) is not int or phase['exitCode']!=0 or resource.get('status')!='passed' or type(resource.get('exit_code')) is not int or resource['exit_code']!=0:raise ValueError('successful fresh cache command required')
 checked_cache_test_delta_reuse_evidence(out)
 run.check_group_recovery_historical_facts(json.loads(run.regular(out/'group-recovery-historical-facts.json',65536)));run.check_release_controls_prior_pass(json.loads(run.regular(out/'release-controls-historical-pass.json',65536)));run.check_kafka_confirm_prior_pass(json.loads(run.regular(out/'kafka-confirm-historical-pass.json',65536)))
 run.check_kafka_acl_receipt(json.loads(run.regular(out/'kafka-acl-historical-pass.json',65536)));run.check_process_image_prior(json.loads(run.regular(out/'process-image-historical-pass.json',65536)))
 if any((out/name).exists() for name in ('current-group-recovery-controls-names.json','same-source-completed-kafka-package-reuse.json','current-managed-producer-control-names.json','current-managed-shape-controls-names.json','current-managed-shape-kafka-individual.json','current-release-controls-names.json','kafka-acl-passed-reuse.json','kafka-confirm-passed-reuse.json','current-kafka-confirm-names.json','current-image-kafka-names.json')):raise ValueError('new cache-only branch cannot alias original failed command or redispatch completed packages')
 return value

def checked_cron_fixture_scope_reuse_evidence(out):
 value=json.loads(run.regular(out/'cron-fixture-named-scope-reuse.json',1<<20));observed=run.check_cron_fixture_dependency_receipt(json.loads(run.regular(out/'cron-fixture-reused-scope-deps.json',1<<20)))
 if value.get('dependencyEvidence')!=observed:raise ValueError('reuse must refer to the same actual fixed-root dependency projection')
 phases=[]
 for path in sorted(out.glob('phase-*.json')):
  phase=json.loads(run.regular(path,1<<20))
  if phase.get('phase')==run.CRON_FIXTURE_DEPENDENCY_CONTROL['phase']:phases.append(phase)
 if len(phases)!=1:raise ValueError('one actual original fixed go-list phase required')
 phase=phases[0];resource=phase.get('resource',{})
 if type(phase.get('exitCode')) is not int or phase['exitCode']!=0 or resource.get('status')!='passed' or type(resource.get('exit_code')) is not int or resource['exit_code']!=0:raise ValueError('only complete successful original dependency command permits scope reuse')
 post=json.loads(run.regular(out/'source-post.json',4096));before=json.loads(run.regular(out/'tools-before.json',65536));after=json.loads(run.regular(out/'tools-after.json',65536))
 if post!={'accepted':True,'source':run.FINAL_SOURCE} or before!=after:raise ValueError('exact current source and unchanged tools required')
 _,bodies,recipe,*_=run.verify()
 return run.check_cron_fixture_scope_reuse_receipt(value,bodies,recipe,before.get('go',{}))

def checked_current_cron_fixture_evidence(out):
 value=run.check_current_cron_fixture_receipt(json.loads(run.regular(out/'current-cron-fixture-contract-names.json',65536)));control=run.CURRENT_CRON_FIXTURE_CONTROL;package=next(iter(control['targets']));prefix='github.com/openimsdk/open-im-server/v3/'
 events=json.loads(run.regular(out/'current-cron-fixture-contract-events.json',1<<20));expected={(row['package'],row['name']) for row in value['namedCompletions']}
 if not isinstance(events,list) or not events or any(not isinstance(row,dict) or set(row)-{'Time','Action','Package','Test','Elapsed'} or row.get('Package')!=prefix+package or row.get('Action') not in ('start','run','output','pass','pause','cont') for row in events):raise ValueError('finite original fixture contract events required')
 for action in ('run','pass'):
  actual=[(package,row['Test']) for row in events if row.get('Test') and row.get('Action')==action]
  if len(actual)!=4 or set(actual)!=expected:raise ValueError('fixture top and all three original child run/pass pairs required')
 if len([row for row in events if row.get('Action')=='start' and not row.get('Test')])!=1 or len([row for row in events if row.get('Action')=='pass' and not row.get('Test')])!=1:raise ValueError('one original fixture package start/footer required')
 phases=[]
 for path in sorted(out.glob('phase-*.json')):
  phase=json.loads(run.regular(path,1<<20))
  if phase.get('phase')==control['phase']:phases.append(phase)
 if len(phases)!=1:raise ValueError('one original fixture contract command required')
 phase=phases[0];resource=phase.get('resource',{})
 if type(phase.get('exitCode')) is not int or phase['exitCode']!=0 or resource.get('status')!='passed' or type(resource.get('exit_code')) is not int or resource['exit_code']!=0:raise ValueError('actual fresh fixture contract must succeed')
 checked_cron_fixture_scope_reuse_evidence(out)
 run.check_group_recovery_historical_facts(json.loads(run.regular(out/'group-recovery-historical-facts.json',65536)));run.check_release_controls_prior_pass(json.loads(run.regular(out/'release-controls-historical-pass.json',65536)));run.check_kafka_confirm_prior_pass(json.loads(run.regular(out/'kafka-confirm-historical-pass.json',65536)));run.check_kafka_acl_receipt(json.loads(run.regular(out/'kafka-acl-historical-pass.json',65536)));run.check_process_image_prior(json.loads(run.regular(out/'process-image-historical-pass.json',65536)))
 if any((out/name).exists() for name in ('completed-package-test-delta-reuse.json','current-cache-test-delta-controls-names.json','current-group-recovery-controls-names.json','current-managed-producer-control-names.json','same-source-completed-kafka-package-reuse.json','current-managed-shape-controls-names.json','current-release-controls-names.json','kafka-acl-passed-reuse.json','kafka-confirm-passed-reuse.json','current-kafka-confirm-names.json','current-image-kafka-names.json')):raise ValueError('new fixture-only branch must not alias prior controls or redispatch unaffected scopes')
 return value

def checked_group_registry_fixture_scope_reuse_evidence(out):
 value=json.loads(run.regular(out/'group-registry-fixture-named-scope-reuse.json',1<<20));observed=run.check_group_registry_fixture_dependency_receipt(json.loads(run.regular(out/'group-registry-fixture-reused-scope-deps.json',1<<20)))
 if value.get('dependencyEvidence')!=observed:raise ValueError('reuse must refer to the same actual fixed-root dependency projection')
 phases=[]
 for path in sorted(out.glob('phase-*.json')):
  phase=json.loads(run.regular(path,1<<20))
  if phase.get('phase')==run.GROUP_REGISTRY_FIXTURE_DEPENDENCY_CONTROL['phase']:phases.append(phase)
 if len(phases)!=1:raise ValueError('one actual original fixed go-list phase required')
 phase=phases[0];resource=phase.get('resource',{})
 if type(phase.get('exitCode')) is not int or phase['exitCode']!=0 or resource.get('status')!='passed' or type(resource.get('exit_code')) is not int or resource['exit_code']!=0:raise ValueError('only complete successful original dependency command permits scope reuse')
 post=json.loads(run.regular(out/'source-post.json',4096));before=json.loads(run.regular(out/'tools-before.json',65536));after=json.loads(run.regular(out/'tools-after.json',65536))
 if post!={'accepted':True,'source':run.FINAL_SOURCE} or before!=after:raise ValueError('exact current source and unchanged tools required')
 _,bodies,recipe,*_=run.verify()
 return run.check_group_registry_fixture_scope_reuse_receipt(value,bodies,recipe,before.get('go',{}))

def checked_current_group_registry_fixture_evidence(out):
 value=run.check_current_group_registry_fixture_receipt(json.loads(run.regular(out/'current-group-registry-fixture-contract-names.json',65536)));control=run.CURRENT_GROUP_REGISTRY_FIXTURE_CONTROL;package=next(iter(control['targets']));prefix='github.com/openimsdk/open-im-server/v3/'
 events=json.loads(run.regular(out/'current-group-registry-fixture-contract-events.json',1<<20));expected={(row['package'],row['name']) for row in value['namedCompletions']}
 if not isinstance(events,list) or not events or any(not isinstance(row,dict) or set(row)-{'Time','Action','Package','Test','Elapsed'} or row.get('Package')!=prefix+package or row.get('Action') not in ('start','run','output','pass','pause','cont') for row in events):raise ValueError('finite original fixture contract events required')
 for action in ('run','pass'):
  actual=[(package,row['Test']) for row in events if row.get('Test') and row.get('Action')==action]
  if len(actual)!=4 or set(actual)!=expected:raise ValueError('fixture top and all three original child run/pass pairs required')
 if len([row for row in events if row.get('Action')=='start' and not row.get('Test')])!=1 or len([row for row in events if row.get('Action')=='pass' and not row.get('Test')])!=1:raise ValueError('one original fixture package start/footer required')
 phases=[]
 for path in sorted(out.glob('phase-*.json')):
  phase=json.loads(run.regular(path,1<<20))
  if phase.get('phase')==control['phase']:phases.append(phase)
 if len(phases)!=1:raise ValueError('one original fixture contract command required')
 phase=phases[0];resource=phase.get('resource',{})
 if type(phase.get('exitCode')) is not int or phase['exitCode']!=0 or resource.get('status')!='passed' or type(resource.get('exit_code')) is not int or resource['exit_code']!=0:raise ValueError('actual fresh fixture contract must succeed')
 checked_group_registry_fixture_scope_reuse_evidence(out)
 run.check_group_recovery_historical_facts(json.loads(run.regular(out/'group-recovery-historical-facts.json',65536)));run.check_release_controls_prior_pass(json.loads(run.regular(out/'release-controls-historical-pass.json',65536)));run.check_kafka_confirm_prior_pass(json.loads(run.regular(out/'kafka-confirm-historical-pass.json',65536)));run.check_kafka_acl_receipt(json.loads(run.regular(out/'kafka-acl-historical-pass.json',65536)));run.check_process_image_prior(json.loads(run.regular(out/'process-image-historical-pass.json',65536)))
 if any((out/name).exists() for name in ('same-source-current-go-named-scopes-reuse.json','cron-fixture-named-scope-reuse.json','cron-fixture-reused-scope-deps.json','current-cron-fixture-contract-names.json','completed-package-test-delta-reuse.json','current-cache-test-delta-controls-names.json','current-group-recovery-controls-names.json','current-managed-producer-control-names.json','same-source-completed-kafka-package-reuse.json','current-managed-shape-controls-names.json','current-release-controls-names.json','kafka-acl-passed-reuse.json','kafka-confirm-passed-reuse.json','current-kafka-confirm-names.json','current-image-kafka-names.json')):raise ValueError('new fixture-only branch must not alias prior controls or redispatch unaffected scopes')
 return value

def constructor_cached_wait_exit(terminal):
 if not isinstance(terminal,dict) or terminal.get('constructorStage')!='start':return None
 match=__import__('re').fullmatch(r'start-service-process-image-(?:context|before-identity|open|initial-stat|read|final-stat|size|hash|after-identity)-wait-exit-code-(-?[0-9]{1,3})',terminal.get('constructorPredicate',''))
 if match is None:return None
 code=int(match[1]);return code if -1<=code<=255 and str(code)==match[1] else None

def original_constructor_output_failure(terminal):
 if not isinstance(terminal,dict) or terminal.get('version')!='owned-fullgraph-token-v2-result-v1' or terminal.get('accepted') is not False or terminal.get('scenario') not in run.SCENARIOS or terminal.get('sourceSHA256')!=run.FINAL_SOURCE['digest'] or terminal.get('constructorCallState')!='returned' or terminal.get('constructorErrorClass')!='owned-evidence':return False
 if terminal.get('constructorStage')=='await-started':return terminal.get('constructorRole') in run.ORIGINAL_CONSTRUCTOR_LOG_POLICY['lateLifecycleRoles'] and terminal.get('constructorPredicate') in run.ORIGINAL_CONSTRUCTOR_LOG_POLICY['lateLifecycleExitPredicates']
 if terminal.get('constructorRole') not in run.ORIGINAL_CONSTRUCTOR_LOG_POLICY['roles']:return False
 return terminal.get('constructorStage')=='observe-registration' and terminal.get('constructorPredicate')=='done' or constructor_cached_wait_exit(terminal) is not None

def constructor_projection_empty(status,role):
 return dict(gateway_projection_empty(status),version=run.ORIGINAL_CONSTRUCTOR_LOG_POLICY['version'],role=role)

def constructor_private_file(path,maximum,allow_empty=False):
 path=Path(path)
 if path.resolve()!=path:raise ValueError('canonical original private file required')
 fd=os.open(path,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK|os.O_CLOEXEC)
 try:
  before=os.fstat(fd)
  if not stat.S_ISREG(before.st_mode) or before.st_uid!=os.getuid() or before.st_nlink!=1 or stat.S_IMODE(before.st_mode)!=0o600 or not (0 if allow_empty else 1)<=before.st_size<=maximum:raise ValueError('owned regular0600 bounded single-link original file required')
  raw=bytearray();left=maximum+1
  while left:
   part=os.read(fd,min(1<<20,left))
   if not part:break
   raw.extend(part);left-=len(part)
  after=os.fstat(fd);current=path.lstat();identity=lambda v:(v.st_dev,v.st_ino,v.st_mode,v.st_uid,v.st_gid,v.st_nlink,v.st_size,v.st_mtime_ns,v.st_ctime_ns)
  if identity(before)!=identity(after) or identity(before)!=identity(current) or len(raw)!=before.st_size or path.resolve()!=path:raise ValueError('original private file changed during bounded read')
  return bytes(raw)
 finally:os.close(fd)

def constructor_original_owner_nonce(work,role):
 directory=work/'runtime-owners';path=directory/(role+'-runtime-actual-owner.json')
 if directory.resolve()!=directory or path.resolve()!=path:raise ValueError('original owner record must be canonical')
 folder=directory.lstat();before=path.lstat()
 if not stat.S_ISDIR(folder.st_mode) or folder.st_uid!=os.getuid() or stat.S_IMODE(folder.st_mode)!=0o700 or not stat.S_ISREG(before.st_mode) or before.st_uid!=os.getuid() or before.st_nlink!=1 or stat.S_IMODE(before.st_mode)!=0o600:raise ValueError('original private owner record required')
 def unique(pairs):
  value={}
  for key,item in pairs:
   if key in value:raise ValueError('duplicate owner record field')
   value[key]=item
  return value
 raw=constructor_private_file(path,run.ORIGINAL_CONSTRUCTOR_LOG_POLICY['ownerRecordLimitBytes']);owner=json.loads(raw,object_pairs_hook=unique);after=path.lstat();again=directory.lstat();identity=lambda v:(v.st_dev,v.st_ino,v.st_mode,v.st_uid,v.st_nlink,v.st_size,v.st_mtime_ns,v.st_ctime_ns)
 keys={'role','pid','uid','startToken','bootID','executable','binarySHA256','sourceSHA256','buildReceiptSHA256','bootstrapSHA256','ownerNonce'}
 if identity(before)!=identity(after) or (folder.st_dev,folder.st_ino,folder.st_mode,folder.st_uid)!=(again.st_dev,again.st_ino,again.st_mode,again.st_uid) or not isinstance(owner,dict) or set(owner)!=keys or owner['role']!=role or owner['sourceSHA256']!=run.FINAL_SOURCE['digest'] or type(owner['pid']) is not int or owner['pid']<=1 or type(owner['uid']) is not int or owner['uid']!=os.getuid() or owner['executable']!=str(work.parent/'bin'/role):raise ValueError('original role/source/owner record differs')
 if any(not isinstance(owner[key],str) or not __import__('re').fullmatch('[0-9a-f]{64}',owner[key]) for key in ('binarySHA256','buildReceiptSHA256','bootstrapSHA256','ownerNonce')) or any(not isinstance(owner[key],str) or not owner[key] for key in ('startToken','bootID')):raise ValueError('finite original owner identity required')
 return owner['ownerNonce']

def constructor_output_boundary(terminal):
 done=terminal.get('constructorStage') in ('observe-registration','await-started')
 return {'failureBoundary':('await-started-original-child-exit' if terminal.get('constructorStage')=='await-started' else 'observe-registration-done') if done else 'startup-cached-wait-exit','originalWaitReturned':True,'originalWaitResult':'not-exported' if done else 'exit-code-'+str(constructor_cached_wait_exit(terminal)),'stdoutSyncAndCloseCallsReturned':True,'stdoutFinalizationResult':'not-exported','stdoutOverflowResult':'not-exported','originalOwnerRecordStatus':'matched' if done else 'not-required-for-startup','exitCodeInferredFromLog':False}

def constructor_error_prefix(payload,sites):
 stage,kind,frames,truncated=rpc_group_error_prefix(payload,sites)
 if kind=='other':
  for _ in range(run.ORIGINAL_CONSTRUCTOR_LOG_POLICY['wrapperLimit']):
   wrapper=next((prefix for prefix in (b'failed to get configuration from command: ',b'failed to initialize from config: ') if payload.startswith(prefix)),None)
   if wrapper is not None:payload=payload[len(wrapper):];continue
   fixed=gateway_fixed_wrapper(payload,sites)
   if fixed is None:break
   payload=fixed[0]
  fixed=((b'api url is empty','object-api-url'),(b'check bucket exists error: ','object-bucket-check'),(b'make bucket error: ','object-bucket-create'),(b'owned object-storage operation is unconfirmed','owned-object-lifetime'),(b'storage namespace requires operator repair','object-namespace-repair'),(b'owned RTC service user activation incomplete','rtc-user-activation'))
  kind=next((label for prefix,label in fixed if payload.startswith(prefix)),'other')
  if kind=='rtc-user-activation' and payload!=b'owned RTC service user activation incomplete':kind='other'
  if kind=='other':
   for code,label in {'OK': 'grpc-ok', 'Canceled': 'grpc-canceled', 'Unknown': 'grpc-unknown', 'InvalidArgument': 'grpc-invalid-argument', 'DeadlineExceeded': 'grpc-deadline-exceeded', 'NotFound': 'grpc-not-found', 'AlreadyExists': 'grpc-already-exists', 'PermissionDenied': 'grpc-permission-denied', 'ResourceExhausted': 'grpc-resource-exhausted', 'FailedPrecondition': 'grpc-failed-precondition', 'Aborted': 'grpc-aborted', 'OutOfRange': 'grpc-out-of-range', 'Unimplemented': 'grpc-unimplemented', 'Internal': 'grpc-internal', 'Unavailable': 'grpc-unavailable', 'DataLoss': 'grpc-data-loss', 'Unauthenticated': 'grpc-unauthenticated'}.items():
    if payload.startswith(b'rpc error: code = '+code.encode('ascii')+b' desc = '):kind=label;break

  if kind=='other':
   for phase in ['attempted', 'parent', 'callbacks', 'admitted', 'lookup', 'call-context', 'unknown']:
    if payload==b'owned RTC activation phase '+phase.encode('ascii'):kind='rtc-phase-'+phase;break
  if kind=='other':
   for cause in ['none', 'context-cancelled', 'context-deadline', 'namespace-evidence', 'sdk-user-missing', 'record-missing', 'permission', 'argument', 'internal', 'grpc-cancelled', 'grpc-unknown', 'grpc-invalid-argument', 'grpc-deadline', 'grpc-not-found', 'grpc-already-exists', 'grpc-permission', 'grpc-resource-exhausted', 'grpc-failed-precondition', 'grpc-aborted', 'grpc-out-of-range', 'grpc-unimplemented', 'grpc-internal', 'grpc-unavailable', 'grpc-data-loss', 'grpc-unauthenticated', 'other']:
    if payload==b'owned RTC activation cause '+cause.encode('ascii'):kind='rtc-cause-'+cause;break
 return stage,kind,frames,truncated

def project_original_constructor_log(work,terminal,bodies):
 if not original_constructor_output_failure(terminal):return None
 role=terminal['constructorRole'];policy=run.ORIGINAL_CONSTRUCTOR_LOG_POLICY;done=terminal['constructorStage'] in ('observe-registration','await-started');result=constructor_projection_empty('unavailable',role);result.update(source=run.FINAL_SOURCE,scenario=terminal['scenario'],**constructor_output_boundary(terminal))
 if done:result['originalOwnerRecordStatus']='not-observed'
 try:
  work=Path(work);directory=work/'services/required'/role
  if work.resolve()!=work or directory.resolve()!=directory:return dict(result,status='unsafe')
  folders=(work,work/'services',work/'services/required',directory);identities=[]
  for folder in folders:
   info=folder.lstat()
   if not stat.S_ISDIR(info.st_mode) or info.st_uid!=os.getuid() or stat.S_IMODE(info.st_mode)!=0o700:return dict(result,status='unsafe')
   identities.append((info.st_dev,info.st_ino,info.st_mode,info.st_uid))
  owner_nonce=None
  if done:
   try:owner_nonce=constructor_original_owner_nonce(work,role)
   except OSError:return dict(result,status='unavailable',originalOwnerRecordStatus='unavailable')
   except (ValueError,RuntimeError):return dict(result,status='unsafe',originalOwnerRecordStatus='invalid')
  matches=[]
  with os.scandir(directory) as entries:
   for count,entry in enumerate(entries):
    if count>=policy['directoryEntriesLimit']:return dict(result,status='unsafe')
    if __import__('re').fullmatch(r'service-[0-9a-f]{64}\.log',entry.name):matches.append(Path(entry.path))
  if len(matches)!=1:return dict(result,status='absent' if not matches else 'ambiguous')
  path=matches[0]
  if owner_nonce is not None and path.name!='service-'+owner_nonce+'.log':return dict(result,status='unsafe',originalOwnerRecordStatus='log-nonce-mismatch')
  raw=constructor_private_file(path,policy['logLimitBytes'])
  for folder,identity in zip(folders,identities):
   info=folder.lstat()
   if folder.resolve()!=folder or identity!=(info.st_dev,info.st_ino,info.st_mode,info.st_uid):return dict(result,status='unsafe')
  value=project_service_log_bytes(raw,bodies,policy,lambda status:constructor_projection_empty(status,role),constructor_error_prefix);value.update(source=run.FINAL_SOURCE,scenario=terminal['scenario'],**constructor_output_boundary(terminal));return value
 except RuntimeError:return dict(result,status='unsafe')
 except (OSError,ValueError,UnicodeError):return result

def cached_wait_unique_json(raw):
 def unique(pairs):
  value={}
  for key,item in pairs:
   if key in value:raise ValueError('duplicate private observation key')
   value[key]=item
  return value
 return json.loads(raw,object_pairs_hook=unique)

def checked_cached_wait_row(row):
 keys={'role','ownerState','waitState','waitErrorClass','processState','signal','stopIntent','joinRecorded'}
 if not isinstance(row,dict) or not keys<=set(row) or set(row)-keys-{'owner','exitCode'} or row['role'] not in run.ROLES or type(row['joinRecorded']) is not bool:raise ValueError('closed original-role cached Wait row required')
 if row['ownerState'] not in ('not-launched','owner-mismatch','matched'):raise ValueError('closed retained owner state required')
 if row['ownerState']!='matched':
  if set(row)!=keys or any(row[key]!='unobserved' for key in ('waitState','waitErrorClass','processState','signal','stopIntent')) or row['joinRecorded']:raise ValueError('missing or mismatched owner cannot expose a Wait result')
 else:
  if not isinstance(row.get('owner'),dict) or row['stopIntent'] not in ('not-attempted','attempted') or row['waitState'] not in ('pending','returned'):raise ValueError('exact private original owner and synchronized stop intent required')
  if row['waitState']=='pending':
   if 'exitCode' in row or any(row[key]!='unobserved' for key in ('waitErrorClass','processState','signal')) or row['joinRecorded']:raise ValueError('pending Wait cannot expose ProcessState')
  else:
   if row['waitErrorClass'] not in ('none','exit-error','exit-error-with-owned-evidence','owned-evidence','other') or row['processState'] not in ('absent','other','exited','signaled'):raise ValueError('closed original Wait and ProcessState required')
   if row['processState']=='exited':
    if type(row.get('exitCode')) is not int or not 0<=row['exitCode']<=255 or row['signal']!='none' or row['exitCode']!=0 and row['waitErrorClass'] not in ('exit-error','exit-error-with-owned-evidence'):raise ValueError('actual bounded exit outcome required')
   elif row['processState']=='signaled':
    if 'exitCode' in row or row['signal'] not in run.CACHED_WAIT_DIAGNOSTIC_POLICY['signals'] or row['waitErrorClass'] not in ('exit-error','exit-error-with-owned-evidence'):raise ValueError('closed original signal outcome required')
   elif 'exitCode' in row or row['signal']!='none':raise ValueError('absent or other ProcessState has no exit/signal claim')
 return {key:value for key,value in row.items() if key!='owner'}

def cached_wait_role_log(work,role,nonce,bodies):
 policy=run.ORIGINAL_CONSTRUCTOR_LOG_POLICY;result=constructor_projection_empty('unavailable',role);directory=work/'services/required'/role
 try:
  folders=(work,work/'services',work/'services/required',directory);identities=[]
  for folder in folders:
   info=folder.lstat()
   if folder.resolve()!=folder or not stat.S_ISDIR(info.st_mode) or info.st_uid!=os.getuid() or stat.S_IMODE(info.st_mode)!=0o700:return dict(result,status='unsafe')
   identities.append((info.st_dev,info.st_ino,info.st_mode,info.st_uid))
  matches=[]
  with os.scandir(directory) as entries:
   for count,entry in enumerate(entries):
    if count>=policy['directoryEntriesLimit']:return dict(result,status='unsafe')
    if __import__('re').fullmatch(r'service-[0-9a-f]{64}\.log',entry.name):matches.append(Path(entry.path))
  if len(matches)!=1:return dict(result,status='absent' if not matches else 'ambiguous')
  if matches[0].name!='service-'+nonce+'.log':return dict(result,status='unsafe')
  raw=constructor_private_file(matches[0],policy['logLimitBytes'],allow_empty=True)
  for folder,identity in zip(folders,identities):
   info=folder.lstat()
   if folder.resolve()!=folder or identity!=(info.st_dev,info.st_ino,info.st_mode,info.st_uid):return dict(result,status='unsafe')
  value=project_service_log_bytes(raw,bodies,policy,lambda status:constructor_projection_empty(status,role),constructor_error_prefix)
  value['managedExecReturnedErrorLineObserved']=role=='msgtransfer-managed' and any(line==b'managed ordinary startup or drain failed; inspect owned evidence' for line in raw.split(b'\n'))
  return value
 except RuntimeError:return dict(result,status='unsafe')
 except (OSError,ValueError,UnicodeError):return result

def project_cached_wait_snapshot(work,terminal,bodies):
 if not original_constructor_output_failure(terminal):return None
 result={'version':run.CACHED_WAIT_DIAGNOSTIC_POLICY['version'],'status':'unavailable','source':run.FINAL_SOURCE,'scenario':terminal['scenario'],'snapshotBoundary':'before-original-cleanup','logReadBoundary':'after-original-cleanup','physicalFirstFailureEstablished':False,'acceptanceEvidence':False,'runtimePIDReadyP20Accepted':False,'rawTextExported':False,'roles':[]}
 try:
  work=Path(work)
  if work.resolve()!=work:return dict(result,status='unsafe')
  info=work.lstat()
  if not stat.S_ISDIR(info.st_mode) or info.st_uid!=os.getuid() or stat.S_IMODE(info.st_mode)!=0o700:return dict(result,status='unsafe')
  value=cached_wait_unique_json(constructor_private_file(work/'original-runtime-wait-before-cleanup.json',run.CACHED_WAIT_DIAGNOSTIC_POLICY['snapshotLimitBytes']))
  if not isinstance(value,dict) or set(value)!={'version','boundary','scenario','sourceSHA256','roles','acceptanceEvidence'} or value['version']!='original-runtime-cached-wait-v1' or value['boundary']!='before-original-cleanup' or value['scenario']!=terminal['scenario'] or value['sourceSHA256']!=run.FINAL_SOURCE['digest'] or value['acceptanceEvidence'] is not False or not isinstance(value['roles'],list) or len(value['roles'])!=len(run.ROLES):raise ValueError('complete original precleanup snapshot required')
  if [row.get('role') for row in value['roles'] if isinstance(row,dict)]!=list(run.ROLES):raise ValueError('every fixed original requirement role exactly once required')
  rows=[]
  for row in value['roles']:
   public=checked_cached_wait_row(row)
   if row['ownerState']=='matched':
    role=row['role'];nonce=constructor_original_owner_nonce(work,role);owner=cached_wait_unique_json(constructor_private_file(work/'runtime-owners'/(role+'-runtime-actual-owner.json'),run.ORIGINAL_CONSTRUCTOR_LOG_POLICY['ownerRecordLimitBytes']))
    if owner!=row['owner'] or owner.get('ownerNonce')!=nonce:raise ValueError('cached original owner differs from original runtime owner record')
    public['originalOwnerRecordStatus']='matched'
    public['logProjection']=cached_wait_role_log(work,role,nonce,bodies)
   else:public['originalOwnerRecordStatus']='not-read';public['logProjection']=constructor_projection_empty('not-read',row['role'])
   rows.append(public)
  result.update(status='projected',roles=rows);return result
 except RuntimeError:return dict(result,status='unsafe')
 except (ValueError,UnicodeError):return dict(result,status='unsafe')
 except OSError:return result

def emit_cached_wait_diagnostic(out,work,terminal):
 if not original_constructor_output_failure(terminal):return False
 try:
  _,bodies,*_=run.verify();value=project_cached_wait_snapshot(work,terminal,bodies)
  if value is not None:run.write(out/('original-runtime-cached-wait-'+terminal['scenario']+'.json'),value)
  return value is not None
 except Exception:return False

def emit_original_constructor_diagnostic(out,work,terminal):
 if not original_constructor_output_failure(terminal):return False
 try:
  _,bodies,_,_,_,_=run.verify();value=project_original_constructor_log(work,terminal,bodies)
  if value is not None:run.write(out/('original-constructor-log-'+terminal['constructorRole']+'-'+terminal['scenario']+'.json'),value)
  return value is not None
 except Exception:return False

def checked_runtime_release_source_bridge_evidence(out):
 value=json.loads(run.regular(out/'runtime-release-historical-source-compatibility.json',1<<20));post=json.loads(run.regular(out/'source-post.json',4096));before=json.loads(run.regular(out/'tools-before.json',65536));after=json.loads(run.regular(out/'tools-after.json',65536))
 if post!={'accepted':True,'source':run.FINAL_SOURCE} or before!=after:raise ValueError('exact new source post and unchanged original tool bodies required')
 _,bodies,recipe,*_=run.verify();return run.check_runtime_release_source_bridge(value,bodies,recipe,before.get('go',{}))

def checked_rtc_activation_source_bridge_evidence(out):
 value=json.loads(run.regular(out/'rtc-activation-historical-source-compatibility.json',1<<20));post=json.loads(run.regular(out/'source-post.json',4096));before=json.loads(run.regular(out/'tools-before.json',65536));after=json.loads(run.regular(out/'tools-after.json',65536))
 if post!={'accepted':True,'source':run.FINAL_SOURCE} or before!=after:raise ValueError('new source post and unchanged current tool bodies required')
 _,bodies,recipe,*_=run.verify();return run.check_rtc_activation_source_bridge(value,bodies,recipe,before.get('go',{}))

def checked_current_rtc_activation_evidence(out):
 value=run.check_current_rtc_activation_receipt(json.loads(run.regular(out/'current-rtc-activation-phase-names.json',65536)));control=run.CURRENT_RTC_ACTIVATION_CONTROL;package=next(iter(control['targets']));prefix='github.com/openimsdk/open-im-server/v3/'
 events=json.loads(run.regular(out/'current-rtc-activation-phase-events.json',1<<20));expected={(row['package'],row['name']) for row in value['namedCompletions']}
 if not isinstance(events,list) or not events or any(not isinstance(row,dict) or set(row)-{'Time','Action','Package','Test','Elapsed'} or row.get('Package')!=prefix+package or row.get('Action') not in ('start','run','output','pass','pause','cont') for row in events):raise ValueError('finite original fixture contract events required')
 for action in ('run','pass'):
  actual=[(package,row['Test']) for row in events if row.get('Test') and row.get('Action')==action]
  if len(actual)!=4 or set(actual)!=expected:raise ValueError('fixture top and all three original child run/pass pairs required')
 if len([row for row in events if row.get('Action')=='start' and not row.get('Test')])!=1 or len([row for row in events if row.get('Action')=='pass' and not row.get('Test')])!=1:raise ValueError('one original fixture package start/footer required')
 phases=[]
 for path in sorted(out.glob('phase-*.json')):
  phase=json.loads(run.regular(path,1<<20))
  if phase.get('phase')==control['phase']:phases.append(phase)
 if len(phases)!=1:raise ValueError('one original fixture contract command required')
 phase=phases[0];resource=phase.get('resource',{})
 if type(phase.get('exitCode')) is not int or phase['exitCode']!=0 or resource.get('status')!='passed' or type(resource.get('exit_code')) is not int or resource['exit_code']!=0:raise ValueError('actual fresh fixture contract must succeed')
 checked_rtc_activation_source_bridge_evidence(out)
 if any((out/name).exists() for name in ('same-go1579-runtime-release-controls-reuse.json','current-runtime-release-context-names.json','current-runtime-release-context-events.json','runtime-release-historical-source-compatibility.json','same-go1578-group-and-named-scopes-reuse.json','current-group-registry-fixture-contract-names.json','group-registry-fixture-named-scope-reuse.json','group-registry-fixture-reused-scope-deps.json','same-source-current-go-named-scopes-reuse.json','cron-fixture-named-scope-reuse.json','cron-fixture-reused-scope-deps.json','current-cron-fixture-contract-names.json','completed-package-test-delta-reuse.json','current-cache-test-delta-controls-names.json','current-group-recovery-controls-names.json','current-managed-producer-control-names.json','same-source-completed-kafka-package-reuse.json','current-managed-shape-controls-names.json','current-release-controls-names.json','kafka-acl-passed-reuse.json','kafka-confirm-passed-reuse.json','current-kafka-confirm-names.json','current-image-kafka-names.json')):raise ValueError('new fixture-only branch must not alias prior controls or redispatch unaffected scopes')
 return value

def checked_current_runtime_release_passed_evidence(out):
 value=json.loads(run.regular(out/'same-go1579-runtime-release-controls-reuse.json',1<<20));post=json.loads(run.regular(out/'source-post.json',4096));before=json.loads(run.regular(out/'tools-before.json',65536));after=json.loads(run.regular(out/'tools-after.json',65536))
 if post!={'accepted':True,'source':run.FINAL_SOURCE} or before!=after:raise ValueError('same complete source post and unchanged current tools required')
 _,bodies,recipe,*_=run.verify();run.check_current_runtime_release_passed_reuse(value,bodies,recipe,before.get('go',{}))
 if any((out/name).exists() for name in ('current-runtime-release-context-names.json','current-runtime-release-context-events.json','runtime-release-historical-source-compatibility.json','same-go1578-group-and-named-scopes-reuse.json','current-group-registry-fixture-contract-names.json','group-registry-fixture-named-scope-reuse.json','group-registry-fixture-reused-scope-deps.json','same-source-current-go-named-scopes-reuse.json','cron-fixture-named-scope-reuse.json','current-cron-fixture-contract-names.json','cron-fixture-reused-scope-deps.json','completed-package-test-delta-reuse.json','current-cache-test-delta-controls-names.json','current-group-recovery-controls-names.json','current-managed-producer-control-names.json','same-source-completed-kafka-package-reuse.json','current-managed-shape-controls-names.json','current-release-controls-names.json','kafka-acl-passed-reuse.json','kafka-confirm-passed-reuse.json','current-kafka-confirm-names.json','current-image-kafka-names.json')):raise ValueError('passed original control cannot impersonate current fresh execution or alias historical modes')
 return value

def checked_current_runtime_release_evidence(out):
 value=run.check_current_runtime_release_receipt(json.loads(run.regular(out/'current-runtime-release-context-names.json',65536)));control=run.CURRENT_RUNTIME_RELEASE_CONTROL;package=next(iter(control['targets']));prefix='github.com/openimsdk/open-im-server/v3/'
 events=json.loads(run.regular(out/'current-runtime-release-context-events.json',1<<20));expected={(row['package'],row['name']) for row in value['namedCompletions']}
 if not isinstance(events,list) or not events or any(not isinstance(row,dict) or set(row)-{'Time','Action','Package','Test','Elapsed'} or row.get('Package')!=prefix+package or row.get('Action') not in ('start','run','output','pass','pause','cont') for row in events):raise ValueError('finite original fixture contract events required')
 for action in ('run','pass'):
  actual=[(package,row['Test']) for row in events if row.get('Test') and row.get('Action')==action]
  if len(actual)!=4 or set(actual)!=expected:raise ValueError('fixture top and all three original child run/pass pairs required')
 if len([row for row in events if row.get('Action')=='start' and not row.get('Test')])!=1 or len([row for row in events if row.get('Action')=='pass' and not row.get('Test')])!=1:raise ValueError('one original fixture package start/footer required')
 phases=[]
 for path in sorted(out.glob('phase-*.json')):
  phase=json.loads(run.regular(path,1<<20))
  if phase.get('phase')==control['phase']:phases.append(phase)
 if len(phases)!=1:raise ValueError('one original fixture contract command required')
 phase=phases[0];resource=phase.get('resource',{})
 if type(phase.get('exitCode')) is not int or phase['exitCode']!=0 or resource.get('status')!='passed' or type(resource.get('exit_code')) is not int or resource['exit_code']!=0:raise ValueError('actual fresh fixture contract must succeed')
 checked_runtime_release_source_bridge_evidence(out)
 if any((out/name).exists() for name in ('same-go1578-group-and-named-scopes-reuse.json','current-group-registry-fixture-contract-names.json','group-registry-fixture-named-scope-reuse.json','group-registry-fixture-reused-scope-deps.json','same-source-current-go-named-scopes-reuse.json','cron-fixture-named-scope-reuse.json','cron-fixture-reused-scope-deps.json','current-cron-fixture-contract-names.json','completed-package-test-delta-reuse.json','current-cache-test-delta-controls-names.json','current-group-recovery-controls-names.json','current-managed-producer-control-names.json','same-source-completed-kafka-package-reuse.json','current-managed-shape-controls-names.json','current-release-controls-names.json','kafka-acl-passed-reuse.json','kafka-confirm-passed-reuse.json','current-kafka-confirm-names.json','current-image-kafka-names.json')):raise ValueError('new fixture-only branch must not alias prior controls or redispatch unaffected scopes')
 return value

def checked_current_group_fixture_passed_evidence(out):
 value=json.loads(run.regular(out/'same-go1578-group-and-named-scopes-reuse.json',1<<20));post=json.loads(run.regular(out/'source-post.json',4096));before=json.loads(run.regular(out/'tools-before.json',65536));after=json.loads(run.regular(out/'tools-after.json',65536))
 if post!={'accepted':True,'source':run.FINAL_SOURCE} or before!=after:raise ValueError('same complete current source and unchanged original tool bodies required')
 _,bodies,recipe,*_=run.verify();run.check_current_group_fixture_passed_reuse(value,bodies,recipe,before.get('go',{}))
 if any((out/name).exists() for name in ('current-group-registry-fixture-contract-names.json','current-group-registry-fixture-contract-events.json','group-registry-fixture-named-scope-reuse.json','group-registry-fixture-reused-scope-deps.json','same-source-current-go-named-scopes-reuse.json','cron-fixture-named-scope-reuse.json','current-cron-fixture-contract-names.json','cron-fixture-reused-scope-deps.json','completed-package-test-delta-reuse.json','current-cache-test-delta-controls-names.json','current-group-recovery-controls-names.json','current-managed-producer-control-names.json','same-source-completed-kafka-package-reuse.json','current-managed-shape-controls-names.json','current-release-controls-names.json','kafka-acl-passed-reuse.json','kafka-confirm-passed-reuse.json','current-kafka-confirm-names.json','current-image-kafka-names.json')):raise ValueError('passed named scopes cannot masquerade as current fresh commands or alias an old mode')
 return value

def checked_current_go_named_scope_reuse_evidence(out):
 value=json.loads(run.regular(out/'same-source-current-go-named-scopes-reuse.json',65536));post=json.loads(run.regular(out/'source-post.json',4096));before=json.loads(run.regular(out/'tools-before.json',65536));after=json.loads(run.regular(out/'tools-after.json',65536))
 if post!={'accepted':True,'source':run.FINAL_SOURCE} or before!=after:raise ValueError('same current complete Go source and tool post required')
 _,bodies,recipe,*_=run.verify();run.check_current_go_named_scope_reuse(value,bodies,recipe,before.get('go',{}))
 if any((out/name).exists() for name in ('cron-fixture-named-scope-reuse.json','current-cron-fixture-contract-names.json','cron-fixture-reused-scope-deps.json','completed-package-test-delta-reuse.json','current-cache-test-delta-controls-names.json','current-group-recovery-controls-names.json','current-managed-producer-control-names.json','same-source-completed-kafka-package-reuse.json','current-managed-shape-controls-names.json','current-release-controls-names.json','kafka-acl-passed-reuse.json','kafka-confirm-passed-reuse.json','current-kafka-confirm-names.json','current-image-kafka-names.json')):raise ValueError('same-source facts cannot masquerade as fresh current invocations')
 return value

def checked_rtc_panic_runtime_evidence(out):
 value=run.check_rtc_panic_runtime_receipt(json.loads(run.regular(out/'rtc-panic-runtime-control.json',65536)));facts=json.loads(run.regular(out/'same-graph-go1579-rtc-control-facts.json',1<<20));post=json.loads(run.regular(out/'source-post.json',4096));before=json.loads(run.regular(out/'tools-before.json',65536));after=json.loads(run.regular(out/'tools-after.json',65536))
 if post!={'accepted':True,'source':run.FINAL_SOURCE} or before!=after:raise ValueError('current exact source/tools post required')
 _,bodies,recipe,*_=run.verify();run.check_rtc_activation_passed_facts(facts,bodies,recipe,before.get('go',{}))
 phases=[]
 for path in sorted(out.glob('phase-*.json')):
  item=json.loads(run.regular(path,1<<20))
  if item.get('phase') in run.RTC_PANIC_RUNTIME_CONTROL['phases']:phases.append(item)
 if [item['phase'] for item in phases]!=run.RTC_PANIC_RUNTIME_CONTROL['phases'] or any(type(item.get('exitCode')) is not int or item['exitCode']!=0 or item.get('resource',{}).get('status')!='passed' or type(item.get('resource',{}).get('exit_code')) is not int or item['resource']['exit_code']!=0 for item in phases):raise ValueError('one original guarded build and wrapper-success/exact-probe-exit2 execution required')
 if any((out/name).exists() for name in ('current-rtc-activation-phase-names.json','current-rtc-activation-phase-events.json','rtc-activation-historical-source-compatibility.json','same-go1579-runtime-release-controls-reuse.json','current-runtime-release-context-names.json','same-go1578-group-and-named-scopes-reuse.json','current-group-registry-fixture-contract-names.json','current-cron-fixture-contract-names.json','current-cache-test-delta-controls-names.json','current-group-recovery-controls-names.json','current-managed-producer-control-names.json','current-release-controls-names.json','current-image-kafka-names.json')):raise ValueError('actual panic probe cannot alias fresh prior named controls')
 return value

def checked_cached_wait_source_bridge_evidence(out):
 value=json.loads(run.regular(out/'cached-wait-historical-source-compatibility.json',1<<20));post=json.loads(run.regular(out/'source-post.json',4096));before=json.loads(run.regular(out/'tools-before.json',65536));after=json.loads(run.regular(out/'tools-after.json',65536))
 if post!={'accepted':True,'source':run.FINAL_SOURCE} or before!=after:raise ValueError('new source post and unchanged current tool bodies required')
 _,bodies,recipe,*_=run.verify();return run.check_cached_wait_source_bridge(value,bodies,recipe,before.get('go',{}))

def checked_cached_wait_control_evidence(out):
 value=run.check_cached_wait_receipt(json.loads(run.regular(out/'current-cached-wait-observation-names.json',65536)));control=run.CURRENT_CACHED_WAIT_CONTROL;package=next(iter(control['targets']));prefix='github.com/openimsdk/open-im-server/v3/'
 events=json.loads(run.regular(out/'current-cached-wait-observation-events.json',1<<20));expected={(row['package'],row['name']) for row in value['namedCompletions']}
 if not isinstance(events,list) or not events or any(not isinstance(row,dict) or set(row)-{'Time','Action','Package','Test','Elapsed'} or row.get('Package')!=prefix+package or row.get('Action') not in ('start','run','output','pass','pause','cont') for row in events):raise ValueError('finite original fixture contract events required')
 for action in ('run','pass'):
  actual=[(package,row['Test']) for row in events if row.get('Test') and row.get('Action')==action]
  if len(actual)!=3 or set(actual)!=expected:raise ValueError('fixture top and all three original child run/pass pairs required')
 if len([row for row in events if row.get('Action')=='start' and not row.get('Test')])!=1 or len([row for row in events if row.get('Action')=='pass' and not row.get('Test')])!=1:raise ValueError('one original fixture package start/footer required')
 phases=[]
 for path in sorted(out.glob('phase-*.json')):
  phase=json.loads(run.regular(path,1<<20))
  if phase.get('phase')==control['phase']:phases.append(phase)
 if len(phases)!=1:raise ValueError('one original fixture contract command required')
 phase=phases[0];resource=phase.get('resource',{})
 if type(phase.get('exitCode')) is not int or phase['exitCode']!=0 or resource.get('status')!='passed' or type(resource.get('exit_code')) is not int or resource['exit_code']!=0:raise ValueError('actual fresh fixture contract must succeed')
 checked_cached_wait_source_bridge_evidence(out)
 if any((out/name).exists() for name in ('rtc-panic-runtime-control.json','rtc-activation-historical-source-compatibility.json','same-graph-go1579-rtc-control-facts.json','current-rtc-activation-phase-names.json','current-rtc-activation-phase-events.json','same-go1579-runtime-release-controls-reuse.json','current-runtime-release-context-names.json','current-runtime-release-context-events.json','runtime-release-historical-source-compatibility.json','same-go1578-group-and-named-scopes-reuse.json','current-group-registry-fixture-contract-names.json','group-registry-fixture-named-scope-reuse.json','group-registry-fixture-reused-scope-deps.json','same-source-current-go-named-scopes-reuse.json','cron-fixture-named-scope-reuse.json','cron-fixture-reused-scope-deps.json','current-cron-fixture-contract-names.json','completed-package-test-delta-reuse.json','current-cache-test-delta-controls-names.json','current-group-recovery-controls-names.json','current-managed-producer-control-names.json','same-source-completed-kafka-package-reuse.json','current-managed-shape-controls-names.json','current-release-controls-names.json','kafka-acl-passed-reuse.json','kafka-confirm-passed-reuse.json','current-kafka-confirm-names.json','current-image-kafka-names.json')):raise ValueError('new fixture-only branch must not alias prior controls or redispatch unaffected scopes')
 return value

def checked_owned_resolver_source_bridge_evidence(out):
 value=json.loads(run.regular(out/'owned-resolver-historical-source-compatibility.json',1<<20));post=json.loads(run.regular(out/'source-post.json',4096));before=json.loads(run.regular(out/'tools-before.json',65536));after=json.loads(run.regular(out/'tools-after.json',65536))
 if post!={'accepted':True,'source':run.FINAL_SOURCE} or before!=after:raise ValueError('new source post and unchanged current tool bodies required')
 _,bodies,recipe,*_=run.verify();return run.check_owned_resolver_source_bridge(value,bodies,recipe,before.get('go',{}))

def checked_owned_resolver_scheme_evidence(out):
 value=run.check_owned_resolver_scheme_receipt(json.loads(run.regular(out/'current-owned-resolver-scheme-names.json',65536)));control=run.CURRENT_OWNED_RESOLVER_SCHEME_CONTROL;package=next(iter(control['targets']));prefix='github.com/openimsdk/open-im-server/v3/'
 events=json.loads(run.regular(out/'current-owned-resolver-scheme-events.json',1<<20));expected={(row['package'],row['name']) for row in value['namedCompletions']}
 if not isinstance(events,list) or not events or any(not isinstance(row,dict) or set(row)-{'Time','Action','Package','Test','Elapsed'} or row.get('Package')!=prefix+package or row.get('Action') not in ('start','run','output','pass','pause','cont') for row in events):raise ValueError('finite original fixture contract events required')
 for action in ('run','pass'):
  actual=[(package,row['Test']) for row in events if row.get('Test') and row.get('Action')==action]
  if len(actual)!=3 or set(actual)!=expected:raise ValueError('owned resolver top and two children run/pass pairs required')
 if len([row for row in events if row.get('Action')=='start' and not row.get('Test')])!=1 or len([row for row in events if row.get('Action')=='pass' and not row.get('Test')])!=1:raise ValueError('one original fixture package start/footer required')
 phases=[]
 for path in sorted(out.glob('phase-*.json')):
  phase=json.loads(run.regular(path,1<<20))
  if phase.get('phase')==control['phase']:phases.append(phase)
 if len(phases)!=1:raise ValueError('one original fixture contract command required')
 phase=phases[0];resource=phase.get('resource',{})
 if type(phase.get('exitCode')) is not int or phase['exitCode']!=0 or resource.get('status')!='passed' or type(resource.get('exit_code')) is not int or resource['exit_code']!=0:raise ValueError('actual fresh fixture contract must succeed')
 checked_owned_resolver_source_bridge_evidence(out)
 if any((out/name).exists() for name in ('current-cached-wait-observation-names.json','current-cached-wait-observation-events.json','cached-wait-historical-source-compatibility.json','rtc-panic-runtime-control.json','rtc-activation-historical-source-compatibility.json','same-graph-go1579-rtc-control-facts.json','current-rtc-activation-phase-names.json','current-rtc-activation-phase-events.json','same-go1579-runtime-release-controls-reuse.json','current-runtime-release-context-names.json','current-runtime-release-context-events.json','runtime-release-historical-source-compatibility.json','same-go1578-group-and-named-scopes-reuse.json','current-group-registry-fixture-contract-names.json','group-registry-fixture-named-scope-reuse.json','group-registry-fixture-reused-scope-deps.json','same-source-current-go-named-scopes-reuse.json','cron-fixture-named-scope-reuse.json','cron-fixture-reused-scope-deps.json','current-cron-fixture-contract-names.json','completed-package-test-delta-reuse.json','current-cache-test-delta-controls-names.json','current-group-recovery-controls-names.json','current-managed-producer-control-names.json','same-source-completed-kafka-package-reuse.json','current-managed-shape-controls-names.json','current-release-controls-names.json','kafka-acl-passed-reuse.json','kafka-confirm-passed-reuse.json','current-kafka-confirm-names.json','current-image-kafka-names.json')):raise ValueError('new fixture-only branch must not alias prior controls or redispatch unaffected scopes')
 return value

def checked_redis_null_source_bridge_evidence(out):
 value=json.loads(run.regular(out/'redis-null-historical-source-compatibility.json',1<<20));post=json.loads(run.regular(out/'source-post.json',4096));before=json.loads(run.regular(out/'tools-before.json',65536));after=json.loads(run.regular(out/'tools-after.json',65536))
 if post!={'accepted':True,'source':run.FINAL_SOURCE} or before!=after:raise ValueError('new source post and unchanged current tool bodies required')
 _,bodies,recipe,*_=run.verify();return run.check_redis_null_source_bridge(value,bodies,recipe,before.get('go',{}))

def checked_redis_null_ack_evidence(out):
 value=run.check_redis_null_ack_receipt(json.loads(run.regular(out/'current-owned-redis-null-ack-names.json',65536)));control=run.CURRENT_REDIS_NULL_ACK_CONTROL;package=next(iter(control['targets']));prefix='github.com/openimsdk/open-im-server/v3/'
 events=json.loads(run.regular(out/'current-owned-redis-null-ack-events.json',1<<20));expected={(row['package'],row['name']) for row in value['namedCompletions']}
 if not isinstance(events,list) or not events or any(not isinstance(row,dict) or set(row)-{'Time','Action','Package','Test','Elapsed'} or row.get('Package')!=prefix+package or row.get('Action') not in ('start','run','output','pass','pause','cont') for row in events):raise ValueError('finite original fixture contract events required')
 for action in ('run','pass'):
  actual=[(package,row['Test']) for row in events if row.get('Test') and row.get('Action')==action]
  if len(actual)!=4 or set(actual)!=expected:raise ValueError('Redis Null ACK top and three children run/pass pairs required')
 if len([row for row in events if row.get('Action')=='start' and not row.get('Test')])!=1 or len([row for row in events if row.get('Action')=='pass' and not row.get('Test')])!=1:raise ValueError('one original fixture package start/footer required')
 phases=[]
 for path in sorted(out.glob('phase-*.json')):
  phase=json.loads(run.regular(path,1<<20))
  if phase.get('phase')==control['phase']:phases.append(phase)
 if len(phases)!=1:raise ValueError('one original fixture contract command required')
 phase=phases[0];resource=phase.get('resource',{})
 if type(phase.get('exitCode')) is not int or phase['exitCode']!=0 or resource.get('status')!='passed' or type(resource.get('exit_code')) is not int or resource['exit_code']!=0:raise ValueError('actual fresh fixture contract must succeed')
 checked_redis_null_source_bridge_evidence(out)
 if any((out/name).exists() for name in ('current-owned-resolver-scheme-names.json','current-owned-resolver-scheme-events.json','owned-resolver-historical-source-compatibility.json','current-cached-wait-observation-names.json','current-cached-wait-observation-events.json','cached-wait-historical-source-compatibility.json','rtc-panic-runtime-control.json','rtc-activation-historical-source-compatibility.json','same-graph-go1579-rtc-control-facts.json','current-rtc-activation-phase-names.json','current-rtc-activation-phase-events.json','same-go1579-runtime-release-controls-reuse.json','current-runtime-release-context-names.json','current-runtime-release-context-events.json','runtime-release-historical-source-compatibility.json','same-go1578-group-and-named-scopes-reuse.json','current-group-registry-fixture-contract-names.json','group-registry-fixture-named-scope-reuse.json','group-registry-fixture-reused-scope-deps.json','same-source-current-go-named-scopes-reuse.json','cron-fixture-named-scope-reuse.json','cron-fixture-reused-scope-deps.json','current-cron-fixture-contract-names.json','completed-package-test-delta-reuse.json','current-cache-test-delta-controls-names.json','current-group-recovery-controls-names.json','current-managed-producer-control-names.json','same-source-completed-kafka-package-reuse.json','current-managed-shape-controls-names.json','current-release-controls-names.json','kafka-acl-passed-reuse.json','kafka-confirm-passed-reuse.json','current-kafka-confirm-names.json','current-image-kafka-names.json')):raise ValueError('new fixture-only branch must not alias prior controls or redispatch unaffected scopes')
 return value

def collect():
 allocation=run.allocation_root();out=run.REPO/'ci-results/backend-fullgraph';out.mkdir(mode=0o700,parents=True,exist_ok=True)
 if not allocation.exists():
  run.write(out/'collection.json',{'started':False,'accepted':False,'reason':'no-original-allocation','privateDataUploaded':False});pack(out);return 0
 if allocation.resolve()!=allocation or allocation.stat().st_uid!=os.getuid() or stat.S_IMODE(allocation.stat().st_mode)!=0o700 or json.loads(run.regular(allocation/'allocation-owner.json'))!=run.job_identity():raise ValueError('original job allocation required')
 c=json.loads(run.regular(run.INPUTS,1<<20));run.check_locked(c);fresh_required=c['kafkaACLTestsToRun']!=[];confirm_required=c.get('currentKafkaConfirmControl') is not None;confirm_reuse_required=c.get('kafkaConfirmPassedReuse') is not None;release_required=c.get('currentReleaseControl') is not None;managed_required=c.get('currentManagedShapeControl') is not None;producer_required=c.get('currentProducerControl') is not None;group_recovery_required=c.get('currentGroupRecoveryControl') is not None;cache_test_delta_required=c.get('currentCacheTestDeltaControl') is not None;cron_fixture_required=c.get('currentCronFixtureControl') is not None;named_scope_reuse_required=run.current_go_named_scope_reuse_selected(c);group_registry_fixture_required=run.current_group_registry_fixture_selected(c);group_fixture_passed_required=c.get('passedGroupRegistryAndNamedScopes') is not None;runtime_release_required=c.get('currentRuntimeReleaseControl') is not None;runtime_release_passed_required=c.get('passedRuntimeReleaseControl') is not None;rtc_activation_required=c.get('currentRTCActivationControl') is not None;rtc_probe_required=c.get('rtcPanicRuntimeProbe') is not None;cached_wait_required=c.get('currentCachedWaitControl') is not None;owned_resolver_required=c.get('currentOwnedResolverSchemeControl') is not None;redis_null_required=c.get('currentRedisNullAckControl') is not None;redis_null_passed_required=c.get('passedRedisNullAckControl') is not None;raw_push_required=c.get('currentRawPushContract') is not None
 root=allocation/'work';resources=run.module('ci/backend-fullgraph/resources.py','always_original_plane');rows=[];failed=False;terminals={}
 for scenario in run.SCENARIOS:
  folder=root/('plane-'+scenario);work=root/('scenario-'+scenario);entry={'scenario':scenario,'resourceStarted':False,'runtimeAttempted':False,'runtimeCleanupAccepted':False}
  # Merely having a partial folder does not authorize a cleanup. Only the
  # original durable owner record can reconstruct its exact attempted IDs.
  if (folder/'private/owner.json').exists():
   entry['resourceStarted']=True
   try:entry['resourceCleanup']=run.public_cleanup(resources.Plane.reopen(folder).cleanup())
   except Exception as error:entry['resourceCleanup']={'accepted':False,'exceptionType':type(error).__name__};failed=True
  preparation=folder/'private'/('prepare-observation-'+scenario+'.json')
  if preparation.exists():
   try:
    entry['resourcePreparation']=public_resource_preparation(run.regular(preparation,4096),scenario)
    run.write(out/('resource-prepare-'+scenario+'.json'),entry['resourcePreparation'])
    if entry['resourcePreparation']['errorClass']!='none':failed=True
   except Exception as error:entry['resourcePreparationExceptionType']=type(error).__name__;failed=True
  if (work/'result.json').exists():
   try:
    terminal=public_terminal(run.regular(work/'result.json',1<<20),scenario);terminals[scenario]=terminal
    run.write(out/('terminal-'+scenario+'.json'),terminal)
    if original_constructor_output_failure(terminal):
     emit_original_constructor_diagnostic(out,work,terminal)
     emit_cached_wait_diagnostic(out,work,terminal)
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
 raw_push_accepted=False
 if (out/'current-required-push-request-names.json').exists():
  try:checked_raw_push_evidence(out);raw_push_accepted=True
  except Exception:failed=True
 redis_null_passed_accepted=False
 if (out/'same-go-null-named-scope-reuse.json').exists():
  try:checked_redis_null_passed_evidence(out);redis_null_passed_accepted=True
  except Exception:failed=True
 redis_null_accepted=False
 if (out/'current-owned-redis-null-ack-names.json').exists():
  try:checked_redis_null_ack_evidence(out);redis_null_accepted=True
  except Exception:failed=True
 owned_resolver_accepted=False
 if (out/'current-owned-resolver-scheme-names.json').exists():
  try:checked_owned_resolver_scheme_evidence(out);owned_resolver_accepted=True
  except Exception:failed=True
 cached_wait_accepted=False
 if (out/'current-cached-wait-observation-names.json').exists():
  try:checked_cached_wait_control_evidence(out);cached_wait_accepted=True
  except Exception:failed=True
 rtc_probe_accepted=False
 if (out/'rtc-panic-runtime-control.json').exists():
  try:checked_rtc_panic_runtime_evidence(out);rtc_probe_accepted=True
  except Exception:failed=True
 rtc_activation_accepted=False
 if (out/'current-rtc-activation-phase-names.json').exists():
  try:checked_current_rtc_activation_evidence(out);rtc_activation_accepted=True
  except Exception:failed=True
 runtime_release_passed_accepted=False
 if (out/'same-go1579-runtime-release-controls-reuse.json').exists():
  try:checked_current_runtime_release_passed_evidence(out);runtime_release_passed_accepted=True
  except Exception:failed=True
 runtime_release_accepted=False
 if (out/'current-runtime-release-context-names.json').exists():
  try:checked_current_runtime_release_evidence(out);runtime_release_accepted=True
  except Exception:failed=True
 group_fixture_passed_accepted=False
 if (out/'same-go1578-group-and-named-scopes-reuse.json').exists():
  try:checked_current_group_fixture_passed_evidence(out);group_fixture_passed_accepted=True
  except Exception:failed=True
 group_registry_fixture_reuse_accepted=False
 if (out/'group-registry-fixture-named-scope-reuse.json').exists():
  try:checked_group_registry_fixture_scope_reuse_evidence(out);group_registry_fixture_reuse_accepted=True
  except Exception:failed=True
 current_group_registry_fixture_accepted=False
 if (out/'current-group-registry-fixture-contract-names.json').exists():
  try:checked_current_group_registry_fixture_evidence(out);current_group_registry_fixture_accepted=True
  except Exception:failed=True
 current_named_scope_reuse_accepted=False
 if (out/'same-source-current-go-named-scopes-reuse.json').exists():
  try:checked_current_go_named_scope_reuse_evidence(out);current_named_scope_reuse_accepted=True
  except Exception:failed=True
 cron_fixture_reuse_accepted=False
 if (out/'cron-fixture-named-scope-reuse.json').exists():
  try:checked_cron_fixture_scope_reuse_evidence(out);cron_fixture_reuse_accepted=True
  except Exception:failed=True
 current_cron_fixture_accepted=False
 if (out/'current-cron-fixture-contract-names.json').exists():
  try:checked_current_cron_fixture_evidence(out);current_cron_fixture_accepted=True
  except Exception:failed=True
 cache_test_delta_reuse_accepted=False
 if (out/'completed-package-test-delta-reuse.json').exists():
  try:checked_cache_test_delta_reuse_evidence(out);cache_test_delta_reuse_accepted=True
  except Exception:failed=True
 current_cache_test_delta_accepted=False
 if (out/'current-cache-test-delta-controls-names.json').exists():
  try:checked_current_cache_test_delta_evidence(out);current_cache_test_delta_accepted=True
  except Exception:failed=True
 current_group_recovery_accepted=False
 if (out/'current-group-recovery-controls-names.json').exists():
  try:checked_current_group_recovery_evidence(out);current_group_recovery_accepted=True
  except Exception:failed=True
 current_image_kafka_accepted=False
 if (out/'current-image-kafka-names.json').exists():
  try:checked_current_image_kafka_evidence(out);current_image_kafka_accepted=True
  except Exception:failed=True
 completed_kafka_reuse_accepted=False
 if (out/'same-source-completed-kafka-package-reuse.json').exists():
  try:checked_completed_kafka_package_reuse_evidence(out);completed_kafka_reuse_accepted=True
  except Exception:failed=True
 current_producer_accepted=False
 if (out/'current-managed-producer-control-names.json').exists():
  try:checked_current_producer_evidence(out);current_producer_accepted=True
  except Exception:failed=True
 current_managed_shape_accepted=False
 if (out/'current-managed-shape-controls-names.json').exists():
  try:checked_current_managed_shape_evidence(out);current_managed_shape_accepted=True
  except Exception:failed=True
 current_release_accepted=False
 if (out/'current-release-controls-names.json').exists():
  try:checked_current_release_evidence(out);current_release_accepted=True
  except Exception:failed=True
 current_kafka_confirm_accepted=False
 if (out/'current-kafka-confirm-names.json').exists():
  try:checked_current_kafka_confirm_evidence(out);current_kafka_confirm_accepted=True
  except Exception:failed=True
 current_kafka_confirm_reuse_accepted=False
 if (out/'kafka-confirm-passed-reuse.json').exists():
  try:checked_kafka_confirm_reuse_evidence(out);current_kafka_confirm_reuse_accepted=True
  except Exception:failed=True
 kafka_reuse_accepted=False
 if (out/'kafka-acl-passed-reuse.json').exists():
  try:checked_kafka_acl_evidence(out);kafka_reuse_accepted=True
  except Exception:failed=True
 minio=root/'minio-source-build/source-post.json'
 if minio.exists():run.write(out/'source-minio-post.json',json.loads(run.regular(minio,65536)))
 controller=run.REPO/'ci-results/backend-fullgraph-controller.resource.json'
 monitor=json.loads(run.regular(controller)) if controller.exists() else {}
 summary=out/'summary.json';main=json.loads(run.regular(summary)) if summary.exists() else {}
 launcher_accepted=False
 try:
  checked_launcher_evidence(out);launcher_accepted=True
 except Exception:failed=True
 if raw_push_required:kafka_current_accepted=raw_push_accepted and not redis_null_passed_accepted and not redis_null_accepted and not owned_resolver_accepted and not cached_wait_accepted and not rtc_probe_accepted and not rtc_activation_accepted and not any((runtime_release_passed_accepted,runtime_release_accepted,group_fixture_passed_accepted,current_group_registry_fixture_accepted,group_registry_fixture_reuse_accepted,current_named_scope_reuse_accepted,current_cron_fixture_accepted,cron_fixture_reuse_accepted,current_cache_test_delta_accepted,cache_test_delta_reuse_accepted,current_group_recovery_accepted,current_producer_accepted,completed_kafka_reuse_accepted,current_managed_shape_accepted,current_release_accepted,current_kafka_confirm_reuse_accepted,current_kafka_confirm_accepted,current_image_kafka_accepted,kafka_reuse_accepted))
 elif redis_null_passed_required:kafka_current_accepted=redis_null_passed_accepted and not redis_null_accepted and not owned_resolver_accepted and not cached_wait_accepted and not rtc_probe_accepted and not rtc_activation_accepted and not any((runtime_release_passed_accepted,runtime_release_accepted,group_fixture_passed_accepted,current_group_registry_fixture_accepted,group_registry_fixture_reuse_accepted,current_named_scope_reuse_accepted,current_cron_fixture_accepted,cron_fixture_reuse_accepted,current_cache_test_delta_accepted,cache_test_delta_reuse_accepted,current_group_recovery_accepted,current_producer_accepted,completed_kafka_reuse_accepted,current_managed_shape_accepted,current_release_accepted,current_kafka_confirm_reuse_accepted,current_kafka_confirm_accepted,current_image_kafka_accepted,kafka_reuse_accepted))
 elif redis_null_required:kafka_current_accepted=redis_null_accepted and not owned_resolver_accepted and not cached_wait_accepted and not rtc_probe_accepted and not rtc_activation_accepted and not any((runtime_release_passed_accepted,runtime_release_accepted,group_fixture_passed_accepted,current_group_registry_fixture_accepted,group_registry_fixture_reuse_accepted,current_named_scope_reuse_accepted,current_cron_fixture_accepted,cron_fixture_reuse_accepted,current_cache_test_delta_accepted,cache_test_delta_reuse_accepted,current_group_recovery_accepted,current_producer_accepted,completed_kafka_reuse_accepted,current_managed_shape_accepted,current_release_accepted,current_kafka_confirm_reuse_accepted,current_kafka_confirm_accepted,current_image_kafka_accepted,kafka_reuse_accepted))
 elif owned_resolver_required:kafka_current_accepted=owned_resolver_accepted and not cached_wait_accepted and not rtc_probe_accepted and not rtc_activation_accepted and not any((runtime_release_passed_accepted,runtime_release_accepted,group_fixture_passed_accepted,current_group_registry_fixture_accepted,group_registry_fixture_reuse_accepted,current_named_scope_reuse_accepted,current_cron_fixture_accepted,cron_fixture_reuse_accepted,current_cache_test_delta_accepted,cache_test_delta_reuse_accepted,current_group_recovery_accepted,current_producer_accepted,completed_kafka_reuse_accepted,current_managed_shape_accepted,current_release_accepted,current_kafka_confirm_reuse_accepted,current_kafka_confirm_accepted,current_image_kafka_accepted,kafka_reuse_accepted))
 elif cached_wait_required:kafka_current_accepted=cached_wait_accepted and not rtc_probe_accepted and not rtc_activation_accepted and not any((runtime_release_passed_accepted,runtime_release_accepted,group_fixture_passed_accepted,current_group_registry_fixture_accepted,group_registry_fixture_reuse_accepted,current_named_scope_reuse_accepted,current_cron_fixture_accepted,cron_fixture_reuse_accepted,current_cache_test_delta_accepted,cache_test_delta_reuse_accepted,current_group_recovery_accepted,current_producer_accepted,completed_kafka_reuse_accepted,current_managed_shape_accepted,current_release_accepted,current_kafka_confirm_reuse_accepted,current_kafka_confirm_accepted,current_image_kafka_accepted,kafka_reuse_accepted))
 elif rtc_probe_required:kafka_current_accepted=rtc_probe_accepted and not rtc_activation_accepted and not any((runtime_release_passed_accepted,runtime_release_accepted,group_fixture_passed_accepted,current_group_registry_fixture_accepted,group_registry_fixture_reuse_accepted,current_named_scope_reuse_accepted,current_cron_fixture_accepted,cron_fixture_reuse_accepted,current_cache_test_delta_accepted,cache_test_delta_reuse_accepted,current_group_recovery_accepted,current_producer_accepted,completed_kafka_reuse_accepted,current_managed_shape_accepted,current_release_accepted,current_kafka_confirm_reuse_accepted,current_kafka_confirm_accepted,current_image_kafka_accepted,kafka_reuse_accepted))
 elif rtc_activation_required:kafka_current_accepted=rtc_activation_accepted and not any((runtime_release_passed_accepted,runtime_release_accepted,group_fixture_passed_accepted,current_group_registry_fixture_accepted,group_registry_fixture_reuse_accepted,current_named_scope_reuse_accepted,current_cron_fixture_accepted,cron_fixture_reuse_accepted,current_cache_test_delta_accepted,cache_test_delta_reuse_accepted,current_group_recovery_accepted,current_producer_accepted,completed_kafka_reuse_accepted,current_managed_shape_accepted,current_release_accepted,current_kafka_confirm_reuse_accepted,current_kafka_confirm_accepted,current_image_kafka_accepted,kafka_reuse_accepted))
 elif runtime_release_passed_required:kafka_current_accepted=runtime_release_passed_accepted and not any((runtime_release_accepted,group_fixture_passed_accepted,current_group_registry_fixture_accepted,group_registry_fixture_reuse_accepted,current_named_scope_reuse_accepted,current_cron_fixture_accepted,cron_fixture_reuse_accepted,current_cache_test_delta_accepted,cache_test_delta_reuse_accepted,current_group_recovery_accepted,current_producer_accepted,completed_kafka_reuse_accepted,current_managed_shape_accepted,current_release_accepted,current_kafka_confirm_reuse_accepted,current_kafka_confirm_accepted,current_image_kafka_accepted,kafka_reuse_accepted))
 elif runtime_release_required:kafka_current_accepted=runtime_release_accepted and not any((group_fixture_passed_accepted,current_group_registry_fixture_accepted,group_registry_fixture_reuse_accepted,current_named_scope_reuse_accepted,current_cron_fixture_accepted,cron_fixture_reuse_accepted,current_cache_test_delta_accepted,cache_test_delta_reuse_accepted,current_group_recovery_accepted,current_producer_accepted,completed_kafka_reuse_accepted,current_managed_shape_accepted,current_release_accepted,current_kafka_confirm_reuse_accepted,current_kafka_confirm_accepted,current_image_kafka_accepted,kafka_reuse_accepted))
 elif group_fixture_passed_required:kafka_current_accepted=group_fixture_passed_accepted and not any((current_group_registry_fixture_accepted,group_registry_fixture_reuse_accepted,current_named_scope_reuse_accepted,current_cron_fixture_accepted,cron_fixture_reuse_accepted,current_cache_test_delta_accepted,cache_test_delta_reuse_accepted,current_group_recovery_accepted,current_producer_accepted,completed_kafka_reuse_accepted,current_managed_shape_accepted,current_release_accepted,current_kafka_confirm_reuse_accepted,current_kafka_confirm_accepted,current_image_kafka_accepted,kafka_reuse_accepted))
 elif group_registry_fixture_required:kafka_current_accepted=current_group_registry_fixture_accepted and group_registry_fixture_reuse_accepted and not any((current_named_scope_reuse_accepted,current_cron_fixture_accepted,cron_fixture_reuse_accepted,current_cache_test_delta_accepted,cache_test_delta_reuse_accepted,current_group_recovery_accepted,current_producer_accepted,completed_kafka_reuse_accepted,current_managed_shape_accepted,current_release_accepted,current_kafka_confirm_reuse_accepted,current_kafka_confirm_accepted,current_image_kafka_accepted,kafka_reuse_accepted))
 elif named_scope_reuse_required:kafka_current_accepted=current_named_scope_reuse_accepted and not any((current_cron_fixture_accepted,cron_fixture_reuse_accepted,current_cache_test_delta_accepted,cache_test_delta_reuse_accepted,current_group_recovery_accepted,current_producer_accepted,completed_kafka_reuse_accepted,current_managed_shape_accepted,current_release_accepted,current_kafka_confirm_reuse_accepted,current_kafka_confirm_accepted,current_image_kafka_accepted,kafka_reuse_accepted))
 elif cron_fixture_required:kafka_current_accepted=current_cron_fixture_accepted and cron_fixture_reuse_accepted and not any((current_cache_test_delta_accepted,cache_test_delta_reuse_accepted,current_group_recovery_accepted,current_producer_accepted,completed_kafka_reuse_accepted,current_managed_shape_accepted,current_release_accepted,current_kafka_confirm_reuse_accepted,current_kafka_confirm_accepted,current_image_kafka_accepted,kafka_reuse_accepted))
 elif cache_test_delta_required:kafka_current_accepted=current_cache_test_delta_accepted and cache_test_delta_reuse_accepted and not any((current_group_recovery_accepted,current_producer_accepted,completed_kafka_reuse_accepted,current_managed_shape_accepted,current_release_accepted,current_kafka_confirm_reuse_accepted,current_kafka_confirm_accepted,current_image_kafka_accepted,kafka_reuse_accepted))
 elif group_recovery_required:kafka_current_accepted=current_group_recovery_accepted and not any((current_producer_accepted,completed_kafka_reuse_accepted,current_managed_shape_accepted,current_release_accepted,current_kafka_confirm_reuse_accepted,current_kafka_confirm_accepted,current_image_kafka_accepted,kafka_reuse_accepted))
 elif producer_required:kafka_current_accepted=current_producer_accepted and completed_kafka_reuse_accepted and not any((current_managed_shape_accepted,current_release_accepted,current_kafka_confirm_reuse_accepted,current_kafka_confirm_accepted,current_image_kafka_accepted,kafka_reuse_accepted))
 elif managed_required:kafka_current_accepted=current_managed_shape_accepted and not current_release_accepted and not current_kafka_confirm_reuse_accepted and not current_kafka_confirm_accepted and not current_image_kafka_accepted and not kafka_reuse_accepted
 elif release_required:kafka_current_accepted=current_release_accepted and not current_kafka_confirm_reuse_accepted and not current_kafka_confirm_accepted and not current_image_kafka_accepted and not kafka_reuse_accepted and not current_managed_shape_accepted
 elif confirm_reuse_required:kafka_current_accepted=current_kafka_confirm_reuse_accepted and not current_kafka_confirm_accepted and not current_image_kafka_accepted and not kafka_reuse_accepted and not current_release_accepted and not current_managed_shape_accepted
 elif confirm_required:kafka_current_accepted=current_kafka_confirm_accepted and not current_image_kafka_accepted and not kafka_reuse_accepted and not current_kafka_confirm_reuse_accepted and not current_release_accepted and not current_managed_shape_accepted
 elif fresh_required:kafka_current_accepted=current_image_kafka_accepted and not current_kafka_confirm_accepted and not kafka_reuse_accepted and not current_kafka_confirm_reuse_accepted and not current_release_accepted and not current_managed_shape_accepted
 else:kafka_current_accepted=kafka_reuse_accepted and not current_image_kafka_accepted and not current_kafka_confirm_accepted and not current_kafka_confirm_reuse_accepted and not current_release_accepted and not current_managed_shape_accepted
 accepted=launcher_accepted and kafka_current_accepted and not failed and monitor.get('status')=='passed' and monitor.get('exit_code')==0 and main.get('accepted') is True and all(r['runtimeAttempted'] and r['runtimeCleanupAccepted'] and r.get('resourceCleanup',{}).get('accepted') is True for r in rows)
 run.write(out/'collection.json',{'started':True,'accepted':accepted,'scenarios':rows,'controllerPassed':monitor.get('status')=='passed','launcherReuseAccepted':launcher_accepted,'kafkaACLReuseAccepted':kafka_reuse_accepted,'sameCompleteGoNullScopeReuseAccepted':redis_null_passed_accepted,'sameCompleteGoNullScopeReuseRequired':redis_null_passed_required,'rawPushRequestContractAccepted':raw_push_accepted,'freshRawPushRequestContractRequired':raw_push_required,'rawPushNullScopeCompatibilityAccepted':raw_push_accepted,'sameCompleteGoSource':False,'actualCurrentNullGoCommands':0 if raw_push_required or redis_null_passed_required else (1 if redis_null_accepted else None),'redisNullAckControlAccepted':redis_null_accepted,'redisNullAckControlRequired':redis_null_required,'actualCurrentResolverCommands':0 if redis_null_required else (1 if owned_resolver_required else 0),'ownedResolverSchemeControlAccepted':owned_resolver_accepted,'ownedResolverSchemeControlRequired':owned_resolver_required,'actualCurrentCachedWaitCommands':0 if owned_resolver_required else (1 if cached_wait_required else 0),'cachedWaitControlAccepted':cached_wait_accepted,'cachedWaitControlRequired':cached_wait_required,'actualCurrentRuntimeProbeCommands':0 if cached_wait_required else (2 if rtc_probe_required else 0),'rtcPanicRuntimeControlAccepted':rtc_probe_accepted,'rtcPanicRuntimeControlRequired':rtc_probe_required,'actualCurrentRTCGoTestCommands':0 if rtc_probe_required else (1 if rtc_activation_required else 0),'rtcActivationPhaseControlAccepted':rtc_activation_accepted,'freshRTCActivationPhaseRequired':rtc_activation_required,'sameWholeGo1579':False,'oldControlCurrentActualPASS':False,'sameGo1579RuntimeReleaseControlReuseAccepted':runtime_release_passed_accepted,'sameGo1579RuntimeReleaseControlReuseRequired':runtime_release_passed_required,'actualCurrentRuntimeReleaseControlCommands':(1 if runtime_release_accepted else None) if runtime_release_required else 0,'runtimeReleaseContextControlAccepted':runtime_release_accepted,'freshRuntimeReleaseContextRequired':runtime_release_required,'historicalSourceCompatibilityOnly':runtime_release_required or runtime_release_passed_required or rtc_activation_required,'sameWholeGo1578':False,'oldScopesCurrentActualPASS':False,'sameGo1578GroupAndNamedScopesReuseAccepted':group_fixture_passed_accepted,'sameGo1578GroupAndNamedScopesReuseRequired':group_fixture_passed_required,'currentGroupRegistryFixtureContractAccepted':current_group_registry_fixture_accepted,'freshGroupRegistryFixtureContractRequired':group_registry_fixture_required,'groupRegistryFixtureNamedScopeCompatibilityAccepted':group_registry_fixture_reuse_accepted,'actualGroupRegistryFixtureTopDispatched':(1 if current_group_registry_fixture_accepted else None) if group_registry_fixture_required else 0,'historicalCronContractOnly':group_registry_fixture_required,'sameSourceCurrentGoNamedScopesReuseAccepted':current_named_scope_reuse_accepted,'sameSourceCurrentGoNamedScopesReuseRequired':named_scope_reuse_required,'currentCronFixtureContractAccepted':current_cron_fixture_accepted,'freshCronFixtureContractRequired':cron_fixture_required,'cronFixtureNamedScopeCompatibilityAccepted':cron_fixture_reuse_accepted,'actualDependencyGoListInvocations':((1 if group_registry_fixture_reuse_accepted else None) if group_registry_fixture_required else ((1 if cron_fixture_reuse_accepted else None) if cron_fixture_required else 0)),'actualCronFixtureTopDispatched':(1 if current_cron_fixture_accepted else None) if cron_fixture_required else 0,'currentCacheTestDeltaControlsAccepted':current_cache_test_delta_accepted,'freshCacheTestDeltaControlsRequired':cache_test_delta_required,'cacheTestDeltaPackageCompatibilityAccepted':cache_test_delta_reuse_accepted,'sameWholeSource':False,'originalGoCommandPassed':False,'cacheCommandInvocations':(1 if current_cache_test_delta_accepted else None) if cache_test_delta_required else 0,'cachePackageExecutions':(1 if current_cache_test_delta_accepted else None) if cache_test_delta_required else 0,'currentGroupRecoveryControlsAccepted':current_group_recovery_accepted,'freshGroupRecoveryControlsRequired':group_recovery_required,'groupRecoveryCommandInvocations':(1 if current_group_recovery_accepted else None) if group_recovery_required else 0,'groupRecoveryPackageExecutions':(3 if current_group_recovery_accepted else None) if group_recovery_required else 0,'actualCacheTopsDispatched':(2 if current_cache_test_delta_accepted else None) if cache_test_delta_required else ((2 if current_group_recovery_accepted else None) if group_recovery_required else 0),'actualManagedShapeTopsDispatched':(1 if current_group_recovery_accepted else None) if group_recovery_required else 0,'currentProducerControlAccepted':current_producer_accepted,'freshProducerControlRequired':producer_required,'producerCommandInvocations':(1 if current_producer_accepted else None) if producer_required else 0,'producerPackageExecutions':(1 if current_producer_accepted else None) if producer_required else 0,'sameSourceCompletedKafkaPackageReuseAccepted':completed_kafka_reuse_accepted,'originalTwoPackageScopeAccepted':False,'currentManagedShapeControlsAccepted':current_managed_shape_accepted,'freshManagedShapeControlsRequired':managed_required,'managedShapeCommandInvocations':(1 if current_managed_shape_accepted else None) if managed_required else 0,'managedShapePackageExecutions':(2 if current_managed_shape_accepted else None) if managed_required else 0,'currentReleaseControlsAccepted':current_release_accepted,'freshReleaseControlsRequired':release_required,'actualReleaseTopsDispatched':(3 if current_release_accepted else None) if release_required else 0,'sameSourceKafkaConfirmationReuseAccepted':current_kafka_confirm_reuse_accepted,'currentImageKafkaAccepted':current_image_kafka_accepted,'freshImageKafkaRequired':fresh_required and not confirm_required and not release_required and not managed_required and not producer_required and not group_recovery_required and not cache_test_delta_required and not cron_fixture_required and not named_scope_reuse_required,'currentKafkaConfirmationAccepted':current_kafka_confirm_accepted,'freshKafkaConfirmationRequired':confirm_required,'actualKafkaACLTestsDispatched':(1 if current_group_recovery_accepted or current_managed_shape_accepted or current_release_accepted or current_kafka_confirm_accepted or current_image_kafka_accepted else None) if fresh_required else 0,'actualProcessImageTestsDispatched':0 if named_scope_reuse_required or cron_fixture_required or cache_test_delta_required or group_recovery_required or producer_required or managed_required or release_required or confirm_required or confirm_reuse_required else ((1 if current_image_kafka_accepted else None) if fresh_required else 0),'processImageHistoricalFactsOnly':named_scope_reuse_required or cron_fixture_required or cache_test_delta_required or group_recovery_required or producer_required or managed_required or release_required or confirm_required or confirm_reuse_required,'currentWholeProjectGoPASS':False,'currentAdminSourceAcceptedByOldRTC':False,'currentWholeOwnedcontrolPackagePASS':False,'originalRetainedRuntimeJoinRequired':True,'privateDataUploaded':False,'containerAbsenceIsNotRuntimeDrain':True})
 emit_console_observation(rows,monitor,main,terminals,out)
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

def checked_compile_cache_evidence(compact,c,allocation):
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
    boundary='runtime-cleanup-unconfirmed'
  finite.append({'scenario':row['scenario'],'resourceStarted':started,'runtimeAttempted':attempted,'resourceCleanupAccepted':bool(started and row.get('resourceCleanup',{}).get('accepted')),'runtimeCleanupAccepted':runtime,'runtimeBoundary':boundary,'canonicalRuntimeAccepted':False})
 for auxiliary in ('acceptance-operator','msgtransfer-legacy'):
  matching=[row for row in phases if row.get('phase')=='compile-'+auxiliary]
  if len(matching)!=1 or matching[0].get('exitCode')!=0:raise ValueError('both original auxiliary builds must have completed')
 return {'tools':before,'custodySHA256':run.sha(run.regular(compact/'custody.json',1<<20)),'collectionSHA256':run.sha(evidence['collection.json']),'sourcePostSHA256':run.sha(evidence['source-post.json']),'controllerStatus':monitor['status'],'controllerExitCode':monitor['exit_code'],'failedRuntimePhase':failed[0]['phase'],'cleanup':finite,'compiled':{role:read('compiled-'+role+'.json') for role in run.ROLES}}


COMPILE_CACHE_POLICY=run.COMPILE_CACHE_POLICY

def snapshot_compile_cache_elf(source,destination,expected,remaining):
 source,destination=Path(source),Path(destination)
 if source.resolve()!=source or destination.parent.resolve()!=destination.parent or destination.exists() or destination.is_symlink() or type(expected.get('bytes')) is not int or not 64<=expected['bytes']<=min(PRODUCT_FILE_LIMIT,remaining) or not __import__('re').fullmatch('[0-9a-f]{64}',expected.get('sha256','')):raise ValueError('exact bounded original ELF and exclusive snapshot path required')
 parent=destination.parent.lstat()
 if not stat.S_ISDIR(parent.st_mode) or parent.st_uid!=os.getuid() or stat.S_IMODE(parent.st_mode)!=0o700:raise ValueError('private snapshot directory required')
 fd=os.open(source,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK|os.O_CLOEXEC);output=None
 try:
  before=os.fstat(fd)
  if not stat.S_ISREG(before.st_mode) or before.st_uid!=os.getuid() or before.st_nlink!=1 or before.st_size!=expected['bytes'] or before.st_mode&(stat.S_ISUID|stat.S_ISGID|stat.S_ISVTX|0o022) or not before.st_mode&0o111:raise ValueError('original owned executable mode/size required')
  output=os.open(destination,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600);digest=hashlib.sha256();size=0;header=b''
  while True:
   block=os.read(fd,1<<20)
   if not block:break
   if not header:header=block[:20]
   size+=len(block)
   if size>expected['bytes'] or size>remaining:raise ValueError('snapshot exceeds reviewed binary bound')
   space=os.statvfs(destination.parent)
   if space.f_bavail*space.f_frsize<1<<30:raise ValueError('snapshot disk floor exhausted')
   digest.update(block);view=memoryview(block)
   while view:
    written=os.write(output,view)
    if written<=0:raise ValueError('short snapshot write')
    view=view[written:]
  after=os.fstat(fd);current=source.lstat();identity=lambda v:(v.st_dev,v.st_ino,v.st_uid,v.st_gid,v.st_mode,v.st_nlink,v.st_size,v.st_mtime_ns,v.st_ctime_ns)
  if identity(before)!=identity(after) or identity(before)!=identity(current) or size!=expected['bytes'] or digest.hexdigest()!=expected['sha256']:raise ValueError('original ELF changed or differs from original compile receipt')
  if header[:6]!=b'\x7fELF\x02\x01' or header[16:18] not in (b'\x02\x00',b'\x03\x00') or header[18:20]!=b'\x3e\x00':raise ValueError('Linux amd64 ELF snapshot required')
  os.fsync(output);os.fchmod(output,0o500);os.close(output);output=None
  actual=product_elf_metadata(destination)
  if actual['bytes']!=expected['bytes'] or actual['sha256']!=expected['sha256'] or actual['mode']!=0o500:raise ValueError('closed read-only snapshot differs')
  return dict(actual,originalMode=stat.S_IMODE(before.st_mode),snapshotMode=0o500,originalCompileHashMatched=True)
 finally:
  if output is not None:os.close(output)
  os.close(fd)

def prepare_compile_cache_artifact():
 c,bodies,_,_,_,_=run.verify();inputs_raw=run.regular(run.INPUTS,1<<20)
 if c.get('compileCacheArtifactPolicy')!=COMPILE_CACHE_POLICY:raise ValueError('explicit independent unaccepted compile-cache policy required')
 allocation=run.allocation_root();root=allocation/'work';directory=root/'bin';private=root/'private';snapshot=allocation/'compile-cache-snapshot'
 for path in (allocation,root,directory,private):
  info=path.lstat()
  if path.resolve()!=path or not stat.S_ISDIR(info.st_mode) or info.st_uid!=os.getuid() or stat.S_IMODE(info.st_mode)!=0o700:raise ValueError('original private task allocation required')
 if json.loads(run.regular(allocation/'allocation-owner.json'))!=run.job_identity():raise ValueError('original task owner required')
 proof=checked_compile_cache_evidence(run.REPO/'ci-results/backend-fullgraph-compact',c,allocation)
 if run.tool_snapshot()!=proof['tools']:raise ValueError('source tools changed before cache snapshot')
 builds=json.loads(run.regular(private/'builds.json',1<<20));original_names=set(run.ROLES)|{'acceptance-operator','msgtransfer-legacy'};expected_names=original_names|{name+'-receipt.json' for name in original_names}
 if builds.get('version')!='source-bound-fullgraph-builds-v1' or builds.get('sourceSHA256')!=c['source']['digest'] or [row['role'] for row in builds['binaries']]!=list(run.ROLES) or {p.name for p in directory.iterdir()}!=expected_names:raise ValueError('closed original sixteen-build inventory required')
 declared=sum(proof['compiled'][role]['bytes'] for role in run.ROLES)
 if type(declared) is not int or declared>PRODUCT_TOTAL_LIMIT:raise ValueError('fourteen cache snapshots exceed2GiB')
 snapshot.mkdir(mode=0o700,exist_ok=False);products=[];total=0;entries={row['role']:row for row in c['entrypointContract']['entrypoints']}
 for row in builds['binaries']:
  role=row['role'];path=directory/role;rp=directory/(role+'-receipt.json');compiled=proof['compiled'][role]
  if row['path']!=str(path) or row['receiptPath']!=str(rp) or row['sourceSHA256']!=c['source']['digest']:raise ValueError('original compile paths and source required')
  receipt_raw=run.regular(rp,65536);wanted={'schema':1,'sourceSHA256':c['source']['digest'],'binarySHA256':compiled['sha256'],'binaryBytes':compiled['bytes'],'toolchainSHA256':proof['tools']['go']['sha256']}
  if run.sha(receipt_raw)!=row['receiptSHA256'] or json.loads(receipt_raw)!=wanted or row['sha256']!=compiled['sha256']:raise ValueError('public and private original compile receipts differ')
  product=snapshot_compile_cache_elf(path,snapshot/role,{'bytes':compiled['bytes'],'sha256':compiled['sha256']},PRODUCT_TOTAL_LIMIT-total);total+=product['bytes'];products.append(dict(product,package=entries[role]['package'],buildReceiptSHA256=row['receiptSHA256'],mayHaveExecutedInOriginalTask=True))
 staged_post=run.source_post(root,bodies,c,bodies['server/config/config.example.yaml']);after_c,after_bodies,_,_,_,_=run.verify()
 if staged_post.get('accepted') is not True or after_c!=c or after_bodies!=bodies or run.regular(run.INPUTS,1<<20)!=inputs_raw or run.tool_snapshot()!=proof['tools'] or {p.name for p in directory.iterdir()}!=expected_names or {p.name for p in snapshot.iterdir()}!=set(run.ROLES):raise ValueError('source/controller/tools/inventory changed during snapshot')
 for product in products:
  actual=product_elf_metadata(snapshot/product['name'])
  if actual['sha256']!=product['sha256'] or actual['bytes']!=product['bytes'] or actual['mode']!=0o500:raise ValueError('sealed snapshot changed before manifest')
 job=run.job_identity();manifest={'schema':1,'kind':COMPILE_CACHE_POLICY['kind'],'runtimeAccepted':False,'pidAbsenceAccepted':False,'readyPermit':False,'product20ProviderAccepted':False,'receiverImplemented':False,'deploymentABIValidated':False,'source':c['source'],'head':job['head'],'runID':job['runID'],'attempt':job['attempt'],'controllerStatus':proof['controllerStatus'],'controllerExitCode':proof['controllerExitCode'],'failedRuntimePhase':proof['failedRuntimePhase'],'cleanup':proof['cleanup'],'pidCleanupMayBeUnconfirmed':True,'liveOriginalPathsUploaded':False,'snapshotOriginalELFs':True,'sourcePostSHA256':proof['sourcePostSHA256'],'compactCustodySHA256':proof['custodySHA256'],'independentCollectionSHA256':proof['collectionSHA256'],'controllerInputsSHA256':run.sha(inputs_raw),'controllerPins':c['controllerPins'],'toolSHA256':{name:value['sha256'] for name,value in proof['tools'].items()},'locks':{name:run.sha(bodies['server/'+name]) for name in ('go.mod','go.sum')},'binaries':products,'totalBinaryBytes':total,'perBinaryLimitBytes':PRODUCT_FILE_LIMIT,'totalBinaryLimitBytes':PRODUCT_TOTAL_LIMIT,'GOOS':'linux','GOARCH':'amd64','CGO_ENABLED':'1','configsLogsKeysAuxiliariesIncluded':False,'uploadedOrDeployedByScript':False}
 digest=run.write(snapshot/'compile-cache-manifest.json',manifest)
 if {p.name for p in snapshot.iterdir()}!=set(COMPILE_CACHE_POLICY['allowedBasenames']):raise ValueError('only fourteen snapshots and new cache manifest may be uploaded')
 print(json.dumps({'kind':'unaccepted-compile-cache-snapshot-prepared-v1','runtimeAccepted':False,'readyPermit':False,'pidAbsenceAccepted':False,'binaryCount':14,'totalBinaryBytes':total,'manifestSHA256':digest,'liveOriginalPathsUploaded':False},separators=(',',':')))
 return 0

if __name__=='__main__':
 if sys.argv[1:]==['--prepare-compile-cache-artifact']:raise SystemExit(prepare_compile_cache_artifact())
 if sys.argv[1:]==['--prepare-build-only-product-artifact']:raise SystemExit(prepare_build_only_product_artifact())
 if sys.argv[1:]==['--prepare-product-artifact']:raise SystemExit(prepare_product_artifact())
 if sys.argv[1:]:raise SystemExit('unsupported collector arguments')
 raise SystemExit(collect())
