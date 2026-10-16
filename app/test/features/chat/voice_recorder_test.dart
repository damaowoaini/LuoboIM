import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';
import 'package:imapp/features/chat/presentation/chat_controller.dart';
import 'package:imapp/features/chat/presentation/chat_media_actions.dart';
import 'package:record/record.dart';

import '../../core/im/sdk_harness.dart';

final class FakeRecorder implements AudioRecorder {
  final permission = Completer<bool>();
  Completer<void>? starting;
  final entered = Completer<void>();
  var starts = 0;
  var cancels = 0;
  var disposed = false;
  String? path;
  @override
  Future<bool> hasPermission({bool request = true}) => permission.future;
  @override
  Future<void> start(RecordConfig config, {required String path}) async {
    starts++;
    this.path = path;
    await File(path).writeAsBytes([1, 2, 3]);
    entered.complete();
    await starting?.future;
  }

  @override
  Future<void> cancel() async {
    cancels++;
  }

  @override
  Future<void> dispose() async {
    disposed = true;
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

void main() {
  late SdkHarness harness;
  late ChatController controller;
  late FakeRecorder recorder;
  setUp(() {
    harness = SdkHarness();
    recorder = FakeRecorder();
  });
  tearDown(() async {
    controller.dispose();
    await harness.dispose();
  });

  Future<void> mount(WidgetTester tester) async {
    harness.conversations.add(SdkHarness.conversation());
    await tester.runAsync(() async {
      await harness.start();
      controller = ChatController(
        ChatRepository(
          gateway: harness.gateway,
          session: harness.session,
          conversationId: 'single-alice-bob',
        ),
      );
      await controller.start();
    });
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: VoiceRecorderSheet(
            controller: controller,
            recorderFactory: () => recorder,
          ),
        ),
      ),
    );
  }

  GestureDetector gesture(WidgetTester tester) =>
      tester.widget<GestureDetector>(
        find
            .ancestor(
              of: find.text('按住说话'),
              matching: find.byType(GestureDetector),
            )
            .first,
      );
  Future<void> finish(WidgetTester tester) async {
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump();
    await tester.runAsync(() async {
      await Future<void>.delayed(const Duration(milliseconds: 20));
      await harness.dispose();
    });
  }

  testWidgets(
    'releasing while permission is pending never starts the microphone',
    (tester) async {
      await mount(tester);
      final detector = gesture(tester);
      await tester.runAsync(() async {
        detector.onLongPressStart!(const LongPressStartDetails());
        detector.onLongPressEnd!(const LongPressEndDetails());
        recorder.permission.complete(true);
        await Future<void>.delayed(const Duration(milliseconds: 10));
      });
      await tester.pumpAndSettle();
      expect(recorder.starts, 0);
      await finish(tester);
      await tester.pump();
      expect(recorder.disposed, isTrue);
    },
  );

  testWidgets(
    'closing during native start cancels a late recording and removes its source',
    (tester) async {
      await mount(tester);
      recorder.starting = Completer<void>();
      recorder.permission.complete(true);
      await tester.runAsync(() async {
        gesture(tester).onLongPressStart!(const LongPressStartDetails());
        await recorder.entered.future;
      });
      expect(
        await tester.runAsync(() => File(recorder.path!).exists()),
        isTrue,
      );
      await tester.pumpWidget(const SizedBox.shrink());
      await tester.pump();
      await tester.runAsync(() async {
        recorder.starting!.complete();
        await Future<void>.delayed(const Duration(milliseconds: 30));
      });
      expect(recorder.cancels, greaterThanOrEqualTo(1));
      await tester.pump();
      expect(recorder.disposed, isTrue);
      expect(
        await tester.runAsync(() => File(recorder.path!).exists()),
        isFalse,
      );
      await tester.runAsync(harness.dispose);
    },
  );

  testWidgets(
    'denied microphone permission keeps the sheet actionable without starting capture',
    (tester) async {
      await mount(tester);
      await tester.runAsync(() async {
        recorder.permission.complete(false);
        gesture(tester).onLongPressStart!(const LongPressStartDetails());
        await Future<void>.delayed(const Duration(milliseconds: 10));
      });
      await tester.pumpAndSettle();
      expect(find.text('无法开始录音，请检查麦克风权限后重试'), findsOneWidget);
      expect(recorder.starts, 0);
      expect(find.byTooltip('取消录音'), findsOneWidget);
      await finish(tester);
    },
  );
}
