import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:imapp/features/chat/presentation/chat_media_actions.dart';
import 'package:imapp/features/chat/presentation/chat_controller.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';
import 'package:imapp/providers.dart';

import '../../core/im/capture_theme.dart';
import 'group_revoke_fixture.dart';

/// Existing codec fixture, production composer/owned confirmation/GoRouter.
/// No controller, permission, route or send implementation is replaced.
final class ChatInputFixture {
  final revoke = GroupRevokeFixture();
  final typing = <Map<String, dynamic>>[];
  late GoRouter router;
  Future<void> Function()? beforeTyping;
  bool _at = false;
  String rowText = '待核对的原消息';
  Finder get editor => find.byType(TextField);
  Finder get restore => find.widgetWithText(TextButton, '重新编辑');
  ChatController controller(WidgetTester tester) => tester
      .widget<ChatMediaActions>(
        find.byType(ChatMediaActions, skipOffstage: false),
      )
      .controller;

  Future<void> mount(
    WidgetTester tester, {
    bool group = false,
    bool self = true,
    bool private = false,
    bool at = false,
    bool malformed = false,
    bool small = false,
    String draft = '',
  }) async {
    await tester.runAsync(
      () => revoke.start(
        group: group,
        self: self,
        private: private,
        withController: false,
      ),
    );
    revoke.metadata['draftText'] = draft;
    _at = at;
    if (at) {
      rowText = 'Hello @鲍勃 ';
      revoke.raw.remove('textElem');
      revoke.raw.addAll({
        'contentType': MessageType.atText,
        'atTextElem': {
          'text': 'Hello @bob ',
          'atUserList': ['bob'],
          'atUsersInfo': [
            {'atUserID': 'bob', 'groupNickname': '鲍勃'},
          ],
        },
      });
    }
    if (malformed) revoke.raw['attachedInfo'] = '[false]';
    final previous = revoke.sdk.overrideCall!;
    revoke.sdk.overrideCall = (call) async {
      if (call.method == 'typingStatusUpdate') {
        typing.add(Map<String, dynamic>.from(call.arguments as Map));
        await beforeTyping?.call();
        return null;
      }
      return previous(call);
    };
    if (small) {
      tester.view.physicalSize = const Size(360, 640);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
    }
    final theme = await captureTheme(tester);
    router = GoRouter(
      navigatorKey: revoke.base.navigator,
      routes: [
        GoRoute(
          path: '/',
          builder: (_, _) => const Scaffold(body: Text('会话列表')),
        ),
        GoRoute(
          path: '/chat',
          builder: (_, _) => ValueListenableBuilder<String>(
            valueListenable: revoke.sourceCid,
            builder: (_, cid, _) =>
                ChatPage(conversationId: cid, now: () => revoke.base.clock),
          ),
        ),
      ],
    );
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          appRuntimeProvider.overrideWith((_) => revoke.sdk.runtime),
          imGatewayProvider.overrideWithValue(revoke.sdk.gateway),
          chatReadingStoreProvider.overrideWithValue(revoke.base.reading),
          chatBackgroundStoreProvider.overrideWithValue(
            revoke.base.backgrounds,
          ),
        ],
        child: MaterialApp.router(
          routerConfig: router,
          theme: theme,
          builder: (context, child) => MediaQuery(
            data: MediaQuery.of(context)
                .copyWith(textScaler: TextScaler.linear(small ? 1.6 : 1)),
            child: child!,
          ),
        ),
      ),
    );
    await tester.pump();
    unawaited(router.push('/chat'));
    await revoke.base.until(
      tester,
      () =>
          editor.evaluate().length == 1 &&
          tester.widget<TextField>(editor).enabled == true,
    );
    await revoke.base.ready(tester, editor);
  }

  Future<void> openRevoke(WidgetTester tester, {bool self = true}) async {
    final row = _at
        ? find.byWidgetPredicate(
            (widget) =>
                widget is Text &&
                widget.textSpan?.toPlainText(includeSemanticsLabels: false) ==
                    rowText,
          )
        : find.text(rowText, findRichText: true);
    await revoke.base.ready(tester, row);
    await tester.longPress(row.hitTestable());
    final menu = find.widgetWithText(ListTile, self ? '撤回消息' : '群管理撤回');
    await revoke.base.ready(tester, menu);
    final route = ModalRoute.of(tester.element(menu))!;
    await tester.tap(menu.hitTestable());
    await revoke.base.actual(tester, () => route.completed);
    await revoke.readyConfirm(tester);
  }

  Future<void> acknowledge(WidgetTester tester) async {
    await tester.tap(revoke.confirm.hitTestable());
    await revoke.base.until(
      tester,
      () => revoke.revokes == 1 && find.byType(AlertDialog).evaluate().isEmpty,
    );
    await revoke.base.frame(tester);
  }

  Future<void> peer(
    WidgetTester tester, {
    String sender = 'bob',
    String recipient = 'alice',
    String tip = 'yes',
    int session = 1,
    String method = 'onRecvNewMessage',
  }) => revoke.base.actual(
    tester,
    () => revoke.sdk.callback(ListenerType.advancedMsgListener, {
      'type': method,
      'data': {
        'message': jsonEncode({
          'clientMsgID': 'ephemeral-typing',
          'sendID': sender,
          'recvID': recipient,
          'sessionType': session,
          'contentType': MessageType.typing,
          'typingElem': {'msgTips': tip},
        }),
      },
    }),
  );
  Future<void> deleteNotice(WidgetTester tester) => revoke.base.actual(
    tester,
    () => revoke.sdk.callback(ListenerType.advancedMsgListener, {
      'type': 'onMsgDeleted',
      'data': {'message': jsonEncode(revoke.raw)},
    }),
  );
  Future<void> close(WidgetTester tester) async {
    try {
      await revoke.close(tester);
    } finally {
      router.dispose();
    }
  }
}
