import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';
import 'package:imapp/providers.dart';
import 'package:imapp/core/network/tenant_api.dart';

import 'mention_harness.dart';

/// These cases use existing ChatPage/MethodChannel public contracts so the440
/// baseline can demonstrate real missing UI behavior without new API stubs.
void main() {
  late MentionHarness h;
  late GoRouter router;
  Future<void> mount(WidgetTester tester) async {
    await tester.runAsync(h.sdk.start);
    tester.view.physicalSize = const Size(360, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    router = GoRouter(
      routes: [
        GoRoute(
          path: '/',
          builder: (_, _) => const ChatPage(conversationId: 'single-alice-bob'),
        ),
        GoRoute(
          path: '/contacts/user/:id',
          builder: (_, state) => Scaffold(
            body: Text(
              'member:${state.pathParameters['id']}/${state.uri.queryParameters['group']}',
            ),
          ),
        ),
        GoRoute(
          path: '/contacts/groups/:id',
          builder: (_, state) =>
              Scaffold(body: Text('group:${state.pathParameters['id']}')),
        ),
      ],
    );
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          imGatewayProvider.overrideWithValue(h.sdk.gateway),
          tenantApiProvider.overrideWithValue(h.api),
          appRuntimeProvider.overrideWith((_) => h.sdk.runtime),
        ],
        child: MaterialApp.router(
          routerConfig: router,
          builder: (context, child) => MediaQuery(
            data: MediaQuery.of(context)
                .copyWith(textScaler: const TextScaler.linear(1.6)),
            child: child!,
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
  }

  Future<void> close(WidgetTester tester) async {
    await tester.runAsync(h.dispose);
    await tester.pumpWidget(const SizedBox.shrink());
    router.dispose();
  }

  setUp(() {
    h = MentionHarness();
  });
  testWidgets(
    'reopened group draft explains lost bindings and sends plain text without an implicit ping',
    (tester) async {
      h.sdk.conversations.single['draftText'] = '@同名成员 ';
      await mount(tester);
      try {
        expect(find.text('草稿仅保留文字，请重新选择需要提醒的成员'), findsOneWidget);
        expect(find.byType(InputChip), findsNothing);
        await tester.tap(find.byTooltip('发送'));
        await tester.pumpAndSettle();
        expect(
          h.sdk.calls.where((c) => c.method == 'createTextAtMessage'),
          isEmpty,
        );
        expect(
          h.sdk.calls.where((c) => c.method == 'createTextMessage'),
          hasLength(1),
        );
        final args =
            h.sdk.calls
                    .singleWhere((c) => c.method == 'createTextMessage')
                    .arguments
                as Map;
        expect(args['text'], '@同名成员 ');
        expect(tester.takeException(), isNull);
      } finally {
        await close(tester);
      }
    },
  );
  testWidgets(
    'group composer opens a stable-ID member picker on360px large text and does not ping by handwritten name',
    (tester) async {
      await mount(tester);
      try {
        expect(find.byTooltip('提及群成员'), findsOneWidget);
        await tester.tap(find.byTooltip('提及群成员'));
        await tester.pumpAndSettle();
        expect(find.text('账号 ID：bob'), findsOneWidget);
        expect(find.text('账号 ID：carol'), findsOneWidget);
        expect(find.widgetWithText(ListTile, '同名成员'), findsNWidgets(2));
        expect(find.text('@所有人'), findsNothing);
        await tester.enterText(
          find.widgetWithText(TextField, '按群昵称或账号 ID 搜索'),
          'carol',
        );
        await tester.tap(find.byTooltip('搜索群成员'));
        await tester.pumpAndSettle();
        expect(find.text('账号 ID：bob'), findsNothing);
        expect(find.text('账号 ID：carol'), findsOneWidget);
        await tester.tap(find.text('账号 ID：carol'));
        await tester.pumpAndSettle();
        expect(find.text('@同名成员 · 账号 ID：carol'), findsOneWidget);
        expect(tester.takeException(), isNull);
        expect(h.sdk.calls.where((c) => c.method == 'sendMessage'), isEmpty);
      } finally {
        await close(tester);
      }
    },
  );
  testWidgets(
    'long press of an incoming group avatar inserts only that authoritative account mention',
    (tester) async {
      await mount(tester);
      try {
        await tester.longPress(find.byType(CircleAvatar).first);
        await tester.pumpAndSettle();
        expect(find.text('@同名成员 · 账号 ID：bob'), findsOneWidget);
        expect(
          h.sdk.calls.where((c) => c.method == 'createTextAtMessage'),
          isEmpty,
        );
      } finally {
        await close(tester);
      }
    },
  );
  for (final id in ['bob', 'AtAllTag']) {
    testWidgets(
      'click received explicit @$id routes by UID and all opens the group rather than a guessed user',
      (tester) async {
        h.sdk.messages[0] = {
          ...h.sdk.messages.single,
          'contentType': 106,
          'atTextElem': {
            'text': '@$id ',
            'atUserList': [id],
            'atUsersInfo': [
              {'atUserID': id, 'groupNickname': id == 'bob' ? '收到的提及' : '所有人'},
            ],
          },
        };
        await mount(tester);
        try {
          final rich = find.byWidgetPredicate(
            (widget) =>
                widget is Text &&
                widget.textSpan?.toPlainText().contains(
                      id == 'bob' ? '@收到的提及' : '@所有人',
                    ) ==
                    true,
          );
          expect(rich, findsOneWidget);
          await tester.tap(rich);
          await tester.pumpAndSettle();
          expect(
            find.text(id == 'bob' ? 'member:bob/group-a' : 'group:group-a'),
            findsOneWidget,
          );
          expect(h.sdk.calls.where((c) => c.method == 'sendMessage'), isEmpty);
        } finally {
          await close(tester);
        }
      },
    );
  }
  for (final hasOrigin in [true, false]) {
    testWidgets(
      'embedded original all ${hasOrigin ? 'opens only its original group' : 'without original group is display-only and never opens target group'}',
      (tester) async {
        final child = {
          ...h.sdk.messages.single,
          'clientMsgID': 'original-at',
          'contentType': 106,
          if (hasOrigin) 'groupID': 'original-group',
          'atTextElem': {
            'text': '@AtAllTag ',
            'atUserList': ['AtAllTag'],
            'atUsersInfo': <Object>[],
          },
        };
        if (!hasOrigin) child.remove('groupID');
        h.sdk.messages[0] = {
          ...h.sdk.messages.single,
          'clientMsgID': 'wrapper',
          'contentType': 107,
          'mergeElem': {
            'title': '转发的消息',
            'multiMessage': [child],
          },
        };
        await mount(tester);
        try {
          expect(find.text('转发的消息'), findsOneWidget);
          final rich = find.byWidgetPredicate(
            (widget) =>
                widget is Text &&
                widget.textSpan?.toPlainText().contains('@所有人') == true,
          );
          expect(rich, findsOneWidget);
          await tester.tap(rich);
          await tester.pumpAndSettle();
          expect(find.text('group:group-a'), findsNothing);
          expect(
            find.text('group:original-group'),
            hasOrigin ? findsOneWidget : findsNothing,
          );
          expect(h.sdk.calls.where((c) => c.method == 'sendMessage'), isEmpty);
        } finally {
          await close(tester);
        }
      },
    );
  }
}
