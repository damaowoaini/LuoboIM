import 'dart:async';

import 'package:flutter/foundation.dart' show debugPrintSynchronously;
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/tenant/session_credentials.dart';
import 'package:imapp/features/chat/data/chat_reading_preferences.dart';
import 'package:imapp/features/chat/presentation/chat_font_size_page.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';
import 'package:imapp/providers.dart';

import '../../core/im/capture_theme.dart';
import '../../core/im/sdk_harness.dart';

final class ChatFontFixture {
  final sdk = SdkHarness();
  final values = <String, double>{};
  final writes = <(String, double)>[];
  int reads = 0;
  bool failRead = false, failWrite = false, started = false;
  bool sourceCurrent = true;
  Completer<double?>? readReply;
  Completer<void>? writeReply, writeEntered;
  late final store = ChatReadingPreferencesStore(
    readDouble: (key) async {
      ++reads;
      if (failRead) throw StateError('synthetic OS read failure');
      final pending = readReply;
      return pending == null ? values[key] : await pending.future;
    },
    writeDouble: (key, value) async {
      writes.add((key, value));
      if (writeEntered?.isCompleted == false) writeEntered!.complete();
      final pending = writeReply;
      if (pending != null) await pending.future;
      if (failWrite) throw StateError('synthetic OS write failure');
      values[key] = value;
    },
  );
  late ChatReadingPreferences preferences;
  Future<void> start() async {
    sdk.conversations.add(SdkHarness.conversation());
    sdk.messages.add(SdkHarness.message(id: 'font-message', text: '实际聊天文字用于字号验证'));
    await sdk.start();
    started = true;
    preferences = ChatReadingPreferences(store, sdk.session);
  }
  Future<void> changeOwner({String user = 'alice', String tenant = 'next-tenant',
    String issuer = 'https://next-route.example.com/'}) async {
    await sdk.session.signOut();
    sdk.userId = user;
    final next = sdk.credentials;
    await sdk.session.signIn((_) async => SessionCredentials(userId: user,
      tenantId: tenant, tenantName: next.tenantName, domains: next.domains,
      chatToken: next.chatToken, imToken: next.imToken, issuer: issuer));
  }
  Future<void> frame(WidgetTester tester) async {
    await tester.runAsync(() async { await Future<void>.delayed(Duration.zero); });
    await tester.pump(const Duration(milliseconds: 20));
  }
  Future<void> until(WidgetTester tester, bool Function() ready) async {
    for (var turn = 0; turn < 500; ++turn) {
      if (ready()) return;
      await frame(tester);
    }
    throw TestFailure('Expected bounded actual font page state/owned route');
  }
  Future<void> ready(WidgetTester tester, Finder finder) async {
    await until(tester, () => finder.evaluate().length == 1 &&
      finder.hitTestable().evaluate().length == 1);
    final route = ModalRoute.of(tester.element(finder));
    await until(tester, () => route != null && route.isCurrent &&
      (route.animation == null || route.animation!.status == AnimationStatus.completed));
  }
  Future<T> actual<T>(WidgetTester tester, Future<T> Function() action) async {
    var completed = false;
    T? value;
    Object? error;
    StackTrace? stack;
    await tester.runAsync(() async {
      unawaited(Future<T>.sync(action).then<void>((result) {
        value = result; completed = true;
      }, onError: (Object caught, StackTrace trace) {
        error = caught; stack = trace; completed = true;
      }));
    });
    await until(tester, () => completed);
    if (error != null) Error.throwWithStackTrace(error!, stack!);
    return value as T;
  }
  Future<void> mount(WidgetTester tester, {bool chat = false, double scale = 1.0,
    bool small = false}) async {
    if (!started) await tester.runAsync(start);
    if (small) {
      tester.view.physicalSize = const Size(360, 640);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
    }
    final theme = await captureTheme(tester);
    await tester.pumpWidget(ProviderScope(overrides: [
      appRuntimeProvider.overrideWith((_) => sdk.runtime),
      imGatewayProvider.overrideWithValue(sdk.gateway),
      chatReadingStoreProvider.overrideWithValue(store),
    ], child: MaterialApp(theme: theme,
      builder: (context, child) => MediaQuery(data: MediaQuery.of(context)
        .copyWith(textScaler: TextScaler.linear(scale)), child: child!),
      home: chat ? const ChatPage(conversationId: 'single-alice-bob') :
        Builder(builder: (context) => Scaffold(body: TextButton(
          onPressed: () => Navigator.of(context).push<void>(MaterialPageRoute<void>(
            builder: (_) => ChatFontSizePage(preferences: preferences,
              sourceCurrent: () => sourceCurrent))), child: const Text('打开字号')))),
    )));
    if (chat) {
      await until(tester, () => find.text('实际聊天文字用于字号验证').evaluate().length == 1);
    } else {
      await tester.tap(find.text('打开字号'));
      await ready(tester, find.byType(Slider));
      await until(tester, () => find.byType(LinearProgressIndicator).evaluate().isEmpty);
    }
  }
  Future<void> openFromChat(WidgetTester tester) async {
    await tester.tap(find.byTooltip('聊天设置').hitTestable());
    await ready(tester, find.text('聊天字号'));
    await tester.tap(find.text('聊天字号').hitTestable());
    await ready(tester, find.byType(Slider));
    await until(tester, () => find.byType(LinearProgressIndicator).evaluate().isEmpty);
  }
  Future<void> coverAndReturn(WidgetTester tester) async {
    final navigator = Navigator.of(tester.element(find.byType(ChatFontSizePage)));
    final foreign = MaterialPageRoute<void>(builder: (_) => const Scaffold(body: Text('外来页面')));
    unawaited(navigator.push(foreign));
    await ready(tester, find.text('外来页面'));
    navigator.pop();
    var removed = false;
    unawaited(foreign.completed.then<void>((_) { removed = true; }));
    await until(tester, () => removed);
  }
  void release() {
    if (readReply?.isCompleted == false) readReply!.complete(null);
    if (writeReply?.isCompleted == false) writeReply!.complete();
  }
  Future<void> closeUnit() async {
    release();
    if (started) await sdk.dispose();
    store.dispose();
  }
  Future<void> close(WidgetTester tester) async {
    release();
    try {
      debugPrintSynchronously('chat_font_cleanup sdk.begin');
      if (started) await actual(tester, () => sdk.dispose().timeout(const Duration(seconds: 20)));
      debugPrintSynchronously('chat_font_cleanup sdk.end');
    } finally {
      debugPrintSynchronously('chat_font_cleanup unmount.begin');
      await tester.pumpWidget(const SizedBox.shrink());
      await tester.pumpAndSettle();
      store.dispose();
      debugPrintSynchronously('chat_font_cleanup unmount.end');
    }
  }
}
