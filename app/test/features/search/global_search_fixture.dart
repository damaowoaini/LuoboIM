import 'dart:async';
import 'dart:convert';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/core/tenant/session_credentials.dart';
import 'package:imapp/features/contacts/contacts_providers.dart';
import 'package:imapp/features/contacts/data/contacts_repository.dart';
import 'package:imapp/features/contacts/presentation/user_profile_page.dart';
import 'package:imapp/features/conversation/presentation/conversations_page.dart';
import 'package:imapp/features/group/data/group_repository.dart';
import 'package:imapp/features/group/presentation/group_page.dart';
import 'package:imapp/features/search/data/global_search_repository.dart';
import 'package:imapp/features/search/presentation/global_search_controller.dart';
import 'package:imapp/features/search/presentation/global_search_page.dart';
import 'package:imapp/providers.dart';

import '../../core/im/capture_theme.dart';
import '../../core/im/sdk_harness.dart';
import '../../support/http_adapter.dart';

const cidA = 'single-alice-bob', cidHidden = 'single-alice-hidden';
Map<String, dynamic> searchText(String id, String text) => SdkHarness.message(id: id, text: text);
Map<String, dynamic> searchFile(String id, String name) => {
  ...SdkHarness.message(id: id, text: ''), 'contentType': 105,
  'fileElem': {'fileName': name, 'fileSize': 86, 'sourceUrl': 'https://media.example.com/$id'},
};
String wireGroups(Map<String, List<Map<String, dynamic>>> rows) => jsonEncode({
  'totalCount': rows.values.fold<int>(0, (sum, list) => sum + list.length),
  'searchResultItems': [for (final entry in rows.entries) {
    'conversationID': entry.key, 'showName': '同名会话',
    'conversationType': 1, 'messageCount': entry.value.length, 'messageList': entry.value,
  }],
});

final class GlobalFixture {
  GlobalFixture({Duration operationTimeout = const Duration(seconds: 15)})
      : sdk = SdkHarness(operationTimeout: operationTimeout);
  final SdkHarness sdk;
  final messages = <String, List<Map<String, dynamic>>>{
    cidA: [searchText('a1', 'match ordinary')],
    cidHidden: [searchText('h1', 'match hidden')],
  };
  final friends = <Map<String, Object?>>[
    {'friendInfo': {'userID': 'bob', 'nickname': 'match 同名', 'remark': '客户'}},
    {'friendInfo': {'userID': 'carol', 'nickname': 'match 同名'}},
  ];
  final groups = <Map<String, Object?>>[
    {'groupID': 'group-a', 'groupName': 'match 测试群', 'ownerUserID': 'alice',
      'groupType': 2, 'memberCount': 3, 'status': 0},
  ];
  final navigations = <({String cid, String? id, String? source, Object? extra})>[];
  Future<dynamic>? Function(MethodCall)? pending;
  late TenantApi api;
  late ContactsRepository contacts;
  late GroupRepository groupRepository;
  late GlobalSearchRepository repository;
  late GlobalSearchController controller;
  DateTime wall = DateTime.fromMillisecondsSinceEpoch(1700000000000);
  bool started = false;
  GoRouter? router;
  final cleanups = <void Function()>[];
  bool closed = false;
  Completer<T> defer<T>(T fallback) {
    final value = Completer<T>();
    cleanups.add(() { if (!value.isCompleted) value.complete(fallback); });
    return value;
  }

