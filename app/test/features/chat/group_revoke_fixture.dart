import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart' show debugPrintSynchronously;
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';
import 'package:imapp/features/chat/presentation/chat_controller.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';
import 'package:imapp/providers.dart';

import '../../core/im/capture_theme.dart';
import '../../core/im/sdk_harness.dart';
import 'chat_location_fixture.dart';

/// Only the existing OS/SDK codec boundary is mocked. All permission, source,
/// confirmation and dispatch code under test is the production implementation.
final class GroupRevokeFixture {
  final base = ChatLocationFixture();
  SdkHarness get sdk => base.sdk;
  ChatController? _controller;
  ChatController get controller => _controller!;
  ChatRepository get repository => controller.repository;
  late String cid;
  late Map<String, dynamic> raw, metadata;
  final sourceCid = ValueNotifier<String>('group-conversation-a');
  bool sourceValid = true,
      joined = true,
      dismissed = false,
      roleFailure = false;
  bool nativeFailure = false,
      committedBeforeFailure = false,
      actorMuted = false;
  int actorRole = 60, senderRole = 20, revokes = 0, roleReads = 0, finds = 0;
  int announcementSelfReads = 0, ambiguousSelfReads = 0;
  final allRoleReadRequests = <Map<String, Object?>>[];
  late String _roleActor, _roleSender, _roleGroup;

  void _recordRoleRead(Map args) {
    final ids = args['userIDList'];
    allRoleReadRequests.add({
      'groupID': args['groupID'],
      'userIDList': ids is List ? List<Object?>.unmodifiable(ids) : ids,
    });
    final exactSelf =
        _roleGroup.isNotEmpty &&
        args['groupID'] == _roleGroup &&
        ids is List &&
        ids.length == 1 &&
        ids.single == _roleActor;
    if (exactSelf && _roleSender != _roleActor) {
      // The announcement profile reads only this actor. A non-self revoke
      // requires both original identities, so this request is unambiguous.
      ++announcementSelfReads;
    } else {
      // Keep every duplicate, malformed or other request in the original
      // counter. A self-group single-ID read is ambiguous and is NOT excluded.
      ++roleReads;
      if (exactSelf && _roleSender == _roleActor) {
        ++ambiguousSelfReads;
      }
    }
  }

  List<Map<String, dynamic>>? members, findRows, conversationRows;
  String? findCid;
  Future<void> Function()? beforeRoles,
      beforeFind,
      beforeNative,
      beforeMetadata;
  final _releases = <VoidCallback>[];

  Completer<void> defer() {
    final result = Completer<void>();
    _releases.add(() {
      if (!result.isCompleted) result.complete();
    });
    return result;
  }

