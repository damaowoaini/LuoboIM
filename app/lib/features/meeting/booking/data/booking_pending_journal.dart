import 'dart:convert';

import 'package:sqflite/sqflite.dart';

import 'booking_contract.dart';

/// accountKey is SessionCredentials.accountKey, never a browser/form/HTTP field.
/// The closure captures one SessionAccess, never re-reads the current account.
final class BookingJournalScope {
  BookingJournalScope({
    required this.accountKey,
    required this.issuer,
    required this.tenantId,
    required this.owner,
    required void Function() checkCurrent,
  }) : _check = checkCurrent,
       epoch = bookingUUID() {
    if (!RegExp(r'^[0-9a-f]{64}$').hasMatch(accountKey) ||
        issuer.isEmpty ||
        tenantId.isEmpty ||
        owner.isEmpty ||
        bookingDigest(jsonEncode([issuer, tenantId, owner])) != accountKey) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
    check();
  }
  final String accountKey;
  final String issuer;
  final String tenantId;
  final String owner;
  final String epoch;
  final void Function() _check;
  bool _closed = false;
  void check() {
    if (_closed) {
      throw const BookingLocalFailure(BookingLocalFailureKind.staleSession);
    }
    _check();
  }

  void close() => _closed = true;
}

final class BookingJournalEntry {
  const BookingJournalEntry({
    required this.id,
    required this.operation,
    required this.target,
    required this.expectedVersion,
    required this.digest,
    required this.revision,
    required this.state,
    required this.intent,
    required this.receipt,
    required this.claim,
    required this.claimEpoch,
    required this.claimKind,
  });
  final String id;
  final BookingOperation operation;
  final String? target;
  final int? expectedVersion;
  final String digest;
  final int revision;
  final String state;
  final BookingIntent? intent;
  final BookingReceipt? receipt;
  final String? claim;
  final String? claimEpoch;
  final String? claimKind;
  bool get terminal => state == 'confirmed';
  @override
  String toString() => 'BookingJournalEntry(<redacted>)';
}

final class BookingPrepared {
  const BookingPrepared(this.entry, this.inserted);
  final BookingJournalEntry entry;
  final bool inserted;
}

final class BookingJournalTicket {
  const BookingJournalTicket(this.entry);
  final BookingJournalEntry entry;
}

/// One DB handle and one captured session. No method accepts an arbitrary owner
/// or accountKey. A new binding fences old handles, including same-user relogin.
final class BookingPendingJournal {
  BookingPendingJournal._(this._db, this.scope);
  final Database _db;
  final BookingJournalScope scope;
  bool _closed = false;

  static Future<BookingPendingJournal> open(
    DatabaseFactory factory,
    String privatePath,
    BookingJournalScope scope,
  ) async {
    scope.check();
    Database? db;
    try {
      db = await factory.openDatabase(
        privatePath,
        options: OpenDatabaseOptions(
          version: 1,
          singleInstance: false,
          onConfigure: (db) async {
            // FULL commit precedes HTTP. DELETE mode avoids long-lived WAL copies
            // of discarded draft bodies; this is not a forensic erasure promise.
            await db.rawQuery('PRAGMA journal_mode=DELETE');
            await db.execute('PRAGMA synchronous=FULL');
            await db.execute('PRAGMA secure_delete=ON');
            await db.execute('PRAGMA foreign_keys=ON');
            await db.execute('PRAGMA busy_timeout=5000');
          },
          onCreate: (db, _) async {
            await db.execute(
              'CREATE TABLE booking_journal_accounts (account_key TEXT PRIMARY KEY, epoch TEXT NOT NULL)',
            );
            await db.execute(
              '''CREATE TABLE booking_pending_intents (
            account_key TEXT NOT NULL, request_id TEXT NOT NULL, operation TEXT NOT NULL,
            target_id TEXT, expected_version INTEGER, payload_hash TEXT NOT NULL,
            payload_json TEXT, state TEXT NOT NULL, revision INTEGER NOT NULL,
            claim_id TEXT, claim_epoch TEXT, claim_kind TEXT, receipt_json TEXT,
            PRIMARY KEY(account_key, request_id),
            FOREIGN KEY(account_key) REFERENCES booking_journal_accounts(account_key))''',
            );
            await db.execute(
              'CREATE INDEX booking_pending_scope_state ON booking_pending_intents(account_key,state,request_id)',
            );
            await db.execute(
              '''CREATE TABLE booking_version_floors (
            account_key TEXT NOT NULL, booking_id TEXT NOT NULL, version INTEGER NOT NULL,
            state TEXT NOT NULL, PRIMARY KEY(account_key,booking_id),
            FOREIGN KEY(account_key) REFERENCES booking_journal_accounts(account_key))''',
            );
          },
        ),
      );
      scope.check();
      await db.transaction((tx) async {
        scope.check();
        final existing = await tx.query(
          'booking_journal_accounts',
          where: 'account_key=?',
          whereArgs: [scope.accountKey],
        );
        if (existing.isNotEmpty && !isBookingUUID(existing.single['epoch'])) {
          throw const BookingLocalFailure(BookingLocalFailureKind.corrupt);
        }
        await tx.rawInsert(
          'INSERT INTO booking_journal_accounts(account_key,epoch) VALUES(?,?) '
          'ON CONFLICT(account_key) DO UPDATE SET epoch=excluded.epoch',
          [scope.accountKey, scope.epoch],
        );
        scope.check();
      });
      scope.check();
      return BookingPendingJournal._(db, scope);
    } catch (error) {
      if (db != null) {
        try {
          await db.close();
        } catch (_) {
          throw const BookingLocalFailure(BookingLocalFailureKind.storage);
        }
      }
      if (error is BookingLocalFailure) {
        rethrow;
      }
      scope.check();
      throw const BookingLocalFailure(BookingLocalFailureKind.storage);
    }
  }

