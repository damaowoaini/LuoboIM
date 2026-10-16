import 'dart:async';
import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';
import 'package:imapp/features/chat/presentation/chat_controller.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';
import 'package:imapp/providers.dart';
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/features/search/domain/global_search.dart';
import '../../core/im/sdk_harness.dart';
import '../../core/im/capture_theme.dart';
import 'global_search_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late GlobalFixture f;
  setUp(() { f = GlobalFixture(); });
  tearDown(() => f.close());
  ChatController chat() => ChatController(ChatRepository(gateway: f.sdk.gateway,
      session: f.sdk.session, conversationId: cidA, now: () => f.wall));

  test('actual route guard refuses a missing or mismatched global ticket and preserves ordinary deep links', () async {
    await f.start();
    expect(globalSourceGuard(cidA, 'a1', null, fromGlobal: true)!(), isFalse);
    final ticket = GlobalSourceSelection(conversationId: cidA, messageId: 'a1', isCurrent: () => true);
    expect(globalSourceGuard(cidHidden, 'a1', ticket, fromGlobal: true)!(), isFalse);
    expect(globalSourceGuard(cidA, 'a1', ticket, fromGlobal: true)!(), isTrue);
    expect(globalSourceGuard(cidA, 'a1', null, fromGlobal: false), isNull);
  });

  test('initial expired selection dispatches zero metadata history or read receipt', () async {
    await f.start();
    final c = chat();
    try {
      await c.start(isCurrent: () => false);
      expect(f.calls('getMultipleConversation'), 0);
      expect(f.calls('getAdvancedHistoryMessageList'), 0);
      expect(f.calls('markConversationMessageAsRead'), 0);
      expect(c.messages, isEmpty);
    } finally { c.dispose(); }
  });
  test('selection withdrawn while metadata waits cannot start a fresh history or mark-read lease', () async {
    await f.start();
    final c = chat();
    final response = f.defer<dynamic>('{"messageList":[],"isEnd":true}');
    final entered = Completer<void>();
    var active = true;
    f.pending = (call) {
      if (call.method == 'getMultipleConversation') { entered.complete(); return response.future; }
      return null;
    };
    try {
      final pending = c.start(isCurrent: () => active);
      await entered.future.timeout(const Duration(seconds: 2)); active = false;
      response.complete(jsonEncode([SdkHarness.conversation()]));
      await pending;
      expect(f.calls('getAdvancedHistoryMessageList'), 0);
      expect(f.calls('markConversationMessageAsRead'), 0);
      expect(c.messages, isEmpty);
    } finally { c.dispose(); }
  });
  test('selection withdrawn during initial history cannot merge or mark read', () async {
    await f.start();
    final c = chat();
    final response = f.defer<dynamic>('{"messageList":[],"isEnd":true}');
    final entered = Completer<void>();
    var active = true;
    f.pending = (call) {
      if (call.method == 'getAdvancedHistoryMessageList') { entered.complete(); return response.future; }
      return null;
    };
    try {
      final pending = c.start(isCurrent: () => active);
      await entered.future.timeout(const Duration(seconds: 2)); active = false;
      response.complete(jsonEncode({'messageList': [searchText('old-start', 'old')], 'isEnd': true}));
      await pending;
      expect(c.messages, isEmpty); expect(c.conversation, isNull);
      expect(f.calls('markConversationMessageAsRead'), 0);
    } finally { c.dispose(); }
  });
  test('initial mark-read already dispatched does not publish staged history after selection loss', () async {
    await f.start();
    final c = chat();
    final response = f.defer<dynamic>('{"messageList":[],"isEnd":true}');
    final entered = Completer<void>();
    var active = true;
    f.sdk.messages.add(searchText('staged', 'staged body'));
    f.pending = (call) {
      if (call.method == 'markConversationMessageAsRead') { entered.complete(); return response.future; }
      return null;
    };
    try {
      final pending = c.start(isCurrent: () => active);
      await entered.future.timeout(const Duration(seconds: 2)); active = false; response.complete(null); await pending;
      expect(c.messages, isEmpty); expect(c.conversation, isNull);
      expect(f.calls('findMessageList'), 0);
    } finally { c.dispose(); }
  });
  test('original source ticket survives normal start but stops second stage when withdrawn during find', () async {
    await f.start();
    final c = chat();
    final response = f.defer<String>(wireGroups({}));
    final entered = Completer<void>();
    var active = true;
    try {
      await c.start(isCurrent: () => active);
      final count = f.calls('getAdvancedHistoryMessageList');
      f.pending = (call) {
        if (call.method == 'findMessageList') { entered.complete(); return response.future; }
        return null;
      };
      final pending = c.viewSource('a1', isCurrent: () => active);
      await entered.future.timeout(const Duration(seconds: 2)); active = false;
      response.complete(wireGroups({cidA: [searchText('a1', 'match source')]}));
      expect(await pending, isFalse);
      expect(f.calls('getAdvancedHistoryMessageList'), count);
      expect(c.sourceMessageId, isNull);
    } finally { c.dispose(); }
  });
  test('source history late return cannot commit, while a later explicit normal intent remains usable', () async {
    await f.start();
    final c = chat();
    final response = f.defer<dynamic>('{"messageList":[],"isEnd":true}');
    final entered = Completer<void>();
    var active = true;
    try {
      f.pending = (call) {
        if (call.method == 'getAdvancedHistoryMessageList') { entered.complete(); return response.future; }
        return null;
      };
      final pending = c.viewSource('a1', isCurrent: () => active);
      await entered.future.timeout(const Duration(seconds: 2)); active = false;
      response.complete(jsonEncode({'messageList': [searchText('old-page', 'old')], 'isEnd': true}));
      expect(await pending, isFalse); expect(c.messages, isEmpty);
      f.pending = null;
      expect(await c.viewSource('a1'), isTrue);
      expect(c.sourceMessageId, 'a1');
    } finally { c.dispose(); }
  });
  Future<void> startWidget(WidgetTester tester, String label) async {
    debugPrint('global_source_phase $label startup.begin');
    var ready = false;
    await tester.runAsync(() async {
      await f.start().timeout(const Duration(seconds: 20));
      ready = true;
    });
    if (!ready) throw StateError('Fixture startup did not complete; see runAsync error');
    debugPrint('global_source_phase $label startup.end');
  }
  Future<void> closeWidget(WidgetTester tester, String label) async {
    debugPrint('global_source_phase $label unmount.begin');
    await tester.pumpWidget(const SizedBox.shrink());
    debugPrint('global_source_phase $label cleanup.begin');
    var finished = false;
    await tester.runAsync(() async {
      await f.close().timeout(const Duration(seconds: 20));
      finished = true;
    });
    if (!finished) throw StateError('Fixture cleanup did not complete; see runAsync error');
    debugPrint('global_source_phase $label cleanup.end');
  }
  testWidgets('actual ChatPage does no source reads when the initial source ticket is already false', (tester) async {
    await startWidget(tester, 'expired');
    try {
      final theme = await captureTheme(tester);
      debugPrint('global_source_phase expired mount.begin');
      await tester.pumpWidget(ProviderScope(overrides: [
        imGatewayProvider.overrideWithValue(f.sdk.gateway),
        appRuntimeProvider.overrideWith((_) => f.sdk.runtime),
        tenantApiProvider.overrideWithValue(f.api),
      ], child: MaterialApp(theme: theme, home: ChatPage(conversationId: cidA,
          sourceMessageId: 'a1', sourceCurrent: () => false))));
      debugPrint('global_source_phase expired settle.begin');
      await tester.pumpAndSettle(const Duration(milliseconds: 100),
          EnginePhase.sendSemanticsUpdate, const Duration(seconds: 10));
      debugPrint('global_source_phase expired assertions.begin');
      expect(f.calls('getMultipleConversation'), 0);
      expect(f.calls('getAdvancedHistoryMessageList'), 0);
      expect(f.calls('findMessageList'), 0);
      debugPrint('global_source_phase expired assertions.end');
    } finally { await closeWidget(tester, 'expired'); }
  }, timeout: const Timeout(Duration(seconds: 45)));
  testWidgets('actual ChatPage valid selection normally starts and locates exact source', (tester) async {
    await startWidget(tester, 'valid');
    try {
      final theme = await captureTheme(tester);
      debugPrint('global_source_phase valid mount.begin');
      await tester.pumpWidget(ProviderScope(overrides: [
        imGatewayProvider.overrideWithValue(f.sdk.gateway),
        appRuntimeProvider.overrideWith((_) => f.sdk.runtime),
        tenantApiProvider.overrideWithValue(f.api),
      ], child: MaterialApp(theme: theme, home: ChatPage(conversationId: cidA,
          sourceMessageId: 'a1', sourceCurrent: () => true))));
      debugPrint('global_source_phase valid settle.begin');
      await tester.pumpAndSettle(const Duration(milliseconds: 100),
          EnginePhase.sendSemanticsUpdate, const Duration(seconds: 10));
      debugPrint('global_source_phase valid assertions.begin');
      expect(f.calls('getMultipleConversation'), greaterThan(0));
      expect(f.calls('findMessageList'), greaterThan(0));
      expect(find.textContaining('match ordinary', findRichText: true), findsWidgets);
      expect(tester.takeException(), isNull);
      debugPrint('global_source_phase valid assertions.end');
    } finally { await closeWidget(tester, 'valid'); }
  }, timeout: const Timeout(Duration(seconds: 45)));
}
