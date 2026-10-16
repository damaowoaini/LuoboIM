import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/error/app_failure.dart';
import 'package:imapp/features/chat/data/chat_reading_preferences.dart';

import 'chat_font_fixture.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  test('chat font absent preferences use standard and an acknowledged save survives a new reader', () async {
    final f = ChatFontFixture(); await f.start();
    try {
      expect(await f.preferences.read(), 1.0);
      await f.preferences.save(1.4, sourceCurrent: () => true);
      final reopened = ChatReadingPreferences(f.store, f.sdk.session);
      expect(await reopened.read(), 1.4);
      expect(f.writes.single.$1, ChatReadingPreferencesStore.keyFor(f.preferences.accountKey));
    } finally { await f.closeUnit(); }
  });
  for (final dimension in ['issuer', 'tenant']) {
    test('chat font same UID remains isolated when its $dimension changes', () async {
      final f = ChatFontFixture(); await f.start();
      try {
        final oldKey = f.preferences.accountKey;
        await f.preferences.save(1.5, sourceCurrent: () => true);
        final before = f.sdk.session.state.credentials!;
        await f.changeOwner(issuer: dimension == 'issuer' ? 'https://changed.example.com/' : before.issuer,
          tenant: dimension == 'tenant' ? 'changed-tenant' : before.tenantId);
        final next = ChatReadingPreferences(f.store, f.sdk.session);
        expect(next.accountKey, isNot(oldKey));
        expect(await next.read(), 1.0);
        expect(f.preferences.current, isFalse);
      } finally { await f.closeUnit(); }
    });
  }
  test('chat font delayed read cannot adopt the owner that logs in afterward', () async {
    final f = ChatFontFixture(); await f.start();
    f.readReply = Completer<double?>();
    try {
      final pending = f.preferences.read();
      final rejected = expectLater(pending, throwsA(isA<AppFailure>()));
      await Future<void>.delayed(Duration.zero);
      await f.changeOwner();
      f.readReply!.complete(1.8);
      await rejected;
      expect(f.preferences.current, isFalse);
    } finally { await f.closeUnit(); }
  });
  test('chat font queued save rechecks its original view before physical preferences dispatch', () async {
    final f = ChatFontFixture(); await f.start();
    f.readReply = Completer<double?>(); var active = true;
    try {
      final reading = f.preferences.read();
      final saving = f.preferences.save(1.7, sourceCurrent: () => active);
      final rejected = expectLater(saving, throwsA(isA<AppFailure>()));
      active = false; f.readReply!.complete(null);
      await reading; await rejected;
      expect(f.writes, isEmpty);
    } finally { await f.closeUnit(); }
  });
  test('chat font an already dispatched write remains on its frozen key after logout', () async {
    final f = ChatFontFixture(); await f.start();
    f.writeReply = Completer<void>(); f.writeEntered = Completer<void>();
    try {
      final key = f.preferences.accountKey;
      final saving = f.preferences.save(1.6, sourceCurrent: () => true);
      final rejected = expectLater(saving, throwsA(isA<AppFailure>()));
      await f.writeEntered!.future;
      await f.changeOwner(); f.writeReply!.complete(); await rejected;
      expect(f.writes, [(ChatReadingPreferencesStore.keyFor(key), 1.6)]);
      expect(await ChatReadingPreferences(f.store, f.sdk.session).read(), 1.0);
    } finally { await f.closeUnit(); }
  });
  test('chat font OS failure is distinct from absent data and a later explicit retry remains usable', () async {
    final f = ChatFontFixture(); await f.start();
    try {
      f.failRead = true;
      await expectLater(f.preferences.read(), throwsA(isA<AppFailure>()));
      f.failRead = false;
      expect(await f.preferences.read(), 1.0);
      f.failWrite = true;
      await expectLater(f.preferences.save(1.2, sourceCurrent: () => true), throwsA(isA<AppFailure>()));
      f.failWrite = false;
      await f.preferences.save(1.2, sourceCurrent: () => true);
      expect(await f.preferences.read(), 1.2);
    } finally { await f.closeUnit(); }
  });
  for (final invalid in [0.0, double.nan, double.infinity]) {
    test('chat font invalid stored factor $invalid does not become a successful standard read', () async {
      final f = ChatFontFixture(); await f.start();
      try {
        f.values[ChatReadingPreferencesStore.keyFor(f.preferences.accountKey)] = invalid;
        await expectLater(f.preferences.read(), throwsA(isA<AppFailure>()));
        expect(() => f.preferences.save(invalid, sourceCurrent: () => true), throwsA(isA<AppFailure>()));
        expect(f.writes, isEmpty);
      } finally { await f.closeUnit(); }
    });
  }
}
