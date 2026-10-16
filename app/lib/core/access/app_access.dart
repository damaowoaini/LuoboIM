import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:flutter/foundation.dart';

import '../error/app_failure.dart';
import '../tenant/session_coordinator.dart';
import '../tenant/session_credentials.dart';
import 'access_store.dart';
import 'native_access.dart';

enum AppAccessPhase { inactive, checking, locked, verifying, unlocked, failed }

/// Application visibility is independent from the server/SDK session. Locking
/// invalidates interactive leases without disconnecting an established call.
final class AppAccessCoordinator extends ChangeNotifier {
  AppAccessCoordinator({
    required this.store,
    required this.native,
    DateTime Function()? now,
  }) : _now = now ?? DateTime.now;
  final AccessStore store;
  final NativeAccess native;
  final DateTime Function() _now;
  SessionCoordinator? _session;
  StreamSubscription<int>? _invalidations;
  StreamSubscription<SessionSnapshot>? _sessions;
  Completer<void>? _restore;
  AccessPolicy? _policy;
  final Map<String, Future<void>> _policyTails = {};
  String? _owner;
  int _generation = -1;
  int _revision = 0;
  int _request = 0;
  bool _disposed = false;
  bool _platformActivated = false;
  bool _busy = false;
  bool _background = false;
  int _backgroundEpoch = 0;
  bool _freshLogin = false;
  AppAccessPhase _phase = AppAccessPhase.inactive;
  AccessFailure? failure;
  int get revision => _revision;
  String? get owner => _owner;
  AppAccessPhase get phase => _phase;
  bool get enabled => _policy != null;
  bool get biometricEnabled => _policy?.biometric ?? false;
  bool get busy => _busy;
  bool get unlocked => _phase == AppAccessPhase.unlocked;
  bool get canResetAfterLogin => unlocked && _freshLogin;
  bool get blocksContent => _owner != null && !unlocked;

  void attach(SessionCoordinator session) {
    if (identical(_session, session)) return;
    _cancelRestore();
    unawaited(_invalidations?.cancel());
    unawaited(_sessions?.cancel());
    _session = session;
    _owner = null;
    _policy = null;
    _phase = AppAccessPhase.inactive;
    _busy = false;
    _invalidations = session.invalidations.listen((generation) {
      _cancelRestore();
      if (_platformActivated) unawaited(native.cancel());
      _generation = generation;
      _owner = null;
      _policy = null;
      _busy = false;
      _freshLogin = false;
      _phase = AppAccessPhase.inactive;
      _changed();
    });
    _sessions = session.changes.listen((state) {
      if (state.phase == SessionPhase.signedOut ||
          state.phase == SessionPhase.failed) {
        _cancelRestore();
        if (_platformActivated) unawaited(native.cancel());
        _owner = null;
        _policy = null;
        _busy = false;
        _freshLogin = false;
        _phase = AppAccessPhase.inactive;
        _changed();
        if (_platformActivated) {
          unawaited(
            native
                .privacy(enabled: false, covered: false)
                .catchError((Object _) {}),
          );
        }
      }
    });
  }

  void _changed() {
    ++_revision;
    ++_request;
    _session?.updateInteractiveAccess(unlocked, _revision);
    if (!_disposed) notifyListeners();
  }

  void _cancelRestore() {
    final pending = _restore;
    _restore = null;
    if (pending != null && !pending.isCompleted) {
      pending.completeError(const AppFailure(FailureKind.cancelled));
    }
  }

  void _check(int request) {
    if (_disposed ||
        request != _request ||
        !(_session?.isCurrent(_generation) ?? false) ||
        _owner == null) {
      throw const AccessFailure(AccessFailureKind.stale);
    }
  }

  /// Physical store mutations cannot be cancelled by a Dart generation check.
  /// Serialize each owner's complete read/modify/write transaction and make a
  /// new login read behind any old write. Crypto and user prompts stay outside
  /// this FIFO; unrelated owners and the SDK transition queue remain separate.
  Future<T> _policyTransaction<T>(String owner, Future<T> Function() action) {
    final result = Completer<T>();
    final previous = _policyTails[owner] ?? Future<void>.value();
    late final Future<void> task;
    task = previous.then((_) async {
      try {
        result.complete(await action());
      } catch (error, stack) {
        result.completeError(error, stack);
      }
    });
    _policyTails[owner] = task;
    unawaited(
      task.then((_) {
        if (identical(_policyTails[owner], task)) _policyTails.remove(owner);
      }),
    );
    // The caller is bounded, but the physical FIFO is retained until a timed
    // out write really settles; a timeout never permits a competing mutation.
    return result.future.timeout(
      const Duration(seconds: 15),
      onTimeout: () {
        throw const AccessFailure(AccessFailureKind.storage);
      },
    );
  }

