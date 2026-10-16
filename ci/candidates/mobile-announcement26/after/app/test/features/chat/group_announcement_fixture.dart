import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart' show debugPrintSynchronously;
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/tenant/session_credentials.dart';
import 'package:imapp/features/chat/data/group_announcement_store.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';
import 'package:imapp/providers.dart';

import '../../core/im/capture_theme.dart';
import 'group_revoke_fixture.dart';

/// Real production ChatPage/repositories/session/SDK codec; only the OS
/// preferences and SDK transport are replaced. No announcement UI is mocked.
final class GroupAnnouncementFixture {
  final business = GroupRevokeFixture();
  final values = <String, bool>{};
  final writes = <String>[];
  String text = '本周群活动安排', publisher = 'bob';
  int version = 1700000000, hint = 4, groupReads = 0, storeReads = 0;
  bool failRead = false, failWrite = false;
  List<Map<String, dynamic>>? groupRows;
  Completer<void>? profileReply, profileEntered, writeReply, writeEntered;
  final _releases = <VoidCallback>[];
  MaterialPageRoute<void>? chatRoute;

  late final store = GroupAnnouncementStore(
    readBool: (key) async {
      ++storeReads;
      return values[key];
    },
    writeBool: (key, value) async {
      writes.add(key);
      if (writeEntered?.isCompleted == false) {
        writeEntered!.complete();
      }
      if (writeReply case final pending?) {
        await pending.future;
      }
      if (failWrite) {
        throw StateError('synthetic preferences failure');
      }
      values[key] = value;
    },
  );

  Completer<void> defer() {
    final value = Completer<void>();
    _releases.add(() {
      if (!value.isCompleted) {
        value.complete();
      }
    });
    return value;
  }

  Future<void> start() async {
    await business.start(withController: false);
    business.metadata['groupAtType'] = hint;
    final prior = business.sdk.overrideCall!;
    business.sdk.overrideCall = (call) async {
      if (call.method != 'getGroupsInfo') {
        return prior(call);
      }
      ++groupReads;
      if (profileEntered?.isCompleted == false) {
        profileEntered!.complete();
      }
      final waiting = profileReply;
      if (waiting != null) {
        await waiting.future;
      }
      if (failRead) {
        throw PlatformException(code: '1002');
      }
      return jsonEncode(
        groupRows ??
            [
              {
                'groupID': 'group-a',
                'groupName': '公告测试群',
                'groupType': 2,
                'status': business.dismissed ? 2 : 0,
                'notification': text,
                'notificationUpdateTime': version,
                'notificationUserID': publisher,
                'memberCount': 3,
              },
            ],
      );
    };
  }

