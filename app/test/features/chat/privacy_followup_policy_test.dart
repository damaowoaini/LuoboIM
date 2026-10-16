import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/domain/message_privacy.dart';

void main() {
  for (final read in [
    1,
    123,
    946684799,
    4102444801,
    946684799999,
    4102444800001,
  ]) {
    test(
      'rejects out-of-contract read timestamp $read without inventing a deadline',
      () {
        expect(
          privateExpiryMilliseconds(readAt: read, durationSeconds: 30),
          isNull,
        );
      },
    );
  }
  test('the earliest valid authoritative root or attachment read wins across both representations', () {
    final value = inspectMessagePrivacy({
      'hasReadTime': 1700000090000,
      'attachedInfo':
          '{"isPrivateChat":true,"burnDuration":30,"hasReadTime":1700000000}',
      'attachedInfoElem': {
        'isPrivateChat': true,
        'burnDuration': 60,
        'hasReadTime': 1700000050000,
      },
    });
    expect(value.expiresAtMilliseconds, 1700000030000);
  });
  test('actual atText quote is private while opaque custom payload remains uninterpreted', () {
    final child = {
      'attachedInfoElem': {'isPrivateChat': true},
    };
    expect(
      inspectMessagePrivacy({
        'atTextElem': {'quoteMessage': child},
      }).allowsRetention,
      false,
    );
    expect(
      inspectMessagePrivacy({
        'customElem': {'data': child},
      }).allowsRetention,
      true,
    );
  });
  test('fractional and malformed read values cannot suppress an earlier valid attached read', () {
    final value = inspectMessagePrivacy({
      'hasReadTime': 1700000090000.5,
      'attachedInfoElem': {
        'isPrivateChat': true,
        'burnDuration': 30,
        'hasReadTime': 1700000000,
      },
    });
    expect(value.expiresAtMilliseconds, 1700000030000);
    expect(value.allowsRetention, false);
  });
}
