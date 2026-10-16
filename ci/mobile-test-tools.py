#!/usr/bin/env python3
"""Pure Python CI gate checks, separate from current354 focused58/full1280."""
import copy
import importlib.util
import json
from pathlib import Path
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location('mobile_check', Path(__file__).with_name('mobile-check.py'))
check = importlib.util.module_from_spec(spec)
spec.loader.exec_module(check)


def reporter(*, footer=True, skip=False, hidden_failure=False):
    events = [
        {'type': 'testStart', 'test': {'id': 0}},
        {'type': 'testDone', 'testID': 0, 'hidden': True, 'skipped': False,
         'result': 'error' if hidden_failure else 'success'},
        {'type': 'testStart', 'test': {'id': 1}},
        {'type': 'testDone', 'testID': 1, 'hidden': False, 'skipped': skip, 'result': 'success'},
    ]
    if footer:
        events.append({'type': 'done', 'success': True})
    return '\n'.join(map(json.dumps, events))


class ReporterTests(unittest.TestCase):
    def test_visible_count_excludes_hidden_loader(self):
        result = check.machine_summary(reporter(), 1)
        self.assertEqual((result['passed'], result['hidden']), (1, 1))

    def test_incomplete_runner_is_not_pass(self):
        with self.assertRaisesRegex(ValueError, 'incomplete'):
            check.machine_summary(reporter(footer=False), 1)

    def test_hidden_load_failure_is_not_pass(self):
        with self.assertRaisesRegex(ValueError, 'incomplete'):
            check.machine_summary(reporter(hidden_failure=True), 1)

    def test_skipped_case_is_not_pass(self):
        with self.assertRaisesRegex(ValueError, 'incomplete'):
            check.machine_summary(reporter(skip=True), 1)

    def test_wrong_expected_count_is_not_pass(self):
        with self.assertRaisesRegex(ValueError, 'unexpected'):
            check.machine_summary(reporter(), 621)

    def test_event_after_success_footer_is_not_pass(self):
        with self.assertRaisesRegex(ValueError, 'after terminal'):
            check.machine_summary(reporter() + '\n' + json.dumps({'type': 'done', 'success': True}), 1)


def reference_report():
    plan = check.source.descriptor()['reference_entry']
    return [
        {'type': 'testStart', 'test': {'id': 0, 'name': 'loading synthetic entry.dart'}},
        {'type': 'testDone', 'testID': 0, 'hidden': True, 'skipped': False, 'result': 'success'},
        {'type': 'testStart', 'test': {'id': 1, 'name': plan['failed_test_name']}},
        {'type': 'error', 'testID': 1, 'isFailure': True,
         'error': 'Expected: exactly one matching candidate\nActual: Found 0 widgets with tooltip "全局搜索"'},
        {'type': 'testDone', 'testID': 1, 'hidden': False, 'skipped': False, 'result': 'error'},
        {'type': 'testStart', 'test': {'id': 2, 'name': plan['passed_test_name']}},
        {'type': 'testDone', 'testID': 2, 'hidden': False, 'skipped': False, 'result': 'success'},
        {'type': 'done', 'success': False},
    ]


