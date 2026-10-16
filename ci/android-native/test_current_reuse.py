"""Only new recovery custody/identity controls; no Go/Gradle/API invocation."""
import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
import zipfile

import current_reuse as r


class Recovery(unittest.TestCase):
    def test_only_original354_source_and_original_run_can_be_imported(self):
        spec=json.loads((r.HERE/'core-reuse.json').read_bytes())
        context=json.loads((r.HERE/'current-app-cohort.json').read_bytes())
        inputs=json.loads((r.HERE/'current-app-inputs.json').read_bytes())
        r.validate_binding(spec,context,inputs)
        for key,value in [('source_digest','0'*64),('source_ref','0'*40)]:
            bad=copy.deepcopy(spec);bad[key]=value
            with self.assertRaises(ValueError):r.validate_binding(bad,context,inputs)
        bad=copy.deepcopy(spec);bad['parent']['run']+=1
        with self.assertRaises(ValueError):r.validate_binding(bad,context,inputs)

    def test_actual_custody_extraction_requires_complete_products_and_member_hashes(self):
        spec=json.loads((r.HERE/'core-reuse.json').read_bytes());c=r.custody(spec)
        names=[row['path'] for row in spec['product_pins']]
        with tempfile.TemporaryDirectory() as text:
            root=Path(text);pins=[]
            def bundle(path,selected):
                with zipfile.ZipFile(path,'w') as z:
                    for name in selected:z.writestr(name,('synthetic:'+name).encode())
                return {'bytes':path.stat().st_size,'sha256':c.sha(path),
                    'members_cap':8,'decoded_cap':8192,'member_cap':2048}
            archive=root/'good.zip';cap=bundle(archive,names)
            for name in names:pins.append({'path':name,'sha256':hashlib.sha256(('synthetic:'+name).encode()).hexdigest()})
            c.extract(archive,cap,root/'good',set(names))
            self.assertEqual(set(r.product_files(root/'good',{'product_pins':pins})),set(names))
            (root/'good'/names[0]).write_bytes(b'tampered')
            with self.assertRaises(ValueError):r.product_files(root/'good',{'product_pins':pins})
            for label,selected in [('missing',names[:-1]),('extra',names+['unapproved-private-key'])]:
                archive=root/(label+'.zip');cap=bundle(archive,selected)
                with self.assertRaises(ValueError):c.extract(archive,cap,root/label,set(names))

    def test_recovery_job_cannot_dispatch_a_fresh_bind(self):
        job=object.__new__(r.Job)
        with self.assertRaisesRegex(ValueError,'fresh Go bind is disabled'):
            job.build_core()


if __name__=='__main__':unittest.main()
