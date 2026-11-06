import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:file_selector/file_selector.dart';
// Public plugin testing boundary, already pinned transitively by file_selector.
// ignore: depend_on_referenced_packages
import 'package:file_selector_platform_interface/file_selector_platform_interface.dart'
    show FileSelectorPlatform;
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/app_runtime.dart';
import 'package:imapp/core/config/app_config.dart';
import 'package:imapp/core/config/route_config_loader.dart';
import 'package:imapp/core/tenant/session_coordinator.dart';
import 'package:imapp/core/tenant/session_credentials.dart';
import 'package:imapp/features/personal/data/personal_archive.dart';
import 'package:imapp/features/personal/data/personal_migration.dart';
import 'package:imapp/features/personal/data/personal_repository.dart';
import 'package:imapp/features/personal/domain/personal_item.dart';
import 'package:imapp/features/personal/personal_provider.dart';
import 'package:imapp/features/personal/presentation/personal_page.dart';
import 'package:imapp/providers.dart';
import 'package:sqflite/sqflite.dart';

import '../../core/privacy_bootstrap_test.dart'
    show ConsentMemory, StoreProbe, ImProbe;

// Exercise the actual page, public OS-picker boundary, repository and
// transaction dispatch. Native selection and SQLite are controlled doubles.
class _Picker extends FileSelectorPlatform {
  final result = Completer<XFile?>();
  final multiple = Completer<List<XFile>>();

  @override
  Future<List<XFile>> openFiles({
    List<XTypeGroup>? acceptedTypeGroups,
    String? initialDirectory,
    String? confirmButtonText,
  }) => multiple.future;

  @override
  Future<XFile?> openFile({
    List<XTypeGroup>? acceptedTypeGroups,
    String? initialDirectory,
    String? confirmButtonText,
  }) => result.future;
}

class _DatabaseSpy implements Database, Transaction {
  _DatabaseSpy({this.noteTitle});
  final String? noteTitle;
  int imports = 0;
  int deletes = 0;

  @override
  Future<List<Map<String, Object?>>> query(
    String table, {
    bool? distinct,
    List<String>? columns,
    String? where,
    List<Object?>? whereArgs,
    String? groupBy,
    String? having,
    String? orderBy,
    int? limit,
    int? offset,
  }) async => table == 'personal_items'
      ? [
          {
            'kind': noteTitle == null ? 'todo' : 'note',
            'id': 'fixture-completed',
            'payload':
                PersonalItem.create(
                      noteTitle == null ? PersonalKind.todo : PersonalKind.note,
                    )
                    .copyWith(
                      id: 'fixture-completed',
                      completed: true,
                      title: noteTitle,
                    )
                    .encoded,
          },
        ]
      : [];

  @override
  Future<T> transaction<T>(
    Future<T> Function(Transaction txn) action, {
    bool? exclusive,
  }) => action(this);

  @override
  Future<int> rawInsert(String sql, [List<Object?>? arguments]) async {
    imports++;
    return 1;
  }

  @override
  Future<List<Map<String, Object?>>> rawQuery(
    String sql, [
    List<Object?>? arguments,
  ]) async => [
    {'changes()': 1},
  ];

  @override
  Future<int> insert(
    String table,
    Map<String, Object?> values, {
    String? nullColumnHack,
    ConflictAlgorithm? conflictAlgorithm,
  }) async => 1;

  @override
  Future<int> delete(
    String table, {
    String? where,
    List<Object?>? whereArgs,
  }) async {
    deletes++;
    return 1;
  }

