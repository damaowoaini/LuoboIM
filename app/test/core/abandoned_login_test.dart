import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/core/tenant/session_coordinator.dart';
import 'package:imapp/core/tenant/session_credentials.dart';
import 'package:imapp/core/tenant/session_revoker.dart';

import 'session_coordinator_test.dart' show MemoryStore, FakeIm;

SessionCredentials issued(String user) => SessionCredentials(
  userId: user,
  tenantId: 'fixture',
  tenantName: 'fixture',
  domains: ['im.example.com'],
  chatToken: 'chat-$user',
  imToken: 'im-$user',
);

class _Revoker implements SessionRevoker {
  final captured = <SessionCredentials>[];
  Future<SessionRevocation> Function()? result;
  @override
  Future<SessionRevocation> revoke(SessionCredentials credentials) async {
    captured.add(credentials);
    return await result?.call() ?? SessionRevocation.confirmed;
  }
}

void main() {
  test(
    'known-issued pair is exactly revoked when native connection fails',
    () async {
      final im = FakeIm()..failLine = 0;
      final revoker = _Revoker();
      final store = MemoryStore();
      final session = SessionCoordinator(
        store: store,
        im: im,
        revoker: revoker,
      );
      addTearDown(session.dispose);
      final pair = issued('alice');
      await expectLater(
        session.signIn((_) async => pair),
        throwsA(isA<AppFailure>()),
      );
      expect(revoker.captured, [same(pair)]);
      expect(store.value, isNull);
      expect(session.state.credentials, isNull);
      expect(session.state.failure?.kind, FailureKind.network);
    },
  );

  test('secure-write failure cleans local SDK before waiting for exact remote revocation', () async {
    final im = FakeIm();
    final store = MemoryStore()..failWrite = true;
    final remote = Completer<SessionRevocation>();
    final revoker = _Revoker()..result = () => remote.future;
    final session = SessionCoordinator(store: store, im: im, revoker: revoker);
    addTearDown(session.dispose);
    final pending = session.signIn((_) async => issued('alice'));
    final rejected = expectLater(
      pending,
      throwsA(
        isA<AppFailure>().having((e) => e.kind, 'kind', FailureKind.storage),
      ),
    );
    for (var i = 0; i < 20 && revoker.captured.isEmpty; i++) {
      await Future<void>.delayed(Duration.zero);
    }
    expect(revoker.captured.length, 1);
    expect(im.calls.last, 'disconnect');
    expect(store.value, isNull);
    remote.complete(SessionRevocation.confirmed);
    await rejected;
  });

  test('superseded authentication revokes its frozen old pair and preserves newer login', () async {
    final sessionStore = MemoryStore();
    final revoker = _Revoker();
    final session = SessionCoordinator(
      store: sessionStore,
      im: FakeIm(),
      revoker: revoker,
    );
    addTearDown(session.dispose);
    final response = Completer<SessionCredentials>(),
        entered = Completer<void>();
    final old = issued('old'), next = issued('next');
    final pending = session.signIn((_) {
      entered.complete();
      return response.future;
    });
    final cancelled = expectLater(
      pending,
      throwsA(
        isA<AppFailure>().having((e) => e.kind, 'kind', FailureKind.cancelled),
      ),
    );
    await entered.future;
    final newer = session.signIn((_) async => next);
    response.complete(old);
    await cancelled;
    await newer;
    expect(revoker.captured, [same(old)]);
    expect(sessionStore.value, same(next));
    expect(session.state.credentials, same(next));
  });

  test('pending and failed revocation retain original cause and honest cleanup warning', () async {
    for (final outcome in [
      SessionRevocation.pending,
      SessionRevocation.unconfirmed,
    ]) {
      final revoker = _Revoker()..result = () async => outcome;
      final session = SessionCoordinator(
        store: MemoryStore(),
        im: FakeIm()..failLine = 0,
        revoker: revoker,
      );
      await expectLater(
        session.signIn((_) async => issued('alice')),
        throwsA(isA<AppFailure>()),
      );
      expect(session.state.failure?.kind, FailureKind.network);
      expect(
        session.state.failure?.cleanupWarning,
        outcome == SessionRevocation.pending
            ? SessionCleanupWarning.pending
            : SessionCleanupWarning.unconfirmed,
      );
      expect(session.state.failure?.message, contains('本次登录会话'));
      await session.dispose();
    }
  });

  test('authentication without an issued pair and saved-session restore do not revoke an unrelated session', () async {
    final revoker = _Revoker();
    final store = MemoryStore();
    final im = FakeIm();
    final session = SessionCoordinator(store: store, im: im, revoker: revoker);
    addTearDown(session.dispose);
    await expectLater(
      session.signIn((_) async => throw const AppFailure(FailureKind.network)),
      throwsA(isA<AppFailure>()),
    );
    store.value = issued('restored');
    im.failLine = 0;
    await expectLater(session.restore(), throwsA(isA<AppFailure>()));
    expect(revoker.captured, isEmpty);
  });
}
