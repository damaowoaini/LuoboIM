import 'dart:async';

import 'package:flutter/foundation.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/tenant/session_coordinator.dart';
import 'rtc_call.dart';

/// One call per signed-in account. Every asynchronous continuation is fenced
/// by both the session generation and this call's monotonically increasing id.
final class RtcCoordinator extends ChangeNotifier {
  RtcCoordinator({
    required this.session,
    required this.permissions,
    required RtcCallTransport Function() transportFactory,
  }) : _factory = transportFactory {
    _states = session.changes.listen((_) => _bind());
    _accessStates = session.accessChanges.listen((_) {
      if (!session.interactiveAllowed &&
          busy &&
          _connectedAt == null &&
          _phase != RtcPhase.incoming) {
        unawaited(hangUp(reason: RtcEndReason.cancelled));
      }
    });
    _invalidations = session.invalidations.listen((_) {
      _finish(RtcEndReason.sessionChanged);
      _transport = null;
      unawaited(_signals?.cancel());
      _signals = null;
    });
    _bind();
  }

  final SessionCoordinator session;
  final RtcPermissionGate permissions;
  final RtcCallTransport Function() _factory;
  late final StreamSubscription<SessionSnapshot> _states;
  late final StreamSubscription<int> _accessStates;
  late final StreamSubscription<int> _invalidations;
  StreamSubscription<RtcSignal>? _signals;
  StreamSubscription<void>? _mediaChanges;
  StreamSubscription<RtcMediaConnection>? _connection;
  RtcCallTransport? _transport;
  RtcMediaSession? _media;
  ImCallInvitation? _call;
  RtcPhase _phase = RtcPhase.idle;
  RtcEndReason? _reason;
  AppFailure? _failure;
  Timer? _timeout;
  int _request = 0;
  bool _disposed = false;
  bool _accepted = false;
  bool _publishing = false;
  bool _background = false;
  bool _sawRemotePeer = false;
  DateTime? _connectedAt;
  String _title = '';
  final Set<String> _recent = {};

  RtcPhase get phase => _phase;
  RtcEndReason? get reason => _reason;
  AppFailure? get failure => _failure;
  ImCallInvitation? get call => _call;
  RtcMediaSession? get media => _media;
  String get title => _title;
  DateTime? get connectedAt => _connectedAt;
  bool get busy => _phase != RtcPhase.idle && _phase != RtcPhase.ended;
  bool get incoming => _call?.inviterId != _transport?.selfId;
  bool get canDial => _transport?.current ?? false;
  RtcEndAction get endAction => incoming && !_accepted
      ? RtcEndAction.reject
      : !incoming && !_accepted
      ? RtcEndAction.cancel
      : RtcEndAction.hangUp;

  void _notify() {
    if (!_disposed) notifyListeners();
  }

  bool _active(int request) =>
      !_disposed &&
      request == _request &&
      busy &&
      (_transport?.current ?? false);

  void _bind() {
    if (_disposed ||
        session.state.phase != SessionPhase.online ||
        _transport?.generation == session.state.generation) {
      return;
    }
    final transport = _factory();
    _transport = transport;
    _recent.clear();
    unawaited(_signals?.cancel());
    _signals = transport.signals.listen(_signal, onError: (Object _) {});
    unawaited(
      transport
          .restoreIncoming()
          .then((call) {
            if (_transport == transport && transport.current && call != null) {
              _signal(RtcSignal(ImSignalAction.invited, call));
            }
          })
          .catchError((Object _) {}),
    );
    _notify();
  }

  Future<void> dial({
    required List<String> inviteeIds,
    required bool video,
    String groupId = '',
    String title = '',
  }) async {
    if (busy || !canDial || !session.interactiveAllowed) return;
    final transport = _transport!;
    try {
      _call = transport.invitation(
        inviteeIds: inviteeIds,
        video: video,
        groupId: groupId,
      );
    } catch (_) {
      _failure = const AppFailure(FailureKind.invalidInput);
      _notify();
      return;
    }
    _title = title.isEmpty ? inviteeIds.join('、') : title;
    _reason = null;
    _failure = null;
    _accepted = false;
    _sawRemotePeer = false;
    _phase = RtcPhase.requestingPermission;
    final request = ++_request;
    _notify();
    try {
      final granted = await permissions.request(video: video);
      if (!_active(request)) return;
      if (!granted) {
        _finish(RtcEndReason.permissionDenied);
        return;
      }
      _phase = RtcPhase.outgoing;
      _armTimeout();
      _notify();
      await _join(request, incoming: false);
    } catch (error) {
      if (_active(request)) _failed(error);
    }
  }

