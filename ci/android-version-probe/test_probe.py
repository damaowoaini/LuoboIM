"""Pure functions/owned files only. No server, upstream request, CLI or Gradle."""
import copy
import importlib.util
import io
import json
import os
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import broker

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('_r11_probe_under_test', HERE / 'run.py')
probe = importlib.util.module_from_spec(spec); spec.loader.exec_module(probe)


class Budget:
    def check(self):
        pass
    def latch(self, reason):
        self.reason = reason


class Origin:
    def __init__(self, bodies):
        self.bodies, self.calls = bodies, []
    def get(self, path, limit):
        self.calls.append(path)
        return self.bodies.get(path, (404, b'not found'))


def pom(group, name, version, dependencies=''):
    return ('<project><modelVersion>4.0.0</modelVersion><groupId>' + group + '</groupId><artifactId>' + name
        + '</artifactId><version>' + version + '</version><dependencies>' + dependencies + '</dependencies></project>').encode()


def dependency(group, name, version):
    return '<dependency><groupId>' + group + '</groupId><artifactId>' + name + '</artifactId><version>' + version + '</version></dependency>'


class ProbeTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.work = Path(self.tmp.name).resolve()
        self.inputs = json.loads((HERE / 'inputs.json').read_text())
        self.inputs['metadata_pins'] = []
    def recorder(self, bodies=None):
        return broker.Recorder(self.work, self.inputs, Budget(), Origin(bodies or {}))

    def actual_report_fixture(self):
        configs, responses, events = [], [], []
        def identity(g, m, v): return {'group':g, 'module':m, 'version':v}
        for name in sorted({r['configuration'] for r in self.inputs['roots']}):
            roots = [r for r in self.inputs['roots'] if r['configuration'] == name]
            edges = []
            for r in roots:
                v = '2.4.21' if r['module'] == 'kotlin-build-tools-impl' else r['constraint']['required']
                edges.append({'from':{'kind':'producer-root'}, 'requested':{**copy.deepcopy(r), 'version':r['constraint']['required']},
                    'selected':identity(r['group'],r['module'],v)})
            for r in self.inputs['rich']:
                if r['configuration'] == name and (r.get('source_parent') or r.get('source_parents')):
                    g,m,v = r.get('source_parents', [r.get('source_parent')])[0].split(':')
                    edges.append({'from':identity(g,m,v), 'requested':copy.deepcopy(r), 'selected':identity(r['group'],r['module'],'1.80')})
            ids = [e['selected'] for e in edges]
            artifacts = []
            for i in ids:
                leaf = i['module'] + '-' + i['version']
                data = ('synthetic:' + leaf).encode(); h = broker.digest(data)
                artifact = 'gradle-home/caches/modules-2/files-2.1/' + i['group'] + '/' + i['module'] + '/' + i['version'] + '/' + h + '/' + leaf + '.jar'
                broker.write_new(self.work / artifact, data)
                artifacts.append({'id':i, 'path':artifact, 'bytes':len(data),'sha256':h,'variant':{'fixture':True}})
                prefix = '/maven2/' + i['group'].replace('.','/') + '/' + i['module'] + '/' + i['version'] + '/' + leaf
                for suffix,kind,body in (('.pom','metadata',b'<project/>'),('.jar','artifact',data)):
                    path = prefix + suffix
                    response = {'path':path,'upstream':broker.ORIGIN+path,'status':200,'kind':kind,
                        'body':{'path':'unused-owned-fixture-body','bytes':len(body),'sha256':broker.digest(body)}}
                    responses.append(response); events.append({'method':'GET','response':copy.deepcopy(response),
                        'complete_GET':True,'HTTP_status':200,'partial_write_bytes_unknown':False,
                        'confirmed_body_sha256':broker.digest(body),'delivered_body_bytes':len(body),'failure':None})
            configs.append({'name':name,'attributes':{},'declared':[{'group':r['group'],'module':r['module'],'constraint':copy.deepcopy(r['constraint'])} for r in roots],
                'components':[{'id':i,'variants':[]} for i in ids], 'edges':edges,'artifacts':artifacts})
        actual = {'schema':1,'Gradle':'9.3.1','configurations':configs}
        broker.json_new(self.work/'evidence/online-actual.json',actual)
        return actual,responses,events

    def test_path_netloc_query_percent_traversal_refused(self):
        for path in ('https://example.com/maven2/a/b/c', '/maven2/a//b/c', '/maven2/a/../b',
                '/maven2/a/%2e%2e/b', '/maven2/a/b/c?token=x', '/maven2/a/b/c#fragment',
                '/maven2/a/b/c\\d', '/maven2/a/b/c:443', '/maven2/a/b/c\x00'):
            with self.subTest(path=path), self.assertRaises(ValueError):
                broker.clean_path(path)

    def test_literal_maven_path_positive(self):
        self.assertEqual(broker.clean_path('/maven2/org/jetbrains/kotlin/a/2.4.0/a-2.4.0.pom')[-1], 'a-2.4.0.pom')
        r = self.recorder()
        for suffix in ('.sha512','.sha256','.sha1','.md5'):
            self.assertEqual(r.admit('/maven2/org/bouncycastle/bcpg-jdk18on/1.80/bcpg-jdk18on-1.80.pom' + suffix)[0], 'checksum')

    def test_unknown_module_has_zero_origin_calls(self):
        r = self.recorder()
        with self.assertRaisesRegex(ValueError, 'unapproved_module_version'):
            r.obtain('/maven2/unknown/not-authorized/1.0/not-authorized-1.0.jar')
        self.assertEqual(r.origin.calls, [])

    def test_pinned_metadata_mismatch_prevents_authority(self):
        path = '/maven2/org/bouncycastle/bcpg-jdk18on/1.80/bcpg-jdk18on-1.80.pom'
        self.inputs['metadata_pins'] = [{'path': path, 'bytes': 3, 'sha256': '0' * 64}]
        r = self.recorder({path: (200, b'bad')})
        with self.assertRaisesRegex(ValueError, 'pin_mismatch'):
            r.obtain(path)
        self.assertEqual(r.cache, {})

    def test_actual_POM_learns_fixed_dependency_origin(self):
        path = '/maven2/org/jetbrains/kotlin/kotlin-build-tools-compat/2.4.0/kotlin-build-tools-compat-2.4.0.pom'
        body = pom('org.jetbrains.kotlin', 'kotlin-build-tools-compat', '2.4.0', dependency('org.jetbrains.kotlin', 'kotlin-tooling-core', '2.4.0'))
        r = self.recorder({path: (200, body)}); row = r.obtain(path)
        self.assertEqual(r.admit('/maven2/org/jetbrains/kotlin/kotlin-tooling-core/2.4.0/kotlin-tooling-core-2.4.0.jar')[0], 'artifact')
        self.assertEqual(r.sources['org.jetbrains.kotlin:kotlin-tooling-core:2.4.0'][0]['body'], row['body'])

    def test_reviewed_transitive_BC_parent_positive(self):
        path = '/maven2/org/bouncycastle/bcpg-jdk18on/1.80/bcpg-jdk18on-1.80.pom'
        body = pom('org.bouncycastle', 'bcpg-jdk18on', '1.80', dependency('org.bouncycastle', 'bcprov-jdk18on', '[1.80,1.81)'))
        r = self.recorder({path: (200, body)}); r.obtain(path)
        self.assertIn(('org.bouncycastle', 'bcprov-jdk18on'), r.ranged)

    def test_same_BC_range_wrong_parent_refused(self):
        r = self.recorder()
        with self.assertRaisesRegex(ValueError, 'unreviewed_rich'):
            r.grant('org.bouncycastle', 'bcprov-jdk18on', broker.constraint('[1.80,1.81)'),
                {'kind': 'published-metadata', 'coordinate': 'org.bouncycastle:bcpg-jdk18on:1.79'})

    def test_exact_bcutil_180_POM_parent_is_independently_bound(self):
        r = self.recorder()
        r.grant('org.bouncycastle', 'bcprov-jdk18on', broker.constraint('[1.80,1.81)'),
            {'kind':'published-metadata','coordinate':'org.bouncycastle:bcutil-jdk18on:1.80'})
        self.assertIn(('org.bouncycastle','bcprov-jdk18on'),r.ranged)
        with self.assertRaises(ValueError):
            r.grant('org.bouncycastle', 'bcprov-jdk18on', broker.constraint('[1.80,1.81)'),
                {'kind':'published-metadata','coordinate':'org.bouncycastle:bcutil-jdk18on:1.80.1'})

    def test_fourth_rich_selector_refused(self):
        with self.assertRaisesRegex(ValueError, 'unreviewed_rich'):
            self.recorder().grant('org.jetbrains.kotlin', 'kotlin-stdlib', broker.constraint('[1,2)'), {'kind': 'reviewed-generator'})

    def test_same_Kotlin_range_without_strict_refused(self):
        with self.assertRaisesRegex(ValueError, 'unreviewed_rich'):
            self.recorder().grant('org.jetbrains.kotlin', 'kotlin-build-tools-impl',
                broker.constraint('[2.4.0-Beta2, 2.5.0)'), {'kind': 'reviewed-generator'})

    def test_rich_preferred_not_hidden_by_fixed_required(self):
        c = broker.constraint('2.4.0', preferred='2.+')
        self.assertTrue(broker.rich(c))
        with self.assertRaisesRegex(ValueError, 'unreviewed_rich'):
            self.recorder().grant('org.jetbrains.kotlin', 'kotlin-build-tools-impl', c, {'kind': 'reviewed-generator'})

    def test_listing_identity_and_snapshot_versions(self):
        path = '/maven2/org/jetbrains/kotlin/kotlin-build-tools-impl/maven-metadata.xml'
        bad = b'<metadata><groupId>wrong</groupId><versioning><versions><version>2.4.0</version></versions></versioning></metadata>'
        with self.assertRaisesRegex(ValueError, 'listing_identity'):
            self.recorder({path: (200, bad)}).obtain(path)
        self.assertFalse(broker.fixed('2.4.0-SNAPSHOT'))

    def test_source_only_KGP_metadata_does_not_authorize_artifacts(self):
        path = '/maven2/org/jetbrains/kotlin/kotlin-gradle-plugin/2.4.0/kotlin-gradle-plugin-2.4.0.module'
        body = b'{"source-only":true}'
        self.inputs['metadata_pins'] = [{'path': path, 'bytes': len(body), 'sha256': broker.digest(body), 'source_only': True}]
        r = self.recorder({path: (200, body)}); r.obtain(path)
        with self.assertRaises(ValueError):
            r.admit(path.replace('.module', '.jar'))

    def test_module_external_file_URL_refused(self):
        path = '/maven2/org/jetbrains/kotlin/kotlin-build-tools-compat/2.4.0/kotlin-build-tools-compat-2.4.0.module'
        body = json.dumps({'component': {'group':'org.jetbrains.kotlin','module':'kotlin-build-tools-compat','version':'2.4.0'},
            'variants':[{'files':[{'name':'kotlin-build-tools-compat-2.4.0.jar','url':'https://example.com/file.jar'}]}]}).encode()
        with self.assertRaisesRegex(ValueError, 'external_module_file_URL'):
            self.recorder({path:(200,body)}).obtain(path)

    def test_duplicate_module_JSON_key_refused(self):
        with self.assertRaisesRegex(ValueError, 'duplicate_metadata'):
            broker.unique_json(b'{"component":{},"component":{}}')

    def test_redirect_is_refused_not_followed(self):
        with self.assertRaisesRegex(ValueError, 'redirect_refused'):
            broker.NoRedirect().redirect_request(None, None, 302, '', {}, broker.ORIGIN + '/maven2/other/file.pom')

    def test_actual_Central_ignores_ambient_proxy_CA_and_headers(self):
        class Response:
            def __init__(self):
                self.url = broker.ORIGIN + '/maven2/g/a/1/a-1.pom'
                self.code = 200
                self.headers = {'Content-Length':'2'}
                self.read = io.BytesIO(b'ok').read
            def __enter__(self): return self
            def __exit__(self, *args): pass
        opener = SimpleNamespace(open=lambda request, timeout: (calls.append(request) or Response()))
        calls = []
        with patch.dict(os.environ, {'HTTPS_PROXY':'http://invalid:1', 'SSL_CERT_FILE':'/not/used'}), \
                patch.object(broker.ssl, 'SSLContext') as context, \
                patch.object(broker.urllib.request, 'build_opener', return_value=opener) as build:
            c = broker.Central(Budget()); self.assertEqual(c.get('/maven2/g/a/1/a-1.pom', 10), (200,b'ok'))
            context.return_value.load_verify_locations.assert_called_once_with(cafile='/etc/ssl/certs/ca-certificates.crt')
            self.assertEqual(build.call_args.args[0].proxies, {})
            self.assertEqual(calls[0].full_url, broker.ORIGIN + '/maven2/g/a/1/a-1.pom')
            self.assertFalse(any(k.lower() in ('authorization','cookie','proxy-authorization') for k in calls[0].headers))

    def test_response_path_is_frozen_and_raw_body_exact(self):
        path = '/maven2/org/bouncycastle/bcpg-jdk18on/1.80/bcpg-jdk18on-1.80.pom'
        body = pom('org.bouncycastle', 'bcpg-jdk18on', '1.80')
        r = self.recorder({path:(200,body)}); a = r.obtain(path)
        r.origin.bodies[path] = (200, b'changed')
        self.assertEqual(r.obtain(path), a); self.assertEqual(r.origin.calls, [path])
        self.assertEqual((self.work / a['body']['path']).read_bytes(), body)

    def test_changed_captured_body_cannot_be_served_under_old_hash(self):
        path = '/maven2/org/bouncycastle/bcpg-jdk18on/1.80/bcpg-jdk18on-1.80.pom'
        body = pom('org.bouncycastle','bcpg-jdk18on','1.80')
        r = self.recorder({path:(200,body)}); row = r.obtain(path)
        with r.open_body(row) as stream:
            self.assertEqual(stream.read(),body)
        (self.work/row['body']['path']).write_bytes(b'x'*len(body))
        with self.assertRaisesRegex(ValueError,'captured_body_bytes_changed'):
            r.open_body(row)

    def test_parent_inheritance_never_guessed(self):
        path = '/maven2/org/bouncycastle/bcpg-jdk18on/1.80/bcpg-jdk18on-1.80.pom'
        body = pom('org.bouncycastle', 'bcpg-jdk18on', '1.80').replace(b'<dependencies>', b'<parent><groupId>g</groupId><artifactId>a</artifactId><version>1</version></parent><dependencies>')
        with self.assertRaisesRegex(ValueError, 'parent_inheritance'):
            self.recorder({path:(200,body)}).obtain(path)

    def test_low_disk_admission_reaches_actual_free_gate(self):
        self.work.chmod(0o700)
        with patch.object(probe.os, 'statvfs', return_value=SimpleNamespace(f_bavail=1,f_frsize=1)) as free:
            with self.assertRaisesRegex(ValueError, 'initial host/output disk gate'):
                probe.admission(self.work / 'new', 8 * probe.GIB, True)
            free.assert_called()

    def test_source_state_refuses_symlink(self):
        (self.work / 'project').mkdir()
        for n in ('build.gradle','settings.gradle','gradle.properties','inputs.json'):
            (self.work / 'project' / n).write_bytes(b'{}')
        (self.work / 'project/build.gradle').unlink()
        (self.work / 'project/build.gradle').symlink_to(self.work / 'project/settings.gradle')
        with self.assertRaises(ValueError): probe.source_state(self.work)

    def test_offline_command_cannot_retarget_or_add_argv(self):
        argv = list(map(str, probe.command(self.work, 'http://127.0.0.1:1234/maven2/', 'offline')))
        self.assertIn('--offline', argv); self.assertEqual(argv[-1], 'produceVersionCatalog')
        self.assertFalse(any(x in argv for x in ('--init-script','--refresh-dependencies')))
        self.assertTrue(any(x == '-DluoboProbeOrigin=http://127.0.0.1:1234/maven2/' for x in argv))

    def test_graph_verifier_accepts_three_exact_actual_contracts(self):
        actual,responses,events = self.actual_report_fixture()
        got,sources = probe.verify_graph(self.work,'online',self.inputs,responses,events)
        self.assertEqual(got,actual); self.assertEqual(len(sources),6)

    def test_graph_verifier_HEAD_cannot_stand_for_consumed_metadata(self):
        actual,responses,events = self.actual_report_fixture()
        events[0].update(method='HEAD', complete_GET=False, delivered_body_bytes=0)
        with self.assertRaisesRegex(ValueError,'real consumed broker metadata'):
            probe.verify_graph(self.work,'online',self.inputs,responses,events)

    def test_graph_verifier_changed_preferred_tuple_refused(self):
        actual,responses,events = self.actual_report_fixture()
        edge = next(e for c in actual['configurations'] for e in c['edges'] if e['requested']['module']=='bcprov-jdk18on')
        edge['requested']['constraint']['preferred'] = '1.80'
        (self.work/'evidence/online-actual.json').write_text(json.dumps(actual))
        with self.assertRaisesRegex(ValueError,'fourth rich selector|root/configuration'):
            probe.verify_graph(self.work,'online',self.inputs,responses,events)


if __name__ == '__main__':
    unittest.main(verbosity=2)
