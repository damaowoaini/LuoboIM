#!/usr/bin/env python3
"""Controller-only controls. No Go, container, DB, service or graph is executed."""
import ast,copy,hashlib,importlib.util,json,os,struct,tempfile,unittest
from pathlib import Path
from unittest import mock
HERE=Path(__file__).resolve().parent

def load(name):
 spec=importlib.util.spec_from_file_location(name,HERE/(name+'.py'));value=importlib.util.module_from_spec(spec);spec.loader.exec_module(value);return value
run=load('run');builder=load('minio_build');resources=load('resources')

class FullGraphControls(unittest.TestCase):
 def test_prepare_lock_fresh_scope_and_unknown_cannot_alias_old_pass_or_canonical(self):
  source={'members':1,'digest':'1'*64};c={'state':'FROZEN_FINAL_SOURCE','source':source,'sourceReviewAccepted':True,'scenarios':list(run.SCENARIOS),'roles':list(run.ROLES),'existingGreenTestsToRerun':[],'reuseOldBinary':False,'resource':run.RESOURCE,'cleanupScopes':copy.deepcopy(run.CLEANUP_SCOPES),'cleanupReuse':copy.deepcopy(run.CLEANUP_REUSE),'ownedcontrolReuse':copy.deepcopy(run.OWNEDCONTROL_REUSE),'originalTargets':copy.deepcopy(run.GRAPH_TARGETS),'requiredOriginalSubtests':copy.deepcopy(run.GRAPH_CHILDREN)}
  with mock.patch.object(run,'FINAL_SOURCE',None),self.assertRaises(ValueError):run.check_locked(c)
  with mock.patch.object(run,'FINAL_SOURCE',source):
   run.check_locked(c)
   for key,value in [('reuseOldBinary',True),('existingGreenTestsToRerun',['old67']),('scenarios',['ack','redis-unknown']),('source',{'members':1,'digest':'2'*64}),('cleanupScopes',[]),('cleanupReuse',{}),('cleanupReuse',dict(run.CLEANUP_REUSE,source=source)),('cleanupReuse',dict(run.CLEANUP_REUSE,head='f'*40)),('cleanupReuse',dict(run.CLEANUP_REUSE,reviewSHA256='f'*64)),('ownedcontrolReuse',{}),('ownedcontrolReuse',dict(run.OWNEDCONTROL_REUSE,source=source)),('ownedcontrolReuse',dict(run.OWNEDCONTROL_REUSE,reviewSHA256='f'*64)),('originalTargets',{}),('requiredOriginalSubtests',{})]:
    bad=copy.deepcopy(c);bad[key]=value
    with self.subTest(key=key),self.assertRaises(ValueError):run.check_locked(bad)
  cleanup={'version':'owned-fullgraph-resource-cleanup-v1','nonce':'a'*32,'exactContainerIDs':['b'*64],'unconfirmedIDs':[],'accepted':True}
  finite=run.public_cleanup(cleanup)
  self.assertNotIn(cleanup['nonce'],json.dumps(finite));self.assertIn('ownerNonceSHA256',finite)
  self.assertNotIn('runtimeDrain',finite)
  cleanup['exactContainerIDs']=['foreign/name']
  with self.assertRaises(ValueError):run.public_cleanup(cleanup)
  with self.assertRaises(ValueError):run.check_result({'version':'owned-fullgraph-token-v2-result-v1','scenario':'ack','accepted':False},'ack','1'*64,{},'2'*64)

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
  for phase in ['pending-two-original-oracles']+[scope['label'] for scope in run.CLEANUP_SCOPES]:
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
  cleanup_line=next(node.lineno for node in calls if isinstance(node.func,ast.Name) and node.func.id=='run_cleanup_controls')
  compile_line=next(node.lineno for node in calls if isinstance(node.func,ast.Name) and node.func.id=='command' and node.args and isinstance(node.args[0],ast.BinOp) and isinstance(node.args[0].left,ast.Constant) and node.args[0].left.value=='compile-')
  minio_line=next(node.lineno for node in calls if isinstance(node.func,ast.Attribute) and isinstance(node.func.value,ast.Name) and node.func.value.id=='builder' and node.func.attr=='build')
  self.assertLess(cleanup_line,compile_line);self.assertLess(cleanup_line,minio_line)
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
     self.assertTrue(all(top_pattern.fullmatch(top) is None for tops in run.GRAPH_TARGETS.values() for top in tops));self.assertIsNone(top_pattern.fullmatch(run.CLEANUP_REUSE['top']))
     self.assertNotEqual(label,'cleanup-ownedcontrol');self.assertIsNone(top_pattern.fullmatch(run.OWNEDCONTROL_REUSE['top']))
     rows=events(scope['targets'],scope['requiredSubtests'])
     if mode=='missing-child' and len(calls)==1:
      removed=next(row['Test'] for row in rows if row.get('Test') and '/' in row['Test']);rows=[row for row in rows if row.get('Test')!=removed]
     if mode=='foreign-top':rows.insert(1,{'Action':'run','Package':parent.PREFIX+'internal/namespaceimport/ownedcontrol','Test':'TestForeign'})
     return ('\n'.join(json.dumps(row) for row in rows)+'\n').encode()
    if mode!='complete':
     with self.assertRaises(ValueError):run.run_cleanup_controls('/owned/go',{'GOPROXY':'off'},command,parent,out)
     self.assertFalse((out/'cleanup-names.json').exists());continue
    result=run.run_cleanup_controls('/owned/go',{'GOPROXY':'off'},command,parent,out)
    self.assertEqual(calls,[scope['label'] for scope in run.CLEANUP_SCOPES]);self.assertEqual((result['topPASS'],result['subPASS']),(1,2))
    original_required={pkg:{top:run.GRAPH_CHILDREN[top] for top in tops} for pkg,tops in run.GRAPH_TARGETS.items()}
    original=parent.audit(events(run.GRAPH_TARGETS,original_required),0,run.GRAPH_TARGETS)
    union=run.audit_target_union(original,result);self.assertEqual((union['topPASS'],union['subPASS']),(3,5))
    self.assertEqual(union['cleanupReuse']['source'],run.CLEANUP_REUSE['source']);self.assertEqual((union['cleanupReuse']['topPASS'],union['cleanupReuse']['subPASS']),(1,4));self.assertEqual(union['originalObligation'],{'topCount':5,'subCount':12,'crossSource':True});self.assertEqual((union['ownedcontrolReuse']['topPASS'],union['ownedcontrolReuse']['subPASS']),(1,3));self.assertEqual(union['ownedcontrolReuse']['source'],run.OWNEDCONTROL_REUSE['source'])
    bad=copy.deepcopy(result);bad['namedCompletions'].pop()
    with self.assertRaises(ValueError):run.audit_target_union(original,bad)
    bad=copy.deepcopy(result);bad['namedCompletions'].append(bad['namedCompletions'][0])
    with self.assertRaises(ValueError):run.audit_target_union(original,bad)
  with tempfile.TemporaryDirectory() as folder:
   command=mock.Mock(side_effect=ValueError('actual phase failed; private raw retained'))
   with self.assertRaises(ValueError):run.run_cleanup_controls('/owned/go',{},command,parent,Path(folder).resolve())
   self.assertEqual(command.call_count,1)

if __name__=='__main__':unittest.main()
