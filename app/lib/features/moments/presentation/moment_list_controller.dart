import 'dart:async';

import 'package:flutter/foundation.dart';

import '../../../core/error/app_failure.dart';
import '../data/moments_repository.dart';
import '../domain/moment.dart';

final class MomentListController extends ChangeNotifier {
  MomentListController(this.repository, {this.author}) {
    _invalidation = repository.api.access.session.invalidations.listen((_) {
      _request++;
      items = [];
      loading = false;
      _notify();
    });
  }
  final MomentsRepository repository;
  final String? author;
  late final StreamSubscription<int> _invalidation;
  List<Moment> items = [];
  bool loading = false, more = true, _disposed = false;
  int _request = 0, _page = 0;
  AppFailure? failure;
  int? failedPage;
  final busy = <String>{};
  bool get usable => !_disposed && repository.isCurrent;
  bool _current(int request) => usable && request == _request;
  void _notify() {
    if (!_disposed) notifyListeners();
  }

  Future<void> refresh() async {
    if (!usable) return;
    final request = ++_request;
    loading = true;
    failure = null;
    failedPage = null;
    _notify();
    await _load(request, 1);
  }

  Future<void> next() async {
    if (!usable || loading || !more) return;
    loading = true;
    failure = null;
    _notify();
    await _load(_request, _page + 1);
  }

  Future<void> retry() => failedPage == 1 ? refresh() : next();

  Future<void> _load(int request, int page) async {
    try {
      final result = await repository.list(author: author, page: page);
      if (!_current(request)) return;
      final previous = page == 1 ? <Moment>[] : items;
      final seen = previous.map((item) => item.id).toSet();
      final additions = result.items
          .where((item) => seen.add(item.id))
          .toList();
      if (result.hasMore && additions.isEmpty) {
        throw const AppFailure(FailureKind.dataChanged);
      }
      items = List.unmodifiable([...previous, ...additions]);
      more = result.hasMore;
      _page = page;
    } on AppFailure catch (error) {
      if (_current(request)) {
        failure = error;
        failedPage = page;
      }
    } finally {
      if (_current(request)) {
        loading = false;
        _notify();
      }
    }
  }

  Future<void> update(String id, Future<Moment> Function() action) async {
    if (!usable || !busy.add(id)) return;
    _notify();
    try {
      final item = await action();
      if (!usable) return;
      items = List.unmodifiable(items.map((old) => old.id == id ? item : old));
    } finally {
      busy.remove(id);
      _notify();
    }
  }

  void remove(String id) {
    if (!usable) return;
    items = List.unmodifiable(items.where((item) => item.id != id));
    _notify();
  }

  @override
  void dispose() {
    _disposed = true;
    _request++;
    unawaited(_invalidation.cancel());
    super.dispose();
  }
}
