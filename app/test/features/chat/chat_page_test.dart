import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';
import 'package:imapp/providers.dart';

import '../../core/im/sdk_harness.dart';
import '../../core/im/capture_theme.dart';

void main() {
  late SdkHarness harness;
  setUp(() {
    harness = SdkHarness();
    harness.conversations.add(
      SdkHarness.conversation(title: '设计讨论与协作', draft: '继续完善交互细节'),
    );
    harness.messages.add(
      SdkHarness.message(id: 'incoming', text: '我们把失败重试和草稿保存一起验证。'),
    );
  });
  tearDown(() => harness.dispose());

  testWidgets(
    'chat restores a draft and sends through the real method codec without overflow at large text',
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
              home: const ChatPage(conversationId: 'single-alice-bob'),
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.text('我们把失败重试和草稿保存一起验证。'), findsOneWidget);
      expect(
        tester.widget<TextField>(find.byType(TextField)).controller!.text,
        '继续完善交互细节',
      );
      await tester.enterText(find.byType(TextField), '这条消息通过 SDK 通道发送');
      await tester.tap(find.byTooltip('发送'));
      await tester.pumpAndSettle();
      expect(
        tester.widget<TextField>(find.byType(TextField)).controller!.text,
        isEmpty,
      );
      expect(find.text('这条消息通过 SDK 通道发送'), findsOneWidget);
      expect(find.text('已发送'), findsOneWidget);
      expect(
        harness.calls.where((call) => call.method == 'sendMessage'),
        hasLength(1),
      );
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
          await File('$capture/chat-widget.png')
              .writeAsBytes(bytes.buffer.asUint8List());
          image.dispose();
        });
      }
      await tester.runAsync(harness.dispose);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );
}
