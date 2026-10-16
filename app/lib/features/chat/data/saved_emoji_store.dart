import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:crypto/crypto.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/tenant/session_access.dart';
import '../domain/saved_emoji.dart';

final class SavedEmojiSnapshot {
  SavedEmojiSnapshot(
    this.revision,
    Iterable<SavedEmoji> entries,
    this.pendingUpload,
  ) : entries = List.unmodifiable(entries);
  final String revision;
  final List<SavedEmoji> entries;
  // Only an opaque nonce. No local image, presigned URL/header or credential.
  final String? pendingUpload;
}

final class SavedEmojiChange {
  const SavedEmojiChange(this.accountKey);
  final String accountKey;
}

/// One account-scoped preferences write is the atomic list/pending-pointer
/// boundary. The singleton serializes all screens, and rejects stale snapshots.
final class SavedEmojiStore {
  SavedEmojiStore({
    Future<String?> Function(String)? readString,
    Future<void> Function(String, String)? writeString,
  }) : _read = readString,
       _write = writeString;
  final Future<String?> Function(String)? _read;
  final Future<void> Function(String, String)? _write;
  SharedPreferencesAsync? _preferences;
  Future<void> _tail = Future<void>.value();
  final _changes = StreamController<SavedEmojiChange>.broadcast(sync: true);
  Stream<SavedEmojiChange> get changes => _changes.stream;
  bool _disposed = false;
  static final _random = Random.secure();
  static String _nonce() => List.generate(
    16,
    (_) => _random.nextInt(256).toRadixString(16).padLeft(2, '0'),
  ).join();
  static String keyFor(String account) {
    if (!RegExp(r'^[a-f0-9]{64}$').hasMatch(account)) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    return 'luobo.saved-emoji.v1.$account';
  }

  void _check(SessionAccess access, bool Function() current) {
    access.check();
    if (_disposed ||
        !identical(access.session.state.credentials, access.credentials) ||
        !current()) {
      throw const AppFailure(FailureKind.cancelled);
    }
  }

  Future<T> _ordered<T>(
    SessionAccess access,
    bool Function() current,
    Future<T> Function() action,
  ) {
    final result = _tail.then((_) async {
      _check(access, current);
      try {
        return await action();
      } on AppFailure {
        rethrow;
      } catch (_) {
        throw const AppFailure(FailureKind.localStorage);
      }
    });
    _tail = result.then<void>((_) {}, onError: (Object _, StackTrace _) {});
    return result;
  }

  Future<String?> _get(String key) => _read != null
      ? _read(key)
      : (_preferences ??= SharedPreferencesAsync()).getString(key);
  Future<void> _set(String key, String body) => _write != null
      ? _write(key, body)
      : (_preferences ??= SharedPreferencesAsync()).setString(key, body);
  SavedEmojiSnapshot _decode(String? raw) {
    final revision = sha256.convert(utf8.encode(raw ?? 'absent')).toString();
    if (raw == null) {
      return SavedEmojiSnapshot(revision, const [], null);
    }
    final data = jsonDecode(raw);
    if (data is! Map ||
        data.keys.any(
          (k) => !const {'version', 'items', 'pending'}.contains(k),
        ) ||
        data['version'] is! int ||
        data['version'] != 1 ||
        data['items'] is! List ||
        data['pending'] != null &&
            (data['pending'] is! String ||
                !RegExp(r'^[a-f0-9]{32}$')
                    .hasMatch(data['pending'] as String))) {
      throw const AppFailure(FailureKind.localStorage);
    }
    final entries = (data['items'] as List).map(SavedEmoji.fromJson).toList();
    if (entries.map((e) => e.id).toSet().length != entries.length) {
      throw const AppFailure(FailureKind.localStorage);
    }
    return SavedEmojiSnapshot(revision, entries, data['pending'] as String?);
  }

  Future<SavedEmojiSnapshot> read(
    SessionAccess access, {
    required bool Function() current,
  }) => _ordered(access, current, () async {
    final raw = await _get(keyFor(access.credentials.accountKey));
    _check(access, current);
    return _decode(raw);
  });
  Future<SavedEmojiSnapshot> _change(
    SessionAccess access,
    SavedEmojiSnapshot expected,
    bool Function() current,
    ({List<SavedEmoji> entries, String? pending}) Function(SavedEmojiSnapshot)
    edit,
  ) => _ordered(access, current, () async {
    final account = access.credentials.accountKey, key = keyFor(account);
    final raw = await _get(key);
    _check(access, current);
    final old = _decode(raw);
    if (old.revision != expected.revision) {
      throw const AppFailure(FailureKind.cancelled);
    }
    final next = edit(old);
    final body = jsonEncode({
      'version': 1,
      'items': next.entries.map((e) => e.toJson()).toList(),
      'pending': next.pending,
    });
    _check(access, current);
    try {
      await _set(key, body);
    } catch (_) {
      // A completed preferences Future may have lost its reply. Re-read only
      // this frozen key; exact desired bytes prove application, never replay.
      if (await _get(key) != body) {
        rethrow;
      }
    }
    if (!_disposed) _changes.add(SavedEmojiChange(account));
    _check(access, current);
    return _decode(body);
  });
  Future<SavedEmojiSnapshot> add(
    SessionAccess access,
    SavedEmojiSnapshot expected,
    SavedEmoji entry, {
    required bool Function() current,
  }) => _change(
    access,
    expected,
    current,
    (old) => (
      entries: [entry, ...old.entries.where((e) => e.id != entry.id)],
      pending: old.pendingUpload,
    ),
  );
  Future<SavedEmojiSnapshot> remove(
    SessionAccess access,
    SavedEmojiSnapshot expected,
    Set<String> ids, {
    required bool Function() current,
  }) => _change(
    access,
    expected,
    current,
    (old) => (
      entries: old.entries.where((e) => !ids.contains(e.id)).toList(),
      pending: old.pendingUpload,
    ),
  );
  Future<SavedEmojiSnapshot> beginUpload(
    SessionAccess access,
    SavedEmojiSnapshot expected, {
    required bool Function() current,
  }) => _change(access, expected, current, (old) {
    if (old.pendingUpload != null) {
      throw const AppFailure(FailureKind.cancelled);
    }
    return (entries: old.entries, pending: _nonce());
  });
  Future<SavedEmojiSnapshot> finishUpload(
    SessionAccess access,
    SavedEmojiSnapshot expected,
    String nonce,
    SavedEmoji receipt, {
    required bool Function() current,
  }) => _change(access, expected, current, (old) {
    if (old.pendingUpload != nonce || receipt.source != null) {
      throw const AppFailure(FailureKind.cancelled);
    }
    return (
      entries: [receipt, ...old.entries.where((e) => e.id != receipt.id)],
      pending: null,
    );
  });
  Future<SavedEmojiSnapshot> endPending(
    SessionAccess access,
    SavedEmojiSnapshot expected, {
    required bool Function() current,
  }) => _change(
    access,
    expected,
    current,
    (old) => (entries: old.entries, pending: null),
  );
  Future<void> flush() => _tail;
  void dispose() {
    _disposed = true;
    unawaited(_changes.close());
  }
}
