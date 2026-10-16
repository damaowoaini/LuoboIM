"""Synthetic snapshot guards only; never invoke the probe/main/Go/network."""
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

import go_lock_probe as probe


class Snapshots(unittest.TestCase):
    def work(self, temporary):
        root = Path(temporary).resolve()
        (root / 'core-prefetch').mkdir()
        (root / 'evidence').mkdir()
        (root / 'core-prefetch/go.mod').write_bytes(b'module synthetic.invalid/module\n')
        (root / 'core-prefetch/go.sum').write_bytes(b'synthetic before\n')
        return root

    def test_retains_exact_before_and_after_without_overwriting_inputs(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = self.work(temporary)
            before = probe.snapshot(root, 'before')
            body = b'synthetic before\nsynthetic added\n'
            (root / 'core-prefetch/go.sum').write_bytes(body)
            after = probe.snapshot(root, 'after')
            self.assertEqual((root / 'evidence/lock-before/go.sum').read_bytes(), b'synthetic before\n')
            self.assertEqual((root / 'evidence/lock-after/go.sum').read_bytes(), body)
            self.assertEqual(after[1]['sha256'], hashlib.sha256(body).hexdigest())
            self.assertNotEqual(before[1]['sha256'], after[1]['sha256'])
            self.assertEqual(json.loads((root / 'evidence/lock-after/snapshot.json').read_text())['files'], after)

    def test_refuses_reusing_an_existing_capture(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = self.work(temporary)
            probe.snapshot(root, 'same')
            original = (root / 'evidence/lock-same/snapshot.json').read_bytes()
            with self.assertRaises(FileExistsError):
                probe.snapshot(root, 'same')
            self.assertEqual((root / 'evidence/lock-same/snapshot.json').read_bytes(), original)

    def test_refuses_linked_lock_without_touching_external_file(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = self.work(temporary)
            outside = root / 'outside'; outside.write_bytes(b'unchanged')
            lock = root / 'core-prefetch/go.sum'; lock.unlink(); lock.symlink_to(outside)
            with self.assertRaisesRegex(ValueError, 'ordinary core lock'):
                probe.snapshot(root, 'linked')
            self.assertEqual(outside.read_bytes(), b'unchanged')


if __name__ == '__main__':
    unittest.main()
