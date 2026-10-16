import 'dart:async';

import 'package:flutter/foundation.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/tenant/session_coordinator.dart';
import '../../../core/tenant/session_credentials.dart';
import '../data/call_records_repository.dart';
import 'call_record.dart';
import 'rtc_call.dart';

typedef CallRecordsRepositoryFactory = CallRecordsRepository Function(String accountKey);

final class CallHistoryAccess {
  CallHistoryAccess._(this.repository, this._current);
  final CallRecordsRepository repository;
  final bool Function() _current;
  bool get current => _current();
}

/// This ticket never switches repositories/owners when an async call returns.
final class CallHistoryTicket {
  CallHistoryTicket._(this._binding, this._record);
  final _CallHistoryBinding _binding;
  CallRecord _record;
  int? _connectedTick;
  String get id => _record.id;
}

/// Bounded, non-sensitive lifecycle diagnostics; no identities or credentials.
final class CallHistoryLifecycle {
  const CallHistoryLifecycle(this.activeOwners, this.drainingOwners,
    this.retainedCredentialOwners, this.retirementFailed);
  final int activeOwners, drainingOwners, retainedCredentialOwners;
  final bool retirementFailed;
}

/// Non-autoDispose and tied to the stable SessionCoordinator, but *not* to one
/// login. Each exact new credential object/generation gets a new sealed owner
/// binding; only immutable events accepted before sealing may drain afterward.
final class CallHistoryRecorder extends ChangeNotifier {
  CallHistoryRecorder({required this.session, CallRecordsRepositoryFactory? repositories,
      DateTime Function()? now, int Function()? monotonicMicros})
      : _repositories = repositories ?? ((key) => CallRecordsRepository(accountKey: key)),
        _now = now ?? DateTime.now {
    _clock.start();
    _ticks = monotonicMicros ?? (() => _clock.elapsedMicroseconds);
    _changes = session.changes.listen((_) => _bind());
    _invalidations = session.invalidations.listen((_) => seal());
    _bind();
  }

  final SessionCoordinator session;
  final CallRecordsRepositoryFactory _repositories;
  final DateTime Function() _now;
  final _clock = Stopwatch();
  late final int Function() _ticks;
  late final StreamSubscription<SessionSnapshot> _changes;
  late final StreamSubscription<int> _invalidations;
  final Map<_CallHistoryBinding, Future<void>> _retiring = {};
  _CallHistoryBinding? _owner;
  bool _disposed = false, _closed = false;
  Future<void>? _closing;
  Future<void> _writeTail = Future<void>.value();
  AppFailure? _admissionFailure;
  bool _retirementFailed = false;

  CallHistoryLifecycle get lifecycle => CallHistoryLifecycle(
    _owner == null ? 0 : 1, _retiring.length,
    (_owner?.retainsCredentials == true ? 1 : 0) +
      _retiring.keys.where((binding) => binding.retainsCredentials).length,
    _retirementFailed);

  void _notify() { if (!_disposed) notifyListeners(); }

  void _bind() {
    if (_closed) return;
    final snapshot = session.state;
    final credentials = snapshot.credentials;
    if (snapshot.phase != SessionPhase.online || credentials == null ||
        !session.acceptsEvent(snapshot.generation)) return;
    if (_owner?.matches(credentials, snapshot.generation) == true &&
        _owner!.current) return;
    seal();
    final binding = _CallHistoryBinding(credentials, snapshot.generation, session,
      _repositories(credentials.accountKey), (binding) {
        // An old queue is permitted to finish in its original DB, never to
        // publish its completion into a replacement account's current UI.
        if (identical(_owner, binding) && binding.current) _notify();
      }, (operation) => _writeTail = _writeTail.then((_) => operation()));
    _owner = binding;
    _admissionFailure = null;
    _notify();
  }

  CallHistoryAccess capture() {
    final binding = _owner;
    final accessRevision = session.interactiveRevision;
    if (binding == null || !binding.current || !session.interactiveAllowed) {
      throw const AppFailure(FailureKind.sessionExpired);
    }
    return CallHistoryAccess._(binding.repository, () => !_closed &&
      identical(_owner, binding) && binding.current &&
      session.isInteractiveCurrent(binding.generation, accessRevision));
  }

  AppFailure? get failure => _admissionFailure ?? _owner?.failure;
  bool live(String id) => _owner?.live.contains(id) == true && _owner!.current;

  void abandon(CallHistoryTicket? ticket) {
    if (ticket == null) return;
    ticket._binding.live.remove(ticket.id);
    if (ticket._binding.current) _notify();
  }

  CallHistoryTicket? attempt(ImCallInvitation invitation, String selfId, String title) {
    final binding = _owner;
    if (_closed || binding == null || !binding.current ||
        invitation.groupId.isNotEmpty) return null;
    try {
      if (selfId != binding.userId) {
        throw const AppFailure(FailureKind.cancelled);
      }
      final record = CallRecord.admit(invitation, selfId: selfId, title: title, now: _now());
      record.validate();
      final ticket = CallHistoryTicket._(binding, record);
      binding.live.add(record.id);
      binding.enqueue(record);
      return ticket;
    } catch (_) {
      _admissionFailure = const AppFailure(FailureKind.localStorage);
      _notify();
      return null; // History failure does not cancel a legitimate live call.
    }
  }

