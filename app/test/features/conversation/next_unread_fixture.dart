import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart' show debugPrintSynchronously;
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:go_router/go_router.dart';
import 'package:imapp/app.dart';
import 'package:imapp/core/app_runtime.dart';
import 'package:imapp/core/tenant/session_credentials.dart';
import 'package:imapp/features/conversation/data/conversation_repository.dart';
import 'package:imapp/features/conversation/presentation/conversations_page.dart';
import 'package:imapp/providers.dart';

import '../../core/im/capture_theme.dart';
import '../../core/im/sdk_harness.dart';

final class NextUnreadFixture {
  NextUnreadFixture({Duration timeout = const Duration(seconds: 15)})
      : sdk = SdkHarness(operationTimeout: timeout);
  final SdkHarness sdk;
  late ConversationRepository repository;
  late GoRouter router;
  late ProviderContainer container;
  Completer<dynamic>? allReply;
  Completer<void>? allEntered;
  bool started = false;
  Future<void> start({int count = 125, Set<int> unread = const {84, 124}, Set<int> muted = const {84}}) async {
    for (var index = 0; index < count; index++) {
      final row = SdkHarness.conversation(id: 'inbox-$index', title: '联系人 $index',
        draft: index.isEven ? '稍后回复 $index' : '');
      row['unreadCount'] = unread.contains(index) ? 2 : 0;
      row['recvMsgOpt'] = muted.contains(index) ? 2 : 0;
      row['latestMsg'] = SdkHarness.message(id: 'message-$index', text: '会话预览 $index');
      row['latestMsgSendTime'] = 1760600000000 - index;
      sdk.conversations.add(row);
    }
    await sdk.start(); started = true;
    sdk.overrideCall = (call) async {
      if (call.method == 'getAllConversationList') {
        if (allEntered?.isCompleted == false) allEntered!.complete();
        return allReply?.future ?? jsonEncode(sdk.conversations);
      }
      return sdk.respond(call);
    };
    repository = ConversationRepository(sdk.gateway, sdk.session);
  }
  int get reads => sdk.calls.where((call) => call.method == 'getAllConversationList').length;
  int get writes => sdk.calls.where((call) => const {
    'markConversationMessageAsRead', 'pinConversation', 'setConversationRecvMessageOpt',
    'hideConversation', 'deleteConversationAndDeleteAllMsg', 'clearConversationAndDeleteAllMsg',
  }.contains(call.method)).length;
  Future<void> changed() => sdk.callback(ListenerType.conversationListener,
    {'type': 'onConversationChanged', 'data': jsonEncode(sdk.conversations)});
  Future<void> changeOwner({String user = 'other', String issuer = 'https://other.example.com/',
      String tenant = 'other-tenant'}) async {
    await sdk.session.signOut(); sdk.userId = user;
    final next = sdk.credentials;
    await sdk.session.signIn((_) async => SessionCredentials(userId: user,
      tenantId: tenant, tenantName: next.tenantName, domains: next.domains,
      chatToken: next.chatToken, imToken: next.imToken, issuer: issuer));
  }
  Future<void> mount(WidgetTester tester, {double width = 320, double height = 568, double scale = 1.5}) async {
    if (!started) await tester.runAsync(start);
    tester.view.physicalSize = Size(width, height); tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize); addTearDown(tester.view.resetDevicePixelRatio);
    final theme = await captureTheme(tester); sdk.runtime.phase = AppPhase.online;
    await tester.pumpWidget(ProviderScope(overrides: [
      appRuntimeProvider.overrideWith((_) => sdk.runtime),
      imGatewayProvider.overrideWithValue(sdk.gateway),
    ], child: Consumer(builder: (context, ref, _) {
      container = ProviderScope.containerOf(context); router = ref.watch(routerProvider);
      return MaterialApp.router(theme: theme, routerConfig: router,
        builder: (context, child) => MediaQuery(data: MediaQuery.of(context)
          .copyWith(textScaler: TextScaler.linear(scale)), child: child!));
    })));
    router.go('/home');
    await until(tester, () => find.text('联系人 0').evaluate().isNotEmpty);
  }
  Future<void> frame(WidgetTester tester) async {
    await tester.runAsync(() async { await Future<void>.delayed(Duration.zero); });
    await tester.pump(const Duration(milliseconds: 20));
  }
  Future<void> until(WidgetTester tester, bool Function() ready) async {
    for (var frameIndex = 0; frameIndex < 700; frameIndex++) {
      if (ready()) return;
      await frame(tester);
    }
    throw TestFailure('Expected bounded actual unread view/route');
  }
  Future<void> ready(WidgetTester tester, Finder finder) async {
    final route = ModalRoute.of(tester.element(finder));
    if (route == null) throw TestFailure('Expected owned route');
    await until(tester, () => route.isCurrent &&
      (route.animation == null || route.animation!.status == AnimationStatus.completed));
  }
  Future<void> choose(WidgetTester tester) async {
    final menu = find.byWidgetPredicate((widget) => widget is PopupMenuButton<Object> && widget.tooltip == '更多操作');
    expect(menu.hitTestable(), findsOneWidget); await tester.tap(menu.hitTestable());
    await until(tester, () => find.text('下一未读会话').hitTestable().evaluate().length == 1);
    await ready(tester, find.text('下一未读会话'));
    await tester.tap(find.text('下一未读会话').hitTestable());
    await frame(tester);
  }
  Future<void> located(WidgetTester tester, String id) async {
    await until(tester, () => find.byKey(ValueKey(id)).hitTestable().evaluate().length == 1 &&
      find.text('已定位到下一未读会话').evaluate().isNotEmpty);
    final bounds = tester.getRect(find.byKey(ValueKey(id)));
    final viewport = tester.getRect(find.byType(CustomScrollView));
    expect(bounds.overlaps(viewport), isTrue);
    expect(bounds.top, greaterThanOrEqualTo(viewport.top - 1));
    expect(bounds.top, lessThan(viewport.bottom));
    expect(find.descendant(of: find.byKey(ValueKey(id)), matching: find.byType(InkWell)).hitTestable(), findsOneWidget);
  }
  Future<void> coverAndReturn(WidgetTester tester) async {
    final navigator = Navigator.of(tester.element(find.byType(ConversationsPage)));
    final route = MaterialPageRoute<void>(builder: (_) => const Scaffold(body: Text('foreign unread route')));
    unawaited(navigator.push(route));
    await until(tester, () => find.text('foreign unread route').evaluate().length == 1);
    await ready(tester, find.text('foreign unread route'));
    navigator.pop();
    var removed = false; unawaited(route.completed.then<void>((_) { removed = true; }));
    await until(tester, () => removed);
  }
  Future<void> closeUnit() async {
    if (allReply?.isCompleted == false) allReply!.complete(jsonEncode(sdk.conversations));
    if (started) await sdk.dispose();
  }
  Future<void> close(WidgetTester tester) async {
    try {
      debugPrintSynchronously('next_unread_cleanup sdk.begin');
      await tester.runAsync(() => closeUnit().timeout(const Duration(seconds: 20)));
      debugPrintSynchronously('next_unread_cleanup sdk.end');
    } finally {
      debugPrintSynchronously('next_unread_cleanup unmount.begin');
      await tester.pumpWidget(const SizedBox.shrink()); await tester.pumpAndSettle();
      debugPrintSynchronously('next_unread_cleanup unmount.end');
    }
  }
}