class ReferenceReporterTests(unittest.TestCase):
    def run_report(self, events, code=1):
        return check.reference_entry_summary('\n'.join(map(json.dumps, events)), code,
                                             check.source.descriptor()['reference_entry'])

    def test_reference_accepts_named_assertion_and_existing_filter_only(self):
        result = self.run_report(reference_report())
        self.assertEqual((result['passed'], result['assertion_failed'], result['hidden_successful_loads']), (1, 1, 1))

    def test_reference_rejects_duplicate_visible_positive_name(self):
        events = reference_report()
        name = check.source.descriptor()['reference_entry']['passed_test_name']
        events[-1:-1] = [
            {'type': 'testStart', 'test': {'id': 99, 'name': name}},
            {'type': 'testDone', 'testID': 99, 'hidden': False, 'skipped': False, 'result': 'success'},
        ]
        with self.assertRaisesRegex(ValueError, 'exactly two distinct'):
            self.run_report(events)

    def test_reference_rejects_hidden_load_failure(self):
        events = reference_report(); events[1]['result'] = 'error'
        with self.assertRaises(ValueError): self.run_report(events)

    def test_reference_rejects_timeout_even_with_other_assertion_words(self):
        events = reference_report(); events[3]['error'] += '\nTimeoutException timed out'
        with self.assertRaises(ValueError): self.run_report(events)

    def test_reference_rejects_panic(self):
        events = reference_report(); events[3]['error'] += '\npanic: native loader'
        with self.assertRaises(ValueError): self.run_report(events)

    def test_reference_rejects_arbitrary_assertion(self):
        events = reference_report(); events[3]['error'] = 'Expected: true\nActual: false'
        with self.assertRaises(ValueError): self.run_report(events)

    def test_reference_rejects_exit0(self):
        with self.assertRaises(ValueError): self.run_report(reference_report(), code=0)

    def test_reference_requires_footer(self):
        with self.assertRaises(ValueError): self.run_report(reference_report()[:-1])

    def test_reference_rejects_numeric_false_footer(self):
        events = reference_report(); events[-1]['success'] = 0
        with self.assertRaises(ValueError): self.run_report(events)

    def test_reference_rejects_duplicate_start(self):
        events = reference_report(); events.insert(3, copy.deepcopy(events[2]))
        with self.assertRaises(ValueError): self.run_report(events)

    def test_reference_rejects_event_after_footer(self):
        events = reference_report(); events.append({'type': 'testStart', 'test': {'id': 5, 'name': 'late'}})
        with self.assertRaises(ValueError): self.run_report(events)

    def test_reference_rejects_failed_positive(self):
        events = reference_report(); events[6]['result'] = 'error'
        with self.assertRaises(ValueError): self.run_report(events)

    def test_reference_rejects_skipped_case(self):
        events = reference_report(); events[6]['skipped'] = True
        with self.assertRaises(ValueError): self.run_report(events)


class DescriptorTests(unittest.TestCase):
    def rejects(self, change):
        plan = check.source.descriptor(); change(plan)
        with mock.patch.object(check.source, 'regular', return_value=json.dumps(plan).encode()):
            with self.assertRaises(ValueError): check.source.descriptor()

    def test_historical_cohorts_cannot_alias354(self):
        for historical in ('current257', 'current269', 'current282', 'current287', 'current291', 'current292', 'current294', 'current304', 'current309', 'current310', 'current316', 'current323', 'current331', 'current336', 'current344', 'current351'):
            with self.subTest(historical=historical):
                self.rejects(lambda p: p.update({historical: {}}))

    def test_wrong_full_count_cannot_pass(self):
        self.rejects(lambda p: p['current354'].update(expected_full_tests=734))

    def test_reference_requires_all_four_before_files(self):
        self.rejects(lambda p: p['reference_entry']['replacement_files'].pop())

    def test_reference_is_not_enabled_by_default(self):
        self.rejects(lambda p: p['reference_entry'].update(default_enabled=True))



def named_report(names):
    events = []
    for identity, name in enumerate(names):
        events += [
            {'type': 'testStart', 'test': {'id': identity, 'name': name}},
            {'type': 'testDone', 'testID': identity, 'hidden': False, 'skipped': False, 'result': 'success'},
        ]
    return '\n'.join(map(json.dumps, events + [{'type': 'done', 'success': True}]))


