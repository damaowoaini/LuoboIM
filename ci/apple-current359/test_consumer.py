"""Prepared new359 identity/driver controls only; no native subprocesses."""
import copy
import json
from pathlib import Path
import unittest
from unittest import mock

import consume
import run as driver


class Current359Consumer(unittest.TestCase):
    def spec(self):
        return json.loads((consume.HERE/'inputs.json').read_bytes())

    def test_new359_477_411_and_published_ref_cannot_alias_historical354_or_partial_union(self):
        original = self.spec()
        self.assertEqual(consume.checked_identity(original), original)
        self.assertEqual((len(original['business_files']), len(original['files']),
                          len(original['portable_paths'])), (359, 477, 411))
        edits = [lambda s: s.update(identity='current354-apple-source-consumer-v1'),
                 lambda s: s.update(source_ref='ff7bc1f588f935d6315ad0211e68e15e58cd037e'),
                 lambda s: s.update(business_members=354),
                 lambda s: s.update(portable_members=406),
                 lambda s: s['files'].pop(),
                 lambda s: s['files'][0].update(sha256='0' * 64),
                 lambda s: s['portable_paths'].__setitem__(0, s['portable_paths'][1])]
        for edit in edits:
            changed = copy.deepcopy(original); edit(changed)
            with self.subTest(change=edit), self.assertRaises(ValueError):
                consume.checked_identity(changed)
        summary = consume.packaging_plan(original)
        self.assertFalse(summary['ready_for_App_build'])
        self.assertFalse(summary['Go_rebind'])

    def test_new_driver_null_approval_blocks_every_App_command_and_workflow_is_resolution_only(self):
        lane = json.loads((driver.HERE/'lane.json').read_bytes())
        self.assertEqual(lane['identity'], 'current359-apple-native-resolution-v1')
        self.assertEqual(lane['source_ref'], consume.REF)
        self.assertIsNone(lane['approved_native_resolution'])
        with mock.patch.object(driver, 'command') as command, \
                mock.patch.object(driver.packaging, 'regular', return_value=b'{}'):
            with self.assertRaisesRegex(ValueError, 'Root-accepted'):
                driver.package(Path('/never-created'), lane, self.spec())
            command.assert_not_called()
        workflow = (driver.REPO/'.github/workflows/apple-current359-resolve.yml').read_text()
        self.assertIn('--stage acquire', workflow)
        self.assertIn('--stage resolve', workflow)
        self.assertNotIn('--stage package', workflow)
        self.assertNotIn('push:', workflow)
        self.assertNotIn('pull_request:', workflow)


if __name__ == '__main__':
    unittest.main()
