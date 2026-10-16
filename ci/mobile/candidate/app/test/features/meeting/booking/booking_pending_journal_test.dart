// Source-only until the parent grants a Dart window. Uses the existing locked
// Flutter test/sqflite_common_ffi/crypto packages, no new dependency or service.
import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

// Candidate-only: keep tests outside the current App package until activation.
// Future App integration must use its real package:imapp import paths.
import 'package:imapp/features/meeting/booking/data/booking_contract.dart';
// Candidate-only boundary; no production file or diagnostic is excluded.
import 'package:imapp/features/meeting/booking/data/booking_pending_journal.dart';
// Candidate-only boundary; replace with package:imapp when integrated.
import 'package:imapp/features/meeting/booking/data/meeting_booking_repository.dart';

const requestA = '10000000-0000-4000-8000-000000000001';
const requestB = '10000000-0000-4000-8000-000000000002';
const bookingA = '20000000-0000-4000-8000-000000000001';

BookingIntent intent({
  String id = requestA,
  String name = 'private synthetic booking',
}) => BookingIntent(
  id: id,
  operation: BookingOperation.create,
  frozenBody: {
    'clientRequestID': id,
    'meetingName': name,
    'startTime': 2100000000,
    'meetingDuration': 300,
    'inviteeUserIDList': <String>['peer'],
    'ex': 'synthetic private Ex',
  },
);

final class FixtureTransport implements BookingTransport {
  FixtureTransport(this.server, {this.owner = 'owner'})
    : client = HttpClient()..connectionTimeout = const Duration(seconds: 2);
  final FixtureServer server;
  final String owner;
  final HttpClient client;
  Future<void> Function(String, Map<String, Object?>)? beforePost;
  @override
  Future<Object?> post(String path, Map<String, Object?> body) async {
    if (path.startsWith('/') || !path.startsWith('signal/meeting_booking/')) {
      throw StateError('invalid relative path');
    }
    await beforePost?.call(path, body);
    final request = await client.postUrl(server.base.resolve(path));
    request.headers.contentType = ContentType.json;
    request.headers.set(
      'x-fixture-owner',
      owner,
    ); // Synthetic fixture, no auth claim.
    request.write(jsonEncode(body));
    final response = await request.close().timeout(const Duration(seconds: 2));
    final raw = await response
        .transform(utf8.decoder)
        .join()
        .timeout(const Duration(seconds: 2));
    if (response.statusCode != 200) {
      throw const HttpException('synthetic HTTP error');
    }
    final map = bookingMap(jsonDecode(raw));
    if (map['errCode'] != 0) {
      throw const HttpException('synthetic business error');
    }
    return map['data'];
  }

  void close() => client.close(force: true);
}

final class FixtureServer {
  late final HttpServer http;
  late final StreamSubscription<HttpRequest> subscription;
  Uri get base => Uri.parse('http://127.0.0.1:${http.port}/api/');
  final paths = <String>[];
  final bodies = <Map<String, Object?>>[];
  final receipts = <String, Map<String, Object?>>{};
  bool dropNextWrite = false;
  int? nextStatus;
  Map<String, Object?>? nextData;
  Completer<void>? enteredWrite;
  Completer<void>? releaseWrite;

  Future<void> start() async {
    http = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    subscription = http.listen((request) {
      unawaited(handle(request));
    });
  }

