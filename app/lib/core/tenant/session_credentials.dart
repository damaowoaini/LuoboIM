import 'dart:convert';

import 'package:crypto/crypto.dart';

import '../config/app_config.dart';
import '../error/app_failure.dart';

final class TenantEndpoints {
  TenantEndpoints.fromDomain(String raw) {
    final normalized = raw.trim();
    final uri = validateHttps(
      normalized.contains('://') ? normalized : 'https://$normalized',
    );
    if ((uri.path.isNotEmpty && uri.path != '/') || uri.hasQuery) {
      throw const AppFailure(FailureKind.configuration);
    }
    origin = uri.replace(path: '', query: null, fragment: null);
  }

  late final Uri origin;
  Uri get chat => origin.resolve('/chat/');
  Uri get im => origin.resolve('/api/');
  Uri get socket => origin.replace(scheme: 'wss', path: '/msg_gateway');
}

final class SessionCredentials {
  SessionCredentials({
    required this.userId,
    required this.tenantId,
    required this.tenantName,
    required List<String> domains,
    required this.chatToken,
    required this.imToken,
    String? issuer,
    this.lineIndex = 0,
  }) : domains = List.unmodifiable(domains) {
    if ([userId, tenantId, chatToken, imToken].any((s) => s.trim().isEmpty) ||
        domains.isEmpty ||
        lineIndex < 0 ||
        lineIndex >= domains.length) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    for (final domain in domains) {
      TenantEndpoints.fromDomain(domain);
    }
    // Authentication issuers can each reuse the same tenant/user identifiers.
    // Manually constructed credentials default to their endpoint. Persisted
    // legacy records require a new issuer-bound login before SDK initialization.
    final source = validateHttps(
      issuer ?? TenantEndpoints.fromDomain(domains.first).origin.toString(),
    );
    if (source.hasQuery) throw const AppFailure(FailureKind.configuration);
    this.issuer = source
        .replace(
          path: source.path.endsWith('/') ? source.path : '${source.path}/',
        )
        .toString();
  }

  factory SessionCredentials.fromJson(Map<String, dynamic> json) {
    if (json['schemaVersion'] != 2 ||
        json['issuer'] is! String ||
        (json['issuer'] as String).isEmpty) {
      throw const AppFailure(FailureKind.sessionUpgrade);
    }
    try {
      return SessionCredentials(
        userId: json['userId'] as String,
        tenantId: json['tenantId'] as String,
        tenantName: json['tenantName'] as String,
        domains: (json['domains'] as List).cast<String>(),
        chatToken: json['chatToken'] as String,
        imToken: json['imToken'] as String,
        issuer: json['issuer'] as String?,
        lineIndex: json['lineIndex'] as int? ?? 0,
      );
    } on AppFailure {
      rethrow;
    } catch (_) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
  }

  factory SessionCredentials.fromLogin(
    Map<String, dynamic> json, {
    required Uri issuer,
  }) {
    final info = json['serverInfo'];
    if (info is! Map) throw const AppFailure(FailureKind.invalidResponse);
    final raw = info['domains'];
    final domains = switch (raw) {
      String value => value.split(','),
      List value => value.whereType<String>().toList(),
      _ => <String>[],
    }.map((s) => s.trim()).where((s) => s.isNotEmpty).toSet().toList();
    if (domains.isEmpty) throw const AppFailure(FailureKind.invalidResponse);
    String requiredString(String key) {
      final value = json[key];
      if (value is! String || value.isEmpty) {
        throw const AppFailure(FailureKind.invalidResponse);
      }
      return value;
    }

    return SessionCredentials(
      userId: requiredString('userID'),
      tenantId:
          '${info['serverID'] ?? info['id'] ?? TenantEndpoints.fromDomain(domains.first).origin.authority}',
      tenantName: info['serverName'] is String
          ? info['serverName'] as String
          : '当前服务',
      domains: domains,
      chatToken: requiredString('chatToken'),
      imToken: requiredString('imToken'),
      issuer: issuer.toString(),
    );
  }

  final String userId;
  final String tenantId;
  final String tenantName;
  final List<String> domains;
  final String chatToken;
  final String imToken;
  late final String issuer;
  final int lineIndex;

  TenantEndpoints get endpoints =>
      TenantEndpoints.fromDomain(domains[lineIndex]);
  String get accountKey => sha256
      .convert(utf8.encode(jsonEncode([issuer, tenantId, userId])))
      .toString();

  SessionCredentials onLine(int index) => SessionCredentials(
    userId: userId,
    tenantId: tenantId,
    tenantName: tenantName,
    domains: domains,
    chatToken: chatToken,
    imToken: imToken,
    issuer: issuer,
    lineIndex: index,
  );

  Map<String, dynamic> toJson() => {
    'userId': userId,
    'tenantId': tenantId,
    'tenantName': tenantName,
    'domains': domains,
    'chatToken': chatToken,
    'imToken': imToken,
    'lineIndex': lineIndex,
    'issuer': issuer,
    'schemaVersion': 2,
  };

  @override
  String toString() => 'SessionCredentials(<redacted>)';
}
