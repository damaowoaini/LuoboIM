import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/scheduler.dart';

import '../../../core/im/im_event.dart';
import '../../../core/im/open_im_gateway.dart';
import '../data/chat_repository.dart';

/// Per-view ephemeral state. There is no message, durable draft, or last-known
/// typing cache. Queued writes keep this view's original history/owner lease.
final class ChatTypingController extends ChangeNotifier {
  ChatTypingController(this.repository, {required this.isCurrent}) {
    _events = repository.gateway.events.listen((event) {
      if (event.generation != repository.generation) return;
      if (event.kind == ImEventKind.historyInvalidated ||
          event.kind == ImEventKind.conversationHistoryInvalidated &&
              event.resourceIds.contains(repository.conversationId)) {
        setVisible(false);
        return;
      }
      final status = event.typing;
      if (event.kind != ImEventKind.typingChanged ||
          status == null ||
          !_current ||
          !repository.acceptsTyping(status)) {
        return;
      }
      _incoming?.cancel();
      _peerTyping = status.active;
      if (status.active) {
        final epoch = _epoch;
        _incoming = Timer(const Duration(seconds: 3), () {
          if (!_disposed && epoch == _epoch) {
            _peerTyping = false;
            _notify();
          }
        });
      }
      _notify();
    });
    _invalidations = repository.session.invalidations.listen(
      (_) => setVisible(false),
    );
    _access = repository.session.accessChanges.listen((_) {
      if (!repository.current) setVisible(false);
    });
  }
  final ChatRepository repository;
  final bool Function() isCurrent;
  late final StreamSubscription<ImEvent> _events;
  late final StreamSubscription<int> _invalidations, _access;
  MessageHistoryAccess? _lease;
  Timer? _incoming, _throttle, _idle;
  bool _visible = false, _peerTyping = false, _sent = false;
  bool _disposed = false, _noticeQueued = false;
  int _epoch = 0, _outbound = 0;
  Future<void> _tail = Future.value();

  bool get _current {
    if (_disposed || !_visible || !repository.current || !isCurrent()) {
      return false;
    }
    try {
      _lease?.check();
      return _lease != null;
    } catch (_) {
      return false;
    }
  }

  bool get peerTyping => _current && _peerTyping;

  void _notify() {
    if (_disposed) return;
    if (SchedulerBinding.instance.schedulerPhase ==
        SchedulerPhase.persistentCallbacks) {
      if (_noticeQueued) return;
      _noticeQueued = true;
      SchedulerBinding.instance.addPostFrameCallback((_) {
        _noticeQueued = false;
        if (!_disposed) notifyListeners();
      });
    } else {
      notifyListeners();
    }
  }

  void setVisible(bool value) {
    if (_disposed || value == _visible) return;
    if (value) {
      try {
        _lease = repository.gateway.historyAccess(
          repository.generation,
          conversationId: repository.conversationId,
        );
      } catch (_) {
        return;
      }
      _visible = true;
      return;
    }
    // A covered/background/old owner cannot dispatch even a delayed "no".
    ++_epoch;
    ++_outbound;
    _visible = false;
    _lease = null;
    _sent = false;
    _incoming?.cancel();
    _throttle?.cancel();
    _idle?.cancel();
    final changed = _peerTyping;
    _peerTyping = false;
    if (changed) _notify();
  }

  void edited(String text, {required bool focused}) {
    if (!_current) return;
    if (!focused || text.trim().isEmpty) {
      stopInput();
      return;
    }
    _idle?.cancel();
    _idle = Timer(const Duration(seconds: 3), stopInput);
    if (_throttle?.isActive == true) return;
    _sent = true;
    _dispatch(true);
    _throttle = Timer(const Duration(seconds: 2), () {});
  }

  void stopInput() {
    _idle?.cancel();
    _throttle?.cancel();
    // Supersede a queued yes without replaying an already dispatched request.
    ++_outbound;
    if (_sent && _current) _dispatch(false);
    _sent = false;
  }

  void _dispatch(bool active) {
    final epoch = _epoch, outbound = ++_outbound, lease = _lease;
    _tail = _tail.then((_) async {
      bool current() => _current && epoch == _epoch && outbound == _outbound;
      if (!current() || lease == null) return;
      try {
        lease.check();
        await repository.sendTyping(active, access: lease, isCurrent: current);
      } catch (_) {
        // Best effort and no automatic retry; it must not affect draft/send UI.
      }
    });
  }

  @override
  void dispose() {
    setVisible(false);
    _disposed = true;
    unawaited(_events.cancel());
    unawaited(_invalidations.cancel());
    unawaited(_access.cancel());
    super.dispose();
  }
}
