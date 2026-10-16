import 'dart:async';
import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/rtc/data/call_records_repository.dart';
import 'package:imapp/features/rtc/domain/call_record.dart';
import 'package:imapp/features/rtc/domain/rtc_call.dart';
import 'package:imapp/features/rtc/presentation/call_records_providers.dart';
import 'package:imapp/features/rtc/presentation/rtc_providers.dart';
import 'package:imapp/providers.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

import 'call_records_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('actual provider and native signaling codec feed the owner SQLite recorder without opening its page', () async {
    final f = CallRecordsFixture(); await f.start();
    final container = ProviderContainer(overrides: [
      appRuntimeProvider.overrideWith((_) => f.sdk.runtime),
      imGatewayProvider.overrideWithValue(f.sdk.gateway),
      callHistoryRecorderProvider.overrideWithValue(f.history),
    ]);
    try {
      final actual = container.read(rtcCoordinatorProvider);
      expect(identical(actual.history, f.history), isTrue);
      final invitation = {'roomID': 'native-owned-room', 'inviterUserID': 'bob',
        'inviteeUserIDList': ['alice'], 'mediaType': 'audio', 'timeout': 30,
        'initiateTime': DateTime.now().millisecondsSinceEpoch ~/ 1000,
        'platformID': 2, 'sessionType': 1};
      await f.sdk.callback('signalingListener', {'type': 'onReceiveNewInvitation',
        'data': jsonEncode({'userID': 'bob', 'token': 'not-recorded-private', 'invitation': invitation})});
      expect(actual.phase, RtcPhase.incoming);
      await f.sdk.callback('signalingListener', {'type': 'onInvitationCancelled',
        'data': jsonEncode({'userID': 'bob', 'invitation': invitation})});
      final row = (await f.rows(filter: CallRecordsFilter.missed)).single;
      expect(row.peerId, 'bob'); expect(row.cause, CallEndCause.remote);
      expect(jsonEncode(row.toRow()), isNot(contains('not-recorded-private')));
      expect(f.transports.single.joins, 0);
    } finally { try { await f.close(); } finally { container.dispose(); } }
  });

  test('actual outgoing coordinator lifecycle persists a terminal call and monotonic duration across wall clock rollback', () async {
    final f = CallRecordsFixture(); await f.start();
    try {
      await f.connectedOutgoing(); f.tick = 3400000;
      f.now = f.now.subtract(const Duration(minutes: 2));
      await f.rtc.hangUp();
      final row = (await f.rows()).single;
      expect(row.direction, CallDirection.outgoing); expect(row.connected, isTrue);
      expect(row.durationMs, 3400); expect(row.endedAt, greaterThanOrEqualTo(row.admittedAt));
      expect(row.missed, isFalse); expect(f.transport.joins, 1);
      expect(f.transport.endActions, [RtcEndAction.hangUp]);
    } finally { await f.close(); }
  });

  test('only a genuine incoming remote cancellation before acceptance appears in missed', () async {
    final f = CallRecordsFixture(); await f.start();
    try {
      final call = historyInvitation();
      f.transport.events.add(RtcSignal(ImSignalAction.invited, call));
      f.transport.events.add(RtcSignal(ImSignalAction.cancelled, call));
      final row = (await f.rows(filter: CallRecordsFilter.missed)).single;
      expect(row.peerId, 'bob'); expect(row.cause, CallEndCause.remote);
      expect(row.accepted, isFalse); expect(row.label(live: false), '未接来电');
      expect(f.permissions.requests, 0); expect(f.transport.joins, 0);
    } finally { await f.close(); }
  });

  testWidgets('actual incoming ring timer freezes a known missed outcome rather than a cold-start guess', (tester) async {
    final f = CallRecordsFixture(); await tester.runAsync(f.start);
    try {
      f.transport.events.add(RtcSignal(ImSignalAction.invited, historyInvitation()));
      await tester.pump(const Duration(seconds: 31));
      final rows = await tester.runAsync(() => f.rows(filter: CallRecordsFilter.missed));
      expect(rows, hasLength(1)); expect(rows!.single.cause, CallEndCause.timer);
      expect(rows.single.reason, RtcEndReason.timedOut);
    } finally { await tester.runAsync(f.close); }
  });

  test('manual incoming decline is explicit and is never classified as missed', () async {
    final f = CallRecordsFixture(); await f.start();
    try {
      f.transport.events.add(RtcSignal(ImSignalAction.invited, historyInvitation()));
      await f.rtc.hangUp();
      expect((await f.rows()).single.label(live: false), '已拒绝');
      expect(await f.rows(filter: CallRecordsFilter.missed), isEmpty);
      expect(f.transport.endActions, [RtcEndAction.reject]);
    } finally { await f.close(); }
  });

  test('outgoing cancellation and locally denied permissions stay in all without inventing missed calls', () async {
    final f = CallRecordsFixture(); await f.start();
    try {
      f.permissions.granted = false;
      await f.rtc.dial(inviteeIds: ['bob'], video: false);
      final denied = (await f.rows()).single;
      expect(denied.reason, RtcEndReason.permissionDenied);
      expect(denied.signalingStarted, isFalse); expect(f.transport.joins, 0);
      f.permissions.granted = true;
      await f.rtc.dial(inviteeIds: ['bob'], video: true); await f.rtc.hangUp();
      expect(await f.rows(), hasLength(2));
      expect(await f.rows(filter: CallRecordsFilter.missed), isEmpty);
    } finally { await f.close(); }
  });

  test('incoming permission failure and other-device handling are explicit non-missed outcomes', () async {
    final f = CallRecordsFixture(); await f.start();
    try {
      f.permissions.granted = false;
      f.transport.events.add(RtcSignal(ImSignalAction.invited, historyInvitation(room: 'denied')));
      await f.rtc.accept();
      final other = historyInvitation(room: 'other-device');
      f.transport.events.add(RtcSignal(ImSignalAction.invited, other));
      f.transport.events.add(RtcSignal(ImSignalAction.acceptedOnOtherDevice, other));
      expect((await f.rows()).map((r) => r.reason).toSet(),
        {RtcEndReason.otherDevice, RtcEndReason.permissionDenied});
      expect(await f.rows(filter: CallRecordsFilter.missed), isEmpty);
    } finally { await f.close(); }
  });

  test('same-room duplicate invitation does not terminate the active record while a different room records busy separately', () async {
    final f = CallRecordsFixture(); await f.start();
    try {
      final first = historyInvitation(room: 'first');
      f.transport.events.add(RtcSignal(ImSignalAction.invited, first));
      f.transport.events.add(RtcSignal(ImSignalAction.invited, first));
      expect(await f.rows(), hasLength(1)); expect((await f.rows()).single.terminal, isFalse);
      f.transport.events.add(RtcSignal(ImSignalAction.invited, historyInvitation(room: 'second')));
      final rows = await f.rows(); expect(rows, hasLength(2));
      expect(rows.where((r) => r.reason == RtcEndReason.busy), hasLength(1));
      expect(rows.where((r) => !r.terminal), hasLength(1));
      expect(f.rtc.call!.roomId, 'first'); expect(f.transport.endActions, [RtcEndAction.reject]);
      expect(await f.rows(filter: CallRecordsFilter.missed), isEmpty);
      await f.rtc.hangUp();
    } finally { await f.close(); }
  });

  test('accepted incoming media completion and duplicate remote endings create exactly one terminal record', () async {
    final f = CallRecordsFixture(); await f.start();
    try {
      final call = historyInvitation(video: true);
      f.transport.events.add(RtcSignal(ImSignalAction.invited, call));
      await f.rtc.accept(); await f.phase(RtcPhase.active); f.tick = 2000000;
      f.transport.events.add(RtcSignal(ImSignalAction.hungUp, call));
      f.transport.events.add(RtcSignal(ImSignalAction.hungUp, call));
      final rows = await f.rows(); expect(rows, hasLength(1));
      expect(rows.single.video, isTrue); expect(rows.single.durationMs, 2000);
      expect(rows.single.accepted, isTrue); expect(rows.single.cause, CallEndCause.remote);
      expect(await f.rows(filter: CallRecordsFilter.missed), isEmpty);
    } finally { await f.close(); }
  });

  test('transport errors retain an uncertain result instead of fabricating a remote refusal', () async {
    final f = CallRecordsFixture(); await f.start();
    try {
      f.transport.joinFailure = const AppFailure(FailureKind.timeout);
      await f.rtc.dial(inviteeIds: ['bob'], video: false);
      final row = (await f.rows()).single;
      expect(row.reason, RtcEndReason.failed); expect(row.cause, CallEndCause.transport);
      expect(row.label(live: false), '连接失败，结果未确认');
      expect(row.missed, isFalse);
    } finally { await f.close(); }
  });

  test('unaddressed expired and group invitations do not become legacy single-call history', () async {
    final f = CallRecordsFixture(); await f.start();
    try {
      f.transport.events.add(RtcSignal(ImSignalAction.invited,
        historyInvitation(room: 'foreign', invitees: ['carol'])));
      f.transport.events.add(RtcSignal(ImSignalAction.invited,
        historyInvitation(room: 'expired', initiated: DateTime.now().subtract(const Duration(minutes: 5)))));
      expect(await f.rows(), isEmpty);
      f.transport.events.add(RtcSignal(ImSignalAction.invited, historyInvitation(room: 'group', group: 'group-a')));
      await f.rtc.hangUp(); expect(await f.rows(), isEmpty);
    } finally { await f.close(); }
  });

  test('logout seals old events before delayed permissions return and cold provisional remains unknown', () async {
    final f = CallRecordsFixture(); await f.start();
    try {
      final old = f.sdk.session.state.credentials!;
      final permission = Completer<bool>(); f.permissions.pending = permission;
      final dial = f.rtc.dial(inviteeIds: ['bob'], video: false);
      await f.history.flush();
      await f.sdk.signInOther(user: 'other');
      permission.complete(true); await dial;
      expect(await f.rows(), isEmpty); expect(f.transports.first.joins, 0);
      final oldDB = CallRecordsRepository(accountKey: old.accountKey, open: f.open);
      try {
        final row = (await oldDB.list(filter: CallRecordsFilter.all, current: () => true)).items.single;
        expect(row.terminal, isFalse); expect(row.label(live: false), '上次通话结果未确认');
        expect(row.missed, isFalse);
      } finally { await oldDB.close(); }
    } finally { await f.close(); }
  });

  for (final changed in ['actor', 'issuer', 'tenant']) {
    test('stable recorder creates a fresh exact owner binding after $changed login replacement', () async {
      final f = CallRecordsFixture(); await f.start();
      try {
        final oldAccess = f.history.capture();
        f.permissions.granted = false; await f.rtc.dial(inviteeIds: ['bob'], video: false);
        await f.history.flush();
        await f.sdk.signInOther(user: changed == 'actor' ? 'other' : 'alice',
          issuer: changed == 'issuer' ? 'https://other.example.com/' : 'https://im.example.com/',
          tenant: changed == 'tenant' ? 'other-tenant' : 'fixture-tenant');
        expect(oldAccess.current, isFalse); expect(await f.rows(), isEmpty);
        await f.rtc.dial(inviteeIds: ['bob'], video: false);
        expect((await f.rows()).single.reason, RtcEndReason.permissionDenied);
        expect(identical(oldAccess.repository, f.history.capture().repository), isFalse);
      } finally { await f.close(); }
    });
  }

  test('close awaits already frozen SQLite events after owner sealing and does not publish old completion to a new owner', () async {
    final f = CallRecordsFixture(); await f.start();
    final gate = Completer<Database>(); final entered = Completer<void>();
    try {
      final old = f.sdk.session.state.credentials!;
      f.overrideOpen = (key) {
        if (key != old.accountKey) return initializeCallRecordsDatabase(databaseFactoryFfi,
          '${f.directory.path}/$key.db', key);
        if (!entered.isCompleted) entered.complete();
        return gate.future;
      };
      f.permissions.granted = false; await f.rtc.dial(inviteeIds: ['bob'], video: false);
      await entered.future.timeout(const Duration(seconds: 5));
      await f.sdk.signInOther(user: 'other');
      final newAccess = f.history.capture();
      expect((await newAccess.repository.list(filter: CallRecordsFilter.all,
        current: () => newAccess.current)).items, isEmpty);
      var notifications = 0;
      void changed() { notifications++; }
      f.history.addListener(changed);
      var closed = false;
      final closing = f.history.close().then((_) { closed = true; });
      final afterSeal = notifications;
      expect(closed, isFalse); expect(newAccess.current, isFalse);
      final db = await initializeCallRecordsDatabase(databaseFactoryFfi,
        '${f.directory.path}/${old.accountKey}.db', old.accountKey);
      gate.complete(db); await closing.timeout(const Duration(seconds: 5));
      expect(closed, isTrue);
      expect(notifications, afterSeal);
      f.history.removeListener(changed);
      final oldDB = CallRecordsRepository(accountKey: old.accountKey,
        open: (key) => initializeCallRecordsDatabase(databaseFactoryFfi, '${f.directory.path}/$key.db', key));
      try {
        final saved = (await oldDB.list(filter: CallRecordsFilter.all, current: () => true)).items.single;
        expect(saved.reason, RtcEndReason.permissionDenied);
        expect(saved.peerId, 'bob');
      } finally { await oldDB.close(); }
    } finally {
      if (!gate.isCompleted) gate.completeError(StateError('release owned test open'));
      await f.close();
    }
  });

  test('persistence retry reuses frozen local events without repeating native dialing', () async {
    final f = CallRecordsFixture(); await f.start();
    try {
      var fail = true;
      f.overrideOpen = (key) {
        if (fail) throw StateError('owned synthetic storage failure');
        return initializeCallRecordsDatabase(databaseFactoryFfi, '${f.directory.path}/$key.db', key);
      };
      f.permissions.granted = false; await f.rtc.dial(inviteeIds: ['bob'], video: false);
      await expectLater(f.history.flush(), throwsA(isA<AppFailure>()));
      expect(f.history.failure, isNotNull);
      fail = false; await f.history.retryPersistence();
      expect((await f.rows()).single.reason, RtcEndReason.permissionDenied);
      expect(f.permissions.requests, 1); expect(f.transport.joins, 0);
      expect(f.history.failure, isNull);
    } finally { await f.close(); }
  });

  test('an undrained storage failure is observable from awaited close rather than an unhandled provider future', () async {
    final f = CallRecordsFixture(); await f.start();
    f.overrideOpen = (_) => Future.error(StateError('owned close failure'));
    f.permissions.granted = false; await f.rtc.dial(inviteeIds: ['bob'], video: false);
    await expectLater(f.history.close(), throwsA(isA<AppFailure>().having((e) => e.kind, 'kind', FailureKind.localStorage)));
    await expectLater(f.close(), throwsA(isA<AppFailure>()));
  });
}
