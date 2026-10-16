#!/usr/bin/env python3
"""Only owned-fixture gates, with Docker/SQL methods mocked; no service launch."""
import importlib.util
import json
import os
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location('owned_risk_mysql', Path(__file__).with_name('mysql_fixture.py'))
SUBJECT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SUBJECT)

class MySQLFixtureTests(unittest.TestCase):
    def test_host_and_private_file_gates(self):
        with patch.dict(os.environ, {}, clear=True), self.assertRaises(ValueError):
            SUBJECT.Fixture()
        with tempfile.TemporaryDirectory() as root:
            parent = Path(root).resolve()
            parent.chmod(0o700)
            file = parent / 'fixture.json'
            file.write_bytes(b'{}')
            file.chmod(0o600)
            self.assertEqual(SUBJECT.private_bytes(file), b'{}')
            file.chmod(0o644)
            with self.assertRaises(ValueError):
                SUBJECT.private_bytes(file)
            file.chmod(0o600)
            link = parent / 'alias.json'
            link.symlink_to(file)
            with self.assertRaises(ValueError):
                SUBJECT.private_bytes(link)

    def subject(self, root):
        subject = SUBJECT.Fixture.__new__(SUBJECT.Fixture)
        subject.private = root / 'private'
        subject.private.mkdir(mode=0o700)
        subject.output = root / 'output'
        subject.output.mkdir(mode=0o700)
        subject.state = {'name':'synthetic-exact-owned', 'database':'luobo_risk_test_' + 'a'*24,
                         'user':'lr_' + 'b'*16, 'serverUUID':'synthetic', 'claim':'c'*64}
        subject.load = lambda: subject.state
        return subject

    def test_unknown_logical_cleanup_still_joins_owned_container_but_fails(self):
        with tempfile.TemporaryDirectory() as temporary:
            subject = self.subject(Path(temporary))
            states = iter([{'Id':'c'*64, 'State':{'Running':True}}, {'Id':'c'*64, 'State':{'Running':False, 'ExitCode':0}}, None])
            subject.inspect_owned = lambda: next(states)
            subject.binding = lambda: None
            subject.sql = lambda _: (_ for _ in ()).throw(ValueError('synthetic unknown SQL ACK'))
            calls = []
            def command(args, **kwargs):
                calls.append(args)
                return SimpleNamespace(returncode=0)
            subject.command = command
            with self.assertRaises(ValueError):
                subject.cleanup()
            receipt = json.loads((subject.output / 'cleanup.json').read_text())
            self.assertEqual(receipt['state'], 'failed')
            self.assertFalse(receipt['logicalDatabaseAndUserZero'])
            self.assertTrue(receipt['normalShutdownJoined'])
            self.assertEqual([call[:2] for call in calls], [['docker','stop'], ['docker','rm']])
            self.assertTrue(all(call[-1] == 'c'*64 for call in calls))

    def test_foreign_container_is_never_stopped_and_exact_cleanup_requires_zero(self):
        with tempfile.TemporaryDirectory() as temporary:
            subject = self.subject(Path(temporary))
            subject.inspect_owned = lambda: (_ for _ in ()).throw(ValueError('foreign nonce'))
            with patch.object(subject, 'command') as command:
                with self.assertRaises(ValueError):
                    subject.cleanup()
                command.assert_not_called()
            states = iter([{'Id':'c'*64, 'State':{'Running':True}}, {'Id':'c'*64, 'State':{'Running':False, 'ExitCode':0}}, None])
            subject.inspect_owned = lambda: next(states)
            subject.binding = lambda: None
            responses = iter(['', '0\n0'])
            subject.sql = lambda _: next(responses)
            subject.command = lambda *args, **kwargs: SimpleNamespace(returncode=0)
            subject.cleanup()
            receipt = json.loads((subject.output / 'cleanup.json').read_text())
            self.assertEqual(receipt['state'], 'passed')
            self.assertTrue(receipt['logicalDatabaseAndUserZero'])

if __name__ == '__main__':
    unittest.main()
