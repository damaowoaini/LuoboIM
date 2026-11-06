import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:path_provider/path_provider.dart';
import 'package:sqflite/sqflite.dart';

import '../../../core/error/app_failure.dart';
import '../domain/personal_item.dart';
import 'personal_archive.dart';
import 'personal_migration.dart';

typedef PersonalDatabaseOpener = Future<Database> Function(String accountKey);

Future<Database> openPersonalDatabase(String accountKey) async {
  if (!RegExp(r'^[a-f0-9]{64}$').hasMatch(accountKey)) {
    throw const AppFailure(FailureKind.invalidInput);
  }
  final support = await getApplicationSupportDirectory();
  final directory = await Directory('${support.path}/personal/v1')
      .create(recursive: true);
  return initializePersonalDatabase(
    databaseFactory,
    '${directory.path}/$accountKey.sqlite3',
  );
}

Future<Database> initializePersonalDatabase(
  DatabaseFactory factory,
  String path,
) => factory.openDatabase(
  path,
  options: OpenDatabaseOptions(
    version: 1,
    singleInstance: false,
    onCreate: (db, _) async {
      await db.execute(
        'CREATE TABLE personal_items (kind TEXT NOT NULL, id TEXT NOT NULL, payload TEXT NOT NULL, PRIMARY KEY (kind, id))',
      );
      await db.execute(
        'CREATE TABLE imports (fingerprint TEXT PRIMARY KEY NOT NULL, imported_at INTEGER NOT NULL)',
      );
    },
    // Optional receipt tables are additive. Keep user_version=1 so the prior
    // binary can still read original items/imports during an ordinary rollback.
    onOpen: (db) => db.transaction((tx) => _createMigrationTables(tx)),
  ),
);

Future<void> _createMigrationTables(DatabaseExecutor db) async {
  await db.execute(
    'CREATE TABLE IF NOT EXISTS migration_sets (set_id TEXT PRIMARY KEY NOT NULL, manifest_sha TEXT NOT NULL, account_key TEXT NOT NULL, raw_sha TEXT NOT NULL, report_sha TEXT NOT NULL, manifest TEXT NOT NULL)',
  );
  await db.execute(
    'CREATE TABLE IF NOT EXISTS migration_parts (set_id TEXT NOT NULL, part_index INTEGER NOT NULL, part_sha TEXT NOT NULL, inserted INTEGER NOT NULL, receipt TEXT NOT NULL, PRIMARY KEY (set_id, part_index))',
  );
}

/// One repository is permanently bound to one tenant/account and generation.
/// Imports and their completion marker commit in the same SQLite transaction.
final class PersonalRepository {
  PersonalRepository({
    required this.accountKey,
    required this.isCurrent,
    this._open = openPersonalDatabase,
  });
  final String accountKey;
  final bool Function() isCurrent;
  final PersonalDatabaseOpener _open;
  Future<Database>? _database;
  bool _closed = false;

  void _check() {
    if (_closed || !isCurrent()) throw const AppFailure(FailureKind.cancelled);
  }

  Future<T> _run<T>(Future<T> Function(Database db) operation) async {
    _check();
    try {
      final opening = _database ??= _open(accountKey);
      late Database db;
      try {
        db = await opening;
      } catch (_) {
        if (identical(_database, opening)) _database = null;
        rethrow;
      }
      _check();
      final result = await operation(db);
      _check();
      return result;
    } on AppFailure {
      rethrow;
    } catch (_) {
      throw const AppFailure(FailureKind.localStorage);
    }
  }

  Future<List<PersonalItem>> list() => _run((db) async {
    final records = await db.query('personal_items');
    return records
        .map(
          (row) => PersonalItem.fromJson(
            PersonalKind.values.byName(row['kind'] as String),
            jsonDecode(row['payload'] as String),
          ),
        )
        .toList();
  });

  Future<void> save(PersonalItem item) {
    // Reuse archive validation before accepting data from an editor.
    PersonalItem.fromJson(item.kind, item.toJson());
    return _run(
      (db) => db.transaction((tx) async {
        await tx.insert('personal_items', {
          'kind': item.kind.name,
          'id': item.id,
          'payload': item.encoded,
        }, conflictAlgorithm: ConflictAlgorithm.replace);
        _check();
      }),
    );
  }

  Future<void> delete(PersonalItem item) => _run(
    (db) => db.transaction((tx) async {
      await tx.delete(
        'personal_items',
        where: 'kind = ? AND id = ?',
        whereArgs: [item.kind.name, item.id],
      );
      _check();
    }),
  );

