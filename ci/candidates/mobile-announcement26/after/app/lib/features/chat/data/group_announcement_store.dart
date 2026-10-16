import 'dart:async';
import 'dart:convert';

import 'package:crypto/crypto.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../../core/error/app_failure.dart';
import '../../group/domain/group.dart';

/// A fresh group metadata snapshot, never a received-message payload or grant.
final class GroupAnnouncementSnapshot {
  GroupAnnouncementSnapshot.fromProfile(GroupProfile profile)
    : groupId = profile.summary.id,
      text = profile.announcement,
      updatedAt = profile.announcementUpdatedAt,
      publisherId = profile.announcementPublisherId {
    if (!profile.joined ||
        profile.me == null ||
        !const {0, 3}.contains(profile.summary.status)) {
      throw const AppFailure(FailureKind.permissionDenied);
    }
    if (!_identifier(groupId) ||
        !_text(text, 10000, multiline: true) ||
        !_text(publisherId, 128) ||
        updatedAt < 0 ||
        updatedAt > 9007199254740991) {
      throw const AppFailure(FailureKind.invalidResponse);
    }
  }

  final String groupId, text, publisherId;
  final int updatedAt;
  bool get empty => text.trim().isEmpty;
  String get version => sha256
      .convert(utf8.encode(jsonEncode([groupId, updatedAt, publisherId, text])))
      .toString();

  static bool _identifier(String value) =>
      value.isNotEmpty &&
      value.trim() == value &&
      _text(value, 128) &&
      !RegExp(r'\s').hasMatch(value);

  static bool _text(String value, int maximum, {bool multiline = false}) {
    if (value.length > maximum || utf8.decode(utf8.encode(value)) != value) {
      return false;
    }
    final controls = multiline
        ? RegExp(
            r'[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f-\u009f\u061c\u200e\u200f\u202a-\u202e\u2066-\u2069]',
          )
        : RegExp(
            r'[\u0000-\u001f\u007f-\u009f\u061c\u200e\u200f\u202a-\u202e\u2066-\u2069]',
          );
    return !controls.hasMatch(value);
  }
}

/// Local exact-version acknowledgement only. No SDK hint reset, content,
/// credentials, names or URLs are stored; this is not a cross-device receipt.
final class GroupAnnouncementStore {
  GroupAnnouncementStore({
    Future<bool?> Function(String)? readBool,
    Future<void> Function(String, bool)? writeBool,
    SharedPreferencesAsync Function()? preferences,
  }) : this._(readBool, writeBool, preferences);

  GroupAnnouncementStore._(
    this._readBool,
    this._writeBool,
    SharedPreferencesAsync Function()? preferences,
  ) : _factory = preferences ?? SharedPreferencesAsync.new;

  final Future<bool?> Function(String)? _readBool;
  final Future<void> Function(String, bool)? _writeBool;
  final SharedPreferencesAsync Function() _factory;
  SharedPreferencesAsync? _preferences;
  Future<void> _tail = Future<void>.value();
  bool _closed = false;

  static String keyFor(String accountKey, String groupId, String version) {
    if (!RegExp(r'^[a-f0-9]{64}$').hasMatch(accountKey) ||
        !RegExp(r'^[a-f0-9]{64}$').hasMatch(version) ||
        !GroupAnnouncementSnapshot._identifier(groupId)) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    final group = sha256.convert(utf8.encode(groupId));
    return 'luobo.group-announcement.v1.$accountKey.$group.$version';
  }

  Future<T> _ordered<T>(bool Function() current, Future<T> Function() action) {
    final future = _tail.then((_) async {
      if (_closed || !current()) {
        throw const AppFailure(FailureKind.cancelled);
      }
      try {
        final value = await action().timeout(const Duration(seconds: 10));
        if (!current()) {
          throw const AppFailure(FailureKind.cancelled);
        }
        return value;
      } on AppFailure {
        rethrow;
      } catch (_) {
        throw const AppFailure(FailureKind.localStorage);
      }
    });
    _tail = future.then<void>((_) {}, onError: (Object _, StackTrace _) {});
    return future;
  }

  Future<bool> dismissed(
    String accountKey,
    GroupAnnouncementSnapshot snapshot, {
    required bool Function() current,
  }) {
    final key = keyFor(accountKey, snapshot.groupId, snapshot.version);
    return _ordered(current, () async {
      final value = _readBool != null
          ? await _readBool(key)
          : await (_preferences ??= _factory()).getBool(key);
      return value == true;
    });
  }

  Future<void> dismiss(
    String accountKey,
    GroupAnnouncementSnapshot snapshot, {
    required bool Function() current,
  }) {
    final key = keyFor(accountKey, snapshot.groupId, snapshot.version);
    return _ordered(current, () async {
      if (_writeBool != null) {
        await _writeBool(key, true);
      } else {
        await (_preferences ??= _factory()).setBool(key, true);
      }
      // A dispatched original-key write is not undone when the view retires.
      // All other versions have distinct keys; there is no broad delete/reset.
    });
  }

  Future<void> drain() => _tail;
  Future<void> close() {
    _closed = true;
    return _tail;
  }
}