  Future<void> accept() async {
    if (_phase != RtcPhase.incoming || !session.interactiveAllowed) return;
    final request = _request;
    _phase = RtcPhase.requestingPermission;
    _notify();
    try {
      final granted = await permissions.request(video: _call!.video);
      if (!_active(request)) return;
      if (!granted) {
        unawaited(_endSignal(_call!, RtcEndAction.reject));
        _finish(RtcEndReason.permissionDenied);
        return;
      }
      _accepted = true;
      _phase = RtcPhase.connecting;
      _notify();
      await _join(request, incoming: true);
    } catch (error) {
      if (_active(request)) _failed(error);
    }
  }

  Future<void> _join(int request, {required bool incoming}) async {
    final media = await _transport!.join(
      _call!,
      incoming: incoming,
      stillActive: () => _active(request),
    );
    if (!_active(request)) {
      await media.close();
      return;
    }
    _media = media;
    _mediaChanges = media.changes.listen((_) {
      if (_active(request)) {
        final hasRemote = media.peers.any((peer) => !peer.local);
        if (hasRemote) _sawRemotePeer = true;
        if (_sawRemotePeer && !hasRemote && _call!.groupId.isEmpty) {
          unawaited(hangUp(reason: RtcEndReason.disconnected));
          return;
        }
        _tryPublish(request);
        _notify();
      }
    });
    _connection = media.connection.listen((connection) {
      if (!_active(request)) return;
      switch (connection) {
        case RtcMediaConnection.disconnected:
          unawaited(hangUp(reason: RtcEndReason.disconnected));
        case RtcMediaConnection.reconnecting:
          _phase = RtcPhase.reconnecting;
          _notify();
        case RtcMediaConnection.connected:
          if (_connectedAt != null) {
            _phase = RtcPhase.active;
            _notify();
          }
      }
    });
    _tryPublish(request);
    _notify();
  }

  void _tryPublish(int request) {
    final media = _media;
    if (media == null ||
        !_accepted ||
        _publishing ||
        _connectedAt != null ||
        !_active(request)) {
      return;
    }
    _publishing = true;
    unawaited(() async {
      try {
        if (!_active(request)) return;
        await media.setMicrophone(true);
        if (!_active(request)) {
          await media.close();
          return;
        }
        if (_call!.video && !_background) await media.setCamera(true);
        if (!_active(request)) {
          await media.close();
          return;
        }
        await media.setSpeaker(_call!.video);
        if (!_active(request)) {
          await media.close();
          return;
        }
        _connectedAt = DateTime.now();
        _timeout?.cancel();
        _phase = RtcPhase.active;
        _notify();
      } catch (error) {
        if (_active(request)) _failed(error);
      } finally {
        if (request == _request) _publishing = false;
      }
    }());
  }

  void _signal(RtcSignal signal) {
    final call = signal.invitation;
    if (!(_transport?.current ?? false)) return;
    if (signal.action == ImSignalAction.invited) {
      if (call.inviterId == _transport!.selfId ||
          !call.inviteeIds.contains(_transport!.selfId) ||
          _recent.contains(call.roomId) ||
          call.roomId == _call?.roomId) {
        return;
      }
      final expires = call.initiatedAt.add(
        Duration(seconds: call.timeoutSeconds),
      );
      if (DateTime.now().isAfter(expires) ||
          call.initiatedAt.isAfter(
            DateTime.now().add(const Duration(minutes: 1)),
          )) {
        return;
      }
      if (busy) {
        _recent.add(call.roomId);
        if (_recent.length > 64) _recent.remove(_recent.first);
        unawaited(_endSignal(call, RtcEndAction.reject));
        return;
      }
      _call = call;
      _title = call.inviterId;
      _reason = null;
      _failure = null;
      _accepted = false;
      _sawRemotePeer = false;
      _phase = RtcPhase.incoming;
      ++_request;
      _armTimeout();
      _notify();
      return;
    }
    if (!busy || call.roomId != _call?.roomId) return;
    switch (signal.action) {
      case ImSignalAction.accepted:
        _accepted = true;
        _tryPublish(_request);
      case ImSignalAction.acceptedOnOtherDevice:
      case ImSignalAction.rejectedOnOtherDevice:
        _finish(RtcEndReason.otherDevice);
      case ImSignalAction.cancelled:
        _finish(RtcEndReason.cancelled);
      case ImSignalAction.timedOut:
        _finish(RtcEndReason.timedOut);
      case ImSignalAction.rejected:
        if (_call!.groupId.isEmpty) _finish(RtcEndReason.declined);
      case ImSignalAction.hungUp:
        if (_call!.groupId.isEmpty) _finish(RtcEndReason.hungUp);
      default:
        break;
    }
    _notify();
  }

