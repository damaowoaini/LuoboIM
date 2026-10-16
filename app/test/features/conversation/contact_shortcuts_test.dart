import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/app.dart';
import 'package:imapp/features/contacts/contacts_providers.dart';
import 'package:imapp/features/contacts/data/contacts_repository.dart';
import 'package:imapp/features/contacts/presentation/contact_search_page.dart';
import 'package:imapp/features/contacts/presentation/user_profile_page.dart';
import 'package:imapp/features/conversation/presentation/conversations_page.dart';
import 'package:imapp/features/group/presentation/group_page.dart';

import '../../support/http_adapter.dart';
import '../../core/im/sdk_harness.dart';
import 'contact_shortcuts_fixture.dart';

String? hint(WidgetTester tester) => tester.widget<TextField>(find.byType(TextField)).decoration?.hintText;
PopupMenuButton<String> menuWidget(WidgetTester tester) => tester.widget<PopupMenuButton<String>>(
  find.byWidgetPredicate((widget) => widget is PopupMenuButton<String> && widget.tooltip == '更多操作'));

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('inbox add friend opens the real ID lookup and normal back preserves inbox state', (tester) async {
    final f = ContactShortcutsFixture();
    try {
      await f.mount(tester);
      final original = tester.state<State<ConversationsPage>>(find.byType(ConversationsPage));
      await f.choose(tester, '添加好友');
      expect(f.router, same(f.container.read(routerProvider)));
      expect(f.router.routeInformationProvider.value.uri.toString(), '/contacts/search?kind=user');
      expect(hint(tester), '输入完整账号 ID');
      expect(f.http.requests, isEmpty);
      await f.back(tester);
      expect(tester.state<State<ConversationsPage>>(find.byType(ConversationsPage)), same(original));
      expect(f.sdkWrites, 0);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });

  testWidgets('inbox search group opens the real group lookup without changing existing menu actions', (tester) async {
    final f = ContactShortcutsFixture();
    try {
      await f.mount(tester);
      await f.menu(tester);
      expect(find.text('会议（预约与管理）').hitTestable(), findsOneWidget);
      expect(find.text('退出登录').hitTestable(), findsOneWidget);
      final target = find.text('搜索加群').hitTestable();
      expect(target, findsOneWidget);
      await tester.tap(target);
      await f.until(tester, () => find.byType(ContactSearchPage).evaluate().length == 1);
      await f.ready(tester, find.byType(ContactSearchPage));
      expect(f.router.routeInformationProvider.value.uri.toString(), '/contacts/search?kind=group');
      expect(hint(tester), '输入完整群 ID');
      expect(f.http.requests, isEmpty);
      expect(f.sdkWrites, 0);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });

  testWidgets('group shortcut performs complete exact SDK lookup and opens the real group detail without joining', (tester) async {
    final f = ContactShortcutsFixture();
    try {
      await f.mount(tester);
      await f.choose(tester, '搜索加群');
      await f.search(tester, 'group-a');
      await f.until(tester, () => find.widgetWithText(ListTile, '设计讨论群').evaluate().length == 1);
      final calls = f.entry.creation.base.sdk.calls.where((call) => call.method == 'getGroupsInfo').toList();
      expect(calls, hasLength(1));
      expect(calls.single.arguments['groupIDList'], ['group-a']);
      expect(f.http.requests, isEmpty);
      await tester.tap(find.widgetWithText(ListTile, '设计讨论群').hitTestable());
      await f.until(tester, () => find.byType(GroupPage).evaluate().length == 1);
      await f.ready(tester, find.byType(GroupPage));
      expect(tester.widget<GroupPage>(find.byType(GroupPage)).groupId, 'group-a');
      expect(f.router.routeInformationProvider.value.uri.path, '/contacts/groups/group-a');
      expect(f.sdkWrites, 0);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });

  testWidgets('friend shortcut uses exact ID and real profile hydration without a phone fallback or automatic request', (tester) async {
    final f = ContactShortcutsFixture();
    try {
      await f.mount(tester);
      await f.choose(tester, '添加好友');
      await f.search(tester, '7031156314');
      await f.until(tester, () => find.text('ID：7031156314').evaluate().length == 1);
      expect(f.http.requests, hasLength(1));
      expect(f.http.requests.single.uri.path, '/chat/user/find/full');
      expect(f.http.requests.single.data, {'userIDs': ['7031156314']});
      await tester.tap(find.widgetWithText(ListTile, 'ID：7031156314').hitTestable());
      await f.until(tester, () => find.byType(UserProfilePage).evaluate().length == 1);
      await f.ready(tester, find.byType(UserProfilePage));
      await f.until(tester, () => f.http.requests.length == 2);
      await f.until(tester, () => find.widgetWithText(SelectableText, 'ID：7031156314').evaluate().length == 1);
      expect(f.router.routeInformationProvider.value.uri.path, '/contacts/user/7031156314');
      expect(f.http.requests.every((request) => request.uri.path == '/chat/user/find/full'), isTrue);
      expect(f.entry.creation.base.sdk.calls.where((call) => call.method == 'getUsersInfo'), isNotEmpty);
      expect(f.sdkWrites, 0);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });

  for (final mode in ['', '?kind=unknown']) {
    testWidgets('production contact route $mode preserves safe ID default and explicit phone mode', (tester) async {
      final f = ContactShortcutsFixture();
      try {
        await f.mount(tester);
        unawaited(f.router.push<void>('/contacts/search$mode'));
        await f.until(tester, () => find.byType(ContactSearchPage).evaluate().length == 1);
        await f.ready(tester, find.byType(ContactSearchPage));
        expect(hint(tester), '输入完整账号 ID');
        expect(f.http.requests, isEmpty);
        await tester.tap(find.byWidgetPredicate((widget) => widget is DropdownButtonFormField));
        await f.until(tester, () => find.text('手机号').hitTestable().evaluate().length == 1);
        final dropdownRoute = ModalRoute.of(tester.element(find.text('手机号').hitTestable()))!;
        await tester.tap(find.text('手机号').hitTestable());
        await f.entry.creation.removedRoute(tester, dropdownRoute);
        expect(hint(tester), '输入完整手机号');
        await f.search(tester, '13800138000');
        await f.until(tester, () => find.text('ID：bob').evaluate().length == 1);
        expect(f.http.requests.single.uri.path, '/chat/user/search/full');
        expect(f.http.requests.single.data, {
          'keyword': '13800138000', 'pagination': {'pageNumber': 1, 'showNumber': 2},
        });
        expect(f.sdkWrites, 0);
        expect(tester.takeException(), isNull);
      } finally { await f.close(tester); }
    });
  }

  testWidgets('same contact route changed to group mode invalidates an already dispatched ID reply without reissuing it', (tester) async {
    final f = ContactShortcutsFixture();
    try {
      await f.mount(tester);
      await f.choose(tester, '添加好友');
      final pending = f.deferred();
      f.reply = (_) => pending.future;
      await f.search(tester, '7031156314');
      await f.until(tester, () => f.http.requests.length == 1);
      final original = tester.state<State<ContactSearchPage>>(find.byType(ContactSearchPage));
      f.router.go('/contacts/search?kind=group');
      await f.frame(tester);
      await f.until(tester, () => hint(tester) == '输入完整群 ID');
      expect(tester.state<State<ContactSearchPage>>(find.byType(ContactSearchPage)), same(original));
      expect(tester.widget<TextField>(find.byType(TextField)).controller!.text, '7031156314');
      await tester.runAsync(() async {
        pending.complete(f.response([{'userID': '7031156314', 'nickname': '旧账号结果', 'allowAddFriend': 1}]));
      });
      await f.frame(tester);
      expect(find.text('旧账号结果'), findsNothing);
      expect(f.http.requests, hasLength(1));
      expect(tester.widget<FilledButton>(find.widgetWithText(FilledButton, '搜索')).onPressed, isNotNull);
      expect(f.sdkWrites, 0);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });

  testWidgets('lookup failure keeps a visible explicit read retry and never falls back to another namespace', (tester) async {
    final f = ContactShortcutsFixture();
    try {
      await f.mount(tester);
      await f.choose(tester, '添加好友');
      f.reply = (_) async => jsonResponse('{"errCode":1002}');
      await f.search(tester, '7031156314');
      await f.until(tester, () => find.text('重试').evaluate().length == 1);
      expect(find.text('没有找到匹配的结果'), findsNothing);
      expect(f.http.requests, hasLength(1));
      expect(f.http.requests.single.uri.path, '/chat/user/find/full');
      f.reply = null;
      await tester.ensureVisible(find.text('重试'));
      await tester.tap(find.text('重试').hitTestable());
      await f.until(tester, () => find.text('ID：7031156314').evaluate().length == 1);
      expect(f.http.requests, hasLength(2));
      expect(f.http.requests.every((request) => request.uri.path == '/chat/user/find/full'), isTrue);
      expect(f.sdkWrites, 0);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });

  testWidgets('same owner unread metadata refresh does not retire a legitimate inbox shortcut', (tester) async {
    final f = ContactShortcutsFixture();
    try {
      await f.mount(tester);
      final selected = menuWidget(tester).onSelected!;
      await tester.runAsync(() => f.entry.creation.base.sdk.callback(ListenerType.conversationListener, {
        'type': 'onConversationChanged',
        'data': jsonEncode([{...SdkHarness.conversation(id: 'origin-cid'), 'unreadCount': 0}]),
      }));
      await f.frame(tester);
      selected('addFriend');
      await f.until(tester, () => find.byType(ContactSearchPage).evaluate().length == 1);
      await f.ready(tester, find.byType(ContactSearchPage));
      expect(hint(tester), '输入完整账号 ID');
      expect(f.http.requests, isEmpty);
      expect(f.sdkWrites, 0);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });

  testWidgets('a pending lookup cannot publish after the actual contacts provider is replaced', (tester) async {
    final f = ContactShortcutsFixture();
    try {
      await f.mount(tester);
      await f.choose(tester, '添加好友');
      final pending = f.deferred();
      f.reply = (_) => pending.future;
      await f.search(tester, '7031156314');
      await f.until(tester, () => f.http.requests.length == 1);
      final old = f.entry.creation.contacts;
      f.entry.creation.contacts = ContactsRepository(old.gateway, old.access, old.api);
      f.container.invalidate(contactsRepositoryProvider);
      await f.frame(tester);
      await tester.runAsync(() async {
        pending.complete(f.response([{'userID': '7031156314', 'nickname': '旧repository结果', 'allowAddFriend': 1}]));
      });
      await f.frame(tester);
      expect(find.text('旧repository结果'), findsNothing);
      expect(f.http.requests, hasLength(1));
      expect(tester.widget<FilledButton>(find.widgetWithText(FilledButton, '搜索')).onPressed, isNotNull);
      expect(f.sdkWrites, 0);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });

  for (final change in ['actor', 'tenant', 'issuer']) {
    testWidgets('old inbox shortcut callback after $change replacement cannot navigate under the new owner', (tester) async {
      final f = ContactShortcutsFixture();
      try {
        await f.mount(tester);
        final oldSelected = menuWidget(tester).onSelected!;
        await tester.runAsync(() => f.entry.creation.base.signInOther(
          user: change == 'actor' ? 'other' : 'alice',
          tenant: change == 'tenant' ? 'other-tenant' : 'fixture-tenant',
          issuer: change == 'issuer' ? 'https://another.example.com/' : 'https://im.example.com/',
        ));
        await f.frame(tester);
        final before = f.router.routeInformationProvider.value.uri;
        oldSelected('addFriend'); oldSelected('addGroup');
        await f.frame(tester);
        expect(f.router.routeInformationProvider.value.uri, before);
        expect(find.byType(ContactSearchPage), findsNothing);
        expect(f.http.requests, isEmpty);
        expect(f.sdkWrites, 0);
        expect(tester.takeException(), isNull);
      } finally { await f.close(tester); }
    });
  }

  testWidgets('a foreign route and return retires the old inbox callback but a new menu action remains usable', (tester) async {
    final f = ContactShortcutsFixture();
    try {
      await f.mount(tester);
      final oldSelected = menuWidget(tester).onSelected!;
      await f.coverAndReturn(tester);
      oldSelected('addGroup');
      await f.frame(tester);
      expect(find.byType(ContactSearchPage), findsNothing);
      await f.choose(tester, '搜索加群');
      expect(hint(tester), '输入完整群 ID');
      expect(f.sdkWrites, 0);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });

  testWidgets('covering the actual owned popup then returning does not revive its old choice', (tester) async {
    final f = ContactShortcutsFixture();
    try {
      await f.mount(tester);
      await f.menu(tester);
      final popupRoute = ModalRoute.of(tester.element(find.text('搜索加群')))!;
      await f.coverAndReturn(tester);
      await f.ready(tester, find.text('搜索加群'));
      expect(popupRoute.isCurrent, isTrue);
      expect(find.text('搜索加群').hitTestable(), findsOneWidget);
      await tester.tap(find.text('搜索加群').hitTestable());
      await f.entry.creation.removedRoute(tester, popupRoute);
      expect(find.byType(ContactSearchPage), findsNothing);
      await f.choose(tester, '添加好友');
      expect(hint(tester), '输入完整账号 ID');
      expect(f.sdkWrites, 0);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });

  testWidgets('canceling an owned menu and reopening keeps the legitimate shortcut usable', (tester) async {
    final f = ContactShortcutsFixture();
    try {
      await f.mount(tester);
      await f.menu(tester);
      final popup = ModalRoute.of(tester.element(find.text('搜索加群')))!;
      Navigator.of(tester.element(find.text('搜索加群'))).pop();
      await f.entry.creation.removedRoute(tester, popup);
      await f.frame(tester);
      await f.choose(tester, '搜索加群');
      expect(hint(tester), '输入完整群 ID');
      expect(f.http.requests, isEmpty);
      expect(f.sdkWrites, 0);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });

  testWidgets('a synchronous repeated old shortcut callback pushes only one search page', (tester) async {
    final f = ContactShortcutsFixture();
    try {
      await f.mount(tester);
      final selected = menuWidget(tester).onSelected!;
      selected('addFriend'); selected('addFriend');
      await f.until(tester, () => find.byType(ContactSearchPage).evaluate().length == 1);
      await f.ready(tester, find.byType(ContactSearchPage));
      expect(find.byType(ContactSearchPage, skipOffstage: false), findsOneWidget);
      expect(hint(tester), '输入完整账号 ID');
      await f.back(tester);
      expect(find.byType(ContactSearchPage, skipOffstage: false), findsNothing);
      expect(f.sdkWrites, 0);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });

  testWidgets('inbox shortcuts and the group lookup remain usable at 320 by 568 with large text', (tester) async {
    final f = ContactShortcutsFixture();
    try {
      await f.mount(tester, width: 320, height: 568);
      await f.menu(tester);
      for (final text in ['添加好友', '搜索加群']) {
        final target = find.widgetWithText(PopupMenuItem<String>, text).hitTestable();
        expect(target, findsOneWidget);
        expect(tester.getSize(target).height, greaterThanOrEqualTo(44));
        expect(tester.getRect(target).right, lessThanOrEqualTo(320));
      }
      await tester.tap(find.text('搜索加群').hitTestable());
      await f.until(tester, () => find.byType(ContactSearchPage).evaluate().length == 1);
      await f.ready(tester, find.byType(ContactSearchPage));
      expect(hint(tester), '输入完整群 ID');
      await tester.enterText(find.byType(TextField), 'group-a');
      final search = find.widgetWithText(FilledButton, '搜索');
      await tester.ensureVisible(search);
      expect(search.hitTestable(), findsOneWidget);
      expect(tester.getSize(search).height, greaterThanOrEqualTo(44));
      await tester.tap(search.hitTestable());
      await f.until(tester, () => find.widgetWithText(ListTile, '设计讨论群').evaluate().length == 1);
      expect(f.sdkWrites, 0);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
}
