"""One finite pure split-producer contract; no network, Go, ELF or cloud use."""
import copy
import os
import ast
import tempfile
import urllib.error
from unittest import mock
import importlib.util
import json
from pathlib import Path
import unittest

HERE=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('product20_split_contract',HERE/'run.py')
controller=importlib.util.module_from_spec(spec)
spec.loader.exec_module(controller)

class HTTPObservationContract(unittest.TestCase):
 def test_http_observation_accepts_only_bounded_original_request_fields(self):
  first={'operation':'config','outcome':'envelope','status':200,'appCode':500,'detailClass':'route-upstream-unavailable'}
  v={'httpExchanges':[first],'configProxyStatus':0,'private':'private-result'}
  observed=controller.project_http_observation(v)
  self.assertEqual(observed['exchanges'],[first])
  self.assertFalse(observed['rawBodiesOrHeadersExported'])
  self.assertNotIn('private-result',json.dumps(observed))
  self.assertIsNone(controller.project_http_observation({}))
  for change in ({'body':'private-body'}, {'operation':'login'}, {'status':True},
                 {'outcome':'transport'}, {'appCode':True}, {'detailClass':'private-detail'}):
   bad=dict(v,httpExchanges=[dict(first,**change)])
   with self.assertRaises(ValueError):controller.project_http_observation(bad)
  with self.assertRaises(ValueError):
   controller.project_http_observation(dict(v,configProxyStatus=200,httpExchanges=[]))