class RequiredNamesTests(unittest.TestCase):
    def test_exact_names_preserve_parent_multiplicity_and_new_cases(self):
        result = check.required_name_summary(named_report(['old', 'old', 'new']), 3,
                                             [{'name': 'old', 'count': 2}, {'name': 'new', 'count': 1}])
        self.assertTrue(result['exact_successful_name_multiset'])

    def test_same_count_different_name_cannot_replace_old_case(self):
        with self.assertRaisesRegex(ValueError, 'multiset differs'):
            check.required_name_summary(named_report(['new', 'replacement']), 2,
                                        [{'name': 'old', 'count': 1}, {'name': 'new', 'count': 1}])

    def test_duplicate_parent_names_require_full_multiplicity(self):
        with self.assertRaisesRegex(ValueError, 'multiset differs'):
            check.required_name_summary(named_report(['old', 'replacement']), 2,
                                        [{'name': 'old', 'count': 2}])

    def test_names_never_override_incomplete_footer_or_hidden_failure(self):
        for text in [named_report(['old']).rsplit('\n', 1)[0], reporter(hidden_failure=True)]:
            with self.subTest(text=text):
                with self.assertRaises(ValueError):
                    check.required_name_summary(text, 1, [{'name': 'old', 'count': 1}])

    def test_required_count_boolean_is_rejected(self):
        with self.assertRaisesRegex(ValueError, 'invalid required'):
            check.required_name_summary(named_report(['old']), 1, [{'name': 'old', 'count': True}])


class BackgroundInventoryTests(unittest.TestCase):
    def rejected_inventory(self, change):
        plan = check.source.descriptor()
        body = check.source.regular(check.source.REPO / plan['required_test_names']['path'])
        names = json.loads(body); change(names)
        body = json.dumps(names).encode()
        plan = copy.deepcopy(plan)
        plan['required_test_names'].update(bytes=len(body), sha256=check.source.sha(body))
        with mock.patch.object(check.source, 'regular', return_value=body):
            with self.assertRaisesRegex(ValueError, 'background names do not preserve'):
                check.source.required_names(plan)

    def test_same_count_target_cannot_replace_background_case(self):
        self.rejected_inventory(lambda n: n['parent_target345'][-1].update(name='forged same-count replacement'))

    def test_same_count_parent_full_cannot_replace_original_case(self):
        self.rejected_inventory(lambda n: n['parent_full985'][-1].update(name='forged old-case replacement'))


class SavedEmojiInventoryTests(BackgroundInventoryTests):
    def rejected_inventory(self, change):
        plan = check.source.descriptor()
        names = json.loads(check.source.regular(check.source.REPO / plan['required_test_names']['path']))
        change(names)
        body = json.dumps(names).encode()
        plan = copy.deepcopy(plan)
        plan['required_test_names'].update(bytes=len(body), sha256=check.source.sha(body))
        with mock.patch.object(check.source, 'regular', return_value=body):
            with self.assertRaisesRegex(ValueError, 'saved emoji names do not preserve'):
                check.source.required_names(plan)

    def test_same_count_target_cannot_replace_background_case(self):
        self.rejected_inventory(lambda n: n['parent_target386'][-1].update(name='forged saved-emoji replacement'))

    def test_same_count_parent_full_cannot_replace_original_case(self):
        self.rejected_inventory(lambda n: n['parent_full1027'][-1].update(name='forged parent replacement'))


class LocationReceiveInventoryTests(unittest.TestCase):
    def rejected_inventory(self, change):
        plan = check.source.descriptor()
        names = json.loads(check.source.regular(check.source.REPO / plan['required_test_names']['path']))
        change(names)
        body = json.dumps(names).encode()
        plan = copy.deepcopy(plan)
        plan['required_test_names'].update(bytes=len(body), sha256=check.source.sha(body))
        with mock.patch.object(check.source, 'regular', return_value=body):
            with self.assertRaisesRegex(ValueError, 'location receive names do not preserve'):
                check.source.required_names(plan)

    def test_same_count_target_cannot_replace_location_receive_case(self):
        self.rejected_inventory(lambda n: next(r for r in n['current_target422'] if r['name'] == n['new_location_receive36'][0]['name']).update(name='forged location receive replacement'))

    def test_same_count_parent_full_cannot_replace_original_case(self):
        self.rejected_inventory(lambda n: n['parent_full1068'][-1].update(name='forged original full replacement'))


