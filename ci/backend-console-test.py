"""Exercise the actual CI console helpers with synthetic diagnostic strings."""
import argparse
import ast
import contextlib
import io
from pathlib import Path
import re
import sys
import unittest


parser = argparse.ArgumentParser(add_help=False)
parser.add_argument('--script', type=Path, default=Path(__file__).with_name('backend-ci.sh'))
options, unittest_arguments = parser.parse_known_args()
source = options.script.read_text().split("<<'PY'\n", 1)[1].rsplit('\nPY', 1)[0]
nodes = [node for node in ast.parse(source).body if isinstance(node, ast.FunctionDef)
         and node.name in ('safe_console', 'console_block')]
if len(nodes) != 2:
    raise ValueError('both actual backend CI console helpers are required')
helpers = {'re': re}
exec(compile(ast.Module(body=nodes, type_ignores=[]), str(options.script), 'exec'), helpers)


class BackendConsoleTests(unittest.TestCase):
    def test_unquoted_bearer_header_value_is_entirely_redacted(self):
        for text in ['Authorization: Bearer synthetic-header-value',
                     'authorization=Bearer synthetic-header-value',
                     'AUTHORIZATION:\tBearer synthetic-header-value']:
            with self.subTest(text=text):
                result = helpers['safe_console'](text)
                self.assertNotIn('synthetic-header-value', result)
                self.assertIn('[REDACTED]', result)

    def test_unquoted_basic_header_value_is_entirely_redacted(self):
        text = 'Authorization: Basic c3ludGhldGljLWZpeHR1cmU='
        result = helpers['safe_console'](text)
        self.assertNotIn('c3ludGhldGljLWZpeHR1cmU=', result)
        self.assertIn('[REDACTED]', result)

    def test_quoted_header_and_existing_secret_fields_remain_redacted(self):
        for text in ['{"authorization":"Bearer synthetic-secret-value"}',
                     "{'token': 'synthetic-secret-value'}",
                     'api_key=synthetic-secret-value']:
            with self.subTest(text=text):
                result = helpers['safe_console'](text)
                self.assertNotIn('synthetic-secret-value', result)
                self.assertIn('[REDACTED]', result)

    def test_multiline_header_keeps_unrelated_diagnostic_line(self):
        result = helpers['safe_console']('Authorization: Bearer synthetic-header-value\nBUILD SUCCESSFUL')
        self.assertNotIn('synthetic-header-value', result)
        self.assertIn('\nBUILD SUCCESSFUL', result)

    def test_ordinary_diagnostics_are_preserved(self):
        text = 'package fixture passed 5 tests; retry after an unavailable database'
        self.assertEqual(helpers['safe_console'](text), text)

    def test_workflow_commands_are_prefixed_and_control_bytes_removed(self):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            helpers['console_block']('synthetic', '\x1b[31m::error::synthetic message\x00\nplain')
        text = output.getvalue()
        self.assertNotIn('\x1b', text)
        self.assertNotIn('\x00', text)
        self.assertIn('[diagnostic] ::error::synthetic message', text)
        self.assertTrue(all(line.startswith('[diagnostic] ') for line in text.splitlines()))


if __name__ == '__main__':
    unittest.main(argv=[sys.argv[0], *unittest_arguments])
