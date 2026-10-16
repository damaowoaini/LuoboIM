import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:path_provider/path_provider.dart';
import 'package:sqflite/sqflite.dart';

import '../../../core/error/app_failure.dart';
import '../domain/personal_item.dart';
import 'personal_archive.dart';

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
  ),
);

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
