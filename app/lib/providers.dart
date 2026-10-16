import 'dart:async';
import 'dart:io';
import 'dart:math';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/legacy.dart';
import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:path_provider/path_provider.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'core/app_runtime.dart';
import 'core/access/app_access.dart';
import 'core/access/access_store.dart';
import 'core/access/native_access.dart';
import 'core/config/app_config.dart';
import 'core/config/route_config_loader.dart';
import 'core/error/app_failure.dart';
import 'core/im/open_im_gateway.dart';
import 'core/network/api_client.dart';
import 'core/network/current_session_revoker.dart';
import 'core/storage/consent_store.dart';
import 'core/storage/secure_credential_store.dart';
import 'core/tenant/session_coordinator.dart';
import 'features/auth/data/auth_repository.dart';
import 'features/chat/data/chat_reading_preferences.dart';

final chatReadingStoreProvider = Provider<ChatReadingPreferencesStore>((ref) {
  final store = ChatReadingPreferencesStore(); // Preferences platform remains lazy.
  ref.onDispose(store.dispose);
  return store;
});

final appConfigProvider = Provider<AppConfig>(
  (ref) => AppConfig.fromEnvironment(),
);

// Platform boundaries remain lazy so reading configuration or showing privacy
// never reads secure credentials, creates an installation ID, or starts IM.
final credentialStoreProvider = Provider<CredentialStore>(
  (_) => SecureCredentialStore(const FlutterSecureStorage()),
);
final consentStoreProvider = Provider<ConsentStore>(
  (_) => PreferencesConsentStore(SharedPreferencesAsync()),
);
final appAccessStoreProvider = Provider<AccessStore>(
  (_) => SecureAccessStore(),
);
final appAccessNativeProvider = Provider<NativeAccess>(
  (_) => const MethodChannelNativeAccess(),
);
final appAccessProvider = ChangeNotifierProvider<AppAccessCoordinator>((ref) {
  final access = AppAccessCoordinator(
    store: ref.watch(appAccessStoreProvider),
    native: ref.watch(appAccessNativeProvider),
  );
  // Constructor and attachment are inert. Consent remains the first I/O gate.
  return access;
});
final imManagerFactoryProvider = Provider<IMManager Function()>(
  (_) =>
      () => OpenIM.iMManager,
);
final imSupportDirectoryProvider = Provider<Future<Directory> Function()>(
  (_) => getApplicationSupportDirectory,
);
final imPlatformProvider = Provider<int?>((_) => null);
final authDioFactoryProvider = Provider<Dio Function()>(
  (_) =>
      () => Dio(BaseOptions(connectTimeout: const Duration(seconds: 15))),
);
final deviceIdentityLoaderProvider =
    Provider<Future<DeviceIdentity> Function()>(
      (_) => () async {
        try {
          const storage = FlutterSecureStorage();
          var id = await storage.read(key: 'luobo.installation.v1');
          if (id == null) {
            final random = Random.secure();
            id = List.generate(
              24,
              (_) => random.nextInt(256).toRadixString(16).padLeft(2, '0'),
            ).join();
            await storage.write(key: 'luobo.installation.v1', value: id);
          }
          return DeviceIdentity(
            id: id,
            platform: Platform.isIOS ? 1 : 2,
            model: Platform.isIOS ? 'iOS' : 'Android',
          );
        } catch (_) {
          throw const AppFailure(FailureKind.storage);
        }
      },
    );

/// Callbacks use this lifetime-bound reference instead of reading the runtime
/// Provider from the gateway Provider, which creates a Riverpod dependency loop.
final class _ImRuntimeBinding {
  AppRuntime? runtime;
  void detach(AppRuntime owner) {
    if (identical(runtime, owner)) runtime = null;
  }
}

final _imRuntimeBindingProvider = Provider((_) => _ImRuntimeBinding());

final Provider<OpenImGateway> imGatewayProvider = Provider<OpenImGateway>((
  ref,
) {
  final binding = ref.watch(_imRuntimeBindingProvider);
  final gateway = OpenImGateway(
    managerFactory: ref.watch(imManagerFactoryProvider),
    supportDirectory: ref.watch(imSupportDirectoryProvider),
    platformId: ref.watch(imPlatformProvider),
    hasPrivacyConsent: () => binding.runtime?.hasPrivacyConsent ?? false,
    interactiveRevision: () =>
        binding.runtime?.session.interactiveRevision ?? 0,
    hasInteractiveAccess: () =>
        binding.runtime?.session.interactiveAllowed ?? false,
    onSessionExpired: (generation) {
      final session = binding.runtime?.session;
      if (session != null && session.isCurrent(generation)) {
        unawaited(session.signOut(revoke: false).catchError((Object _) {}));
      }
    },
  );
  ref.onDispose(() {
    unawaited(gateway.dispose());
  });
  return gateway;
});

final ChangeNotifierProvider<AppRuntime> appRuntimeProvider =
    ChangeNotifierProvider<AppRuntime>((ref) {
      final config = ref.watch(appConfigProvider);
      final binding = ref.watch(_imRuntimeBindingProvider);
      final clients = <(Uri, int?), ApiClient>{};
      final dio = ref.watch(authDioFactoryProvider)();
      late SessionCoordinator session;
      final gateway = ref.watch(imGatewayProvider);
      final access = ref.read(appAccessProvider);
      session = SessionCoordinator(
        store: ref.watch(credentialStoreProvider),
        im: gateway,
        revoker: CurrentSessionRevoker(),
        beforeRestore: access.authorizeRestore,
        authenticated: access.authenticated,
        reconnected: access.reconnected,
      );
      access.attach(session);
      final invalidation = session.invalidations.listen((_) {
        for (final client in clients.values) {
          client.close();
        }
        clients.clear();
      });
      Future<DeviceIdentity>? device;
      final loadIdentity = ref.watch(deviceIdentityLoaderProvider);
      Future<DeviceIdentity> identity() => device ??= loadIdentity();
      final runtime = AppRuntime(
        config: config,
        consent: ref.watch(consentStoreProvider),
        routes: RouteConfigLoader(config: config),
        session: session,
        auth: (route, generation) async {
          final client = clients.putIfAbsent(
            (route, generation),
            () => ApiClient(
              base: route,
              dio: dio,
              isCurrent: generation == null
                  ? null
                  : () => session.isCurrent(generation),
            ),
          );
          return AuthRepository(client, await identity());
        },
      );
      binding.runtime = runtime;
      ref.onDispose(() {
        binding.detach(runtime);
        unawaited(invalidation.cancel());
        for (final client in clients.values) {
          client.close();
        }
        dio.close(force: true);
      });
      unawaited(runtime.start());
      return runtime;
    });
