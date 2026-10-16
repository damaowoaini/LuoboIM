import 'package:flutter/foundation.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/im/open_im_gateway.dart';
import '../domain/chat_batch.dart';
import '../domain/chat_message.dart';
import 'chat_controller.dart';

/// ID-only state is reconciled with the live timeline and shared privacy facts.
/// Clearing, revoking, locking or changing owner permanently invalidates a
/// previously opened confirmation; a later login/unlock cannot renew it.
final class ChatBatchSelection extends ChangeNotifier {
  ChatBatchSelection(this.chat) {
    chat.addListener(_synchronize);
  }
  final ChatController chat;
  final Set<String> _ids = {};
  MessageHistoryAccess? _lease;
  int _serial = 0;
  bool _disposed = false, active = false, busy = false;
  int get count => _ids.length;
  Set<String> get selectedIds => Set.unmodifiable(_ids);
  bool selected(String id) => _ids.contains(id);
  ChatMessage? _item(String id) =>
      chat.messages.where((m) => m.id == id).firstOrNull;
  bool eligible(ChatMessage item) =>
      chat.usable &&
      chat.repository.canView(item) &&
      chat.repository.canRetain(item) &&
      !const {
        ChatMessageKind.system,
        ChatMessageKind.typing,
        ChatMessageKind.custom,
        ChatMessageKind.unsupported,
      }.contains(item.kind);
  bool get canMerge =>
      _ids.isNotEmpty &&
      _ids.every((id) {
        final item = _item(id);
        return item != null && eligible(item) && item.canForward;
      });
  void _checkLease() {
    if (_disposed || !chat.usable || _lease == null) {
      throw const AppFailure(FailureKind.cancelled);
    }
    _lease!.check();
  }

  bool begin(ChatMessage item) {
    if (busy || !eligible(item)) return false;
    _lease = chat.repository.gateway.historyAccess(
      chat.repository.generation,
      conversationId: chat.repository.conversationId,
    );
    ++_serial;
    _ids.clear();
    _ids.add(item.id);
    active = true;
    notifyListeners();
    return true;
  }

  bool toggle(ChatMessage item) {
    if (!active || busy || !eligible(item)) return false;
    _checkLease();
    if (!_ids.contains(item.id) && _ids.length >= maximumBatchMessages) {
      return false;
    }
    ++_serial;
    if (!_ids.remove(item.id)) _ids.add(item.id);
    notifyListeners();
    return true;
  }

  void cancel() {
    ++_serial;
    _ids.clear();
    active = false;
    _lease = null;
    if (!_disposed) notifyListeners();
  }

  void _synchronize() {
    if (_disposed || !active) return;
    try {
      _checkLease();
    } on AppFailure {
      cancel();
      return;
    }
    final removed = _ids.where((id) {
      final item = _item(id);
      return item == null || !eligible(item);
    }).toList();
    if (removed.isEmpty) return;
    ++_serial;
    _ids.removeAll(removed);
    notifyListeners();
  }

  ChatBatchIntent capture() {
    _synchronize();
    _checkLease();
    final serial = _serial;
    final ids = _ids.toList()
      ..sort((a, b) {
        final time = _item(a)!.sentAt.compareTo(_item(b)!.sentAt);
        return time == 0 ? a.compareTo(b) : time;
      });
    return ChatBatchIntent(
      conversationId: chat.repository.conversationId,
      generation: chat.repository.generation,
      messageIds: ids,
      check: () {
        _checkLease();
        if (serial != _serial) throw const AppFailure(FailureKind.dataChanged);
      },
    );
  }

  Future<BatchDeleteResult> deleteConfirmed(ChatBatchIntent intent) async {
    if (busy) throw const AppFailure(FailureKind.cancelled);
    intent.check();
    busy = true;
    notifyListeners();
    final acknowledged = <String>[];
    String? unconfirmed;
    var next = 0, stopped = false, acceptingCallbacks = true;
    try {
      for (; next < intent.messageIds.length; next++) {
        final id = intent.messageIds[next];
        var dispatched = false, ack = false;
        try {
          intent.check();
          final item = _item(id);
          if (item == null || !eligible(item)) {
            throw const AppFailure(FailureKind.dataChanged);
          }
          await chat.repository.deleteSelected(
            id,
            checkSource: intent.check,
            onDispatched: () {
              if (acceptingCallbacks) dispatched = true;
            },
            onAcknowledged: () {
              if (!acceptingCallbacks) return;
              ack = true;
              acknowledged.add(id);
              _ids.remove(id);
            },
          );
          intent.check();
          chat.acceptBatchDeletion([id]);
        } catch (_) {
          stopped = true;
          if (dispatched && !ack) {
            unconfirmed = id;
            next++;
          } else if (ack) {
            next++;
          }
          break;
        }
      }
    } finally {
      acceptingCallbacks = false;
      busy = false;
      if (!stopped) cancel();
      if (!_disposed) notifyListeners();
    }
    return BatchDeleteResult(
      acknowledged: acknowledged,
      unconfirmedId: unconfirmed,
      notAttempted: intent.messageIds.skip(next),
      stopped: stopped,
    );
  }

  bool beginForward(ChatBatchIntent intent) {
    if (busy || !canMerge) return false;
    intent.check();
    busy = true;
    notifyListeners();
    return true;
  }

  void endForward({required bool sent}) {
    busy = false;
    if (sent) {
      cancel();
    } else if (!_disposed) {
      notifyListeners();
    }
  }

  @override
  void dispose() {
    _disposed = true;
    ++_serial;
    _ids.clear();
    _lease = null;
    chat.removeListener(_synchronize);
    super.dispose();
  }
}