  bool _accepts(CallHistoryTicket? ticket) => ticket != null && !_closed &&
      identical(_owner, ticket._binding) && ticket._binding.current &&
      !ticket._record.terminal;

  void accepted(CallHistoryTicket? ticket) {
    if (ticket == null || !_accepts(ticket) || ticket._record.accepted) return;
    ticket._record = ticket._record.event(accepted: true);
    ticket._binding.enqueue(ticket._record);
  }

  void joining(CallHistoryTicket? ticket) {
    if (ticket == null || !_accepts(ticket) || ticket._record.signalingStarted) return;
    ticket._record = ticket._record.event(signalingStarted: true);
    ticket._binding.enqueue(ticket._record);
  }

  void connected(CallHistoryTicket? ticket) {
    if (ticket == null || !_accepts(ticket) || ticket._record.connected) return;
    ticket._connectedTick = _ticks();
    ticket._record = ticket._record.event(accepted: true, connected: true);
    ticket._binding.enqueue(ticket._record);
  }

  void finish(CallHistoryTicket? ticket, RtcEndReason reason, CallEndCause cause) {
    if (ticket == null || !_accepts(ticket)) return;
    final original = ticket._record;
    final started = ticket._connectedTick;
    final duration = started == null ? 0 : ((_ticks() - started).clamp(0, 31536000000000) ~/ 1000);
    ticket._record = original.event(terminal: true, reason: reason, cause: cause,
      durationMs: duration, endedAt: _now().millisecondsSinceEpoch.clamp(original.admittedAt, 4102444800000).toInt());
    ticket._binding.live.remove(original.id);
    ticket._binding.enqueue(ticket._record);
  }

  /// Immediate admission barrier, before coordinator _finish(sessionChanged).
  /// The finite queue already frozen for this owner may still drain.
  void seal() {
    final binding = _owner;
    _owner = null;
    if (binding == null) return;
    binding.seal();
    // Only in-progress retirements retain a binding. Both success and failure
    // release it at completion; failure remains a bounded generic latch. Its
    // account's credential object was already released before any await.
    final task = () async {
      try { await binding.close(); } catch (_) { _retirementFailed = true; }
      finally { _retiring.remove(binding); }
    }();
    _retiring[binding] = task;
    _notify();
  }

  /// Awaits the finite retirement set captured at entry. It never drains a new
  /// owner's future events or turns an old failure into that owner's UI error.
  Future<void> settleRetired() async {
    await Future.wait(List<Future<void>>.of(_retiring.values));
    if (_retirementFailed) throw const AppFailure(FailureKind.localStorage);
  }

  Future<void> flush() async {
    final binding = _owner;
    if (binding == null || !binding.current) {
      throw const AppFailure(FailureKind.cancelled);
    }
    await binding.tail;
    if (binding.failure != null) throw binding.failure!;
  }

  Future<void> retryPersistence() async {
    final binding = _owner;
    if (binding == null || !binding.current) {
      throw const AppFailure(FailureKind.cancelled);
    }
    final pending = binding.pending.values.toList(growable: false);
    for (final record in pending) {
      if (!binding.current) throw const AppFailure(FailureKind.cancelled);
      binding.enqueue(record);
    }
    await flush();
  }

  Future<void> close() {
    if (_closing != null) return _closing!;
    _closed = true;
    seal();
    return _closing = () async {
      await _changes.cancel();
      await _invalidations.cancel();
      try { await settleRetired(); } finally { _clock.stop(); }
    }();
  }

  @override
  void dispose() {
    _disposed = true;
    unawaited(close().catchError((Object _) {}));
    super.dispose();
  }
}

final class _CallHistoryBinding {
  _CallHistoryBinding(SessionCredentials credentials, this.generation, this.session,
    this.repository, this.changed, this.serialize)
    : _credentials = credentials, accountKey = credentials.accountKey,
      userId = credentials.userId;
  SessionCredentials? _credentials;
  final String accountKey, userId;
  final int generation;
  final SessionCoordinator session;
  final CallRecordsRepository repository;
  final void Function(_CallHistoryBinding) changed;
  final Future<void> Function(Future<void> Function()) serialize;
  final Set<String> live = {};
  final Map<(String, int), CallRecord> pending = {};
  Future<void> tail = Future<void>.value();
  Future<void>? _closing;
  bool sealed = false;
  AppFailure? failure;
  bool get retainsCredentials => _credentials != null;
  bool matches(SessionCredentials value, int gen) => identical(_credentials, value) && generation == gen;
  bool get current => !sealed && session.acceptsEvent(generation) &&
    identical(session.state.credentials, _credentials);

  void seal() {
    sealed = true;
    _credentials = null;
    live.clear();
  }

  void enqueue(CallRecord record) {
    // Called synchronously at event admission, before any await. Draining this
    // immutable value never checks/replaces the owner with a new session.
    pending[(record.id, record.revision)] = record;
    tail = serialize(() async {
      try {
        await repository.apply(accountKey, record);
        pending.removeWhere((key, _) => key.$1 == record.id && key.$2 <= record.revision);
        failure = pending.isEmpty ? null : failure;
      } catch (_) {
        failure = const AppFailure(FailureKind.localStorage);
      }
      if (current) changed(this);
    });
  }

  Future<void> close() {
    if (_closing != null) return _closing!;
    seal();
    return _closing = () async {
      await tail;
      await repository.close();
      if (pending.isNotEmpty) throw const AppFailure(FailureKind.localStorage);
    }();
  }
}
