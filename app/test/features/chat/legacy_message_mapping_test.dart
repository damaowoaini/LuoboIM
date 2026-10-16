import 'dart:convert';

import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/data/message_mapper.dart';
import 'package:imapp/features/chat/domain/chat_message.dart';

Message custom(Object? value, {String description = ''}) => Message.fromJson({
  'clientMsgID': 'legacy-fixture',
  'contentType': MessageType.custom,
  'sendID': 'bob',
  'status': 2,
  'customElem': {
    'data': jsonEncode(value),
    'description': description,
    'extension': 'opaque-vendor-data',
  },
});

void main() {
  test('original favorite-face JSON exposes its HTTPS image and preserves protocol data', () {
    final data = jsonEncode({
      'url': 'https://assets.example.com/face.webp',
      'width': 128,
      'height': 256,
    });
    final message = Message.fromJson({
      'contentType': MessageType.customFace,
      'faceElem': {'data': data, 'index': 4},
    });
    final before = jsonEncode(message.toJson());
    final mapped = mapMessage(message, 'alice');
    expect(mapped.kind, ChatMessageKind.sticker);
    expect(mapped.media.toString(), 'https://assets.example.com/face.webp');
    expect(mapped.text, '[表情]');
    expect(jsonEncode(message.toJson()), before);
    expect(message.faceElem?.data, data);
  });
  test('direct-URL face data stays compatible', () {
    final message = Message.fromJson({
      'contentType': MessageType.customFace,
      'faceElem': {'data': 'https://assets.example.com/old.png'},
    });
    expect(mapMessage(message, 'alice').media?.path, '/old.png');
  });
  test('legacy custom emoji902 maps to a sticker without rewriting the custom envelope', () {
    final message = custom({
      'customType': 902,
      'data': {
        'url': 'https://assets.example.com/custom.gif',
        'width': 90,
        'height': 90,
      },
    });
    final before = jsonEncode(message.toJson());
    final mapped = mapMessage(message, 'alice');
    expect(mapped.kind, ChatMessageKind.sticker);
    expect(mapped.media?.path, '/custom.gif');
    expect(messagePreview(message), '[表情]');
    expect(jsonEncode(message.toJson()), before);
  });
  test(
    'call901 states and durations are readable while opaque data is retained',
    () {
      for (final sample in <(String, int?, String)>[
        ('hangup', 65, '通话时长 01:05'),
        ('beHangup', 3600, '通话时长 01:00:00'),
        ('hangup', -1, '通话已结束'),
        ('cancel', null, '已取消'),
        ('beCanceled', null, '对方已取消'),
        ('reject', null, '已拒绝'),
        ('beRejected', null, '对方已拒绝'),
        ('timeout', null, '未接听'),
        ('networkError', null, '网络异常，通话已结束'),
        ('opaque-state-value', null, '通话记录'),
      ]) {
        final message = custom({
          'customType': 901,
          'data': {
            'type': 'video',
            'state': sample.$1,
            'duration': sample.$2,
            'vendorOpaque': 'retained',
          },
        });
        final before = jsonEncode(message.toJson());
        final mapped = mapMessage(message, 'alice');
        expect(mapped.text, '[视频通话] ${sample.$3}');
        expect(mapped.media, isNull);
        expect(mapped.kind, ChatMessageKind.custom);
        expect(isLegacyCallSummary(message), isTrue);
        expect(jsonEncode(message.toJson()), before);
        expect(mapped.text, isNot(contains('opaque-state-value')));
      }
      expect(
        messagePreview(
          custom({
            'customType': 901,
            'data': {'type': 'voice', 'state': 'timeout'},
          }),
        ),
        '[语音通话] 未接听',
      );
    },
  );
  test(
    'malformed or oversized custom data stays opaque with a safe fallback',
    () {
      for (final value in <Object?>[
        [],
        null,
        {'customType': 902, 'data': 'file:///private'},
        {'customType': 901.0, 'data': {}},
        {'customType': '901', 'data': {}},
      ]) {
        final message = custom(value);
        expect(() => mapMessage(message, 'alice'), returnsNormally);
        expect(messagePreview(message), '[自定义消息]');
        expect(mapMessage(message, 'alice').media, isNull);
      }
      final malformed = Message.fromJson({
        'contentType': MessageType.custom,
        'customElem': {'data': '{invalid private-json'},
      });
      expect(messagePreview(malformed), '[自定义消息]');
      final oversized = custom({
        'customType': 902,
        'data': {'url': 'https://assets.example.com/${'x' * 65536}'},
      });
      expect(mapMessage(oversized, 'alice').media, isNull);
      expect(messagePreview(oversized), '[自定义消息]');
    },
  );
  test('emoji data cannot become a local, executable, insecure or credential-bearing media URL', () {
    for (final url in [
      'file:///private/sensitive',
      '/private/sensitive',
      'javascript:alert(1)',
      'data:image/png,private',
      'http://assets.example.com/face.png',
      'https://token:secret@assets.example.com/face.png',
    ]) {
      final face = Message.fromJson({
        'contentType': MessageType.customFace,
        'faceElem': {
          'data': jsonEncode({'url': url}),
        },
      });
      final emoji = custom({
        'customType': 902,
        'data': {'url': url},
      });
      for (final message in [face, emoji]) {
        final mapped = mapMessage(message, 'alice');
        expect(mapped.media, isNull);
        expect(mapped.playableMedia, isNull);
        expect(mapped.localMedia, isNull);
        expect(mapped.text, '[表情]');
      }
    }
  });
  test('unknown custom messages retain their description and complete protocol data', () {
    final message = custom({
      'customType': 9999,
      'data': {'opaque': true},
    }, description: '业务自定义摘要');
    final before = jsonEncode(message.toJson());
    expect(messagePreview(message), '业务自定义摘要');
    expect(mapMessage(message, 'alice').kind, ChatMessageKind.custom);
    expect(jsonEncode(message.toJson()), before);
  });
}
