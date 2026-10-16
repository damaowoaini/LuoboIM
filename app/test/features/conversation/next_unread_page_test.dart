import 'dart:async';
import 'dart:convert';

import 'package:flutter/gestures.dart' show kDoubleTapMinTime;
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/contacts/presentation/contacts_page.dart';
import 'package:imapp/features/conversation/presentation/conversations_page.dart';
import 'package:imapp/features/conversation/presentation/conversation_unread_signal.dart';

import '../../core/im/capture_theme.dart';
import 'next_unread_fixture.dart';

Finder messageTab() => find.descendant(of: find.byType(NavigationBar), matching: find.text('消息'));
ScrollController inboxScroll(WidgetTester tester) => tester.widget<CustomScrollView>(find.byType(CustomScrollView)).controller!;

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('actual inbox more menu exposes an accessible next unread entry without replacing existing actions', (tester) async {
    final f = NextUnreadFixture();
    try {
      await f.mount(tester);
      final menu = find.byWidgetPredicate((widget) => widget is PopupMenuButton<Object> && widget.tooltip == '更多操作');
      await tester.tap(menu.hitTestable());
      await f.until(tester, () => find.text('添加好友').hitTestable().evaluate().length == 1);
      expect(find.text('下一未读会话').hitTestable(), findsOneWidget);
      expect(find.text('搜索加群').hitTestable(), findsOneWidget);
      expect(find.text('通话记录').hitTestable(), findsOneWidget);
      expect(f.reads, 0); expect(f.writes, 0); expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });

  testWidgets('ordinary message and contacts tab navigation remains available without reading or marking unread', (tester) async {
    final f = NextUnreadFixture();
    try {
      await f.mount(tester);
      final contacts = find.descendant(of: find.byType(NavigationBar), matching: find.text('通讯录'));
      expect(contacts.hitTestable(), findsOneWidget); await tester.tap(contacts.hitTestable());
      await f.until(tester, () => find.byType(ContactsPage).evaluate().length == 1);
      expect(messageTab().hitTestable(), findsOneWidget); await tester.tap(messageTab().hitTestable());
      await f.until(tester, () => find.byType(ConversationsPage).evaluate().length == 1);
      expect(f.reads, 0); expect(f.writes, 0); expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });

  testWidgets('real message tab double tap reveals the muted unread beyond the first SDK page at large text', (tester) async {
    final f = NextUnreadFixture();
    try {
      await f.mount(tester, scale: 2);
      expect(find.byKey(const ValueKey('inbox-84')), findsNothing);
      await tester.tap(messageTab().hitTestable()); await tester.pump(kDoubleTapMinTime);
      await tester.tap(messageTab().hitTestable()); await f.located(tester, 'inbox-84');
      expect(f.reads, 1); expect(f.writes, 0);
      expect(find.descendant(of: find.byKey(const ValueKey('inbox-84')),
        matching: find.byIcon(Icons.notifications_off_outlined)), findsOneWidget);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });

  testWidgets('actual next unread menu reaches the end then wraps to the original CID without opening chat', (tester) async {
    final f = NextUnreadFixture();
    try {
      await f.mount(tester); await f.choose(tester); await f.located(tester, 'inbox-84');
      await f.choose(tester); await f.located(tester, 'inbox-124');
      await f.choose(tester); await f.located(tester, 'inbox-84');
      expect(f.router.state.uri.path, '/home'); expect(f.reads, 3); expect(f.writes, 0);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });

  testWidgets('canceling the actual search switch confirmation preserves the typed filter and performs no full read', (tester) async {
    final f = NextUnreadFixture();
    try {
      await f.mount(tester); await tester.tap(find.byTooltip('搜索当前会话').hitTestable());
      await tester.enterText(find.byType(TextField), '联系人 1'); await f.frame(tester);
      await f.choose(tester);
      await f.until(tester, () => find.text('查看全部会话').hitTestable().evaluate().length == 1);
      await tester.tap(find.widgetWithText(TextButton, '取消').hitTestable());
      await f.until(tester, () => find.text('查看全部会话').evaluate().isEmpty);
      expect(tester.widget<TextField>(find.byType(TextField)).controller!.text, '联系人 1');
      expect(f.reads, 0); expect(f.writes, 0); expect(tester.takeException(), isNull);
      await f.choose(tester);
      await f.until(tester, () => find.text('查看全部会话').hitTestable().evaluate().length == 1);
      await f.coverAndReturn(tester);
      expect(find.widgetWithText(FilledButton, '查看全部会话').hitTestable(), findsOneWidget);
      await tester.tap(find.widgetWithText(FilledButton, '查看全部会话').hitTestable());
      await f.until(tester, () => find.text('查看全部会话').evaluate().isEmpty);
      expect(f.reads, 0);
      expect(tester.widget<TextField>(find.byType(TextField)).controller!.text, '联系人 1');
    } finally { await f.close(tester); }
  });

  testWidgets('explicitly switching to all conversations finds a hidden-by-filter unread and retains the search draft for reopening', (tester) async {
    final f = NextUnreadFixture();
    try {
      await f.mount(tester); await tester.tap(find.byTooltip('搜索当前会话').hitTestable());
      await tester.enterText(find.byType(TextField), '联系人 1'); await f.frame(tester);
      await f.choose(tester);
      await f.until(tester, () => find.text('查看全部会话').hitTestable().evaluate().length == 1);
      await tester.tap(find.widgetWithText(FilledButton, '查看全部会话').hitTestable());
      await f.located(tester, 'inbox-84');
      await tester.tap(find.byTooltip('搜索当前会话').hitTestable()); await f.frame(tester);
      expect(tester.widget<TextField>(find.byType(TextField)).controller!.text, '联系人 1');
      expect(f.reads, 1); expect(f.writes, 0); expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });

  testWidgets('typing a new filter retires an already dispatched complete read before any old result can scroll', (tester) async {
    final f = NextUnreadFixture();
    try {
      await f.mount(tester); f.allReply = Completer<dynamic>(); f.allEntered = Completer<void>();
      await f.choose(tester); await f.until(tester, () => f.allEntered!.isCompleted);
      await tester.tap(find.byTooltip('搜索当前会话').hitTestable());
      await tester.enterText(find.byType(TextField), '联系人 2'); await f.frame(tester);
      f.allReply!.complete(jsonEncode(f.sdk.conversations)); await f.frame(tester); await f.frame(tester);
      expect(f.reads, 1); expect(find.text('已定位到下一未读会话'), findsNothing);
      expect(tester.widget<TextField>(find.byType(TextField)).controller!.text, '联系人 2');
      expect(f.writes, 0); expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });

  testWidgets('manual scrolling cancels an admitted pending unread seek and its late reply cannot take the scroll position back', (tester) async {
    final f = NextUnreadFixture();
    try {
      await f.mount(tester); f.allReply = Completer<dynamic>(); f.allEntered = Completer<void>();
      await f.choose(tester); await f.until(tester, () => f.allEntered!.isCompleted);
      final viewport = find.byType(CustomScrollView).hitTestable(); expect(viewport, findsOneWidget);
      await tester.drag(viewport, const Offset(0, -120)); await tester.pumpAndSettle();
      final chosen = inboxScroll(tester).offset; expect(chosen, greaterThan(0));
      f.allReply!.complete(jsonEncode(f.sdk.conversations)); await f.frame(tester); await f.frame(tester);
      expect(inboxScroll(tester).offset, closeTo(chosen, .5));
      expect(find.text('已定位到下一未读会话'), findsNothing); expect(f.writes, 0);
    } finally { await f.close(tester); }
  });

  testWidgets('foreign route coverage and return cannot revive a dispatched unread view but a new explicit action can work', (tester) async {
    final f = NextUnreadFixture();
    try {
      await f.mount(tester); f.allReply = Completer<dynamic>(); f.allEntered = Completer<void>();
      await f.choose(tester); await f.until(tester, () => f.allEntered!.isCompleted);
      await f.coverAndReturn(tester); f.allReply!.complete(jsonEncode(f.sdk.conversations));
      await f.frame(tester); await f.frame(tester);
      expect(find.text('已定位到下一未读会话'), findsNothing); expect(f.reads, 1);
      f.allReply = null; await f.choose(tester); await f.located(tester, 'inbox-84');
      expect(f.reads, 2); expect(f.writes, 0); expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });

  testWidgets('SDK deletion before the unread reply retires the original CID and never redirects to its shifted row index', (tester) async {
    final f = NextUnreadFixture();
    try {
      await f.mount(tester); f.allReply = Completer<dynamic>(); f.allEntered = Completer<void>();
      final original = jsonEncode(f.sdk.conversations);
      await f.choose(tester); await f.until(tester, () => f.allEntered!.isCompleted);
      f.sdk.conversations.removeWhere((row) => row['conversationID'] == 'inbox-84');
      await tester.runAsync(f.changed); f.allReply!.complete(original);
      await f.frame(tester); await f.frame(tester);
      expect(find.text('已定位到下一未读会话'), findsNothing);
      expect(find.byKey(const ValueKey('inbox-84')), findsNothing); expect(f.writes, 0);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });

  testWidgets('owner replacement retires both a dispatched page read and retained old message-tab callbacks', (tester) async {
    final f = NextUnreadFixture();
    try {
      await f.mount(tester); final oldSelected = tester.widget<NavigationBar>(find.byType(NavigationBar)).onDestinationSelected!;
      f.allReply = Completer<dynamic>(); f.allEntered = Completer<void>();
      final oldBody = jsonEncode(f.sdk.conversations);
      await f.choose(tester); await f.until(tester, () => f.allEntered!.isCompleted);
      await tester.runAsync(() async {
        final changing = f.changeOwner(); f.sdk.conversations.clear();
        f.allReply!.complete(oldBody); await changing;
      });
      await f.frame(tester); oldSelected(0); oldSelected(0); await f.frame(tester);
      expect(find.text('已定位到下一未读会话'), findsNothing);
      expect(find.text('联系人 84'), findsNothing); expect(f.reads, 1); expect(f.writes, 0);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });

  testWidgets('real production seek reveals a variable-height sliver target by measured geometry rather than an average height', (tester) async {
    final theme = await captureTheme(tester); final scroll = ScrollController();
    final viewport = GlobalKey(); final rows = <String, BuildContext>{};
    final ids = [for (var index = 0; index < 90; index++) 'variable-$index'];
    bool current = true, done = false, reached = false;
    final heights = [for (var index = 0; index < 90; index++) 48.0 + (index % 7) * 31.0];
    tester.view.physicalSize = const Size(320, 568); tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize); addTearDown(tester.view.resetDevicePixelRatio);
    try {
      await tester.pumpWidget(MaterialApp(theme: theme, home: Scaffold(body: CustomScrollView(
        key: viewport, controller: scroll, slivers: [SliverList.builder(itemCount: ids.length,
          itemBuilder: (_, index) => Builder(builder: (context) {
            rows[ids[index]] = context;
            return SizedBox(key: ValueKey(ids[index]), height: heights[index],
              child: TextButton(onPressed: () {}, child: Text('真实变高行 $index')));
          }))]))));
      final operation = revealUnreadConversation(id: 'variable-87', scroll: scroll, rowContexts: rows,
        viewport: () => viewport.currentContext, orderedIds: () => ids, current: () => current)
        .then((value) { reached = value; done = true; });
      for (var frame = 0; frame < 1200 && !done; frame++) { await tester.pump(const Duration(milliseconds: 20)); }
      expect(done, isTrue); await operation; expect(reached, isTrue);
      final target = find.byKey(const ValueKey('variable-87'));
      expect(target.hitTestable(), findsOneWidget);
      final bounds = tester.getRect(target), screen = tester.getRect(find.byType(CustomScrollView));
      expect(bounds.top, greaterThanOrEqualTo(screen.top - 1)); expect(bounds.overlaps(screen), isTrue);
      expect(heights.toSet().length, 7); expect(tester.takeException(), isNull);
    } finally { current = false; await tester.pumpWidget(const SizedBox.shrink()); scroll.dispose(); }
  });

  testWidgets('production measured seek stops after its original view ticket retires during an actual scroll animation', (tester) async {
    final theme = await captureTheme(tester); final scroll = ScrollController();
    final viewport = GlobalKey(); final rows = <String, BuildContext>{};
    final ids = [for (var index = 0; index < 90; index++) 'cancel-$index'];
    var current = true, done = false, reached = true;
    try {
      await tester.pumpWidget(MaterialApp(theme: theme, home: Scaffold(body: CustomScrollView(key: viewport,
        controller: scroll, slivers: [SliverList.builder(itemCount: ids.length,
          itemBuilder: (_, index) => Builder(builder: (context) {
            rows[ids[index]] = context; return SizedBox(height: 48 + (index % 5) * 37.0, child: Text(ids[index]));
          }))]))));
      final operation = revealUnreadConversation(id: ids.last, scroll: scroll, rowContexts: rows,
        viewport: () => viewport.currentContext, orderedIds: () => ids, current: () => current)
        .then((value) { reached = value; done = true; });
      await tester.pump(); await tester.pump(const Duration(milliseconds: 20)); current = false;
      for (var frame = 0; frame < 30 && !done; frame++) { await tester.pump(const Duration(milliseconds: 20)); }
      expect(done, isTrue); await operation; expect(reached, isFalse);
      expect(find.text(ids.last), findsNothing); expect(tester.takeException(), isNull);
    } finally { current = false; await tester.pumpWidget(const SizedBox.shrink()); scroll.dispose(); }
  });
}
