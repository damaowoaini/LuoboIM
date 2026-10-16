import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/core/tenant/session_coordinator.dart';
import 'package:imapp/core/tenant/session_credentials.dart';

SessionCredentials credentials(String user, {int line = 0}) =>
    SessionCredentials(
      userId: user,
      tenantId: 'tenant',
      tenantName: '测试企业',
      domains: ['one.example.com', 'two.example.com'],
      chatToken: 'chat-test',
      imToken: 'im-test',
      lineIndex: line,
    );

class MemoryStore implements CredentialStore {
  SessionCredentials? value;
  bool failWrite = false;
  @override
  Future<SessionCredentials?> read() async => value;
  @override
  Future<void> clear() async {
    value = null;
  }

  @override
  Future<void> write(SessionCredentials credentials) async {
    if (failWrite) throw const AppFailure(FailureKind.storage);
    value = credentials;
  }
}

class FakeIm implements ImGateway {
  final calls = <String>[];
  int? failLine;
  @override
  Future<void> connect(SessionCredentials credentials, int generation) async {
    calls.add('connect:${credentials.userId}:${credentials.lineIndex}');
    if (credentials.lineIndex == failLine) {
      throw const AppFailure(FailureKind.network);
    }
  }

  @override
  Future<void> disconnect() async {
    calls.add('disconnect');
  }
}

void main() {
  late MemoryStore store;
  late FakeIm im;
  late SessionCoordinator session;
  setUp(() {
    store = MemoryStore();
    im = FakeIm();
    session = SessionCoordinator(store: store, im: im);
  });
  tearDown(() async {
    await session.dispose();
  });

  test(
    'logout invalidates pending authentication and never saves its result',
    () async {
      final response = Completer<SessionCredentials>();
      final started = Completer<void>();
      final login = session.signIn((_) {
        started.complete();
        return response.future;
      });
      final expected = expectLater(
        login,
        throwsA(
          isA<AppFailure>().having(
            (e) => e.kind,
            'kind',
            FailureKind.cancelled,
          ),
        ),
      );
      await started.future;
      final epoch = session.state.generation;
      final logout = session.signOut();
      expect(session.acceptsEvent(epoch), isFalse);
      expect(session.state.credentials, isNull);
      response.complete(credentials('old-user'));
      await expected;
      await logout;
      expect(store.value, isNull);
      expect(im.calls.where((s) => s.startsWith('connect:')), isEmpty);
    },
  );

  test(
    'a later login supersedes the old one and native operations stay ordered',
    () async {
      final firstResponse = Completer<SessionCredentials>();
      final started = Completer<void>();
      final first = session.signIn((_) {
        started.complete();
        return firstResponse.future;
      });
      final rejected = expectLater(first, throwsA(isA<AppFailure>()));
      await started.future;
      final second = session.signIn((_) async => credentials('new-user'));
      firstResponse.complete(credentials('old-user'));
      await rejected;
      await second;
      expect(session.state.credentials?.userId, 'new-user');
      expect(store.value?.userId, 'new-user');
      expect(im.calls.where((s) => s.startsWith('connect:')).toList(), [
        'connect:new-user:0',
      ]);
    },
  );

  test(
    'failed line change reconnects the original line and retains credentials',
    () async {
      await session.signIn((_) async => credentials('user'));
      final oldEpoch = session.state.generation;
      im.failLine = 1;
      await expectLater(session.switchLine(1), throwsA(isA<AppFailure>()));
      expect(session.state.phase, SessionPhase.online);
      expect(session.state.credentials?.lineIndex, 0);
      expect(store.value?.lineIndex, 0);
      expect(session.acceptsEvent(oldEpoch), isFalse);
      expect(im.calls.sublist(im.calls.length - 4), [
        'disconnect',
        'connect:user:1',
        'disconnect',
        'connect:user:0',
      ]);
    },
  );

  test('failed secure save cannot leave an authenticated session', () async {
    store.failWrite = true;
    await expectLater(
      session.signIn((_) async => credentials('user')),
      throwsA(isA<AppFailure>()),
    );
    expect(session.state.phase, SessionPhase.failed);
    expect(session.state.credentials, isNull);
    expect(store.value, isNull);
    expect(im.calls.last, 'disconnect');
  });

  test(
    'tenant credentials validate domains and account cache is tenant scoped',
    () {
      expect(
        () => SessionCredentials(
          userId: 'u',
          tenantId: 't',
          tenantName: 't',
          domains: ['https://user:pass@evil.test'],
          chatToken: 'x',
          imToken: 'y',
        ),
        throwsA(isA<AppFailure>()),
      );
      final first = credentials('u');
      final second = SessionCredentials(
        userId: 'u',
        tenantId: 'other',
        tenantName: 't',
        domains: ['one.example.com'],
        chatToken: 'x',
        imToken: 'y',
      );
      expect(first.accountKey, isNot(second.accountKey));
      expect(first.toString(), isNot(contains('chat-test')));
    },
  );
}
