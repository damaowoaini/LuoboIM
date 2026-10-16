#!/usr/bin/env python3
"""Two prepared source-only controller checks; no Go/verify/DB/network calls."""
import hashlib
import importlib.util
from pathlib import Path
import tempfile
import unittest

SPEC=importlib.util.spec_from_file_location('pure_collision_controller',Path(__file__).with_name('run.py'))
SUBJECT=importlib.util.module_from_spec(SPEC);SPEC.loader.exec_module(SUBJECT)


class ControllerTests(unittest.TestCase):
    def test_only_failed_top_pass_is_new_and_any_old_case_or_bad_footer_rejects(self):
        package=SUBJECT.PREFIX+SUBJECT.PACKAGE
        events=[{'Package':package,'Action':'start'}, {'Package':package,'Action':'run','Test':SUBJECT.TOP},
                {'Package':package,'Action':'pass','Test':SUBJECT.TOP}, {'Package':package,'Action':'pass'}]
        self.assertEqual(SUBJECT.audit(events,0,SUBJECT.PACKAGE,SUBJECT.EXPECTED)['topPASS'],1)
        for change in ('missing-run','duplicate-pass','skip','fail','old-pass','subtest','race','missing-footer','post-footer','nonzero'):
            bad=[dict(x)for x in events];code=0
            if change=='missing-run':bad.pop(1)
            elif change=='duplicate-pass':bad.insert(2,dict(bad[2]))
            elif change in ('skip','fail'):bad[2]['Action']=change
            elif change=='old-pass':bad[1]['Test']='TestNamespaceInventoryOwnedStoresReadOnly'
            elif change=='subtest':bad[1]['Test']=SUBJECT.TOP+'/unselected'
            elif change=='race':bad.insert(1,{'Package':package,'Action':'output','Output':'WARNING: DATA RACE'})
            elif change=='missing-footer':bad.pop()
            elif change=='post-footer':bad.append({'Package':package,'Action':'output'})
            else:code=1
            with self.subTest(change=change),self.assertRaises(ValueError):SUBJECT.audit(bad,code,SUBJECT.PACKAGE,SUBJECT.EXPECTED)
        with self.assertRaises(ValueError):SUBJECT.require_guard({'status':'stopped_timeout','exit_code':-15},75)

    def test_exact_fixture_options_and_precondition_are_the_only_allowed_difference(self):
        old_repo=SUBJECT.REPO
        try:
            with tempfile.TemporaryDirectory()as temporary:
                SUBJECT.REPO=Path(temporary).resolve()
                path='ci/candidates/namespace-inventory-collision/source/'+SUBJECT.TARGET
                p=SUBJECT.REPO/path;p.parent.mkdir(parents=True)
                original=b'package candidate\n'+SUBJECT.OLD_NOTICE+SUBJECT.ROWS_ANCHOR+b'original-assertions-and-other-tests\n'
                updated=original.replace(SUBJECT.OLD_NOTICE,SUBJECT.NEW_NOTICE,1).replace(SUBJECT.ROWS_ANCHOR,SUBJECT.PRECONDITION+SUBJECT.ROWS_ANCHOR,1)
                before={SUBJECT.TARGET:original,'server/production.go':b'unchanged production'}
                def row(raw):return {'path':path,'target':SUBJECT.TARGET,'mode':'0o644','beforeSHA256':hashlib.sha256(original).hexdigest(),'bytes':len(raw),'sha256':hashlib.sha256(raw).hexdigest()}
                p.write_bytes(updated);p.chmod(0o644)
                self.assertEqual(SUBJECT.apply_fixture({'overlay':[row(updated)]},before),{**before,SUBJECT.TARGET:updated})
                for change in ('weaken-assertion','other-helper','wrong-before','foreign-target','mode','extra'):
                    raw=updated;c={'overlay':[row(raw)]};extra=p.with_name('extra.go')
                    if change=='weaken-assertion':raw=raw.replace(b'original-assertions',b'weakened-assertions')
                    elif change=='other-helper':raw+=b'additional helper mutation\n'
                    elif change=='wrong-before':c['overlay'][0]['beforeSHA256']='0'*64
                    elif change=='foreign-target':c['overlay'][0]['target']='server/production.go'
                    elif change=='mode':p.chmod(0o600)
                    else:extra.write_bytes(b'package extra')
                    if raw!=updated:c={'overlay':[row(raw)]};p.write_bytes(raw)
                    with self.subTest(change=change),self.assertRaises(ValueError):SUBJECT.apply_fixture(c,before)
                    p.write_bytes(updated);p.chmod(0o644)
                    if extra.exists():extra.unlink()
        finally:SUBJECT.REPO=old_repo


if __name__=='__main__':unittest.main(verbosity=2)
