import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';
import 'package:imapp/providers.dart';

import 'batch_harness.dart';
import '../../core/im/sdk_harness.dart';

void main() {
  late BatchHarness h;
  setUp(() {
    h = BatchHarness();
  });
  Future<void> mount(WidgetTester tester) async {
    await tester.runAsync(h.start);
    tester.view.physicalSize = const Size(360, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          imGatewayProvider.overrideWithValue(h.sdk.gateway),
          appRuntimeProvider.overrideWith((_) => h.sdk.runtime),
        ],
        child: MaterialApp(
          builder: (context, child) => MediaQuery(
            data: MediaQuery.of(context)
                .copyWith(textScaler: const TextScaler.linear(1.6)),
            child: child!,
          ),
          home: const ChatPage(conversationId: 'single-alice-bob'),
        ),
      ),
    );
    await tester.pumpAndSettle();
  }

  Future<void> close(WidgetTester tester) async {
    await tester.runAsync(h.dispose);
    await tester.pumpWidget(const SizedBox.shrink());
  }

  Future<void> begin(WidgetTester tester, {String text = '原消息1'}) async {
    await tester.ensureVisible(find.text(text));
    await tester.pumpAndSettle();
    await tester.longPress(find.text(text).hitTestable());
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.text('多选消息'));
    await tester.tap(find.text('多选消息'));
    await tester.pumpAndSettle();
  }

  Future<void> selectAll(WidgetTester tester) async {
    await begin(tester);
    final boxes = find.byType(Checkbox);
    for (final element in boxes.evaluate().toList()) {
      final box = element.widget as Checkbox;
      if (box.value == false) await tester.tap(find.byWidget(box));
    }
    await tester.pumpAndSettle();
  }

  testWidgets(
    'long press enters bounded checkbox mode with count and cancel on360 large text',
    (tester) async {
      await mount(tester);
      try {
        await begin(tester);
        expect(find.text('已选 1 条'), findsOneWidget);
        expect(find.byType(Checkbox), findsNWidgets(3));
        expect(find.byTooltip('发送'), findsNothing);
        final unselected = find
            .byWidgetPredicate(
              (widget) => widget is Checkbox && widget.value == false,
            )
            .hitTestable()
            .first;
        await tester.tap(unselected);
        await tester.pump();
        expect(find.text('已选 2 条'), findsOneWidget);
        await tester.tap(find.text('取消多选'));
        await tester.pumpAndSettle();
        expect(find.byType(Checkbox), findsNothing);
        expect(find.byTooltip('发送'), findsOneWidget);
        expect(h.count('deleteMessageFromLocalAndSvr'), 0);
        expect(tester.takeException(), isNull);
      } finally {
        await close(tester);
      }
    },
  );
  testWidgets(
    'the21st visible checkbox tap keeps20 selected and displays the legacy limit',
    (tester) async {
      for (var i = 4; i <= 21; i++) {
        h.sdk.messages.add({
          ...SdkHarness.message(id: 'm$i', text: '原消息$i'),
          'sendTime': 1700000000000 + i,
        });
      }
      await mount(tester);
      try {
        // Keep this cap-only test independent from virtualized scrolling.
        // The separate first case retains the360px/1.6 layout assertions.
        tester.view.physicalSize = const Size(360, 8000);
        await tester.pumpAndSettle();
        await begin(tester);
        expect(find.byType(Checkbox), findsNWidgets(21));
        for (var i = 0; i < 20; i++) {
          final unselected = find
              .byWidgetPredicate(
                (widget) => widget is Checkbox && widget.value == false,
              )
              .hitTestable()
              .first;
          await tester.tap(unselected);
          await tester.pump();
        }
        expect(
          find.byWidgetPredicate(
            (widget) => widget is Checkbox && widget.value == true,
          ),
          findsNWidgets(20),
        );
        expect(find.text('已选 20 条'), findsOneWidget);
        expect(find.text('最多选择 20 条消息'), findsOneWidget);
        expect(h.count('deleteMessageFromLocalAndSvr'), 0);
        expect(h.count('sendMessage'), 0);
        expect(tester.takeException(), isNull);
      } finally {
        await close(tester);
      }
    },
  );
  testWidgets(
    'batch deletion requires a second explicit confirmation and cancelling submits nothing',
    (tester) async {
      await mount(tester);
      try {
        await selectAll(tester);
        await tester.tap(find.text('删除（3）'));
        await tester.pumpAndSettle();
        expect(find.text('删除所选消息？'), findsOneWidget);
        expect(h.count('deleteMessageFromLocalAndSvr'), 0);
        await tester.tap(find.text('取消'));
        await tester.pumpAndSettle();
        expect(h.count('deleteMessageFromLocalAndSvr'), 0);
        expect(find.text('已选 3 条'), findsOneWidget);
      } finally {
        await close(tester);
      }
    },
  );
  testWidgets(
    'partial deletion displays ACK unknown and unattempted counts without a false all-success message',
    (tester) async {
      await mount(tester);
      try {
        h.intercept = (call) async {
          if (call.method == 'deleteMessageFromLocalAndSvr' &&
              (call.arguments as Map)['clientMsgID'] == 'm2') {
            throw PlatformException(code: '10005');
          }
          return h.respond(call);
        };
        await selectAll(tester);
        await tester.tap(find.text('删除（3）'));
        await tester.pumpAndSettle();
        await tester.tap(find.text('确认'));
        await tester.pumpAndSettle();
        expect(find.textContaining('1 条结果未确认'), findsOneWidget);
        expect(find.textContaining('剩余 1 条未提交'), findsOneWidget);
        expect(h.count('deleteMessageFromLocalAndSvr'), 2);
        expect(h.count('deleteMessageFromLocalStorage'), 0);
        expect(find.text('原消息1'), findsNothing);
        expect(find.text('原消息3'), findsOneWidget);
      } finally {
        await close(tester);
      }
    },
  );
  testWidgets(
    'private facts learned while confirmation is open invalidate that exact intent before any deletion',
    (tester) async {
      await mount(tester);
      try {
        await selectAll(tester);
        await tester.tap(find.text('删除（3）'));
        await tester.pumpAndSettle();
        h.sdk.gateway.privacyLedger.observe('single-alice-bob', {
          'clientMsgID': 'm2',
          'attachedInfoElem': {'isPrivateChat': true},
        }, now: DateTime.now());
        await tester.pump();
        await tester.tap(find.text('确认'));
        await tester.pumpAndSettle();
        expect(h.count('deleteMessageFromLocalAndSvr'), 0);
        expect(h.count('deleteMessageFromLocalStorage'), 0);
      } finally {
        await close(tester);
      }
    },
  );
  testWidgets(
    'merge reuses one stable-ID destination picker and generic count confirmation without copying bodies into its dialog',
    (tester) async {
      await mount(tester);
      try {
        await selectAll(tester);
        await tester.tap(find.text('合并转发'));
        await tester.pumpAndSettle();
        expect(find.text('账号 ID：carol'), findsOneWidget);
        await tester.tap(find.text('目标会话'));
        await tester.pumpAndSettle();
        final dialog = find.byType(AlertDialog);
        expect(
          find.descendant(of: dialog, matching: find.textContaining('3 条消息')),
          findsOneWidget,
        );
        expect(
          find.descendant(of: dialog, matching: find.textContaining('原消息')),
          findsNothing,
        );
        await tester.tap(find.text('发送'));
        await tester.pumpAndSettle();
        expect(h.count('createMergerMessage'), 1);
        expect(h.count('sendMessage'), 1);
        expect(find.byType(Checkbox), findsNothing);
        expect(tester.takeException(), isNull);
      } finally {
        await close(tester);
      }
    },
  );
  testWidgets(
    'a merge send with unknown ACK is terminal in the picker and a second tap cannot repeat it',
    (tester) async {
      await mount(tester);
      try {
        h.intercept = (call) async {
          if (call.method == 'sendMessage') {
            throw PlatformException(code: '10005');
          }
          return h.respond(call);
        };
        await selectAll(tester);
        await tester.tap(find.text('合并转发'));
        await tester.pumpAndSettle();
        await tester.tap(find.text('目标会话'));
        await tester.pumpAndSettle();
        await tester.tap(find.text('发送'));
        await tester.pumpAndSettle();
        expect(find.textContaining('转发结果未确认'), findsWidgets);
        final tile = tester.widget<ListTile>(
          find.ancestor(of: find.text('目标会话'), matching: find.byType(ListTile)),
        );
        expect(tile.onTap, isNull);
        expect(h.count('sendMessage'), 1);
        expect(h.count('createMergerMessage'), 1);
      } finally {
        await close(tester);
      }
    },
  );
}
