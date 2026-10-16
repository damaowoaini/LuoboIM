import 'dart:async';

import 'package:flutter/foundation.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/im/im_event.dart';
import '../../../core/im/open_im_gateway.dart';
import '../../conversation/domain/conversation.dart';
import '../data/chat_repository.dart';
import '../domain/chat_message.dart';
import '../domain/chat_mention.dart';

final class ChatController extends ChangeNotifier {
  ChatController(this.repository) {
    _events = repository.gateway.events.listen((event) {
      if (event.generation != repository.generation || !repository.current) {
        return;
      }
      if (event.kind == ImEventKind.historyInvalidated ||
          event.kind == ImEventKind.conversationHistoryInvalidated &&
              event.resourceIds.contains(repository.conversationId)) {
        ++_timeline;
        _draftTimer?.cancel();
        _refreshTimer?.cancel();
        repository.resetHistory();
        conversation = conversation?.withoutMessagePreview();
        messages = const [];
        quote = null;
        sourceMessageId = null;
        progress.clear();
        _sending.clear();
        _lastReadId = null;
        loading = loadingOlder = preparing = false;
        hasMore = false;
        _historyNeedsReview = true;
        failure = event.failure;
        notifyListeners();
        return;
      }
      if (event.kind == ImEventKind.sendProgress &&
          event.resourceIds.isNotEmpty) {
        if (!messages.any((item) => item.id == event.resourceIds.first)) return;
        progress[event.resourceIds.first] = event.count ?? 0;
        notifyListeners();
        return;
      }
      if (!const {
        ImEventKind.messageReceived,
        ImEventKind.offlineMessageReceived,
        ImEventKind.messageDeleted,
        ImEventKind.messageRevoked,
        ImEventKind.readReceiptChanged,
        ImEventKind.messageExtensionsChanged,
        ImEventKind.syncFinished,
      }.contains(event.kind)) {
        return;
      }
      if (event.kind == ImEventKind.messageDeleted ||
          event.kind == ImEventKind.messageRevoked) {
        _forget(event.resourceIds);
      }
      _refreshTimer?.cancel();
      if (!_historyNeedsReview) {
        _refreshTimer = Timer(const Duration(milliseconds: 100), () {
          if (!_historyNeedsReview) unawaited(refresh());
        });
      }
    });
    _privacy = repository.gateway.privacyLedger.changes.listen((_) {
      if (usable) notifyListeners();
    });
    _invalidations = repository.session.invalidations.listen((_) {
      _valid = false;
      ++_timeline;
      messages = const [];
      conversation = null;
      _draftTimer?.cancel();
      _refreshTimer?.cancel();
      repository.clear();
      failure = const AppFailure(FailureKind.sessionExpired);
      notifyListeners();
    });
  }

  final ChatRepository repository;
  late final StreamSubscription<ImEvent> _events;
  late final StreamSubscription<void> _privacy;
  late final StreamSubscription<int> _invalidations;
  Timer? _draftTimer;
  Timer? _refreshTimer;
  Conversation? conversation;
  List<ChatMessage> messages = const [];
  AppFailure? failure;
  AppFailure? draftFailure;
  String draft = '';
  ChatMessage? quote;
  bool loading = false;
  bool loadingOlder = false;
  bool preparing = false;
  bool hasMore = true;
  String? sourceMessageId;
  bool _valid = true;
  bool _disposed = false;
  int _lastMinSeq = 0;
  int _timeline = 0;
  bool _historyNeedsReview = false;
  String? _lastReadId;
  final Map<String, int> progress = {};
  final Set<String> _sending = {};
  Future<void> _draftTail = Future.value();

  bool get usable => _valid && !_disposed && repository.current;

  void _forget(Iterable<String> ids) {
    final removed = ids.where((id) => id.isNotEmpty).toSet();
    if (removed.isEmpty) return;
    ++_timeline;
    loading = false;
    loadingOlder = false;
    _lastReadId = null;
    repository.forgetMessages(removed);
    if (removed.contains(quote?.id)) quote = null;
    if (removed.contains(sourceMessageId)) sourceMessageId = null;
    messages = List.unmodifiable(
      messages.where((item) => !removed.contains(item.id)),
    );
    if (!_disposed) notifyListeners();
  }

  Future<void> start() async {
    final timeline = ++_timeline;
    loading = true;
    failure = null;
    notifyListeners();
    try {
      final metadata = await repository.conversation();
      if (!usable || timeline != _timeline) return;
      final page = await repository.history();
      if (!usable || timeline != _timeline) return;
      conversation = metadata;
      draft = metadata.draft;
      _merge(page.messages);
      hasMore = !page.isEnd;
      _lastMinSeq = page.lastMinSeq;
      await _markRead();
    } catch (error) {
      if (usable && timeline == _timeline) failure = _failure(error);
    } finally {
      if (!_disposed && timeline == _timeline) {
        loading = false;
        notifyListeners();
      }
    }
  }

