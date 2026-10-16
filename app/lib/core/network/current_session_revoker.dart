import 'package:dio/dio.dart';

import '../tenant/session_credentials.dart';
import '../tenant/session_revoker.dart';
import 'api_client.dart';

/// Logout must survive invalidation of ordinary authenticated clients. Each
/// call captures one immutable endpoint/token pair and owns its short-lived
/// transport; it cannot revoke a newly signed-in account or a whole platform.
final class CurrentSessionRevoker implements SessionRevoker {
  CurrentSessionRevoker({
    Dio Function()? transport,
    this.timeout = const Duration(seconds: 10),
  }) : _transport =
           transport ??
           (() => Dio(BaseOptions(connectTimeout: const Duration(seconds: 5))));
  final Dio Function() _transport;
  final Duration timeout;
  @override
  Future<SessionRevocation> revoke(SessionCredentials credentials) async {
    final dio = _transport();
    final api = ApiClient(
      base: credentials.endpoints.chat,
      dio: dio,
      token: credentials.chatToken,
    );
    try {
      final raw = await api
          .post('account/logout', {'imToken': credentials.imToken})
          .timeout(timeout);
      if (raw is! Map || raw['revoked'] != true) {
        return SessionRevocation.unconfirmed;
      }
      if (raw['sessionClosed'] == true && raw['pending'] == false) {
        return SessionRevocation.confirmed;
      }
      if (raw['sessionClosed'] == false && raw['pending'] == true) {
        return SessionRevocation.pending;
      }
      return SessionRevocation.unconfirmed;
    } catch (_) {
      // Local cleanup still finishes. The login page distinguishes lack of
      // acknowledgement from a durable server-side pending revocation.
      return SessionRevocation.unconfirmed;
    } finally {
      api.close();
      dio.close(force: true);
    }
  }
}
