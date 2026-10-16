import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/chat/data/native_location_once.dart';
import 'package:imapp/features/chat/domain/location_pick.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  final nonce = 'a' * 64;
  Map<String, Object> payload() => {
    'requestNonce': nonce,
    'sequence': 1,
    'lng': 0,
    'lat': 0,
    'name': '原点',
  };
  LocationPick parse(Map<String, Object> value) =>
      LocationPick.parse(jsonEncode(value), nonce: nonce, afterSequence: 0);
  test('new location configuration requires deployment HTTPS fixed picker and explicit valid center', () {
    expect(LocationPickerConfig.parse('', '', ''), isNull);
    for (final uri in [
      'http://map.example/picker',
      'https://map.example/other',
      'https://user@map.example/picker',
      'https://map.example/picker?url=other',
    ]) {
      expect(LocationPickerConfig.parse(uri, '0', '0'), isNull);
    }
    final config = LocationPickerConfig.parse(
      'https://map.example:8443/picker',
      '0',
      '0',
    )!;
    expect(config.allows('https://map.example:8443/picker'), isTrue);
    expect(config.allows('https://map.example/picker'), isFalse);
    expect(config.allows('https://foreign.example:8443/picker'), isFalse);
    expect(
      LocationPickerConfig.parse('https://map.example/picker', 'NaN', '0'),
      isNull,
    );
  });
  test('explicit zero candidate creates bounded standard GCJ02 description without URLs or nonce', () {
    final pick = parse(payload());
    expect(pick.latitude, 0);
    expect(pick.longitude, 0);
    expect(jsonDecode(pick.description), {
      'v': 1,
      'name': '原点',
      'addr': '',
      'coordinateSystem': 'gcj02',
    });
    expect(pick.description, isNot(contains(nonce)));
    expect(pick.toString(), 'LocationPick');
  });
  test('candidate rejects stale namespace sequence actions nested JSON and invalid coordinate ranges', () {
    for (final changes in <Map<String, Object>>[
      {'requestNonce': 'b' * 64},
      {'sequence': 0},
      {'sequence': 1.0},
      {'sequence': 9007199254740992},
      {'lat': 90.1},
      {'lng': -180.1},
      {'action': 'send'},
      {
        'name': {'url': 'https://foreign.example'},
      },
    ]) {
      expect(
        () => parse({...payload(), ...changes}),
        throwsA(isA<AppFailure>()),
      );
    }
    expect(
      () => LocationPick.parse(
        jsonEncode(payload()),
        nonce: nonce,
        afterSequence: 1,
      ),
      throwsA(isA<AppFailure>()),
    );
    expect(
      () => LocationPick.parse(
        '{"x":${'[' * 10000}',
        nonce: nonce,
        afterSequence: 0,
      ),
      throwsA(isA<AppFailure>()),
    );
  });
  test('candidate rejects oversize controls URLs and invalid Unicode while keeping boundary text', () {
    for (final name in [
      '字' * 129,
      'https://host.example/?key=not-allowed',
      '\u202etext',
      '\u0000',
      '\u0080',
      '\u009f',
      '\u061cname',
      '\u200ename',
      '\u200fname',
      'www.host.example',
      'javascript:alert(1)',
      'data:image/png;base64,AA',
      'file:/private/path',
      'http:provider-detail',
      String.fromCharCode(0xd800),
    ]) {
      expect(
        () => parse({...payload(), 'name': name}),
        throwsA(isA<AppFailure>()),
      );
    }
    expect(parse({...payload(), 'name': '😀' * 128}).name.runes.length, 128);
  });
  test('ready and fixed error state cannot carry native actions or provider error details', () {
    final status = LocationMapStatus.parse(
      jsonEncode({'requestNonce': nonce, 'sequence': 0, 'state': 'ready'}),
      nonce,
    );
    expect(status.ready, isTrue);
    expect(
      () => LocationMapStatus.parse(
        jsonEncode({
          'requestNonce': nonce,
          'sequence': 0,
          'state': 'error',
          'code': 'map_load',
          'url': 'https://bad.example',
        }),
        nonce,
      ),
      throwsA(isA<AppFailure>()),
    );
    expect(
      () => LocationMapStatus.parse(
        jsonEncode({'requestNonce': nonce, 'sequence': 0, 'state': 'gps'}),
        nonce,
      ),
      throwsA(isA<AppFailure>()),
    );
  });
  test(
    'native fix requires exact request WGS84 recent time and valid accuracy',
    () {
      final now = DateTime.fromMillisecondsSinceEpoch(1700000000000);
      final value = <String, Object>{
        'requestId': 'r',
        'longitude': 0.0,
        'latitude': 0.0,
        'accuracyMeters': 100.0,
        'timestampMillis': now.millisecondsSinceEpoch,
        'coordinateSystem': 'wgs84',
        'approximate': true,
      };
      expect(LocationFix.parse(value, 'r', now).approximate, isTrue);
      for (final changes in <Map<String, Object>>[
        {'requestId': 'other'},
        {'coordinateSystem': 'gcj02'},
        {'accuracyMeters': -1},
        {'timestampMillis': now.millisecondsSinceEpoch - 10001},
        {'timestampMillis': now.millisecondsSinceEpoch + 2001},
        {'extra': 'field'},
      ]) {
        expect(
          () => LocationFix.parse({...value, ...changes}, 'r', now),
          throwsA(isA<AppFailure>()),
        );
      }
    },
  );
  test('real MethodChannel one-shot sends only exact request contract and cleans its own id', () async {
    final now = DateTime.fromMillisecondsSinceEpoch(1700000000000);
    final calls = <MethodCall>[];
    final messenger =
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(MethodChannelLocationOnce.channel, (
      call,
    ) async {
      calls.add(call);
      final id = (call.arguments as Map)['requestId'];
      if (call.method == 'cancel') {
        return {'requestId': id, 'cancelled': false};
      }
      return {
        'requestId': id,
        'longitude': 0.0,
        'latitude': 0.0,
        'accuracyMeters': 10.0,
        'timestampMillis': now.millisecondsSinceEpoch,
        'coordinateSystem': 'wgs84',
        'approximate': false,
      };
    });
    try {
      final fix = await MethodChannelLocationOnce(now: () => now)
          .request('b' * 32);
      expect(fix.latitude, 0);
      expect(calls.map((v) => v.method), ['request', 'cancel']);
      expect(calls.first.arguments, {
        'requestId': 'b' * 32,
        'timeoutMs': 15000,
      });
      expect(calls.last.arguments, {'requestId': 'b' * 32});
    } finally {
      messenger.setMockMethodCallHandler(
        MethodChannelLocationOnce.channel,
        null,
      );
    }
  });
  test(
    'native permission failure stays typed and exposes no raw platform message',
    () async {
      final messenger =
          TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
      messenger.setMockMethodCallHandler(MethodChannelLocationOnce.channel, (
        call,
      ) async {
        if (call.method == 'request') {
          throw PlatformException(
            code: 'denied',
            message: 'sensitive-native-detail',
          );
        }
        return {
          'requestId': (call.arguments as Map)['requestId'],
          'cancelled': false,
        };
      });
      try {
        await expectLater(
          MethodChannelLocationOnce().request('c' * 32),
          throwsA(
            isA<AppFailure>()
                .having((e) => e.kind, 'kind', FailureKind.permissionDenied)
                .having(
                  (e) => e.toString(),
                  'redacted',
                  isNot(contains('sensitive-native-detail')),
                ),
          ),
        );
      } finally {
        messenger.setMockMethodCallHandler(
          MethodChannelLocationOnce.channel,
          null,
        );
      }
    },
  );
}
