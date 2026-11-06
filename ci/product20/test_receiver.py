"""One finite pure split-producer contract; no network, Go, ELF or cloud use."""
import copy
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

if __name__=='__main__':unittest.main()
