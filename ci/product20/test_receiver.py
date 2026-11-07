"""One finite pure split-producer contract; no network, Go, ELF or cloud use."""
import copy
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

class SplitProducerContract(unittest.TestCase):
 def test_current5_expected_manifest_original6_route_and_locked_core_keep_exact_provenance(self):
  c=json.loads((HERE/'inputs.json').read_bytes())
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


if __name__=='__main__':unittest.main()
