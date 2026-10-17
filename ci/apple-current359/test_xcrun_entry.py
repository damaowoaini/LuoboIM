"""New entry/cache filesystem controls only. Never spawn Apple or Python tools."""
import ast
import copy
import json
import os
from pathlib import Path
import re
import stat
import tempfile
import unittest
from unittest import mock

import xcrun_adapter as a


class ActualEntryBoundary(unittest.TestCase):
    def test_installed_shell_entry_keeps_original_arguments_and_executes_real_Apple_ARM_argv(self):
        with tempfile.TemporaryDirectory() as tmp:
            work = Path(tmp).resolve()/'luobo-apple-current359'
            work.mkdir(mode=0o700)
            source = Path(a.__file__).resolve()
            python = '/Library/Frameworks/Python.framework/Versions/3.12/bin/python3'
            installed = a.install(work, source, python)
            script = work/'tool-adapters/xcrun'
            body = script.read_bytes()
            self.assertTrue(body.startswith(b'#!/bin/sh\n""":"\n'))
            self.assertEqual(body.splitlines()[2], ('exec '+python+' -B "$0" "$@"').encode())
            ast.parse(body)
            self.assertEqual(stat.S_IMODE(script.stat().st_mode), 0o500)
            self.assertEqual(installed['outer_exec'], '/usr/bin/arch')
            self.assertEqual(installed['real_tool'], '/usr/bin/xcrun')
            self.assertEqual(installed, a.check(work, source, python))
            (work/'activation.json').write_text(json.dumps({'identity': a.IDENTITY}))
            class ReplacedProcess(Exception): pass
            with mock.patch.object(a, '__file__', str(script)), \
                    mock.patch.object(a.sys, 'argv', [str(script), 'xcodebuild', '-version']), \
                    mock.patch.object(a.Path, 'cwd', return_value=work), \
                    mock.patch.object(a, 'cache_boundary', return_value={}) as boundary, \
                    mock.patch.object(a.os, 'execv', side_effect=ReplacedProcess) as actual:
                with self.assertRaises(ReplacedProcess): a.main()
            boundary.assert_called_once_with(work)
            actual.assert_called_once_with('/usr/bin/arch',
                ['/usr/bin/arch','-arm64e','/usr/bin/xcrun','--no-cache','xcodebuild','-version'])
            rows = [json.loads(p.read_bytes()) for p in (work/'evidence/xcrun-adapter').glob('*.json')]
            self.assertEqual(len(rows), 1)
            self.assertEqual(rows[0]['outer_exec'], '/usr/bin/arch')
            self.assertEqual(rows[0]['outer_argv'], actual.call_args.args[1][1:])
            self.assertEqual(rows[0]['real_tool'], '/usr/bin/xcrun')
            self.assertEqual(rows[0]['tool_argv'], ['--no-cache','xcodebuild','-version'])
            self.assertIs(rows[0]['response_synthesized'], False)
            for value in ['/tmp/python;bad', '/tmp/python$(bad)', '/tmp/../python', 'python3']:
                with self.subTest(value=value):
                    with self.assertRaises(ValueError): a.script_body(source.read_bytes(), value)
            script.chmod(0o600); script.write_bytes(body+b'\n'); script.chmod(0o500)
            with self.assertRaisesRegex(ValueError, 'installed source-pinned adapter differs'):
                a.check(work, source, python)

    def test_private_stable_cache_root_and_exact_new_leaf_grant_protect_existing_and_foreign_paths(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()/'T'; root.mkdir(mode=0o700)
            uid = os.getuid()
            identity = a.cache_directory_identity(root, uid)
            work = Path(tmp).resolve()/'owned'; (work/'tool-adapters').mkdir(parents=True)
            old = root/'xcrun_db-OldABC12'; old.write_bytes(b'existing cache body must not be read')
            row = {'schema':1,'lexical':str(root),'canonical':str(root),
                   'uid':uid,'directory_identity':identity}
            with mock.patch.object(a, 'discover_cache_directory', side_effect=lambda: copy.deepcopy(row)):
                sealed = a.seal_cache_boundary(work)
                self.assertEqual(sealed['protected_preexisting'], [old.name])
                self.assertEqual(a.cache_boundary(work), sealed)
                profile = a.cache_profile(work)
                self.assertIn('(regex #'+json.dumps(a.cache_leaf_pattern(root))+')', profile)
                self.assertIn('(deny file-write* (literal '+json.dumps(str(old))+'))', profile)
                self.assertNotIn('(subpath ', profile)
                pattern = a.cache_leaf_pattern(root)
                self.assertIsNotNone(re.fullmatch(pattern, str(root/'xcrun_db-Ab12Cd34')))
                for value in [root/'other',root/'xcrun_db-abc',root/'xcrun_db-Ab12Cd34'/'nested',
                              root.parent/'xcrun_db-Ab12Cd34']:
                    self.assertIsNone(re.fullmatch(pattern,str(value)))
                changed = copy.deepcopy(row); changed['directory_identity'][1] += 1
                with mock.patch.object(a, 'discover_cache_directory', return_value=changed):
                    with self.assertRaisesRegex(ValueError, 'boundary changed'): a.cache_profile(work)
            with self.assertRaises(ValueError): a.cache_directory_identity(root, uid+1)
            root.chmod(0o755)
            with self.assertRaises(ValueError): a.cache_directory_identity(root, uid)
            root.chmod(0o700)
            link = root.parent/'alias'; link.symlink_to(root, target_is_directory=True)
            with self.assertRaises(ValueError): a.cache_directory_identity(link, uid)
            with mock.patch.object(a.sys, 'platform', 'darwin'), \
                    mock.patch.object(a.os, 'confstr', return_value='/tmp/') as confstr:
                with self.assertRaisesRegex(ValueError, 'temporary directory shape'): a.discover_cache_directory()
                confstr.assert_called_once_with(65537)


if __name__ == '__main__': unittest.main()
