#!/usr/bin/env python3
import copy,importlib.util,json,unittest
from pathlib import Path
s=importlib.util.spec_from_file_location('failed_only_r7',Path(__file__).with_name('run.py'));m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
class FailedOnlyControls(unittest.TestCase):
 def test_original_failed_and_new_children_cannot_be_replaced_by_replayed_passes(self):
  c=json.loads(m.INPUTS.read_bytes());m.check_selection(c)
  for label in ['omit-failed','omit-new','replay-sibling','invent-pass','drop-pass','duplicate-selected']:
   bad=copy.deepcopy(c)
   if label=='omit-failed':bad['targets']['internal/rpc/third'].remove(bad['failedTargets']['internal/rpc/third'][0])
   elif label=='omit-new':bad['targets']['pkg/common/db/cache'].remove(bad['newTargets']['pkg/common/db/cache'][0])
   elif label=='replay-sibling':bad['requiredSubtests']['internal/rpc/third'][bad['failedTargets']['internal/rpc/third'][0]].append('minio')
   elif label=='invent-pass':bad['reusedTargets']['internal/rpc/third']=['TestInventedPriorPass']
   elif label=='drop-pass':bad['reusedTargets'].pop(next(iter(bad['reusedTargets'])))
   else:bad['targets']['pkg/common/db/cache'].append(bad['targets']['pkg/common/db/cache'][0])
   with self.subTest(label=label),self.assertRaises(ValueError):m.check_selection(bad)
  events=[{'package':p,'name':n+'/'+s} for p,ns in c['requiredSubtests'].items() for n,subs in ns.items() for s in subs]
  m.require_children({'namedCompletions':events},c)
  for altered in [events[:-1],events+[{'package':'internal/rpc/third','name':c['failedTargets']['internal/rpc/third'][0]+'/minio'}]]:
   with self.assertRaises(ValueError):m.require_children({'namedCompletions':altered},c)
if __name__=='__main__':unittest.main()