  Future<void> start({
    bool group = true,
    bool self = false,
    int sessionType = 3,
    bool withController = true,
    bool private = false,
    bool call = false,
  }) async {
    cid = group ? 'group-conversation-a' : ChatLocationFixture.cid;
    sourceCid.value = cid;
    raw = {
      ...SdkHarness.message(
        id: 'moderated-source',
        text: '待核对的原消息',
        sender: self ? 'alice' : 'bob',
      ),
      'sessionType': group ? sessionType : 1,
      'groupID': group ? 'group-a' : '',
      if (call) ...{
        'contentType': MessageType.custom,
        'customElem': {
          'data': jsonEncode({
            'customType': 901,
            'data': {'type': 'voice', 'state': 'hangup'},
          }),
        },
      },
      if (private)
        'attachedInfoElem': {'isPrivateChat': true, 'burnDuration': 1},
    };
    _roleActor = sdk.userId;
    _roleSender = raw['sendID'] as String;
    _roleGroup = raw['groupID'] as String;
    metadata = {
      ...SdkHarness.conversation(id: cid, title: '原群会话', group: group),
      'conversationType': group ? sessionType : 1,
      'latestMsg': raw,
    };
    sdk.conversations.add(metadata);
    sdk.messages.add(raw);
    await sdk.start();
    base.started = true;
    sdk.overrideCall = (call) async {
      final args = call.arguments as Map;
      switch (call.method) {
        case 'getMultipleConversation':
          await beforeMetadata?.call();
          return jsonEncode(
            conversationRows ??
                sdk.conversations
                    .where(
                      (row) => (args['conversationIDList'] as List).contains(
                        row['conversationID'],
                      ),
                    )
                    .toList(),
          );
        case 'findMessageList':
          ++finds;
          await beforeFind?.call();
          final params = (args['searchParams'] as List).single as Map;
          return jsonEncode({
            'findResultItems': [
              {
                'conversationID': findCid ?? cid,
                'messageList':
                    findRows ??
                    sdk.messages
                        .where(
                          (row) => (params['clientMsgIDList'] as List).contains(
                            row['clientMsgID'],
                          ),
                        )
                        .toList(),
              },
            ],
          });
        case 'getGroupsInfo':
          return jsonEncode([
            {'groupID': 'group-a', 'status': dismissed ? 2 : 0},
          ]);
        case 'isJoinGroup':
          return '$joined';
        case 'getGroupMembersInfo':
          _recordRoleRead(args);
          await beforeRoles?.call();
          if (roleFailure) throw PlatformException(code: '1002');
          return jsonEncode(
            members ??
                [
                  for (final id in (args['userIDList'] as List))
                    {
                      'groupID': 'group-a',
                      'userID': id,
                      'roleLevel': id == 'alice' ? actorRole : senderRole,
                      'muteEndTime': id == 'alice' && actorMuted
                          ? 1700000300000
                          : 0,
                    },
                ],
          );
        case 'revokeMessage':
          ++revokes;
          await beforeNative?.call();
          if (!nativeFailure || committedBeforeFailure) {
            sdk.messages.removeWhere(
              (row) => row['clientMsgID'] == args['clientMsgID'],
            );
          }
          if (nativeFailure) throw PlatformException(code: '1002');
          return null;
        default:
          return sdk.respond(call);
      }
    };
    if (withController) {
      _controller = ChatController(
        ChatRepository(
          gateway: sdk.gateway,
          session: sdk.session,
          conversationId: cid,
          now: () => base.clock,
        ),
      );
      await controller.start();
    }
  }

  ChatRevokeIntent capture() => controller.captureRevoke(
    controller.messages.single,
    access: sdk.gateway.historyAccess(
      repository.generation,
      conversationId: cid,
    ),
    isCurrent: () => sourceValid,
  );

