import 'dart:async';

import 'package:flutter/foundation.dart';

import '../../../core/error/app_failure.dart';
import '../data/meeting_repository.dart';
import '../domain/meeting.dart';

final class MeetingController extends ChangeNotifier {
  MeetingController(this.repository) {
    _invalidations = repository.session.invalidations.listen((_) {
      ++_request;
      items = const [];
      failure = const AppFailure(FailureKind.sessionExpired);
      notice = null;
      loading = busy = false;
      if (!_disposed) {
        notifyListeners();
      }
    });
  }
  final MeetingRepository repository;
  late final StreamSubscription<int> _invalidations;
  List<Meeting> items = const [];
  AppFailure? failure;
  String? notice;
  bool loading = false, busy = false;
  bool _disposed = false;
  int _request = 0;
  bool get usable => !_disposed && repository.current;

  Future<void> refresh() async {
    if (!usable || busy) {
      return;
    }
    final request = ++_request;
    loading = true;
    failure = null;
    notifyListeners();
    try {
      final result = await repository.list();
      if (!usable || request != _request) {
        return;
      }
      items = result;
    } catch (error) {
      if (usable && request == _request) {
        failure = _failure(error);
      }
    } finally {
      if (usable && request == _request) {
        loading = false;
        notifyListeners();
      }
    }
  }

  Future<MeetingWriteResult?> perform(
    Future<MeetingWriteResult> Function() action,
  ) async {
    if (!usable || busy) {
      return null;
    }
    final request = ++_request;
    busy = true;
    loading = false;
    failure = null;
    notice = null;
    notifyListeners();
    try {
      final result = await action();
      if (!usable || request != _request) {
        return null;
      }
      notice = result.message;
      return result;
    } catch (error) {
      if (usable && request == _request) {
        failure = _failure(error);
      }
      return null;
    } finally {
      if (usable && request == _request) {
        busy = false;
        notifyListeners();
      }
    }
  }

  AppFailure _failure(Object error) =>
      error is AppFailure ? error : const AppFailure(FailureKind.unknown);
  @override
  void dispose() {
    _disposed = true;
    ++_request;
    unawaited(_invalidations.cancel());
    super.dispose();
  }
}
