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


def jvm_api_variant(data):
    return next(v for v in data['variants'] if v['name']=='jvmApiElements-published')


class ProbeTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.work = Path(self.tmp.name).resolve()
        self.inputs = json.loads((HERE / 'inputs.json').read_text())
        self.inputs['metadata_pins'] = []
    def recorder(self, bodies=None):
        return broker.Recorder(self.work, self.inputs, Budget(), Origin(bodies or {}))

    def pair_recorder(self, child_change=None, parent_change=None, spec_change=None,
            repin_child=False, repin_parent=False):
        inputs = json.loads((HERE / 'inputs.json').read_text())
        pair = inputs['published_component_pairs'][0]
        bodies = {}
        for side, name, change, repin in (
                ('child','kotlinx-coroutines-core-jvm-1.8.0.module',child_change,repin_child),
                ('parent','kotlinx-coroutines-core-1.8.0.module',parent_change,repin_parent)):
            body = (HERE / 'fixtures' / name).read_bytes()
            self.assertEqual(broker.digest(body),pair[side]['sha256'])
            if change:
                data = json.loads(body); change(data); body = json.dumps(data).encode()
            if repin:
                # Synthetic reviewed-input copies reach link/identity checks beyond
                # the real immutable hash gate. Actual shipped inputs are untouched.
                pair[side].update(bytes=len(body),sha256=broker.digest(body))
                pin = next(x for x in inputs['metadata_pins'] if x['path']==pair[side]['path'])
                pin.update(bytes=len(body),sha256=broker.digest(body))
            bodies[pair[side]['path']] = (200,body)
        if spec_change:
            spec_change(pair)
        self.pair_turn = getattr(self,'pair_turn',0) + 1
        work = self.work / ('pair-' + str(self.pair_turn)); work.mkdir()
        r = broker.Recorder(work,inputs,Budget(),Origin(bodies))
        # Existing fixed transitive authority; actual POM->grant behavior is
        # already covered by the original unchanged test_actual_POM... method.
        r.grant(*pair['child']['coordinate'][:2],broker.constraint('1.8.0'),
            {'kind':'published-metadata','coordinate':'unit:already-authorized:1'})
        return r,pair['child']['path'],pair['parent']['path']

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

    def test_real_KMP_pair_learns_only_authorized_leaf(self):
        r,path,parent = self.pair_recorder()
        row = r.obtain(path)
        self.assertEqual(r.states[path],'validated'); self.assertEqual(r.states[parent],'validated')
        self.assertEqual(r.origin.calls,[path,parent])
        self.assertEqual(r.pair_proofs[path]['parent_response']['kind'],'source-only')
        self.assertEqual(len(r.pair_proofs[path]['verified_links']),3)
        expected = {(x['group'],x['module']) for x in r.inputs['roots']} | {
            ('org.jetbrains.kotlinx','kotlinx-coroutines-core-jvm'),('org.jetbrains.kotlinx','kotlinx-coroutines-bom'),
            ('org.jetbrains','annotations'),('org.jetbrains.kotlin','kotlin-stdlib')}
        self.assertEqual(set(r.versions),expected)
        self.assertEqual(row['body']['sha256'],'ff6a22da40040938751db0ae21177e76517dbf126a76796f5426727bf76c1228')
        self.assertIn('kotlinx-coroutines-core-jvm-1.8.0.jar',r.files[('org.jetbrains.kotlinx','kotlinx-coroutines-core-jvm','1.8.0')])
        self.assertIsNone(r.failure)

    def test_proof_only_parent_cannot_be_client_metadata(self):
        r,path,parent = self.pair_recorder()
        with self.assertRaisesRegex(ValueError,'internal_parent_proof_not_client_metadata'):
            r.admit_client(parent)
        self.assertEqual(r.origin.calls,[])

    def test_pair_real_child_or_parent_hash_change_refused(self):
        for side in ('child','parent'):
            with self.subTest(side=side):
                changes={side+'_change':lambda x:x.update(createdBy={'synthetic':'changed'})}
                r,path,parent = self.pair_recorder(**changes)
                with self.assertRaisesRegex(ValueError,'published_root_metadata_pin_mismatch'):
                    r.obtain(path)
                self.assertNotIn(path,r.cache); self.assertEqual(r.states[path],'rejected')
                self.assertNotIn(('org.jetbrains','annotations'),r.versions)

    def test_pair_unknown_component_identity_or_version_refused(self):
        for key,value in (('module','unreviewed-component'),('version','1.8.1')):
            with self.subTest(field=key):
                r,path,parent = self.pair_recorder(child_change=lambda x:x['component'].update({key:value}),repin_child=True)
                with self.assertRaisesRegex(ValueError,'published_pair_child_component'):
                    r.obtain(path)
                self.assertEqual(r.origin.calls,[path]); self.assertNotIn(path,r.cache)

    def test_pair_forward_cross_origin_absolute_and_query_urls_refused(self):
        for bad in ('https://outside.example/a.module',
                'https://repo.maven.apache.org/maven2/org/jetbrains/kotlinx/kotlinx-coroutines-core/1.8.0/kotlinx-coroutines-core-1.8.0.module',
                '../../kotlinx-coroutines-core/1.8.0/kotlinx-coroutines-core-1.8.0.module?synthetic=1',
                '../../kotlinx-coroutines-core/1.8.0/kotlinx-coroutines-core-1.8.0.module#fragment',
                '../../kotlinx-coroutines-core/1.8.0/%6botlinx-coroutines-core-1.8.0.module'):
            with self.subTest(URL=bad):
                r,path,parent = self.pair_recorder(child_change=lambda x:x['component'].update(url=bad),repin_child=True,
                    spec_change=lambda x:x['child']['published_component'].update(url=bad))
                with self.assertRaisesRegex(ValueError,'published_pair_link_refused'):
                    r.obtain(path)
                self.assertEqual(r.origin.calls,[path]); self.assertNotIn(path,r.cache)

    def test_pair_wrong_missing_backlink_or_attributes_refused(self):
        def missing(x): del jvm_api_variant(x)['available-at']
        for change in (missing,
                lambda x:jvm_api_variant(x)['available-at'].update(module='unreviewed-leaf'),
                lambda x:jvm_api_variant(x)['attributes'].update({'org.gradle.usage':'unreviewed-usage'})):
            r,path,parent = self.pair_recorder(parent_change=change,repin_parent=True)
            with self.assertRaisesRegex(ValueError,'published_pair_backlink_or_attributes'):
                r.obtain(path)
            self.assertNotIn(path,r.cache); self.assertNotIn(('org.jetbrains','annotations'),r.versions)

    def test_pair_reverse_cross_origin_link_refused_even_with_same_tuple(self):
        bad='https://outside.example/unreviewed.module'
        r,path,parent = self.pair_recorder(parent_change=lambda x:jvm_api_variant(x)['available-at'].update(url=bad),repin_parent=True,
            spec_change=lambda x:x['links'][0]['available_at'].update(url=bad))
        with self.assertRaisesRegex(ValueError,'published_pair_link_refused'):
            r.obtain(path)
        self.assertNotIn(path,r.cache)

    def test_unknown_component_pair_is_not_general_exception(self):
        r,path,parent = self.pair_recorder(); r.inputs['published_component_pairs']=[]
        with self.assertRaisesRegex(ValueError,'unreviewed_published_component_pair'):
            r.obtain(path)
        self.assertEqual(r.origin.calls,[path]); self.assertNotIn(path,r.cache)

    def test_exact_pair_cannot_hide_fourth_rich_dependency(self):
        r,path,parent = self.pair_recorder(
            child_change=lambda x:x['variants'][0]['dependencies'][0]['version'].update(requires='[1,2)'),repin_child=True)
        with self.assertRaisesRegex(ValueError,'unreviewed_rich_metadata_edge'):
            r.obtain(path)
        self.assertEqual(r.states[path],'rejected'); self.assertNotIn(path,r.cache)
        calls=list(r.origin.calls)
        with self.assertRaisesRegex(ValueError,'pending_or_rejected_response_not_servable'):
            r.obtain(path)
        with self.assertRaisesRegex(ValueError,'broker_failure_latched'):
            r.obtain(parent)
        self.assertEqual(r.origin.calls,calls)

    def test_rejected_metadata_retry_retains_witness_without_cache_revival(self):
        path='/maven2/org/bouncycastle/bcpg-jdk18on/1.80/bcpg-jdk18on-1.80.pom'
        wrong=pom('wrong.group','bcpg-jdk18on','1.80')
        r=self.recorder({path:(200,wrong)})
        with self.assertRaisesRegex(ValueError,'POM_identity_mismatch'): r.obtain(path)
        r.origin.bodies[path]=(200,pom('org.bouncycastle','bcpg-jdk18on','1.80'))
        with self.assertRaisesRegex(ValueError,'pending_or_rejected_response_not_servable'): r.obtain(path)
        self.assertEqual(r.origin.calls,[path]); self.assertEqual(r.cache,{})
        self.assertEqual((r.work/r.observations[path]['body']['path']).read_bytes(),wrong)
        r.snapshot('rejected-unit'); snapshot=json.loads((r.work/'evidence/broker/rejected-unit.json').read_text())
        self.assertEqual(snapshot['responses'],[])
        self.assertEqual(snapshot['rejection_codes'],{path:'POM_identity_mismatch'})
        self.assertEqual(len(snapshot['rejected_responses']),1)

    def test_pending_response_is_not_servable_during_origin_read(self):
        path='/maven2/org/bouncycastle/bcpg-jdk18on/1.80/bcpg-jdk18on-1.80.pom'
        r=self.recorder(); seen=[]
        def origin_read(request,limit):
            try: r.obtain(path)
            except ValueError as error: seen.append(str(error))
            return 200,pom('org.bouncycastle','bcpg-jdk18on','1.80')
        r.origin.get=origin_read
        r.obtain(path)
        self.assertEqual(seen,['pending_or_rejected_response_not_servable'])
        self.assertEqual(r.states[path],'validated')

    def test_untrusted_error_text_and_nonadmitted_paths_are_not_public_evidence(self):
        self.assertEqual(broker.refusal_code(ValueError('untrusted /query?synthetic=1')),'metadata_or_IO_refused')
        self.assertEqual(broker.refusal_code(ValueError('POM_identity_mismatch')),'POM_identity_mismatch')
        r=self.recorder()
        with self.assertRaisesRegex(ValueError,'unsafe_maven_path'):
            r.obtain('/maven2/org/bouncycastle/bcpg/1/a.pom?synthetic=1')
        self.assertEqual(r.states,{}); self.assertEqual(r.rejections,{})
        self.assertEqual(r.observations,{}); self.assertEqual(r.origin.calls,[])

    def test_prevalidated_parent_body_tamper_refused_before_child_grants(self):
        r,path,parent = self.pair_recorder(); row=r.obtain(parent)
        p=r.work/row['body']['path']; body=p.read_bytes(); p.write_bytes(b'x'+body[1:])
        with self.assertRaisesRegex(ValueError,'captured_body_bytes_changed'):
            r.obtain(path)
        self.assertEqual(r.states[path],'rejected'); self.assertNotIn(path,r.cache)
        self.assertNotIn(('org.jetbrains','annotations'),r.versions)

    def test_arbitrary_ASCII_word_is_not_a_trusted_refusal_enum(self):
        self.assertEqual(broker.refusal_code(ValueError('syntheticCredentialAlpha123')),'metadata_or_IO_refused')
        self.assertEqual(broker.refusal_code(ValueError('ordinaryASCIIword')),'metadata_or_IO_refused')

    def test_origin_ASCII_word_never_enters_public_rejection_or_snapshot(self):
        path='/maven2/org/bouncycastle/bcpg-jdk18on/1.80/bcpg-jdk18on-1.80.pom'
        r=self.recorder()
        def origin_failure(request,limit): raise ValueError('syntheticCredentialAlpha123')
        r.origin.get=origin_failure
        with self.assertRaisesRegex(ValueError,'syntheticCredentialAlpha123'): r.obtain(path)
        self.assertEqual(r.failure,'metadata_or_IO_refused')
        self.assertEqual(r.rejections,{path:'metadata_or_IO_refused'})
        r.snapshot('opaque-origin-unit')
        body=(r.work/'evidence/broker/opaque-origin-unit.json').read_text()
        self.assertNotIn('syntheticCredentialAlpha123',body)
        self.assertEqual(json.loads(body)['rejection_codes'],{path:'metadata_or_IO_refused'})


if __name__ == '__main__':
    unittest.main(verbosity=2)
