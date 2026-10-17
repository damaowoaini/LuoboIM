import 'dart:async';

import 'package:flutter/foundation.dart' show debugPrintSynchronously;
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/im/im_event.dart';

import 'chat_input_fixture.dart';

void main() {
  testWidgets(
    'real focused composer sends bounded yes and throttles further input without sending a message',
    (tester) async {
      final f = ChatInputFixture();
      try {
        await f.mount(tester);
        await tester.tap(f.editor);
        await tester.enterText(f.editor, 'a');
        await f.revoke.base.until(tester, () => f.typing.length == 1);
        expect(f.typing.single['userID'], 'bob');
        expect(f.typing.single['msgTip'], 'yes');
        await tester.enterText(f.editor, 'ab');
        await tester.pump(const Duration(seconds: 1));
        expect(f.typing.length, 1);
        await tester.pump(const Duration(milliseconds: 1100));
        await tester.enterText(f.editor, 'abc');
        await f.revoke.base.until(tester, () => f.typing.length == 2);
        expect(f.typing.last['msgTip'], 'yes');
        expect(
          f.revoke.sdk.calls.where(
            (c) => c.method == 'createTextMessage' || c.method == 'sendMessage',
          ),
          isEmpty,
        );
        expect(f.controller(tester).messages.length, 1);
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'input idle and focus loss stop typing through no without replaying a yes',
    (tester) async {
      final f = ChatInputFixture();
      try {
        await f.mount(tester);
        await tester.tap(f.editor);
        await tester.enterText(f.editor, 'hello');
        await f.revoke.base.until(tester, () => f.typing.length == 1);
        await tester.pump(const Duration(milliseconds: 3100));
        await f.revoke.base.until(tester, () => f.typing.length == 2);
        expect(f.typing.last['msgTip'], 'no');
        await tester.enterText(f.editor, 'next');
        await f.revoke.base.until(tester, () => f.typing.length == 3);
        tester.widget<TextField>(f.editor).focusNode!.unfocus();
        await tester.pump();
        await f.revoke.base.until(tester, () => f.typing.length == 4);
        expect(f.typing.last['msgTip'], 'no');
        await tester.pump(const Duration(seconds: 4));
        expect(f.typing.length, 4);
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'live exact-peer typing updates the real header and each yes renews only a short TTL',
    (tester) async {
      final f = ChatInputFixture();
      try {
        await f.mount(tester);
        final history = f.revoke.sdk.calls
            .where((c) => c.method == 'getAdvancedHistoryMessageList')
            .length;
        await f.peer(tester);
        await tester.pump();
        expect(find.text('对方正在输入…'), findsOneWidget);
        await tester.pump(const Duration(seconds: 2));
        await f.peer(tester);
        await tester.pump(const Duration(seconds: 2));
        expect(find.text('对方正在输入…'), findsOneWidget);
        await tester.pump(const Duration(milliseconds: 1100));
        expect(find.text('对方正在输入…'), findsNothing);
        expect(f.controller(tester).messages.length, 1);
        expect(
          f.revoke.sdk.calls
              .where((c) => c.method == 'getAdvancedHistoryMessageList')
              .length,
          history,
        );
        expect(
          f.revoke.sdk.gateway.privacyLedger
              .lookup('ephemeral-typing')
              .inspectionComplete,
          isFalse,
        );
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'wrong-peer wrong-recipient malformed and offline typing never enters history or the active header',
    (tester) async {
      final f = ChatInputFixture();
      f.revoke.sdk.traceDisposal = true;
      StreamSubscription<ImEvent>? events;
      final received = <ImEvent>[];
      try {
        await f.mount(tester);
        debugPrintSynchronously('chat_input_typing_negative mounted');
        events = f.revoke.sdk.gateway.events.listen(received.add);
        await f.peer(tester, sender: 'mallory');
        await f.peer(tester, recipient: 'another-owner');
        await f.peer(tester, sender: 'bad\nuser');
        await f.peer(tester, tip: 'not-a-protocol-state');
        await f.peer(tester, session: 3);
        await f.peer(tester, method: 'onRecvOfflineNewMessage');
        debugPrintSynchronously('chat_input_typing_negative incoming.complete');
        await tester.pump();
        expect(find.text('对方正在输入…'), findsNothing);
        expect(
          received.where(
            (e) =>
                e.kind == ImEventKind.messageReceived ||
                e.kind == ImEventKind.offlineMessageReceived ||
                e.kind == ImEventKind.transientMessageReceived,
          ),
          isEmpty,
        );
        expect(
          f.revoke.sdk.gateway.privacyLedger
              .lookup('ephemeral-typing')
              .inspectionComplete,
          isFalse,
        );
        expect(f.controller(tester).messages.length, 1);
        debugPrintSynchronously('chat_input_typing_negative body.checked');
      } finally {
        debugPrintSynchronously(
          'chat_input_typing_negative events.cancel.begin',
        );
        await events?.cancel();
        debugPrintSynchronously('chat_input_typing_negative events.cancel.end');
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'online-only typing uses the same transient yes-no path while a group composer sends none',
    (tester) async {
      final f = ChatInputFixture();
      try {
        await f.mount(tester, group: true);
        await tester.tap(f.editor);
        await tester.enterText(f.editor, 'group input');
        await f.revoke.base.frame(tester);
        await f.peer(tester, method: 'onRecvOnlineOnlyMessage');
        await tester.pump();
        expect(f.typing, isEmpty);
        expect(find.text('对方正在输入…'), findsNothing);
        expect(f.controller(tester).messages.length, 1);
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'live online-only no clears the header without an ordinary receive refresh',
    (tester) async {
      final f = ChatInputFixture();
      try {
        await f.mount(tester);
        await f.peer(tester, method: 'onRecvOnlineOnlyMessage');
        await tester.pump();
        expect(find.text('对方正在输入…'), findsOneWidget);
        await f.peer(tester, tip: 'no', method: 'onRecvOnlineOnlyMessage');
        await tester.pump();
        expect(find.text('对方正在输入…'), findsNothing);
        expect(f.controller(tester).messages.length, 1);
        expect(f.typing, isEmpty);
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'background and foreign coverage discard typing state until a new foreground event arrives',
    (tester) async {
      final f = ChatInputFixture();
      try {
        await f.mount(tester);
        await f.peer(tester);
        await tester.pump();
        expect(find.text('对方正在输入…'), findsOneWidget);
        tester.binding.handleAppLifecycleStateChanged(
          AppLifecycleState.inactive,
        );
        await tester.pump();
        await f.peer(tester);
        expect(find.text('对方正在输入…'), findsNothing);
        tester.binding.handleAppLifecycleStateChanged(
          AppLifecycleState.resumed,
        );
        await tester.pump();
        expect(find.text('对方正在输入…'), findsNothing);
        await f.peer(tester);
        await tester.pump();
        expect(find.text('对方正在输入…'), findsOneWidget);
        final foreign = MaterialPageRoute<void>(
          builder: (_) => const Scaffold(body: Text('独立页面')),
        );
        unawaited(f.revoke.base.navigator.currentState!.push(foreign));
        await f.revoke.base.ready(tester, find.text('独立页面'));
        await f.peer(tester);
        f.revoke.base.navigator.currentState!.pop();
        await f.revoke.base.actual(tester, () => foreign.completed);
        expect(find.text('对方正在输入…'), findsNothing);
        await f.peer(tester);
        await tester.pump();
        expect(find.text('对方正在输入…'), findsOneWidget);
      } finally {
        tester.binding.handleAppLifecycleStateChanged(
          AppLifecycleState.resumed,
        );
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'real back navigation cancels outgoing timers and a fresh chat does not resurrect old typing',
    (tester) async {
      final f = ChatInputFixture();
      try {
        await f.mount(tester);
        await tester.tap(f.editor);
        await tester.enterText(f.editor, 'draft');
        await f.revoke.base.until(tester, () => f.typing.length == 1);
        await f.peer(tester);
        await tester.pump();
        final back = find.byType(BackButton);
        await f.revoke.base.ready(tester, back);
        await tester.tap(back.hitTestable());
        await f.revoke.base.ready(tester, find.text('会话列表'));
        final settled = f.typing.length;
        await tester.pump(const Duration(seconds: 4));
        await f.revoke.base.frame(tester);
        expect(f.typing.length, settled);
        unawaited(f.router.push('/chat'));
        await f.revoke.base.ready(tester, f.editor);
        expect(find.text('对方正在输入…'), findsNothing);
        expect(
          f.revoke.sdk.calls.where((c) => c.method == 'sendMessage'),
          isEmpty,
        );
      } finally {
        await f.close(tester);
      }
    },
  );
}
