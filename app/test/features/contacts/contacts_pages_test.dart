import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/features/contacts/contacts_providers.dart';
import 'package:imapp/features/contacts/domain/contact.dart';
import 'package:imapp/features/contacts/presentation/contact_requests_page.dart';
import 'package:imapp/features/contacts/presentation/contacts_page.dart';
import 'package:imapp/features/contacts/presentation/select_contacts_page.dart';
import 'package:imapp/features/group/presentation/group_page.dart';
import 'package:imapp/providers.dart';

import '../../core/im/capture_theme.dart';
import '../../core/im/sdk_harness.dart';
import '../../support/http_adapter.dart';

void main() {
  late SdkHarness harness;
  late TenantApi api;
  setUp(() {
    harness = SdkHarness();
  });
  tearDown(() => harness.dispose());

  Future<GlobalKey> mount(WidgetTester tester, Widget page) async {
    await tester.runAsync(harness.start);
    api = TenantApi(
      SessionAccess(harness.session),
      dio: Dio()
        ..httpClientAdapter = TestHttpAdapter(
          (_) async => jsonResponse('{"errCode":0,"data":{"users":[]}}'),
        ),
    );
    harness.overrideCall = (call) async {
      switch (call.method) {
        case 'getGroupsInfo':
          return jsonEncode([
            {
              'groupID': 'group-a',
              'groupName': '设计讨论群',
              'groupType': 2,
              'ownerUserID': 'alice',
              'memberCount': 3,
              'lookMemberInfo': 1,
              'applyMemberFriend': 1,
              'status': 0,
            },
          ]);
        case 'isJoinGroup':
          return 'true';
        case 'getGroupMembersInfo':
          return jsonEncode([
            {
              'groupID': 'group-a',
              'userID': 'alice',
              'nickname': '我的群昵称',
              'roleLevel': 100,
            },
          ]);
        case 'getFriendList':
          return jsonEncode([
            {
              'friendInfo': {
                'userID': 'bob',
                'nickname': '设计协作好友',
                'remark': '产品讨论',
              },
            },
            {
              'friendInfo': {'userID': 'carol', 'nickname': '另一位好友'},
            },
          ]);
        case 'getFriendApplicationListAsRecipient':
          return jsonEncode([
            {
              'fromUserID': 'bob',
              'toUserID': 'alice',
              'fromNickname': '收到的申请',
              'handleResult': 0,
              'createTime': 20,
            },
          ]);
        case 'getFriendApplicationListAsApplicant':
          return jsonEncode([
            {
              'fromUserID': 'alice',
              'toUserID': 'carol',
              'toNickname': '发出的申请',
              'handleResult': 0,
              'createTime': 10,
            },
          ]);
      }
      return harness.respond(call);
    };
    tester.view.physicalSize = const Size(360, 640);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final key = GlobalKey();
    final theme = await captureTheme(tester);
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          imGatewayProvider.overrideWithValue(harness.gateway),
          appRuntimeProvider.overrideWith((_) => harness.runtime),
          tenantApiProvider.overrideWith((ref) {
            ref.onDispose(api.close);
            return api;
          }),
        ],
        child: RepaintBoundary(
          key: key,
          child: MaterialApp(
            debugShowCheckedModeBanner: false,
            theme: theme,
            builder: (context, child) => MediaQuery(
              data: MediaQuery.of(context)
                  .copyWith(textScaler: TextScaler.linear(1.6)),
              child: child!,
            ),
            home: page,
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    return key;
  }

  Future<void> close(WidgetTester tester) async {
    await tester.runAsync(harness.dispose);
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump();
  }

  testWidgets(
    'address book preserves remarks and filters friends on a small large-text screen',
    (tester) async {
      final key = await mount(tester, const ContactsPage());
      expect(find.text('新的朋友'), findsOneWidget);
      expect(tester.takeException(), isNull);
      const directory = String.fromEnvironment('TEST_CAPTURE_DIR');
      if (directory.isNotEmpty) {
        await tester.runAsync(() async {
          final boundary =
              key.currentContext!.findRenderObject()! as RenderRepaintBoundary;
          final image = await boundary.toImage(pixelRatio: 2);
          final bytes = (await image.toByteData(
            format: ui.ImageByteFormat.png,
          ))!;
          await Directory(directory).create(recursive: true);
          await File('$directory/contacts-widget.png')
              .writeAsBytes(bytes.buffer.asUint8List());
          image.dispose();
        });
      }
      await tester.enterText(find.byType(TextField), '不存在');
      await tester.pumpAndSettle();
      await tester.ensureVisible(find.text('没有找到匹配的好友'));
      expect(find.text('没有找到匹配的好友'), findsOneWidget);
      await close(tester);
    },
  );
  testWidgets(
    'large address books build visible rows on demand and still search the complete friend list',
    (tester) async {
      final key = await mount(tester, const ContactsPage());
      final original = harness.overrideCall!;
      harness.overrideCall = (call) async {
        if (call.method == 'getFriendList') {
          return jsonEncode([
            for (var i = 0; i < 5000; i++)
              {
                'friendInfo': {'userID': 'friend-$i', 'nickname': '好友$i'},
              },
          ]);
        }
        return original(call);
      };
      ProviderScope.containerOf(
        key.currentContext!,
        listen: false,
      ).invalidate(friendsProvider);
      await tester.pumpAndSettle();
      expect(find.byType(ListTile).evaluate().length, lessThan(50));
      expect(find.text('好友4999'), findsNothing);
      await tester.enterText(find.byType(TextField), '好友4999');
      await tester.pumpAndSettle();
      await tester.ensureVisible(find.widgetWithText(ListTile, '好友4999'));
      expect(find.widgetWithText(ListTile, '好友4999'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await close(tester);
    },
  );
  testWidgets(
    'only incoming pending requests offer handling, rejecting needs an explicit dialog submission',
    (tester) async {
      await mount(
        tester,
        const ContactRequestsPage(kind: ContactRequestKind.friend),
      );
      expect(find.text('通过'), findsOneWidget);
      expect(find.text('等待验证'), findsOneWidget);
      await tester.tap(find.text('拒绝'));
      await tester.pumpAndSettle();
      expect(find.text('拒绝申请'), findsOneWidget);
      expect(
        harness.calls.any((call) => call.method == 'refuseFriendApplication'),
        isFalse,
      );
      await tester.tap(find.text('取消'));
      await tester.pumpAndSettle();
      expect(
        harness.calls.any((call) => call.method == 'refuseFriendApplication'),
        isFalse,
      );
      await tester.tap(find.text('通过'));
      await tester.pumpAndSettle();
      expect(
        harness.calls
            .lastWhere((call) => call.method == 'acceptFriendApplication')
            .arguments['toUserID'],
        'bob',
      );
      expect(tester.takeException(), isNull);
      await close(tester);
    },
  );
  testWidgets(
    'group restrictions keep explicit prohibition labels and dismissing requires confirmation',
    (tester) async {
      await mount(tester, const GroupPage(groupId: 'group-a'));
      await tester.scrollUntilVisible(
        find.text('禁止查看成员资料'),
        300,
        scrollable: find
            .descendant(
              of: find.byType(ListView),
              matching: find.byType(Scrollable),
            )
            .first,
      );
      await tester.pumpAndSettle();
      final hideProfiles = tester.widget<SwitchListTile>(
        find.widgetWithText(SwitchListTile, '禁止查看成员资料'),
      );
      expect(hideProfiles.value, isTrue);
      final preventFriends = tester.widget<SwitchListTile>(
        find.widgetWithText(SwitchListTile, '禁止通过群聊添加好友'),
      );
      expect(preventFriends.value, isTrue);
      await tester.scrollUntilVisible(
        find.text('解散群聊'),
        300,
        scrollable: find
            .descendant(
              of: find.byType(ListView),
              matching: find.byType(Scrollable),
            )
            .first,
      );
      await tester.tap(find.text('解散群聊'));
      await tester.pumpAndSettle();
      expect(find.text('解散群聊？'), findsOneWidget);
      expect(
        harness.calls.any((call) => call.method == 'dismissGroup'),
        isFalse,
      );
      await tester.tap(find.text('取消'));
      await tester.pumpAndSettle();
      expect(
        harness.calls.any((call) => call.method == 'dismissGroup'),
        isFalse,
      );
      expect(tester.takeException(), isNull);
      await close(tester);
    },
  );
  testWidgets(
    'contact picker excludes existing members and enables confirmation only after choosing someone',
    (tester) async {
      await mount(
        tester,
        const SelectContactsPage(title: '邀请好友', excluded: {'bob'}),
      );
      expect(find.text('产品讨论'), findsNothing);
      final disabled = tester.widget<TextButton>(
        find.widgetWithText(TextButton, '确定 (0)'),
      );
      expect(disabled.onPressed, isNull);
      await tester.tap(find.text('另一位好友'));
      await tester.pumpAndSettle();
      expect(
        tester
            .widget<TextButton>(find.widgetWithText(TextButton, '确定 (1)'))
            .onPressed,
        isNotNull,
      );
      expect(tester.takeException(), isNull);
      await close(tester);
    },
  );
}
