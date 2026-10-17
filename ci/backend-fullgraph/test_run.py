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
  source={'members':1,'digest':'1'*64};c={'state':'FROZEN_FINAL_SOURCE','source':source,'sourceReviewAccepted':True,'scenarios':list(run.SCENARIOS),'roles':list(run.ROLES),'existingGreenTestsToRerun':[],'reuseOldBinary':False,'resource':run.RESOURCE,'cleanupScopes':copy.deepcopy(run.CLEANUP_SCOPES),'cleanupReuse':copy.deepcopy(run.CLEANUP_REUSE),'cleanupCompleteScopes':copy.deepcopy(run.CLEANUP_COMPLETE_SCOPES),'cleanupRecentReuse':copy.deepcopy(run.CLEANUP_RECENT_REUSE),'cleanupFixtureRepair':copy.deepcopy(run.CLEANUP_FIXTURE_REPAIR),'originalTargets':copy.deepcopy(run.GRAPH_TARGETS),'requiredOriginalSubtests':copy.deepcopy(run.GRAPH_CHILDREN),'cleanupPassedReuse':copy.deepcopy(run.CLEANUP_PASSED_REUSE),'cleanupTestsToRun':[]}
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
  for phase in ['pending-two-original-oracles','launcher-ownedcontrol']+[scope['label'] for scope in run.CLEANUP_SCOPES]:
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

if __name__=='__main__':unittest.main()