  Future<void> mount(
    WidgetTester tester, {
    bool group = true,
    bool self = false,
    bool small = false,
    bool private = false,
  }) async {
    await tester.runAsync(
      () => start(
        group: group,
        self: self,
        withController: false,
        private: private,
      ),
    );
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
          appRuntimeProvider.overrideWith((_) => sdk.runtime),
          imGatewayProvider.overrideWithValue(sdk.gateway),
          chatReadingStoreProvider.overrideWithValue(base.reading),
          chatBackgroundStoreProvider.overrideWithValue(base.backgrounds),
        ],
        child: MaterialApp(
          navigatorKey: base.navigator,
          theme: theme,
          builder: (context, child) => MediaQuery(
            data: MediaQuery.of(context)
                .copyWith(textScaler: TextScaler.linear(small ? 1.6 : 1)),
            child: child!,
          ),
          home: ValueListenableBuilder<String>(
            valueListenable: sourceCid,
            builder: (_, cid, _) =>
                ChatPage(conversationId: cid, now: () => base.clock),
          ),
        ),
      ),
    );
    await base.ready(tester, find.text('待核对的原消息'));
  }

  Future<void> open(WidgetTester tester, {bool self = false}) async {
    final row = find.text('待核对的原消息');
    await base.ready(tester, row);
    await tester.longPress(row.hitTestable());
    final action = find.widgetWithText(ListTile, self ? '撤回消息' : '群管理撤回');
    await base.ready(tester, action);
    final menu = ModalRoute.of(tester.element(action))!;
    await tester.tap(action.hitTestable());
    await base.actual(tester, () => menu.completed);
    await base.ready(tester, find.text('撤回这条消息？'));
  }

  Finder get confirm => find.widgetWithText(TextButton, '确认撤回');
  Future<void> readyConfirm(WidgetTester tester) async {
    await base.until(
      tester,
      () =>
          confirm.evaluate().length == 1 &&
          confirm.hitTestable().evaluate().length == 1 &&
          tester.widget<TextButton>(confirm).onPressed != null,
    );
  }

  Future<void> closeDialog(WidgetTester tester) async {
    final button = find.widgetWithText(TextButton, '关闭');
    await base.ready(tester, button);
    final route = ModalRoute.of(tester.element(button))!;
    await tester.tap(button.hitTestable());
    await base.actual(tester, () => route.completed);
  }

  Future<void> memberEvent() => sdk.callback(ListenerType.groupListener, {
    'type': 'onGroupMemberInfoChanged',
    'data': jsonEncode({
      'groupID': 'group-a',
      'userID': 'alice',
      'roleLevel': actorRole,
    }),
  });

  Future<void> revokedEvent() =>
      sdk.callback(ListenerType.advancedMsgListener, {
        'type': 'onNewRecvMessageRevoked',
        'data': {
          'messageRevoked': jsonEncode({'clientMsgID': 'moderated-source'}),
        },
      });

  void release() {
    for (final action in _releases) {
      action();
    }
  }

  Future<void> closeRepository() async {
    release();
    debugPrintSynchronously('group_revoke_repository_cleanup sdk.begin');
    try {
      _controller?.dispose();
      if (base.started) {
        await sdk.dispose().timeout(const Duration(seconds: 20));
      }
      debugPrintSynchronously('group_revoke_repository_cleanup sdk.end');
    } finally {
      debugPrintSynchronously('group_revoke_repository_cleanup storage.begin');
      sourceCid.dispose();
      base.reading.dispose();
      base.backgrounds.dispose();
      debugPrintSynchronously('group_revoke_repository_cleanup storage.end');
    }
  }

  Future<void> close(WidgetTester tester) async {
    release();
    debugPrintSynchronously('group_revoke_cleanup sdk.begin');
    try {
      Object? disposeError;
      StackTrace? disposeStack;
      await tester.runAsync(() async {
        try {
          if (base.started) {
            await sdk.dispose().timeout(const Duration(seconds: 20));
            if (base.traceActual) {
              debugPrintSynchronously('chat_input_actual sdk.wrapper.end');
            }
          }
        } catch (error, stack) {
          // runAsync reports uncaught errors instead of forwarding them. Keep
          // the actual failure and stack for the caller outside its real zone.
          disposeError = error;
          disposeStack = stack;
        }
      });
      if (disposeError != null) {
        Error.throwWithStackTrace(disposeError!, disposeStack!);
      }
      debugPrintSynchronously('group_revoke_cleanup sdk.end');
    } finally {
      debugPrintSynchronously('group_revoke_cleanup unmount.begin');
      if (base.traceActual) {
        debugPrintSynchronously('chat_input_unmount pumpWidget.before');
      }
      await tester.pumpWidget(const SizedBox.shrink());
      if (base.traceActual) {
        debugPrintSynchronously('chat_input_unmount pumpWidget.after');
        debugPrintSynchronously('chat_input_unmount pumpAndSettle.before');
      }
      await tester.pumpAndSettle();
      if (base.traceActual) {
        debugPrintSynchronously('chat_input_unmount pumpAndSettle.after');
      }
      sourceCid.dispose();
      base.reading.dispose();
      base.backgrounds.dispose();
      debugPrintSynchronously('group_revoke_cleanup unmount.end');
    }
  }
}
