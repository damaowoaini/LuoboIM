import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/network/api_client.dart';
import 'package:imapp/features/auth/data/auth_repository.dart';

import '../../support/http_adapter.dart';

void main() {
  late TestHttpAdapter adapter;
  late AuthRepository repo;
  setUp(() {
    adapter = TestHttpAdapter(
      (_) async => jsonResponse(
        jsonEncode({
          'errCode': 0,
          'data': {
            'userID': 'fixture-user',
            'chatToken': 'chat-fixture',
            'imToken': 'im-fixture',
            'serverInfo': {
              'serverName': '测试',
              'domains': 'tenant.test, backup.test',
            },
          },
        }),
      ),
    );
    repo = AuthRepository(
      ApiClient(
        base: Uri.parse('https://route.test/'),
        dio: Dio()..httpClientAdapter = adapter,
      ),
      const DeviceIdentity(
        id: 'installation-fixture',
        platform: 1,
        model: 'iOS',
      ),
    );
  });

  test(
    'email and verification-code login actually send the selected fields',
    () async {
      final result = await repo.login(
        identity: AuthIdentity(account: 'person@example.test'),
        verificationCode: '123456',
      );
      final payload = adapter.requests.single.data as Map;
      expect(payload['email'], 'person@example.test');
      expect(payload['verificationCode'], '123456');
      expect(payload.containsKey('password'), isFalse);
      expect(payload.containsKey('phoneNumber'), isFalse);
      expect(adapter.requests.single.headers.containsKey('token'), isFalse);
      expect(result.domains, ['tenant.test', 'backup.test']);
      expect(
        result.endpoints.socket.toString(),
        'wss://tenant.test/msg_gateway',
      );
    },
  );

  test(
    'password wire representation stays compatible without sending plaintext',
    () async {
      await repo.login(
        identity: AuthIdentity(account: '13800000000'),
        password: 'password',
      );
      final payload = adapter.requests.single.data as Map;
      expect(payload['password'], '5f4dcc3b5aa765d61d8327deb882cf99');
      expect(payload['areaCode'], '+86');
      expect(payload['deviceID'], 'installation-fixture');
    },
  );

  test(
    'email reset and OTP purposes preserve their distinct contracts',
    () async {
      final identity = AuthIdentity(account: 'person@example.test');
      await repo.sendCode(identity, VerificationPurpose.resetPassword);
      await repo.resetPassword(
        identity: identity,
        password: 'new-password',
        verifyCode: '123456',
      );
      expect((adapter.requests[0].data as Map)['usedFor'], 2);
      expect((adapter.requests[0].data as Map)['email'], 'person@example.test');
      expect((adapter.requests[1].data as Map)['email'], 'person@example.test');
      expect((adapter.requests[1].data as Map)['verifyCode'], '123456');
    },
  );
}