class SplitProducerContract(unittest.TestCase):
 def test_current5_expected_manifest_original6_route_and_locked_core_keep_exact_provenance(self):
  c=controller.historical_chart_control(json.loads((HERE/'inputs.json').read_bytes()))  # Original259 fixture, never the pending current source.
  c['executionBinding']=None;c['acceptedChartArtifact']=None  # Explicit original locked-policy fixture.
  self.assertIsNone(c['executionBinding']);self.assertIsNone(c['acceptedChartArtifact'])
  with self.assertRaises(ValueError):controller.require_bound(c)
  policy=controller.chart_assembly_policy(c)
  five=copy.deepcopy(policy['currentChart5']['expectedManifest']['body'])
  old=policy['historicalRouteFrom6']
  # Synthetic legacy manifest metadata exercises its unchanged schema branch;
  # no fixture object represents a received ELF/ZIP or real current core14.
  legacy_rows=[]
  for row in five['binaries']:
   item=copy.deepcopy(row);item.update(bytes=1,sha256='a'*64,sourceVector=old['provider']['sources']['chart'])
   legacy_rows.append(item)
  legacy_rows.append(copy.deepcopy(old['selectedRoute']))
  six={'binaries':legacy_rows,'sourceVectors':old['provider']['sources'],
       'locks':old['provider']['locks'],'head':old['artifact']['head'],
       'run':str(old['artifact']['run']),'attempt':str(old['artifact']['attempt']),
       'sourceHelperToolPostExact':True,'configsLogsOrSourceIncluded':False}
  selected=controller.assemble_chart_products(c,five,six)
  self.assertEqual([x['binary']['name'] for x in selected],list(controller.NAMES))
  self.assertEqual(sum(x['provider']=='currentChart5' for x in selected),5)
  self.assertEqual(selected[-1],{'provider':'historicalRouteFrom6','binary':old['selectedRoute']})
  self.assertTrue(all(x['binary']['sourceVector']==c['chartProvider']['sources']['chart'] for x in selected[:-1]))
  self.assertEqual(six['sourceVectors']['chart']['members'],258)
  self.assertEqual(five['source']['members'],259)
  self.assertEqual(policy['currentChart5']['expectedManifest']['evidence'],'expected-derived-not-observed')
  before=copy.deepcopy((five,six))
  controller.assemble_chart_products(c,five,six)
  self.assertEqual((five,six),before)

  bad_five=[]
  for key,value in [('kind','legacy-six'),('run',42283432687),('sourceToolHelperPostExact',False),
                    ('ReadyOrP20OrDeploymentAccepted',True),('source',six['sourceVectors']['chart'])]:
   bad=copy.deepcopy(five);bad[key]=value;bad_five.append(bad)
  bad=copy.deepcopy(five);bad['binaries'][0]['sha256']='0'*64;bad_five.append(bad)
  bad=copy.deepcopy(five);bad['binaries'][0]=bad['binaries'][1];bad_five.append(bad)
  bad=copy.deepcopy(five);bad['binaries'].pop();bad_five.append(bad)
  bad=copy.deepcopy(five);bad['locks']['go.sum']='0'*64;bad_five.append(bad)
  for bad in bad_five:
   with self.assertRaises(ValueError):controller.assemble_chart_products(c,bad,six)
  bad_six=[]
  bad=copy.deepcopy(six);bad['sourceVectors']['chart']=five['source'];bad_six.append(bad)
  bad=copy.deepcopy(six);bad['locks']['route']['go.sum']='0'*64;bad_six.append(bad)
  bad=copy.deepcopy(six);bad['binaries'][-1]['sha256']='0'*64;bad_six.append(bad)
  bad=copy.deepcopy(six);bad['binaries'][-1]['sourceVector']=five['source'];bad_six.append(bad)
  bad=copy.deepcopy(six);bad['binaries'][-1]=bad['binaries'][0];bad_six.append(bad)
  bad=copy.deepcopy(six);bad['configsLogsOrSourceIncluded']=True;bad_six.append(bad)
  for bad in bad_six:
   with self.assertRaises(ValueError):controller.assemble_chart_products(c,five,bad)
  with self.assertRaises(ValueError):
   controller.manifest_products(five,policy['currentChart5']['artifact'],tuple(controller.CHART5),512<<20,chart_sources=six['sourceVectors'])
  with self.assertRaises(ValueError):
   controller.manifest_products(five,old['artifact'],tuple(controller.CHART5),512<<20,chart5=policy['currentChart5'])
  with self.assertRaises(ValueError):
   controller.manifest_products(five,policy['currentChart5']['artifact'],tuple(controller.CHART5),512<<20,chart5=policy['currentChart5'],chart_sources=six['sourceVectors'])
  for change in ('hash','observed','route-alias'):
   bad=copy.deepcopy(c)
   if change=='hash':bad['chartAssembly']['currentChart5']['expectedManifest']['sha256']='0'*64
   elif change=='observed':bad['chartAssembly']['currentChart5']['expectedManifest']['evidence']='observed'
   else:bad['chartAssembly']['historicalRouteFrom6']['provider']['sources']['chart']=five['source']
   with self.assertRaises(ValueError):controller.chart_assembly_policy(bad)

  # Shape-only Root admission control, with a synthetic core tuple; the real
  # artifact() still independently requires successful original API producer.
  accepted=controller.chart_artifact_binding(c)
  enabled=copy.deepcopy(c);enabled['acceptedChartArtifact']=accepted
  core={'run':1,'head':'0'*40,'attempt':1,'artifactID':1,'artifactBytes':1,
        'artifactSHA256':'0'*64,'manifestSHA256':'0'*64}
  enabled['executionBinding']={'rootAccepted':True,'rootAcceptanceSHA256':'0'*64,
      'source':enabled['graphProvider']['source'],'mode':controller.ASSEMBLY_MODE,
      'core14':core,'currentChart5':accepted['currentChart5'],
      'historicalRouteFrom6':accepted['historicalRouteFrom6']}
  self.assertEqual(controller.require_bound(enabled),enabled['executionBinding'])
  for change in ('root','source','mode','provider','partial','bool-attempt','unaccepted'):
   bad=copy.deepcopy(enabled)
   if change=='root':bad['executionBinding']['rootAccepted']=False
   elif change=='source':bad['executionBinding']['source']={'members':1,'digest':'0'*64}
   elif change=='mode':bad['executionBinding']['mode']='old-chart6'
   elif change=='provider':bad['executionBinding']['currentChart5']=old['artifact']
   elif change=='partial':del bad['executionBinding']['historicalRouteFrom6']
   elif change=='bool-attempt':bad['executionBinding']['core14']['attempt']=True
   else:bad['acceptedChartArtifact']=None
   with self.assertRaises(ValueError):controller.require_bound(bad)

 def test_post_aux_failure_projection_keeps_real_source_http_context_and_original_primary(self):
  c=json.loads((HERE/'inputs.json').read_bytes());controls={p:controller.sha(controller.regular(controller.REPO/p)) for p in c['controllers']};stage='minio-artifact-receive'
  try:controller.require_bound(dict(c,executionBinding=None))
  except ValueError as error:original=error
  observed=controller.project_p20_failure(stage,original,c,controls);frames=[frame for row in observed['exceptions'] for frame in row['frames']];self.assertTrue(any(frame['path']=='ci/product20/run.py' and type(frame['line']) is int and frame['line']>0 for frame in frames));self.assertEqual(observed['exceptions'][0]['class'],'value-error');self.assertFalse(observed['runtimeReadyP20Accepted']);self.assertFalse(observed['uniqueCauseProven'])
  private='PRIVATE_PASSWORD https://private.invalid/token /private/config'
  try:
   try:raise urllib.error.HTTPError('https://private.invalid/'+private,403,private,{'Secret':private},None)
   except urllib.error.HTTPError:raise original from None
  except ValueError as wrapped:
   value=controller.project_p20_failure(stage,wrapped,c,controls);self.assertEqual([row['class'] for row in value['exceptions']],['value-error','http-error']);self.assertEqual(value['exceptions'][1]['httpStatus'],403);self.assertEqual(value['exceptions'][1]['httpStatusClass'],'forbidden');self.assertNotIn('PRIVATE_',json.dumps(value));self.assertNotIn('/private/',json.dumps(value));self.assertNotIn('private.invalid',json.dumps(value))
  class Unknown(Exception):
   def __str__(self):raise AssertionError('custom exception formatting forbidden')
   def __repr__(self):raise AssertionError('custom exception formatting forbidden')
   def __getattribute__(self,key):
    if key in ('args','__dict__','__traceback__','__cause__','__context__'):raise AssertionError('unknown exception attribute dispatch forbidden')
    return super().__getattribute__(key)
  unknown=Unknown(private);value=controller.project_p20_failure(stage,unknown,c,controls);self.assertEqual(value['exceptions'],[{'class':'other','frames':[]}]);self.assertNotIn('PRIVATE_',json.dumps(value))
  odd=urllib.error.HTTPError('https://private.invalid',987654321,private,{},None);value=controller.project_p20_failure(stage,odd,c,controls);self.assertIsNone(value['exceptions'][0]['httpStatus']);self.assertEqual(value['exceptions'][0]['httpStatusClass'],'other-http')
  with self.assertRaises(ValueError):controller.project_p20_failure('PRIVATE_STAGE',original,c,controls)
  with self.assertRaises(ValueError):controller.project_p20_failure(stage,original,c,dict(controls,**{'ci/product20/run.py':'0'*64}))
  chain=[ValueError(private) for _ in range(6)]
  for left,right in zip(chain,chain[1:]):BaseException.__dict__['__context__'].__set__(left,right)
  value=controller.project_p20_failure(stage,chain[0],c,controls);self.assertEqual(len(value['exceptions']),4);self.assertTrue(value['chainTruncated']);self.assertLess(len(json.dumps(value).encode()),16384)
  with tempfile.TemporaryDirectory() as folder,mock.patch.object(controller,'write',side_effect=RuntimeError(private)):
   self.assertFalse(controller.emit_p20_failure_observation(Path(folder),stage,original,c,controls))
  # Exercise the exact controller except handler in isolation: no run/API/business calls.
  tree=ast.parse((HERE/'run.py').read_bytes());run=next(n for n in tree.body if isinstance(n,ast.FunctionDef) and n.name=='run');outer=next(n for n in run.body if isinstance(n,ast.Try) and any(h.name=='e' for h in n.handlers));handler=copy.deepcopy(next(h for h in outer.handlers if h.name=='e'))
  function=ast.FunctionDef(name='actual_except_path',args=ast.arguments(posonlyargs=[],args=[],vararg=None,kwonlyargs=[],kw_defaults=[],kwarg=None,defaults=[]),body=[ast.Try(body=[ast.Raise(exc=ast.Name(id='primary_original',ctx=ast.Load()),cause=None)],handlers=[handler],orelse=[],finalbody=[])],decorator_list=[],returns=None,type_comment=None,type_params=[]);module=ast.fix_missing_locations(ast.Module(body=[function],type_ignores=[]))
  def failed_observation(*args):raise RuntimeError(private)
  namespace={'primary_original':original,'out':Path('/unused'),'failure_stage':stage,'c':c,'controls':controls,'emit_p20_failure_observation':failed_observation};exec(compile(module,'<actual-controller-except-only>','exec'),namespace)
  with self.assertRaises(ValueError) as caught:namespace['actual_except_path']()
  self.assertIs(caught.exception,original)

 def test_startup_boundary_exports_only_closed_primary_and_cleanup_observations(self):
  private='PRIVATE_PASSWORD https://private.invalid/secret /private/task/stdout.private'
  base={'version':'fresh-product20-http-ws-result-v1','accepted':False,'phase':'fresh-sql-and-six-real-services','failureSHA256':'a'*64,'coreSourceSHA256':'b'*64,'productProcesses':14,'realRegistrations':0,'realPasswordLogins':0,'ordinaryDispatches':0,'peerMessages':0,'historyMessages':0,'auxiliaryListenersJoined':True,'originalSixChildrenExited':False,'originalCoreCleanupComplete':True,'sixOriginalPIDs':[7001],'privateLog':private,'privateURL':private,'startupBoundary':{'version':'product20-startup-boundary-v1','entered':True,'substep':'product-wait-port','role':'admin-rpc','sixForked':1,'sixReady':0,'primaryCause':{'present':True,'classes':['other','exec-exit'],'truncated':False,'errno':0,'mysqlNumber':0,'execState':'exited','exitCode':2,'signal':0}}}
  with tempfile.TemporaryDirectory() as folder:
   root=Path(folder).resolve();sequence=0
   def project(value):
    nonlocal sequence
    sequence+=1;source=root/('input-'+str(sequence)+'.json');output=root/('public-'+str(sequence)+'.json');source.write_text(json.dumps(value));controller.export_terminal(source,output);return json.loads(output.read_text())
   value=project(base);observation=value['startupBoundary']
   self.assertEqual(observation['substep'],'product-wait-port');self.assertEqual(observation['role'],'admin-rpc');self.assertEqual(observation['sixForked'],1);self.assertEqual(observation['sixReady'],0);self.assertEqual(observation['primaryCause']['exitCode'],2);self.assertEqual(observation['cleanup'],{k:base[k] for k in ('auxiliaryListenersJoined','originalSixChildrenExited','originalCoreCleanupComplete')});self.assertTrue(observation['primaryCauseCapturedBeforeCleanup']);self.assertFalse(observation['runtimeReadyP20Accepted']);self.assertFalse(observation['physicalFirstFailureClaimed']);self.assertNotIn(private,json.dumps(value));self.assertNotIn('7001',json.dumps(value));self.assertNotIn('privateLog',json.dumps(value))
   # Every role is finite; migration and primary numeric facts stay distinct.
   for role in controller.NAMES:
    good=copy.deepcopy(base);good['startupBoundary']['role']=role;self.assertEqual(project(good)['startupBoundary']['role'],role)
   good=copy.deepcopy(base);good['startupBoundary'].update(substep='route-migrate-wait',role='route-service',sixForked=0);good['startupBoundary']['primaryCause'].update(execState='signaled',exitCode=-1,signal=15);self.assertEqual(project(good)['startupBoundary']['primaryCause']['signal'],15)
   good=copy.deepcopy(base);good['startupBoundary'].update(substep='aux-https-listen',role='aux',sixForked=0);good['startupBoundary']['primaryCause'].update(classes=['net-op','os-syscall','syscall-errno'],errno=13,execState='none',exitCode=-1);self.assertEqual(project(good)['startupBoundary']['primaryCause']['errno'],13)
   good=copy.deepcopy(base);good['startupBoundary'].update(substep='route-tenant-seed',role='route-service',sixForked=0);good['startupBoundary']['primaryCause'].update(classes=['mysql'],mysqlNumber=1045,execState='none',exitCode=-1);self.assertEqual(project(good)['startupBoundary']['primaryCause']['mysqlNumber'],1045)
   # Known false and not-entered are observations; missing is rejected.
   good=copy.deepcopy(base);good['startupBoundary'].update(entered=False,substep='not-entered',role='none',sixForked=0);good['startupBoundary']['primaryCause'].update(present=False,classes=[],execState='none',exitCode=-1);good['productProcesses']=0;self.assertFalse(project(good)['startupBoundary']['entered'])
   good=copy.deepcopy(base);good['startupBoundary'].update(substep='complete',role='none',sixForked=6,sixReady=6);good['startupBoundary']['primaryCause'].update(present=False,classes=[],execState='none',exitCode=-1);good['productProcesses']=20;self.assertEqual(project(good)['startupBoundary']['substep'],'complete')
   mutations=[]
   def bad_observation(key,value):
    bad=copy.deepcopy(base);bad['startupBoundary'][key]=value;mutations.append(bad)
   def bad_cause(key,value):
    bad=copy.deepcopy(base);bad['startupBoundary']['primaryCause'][key]=value;mutations.append(bad)
   for key,value in [('substep',private),('role',private),('role','aux'),('entered',1),('sixForked',True),('sixReady',False),('sixForked',7),('sixForked',-1),('sixReady',2),('secret',private)]:bad_observation(key,value)
   for key,value in [('classes',[private]),('classes',['other','other']),('classes',[]),('present',1),('truncated',0),('errno',True),('errno',-1),('errno',4096),('errno',13),('mysqlNumber',True),('mysqlNumber',-1),('mysqlNumber',65536),('mysqlNumber',1045),('execState',private),('exitCode',True),('exitCode',256),('exitCode',-1),('signal',True),('signal',1),('detail',private)]:bad_cause(key,value)
   bad=copy.deepcopy(base);del bad['startupBoundary'];mutations.append(bad)
   bad=copy.deepcopy(base);del bad['startupBoundary']['primaryCause'];mutations.append(bad)
   for key in ('auxiliaryListenersJoined','originalSixChildrenExited','originalCoreCleanupComplete'):
    bad=copy.deepcopy(base);del bad[key];mutations.append(bad)
    bad=copy.deepcopy(base);bad[key]=0;mutations.append(bad)
   bad=copy.deepcopy(base);bad['productProcesses']=True;mutations.append(bad)
   for bad in mutations:
    with self.assertRaises(ValueError) as caught:project(bad)
    self.assertNotIn(private,str(caught.exception))

 def test_original_exited_child_projection_binds_private_owner_product_and_closed_chart_frames(self):
  private='PRIVATE_PASSWORD https://private.invalid/token /private/task/secret-path'
  config=json.loads((HERE/'inputs.json').read_bytes());owner={'run':'1234','attempt':'1','head':'a'*40,'uid':os.getuid()}
  public='chart/cmd/rpc/admin-rpc/main.go';public_body=(controller.REPO/public).read_bytes();gomod=(controller.REPO/'chart/go.mod').read_bytes();chart_bodies={public:public_body,'chart/go.mod':gomod}
  log=('panic: '+private+'\n\tgithub.com/OpenIMSDK/chat/cmd/rpc/admin-rpc/main.go:59 +0x1\n\tgithub.com/OpenIMSDK/tools@v0.0.17/log/log.go:1 +0x1\n\tgithub.com/go-zookeeper/zk@v1.0.3/conn.go:1 +0x1\n\tgoogle.golang.org/grpc@v1.84.0/server.go:1 +0x1\n\t/private/task/github.com/OpenIMSDK/chat/cmd/rpc/admin-rpc/main.go:59 +0x1\n\tgithub.com/OpenIMSDK/chat/../cmd/rpc/admin-rpc/main.go:59 +0x1\n\tgithub.com/OpenIMSDK/chat/cmd/rpc/admin-rpc/main.go:999999 +0x1\n\tgithub.com/elsewhere/secret.go:1 +0x1\n').encode()
  with tempfile.TemporaryDirectory() as folder,mock.patch.object(controller,'allocation_owner',return_value=owner):
   parent=Path(folder).resolve();sequence=0
   def fixture():
    nonlocal sequence
    sequence+=1;root=parent/str(sequence);root.mkdir(mode=0o700)
    for name in ('private','chain','chain/admin-rpc','current-chart5','historical-six','out'):(root/name).mkdir(mode=0o700)
    products=[{'name':name,'path':str(root/('historical-six' if name=='route-service' else 'current-chart5')/name),'sha256':controller.sha(name.encode()),'bytes':123,'sourceSHA256':config['chartProvider']['sources']['route' if name=='route-service' else 'chart']['sha256']} for name in controller.NAMES]
    plan={'version':'fresh-product20-http-ws-v1','work':str(root/'chain'),'products':products,'sqlRootPassword':private};ps=controller.write(root/'private/product20-input.json',plan)
    controller.write(root/'allocation.json',owner);controller.write(root/'private/owner.json',owner);controller.write(root/'private/operator-intent.json',{'pidBindingAtOriginalStart':True,'inputSHA256':ps,'source':config['graphProvider']['source'],'harness':config['derivedHarnessSource']})
    result={'version':'fresh-product20-http-ws-result-v1','accepted':False,'phase':'fresh-sql-and-six-real-services','failureSHA256':'b'*64,'coreSourceSHA256':config['graphProvider']['source']['digest'],'productProcesses':14,'realRegistrations':0,'realPasswordLogins':0,'ordinaryDispatches':0,'peerMessages':0,'historyMessages':0,'auxiliaryListenersJoined':True,'originalSixChildrenExited':False,'originalCoreCleanupComplete':True,'sixOriginalPIDs':[7001],'secret':private,'startupBoundary':{'version':'product20-startup-boundary-v1','entered':True,'substep':'product-wait-port','role':'admin-rpc','sixForked':1,'sixReady':0,'primaryCause':{'present':True,'classes':['other','exec-exit'],'truncated':False,'errno':0,'mysqlNumber':0,'execState':'exited','exitCode':2,'signal':0}}}
    controller.write(root/'chain/product20-result.json',result)
    child={'uid':os.getuid(),'pid':7001,'startToken':'123456','path':products[0]['path'],'sha256':products[0]['sha256']};controller.write(root/'chain/admin-rpc/original-child.json',child)
    for name,raw in [('stderr',log),('stdout',('flag provided but not defined: '+private+'\n').encode())]:
     path=root/'chain/admin-rpc'/(name+'.private');path.write_bytes(raw);path.chmod(0o600)
    terminal=controller.export_terminal(root/'chain/product20-result.json',root/'out/actual-terminal.json');return root,plan,ps,products,terminal
   def project(state):
    root,plan,ps,products,terminal=state;return controller.project_p20_original_child_exit(root,terminal,plan,ps,products,chart_bodies,config)
   state=fixture();root,plan,ps,products,terminal=state;value=project(state)
   self.assertEqual(value['status'],'projected');self.assertEqual(value['role'],'admin-rpc');self.assertTrue(value['originalOwnerAndProductBinding']);self.assertEqual(value['sourceFrames'],[{'path':public,'line':59,'sourceSHA256':controller.sha(public_body)}]);self.assertEqual(value['headerClasses'],['flag-parser','go-panic']);self.assertEqual(value['fixedClasses'],['flag-unknown']);self.assertEqual(value['dependencyFamilies'],['grpc','tools-log','zk']);self.assertFalse(value['runtimeReadyP20Accepted']);self.assertFalse(value['prefixIsCauseProof']);self.assertFalse(value['physicalFirstFailureClaimed']);self.assertNotIn(private,json.dumps(value));self.assertNotIn(str(root),json.dumps(value));self.assertNotIn('7001',json.dumps(value));self.assertNotIn(controller.sha(log),json.dumps(value))
   self.assertTrue(controller.emit_p20_child_exit_observation(root,root/'out',terminal,plan,ps,products,chart_bodies,config));self.assertEqual(json.loads((root/'out/original-child-exit-observation.json').read_text()),value)
   # Unknown and externally sourced frames remain unknown; bounded scan never exports text.
   unknown=controller.project_p20_child_log_streams({'stdout':private.encode(),'stderr':b'\t/private/secret.go:1 +0x1\n'},chart_bodies);self.assertEqual(unknown['headerClasses'],['unknown']);self.assertEqual(unknown['dependencyFamilies'],['unknown']);self.assertEqual(unknown['sourceFrames'],[])
   frames=b''.join(('\tgithub.com/OpenIMSDK/chat/cmd/rpc/admin-rpc/main.go:'+str(i)+' +0x1\n').encode() for i in range(1,12));bounded=controller.project_p20_child_log_streams({'stdout':b'','stderr':frames},chart_bodies);self.assertEqual(len(bounded['sourceFrames']),8);self.assertTrue(bounded['scanTruncated'])
   with self.assertRaises(ValueError):controller.project_p20_child_log_streams({'stdout':b'','stderr':b'x'*(controller.P20_CHILD_EXIT_POLICY['perStreamBytes']+1)},chart_bodies)
   for mutation in ('owner','uid-bool','pid-slot','start-token','binary-path','binary-hash','plan-sha','plan-body','intent','source','symlink','hardlink','mode','directory-alias','fifo'):
    state=fixture();root,plan,ps,products,terminal=state;childpath=root/'chain/admin-rpc/original-child.json';child=json.loads(childpath.read_text());logpath=root/'chain/admin-rpc/stderr.private'
    if mutation=='owner':bad=dict(owner,head='c'*40);(root/'private/owner.json').unlink();controller.write(root/'private/owner.json',bad)
    elif mutation=='uid-bool':child['uid']=True
    elif mutation=='pid-slot':child['pid']=7002
    elif mutation=='start-token':child['startToken']=private
    elif mutation=='binary-path':child['path']=private
    elif mutation=='binary-hash':child['sha256']='0'*64
    elif mutation=='plan-sha':state=(root,plan,'0'*64,products,terminal)
    elif mutation=='plan-body':(root/'private/product20-input.json').write_text(json.dumps(dict(plan,sqlRootPassword='changed')))
    elif mutation=='intent':path=root/'private/operator-intent.json';v=json.loads(path.read_text());path.unlink();controller.write(path,dict(v,inputSHA256='0'*64))
    elif mutation=='source':
     products=[dict(x,sourceSHA256='0'*64) for x in products];plan=dict(plan,products=products);(root/'private/product20-input.json').unlink();ps=controller.write(root/'private/product20-input.json',plan);(root/'private/operator-intent.json').unlink();controller.write(root/'private/operator-intent.json',{'pidBindingAtOriginalStart':True,'inputSHA256':ps,'source':config['graphProvider']['source'],'harness':config['derivedHarnessSource']});state=(root,plan,ps,products,terminal)
    elif mutation=='symlink':logpath.unlink();logpath.symlink_to(root/'private/owner.json')
    elif mutation=='hardlink':(root/'leak').hardlink_to(logpath)
    elif mutation=='mode':logpath.chmod(0o644)
    elif mutation=='directory-alias':directory=root/'chain/admin-rpc';directory.rename(root/'alias');directory.symlink_to(root/'alias',target_is_directory=True)
    elif mutation=='fifo':logpath.unlink();os.mkfifo(logpath,0o600)
    if mutation in ('uid-bool','pid-slot','start-token','binary-path','binary-hash'):childpath.unlink();controller.write(childpath,child)
    with self.assertRaises((ValueError,OSError)):project(state)
   for scope,key in (('boundary','sixForked'),('boundary','sixReady'),('cause','exitCode'),('cause','signal')):
    state=fixture();root,plan,ps,products,terminal=state;terminal=copy.deepcopy(terminal);target=terminal['startupBoundary'] if scope=='boundary' else terminal['startupBoundary']['primaryCause'];target[key]=True
    with self.assertRaises(ValueError):controller.project_p20_original_child_exit(root,terminal,plan,ps,products,chart_bodies,config)
   state=fixture();root,plan,ps,products,terminal=state;terminal=copy.deepcopy(terminal);terminal['startupBoundary']['primaryCause']['execState']='none';self.assertEqual(controller.project_p20_original_child_exit(root,terminal,plan,ps,products,chart_bodies,config)['status'],'not-eligible')
   # Exact actual command failure tail: observation exceptions cannot replace phase failure.
   tree=ast.parse((HERE/'run.py').read_text());run_node=next(n for n in tree.body if isinstance(n,ast.FunctionDef) and n.name=='run');command=next(n for n in run_node.body if isinstance(n,ast.FunctionDef) and n.name=='command');tail=[n for n in command.body if isinstance(n,ast.If) and (any(isinstance(x,ast.Compare) and isinstance(x.left,ast.Name) and x.left.id=='label' and any(isinstance(y,ast.Constant) and y.value=='actual-fresh-product20-http-ws-once' for y in x.comparators) for x in ast.walk(n.test)) or isinstance(n.test,ast.Name) and n.test.id=='failed' and any(isinstance(x,ast.Raise) for x in n.body))];self.assertEqual(len(tail),2)
   function=ast.FunctionDef(name='actual_failed_tail',args=ast.arguments(posonlyargs=[],args=[],kwonlyargs=[],kw_defaults=[],defaults=[]),body=copy.deepcopy(tail),decorator_list=[]);module=ast.fix_missing_locations(ast.Module(body=[function],type_ignores=[]))
   def broken_observer(*args):raise RuntimeError(private)
   state=fixture();root,plan,ps,products,terminal=state;namespace={'root':root,'out':root/'out','p':plan,'ps':ps,'products':products,'chart_bodies':chart_bodies,'c':config,'label':'actual-fresh-product20-http-ws-once','failed':True,'export_terminal':lambda *a:terminal,'emit_p20_child_exit_observation':broken_observer};exec(compile(module,'<actual-command-tail-only>','exec'),namespace)
   with self.assertRaisesRegex(ValueError,'^phase failed; private raw retained$'):namespace['actual_failed_tail']()
   with mock.patch.object(controller,'write',side_effect=RuntimeError(private)):self.assertFalse(controller.emit_p20_child_exit_observation(root,root/'out',terminal,plan,ps,products,chart_bodies,config))




