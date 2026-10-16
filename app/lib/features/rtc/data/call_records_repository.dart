import 'dart:async';
import 'dart:io';

import 'package:path_provider/path_provider.dart';
import 'package:sqflite/sqflite.dart';

import '../../../core/error/app_failure.dart';
import '../domain/call_record.dart';

typedef CallRecordsDatabaseOpener = Future<Database> Function(String accountKey);

Future<Database> openCallRecordsDatabase(String accountKey) async {
  if (!RegExp(r'^[a-f0-9]{64}$').hasMatch(accountKey)) {
    throw const AppFailure(FailureKind.invalidInput);
  }
  final support = await getApplicationSupportDirectory();
  final directory = await Directory('${support.path}/call-records/v1').create(recursive: true);
  return initializeCallRecordsDatabase(databaseFactory,
    '${directory.path}/$accountKey.sqlite3', accountKey);
}

Future<Database> initializeCallRecordsDatabase(DatabaseFactory factory,
    String path, String accountKey) => factory.openDatabase(path,
  options: OpenDatabaseOptions(version: 1, singleInstance: false,
    onCreate: (db, _) async {
      await db.execute('CREATE TABLE call_meta (singleton INTEGER PRIMARY KEY CHECK(singleton=1), account_key TEXT NOT NULL, read_revision INTEGER NOT NULL)');
      await db.insert('call_meta', {'singleton': 1, 'account_key': accountKey, 'read_revision': 0});
      await db.execute('CREATE TABLE call_claims (call_id TEXT PRIMARY KEY NOT NULL, tuple_digest TEXT NOT NULL, deleted INTEGER NOT NULL DEFAULT 0)');
      await db.execute('CREATE TABLE call_records (sequence INTEGER PRIMARY KEY AUTOINCREMENT, call_id TEXT UNIQUE NOT NULL, tuple_digest TEXT NOT NULL, event_revision INTEGER NOT NULL, peer_id TEXT NOT NULL, name TEXT NOT NULL, avatar TEXT, direction TEXT NOT NULL, video INTEGER NOT NULL, admitted_at INTEGER NOT NULL, accepted INTEGER NOT NULL, connected INTEGER NOT NULL, signaling_started INTEGER NOT NULL, terminal INTEGER NOT NULL, duration_ms INTEGER NOT NULL, ended_at INTEGER, reason TEXT, cause TEXT)');
    },
    onOpen: (db) async {
      final meta = await db.query('call_meta', where: 'singleton=1');
      if (meta.length != 1 || meta.single['account_key'] != accountKey) {
        throw const AppFailure(FailureKind.localStorage);
      }
    },
  ));

/// Permanently owns one database. Writer events already frozen by the recorder
/// may drain after logout. UI reads/deletes instead retain their caller's lease.
final class CallRecordsRepository {
  CallRecordsRepository({required String accountKey,
    CallRecordsDatabaseOpener open = openCallRecordsDatabase}) : this._(accountKey, open);
  CallRecordsRepository._(this.accountKey, this._open);
  final String accountKey;
  final CallRecordsDatabaseOpener _open;
  Future<Database>? _database;
  Future<void> _tail = Future<void>.value();
  Future<void>? _closing;
  bool _closed = false;
  final Map<String, int> _revisionBase = {};

  Future<T> _run<T>(Future<T> Function(Database) operation,
      {bool Function()? current}) {
    if (_closed) return Future.error(const AppFailure(FailureKind.cancelled));
    final result = Completer<T>();
    _tail = _tail.then((_) async {
      try {
        void check() {
          if (current != null && !current()) {
            throw const AppFailure(FailureKind.cancelled);
          }
        }
        check();
        final opening = _database ??= _open(accountKey);
        late Database db;
        try { db = await opening; } catch (_) {
          if (identical(_database, opening)) _database = null;
          rethrow;
        }
        check();
        final value = await operation(db);
        check();
        result.complete(value);
      } catch (error, stack) {
        result.completeError(error is AppFailure ? error :
          const AppFailure(FailureKind.localStorage), stack);
      }
    });
    return result.future;
  }

  Future<void> _bump(Transaction tx) => tx.rawUpdate(
    'UPDATE call_meta SET read_revision=read_revision+1 WHERE singleton=1').then((_) {});

