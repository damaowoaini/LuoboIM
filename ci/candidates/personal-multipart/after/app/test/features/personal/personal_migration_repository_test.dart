import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/personal/data/personal_archive.dart';
import 'package:imapp/features/personal/data/personal_migration.dart';
import 'package:imapp/features/personal/data/personal_repository.dart';
import 'package:imapp/features/personal/domain/personal_item.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

PersonalItem _note(String id, String title) => PersonalItem(
  id: id, kind: PersonalKind.note, title: title, body: '原文',
  createdAt: DateTime.utc(2025), updatedAt: DateTime.utc(2025),
);

({PersonalMigrationManifest manifest, List<List<int>> parts}) _fixture(
    List<List<PersonalItem>> chunks) {
  final raw = utf8.encode(jsonEncode({'local_notes': jsonEncode(
      chunks.expand((chunk) => chunk).map((item) => item.toJson()).toList()),
    'local_todos': '[]'}));
  final setId = personalMigrationSha(raw), report = utf8.encode('[]');
  final parts = <List<int>>[], specs = <Map<String, dynamic>>[];
  var sourceIndex = 0;
  for (var index = 0; index < chunks.length; index++) {
    final first = sourceIndex;
    final records = [for (final item in chunks[index])
      {'kind': 'note', 'sourceIndex': sourceIndex++, 'item': item.toJson()}];
    final bytes = utf8.encode(jsonEncode({'format': 'luobo-personal-migration-part',
      'version': 1, 'set': setId, 'index': index, 'records': records}));
    parts.add(bytes);
    specs.add({'index': index, 'file': 'part-$index.json', 'bytes': bytes.length,
      'sha256': personalMigrationSha(bytes),
      'sources': {'note': [first, sourceIndex - 1, records.length], 'todo': null}});
  }
  final manifest = PersonalMigrationManifest.decode(utf8.encode(jsonEncode({
    'format': 'luobo-personal-migration', 'version': 1,
    'applicationId': 'com.luoboduihua.imapp', 'owner': null, 'set': setId,
    'raw': {'file': 'original.json', 'bytes': raw.length, 'sha256': setId},
    'report': {'file': 'unmigrated.json', 'bytes': report.length,
      'sha256': personalMigrationSha(report)},
    'sourceCounts': {'note': sourceIndex, 'todo': 0},
    'rejected': 0, 'structuralErrors': [], 'parts': specs,
  })));
  return (manifest: manifest, parts: parts);
}

Future<void> _start(PersonalRepository repo, PersonalMigrationManifest manifest) =>
    repo.startMigration(manifest, confirmOwnership: true,
      verifiedRawSha: manifest.setId,
      verifiedReportSha: (manifest.data['report'] as Map)['sha256'] as String);