  Future<T> _transaction<T>(Future<T> Function(Transaction) action) async {
    if (_closed) {
      throw const BookingLocalFailure(BookingLocalFailureKind.staleSession);
    }
    scope.check();
    try {
      return await _db.transaction((tx) async {
        await _fence(tx);
        final result = await action(tx);
        await _fence(tx);
        return result;
      });
    } catch (error) {
      if (error is BookingLocalFailure) {
        rethrow;
      }
      scope.check();
      throw const BookingLocalFailure(BookingLocalFailureKind.storage);
    }
  }

  Future<void> _fence(Transaction tx) async {
    scope.check();
    final rows = await tx.query(
      'booking_journal_accounts',
      columns: ['epoch'],
      where: 'account_key=?',
      whereArgs: [scope.accountKey],
    );
    scope.check();
    if (rows.length != 1 || rows.single['epoch'] != scope.epoch) {
      throw const BookingLocalFailure(BookingLocalFailureKind.staleSession);
    }
  }

  Future<BookingJournalEntry?> _read(Transaction tx, String id) async {
    if (!isBookingUUID(id)) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
    final rows = await tx.query(
      'booking_pending_intents',
      where: 'account_key=? AND request_id=?',
      whereArgs: [scope.accountKey, id],
    );
    if (rows.isEmpty) {
      return null;
    }
    final entry = _decode(rows.single);
    final record = entry.receipt?.record;
    if (record == null) {
      return entry;
    }
    final floors = await tx.query(
      'booking_version_floors',
      where: 'account_key=? AND booking_id=?',
      whereArgs: [scope.accountKey, record.id],
    );
    if (floors.length != 1 ||
        floors.single['version'] is! int ||
        (floors.single['version'] as int) < record.version ||
        (floors.single['version'] as int) > 1000000000 ||
        !{'scheduled', 'cancelled'}.contains(floors.single['state']) ||
        (record.state == 'cancelled' &&
            floors.single['state'] != 'cancelled')) {
      throw const BookingLocalFailure(BookingLocalFailureKind.corrupt);
    }
    final version = floors.single['version'] as int,
        state = floors.single['state'] as String;
    if (version == record.version && state != record.state) {
      throw const BookingLocalFailure(BookingLocalFailureKind.corrupt);
    }
    if (version == record.version) {
      return entry;
    }
    final receipt = entry.receipt!;
    return BookingJournalEntry(
      id: entry.id,
      operation: entry.operation,
      target: entry.target,
      expectedVersion: entry.expectedVersion,
      digest: entry.digest,
      revision: entry.revision,
      state: entry.state,
      intent: null,
      claim: null,
      claimEpoch: null,
      claimKind: null,
      receipt: BookingReceipt(
        id: receipt.id,
        found: true,
        applied: receipt.applied,
        definitiveNotApplied: receipt.definitiveNotApplied,
        action: receipt.action,
        appliedVersion: receipt.appliedVersion,
        record: BookingRecord(
          record.id,
          scope.owner,
          state,
          version,
          null,
          detailsAvailable: false,
        ),
      ),
    );
  }

