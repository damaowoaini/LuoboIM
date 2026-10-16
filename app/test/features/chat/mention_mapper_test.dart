import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/data/message_mapper.dart';
import 'package:imapp/features/chat/domain/chat_message.dart';
import 'package:imapp/features/chat/domain/chat_mention.dart';

void main() {
  Message message(
    String text,
    List<String> ids,
    List<Map<String, String>> info, {
    Map<String, Object?>? quote,
  }) => Message.fromJson({
    'clientMsgID': 'at',
    'sendID': 'bob',
    'contentType': 106,
    'status': 2,
    'atTextElem': {
      'text': text,
      'atUserList': ids,
      'atUsersInfo': info,
      'quoteMessage': quote,
    },
  });
  test('at sign inside a stable UID maps the exact longest identity rather than a suffix nickname or account', () {
    final mapped = mapMessage(
      message(
        '@legacy@example.test @example.test embedded@legacy@example.test ',
        ['example.test', 'legacy@example.test'],
        [
          {'atUserID': 'legacy@example.test', 'groupNickname': '历史账号'},
        ],
      ),
      'alice',
    );
    expect(mapped.mentions.map((span) => span.identity.userId), [
      'legacy@example.test',
      'example.test',
    ]);
    expect(mapped.mentions.first.identity.name, '历史账号');
  });
  test('received links require actual UID metadata and exact wire occurrences, never a guessed nickname', () {
    final mapped = mapMessage(
      message(
        '@同名 @bob @ghost ',
        ['bob'],
        [
          {'atUserID': 'bob', 'groupNickname': '同名'},
          {'atUserID': 'ghost', 'groupNickname': '同名'},
        ],
      ),
      'alice',
    );
    expect(mapped.mentions, hasLength(1));
    expect(mapped.mentions.single.identity.userId, 'bob');
    expect(
      mapped.text.substring(
        mapped.mentions.single.start,
        mapped.mentions.single.end,
      ),
      '@bob',
    );
  });
  test(
    'all is a group identity and UID prefixes never select the wrong account',
    () {
      final mapped = mapMessage(
        message('@bob-long @bob @AtAllTag ', [
          'bob',
          'bob-long',
          mentionAllId,
        ], []),
        'alice',
      );
      expect(mapped.mentions.map((m) => m.identity.userId), [
        'bob-long',
        'bob',
        mentionAllId,
      ]);
      expect(mapped.mentions.last.identity.all, true);
    },
  );
  test('at with quote preserves actual quote identity and private descendant guard', () {
    final mapped = mapMessage(
      message(
        '@bob ',
        ['bob'],
        [],
        quote: {
          'clientMsgID': 'quoted',
          'contentType': 101,
          'textElem': {'content': 'hidden'},
          'attachedInfoElem': {'isPrivateChat': true},
        },
      ),
      'alice',
    );
    expect(mapped.kind, ChatMessageKind.quote);
    expect(mapped.quoteId, 'quoted');
    expect(mapped.privacy.allowsRetention, false);
    expect(
      mapped
          .withDelivery(MessageDelivery.failed)
          .mentions
          .single
          .identity
          .userId,
      'bob',
    );
  });
  test('a single display-only forwarded at-message keeps original identity and inherited private expiry', () {
    final child = message('@AtAllTag ', [mentionAllId], []).toJson()
      ..['groupID'] = 'original-group'
      ..['attachedInfoElem'] = {
        'isPrivateChat': true,
        'hasReadTime': 1700000000000,
        'burnDuration': 30,
      };
    final wrapper = Message.fromJson({
      'clientMsgID': 'wrapper',
      'contentType': 107,
      'mergeElem': {
        'multiMessage': [child],
      },
    });
    final item = mapMessage(wrapper, 'alice');
    expect(item.forwardedMessage?.sourceGroupId, 'original-group');
    expect(item.forwardedMessage?.mentions.single.identity.all, true);
    expect(
      item.privacy.canViewAt(
        DateTime.fromMillisecondsSinceEpoch(1700000030001),
      ),
      false,
    );
    expect(item.forwardedMessage!.privacy.canRetainAt(DateTime.now()), false);
  });
}
