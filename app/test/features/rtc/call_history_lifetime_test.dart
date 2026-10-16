import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/rtc/data/call_records_repository.dart';
import 'package:imapp/features/rtc/domain/call_record.dart';
import 'package:imapp/features/rtc/domain/rtc_call.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

import 'call_records_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('successfully retired owners do not accumulate across repeated real logins and the new pipeline stays writable', () async {
    final f = CallRecordsFixture(); await f.start();
    try {
      f.permissions.granted = false;
      final firstKey = f.sdk.session.state.credentials!.accountKey;
      for (var index = 0; index < 6; index++) {
        await f.rtc.dial(inviteeIds: ['bob'], video: false);
        await f.history.flush();
        final old = f.history.capture();
        await f.sdk.signInOther(user: 'owner-$index', tenant: 'tenant-$index',
          issuer: 'https://issuer-$index.example.com/');
        await f.history.settleRetired().timeout(const Duration(seconds: 5));
        expect(old.current, isFalse);
        final state = f.history.lifecycle;
        expect(state.activeOwners, 1); expect(state.drainingOwners, 0);
        expect(state.retainedCredentialOwners, 1); expect(state.retirementFailed, isFalse);
        expect(await f.rows(), isEmpty);
      }
      await f.rtc.dial(inviteeIds: ['bob'], video: false);
      expect((await f.rows()).single.reason, RtcEndReason.permissionDenied);
      expect(f.transport.joins, 0);
      final original = CallRecordsRepository(accountKey: firstKey, open: f.open);
      try {
        expect((await original.list(filter: CallRecordsFilter.all, current: () => true)).items.single.peerId, 'bob');
      } finally { await original.close(); }
    } finally { await f.close(); }
  });

  test('sealing releases the old credential reference before a blocked original SQLite queue finishes draining', () async {
    final f = CallRecordsFixture(); await f.start();
    final gate = Completer<Database>(); final entered = Completer<void>();
    try {
      final oldKey = f.sdk.session.state.credentials!.accountKey;
      f.overrideOpen = (key) {
        if (key != oldKey) return initializeCallRecordsDatabase(databaseFactoryFfi,
          '${f.directory.path}/$key.db', key);
        if (!entered.isCompleted) entered.complete();
        return gate.future;
      };
      f.permissions.granted = false;
      await f.rtc.dial(inviteeIds: ['bob'], video: false);
      await entered.future.timeout(const Duration(seconds: 5));
      await f.sdk.signInOther(user: 'other');
      expect(f.history.lifecycle.activeOwners, 1);
      expect(f.history.lifecycle.drainingOwners, 1);
      expect(f.history.lifecycle.retainedCredentialOwners, 1);
      var settled = false;
      final retiring = f.history.settleRetired().then((_) { settled = true; });
      expect(settled, isFalse);
      expect(await f.rows(), isEmpty);
      await f.rtc.dial(inviteeIds: ['bob'], video: true);
      gate.complete(await initializeCallRecordsDatabase(databaseFactoryFfi,
        '${f.directory.path}/$oldKey.db', oldKey));
      await retiring.timeout(const Duration(seconds: 5));
      expect(settled, isTrue); expect(f.history.lifecycle.drainingOwners, 0);
      expect(f.history.lifecycle.retainedCredentialOwners, 1);
      final current = (await f.rows()).single;
      expect(current.video, isTrue); expect(current.reason, RtcEndReason.permissionDenied);
    } finally {
      if (!gate.isCompleted) gate.completeError(StateError('release owned blocked database'));
      await f.close();
    }
  });

  test('failed retirement keeps only an observable generic failure while a replacement owner records normally', () async {
    final f = CallRecordsFixture(); await f.start();
    try {
      final oldKey = f.sdk.session.state.credentials!.accountKey;
      f.overrideOpen = (key) {
        if (key == oldKey) return Future.error(StateError('owned original storage failure'));
        return initializeCallRecordsDatabase(databaseFactoryFfi, '${f.directory.path}/$key.db', key);
      };
      f.permissions.granted = false;
      await f.rtc.dial(inviteeIds: ['bob'], video: false);
      await expectLater(f.history.flush(), throwsA(isA<AppFailure>()));
      await f.sdk.signInOther(user: 'other');
      await expectLater(f.history.settleRetired(), throwsA(isA<AppFailure>().having(
        (failure) => failure.kind, 'kind', FailureKind.localStorage)));
      expect(f.history.lifecycle.activeOwners, 1); expect(f.history.lifecycle.drainingOwners, 0);
      expect(f.history.lifecycle.retainedCredentialOwners, 1);
      expect(f.history.lifecycle.retirementFailed, isTrue);
      expect(f.history.failure, isNull);
      await f.rtc.dial(inviteeIds: ['bob'], video: false);
      expect((await f.rows()).single.peerId, 'bob'); expect(f.transport.joins, 0);
    } finally {
      await expectLater(f.close(), throwsA(isA<AppFailure>().having(
        (failure) => failure.kind, 'kind', FailureKind.localStorage)));
    }
  });
}
