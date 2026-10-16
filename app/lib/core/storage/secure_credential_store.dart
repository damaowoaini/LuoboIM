import 'dart:convert';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import '../error/app_failure.dart';
import '../tenant/session_coordinator.dart';
import '../tenant/session_credentials.dart';

/// One encrypted record prevents partial token/domain combinations after a
/// write interruption. It is intentionally isolated from the old app's bundle.
final class SecureCredentialStore implements CredentialStore {
  SecureCredentialStore(this._storage);
  final FlutterSecureStorage _storage;
  static const _key = 'luobo.session.v1';

  @override
  Future<SessionCredentials?> read() async {
    try {
      final raw = await _storage.read(key: _key);
      if (raw == null) return null;
      return SessionCredentials.fromJson(
        jsonDecode(raw) as Map<String, dynamic>,
      );
    } on AppFailure {
      rethrow;
    } catch (_) {
      throw const AppFailure(FailureKind.storage);
    }
  }

  @override
  Future<void> write(SessionCredentials credentials) async {
    try {
      await _storage.write(key: _key, value: jsonEncode(credentials.toJson()));
    } catch (_) {
      throw const AppFailure(FailureKind.storage);
    }
  }

  @override
  Future<void> clear() async {
    try {
      await _storage.delete(key: _key);
    } catch (_) {
      throw const AppFailure(FailureKind.storage);
    }
  }
}