  void _armTimeout() {
    _timeout?.cancel();
    final call = _call!;
    final remaining = call.initiatedAt
        .add(Duration(seconds: call.timeoutSeconds))
        .difference(DateTime.now());
    _timeout = Timer(remaining.isNegative ? Duration.zero : remaining, () {
      if (busy && _connectedAt == null) {
        unawaited(hangUp(reason: RtcEndReason.timedOut));
      }
    });
  }

  Future<void> _endSignal(ImCallInvitation call, RtcEndAction action) async {
    final transport = _transport;
    if (transport == null || !transport.current) return;
    try {
      await transport.end(call, action);
    } catch (_) {
      /* Local capture is already closed; server expiry reconciles. */
    }
  }

  Future<void> hangUp({RtcEndReason? reason}) async {
    if (!busy || _call == null) return;
    final call = _call!;
    final beforeDispatch = _phase == RtcPhase.requestingPermission && !incoming;
    final action = endAction;
    final ending =
        reason ??
        (action == RtcEndAction.cancel
            ? RtcEndReason.cancelled
            : action == RtcEndAction.reject
            ? RtcEndReason.declined
            : RtcEndReason.hungUp);
    _finish(ending);
    if (!beforeDispatch) await _endSignal(call, action);
  }

  void _failed(Object error) {
    final call = _call;
    final beforeDispatch = _phase == RtcPhase.requestingPermission && !incoming;
    final action = endAction;
    _failure = error is AppFailure
        ? error
        : const AppFailure(FailureKind.network);
    _finish(RtcEndReason.failed);
    if (call != null && !beforeDispatch) unawaited(_endSignal(call, action));
  }

  void _finish(RtcEndReason reason) {
    ++_request;
    _timeout?.cancel();
    _timeout = null;
    if (_call != null) {
      _recent.add(_call!.roomId);
      if (_recent.length > 64) _recent.remove(_recent.first);
    }
    final media = _media;
    _media = null;
    unawaited(_mediaChanges?.cancel());
    _mediaChanges = null;
    unawaited(_connection?.cancel());
    _connection = null;
    if (media != null) unawaited(media.close());
    _publishing = false;
    _connectedAt = null;
    _phase = RtcPhase.ended;
    _reason = reason;
    if (reason == RtcEndReason.sessionChanged) {
      _call = null;
      _title = '';
      _failure = null;
    }
    _notify();
  }

  Future<void> toggleMicrophone() =>
      _control((media) => media.setMicrophone(!media.microphone));
  Future<void> toggleCamera() =>
      _control((media) => media.setCamera(!media.camera));
  Future<void> toggleSpeaker() =>
      _control((media) => media.setSpeaker(!media.speaker));
  Future<void> switchCamera() => _control((media) => media.switchCamera());
  Future<void> _control(
    Future<void> Function(RtcMediaSession) operation, {
    bool allowWhileLocked = false,
  }) async {
    final media = _media;
    final request = _request;
    final revision = session.interactiveRevision;
    if (media == null ||
        !_active(request) ||
        (!allowWhileLocked && !session.interactiveAllowed)) {
      return;
    }
    try {
      await operation(media);
      if (!allowWhileLocked &&
          revision != session.interactiveRevision &&
          media.camera) {
        await media.setCamera(false);
      }
      if (_active(request)) _notify();
    } catch (_) {
      if (_active(request)) {
        _failure = const AppFailure(FailureKind.permissionDenied);
        _notify();
      }
    }
  }

  void background(bool value) {
    _background = value;
    if (value && (_media?.camera ?? false)) {
      unawaited(
        _control((media) => media.setCamera(false), allowWhileLocked: true),
      );
    }
  }

  @override
  void dispose() {
    _disposed = true;
    _finish(RtcEndReason.sessionChanged);
    unawaited(_states.cancel());
    unawaited(_accessStates.cancel());
    unawaited(_invalidations.cancel());
    unawaited(_signals?.cancel());
    super.dispose();
  }
}
