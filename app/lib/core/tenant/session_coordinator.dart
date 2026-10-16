import 'dart:async';

import '../error/app_failure.dart';
import 'session_credentials.dart';
import 'session_revoker.dart';

abstract interface class CredentialStore {
  Future<SessionCredentials?> read();
  Future<void> write(SessionCredentials credentials);
  Future<void> clear();
}

abstract interface class ImGateway {
  Future<void> connect(SessionCredentials credentials, int generation);
  Future<void> disconnect();
}

enum SessionPhase {
  signedOut,
  restoring,
  signingIn,
  switchingLine,
  online,
  failed,
}

final class SessionSnapshot {
  const SessionSnapshot(
    this.phase,
    this.generation, {
    this.credentials,
    this.failure,
  });
  final SessionPhase phase;
  final int generation;
  final SessionCredentials? credentials;
  final AppFailure? failure;
}

/// Serializes native SDK transitions while invalidating old requests/events
/// immediately when a newer operation is requested.
final class SessionCoordinator {
  SessionCoordinator({
    required this._store,
    required this._im,
    this._revoker,
    this.beforeRestore,
    this.authenticated,
    this.reconnected,
  });

  final Future<void> Function(SessionCredentials, int)? beforeRestore;
  final Future<void> Function(SessionCredentials, int)? authenticated;
  final Future<void> Function(SessionCredentials, int)? reconnected;

  final CredentialStore _store;
  final ImGateway _im;
  final SessionRevoker? _revoker;
  Future<void>? _signOutTask;
  final _changes = StreamController<SessionSnapshot>.broadcast(sync: true);
  final _invalidations = StreamController<int>.broadcast(sync: true);
  final _accessChanges = StreamController<int>.broadcast(sync: true);
  int _interactiveRevision = 0;
  bool _interactiveAllowed = true;
  Future<void> _tail = Future<void>.value();
  int _epoch = 0;
  bool _disposed = false;
  SessionSnapshot _state = const SessionSnapshot(SessionPhase.signedOut, 0);

  SessionSnapshot get state => _state;
  Stream<SessionSnapshot> get changes => _changes.stream;
  Stream<int> get invalidations => _invalidations.stream;
  Stream<int> get accessChanges => _accessChanges.stream;
  int get interactiveRevision => _interactiveRevision;
  bool get interactiveAllowed => _interactiveAllowed;
  bool isInteractiveCurrent(int generation, int revision) =>
      acceptsEvent(generation) &&
      _interactiveAllowed &&
      revision == _interactiveRevision;
  void updateInteractiveAccess(bool allowed, int revision) {
    if (_disposed ||
        (_interactiveAllowed == allowed && _interactiveRevision == revision)) {
      return;
    }
    _interactiveAllowed = allowed;
    _interactiveRevision = revision;
    _accessChanges.add(revision);
  }

  bool isCurrent(int generation) => !_disposed && generation == _epoch;
  bool acceptsEvent(int generation) =>
      isCurrent(generation) && _state.phase == SessionPhase.online;

  int _begin(SessionPhase phase) {
    if (_disposed) throw StateError('SessionCoordinator is disposed');
    final epoch = ++_epoch;
    _invalidations.add(epoch);
    _emit(SessionSnapshot(phase, epoch));
    return epoch;
  }

  void _emit(SessionSnapshot state) {
    if (!_disposed) {
      _state = state;
      _changes.add(state);
    }
  }

  void _check(int epoch) {
    if (!isCurrent(epoch)) throw const AppFailure(FailureKind.cancelled);
  }

  Future<T> _serialize<T>(Future<T> Function() action) {
    final result = Completer<T>();
    _tail = _tail.then((_) async {
      try {
        result.complete(await action());
      } catch (error, stack) {
        result.completeError(error, stack);
      }
    });
    return result.future;
  }

  Future<void> restore() {
    final epoch = _begin(SessionPhase.restoring);
    return () async {
      var accessGateFailed = false;
      try {
        final credentials = await _serialize(() async {
          _check(epoch);
          final value = await _store.read();
          _check(epoch);
          return value;
        });
        _check(epoch);
        if (credentials == null) {
          _emit(SessionSnapshot(SessionPhase.signedOut, epoch));
          return;
        }
        // Preserve the exact saved pair for an explicit logout from the local
        // unlock screen. This does not connect or expose it to the UI.
        _emit(
          SessionSnapshot(
            SessionPhase.restoring,
            epoch,
            credentials: credentials,
          ),
        );
        // Interactive authentication must not hold the native transition FIFO.
        // A logout/new login invalidates and cancels its separate access lease.
        try {
          await beforeRestore?.call(credentials, epoch);
        } catch (_) {
          accessGateFailed = true;
          rethrow;
        }
        await _serialize(() async {
          _check(epoch);
          await _im.connect(credentials, epoch);
          _check(epoch);
          _emit(
            SessionSnapshot(
              SessionPhase.online,
              epoch,
              credentials: credentials,
            ),
          );
        });
      } catch (error) {
        if (!isCurrent(epoch)) throw const AppFailure(FailureKind.cancelled);
        // A local lock/key/storage problem must never erase the only saved
        // credentials. The access screen remains closed with retry/logout.
        if (accessGateFailed) throw const AppFailure(FailureKind.storage);
        await _serialize(() async {
          _check(epoch);
          await _fail(epoch, error);
        });
      }
    }();
  }