class LocationSendInventoryTests(unittest.TestCase):
    def rejected_inventory(self, change):
        plan = check.source.descriptor()
        names = json.loads(check.source.regular(check.source.REPO / plan['required_test_names']['path']))
        change(names)
        body = json.dumps(names).encode()
        plan = copy.deepcopy(plan)
        plan['required_test_names'].update(bytes=len(body), sha256=check.source.sha(body))
        with mock.patch.object(check.source, 'regular', return_value=body):
            with self.assertRaisesRegex(ValueError, 'location send names do not preserve'):
                check.source.required_names(plan)

    def test_same_count_target_cannot_replace_location_send_case(self):
        self.rejected_inventory(lambda n: next(r for r in n['current_target460'] if r['name'] == n['new_location_send38'][0]['name']).update(name='forged location send replacement'))

    def test_same_count_parent_full_cannot_replace_original_case(self):
        self.rejected_inventory(lambda n: n['parent_full1104'][-1].update(name='forged original full replacement'))


class LocationSourceCouplingTests(unittest.TestCase):
    def test_official_webview_archive_cannot_change_under_same_lock(self):
        plan = copy.deepcopy(check.source.descriptor())
        lock = check.source.regular(check.source.REPO / 'app/pubspec.lock')
        check.source.webview_dependency_contract(plan, lock)
        plan['webview_dependencies']['files'][0]['archive_sha256'] = '0' * 64
        with self.assertRaisesRegex(ValueError, 'archive or lock identity differs'):
            check.source.webview_dependency_contract(plan, lock)

    def test_native_snapshot_rejects_an_unapproved_same_count_path(self):
        plan = copy.deepcopy(check.source.descriptor())
        check.source.native_source_contract(plan)
        plan['native_source_snapshot']['files'][0]['path'] = 'app/ios/Runner/Unapproved.swift'
        with self.assertRaisesRegex(ValueError, 'native location source snapshot differs'):
            check.source.native_source_contract(plan)


class CleanupTests(unittest.TestCase):
    def fixture_report(self):
        normal = ['recorder.begin', 'recorder.end', 'sdk.begin', 'sdk.end', 'storage.end']
        failure = ['recorder.begin', 'sdk.begin', 'sdk.end', 'storage.end']
        contract = {
            'named_fixtures': [
                {'name': 'page', 'kind': 'page', 'markers': normal + ['unmount.begin', 'unmount.end']},
                {'name': 'expected failure', 'kind': 'expected_close_failure', 'markers': failure}],
            'repository_awaited_teardown_names': ['repository'],
            'parent_creation_instances': 1,
            'parent_creation_markers': ['release.begin', 'native.drained', 'sdk.begin', 'sdk.end', 'unmount.begin', 'unmount.end'],
            'parent_safe_exit_markers': ['create.entered', 'signout.begin', 'signout.end', 'route.removed', 'create.released', 'create.returned']}
        events = []
        for identity, row in enumerate(contract['named_fixtures']):
            events.append({'type': 'testStart', 'test': {'id': identity, 'name': row['name']}})
            events += [{'type': 'print', 'testID': identity, 'message': 'call_records_cleanup ' + marker}
                       for marker in row['markers']]
        events.append({'type': 'testStart', 'test': {'id': 9, 'name': 'parent'}})
        events += [{'type': 'print', 'testID': 9, 'message': prefix + marker}
                   for prefix, markers in [('creation_fixture_cleanup ', contract['parent_creation_markers']),
                                           ('creation_safe_exit ', contract['parent_safe_exit_markers'])]
                   for marker in markers]
        return contract, events

    def test_cleanup_distinguishes_successful_close_and_expected_failure(self):
        contract, events = self.fixture_report()
        result = check.call_records_cleanup_summary('\n'.join(map(json.dumps, events)), contract)
        self.assertEqual((result['call_record_fixtures'], result['normal_recorder_closes'],
                          result['expected_close_failures'], result['page_unmounts'],
                          result['parent_creation_fixtures']), (2, 1, 1, 1, 1))

    def test_cleanup_rejects_missing_tail_even_after_other_tests_finish(self):
        contract, events = self.fixture_report()
        events = [e for e in events if e.get('message') != 'call_records_cleanup storage.end']
        with self.assertRaisesRegex(ValueError, 'sequence differs'):
            check.call_records_cleanup_summary('\n'.join(map(json.dumps, events)), contract)

    def test_cleanup_rejects_duplicate_or_foreign_print(self):
        contract, events = self.fixture_report()
        for extra in [next(e for e in events if e.get('type') == 'print'),
                      {'type': 'print', 'testID': 99, 'message': 'call_records_cleanup recorder.begin'}]:
            with self.subTest(extra=extra):
                with self.assertRaises(ValueError):
                    check.call_records_cleanup_summary('\n'.join(map(json.dumps, events + [extra])), contract)

    def test_cleanup_rejects_reordered_parent_sequence(self):
        contract, events = self.fixture_report()
        indices = [i for i, e in enumerate(events) if e.get('message', '').startswith('creation_fixture_cleanup ')]
        a, b = indices[0:2]
        events[a], events[b] = events[b], events[a]
        with self.assertRaisesRegex(ValueError, 'parent creation sequence'):
            check.call_records_cleanup_summary('\n'.join(map(json.dumps, events)), contract)


