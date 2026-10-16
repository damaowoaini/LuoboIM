import 'dart:async';
import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart' show debugPrintSynchronously;
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_openim_sdk/flutter_openim_sdk.dart' show MessageType;
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:imapp/app.dart' show routerProvider;
import 'package:imapp/core/app_runtime.dart' show AppPhase;
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/features/chat/presentation/chat_card_picker_page.dart';
import 'package:imapp/features/chat/presentation/chat_card_view.dart';
import 'package:imapp/features/chat/presentation/chat_controller.dart';
import 'package:imapp/features/chat/presentation/chat_media_actions.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';
import 'package:imapp/features/contacts/contacts_providers.dart';
import 'package:imapp/features/contacts/data/contacts_repository.dart';
import 'package:imapp/features/group/data/group_repository.dart';
import 'package:imapp/providers.dart';

import '../../core/im/capture_theme.dart';
import '../../core/im/sdk_harness.dart';
import '../../support/http_adapter.dart';
import 'location_send_fixture.dart';

final class ChatCardFixture {
  // Reuse the accepted SDK/session/real-zone teardown harness. Only its OS/SDK
  // transport and the existing HTTP adapter are mocked, never app controllers.
  final transport = LocationSendFixture();
  SdkHarness get sdk => transport.sdk;
  ChatController get controller => transport.controller;
  final friends = <Map<String, dynamic>>[
    for (var i = 0; i < 55; ++i)
      {
        'friendInfo': {
          'userID': 'friend-$i',
          'nickname': i < 2 ? '同名好友' : '好友$i',
          if (i == 54) 'remark': '私人备注54',
          'faceURL': 'https://never-load.invalid/avatar/$i',
        },
      },
  ];
  late TenantApi api;
  late ContactsRepository contacts;
  late GroupRepository groups;
  GoRouter? router;
  ProviderContainer? container;
  final httpCalls = <RequestOptions>[];
  int creates = 0, friendReads = 0, profileReads = 0;
  int conversations = 0, friendRequests = 0;
  int get sends => transport.sends;
  String? wrongProfile;
  bool failFriendRead = false, invalidCard = false;
  bool profileFriend = false,
      groupHideProfiles = false,
      groupPreventFriends = false;
  int groupRole = 20;
  bool groupJoined = true;
  List<Map<String, dynamic>>? groupResponse, groupSelfResponse;
  Map<String, dynamic> Function(Map<String, dynamic>)? conversationResponse;
  Object? friendPolicy = 1;
  bool _installed = false, _sourceReady = false;
  Future<void> Function()? beforeFriends, beforeCard, beforeProfile;
  Future<void> Function()? beforeBusiness, beforeConversation;
  Future<void> Function()? beforeGroups;
  Future<void> start({bool group = false}) async {
    await transport.start(group: group);
    install();
  }

  void install() {
    final prior = sdk.overrideCall;
    sdk.overrideCall = (call) async {
      final args = call.arguments as Map;
      switch (call.method) {
        case 'getFriendList':
          ++friendReads;
          await beforeFriends?.call();
          if (failFriendRead) {
            throw StateError('mock friend read failed');
          }
          return jsonEncode(friends);
        case 'createCardMessage':
          ++creates;
          await beforeCard?.call();
          final value = args['cardMessage'] as Map;
          return jsonEncode({
            ...SdkHarness.message(
              id: 'created-card-$creates',
              text: '',
              sender: 'alice',
              status: 1,
            ),
            'contentType': 108,
            'cardElem': {...value, if (invalidCard) 'userID': 'wrong-user'},
          });
        case 'getUsersInfo':
          ++profileReads;
          await beforeProfile?.call();
          final id = (args['userIDList'] as List).single as String;
          return jsonEncode([
            {
              profileFriend ? 'friendInfo' : 'publicInfo': {
                'userID': wrongProfile ?? id,
                'nickname': '权威资料',
              },
            },
          ]);
        case 'getGroupsInfo':
          await beforeGroups?.call();
          return jsonEncode(
            groupResponse ??
                [
                  {
                    'groupID': 'group-a',
                    'groupName': '原群',
                    'groupType': 2,
                    'ownerUserID': 'owner',
                    'memberCount': 3,
                    'lookMemberInfo': groupHideProfiles ? 1 : 0,
                    'applyMemberFriend': groupPreventFriends ? 1 : 0,
                    'status': 0,
                  },
                ],
          );
        case 'isJoinGroup':
          return '$groupJoined';
        case 'getGroupMembersInfo':
          return jsonEncode(
            groupSelfResponse ??
                [
                  {
                    'groupID': 'group-a',
                    'userID': 'alice',
                    'nickname': '本人',
                    'roleLevel': groupRole,
                  },
                ],
          );
        case 'addFriend':
          ++friendRequests;
          return null;
        case 'getOneConversation':
          ++conversations;
          await beforeConversation?.call();
          final id = args['sourceID'] as String;
          final value = {
            ...SdkHarness.conversation(id: 'single-alice-$id', title: '名片目标会话'),
            'userID': id,
          };
          if (!sdk.conversations.any(
            (v) => v['conversationID'] == value['conversationID'],
          )) {
            sdk.conversations.add(value);
          }
          return jsonEncode(conversationResponse?.call(value) ?? value);
      }
      return prior == null ? sdk.respond(call) : prior(call);
    };
    api = TenantApi(
      SessionAccess(sdk.session),
      dio: Dio()
        ..httpClientAdapter = TestHttpAdapter((request) async {
          httpCalls.add(request);
          await beforeBusiness?.call();
          final id = (request.data['userIDs'] as List).single as String;
          return jsonResponse(
            jsonEncode({
              'errCode': 0,
              'data': {
                'users': [
                  {
                    'userID': wrongProfile ?? id,
                    'nickname': '权威资料',
                    'allowAddFriend': friendPolicy,
                  },
                ],
              },
            }),
          );
        }),
    );
    contacts = ContactsRepository(sdk.gateway, api.access, api);
    groups = GroupRepository(sdk.gateway, api.access);
    _installed = true;
  }

