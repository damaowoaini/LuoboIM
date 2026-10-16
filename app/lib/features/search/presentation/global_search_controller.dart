import 'dart:async';
import 'package:flutter/foundation.dart';

import '../../../core/error/app_failure.dart';
import '../../../core/im/im_event.dart';
import '../../contacts/domain/contact.dart';
import '../../group/domain/group.dart';
import '../data/global_search_repository.dart';
import '../domain/global_search.dart';

final class GlobalSearchController extends ChangeNotifier {
  GlobalSearchController(this.repository) {
    _session = repository.access.session.invalidations.listen((_) => invalidate());
    _access = repository.access.session.accessChanges.listen((_) => invalidate());
    _privacy = repository.gateway.privacyLedger.changes.listen((_) {
      if (!_disposed) notifyListeners();
    });
    _events = repository.gateway.events.listen((event) {
      if (!usable || event.generation != repository.access.generation) return;
      if (event.kind == ImEventKind.historyInvalidated) {
        ++_request;
        _cut = null;
        text = files = const [];
        for (final kind in GlobalSearchKind.values) {
          if (phases[kind] == GlobalSearchPhase.loading ||
              kind == GlobalSearchKind.text || kind == GlobalSearchKind.files) {
            phases[kind] = GlobalSearchPhase.idle;
          }
        }
        historyNotice = '聊天记录已清理，请重新搜索';
        notifyListeners();
      } else if (event.kind == ImEventKind.conversationHistoryInvalidated) {
        text = List.unmodifiable(text.where((hit) => !event.resourceIds.contains(hit.conversationId)));
        files = List.unmodifiable(files.where((hit) => !event.resourceIds.contains(hit.conversationId)));
        notifyListeners();
      } else if (event.kind == ImEventKind.messageDeleted ||
          event.kind == ImEventKind.messageRevoked) {
        repository.forget(event.resourceIds);
        text = List.unmodifiable(text.where((hit) => !event.resourceIds.contains(hit.message.id)));
        files = List.unmodifiable(files.where((hit) => !event.resourceIds.contains(hit.message.id)));
        notifyListeners();
      }
    });
  }
  final GlobalSearchRepository repository;
  late final StreamSubscription<int> _session, _access;
  late final StreamSubscription<void> _privacy;
  late final StreamSubscription<ImEvent> _events;
  final phases = {for (final kind in GlobalSearchKind.values) kind: GlobalSearchPhase.idle};
  final failures = <GlobalSearchKind, AppFailure>{};
  final shown = {for (final kind in GlobalSearchKind.values) kind: 30};
  final _turns = <GlobalSearchKind, int>{};
  List<Contact> contacts = const [];
  List<GroupSummary> groups = const [];
  List<GlobalMessageHit> text = const [], files = const [];
  String query = '';
  String? historyNotice;
  bool _disposed = false, _valid = true;
  int _request = 0;
  GlobalSearchQuery? _cut;
  int get epoch => _request;
  bool get usable => !_disposed && _valid && repository.current;
  bool get searched => query.isNotEmpty;

  Future<void> search(String raw) async {
    if (!usable) return;
    final keyword = raw.trim();
    if (keyword.isNotEmpty && keyword == query &&
        phases.values.contains(GlobalSearchPhase.loading)) return;
    ++_request;
    query = keyword;
    _cut = null;
    contacts = const []; groups = const []; text = files = const [];
    failures.clear(); historyNotice = null;
    for (final kind in GlobalSearchKind.values) {
      phases[kind] = GlobalSearchPhase.idle; shown[kind] = 30;
    }
    notifyListeners();
    if (query.isEmpty) return;
    await Future.wait(GlobalSearchKind.values.map(retry));
  }

  Future<void> retry(GlobalSearchKind kind) async {
    if (!usable || query.isEmpty || phases[kind] == GlobalSearchPhase.loading) return;
    final request = _request;
    final turn = (_turns[kind] ?? 0) + 1;
    _turns[kind] = turn;
    bool current() => usable && request == _request && _turns[kind] == turn;
    phases[kind] = GlobalSearchPhase.loading; failures.remove(kind);
    notifyListeners();
    try {
      final cut = _cut ??= repository.begin(query, () => usable && request == _request);
      switch (kind) {
        case GlobalSearchKind.contacts:
          final contactsResult = await repository.friends(cut);
          if (current()) contacts = contactsResult;
        case GlobalSearchKind.groups:
          final groupsResult = await repository.groups(cut);
          if (current()) groups = groupsResult;
        case GlobalSearchKind.text:
        case GlobalSearchKind.files:
          final messageResult = await repository.messages(cut, kind);
          if (current()) {
            if (kind == GlobalSearchKind.text) { text = messageResult; } else { files = messageResult; }
          }
      }
      if (current()) phases[kind] = GlobalSearchPhase.ready;
    } catch (error) {
      if (current()) {
        failures[kind] = error is AppFailure ? error : const AppFailure(FailureKind.unknown);
        phases[kind] = GlobalSearchPhase.error;
      }
    } finally {
      if (current()) notifyListeners();
    }
  }

  List<GlobalMessageHit> get visibleFiles => List.unmodifiable(
      files.where((hit) => hit.visibleAt(repository.now())));
  List<GlobalMessageGroup> get threads {
    final rows = <String, List<GlobalMessageHit>>{};
    for (final hit in text) {
      if (hit.visibleAt(repository.now())) {
        rows.putIfAbsent(hit.conversationId, () => []).add(hit);
      }
    }
    final result = rows.entries.map((e) =>
        GlobalMessageGroup(e.key, e.value.first.title, e.value)).toList();
    result.sort((a, b) => GlobalSearchRepository.compareHits(a.hits.first, b.hits.first));
    return List.unmodifiable(result);
  }
  void more(GlobalSearchKind kind) {
    if (!usable || phases[kind] != GlobalSearchPhase.ready) return;
    shown[kind] = (shown[kind] ?? 30) + 30;
    notifyListeners();
  }
  void invalidate() {
    if (_disposed) return;
    _valid = false; ++_request; _cut = null;
    query = ''; contacts = const []; groups = const []; text = files = const [];
    failures.clear();
    for (final kind in GlobalSearchKind.values) { phases[kind] = GlobalSearchPhase.idle; }
    notifyListeners();
  }
  @override
  void dispose() {
    _disposed = true; ++_request;
    unawaited(_session.cancel()); unawaited(_access.cancel());
    unawaited(_events.cancel()); unawaited(_privacy.cancel());
    repository.dispose();
    super.dispose();
  }
}
