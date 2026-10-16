import 'dart:convert';

import 'package:crypto/crypto.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/network/api_client.dart';
import '../../../core/tenant/session_credentials.dart';

enum VerificationPurpose {
  register(1),
  resetPassword(2),
  login(3);

  const VerificationPurpose(this.value);
  final int value;
}

final class AuthIdentity {
  AuthIdentity({required String account, this.areaCode = '+86'})
    : account = account.trim() {
    if (this.account.isEmpty) throw const AppFailure(FailureKind.invalidInput);
  }
  final String account;
  final String areaCode;
  bool get isEmail => account.contains('@');
  Map<String, Object?> get payload => {
    'areaCode': areaCode,
    if (isEmail) 'email': account else 'phoneNumber': account,
  };
}

final class DeviceIdentity {
  const DeviceIdentity({
    required this.id,
    required this.platform,
    required this.model,
  });
  final String id;
  final int platform;
  final String model;
  Map<String, Object> get payload => {
    'deviceID': id,
    'platform': platform,
    'deviceModel': model,
  };
}

final class RegistrationRules {
  const RegistrationRules({
    this.invitationRequired = false,
    this.verificationRequired = true,
  });
  factory RegistrationRules.fromJson(Map data) {
    bool truth(Object? raw, bool fallback) {
      if (raw == null || '$raw'.isEmpty) return fallback;
      return !['0', 'false', 'no'].contains('$raw'.toLowerCase());
    }

    return RegistrationRules(
      invitationRequired: truth(data['needInvitationCodeRegister'], false),
      verificationRequired: truth(data['needVerificationCodeRegister'], true),
    );
  }
  final bool invitationRequired;
  final bool verificationRequired;
}

final class AuthRepository {
  AuthRepository(this._api, this.device);
  final ApiClient _api;
  final DeviceIdentity device;

  Future<SessionCredentials> login({
    required AuthIdentity identity,
    String? password,
    String? verificationCode,
    String? googleAuthCode,
  }) async {
    if ((password == null || password.isEmpty) &&
        (verificationCode == null || verificationCode.isEmpty)) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final data = await _api.post('api/auth/login', {
      ...identity.payload,
      ...device.payload,
      if (password != null && password.isNotEmpty)
        'password': _digest(password),
      if (verificationCode != null && verificationCode.isNotEmpty)
        'verificationCode': verificationCode,
      if (googleAuthCode != null && googleAuthCode.isNotEmpty)
        'googleAuthCode': googleAuthCode,
    });
    return _credentials(data);
  }

  Future<SessionCredentials> register({
    required AuthIdentity identity,
    required String password,
    required String nickname,
    String verifyCode = '',
    String invitationCode = '',
  }) async {
    if (password.length < 8 || nickname.trim().isEmpty) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final data = await _api.post('api/auth/register', {
      ...device.payload,
      'verifyCode': verifyCode,
      'invitationCode': invitationCode,
      'autoLogin': true,
      'user': {
        ...identity.payload,
        'nickname': nickname.trim(),
        'password': _digest(password),
        'birth': 0,
        'gender': 1,
      },
    });
    return _credentials(data);
  }

  Future<void> sendCode(
    AuthIdentity identity,
    VerificationPurpose purpose, {
    String invitationCode = '',
  }) async {
    await _api.post('api/verify/send', {
      ...identity.payload,
      'usedFor': purpose.value,
      'invitationCode': invitationCode,
    });
  }

  Future<void> resetPassword({
    required AuthIdentity identity,
    required String password,
    required String verifyCode,
  }) async {
    if (password.length < 8 || verifyCode.isEmpty) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    await _api.post('api/auth/reset-password', {
      ...identity.payload,
      'password': _digest(password),
      'verifyCode': verifyCode,
      'platform': device.platform,
    });
  }

  Future<RegistrationRules> registrationRules({
    String invitationCode = '',
  }) async {
    final data = await _api.post('api/client_config/get', {
      'invitationCode': invitationCode,
    });
    if (data is! Map || data['config'] is! Map) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return RegistrationRules.fromJson(data['config'] as Map);
  }

  SessionCredentials _credentials(Object? data) {
    if (data is! Map<String, dynamic>) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return SessionCredentials.fromLogin(data, issuer: _api.base);
  }

  // Required only by the existing protocol; transport is HTTPS and new backend
  // password storage is independent from this wire representation.
  String _digest(String raw) => md5.convert(utf8.encode(raw)).toString();
}
