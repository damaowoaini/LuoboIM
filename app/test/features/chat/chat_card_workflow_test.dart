import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_openim_sdk/flutter_openim_sdk.dart' show ListenerType;
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/presentation/chat_card_picker_page.dart';
import 'package:imapp/features/chat/presentation/chat_card_view.dart';
import 'package:imapp/features/chat/presentation/chat_merged_messages_page.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';
import 'package:imapp/app.dart' show routerProvider;
import 'package:imapp/features/contacts/contacts_providers.dart';
import 'package:imapp/features/contacts/presentation/user_profile_page.dart';
import 'package:imapp/core/error/app_failure.dart';

import 'chat_card_fixture.dart';

Finder dialogText(String value) =>
    find.descendant(of: find.byType(AlertDialog), matching: find.text(value));

Future<void> readyProfile(WidgetTester tester, ChatCardFixture f) async {
  await f.openReceived(tester);
  await f.transport.base.until(
    tester,
    () => find.text('权威资料').evaluate().length == 1,
  );
}

Future<void> requestReason(WidgetTester tester, ChatCardFixture f) async {
  final add = find.widgetWithText(OutlinedButton, '添加好友');
  await revealProfileControl(tester, f, add);
  await tester.tap(add.hitTestable());
  final input = find.descendant(
    of: find.byType(AlertDialog),
    matching: find.byType(TextField),
  );
  await f.transport.base.ready(tester, input);
  await tester.enterText(input, '真实验证请求');
}

Future<void> revealProfileControl(
  WidgetTester tester,
  ChatCardFixture f,
  Finder target,
) async {
  final list = find.descendant(
    of: find.byType(UserProfilePage),
    matching: find.byType(ListView),
  );
  expect(list, findsOneWidget);
  final listElement = tester.element(list);
  final scrollable = find.descendant(
    of: list,
    matching: find.byElementPredicate((element) {
      if (element.widget is! Scrollable) {
        return false;
      }
      var owned = false;
      element.visitAncestorElements((ancestor) {
        if (identical(ancestor, listElement)) {
          owned = true;
          return false;
        }
        return ancestor.widget is! Scrollable;
      });
      return owned;
    }),
  );
  expect(scrollable, findsOneWidget);
  await tester.scrollUntilVisible(
    target,
    140,
    scrollable: scrollable,
    maxScrolls: 25,
  );
  await tester.ensureVisible(target);
  await f.transport.base.ready(tester, target);
}

