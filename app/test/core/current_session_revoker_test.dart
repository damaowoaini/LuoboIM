import 'dart:async';
import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/core/network/current_session_revoker.dart';
import 'package:imapp/core/tenant/session_coordinator.dart';
import 'package:imapp/core/tenant/session_credentials.dart';
import 'package:imapp/core/tenant/session_revoker.dart';

import '../support/http_adapter.dart';
import 'session_coordinator_test.dart' show MemoryStore, FakeIm, credentials;

class RevocationProbe implements SessionRevoker {
  final calls = <SessionCredentials>[];
  final entered = Completer<void>();
  final result = Completer<SessionRevocation>();
  @override
  Future<SessionRevocation> revoke(SessionCredentials value) {
    calls.add(value);
    entered.complete();
    return result.future;
  }
}

class SignaledStore extends MemoryStore {
  Completer<void>? clearSignal;
  @override
  Future<void> clear() async {
    await super.clear();
    clearSignal?.complete();
  }
}

void main() {
  test('only the captured chat endpoint and exact IM token pair are sent; no platform-wide logout', () async {
    final adapter = TestHttpAdapter(
      (request) async => jsonResponse(
        jsonEncode({
          'errCode': 0,
          'data': {'revoked': true, 'sessionClosed': true, 'pending': false},
        }),
      ),
    );
    final service = CurrentSessionRevoker(
      transport: () => Dio()..httpClientAdapter = adapter,
    );
    final captured = credentials('alice', line: 1);
    expect(await service.revoke(captured), SessionRevocation.confirmed);
    final request = adapter.requests.single;
    expect(
      request.uri.toString(),
      'https://two.example.com/chat/account/logout',
    );
    expect(request.data, {'imToken': 'im-test'});
    expect(request.headers['token'], 'chat-test');
    expect(request.followRedirects, isFalse);
    expect(request.data.containsKey('platform'), isFalse);
  });
  test('pending and malformed acknowledgements are never presented as completed revocations', () async {
    for (final entry in <Map<String, Object?>>[
      {'revoked': true, 'sessionClosed': false, 'pending': true},
      {'revoked': false, 'sessionClosed': true, 'pending': false},
      {'revoked': true, 'sessionClosed': true},
    ]) {
      final adapter = TestHttpAdapter(
        (_) async => jsonResponse(jsonEncode({'errCode': 0, 'data': entry})),
      );
      final service = CurrentSessionRevoker(
        transport: () => Dio()..httpClientAdapter = adapter,
      );
      expect(
        await service.revoke(credentials('alice')),
        entry['pending'] == true
            ? SessionRevocation.pending
            : SessionRevocation.unconfirmed,
      );
    }
  });
  test('a bounded timeout cancels the detached request without exposing credentials in its result', () async {
    final response = Completer<ResponseBody>();
    final adapter = TestHttpAdapter((_) => response.future);
    final service = CurrentSessionRevoker(
      transport: () => Dio()..httpClientAdapter = adapter,
      timeout: const Duration(milliseconds: 10),
    );
    expect(
      await service.revoke(credentials('alice')),
      SessionRevocation.unconfirmed,
    );
    response.complete(jsonResponse('{"errCode":0,"data":{}}'));
    await Future<void>.delayed(Duration.zero);
    expect(adapter.requests.length, 1);
  });
  test('local logout invalidates immediately, clears storage before remote acknowledgement and coalesces duplicate calls', () async {
    final store = SignaledStore(), im = FakeIm(), revoker = RevocationProbe();
    final session = SessionCoordinator(store: store, im: im, revoker: revoker);
    addTearDown(session.dispose);
    await session.signIn((_) async => credentials('alice'));
    final epoch = session.state.generation;
    final first = session.signOut(), second = session.signOut();
    expect(identical(first, second), isTrue);
    expect(session.acceptsEvent(epoch), isFalse);
    await revoker.entered.future;
    await Future<void>.delayed(Duration.zero);
    expect(store.value, isNull);
    expect(session.state.credentials, isNull);
    expect(revoker.calls.single.userId, 'alice');
    revoker.result.complete(SessionRevocation.pending);
    await first;
    expect(session.state.failure?.kind, FailureKind.logoutPending);
  });
  test(
    'a late old-tenant logout response cannot replace a newly queued session',
    () async {
      final store = SignaledStore(), im = FakeIm(), revoker = RevocationProbe();
      final session = SessionCoordinator(
        store: store,
        im: im,
        revoker: revoker,
      );
      addTearDown(session.dispose);
      await session.signIn((_) async => credentials('alice'));
      store.clearSignal = Completer<void>();
      final old = session.signOut();
      await store.clearSignal!.future;
      store.clearSignal = null;
      final next = session.signIn((_) async => credentials('bob'));
      revoker.result.complete(SessionRevocation.unconfirmed);
      await old;
      await next;
      expect(session.state.phase, SessionPhase.online);
      expect(session.state.credentials?.userId, 'bob');
      expect(store.value?.userId, 'bob');
      expect(session.state.failure, isNull);
      expect(revoker.calls.single.userId, 'alice');
    },
  );
  test(
    'forced expiry cleans locally without creating a remote user logout',
    () async {
      final store = MemoryStore(), im = FakeIm(), revoker = RevocationProbe();
      final session = SessionCoordinator(
        store: store,
        im: im,
        revoker: revoker,
      );
      addTearDown(session.dispose);
      await session.signIn((_) async => credentials('alice'));
      await session.signOut(revoke: false);
      expect(revoker.calls, isEmpty);
      expect(store.value, isNull);
      expect(session.state.failure, isNull);
    },
  );
}
