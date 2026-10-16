import '../error/app_failure.dart';
import 'session_coordinator.dart';
import 'session_credentials.dart';

/// Freeze the identity at repository construction. Never let an old screen's
/// delayed action acquire the new account's generation or credentials.
final class SessionAccess {
  SessionAccess(this.session)
    : snapshot = session.state,
      interactiveRevision = session.interactiveRevision {
    check();
  }
  final SessionCoordinator session;
  final SessionSnapshot snapshot;
  final int interactiveRevision;
  int get generation => snapshot.generation;
  SessionCredentials get credentials => snapshot.credentials!;
  bool get isCurrent =>
      snapshot.credentials != null &&
      session.isInteractiveCurrent(generation, interactiveRevision);
  void check() {
    if (!isCurrent) throw const AppFailure(FailureKind.cancelled);
  }
}
