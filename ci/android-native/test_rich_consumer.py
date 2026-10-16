"""Affected pure controls only. No Go/Gradle/CLI/network invocation."""
import copy
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock,patch

import rich_consumer as rich
import rich_fixture as fixture
import support as s

HERE=Path(__file__).resolve().parent


def report(catalog,config):
    return dict(catalog_sha256=rich.CATALOG_SHA,project=catalog['scope']['project'],
        configuration=config['name'],scope_sources=copy.deepcopy(catalog['scope']['sources']),
        no_force_or_substitution=True,edges=copy.deepcopy(config['edges']),
        artifacts=[{k:v for k,v in a.items() if k!='origin'} for a in copy.deepcopy(config['artifacts'])])


def origins(catalog):
    rows=[]
    for c in catalog['configurations']:
        for a in c['artifacts']:
            rows.append(dict(coordinate=':'.join(a['id'][k] for k in ['group','module','version']),
                filename=a['filename'],bytes=a['bytes'],sha256=a['sha256'],observed_sources=[a['origin']]))
    for m in rich.metadata_sources(catalog):
        rows.append(dict(coordinate=m['coordinate'],filename=m['filename'],bytes=m['bytes'],sha256=m['sha256'],observed_sources=[m['origin']]))
    return dict(artifacts=rows)


