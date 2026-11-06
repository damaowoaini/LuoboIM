#!/usr/bin/env python3
"""Controller-only controls. No Go, container, DB, service or graph is executed."""
import ast,copy,hashlib,importlib.util,json,os,struct,tempfile,unittest,io,zipfile
from pathlib import Path
from unittest import mock
HERE=Path(__file__).resolve().parent

def load(name):
 spec=importlib.util.spec_from_file_location(name,HERE/(name+'.py'));value=importlib.util.module_from_spec(spec);spec.loader.exec_module(value);return value
run=load('run');builder=load('minio_build');resources=load('resources')

class FullGraphControls(unittest.TestCase):
 def test_prepare_lock_fresh_scope_and_unknown_cannot_alias_old_pass_or_canonical(self):
  source={'members':1,'digest':'1'*64};c={'state':'FROZEN_FINAL_SOURCE','source':source,'sourceReviewAccepted':True,'scenarios':list(run.SCENARIOS),'roles':list(run.ROLES),'existingGreenTestsToRerun':[],'reuseOldBinary':False,'resource':run.RESOURCE,'cleanupScopes':copy.deepcopy(run.CLEANUP_SCOPES),'cleanupReuse':copy.deepcopy(run.CLEANUP_REUSE),'cleanupCompleteScopes':copy.deepcopy(run.CLEANUP_COMPLETE_SCOPES),'cleanupRecentReuse':copy.deepcopy(run.CLEANUP_RECENT_REUSE),'cleanupFixtureRepair':copy.deepcopy(run.CLEANUP_FIXTURE_REPAIR),'originalTargets':copy.deepcopy(run.GRAPH_TARGETS),'requiredOriginalSubtests':copy.deepcopy(run.GRAPH_CHILDREN),'cleanupPassedReuse':copy.deepcopy(run.CLEANUP_PASSED_REUSE),'cleanupTestsToRun':[],'targetedKafkaACLControl':copy.deepcopy(run.KAFKA_ACL_CONTROL),'kafkaACLPassedReuse':copy.deepcopy(run.KAFKA_ACL_PASSED_REUSE),'kafkaACLTestsToRun':[]}
  with mock.patch.object(run,'FINAL_SOURCE',None),self.assertRaises(ValueError):run.check_locked(c)
  with mock.patch.object(run,'FINAL_SOURCE',source):
   run.check_locked(c)
   for key,value in [('reuseOldBinary',True),('existingGreenTestsToRerun',['old67']),('scenarios',['ack','redis-unknown']),('source',{'members':1,'digest':'2'*64}),('cleanupScopes',[]),('cleanupPassedReuse',{}),('cleanupTestsToRun',['old-passed']),('cleanupReuse',{}),('cleanupReuse',dict(run.CLEANUP_REUSE,source=source)),('cleanupReuse',dict(run.CLEANUP_REUSE,head='f'*40)),('cleanupReuse',dict(run.CLEANUP_REUSE,reviewSHA256='f'*64)),('cleanupRecentReuse',dict(run.CLEANUP_RECENT_REUSE,source=source)),('cleanupRecentReuse',dict(run.CLEANUP_RECENT_REUSE,head='f'*40)),('cleanupRecentReuse',dict(run.CLEANUP_RECENT_REUSE,reviewSHA256='f'*64)),('cleanupFixtureRepair',dict(run.CLEANUP_FIXTURE_REPAIR,productionChanges=1)),('cleanupCompleteScopes',[]),('ownedcontrolReuse',{'stale':'must-not-reuse-changed-production'}),('originalTargets',{}),('requiredOriginalSubtests',{})]:
    bad=copy.deepcopy(c);bad[key]=value
    with self.subTest(key=key),self.assertRaises(ValueError):run.check_locked(bad)
  cleanup={'version':'owned-fullgraph-resource-cleanup-v1','nonce':'a'*32,'exactContainerIDs':['b'*64],'unconfirmedIDs':[],'accepted':True}
  finite=run.public_cleanup(cleanup)
  self.assertNotIn(cleanup['nonce'],json.dumps(finite));self.assertIn('ownerNonceSHA256',finite)
  self.assertNotIn('runtimeDrain',finite)
  cleanup['exactContainerIDs']=['foreign/name']
  with self.assertRaises(ValueError):run.public_cleanup(cleanup)
  with self.assertRaises(ValueError):run.check_result({'version':'owned-fullgraph-token-v2-result-v1','scenario':'ack','accepted':False},'ack','1'*64,{},'2'*64)

  # The actual collector keeps only the new finite coordinator observation.
  collector=load('collect')
  terminal={'version':'owned-fullgraph-token-v2-result-v1','scenario':'reader-registered-cancel','accepted':False,'phase':'actual-resource-bootstrap','bootstrapStage':'kafka-coordinator','bootstrapFailureTag':'kafka-coordinator-unavailable','failureClass':'source-error-private','failureSHA256':'a'*64,'sourceSHA256':'','resourcePlaneSHA256':'b'*64,'actualStartedRoles':0,'rawError':'PRIVATE_ERROR_USER_GROUP'}
  observed=collector.public_terminal(json.dumps(terminal).encode(),terminal['scenario'])
  self.assertEqual((observed['bootstrapStage'],observed['bootstrapFailureTag']),('kafka-coordinator','kafka-coordinator-unavailable'))
  self.assertFalse(observed['rawErrorText']);self.assertNotIn('PRIVATE_',json.dumps(observed))
  for key,value in [('bootstrapStage','kafka-anything'),('bootstrapFailureTag','kafka-anything'),('phase','running')]:
   bad=copy.deepcopy(terminal);bad[key]=value
   with self.subTest(coordinator=key),self.assertRaises(ValueError):collector.public_terminal(json.dumps(bad).encode(),bad['scenario'])
  early=dict(terminal,localCleanupObserved=True,localCleanupState='no-session',localCleanupFailureClass='none')
  observed=collector.public_terminal(json.dumps(early).encode(),early['scenario'])
  self.assertEqual((observed['localCleanupObserved'],observed['localCleanupState'],observed['localCleanupFailureClass']),(True,'no-session','none'))
  terminal.update(phase='actual-owned-legacy-start-rpc-msg',bootstrapStage='launcher-probe',bootstrapFailureTag='',sourceSHA256='c'*64)
  stages=['launcher-enter', 'launcher-admission', 'launcher-state', 'launcher-role', 'spec', 'receipt', 'binary', 'bootstrap', 'work', 'environment', 'owner-files', 'exec', 'process-identity', 'process-image', 'launcher-persist', 'await-exit', 'await-identity', 'await-announcement', 'await-context']
  for stage in stages:
   retained=stage in ('process-identity', 'process-image','launcher-persist') or stage.startswith('await-')
   for state,kind in [('incomplete','deadline'),('complete','none'),('complete','owned-evidence')]:
    value=dict(terminal,legacyStartupStage=stage,legacyChildRetained=retained,localCleanupObserved=True,localCleanupState=state,localCleanupFailureClass=kind)
    observed=collector.public_terminal(json.dumps(value).encode(),value['scenario'])
    self.assertEqual((observed['legacyStartupStage'],observed['legacyChildRetained'],observed['localCleanupObserved'],observed['localCleanupState'],observed['localCleanupFailureClass']),(stage,retained,True,state,kind))
    self.assertFalse(observed['accepted']);self.assertEqual(observed['failureSHA256'],terminal['failureSHA256'])
    self.assertNotIn('PRIVATE_ERROR_USER_GROUP',json.dumps(observed));self.assertNotIn('rawError',observed)
  valid=dict(terminal,legacyStartupStage='await-announcement',legacyChildRetained=True,localCleanupObserved=True,localCleanupState='complete',localCleanupFailureClass='owned-evidence')
  for key,value in [('legacyStartupStage','PRIVATE_ERROR_USER_GROUP'),('legacyChildRetained',1),('legacyChildRetained',False),('localCleanupObserved',1),('localCleanupObserved',False),('localCleanupState','canonical-drain'),('localCleanupFailureClass','PRIVATE_ERROR_USER_GROUP'),('localCleanupState','no-session')]:
   bad=dict(valid);bad[key]=value
   with self.subTest(startup=key,value=value),self.assertRaises(ValueError):collector.public_terminal(json.dumps(bad).encode(),bad['scenario'])

  # Last entered Acquire stage is separate from both the overall phase and
  # legacy's last successful announcement. Only the original call return is
  # represented; these finite fields never mint canonical or Ready evidence.
  acquire_base=dict(valid,phase='actual-agent-stop-dual-drain-and-credential-exclusion')
  acquire_stages=['validate-request', 'check-targets', 'hold', 'services', 'stop-producer', 'drain-consumers-live', 'stop-consumer', 'exclude-credentials', 'exclude-credentials-mongo', 'exclude-credentials-redis', 'exclude-credentials-kafka', 'drain-consumers-empty', 'held-metadata', 'signed-observe']
  acquire_stages += ['exclude-credentials-kafka-input', 'exclude-credentials-kafka-admin-client', 'exclude-credentials-kafka-admin-handle', 'exclude-credentials-kafka-metadata-controller', 'exclude-credentials-kafka-metadata', 'exclude-credentials-kafka-acl-resource', 'exclude-credentials-kafka-legacy-client', 'exclude-credentials-kafka-broker-input', 'exclude-credentials-kafka-broker-handle', 'exclude-credentials-kafka-broker-open', 'exclude-credentials-kafka-live-denial', 'exclude-credentials-kafka-evidence', 'exclude-credentials-kafka-complete', 'exclude-credentials-kafka-legacy-close', 'exclude-credentials-kafka-admin-close', 'exclude-credentials-kafka-topic-guard', 'exclude-credentials-kafka-topic-write-controller', 'exclude-credentials-kafka-topic-write-call', 'exclude-credentials-kafka-topic-write-response', 'exclude-credentials-kafka-topic-read-controller', 'exclude-credentials-kafka-topic-read-call', 'exclude-credentials-kafka-topic-read-response', 'exclude-credentials-kafka-topic-exact-deny', 'exclude-credentials-kafka-group-guard', 'exclude-credentials-kafka-group-write-controller', 'exclude-credentials-kafka-group-write-call', 'exclude-credentials-kafka-group-write-response', 'exclude-credentials-kafka-group-read-controller', 'exclude-credentials-kafka-group-read-call', 'exclude-credentials-kafka-group-read-response', 'exclude-credentials-kafka-group-exact-deny', 'exclude-credentials-kafka-transaction-guard', 'exclude-credentials-kafka-transaction-write-controller', 'exclude-credentials-kafka-transaction-write-call', 'exclude-credentials-kafka-transaction-write-response', 'exclude-credentials-kafka-transaction-read-controller', 'exclude-credentials-kafka-transaction-read-call', 'exclude-credentials-kafka-transaction-read-response', 'exclude-credentials-kafka-transaction-exact-deny', 'exclude-credentials-kafka-cluster-guard', 'exclude-credentials-kafka-cluster-write-controller', 'exclude-credentials-kafka-cluster-write-call', 'exclude-credentials-kafka-cluster-write-response', 'exclude-credentials-kafka-cluster-read-controller', 'exclude-credentials-kafka-cluster-read-call', 'exclude-credentials-kafka-cluster-read-response', 'exclude-credentials-kafka-cluster-exact-deny']
  for stage in acquire_stages:
   for error_class in ('owned-evidence','deadline','cancelled','other'):
    value=dict(acquire_base,acquireStage=stage,acquireAttempted=True,acquireReturned=True,acquireErrorClass=error_class)
    observed=collector.public_terminal(json.dumps(value).encode(),value['scenario'])
    self.assertEqual((observed['acquireStage'],observed['acquireAttempted'],observed['acquireReturned'],observed['acquireErrorClass']),(stage,True,True,error_class))
    self.assertEqual((observed['accepted'],observed['failureClass'],observed['failureSHA256']),(value['accepted'],value['failureClass'],value['failureSHA256']))
    self.assertNotIn('PRIVATE_',json.dumps(observed));self.assertNotIn('rawError',observed)
  # A later Begin verification/journal failure may follow a SUCCESSFUL Acquire.
  value=dict(acquire_base,acquireStage='signed-observe',acquireAttempted=True,acquireReturned=True,acquireErrorClass='ok')
  observed=collector.public_terminal(json.dumps(value).encode(),value['scenario'])
  self.assertFalse(observed['accepted']);self.assertEqual(observed['acquireErrorClass'],'ok');self.assertEqual(observed['failureClass'],value['failureClass'])
  not_entered=dict(acquire_base,acquireStage='not-entered',acquireAttempted=False,acquireReturned=False,acquireErrorClass='not-entered')
  self.assertFalse(collector.public_terminal(json.dumps(not_entered).encode(),not_entered['scenario'])['acquireAttempted'])
  in_progress=dict(acquire_base,acquireStage='hold',acquireAttempted=True,acquireReturned=False,acquireErrorClass='in-progress')
  self.assertFalse(collector.public_terminal(json.dumps(in_progress).encode(),in_progress['scenario'])['acquireReturned'])
  for changed in [dict(acquireStage='PRIVATE_ERROR_USER_GROUP'),dict(acquireErrorClass='provider-raw'),dict(acquireAttempted=1),dict(acquireReturned=1),dict(acquireAttempted=False),dict(acquireReturned=False),dict(acquireStage='not-entered'),dict(acquireErrorClass='in-progress'),dict(acquireStage='hold',acquireErrorClass='ok')]:
   bad=dict(value,**changed)
   with self.subTest(acquire=changed),self.assertRaises(ValueError):collector.public_terminal(json.dumps(bad).encode(),bad['scenario'])

  for stage in ("exclude-credentials-kafka-topic-write-response", "exclude-credentials-kafka-live-denial", "exclude-credentials-kafka-admin-close"):
   for changed in (dict(acquireAttempted=False),dict(acquireErrorClass='ok'),dict(acquireStage=stage+'-PRIVATE_USER')):
    bad=dict(acquire_base,acquireStage=stage,acquireAttempted=True,acquireReturned=True,acquireErrorClass='owned-evidence',rawKafkaPrincipal='PRIVATE_USER')
    bad.update(changed)
    with self.subTest(kafka_stage=stage,kafka_invalid=changed),self.assertRaises(ValueError):collector.public_terminal(json.dumps(bad).encode(),bad['scenario'])

  # Original primary and local cleanup error classifications are independent.
  reader_base=dict(valid,phase='actual-two-reader-processes-and-original-registration',primaryRunErrorClass='preparation-unavailable',readerStage='start-rpc-conversation',readerCallState='returned',readerErrorClass='preparation-unavailable')
  observed=collector.public_terminal(json.dumps(reader_base).encode(),reader_base['scenario'])
  self.assertEqual((observed['primaryRunErrorClass'],observed['readerErrorClass']),('preparation-unavailable','preparation-unavailable'))
  self.assertEqual(observed['localCleanupFailureClass'],'owned-evidence')
  self.assertFalse(observed['accepted']);self.assertNotIn('PRIVATE_',json.dumps(observed))
  joined=dict(reader_base,failureClass='owned-evidence-rejected')
  observed=collector.public_terminal(json.dumps(joined).encode(),joined['scenario'])
  self.assertEqual(observed['primaryRunErrorClass'],'preparation-unavailable')
  self.assertEqual(observed['localCleanupFailureClass'],'owned-evidence')
  cleanup_only=dict(joined,primaryRunErrorClass='none',readerStage='observe-registration',readerErrorClass='none')
  self.assertEqual(collector.public_terminal(json.dumps(cleanup_only).encode(),cleanup_only['scenario'])['primaryRunErrorClass'],'none')
  for stage in (('start-rpc-conversation', 'start-rpc-msg', 'source-custody', 'observe-registration')):
   for kind in (('none', 'cancelled', 'deadline', 'permission', 'not-found', 'already-exists', 'registration-pending', 'commit-unconfirmed', 'owned-evidence', 'import-evidence', 'import-conflict', 'import-invalid', 'preparation-unavailable', 'preparation-conflict', 'inventory-private', 'inventory-identity', 'inventory-changed', 'inventory-source', 'other')):
    if kind=='registration-pending' and stage!='observe-registration':continue
    value=dict(joined,readerStage=stage,readerCallState='returned',readerErrorClass=kind,primaryRunErrorClass=kind)
    observed=collector.public_terminal(json.dumps(value).encode(),value['scenario'])
    self.assertEqual((observed['readerStage'],observed['readerCallState'],observed['readerErrorClass'],observed['primaryRunErrorClass']),(stage,'returned',kind,kind))
    self.assertFalse(observed['accepted']);self.assertNotIn('rawError',observed)
  # A last returned pending value does not become the later terminal cause.
  pending=dict(joined,readerStage='observe-registration',readerCallState='returned',readerErrorClass='registration-pending',primaryRunErrorClass='deadline',failureClass='deadline')
  observed=collector.public_terminal(json.dumps(pending).encode(),pending['scenario'])
  self.assertEqual((observed['readerErrorClass'],observed['primaryRunErrorClass']),('registration-pending','deadline'))
  for state,stage,kind in [('not-entered','not-entered','not-entered'),('in-progress','source-custody','in-progress')]:
   value=dict(joined,readerStage=stage,readerCallState=state,readerErrorClass=kind)
   observed=collector.public_terminal(json.dumps(value).encode(),value['scenario'])
   self.assertEqual((observed['readerStage'],observed['readerCallState'],observed['readerErrorClass']),(stage,state,kind))
  for change in [dict(primaryRunErrorClass='PRIVATE_ERROR_USER_GROUP'),dict(primaryRunErrorClass=True),dict(readerStage='PRIVATE_ERROR_USER_GROUP'),dict(readerCallState=True),dict(readerErrorClass='private-provider-text'),dict(readerStage='not-entered'),dict(readerCallState='not-entered'),dict(readerCallState='in-progress'),dict(readerStage='start-rpc-msg',readerErrorClass='registration-pending')]:
   bad=dict(joined,**change)
   with self.subTest(reader=change),self.assertRaises(ValueError):collector.public_terminal(json.dumps(bad).encode(),bad['scenario'])

  # These are labels from the same original call, never a new child/Ready probe.
  constructor_keys=('constructorStage','constructorRole','constructorCallState','constructorErrorClass','constructorPredicate')
  observed=collector.public_terminal(json.dumps(joined).encode(),joined['scenario'])
  self.assertEqual(tuple(observed[k] for k in constructor_keys),('not-entered','none','not-entered','none','none'))
  constructor_roles=('api','crontask','msggateway','msgtransfer-managed','push','rpc-auth','rpc-conversation','rpc-encryption','rpc-friend','rpc-group','rpc-msg','rpc-rtc','rpc-third','rpc-user')
  constructor_base=dict(joined,phase='actual-twelve-other-source-constructors',primaryRunErrorClass='permission',constructorStage='start',constructorRole='api',constructorCallState='returned',constructorErrorClass='owned-evidence',constructorPredicate='none',rawOwner='PRIVATE_OWNER_NONCE')
  for role in constructor_roles:
   if role not in ('rpc-conversation','rpc-msg'):
    value=dict(constructor_base,constructorRole=role)
    observed=collector.public_terminal(json.dumps(value).encode(),value['scenario'])
    self.assertEqual((observed['constructorRole'],observed['constructorErrorClass']),(role,'owned-evidence'))
    self.assertEqual(observed['primaryRunErrorClass'],'permission');self.assertFalse(observed['accepted'])
    self.assertNotIn('rawOwner',observed);self.assertNotIn('PRIVATE_OWNER_NONCE',json.dumps(observed))
  custody=dict(constructor_base,constructorStage='source-custody',constructorRole='none')
  observed=collector.public_terminal(json.dumps(custody).encode(),custody['scenario'])
  self.assertEqual((observed['constructorRole'],observed['constructorPredicate']),('none','none'))
  predicates=('process-set','graph-preconditions','bootstrap','requirement','input-state','handle','joined','owner','done','process-before','image','envelope','instance','tuple','mongo-binding','services','components','process-after')
  for predicate in predicates:
   role='none' if predicate=='graph-preconditions' else 'api'
   value=dict(constructor_base,constructorStage='observe-registration',constructorRole=role,constructorPredicate=predicate)
   observed=collector.public_terminal(json.dumps(value).encode(),value['scenario'])
   self.assertEqual((observed['constructorRole'],observed['constructorPredicate']),(role,predicate))
   self.assertEqual((observed['primaryRunErrorClass'],observed['localCleanupFailureClass']),('permission','owned-evidence'))
  for role in constructor_roles:
   pending=dict(constructor_base,constructorStage='observe-registration',constructorRole=role,constructorPredicate='registration-absent',constructorErrorClass='registration-pending',primaryRunErrorClass='deadline',failureClass='deadline')
   observed=collector.public_terminal(json.dumps(pending).encode(),pending['scenario'])
   self.assertEqual((observed['constructorRole'],observed['constructorErrorClass'],observed['primaryRunErrorClass']),(role,'registration-pending','deadline'))
  complete=dict(constructor_base,constructorStage='observe-registration',constructorRole='none',constructorPredicate='complete',constructorErrorClass='none')
  observed=collector.public_terminal(json.dumps(complete).encode(),complete['scenario'])
  self.assertFalse(observed['accepted']);self.assertEqual(observed['primaryRunErrorClass'],'permission')
  for stage,role in [('start','api'),('source-custody','none'),('observe-registration','none')]:
   value=dict(constructor_base,constructorStage=stage,constructorRole=role,constructorCallState='in-progress',constructorErrorClass='in-progress')
   observed=collector.public_terminal(json.dumps(value).encode(),value['scenario'])
   self.assertEqual(observed['constructorCallState'],'in-progress')
  for change in [dict(constructorStage='PRIVATE_PATH'),dict(constructorRole='PRIVATE_UID'),dict(constructorRole=['api']),dict(constructorCallState=True),dict(constructorErrorClass='private-error-text'),dict(constructorPredicate='private-owner-path'),dict(constructorStage='not-entered'),dict(constructorCallState='not-entered'),dict(constructorCallState='in-progress'),dict(constructorRole='rpc-msg'),dict(constructorRole='none'),dict(constructorPredicate='done'),dict(constructorErrorClass='registration-pending')]:
   bad=dict(constructor_base,**change)
   with self.subTest(constructor=change),self.assertRaises(ValueError):collector.public_terminal(json.dumps(bad).encode(),bad['scenario'])
  for change in [dict(constructorRole='api'),dict(constructorPredicate='done'),dict(constructorErrorClass='registration-pending')]:
   bad=dict(custody,**change)
   with self.subTest(custody=change),self.assertRaises(ValueError):collector.public_terminal(json.dumps(bad).encode(),bad['scenario'])
  observe_base=dict(constructor_base,constructorStage='observe-registration',constructorRole='api',constructorPredicate='done')
  for change in [dict(constructorRole='none'),dict(constructorPredicate='none'),dict(constructorPredicate='complete'),dict(constructorPredicate='registration-absent'),dict(constructorErrorClass='none'),dict(constructorErrorClass='registration-pending'),dict(constructorCallState='in-progress',constructorErrorClass='in-progress')]:
   bad=dict(observe_base,**change)
   with self.subTest(observe=change),self.assertRaises(ValueError):collector.public_terminal(json.dumps(bad).encode(),bad['scenario'])

  # Reuse only the original same-call Start/launcher labels; no second probe.
  start_predicates=('start-input', 'start-hold', 'start-bootstrap', 'start-requirement', 'start-server-environment', 'start-server-bootstrap', 'start-managed-environment', 'start-managed-shape', 'start-managed-fields', 'start-reader-input', 'start-nonreader-input', 'start-intent', 'start-reader', 'start-owner-record', 'start-service-spec', 'start-service-receipt', 'start-service-binary', 'start-service-bootstrap', 'start-service-work', 'start-service-environment', 'start-service-owner-files', 'start-service-exec', 'start-service-process-identity', 'start-service-process-image')
  for predicate in start_predicates:
   value=dict(constructor_base,constructorRole='msggateway',constructorPredicate=predicate)
   observed=collector.public_terminal(json.dumps(value).encode(),value['scenario'])
   self.assertEqual((observed['constructorRole'],observed['constructorPredicate'],observed['constructorErrorClass']),('msggateway',predicate,'owned-evidence'))
   self.assertEqual((observed['primaryRunErrorClass'],observed['localCleanupFailureClass']),('permission','owned-evidence'))
   self.assertFalse(observed['accepted']);self.assertNotIn('rawOwner',observed)
   self.assertNotIn('PRIVATE_OWNER_NONCE',json.dumps(observed))
   for stage in ('source-custody','observe-registration'):
    bad=dict(value,constructorStage=stage)
    if stage=='source-custody':bad['constructorRole']='none'
    with self.subTest(start_predicate=predicate,stage=stage),self.assertRaises(ValueError):collector.public_terminal(json.dumps(bad).encode(),bad['scenario'])
  value=dict(constructor_base,constructorRole='msggateway',constructorPredicate='start-complete',constructorErrorClass='none')
  observed=collector.public_terminal(json.dumps(value).encode(),value['scenario'])
  self.assertEqual((observed['constructorPredicate'],observed['primaryRunErrorClass']),('start-complete','permission'));self.assertFalse(observed['accepted'])
  for change in (dict(constructorPredicate='start-service-private-path'),dict(constructorPredicate='start-complete'),dict(constructorPredicate='start-service-process-image',constructorCallState='in-progress',constructorErrorClass='in-progress')):
   bad=dict(constructor_base,**change)
   with self.subTest(start_invalid=change),self.assertRaises(ValueError):collector.public_terminal(json.dumps(bad).encode(),bad['scenario'])

  # Original prepare failure is latched before cleanup; no Docker or service runs.
  with tempfile.TemporaryDirectory() as directory:
   folder=Path(directory);private=folder/'private';private.mkdir(mode=0o700)
   plane=object.__new__(resources.Plane);plane.private=private;plane.docker=Path('/usr/bin/docker');plane.calls=0;plane.failed=False
   process=mock.Mock();process.wait.return_value=1
   def fail_original_prepare():
    plane._prepare_at('kafka-format','format-attach')
    return plane.call(['start','--attach','a'*64],90)
   with mock.patch.object(plane,'_prepare_body',side_effect=fail_original_prepare),mock.patch.object(resources.subprocess,'Popen',return_value=process):
    with self.assertRaises(RuntimeError) as caught:run.prepare_resource_plane(plane,run.SCENARIOS[1])
   original=caught.exception
   raw=run.regular(private/('prepare-observation-'+run.SCENARIOS[1]+'.json'),4096)
   record=collector.public_resource_preparation(raw,run.SCENARIOS[1])
   self.assertEqual((record['role'],record['step'],record['callState'],record['errorClass'],record['cliExitCode']),('kafka-format','format-attach','returned','command-nonzero',1))
   process.wait.return_value=0
   with mock.patch.object(resources.subprocess,'Popen',return_value=process):plane.call(['ps','--all'])
   self.assertEqual(plane.prepare_observation(),{k:record[k] for k in ('role','step','callState','errorClass','cliExitCode')})
   self.assertEqual(run.regular(private/('prepare-observation-'+run.SCENARIOS[1]+'.json')),raw)
   # The exact original exception survives a failure to write diagnostics.
   with mock.patch.object(plane,'_prepare_body',side_effect=original),mock.patch.object(run,'write',side_effect=PermissionError('PRIVATE_PATH')):
    with self.assertRaises(RuntimeError) as repeated:run.prepare_resource_plane(plane,run.SCENARIOS[1])
   self.assertIs(repeated.exception,original)
   with mock.patch.object(plane,'_prepare_body',return_value={'fixture':'same-claim'}):
    self.assertEqual(run.prepare_resource_plane(plane,run.SCENARIOS[0]),{'fixture':'same-claim'})
   succeeded=collector.public_resource_preparation(run.regular(private/('prepare-observation-'+run.SCENARIOS[0]+'.json')),run.SCENARIOS[0])
   self.assertEqual(run.regular(private/('prepare-observation-'+run.SCENARIOS[1]+'.json')),raw)
   self.assertEqual((succeeded['errorClass'],succeeded['cliExitCode']),('none',None))
   # A later non-prepare/cleanup command cannot rewrite successful preparation.
   process.wait.return_value=1
   with mock.patch.object(resources.subprocess,'Popen',return_value=process),self.assertRaises(RuntimeError):plane.call(['stop','a'*64])
   self.assertEqual(plane.prepare_observation()['errorClass'],'none')
   self.assertIsNone(object.__new__(resources.Plane).prepare_observation())
   for key,badvalue in [('role','PRIVATE_USER'),('role','mongo'),('step','/PRIVATE_PATH'),('callState','in-progress'),('errorClass','PRIVATE_ERROR'),('cliExitCode',True),('cliExitCode',256),('cliExitCode',0),('rawErrorText',True)]:
    changed=dict(record);changed[key]=badvalue
    with self.subTest(resourcePreparation=key),self.assertRaises(ValueError):collector.public_resource_preparation(json.dumps(changed).encode(),run.SCENARIOS[1])
   changed=dict(record,privatePath='/private/not-for-upload')
   with self.assertRaises(ValueError):collector.public_resource_preparation(json.dumps(changed).encode(),run.SCENARIOS[1])

  # Use the real source command builder: a leading '-' remains one unchanged value.
  with tempfile.TemporaryDirectory() as directory:
   folder=Path(directory);private=folder/'private';private.mkdir(mode=0o700)
   plane=object.__new__(resources.Plane);plane.root=folder;plane.private=private;plane.nonce='a'*32;plane.pending_roles=[];plane.ids=['b'*64]
   image='docker.io/apache/kafka@sha256:'+'c'*64;plane.images={'kafka':image};cid='d'*64;commands=[]
   image_row={'Id':'sha256:'+'e'*64,'RepoDigests':[image],'Config':{'Volumes':{p:{} for p in ('/var/lib/kafka/data','/etc/kafka/secrets','/mnt/shared/config')}}}
   container_row={'Id':cid,'Config':{'Labels':{'luobo.fullgraph.owner':plane.nonce,'luobo.fullgraph.role':'kafka-format'}},'State':{'Running':False,'ExitCode':0}}
   def original_command(argv,timeout=60):
    commands.append(list(argv))
    if argv[:2]==['image','inspect']:return json.dumps([image_row]).encode()
    if argv[0]=='create':
     (private/'kafka-format.cid').write_text(cid+'\n');return cid.encode()
    if argv[:2]==['container','inspect']:return json.dumps([container_row]).encode()
    if argv[:2]==['start','--attach']:return b''
    raise AssertionError('unexpected fixture source command')
   cluster='-ABCDEFGHIJKLMNOPQRSTU'
   with mock.patch.object(plane,'call',side_effect=original_command):plane.format_kafka(cluster)
   create=next(argv for argv in commands if argv[0]=='create');tail=create[create.index(image)+1:]
   self.assertEqual(tail,['format','--cluster-id='+cluster,'--config','/task/kafka.properties'])
   self.assertNotIn('--cluster-id',tail);self.assertEqual(tail[1].split('=',1)[1],cluster)
   self.assertEqual(sum(argv[:2]==['start','--attach'] for argv in commands),1)

  # Reuse is source/tool/reporter-bound and never dispatches the retained helper.
  kafka=copy.deepcopy(run.KAFKA_ACL_PASSED_REUSE)
  self.assertEqual(run.check_kafka_acl_receipt(kafka),kafka)
  self.assertEqual((kafka['run'],kafka['job'],kafka['attempt'],kafka['head']),(39091570750,117569259635,1,'500baca44c853079ca6f06a001ec3a2bf873cf23'))
  rejected=[dict(kafka,source={'members':1576,'digest':'f'*64}),dict(kafka,run=1),dict(kafka,job=1),dict(kafka,attempt=2),dict(kafka,head='f'*40),dict(kafka,currentFreshPASS=True),dict(kafka,runtimeAccepted=True),dict(kafka,actualCurrentTestsDispatched=1)]
  for change in ('drop-parent','foreign-child','events','source-post','go-tool','artifact'):
   bad=copy.deepcopy(kafka)
   if change=='drop-parent':bad['names']['body']['namedCompletions']=bad['names']['body']['namedCompletions'][1:]
   elif change=='foreign-child':bad['names']['body']['namedCompletions'][1]['name']='TestForeign/child'
   elif change=='events':bad['events']['body']=[r for r in bad['events']['body'] if not(r.get('Action')=='pass' and r.get('Test'))]
   elif change=='source-post':bad['sourcePost']['body']['accepted']=False
   elif change=='go-tool':bad['goTool']['sha256']='f'*64
   else:bad['artifact']['sha256']='f'*64
   rejected.append(bad)
  for bad in rejected:
   with self.assertRaises(ValueError):run.check_kafka_acl_receipt(bad)
  # Small explicit test source: the real checker must query every body hash.
  fixture_bodies={'server/fixture.go':b'fixed source'}
  fixture_recipe=mock.Mock()
  fixture_recipe.rows.side_effect=lambda bodies:sorted((name,len(body),run.sha(body)) for name,body in bodies.items())
  fixture_recipe.digest.side_effect=lambda rows:run.sha(json.dumps(rows,separators=(',',':')).encode())
  fixture=copy.deepcopy(kafka);fixture['source']={'members':1,'digest':fixture_recipe.digest(fixture_recipe.rows(fixture_bodies))};fixture['names']['body']['source']=fixture['source'];fixture['sourcePost']['body']['source']=fixture['source']
  for key in ('names','sourcePost'):
   raw=(json.dumps(fixture[key]['body'],separators=(',',':'),ensure_ascii=False)+'\n').encode();fixture[key]['bytes']=len(raw);fixture[key]['sha256']=run.sha(raw)
  with mock.patch.object(run,'KAFKA_ACL_PASSED_REUSE',fixture),mock.patch.object(collector.run,'KAFKA_ACL_PASSED_REUSE',fixture),mock.patch.object(run,'FINAL_SOURCE',fixture['source']),tempfile.TemporaryDirectory() as directory:
   out=Path(directory).resolve()
   with mock.patch.object(run,'run_kafka_acl_control',side_effect=AssertionError('unchanged Kafka ACL must not dispatch')) as dispatch:
    self.assertEqual(run.load_kafka_acl_reuse(out,fixture_bodies,fixture_recipe,fixture['goTool']),fixture);dispatch.assert_not_called()
   self.assertEqual(json.loads((out/'kafka-acl-passed-reuse.json').read_bytes()),fixture)
   for bodies in ({},{'server/fixture.go':b'changed source'},{**fixture_bodies,'server/foreign.go':b'extra'}):
    with self.assertRaises(ValueError):run.check_kafka_acl_reuse(fixture,bodies,fixture_recipe)
   with self.assertRaises(ValueError):run.load_kafka_acl_reuse(out,fixture_bodies,fixture_recipe,dict(fixture['goTool'],sha256='f'*64))
   run.write(out/'source-post.json',fixture['sourcePost']['body']);run.write(out/'tools-before.json',{'go':fixture['goTool']});run.write(out/'tools-after.json',{'go':fixture['goTool']})
   self.assertEqual(collector.checked_kafka_acl_evidence(out),fixture)
   (out/'source-post.json').write_text(json.dumps({'accepted':False,'source':fixture['source']}))
   with self.assertRaises(ValueError):collector.checked_kafka_acl_evidence(out)
  with mock.patch.object(run,'FINAL_SOURCE',source):
   for key,value in [('kafkaACLTestsToRun',['changed']),('kafkaACLPassedReuse',dict(kafka,actualCurrentTestsDispatched=1))]:
    bad=copy.deepcopy(c);bad[key]=value
    with self.assertRaises(ValueError):run.check_locked(bad)

 def test_real_tracked_git_bodies_symlink_bounds_static_elf_and_strict_receipt(self):
  with tempfile.TemporaryDirectory() as directory:
   root=Path(directory);source=root/'source';source.mkdir()
   listing=b''
   for name,mode,body in [('a.go','100644',b'package main\n'),('tool.sh','100755',b'#!/bin/sh\nexit 0\n')]:
    p=source/name;p.write_bytes(body);p.chmod(0o700 if mode=='100755' else 0o600)
    oid=hashlib.sha1(b'blob '+str(len(body)).encode()+b'\0'+body).hexdigest();listing+=mode.encode()+b' '+oid.encode()+b' 0\t'+name.encode()+b'\0'
   rows,digest=builder.tracked_vector(source,listing)
   self.assertEqual([r['path'] for r in rows],['a.go','tool.sh']);self.assertRegex(digest,'^[0-9a-f]{64}$')
   (source/'a.go').write_bytes(b'package changed\n')
   with self.assertRaises(ValueError):builder.tracked_vector(source,listing)
   (source/'escape').symlink_to('../outside')
   target=b'../outside';oid=hashlib.sha1(b'blob '+str(len(target)).encode()+b'\0'+target).hexdigest()
   with self.assertRaises(ValueError):builder.tracked_vector(source,b'120000 '+oid.encode()+b' 0\tescape\0')
   receipt={'version':'owned-minio-source-build-receipt-v1','repository':'https://github.com/minio/minio','commit':'a'*40,'sourceDigestSHA256':'b'*64,'goModSHA256':'c'*64,'goSumSHA256':'d'*64,'toolchainSHA256':'e'*64,'cgoEnabled':'0','binarySHA256':'f'*64,'binaryBytes':64}
   path=root/'receipt.json';pin=builder.private_json(path,receipt)
   self.assertEqual(path.read_bytes(),(json.dumps(receipt,separators=(',',':'))+'\n').encode());self.assertEqual(pin,hashlib.sha256(path.read_bytes()).hexdigest())
   before=path.read_bytes()
   with self.assertRaises(FileExistsError):builder.private_json(path,{'overwrite':True})
   self.assertEqual(path.read_bytes(),before)
   elf=bytearray(120);elf[:6]=b'\x7fELF\x02\x01';struct.pack_into('<H',elf,18,62);struct.pack_into('<Q',elf,32,64);struct.pack_into('<HH',elf,54,56,1)
   builder.validate_static_elf(elf);struct.pack_into('<I',elf,64,3)
   with self.assertRaises(ValueError):builder.validate_static_elf(elf)
   # Real tiny synthetic ELF/ZIP fixtures only, not an actual MinIO execution.
   root=root.resolve()
   struct.pack_into('<I',elf,64,1);binary=bytes(elf)
   original=dict(receipt,binarySHA256=builder.sha(binary),binaryBytes=len(binary))
   catalog={'minio':{'mode':'source-elf','status':'historical_unmaintained_test_only','repository':original['repository'],'commit':original['commit'],'goMod':{'sha256':original['goModSHA256']},'goSum':{'sha256':original['goSumSHA256']},'cgoEnabled':'0'}}
   original_raw=(json.dumps(original,separators=(',',':'))+'\n').encode()
   producer={'version':builder.FIXTURE_POLICY['version'],'fixtureOnly':True,'runtimeAccepted':False,'readyPermit':False,'repository':'owner/repo','head':'a'*40,'run':123,'attempt':1,'receipt':original,'receiptSHA256':builder.sha(original_raw),'sourceToolPostExact':True,'actualPlanePrepared':True,'exactSevenContainerCleanup':True,'binary':{'name':'minio','bytes':len(binary),'sha256':builder.sha(binary),'mode':0o700,'GOOS':'linux','GOARCH':'amd64','CGO_ENABLED':'0'}}
   manifest=(json.dumps(producer,separators=(',',':'))+'\n').encode()
   archive=root/'fixture.zip'
   with zipfile.ZipFile(archive,'x') as zipped:
    for name,raw in [('minio',binary),('build-receipt.json',original_raw),('manifest.json',manifest)]:zipped.writestr(name,raw)
   binding={'rootFixtureAccepted':True,'rootAcceptanceSHA256':'1'*64,'repository':'owner/repo','run':123,'head':'a'*40,'attempt':1,'jobID':456,'jobConclusion':'failure','runConclusion':'failure','artifactID':789,'artifactBytes':archive.stat().st_size,'artifactSHA256':builder.sha(archive.read_bytes()),'manifestSHA256':builder.sha(manifest),'receipt':original}
   self.assertIsNone(builder.fixture_policy({'minioFixturePolicy':builder.FIXTURE_POLICY,'minioFixtureBinding':None}))
   self.assertEqual(builder.fixture_policy({'minioFixturePolicy':builder.FIXTURE_POLICY,'minioFixtureBinding':binding}),binding)
   for bad in [dict(binding,rootFixtureAccepted=False),dict(binding,attempt=2),dict(binding,artifactBytes=True),dict(binding,jobConclusion='cancelled')]:
    with self.assertRaises(ValueError):builder.fixture_policy({'minioFixturePolicy':builder.FIXTURE_POLICY,'minioFixtureBinding':bad})
   received=root/'received';received.mkdir(mode=0o700)
   dto,value=builder.unpack_fixture(archive,received,binding,catalog,original['toolchainSHA256'])
   self.assertEqual(value,original);self.assertEqual(dto['receiptSHA256'],builder.sha(original_raw))
   self.assertEqual(builder.stream_static_elf(received/'minio'),producer['binary'])
   self.assertEqual(resources.validate_source_minio(dto),dto)
   with self.assertRaises(ValueError):builder.proof(original,catalog,'f'*64)
   with self.assertRaises(ValueError):builder.proof(original,{'minio':dict(catalog['minio'],commit='b'*40)},original['toolchainSHA256'])
   (received/'alias').symlink_to('minio')
   with self.assertRaises(ValueError):builder.stream_static_elf(received/'alias')
   altered=bytearray(binary);altered[-1]^=1;(received/'minio').write_bytes(altered)
   with self.assertRaises(ValueError):builder.fixture_dto(received,original)
   for kind in ('extra','symlink','oversize','private-manifest-upgrade'):
    badzip=root/(kind+'.zip')
    badmanifest=manifest if kind!='private-manifest-upgrade' else json.dumps(dict(producer,runtimeAccepted=True)).encode()
    with zipfile.ZipFile(badzip,'x') as zipped:
     if kind=='symlink':
      alias=zipfile.ZipInfo('minio');alias.create_system=3;alias.external_attr=(0o120777<<16);zipped.writestr(alias,'other')
     else:zipped.writestr('minio',binary)
     zipped.writestr('build-receipt.json',original_raw);zipped.writestr('manifest.json',badmanifest)
     if kind=='extra':zipped.writestr('config.yaml','PRIVATE_CONFIG')
    if kind=='oversize':
     raw=bytearray(badzip.read_bytes());at=raw.index(b'PK\x01\x02');struct.pack_into('<I',raw,at+24,(256<<20)+1);badzip.write_bytes(raw)
    output=root/('bad-'+kind);output.mkdir(mode=0o700)
    badbinding=dict(binding,manifestSHA256=builder.sha(badmanifest))
    with self.subTest(fixture=kind),self.assertRaises(ValueError):builder.unpack_fixture(badzip,output,badbinding,catalog,original['toolchainSHA256'])
   # API requests use a synthetic token; the actual anonymous blob pipe cannot receive it.
   seen=[];blob=archive.read_bytes();token='FIXTURE_API_TOKEN_NOT_A_REAL_SECRET'
   class Response(io.BytesIO):status=200
   class Opener:
    def open(self,request,timeout):
     url=request.full_url;seen.append((url,dict(request.header_items())))
     if '.blob.core.windows.net/' in url:
      self_token=request.get_header('Authorization');assert self_token is None
      return Response(blob)
     self_token=request.get_header('Authorization');assert self_token=='Bearer '+token
     if url.endswith('/zip'):raise builder.urllib.error.HTTPError(url,302,'redirect',{'Location':'https://fixture.blob.core.windows.net/fixed'},None)
     if '/artifacts/' in url:data={'id':789,'name':'backend-fullgraph-minio-fixture-123-1','expired':False,'size_in_bytes':len(blob),'digest':'sha256:'+binding['artifactSHA256'],'workflow_run':{'id':123,'head_sha':'a'*40}}
     elif '/jobs/' in url:data={'run_id':123,'run_attempt':1,'head_sha':'a'*40,'status':'completed','conclusion':'failure'}
     else:data={'head_sha':'a'*40,'run_attempt':1,'status':'completed','conclusion':'failure'}
     return Response(json.dumps(data).encode())
   downloaded=root/'download.part'
   with mock.patch.object(builder.urllib.request,'build_opener',return_value=Opener()):
    builder.acquire_archive(binding,downloaded,token,'owner/repo')
   self.assertEqual(downloaded.read_bytes(),blob);self.assertEqual(len(seen),5)
   self.assertEqual(seen[-1][1],{});self.assertTrue(all(url.startswith('https://api.github.com/repos/owner/repo/') for url,headers in seen[:-1]))
   with self.assertRaises(ValueError):builder.acquire_archive(binding,root/'foreign.part',token,'other/repo')
   # Actual retained plane/CID files, including the seventh KRaft-format child.
   scenario='reader-registered-cancel';folder=root/('plane-'+scenario);private=folder/'private';private.mkdir(parents=True)
   roles=('keeper','mongo','redis','zookeeper','kafka','minio','kafka-format');ids={role:format(i+1,'064x') for i,role in enumerate(roles)};nonce='a'*32
   for role,cid in ids.items():(private/(role+'.cid')).write_text(cid+'\n')
   claim={'version':'owned-fullgraph-resource-plane-v1','nonce':nonce,'uid':os.getuid(),'resourceRoot':str(folder),'minioMode':'source-elf','minioSource':dto,'observations':[{'role':role,'id':ids[role],'PID':i+2} for i,role in enumerate(roles[:-1])]}
   run.write(private/'plane.json',claim);run.write(private/'owner.json',{'version':'fullgraph-resource-owner-v1','nonce':nonce,'uid':os.getuid(),'minioSource':dto})
   cleanup={'accepted':True,'unconfirmedIDs':[],'ownerNonceSHA256':builder.sha(nonce.encode()),'exactContainerIDs':list(ids.values())}
   collection={'scenarios':[{'scenario':scenario,'resourceStarted':True,'resourceCleanup':cleanup}]}
   self.assertEqual(builder.checked_prepared_planes(root,collection,dto,run),[scenario])
   for key,bad in [('accepted',False),('unconfirmedIDs',['unknown']),('ownerNonceSHA256','b'*64),('exactContainerIDs',list(ids.values())[:-1]),('exactContainerIDs',list(ids.values())[:-1]+['f'*64])]:
    changed=copy.deepcopy(collection);changed['scenarios'][0]['resourceCleanup'][key]=bad
    with self.subTest(cleanup=key),self.assertRaises(ValueError):builder.checked_prepared_planes(root,changed,dto,run)
   with self.assertRaises(ValueError):builder.checked_prepared_planes(root,{'scenarios':[]},dto,run)
   # The fixed tool has independent custody/resource/source gates. A real
   # business Start may have failed with its cleanup still unknown: that is
   # not a product ELF permit, but cannot invalidate exact fixture cleanup.
   source_identity={'members':1575,'digest':'9'*64};tools={'go':{'sha256':original['toolchainSHA256']}}
   def guarded(base,failed=False):
    return {'guard_revision':2,'environment_values_recorded':False,'allocated_root':str(base),
     'minimum_free_required_bytes':1<<30,'minimum_observed_free_bytes':2<<30,
     'max_allocated_allowed_bytes':16<<30,'maximum_observed_task_allocated_bytes':1<<20,
     'status':'failed' if failed else 'passed','exit_code':1 if failed else 0}
   all_scenarios=[dict(collection['scenarios'][0],runtimeAttempted=True,runtimeCleanupAccepted=False)]
   all_scenarios.extend({'scenario':s,'resourceStarted':False,'runtimeAttempted':False,'runtimeCleanupAccepted':False} for s in run.SCENARIOS if s!=scenario)
   actual_collection={'started':True,'accepted':False,'controllerPassed':False,'originalRetainedRuntimeJoinRequired':True,
    'containerAbsenceIsNotRuntimeDrain':True,'privateDataUploaded':False,'scenarios':all_scenarios}
   public={'source-post.json':{'accepted':True,'source':source_identity},'tools-before.json':tools,'tools-after.json':tools,
    'backend-fullgraph-controller.resource.json':guarded(root,True),'collection.json':actual_collection,
    'source-minio-build.json':original,'source-minio-post.json':{'accepted':True,'sourceDigestSHA256':original['sourceDigestSHA256']},
    'phase-0001.json':{'phase':'minio-source-compile','exitCode':0,'resource':guarded(root/'work')},
    'phase-0002.json':{'phase':'runtime-'+scenario,'exitCode':1,'resource':guarded(root/'work',True)}}
   folders=[]
   def fixture_evidence(entries,mode=None):
    compact=root/('compact-'+str(len(folders)));folders.append(compact);compact.mkdir(mode=0o700);rows=[];total=0
    for name,value in entries.items():
     raw=(json.dumps(value,separators=(',',':'))+'\n').encode();run.write(compact/name,value)
     rows.append({'path':name,'bytes':len(raw),'sha256':builder.sha(raw)});total+=len(raw)
    if mode=='row-hash':rows[0]['sha256']='f'*64
    if mode=='row-bytes':rows[0]['bytes']+=1
    if mode=='duplicate':rows.append(rows[0])
    run.write(compact/'custody.json',{'files':rows,'bodyBytes':total+(mode=='total'), 'binaries':0,'credentials':0,'privateLogs':0})
    if mode=='extra':run.write(compact/'unexpected.json',{})
    if mode=='symlink':
     path=compact/'source-post.json';raw=path.read_bytes();path.unlink();target=root/('foreign-'+str(len(folders)));target.write_bytes(raw);path.symlink_to(target)
    with mock.patch.object(run,'tool_snapshot',return_value=tools):return builder.checked_fixture_evidence(compact,{'source':source_identity},root,run)
   checked=fixture_evidence(public)
   self.assertEqual(checked['runtimeCleanupUnconfirmedScenarios'],[scenario]);self.assertFalse(checked['collection']['accepted'])
   self.assertEqual(checked['monitor']['status'],'failed')
   # Actual runtime source/phase, not only bootstrap with zero roles, is valid
   # for this fixture gate; no product-acceptance helper is consulted.
   producer_ast=ast.parse(Path(builder.__file__).read_bytes())
   seal=next(n for n in producer_ast.body if isinstance(n,ast.FunctionDef) and n.name=='prepare_fixture_artifact')
   self.assertFalse(any(isinstance(n,ast.Attribute) and n.attr in ('checked_product_evidence','checked_build_only_evidence') for n in ast.walk(seal)))
   successful=copy.deepcopy(public);successful['backend-fullgraph-controller.resource.json']=guarded(root)
   successful['phase-0002.json']={'phase':'runtime-'+scenario,'exitCode':0,'resource':guarded(root/'work')}
   successful['collection.json']['controllerPassed']=True
   self.assertEqual(fixture_evidence(successful)['monitor']['status'],'passed')
   for mode in ('row-hash','row-bytes','total','duplicate','extra','symlink'):
    with self.subTest(custody=mode),self.assertRaises(ValueError):fixture_evidence(public,mode)
   invalid=[]
   for filename,key,value in [('source-post.json','accepted',False),('source-post.json','source',{'members':1575,'digest':'8'*64}),('source-minio-post.json','accepted',False),('backend-fullgraph-controller.resource.json','status','stopped_timeout'),('backend-fullgraph-controller.resource.json','status','stopped_free_space'),('backend-fullgraph-controller.resource.json','exit_code',-15),('backend-fullgraph-controller.resource.json','minimum_observed_free_bytes',0),('backend-fullgraph-controller.resource.json','maximum_observed_task_allocated_bytes',(16<<30)+1),('backend-fullgraph-controller.resource.json','allocated_root','/foreign'),('collection.json','controllerPassed',True),('collection.json','privateDataUploaded',True)]:
    bad=copy.deepcopy(public);bad[filename][key]=value;invalid.append(bad)
   bad=copy.deepcopy(public);bad['tools-after.json']={'go':{'sha256':'f'*64}};invalid.append(bad)
   bad=copy.deepcopy(public);bad['tools-before.json']=bad['tools-after.json']={'go':{'sha256':'f'*64}};invalid.append(bad)
   bad=copy.deepcopy(public);bad['phase-0002.json']['phase']='compile-foreign';invalid.append(bad)
   bad=copy.deepcopy(public);bad['phase-0001.json']['resource']['status']='stopped_timeout';invalid.append(bad)
   bad=copy.deepcopy(public);bad['phase-0001.json']['exitCode']=1;invalid.append(bad)
   bad=copy.deepcopy(public);bad['phase-0003.json']=bad.pop('phase-0002.json');invalid.append(bad)
   bad=copy.deepcopy(public);bad['phase-0001.json']['phase']='minio-source-other';invalid.append(bad)
   bad=copy.deepcopy(public);bad.pop('source-minio-post.json');invalid.append(bad)
   for entries in invalid:
    with self.subTest(fixture_gate=len(folders)),self.assertRaises(ValueError):fixture_evidence(entries)

 def test_real_source_post_preserves_body_mode_generated_lock_and_failure_receipt(self):
  with tempfile.TemporaryDirectory() as directory:
   root=Path(directory);bodies={'server/go.mod':b'module fixture\n','server/go.sum':b'fixed lock\n','server/config/config.example.yaml':b'fixture: true\n'}
   for name,body in bodies.items():
    p=root/name;p.parent.mkdir(parents=True,exist_ok=True);p.write_bytes(body);p.chmod(0o644)
   template=bodies['server/config/config.example.yaml'];(root/'server/config/config.yaml').write_bytes(template);(root/'server/config/config.yaml').chmod(0o600)
   c={'sourceModes':dict.fromkeys(bodies,0o644)}
   with mock.patch.object(run,'verify',return_value=None) as verify:
    self.assertTrue(run.source_post(root,bodies,c,template)['accepted']);self.assertEqual(verify.call_count,1)
    (root/'server/go.sum').chmod(0o600)
    rejected=run.source_post(root,bodies,c,template);self.assertFalse(rejected['accepted']);self.assertEqual(rejected['exceptionType'],'ValueError');self.assertEqual(verify.call_count,1)
    (root/'server/go.sum').chmod(0o644);(root/'server/config/config.yaml').write_bytes(b'changed\n')
    self.assertFalse(run.source_post(root,bodies,c,template)['accepted'])
    path=root/'source-post.json';run.write(path,rejected);original=path.read_bytes()
    with self.assertRaises(FileExistsError):run.write(path,{'accepted':True})
    self.assertEqual(path.read_bytes(),original)


 def test_business_redis_policy_limits_original_principals_keys_channel_and_readonly_identity(self):
  nonce = 'a'*32
  credentials = {key: value for role in ('Admin', 'Legacy', 'Runtime') for key, value in (('redis'+role+'User', 'owned_'+role.lower()+'_'+nonce[:16]), ('redis'+role+'Password', 'b'*48))}
  rows = resources.redis_acl(credentials, nonce).decode().splitlines()
  self.assertEqual(len(rows), 4)
  self.assertEqual(rows[0], 'user default off')
  self.assertEqual([line for line in rows if '+@all' in line], [rows[1]])
  expected_keys = {'~'+prefix+user for prefix in ('USER_INFO:', 'USER_GLOBAL_RECV_MSG_OPT_KEY:', 'ACCOUNT_DISABLED:') for user in ('fullgraph_manager_'+nonce, 'fullgraph_token_'+nonce)}
  expected_keys |= {'~FCM_TOKEN:fullgraph_token_'+nonce+':5', '~cron_clear_msg_and_fix_seq', '~cron_conversations_destruct_msgs'}
  required = {'+auth', '+hello', '+ping', '+quit', '+info', '+acl|whoami', '+client|info', '+client|setinfo', '+get', '+set', '+del', '+exists', '+hget', '+hmget', '+hgetall', '+hset', '+hdel', '+expire', '+eval', '+subscribe', '+unsubscribe', '+publish'}
  for role, line in zip(('Legacy', 'Runtime'), rows[2:]):
   with self.subTest(role=role):
    words = line.split()
    self.assertEqual(words[:3], ['user', credentials['redis'+role+'User'], 'reset'])
    self.assertIn('-@all', words);self.assertIn('resetkeys', words);self.assertIn('resetchannels', words)
    self.assertEqual({v for v in words if v.startswith('+')}, required)
    self.assertEqual({v for v in words if v.startswith('~')}, expected_keys)
    self.assertEqual({v for v in words if v.startswith('&')}, {'&sensitive_word_reload'})
    self.assertFalse(any(any(c in key for c in '*?[]') for key in expected_keys))
    for denied in ('+acl', '+client', '+config', '+module', '+shutdown', '+migrate', '+replicaof', '+slaveof', '+script', '+function', '+evalsha', '+@admin', '+@dangerous', '~*', '&*'):
     self.assertNotIn(denied, words)
  for bad in ('', '../plane', 'a'*31, 'A'*32, 'a'*32+' *'):
   with self.subTest(nonce=bad), self.assertRaises(ValueError):resources.redis_acl(credentials, bad)
  changed = dict(credentials, redisLegacyUser='foreign')
  with self.assertRaises(ValueError):resources.redis_acl(changed, nonce)


 def test_actual_business_permission_evidence_requires_typed_denials_and_both_result_paths(self):
  # This is a controller receipt control, never evidence of real ACL execution.
  def row(role):return {'role':role,'redisWhoami':True,'redisClientInfo':True,'redisAclGetUserError':'NOPERM','redisConfigGetError':'NOPERM','redisForeignKeyError':'NOPERM','kafkaOriginalTopics':True,'kafkaAclDescribeError':31,'kafkaForeignTopicError':29,'kafkaConnectionClosed':True}
  evidence={'version':'owned-fullgraph-business-permissions-v1','principals':[row('legacy'),row('runtime')]}
  self.assertEqual(run.check_business_permissions(evidence),evidence)
  invalid=[None,{},dict(evidence,extra='forbidden'),dict(evidence,principals=[]),dict(evidence,principals=[row('legacy'),row('legacy')])]
  for key,value in [('redisWhoami',False),('redisClientInfo',1),('kafkaOriginalTopics',False),('kafkaConnectionClosed',False),('redisAclGetUserError','EOF'),('redisConfigGetError',None),('redisForeignKeyError','redis.Nil'),('kafkaAclDescribeError',0),('kafkaAclDescribeError',True),('kafkaForeignTopicError',3),('kafkaForeignTopicError','29'),('rawResult','forbidden')]:
   changed=copy.deepcopy(evidence);changed['principals'][1][key]=value;invalid.append(changed)
  for value in invalid:
   with self.subTest(value=value),self.assertRaises(ValueError):run.check_business_permissions(value)
  for scenario in run.SCENARIOS:
   with self.subTest(scenario=scenario),mock.patch.object(run,'check_business_permissions',wraps=run.check_business_permissions) as check:
    with self.assertRaises(ValueError):run.check_result({'businessPermissions':None},scenario,'1'*64,{},'2'*64)
    self.assertEqual(check.call_args_list,[mock.call(None)])

 def test_actual_cleanup_dispatch_and_exact_union_keep_new2_old1_all_children_and_original_graph_obligations(self):
  # Go1.27 build failures use ImportPath/build-output, outside runtime output.
  public='public/source.go:158: snapshot.Identity().CapturedAt undefined\n'
  rows=[{'Action':'build-output','ImportPath':'public/package','Output':public},
        {'Action':'output','Output':'RUNTIME_PRIVATE_UNSCOPED'},
        {'Action':'output','Test':'TestRuntime','Output':'RUNTIME_PRIVATE_TEST'},
        {'Action':'build-output','Test':'TestRuntime','Output':'NOT_A_COMPILER_EVENT'}]
  raw=('\n'.join(json.dumps(row) for row in rows)+'\n').encode()
  for phase in ['pending-two-original-oracles','launcher-ownedcontrol','redis-parser','kafka-acl-response']+[scope['label'] for scope in run.CLEANUP_SCOPES]:
   projected,diagnostic=run.project_go_test_events(raw,phase)
   self.assertEqual(diagnostic,{'phase':phase,'publicSourceOnly':True,'selection':'go-test-build-output-without-Test-only','completeBytes':len(public.encode()),'completeSHA256':hashlib.sha256(public.encode()).hexdigest(),'diagnostic':public,'truncated':False})
   self.assertTrue(all('Output' not in row and 'ImportPath' not in row for row in projected))
   saved=json.dumps({'events':projected,'diagnostic':diagnostic})
   for private in ('RUNTIME_PRIVATE_UNSCOPED','RUNTIME_PRIVATE_TEST','NOT_A_COMPILER_EVENT'):self.assertNotIn(private,saved)
  runtime=('\n'.join(json.dumps(row) for row in rows[1:])+'\n').encode()
  self.assertIsNone(run.project_go_test_events(runtime,'cleanup-runtimeacceptance')[1])
  large=public+'界'*23000
  bounded=json.dumps({'Action':'build-output','ImportPath':'public/package','Output':large}).encode()
  _,diagnostic=run.project_go_test_events(bounded,'cleanup-runtimeacceptance')
  self.assertTrue(diagnostic['truncated']);self.assertLessEqual(len(diagnostic['diagnostic'].encode()),65536)
  self.assertEqual(diagnostic['completeBytes'],len(large.encode()));self.assertEqual(diagnostic['completeSHA256'],hashlib.sha256(large.encode()).hexdigest())
  self.assertTrue(large.startswith(diagnostic['diagnostic']))
  with self.assertRaises(ValueError):run.project_go_test_events(raw,'runtime-ack')
  with self.assertRaises(ValueError):run.project_go_test_events(json.dumps({'Action':'build-output','Output':7}).encode(),'cleanup-runtimeacceptance')
  syntax=ast.parse(Path(run.__file__).read_bytes());orchestration=next(node for node in syntax.body if isinstance(node,ast.FunctionDef) and node.name=='run')
  calls=[node for node in ast.walk(orchestration) if isinstance(node,ast.Call)]
  cleanup_line=next(node.lineno for node in calls if isinstance(node.func,ast.Name) and node.func.id=='load_cleanup_reuse')
  self.assertFalse(any(isinstance(n.func,ast.Name) and n.func.id=='run_cleanup_controls' for n in calls))
  compile_line=next(node.lineno for node in calls if isinstance(node.func,ast.Name) and node.func.id=='command' and node.args and isinstance(node.args[0],ast.BinOp) and isinstance(node.args[0].left,ast.Constant) and node.args[0].left.value=='compile-')
  minio_line=next(node.lineno for node in calls if isinstance(node.func,ast.Attribute) and isinstance(node.func.value,ast.Name) and node.func.value.id=='builder' and node.func.attr=='build')
  launcher_line=next(node.lineno for node in calls if isinstance(node.func,ast.Name) and node.func.id=='load_launcher_reuse')
  self.assertFalse(any(isinstance(node.func,ast.Name) and node.func.id=='run_launcher_control' for node in calls))
  self.assertLess(launcher_line,cleanup_line);self.assertLess(cleanup_line,compile_line);self.assertLess(cleanup_line,minio_line)
  summary_node=next(node.args[1] for node in calls if isinstance(node.func,ast.Name) and node.func.id=='write' and isinstance(node.args[0],ast.BinOp) and isinstance(node.args[0].right,ast.Constant) and node.args[0].right.value=='summary.json')
  summary_fields={key.value:value for key,value in zip(summary_node.keys,summary_node.values)}
  self.assertEqual(ast.literal_eval(summary_fields['targetedTopPASS']),2);self.assertEqual(ast.literal_eval(summary_fields['launcherTopPASS']),0);self.assertEqual(ast.literal_eval(summary_fields['actualLauncherTestsDispatched']),0)

  with tempfile.TemporaryDirectory() as directory:
   out=Path(directory);reused=run.load_cleanup_reuse(out)
   self.assertEqual(reused,run.CLEANUP_PASSED_REUSE)
   self.assertEqual(json.loads((out/'cleanup-passed-reuse.json').read_bytes()),reused)
  original_names=[{'package':pkg,'name':top} for pkg,tops in run.GRAPH_TARGETS.items() for top in tops]
  original_names += [{'package':pkg,'name':top+'/'+child} for pkg,tops in run.GRAPH_TARGETS.items() for top in tops for child in run.GRAPH_CHILDREN[top]]
  original={'namedCompletions':original_names}
  fresh=run.audit_current_oracles(original,reused)
  self.assertEqual((fresh['topPASS'],fresh['subPASS'],fresh['actualCleanupTestsDispatched']),(2,3,0))
  self.assertEqual(fresh['namedCompletions'],original_names)
  self.assertEqual(fresh['completeObligation'],{'topCount':7,'subCount':16,'crossSource':True})
  with self.assertRaises(ValueError):run.audit_current_oracles(original,dict(reused,reviewSHA256='f'*64))
  # The exact collector helper keeps closed fixture observations on failure,
  # never arbitrary runtime Output, error strings, paths or foreign test data.
  top='TestFullgraphEarlyFailureKeepsOriginalChildrenForFinalCleanup';package='github.com/openimsdk/open-im-server/v3/internal/runtimeacceptance';marker='LUOBO_RUNTIME_CLEANUP_DIAG_V1 '
  kinds=['cancel-audit-before-stop','timeout-before-local-wait-complete']
  def observation(kind):return {'version':'runtime-cleanup-fixture-observation-v1','kind':kind,'stage':'assert-complete','errorClass':'deadline','shortErrorClass':'unobserved','role':'none','binaryBytes':12345,'childReturned':True,'finalReturned':True,'observedExits':1,'complete':False,'launcherClosed':False,'signal0':'seen','signal1':'one','primaryPreserved':True,'elapsedMicros':8000000,'firstStopMicros':0,'finalCleanupMicros':3000000}
  def frame(value,test=None,pkg=package,action='output'):
   return {'Action':action,'Package':pkg,'Test':test or top+'/'+value['kind'],'Output':marker+json.dumps(value,separators=(',',':'))+'\n'}
  def encoded(rows):return ('\n'.join(json.dumps(row) for row in rows)+'\n').encode()
  valid=[frame(observation(kind)) for kind in kinds]
  foreign=[dict(valid[0],Package='foreign',Output=marker+'PRIVATE_FOREIGN\n'),dict(valid[0],Test='TestForeign',Output=marker+'PRIVATE_TEST\n'),dict(valid[0],Output='PRIVATE_RUNTIME_OUTPUT\n')]
  result=run.runtime_cleanup_observations(encoded(valid+foreign),'cleanup-runtimeacceptance')
  self.assertTrue(result['diagnosticsComplete']);self.assertEqual(result['rejectedMarkers'],0);self.assertEqual(result['observations'],[observation(kind) for kind in kinds])
  self.assertNotIn('PRIVATE_',json.dumps(result));self.assertLessEqual(len(json.dumps(result).encode()),4096)
  self.assertFalse(run.runtime_cleanup_observations(encoded(valid[:1]),'cleanup-runtimeacceptance')['diagnosticsComplete'])
  for key,value in [('rawError','PRIVATE_URL_TOKEN'),('errorClass','PRIVATE_PATH'),('stage','unknown'),('binaryBytes',True),('binaryBytes',(1<<30)+1),('observedExits',3),('complete',1),('elapsedMicros',180000001),('signal0','PRIVATE_NONCE')]:
   bad=observation(kinds[0]);bad[key]=value
   rejected=run.runtime_cleanup_observations(encoded([frame(bad),valid[1]]),'cleanup-runtimeacceptance')
   self.assertFalse(rejected['diagnosticsComplete']);self.assertEqual(rejected['rejectedMarkers'],1);self.assertNotIn('PRIVATE_',json.dumps(rejected))
  for bad in [dict(valid[0],Output=marker+'{"version":'),dict(valid[0],Output=marker+'x'*2049+'\n'),dict(valid[0],Test=top+'/'+kinds[1]),dict(valid[0],Output=marker+'{"kind":"a","kind":"b"}\n')]:
   rejected=run.runtime_cleanup_observations(encoded([bad,valid[1]]),'cleanup-runtimeacceptance');self.assertFalse(rejected['diagnosticsComplete']);self.assertGreater(rejected['rejectedMarkers'],0)
  self.assertFalse(run.runtime_cleanup_observations(encoded(valid+[valid[0]]),'cleanup-runtimeacceptance')['diagnosticsComplete'])
  with self.assertRaises(ValueError):run.runtime_cleanup_observations(encoded(valid),'runtime-ack')
  # The actual command writes safe observations before its latched failure raise.
  command_node=next(node for node in ast.walk(orchestration) if isinstance(node,ast.FunctionDef) and node.name=='command')
  observed_write=next(node.lineno for node in ast.walk(command_node) if isinstance(node,ast.Call) and isinstance(node.func,ast.Name) and node.func.id=='write' and any(isinstance(part,ast.Constant) and part.value=='runtime-cleanup-observations.json' for arg in node.args for part in ast.walk(arg)))
  failed_raise=next(node.lineno for node in ast.walk(command_node) if isinstance(node,ast.Raise) and any(isinstance(part,ast.Constant) and part.value=='actual phase failed; private raw retained' for part in ast.walk(node)))
  self.assertLess(observed_write,failed_raise)
  self.assertFalse(any(isinstance(node,ast.Raise) and any(isinstance(part,ast.Constant) and part.value=='both exact bounded cleanup fixture observations required' for part in ast.walk(node)) for node in ast.walk(command_node)))
  parent=run.module('ci/backend-runtime-join-r7/run.py','new_fullgraph_cleanup_original_audit')
  def events(targets,required):
   rows=[]
   for pkg,tops in targets.items():
    full=parent.PREFIX+pkg;rows.append({'Action':'start','Package':full})
    for top in tops:
     rows.append({'Action':'run','Package':full,'Test':top})
     for child in required.get(pkg,{}).get(top,[]):rows.extend([{'Action':'run','Package':full,'Test':top+'/'+child},{'Action':'pass','Package':full,'Test':top+'/'+child}])
     rows.append({'Action':'pass','Package':full,'Test':top})
    rows.append({'Action':'pass','Package':full})
   return rows
  # This exact source-only ACL top reuses the same owned Go cache/environment.
  control=run.KAFKA_ACL_CONTROL;package='internal/namespaceimport/ownedcontrol';top='TestOwnedKafkaExclusionRequiresEveryWildcardDeny'
  self.assertEqual(control,{'phase':'kafka-acl-response','targets':{package:[top]},'requiredSubtests':{package:{top:['create-reply-error-and-shape-cannot-be-a-success','describe-reply-error-and-shape-cannot-replace-exact-deny']}},'goTimeoutSeconds':180,'phaseGuardSeconds':240,'newDatabaseFixtures':0,'otherKafkaOrRedisTestsDispatched':False})
  kafka_line=next(node.lineno for node in calls if isinstance(node.func,ast.Name) and node.func.id=='load_kafka_acl_reuse')
  self.assertLess(cleanup_line,kafka_line);self.assertLess(kafka_line,compile_line)
  self.assertFalse(any(isinstance(node.func,ast.Name) and node.func.id=='run_kafka_acl_control' for node in calls))
  self.assertEqual(ast.literal_eval(summary_fields['actualKafkaACLTestsDispatched']),0)
  locked=json.loads(run.INPUTS.read_bytes());run.check_locked(locked)
  changed=copy.deepcopy(locked);changed['targetedKafkaACLControl']['requiredSubtests'][package][top]=[]
  with self.assertRaises(ValueError):run.check_locked(changed)
  for mode in ('complete','missing-child','skip','foreign-top','missing-footer','phase-error'):
   with self.subTest(kafkaACL=mode),tempfile.TemporaryDirectory() as directory:
    out=Path(directory).resolve();seen=[];environment={'GOPROXY':'off','GOSUMDB':'off','GOCACHE':'/owned/buildcache','GOMODCACHE':'/owned/modcache'}
    def acl_command(label,argv,seconds,actual_environment):
     seen.append(label);self.assertEqual(label,'kafka-acl-response');self.assertEqual(seconds,240);self.assertIs(actual_environment,environment)
     self.assertEqual(argv,['/owned/go','test','-mod=readonly','-race','-p=1','-parallel=1','-count=1','-json','-vet=off','-timeout=180s','-run','^'+top+'$','./'+package])
     if mode=='phase-error':raise ValueError('actual phase failed; private raw retained')
     rows=events(control['targets'],control['requiredSubtests'])
     if mode=='missing-child':rows=[row for row in rows if row.get('Test')!=top+'/create-reply-error-and-shape-cannot-be-a-success']
     if mode=='skip':rows=[dict(row,Action='skip') if row.get('Test')==top+'/describe-reply-error-and-shape-cannot-replace-exact-deny' and row['Action']=='pass' else row for row in rows]
     if mode=='foreign-top':rows.insert(1,{'Action':'run','Package':parent.PREFIX+package,'Test':'TestForeign'})
     if mode=='missing-footer':rows=[row for row in rows if not(row['Action']=='pass' and 'Test'not in row)]
     return encoded(rows)
    if mode!='complete':
     with self.assertRaises(ValueError):run.run_kafka_acl_control('/owned/go',environment,acl_command,parent,out)
     self.assertEqual(seen,['kafka-acl-response']);self.assertFalse((out/'kafka-acl-response-names.json').exists());continue
    actual=run.run_kafka_acl_control('/owned/go',environment,acl_command,parent,out)
    self.assertEqual((actual['source'],actual['topPASS'],actual['subPASS'],actual['skip'],actual['fail']),(run.FINAL_SOURCE,1,2,0,0))
    self.assertEqual(len(actual['namedCompletions']),3);self.assertEqual(seen,['kafka-acl-response'])
    self.assertEqual(json.loads((out/'kafka-acl-response-names.json').read_bytes()),actual)

  # Exercise the real dispatch and original Go JSON auditor, without Go/DB.
  for mode in ('complete','missing-child','skip','foreign-top','missing-footer','phase-error'):
   with self.subTest(redisParser=mode),tempfile.TemporaryDirectory() as directory:
    out=Path(directory).resolve();calls=[];environment={'GOPROXY':'off','GOSUMDB':'off','GOCACHE':'/owned/buildcache','GOMODCACHE':'/owned/modcache'}
    def parser_command(label,argv,seconds,actual_environment):
     calls.append(label);self.assertEqual(label,'redis-parser');self.assertEqual(seconds,240);self.assertIs(actual_environment,environment)
     top='TestOwnedRedisCredentialNormalizesRESP2RESP3WithoutWeakeningDisableProof';package='internal/namespaceimport/ownedcontrol'
     self.assertEqual(argv,['/owned/go','test','-mod=readonly','-race','-p=1','-parallel=1','-count=1','-json','-vet=off','-timeout=180s','-run','^'+top+'$','./'+package])
     if mode=='phase-error':raise ValueError('actual phase failed; private raw retained')
     rows=events(run.REDIS_PARSER_CONTROL['targets'],run.REDIS_PARSER_CONTROL['requiredSubtests'])
     if mode=='missing-child':rows=[row for row in rows if row.get('Test')!=top+'/equivalent-protocols-complete-summary-and-real-disabled-state']
     if mode=='skip':
      rows=[dict(row,Action='skip') if row.get('Test')==top+'/malformed-fields-and-nil-first-duplicate-reject' and row['Action']=='pass' else row for row in rows]
     if mode=='foreign-top':rows.insert(1,{'Action':'run','Package':parent.PREFIX+package,'Test':'TestForeign'})
     if mode=='missing-footer':rows=rows[:-1]
     return ('\n'.join(json.dumps(row) for row in rows)+'\n').encode()
    if mode!='complete':
     with self.assertRaises(ValueError):run.run_redis_parser_control('/owned/go',environment,parser_command,parent,out)
     self.assertEqual(calls,['redis-parser']);self.assertFalse((out/'redis-parser-names.json').exists());continue
    result=run.run_redis_parser_control('/owned/go',environment,parser_command,parent,out)
    self.assertEqual(calls,['redis-parser']);self.assertEqual((result['topPASS'],result['subPASS']),(1,3));self.assertTrue(result['accepted'])
    self.assertEqual(result['source'],run.FINAL_SOURCE);self.assertEqual(result['newDatabaseFixtures'],0);self.assertFalse(result['otherR8TargetsDispatched'])
    self.assertEqual(json.loads((out/'redis-parser-names.json').read_bytes()),result)
  parser_line=next(node.lineno for node in [node for node in ast.walk(orchestration) if isinstance(node,ast.Call)] if isinstance(node.func,ast.Name) and node.func.id=='load_redis_parser_reuse')
  self.assertFalse(any(isinstance(node.func,ast.Name) and node.func.id=='run_redis_parser_control' for node in ast.walk(orchestration) if isinstance(node,ast.Call)))
  self.assertLess(parser_line,compile_line);self.assertLess(parser_line,minio_line)
  # Keep the original parser dispatch positive/negative controls above. Only
  # runtime orchestration changes: exact old-source reuse with real body pins.
  parser_bodies={row['target']:run.regular(run.REPO/'ci/candidates/backend-fullgraph/source'/row['target']) for row in run.REDIS_PARSER_PASSED_REUSE['sourcePins']}
  with tempfile.TemporaryDirectory() as directory:
   out=Path(directory).resolve()
   with mock.patch.object(run,'run_redis_parser_control',side_effect=AssertionError('no repeated Go parser test')) as dispatch:
    receipt=run.load_redis_parser_reuse(out,parser_bodies);dispatch.assert_not_called()
   self.assertEqual(receipt,run.REDIS_PARSER_PASSED_REUSE);self.assertEqual((receipt['topPASS'],receipt['subPASS'],receipt['skip'],receipt['fail']),(1,3,0,0))
   self.assertEqual(receipt['source'],{'members':1576,'digest':'122c6e2d1b158bbe9addd879c733ef85bf6c2b26a345f583aa4b5a31d2260f48'})
   self.assertFalse(receipt['currentSourcePASS']);self.assertFalse(receipt['runtimeAccepted']);self.assertEqual(receipt['actualRedisParserTestsDispatched'],0)
   self.assertEqual(json.loads((out/'redis-parser-passed-reuse.json').read_bytes()),receipt);self.assertFalse((out/'redis-parser-names.json').exists())
   for mode in ('source','pin','missing-name','duplicate-name','foreign-name','fresh-claim','bool-count'):
    bad=copy.deepcopy(receipt)
    if mode=='source':bad['source']['digest']='0'*64
    elif mode=='pin':bad['sourcePins'][0]['sha256']='0'*64
    elif mode=='missing-name':bad['namedCompletions'].pop()
    elif mode=='duplicate-name':bad['namedCompletions'].append(copy.deepcopy(bad['namedCompletions'][0]))
    elif mode=='foreign-name':bad['namedCompletions'][0]['name']='TestForeign'
    elif mode=='fresh-claim':bad['currentSourcePASS']=True
    else:bad['topPASS']=True
    with self.subTest(redisReuse=mode),self.assertRaises(ValueError):run.check_redis_parser_reuse(bad,parser_bodies)
   for target,raw in parser_bodies.items():
    changed=dict(parser_bodies);changed[target]=raw+b'changed'
    with self.subTest(redisSource=target),self.assertRaises(ValueError):run.check_redis_parser_reuse(receipt,changed)
  for mode in ('complete','missing-child','foreign-top'):
   with self.subTest(mode=mode),tempfile.TemporaryDirectory() as folder:
    out=Path(folder).resolve();calls=[]
    def command(label,argv,seconds,environment):
     scope=run.CLEANUP_SCOPES[len(calls)];calls.append(label)
     self.assertEqual(label,scope['label']);self.assertEqual(seconds,240);self.assertEqual(environment,{'GOPROXY':'off'})
     self.assertEqual(argv[:3],['/owned/go','test','-mod=readonly']);self.assertIn('-race',argv);self.assertIn('-timeout=180s',argv)
     self.assertEqual([word for word in argv if word.startswith('./')],['./'+pkg for pkg in scope['targets']])
     top_pattern,child_pattern=[__import__('re').compile(part) for part in argv[argv.index('-run')+1].split('/')]
     self.assertTrue(all(top_pattern.fullmatch(top) for tops in scope['targets'].values() for top in tops))
     self.assertTrue(all(child_pattern.fullmatch(child) for tops in scope['requiredSubtests'].values() for subs in tops.values() for child in subs))
     self.assertTrue(all(top_pattern.fullmatch(top) is None for tops in run.GRAPH_TARGETS.values() for top in tops))
     self.assertIsNone(child_pattern.fullmatch('unbound_start_failure_only_waits_original_done'))
     if label=='cleanup-ownedcontrol':
      self.assertEqual(scope['targets'],{'internal/namespaceimport/ownedcontrol':['TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths']})
      self.assertEqual(scope['requiredSubtests']['internal/namespaceimport/ownedcontrol']['TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths'],['local-first'])
      for old_child in ('canonical-first','runtime-first'):self.assertIsNone(child_pattern.fullmatch(old_child))
     rows=events(scope['targets'],scope['requiredSubtests'])
     if mode=='missing-child' and len(calls)==1:
      removed=next(row['Test'] for row in rows if row.get('Test') and '/' in row['Test']);rows=[row for row in rows if row.get('Test')!=removed]
     if mode=='foreign-top':rows.insert(1,{'Action':'run','Package':parent.PREFIX+'internal/namespaceimport/ownedcontrol','Test':'TestForeign'})
     return ('\n'.join(json.dumps(row) for row in rows)+'\n').encode()
    if mode!='complete':
     with self.assertRaises(ValueError):run.run_cleanup_controls('/owned/go',{'GOPROXY':'off'},command,parent,out)
     self.assertFalse((out/'cleanup-names.json').exists());continue
    result=run.run_cleanup_controls('/owned/go',{'GOPROXY':'off'},command,parent,out)
    self.assertEqual(calls,[scope['label'] for scope in run.CLEANUP_SCOPES]);self.assertEqual((result['topPASS'],result['subPASS']),(2,3))
    original_required={pkg:{top:run.GRAPH_CHILDREN[top] for top in tops} for pkg,tops in run.GRAPH_TARGETS.items()}
    original=parent.audit(events(run.GRAPH_TARGETS,original_required),0,run.GRAPH_TARGETS)
    union=run.audit_target_union(original,result);self.assertEqual((union['topPASS'],union['subPASS']),(4,6))
    self.assertEqual((union['cleanupRecentReuse']['topPASS'],union['cleanupRecentReuse']['subPASS']),(3,9));self.assertNotIn({'package':'internal/namespaceimport/ownedcontrol','name':'TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths'},union['cleanupRecentReuse']['namedCompletions'])
    self.assertEqual(union['cleanupReuse']['source'],run.CLEANUP_REUSE['source']);self.assertEqual((union['cleanupReuse']['topPASS'],union['cleanupReuse']['subPASS']),(0,1));self.assertEqual(union['originalObligation'],{'topCount':5,'subCount':12,'crossSource':True});self.assertNotIn('ownedcontrolReuse',union);self.assertEqual(union['newSafety'],{'topCount':2,'subCount':4,'crossSource':True});self.assertEqual(union['completeObligation'],{'topCount':7,'subCount':16,'crossSource':True})
    bad=copy.deepcopy(result);bad['namedCompletions'].pop()
    with self.assertRaises(ValueError):run.audit_target_union(original,bad)
    bad=copy.deepcopy(result);bad['namedCompletions'].append(bad['namedCompletions'][0])
    with self.assertRaises(ValueError):run.audit_target_union(original,bad)
  with tempfile.TemporaryDirectory() as folder:
   command=mock.Mock(side_effect=ValueError('actual phase failed; private raw retained'))
   with self.assertRaises(ValueError):run.run_cleanup_controls('/owned/go',{},command,parent,Path(folder).resolve())
   self.assertEqual(command.call_count,1)

  launcher_targets={'internal/namespaceimport/ownedcontrol':['TestOwnedLauncherHoldPersistsAndCannotPrepareMetadata']}
  for mode in ('complete','missing-pass','extra-child','foreign-top','phase-error'):
   with self.subTest(launcher=mode),tempfile.TemporaryDirectory() as folder:
    out=Path(folder).resolve();calls=[]
    def launcher_command(label,argv,seconds,environment):
     calls.append(label);self.assertEqual(label,'launcher-ownedcontrol');self.assertEqual(seconds,240);self.assertEqual(environment,{'GOPROXY':'off'})
     self.assertEqual(argv,['/owned/go','test','-mod=readonly','-race','-p=1','-parallel=1','-count=1','-json','-vet=off','-timeout=180s','-run','^TestOwnedLauncherHoldPersistsAndCannotPrepareMetadata$','./internal/namespaceimport/ownedcontrol'])
     if mode=='phase-error':raise ValueError('actual phase failed; private raw retained')
     required={} if mode!='extra-child' else {'internal/namespaceimport/ownedcontrol':{'TestOwnedLauncherHoldPersistsAndCannotPrepareMetadata':['unexpected']}}
     rows=events(launcher_targets,required)
     if mode=='missing-pass':rows=[row for row in rows if not (row.get('Test') and row['Action']=='pass')]
     if mode=='foreign-top':rows.insert(1,{'Action':'run','Package':parent.PREFIX+'internal/namespaceimport/ownedcontrol','Test':'TestForeign'})
     return ('\n'.join(json.dumps(row) for row in rows)+'\n').encode()
    if mode!='complete':
     with self.assertRaises(ValueError):run.run_launcher_control('/owned/go',{'GOPROXY':'off'},launcher_command,parent,out)
     self.assertEqual(calls,['launcher-ownedcontrol']);self.assertFalse((out/'launcher-names.json').exists());continue
    launcher=run.run_launcher_control('/owned/go',{'GOPROXY':'off'},launcher_command,parent,out)
    self.assertEqual((launcher['source'],launcher['topPASS'],launcher['subPASS'],launcher['skip'],launcher['fail']),(run.FINAL_SOURCE,1,0,0,0));self.assertEqual(calls,['launcher-ownedcontrol'])
    self.assertEqual(json.loads((out/'launcher-names.json').read_bytes()),launcher)
    collector=load('collect')
    # A fresh-looking names file is never substituted for old-source custody.
    with self.assertRaises(FileNotFoundError):collector.checked_launcher_evidence(out)
    invalid=[dict(launcher,source={'members':1575,'digest':'f'*64}),dict(launcher,topPASS=True),dict(launcher,subPASS=1),dict(launcher,skip=1),dict(launcher,fail=1),dict(launcher,namedCompletions=[]),dict(launcher,namedCompletions=launcher['namedCompletions']*2),dict(launcher,extra='private')]
    for value in invalid:
     with self.assertRaises(ValueError):run.check_launcher_names(value)
    with mock.patch.object(run,'run_launcher_control',side_effect=AssertionError('Go must not be dispatched')) as dispatch:
     prior=run.load_launcher_reuse(out);dispatch.assert_not_called()
    self.assertEqual(prior,run.LAUNCHER_PASSED_REUSE);self.assertEqual(json.loads((out/'launcher-passed-reuse.json').read_bytes()),prior)
    self.assertNotEqual(prior['source'],run.FINAL_SOURCE);self.assertFalse(prior['currentSourcePASS']);self.assertEqual((prior['run'],prior['job'],prior['head']),(39075035152,117520306290,'a6d9276c53a75e8ca19e351cab1d34b419b560e7'))
    self.assertEqual(collector.checked_launcher_evidence(out),prior)
    rejected=[dict(prior,source=run.FINAL_SOURCE),dict(prior,run=1),dict(prior,job=1),dict(prior,head='f'*40),dict(prior,topPASS=True),dict(prior,topPASS=1.0),dict(prior,namedCompletions=[]),dict(prior,currentSourcePASS=True),dict(prior,extra='private')]
    for value in rejected:
     with self.assertRaises(ValueError):run.check_launcher_reuse(value)
    # Runtime failure preserves only the real historical receipt; current
    # source/runtime still cannot pass. No resource or Go command is invoked.
    with tempfile.TemporaryDirectory() as directory:
     repo=Path(directory).resolve();allocation=repo/'allocation';allocation.mkdir(mode=0o700);(allocation/'work').mkdir(mode=0o700)
     owner={'fixture':'original-owner'};run.write(allocation/'allocation-owner.json',owner)
     target=repo/'ci-results/backend-fullgraph';target.mkdir(parents=True);run.write(target/'launcher-passed-reuse.json',prior);run.write(target/'summary.json',{'accepted':False})
     with mock.patch.object(collector.run,'REPO',repo),mock.patch.object(collector.run,'allocation_root',return_value=allocation),mock.patch.object(collector.run,'job_identity',return_value=owner),mock.patch.object(collector.run,'module',return_value=object()),mock.patch.object(collector,'pack'):
      self.assertEqual(collector.collect(),1)
     observed=json.loads((target/'collection.json').read_bytes());self.assertTrue(observed['launcherReuseAccepted']);self.assertFalse(observed['accepted']);self.assertEqual(json.loads((target/'launcher-passed-reuse.json').read_bytes()),prior)
    (out/'launcher-passed-reuse.json').unlink()
    with self.assertRaises(FileNotFoundError):collector.checked_launcher_evidence(out)

 def test_unbound_child_reuse_never_supplies_a_current_parent_or_changed_cleanup(self):
  def names(targets,children):
   rows=[]
   for pkg,tops in targets.items():
    for top in tops:
     rows.append({'package':pkg,'name':top})
     rows.extend({'package':pkg,'name':top+'/'+child} for child in children.get(pkg,{}).get(top,[]))
   return rows
  cleanup={'namedCompletions':[row for scope in run.CLEANUP_SCOPES for row in names(scope['targets'],scope['requiredSubtests'])]}
  original={'namedCompletions':names(run.GRAPH_TARGETS,{pkg:{top:run.GRAPH_CHILDREN[top] for top in tops} for pkg,tops in run.GRAPH_TARGETS.items()})}
  result=run.audit_target_union(original,cleanup)
  self.assertEqual((result['topPASS'],result['subPASS']),(4,6));self.assertEqual(len(result['namedCompletions']),10)
  self.assertEqual((result['cleanupReuse']['topPASS'],result['cleanupReuse']['subPASS']),(0,1));self.assertEqual(len(result['cleanupReuse']['namedCompletions']),1)
  reused=result['cleanupReuse']['namedCompletions'][0]
  self.assertTrue(reused['name'].endswith('/unbound_start_failure_only_waits_original_done'))
  self.assertNotIn(reused,result['namedCompletions']);self.assertNotIn('ownedcontrolReuse',result)
  self.assertEqual((result['cleanupRecentReuse']['topPASS'],result['cleanupRecentReuse']['subPASS']),(3,9))
  self.assertEqual(len(result['cleanupRecentReuse']['namedCompletions']),12)
  self.assertTrue(all(row not in result['namedCompletions'] for row in result['cleanupRecentReuse']['namedCompletions']))
  self.assertEqual(result['originalObligation'],{'topCount':5,'subCount':12,'crossSource':True});self.assertEqual(result['completeObligation'],{'topCount':7,'subCount':16,'crossSource':True})
  bad=copy.deepcopy(cleanup);bad['namedCompletions'].append(reused)
  with self.assertRaises(ValueError):run.audit_target_union(original,bad)
  for removed in ('TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths/local-first','TestFullgraphEarlyFailureKeepsOriginalChildrenForFinalCleanup/cancel-audit-before-stop'):
   bad=copy.deepcopy(cleanup);bad['namedCompletions']=[row for row in bad['namedCompletions'] if row['name']!=removed]
   # Old receipts remain historical facts; they cannot fill a current-source gap.
   bad['priorSourcePassed']=[{'package':run.CLEANUP_REUSE['package'],'name':removed}]
   with self.assertRaises(ValueError):run.audit_target_union(original,bad)
  bad=copy.deepcopy(run.CLEANUP_RECENT_REUSE)
  bad['namedCompletions'].append({'package':'internal/namespaceimport/ownedcontrol','name':'TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths'})
  with mock.patch.object(run,'CLEANUP_RECENT_REUSE',bad),self.assertRaises(ValueError):run.audit_target_union(original,cleanup)

 def test_closed_console_observation_retains_failed_stage_without_private_fields_or_acceptance(self):
  collector=load('collect');scenario=run.SCENARIOS[1]
  terminal={'version':'owned-fullgraph-token-v2-result-v1','scenario':scenario,'accepted':False,'phase':'actual-resource-bootstrap','bootstrapStage':'kafka-coordinator','bootstrapFailureTag':'kafka-coordinator-unavailable','failureClass':'source-error-private','failureSHA256':'a'*64,'sourceSHA256':'','resourcePlaneSHA256':'b'*64,'actualStartedRoles':0,'rawError':'PRIVATE_RAW_ERROR'}
  preparation={'version':'owned-fullgraph-resource-prepare-observation-v1','scenario':scenario,'role':'kafka-format','step':'format-attach','callState':'returned','errorClass':'command-nonzero','cliExitCode':1,'rawErrorText':False}
  rows=[{'scenario':s,'resourceStarted':s==scenario,'runtimeAttempted':False,'runtimeCleanupAccepted':False,'privatePath':'PRIVATE_PATH','exceptionType':'PRIVATE_CLASS'} for s in run.SCENARIOS]
  rows[1]['resourcePreparation']=preparation
  monitor={'status':'failed','exit_code':1,'command':['PRIVATE_ARGV'],'log':'PRIVATE_PATH'}
  phases=[{'phase':'runtime-'+scenario,'exitCode':1,'resource':{'status':'failed','command':['PRIVATE_ARGV']},'rawCustody':'PRIVATE_CAPTURE'}]
  summary=collector.console_observation(rows,monitor,{'accepted':False},{scenario:terminal},phases)
  self.assertEqual(summary['controllerStatus'],'failed');self.assertEqual(summary['controllerExitCode'],1)
  self.assertEqual(summary['lastPhase'],{'phase':'runtime','role':'none','scenario':scenario,'exitCode':1,'resourceStatus':'failed'})
  self.assertEqual(summary['scenarios'][1]['resourcePreparation']['errorClass'],'command-nonzero')
  self.assertEqual(summary['scenarios'][1]['runtime']['bootstrapFailureTag'],'kafka-coordinator-unavailable')
  self.assertFalse(summary['acceptanceEvidence']);self.assertFalse(summary['rawErrorText']);self.assertNotIn('PRIVATE_',json.dumps(summary))
  self.assertLessEqual(len(json.dumps(summary,separators=(',',':')).encode()),8192)
  # Unknown status/command labels cannot be echoed as newly invented enums.
  foreign=collector.console_observation(rows,{'status':'PRIVATE_STATUS','exit_code':True},{},{scenario:dict(terminal,phase='private-stage-secret',bootstrapStage='',bootstrapFailureTag='')},[{'phase':'compile-PRIVATE_USER','exitCode':256,'resource':{'status':'PRIVATE_STATUS'}}])
  self.assertEqual(foreign['controllerStatus'],'unobserved');self.assertIsNone(foreign['controllerExitCode'])
  self.assertEqual(foreign['lastPhase']['phase'],'unobserved');self.assertIsNone(foreign['lastPhase']['exitCode'])
  self.assertNotIn('private-stage-secret',json.dumps(foreign));self.assertNotIn('PRIVATE_',json.dumps(foreign))
  for changed in (dict(preparation,role='PRIVATE_USER'),dict(preparation,step='/PRIVATE_PATH'),dict(preparation,rawErrorText=True)):
   bad=copy.deepcopy(rows);bad[1]['resourcePreparation']=changed
   with self.assertRaises(ValueError):collector.console_observation(bad,monitor,{}, {},phases)
  with self.assertRaises(ValueError):collector.console_observation(rows[:-1],monitor,{}, {},phases)
  # Actual always collector emits this projection even when no runtime started.
  with tempfile.TemporaryDirectory() as directory:
   root=Path(directory).resolve();allocation=root/'allocation';allocation.mkdir(mode=0o700)
   owner={'fixture':'original-owned-allocation'};run.write(allocation/'allocation-owner.json',owner)
   output=io.StringIO()
   with mock.patch.object(collector.run,'REPO',root),mock.patch.object(collector.run,'allocation_root',return_value=allocation),mock.patch.object(collector.run,'job_identity',return_value=owner),mock.patch.object(collector.run,'module',return_value=resources),mock.patch.object(collector,'pack') as pack,mock.patch('sys.stdout',new=output):
    self.assertEqual(collector.collect(),1)
   observed=json.loads(output.getvalue());self.assertEqual(observed['event'],'backend_graph_observation')
   self.assertTrue(observed['summaryAvailable']);self.assertFalse(observed['mainAccepted'])
   self.assertEqual([r['scenario'] for r in observed['scenarios']],list(run.SCENARIOS))
   self.assertTrue(all(not r['runtimeAttempted'] and not r['runtimeCleanupAccepted'] for r in observed['scenarios']))
   pack.assert_called_once_with(root/'ci-results/backend-fullgraph')
   # Summary I/O cannot replace a collector outcome or expose an exception.
   with mock.patch('sys.stdout',new=io.StringIO()) as stream:
    collector.emit_console_observation(rows,monitor,{}, {},root/'absent')
    self.assertNotIn('PRIVATE_',stream.getvalue())
   for error in (BrokenPipeError('PRIVATE_CHANNEL'),ValueError('PRIVATE_CHANNEL')):
    with mock.patch('builtins.print',side_effect=error):
     collector.emit_console_observation(rows,monitor,{}, {},root/'absent')

 def test_process_image_start_labels_are_closed_failed_calls_and_never_exit_or_hash_authority(self):
  collector=load('collect')
  terminal={'version':'owned-fullgraph-token-v2-result-v1','scenario':run.SCENARIOS[1],'accepted':False,'phase':'actual-twelve-other-source-constructors','failureClass':'owned-evidence-rejected','failureSHA256':'a'*64,'sourceSHA256':run.FINAL_SOURCE['digest'],'resourcePlaneSHA256':'b'*64,'actualStartedRoles':0,'constructorStage':'start','constructorRole':'msggateway','constructorCallState':'returned','constructorErrorClass':'owned-evidence','rawError':'PRIVATE_BODY','privatePath':'PRIVATE_PATH'}
  for step in ('context','before-identity','open','initial-stat','read','final-stat','size','hash','after-identity'):
   for state in ('wait-unobserved','wait-success','wait-other','wait-exit-code--1','wait-exit-code-0','wait-exit-code-1','wait-exit-code-255'):
    predicate='start-service-process-image-'+step+'-'+state
    value=dict(terminal,constructorPredicate=predicate)
    observed=collector.public_terminal(json.dumps(value).encode(),value['scenario'])
    self.assertEqual(observed['constructorPredicate'],predicate);self.assertFalse(observed['accepted']);self.assertFalse(observed['rawErrorText'])
    self.assertNotIn('PRIVATE_',json.dumps(observed))
    for fields in ({'constructorErrorClass':'none'},{'constructorErrorClass':'registration-pending'},{'constructorCallState':'in-progress','constructorErrorClass':'in-progress'},{'constructorRole':'none'},{'constructorRole':'rpc-msg'},{'constructorStage':'observe-registration'}):
     with self.subTest(predicate=predicate,invalid=fields),self.assertRaises(ValueError):collector.public_terminal(json.dumps(dict(value,**fields)).encode(),value['scenario'])
  for predicate in ('start-service-process-image-PRIVATE_PATH-wait-success','start-service-process-image-hash-live','start-service-process-image-hash-wait-success-extra','start-service-process-image-hash-wait-exit-code--2','start-service-process-image-hash-wait-exit-code-256','start-service-process-image-hash-wait-exit-code-01','start-service-process-image-hash-wait-exit-code-+1','start-service-process-image-hash-wait-exit-code-1.0'):
   with self.assertRaises(ValueError):collector.public_terminal(json.dumps(dict(terminal,constructorPredicate=predicate)).encode(),terminal['scenario'])

  # The earlier explicit fresh image/Kafka branch remains independently checked.
  control=run.CURRENT_IMAGE_KAFKA_CONTROL;package=next(iter(control['targets']));parent=run.module('ci/backend-runtime-join-r7/run.py','image_kafka_pure_auditor')
  locked=json.loads(run.INPUTS.read_bytes());locked.update(kafkaACLTestsToRun=list(control['targets'][package][:1]),processImageTestsToRun=list(control['targets'][package][1:]),currentImageKafkaControl=copy.deepcopy(control),currentKafkaConfirmControl=None,currentReleaseControl=None,currentReleaseTestsToRun=[],currentManagedShapeControl=None,currentManagedShapeTestsToRun=[],kafkaConfirmPassedReuse=None,kafkaACLPassedReuse=None);run.check_current_image_kafka_plan(locked)
  for change in ({'kafkaACLPassedReuse':run.KAFKA_ACL_PASSED_REUSE},{'processImageTestsToRun':[]},{'kafkaACLTestsToRun':[]},{'currentImageKafkaControl':{}}):
   with self.assertRaises(ValueError):run.check_current_image_kafka_plan(dict(locked,**change))
  events=[{'Action':'start','Package':parent.PREFIX+package}]
  for top in control['targets'][package]:
   events.append({'Action':'run','Package':parent.PREFIX+package,'Test':top})
   for child in control['requiredSubtests'][package][top]:
    name=top+'/'+child;events.extend([{'Action':'run','Package':parent.PREFIX+package,'Test':name},{'Action':'pass','Package':parent.PREFIX+package,'Test':name}])
   events.append({'Action':'pass','Package':parent.PREFIX+package,'Test':top})
  events.append({'Action':'pass','Package':parent.PREFIX+package})
  for mode in ('complete','missing-child','skip','foreign-top','missing-footer','phase-error'):
   with self.subTest(currentImageKafka=mode),tempfile.TemporaryDirectory() as directory:
    out=Path(directory).resolve();seen=[];environment={'GOPROXY':'off','GOSUMDB':'off','GOCACHE':'/owned/race-cache'}
    def command(label,argv,seconds,actual_environment):
     seen.append(label);self.assertEqual(label,control['phase']);self.assertEqual(seconds,240);self.assertEqual(actual_environment,environment)
     self.assertEqual(argv[:3],['/owned/go','test','-mod=readonly']);self.assertIn('-race',argv);self.assertIn('-timeout=180s',argv);self.assertEqual(argv[-1],'./'+package)
     pattern=__import__('re').compile(argv[argv.index('-run')+1])
     self.assertTrue(all(pattern.fullmatch(top) for top in control['targets'][package]));self.assertIsNone(pattern.fullmatch('TestForeign'))
     if mode=='phase-error':raise ValueError('original phase failure')
     rows=copy.deepcopy(events)
     if mode=='missing-child':rows=[r for r in rows if r.get('Test')!='TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation/real-fd-path-stability-and-mid-read-cancellation']
     elif mode=='skip':rows[2]['Action']='skip'
     elif mode=='foreign-top':rows.insert(1,{'Action':'run','Package':parent.PREFIX+package,'Test':'TestForeign'})
     elif mode=='missing-footer':rows=rows[:-1]
     return ('\n'.join(json.dumps(r) for r in rows)+'\n').encode()
    if mode!='complete':
     with self.assertRaises(ValueError):run.run_current_image_kafka_control('/owned/go',environment,command,parent,out)
     self.assertEqual(seen,[control['phase']]);self.assertFalse((out/'current-image-kafka-names.json').exists());continue
    value=run.run_current_image_kafka_control('/owned/go',environment,command,parent,out)
    self.assertEqual(seen,[control['phase']]);self.assertEqual((value['topPASS'],value['subPASS']),(2,4))
    self.assertEqual(value['source'],run.FINAL_SOURCE);self.assertFalse(value['runtimeAccepted']);self.assertFalse(value['wholeSourceAccepted'])
    projected,_=run.project_go_test_events(('\n'.join(json.dumps(r) for r in events)+'\n').encode(),control['phase'])
    run.write(out/'current-image-kafka-events.json',projected);run.write(out/'phase-0001.json',{'phase':control['phase'],'exitCode':0,'resource':{'status':'passed','exit_code':0}})
    run.write(out/'source-post.json',{'accepted':True,'source':run.FINAL_SOURCE});tools={'go':{'bytes':1,'sha256':'a'*64,'mode':0o755}};run.write(out/'tools-before.json',tools);run.write(out/'tools-after.json',tools)
    self.assertEqual(collector.checked_current_image_kafka_evidence(out),value)
    original={name:(out/name).read_bytes() for name in ('current-image-kafka-events.json','phase-0001.json','source-post.json','tools-after.json','kafka-acl-historical-pass.json')}
    for changed in ('events','phase','source-post','tools','historical'):
     for name,raw in original.items():(out/name).write_bytes(raw)
     if changed=='events':(out/'current-image-kafka-events.json').write_text(json.dumps(projected[:-1]))
     elif changed=='phase':(out/'phase-0001.json').write_text(json.dumps({'phase':control['phase'],'exitCode':0,'resource':{'status':'failed','exit_code':0}}))
     elif changed=='source-post':(out/'source-post.json').write_text(json.dumps({'accepted':False,'source':run.FINAL_SOURCE}))
     elif changed=='tools':(out/'tools-after.json').write_text('{}')
     else:(out/'kafka-acl-historical-pass.json').write_text('{}')
     with self.subTest(currentCustody=changed),self.assertRaises(ValueError):collector.checked_current_image_kafka_evidence(out)
    for name,raw in original.items():(out/name).write_bytes(raw)
    for changed in (dict(value,source=run.KAFKA_ACL_PASSED_REUSE['source']),dict(value,topPASS=True),dict(value,subPASS=2),dict(value,runtimeAccepted=True),dict(value,wholeSourceAccepted=True),dict(value,namedCompletions=value['namedCompletions'][:-1])):
     with self.assertRaises(ValueError):run.check_current_image_kafka_receipt(changed)
    run.write(out/'kafka-acl-passed-reuse.json',run.KAFKA_ACL_PASSED_REUSE)
    with self.assertRaises(ValueError):collector.checked_current_image_kafka_evidence(out)

  # The earlier explicit fresh Kafka branch remains independently checked.
  control=run.CURRENT_KAFKA_CONFIRM_CONTROL;package=next(iter(control['targets']));parent=run.module('ci/backend-runtime-join-r7/run.py','kafka_confirm_pure_auditor')
  locked=json.loads(run.INPUTS.read_bytes());locked.update(kafkaACLTestsToRun=list(control['targets'][package]),processImageTestsToRun=[],currentImageKafkaControl=None,currentKafkaConfirmControl=copy.deepcopy(control),currentReleaseControl=None,currentReleaseTestsToRun=[],currentManagedShapeControl=None,currentManagedShapeTestsToRun=[],kafkaConfirmPassedReuse=None,kafkaACLPassedReuse=None);run.check_current_kafka_confirm_plan(locked)
  for change in ({'kafkaACLPassedReuse':run.KAFKA_ACL_PASSED_REUSE},{'processImageTestsToRun':['TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation']},{'kafkaACLTestsToRun':[]},{'currentKafkaConfirmControl':{}},{'processImageHistoricalPass':{}},{'currentImageKafkaControl':run.CURRENT_IMAGE_KAFKA_CONTROL}):
   with self.assertRaises(ValueError):run.check_current_kafka_confirm_plan(dict(locked,**change))
  events=[{'Action':'start','Package':parent.PREFIX+package}]
  for top in control['targets'][package]:
   events.append({'Action':'run','Package':parent.PREFIX+package,'Test':top})
   for child in control['requiredSubtests'][package][top]:
    name=top+'/'+child;events.extend([{'Action':'run','Package':parent.PREFIX+package,'Test':name},{'Action':'pass','Package':parent.PREFIX+package,'Test':name}])
   events.append({'Action':'pass','Package':parent.PREFIX+package,'Test':top})
  events.append({'Action':'pass','Package':parent.PREFIX+package})
  for mode in ('complete','missing-new-child','skip','image-redispatch','missing-footer','phase-error'):
   with self.subTest(currentKafkaConfirmation=mode),tempfile.TemporaryDirectory() as directory:
    out=Path(directory).resolve();seen=[];environment={'GOPROXY':'off','GOSUMDB':'off','GOCACHE':'/owned/race-cache'}
    def command(label,argv,seconds,actual_environment):
     seen.append(label);self.assertEqual(label,control['phase']);self.assertEqual(seconds,240);self.assertEqual(actual_environment,environment)
     self.assertEqual(argv[:3],['/owned/go','test','-mod=readonly']);self.assertIn('-race',argv);self.assertIn('-timeout=180s',argv);self.assertEqual(argv[-1],'./'+package)
     pattern=__import__('re').compile(argv[argv.index('-run')+1])
     self.assertTrue(all(pattern.fullmatch(top) for top in control['targets'][package]));self.assertIsNone(pattern.fullmatch('TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation'))
     if mode=='phase-error':raise ValueError('original phase failure')
     rows=copy.deepcopy(events)
     if mode=='missing-new-child':rows=[r for r in rows if r.get('Test')!='TestOwnedKafkaExclusionRequiresEveryWildcardDeny/new-deny-visibility-is-bounded-without-grace-for-existing-denies']
     elif mode=='skip':rows[2]['Action']='skip'
     elif mode=='image-redispatch':rows.insert(1,{'Action':'run','Package':parent.PREFIX+package,'Test':'TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation'})
     elif mode=='missing-footer':rows=rows[:-1]
     return ('\n'.join(json.dumps(r) for r in rows)+'\n').encode()
    if mode!='complete':
     with self.assertRaises(ValueError):run.run_current_kafka_confirm_control('/owned/go',environment,command,parent,out)
     self.assertEqual(seen,[control['phase']]);self.assertFalse((out/'current-kafka-confirm-names.json').exists());continue
    value=run.run_current_kafka_confirm_control('/owned/go',environment,command,parent,out)
    self.assertEqual(seen,[control['phase']]);self.assertEqual((value['topPASS'],value['subPASS']),(1,3));self.assertEqual(value['actualProcessImageTestsDispatched'],0)
    self.assertEqual(value['source'],run.FINAL_SOURCE);self.assertFalse(value['runtimeAccepted']);self.assertFalse(value['wholeSourceAccepted'])
    projected,_=run.project_go_test_events(('\n'.join(json.dumps(r) for r in events)+'\n').encode(),control['phase'])
    run.write(out/'current-kafka-confirm-events.json',projected);run.write(out/'phase-0001.json',{'phase':control['phase'],'exitCode':0,'resource':{'status':'passed','exit_code':0}})
    run.write(out/'source-post.json',{'accepted':True,'source':run.FINAL_SOURCE});tools={'go':{'bytes':1,'sha256':'a'*64,'mode':0o755}};run.write(out/'tools-before.json',tools);run.write(out/'tools-after.json',tools)
    self.assertEqual(collector.checked_current_kafka_confirm_evidence(out),value)
    original={name:(out/name).read_bytes() for name in ('current-kafka-confirm-events.json','phase-0001.json','source-post.json','tools-after.json','kafka-acl-historical-pass.json','process-image-historical-pass.json')}
    for changed in ('events','phase','source-post','tools','historical-kafka','historical-image'):
     for name,raw in original.items():(out/name).write_bytes(raw)
     if changed=='events':(out/'current-kafka-confirm-events.json').write_text(json.dumps(projected[:-1]))
     elif changed=='phase':(out/'phase-0001.json').write_text(json.dumps({'phase':control['phase'],'exitCode':0,'resource':{'status':'failed','exit_code':0}}))
     elif changed=='source-post':(out/'source-post.json').write_text(json.dumps({'accepted':False,'source':run.FINAL_SOURCE}))
     elif changed=='tools':(out/'tools-after.json').write_text('{}')
     elif changed=='historical-kafka':(out/'kafka-acl-historical-pass.json').write_text('{}')
     else:(out/'process-image-historical-pass.json').write_text('{}')
     with self.subTest(currentKafkaCustody=changed),self.assertRaises(ValueError):collector.checked_current_kafka_confirm_evidence(out)
    for name,raw in original.items():(out/name).write_bytes(raw)
    for changed in (dict(value,source=run.PROCESS_IMAGE_PRIOR_PASS['source']),dict(value,topPASS=True),dict(value,subPASS=2),dict(value,actualProcessImageTestsDispatched=1),dict(value,runtimeAccepted=True),dict(value,wholeSourceAccepted=True),dict(value,namedCompletions=value['namedCompletions'][:-1])):
     with self.assertRaises(ValueError):run.check_current_kafka_confirm_receipt(changed)
    run.write(out/'current-image-kafka-names.json',{})
    with self.assertRaises(ValueError):collector.checked_current_kafka_confirm_evidence(out)

  # Historical reuse is valid only under its original whole-source identity.
  with mock.patch.object(run,'FINAL_SOURCE',run.KAFKA_CONFIRM_PASSED_REUSE['source']),mock.patch.object(collector.run,'FINAL_SOURCE',run.KAFKA_CONFIRM_PASSED_REUSE['source']):
   # Same whole-source Kafka reuse preserves the original run and never dispatches.
   locked=json.loads(run.INPUTS.read_bytes());locked.update(source=run.FINAL_SOURCE,currentReleaseControl=None,currentReleaseTestsToRun=[],currentManagedShapeControl=None,currentManagedShapeTestsToRun=[],kafkaACLTestsToRun=[],currentKafkaConfirmControl=None,kafkaConfirmPassedReuse=copy.deepcopy(run.KAFKA_CONFIRM_PASSED_REUSE));receipt=copy.deepcopy(run.KAFKA_CONFIRM_PASSED_REUSE)
   self.assertEqual(run.check_kafka_confirm_reuse_plan(locked),receipt)
   self.assertEqual((receipt['originalReceipt']['topPASS'],receipt['originalReceipt']['subPASS']),(1,3))
   self.assertEqual(receipt['originalActualKafkaTestsDispatched'],1);self.assertEqual(receipt['actualCurrentKafkaTestsDispatched'],0)
   for change in ({'kafkaACLTestsToRun':['TestOwnedKafkaExclusionRequiresEveryWildcardDeny']},{'kafkaACLPassedReuse':run.KAFKA_ACL_PASSED_REUSE},{'currentKafkaConfirmControl':run.CURRENT_KAFKA_CONFIRM_CONTROL},{'gatewayStartLogProjectionPolicy':{}},{'kafkaConfirmPassedReuse':None}):
    with self.assertRaises(ValueError):run.check_kafka_confirm_reuse_plan(dict(locked,**change))
   for changed in (dict(receipt,source=run.KAFKA_ACL_PASSED_REUSE['source']),dict(receipt,actualCurrentKafkaTestsDispatched=1),dict(receipt,originalActualKafkaTestsDispatched=0),dict(receipt,currentFreshPASS=True),dict(receipt,currentWholeOwnedcontrolPackagePASS=True),dict(receipt,runtimeAccepted=True)):
    with self.assertRaises(ValueError):run.check_kafka_confirm_reuse_receipt(changed)
   fixture={'public/whole-source':b'original'}
   class Recipe:
    @staticmethod
    def rows(bodies):return dict(bodies)
    @staticmethod
    def digest(rows):return run.FINAL_SOURCE['digest'] if rows==fixture else 'f'*64
   with tempfile.TemporaryDirectory() as directory:
    out=Path(directory).resolve();go=copy.deepcopy(receipt['goTool'])
    with mock.patch.object(run.subprocess,'run',side_effect=AssertionError('no Go dispatch')):
     self.assertEqual(run.load_kafka_confirm_reuse(out,fixture,Recipe,go),receipt)
    with self.assertRaises(ValueError):run.check_kafka_confirm_reuse(receipt,dict(fixture,changed=b'changed'),Recipe,go)
    with self.assertRaises(ValueError):run.check_kafka_confirm_reuse(receipt,fixture,Recipe,dict(go,sha256='f'*64))
    run.write(out/'source-post.json',{'accepted':True,'source':run.FINAL_SOURCE});run.write(out/'tools-before.json',{'go':go});run.write(out/'tools-after.json',{'go':go})
    self.assertEqual(collector.checked_kafka_confirm_reuse_evidence(out),receipt)
    (out/'tools-after.json').write_text('{}')
    with self.assertRaises(ValueError):collector.checked_kafka_confirm_reuse_evidence(out)
    (out/'tools-after.json').write_text(json.dumps({'go':go}))
    run.write(out/'current-kafka-confirm-names.json',receipt['originalReceipt'])
    with self.assertRaises(ValueError):collector.checked_kafka_confirm_reuse_evidence(out)


  # Private output can only produce a closed class and validated source lines.
  target='server/cmd/openim-msggateway/main.go';bodies={target:b'package main\nfunc main() {}\n'}
  private=b'PRIVATE_SECRET'
  raw=b'configFolderPath: /PRIVATE/config.yaml\npanic: yaml: PRIVATE_SECRET\nmain.main(PRIVATE_ARGUMENT)\n\t/PRIVATE/source/'+target.encode()+b':2 +0xabc\n\t/PRIVATE/vendor/foreign.go:2 +0xdef\n'
  projected=collector.project_gateway_log_bytes(raw,bodies)
  self.assertEqual((projected['headerClass'],projected['errorPrefixClass']),('go-panic','config-yaml'))
  self.assertEqual(projected['sourceFrames'],[{'path':target,'line':2}]);self.assertFalse(projected['rawTextExported']);self.assertFalse(projected['acceptanceEvidence']);self.assertFalse(projected['prefixIsCauseProof'])
  self.assertNotIn('PRIVATE',json.dumps(projected));self.assertNotIn('0xabc',json.dumps(projected))
  categories=((b'runtime error: invalid memory address or nil pointer dereference','runtime-nil'),(b'failed to get configuration from command: yaml:','config-yaml'),(b'json:','json'),(b'invalid character ','json'),(b'open ','file-open'),(b'listen ','listen'),(b'dial ','dial'),(b'tls:','tls'),(b'x509:','x509'),(b'unknown private body ','other'))
  for prefix,expected in categories:
   value=collector.project_gateway_log_bytes(b'panic: '+prefix+private,bodies)
   self.assertEqual(value['errorPrefixClass'],expected);self.assertNotIn('PRIVATE',json.dumps(value))
  value=collector.project_gateway_log_bytes(b'fatal error: PRIVATE_SECRET',bodies);self.assertEqual(value['headerClass'],'go-runtime-fatal');self.assertEqual(value['errorPrefixClass'],'other')
  for bad in (b'0',b'9999999',b'-1'):
   value=collector.project_gateway_log_bytes(b'\t/PRIVATE/source/'+target.encode()+b':'+bad+b' +0xabc',bodies);self.assertEqual(value['sourceFrames'],[])
  for path in ('/PRIVATE/source/server/cmd/../cmd/openim-msggateway/main.go','/PRIVATE/source/server/cmd/openim-msggateway/./main.go','//PRIVATE/source/'+target,'/PRIVATE/foreign/main.go','/PRIVATE/source/server/secret.go','/PRIVATE/source/server\\cmd\\openim-msggateway\\main.go'):
   value=collector.project_gateway_log_bytes(('\t'+path+':2 +0xabc').encode(),bodies);self.assertEqual(value['sourceFrames'],[])
  self.assertTrue(collector.project_gateway_log_bytes(b'X'*(run.GATEWAY_START_LOG_POLICY['lineBytesLimit']+1),bodies)['scanTruncated'])
  self.assertTrue(collector.project_gateway_log_bytes(b'\n'*(run.GATEWAY_START_LOG_POLICY['lineLimit']+1),bodies)['scanTruncated'])
  with self.assertRaises(ValueError):collector.project_gateway_log_bytes(b'X'*(run.GATEWAY_START_LOG_POLICY['logLimitBytes']+1),bodies)
  many={f'server/f{i}.go':b'one\ntwo\n' for i in range(run.GATEWAY_START_LOG_POLICY['framesLimit']+1)}
  many_raw='\n'.join('\t/PRIVATE/source/'+name+':2 +0xabc' for name in many).encode();value=collector.project_gateway_log_bytes(many_raw,many)
  self.assertEqual(len(value['sourceFrames']),run.GATEWAY_START_LOG_POLICY['framesLimit']);self.assertTrue(value['scanTruncated'])
  terminal={'version':'owned-fullgraph-token-v2-result-v1','scenario':run.SCENARIOS[1],'sourceSHA256':run.FINAL_SOURCE['digest'],'accepted':False,'constructorStage':'start','constructorRole':'msggateway','constructorCallState':'returned','constructorErrorClass':'owned-evidence','constructorPredicate':'start-service-process-image-after-identity-wait-exit-code-2'}
  for change in ({'accepted':True},{'constructorRole':'api'},{'constructorCallState':'in-progress'},{'constructorPredicate':'start-service-process-image-after-identity-wait-unobserved'},{'sourceSHA256':'f'*64}):
   with mock.patch.object(collector.run,'regular',side_effect=AssertionError('ineligible output must not be read')):
    self.assertIsNone(collector.project_gateway_start_log(Path('/not-read'),dict(terminal,**change),bodies))
  with tempfile.TemporaryDirectory() as directory:
   work=Path(directory).resolve()
   # Follow the actual constructor chain: scenario/services/required/<role>.
   # A valid-looking log in the earlier incorrect directory is never searched.
   old=work/'required/msggateway';old.parent.mkdir(mode=0o700);old.mkdir(mode=0o700)
   bait=old/('service-'+'a'*64+'.log');bait.write_bytes(b'panic: x509: PRIVATE_WRONG_DIRECTORY');bait.chmod(0o600)
   self.assertEqual(collector.project_gateway_start_log(work,terminal,bodies)['status'],'unavailable')
   folder=work/'services/required/msggateway'
   for current in (work/'services',work/'services/required',folder):current.mkdir(mode=0o700)
   self.assertEqual(collector.project_gateway_start_log(work,terminal,bodies)['status'],'absent')
   path=folder/('service-'+'a'*64+'.log');path.write_bytes(raw);path.chmod(0o600)
   value=collector.project_gateway_start_log(work,terminal,bodies);self.assertEqual(value['status'],'projected');self.assertEqual(value['sourceFrames'],[{'path':target,'line':2}]);self.assertNotIn('PRIVATE',json.dumps(value));self.assertNotIn('originalPrivateLogSHA256',value);self.assertNotIn('originalPrivateLogBytes',value)
   duplicate=folder/('service-'+'b'*64+'.log');duplicate.write_bytes(raw);duplicate.chmod(0o600)
   self.assertEqual(collector.project_gateway_start_log(work,terminal,bodies)['status'],'ambiguous');duplicate.unlink()
   path.chmod(0o644);self.assertEqual(collector.project_gateway_start_log(work,terminal,bodies)['status'],'unsafe');path.chmod(0o600)
   original=work/'original-private-log';path.rename(original);path.symlink_to(original)
   self.assertEqual(collector.project_gateway_start_log(work,terminal,bodies)['status'],'unsafe');path.unlink();original.rename(path)
   # Every fixed directory is private; canonical resolution rejects aliases at
   # services, required and the role without opening any private log.
   for current in (work,work/'services',work/'services/required',folder):
    current.chmod(0o755);self.assertEqual(collector.project_gateway_start_log(work,terminal,bodies)['status'],'unsafe');current.chmod(0o700)
   for current in (work/'services',work/'services/required',folder):
    retained=work/('retained-'+current.name);current.rename(retained)
    for destination in (retained,current):
     current.symlink_to(destination,target_is_directory=True)
     with mock.patch.object(collector.run,'regular',side_effect=AssertionError('directory aliases cannot read logs')):
      self.assertEqual(collector.project_gateway_start_log(work,terminal,bodies)['status'],'unsafe')
     current.unlink()
    retained.rename(current)
   linked=work/'hardlinked-private-log';os.link(path,linked)
   self.assertEqual(collector.project_gateway_start_log(work,terminal,bodies)['status'],'unsafe');linked.unlink()
   original_regular=collector.run.regular;retained=work/'replaced-private-log'
   def replace_after_original_read(candidate,limit):
    value=original_regular(candidate,limit)
    if candidate==path:path.rename(retained);path.write_bytes(value);path.chmod(0o600)
    return value
   with mock.patch.object(collector.run,'regular',side_effect=replace_after_original_read):
    self.assertEqual(collector.project_gateway_start_log(work,terminal,bodies)['status'],'unsafe')
   path.unlink();retained.rename(path)
   extras=[folder/('bounded-entry-'+str(i)) for i in range(run.GATEWAY_START_LOG_POLICY['directoryEntriesLimit'])]
   for extra in extras:extra.touch(mode=0o600)
   self.assertEqual(collector.project_gateway_start_log(work,terminal,bodies)['status'],'unsafe')
   for extra in extras:extra.unlink()
   value=collector.project_gateway_start_log(work,terminal,bodies)
   self.assertEqual(value['status'],'projected');self.assertEqual(value['errorPrefixClass'],'config-yaml');self.assertNotIn('PRIVATE',json.dumps(value))
   with mock.patch.object(collector.run,'verify',return_value=({},bodies,None,None,None,None)),mock.patch.object(collector.run,'write',side_effect=OSError('PRIVATE_ERROR')):
    self.assertFalse(collector.emit_gateway_start_log_diagnostic(work,work,terminal))

  # Preserve the exact command wrapper stage; a returned error is never a
  # unique panic/runtime diagnosis, and only fixed public prefixes escape.
  self.assertEqual(projected['version'],'owned-gateway-start-log-projection-v2')
  cases=(
   (b'failed to get configuration from command: unmarshal yaml error: PRIVATE','configuration','config-yaml'),
   (b'failed to get configuration from command: stat config path error: PRIVATE','configuration','config-stat'),
   (b'failed to get configuration from command: read file error: PRIVATE','configuration','config-read'),
   ('failed to get configuration from command: 配置项 secret 不安全（为空、占位符、默认值或少于 16 位），请换成随机强密钥后再启动'.encode(),'configuration','config-secret'),
   (b'failed to initialize from config: PRIVATE','logger','other'),
   (b'owned RPC lifecycle is unconfirmed','unclassified','owned-rpc'),
   (b'owned gateway lifecycle is unconfirmed','unclassified','owned-gateway'),
   (b'original owned Redis source is unconfirmed','unclassified','owned-redis'),
   (b'owned namespace control evidence invalid','unclassified','namespace-evidence'),
   (b'owned namespace runtime registration pending','unclassified','namespace-registration-pending'),
   (b'owned history reader has not registered yet','unclassified','history-registration-pending'),
   (b'original owned Mongo observer is required','unclassified','owned-mongo-observer'),
   (b'original source outcome owner is required','unclassified','owned-source-observer'),
   (b'callback transport type is unconfirmed','unclassified','callback-transport'),
   (b'callback transport is absent','unclassified','callback-transport'),
   (b'context deadline exceeded','unclassified','deadline'),(b'context canceled','unclassified','cancelled'))
  discovery=(b'owned discovery lifetime required',b'owned discovery context required',b'owned discovery type refused',b'owned discovery returned nil connection',b'owned discovery connection closed before ready',b'owned discovery connection timeout',b'owned discovery is closed',b'owned discovery close is unconfirmed')
  for payload,stage,kind in cases+tuple((value,'unclassified','owned-discovery') for value in discovery):
   value=collector.project_gateway_log_bytes(b'panic: '+payload,bodies)
   self.assertEqual((value['execErrorStage'],value['errorPrefixClass']),(stage,kind));self.assertNotIn('PRIVATE',json.dumps(value));self.assertFalse(value['prefixIsCauseProof'])
  # Join can preserve a lower-level category on later lines. Log lines before
  # the first panic and text after its runtime stack header are not panic data.
  joined=b'yaml: PRIVATE_BEFORE\npanic: owned RPC lifecycle is unconfirmed\nPRIVATE_UNKNOWN\noriginal owned Redis source is unconfirmed\noriginal owned Redis source is unconfirmed PRIVATE_DUPLICATE\nread file error: PRIVATE_FILE\nPRIVATE_MIDLINE dial PRIVATE\n\ngoroutine 1 [running]:\nx509: PRIVATE_AFTER\n\t/PRIVATE/source/'+target.encode()+b':2 +0xabc\n'
  value=collector.project_gateway_log_bytes(joined,bodies)
  self.assertEqual(value['errorPrefixClass'],'owned-rpc');self.assertEqual(value['panicPrefixClasses'],['owned-rpc','owned-redis','config-read']);self.assertEqual(value['sourceFrames'],[{'path':target,'line':2}]);self.assertNotIn('PRIVATE',json.dumps(value))
  self.assertFalse(value['acceptanceEvidence']);self.assertFalse(value['prefixIsCauseProof'])
  for header in (b'runtime stack:',b'stack trace:'):
   value=collector.project_gateway_log_bytes(b'panic: PRIVATE\n'+header+b'\nyaml: PRIVATE',bodies);self.assertEqual(value['panicPrefixClasses'],[])
  value=collector.project_gateway_log_bytes(b'panic: PRIVATE\n'+b'\n'.join((b'open PRIVATE',b'listen PRIVATE',b'dial PRIVATE',b'tls:PRIVATE',b'x509:PRIVATE',b'json:PRIVATE',b'yaml:PRIVATE',b'owned RPC lifecycle is unconfirmed',b'original owned Redis source is unconfirmed')),bodies)
  self.assertEqual(len(value['panicPrefixClasses']),run.GATEWAY_START_LOG_POLICY['panicPrefixClassesLimit']);self.assertTrue(value['scanTruncated']);self.assertNotIn('PRIVATE',json.dumps(value))
  # The verified Wrap1/2/3 text contains a function+line, never a Go filename.
  # Resolve only a unique same-package actual call; no symbol or argument is
  # retained. Closure suffixes share their actual source function declaration.
  wrap_target='server/internal/msggateway/init.go'
  wrap_body=b'package msggateway\nimport "github.com/OpenIMSDK/tools/utils"\nfunc Launch() error {\n return utils.Wrap1(nil)\n}\n'
  wrap_bodies=dict(bodies,**{wrap_target:wrap_body})
  wrapper=b'==> github.com/openimsdk/open-im-server/v3/internal/msggateway.Launch.func1()@4: : '
  value=collector.project_gateway_log_bytes(b'panic: '+wrapper+b'listen tcp PRIVATE\n\ngoroutine 1 [running]:\n\t/PRIVATE/source/'+target.encode()+b':2 +0xabc',wrap_bodies)
  self.assertEqual(value['sourceFrames'],[{'path':wrap_target,'line':4},{'path':target,'line':2}]);self.assertEqual(value['errorPrefixClass'],'listen');self.assertNotIn('Launch',json.dumps(value));self.assertNotIn('PRIVATE',json.dumps(value));self.assertNotIn('0xabc',json.dumps(value))
  for name in (b'Wrap1',b'Wrap2',b'Wrap3'):
   current=dict(wrap_bodies,**{wrap_target:wrap_body.replace(b'Wrap1',name)})
   value=collector.project_gateway_log_bytes(b'panic: failed to initialize from config: '+wrapper+b'open PRIVATE',current)
   self.assertEqual((value['execErrorStage'],value['errorPrefixClass']),('logger','file-open'));self.assertEqual(value['sourceFrames'],[{'path':wrap_target,'line':4}])
  method_body=wrap_body.replace(b'func Launch()',b'func (owner *Owner) Launch()')
  method_wrapper=wrapper.replace(b'Launch.func1',b'(*Owner).Launch.func1')
  value=collector.project_gateway_log_bytes(b'panic: '+method_wrapper+b'dial PRIVATE',dict(wrap_bodies,**{wrap_target:method_body}));self.assertEqual(value['sourceFrames'],[{'path':wrap_target,'line':4}]);self.assertEqual(value['errorPrefixClass'],'dial')
  for invalid in (wrapper.replace(b'github.com/openimsdk/open-im-server/v3/',b'github.com/foreign/private/'),wrapper.replace(b'internal/msggateway',b'internal/../msggateway'),wrapper.replace(b'internal/msggateway',b'internal//msggateway'),wrapper.replace(b'Launch.func1',b'Unknown'),wrapper.replace(b'Launch.func1',b'Launch(PRIVATE_ARGUMENT)'),wrapper.replace(b'@4:',b'@0:'),wrapper.replace(b'@4:',b'@9999999:'),wrapper.replace(b'@4:',b'@-4:'),wrapper.replace(b': : ',b': PRIVATE_MESSAGE: ')):
   value=collector.project_gateway_log_bytes(b'panic: '+invalid+b'listen PRIVATE',wrap_bodies);self.assertEqual(value['sourceFrames'],[]);self.assertEqual(value['errorPrefixClass'],'other');self.assertNotIn('PRIVATE',json.dumps(value))
  for changed in (dict(wrap_bodies,**{'server/internal/msggateway/duplicate.go':wrap_body}),dict(wrap_bodies,**{wrap_target:wrap_body.replace(b'return utils.Wrap1(nil)',b'return nil // utils.Wrap1(nil)')}),dict(wrap_bodies,**{wrap_target:wrap_body.replace(b'return utils.Wrap1(nil)',b'return "utils.Wrap1(nil)"')}),dict(wrap_bodies,**{wrap_target:wrap_body.replace(b'"github.com/OpenIMSDK/tools/utils"',b'"github.com/foreign/utils"')})):
   value=collector.project_gateway_log_bytes(b'panic: '+wrapper+b'listen PRIVATE',changed);self.assertEqual(value['sourceFrames'],[]);self.assertEqual(value['errorPrefixClass'],'other')
  value=collector.project_gateway_log_bytes(b'panic: '+wrapper*run.GATEWAY_START_LOG_POLICY['wrapperLimit']+b'tls:PRIVATE',wrap_bodies)
  self.assertEqual(value['errorPrefixClass'],'tls');self.assertEqual(value['sourceFrames'],[{'path':wrap_target,'line':4}])
  value=collector.project_gateway_log_bytes(b'panic: '+wrapper*(run.GATEWAY_START_LOG_POLICY['wrapperLimit']+1)+b'tls:PRIVATE',wrap_bodies)
  self.assertEqual(value['errorPrefixClass'],'other');self.assertTrue(value['scanTruncated']);self.assertNotIn('PRIVATE',json.dumps(value))
  # Multiple Join references share the original combined eight-frame limit.
  many_bodies={};many_lines=[]
  for index in range(run.GATEWAY_START_LOG_POLICY['framesLimit']+1):
   name=f'server/public{index}/wrap.go';many_bodies[name]=wrap_body
   many_lines.append(b'==> github.com/openimsdk/open-im-server/v3/public'+str(index).encode()+b'.Launch()@4: : dial PRIVATE')
  value=collector.project_gateway_log_bytes(b'panic: PRIVATE\n'+b'\n'.join(many_lines),many_bodies)
  self.assertEqual(len(value['sourceFrames']),run.GATEWAY_START_LOG_POLICY['framesLimit']);self.assertTrue(value['scanTruncated']);self.assertNotIn('PRIVATE',json.dumps(value))


 def test_release_controls_keep_one_race_and_exact_named_evidence(self):
  collector=load('collect')
  with mock.patch.object(run,'FINAL_SOURCE',run.RELEASE_CONTROLS_PRIOR_PASS['source']),mock.patch.object(collector.run,'FINAL_SOURCE',run.RELEASE_CONTROLS_PRIOR_PASS['source']):
   control=run.CURRENT_RELEASE_CONTROL;package=next(iter(control['targets']));locked=json.loads(run.INPUTS.read_bytes());locked.update(source=run.FINAL_SOURCE,currentCronFixtureControl=None,cronFixtureScopeReuse=None,cronFixtureTestsToRun=[],currentCacheTestDeltaControl=None,cacheTestDeltaPackageReuse=None,cacheTestDeltaTestsToRun=[],currentGroupRecoveryControl=None,currentGroupRecoveryTargets={},currentProducerControl=None,sameSourceCompletedKafkaPackageReuse=None,currentManagedShapeControl=None,currentManagedShapeTestsToRun=[],currentReleaseControl=copy.deepcopy(control),currentReleaseTestsToRun=control['targets'][package][:-1],kafkaACLTestsToRun=control['targets'][package][-1:])
   self.assertEqual(run.check_current_release_plan(locked),control);run.check_locked(locked)
   self.assertNotEqual(run.FINAL_SOURCE,run.KAFKA_CONFIRM_PRIOR_PASS['source']);self.assertIsNone(locked['kafkaConfirmPassedReuse']);self.assertEqual(run.check_kafka_confirm_prior_pass(locked['kafkaConfirmHistoricalPass']),run.KAFKA_CONFIRM_PRIOR_PASS)
   rows=[{'scenario':scenario,'resourceStarted':False,'runtimeAttempted':False,'runtimeCleanupAccepted':False} for scenario in run.SCENARIOS]
   observation=collector.console_observation(rows,{}, {},{},[{'phase':control['phase'],'exitCode':0,'resource':{'status':'passed'}}])
   self.assertEqual(observation['lastPhase']['phase'],control['phase']);self.assertFalse(observation['acceptanceEvidence'])
   for change in ({'currentReleaseTestsToRun':[]},{'kafkaACLTestsToRun':[]},{'kafkaConfirmPassedReuse':run.KAFKA_CONFIRM_PRIOR_PASS},{'currentKafkaConfirmControl':run.CURRENT_KAFKA_CONFIRM_CONTROL},{'currentImageKafkaControl':run.CURRENT_IMAGE_KAFKA_CONTROL},{'kafkaConfirmHistoricalPass':{}},{'processImageTestsToRun':['TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation']}):
    with self.assertRaises(ValueError):run.check_current_release_plan(dict(locked,**change))
   with self.assertRaises(ValueError):run.check_kafka_confirm_reuse_receipt(run.KAFKA_CONFIRM_PRIOR_PASS)
   parent=run.module('ci/backend-runtime-join-r7/run.py','release_control_pure_auditor');events=[{'Action':'start','Package':parent.PREFIX+package}]
   for top in control['targets'][package]:
    events.append({'Action':'run','Package':parent.PREFIX+package,'Test':top})
    for child in control['requiredSubtests'][package][top]:
     name=top+'/'+child;events.extend([{'Action':'run','Package':parent.PREFIX+package,'Test':name},{'Action':'output','Package':parent.PREFIX+package,'Test':name,'Output':'PRIVATE_OUTPUT'},{'Action':'pass','Package':parent.PREFIX+package,'Test':name}])
    events.append({'Action':'pass','Package':parent.PREFIX+package,'Test':top})
   events.append({'Action':'pass','Package':parent.PREFIX+package})
   for mode in ('complete','missing-release-child','missing-kafka-child','skip','foreign-top','missing-footer','phase-error'):
    with self.subTest(releaseControl=mode),tempfile.TemporaryDirectory() as directory:
     out=Path(directory).resolve();seen=[];environment={'GOPROXY':'off','GOSUMDB':'off'}
     def command(label,argv,seconds,actual_environment):
      seen.append(label);self.assertEqual(label,control['phase']);self.assertEqual(seconds,240);self.assertEqual(actual_environment,environment)
      self.assertEqual(argv[:3],['/owned/go','test','-mod=readonly']);self.assertIn('-race',argv);self.assertIn('-count=1',argv);self.assertIn('-timeout=180s',argv);self.assertEqual(argv[-1],'./'+package)
      pattern=__import__('re').compile(argv[argv.index('-run')+1]);self.assertTrue(all(pattern.fullmatch(top) for top in control['targets'][package]));self.assertIsNone(pattern.fullmatch('TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation'))
      if mode=='phase-error':raise ValueError('original phase failure')
      rows=copy.deepcopy(events)
      if mode=='missing-release-child':rows=[row for row in rows if row.get('Test')!=control['targets'][package][0]+'/'+control['requiredSubtests'][package][control['targets'][package][0]][0]]
      elif mode=='missing-kafka-child':rows=[row for row in rows if row.get('Test')!=control['targets'][package][-1]+'/'+control['requiredSubtests'][package][control['targets'][package][-1]][-1]]
      elif mode=='skip':rows[2]['Action']='skip'
      elif mode=='foreign-top':rows.insert(1,{'Action':'run','Package':parent.PREFIX+package,'Test':'TestForeign'})
      elif mode=='missing-footer':rows=rows[:-1]
      return ('\n'.join(json.dumps(row) for row in rows)+'\n').encode()
     if mode!='complete':
      with self.assertRaises(ValueError):run.run_current_release_control('/owned/go',environment,command,parent,out)
      self.assertEqual(seen,[control['phase']]);self.assertFalse((out/'current-release-controls-names.json').exists());continue
     value=run.run_current_release_control('/owned/go',environment,command,parent,out)
     self.assertEqual(seen,[control['phase']]);self.assertEqual((value['topPASS'],value['subPASS']),(4,22));self.assertEqual(value['actualOwnedcontrolRaceInvocations'],1);self.assertEqual(value['actualReleaseTopsDispatched'],3);self.assertFalse(value['wholeSourceAccepted']);self.assertFalse(value['runtimeAccepted'])
     item=value['individualKafkaFact'];self.assertEqual((item['topPASS'],item['subPASS'],len(item['namedCompletions'])),(1,3,4));self.assertEqual(item['sharedRaceInvocation'],1);self.assertEqual(item['separateKafkaInvocations'],0);self.assertFalse(item['currentWholeOwnedcontrolPackagePASS'])
     projected,_=run.project_go_test_events(('\n'.join(json.dumps(row) for row in events)+'\n').encode(),control['phase']);self.assertNotIn('PRIVATE_OUTPUT',json.dumps(projected))
     run.write(out/'current-release-controls-events.json',projected);run.write(out/'phase-0001.json',{'phase':control['phase'],'exitCode':0,'resource':{'status':'passed','exit_code':0}})
     run.write(out/'source-post.json',{'accepted':True,'source':run.FINAL_SOURCE});tools={'go':{'bytes':1,'sha256':'a'*64,'mode':0o755}};run.write(out/'tools-before.json',tools);run.write(out/'tools-after.json',tools)
     self.assertEqual(collector.checked_current_release_evidence(out),value)
     names=('current-release-controls-events.json','current-release-kafka-individual.json','phase-0001.json','source-post.json','tools-after.json','kafka-confirm-historical-pass.json','kafka-acl-historical-pass.json','process-image-historical-pass.json');original={name:(out/name).read_bytes() for name in names}
     for changed in names:
      for name,raw in original.items():(out/name).write_bytes(raw)
      (out/changed).write_text(json.dumps(projected[:-1]) if changed.endswith('-events.json') else '{}')
      with self.subTest(releaseCustody=changed),self.assertRaises(ValueError):collector.checked_current_release_evidence(out)
     for name,raw in original.items():(out/name).write_bytes(raw)
     for fields in ({'source':run.KAFKA_CONFIRM_PRIOR_PASS['source']},{'topPASS':True},{'subPASS':21},{'actualReleaseTopsDispatched':0},{'actualOwnedcontrolRaceInvocations':2},{'actualKafkaACLTestsDispatched':0},{'actualProcessImageTestsDispatched':1},{'runtimeAccepted':True},{'wholeSourceAccepted':True},{'individualKafkaFact':dict(item,separateKafkaInvocations=1)},{'namedCompletions':value['namedCompletions'][:-1]}):
      with self.assertRaises(ValueError):run.check_current_release_receipt(dict(value,**fields))
     for alias in ('kafka-acl-passed-reuse.json','kafka-confirm-passed-reuse.json','current-kafka-confirm-names.json','current-image-kafka-names.json'):
      run.write(out/alias,{})
      with self.assertRaises(ValueError):collector.checked_current_release_evidence(out)
      (out/alias).unlink()


 def test_managed_shape_controls_keep_two_packages_one_command_and_exact_subsets(self):
  collector=load('collect');control=run.CURRENT_MANAGED_SHAPE_CONTROL;locked=json.loads(run.INPUTS.read_bytes());owned='internal/namespaceimport/ownedcontrol';managed='internal/msgtransfer'
  locked.update(currentCronFixtureControl=None,cronFixtureScopeReuse=None,cronFixtureTestsToRun=[],currentCacheTestDeltaControl=None,cacheTestDeltaPackageReuse=None,cacheTestDeltaTestsToRun=[],currentGroupRecoveryControl=None,currentGroupRecoveryTargets={},currentProducerControl=None,sameSourceCompletedKafkaPackageReuse=None,currentManagedShapeControl=copy.deepcopy(control),currentManagedShapeTestsToRun=control['targets'][managed],kafkaACLTestsToRun=control['targets'][owned])
  self.assertEqual(run.check_current_managed_shape_plan(locked),control);run.check_locked(locked);self.assertIsNone(locked['currentReleaseControl']);self.assertNotEqual(run.FINAL_SOURCE,run.RELEASE_CONTROLS_PRIOR_PASS['source'])
  for fields in ({'currentManagedShapeTestsToRun':[]},{'kafkaACLTestsToRun':[]},{'currentReleaseControl':run.CURRENT_RELEASE_CONTROL},{'releaseControlsHistoricalPass':{}},{'kafkaConfirmPassedReuse':run.KAFKA_CONFIRM_PRIOR_PASS},{'processImageTestsToRun':['TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation']}):
   with self.assertRaises(ValueError):run.check_current_managed_shape_plan(dict(locked,**fields))
  with self.assertRaises(ValueError):run.check_release_controls_prior_pass(dict(run.RELEASE_CONTROLS_PRIOR_PASS,source=run.FINAL_SOURCE))
  rows=[{'scenario':scenario,'resourceStarted':False,'runtimeAttempted':False,'runtimeCleanupAccepted':False} for scenario in run.SCENARIOS]
  self.assertEqual(collector.console_observation(rows,{}, {},{},[{'phase':control['phase']}])['lastPhase']['phase'],control['phase'])
  parent=run.module('ci/backend-runtime-join-r7/run.py','managed_shape_pure_auditor');events=[]
  for package,tops in control['targets'].items():
   events.append({'Action':'start','Package':parent.PREFIX+package})
   for top in tops:
    events.append({'Action':'run','Package':parent.PREFIX+package,'Test':top})
    for child in control['requiredSubtests'][package][top]:
     name=top+'/'+child;events.extend([{'Action':'run','Package':parent.PREFIX+package,'Test':name},{'Action':'output','Package':parent.PREFIX+package,'Test':name,'Output':'PRIVATE_OUTPUT'},{'Action':'pass','Package':parent.PREFIX+package,'Test':name}])
    events.append({'Action':'pass','Package':parent.PREFIX+package,'Test':top})
   events.append({'Action':'pass','Package':parent.PREFIX+package})
  for mode in ('complete','missing-producer-child','missing-kafka-child','skip','foreign-top','missing-package-footer','phase-error'):
   with self.subTest(managedShape=mode),tempfile.TemporaryDirectory() as directory:
    out=Path(directory).resolve();seen=[];environment={'GOPROXY':'off','GOSUMDB':'off'}
    def command(label,argv,seconds,actual_environment):
     seen.append(label);self.assertEqual(label,control['phase']);self.assertEqual(seconds,240);self.assertEqual(actual_environment,environment);self.assertEqual(argv[-2:],['./'+owned,'./'+managed]);self.assertIn('-race',argv);self.assertIn('-count=1',argv)
     pattern=__import__('re').compile(argv[argv.index('-run')+1]);self.assertTrue(all(pattern.fullmatch(top) for tops in control['targets'].values() for top in tops));self.assertIsNone(pattern.fullmatch('TestNamespaceRuntimeMissingReleaseWaitsWithoutWeakeningPrivateEvidence'))
     if mode=='phase-error':raise ValueError('original phase failed')
     selected=copy.deepcopy(events)
     if mode in ('missing-producer-child','missing-kafka-child'):
      package=managed if mode=='missing-producer-child' else owned;top=control['targets'][package][0];name=top+'/'+control['requiredSubtests'][package][top][0];selected=[row for row in selected if (row.get('Package'),row.get('Test'))!=(parent.PREFIX+package,name)]
     elif mode=='skip':selected[2]['Action']='skip'
     elif mode=='foreign-top':selected.insert(1,{'Action':'run','Package':parent.PREFIX+owned,'Test':'TestForeign'})
     elif mode=='missing-package-footer':selected=[row for row in selected if not(row.get('Package')==parent.PREFIX+managed and row.get('Action')=='pass' and not row.get('Test'))]
     return ('\n'.join(json.dumps(row) for row in selected)+'\n').encode()
    if mode!='complete':
     with self.assertRaises(ValueError):run.run_current_managed_shape_control('/owned/go',environment,command,parent,out)
     self.assertEqual(seen,[control['phase']]);self.assertFalse((out/'current-managed-shape-controls-names.json').exists());continue
    value=run.run_current_managed_shape_control('/owned/go',environment,command,parent,out);self.assertEqual(seen,[control['phase']]);self.assertEqual((value['topPASS'],value['subPASS'],value['packages']),(2,16,2));self.assertEqual((value['actualGoCommandInvocations'],value['actualPackageExecutions']),(1,2));self.assertFalse(value['samePackageSingleRaceInvocation'])
    self.assertEqual((value['packageFacts'][owned]['topPASS'],value['packageFacts'][owned]['subPASS']),(1,3));self.assertEqual((value['packageFacts'][managed]['topPASS'],value['packageFacts'][managed]['subPASS']),(1,13));self.assertEqual(value['individualKafkaFact']['separateKafkaCommandInvocations'],0);self.assertFalse(value['wholeSourceAccepted'])
    projected,_=run.project_go_test_events(('\n'.join(json.dumps(row) for row in events)+'\n').encode(),control['phase']);self.assertNotIn('PRIVATE_OUTPUT',json.dumps(projected))
    run.write(out/'current-managed-shape-controls-events.json',projected);run.write(out/'phase-0001.json',{'phase':control['phase'],'exitCode':0,'resource':{'status':'passed','exit_code':0}});run.write(out/'source-post.json',{'accepted':True,'source':run.FINAL_SOURCE});tools={'go':{'bytes':1,'sha256':'a'*64,'mode':0o755}};run.write(out/'tools-before.json',tools);run.write(out/'tools-after.json',tools)
    self.assertEqual(collector.checked_current_managed_shape_evidence(out),value)
    names=('current-managed-shape-controls-events.json','current-managed-shape-kafka-individual.json','phase-0001.json','source-post.json','tools-after.json','release-controls-historical-pass.json','kafka-confirm-historical-pass.json','kafka-acl-historical-pass.json','process-image-historical-pass.json');original={name:(out/name).read_bytes() for name in names}
    for changed in names:
     for name,raw in original.items():(out/name).write_bytes(raw)
     (out/changed).write_text(json.dumps(projected[:-1]) if changed.endswith('-events.json') else '{}')
     with self.subTest(managedCustody=changed),self.assertRaises(ValueError):collector.checked_current_managed_shape_evidence(out)
    for name,raw in original.items():(out/name).write_bytes(raw)
    (out/'current-managed-shape-kafka-individual.json').write_text(json.dumps(dict(value['individualKafkaFact'],commandInvocations=True)))
    with self.assertRaises(ValueError):collector.checked_current_managed_shape_evidence(out)
    (out/'current-managed-shape-kafka-individual.json').write_bytes(original['current-managed-shape-kafka-individual.json'])
    bad_facts=copy.deepcopy(value['packageFacts']);bad_facts[owned]['topPASS']=True
    for fields in ({'source':run.RELEASE_CONTROLS_PRIOR_PASS['source']},{'packages':1},{'topPASS':True},{'subPASS':15},{'actualGoCommandInvocations':2},{'actualPackageExecutions':1},{'samePackageSingleRaceInvocation':True},{'packageFacts':bad_facts},{'runtimeAccepted':True},{'wholeSourceAccepted':True},{'individualKafkaFact':dict(value['individualKafkaFact'],separateKafkaCommandInvocations=1)}):
     with self.assertRaises(ValueError):run.check_current_managed_shape_receipt(dict(value,**fields))
    for alias in ('current-release-controls-names.json','kafka-acl-passed-reuse.json','kafka-confirm-passed-reuse.json','current-kafka-confirm-names.json','current-image-kafka-names.json'):
     run.write(out/alias,{})
     with self.assertRaises(ValueError):collector.checked_current_managed_shape_evidence(out)
     (out/alias).unlink()


 def test_producer_only_reuses_same_source_completed_kafka_without_accepting_timeout_scope(self):
  collector=load('collect')
  with mock.patch.object(run,'FINAL_SOURCE',run.COMPLETED_KAFKA_PACKAGE_BINDING['source']),mock.patch.object(collector.run,'FINAL_SOURCE',run.COMPLETED_KAFKA_PACKAGE_BINDING['source']):
   control=run.CURRENT_PRODUCER_CONTROL;locked=json.loads(run.INPUTS.read_bytes());package=next(iter(control['targets']));top=control['targets'][package][0]
   locked.update(source=run.FINAL_SOURCE,currentCronFixtureControl=None,cronFixtureScopeReuse=None,cronFixtureTestsToRun=[],currentCacheTestDeltaControl=None,cacheTestDeltaPackageReuse=None,cacheTestDeltaTestsToRun=[],currentGroupRecoveryControl=None,currentGroupRecoveryTargets={},currentProducerControl=copy.deepcopy(control),currentProducerTestsToRun=control['targets'][package],kafkaACLTestsToRun=[],sameSourceCompletedKafkaPackageReuse=copy.deepcopy(run.COMPLETED_KAFKA_PACKAGE_REUSE))
   self.assertEqual(run.check_current_producer_plan(locked),control);run.check_locked(locked);self.assertEqual(control['phaseGuardSeconds'],600);self.assertEqual(control['goTimeoutSeconds'],180)
   self.assertEqual(locked['kafkaACLTestsToRun'],[]);self.assertIsNone(locked['currentManagedShapeControl']);self.assertFalse(run.COMPLETED_KAFKA_PACKAGE_REUSE['originalGoCommandPassed']);self.assertFalse(run.COMPLETED_KAFKA_PACKAGE_REUSE['originalTwoPackageScopeAccepted'])
   for fields in ({'currentProducerTestsToRun':[]},{'kafkaACLTestsToRun':['TestOwnedKafkaExclusionRequiresEveryWildcardDeny']},{'currentManagedShapeControl':run.CURRENT_MANAGED_SHAPE_CONTROL},{'currentReleaseControl':run.CURRENT_RELEASE_CONTROL},{'sameSourceCompletedKafkaPackageReuse':{}},{'managedShapeTwoPackageHistoricalAttempt':{}},{'processImageTestsToRun':['TestOwnedProcessImageStreamingKeepsOriginalIdentityAndCancellation']}):
    with self.assertRaises(ValueError):run.check_current_producer_plan(dict(locked,**fields))
   for fields in ({'originalGoCommandPassed':True},{'originalTwoPackageScopeAccepted':True},{'actualCurrentGoCommandInvocations':1},{'actualCurrentKafkaACLTestsDispatched':1},{'reusedTopPASS':True},{'runtimeAccepted':True}):
    with self.assertRaises(ValueError):run.check_completed_kafka_package_reuse(dict(run.COMPLETED_KAFKA_PACKAGE_REUSE,**fields))
   bad=copy.deepcopy(run.COMPLETED_KAFKA_PACKAGE_REUSE);bad['binding']['source']=run.RELEASE_CONTROLS_PRIOR_PASS['source']
   with self.assertRaises(ValueError):run.check_completed_kafka_package_reuse(bad)
   with self.assertRaises(ValueError):run.check_completed_kafka_package_reuse(run.COMPLETED_KAFKA_PACKAGE_REUSE,dict(run.COMPLETED_KAFKA_PACKAGE_GO,sha256='0'*64))
   parsed=ast.parse(Path(run.__file__).read_text());actual_run=next(node for node in parsed.body if isinstance(node,ast.FunctionDef) and node.name=='run');actual_command=next(node for node in ast.walk(actual_run) if isinstance(node,ast.FunctionDef) and node.name=='command')
   direct=[node.value for node in actual_command.body if isinstance(node,ast.Expr) and isinstance(node.value,ast.Call) and isinstance(node.value.func,ast.Name) and node.value.func.id=='persist_go_test_projection']
   self.assertEqual(len(direct),1);self.assertEqual(ast.dump(direct[0],include_attributes=False),ast.dump(ast.parse("persist_go_test_projection(out,folder/'stdout',label,command_index)").body[0].value,include_attributes=False))
   self.assertTrue(run.go_test_projection_phase(control['phase']));self.assertFalse(run.go_test_projection_phase('runtime-reader-writer-fence-cancel'));self.assertFalse(run.go_test_projection_phase('PRIVATE_PHASE'))
   parent=run.module('ci/backend-runtime-join-r7/run.py','producer_only_pure_auditor');names=[top]+[top+'/'+child for child in control['requiredSubtests'][package][top]]
   events=[{'Action':'start','Package':parent.PREFIX+package},{'Action':'run','Package':parent.PREFIX+package,'Test':top}]
   for name in names[1:]:events.extend([{'Action':'run','Package':parent.PREFIX+package,'Test':name},{'Action':'output','Package':parent.PREFIX+package,'Test':name,'Output':'PRIVATE_OUTPUT'},{'Action':'pass','Package':parent.PREFIX+package,'Test':name}])
   events.extend([{'Action':'pass','Package':parent.PREFIX+package,'Test':top},{'Action':'pass','Package':parent.PREFIX+package}])
   for mode in ('complete','missing-child','foreign-child','skip','missing-footer','foreign-package','phase-error'):
    with self.subTest(producer=mode),tempfile.TemporaryDirectory() as directory:
     out=Path(directory).resolve();seen=[];environment={'GOPROXY':'off','GOSUMDB':'off'}
     def command(label,argv,seconds,actual_environment):
      seen.append(label);self.assertEqual(label,control['phase']);self.assertEqual(seconds,600);self.assertEqual(actual_environment,environment)
      self.assertEqual(argv,['/owned/go','test','-mod=readonly','-race','-p=1','-parallel=1','-count=1','-json','-vet=off','-timeout=180s','-run','^'+top+'$','./'+package])
      if mode=='phase-error':raise ValueError('original producer phase failed')
      selected=copy.deepcopy(events)
      if mode=='missing-child':selected=[row for row in selected if row.get('Test')!=names[-1]]
      if mode=='foreign-child':selected[-2]['Test']=top+'/foreign'
      if mode=='skip':selected[-2]['Action']='skip'
      if mode=='missing-footer':selected=selected[:-1]
      if mode=='foreign-package':selected[-1]['Package']=parent.PREFIX+'internal/namespaceimport/ownedcontrol'
      raw=('\n'.join(json.dumps(row) for row in selected)+'\n').encode();stdout=out/'private-stdout';stdout.write_bytes(raw)
      self.assertTrue(run.persist_go_test_projection(out,stdout,label,1));stdout.unlink()
      return raw
     if mode!='complete':
      with self.assertRaises(ValueError):run.run_current_producer_control('/owned/go',environment,command,parent,out,run.COMPLETED_KAFKA_PACKAGE_GO)
      self.assertEqual(seen,[control['phase']]);self.assertFalse((out/'current-managed-producer-control-names.json').exists())
      if mode=='phase-error':
       run.write(out/'source-post.json',{'accepted':True,'source':run.FINAL_SOURCE});tools={'go':run.COMPLETED_KAFKA_PACKAGE_GO};run.write(out/'tools-before.json',tools);run.write(out/'tools-after.json',tools)
       self.assertEqual(collector.checked_completed_kafka_package_reuse_evidence(out),run.COMPLETED_KAFKA_PACKAGE_REUSE)
      continue
     value=run.run_current_producer_control('/owned/go',environment,command,parent,out,run.COMPLETED_KAFKA_PACKAGE_GO);self.assertEqual(seen,[control['phase']]);self.assertEqual((value['topPASS'],value['subPASS'],value['packages']),(1,13,1));self.assertEqual(value['actualKafkaACLTestsDispatched'],0)
     projected=json.loads((out/'current-managed-producer-control-events.json').read_bytes());self.assertNotIn('PRIVATE_OUTPUT',json.dumps(projected));self.assertFalse(run.persist_go_test_projection(out,out/'missing-private-stdout','PRIVATE_PHASE',2))
     run.write(out/'phase-0001.json',{'phase':control['phase'],'exitCode':0,'resource':{'status':'passed','exit_code':0}});run.write(out/'source-post.json',{'accepted':True,'source':run.FINAL_SOURCE});tools={'go':run.COMPLETED_KAFKA_PACKAGE_GO};run.write(out/'tools-before.json',tools);run.write(out/'tools-after.json',tools)
     self.assertEqual(collector.checked_current_producer_evidence(out),value)
     checked=('current-managed-producer-control-events.json','same-source-completed-kafka-package-reuse.json','managed-shape-two-package-historical-attempt.json','phase-0001.json','source-post.json','tools-after.json','release-controls-historical-pass.json','kafka-confirm-historical-pass.json','kafka-acl-historical-pass.json','process-image-historical-pass.json');original={name:(out/name).read_bytes() for name in checked}
     for changed in checked:
      for name,raw in original.items():(out/name).write_bytes(raw)
      (out/changed).write_text(json.dumps(projected[:-1]) if changed.endswith('-events.json') else '{}')
      with self.subTest(producerCustody=changed),self.assertRaises(ValueError):collector.checked_current_producer_evidence(out)
     for name,raw in original.items():(out/name).write_bytes(raw)
     for fields in ({'topPASS':True},{'subPASS':12},{'packages':2},{'actualKafkaACLTestsDispatched':1},{'actualPackageExecutions':2},{'wholeMsgtransferPackagePASS':True},{'runtimeAccepted':True},{'source':run.RELEASE_CONTROLS_PRIOR_PASS['source']}):
      with self.assertRaises(ValueError):run.check_current_producer_receipt(dict(value,**fields))
     for alias in ('current-managed-shape-controls-names.json','current-managed-shape-kafka-individual.json','current-release-controls-names.json','kafka-acl-passed-reuse.json','kafka-confirm-passed-reuse.json','current-kafka-confirm-names.json','current-image-kafka-names.json'):
      run.write(out/alias,{})
      with self.assertRaises(ValueError):collector.checked_current_producer_evidence(out)
      (out/alias).unlink()
     run.write_current_producer_summary(out,value,None,{},{});summary=json.loads((out/'summary.json').read_bytes());self.assertEqual(summary['currentFreshGo'],{'topPASS':3,'subPASS':16,'includesOriginalGraph':True});self.assertFalse(summary['managedShapeTwoPackageHistoricalAttempt']['originalTwoPackageScopeAccepted'])


 def test_group_recovery_controls_use_real_event_hook_and_exact_three_package_scope(self):
  collector=load('collect')
  with mock.patch.object(run,'FINAL_SOURCE',run.CACHE_TEST_DELTA_PACKAGE_REUSE['originalSource']),mock.patch.object(collector.run,'FINAL_SOURCE',run.CACHE_TEST_DELTA_PACKAGE_REUSE['originalSource']):
   control=run.CURRENT_GROUP_RECOVERY_CONTROL;locked=json.loads(run.INPUTS.read_bytes());locked.update(source=run.FINAL_SOURCE,currentCronFixtureControl=None,cronFixtureScopeReuse=None,cronFixtureTestsToRun=[],currentCacheTestDeltaControl=None,cacheTestDeltaPackageReuse=None,cacheTestDeltaTestsToRun=[],currentGroupRecoveryControl=copy.deepcopy(control),currentGroupRecoveryTargets=control['targets'],kafkaACLTestsToRun=control['targets']['internal/namespaceimport/ownedcontrol']);self.assertEqual(run.check_current_group_recovery_plan(locked),control);run.check_locked(locked)
   self.assertEqual((control['topPASS'],control['subPASS'],control['packages'],control['phaseGuardSeconds'],control['goTimeoutSeconds']),(4,23,3,600,180))
   for fields in ({'kafkaACLTestsToRun':[]},{'currentGroupRecoveryTargets':{}},{'currentProducerControl':run.CURRENT_PRODUCER_CONTROL},{'sameSourceCompletedKafkaPackageReuse':run.COMPLETED_KAFKA_PACKAGE_REUSE},{'groupRecoveryHistoricalFacts':{}},{'rpcGroupStartLogProjectionPolicy':{}}):
    with self.assertRaises(ValueError):run.check_current_group_recovery_plan(dict(locked,**fields))
   parsed=ast.parse(Path(run.__file__).read_text());actual_run=next(node for node in parsed.body if isinstance(node,ast.FunctionDef) and node.name=='run');actual_command=next(node for node in ast.walk(actual_run) if isinstance(node,ast.FunctionDef) and node.name=='command')
   direct=[node.value for node in actual_command.body if isinstance(node,ast.Expr) and isinstance(node.value,ast.Call) and isinstance(node.value.func,ast.Name) and node.value.func.id=='persist_go_test_projection']
   self.assertEqual(len(direct),1);self.assertEqual(ast.dump(direct[0],include_attributes=False),ast.dump(ast.parse("persist_go_test_projection(out,folder/'stdout',label,command_index)").body[0].value,include_attributes=False))
   parent=run.module('ci/backend-runtime-join-r7/run.py','group_recovery_pure_auditor');events=[]
   for package,tops in control['targets'].items():
    events.append({'Action':'start','Package':parent.PREFIX+package})
    for top in tops:
     events.append({'Action':'run','Package':parent.PREFIX+package,'Test':top})
     for child in control['requiredSubtests'][package].get(top,[]):
      name=top+'/'+child;events.extend([{'Action':'run','Package':parent.PREFIX+package,'Test':name},{'Action':'output','Package':parent.PREFIX+package,'Test':name,'Output':'PRIVATE_OUTPUT'},{'Action':'pass','Package':parent.PREFIX+package,'Test':name}])
     events.append({'Action':'pass','Package':parent.PREFIX+package,'Test':top})
    events.append({'Action':'pass','Package':parent.PREFIX+package})
   for mode in ('complete','missing-child','foreign-top','skip','missing-footer','phase-error'):
    with self.subTest(groupRecovery=mode),tempfile.TemporaryDirectory() as directory:
     out=Path(directory).resolve();seen=[];environment={'GOPROXY':'off','GOSUMDB':'off'}
     def command(label,argv,seconds,actual_environment):
      seen.append(label);self.assertEqual(label,control['phase']);self.assertEqual(seconds,600);self.assertEqual(actual_environment,environment);self.assertEqual(argv[-3:],['./'+package for package in control['targets']]);self.assertIn('-race',argv);self.assertIn('-timeout=180s',argv)
      pattern=__import__('re').compile(argv[argv.index('-run')+1]);self.assertTrue(all(pattern.fullmatch(top) for tops in control['targets'].values() for top in tops));self.assertIsNone(pattern.fullmatch('TestUnrelated'))
      if mode=='phase-error':raise ValueError('original three-package phase failed')
      selected=copy.deepcopy(events)
      if mode=='missing-child':selected=[row for row in selected if row.get('Test')!=events[-3]['Test']]
      if mode=='foreign-top':selected[-2]['Test']='TestForeign'
      if mode=='skip':selected[-2]['Action']='skip'
      if mode=='missing-footer':selected=selected[:-1]
      raw=('\n'.join(json.dumps(row) for row in selected)+'\n').encode();stdout=out/'private-stdout';stdout.write_bytes(raw);self.assertTrue(run.persist_go_test_projection(out,stdout,label,1));stdout.unlink();return raw
     if mode!='complete':
      with self.assertRaises(ValueError):run.run_current_group_recovery_control('/owned/go',environment,command,parent,out)
      self.assertEqual(seen,[control['phase']]);self.assertFalse((out/'current-group-recovery-controls-names.json').exists());continue
     value=run.run_current_group_recovery_control('/owned/go',environment,command,parent,out);self.assertEqual(seen,[control['phase']]);self.assertEqual((value['topPASS'],value['subPASS'],value['packages']),(4,23,3));self.assertEqual(value['packageFacts']['pkg/common/db/cache']['topPASS'],2);self.assertFalse(value['wholeSourceAccepted'])
     projected=json.loads((out/'current-group-recovery-controls-events.json').read_bytes());self.assertNotIn('PRIVATE_OUTPUT',json.dumps(projected));self.assertFalse(run.persist_go_test_projection(out,out/'absent-stdout','PRIVATE_PHASE',2))
     run.write(out/'phase-0001.json',{'phase':control['phase'],'exitCode':0,'resource':{'status':'passed','exit_code':0}});run.write(out/'source-post.json',{'accepted':True,'source':run.FINAL_SOURCE});tools={'go':run.COMPLETED_KAFKA_PACKAGE_GO};run.write(out/'tools-before.json',tools);run.write(out/'tools-after.json',tools)
     self.assertEqual(collector.checked_current_group_recovery_evidence(out),value)
     checked=('current-group-recovery-controls-events.json','phase-0001.json','source-post.json','tools-after.json','group-recovery-historical-facts.json','release-controls-historical-pass.json','kafka-confirm-historical-pass.json','kafka-acl-historical-pass.json','process-image-historical-pass.json');original={name:(out/name).read_bytes() for name in checked}
     for changed in checked:
      for name,raw in original.items():(out/name).write_bytes(raw)
      (out/changed).write_text(json.dumps(projected[:-1]) if changed.endswith('-events.json') else '{}')
      with self.subTest(custody=changed),self.assertRaises(ValueError):collector.checked_current_group_recovery_evidence(out)
     for name,raw in original.items():(out/name).write_bytes(raw)
     events_path=out/'current-group-recovery-controls-events.json';saved=events_path.read_bytes();events_path.unlink()
     with self.assertRaises(OSError):collector.checked_current_group_recovery_evidence(out)
     events_path.write_bytes(saved)
     for fields in ({'topPASS':True},{'subPASS':22},{'packages':2},{'actualKafkaACLTestsDispatched':0},{'actualPackageExecutions':1},{'runtimeAccepted':True},{'source':run.GROUP_RECOVERY_HISTORICAL_FACTS['source']}):
      with self.assertRaises(ValueError):run.check_current_group_recovery_receipt(dict(value,**fields))
     for alias in ('same-source-completed-kafka-package-reuse.json','current-managed-producer-control-names.json','current-managed-shape-controls-names.json','current-release-controls-names.json','kafka-acl-passed-reuse.json'):
      run.write(out/alias,{})
      with self.assertRaises(ValueError):collector.checked_current_group_recovery_evidence(out)
      (out/alias).unlink()
     run.write_current_group_recovery_summary(out,value,None,{},{});summary=json.loads((out/'summary.json').read_bytes());self.assertEqual(summary['currentFreshGo'],{'topPASS':6,'subPASS':26,'includesOriginalGraph':True});self.assertFalse(summary['groupRecoveryHistoricalFacts']['producerEventGap']['collectorAccepted'])

 def test_rpc_group_cached_exit2_projection_keeps_fixed_source_classes_and_private_file_fences(self):
  collector=load('collect');scenario=run.SCENARIOS[1];main='server/cmd/openim-rpc/openim-rpc-group/main.go';bodies={main:b'one\n'*35}
  terminal={'version':'owned-fullgraph-token-v2-result-v1','accepted':False,'scenario':scenario,'sourceSHA256':run.FINAL_SOURCE['digest'],'constructorStage':'start','constructorRole':'rpc-group','constructorCallState':'returned','constructorErrorClass':'owned-evidence','constructorPredicate':'start-service-process-image-after-identity-wait-exit-code-2'}
  self.assertTrue(collector.rpc_group_start_exit2(terminal));self.assertFalse(collector.gateway_start_exit2(terminal))
  raw=b'panic: group cache recovery initialization failed: PRIVATE_TOKEN\ngroup authority storage is not ready: PRIVATE_PATH\n\ngoroutine 1 [running]:\nmain.main()\n\t/private/work/'+main.encode()+b':31 +0x1\n'
  value=collector.project_rpc_group_log_bytes(raw,bodies);self.assertEqual(value['errorPrefixClass'],'group-cache-recovery');self.assertEqual(value['panicPrefixClasses'],['group-cache-recovery','group-authority-storage']);self.assertEqual(value['sourceFrames'],[{'path':main,'line':31}]);self.assertFalse(value['prefixIsCauseProof']);self.assertNotIn('PRIVATE',json.dumps(value));self.assertNotIn('/private',json.dumps(value))
  for fields in ({'constructorRole':'push'},{'constructorPredicate':'start-service-process-image-after-identity-wait-exit-code-1'},{'sourceSHA256':'f'*64},{'accepted':True},{'constructorCallState':'unknown'}):self.assertFalse(collector.rpc_group_start_exit2(dict(terminal,**fields)))
  with tempfile.TemporaryDirectory() as directory:
   work=Path(directory).resolve();work.chmod(0o700)
   for folder in (work/'services',work/'services/required',work/'services/required/rpc-group'):folder.mkdir(mode=0o700)
   folder=work/'services/required/rpc-group';path=folder/('service-'+'a'*64+'.log');path.write_bytes(raw);path.chmod(0o600)
   self.assertEqual(collector.project_rpc_group_start_log(work,terminal,bodies)['status'],'projected');self.assertIsNone(collector.project_service_start_log(work,terminal,bodies,'push'))
   path.chmod(0o644);self.assertEqual(collector.project_rpc_group_start_log(work,terminal,bodies)['status'],'unsafe');path.chmod(0o600)
   link=folder/'hardlink';os.link(path,link);self.assertEqual(collector.project_rpc_group_start_log(work,terminal,bodies)['status'],'unsafe');link.unlink()
   saved=work/'saved-log';path.rename(saved);path.symlink_to(saved);self.assertEqual(collector.project_rpc_group_start_log(work,terminal,bodies)['status'],'unsafe');path.unlink();saved.rename(path)
   real=work/'real-rpc-group';folder.rename(real);folder.symlink_to(real,target_is_directory=True);self.assertEqual(collector.project_rpc_group_start_log(work,terminal,bodies)['status'],'unsafe')


 def test_cache_test_delta_bridge_preserves_completed_packages_and_requires_fresh_cache(self):
  collector=load('collect')
  with mock.patch.object(run,'FINAL_SOURCE',run.CRON_FIXTURE_SCOPE_REUSE['originalSource']),mock.patch.object(collector.run,'FINAL_SOURCE',run.CRON_FIXTURE_SCOPE_REUSE['originalSource']):
   control=run.CURRENT_CACHE_TEST_DELTA_CONTROL;locked=json.loads(run.INPUTS.read_bytes());locked.update(source=run.FINAL_SOURCE,currentCronFixtureControl=None,cronFixtureScopeReuse=None,cronFixtureTestsToRun=[],currentCacheTestDeltaControl=copy.deepcopy(control),cacheTestDeltaTestsToRun=control['targets'][next(iter(control['targets']))],cacheTestDeltaPackageReuse=copy.deepcopy(run.CACHE_TEST_DELTA_PACKAGE_REUSE));self.assertEqual(run.check_cache_test_delta_plan(locked),control);run.check_locked(locked)
   self.assertEqual((control['topPASS'],control['subPASS'],control['packages'],control['phaseGuardSeconds'],control['goTimeoutSeconds']),(2,7,1,600,180));self.assertEqual(locked['kafkaACLTestsToRun'],[])
   for fields in ({'kafkaACLTestsToRun':['TestOwnedKafkaExclusionRequiresEveryWildcardDeny']},{'currentGroupRecoveryControl':run.CURRENT_GROUP_RECOVERY_CONTROL},{'cacheTestDeltaPackageReuse':{}},{'cacheTestDeltaTestsToRun':[]}):
    with self.assertRaises(ValueError):run.check_cache_test_delta_plan(dict(locked,**fields))
   parsed=ast.parse(Path(run.__file__).read_text());actual_run=next(node for node in parsed.body if isinstance(node,ast.FunctionDef) and node.name=='run');actual_command=next(node for node in ast.walk(actual_run) if isinstance(node,ast.FunctionDef) and node.name=='command');calls=[node.value for node in actual_command.body if isinstance(node,ast.Expr) and isinstance(node.value,ast.Call) and isinstance(node.value.func,ast.Name) and node.value.func.id=='persist_go_test_projection'];self.assertEqual(len(calls),1)
   class Recipe:
    def rows(self,bodies):return [{'path':name,'bytes':len(raw),'sha256':run.sha(raw)} for name,raw in sorted(bodies.items())]
    def digest(self,rows):return run.sha(b''.join(row['path'].encode()+b'\0'+row['sha256'].encode()+b'\n' for row in sorted(rows,key=lambda row:row['path'])))
   recipe=Recipe();reuse=copy.deepcopy(run.CACHE_TEST_DELTA_PACKAGE_REUSE);delta=reuse['delta'];raw=run.regular(run.REPO/'ci/candidates/backend-fullgraph/source'/delta['target']);bodies={delta['target']:raw,'server/pkg/common/db/cache/fixture.go':b'package cache\n','server/go.mod':b'module fixture\n'}
   left,right=raw.index(delta['contextStart'].encode()),raw.index(delta['contextEnd'].encode());prior=raw[:left]+raw[left:right].replace(delta['newLiteral'].encode(),delta['oldLiteral'].encode(),1)+raw[right:];old_bodies=dict(bodies);old_bodies[delta['target']]=prior
   current={'members':len(bodies),'digest':recipe.digest(recipe.rows(bodies))};original={'members':len(old_bodies),'digest':recipe.digest(recipe.rows(old_bodies))};reuse.update(currentSource=current,originalSource=original);reuse['originalBinding']['source']=original
   for value in reuse['originalBinding']['completedPackages'].values():value['source']=original
   parent=run.module('ci/backend-runtime-join-r7/run.py','cache_delta_pure_auditor');package=next(iter(control['targets']));events=[{'Action':'start','Package':parent.PREFIX+package}]
   for top in control['targets'][package]:
    events.append({'Action':'run','Package':parent.PREFIX+package,'Test':top})
    for child in control['requiredSubtests'][package].get(top,[]):
     name=top+'/'+child;events.extend([{'Action':'run','Package':parent.PREFIX+package,'Test':name},{'Action':'output','Package':parent.PREFIX+package,'Test':name,'Output':'PRIVATE_OUTPUT'},{'Action':'pass','Package':parent.PREFIX+package,'Test':name}])
    events.append({'Action':'pass','Package':parent.PREFIX+package,'Test':top})
   events.append({'Action':'pass','Package':parent.PREFIX+package})
   with mock.patch.object(run,'FINAL_SOURCE',current),mock.patch.object(run,'CACHE_TEST_DELTA_PACKAGE_REUSE',reuse),mock.patch.object(collector.run,'FINAL_SOURCE',current),mock.patch.object(collector.run,'CACHE_TEST_DELTA_PACKAGE_REUSE',reuse),mock.patch.object(collector.run,'verify',return_value=(None,bodies,recipe,None,None,None)):
    self.assertEqual(run.check_cache_test_delta_reuse(reuse,bodies,recipe,reuse['originalBinding']['GoToolBodyAndMode']),reuse)
    for fields in ({'sameWholeSource':True},{'originalGoCommandPassed':True},{'originalWholeScopeAccepted':True},{'actualCurrentKafkaCommands':1},{'oldCachePackagePassed':True},{'currentSource':original}):
     with self.assertRaises(ValueError):run.check_cache_test_delta_reuse(dict(reuse,**fields),bodies,recipe)
    for changed in ({},{**bodies,'server/go.mod':b'changed'},{**bodies,delta['target']:raw+b'\n'},{**bodies,'server/extra.go':b'package extra'}):
     with self.assertRaises(ValueError):run.check_cache_test_delta_reuse(reuse,changed,recipe)
    with self.assertRaises(ValueError):run.check_cache_test_delta_reuse(reuse,bodies,recipe,dict(reuse['originalBinding']['GoToolBodyAndMode'],sha256='f'*64))
    for mode in ('complete','missing-child','skip','missing-footer','phase-error'):
     with self.subTest(cacheDelta=mode),tempfile.TemporaryDirectory() as directory:
      out=Path(directory).resolve();seen=[]
      def command(label,argv,seconds,environment):
       seen.append(label);self.assertEqual(label,control['phase']);self.assertEqual(seconds,600);self.assertEqual(argv[-1],'./'+package);self.assertIn('-race',argv);self.assertIn('-timeout=180s',argv);self.assertEqual(len([value for value in argv if value.startswith('./')]),1)
       if mode=='phase-error':raise ValueError('original cache phase failed')
       selected=copy.deepcopy(events)
       if mode=='missing-child':selected=[row for row in selected if row.get('Test')!=events[-3]['Test']]
       if mode=='skip':selected[-2]['Action']='skip'
       if mode=='missing-footer':selected=selected[:-1]
       raw=('\n'.join(json.dumps(row) for row in selected)+'\n').encode();stdout=out/'private-stdout';stdout.write_bytes(raw);self.assertTrue(run.persist_go_test_projection(out,stdout,label,1));stdout.unlink();return raw
      if mode!='complete':
       with self.assertRaises(ValueError):run.run_current_cache_test_delta_control('/owned/go',{},command,parent,out,bodies,recipe,reuse['originalBinding']['GoToolBodyAndMode'])
       self.assertEqual(seen,[control['phase']]);self.assertFalse((out/'current-cache-test-delta-controls-names.json').exists());continue
      value=run.run_current_cache_test_delta_control('/owned/go',{},command,parent,out,bodies,recipe,reuse['originalBinding']['GoToolBodyAndMode']);self.assertEqual(seen,[control['phase']]);self.assertEqual((value['topPASS'],value['subPASS'],value['packages']),(2,7,1));self.assertEqual(value['actualKafkaACLTestsDispatched'],0)
      projected=json.loads((out/'current-cache-test-delta-controls-events.json').read_bytes());self.assertNotIn('PRIVATE_OUTPUT',json.dumps(projected));run.write(out/'phase-0001.json',{'phase':control['phase'],'exitCode':0,'resource':{'status':'passed','exit_code':0}});run.write(out/'source-post.json',{'accepted':True,'source':current});tools={'go':reuse['originalBinding']['GoToolBodyAndMode']};run.write(out/'tools-before.json',tools);run.write(out/'tools-after.json',tools)
      self.assertEqual(collector.checked_current_cache_test_delta_evidence(out),value)
      checked=('current-cache-test-delta-controls-events.json','completed-package-test-delta-reuse.json','phase-0001.json','source-post.json','tools-after.json');saved={name:(out/name).read_bytes() for name in checked}
      for changed in checked:
       for name,body in saved.items():(out/name).write_bytes(body)
       (out/changed).write_text(json.dumps(projected[:-1]) if changed.endswith('-events.json') else '{}')
       with self.subTest(cacheCustody=changed),self.assertRaises(ValueError):collector.checked_current_cache_test_delta_evidence(out)
      for name,body in saved.items():(out/name).write_bytes(body)
      for alias in ('current-group-recovery-controls-names.json','current-managed-producer-control-names.json','kafka-acl-passed-reuse.json'):
       run.write(out/alias,{})
       with self.assertRaises(ValueError):collector.checked_current_cache_test_delta_evidence(out)
       (out/alias).unlink()
      run.write_current_cache_test_delta_summary(out,value,None,{},{});summary=json.loads((out/'summary.json').read_bytes());self.assertEqual(summary['currentFreshGo'],{'topPASS':4,'subPASS':10,'includesOriginalGraph':True});self.assertFalse(summary['sameWholeSource']);self.assertFalse(summary['originalGoCommandPassed'])
   # Even a self-consistent old/new source vector cannot bridge a cache embed.
   embedded=dict(bodies);embedded['server/pkg/common/db/cache/fixture.go']=b'package cache\n//go:embed *.go\n';old_embedded=dict(embedded);old_embedded[delta['target']]=prior;bad=copy.deepcopy(reuse);bad['currentSource']={'members':len(embedded),'digest':recipe.digest(recipe.rows(embedded))};bad['originalSource']={'members':len(old_embedded),'digest':recipe.digest(recipe.rows(old_embedded))};bad['originalBinding']['source']=bad['originalSource']
   with mock.patch.object(run,'FINAL_SOURCE',bad['currentSource']),mock.patch.object(run,'CACHE_TEST_DELTA_PACKAGE_REUSE',bad),self.assertRaises(ValueError):run.check_cache_test_delta_reuse(bad,embedded,recipe)


 def test_cron_fixture_dependency_projection_is_complete_bounded_and_excludes_fixture(self):
  policy=run.CRON_FIXTURE_DEPENDENCY_CONTROL;roots=policy['roots'];rows=[]
  for root in roots:rows.extend([{'ImportPath':root},{'ImportPath':root+' ['+root+'.test]','ForTest':root},{'ImportPath':root+'.test'}])
  raw=lambda rows:('\n'.join(json.dumps(row) for row in rows)+'\n').encode()
  value=run.project_cron_fixture_dependencies(raw(rows));self.assertEqual(value['packageCount'],9);self.assertFalse(value['runtimeAccepted']);self.assertEqual(len(value['packages']),9)
  with_private=copy.deepcopy(rows);with_private[0]['Dir']='/PRIVATE/cache';with_private[0]['Module']={'Dir':'/PRIVATE/mod'};self.assertNotIn('PRIVATE',json.dumps(run.project_cron_fixture_dependencies(raw(with_private))))
  forbidden=policy['forbiddenPackage']
  for added in ({'ImportPath':forbidden},{'ImportPath':forbidden+'.test'},{'ImportPath':forbidden+' ['+forbidden+'.test]','ForTest':forbidden},{'ImportPath':forbidden+'_test'},{'ImportPath':roots[0],'Error':{'Err':'PRIVATE'}},{'ImportPath':'unsafe','Incomplete':True},{'ImportPath':'unsafe','DepsErrors':[{'Err':'PRIVATE'}]},{'ImportPath':'/PRIVATE/path'},{'ImportPath':'unsafe','ForTest':'foreign'},rows[0]):
   with self.subTest(dependency=added),self.assertRaises(ValueError):run.project_cron_fixture_dependencies(raw(rows+[added]))
  for bad in (b'',raw(rows)[:-2],raw(rows[:-1]),raw(rows)+b'{bad}\n',b'{"ImportPath":"unsafe","ImportPath":"other"}\n',raw(rows)+b'{"ImportPath":NaN}\n'):
   with self.assertRaises(ValueError):run.project_cron_fixture_dependencies(bad)
  bad=copy.deepcopy(value);bad['packageCount']=True
  with self.assertRaises(ValueError):run.check_cron_fixture_dependency_receipt(bad)
  with mock.patch.dict(run.CRON_FIXTURE_DEPENDENCY_CONTROL,packagesLimit=8),self.assertRaises(ValueError):run.project_cron_fixture_dependencies(raw(rows))

 def test_cron_fixture_scope_reuse_requires_actual_dependencies_and_fresh_contract(self):
  collector=load('collect');control=run.CURRENT_CRON_FIXTURE_CONTROL;locked=json.loads(run.INPUTS.read_bytes());locked.update(currentCronFixtureControl=copy.deepcopy(control),cronFixtureTestsToRun=control['targets']['internal/runtimeacceptance'],cronFixtureScopeReuse=copy.deepcopy(run.CRON_FIXTURE_SCOPE_REUSE),cronFixtureDependencyControl=copy.deepcopy(run.CRON_FIXTURE_DEPENDENCY_CONTROL));self.assertEqual(run.check_current_cron_fixture_plan(locked),control);run.check_locked(locked)
  for fields in ({'cronFixtureTestsToRun':[]},{'currentCacheTestDeltaControl':run.CURRENT_CACHE_TEST_DELTA_CONTROL},{'kafkaACLTestsToRun':['TestOwnedKafkaExclusionRequiresEveryWildcardDeny']},{'cronFixtureDependencyControl':{}},{'cronFixtureScopeReuse':{}}):
   with self.assertRaises(ValueError):run.check_current_cron_fixture_plan(dict(locked,**fields))
  class Recipe:
   def rows(self,bodies):return [{'path':name,'bytes':len(raw),'sha256':run.sha(raw)} for name,raw in sorted(bodies.items())]
   def digest(self,rows):return run.sha(b''.join(row['path'].encode()+b'\0'+row['sha256'].encode()+b'\n' for row in sorted(rows,key=lambda row:row['path'])))
  recipe=Recipe();compat=copy.deepcopy(run.CRON_FIXTURE_SCOPE_REUSE);delta=compat['bootstrapDelta'];added=compat['newTest'];bodies={delta['target']:run.regular(run.REPO/'ci/candidates/backend-fullgraph/source'/delta['target']),added['target']:run.regular(run.REPO/'ci/candidates/backend-fullgraph/source'/added['target']),'server/go.mod':b'module fixture\n'};old=dict(bodies);del old[added['target']]
  for item in delta['replacements']:old[delta['target']]=old[delta['target']].replace(item['new'].encode(),item['old'].encode(),1)
  current={'members':len(bodies),'digest':recipe.digest(recipe.rows(bodies))};prior={'members':len(old),'digest':recipe.digest(recipe.rows(old))};compat.update(currentSource=current,originalSource=prior);compat['cacheFact']['source']=prior;compat['priorProducerKafkaCompatibility']['currentSource']=prior
  dep_rows=[]
  for root in run.CRON_FIXTURE_DEPENDENCY_CONTROL['roots']:dep_rows.extend([{'ImportPath':root},{'ImportPath':root+' ['+root+'.test]','ForTest':root},{'ImportPath':root+'.test'}])
  dep_raw=('\n'.join(json.dumps(row) for row in dep_rows)+'\n').encode();parent=run.module('ci/backend-runtime-join-r7/run.py','cron_fixture_pure_auditor');package=next(iter(control['targets']));top=control['targets'][package][0];events=[{'Action':'start','Package':parent.PREFIX+package},{'Action':'run','Package':parent.PREFIX+package,'Test':top}]
  for child in control['requiredSubtests'][package][top]:
   name=top+'/'+child;events.extend([{'Action':'run','Package':parent.PREFIX+package,'Test':name},{'Action':'output','Package':parent.PREFIX+package,'Test':name,'Output':'PRIVATE'},{'Action':'pass','Package':parent.PREFIX+package,'Test':name}])
  events.extend([{'Action':'pass','Package':parent.PREFIX+package,'Test':top},{'Action':'pass','Package':parent.PREFIX+package}])
  with mock.patch.object(run,'FINAL_SOURCE',current),mock.patch.object(run,'CRON_FIXTURE_SCOPE_REUSE',compat),mock.patch.object(collector.run,'FINAL_SOURCE',current),mock.patch.object(collector.run,'CRON_FIXTURE_SCOPE_REUSE',compat),mock.patch.object(collector.run,'verify',return_value=(None,bodies,recipe,None,None,None)):
   self.assertEqual(run.check_cron_fixture_scope_source(compat,bodies,recipe),compat)
   for changed in ({},{**bodies,'server/go.mod':b'changed'},{**bodies,delta['target']:bodies[delta['target']]+b'\n'},{**bodies,added['target']:b'changed'},{**bodies,'server/extra.go':b'package extra'}):
    with self.assertRaises(ValueError):run.check_cron_fixture_scope_source(compat,changed,recipe)
   for fields in ({'sameWholeSource':True},{'actualCurrentCacheTestCommands':1},{'runtimeAccepted':True}):
    with self.assertRaises(ValueError):run.check_cron_fixture_scope_source(dict(compat,**fields),bodies,recipe)
   for mode in ('complete','dependency-error','dependency-truncated','dependency-fixture','missing-child','missing-footer','contract-error'):
    with self.subTest(cronFixture=mode),tempfile.TemporaryDirectory() as directory:
     out=Path(directory).resolve();seen=[]
     def command(label,argv,seconds,environment):
      seen.append(label);self.assertEqual(seconds,600)
      if label==run.CRON_FIXTURE_DEPENDENCY_CONTROL['phase']:
       self.assertEqual(argv,['/owned/go','list','-deps','-test','-mod=readonly','-json=ImportPath,ForTest,Error,DepsErrors,Incomplete']+['./'+p for p in run.CRON_FIXTURE_DEPENDENCY_CONTROL['relativeRoots']])
       if mode=='dependency-error':raise ValueError('original go list failed')
       if mode=='dependency-truncated':return dep_raw[:-2]
       if mode=='dependency-fixture':return dep_raw+json.dumps({'ImportPath':run.CRON_FIXTURE_DEPENDENCY_CONTROL['forbiddenPackage']}).encode()+b'\n'
       return dep_raw
      self.assertEqual(label,control['phase']);self.assertEqual(argv[-1],'./'+package);self.assertIn('-timeout=180s',argv);self.assertIn('-race',argv)
      if mode=='contract-error':raise ValueError('actual contract failed')
      selected=copy.deepcopy(events)
      if mode=='missing-child':selected=[row for row in selected if row.get('Test')==top or row.get('Test')!=top+'/'+control['requiredSubtests'][package][top][-1]]
      if mode=='missing-footer':selected=selected[:-1]
      raw=('\n'.join(json.dumps(row) for row in selected)+'\n').encode();stdout=out/'private-stdout';stdout.write_bytes(raw);self.assertTrue(run.persist_go_test_projection(out,stdout,label,2));stdout.unlink();return raw
     if mode!='complete':
      with self.assertRaises(ValueError):run.run_current_cron_fixture_control('/owned/go',{},command,parent,out,bodies,recipe,compat['cacheFact']['GoToolBodyAndMode'])
      self.assertEqual(seen[0],run.CRON_FIXTURE_DEPENDENCY_CONTROL['phase']);self.assertLessEqual(len(seen),2);self.assertFalse((out/'current-cron-fixture-contract-names.json').exists());continue
     value=run.run_current_cron_fixture_control('/owned/go',{},command,parent,out,bodies,recipe,compat['cacheFact']['GoToolBodyAndMode']);self.assertEqual(seen,[run.CRON_FIXTURE_DEPENDENCY_CONTROL['phase'],control['phase']]);self.assertEqual((value['topPASS'],value['subPASS'],value['packages']),(1,3,1));self.assertEqual(value['actualCacheTopsDispatched'],0)
     for index,label in enumerate(seen,1):run.write(out/('phase-%04d.json'%index),{'phase':label,'exitCode':0,'resource':{'status':'passed','exit_code':0}})
     run.write(out/'source-post.json',{'accepted':True,'source':current});tools={'go':compat['cacheFact']['GoToolBodyAndMode']};run.write(out/'tools-before.json',tools);run.write(out/'tools-after.json',tools);self.assertEqual(collector.checked_current_cron_fixture_evidence(out),value)
     names=('cron-fixture-reused-scope-deps.json','cron-fixture-named-scope-reuse.json','current-cron-fixture-contract-events.json','phase-0001.json','phase-0002.json','source-post.json','tools-after.json');saved={name:(out/name).read_bytes() for name in names}
     for changed in names:
      for name,body in saved.items():(out/name).write_bytes(body)
      (out/changed).write_text('{}')
      with self.subTest(cronCustody=changed),self.assertRaises(ValueError):collector.checked_current_cron_fixture_evidence(out)
     for name,body in saved.items():(out/name).write_bytes(body)
     for alias in ('current-cache-test-delta-controls-names.json','current-group-recovery-controls-names.json','kafka-acl-passed-reuse.json'):
      run.write(out/alias,{})
      with self.assertRaises(ValueError):collector.checked_current_cron_fixture_evidence(out)
      (out/alias).unlink()
     run.write_current_cron_fixture_summary(out,value,None,{},{});summary=json.loads((out/'summary.json').read_bytes());self.assertEqual(summary['currentFreshGo'],{'topPASS':3,'subPASS':6,'includesOriginalGraph':True});self.assertFalse(summary['sameWholeSource'])


 def test_same_current_go_named_scope_reuse_keeps_original_counts_without_dispatch(self):
  collector=load('collect');locked=json.loads(run.INPUTS.read_bytes());locked.update(source=copy.deepcopy(run.CURRENT_GO_NAMED_SCOPES_REUSE['source']),passedCurrentGoNamedScopes=copy.deepcopy(run.CURRENT_GO_NAMED_SCOPES_REUSE),currentGroupRegistryFixtureControl=None,groupRegistryFixtureTestsToRun=[])
  with mock.patch.object(run,'FINAL_SOURCE',run.CURRENT_GO_NAMED_SCOPES_REUSE['source']):self.assertTrue(run.current_go_named_scope_reuse_selected(locked));run.check_locked(locked)
  for key in ('currentCronFixtureControl','currentCacheTestDeltaControl','currentGroupRecoveryControl','currentProducerControl','currentManagedShapeControl','currentReleaseControl','currentImageKafkaControl','currentKafkaConfirmControl'):
   self.assertFalse(run.current_go_named_scope_reuse_selected(dict(locked,**{key:{'legacy':'explicit'}})))
  self.assertEqual(locked['cronFixtureTestsToRun'],[]);self.assertEqual(locked['kafkaACLTestsToRun'],[])
  value=copy.deepcopy(run.CURRENT_GO_NAMED_SCOPES_REUSE)
  for fields in ({'actualCurrentContractCommands':1},{'actualCurrentDependencyGoListInvocations':1},{'wholePackagePASS':True},{'source':run.CRON_FIXTURE_SCOPE_REUSE['originalSource']}):
   with self.assertRaises(ValueError):run.check_current_go_named_scope_reuse(dict(value,**fields))
  class Recipe:
   def rows(self,bodies):return [{'path':name,'sha256':run.sha(raw)} for name,raw in sorted(bodies.items())]
   def digest(self,rows):return run.sha(json.dumps(rows,sort_keys=True).encode())
  recipe=Recipe();bodies={'server/fixture.go':b'package fixture\n'};source={'members':1,'digest':recipe.digest(recipe.rows(bodies))};value['source']=source;value['binding']['source']=source;value['binding']['freshContractReceipt']['source']=source;previous=copy.deepcopy(value['binding']['previousCompatibility']);previous['currentSource']=source;value['binding']['previousCompatibility']=previous;go=value['binding']['GoToolBodyAndMode']
  with mock.patch.object(run,'FINAL_SOURCE',source),mock.patch.object(run,'CURRENT_GO_NAMED_SCOPES_REUSE',value),mock.patch.object(run,'CRON_FIXTURE_SCOPE_REUSE',previous),mock.patch.object(collector.run,'FINAL_SOURCE',source),mock.patch.object(collector.run,'CURRENT_GO_NAMED_SCOPES_REUSE',value),mock.patch.object(collector.run,'CRON_FIXTURE_SCOPE_REUSE',previous),mock.patch.object(collector.run,'verify',return_value=(None,bodies,recipe,None,None,None)),tempfile.TemporaryDirectory() as directory:
   out=Path(directory).resolve();self.assertEqual(run.load_current_go_named_scope_reuse(out,bodies,recipe,go),value);self.assertEqual(len(list(out.iterdir())),1)
   with self.assertRaises(ValueError):run.check_current_go_named_scope_reuse(value,{'server/fixture.go':b'changed'},recipe,go)
   with self.assertRaises(ValueError):run.check_current_go_named_scope_reuse(value,bodies,recipe,dict(go,sha256='f'*64))
   run.write(out/'source-post.json',{'accepted':True,'source':source});run.write(out/'tools-before.json',{'go':go});run.write(out/'tools-after.json',{'go':go});self.assertEqual(collector.checked_current_go_named_scope_reuse_evidence(out),value)
   for name in ('current-cron-fixture-contract-names.json','cron-fixture-reused-scope-deps.json','kafka-acl-passed-reuse.json'):
    run.write(out/name,{})
    with self.assertRaises(ValueError):collector.checked_current_go_named_scope_reuse_evidence(out)
    (out/name).unlink()
   run.write_current_go_named_scope_reuse_summary(out,value,None,{},{});summary=json.loads((out/'summary.json').read_bytes());self.assertEqual(summary['currentFreshGo'],{'topPASS':2,'subPASS':3,'includesOriginalGraph':True});self.assertEqual(summary['actualDependencyGoListInvocations'],0)

 def test_rpc_group_registration_done_binds_original_owner_log_without_inventing_exit(self):
  collector=load('collect');terminal={'version':'owned-fullgraph-token-v2-result-v1','accepted':False,'scenario':run.SCENARIOS[1],'sourceSHA256':run.FINAL_SOURCE['digest'],'constructorRole':'rpc-group','constructorStage':'observe-registration','constructorCallState':'returned','constructorErrorClass':'owned-evidence','constructorPredicate':'done'};main='server/cmd/openim-rpc/openim-rpc-group/main.go';bodies={main:b'line\n'*40}
  self.assertTrue(collector.rpc_group_registration_done(terminal));self.assertFalse(collector.rpc_group_start_exit2(terminal))
  for fields in ({'constructorRole':'crontask'},{'constructorPredicate':'registration-absent'},{'constructorStage':'start'},{'accepted':True},{'sourceSHA256':'f'*64}):self.assertFalse(collector.rpc_group_registration_done(dict(terminal,**fields)))
  with tempfile.TemporaryDirectory() as directory:
   work=Path(directory).resolve();work.chmod(0o700)
   for folder in (work/'services',work/'services/required',work/'services/required/rpc-group',work/'runtime-owners'):folder.mkdir(mode=0o700)
   nonce='a'*64;owner={'role':'rpc-group','pid':123,'uid':os.getuid(),'startToken':'123','bootID':'synthetic-original-boot','executable':str(work.parent/'bin/rpc-group'),'binarySHA256':'b'*64,'sourceSHA256':run.FINAL_SOURCE['digest'],'buildReceiptSHA256':'c'*64,'bootstrapSHA256':'d'*64,'ownerNonce':nonce};owner_path=work/'runtime-owners/rpc-group-runtime-actual-owner.json';owner_path.write_text(json.dumps(owner));owner_path.chmod(0o600)
   path=work/'services/required/rpc-group'/('service-'+nonce+'.log');path.write_bytes(b'panic: owned group receiver initialization failed: PRIVATE_TOKEN\n\ngoroutine 1 [running]:\nmain.main()\n\t/private/work/'+main.encode()+b':31 +0x1\n');path.chmod(0o600)
   value=collector.project_rpc_group_start_log(work,terminal,bodies);self.assertEqual(value['status'],'projected');self.assertEqual(value['errorPrefixClass'],'group-receiver-initialization');self.assertTrue(value['originalWaitReturned']);self.assertTrue(value['stdoutSyncAndCloseCallsReturned']);self.assertEqual(value['originalWaitResult'],'not-exported');self.assertEqual(value['stdoutFinalizationResult'],'not-exported');self.assertFalse(value['exitCodeInferredFromLog']);self.assertNotIn('PRIVATE',json.dumps(value));self.assertNotIn('/private',json.dumps(value));self.assertNotIn(nonce,json.dumps(value))
   for fields in ({'ownerNonce':'e'*64},{'sourceSHA256':'f'*64},{'uid':os.getuid()+1},{'role':'push'}):
    owner_path.write_text(json.dumps(dict(owner,**fields)));self.assertEqual(collector.project_rpc_group_start_log(work,terminal,bodies)['status'],'unsafe')
   owner_path.write_text(json.dumps(owner));owner_path.chmod(0o644);self.assertEqual(collector.project_rpc_group_start_log(work,terminal,bodies)['status'],'unsafe');owner_path.chmod(0o600)
   saved=work/'saved-owner';owner_path.rename(saved);owner_path.symlink_to(saved);self.assertEqual(collector.project_rpc_group_start_log(work,terminal,bodies)['status'],'unsafe');owner_path.unlink();saved.rename(owner_path)
   owner_path.unlink();self.assertEqual(collector.project_rpc_group_start_log(work,terminal,bodies)['status'],'unavailable')

 def test_unaccepted_compile_cache_snapshot_matches_original_receipt_without_runtime_promotion(self):
  collector=load('collect');body=bytearray(128);body[:6]=b'\x7fELF\x02\x01';body[16:20]=b'\x02\x00\x3e\x00';body=bytes(body)
  with tempfile.TemporaryDirectory() as directory:
   root=Path(directory).resolve();root.chmod(0o700);source=root/'rpc-group';source.write_bytes(body);source.chmod(0o700);snapshot=root/'snapshot';snapshot.mkdir(mode=0o700);expected={'bytes':len(body),'sha256':run.sha(body)}
   value=collector.snapshot_compile_cache_elf(source,snapshot/'rpc-group',expected,1<<20);self.assertEqual((snapshot/'rpc-group').read_bytes(),body);self.assertEqual(value['snapshotMode'],0o500);self.assertEqual(source.stat().st_mode&0o777,0o700)
   for name,wanted,remaining in [('bad-hash',dict(expected,sha256='f'*64),1<<20),('too-small',expected,1),('bad-bytes',dict(expected,bytes=True),1<<20)]:
    with self.assertRaises(ValueError):collector.snapshot_compile_cache_elf(source,snapshot/name,wanted,remaining)
   linked=root/'linked';os.link(source,linked)
   with self.assertRaises(ValueError):collector.snapshot_compile_cache_elf(source,snapshot/'hardlink-refused',expected,1<<20)
   linked.unlink()
   fifo=root/'fifo';os.mkfifo(fifo,0o700)
   with self.assertRaises(ValueError):collector.snapshot_compile_cache_elf(fifo,snapshot/'fifo-refused',expected,1<<20)
   self.assertFalse((snapshot/'fifo-refused').exists())
   with mock.patch.object(collector.os,'statvfs',return_value=type('Space',(),{'f_bavail':0,'f_frsize':4096})()),self.assertRaises(ValueError):collector.snapshot_compile_cache_elf(source,snapshot/'no-space',expected,1<<20)
  # Separate evidence kind preserves original canonical-cleanup rejection.
  with tempfile.TemporaryDirectory() as directory:
   root=Path(directory).resolve();allocation=root/'allocation';allocation.mkdir();compact=root/'compact';compact.mkdir();c=json.loads(run.INPUTS.read_bytes());guard={'guard_revision':2,'environment_values_recorded':False,'allocated_root':str(allocation),'minimum_free_required_bytes':1<<30,'minimum_observed_free_bytes':2<<30,'max_allocated_allowed_bytes':16<<30,'maximum_observed_task_allocated_bytes':1,'status':'failed','exit_code':1};records={'source-post.json':{'accepted':True,'source':c['source']},'tools-before.json':{'go':{'sha256':'a'*64}},'tools-after.json':{'go':{'sha256':'a'*64}},'backend-fullgraph-controller.resource.json':guard};phases=[]
   for index,role in enumerate((*run.ROLES,'acceptance-operator','msgtransfer-legacy'),1):
    phases.append({'phase':'compile-'+role,'exitCode':0,'resource':dict(guard,allocated_root=str(allocation/'work'),status='passed',exit_code=0)})
    if role in run.ROLES:
     entry=next(row for row in c['entrypointContract']['entrypoints'] if row['role']==role);records['compiled-'+role+'.json']={'source':c['source'],'role':role,'package':entry['package'],'executed':False,'sha256':'b'*64,'bytes':128}
   phases.append({'phase':'runtime-'+run.SCENARIOS[1],'exitCode':1,'resource':dict(guard,allocated_root=str(allocation/'work'))})
   for index,phase in enumerate(phases,1):records['phase-%04d.json'%index]=phase
   rows=[]
   for index,scenario in enumerate(run.SCENARIOS):
    attempted=index<2;rows.append({'scenario':scenario,'resourceStarted':attempted,'runtimeAttempted':attempted,'runtimeCleanupAccepted':index==0,**({'resourceCleanup':{'accepted':True,'unconfirmedIDs':[]}} if attempted else {}),**({'actualAbsentRoles':2} if index==0 else {})})
    if attempted:records['terminal-'+scenario+'.json']={'version':'owned-fullgraph-token-v2-result-v1','scenario':scenario,'accepted':index==0,'rawErrorText':False,'sourceSHA256':c['source']['digest'],'phase':'actual-twelve-other-source-constructors','actualStartedRoles':14}
   records['collection.json']={'started':True,'controllerPassed':False,'originalRetainedRuntimeJoinRequired':True,'containerAbsenceIsNotRuntimeDrain':True,'scenarios':rows}
   def persist():
    manifest=[]
    for name,value in records.items():
     raw=(json.dumps(value)+'\n').encode();(compact/name).write_bytes(raw);manifest.append({'path':name,'bytes':len(raw),'sha256':run.sha(raw)})
    (compact/'custody.json').write_text(json.dumps({'files':manifest,'bodyBytes':sum(row['bytes'] for row in manifest),'binaries':0,'credentials':0,'privateLogs':0}))
   persist();proof=collector.checked_compile_cache_evidence(compact,c,allocation);self.assertEqual(proof['cleanup'][1]['runtimeBoundary'],'runtime-cleanup-unconfirmed')
   with self.assertRaises(ValueError):collector.checked_build_only_evidence(compact,c,allocation)
   records['collection.json']['scenarios'][1]['resourceCleanup']['accepted']=False;persist()
   with self.assertRaises(ValueError):collector.checked_compile_cache_evidence(compact,c,allocation)


 def test_group_registry_fixture_dependency_projection_rejects_foreign_or_incomplete_scope(self):
  rows=[]
  for root in run.GROUP_REGISTRY_FIXTURE_DEPENDENCY_CONTROL['roots']:rows.extend([{'ImportPath':root},{'ImportPath':root+' ['+root+'.test]','ForTest':root},{'ImportPath':root+'.test'}])
  raw=('\n'.join(json.dumps(row) for row in rows)+'\n').encode();value=run.project_group_registry_fixture_dependencies(raw);self.assertTrue(value['fixturePackageAbsent']);self.assertEqual(value['packageCount'],9)
  for bad in (raw[:-2],raw+json.dumps({'ImportPath':run.GROUP_REGISTRY_FIXTURE_DEPENDENCY_CONTROL['forbiddenPackage']}).encode()+b'\n',raw+json.dumps({'ImportPath':'foreign.test'}).encode()+b'\n',raw+json.dumps(rows[0]).encode()+b'\n',b'{"ImportPath":"x","Error":{"Err":"PRIVATE"}}\n'):
   with self.assertRaises(ValueError):run.project_group_registry_fixture_dependencies(bad)

 def test_group_registry_fixture_reuse_requires_exact_delta_dependency_and_real_event_hook(self):
  collector=load('collect');locked=json.loads(run.INPUTS.read_bytes());self.assertTrue(run.current_group_registry_fixture_selected(locked));run.check_locked(locked);control=run.CURRENT_GROUP_REGISTRY_FIXTURE_CONTROL
  for fields in ({'groupRegistryFixtureTestsToRun':[]},{'groupRegistryFixtureScopeReuse':{}},{'cronFixtureTestsToRun':['TestFreshFixtureCronConfigurationMatchesDefaultScheduler']},{'passedCurrentGoNamedScopes':run.CURRENT_GO_NAMED_SCOPES_REUSE}):
   with self.assertRaises(ValueError):run.check_current_group_registry_fixture_plan(dict(locked,**fields))
  class Recipe:
   def rows(self,bodies):return [{'path':name,'bytes':len(raw),'sha256':run.sha(raw)} for name,raw in sorted(bodies.items())]
   def digest(self,rows):return run.sha(b''.join(row['path'].encode()+b'\0'+row['sha256'].encode()+b'\n' for row in sorted(rows,key=lambda row:row['path'])))
  recipe=Recipe();compat=copy.deepcopy(run.GROUP_REGISTRY_FIXTURE_SCOPE_REUSE);delta=compat['fixtureDelta'];added=compat['newTest'];paths=[delta['target'],added['target']]+[row['target'] for row in compat['unchangedHistoricalCronBodies']];bodies={path:run.regular(run.REPO/'ci/candidates/backend-fullgraph/source'/path) for path in paths};bodies['server/go.mod']=b'module fixture\n';previous=dict(bodies);del previous[added['target']]
  for item in delta['replacements']:previous[delta['target']]=previous[delta['target']].replace(item['new'].encode(),item['old'].encode(),1)
  current={'members':len(bodies),'digest':recipe.digest(recipe.rows(bodies))};prior={'members':len(previous),'digest':recipe.digest(recipe.rows(previous))};compat.update(currentSource=current,originalSource=prior);named=copy.deepcopy(compat['priorAcceptedNamedScopeFacts']);named['source']=prior;named['binding']['source']=prior;compat['priorAcceptedNamedScopeFacts']=named;go=named['binding']['GoToolBodyAndMode'];dep_rows=[]
  for root in run.GROUP_REGISTRY_FIXTURE_DEPENDENCY_CONTROL['roots']:dep_rows.extend([{'ImportPath':root},{'ImportPath':root+' ['+root+'.test]','ForTest':root},{'ImportPath':root+'.test'}])
  dep_raw=('\n'.join(json.dumps(row) for row in dep_rows)+'\n').encode();parent=run.module('ci/backend-runtime-join-r7/run.py','group_registry_fixture_pure_auditor');package=next(iter(control['targets']));top=control['targets'][package][0];events=[{'Action':'start','Package':parent.PREFIX+package},{'Action':'run','Package':parent.PREFIX+package,'Test':top}]
  for child in control['requiredSubtests'][package][top]:
   name=top+'/'+child;events.extend([{'Action':'run','Package':parent.PREFIX+package,'Test':name},{'Action':'output','Package':parent.PREFIX+package,'Test':name,'Output':'PRIVATE'},{'Action':'pass','Package':parent.PREFIX+package,'Test':name}])
  events.extend([{'Action':'pass','Package':parent.PREFIX+package,'Test':top},{'Action':'pass','Package':parent.PREFIX+package}])
  with mock.patch.object(run,'FINAL_SOURCE',current),mock.patch.object(run,'GROUP_REGISTRY_FIXTURE_SCOPE_REUSE',compat),mock.patch.object(run,'CURRENT_GO_NAMED_SCOPES_REUSE',named),mock.patch.object(collector.run,'FINAL_SOURCE',current),mock.patch.object(collector.run,'GROUP_REGISTRY_FIXTURE_SCOPE_REUSE',compat),mock.patch.object(collector.run,'CURRENT_GO_NAMED_SCOPES_REUSE',named),mock.patch.object(collector.run,'verify',return_value=(None,bodies,recipe,None,None,None)):
   self.assertEqual(run.check_group_registry_fixture_scope_source(compat,bodies,recipe,go),compat)
   for bad in ({**bodies,'server/go.mod':b'changed'},{**bodies,delta['target']:bodies[delta['target']]+b'\n'},{**bodies,added['target']:b'changed'}):
    with self.assertRaises(ValueError):run.check_group_registry_fixture_scope_source(compat,bad,recipe,go)
   with self.assertRaises(ValueError):run.check_group_registry_fixture_scope_source(compat,bodies,recipe,dict(go,sha256='f'*64))
   for mode in ('complete','dependency-fixture','dependency-error','missing-child','missing-footer','contract-error'):
    with self.subTest(groupRegistry=mode),tempfile.TemporaryDirectory() as directory:
     out=Path(directory).resolve();seen=[]
     def command(label,argv,seconds,environment):
      seen.append(label);self.assertEqual(seconds,600)
      if label==run.GROUP_REGISTRY_FIXTURE_DEPENDENCY_CONTROL['phase']:
       self.assertEqual(argv,['/owned/go','list','-deps','-test','-mod=readonly','-json=ImportPath,ForTest,Error,DepsErrors,Incomplete']+['./'+p for p in run.GROUP_REGISTRY_FIXTURE_DEPENDENCY_CONTROL['relativeRoots']])
       if mode=='dependency-error':raise ValueError('actual dependency command failed')
       if mode=='dependency-fixture':return dep_raw+json.dumps({'ImportPath':run.GROUP_REGISTRY_FIXTURE_DEPENDENCY_CONTROL['forbiddenPackage']}).encode()+b'\n'
       return dep_raw
      self.assertEqual(label,control['phase']);self.assertIn('-timeout=180s',argv);self.assertIn('-race',argv);self.assertEqual(argv[-1],'./'+package)
      if mode=='contract-error':raise ValueError('actual contract command failed')
      selected=copy.deepcopy(events)
      if mode=='missing-child':selected=[row for row in selected if row.get('Test')!=top+'/'+control['requiredSubtests'][package][top][-1]]
      if mode=='missing-footer':selected=selected[:-1]
      raw=('\n'.join(json.dumps(row) for row in selected)+'\n').encode();stdout=out/'private-stdout';stdout.write_bytes(raw);self.assertTrue(run.persist_go_test_projection(out,stdout,label,2));stdout.unlink();return raw
     if mode!='complete':
      with self.assertRaises(ValueError):run.run_current_group_registry_fixture_control('/owned/go',{},command,parent,out,bodies,recipe,go)
      self.assertLessEqual(len(seen),2);self.assertFalse((out/'current-group-registry-fixture-contract-names.json').exists());continue
     value=run.run_current_group_registry_fixture_control('/owned/go',{},command,parent,out,bodies,recipe,go);self.assertEqual(seen,[run.GROUP_REGISTRY_FIXTURE_DEPENDENCY_CONTROL['phase'],control['phase']]);self.assertEqual((value['topPASS'],value['subPASS']),(1,3));self.assertNotIn('PRIVATE',(out/'current-group-registry-fixture-contract-events.json').read_text())
     for index,label in enumerate(seen,1):run.write(out/('phase-%04d.json'%index),{'phase':label,'exitCode':0,'resource':{'status':'passed','exit_code':0}})
     run.write(out/'source-post.json',{'accepted':True,'source':current});run.write(out/'tools-before.json',{'go':go});run.write(out/'tools-after.json',{'go':go});self.assertEqual(collector.checked_current_group_registry_fixture_evidence(out),value)
     saved=(out/'current-group-registry-fixture-contract-events.json').read_bytes();(out/'current-group-registry-fixture-contract-events.json').write_text('[]')
     with self.assertRaises(ValueError):collector.checked_current_group_registry_fixture_evidence(out)
     (out/'current-group-registry-fixture-contract-events.json').write_bytes(saved);run.write(out/'same-source-current-go-named-scopes-reuse.json',{})
     with self.assertRaises(ValueError):collector.checked_current_group_registry_fixture_evidence(out)
     (out/'same-source-current-go-named-scopes-reuse.json').unlink();run.write_current_group_registry_fixture_summary(out,value,None,{},{});summary=json.loads((out/'summary.json').read_bytes());self.assertEqual(summary['currentFreshGo'],{'topPASS':3,'subPASS':6,'includesOriginalGraph':True});self.assertTrue(summary['historicalCronContractOnly']);self.assertEqual(summary['actualCronFixtureTopDispatched'],0)

if __name__=='__main__':unittest.main()
