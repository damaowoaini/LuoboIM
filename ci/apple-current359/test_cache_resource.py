"""Actual guard and real filesystem controls; all child process creation mocked."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import stat
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import cache_resource
import xcrun_adapter


def resource_guard():
    path = Path(__file__).resolve().parents[1]/'apple-sdk/resource_dual.py'
    spec = importlib.util.spec_from_file_location('independent_unchanged_guard', path)
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


class CacheResourceControls(unittest.TestCase):
    def test_actual_guard_ignores_unrelated_shared_temp_and_retains_the_original_caps(self):
        for variant in ['valid', 'cache-cap', 'private-cache-cap', 'primary-cap', 'free-disk']:
            with self.subTest(variant=variant), tempfile.TemporaryDirectory() as tmp:
                parent = Path(tmp).resolve()
                root = parent/'T'; root.mkdir(mode=0o700)
                (root/'unrelated-runner-cache').mkdir()
                (root/'unrelated-runner-cache'/'existing').write_bytes(b'never scan this body')
                old = root/'xcrun_db-OldABC12'; old.write_bytes(b'preexisting cache remains intact')
                new = root/'xcrun_db-NewABC12'; new.write_bytes(b'new exact permitted leaf')
                work = parent/'owned'; (work/'evidence').mkdir(parents=True)
                private = work/'xcode/ToolCache'; private.mkdir(mode=0o700, parents=True)
                private_cache = private/'xcrun_db'; private_cache.write_bytes(b'job private cache'); private_cache.chmod(0o600)
                row = {'canonical': str(root), 'uid': os.getuid(),
                       'directory_identity': xcrun_adapter.cache_directory_identity(root, os.getuid()),
                       'protected_preexisting': [old.name]}
                adapter = SimpleNamespace(cache_boundary=lambda p: copy.deepcopy(row),
                    private_cache_path=xcrun_adapter.private_cache_path,
                    cache_directory_identity=xcrun_adapter.cache_directory_identity)
                guard = resource_guard()
                original = guard.allocated

                def before(root_arg):
                    # R16's actual failure was181145600 unrelated shared bytes.
                    if root_arg is not None and Path(root_arg) == root:
                        return 181145600
                    return 65*1048576 if variant == 'primary-cap' else original(root_arg)

                guard.allocated = before
                arguments = SimpleNamespace(command=['never-executed-native-tool'], cwd=work,
                    log=work/'child.log', record=work/'resource.json', min_free_mib=1024,
                    allocated_root=work, max_allocated_mib=64, secondary_root=root,
                    secondary_cap_mib=64, timeout=1, poll_interval=.2)
                child = SimpleNamespace(pid=99999999, poll=lambda: 0, wait=lambda: 0)
                measurement = cache_resource.allocated_cache_leaves

                def measured(*args):
                    if variant == 'cache-cap' or (variant == 'private-cache-cap' and args[0]['canonical'] == str(private)):
                        return 65*1048576
                    return measurement(*args)

                disk = SimpleNamespace(f_bavail=512, f_frsize=1024*1024)
                actual_statvfs = guard.os.statvfs
                with patch.object(guard.subprocess, 'Popen', return_value=child) as start, \
                        patch.object(guard.os, 'killpg', side_effect=ProcessLookupError), \
                        patch.object(guard.os, 'statvfs', side_effect=lambda p: disk if variant == 'free-disk' else actual_statvfs(p)), \
                        patch.object(cache_resource, 'allocated_cache_leaves', side_effect=measured):
                    # The original whole-root monitor demonstrably refuses
                    # BEFORE a child. The wrapper must preserve every other gate.
                    self.assertEqual(guard.run(arguments), 75)
                    start.assert_not_called()
                    code = cache_resource.run(guard, adapter, work, arguments)
                self.assertIs(guard.allocated, before)
                receipt = json.loads(arguments.record.read_text())
                if variant == 'valid':
                    self.assertEqual(code, 0); start.assert_called_once()
                    self.assertEqual(receipt['status'], 'passed')
                    self.assertEqual(receipt['maximum_observed_secondary_allocated_bytes'], (new.stat().st_blocks+private_cache.stat().st_blocks)*512)
                else:
                    self.assertEqual(code, 75); start.assert_not_called()
                    self.assertTrue(receipt['status'].startswith('not_started_'))
                self.assertEqual(old.read_bytes(), b'preexisting cache remains intact')
                self.assertEqual((root/'unrelated-runner-cache'/'existing').read_bytes(), b'never scan this body')
                scope = json.loads((work/'evidence/cache-resource-scope.json').read_text())
                self.assertEqual(scope['secondary_cap_bytes'], 64*1048576)
                self.assertTrue(scope['private_cache_counted_under_same_secondary_cap'])
                self.assertEqual(scope['private_cache_root'], str(private))
                self.assertFalse(scope['files_deleted'])
                self.assertFalse(scope['process_exclusive_cache_ownership_claimed'])

    def test_exact_leaf_measurement_rejects_aliases_and_identity_changes_without_reading_bodies(self):
        for filename in ['xcrun_db-Ab12Cd34', 'xcrun_db']:
            for variant in ['valid', 'symlink', 'hardlink', 'directory', 'identity', 'protected']:
                with self.subTest(filename=filename, variant=variant), tempfile.TemporaryDirectory() as tmp:
                    root = Path(tmp).resolve()/'T'; root.mkdir(mode=0o700)
                    uid = os.getuid()
                    row = {'canonical': str(root), 'uid': uid,
                           'directory_identity': xcrun_adapter.cache_directory_identity(root, uid),
                           'protected_preexisting': []}
                    leaf = root/filename
                    if variant in ['valid', 'identity', 'protected']:
                        leaf.write_bytes(b'small actual allocated cache')
                    elif variant in ['symlink', 'hardlink']:
                        outside = root.parent/'foreign'; outside.write_bytes(b'not cache')
                        if variant == 'symlink': leaf.symlink_to(outside)
                        else: os.link(outside, leaf)
                    else: leaf.mkdir()
                    if variant == 'identity': row['directory_identity'][1] += 1
                    if variant == 'protected': row['protected_preexisting'] = [leaf.name]
                    with patch.object(Path, 'read_bytes', side_effect=AssertionError('measurement read a body')):
                        if variant == 'protected':
                            self.assertEqual(cache_resource.allocated_cache_leaves(row, xcrun_adapter.cache_directory_identity), 0)
                        elif variant == 'valid':
                            self.assertEqual(cache_resource.allocated_cache_leaves(row, xcrun_adapter.cache_directory_identity), leaf.stat().st_blocks*512)
                        else:
                            with self.assertRaises(ValueError):
                                cache_resource.allocated_cache_leaves(row, xcrun_adapter.cache_directory_identity)


if __name__ == '__main__':
    unittest.main()
