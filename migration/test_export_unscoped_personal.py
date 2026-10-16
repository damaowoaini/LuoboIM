import hashlib
import importlib.util
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('legacy_export', Path(__file__).with_name('export_unscoped_personal.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class ReadOnlyRecoveryTest(unittest.TestCase):
    def test_large_exports_split_without_dropping_or_duplicating_records(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / ('b' * 64 + '.sqlite3')
            connection = sqlite3.connect(source)
            connection.execute('CREATE TABLE personal_items (kind TEXT,id TEXT,payload TEXT)')
            connection.executemany('INSERT INTO personal_items VALUES (?,?,?)',
                                   [('note', str(i), json.dumps({'id': str(i), 'title': '保留资料'})) for i in range(10001)])
            connection.commit()
            connection.close()
            folder, manifest = module.export_database(source, root)
            self.assertEqual(len(manifest['parts']), 2)
            identifiers = []
            for part in manifest['parts']:
                raw = (folder / part['file']).read_bytes()
                self.assertLess(len(raw), 16 * 1024 * 1024)
                identifiers.extend(item['id'] for item in json.loads(raw)['notes'])
            self.assertEqual(len(identifiers), 10001)
            self.assertEqual(len(set(identifiers)), 10001)

    def test_export_retains_every_record_and_original_bytes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / ('a' * 64 + '.sqlite3')
            connection = sqlite3.connect(source)
            connection.execute('CREATE TABLE personal_items (kind TEXT,id TEXT,payload TEXT)')
            for kind, identifier in [('note', 'note1'), ('todo', 'todo1')]:
                connection.execute('INSERT INTO personal_items VALUES (?,?,?)',
                                   (kind, identifier, json.dumps({'id': identifier, 'title': '私有测试资料'})))
            connection.commit()
            connection.close()
            before = hashlib.sha256(source.read_bytes()).hexdigest()
            folder, manifest = module.export_database(source, root)
            self.assertEqual(len(manifest['parts']), 1)
            backup = json.loads((folder / manifest['parts'][0]['file']).read_text())
            self.assertEqual(backup['notes'][0]['id'], 'note1')
            self.assertEqual(backup['todos'][0]['id'], 'todo1')
            self.assertEqual(backup['owner'], 'a' * 64)
            self.assertEqual(hashlib.sha256(source.read_bytes()).hexdigest(), before)
            self.assertEqual((folder.stat().st_mode & 0o777), 0o700)
            self.assertEqual(((folder / manifest['parts'][0]['file']).stat().st_mode & 0o777), 0o600)


if __name__ == '__main__':
    unittest.main()