  /// Called after consent and the encrypted credential read, before SDK
  /// connect. SessionCoordinator invokes this outside its native FIFO.
  Future<void> authorizeRestore(
    SessionCredentials credentials,
    int generation,
  ) async {
    _freshLogin = false;
    await _load(credentials, generation);
    if (!enabled) return;
    _phase = AppAccessPhase.locked;
    final waiting = _restore = Completer<void>();
    _changed();
    await waiting.future;
  }

  /// A new server-verified password/OTP login has already authenticated this
  /// owner. Its next background transition requires the configured local PIN.
  Future<void> authenticated(
    SessionCredentials credentials,
    int generation,
  ) async {
    await _load(credentials, generation);
    _freshLogin = true;
  }

  Future<void> reconnected(
    SessionCredentials credentials,
    int generation,
  ) async {
    await _load(credentials, generation);
  }

  Future<void> _load(SessionCredentials credentials, int generation) async {
    _owner = credentials.accountKey;
    _generation = generation;
    _phase = AppAccessPhase.checking;
    _freshLogin = false;
    _busy = false;
    failure = null;
    _changed();
    final request = _request;
    try {
      final policy = await _policyTransaction(credentials.accountKey, () async {
        _check(request);
        return store.read(credentials.accountKey);
      });
      _check(request);
      _policy = policy;
      _platformActivated = true;
      if (policy != null) {
        await native.privacy(enabled: true, covered: _background);
        _check(request);
      } else {
        await native
            .privacy(enabled: false, covered: false)
            .catchError((Object _) {});
        _check(request);
      }
      _phase = _background && policy != null
          ? AppAccessPhase.locked
          : AppAccessPhase.unlocked;
      _changed();
    } catch (_) {
      _check(request);
      failure = const AccessFailure(AccessFailureKind.storage);
      _phase = AppAccessPhase.failed;
      _changed();
      throw failure!;
    }
  }

  void lock() {
    if (!enabled || _owner == null) return;
    _phase = AppAccessPhase.locked;
    failure = null;
    _busy = false;
    _freshLogin = false;
    _changed();
    if (_platformActivated) unawaited(native.cancel());
  }

  /// The native layer covers snapshots immediately on inactivity. A short
  /// system prompt does not invalidate its own auth; a real background does.
  void background(bool background) {
    if (background && !_background) ++_backgroundEpoch;
    _background = background;
    if (background) lock();
  }

  Future<void> revealNativeCover() async {
    if (enabled || blocksContent) {
      _platformActivated = true;
      await native.privacy(enabled: true, covered: false);
    }
  }

  static void validatePin(String pin) {
    if (!RegExp(r'^[0-9]{6,12}$').hasMatch(pin)) {
      throw const AccessFailure(AccessFailureKind.incorrect);
    }
  }

  static bool _equal(String expected, String actual) {
    final a = base64Decode(expected), b = base64Decode(actual);
    if (a.length != 32 || b.length != 32) return false;
    var difference = 0;
    for (var i = 0; i < 32; i++) {
      difference |= a[i] ^ b[i];
    }
    return difference == 0;
  }

  Future<void> _verifyPin(String pin, int request) async {
    validatePin(pin);
    final owner = _owner!;
    final pending = await _policyTransaction(owner, () async {
      _check(request);
      final policy = await store.read(owner);
      _check(request);
      if (policy == null) throw const AccessFailure(AccessFailureKind.storage);
      final now = max(
        _now().millisecondsSinceEpoch,
        policy.lastAttemptMilliseconds,
      );
      if (now < policy.retryAfterMilliseconds || policy.failures >= 1000000) {
        throw const AccessFailure(AccessFailureKind.cooldown);
      }
      final attempts = policy.failures + 1;
      final seconds = attempts < 5
          ? 0
          : min(900, 30 * (1 << min(5, (attempts - 5) ~/ 5)));
      final reserved = policy.copy(
        failures: attempts,
        attemptedAt: now,
        retryAfter: now + seconds * 1000,
      );
      await store.write(owner, reserved);
      return reserved;
    });
    _check(request);
    _policy = pending;
    final derived = await native.derivePin(
      pin,
      pending.salt,
      pending.iterations,
    );
    _check(request);
    if (!_equal(pending.verifier, derived)) {
      throw const AccessFailure(AccessFailureKind.incorrect);
    }
    final success = await _policyTransaction(owner, () async {
      _check(request);
      final current = await store.read(owner);
      _check(request);
      if (current == null ||
          current.salt != pending.salt ||
          current.verifier != pending.verifier ||
          current.iterations != pending.iterations) {
        throw const AccessFailure(AccessFailureKind.stale);
      }
      final reset = current.copy(failures: 0, retryAfter: 0);
      await store.write(owner, reset);
      return reset;
    });
    _check(request);
    _policy = success;
  }

