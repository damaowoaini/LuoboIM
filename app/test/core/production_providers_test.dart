import 'dart:convert';
import 'dart:io';

import 'package:dio/dio.dart';
import 'package:flutter/services.dart';
import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/app_runtime.dart';
import 'package:imapp/core/config/app_config.dart';
import 'package:imapp/core/tenant/session_coordinator.dart';
import 'package:imapp/core/tenant/session_credentials.dart';
import 'package:imapp/features/auth/data/auth_repository.dart';
import 'package:imapp/providers.dart';

import 'im/sdk_harness.dart';
import 'privacy_bootstrap_test.dart' show ConsentMemory;
import '../support/http_adapter.dart';
import 'access/app_access_test.dart' show AccessMemory, NativeProbe;

class _Store implements CredentialStore {
  SessionCredentials? value;
  int reads = 0;
  @override
  Future<SessionCredentials?> read() async {
    reads++;
    return value;
  }

  @override
  Future<void> write(SessionCredentials credentials) async {
    value = credentials;
  }

  @override
  Future<void> clear() async {
    value = null;
  }
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late ProviderContainer container;
  late Directory directory;
  late MethodChannel channel;
  late SdkHarness codec;
  late ConsentMemory consent;
  late _Store store;
  late TestHttpAdapter http;
  late AccessMemory localAccess;
  late NativeProbe nativeAccess;
  var deviceReads = 0, serial = 0;
  setUp(() async {
    directory = await Directory.systemTemp.createTemp('luobo-real-providers-');
    channel = MethodChannel('real_providers_${++serial}');
    codec = SdkHarness();
    consent = ConsentMemory();
    store = _Store();
    deviceReads = 0;
    localAccess = AccessMemory();
    nativeAccess = NativeProbe();
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (call) async {
          codec.calls.add(call);
          return codec.respond(call);
        });
    http = TestHttpAdapter(
      (_) async => jsonResponse(
        jsonEncode({
          'errCode': 0,
          'data': {
            'userID': 'alice',
            'chatToken': 'fixture-chat',
            'imToken': 'fixture-im',
            'serverInfo': {
              'serverID': 't',
              'serverName': '合成服务',
              'domains': ['im.example.com'],
            },
          },
        }),
      ),
    );
    container = ProviderContainer(
      overrides: [
        appConfigProvider.overrideWithValue(
          AppConfig(
            privacyUrl: 'https://policy.test/privacy',
            termsUrl: 'https://policy.test/terms',
            fallbackRoutes: [Uri.parse('https://route.test/routing/')],
          ),
        ),
        consentStoreProvider.overrideWithValue(consent),
        credentialStoreProvider.overrideWithValue(store),
        appAccessStoreProvider.overrideWithValue(localAccess),
        appAccessNativeProvider.overrideWithValue(nativeAccess),
        imManagerFactoryProvider.overrideWithValue(() => IMManager(channel)),
        imSupportDirectoryProvider.overrideWithValue(() async => directory),
        imPlatformProvider.overrideWithValue(2),
        authDioFactoryProvider.overrideWithValue(
          () => Dio()..httpClientAdapter = http,
        ),
        deviceIdentityLoaderProvider.overrideWithValue(() async {
          deviceReads++;
          return const DeviceIdentity(
            id: 'fixture-installation',
            platform: 2,
            model: 'Android',
          );
        }),
      ],
    );
  });
  tearDown(() async {
    final runtime = container.read(appRuntimeProvider);
    final gateway = container.read(imGatewayProvider);
    container.dispose();
    await runtime.session.dispose();
    await gateway.dispose();
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, null);
    await directory.delete(recursive: true);
  });

  test('production provider graph keeps privacy gating without a runtime dependency loop', () async {
    final runtime = container.read(appRuntimeProvider);
    await runtime.start();
    final gateway = container.read(imGatewayProvider);
    expect(gateway.hasPrivacyConsent(), isFalse);
    expect(runtime.phase, AppPhase.consent);
    expect(store.reads, 0);
    expect(deviceReads, 0);
    expect(codec.calls, isEmpty);
    expect(http.requests, isEmpty);
    expect(localAccess.reads, 0);
    expect(nativeAccess.calls, 0);
    await runtime.acceptConsent();
    expect(gateway.hasPrivacyConsent(), isTrue);
    expect(runtime.phase, AppPhase.login);
  });

  test('production runtime and gateway providers complete password login through native codec', () async {
    final runtime = container.read(appRuntimeProvider);
    await runtime.start();
    await runtime.acceptConsent();
    await runtime.signIn(
      identity: AuthIdentity(account: 'alice@example.com'),
      password: 'fixture-password',
    );
    expect(runtime.phase, AppPhase.online);
    expect(runtime.failure, isNull);
    expect(store.value?.userId, 'alice');
    expect(codec.calls.any((call) => call.method == 'login'), isTrue);
    expect(http.requests.single.uri.path, '/routing/api/auth/login');
    expect(deviceReads, 1);
  });

  test('production saved-session restore and token-expiry callbacks do not read back through gateway Ref', () async {
    consent.granted = true;
    store.value = codec.credentials;
    final runtime = container.read(appRuntimeProvider);
    await runtime.start();
    expect(runtime.phase, AppPhase.online);
    expect(http.requests, isEmpty);
    expect(deviceReads, 0);
    final gateway = container.read(imGatewayProvider);
    gateway.onSessionExpired!(runtime.session.state.generation);
    for (var i = 0; i < 20 && store.value != null; i++) {
      await Future<void>.delayed(Duration.zero);
    }
    expect(store.value, isNull);
    expect(runtime.session.state.phase, SessionPhase.signedOut);
  });
}