  Map<String, Object?> absent(String id) => {
    'clientRequestID': id,
    'found': false,
    'applied': false,
    'definitiveNotApplied': false,
    'replaySafe': true,
  };
  Map<String, Object?> applied(
    String id, {
    String action = 'create',
    String target = bookingA,
    int version = 1,
    String state = 'scheduled',
  }) => {
    'clientRequestID': id,
    'found': true,
    'applied': true,
    'definitiveNotApplied': false,
    'replaySafe': true,
    'action': action,
    'appliedVersion': version,
    'booking': {
      'bookingID': target,
      'hostUserID': 'owner',
      'version': version,
      'state': state,
      'mediaReady': false,
      'payload': state == 'cancelled'
          ? null
          : {
              'meetingName': 'private synthetic booking',
              'ex': 'synthetic private Ex',
            },
    },
  };
  Future<void> handle(HttpRequest request) async {
    try {
      final body = bookingMap(
        jsonDecode(
          await request.cast<List<int>>().transform(utf8.decoder).join(),
        ),
      );
      final action = request.uri.path.split('/').last;
      final id = body['clientRequestID'] as String;
      paths.add(action);
      bodies.add(bookingMap(jsonDecode(jsonEncode(body))));
      final key = '${request.headers.value('x-fixture-owner')}:$id';
      Map<String, Object?> data;
      if (nextData != null) {
        data = nextData!;
        nextData = null;
      } else if (action == 'receipt') {
        data = receipts[key] ?? absent(id);
      } else if (action == 'resolve') {
        data = receipts.putIfAbsent(
          key,
          () => {
            'clientRequestID': id,
            'found': true,
            'applied': false,
            'definitiveNotApplied': true,
            'replaySafe': true,
            'action': 'resolve',
          },
        );
      } else {
        data = receipts.putIfAbsent(
          key,
          () => applied(
            id,
            action: action,
            target: body['bookingID'] as String? ?? bookingA,
          ),
        );
        enteredWrite?.complete();
        enteredWrite = null;
        if (releaseWrite != null) {
          await releaseWrite!.future;
        }
        if (dropNextWrite) {
          dropNextWrite = false;
          await request.response.close();
          return;
        }
      }
      request.response.statusCode = nextStatus ?? 200;
      nextStatus = null;
      request.response.headers.contentType = ContentType.json;
      request.response.write(
        jsonEncode({'errCode': 0, 'errMsg': '', 'data': data}),
      );
      await request.response.close();
    } catch (_) {
      // Test-server failures become an explicit transport failure, no raw dump.
      request.response.statusCode = 500;
      await request.response.close();
    }
  }

  Future<void> close() async {
    if (releaseWrite != null && !releaseWrite!.isCompleted) {
      releaseWrite!.complete();
    }
    await http.close(force: true);
    await subscription.cancel();
  }
}

final class Fixture {
  late final Directory directory;
  late final FixtureServer server;
  final journals = <BookingPendingJournal>[];
  final transports = <FixtureTransport>[];
  String get path => '${directory.path}/booking.sqlite';
  Future<void> start() async {
    directory = await Directory.systemTemp.createTemp('luobo_booking_journal_');
    server = FixtureServer();
    await server.start();
  }

  Future<MeetingBookingRepository> open({
    String owner = 'owner',
    String issuer = 'issuer-a',
    void Function()? guard,
  }) async {
    final key = bookingDigest(jsonEncode([issuer, 'tenant', owner]));
    final scope = BookingJournalScope(
      accountKey: key,
      issuer: issuer,
      tenantId: 'tenant',
      owner: owner,
      checkCurrent: guard ?? () {},
    );
    final journal = await BookingPendingJournal.open(
      databaseFactoryFfi,
      path,
      scope,
    );
    journals.add(journal);
    final transport = FixtureTransport(server, owner: owner);
    transports.add(transport);
    return MeetingBookingRepository(transport: transport, journal: journal);
  }

  Future<Database> raw() => databaseFactoryFfi.openDatabase(
    path,
    options: OpenDatabaseOptions(singleInstance: false),
  );
  Future<Map<String, Object?>?> row(
    MeetingBookingRepository repo,
    String id,
  ) async {
    final db = await raw();
    try {
      final rows = await db.query(
        'booking_pending_intents',
        where: 'account_key=? AND request_id=?',
        whereArgs: [repo.scope.accountKey, id],
      );
      return rows.isEmpty ? null : rows.single;
    } finally {
      await db.close();
    }
  }

  Future<void> close() async {
    for (final t in transports) {
      t.close();
    }
    await server.close();
    for (final j in journals) {
      await j.close();
    }
    await directory.delete(
      recursive: true,
    ); // Cleanup failures are test failures.
  }
}

