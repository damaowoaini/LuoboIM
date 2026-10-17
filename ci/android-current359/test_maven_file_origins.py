"""Synthetic cache/actual-report controls; no Gradle, network, or native tools."""
import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock
import xml.etree.ElementTree as ET

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
        if variant == 'opaque-cache-keys':
            metadata.parent.rename(metadata.parent.with_name('opaque-module-cache-key'))
            destination = target.parent.with_name('opaque-artifact-cache-key')
            target.parent.rename(destination)
            target = destination/target.name
            row['path'] = str(target.relative_to(work))
        (evidence/'gradle-resolved-artifacts.json').write_text(json.dumps([row, copy.deepcopy(row)]))
        log = '\n'.join('Downloading '+url+' to owned/cache' for url in downloads)
        return log, artifact_url, coordinate

    def test_published_file_alias_requires_actual_resolved_identity_body_and_downloads(self):
        variants = ['valid', 'missing-artifact-download', 'missing-module-download', 'wrong-size', 'wrong-hash',
                    'no-strong-hash', 'wrong-resolved-coordinate', 'wrong-resolved-body', 'traversal', 'external-url',
                    'ambiguous-name', 'unknown-name', 'unapproved-origin', 'duplicate-key', 'opaque-cache-keys']
        for variant in variants:
            with self.subTest(variant=variant), tempfile.TemporaryDirectory() as temp:
                work = Path(temp).resolve()
                log, url, coordinate = self.fixture(work, variant)
                if variant not in ('valid', 'opaque-cache-keys'):
                    with self.assertRaises(ValueError): origins.audit(work, log)
                    continue
                proof = origins.audit(work, log)
                selected = [x for x in proof['artifacts'] if x['filename'] == 'runtime-annotation.aar']
                self.assertEqual(len(selected), 1)
                self.assertEqual(selected[0]['observed_sources'], [url])
                self.assertEqual(selected[0]['published_file_alias']['resolved_coordinate'], coordinate)
                self.assertEqual(len(selected[0]['published_file_alias']['metadata']), 1)
                metadata_proof = selected[0]['published_file_alias']['metadata'][0]
                actual_metadata = work/metadata_proof['path']
                self.assertEqual(metadata_proof['sha256'], hashlib.sha256(actual_metadata.read_bytes()).hexdigest())
                self.assertTrue(metadata_proof['cacheKeyIsOpaque'])
                if variant == 'opaque-cache-keys':
                    self.assertEqual(metadata_proof['cacheKey'], 'opaque-module-cache-key')
                    self.assertEqual(selected[0]['published_file_alias']['target_cache_key'], 'opaque-artifact-cache-key')
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

    def test_alias_refusal_records_public_stable_metadata_without_granting_identity(self):
        for case in ['missing-selected-path', 'wrong-selected-coordinate']:
            with self.subTest(case=case), tempfile.TemporaryDirectory() as temp:
                work = Path(temp).resolve()
                log, _url, coordinate = self.fixture(work, 'opaque-cache-keys')
                report = work/'evidence/gradle-resolved-artifacts.json'
                rows = json.loads(report.read_text()); target = work/rows[0]['path']
                actual_body = target.read_bytes()
                if case == 'missing-selected-path':
                    rows = []; expected_count = 0; message = 'file alias lacks actual resolved component'
                else:
                    for row in rows: row['coordinate'] = 'foreign:coordinate:1'
                    expected_count = 2; message = 'file alias resolved identity or body differs'
                report.write_text(json.dumps(rows))
                with self.assertRaisesRegex(ValueError, message):
                    origins.audit(work, log)
                path = work/'evidence/maven-file-alias-refusal.json'
                proof = json.loads(path.read_text())
                self.assertTrue(proof['originalGateStillRefused'])
                self.assertTrue(proof['metadataAvailable'])
                self.assertTrue(proof['stableFD'])
                self.assertTrue(proof['auditObservedBodyStillMatches'])
                self.assertEqual(proof['observedCoordinate'], coordinate)
                self.assertEqual(proof['publicRelativePath'], str(target.relative_to(work)))
                self.assertEqual(proof['cacheKey'], 'opaque-artifact-cache-key')
                self.assertEqual(proof['filename'], target.name)
                self.assertEqual(proof['matchedResolvedRows'], expected_count)
                self.assertEqual(proof['bytes'], len(actual_body))
                self.assertEqual(proof['sha256'], hashlib.sha256(actual_body).hexdigest())
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
                self.assertNotIn(str(work), path.read_text())
                self.assertNotIn(actual_body.decode(), path.read_text())
                self.assertFalse(proof['sourceContentsAbsolutePathsOrURLsRecorded'])

    def test_generated_acquisition_identity_binds_variant_bytes_without_production_scope(self):
        source = Path(__file__).with_name('run.py').read_text()
        self.assertLess(source.index("s.put(self.w/'evidence/gradle-verification-metadata.xml'"),
                        source.index('proof=maven_file_origins.audit'))
        cases = ['valid', 'missing-XML', 'wrong-artifact-SHA', 'wrong-module-SHA', 'duplicate-component',
                 'DOCTYPE', 'foreign-URL', 'cross-module-URL', 'missing-download',
                 'foreign-consumed-row', 'foreign-XML-component', 'weak-only']
        for case in cases:
            with self.subTest(case=case), tempfile.TemporaryDirectory() as temp:
                work = Path(temp).resolve(); log, old_url, coordinate = self.fixture(work, 'opaque-cache-keys')
                report = work/'evidence/gradle-resolved-artifacts.json'
                original_rows = json.loads(report.read_text()); target = work/original_rows[0]['path']
                metadata = next((work/'gradle-cache/caches/modules-2/files-2.1').rglob('*.module'))
                data = json.loads(metadata.read_text())
                relative = '../1.9.0-android/runtime-annotation.aar'
                if case == 'foreign-URL': relative = 'https://foreign.invalid/runtime-annotation.aar'
                if case == 'cross-module-URL': relative = '../../foreign-module/1.9.0/runtime-annotation.aar'
                for variant in data['variants']: variant['files'][0]['url'] = relative
                metadata.write_text(json.dumps(data))
                actual_url = old_url.replace('/1.9.0/', '/1.9.0-android/').rsplit('/', 1)[0]+'/runtime-annotation.aar'
                log = log.replace(old_url, actual_url)
                if case == 'missing-download': log = '\n'.join(line for line in log.splitlines() if actual_url not in line)
                report.write_text('[]')
                if case == 'foreign-consumed-row':
                    for row in original_rows: row['coordinate'] = 'foreign:component:1'
                    report.write_text(json.dumps(original_rows))
                ns = 'https://schema.gradle.org/dependency-verification'
                ET.register_namespace('', ns)
                root = ET.Element('{'+ns+'}verification-metadata')
                components = ET.SubElement(root, '{'+ns+'}components')
                group, module, version = coordinate.split(':')
                component = ET.SubElement(components, '{'+ns+'}component', group=group, name=module,
                                          version='foreign' if case == 'foreign-XML-component' else version)
                for path in [target, metadata]:
                    entry = ET.SubElement(component, '{'+ns+'}artifact', name=path.name)
                    digest = hashlib.sha256(path.read_bytes()).hexdigest()
                    if (path == target and case == 'wrong-artifact-SHA') or (path == metadata and case == 'wrong-module-SHA'):
                        digest = '0'*64
                    if case == 'weak-only': ET.SubElement(entry, '{'+ns+'}sha1', value='0'*40)
                    else: ET.SubElement(entry, '{'+ns+'}sha256', value=digest, origin='Generated by Gradle')
                if case == 'duplicate-component': components.append(copy.deepcopy(component))
                xml = ET.tostring(root, encoding='utf-8')
                if case == 'DOCTYPE': xml = b'<!DOCTYPE verification-metadata []>'+xml
                if case != 'missing-XML': (work/'evidence/gradle-verification-metadata.xml').write_bytes(xml)
                if case != 'valid':
                    with self.assertRaises(ValueError): origins.audit(work, log)
                    continue
                result = origins.audit(work, log)
                selected = next(row for row in result['artifacts'] if row['filename'] == target.name)
                proof = selected['published_file_alias']
                self.assertEqual(selected['observed_sources'], [actual_url])
                self.assertTrue(proof['acquisition_identity']['acquisition_only'])
                self.assertFalse(proof['acquisition_identity']['grants_production_component_or_configuration_scope'])
                self.assertTrue(proof['not_a_production_consumed_component_grant'])
                self.assertEqual(proof['resolved_report_matching_rows'], 0)
                self.assertEqual(json.loads(report.read_text()), [])
                captured = work/proof['metadata'][0]['captured_public_module']
                self.assertEqual(captured.read_bytes(), metadata.read_bytes())
                self.assertEqual(proof['metadata'][0]['acquisition_identity']['verification_metadata_sha256'],
                                 hashlib.sha256(xml).hexdigest())

    def test_acquisition_optional_publisher_fields_keep_XML_and_actual_FD_identity(self):
        cases = ['absent-both', 'valid-size-only', 'valid-hash-only', 'valid-weak-hashes', 'valid-all',
                 'wrong-size', 'null-size', 'wrong-hash', 'null-hash', 'wrong-weak-hash',
                 'wrong-XML-target', 'missing-XML', 'production-fields-absent', 'target-drift-after-XML']
        valid = {'absent-both', 'valid-size-only', 'valid-hash-only', 'valid-weak-hashes', 'valid-all'}
        for case in cases:
            with self.subTest(case=case), tempfile.TemporaryDirectory() as temp:
                work = Path(temp).resolve(); log, old_url, coordinate = self.fixture(work, 'opaque-cache-keys')
                report = work/'evidence/gradle-resolved-artifacts.json'
                consumed = json.loads(report.read_text()); target = work/consumed[0]['path']
                body = target.read_bytes(); digest = hashlib.sha256(body).hexdigest()
                metadata = next((work/'gradle-cache/caches/modules-2/files-2.1').rglob('*.module'))
                data = json.loads(metadata.read_text())
                entry = {'name': target.name, 'url': '../1.9.0-android/runtime-annotation.aar'}
                if case in {'valid-size-only', 'valid-all'}: entry['size'] = len(body)
                if case in {'valid-hash-only', 'valid-all'}: entry['sha512'] = hashlib.sha512(body).hexdigest()
                if case in {'valid-weak-hashes', 'valid-all'}:
                    entry.update(sha1=hashlib.sha1(body).hexdigest(), md5=hashlib.md5(body).hexdigest())
                if case == 'wrong-size': entry['size'] = len(body)+1
                if case == 'null-size': entry['size'] = None
                if case == 'wrong-hash': entry['sha256'] = '0'*64
                if case == 'null-hash': entry['sha512'] = None
                if case == 'wrong-weak-hash': entry['sha1'] = '0'*40
                for variant in data['variants']: variant['files'] = [copy.deepcopy(entry)]
                metadata.write_text(json.dumps(data))
                actual_url = old_url.replace('/1.9.0/', '/1.9.0-android/').rsplit('/', 1)[0]+'/runtime-annotation.aar'
                log = log.replace(old_url, actual_url)
                report.write_text(json.dumps(consumed) if case == 'production-fields-absent' else '[]')
                ns = 'https://schema.gradle.org/dependency-verification'
                root = ET.Element('{'+ns+'}verification-metadata')
                components = ET.SubElement(root, '{'+ns+'}components')
                group, module, version = coordinate.split(':')
                component = ET.SubElement(components, '{'+ns+'}component', group=group, name=module, version=version)
                for path in [target, metadata]:
                    artifact = ET.SubElement(component, '{'+ns+'}artifact', name=path.name)
                    value = '0'*64 if path == target and case == 'wrong-XML-target' else hashlib.sha256(path.read_bytes()).hexdigest()
                    ET.SubElement(artifact, '{'+ns+'}sha256', value=value, origin='Generated by Gradle')
                xml = ET.tostring(root, encoding='utf-8')
                if case != 'missing-XML': (work/'evidence/gradle-verification-metadata.xml').write_bytes(xml)
                if case == 'target-drift-after-XML':
                    original = origins.PublishedFiles.module
                    def loaded(instance, *args, **kwargs):
                        result = original(instance, *args, **kwargs)
                        target.write_bytes(b'x'*len(body))
                        return result
                    with mock.patch.object(origins.PublishedFiles, 'module', loaded):
                        with self.assertRaises(ValueError): origins.audit(work, log)
                    continue
                if case not in valid:
                    with self.assertRaises(ValueError): origins.audit(work, log)
                    continue
                result = origins.audit(work, log)
                selected = next(r for r in result['artifacts'] if r['filename'] == target.name)
                proof = selected['published_file_alias']
                self.assertEqual(selected['observed_sources'], [actual_url])
                self.assertTrue(proof['acquisition_identity']['acquisition_only'])
                self.assertFalse(proof['acquisition_identity']['grants_production_component_or_configuration_scope'])
                self.assertTrue(proof['not_a_production_consumed_component_grant'])
                self.assertNotIn('resolved_coordinate', proof)
                self.assertEqual(proof['resolved_report_matching_rows'], 0)
                self.assertEqual(proof['acquired_target']['bytes'], len(body))
                self.assertEqual(proof['acquired_target']['sha256'], digest)
                self.assertTrue(proof['acquired_target']['stableFD'])
                self.assertFalse(proof['acquired_target']['publisher_fields_synthesized'])
                self.assertEqual(proof['publisher_size_present'], 'size' in entry)
                self.assertEqual(proof['publisher_hashes_present_and_checked'], sorted(k for k in entry if k in {'md5', 'sha1', 'sha256', 'sha512'}))
                self.assertEqual(proof['metadata'][0]['acquisition_identity']['verification_metadata_sha256'], hashlib.sha256(xml).hexdigest())


if __name__ == '__main__':
    unittest.main(verbosity=2)
