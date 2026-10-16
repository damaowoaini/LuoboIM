import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../providers.dart';
import '../tenant/session_access.dart';
import '../tenant/session_coordinator.dart';
import 'api_client.dart';

final sessionAccessProvider = Provider.autoDispose<SessionAccess?>((ref) {
  ref.watch(appAccessProvider.select((access) => access.revision));
  final snapshot = ref.watch(
    appRuntimeProvider.select((runtime) => runtime.session.state),
  );
  if (snapshot.phase != SessionPhase.online || snapshot.credentials == null) {
    return null;
  }
  if (!ref.read(appRuntimeProvider).session.interactiveAllowed) return null;
  return SessionAccess(ref.read(appRuntimeProvider).session);
});

final tenantApiProvider = Provider.autoDispose<TenantApi?>((ref) {
  final access = ref.watch(sessionAccessProvider);
  if (access == null) return null;
  final api = TenantApi(access);
  ref.onDispose(api.close);
  return api;
});

/// Chat and IM tokens always travel to their own immutable service root.
final class TenantApi {
  TenantApi(this.access, {Dio? dio})
    : _dio =
          dio ?? Dio(BaseOptions(connectTimeout: const Duration(seconds: 15))) {
    final credentials = access.credentials;
    void expired() {
      if (access.isCurrent) {
        unawaited(
          access.session.signOut(revoke: false).catchError((Object _) {}),
        );
      }
    }

    chat = ApiClient(
      base: credentials.endpoints.chat,
      dio: _dio,
      token: credentials.chatToken,
      isCurrent: () => access.isCurrent,
      onExpired: expired,
    );
    im = ApiClient(
      base: credentials.endpoints.im,
      dio: _dio,
      token: credentials.imToken,
      isCurrent: () => access.isCurrent,
      onExpired: expired,
    );
    _invalidation = access.session.invalidations.listen((_) => close());
    _accessInvalidation = access.session.accessChanges.listen((_) => close());
  }
  final SessionAccess access;
  final Dio _dio;
  late final ApiClient chat;
  late final ApiClient im;
  late final StreamSubscription<int> _invalidation;
  late final StreamSubscription<int> _accessInvalidation;
  bool _closed = false;
  void close() {
    if (_closed) return;
    _closed = true;
    chat.close();
    im.close();
    _dio.close(force: true);
    unawaited(_invalidation.cancel());
    unawaited(_accessInvalidation.cancel());
  }
}