void main() {
  testWidgets(
    'ordinary contact profile without a message source keeps its normal friend request and dialog behavior',
    (tester) async {
      final f = ChatCardFixture();
      try {
        await f.mount(tester);
        unawaited(
          f.transport.base.navigator.currentState!.push(
            MaterialPageRoute<void>(
              builder: (_) => const UserProfilePage(userId: 'friend-54'),
            ),
          ),
        );
        await f.transport.base.ready(tester, find.byType(UserProfilePage));
        await f.transport.base.until(
          tester,
          () => find.text('权威资料').evaluate().length == 1,
        );
        await requestReason(tester, f);
        await tester.tap(find.widgetWithText(TextButton, '确定').hitTestable());
        await f.transport.base.until(tester, () => f.friendRequests == 1);
        expect(
          f.sdk.calls
              .singleWhere((v) => v.method == 'addFriend')
              .arguments['toUserID'],
          'friend-54',
        );
        expect(f.sends, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'actual attachment card entry searches the complete friend list beyond forty and confirms exact identities',
    (tester) async {
      final f = ChatCardFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.choose(tester, id: 'friend-54');
        expect(dialogText('账号 ID：friend-54'), findsOneWidget);
        expect(dialogText('账号 ID：bob'), findsOneWidget);
        expect(dialogText('发送到：测试联系人'), findsOneWidget);
        expect(dialogText('好友54'), findsOneWidget);
        expect(dialogText('私人备注54'), findsNothing);
        expect(f.creates, 0);
        await f.send(tester);
        await f.transport.base.until(
          tester,
          () =>
              f.sends == 1 &&
              find.byType(ChatCardPickerPage).evaluate().isEmpty,
        );
        expect(f.friendReads, 2);
        expect(f.creates, 1);
        expect(
          f.sdk.calls
              .singleWhere((v) => v.method == 'sendMessage')
              .arguments['message']['cardElem']['userID'],
          'friend-54',
        );
        expect(find.byType(ChatPage), findsOneWidget);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'same nickname friend selection and cancellation preserve the original picker without creating a message',
    (tester) async {
      final f = ChatCardFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.choose(tester, id: 'friend-1');
        expect(dialogText('同名好友'), findsOneWidget);
        expect(dialogText('账号 ID：friend-1'), findsOneWidget);
        expect(dialogText('账号 ID：friend-0'), findsNothing);
        final confirmation = ModalRoute.of(
          tester.element(find.byType(AlertDialog)),
        )!;
        await tester.tap(find.widgetWithText(TextButton, '取消').hitTestable());
        await f.transport.base.actual(tester, () => confirmation.completed);
        await f.transport.base.ready(tester, find.byType(ChatCardPickerPage));
        expect(find.text('发送好友名片？'), findsNothing);
        expect(f.creates, 0);
        expect(f.sends, 0);
        expect(
          find.byKey(const ValueKey(('share-card-friend', 'friend-1'))),
          findsOneWidget,
        );
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'friend read errors remain errors and explicit retry is mutually exclusive and read only',
    (tester) async {
      final f = ChatCardFixture()..failFriendRead = true;
      try {
        await f.mount(tester);
        await f.open(tester);
        expect(find.text('好友列表未能加载'), findsOneWidget);
        expect(find.text('暂无好友可分享'), findsNothing);
        f.failFriendRead = false;
        final entered = Completer<void>(), release = f.transport.defer();
        f.beforeFriends = () async {
          entered.complete();
          await release.future;
        };
        final retry = tester
            .widget<TextButton>(find.widgetWithText(TextButton, '重新读取好友列表'))
            .onPressed!;
        retry();
        retry();
        await f.transport.base.until(tester, () => entered.isCompleted);
        expect(f.friendReads, 2);
        expect(f.creates, 0);
        expect(f.sends, 0);
        release.complete();
        await f.transport.base.until(
          tester,
          () =>
              find.text('好友列表未能加载').evaluate().isEmpty &&
              find.byType(ListTile).evaluate().isNotEmpty,
        );
        expect(find.text('暂无好友可分享'), findsNothing);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'same CID metadata refresh preserves the current friend query and selected identity',
    (tester) async {
      final f = ChatCardFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await tester.enterText(
          find.byType(TextField).hitTestable(),
          'friend-54',
        );
        f.sdk.conversations.single['showName'] = '新的收件人显示名';
        await f.transport.base.actual(tester, f.controller.start);
        await f.transport.base.frame(tester);
        expect(
          find.byKey(const ValueKey(('share-card-friend', 'friend-54'))),
          findsOneWidget,
        );
        await f.choose(tester, id: 'friend-54');
        await f.send(tester);
        await f.transport.base.until(tester, () => f.sends == 1);
        expect(f.creates, 1);
      } finally {
        await f.close(tester);
      }
    },
  );
  for (final change in ['owner', 'issuer', 'tenant']) {
    testWidgets(
      'open card confirmation withdraws sensitive names after original $change changes',
      (tester) async {
        final f = ChatCardFixture();
        try {
          await f.mount(tester);
          await f.open(tester);
          await f.choose(tester);
          final retained = tester.widget<FilledButton>(f.confirm).onPressed!;
          await f.transport.base.actual(
            tester,
            () => f.transport.base.changeOwner(
              user: change == 'owner' ? 'carol' : 'alice',
              tenant: change == 'tenant'
                  ? 'another-tenant'
                  : f.sdk.credentials.tenantId,
              issuer: change == 'issuer'
                  ? 'https://another.example/'
                  : f.sdk.credentials.issuer,
            ),
          );
          await f.transport.base.frame(tester);
          expect(find.text('同名好友'), findsNothing);
          expect(find.text('账号 ID：friend-0'), findsNothing);
          expect(find.text('发送好友名片？'), findsNothing);
          retained();
          await f.transport.base.frame(tester);
          expect(f.creates, 0);
          expect(f.sends, 0);
        } finally {
          await f.close(tester);
        }
      },
    );
  }
  testWidgets(
    'foreign coverage and ABA cannot revive the original retained card confirmation',
    (tester) async {
      final f = ChatCardFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.choose(tester);
        final retained = tester.widget<FilledButton>(f.confirm).onPressed!;
        await f.transport.base.coverAndReturn(tester);
        retained();
        await f.transport.base.frame(tester);
        expect(find.text('发送好友名片？'), findsNothing);
        expect(find.text('操作已失效'), findsOneWidget);
        expect(f.creates, 0);
        expect(f.sends, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'same ChatPage State CID ABA cannot renew the old card confirmation or history cut',
    (tester) async {
      final f = ChatCardFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.choose(tester);
        final retained = tester.widget<FilledButton>(f.confirm).onPressed!;
        f.transport.sourceCid.value = 'other-cid';
        await f.transport.base.frame(tester);
        f.transport.sourceCid.value = f.transport.cid;
        await f.transport.base.frame(tester);
        retained();
        await f.transport.base.frame(tester);
        expect(find.text('账号 ID：friend-0'), findsNothing);
        expect(f.creates, 0);
        expect(f.sends, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'original conversation clear retires the actual picker confirmation before native create',
    (tester) async {
      final f = ChatCardFixture();
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.choose(tester);
        final retained = tester.widget<FilledButton>(f.confirm).onPressed!;
        await f.transport.base.actual(
          tester,
          () => f.sdk.gateway.clearConversationMessages(
            f.sdk.session.state.generation,
            f.transport.cid,
          ),
        );
        retained();
        await f.transport.base.frame(tester);
        expect(find.text('账号 ID：friend-0'), findsNothing);
        expect(f.creates, 0);
        expect(f.sends, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'late native card creation after picker exit cannot send or pop the chat',
    (tester) async {
      final f = ChatCardFixture();
      final entered = Completer<void>(), release = f.transport.defer();
      f.beforeCard = () async {
        entered.complete();
        await release.future;
      };
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.choose(tester);
        await f.send(tester);
        await f.transport.base.until(tester, () => entered.isCompleted);
        await f.popOwned(tester, find.byType(ChatCardPickerPage));
        release.complete();
        await f.transport.base.until(tester, () => !f.controller.preparing);
        expect(f.creates, 1);
        expect(f.sends, 0);
        expect(find.byType(ChatPage), findsOneWidget);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'latest no longer joined group state rejects a retained card confirmation before any SDK create',
    (tester) async {
      final f = ChatCardFixture();
      try {
        await f.mount(tester, group: true);
        await f.open(tester);
        await f.choose(tester);
        final retained = tester.widget<FilledButton>(f.confirm).onPressed!;
        f.sdk.conversations.single['isNotInGroup'] = true;
        await f.transport.base.actual(tester, f.controller.start);
        retained();
        await f.transport.base.until(
          tester,
          () => find.text('发送好友名片？').evaluate().isEmpty,
        );
        await f.transport.base.frame(tester);
        expect(f.creates, 0);
        expect(f.sends, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'already dispatched card ACK after foreign coverage cannot pop that route or replay',
    (tester) async {
      final f = ChatCardFixture();
      final entered = Completer<void>(), release = f.transport.defer();
      f.transport.beforeSend = () async {
        entered.complete();
        await release.future;
      };
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.choose(tester);
        await f.send(tester);
        await f.transport.base.until(tester, () => entered.isCompleted);
        final foreign = MaterialPageRoute<void>(
          builder: (_) => const Scaffold(body: Text('外来页面')),
        );
        unawaited(f.transport.base.navigator.currentState!.push(foreign));
        await f.transport.base.ready(tester, find.text('外来页面'));
        release.complete();
        await f.transport.base.until(tester, () => !f.controller.preparing);
        expect(foreign.isCurrent, isTrue);
        expect(find.text('外来页面'), findsOneWidget);
        expect(f.creates, 1);
        expect(f.sends, 1);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'unknown card ACK blocks the original bubble but a new explicit picker can send a new card',
    (tester) async {
      final f = ChatCardFixture()..transport.failSend = true;
      try {
        await f.mount(tester);
        await f.open(tester);
        await f.choose(tester);
        await f.send(tester);
        await f.transport.base.until(
          tester,
          () => find.textContaining('发送结果未确认').evaluate().isNotEmpty,
        );
        await f.transport.base.actual(
          tester,
          () => f.controller.resend('created-card-1'),
        );
        expect(f.creates, 1);
        expect(f.sends, 1);
        await f.popOwned(tester, find.byType(ChatCardPickerPage));
        f.transport.failSend = false;
        await f.open(tester);
        await f.choose(tester, id: 'friend-1');
        await f.send(tester);
        await f.transport.base.until(
          tester,
          () =>
              f.sends == 2 &&
              find.byType(ChatCardPickerPage).evaluate().isEmpty,
        );
        await f.transport.base.actual(
          tester,
          () => f.controller.resend('created-card-1'),
        );
        expect(f.creates, 2);
        expect(f.sends, 2);
        expect(f.controller.isUnconfirmedCard('created-card-1'), isTrue);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'received card renders no network image and explicit profile lookup uses the exact SDK and business UID',
    (tester) async {
      final f = ChatCardFixture();
      try {
        await f.mount(tester, received: true);
        final card = find.byType(ChatCardView);
        await f.transport.base.ready(tester, card);
        expect(
          find.descendant(of: card, matching: find.byType(Image)),
          findsNothing,
        );
        expect(f.profileReads, 0);
        expect(f.httpCalls, isEmpty);
        await f.openReceived(tester);
        await f.transport.base.until(
          tester,
          () => find.text('权威资料').evaluate().length == 1,
        );
        expect(f.profileReads, 1);
        expect(f.httpCalls.single.path, endsWith('/user/find/full'));
        expect(f.httpCalls.single.data, {
          'userIDs': ['friend-54'],
        });
        expect(
          f.sdk.calls
              .singleWhere((v) => v.method == 'getUsersInfo')
              .arguments['userIDList'],
          ['friend-54'],
        );
        expect(find.text('ID：friend-54'), findsOneWidget);
        expect(find.byType(Image), findsNothing);
        await revealProfileControl(tester, f, find.text('添加好友'));
        expect(find.text('添加好友'), findsOneWidget);
        expect(find.text('发消息'), findsOneWidget);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'malformed received card has no profile action or received faceURL request',
    (tester) async {
      final f = ChatCardFixture();
      try {
        await f.mount(
          tester,
          receivedWire: f.wire(user: 'invalid uid', name: 'not trusted'),
        );
        await f.transport.base.until(
          tester,
          () => find.text('名片资料暂不可用').evaluate().isNotEmpty,
        );
        expect(f.profileReads, 0);
        expect(f.httpCalls, isEmpty);
        expect(find.byType(ChatCardDetail), findsNothing);
        expect(find.text('not trusted'), findsNothing);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'wrong authoritative profile ID is rejected and explicit retry can query the original identity',
    (tester) async {
      final f = ChatCardFixture()..wrongProfile = 'wrong-user';
      try {
        await f.mount(tester, received: true);
        await f.openReceived(tester);
        await f.transport.base.ready(
          tester,
          find.widgetWithText(TextButton, '重试'),
        );
        expect(find.text('权威资料'), findsNothing);
        expect(f.httpCalls, isEmpty);
        f.wrongProfile = null;
        await tester.tap(find.widgetWithText(TextButton, '重试').hitTestable());
        await f.transport.base.until(
          tester,
          () => find.text('权威资料').evaluate().length == 1,
        );
        expect(f.profileReads, 2);
        expect(f.httpCalls.single.data, {
          'userIDs': ['friend-54'],
        });
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'late authoritative profile after account switch withdraws all old card content',
    (tester) async {
      final f = ChatCardFixture();
      final entered = Completer<void>(), release = f.transport.defer();
      f.beforeProfile = () async {
        entered.complete();
        await release.future;
      };
      try {
        await f.mount(tester, received: true);
        await f.openReceived(tester);
        await f.transport.base.until(tester, () => entered.isCompleted);
        final source = tester
            .widget<UserProfilePage>(find.byType(UserProfilePage))
            .source!;
        final generation = f.sdk.session.state.generation;
        await f.transport.base.actual(tester, () async {
          final changed = f.transport.base.changeOwner(user: 'carol');
          expect(f.sdk.session.state.generation, greaterThan(generation));
          expect(source.current(), isFalse);
          release.complete();
          await changed;
        });
        await f.transport.base.frame(tester);
        expect(find.text('ID：friend-54'), findsNothing);
        expect(find.text('权威资料'), findsNothing);
        expect(f.httpCalls, isEmpty);
        expect(f.creates, 0);
        expect(f.sends, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'actual merged child card detail stays valid under its own route and withdraws after original CID clear',
    (tester) async {
      final f = ChatCardFixture();
      try {
        await f.mount(tester, received: true, merged: true);
        await f.openMerged(tester);
        expect(find.byType(ChatMergedMessagesPage), findsOneWidget);
        await f.openReceived(tester);
        await f.transport.base.until(
          tester,
          () => find.text('权威资料').evaluate().length == 1,
        );
        expect(find.text('ID：friend-54'), findsOneWidget);
        await f.transport.base.actual(
          tester,
          () => f.sdk.gateway.clearConversationMessages(
            f.sdk.session.state.generation,
            f.transport.cid,
          ),
        );
        await f.transport.base.frame(tester);
        expect(find.text('权威资料'), findsNothing);
        expect(find.text('ID：friend-54'), findsNothing);
        expect(f.profileReads, 1);
      } finally {
        await f.close(tester);
      }
    },
  );
  for (final event in ['revoke', 'expire']) {
    testWidgets(
      'an open received card detail withdraws after original $event without granting a new profile lease',
      (tester) async {
        final f = ChatCardFixture();
        try {
          await f.mount(
            tester,
            receivedWire: f.wire(private: event == 'expire'),
          );
          await f.openReceived(tester);
          await f.transport.base.until(
            tester,
            () => find.text('权威资料').evaluate().length == 1,
          );
          if (event == 'revoke') {
            await f.transport.base.actual(
              tester,
              () => f.sdk.callback(ListenerType.advancedMsgListener, {
                'type': 'onNewRecvMessageRevoked',
                'data': {
                  'messageRevoked': jsonEncode({
                    'clientMsgID': 'received-card',
                  }),
                },
              }),
            );
          } else {
            final source = tester
                .widget<UserProfilePage>(find.byType(UserProfilePage))
                .source!;
            expect(source.current(), isTrue);
            f.sdk.gateway.privacyLedger.observe(f.transport.cid, {
              'clientMsgID': 'received-card',
              'attachedInfoElem': {
                'isPrivateChat': true,
                'hasReadTime': DateTime.now().millisecondsSinceEpoch - 60000,
                'burnDuration': 30,
              },
            }, now: DateTime.now());
            expect(source.current(), isFalse);
            expect(source.canRetain(), isFalse);
            expect(source.check, throwsA(isA<AppFailure>()));
          }
          await f.transport.base.frame(tester);
          expect(find.text('权威资料'), findsNothing);
          expect(find.text('ID：friend-54'), findsNothing);
          expect(f.profileReads, 1);
          expect(f.httpCalls, hasLength(1));
        } finally {
          await f.close(tester);
        }
      },
    );
  }
  testWidgets(
    'foreign route ABA permanently retires an already open card detail and its retained retry',
    (tester) async {
      final f = ChatCardFixture()..wrongProfile = 'wrong-user';
      try {
        await f.mount(tester, received: true);
        await f.openReceived(tester);
        final retry = find.widgetWithText(TextButton, '重试');
        await f.transport.base.ready(tester, retry);
        final retained = tester.widget<TextButton>(retry).onPressed!;
        await f.transport.base.coverAndReturn(tester);
        f.wrongProfile = null;
        retained();
        await f.transport.base.frame(tester);
        expect(find.text('权威资料'), findsNothing);
        expect(find.text('ID：friend-54'), findsNothing);
        expect(f.profileReads, 1);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    '360px large text card selection confirmation and received detail remain usable without overflow',
    (tester) async {
      final f = ChatCardFixture();
      try {
        await f.mount(tester, small: true, received: true);
        await f.open(tester);
        await f.choose(tester, id: 'friend-54');
        await tester.ensureVisible(f.confirm);
        await f.transport.base.ready(tester, f.confirm);
        expect(tester.getSize(f.confirm).height, greaterThanOrEqualTo(44));
        expect(tester.takeException(), isNull);
        await tester.tap(find.widgetWithText(TextButton, '取消').hitTestable());
        await f.transport.base.ready(tester, find.byType(ChatCardPickerPage));
        await f.popOwned(tester, find.byType(ChatCardPickerPage));
        await f.openReceived(tester);
        await f.transport.base.until(
          tester,
          () => find.text('权威资料').evaluate().length == 1,
        );
        expect(find.text('ID：friend-54'), findsOneWidget);
        expect(tester.takeException(), isNull);
        expect(f.creates, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'URL-looking received labels remain plain text and never become a URL request or link action',
    (tester) async {
      final f = ChatCardFixture();
      try {
        await f.mount(
          tester,
          receivedWire: f.wire(user: 'https:exact-id', name: 'www.example'),
        );
        await f.transport.base.ready(tester, find.byType(ChatCardView));
        expect(find.text('www.example'), findsOneWidget);
        expect(f.httpCalls, isEmpty);
        expect(f.profileReads, 0);
        await readyProfile(tester, f);
        expect(f.httpCalls.single.data, {
          'userIDs': ['https:exact-id'],
        });
        expect(f.httpCalls.single.path, endsWith('/user/find/full'));
        expect(find.text('ID：https:exact-id'), findsOneWidget);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'real source-bound UserProfilePage permits an explicitly confirmed current-policy friend request',
    (tester) async {
      final f = ChatCardFixture();
      try {
        await f.mount(tester, received: true);
        await readyProfile(tester, f);
        expect(find.byType(UserProfilePage), findsOneWidget);
        await requestReason(tester, f);
        final save = find.widgetWithText(TextButton, '确定');
        await f.transport.base.ready(tester, save);
        await tester.tap(save.hitTestable());
        await f.transport.base.until(tester, () => f.friendRequests == 1);
        final request = f.sdk.calls.singleWhere((v) => v.method == 'addFriend');
        expect(request.arguments['toUserID'], 'friend-54');
        expect(request.arguments['reqMsg'], '真实验证请求');
        expect(f.profileReads, greaterThanOrEqualTo(3));
        expect(f.sends, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'cached ordinary profile policy cannot authorize the new card source without fresh exact hydration',
    (tester) async {
      final f = ChatCardFixture();
      try {
        await f.mount(tester, received: true);
        final held = f.container!.listen(
          contactProfileProvider('friend-54'),
          (_, _) {},
        );
        try {
          await f.transport.base.actual(
            tester,
            () => f.container!.read(contactProfileProvider('friend-54').future),
          );
          expect(f.profileReads, 1);
          f.friendPolicy = 0;
          await readyProfile(tester, f);
          expect(f.profileReads, 2);
          await revealProfileControl(tester, f, find.text('对方暂不接受好友申请'));
          expect(find.text('添加好友'), findsNothing);
          expect(find.text('对方暂不接受好友申请'), findsOneWidget);
          expect(f.friendRequests, 0);
        } finally {
          held.close();
        }
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'clear during the requestFriend inner profile await prevents the real final addFriend dispatch',
    (tester) async {
      final f = ChatCardFixture();
      final entered = Completer<void>(), release = f.transport.defer();
      try {
        await f.mount(tester, received: true);
        await readyProfile(tester, f);
        f.beforeProfile = () async {
          if (f.profileReads == 3) {
            entered.complete();
            await release.future;
          }
        };
        await requestReason(tester, f);
        await tester.tap(find.widgetWithText(TextButton, '确定').hitTestable());
        await f.transport.base.until(tester, () => entered.isCompleted);
        await f.transport.base.actual(
          tester,
          () => f.sdk.gateway.clearConversationMessages(
            f.sdk.session.state.generation,
            f.transport.cid,
          ),
        );
        release.complete();
        await f.transport.base.frame(tester);
        expect(f.friendRequests, 0);
        expect(find.text('权威资料'), findsNothing);
        expect(find.text('ID：friend-54'), findsNothing);
        expect(f.httpCalls, hasLength(2));
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'foreign coverage of the owned friend-reason dialog retires its retained submit without a new action',
    (tester) async {
      final f = ChatCardFixture();
      try {
        await f.mount(tester, received: true);
        await readyProfile(tester, f);
        await requestReason(tester, f);
        final retained = tester
            .widget<TextButton>(find.widgetWithText(TextButton, '确定'))
            .onPressed!;
        await f.transport.base.coverAndReturn(tester);
        retained();
        await f.transport.base.frame(tester);
        expect(find.text('真实验证请求'), findsNothing);
        expect(find.text('操作已失效'), findsOneWidget);
        expect(f.friendRequests, 0);
        expect(f.conversations, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
  for (final change in ['owner', 'issuer', 'tenant']) {
    testWidgets(
      'owned profile dialog withdraws its old identity and reason after $change rotation',
      (tester) async {
        final f = ChatCardFixture();
        try {
          await f.mount(tester, received: true);
          await readyProfile(tester, f);
          await requestReason(tester, f);
          final retained = tester
              .widget<TextButton>(find.widgetWithText(TextButton, '确定'))
              .onPressed!;
          await f.transport.base.actual(
            tester,
            () => f.transport.base.changeOwner(
              user: change == 'owner' ? 'carol' : 'alice',
              tenant: change == 'tenant'
                  ? 'another-tenant'
                  : f.sdk.credentials.tenantId,
              issuer: change == 'issuer'
                  ? 'https://another.example/'
                  : f.sdk.credentials.issuer,
            ),
          );
          retained();
          await f.transport.base.frame(tester);
          expect(find.text('真实验证请求'), findsNothing);
          expect(find.text('权威资料'), findsNothing);
          expect(find.text('ID：friend-54'), findsNothing);
          expect(f.friendRequests, 0);
          expect(f.conversations, 0);
        } finally {
          await f.close(tester);
        }
      },
    );
  }
  testWidgets(
    'authoritative group hideMemberProfiles blocks a received card without trusting payload group claims',
    (tester) async {
      final f = ChatCardFixture()..groupHideProfiles = true;
      try {
        await f.mount(tester, group: true, received: true);
        await f.openReceived(tester);
        await f.transport.base.until(
          tester,
          () => find.text('该群已禁止查看成员资料').evaluate().isNotEmpty,
        );
        expect(find.text('权威资料'), findsNothing);
        expect(find.text('添加好友'), findsNothing);
        expect(find.text('发消息'), findsNothing);
        expect(f.friendRequests, 0);
        expect(
          f.sdk.calls
              .where((v) => v.method == 'getGroupsInfo')
              .every(
                (v) => (v.arguments as Map)['groupIDList'].single == 'group-a',
              ),
          isTrue,
        );
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'latest original group preventMemberFriends denies a retained card application after fresh policy read',
    (tester) async {
      final f = ChatCardFixture();
      try {
        await f.mount(tester, group: true, received: true);
        await readyProfile(tester, f);
        await requestReason(tester, f);
        f.groupPreventFriends = true;
        await tester.tap(find.widgetWithText(TextButton, '确定').hitTestable());
        await f.transport.base.until(
          tester,
          () => find.text('添加好友').evaluate().isEmpty,
        );
        expect(f.friendRequests, 0);
        expect(f.conversations, 0);
        expect(
          f.sdk.calls.where((v) => v.method == 'getGroupsInfo').length,
          greaterThanOrEqualTo(2),
        );
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'clear during the real group profile await prevents subsequent group SDK reads and stale card content',
    (tester) async {
      final f = ChatCardFixture();
      final entered = Completer<void>(), release = f.transport.defer();
      f.beforeGroups = () async {
        entered.complete();
        await release.future;
      };
      try {
        await f.mount(tester, group: true, received: true);
        await f.openReceived(tester);
        await f.transport.base.until(tester, () => entered.isCompleted);
        final joinedBefore = f.sdk.calls
            .where((v) => v.method == 'isJoinGroup')
            .length;
        await f.transport.base.actual(
          tester,
          () => f.sdk.gateway.clearConversationMessages(
            f.sdk.session.state.generation,
            f.transport.cid,
          ),
        );
        release.complete();
        await f.transport.base.frame(tester);
        expect(
          f.sdk.calls.where((v) => v.method == 'isJoinGroup'),
          hasLength(joinedBefore),
        );
        expect(find.text('权威资料'), findsNothing);
        expect(f.friendRequests, 0);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'verified card starts its exact user conversation through the real production router',
    (tester) async {
      final f = ChatCardFixture();
      try {
        await f.mount(tester, received: true, productionRouter: true);
        expect(f.router, same(f.container!.read(routerProvider)));
        await readyProfile(tester, f);
        final send = find.widgetWithText(FilledButton, '发消息');
        await tester.ensureVisible(send);
        await f.transport.base.ready(tester, send);
        await tester.tap(send.hitTestable());
        final target = find.byElementPredicate(
          (element) =>
              element.widget is ChatPage &&
              (element.widget as ChatPage).conversationId ==
                  'single-alice-friend-54' &&
              ModalRoute.of(element)?.isCurrent == true,
        );
        await f.transport.base.ready(tester, target);
        expect(f.conversations, 1);
        expect(
          f.sdk.calls
              .singleWhere((v) => v.method == 'getOneConversation')
              .arguments['sourceID'],
          'friend-54',
        );
        expect(
          f.sdk.calls.where(
            (v) =>
                v.method == 'getAdvancedHistoryMessageList' &&
                v.arguments['conversationID'] == 'single-alice-friend-54',
          ),
          isNotEmpty,
        );
        expect(tester.takeException(), isNull);
      } finally {
        await f.close(tester);
      }
    },
  );
  testWidgets(
    'late exact conversation resolution after source clear cannot navigate away from the current profile route',
    (tester) async {
      final f = ChatCardFixture();
      final entered = Completer<void>(), release = f.transport.defer();
      f.beforeConversation = () async {
        entered.complete();
        await release.future;
      };
      try {
        await f.mount(tester, received: true, productionRouter: true);
        await readyProfile(tester, f);
        final route = ModalRoute.of(
          tester.element(find.byType(UserProfilePage)),
        )!;
        final send = find.widgetWithText(FilledButton, '发消息');
        await tester.ensureVisible(send);
        await f.transport.base.ready(tester, send);
        await tester.tap(send.hitTestable());
        await f.transport.base.until(tester, () => entered.isCompleted);
        await f.transport.base.actual(
          tester,
          () => f.sdk.gateway.clearConversationMessages(
            f.sdk.session.state.generation,
            f.transport.cid,
          ),
        );
        release.complete();
        await f.transport.base.frame(tester);
        expect(route.isCurrent, isTrue);
        expect(find.text('ID：friend-54'), findsNothing);
        expect(
          f.sdk.calls.where(
            (v) =>
                v.method == 'getAdvancedHistoryMessageList' &&
                v.arguments['conversationID'] == 'single-alice-friend-54',
          ),
          isEmpty,
        );
        expect(f.conversations, 1);
      } finally {
        await f.close(tester);
      }
    },
  );
  for (final source in ['private', 'normal', 'cleared', 'ordinary contact']) {
    testWidgets(
      'actual $source profile respects original ID clipboard retention permission',
      (tester) async {
        final f = ChatCardFixture();
        final clipboard = <Object?>[];
        final messenger =
            TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
        messenger.setMockMethodCallHandler(SystemChannels.platform, (
          call,
        ) async {
          if (call.method == 'Clipboard.setData') {
            clipboard.add(call.arguments);
          }
          return null;
        });
        try {
          if (source == 'ordinary contact') {
            await f.mount(tester);
            unawaited(
              f.transport.base.navigator.currentState!.push(
                MaterialPageRoute<void>(
                  builder: (_) => const UserProfilePage(userId: 'friend-54'),
                ),
              ),
            );
            await f.transport.base.ready(tester, find.byType(UserProfilePage));
            await f.transport.base.until(
              tester,
              () => find.text('权威资料').evaluate().length == 1,
            );
          } else {
            await f.mount(
              tester,
              receivedWire: f.wire(private: source == 'private'),
            );
            await readyProfile(tester, f);
          }
          final profile = find.byType(UserProfilePage);
          final copy = find.widgetWithIcon(IconButton, Icons.copy_outlined);
          await tester.ensureVisible(copy);
          await f.transport.base.ready(tester, copy);
          final button = tester.widget<IconButton>(copy);
          expect(find.text('ID：friend-54'), findsOneWidget);
          expect(tester.getSize(copy).height, greaterThanOrEqualTo(44));
          expect(tester.getSize(copy).width, greaterThanOrEqualTo(44));
          expect(
            find.descendant(of: profile, matching: find.byType(SelectableText)),
            source == 'ordinary contact' ? findsOneWidget : findsNothing,
          );
          if (source == 'private') {
            expect(button.onPressed, isNull);
            expect(clipboard, isEmpty);
            expect(find.text('权威资料'), findsOneWidget);
          } else if (source == 'cleared') {
            final retained = button.onPressed!;
            await f.transport.base.actual(
              tester,
              () => f.sdk.gateway.clearConversationMessages(
                f.sdk.session.state.generation,
                f.transport.cid,
              ),
            );
            retained();
            await f.transport.base.frame(tester);
            expect(clipboard, isEmpty);
            expect(find.text('ID：friend-54'), findsNothing);
            expect(f.profileReads, 1);
          } else {
            expect(button.onPressed, isNotNull);
            await tester.tap(copy.hitTestable());
            await f.transport.base.until(tester, () => clipboard.length == 1);
            expect(clipboard, [
              {'text': 'friend-54'},
            ]);
          }
          expect(f.friendRequests, 0);
          expect(f.sends, 0);
        } finally {
          try {
            await f.close(tester);
          } finally {
            messenger.setMockMethodCallHandler(SystemChannels.platform, null);
          }
        }
      },
    );
  }
}
