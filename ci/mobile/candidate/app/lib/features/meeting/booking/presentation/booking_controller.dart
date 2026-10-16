import 'dart:async';

import 'package:flutter/foundation.dart';

import '../data/booking_contract.dart';
import '../data/booking_pending_journal.dart';
import '../data/meeting_booking_repository.dart';
import '../domain/booking_view.dart';

enum BookingAvailability { unsupported, checking, ready, unavailable }

final class BookingController extends ChangeNotifier {
  BookingController(this.repository, {required bool released,
    DateTime Function()? now, Stream<int>? invalidations, Stream<int>? accessChanges})
    : availability = released ? BookingAvailability.checking : BookingAvailability.unsupported,
      _released = released, now = now ?? DateTime.now {
    _invalidations = invalidations?.listen((_) => retire());
    _accessChanges = accessChanges?.listen((_) => retire());
  }
  final MeetingBookingRepository repository;
  final bool _released;
  final DateTime Function() now;
  StreamSubscription<int>? _invalidations, _accessChanges;
  BookingAvailability availability;
  List<OwnerBookingView> records = const [];
  List<BookingJournalEntry> pending = const [];
  final Map<String, BookingIntent> _held = {};
  final Set<String> _busy = {};
  bool loading = false, includeCancelled = false;
  bool hasMore = false;
  String _cursor = '';
  String? notice;
  bool _disposed = false, _retired = false;
  int _request = 0;
  bool get usable {
    if (_disposed || _retired) return false;
    try { repository.scope.check(); return true; } catch (_) { return false; }
  }
  bool get canWrite => usable && availability == BookingAvailability.ready;
  List<String> get unknownIDs => List.unmodifiable({...pending.map((e) => e.id), ..._held.keys});
  bool get canCreate => canWrite && !_busy.contains('create') &&
    !pending.any((e) => e.operation == BookingOperation.create) &&
    !_held.values.any((e) => e.operation == BookingOperation.create);
  bool canEdit(OwnerBookingView row) => canWrite && row.editable && !_busy.contains(row.id.value) &&
    !pending.any((e) => e.target == row.id.value) && !_held.values.any((e) => e.target == row.id.value);
  bool isBusy(String id) => _busy.contains(id) || _busy.contains('intent:$id');
  void _emit() { if (!_disposed) notifyListeners(); }
  void _check() {
    if (!usable) throw const BookingLocalFailure(BookingLocalFailureKind.staleSession);
  }
  Future<void> start() async {
    await _pending();
    if (usable && _released) await refresh();
  }
  Future<void> _pending() async {
    _check();
    final rows = await repository.journal.pending();
    _check(); pending = rows;
  }
  Future<void> refresh({bool? cancelled}) async {
    if (!usable || !_released) return;
    if (cancelled != null) includeCancelled = cancelled;
    final request = ++_request, filter = includeCancelled;
    loading = true; notice = null; _emit();
    try {
      final page = await repository.list(includeCancelled: filter);
      if (!usable || request != _request) return;
      final parsed = page.records.map((r) => OwnerBookingView.fromRecord(r, repository.scope.owner)).toList();
      if (page.hasMore && (page.nextCursor == null || page.nextCursor!.length > 512)) {
        throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
      }
      records = List.unmodifiable(parsed); hasMore = page.hasMore; _cursor = page.nextCursor ?? '';
      availability = BookingAvailability.ready;
      await _pending();
    } catch (_) {
      if (usable && request == _request) {
        availability = BookingAvailability.unavailable; notice = '预约资料暂时无法加载。已有待确认操作会保留。';
      }
    } finally { if (usable && request == _request) { loading = false; _emit(); } }
  }
  Future<void> loadMore() async {
    if (!usable || loading || !hasMore || availability != BookingAvailability.ready) return;
    final request = ++_request, cursor = _cursor, filter = includeCancelled;
    loading = true; _emit();
    try {
      final page = await repository.list(cursor: cursor, includeCancelled: filter);
      if (!usable || request != _request) return;
      if (page.hasMore && (page.nextCursor == cursor || page.nextCursor == null || page.nextCursor!.length > 512)) {
        throw const BookingLocalFailure(BookingLocalFailureKind.invalid);
      }
      final combined = {for (final row in records) row.id.value: row};
      for (final raw in page.records) {
        final row = OwnerBookingView.fromRecord(raw, repository.scope.owner), old = combined[raw.id];
        if (old != null && (row.version < old.version || (old.state == 'cancelled' && row.state != 'cancelled'))) {
          throw const BookingLocalFailure(BookingLocalFailureKind.staleResponse);
        }
        combined[raw.id] = row;
      }
      records = List.unmodifiable(combined.values); hasMore = page.hasMore; _cursor = page.nextCursor ?? '';
    } catch (_) { if (usable && request == _request) notice = '后续预约暂时无法加载，请重新刷新核对。'; }
    finally { if (usable && request == _request) { loading = false; _emit(); } }
  }
  Future<OwnerBookingView?> detail(String id) async {
    if (!usable || !_released) return null;
    try {
      final raw = await repository.detail(BookingID(id).value); _check();
      return OwnerBookingView.fromRecord(raw, repository.scope.owner);
    } catch (_) { if (usable) { notice = '预约详情暂时无法核对，请重试。'; _emit(); } return null; }
  }
  Future<BookingReceipt?> submit(BookingIntent intent) async {
    _check();
    final key = intent.target ?? 'create';
    if (!canWrite || _busy.contains(key) ||
        (intent.operation == BookingOperation.create && !canCreate) ||
        (intent.target != null && pending.any((e) => e.target == intent.target && e.id != intent.id))) return null;
    _busy.add(key); _busy.add('intent:${intent.id}'); _held[intent.id] = intent; notice = null; _emit();
    try {
      final receipt = await repository.submit(intent); _check();
      _held.remove(intent.id); notice = receipt.definitiveNotApplied ? '原操作已确认未应用。' : '预约操作已保存，请刷新详情核对。';
      return receipt;
    } catch (_) {
      if (usable) notice = '结果尚未确认。请查询原操作回执，勿重新创建。';
      return null;
    } finally {
      _busy.remove(key); _busy.remove('intent:${intent.id}');
      if (usable) { try { await _pending(); } catch (_) { notice = '本地待确认记录暂时无法读取，原操作会保留。'; } _emit(); }
    }
  }
  Future<BookingReceipt?> recover(String id, {bool retry = false, bool resolve = false}) async {
    if (!usable || !_released || _busy.contains('intent:$id')) return null;
    final key = 'intent:$id'; _busy.add(key); notice = null; _emit();
    try {
      // A local prepare failure may leave only this mounted controller's frozen
      // draft. Explicit recovery first persists those same bytes and UUID; it
      // never substitutes a new intent or dispatches a create by itself.
      final held = _held[id];
      if (held != null) { await repository.journal.prepare(held); _check(); }
      final receipt = await (resolve ? repository.resolveUnknown(id) : retry ? repository.retryFrozen(id) : repository.recover(id));
      _check();
      if (receipt.found) {
        _held.remove(id);
        notice = receipt.definitiveNotApplied ? '原操作已确认未应用，可以重新填写。' : '原操作已经保存。请刷新详情；这不表示预约已取消。';
      } else { notice = '尚无原操作回执，结果仍未确认，请勿重新创建。'; }
      return receipt;
    } catch (_) { if (usable) notice = '暂时无法确认原操作，原意图和草稿会保留。'; return null; }
    finally { _busy.remove(key); if (usable) {
      try { await _pending(); } catch (_) { notice = '本地待确认列表暂时无法读取，请保留原操作并重新核对。'; }
      _emit();
    } }
  }
  void retire() {
    if (_disposed || _retired) return;
    _retired = true; ++_request; records = const []; pending = const []; _held.clear();
    loading = false; notice = '登录或本机访问状态已变化，请重新进入。'; _emit();
  }
  @override
  void dispose() {
    if (_disposed) return;
    _disposed = true; ++_request; records = const []; pending = const []; _held.clear();
    unawaited(_invalidations?.cancel() ?? Future<void>.value());
    unawaited(_accessChanges?.cancel() ?? Future<void>.value()); super.dispose();
  }
}