void main() {
  sqfliteFfiInit();
  late Directory directory;
  final repositories = <PersonalRepository>[];
  setUp(() async {
    directory = await Directory.systemTemp.createTemp('luobo-migration-db-');
  });
  tearDown(() async {
    for (final repo in repositories) { await repo.close(); }
    repositories.clear();
    await directory.delete(recursive: true);
  });
  PersonalRepository repository(String key, {bool Function()? current}) {
    final repo = PersonalRepository(accountKey: key, isCurrent: current ?? () => true,
      open: (key) => initializePersonalDatabase(databaseFactoryFfi,
          '${directory.path}/$key.db'));
    repositories.add(repo); return repo;
  }

  test('multipart receipts preserve ordered duplicate IDs and stable aliases across resume and accounts', () async {
    final first = repository('a' * 64), second = repository('b' * 64);
    final item = _note('same-id', '旧内容一');
    final fixture = _fixture([[item, _note('same-id', '旧内容二')], [_note('third', '第三条')]]);
    final manifest = fixture.manifest;
    await first.save(_note('same-id', '当前内容'));
    final firstAlias = 'import-${personalMigrationSha(utf8.encode(jsonEncode([
      manifest.setId, 'note', 0, item.encoded, 1])))}';
    await first.save(_note(firstAlias, '已有别名冲突也不能丢弃'));
    expect(() => first.startMigration(manifest, confirmOwnership: false,
      verifiedRawSha: manifest.setId,
      verifiedReportSha: (manifest.data['report'] as Map)['sha256'] as String),
      throwsA(isA<AppFailure>()));
    expect(await first.migrationProgress(manifest), isNull);
    await _start(first, manifest);
    expect(await first.importMigrationPart(manifest, 0, fixture.parts[0]), 2);
    expect(await first.migrationProgress(manifest), {0});
    final imported = (await first.list()).singleWhere((value) => value.title == '旧内容一');
    expect(imported.id, isNot(anyOf('same-id', firstAlias)));
    await first.save(imported.copyWith(title: '用户已编辑'));
    expect(await first.importMigrationPart(manifest, 0, fixture.parts[0]), 0);
    expect((await first.list()).singleWhere((value) => value.id == imported.id).title, '用户已编辑');
    expect(await first.importMigrationPart(manifest, 1, fixture.parts[1]), 1);
    expect(await first.migrationProgress(manifest), {0, 1});
    // Verify the durable mapping, not only the inserted count.
    final db = await initializePersonalDatabase(databaseFactoryFfi, '${directory.path}/${first.accountKey}.db');
    try {
      final row = (await db.query('migration_parts', where: 'part_index = 0')).single;
      final receipt = jsonDecode(row['receipt'] as String) as Map;
      final mappings = receipt['mappings'] as List;
      expect(mappings.map((value) => value['sourceIndex']), [0, 1]);
      expect(mappings.map((value) => value['originalId']), ['same-id', 'same-id']);
      expect(mappings.map((value) => value['targetId']).toSet().length, 2);
      expect(mappings.first['targetId'], imported.id);
    } finally { await db.close(); }
    expect(await second.migrationProgress(manifest), isNull);
    await expectLater(second.importMigrationPart(manifest, 1, fixture.parts[1]), throwsA(isA<AppFailure>()));
    expect(await second.list(), isEmpty);
    final changed = jsonDecode(jsonEncode(manifest.data)) as Map;
    changed['report']['sha256'] = 'c' * 64;
    final changedManifest = PersonalMigrationManifest.decode(utf8.encode(jsonEncode(changed)));
    await expectLater(first.migrationProgress(changedManifest), throwsA(isA<AppFailure>()));
    expect(() => first.importMigrationPart(manifest, 1, [...fixture.parts[1], 32]), throwsA(isA<AppFailure>()));
  });

  test('logout during a part rolls back both rows and receipt then resumes only that part', () async {
    var armed = false, checks = 0;
    final first = repository('a' * 64, current: () => !armed || ++checks < 4);
    final fixture = _fixture([[_note('one', '第一条'), _note('two', '第二条')]]);
    await _start(first, fixture.manifest);
    armed = true;
    await expectLater(first.importMigrationPart(fixture.manifest, 0, fixture.parts[0]),
      throwsA(isA<AppFailure>().having((failure) => failure.kind, 'kind', FailureKind.cancelled)));
    await first.close();
    final reopened = repository('a' * 64);
    expect(await reopened.list(), isEmpty);
    expect(await reopened.migrationProgress(fixture.manifest), isEmpty);
    expect(await reopened.importMigrationPart(fixture.manifest, 0, fixture.parts[0]), 2);
    expect(await reopened.migrationProgress(fixture.manifest), {0});
  });

  test('additive v1 receipts retain old single imports and rollback reader compatibility', () async {
    final path = '${directory.path}/${'a' * 64}.db';
    final oldItem = _note('old-db', '原有本地笔记');
    final old = await databaseFactoryFfi.openDatabase(path, options: OpenDatabaseOptions(
      version: 1, singleInstance: false, onCreate: (db, _) async {
        await db.execute('CREATE TABLE personal_items (kind TEXT NOT NULL, id TEXT NOT NULL, payload TEXT NOT NULL, PRIMARY KEY (kind,id))');
        await db.execute('CREATE TABLE imports (fingerprint TEXT PRIMARY KEY NOT NULL, imported_at INTEGER NOT NULL)');
      }));
    await old.insert('personal_items', {'kind': 'note', 'id': oldItem.id, 'payload': oldItem.encoded});
    await old.insert('imports', {'fingerprint': 'original-marker', 'imported_at': 1});
    await old.close();
    final repo = repository('a' * 64);
    expect((await repo.list()).single.title, oldItem.title);
    final legacy = PersonalArchive.decode(utf8.encode(jsonEncode({
      'notes': [_note('legacy', '单文件旧包').toJson()], 'todos': []})));
    expect(() => repo.import(legacy, confirmOwnership: false), throwsA(isA<AppFailure>()));
    expect(await repo.import(legacy, confirmOwnership: true), 1);
    final known = PersonalArchive.decode(PersonalArchive.encode(repo.accountKey, [_note('v1', '已归属v1')]));
    expect(await repo.import(known, confirmOwnership: false), 1);
    final fixture = _fixture([[_note('part', '新分卷')]]);
    await _start(repo, fixture.manifest);
    expect(await repo.importMigrationPart(fixture.manifest, 0, fixture.parts[0]), 1);
    expect((await repo.list()).length, 4);
    await repo.close();
    // The previous binary opens version1 without our new onOpen hook.
    final db = await databaseFactoryFfi.openDatabase(path,
        options: OpenDatabaseOptions(version: 1, singleInstance: false));
    try {
      expect(await db.getVersion(), 1);
      expect(await db.query('personal_items'), hasLength(4));
      expect(await db.query('imports', where: 'fingerprint = ?', whereArgs: ['original-marker']), hasLength(1));
    } finally { await db.close(); }
  });
}
