import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/app.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';
import 'package:imapp/features/contacts/presentation/select_contacts_page.dart';
import 'package:imapp/features/group/presentation/create_group_page.dart';
import 'package:imapp/features/search/domain/global_search.dart';

import '../../core/im/sdk_harness.dart';
import 'group_creation_entry_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('production router replaces a different CID and retires the owned group creation stack after read retry', (tester) async {
    final f = GroupEntryFixture();
    try {
      await f.mount(tester, CreationSurface.chat, productionRouter: true);
      final router = f.creation.router!;
      expect(router, same(f.creation.container.read(routerProvider)));
      final original = tester.state<State<ChatPage>>(find.byType(ChatPage));
      final originalRoute = ModalRoute.of(tester.element(find.byType(ChatPage)))!;
      expect((originalRoute.settings as Page).key, const ValueKey(('/chat/:conversationId', 'origin-cid')));

      await f.settings(tester);
      await f.creation.readyRoute(tester, f.entry);
      final sheet = ModalRoute.of(tester.element(f.entry))!;
      await tester.tap(f.entry);
      await f.until(tester, () => find.byType(SelectContactsPage).evaluate().isNotEmpty);
      await f.creation.readyRoute(tester, find.byType(SelectContactsPage));
      final selector = ModalRoute.of(tester.element(find.byType(SelectContactsPage)))!;
      await f.select(tester, 'carol');
      await f.editor(tester);
      final editor = ModalRoute.of(tester.element(find.byType(CreateGroupPage)))!;
      final completed = <String>{};
      for (final entry in {'sheet': sheet, 'selector': selector, 'editor': editor}.entries) {
        unawaited(entry.value.completed.then<void>((_) { completed.add(entry.key); }));
      }

      // Native transport fixture only. The destination itself remains the real
      // ChatPage/controller reading its exact CID through the production SDK.
      f.creation.base.sdk.conversations.add({
        ...SdkHarness.conversation(id: 'created-cid', title: '新建讨论群', group: true),
        'groupID': 'created-group',
      });
      f.creation.conversationReply = () async => throw const AppFailure(FailureKind.network);
      await f.creation.tap(tester, '创建群聊');
      await f.until(tester, () => find.textContaining('群已创建，暂时无法打开会话').evaluate().isNotEmpty);
      expect(f.creation.creates, hasLength(1));
      expect(router.routeInformationProvider.value.uri.path, '/chat/origin-cid');
      expect(f.creation.base.sdk.calls.where((call) => call.method == 'getOneConversation' &&
        (call.arguments as Map)['sourceID'] == 'created-group'), hasLength(1));

      f.creation.conversationReply = null;
      await f.until(tester, () => tester.widget<FilledButton>(
        find.widgetWithText(FilledButton, '重试打开会话')).onPressed != null);
      await f.creation.tap(tester, '重试打开会话');
      await f.until(tester, () => find.text('新建讨论群').evaluate().isNotEmpty &&
        find.byType(CreateGroupPage).evaluate().isEmpty);
      await f.until(tester, () => completed.length == 3);
      final current = tester.state<State<ChatPage>>(find.byType(ChatPage));
      expect(current, isNot(same(original)));
      expect(original.mounted, isFalse);
      expect(tester.widget<ChatPage>(find.byType(ChatPage)).conversationId, 'created-cid');
      final targetRoute = ModalRoute.of(tester.element(find.byType(ChatPage)))!;
      expect((targetRoute.settings as Page).key, const ValueKey(('/chat/:conversationId', 'created-cid')));
      expect(targetRoute.isCurrent, isTrue);
      expect(completed, {'sheet', 'selector', 'editor'});
      expect(router.routeInformationProvider.value.uri.path, '/chat/created-cid');
      expect(f.creation.base.sdk.calls.where((call) => call.method == 'getOneConversation' &&
        (call.arguments as Map)['sourceID'] == 'created-group'), hasLength(2));
      expect(f.creation.base.sdk.calls.where((call) => call.method == 'getMultipleConversation' &&
        ((call.arguments as Map)['conversationIDList'] as List).contains('created-cid')), isNotEmpty);
      expect(f.creation.base.sdk.calls.where((call) => call.method == 'getAdvancedHistoryMessageList' &&
        (call.arguments as Map)['conversationID'] == 'created-cid'), isNotEmpty);
      expect(find.byType(SelectContactsPage), findsNothing);
      expect(find.byType(CreateGroupPage), findsNothing);
      expect(f.creation.creates, hasLength(1));
      expect(tester.takeException(), isNull);
      debugPrint('group_entry_production_route retry.reads2 owned.completed3 newCID.visible');
    } finally { await f.close(tester); }
  });

  testWidgets('production router keeps a same-CID query and metadata update on the existing anchored page and draft', (tester) async {
    final f = GroupEntryFixture();
    var selectionCurrent = true;
    try {
      await f.mount(tester, CreationSurface.chat, productionRouter: true);
      final sdk = f.creation.base.sdk;
      final anchor = SdkHarness.message(id: 'anchor-message', text: '原消息定位正文');
      sdk.messages.add(anchor);
      final metadata = SdkHarness.conversation(id: 'anchor-cid', title: '来源联系人');
      sdk.conversations.add(metadata);
      final previous = sdk.overrideCall!;
      sdk.overrideCall = (call) async {
        if (call.method == 'findMessageList') {
          final params = (call.arguments as Map)['searchParams'] as List;
          return jsonEncode({'findResultItems': [
            for (final param in params)
              if ((param as Map)['conversationID'] == 'anchor-cid')
                {'conversationID': 'anchor-cid', 'messageList': [anchor]},
          ]});
        }
        return await previous(call);
      };
      final selection = GlobalSourceSelection(conversationId: 'anchor-cid',
        messageId: 'anchor-message', isCurrent: () => selectionCurrent);
      final router = f.creation.router!;
      expect(router, same(f.creation.container.read(routerProvider)));
      router.go('/chat/anchor-cid?message=anchor-message&source=global', extra: selection);
      await f.until(tester, () => find.text('正在查看来源消息').evaluate().isNotEmpty &&
        find.text('原消息定位正文').evaluate().isNotEmpty);
      final before = tester.state<State<ChatPage>>(find.byType(ChatPage));
      final beforeRoute = ModalRoute.of(tester.element(find.byType(ChatPage)))!;
      final input = tester.widget<TextField>(find.byType(TextField)).controller!;
      await tester.enterText(find.byType(TextField), '保留未发送草稿');
      final reads = sdk.calls.where((call) => const {
        'getMultipleConversation', 'getAdvancedHistoryMessageList', 'findMessageList',
      }.contains(call.method)).length;

      router.go('/chat/anchor-cid?message=anchor-message&source=global&view=stable', extra: selection);
      await f.frame(tester);
      await f.until(tester, () => router.routeInformationProvider.value.uri.queryParameters['view'] == 'stable');
      await tester.runAsync(() => sdk.callback(ListenerType.conversationListener, {
        'type': 'onConversationChanged',
        'data': jsonEncode([{...metadata, 'unreadCount': 0}]),
      }));
      await f.frame(tester);
      expect(tester.state<State<ChatPage>>(find.byType(ChatPage)), same(before));
      final afterRoute = ModalRoute.of(tester.element(find.byType(ChatPage)))!;
      expect(afterRoute, same(beforeRoute));
      expect((afterRoute.settings as Page).key, const ValueKey(('/chat/:conversationId', 'anchor-cid')));
      final widget = tester.widget<ChatPage>(find.byType(ChatPage));
      expect(widget.sourceMessageId, 'anchor-message');
      expect(widget.sourceCurrent, isNotNull);
      expect(widget.sourceCurrent!(), isTrue);
      expect(find.text('正在查看来源消息'), findsOneWidget);
      expect(find.text('原消息定位正文'), findsOneWidget);
      expect(tester.widget<TextField>(find.byType(TextField)).controller, same(input));
      expect(input.text, '保留未发送草稿');
      expect(sdk.calls.where((call) => const {
        'getMultipleConversation', 'getAdvancedHistoryMessageList', 'findMessageList',
      }.contains(call.method)), hasLength(reads));
      selectionCurrent = false;
      expect(widget.sourceCurrent!(), isFalse);
      expect(f.creation.creates, isEmpty);
      expect(sdk.calls.where((call) => call.method == 'sendMessage'), isEmpty);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });

  testWidgets('production router preserves unique page identity for repeated CID and A B A imperative pushes', (tester) async {
    final f = GroupEntryFixture();
    try {
      await f.mount(tester, CreationSurface.chat, productionRouter: true);
      final sdk = f.creation.base.sdk;
      sdk.conversations.add(SdkHarness.conversation(id: 'second-cid', title: '另一个联系人'));
      final router = f.creation.router!;
      final original = tester.state<State<ChatPage>>(find.byType(ChatPage));
      final originalRoute = ModalRoute.of(tester.element(find.byType(ChatPage)))!;
      final visited = <State<ChatPage>>[original];
      final keys = <LocalKey?>[(originalRoute.settings as Page).key];
      for (final cid in ['origin-cid', 'second-cid', 'origin-cid']) {
        final previous = visited.last;
        unawaited(router.push<void>('/chat/$cid'));
        await f.until(tester, () => find.byType(ChatPage).evaluate().isNotEmpty &&
          !identical(tester.state<State<ChatPage>>(find.byType(ChatPage)), previous));
        await f.creation.readyRoute(tester, find.byType(ChatPage));
        await f.until(tester, () => tester.widget<TextField>(find.byType(TextField)).controller != null);
        final current = tester.state<State<ChatPage>>(find.byType(ChatPage));
        final route = ModalRoute.of(tester.element(find.byType(ChatPage)))!;
        final page = route.settings as Page;
        expect(page.key, isNot(isIn(keys)));
        expect(route.isCurrent, isTrue);
        expect(tester.widget<ChatPage>(find.byType(ChatPage)).conversationId, cid);
        expect(tester.takeException(), isNull);
        keys.add(page.key); visited.add(current);
      }
      expect(keys.toSet(), hasLength(4));
      expect(visited.toSet(), hasLength(4));
      expect(visited.every((state) => state.mounted), isTrue);
      expect(find.byType(ChatPage, skipOffstage: false), findsNWidgets(4));
      expect(router.routeInformationProvider.value.uri.path, '/chat/origin-cid');
      expect(f.creation.creates, isEmpty);
      expect(sdk.calls.where((call) => call.method == 'sendMessage'), isEmpty);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
}
