import 'dart:convert';

import 'package:crypto/crypto.dart';

import '../../../core/tenant/session_credentials.dart';

enum QrInviteKind { user, group }

enum QrInviteRejection { invalid, unsupported, otherService }

final class QrInviteException implements Exception {
  const QrInviteException(this.reason);
  final QrInviteRejection reason;
  @override
  String toString() => 'QrInviteException(${reason.name})';
}

/// Discovery data only. It is never authorization to add, join, change service
/// configuration or open an external URL. UI must retain its original session
/// and recheck that session before showing/confirming an async scan result.
final class QrInvite {
  const QrInvite._(this.kind, this.subjectId, this.legacy);
  final QrInviteKind kind;
  final String subjectId;
  final bool legacy;

  /// User ID is deliberately excluded: another account on the same issuer and
  /// tenant must be able to scan. Line changes retain the same service scope.
  /// No access token, service endpoint or display name is put into the code.
  static String serviceScope(SessionCredentials credentials) => sha256
      .convert(utf8.encode(jsonEncode([
        'luoboduihua-qr-v1', credentials.issuer, credentials.tenantId,
      ])))
      .toString();

  static bool validId(String id) => id.isNotEmpty &&
      utf8.encode(id).length <= 256 &&
      utf8.decode(utf8.encode(id)) == id &&
      !RegExp(r'[\s\x00-\x1f\x7f/\\?#%]').hasMatch(id);

  static String encode({
    required QrInviteKind kind,
    required String subjectId,
    required SessionCredentials credentials,
  }) {
    if (!validId(subjectId)) {
      throw const QrInviteException(QrInviteRejection.invalid);
    }
    return Uri(
      scheme: 'luoboduihua',
      host: 'qr',
      pathSegments: [kind.name, subjectId],
      queryParameters: {'v': '1', 'space': serviceScope(credentials)},
    ).toString();
  }

  static QrInvite decode(String raw, SessionCredentials credentials) {
    if (raw.isEmpty || utf8.encode(raw).length > 2048 ||
        raw.trim() != raw || RegExp(r'[\x00-\x1f\x7f]').hasMatch(raw)) {
      throw const QrInviteException(QrInviteRejection.invalid);
    }
    // Historical brand is read-only compatibility input. Never generate it.
    const legacyPrefixes = {
      'com.jrtxa.wanfeng/addFriend/': QrInviteKind.user,
      'com.jrtxa.wanfeng/joinGroup/': QrInviteKind.group,
    };
    for (final entry in legacyPrefixes.entries) {
      if (raw.startsWith(entry.key)) {
        final id = raw.substring(entry.key.length);
        if (!validId(id)) {
          throw const QrInviteException(QrInviteRejection.invalid);
        }
        // The caller must explicitly confirm the current service, because an
        // old code contains no issuer or tenant binding.
        return QrInvite._(entry.value, id, true);
      }
    }
    try {
      final uri = Uri.parse(raw);
      if (uri.scheme != 'luoboduihua') {
        throw const QrInviteException(QrInviteRejection.unsupported);
      }
      if (uri.host != 'qr' || uri.userInfo.isNotEmpty || uri.hasPort ||
          uri.hasFragment || uri.pathSegments.length != 2 ||
          !validId(uri.pathSegments[1])) {
        throw const QrInviteException(QrInviteRejection.invalid);
      }
      final parameters = uri.queryParametersAll;
      if (parameters.length != 2 || parameters['v']?.length != 1 ||
          parameters['v']?.single != '1' || parameters['space']?.length != 1 ||
          !RegExp(r'^[0-9a-f]{64}$').hasMatch(parameters['space']!.single)) {
        throw const QrInviteException(QrInviteRejection.invalid);
      }
      final kind = switch (uri.pathSegments[0]) {
        'user' => QrInviteKind.user,
        'group' => QrInviteKind.group,
        _ => throw const QrInviteException(QrInviteRejection.unsupported),
      };
      if (parameters['space']!.single != serviceScope(credentials)) {
        throw const QrInviteException(QrInviteRejection.otherService);
      }
      return QrInvite._(kind, uri.pathSegments[1], false);
    } on QrInviteException {
      rethrow;
    } on FormatException {
      throw const QrInviteException(QrInviteRejection.invalid);
    }
  }
}