  Future<void> mount(WidgetTester tester, {bool small = false}) async {
    await tester.runAsync(start);
    if (small) {
      tester.view.physicalSize = const Size(360, 640);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
    }
    final theme = await captureTheme(tester);
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          appRuntimeProvider.overrideWith((_) => business.sdk.runtime),
          imGatewayProvider.overrideWithValue(business.sdk.gateway),
          groupAnnouncementStoreProvider.overrideWithValue(store),
          chatReadingStoreProvider.overrideWithValue(business.base.reading),
          chatBackgroundStoreProvider.overrideWithValue(
            business.base.backgrounds,
          ),
        ],
        child: MaterialApp(
          navigatorKey: business.base.navigator,
          theme: theme,
          builder: (context, child) => MediaQuery(
            data: MediaQuery.of(context)
                .copyWith(textScaler: TextScaler.linear(small ? 1.6 : 1)),
            child: child!,
          ),
          home: const Scaffold(body: Text('群公告测试入口')),
        ),
      ),
    );
    await openChat(tester);
  }

  Future<void> openChat(WidgetTester tester) async {
    final route = MaterialPageRoute<void>(
      builder: (_) => ValueListenableBuilder<String>(
        valueListenable: business.sourceCid,
        builder: (_, cid, _) =>
            ChatPage(conversationId: cid, now: () => business.base.clock),
      ),
    );
    chatRoute = route;
    unawaited(business.base.navigator.currentState!.push<void>(route));
    await business.base.ready(tester, find.text('待核对的原消息'));
  }

  Future<void> leaveChat(WidgetTester tester) async {
    final route = chatRoute!;
    final back = find.byType(BackButton).hitTestable();
    await business.base.ready(tester, back);
    await tester.tap(back);
    await business.base.actual(tester, () => route.completed);
  }

  Finder get banner => find.byKey(const ValueKey('group-announcement-banner'));
  Finder get preview => find.widgetWithText(TextButton, '群公告：$text');
  Finder get dismiss => find.byWidgetPredicate(
    (widget) => widget is IconButton && widget.tooltip == '关闭本条群公告提醒',
  );
  Finder get returnToChat => find.widgetWithText(TextButton, '返回聊天');

  Future<void> ready(WidgetTester tester) =>
      business.base.ready(tester, preview);
  Future<void> open(WidgetTester tester) async {
    await ready(tester);
    await tester.tap(preview.hitTestable());
    await business.base.ready(tester, returnToChat);
    expect(find.widgetWithText(AlertDialog, '群公告'), findsOneWidget);
  }

  Future<void> closeDetail(WidgetTester tester) async {
    await business.base.ready(tester, returnToChat);
    final route = ModalRoute.of(tester.element(returnToChat))!;
    await tester.tap(returnToChat.hitTestable());
    await business.base.actual(tester, () => route.completed);
  }

  Future<void> groupEvent() =>
      business.sdk.callback(ListenerType.groupListener, {
        'type': 'onGroupInfoChanged',
        'data': jsonEncode({'groupID': 'group-a'}),
      });
  Future<void> changeOwner(String dimension) async {
    final before = business.sdk.session.state.credentials!;
    await business.sdk.session.signOut();
    final user = dimension == 'owner' ? 'carol' : before.userId;
    business.sdk.userId = user;
    await business.sdk.session.signIn(
      (_) async => SessionCredentials(
        userId: user,
        tenantId: dimension == 'tenant' ? 'next-tenant' : before.tenantId,
        tenantName: before.tenantName,
        domains: before.domains,
        chatToken: before.chatToken,
        imToken: before.imToken,
        issuer: dimension == 'issuer'
            ? 'https://next.example.com/'
            : before.issuer,
      ),
    );
  }

  Future<MaterialPageRoute<void>> cover(WidgetTester tester) async {
    final route = MaterialPageRoute<void>(
      builder: (_) => const Scaffold(body: Text('外来公告测试页面')),
    );
    unawaited(business.base.navigator.currentState!.push<void>(route));
    await business.base.ready(tester, find.text('外来公告测试页面'));
    return route;
  }

  Future<void> uncover(
    WidgetTester tester,
    MaterialPageRoute<void> route,
  ) async {
    business.base.navigator.currentState!.pop();
    await business.base.actual(tester, () => route.completed);
  }

  void release() {
    for (final release in _releases) {
      release();
    }
  }

  Future<void> close(WidgetTester tester) async {
    release();
    debugPrintSynchronously('group_announcement_cleanup sdk.begin');
    try {
      await business.base.actual(
        tester,
        () => business.sdk.dispose().timeout(const Duration(seconds: 20)),
      );
      debugPrintSynchronously('group_announcement_cleanup sdk.end');
    } finally {
      debugPrintSynchronously('group_announcement_cleanup unmount.begin');
      await tester.pumpWidget(const SizedBox.shrink());
      await tester.pumpAndSettle();
      await business.base.actual(
        tester,
        () => store.close().timeout(const Duration(seconds: 20)),
      );
      business.sourceCid.dispose();
      business.base.reading.dispose();
      business.base.backgrounds.dispose();
      debugPrintSynchronously('group_announcement_cleanup unmount.end');
    }
  }
}