  AppFailure _failure(Object error) =>
      error is AppFailure ? error : const AppFailure(FailureKind.unknown);

  void _merge(Iterable<ChatMessage> values) {
    final merged = {for (final item in messages) item.id: item};
    for (final item in values) {
      if (!_sending.contains(item.id) && !repository.isRemoved(item.id)) {
        merged[item.id] = item;
      }
    }
    final sorted = merged.values.toList()
      ..sort((a, b) {
        final time = a.sentAt.compareTo(b.sentAt);
        return time != 0 ? time : a.id.compareTo(b.id);
      });
    messages = List.unmodifiable(sorted);
    if (quote != null && merged.containsKey(quote!.id)) {
      quote = merged[quote!.id];
    }
  }

  Future<void> refresh() async {
    if (sourceMessageId != null) return;
    if (!usable || loading || loadingOlder) return;
    _historyNeedsReview = false;
    final timeline = _timeline;
    try {
      final page = await repository.history();
      if (!usable || timeline != _timeline) return;
      _merge(page.messages);
      failure = null;
      await _markRead();
    } catch (error) {
      if (usable && timeline == _timeline) failure = _failure(error);
    }
    if (!_disposed && timeline == _timeline) notifyListeners();
  }

  Future<bool> viewSource(String id) async {
    if (!usable) return false;
    final timeline = ++_timeline;
    loadingOlder = false;
    loading = false;
    final source = await repository.findSource(id);
    if (source == null || !usable || timeline != _timeline) return false;
    final page = await repository.history(beforeId: id);
    if (!usable || timeline != _timeline) return false;
    messages = const [];
    _merge([...page.messages, source]);
    hasMore = !page.isEnd;
    _lastMinSeq = page.lastMinSeq;
    sourceMessageId = id;
    notifyListeners();
    return true;
  }

  Future<void> returnToLatest() async {
    sourceMessageId = null;
    messages = const [];
    _lastMinSeq = 0;
    await start();
  }

  Future<void> loadOlder() async {
    if (!usable || loading || loadingOlder || !hasMore || messages.isEmpty) {
      return;
    }
    final timeline = _timeline;
    loadingOlder = true;
    notifyListeners();
    try {
      final page = await repository.history(
        beforeId: messages.first.id,
        lastMinSeq: _lastMinSeq,
      );
      if (!usable || timeline != _timeline) return;
      _merge(page.messages);
      hasMore = !page.isEnd;
      _lastMinSeq = page.lastMinSeq;
    } catch (error) {
      if (usable && timeline == _timeline) failure = _failure(error);
    } finally {
      if (!_disposed && timeline == _timeline) {
        loadingOlder = false;
        notifyListeners();
      }
    }
  }

  Future<void> markVisibleRead(Iterable<String> ids) async {
    if (!usable) return;
    try {
      await repository.markVisibleRead(ids);
    } catch (error) {
      if (usable) {
        failure = _failure(error);
        notifyListeners();
      }
    }
  }

  Future<void> _markRead() async {
    final timeline = _timeline;
    final incoming = messages.where((item) => !item.mine);
    final latestId = incoming.isEmpty ? '' : incoming.last.id;
    if (_lastReadId == latestId) return;
    try {
      await repository.markRead();
      if (!usable || timeline != _timeline) return;
      final privateIds = messages
          .where(
            (item) =>
                !item.mine &&
                item.privacy.isPrivateChat == true &&
                (item.privacy.messageReadAtMilliseconds ?? 0) <= 0 &&
                (item.privacy.attachmentReadAtMilliseconds ?? 0) <= 0,
          )
          .map((item) => item.id)
          .toList();
      if (privateIds.isNotEmpty) {
        final updated = await repository.refreshReadMetadata(privateIds);
        if (!usable || timeline != _timeline) return;
        _merge(updated);
      }
      _lastReadId = latestId;
    } catch (error) {
      if (usable && timeline == _timeline) failure = _failure(error);
    }
  }