  Future<int> _perform(Future<void> Function(int request) operation) async {
    if (_busy) throw const AccessFailure(AccessFailureKind.cancelled);
    _busy = true;
    failure = null;
    if (!_disposed) notifyListeners();
    final request = _request;
    try {
      _check(request);
      await operation(request);
      _check(request);
      return request;
    } catch (error) {
      if (request == _request && !_disposed) {
        failure = error is AccessFailure
            ? error
            : const AccessFailure(AccessFailureKind.storage);
      }
      rethrow;
    } finally {
      if (request == _request) {
        _busy = false;
        if (!_disposed) notifyListeners();
      }
    }
  }

  Future<void> unlockPin(String pin) async {
    final request = await _perform((request) async {
      await _verifyPin(pin, request);
    });
    _check(request);
    if (_background) return;
    _unlock();
  }

  Future<void> unlockBiometric() async {
    if (!biometricEnabled) {
      throw const AccessFailure(AccessFailureKind.unavailable);
    }
    final request = await _perform((request) async {
      await native.biometric(_owner!, request);
    });
    _check(request);
    if (_background) return;
    _unlock();
  }

  void _unlock() {
    _phase = AppAccessPhase.unlocked;
    _busy = false;
    _changed();
    final waiting = _restore;
    _restore = null;
    if (waiting != null && !waiting.isCompleted) waiting.complete();
  }

  Future<void> setPin(
    String pin, {
    String? currentPin,
    bool resetAfterLogin = false,
  }) async {
    if (!unlocked) throw const AccessFailure(AccessFailureKind.stale);
    if (resetAfterLogin && !_freshLogin) {
      throw const AccessFailure(AccessFailureKind.stale);
    }
    validatePin(pin);
    final ownerAtStart = _owner, generationAtStart = _generation;
    final backgroundAtStart = _backgroundEpoch;
    try {
      await _perform((request) async {
        if (enabled && !resetAfterLogin) {
          await _verifyPin(currentPin ?? '', request);
        }
        final random = Random.secure();
        final salt = base64Encode(
          List.generate(32, (_) => random.nextInt(256)),
        );
        final verifier = await native.derivePin(pin, salt, 210000);
        _check(request);
        if (base64Decode(verifier).length != 32) {
          throw const AccessFailure(AccessFailureKind.storage);
        }
        final next = AccessPolicy(salt: salt, verifier: verifier);
        await native.privacy(
          enabled: true,
          covered: _background || backgroundAtStart != _backgroundEpoch,
        );
        _check(request);
        final owner = _owner!;
        await _policyTransaction(owner, () async {
          _check(request);
          await store.write(owner, next);
        });
        _check(request);
        _policy = next;
        // A previous biometric challenge cannot survive a PIN replacement.
        await native.removeBiometric(_owner!);
      });
    } catch (_) {
      if (_owner == ownerAtStart &&
          _generation == generationAtStart &&
          !enabled) {
        _phase = AppAccessPhase.failed;
        _changed();
      }
      rethrow;
    } finally {
      if (_owner == ownerAtStart &&
          _generation == generationAtStart &&
          enabled &&
          (_background || backgroundAtStart != _backgroundEpoch)) {
        lock();
        await native
            .privacy(enabled: true, covered: _background)
            .catchError((Object _) {});
      }
    }
    if (!_disposed) notifyListeners();
  }

  Future<void> disable(String pin) async {
    if (!unlocked || !enabled) {
      throw const AccessFailure(AccessFailureKind.stale);
    }
    await _perform((request) async {
      await _verifyPin(pin, request);
      await native.removeBiometric(_owner!);
      _check(request);
      final owner = _owner!;
      await _policyTransaction(owner, () async {
        _check(request);
        await store.remove(owner);
      });
      _check(request);
      _policy = null;
      await native.privacy(enabled: false, covered: false);
    });
    if (!_disposed) notifyListeners();
  }

  Future<void> setBiometric(bool value, String pin) async {
    if (!unlocked || !enabled) {
      throw const AccessFailure(AccessFailureKind.stale);
    }
    await _perform((request) async {
      await _verifyPin(pin, request);
      if (value) {
        if (!await native.biometricAvailable()) {
          throw const AccessFailure(AccessFailureKind.unavailable);
        }
        _check(request);
        await native.biometric(_owner!, request, enroll: true);
      } else {
        await native.removeBiometric(_owner!);
      }
      _check(request);
      final next = _policy!.copy(biometric: value);
      final owner = _owner!;
      await _policyTransaction(owner, () async {
        _check(request);
        await store.write(owner, next);
      });
      _check(request);
      _policy = next;
    });
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    if (_disposed) return;
    _disposed = true;
    _cancelRestore();
    if (_platformActivated) unawaited(native.cancel());
    unawaited(_invalidations?.cancel());
    unawaited(_sessions?.cancel());
    super.dispose();
  }
}
