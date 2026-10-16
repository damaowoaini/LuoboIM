import 'dart:async';

import 'package:flutter/foundation.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/im/im_event.dart';
import '../data/chat_repository.dart';
import '../domain/chat_message.dart';
import '../domain/chat_search.dart';

final class ChatSearchController extends ChangeNotifier {
  ChatSearchController(this.repository, {this.pageSize = 30}) {
    _events = repository.gateway.events.listen((event) {
      if (event.generation != repository.generation || !usable) return;
      if (event.kind == ImEventKind.historyInvalidated ||
          event.kind == ImEventKind.conversationHistoryInvalidated &&
              event.resourceIds.contains(repository.conversationId)) {
        ++_request;
        repository.resetHistory();
        messages = const [];
        query = '';
        loading = loadingMore = hasMore = false;
        failure = event.failure;
        notifyListeners();
      } else if (event.kind == ImEventKind.messageDeleted ||
          event.kind == ImEventKind.messageRevoked) {
        ++_request;
        repository.forgetMessages(event.resourceIds);
        messages = List.unmodifiable(
          messages.where((item) => !event.resourceIds.contains(item.id)),
        );
        loading = loadingMore = false;
        notifyListeners();
      }
    });
    _privacy = repository.gateway.privacyLedger.changes.listen((_) {
      if (!_disposed && repository.current) notifyListeners();
    });
    _invalidations = repository.session.invalidations.listen((_) {
      _valid = false;
      ++_request;
      messages = const [];
      query = '';
      loading = false;
      loadingMore = false;
      hasMore = false;
      failure = const AppFailure(FailureKind.sessionExpired);
      notifyListeners();
    });
  }
  final ChatRepository repository;
  final int pageSize;
  late final StreamSubscription<int> _invalidations;
  late final StreamSubscription<ImEvent> _events;
  late final StreamSubscription<void> _privacy;
  List<ChatMessage> messages = const [];
  String query = '';
  ChatHistoryFilter filter = ChatHistoryFilter.all;
  AppFailure? failure;
  bool loading = false, loadingMore = false, hasMore = false, searched = false;
  bool _valid = true, _disposed = false;
  int _request = 0, _page = 1, _timePosition = 0;

  bool get usable => _valid && !_disposed && repository.current;

  Future<void> search({String? query, ChatHistoryFilter? filter}) async {
    if (!usable) return;
    final request = ++_request;
    this.query = (query ?? this.query).trim();
    this.filter = filter ?? this.filter;
    if (this.filter == ChatHistoryFilter.media) this.query = '';
    _page = 1;
    // Freeze this search's upper time boundary so incoming messages do not
    // shift offset-based pages while the user is browsing old results.
    _timePosition = DateTime.now().millisecondsSinceEpoch ~/ 1000;
    messages = const [];
    failure = null;
    hasMore = false;
    loadingMore = false;
    searched = this.query.isNotEmpty || this.filter != ChatHistoryFilter.all;
    loading = searched;
    notifyListeners();
    if (!searched) return;
    try {
      final page = await _fetch(1);
      if (!usable || request != _request) return;
      messages = _sorted(page.messages);
      hasMore = page.hasMore;
    } catch (error) {
      if (usable && request == _request) failure = _failure(error);
    } finally {
      if (usable && request == _request) {
        loading = false;
        notifyListeners();
      }
    }
  }

  Future<ChatSearchPage> _fetch(int page) => repository.search(
    query: query,
    filter: filter,
    timePosition: _timePosition,
    pageIndex: page,
    count: pageSize,
  );

  Future<void> loadMore() async {
    if (!usable || loading || loadingMore || !hasMore) return;
    final request = _request;
    loadingMore = true;
    failure = null;
    notifyListeners();
    try {
      final page = await _fetch(_page + 1);
      if (!usable || request != _request) return;
      final merged = {for (final message in messages) message.id: message};
      final previousCount = merged.length;
      for (final message in page.messages) {
        merged[message.id] = message;
      }
      if (page.hasMore && merged.length == previousCount) {
        hasMore = false;
        throw const AppFailure(FailureKind.invalidResponse);
      }
      messages = _sorted(merged.values);
      hasMore = page.hasMore;
      _page++;
    } catch (error) {
      if (usable && request == _request) failure = _failure(error);
    } finally {
      if (usable && request == _request) {
        loadingMore = false;
        notifyListeners();
      }
    }
  }

  List<ChatMessage> _sorted(Iterable<ChatMessage> values) {
    final result = values.toList()
      ..sort((a, b) {
        final order = b.sentAt.compareTo(a.sentAt);
        return order == 0 ? a.id.compareTo(b.id) : order;
      });
    return List.unmodifiable(result);
  }

  AppFailure _failure(Object error) =>
      error is AppFailure ? error : const AppFailure(FailureKind.unknown);

  @override
  void dispose() {
    _disposed = true;
    ++_request;
    unawaited(_invalidations.cancel());
    unawaited(_events.cancel());
    unawaited(_privacy.cancel());
    super.dispose();
  }
}
