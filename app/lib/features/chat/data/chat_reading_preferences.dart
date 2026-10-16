import 'dart:async';

import 'package:shared_preferences/shared_preferences.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/tenant/session_coordinator.dart';
import '../../../core/tenant/session_credentials.dart';

const defaultChatFontFactor = 1.0;
const minimumChatFontFactor = .85;
const maximumChatFontFactor = 2.0;

bool validChatFontFactor(double factor) => factor.isFinite &&
    factor >= minimumChatFontFactor && factor <= maximumChatFontFactor;

final class ChatReadingChange {
  const ChatReadingChange(this.accountKey, this.factor);
  final String accountKey;
  final double factor;
}

/// One small preference, with no message data, credential or raw account name.
/// The injected functions are the OS preferences boundary, not a second store.
final class ChatReadingPreferencesStore {
  ChatReadingPreferencesStore({
    Future<double?> Function(String)? readDouble,
    Future<void> Function(String, double)? writeDouble,
    SharedPreferencesAsync Function()? preferences,
  }) : this._(readDouble, writeDouble, preferences ?? SharedPreferencesAsync.new);
  ChatReadingPreferencesStore._(this._readDouble, this._writeDouble,
      this._preferencesFactory);
  final Future<double?> Function(String)? _readDouble;
  final Future<void> Function(String, double)? _writeDouble;
  final SharedPreferencesAsync Function() _preferencesFactory;
  SharedPreferencesAsync? _preferences;
  Future<void> _tail = Future<void>.value();
  final _changes = StreamController<ChatReadingChange>.broadcast(sync: true);
  bool _disposed = false;
  Stream<ChatReadingChange> get changes => _changes.stream;

  static String keyFor(String accountKey) {
    if (!RegExp(r'^[a-f0-9]{64}$').hasMatch(accountKey)) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    return 'luobo.chat-reading.v1.$accountKey';
  }

  Future<T> _ordered<T>(bool Function() current, Future<T> Function() action) {
    final result = _tail.then((_) async {
      if (_disposed || !current()) {
        throw const AppFailure(FailureKind.cancelled);
      }
      try {
        return await action();
      } on AppFailure {
        rethrow;
      } catch (_) {
        throw const AppFailure(FailureKind.localStorage);
      }
    });
    // An error belongs to its original caller and must not poison later reads.
    _tail = result.then<void>((_) {}, onError: (Object _, StackTrace _) {});
    return result;
  }

  Future<double> read(String accountKey, {required bool Function() current}) {
    final key = keyFor(accountKey);
    return _ordered(current, () async {
      final value = _readDouble != null
          ? await _readDouble(key)
          : await (_preferences ??= _preferencesFactory()).getDouble(key);
      if (!current()) throw const AppFailure(FailureKind.cancelled);
      if (value != null && !validChatFontFactor(value)) {
        throw const AppFailure(FailureKind.localStorage);
      }
      return value ?? defaultChatFontFactor;
    });
  }

  Future<void> save(String accountKey, double factor,
      {required bool Function() current}) {
    final key = keyFor(accountKey);
    if (!validChatFontFactor(factor)) {
      throw const AppFailure(FailureKind.invalidInput);
    }
    return _ordered(current, () async {
      if (_writeDouble != null) {
        await _writeDouble(key, factor);
      } else {
        await (_preferences ??= _preferencesFactory()).setDouble(key, factor);
      }
      // A dispatched write still belongs to its frozen key if the view leaves.
      // Notifications contain no credentials; listeners check their own owner.
      if (!_disposed) _changes.add(ChatReadingChange(accountKey, factor));
      if (!current()) throw const AppFailure(FailureKind.cancelled);
    });
  }

  void dispose() {
    _disposed = true;
    unawaited(_changes.close());
  }
}

/// A page's original owner; never renew this binding from a late callback.
final class ChatReadingPreferences {
  ChatReadingPreferences(this.store, this.session)
      : _owner = session.state.credentials,
        _generation = session.state.generation,
        _interactiveRevision = session.interactiveRevision,
        _issuer = session.state.credentials?.issuer,
        _tenant = session.state.credentials?.tenantId;
  final ChatReadingPreferencesStore store;
  final SessionCoordinator session;
  final SessionCredentials? _owner;
  final int _generation, _interactiveRevision;
  final String? _issuer, _tenant;
  String get accountKey => _owner?.accountKey ?? '';
  bool get current => _owner != null &&
      identical(session.state.credentials, _owner) &&
      session.state.credentials?.issuer == _issuer &&
      session.state.credentials?.tenantId == _tenant &&
      session.isInteractiveCurrent(_generation, _interactiveRevision);
  Future<double> read() => store.read(accountKey, current: () => current);
  Future<void> save(double factor, {required bool Function() sourceCurrent}) =>
      store.save(accountKey, factor, current: () => current && sourceCurrent());
}