void main() {
  sqfliteFfiInit();
  late Fixture f;
  setUp(() async {
    f = Fixture();
    await f.start();
  });
  tearDown(() async {
    await f.close();
  });

  test(
    'SQLite commit with frozen body precedes the first actual HTTP request',
    () async {
      final repo = await f.open();
      var checked = false;
      (repo.transport as FixtureTransport).beforePost = (path, body) async {
        final row = (await f.row(repo, requestA))!;
        expect(row['state'], 'unknown');
        expect(row['payload_json'], intent().frozenJson);
        expect(row['payload_hash'], intent().digest);
        expect(row['claim_id'], isNotNull);
        checked = true;
      };
      final result = await repo.submit(intent());
      expect(checked, isTrue);
      expect(result.applied, isTrue);
      expect(f.server.paths, ['create']);
    },
  );

  test('scope accountKey must bind the exact issuer tenant and user before DB access', () async {
    final key = bookingDigest(jsonEncode(['issuer-a', 'tenant', 'owner']));
    expect(
      () => BookingJournalScope(
        accountKey: key,
        issuer: 'issuer-b',
        tenantId: 'tenant',
        owner: 'owner',
        checkCurrent: () {},
      ),
      throwsA(isA<BookingLocalFailure>()),
    );
    expect(
      () => BookingJournalScope(
        accountKey: key,
        issuer: 'issuer-a',
        tenantId: 'other',
        owner: 'owner',
        checkCurrent: () {},
      ),
      throwsA(isA<BookingLocalFailure>()),
    );
    expect(
      () => BookingJournalScope(
        accountKey: key,
        issuer: 'issuer-a',
        tenantId: 'tenant',
        owner: 'other',
        checkCurrent: () {},
      ),
      throwsA(isA<BookingLocalFailure>()),
    );
    expect(await File(f.path).exists(), isFalse);
    expect(f.server.paths, isEmpty);
  });

  test('process restart after prepared uses receipt first and never automatically resends', () async {
    final old = await f.open();
    await old.journal.prepare(intent());
    await old.journal.close();
    final next = await f.open();
    final result = await next.recover(requestA);
    expect(result.found, isFalse);
    expect(f.server.paths, ['receipt']);
    final row = (await f.row(next, requestA))!;
    expect(row['state'], 'unknown');
    expect(row['payload_json'], isNotNull);
  });

  test('lost HTTP ACK after server commit recovers after closing and reopening SQLite', () async {
    final old = await f.open();
    f.server.dropNextWrite = true;
    await expectLater(old.submit(intent()), throwsA(isA<BookingUnconfirmed>()));
    await old.journal.close();
    final next = await f.open();
    final result = await next.recover(requestA);
    expect(result.applied, isTrue);
    expect(f.server.paths, ['create', 'receipt']);
    expect(f.server.receipts.length, 1);
    expect((await f.row(next, requestA))!['payload_json'], isNull);
  });

  test('restart with an inflight claim still resolves missing receipt before explicit same-byte retry', () async {
    final old = await f.open();
    await old.journal.prepare(intent());
    await old.journal.claim(requestA, 'submit');
    await old.journal.close();
    final next = await f.open();
    await next.retryFrozen(requestA);
    expect(f.server.paths, ['receipt', 'create']);
    expect(f.server.bodies.last['clientRequestID'], requestA);
    expect(bookingCanonical(f.server.bodies.last), intent().frozenJson);
  });

  test(
    'mutating original form arrays cannot change durable retry bytes',
    () async {
      final repo = await f.open();
      final body = intent().payload;
      final frozen = BookingIntent(
        id: requestA,
        operation: BookingOperation.create,
        frozenBody: body,
      );
      await repo.journal.prepare(frozen);
      (body['inviteeUserIDList'] as List<Object?>).add('different');
      body['meetingName'] = 'changed';
      await repo.retryFrozen(requestA);
      expect(bookingCanonical(f.server.bodies.last), frozen.frozenJson);
    },
  );

  test('same UUID with different content is rejected before HTTP and never overwrites a draft', () async {
    final repo = await f.open();
    await repo.journal.prepare(intent());
    await expectLater(
      repo.submit(intent(name: 'different')),
      throwsA(isA<BookingLocalFailure>()),
    );
    expect(f.server.paths, isEmpty);
    expect((await f.row(repo, requestA))!['payload_json'], intent().frozenJson);
  });

  test(
    'concurrent submit sends one HTTP write and creates one durable row',
    () async {
      final repo = await f.open();
      f.server.enteredWrite = Completer<void>();
      final entered = f.server.enteredWrite!.future;
      f.server.releaseWrite = Completer<void>();
      final first = repo.submit(intent());
      await entered;
      await expectLater(
        repo.submit(intent()),
        throwsA(isA<BookingUnconfirmed>()),
      );
      f.server.releaseWrite!.complete();
      await first;
      expect(f.server.paths, ['create']);
      final db = await f.raw();
      try {
        expect((await db.query('booking_pending_intents')).length, 1);
      } finally {
        await db.close();
      }
    },
  );

  test(
    'active write cannot be replaced by concurrent resolve or retry',
    () async {
      final repo = await f.open();
      f.server.enteredWrite = Completer<void>();
      final entered = f.server.enteredWrite!.future;
      f.server.releaseWrite = Completer<void>();
      final first = repo.submit(intent());
      await entered;
      await expectLater(
        repo.resolveUnknown(requestA),
        throwsA(isA<BookingLocalFailure>()),
      );
      await expectLater(
        repo.retryFrozen(requestA),
        throwsA(isA<BookingLocalFailure>()),
      );
      f.server.releaseWrite!.complete();
      await first;
      expect(f.server.paths, ['create']);
    },
  );

  test(
    'new same-account epoch rejects late ACK and does not clear another intent',
    () async {
      final old = await f.open();
      f.server.enteredWrite = Completer<void>();
      final entered = f.server.enteredWrite!.future;
      f.server.releaseWrite = Completer<void>();
      final first = expectLater(
        old.submit(intent()),
        throwsA(isA<BookingUnconfirmed>()),
      );
      await entered;
      final next = await f.open();
      await next.journal.prepare(intent(id: requestB));
      await next.recover(requestA);
      f.server.releaseWrite!.complete();
      await first;
      expect((await next.journal.read(requestA))!.terminal, isTrue);
      expect((await next.journal.read(requestB))!.state, 'prepared');
      await expectLater(
        old.journal.read(requestA),
        throwsA(isA<BookingLocalFailure>()),
      );
    },
  );

  test(
    'issuer and accountKey isolate identical user IDs and request UUIDs',
    () async {
      final a = await f.open();
      await a.journal.prepare(intent());
      final b = await f.open(issuer: 'issuer-b');
      expect(await b.journal.read(requestA), isNull);
      await b.journal.prepare(intent(name: 'separate tenant'));
      expect(
        (await a.journal.read(requestA))!.intent!.payload['meetingName'],
        'private synthetic booking',
      );
      expect(
        (await b.journal.read(requestA))!.intent!.payload['meetingName'],
        'separate tenant',
      );
    },
  );

  test(
    'old session check stops every continuation before accessing another owner',
    () async {
      var current = true;
      final old = await f.open(
        guard: () {
          if (!current) {
            throw const BookingLocalFailure(
              BookingLocalFailureKind.staleSession,
            );
          }
        },
      );
      await old.journal.prepare(intent());
      current = false;
      final next = await f.open(owner: 'new-owner');
      await next.journal.prepare(intent(id: requestB));
      await expectLater(
        old.recover(requestA),
        throwsA(isA<BookingLocalFailure>()),
      );
      expect((await next.journal.read(requestB))!.state, 'prepared');
      expect(f.server.paths, isEmpty);
    },
  );

  test('failed resolve retains original intent and private draft until authoritative proof', () async {
    final repo = await f.open();
    await repo.journal.prepare(intent());
    f.server.nextStatus = 503;
    await expectLater(
      repo.resolveUnknown(requestA),
      throwsA(isA<BookingUnconfirmed>()),
    );
    final row = (await f.row(repo, requestA))!;
    expect(row['state'], 'unknown');
    expect(row['payload_json'], intent().frozenJson);
    expect(row['receipt_json'], isNull);
  });

  test(
    'resolve found false is unconfirmed rather than local cancellation',
    () async {
      final repo = await f.open();
      await repo.journal.prepare(intent());
      f.server.nextData = f.server.absent(requestA);
      await expectLater(
        repo.resolveUnknown(requestA),
        throwsA(isA<BookingUnconfirmed>()),
      );
      expect((await repo.journal.read(requestA))!.terminal, isFalse);
    },
  );

  test('resolve an applied create does not implicitly cancel the scheduled booking', () async {
    final repo = await f.open();
    await repo.journal.prepare(intent());
    f.server.receipts['owner:$requestA'] = f.server.applied(requestA);
    final result = await repo.resolveUnknown(requestA);
    expect(result.applied, isTrue);
    expect(result.record!.state, 'scheduled');
    expect(f.server.paths, ['resolve']);
    expect((await f.row(repo, requestA))!['payload_json'], isNull);
  });

  test('definitive resolve tombstone survives restart and prevents late same-UUID submit', () async {
    final old = await f.open();
    await old.journal.prepare(intent());
    expect((await old.resolveUnknown(requestA)).definitiveNotApplied, isTrue);
    await old.journal.close();
    final next = await f.open();
    final result = await next.submit(intent());
    expect(result.definitiveNotApplied, isTrue);
    expect(f.server.paths, ['resolve']);
    expect((await f.row(next, requestA))!['payload_json'], isNull);
  });

  test(
    'corrupt journal records fail closed without deleting or dispatching',
    () async {
      final repo = await f.open();
      await repo.journal.prepare(intent());
      final db = await f.raw();
      try {
        for (final patch in <Map<String, Object?>>[
          {'state': 'surprise'},
          {'payload_hash': List.filled(64, '0').join()},
          {'payload_json': '{bad'},
          {'operation': 'resolve'},
          {'claim_id': requestB},
          {'state': 'confirmed'},
        ]) {
          final before = (await f.row(repo, requestA))!;
          await db.update(
            'booking_pending_intents',
            patch,
            where: 'account_key=? AND request_id=?',
            whereArgs: [repo.scope.accountKey, requestA],
          );
          await expectLater(
            repo.recover(requestA),
            throwsA(isA<BookingLocalFailure>()),
          );
          expect(await f.row(repo, requestA), isNotNull);
          expect(f.server.paths, isEmpty);
          await db.update(
            'booking_pending_intents',
            before,
            where: 'account_key=? AND request_id=?',
            whereArgs: [repo.scope.accountKey, requestA],
          );
        }
      } finally {
        await db.close();
      }
    },
  );

  test(
    'mismatched action and update target receipts do not finalize a draft',
    () async {
      final repo = await f.open();
      final update = BookingIntent(
        id: requestA,
        operation: BookingOperation.update,
        frozenBody: {
          ...intent().payload,
          'bookingID': bookingA,
          'expectedVersion': 1,
        },
      );
      await repo.journal.prepare(update);
      for (final wrong in [
        f.server.applied(requestA, action: 'create'),
        f.server.applied(requestA, action: 'update', target: requestB),
      ]) {
        f.server.nextData = wrong;
        await expectLater(
          repo.recover(requestA),
          throwsA(isA<BookingUnconfirmed>()),
        );
        expect((await f.row(repo, requestA))!['payload_json'], isNotNull);
      }
    },
  );

  test('confirmed records retain minimum proof and erase draft content from logical rows', () async {
    final repo = await f.open();
    await repo.submit(intent());
    final row = (await f.row(repo, requestA))!;
    expect(row['payload_json'], isNull);
    expect(row['claim_id'], isNull);
    final serialized = jsonEncode(row);
    expect(serialized, isNot(contains('private synthetic booking')));
    expect(serialized, isNot(contains('synthetic private Ex')));
    final receipt = (await repo.journal.read(requestA))!.receipt!;
    expect(receipt.record!.payload, isNull);
    expect(receipt.record!.detailsAvailable, isFalse);
  });

  test('persistent cancellation version floor blocks a stale scheduled receipt after restart', () async {
    final old = await f.open();
    await old.journal.observe([
      const BookingRecord(bookingA, 'owner', 'cancelled', 3, null),
    ]);
    await old.journal.close();
    final next = await f.open();
    await next.journal.prepare(intent());
    f.server.nextData = f.server.applied(requestA, version: 1);
    await expectLater(
      next.recover(requestA),
      throwsA(isA<BookingUnconfirmed>()),
    );
    expect((await next.journal.read(requestA))!.terminal, isFalse);
  });

  test('SQLite prepare failure never dispatches and does not invent a replacement UUID', () async {
    final repo = await f.open();
    final db = await f.raw();
    try {
      await db.execute(
        "CREATE TRIGGER synthetic_insert_failure BEFORE INSERT ON booking_pending_intents BEGIN SELECT RAISE(ABORT,'synthetic'); END",
      );
    } finally {
      await db.close();
    }
    await expectLater(
      repo.submit(intent()),
      throwsA(isA<BookingLocalFailure>()),
    );
    expect(f.server.paths, isEmpty);
    expect(await f.row(repo, requestA), isNull);
  });

  test('session invalidation after SQL mutation rolls the entire local transaction back', () async {
    var checks = 0;
    var failAt = 0;
    final repo = await f.open(
      guard: () {
        checks++;
        if (failAt != 0 && checks >= failAt) {
          throw const BookingLocalFailure(BookingLocalFailureKind.staleSession);
        }
      },
    );
    checks = 0;
    failAt = 4;
    await expectLater(
      repo.submit(intent()),
      throwsA(isA<BookingLocalFailure>()),
    );
    failAt = 0;
    expect(await f.row(repo, requestA), isNull);
    expect(f.server.paths, isEmpty);
  });

  test(
    'closing an old handle cannot erase or deactivate a newer account binding',
    () async {
      final old = await f.open();
      await old.journal.prepare(intent());
      final next = await f.open();
      await next.journal.prepare(intent(id: requestB));
      await old.journal.close();
      expect((await next.journal.read(requestB))!.state, 'prepared');
      expect((await next.journal.read(requestA))!.state, 'prepared');
    },
  );

  test('unsupported booking endpoint remains unknown without SDK immediate-create fallback', () async {
    final repo = await f.open();
    f.server.nextStatus = 404;
    await expectLater(
      repo.submit(intent()),
      throwsA(isA<BookingUnconfirmed>()),
    );
    expect(f.server.paths, ['create']);
    expect((await repo.journal.read(requestA))!.state, 'unknown');
    expect((await f.row(repo, requestA))!['payload_json'], isNotNull);
  });

  test('terminal receipt cannot regress a newer locally verified cancellation after restart', () async {
    final old = await f.open();
    await old.submit(intent());
    await old.journal.observe([
      const BookingRecord(bookingA, 'owner', 'cancelled', 3, null),
    ]);
    await old.journal.close();
    final next = await f.open();
    final proof = await next.recover(requestA);
    expect(proof.action, 'create');
    expect(proof.appliedVersion, 1);
    expect(proof.record!.version, 3);
    expect(proof.record!.state, 'cancelled');
    expect(proof.record!.payload, isNull);
    expect(f.server.paths, ['create']);
  });

  test('malformed or media-bearing responses preserve unknown state and private draft', () async {
    final repo = await f.open();
    await repo.journal.prepare(intent());
    final valid = f.server.applied(requestA);
    for (final wrong in <Map<String, Object?>>[
      {...valid, 'replaySafe': false},
      {...valid, 'found': 'true'},
      {...valid, 'joinToken': 'synthetic-forbidden'},
      {
        ...valid,
        'booking': {
          ...bookingMap(valid['booking']),
          'hostUserID': 'foreign-owner',
        },
      },
      {
        ...valid,
        'booking': {...bookingMap(valid['booking']), 'mediaReady': true},
      },
    ]) {
      f.server.nextData = wrong;
      await expectLater(
        repo.recover(requestA),
        throwsA(isA<BookingUnconfirmed>()),
      );
      expect((await f.row(repo, requestA))!['payload_json'], isNotNull);
    }
  });
}
