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
        self.assertEqual(original['source_ref'], '0e1cb1ae5f3edc3db8a0a94970b363a4cd6ae3b1')
        by_path = {row['path']: row for row in original['files']}
        self.assertEqual(by_path['app/lib/core/privacy/message_privacy.dart'], {'path': 'app/lib/core/privacy/message_privacy.dart', 'bytes': 18789, 'sha256': 'c0da515e6ca3b935ac7b96c3dbae5302125c72e0c2ad38727bd84539b4f3073a'})
        self.assertEqual(by_path['app/test/features/chat/message_privacy_test.dart'], {'path': 'app/test/features/chat/message_privacy_test.dart', 'bytes': 13002, 'sha256': '6a80b8f4e79d2457ebb6799475b4fce6a81b6c76b8037e2b56365354bc275c90'})
        edits = [lambda s: s.update(identity='current359-apple-source-consumer-v1'),
                 lambda s: s.update(source_ref='bdffc675e6ec0f193bcb5cf786859a7746869061'),
                 lambda s: s.update(identity='current354-apple-source-consumer-v1'),
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
        self.assertEqual(lane['identity'], 'current359-null2-apple-native-resolution-v2')
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
