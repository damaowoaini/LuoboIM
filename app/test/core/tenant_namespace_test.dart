import 'dart:convert';

import 'package:crypto/crypto.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/core/tenant/session_credentials.dart';

SessionCredentials scoped(
  String issuer, {
  List<String> domains = const ['one.example.com', 'two.example.com'],
}) => SessionCredentials(
  userId: 'same-user',
  tenantId: 'same-tenant',
  tenantName: '服务',
  domains: domains,
  chatToken: 'chat',
  imToken: 'im',
  issuer: issuer,
);
void main() {
  test('independent issuers and route paths cannot share SDK or personal-data namespaces', () {
    final first = scoped('https://route.example.com/provider-a/');
    final second = scoped('https://route.example.com/provider-b/');
    final third = scoped('https://other.example.com/provider-a/');
    expect({first.accountKey, second.accountKey, third.accountKey}.length, 3);
    expect(first.onLine(1).accountKey, first.accountKey);
    expect(first.onLine(1).issuer, first.issuer);
    expect(
      SessionCredentials.fromJson(first.toJson()).accountKey,
      first.accountKey,
    );
  });
  test('legacy records without issuer never automatically reopen ambiguous unscoped caches', () {
    final legacy = {
      'userId': 'same-user',
      'tenantId': 'same-tenant',
      'tenantName': '旧服务',
      'domains': ['one.example.com', 'two.example.com'],
      'chatToken': 'chat',
      'imToken': 'im',
    };
    expect(
      () => SessionCredentials.fromJson(legacy),
      throwsA(
        isA<AppFailure>().having(
          (e) => e.kind,
          'kind',
          FailureKind.sessionUpgrade,
        ),
      ),
    );
    final oldKey = sha256
        .convert(utf8.encode(jsonEncode(['same-tenant', 'same-user'])))
        .toString();
    final authenticated = scoped('https://route.example.com/');
    expect(authenticated.accountKey, isNot(oldKey));
  });
}
