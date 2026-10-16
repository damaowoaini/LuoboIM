import 'dart:async';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/rtc/data/call_records_repository.dart';
import 'package:imapp/features/rtc/domain/call_record.dart';
import 'package:imapp/features/rtc/domain/rtc_call.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

import 'call_records_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  sqfliteFfiInit();
  late Directory directory;
  late CallRecordsRepository repository;
  final others = <CallRecordsRepository>[];
  const owner = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
  bool current() => true;
  Future<Database> open(String key) => initializeCallRecordsDatabase(
    databaseFactoryFfi, '${directory.path}/$key.db', key);
  CallRecord record(String room, {String peer = 'bob', DateTime? initiated}) =>
    CallRecord.admit(historyInvitation(room: room, inviter: peer,
      initiated: initiated ?? DateTime.utc(2026, 10, 16)), selfId: 'alice',
      title: '同名好友', now: DateTime.utc(2026, 10, 16));
  Future<List<CallRecord>> rows({CallRecordsFilter filter = CallRecordsFilter.all}) async =>
    (await repository.list(filter: filter, current: current)).items;
  setUp(() async {
    directory = await Directory.systemTemp.createTemp('luobo-call-db-');
    repository = CallRecordsRepository(accountKey: owner, open: open);
  });
  tearDown(() async {
    await repository.close();
    for (final other in others) { await other.close(); }
    others.clear();
    await directory.delete(recursive: true);
  });

  test('attempt connected and terminal events survive a real SQLite close and reopen', () async {
    final attempt = record('durable');
    await repository.apply(owner, attempt);
    final connected = attempt.event(accepted: true, connected: true, signalingStarted: true);
    await repository.apply(owner, connected);
    await repository.apply(owner, connected.event(terminal: true, reason: RtcEndReason.hungUp,
      cause: CallEndCause.local, durationMs: 3400, endedAt: attempt.admittedAt + 3400));
    await repository.close();
    repository = CallRecordsRepository(accountKey: owner, open: open);
    final saved = (await rows()).single;
    expect(saved.peerId, 'bob'); expect(saved.name, '同名好友');
    expect(saved.terminal, isTrue); expect(saved.connected, isTrue);
    expect(saved.durationMs, 3400); expect(saved.missed, isFalse);
  });

  test('durable delete tombstone blocks already queued connected and terminal events after reopen', () async {
    final attempt = record('delete');
    await repository.apply(owner, attempt);
    final late = Completer<void>();
    final connected = attempt.event(accepted: true, connected: true);
    final queued = late.future.then((_) async {
      await repository.apply(owner, connected);
      await repository.apply(owner, connected.event(terminal: true, reason: RtcEndReason.hungUp,
        cause: CallEndCause.remote, endedAt: attempt.admittedAt + 100));
    });
    final exact = (await rows()).single;
    expect(await repository.delete(exact, current: current), isTrue);
    late.complete(); await queued;
    expect(await rows(), isEmpty);
    await repository.close();
    repository = CallRecordsRepository(accountKey: owner, open: open);
    await repository.apply(owner, connected.event());
    expect(await rows(), isEmpty);
    final db = await open(owner);
    expect((await db.query('call_claims')).single['deleted'], 1);
    expect(await db.query('call_records'), isEmpty);
    await db.close();
  });

  test('first terminal rejects both older and forged higher connected revisions', () async {
    final attempt = record('terminal');
    final connected = attempt.event(accepted: true, connected: true);
    final terminal = connected.event(terminal: true, reason: RtcEndReason.hungUp,
      cause: CallEndCause.remote, endedAt: attempt.admittedAt + 1);
    await repository.apply(owner, attempt); await repository.apply(owner, terminal);
    await repository.apply(owner, connected); await repository.apply(owner, connected.event().event());
    final value = (await rows()).single;
    expect(value.terminal, isTrue); expect(value.reason, RtcEndReason.hungUp);
    expect(value.revision, terminal.revision);
  });

  test('same room with a conflicting inviter cannot create a second history row', () async {
    await repository.apply(owner, record('claim'));
    await expectLater(repository.apply(owner, record('claim', peer: 'carol')),
      throwsA(isA<AppFailure>().having((e) => e.kind, 'kind', FailureKind.dataChanged)));
    expect((await rows()).single.peerId, 'bob');
  });

  test('remote initiation time is part of an immutable claim rather than a second room identity', () async {
    await repository.apply(owner, record('time-claim'));
    await expectLater(repository.apply(owner, record('time-claim', initiated: DateTime.utc(2026, 10, 17))), throwsA(isA<AppFailure>()));
    expect(await rows(), hasLength(1));
  });

  test('two account SQLite files with the same peer and room remain separate', () async {
    final other = CallRecordsRepository(accountKey: 'b' * 64, open: open); others.add(other);
    await repository.apply(owner, record('same'));
    expect((await other.list(filter: CallRecordsFilter.all, current: current)).items, isEmpty);
    await expectLater(other.apply(owner, record('same')), throwsA(isA<AppFailure>()));
    await other.apply('b' * 64, record('same', peer: 'carol'));
    expect((await rows()).single.peerId, 'bob');
    expect((await other.list(filter: CallRecordsFilter.all, current: current)).items.single.peerId, 'carol');
  });

  test('stable admission sequence pages all records without relying on end dates', () async {
    for (final room in ['one', 'two', 'three']) { await repository.apply(owner, record(room)); }
    final first = await repository.list(filter: CallRecordsFilter.all, current: current, limit: 1);
    final second = await repository.list(filter: CallRecordsFilter.all, current: current, limit: 1, cursor: first.next);
    final third = await repository.list(filter: CallRecordsFilter.all, current: current, limit: 1, cursor: second.next);
    expect([first.items.single.id, second.items.single.id, third.items.single.id],
      [record('three').id, record('two').id, record('one').id]);
    expect(third.next, isNull);
  });

  test('a provisional to missed transition invalidates an old filter cursor instead of losing a page', () async {
    final provisional = record('changing'); await repository.apply(owner, provisional);
    await repository.apply(owner, record('second'));
    final first = await repository.list(filter: CallRecordsFilter.all, current: current, limit: 1);
    await repository.apply(owner, provisional.event(terminal: true, reason: RtcEndReason.timedOut,
      cause: CallEndCause.timer, endedAt: provisional.admittedAt + 30000));
    await expectLater(repository.list(filter: CallRecordsFilter.all, current: current, cursor: first.next, limit: 1),
      throwsA(isA<AppFailure>().having((e) => e.kind, 'kind', FailureKind.dataChanged)));
    expect((await rows(filter: CallRecordsFilter.missed)).single.id, provisional.id);
  });

  test('insert and delete changes reject existing pagination cursors', () async {
    await repository.apply(owner, record('one')); await repository.apply(owner, record('two'));
    final page = await repository.list(filter: CallRecordsFilter.all, current: current, limit: 1);
    await repository.delete(page.items.single, current: current);
    await expectLater(repository.list(filter: CallRecordsFilter.all, current: current, cursor: page.next), throwsA(isA<AppFailure>()));
    await repository.apply(owner, record('three'));
    expect((await rows()).map((r) => r.id), [record('three').id, record('one').id]);
  });

  test('cursor ownership filter and limits are validated before reading the database', () async {
    await expectLater(repository.list(filter: CallRecordsFilter.all, current: current,
      cursor: CallRecordsCursor('b' * 64, CallRecordsFilter.all, 0, 1, 1)), throwsA(isA<AppFailure>()));
    await expectLater(repository.list(filter: CallRecordsFilter.all, current: current,
      cursor: const CallRecordsCursor(owner, CallRecordsFilter.missed, 0, 1, 1)), throwsA(isA<AppFailure>()));
    await expectLater(repository.list(filter: CallRecordsFilter.all, current: current, limit: 0), throwsA(isA<AppFailure>()));
    await expectLater(repository.list(filter: CallRecordsFilter.all, current: current, limit: 101), throwsA(isA<AppFailure>()));
  });

  test('a retired UI delete transaction rolls back and preserves the visible record', () async {
    await repository.apply(owner, record('keep'));
    final saved = (await rows()).single;
    var checks = 0;
    await expectLater(repository.delete(saved, current: () => ++checks < 4), throwsA(isA<AppFailure>()));
    expect((await rows()).single.id, saved.id);
  });

  test('read failure is an explicit storage error and a later explicit retry can recover', () async {
    var fail = true;
    await repository.close();
    repository = CallRecordsRepository(accountKey: owner, open: (key) {
      if (fail) throw StateError('synthetic owned open failure');
      return open(key);
    });
    await expectLater(rows(), throwsA(isA<AppFailure>().having((e) => e.kind, 'kind', FailureKind.localStorage)));
    fail = false; expect(await rows(), isEmpty);
  });

  test('a reopened writer offsets revisions and keeps known connection facts and original sequence', () async {
    final attempt = record('restored');
    await repository.apply(owner, attempt);
    await repository.apply(owner, attempt.event(accepted: true, connected: true));
    final previous = (await rows()).single;
    await repository.close(); repository = CallRecordsRepository(accountKey: owner, open: open);
    await repository.apply(owner, attempt);
    final restored = (await rows()).single;
    expect(restored.revision, greaterThan(previous.revision));
    expect(restored.sequence, previous.sequence); expect(restored.admittedAt, previous.admittedAt);
    expect(restored.connected, isTrue); expect(restored.accepted, isTrue);
    expect(restored.label(live: false), '上次通话结果未确认');
  });

  test('safe avatar storage drops signed credentials and rejects excessive identity data', () async {
    final value = record('avatar');
    final raw = value.toRow()..['avatar'] = 'https://example.com/image?token=synthetic';
    final decoded = CallRecord.fromRow({...raw, 'sequence': 0});
    await repository.apply(owner, decoded);
    expect((await rows()).single.avatar, isNull);
    expect(() => record('bad', peer: 'x' * 129), throwsA(isA<AppFailure>()));
    expect(() => record('x' * 513), throwsA(isA<AppFailure>()));
    expect(CallRecord.safeAvatar('https://example.com/public.png'), 'https://example.com/public.png');
    expect(CallRecord.safeAvatar('https://user:secret@example.com/a'), isNull);
  });

  test('database ownership metadata refuses opening another account file under a new account key', () async {
    await repository.apply(owner, record('owned')); await repository.close();
    final wrong = CallRecordsRepository(accountKey: 'b' * 64,
      open: (key) => initializeCallRecordsDatabase(databaseFactoryFfi, '${directory.path}/$owner.db', key));
    others.add(wrong);
    await expectLater(wrong.list(filter: CallRecordsFilter.all, current: current), throwsA(isA<AppFailure>()));
  });
}
