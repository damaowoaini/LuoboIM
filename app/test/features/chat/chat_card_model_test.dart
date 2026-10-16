import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/data/message_mapper.dart';
import 'package:imapp/features/chat/domain/chat_card.dart';
import 'package:imapp/features/chat/domain/chat_message.dart';

import '../../core/im/sdk_harness.dart';

Message wire({
  String id = 'card',
  String uid = 'exact-uid',
  String name = '联系人',
}) => Message.fromJson({
  ...SdkHarness.message(id: id, text: ''),
  'contentType': MessageType.card,
  'cardElem': {
    'userID': uid,
    'nickname': name,
    'faceURL': 'https://untrusted.invalid/avatar?token=opaque',
    'ex': '{"url":"javascript:never"}',
  },
});

void main() {
  test('standard108 maps a typed exact UID hint and discards received faceURL and ex actions', () {
    final value = mapMessage(wire(), 'alice');
    expect(value.kind, ChatMessageKind.card);
    expect(value.card?.userId, 'exact-uid');
    expect(value.card?.name, '联系人');
    expect(value.media, isNull);
    expect(value.thumbnail, isNull);
    expect(messagePreview(wire()), '[名片] 联系人');
  });
  test(
    'card delivery and local media clones preserve the original typed identity',
    () {
      final value = mapMessage(wire(), 'alice');
      expect(
        identical(value.withDelivery(MessageDelivery.failed).card, value.card),
        isTrue,
      );
      expect(
        identical(value.withLocalMedia('/owned/file').card, value.card),
        isTrue,
      );
    },
  );
  test('same display names retain distinct card UIDs and empty names show the complete UID', () {
    final first = ChatCard.parse(userId: 'friend-0', name: '同名好友')!;
    final second = ChatCard.parse(userId: 'friend-1', name: '同名好友')!;
    expect(first.userId, isNot(second.userId));
    expect(first, isNot(second));
    expect(
      ChatCard.parse(userId: 'full-identity', name: '')!.name,
      'full-identity',
    );
  });
  test(
    'bounded card parser rejects ambiguous or unsafe identities and labels',
    () {
      for (final uid in [
        '',
        ' a',
        'a ',
        'a b',
        'a\n',
        'a\u0085',
        'a\u061c',
        'a\u200e',
        'a\u202e',
        'a' * 129,
      ]) {
        expect(
          ChatCard.parse(userId: uid, name: '名字'),
          isNull,
          reason: 'invalid UID',
        );
      }
      for (final name in [
        'a\n',
        'a\u009f',
        'a\u200f',
        'a\u2066',
        '😀' * 129,
        String.fromCharCode(0xd800),
      ]) {
        expect(
          ChatCard.parse(userId: 'uid', name: name),
          isNull,
          reason: 'invalid name',
        );
      }
      expect(ChatCard.parse(userId: 123, name: '名字'), isNull);
      expect(ChatCard.parse(userId: 'uid', name: true), isNull);
      expect(ChatCard.parse(userId: 'uid', name: '名字', faceUrl: {}), isNull);
      expect(ChatCard.parse(userId: 'uid', name: '名字', extra: []), isNull);
      expect(
        ChatCard.parse(userId: 'uid', name: '名字', faceUrl: 'x' * 2049),
        isNull,
      );
      expect(
        ChatCard.parse(userId: 'uid', name: '名字', extra: 'x' * 4097),
        isNull,
      );
      expect(
        ChatCard.parse(userId: 'uid', name: '名字', extra: '😀' * 2100),
        isNull,
      );
    },
  );
  test(
    'URL-looking card names and exact UIDs remain literal text identity hints',
    () {
      for (final text in [
        'https:example',
        'www.example',
        'javascript:alert',
        'data:opaque',
        'file:path',
      ]) {
        final value = ChatCard.parse(userId: text, name: text)!;
        expect(value.name, text);
        expect(value.userId, text);
      }
    },
  );
  test(
    'malformed native card never leaks an untrusted nickname through previews',
    () {
      final value = wire(uid: 'a b', name: 'bad identity');
      expect(mapMessage(value, 'alice').card, isNull);
      expect(messagePreview(value), '[名片] 资料暂不可用');
    },
  );
  test('nested merger preserves each standard108 identity without treating ex as a child message', () {
    final root = Message.fromJson({
      ...SdkHarness.message(id: 'merge', text: ''),
      'contentType': MessageType.merger,
      'mergeElem': {
        'multiMessage': [
          wire(uid: 'a', name: '同名').toJson(),
          wire(id: 'card2', uid: 'b', name: '同名').toJson(),
        ],
      },
    });
    final value = mapMessage(root, 'alice');
    expect(value.mergedMessages.map((v) => v.card?.userId), ['a', 'b']);
    expect(value.mergedMessages.every((v) => v.mergedMessages.isEmpty), isTrue);
  });
}