  Future<void> start() async {
    if (started) return;
    started = true;
    sdk.conversations.add(SdkHarness.conversation(id: cidA, title: 'inbox first'));
    await sdk.start();
    final access = SessionAccess(sdk.session);
    api = TenantApi(access, dio: Dio()..httpClientAdapter = TestHttpAdapter((request) async {
      final data = request.data as Map;
      final id = ((data['userIDs'] as List?)?.firstOrNull ?? 'bob') as String;
      return jsonResponse(jsonEncode({'errCode': 0, 'data': {'users': [
        {'userID': id, 'nickname': '资料已补全$id', 'allowAddFriend': 1},
      ]}}));
    }));
    contacts = ContactsRepository(sdk.gateway, access, api);
    groupRepository = GroupRepository(sdk.gateway, access);
    repository = GlobalSearchRepository(sdk.gateway, access, contacts, now: () => wall);
    controller = GlobalSearchController(repository);
    sdk.overrideCall = (call) async {
      final custom = pending?.call(call);
      return custom == null ? respond(call) : await custom;
    };
  }

  dynamic respond(MethodCall call) {
    final args = call.arguments as Map;
    switch (call.method) {
      case 'getFriendList': return jsonEncode(friends);
      case 'getJoinedGroupList': return jsonEncode(groups);
      case 'getUsersInfo':
        return jsonEncode([for (final id in args['userIDList'] as List) {
          'publicInfo': {'userID': id, 'nickname': 'SDK$id'},
          'friendInfo': {'userID': id, 'nickname': 'SDK$id'},
        }]);
      case 'getGroupsInfo':
        return jsonEncode(groups.where((g) => (args['groupIDList'] as List).contains(g['groupID'])).toList());
      case 'isJoinGroup': return 'true';
      case 'getGroupMembersInfo':
        return jsonEncode([for (final id in args['userIDList'] as List) {
          'groupID': args['groupID'], 'userID': id, 'nickname': id,
          'roleLevel': id == 'alice' ? 100 : 20,
        }]);
      case 'getMultipleConversation':
        return jsonEncode([for (final id in args['conversationIDList'] as List)
          SdkHarness.conversation(id: id as String, title: '同名会话')]);
      case 'searchLocalMessages':
        final filter = args['filter'] as Map;
        final types = (filter['messageTypeList'] as List).cast<int>();
        final word = (filter['keywordList'] as List).single as String;
        return wireGroups({for (final entry in messages.entries) entry.key: entry.value.where((m) {
          final name = (m['fileElem'] as Map?)?['fileName'];
          final text = (m['textElem'] as Map?)?['content'];
          return types.contains(m['contentType']) && '${name ?? text ?? ''}'.contains(word);
        }).toList()});
      case 'findMessageList':
        final searches = args['searchParams'] as List;
        return jsonEncode({'findResultItems': [for (final raw in searches) {
          'conversationID': (raw as Map)['conversationID'],
          'messageList': (messages[raw['conversationID']] ?? [])
              .where((m) => (raw['clientMsgIDList'] as List).contains(m['clientMsgID'])).toList(),
        }]});
      default: return sdk.respond(call);
    }
  }

