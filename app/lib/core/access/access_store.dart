import 'dart:convert';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';

/// Local protection settings have their own storage namespace. Tokens and the
/// SDK database are never migrated, deleted, or reconfigured by this store.
abstract interface class AccessStore {
  Future<AccessPolicy?> read(String owner);
  Future<void> write(String owner, AccessPolicy policy);
  Future<void> remove(String owner);
}

final class AccessPolicy {
  const AccessPolicy({
    required this.salt,
    required this.verifier,
    this.iterations = 210000,
    this.failures = 0,
    this.lastAttemptMilliseconds = 0,
    this.retryAfterMilliseconds = 0,
    this.biometric = false,
  });
  final String salt;
  final String verifier;
  final int iterations;
  final int failures;
  final int lastAttemptMilliseconds;
  final int retryAfterMilliseconds;
  final bool biometric;

  factory AccessPolicy.fromJson(Map<String, dynamic> json) {
    if (json['version'] != 1 ||
        json['algorithm'] != 'pbkdf2-hmac-sha256' ||
        json['biometric'] is! bool) {
      throw const FormatException('Invalid access policy');
    }
    final policy = AccessPolicy(
      salt: json['salt'] as String,
      verifier: json['verifier'] as String,
      iterations: json['iterations'] as int,
      failures: json['failures'] as int,
      lastAttemptMilliseconds: json['lastAttemptMilliseconds'] as int,
      retryAfterMilliseconds: json['retryAfterMilliseconds'] as int,
      biometric: json['biometric'] as bool,
    );
    if (base64Decode(policy.salt).length != 32 ||
        base64Decode(policy.verifier).length != 32 ||
        policy.iterations < 210000 ||
        policy.iterations > 1000000 ||
        policy.failures < 0 ||
        policy.failures > 1000000 ||
        policy.lastAttemptMilliseconds < 0 ||
        policy.retryAfterMilliseconds < 0) {
      throw const FormatException('Invalid access policy');
    }
    return policy;
  }

  AccessPolicy copy({
    int? failures,
    int? attemptedAt,
    int? retryAfter,
    bool? biometric,
  }) => AccessPolicy(
    salt: salt,
    verifier: verifier,
    iterations: iterations,
    failures: failures ?? this.failures,
    lastAttemptMilliseconds: attemptedAt ?? lastAttemptMilliseconds,
    retryAfterMilliseconds: retryAfter ?? retryAfterMilliseconds,
    biometric: biometric ?? this.biometric,
  );

  Map<String, dynamic> toJson() => {
    'version': 1,
    'algorithm': 'pbkdf2-hmac-sha256',
    'salt': salt,
    'verifier': verifier,
    'iterations': iterations,
    'failures': failures,
    'lastAttemptMilliseconds': lastAttemptMilliseconds,
    'retryAfterMilliseconds': retryAfterMilliseconds,
    'biometric': biometric,
  };

  @override
  String toString() => 'AccessPolicy(<redacted>)';
}

final class SecureAccessStore implements AccessStore {
  SecureAccessStore([FlutterSecureStorage? storage])
    : _storage =
          storage ??
          const FlutterSecureStorage(
            aOptions: AndroidOptions(
              storageNamespace: 'luobo.app_access.v1',
              resetOnError: false,
              migrateOnAlgorithmChange: false,
            ),
            iOptions: IOSOptions(
              accountName: 'luobo.app_access.v1',
              accessibility: KeychainAccessibility.unlocked_this_device,
            ),
          );
  final FlutterSecureStorage _storage;
  String _key(String owner) {
    if (!RegExp(r'^[0-9a-f]{64}$').hasMatch(owner)) {
      throw const FormatException('Invalid access owner');
    }
    return 'owner.$owner';
  }

  @override
  Future<AccessPolicy?> read(String owner) async {
    final value = await _storage.read(key: _key(owner));
    if (value == null) return null;
    return AccessPolicy.fromJson(jsonDecode(value) as Map<String, dynamic>);
  }

  @override
  Future<void> write(String owner, AccessPolicy policy) =>
      _storage.write(key: _key(owner), value: jsonEncode(policy.toJson()));
  @override
  Future<void> remove(String owner) => _storage.delete(key: _key(owner));
}
