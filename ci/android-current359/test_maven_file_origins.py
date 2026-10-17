"""Synthetic cache/actual-report controls; no Gradle, network, or native tools."""
import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

import maven_file_origins as origins


class PublishedFileOrigins(unittest.TestCase):
    def fixture(self, work, variant, kmp=False):
        group, module, version = 'androidx.compose.runtime', 'runtime-annotation-android', '1.9.0'
        coordinate = ':'.join((group, module, version))
        cache = work/'gradle-cache/caches/modules-2/files-2.1'
        evidence = work/'evidence'; evidence.mkdir()
        body = b'synthetic immutable AAR bytes'
        digest = hashlib.sha256(body).hexdigest()
        def cached(g, m, v, name, value):
            path = cache/g/m/v/hashlib.sha1(value).hexdigest()/name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(value)
            return path
        target = cached(group, module, version, 'runtime-annotation.aar', body)
        module_url = 'https://dl.google.com/dl/android/maven2/androidx/compose/runtime/runtime-annotation-android/1.9.0/runtime-annotation-android-1.9.0.module'
        artifact_url = module_url[:-len('.module')]+'.aar'
        file = {'name': target.name, 'url': 'runtime-annotation-android-1.9.0.aar',
                'size': len(body), 'sha256': digest, 'sha512': hashlib.sha512(body).hexdigest()}
        child = {'formatVersion': '1.1', 'component': {'group': group, 'module': module, 'version': version},
                 'variants': [{'files': [file]}, {'files': [copy.deepcopy(file)]}]}
        row = {'coordinate': coordinate, 'selected': {'kind': 'module', 'identity': coordinate,
               'group': group, 'module': module, 'version': version},
               'path': str(target.relative_to(work)), 'bytes': len(body), 'sha256': digest}
        downloads = [module_url, artifact_url]
        if variant == 'missing-artifact-download': downloads.remove(artifact_url)
        if variant == 'missing-module-download': downloads.remove(module_url)
        if variant == 'wrong-size':
            for item in child['variants']: item['files'][0]['size'] += 1
        if variant == 'wrong-hash':
            for item in child['variants']: item['files'][0]['sha256'] = '0'*64
        if variant == 'no-strong-hash':
            for item in child['variants']:
                item['files'][0].pop('sha256'); item['files'][0].pop('sha512')
        if variant == 'wrong-resolved-coordinate': row['selected']['module'] = 'foreign'
        if variant == 'wrong-resolved-body': row['sha256'] = '0'*64
        if variant == 'traversal':
            for item in child['variants']: item['files'][0]['url'] = '../foreign.aar'
        if variant == 'external-url':
            for item in child['variants']: item['files'][0]['url'] = 'https://untrusted.invalid/foreign.aar'
        if variant == 'ambiguous-name': child['variants'][1]['files'][0]['url'] = 'different.aar'
        if variant == 'unknown-name':
            for item in child['variants']: item['files'][0]['name'] = 'other.aar'
        if variant == 'unapproved-origin': downloads.append('https://untrusted.invalid/foreign.jar')
        if kmp:
            parent_url = module_url.replace('runtime-annotation-android', 'runtime-annotation')
            child['component'] = {'group': group, 'module': 'runtime-annotation', 'version': version,
                                  'url': '../../runtime-annotation/1.9.0/runtime-annotation-1.9.0.module'}
            available = {'group': group, 'module': module, 'version': version,
                         'url': '../../runtime-annotation-android/1.9.0/runtime-annotation-android-1.9.0.module'}
            parent = {'formatVersion': '1.1', 'component': {'group': group, 'module': 'runtime-annotation', 'version': version},
                      'variants': [{'available-at': available}, {'available-at': copy.deepcopy(available)}]}
            downloads.append(parent_url)
            if variant == 'missing-parent-download': downloads.remove(parent_url)
            if variant == 'external-backlink': child['component']['url'] = 'https://untrusted.invalid/p.module'
            if variant == 'foreign-repository-backlink': child['component']['url'] = '/different/'+parent_url.split('/maven2/', 1)[1]
            if variant == 'parent-identity': parent['component']['module'] = 'foreign'
            if variant == 'no-reciprocal': parent['variants'] = []
            if variant == 'reciprocal-url': parent['variants'][0]['available-at']['url'] = '../../foreign/1.9.0/foreign-1.9.0.module'
            if variant == 'reciprocal-coordinate':
                for item in parent['variants']: item['available-at']['module'] = 'foreign'
            if variant != 'missing-parent-body':
                cached(group, 'runtime-annotation', version, 'runtime-annotation-1.9.0.module', json.dumps(parent).encode())
        raw = json.dumps(child).encode()
        if variant == 'duplicate-key': raw = raw.replace(b'"formatVersion": "1.1"', b'"formatVersion":"1.1","formatVersion":"1.1"')
        metadata = cached(group, module, version, 'runtime-annotation-android-1.9.0.module', raw)
        if variant == 'cache-key': metadata.parent.rename(metadata.parent.with_name('0'*40))
        (evidence/'gradle-resolved-artifacts.json').write_text(json.dumps([row, copy.deepcopy(row)]))
        log = '\n'.join('Downloading '+url+' to owned/cache' for url in downloads)
        return log, artifact_url, coordinate

    def test_published_file_alias_requires_actual_resolved_identity_body_and_downloads(self):
        variants = ['valid', 'missing-artifact-download', 'missing-module-download', 'wrong-size', 'wrong-hash',
                    'no-strong-hash', 'wrong-resolved-coordinate', 'wrong-resolved-body', 'traversal', 'external-url',
                    'ambiguous-name', 'unknown-name', 'unapproved-origin', 'duplicate-key', 'cache-key']
        for variant in variants:
            with self.subTest(variant=variant), tempfile.TemporaryDirectory() as temp:
                work = Path(temp).resolve()
                log, url, coordinate = self.fixture(work, variant)
                if variant != 'valid':
                    with self.assertRaises(ValueError): origins.audit(work, log)
                    continue
                proof = origins.audit(work, log)
                selected = [x for x in proof['artifacts'] if x['filename'] == 'runtime-annotation.aar']
                self.assertEqual(len(selected), 1)
                self.assertEqual(selected[0]['observed_sources'], [url])
                self.assertEqual(selected[0]['published_file_alias']['resolved_coordinate'], coordinate)
                self.assertEqual(len(selected[0]['published_file_alias']['metadata']), 1)
                self.assertTrue(proof['all_actual_artifact_sources_accounted'])

    def test_kmp_alias_requires_reciprocal_downloaded_parent_without_graph_grant(self):
        variants = ['valid', 'missing-parent-download', 'missing-parent-body', 'external-backlink',
                    'foreign-repository-backlink', 'parent-identity', 'no-reciprocal', 'reciprocal-url', 'reciprocal-coordinate']
        for variant in variants:
            with self.subTest(variant=variant), tempfile.TemporaryDirectory() as temp:
                work = Path(temp).resolve()
                log, url, _coordinate = self.fixture(work, variant, kmp=True)
                if variant != 'valid':
                    with self.assertRaises(ValueError): origins.audit(work, log)
                    continue
                proof = origins.audit(work, log)
                selected = next(x for x in proof['artifacts'] if x['filename'] == 'runtime-annotation.aar')
                self.assertEqual(selected['observed_sources'], [url])
                self.assertEqual(len(selected['published_file_alias']['metadata']), 2)
                self.assertTrue(selected['published_file_alias']['KMP_relation_grants_no_new_component_or_repository'])


if __name__ == '__main__':
    unittest.main(verbosity=2)
