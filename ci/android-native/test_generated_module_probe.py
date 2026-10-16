"""Pure snapshot tests. Import only: never call the CLI, Go, or a network tool."""
import hashlib
import json
import os
from pathlib import Path
import tempfile
import unittest

import generated_module_probe as probe


class SnapshotTests(unittest.TestCase):
    def test_exact_raw_and_missing_are_distinct(self):
        with tempfile.TemporaryDirectory() as d:
            work = Path(d)
            source = work / 'module'
            source.mkdir()
            body = b'module gobind\n\ngo 1.27\n'
            (source / 'go.mod').write_bytes(body)
            rows = probe.snapshot(work, source, 'raw-before')
            self.assertEqual((work / 'evidence/locks/raw-before/go.mod').read_bytes(), body)
            self.assertEqual(rows['go.mod']['sha256'], hashlib.sha256(body).hexdigest())
            self.assertEqual(rows['go.sum'], {'exists': False})
            self.assertFalse((work / 'evidence/locks/raw-before/go.sum').exists())
            saved = json.loads((work / 'evidence/locks/raw-before/snapshot.json').read_text())
            self.assertEqual(saved['files'], rows)

    def test_changed_snapshot_preserves_prior_raw_and_refuses_overwrite(self):
        with tempfile.TemporaryDirectory() as d:
            work = Path(d)
            source = work / 'module'
            source.mkdir()
            (source / 'go.mod').write_bytes(b'module gobind\n')
            first = b'example.test/a v1.0.0 h1:synthetic\n'
            (source / 'go.sum').write_bytes(first)
            before = probe.snapshot(work, source, 'before')
            (source / 'go.sum').write_bytes(first + b'example.test/b v1.0.0 h1:synthetic\n')
            after = probe.snapshot(work, source, 'after')
            self.assertNotEqual(before['go.sum'], after['go.sum'])
            self.assertEqual((work / 'evidence/locks/before/go.sum').read_bytes(), first)
            with self.assertRaises(FileExistsError):
                probe.snapshot(work, source, 'before')

    def test_link_and_escaping_label_rejected(self):
        with tempfile.TemporaryDirectory() as d:
            work = Path(d)
            source = work / 'module'
            source.mkdir()
            target = work / 'target'
            target.write_bytes(b'module gobind\n')
            (source / 'go.mod').symlink_to(target)
            with self.assertRaisesRegex(ValueError, 'single-link'):
                probe.snapshot(work, source, 'symlink')
            (source / 'go.mod').unlink()
            os.link(target, source / 'go.mod')
            with self.assertRaisesRegex(ValueError, 'single-link'):
                probe.snapshot(work, source, 'hardlink')
            with self.assertRaisesRegex(ValueError, 'unsafe relative'):
                probe.snapshot(work, source, '../escape')


if __name__ == '__main__':
    unittest.main()
