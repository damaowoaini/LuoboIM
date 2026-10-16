import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/access/access_store.dart';
import 'package:imapp/core/access/app_access.dart';
import 'package:imapp/core/access/native_access.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/core/tenant/session_access.dart';
import 'package:imapp/core/tenant/session_coordinator.dart';
import 'package:imapp/core/tenant/session_credentials.dart';

import '../session_coordinator_test.dart' show FakeIm, MemoryStore, credentials;

AccessPolicy policy({bool biometric = false}) => AccessPolicy(
  salt: base64Encode(List.filled(32, 1)),
  verifier: base64Encode(List.filled(32, 7)),
  biometric: biometric,
);

class AccessMemory implements AccessStore {
  final values = <String, AccessPolicy>{};
  bool failRead = false, failWrite = false;
  int reads = 0;
  Future<void> Function(String, AccessPolicy)? beforeWrite;
  @override
  Future<AccessPolicy?> read(String owner) async {
    reads++;
    if (failRead) throw StateError('synthetic storage failure');
    return values[owner];
  }

  @override
  Future<void> write(String owner, AccessPolicy policy) async {
    if (failWrite) throw StateError('synthetic storage failure');
    await beforeWrite?.call(owner, policy);
    values[owner] = policy;
  }

  @override
  Future<void> remove(String owner) async {
    values.remove(owner);
  }
}

class NativeProbe implements NativeAccess {
  int calls = 0, derives = 0, enrollments = 0;
  bool available = true;
  Completer<String>? delayedPin;
  Completer<void>? delayedBiometric;
  AccessFailure? biometricFailure;
  final salts = <String>[];
  @override
  Future<bool> biometricAvailable() async {
    calls++;
    return available;
  }

  @override
  Future<String> derivePin(String pin, String salt, int iterations) async {
    calls++;
    derives++;
    salts.add(salt);
    expect(iterations, 210000);
    if (delayedPin != null) return delayedPin!.future;
    return base64Encode(List.filled(32, pin == '123456' ? 7 : 8));
  }

  @override
  Future<void> biometric(
    String owner,
    int request, {
    bool enroll = false,
  }) async {
    calls++;
    if (enroll) enrollments++;
    if (biometricFailure != null) throw biometricFailure!;
    await delayedBiometric?.future;
  }

  @override
  Future<void> cancel() async {
    calls++;
  }

  @override
  Future<void> removeBiometric(String owner) async {
    calls++;
  }

  @override
  Future<void> privacy({required bool enabled, required bool covered}) async {
    calls++;
  }
}

class AccessHarness {
  AccessHarness() {
    access = AppAccessCoordinator(
      store: policies,
      native: native,
      now: () => clock,
    );
    session = SessionCoordinator(
      store: credentialsStore,
      im: im,
      beforeRestore: access.authorizeRestore,
      authenticated: access.authenticated,
      reconnected: access.reconnected,
    );
    access.attach(session);
  }
  final policies = AccessMemory();
  final native = NativeProbe();
  final credentialsStore = MemoryStore();
  final im = FakeIm();
  DateTime clock = DateTime.utc(2026, 10, 15);
  late final AppAccessCoordinator access;
  late final SessionCoordinator session;
  Future<void> start([String user = 'alice']) =>
      session.signIn((_) async => credentials(user));
  Future<void> dispose() async {
    access.dispose();
    await session.dispose();
  }
}

Future<void> settleAccess() async {
  for (var i = 0; i < 12; i++) {
    await Future<void>.delayed(Duration.zero);
  }
}

