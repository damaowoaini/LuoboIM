import copy
import json
from pathlib import Path
import unittest
import kgp_owner_model as m

class ActualKGPOwner(unittest.TestCase):
 def test_publishing_owner_preserves_original_BC4_without_promoting_it_to_App(self):
  catalog=json.loads((Path(__file__).parent/'rich-catalog.json').read_bytes())
  spec=next(c for c in catalog['configurations'] if c['name']==m.BC)
  report={'catalog_sha256':'00d559f734c28560b9f18a27ef2fedfd5488c7eb9fa0a6d8d18930d0927d2cc7',
   'project':':','configuration':m.BC,'scope_sources':{k:v['sha256'] for k,v in m.SOURCES.items()},
   'no_force_or_substitution':True,'edges':copy.deepcopy(spec['edges']),'artifacts':copy.deepcopy(spec['artifacts'])}
  origin={'coordinate':'org.jetbrains.kotlin:kotlin-gradle-plugin:2.2.21',
   'filename':'kotlin-gradle-plugin-2.2.21-gradle813.jar','bytes':23843763,
   'sha256':'b7a52865c6a5e160d2cf61e4d423ce44a6a20de0a6b23d03404b6d01a2811737',
   'observed_sources':['https://plugins.gradle.org/m2/org/jetbrains/kotlin/kotlin-gradle-plugin/2.2.21/kotlin-gradle-plugin-2.2.21-gradle813.jar']}
  origins={'all_actual_artifact_sources_accounted':True,'artifacts':[origin]}
  value={'schema':1,'owner':copy.deepcopy(m.OWNER),'configuration':m.BC,'registered_by_identity':True,
   'source_files':copy.deepcopy(m.SOURCES),'implementation':{'class_name':'org.jetbrains.kotlin.gradle.plugin.KotlinBasePluginWrapper',
   'classloader':'org.gradle.internal.classloader.VisitableURLClassLoader$InstrumentingVisitableURLClassLoader',
   'code_source':{'path':'gradle-cache/arbitrary-opaque-cache-key/original.jar','bytes':origin['bytes'],'sha256':origin['sha256']}},
   'consumers':[{'task':k,'implementation_class':v,'same_code_source':True,'configuration_bound_by_identity':True} for k,v in m.TASK_CLASSES.items()],
   'report':report}
  with self.subTest('actual source owner and strong original body'):
   got=m.validate_owner_witness(value,catalog,origins);self.assertTrue(got['original_BC4_template_verified']);self.assertFalse(got['compilation_consumed']);self.assertFalse(got['publishing_tasks_executed'])
  def rejected(label,change):
   with self.subTest(label):
    v=copy.deepcopy(value);o=copy.deepcopy(origins);change(v,o)
    with self.assertRaises(ValueError):m.validate_owner_witness(v,catalog,o)
  rejected('App cannot borrow publishing owner',lambda v,o:v['owner'].update(project=':app'))
  rejected('source drift',lambda v,o:v['source_files'].clear())
  rejected('foreign implementation body',lambda v,o:v['implementation']['code_source'].update(sha256='a'*64))
  rejected('unreviewed transformed loader',lambda v,o:v['implementation'].update(classloader='foreign.Loader'))
  rejected('arbitrary JAR cannot adopt KGP coordinate',lambda v,o:o['artifacts'][0].update(coordinate='foreign:plugin:1'))
  rejected('missing actual acquisition',lambda v,o:o.update(all_actual_artifact_sources_accounted=False))
  rejected('missing real consumer',lambda v,o:v['consumers'].pop())
  rejected('FileCollection not original configuration',lambda v,o:v['consumers'][0].update(configuration_bound_by_identity=False))
  rejected('original BC4 hash drift',lambda v,o:v['report']['artifacts'][0].update(sha256='f'*64))
  rejected('original graph missing',lambda v,o:v['report']['edges'].clear())

if __name__=='__main__':unittest.main()
