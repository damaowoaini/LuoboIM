// Explicit local QA entry point. Production lib/main.dart has no fixture CA or
// proxy, and this entry refuses profile/release builds.
import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:flutter/foundation.dart';
import 'package:imapp/main.dart' as application;

import '../../packages/flutter_openim_sdk/tool/fixture_native_endpoint_messenger.dart';

final class FixtureHttpOverrides extends HttpOverrides {
  FixtureHttpOverrides(this.context, this.proxy);
  final SecurityContext context;
  final String proxy;
  static const hosts = {
    'luobo.fixture.test',
    'tenant-b.luobo.fixture.test',
    'admin.luobo.fixture.test',
    'objects.luobo.fixture.test',
    'media.luobo.fixture.test',
  };
  @override
  HttpClient createHttpClient(SecurityContext? ignored) =>
      super.createHttpClient(context)
        ..findProxy = (uri) {
          if (!hosts.contains(uri.host) || uri.scheme != 'https') {
            throw const SocketException(
              'QA transport only permits the explicit HTTPS fixture hosts',
            );
          }
          return 'PROXY $proxy';
        };
}

void main() {
  if (!kDebugMode) throw StateError('Local fixture entry is debug-only');
  const encoded = String.fromEnvironment('LUOBO_FIXTURE_CA_BASE64');
  const expected = String.fromEnvironment('LUOBO_FIXTURE_CA_SHA256');
  final bytes = base64Decode(encoded);
  if (expected.isEmpty || sha256.convert(bytes).toString() != expected) {
    throw StateError('Explicit fixture CA fingerprint is required');
  }
  final context = SecurityContext(withTrustedRoots: true)
    ..setTrustedCertificatesBytes(bytes);
  final proxy = Platform.isAndroid ? '10.0.2.2:38081' : '127.0.0.1:38081';
  HttpOverrides.global = FixtureHttpOverrides(context, proxy);
  // The legacy native HTTP client ignores environment proxies. Keep identity
  // and CA validation unchanged; only this QA binding adapts its local API/WS.
  if (Platform.isAndroid) FixtureNativeEndpointBinding();
  application.main();
}