class NextUnreadCleanupTests(unittest.TestCase):
    def fixture(self):
        contract = {'fixture_names': ['actual page'], 'markers': ['sdk.begin', 'sdk.end', 'unmount.begin', 'unmount.end'],
                    'unit_names': ['unit'], 'geometry_names': ['geometry']}
        events = [{'type': 'testStart', 'test': {'id': 1, 'name': 'actual page'}}]
        events += [{'type': 'print', 'testID': 1, 'message': 'next_unread_cleanup ' + marker}
                   for marker in contract['markers']]
        return contract, events

    def test_next_unread_cleanup_requires_the_actual_owned_sdk_and_unmount_sequence(self):
        contract, events = self.fixture()
        result = check.next_unread_cleanup_summary('\n'.join(map(json.dumps, events)), contract)
        self.assertEqual((result['sdk_fixture_sequences'], result['unit_awaited_teardown_cases'],
                          result['owned_geometry_finally_cases']), (1, 1, 1))

    def test_next_unread_cleanup_missing_shutdown_tail_does_not_pass(self):
        contract, events = self.fixture()
        with self.assertRaises(ValueError):
            check.next_unread_cleanup_summary('\n'.join(map(json.dumps, events[:-1])), contract)

    def test_next_unread_cleanup_rejects_duplicate_or_foreign_prints(self):
        contract, events = self.fixture()
        for extra in [events[-1], {'type': 'print', 'testID': 99, 'message': 'next_unread_cleanup sdk.end'}]:
            with self.subTest(extra=extra):
                with self.assertRaises(ValueError):
                    check.next_unread_cleanup_summary('\n'.join(map(json.dumps, events + [extra])), contract)


class CardInventoryTests(unittest.TestCase):
    def test_same_count_focused_replacement_cannot_drop_a_card_case(self):
        plan = check.source.descriptor()
        names = json.loads(check.source.regular(check.source.REPO / plan['required_test_names']['path']))
        original = names['new_chat_card80'][0]['name']
        next(r for r in names['current_target540'] if r['name'] == original)['name'] = 'forged same-count card replacement'
        body = json.dumps(names).encode()
        plan['required_test_names'].update(bytes=len(body), sha256=check.source.sha(body))
        with mock.patch.object(check.source, 'regular', return_value=body):
            with self.assertRaisesRegex(ValueError, 'card names do not preserve'):
                check.source.required_names(plan)

    def test_analysis_file_must_match_the_current_authored_source_pin(self):
        plan = copy.deepcopy(check.source.descriptor())
        plan['chat_card']['selected_files'][0]['sha256'] = '0' * 64
        with self.assertRaisesRegex(ValueError, 'card source/analysis/cleanup inventory'):
            check.source.card_contract(plan)


