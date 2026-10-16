import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/chat/domain/chat_card.dart';
import 'package:imapp/features/chat/domain/chat_message.dart';

import 'chat_card_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  for (final group in [false, true]) {
    test(
      'real SDK Card108 ${group ? 'group' : 'single'} sends exact confirmed friend and original recipient',
      () async {
        final f = ChatCardFixture();
        await f.startRepository(group: group);
        try {
          final result = await f.controller.sendCard(
            'friend-54',
            intentId: 'intent-a',
            access: f.sdk.gateway.historyAccess(
              f.sdk.session.state.generation,
              conversationId: f.transport.cid,
            ),
            checkSource: () {},
          );
          expect(result.kind, CardSendKind.sent);
          expect(f.friendReads, 1);
          expect(f.creates, 1);
          expect(f.sends, 1);
          final native = f.sdk.calls.singleWhere(
            (v) => v.method == 'createCardMessage',
          );
          expect(native.arguments['cardMessage'], {
            'userID': 'friend-54',
            'nickname': '好友54',
            'faceURL': null,
            'ex': null,
          });
          final send = f.sdk.calls.singleWhere(
            (v) => v.method == 'sendMessage',
          );
          expect(
            send.arguments[group ? 'groupID' : 'userID'],
            group ? 'group-a' : 'bob',
          );
          expect(send.arguments['message']['contentType'], 108);
          expect(send.arguments['message']['cardElem']['userID'], 'friend-54');
          expect(
            f.controller.messages
                .singleWhere((v) => v.id == result.messageId)
                .kind,
            ChatMessageKind.card,
          );
        } finally {
          await f.closeRepository();
        }
      },
    );
  }
  for (final changed in [
    'removed',
    'duplicate',
    'blacklisted',
    'blocked duplicate',
    'nested mismatch',
    'no longer friend',
  ]) {
    test(
      'actual complete friend reread rejects $changed selection before native card creation',
      () async {
        final f = ChatCardFixture();
        await f.startRepository();
        try {
          if (changed == 'removed') f.friends.removeAt(0);
          if (changed == 'duplicate') f.friends.add(Map.of(f.friends.first));
          if (changed == 'blacklisted') {
            f.friends.first['blackInfo'] = {'userID': 'friend-0'};
          }
          if (changed == 'blocked duplicate') {
            f.friends.add({
              'friendInfo': {'userID': 'friend-0', 'nickname': '同名好友'},
              'blackInfo': {'userID': 'friend-0'},
            });
          }
          if (changed == 'nested mismatch') {
            f.friends.first['publicInfo'] = {
              'userID': 'friend-0',
              'nickname': '公开名',
            };
          }
          if (changed == 'nested mismatch') {
            f.friends.first['friendInfo'] = {
              'userID': 'different-id',
              'nickname': '旧关系',
            };
          }
          if (changed == 'no longer friend') {
            f.friends[0] = {
              'publicInfo': {'userID': 'friend-0', 'nickname': '旧名片'},
            };
          }
          final result = await f.controller.sendCard(
            'friend-0',
            intentId: 'intent-a',
            access: f.sdk.gateway.historyAccess(
              f.sdk.session.state.generation,
              conversationId: f.transport.cid,
            ),
            checkSource: () {},
          );
          expect(result.kind, CardSendKind.notDispatched);
          expect(f.friendReads, 1);
          expect(f.creates, 0);
          expect(f.sends, 0);
        } finally {
          await f.closeRepository();
        }
      },
    );
  }
  test(
    'invalid actual native Card108 identity is rejected before send',
    () async {
      final f = ChatCardFixture();
      await f.startRepository();
      try {
        f.invalidCard = true;
        final result = await f.controller.sendCard(
          'friend-0',
          intentId: 'intent-a',
          access: f.sdk.gateway.historyAccess(
            f.sdk.session.state.generation,
            conversationId: f.transport.cid,
          ),
          checkSource: () {},
        );
        expect(result.kind, CardSendKind.notDispatched);
        expect(f.creates, 1);
        expect(f.sends, 0);
      } finally {
        await f.closeRepository();
      }
    },
  );
  test(
    'a cleared original cut cannot borrow a new lease for card preparation',
    () async {
      final f = ChatCardFixture();
      await f.startRepository();
      try {
        final cut = f.sdk.gateway.historyAccess(
          f.sdk.session.state.generation,
          conversationId: f.transport.cid,
        );
        await f.sdk.gateway.clearConversationMessages(
          f.sdk.session.state.generation,
          f.transport.cid,
        );
        final result = await f.controller.sendCard(
          'friend-0',
          intentId: 'intent-a',
          access: cut,
          checkSource: () {},
        );
        expect(result.kind, CardSendKind.notDispatched);
        expect(f.friendReads, 0);
        expect(f.creates, 0);
        expect(f.sends, 0);
      } finally {
        await f.closeRepository();
      }
    },
  );
  test('retirement during real friend reread prevents both local create and SDK send', () async {
    final f = ChatCardFixture();
    await f.startRepository();
    try {
      final entered = Completer<void>(), release = f.transport.defer();
      f.beforeFriends = () async {
        entered.complete();
        await release.future;
      };
      var current = true;
      final pending = f.controller.sendCard(
        'friend-0',
        intentId: 'intent-a',
        access: f.sdk.gateway.historyAccess(
          f.sdk.session.state.generation,
          conversationId: f.transport.cid,
        ),
        checkSource: () {
          if (!current) {
            throw const AppFailure(FailureKind.cancelled);
          }
        },
      );
      await entered.future;
      current = false;
      release.complete();
      expect((await pending).kind, CardSendKind.notDispatched);
      expect(f.creates, 0);
      expect(f.sends, 0);
    } finally {
      await f.closeRepository();
    }
  });
  test('retirement during actual native create prevents the subsequent remote send', () async {
    final f = ChatCardFixture();
    await f.startRepository();
    try {
      final entered = Completer<void>(), release = f.transport.defer();
      f.beforeCard = () async {
        entered.complete();
        await release.future;
      };
      var current = true;
      final pending = f.controller.sendCard(
        'friend-0',
        intentId: 'intent-a',
        access: f.sdk.gateway.historyAccess(
          f.sdk.session.state.generation,
          conversationId: f.transport.cid,
        ),
        checkSource: () {
          if (!current) {
            throw const AppFailure(FailureKind.cancelled);
          }
        },
      );
      await entered.future;
      current = false;
      release.complete();
      expect((await pending).kind, CardSendKind.notDispatched);
      expect(f.creates, 1);
      expect(f.sends, 0);
    } finally {
      await f.closeRepository();
    }
  });
  test('synchronous card admission and unknown ACK lock only the original intent and bubble', () async {
    final f = ChatCardFixture();
    await f.startRepository();
    try {
      final entered = Completer<void>(), release = f.transport.defer();
      f.transport.beforeSend = () async {
        entered.complete();
        await release.future;
      };
      f.transport.failSend = true;
      final cut = f.sdk.gateway.historyAccess(
        f.sdk.session.state.generation,
        conversationId: f.transport.cid,
      );
      Future<CardSendResult> send(String intent) => f.controller.sendCard(
        'friend-0',
        intentId: intent,
        access: cut,
        checkSource: () {},
      );
      final pending = send('intent-a');
      await entered.future;
      expect((await send('intent-b')).kind, CardSendKind.notDispatched);
      release.complete();
      final first = await pending;
      expect(first.kind, CardSendKind.unconfirmed);
      expect(f.controller.isCardIntentUnconfirmed('intent-a'), isTrue);
      await f.controller.resend(first.messageId!);
      expect((await send('intent-a')).kind, CardSendKind.notDispatched);
      expect(f.creates, 1);
      expect(f.sends, 1);
      f.transport.beforeSend = null;
      f.transport.failSend = false;
      expect((await send('intent-b')).kind, CardSendKind.sent);
      await f.controller.resend(first.messageId!);
      expect(f.creates, 2);
      expect(f.sends, 2);
      expect(f.controller.isUnconfirmedCard(first.messageId!), isTrue);
    } finally {
      await f.closeRepository();
    }
  });
  test('source retirement during exact SDK profile prevents the business lookup and friend mutation', () async {
    final f = ChatCardFixture();
    await f.startRepository();
    try {
      final entered = Completer<void>(), release = f.transport.defer();
      f.beforeProfile = () async {
        entered.complete();
        await release.future;
      };
      var current = true;
      final pending = f.contacts.requestFriend(
        'friend-54',
        '验证',
        checkSource: () {
          if (!current) {
            throw const AppFailure(FailureKind.cancelled);
          }
        },
      );
      final rejected = expectLater(pending, throwsA(isA<AppFailure>()));
      await entered.future;
      current = false;
      release.complete();
      await rejected;
      expect(f.profileReads, 1);
      expect(f.httpCalls, isEmpty);
      expect(f.friendRequests, 0);
    } finally {
      await f.closeRepository();
    }
  });
  test('source retirement during authoritative business policy prevents final addFriend dispatch', () async {
    final f = ChatCardFixture();
    await f.startRepository();
    try {
      final entered = Completer<void>(), release = f.transport.defer();
      f.beforeBusiness = () async {
        entered.complete();
        await release.future;
      };
      var current = true;
      final pending = f.contacts.requestFriend(
        'friend-54',
        '验证',
        checkSource: () {
          if (!current) {
            throw const AppFailure(FailureKind.cancelled);
          }
        },
      );
      final rejected = expectLater(pending, throwsA(isA<AppFailure>()));
      await entered.future;
      current = false;
      release.complete();
      await rejected;
      expect(f.httpCalls, hasLength(1));
      expect(f.friendRequests, 0);
    } finally {
      await f.closeRepository();
    }
  });
  test('actual gateway operation entry checks the source again before any native contact lookup', () async {
    final f = ChatCardFixture();
    await f.startRepository();
    try {
      var checks = 0;
      await expectLater(
        f.contacts.conversation(
          'friend-54',
          checkSource: () {
            if (++checks > 1) {
              throw const AppFailure(FailureKind.cancelled);
            }
          },
        ),
        throwsA(isA<AppFailure>()),
      );
      expect(f.conversations, 0);
      expect(
        f.sdk.calls.where((v) => v.method == 'getOneConversation'),
        isEmpty,
      );
    } finally {
      await f.closeRepository();
    }
  });
  for (final changed in ['type', 'user', 'group', 'empty CID']) {
    test(
      'actual contact conversation rejects $changed response before returning a navigation target',
      () async {
        final f = ChatCardFixture();
        await f.startRepository();
        try {
          f.conversationResponse = (value) => {
            ...value,
            if (changed == 'type') 'conversationType': 3,
            if (changed == 'user') 'userID': 'other-user',
            if (changed == 'group') 'groupID': 'other-group',
            if (changed == 'empty CID') 'conversationID': '',
          };
          await expectLater(
            f.contacts.conversation('friend-54', checkSource: () {}),
            throwsA(
              isA<AppFailure>().having(
                (failure) => failure.kind,
                'kind',
                FailureKind.invalidResponse,
              ),
            ),
          );
          expect(f.conversations, 1);
          final request = f.sdk.calls.singleWhere(
            (v) => v.method == 'getOneConversation',
          );
          expect(request.arguments['sourceID'], 'friend-54');
          expect(request.arguments['sessionType'], 1);
          expect(f.sends, 0);
        } finally {
          await f.closeRepository();
        }
      },
    );
  }
  test(
    'actual contact conversation accepts only the exact ordinary single target',
    () async {
      final f = ChatCardFixture();
      await f.startRepository();
      try {
        expect(
          await f.contacts.conversation('friend-54', checkSource: () {}),
          'single-alice-friend-54',
        );
        expect(f.conversations, 1);
        expect(f.sends, 0);
      } finally {
        await f.closeRepository();
      }
    },
  );
  for (final changed in [
    'duplicate group',
    'foreign group',
    'duplicate self',
    'foreign self group',
    'foreign self actor',
    'missing self',
    'unknown role',
  ]) {
    test(
      'actual group profile rejects $changed instead of lending a management role',
      () async {
        final f = ChatCardFixture();
        await f.startRepository();
        try {
          const group = {
            'groupID': 'group-a',
            'groupName': '原群',
            'groupType': 2,
          };
          const self = {
            'groupID': 'group-a',
            'userID': 'alice',
            'nickname': '本人',
            'roleLevel': 60,
          };
          if (changed == 'duplicate group') {
            f.groupResponse = [
              group,
              {...group},
            ];
          }
          if (changed == 'foreign group') {
            f.groupResponse = [
              {...group, 'groupID': 'different-group'},
            ];
          }
          if (changed == 'duplicate self') {
            f.groupSelfResponse = [
              self,
              {...self},
            ];
          }
          if (changed == 'foreign self group') {
            f.groupSelfResponse = [
              {...self, 'groupID': 'different-group'},
            ];
          }
          if (changed == 'foreign self actor') {
            f.groupSelfResponse = [
              {...self, 'userID': 'different-user'},
            ];
          }
          if (changed == 'missing self') {
            f.groupSelfResponse = [];
          }
          if (changed == 'unknown role') {
            f.groupSelfResponse = [
              {...self, 'roleLevel': 600},
            ];
          }
          await expectLater(
            f.groups.profile('group-a', checkSource: () {}),
            throwsA(
              isA<AppFailure>().having(
                (failure) => failure.kind,
                'kind',
                FailureKind.invalidResponse,
              ),
            ),
          );
          expect(f.friendRequests, 0);
          expect(f.conversations, 0);
          if (changed == 'duplicate group' || changed == 'foreign group') {
            expect(
              f.sdk.calls.where((v) => v.method == 'isJoinGroup'),
              isEmpty,
            );
            expect(
              f.sdk.calls.where((v) => v.method == 'getGroupMembersInfo'),
              isEmpty,
            );
          } else {
            final request = f.sdk.calls.singleWhere(
              (v) => v.method == 'getGroupMembersInfo',
            );
            expect(request.arguments['groupID'], 'group-a');
            expect(request.arguments['userIDList'], ['alice']);
          }
        } finally {
          await f.closeRepository();
        }
      },
    );
  }
  for (final role in [20, 60, 100]) {
    test(
      'actual group profile preserves exact role $role in the original group',
      () async {
        final f = ChatCardFixture();
        await f.startRepository();
        try {
          f.groupRole = role;
          final profile = await f.groups.profile('group-a', checkSource: () {});
          expect(profile.summary.id, 'group-a');
          expect(profile.joined, isTrue);
          expect(profile.me?.id, 'alice');
          expect(profile.me?.role, role);
          expect(profile.canManage, role == 60 || role == 100);
        } finally {
          await f.closeRepository();
        }
      },
    );
  }
  test('actual group profile outside the group preserves read-only metadata without a self lookup', () async {
    final f = ChatCardFixture();
    await f.startRepository();
    try {
      f.groupJoined = false;
      final profile = await f.groups.profile('group-a', checkSource: () {});
      expect(profile.summary.id, 'group-a');
      expect(profile.joined, isFalse);
      expect(profile.me, isNull);
      expect(profile.canManage, isFalse);
      expect(
        f.sdk.calls.where((v) => v.method == 'getGroupMembersInfo'),
        isEmpty,
      );
    } finally {
      await f.closeRepository();
    }
  });
}
