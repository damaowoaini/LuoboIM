import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/conversation/presentation/conversations_page.dart';
import 'package:imapp/features/rtc/data/call_records_repository.dart';
import 'package:imapp/features/rtc/domain/call_record.dart';
import 'package:imapp/features/rtc/domain/rtc_call.dart';
import 'package:imapp/features/rtc/presentation/call_records_page.dart';
import 'package:imapp/features/rtc/presentation/rtc_page.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

import 'call_records_fixture.dart';

Future<void> finish(WidgetTester tester, CallRecordsFixture f) async {
  try { await tester.runAsync(f.close); } finally {
    print('call_records_cleanup unmount.begin');
    await tester.pumpWidget(const SizedBox()); await tester.pump();
    print('call_records_cleanup unmount.end');
  }
}
Future<void> seed(CallRecordsFixture f) async {
  f.permissions.granted = false;
  await f.rtc.dial(inviteeIds: ['bob'], video: false, title: '同名好友');
  await f.history.flush();
  f.permissions.granted = true;
}
Future<void> waitRow(WidgetTester tester, CallRecordsFixture f) =>
  f.until(tester, () => find.byType(ListTile).evaluate().isNotEmpty &&
    find.byType(LinearProgressIndicator).evaluate().isEmpty);
Future<void> tapOwned(WidgetTester tester, CallRecordsFixture f, Finder target) async {
  await tester.ensureVisible(target); await tester.pump();
  expect(target.hitTestable(), findsOneWidget);
  await tester.runAsync(() => tester.tap(target.hitTestable()));
  await f.frame(tester);
}
Future<void> openRedial(WidgetTester tester, CallRecordsFixture f) async {
  await f.ready(tester, find.byType(CallRecordsPage));
  await tapOwned(tester, f, find.byType(ListTile));
  await f.until(tester, () => find.text('语音通话').evaluate().isNotEmpty);
  await f.ready(tester, find.text('语音通话'));
}

