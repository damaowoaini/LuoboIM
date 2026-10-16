import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/personal/data/personal_archive.dart';
import 'package:imapp/features/personal/data/personal_repository.dart';
import 'package:imapp/features/personal/domain/personal_item.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

void main() {
  sqfliteFfiInit();
  late Directory directory;
  late PersonalRepository first;
  late PersonalRepository second;
  var active = true;
  setUp(() async {
    active = true;
    directory = await Directory.systemTemp.createTemp('luobo-personal-test-');
    Future<Database> open(String key) => initializePersonalDatabase(
      databaseFactoryFfi,
      '${directory.path}/$key.db',
    );
    first = PersonalRepository(
      accountKey: 'a' * 64,
      isCurrent: () => active,
      open: open,
    );
    second = PersonalRepository(
      accountKey: 'b' * 64,
      isCurrent: () => true,
      open: open,
    );
  });
  tearDown(() async {
    await first.close();
    await second.close();
    await directory.delete(recursive: true);
  });
  PersonalItem note(String text) =>
      PersonalItem.create(PersonalKind.note).copyWith(title: text, body: '正文');
  PersonalArchive legacy(List<PersonalItem> items) => PersonalArchive.decode(
    utf8.encode(
      jsonEncode({
        'notes': items.map((item) => item.toJson()).toList(),
        'todos': [],
      }),
    ),
  );

  test('accounts use different durable databases and stale sessions cannot read or write', () async {
    await first.save(note('仅属于第一个账号'));
    expect(await second.list(), isEmpty);
    active = false;
    await expectLater(
      first.list(),
      throwsA(
        isA<AppFailure>().having((e) => e.kind, 'kind', FailureKind.cancelled),
      ),
    );
    await expectLater(first.save(note('迟到写入')), throwsA(isA<AppFailure>()));
  });
  test('unowned legacy data requires explicit ownership and import is idempotent after edits', () async {
    final original = note('旧笔记');
    final archive = legacy([original]);
    expect(
      () => first.import(archive, confirmOwnership: false),
      throwsA(isA<AppFailure>()),
    );
    expect(await first.import(archive, confirmOwnership: true), 1);
    await first.save(original.copyWith(title: '导入后修改'));
    expect(await first.import(archive, confirmOwnership: true), 0);
    expect((await first.list()).single.title, '导入后修改');
    final backup = PersonalArchive.decode(await first.export());
    expect(
      () => second.import(backup, confirmOwnership: false),
      throwsA(isA<AppFailure>()),
    );
  });
  test(
    'ID conflicts retain both contents, including note and todo with equal IDs',
    () async {
      final original = note('当前内容');
      await first.save(original);
      final imported = original.copyWith(title: '备份内容');
      final todo = PersonalItem.create(PersonalKind.todo).copyWith(
        id: original.id,
        title: '待办',
        priority: 2,
        dueAt: DateTime.utc(2027),
      );
      final archive = PersonalArchive.decode(
        utf8.encode(
          jsonEncode({
            'notes': [imported.toJson()],
            'todos': [todo.toJson()],
          }),
        ),
      );
      expect(await first.import(archive, confirmOwnership: true), 2);
      final values = await first.list();
      expect(values.map((item) => item.title).toSet(), {'当前内容', '备份内容', '待办'});
      expect(
        values
            .where((item) => item.kind == PersonalKind.note)
            .map((item) => item.id)
            .toSet()
            .length,
        2,
      );
      final backup = PersonalArchive.decode(await first.export());
      expect(backup.owner, first.accountKey);
      expect(backup.items.length, 3);
      expect(
        backup.items
            .singleWhere((item) => item.kind == PersonalKind.todo)
            .dueAt,
        DateTime.utc(2027),
      );
    },
  );
  test('invalid archive leaves existing contents untouched', () async {
    await first.save(note('原始资料'));
    final broken = {
      'notes': [
        note('可读但不能部分导入').toJson(),
        {'id': 'broken'},
      ],
      'todos': [],
    };
    expect(
      () => PersonalArchive.decode(utf8.encode(jsonEncode(broken))),
      throwsA(isA<AppFailure>()),
    );
    expect((await first.list()).single.title, '原始资料');
  });
  test(
    'logout during transaction rolls every imported row and marker back',
    () async {
      var checks = 0;
      final interruptible = PersonalRepository(
        accountKey: 'c' * 64,
        isCurrent: () => ++checks < 4,
        open: (key) => initializePersonalDatabase(
          databaseFactoryFfi,
          '${directory.path}/$key.db',
        ),
      );
      final archive = legacy([note('第一条'), note('第二条')]);
      await expectLater(
        interruptible.import(archive, confirmOwnership: true),
        throwsA(isA<AppFailure>()),
      );
      await interruptible.close();
      final reopened = PersonalRepository(
        accountKey: 'c' * 64,
        isCurrent: () => true,
        open: (key) => initializePersonalDatabase(
          databaseFactoryFfi,
          '${directory.path}/$key.db',
        ),
      );
      expect(await reopened.list(), isEmpty);
      expect(await reopened.import(archive, confirmOwnership: true), 2);
      await reopened.close();
    },
  );
  test('completed cleanup preserves notes and incomplete todos', () async {
    await first.save(note('笔记'));
    await first.save(
      PersonalItem.create(PersonalKind.todo).copyWith(title: '待完成'),
    );
    await first.save(
      PersonalItem.create(PersonalKind.todo)
          .copyWith(title: '已完成', completed: true),
    );
    expect(await first.deleteCompleted(), 1);
    expect((await first.list()).map((item) => item.title).toSet(), {
      '笔记',
      '待完成',
    });
  });
  test(
    'large SQLite backups restore every part to a confirmed owner exactly once',
    () async {
      final body = '界' * 1000000;
      for (var index = 0; index < 6; index++) {
        await first.save(note('大笔记 $index').copyWith(body: body));
      }
      final parts = await first.exportParts();
      expect(parts, hasLength(2));
      var imported = 0;
      for (final bytes in parts) {
        expect(bytes.length, lessThanOrEqualTo(PersonalArchive.maxBytes));
        final archive = PersonalArchive.decode(bytes);
        expect(
          () => second.import(archive, confirmOwnership: false),
          throwsA(isA<AppFailure>()),
        );
        imported += await second.import(archive, confirmOwnership: true);
        expect(await second.import(archive, confirmOwnership: true), 0);
      }
      expect(imported, 6);
      final restored = await second.list();
      expect(restored, hasLength(6));
      expect(restored.map((item) => item.title).toSet(), {
        for (var index = 0; index < 6; index++) '大笔记 $index',
      });
      expect(restored.every((item) => item.body == body), isTrue);
      final secondExport = await second.exportParts();
      expect(
        secondExport.every(
          (part) => PersonalArchive.decode(part).owner == second.accountKey,
        ),
        isTrue,
      );
    },
  );
}
