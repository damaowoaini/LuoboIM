import 'session_credentials.dart';

enum SessionRevocation { confirmed, pending, unconfirmed }

abstract interface class SessionRevoker {
  Future<SessionRevocation> revoke(SessionCredentials credentials);
}