  Future<void> signIn(
    Future<SessionCredentials> Function(int generation) authenticate,
  ) {
    final epoch = _begin(SessionPhase.signingIn);
    return _serialize(() async {
      _check(epoch);
      SessionCredentials? newlyIssued;
      try {
        await _im.disconnect();
        _check(epoch);
        await _store.clear();
        _check(epoch);
        final credentials = newlyIssued = await authenticate(epoch);
        _check(epoch);
        await authenticated?.call(credentials, epoch);
        _check(epoch);
        await _im.connect(credentials, epoch);
        _check(epoch);
        await _store.write(credentials);
        _check(epoch);
        _emit(
          SessionSnapshot(SessionPhase.online, epoch, credentials: credentials),
        );
      } catch (error) {
        await _fail(epoch, error, newlyIssued: newlyIssued);
      }
    });
  }

  Future<void> signOut({bool revoke = true}) {
    if (_state.phase == SessionPhase.signedOut && _signOutTask != null) {
      return _signOutTask!;
    }
    final credentials = _state.credentials;
    final epoch = _begin(SessionPhase.signedOut);
    final remote = () async {
      if (!revoke || credentials == null || _revoker == null) return null;
      try {
        return await _revoker.revoke(credentials);
      } catch (_) {
        return SessionRevocation.unconfirmed;
      }
    }();
    final task = _serialize(() async {
      _check(epoch);
      AppFailure? failure;
      try {
        await _store.clear();
      } catch (_) {
        failure = const AppFailure(FailureKind.storage);
      }
      try {
        await _im.disconnect();
      } catch (_) {
        failure ??= const AppFailure(FailureKind.unknown);
      }
      final result = await remote;
      final warning = switch (result) {
        SessionRevocation.pending => const AppFailure(
          FailureKind.logoutPending,
        ),
        SessionRevocation.unconfirmed => const AppFailure(
          FailureKind.logoutUnconfirmed,
        ),
        _ => null,
      };
      if (isCurrent(epoch)) {
        _emit(
          SessionSnapshot(
            SessionPhase.signedOut,
            epoch,
            failure: failure ?? warning,
          ),
        );
      }
      if (failure != null) throw failure;
    });
    _signOutTask = task;
    void finished() {
      if (identical(_signOutTask, task)) _signOutTask = null;
    }

    unawaited(
      task.then<void>(
        (_) => finished(),
        onError: (Object _, StackTrace _) => finished(),
      ),
    );
    return task;
  }

  Future<void> switchLine(int index) {
    final previous = _state.credentials;
    if (_state.phase != SessionPhase.online || previous == null) {
      return Future.error(const AppFailure(FailureKind.sessionExpired));
    }
    late SessionCredentials next;
    try {
      next = previous.onLine(index);
    } catch (_) {
      return Future.error(const AppFailure(FailureKind.invalidInput));
    }
    if (index == previous.lineIndex) return Future.value();
    final epoch = _begin(SessionPhase.switchingLine);
    return _serialize(() async {
      _check(epoch);
      try {
        await _im.disconnect();
        _check(epoch);
        await reconnected?.call(next, epoch);
        _check(epoch);
        await _im.connect(next, epoch);
        _check(epoch);
        await _store.write(next);
        _check(epoch);
        _emit(SessionSnapshot(SessionPhase.online, epoch, credentials: next));
      } catch (error) {
        if (!isCurrent(epoch)) {
          await _fail(epoch, error);
          return;
        }
        try {
          await _im.disconnect();
          _check(epoch);
          await reconnected?.call(previous, epoch);
          _check(epoch);
          await _im.connect(previous, epoch);
          _check(epoch);
          await _store.write(previous);
          _check(epoch);
          _emit(
            SessionSnapshot(
              SessionPhase.online,
              epoch,
              credentials: previous,
              failure: error is AppFailure
                  ? error
                  : const AppFailure(FailureKind.network),
            ),
          );
        } catch (rollbackError) {
          await _fail(epoch, rollbackError);
        }
        throw error is AppFailure
            ? error
            : const AppFailure(FailureKind.network);
      }
    });
  }

  Future<void> _fail(
    int epoch,
    Object error, {
    SessionCredentials? newlyIssued,
  }) async {
    // Authentication can succeed before native login or secure persistence
    // fails. Revoke exactly those issued credentials, including when a newer
    // epoch superseded this attempt; never kick the whole platform.
    final remote = () async {
      if (newlyIssued == null || _revoker == null) return null;
      try {
        return await _revoker
            .revoke(newlyIssued)
            .timeout(const Duration(seconds: 10));
      } catch (_) {
        return SessionRevocation.unconfirmed;
      }
    }();
    // This action owns the serialized native transition even if superseded.
    // Clean it before the next queued login can start.
    AppFailure? cleanupFailure;
    try {
      await _im.disconnect();
    } catch (_) {
      cleanupFailure = const AppFailure(FailureKind.unknown);
    }
    try {
      await _store.clear();
    } catch (_) {
      cleanupFailure = const AppFailure(FailureKind.storage);
    }
    var failure =
        cleanupFailure ??
        (error is AppFailure ? error : const AppFailure(FailureKind.unknown));
    final warning = switch (await remote) {
      SessionRevocation.pending => SessionCleanupWarning.pending,
      SessionRevocation.unconfirmed => SessionCleanupWarning.unconfirmed,
      _ => null,
    };
    if (warning != null) failure = failure.withCleanup(warning);
    if (isCurrent(epoch)) {
      _emit(SessionSnapshot(SessionPhase.failed, epoch, failure: failure));
    }
    throw isCurrent(epoch) ? failure : const AppFailure(FailureKind.cancelled);
  }

  Future<void> dispose() async {
    if (_disposed) return;
    ++_epoch;
    _disposed = true;
    await _tail;
    await _im.disconnect();
    await _changes.close();
    await _invalidations.close();
    await _accessChanges.close();
  }
}