  Future<int> deleteCompleted() => _run(
    (db) => db.transaction((tx) async {
      var count = 0;
      final records = await tx.query(
        'personal_items',
        where: 'kind = ?',
        whereArgs: ['todo'],
      );
      for (final record in records) {
        final item = PersonalItem.fromJson(
          PersonalKind.todo,
          jsonDecode(record['payload'] as String),
        );
        if (item.completed) {
          count += await tx.delete(
            'personal_items',
            where: 'kind = ? AND id = ?',
            whereArgs: ['todo', item.id],
          );
        }
      }
      _check();
      return count;
    }),
  );

  Future<List<int>> export() async =>
      PersonalArchive.encode(accountKey, await list());

  Future<List<List<int>>> exportParts() async =>
      PersonalArchive.encodeParts(accountKey, await list());

  Future<int> import(
    PersonalArchive archive, {
    required bool confirmOwnership,
  }) {
    if ((archive.owner == null || archive.owner != accountKey) &&
        !confirmOwnership) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    return _run(
      (db) => db.transaction((tx) async {
        final previous = await tx.query(
          'imports',
          where: 'fingerprint = ?',
          whereArgs: [archive.fingerprint],
          limit: 1,
        );
        if (previous.isNotEmpty) return 0;
        var count = 0;
        for (final item in archive.items) {
          var id = item.id;
          final previousItem = await tx.query(
            'personal_items',
            where: 'kind = ? AND id = ?',
            whereArgs: [item.kind.name, id],
            limit: 1,
          );
          if (previousItem.isNotEmpty) {
            if (previousItem.single['payload'] == item.encoded) continue;
            id =
                'import-${sha256.convert(utf8.encode('${item.kind.name}:${item.encoded}'))}';
          }
          final data = item.toJson()..['id'] = id;
          await tx.rawInsert(
            'INSERT OR IGNORE INTO personal_items(kind,id,payload) VALUES(?,?,?)',
            [item.kind.name, id, jsonEncode(data)],
          );
          // rawInsert returns lastRowId, not affected rows; changes() is authoritative.
          count +=
              Sqflite.firstIntValue(await tx.rawQuery('SELECT changes()')) ?? 0;
          _check();
        }
        await tx.insert('imports', {
          'fingerprint': archive.fingerprint,
          'imported_at': DateTime.now().millisecondsSinceEpoch,
        });
        _check();
        return count;
      }),
    );
  }

  void _requireMigrationSet(
    List<Map<String, Object?>> rows,
    PersonalMigrationManifest manifest,
  ) {
    if (rows.length != 1 ||
        rows.single['account_key'] != accountKey ||
        rows.single['manifest_sha'] != manifest.fingerprint ||
        rows.single['raw_sha'] != manifest.setId ||
        rows.single['report_sha'] !=
            (manifest.data['report'] as Map)['sha256']) {
      throw const AppFailure(FailureKind.invalidInput);
    }
  }

  void _requireMigrationPartPin(
    Map<String, Object?> row,
    PersonalMigrationManifest manifest,
  ) {
    final index = row['part_index'];
    if (row['set_id'] != manifest.setId ||
        index is! int ||
        index < 0 ||
        index >= manifest.parts.length ||
        row['part_sha'] != manifest.parts[index]['sha256']) {
      throw const AppFailure(FailureKind.localStorage);
    }
  }

  void _requireMigrationReceipt(
    Map<String, Object?> row,
    PersonalMigrationManifest manifest,
  ) {
    _requireMigrationPartPin(row, manifest);
    final index = row['part_index'] as int;
    final receipt = jsonDecode(row['receipt'] as String);
    if (receipt is! Map ||
        receipt['version'] != 1 ||
        receipt['set'] != manifest.setId ||
        receipt['manifestSha'] != manifest.fingerprint ||
        receipt['accountKey'] != accountKey ||
        receipt['partIndex'] != index ||
        receipt['partSha'] != row['part_sha'] ||
        receipt['inserted'] != row['inserted'] ||
        receipt['mappings'] is! List) {
      throw const AppFailure(FailureKind.localStorage);
    }
  }

  /// null means this account has not confirmed and verified this exact set.
  /// A different account never inherits the first account's consent or receipts.
  Future<Set<int>?> migrationProgress(PersonalMigrationManifest manifest) =>
      _run((db) async {
        final rows = await db.query(
          'migration_sets',
          where: 'set_id = ?',
          whereArgs: [manifest.setId],
        );
        if (rows.isEmpty) return null;
        _requireMigrationSet(rows, manifest);
        // Only compact pins are needed for progress; do not deserialize every
        // large per-record mapping when resuming a multi-gigabyte backup.
        final parts = await db.query(
          'migration_parts',
          columns: ['set_id', 'part_index', 'part_sha'],
          where: 'set_id = ?',
          whereArgs: [manifest.setId],
        );
        for (final row in parts) {
          _requireMigrationPartPin(row, manifest);
        }
        return parts.map((row) => row['part_index'] as int).toSet();
      });

