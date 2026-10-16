import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/core/tenant/session_credentials.dart';
import 'package:imapp/features/contacts/contacts_providers.dart';
import 'package:imapp/features/group/data/group_repository.dart';
import 'package:imapp/features/group/presentation/group_members_page.dart';
import 'package:imapp/providers.dart';

import '../../core/im/capture_theme.dart';
import '../../core/im/sdk_harness.dart';
import '../../support/http_adapter.dart';

final class BatchFixture {
  final sdk = SdkHarness();
  final navigator = GlobalKey<NavigatorState>();
  final groupId = ValueNotifier('group-a');
  final kickCalls = <Map<String, dynamic>>[];
  final roles = <String, int>{'alice': 100, 'bob': 20, 'carol': 20, 'dave': 60};
  final names = <String, String>{
    'alice': '我',
    'bob': '同名成员',
    'carol': '同名成员',
    'dave': '管理员',
  };
  int status = 0;
  Future<void> Function(List<String> ids)? beforeRoles;
  Future<Object?> Function(List<String> ids, int index)? kickReply;
  Future<List<Map<String, Object?>>> Function(
    String id,
    String query,
    int offset,
  )?
  readReply;
  late GroupRepository repository;
  late TenantApi api;
  late ProviderContainer container;

  Map<String, Object?> member(String id, String user) => {
    'groupID': id,
    'userID': user,
    'nickname': names[user] ?? user,
    'roleLevel': roles[user],
    'muteEndTime': 4102444800000,
  };

  Future<void> start() async {
    await sdk.start();
    repository = GroupRepository(sdk.gateway, SessionAccess(sdk.session));
    api = TenantApi(
      repository.access,
      dio: Dio()
        ..httpClientAdapter = TestHttpAdapter(
          (_) async => jsonResponse('{"errCode":0,"data":{"users":[]}}'),
        ),
    );
    sdk.overrideCall = (call) async {
      final args = call.arguments as Map;
      switch (call.method) {
        case 'getGroupsInfo':
          return jsonEncode([
            for (final id in args['groupIDList'] as List)
              {
                'groupID': id,
                'groupName': '测试群',
                'groupType': 2,
                'memberCount': roles.length,
                'status': status,
                'ownerUserID': 'alice',
              },
          ]);
        case 'isJoinGroup':
          return 'true';
        case 'getGroupMembersInfo':
          final ids = (args['userIDList'] as List).cast<String>();
          await beforeRoles?.call(ids);
          return jsonEncode([
            for (final id in ids)
              if (roles.containsKey(id)) member(args['groupID'] as String, id),
          ]);
        case 'getGroupMemberList':
        case 'searchGroupMembers':
          final query = call.method == 'searchGroupMembers'
              ? args['searchParam'] as Map
              : args;
          final id = query['groupID'] as String;
          final word = (query['keywordList'] as List? ?? []).join();
          final offset = query['offset'] as int;
          final result = readReply == null
              ? [
                  for (final uid in roles.keys)
                    if (word.isEmpty ||
                        uid.contains(word) ||
                        (names[uid] ?? '').contains(word))
                      member(id, uid),
                ].skip(offset).take(query['count'] as int).toList()
              : await readReply!(id, word, offset);
          return jsonEncode(result);
        case 'kickGroupMember':
          kickCalls.add(Map<String, dynamic>.from(args));
          final ids = (args['userIDList'] as List).cast<String>();
          final result = kickReply == null
              ? '[]'
              : await kickReply!(ids, kickCalls.length);
          if (result == '[]') {
            for (final id in ids) {
              roles.remove(id);
            }
          }
          return result;
      }
      return sdk.respond(call);
    };
  }

  Future<void> mount(WidgetTester tester) async {
    await tester.runAsync(start);
    addTearDown(() async {
      api.close();
      await sdk.dispose();
    });
    tester.view.physicalSize = const Size(390, 880);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final theme = await captureTheme(tester);
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          imGatewayProvider.overrideWithValue(sdk.gateway),
          appRuntimeProvider.overrideWith((_) => sdk.runtime),
          tenantApiProvider.overrideWithValue(api),
        ],
        child: Builder(
          builder: (context) {
            container = ProviderScope.containerOf(context, listen: false);
            return MaterialApp(
              navigatorKey: navigator,
              theme: theme,
              builder: (context, child) => MediaQuery(
                data: MediaQuery.of(context)
                    .copyWith(textScaler: TextScaler.linear(1.5)),
                child: child!,
              ),
              home: ValueListenableBuilder(
                valueListenable: groupId,
                builder: (_, id, _) => id.isEmpty
                    ? const SizedBox.shrink()
                    : GroupMembersPage(groupId: id),
              ),
            );
          },
        ),
      ),
    );
    await until(
      tester,
      () =>
          find.text('群成员').evaluate().isNotEmpty &&
          find.byType(ListTile).evaluate().isNotEmpty &&
          container.read(groupProfileProvider(groupId.value)).asData != null,
    );
  }

  void refreshProfile() =>
      container.invalidate(groupProfileProvider(groupId.value));

  Finder get entry => find.byKey(const ValueKey('group-batch-remove-entry'));
  Finder get submit => find.byKey(const ValueKey('group-batch-remove-submit'));
  Future<void> choose(WidgetTester tester, List<String> ids) async {
    if (entry.evaluate().isNotEmpty) {
      await tester.tap(entry);
      await frame(tester);
    }
    for (final id in ids) {
      final row = find.byKey(ValueKey('batch-member-$id'));
      await tester.ensureVisible(row);
      await tester.tap(row);
      await frame(tester);
    }
  }

  Future<void> confirm(WidgetTester tester) async {
    await tester.tap(submit);
    await frame(tester);
    await tester.tap(find.widgetWithText(FilledButton, '确认'));
    await frame(tester);
  }

  Future<void> switchOwner({Future<void>? settled}) async {
    await sdk.session.signOut(revoke: false);
    // The real gateway quarantines while an old native operation is unsettled.
    // A test must await that observed operation, never bypass this guard.
    await settled;
    sdk.userId = 'other';
    roles['other'] = 100;
    await sdk.session.signIn(
      (_) async => SessionCredentials(
        userId: 'other',
        tenantId: 'tenant-b',
        tenantName: '另一个租户',
        domains: ['im.example.com'],
        issuer: 'https://route-b.example.com/',
        chatToken: 'other-chat',
        imToken: 'other-im',
      ),
    );
  }

  Future<void> close(WidgetTester tester) async {
    await tester.runAsync(() async {
      api.close();
      await sdk.dispose();
    });
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump();
    groupId.dispose();
  }
}

Future<void> frame(WidgetTester tester) async {
  await tester.pump();
  await tester.pump(const Duration(milliseconds: 400));
  await tester.pump();
}

Future<void> until(WidgetTester tester, bool Function() condition) async {
  for (var i = 0; i < 150; i++) {
    await tester.runAsync(() async => Future<void>.delayed(Duration.zero));
    await tester.pump(const Duration(milliseconds: 16));
    if (condition()) return;
  }
  throw TestFailure('Expected batch-removal stage did not settle');
}
