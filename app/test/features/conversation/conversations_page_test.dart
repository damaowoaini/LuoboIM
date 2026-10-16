import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/conversation/presentation/conversations_page.dart';
import 'package:imapp/providers.dart';

import '../../core/im/sdk_harness.dart';
import '../../core/im/capture_theme.dart';

void main() {
  late SdkHarness harness;
  setUp(() {
    harness = SdkHarness();
    harness.conversations.add(
      SdkHarness.conversation(title: '设计讨论与协作', draft: '稍后继续讨论'),
    );
  });
  tearDown(() => harness.dispose());

  testWidgets(
    'inbox searches real SDK results and confirms before deleting records on a small large-text screen',
    (tester) async {
      await tester.runAsync(harness.start);
      tester.view.physicalSize = const Size(360, 640);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final screenshotKey = GlobalKey();
      final theme = await captureTheme(tester);
      await tester.pumpWidget(
        ProviderScope(
          overrides: [
            imGatewayProvider.overrideWithValue(harness.gateway),
            appRuntimeProvider.overrideWith((_) => harness.runtime),
          ],
          child: RepaintBoundary(
            key: screenshotKey,
            child: MaterialApp(
              theme: theme,
              debugShowCheckedModeBanner: false,
              builder: (context, child) => MediaQuery(
                data: MediaQuery.of(context)
                    .copyWith(textScaler: TextScaler.linear(1.6)),
                child: child!,
              ),
              home: const ConversationsPage(),
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.text('设计讨论与协作'), findsOneWidget);
      expect(find.text('[草稿] '), findsOneWidget);
      expect(tester.takeException(), isNull);
      const capture = String.fromEnvironment('TEST_CAPTURE_DIR');
      if (capture.isNotEmpty) {
        await tester.runAsync(() async {
          final boundary =
              screenshotKey.currentContext!.findRenderObject()!
                  as RenderRepaintBoundary;
          final image = await boundary.toImage(pixelRatio: 2);
          final bytes = (await image.toByteData(
            format: ui.ImageByteFormat.png,
          ))!;
          await Directory(capture).create(recursive: true);
          await File('$capture/inbox-widget.png')
              .writeAsBytes(bytes.buffer.asUint8List());
          image.dispose();
        });
      }
      await tester.tap(find.byTooltip('搜索当前会话'));
      await tester.pumpAndSettle();
      await tester.enterText(find.byType(TextField), '不存在');
      await tester.pump();
      expect(find.text('没有找到匹配的会话'), findsOneWidget);
      await tester.tap(find.byTooltip('搜索当前会话'));
      await tester.pumpAndSettle();
      await tester.longPress(find.text('设计讨论与协作'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('移除会话并删除聊天记录'));
      await tester.pumpAndSettle();
      expect(find.text('删除聊天记录？'), findsOneWidget);
      expect(
        harness.calls.any(
          (call) => call.method == 'deleteConversationAndDeleteAllMsg',
        ),
        isFalse,
      );
      await tester.tap(find.text('取消'));
      await tester.pumpAndSettle();
      expect(find.text('设计讨论与协作'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.runAsync(harness.dispose);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );
}
