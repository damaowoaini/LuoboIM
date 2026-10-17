#!/usr/bin/env python3
"""Three new pure controller controls. No Go, parent verification, materialization or services."""
import copy
import importlib.util
import os
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('notification_facts_gate', Path(__file__).with_name('run.py'))
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


def events(package, expected):
    prefix = m.PREFIX + package
    result = [{'Action': 'start', 'Package': prefix}]
    for test in expected['tops'] + expected['subs']:
        result.extend([{'Action': 'run', 'Package': prefix, 'Test': test},
                       {'Action': 'output', 'Package': prefix, 'Test': test, 'Output': 'synthetic complete case\n'},
                       {'Action': 'pass', 'Package': prefix, 'Test': test, 'Elapsed': .01}])
    result.append({'Action': 'pass', 'Package': prefix, 'Elapsed': .02})
    return result


class FactsControls(unittest.TestCase):
    def test_exact_two_scope_events_and_slash_selection(self):
        counts = [0, 0]
        for package, selected in m.TARGETS.items():
            raw = events(package, selected)
            summary = m.audit(raw, 0, package, selected)
            counts[0] += summary['topPASS']
            counts[1] += summary['subPASS']
            mutations = [raw[:-1], raw + [raw[-1]], raw[:-1] + [dict(raw[1]), raw[-1]],
                         raw[:-1] + [{'Action': 'skip', 'Package': m.PREFIX + package, 'Test': selected['tops'][0]}, raw[-1]],
                         raw[:-1] + [{'Action': 'output', 'Package': m.PREFIX + package, 'Output': 'WARNING: DATA RACE'}, raw[-1]],
                         raw[:-1] + [{'Action': 'output', 'Package': m.PREFIX + package, 'Output': 'panic: fixture failure'}, raw[-1]]]
            for leaf in selected['subs']:
                mutations.append([e for e in raw if e.get('Test') != leaf])
            for broken in mutations:
                with self.assertRaises(ValueError):
                    m.audit(broken, 0, package, selected)
            with self.assertRaises(ValueError):
                m.audit(raw, 1, package, selected)
        self.assertEqual(counts, [6, 5])
        self.assertNotIn('/', m.expression('internal/messagedelivery', m.TARGETS['internal/messagedelivery']))
        self.assertEqual(m.expression('internal/rpc/group', m.TARGETS['internal/rpc/group']), '^' + m.OLD_TOP + '$/^stored-n$')
        selected = m.TARGETS['internal/rpc/group']
        foreign = events('internal/rpc/group', selected)
        foreign.insert(1, {'Action': 'run', 'Package': m.PREFIX + 'internal/rpc/group', 'Test': m.OLD_TOP + '/silent'})
        with self.assertRaises(ValueError):
            m.audit(foreign, 0, 'internal/rpc/group', selected)

    def test_overlay_and_only_declared_old_business_adaptation(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            before, overlay = {}, []
            for target in sorted(m.OVERLAY_TARGETS):
                original = b'parent synthetic source\n'
                if target in m.MODIFIED:
                    before[target] = original
                raw = ('synthetic ' + target).encode()
                path = root / (m.SOURCE_ROOT + target)
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(raw)
                path.chmod(0o644)
                overlay.append({'target': target, 'path': m.SOURCE_ROOT + target, 'bytes': len(raw),
                                'sha256': m.sha(raw), 'mode': '0o644',
                                'beforeSHA256': m.sha(original) if target in m.MODIFIED else None})
            contract = {'overlay': overlay}
            actual = m.apply_overlay(contract, before, root)
            self.assertEqual(set(actual), m.OVERLAY_TARGETS)
            for field, value in [('beforeSHA256', '0' * 64), ('mode', '0o600')]:
                broken = copy.deepcopy(contract)
                broken['overlay'][0][field] = value
                with self.assertRaises(ValueError):
                    m.apply_overlay(broken, before, root)
            extra = root / (m.SOURCE_ROOT + 'unreviewed.go')
            extra.write_text('unexpected')
            with self.assertRaises(ValueError):
                m.apply_overlay(contract, before, root)
        old = ('func ' + m.OLD_TOP + '(t *testing.T) {\n'
               'ctx, s, db, _ := roleEffectsFixture(t, nil, nil)\n'
               'count, err = db.Collection(messagedelivery.Receipts).CountDocuments(ctx, bson.M{"source_id": id})\n'
               'if err != nil || count != 1 { t.Fatal("recovery created duplicate source", err) }\n'
               '}\ntype ownedSilentReader struct{}\n')
        new = old.replace(':= roleEffectsFixture(', ':= ownedRPCFreshFixture(').replace(
            'bson.M{"source_id": id})', 'bson.M{"_id": result.ID, "epoch": epoch, "source_id": id, "branches": bson.M{"$exists": true}, "accepted_at": bson.M{"$exists": true}})')
        new = new.replace('\n}\ntype', '\n\t\t\tassertOwnedRPCFactsRows(t, ctx, db, epoch, activation, rows[0], result)\n}\ntype')
        m.declared_fixture_only(old.encode(), new.encode())
        with self.assertRaises(ValueError):
            m.declared_fixture_only(old.encode(), new.replace('count != 1', 'count != 2').encode())

    def test_private_fixture_environment_resources_and_cleanup_fail_closed(self):
        env = {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted', 'RUNNER_OS': 'Linux',
               'GOTOOLCHAIN': 'local', 'GOWORK': 'off', 'GITHUB_RUN_ID': '123', 'GITHUB_RUN_ATTEMPT': '1',
               'GITHUB_SHA': 'a' * 40, **m.ZERO_FLAGS, **{k: m.MONGO for k in m.MONGO_KEYS}}
        bindings = m.fixture_environment(env)
        self.assertEqual(bindings[m.SQL_KEY], '/tmp/luobo-risk-123-1/private/fixture.json')
        for key, value in [(m.SQL_KEY, '/foreign/fixture.json'), (m.MONGO_KEYS[0], 'mongodb://foreign'),
                           ('LUOBO_UNKNOWN_TEST', '1'), ('GITHUB_SHA', 'bad')]:
            with self.assertRaises(ValueError):
                m.fixture_environment(dict(env, **{key: value}))
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary).resolve()
            directory.chmod(0o700)
            path = directory / 'fixture.json'
            with self.assertRaises(FileNotFoundError):
                m.require_private_fixture(path)
            path.write_text('{}')
            path.chmod(0o600)
            m.require_private_fixture(path)
            path.chmod(0o644)
            with self.assertRaises(ValueError):
                m.require_private_fixture(path)
            generated = m.test_environment({'PATH': '/fixed', 'HOME': '/actual/home'}, directory, '/fixed/go', bindings)
            self.assertEqual(generated['HOME'], '/actual/home')
            self.assertEqual(generated['GOPROXY'], 'off')
        guard = {'status': 'passed', 'exit_code': 0, 'minimum_free_required_bytes': 1 << 30,
                 'max_allocated_allowed_bytes': 16 << 30, 'minimum_observed_free_bytes': 2 << 30,
                 'maximum_observed_task_allocated_bytes': 0}
        m.require_guard(guard, 0)
        for field, value in [('status', 'timeout'), ('exit_code', None), ('minimum_observed_free_bytes', 1),
                             ('maximum_observed_task_allocated_bytes', (16 << 30) + 1)]:
            with self.assertRaises(ValueError):
                m.require_guard(dict(guard, **{field: value}), 0)
        mongo = {'state': 'no_fixture_residue', **{k: 0 for k in ('mongo_user_database_count',
                 'redis_db15_dbsize_before_info', 'redis_db15_key_count', 'redis_db15_expiring_keys', 'unexpected_redis_database_count')}}
        sql = {'ownedDatabaseCount': 1, 'ownedUserCount': 1, 'ownedTableCount': 2, 'unexpectedDatabases': 0,
               'UUIDAndClaimConfirmed': True, 'privateDiagnosticsUploaded': False}
        m.require_collected(mongo, sql)
        with self.assertRaises(ValueError):
            m.require_collected(dict(mongo, redis_db15_key_count=1), sql)
        with self.assertRaises(ValueError):
            m.require_collected(mongo, dict(sql, privateDiagnosticsUploaded=True))
        done = {'state': 'passed', 'scope': 'single_exact_id_name_run_nonce_image',
                'logicalDatabaseAndUserZero': True, 'normalShutdownJoined': True, 'exactContainerAbsent': True}
        m.require_cleanup({'container_cleanup_exit_code': 0, 'scope': 'exact_run_label_and_two_names_only'}, done)
        for field in ('logicalDatabaseAndUserZero', 'normalShutdownJoined', 'exactContainerAbsent'):
            with self.assertRaises(ValueError):
                m.require_cleanup({'container_cleanup_exit_code': 0, 'scope': 'exact_run_label_and_two_names_only'}, dict(done, **{field: False}))


if __name__ == '__main__':
    unittest.main(verbosity=2)
