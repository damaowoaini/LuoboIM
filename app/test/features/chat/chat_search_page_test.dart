import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';
import 'package:imapp/providers.dart';

import '../../core/im/sdk_harness.dart';

void main() {
  testWidgets(
    'chat search uses the native codec and selects an original message at large text',
    (tester) async {
      final harness = SdkHarness()
        ..conversations.add(SdkHarness.conversation())
        ..messages.add(SdkHarness.message(id: 'latest', text: '最近的一条消息'));
      final source = SdkHarness.message(id: 'found-message', text: '搜索命中的原消息');
      await tester.runAsync(harness.start);
      addTearDown(() => tester.runAsync(harness.dispose));
      harness.overrideCall = (call) async {
        if (call.method == 'searchLocalMessages' ||
            call.method == 'findMessageList') {
          return jsonEncode({
            'searchResultItems': [
              {
                'conversationID': 'single-alice-bob',
                'messageList': [source],
              },
            ],
            'totalCount': 1,
          });
        }
        return harness.respond(call);
      };
      tester.view.physicalSize = const Size(360, 720);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      await tester.pumpWidget(
        ProviderScope(
          overrides: [
            imGatewayProvider.overrideWithValue(harness.gateway),
            appRuntimeProvider.overrideWith((_) => harness.runtime),
          ],
          child: MaterialApp(
            builder: (context, child) => MediaQuery(
              data: MediaQuery.of(context)
                  .copyWith(textScaler: TextScaler.linear(1.5)),
              child: child!,
            ),
            home: const ChatPage(conversationId: 'single-alice-bob'),
          ),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.byTooltip('聊天设置'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('查找聊天记录'));
      await tester.pumpAndSettle();
      expect(find.text('输入关键词，或选择图片、视频和文件'), findsOneWidget);
      await tester.enterText(find.byType(TextField), '原消息');
      await tester.pump(const Duration(milliseconds: 400));
      await tester.pumpAndSettle();
      expect(find.text('搜索命中的原消息'), findsOneWidget);
      final args =
          harness.calls
                  .lastWhere((call) => call.method == 'searchLocalMessages')
                  .arguments
              as Map;
      expect(args['filter']['keywordList'], ['原消息']);
      await tester.tap(find.text('搜索命中的原消息'));
      await tester.pumpAndSettle();
      expect(find.text('正在查看来源消息'), findsOneWidget);
      expect(find.text('搜索命中的原消息'), findsOneWidget);
      expect(
        harness.calls.where((call) => call.method == 'findMessageList'),
        hasLength(1),
      );
      expect(tester.takeException(), isNull);
      await tester.runAsync(harness.dispose);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );
}
