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


class ProjectionTransitionTests(unittest.TestCase):
    # Both templates are independent raw r1 artifact bytes with only the two
    # exact job-owned replace paths normalized. Never synthesize the expected
    # after-body using the implementation's REQUIRE_UPDATES table.
    def transition(self, prefix='/job-one/current257'):
        directory = Path(__file__).resolve().parent
        before = (directory / 'projected-module-before.mod').read_bytes()
        after = (directory / 'projected-module-after.mod').read_bytes()
        self.assertEqual(hashlib.sha256(before).hexdigest(),
                         '05e30d386898edc45e01458e5dcd79d166965d89cb4537a436ed5beeaba21088')
        self.assertEqual(hashlib.sha256(after).hexdigest(),
                         'f68c72acfd31c65d734712c229af867f8d2b8d06ab2ffae631dbf26a59223841')
        core, protocol = Path(prefix) / 'core-prefetch', Path(prefix) / 'source/server/pkg/protocol'
        replacements = [(b'/__LUOBO_JOB_CORE__', str(core).encode()),
                        (b'/__LUOBO_JOB_PROTOCOL__', str(protocol).encode())]
        for placeholder, actual in replacements:
            before, after = before.replace(placeholder, actual), after.replace(placeholder, actual)
        return before, after, core, protocol

    def test_two_fresh_paths_allow_exact_observed_eight_changes(self):
        for prefix in ['/job-one/current257', '/different/private-job/current257']:
            with self.subTest(prefix=prefix):
                before, after, core, protocol = self.transition(prefix)
                result = probe.validate_projected_acquisition(before, after, core, protocol)
                self.assertEqual(result['require_count'], 97)
                self.assertEqual(len(result['only_allowed_require_changes']), 8)
                self.assertFalse(result['full_bind_lock'])

    def test_unknown_ninth_or_partial_transition_rejected(self):
        before, after, core, protocol = self.transition()
        for name, mutation in [
            ('ninth', after.replace(b'google.golang.org/protobuf v1.31.0',
                                   b'google.golang.org/protobuf v1.32.0')),
            ('missing-one', after.replace(b'cloud.google.com/go/compute v1.20.1',
                                         b'cloud.google.com/go/compute v1.19.1')),
        ]:
            with self.subTest(name=name):
                self.assertNotEqual(mutation, after)
                with self.assertRaisesRegex(ValueError, 'outside exact eight'):
                    probe.validate_projected_acquisition(before, mutation, core, protocol)

    def test_original_version_mismatch_rejected(self):
        before, after, core, protocol = self.transition()
        changed = before.replace(b'cloud.google.com/go/compute v1.19.1',
                                 b'cloud.google.com/go/compute v1.18.0')
        self.assertNotEqual(changed, before)
        with self.assertRaisesRegex(ValueError, 'original97-require'):
            probe.validate_projected_acquisition(changed, after, core, protocol)

    def test_acquired_paths_directive_and_other_bytes_immutable(self):
        before, after, core, protocol = self.transition()
        for name, mutation in [
            ('path', after.replace(str(protocol).encode(), b'/different-job/protocol')),
            ('go-directive', after.replace(b'go 1.27\n', b'go 1.28\n')),
            ('other-byte', after + b'\n'),
        ]:
            with self.subTest(name=name):
                self.assertNotEqual(mutation, after)
                with self.assertRaisesRegex(ValueError, 'outside exact eight'):
                    probe.validate_projected_acquisition(before, mutation, core, protocol)

    def test_original_require_set_and_path_must_match_reviewed_projection(self):
        before, after, core, protocol = self.transition()
        for name, mutation in [
            ('missing', before.replace(b'\tgoogle.golang.org/protobuf v1.31.0\n', b'')),
            ('extra', before.replace(b'require (\n', b'require (\n\texample.test/unknown v1.0.0\n')),
            ('wrong-path', before.replace(str(core).encode(), b'/different-job/core')),
        ]:
            with self.subTest(name=name):
                self.assertNotEqual(mutation, before)
                with self.assertRaises(ValueError):
                    probe.validate_projected_acquisition(mutation, after, core, protocol)


if __name__ == '__main__':
    unittest.main()
