import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/contacts/presentation/select_contacts_page.dart';
import 'package:imapp/features/group/presentation/create_group_page.dart';

import 'create_group_fixture.dart';

Future<void> select(WidgetTester tester, CreationFixture f, {bool both = true}) async {
  await tester.tap(find.text('发起聊天')); await f.frame(tester);
  await f.until(tester, () => find.byType(CheckboxListTile).evaluate().length == 2);
  await tester.tap(find.byType(CheckboxListTile).at(0)); await tester.pump();
  if (both) { await tester.tap(find.byType(CheckboxListTile).at(1)); await tester.pump(); }
  await tester.tap(find.text(both ? '确定 (2)' : '确定 (1)')); await f.frame(tester);
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  testWidgets('normal multi friend flow reaches actual group editor and avatar entry without creating', (tester) async {
    final f = CreationFixture();
    try {
      await f.mount(tester, entry: true); await select(tester, f);
      expect(find.byType(CreateGroupPage), findsOneWidget);
      expect(find.byKey(const ValueKey('create-group-avatar')), findsOneWidget);
      expect(f.creates, isEmpty); expect(f.providerBodies, isEmpty);
      expect(find.text('ID：bob'), findsOneWidget); expect(find.text('ID：carol'), findsOneWidget);
    } finally { await f.close(tester); }
  });
  testWidgets('single friend selection keeps the original single chat capability', (tester) async {
    final f = CreationFixture();
    try {
      await f.mount(tester, entry: true); await select(tester, f, both: false);
      await f.until(tester, () => find.text('chat:single-cid').evaluate().isNotEmpty);
      expect(find.byType(CreateGroupPage), findsNothing); expect(f.creates, isEmpty);
    } finally { await f.close(tester); }
  });
  testWidgets('returning before creation preserves selected people for explicit editing', (tester) async {
    final f = CreationFixture();
    try {
      await f.mount(tester, entry: true); await select(tester, f);
      await tester.pageBack(); await f.frame(tester);
      expect(find.byType(SelectContactsPage), findsOneWidget);
      expect(tester.widgetList<CheckboxListTile>(find.byType(CheckboxListTile)).where((x) => x.value == true), hasLength(2));
      await tester.tap(find.byType(CheckboxListTile).at(1)); await tester.pump();
      expect(find.text('确定 (1)'), findsOneWidget); expect(f.creates, isEmpty);
    } finally { await f.close(tester); }
  });
  testWidgets('small large text editor keeps stable member IDs and creation action reachable', (tester) async {
    final f = CreationFixture();
    try {
      await f.mount(tester, width: 320, height: 568);
      final create = find.widgetWithText(FilledButton, '创建群聊');
      await tester.scrollUntilVisible(create, 80,
        scrollable: find.descendant(of: find.byType(ListView), matching: find.byType(Scrollable)).first);
      await tester.pump();
      expect(create.hitTestable(), findsOneWidget);
      expect(find.text('ID：bob'), findsOneWidget); expect(find.text('ID：carol'), findsOneWidget);
      expect(tester.takeException(), isNull); expect(f.creates, isEmpty);
    } finally { await f.close(tester); }
  });
  testWidgets('old selected people and submit closure cannot acquire replacement owner repositories', (tester) async {
    final f = CreationFixture();
    try {
      await f.mount(tester, entry: true);
      await tester.tap(find.text('发起聊天')); await f.frame(tester);
      await f.until(tester, () => find.byType(CheckboxListTile).evaluate().length == 2);
      await tester.tap(find.byType(CheckboxListTile).at(0)); await tester.pump();
      await tester.tap(find.byType(CheckboxListTile).at(1)); await tester.pump();
      final old = tester.widget<TextButton>(find.widgetWithText(TextButton, '确定 (2)')).onPressed!;
      await tester.runAsync(f.replaceOwner); await f.frame(tester);
      old(); await f.frame(tester);
      expect(find.text('会话已变化，请返回后重新选择好友'), findsOneWidget);
      expect(find.byType(CreateGroupPage), findsNothing); expect(f.creates, isEmpty);
      expect(f.base.sdk.calls.where((c) => c.method == 'getOneConversation'), isEmpty);
    } finally { await f.close(tester); }
  });
  testWidgets('old selector submit cannot revive after foreign navigation ABA while new click remains usable', (tester) async {
    final f = CreationFixture();
    try {
      await f.mount(tester, entry: true);
      await tester.tap(find.text('发起聊天')); await f.frame(tester);
      await f.until(tester, () => find.byType(CheckboxListTile).evaluate().length == 2);
      await tester.tap(find.byType(CheckboxListTile).at(0)); await tester.pump();
      await tester.tap(find.byType(CheckboxListTile).at(1)); await tester.pump();
      final old = tester.widget<TextButton>(find.widgetWithText(TextButton, '确定 (2)')).onPressed!;
      await f.coverAndReturn(tester); old(); await f.frame(tester);
      expect(find.byType(CreateGroupPage), findsNothing); expect(f.creates, isEmpty);
      await tester.tap(find.widgetWithText(TextButton, '确定 (2)')); await f.frame(tester);
      expect(find.byType(CreateGroupPage), findsOneWidget); expect(f.creates, isEmpty);
    } finally { await f.close(tester); }
  });
}
