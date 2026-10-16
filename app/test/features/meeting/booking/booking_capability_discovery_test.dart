import 'dart:async';
import 'dart:io';

import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';
import 'package:imapp/core/network/tenant_api.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/core/tenant/session_coordinator.dart';
import 'package:imapp/core/tenant/session_credentials.dart';
import 'package:imapp/features/meeting/booking/booking_providers.dart';
import 'package:imapp/features/meeting/booking/data/booking_contract.dart';
import 'package:imapp/features/meeting/booking/presentation/booking_controller.dart';

import 'booking_provider_lifecycle_test.dart' show MemoryCredentials, NoNative, NetworkForbidden;
import 'booking_test_harness.dart' show bookingOne, intentOne;

final class _DiscoveryFixture {
  Directory? directory;
  SessionCoordinator? session;
  TenantApi? api;
  ProviderContainer? container;
  BookingController? controller;
  final requests = <RequestOptions>[];
  Map<String, Object?>? listReply;
  bool unknownCreate = false;
  Completer<Object?>? listGate;
  Completer<void>? listEntered, listReturned;

  Map<String, Object?> capability() => {
    'contract': 'luobo-owner-booking', 'contractVersion': 1,
    'ownerUserID': 'owner', 'replaySafe': true, 'mediaReady': false,
    'bookings': <Object?>[], 'hasMore': false,
  };
  Map<String, Object?> payload() => {
    'meetingName': '合成预约',
    'startTime': DateTime.utc(2030, 1, 1, 0, 10).millisecondsSinceEpoch ~/ 1000,
    'meetingDuration': 3600, 'inviteeUserIDList': <String>[], 'ex': '',
  };
  BookingIntent intent() => BookingIntent(id: intentOne,
    operation: BookingOperation.create, frozenBody: {'clientRequestID': intentOne, ...payload()});
  Map<String, Object?> applied() => {
    'clientRequestID': intentOne, 'found': true, 'applied': true,
    'definitiveNotApplied': false, 'replaySafe': true, 'action': 'create',
    'appliedVersion': 1, 'booking': {
      'bookingID': bookingOne, 'hostUserID': 'owner', 'state': 'scheduled',
      'version': 1, 'mediaReady': false, 'payload': payload(),
    },
  };
  Future<BookingController> open() async {
    sqfliteFfiInit();
    final owned = await Directory.systemTemp.createTemp('luobo_booking_discovery_');
    directory = owned;
    final owner = SessionCoordinator(store: MemoryCredentials(), im: NoNative());
    session = owner;
    final credentials = SessionCredentials(userId: 'owner', tenantId: 'tenant',
      tenantName: 'fixture', domains: ['im.fixture.test'],
      issuer: 'https://route.fixture.test/', chatToken: 'synthetic-chat', imToken: 'synthetic-im');
    await owner.signIn((_) async => credentials);
    final dio = Dio()..httpClientAdapter = NetworkForbidden();
    dio.interceptors.add(InterceptorsWrapper(onRequest: (options, handler) async {
      requests.add(options);
      final action = options.uri.path.split('/').last;
      Object? response;
      if (action == 'list') {
        final gate = listGate;
        if (gate == null) {
          response = listReply ?? capability();
        } else {
          listEntered!.complete();
          try {
            response = await gate.future;
          } finally {
            listReturned!.complete();
          }
        }
      } else if (action == 'create') {
        if (unknownCreate) {
          handler.resolve(Response(requestOptions: options, data: {'errCode': 500}));
          return;
        }
        response = applied();
      } else if (action == 'receipt') {
        response = {'clientRequestID': intentOne, 'found': false, 'applied': false,
          'definitiveNotApplied': false, 'replaySafe': true};
      } else {
        handler.resolve(Response(requestOptions: options, data: {'errCode': 1002}));
        return;
      }
      handler.resolve(Response(requestOptions: options, data: {'errCode': 0, 'data': response}));
    }));
    final transport = TenantApi(SessionAccess(owner), dio: dio);
    api = transport;
    final scope = ProviderContainer(overrides: [
      tenantApiProvider.overrideWithValue(transport),
      bookingDatabaseFactoryProvider.overrideWithValue(databaseFactoryFfi),
      bookingPrivatePathProvider.overrideWithValue((_) async => '${owned.path}/journal.sqlite'),
      // Use the actual production null/discovery default, not a static release override.
    ]);
    container = scope;
    scope.listen(bookingControllerProvider, (_, _) {});
    final ready = await scope.read(bookingControllerProvider.future);
    controller = ready;
    return ready;
  }
  int calls(String action) => requests.where((r) => r.uri.path.endsWith('/$action')).length;
  Future<void> close() async {
    final gate = listGate;
    if (gate != null && !gate.isCompleted) gate.complete(capability());
    final returned = listReturned;
    if (returned != null) await returned.future.timeout(const Duration(seconds: 2));
    // Join the actual journal close before provider disposal/directory removal.
    // Provider's own idempotent close then has no pending database operation.
    await controller?.repository.journal.close();
    container?.dispose();
    api?.close();
    await session?.dispose();
    final owned = directory;
    if (owned != null) await owned.delete(recursive: true);
  }
}