void main() {
  test('default access construction, attachment and unused disposal perform no storage or native I/O', () async {
    final h = AccessHarness();
    expect(h.policies.reads, 0);
    expect(h.native.calls, 0);
    await h.dispose();
    expect(h.native.calls, 0);
    expect(h.policies.reads, 0);
  });
  test('cold restore waits outside the SDK FIFO and explicit logout cancels it without connecting', () async {
    final h = AccessHarness();
    addTearDown(h.dispose);
    final saved = credentials('alice');
    h.credentialsStore.value = saved;
    h.policies.values[saved.accountKey] = policy();
    final restore = h.session.restore();
    final rejected = expectLater(restore, throwsA(isA<AppFailure>()));
    await settleAccess();
    expect(h.access.phase, AppAccessPhase.locked);
    expect(h.im.calls, isEmpty);
    expect(h.session.state.credentials, same(saved));
    await h.session.signOut().timeout(const Duration(seconds: 1));
    await rejected;
    expect(h.im.calls.where((c) => c.startsWith('connect:')), isEmpty);
  });
  test('incorrect PIN preserves locked restore and consumes a durable attempt; correct PIN connects once', () async {
    final h = AccessHarness();
    addTearDown(h.dispose);
    final saved = credentials('alice');
    h.credentialsStore.value = saved;
    h.policies.values[saved.accountKey] = policy();
    final restore = h.session.restore();
    await settleAccess();
    await expectLater(
      h.access.unlockPin('999999'),
      throwsA(isA<AccessFailure>()),
    );
    expect(h.policies.values[saved.accountKey]!.failures, 1);
    expect(h.im.calls, isEmpty);
    await h.access.unlockPin('123456');
    await restore;
    expect(h.session.state.phase, SessionPhase.online);
    expect(h.policies.values[saved.accountKey]!.failures, 0);
    expect(h.im.calls.where((c) => c.startsWith('connect:')).length, 1);
  });
  test('a concurrent unlock cannot treat the busy reservation as successful verification', () async {
    final h = AccessHarness();
    addTearDown(h.dispose);
    await h.start();
    await h.access.setPin('123456');
    h.access.lock();
    h.native.delayedPin = Completer<String>();
    final first = h.access.unlockPin('123456');
    await settleAccess();
    await expectLater(
      h.access.unlockPin('123456'),
      throwsA(isA<AccessFailure>()),
    );
    expect(h.access.unlocked, false);
    h.native.delayedPin!.complete(base64Encode(List.filled(32, 7)));
    await first;
    expect(h.access.unlocked, true);
  });
  test('five failed attempts persist cooldown across a new coordinator and reject before another derivation', () async {
    final h = AccessHarness();
    await h.start();
    await h.access.setPin('123456');
    h.access.lock();
    for (var i = 0; i < 5; i++) {
      await expectLater(
        h.access.unlockPin('999999'),
        throwsA(isA<AccessFailure>()),
      );
    }
    final saved = h.policies.values[credentials('alice').accountKey]!;
    await h.dispose();
    final next = AccessHarness();
    addTearDown(next.dispose);
    next.policies.values[credentials('alice').accountKey] = saved;
    await next.start();
    next.access.lock();
    await expectLater(
      next.access.unlockPin('123456'),
      throwsA(
        isA<AccessFailure>().having(
          (e) => e.kind,
          'kind',
          AccessFailureKind.cooldown,
        ),
      ),
    );
    expect(next.native.derives, 0);
  });
  test('the attempt is reserved before a delayed derivation and background cancellation cannot restore its budget', () async {
    final h = AccessHarness();
    addTearDown(h.dispose);
    await h.start();
    await h.access.setPin('123456');
    h.access.lock();
    h.native.delayedPin = Completer<String>();
    final attempt = h.access.unlockPin('123456');
    final rejected = expectLater(attempt, throwsA(isA<AccessFailure>()));
    await settleAccess();
    h.access.background(true);
    h.native.delayedPin!.complete(base64Encode(List.filled(32, 7)));
    await rejected;
    expect(h.policies.values[credentials('alice').accountKey]!.failures, 1);
    expect(h.access.unlocked, false);
  });
  test('lock and unlock preserve the server generation and connection but old interactive leases never revive', () async {
    final h = AccessHarness();
    addTearDown(h.dispose);
    await h.start();
    await h.access.setPin('123456');
    final old = SessionAccess(h.session),
        generation = h.session.state.generation;
    final nativeBefore = List<String>.of(h.im.calls);
    h.access.lock();
    expect(old.isCurrent, false);
    await h.access.unlockPin('123456');
    expect(old.isCurrent, false);
    expect(SessionAccess(h.session).isCurrent, true);
    expect(h.session.state.generation, generation);
    expect(h.im.calls, nativeBefore);
  });
  test(
    'late biometric success cannot unlock another owner after logout and login',
    () async {
      final h = AccessHarness();
      addTearDown(h.dispose);
      h.policies.values[credentials('alice').accountKey] = policy(
        biometric: true,
      );
      h.policies.values[credentials('bob').accountKey] = policy();
      await h.start();
      h.access.lock();
      h.native.delayedBiometric = Completer<void>();
      final attempt = h.access.unlockBiometric();
      final rejected = expectLater(attempt, throwsA(isA<AccessFailure>()));
      await settleAccess();
      h.access.background(true);
      await h.session.signOut();
      await h.start('bob');
      h.native.delayedBiometric!.complete();
      await rejected;
      expect(h.access.owner, credentials('bob').accountKey);
      expect(h.access.unlocked, false);
    },
  );
  test('corrupt policy fails closed before SDK connection without deleting saved credentials', () async {
    final h = AccessHarness();
    addTearDown(h.dispose);
    final saved = credentials('alice');
    h.credentialsStore.value = saved;
    h.policies.failRead = true;
    await expectLater(h.session.restore(), throwsA(isA<AppFailure>()));
    expect(h.im.calls, isEmpty);
    expect(h.credentialsStore.value, same(saved));
    expect(h.access.blocksContent, true);
  });
  test('biometric enable requires the configured PIN and persists only a completed challenge', () async {
    final h = AccessHarness();
    addTearDown(h.dispose);
    await h.start();
    await h.access.setPin('123456');
    await expectLater(
      h.access.setBiometric(true, '999999'),
      throwsA(isA<AccessFailure>()),
    );
    expect(h.native.enrollments, 0);
    h.native.biometricFailure = const AccessFailure(
      AccessFailureKind.cancelled,
    );
    await expectLater(
      h.access.setBiometric(true, '123456'),
      throwsA(isA<AccessFailure>()),
    );
    expect(h.access.biometricEnabled, false);
    h.native.biometricFailure = null;
    await h.access.setBiometric(true, '123456');
    expect(h.access.biometricEnabled, true);
  });
  test('PIN persistence failure cannot display an enabled lock or consume existing credentials', () async {
    final h = AccessHarness();
    addTearDown(h.dispose);
    await h.start();
    final saved = h.credentialsStore.value;
    h.policies.failWrite = true;
    await expectLater(h.access.setPin('123456'), throwsA(anything));
    expect(h.access.enabled, false);
    expect(h.credentialsStore.value, same(saved));
  });
  test('same user ID from different issuers uses separate policy owners and random salts', () async {
    final h = AccessHarness();
    addTearDown(h.dispose);
    await h.start();
    await h.access.setPin('123456');
    final alice = credentials('alice');
    await h.session.signOut();
    final other = SessionCredentials(
      userId: alice.userId,
      tenantId: alice.tenantId,
      tenantName: 'other',
      domains: ['one.example.com'],
      issuer: 'https://different.example/',
      chatToken: 'synthetic-chat',
      imToken: 'synthetic-im',
    );
    await h.session.signIn((_) async => other);
    expect(h.access.enabled, false);
    await h.access.setPin('123456');
    expect(h.policies.values.length, 2);
    expect(
      h.policies.values[alice.accountKey]!.salt,
      isNot(h.policies.values[other.accountKey]!.salt),
    );
  });
  test('slow old success write cannot overwrite a new failed attempt across lock and resume', () async {
    final h = AccessHarness();
    addTearDown(h.dispose);
    await h.start();
    await h.access.setPin('123456');
    h.access.lock();
    h.native.delayedPin = Completer<String>();
    final old = h.access.unlockPin('123456');
    final oldRejected = expectLater(old, throwsA(isA<AccessFailure>()));
    await settleAccess();
    final writeEntered = Completer<void>(), releaseWrite = Completer<void>();
    h.policies.beforeWrite = (_, _) async {
      h.policies.beforeWrite = null;
      writeEntered.complete();
      await releaseWrite.future;
    };
    h.native.delayedPin!.complete(base64Encode(List.filled(32, 7)));
    h.native.delayedPin = null;
    await writeEntered.future;
    h.access.background(true);
    h.access.background(false);
    final next = h.access.unlockPin('999999');
    final nextRejected = expectLater(next, throwsA(isA<AccessFailure>()));
    await settleAccess();
    releaseWrite.complete();
    await oldRejected;
    await nextRejected;
    expect(h.policies.values[credentials('alice').accountKey]!.failures, 1);
    expect(h.access.unlocked, false);
  });
  test(
    'same-owner login reads behind an old in-flight PIN replacement write',
    () async {
      final h = AccessHarness();
      addTearDown(h.dispose);
      await h.start();
      await h.access.setPin('123456');
      final entered = Completer<void>(), release = Completer<void>();
      h.policies.beforeWrite = (_, value) async {
        if (value.verifier == base64Encode(List.filled(32, 8))) {
          h.policies.beforeWrite = null;
          entered.complete();
          await release.future;
        }
      };
      final old = h.access.setPin('654321', currentPin: '123456');
      final rejected = expectLater(old, throwsA(isA<AccessFailure>()));
      await entered.future;
      await h.session.signOut().timeout(const Duration(seconds: 1));
      var loggedIn = false;
      final login = h.start().then((_) => loggedIn = true);
      await settleAccess();
      expect(loggedIn, false);
      release.complete();
      await rejected;
      await login;
      h.access.lock();
      await h.access.unlockPin('654321');
      expect(h.access.unlocked, true);
    },
  );
  test('an unrelated owner and explicit logout never wait for another owners policy write', () async {
    final h = AccessHarness();
    addTearDown(h.dispose);
    await h.start();
    await h.access.setPin('123456');
    final entered = Completer<void>(), release = Completer<void>();
    h.policies.beforeWrite = (_, value) async {
      if (value.verifier == base64Encode(List.filled(32, 8))) {
        h.policies.beforeWrite = null;
        entered.complete();
        await release.future;
      }
    };
    final old = h.access.setPin('654321', currentPin: '123456');
    final rejected = expectLater(old, throwsA(isA<AccessFailure>()));
    await entered.future;
    await h.session.signOut().timeout(const Duration(seconds: 1));
    await h.start('bob').timeout(const Duration(seconds: 1));
    expect(h.session.state.credentials!.userId, 'bob');
    release.complete();
    await rejected;
  });
  test('same-owner fresh login and logout do not wait for an old native derivation', () async {
    final h = AccessHarness();
    addTearDown(h.dispose);
    await h.start();
    await h.access.setPin('123456');
    h.access.lock();
    h.native.delayedPin = Completer<String>();
    final old = h.access.unlockPin('123456');
    final rejected = expectLater(old, throwsA(isA<AccessFailure>()));
    await settleAccess();
    await h.session.signOut().timeout(const Duration(seconds: 1));
    await h.start().timeout(const Duration(seconds: 1));
    expect(h.session.state.credentials!.userId, 'alice');
    h.native.delayedPin!.complete(base64Encode(List.filled(32, 7)));
    await rejected;
    expect(h.access.unlocked, true);
  });
  for (final stage in ['derive', 'write']) {
    test(
      'first PIN enable finishing after a background $stage remains locked on return',
      () async {
        final h = AccessHarness();
        addTearDown(h.dispose);
        await h.start();
        final entered = Completer<void>(), releaseWrite = Completer<void>();
        if (stage == 'derive') {
          h.native.delayedPin = Completer<String>();
        } else {
          h.policies.beforeWrite = (_, _) async {
            h.policies.beforeWrite = null;
            entered.complete();
            await releaseWrite.future;
          };
        }
        final enabling = h.access.setPin('123456');
        if (stage == 'write') {
          await entered.future;
        } else {
          await settleAccess();
        }
        h.access.background(true);
        h.access.background(false);
        if (stage == 'write') {
          releaseWrite.complete();
        } else {
          h.native.delayedPin!.complete(base64Encode(List.filled(32, 7)));
        }
        await enabling;
        expect(h.access.enabled, true);
        expect(h.access.unlocked, false);
      },
    );
  }
  test('line switching and a local PIN unlock never count as a fresh server login for PIN recovery', () async {
    final h = AccessHarness();
    addTearDown(h.dispose);
    await h.start();
    await h.access.setPin('123456');
    expect(h.access.canResetAfterLogin, true);
    await h.session.switchLine(1);
    expect(h.access.canResetAfterLogin, false);
    h.access.lock();
    await h.access.unlockPin('123456');
    await expectLater(
      h.access.setPin('654321', resetAfterLogin: true),
      throwsA(isA<AccessFailure>()),
    );
  });
}