class StartupFiveScopeContract(unittest.TestCase):
 def test_startup_five_reuse_requires_exact_source_race_closure_and_pending_typed_contract(self):
  import sys
  admin_path=controller.REPO/'ci/admin-session-targeted/run.py'
  spec=importlib.util.spec_from_file_location('startup_five_scope_admin',admin_path);admin=importlib.util.module_from_spec(spec)
  before=list(sys.path);sys.path.insert(0,str(admin_path.parent))
  try:spec.loader.exec_module(admin)
  finally:sys.path[:]=before
  inputs=json.loads((admin_path.parent/'inputs.json').read_bytes())
  class SecretFailure(Exception):
   def __str__(self):raise AssertionError('exception formatting must never run')
  caught=None
  with mock.patch.object(admin,'stable',side_effect=SecretFailure('PRIVATE_SECRET_ARG')):
   try:admin.verify(admin.sha((admin_path.parent/'inputs.json').read_bytes()),source_required=False)
   except SecretFailure as error:caught=error
  self.assertIsNotNone(caught)
  projected=admin.public_controller_failure_frames(caught,inputs)
  self.assertTrue(projected['frames']);self.assertLessEqual(len(projected['frames']),8)
  expected={r['path']:r for r in inputs['controllerInputs']+inputs['borrowedInputs']}
  for row in projected['frames']:
   self.assertEqual(set(row),{'path','line','sourceSHA256'});self.assertEqual(row['path'],'ci/admin-session-targeted/run.py')
   self.assertEqual(row['sourceSHA256'],expected[row['path']]['sha256'])
   self.assertTrue(1<=row['line']<=len((controller.REPO/row['path']).read_bytes().splitlines()))
  self.assertNotIn('PRIVATE_SECRET_ARG',json.dumps(projected));self.assertNotIn('SecretFailure',json.dumps(projected))
  changed=copy.deepcopy(inputs)
  next(r for r in changed['controllerInputs'] if r['path']=='ci/admin-session-targeted/run.py')['sha256']='0'*64
  self.assertEqual(admin.public_controller_failure_frames(caught,changed)['frames'],[])
  try:raise SecretFailure('PRIVATE_SECRET_ARG')
  except SecretFailure as unknown:self.assertEqual(admin.public_controller_failure_frames(unknown,inputs)['frames'],[])
  self.assertTrue(admin.prior_admin_source_bridge(inputs)['sourceInverseExact'])
  changed=copy.deepcopy(inputs);changed['source'][0]['sha256']='0'*64
  with self.assertRaises(ValueError):admin.prior_admin_source_bridge(changed)
  tools={n:dict(r,path='/not-exported') for n,r in inputs['priorAdmin4']['toolsBodyMode'].items()}
  sums=(controller.REPO/'product/chart/go.sum').read_bytes();pairs={tuple(line.split()[:2]):line.split()[2] for line in sums.decode().splitlines()}
  module,version=next((m,v) for m,v in pairs if not v.endswith('/go.mod') and (m,v+'/go.mod') in pairs)
  local={'Path':'github.com/OpenIMSDK/chat','Main':True}
  packages=[{'ImportPath':admin.PACKAGE,'Name':'admindelegation','Module':local},
    {'ImportPath':admin.PACKAGE+' ['+admin.PACKAGE+'.test]','Name':'admindelegation','ForTest':admin.PACKAGE,'Module':local},
    {'ImportPath':admin.PACKAGE+'.test','Name':'main'},
    {'ImportPath':'testing','Name':'testing','Standard':True},
    {'ImportPath':module+'/metadata_fixture','Name':'fixture','Module':{'Path':module,'Version':version,'Sum':pairs[(module,version)],'GoModSum':pairs[(module,version+'/go.mod')]}}]
  encode=lambda rows:(''.join(json.dumps(v)+'\n' for v in rows)).encode()
  closure=admin.admin_test_closure(encode(packages),0,sums,tools,inputs)
  self.assertTrue(closure['accepted']);self.assertEqual(closure['currentAdmin4TestCommands'],0)
  self.assertEqual(admin.admin_closure_plan('/fixed/go'),['/fixed/go','list','-race','-p=1','-deps','-test','-mod=readonly','-json=ImportPath,Name,ForTest,Standard,Module,Error,DepsErrors,Incomplete','./pkg/common/admindelegation'])
  bad_sets=[packages[:-1]+[dict(packages[-1],Error={})],packages[:-2],packages+[packages[0]],packages[:-1]+[dict(packages[-1],Module={'Path':module,'Version':'v0.0.0-unknown'})]]
  for blocked in ('github.com/OpenIMSDK/chat/tools/component',admin.STARTUP_PACKAGE):
   bad_sets.append(packages+[{'ImportPath':blocked+' ['+admin.PACKAGE+'.test]','ForTest':admin.PACKAGE,'Module':local}])
  for rows in bad_sets:
   with self.assertRaises(ValueError):admin.admin_test_closure(encode(rows),0,sums,tools,inputs)
  altered=copy.deepcopy(tools);altered['go']['sha256']='0'*64
  with self.assertRaises(ValueError):admin.admin_test_closure(encode(packages),0,sums,altered,inputs)
  for raw,code in [(encode(packages)[:-2],0),(encode(packages),1),(encode(packages)+b'{}trailing\n',0)]:
   with self.assertRaises(ValueError):admin.admin_test_closure(raw,code,sums,tools,inputs)
  c=json.loads((HERE/'inputs.json').read_bytes());policy=controller.chart_assembly_policy(c)
  self.assertEqual(policy['pendingCurrent5']['source'],c['chartProvider']['sources']['chart'])
  self.assertIsNone(c['executionBinding']);self.assertIsNone(c['acceptedChartArtifact'])
  with self.assertRaises(ValueError):controller.require_bound(c)
  bad=copy.deepcopy(c);bad['chartAssembly']['currentChart5']=bad['historicalPreviousChart5']['currentChart5']
  with self.assertRaises(ValueError):controller.chart_assembly_policy(bad)
  diagnostic={'version':'chart-startup-error-v1','stage':'rpc-init','causes':[{'kind':'sdk','mysqlNumber':0,'errno':0,'grpcCode':-1,'sdkCode':1001,'redisPrefix':'none'}],'truncated':False}
  emit=lambda d:{'stdout':b'','stderr':('panic: LUOBO_CHART_STARTUP_V1 '+json.dumps(d)+'\n').encode()}
  source=c['typedStartupDiagnostic'];bodies={source['path']:controller.regular(controller.REPO/'product'/source['path'])}
  typed=controller.project_chart_startup_lines(emit(diagnostic),bodies,source)
  self.assertEqual(typed['diagnostic'],diagnostic);self.assertTrue(typed['diagnosticOnlyNotStageSuccess'])
  flooded={'stdout':b'\n'*controller.P20_CHILD_EXIT_POLICY['lineLimit'],'stderr':emit(diagnostic)['stderr']}
  self.assertEqual(controller.project_chart_startup_lines(flooded,bodies,source)['diagnostic'],diagnostic)
  for key,value in [('sdkCode',1234567),('redisPrefix','PRIVATE'),('errno',True)]:
   bad=copy.deepcopy(diagnostic);bad['causes'][0][key]=value
   with self.assertRaises(ValueError):controller.project_chart_startup_lines(emit(bad),bodies,source)
  bad=copy.deepcopy(diagnostic);bad['secret']='PRIVATE'
  with self.assertRaises(ValueError):controller.project_chart_startup_lines(emit(bad),bodies,source)
  with self.assertRaises(ValueError):controller.project_chart_startup_lines(emit(diagnostic),{},source)
  events=[{'Action':'start','Package':admin.STARTUP_PACKAGE},{'Action':'run','Package':admin.STARTUP_PACKAGE,'Test':admin.STARTUP_NAME},{'Action':'pass','Package':admin.STARTUP_PACKAGE,'Test':admin.STARTUP_NAME},{'Action':'output','Package':admin.STARTUP_PACKAGE,'Output':'ok\t'+admin.STARTUP_PACKAGE+'\t0.001s\n'},{'Action':'pass','Package':admin.STARTUP_PACKAGE}]
  event=admin.startup_go_events(encode(events),0);event.update(actualResourceGuardPassed=True,resourceSHA256='c'*64)
  skipped=copy.deepcopy(events);skipped[2]['Action']='skip'
  with self.assertRaises(ValueError):admin.startup_go_events(encode(skipped),0)
  old=controller.historical_chart_control(c);five=copy.deepcopy(old['chartAssembly']['currentChart5']['expectedManifest']['body'])
  five.pop('selectedLive4Accepted');five.pop('SQLRedisOriginalOwnerCleanupAccepted')
  prior={'kind':'historical-selective4-source-compatible-not-fresh','originalHead':inputs['priorAdmin4']['head'],'originalRun':inputs['priorAdmin4']['run'],'originalJob':inputs['priorAdmin4']['job'],'originalChartSource':admin.PRIOR_CHART,'originalSourceDigest':admin.PRIOR_SOURCE,'originalRootReviewSHA256':admin.PRIOR_REVIEW,'originalReceiptPins':admin.PRIOR_RECEIPTS,'sourceBridgeSHA256':'a'*64,'closureReceiptSHA256':'b'*64,'compatibilityAccepted':True,'currentAdmin4TestCommands':0,'currentFreshAdmin4PASS':False}
  five.update(schema=2,kind=controller.STARTUP_FIVE_KIND,source=c['chartProvider']['sources']['chart'],startupTest=event,priorAdmin4=prior,currentAdmin4TestCommands=0,SQLRedisFixturesStarted=0)
  for r in five['binaries']:r['sourceVector']=c['chartProvider']['sources']['chart']
  # Synthetic metadata only: no ZIP, ELF, producer success or Root acceptance.
  new=copy.deepcopy(c);new['chartAssembly'].pop('pendingCurrent5');slot=new['chartAssembly']['currentChart5']
  slot['artifact']=copy.deepcopy(old['chartAssembly']['currentChart5']['artifact']);raw=(json.dumps(five,sort_keys=True,indent=2)+'\n').encode();slot['artifact']['manifestSHA256']=controller.sha(raw)
  slot['expectedManifest']={'evidence':'expected-derived-not-observed','bytes':len(raw),'sha256':controller.sha(raw),'body':five};slot['derivation']=copy.deepcopy(old['chartAssembly']['currentChart5']['derivation'])
  self.assertEqual(controller.chart_assembly_policy(new)['currentChart5']['expectedManifest']['body']['currentAdmin4TestCommands'],0)
  bad=copy.deepcopy(new);bad['chartAssembly']['currentChart5']['expectedManifest']['body']['priorAdmin4']['currentFreshAdmin4PASS']=True
  body=bad['chartAssembly']['currentChart5']['expectedManifest']['body'];raw=(json.dumps(body,sort_keys=True,indent=2)+'\n').encode();bad['chartAssembly']['currentChart5']['expectedManifest'].update(bytes=len(raw),sha256=controller.sha(raw));bad['chartAssembly']['currentChart5']['artifact']['manifestSHA256']=controller.sha(raw)
  with self.assertRaises(ValueError):controller.chart_assembly_policy(bad)

if __name__=='__main__':unittest.main()
