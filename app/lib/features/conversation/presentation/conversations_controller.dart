import 'dart:async';

import 'package:flutter/foundation.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/im/im_event.dart';
import '../data/conversation_repository.dart';
import '../domain/conversation.dart';

final class ConversationsController extends ChangeNotifier {
  ConversationsController(this.repository) {
    _events = repository.gateway.events.listen((event) {
      if (!repository.current ||
          !repository.session.acceptsEvent(event.generation)) {
        return;
      }
      if (event.kind == ImEventKind.conversationHistoryInvalidated) {
        ++_revision;
        loading = loadingMore = false;
        items = List.unmodifiable(
          items.map(
            (item) => event.resourceIds.contains(item.id)
                ? item.withoutMessagePreview()
                : item,
          ),
        );
        notifyListeners();
        return;
      }
      if (event.kind == ImEventKind.historyInvalidated) {
        ++_revision;
        _debounce?.cancel();
        _reloadPending = false;
        _historyNeedsReview = true;
        items = const [];
        loading = loadingMore = hasMore = false;
        failure = event.failure;
        notifyListeners();
        return;
      }
      switch (event.kind) {
        case ImEventKind.connecting:
          connection = '正在连接…';
        case ImEventKind.connected:
          connection = null;
        case ImEventKind.connectionFailed:
          connection = '网络异常，正在尝试重新连接';
        case ImEventKind.syncStarted:
          connection = '正在同步消息…';
        case ImEventKind.syncFinished:
          connection = null;
        case ImEventKind.syncFailed:
          connection = '消息同步失败，请下拉重试';
        default:
          break;
      }
      if (const {
            ImEventKind.conversationsChanged,
            ImEventKind.unreadChanged,
            ImEventKind.messageReceived,
            ImEventKind.offlineMessageReceived,
            ImEventKind.messageDeleted,
            ImEventKind.messageRevoked,
            ImEventKind.syncFinished,
            ImEventKind.contactsChanged,
            ImEventKind.groupsChanged,
          }.contains(event.kind) &&
          !_historyNeedsReview) {
        _debounce?.cancel();
        _debounce = Timer(const Duration(milliseconds: 120), () {
          if (!_historyNeedsReview) unawaited(refresh());
        });
      }
      notifyListeners();
    });
    _privacy = repository.gateway.privacyLedger.changes.listen((_) {
      if (!_disposed && repository.current) notifyListeners();
    });
    _invalidations = repository.session.invalidations.listen((_) {
      ++_revision;
      _debounce?.cancel();
      items = const [];
      loading = false;
      loadingMore = false;
      failure = null;
      connection = null;
      hasMore = false;
      notifyListeners();
    });
  }

  final ConversationRepository repository;
  late final StreamSubscription<ImEvent> _events;
  late final StreamSubscription<void> _privacy;
  late final StreamSubscription<int> _invalidations;
  Timer? _debounce;
  List<Conversation> items = const [];
  AppFailure? failure;
  String? connection;
  bool loading = false;
  bool loadingMore = false;
  bool hasMore = true;
  bool _disposed = false;
  bool _reloadPending = false;
  bool _historyNeedsReview = false;
  int _revision = 0;
  final Set<String> pendingActions = {};

  Future<void> refresh() async {
    if (_disposed) return;
    _historyNeedsReview = false;
    if (loading || loadingMore) {
      _reloadPending = true;
      return;
    }
    final revision = ++_revision;
    final count = items.length > 40 ? items.length : 40;
    loading = true;
    failure = null;
    notifyListeners();
    try {
      final result = await repository.list(count: count);
      if (_disposed || revision != _revision) return;
      items = List.unmodifiable(result);
      hasMore = result.length == count;
    } catch (error) {
      if (!_disposed && revision == _revision) {
        failure = error is AppFailure
            ? error
            : const AppFailure(FailureKind.unknown);
      }
    } finally {
      if (!_disposed && revision == _revision) {
        loading = false;
        notifyListeners();
        _reloadIfPending();
      }
    }
  }

  Future<void> loadMore() async {
    if (_disposed || loading || loadingMore || !hasMore) return;
    final revision = _revision;
    loadingMore = true;
    notifyListeners();
    try {
      final result = await repository.list(offset: items.length);
      if (_disposed || revision != _revision) return;
      final merged = {
        for (final item in items) item.id: item,
        for (final item in result) item.id: item,
      };
      items = List.unmodifiable(merged.values);
      hasMore = result.length == 40;
    } catch (error) {
      if (!_disposed && revision == _revision) {
        failure = error is AppFailure
            ? error
            : const AppFailure(FailureKind.unknown);
      }
    } finally {
      if (!_disposed && revision == _revision) {
        loadingMore = false;
        notifyListeners();
        _reloadIfPending();
      }
    }
  }

  void _reloadIfPending() {
    if (!_reloadPending) return;
    _reloadPending = false;
    unawaited(refresh());
  }

  Future<AppFailure?> perform(String id, Future<void> Function() action) async {
    if (!pendingActions.add(id)) return null;
    final revision = _revision;
    notifyListeners();
    try {
      await action();
      if (_disposed || revision != _revision) {
        return const AppFailure(FailureKind.cancelled);
      }
      await refresh();
      return null;
    } catch (error) {
      return error is AppFailure
          ? error
          : const AppFailure(FailureKind.unknown);
    } finally {
      pendingActions.remove(id);
      if (!_disposed) notifyListeners();
    }
  }

  @override
  void dispose() {
    _disposed = true;
    _debounce?.cancel();
    unawaited(_events.cancel());
    unawaited(_privacy.cancel());
    unawaited(_invalidations.cancel());
    super.dispose();
  }
}
