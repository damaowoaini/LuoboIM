import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';
import 'package:imapp/features/chat/data/message_mapper.dart';
import 'package:imapp/features/chat/domain/chat_message.dart';
import 'package:imapp/features/conversation/data/conversation_repository.dart';

import '../../core/im/sdk_harness.dart';
import 'batch_harness.dart';
import 'chat_card_fixture.dart';

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
  _relationshipHintTests();

  test('tag903 text uses the actual legacy textElem payload', () {
    final message = legacyCustom(903, {
      'textElem': {'content': '合成标签通知'},
      'soundElem': null,
    });
    final before = jsonEncode(message.toJson());
    final mapped = mapMessage(message, 'alice');
    expect(mapped.text, '合成标签通知');
    expect(mapped.tagText, '合成标签通知');
    expect(mapped.legacyCustomType, 903);
    expect(mapped.canUseTextActions, isTrue);
    expect(mapped.canForward, isTrue);
    expect(mapped.tagAudio, isNull);
    expect(mapped.withDelivery(MessageDelivery.sent).canForward, isTrue);
    expect(mapped.withDelivery(MessageDelivery.failed).canForward, isFalse);
    expect(
      mapped.withLocalMedia('/private/untrusted').kind,
      ChatMessageKind.custom,
    );
    expect(jsonEncode(message.toJson()), before);
    expect(messagePreview(message), '合成标签通知');
    expect(mapMessage(message, 'alice').kind, ChatMessageKind.custom);
    for (final content in ['', '   ', '附加字段原文']) {
      final compatible = legacyCustom(903, {
        'textElem': {'content': content, 'language': 'zh', 'revision': 2},
        'pictureElem': {
          'sourcePicture': {
            'url': 'https://assets.example.com/p.png',
            'width': 1,
            'height': 1,
          },
        },
        'videoElem': <String, Object?>{},
        'fileElem': <String, Object?>{},
        'cardElem': <String, Object?>{},
        'locationElem': <String, Object?>{},
        'vendorMetadata': {
          'revision': 3,
          'labels': ['legacy'],
        },
      });
      final raw = jsonEncode(compatible.toJson());
      final projection = mapMessage(compatible, 'alice');
      expect(projection.text, content);
      expect(projection.tagText, content);
      expect(projection.canUseTextActions, isTrue);
      expect(projection.kind, ChatMessageKind.custom);
      expect(projection.canForward, isTrue);
      expect(projection.tagAudio, isNull);
      expect(projection.media, isNull);
      expect(jsonEncode(compatible.toJson()), raw);
    }
    // Native fields do not retype an envelope whose protocol identity is 110.
    final nativeFields = Message.fromJson({
      ...message.toJson(),
      'soundElem': {
        'sourceUrl': 'https://assets.example.com/native.m4a',
        'duration': 2,
      },
      'pictureElem': <String, Object?>{},
    });
    final nativeJSON = jsonEncode(nativeFields.toJson());
    expect(mapMessage(nativeFields, 'alice').kind, ChatMessageKind.custom);
    expect(mapMessage(nativeFields, 'alice').canForward, isTrue);
    expect(mapMessage(nativeFields, 'alice').playableAudio, isNull);
    expect(jsonEncode(nativeFields.toJson()), nativeJSON);
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
    expect(mapped.tagText, isNull);
    expect(mapped.legacyCustomType, 903);
    expect(mapped.canUseTextActions, isFalse);
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
      expect(mapped.canForward, isFalse);
      expect(
        mapped.canUseTextActions,
        text is Map && text['content'] is String,
      );
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
      'inviterNickname': '原邀请人',
      'inviterFaceURL': 'https://assets.example.com/inviter.png',
      'vendorMetadata': {'revision': 2, 'extra': true},
      'start': 1700000000,
      'duration': 1800,
      'url': 'https://untrusted.example.com/join',
    });
    final before = jsonEncode(message.toJson());
    final mapped = mapMessage(message, 'alice');
    expect(mapped.text, contains('[会议邀请] 合成项目例会'));
    expect(mapped.text, contains('会议号：fixture-meeting-1'));
    expect(mapped.text, contains('当前版本未开放从邀请加入会议'));
    expect(mapped.media, isNull);
    expect(mapped.sourceGroupId, isNull);
    expect(mapped.kind, ChatMessageKind.custom);
    expect(mapped.tagTextForwardable, isFalse);
    expect(mapped.tagText, isNull);
    expect(mapped.legacyCustomType, 905);
    expect(mapped.canUseTextActions, isFalse);
    expect(mapped.tagMeeting, (
      inviterUserId: 'untrusted-inviter',
      inviterNickname: '原邀请人',
      inviterFaceUrl: 'https://assets.example.com/inviter.png',
      subject: '合成项目例会',
      id: 'fixture-meeting-1',
      startSeconds: 1700000000,
      durationSeconds: 1800,
    ));
    expect(mapped.canForward, isTrue);
    expect(mapped.withDelivery(MessageDelivery.failed).canForward, isFalse);
    expect(
      mapped.withDelivery(MessageDelivery.sent).tagMeeting,
      mapped.tagMeeting,
    );
    expect(
      mapped.withLocalMedia('/private/untrusted').tagMeeting,
      mapped.tagMeeting,
    );
    expect(jsonEncode(message.toJson()), before);
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

  test(
    'tag903 voice and malformed text cannot bypass the native forwarding gate',
    () async {
      final harness = SdkHarness()
        ..conversations.add(SdkHarness.conversation());
      await harness.start();
      final repository = ChatRepository(
        gateway: harness.gateway,
        session: harness.session,
        conversationId: 'single-alice-bob',
      );
      try {
        await repository.conversation();
        final denied = <Message>[
          for (final content in <Object?>[null, 12, false, <String>[]])
            legacyCustom(903, {
              'textElem': {'content': content},
            }),
          legacyCustom(903, {'textElem': 'invalid-element'}),
          legacyCustom(903, {'textElem': <String, Object?>{}}),
          legacyCustom(903, {'textElem': <String>[]}),
          for (final sound in <Object>[
            false,
            '',
            <String, Object?>{},
            {'sourceUrl': 'https://assets.example.com/a.m4a', 'duration': 2},
          ])
            legacyCustom(903, {
              'textElem': {'content': 'text'},
              'soundElem': sound,
            }),
          legacyCustom(904, {
            'textElem': {'content': 'text'},
          }),
          legacyCustom(905, {
            'textElem': {'content': 'text'},
          }),
          Message.fromJson({
            ...legacyCustom(903, {
              'textElem': {'content': 'text'},
            }).toJson(),
            'customElem': {
              'data': '{',
              'extension': 'opaque',
              'description': 'fallback',
            },
          }),
        ];
        for (var index = 0; index < denied.length; index++) {
          final message = denied[index]..clientMsgID = 'denied-$index';
          final before = jsonEncode(message.toJson());
          expect(mapMessage(message, 'alice').canForward, isFalse);
          final calls = harness.calls.length;
          await expectLater(
            repository.forwardMessageJson(before),
            throwsA(isA<AppFailure>()),
          );
          expect(harness.calls.length, calls);
          expect(jsonEncode(message.toJson()), before);
        }
        expect(
          harness.calls.where((call) => call.method == 'createForwardMessage'),
          isEmpty,
        );
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

  test('tag903 forwarding retains original owner and privacy fences while native creation waits', () async {
    for (final retirement in ['owner', 'private', 'expiry']) {
      final harness = SdkHarness()
        ..conversations.add(SdkHarness.conversation());
      final entered = Completer<void>();
      final created = Completer<String>();
      harness.overrideCall = (call) async {
        if (call.method == 'createForwardMessage') {
          entered.complete();
          return created.future;
        }
        return harness.respond(call);
      };
      await harness.start();
      final repository = ChatRepository(
        gateway: harness.gateway,
        session: harness.session,
        conversationId: 'single-alice-bob',
      );
      final original = legacyCustom(903, {
        'textElem': {'content': '合成标签通知'},
      }, id: 'retained-$retirement').toJson();
      try {
        await repository.conversation();
        final pending = repository.forwardMessageJson(jsonEncode(original));
        final rejected = expectLater(pending, throwsA(isA<AppFailure>()));
        await entered.future.timeout(const Duration(seconds: 2));
        if (retirement == 'owner') {
          await harness.session.signOut();
        } else {
          harness.gateway.privacyLedger.observe('single-alice-bob', {
            'clientMsgID': original['clientMsgID'],
            'attachedInfoElem': {
              'isPrivateChat': true,
              if (retirement == 'expiry') 'hasReadTime': 1700000000000,
              if (retirement == 'expiry') 'burnDuration': 30,
            },
          }, now: DateTime.now());
        }
        created.complete(
          jsonEncode({
            ...original,
            'clientMsgID': 'new-$retirement',
            'sendID': 'alice',
          }),
        );
        await rejected;
        expect(
          harness.calls.where((call) => call.method == 'createForwardMessage'),
          hasLength(1),
        );
        expect(
          harness.calls.where((call) => call.method == 'sendMessage'),
          isEmpty,
        );
      } finally {
        if (!created.isCompleted) {
          created.complete(
            jsonEncode({
              ...original,
              'clientMsgID': 'abandoned',
              'sendID': 'alice',
            }),
          );
        }
        repository.clear();
        await harness.dispose();
      }
    }
  });

  test(
    'meeting905 malformed fields cannot gain native forwarding authority',
    () async {
      final harness = SdkHarness()
        ..conversations.add(SdkHarness.conversation());
      await harness.start();
      final repository = ChatRepository(
        gateway: harness.gateway,
        session: harness.session,
        conversationId: 'single-alice-bob',
      );
      final valid = <String, Object?>{
        'inviterUserID': 'bob',
        'inviterNickname': '原邀请人',
        'inviterFaceURL': null,
        'subject': '原会议',
        'id': 'original-meeting',
        'start': 1700000000,
        'duration': 3600,
        'unknownMetadata': {'revision': 3},
      };
      try {
        await repository.conversation();
        final denied = <Map<String, Object?>>[
          for (final field in [
            'inviterUserID',
            'subject',
            'id',
            'start',
            'duration',
          ])
            {...valid}..remove(field),
          {...valid, 'inviterUserID': 12},
          {...valid, 'inviterNickname': false},
          {...valid, 'inviterFaceURL': <String>[]},
          {...valid, 'subject': <String>[]},
          {...valid, 'id': '   '},
          {...valid, 'start': '1700000000'},
          {...valid, 'start': 8640000000001},
          {...valid, 'duration': -1},
          {...valid, 'duration': 1.5},
        ];
        for (var index = 0; index < denied.length; index++) {
          final message = legacyCustom(905, denied[index], id: 'bad905-$index');
          final before = jsonEncode(message.toJson());
          final mapped = mapMessage(message, 'alice');
          expect(mapped.kind, ChatMessageKind.custom);
          expect(mapped.tagMeeting, isNull);
          expect(mapped.canForward, isFalse);
          await expectLater(
            repository.forwardMessageJson(before),
            throwsA(isA<AppFailure>()),
          );
          expect(jsonEncode(message.toJson()), before);
        }
        final zero = mapMessage(
          legacyCustom(905, {...valid, 'duration': 0}),
          'alice',
        );
        expect(zero.tagMeeting?.durationSeconds, 0);
        expect(zero.canForward, isTrue);
        expect(
          harness.calls.where((call) => call.method == 'createForwardMessage'),
          isEmpty,
        );
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

  test('meeting905 retains original owner and privacy fences while native creation waits', () async {
    for (final retirement in ['owner', 'private', 'expiry']) {
      final harness = SdkHarness()
        ..conversations.add(SdkHarness.conversation());
      final entered = Completer<void>();
      final created = Completer<String>();
      harness.overrideCall = (call) async {
        if (call.method == 'createForwardMessage') {
          entered.complete();
          return created.future;
        }
        return harness.respond(call);
      };
      await harness.start();
      final repository = ChatRepository(
        gateway: harness.gateway,
        session: harness.session,
        conversationId: 'single-alice-bob',
      );
      final original = legacyCustom(905, {
        'inviterUserID': 'bob',
        'subject': '原会议',
        'id': 'original-meeting',
        'start': 1700000000,
        'duration': 3600,
      }, id: 'retained905-$retirement').toJson();
      try {
        await repository.conversation();
        final pending = repository.forwardMessageJson(jsonEncode(original));
        final rejected = expectLater(pending, throwsA(isA<AppFailure>()));
        await entered.future.timeout(const Duration(seconds: 2));
        if (retirement == 'owner') {
          await harness.session.signOut();
        } else {
          harness.gateway.privacyLedger.observe('single-alice-bob', {
            'clientMsgID': original['clientMsgID'],
            'attachedInfoElem': {
              'isPrivateChat': true,
              if (retirement == 'expiry') 'hasReadTime': 1700000000000,
              if (retirement == 'expiry') 'burnDuration': 30,
            },
          }, now: DateTime.now());
        }
        created.complete(
          jsonEncode({
            ...original,
            'clientMsgID': 'new905-$retirement',
            'sendID': 'alice',
          }),
        );
        await rejected;
        expect(
          harness.calls.where((call) => call.method == 'createForwardMessage'),
          hasLength(1),
        );
        expect(
          harness.calls.where((call) => call.method == 'sendMessage'),
          isEmpty,
        );
      } finally {
        if (!created.isCompleted) {
          created.complete(
            jsonEncode({
              ...original,
              'clientMsgID': 'abandoned905',
              'sendID': 'alice',
            }),
          );
        }
        repository.clear();
        await harness.dispose();
      }
    }
  });
}

final class _HintHarness {
  final h = BatchHarness();
  int failureCode = 1303;
  Matcher get failure => isA<AppFailure>()
      .having((e) => e.kind, 'original failure kind', FailureKind.unknown)
      .having((e) => e.code, 'original send code', failureCode);
  Completer<void>? createRelease, insertRelease;
  bool failCreate = false, unknownInsert = false;
  Map<String, dynamic>? privacy;
  String? encodedPrivacy;
  Map<String, dynamic> Function(Map<String, dynamic>)? corruptStoredAck;
  int creates = 0, inserts = 0;
  Future<void> start() async {
    await h.start();
    h.intercept = (call) async {
      final args = call.arguments as Map;
      switch (call.method) {
        case 'createTextMessage':
          final value = Map<String, dynamic>.from(
            jsonDecode(h.respond(call) as String) as Map,
          );
          if (privacy != null) value['attachedInfoElem'] = privacy;
          if (encodedPrivacy != null) value['attachedInfo'] = encodedPrivacy;
          return jsonEncode(value);
        case 'sendMessage':
          throw PlatformException(code: '$failureCode');
        case 'createCustomMessage':
          ++creates;
          await createRelease?.future;
          if (failCreate) {
            throw PlatformException(code: 'local-storage-failure');
          }
          return jsonEncode({
            ...SdkHarness.message(
              id: 'local-hint-$creates',
              text: '',
              sender: 'alice',
              status: 1,
            ),
            'contentType': 110,
            'customElem': {
              'data': args['data'],
              'extension': '',
              'description': '',
            },
          });
        case 'insertSingleMessageToLocalStorage':
          ++inserts;
          final sent = Map<String, dynamic>.from(args['message'] as Map);
          final stored = corruptStoredAck?.call(sent) ?? sent;
          // Native persistence can precede or outlive its method response.
          h.sdk.messages.add(stored);
          await insertRelease?.future;
          if (unknownInsert) {
            throw PlatformException(code: 'local-insert-unconfirmed');
          }
          return jsonEncode(stored);
      }
      return h.respond(call);
    };
  }

  Future<void> settle(bool Function() done) async {
    for (var i = 0; i < 100 && !done(); ++i) {
      await Future<void>.delayed(Duration.zero);
    }
    expect(done(), isTrue);
  }

  Future<void> close() async {
    if (createRelease?.isCompleted == false) createRelease!.complete();
    if (insertRelease?.isCompleted == false) insertRelease!.complete();
    await h.dispose();
  }
}

Map<String, dynamic> _wire(int type) => {
  ...SdkHarness.message(id: 'relationship-source', text: '', sender: 'alice'),
  'contentType': 110,
  // Even an untrusted payload claiming another UID cannot select the target.
  'customElem': {
    'data': jsonEncode({
      'customType': type,
      'data': {'userID': 'untrusted-payload-user'},
    }),
  },
};

Future<Finder> _reason(WidgetTester tester, ChatCardFixture f) async {
  final action = find.widgetWithText(TextButton, '发送好友验证');
  await f.transport.base.ready(tester, action);
  await tester.tap(action);
  final input = find.descendant(
    of: find.byType(AlertDialog),
    matching: find.byType(TextField),
  );
  await f.transport.base.ready(tester, input);
  return input;
}

void _relationshipHintTests() {
  for (final code in [1302, 1303]) {
    test(
      'real send $code keeps the failed row and original error while inserting only one read local hint',
      () async {
        final f = _HintHarness()..failureCode = code;
        try {
          await f.start();
          expect(await f.h.chat.sendText('原发送正文'), isTrue);
          await f.settle(() => f.h.chat.localHintResults.isNotEmpty);
          await f.settle(() => f.h.chat.failure != null);
          final outcome = f.h.chat.localHintResults.values.single;
          expect(outcome.kind, ChatLocalHintKind.inserted);
          expect(f.h.chat.failure, f.failure);
          final original = f.h.chat.messages.singleWhere(
            (m) => m.id == outcome.sourceMessageId,
          );
          expect(original.text, '原发送正文');
          expect(original.delivery, MessageDelivery.failed);
          final hint = f.h.chat.messages.singleWhere(
            (m) => m.id == outcome.hintId,
          );
          expect(hint.kind, ChatMessageKind.custom);
          expect(hint.canForward, isFalse);
          expect(
            f.h.source.relationshipHintType(hint.id),
            code == 1302 ? 910 : 911,
          );
          final call = f.h.sdk.calls.singleWhere(
            (c) => c.method == 'insertSingleMessageToLocalStorage',
          );
          expect(call.arguments['receiverID'], 'bob');
          expect(call.arguments['senderID'], 'alice');
          expect(call.arguments['message']['status'], 2);
          expect(call.arguments['message']['isRead'], isTrue);
          expect(jsonDecode(call.arguments['message']['customElem']['data']), {
            'customType': code == 1302 ? 910 : 911,
            'data': <String, dynamic>{},
          });
          expect(f.h.count('sendMessage'), 1);
          expect(f.creates, 1);
          expect(f.inserts, 1);
        } finally {
          await f.close();
        }
      },
    );
  }

  test('ordinary unopted send and non-relationship failure never create a local side effect', () async {
    final f = _HintHarness();
    try {
      await f.start();
      final prepared = await f.h.source.prepareText('原调用');
      await expectLater(f.h.source.send(prepared.id), throwsA(f.failure));
      final results = <ChatLocalHintResult>[];
      await expectLater(
        f.h.source.send(prepared.id, onLocalHint: results.add),
        throwsA(f.failure),
      );
      f.failureCode = 999;
      await expectLater(
        f.h.source.send(
          prepared.id,
          checkSource: () {},
          onLocalHint: results.add,
        ),
        throwsA(f.failure),
      );
      expect(results, isEmpty);
      expect(f.creates, 0);
      expect(f.inserts, 0);
      expect(f.h.count('sendMessage'), 3);
    } finally {
      await f.close();
    }
  });

  for (final unknown in [false, true]) {
    test(
      'local hint ${unknown ? 'unknown' : 'create failure'} is observable and never replaces or replays the original failure',
      () async {
        final f = _HintHarness()
          ..failCreate = !unknown
          ..unknownInsert = unknown;
        try {
          await f.start();
          final prepared = await f.h.source.prepareText('失败原文');
          final results = <ChatLocalHintResult>[];
          Future<void> attempt() async {
            await expectLater(
              f.h.source.send(
                prepared.id,
                checkSource: () {},
                onLocalHint: results.add,
              ),
              throwsA(f.failure),
            );
          }

          await attempt();
          expect(
            results.single.kind,
            unknown ? ChatLocalHintKind.unknown : ChatLocalHintKind.failed,
          );
          expect(results.single.message, isNull);
          await attempt();
          expect(
            results.last.kind,
            unknown ? ChatLocalHintKind.suppressed : ChatLocalHintKind.failed,
          );
          expect(f.creates, unknown ? 1 : 2);
          expect(f.inserts, unknown ? 1 : 0);
          expect(f.h.count('sendMessage'), 2);
        } finally {
          await f.close();
        }
      },
    );
  }

  test('CID clear during native local insert retires the original timeline before its late ACK', () async {
    final f = _HintHarness()..insertRelease = Completer<void>();
    try {
      await f.start();
      await f.h.chat.sendText('清理前原文');
      await f.settle(() => f.inserts == 1);
      final clear = f.h.sdk.gateway.clearConversationMessages(
        f.h.sdk.session.state.generation,
        f.h.source.conversationId,
      );
      expect(f.h.chat.messages, isEmpty);
      f.insertRelease!.complete();
      await clear;
      await f.settle(() => f.h.count('clearConversationAndDeleteAllMsg') == 1);
      expect(f.h.chat.messages, isEmpty);
      expect(f.h.chat.localHintResults, isEmpty);
      expect(f.h.sdk.messages, isEmpty);
      expect(f.inserts, 1);
      expect(f.h.count('sendMessage'), 1);
    } finally {
      await f.close();
    }
  });

  test('source expiry while create is pending prevents LOCAL insert and never renews the original burn window', () async {
    final f = _HintHarness()..createRelease = Completer<void>();
    var clock = DateTime.now();
    try {
      await f.start();
      clock = DateTime.now();
      final repository = ChatRepository(
        gateway: f.h.sdk.gateway,
        session: f.h.sdk.session,
        conversationId: f.h.source.conversationId,
        now: () => clock,
      );
      await repository.conversation();
      final prepared = await repository.prepareText('短时原文');
      final results = <ChatLocalHintResult>[];
      final send = expectLater(
        repository.send(
          prepared.id,
          checkSource: () {},
          onLocalHint: results.add,
        ),
        throwsA(f.failure),
      );
      await f.settle(() => f.creates == 1);
      final readAt = clock.millisecondsSinceEpoch;
      clock = clock.add(const Duration(seconds: 61));
      f.h.sdk.gateway.privacyLedger.observe(repository.conversationId, {
        'clientMsgID': prepared.id,
        'attachedInfoElem': {
          'isPrivateChat': true,
          'burnDuration': 60,
          'hasReadTime': readAt,
        },
      }, now: clock);
      f.createRelease!.complete();
      await send;
      expect(results.single.kind, ChatLocalHintKind.failed);
      expect(f.inserts, 0);
      expect(f.h.count('sendMessage'), 1);
    } finally {
      await f.close();
    }
  });

  test('private source keeps its original privacy and suppresses a persistent hint without a new burn window', () async {
    final f = _HintHarness();
    try {
      await f.start();
      f.privacy = {
        'isPrivateChat': true,
        'burnDuration': 60,
        'hasReadTime': DateTime.now().millisecondsSinceEpoch,
      };
      final prepared = await f.h.source.prepareText('隐私原文');
      final expiry = prepared.privacy.expiresAtMilliseconds;
      final results = <ChatLocalHintResult>[];
      await expectLater(
        f.h.source.send(
          prepared.id,
          checkSource: () {},
          onLocalHint: results.add,
        ),
        throwsA(f.failure),
      );
      expect(results.single.kind, ChatLocalHintKind.suppressed);
      expect(results.single.message, isNull);
      expect(prepared.privacy.expiresAtMilliseconds, expiry);
      expect(prepared.privacy.containsPrivateContent, isTrue);
      expect(f.creates, 0);
      expect(f.inserts, 0);
      expect(f.h.count('sendMessage'), 1);
    } finally {
      await f.close();
    }
  });

  test('stricter ledger facts than the raw send suppress durable hints without rewriting the original failure', () async {
    for (final unknown in [false, true]) {
      final f = _HintHarness();
      try {
        await f.start();
        final prepared = await f.h.source.prepareText('普通原始行');
        f.h.sdk.gateway.privacyLedger.observe(f.h.source.conversationId, {
          'clientMsgID': prepared.id,
          if (unknown) 'attachedInfo': '{',
          if (!unknown)
            'attachedInfoElem': {
              'isPrivateChat': true,
              'burnDuration': 60,
              'hasReadTime': DateTime.now().millisecondsSinceEpoch,
            },
        }, now: DateTime.now());
        final results = <ChatLocalHintResult>[];
        await expectLater(
          f.h.source.send(
            prepared.id,
            checkSource: () {},
            onLocalHint: results.add,
          ),
          throwsA(f.failure),
        );
        expect(results.single.kind, ChatLocalHintKind.suppressed);
        expect(
          f.h.sdk.calls
              .singleWhere((c) => c.method == 'sendMessage')
              .arguments['message']['attachedInfoElem'],
          isNull,
        );
        expect(f.creates, 0);
        expect(f.inserts, 0);
        expect(f.h.count('sendMessage'), 1);
      } finally {
        await f.close();
      }
    }
  });

  test('damaged LOCAL ACK remains unknown and the already stored native hint is hidden after remount without replay', () async {
    for (final field in ['attachedInfo', 'attachedInfoElem', 'hasReadTime']) {
      final f = _HintHarness();
      try {
        await f.start();
        f.privacy = {
          'isPrivateChat': false,
          'burnDuration': 60,
          'hasReadTime': DateTime.now().millisecondsSinceEpoch,
        };
        f.encodedPrivacy = jsonEncode(f.privacy);
        f.corruptStoredAck = (value) => value
          ..[field] = field == 'hasReadTime'
              ? (value[field] as int) + 60000
              : null;
        final prepared = await f.h.source.prepareText('只失败一次的原文');
        final results = <ChatLocalHintResult>[];
        await expectLater(
          f.h.source.send(
            prepared.id,
            checkSource: () {},
            onLocalHint: results.add,
          ),
          throwsA(f.failure),
        );
        final outcome = results.single;
        expect(outcome.kind, ChatLocalHintKind.unknown);
        expect(outcome.message, isNull);
        expect(
          f.h.sdk.messages.any((m) => m['clientMsgID'] == outcome.hintId),
          isTrue,
        );
        final remounted = ChatRepository(
          gateway: f.h.sdk.gateway,
          session: f.h.sdk.session,
          conversationId: f.h.source.conversationId,
        );
        await remounted.conversation();
        final page = await remounted.history();
        expect(remounted.isRemoved(outcome.hintId!), isTrue);
        expect(page.messages.where((m) => m.id == outcome.hintId), isEmpty);
        expect(
          f.h.sdk.gateway.privacyLedger.lookup(outcome.hintId!).hidden,
          isTrue,
        );
        await expectLater(
          f.h.source.send(
            prepared.id,
            checkSource: () {},
            onLocalHint: results.add,
          ),
          throwsA(f.failure),
        );
        expect(results.last.kind, ChatLocalHintKind.suppressed);
        expect(f.creates, 1);
        expect(f.inserts, 1);
        expect(f.h.count('addFriend'), 0);
        remounted.clear();
      } finally {
        await f.close();
      }
    }
  });

  test('late stricter source facts never upgrade a pending hint and remain effective for a known hint after remount', () async {
    for (final pending in [true, false]) {
      final f = _HintHarness();
      if (pending) f.insertRelease = Completer<void>();
      try {
        await f.start();
        final prepared = await f.h.source.prepareText('原始普通消息');
        final results = <ChatLocalHintResult>[];
        final send = expectLater(
          f.h.source.send(
            prepared.id,
            checkSource: () {},
            onLocalHint: results.add,
          ),
          throwsA(f.failure),
        );
        await f.settle(() => pending ? f.inserts == 1 : results.isNotEmpty);
        if (pending) {
          final whilePending = ChatRepository(
            gateway: f.h.sdk.gateway,
            session: f.h.sdk.session,
            conversationId: f.h.source.conversationId,
          );
          await whilePending.conversation();
          final page = await whilePending.history();
          expect(
            f.h.sdk.messages.any((m) => m['clientMsgID'] == 'local-hint-1'),
            isTrue,
          );
          expect(page.messages.where((m) => m.id == 'local-hint-1'), isEmpty);
          whilePending.clear();
        }
        f.h.sdk.gateway.privacyLedger.observe(f.h.source.conversationId, {
          'clientMsgID': prepared.id,
          'attachedInfoElem': {
            'isPrivateChat': true,
            'burnDuration': 1,
            'hasReadTime': 1700000000000,
          },
        }, now: DateTime.now());
        if (pending) f.insertRelease!.complete();
        await send;
        final outcome = results.single;
        expect(
          outcome.kind,
          pending ? ChatLocalHintKind.unknown : ChatLocalHintKind.inserted,
        );
        final remounted = ChatRepository(
          gateway: f.h.sdk.gateway,
          session: f.h.sdk.session,
          conversationId: f.h.source.conversationId,
        );
        await remounted.conversation();
        final page = await remounted.history();
        if (pending) {
          expect(page.messages.where((m) => m.id == outcome.hintId), isEmpty);
          expect(remounted.isRemoved(outcome.hintId!), isTrue);
        } else {
          final hint = page.messages.singleWhere((m) => m.id == outcome.hintId);
          expect(remounted.canView(hint), isFalse);
          expect(hint.privacy.hidden, isTrue);
        }
        expect(f.inserts, 1);
        expect(f.h.count('sendMessage'), 1);
        expect(f.h.count('addFriend'), 0);
        remounted.clear();
      } finally {
        await f.close();
      }
    }
  });

  testWidgets(
    '911 hint opens the actual reason dialog once and confirms only the current single peer with no message send',
    (tester) async {
      final f = ChatCardFixture();
      try {
        await f.mount(tester, receivedWire: _wire(911));
        final text = find.text('对方开启了好友验证，你还不是对方的好友');
        await tester.longPress(text);
        await f.transport.base.frame(tester);
        expect(find.text('复制文字'), findsNothing);
        expect(find.text('引用回复'), findsNothing);
        expect(find.text('转发到聊天'), findsNothing);
        final input = await _reason(tester, f);
        expect(f.friendRequests, 0);
        await tester.enterText(input, '纯测试验证理由');
        await tester.tap(find.widgetWithText(TextButton, '确定').hitTestable());
        await f.transport.base.until(tester, () => f.friendRequests == 1);
        final request = f.sdk.calls.singleWhere((c) => c.method == 'addFriend');
        expect(request.arguments['toUserID'], 'bob');
        expect(request.arguments['reqMsg'], '纯测试验证理由');
        expect(f.conversations, 0);
        expect(f.sends, 0);
      } finally {
        await f.close(tester);
      }
    },
  );

  for (final change in ['clear', 'owner', 'issuer']) {
    testWidgets(
      '911 retained reason cannot apply after original $change invalidation',
      (tester) async {
        final f = ChatCardFixture();
        try {
          await f.mount(tester, receivedWire: _wire(911));
          final input = await _reason(tester, f);
          await tester.enterText(input, '不可迟发的理由');
          final retained = tester
              .widget<TextButton>(find.widgetWithText(TextButton, '确定'))
              .onPressed!;
          await f.transport.base.actual(tester, () async {
            if (change == 'clear') {
              await f.sdk.gateway.clearConversationMessages(
                f.sdk.session.state.generation,
                f.transport.cid,
              );
            } else {
              await f.transport.base.changeOwner(
                user: change == 'owner' ? 'carol' : 'alice',
                issuer: change == 'issuer'
                    ? 'https://another.example/'
                    : f.sdk.credentials.issuer,
              );
            }
          });
          retained();
          await f.transport.base.frame(tester);
          expect(find.text('不可迟发的理由'), findsNothing);
          expect(f.friendRequests, 0);
          expect(f.conversations, 0);
          expect(f.sends, 0);
        } finally {
          await f.close(tester);
        }
      },
    );
  }

  testWidgets(
    '911 reason retained after its owned profile exits cannot dispatch a friend request',
    (tester) async {
      final f = ChatCardFixture();
      try {
        await f.mount(tester, receivedWire: _wire(911));
        final input = await _reason(tester, f);
        await tester.enterText(input, '退出后不可发送');
        final retained = tester
            .widget<TextButton>(find.widgetWithText(TextButton, '确定'))
            .onPressed!;
        Navigator.of(tester.element(find.byType(AlertDialog))).pop();
        await f.transport.base.frame(tester);
        f.transport.base.navigator.currentState!.pop();
        await f.transport.base.frame(tester);
        retained();
        await f.transport.base.frame(tester);
        expect(f.friendRequests, 0);
        expect(f.conversations, 0);
        expect(f.sends, 0);
      } finally {
        await f.close(tester);
      }
    },
  );

  testWidgets('910 has no friend-request action or ordinary message menu', (
    tester,
  ) async {
    final f = ChatCardFixture();
    try {
      await f.mount(tester, receivedWire: _wire(910));
      await tester.longPress(find.text('消息已发出，但被对方拒收了'));
      await f.transport.base.frame(tester);
      expect(find.text('发送好友验证'), findsNothing);
      expect(find.text('复制文字'), findsNothing);
      expect(find.text('引用回复'), findsNothing);
      expect(find.text('转发到聊天'), findsNothing);
      expect(f.friendRequests, 0);
      expect(f.sends, 0);
    } finally {
      await f.close(tester);
    }
  });
}
