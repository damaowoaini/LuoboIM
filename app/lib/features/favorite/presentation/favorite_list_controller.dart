import 'dart:async';

import 'package:flutter/foundation.dart';

import '../../../core/error/app_failure.dart';
import '../data/favorite_repository.dart';
import '../domain/favorite.dart';

final class FavoriteListController extends ChangeNotifier {
  FavoriteListController(this.repository) {
    _invalidation = repository.api.access.session.invalidations.listen((_) {
      _request++;
      items = [];
      selected.clear();
      _notify();
    });
  }
  final FavoriteRepository repository;
  late final StreamSubscription<int> _invalidation;
  FavoriteQuery query = const FavoriteQuery();
  List<FavoriteItem> items = [];
  final selected = <String>{};
  int total = 0, _page = 0, _request = 0;
  bool loading = false, more = true, selecting = false, _disposed = false;
  AppFailure? failure;
  void _notify() {
    if (!_disposed) notifyListeners();
  }

  bool _current(int request) =>
      !_disposed && request == _request && repository.isCurrent;
  Future<void> filter(FavoriteQuery value) {
    query = FavoriteQuery(
      keyword: value.keyword,
      type: value.type,
      categoryId: value.categoryId,
      tags: List.unmodifiable(value.tags),
    );
    selected.clear();
    return refresh();
  }

  Future<void> refresh() async {
    final request = ++_request;
    if (!repository.isCurrent || _disposed) return;
    items = [];
    _page = 0;
    more = true;
    loading = true;
    failure = null;
    _notify();
    await _load(request, 1);
  }

  Future<void> next() async {
    if (loading || !more || !repository.isCurrent || _disposed) return;
    loading = true;
    failure = null;
    _notify();
    await _load(_request, _page + 1);
  }

  Future<void> _load(int request, int page) async {
    try {
      final result = await repository.list(query: query, page: page);
      if (!_current(request)) return;
      final seen = items.map((item) => item.id).toSet();
      final additions = result.items
          .where((item) => seen.add(item.id))
          .toList();
      if (result.hasMore && additions.isEmpty) {
        throw const AppFailure(FailureKind.dataChanged);
      }
      items = List.unmodifiable([...items, ...additions]);
      total = result.total;
      more = result.hasMore;
      _page = page;
      if (page == 1) selected.retainAll(items.map((item) => item.id));
    } on AppFailure catch (error) {
      if (_current(request)) failure = error;
    } finally {
      if (_current(request)) {
        loading = false;
        _notify();
      }
    }
  }

  void selectionMode(bool enabled) {
    selecting = enabled;
    if (!enabled) selected.clear();
    _notify();
  }

  bool toggle(String id) {
    if (selected.contains(id)) {
      selected.remove(id);
    } else {
      if (selected.length >= 100) return false;
      selected.add(id);
    }
    _notify();
    return true;
  }

  @override
  void dispose() {
    _disposed = true;
    _request++;
    unawaited(_invalidation.cancel());
    super.dispose();
  }
}