class RichConsumerTests(unittest.TestCase):
    def test_independent_embed_checks_actual_catalog_and_library_bytes(self):
        c,body,script=rich.approved(HERE)
        value=rich.embedded(HERE)
        self.assertEqual(value['catalog_sha256'],rich.sha(body))
        self.assertEqual(value['script_sha256'],rich.sha(script))
        self.assertEqual(len(c['rich']),3)
        with tempfile.TemporaryDirectory() as name:
            root=Path(name).resolve()
            for fn in ['rich-catalog.json','rich-consumer.gradle']:s.put(root/fn,rich.read(HERE/fn))
            with (root/'rich-catalog.json').open('ab') as f:f.write(b'\n')
            with self.assertRaisesRegex(ValueError,'pin differs'):rich.embedded(root)

    def test_actual_accepted_two_graphs_and_artifact18_positive(self):
        catalog,_,_=rich.approved(HERE)
        self.assertEqual(sum(len(c['artifacts']) for c in catalog['configurations']),18)
        for c in catalog['configurations']:self.assertIs(rich.validate_report(catalog,report(catalog,c)),c)
        self.assertEqual(len(rich.verify_origin_rows(catalog,origins(catalog))),18)

    def test_fourth_selector_parent_constraint_selected_source_and_force_rejected(self):
        c,_,_=rich.approved(HERE);base=report(c,c['configurations'][0])
        for mode in ['fourth','parent','strict','prefer','reject','selected','source','force','catalog']:
            with self.subTest(mode=mode):
                r=copy.deepcopy(base)
                if mode=='fourth':r['edges'].append(copy.deepcopy(r['edges'][0]))
                if mode=='parent':r['edges'][0]['from']={'group':'unapproved','module':'parent','version':'1.0'}
                if mode=='strict':r['edges'][0]['requested']['constraint']['strict']='[0,9)'
                if mode=='prefer':r['edges'][0]['requested']['constraint']['preferred']='2.4.0'
                if mode=='reject':r['edges'][0]['requested']['constraint']['rejected']=['2.4.21']
                if mode=='selected':r['edges'][0]['selected']['version']='2.4.0'
                if mode=='source':r['scope_sources']['app/android/settings.gradle.kts']='0'*64
                if mode=='force':r['no_force_or_substitution']=False
                if mode=='catalog':r['catalog_sha256']='0'*64
                with self.assertRaises(ValueError):rich.validate_report(c,r)

    def test_selected_body_origin_and_generator_parent_metadata_rejected(self):
        c,_,_=rich.approved(HERE)
        for mode in ['jar','origin','pom','generator','parent','missing']:
            with self.subTest(mode=mode):
                actual=origins(c)
                if mode=='jar':actual['artifacts'][0]['sha256']='0'*64
                if mode=='origin':actual['artifacts'][0]['observed_sources']=['https://unapproved.invalid/file.jar']
                if mode=='pom':next(r for r in actual['artifacts'] if r['filename'].endswith('.pom'))['sha256']='0'*64
                if mode=='generator':next(r for r in actual['artifacts'] if r['filename']=='kotlin-gradle-plugin-2.4.0.module')['sha256']='0'*64
                if mode=='parent':next(r for r in actual['artifacts'] if r['filename']=='bcpg-jdk18on-1.80.pom')['sha256']='0'*64
                if mode=='missing':actual['artifacts'].pop(0)
                with self.assertRaises(ValueError):rich.verify_origin_rows(c,actual)

    def test_binding_checks_reports_and_origin_again_before_apk(self):
        c,_,_=rich.approved(HERE)
        with tempfile.TemporaryDirectory() as name:
            root=Path(name).resolve()
            for config in c['configurations']:
                s.write_json(root/'evidence/rich-consumed/metadata'/(config['name']+'.json'),report(c,config))
            s.write_json(root/'evidence/dependency-origin-audit.json',origins(c))
            rich.bind_origins(root,HERE,s.write_json)
            rich.require_origin_binding(root,HERE)
            self.assertEqual(rich.check_apk_reports(root,HERE),[])
            target=root/'evidence/dependency-origin-audit.json'
            with target.open('ab') as f:f.write(b'\n')
            with self.assertRaisesRegex(ValueError,'binding changed'):rich.require_origin_binding(root,HERE)

    def test_report_symlink_and_missing_configuration_rejected(self):
        c,_,_=rich.approved(HERE)
        with tempfile.TemporaryDirectory() as name:
            root=Path(name).resolve();d=root/'evidence/rich-consumed/metadata'
            s.write_json(d/(c['configurations'][0]['name']+'.json'),report(c,c['configurations'][0]))
            with self.assertRaisesRegex(ValueError,'complete actual'):rich.reports(root,HERE,'metadata',True)
            s.put(root/'elsewhere.json',b'{}')
            (d/(c['configurations'][1]['name']+'.json')).symlink_to(root/'elsewhere.json')
            with self.assertRaisesRegex(ValueError,'canonical'):rich.reports(root,HERE,'metadata',True)

    def test_six_actual_Gradle_control_sources_prepared_without_execution(self):
        with tempfile.TemporaryDirectory() as name:
            root=Path(name).resolve();cases=fixture.prepare(root,HERE)
            self.assertEqual(len(cases),6)
            self.assertIsNone(cases[0]['expected_rejection'])
            self.assertEqual(sum(c['expected_rejection'] is not None for c in cases),5)
            positive=Path(cases[0]['cwd'])
            value=json.loads((positive/'control-catalog.json').read_text())
            self.assertEqual(len(value['rich']),3)
            self.assertEqual(value['library_sha256'],rich.SCRIPT_SHA)
            self.assertIn('strictly', (positive/'app/build.gradle').read_text())
            self.assertEqual((positive/'rich-consumer.gradle').read_bytes(),rich.read(HERE/'rich-consumer.gradle'))
            self.assertFalse((positive/'actual-controls.json').exists())
            with self.assertRaises(FileExistsError):fixture.prepare(root,HERE)

    def test_actual_APK_dispatch_binds_same_catalog_and_rechecks_after(self):
        import observed_bind as observed
        with tempfile.TemporaryDirectory() as name:
            root=Path(name).resolve();job=object.__new__(observed.Job);job.w=root
            s.put(root/'artifacts/core-current257-arm64.aar',b'synthetic_not_an_AAR')
            original=job.gradle('assembleDebug');env=observed.parent.environment(root,True)
            catalog=(root/'evidence/classpath-preseal/catalog.json','1'*64)
            with patch.object(observed.classpaths,'verified_catalog',return_value=catalog), \
                    patch.object(observed.rich_consumer,'require_origin_binding') as before, \
                    patch.object(observed.rich_consumer,'check_apk_reports',return_value=['kotlinAbiValidationCompatClasspath']) as after, \
                    patch.object(observed.parent.Job,'cmd',return_value=root/'log') as dispatch:
                self.assertEqual(job.cmd(original,root/'source/app/android',env,'assemble-debug',1800),root/'log')
                before.assert_called_once_with(root,observed.HERE)
                after.assert_called_once_with(root,observed.HERE)
                args=dispatch.call_args.args[0]
                self.assertEqual(args[:len(original)],original)
                self.assertEqual(args[-4:],['-I',observed.HERE/'resolve-observed.gradle',
                    '-DluoboClasspathCatalog='+str(catalog[0]),'-DluoboClasspathCatalogSha256='+catalog[1]])
            with patch.object(observed.parent.Job,'cmd') as dispatch:
                with self.assertRaisesRegex(ValueError,'fixed final APK'):
                    job.cmd(original+['--refresh-dependencies'],root/'source/app/android',env,'assemble-debug',1800)
                dispatch.assert_not_called()


if __name__=='__main__':unittest.main()
