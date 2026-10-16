import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import 'batch_remove_fixture.dart';

void main() {
  late BatchFixture f;
  setUp(() => f = BatchFixture());

  testWidgets(
    'same-name targets retain exact visible UID and individually acknowledged results',
    (tester) async {
      await f.mount(tester);
      await f.choose(tester, ['bob', 'carol']);
      expect(find.textContaining(RegExp(r'^bob(?:\n|$)')), findsOneWidget);
      expect(find.textContaining(RegExp(r'^carol(?:\n|$)')), findsOneWidget);
      await f.confirm(tester);
      await until(
        tester,
        () =>
            f.kickCalls.length == 2 &&
            find.text('移除结果：已确认 2 / 2').evaluate().isNotEmpty,
      );
      expect(f.kickCalls.map((r) => r['userIDList']), [
        ['bob'],
        ['carol'],
      ]);
      expect(tester.takeException(), isNull);
      await f.close(tester);
    },
  );

  testWidgets(
    'cancelling confirmation preserves selection and dispatches nothing',
    (tester) async {
      await f.mount(tester);
      await f.choose(tester, ['bob', 'carol']);
      await tester.tap(f.submit);
      await frame(tester);
      expect(find.text('移除选中的 2 位成员？'), findsOneWidget);
      await tester.tap(find.widgetWithText(TextButton, '取消'));
      await frame(tester);
      expect(f.kickCalls, isEmpty);
      expect(find.text('已选 2 人'), findsOneWidget);
      await f.close(tester);
    },
  );

  testWidgets('admin can select ordinary members but not self or peer admin', (
    tester,
  ) async {
    f.roles['alice'] = 60;
    await f.mount(tester);
    await tester.tap(f.entry);
    await frame(tester);
    expect(
      tester
          .widget<CheckboxListTile>(
            find.byKey(const ValueKey('batch-member-alice')),
          )
          .onChanged,
      isNull,
    );
    expect(
      tester
          .widget<CheckboxListTile>(
            find.byKey(const ValueKey('batch-member-dave')),
          )
          .onChanged,
      isNull,
    );
    expect(
      tester
          .widget<CheckboxListTile>(
            find.byKey(const ValueKey('batch-member-bob')),
          )
          .onChanged,
      isNotNull,
    );
    await f.close(tester);
  });

  testWidgets(
    'fresh target promotion produces an explicit rejection then continues allowed item',
    (tester) async {
      f.roles['alice'] = 60;
      await f.mount(tester);
      await f.choose(tester, ['bob', 'carol']);
      await tester.tap(f.submit);
      await frame(tester);
      f.roles['bob'] = 60;
      await tester.tap(find.widgetWithText(FilledButton, '确认'));
      await frame(tester);
      await until(
        tester,
        () => find.text('移除结果：已确认 1 / 2').evaluate().isNotEmpty,
      );
      expect(f.kickCalls.single['userIDList'], ['carol']);
      expect(find.textContaining('未移除：'), findsOneWidget);
      await f.close(tester);
    },
  );

  testWidgets(
    'unknown dispatched result stops remainder, is visible, and cannot silently replay',
    (tester) async {
      await f.mount(tester);
      await f.choose(tester, ['bob', 'carol']);
      f.kickReply = (_, _) async => throw PlatformException(code: '1002');
      await f.confirm(tester);
      await until(
        tester,
        () => find.text('移除结果：已确认 0 / 2').evaluate().isNotEmpty,
      );
      expect(f.kickCalls.length, 1);
      expect(find.text('结果未确认，请刷新核对，本页不再重复移除'), findsOneWidget);
      expect(find.text('未执行'), findsOneWidget);
      await frame(tester);
      expect(f.kickCalls.length, 1);
      await f.close(tester);
    },
  );

  testWidgets(
    'unconfirmed target cannot bypass the batch guard through its single removal menu',
    (tester) async {
      await f.mount(tester);
      await f.choose(tester, ['bob', 'carol']);
      f.kickReply = (_, _) async => throw PlatformException(code: '1002');
      await f.confirm(tester);
      await until(
        tester,
        () => find.text('移除结果：已确认 0 / 2').evaluate().isNotEmpty,
      );
      expect(f.kickCalls.length, 1);
      await tester.tap(find.text('退出选择'));
      await frame(tester);
      final bobRow = find
          .ancestor(
            of: find.textContaining(RegExp(r'^bob(?:\n|$)')),
            matching: find.byType(ListTile),
          )
          .last;
      await tester.tap(bobRow);
      await frame(tester);
      final remove = tester.widget<ListTile>(
        find.widgetWithText(ListTile, '移出群聊'),
      );
      expect(remove.enabled, isFalse);
      expect(remove.onTap, isNull);
      await tester.tap(find.text('移出群聊'), warnIfMissed: false);
      await frame(tester);
      expect(find.text('移出群聊？'), findsNothing);
      expect(f.kickCalls.length, 1);
      await f.close(tester);
    },
  );

  testWidgets(
    '360 by 640 with large text keeps selection confirmation partial results and exit reachable',
    (tester) async {
      await f.mount(tester);
      tester.view.physicalSize = const Size(360, 640);
      await frame(tester);
      await f.choose(tester, ['bob', 'carol']);
      expect(tester.takeException(), isNull);
      expect(tester.getRect(f.submit).bottom, lessThanOrEqualTo(640));
      await tester.tap(f.submit);
      await frame(tester);
      expect(find.text('移除选中的 2 位成员？'), findsOneWidget);
      expect(find.text('bob'), findsOneWidget);
      expect(find.text('carol'), findsOneWidget);
      expect(tester.takeException(), isNull);
      f.kickReply = (_, index) async {
        if (index == 1) return '[]';
        throw PlatformException(code: '1002');
      };
      await tester.tap(find.widgetWithText(FilledButton, '确认'));
      await frame(tester);
      await until(
        tester,
        () => find.text('移除结果：已确认 1 / 2').evaluate().isNotEmpty,
      );
      expect(tester.takeException(), isNull);
      final exit = find.widgetWithText(TextButton, '退出选择');
      expect(tester.getRect(exit).bottom, lessThanOrEqualTo(640));
      await tester.tap(exit);
      await frame(tester);
      expect(f.entry, findsOneWidget);
      expect(f.kickCalls.length, 2);
      expect(tester.takeException(), isNull);
      await f.close(tester);
    },
  );

  testWidgets(
    'owner replacement while confirmation is open dispatches zero old intents',
    (tester) async {
      await f.mount(tester);
      await f.choose(tester, ['bob', 'carol']);
      await tester.tap(f.submit);
      await frame(tester);
      await tester.runAsync(f.switchOwner);
      await frame(tester);
      await tester.tap(find.widgetWithText(FilledButton, '确认'));
      await frame(tester);
      expect(f.kickCalls, isEmpty);
      expect(find.textContaining('移除结果'), findsNothing);
      await f.close(tester);
    },
  );

  testWidgets(
    'same mounted screen changing group cannot confirm old target list',
    (tester) async {
      await f.mount(tester);
      await f.choose(tester, ['bob', 'carol']);
      await tester.tap(f.submit);
      await frame(tester);
      f.groupId.value = 'group-b';
      await frame(tester);
      await tester.tap(find.widgetWithText(FilledButton, '确认'));
      await frame(tester);
      expect(f.kickCalls, isEmpty);
      await f.close(tester);
    },
  );

  testWidgets(
    'foreign route covering the owned confirmation then returning does not revive it',
    (tester) async {
      await f.mount(tester);
      await f.choose(tester, ['bob', 'carol']);
      await tester.tap(f.submit);
      await frame(tester);
      final confirm = find.widgetWithText(FilledButton, '确认');
      final owned = ModalRoute.of(tester.element(confirm))!;
      final foreign = MaterialPageRoute<void>(
        builder: (_) => const Scaffold(body: Text('外来页面')),
      );
      unawaited(f.navigator.currentState!.push<void>(foreign));
      await until(tester, () => foreign.isCurrent &&
        foreign.animation?.status == AnimationStatus.completed);
      var foreignRemoved = false;
      unawaited(foreign.completed.then<void>((_) { foreignRemoved = true; }));
      f.navigator.currentState!.pop();
      await until(tester, () => foreignRemoved && owned.isCurrent &&
        confirm.hitTestable().evaluate().length == 1);
      var ownedRemoved = false;
      unawaited(owned.completed.then<void>((_) { ownedRemoved = true; }));
      await tester.tap(confirm.hitTestable());
      // Completion of this exact confirmation proves its real onPressed ran;
      // a tap intercepted by the exiting foreign route cannot satisfy this.
      await until(tester, () => ownedRemoved);
      await frame(tester);
      expect(f.kickCalls, isEmpty);
      await f.close(tester);
    },
  );

  testWidgets(
    'role withdrawal while confirming reports no dispatched removals',
    (tester) async {
      await f.mount(tester);
      await f.choose(tester, ['bob', 'carol']);
      await tester.tap(f.submit);
      await frame(tester);
      f.roles['alice'] = 20;
      await tester.tap(find.widgetWithText(FilledButton, '确认'));
      await frame(tester);
      await until(
        tester,
        () => find.text('移除结果：已确认 0 / 2').evaluate().isNotEmpty,
      );
      expect(f.kickCalls, isEmpty);
      expect(find.textContaining('未移除：'), findsNWidgets(2));
      await f.close(tester);
    },
  );

  testWidgets(
    'late first ACK after group navigation cannot send second or show old progress',
    (tester) async {
      await f.mount(tester);
      await f.choose(tester, ['bob', 'carol']);
      final reply = Completer<Object?>();
      f.kickReply = (_, _) => reply.future;
      await f.confirm(tester);
      await until(tester, () => f.kickCalls.length == 1);
      f.groupId.value = 'group-b';
      await frame(tester);
      reply.complete('[]');
      await frame(tester);
      expect(f.kickCalls.length, 1);
      expect(find.textContaining('移除结果'), findsNothing);
      await f.close(tester);
    },
  );

  testWidgets(
    'selection survives normal searches and includes both frozen exact UIDs at confirmation',
    (tester) async {
      await f.mount(tester);
      await f.choose(tester, ['bob']);
      await tester.enterText(find.byType(TextField), 'carol');
      await tester.tap(find.byTooltip('搜索群成员'));
      await frame(tester);
      await until(
        tester,
        () => find
            .byKey(const ValueKey('batch-member-carol'))
            .evaluate()
            .isNotEmpty,
      );
      await f.choose(tester, ['carol']);
      expect(find.text('已选 2 人'), findsOneWidget);
      await f.confirm(tester);
      await until(tester, () => f.kickCalls.length == 2);
      expect(f.kickCalls.map((r) => r['userIDList']), [
        ['bob'],
        ['carol'],
      ]);
      await f.close(tester);
    },
  );

  testWidgets(
    'ordinary single-member menu and explicit removal confirmation remain available',
    (tester) async {
      await f.mount(tester);
      final row = find.widgetWithText(ListTile, '同名成员').first;
      await tester.tap(row);
      await frame(tester);
      await tester.tap(find.text('移出群聊'));
      await frame(tester);
      expect(f.kickCalls, isEmpty);
      await tester.tap(find.widgetWithText(FilledButton, '确认'));
      await frame(tester);
      await until(tester, () => f.kickCalls.length == 1);
      expect(f.kickCalls.single['userIDList'], ['bob']);
      await f.close(tester);
    },
  );

  testWidgets(
    'same owner unrelated metadata refresh does not erase the chosen IDs',
    (tester) async {
      await f.mount(tester);
      await f.choose(tester, ['bob', 'carol']);
      f.refreshProfile();
      await frame(tester);
      expect(find.text('已选 2 人'), findsOneWidget);
      await f.confirm(tester);
      await until(tester, () => f.kickCalls.length == 2);
      await f.close(tester);
    },
  );
  testWidgets('new owner pending read never exposes prior owner rows', (
    tester,
  ) async {
    await f.mount(tester);
    final entered = Completer<void>();
    final reply = Completer<List<Map<String, Object?>>>();
    f.readReply = (id, query, offset) {
      if (!entered.isCompleted) entered.complete();
      return reply.future;
    };
    // No old native operation is pending at login: the existing SDK quarantine
    // already forbids that case. This observes real A rows -> B loading scope.
    await tester.runAsync(() => f.switchOwner());
    await frame(tester);
    expect(entered.isCompleted, isTrue);
    expect(find.text('同名成员'), findsNothing);
    reply.complete([
      {
        'groupID': 'group-a',
        'userID': 'other',
        'nickname': '新账号列表',
        'roleLevel': 100,
      },
    ]);
    await until(tester, () => find.text('新账号列表').evaluate().isNotEmpty);
    expect(find.text('同名成员'), findsNothing);
    expect(f.kickCalls, isEmpty);
    await f.close(tester);
  });

  testWidgets('newer search read wins without late old-page list refill', (
    tester,
  ) async {
    await f.mount(tester);
    final old = Completer<List<Map<String, Object?>>>();
    f.readReply = (id, query, offset) =>
        query == 'old' ? old.future : Future.value([f.member(id, 'carol')]);
    await tester.enterText(find.byType(TextField), 'old');
    await tester.tap(find.byTooltip('搜索群成员'));
    await frame(tester);
    await tester.enterText(find.byType(TextField), 'new');
    await tester.tap(find.byTooltip('搜索群成员'));
    await frame(tester);
    await until(
      tester,
      () =>
          find.textContaining(RegExp(r'^carol(?:\n|$)')).evaluate().isNotEmpty,
    );
    old.complete([f.member('group-a', 'bob')]);
    await frame(tester);
    expect(find.textContaining(RegExp(r'^bob(?:\n|$)')), findsNothing);
    expect(find.textContaining(RegExp(r'^carol(?:\n|$)')), findsOneWidget);
    await f.close(tester);
  });
}
