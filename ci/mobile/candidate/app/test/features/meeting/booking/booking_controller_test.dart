import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';
import 'package:imapp/features/meeting/booking/data/booking_contract.dart';
import 'package:imapp/features/meeting/booking/presentation/booking_controller.dart';

import 'booking_test_harness.dart';

void main() {
  late BookingFixture f;
  setUp(() { f = BookingFixture(); });
  tearDown(() async => f.close());
  test('unsupported release reads local pending only and never dispatches list or a write', () async {
    await f.open(released: false);
    expect(f.controller.availability, BookingAvailability.unsupported); expect(f.paths, isEmpty);
    expect(f.controller.canCreate, isFalse); expect(await f.controller.detail(bookingOne), isNull);
    expect(f.paths, isEmpty);
  });
  test('lost reply freezes original UUID; actual SQLite receipt recovery does not create again', () async {
    await f.open(); f.loseReply = true;
    expect(await f.controller.submit(f.intent()), isNull); expect(f.controller.canCreate, isFalse);
    expect(f.controller.unknownIDs, [intentOne]); expect(f.paths.where((x) => x == 'create'), hasLength(1));
    final receipt = await f.controller.recover(intentOne);
    expect(receipt?.applied, isTrue); expect(f.controller.unknownIDs, isEmpty);
    expect(f.paths.where((x) => x == 'create'), hasLength(1));
    final local = await f.repository.journal.read(intentOne);
    expect(local?.intent, isNull); expect(local?.receipt?.record?.detailsAvailable, isFalse);
  });
  test('a synchronous second create cannot dispatch while the first actual transport call is pending', () async {
    await f.open(); final entered = Completer<void>(), reply = Completer<Object?>();
    f.intercept = (action, body) async { if (action == 'create') { entered.complete(); return reply.future; } return f.respond(action, body); };
    final first = f.controller.submit(f.intent()); await entered.future;
    expect(await f.controller.submit(f.intent(id: '10000000-0000-4000-8000-000000000002')), isNull);
    expect(f.paths.where((x) => x == 'create'), hasLength(1));
    reply.complete(f.respond('create', f.intent().payload)); await first;
  });
  test('session invalidation after real dispatch clears UI and prevents its late result from returning', () async {
    await f.open(); final entered = Completer<void>(), reply = Completer<Object?>();
    f.intercept = (action, body) async { if (action == 'create') { entered.complete(); return reply.future; } return f.respond(action, body); };
    final first = f.controller.submit(f.intent()); await entered.future;
    f.current = false; f.invalidations.add(2); expect(f.controller.records, isEmpty); expect(f.controller.unknownIDs, isEmpty);
    reply.complete(f.respond('create', f.intent().payload)); expect(await first, isNull);
    expect(f.controller.usable, isFalse);
  });
  test('refresh filter revision drops a real delayed old page and cannot regress current rows', () async {
    await f.open(); final old = Completer<Object?>(), entered = Completer<void>();
    f.intercept = (action, body) async { if (action == 'list' && body['includeCancelled'] == false) { entered.complete(); return old.future; }
      return {'bookings': [f.record(state: 'cancelled', version: 2)], 'hasMore': false}; };
    final first = f.controller.refresh(); await entered.future; await f.controller.refresh(cancelled: true);
    old.complete({'bookings': [f.record()], 'hasMore': false}); await first;
    expect(f.controller.records.single.state, 'cancelled'); expect(f.controller.records.single.version, 2);
  });
  test('unsupported or malformed response does not turn read failure into successful empty or SDK fallback', () async {
    await f.open(); f.intercept = (_, _) async => throw StateError('synthetic unsupported service');
    await f.controller.refresh(); expect(f.controller.availability, BookingAvailability.unavailable);
    expect(f.controller.canWrite, isFalse); expect(f.paths.every((p) => p == 'list'), isTrue);
  });
  test('real SQLite prepare failure is zero HTTP, and explicit recovery persists the same held UUID before querying', () async {
    await f.open();
    final db = await databaseFactoryFfi.openDatabase('${f.directory.path}/journal.sqlite',
      options: OpenDatabaseOptions(singleInstance: false));
    try {
      await db.execute("CREATE TRIGGER reject_synthetic_prepare BEFORE INSERT ON booking_pending_intents BEGIN SELECT RAISE(ABORT,'synthetic disk failure'); END");
      expect(await f.controller.submit(f.intent()), isNull);
      expect(f.paths.where((x) => x == 'create'), isEmpty);
      expect(f.controller.unknownIDs, [intentOne]); expect(f.controller.canCreate, isFalse);
      await db.execute('DROP TRIGGER reject_synthetic_prepare');
    } finally { await db.close(); }
    final queried = await f.controller.recover(intentOne);
    expect(queried?.found, isFalse); expect(f.controller.canCreate, isFalse);
    expect((await f.repository.journal.read(intentOne))?.intent?.frozenJson, f.intent().frozenJson);
    final retried = await f.controller.recover(intentOne, retry: true);
    expect(retried?.applied, isTrue); expect(f.paths.where((x) => x == 'create'), hasLength(1));
    expect(f.requests[f.paths.indexOf('create')]['clientRequestID'], intentOne);
  });
}