  Future<void> startMigration(
    PersonalMigrationManifest manifest, {
    required bool confirmOwnership,
    required String verifiedRawSha,
    required String verifiedReportSha,
  }) {
    if (!confirmOwnership ||
        verifiedRawSha != manifest.setId ||
        verifiedReportSha != (manifest.data['report'] as Map)['sha256']) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    return _run(
      (db) => db.transaction((tx) async {
        final rows = await tx.query(
          'migration_sets',
          where: 'set_id = ?',
          whereArgs: [manifest.setId],
        );
        if (rows.isNotEmpty) {
          _requireMigrationSet(rows, manifest);
        } else {
          await tx.insert('migration_sets', {
            'set_id': manifest.setId,
            'manifest_sha': manifest.fingerprint,
            'account_key': accountKey,
            'raw_sha': verifiedRawSha,
            'report_sha': verifiedReportSha,
            'manifest': jsonEncode(manifest.data),
          });
        }
        _check();
      }),
    );
  }

  /// One part and its exact source-index -> destination-ID receipt commit
  /// together. A committed receipt wins even if the user later edits/deletes an
  /// imported item; resuming never resurrects or overwrites that item.
  Future<int> importMigrationPart(
    PersonalMigrationManifest manifest,
    int index,
    List<int> bytes,
  ) {
    late List<Map<String, dynamic>> records;
    try {
      records = manifest.decodePart(index, bytes);
      for (final record in records) {
        PersonalItem.fromJson(
          PersonalKind.values.byName(record['kind'] as String),
          record['item'],
        );
      }
    } catch (_) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    return _run(
      (db) => db.transaction((tx) async {
        _requireMigrationSet(
          await tx.query(
            'migration_sets',
            where: 'set_id = ?',
            whereArgs: [manifest.setId],
          ),
          manifest,
        );
        final previous = await tx.query(
          'migration_parts',
          where: 'set_id = ? AND part_index = ?',
          whereArgs: [manifest.setId, index],
        );
        final partSha = manifest.parts[index]['sha256'] as String;
        if (previous.isNotEmpty) {
          _requireMigrationReceipt(previous.single, manifest);
          if (previous.single['part_sha'] != partSha) {
            throw const AppFailure(FailureKind.invalidInput);
          }
          _check();
          return 0;
        }
        var inserted = 0;
        final mappings = <Map<String, dynamic>>[];
        for (final record in records) {
          _check();
          final item = PersonalItem.fromJson(
            PersonalKind.values.byName(record['kind'] as String),
            record['item'],
          );
          var resolved = false;
          // Deterministic probing handles even a pre-existing conflicting alias.
          // No INSERT OR IGNORE: exhausting the bound rolls back this whole part.
          for (var probe = 0; probe < 1000; probe++) {
            final id = probe == 0
                ? item.id
                : 'import-${sha256.convert(utf8.encode(jsonEncode([manifest.setId, item.kind.name, record['sourceIndex'], item.encoded, probe])))}';
            final payload = jsonEncode(item.toJson()..['id'] = id);
            final existing = await tx.query(
              'personal_items',
              where: 'kind = ? AND id = ?',
              whereArgs: [item.kind.name, id],
              limit: 1,
            );
            if (existing.isNotEmpty && existing.single['payload'] != payload) {
              continue;
            }
            if (existing.isEmpty) {
              await tx.insert('personal_items', {
                'kind': item.kind.name,
                'id': id,
                'payload': payload,
              });
              inserted++;
            }
            mappings.add({
              'kind': item.kind.name,
              'sourceIndex': record['sourceIndex'],
              'originalId': item.id,
              'targetId': id,
              'action': existing.isEmpty ? 'inserted' : 'already-present',
            });
            resolved = true;
            break;
          }
          if (!resolved) throw const AppFailure(FailureKind.localStorage);
          _check();
        }
        final receipt = {
          'version': 1,
          'set': manifest.setId,
          'manifestSha': manifest.fingerprint,
          'accountKey': accountKey,
          'partIndex': index,
          'partSha': partSha,
          'inserted': inserted,
          'mappings': mappings,
        };
        await tx.insert('migration_parts', {
          'set_id': manifest.setId,
          'part_index': index,
          'part_sha': partSha,
          'inserted': inserted,
          'receipt': jsonEncode(receipt),
        });
        _check();
        return inserted;
      }),
    );
  }

  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    final pending = _database;
    if (pending != null) {
      try {
        await (await pending).close();
      } catch (_) {}
    }
  }
}