void main() {
  test('production booking discovery reads the current IM contract and saves only an explicit durable intent', () async {
    final f = _DiscoveryFixture();
    addTearDown(f.close);
    final controller = await f.open();
    expect(controller.availability, BookingAvailability.ready);
    expect(controller.canCreate, isTrue);
    expect(f.calls('list'), 1);
    expect(f.calls('create'), 0);
    expect(f.requests.single.uri.toString(), 'https://im.fixture.test/api/signal/meeting_booking/list');
    expect(f.requests.single.headers['token'], 'synthetic-im');
    final receipt = await controller.submit(f.intent());
    expect(receipt?.applied, isTrue);
    expect(f.calls('create'), 1);
    expect(f.requests.last.data, f.intent().payload);
    expect((await controller.repository.journal.read(intentOne))?.receipt?.applied, isTrue);
    expect((await controller.repository.journal.read(intentOne))?.intent, isNull);
    expect(f.requests.every((r) => r.uri.path.contains('/meeting_booking/')), isTrue);
  });

  for (final fault in ['missing', 'owner', 'version', 'media', 'token']) {
    test('booking discovery cannot activate writes from a $fault capability contract', () async {
      final f = _DiscoveryFixture();
      addTearDown(f.close);
      final reply = f.capability();
      switch (fault) {
        case 'missing': reply.remove('contract'); break;
        case 'owner': reply['ownerUserID'] = 'another-owner'; break;
        case 'version': reply['contractVersion'] = 2; break;
        case 'media': reply['mediaReady'] = true; break;
        case 'token': reply['joinToken'] = 'synthetic-unexpected-media-material'; break;
      }
      f.listReply = reply;
      final controller = await f.open();
      expect(controller.availability, BookingAvailability.unavailable);
      expect(controller.canWrite, isFalse);
      expect(await controller.submit(f.intent()), isNull);
      expect(f.calls('list'), 1);
      expect(f.calls('create'), 0);
      expect(controller.unknownIDs, isEmpty);
      expect(f.requests.every((r) => r.uri.path.endsWith('/list')), isTrue);
    });
  }

  test('failed booking discovery preserves an unknown intent and blocks retries until a fresh valid read', () async {
    final f = _DiscoveryFixture();
    addTearDown(f.close);
    final controller = await f.open();
    f.unknownCreate = true;
    expect(await controller.submit(f.intent()), isNull);
    expect(controller.unknownIDs, [intentOne]);
    f.listReply = {...f.capability(), 'contractVersion': 2};
    await controller.refresh();
    expect(controller.canWrite, isFalse);
    expect(await controller.recover(intentOne, retry: true), isNull);
    expect(await controller.recover(intentOne, resolve: true), isNull);
    expect(f.calls('create'), 1);
    expect(f.calls('resolve'), 0);
    expect((await controller.recover(intentOne))?.found, isFalse);
    expect(controller.unknownIDs, [intentOne]);
    expect((await controller.repository.journal.read(intentOne))?.intent?.frozenJson, f.intent().frozenJson);
    f.listReply = f.capability();
    await controller.refresh();
    f.unknownCreate = false;
    expect((await controller.recover(intentOne, retry: true))?.applied, isTrue);
    expect(f.calls('create'), 2);
    expect(f.requests.where((r) => r.uri.path.endsWith('/create')).every((r) =>
      bookingCanonical(r.data) == f.intent().frozenJson), isTrue);
    expect(controller.unknownIDs, isEmpty);
  });

  test('a readiness refresh suspends booking writes and its late old owner reply cannot reactivate them', () async {
    final f = _DiscoveryFixture();
    addTearDown(f.close);
    final controller = await f.open();
    f.listGate = Completer<Object?>();
    f.listEntered = Completer<void>();
    f.listReturned = Completer<void>();
    final refreshing = controller.refresh();
    await f.listEntered!.future.timeout(const Duration(seconds: 2));
    expect(controller.availability, BookingAvailability.checking);
    expect(controller.canWrite, isFalse);
    expect(await controller.submit(f.intent()), isNull);
    await f.session!.signOut(revoke: false);
    f.listGate!.complete(f.capability());
    await refreshing;
    await f.listReturned!.future;
    expect(controller.usable, isFalse);
    expect(controller.canWrite, isFalse);
    expect(f.calls('create'), 0);
  });
}
