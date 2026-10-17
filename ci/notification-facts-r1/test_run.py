#!/usr/bin/env python3
"""Only two new pure failed-only controls; never parent.verify, Go or fixtures."""
import copy
import importlib.util
import os
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('facts_mongo_retry_gate', Path(__file__).with_name('run.py'))
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


def events():
    package = m.PREFIX + m.PACKAGE
    result = [{'Action': 'start', 'Package': package}]
    for test in m.EXPECTED['tops'] + m.EXPECTED['subs']:
        result.extend([{'Action': 'run', 'Package': package, 'Test': test},
                       {'Action': 'pass', 'Package': package, 'Test': test}])
    result.append({'Action': 'pass', 'Package': package, 'Elapsed': .1})
    return result


class FactsRetryControls(unittest.TestCase):
    def test_failed_only_four_requires_every_child_and_rejects_reused_cases(self):
        raw = events()
        result = m.audit(raw, 0, m.PACKAGE, m.EXPECTED)
        self.assertEqual([result['topPASS'], result['subPASS']], [4, 4])
        broken = [raw[:-1], raw + [raw[-1]], raw[:1] + [raw[1]] + raw[1:]]
        for sub in m.EXPECTED['subs']:
            broken.append([x for x in raw if x.get('Test') != sub])
        for action, output in [('skip', ''), ('output', 'panic: fixture failure'), ('output', 'WARNING: DATA RACE')]:
            broken.append(raw[:-1] + [{'Action': action, 'Package': m.PREFIX + m.PACKAGE,
                                      'Test': m.EXPECTED['tops'][0], 'Output': output}, raw[-1]])
        for reused in ['TestNotificationFactsPagesBoundDocumentsWithoutRosterCap',
                       'TestOwnedRoleRPCReceiverRiskSQLSignedCallbackUnknownRestartAndReceiptFirst/stored-n']:
            broken.append(raw[:-1] + [{'Action': 'run', 'Package': m.PREFIX + m.PACKAGE, 'Test': reused}, raw[-1]])
        for changed in broken:
            with self.assertRaises(ValueError):
                m.audit(changed, 0, m.PACKAGE, m.EXPECTED)
        with self.assertRaises(ValueError):
            m.audit(raw, 1, m.PACKAGE, m.EXPECTED)

    def test_fixture_only_source_mongo_environment_and_cleanup_fail_closed(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            marker = b'func TestOwnedNotificationFactsCommitSurviveTerminalCleanupAndOriginalReceipt('
            before = b'package synthetic\n// old fixture\n' + marker + b't *testing.T) { retainedAssertion() }\n'
            after = b'package synthetic\n// fixture catalog only\n' + marker + b't *testing.T) { retainedAssertion() }\n'
            path = root / m.SOURCE
            path.parent.mkdir(parents=True)
            path.write_bytes(after)
            path.chmod(0o644)
            row = {'path': m.SOURCE, 'target': m.TARGET, 'bytes': len(after), 'sha256': m.sha(after),
                   'mode': '0o644', 'beforeSHA256': m.sha(before)}
            prior = {m.TARGET: before, 'unrelated.go': b'unchanged'}
            self.assertEqual(m.apply_fixture({'overlay': row}, prior, root)['unrelated.go'], b'unchanged')
            with self.assertRaises(ValueError):
                m.apply_fixture({'overlay': dict(row, beforeSHA256='0' * 64)}, prior, root)
            changed = after.replace(b'retainedAssertion()', b'weakenedAssertion()')
            path.write_bytes(changed)
            with self.assertRaises(ValueError):
                m.apply_fixture({'overlay': dict(row, bytes=len(changed), sha256=m.sha(changed))}, prior, root)
            path.write_bytes(after)
            extra = path.with_name('unreviewed.go')
            extra.write_text('extra')
            with self.assertRaises(ValueError):
                m.apply_fixture({'overlay': row}, prior, root)
        env = {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux',
               'GOTOOLCHAIN': 'local', 'GOWORK': 'off', 'GITHUB_RUN_ID': '123', 'GITHUB_RUN_ATTEMPT': '1',
               'GITHUB_SHA': 'a' * 40, **m.ZERO_FLAGS, **{key: m.MONGO for key in m.MONGO_KEYS}}
        self.assertEqual(m.fixture_environment(env), {**m.ZERO_FLAGS, **{key: m.MONGO for key in m.MONGO_KEYS}})
        for key, value in [(m.MONGO_KEYS[0], 'mongodb://foreign'), ('LUOBO_RISK_TEST_MYSQL_FIXTURE', '/foreign'),
                           ('LUOBO_UNKNOWN_TEST', '1'), ('GITHUB_SHA', 'bad')]:
            with self.assertRaises(ValueError):
                m.fixture_environment(dict(env, **{key: value}))
        clean = {'state': 'no_fixture_residue', **{key: 0 for key in ('mongo_user_database_count',
                 'redis_db15_dbsize_before_info', 'redis_db15_key_count', 'redis_db15_expiring_keys', 'unexpected_redis_database_count')}}
        m.require_collected(clean)
        with self.assertRaises(ValueError):
            m.require_collected(dict(clean, redis_db15_key_count=1))
        m.require_cleanup({'container_cleanup_exit_code': 0, 'scope': 'exact_run_label_and_two_names_only'})
        for broken in [{'container_cleanup_exit_code': 1, 'scope': 'exact_run_label_and_two_names_only'},
                       {'container_cleanup_exit_code': 0, 'scope': 'broader_unknown_cleanup'}]:
            with self.assertRaises(ValueError):
                m.require_cleanup(broken)


if __name__ == '__main__':
    unittest.main(verbosity=2)
