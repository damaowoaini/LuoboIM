import 'dart:convert';

import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/data/message_mapper.dart';
import 'package:imapp/features/chat/domain/chat_location.dart';
import 'package:imapp/features/chat/domain/chat_message.dart';

void main() {
  ChatLocation? parse(
    String? description, {
    double? lat = 31.2,
    double? lon = 121.5,
  }) => ChatLocation.parse(
    latitude: lat,
    longitude: lon,
    description: description,
  );

  test('location accepts explicitly supplied zero and both inclusive geographic limits', () {
    for (final point in [(0.0, 0.0), (-90.0, -180.0), (90.0, 180.0)]) {
      final value = parse(null, lat: point.$1, lon: point.$2)!;
      expect((value.latitude, value.longitude), point);
      expect(value.name, '位置');
    }
  });
  test('location rejects missing nonfinite and out of range coordinates', () {
    for (final point in <(double?, double?)>[
      (null, 0),
      (0, null),
      (double.nan, 0),
      (0, double.infinity),
      (double.negativeInfinity, 0),
      (90.001, 0),
      (-90.001, 0),
      (0, 180.001),
      (0, -180.001),
    ]) {
      expect(parse('地点', lat: point.$1, lon: point.$2), isNull);
    }
  });
  test('legacy JSON displays bounded name and addr and ignores its URL and duplicate coordinates', () {
    final value = parse(
      jsonEncode({
        'name': '人民公园',
        'addr': '东门集合',
        'latitude': 0,
        'longitude': 0,
        'url': 'https://untrusted.example/static?key=synthetic',
      }),
    )!;
    expect((value.latitude, value.longitude), (31.2, 121.5));
    expect((value.name, value.address), ('人民公园', '东门集合'));
    expect(value.coordinateSystem, isNull);
    expect(value.toString(), 'ChatLocation');
  });
  test('legacy plaintext remains local readable text with control and bidi characters removed', () {
    final value = parse('  原有地点\n一号门\u202e  ')!;
    expect(value.address, '原有地点 一号门');
    expect(value.name, '位置');
    expect(
      parse('https://untrusted.example/static?key=synthetic')!.address,
      isEmpty,
    );
  });
  test('malformed arrays nonflat objects and excessively nested JSON are rejected before projection', () {
    for (final value in [
      '{',
      '[1]',
      '{"name":{}}',
      '{"x":[]}',
      '{"x":${'[' * 2000}0${']' * 2000}}',
    ]) {
      expect(parse(value), isNull);
    }
  });
  test('escaped braces in a valid flat JSON label do not count as nesting', () {
    final value = parse(jsonEncode({'name': '集合 {"东"} 门', 'addr': 'A\\B'}))!;
    expect(value.name, '集合 {"东"} 门');
    expect(value.address, 'A\\B');
  });
  test('location bounds description fields key count and wrong text types without truncating identity', () {
    for (final value in [
      'x' * 4097,
      'x' * 513,
      jsonEncode({'name': '界' * 129}),
      jsonEncode({'addr': '界' * 513}),
      jsonEncode({'name': 10}),
      jsonEncode({'addr': false}),
      jsonEncode({for (var i = 0; i < 17; i++) 'key$i': i}),
    ]) {
      expect(parse(value), isNull);
    }
    expect(
      parse(jsonEncode({'name': '界' * 128, 'addr': '界' * 512})),
      isNotNull,
    );
  });
  test('sender datum markers stay explicit and unrecognized or omitted markers stay unknown', () {
    expect(parse('{"coordinateSystem":"wgs84"}')!.coordinateSystem, 'WGS84');
    expect(parse('{"coordinateSystem":"gcj02"}')!.coordinateSystem, 'GCJ-02');
    expect(parse('{"coordinateSystem":"invented"}')!.coordinateSystem, isNull);
    expect(parse('{}')!.coordinateSystem, isNull);
    expect(parse('{"coordinateSystem":"gcj02"}')!.latitude, 31.2);
  });

  Message wire({
    int type = MessageType.location,
    bool private = false,
    Map<String, Object?>? element,
  }) => Message.fromJson({
    'clientMsgID': 'actual-location',
    'sendID': 'bob',
    'senderNickname': 'Bob',
    'contentType': type,
    'sendTime': 1700000000000,
    'status': 2,
    'locationElem':
        element ??
        {
          'latitude': 31.2,
          'longitude': 121.5,
          'description': '{"name":"人民公园","addr":"东门"}',
        },
    if (private) 'attachedInfoElem': {'isPrivateChat': true},
  });
  test('actual SDK Location109 maps a typed projection without mutating the standard wire', () {
    final original = wire();
    final before = jsonEncode(original.toJson());
    final message = mapMessage(original, 'alice');
    expect(original.contentType, 109);
    expect(message.kind, ChatMessageKind.location);
    expect(message.location!.name, '人民公园');
    expect(message.location!.latitude, 31.2);
    expect(message.text, '[位置]');
    expect(message.media, isNull);
    expect(jsonEncode(original.toJson()), before);
  });
  test(
    'both ChatMessage copies preserve typed location and original privacy',
    () {
      final original = mapMessage(wire(private: true), 'alice');
      for (final copy in [
        original.withDelivery(MessageDelivery.failed),
        original.withLocalMedia('/owned/staged'),
        original
            .withDelivery(MessageDelivery.sending)
            .withLocalMedia('/owned/staged'),
      ]) {
        expect(copy.location, same(original.location));
        expect(copy.privacy, same(original.privacy));
        expect(copy.privacy.allowsRetention, false);
        expect(copy.id, original.id);
      }
    },
  );
  test('invalid actual SDK location keeps its kind but has no actionable coordinate payload', () {
    final mapped = mapMessage(
      wire(element: {'latitude': 91, 'longitude': 10, 'description': '{}'}),
      'alice',
    );
    expect(mapped.kind, ChatMessageKind.location);
    expect(mapped.location, isNull);
  });
  test('nonlocation messages cannot acquire an injected location and ordinary text is unchanged', () {
    final raw = wire(type: MessageType.text)
      ..textElem = TextElem(content: '正常文字');
    final mapped = mapMessage(raw, 'alice');
    expect(mapped.kind, ChatMessageKind.text);
    expect(mapped.text, '正常文字');
    expect(mapped.location, isNull);
  });
  test('actual merger descendants retain Location109 and root privacy', () {
    final root = Message.fromJson({
      'clientMsgID': 'root',
      'contentType': MessageType.merger,
      'attachedInfoElem': {'isPrivateChat': true},
      'mergeElem': {
        'multiMessage': [wire().toJson()],
      },
    });
    final mapped = mapMessage(root, 'alice');
    expect(mapped.mergedMessages.single.location!.name, '人民公园');
    expect(mapped.mergedMessages.single.privacy.allowsRetention, false);
    expect(mapped.mergedContentComplete, true);
  });
}