void main() {
  testWidgets('production inbox more menu reaches actual durable call history and back retains the inbox', (tester) async {
    final f = CallRecordsFixture(); await tester.runAsync(f.start);
    try {
      await f.mount(tester, inbox: true);
      await f.ready(tester, find.byType(ConversationsPage));
      final original = tester.state<State<ConversationsPage>>(find.byType(ConversationsPage));
      await tapOwned(tester, f, find.byTooltip('更多操作'));
      await f.until(tester, () => find.text('会议（预约与管理）').evaluate().length == 1);
      await f.ready(tester, find.text('会议（预约与管理）'));
      expect(find.text('通话记录').hitTestable(), findsOneWidget);
      await tapOwned(tester, f, find.text('通话记录'));
      await f.until(tester, () => find.byType(CallRecordsPage).evaluate().length == 1);
      await f.ready(tester, find.byType(CallRecordsPage));
      await f.until(tester, () => find.text('暂无通话记录').evaluate().isNotEmpty);
      expect(f.router.state.uri.path, '/calls'); expect(f.permissions.requests, 0);
      final route = ModalRoute.of(tester.element(find.byType(CallRecordsPage)))!;
      await tapOwned(tester, f, find.descendant(of: find.byType(CallRecordsPage), matching: find.byType(BackButton)));
      await f.removed(tester, route);
      expect(tester.state<State<ConversationsPage>>(find.byType(ConversationsPage)), same(original));
      expect(tester.takeException(), isNull);
    } finally { await finish(tester, f); }
  });

  testWidgets('all and missed tabs render distinct actual coordinator outcomes with stable peer identity', (tester) async {
    final f = CallRecordsFixture(); await tester.runAsync(f.start);
    try {
      await tester.runAsync(() async {
        await seed(f);
        final call = historyInvitation(room: 'missed');
        f.transport.events.add(RtcSignal(ImSignalAction.invited, call));
        f.transport.events.add(RtcSignal(ImSignalAction.cancelled, call));
        await f.history.flush();
      });
      await f.mount(tester); await waitRow(tester, f);
      expect(find.byType(ListTile), findsNWidgets(2));
      expect(find.textContaining('未获得通话权限'), findsOneWidget);
      expect(find.textContaining('未接来电'), findsOneWidget);
      await tapOwned(tester, f, find.text('未接')); await waitRow(tester, f);
      expect(find.byType(ListTile), findsOneWidget);
      expect(find.textContaining('ID：bob'), findsOneWidget);
      expect(find.textContaining('未获得通话权限'), findsNothing);
      expect(tester.takeException(), isNull);
    } finally { await finish(tester, f); }
  });

  testWidgets('storage read error is not presented as empty and only explicit retry recovers', (tester) async {
    final f = CallRecordsFixture(); await tester.runAsync(f.start);
    try {
      var fail = true;
      f.overrideOpen = (key) {
        if (fail) throw StateError('owned synthetic read failure');
        return initializeCallRecordsDatabase(databaseFactoryFfi, '${f.directory.path}/$key.db', key);
      };
      await f.mount(tester);
      await f.until(tester, () => find.text('重新读取').evaluate().isNotEmpty);
      expect(find.text('暂无通话记录'), findsNothing);
      expect(find.byType(LinearProgressIndicator), findsNothing);
      fail = false;
      await tapOwned(tester, f, find.text('重新读取'));
      await f.until(tester, () => find.text('暂无通话记录').evaluate().isNotEmpty);
      expect(find.text('重新读取'), findsNothing); expect(tester.takeException(), isNull);
    } finally { await finish(tester, f); }
  });

  testWidgets('new durable RTC events mark a loaded snapshot stale and explicit refresh reads the new record', (tester) async {
    final f = CallRecordsFixture(); await tester.runAsync(f.start);
    try {
      await f.mount(tester);
      await f.until(tester, () => find.text('暂无通话记录').evaluate().isNotEmpty);
      await tester.runAsync(() async {
        final call = historyInvitation(room: 'arrived');
        f.transport.events.add(RtcSignal(ImSignalAction.invited, call));
        f.transport.events.add(RtcSignal(ImSignalAction.cancelled, call));
        await f.history.flush();
      });
      await f.frame(tester);
      expect(find.text('通话记录已更新，刷新后查看'), findsOneWidget);
      expect(find.text('暂无通话记录'), findsNothing);
      await tapOwned(tester, f, find.text('刷新记录')); await waitRow(tester, f);
      expect(find.textContaining('未接来电'), findsOneWidget);
      expect(find.textContaining('ID：bob'), findsOneWidget);
      expect(f.transport.joins, 0); expect(tester.takeException(), isNull);
    } finally { await finish(tester, f); }
  });

  testWidgets('cancelled delete preserves its row while confirmed delete is durable and never hangs up a call', (tester) async {
    final f = CallRecordsFixture(); await tester.runAsync(f.start);
    try {
      await tester.runAsync(() => seed(f)); await f.mount(tester); await waitRow(tester, f);
      await tapOwned(tester, f, find.byTooltip('删除 同名好友 的通话记录'));
      await f.until(tester, () => find.text('删除记录').evaluate().isNotEmpty);
      await f.ready(tester, find.text('删除记录'));
      final cancelRoute = ModalRoute.of(tester.element(find.text('取消')))!;
      await tapOwned(tester, f, find.text('取消')); await f.removed(tester, cancelRoute);
      expect(find.byType(ListTile), findsOneWidget);
      await tapOwned(tester, f, find.byTooltip('删除 同名好友 的通话记录'));
      await f.until(tester, () => find.text('删除记录').evaluate().isNotEmpty);
      await f.ready(tester, find.text('删除记录'));
      final deleting = ModalRoute.of(tester.element(find.text('删除记录')))!;
      await tapOwned(tester, f, find.text('删除记录')); await f.removed(tester, deleting);
      await f.until(tester, () => find.text('暂无通话记录').evaluate().isNotEmpty);
      expect(await tester.runAsync(() => f.rows()), isEmpty);
      expect(f.transport.endActions, isEmpty); expect(f.transport.joins, 0);
      expect(tester.takeException(), isNull);
    } finally { await finish(tester, f); }
  });

  for (final video in [false, true]) {
    testWidgets('real history ${video ? 'video' : 'audio'} redial dispatches the exact stored peer through RtcCoordinator once', (tester) async {
      final f = CallRecordsFixture(); await tester.runAsync(f.start);
      try {
        await tester.runAsync(() => seed(f)); await f.mount(tester); await waitRow(tester, f);
        await openRedial(tester, f);
        final route = ModalRoute.of(tester.element(find.text('语音通话')))!;
        await tapOwned(tester, f, find.text(video ? '视频通话' : '语音通话'));
        await f.removed(tester, route);
        await f.until(tester, () => find.byType(RtcPage).evaluate().length == 1);
        await f.ready(tester, find.byType(RtcPage));
        expect(f.rtc.call!.inviteeIds, ['bob']); expect(f.rtc.call!.video, video);
        expect(f.transport.joins, 1); expect(f.permissions.requests, 2);
        expect(f.router.state.uri.path, '/rtc'); expect(tester.takeException(), isNull);
      } finally { await finish(tester, f); }
    });
  }

  testWidgets('busy changes while the owned redial sheet is open stop a second native call', (tester) async {
    final f = CallRecordsFixture(); await tester.runAsync(f.start);
    try {
      await tester.runAsync(() => seed(f)); await f.mount(tester); await waitRow(tester, f);
      await openRedial(tester, f);
      f.transport.events.add(RtcSignal(ImSignalAction.invited, historyInvitation(room: 'current-other-call')));
      await tapOwned(tester, f, find.text('视频通话'));
      await f.until(tester, () => find.text('正在通话，请结束后再回拨').evaluate().isNotEmpty);
      expect(f.permissions.requests, 1); expect(f.transport.joins, 0);
      expect(f.rtc.call!.roomId, 'current-other-call'); expect(tester.takeException(), isNull);
    } finally { await finish(tester, f); }
  });

  testWidgets('a saved old row callback after foreign route ABA cannot renew its view ticket while a new click works', (tester) async {
    final f = CallRecordsFixture(); await tester.runAsync(f.start);
    try {
      await tester.runAsync(() => seed(f)); await f.mount(tester); await waitRow(tester, f);
      final old = tester.widget<ListTile>(find.byType(ListTile)).onTap!;
      await f.coverAndReturn(tester, find.byType(CallRecordsPage));
      old(); await f.frame(tester);
      expect(find.text('语音通话'), findsNothing); expect(f.permissions.requests, 1);
      await openRedial(tester, f); expect(find.text('语音通话').hitTestable(), findsOneWidget);
      final route = ModalRoute.of(tester.element(find.text('取消')))!;
      await tapOwned(tester, f, find.text('取消')); await f.removed(tester, route);
      expect(f.transport.joins, 0); expect(tester.takeException(), isNull);
    } finally { await finish(tester, f); }
  });

  testWidgets('foreign coverage of an owned redial sheet retires its sensitive content and does not revive the old choice', (tester) async {
    final f = CallRecordsFixture(); await tester.runAsync(f.start);
    try {
      await tester.runAsync(() => seed(f)); await f.mount(tester); await waitRow(tester, f);
      await openRedial(tester, f);
      final old = tester.widget<FilledButton>(find.widgetWithText(FilledButton, '语音通话')).onPressed!;
      await f.coverAndReturn(tester, find.text('语音通话'));
      await f.until(tester, () => find.text('会话或页面已变化，请关闭后重新操作').evaluate().isNotEmpty);
      old(); await f.frame(tester);
      expect(find.text('语音通话'), findsNothing); expect(f.transport.joins, 0);
      final route = ModalRoute.of(tester.element(find.text('关闭')))!;
      await f.ready(tester, find.text('关闭')); await tapOwned(tester, f, find.text('关闭'));
      await f.removed(tester, route);
      await openRedial(tester, f); expect(find.text('语音通话').hitTestable(), findsOneWidget);
      expect(tester.takeException(), isNull);
    } finally { await finish(tester, f); }
  });

  testWidgets('owner replacement removes old confirmation details and its retained delete callback cannot affect either owner', (tester) async {
    final f = CallRecordsFixture(); await tester.runAsync(f.start);
    try {
      await tester.runAsync(() => seed(f)); final oldOwner = f.sdk.session.state.credentials!;
      await f.mount(tester); await waitRow(tester, f);
      await tapOwned(tester, f, find.byTooltip('删除 同名好友 的通话记录'));
      await f.until(tester, () => find.text('删除记录').evaluate().isNotEmpty);
      await f.ready(tester, find.text('删除记录'));
      final old = tester.widget<FilledButton>(find.widgetWithText(FilledButton, '删除记录')).onPressed!;
      await tester.runAsync(() => f.sdk.signInOther(user: 'other'));
      await f.frame(tester); old(); await f.frame(tester);
      expect(find.text('ID：bob'), findsNothing);
      final oldDB = CallRecordsRepository(accountKey: oldOwner.accountKey, open: f.open);
      try {
        final original = await tester.runAsync(() => oldDB.list(filter: CallRecordsFilter.all, current: () => true));
        expect(original!.items, hasLength(1));
      } finally { await tester.runAsync(oldDB.close); }
      expect(await tester.runAsync(() => f.rows()), isEmpty);
      expect(f.transport.joins, 0); expect(tester.takeException(), isNull);
    } finally { await finish(tester, f); }
  });

  testWidgets('deleting an active record does not hang up and its later real terminal event cannot resurrect it', (tester) async {
    final f = CallRecordsFixture(); await tester.runAsync(f.start);
    try {
      await tester.runAsync(() async { await f.connectedOutgoing(); await f.history.flush(); });
      final call = f.rtc.call!; await f.mount(tester); await waitRow(tester, f);
      await tapOwned(tester, f, find.byTooltip('删除 同名好友 的通话记录'));
      await f.until(tester, () => find.text('删除记录').evaluate().isNotEmpty);
      await f.ready(tester, find.text('删除记录'));
      await tapOwned(tester, f, find.text('删除记录'));
      await f.until(tester, () => find.text('暂无通话记录').evaluate().isNotEmpty);
      expect(f.rtc.busy, isTrue); expect(f.transport.endActions, isEmpty);
      await tester.runAsync(() async {
        f.transport.events.add(RtcSignal(ImSignalAction.hungUp, call)); await f.history.flush();
      });
      expect(await tester.runAsync(() => f.rows()), isEmpty);
      expect(f.transport.endActions, isEmpty); expect(tester.takeException(), isNull);
    } finally { await finish(tester, f); }
  });

  testWidgets('leaving during a delayed initial SQLite open cannot publish old rows or errors into the inbox', (tester) async {
    final f = CallRecordsFixture(); await tester.runAsync(f.start);
    final entered = Completer<void>(); final reply = Completer<Database>();
    try {
      f.overrideOpen = (_) { if (!entered.isCompleted) entered.complete(); return reply.future; };
      await f.mount(tester);
      await tester.runAsync(() => entered.future.timeout(const Duration(seconds: 5)));
      f.router.go('/home'); await f.frame(tester);
      await f.ready(tester, find.byType(ConversationsPage));
      await tester.runAsync(() async { reply.completeError(StateError('owned late open failure')); });
      await f.frame(tester);
      expect(find.byType(CallRecordsPage), findsNothing);
      expect(find.text('无法读取或保存本地资料，请重试'), findsNothing);
      expect(tester.takeException(), isNull);
    } finally {
      if (!reply.isCompleted) reply.completeError(StateError('release owned read'));
      await finish(tester, f);
    }
  });

  testWidgets('call history and owned actions remain scrollable and accessible at 320 by 568 with large text', (tester) async {
    final f = CallRecordsFixture(); await tester.runAsync(f.start);
    try {
      await tester.runAsync(() => seed(f)); await f.mount(tester, width: 320, height: 568);
      await waitRow(tester, f);
      expect(find.textContaining('ID：bob'), findsOneWidget);
      final remove = find.byTooltip('删除 同名好友 的通话记录');
      expect(tester.getSize(remove).height, greaterThanOrEqualTo(44));
      await tapOwned(tester, f, remove);
      await f.until(tester, () => find.text('删除记录').evaluate().isNotEmpty);
      await f.ready(tester, find.text('删除记录'));
      final button = find.widgetWithText(FilledButton, '删除记录');
      await tester.ensureVisible(button); await tester.pump();
      expect(button.hitTestable(), findsOneWidget);
      expect(tester.getSize(button).height, greaterThanOrEqualTo(44));
      expect(tester.getRect(button).right, lessThanOrEqualTo(320));
      final route = ModalRoute.of(tester.element(find.text('取消')))!;
      await tapOwned(tester, f, find.text('取消')); await f.removed(tester, route);
      expect(find.byType(ListTile), findsOneWidget); expect(tester.takeException(), isNull);
    } finally { await finish(tester, f); }
  });
}
