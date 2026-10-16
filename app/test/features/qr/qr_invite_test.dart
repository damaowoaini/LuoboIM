import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/tenant/session_credentials.dart';
import 'package:imapp/features/qr/domain/qr_invite.dart';

SessionCredentials account({String issuer = 'https://login.example.com/auth/',
    String tenant = 'tenant-a', String user = 'alice'}) => SessionCredentials(
  userId: user, tenantId: tenant, tenantName: '测试服务',
  domains: ['line1.example.com', 'line2.example.com'],
  chatToken: 'private-chat-test-token', imToken: 'private-im-test-token',
  issuer: issuer,
);

void main() {
  final first = account();
  String code([QrInviteKind kind = QrInviteKind.user, String id = 'bob']) =>
      QrInvite.encode(kind: kind, subjectId: id, credentials: first);
  Matcher rejected(QrInviteRejection reason) => throwsA(
      isA<QrInviteException>().having((e) => e.reason, 'reason', reason));

  test('new personal and group codes preserve exact Unicode IDs without credentials', () {
    for (final kind in QrInviteKind.values) {
      final text = code(kind, '用户+组@123');
      final invite = QrInvite.decode(text, first);
      expect(invite.kind, kind);
      expect(invite.subjectId, '用户+组@123');
      expect(invite.legacy, isFalse);
      expect(text, startsWith('luoboduihua://qr/'));
      for (final secret in [first.chatToken, first.imToken, first.issuer,
        first.domains.first, first.tenantName, first.tenantId]) {
        expect(text, isNot(contains(secret)));
      }
    }
  });

  test('another account and selected line retain the same issuer tenant scope', () {
    expect(QrInvite.decode(code(), account(user: 'carol')).subjectId, 'bob');
    expect(QrInvite.serviceScope(first.onLine(1)), QrInvite.serviceScope(first));
    expect(QrInvite.decode(code(), first.onLine(1)).legacy, isFalse);
  });

  test('equal tenant ID on another auth issuer and equal issuer on another tenant are rejected', () {
    for (final foreign in [account(tenant: 'tenant-b'),
      account(issuer: 'https://other.example.com/auth/'),
      account(issuer: 'https://login.example.com/different/')]) {
      expect(() => QrInvite.decode(code(), foreign),
        rejected(QrInviteRejection.otherService));
    }
  });

  test('both historical formats are typed and require current service confirmation', () {
    for (final row in [
      ('com.jrtxa.wanfeng/addFriend/bob', QrInviteKind.user, 'bob'),
      ('com.jrtxa.wanfeng/joinGroup/group-123', QrInviteKind.group, 'group-123'),
    ]) {
      final invite = QrInvite.decode(row.$1, first);
      expect(invite.kind, row.$2);
      expect(invite.subjectId, row.$3);
      expect(invite.legacy, isTrue);
    }
    expect(code(), isNot(contains('wanfeng')));
  });

  test('arbitrary websites, app links, credentials and lookalike brands never decode as discovery', () {
    for (final text in ['https://example.com', 'http://example.com',
      'javascript:alert(1)', 'file:///private/test', 'luoboduihua-evil://qr/user/bob',
      'https://luoboduihua/qr/user/bob', 'com.jrtxa.wanfeng.evil/addFriend/bob']) {
      expect(() => QrInvite.decode(text, first), throwsA(isA<QrInviteException>()));
    }
  });

  test('new URI refuses unknown or duplicated fields, userinfo, port and fragment', () {
    final uri = Uri.parse(code());
    for (final invalid in [
      '$uri&v=1', '$uri&space=${QrInvite.serviceScope(first)}', '$uri&token=x',
      uri.replace(userInfo: 'attacker').toString(),
      uri.replace(port: 443).toString(), uri.replace(fragment: 'target').toString(),
      uri.replace(queryParameters: {'v':'2', 'space': QrInvite.serviceScope(first)}).toString(),
      uri.replace(queryParameters: {'v':'1'}).toString(),
      uri.replace(path: '/user/bob/extra').toString(),
    ]) {
      expect(() => QrInvite.decode(invalid, first), rejected(QrInviteRejection.invalid));
    }
  });

  test('empty, oversized, control, path and encoded delimiter IDs are rejected', () {
    for (final id in ['', 'a/b', r'a\b', 'a?b', 'a#b', 'a%2Fb', 'a b', 'a\nb', '\uD800',
      List.filled(257, 'a').join()]) {
      expect(() => code(QrInviteKind.user, id), rejected(QrInviteRejection.invalid));
      expect(() => QrInvite.decode('com.jrtxa.wanfeng/addFriend/$id', first),
        rejected(QrInviteRejection.invalid));
    }
    for (final invalid in ['', ' ${code()}', '${code()}\n', List.filled(2049,'x').join(),
      code().replaceFirst('/bob?', '/a%2Fb?')]) {
      expect(() => QrInvite.decode(invalid, first), rejected(QrInviteRejection.invalid));
    }
  });
}