  BookingJournalEntry _decode(Map<String, Object?> row) {
    try {
      final id = row['request_id'],
          operation = row['operation'],
          revision = row['revision'];
      final target = row['target_id'],
          expected = row['expected_version'],
          hash = row['payload_hash'];
      final state = row['state'],
          body = row['payload_json'],
          receipt = row['receipt_json'];
      final claim = row['claim_id'],
          epoch = row['claim_epoch'],
          kind = row['claim_kind'];
      if (row['account_key'] != scope.accountKey ||
          !isBookingUUID(id) ||
          !BookingOperation.values.any((v) => v.name == operation) ||
          revision is! int ||
          revision < 1 ||
          hash is! String ||
          !RegExp(r'^[0-9a-f]{64}$').hasMatch(hash) ||
          !{'prepared', 'unknown', 'confirmed'}.contains(state) ||
          (claim != null &&
              (!isBookingUUID(claim) ||
                  !isBookingUUID(epoch) ||
                  !{'submit', 'retry', 'receipt', 'resolve'}.contains(kind))) ||
          (claim == null && (epoch != null || kind != null))) {
        throw const BookingLocalFailure(BookingLocalFailureKind.corrupt);
      }
      final op = BookingOperation.values.firstWhere((v) => v.name == operation);
      if ((op == BookingOperation.create &&
              (target != null || expected != null)) ||
          (op != BookingOperation.create &&
              (!isBookingUUID(target) ||
                  expected is! int ||
                  expected < 1 ||
                  expected > 1000000000))) {
        throw const BookingLocalFailure(BookingLocalFailureKind.corrupt);
      }
      BookingIntent? intent;
      BookingReceipt? result;
      if (state == 'confirmed') {
        if (body != null || receipt is! String || claim != null) {
          throw const BookingLocalFailure(BookingLocalFailureKind.corrupt);
        }
        result = BookingReceipt.minimal(id as String, scope.owner, receipt);
        result.verifyIntent(op, target as String?);
        if (result.minimalJson() != receipt) {
          throw const BookingLocalFailure(BookingLocalFailureKind.corrupt);
        }
      } else {
        if (body is! String ||
            receipt != null ||
            (state == 'prepared' && claim != null)) {
          throw const BookingLocalFailure(BookingLocalFailureKind.corrupt);
        }
        intent = BookingIntent(
          id: id as String,
          operation: op,
          frozenBody: bookingMap(jsonDecode(body)),
        );
        if (intent.frozenJson != body ||
            intent.digest != hash ||
            intent.target != target ||
            intent.expectedVersion != expected) {
          throw const BookingLocalFailure(BookingLocalFailureKind.corrupt);
        }
      }
      return BookingJournalEntry(
        id: id,
        operation: op,
        target: target as String?,
        expectedVersion: expected as int?,
        digest: hash,
        revision: revision,
        state: state as String,
        intent: intent,
        receipt: result,
        claim: claim as String?,
        claimEpoch: epoch as String?,
        claimKind: kind as String?,
      );
    } catch (_) {
      throw const BookingLocalFailure(BookingLocalFailureKind.corrupt);
    }
  }

  Future<BookingPrepared> prepare(BookingIntent intent) =>
      _transaction((tx) async {
        final old = await _read(tx, intent.id);
        if (old != null) {
          if (old.digest != intent.digest ||
              old.operation != intent.operation ||
              old.target != intent.target ||
              old.expectedVersion != intent.expectedVersion) {
            throw const BookingLocalFailure(BookingLocalFailureKind.conflict);
          }
          return BookingPrepared(old, false);
        }
        await tx.insert('booking_pending_intents', {
          'account_key': scope.accountKey,
          'request_id': intent.id,
          'operation': intent.operation.name,
          'target_id': intent.target,
          'expected_version': intent.expectedVersion,
          'payload_hash': intent.digest,
          'payload_json': intent.frozenJson,
          'state': 'prepared',
          'revision': 1,
        });
        return BookingPrepared((await _read(tx, intent.id))!, true);
      });

  Future<BookingJournalEntry?> read(String id) =>
      _transaction((tx) => _read(tx, id));

  Future<List<BookingJournalEntry>> pending() => _transaction((tx) async {
    final rows = await tx.query(
      'booking_pending_intents',
      where: 'account_key=? AND state<>?',
      whereArgs: [scope.accountKey, 'confirmed'],
      orderBy: 'request_id',
    );
    return List.unmodifiable(rows.map(_decode));
  });

