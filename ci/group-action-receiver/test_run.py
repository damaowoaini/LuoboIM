#!/usr/bin/env python3
"""Six prepared pure controller tests; no Go/DB/container/download operation."""
import hashlib
import importlib.util
from pathlib import Path
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location('receiver_pure_controller', Path(__file__).with_name('run.py'))
SUBJECT = importlib.util.module_from_spec(SPEC); SPEC.loader.exec_module(SUBJECT)


def events(names):
    package = SUBJECT.PREFIX + 'internal/synthetic'
    result = [{'Package':package, 'Action':'start'}]
    for name in names:
        result.extend([{'Package':package,'Action':'run','Test':name},
                       {'Package':package,'Action':'pass','Test':name}])
    result.append({'Package':package,'Action':'pass'})
    return result


class ControllerTests(unittest.TestCase):
    def test_exact_raw_style_selected_names_and_footer_are_accepted(self):
        result = SUBJECT.audit(events(['TestFirst','TestSecond']), 0, 'internal/synthetic', ['TestFirst','TestSecond'])
        self.assertEqual((result['topPASS'],result['subPASS'],result['skip']), (2,0,0))
        self.assertEqual(sum(len(x) for x in SUBJECT.PURE13.values()),13)

    def test_compile_only_missing_duplicate_or_nonzero_cannot_pass(self):
        for raw, code in [(events([]),0),(events(['TestFirst']),0),
                          (events(['TestFirst','TestFirst','TestSecond']),0),
                          (events(['TestFirst','TestSecond']),1)]:
            with self.subTest(code=code, length=len(raw)), self.assertRaises(ValueError):
                SUBJECT.audit(raw,code,'internal/synthetic',['TestFirst','TestSecond'])

    def test_foreign_test_subtest_skip_and_package_are_rejected(self):
        for change in ('extra','sub','skip','package'):
            raw = events(['TestFirst']); package=SUBJECT.PREFIX+'internal/synthetic'
            if change=='extra': raw.insert(1,{'Package':package,'Action':'run','Test':'TestForeign'})
            if change=='sub': raw.insert(1,{'Package':package,'Action':'run','Test':'TestFirst/foreign'})
            if change=='skip': raw[2]['Action']='skip'
            if change=='package': raw[1]['Package']='foreign/package'
            with self.subTest(change=change), self.assertRaises(ValueError):
                SUBJECT.audit(raw,0,'internal/synthetic',['TestFirst'])

    def test_race_panic_or_post_terminal_evidence_is_not_accepted(self):
        package=SUBJECT.PREFIX+'internal/synthetic'
        for text in ('panic: synthetic','WARNING: DATA RACE','fatal error: synthetic'):
            raw=events(['TestFirst']);raw.insert(1,{'Package':package,'Action':'output','Output':text})
            with self.subTest(text=text), self.assertRaises(ValueError):
                SUBJECT.audit(raw,0,'internal/synthetic',['TestFirst'])
        raw=events(['TestFirst']);raw.append({'Package':package,'Action':'output','Output':'trailing'})
        with self.assertRaises(ValueError):SUBJECT.audit(raw,0,'internal/synthetic',['TestFirst'])

    def test_resource_stop_unknown_or_contradictory_exit_cannot_pass(self):
        SUBJECT.require_guard({'status':'passed','exit_code':0},0)
        SUBJECT.require_guard({'status':'failed','exit_code':1},1)
        for record,code in [({'status':'stopped_task_allocation_gate','exit_code':-15},75),
                            ({'status':'monitor_error','exit_code':None},75),
                            ({'status':'passed','exit_code':1},1),
                            ({'status':'passed','exit_code':0},1)]:
            with self.subTest(record=record),self.assertRaises(ValueError):SUBJECT.require_guard(record,code)

    def test_real_tiny_overlay_checks_before_hash_mode_and_extra_member(self):
        previous=SUBJECT.REPO
        try:
            with tempfile.TemporaryDirectory() as root:
                SUBJECT.REPO=Path(root).resolve();target='server/internal/example.go'
                path='ci/candidates/group-action-receiver/source/'+target;p=SUBJECT.REPO/path
                p.parent.mkdir(parents=True);body=b'package example\n';p.write_bytes(body);p.chmod(0o644)
                row={'path':path,'target':target,'mode':'0o644','beforeSHA256':None,
                     'bytes':len(body),'sha256':hashlib.sha256(body).hexdigest()}
                self.assertEqual(SUBJECT.apply_overlay({'overlay':[row]},{}),{target:body})
                with self.assertRaises(ValueError):SUBJECT.apply_overlay({'overlay':[row]},{target:b'old'})
                p.chmod(0o600)
                with self.assertRaises(ValueError):SUBJECT.apply_overlay({'overlay':[row]}, {})
                p.chmod(0o644);(p.parent/'extra.go').write_bytes(body)
                with self.assertRaises(ValueError):SUBJECT.apply_overlay({'overlay':[row]}, {})
        finally:SUBJECT.REPO=previous


if __name__=='__main__':unittest.main(verbosity=2)
