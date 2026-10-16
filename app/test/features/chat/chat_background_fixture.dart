import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart' show debugPrintSynchronously;
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:image_picker/image_picker.dart';
import 'package:imapp/core/storage/staged_image.dart';
import 'package:imapp/features/chat/data/chat_background_store.dart';
import 'package:imapp/features/chat/presentation/chat_background_page.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';
import 'package:imapp/providers.dart';

import '../../core/im/capture_theme.dart';
import 'chat_font_fixture.dart';

/// Reuse the accepted SdkHarness native/HTTP mocks; new seams replace only the
/// OS picker and atomic metadata replacement. SessionCoordinator, SDK wrapper,
/// ImageStager, file copies, decoder, ChatPage and the editor are production code.
final class BackgroundFixture {
  final font = ChatFontFixture();
  final pickerReplies = <Future<XFile?> Function()>[];
  int pickerCalls = 0, replacements = 0;
  bool failReplace = false, sourceCurrent = true;
  Completer<void>? replaceReply, replaceEntered, supportReply, supportEntered;
  Completer<XFile?>? pickerReply;
  late File original;
  late Directory support;
  late ChatBackgroundPreferences preferences;
  late final store = ChatBackgroundStore(supportDirectory: () async {
    if (supportEntered?.isCompleted == false) supportEntered!.complete();
    final reply = supportReply;
    if (reply != null) await reply.future;
    return support;
  }, replacePointer: (temporary, destination) async {
    ++replacements;
    if (replaceEntered?.isCompleted == false) replaceEntered!.complete();
    final reply = replaceReply;
    if (reply != null) await reply.future;
    if (failReplace) throw const FileSystemException('synthetic atomic replace failure');
    await temporary.rename(destination.path);
  });
  Future<void> start() async {
    await font.start();
    support = await Directory('${font.sdk.directory.path}/application-support').create();
    original = File('${font.sdk.directory.path}/background-original.png');
    await original.writeAsBytes(base64Decode(
      'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jF9kAAAAASUVORK5CYII='));
    preferences = ChatBackgroundPreferences(store, font.sdk.session, 'single-alice-bob');
  }
  StagedImage source([File? file]) => StagedImage(file ?? original, 1, 1, 'image/png', '.png');
  Future<ChatBackgroundSnapshot> seed() => preferences.save(ChatBackgroundSnapshot.empty,
    source(), sourceCurrent: () => true);
  Future<XFile?> pick() async {
    ++pickerCalls;
    if (pickerReplies.isNotEmpty) return pickerReplies.removeAt(0)();
    final reply = pickerReply;
    return reply == null ? XFile(original.path) : reply.future;
  }
  Future<T> actual<T>(WidgetTester tester, Future<T> Function() action) => font.actual(tester, action);
  Future<void> frame(WidgetTester tester) => font.frame(tester);
  Future<void> until(WidgetTester tester, bool Function() ready) => font.until(tester, ready);
  Future<void> ready(WidgetTester tester, Finder target) => font.ready(tester, target);
  Future<void> mount(WidgetTester tester, {bool chat = false, bool small = false, Future<void> Function()? beforeBuild}) async {
    await tester.runAsync(start);
    if (beforeBuild != null) await tester.runAsync(beforeBuild);
    if (small) {
      tester.view.physicalSize = const Size(360, 640); tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize); addTearDown(tester.view.resetDevicePixelRatio);
    }
    final theme = await captureTheme(tester);
    await tester.pumpWidget(ProviderScope(overrides: [
      appRuntimeProvider.overrideWith((_) => font.sdk.runtime),
      imGatewayProvider.overrideWithValue(font.sdk.gateway),
      chatReadingStoreProvider.overrideWithValue(font.store),
      chatBackgroundStoreProvider.overrideWithValue(store),
      chatBackgroundPickerProvider.overrideWithValue(pick),
    ], child: MaterialApp(theme: theme,
      builder: (context, child) => MediaQuery(data: MediaQuery.of(context).copyWith(
        textScaler: TextScaler.linear(small ? 2 : 1)), child: child!),
      home: chat ? const ChatPage(conversationId: 'single-alice-bob') :
        Builder(builder: (context) => Scaffold(body: TextButton(onPressed: () =>
          Navigator.of(context).push<void>(MaterialPageRoute<void>(builder: (_) =>
            ChatBackgroundPage(preferences: preferences, sourceCurrent: () => sourceCurrent))),
          child: const Text('打开背景')))),
    )));
    if (chat) {
      await until(tester, () => find.text('实际聊天文字用于字号验证').evaluate().length == 1);
    } else { await open(tester); }
  }
  Future<void> open(WidgetTester tester) async {
    await tester.tap(find.text('打开背景').hitTestable());
    await loaded(tester);
  }
  Future<void> loaded(WidgetTester tester) async {
    await ready(tester, find.text('从相册选择背景'));
    await until(tester, () => find.byType(LinearProgressIndicator).evaluate().isEmpty);
  }
  Future<void> openFromChat(WidgetTester tester) async {
    await tester.tap(find.byTooltip('聊天设置').hitTestable());
    await ready(tester, find.text('聊天字号')); // Known old entry, no missing-background timeout.
    expect(find.text('聊天背景'), findsOneWidget);
    await tester.ensureVisible(find.text('聊天背景'));
    await tester.tap(find.text('聊天背景').hitTestable()); await loaded(tester);
  }
  Future<void> select(WidgetTester tester) async {
    final button = find.widgetWithText(OutlinedButton, '从相册选择背景');
    await tester.ensureVisible(button);
    await frame(tester);
    await ready(tester, button);
    await until(tester, () => button.evaluate().length == 1 &&
      button.hitTestable().evaluate().length == 1 &&
      tester.widget<OutlinedButton>(button).onPressed != null);
    final previousCalls = pickerCalls;
    await tester.tap(button.hitTestable());
    // tap() does not rebuild the setState(_choosing=true) frame. Observe a new
    // dispatch and a rendered terminal state, never the previous empty spinner.
    await frame(tester);
    await until(tester, () => pickerCalls == previousCalls + 1 &&
      find.byType(LinearProgressIndicator).evaluate().isEmpty &&
      button.evaluate().length == 1 && tester.widget<OutlinedButton>(button).onPressed != null);
  }
  Future<void> tapSave(WidgetTester tester) async {
    final button = find.widgetWithText(FilledButton, '保存背景');
    await frame(tester); // Render the selected/default draft before reading enabled.
    await tester.ensureVisible(button);
    await ready(tester, button);
    await until(tester, () => button.evaluate().length == 1 &&
      button.hitTestable().evaluate().length == 1 &&
      tester.widget<FilledButton>(button).onPressed != null);
    await tester.tap(button.hitTestable());
    await frame(tester); // Pending ACK stays pending; this does not wait for success.
  }
  Future<void> _tapTextButton(WidgetTester tester, String label) async {
    final button = find.widgetWithText(TextButton, label);
    await tester.ensureVisible(button);
    await frame(tester);
    await ready(tester, button);
    await until(tester, () => button.evaluate().length == 1 &&
      button.hitTestable().evaluate().length == 1 &&
      tester.widget<TextButton>(button).onPressed != null);
    await tester.tap(button.hitTestable());
    await frame(tester);
  }
  Future<void> tapDefault(WidgetTester tester) => _tapTextButton(tester, '恢复默认背景');
  Future<void> tapReread(WidgetTester tester) => _tapTextButton(tester, '重新读取');
  Future<void> coverAndReturn(WidgetTester tester) async {
    final navigator = Navigator.of(tester.element(find.byType(ChatBackgroundPage)));
    final foreign = MaterialPageRoute<void>(builder: (_) => const Scaffold(body: Text('背景外来页面')));
    unawaited(navigator.push(foreign)); await ready(tester, find.text('背景外来页面'));
    navigator.pop(); var complete = false;
    unawaited(foreign.completed.then<void>((_) { complete = true; }));
    await until(tester, () => complete);
  }
  void release() {
    if (replaceReply?.isCompleted == false) replaceReply!.complete();
    if (supportReply?.isCompleted == false) supportReply!.complete();
    if (pickerReply?.isCompleted == false) pickerReply!.complete(null);
  }
  Future<void> closeUnit() async {
    release(); await store.flush(); await font.closeUnit(); store.dispose();
  }
  Future<void> close(WidgetTester tester) async {
    release();
    try {
      debugPrintSynchronously('chat_background_cleanup drain.begin');
      await actual(tester, () => store.flush().timeout(const Duration(seconds: 20)));
      debugPrintSynchronously('chat_background_cleanup drain.end');
    } finally {
      try {
        debugPrintSynchronously('chat_background_cleanup sdk.begin');
        if (font.started) await actual(tester, () => font.sdk.dispose().timeout(const Duration(seconds: 20)));
        debugPrintSynchronously('chat_background_cleanup sdk.end');
      } finally {
        debugPrintSynchronously('chat_background_cleanup unmount.begin');
        await tester.pumpWidget(const SizedBox.shrink());
        await tester.pumpAndSettle();
        font.store.dispose(); store.dispose();
        debugPrintSynchronously('chat_background_cleanup unmount.end');
      }
    }
  }
}
