import 'dart:convert';

import '../../../core/error/app_failure.dart';
import '../../../core/im/open_im_gateway.dart';
import '../../../core/network/tenant_api.dart';
import '../../chat/data/message_mapper.dart' show secureMediaUri;

enum AccountPreference { friendRequests, sound, vibration }

final class AccountSaveResult {
  const AccountSaveResult(this.profile, {this.imSyncPending = false});
  final AccountProfile profile;
  final bool imSyncPending;
}

final class AccountProfile {
  const AccountProfile({
    required this.userId,
    required this.nickname,
    this.avatar,
    this.hasStoredAvatar = false,
    this.email = '',
    this.phone = '',
    this.areaCode = '',
    this.gender = 0,
    this.birthday,
    this.friendRequests = false,
    this.sound = false,
    this.vibration = false,
    this.globalReceive = 0,
  });
  final String userId, nickname, email, phone, areaCode;
  final Uri? avatar;
  final bool hasStoredAvatar;
  final int gender, globalReceive;
  final DateTime? birthday;
  final bool friendRequests, sound, vibration;

  bool enabled(AccountPreference preference) => switch (preference) {
    AccountPreference.friendRequests => friendRequests,
    AccountPreference.sound => sound,
    AccountPreference.vibration => vibration,
  };
}

/// Profile edits never change verified login identities or privilege fields.
/// Every action keeps the account and service roots captured at construction.
final class AccountRepository {
  AccountRepository(this.api, this.gateway);
  final TenantApi api;
  final OpenImGateway gateway;
  bool _clearingHistory = false;
  bool get isCurrent => api.access.isCurrent;

  Future<void> clearAllHistory() async {
    api.access.check();
    if (_clearingHistory) {
      throw const AppFailure(FailureKind.cancelled);
    }
    _clearingHistory = true;
    try {
      await gateway.clearAllMessages(api.access.generation);
      api.access.check();
    } finally {
      _clearingHistory = false;
    }
  }

  Future<AccountProfile> profile() async {
    api.access.check();
    final owner = api.access.credentials.userId;
    final raw = await api.chat.post('user/find/full', {
      'userIDs': [owner],
    });
    api.access.check();
    if (raw is! Map || raw['users'] is! List) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    final rows = raw['users'] as List;
    if (rows.length != 1 || rows.single is! Map) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    final user = rows.single as Map;
    if (user['userID'] != owner || user['nickname'] is! String) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    int integer(String key) {
      final value = user[key];
      if (value == null) return 0;
      final parsed = value is int
          ? value
          : value is String
          ? int.tryParse(value)
          : null;
      if (parsed == null) throw const AppFailure(FailureKind.invalidResponse);
      return parsed;
    }

    String text(String key) {
      final value = user[key];
      if (value == null) return '';
      if (value is! String) throw const AppFailure(FailureKind.invalidResponse);
      return value;
    }

    final birth = integer('birth');
    final gender = integer('gender');
    if (birth < DateTime(1900).millisecondsSinceEpoch ||
        birth > DateTime.now().millisecondsSinceEpoch ||
        ![0, 1, 2].contains(gender)) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    final native = await gateway.runForSession(
      api.access.generation,
      (manager) => manager.userManager.getSelfUserInfo(),
    );
    api.access.check();
    if (native.userID != owner ||
        ![0, 1, 2].contains(native.globalRecvMsgOpt ?? 0)) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return AccountProfile(
      userId: owner,
      nickname: text('nickname'),
      avatar: _avatar(text('faceURL')),
      hasStoredAvatar: text('faceURL').isNotEmpty,
      email: text('email'),
      phone: text('phoneNumber'),
      areaCode: text('areaCode'),
      gender: gender,
      birthday: birth == 0 ? null : DateTime.fromMillisecondsSinceEpoch(birth),
      friendRequests: integer('allowAddFriend') == 1,
      sound: integer('allowBeep') == 1,
      vibration: integer('allowVibration') == 1,
      globalReceive: native.globalRecvMsgOpt ?? 0,
    );
  }

  Future<AccountSaveResult> updateProfile({
    String? nickname,
    Uri? avatar,
    bool clearAvatar = false,
    int? gender,
    DateTime? birthday,
  }) async {
    api.access.check();
    final name = nickname?.trim();
    if ((name != null &&
            (name.isEmpty ||
                name.runes.length > 64 ||
                utf8.encode(name).length > 256 ||
                RegExp(r'[\x00-\x1f\x7f-\x9f]').hasMatch(name))) ||
        (clearAvatar && avatar != null) ||
        (avatar != null && _avatar(avatar.toString()) != avatar) ||
        (gender != null && ![0, 1, 2].contains(gender)) ||
        (birthday != null &&
            (birthday.year < 1900 || birthday.isAfter(DateTime.now())))) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final changes = <String, Object?>{
      'nickname': ?name,
      if (avatar != null) 'faceURL': avatar.toString(),
      if (clearAvatar) 'faceURL': '',
      'gender': ?gender,
      if (birthday != null) 'birth': birthday.millisecondsSinceEpoch,
    };
    if (changes.isEmpty) throw const AppFailure(FailureKind.invalidInput);
    final pending = await _update(changes);
    final result = await profile();
    if ((name != null && result.nickname != name) ||
        (avatar != null && result.avatar != avatar) ||
        (clearAvatar && result.hasStoredAvatar) ||
        (gender != null && result.gender != gender) ||
        (birthday != null &&
            result.birthday?.millisecondsSinceEpoch !=
                birthday.millisecondsSinceEpoch)) {
      throw const AppFailure(FailureKind.dataChanged);
    }
    return AccountSaveResult(result, imSyncPending: pending);
  }

  Future<AccountSaveResult> setPreference(
    AccountPreference preference,
    bool enabled,
  ) async {
    final field = switch (preference) {
      AccountPreference.friendRequests => 'allowAddFriend',
      AccountPreference.sound => 'allowBeep',
      AccountPreference.vibration => 'allowVibration',
    };
    final pending = await _update({field: enabled ? 1 : 2});
    final result = await profile();
    if (result.enabled(preference) != enabled) {
      throw const AppFailure(FailureKind.dataChanged);
    }
    return AccountSaveResult(result, imSyncPending: pending);
  }

  Future<bool> _update(Map<String, Object?> changes) async {
    api.access.check();
    final raw = await api.chat.post('user/update', {
      'userID': api.access.credentials.userId,
      ...changes,
    });
    api.access.check();
    if (raw is! Map ||
        (raw['imSyncPending'] != null && raw['imSyncPending'] is! bool)) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
    return raw['imSyncPending'] == true;
  }

  Uri? _avatar(String value) {
    final uri = secureMediaUri(value);
    return uri != null && !uri.hasFragment && utf8.encode(value).length <= 255
        ? uri
        : null;
  }

  Future<AccountSaveResult> setDoNotDisturb(bool enabled) async {
    api.access.check();
    final status = enabled ? 2 : 0;
    await gateway.runForSession(
      api.access.generation,
      (manager) =>
          manager.conversationManager.setGlobalRecvMessageOpt(status: status),
    );
    api.access.check();
    final result = await profile();
    if (result.globalReceive != status) {
      throw const AppFailure(FailureKind.dataChanged);
    }
    return AccountSaveResult(result);
  }
}
