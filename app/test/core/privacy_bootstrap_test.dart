import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/app_runtime.dart';
import 'package:imapp/core/config/app_config.dart';
import 'package:imapp/core/config/route_config_loader.dart';
import 'package:imapp/core/storage/consent_store.dart';
import 'package:imapp/core/tenant/session_coordinator.dart';
import 'package:imapp/core/tenant/session_credentials.dart';

class ConsentMemory implements ConsentStore {
  bool granted = false;
  bool failWrite = false;
  @override
  Future<bool> read() async => granted;
  @override
  Future<void> accept() async {
    if (failWrite) throw StateError('disk unavailable');
    granted = true;
  }
}

class StoreProbe implements CredentialStore {
  int reads = 0;
  SessionCredentials? value;
  @override
  Future<SessionCredentials?> read() async {
    reads++;
    return value;
  }

  @override
  Future<void> write(SessionCredentials value) async {}
  @override
  Future<void> clear() async {}
}

class ImProbe implements ImGateway {
  int connects = 0;
  @override
  Future<void> connect(SessionCredentials credentials, int generation) async {
    connects++;
  }

  @override
  Future<void> disconnect() async {}
}

void main() {
  test('saved tenant session reconnects even when bootstrap sources are unavailable', () async {
    final consent = ConsentMemory()..granted = true;
    final store = StoreProbe()
      ..value = SessionCredentials(
        userId: 'u',
        tenantId: 't',
        tenantName: 'tenant',
        domains: ['tenant.test'],
        chatToken: 'chat-fixture',
        imToken: 'im-fixture',
      );
    final im = ImProbe();
    var requests = 0;
    final config = AppConfig(
      routeSources: [Uri.parse('https://unavailable.test/')],
    );
    final session = SessionCoordinator(store: store, im: im);
    final runtime = AppRuntime(
      config: config,
      consent: consent,
      session: session,
      routes: RouteConfigLoader(
        config: config,
        fetch: (_, _) async {
          requests++;
          throw StateError('offline');
        },
      ),
      auth: (_, _) async => throw StateError('no auth'),
    );
    await runtime.start();
    expect(runtime.phase, AppPhase.online);
    expect(im.connects, 1);
    expect(requests, 0);
    runtime.dispose();
    await session.dispose();
  });
  test(
    'no routes, credentials or SDK are touched before explicit privacy consent',
    () async {
      var requests = 0;
      final consent = ConsentMemory();
      final store = StoreProbe();
      final im = ImProbe();
      final config = AppConfig(
        privacyUrl: 'https://policy.test/privacy',
        termsUrl: 'https://policy.test/terms',
        routeSources: [Uri.parse('https://config.test/routes.json')],
      );
      final session = SessionCoordinator(store: store, im: im);
      final runtime = AppRuntime(
        config: config,
        consent: consent,
        session: session,
        routes: RouteConfigLoader(
          config: config,
          fetch: (_, _) async {
            requests++;
            return ['https://route.test'];
          },
        ),
        auth: (_, _) async =>
            throw StateError('auth must not run during bootstrap'),
      );
      await runtime.start();
      expect(runtime.phase, AppPhase.consent);
      expect(requests, 0);
      expect(store.reads, 0);
      expect(im.connects, 0);
      await runtime.acceptConsent();
      expect(requests, 1);
      expect(store.reads, 1);
      expect(im.connects, 0);
      expect(runtime.phase, AppPhase.login);
      runtime.dispose();
      await session.dispose();
    },
  );

  test('failed consent persistence leaves privacy gating closed', () async {
    final consent = ConsentMemory()..failWrite = true;
    final config = AppConfig(
      privacyUrl: 'https://policy.test/privacy',
      termsUrl: 'https://policy.test/terms',
      fallbackRoutes: [Uri.parse('https://route.test')],
    );
    final store = StoreProbe();
    final session = SessionCoordinator(store: store, im: ImProbe());
    final runtime = AppRuntime(
      config: config,
      consent: consent,
      session: session,
      routes: RouteConfigLoader(config: config),
      auth: (_, _) async => throw StateError('no auth'),
    );
    await runtime.acceptConsent();
    expect(runtime.hasPrivacyConsent, isFalse);
    expect(store.reads, 0);
    expect(runtime.phase, AppPhase.consent);
    runtime.dispose();
    await session.dispose();
  });
}
