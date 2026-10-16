import 'dart:convert';

import 'package:crypto/crypto.dart';
import 'package:dio/dio.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/network/api_client.dart';
import '../../../core/tenant/session_access.dart';

enum PasswordChangeOutcome { confirmed, unconfirmed }

enum AccountCancellationOutcome {
  sessionsRevoked,
  revocationPending,
  unconfirmed,
}

/// Credential changes intentionally revoke the current SDK connection. The
/// captured one-shot transport must survive that expected local invalidation.
final class AccountSecurityRepository {
  AccountSecurityRepository(
    this.access, {
    Dio Function()? transport,
    this.timeout = const Duration(seconds: 10),
  }) : _transport =
           transport ??
           (() => Dio(BaseOptions(connectTimeout: const Duration(seconds: 5))));
  final SessionAccess access;
  final Dio Function() _transport;
  final Duration timeout;

  bool _definiteRejection(AppFailure failure) {
    final code = failure.code;
    return const {1001, 1002, 20013, 20018}.contains(code) ||
        (code != null && code >= 1501 && code <= 1507);
  }

  Future<AccountCancellationOutcome> cancelAccount(
    String password, {
    String googleAuthCode = '',
  }) async {
    access.check();
    if (password.isEmpty ||
        password.length > 1024 ||
        (googleAuthCode.isNotEmpty &&
            !RegExp(r'^\d{6}$').hasMatch(googleAuthCode))) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final pair = access.credentials;
    final dio = _transport();
    final client = ApiClient(
      base: pair.endpoints.chat,
      dio: dio,
      token: pair.chatToken,
    );
    try {
      final data = await client
          .post('account/cancel', {
            'userID': pair.userId,
            'password': md5.convert(utf8.encode(password)).toString(),
            if (googleAuthCode.isNotEmpty) 'googleAuthCode': googleAuthCode,
          })
          .timeout(timeout);
      // An accepted cancellation is a durable login prohibition. It is not a
      // claim that retained history/backups were erased, or cleanup completed.
      if (data is! Map ||
          data['userID'] != pair.userId ||
          data['dataErased'] != false ||
          data['requestedAt'] is! int ||
          (data['requestedAt'] as int) <= 0) {
        return AccountCancellationOutcome.unconfirmed;
      }
      return switch (data['status']) {
        'sessions_revoked' => AccountCancellationOutcome.sessionsRevoked,
        'cancellation_pending' => AccountCancellationOutcome.revocationPending,
        _ => AccountCancellationOutcome.unconfirmed,
      };
    } on AppFailure catch (error) {
      if (_definiteRejection(error)) rethrow;
      return AccountCancellationOutcome.unconfirmed;
    } catch (_) {
      return AccountCancellationOutcome.unconfirmed;
    } finally {
      client.close();
      dio.close(force: true);
    }
  }

  Future<PasswordChangeOutcome> changePassword(
    String current,
    String next,
  ) async {
    access.check();
    if (current.isEmpty ||
        current.length > 1024 ||
        next.length < 8 ||
        next.length > 128 ||
        current == next ||
        RegExp(r'[\x00-\x1f\x7f]').hasMatch(next)) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final pair = access.credentials;
    final dio = _transport();
    final client = ApiClient(
      base: pair.endpoints.chat,
      dio: dio,
      token: pair.chatToken,
    );
    String digest(String value) => md5.convert(utf8.encode(value)).toString();
    try {
      await client
          .post('account/password/change', {
            'userID': pair.userId,
            'currentPassword': digest(current),
            'newPassword': digest(next),
          })
          .timeout(timeout);
      return PasswordChangeOutcome.confirmed;
    } on AppFailure catch (error) {
      // A received rejection has a definite outcome. A lost response may follow
      // a committed password change; do not automatically submit it again.
      if (_definiteRejection(error)) rethrow;
      return PasswordChangeOutcome.unconfirmed;
    } catch (_) {
      return PasswordChangeOutcome.unconfirmed;
    } finally {
      client.close();
      dio.close(force: true);
    }
  }
}
