import 'dart:async';
import 'dart:convert';

import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';
import 'package:imapp/features/chat/data/message_mapper.dart';
import 'package:imapp/features/chat/domain/chat_message.dart';
import 'package:imapp/features/conversation/data/conversation_repository.dart';

import '../../core/im/sdk_harness.dart';

Message legacyCustom(
  int type,
  Map<String, Object?> data, {
  String id = 'legacy-summary',
  String description = '',
  bool private = false,
}) => Message.fromJson({
  'clientMsgID': id,
  'contentType': MessageType.custom,
  'sendID': 'bob',
  'status': MessageStatus.succeeded,
  'attachedInfoElem': {'isPrivateChat': private},
  'customElem': {
    'data': jsonEncode({'customType': type, 'data': data}),
    'extension': 'opaque-vendor-extension',
    'description': description,
  },
});

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('tag903 text uses the actual legacy textElem payload', () {
    final message = legacyCustom(903, {
      'textElem': {'content': '合成标签通知'},
      'soundElem': null,
    });
    expect(mapMessage(message, 'alice').text, '合成标签通知');
    expect(messagePreview(message), '合成标签通知');
    expect(mapMessage(message, 'alice').kind, ChatMessageKind.custom);
  });

  test('tag903 sound has a restricted audio projection without changing custom identity', () {
    final message = legacyCustom(903, {
      'soundElem': {
        'sourceUrl': 'https://assets.example.com/notice.m4a',
        'soundPath': '/private/never-open-this',
        'duration': 12,
      },
    });
    final before = jsonEncode(message.toJson());
    final mapped = mapMessage(message, 'alice');
    expect(mapped.text, '[标签语音] 12 秒');
    expect(mapped.kind, ChatMessageKind.custom);
    expect(mapped.tagAudio?.durationSeconds, 12);
    expect(
      mapped.playableAudio.toString(),
      'https://assets.example.com/notice.m4a',
    );
    expect(mapped.media, isNull);
    expect(mapped.playableMedia, isNull);
    expect(mapped.localMedia, isNull);
    expect(mapped.canForward, isFalse);
    expect(mapped.withDelivery(MessageDelivery.sent).tagAudio, mapped.tagAudio);
    expect(
      mapped.withLocalMedia('/private/untrusted').playableAudio,
      mapped.playableAudio,
    );
    expect(jsonEncode(message.toJson()), before);
  });

  test('tag903 keeps text priority and rejects unsafe or malformed audio projections', () {
    const source = 'https://assets.example.com/notice.m4a';
    for (final text in <Object>[
      {'content': '原文字通知'},
      {'content': 12},
      'malformed-text-element',
    ]) {
      final mapped = mapMessage(
        legacyCustom(903, {
          'textElem': text,
          'soundElem': {'sourceUrl': source, 'duration': 2},
        }),
        'alice',
      );
      expect(mapped.tagAudio, isNull);
      expect(mapped.playableAudio, isNull);
      if (text is Map && text['content'] is String) {
        expect(mapped.text, '原文字通知');
      }
    }
    for (final sound in <Object?>[
      null,
      false,
      <String, Object?>{},
      {
        'sourceUrl': ['not-a-url'],
      },
      {'sourceUrl': 'http://assets.example.com/notice.m4a'},
      {'sourceUrl': 'file:///private/notice.m4a'},
      {'sourceUrl': 'javascript:alert(1)'},
      {'sourceUrl': 'https://user:password@assets.example.com/notice.m4a'},
      {'sourceUrl': source, 'duration': '12'},
      {'sourceUrl': source, 'duration': -1},
    ]) {
      final raw = legacyCustom(903, {'soundElem': sound});
      final before = jsonEncode(raw.toJson());
      final mapped = mapMessage(raw, 'alice');
      expect(mapped.kind, ChatMessageKind.custom);
      expect(mapped.tagAudio, isNull);
      expect(mapped.playableAudio, isNull);
      expect(mapped.media, isNull);
      expect(mapped.canForward, isFalse);
      expect(jsonEncode(raw.toJson()), before);
    }
    for (final duration in <int?>[null, 0]) {
      final mapped = mapMessage(
        legacyCustom(903, {
          'soundElem': {'sourceUrl': source, 'duration': duration},
        }),
        'alice',
      );
      expect(mapped.playableAudio.toString(), source);
      expect(mapped.tagAudio?.durationSeconds, duration);
    }
  });

  test('meeting905 is readable without manufacturing a join action or URL', () {
    final message = legacyCustom(905, {
      'subject': '合成项目例会',
      'id': 'fixture-meeting-1',
      'inviterUserID': 'untrusted-inviter',
      'start': 1700000000,
      'duration': 1800,
      'url': 'https://untrusted.example.com/join',
    });
    final mapped = mapMessage(message, 'alice');
    expect(mapped.text, contains('[会议邀请] 合成项目例会'));
    expect(mapped.text, contains('会议号：fixture-meeting-1'));
    expect(mapped.text, contains('当前版本未开放从邀请加入会议'));
    expect(mapped.media, isNull);
    expect(mapped.sourceGroupId, isNull);
    expect(mapped.canForward, isFalse);
  });

  test(
    'known910 through913 explain historical relationship and group hints',
    () {
      for (final sample in <(int, String)>[
        (910, '消息已发出，但被对方拒收了'),
        (911, '对方开启了好友验证，你还不是对方的好友'),
        (912, '你已被移出群聊'),
        (913, '群已经解散'),
      ]) {
        final mapped = mapMessage(legacyCustom(sample.$1, {}), 'alice');
        expect(mapped.text, sample.$2);
        expect(mapped.kind, ChatMessageKind.custom);
        expect(mapped.canForward, isFalse);
      }
    },
  );

  test(
    'hint summaries never mutate native message type or authority fields',
    () {
      final message = legacyCustom(912, {'isNotInGroup': true});
      final before = jsonEncode(message.toJson());
      final mapped = mapMessage(message, 'alice');
      expect(mapped.kind, isNot(ChatMessageKind.system));
      expect(message.contentType, MessageType.custom);
      expect(message.groupID, isNull);
      expect(jsonEncode(message.toJson()), before);
    },
  );

  test(
    'wrong typed known payload fields fall back without string coercion',
    () {
      for (final sample in <(int, Map<String, Object?>)>[
        (
          903,
          {
            'textElem': {'content': 12},
          },
        ),
        (903, {'textElem': 'not-an-element'}),
        (
          905,
          {
            'subject': ['not-a-title'],
            'id': 'fixture',
          },
        ),
        (905, {'subject': '合成例会', 'id': false}),
        (905, {'subject': '合成例会', 'id': '   '}),
      ]) {
        final message = legacyCustom(sample.$1, sample.$2, description: '旧摘要');
        expect(messagePreview(message), '旧摘要');
        expect(mapMessage(message, 'alice').media, isNull);
      }
      final malformedDuration = legacyCustom(903, {
        'soundElem': {'duration': '60'},
      });
      expect(messagePreview(malformedDuration), '[标签语音]（当前版本暂不支持播放）');
    },
  );

  test(
    'unknown904 and future vendor types retain the existing opaque fallback',
    () {
      for (final type in [904, 900, 9999]) {
        final message = legacyCustom(type, {
          'textElem': {'content': '不得自动解析的未知正文'},
        }, description: '业务原摘要');
        expect(messagePreview(message), '业务原摘要');
        expect(mapMessage(message, 'alice').kind, ChatMessageKind.custom);
        expect(messagePreview(legacyCustom(type, {})), '[自定义消息]');
      }
    },
  );

  test('custom data does not manufacture OpenIM privacy metadata', () {
    final mapped = mapMessage(
      legacyCustom(903, {
        'textElem': {'content': '普通通知'},
        'attachedInfoElem': {'isPrivateChat': true},
        'quoteElem': {
          'quoteMessage': {'isPrivateChat': true},
        },
      }),
      'alice',
    );
    expect(mapped.text, '普通通知');
    expect(mapped.privacy.containsPrivateContent, isFalse);
  });

  test(
    'real private root still masks every known custom conversation preview',
    () {
      for (final type in [903, 905, 910, 911, 912, 913]) {
        final message = legacyCustom(type, {
          'textElem': {'content': '合成私密通知'},
          'subject': '合成私密会议',
          'id': 'fixture',
        }, private: true);
        final mapped = mapMessage(message, 'alice');
        expect(messagePreview(message), '[阅后即焚消息]');
        expect(mapped.privacy.containsPrivateContent, isTrue);
        expect(mapped.canForward, isFalse);
      }
    },
  );

  test(
    'quote summaries use the same custom decoder without copying opaque data',
    () {
      final child = legacyCustom(903, {
        'textElem': {'content': '合成标签通知'},
      });
      final quote = Message.fromJson({
        'contentType': MessageType.quote,
        'quoteElem': {'text': '回复正文', 'quoteMessage': child.toJson()},
      });
      expect(mapMessage(quote, 'alice').quotePreview, '合成标签通知');
    },
  );

  test(
    'standard107 nested custom summaries keep the original joint privacy',
    () {
      final child = legacyCustom(903, {
        'textElem': {'content': '合成嵌套通知'},
      });
      final merged = Message.fromJson({
        'clientMsgID': 'merged-fixture',
        'contentType': MessageType.merger,
        'attachedInfoElem': {'isPrivateChat': true},
        'mergeElem': {
          'multiMessage': [child.toJson()],
        },
      });
      final mapped = mapMessage(merged, 'alice');
      expect(mapped.mergedMessages.single.text, '合成嵌套通知');
      expect(
        mapped.mergedMessages.single.privacy.containsPrivateContent,
        isTrue,
      );
      expect(mapped.mergedMessages.single.canForward, isFalse);
    },
  );

  test('conversation list projection shares the custom summary decoder', () {
    final message = legacyCustom(913, {});
    final item = mapConversation(
      ConversationInfo(
        conversationID: 'sg_fixture',
        groupID: 'fixture',
        latestMsg: message,
      ),
    );
    expect(item.preview, '群已经解散');
  });

  test('all known summaries preserve full original protocol JSON', () {
    for (final type in [901, 902, 903, 905, 910, 911, 912, 913]) {
      final message = legacyCustom(type, {
        'textElem': {'content': '合成标签正文'},
        'subject': '合成例会',
        'id': 'fixture',
        'url': 'https://assets.example.com/emoji.gif',
        'vendorOpaque': {'unchanged': true},
      });
      final before = jsonEncode(message.toJson());
      mapMessage(message, 'alice');
      messagePreview(message);
      expect(jsonEncode(message.toJson()), before);
    }
  });

  test('oversized and other top-level custom namespaces stay opaque', () {
    final oversized = legacyCustom(903, {
      'textElem': {'content': 'x' * 65536},
    });
    expect(messagePreview(oversized), '[自定义消息]');
    for (final type in [
      MessageType.customMsgNotTriggerConversation,
      MessageType.customMsgOnlineOnly,
    ]) {
      final raw = legacyCustom(913, {}).toJson()..['contentType'] = type;
      expect(messagePreview(Message.fromJson(raw)), '[自定义消息]');
    }
  });

  test(
    'native history codec exposes legacy text and meeting summaries',
    () async {
      final harness = SdkHarness();
      harness.conversations.add(
        SdkHarness.conversation(id: 'sg_fixture', group: true),
      );
      harness.messages.addAll([
        legacyCustom(903, {
          'textElem': {'content': '合成标签通知'},
        }, id: 'tag').toJson(),
        legacyCustom(905, {
          'subject': '合成例会',
          'id': 'fixture',
        }, id: 'meeting').toJson(),
      ]);
      await harness.start();
      final repository = ChatRepository(
        gateway: harness.gateway,
        session: harness.session,
        conversationId: 'sg_fixture',
      );
      try {
        await repository.conversation();
        final page = await repository.history();
        expect(page.messages.map((item) => item.id), ['tag', 'meeting']);
        expect(page.messages.first.text, '合成标签通知');
        expect(page.messages.last.text, contains('[会议邀请] 合成例会'));
        expect(
          harness.calls.where((call) => call.method == 'sendMessage'),
          isEmpty,
        );
      } finally {
        repository.clear();
        await harness.dispose();
      }
    },
  );

  test('late custom history cannot enter a session that has ended', () async {
    final harness = SdkHarness();
    final entered = Completer<void>();
    final returned = Completer<String>();
    await harness.start();
    final repository = ChatRepository(
      gateway: harness.gateway,
      session: harness.session,
      conversationId: 'sg_fixture',
    );
    harness.overrideCall = (call) async {
      if (call.method == 'getAdvancedHistoryMessageList') {
        entered.complete();
        return returned.future;
      }
      return harness.respond(call);
    };
    try {
      final history = repository.history();
      final rejected = expectLater(history, throwsA(isA<AppFailure>()));
      await entered.future.timeout(const Duration(seconds: 2));
      final exit = harness.session.signOut();
      returned.complete(
        jsonEncode({
          'messageList': [
            legacyCustom(903, {
              'textElem': {'content': '旧账号通知'},
            }).toJson(),
          ],
          'errCode': 0,
          'isEnd': true,
        }),
      );
      await rejected;
      await exit;
      expect(repository.current, isFalse);
    } finally {
      if (!returned.isCompleted) returned.complete('{}');
      repository.clear();
      await harness.dispose();
    }
  });

  test('late conversation lookup cannot enter a session whose generation ended before native disconnect', () async {
    final harness = SdkHarness();
    final entered = Completer<void>();
    final returned = Completer<String>();
    await harness.start();
    final repository = ChatRepository(
      gateway: harness.gateway,
      session: harness.session,
      conversationId: 'sg_fixture',
    );
    final generation = harness.session.state.generation;
    final nativeLease = harness.gateway.historyAccess(
      generation,
      conversationId: 'sg_fixture',
    );
    harness.overrideCall = (call) async {
      if (call.method == 'getMultipleConversation') {
        entered.complete();
        return returned.future;
      }
      return harness.respond(call);
    };
    try {
      final conversation = repository.conversation();
      final rejected = expectLater(conversation, throwsA(isA<AppFailure>()));
      await entered.future.timeout(const Duration(seconds: 2));
      final exit = harness.session.signOut();
      expect(harness.session.acceptsEvent(generation), isFalse);
      // signOut invalidates its generation synchronously; the native SDK's
      // disconnect is still queued. Do not rely solely on its later fence.
      expect(nativeLease.check, returnsNormally);
      returned.complete(
        jsonEncode([SdkHarness.conversation(id: 'sg_fixture', group: true)]),
      );
      await rejected;
      await exit;
      expect(repository.current, isFalse);
    } finally {
      if (!returned.isCompleted) returned.complete('[]');
      repository.clear();
      await harness.dispose();
    }
  });
}