  @override
  Future<void> close() async {}

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class _MigrationDatabaseSpy extends _DatabaseSpy {
  final sets = <Map<String, Object?>>[];
  final parts = <Map<String, Object?>>[];
  @override
  Future<List<Map<String, Object?>>> query(
    String table, {bool? distinct, List<String>? columns, String? where,
    List<Object?>? whereArgs, String? groupBy, String? having, String? orderBy,
    int? limit, int? offset,
  }) async {
    if (table == 'migration_sets') return sets;
    if (table == 'migration_parts') return parts;
    if (table == 'personal_items' && where != null) return [];
    return super.query(table, distinct: distinct, columns: columns, where: where,
      whereArgs: whereArgs, groupBy: groupBy, having: having, orderBy: orderBy,
      limit: limit, offset: offset);
  }
  @override
  Future<int> insert(String table, Map<String, Object?> values,
      {String? nullColumnHack, ConflictAlgorithm? conflictAlgorithm}) async {
    if (table == 'migration_sets') sets.add(Map.of(values));
    if (table == 'migration_parts') parts.add(Map.of(values));
    if (table == 'personal_items') imports++;
    return 1;
  }
}

List<XFile> _migrationFiles() {
  final item = PersonalItem.create(PersonalKind.note).copyWith(id: 'old-note', title: '分卷资料');
  final raw = utf8.encode(jsonEncode({'local_notes': jsonEncode([item.toJson()]), 'local_todos': '[]'}));
  final report = utf8.encode('[]');
  final setId = personalMigrationSha(raw);
  final part = utf8.encode(jsonEncode({'format': 'luobo-personal-migration-part',
    'version': 1, 'set': setId, 'index': 0,
    'records': [{'kind': 'note', 'sourceIndex': 0, 'item': item.toJson()}]}));
  final manifest = utf8.encode(jsonEncode({'format': 'luobo-personal-migration',
    'version': 1, 'applicationId': 'com.luoboduihua.imapp', 'owner': null, 'set': setId,
    'raw': {'file': 'original.json', 'bytes': raw.length, 'sha256': setId},
    'report': {'file': 'unmigrated.json', 'bytes': report.length, 'sha256': personalMigrationSha(report)},
    'sourceCounts': {'note': 1, 'todo': 0}, 'rejected': 0, 'structuralErrors': [],
    'parts': [{'index': 0, 'file': 'part-0.json', 'bytes': part.length,
      'sha256': personalMigrationSha(part), 'sources': {'note': [0, 0, 1], 'todo': null}}]}));
  return [for (final entry in {'manifest.json': manifest, 'original.json': raw,
      'unmigrated.json': report, 'part-0.json': part}.entries)
    XFile.fromData(Uint8List.fromList(entry.value), name: entry.key, mimeType: 'application/json')];
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  Future<
    ({
      _DatabaseSpy first,
      _DatabaseSpy second,
      Completer<XFile?> picker,
      Completer<List<XFile>> migrationPicker,
      Future<void> Function() switchAccount,
      String Function() owner,
    })
  >
  mount(
    WidgetTester tester,
    PersonalKind kind, {
    bool withNotes = false,
    bool migration = false,
  }) async {
    final session = SessionCoordinator(store: StoreProbe(), im: ImProbe());
    SessionCredentials credentials(String id) => SessionCredentials(
      userId: id,
      tenantId: 'fixture',
      tenantName: 'fixture',
      domains: ['tenant.test'],
      chatToken: 'fixture-chat-$id',
      imToken: 'fixture-im-$id',
    );
    final runtime = AppRuntime(
      config: const AppConfig(),
      consent: ConsentMemory(),
      routes: RouteConfigLoader(config: const AppConfig()),
      session: session,
      auth: (_, _) async => throw StateError('unused'),
    )..phase = AppPhase.online;
    final first = migration ? _MigrationDatabaseSpy() : _DatabaseSpy(noteTitle: withNotes ? '第一账号资料' : null);
    final second = migration ? _MigrationDatabaseSpy() : _DatabaseSpy(noteTitle: withNotes ? '第二账号资料' : null);
    var current = 'first';
    final repositories = {
      'first': PersonalRepository(
        accountKey: credentials('first').accountKey,
        isCurrent: () => current == 'first',
        open: (_) async => first,
      ),
      'second': PersonalRepository(
        accountKey: credentials('second').accountKey,
        isCurrent: () => current == 'second',
        open: (_) async => second,
      ),
    };
    final container = ProviderContainer(
      overrides: [
        appRuntimeProvider.overrideWith((ref) => runtime),
        personalRepositoryProvider.overrideWith(
          (ref) => repositories[current]!,
        ),
      ],
    );
    final previousPicker = FileSelectorPlatform.instance;
    final picker = _Picker();
    FileSelectorPlatform.instance = picker;
    addTearDown(() {
      if (!picker.result.isCompleted) picker.result.complete(null);
      if (!picker.multiple.isCompleted) picker.multiple.complete([]);
      FileSelectorPlatform.instance = previousPicker;
      container.dispose();
      // The runtime provider owns and disposes the session. These databases
      // are in-memory spies, so teardown does not wait for a fake-clock stream.
      unawaited(repositories['first']!.close());
      unawaited(repositories['second']!.close());
    });
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: MaterialApp(home: PersonalPage(kind: kind)),
      ),
    );
    await tester.pumpAndSettle();
    return (
      first: first,
      second: second,
      picker: picker.result,
      migrationPicker: picker.multiple,
      switchAccount: () async {
        current = 'second';
        container.invalidate(personalRepositoryProvider);
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 350));
      },
      owner: () => repositories[current]!.accountKey,
    );
  }

  Future<void> menu(WidgetTester tester, String action) async {
    await tester.tap(find.byTooltip('资料管理'));
    await tester.pumpAndSettle();
    await tester.tap(find.text(action));
    // The page intentionally animates a busy indicator while waiting for the
    // OS picker or confirmation. Waiting for all frames would never settle.
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 350));
  }

  testWidgets('late multipart picker cannot import or confirm the replacement account', (tester) async {
    final f = await mount(tester, PersonalKind.note, migration: true);
    await menu(tester, '导入分卷资料');
    await f.switchAccount();
    f.migrationPicker.complete(_migrationFiles());
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 350));
    expect(find.text('导入旧版分卷资料'), findsNothing);
    expect((f.first as _MigrationDatabaseSpy).sets, isEmpty);
    expect((f.second as _MigrationDatabaseSpy).sets, isEmpty);
    expect(f.first.imports + f.second.imports, 0);
    await tester.pumpWidget(const SizedBox.shrink());
  });

  for (final switching in [false, true]) {
    testWidgets(switching ? 'multipart ownership confirmation expires on account switch' :
        'multipart unknown owner needs explicit consent before same-account writes', (tester) async {
      final f = await mount(tester, PersonalKind.note, migration: true);
      await menu(tester, '导入分卷资料');
      f.migrationPicker.complete(_migrationFiles());
      for (var i = 0; i < 12; i++) { await tester.pump(const Duration(milliseconds: 50)); }
      expect(find.text('导入旧版分卷资料'), findsOneWidget);
      final confirm = find.widgetWithText(FilledButton, '导入');
      expect(tester.widget<FilledButton>(confirm).onPressed, isNull);
      expect((f.first as _MigrationDatabaseSpy).sets, isEmpty);
      final ownership = find.byType(CheckboxListTile);
      await tester.ensureVisible(ownership);
      await tester.tap(ownership);
      await tester.pump();
      expect(tester.widget<FilledButton>(confirm).onPressed, isNotNull);
      if (switching) await f.switchAccount();
      await tester.tap(confirm);
      for (var i = 0; i < 12; i++) { await tester.pump(const Duration(milliseconds: 50)); }
      expect(f.first.imports, switching ? 0 : 1);
      expect(f.second.imports, 0);
      expect((f.first as _MigrationDatabaseSpy).parts.length, switching ? 0 : 1);
      expect((f.second as _MigrationDatabaseSpy).sets, isEmpty);
      await tester.pumpWidget(const SizedBox.shrink());
    });
  }

  testWidgets('repository replacement refreshes visible account data', (
    tester,
  ) async {
    final f = await mount(tester, PersonalKind.note, withNotes: true);
    expect(find.text('第一账号资料'), findsOneWidget);
    await f.switchAccount();
    await tester.pumpAndSettle();
    expect(find.text('第一账号资料'), findsNothing);
    expect(find.text('第二账号资料'), findsOneWidget);
    await tester.pumpWidget(const SizedBox.shrink());
  });

  for (final switching in [true, false]) {
    testWidgets(
      switching
          ? 'an old OS picker result cannot import into the replacement account'
          : 'same-account OS picker confirmation still imports once',
      (tester) async {
        final f = await mount(tester, PersonalKind.note);
        await menu(tester, '导入笔记与待办');
        if (switching) await f.switchAccount();
        // Backup belongs to B in the stale-picker case. It must not borrow B's
        // current repository and thereby skip the cross-owner confirmation.
        f.picker.complete(
          XFile.fromData(
            Uint8List.fromList(
              PersonalArchive.encode(f.owner(), [
                PersonalItem.create(PersonalKind.note).copyWith(title: '导入样本'),
              ]),
            ),
            name: 'fixture.json',
            mimeType: 'application/json',
          ),
        );
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 350));
        if (find.widgetWithText(FilledButton, '导入').evaluate().isNotEmpty) {
          await tester.tap(find.widgetWithText(FilledButton, '导入'));
          await tester.pumpAndSettle();
        }
        expect(f.first.imports, switching ? 0 : 1);
        expect(f.second.imports, 0);
        expect(find.text('导入本地资料'), findsNothing);
        await tester.pumpWidget(const SizedBox.shrink());
      },
    );
  }

  for (final switching in [true, false]) {
    testWidgets(
      switching
          ? 'an old clear confirmation cannot delete replacement-account todos'
          : 'same-account clear confirmation still deletes completed todos',
      (tester) async {
        final f = await mount(tester, PersonalKind.todo);
        await menu(tester, '清理已完成');
        expect(find.text('删除已完成的待办？'), findsOneWidget);
        if (switching) await f.switchAccount();
        await tester.tap(find.widgetWithText(FilledButton, '确认'));
        await tester.pumpAndSettle();
        expect(f.first.deletes, switching ? 0 : 1);
        expect(f.second.deletes, 0);
        await tester.pumpWidget(const SizedBox.shrink());
      },
    );
  }
}
