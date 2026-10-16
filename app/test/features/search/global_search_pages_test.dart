import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/search/domain/global_search.dart';
import 'package:imapp/features/search/presentation/global_search_file_page.dart';
import 'global_search_fixture.dart';

void main() {
  testWidgets('five real tabs expose complete sources and exact IDs on a small large-text screen', (tester) async {
    final f = GlobalFixture();
    try {
      await f.mount(tester, width: 360, height: 640, scale: 1.6);
      await f.searchUI(tester);
      expect(find.byType(Tab), findsNWidgets(5));
      await tester.tap(find.widgetWithText(Tab, '联系人'));
      await tester.pumpAndSettle();
      expect(find.text('bob'), findsOneWidget); expect(find.text('carol'), findsOneWidget);
      await tester.tap(find.widgetWithText(Tab, '群聊')); await tester.pumpAndSettle();
      expect(find.text('group-a'), findsOneWidget);
      await tester.tap(find.widgetWithText(Tab, '聊天记录')); await tester.pumpAndSettle();
      expect(find.textContaining(cidHidden), findsOneWidget);
      expect(tester.takeException(), isNull);
      expect(f.calls('searchLocalMessages'), 2);
    } finally { await f.close(tester); }
  });
  testWidgets('contact result uses actual profile hydration rather than incomplete search payload', (tester) async {
    final f = GlobalFixture();
    try {
      await f.mount(tester); await f.searchUI(tester);
      await tester.tap(find.widgetWithText(Tab, '联系人')); await tester.pumpAndSettle();
      await tester.tap(find.text('bob')); await tester.pumpAndSettle();
      expect(f.calls('getUsersInfo'), 1);
      expect(find.textContaining('bob'), findsWidgets);
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('group result reaches actual group detail and rechecks membership', (tester) async {
    final f = GlobalFixture();
    try {
      await f.mount(tester); await f.searchUI(tester);
      await tester.tap(find.widgetWithText(Tab, '群聊')); await tester.pumpAndSettle();
      await tester.tap(find.text('group-a')); await tester.pumpAndSettle();
      expect(f.calls('isJoinGroup'), greaterThan(0));
      expect(f.calls('getGroupMembersInfo'), greaterThan(0));
      expect(tester.takeException(), isNull);
    } finally { await f.close(tester); }
  });
  testWidgets('text expansion preserves hidden CID and exact original-message selection', (tester) async {
    final f = GlobalFixture();
    try {
      await f.mount(tester); await f.searchUI(tester);
      await tester.tap(find.widgetWithText(Tab, '聊天记录')); await tester.pumpAndSettle();
      await tester.tap(find.textContaining('$cidHidden\n')); await tester.pumpAndSettle();
      expect(find.text('相关聊天记录'), findsOneWidget);
      await tester.tap(find.text('match hidden')); await tester.pumpAndSettle();
      expect(f.navigations.last.cid, cidHidden); expect(f.navigations.last.id, 'h1');
      expect(f.navigations.last.source, 'global');
      final ticket = f.navigations.last.extra as GlobalSourceSelection;
      expect(ticket.matches(cidHidden, 'h1'), isTrue);
    } finally { await f.close(tester); }
  });
  testWidgets('typing immediately retires old row callback before debounce dispatch', (tester) async {
    final f = GlobalFixture();
    try {
      await f.mount(tester); await f.searchUI(tester);
      await tester.tap(find.widgetWithText(Tab, '聊天记录')); await tester.pumpAndSettle();
      final tile = tester.widget<ListTile>(find.widgetWithText(ListTile, '同名会话').first);
      final before = f.calls('searchLocalMessages');
      await tester.enterText(find.byType(TextField), 'new word');
      await tester.pump(const Duration(milliseconds: 100));
      tile.onTap!(); await tester.pump();
      expect(find.text('相关聊天记录'), findsNothing);
      expect(f.calls('searchLocalMessages'), before);
      expect(f.navigations, isEmpty);
    } finally { await f.close(tester); }
  });
  testWidgets('failed contacts show an explicit read-only retry rather than an empty-state success', (tester) async {
    final f = GlobalFixture();
    try {
      await f.mount(tester);
      f.pending = (call) => call.method == 'getFriendList'
          ? Future.error(PlatformException(code: '10005', message: 'connection refused')) : null;
      await f.searchUI(tester);
      await tester.tap(find.widgetWithText(Tab, '联系人')); await tester.pumpAndSettle();
      expect(find.text('重试此类搜索'), findsOneWidget);
      expect(find.text('没有找到匹配结果'), findsNothing);
      final calls = f.calls('searchLocalMessages');
      f.pending = null;
      await tester.tap(find.text('重试此类搜索')); await tester.pumpAndSettle();
      expect(find.text('bob'), findsOneWidget);
      expect(f.calls('searchLocalMessages'), calls);
    } finally { await f.close(tester); }
  });
  testWidgets('live learned private expiry hides body and does not need another native search', (tester) async {
    final f = GlobalFixture();
    f.messages[cidA] = [{...searchText('private', 'match secret'), 'attachedInfoElem': {'isPrivateChat': true}}];
    try {
      await f.mount(tester); await f.searchUI(tester);
      await tester.tap(find.widgetWithText(Tab, '聊天记录')); await tester.pumpAndSettle();
      expect(find.textContaining('match secret'), findsNothing);
      expect(find.textContaining('[阅后即焚消息]'), findsOneWidget);
      final before = f.calls('searchLocalMessages');
      f.sdk.gateway.privacyLedger.observe(cidA, {
        ...searchText('private', 'match secret'),
        'attachedInfoElem': {'isPrivateChat': true, 'burnDuration': 30, 'hasReadTime': 1699999960000},
      }, now: f.wall);
      await tester.pump(); await tester.pump();
      expect(find.textContaining('match secret'), findsNothing);
      expect(f.calls('searchLocalMessages'), before);
    } finally { await f.close(tester); }
  });
  testWidgets('normal file detail rereads exact source and may open, private file stays restricted', (tester) async {
    final f = GlobalFixture(); var opens = 0;
    try {
      await tester.runAsync(f.start);
      f.messages[cidA] = [searchFile('normal', 'match.txt')];
      await tester.runAsync(() => f.controller.search('match'));
      final hit = f.controller.visibleFiles.single;
      await f.mount(tester, home: () => GlobalSearchFilePage(controller: f.controller, hit: hit,
          openExternal: (_) async { opens++; return true; }));
      expect(find.text('match.txt'), findsOneWidget);
      await tester.tap(find.text('使用外部应用打开')); await tester.pumpAndSettle();
      expect(opens, 1); expect(f.calls('findMessageList'), 2);
      f.sdk.gateway.privacyLedger.observe(cidA, {...searchFile('normal', 'match.txt'),
        'attachedInfoElem': {'isPrivateChat': true}}, now: f.wall);
      await tester.pump(); await tester.pump();
      final button = tester.widget<FilledButton>(find.widgetWithText(FilledButton, '使用外部应用打开'));
      expect(button.onPressed, isNull);
      expect(find.text('match.txt'), findsNothing); expect(opens, 1);
    } finally { await f.close(tester); }
  });
  testWidgets('file read followed by foreign-route ABA cannot late-open OS; a new explicit click works', (tester) async {
    final f = GlobalFixture(); var opens = 0;
    try {
      await tester.runAsync(f.start);
      f.messages[cidA] = [searchFile('normal', 'match.txt')];
      await tester.runAsync(() => f.controller.search('match'));
      final hit = f.controller.visibleFiles.single;
      await f.mount(tester, home: () => GlobalSearchFilePage(controller: f.controller, hit: hit,
          openExternal: (_) async { opens++; return true; }));
      final response = f.defer<String>(wireGroups({})); var entered = false;
      f.pending = (call) {
        if (call.method == 'findMessageList') { entered = true; return response.future; }
        return null;
      };
      await tester.tap(find.text('使用外部应用打开')); await tester.pump();
      expect(entered, isTrue);
      final nav = f.router!.routerDelegate.navigatorKey.currentState!;
      unawaited(nav.push<void>(MaterialPageRoute(builder: (_) => const Scaffold(body: Text('foreign')))));
      await tester.pumpAndSettle(); nav.pop(); await tester.pumpAndSettle();
      f.pending = null;
      response.complete(wireGroups({cidA: [searchFile('normal', 'match.txt')]}));
      await tester.pumpAndSettle();
      expect(opens, 0);
      await tester.tap(find.text('使用外部应用打开')); await tester.pumpAndSettle();
      expect(opens, 1);
    } finally { await f.close(tester); }
  });
  testWidgets('old file action callback after issuer and tenant switch dispatches no source read or external open', (tester) async {
    final f = GlobalFixture(); var opens = 0;
    try {
      await tester.runAsync(f.start);
      f.messages[cidA] = [searchFile('normal', 'match.txt')];
      await tester.runAsync(() => f.controller.search('match'));
      final hit = f.controller.visibleFiles.single;
      await f.mount(tester, home: () => GlobalSearchFilePage(controller: f.controller, hit: hit,
          openExternal: (_) async { opens++; return true; }));
      final old = tester.widget<FilledButton>(find.widgetWithText(FilledButton, '使用外部应用打开')).onPressed!;
      final reads = f.calls('findMessageList');
      await tester.runAsync(f.rotateOwner); await tester.pump();
      old(); await tester.pump();
      expect(f.calls('findMessageList'), reads); expect(opens, 0);
      expect(f.navigations, isEmpty);
    } finally { await f.close(tester); }
  });
}