  Future<void> apply(String ownerKey, CallRecord event) {
    if (ownerKey != accountKey) {
      return Future.error(const AppFailure(FailureKind.cancelled));
    }
    event.validate();
    return _run((db) => db.transaction((tx) async {
      final claims = await tx.query('call_claims', where: 'call_id=?', whereArgs: [event.id]);
      if (claims.isNotEmpty) {
        if (claims.single['tuple_digest'] != event.claim) {
          throw const AppFailure(FailureKind.dataChanged);
        }
        if (claims.single['deleted'] == 1) return;
      } else {
        await tx.insert('call_claims', {'call_id': event.id,
          'tuple_digest': event.claim, 'deleted': 0});
      }
      final old = await tx.query('call_records', where: 'call_id=?', whereArgs: [event.id]);
      final base = _revisionBase.putIfAbsent(event.id, () => old.isEmpty ? 0 : old.single['event_revision'] as int);
      final revision = base + event.revision;
      if (revision > 0x1fffffffffffff) throw const AppFailure(FailureKind.localStorage);
      final data = event.toRow()..['event_revision'] = revision;
      if (old.isNotEmpty) {
        final previous = old.single;
        if (previous['terminal'] == 1 ||
            (previous['event_revision'] as int) >= revision) {
          return;
        }
        // A restored invitation in a new owner binding does not erase durable
        // accepted/connected facts or change its original admission order.
        data['admitted_at'] = previous['admitted_at'];
        for (final key in ['accepted', 'connected', 'signaling_started']) {
          if (previous[key] == 1) data[key] = 1;
        }
        if ((previous['duration_ms'] as int) > event.durationMs) {
          data['duration_ms'] = previous['duration_ms'];
        }
        if (event.endedAt != null && event.endedAt! < (previous['admitted_at'] as int)) {
          data['ended_at'] = previous['admitted_at'];
        }
        await tx.update('call_records', data, where: 'call_id=?', whereArgs: [event.id]);
      } else {
        await tx.insert('call_records', data);
      }
      await _bump(tx);
    }));
  }

  Future<CallRecord?> exact(String id, {required bool Function() current}) =>
    _run((db) async {
      final rows = await db.query('call_records', where: 'call_id=?', whereArgs: [id], limit: 1);
      return rows.isEmpty ? null : CallRecord.fromRow(rows.single);
    }, current: current);

  Future<CallRecordsPageResult> list({required CallRecordsFilter filter,
      required bool Function() current, CallRecordsCursor? cursor, int limit = 30}) {
    if (limit < 1 || limit > 100 || (cursor != null &&
        (cursor.accountKey != accountKey || cursor.filter != filter ||
         cursor.revision < 0 || cursor.highWater < 1 ||
         cursor.beforeSequence < 1 || cursor.beforeSequence > cursor.highWater))) {
      return Future.error(const AppFailure(FailureKind.invalidInput));
    }
    return _run((db) => db.transaction((tx) async {
      final meta = (await tx.query('call_meta', where: 'singleton=1')).single;
      final revision = meta['read_revision'] as int;
      if (cursor != null && cursor.revision != revision) {
        throw const AppFailure(FailureKind.dataChanged);
      }
      final high = cursor?.highWater ??
        (Sqflite.firstIntValue(await tx.rawQuery('SELECT MAX(sequence) FROM call_records')) ?? 0);
      final clauses = <String>['sequence<=?'];
      final values = <Object?>[high];
      if (cursor != null) {
        clauses.add('sequence<?'); values.add(cursor.beforeSequence);
      }
      if (filter == CallRecordsFilter.missed) {
        clauses.add("terminal=1 AND direction='incoming' AND accepted=0 AND connected=0 AND ((reason='timedOut' AND cause IN ('remote','timer')) OR (reason IN ('cancelled','hungUp') AND cause='remote'))");
      }
      final rows = await tx.query('call_records', where: clauses.join(' AND '),
        whereArgs: values, orderBy: 'sequence DESC, call_id DESC', limit: limit + 1);
      if (!current()) throw const AppFailure(FailureKind.cancelled);
      final items = rows.take(limit).map(CallRecord.fromRow).toList(growable: false);
      return CallRecordsPageResult(List.unmodifiable(items), rows.length > limit ?
        CallRecordsCursor(accountKey, filter, revision, high, items.last.sequence) : null);
    }), current: current);
  }

  Future<bool> delete(CallRecord record, {required bool Function() current}) =>
    _run((db) => db.transaction((tx) async {
      if (!current()) throw const AppFailure(FailureKind.cancelled);
      final rows = await tx.query('call_records', where: 'call_id=?', whereArgs: [record.id]);
      if (rows.isEmpty) return false;
      if (rows.single['tuple_digest'] != record.claim ||
          rows.single['sequence'] != record.sequence) {
        throw const AppFailure(FailureKind.dataChanged);
      }
      await tx.update('call_claims', {'deleted': 1},
        where: 'call_id=? AND tuple_digest=?', whereArgs: [record.id, record.claim]);
      await tx.delete('call_records', where: 'call_id=?', whereArgs: [record.id]);
      await _bump(tx);
      if (!current()) throw const AppFailure(FailureKind.cancelled);
      return true;
    }), current: current);

  Future<void> close() {
    if (_closing != null) return _closing!;
    _closed = true;
    return _closing = () async {
      await _tail;
      final opened = _database;
      if (opened != null) await (await opened).close();
    }();
  }
}