  Future<bool> sendText(String text, {MentionDraft? mentionDraft}) async {
    if (!usable || preparing || text.trim().isEmpty) return false;
    final timeline = _timeline;
    preparing = true;
    failure = null;
    notifyListeners();
    try {
      final message = await repository.prepareText(
        text,
        quoteId: quote?.id,
        mentionDraft: mentionDraft,
      );
      if (!usable || timeline != _timeline) return false;
      _merge([message]);
      quote = null;
      unawaited(resend(message.id));
      return true;
    } catch (error) {
      if (usable && timeline == _timeline) failure = _failure(error);
      return false;
    } finally {
      if (!_disposed) {
        preparing = false;
        notifyListeners();
      }
    }
  }

  Future<void> resend(String id) async {
    if (!usable || !_sending.add(id)) return;
    final timeline = _timeline;
    messages = List.unmodifiable(
      messages.map(
        (item) =>
            item.id == id ? item.withDelivery(MessageDelivery.sending) : item,
      ),
    );
    notifyListeners();
    try {
      final sent = await repository.send(id);
      if (!usable || timeline != _timeline) return;
      _sending.remove(id);
      _merge([sent]);
    } catch (error) {
      if (usable && timeline == _timeline) {
        messages = List.unmodifiable(
          messages.map(
            (item) => item.id == id
                ? item.withDelivery(MessageDelivery.failed)
                : item,
          ),
        );
        failure = _failure(error);
      }
    } finally {
      _sending.remove(id);
      progress.remove(id);
      if (!_disposed) notifyListeners();
    }
  }

  Future<bool> sendAttachment(ChatAttachment attachment) async {
    if (!usable || preparing) return false;
    final timeline = _timeline;
    preparing = true;
    failure = null;
    notifyListeners();
    try {
      final message = await repository.prepareAttachment(attachment);
      if (!usable || timeline != _timeline) return false;
      _merge([message]);
      unawaited(resend(message.id));
      return true;
    } catch (error) {
      if (usable && timeline == _timeline) failure = _failure(error);
      return false;
    } finally {
      if (!_disposed) {
        preparing = false;
        notifyListeners();
      }
    }
  }

  void reply(ChatMessage item) {
    if (!usable || !repository.canRetain(item)) {
      failure = const AppFailure(FailureKind.permissionDenied);
      notifyListeners();
      return;
    }
    quote = item;
    notifyListeners();
  }

  void clearReply() {
    quote = null;
    notifyListeners();
  }

  void updateDraft(String text) {
    draft = text;
    _draftTimer?.cancel();
    _draftTimer = Timer(const Duration(milliseconds: 500), flushDraft);
  }

  Future<void> flushDraft() async {
    _draftTimer?.cancel();
    if (!usable) return;
    late final MessageHistoryAccess access;
    try {
      access = repository.gateway.historyAccess(
        repository.generation,
        conversationId: repository.conversationId,
      );
    } catch (error) {
      draftFailure = _failure(error);
      return;
    }
    final value = draft;
    _draftTail = _draftTail.then((_) async {
      if (!usable) return;
      try {
        await repository.saveDraft(value, access: access);
        if (usable &&
            value == draft &&
            access.revision == repository.gateway.historyRevision) {
          draftFailure = null;
        }
      } catch (error) {
        if (usable &&
            value == draft &&
            access.revision == repository.gateway.historyRevision) {
          draftFailure = _failure(error);
        }
      }
      if (!_disposed) notifyListeners();
    });
    await _draftTail;
  }

  Future<void> revoke(ChatMessage item) async {
    try {
      await repository.revoke(item.id);
      if (!usable) return;
      _forget([item.id]);
      await refresh();
    } catch (error) {
      if (usable) failure = _failure(error);
    }
    if (!_disposed) notifyListeners();
  }

  void acceptBatchDeletion(Iterable<String> ids) {
    if (usable) _forget(ids);
  }

  Future<void> delete(ChatMessage item) async {
    try {
      await repository.delete(item.id);
      if (!usable) return;
      _forget([item.id]);
    } catch (error) {
      if (usable) failure = _failure(error);
    }
    if (!_disposed) notifyListeners();
  }

  Future<void> clearHistory() async {
    try {
      await repository.clearHistory();
      if (!usable) return;
      messages = const [];
      hasMore = false;
      _lastMinSeq = 0;
    } catch (error) {
      if (usable) failure = _failure(error);
    }
    if (!_disposed) notifyListeners();
  }

  Future<void> refreshConversation() async {
    try {
      final item = await repository.conversation();
      if (usable) conversation = item;
    } catch (error) {
      if (usable) failure = _failure(error);
    }
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    _draftTimer?.cancel();
    _refreshTimer?.cancel();
    unawaited(_events.cancel());
    unawaited(_privacy.cancel());
    unawaited(_invalidations.cancel());
    repository.clear();
    super.dispose();
  }
}
