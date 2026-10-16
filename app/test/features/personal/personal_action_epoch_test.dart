import 'dart:async';
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

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  Future<
    ({
      _DatabaseSpy first,
      _DatabaseSpy second,
      Completer<XFile?> picker,
      Future<void> Function() switchAccount,
      String Function() owner,
    })
  >
  mount(
    WidgetTester tester,
    PersonalKind kind, {
    bool withNotes = false,
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
    final first = _DatabaseSpy(noteTitle: withNotes ? '第一账号资料' : null);
    final second = _DatabaseSpy(noteTitle: withNotes ? '第二账号资料' : null);
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