class ValidationModeTests(unittest.TestCase):
    def test_focused_runs_only_new58_and_selected6_analysis(self):
        plan = check.source.descriptor()
        phase = check.validation_plan(plan, 'group-revoke-58')
        self.assertEqual((phase['expected'], phase['names_key']), (58, 'new_group_revoke58'))
        self.assertEqual(phase['test_paths'], plan['group_revoke']['test_paths'])
        self.assertEqual(phase['analysis_paths'], plan['group_revoke']['analysis_paths'])
        self.assertEqual((len(phase['test_paths']), len(phase['analysis_paths'])), (2, 6))

    def test_full_has_only_full1280_without_a_repeated_historical540_prerequisite(self):
        phase = check.validation_plan(check.source.descriptor(), 'full-regression', True)
        self.assertEqual(phase, {'test_label': 'full1280', 'expected': 1280,
                                'test_paths': [], 'names_key': 'current_full1280',
                                'analysis_paths': None})

    def test_reference_or_unknown_mode_cannot_dispatch_under_focused_default(self):
        plan = check.source.descriptor()
        for mode, reference in [('group-revoke-58', True), ('target540', False), ('chat-card-80', False), ('group-revoke-58', 1)]:
            with self.subTest(mode=mode, reference=reference):
                with self.assertRaises(ValueError):
                    check.validation_plan(plan, mode, reference)


class CardCleanupTests(unittest.TestCase):
    def fixture(self):
        contract = {'widget_names': ['actual widget'], 'repository_names': ['actual repository'],
                    'model_names': ['pure model'],
                    'widget_markers': ['sdk.begin', 'sdk.end', 'unmount.begin', 'unmount.end'],
                    'repository_markers': ['sdk.begin', 'sdk.end', 'storage.begin', 'storage.end']}
        events = []
        for identity, group, prefix, markers in [(1, 'widget_names', 'chat_card_cleanup ', 'widget_markers'),
                                                 (2, 'repository_names', 'chat_card_repository_cleanup ', 'repository_markers')]:
            events.append({'type': 'testStart', 'test': {'id': identity, 'name': contract[group][0]}})
            events += [{'type': 'print', 'testID': identity, 'message': prefix + marker}
                       for marker in contract[markers]]
        return contract, events

    def test_widget_and_repository_require_their_own_exact_shutdown_tail(self):
        contract, events = self.fixture()
        result = check.card_cleanup_summary('\n'.join(map(json.dumps, events)), contract)
        self.assertEqual((result['widget_names'], result['repository_names'],
                          result['model_cases_without_SDK_claim']), (1, 1, 1))

    def test_missing_foreign_or_duplicate_cleanup_cannot_borrow_another_test(self):
        contract, events = self.fixture()
        foreign = copy.deepcopy(events)
        foreign[-1]['testID'] = 99
        duplicate = events + [events[-1]]
        wrong_name = copy.deepcopy(events)
        wrong_name[0]['test']['name'] = 'unrelated successful case'
        for body in [events[:-1], foreign, duplicate, wrong_name]:
            with self.subTest(body=body):
                with self.assertRaises(ValueError):
                    check.card_cleanup_summary('\n'.join(map(json.dumps, body)), contract)



class GroupRevokeInventoryTests(unittest.TestCase):
    def test_same_count_new58_cannot_replace_a_required_parent_or_group_case(self):
        plan = check.source.descriptor()
        names = json.loads(check.source.regular(check.source.REPO / plan['required_test_names']['path']))
        for key in ('parent_full1222', 'new_group_revoke58', 'current_full1280'):
            with self.subTest(key=key):
                changed = copy.deepcopy(names)
                changed[key][0]['name'] = 'forged same-count replacement'
                body = json.dumps(changed).encode()
                altered = copy.deepcopy(plan)
                altered['required_test_names'].update(bytes=len(body), sha256=check.source.sha(body))
                with mock.patch.object(check.source, 'regular', return_value=body):
                    with self.assertRaises(ValueError): check.source.required_names(altered)

    def test_new_source_pin_and_historical_card_lineage_fail_closed(self):
        original = check.source.descriptor()
        changes = [lambda p: p['group_revoke']['selected_files'][0].update(sha256='0' * 64),
                   lambda p: p['chat_card']['current_source_lineage']['replacements'][0]['before'].update(sha256='0' * 64),
                   lambda p: p['chat_card']['current_source_lineage']['replacements'][0]['after'].update(sha256='0' * 64)]
        for change in changes:
            plan = copy.deepcopy(original); change(plan)
            with self.assertRaises(ValueError):
                check.source.group_revoke_contract(plan)
                check.source.card_contract(plan)
        plan = copy.deepcopy(original)
        before = plan['chat_card']['current_source_lineage']['replacements'][0]['before']
        before['sha256'] = '0' * 64
        next(r for r in plan['chat_card']['selected_files'] if r['path'] == before['path'])['sha256'] = '0' * 64
        with self.assertRaises(ValueError): check.source.card_contract(plan)


