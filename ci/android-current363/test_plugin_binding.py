"""Exercise actual JSON bind/require I/O without Pub/native/cache sources."""
import copy
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

HERE=Path(__file__).resolve().parent
sys.path.insert(0,str(HERE.parent/'android-native'))
sys.path.append(str(HERE.parent/'android-current359'))
import plugin_scope as scope


class PluginBindingJSONTests(unittest.TestCase):
    def test_binding_roundtrip_preserves_pairs_and_rejects_changed_fields(self):
        seen=[(':synthetic_one',scope.rich.kgp_owner_model.ABI),
              (':synthetic_two',scope.rich.kgp_owner_model.ABI)]
        pins=[{'path':'evidence/plugin-rich-consumed/metadata/one/report.json','sha256':'1'*64},
              {'path':'evidence/plugin-rich-consumed/metadata/two/report.json','sha256':'2'*64}]
        proof=scope.summarize_reports('a'*64,pins,seen)
        self.assertEqual(proof,json.loads(json.dumps(proof)))
        self.assertEqual(seen,[(':synthetic_one',scope.rich.kgp_owner_model.ABI),
                               (':synthetic_two',scope.rich.kgp_owner_model.ABI)])
        self.assertIs(proof['reports'],pins)
        with self.assertRaises(ValueError):
            scope.summarize_reports('a'*64,pins,[[':synthetic_one',scope.rich.kgp_owner_model.ABI]])
        with tempfile.TemporaryDirectory() as temporary:
            work=Path(temporary).resolve()
            (work/'evidence').mkdir()
            (work/'evidence/rich-origin-binding.json').write_bytes(b'{"synthetic_origin_binding":true}\n')
            # Only independently validated source acquisition/report discovery is
            # stubbed. Production bind writer, SHA, JSON reader and comparator run.
            with patch.object(scope.origins,'require_origin_binding') as origin_check, patch.object(
                    scope,'reports',side_effect=lambda *_:copy.deepcopy(proof)):
                scope.bind_origins(work,HERE,{})
                file=work/'evidence/plugin-rich-origin-binding.json'
                original=json.loads(file.read_bytes())
                self.assertEqual(original['actual_project_configurations'],[list(pair) for pair in seen])
                scope.require_origin_binding(work,HERE,{})
                for case in ('seal','report-sha','report-missing','pair','pair-type','count','runtime','numeric-false','App-origin','template','extra'):
                    value=copy.deepcopy(original)
                    if case=='seal':value['seal_sha256']='b'*64
                    elif case=='report-sha':value['reports'][0]['sha256']='c'*64
                    elif case=='report-missing':value['reports'].pop()
                    elif case=='pair':value['actual_project_configurations'][0][0]=':changed'
                    elif case=='pair-type':value['actual_project_configurations'][0][1]=0
                    elif case=='count':value['actual_registered_configurations']+=1
                    elif case=='runtime':value['runtime_or_APK_accepted']=True
                    elif case=='numeric-false':value['runtime_or_APK_accepted']=0
                    elif case=='App-origin':value['App_origin_binding_sha256']='d'*64
                    elif case=='template':value['template_catalog_sha256']='e'*64
                    else:value['unexpected']='field'
                    file.write_text(json.dumps(value)+'\n')
                    with self.subTest(case=case),self.assertRaises(ValueError):
                        scope.require_origin_binding(work,HERE,{})
                self.assertEqual(origin_check.call_count,13)


if __name__=='__main__':
    unittest.main()
