import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';
import 'package:imapp/features/chat/presentation/chat_controller.dart';

import 'group_revoke_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  for (final entry in <String, (int, int)>{
    'admin removes an ordinary sender': (60, 20),
    'owner removes an administrator sender': (100, 60),
    'owner removes an ordinary sender': (100, 20),
  }.entries) {
    test(
      'actual standard revoke ${entry.key} with exact CID and original sender',
      () async {
        final f = GroupRevokeFixture()
          ..actorRole = entry.value.$1
          ..senderRole = entry.value.$2;
        try {
          await f.start();
          final intent = f.capture();
          await f.repository.inspectRevoke(intent);
          final result = await f.controller.executeRevoke(
            intent,
            isCurrent: () => f.sourceValid,
          );
          expect(result.kind, ChatRevokeKind.acknowledged);
          expect(f.revokes, 1);
          final call = f.sdk.calls.singleWhere(
            (call) => call.method == 'revokeMessage',
          );
          expect(call.arguments['conversationID'], f.cid);
          expect(call.arguments['clientMsgID'], 'moderated-source');
          expect(f.controller.messages, isEmpty);
          expect(f.sdk.messages, isEmpty);
        } finally {
          await f.closeRepository();
        }
      },
    );
  }

  test(
    'actual ordinary single owner message keeps its existing revoke channel',
    () async {
      final f = GroupRevokeFixture();
      try {
        await f.start(group: false, self: true);
        final intent = f.capture();
        await f.repository.inspectRevoke(intent);
        expect(
          (await f.repository.executeRevoke(intent)).kind,
          ChatRevokeKind.acknowledged,
        );
        expect(f.revokes, 1);
        expect(f.roleReads, 0);
      } finally {
        await f.closeRepository();
      }
    },
  );

  test(
    'a muted administrator is not denied moderation by message send admission',
    () async {
      final f = GroupRevokeFixture()..actorMuted = true;
      try {
        await f.start();
        final intent = f.capture();
        await f.repository.inspectRevoke(intent);
        expect(
          (await f.repository.executeRevoke(intent)).kind,
          ChatRevokeKind.acknowledged,
        );
        expect(f.revokes, 1);
        expect(
          f.sdk.calls.where((call) => call.method == 'sendMessage'),
          isEmpty,
        );
        expect(
          f.sdk.calls.where((call) => call.method == 'getGroupMembersInfo'),
          hasLength(2),
        );
      } finally {
        await f.closeRepository();
      }
    },
  );

  for (final timestamp in ['over two minutes', 'missing', 'zero']) {
    test(
      'historical moderation with $timestamp time still uses exact fresh roles and original source',
      () async {
        final f = GroupRevokeFixture();
        try {
          await f.start();
          if (timestamp == 'missing') {
            f.raw.remove('sendTime');
          } else {
            f.raw['sendTime'] = timestamp == 'zero'
                ? 0
                : f.base.clock.millisecondsSinceEpoch - 10 * 60 * 1000;
          }
          await f.controller.refresh();
          final intent = f.capture();
          await f.repository.inspectRevoke(intent);
          expect(
            (await f.repository.executeRevoke(intent)).kind,
            ChatRevokeKind.acknowledged,
          );
          expect(f.roleReads, 2);
          expect(f.revokes, 1);
          expect(
            f.sdk.calls
                .singleWhere((call) => call.method == 'revokeMessage')
                .arguments['clientMsgID'],
            'moderated-source',
          );
        } finally {
          await f.closeRepository();
        }
      },
    );
  }

  test('a readable legacy call summary is excluded before moderation capture and dispatch', () async {
    final f = GroupRevokeFixture();
    try {
      await f.start(call: true);
      final message = f.controller.messages.single;
      expect(f.repository.canView(message), true);
      expect(f.repository.canRevokeSource(message), false);
      expect(f.capture, throwsA(isA<AppFailure>()));
      expect(f.revokes, 0);
      expect(f.roleReads, 0);
    } finally {
      await f.closeRepository();
    }
  });

  test('ordinary single chat cannot revoke the other participant through the repository', () async {
    final f = GroupRevokeFixture();
    try {
      await f.start(group: false);
      final result = await f.repository.executeRevoke(f.capture());
      expect(result.kind, ChatRevokeKind.notDispatched);
      expect(f.revokes, 0);
      expect(f.roleReads, 0);
    } finally {
      await f.closeRepository();
    }
  });

  for (final entry in <String, (int, int)>{
    'ordinary actor': (20, 20),
    'equal administrators': (60, 60),
    'administrator targeting owner': (60, 100),
    'conflicting equal owners': (100, 100),
    'unknown actor role': (0, 20),
    'unknown sender role': (100, 0),
  }.entries) {
    test(
      'fresh actual member roles reject ${entry.key} before revoke dispatch',
      () async {
        final f = GroupRevokeFixture()
          ..actorRole = entry.value.$1
          ..senderRole = entry.value.$2;
        try {
          await f.start();
          f.raw.remove('sendTime');
          await f.controller.refresh();
          final result = await f.repository.executeRevoke(f.capture());
          expect(result.kind, ChatRevokeKind.notDispatched);
          expect(f.revokes, 0);
          expect(f.sdk.messages.single['clientMsgID'], 'moderated-source');
        } finally {
          await f.closeRepository();
        }
      },
    );
  }

  for (final changed in [
    'missing actor',
    'left sender',
    'duplicate actor',
    'foreign group',
    'foreign member',
  ]) {
    test(
      'exact membership rejects $changed without treating an absent sender as ordinary',
      () async {
        final f = GroupRevokeFixture();
        try {
          await f.start();
          final actor = <String, dynamic>{
            'userID': 'alice',
            'groupID': 'group-a',
            'roleLevel': 60,
          };
          final sender = <String, dynamic>{
            'userID': 'bob',
            'groupID': 'group-a',
            'roleLevel': 20,
          };
          f.members = switch (changed) {
            'missing actor' => [sender],
            'left sender' => [actor],
            'duplicate actor' => [
              actor,
              {...actor},
            ],
            'foreign group' => [
              actor,
              {...sender, 'groupID': 'other-group'},
            ],
            _ => [
              actor,
              {...sender, 'userID': 'carol'},
            ],
          };
          expect(
            (await f.repository.executeRevoke(f.capture())).kind,
            ChatRevokeKind.notDispatched,
          );
          expect(f.revokes, 0);
        } finally {
          await f.closeRepository();
        }
      },
    );
  }

  for (final changed in [
    'legacy session type',
    'actor left group',
    'dismissed group',
  ]) {
    test(
      'actual group admission refuses $changed and never invents server support',
      () async {
        final f = GroupRevokeFixture();
        try {
          await f.start(sessionType: changed == 'legacy session type' ? 2 : 3);
          f.joined = changed != 'actor left group';
          f.dismissed = changed == 'dismissed group';
          final intent = f.capture();
          expect(
            (await f.repository.executeRevoke(intent)).kind,
            ChatRevokeKind.notDispatched,
          );
          expect(f.revokes, 0);
          expect(intent.unsupported, changed == 'legacy session type');
        } finally {
          await f.closeRepository();
        }
      },
    );
  }

  for (final changed in [
    'CID',
    'sender',
    'message ID',
    'kind',
    'duplicate message',
    'metadata group',
  ]) {
    test(
      'fresh exact source rejects changed $changed before native revoke',
      () async {
        final f = GroupRevokeFixture();
        try {
          await f.start();
          final intent = f.capture();
          switch (changed) {
            case 'CID':
              f.findCid = 'other-cid';
            case 'sender':
              f.findRows = [
                {...f.raw, 'sendID': 'carol'},
              ];
            case 'message ID':
              f.findRows = [
                {...f.raw, 'clientMsgID': 'other-id'},
              ];
            case 'kind':
              f.findRows = [
                {...f.raw, 'contentType': 113},
              ];
            case 'duplicate message':
              f.findRows = [
                f.raw,
                {...f.raw},
              ];
            case 'metadata group':
              f.conversationRows = [
                {...f.metadata, 'groupID': 'other-group'},
              ];
          }
          expect(
            (await f.repository.executeRevoke(intent)).kind,
            ChatRevokeKind.notDispatched,
          );
          expect(f.revokes, 0);
        } finally {
          await f.closeRepository();
        }
      },
    );
  }

  test('final actual role reread refuses a downgrade after confirmation inspection', () async {
    final f = GroupRevokeFixture();
    try {
      await f.start();
      final intent = f.capture();
      await f.repository.inspectRevoke(intent);
      f.actorRole = 20;
      final result = await f.repository.executeRevoke(intent);
      expect(result.kind, ChatRevokeKind.notDispatched);
      expect(result.failure?.kind, FailureKind.permissionDenied);
      expect(f.roleReads, 2);
      expect(f.revokes, 0);
    } finally {
      await f.closeRepository();
    }
  });

  test('actual group event retires a captured old authority even before its next read', () async {
    final f = GroupRevokeFixture();
    try {
      await f.start();
      final intent = f.capture();
      await f.repository.inspectRevoke(intent);
      await f.memberEvent();
      expect(f.controller.revokeCurrent(intent), false);
      await expectLater(
        f.repository.executeRevoke(intent),
        throwsA(isA<AppFailure>()),
      );
      expect(f.revokes, 0);
      expect(f.roleReads, 1);
    } finally {
      await f.closeRepository();
    }
  });

  test('source retirement during actual roles prevents the subsequent native mutation', () async {
    final f = GroupRevokeFixture();
    try {
      await f.start();
      final entered = Completer<void>(), release = f.defer();
      f.beforeRoles = () async {
        entered.complete();
        await release.future;
      };
      final pending = f.repository.executeRevoke(f.capture());
      await entered.future;
      f.sourceValid = false;
      release.complete();
      expect((await pending).kind, ChatRevokeKind.notDispatched);
      expect(f.revokes, 0);
    } finally {
      await f.closeRepository();
    }
  });

  test('real SessionCoordinator owner replacement during member IO prevents revoke and drains the original call', () async {
    final f = GroupRevokeFixture();
    try {
      await f.start();
      final entered = Completer<void>(), release = f.defer();
      f.beforeRoles = () async {
        entered.complete();
        await release.future;
      };
      final pending = f.repository.executeRevoke(f.capture());
      await entered.future;
      final generation = f.sdk.session.state.generation;
      final changing = f.base.changeOwner(user: 'carol');
      expect(f.repository.current, false);
      expect(f.sdk.session.state.generation, greaterThan(generation));
      release.complete();
      expect((await pending).kind, ChatRevokeKind.notDispatched);
      await changing;
      expect(f.revokes, 0);
      expect(f.sdk.session.state.credentials?.userId, 'carol');
    } finally {
      await f.closeRepository();
    }
  });

  test('clear while actual message read is pending cannot lend a later history cut', () async {
    final f = GroupRevokeFixture();
    try {
      await f.start();
      final entered = Completer<void>(), release = f.defer();
      f.beforeFind = () async {
        entered.complete();
        await release.future;
      };
      final pending = f.repository.executeRevoke(f.capture());
      await entered.future;
      final clearing = f.sdk.gateway.clearConversationMessages(
        f.repository.generation,
        f.cid,
      );
      release.complete();
      expect((await pending).kind, ChatRevokeKind.notDispatched);
      await clearing;
      expect(f.revokes, 0);
      expect(f.roleReads, 0);
    } finally {
      await f.closeRepository();
    }
  });

  test('synchronous admission blocks a second repository while the original revoke is pending', () async {
    final f = GroupRevokeFixture();
    ChatController? other;
    try {
      await f.start();
      other = ChatController(
        ChatRepository(
          gateway: f.sdk.gateway,
          session: f.sdk.session,
          conversationId: f.cid,
          now: () => f.base.clock,
        ),
      );
      await other.start();
      final first = f.capture();
      final second = other.captureRevoke(
        other.messages.single,
        access: f.sdk.gateway.historyAccess(
          other.repository.generation,
          conversationId: f.cid,
        ),
        isCurrent: () => true,
      );
      final entered = Completer<void>(), release = f.defer();
      f.beforeRoles = () async {
        entered.complete();
        await release.future;
      };
      final pending = f.repository.executeRevoke(first);
      await entered.future;
      expect(
        (await other.repository.executeRevoke(second)).kind,
        ChatRevokeKind.busy,
      );
      expect(f.revokes, 0);
      release.complete();
      expect((await pending).kind, ChatRevokeKind.acknowledged);
      expect(f.revokes, 1);
    } finally {
      other?.dispose();
      await f.closeRepository();
    }
  });

  test('dispatched native1002 stays unknown across same-owner repositories and only permits reads', () async {
    final f = GroupRevokeFixture()..nativeFailure = true;
    ChatController? other;
    try {
      await f.start();
      final intent = f.capture();
      expect(
        (await f.repository.executeRevoke(intent)).kind,
        ChatRevokeKind.unconfirmed,
      );
      expect(intent.unconfirmed, true);
      expect(
        (await f.repository.executeRevoke(intent)).kind,
        ChatRevokeKind.unconfirmed,
      );
      other = ChatController(
        ChatRepository(
          gateway: f.sdk.gateway,
          session: f.sdk.session,
          conversationId: f.cid,
          now: () => f.base.clock,
        ),
      );
      await other.start();
      final next = other.captureRevoke(
        other.messages.single,
        access: f.sdk.gateway.historyAccess(
          other.repository.generation,
          conversationId: f.cid,
        ),
        isCurrent: () => true,
      );
      expect(
        (await other.repository.executeRevoke(next)).kind,
        ChatRevokeKind.unconfirmed,
      );
      final oldReads = f.finds;
      expect(await other.repository.reviewRevoke(next), true);
      expect(f.finds, oldReads + 1);
      expect(f.revokes, 1);
      expect(f.sdk.messages, hasLength(1));
    } finally {
      other?.dispose();
      await f.closeRepository();
    }
  });

  test('post-commit callback failure and an absent original are never relabeled as an ACK', () async {
    final f = GroupRevokeFixture()
      ..nativeFailure = true
      ..committedBeforeFailure = true;
    try {
      await f.start();
      final intent = f.capture();
      expect(
        (await f.repository.executeRevoke(intent)).kind,
        ChatRevokeKind.unconfirmed,
      );
      expect(await f.repository.reviewRevoke(intent), false);
      expect(intent.acknowledged, false);
      expect(intent.unconfirmed, true);
      expect(
        (await f.repository.executeRevoke(intent)).kind,
        ChatRevokeKind.unconfirmed,
      );
      expect(f.revokes, 1);
    } finally {
      await f.closeRepository();
    }
  });

  test('an unknown revoke locks only its original CID and message while a different explicit target can complete', () async {
    final f = GroupRevokeFixture()..nativeFailure = true;
    try {
      await f.start();
      final original = f.capture();
      expect(
        (await f.repository.executeRevoke(original)).kind,
        ChatRevokeKind.unconfirmed,
      );
      f.nativeFailure = false;
      f.sdk.messages.add({...f.raw, 'clientMsgID': 'different-source'});
      await f.controller.refresh();
      final next = f.controller.captureRevoke(
        f.controller.messages.singleWhere(
          (message) => message.id == 'different-source',
        ),
        access: f.sdk.gateway.historyAccess(
          f.repository.generation,
          conversationId: f.cid,
        ),
        isCurrent: () => f.sourceValid,
      );
      expect(
        (await f.controller.executeRevoke(
          next,
          isCurrent: () => f.sourceValid,
        )).kind,
        ChatRevokeKind.acknowledged,
      );
      expect(f.revokes, 2);
      expect(original.unconfirmed, true);
      expect(f.sdk.messages.single['clientMsgID'], 'moderated-source');
      expect(
        f.sdk.calls
            .where((call) => call.method == 'revokeMessage')
            .map((call) => call.arguments['clientMsgID'])
            .toList(),
        ['moderated-source', 'different-source'],
      );
    } finally {
      await f.closeRepository();
    }
  });

  test('late actual native ACK settles the started operation without refreshing a retired view', () async {
    final f = GroupRevokeFixture();
    try {
      await f.start();
      final entered = Completer<void>(), release = f.defer();
      f.beforeNative = () async {
        entered.complete();
        await release.future;
      };
      final before = f.controller.messages;
      final pending = f.controller.executeRevoke(
        f.capture(),
        isCurrent: () => f.sourceValid,
      );
      await entered.future;
      f.sourceValid = false;
      release.complete();
      expect((await pending).kind, ChatRevokeKind.acknowledged);
      expect(f.controller.messages, same(before));
      expect(f.revokes, 1);
      expect(f.sdk.messages, isEmpty);
    } finally {
      await f.closeRepository();
    }
  });
}
