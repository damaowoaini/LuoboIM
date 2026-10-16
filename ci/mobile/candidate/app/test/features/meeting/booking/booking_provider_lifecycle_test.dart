import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/core/tenant/session_coordinator.dart';
import 'package:imapp/core/tenant/session_credentials.dart';
import 'package:imapp/features/meeting/booking/booking_providers.dart';
import 'package:imapp/features/meeting/booking/presentation/booking_controller.dart';

final class MemoryCredentials implements CredentialStore {
  SessionCredentials? value;
  @override Future<SessionCredentials?> read() async => value;
  @override Future<void> clear() async { value = null; }
  @override Future<void> write(SessionCredentials credentials) async { value = credentials; }
}
final class NoNative implements ImGateway {
  @override Future<void> connect(SessionCredentials credentials, int generation) async {}
  @override Future<void> disconnect() async {}
}
final class NetworkForbidden implements HttpClientAdapter {
  @override void close({bool force = false}) {}
  @override Future<ResponseBody> fetch(RequestOptions options, Stream<Uint8List>? requestStream,
      Future<void>? cancelFuture) async => throw StateError('All physical network is forbidden');
}

void main() {
  late Directory directory;
  late SessionCoordinator session;
  late TenantApi api;
  late SessionCredentials credentials;
  late List<String> requests;
  ProviderContainer? container;
  setUp(() async {
    sqfliteFfiInit(); directory = await Directory.systemTemp.createTemp('luobo_booking_provider_');
    session = SessionCoordinator(store: MemoryCredentials(), im: NoNative());
    credentials = SessionCredentials(userId: 'owner', tenantId: 'tenant', tenantName: 'fixture',
      domains: ['im.fixture.test'], issuer: 'https://route.fixture.test/', chatToken: 'synthetic-chat', imToken: 'synthetic-im');
    await session.signIn((_) async => credentials); requests = [];
    final dio = Dio()..httpClientAdapter = NetworkForbidden();
    dio.interceptors.add(InterceptorsWrapper(onRequest: (options, handler) {
      requests.add(options.uri.path);
      handler.resolve(Response(requestOptions: options, data: {'errCode': 0,
        'data': {'bookings': <Object?>[], 'hasMore': false}}));
    }));
    api = TenantApi(SessionAccess(session), dio: dio);
  });
  tearDown(() async {
    container?.dispose(); container = null; api.close(); await session.dispose();
    await directory.delete(recursive: true);
  });
  ProviderContainer create({Future<String> Function(String)? path, List<BookingReleaseCapability> catalog = const []}) {
    return container = ProviderContainer(overrides: [
      tenantApiProvider.overrideWithValue(api), bookingDatabaseFactoryProvider.overrideWithValue(databaseFactoryFfi),
      bookingPrivatePathProvider.overrideWithValue(path ?? (_) async => '${directory.path}/journal.sqlite'),
      bookingReleaseCatalogProvider.overrideWithValue(catalog),
    ]);
  }
  test('empty release catalog returns unsupported with real SQLite and no authenticated network dispatch', () async {
    final c = create(); final subscription = c.listen(bookingControllerProvider, (_, _) {});
    final controller = await c.read(bookingControllerProvider.future);
    expect(controller.availability, BookingAvailability.unsupported); expect(requests, isEmpty);
    expect(controller.canCreate, isFalse); subscription.close();
  });
  test('provider disposed during real delayed OS-path read never opens the file or publishes a controller', () async {
    final entered = Completer<void>(), path = Completer<String>();
    final c = create(path: (key) { expect(key, credentials.accountKey); entered.complete(); return path.future; });
    c.listen(bookingControllerProvider, (_, _) {}); final opening = c.read(bookingControllerProvider.future);
    final rejected = expectLater(opening, throwsA(anything)); await entered.future;
    c.dispose(); container = null; path.complete('${directory.path}/journal.sqlite'); await rejected;
    expect(await File('${directory.path}/journal.sqlite').exists(), isFalse); expect(requests, isEmpty);
  });
  test('correct exact release opens readiness via a normal read, while a foreign issuer cannot activate', () async {
    final grant = BookingReleaseCapability(issuer: credentials.issuer, tenantId: credentials.tenantId,
      imBase: api.im.base.toString());
    final c = create(catalog: [grant]); final subscription = c.listen(bookingControllerProvider, (_, _) {});
    final controller = await c.read(bookingControllerProvider.future);
    expect(controller.availability, BookingAvailability.ready); expect(requests, ['/api/signal/meeting_booking/list']);
    expect(BookingReleaseCapability(issuer: 'https://other.fixture.test/', tenantId: credentials.tenantId,
      imBase: api.im.base.toString()).matches(api), isFalse); subscription.close();
  });
}
