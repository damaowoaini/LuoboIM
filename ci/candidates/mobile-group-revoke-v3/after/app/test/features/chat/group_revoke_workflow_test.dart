import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/presentation/chat_media_actions.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';

import 'group_revoke_fixture.dart';

void main() {
  testWidgets(
    'actual long press group moderation inspects roles then confirms one standard revoke',
    (tester) async {
      final f = GroupRevokeFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.readyConfirm(tester);
        expect(f.roleReads, 1);
        expect(f.revokes, 0);
        await tester.tap(f.confirm.hitTestable());
        await f.base.until(
          tester,
          () => find.text('撤回这条消息？').evaluate().isEmpty,
        );
        expect(f.revokes, 1);
        expect(f.roleReads, 2);
        expect(find.text('待核对的原消息'), findsNothing);
        expect(find.byType(ChatPage), findsOneWidget);
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'actual self message menu keeps ordinary single chat revoke usable',
    (tester) async {
      final f = GroupRevokeFixture();
      try {
        await f.mount(tester, group: false, self: true);
        await f.open(tester, self: true);
        await f.readyConfirm(tester);
        await tester.tap(f.confirm.hitTestable());
        await f.base.until(
          tester,
          () => f.revokes == 1 && find.byType(AlertDialog).evaluate().isEmpty,
        );
        expect(f.roleReads, 0);
        expect(find.text('待核对的原消息'), findsNothing);
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'muted admin moderation is usable at360 large text with real44px confirmation controls',
    (tester) async {
      final f = GroupRevokeFixture()..actorMuted = true;
      try {
        await f.mount(tester, small: true);
        await f.open(tester);
        await f.readyConfirm(tester);
        for (final button in [
          f.confirm,
          find.widgetWithText(TextButton, '关闭'),
        ]) {
          expect(button.hitTestable(), findsOneWidget);
          expect(tester.getSize(button).height, greaterThanOrEqualTo(44));
          expect(tester.getSize(button).width, greaterThanOrEqualTo(44));
        }
        await tester.tap(f.confirm.hitTestable());
        await f.base.until(
          tester,
          () => f.revokes == 1 && find.byType(AlertDialog).evaluate().isEmpty,
        );
        expect(tester.takeException(), isNull);
        expect(f.revokes, 1);
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'actual role read failure stays an error and explicit retry remains read only until confirmation',
    (tester) async {
      final f = GroupRevokeFixture()..roleFailure = true;
      try {
        await f.mount(tester);
        await f.open(tester);
        final retry = find.widgetWithText(TextButton, '重新核对');
        await f.base.ready(tester, retry);
        expect(f.revokes, 0);
        f.roleFailure = false;
        final entered = Completer<void>(), release = f.defer();
        f.beforeRoles = () async {
          entered.complete();
          await release.future;
        };
        final read = tester.widget<TextButton>(retry).onPressed!;
        read();
        read();
        await f.base.until(tester, () => entered.isCompleted);
        expect(f.roleReads, 2);
        release.complete();
        f.beforeRoles = null;
        await f.readyConfirm(tester);
        expect(f.roleReads, 2);
        expect(f.revokes, 0);
        await tester.tap(f.confirm.hitTestable());
        await f.base.until(
          tester,
          () => f.revokes == 1 && find.byType(AlertDialog).evaluate().isEmpty,
        );
        expect(f.roleReads, 3);
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'an unannounced downgrade is denied by the final real role lookup after an enabled confirm',
    (tester) async {
      final f = GroupRevokeFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.readyConfirm(tester);
        f.actorRole = 20;
        await tester.tap(f.confirm.hitTestable());
        await f.base.ready(tester, find.widgetWithText(TextButton, '重新核对'));
        expect(find.text('权限不足，请刷新后重试'), findsOneWidget);
        expect(f.revokes, 0);
        expect(f.roleReads, 2);
        await f.closeDialog(tester);
        expect(find.text('待核对的原消息'), findsOneWidget);
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'a retained enabled confirm cannot survive an actual group member event',
    (tester) async {
      final f = GroupRevokeFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.readyConfirm(tester);
        final old = tester.widget<TextButton>(f.confirm).onPressed!;
        await f.base.actual(tester, f.memberEvent);
        old();
        await tester.pump();
        expect(f.revokes, 0);
        expect(f.roleReads, 1);
        expect(find.text('会话或消息已变化，请关闭后重新核对。'), findsOneWidget);
        await f.closeDialog(tester);
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'same CID title unread and empty metadata refresh preserves the original moderation view',
    (tester) async {
      final f = GroupRevokeFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.readyConfirm(tester);
        final old = tester.widget<TextButton>(f.confirm).onPressed!;
        f.metadata['showName'] = '同一个群的新标题';
        f.metadata['unreadCount'] = 8;
        f.metadata['userID'] = null;
        final actualComposer = find.byType(
          ChatMediaActions,
          skipOffstage: false,
        );
        expect(actualComposer, findsOneWidget);
        final live = tester.widget<ChatMediaActions>(actualComposer).controller;
        await f.base.actual(tester, live.refreshConversation);
        await f.base.frame(tester);
        expect(tester.widget<TextButton>(f.confirm).onPressed, isNotNull);
        old();
        await f.base.until(
          tester,
          () => f.revokes == 1 && find.byType(AlertDialog).evaluate().isEmpty,
        );
        expect(f.revokes, 1);
        expect(f.roleReads, 2);
        expect(find.text('待核对的原消息'), findsNothing);
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'foreign coverage and return retire old confirm while a new explicit moderation still works',
    (tester) async {
      final f = GroupRevokeFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.readyConfirm(tester);
        final old = tester.widget<TextButton>(f.confirm).onPressed!;
        final foreign = MaterialPageRoute<void>(
          builder: (_) => const Scaffold(body: Text('外来页面')),
        );
        unawaited(f.base.navigator.currentState!.push(foreign));
        await f.base.ready(tester, find.text('外来页面'));
        old();
        await f.base.frame(tester);
        expect(find.text('外来页面'), findsOneWidget);
        expect(f.revokes, 0);
        f.base.navigator.currentState!.pop();
        await f.base.actual(tester, () => foreign.completed);
        await f.base.ready(tester, find.widgetWithText(TextButton, '关闭'));
        old();
        await f.base.frame(tester);
        expect(f.revokes, 0);
        await f.closeDialog(tester);
        await f.open(tester);
        await f.readyConfirm(tester);
        await tester.tap(f.confirm.hitTestable());
        await f.base.until(
          tester,
          () => f.revokes == 1 && find.byType(AlertDialog).evaluate().isEmpty,
        );
        expect(f.revokes, 1);
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'same ChatPage State CID ABA cannot renew its original enabled confirmation',
    (tester) async {
      final f = GroupRevokeFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.readyConfirm(tester);
        final old = tester.widget<TextButton>(f.confirm).onPressed!;
        f.sourceCid.value = 'other-cid';
        await tester.pump();
        f.sourceCid.value = f.cid;
        await tester.pump();
        old();
        await f.base.frame(tester);
        expect(f.revokes, 0);
        expect(find.text('会话或消息已变化，请关闭后重新核对。'), findsOneWidget);
        await f.closeDialog(tester);
      } finally {
        await f.close(tester);
      }
    },
  );

  for (final change in ['owner', 'issuer', 'tenant']) {
    testWidgets(
      'an open moderation confirm withdraws after original $change replacement',
      (tester) async {
        final f = GroupRevokeFixture();
        try {
          await f.mount(tester);
          await f.open(tester);
          await f.readyConfirm(tester);
          final old = tester.widget<TextButton>(f.confirm).onPressed!;
          final prior = f.sdk.session.state.credentials!;
          await f.base.actual(
            tester,
            () => f.base.changeOwner(
              user: change == 'owner' ? 'carol' : prior.userId,
              tenant: change == 'tenant' ? 'other-tenant' : prior.tenantId,
              issuer: change == 'issuer'
                  ? 'https://other-issuer.example/'
                  : prior.issuer,
            ),
          );
          old();
          await tester.pump();
          expect(f.revokes, 0);
          expect(find.text('待核对的原消息'), findsNothing);
          expect(find.text('会话或消息已变化，请关闭后重新核对。'), findsOneWidget);
          await f.closeDialog(tester);
          expect(find.byType(AlertDialog), findsNothing);
        } finally {
          await f.close(tester);
        }
      },
    );
  }

  testWidgets(
    'clear during an owned confirmation invalidates its original cut and retained action',
    (tester) async {
      final f = GroupRevokeFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.readyConfirm(tester);
        final old = tester.widget<TextButton>(f.confirm).onPressed!;
        await f.base.actual(
          tester,
          () => f.sdk.gateway.clearConversationMessages(
            f.sdk.session.state.generation,
            f.cid,
          ),
        );
        old();
        await tester.pump();
        expect(f.revokes, 0);
        expect(find.text('会话或消息已变化，请关闭后重新核对。'), findsOneWidget);
        await f.closeDialog(tester);
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'new expiry facts retire an enabled moderation action immediately and on the next frame',
    (tester) async {
      final f = GroupRevokeFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.readyConfirm(tester);
        final old = tester.widget<TextButton>(f.confirm).onPressed!;
        f.sdk.gateway.privacyLedger.observe(f.cid, {
          'clientMsgID': 'moderated-source',
          'attachedInfoElem': {
            'isPrivateChat': true,
            'hasReadTime': 1699999960000,
            'burnDuration': 30,
          },
        }, now: f.base.clock);
        old();
        expect(f.revokes, 0);
        await tester.pump();
        expect(f.confirm, findsNothing);
        expect(find.text('会话或消息已变化，请关闭后重新核对。'), findsOneWidget);
        await f.closeDialog(tester);
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'real doubletap dispatches once and native unknown offers only read review even after reopen',
    (tester) async {
      final f = GroupRevokeFixture()..nativeFailure = true;
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.readyConfirm(tester);
        final pressed = tester.widget<TextButton>(f.confirm).onPressed!;
        pressed();
        pressed();
        final review = find.widgetWithText(TextButton, '只读核对');
        await f.base.ready(tester, review);
        expect(f.revokes, 1);
        expect(f.confirm, findsNothing);
        final reads = f.finds;
        await tester.tap(review.hitTestable());
        await f.base.until(
          tester,
          () =>
              f.finds == reads + 1 &&
              find.text('原消息仍可见，撤回结果尚未确认，请稍后再核对。').evaluate().length == 1,
        );
        expect(f.revokes, 1);
        await f.closeDialog(tester);
        await f.open(tester);
        await f.base.ready(tester, review);
        expect(f.confirm, findsNothing);
        expect(f.revokes, 1);
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'late successful revoke ACK does not pop a foreign route or claim the dispatched write was cancelled',
    (tester) async {
      final f = GroupRevokeFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.readyConfirm(tester);
        final entered = Completer<void>(), release = f.defer();
        f.beforeNative = () async {
          entered.complete();
          await release.future;
        };
        await tester.tap(f.confirm.hitTestable());
        await f.base.until(tester, () => entered.isCompleted);
        final foreign = MaterialPageRoute<void>(
          builder: (_) => const Scaffold(body: Text('新的独立页面')),
        );
        unawaited(f.base.navigator.currentState!.push(foreign));
        await f.base.ready(tester, find.text('新的独立页面'));
        release.complete();
        await f.base.until(tester, () => f.sdk.messages.isEmpty);
        await f.base.frame(tester);
        expect(f.revokes, 1);
        expect(find.text('新的独立页面'), findsOneWidget);
        expect(f.sdk.messages, isEmpty);
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets(
    'actual standard revoke notification withdraws an open source without dispatching a second revoke',
    (tester) async {
      final f = GroupRevokeFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.readyConfirm(tester);
        final old = tester.widget<TextButton>(f.confirm).onPressed!;
        f.sdk.messages.clear();
        await f.base.actual(tester, f.revokedEvent);
        old();
        await tester.pump();
        expect(f.revokes, 0);
        expect(find.text('待核对的原消息'), findsNothing);
        expect(
          f.sdk.gateway.privacyLedger.lookup('moderated-source').hidden,
          true,
        );
        await f.closeDialog(tester);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'foreground loss retires moderation synchronously and withdraws its confirm on the next frame',
    (tester) async {
      final f = GroupRevokeFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.readyConfirm(tester);
        final old = tester.widget<TextButton>(f.confirm).onPressed!;
        tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
        old();
        expect(f.revokes, 0);
        await tester.pump();
        expect(f.confirm, findsNothing);
        expect(
          find.text('会话或消息已变化，请关闭后重新核对。'),
          findsOneWidget,
        );
        tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
        old();
        await tester.pump();
        expect(f.revokes, 0);
        expect(f.confirm, findsNothing);
        await f.closeDialog(tester);
      } finally {
        if (tester.binding.lifecycleState == AppLifecycleState.inactive) {
          tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
        }
        await f.close(tester);
      }
    },
  );
}