  Future<void> startRepository({bool group = false}) async {
    await transport.startRepository(group: group);
    install();
  }

  Map<String, dynamic> wire({
    String id = 'received-card',
    String user = 'friend-54',
    String name = '来信名片',
    bool private = false,
  }) => {
    ...SdkHarness.message(id: id, text: ''),
    'contentType': 108,
    'cardElem': {
      'userID': user,
      'nickname': name,
      'faceURL': 'https://never-load.invalid/received?token=never',
      'ex': '{"notAnAction":true}',
    },
    if (private) 'attachedInfoElem': {'isPrivateChat': true},
  };
  Future<void> mount(
    WidgetTester tester, {
    bool group = false,
    bool received = false,
    bool merged = false,
    bool small = false,
    bool productionRouter = false,
    Map<String, dynamic>? receivedWire,
  }) async {
    await tester.runAsync(() async {
      await start(group: group);
      if (received || receivedWire != null) {
        sdk.messages.add(
          merged
              ? {
                  ...SdkHarness.message(id: 'root-card-merge', text: ''),
                  'contentType': MessageType.merger,
                  'mergeElem': {
                    'title': '聊天记录',
                    'abstractList': ['名片'],
                    'multiMessage': [receivedWire ?? wire()],
                  },
                }
              : receivedWire ?? wire(),
        );
      }
    });
    if (small) {
      tester.view.physicalSize = const Size(360, 640);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
    }
    transport.sourceCid = ValueNotifier(transport.cid);
    _sourceReady = true;
    final theme = await captureTheme(tester);
    if (productionRouter) sdk.runtime.phase = AppPhase.online;
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          appRuntimeProvider.overrideWith((_) => sdk.runtime),
          imGatewayProvider.overrideWithValue(sdk.gateway),
          tenantApiProvider.overrideWithValue(api),
          contactsRepositoryProvider.overrideWithValue(contacts),
          groupRepositoryProvider.overrideWithValue(groups),
          chatReadingStoreProvider.overrideWithValue(transport.base.reading),
          chatBackgroundStoreProvider.overrideWithValue(
            transport.base.backgrounds,
          ),
        ],
        child: productionRouter
            ? Consumer(
                builder: (context, ref, _) {
                  container = ProviderScope.containerOf(context, listen: false);
                  router = ref.watch(routerProvider);
                  return MaterialApp.router(theme: theme, routerConfig: router);
                },
              )
            : MaterialApp(
                navigatorKey: transport.base.navigator,
                theme: theme,
                builder: (context, child) => MediaQuery(
                  data: MediaQuery.of(context)
                      .copyWith(textScaler: TextScaler.linear(small ? 1.6 : 1)),
                  child: child!,
                ),
                home: ValueListenableBuilder<String>(
                  valueListenable: transport.sourceCid,
                  builder: (_, cid, _) => ChatPage(conversationId: cid),
                ),
              ),
      ),
    );
    if (productionRouter) {
      await transport.base.frame(tester);
      router!.go('/chat/${transport.cid}');
      await transport.base.frame(tester);
    }
    await readyAttachment(tester);
    container ??= ProviderScope.containerOf(
      tester.element(find.byType(ChatPage)),
      listen: false,
    );
    transport.controller = tester
        .widget<ChatMediaActions>(find.byType(ChatMediaActions))
        .controller;
  }

  Future<Finder> readyAttachment(WidgetTester tester) async {
    final button = find.descendant(
      of: find.byType(ChatMediaActions),
      matching: find.byType(IconButton),
    );
    await transport.base.until(
      tester,
      () =>
          button.evaluate().length == 1 &&
          tester.widget<IconButton>(button).onPressed != null &&
          button.hitTestable().evaluate().length == 1,
    );
    await transport.base.ready(tester, button);
    return button;
  }

  Future<void> open(WidgetTester tester) async {
    await tester.tap(await readyAttachment(tester));
    await transport.base.until(
      tester,
      () => find.text('好友名片').evaluate().length == 1,
    );
    await tester.ensureVisible(find.text('好友名片'));
    await transport.base.ready(tester, find.text('好友名片'));
    await tester.tap(find.text('好友名片').hitTestable());
    await transport.base.ready(tester, find.byType(ChatCardPickerPage));
    await transport.base.until(
      tester,
      () =>
          find
              .descendant(
                of: find.byType(ChatCardPickerPage),
                matching: find.byType(ListTile),
              )
              .evaluate()
              .isNotEmpty ||
          find.text('好友列表未能加载').evaluate().isNotEmpty ||
          find.text('暂无好友可分享').evaluate().isNotEmpty,
    );
  }

  Future<void> choose(WidgetTester tester, {String id = 'friend-0'}) async {
    await tester.enterText(find.byType(TextField).hitTestable(), id);
    await transport.base.frame(tester);
    final row = find.byKey(ValueKey(('share-card-friend', id)));
    await transport.base.ready(tester, row);
    await tester.tap(row.hitTestable());
    await transport.base.ready(tester, find.text('发送好友名片？'));
  }

  Finder get confirm => find.widgetWithText(FilledButton, '发送');
  Future<void> send(WidgetTester tester) async {
    await transport.base.ready(tester, confirm);
    await tester.tap(confirm.hitTestable());
  }

  Future<void> openReceived(WidgetTester tester) async {
    final card = find.byType(ChatCardView);
    await transport.base.ready(tester, card);
    await tester.tap(card.hitTestable());
    await transport.base.ready(tester, find.byType(ChatCardDetail));
  }

  Future<void> openMerged(WidgetTester tester) async {
    final entry = find.textContaining('聊天记录 ·');
    await transport.base.ready(tester, entry);
    await tester.tap(entry.hitTestable());
    await transport.base.ready(tester, find.byType(ChatCardView));
  }

  Future<void> popOwned(WidgetTester tester, Finder finder) async {
    final route = ModalRoute.of(tester.element(finder))!;
    route.navigator!.pop();
    var completed = false;
    unawaited(route.completed.then<void>((_) => completed = true));
    await transport.base.until(tester, () => completed);
    await transport.base.frame(tester);
  }

  Future<void> close(WidgetTester tester) async {
    if (_installed) api.close();
    for (final release in transport.releases) {
      release();
    }
    try {
      debugPrintSynchronously('chat_card_cleanup sdk.begin');
      if (transport.base.started) {
        await transport.base.actual(
          tester,
          () => sdk.dispose().timeout(const Duration(seconds: 20)),
        );
      }
      debugPrintSynchronously('chat_card_cleanup sdk.end');
    } finally {
      debugPrintSynchronously('chat_card_cleanup unmount.begin');
      await tester.pumpWidget(const SizedBox.shrink());
      await tester.pumpAndSettle();
      transport.base.reading.dispose();
      transport.base.backgrounds.dispose();
      if (_sourceReady) transport.sourceCid.dispose();
      debugPrintSynchronously('chat_card_cleanup unmount.end');
    }
  }

  Future<void> closeRepository() async {
    if (_installed) api.close();
    for (final release in transport.releases) {
      release();
    }
    debugPrintSynchronously('chat_card_repository_cleanup sdk.begin');
    try {
      controller.dispose();
      await sdk.dispose().timeout(const Duration(seconds: 20));
      debugPrintSynchronously('chat_card_repository_cleanup sdk.end');
    } finally {
      debugPrintSynchronously('chat_card_repository_cleanup storage.begin');
      transport.base.reading.dispose();
      transport.base.backgrounds.dispose();
      debugPrintSynchronously('chat_card_repository_cleanup storage.end');
    }
  }
}