  int calls(String name) => sdk.calls.where((c) => c.method == name).length;
  Future<void> rotateOwner() async {
    // Only used after observed native operations have settled, never to bypass
    // the existing SDK quarantine barrier.
    await sdk.session.signOut(revoke: false);
    await sdk.session.signIn((_) async => SessionCredentials(userId: sdk.userId,
      tenantId: 'tenant-b', tenantName: '另一个租户', issuer: 'https://route-b.example.com/',
      domains: ['b.example.com'], chatToken: 'fixture-b-chat', imToken: 'fixture-b-im'));
  }
  Future<void> mount(WidgetTester tester, {bool inbox = false, double width = 390,
      double height = 844, double scale = 1.5, Widget Function()? home}) async {
    debugPrint('global_fixture_mount startup.begin');
    await tester.runAsync(start);
    debugPrint('global_fixture_mount startup.end');
    tester.view.physicalSize = Size(width, height); tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize); addTearDown(tester.view.resetDevicePixelRatio);
    debugPrint('global_fixture_mount theme.begin');
    final theme = await captureTheme(tester);
    debugPrint('global_fixture_mount theme.end');
    router = GoRouter(routes: [
      GoRoute(path: '/', builder: (_, _) => home?.call() ??
          (inbox ? const ConversationsPage() : GlobalSearchPage(now: () => wall))),
      GoRoute(path: '/search', builder: (_, _) => GlobalSearchPage(now: () => wall)),
      GoRoute(path: '/contacts/user/:id', builder: (_, state) => UserProfilePage(userId: state.pathParameters['id']!)),
      GoRoute(path: '/contacts/groups/:id', builder: (_, state) => GroupPage(groupId: state.pathParameters['id']!)),
      // Navigation observation boundary. Actual ChatPage/Controller are tested
      // separately; this fixture does not claim their entire behavior passed.
      GoRoute(path: '/chat/:cid', builder: (_, state) {
        navigations.add((cid: state.pathParameters['cid']!, id: state.uri.queryParameters['message'],
          source: state.uri.queryParameters['source'], extra: state.extra));
        return Scaffold(appBar: AppBar(title: const Text('导航边界')), body: Text('${state.pathParameters['cid']}'));
      }),
    ]);
    debugPrint('global_fixture_mount frame.begin');
    await tester.pumpWidget(ProviderScope(overrides: [
      contactsRepositoryProvider.overrideWithValue(contacts),
      groupRepositoryProvider.overrideWithValue(groupRepository),
      imGatewayProvider.overrideWithValue(sdk.gateway),
      appRuntimeProvider.overrideWith((_) => sdk.runtime),
      tenantApiProvider.overrideWithValue(api),
    ], child: MaterialApp.router(routerConfig: router, theme: theme,
      builder: (context, child) => MediaQuery(data: MediaQuery.of(context)
          .copyWith(textScaler: TextScaler.linear(scale)), child: child!))));
    debugPrint('global_fixture_mount frame.end');
    debugPrint('global_fixture_mount settle.begin');
    await tester.pumpAndSettle();
    debugPrint('global_fixture_mount settle.end');
  }
  Future<void> searchUI(WidgetTester tester, [String word = 'match']) async {
    await tester.enterText(find.byType(TextField), word);
    await tester.tap(find.byTooltip('开始全局搜索'));
    await tester.pumpAndSettle();
  }
  Future<void> close([WidgetTester? tester]) async {
    if (closed) return;
    closed = true;
    void phase(String value) => debugPrint('global_fixture_cleanup $value');
    Future<void> release() async {
      phase('pending.begin');
      for (final finish in cleanups) { finish(); }
      await Future<void>.delayed(Duration.zero);
      phase('pending.end');
      phase('controller.begin');
      controller.dispose();
      phase('controller.end');
      phase('api.begin');
      api.close();
      phase('api.end');
      // Keep the provider-owned runtime mounted until the real SDK teardown has
      // settled. Unmount first would start its unawaited session.dispose in the
      // widget's fake zone, which a later runAsync cannot drive to completion.
      // These are the same public, idempotent steps used by SdkHarness.dispose;
      // the final harness call still owns channel removal and native-folder IO.
      phase('session.begin');
      await sdk.session.dispose().timeout(const Duration(seconds: 20));
      phase('session.end');
      phase('gateway.begin');
      await sdk.gateway.dispose().timeout(const Duration(seconds: 20));
      phase('gateway.end');
      phase('sdk-channel-native-folder.begin');
      await sdk.dispose().timeout(const Duration(seconds: 20));
      phase('sdk-channel-native-folder.end');
    }
    try {
      if (tester == null) {
        await release();
      } else {
        var completed = false;
        await tester.runAsync(() async {
          await release();
          completed = true;
        });
        // runAsync reports asynchronous exceptions to the framework and can
        // return null. Do not mistake that path for completed SDK teardown.
        if (!completed) throw StateError('Fixture release did not complete; see runAsync error');
      }
    } finally {
      if (tester != null) {
        phase('unmount.begin');
        await tester.pumpWidget(const SizedBox.shrink());
        phase('unmount.end');
      }
      phase('router.begin');
      router?.dispose();
      phase('router.end');
    }
  }
}