class GroupRevokeCleanupTests(unittest.TestCase):
    def test_each_group_test_needs_its_own_ordered_shutdown(self):
        contract = {'widget_names': ['owned dialog'], 'repository_names': ['owned repository'],
                    'widget_markers': ['sdk.begin', 'sdk.end', 'unmount.begin', 'unmount.end'],
                    'repository_markers': ['sdk.begin', 'sdk.end', 'storage.begin', 'storage.end']}
        events = []
        for identity, group, prefix, markers in [(1, 'widget_names', 'group_revoke_cleanup ', 'widget_markers'),
                                                 (2, 'repository_names', 'group_revoke_repository_cleanup ', 'repository_markers')]:
            events.append({'type': 'testStart', 'test': {'id': identity, 'name': contract[group][0]}})
            events += [{'type': 'print', 'testID': identity, 'message': prefix + marker} for marker in contract[markers]]
        result = check.group_revoke_cleanup_summary('\n'.join(map(json.dumps, events)), contract)
        self.assertEqual((result['widget_names'], result['repository_names']), (1, 1))
        foreign = copy.deepcopy(events); foreign[-1]['testID'] = 99
        wrong = copy.deepcopy(events); wrong[0]['test']['name'] = 'foreign successful case'
        for altered in (events[:-1], events + [events[-1]], foreign, wrong):
            with self.assertRaises(ValueError):
                check.group_revoke_cleanup_summary('\n'.join(map(json.dumps, altered)), contract)


class RealHomeTests(unittest.TestCase):
    def test_real_account_home_is_not_task_home_and_invalid_home_is_rejected(self):
        import os
        import tempfile
        from types import SimpleNamespace
        actual_home = os.environ.get('HOME')
        with tempfile.TemporaryDirectory() as root:
            home = Path(root).resolve() / 'account'; home.mkdir()
            task = Path(root).resolve() / 'work'; task.mkdir()
            account = SimpleNamespace(pw_uid=home.stat().st_uid, pw_dir=str(home))
            with mock.patch.object(check.pwd, 'getpwnam', return_value=account):
                self.assertEqual(check.run_user_home('runner'), str(home))
                self.assertNotEqual(check.run_user_home('runner'), str(task / 'home'))
            for value in (SimpleNamespace(pw_uid=0, pw_dir=str(home)),
                          SimpleNamespace(pw_uid=os.getuid(), pw_dir='relative/home'),
                          SimpleNamespace(pw_uid=os.getuid() + 1, pw_dir=str(home)),
                          SimpleNamespace(pw_uid=os.getuid(), pw_dir=str(home / '..' / 'account'))):
                with mock.patch.object(check.pwd, 'getpwnam', return_value=value):
                    with self.assertRaises(ValueError): check.run_user_home('runner')
        self.assertEqual(os.environ.get('HOME'), actual_home)
        self.assertIn('HOME="$real_user_home"', (check.REPO / 'ci/mobile-netns.sh').read_text())
        self.assertNotIn('export HOME=', (check.REPO / 'ci/mobile-setup.sh').read_text())


if __name__ == '__main__':
    unittest.main()