  Future<BookingJournalTicket> claim(
    String id,
    String kind, {
    bool requirePrepared = false,
  }) => _transaction((tx) async {
    if (!{'submit', 'retry', 'receipt', 'resolve'}.contains(kind)) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
    final old = await _read(tx, id);
    if (old == null || old.terminal) {
      throw const BookingLocalFailure(BookingLocalFailureKind.conflict);
    }
    if (old.claim != null && old.claimEpoch == scope.epoch) {
      throw const BookingLocalFailure(BookingLocalFailureKind.busy);
    }
    if (requirePrepared && old.state != 'prepared') {
      throw const BookingLocalFailure(BookingLocalFailureKind.conflict);
    }
    final changed = await tx.update(
      'booking_pending_intents',
      {
        'state': 'unknown',
        'revision': old.revision + 1,
        'claim_id': bookingUUID(),
        'claim_epoch': scope.epoch,
        'claim_kind': kind,
      },
      where: 'account_key=? AND request_id=? AND revision=?',
      whereArgs: [scope.accountKey, id, old.revision],
    );
    if (changed != 1) {
      throw const BookingLocalFailure(BookingLocalFailureKind.conflict);
    }
    return BookingJournalTicket((await _read(tx, id))!);
  });

  Future<void> release(BookingJournalTicket ticket) => _transaction((tx) async {
    final changed = await tx.update(
      'booking_pending_intents',
      {
        'state': 'unknown',
        'revision': ticket.entry.revision + 1,
        'claim_id': null,
        'claim_epoch': null,
        'claim_kind': null,
      },
      where: 'account_key=? AND request_id=? AND revision=? AND claim_id=? AND claim_epoch=?',
      whereArgs: [
        scope.accountKey,
        ticket.entry.id,
        ticket.entry.revision,
        ticket.entry.claim,
        scope.epoch,
      ],
    );
    if (changed != 1) {
      throw const BookingLocalFailure(BookingLocalFailureKind.conflict);
    }
  });

  Future<void> _observe(Transaction tx, BookingRecord row) async {
    if (row.host != scope.owner) {
      throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
    }
    final old = await tx.query(
      'booking_version_floors',
      where: 'account_key=? AND booking_id=?',
      whereArgs: [scope.accountKey, row.id],
    );
    if (old.isNotEmpty) {
      final version = old.single['version'], state = old.single['state'];
      if (version is! int ||
          version < 1 ||
          version > 1000000000 ||
          !{'scheduled', 'cancelled'}.contains(state)) {
        throw const BookingLocalFailure(BookingLocalFailureKind.corrupt);
      }
      if (row.version < version ||
          (row.version == version && row.state != state) ||
          (state == 'cancelled' && row.state != 'cancelled')) {
        throw const BookingLocalFailure(BookingLocalFailureKind.staleResponse);
      }
    }
    await tx.rawInsert(
      'INSERT INTO booking_version_floors(account_key,booking_id,version,state) VALUES(?,?,?,?) '
      'ON CONFLICT(account_key,booking_id) DO UPDATE SET version=excluded.version,state=excluded.state',
      [scope.accountKey, row.id, row.version, row.state],
    );
  }

  Future<void> observe(List<BookingRecord> records) => _transaction((tx) async {
    for (final row in records) {
      await _observe(tx, row);
    }
  });

  Future<void> confirm(BookingJournalTicket ticket, BookingReceipt receipt) =>
      _transaction((tx) async {
        if (receipt.id != ticket.entry.id ||
            !receipt.found ||
            (!receipt.applied && !receipt.definitiveNotApplied)) {
          throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
        }
        receipt.verifyIntent(ticket.entry.operation, ticket.entry.target);
        // Verify the exact current ticket before any version-floor or receipt write.
        final current = await _read(tx, ticket.entry.id);
        if (current == null ||
            current.terminal ||
            current.revision != ticket.entry.revision ||
            current.claim != ticket.entry.claim ||
            current.claimEpoch != scope.epoch) {
          throw const BookingLocalFailure(BookingLocalFailureKind.conflict);
        }
        if (receipt.record != null) {
          await _observe(tx, receipt.record!);
        }
        final changed = await tx.update(
          'booking_pending_intents',
          {
            'state': 'confirmed',
            'revision': ticket.entry.revision + 1,
            'payload_json': null,
            'claim_id': null,
            'claim_epoch': null,
            'claim_kind': null,
            'receipt_json': receipt.minimalJson(),
          },
          where: 'account_key=? AND request_id=? AND revision=? AND claim_id=? AND claim_epoch=?',
          whereArgs: [
            scope.accountKey,
            ticket.entry.id,
            ticket.entry.revision,
            ticket.entry.claim,
            scope.epoch,
          ],
        );
        if (changed != 1) {
          throw const BookingLocalFailure(BookingLocalFailureKind.conflict);
        }
      });

  Future<void> close() async {
    if (_closed) {
      return;
    }
    _closed = true;
    scope.close();
    try {
      await _db.close();
    } catch (_) {
      throw const BookingLocalFailure(BookingLocalFailureKind.storage);
    }
  }
}
