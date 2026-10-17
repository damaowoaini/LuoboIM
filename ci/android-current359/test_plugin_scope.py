"""Only new producer/report controls; never call setup/download/Gradle/Go."""
import copy
import hashlib
import io
import json
import os
from pathlib import Path
import sys
import tarfile
import tempfile
import unittest

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
sys.path.insert(1, str(HERE.parent / 'android-native'))
import plugin_scope as subject


def archive(path, entries):
    with tarfile.open(path, 'w:gz') as out:
        for name, body, kind in entries:
            item = tarfile.TarInfo(name); item.mode = 0o644
            if kind == 'link':
                item.type = tarfile.SYMTYPE; item.linkname = 'pubspec.yaml'
                out.addfile(item)
            else:
                item.size = len(body); out.addfile(item, io.BytesIO(body))
    return hashlib.sha256(path.read_bytes()).hexdigest()


class PluginScopeTests(unittest.TestCase):
    def test_fixed535_and21pub1SDK_contract_rejects_source_lock_and_library_drift(self):
        inputs = json.loads((HERE / 'inputs.json').read_bytes())
        data = subject.contract(HERE, inputs)
        self.assertEqual((len(data['projects']), sum(p['kind'] == 'local-source535' for p in data['projects'])), (22, 1))
        for key, value in [('members', 530), ('source_digest', '0' * 64)]:
            altered = copy.deepcopy(inputs); altered[key] = value
            with self.subTest(key=key), self.assertRaises(ValueError): subject.contract(HERE, altered)
        altered = copy.deepcopy(inputs)
        next(r for r in altered['files'] if r['path'] == 'app/pubspec.lock')['sha256'] = '0' * 64
        with self.assertRaises(ValueError): subject.contract(HERE, altered)
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder).resolve()
            names = ['plugin-scope-inputs.json', 'plugin-rich-consumer.gradle', 'rich-catalog.json', 'rich-consumer.gradle']
            for name in names: (root / name).write_bytes((HERE / name).read_bytes())
            self.assertEqual(subject.contract(root, inputs), data)
            (root / 'plugin-rich-consumer.gradle').write_bytes(b'// changed source\n')
            with self.assertRaises(ValueError): subject.contract(root, inputs)

    def test_archive_inventory_uses_same_fd_exact_checksum_and_refuses_unsafe_or_duplicate_members(self):
        limits = dict(each=65536, sum=65536, decoded_sum=65536, file_each=32768, files_sum=32)
        entries = [('pubspec.yaml', b'name: controlled\n', 'file'),
                   ('android/build.gradle', b'// source\n', 'file')]
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder).resolve(); p = root / 'source.tar.gz'
            good = archive(p, entries)
            rows = subject.archive_rows(p, limits, good)
            self.assertEqual([r['path'] for r in rows], ['android/build.gradle', 'pubspec.yaml'])
            with self.assertRaises(ValueError): subject.archive_rows(p, limits, '0' * 64)
            for label, extra in [('duplicate', entries[:1]), ('traversal', [('../outside', b'x', 'file')]),
                                 ('symlink', [('android/link', b'', 'link')]), ('absolute', [('/outside', b'x', 'file')])]:
                p.unlink(); pin = archive(p, entries + extra)
                with self.subTest(label=label), self.assertRaises(ValueError): subject.archive_rows(p, limits, pin)
            p.unlink(); archive(p, entries)
            link = root / 'alias.tar.gz'; link.symlink_to(p)
            with self.assertRaises(ValueError): subject.archive_rows(link, limits, hashlib.sha256(p.read_bytes()).hexdigest())

    def test_installed_tree_requires_exact_independent_bytes_paths_single_links_and_ownership_shape(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder).resolve(); body = b'known independent public source\n'
            p = root / 'pubspec.yaml'; p.write_bytes(body)
            rows = [{'path': p.name, 'bytes': len(body), 'sha256': hashlib.sha256(body).hexdigest()}]
            subject.check_tree(root, rows)
            p.write_bytes(b'changed')
            with self.assertRaises(ValueError): subject.check_tree(root, rows)
            p.write_bytes(body); extra = root / 'extra'; extra.write_bytes(b'x')
            with self.assertRaises(ValueError): subject.check_tree(root, rows)
            extra.unlink(); os.link(p, extra)
            with self.assertRaises(ValueError): subject.file_row(p)
            extra.unlink(); p.unlink(); p.symlink_to(root.parent / 'outside')
            with self.assertRaises((ValueError, FileNotFoundError)): subject.check_tree(root, rows)

    def test_plugin_report_keeps_exact_original14_edges_variants_and_bound_origin_without_App_projection(self):
        catalog, _, _ = subject.rich.approved(HERE)
        config = catalog['configurations'][0]
        entry = {'project': ':audio_session', 'origin': {'kind': 'locked-official-pub-archive', 'sha256': '1' * 64},
                 'gate_sources': {'pub-cache/controlled/pubspec.yaml': '2' * 64}}
        value = {'catalog_sha256': subject.rich.CATALOG_SHA, 'project': entry['project'],
                 'configuration': config['name'], 'scope_sources': entry['gate_sources'],
                 'no_force_or_substitution': True, 'edges': copy.deepcopy(config['edges']),
                 'artifacts': [{k: v for k, v in a.items() if k != 'origin'} for a in config['artifacts']],
                 'plugin_seal_sha256': '3' * 64, 'plugin_origin': entry['origin']}
        self.assertEqual(subject.validate_report(catalog, entry, '3' * 64, value)['name'], config['name'])
        for label in ['project', 'seal', 'origin', 'source', 'force', 'fourth-edge', 'parent', 'variant', 'body']:
            bad = copy.deepcopy(value)
            if label == 'project': bad['project'] = ':other'
            elif label == 'seal': bad['plugin_seal_sha256'] = '0' * 64
            elif label == 'origin': bad['plugin_origin']['sha256'] = '0' * 64
            elif label == 'source': bad['scope_sources'] = {}
            elif label == 'force': bad['no_force_or_substitution'] = False
            elif label == 'fourth-edge': bad['edges'].append(copy.deepcopy(bad['edges'][0]))
            elif label == 'parent': bad['edges'][0]['from'] = {'kind': 'foreign-parent'}
            elif label == 'variant': bad['artifacts'][0]['variant']['attributes'] = {}
            else: bad['artifacts'][0]['sha256'] = '0' * 64
            with self.subTest(label=label), self.assertRaises(ValueError): subject.validate_report(catalog, entry, '3' * 64, bad)
        with self.assertRaises(ValueError): subject.rich.validate_report(catalog, value)


if __name__ == '__main__':
    unittest.main(verbosity=2)
