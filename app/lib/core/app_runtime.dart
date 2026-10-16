import 'dart:async';

import 'package:flutter/foundation.dart';

import '../features/auth/data/auth_repository.dart';
import 'config/app_config.dart';
import 'config/route_config_loader.dart';
import 'error/app_failure.dart';
import 'storage/consent_store.dart';
import 'tenant/session_coordinator.dart';

enum AppPhase {
  checkingConsent,
  consent,
  starting,
  login,
  signingIn,
  online,
  unavailable,
}

typedef AuthRepositoryFactory = Future<AuthRepository> Function(
  Uri route,
  int? generation,
);

final class AppRuntime extends ChangeNotifier {
  AppRuntime({
    required this.config,
    required this._consent,
    required this._routes,
    required this.session,
    required this._auth,
  }) {
    _subscription = session.changes.listen(_onSession);
  }

  final AppConfig config;
  final ConsentStore _consent;
  final RouteConfigLoader _routes;
  final SessionCoordinator session;
  final AuthRepositoryFactory _auth;
  late final StreamSubscription<SessionSnapshot> _subscription;
  AppPhase phase = AppPhase.checkingConsent;
  AppFailure? failure;
  List<Uri> lines = const [];
  int lineIndex = 0;
  bool _disposed = false;
  bool _hasConsent = false;
  bool _bootstrapping = false;
  Future<void>? _startup;
  bool get hasPrivacyConsent => _hasConsent;

  void _changed(AppPhase value, [AppFailure? error]) {
    if (_disposed) return;
    phase = value;
    failure = error;
    notifyListeners();
  }

  Future<void> start() => _startup ??= _start().whenComplete(() {
    _startup = null;
  });

  Future<void> _start() async {
    try {
      _hasConsent = await _consent.read();
      if (!_hasConsent) {
        _changed(AppPhase.consent);
        return;
      }
      await _bootstrap();
    } catch (_) {
      _changed(AppPhase.unavailable, const AppFailure(FailureKind.storage));
    }
  }

  Future<void> acceptConsent() =>
      _startup ??= _acceptConsent().whenComplete(() {
        _startup = null;
      });

  Future<void> _acceptConsent() async {
    if (!config.hasPolicies) {
      _changed(
        AppPhase.unavailable,
        const AppFailure(FailureKind.configuration),
      );
      return;
    }
    try {
      await _consent.accept();
      _hasConsent = true;
    } catch (_) {
      _changed(AppPhase.consent, const AppFailure(FailureKind.storage));
      return;
    }
    await _bootstrap();
  }

  Future<void> retry() => start();

  Future<void> _bootstrap() async {
    if (_disposed) return;
    _bootstrapping = true;
    _changed(AppPhase.starting);
    AppFailure? restoreFailure;
    try {
      await session.restore();
    } on AppFailure catch (error) {
      restoreFailure = error;
    } catch (_) {
      restoreFailure = const AppFailure(FailureKind.unknown);
    } finally {
      _bootstrapping = false;
    }
    if (_disposed) return;
    // Returning users reconnect directly to their validated tenant. Bootstrap
    // configuration availability must not become an extra login dependency.
    if (session.state.phase == SessionPhase.online) {
      _changed(AppPhase.online);
      return;
    }
    await _prepareLogin(restoreFailure);
  }

  Future<void> _prepareLogin([AppFailure? warning]) async {
    if (lines.isNotEmpty) {
      _changed(AppPhase.login, warning);
      return;
    }
    _changed(AppPhase.starting);
    try {
      final routes = await _routes.load();
      if (_disposed || session.state.phase == SessionPhase.online) return;
      lines = routes;
      lineIndex = 0;
      _changed(AppPhase.login, warning);
    } on AppFailure catch (error) {
      _changed(AppPhase.unavailable, error);
    } catch (_) {
      _changed(AppPhase.unavailable, const AppFailure(FailureKind.network));
    }
  }

  void _onSession(SessionSnapshot snapshot) {
    switch (snapshot.phase) {
      case SessionPhase.signedOut:
      case SessionPhase.failed:
        if (!_bootstrapping) {
          unawaited(_prepareLogin(snapshot.failure));
        }
      case SessionPhase.signingIn:
        _changed(AppPhase.signingIn);
      case SessionPhase.online:
        _changed(AppPhase.online, snapshot.failure);
      case SessionPhase.restoring:
      case SessionPhase.switchingLine:
        _changed(AppPhase.starting);
    }
  }

  Future<AuthRepository> auth({int? generation}) {
    if (lines.isEmpty || lineIndex >= lines.length) {
      throw const AppFailure(FailureKind.configuration);
    }
    return _auth(lines[lineIndex], generation);
  }

  Future<void> signIn({
    required AuthIdentity identity,
    String? password,
    String? verificationCode,
    String? googleAuthCode,
  }) async {
    try {
      await session.signIn(
        (generation) async => (await auth(generation: generation)).login(
          identity: identity,
          password: password,
          verificationCode: verificationCode,
          googleAuthCode: googleAuthCode,
        ),
      );
    } on AppFailure catch (error) {
      if (error.kind != FailureKind.cancelled) _changed(AppPhase.login, error);
    }
  }

  Future<void> register({
    required AuthIdentity identity,
    required String password,
    required String nickname,
    String verifyCode = '',
    String invitationCode = '',
  }) async {
    try {
      await session.signIn(
        (generation) async => (await auth(generation: generation)).register(
          identity: identity,
          password: password,
          nickname: nickname,
          verifyCode: verifyCode,
          invitationCode: invitationCode,
        ),
      );
    } on AppFailure catch (error) {
      if (error.kind != FailureKind.cancelled) _changed(AppPhase.login, error);
    }
  }

  void chooseRoute(int index) {
    if (phase != AppPhase.login || index < 0 || index >= lines.length) return;
    lineIndex = index;
    failure = null;
    notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    unawaited(_subscription.cancel());
    unawaited(session.dispose().catchError((Object _) {}));
    super.dispose();
  }
}
