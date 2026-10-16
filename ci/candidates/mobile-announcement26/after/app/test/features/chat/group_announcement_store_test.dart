import 'dart:async';

import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/chat/data/group_announcement_store.dart';
import 'package:imapp/features/conversation/data/conversation_repository.dart';
import 'package:imapp/features/conversation/domain/conversation.dart';
import 'package:imapp/features/group/domain/group.dart';

GroupProfile profile({
  String text = '原公告',
  int version = 1,
  bool joined = true,
  int role = 20,
  String group = 'group-a',
}) => GroupProfile(
  summary: GroupSummary(
    id: group,
    name: '原群',
    ownerId: 'owner',
    members: 3,
    sessionType: 3,
  ),
  joined: joined,
  me: GroupMember(id: 'alice', name: '我', role: role, mutedUntil: 0),
  announcement: text,
  announcementUpdatedAt: version,
  announcementPublisherId: 'owner',
  introduction: '',
  verification: 0,
  hideMemberProfiles: false,
  preventMemberFriends: false,
);

void main() {
  test('SDK hint4 is announcement while true at1to3 and preview stripping preserve independent identity', () {
    for (var value = 0; value <= 4; ++value) {
      final mapped = mapConversation(
        ConversationInfo.fromJson({
          'conversationID': 'group-cid',
          'groupID': 'group-a',
          'conversationType': 3,
          'unreadCount': 2,
          'groupAtType': value,
        }),
      );
      expect(mapped.mentions, const {1, 2, 3}.contains(value));
      expect(mapped.groupHint, ConversationHint.values[value]);
      expect(mapped.withoutMessagePreview().groupHint, mapped.groupHint);
      expect(mapped.unread, 2);
    }
    expect(conversationHint(99), ConversationHint.unknown);
  });
  test('fresh snapshot bounds reject controls bidi malformed unicode and unavailable membership while URL text stays plain', () {
    final valid = GroupAnnouncementSnapshot.fromProfile(
      profile(text: 'www.example.com 仅为公告文字\n第二行'),
    );
    expect(valid.text, 'www.example.com 仅为公告文字\n第二行');
    for (final invalid in [
      '\u0000',
      '\u0085',
      '\u202e',
      '\u200f',
      '\ud800',
      'a' * 10001,
    ]) {
      expect(
        () => GroupAnnouncementSnapshot.fromProfile(profile(text: invalid)),
        throwsA(isA<AppFailure>()),
      );
    }
    expect(
      () => GroupAnnouncementSnapshot.fromProfile(profile(joined: false)),
      throwsA(isA<AppFailure>()),
    );
    expect(
      () => GroupAnnouncementSnapshot.fromProfile(profile(version: -1)),
      throwsA(isA<AppFailure>()),
    );
  });
  test('acknowledged exact-version dismissal survives reopening without consuming a republished version or another owner group', () async {
    final values = <String, bool>{};
    final store = GroupAnnouncementStore(
      readBool: (key) async => values[key],
      writeBool: (key, value) async {
        values[key] = value;
      },
    );
    final first = GroupAnnouncementSnapshot.fromProfile(profile());
    final second = GroupAnnouncementSnapshot.fromProfile(profile(version: 2));
    final other = GroupAnnouncementSnapshot.fromProfile(
      profile(group: 'group-b'),
    );
    final a = 'a' * 64, b = 'b' * 64;
    try {
      expect(await store.dismissed(a, first, current: () => true), isFalse);
      await store.dismiss(a, first, current: () => true);
      expect(await store.dismissed(a, first, current: () => true), isTrue);
      expect(await store.dismissed(a, second, current: () => true), isFalse);
      expect(await store.dismissed(b, first, current: () => true), isFalse);
      expect(await store.dismissed(a, other, current: () => true), isFalse);
      expect(values.keys.single, isNot(contains('原公告')));
      expect(values.keys.single, isNot(contains('group-a')));
    } finally {
      await store.close();
    }
  });
  test('preferences failure leaves the version unacknowledged and one explicit retry persists only a boolean digest key', () async {
    final values = <String, bool>{};
    var fail = true;
    final store = GroupAnnouncementStore(
      readBool: (key) async => values[key],
      writeBool: (key, value) async {
        if (fail) {
          throw StateError('synthetic OS failure');
        }
        values[key] = value;
      },
    );
    final snapshot = GroupAnnouncementSnapshot.fromProfile(profile());
    try {
      await expectLater(
        store.dismiss('a' * 64, snapshot, current: () => true),
        throwsA(isA<AppFailure>()),
      );
      expect(
        await store.dismissed('a' * 64, snapshot, current: () => true),
        isFalse,
      );
      fail = false;
      await store.dismiss('a' * 64, snapshot, current: () => true);
      expect(values.values.single, isTrue);
    } finally {
      await store.close();
    }
  });
  test('queued preference dispatch rechecks its original source before physical acknowledgement', () async {
    final entered = Completer<void>(), reply = Completer<bool?>();
    final writes = <String>[];
    var current = true;
    final store = GroupAnnouncementStore(
      readBool: (_) {
        entered.complete();
        return reply.future;
      },
      writeBool: (key, _) async {
        writes.add(key);
      },
    );
    final snapshot = GroupAnnouncementSnapshot.fromProfile(profile());
    final reading = store.dismissed('a' * 64, snapshot, current: () => current);
    final rejectedRead = expectLater(reading, throwsA(isA<AppFailure>()));
    final saving = store.dismiss('a' * 64, snapshot, current: () => current);
    final rejectedSave = expectLater(saving, throwsA(isA<AppFailure>()));
    await entered.future;
    current = false;
    reply.complete(false);
    await rejectedRead;
    await rejectedSave;
    expect(writes, isEmpty);
    await store.close();
  });
  test('an already dispatched dismissal keeps its original key after source retirement without acknowledging another owner', () async {
    final entered = Completer<void>(), reply = Completer<void>();
    final values = <String, bool>{};
    var current = true;
    final store = GroupAnnouncementStore(
      readBool: (key) async => values[key],
      writeBool: (key, value) async {
        entered.complete();
        await reply.future;
        values[key] = value;
      },
    );
    final snapshot = GroupAnnouncementSnapshot.fromProfile(profile());
    final pending = store.dismiss('a' * 64, snapshot, current: () => current);
    final rejected = expectLater(pending, throwsA(isA<AppFailure>()));
    await entered.future;
    current = false;
    reply.complete();
    await rejected;
    expect(
      values.keys.single,
      GroupAnnouncementStore.keyFor(
        'a' * 64,
        snapshot.groupId,
        snapshot.version,
      ),
    );
    expect(
      await store.dismissed('b' * 64, snapshot, current: () => true),
      isFalse,
    );
    await store.close();
  });
}
