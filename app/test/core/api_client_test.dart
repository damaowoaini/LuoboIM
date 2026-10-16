import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/core/network/api_client.dart';

import '../support/http_adapter.dart';

void main() {
  test('stale token errors cannot log a newer session out', () async {
    var current = true;
    var expired = 0;
    final pending = Completer<ResponseBody>();
    final dio = Dio()
      ..httpClientAdapter = TestHttpAdapter((_) => pending.future);
    final client = ApiClient(
      base: Uri.parse('https://tenant.test/chat/'),
      dio: dio,
      token: 'private-fixture',
      isCurrent: () => current,
      onExpired: () => expired++,
    );
    final response = client.post('account/test', {});
    final expected = expectLater(
      response,
      throwsA(
        isA<AppFailure>().having((e) => e.kind, 'kind', FailureKind.cancelled),
      ),
    );
    current = false;
    pending.complete(jsonResponse('{"errCode":1501,"data":null}'));
    await expected;
    expect(expired, 0);
  });

  test(
    'expired credentials trigger logout while account lock does not',
    () async {
      var expired = 0;
      var code = 1501;
      final dio = Dio()
        ..httpClientAdapter = TestHttpAdapter(
          (_) async => jsonResponse(
            '{"errCode":$code,"errMsg":"secret token=private-fixture"}',
          ),
        );
      final client = ApiClient(
        base: Uri.parse('https://tenant.test/chat/'),
        dio: dio,
        token: 'private-fixture',
        onExpired: () => expired++,
      );
      await expectLater(
        client.post('account/test', {}),
        throwsA(
          isA<AppFailure>().having(
            (e) => e.message,
            'safe message',
            isNot(contains('private-fixture')),
          ),
        ),
      );
      expect(expired, 1);
      code = 20018;
      await expectLater(
        client.post('account/test', {}),
        throwsA(
          isA<AppFailure>().having(
            (e) => e.kind,
            'kind',
            FailureKind.rateLimited,
          ),
        ),
      );
      expect(expired, 1);
    },
  );

  test(
    'authenticated requests cannot follow redirects or escape their API root',
    () async {
      final adapter = TestHttpAdapter(
        (_) async => jsonResponse('{"errCode":0,"data":{}}'),
      );
      final client = ApiClient(
        base: Uri.parse('https://tenant.test/chat/'),
        dio: Dio()..httpClientAdapter = adapter,
        token: 'fixture',
      );
      await client.post('account/test', {});
      expect(adapter.requests.single.followRedirects, isFalse);
      expect(
        adapter.requests.single.uri.toString(),
        'https://tenant.test/chat/account/test',
      );
      expect(adapter.requests.single.headers['token'], 'fixture');
      await expectLater(
        client.post('https://other.test/steal', {}),
        throwsA(isA<AppFailure>()),
      );
      await expectLater(
        client.post('../steal', {}),
        throwsA(isA<AppFailure>()),
      );
      expect(adapter.requests.length, 1);
    },
  );

  test('invalid envelopes never become a successful operation', () async {
    for (final raw in [
      '<html>proxy error</html>',
      '{"data":{"userID":"u"}}',
      '{"errCode":"0","data":{}}',
    ]) {
      final client = ApiClient(
        base: Uri.parse('https://route.test/'),
        dio: Dio()
          ..httpClientAdapter = TestHttpAdapter((_) async => jsonResponse(raw)),
      );
      await expectLater(
        client.post('api/auth/login', {}),
        throwsA(isA<AppFailure>()),
      );
    }
  });
}
